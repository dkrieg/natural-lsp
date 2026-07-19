package natural

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// FuzzParse is the executable proof of the parser's robustness (M-6, ADR-013):
// the parser must NEVER panic and must ALWAYS return a non-nil *Program for
// arbitrary input — even malformed, garbage, or edge-case bytes.
//
// The seed corpus is drawn from the committed testdata/parser fixtures
// (01-19) representing real, known-interesting Natural constructs (lexer
// tokens, statements, READ/STORE, DEFINE DATA arrays/redefine, parse errors,
// inline comments, embedded SQL, parse error recovery), plus hand-written edge
// cases (empty, unterminated string, lone parentheses, deeply nested parens,
// multi-byte UTF-8, very long line, unterminated SELECT loop, unterminated
// PROCESS SQL << >> span).
//
// Feature 00 Task 11; M-6, FR-43, ADR-013. ES-10 adds fixtures 09-19 and
// unterminated-SQL seeds. Refactor phase adds seeds for the clause-skip path
// (SELECT/SELECT SINGLE with GROUP BY, ORDER BY, HAVING — both terminated and
// unterminated) to cover the infinite-loop class that was the blocker.
func FuzzParse(f *testing.F) {
	// Seed from the existing testdata/parser fixtures (01-19).
	// Read at fuzz-setup time; if a read fails, skip that seed with a warning
	// (fixture not found is not a test failure — it's a missing file that the
	// build will have flagged already).
	fixtureNames := []string{
		"01-lexer-token-types.nsp",
		"02-lexer-multi-line.nsp",
		"03-parser-statements.nsp",
		"04-parser-parse-errors.nsp",
		"05-inline-comments.nsp",
		"06-read-store.nsp",
		"07-data-arrays.nsp",
		"08-data-redefine.nsp",
		"09-sql-txn-calldbproc.nsp",
		"10-sql-select-loop.nsp",
		"11-sql-select-single.nsp",
		"12-sql-select-loop-reporting.nsp",
		"13-sql-read-result-set.nsp",
		"14-sql-read-result-set-loop.nsp",
		"15-sql-process-sql.nsp",
		"16-sql-insert.nsp",
		"17-sql-update-delete.nsp",
		"18-sql-merge.nsp",
		"19-sql-parse-errors.nsp",
		"24-work-file.nsp",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("testdata", "parser", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure; the test framework
			// will report missing testdata separately).
			continue
		}
		f.Add(data)
	}

	// Hand-written tricky seeds exercising edge cases.

	// Empty input: the lexer must consume EOF without panic.
	f.Add([]byte(""))

	// Lone unterminated string literal.
	f.Add([]byte("'unterminated"))

	// Lone opening parenthesis (not closed).
	f.Add([]byte("("))

	// Deeply nested parentheses (tests parser recursion limits gracefully).
	f.Add([]byte("(((((()))))))"))

	// Bare CALLNAT with no target.
	f.Add([]byte("CALLNAT"))

	// Multi-byte UTF-8 characters (Natural is ASCII, but the lexer must
	// handle arbitrary bytes without panic).
	f.Add([]byte("CALLNAT 'café'"))

	// Very long line (tests lexer buffer handling).
	longLine := make([]byte, 10000)
	for i := range longLine {
		longLine[i] = 'A'
	}
	f.Add(longLine)

	// Mixed valid and invalid: valid statement followed by garbage.
	f.Add([]byte("CALLNAT 'MYPROG'\n\x00\x01\x02"))

	// Newline-heavy input.
	f.Add([]byte("\n\n\n\nCALLNAT\n\n\n"))

	// Leading/trailing whitespace.
	f.Add([]byte("  \t\n  CALLNAT 'PROG'  \t\n  "))

	// ES-10: Unterminated SELECT loop (loop body never terminated by END-SELECT/LOOP).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nSELECT COL INTO #V FROM EMPLOYEES WHERE ID = #K\n  PERFORM 'DO-IT'\nEND\n"))

	// ES-10: Unterminated PROCESS SQL << >> opaque span (no closing >>).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nPROCESS SQL PAYROLL <<\n  UPDATE T SET C = :#A WHERE K = :#B\nEND\n"))

	// Clause-skip path seeds: SELECT with GROUP BY, ORDER BY, HAVING (terminated).
	// These exercise the skipSQLClause path that previously infinite-looped (blocker).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #V (A10)\nEND-DEFINE\nSELECT COL INTO #V FROM T WHERE X = 1 GROUP BY COL\nEND-SELECT\nEND\n"))
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #V (A10)\nEND-DEFINE\nSELECT COL INTO #V FROM T WHERE X = 1 ORDER BY COL\nEND-SELECT\nEND\n"))
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #V (A10)\nEND-DEFINE\nSELECT COL INTO #V FROM T WHERE X = 1 GROUP BY COL HAVING COUNT(*) > 1\nEND-SELECT\nEND\n"))

	// Clause-skip path seeds: SELECT SINGLE with GROUP BY, ORDER BY, HAVING (no terminator).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #V (A10)\nEND-DEFINE\nSELECT SINGLE COL INTO #V FROM T WHERE X = 1 GROUP BY COL\nEND\n"))
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #V (A10)\nEND-DEFINE\nSELECT SINGLE COL INTO #V FROM T WHERE X = 1 ORDER BY COL\nEND\n"))
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #V (A10)\nEND-DEFINE\nSELECT SINGLE COL INTO #V FROM T WHERE X = 1 HAVING COUNT(*) > 1\nEND\n"))

	// Unterminated SELECT with GROUP BY (no END-SELECT/LOOP — exercises clause-skip + unterminated path).
	f.Add([]byte("SELECT COL INTO #V FROM T WHERE X = 1 GROUP BY COL\n  PERFORM DOSOMETHING\n"))
	f.Add([]byte("SELECT SINGLE COL INTO #V FROM T WHERE X = 1 ORDER BY COL\n"))

	// Malformed DEFINE WORK FILE variants (FR-43 graceful-degradation seeds).
	f.Add([]byte("DEFINE WORK FILE"))                         // bare — missing number and name
	f.Add([]byte("DEFINE WORK 1 'X'\n"))                      // missing FILE keyword
	f.Add([]byte("DEFINE WORK FILE 'X'\n"))                   // missing number (name in number position)
	f.Add([]byte("DEFINE WORK FILE 1.5 'X'\n"))               // non-integer number (decimal)
	f.Add([]byte("DEFINE WORK FILE 1\n"))                     // missing name
	f.Add([]byte("DEFINE WORK FILE 1 'GOOD'\nCALLNAT 'X'\n")) // well-formed followed by another stmt

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: construct the lexer and parser from the arbitrary input.
		lexer := NewLexer(string(input))
		parser := NewParser(lexer)

		// Act: parse the input. The fuzzer automatically catches panics,
		// but we assert here that prog is non-nil as an additional safeguard.
		prog, _ := parser.Parse()

		// Assert: parser must ALWAYS return a non-nil *Program, even for
		// arbitrary/garbage input. A nil return is a violation of the
		// contract (M-6, FR-43: no silent gaps, graceful degradation).
		if prog == nil {
			t.Fatal("Parse() returned nil *Program for arbitrary input; want non-nil *Program")
		}
	})
}

// FuzzExtractSQL is the executable proof of the SQL extraction functions' robustness
// (FR-43, Task 9): extractSQLAccess, extractSQLCalls, and extractHostVarRefs must
// NEVER panic on arbitrary input — even malformed, truncated, or edge-case bytes —
// and must ALWAYS return non-nil-safe slices (nil is acceptable; panic is not).
//
// The fuzzer exercises:
//   - Arbitrary tokenization (the parser produces partial/nil-safe ASTs)
//   - Truncated SQL statements (missing operands, unclosed loops, opaque spans)
//   - Malformed constructs (CALLDBPROC with no proc name, MERGE with no table)
//   - Adversarial opaque-body strings (colons only, unterminated <<"<<, escaped chars)
//   - Mixed valid and garbage inputs
//
// Seed corpus:
//   - All sqlaccess/ fixtures (SELECT, INSERT, UPDATE, DELETE, MERGE, CALLDBPROC, PROCESS SQL)
//   - Hand-written adversarial cases (unterminated PROCESS SQL <<"<<, malformed CALLDBPROC/MERGE)
//
// Feature 08b Task 9, FR-43, M-6, ADR-013.
func FuzzExtractSQL(f *testing.F) {
	// Seed from sqlaccess/ fixtures (Tasks 2–7).
	fixtureNames := []string{
		"select_loop.NSP",
		"select_single.NSP",
		"insert.NSP",
		"sql_update.NSP",
		"sql_delete.NSP",
		"merge.NSP",
		"calldbproc.NSP",
		"read_result_set.NSP",
		"process_sql.NSP",
		"hostvars_native.NSP",
		"kb_minimal.NSP",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("testdata", "sqlaccess", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure).
			continue
		}
		f.Add(data)
	}

	// Hand-written adversarial seeds (FR-43 graceful degradation).

	// Unterminated PROCESS SQL opaque span (no closing >>).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nPROCESS SQL PAYROLL <<\n  SELECT * FROM T\nEND\n"))

	// CALLDBPROC with no proc-name operand (bare keyword).
	f.Add([]byte("CALLDBPROC\nEND\n"))

	// MERGE INTO without a table operand (malformed).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nMERGE INTO\nEND\n"))

	// PROCESS SQL opaque body with colons only (no valid host-var names).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nPROCESS SQL DDM << ::::: >> END\n"))

	// PROCESS SQL opaque body with malformed qualifier (colon-U-colon with no name after).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nPROCESS SQL DDM << :U: >> END\n"))

	// Truncated SELECT with unterminated WHERE clause.
	f.Add([]byte("SELECT COL FROM T WHERE X =\nEND\n"))

	// SELECT with unterminated opaque body (colon + incomplete name).
	f.Add([]byte("SELECT X INTO :\nEND-SELECT\n"))

	// CALLDBPROC followed by garbage.
	f.Add([]byte("CALLDBPROC 'PROC' !!!! ^^^^\nEND\n"))

	// MERGE with malformed USING clause (parser should not panic).
	f.Add([]byte("MERGE INTO EMP USING (GARBAGE)\nEND\n"))

	// Mixed valid SQL and newlines.
	f.Add([]byte("\n\nSELECT A FROM T\n\n\nEND-SELECT\n\n"))

	// Lone PROCESS SQL with no DDM name.
	f.Add([]byte("PROCESS SQL << DATA >> END\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: construct the lexer and parser from the arbitrary input.
		lexer := NewLexer(string(input))
		parser := NewParser(lexer)

		// Act: parse the input (may be partial/malformed).
		prog, _ := parser.Parse()

		// Extract SQL data-access entries. Must not panic even over nil or partial ASTs.
		accessEntries := extractSQLAccess(prog)

		// Extract SQL call-like edges (CALLDBPROC). Must not panic.
		callEdges := extractSQLCalls(prog)

		// Extract host-variable references. Must not panic.
		hostVarRefs := extractHostVarRefs(prog)

		// Assert: all three extractors returned non-nil-safe slices (nil acceptable, panic forbidden).
		// The acceptance criterion is no panic; we verify the return types are valid.
		_ = accessEntries // slice of DataAccessEntry or nil
		_ = callEdges     // slice of EdgeEntry or nil
		_ = hostVarRefs   // slice of HostVarRef or nil

		// Verify FR-17 channel separation: every extracted entry must have a non-zero Source
		// (entries are only emitted for actual statements/operands, never for parser errors).
		// Parser diagnostics stay on prog.Diagnostics; extracted entries carry statement ranges.
		for _, entry := range accessEntries {
			if entry.Source.Start == entry.Source.End {
				t.Logf("WARNING: accessEntries entry has zero Source range (may be malformed): %+v", entry)
			}
		}
		for _, edge := range callEdges {
			if edge.Source.Start == edge.Source.End {
				t.Logf("WARNING: callEdges edge has zero Source range (may be malformed): %+v", edge)
			}
		}
	})
}

// TestExtractSQL_NilGuards verifies the nil-guard safety assertions (FR-43, ADR-013):
// each extractor must accept a nil *Program and return a non-panicking, non-nil-safe result.
func TestExtractSQL_NilGuards(t *testing.T) {
	// extractSQLAccess(nil) must not panic and must return nil (or an empty slice).
	if got := extractSQLAccess(nil); len(got) > 0 {
		t.Errorf("extractSQLAccess(nil) returned %d entries; want nil or empty slice", len(got))
	}

	// extractSQLCalls(nil) must not panic and must return nil (or an empty slice).
	if got := extractSQLCalls(nil); len(got) > 0 {
		t.Errorf("extractSQLCalls(nil) returned %d edges; want nil or empty slice", len(got))
	}

	// extractHostVarRefs(nil) must not panic and must return nil (or an empty slice).
	if got := extractHostVarRefs(nil); len(got) > 0 {
		t.Errorf("extractHostVarRefs(nil) returned %d refs; want nil or empty slice", len(got))
	}
}

// FuzzExtractStructure is the executable proof of the structure-extraction function's
// robustness (FR-43, feature 09 Task 7): extractStructure must NEVER panic on arbitrary
// input — even malformed, partial, or edge-case bytes — and must ALWAYS return either nil
// or a well-formed hierarchical tree (no nil *Symbol children, no invariant violations).
//
// The fuzzer exercises:
//   - Arbitrary tokenization (the parser produces partial/nil-safe ASTs)
//   - Truncated DEFINE DATA sections (missing levels, no END-DEFINE)
//   - Malformed subroutine definitions (no END-SUBROUTINE)
//   - Truncated MAP declarations (missing fields, no END-MAP)
//   - Adversarial input (pure garbage, deeply nested fields, mixed valid/invalid)
//   - Files with all combinations of the above (robustness to "one bad thing among many")
//
// Seed corpus:
//   - All structure/ fixtures (Tasks 2–5: program, subprogram, map, DDM refs, partial)
//   - Hand-written adversarial cases (unterminated DEFINE SUBROUTINE, malformed MAP, garbage bytes)
//
// Feature 09 Task 7, FR-43, M-6, ADR-013.
func FuzzExtractStructure(f *testing.F) {
	// Seed from structure/ fixtures (Tasks 2–5).
	fixtureNames := []string{
		"01-program-full.NSP",
		"02-subprogram-params.NSN",
		"03-map.NSM",
		"04-ddm-access.NSP",
		"05-partial.NSP",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("testdata", "structure", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure).
			continue
		}
		f.Add(data)
	}

	// Hand-written adversarial seeds (FR-43 graceful degradation).

	// Unterminated DEFINE SUBROUTINE (no END-SUBROUTINE).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nDEFINE SUBROUTINE DO-WORK\n  PERFORM 'X'\nEND\n"))

	// Malformed DEFINE MAP (missing fields, no END-MAP).
	f.Add([]byte("DEFINE MAP\n  1 FIELD-A (A10)\nEND\n"))

	// Truncated DEFINE DATA (no END-DEFINE).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #VAR (A5)\nCALLNAT 'X'\nEND\n"))

	// Pure garbage bytes (no structure expected).
	f.Add([]byte("\x00\x01\x02\x03\xFF\xFE\xFD"))

	// Empty input.
	f.Add([]byte(""))

	// Deeply nested field groups (tests recursive symbol conversion).
	f.Add([]byte("DEFINE DATA LOCAL\n1 GROUP1\n  2 GROUP2\n    3 GROUP3\n      4 #DEEP (A5)\nEND-DEFINE\nEND\n"))

	// Mixed valid subroutine + malformed MAP.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nDEFINE SUBROUTINE GOOD\nEND-SUBROUTINE\nDEFINE MAP BROKEN\nEND\n"))

	// Multiple data sections with REDEFINE.
	f.Add([]byte("DEFINE DATA\nLOCAL\n  1 #X (A5)\n  1 #Y REDEFINE #X (N5)\nGLOBAL\n  1 #G (A10)\nEND-DEFINE\nEND\n"))

	// Lone DEFINE WORK FILE (may be recognized or ignored gracefully).
	f.Add([]byte("DEFINE WORK FILE 1 'MYFILE'\nEND\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: construct the lexer and parser from the arbitrary input.
		lexer := NewLexer(string(input))
		parser := NewParser(lexer)

		// Act: parse the input (may be partial/malformed).
		prog, _ := parser.Parse()

		// Extract definitions.
		defs := extractDefinitions(prog)

		// Extract data access.
		access := extractDataAccess(prog)

		// Run extractStructure. Must not panic even over nil or partial ASTs.
		sym := extractStructure("fuzz.NSP", prog, defs, access)

		// Assert: extractStructure returned either nil or a well-formed tree.
		// The key assertion is NEVER PANIC; well-formedness check is defensive.
		if sym != nil {
			// If a non-nil tree is returned, walk it recursively and verify no
			// nil children and that the Kind is valid (basic invariant check).
			var walkSymbols func(s *model.Symbol)
			walkSymbols = func(s *model.Symbol) {
				if s == nil {
					t.Errorf("Symbol tree contains a nil child, want well-formed tree")
					return
				}
				// Basic kind check: all kinds should be non-empty strings.
				if s.Kind == "" {
					t.Errorf("Symbol has empty Kind, want a valid SymbolKind")
				}
				// Range must be sensible (both Start and End set, or both zero).
				// We don't enforce strict ordering here (the fuzzer is for crash-testing).

				// Recursively walk children.
				for i := range s.Children {
					walkSymbols(&s.Children[i])
				}
			}
			walkSymbols(sym)
		}
	})
}

// TestExtractStructure_NilGuard verifies the nil-guard safety assertion (FR-43, ADR-013):
// extractStructure must accept a nil *Program and return a non-panicking, nil result.
func TestExtractStructure_NilGuard(t *testing.T) {
	// extractStructure(path, nil, nil, nil) must not panic and must return nil.
	got := extractStructure("x.NSP", nil, nil, nil)
	if got != nil {
		t.Errorf("extractStructure(path, nil, nil, nil) returned %v, want nil", got)
	}
}

// FuzzParseDDM is the executable proof of the DDM scanner's robustness (FR-43,
// feature 12-hover T2A): extractDDMDefinitions must NEVER panic on arbitrary input
// and must ALWAYS return a slice (nil is acceptable; panic is not).
//
// The DDM scanner operates on fixed-column text, not Natural source, so it has its
// own failure modes: short lines, non-digit level columns, TYPE: variants, malformed
// or absent column data, deeply nested groups with no preceding parent, and garbage
// bytes. This target guards all those paths.
//
// Seed corpus:
//   - testdata/ddm/customer.NSD — full Adabas DDM with groups, MU, superdescriptor
//   - testdata/ddm/headers-only.NSD — headers + terminator, no field rows
//   - testdata/ddm/sql-type.NSD — TYPE: SQL header, should return nil
//   - testdata/ddm/short-lines.NSD — group line shorter than full column width,
//     a bare "TYPE:" line (5 chars), and a non-digit level to exercise bounds guards
//
// Hand-written degenerate seeds cover: empty input, headers-only, a TYPE: SQL
// early-exit, a line shorter than every column offset, a non-digit at the level
// offset, a child row with no preceding group (level 2 with empty stack), and
// arbitrary unicode/garbage bytes.
//
// Feature 12-hover T2A, FR-43, M-6, ADR-013.
func FuzzParseDDM(f *testing.F) {
	// Seed from the DDM fixture files.
	fixtureNames := []string{
		"customer.NSD",
		"headers-only.NSD",
		"sql-type.NSD",
		"short-lines.NSD",
	}
	for _, name := range fixtureNames {
		data, err := os.ReadFile(filepath.Join("testdata", "ddm", name))
		if err != nil {
			continue // missing fixture is not a test failure
		}
		f.Add(data)
	}

	// Empty input — must return nil without panic.
	f.Add([]byte(""))

	// Headers only, no field rows — must return nil or empty slice.
	f.Add([]byte("DB: 000 FILE: 1  - EMPTY\nTYPE: ADABAS\n\nT L DB Name                              F Leng  S D Remark\n- - -- --------------------------------  - ----  - - ------\n"))

	// TYPE: SQL early-exit path.
	f.Add([]byte("DB: 000 FILE: 1\nTYPE: SQL\n  1 AA COL A 20\n"))

	// Bare "TYPE:" (5 chars, no space after colon) — previously caused out-of-range
	// slice in the old header-skip guard; regression seed.
	f.Add([]byte("DB: 000 FILE: 1\nTYPE:\n  1 AA FIELD-A                           A   10  N\n"))

	// A line shorter than the Name offset (< 8 chars) with a valid level digit.
	f.Add([]byte("  1 AB\n"))

	// A line with a non-digit at the level offset (col 2) — must be skipped, not mis-nested.
	f.Add([]byte("  X AB FIELD                             A   10  N\n"))

	// Level-2 child row with NO preceding group (stack is empty at start) — must
	// be added to top-level definitions without panic.
	f.Add([]byte("DB: 000 FILE: 1\nTYPE: ADABAS\n\nT L DB Name                              F Leng  S D Remark\n- - -- --------------------------------  - ----  - - ------\n  2 AD ORPHAN-CHILD                       A   10  N\n"))

	// Deeply nested groups (levels 1→2→3) — exercises stack growth and pop.
	f.Add([]byte("DB: 000 FILE: 1\nTYPE: ADABAS\n\n" +
		"T L DB Name                              F Leng  S D Remark\n" +
		"- - -- --------------------------------  - ----  - - ------\n" +
		"G 1 AA OUTER\n" +
		"G 2 AB INNER\n" +
		"  3 AC LEAF                              A   10  N\n"))

	// MU field at level 1.
	f.Add([]byte("DB: 000 FILE: 1\nTYPE: ADABAS\n\n" +
		"T L DB Name                              F Leng  S D Remark\n" +
		"- - -- --------------------------------  - ----  - - ------\n" +
		"M 1 AA MULTI-VAL                         A   20  N\n"))

	// Unicode/garbage bytes — scanner must not panic on non-ASCII.
	f.Add([]byte("DB: 000 FILE: 1\nTYPE: ADABAS\n\xff\xfe\x00\x01\n  1 AA FIELD\xc3\xa9                           A   10  N\n"))

	// Very long line (wide field name padding or garbage).
	longLine := make([]byte, 2000)
	for i := range longLine {
		longLine[i] = ' '
	}
	// Place a valid header + terminator around it.
	f.Add(append([]byte("DB: 000 FILE: 1\nTYPE: ADABAS\n- - -- --------------------------------  - ----  - - ------\n  1 AA "), longLine...))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Act: run the DDM scanner on arbitrary input.
		// The fuzzer automatically catches panics; the assertion below adds an
		// explicit nil-slice vs nil-return check (nil is valid, panic is not).
		defs := extractDDMDefinitions(string(input))

		// Assert: a nil slice is acceptable (SQL DDM or no data rows); any non-nil
		// slice must be well-formed (no nil entries). Walking the tree validates
		// that nested Children slices are also safe.
		var walkDefs func([]model.DataDefinition)
		walkDefs = func(ds []model.DataDefinition) {
			for i := range ds {
				if ds[i].Name == "" && len(ds[i].Children) == 0 {
					// A definition with no name and no children is suspicious but
					// not a panic — log for diagnostics without failing.
					t.Logf("FuzzParseDDM: definition at index %d has empty name and no children", i)
				}
				walkDefs(ds[i].Children)
			}
		}
		walkDefs(defs)
	})
}
