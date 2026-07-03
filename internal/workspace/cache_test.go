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
					{
						Kind:       model.EdgeNavigatesTo,
						TargetName: "BATCHJOB",
						Library:    "MYLIB",
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
				// Verify EdgeEntry.Library field survives round-trip (Decision 3)
				for i, edge := range fa.Edges {
					if i < len(original.Edges) {
						originalEdge := original.Edges[i]
						if edge.Library != originalEdge.Library {
							t.Errorf("Load() Edge[%d].Library for %s = %q, want %q", i, path, edge.Library, originalEdge.Library)
						}
					}
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

// TestSave_Load_DataAccessWithNameRange verifies that DataAccessEntry fields
// Name and NameRange are persisted correctly and survive Save/Load round-trip
// (Task 2 / FR-19, FR-20, OQ-3).
func TestSave_Load_DataAccessWithNameRange(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"persists Name field across round-trip"},
		{"persists NameRange field across round-trip"},
		{"NameRange is non-zero after load"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index with DataAccessEntry carrying both Name and NameRange.
			idx := &Index{}
			idx.Add("program.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				DataAccess: []model.DataAccessEntry{
					{
						Name: "EMPLOYEES",
						Kind: model.EdgeReads,
						Source: model.Range{
							Start: model.Position{Line: 10, Column: 1},
							End:   model.Position{Line: 10, Column: 16},
						},
						NameRange: model.Range{
							Start: model.Position{Line: 10, Column: 6},
							End:   model.Position{Line: 10, Column: 15},
						},
					},
					{
						Name: "VEHICLES",
						Kind: model.EdgeReads,
						Source: model.Range{
							Start: model.Position{Line: 12, Column: 1},
							End:   model.Position{Line: 12, Column: 34},
						},
						NameRange: model.Range{
							Start: model.Position{Line: 12, Column: 15},
							End:   model.Position{Line: 12, Column: 23},
						},
					},
					{
						Name: "EMPLOYEES",
						Kind: model.EdgeWrites,
						Source: model.Range{
							Start: model.Position{Line: 16, Column: 1},
							End:   model.Position{Line: 16, Column: 16},
						},
						NameRange: model.Range{
							Start: model.Position{Line: 16, Column: 7},
							End:   model.Position{Line: 16, Column: 16},
						},
					},
				},
			})

			// Save the index.
			err := Save(idx, cachePath)
			if err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			// Load the index.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			if loaded == nil {
				t.Fatal("Load() returned nil index")
			}

			if len(stale) != 0 {
				t.Errorf("Load() returned %d stale files, want 0: %v", len(stale), stale)
			}

			// Verify DataAccess entries round-trip correctly.
			fa, ok := loaded.Get("program.NSP")
			if !ok {
				t.Fatal("Load() missing file program.NSP")
			}

			if len(fa.DataAccess) != 3 {
				t.Fatalf("Load() DataAccess count = %d, want 3", len(fa.DataAccess))
			}

			// Check first entry: READ EMPLOYEES
			entry0 := fa.DataAccess[0]
			if entry0.Name != "EMPLOYEES" {
				t.Errorf("entry[0].Name = %q, want %q", entry0.Name, "EMPLOYEES")
			}
			if entry0.Kind != model.EdgeReads {
				t.Errorf("entry[0].Kind = %s, want %s", entry0.Kind, model.EdgeReads)
			}
			if entry0.NameRange.Start.Line == 0 {
				t.Error("entry[0].NameRange.Start.Line = 0 (zero-valued), want populated")
			}
			if entry0.NameRange.Start.Column == 0 {
				t.Error("entry[0].NameRange.Start.Column = 0 (zero-valued), want populated")
			}

			// Check second entry: READ VEHICLES
			entry1 := fa.DataAccess[1]
			if entry1.Name != "VEHICLES" {
				t.Errorf("entry[1].Name = %q, want %q", entry1.Name, "VEHICLES")
			}
			if entry1.NameRange.Start.Line == 0 {
				t.Error("entry[1].NameRange.Start.Line = 0 (zero-valued), want populated")
			}

			// Check third entry: STORE EMPLOYEES
			entry2 := fa.DataAccess[2]
			if entry2.Name != "EMPLOYEES" {
				t.Errorf("entry[2].Name = %q, want %q", entry2.Name, "EMPLOYEES")
			}
			if entry2.Kind != model.EdgeWrites {
				t.Errorf("entry[2].Kind = %s, want %s", entry2.Kind, model.EdgeWrites)
			}
			if entry2.NameRange.Start.Line == 0 {
				t.Error("entry[2].NameRange.Start.Line = 0 (zero-valued), want populated")
			}
		})
	}
}

// TestLoad_CacheVersionBumpedForDataAccessRefactoring verifies that a cache
// created before Task 2 (format version 0.3.0) is marked as stale when the
// version is bumped to 0.4.0 to accommodate the Name + NameRange refactoring.
// This test FAILS until the cache format version is bumped (GREEN phase / Task 2).
//
// Task 2 spec: "add NameRange Range; keep Source as the whole-statement range;
// rename File → Name for clarity... the cache-format version must bump 0.3.0 →
// 0.4.0 so stale caches rebuild."
//
// This test arranges a 0.3.0 cache with old-form DataAccessEntry (File field,
// no NameRange) and verifies that Load() at the new version treats it as stale.
func TestLoad_CacheVersionBumpedForDataAccessRefactoring(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"0.3.0 cache marked stale when version bumped to 0.4.0"},
		{"forces full rebuild on Name+NameRange migration"},
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

			// Manually downgrade the cache to version 0.3.0 (the pre-Task-2 version).
			// Read the current cache content.
			content, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatalf("Failed to read cache file: %v", err)
			}

			// Replace the current version string with 0.3.0 (pre-refactoring).
			newVersion := "0.3.0"
			newContent := string(content)
			newContent = strings.Replace(newContent, cacheFormatVersion, newVersion, 1)

			// Write the downgraded cache back.
			if err := os.WriteFile(cachePath, []byte(newContent), 0644); err != nil {
				t.Fatalf("Failed to write downgraded cache: %v", err)
			}

			// Try to load the cache - should treat it as stale due to version mismatch.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify Load() returns nil index (stale, version mismatch).
			if loaded != nil {
				t.Error("Load() returned non-nil index for 0.3.0 cache, want nil (stale)")
			}

			// Verify stale list contains the file (forces rebuild).
			if len(stale) == 0 {
				t.Error("Load() returned empty stale list for version mismatch, want all files marked stale")
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
			newContent = strings.Replace(newContent, cacheFormatVersion, oldVersion, 1)

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

// TestSave_Load_Definitions verifies that FileAnalysis.Definitions (including
// nested Children and ArrayDimensions) are persisted correctly through a cache
// Save→Load round-trip. This tests Task 12 (FR-21): data-definition persistence.
func TestSave_Load_Definitions(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"persists Definitions with nested Children across round-trip"},
		{"persists ArrayDimensions within Definitions"},
		{"preserves declaration order and hierarchy"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index with Definitions carrying nested Children and Dimensions.
			idx := &Index{}
			idx.Add("defs.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Definitions: []model.DataDefinition{
					{
						Name:        "EMPLOYEE",
						Level:       1,
						Type:        "GROUP",
						SectionKind: "local",
						Range: model.Range{
							Start: model.Position{Line: 5, Column: 3},
							End:   model.Position{Line: 15, Column: 30},
						},
						Children: []model.DataDefinition{
							{
								Name:  "EMP_ID",
								Level: 2,
								Type:  "N9",
								Range: model.Range{
									Start: model.Position{Line: 6, Column: 5},
									End:   model.Position{Line: 6, Column: 20},
								},
								Children:   nil,
								Dimensions: []model.ArrayDimension{},
							},
							{
								Name:  "EMP_SALARY",
								Level: 2,
								Type:  "N11.2",
								Range: model.Range{
									Start: model.Position{Line: 7, Column: 5},
									End:   model.Position{Line: 7, Column: 25},
								},
								Children:   nil,
								Dimensions: []model.ArrayDimension{},
							},
						},
						Dimensions: []model.ArrayDimension{},
					},
					{
						Name:        "VEHICLES",
						Level:       1,
						Type:        "A10",
						SectionKind: "local",
						Range: model.Range{
							Start: model.Position{Line: 18, Column: 3},
							End:   model.Position{Line: 18, Column: 30},
						},
						Children: nil,
						Dimensions: []model.ArrayDimension{
							{
								Lower:          1,
								Upper:          10,
								UpperUnbounded: false,
							},
						},
					},
					{
						Name:        "PARAM_NAME",
						Level:       1,
						Type:        "A20",
						SectionKind: "parameter",
						Range: model.Range{
							Start: model.Position{Line: 25, Column: 3},
							End:   model.Position{Line: 25, Column: 25},
						},
						Children:   nil,
						Dimensions: []model.ArrayDimension{},
					},
				},
			})

			// Save the index.
			err := Save(idx, cachePath)
			if err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			// Load the index.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			if loaded == nil {
				t.Fatal("Load() returned nil index")
			}

			if len(stale) != 0 {
				t.Errorf("Load() returned %d stale files, want 0: %v", len(stale), stale)
			}

			// Verify Definitions round-trip correctly.
			fa, ok := loaded.Get("defs.NSP")
			if !ok {
				t.Fatal("Load() missing file defs.NSP")
			}

			if len(fa.Definitions) != 3 {
				t.Fatalf("Load() Definitions count = %d, want 3", len(fa.Definitions))
			}

			// Check first definition: EMPLOYEE group with Children
			def0 := fa.Definitions[0]
			if def0.Name != "EMPLOYEE" {
				t.Errorf("def[0].Name = %q, want %q", def0.Name, "EMPLOYEE")
			}
			if def0.Level != 1 {
				t.Errorf("def[0].Level = %d, want 1", def0.Level)
			}
			if def0.Type != "GROUP" {
				t.Errorf("def[0].Type = %q, want %q", def0.Type, "GROUP")
			}
			if def0.SectionKind != "local" {
				t.Errorf("def[0].SectionKind = %q, want %q", def0.SectionKind, "local")
			}
			if len(def0.Children) != 2 {
				t.Errorf("def[0].Children count = %d, want 2", len(def0.Children))
			}

			// Verify nested children preserved
			if len(def0.Children) >= 2 {
				child0 := def0.Children[0]
				if child0.Name != "EMP_ID" {
					t.Errorf("def[0].Children[0].Name = %q, want %q", child0.Name, "EMP_ID")
				}
				if child0.Type != "N9" {
					t.Errorf("def[0].Children[0].Type = %q, want %q", child0.Type, "N9")
				}
				if child0.Level != 2 {
					t.Errorf("def[0].Children[0].Level = %d, want 2", child0.Level)
				}

				child1 := def0.Children[1]
				if child1.Name != "EMP_SALARY" {
					t.Errorf("def[0].Children[1].Name = %q, want %q", child1.Name, "EMP_SALARY")
				}
				if child1.Type != "N11.2" {
					t.Errorf("def[0].Children[1].Type = %q, want %q", child1.Type, "N11.2")
				}
			}

			// Check second definition: VEHICLES array
			def1 := fa.Definitions[1]
			if def1.Name != "VEHICLES" {
				t.Errorf("def[1].Name = %q, want %q", def1.Name, "VEHICLES")
			}
			if def1.Type != "A10" {
				t.Errorf("def[1].Type = %q, want %q", def1.Type, "A10")
			}
			if len(def1.Dimensions) != 1 {
				t.Errorf("def[1].Dimensions count = %d, want 1", len(def1.Dimensions))
			}
			if len(def1.Dimensions) >= 1 {
				dim := def1.Dimensions[0]
				if dim.Lower != 1 {
					t.Errorf("def[1].Dimensions[0].Lower = %d, want 1", dim.Lower)
				}
				if dim.Upper != 10 {
					t.Errorf("def[1].Dimensions[0].Upper = %d, want 10", dim.Upper)
				}
				if dim.UpperUnbounded {
					t.Errorf("def[1].Dimensions[0].UpperUnbounded = %v, want false", dim.UpperUnbounded)
				}
			}

			// Check third definition: PARAM_NAME parameter
			def2 := fa.Definitions[2]
			if def2.Name != "PARAM_NAME" {
				t.Errorf("def[2].Name = %q, want %q", def2.Name, "PARAM_NAME")
			}
			if def2.SectionKind != "parameter" {
				t.Errorf("def[2].SectionKind = %q, want %q", def2.SectionKind, "parameter")
			}
		})
	}
}

// TestSave_Load_WorkFiles verifies that FileAnalysis.WorkFiles (work-file definitions)
// are persisted correctly and survive cache Save→Load round-trip.
// This tests Task 15 (FR-22): work-file-definition persistence.
func TestSave_Load_WorkFiles(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"persists WorkFiles across round-trip"},
		{"preserves Number and Name fields"},
		{"preserves Range information"},
		{"rounds-trip literal and variable work-file names"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index with WorkFiles: one literal file name, one variable name.
			idx := &Index{}
			idx.Add("workfile-example.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				WorkFiles: []model.WorkFile{
					{
						Number: 1,
						Name:   "REPORT.TXT", // literal file name (quotes stripped)
						Range: model.Range{
							Start: model.Position{Line: 5, Column: 1},
							End:   model.Position{Line: 5, Column: 35},
						},
					},
					{
						Number: 2,
						Name:   "#DYNNAME", // variable name (verbatim with sigil)
						Range: model.Range{
							Start: model.Position{Line: 7, Column: 1},
							End:   model.Position{Line: 7, Column: 32},
						},
					},
				},
			})

			// Save the index.
			err := Save(idx, cachePath)
			if err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			// Load the index.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			if loaded == nil {
				t.Fatal("Load() returned nil index")
			}

			if len(stale) != 0 {
				t.Errorf("Load() returned %d stale files, want 0: %v", len(stale), stale)
			}

			// Verify WorkFiles entries round-trip correctly.
			fa, ok := loaded.Get("workfile-example.NSP")
			if !ok {
				t.Fatal("Load() missing file workfile-example.NSP")
			}

			if len(fa.WorkFiles) != 2 {
				t.Fatalf("Load() WorkFiles count = %d, want 2", len(fa.WorkFiles))
			}

			// Check first entry: literal file name
			wf0 := fa.WorkFiles[0]
			if wf0.Number != 1 {
				t.Errorf("workFiles[0].Number = %d, want 1", wf0.Number)
			}
			if wf0.Name != "REPORT.TXT" {
				t.Errorf("workFiles[0].Name = %q, want %q", wf0.Name, "REPORT.TXT")
			}
			if wf0.Range.Start.Line != 5 {
				t.Errorf("workFiles[0].Range.Start.Line = %d, want 5", wf0.Range.Start.Line)
			}
			if wf0.Range.Start.Column != 1 {
				t.Errorf("workFiles[0].Range.Start.Column = %d, want 1", wf0.Range.Start.Column)
			}
			if wf0.Range.End.Line != 5 {
				t.Errorf("workFiles[0].Range.End.Line = %d, want 5", wf0.Range.End.Line)
			}
			if wf0.Range.End.Column != 35 {
				t.Errorf("workFiles[0].Range.End.Column = %d, want 35", wf0.Range.End.Column)
			}

			// Check second entry: variable file name (with sigil)
			wf1 := fa.WorkFiles[1]
			if wf1.Number != 2 {
				t.Errorf("workFiles[1].Number = %d, want 2", wf1.Number)
			}
			if wf1.Name != "#DYNNAME" {
				t.Errorf("workFiles[1].Name = %q, want %q (dynamic name with sigil)", wf1.Name, "#DYNNAME")
			}
			if wf1.Range.Start.Line != 7 {
				t.Errorf("workFiles[1].Range.Start.Line = %d, want 7", wf1.Range.Start.Line)
			}

			// Verify literal vs variable names are both preserved and distinguishable
			if len(wf0.Name) > 0 && wf0.Name[0] == '#' {
				t.Errorf("workFiles[0].Name = %q, expected literal (no '#' prefix after round-trip)", wf0.Name)
			}
			if len(wf1.Name) == 0 || wf1.Name[0] != '#' {
				t.Errorf("workFiles[1].Name = %q, expected variable with '#' prefix after round-trip", wf1.Name)
			}
		})
	}
}
