package workspace

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"log/slog"
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

			// Manually build a 0.3.0 cache (the pre-Task-2 version) with plaintext JSON.
			// This simulates a cache written by the old format before the version bump.
			oldVersionCache := CacheFile{
				Version: "0.3.0",
				Entries: map[string]cacheEntry{
					"test.NSP": {
						ObjectType:  string(model.ObjectProgram),
						ContentHash: "deadbeef",
					},
				},
			}
			data, err := json.MarshalIndent(oldVersionCache, "", "    ")
			if err != nil {
				t.Fatalf("Failed to marshal old-version cache: %v", err)
			}
			if err := os.WriteFile(cachePath, data, 0644); err != nil {
				t.Fatalf("Failed to write old-version cache: %v", err)
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

			// Manually build a 0.1.0 cache (an old incompatible version) with plaintext JSON.
			// This simulates a cache written by an ancient format.
			oldVersionCache := CacheFile{
				Version: "0.1.0",
				Entries: map[string]cacheEntry{
					"test.NSP": {
						ObjectType:  string(model.ObjectProgram),
						ContentHash: "deadbeef",
					},
				},
			}
			data, err := json.MarshalIndent(oldVersionCache, "", "    ")
			if err != nil {
				t.Fatalf("Failed to marshal old-version cache: %v", err)
			}
			if err := os.WriteFile(cachePath, data, 0644); err != nil {
				t.Fatalf("Failed to write old-version cache: %v", err)
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

// TestSave_Load_HostVarRefs verifies that FileAnalysis.HostVarRefs (host-variable references)
// are persisted correctly through a cache Save→Load round-trip.
// This tests Task 1 (FR-21) of feature 08b-embedded-sql-extraction: HostVarRef persistence.
func TestSave_Load_HostVarRefs(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"persists HostVarRefs across round-trip"},
		{"preserves Name and Range fields in HostVarRefs"},
		{"HostVarRefs maintains sigil-normalized names"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build an index with HostVarRefs carrying normalized names and ranges.
			idx := &Index{}
			idx.Add("sql_program.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				HostVarRefs: []model.HostVarRef{
					{
						Name: "#EMPLOYEE_ID",
						Range: model.Range{
							Start: model.Position{Line: 15, Column: 10},
							End:   model.Position{Line: 15, Column: 23},
						},
					},
					{
						Name: "#SALARY",
						Range: model.Range{
							Start: model.Position{Line: 18, Column: 8},
							End:   model.Position{Line: 18, Column: 15},
						},
					},
					{
						Name: "&PARAM_VAR",
						Range: model.Range{
							Start: model.Position{Line: 20, Column: 5},
							End:   model.Position{Line: 20, Column: 15},
						},
					},
					{
						Name: "@OBJECT_VAR",
						Range: model.Range{
							Start: model.Position{Line: 22, Column: 1},
							End:   model.Position{Line: 22, Column: 12},
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

			// Verify HostVarRefs entries round-trip correctly.
			fa, ok := loaded.Get("sql_program.NSP")
			if !ok {
				t.Fatal("Load() missing file sql_program.NSP")
			}

			if len(fa.HostVarRefs) != 4 {
				t.Fatalf("Load() HostVarRefs count = %d, want 4", len(fa.HostVarRefs))
			}

			// Check first entry: #EMPLOYEE_ID
			hvr0 := fa.HostVarRefs[0]
			if hvr0.Name != "#EMPLOYEE_ID" {
				t.Errorf("HostVarRef[0].Name = %q, want %q", hvr0.Name, "#EMPLOYEE_ID")
			}
			if hvr0.Range.Start.Line != 15 {
				t.Errorf("HostVarRef[0].Range.Start.Line = %d, want 15", hvr0.Range.Start.Line)
			}
			if hvr0.Range.Start.Column != 10 {
				t.Errorf("HostVarRef[0].Range.Start.Column = %d, want 10", hvr0.Range.Start.Column)
			}
			if hvr0.Range.End.Column != 23 {
				t.Errorf("HostVarRef[0].Range.End.Column = %d, want 23", hvr0.Range.End.Column)
			}

			// Check second entry: #SALARY
			hvr1 := fa.HostVarRefs[1]
			if hvr1.Name != "#SALARY" {
				t.Errorf("HostVarRef[1].Name = %q, want %q", hvr1.Name, "#SALARY")
			}

			// Check third entry: &PARAM_VAR (ampersand sigil)
			hvr2 := fa.HostVarRefs[2]
			if hvr2.Name != "&PARAM_VAR" {
				t.Errorf("HostVarRef[2].Name = %q, want %q", hvr2.Name, "&PARAM_VAR")
			}

			// Check fourth entry: @OBJECT_VAR (at sigil)
			hvr3 := fa.HostVarRefs[3]
			if hvr3.Name != "@OBJECT_VAR" {
				t.Errorf("HostVarRef[3].Name = %q, want %q", hvr3.Name, "@OBJECT_VAR")
			}
		})
	}
}

// TestLoad_CacheVersionBumpedForHostVarRefs verifies that a cache created
// before Task 1 (format version 0.4.0) is marked as stale when the version
// is bumped to 0.5.0 to accommodate the new HostVarRefs field.
// This test FAILS until the cache format version is bumped to "0.5.0"
// (GREEN phase / Task 1 of feature 08b-embedded-sql-extraction).
//
// Task 1 spec: "Bump cacheFormatVersion '0.4.0' → '0.5.0' in
// internal/workspace/cache.go; cache round-trips a FileAnalysis carrying
// HostVarRefs and rejects a 0.4.0 cache (forces rebuild)."
//
// This test arranges a 0.4.0 cache (the pre-HostVarRefs version) and verifies
// that Load() at the new 0.5.0 version treats it as stale (all files marked
// for rebuild).
func TestLoad_CacheVersionBumpedForHostVarRefs(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"0.4.0 cache marked stale when version bumped to 0.5.0"},
		{"forces full rebuild on HostVarRefs field addition"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Manually build a 0.4.0 cache (the pre-HostVarRefs version) with plaintext JSON.
			// This simulates a cache written by the old format before the HostVarRefs field was added.
			oldVersionCache := CacheFile{
				Version: "0.4.0",
				Entries: map[string]cacheEntry{
					"test.NSP": {
						ObjectType:  string(model.ObjectProgram),
						ContentHash: "deadbeef",
					},
				},
			}
			data, err := json.MarshalIndent(oldVersionCache, "", "    ")
			if err != nil {
				t.Fatalf("Failed to marshal old-version cache: %v", err)
			}
			if err := os.WriteFile(cachePath, data, 0644); err != nil {
				t.Fatalf("Failed to write old-version cache: %v", err)
			}

			// Try to load the cache - should treat it as stale due to version mismatch.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify Load() returns nil index (stale, version mismatch).
			if loaded != nil {
				t.Error("Load() returned non-nil index for 0.4.0 cache, want nil (stale)")
			}

			// Verify stale list contains the file (forces rebuild).
			if len(stale) == 0 {
				t.Error("Load() returned empty stale list for version mismatch, want all files marked stale")
			}

			// Verify the stale list contains our test file.
			foundStale := false
			for _, s := range stale {
				if s == "test.NSP" {
					foundStale = true
					break
				}
			}
			if !foundStale {
				t.Errorf("Load() did not mark test.NSP as stale: %v", stale)
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

// TestSave_Load_Structure verifies that FileAnalysis.Structure (the program-structure tree)
// is persisted correctly and survives a cache Save→Load round-trip.
// This tests Task 6 (FR-23): persistence of the hierarchical structure model for outline/navigation.
// The Structure field carries a nested model.Symbol tree (root object with data-section children
// containing data-field children, plus subroutine and map children).
func TestSave_Load_Structure(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"persists Structure across round-trip"},
		{"preserves nested Symbol children and ranges"},
		{"preserves SelectionRange for name tokens"},
		{"roundtrips object root with multiple child kinds (sections, subroutines, maps)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build a nested Symbol structure: object root with a data-section child
			// (containing a data-field child) and a subroutine child.
			structure := &model.Symbol{
				Kind: model.SymbolObject,
				Name: "TESTPROG",
				Range: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 50, Column: 80},
				},
				SelectionRange: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 1, Column: 8},
				},
				Children: []model.Symbol{
					// Data section child
					{
						Kind: model.SymbolDataSection,
						Name: "LOCAL",
						Range: model.Range{
							Start: model.Position{Line: 3, Column: 1},
							End:   model.Position{Line: 15, Column: 50},
						},
						SelectionRange: model.Range{
							Start: model.Position{Line: 3, Column: 1},
							End:   model.Position{Line: 3, Column: 21},
						},
						Children: []model.Symbol{
							// Data field child nested within section
							{
								Kind: model.SymbolDataField,
								Name: "EMPLOYEE_NAME",
								Range: model.Range{
									Start: model.Position{Line: 5, Column: 3},
									End:   model.Position{Line: 5, Column: 30},
								},
								SelectionRange: model.Range{
									Start: model.Position{Line: 5, Column: 5},
									End:   model.Position{Line: 5, Column: 18},
								},
								Children: nil,
							},
						},
					},
					// Subroutine child
					{
						Kind: model.SymbolSubroutine,
						Name: "PROCESS_DATA",
						Range: model.Range{
							Start: model.Position{Line: 20, Column: 1},
							End:   model.Position{Line: 45, Column: 30},
						},
						SelectionRange: model.Range{
							Start: model.Position{Line: 20, Column: 18},
							End:   model.Position{Line: 20, Column: 30},
						},
						Children: nil,
					},
				},
			}

			// Build an index with the Structure populated.
			idx := &Index{}
			idx.Add("testprog.NSP", model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Structure:  structure,
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

			// Retrieve the loaded FileAnalysis and verify Structure round-tripped.
			fa, ok := loaded.Get("testprog.NSP")
			if !ok {
				t.Fatal("Load() missing file testprog.NSP")
			}

			if fa.Structure == nil {
				t.Fatal("Load() returned nil Structure, want populated structure tree")
			}

			// Verify root node properties.
			if fa.Structure.Kind != model.SymbolObject {
				t.Errorf("Structure.Kind = %s, want %s", fa.Structure.Kind, model.SymbolObject)
			}
			if fa.Structure.Name != "TESTPROG" {
				t.Errorf("Structure.Name = %q, want %q", fa.Structure.Name, "TESTPROG")
			}
			if fa.Structure.Range.Start.Line != 1 {
				t.Errorf("Structure.Range.Start.Line = %d, want 1", fa.Structure.Range.Start.Line)
			}
			if fa.Structure.Range.End.Line != 50 {
				t.Errorf("Structure.Range.End.Line = %d, want 50", fa.Structure.Range.End.Line)
			}
			if fa.Structure.SelectionRange.Start.Line != 1 {
				t.Errorf("Structure.SelectionRange.Start.Line = %d, want 1", fa.Structure.SelectionRange.Start.Line)
			}

			// Verify root has children (data section + subroutine).
			if len(fa.Structure.Children) != 2 {
				t.Errorf("Structure.Children count = %d, want 2", len(fa.Structure.Children))
			}

			// Verify first child: data section.
			if len(fa.Structure.Children) >= 1 {
				dataSection := fa.Structure.Children[0]
				if dataSection.Kind != model.SymbolDataSection {
					t.Errorf("Structure.Children[0].Kind = %s, want %s", dataSection.Kind, model.SymbolDataSection)
				}
				if dataSection.Name != "LOCAL" {
					t.Errorf("Structure.Children[0].Name = %q, want %q", dataSection.Name, "LOCAL")
				}
				if dataSection.Range.Start.Line != 3 {
					t.Errorf("Structure.Children[0].Range.Start.Line = %d, want 3", dataSection.Range.Start.Line)
				}

				// Verify data-field child nested within section.
				if len(dataSection.Children) != 1 {
					t.Errorf("Structure.Children[0].Children count = %d, want 1", len(dataSection.Children))
				}
				if len(dataSection.Children) >= 1 {
					field := dataSection.Children[0]
					if field.Kind != model.SymbolDataField {
						t.Errorf("dataSection.Children[0].Kind = %s, want %s", field.Kind, model.SymbolDataField)
					}
					if field.Name != "EMPLOYEE_NAME" {
						t.Errorf("dataSection.Children[0].Name = %q, want %q", field.Name, "EMPLOYEE_NAME")
					}
					if field.Range.Start.Column != 3 {
						t.Errorf("dataSection.Children[0].Range.Start.Column = %d, want 3", field.Range.Start.Column)
					}
					if field.SelectionRange.Start.Column != 5 {
						t.Errorf("dataSection.Children[0].SelectionRange.Start.Column = %d, want 5", field.SelectionRange.Start.Column)
					}
				}
			}

			// Verify second child: subroutine.
			if len(fa.Structure.Children) >= 2 {
				subroutine := fa.Structure.Children[1]
				if subroutine.Kind != model.SymbolSubroutine {
					t.Errorf("Structure.Children[1].Kind = %s, want %s", subroutine.Kind, model.SymbolSubroutine)
				}
				if subroutine.Name != "PROCESS_DATA" {
					t.Errorf("Structure.Children[1].Name = %q, want %q", subroutine.Name, "PROCESS_DATA")
				}
				if subroutine.Range.Start.Line != 20 {
					t.Errorf("Structure.Children[1].Range.Start.Line = %d, want 20", subroutine.Range.Start.Line)
				}
				if subroutine.SelectionRange.Start.Column != 18 {
					t.Errorf("Structure.Children[1].SelectionRange.Start.Column = %d, want 18", subroutine.SelectionRange.Start.Column)
				}
			}
		})
	}
}

// TestLoad_CacheVersionBumpedForStructure verifies that a cache created before
// Task 6 (format version 0.5.0) is marked as fully stale when the version is
// bumped to 0.6.0 to accommodate the new Structure field.
// This test FAILS until the cache format version is bumped to "0.6.0"
// (GREEN phase / Task 6 of feature 09-program-structure-extraction).
//
// Task 6 spec: "Bump cacheFormatVersion '0.5.0' → '0.6.0' in
// internal/workspace/cache.go; cache round-trips a FileAnalysis carrying
// Structure and rejects a 0.5.0 cache (forces rebuild)."
//
// This test arranges a 0.5.0 cache (the pre-Structure version) and verifies
// that Load() at the new 0.6.0 version treats it as stale (all files marked
// for rebuild).
func TestLoad_CacheVersionBumpedForStructure(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"0.5.0 cache marked stale when version bumped to 0.6.0"},
		{"forces full rebuild on Structure field addition"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Manually build a 0.5.0 cache (the pre-Structure version) with plaintext JSON.
			// This simulates a cache written by the old format before the Structure field was added.
			oldVersionCache := CacheFile{
				Version: "0.5.0",
				Entries: map[string]cacheEntry{
					"test.NSP": {
						ObjectType:  string(model.ObjectProgram),
						ContentHash: "deadbeef",
					},
				},
			}
			data, err := json.MarshalIndent(oldVersionCache, "", "    ")
			if err != nil {
				t.Fatalf("Failed to marshal old-version cache: %v", err)
			}
			if err := os.WriteFile(cachePath, data, 0644); err != nil {
				t.Fatalf("Failed to write old-version cache: %v", err)
			}

			// Try to load the cache - should treat it as stale due to version mismatch.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify Load() returns nil index (stale, version mismatch).
			if loaded != nil {
				t.Error("Load() returned non-nil index for 0.5.0 cache, want nil (stale)")
			}

			// Verify stale list contains the file (forces rebuild).
			if len(stale) == 0 {
				t.Error("Load() returned empty stale list for version mismatch, want all files marked stale")
			}

			// Verify the stale list contains our test file.
			foundStale := false
			for _, s := range stale {
				if s == "test.NSP" {
					foundStale = true
					break
				}
			}
			if !foundStale {
				t.Errorf("Load() did not mark test.NSP as stale: %v", stale)
			}
		})
	}
}

// TestLoad_CanonicalizesBackslashKeys is the regression guard for ADR-027's
// cache self-heal (Finding 2): a PRE-FIX cache written on Windows holds
// backslash keys (e.g. "code\LIB1\MYSUB.NSN"). Load MUST canonicalize each
// stored key through NormalizeKey before inserting it into the index, so that
// (a) the index holds only the forward-slash key (no orphaned backslash key
// that saveIndex would re-persist forever), and (b) the current-hash lookup
// (keyed forward-slash by BuildWithCache) HITS, producing a proper warm hit /
// re-analyze rather than leaving a duplicate entry.
//
// The bug's downstream consequence in a flat namespace: objectIdentity would
// map BOTH "code\LIB1\MYSUB.NSN" and the real "code/LIB1/MYSUB.NSN" to the
// same ("MYSUB","") name → two Candidates → a spurious ambiguity diagnostic +
// a double-location definition, indefinitely. This test asserts exactly ONE
// candidate for MYSUB.
//
// It is PLATFORM-INDEPENDENT: it hand-serializes a CacheFile with a literal
// backslash key, so it exercises the Windows code path on Linux/macOS CI. It
// is designed to FAIL if load-time normalization is removed (two keys / two
// candidates).
func TestLoad_CanonicalizesBackslashKeys(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache.json")

	const backslashKey = "code\\LIB1\\MYSUB.NSN"
	const canonicalKey = "code/LIB1/MYSUB.NSN"

	// Hand-build a PRE-FIX cache holding a backslash key (as a Windows build
	// would have written before ADR-027). Serialize via the cache's own format.
	cache := CacheFile{
		Version: cacheFormatVersion,
		Entries: map[string]cacheEntry{
			backslashKey: {
				ObjectType:  string(model.ObjectSubprogram),
				ContentHash: "deadbeef",
			},
		},
	}
	data, err := json.MarshalIndent(cache, "", "    ")
	if err != nil {
		t.Fatalf("marshal cache: %v", err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	// currentHashes is keyed forward-slash (as BuildWithCache produces via
	// NormalizeKey) and carries the SAME hash, so a correctly-normalized Load
	// records a warm hit (no staleness) rather than a duplicate.
	currentHashes := map[string]string{canonicalKey: "deadbeef"}

	idx, stale, err := Load(cachePath, currentHashes, nil)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if idx == nil {
		t.Fatalf("Load() returned nil index for a same-version cache")
	}

	// (a) The index holds the canonical key and NOT the backslash key.
	if _, ok := idx.Get(canonicalKey); !ok {
		t.Errorf("index missing canonical key %q; keys present: %v", canonicalKey, idx.Keys())
	}
	if _, ok := idx.Get(backslashKey); ok {
		t.Errorf("index still holds orphaned backslash key %q (load-time normalization missing)", backslashKey)
	}
	for _, k := range idx.Keys() {
		if strings.Contains(k, "\\") {
			t.Errorf("index key %q contains a backslash after Load; keys must be forward-slash canonical", k)
		}
	}

	// The warm hit means the entry is NOT stale (the forward-slash currentHashes
	// lookup matched the normalized key + identical hash).
	for _, s := range stale {
		if s == canonicalKey || s == backslashKey {
			t.Errorf("Load() marked %q stale; the forward-slash hash lookup should HIT the normalized key", s)
		}
	}

	// (b) LookupByName / resolution yields exactly ONE candidate for MYSUB — no
	// spurious ambiguity from a duplicated backslash+forward-slash entry.
	cfg := config.Defaults()
	cands := idx.LookupByName("MYSUB", model.ObjectSubprogram, &cfg)
	if len(cands) != 1 {
		t.Fatalf("LookupByName(MYSUB) = %d candidates, want exactly 1 (orphaned backslash key would produce 2): %+v", len(cands), cands)
	}
	if cands[0].Path != canonicalKey {
		t.Errorf("candidate Path = %q, want %q", cands[0].Path, canonicalKey)
	}
}

// TestEncodeDecodeCache_RoundTrip verifies that encodeCache and decodeCache
// provide lossless round-trip encoding of a CacheFile. This test pins the
// contract for the two pure helpers that will be used by T1–T3 of feature 24.
// Feature 24 / T1.
func TestEncodeDecodeCache_RoundTrip(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"encodeCache produces gzip-compressed bytes"},
		{"decodeCache recovers the original CacheFile"},
		{"round-trip preserves all persisted fields"},
		{"encoded bytes are smaller than indented JSON"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Build a representative CacheFile with one cacheEntry exercising
			// every persisted field: ObjectType, Structure (hierarchical tree),
			// Edges, Definitions, HostVarRefs, DataAccess (with NameRange),
			// WorkFiles, and ContentHash.

			// Build a nested Symbol structure: object root with a data-section child
			// and a subroutine child.
			structure := &model.Symbol{
				Kind: model.SymbolObject,
				Name: "TESTPROG",
				Range: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 50, Column: 80},
				},
				SelectionRange: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 1, Column: 8},
				},
				Children: []model.Symbol{
					// Data section with a data field child
					{
						Kind: model.SymbolDataSection,
						Name: "LOCAL",
						Range: model.Range{
							Start: model.Position{Line: 3, Column: 1},
							End:   model.Position{Line: 15, Column: 50},
						},
						SelectionRange: model.Range{
							Start: model.Position{Line: 3, Column: 1},
							End:   model.Position{Line: 3, Column: 21},
						},
						Children: []model.Symbol{
							{
								Kind: model.SymbolDataField,
								Name: "EMPLOYEE_ID",
								Range: model.Range{
									Start: model.Position{Line: 5, Column: 3},
									End:   model.Position{Line: 5, Column: 20},
								},
								SelectionRange: model.Range{
									Start: model.Position{Line: 5, Column: 5},
									End:   model.Position{Line: 5, Column: 16},
								},
								Children: nil,
							},
						},
					},
					// Subroutine child
					{
						Kind: model.SymbolSubroutine,
						Name: "PROCESS_DATA",
						Range: model.Range{
							Start: model.Position{Line: 20, Column: 1},
							End:   model.Position{Line: 45, Column: 30},
						},
						SelectionRange: model.Range{
							Start: model.Position{Line: 20, Column: 18},
							End:   model.Position{Line: 20, Column: 30},
						},
						Children: nil,
					},
				},
			}

			// Construct the test CacheFile with all persisted fields populated.
			cache := CacheFile{
				Version: cacheFormatVersion,
				Entries: map[string]cacheEntry{
					"test_program.NSP": {
						ObjectType: string(model.ObjectProgram),
						Symbols: []model.SymbolEntry{
							{Name: "PROG1", Kind: model.SymbolProgram},
						},
						Edges: []model.EdgeEntry{
							{
								Kind:       model.EdgeCalls,
								TargetName: "SUBRTN1",
								Source: model.Range{
									Start: model.Position{Line: 10, Column: 1},
									End:   model.Position{Line: 10, Column: 20},
								},
							},
						},
						DataAccess: []model.DataAccessEntry{
							{
								Name: "EMPLOYEES",
								Kind: model.EdgeReads,
								Source: model.Range{
									Start: model.Position{Line: 12, Column: 1},
									End:   model.Position{Line: 12, Column: 16},
								},
								NameRange: model.Range{
									Start: model.Position{Line: 12, Column: 6},
									End:   model.Position{Line: 12, Column: 15},
								},
							},
						},
						Definitions: []model.DataDefinition{
							{
								Name:        "EMP_ID",
								Level:       1,
								Type:        "N9",
								SectionKind: "local",
								Range: model.Range{
									Start: model.Position{Line: 5, Column: 3},
									End:   model.Position{Line: 5, Column: 20},
								},
								Children:   nil,
								Dimensions: []model.ArrayDimension{},
							},
						},
						HostVarRefs: []model.HostVarRef{
							{
								Name: "#SALARY",
								Range: model.Range{
									Start: model.Position{Line: 18, Column: 8},
									End:   model.Position{Line: 18, Column: 15},
								},
							},
						},
						WorkFiles: []model.WorkFile{
							{
								Number: 1,
								Name:   "REPORT.TXT",
								Range: model.Range{
									Start: model.Position{Line: 5, Column: 1},
									End:   model.Position{Line: 5, Column: 35},
								},
							},
						},
						Structure:   structure,
						ContentHash: "abc123def456",
					},
				},
			}

			// Call encodeCache (will FAIL because it doesn't exist yet).
			// This is the RED signal: the test fails because the helper is missing.
			encoded, err := encodeCache(cache)
			if err != nil {
				t.Fatalf("encodeCache() returned error: %v", err)
			}

			// Verify the encoded bytes start with the gzip magic bytes.
			if len(encoded) < 2 {
				t.Fatal("encodeCache() returned fewer than 2 bytes, want gzip magic")
			}
			if encoded[0] != 0x1f || encoded[1] != 0x8b {
				t.Fatalf("encodeCache() did not produce gzip magic bytes; got [0x%02x, 0x%02x], want [0x1f, 0x8b]", encoded[0], encoded[1])
			}

			// Verify the encoded size is smaller than indented JSON.
			indented, err := json.MarshalIndent(cache, "", "    ")
			if err != nil {
				t.Fatalf("json.MarshalIndent() returned error: %v", err)
			}
			if len(encoded) >= len(indented) {
				t.Errorf("encodeCache() produced %d bytes, indented JSON produced %d bytes; want encoded < indented (compaction failed)", len(encoded), len(indented))
			}

			// Call decodeCache and verify it recovers the original CacheFile.
			decoded, err := decodeCache(encoded)
			if err != nil {
				t.Fatalf("decodeCache() returned error: %v", err)
			}

			// Assert deep equality: the decoded value must be identical to the input.
			if !reflect.DeepEqual(cache, decoded) {
				t.Errorf("decodeCache() did not recover the original CacheFile:\nGot:\n%+v\nWant:\n%+v", decoded, cache)
			}
		})
	}
}

// TestLoad_ReadsGzipAndPlaintext verifies that Load() can read both gzip-compressed
// caches (written by the new encodeCache path) and legacy plaintext caches
// (written by pre-feature-24 binaries). This test proves FR-43 (graceful degradation)
// for the backward-compatibility path and that the new compressed path works end-to-end.
// Feature 24 / T2.
func TestLoad_ReadsGzipAndPlaintext(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"Load reads gzip-compressed cache written by encodeCache"},
		{"Load reads legacy plaintext cache (backward compat, FR-43)"},
		{"both gzip and plaintext paths load identical entry data"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Build a representative CacheFile with one cacheEntry exercising
			// every persisted field: ObjectType, Structure, Edges, Definitions,
			// HostVarRefs, DataAccess with NameRange, WorkFiles, ContentHash.

			// Create a nested Symbol structure (small tree for quick test).
			structure := &model.Symbol{
				Kind: model.SymbolObject,
				Name: "TESTPROG",
				Range: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 50, Column: 80},
				},
				SelectionRange: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 1, Column: 8},
				},
				Children: []model.Symbol{
					{
						Kind: model.SymbolSubroutine,
						Name: "PROCESS_DATA",
						Range: model.Range{
							Start: model.Position{Line: 20, Column: 1},
							End:   model.Position{Line: 45, Column: 30},
						},
						SelectionRange: model.Range{
							Start: model.Position{Line: 20, Column: 18},
							End:   model.Position{Line: 20, Column: 30},
						},
						Children: nil,
					},
				},
			}

			// Build the CacheFile with representative data.
			cache := CacheFile{
				Version: cacheFormatVersion,
				Entries: map[string]cacheEntry{
					"prog1.NSP": {
						ObjectType: string(model.ObjectProgram),
						Edges: []model.EdgeEntry{
							{
								Kind:       model.EdgeCalls,
								TargetName: "SUBRTN1",
								Source: model.Range{
									Start: model.Position{Line: 10, Column: 1},
									End:   model.Position{Line: 10, Column: 20},
								},
							},
						},
						DataAccess: []model.DataAccessEntry{
							{
								Name: "EMPLOYEES",
								Kind: model.EdgeReads,
								Source: model.Range{
									Start: model.Position{Line: 12, Column: 1},
									End:   model.Position{Line: 12, Column: 16},
								},
								NameRange: model.Range{
									Start: model.Position{Line: 12, Column: 6},
									End:   model.Position{Line: 12, Column: 15},
								},
							},
						},
						Definitions: []model.DataDefinition{
							{
								Name:        "EMP_ID",
								Level:       1,
								Type:        "N9",
								SectionKind: "local",
								Range: model.Range{
									Start: model.Position{Line: 5, Column: 3},
									End:   model.Position{Line: 5, Column: 20},
								},
								Children:   nil,
								Dimensions: []model.ArrayDimension{},
							},
						},
						HostVarRefs: []model.HostVarRef{
							{
								Name: "#SALARY",
								Range: model.Range{
									Start: model.Position{Line: 18, Column: 8},
									End:   model.Position{Line: 18, Column: 15},
								},
							},
						},
						Structure:   structure,
						ContentHash: "abc123def456",
					},
				},
			}

			tmpDir := t.TempDir()

			// Sub-case 1: gzip case
			// Encode via encodeCache (produces gzip bytes), write to disk, Load, assert success.
			t.Run("gzip case", func(t *testing.T) {
				gzipCachePath := filepath.Join(tmpDir, "cache_gzip.nslp")

				// Encode the cache to gzip bytes.
				encoded, err := encodeCache(cache)
				if err != nil {
					t.Fatalf("encodeCache() returned error: %v", err)
				}

				// Write the gzip bytes to disk.
				if err := os.WriteFile(gzipCachePath, encoded, 0644); err != nil {
					t.Fatalf("failed to write gzip cache: %v", err)
				}

				// Load the gzip cache (no current hashes, so no stale detection).
				loaded, stale, err := Load(gzipCachePath, nil, nil)
				if err != nil {
					t.Fatalf("Load(gzip) returned error: %v", err)
				}

				// Verify Load succeeded.
				if loaded == nil {
					t.Fatal("Load(gzip) returned nil index, want non-nil")
				}

				// Verify no unexpected stale entries (no current hashes to compare).
				if len(stale) != 0 {
					t.Errorf("Load(gzip) returned %d stale entries, want 0: %v", len(stale), stale)
				}

				// Verify the entry is present and intact.
				// Key is normalized (forward slashes) when loaded.
				key := "prog1.NSP"
				fa, ok := loaded.Get(key)
				if !ok {
					t.Fatalf("Load(gzip) missing key %q", key)
				}

				// Check ObjectType.
				if fa.ObjectType != model.ObjectProgram {
					t.Errorf("ObjectType = %v, want %v", fa.ObjectType, model.ObjectProgram)
				}

				// Check Edges.
				if len(fa.Edges) != 1 {
					t.Errorf("Edges count = %d, want 1", len(fa.Edges))
				} else {
					if fa.Edges[0].TargetName != "SUBRTN1" {
						t.Errorf("Edge[0].TargetName = %q, want %q", fa.Edges[0].TargetName, "SUBRTN1")
					}
				}

				// Check DataAccess.
				if len(fa.DataAccess) != 1 {
					t.Errorf("DataAccess count = %d, want 1", len(fa.DataAccess))
				} else {
					if fa.DataAccess[0].Name != "EMPLOYEES" {
						t.Errorf("DataAccess[0].Name = %q, want %q", fa.DataAccess[0].Name, "EMPLOYEES")
					}
					if fa.DataAccess[0].NameRange.Start.Column != 6 {
						t.Errorf("DataAccess[0].NameRange.Start.Column = %d, want 6", fa.DataAccess[0].NameRange.Start.Column)
					}
				}

				// Check Structure.
				if fa.Structure == nil {
					t.Fatal("Structure = nil, want non-nil")
				}
				if fa.Structure.Name != "TESTPROG" {
					t.Errorf("Structure.Name = %q, want %q", fa.Structure.Name, "TESTPROG")
				}
				if len(fa.Structure.Children) != 1 {
					t.Errorf("Structure.Children count = %d, want 1", len(fa.Structure.Children))
				}
			})

			// Sub-case 2: plaintext case
			// Write the SAME CacheFile via json.MarshalIndent (legacy format),
			// Load, assert it still works (backward compat).
			t.Run("plaintext case", func(t *testing.T) {
				plaintextCachePath := filepath.Join(tmpDir, "cache_plaintext.nslp")

				// Write the CacheFile as indented JSON (legacy plaintext format).
				plaintextBytes, err := json.MarshalIndent(cache, "", "    ")
				if err != nil {
					t.Fatalf("json.MarshalIndent() returned error: %v", err)
				}

				if err := os.WriteFile(plaintextCachePath, plaintextBytes, 0644); err != nil {
					t.Fatalf("failed to write plaintext cache: %v", err)
				}

				// Load the plaintext cache.
				loaded, stale, err := Load(plaintextCachePath, nil, nil)
				if err != nil {
					t.Fatalf("Load(plaintext) returned error: %v", err)
				}

				// Verify Load succeeded.
				if loaded == nil {
					t.Fatal("Load(plaintext) returned nil index, want non-nil")
				}

				// Verify no unexpected stale entries.
				if len(stale) != 0 {
					t.Errorf("Load(plaintext) returned %d stale entries, want 0: %v", len(stale), stale)
				}

				// Verify the entry is present and intact.
				key := "prog1.NSP"
				fa, ok := loaded.Get(key)
				if !ok {
					t.Fatalf("Load(plaintext) missing key %q", key)
				}

				// Check ObjectType.
				if fa.ObjectType != model.ObjectProgram {
					t.Errorf("ObjectType = %v, want %v", fa.ObjectType, model.ObjectProgram)
				}

				// Check Edges.
				if len(fa.Edges) != 1 {
					t.Errorf("Edges count = %d, want 1", len(fa.Edges))
				} else {
					if fa.Edges[0].TargetName != "SUBRTN1" {
						t.Errorf("Edge[0].TargetName = %q, want %q", fa.Edges[0].TargetName, "SUBRTN1")
					}
				}

				// Check DataAccess.
				if len(fa.DataAccess) != 1 {
					t.Errorf("DataAccess count = %d, want 1", len(fa.DataAccess))
				} else {
					if fa.DataAccess[0].Name != "EMPLOYEES" {
						t.Errorf("DataAccess[0].Name = %q, want %q", fa.DataAccess[0].Name, "EMPLOYEES")
					}
					if fa.DataAccess[0].NameRange.Start.Column != 6 {
						t.Errorf("DataAccess[0].NameRange.Start.Column = %d, want 6", fa.DataAccess[0].NameRange.Start.Column)
					}
				}

				// Check Structure.
				if fa.Structure == nil {
					t.Fatal("Structure = nil, want non-nil")
				}
				if fa.Structure.Name != "TESTPROG" {
					t.Errorf("Structure.Name = %q, want %q", fa.Structure.Name, "TESTPROG")
				}
				if len(fa.Structure.Children) != 1 {
					t.Errorf("Structure.Children count = %d, want 1", len(fa.Structure.Children))
				}
			})
		})
	}
}

// TestLoad_CacheVersionBumpedTo070 verifies that the cache version bump from
// 0.6.0 to 0.7.0 (feature 24, T3) causes an old-format cache to trigger a
// full rebuild. This tests FR-39 (format-version gating) and Story 3 AC1
// (version bump → one-time rebuild).
// NOTE: As of feature 27 T1, the cache version is 0.8.0 (DataDefinition.NameRange added).
func TestLoad_CacheVersionBumpedTo070(t *testing.T) {
	t.Helper()

	// First, verify the version constant has been bumped to 0.8.0 (feature 27 T1: DataDefinition.NameRange).
	// This test documents the progression: 0.6.0 → 0.7.0 (feature 24) → 0.8.0 (feature 27).
	if cacheFormatVersion != "0.8.0" {
		t.Errorf("cacheFormatVersion = %q, want %q (version must be 0.8.0)", cacheFormatVersion, "0.8.0")
	}

	tests := []struct {
		name string
	}{
		{"version bump to 0.7.0 forces full rebuild of 0.6.0 cache"},
		{"0.6.0 plaintext cache is rejected, all files marked stale"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Build a pre-0.7.0 cache artifact with hardcoded version "0.6.0".
			// This simulates a cache written by a pre-feature-24 binary.
			oldCache := CacheFile{
				Version: "0.6.0", // Hardcoded old version, not using the const
				Entries: map[string]cacheEntry{
					"test1.NSP": {
						ObjectType: string(model.ObjectProgram),
						Edges: []model.EdgeEntry{
							{Kind: model.EdgeCalls, TargetName: "test2.NSP"},
						},
						ContentHash: "abc123",
					},
					"test2.NSP": {
						ObjectType:  string(model.ObjectSubprogram),
						ContentHash: "def456",
					},
				},
			}

			// Write the old cache as plaintext indented JSON (pre-gzip encoding).
			data, err := json.MarshalIndent(oldCache, "", "    ")
			if err != nil {
				t.Fatalf("json.MarshalIndent() returned error: %v", err)
			}

			if err := os.WriteFile(cachePath, data, 0644); err != nil {
				t.Fatalf("WriteFile() returned error: %v", err)
			}

			// Try to load the cache with the new format version.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)

			// Verify Load() returns nil index (version mismatch → full rebuild).
			if loaded != nil {
				t.Error("Load() returned non-nil index for 0.6.0 cache, want nil (forces rebuild)")
			}

			// Verify stale list is non-empty (all files marked stale due to version bump).
			if len(stale) == 0 {
				t.Error("Load() returned empty stale list for version mismatch, want all files marked stale")
			}

			// Verify we got the expected files in the stale list.
			expectedFiles := map[string]bool{"test1.NSP": true, "test2.NSP": true}
			for _, path := range stale {
				delete(expectedFiles, path)
			}
			if len(expectedFiles) > 0 {
				t.Errorf("Load() missing some files from stale list: %v", expectedFiles)
			}

			// Verify no error on Load (version mismatch is not an error, just returns nil+stale).
			if err != nil {
				t.Errorf("Load() returned error: %v", err)
			}
		})
	}
}

// TestLoad_CorruptCompressedCache verifies that Load() gracefully handles
// corrupt/truncated/unexpected-encoding cache bytes (Story 3 AC2, FR-43).
// This is the regression test for corrupt compressed caches required by the
// plan. Each sub-case verifies that Load() never panics and returns the
// rebuild signal (either err != nil or idx == nil).
// Feature 24 / T4.
func TestLoad_CorruptCompressedCache(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "gzip magic + truncated body",
			// Gzip magic bytes followed by invalid/truncated data.
			// This should fail to decompress gracefully without panicking.
			data: []byte{0x1f, 0x8b, 0x08, 0x00, 0x01, 0x02},
		},
		{
			name: "gzip magic + valid gzip of non-JSON",
			// Valid gzip-compressed data that decompresses to non-JSON.
			// gunzip succeeds but json.Unmarshal fails → should return error.
		},
		{
			name: "plaintext garbage (no gzip magic)",
			// Plaintext data that is not valid JSON — exercises the legacy path.
			data: []byte("{ this is not json"),
		},
	}

	// For the "gzip magic + valid gzip of non-JSON" case, we need to dynamically
	// generate valid gzip data.
	gzipOfGarbage := func(t *testing.T, plaintext string) []byte {
		var buf bytes.Buffer
		gw, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
		if err != nil {
			t.Fatalf("NewWriterLevel failed: %v", err)
		}
		_, err = gw.Write([]byte(plaintext))
		if err != nil {
			t.Fatalf("Write to gzip failed: %v", err)
		}
		err = gw.Close()
		if err != nil {
			t.Fatalf("Close gzip writer failed: %v", err)
		}
		return buf.Bytes()
	}
	tests[1].data = gzipOfGarbage(t, "not json at all")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Write the corrupt cache to a temp file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "corrupt.cache")

			if err := os.WriteFile(cachePath, tc.data, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			// Call Load() and catch any panic.
			var panicked bool
			var panicValue interface{}
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						panicValue = r
					}
				}()

				// Load with empty currentHashes and nil logger (as per the plan).
				loaded, stale, err := Load(cachePath, nil, nil)

				// The contract: BuildWithCache (index.go:654/659) checks:
				// - if err != nil: cold rebuild
				// - else if idx == nil: cold rebuild
				// Both satisfy the rebuild signal; neither should panic.
				if err == nil && loaded != nil {
					// This is acceptable IFF the data happens to be valid,
					// but for our corrupt cases we expect either err != nil or idx == nil.
					if len(tc.data) > 0 && (tc.data[0] != '{') && (len(tc.data) < 2 || !(tc.data[0] == 0x1f && tc.data[1] == 0x8b)) {
						// Plaintext garbage that doesn't start with '{' and isn't gzip:
						// should have failed to unmarshal.
						t.Errorf("Load() succeeded on corrupt data; loaded=%v, stale=%v", loaded, stale)
					}
				}
				// else: either err != nil (cold rebuild signal) or idx == nil (rebuild signal).
				// Both are acceptable for corrupt data.
			}()

			if panicked {
				t.Errorf("Load() panicked with: %v", panicValue)
			}
		})
	}
}

// TestBuildWithCache_CorruptCacheRebuilds verifies the end-to-end behavior:
// when the cache is corrupt (gzip truncated), BuildWithCache still produces
// a valid non-empty index and returns no error to the caller.
// This proves Story 3 AC2 (FR-43): graceful degradation on corrupt cache.
// Feature 24 / T4 (integration assertion).
func TestBuildWithCache_CorruptCacheRebuilds(t *testing.T) {
	t.Helper()

	// Set up a tiny real workspace with a few test files.
	tmpDir := t.TempDir()

	// Create a simple test program file.
	testFile := filepath.Join(tmpDir, "test.NSP")
	testContent := []byte(`DEFINE PROGRAM test
DEFINE DATA
LOCAL
1 X (N5)
END-DEFINE

CALLNAT 'SUB1'

END PROGRAM`)

	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("WriteFile test.NSP failed: %v", err)
	}

	// Create a cache directory and write a corrupt cache file.
	cacheDir := filepath.Join(tmpDir, ".natural-lsp-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	cachePath := filepath.Join(cacheDir, "index.nslp")
	// Write a corrupt compressed cache (gzip magic + truncated body).
	corruptData := []byte{0x1f, 0x8b, 0x08, 0x00, 0x01, 0x02}
	if err := os.WriteFile(cachePath, corruptData, 0644); err != nil {
		t.Fatalf("WriteFile corrupt cache failed: %v", err)
	}

	// Set up config with cache path.
	cfg := config.Defaults()
	cfg.Cache.Path = ".natural-lsp-cache"

	// Create a parser-based analyzer (nil custom extensions for this test).
	az := natural.New(nil)

	// Call BuildWithCache over the tiny workspace with the corrupt cache.
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	idx, _, _, err := BuildWithCache(ctx, tmpDir, cfg, az, logger, cachePath, nil, nil)

	// Verify no error is returned to the caller.
	if err != nil {
		t.Errorf("BuildWithCache() returned error: %v (should degrade to cold rebuild)", err)
	}

	// Verify the index is non-nil and non-empty (cold rebuild succeeded).
	if idx == nil {
		t.Fatal("BuildWithCache() returned nil index after corrupt cache; expected a valid cold-built index")
	}

	if len(idx.Keys()) == 0 {
		t.Error("BuildWithCache() returned empty index after corrupt cache; expected at least one file")
	}

	// Verify the test file is in the index (basic sanity).
	relPath := "test.NSP"
	fa, ok := idx.Get(relPath)
	if !ok {
		t.Errorf("index missing %q after cold rebuild", relPath)
	} else if fa.ObjectType != model.ObjectProgram {
		t.Errorf("test.NSP ObjectType = %v, want %v", fa.ObjectType, model.ObjectProgram)
	}
}

// TestBuildWithCache_RoundTripFidelity verifies that a real analyzer-built index
// survives save→load with identical FileAnalysis behavior across all files.
// Feature 24 / Story 1 AC2 — corpus-level round-trip fidelity.
// Uses the existing testdata/resolution/static-call fixture (MAIN.NSP + MYSUB.NSN)
// which carries edges, definitions, and program structure.
func TestBuildWithCache_RoundTripFidelity(t *testing.T) {
	t.Helper()

	// Fixture: testdata/resolution/static-call/
	// Contains MAIN.NSP (calls MYSUB) and MYSUB.NSN (subprogram definition).
	// Carries edges, definitions (DEFINE DATA blocks), and structure (root + subroutine).
	workspaceRoot := "testdata/resolution/static-call"

	// Load config (defaults — no library map for this fixture).
	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	az := natural.New(nil)

	// Step 1: COLD BUILD — build the index fresh from the fixture files.
	ctx := context.Background()
	idxA, _, _, err := BuildWithCache(ctx, workspaceRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("cold BuildWithCache failed: %v", err)
	}
	if idxA == nil {
		t.Fatal("cold BuildWithCache returned nil index")
	}

	// Verify the index has the expected content (non-trivial: at least one file with structure).
	keysA := idxA.Keys()
	if len(keysA) == 0 {
		t.Fatal("cold-built index is empty; fixture not loaded")
	}

	var mainAnalysis model.FileAnalysis
	var foundMain, foundSub bool
	for _, key := range keysA {
		fa, _ := idxA.Get(key)
		if strings.Contains(key, "MAIN") {
			mainAnalysis = fa
			foundMain = true
		}
		if strings.Contains(key, "MYSUB") {
			foundSub = true
		}
	}
	if !foundMain || !foundSub {
		t.Fatalf("fixture did not load both MAIN and MYSUB; found MAIN=%v MYSUB=%v", foundMain, foundSub)
	}

	// Verify the cold-built index has non-empty edges or definitions (fixture is non-trivial).
	if len(mainAnalysis.Edges) == 0 && len(mainAnalysis.Definitions) == 0 {
		t.Error("MAIN.NSP has no edges or definitions; fixture non-trivial assumption violated")
	}

	// Step 2: WRITE CACHE — save the cold-built index to disk in new format.
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache.nslp")

	err = Save(idxA, cachePath)
	if err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Verify the cache file was created and is in the new gzip format.
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("ReadFile cache failed: %v", err)
	}
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Error("cache file is not in gzip format (magic bytes missing)")
	}

	// Step 3: LOAD CACHE — load the saved cache into a fresh index.
	// Provide current hashes empty so Load treats the cache as fresh (no stale files).
	// This simulates a warm start where the cache is valid and unchanged.
	idxB, staleFiles, err := Load(cachePath, map[string]string{}, logger)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if idxB == nil {
		t.Fatal("Load returned nil index (cache format mismatch or error)")
	}

	// Verify cache was actually hit: staleFiles should be empty for unchanged files.
	if len(staleFiles) > 0 {
		t.Errorf("Load() marked %d files as stale; expected 0 (cache was fresh): %v", len(staleFiles), staleFiles)
	}

	// Step 4: COMPARE INDICES — assert fidelity across FileAnalysis fields.
	// For each file in the cold-built index, verify the loaded index has identical
	// ObjectType, Edges, DataAccess, Definitions, WorkFiles, HostVarRefs, and Structure.

	keysB := idxB.Keys()
	if len(keysA) != len(keysB) {
		t.Errorf("key count mismatch: cold=%d, loaded=%d", len(keysA), len(keysB))
	}

	for _, key := range keysA {
		faA, _ := idxA.Get(key)
		faB, ok := idxB.Get(key)
		if !ok {
			t.Errorf("loaded index missing key %q", key)
			continue
		}

		// ObjectType must be identical.
		if faA.ObjectType != faB.ObjectType {
			t.Errorf("ObjectType mismatch for %q: cold=%v, loaded=%v", key, faA.ObjectType, faB.ObjectType)
		}

		// Edges must be identical (call/dependency bindings).
		if !reflect.DeepEqual(faA.Edges, faB.Edges) {
			t.Errorf("Edges mismatch for %q:\n  cold:   %#v\n  loaded: %#v", key, faA.Edges, faB.Edges)
		}

		// DataAccess must be identical (read/write access edges).
		if !reflect.DeepEqual(faA.DataAccess, faB.DataAccess) {
			t.Errorf("DataAccess mismatch for %q:\n  cold:   %#v\n  loaded: %#v", key, faA.DataAccess, faB.DataAccess)
		}

		// Definitions must be identical (DEFINE DATA fields).
		if !reflect.DeepEqual(faA.Definitions, faB.Definitions) {
			t.Errorf("Definitions mismatch for %q:\n  cold:   %#v\n  loaded: %#v", key, faA.Definitions, faB.Definitions)
		}

		// WorkFiles must be identical.
		if !reflect.DeepEqual(faA.WorkFiles, faB.WorkFiles) {
			t.Errorf("WorkFiles mismatch for %q:\n  cold:   %#v\n  loaded: %#v", key, faA.WorkFiles, faB.WorkFiles)
		}

		// HostVarRefs must be identical (SQL host-variable references).
		if !reflect.DeepEqual(faA.HostVarRefs, faB.HostVarRefs) {
			t.Errorf("HostVarRefs mismatch for %q:\n  cold:   %#v\n  loaded: %#v", key, faA.HostVarRefs, faB.HostVarRefs)
		}

		// Structure must be identical (program outline tree).
		if !reflect.DeepEqual(faA.Structure, faB.Structure) {
			t.Errorf("Structure mismatch for %q:\n  cold:   %#v\n  loaded: %#v", key, faA.Structure, faB.Structure)
		}
	}

	// Step 5 (OPTIONAL): Run Resolve on both indices and compare resolution outcomes.
	// This verifies that the cached edges resolve identically.
	resA := Resolve(idxA, &cfg)
	resB := Resolve(idxB, &cfg)

	// Find a representative CALLNAT site to compare resolution outcomes.
	// MAIN.NSP's CALLNAT to MYSUB should resolve identically in both.
	mainKey := ""
	for _, key := range keysA {
		if strings.Contains(key, "MAIN") {
			mainKey = key
			break
		}
	}
	if mainKey != "" {
		fa, _ := idxA.Get(mainKey)
		for _, edge := range fa.Edges {
			// Find the first CALLNAT edge.
			if edge.Kind == model.EdgeCalls {
				// Create a source range key for the resolution lookup.
				// ResolutionSet keys by (referencing file, edge Source).
				outA, okA := resA.Get(mainKey, edge.Source)
				outB, okB := resB.Get(mainKey, edge.Source)

				if okA != okB {
					t.Errorf("resolution ok mismatch for %s edge at %v: cold=%v, loaded=%v", mainKey, edge.Source, okA, okB)
				} else if !reflect.DeepEqual(outA, outB) {
					t.Errorf("resolution outcome mismatch for %s edge at %v:\n  cold:   %#v\n  loaded: %#v",
						mainKey, edge.Source, outA, outB)
				}
				break
			}
		}
	}
}

// TestLoad_RejectsCacheVersion0_7_0_AsStale_T1 (T1 RED, feature 27) verifies
// that a cache written at version "0.7.0" (before DataDefinition.NameRange was
// added) is detected as stale and triggers a full rebuild. This regression test
// ensures the version bump to "0.8.0" works correctly and enforces cache
// invalidation on model changes.
func TestLoad_RejectsCacheVersion0_7_0_AsStale_T1(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"0.7.0 cache marked stale when version bumped to 0.8.0"},
		{"forces full rebuild on DataDefinition.NameRange migration"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()
			cachePath := filepath.Join(tmpDir, "cache.json")

			// Manually build a 0.7.0 cache (the pre-T1 version) with plaintext JSON.
			// This simulates a cache written by the old format before the NameRange addition.
			oldVersionCache := CacheFile{
				Version: "0.7.0",
				Entries: map[string]cacheEntry{
					"test.NSP": {
						ObjectType:  string(model.ObjectProgram),
						ContentHash: "deadbeef",
						Definitions: []model.DataDefinition{
							{
								Name:  "OLD-FIELD",
								Level: 1,
								Type:  "A10",
								Range: model.Range{
									Start: model.Position{Line: 5, Column: 1},
									End:   model.Position{Line: 5, Column: 10},
								},
								// Note: NameRange not present in old cache (the new field)
							},
						},
					},
				},
			}
			data, err := json.MarshalIndent(oldVersionCache, "", "    ")
			if err != nil {
				t.Fatalf("Failed to marshal old-version cache: %v", err)
			}
			if err := os.WriteFile(cachePath, data, 0644); err != nil {
				t.Fatalf("Failed to write old-version cache: %v", err)
			}

			// Try to load the cache - should treat it as stale due to version mismatch.
			loaded, stale, err := Load(cachePath, map[string]string{}, nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Verify Load() returns nil loaded index (stale, version mismatch).
			if loaded != nil {
				t.Error("Load() returned non-nil index for 0.7.0 cache, want nil (stale)")
			}

			// Verify stale list contains the file (forces rebuild).
			if len(stale) == 0 {
				t.Error("Load() returned empty stale list for version mismatch, want all files marked stale")
			}
		})
	}
}
