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

// TestExtractSQLAccess_WriteStatements verifies Task 3 / FR-20:
// native SQL INSERT, UPDATE (SQL form), and DELETE (SQL form) statements emit
// exactly one EdgeWrites entry per table operand, with no false edges from
// column names, SET targets, or WHERE operands.
//
// Acceptance criteria (Task 3):
//   - InsertStatement emits one EdgeWrites for IntoTable operand
//   - SQLUpdateStatement (SET/WHERE form) emits one EdgeWrites for Table operand
//   - SQLDeleteStatement (WHERE/FROM form) emits one EdgeWrites for FromTable operand
//   - Name is the DDM table name (upper-cased by lexer)
//   - NameRange spans the table operand token exactly
//   - Source is the statement range (StartPos to EndPos)
//   - Kind is EdgeWrites (distinct from EdgeReads reads to the same DDM)
//   - Zero false edges: column names, SET targets, WHERE operands are NOT DDM edges
//   - Entries are in source order
func TestExtractSQLAccess_WriteStatements(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantTableName string
		wantKind      model.EdgeKind
		wantCount     int // expect exactly one EdgeWrites per fixture
	}{
		{
			name:          "insert_into_customers",
			fixture:       filepath.Join("testdata", "sqlaccess", "insert.NSP"),
			wantTableName: "CUSTOMERS",
			wantKind:      model.EdgeWrites,
			wantCount:     1,
		},
		{
			name:          "sql_update_employees",
			fixture:       filepath.Join("testdata", "sqlaccess", "sql_update.NSP"),
			wantTableName: "EMPLOYEES",
			wantKind:      model.EdgeWrites,
			wantCount:     1,
		},
		{
			name:          "sql_delete_orders",
			fixture:       filepath.Join("testdata", "sqlaccess", "sql_delete.NSP"),
			wantTableName: "ORDERS",
			wantKind:      model.EdgeWrites,
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

			// Act: extract SQL access entries
			entries := extractSQLAccess(prog)

			// Assert: exactly one EdgeWrites entry
			if len(entries) != tc.wantCount {
				t.Errorf("extractSQLAccess returned %d entries, want %d", len(entries), tc.wantCount)
				for i, e := range entries {
					t.Logf("  entry[%d]: Kind=%v, Name=%q, Source=%v", i, e.Kind, e.Name, e.Source)
				}
				return
			}

			// Assert: entry is EdgeWrites with correct Name
			if entries[0].Kind != tc.wantKind {
				t.Errorf("entries[0].Kind = %v, want %v", entries[0].Kind, tc.wantKind)
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

// TestExtractSQLAccess_ReadAndWriteDistinct verifies Task 3 / FR-20:
// a fixture that reads from and writes to the same DDM produces one EdgeReads
// and one EdgeWrites entry, distinct by Kind field.
func TestExtractSQLAccess_ReadAndWriteDistinct(t *testing.T) {
	t.Run("read_and_write_same_ddm", func(t *testing.T) {
		// Create an inline fixture: SELECT from PRODUCTS, then UPDATE PRODUCTS
		// This demonstrates that reads and writes to the same DDM are distinct edges.
		content := `
DEFINE DATA LOCAL
  1 #PRODUCT-ID (N5)
  1 #NEW-PRICE (N9.2)
  1 #PRICE (N9.2)
END-DEFINE

SELECT COL1 INTO #PRICE FROM PRODUCTS WHERE ID = :#PRODUCT-ID
  CONTINUE
END-SELECT

UPDATE PRODUCTS SET PRICE = :#NEW-PRICE WHERE ID = :#PRODUCT-ID

END
`

		lexer := NewLexer(content)
		parser := NewParser(lexer)
		prog, _ := parser.Parse()
		if prog == nil {
			t.Fatal("parser returned nil AST")
		}

		// Act: extract SQL access entries
		entries := extractSQLAccess(prog)

		// Assert: we get exactly 2 entries (one read, one write)
		if len(entries) != 2 {
			t.Errorf("extractSQLAccess returned %d entries, want 2", len(entries))
			for i, e := range entries {
				t.Logf("  entry[%d]: Kind=%v, Name=%q", i, e.Kind, e.Name)
			}
			return
		}

		// Assert: first entry is EdgeReads (SELECT)
		if entries[0].Kind != model.EdgeReads {
			t.Errorf("entries[0].Kind = %v, want EdgeReads", entries[0].Kind)
		}
		if entries[0].Name != "PRODUCTS" {
			t.Errorf("entries[0].Name = %q, want PRODUCTS", entries[0].Name)
		}

		// Assert: second entry is EdgeWrites (UPDATE)
		if entries[1].Kind != model.EdgeWrites {
			t.Errorf("entries[1].Kind = %v, want EdgeWrites", entries[1].Kind)
		}
		if entries[1].Name != "PRODUCTS" {
			t.Errorf("entries[1].Name = %q, want PRODUCTS", entries[1].Name)
		}
	})
}

// TestExtractHostVarRefs_Native verifies Task 4 / FR-21:
// host-variable operands in native SQL clauses (INTO, WHERE, VALUES, SET) are
// extracted as HostVarRef entries with normalized names and correct ranges.
// Both bare (#-prefixed) and colon-prefixed forms bind; reserved-word cases
// (e.g., :DATE) are handled by colon-stripping.
//
// Acceptance criteria (Task 4):
//   - Host-var operands from IntoTargets, WhereOperands, Values, SetTargets are extracted
//   - Name is normalized: sigil (#, &, @, +) preserved, colon stripped, upper-cased
//   - Bare (#VAR) and colon-prefixed (:DATE) both extract to the same normalized form
//   - Range spans the operand token exactly (after colon, if present)
//   - Zero false edges: column names (COL1, COL2) and DDM table names (SQL-PERSONNEL)
//     are NOT extracted as host-var refs
//   - Entries are in source order
func TestExtractHostVarRefs_Native(t *testing.T) {
	t.Run("select_with_bare_and_colon_hostvars", func(t *testing.T) {
		// Arrange: read fixture with bare (#) and colon-prefixed (:) host vars
		content, err := os.ReadFile(filepath.Join("testdata", "sqlaccess", "hostvars_native.NSP"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}

		// Parse to AST
		lexer := NewLexer(string(content))
		parser := NewParser(lexer)
		prog, err := parser.Parse()
		if prog == nil {
			t.Fatal("parser returned nil AST")
		}

		// Act: extract host-var references (not yet implemented)
		refs := extractHostVarRefs(prog)

		// Assert: exactly 4 host-var refs expected
		// #NAME (FROM INTO), #SALARY (INTO), #PERS-ID (WHERE), DATE (FROM :DATE)
		if len(refs) != 4 {
			t.Errorf("extractHostVarRefs returned %d refs, want 4", len(refs))
			for i, r := range refs {
				t.Logf("  ref[%d]: Name=%q Range=%v", i, r.Name, r.Range)
			}
			return
		}

		// Expected refs in source order:
		expectedNames := []string{"#NAME", "#SALARY", "#PERS-ID", "DATE"}

		// Assert: all names are present and normalized (colon stripped, upper-cased)
		for i, wantName := range expectedNames {
			if refs[i].Name != wantName {
				t.Errorf("refs[%d].Name = %q, want %q", i, refs[i].Name, wantName)
			}

			// Assert: Range is non-zero (spans the operand token)
			if refs[i].Range.Start == refs[i].Range.End {
				t.Errorf("refs[%d].Range is zero (empty), want non-zero range", i)
			}
		}

		// Sanity check: no column names (COL1, COL2) or DDM table name (SQL-PERSONNEL)
		// should appear in the refs
		for _, ref := range refs {
			if ref.Name == "COL1" || ref.Name == "COL2" {
				t.Errorf("column name %q leaked into host-var refs (should not be extracted)", ref.Name)
			}
			if ref.Name == "SQL-PERSONNEL" {
				t.Errorf("DDM table name SQL-PERSONNEL leaked into host-var refs (should not be extracted)")
			}
		}
	})
}
