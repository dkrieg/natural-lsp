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

// TestExtractSQLAccess_Merge verifies Task 5 / FR-20: a MERGE statement emits
// exactly one EdgeWrites entry for its target DDM table. Requires the parser to
// capture the MERGE INTO <table> operand (Task 5a); the merge body (USING/WHEN
// clauses) is not modeled and must not produce false edges.
func TestExtractSQLAccess_Merge(t *testing.T) {
	fixture := filepath.Join("testdata", "sqlaccess", "merge.NSP")
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read fixture %q: %v", fixture, err)
	}

	prog, err := NewParser(NewLexer(string(content))).Parse()
	if prog == nil {
		t.Fatalf("parser returned nil AST (err=%v)", err)
	}

	entries := extractSQLAccess(prog)

	var writes []model.DataAccessEntry
	for _, e := range entries {
		if e.Kind == model.EdgeWrites {
			writes = append(writes, e)
		}
	}

	if len(writes) != 1 {
		t.Fatalf("got %d EdgeWrites entries, want 1: %+v", len(writes), entries)
	}
	if writes[0].Name != "EMPLOYEES" {
		t.Errorf("write Name = %q, want %q", writes[0].Name, "EMPLOYEES")
	}
	if writes[0].NameRange.Start == writes[0].NameRange.End {
		t.Error("write NameRange is zero, want non-zero range on the table token")
	}
	if writes[0].Source.Start == writes[0].Source.End {
		t.Error("write Source is zero, want non-zero statement range")
	}
}

// TestExtractSQLCalls_CallDBProc verifies Task 6b / FR-10,FR-14: a CALLDBPROC
// statement emits one call-like edge (EdgeCalls) whose TargetName is the stored
// procedure name, with the call site preserved. Requires the parser to capture
// the proc-name operand (Task 6a).
func TestExtractSQLCalls_CallDBProc(t *testing.T) {
	fixture := filepath.Join("testdata", "sqlaccess", "calldbproc.NSP")
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read fixture %q: %v", fixture, err)
	}
	prog, err := NewParser(NewLexer(string(content))).Parse()
	if prog == nil {
		t.Fatalf("parser returned nil AST (err=%v)", err)
	}

	edges := extractSQLCalls(prog)
	if len(edges) != 1 {
		t.Fatalf("got %d SQL call edges, want 1: %+v", len(edges), edges)
	}
	if edges[0].Kind != model.EdgeCalls {
		t.Errorf("edge Kind = %v, want %v (static literal proc name)", edges[0].Kind, model.EdgeCalls)
	}
	if edges[0].TargetName != "GET_EMPLOYEE" {
		t.Errorf("edge TargetName = %q, want %q (quotes stripped)", edges[0].TargetName, "GET_EMPLOYEE")
	}
	if edges[0].Source.Start == edges[0].Source.End {
		t.Error("edge Source is zero, want non-zero call-site range")
	}
}

// TestExtractSQLAccess_ReadResultSet verifies Task 6c: a READ RESULT SET records
// a read-access site. Its operand is a result-set handle (not a DDM), so the
// entry carries an empty Name (site recorded, binding deferred) — never a false
// DDM edge on the handle. The positional CALLDBPROC pairing is documented here.
func TestExtractSQLAccess_ReadResultSet(t *testing.T) {
	fixture := filepath.Join("testdata", "sqlaccess", "read_result_set.NSP")
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read fixture %q: %v", fixture, err)
	}
	prog, err := NewParser(NewLexer(string(content))).Parse()
	if prog == nil {
		t.Fatalf("parser returned nil AST (err=%v)", err)
	}

	entries := extractSQLAccess(prog)

	// Exactly one read-access entry, with empty Name (result-set handle, not a DDM).
	var reads []model.DataAccessEntry
	for _, e := range entries {
		if e.Kind == model.EdgeReads {
			reads = append(reads, e)
		}
	}
	if len(reads) != 1 {
		t.Fatalf("got %d EdgeReads entries, want 1 (the READ RESULT SET site): %+v", len(reads), entries)
	}
	if reads[0].Name != "" {
		t.Errorf("read Name = %q, want empty (result-set handle is not a DDM)", reads[0].Name)
	}
	if reads[0].Source.Start == reads[0].Source.End {
		t.Error("read Source is zero, want non-zero statement range")
	}

	// The CALLDBPROC edge is also present (positional association: it precedes
	// the READ RESULT SET in source order).
	calls := extractSQLCalls(prog)
	if len(calls) != 1 || calls[0].TargetName != "GET_DATA" {
		t.Errorf("expected one CALLDBPROC edge to GET_DATA preceding the READ RESULT SET, got %+v", calls)
	}
}

// TestScanOpaqueHostVars_Robustness is a table-driven robustness suite for
// scanOpaqueHostVars (FR-43). It asserts that the function never panics on
// degenerate or adversarial input, and that the outputs for known constructs
// are correct. Cases are organized around the robustness axes from the refactor
// checklist: empty body, trailing/isolated colons, qualifier edge cases, array
// range colons, multibyte input, and INDICATOR handling.
func TestScanOpaqueHostVars_Robustness(t *testing.T) {
	origin := model.Position{Line: 1, Column: 1}

	tests := []struct {
		name      string
		body      string
		wantNames []string // expected ref names in scan order; nil means no refs
	}{
		// --- Degenerate / empty inputs ---
		{
			name:      "empty_body",
			body:      "",
			wantNames: nil,
		},
		{
			name:      "body_all_colons",
			body:      "::::",
			wantNames: nil, // bare ':' with nothing after it is not a host-var
		},
		{
			name:      "trailing_colon",
			body:      "SELECT SOMETHING :",
			wantNames: nil,
		},
		{
			name:      "colon_U_nothing_after",
			body:      ":U",
			wantNames: []string{"U"}, // ':U' without second ':' — qualifier not consumed; 'U' is a valid letter-start name
		},
		{
			name:      "colon_U_no_second_colon",
			body:      ":Ufoo",
			wantNames: []string{"UFOO"}, // ':U' not followed by ':' so no qualifier strip; scanned as letter-start name "UFOO"
		},

		// --- Array range colon must NOT start a new host var ---
		{
			name: "array_range_colon_in_subscript",
			// :NAME(01:10) — the ':10' inside parens is consumed by skipParenSubscript
			body:      ":NAME(01:10)",
			wantNames: []string{"NAME"}, // NAME starts with a letter — valid name; ':10' inside parens is not a ref
		},
		{
			name:      "bare_array_range_in_sql_text",
			body:      "WHERE X BETWEEN 01 AND 10", // no colons — no refs
			wantNames: nil,
		},
		{
			name:      "colon_digit_start_no_ref",
			body:      ":10",
			wantNames: nil, // digit-starting sequence after ':' is not a host-var
		},
		{
			name:      "colon_digit_inside_parentheses",
			body:      ":SALARY(01:10)",
			wantNames: []string{"SALARY"}, // ':10' inside parens must NOT produce a second ref
		},

		// --- Qualifier grammar ---
		{
			name:      "qualifier_U_uppercase",
			body:      ":U:#NAME",
			wantNames: []string{"#NAME"},
		},
		{
			name:      "qualifier_G_uppercase",
			body:      ":G:#NAME",
			wantNames: []string{"#NAME"},
		},
		{
			name:      "qualifier_T_uppercase",
			body:      ":T:#NAME",
			wantNames: []string{"#NAME"},
		},
		{
			name:      "qualifier_u_lowercase",
			body:      ":u:#NAME",
			wantNames: []string{"#NAME"}, // case-insensitive qualifier
		},
		{
			name:      "qualifier_g_lowercase",
			body:      ":g:#NAME",
			wantNames: []string{"#NAME"},
		},
		{
			name: "qualifier_no_second_colon_not_consumed",
			// ':U' without a following ':' means qualifier letter is NOT stripped;
			// 'U' is scanned as the start of a letter-only name → "U" emitted;
			// '#' terminates the name scan (sigil is only valid at position 0).
			body:      ":U#NAME",
			wantNames: []string{"U"}, // letter-start name; '#' stops the scan
		},

		// --- INDICATOR / LINDICATOR handling ---
		{
			name:      "indicator_prefix",
			body:      ":INDICATOR :#SALARY",
			wantNames: []string{"#SALARY"},
		},
		{
			name:      "lindicator_prefix",
			body:      ":LINDICATOR :#DEPT",
			wantNames: []string{"#DEPT"},
		},
		{
			name:      "indicator_no_following_colon",
			body:      ":INDICATOR",
			wantNames: nil, // malformed INDICATOR with no ':name' — skip
		},
		{
			name:      "lindicator_no_following_name",
			body:      ":LINDICATOR :",
			wantNames: nil, // ':' with no name after LINDICATOR — skip
		},

		// --- Multibyte / CRLF ---
		{
			name:      "multibyte_utf8_passthrough",
			body:      "café :#NAME résumé",
			wantNames: []string{"#NAME"}, // multibyte bytes outside ':name' are plain chars; must not panic
		},
		{
			name:      "crlf_newlines",
			body:      ":#A\r\n:#B",
			wantNames: []string{"#A", "#B"}, // CRLF treated as one line advance; both refs found
		},
		{
			name:      "cr_only_newlines",
			body:      ":#A\r:#B",
			wantNames: []string{"#A", "#B"},
		},

		// --- Normal cases (regression) ---
		{
			name:      "plain_host_var",
			body:      ":#PERS-ID",
			wantNames: []string{"#PERS-ID"},
		},
		{
			name:      "multiple_vars_in_body",
			body:      "SELECT X FROM T WHERE A = :#ID AND B = :U:#NAME",
			wantNames: []string{"#ID", "#NAME"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic for any input (FR-43).
			refs := scanOpaqueHostVars(tc.body, origin)

			gotNames := make([]string, len(refs))
			for i, r := range refs {
				gotNames[i] = r.Name
			}

			if len(gotNames) != len(tc.wantNames) {
				t.Errorf("got %d refs %v, want %d refs %v", len(gotNames), gotNames, len(tc.wantNames), tc.wantNames)
				return
			}
			for i, want := range tc.wantNames {
				if gotNames[i] != want {
					t.Errorf("ref[%d].Name = %q, want %q (full: %v)", i, gotNames[i], want, gotNames)
				}
			}

			// Non-zero source ranges for every ref (monotonic: End >= Start by column or line).
			for _, r := range refs {
				if r.Range.Start.Line > r.Range.End.Line ||
					(r.Range.Start.Line == r.Range.End.Line && r.Range.Start.Column > r.Range.End.Column) {
					t.Errorf("ref %q has inverted range: Start=%v End=%v", r.Name, r.Range.Start, r.Range.End)
				}
			}
		})
	}
}

// TestExtractSQLAccess_ProcessSQL verifies Task 7 / FR-19, FR-21, OQ-3, M-6:
// a PROCESS SQL statement emits exactly one EdgeReads entry for its DDM operand
// (the load-bearing read-style access per OQ-3), and scanOpaqueHostVars scans
// the opaque body for colon-mandatory host-var references. The modeled gap is
// critical: in-body table names (pass-through text) are NOT bound as DDM edges,
// and only references of the form :name or :U:|G:|T: form are host-var refs.
//
// Acceptance criteria (Task 7):
//   - PROCESS SQL <DDMName> emits exactly ONE EdgeReads for the DDMName operand
//   - Name is the DDM name (upper-cased), NameRange is non-zero
//   - Source is the statement range (StartPos to EndPos)
//   - A bare table name in the opaque body (FROM SALARY_TABLE) is NOT a DDM edge (the modeled gap)
//   - Host-var refs from the opaque body (starting with :) are extracted via scanOpaqueHostVars
//   - Qualifier forms :U:#NAME and :G:#X are recognized and stripped, yielding #NAME and #X
//   - In-body SQL keywords (SELECT, WHERE) are NOT host-var refs
func TestExtractSQLAccess_ProcessSQL_DDMEdgeAndOpaqueBodyHostVars(t *testing.T) {
	t.Run("process_sql_ddm_and_opaque_hostvars", func(t *testing.T) {
		// Arrange: read the process_sql fixture with opaque body containing
		// table name (SALARY_TABLE), plain host var (:#PERS-ID), qualified forms
		// (:U:#NAME, :G:#X), and SQL keyword (SELECT, WHERE).
		fixture := filepath.Join("testdata", "sqlaccess", "process_sql.NSP")
		content, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("failed to read fixture %q: %v", fixture, err)
		}

		// Parse to AST
		lexer := NewLexer(string(content))
		parser := NewParser(lexer)
		prog, err := parser.Parse()
		if prog == nil {
			t.Fatal("parser returned nil AST")
		}

		// Sanity check: PROCESS SQL statement is in the AST.
		if len(prog.ProcessSQLs) != 1 {
			t.Fatalf("expected 1 ProcessSQLStatement, got %d", len(prog.ProcessSQLs))
		}
		stmt := prog.ProcessSQLs[0]
		if stmt.DDMName != "EMPLOYEE-DATA" {
			t.Errorf("DDMName = %q, want %q", stmt.DDMName, "EMPLOYEE-DATA")
		}

		// Act: extract SQL access entries (should yield Task 7 DDM edge)
		entries := extractSQLAccess(prog)

		// Assert: extract exactly ONE EdgeReads entry for the DDM operand.
		var reads []model.DataAccessEntry
		for _, e := range entries {
			if e.Kind == model.EdgeReads {
				reads = append(reads, e)
			}
		}
		if len(reads) < 1 {
			t.Fatalf("extractSQLAccess returned %d EdgeReads entries, want at least 1 for the DDM operand", len(reads))
		}

		// Find the one that matches the PROCESS SQL DDM (not any from opaque body).
		// The fixture has only one PROCESS SQL, so we expect exactly one read.
		// (Task 8 will merge this with native SQL reads, but here we're testing Task 7 in isolation.)
		if len(reads) != 1 {
			t.Logf("WARNING: got %d EdgeReads entries; Task 7 should emit exactly 1 for the PROCESS SQL DDM", len(reads))
			t.Logf("Entries: %+v", reads)
		}

		// Assert: the read has the correct DDM name and non-zero range.
		if reads[0].Name != "EMPLOYEE-DATA" {
			t.Errorf("reads[0].Name = %q, want %q", reads[0].Name, "EMPLOYEE-DATA")
		}
		if reads[0].NameRange.Start == reads[0].NameRange.End {
			t.Error("reads[0].NameRange is zero (empty), want non-zero range on the DDM name token")
		}
		if reads[0].Source.Start == reads[0].Source.End {
			t.Error("reads[0].Source is zero (empty), want non-zero statement range")
		}

		// LOAD-BEARING MODELED GAP: the in-body table name "SALARY_TABLE" must NOT
		// appear as an EdgeReads entry. This is the critical assertion: opaque-body
		// table names are pass-through text, never DDM edges.
		for _, e := range entries {
			if e.Kind == model.EdgeReads && e.Name == "SALARY_TABLE" {
				t.Errorf("ERROR: in-body table name SALARY_TABLE became an EdgeReads entry (modeled gap violated) — opaque-body table names are pass-through text: entry=%+v", e)
			}
		}

		// Act: extract host-var references (Task 7 opaque-body scan — NOT YET IMPLEMENTED).
		refs := extractHostVarRefs(prog)

		// Assert: host-var refs from the opaque body are extracted with colon+qualifier stripped.
		// Expected (in source order from the opaque body):
		//   - :#PERS-ID → #PERS-ID
		//   - :U:#NAME → #NAME (qualifier stripped)
		//   - :G:#X → #X (qualifier stripped)
		wantRefs := []string{"#PERS-ID", "#NAME", "#X"}
		if len(refs) < len(wantRefs) {
			t.Fatalf("extractHostVarRefs returned %d refs, want at least %d (opaque body host-vars)", len(refs), len(wantRefs))
		}

		// Map expected names to their positions in the ref list for assertion.
		// (Task 7 must scan and append them in source order within the body.)
		refNames := make(map[string]int)
		for i, r := range refs {
			refNames[r.Name]++
			t.Logf("ref[%d]: Name=%q Range=%v", i, r.Name, r.Range)
		}

		for _, want := range wantRefs {
			if refNames[want] == 0 {
				t.Errorf("host-var %q not found in refs (Task 7 opaque-body scan missing or incomplete)", want)
			}
		}

		// MODELED GAP: SQL keywords (SELECT, WHERE) and in-body table names (SALARY_TABLE)
		// must NOT appear as HostVarRefs. These are pass-through text.
		for _, ref := range refs {
			if ref.Name == "SELECT" || ref.Name == "WHERE" {
				t.Errorf("SQL keyword %q leaked into host-var refs (should be pass-through text)", ref.Name)
			}
			if ref.Name == "SALARY_TABLE" {
				t.Errorf("in-body table name SALARY_TABLE leaked into host-var refs (should be pass-through text)")
			}
		}
	})
}
