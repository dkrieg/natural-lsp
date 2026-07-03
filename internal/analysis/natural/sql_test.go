package natural

import (
	"os"
	"path/filepath"
	"testing"

	"natural-lsp/internal/model"
)

// TestExtractSQLAccess_SelectStatements verifies Task 2 / FR-19:
// native SQL SELECT and SELECT SINGLE statements emit exactly one
// EdgeReads entry per FROM table operand, with no false edges from
// column names, INTO targets, or WHERE operands.
//
// Acceptance criteria (Task 2):
//   - SelectStatement (cursor loop) emits one EdgeReads per FromTables operand
//   - SelectSingleStatement (singleton) emits one EdgeReads per FromTables operand
//   - Name is the DDM table name (upper-cased by lexer)
//   - NameRange spans the table operand token exactly
//   - Source is the statement range (StartPos to EndPos)
//   - Zero false edges: columns, INTO targets, WHERE operands are NOT DDM edges
//   - Entries are in source order
func TestExtractSQLAccess_SelectStatements(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantTableName string
		wantCount     int // expect exactly one EdgeReads per fixture
	}{
		{
			name:          "select_loop_from_sql_personnel",
			fixture:       filepath.Join("testdata", "sqlaccess", "select_loop.NSP"),
			wantTableName: "SQL-PERSONNEL",
			wantCount:     1,
		},
		{
			name:          "select_single_from_employees",
			fixture:       filepath.Join("testdata", "sqlaccess", "select_single.NSP"),
			wantTableName: "EMPLOYEES",
			wantCount:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: read fixture
			content, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatalf("failed to read fixture %q: %v", tc.fixture, err)
			}

			// Parse to AST
			lexer := NewLexer(string(content))
			parser := NewParser(lexer)
			prog, err := parser.Parse()
			if prog == nil {
				t.Fatal("parser returned nil AST")
			}

			// Act: extract SQL access entries (stub function — not yet implemented)
			entries := extractSQLAccess(prog)

			// Assert: exactly one EdgeReads entry
			if len(entries) != tc.wantCount {
				t.Errorf("extractSQLAccess returned %d entries, want %d", len(entries), tc.wantCount)
				for i, e := range entries {
					t.Logf("  entry[%d]: Kind=%v, Name=%q, Source=%v", i, e.Kind, e.Name, e.Source)
				}
				return
			}

			// Assert: entry is EdgeReads with correct Name
			if entries[0].Kind != model.EdgeReads {
				t.Errorf("entries[0].Kind = %v, want %v", entries[0].Kind, model.EdgeReads)
			}

			if entries[0].Name != tc.wantTableName {
				t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, tc.wantTableName)
			}

			// Assert: NameRange is non-zero (spans the table operand token)
			if entries[0].NameRange.Start == entries[0].NameRange.End {
				t.Errorf("entries[0].NameRange is zero (empty), want non-zero range on the table name token")
			}

			// Assert: Source is non-zero (spans the statement)
			if entries[0].Source.Start == entries[0].Source.End {
				t.Errorf("entries[0].Source is zero (empty), want non-zero range for the statement")
			}
		})
	}
}
