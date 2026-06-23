package workspace

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"natural-lsp/internal/model"
)

// TestSave_Load verifies that Save() and Load() provide a correct round-trip
// for the index. This tests FR-37 (persistent cache).
func TestSave_Load(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"serializes index to JSON file"},
		{"deserializes index from JSON file"},
		{"preserves all indexed files across round-trip"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index with known test data.
			idx := &Index{}
			idx.Add("program1.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Symbols: []model.SymbolEntry{
					{Name: "SUB1", Kind: model.SymbolProgram},
				},
				Edges: []model.EdgeEntry{
					{
						Kind:       model.EdgeCalls,
						TargetName: "program2.NSP",
					},
				},
			})
			idx.Add("program2.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Symbols: []model.SymbolEntry{
					{Name: "SUB2", Kind: model.SymbolProgram},
				},
				Edges: []model.EdgeEntry{},
			})

			// Save the index.
			err := Save(idx, cachePath)
			if err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			// Verify the cache file exists.
			if _, err := os.Stat(cachePath); os.IsNotExist(err) {
				t.Fatal("Save() did not create cache file")
			}

			// Load the index (no current hashes, so no stale files).
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify no stale files (cache is fresh).
			if len(stale) != 0 {
				t.Errorf("Load() returned %d stale files, want 0: %v", len(stale), stale)
			}

			// Verify loaded index is not nil.
			if loaded == nil {
				t.Fatal("Load() returned nil index")
			}

			// Verify all files are present.
			for path := range idx.entries {
				fa, ok := loaded.Get(path)
				if !ok {
					t.Errorf("Load() missing file %s", path)
					continue
				}
				original, _ := idx.Get(path)
				if fa.ObjectType != original.ObjectType {
					t.Errorf("Load() ObjectType for %s = %v, want %v", path, fa.ObjectType, original.ObjectType)
				}
				if len(fa.Symbols) != len(original.Symbols) {
					t.Errorf("Load() Symbols count for %s = %d, want %d", path, len(fa.Symbols), len(original.Symbols))
				}
				if len(fa.Edges) != len(original.Edges) {
					t.Errorf("Load() Edges count for %s = %d, want %d", path, len(fa.Edges), len(original.Edges))
				}
			}
		})
	}
}

// TestLoad_ContentHashInvalidation verifies that Load() correctly detects
// when a file's content has changed by comparing content hashes.
// This tests FR-38 (content-hash invalidation).
func TestLoad_ContentHashInvalidation(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"detects changed file content"},
		{"returns unchanged files as non-stale"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index with known test data.
			idx := &Index{}
			idx.Add("file1.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Symbols:    []model.SymbolEntry{{Name: "SYMBOL1", Kind: model.SymbolProgram}},
				Edges:      []model.EdgeEntry{{Kind: model.EdgeCalls, TargetName: "file2.NSP"}},
			})
			idx.Add("file2.NSP", model.FileAnalysis{
				ObjectType: model.ObjectSubprogram,
				Symbols:    []model.SymbolEntry{{Name: "SYMBOL2", Kind: model.SymbolProgram}},
				Edges:      []model.EdgeEntry{},
			})
			idx.Add("file3.NSP", model.FileAnalysis{
				ObjectType: model.ObjectCopycode,
				Symbols:    []model.SymbolEntry{},
				Edges:      []model.EdgeEntry{},
			})

			// Save the initial index.
			err := Save(idx, cachePath)
			if err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			// Modify file1.NSP's content in the index (simulating disk change).
			idx2 := &Index{}
			idx2.Add("file1.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Symbols:    []model.SymbolEntry{{Name: "SYMBOL1_CHANGED", Kind: model.SymbolProgram}}, // Changed content
				Edges:      []model.EdgeEntry{{Kind: model.EdgeCalls, TargetName: "file2.NSP"}},
			})
			idx2.Add("file2.NSP", model.FileAnalysis{
				ObjectType: model.ObjectSubprogram,
				Symbols:    []model.SymbolEntry{{Name: "SYMBOL2", Kind: model.SymbolProgram}},
				Edges:      []model.EdgeEntry{},
			})
			idx2.Add("file3.NSP", model.FileAnalysis{
				ObjectType: model.ObjectCopycode,
				Symbols:    []model.SymbolEntry{},
				Edges:      []model.EdgeEntry{},
			})

			// Load the cache - file1.NSP should be marked as stale.
			// Provide current hashes: file1.NSP has a different hash (changed),
			// file2.NSP and file3.NSP have matching hashes (unchanged).
			currentHashes := map[string]string{
				"file1.NSP": "different_hash_from_cache",
				"file2.NSP": fmt.Sprintf("%x", sha256.Sum256([]byte("file2.NSP"))),
				"file3.NSP": fmt.Sprintf("%x", sha256.Sum256([]byte("file3.NSP"))),
			}
			loaded, stale, err := Load(cachePath, currentHashes, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify file1.NSP is in the stale list.
			foundStale := false
			for _, s := range stale {
				if s == "file1.NSP" {
					foundStale = true
					break
				}
			}
			if !foundStale {
				t.Errorf("Load() did not mark file1.NSP as stale: %v", stale)
			}

			// Verify file2.NSP and file3.NSP are NOT stale.
			for _, path := range []string{"file2.NSP", "file3.NSP"} {
				for _, s := range stale {
					if s == path {
						t.Errorf("Load() incorrectly marked %s as stale", path)
					}
				}
			}

			// Verify loaded index is not nil.
			if loaded == nil {
				t.Fatal("Load() returned nil index")
			}
		})
	}
}

// TestLoad_FormatVersionMismatch verifies that Load() returns false when the
// cache format version doesn't match the expected version, forcing a full
// rebuild. This tests FR-39 (format-version gating).
func TestLoad_FormatVersionMismatch(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"returns false on version mismatch"},
		{"prevents use of incompatible cache"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index and save it.
			idx := &Index{}
			idx.Add("test.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
			})

			err := Save(idx, cachePath)
			if err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			// Manually corrupt the cache file by changing the version field.
			// Read the current cache content.
			content, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatalf("Failed to read cache file: %v", err)
			}

			// Replace the version string with an old version.
			oldVersion := "0.1.0"
			newContent := string(content)
			// The actual version string in the cache - replace it with an old one.
			newContent = strings.Replace(newContent, "0.2.0", oldVersion, 1)

			// Write the corrupted cache back.
			if err := os.WriteFile(cachePath, []byte(newContent), 0644); err != nil {
				t.Fatalf("Failed to write corrupted cache: %v", err)
			}

			// Try to load the cache - should return false due to version mismatch.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify Load() returns false (indicates stale/incompatible cache).
			if loaded != nil {
				t.Error("Load() returned non-nil index on version mismatch, want nil")
			}

			// Verify stale list contains all files (full rebuild required).
			if len(stale) == 0 {
				t.Error("Load() returned empty stale list on version mismatch, want all files")
			}
		})
	}
}
