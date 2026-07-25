// Package workspace: serializes the index to disk and loads it on startup.
// Entries are invalidated on file *content* hash (not mtime, which breaks
// across git checkouts); a cache-format version forces a full rebuild on
// upgrade (PRD FR-37..40).
package workspace

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
)

const cacheFormatVersion = "0.7.0"

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

// CacheExists reports whether a persisted cache file exists at path. The server
// calls it BEFORE a build to distinguish a cold first build (no prior cache)
// from a warm start (cache present), which the re-analysis counts alone cannot
// tell apart — a cold build and a fully-warm build both report zero stale files
// (feature 26, Story 1, warm-vs-rebuild signal).
func CacheExists(path string) bool {
	return cacheExists(path)
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
	data, err := encodeCache(cache)
	if err != nil {
		return fmt.Errorf("failed to encode cache: %w", err)
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

	data, err := encodeCache(cache)
	if err != nil {
		return fmt.Errorf("failed to encode cache: %w", err)
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

	cache, err := decodeCache(data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode cache: %w", err)
	}

	// Check version mismatch - return nil index and all files as stale
	if cache.Version != cacheFormatVersion {
		stale := make([]string, 0, len(cache.Entries))
		for path := range cache.Entries {
			// Canonical keyspace (ADR-027): staleMap in BuildWithCache is keyed by
			// the normalized relPath, so report stale keys normalized too.
			stale = append(stale, paths.NormalizeKey(path))
		}
		return nil, stale, nil
	}

	idx := &Index{entries: make(map[string]model.FileAnalysis)}
	var stale []string

	for path, entry := range cache.Entries {
		// Canonicalize the stored key to the forward-slash index keyspace
		// (ADR-027). A PRE-FIX cache written on Windows holds backslash keys
		// (e.g. "code\LIB1\MYSUB.NSN"); normalizing at load time makes the entry
		// land under the SAME canonical key the scan loop / currentHashes use, so
		// the hash lookup below HITS (warm hit if unchanged, or re-analyze) rather
		// than leaving an orphaned backslash entry that saveIndex would re-persist
		// forever (and that would double a flat-namespace object → spurious
		// ambiguity). Old cache upgrades in place — no forced full rebuild.
		key := paths.NormalizeKey(path)

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
		idx.Add(key, fa)

		// Check if content hash matches. currentHashes is keyed by the canonical
		// (forward-slash) relPath, so compare against the normalized key. A stale
		// entry is reported under its canonical key.
		if currentHash, ok := currentHashes[key]; ok {
			if currentHash != entry.ContentHash {
				stale = append(stale, key)
				if logger != nil {
					logger.Debug("cache: content hash mismatch", "path", key, "currentHash", currentHash, "storedHash", entry.ContentHash)
				}
			}
		}
	}

	return idx, stale, nil
}

// encodeCache encodes a CacheFile as compact (non-indented) JSON and compresses
// it with gzip (BestCompression). It returns the gzip-compressed bytes suitable
// for writing to disk, or an error if encoding or compression fails.
// Feature 24 / T1.
func encodeCache(cache CacheFile) ([]byte, error) {
	// Marshal to compact JSON (not indented)
	jsonBytes, err := json.Marshal(cache)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cache: %w", err)
	}

	// Compress with gzip at BestCompression
	var buf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}

	_, err = gw.Write(jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to write to gzip: %w", err)
	}

	// Close the gzip writer to flush
	err = gw.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// decodeCache decodes a byte stream produced by encodeCache. It detects gzip
// magic bytes (0x1f 0x8b) and gunzips if present; otherwise it treats the data
// as plaintext JSON (for backward compatibility with pre-gzip caches).
// It returns the decoded CacheFile or an error if decoding fails.
// Feature 24 / T1.
func decodeCache(data []byte) (CacheFile, error) {
	var jsonBytes []byte

	// Check for gzip magic bytes
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		// Decompress gzip
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return CacheFile{}, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gr.Close()

		var err2 error
		jsonBytes, err2 = io.ReadAll(gr)
		if err2 != nil {
			return CacheFile{}, fmt.Errorf("failed to read gzip data: %w", err2)
		}
	} else {
		// Plain JSON
		jsonBytes = data
	}

	// Unmarshal JSON
	var cache CacheFile
	err := json.Unmarshal(jsonBytes, &cache)
	if err != nil {
		return CacheFile{}, fmt.Errorf("failed to unmarshal cache: %w", err)
	}

	return cache, nil
}
