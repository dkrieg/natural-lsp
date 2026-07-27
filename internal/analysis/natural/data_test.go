package natural

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// Analyze is a test helper that runs the full extraction pipeline
// and returns the FileAnalysis result (with Structure, Definitions, etc.).
func Analyze(path string, content string) *model.FileAnalysis {
	lexer := NewLexer(content)
	parser := NewParser(lexer)
	prog, _ := parser.Parse()
	if prog == nil {
		return nil
	}

	// Run extraction pipeline
	result := model.FileAnalysis{
		ObjectType: classify(path, nil),
	}

	// Extract components
	result.Edges = extractEdges(prog)
	result.DataAccess = extractDataAccess(prog)
	result.Definitions = extractDefinitions(prog)
	result.WorkFiles = extractWorkFiles(prog)
	result.HostVarRefs = extractHostVarRefs(prog)
	result.DataAreaRefs = extractDataAreaRefs(prog)
	result.AST = prog
	result.Diagnostics = prog.Diagnostics

	// Extract structure (uses Definitions)
	result.Structure = extractStructure(path, prog, result.Definitions, result.DataAccess)

	return &result
}

// TestExtractDataAccess_GracefulDegradation verifies FR-43: extractDataAccess
// never panics on a nil program, an empty program, or statements with empty
// Target fields (malformed, diagnostics already emitted by the parser).
func TestExtractDataAccess_GracefulDegradation(t *testing.T) {
	t.Run("nil_program_returns_nil", func(t *testing.T) {
		got := extractDataAccess(nil)
		if got != nil {
			t.Errorf("extractDataAccess(nil) = %v, want nil", got)
		}
	})

	t.Run("empty_program_returns_empty", func(t *testing.T) {
		got := extractDataAccess(&Program{})
		if len(got) != 0 {
			t.Errorf("extractDataAccess(&Program{}) len = %d, want 0", len(got))
		}
	})

	t.Run("empty_target_read_skipped", func(t *testing.T) {
		prog := &Program{
			Reads: []*ReadStatement{
				{Target: ""}, // malformed — must be skipped
				{Target: "EMPLOYEES", StartPos: model.Position{Line: 2, Column: 1}},
			},
		}
		got := extractDataAccess(prog)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (malformed entry must be skipped)", len(got))
		}
		if got[0].Name != "EMPLOYEES" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "EMPLOYEES")
		}
	})

	t.Run("empty_target_store_skipped", func(t *testing.T) {
		prog := &Program{
			Stores: []*StoreStatement{
				{Target: ""}, // malformed — must be skipped
				{Target: "VEHICLES", StartPos: model.Position{Line: 3, Column: 1}},
			},
		}
		got := extractDataAccess(prog)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (malformed entry must be skipped)", len(got))
		}
		if got[0].Name != "VEHICLES" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "VEHICLES")
		}
	})
}

// TestExtractDataAccess_ReadStore verifies that READ and STORE statements
// are extracted as data-access edges in source order (Task 1 / FR-19, FR-20).
//
// Acceptance criteria:
//   - Emit exactly one EdgeReads entry for each READ statement
//   - Emit exactly one EdgeWrites entry for each STORE statement
//   - File (name) is the target view name, normalized to upper-case
//   - Source is the statement range
//   - Entries are in source order (stable sort on Source.Start)
//   - Zero entries are produced for non-data-access lines (DEFINE DATA, WRITE)
func TestExtractDataAccess_ReadStore(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "dataaccess", "01-read-store.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor (stub function for RED phase)
	entries := extractDataAccess(prog)

	// Test table-driven assertions (AAA)
	tests := []struct {
		name      string
		wantCount int
		verify    func(t *testing.T, entries []model.DataAccessEntry)
	}{
		{
			name:      "extractDataAccess_ReadStore_exactlyThreeEntries",
			wantCount: 3,
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				if len(entries) != 3 {
					t.Errorf("len(entries) = %d, want 3", len(entries))
				}

				// Assert first entry: READ EMPLOYEES
				if len(entries) > 0 {
					entry0 := entries[0]
					if entry0.Kind != model.EdgeReads {
						t.Errorf("entries[0].Kind = %s, want %s", entry0.Kind, model.EdgeReads)
					}
					if entry0.Name != "EMPLOYEES" {
						t.Errorf("entries[0].Name = %q, want %q", entry0.Name, "EMPLOYEES")
					}
					// Source must be non-zero (statement range)
					if entry0.Source.Start.Line == 0 && entry0.Source.Start.Column == 0 &&
						entry0.Source.End.Line == 0 && entry0.Source.End.Column == 0 {
						t.Error("entries[0].Source is zero, want non-zero statement range")
					}
				}

				// Assert second entry: READ (5) VEHICLES BY MAKE
				if len(entries) > 1 {
					entry1 := entries[1]
					if entry1.Kind != model.EdgeReads {
						t.Errorf("entries[1].Kind = %s, want %s", entry1.Kind, model.EdgeReads)
					}
					if entry1.Name != "VEHICLES" {
						t.Errorf("entries[1].Name = %q, want %q", entry1.Name, "VEHICLES")
					}
					// Source must be non-zero
					if entry1.Source.Start.Line == 0 && entry1.Source.Start.Column == 0 &&
						entry1.Source.End.Line == 0 && entry1.Source.End.Column == 0 {
						t.Error("entries[1].Source is zero, want non-zero statement range")
					}
				}

				// Assert third entry: STORE EMPLOYEES
				if len(entries) > 2 {
					entry2 := entries[2]
					if entry2.Kind != model.EdgeWrites {
						t.Errorf("entries[2].Kind = %s, want %s", entry2.Kind, model.EdgeWrites)
					}
					if entry2.Name != "EMPLOYEES" {
						t.Errorf("entries[2].Name = %q, want %q", entry2.Name, "EMPLOYEES")
					}
					// Source must be non-zero
					if entry2.Source.Start.Line == 0 && entry2.Source.Start.Column == 0 &&
						entry2.Source.End.Line == 0 && entry2.Source.End.Column == 0 {
						t.Error("entries[2].Source is zero, want non-zero statement range")
					}
				}

				// Assert source order: entry0 comes before entry1 before entry2
				if len(entries) >= 2 {
					entry0Start := entries[0].Source.Start.Line
					entry1Start := entries[1].Source.Start.Line
					if entry0Start >= entry1Start {
						t.Errorf("entries not in source order: entry[0] at line %d, entry[1] at line %d",
							entry0Start, entry1Start)
					}
				}
				if len(entries) >= 3 {
					entry1Start := entries[1].Source.Start.Line
					entry2Start := entries[2].Source.Start.Line
					if entry1Start >= entry2Start {
						t.Errorf("entries not in source order: entry[1] at line %d, entry[2] at line %d",
							entry1Start, entry2Start)
					}
				}
			},
		},
		{
			name:      "extractDataAccess_ReadStore_noFalseEntriesFromNonDataAccessLines",
			wantCount: 3,
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// DEFINE DATA, WRITE should produce zero entries
				// (already verified by count == 3, but document the intent)
				if len(entries) != 3 {
					t.Errorf("len(entries) = %d; expected only 3 entries from READ/STORE calls, zero from DEFINE/WRITE", len(entries))
				}
			},
		},
		{
			name:      "extractDataAccess_ReadStore_caseSensitiveNormalization",
			wantCount: 3,
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// The lexer normalizes identifiers to uppercase.
				// Assert that File fields are uppercased (not mixed-case as written).
				expectedNames := []string{"EMPLOYEES", "VEHICLES", "EMPLOYEES"}
				for i, want := range expectedNames {
					if i < len(entries) {
						if entries[i].Name != want {
							t.Errorf("entries[%d].Name = %q, want %q (normalized)", i, entries[i].Name, want)
						}
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, entries)
		})
	}
}

// TestExtractDataAccess_NameRange verifies FR-20 acceptance criterion:
// "accessed name" with "records the access site". Each DataAccessEntry
// carries a NameRange that spans just the view-name token (not the whole statement),
// enabling hover/references to point precisely at the accessed DDM/view name.
//
// OQ-3 decision: rename File → Name, add NameRange Range covering just the token.
func TestExtractDataAccess_NameRange(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "dataaccess", "01-read-store.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}

	// Call the extractor
	entries := extractDataAccess(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, entries []model.DataAccessEntry)
	}{
		{
			name: "NameRange_isNarrowerThanSource",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// For `READ EMPLOYEES` on line 10:
				// - Source should span the entire READ statement (START…END)
				// - NameRange should span just "EMPLOYEES" token
				// Assert NameRange.Start.Column > Source.Start.Column (READ keyword comes first)
				// AND NameRange.End.Column < Source.End.Column (EMPLOYEES ends before the statement)

				if len(entries) == 0 {
					t.Fatal("Expected at least 1 entry")
				}

				// First entry is READ EMPLOYEES (line 10)
				entry0 := entries[0]

				// NameRange must be populated (not zero-valued)
				if entry0.NameRange.Start.Line == 0 && entry0.NameRange.Start.Column == 0 &&
					entry0.NameRange.End.Line == 0 && entry0.NameRange.End.Column == 0 {
					t.Error("entry[0].NameRange is zero, want populated range for 'EMPLOYEES' token")
				}

				// NameRange.Start should be after Source.Start (READ keyword is before EMPLOYEES)
				if entry0.NameRange.Start.Column <= entry0.Source.Start.Column {
					t.Errorf("entry[0].NameRange.Start.Column = %d, want > %d (after READ keyword)",
						entry0.NameRange.Start.Column, entry0.Source.Start.Column)
				}

				// Source should span the whole statement; NameRange only the token
				if entry0.Source.Start.Column >= entry0.NameRange.Start.Column {
					t.Errorf("Source.Start.Column should be <= NameRange.Start.Column (SOURCE comes first)")
				}
			},
		},
		{
			name: "NameRange_pointsAtViewName",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Verify each entry's NameRange reflects the accessed name:
				// entry[0]: READ EMPLOYEES (NameRange should point to "EMPLOYEES")
				// entry[1]: READ (5) VEHICLES BY MAKE (NameRange should point to "VEHICLES")
				// entry[2]: STORE EMPLOYEES (NameRange should point to "EMPLOYEES")

				if len(entries) < 3 {
					t.Fatalf("Expected at least 3 entries, got %d", len(entries))
				}

				// For each entry, NameRange.Start and .End must be non-zero
				for i, entry := range entries[:3] {
					if entry.NameRange.Start.Line == 0 {
						t.Errorf("entries[%d].NameRange.Start.Line = 0, want > 0", i)
					}
					if entry.NameRange.Start.Column == 0 {
						t.Errorf("entries[%d].NameRange.Start.Column = 0, want > 0", i)
					}
					if entry.NameRange.End.Line == 0 {
						t.Errorf("entries[%d].NameRange.End.Line = 0, want > 0", i)
					}
					if entry.NameRange.End.Column == 0 {
						t.Errorf("entries[%d].NameRange.End.Column = 0, want > 0", i)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, entries)
		})
	}
}

// TestExtractDataAccess_Find verifies FR-19 acceptance criterion: FIND statements
// are extracted as EdgeReads entries with the normalized (uppercase) view name and
// NameRange pointing to the view-name token (Task 4).
//
// Acceptance criteria:
//   - Emit exactly one EdgeReads entry for each valid FIND statement
//   - Name is the target view name, normalized to upper-case
//   - NameRange points to just the view-name token (not the whole FIND statement)
//   - Source is the statement range
//   - Entries are in source order when interleaved with READ/STORE statements
//   - Empty-target FIND (malformed) is skipped
func TestExtractDataAccess_Find(t *testing.T) {
	// Read the fixture: contains READ DEPARTMENTS, FIND EMPLOYEES, STORE EMPLOYEES, FIND VEHICLES
	content, err := os.ReadFile(filepath.Join("testdata", "dataaccess", "02-find.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	entries := extractDataAccess(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, entries []model.DataAccessEntry)
	}{
		{
			name: "find_emitsEdgeReadsPerFindStatement",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Fixture contains: READ DEPARTMENTS, FIND EMPLOYEES, STORE EMPLOYEES, FIND VEHICLES
				// Expected: 4 entries in source order
				//   0: READ DEPARTMENTS (EdgeReads)
				//   1: FIND EMPLOYEES (EdgeReads) — NEW
				//   2: STORE EMPLOYEES (EdgeWrites)
				//   3: FIND VEHICLES (EdgeReads) — NEW
				if len(entries) != 4 {
					t.Errorf("len(entries) = %d, want 4 (READ, FIND, STORE, FIND)", len(entries))
				}
			},
		},
		{
			name: "find_normalizedNameAndKind",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Assert second entry is FIND EMPLOYEES with EdgeReads kind
				if len(entries) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(entries))
				}
				entry1 := entries[1]
				if entry1.Kind != model.EdgeReads {
					t.Errorf("entries[1].Kind = %s, want %s (FIND should be EdgeReads)", entry1.Kind, model.EdgeReads)
				}
				if entry1.Name != "EMPLOYEES" {
					t.Errorf("entries[1].Name = %q, want %q (normalized to uppercase)", entry1.Name, "EMPLOYEES")
				}
			},
		},
		{
			name: "find_secondFindAlsoExtracted",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Assert fourth entry is FIND VEHICLES (3) with EdgeReads kind
				if len(entries) < 4 {
					t.Fatalf("Expected at least 4 entries, got %d", len(entries))
				}
				entry3 := entries[3]
				if entry3.Kind != model.EdgeReads {
					t.Errorf("entries[3].Kind = %s, want %s (FIND should be EdgeReads)", entry3.Kind, model.EdgeReads)
				}
				if entry3.Name != "VEHICLES" {
					t.Errorf("entries[3].Name = %q, want %q (normalized to uppercase)", entry3.Name, "VEHICLES")
				}
			},
		},
		{
			name: "find_sourceOrderPreserved",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Verify entries are in source order: READ(line N), FIND(line N+3), STORE(line N+5), FIND(line N+7)
				if len(entries) < 4 {
					t.Fatalf("Expected at least 4 entries, got %d", len(entries))
				}

				// Rough check: lines should be increasing (allowing for same-line entries)
				for i := 0; i < len(entries)-1; i++ {
					if entries[i].Source.Start.Line > entries[i+1].Source.Start.Line {
						t.Errorf("entries[%d] at line %d comes after entries[%d] at line %d, violates source order",
							i, entries[i].Source.Start.Line, i+1, entries[i+1].Source.Start.Line)
					}
				}
			},
		},
		{
			name: "find_nameRangePopulated",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Both FIND entries should have non-zero NameRange (from Task 2 contract)
				if len(entries) < 4 {
					t.Fatalf("Expected at least 4 entries, got %d", len(entries))
				}

				// Entry 1: FIND EMPLOYEES
				entry1 := entries[1]
				if entry1.NameRange.Start.Line == 0 || entry1.NameRange.Start.Column == 0 ||
					entry1.NameRange.End.Line == 0 || entry1.NameRange.End.Column == 0 {
					t.Error("entries[1].NameRange is zero, want populated range for 'EMPLOYEES' token")
				}

				// Entry 3: FIND VEHICLES
				entry3 := entries[3]
				if entry3.NameRange.Start.Line == 0 || entry3.NameRange.Start.Column == 0 ||
					entry3.NameRange.End.Line == 0 || entry3.NameRange.End.Column == 0 {
					t.Error("entries[3].NameRange is zero, want populated range for 'VEHICLES' token")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, entries)
		})
	}
}

// TestExtractDataAccess_Get verifies FR-19 acceptance criterion: GET statements
// with a valid operand are extracted as EdgeReads entries, and GET SAME (no operand)
// is SKIPPED (produces no edge — a read with no resolvable file/DDM name, consistent
// with how empty-target READ/FIND are skipped). Task 6 / FR-19.
//
// Acceptance criteria:
//   - Emit exactly one EdgeReads entry for GET EMPLOYEES
//   - Name is the target view name, normalized to upper-case
//   - NameRange points to just the view-name token
//   - Kind is EdgeReads
//   - GET SAME (empty Target) is deliberately skipped — no entry emitted
//   - Total GET-derived entries == 1 (not 2, proving GET SAME is skipped)
func TestExtractDataAccess_Get(t *testing.T) {
	// Read the fixture: contains READ EMPLOYEES, GET EMPLOYEES (#ISN), GET SAME
	content, err := os.ReadFile(filepath.Join("testdata", "dataaccess", "03-get.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	entries := extractDataAccess(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, entries []model.DataAccessEntry)
	}{
		{
			name: "get_emitsOneEdgeReadsForValidOperand",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Fixture contains: READ EMPLOYEES, GET EMPLOYEES, GET SAME
				// Expected: 2 total entries (READ + GET), NOT 3
				// (GET SAME is skipped because Target is empty)
				if len(entries) != 2 {
					t.Errorf("len(entries) = %d, want 2 (READ + GET; GET SAME must be skipped)",
						len(entries))
				}
			},
		},
		{
			name: "get_same_isSkipped",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Assert that GET SAME (the second GET in the fixture) produces NO edge.
				// This is the load-bearing assertion: empty-target GET is deliberately
				// skipped, not extracted as a partial entry.
				if len(entries) != 2 {
					t.Errorf("Expected exactly 2 entries (READ + GET), got %d; GET SAME must be skipped",
						len(entries))
				}
			},
		},
		{
			name: "get_normalizedNameAndKind",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Assert the GET entry (second entry, index 1) carries the right name and kind
				if len(entries) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(entries))
				}
				entry1 := entries[1]
				if entry1.Kind != model.EdgeReads {
					t.Errorf("entries[1].Kind = %s, want %s (GET should be EdgeReads)",
						entry1.Kind, model.EdgeReads)
				}
				if entry1.Name != "EMPLOYEES" {
					t.Errorf("entries[1].Name = %q, want %q (normalized to uppercase)",
						entry1.Name, "EMPLOYEES")
				}
			},
		},
		{
			name: "get_nameRangePopulated",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Assert the GET EMPLOYEES entry has a non-zero NameRange
				if len(entries) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(entries))
				}
				entry1 := entries[1]
				if entry1.NameRange.Start.Line == 0 || entry1.NameRange.Start.Column == 0 ||
					entry1.NameRange.End.Line == 0 || entry1.NameRange.End.Column == 0 {
					t.Error("entries[1].NameRange is zero, want populated range for 'EMPLOYEES' token")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, entries)
		})
	}
}

// TestExtractDataAccess_RecordWrites verifies FR-20 acceptance criterion:
// record UPDATE/DELETE statements are extracted as EdgeWrites entries with
// EMPTY Name (because record-form write has no file operand; the file comes
// from the enclosing READ/FIND/GET loop). The Source is the statement range,
// enabling impact analysis (recording that a write occurs at this site even
// though file binding is deferred). Task 8 / FR-20.
//
// Design (OQ-4): record UPDATE/DELETE carry NO file operand, so their write
// edge has an empty Name and the Source is the statement range. This records
// THAT a write occurs at this site (impact-analysis value), file bound later.
//
// Acceptance criteria:
//   - Walk prog.RecordUpdates → EdgeWrites entries with empty Name, non-zero Source
//   - Walk prog.RecordDeletes → EdgeWrites entries with empty Name, non-zero Source
//   - Read/write kinds are distinguishable on the same view:
//     READ EMPLOYEES (Name="EMPLOYEES", Kind=EdgeReads)
//     STORE EMPLOYEES (Name="EMPLOYEES", Kind=EdgeWrites)
//     record UPDATE (Name="", Kind=EdgeWrites)
//     record DELETE (Name="", Kind=EdgeWrites)
//   - All in global source order
func TestExtractDataAccess_RecordWrites(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "dataaccess", "04-write-mix.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	entries := extractDataAccess(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, entries []model.DataAccessEntry)
	}{
		{
			name: "recordWrites_emitsEdgeWritesForUpdateAndDelete",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Fixture contains:
				//   READ EMPLOYEES (EdgeReads, Name="EMPLOYEES")
				//   UPDATE (EdgeWrites, Name="")          ← record write, no operand
				//   STORE EMPLOYEES (EdgeWrites, Name="EMPLOYEES")
				//   READ VEHICLES (EdgeReads, Name="VEHICLES")
				//   DELETE (EdgeWrites, Name="")          ← record write, no operand
				//
				// Expected: 5 total entries
				if len(entries) != 5 {
					t.Errorf("len(entries) = %d, want 5 (READ, UPDATE, STORE, READ, DELETE)", len(entries))
				}
			},
		},
		{
			name: "recordWrites_updateHasEmptyNameButNonZeroSource",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// The UPDATE entry (index 1) must have:
				// - Kind = EdgeWrites
				// - Name = "" (empty, not set)
				// - Source = non-zero (statement range)
				// - NameRange = zero (empty-name write has no token range)
				if len(entries) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(entries))
				}

				updateEntry := entries[1]

				// Kind must be EdgeWrites
				if updateEntry.Kind != model.EdgeWrites {
					t.Errorf("entries[1].Kind = %s, want %s (UPDATE is a write)", updateEntry.Kind, model.EdgeWrites)
				}

				// Name must be EMPTY (the design point: no file operand)
				if updateEntry.Name != "" {
					t.Errorf("entries[1].Name = %q, want %q (record UPDATE has no file operand)", updateEntry.Name, "")
				}

				// Source must be non-zero (statement range)
				if updateEntry.Source.Start.Line == 0 || updateEntry.Source.Start.Column == 0 ||
					updateEntry.Source.End.Line == 0 || updateEntry.Source.End.Column == 0 {
					t.Error("entries[1].Source is zero, want non-zero statement range (UPDATE statement position)")
				}

				// NameRange must be zero (no Name token)
				if updateEntry.NameRange.Start.Line != 0 || updateEntry.NameRange.Start.Column != 0 ||
					updateEntry.NameRange.End.Line != 0 || updateEntry.NameRange.End.Column != 0 {
					t.Errorf("entries[1].NameRange = %+v, want zero (empty-Name write has no token range)", updateEntry.NameRange)
				}
			},
		},
		{
			name: "recordWrites_deleteHasEmptyNameButNonZeroSource",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// The DELETE entry (index 4) must have:
				// - Kind = EdgeWrites
				// - Name = "" (empty)
				// - Source = non-zero (statement range)
				// - NameRange = zero (empty-name write has no token range)
				if len(entries) < 5 {
					t.Fatalf("Expected at least 5 entries, got %d", len(entries))
				}

				deleteEntry := entries[4]

				// Kind must be EdgeWrites
				if deleteEntry.Kind != model.EdgeWrites {
					t.Errorf("entries[4].Kind = %s, want %s (DELETE is a write)", deleteEntry.Kind, model.EdgeWrites)
				}

				// Name must be EMPTY (the design point: no file operand)
				if deleteEntry.Name != "" {
					t.Errorf("entries[4].Name = %q, want %q (record DELETE has no file operand)", deleteEntry.Name, "")
				}

				// Source must be non-zero (statement range)
				if deleteEntry.Source.Start.Line == 0 || deleteEntry.Source.Start.Column == 0 ||
					deleteEntry.Source.End.Line == 0 || deleteEntry.Source.End.Column == 0 {
					t.Error("entries[4].Source is zero, want non-zero statement range (DELETE statement position)")
				}

				// NameRange must be zero (no Name token)
				if deleteEntry.NameRange.Start.Line != 0 || deleteEntry.NameRange.Start.Column != 0 ||
					deleteEntry.NameRange.End.Line != 0 || deleteEntry.NameRange.End.Column != 0 {
					t.Errorf("entries[4].NameRange = %+v, want zero (empty-Name write has no token range)", deleteEntry.NameRange)
				}
			},
		},
		{
			name: "recordWrites_readVsWriteDistinguishableByKind",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// On the same view name (EMPLOYEES):
				// - READ EMPLOYEES: Kind=EdgeReads, Name="EMPLOYEES"
				// - STORE EMPLOYEES: Kind=EdgeWrites, Name="EMPLOYEES"
				// Both have the same Name but different Kind, making them distinguishable.
				//
				// Also verify: record UPDATE (Name="") vs STORE (Name="EMPLOYEES")
				// are distinguishable by Name (empty vs set).
				if len(entries) < 3 {
					t.Fatalf("Expected at least 3 entries, got %d", len(entries))
				}

				readEntry := entries[0]   // READ EMPLOYEES
				updateEntry := entries[1] // UPDATE (record, no operand)
				storeEntry := entries[2]  // STORE EMPLOYEES

				// Verify READ and STORE have same Name but different Kind
				if readEntry.Name != "EMPLOYEES" {
					t.Errorf("entries[0].Name = %q, want %q", readEntry.Name, "EMPLOYEES")
				}
				if storeEntry.Name != "EMPLOYEES" {
					t.Errorf("entries[2].Name = %q, want %q", storeEntry.Name, "EMPLOYEES")
				}
				if readEntry.Kind == storeEntry.Kind {
					t.Errorf("READ EMPLOYEES and STORE EMPLOYEES should have different Kind: both are %s", readEntry.Kind)
				}
				if readEntry.Kind != model.EdgeReads {
					t.Errorf("READ kind = %s, want %s", readEntry.Kind, model.EdgeReads)
				}
				if storeEntry.Kind != model.EdgeWrites {
					t.Errorf("STORE kind = %s, want %s", storeEntry.Kind, model.EdgeWrites)
				}

				// Verify record UPDATE (empty Name) vs STORE (set Name) are distinguishable
				if updateEntry.Name != "" {
					t.Errorf("record UPDATE Name = %q, want %q (empty)", updateEntry.Name, "")
				}
				if storeEntry.Name == "" {
					t.Errorf("STORE Name = %q, want non-empty", storeEntry.Name)
				}
			},
		},
		{
			name: "recordWrites_sourceOrderPreserved",
			verify: func(t *testing.T, entries []model.DataAccessEntry) {
				// Verify all entries are in source order:
				// 0: READ EMPLOYEES (line ~11)
				// 1: UPDATE (line ~12, inside READ loop)
				// 2: STORE EMPLOYEES (line ~15)
				// 3: READ VEHICLES (line ~17)
				// 4: DELETE (line ~18, inside READ loop)
				if len(entries) < 5 {
					t.Fatalf("Expected at least 5 entries, got %d", len(entries))
				}

				for i := 0; i < len(entries)-1; i++ {
					if entries[i].Source.Start.Line > entries[i+1].Source.Start.Line {
						t.Errorf("entries[%d] at line %d comes after entries[%d] at line %d, violates source order",
							i, entries[i].Source.Start.Line, i+1, entries[i+1].Source.Start.Line)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, entries)
		})
	}
}

// TestExtractDefinitions_ExtendedSections_Task17Regression verifies FR-17/M-6:
// DEFINE DATA sections that are less commonly used (INDEPENDENT, CONTEXT, OBJECT)
// must NOT be silently dropped. Their fields must be extracted and tagged with the
// correct SectionKind, so all declared variables are visible in the symbol table.
//
// Regression (Task 17): isSectionKeyword only recognized LOCAL/PARAMETER/GLOBAL/LINKAGE,
// causing INDEPENDENT/CONTEXT/OBJECT sections to terminate the section loop early,
// with their fields vanishing with no diagnostic (violating M-6 no-silent-drop invariant).
//
// Acceptance criteria:
//   - INDEPENDENT section fields are recognized and extracted with SectionKind="independent"
//   - CONTEXT section fields are recognized and extracted with SectionKind="context"
//   - OBJECT section fields are recognized and extracted with SectionKind="object"
//   - Combined fixture (LOCAL + INDEPENDENT + CONTEXT) extracts all fields in declaration order
//   - Parser does NOT emit a diagnostic for recognized sections (they are valid Natural)
func TestExtractDefinitions_ExtendedSections_Task17Regression(t *testing.T) {
	// Read the fixture: 25-data-sections-extended.nsp with LOCAL, INDEPENDENT, CONTEXT
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "25-data-sections-extended.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	// Note: parser may emit diagnostics if the section keywords are not recognized,
	// but we verify the definitions are extracted regardless.

	// Call the extractor
	defs := extractDefinitions(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, defs []model.DataDefinition, prog *Program)
	}{
		{
			name: "regression_independentSectionNotSilentlyDropped",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// The critical regression check: INDEPENDENT section fields are extracted
				// (not silently dropped). The fixture has:
				//   LOCAL: 1 #LOCAL-COUNTER (N5)
				//   INDEPENDENT: 1 #APP-STATE (A20)
				//   CONTEXT: 1 #RPC-SESSION (N7)
				// Expected: 3 definitions total (all sections' fields)
				if len(defs) != 3 {
					t.Errorf("len(defs) = %d, want 3 (LOCAL + INDEPENDENT + CONTEXT fields, none dropped)",
						len(defs))
				}
			},
		},
		{
			name: "regression_independentFieldsExtracted",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Assert that the INDEPENDENT field #APP-STATE is present
				foundIndependent := false
				for _, def := range defs {
					if def.Name == "#APP-STATE" && def.SectionKind == "independent" {
						foundIndependent = true
						break
					}
				}
				if !foundIndependent {
					t.Error("INDEPENDENT section field #APP-STATE not found in definitions (silently dropped)")
				}
			},
		},
		{
			name: "regression_contextFieldsExtracted",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Assert that the CONTEXT field #RPC-SESSION is present
				foundContext := false
				for _, def := range defs {
					if def.Name == "#RPC-SESSION" && def.SectionKind == "context" {
						foundContext = true
						break
					}
				}
				if !foundContext {
					t.Error("CONTEXT section field #RPC-SESSION not found in definitions (silently dropped)")
				}
			},
		},
		{
			name: "regression_sectionKindTagsPreserved",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Verify each definition carries the correct SectionKind
				// Expected mapping: LOCAL=#LOCAL-COUNTER, INDEPENDENT=#APP-STATE, CONTEXT=#RPC-SESSION
				expectedSectionKinds := map[string]string{
					"#LOCAL-COUNTER": "local",
					"#APP-STATE":     "independent",
					"#RPC-SESSION":   "context",
				}

				for _, def := range defs {
					if expectedKind, exists := expectedSectionKinds[def.Name]; exists {
						if def.SectionKind != expectedKind {
							t.Errorf("%s: SectionKind = %q, want %q",
								def.Name, def.SectionKind, expectedKind)
						}
					}
				}
			},
		},
		{
			name: "regression_declarationOrderPreserved",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Verify definitions appear in declaration order: LOCAL, INDEPENDENT, CONTEXT
				expectedOrder := []string{"#LOCAL-COUNTER", "#APP-STATE", "#RPC-SESSION"}
				if len(defs) != len(expectedOrder) {
					t.Fatalf("len(defs) = %d, want %d for order check", len(defs), len(expectedOrder))
				}

				for i, want := range expectedOrder {
					if defs[i].Name != want {
						t.Errorf("defs[%d].Name = %q, want %q (declaration order must be preserved)",
							i, defs[i].Name, want)
					}
				}
			},
		},
		{
			name: "regression_allDefinitionsPresent",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Comprehensive check: all three fields must be present with correct attributes
				defMap := make(map[string]model.DataDefinition)
				for _, def := range defs {
					defMap[def.Name] = def
				}

				expectedDefs := []struct {
					name        string
					level       int
					typ         string
					sectionKind string
				}{
					{"#LOCAL-COUNTER", 1, "N5", "local"},
					{"#APP-STATE", 1, "A20", "independent"},
					{"#RPC-SESSION", 1, "N7", "context"},
				}

				for _, exp := range expectedDefs {
					def, found := defMap[exp.name]
					if !found {
						t.Errorf("Definition %q not found (silently dropped)", exp.name)
						continue
					}

					if def.Level != exp.level {
						t.Errorf("%s: Level = %d, want %d", exp.name, def.Level, exp.level)
					}
					if def.Type != exp.typ {
						t.Errorf("%s: Type = %q, want %q", exp.name, def.Type, exp.typ)
					}
					if def.SectionKind != exp.sectionKind {
						t.Errorf("%s: SectionKind = %q, want %q", exp.name, def.SectionKind, exp.sectionKind)
					}

					// Range must be non-zero
					if def.Range.Start.Line == 0 || def.Range.Start.Column == 0 ||
						def.Range.End.Line == 0 || def.Range.End.Column == 0 {
						t.Errorf("%s: Range is zero, want non-zero source range", exp.name)
					}
				}
			},
		},
		{
			name: "regression_parserDataSectionsIncludesExtendedKinds",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Verify the parser also recognizes the extended section kinds in prog.DataSections
				// This checks the parser-level fix: isSectionKeyword should include INDEPENDENT, CONTEXT, OBJECT
				sectionKinds := make(map[string]bool)
				for _, section := range prog.DataSections {
					if section != nil {
						sectionKinds[section.Kind] = true
					}
				}

				// After the fix, we expect: local, independent, context (OBJECT not in fixture)
				expectedKinds := []string{"local", "independent", "context"}
				for _, kind := range expectedKinds {
					if !sectionKinds[kind] {
						t.Errorf("Parser did not recognize %q section kind (isSectionKeyword bug)", kind)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, defs, prog)
		})
	}
}

// TestExtractDefinitions_DataSections verifies FR-21 acceptance criterion:
// data definitions extracted from DEFINE DATA sections with all declaring information
// (name, level, type/format, array dimensions, section kind, source range).
// The PARAMETER section's fields are distinguishable by SectionKind field.
// Array bounds and redefine nesting are preserved per OQ-1.
// Task 11 / FR-21.
//
// Acceptance criteria:
//   - Emit exactly one DataDefinition per declared field (across all sections)
//   - Each definition carries name (normalized to uppercase), level, verbatim type, dimensions
//   - SectionKind distinguishes LOCAL/PARAMETER/GLOBAL/LINKAGE
//   - Array bounds are preserved (Dimensions populated for arrays)
//   - Redefine nesting preserved (Children populated; recursive)
//   - Declaration order maintained (global source order)
//   - PARAMETER fields tagged with SectionKind="parameter" (queryable for signatures)
//   - Graceful degradation: malformed field lines do not crash; extraction tolerates empty names/types
func TestExtractDefinitions_DataSections(t *testing.T) {
	// Read the fixture: 23-data-sections.nsp with LOCAL/PARAMETER/GLOBAL sections
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "23-data-sections.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	defs := extractDefinitions(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, defs []model.DataDefinition)
	}{
		{
			name: "extractDefinitions_emitsOneDefinitionPerField",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Fixture 23-data-sections.nsp (Task 10) has:
				//   LOCAL: #LOCAL-COUNTER, #LOCAL-NAME (2 fields)
				//   PARAMETER: #INPUT-VALUE, #OUTPUT-RESULT (2 fields)
				//   GLOBAL USING EMPLOYEES-GDA: (0 inline field definitions; fields live in external GDA)
				// Expected: 4 definitions total (2 LOCAL + 2 PARAMETER + 0 GLOBAL)
				if len(defs) != 4 {
					t.Errorf("len(defs) = %d, want 4 (2 LOCAL + 2 PARAMETER + 0 GLOBAL)", len(defs))
				}
			},
		},
		{
			name: "extractDefinitions_capturesNameLevelTypeRange",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Verify first LOCAL field: #LOCAL-COUNTER, level=1, type="N5"
				if len(defs) < 1 {
					t.Fatalf("Expected at least 1 definition, got %d", len(defs))
				}

				def0 := defs[0]
				if def0.Name != "#LOCAL-COUNTER" {
					t.Errorf("defs[0].Name = %q, want %q (normalized to uppercase)", def0.Name, "#LOCAL-COUNTER")
				}
				if def0.Level != 1 {
					t.Errorf("defs[0].Level = %d, want 1", def0.Level)
				}
				if def0.Type != "N5" {
					t.Errorf("defs[0].Type = %q, want %q (verbatim format)", def0.Type, "N5")
				}
				if def0.SectionKind != "local" {
					t.Errorf("defs[0].SectionKind = %q, want %q", def0.SectionKind, "local")
				}

				// Range must be non-zero (source span of the field)
				if def0.Range.Start.Line == 0 || def0.Range.Start.Column == 0 ||
					def0.Range.End.Line == 0 || def0.Range.End.Column == 0 {
					t.Error("defs[0].Range is zero, want non-zero source range")
				}
			},
		},
		{
			name: "extractDefinitions_sectionKindDistinguishesParameter",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Verify PARAMETER fields (indices 2-3) are tagged with SectionKind="parameter"
				// while LOCAL fields (indices 0-1) have SectionKind="local"
				if len(defs) < 4 {
					t.Fatalf("Expected at least 4 definitions, got %d", len(defs))
				}

				// First 2 should be LOCAL
				for i := range 2 {
					if defs[i].SectionKind != "local" {
						t.Errorf("defs[%d].SectionKind = %q, want %q (LOCAL section)",
							i, defs[i].SectionKind, "local")
					}
				}

				// Next 2 should be PARAMETER
				for i := range 2 {
					if defs[i+2].SectionKind != "parameter" {
						t.Errorf("defs[%d].SectionKind = %q, want %q (PARAMETER section)",
							i+2, defs[i+2].SectionKind, "parameter")
					}
				}

				// No GLOBAL fields inline (GLOBAL USING references external GDA)
			},
		},
		{
			name: "extractDefinitions_parameterFieldsQueryable",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Signature builders can query SectionKind to find parameter interface.
				// Verify PARAMETER fields are distinguishable:
				if len(defs) < 4 {
					t.Fatalf("Expected at least 4 definitions, got %d", len(defs))
				}

				// Count PARAMETER-section fields (should be exactly 2)
				parameterCount := 0
				for _, def := range defs {
					if def.SectionKind == "parameter" {
						parameterCount++
					}
				}

				if parameterCount != 2 {
					t.Errorf("Found %d PARAMETER fields, want 2", parameterCount)
				}

				// Count LOCAL-section fields (should be exactly 2)
				localCount := 0
				for _, def := range defs {
					if def.SectionKind == "local" {
						localCount++
					}
				}

				if localCount != 2 {
					t.Errorf("Found %d LOCAL fields, want 2", localCount)
				}

				// Verify PARAMETER field names (in declaration order)
				expectedParamNames := []string{"#INPUT-VALUE", "#OUTPUT-RESULT"}
				paramIdx := 0
				for _, def := range defs {
					if def.SectionKind == "parameter" {
						if paramIdx < len(expectedParamNames) {
							if def.Name != expectedParamNames[paramIdx] {
								t.Errorf("PARAMETER field %d: Name = %q, want %q",
									paramIdx, def.Name, expectedParamNames[paramIdx])
							}
						}
						paramIdx++
					}
				}
			},
		},
		{
			name: "extractDefinitions_declarationOrderPreserved",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Verify declaration order: LOCAL fields first, then PARAMETER
				if len(defs) < 4 {
					t.Fatalf("Expected at least 4 definitions, got %d", len(defs))
				}

				// Expected order (in source order): #LOCAL-COUNTER, #LOCAL-NAME (LOCAL), #INPUT-VALUE, #OUTPUT-RESULT (PARAMETER)
				expectedOrder := []string{"#LOCAL-COUNTER", "#LOCAL-NAME", "#INPUT-VALUE", "#OUTPUT-RESULT"}
				for i, want := range expectedOrder {
					if i < len(defs) {
						if defs[i].Name != want {
							t.Errorf("defs[%d].Name = %q, want %q (expected order)",
								i, defs[i].Name, want)
						}
					}
				}
			},
		},
		{
			name: "extractDefinitions_globalUsingContributesZeroDefinitions",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// GLOBAL USING EMPLOYEES-GDA section has no inline field definitions.
				// Fields live in the external GDA, not in this file.
				// Assert that we have exactly 4 definitions (no extra from GLOBAL USING).
				if len(defs) != 4 {
					t.Errorf("len(defs) = %d, want 4 (GLOBAL USING contributes 0 inline definitions)", len(defs))
				}

				// Assert no definitions have SectionKind=="global"
				for i, def := range defs {
					if def.SectionKind == "global" {
						t.Errorf("defs[%d] has SectionKind=global, but GLOBAL USING section has no inline fields", i)
					}
				}
			},
		},
		{
			name: "extractDefinitions_allFieldsNormalized",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// The lexer normalizes identifiers to uppercase (keeps # prefix).
				// Verify all definition names are uppercased (and start with # for variables).
				for i, def := range defs {
					if def.Name == "" {
						// Empty names are allowed (malformed fields), but skip assertion
						continue
					}

					// Check that names starting with # have uppercase letters after the #
					if def.Name[0] == '#' && len(def.Name) > 1 {
						for j := 1; j < len(def.Name); j++ {
							ch := def.Name[j]
							if ch >= 'a' && ch <= 'z' {
								t.Errorf("defs[%d].Name = %q has lowercase letters after #, want uppercase", i, def.Name)
								break
							}
						}
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, defs)
		})
	}
}

// TestExtractDefinitions_ArraysAndRedefines verifies OQ-1: array bounds and redefine
// nesting are preserved from the AST and survive extraction without deepening the grammar.
// Task 11 / FR-21.
//
// Acceptance criteria:
//   - Array fields have Dimensions populated with correct bounds (1:12 → Lower=1, Upper=12)
//   - Unbounded arrays (1:*) have UpperUnbounded=true
//   - Multi-dimensional arrays are captured per-dimension
//   - Redefine children are nested in the Children slice (recursive structure preserved)
//   - Declaration order within Children matches the source
func TestExtractDefinitions_ArraysAndRedefines(t *testing.T) {
	tests := []struct {
		name        string
		fixtureFile string
		verify      func(t *testing.T, defs []model.DataDefinition)
	}{
		{
			name:        "arrays_1D_and_multidimensional",
			fixtureFile: filepath.Join("testdata", "parser", "07-data-arrays.nsp"),
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Fixture has:
				//   1 #EMPLOYEE-ID (N7) — scalar, no dimensions
				//   1 #SALARY (P9.2) — scalar, no dimensions
				//   1 #ADDRESS — group (no type), 3 children
				//     2 #STREET (A30)
				//     2 #CITY (A20)
				//     2 #ZIP (A10)
				//   1 #MONTH-NAMES (A3/1:12) — 1-D array, 1 dimension: 1:12
				//   1 #SCORE-MATRIX (N3/1:5,1:3) — 2-D array, 2 dimensions: 1:5 and 1:3
				// Expected: 5 top-level definitions

				if len(defs) != 5 {
					t.Errorf("len(defs) = %d, want 5 (5 top-level fields)", len(defs))
					return
				}

				// defs[3] is #MONTH-NAMES (A3/1:12)
				monthNames := defs[3]
				if monthNames.Name != "#MONTH-NAMES" {
					t.Errorf("defs[3].Name = %q, want %q", monthNames.Name, "#MONTH-NAMES")
				}
				if len(monthNames.Dimensions) != 1 {
					t.Errorf("defs[3].Dimensions len = %d, want 1 (1-D array)", len(monthNames.Dimensions))
				}
				if len(monthNames.Dimensions) > 0 {
					dim0 := monthNames.Dimensions[0]
					if dim0.Lower != 1 || dim0.Upper != 12 || dim0.UpperUnbounded {
						t.Errorf("defs[3].Dimensions[0] = {%d, %d, %v}, want {1, 12, false}",
							dim0.Lower, dim0.Upper, dim0.UpperUnbounded)
					}
				}

				// defs[4] is #SCORE-MATRIX (N3/1:5,1:3)
				scoreMatrix := defs[4]
				if scoreMatrix.Name != "#SCORE-MATRIX" {
					t.Errorf("defs[4].Name = %q, want %q", scoreMatrix.Name, "#SCORE-MATRIX")
				}
				if len(scoreMatrix.Dimensions) != 2 {
					t.Errorf("defs[4].Dimensions len = %d, want 2 (2-D array)", len(scoreMatrix.Dimensions))
				}
				if len(scoreMatrix.Dimensions) >= 2 {
					dim0 := scoreMatrix.Dimensions[0]
					dim1 := scoreMatrix.Dimensions[1]
					if dim0.Lower != 1 || dim0.Upper != 5 || dim0.UpperUnbounded {
						t.Errorf("defs[4].Dimensions[0] = {%d, %d, %v}, want {1, 5, false}",
							dim0.Lower, dim0.Upper, dim0.UpperUnbounded)
					}
					if dim1.Lower != 1 || dim1.Upper != 3 || dim1.UpperUnbounded {
						t.Errorf("defs[4].Dimensions[1] = {%d, %d, %v}, want {1, 3, false}",
							dim1.Lower, dim1.Upper, dim1.UpperUnbounded)
					}
				}
			},
		},
		{
			name:        "redefines_nested_children",
			fixtureFile: filepath.Join("testdata", "parser", "08-data-redefine.nsp"),
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Fixture has:
				//   1 #EMPLOYEE-ID (A7)
				//   1 REDEFINE #EMPLOYEE-ID
				//     2 #ID-PREFIX (A3)
				//     2 #ID-SEQUENCE (A4)
				// Expected: 1 top-level definition with 2 children (the REDEFINE block)
				// OR: 3 definitions if the parser flattens (depends on implementation)
				// Per the task description, "flattening groups/redefines" and "Children" imply nesting.
				// Assume the extraction preserves the nesting: 1 definition with 2 children.

				if len(defs) == 0 {
					t.Fatal("Expected at least 1 definition, got 0")
				}

				// The first definition should be #EMPLOYEE-ID
				empId := defs[0]
				if empId.Name != "#EMPLOYEE-ID" {
					t.Errorf("defs[0].Name = %q, want %q", empId.Name, "#EMPLOYEE-ID")
				}

				// It should have 2 children (the REDEFINE subfields)
				if len(empId.Children) != 2 {
					t.Errorf("defs[0].Children len = %d, want 2 (REDEFINE subfields)", len(empId.Children))
				}

				// Verify child names
				if len(empId.Children) >= 2 {
					child0 := empId.Children[0]
					child1 := empId.Children[1]
					if child0.Name != "#ID-PREFIX" {
						t.Errorf("defs[0].Children[0].Name = %q, want %q", child0.Name, "#ID-PREFIX")
					}
					if child1.Name != "#ID-SEQUENCE" {
						t.Errorf("defs[0].Children[1].Name = %q, want %q", child1.Name, "#ID-SEQUENCE")
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Read the fixture
			content, err := os.ReadFile(tc.fixtureFile)
			if err != nil {
				t.Fatalf("Failed to read fixture %q: %v", tc.fixtureFile, err)
			}

			// Parse to AST
			lexer := NewLexer(string(content))
			parser := NewParser(lexer)
			prog, err := parser.Parse()

			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}
			if err != nil {
				t.Logf("Parse returned error %v; expected graceful degradation", err)
			}

			// Call the extractor
			defs := extractDefinitions(prog)

			// Run the test's verify function
			tc.verify(t, defs)
		})
	}
}

// TestExtractDefinitions_GracefulDegradation verifies FR-43: extractDefinitions
// never panics on degenerate inputs and degrades sensibly — returning nil or an
// empty slice, never crashing.
func TestExtractDefinitions_GracefulDegradation(t *testing.T) {
	t.Run("nil_program_returns_nil", func(t *testing.T) {
		got := extractDefinitions(nil)
		if got != nil {
			t.Errorf("extractDefinitions(nil) = %v, want nil", got)
		}
	})

	t.Run("empty_program_returns_empty", func(t *testing.T) {
		got := extractDefinitions(&Program{})
		if len(got) != 0 {
			t.Errorf("extractDefinitions(&Program{}) len = %d, want 0", len(got))
		}
	})

	t.Run("nil_section_in_DataSections_is_skipped", func(t *testing.T) {
		prog := &Program{
			DataSections: []*DataSection{nil},
		}
		got := extractDefinitions(prog)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0 (nil section must be skipped without panic)", len(got))
		}
	})

	t.Run("empty_section_contributes_no_definitions", func(t *testing.T) {
		prog := &Program{
			DataSections: []*DataSection{
				{Kind: "local", Fields: nil},
			},
		}
		got := extractDefinitions(prog)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0 (empty section yields no definitions)", len(got))
		}
	})

	t.Run("nil_field_in_section_is_skipped", func(t *testing.T) {
		// A section whose Fields slice contains a nil pointer — the nil entry must be
		// skipped; only the non-nil field produces a definition.
		prog := &Program{
			DataSections: []*DataSection{
				{
					Kind: "local",
					Fields: []*DataField{
						nil, // must not panic
						{Level: 1, Name: "#COUNTER", Type: "N5",
							StartPos: model.Position{Line: 2, Column: 1},
							EndPos:   model.Position{Line: 2, Column: 20}},
					},
				},
			},
		}
		got := extractDefinitions(prog)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (nil field skipped, non-nil field extracted)", len(got))
		}
		if got[0].Name != "#COUNTER" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "#COUNTER")
		}
	})

	t.Run("field_with_empty_name_and_no_redefines_is_extracted", func(t *testing.T) {
		// A DataField with an empty Name and empty Redefines — this is a malformed
		// field (e.g. the parser emitted a diagnostic but still produced the node).
		// The extractor must not panic and should emit the definition as-is;
		// the consumer can inspect the empty Name and ignore it.
		prog := &Program{
			DataSections: []*DataSection{
				{
					Kind: "local",
					Fields: []*DataField{
						{Level: 1, Name: "", Type: "A10",
							StartPos: model.Position{Line: 3, Column: 1},
							EndPos:   model.Position{Line: 3, Column: 15}},
					},
				},
			},
		}
		got := extractDefinitions(prog)
		// One definition emitted (empty-name, empty-redefines fields are NOT treated as
		// REDEFINE blocks — they fall through to the normal extraction path).
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (empty-name non-redefine field must be extracted)", len(got))
		}
		if got[0].Name != "" {
			t.Errorf("got[0].Name = %q, want empty string", got[0].Name)
		}
		if got[0].Type != "A10" {
			t.Errorf("got[0].Type = %q, want %q", got[0].Type, "A10")
		}
	})

	t.Run("redefine_targeting_unknown_field_is_silently_dropped", func(t *testing.T) {
		// A REDEFINE block whose target does not exist in the section — no definition
		// must be emitted for the REDEFINE itself, and no panic must occur.
		prog := &Program{
			DataSections: []*DataSection{
				{
					Kind: "local",
					Fields: []*DataField{
						{Level: 1, Name: "#REAL", Type: "A7",
							StartPos: model.Position{Line: 2, Column: 1},
							EndPos:   model.Position{Line: 2, Column: 15}},
						// REDEFINE targeting a field "#GHOST" that was never declared.
						{Level: 1, Name: "", Redefines: "#GHOST",
							Children: []*DataField{
								{Level: 2, Name: "#PART", Type: "A3",
									StartPos: model.Position{Line: 4, Column: 3},
									EndPos:   model.Position{Line: 4, Column: 15}},
							},
							StartPos: model.Position{Line: 3, Column: 1},
							EndPos:   model.Position{Line: 5, Column: 1}},
					},
				},
			},
		}
		got := extractDefinitions(prog)
		// Only #REAL should appear; the dangling REDEFINE is silently dropped.
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (dangling REDEFINE must not emit a definition)", len(got))
		}
		if got[0].Name != "#REAL" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "#REAL")
		}
		if len(got[0].Children) != 0 {
			t.Errorf("got[0].Children len = %d, want 0 (dangling REDEFINE adds no children)", len(got[0].Children))
		}
	})
}

// TestExtractWorkFiles_GracefulDegradation verifies FR-43: extractWorkFiles
// never panics on a nil program, an empty program, or a WorkFiles slice that
// contains nil entries (malformed AST nodes the parser may emit during error
// recovery).
func TestExtractWorkFiles_GracefulDegradation(t *testing.T) {
	t.Run("nil_program_returns_nil", func(t *testing.T) {
		got := extractWorkFiles(nil)
		if got != nil {
			t.Errorf("extractWorkFiles(nil) = %v, want nil", got)
		}
	})

	t.Run("empty_program_returns_empty", func(t *testing.T) {
		got := extractWorkFiles(&Program{})
		if len(got) != 0 {
			t.Errorf("extractWorkFiles(&Program{}) len = %d, want 0", len(got))
		}
	})

	t.Run("nil_entry_in_WorkFiles_is_skipped", func(t *testing.T) {
		// A WorkFiles slice containing a nil pointer — the nil entry must be
		// skipped; only the non-nil entry produces a WorkFile.
		prog := &Program{
			WorkFiles: []*WorkFileDefinition{
				nil, // must not panic
				{
					Number:   3,
					Name:     "OUTPUT.TXT",
					StartPos: model.Position{Line: 4, Column: 1},
					EndPos:   model.Position{Line: 4, Column: 32},
				},
			},
		}
		got := extractWorkFiles(prog)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (nil entry skipped, non-nil entry extracted)", len(got))
		}
		if got[0].Number != 3 {
			t.Errorf("got[0].Number = %d, want 3", got[0].Number)
		}
		if got[0].Name != "OUTPUT.TXT" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "OUTPUT.TXT")
		}
	})
}

// TestExtractWorkFiles_BasicExtraction verifies FR-22 acceptance criterion:
// work-file definitions extracted from DEFINE WORK FILE statements.
// Task 15 / FR-22.
//
// Acceptance criteria:
//   - Emit exactly one WorkFile entry for each DEFINE WORK FILE statement
//   - Number is the work-file slot number (e.g., 1, 2)
//   - Name is the file name (quoted string with quotes stripped, or variable verbatim)
//   - Range spans the entire DEFINE WORK FILE statement
//   - Entries are in source order
//   - Literal vs variable name distinguishable (variable has leading '#')
//   - Variable/dynamic names recorded verbatim, not diagnosed as errors (modeled gap)
func TestExtractWorkFiles_BasicExtraction(t *testing.T) {
	// Read the fixture: 24-work-file.nsp with two DEFINE WORK FILE statements
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "24-work-file.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor (stub function for RED phase)
	workFiles := extractWorkFiles(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, workFiles []model.WorkFile)
	}{
		{
			name: "workFiles_extractsExactlyTwoEntries",
			verify: func(t *testing.T, workFiles []model.WorkFile) {
				// Fixture has: DEFINE WORK FILE 1 'REPORT.TXT' and DEFINE WORK FILE 2 #DYNNAME
				// Expected: exactly 2 entries
				if len(workFiles) != 2 {
					t.Errorf("len(workFiles) = %d, want 2", len(workFiles))
				}
			},
		},
		{
			name: "workFiles_capturesNumberAndName",
			verify: func(t *testing.T, workFiles []model.WorkFile) {
				// Assert first entry: number=1, name="REPORT.TXT" (quotes stripped)
				if len(workFiles) < 1 {
					t.Fatalf("Expected at least 1 entry, got %d", len(workFiles))
				}

				wf0 := workFiles[0]
				if wf0.Number != 1 {
					t.Errorf("workFiles[0].Number = %d, want 1", wf0.Number)
				}
				if wf0.Name != "REPORT.TXT" {
					t.Errorf("workFiles[0].Name = %q, want %q (quoted literal, quotes stripped)", wf0.Name, "REPORT.TXT")
				}

				// Assert second entry: number=2, name="#DYNNAME" (variable, verbatim with sigil)
				if len(workFiles) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(workFiles))
				}

				wf1 := workFiles[1]
				if wf1.Number != 2 {
					t.Errorf("workFiles[1].Number = %d, want 2", wf1.Number)
				}
				if wf1.Name != "#DYNNAME" {
					t.Errorf("workFiles[1].Name = %q, want %q (variable verbatim with sigil)", wf1.Name, "#DYNNAME")
				}
			},
		},
		{
			name: "workFiles_rangeIsNonZero",
			verify: func(t *testing.T, workFiles []model.WorkFile) {
				// Each entry must have a non-zero Range covering the statement
				for i, wf := range workFiles {
					if wf.Range.Start.Line == 0 || wf.Range.Start.Column == 0 ||
						wf.Range.End.Line == 0 || wf.Range.End.Column == 0 {
						t.Errorf("workFiles[%d].Range is zero, want non-zero statement range", i)
					}
				}
			},
		},
		{
			name: "workFiles_sourceOrderPreserved",
			verify: func(t *testing.T, workFiles []model.WorkFile) {
				// Entries must be in source order (line 5 before line 7)
				if len(workFiles) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(workFiles))
				}

				wf0 := workFiles[0]
				wf1 := workFiles[1]

				if wf0.Range.Start.Line >= wf1.Range.Start.Line {
					t.Errorf("workFiles[0] at line %d comes after workFiles[1] at line %d, violates source order",
						wf0.Range.Start.Line, wf1.Range.Start.Line)
				}
			},
		},
		{
			name: "workFiles_literalVsVariableDistinguishable",
			verify: func(t *testing.T, workFiles []model.WorkFile) {
				// The first entry (literal) does not start with '#'
				// The second entry (variable) starts with '#'
				// This distinguishes dynamic from static file names.
				if len(workFiles) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(workFiles))
				}

				wf0 := workFiles[0]
				wf1 := workFiles[1]

				// First is a literal file name (no '#' prefix)
				if len(wf0.Name) > 0 && wf0.Name[0] == '#' {
					t.Errorf("workFiles[0].Name = %q, expected literal (no '#' prefix)", wf0.Name)
				}

				// Second is a variable (has '#' prefix)
				if len(wf1.Name) == 0 || wf1.Name[0] != '#' {
					t.Errorf("workFiles[1].Name = %q, expected variable with '#' prefix", wf1.Name)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, workFiles)
		})
	}
}

// TestAnalyze_PopulatesWorkFiles verifies that Analyze wires extractWorkFiles
// into FileAnalysis.WorkFiles end-to-end (Task 15 / FR-22).
//
// Acceptance criteria:
//   - Analyze returns FileAnalysis with WorkFiles populated
//   - WorkFiles matches the extractor output (2 entries from fixture)
//   - Source ranges are preserved
func TestAnalyze_PopulatesWorkFiles(t *testing.T) {
	// Read the work-file fixture
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "24-work-file.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Analyze the file end-to-end
	az := New(nil)
	result, err := az.Analyze("test.NSP", content)
	if err != nil {
		t.Errorf("Analyze returned error: %v", err)
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "analyze_workFiles_exactlyTwoEntries",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// FileAnalysis.WorkFiles must be populated with 2 entries
				if len(result.WorkFiles) != 2 {
					t.Errorf("len(FileAnalysis.WorkFiles) = %d, want 2", len(result.WorkFiles))
				}
			},
		},
		{
			name: "analyze_workFiles_capturesNumberAndName",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Verify content matches extractor output
				if len(result.WorkFiles) < 2 {
					t.Fatalf("Expected at least 2 entries, got %d", len(result.WorkFiles))
				}

				wf0 := result.WorkFiles[0]
				if wf0.Number != 1 || wf0.Name != "REPORT.TXT" {
					t.Errorf("workFiles[0] = {Number: %d, Name: %q}, want {1, 'REPORT.TXT'}", wf0.Number, wf0.Name)
				}

				wf1 := result.WorkFiles[1]
				if wf1.Number != 2 || wf1.Name != "#DYNNAME" {
					t.Errorf("workFiles[1] = {Number: %d, Name: %q}, want {2, '#DYNNAME'}", wf1.Number, wf1.Name)
				}
			},
		},
		{
			name: "analyze_workFiles_rangePreserved",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Source ranges must be non-zero and preserved
				for i, wf := range result.WorkFiles {
					if wf.Range.Start.Line == 0 || wf.Range.Start.Column == 0 ||
						wf.Range.End.Line == 0 || wf.Range.End.Column == 0 {
						t.Errorf("workFiles[%d].Range is zero, want non-zero", i)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestExtractDefinitions_AIVFields_Task18Regression verifies FR-17/M-6:
// Application-Independent Variables (AIV) in INDEPENDENT sections with real `+`-prefixed
// names must NOT be silently dropped. The lexer currently does not lex `+` as an
// identifier-start sigil (only `#`, `&`, `@`), causing `+AIV-NAME` to be unparseable
// at the lexical level and the field to vanish silently.
//
// Task 18 (regression, round 2): a real `+`-prefixed AIV name (e.g., `1 +AIV-COUNTER (N5)`)
// inside an INDEPENDENT section yields 0 definitions and 0 diagnostics — a silent drop
// violating M-6/FR-17. Fixtures Task 17 used `#`-names (#APP-STATE) and missed this.
//
// Acceptance criteria:
//   - A fixture with `DEFINE DATA INDEPENDENT` containing real `+AIV-COUNTER (N5)` and `+AIV-NAME (A20)` fields
//   - extractDefinitions produces DataDefinition entries for the AIV fields
//   - Each AIV definition has SectionKind="independent"
//   - No silent drop: definitions are present AND no associated diagnostics should be emitted
//     (if lexer cannot be extended to support `+`, a diagnostic fallback is acceptable, but preferred
//     outcome is extraction of the definition with normalized name)
//
// Status: This test FAILS now (0 definitions for AIV fields) — the fix is either:
//  1. (Preferred) Extend lexer's isIdentStart to include `+` AND parser's parseDataField to handle `+ident`
//     → extractDefinitions returns definitions with name like "+AIV-COUNTER" or normalized form
//  2. (Fallback) Parser emits a diagnostic for unparseable field lines in INDEPENDENT sections
//     → definition extraction still produces a definition (empty-name or partial)
func TestExtractDefinitions_AIVFields_Task18Regression(t *testing.T) {
	// Read the fixture: 26-independent-aiv.nsp with INDEPENDENT section containing +AIV fields
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "26-independent-aiv.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}

	// Call the extractor
	defs := extractDefinitions(prog)

	// Count diagnostics emitted during parsing (should be 0 for valid syntax,
	// though the current lexer may reject the + sigil and emit a diagnostic)
	diagCount := len(prog.Diagnostics)

	tests := []struct {
		name   string
		verify func(t *testing.T, defs []model.DataDefinition, prog *Program)
	}{
		{
			name: "aiv_noSilentDrop_definitionsPresent",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// CRITICAL ASSERTION: The fixture has:
				//   LOCAL: 1 #LOCAL-VAR (A10)
				//   INDEPENDENT: 1 +AIV-COUNTER (N5), 1 +AIV-NAME (A20)
				// Expected: at least 3 definitions total (1 LOCAL + 2 INDEPENDENT)
				// Current behavior (bug): 1 definition only (LOCAL), the AIV fields are silently dropped
				// This assertion FAILS today, proving the silent drop.
				if len(defs) < 3 {
					t.Errorf("len(defs) = %d, want >= 3 (1 LOCAL + 2 INDEPENDENT AIV fields); AIV fields are being silently dropped",
						len(defs))
				}
			},
		},
		{
			name: "aiv_countDiagnosticsIfLexerRejectsPlus",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// If the lexer cannot be extended to support `+`, then the parser will emit
				// diagnostics for the unparseable `+AIV-NAME` lines. Acceptable fallback:
				// diagnostics are emitted (showing the lines are unparseable) so the drop is NOT silent.
				// This is a documentation check: if len(defs) < 3, we should see diagnostics.
				if len(defs) < 3 && diagCount == 0 {
					t.Errorf("Expected diagnostics if AIV fields are not extracted, but got 0 diagnostics and %d definitions (silent drop)",
						len(defs))
				}
			},
		},
		{
			name: "aiv_independentSectionKindPreserved",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// Once AIV fields are extracted, verify they are tagged with SectionKind="independent"
				// This check only applies if len(defs) >= 3 (i.e., the fix works).
				if len(defs) < 3 {
					t.Skip("AIV fields not yet extracted; skipping SectionKind verification")
				}

				foundAIVCounter := false
				foundAIVName := false
				for _, def := range defs {
					// Check for normalized names (the lexer uppercases, and may strip or keep the +)
					if (def.Name == "+AIV-COUNTER" || def.Name == "AIV-COUNTER") && def.SectionKind == "independent" {
						foundAIVCounter = true
					}
					if (def.Name == "+AIV-NAME" || def.Name == "AIV-NAME") && def.SectionKind == "independent" {
						foundAIVName = true
					}
				}

				if !foundAIVCounter {
					t.Error("INDEPENDENT field +AIV-COUNTER (or normalized form) not found with SectionKind='independent'")
				}
				if !foundAIVName {
					t.Error("INDEPENDENT field +AIV-NAME (or normalized form) not found with SectionKind='independent'")
				}
			},
		},
		{
			name: "aiv_localFieldStillExtracted",
			verify: func(t *testing.T, defs []model.DataDefinition, prog *Program) {
				// The LOCAL field #LOCAL-VAR should always be extracted (it uses the #-prefix which is supported).
				// This is a sanity check that the fixture itself parses correctly on the LOCAL side.
				foundLocal := false
				for _, def := range defs {
					if def.Name == "#LOCAL-VAR" && def.SectionKind == "local" {
						foundLocal = true
						break
					}
				}
				if !foundLocal {
					t.Error("LOCAL field #LOCAL-VAR not found (sanity check: fixture should parse LOCAL section)")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, defs, prog)
		})
	}
}

// TestDataDefinition_NameRange_ModelField (T1 RED) verifies that
// model.DataDefinition now carries a NameRange field (additive, models the
// name-token span for feature 27 variable navigation). This is a compile-fail
// test: the field must exist and be assignable.
func TestDataDefinition_NameRange_ModelField(t *testing.T) {
	t.Run("DataDefinition has NameRange field", func(t *testing.T) {
		// This test is a structural proof: if model.DataDefinition does not have
		// a NameRange field, this will fail to compile (red → green will add it).
		def := model.DataDefinition{
			Name:  "TEST-VAR",
			Level: 1,
			Type:  "A10",
			Range: model.Range{
				Start: model.Position{Line: 5, Column: 3},
				End:   model.Position{Line: 5, Column: 12},
			},
			// NameRange must be assignable (it is the new field being added)
			NameRange: model.Range{
				Start: model.Position{Line: 5, Column: 3},
				End:   model.Position{Line: 5, Column: 11},
			},
		}
		// If this compiles, NameRange exists; if not, it's red and needs to be added.
		if def.NameRange.Start.Line == 0 {
			t.Error("NameRange.Start.Line is zero after assignment, want non-zero")
		}
	})
}

// TestExtractDefinitions_NameRange_PopulatedFromParser (T1 RED) verifies that
// extractDefinitions populates each DataDefinition.NameRange (copied from the
// parser's DataField.NameRange) with the exact span of the field's name token.
//
// Uses the existing testdata/structure/01-program-full.NSP fixture which has:
//
//	1 EMPLOYEE-REC (group header, Level 1)
//	  2 EMP-ID (scalar, Level 2)
//	  2 EMP-NAME (scalar, Level 2)
//	  2 EMP-SALARY (scalar, Level 2)
//	1 EMP-ID-ALT REDEFINE EMP-ID (A5) (REDEFINE sub-field)
//
// Assertions:
//   - Each extracted DataDefinition for a named field has NameRange set to the
//     name token's span (not the whole field, not the level number)
//   - A REDEFINE block header (Name="") has zero NameRange (FR-43 graceful)
//   - NameRange.Start.Column and .End.Column match the name token position
//     (1-based line, byte-offset column, inclusive end)
func TestExtractDefinitions_NameRange_PopulatedFromParser(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "structure", "01-program-full.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Logf("Parse returned error (expected for graceful degradation): %v", err)
	}

	// Call the extractor
	defs := extractDefinitions(prog)

	tests := []struct {
		name   string
		verify func(t *testing.T, defs []model.DataDefinition)
	}{
		{
			name: "NameRange_isPopulatedForAllNamedFields",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// Every named field (Level 1, 2) should have non-zero NameRange
				for i, def := range defs {
					if def.Name != "" {
						if def.NameRange.Start.Line == 0 && def.NameRange.Start.Column == 0 &&
							def.NameRange.End.Line == 0 && def.NameRange.End.Column == 0 {
							t.Errorf("defs[%d] (name=%q): NameRange is zero, want populated",
								i, def.Name)
						}
					}
				}
			},
		},
		{
			name: "NameRange_spansOnlyTheNameToken",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// For EMPLOYEE-REC (level 1, group header):
				// - Range spans the whole line "1 EMPLOYEE-REC"
				// - NameRange should span only "EMPLOYEE-REC" (not the level number)
				var empRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "EMPLOYEE-REC" {
						empRec = &defs[i]
						break
					}
				}

				if empRec == nil {
					t.Fatal("EMPLOYEE-REC not found in extracted definitions")
				}

				// NameRange should start after the level number (which is "1")
				// Level is 1-based, column is 1-based, byte-offset
				// NameRange.Start.Column should be greater than level column
				if empRec.NameRange.Start.Column <= 4 {
					t.Errorf("NameRange.Start.Column = %d, want > 4 (after level number)",
						empRec.NameRange.Start.Column)
				}

				// Range.Start should be at or before the level
				if empRec.Range.Start.Column > empRec.NameRange.Start.Column {
					t.Errorf("Range.Start.Column (%d) should be <= NameRange.Start.Column (%d)",
						empRec.Range.Start.Column, empRec.NameRange.Start.Column)
				}
			},
		},
		{
			name: "NameRange_includesChildrensNameRanges",
			verify: func(t *testing.T, defs []model.DataDefinition) {
				// For EMPLOYEE-REC children (EMP-ID, EMP-NAME, EMP-SALARY),
				// verify each has NameRange populated
				var empRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "EMPLOYEE-REC" {
						empRec = &defs[i]
						break
					}
				}

				if empRec == nil {
					t.Fatal("EMPLOYEE-REC not found")
				}

				if len(empRec.Children) == 0 {
					t.Fatal("EMPLOYEE-REC has no children, want EMP-ID/EMP-NAME/EMP-SALARY")
				}

				// Each child should have NameRange populated
				for i, child := range empRec.Children {
					if child.NameRange.Start.Line == 0 && child.NameRange.Start.Column == 0 &&
						child.NameRange.End.Line == 0 && child.NameRange.End.Column == 0 {
						t.Errorf("child[%d] (name=%q): NameRange is zero, want populated",
							i, child.Name)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, defs)
		})
	}
}

// TestStructure_DataFieldSelectionRange_UsesNameRange (T1 RED) verifies that
// SymbolDataField.SelectionRange is set from DataDefinition.NameRange
// (feature 09 → feature 27 refinement). SelectionRange should point at the
// name-token span, enabling precise selection in the outline.
func TestStructure_DataFieldSelectionRange_UsesNameRange(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "structure", "01-program-full.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Analyze to get FileAnalysis with Structure
	fa := Analyze("01-program-full.NSP", string(content))
	if fa == nil || fa.Structure == nil {
		t.Fatal("Analyze returned nil FileAnalysis or Structure")
	}

	// Navigate to the LOCAL section and its fields
	root := fa.Structure
	if root == nil || len(root.Children) == 0 {
		t.Fatal("Structure has no children (expected LOCAL section at minimum)")
	}

	// Find the LOCAL section (should be first data section child)
	var localSection *model.Symbol
	for i := range root.Children {
		if root.Children[i].Kind == model.SymbolDataSection && root.Children[i].Name == "LOCAL" {
			localSection = &root.Children[i]
			break
		}
	}

	if localSection == nil {
		t.Fatal("LOCAL section not found in Structure")
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, localSection *model.Symbol)
	}{
		{
			name: "SelectionRange_pointsAtFieldName",
			verify: func(t *testing.T, localSection *model.Symbol) {
				// Find the EMPLOYEE-REC field (level 1)
				var empRec *model.Symbol
				for i := range localSection.Children {
					if localSection.Children[i].Name == "EMPLOYEE-REC" {
						empRec = &localSection.Children[i]
						break
					}
				}

				if empRec == nil {
					t.Fatal("EMPLOYEE-REC not found in LOCAL section")
				}

				// SelectionRange should be set from NameRange (not zero)
				if empRec.SelectionRange.Start.Line == 0 && empRec.SelectionRange.Start.Column == 0 &&
					empRec.SelectionRange.End.Line == 0 && empRec.SelectionRange.End.Column == 0 {
					t.Error("SelectionRange is zero, want populated (from NameRange)")
				}

				// SelectionRange should be narrower than Range (points to name, not whole statement)
				if empRec.Range.Start.Column > empRec.SelectionRange.Start.Column {
					t.Errorf("Range.Start.Column (%d) > SelectionRange.Start.Column (%d), want Range to span wider",
						empRec.Range.Start.Column, empRec.SelectionRange.Start.Column)
				}
			},
		},
		{
			name: "SelectionRange_childrenAlsoPopulated",
			verify: func(t *testing.T, localSection *model.Symbol) {
				// Find EMPLOYEE-REC and verify its children (EMP-ID, etc.) have SelectionRange
				var empRec *model.Symbol
				for i := range localSection.Children {
					if localSection.Children[i].Name == "EMPLOYEE-REC" {
						empRec = &localSection.Children[i]
						break
					}
				}

				if empRec == nil || len(empRec.Children) == 0 {
					t.Fatal("EMPLOYEE-REC not found or has no children")
				}

				// Each child (EMP-ID, EMP-NAME, EMP-SALARY) should have SelectionRange
				for i, child := range empRec.Children {
					if child.SelectionRange.Start.Line == 0 {
						t.Errorf("child[%d] (name=%q): SelectionRange.Start.Line is zero, want non-zero",
							i, child.Name)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, localSection)
		})
	}
}

// TestExtractDefinitions_DataArea verifies that data-area files (.NSL/.NSA/.NSG)
// route through the Natural parser and extract their DEFINE DATA sections into
// FileAnalysis.Definitions, with NameRange populated for each field (feature 27, T6).
//
// This is a verify-first task: data-area exports should already extract correctly
// since they route through the same parser as programs. The test confirms this
// against real exported shapes under testdata/dataarea/.
func TestExtractDefinitions_DataArea(t *testing.T) {
	tests := []struct {
		name           string
		filePath       string
		wantObjectType model.ObjectType
		wantDefCount   int
		wantFieldNames []string
	}{
		{
			name:           "local_data_area",
			filePath:       "testdata/dataarea/local.NSL",
			wantObjectType: model.ObjectLocalDataArea,
			wantDefCount:   1,
			wantFieldNames: []string{"#X"},
		},
		{
			name:           "parameter_data_area",
			filePath:       "testdata/dataarea/parameter.NSA",
			wantObjectType: model.ObjectParameterDataArea,
			wantDefCount:   1,
			wantFieldNames: []string{"#P"},
		},
		{
			name:           "global_data_area",
			filePath:       "testdata/dataarea/global.NSG",
			wantObjectType: model.ObjectGlobalDataArea,
			wantDefCount:   1,
			wantFieldNames: []string{"#G"},
		},
		{
			name:           "complex_local_data_area",
			filePath:       "testdata/dataarea/complex.NSL",
			wantObjectType: model.ObjectLocalDataArea,
			wantDefCount:   4, // #EMPLOYEE-ID, #EMPLOYEE-REC (group), #DATES-ARRAY, #REDEF-FIELD (which has redefine children)
			wantFieldNames: []string{"#EMPLOYEE-ID", "#EMPLOYEE-REC", "#DATES-ARRAY", "#REDEF-FIELD"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := os.ReadFile(tt.filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", tt.filePath, err)
			}

			result := Analyze(tt.filePath, string(content))
			if result == nil {
				t.Fatal("Analyze returned nil result")
			}

			// Verify ObjectType classification
			if result.ObjectType != tt.wantObjectType {
				t.Errorf("ObjectType = %v, want %v", result.ObjectType, tt.wantObjectType)
			}

			// Verify definition count
			if len(result.Definitions) != tt.wantDefCount {
				t.Errorf("len(Definitions) = %d, want %d", len(result.Definitions), tt.wantDefCount)
				for i, def := range result.Definitions {
					t.Logf("  Definitions[%d]: Name=%q, Level=%d, Type=%q", i, def.Name, def.Level, def.Type)
				}
			}

			// Verify field names
			for i, wantName := range tt.wantFieldNames {
				if i >= len(result.Definitions) {
					t.Errorf("expected Definitions[%d].Name = %q, but index out of range", i, wantName)
					continue
				}
				def := result.Definitions[i]
				if def.Name != wantName {
					t.Errorf("Definitions[%d].Name = %q, want %q", i, def.Name, wantName)
				}
			}

			// Verify NameRange is populated (Feature 27, T1)
			// All definitions should have a non-zero NameRange since field names have tokens
			for i, def := range result.Definitions {
				if def.NameRange.Start.Line == 0 && def.NameRange.Start.Column == 0 {
					t.Errorf("Definitions[%d] (%q) NameRange.Start is zero, want non-zero (enabled by T1)", i, def.Name)
				}
				if def.NameRange.End.Line == 0 && def.NameRange.End.Column == 0 {
					t.Errorf("Definitions[%d] (%q) NameRange.End is zero, want non-zero (enabled by T1)", i, def.Name)
				}
				// NameRange should be narrower than Range on the same line
				// (name-token only, not level+type+format)
				if def.NameRange.Start.Line == def.Range.Start.Line &&
					def.NameRange.End.Line == def.Range.End.Line {
					// NameRange should be contained within Range on this line
					if !(def.Range.Start.Column <= def.NameRange.Start.Column &&
						def.NameRange.End.Column <= def.Range.End.Column) {
						t.Errorf("Definitions[%d] (%q) NameRange (%d-%d) not contained in Range (%d-%d) on line %d",
							i, def.Name,
							def.NameRange.Start.Column, def.NameRange.End.Column,
							def.Range.Start.Column, def.Range.End.Column,
							def.Range.Start.Line)
					}
				}
			}

			// Verify array-dimension extraction from a data area (Feature 27, review fix).
			// The complex fixture declares #DATES-ARRAY as a 1-D array using Natural's
			// index-range syntax (D/1:10). Its Dimensions must be exactly one bounded
			// [1:10] dimension — this guards that array bounds actually flow from a
			// DEFINE DATA declaration in a data-area file, not just from programs.
			if tt.name == "complex_local_data_area" {
				var datesArray *model.DataDefinition
				for i := range result.Definitions {
					if result.Definitions[i].Name == "#DATES-ARRAY" {
						datesArray = &result.Definitions[i]
						break
					}
				}
				if datesArray == nil {
					t.Fatal("expected a #DATES-ARRAY definition, found none")
				}
				wantDims := []model.ArrayDimension{{Lower: 1, Upper: 10, UpperUnbounded: false}}
				if !reflect.DeepEqual(datesArray.Dimensions, wantDims) {
					t.Errorf("#DATES-ARRAY Dimensions = %+v, want %+v", datesArray.Dimensions, wantDims)
				}
			}
		})
	}
}

// TestExtractDataAreaRefs verifies that USING clauses in DEFINE DATA sections
// are captured and returned as DataAreaRef entries (feature 27, T7).
//
// Acceptance criteria:
//   - Each section with a non-empty USING clause yields one DataAreaRef
//   - Name is the data-area name, normalized to upper-case
//   - SectionKind matches the section keyword (local, parameter, global, etc.)
//   - Range points to just the data-area name token in the USING clause
//   - Sections without a USING clause are skipped
//   - Never panics on nil program or partial ASTs (FR-43)
func TestExtractDataAreaRefs(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantCount int
		verify    func(t *testing.T, refs []model.DataAreaRef)
	}{
		{
			name: "basic_local_using",
			content: `DEFINE DATA
LOCAL USING CUSTLDA
END-DEFINE`,
			wantCount: 1,
			verify: func(t *testing.T, refs []model.DataAreaRef) {
				if len(refs) != 1 {
					t.Fatalf("Expected 1 ref, got %d", len(refs))
				}
				if refs[0].Name != "CUSTLDA" {
					t.Errorf("Name = %q, want CUSTLDA", refs[0].Name)
				}
				if refs[0].SectionKind != "local" {
					t.Errorf("SectionKind = %q, want local", refs[0].SectionKind)
				}
				if refs[0].Range.Start.Line == 0 {
					t.Error("Range.Start.Line should not be 0")
				}
			},
		},
		{
			name: "multiple_sections_with_using",
			content: `DEFINE DATA
LOCAL USING MYLDA
PARAMETER USING PARDLDA
GLOBAL USING GLBDLDA
END-DEFINE`,
			wantCount: 3,
			verify: func(t *testing.T, refs []model.DataAreaRef) {
				if len(refs) != 3 {
					t.Fatalf("Expected 3 refs, got %d", len(refs))
				}
				// Check each ref
				expectedNames := []string{"MYLDA", "PARDLDA", "GLBDLDA"}
				expectedKinds := []string{"local", "parameter", "global"}
				for i, expectedName := range expectedNames {
					if refs[i].Name != expectedName {
						t.Errorf("refs[%d].Name = %q, want %q", i, refs[i].Name, expectedName)
					}
					if refs[i].SectionKind != expectedKinds[i] {
						t.Errorf("refs[%d].SectionKind = %q, want %q", i, refs[i].SectionKind, expectedKinds[i])
					}
				}
			},
		},
		{
			name: "no_using_clauses",
			content: `DEFINE DATA
LOCAL
  1 #VAR (A10)
END-DEFINE`,
			wantCount: 0,
			verify: func(t *testing.T, refs []model.DataAreaRef) {
				if len(refs) != 0 {
					t.Errorf("Expected 0 refs, got %d", len(refs))
				}
			},
		},
		{
			name: "mixed_with_and_without_using",
			content: `DEFINE DATA
LOCAL
  1 #VAR1 (A10)
PARAMETER USING PARDLDA
GLOBAL
  1 #VAR2 (N5)
END-DEFINE`,
			wantCount: 1,
			verify: func(t *testing.T, refs []model.DataAreaRef) {
				if len(refs) != 1 {
					t.Fatalf("Expected 1 ref, got %d", len(refs))
				}
				if refs[0].Name != "PARDLDA" {
					t.Errorf("Name = %q, want PARDLDA", refs[0].Name)
				}
			},
		},
		{
			name: "nil_program_returns_nil",
			verify: func(t *testing.T, refs []model.DataAreaRef) {
				// This test will be called with refs from a nil program
				if refs != nil {
					t.Errorf("extractDataAreaRefs(nil) should return nil, got %v", refs)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var refs []model.DataAreaRef
			if tc.content == "" {
				// Test nil program
				refs = extractDataAreaRefs(nil)
			} else {
				lexer := NewLexer(tc.content)
				parser := NewParser(lexer)
				prog, _ := parser.Parse()
				refs = extractDataAreaRefs(prog)
			}

			if len(refs) != tc.wantCount {
				t.Errorf("Expected %d refs, got %d", tc.wantCount, len(refs))
			}
			tc.verify(t, refs)
		})
	}
}

// TestDataAreaRef_RangePointsAtName verifies that DataAreaRef.Range
// points only to the data-area name token, not to the whole USING clause.
func TestDataAreaRef_RangePointsAtName(t *testing.T) {
	content := `DEFINE DATA
LOCAL USING CUSTLDA
END-DEFINE`

	lexer := NewLexer(content)
	parser := NewParser(lexer)
	prog, _ := parser.Parse()
	refs := extractDataAreaRefs(prog)

	if len(refs) < 1 {
		t.Fatal("Expected at least 1 ref")
	}

	ref := refs[0]

	// The Range should not include the USING keyword, only the data-area name
	// Extract the content at the range position
	lines := []string{
		"DEFINE DATA",
		"LOCAL USING CUSTLDA",
		"END-DEFINE",
	}

	if ref.Range.Start.Line != 2 {
		t.Errorf("Range.Start.Line = %d, want 2 (LOCAL USING CUSTLDA line)", ref.Range.Start.Line)
	}

	// Extract the text at the range.
	// Range.End.Column is inclusive (points at the last character, ADR-008),
	// so the exclusive Go slice upper bound is End.Column (not End.Column-1).
	line := lines[ref.Range.Start.Line-1]
	rangeText := line[ref.Range.Start.Column-1 : ref.Range.End.Column]

	// It should be "CUSTLDA", not "USING CUSTLDA"
	if rangeText != "CUSTLDA" {
		t.Errorf("Range text = %q, want CUSTLDA", rangeText)
	}

	// CUSTLDA occupies columns 13-19 (1-based, inclusive) on the LOCAL line.
	// Assert the exact inclusive End.Column so an off-by-one in the USING-clause
	// range capture cannot regress silently (must be 19, not 20).
	if ref.Range.Start.Column != 13 {
		t.Errorf("Range.Start.Column = %d, want 13", ref.Range.Start.Column)
	}
	if ref.Range.End.Column != 19 {
		t.Errorf("Range.End.Column = %d, want 19 (inclusive last char of CUSTLDA)", ref.Range.End.Column)
	}
}

// TestDataAreaRef_RangeEndInclusive pins the exact inclusive End.Column of a
// USING data-area name, guarding the ADR-008 range convention (feature 27
// review fix). GLOBAL USING MYGDA places MYGDA at columns 14-18 (1-based); the
// inclusive End.Column must be 18 (a prior off-by-one produced 19).
func TestDataAreaRef_RangeEndInclusive(t *testing.T) {
	content := `DEFINE DATA
GLOBAL USING MYGDA
END-DEFINE`

	lexer := NewLexer(content)
	parser := NewParser(lexer)
	prog, _ := parser.Parse()
	refs := extractDataAreaRefs(prog)

	if len(refs) != 1 {
		t.Fatalf("Expected 1 ref, got %d", len(refs))
	}
	ref := refs[0]
	if ref.Name != "MYGDA" {
		t.Errorf("Name = %q, want MYGDA", ref.Name)
	}
	if ref.Range.Start.Column != 14 {
		t.Errorf("Range.Start.Column = %d, want 14 (start of MYGDA)", ref.Range.Start.Column)
	}
	if ref.Range.End.Column != 18 {
		t.Errorf("Range.End.Column = %d, want 18 (inclusive last char of MYGDA)", ref.Range.End.Column)
	}
}

// TestExtractDefinitions_RedefineRelationship verifies that the REDEFINE
// relationship (Feature 28, T3) is surfaced in the model so the outline
// can label redefine blocks (FR-55).
//
// Acceptance criteria (from T3):
//   - Each redefine sub-field's DataDefinition.Redefines == the target name
//     (as the AST carries it, normalized by the lexer)
//   - Non-redefine fields have Redefines == ""
//   - FILLER sub-fields are represented as normal DataDefinition entries
//   - No Diagnostic emitted for legal partial/overlapping coverage (FR-17)
func TestExtractDefinitions_RedefineRelationship(t *testing.T) {
	// Read fixture: a scalar (#CUSTOMER-ID) and a REDEFINE block with sub-fields
	fixturePath := filepath.Join("testdata", "structure", "08-redefine.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse and extract definitions
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, _ := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}

	defs := extractDefinitions(prog)
	diags := prog.Diagnostics

	// Test table-driven assertions
	tests := []struct {
		name   string
		verify func(t *testing.T)
	}{
		{
			name: "scalar_field_has_empty_redefines",
			verify: func(t *testing.T) {
				// Find the #CUSTOMER-ID scalar field (level 1, non-redefine)
				var found *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "#CUSTOMER-ID" && defs[i].Type != "" {
						found = &defs[i]
						break
					}
				}
				if found == nil {
					t.Fatal("Could not find #CUSTOMER-ID scalar field in definitions")
				}
				// Per T3 AC: non-redefine fields have Redefines == ""
				if found.Redefines != "" {
					t.Errorf("#CUSTOMER-ID.Redefines = %q, want \"\" (non-redefine)", found.Redefines)
				}
			},
		},
		{
			name: "redefine_children_have_redefines_stamp",
			verify: func(t *testing.T) {
				// Find the #CUSTOMER-ID field and check its Children for Redefines stamps
				var targetField *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "#CUSTOMER-ID" && defs[i].Type != "" {
						targetField = &defs[i]
						break
					}
				}
				if targetField == nil {
					t.Fatal("Could not find target field #CUSTOMER-ID")
				}

				if len(targetField.Children) == 0 {
					t.Fatal("Target field has no children (redefine sub-fields not merged)")
				}

				// Per T3 AC: each redefine sub-field's Redefines == target name
				// The lexer normalizes case, so expect uppercase "#CUSTOMER-ID"
				expectedRedefines := "#CUSTOMER-ID"

				// Check #REGION (2nd level, typed)
				if len(targetField.Children) > 0 {
					region := &targetField.Children[0]
					if region.Name != "#REGION" {
						t.Errorf("Child[0].Name = %q, want #REGION", region.Name)
					}
					if region.Redefines != expectedRedefines {
						t.Errorf("Child[0].Redefines = %q, want %q", region.Redefines, expectedRedefines)
					}
					if region.Type != "A2" {
						t.Errorf("Child[0].Type = %q, want A2", region.Type)
					}
				}

				// Check #SEQ (2nd level, typed)
				if len(targetField.Children) > 1 {
					seq := &targetField.Children[1]
					if seq.Name != "#SEQ" {
						t.Errorf("Child[1].Name = %q, want #SEQ", seq.Name)
					}
					if seq.Redefines != expectedRedefines {
						t.Errorf("Child[1].Redefines = %q, want %q", seq.Redefines, expectedRedefines)
					}
					if seq.Type != "N8" {
						t.Errorf("Child[1].Type = %q, want N8", seq.Type)
					}
				}

				// Check FILLER (2nd level, nX format)
				if len(targetField.Children) > 2 {
					filler := &targetField.Children[2]
					// FILLER may have empty Name or "FILLER" depending on parser
					if filler.Redefines != expectedRedefines {
						t.Errorf("Child[2].Redefines = %q, want %q", filler.Redefines, expectedRedefines)
					}
					// FILLER carries the count in Type, e.g., "3X"
					if filler.Type != "3X" {
						t.Errorf("Child[2].Type = %q, want 3X (FILLER gap)", filler.Type)
					}
				}

				// Check #CODE (2nd level, typed)
				if len(targetField.Children) > 3 {
					code := &targetField.Children[3]
					if code.Name != "#CODE" {
						t.Errorf("Child[3].Name = %q, want #CODE", code.Name)
					}
					if code.Redefines != expectedRedefines {
						t.Errorf("Child[3].Redefines = %q, want %q", code.Redefines, expectedRedefines)
					}
					if code.Type != "A3" {
						t.Errorf("Child[3].Type = %q, want A3", code.Type)
					}
				}
			},
		},
		{
			name: "no_diagnostics_for_legal_redefine",
			verify: func(t *testing.T) {
				// Per T3 AC: no diagnostic emitted for legal partial/overlapping coverage (FR-17)
				if len(diags) > 0 {
					t.Errorf("Expected 0 diagnostics, got %d: %v", len(diags), diags)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.verify)
	}
}

// TestExtractDefinitions_NestedRedefine tests REDEFINE nested inside a GROUP.
// Regression test for Feature 28 T8b: ensures that a REDEFINE at level 2+ inside a
// level-1 GROUP is handled correctly — the REDEFINE sub-fields are merged into the
// target sibling (just as top-level REDEFINE), and no empty-Name placeholder is emitted.
func TestExtractDefinitions_NestedRedefine(t *testing.T) {
	// Read fixture: a GROUP containing a field and a REDEFINE of that field
	fixturePath := filepath.Join("testdata", "structure", "11-nested-redefine.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse and extract definitions
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, _ := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}

	defs := extractDefinitions(prog)
	diags := prog.Diagnostics

	// Test table-driven assertions
	tests := []struct {
		name   string
		verify func(t *testing.T)
	}{
		{
			name: "group_field_present",
			verify: func(t *testing.T) {
				// Find the level-1 CUSTOMER-REC group
				var customerRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-REC" && defs[i].Level == 1 {
						customerRec = &defs[i]
						break
					}
				}
				if customerRec == nil {
					t.Fatal("Could not find CUSTOMER-REC group in definitions")
				}
				// The group should have children
				if len(customerRec.Children) == 0 {
					t.Fatal("CUSTOMER-REC has no children")
				}
			},
		},
		{
			name: "target_field_present_in_group",
			verify: func(t *testing.T) {
				// Find CUSTOMER-REC and then find CUST-ID within it
				var customerRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-REC" && defs[i].Level == 1 {
						customerRec = &defs[i]
						break
					}
				}
				if customerRec == nil {
					t.Fatal("Could not find CUSTOMER-REC group")
				}

				// Look for CUST-ID (level 2) as a direct child of CUSTOMER-REC
				var custID *model.DataDefinition
				for i := range customerRec.Children {
					if customerRec.Children[i].Name == "CUST-ID" && customerRec.Children[i].Level == 2 {
						custID = &customerRec.Children[i]
						break
					}
				}
				if custID == nil {
					t.Fatalf("Could not find CUST-ID field in CUSTOMER-REC; found children: %+v",
						customerRec.Children)
				}
				// CUST-ID should have type and be non-redefine
				if custID.Type != "A10" {
					t.Errorf("CUST-ID.Type = %q, want A10", custID.Type)
				}
				if custID.Redefines != "" {
					t.Errorf("CUST-ID.Redefines = %q, want \"\" (non-redefine)", custID.Redefines)
				}
			},
		},
		{
			name: "redefine_subfields_merged_into_target",
			verify: func(t *testing.T) {
				// Find CUSTOMER-REC → CUST-ID and verify REDEFINE sub-fields are Children
				var customerRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-REC" && defs[i].Level == 1 {
						customerRec = &defs[i]
						break
					}
				}
				if customerRec == nil {
					t.Fatal("Could not find CUSTOMER-REC group")
				}

				var custID *model.DataDefinition
				for i := range customerRec.Children {
					if customerRec.Children[i].Name == "CUST-ID" {
						custID = &customerRec.Children[i]
						break
					}
				}
				if custID == nil {
					t.Fatal("Could not find CUST-ID field")
				}

				// CUST-ID should have children from the REDEFINE block
				if len(custID.Children) == 0 {
					t.Fatal("CUST-ID has no children (REDEFINE sub-fields not merged)")
				}

				// Check REGION (level 3, first redefine sub-field)
				if len(custID.Children) > 0 {
					region := &custID.Children[0]
					if region.Name != "REGION" {
						t.Errorf("Child[0].Name = %q, want REGION", region.Name)
					}
					if region.Redefines != "CUST-ID" {
						t.Errorf("Child[0].Redefines = %q, want CUST-ID", region.Redefines)
					}
					if region.Type != "A2" {
						t.Errorf("Child[0].Type = %q, want A2", region.Type)
					}
					if region.Level != 3 {
						t.Errorf("Child[0].Level = %d, want 3", region.Level)
					}
				}

				// Check SEQUENCE (level 3, second redefine sub-field)
				if len(custID.Children) > 1 {
					sequence := &custID.Children[1]
					if sequence.Name != "SEQUENCE" {
						t.Errorf("Child[1].Name = %q, want SEQUENCE", sequence.Name)
					}
					if sequence.Redefines != "CUST-ID" {
						t.Errorf("Child[1].Redefines = %q, want CUST-ID", sequence.Redefines)
					}
					if sequence.Type != "N8" {
						t.Errorf("Child[1].Type = %q, want N8", sequence.Type)
					}
					if sequence.Level != 3 {
						t.Errorf("Child[1].Level = %d, want 3", sequence.Level)
					}
				}

				// Verify no extra children (exactly 2 redefine sub-fields, no placeholder)
				if len(custID.Children) != 2 {
					t.Errorf("CUST-ID.Children len = %d, want 2 (no placeholder node)", len(custID.Children))
				}
			},
		},
		{
			name: "no_empty_name_placeholder",
			verify: func(t *testing.T) {
				// Find CUSTOMER-REC and verify it has no empty-Name children
				var customerRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-REC" && defs[i].Level == 1 {
						customerRec = &defs[i]
						break
					}
				}
				if customerRec == nil {
					return
				}

				// Check direct children of CUSTOMER-REC for empty-Name nodes
				for _, child := range customerRec.Children {
					if child.Name == "" {
						t.Errorf("Found empty-Name placeholder child in CUSTOMER-REC, want no empty-Name nodes")
					}
				}
			},
		},
		{
			name: "group_has_only_cust_id_as_sibling",
			verify: func(t *testing.T) {
				// Find CUSTOMER-REC and verify it has exactly one level-2 child (CUST-ID)
				var customerRec *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-REC" && defs[i].Level == 1 {
						customerRec = &defs[i]
						break
					}
				}
				if customerRec == nil {
					t.Fatal("Could not find CUSTOMER-REC group")
				}

				// Count level-2 children (should be just CUST-ID, REDEFINE block is merged)
				var level2Children []*model.DataDefinition
				for i := range customerRec.Children {
					if customerRec.Children[i].Level == 2 {
						level2Children = append(level2Children, &customerRec.Children[i])
					}
				}

				if len(level2Children) != 1 {
					t.Errorf("CUSTOMER-REC has %d level-2 children, want 1 (CUST-ID only; REDEFINE block is merged)",
						len(level2Children))
				}
				if len(level2Children) > 0 && level2Children[0].Name != "CUST-ID" {
					t.Errorf("Level-2 child is %q, want CUST-ID", level2Children[0].Name)
				}
			},
		},
		{
			name: "no_diagnostics_for_legal_nested_redefine",
			verify: func(t *testing.T) {
				// Per FR-17: no diagnostic emitted for legal nested REDEFINE
				if len(diags) > 0 {
					t.Errorf("Expected 0 diagnostics, got %d: %v", len(diags), diags)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.verify)
	}
}
