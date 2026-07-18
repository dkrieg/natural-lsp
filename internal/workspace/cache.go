// Package workspace: serializes the index to disk and loads it on startup.
// Entries are invalidated on file *content* hash (not mtime, which breaks
// across git checkouts); a cache-format version forces a full rebuild on
// upgrade (PRD FR-37..40).
package workspace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"natural-lsp/internal/model"
	"os"
	"path/filepath"
)

const cacheFormatVersion = "0.6.0"

// CacheFile represents the on-disk cache format.
type CacheFile struct {
	Version string                `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

// cacheEntry holds the cached data for a single file, including its content hash.
type cacheEntry struct {
	ObjectType  string                  `json:"objectType"`
	Symbols     []model.SymbolEntry     `json:"symbols"`
	Edges       []model.EdgeEntry       `json:"edges"`
	DataAccess  []model.DataAccessEntry `json:"dataAccess"`
	Definitions []model.DataDefinition  `json:"definitions"`
	WorkFiles   []model.WorkFile        `json:"workFiles"`
	HostVarRefs []model.HostVarRef      `json:"hostVarRefs"`
	Structure   *model.Symbol           `json:"structure,omitempty"`
	ContentHash string                  `json:"contentHash"`
}

// cacheExists reports whether a regular cache file is present at path.
// Used to decide whether a fully-warm build (no re-analysis) still needs to
// write the cache (it does when the file is absent — e.g. it was created for
// the first time this session).
func cacheExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// saveIndex is the root-aware cache writer used by BuildWithCache. Index keys
// are workspace-relative paths; content hashes are computed from the file at
// root/relPath so the write is correct regardless of the process CWD (Save,
// by contrast, reads the key verbatim and is only correct when CWD == root —
// it is retained for the existing test round-trips). Feature 21 T12.
func saveIndex(idx *Index, root, cachePath string) error {
	entries := make(map[string]cacheEntry)
	idx.ForEach(func(relPath string, fa model.FileAnalysis) {
		content, err := os.ReadFile(filepath.Join(root, relPath))
		var hash string
		if err != nil {
			hash = fmt.Sprintf("%x", sha256.Sum256([]byte(relPath)))
		} else {
			hash = fmt.Sprintf("%x", sha256.Sum256(content))
		}
		entries[relPath] = cacheEntry{
			ObjectType:  string(fa.ObjectType),
			Symbols:     fa.Symbols,
			Edges:       fa.Edges,
			DataAccess:  fa.DataAccess,
			Definitions: fa.Definitions,
			WorkFiles:   fa.WorkFiles,
			HostVarRefs: fa.HostVarRefs,
			Structure:   fa.Structure,
			ContentHash: hash,
		}
	})

	cache := CacheFile{Version: cacheFormatVersion, Entries: entries}
	data, err := json.MarshalIndent(cache, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}
	return nil
}

// Save serializes the index to a JSON file at the given path.
func Save(idx *Index, path string) error {
	entries := make(map[string]cacheEntry)
	idx.ForEach(func(filePath string, fa model.FileAnalysis) {
		// Read file content to compute content hash
		content, err := os.ReadFile(filePath)
		var hash string
		if err != nil {
			// If we can't read the file, use a placeholder hash
			hash = fmt.Sprintf("%x", sha256.Sum256([]byte(filePath)))
		} else {
			hash = fmt.Sprintf("%x", sha256.Sum256(content))
		}
		entries[filePath] = cacheEntry{
			ObjectType:  string(fa.ObjectType),
			Symbols:     fa.Symbols,
			Edges:       fa.Edges,
			DataAccess:  fa.DataAccess,
			Definitions: fa.Definitions,
			WorkFiles:   fa.WorkFiles,
			HostVarRefs: fa.HostVarRefs,
			Structure:   fa.Structure,
			ContentHash: hash,
		}
	})

	cache := CacheFile{
		Version: cacheFormatVersion,
		Entries: entries,
	}

	data, err := json.MarshalIndent(cache, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}

// Load deserializes the index from a JSON file at the given path.
// It returns the loaded index, a list of stale files (whose content hash changed),
// and an error if the cache file cannot be read or parsed.
// currentHashes maps file paths to their current content hashes for invalidation check.
func Load(path string, currentHashes map[string]string, logger *slog.Logger) (*Index, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var cache CacheFile
	err = json.Unmarshal(data, &cache)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal cache: %w", err)
	}

	// Check version mismatch - return nil index and all files as stale
	if cache.Version != cacheFormatVersion {
		stale := make([]string, 0, len(cache.Entries))
		for path := range cache.Entries {
			stale = append(stale, path)
		}
		return nil, stale, nil
	}

	idx := &Index{entries: make(map[string]model.FileAnalysis)}
	var stale []string

	for path, entry := range cache.Entries {
		fa := model.FileAnalysis{
			ObjectType:  model.ObjectType(entry.ObjectType),
			Symbols:     entry.Symbols,
			Edges:       entry.Edges,
			DataAccess:  entry.DataAccess,
			Definitions: entry.Definitions,
			WorkFiles:   entry.WorkFiles,
			HostVarRefs: entry.HostVarRefs,
			Structure:   entry.Structure,
		}
		idx.Add(path, fa)

		// Check if content hash matches
		if currentHash, ok := currentHashes[path]; ok {
			if currentHash != entry.ContentHash {
				stale = append(stale, path)
				if logger != nil {
					logger.Debug("cache: content hash mismatch", "path", path, "currentHash", currentHash, "storedHash", entry.ContentHash)
				}
			}
		}
	}

	return idx, stale, nil
}
