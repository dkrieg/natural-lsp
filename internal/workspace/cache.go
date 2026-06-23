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
)

const cacheFormatVersion = "0.2.0"

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
	ContentHash string                  `json:"contentHash"`
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
			ObjectType: model.ObjectType(entry.ObjectType),
			Symbols:    entry.Symbols,
			Edges:      entry.Edges,
			DataAccess: entry.DataAccess,
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
