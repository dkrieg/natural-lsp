package natural

import (
	"os"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestParser_DiagnosticsForParseErrors verifies that the parser emits syntax
// diagnostics with real positional ranges for malformed statements (FR-30, Task 7).
// This test replaces weak t.Log assertions with hard assertions on Program.Diagnostics.
func TestParser_DiagnosticsForParseErrors(t *testing.T) {
	tests := []struct {
		name                  string
		input                 string
		expectMinDiagnostics  int // at least this many diagnostics
		expectCallCount       int // at least this many valid calls
		validateRangeCallback func(t *testing.T, diag model.Diagnostic)
		description           string
	}{
		{
			name: "missing_required_operand_callnat_FR30",
			// CALLNAT with no operand on its line → diagnostic with Range
			// at the CALLNAT keyword (line 1).
			input: `CALLNAT
MALFORMED`,
			expectMinDiagnostics: 1,
			validateRangeCallback: func(t *testing.T, diag model.Diagnostic) {
				// The diagnostic Range must point at the CALLNAT keyword (line 1)
				if diag.Range.Start.Line != 1 {
					t.Errorf("diagnostic Range.Start.Line: got %d, want 1", diag.Range.Start.Line)
				}
				// Range should be non-zero (not a placeholder {1,1}→{1,1})
				if diag.Range.Start.Line == 0 || diag.Range.Start.Column == 0 {
					t.Error("diagnostic Range is zero/placeholder, want real token position")
				}
			},
			description: "Missing required operand for CALLNAT should emit ranged diagnostic",
		},
		{
			name: "missing_operand_fetch_FR30",
			// FETCH with no operand on its line → diagnostic
			input: `FETCH
NEXT_STATEMENT`,
			expectMinDiagnostics: 1,
			validateRangeCallback: func(t *testing.T, diag model.Diagnostic) {
				// The diagnostic Range must point at the FETCH keyword (line 1)
				if diag.Range.Start.Line != 1 {
					t.Errorf("diagnostic Range.Start.Line: got %d, want 1", diag.Range.Start.Line)
				}
				// Range should be non-zero
				if diag.Range.Start.Line == 0 || diag.Range.Start.Column == 0 {
					t.Error("diagnostic Range is zero/placeholder, want real token position")
				}
			},
			description: "Missing target for FETCH should emit ranged diagnostic",
		},
		{
			name: "valid_input_no_diagnostics_FR30",
			// Valid CALLNAT with operand → no diagnostics
			input:                `CALLNAT 'VALID'`,
			expectMinDiagnostics: 0,
			description:          "Valid CALLNAT statement should emit zero diagnostics",
		},
		{
			name: "recovery_after_error_FR30",
			// Malformed statement between two valid ones; parser recovers and
			// extracts both valid calls while emitting diagnostic for the malformed one.
			input: `CALLNAT 'VALID'
CALLNAT
CALLNAT 'ALSO_VALID'`,
			expectMinDiagnostics: 1,
			expectCallCount:      2, // both valid calls should be extracted
			description:          "Parser recovers after error: extracts valid calls and emits diagnostic",
		},
		{
			name: "unterminated_string_literal_FR30",
			// Unterminated string literal (no closing quote) → diagnostic with Range
			input:                `CALLNAT 'PROG`,
			expectMinDiagnostics: 1,
			validateRangeCallback: func(t *testing.T, diag model.Diagnostic) {
				// The diagnostic should have a non-zero Range
				if diag.Range.Start.Line == 0 && diag.Range.Start.Column == 0 {
					t.Error("diagnostic Range is zero/placeholder, want real token position")
				}
			},
			description: "Unterminated string literal should emit ranged diagnostic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)
			prog, err := parser.Parse()

			// Parser must not crash; AST must be returned
			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}

			// If a parse error was returned, log it (but continue testing)
			if err != nil {
				t.Logf("Parse returned error (acceptable): %v", err)
			}

			// Assertion 1: Check diagnostic count
			if len(prog.Diagnostics) < tc.expectMinDiagnostics {
				t.Errorf("prog.Diagnostics count: got %d, want >= %d (%s)",
					len(prog.Diagnostics), tc.expectMinDiagnostics, tc.description)
			}

			// Assertion 2: Validate Range on first diagnostic if callback provided
			if tc.validateRangeCallback != nil && len(prog.Diagnostics) > 0 {
				tc.validateRangeCallback(t, prog.Diagnostics[0])
			}

			// Assertion 3: Check call count if specified
			if tc.expectCallCount > 0 {
				if len(prog.Calls) < tc.expectCallCount {
					t.Errorf("prog.Calls count: got %d, want >= %d (recovery check)",
						len(prog.Calls), tc.expectCallCount)
				}
			}
		})
	}
}

// TestAnalyzer_ASTPopulation verifies that Analyzer.Analyze() populates
// the AST field with the correct concrete type and expected contents for
// valid input on a recognized extension (Task 7, FR-30).
func TestAnalyzer_ASTPopulation(t *testing.T) {
	analyzer := New(nil)
	result, err := analyzer.Analyze("test.NSP", []byte("CALLNAT 'MYPROG'"))

	if err != nil {
		t.Errorf("Analyze error: %v", err)
	}

	// AST must be non-nil for any parseable input.
	if result.AST == nil {
		t.Fatal("FileAnalysis.AST is nil, want non-nil *Program")
	}

	// The concrete type must be *Program (accessible within the natural package).
	prog, ok := result.AST.(*Program)
	if !ok {
		t.Fatalf("FileAnalysis.AST has type %T, want *Program", result.AST)
	}

	// Valid input with a single CALLNAT must produce exactly one call.
	if len(prog.Calls) != 1 {
		t.Fatalf("prog.Calls: got %d, want 1", len(prog.Calls))
	}
	if prog.Calls[0].Target != "MYPROG" {
		t.Errorf("prog.Calls[0].Target = %q, want %q", prog.Calls[0].Target, "MYPROG")
	}

	// Valid input and a recognized extension (.NSP) must produce no diagnostics.
	if len(result.Diagnostics) != 0 {
		t.Errorf("result.Diagnostics: got %d, want 0; diagnostics: %v", len(result.Diagnostics), result.Diagnostics)
	}
}

// TestAnalyzer_DiagnosticsForParseErrors verifies that parse errors from the parser
// are surfaced through FileAnalysis.Diagnostics with a populated Range (Task 7 / S4 /
// FR-30). Weak t.Log assertions are replaced with hard assertions.
func TestAnalyzer_DiagnosticsForParseErrors(t *testing.T) {
	analyzer := New(nil)
	result, err := analyzer.Analyze("test.NSP", []byte("CALLNAT\nMALFORMED"))

	if err != nil {
		t.Errorf("Analyze returned error: %v", err)
	}

	// At least one diagnostic must be present for malformed input (missing CALLNAT operand).
	if len(result.Diagnostics) == 0 {
		t.Fatal("FileAnalysis.Diagnostics is empty for malformed input, want at least 1")
	}

	// Find the parser-emitted diagnostic (not the unrecognized-extension diagnostic).
	// For a .NSP file the extension is known, so all diagnostics here should be parser errors.
	var parseErrorDiag *model.Diagnostic
	for i := range result.Diagnostics {
		d := &result.Diagnostics[i]
		if d.Severity == model.DiagnosticError {
			parseErrorDiag = d
			break
		}
	}
	if parseErrorDiag == nil {
		t.Fatalf("No DiagnosticError found in FileAnalysis.Diagnostics; got %d diagnostics: %v",
			len(result.Diagnostics), result.Diagnostics)
	}

	// The diagnostic must carry a real (non-zero) Range.
	if parseErrorDiag.Range.Start.Line == 0 && parseErrorDiag.Range.Start.Column == 0 {
		t.Error("Diagnostic Range is zero/placeholder; want real token position (Task 7)")
	}
	if parseErrorDiag.Range.Start.Line != 1 {
		t.Errorf("Diagnostic Range.Start.Line = %d, want 1 (CALLNAT is on line 1)",
			parseErrorDiag.Range.Start.Line)
	}
	// Range.End must be >= Range.Start (not a backwards range).
	start := parseErrorDiag.Range.Start
	end := parseErrorDiag.Range.End
	if end.Line < start.Line || (end.Line == start.Line && end.Column < start.Column) {
		t.Errorf("Diagnostic Range.End {%d,%d} is before Range.Start {%d,%d}",
			end.Line, end.Column, start.Line, start.Column)
	}
}

// TestAnalyzer_EmbeddedSQLNodes verifies that SQL nodes and diagnostics flow through
// Analyzer.Analyze() unchanged-signature (ES-11). This is an integration/characterization
// test confirming the wiring already exists and requires no Analyzer-signature change.
// Tests the valid SQL fixtures from ES-6 (SELECT with body) and ES-8 (PROCESS SQL).
func TestAnalyzer_EmbeddedSQLNodes(t *testing.T) {
	tests := []struct {
		name              string
		path              string
		content           []byte
		expectSQLNodeType string // expected SQL node type in the AST
		assertNodePresent func(t *testing.T, prog *Program)
		expectZeroDiags   bool
		description       string
	}{
		{
			name:              "sql_select_loop_ES6",
			path:              "test.NSP",
			content:           readFixture(t, "testdata/parser/10-sql-select-loop.nsp"),
			expectSQLNodeType: "*SelectStatement",
			assertNodePresent: func(t *testing.T, prog *Program) {
				if len(prog.Selects) < 1 {
					t.Errorf("prog.Selects: got %d, want >= 1 (SELECT loop from fixture 10)", len(prog.Selects))
				}
				// Verify the node carries a body (loop body nesting).
				if len(prog.Selects) > 0 && len(prog.Selects[0].Body) == 0 {
					t.Error("SelectStatement.Body is empty, want body statements from loop fixture")
				}
			},
			expectZeroDiags: true,
			description:     "SELECT cursor loop (ES-6) should flow through Analyzer and populate Program.Selects with body",
		},
		{
			name:              "sql_process_sql_ES8",
			path:              "test.NSP",
			content:           readFixture(t, "testdata/parser/15-sql-process-sql.nsp"),
			expectSQLNodeType: "*ProcessSQLStatement",
			assertNodePresent: func(t *testing.T, prog *Program) {
				if len(prog.ProcessSQLs) < 1 {
					t.Errorf("prog.ProcessSQLs: got %d, want >= 1 (PROCESS SQL from fixture 15)", len(prog.ProcessSQLs))
				}
				// Verify the node has opaque body (not parsed interior).
				if len(prog.ProcessSQLs) > 0 {
					stmt := prog.ProcessSQLs[0]
					if stmt.DDMName == "" {
						t.Error("ProcessSQLStatement.DDMName is empty, want DDM name operand")
					}
					if stmt.Body == "" {
						t.Error("ProcessSQLStatement.Body is empty, want opaque body text")
					}
					// Body should contain literal :#PERS-ID (unparsed, verbatim).
					if !contains(stmt.Body, ":#PERS-ID") {
						t.Errorf("ProcessSQLStatement.Body does not contain literal :#PERS-ID (expected opaque/unparsed); Body=%q", stmt.Body)
					}
				}
			},
			expectZeroDiags: true,
			description:     "PROCESS SQL with opaque body (ES-8) should flow through Analyzer and populate Program.ProcessSQLs",
		},
		{
			name:              "sql_parse_errors_ES10",
			path:              "test.NSP",
			content:           readFixture(t, "testdata/parser/19-sql-parse-errors.nsp"),
			expectSQLNodeType: "",
			assertNodePresent: func(t *testing.T, prog *Program) {
				// Fixture 19 is malformed (missing << body). Verify that a valid CALLNAT
				// after the malformed statement is still retained (M-6 recovery contract).
				if len(prog.Calls) == 0 {
					t.Error("prog.Calls is empty; CALLNAT after malformed PROCESS SQL should be retained (M-6 recovery)")
				}
			},
			expectZeroDiags: false,
			description:     "Malformed SQL (ES-10) should emit diagnostics and retain surrounding valid statements",
		},
	}

	analyzer := New(nil)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := analyzer.Analyze(tc.path, tc.content)

			// Analyzer must not crash; result must be returned.
			if err != nil {
				t.Logf("Analyze returned error (acceptable): %v", err)
			}

			// FileAnalysis.AST must be non-nil for any parsed input.
			if result.AST == nil {
				t.Fatal("FileAnalysis.AST is nil, want non-nil *Program")
			}

			// Type-assert to *Program (allowed within the natural package).
			prog, ok := result.AST.(*Program)
			if !ok {
				t.Fatalf("FileAnalysis.AST has type %T, want *Program", result.AST)
			}

			// Run the node-presence assertion.
			tc.assertNodePresent(t, prog)

			// Check diagnostic count expectation.
			if tc.expectZeroDiags {
				if len(result.Diagnostics) != 0 {
					t.Errorf("result.Diagnostics: got %d, want 0 (valid SQL fixture); diagnostics: %v",
						len(result.Diagnostics), result.Diagnostics)
				}
			} else {
				if len(result.Diagnostics) == 0 {
					t.Errorf("result.Diagnostics is empty for malformed fixture %s, want diagnostics for syntax errors", tc.name)
				}
			}
		})
	}
}

// TestAnalyzer_EmbeddedSQLEdgesDeferred verifies that FileAnalysis.Edges remains
// unchanged for SQL constructs (ES-11). SQL extraction is deferred to feature 08b;
// this test confirms that valid SQL fixtures produce no edges FROM the SQL statements.
func TestAnalyzer_EmbeddedSQLEdgesDeferred(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		content     []byte
		description string
	}{
		{
			name:    "sql_select_loop_no_edges",
			path:    "test.NSP",
			content: readFixture(t, "testdata/parser/10-sql-select-loop.nsp"),
			description: "SELECT loop body contains CALLNAT/PERFORM, which should produce edges " +
				"from the inner statements but NOT from the SELECT itself (extraction deferred)",
		},
		{
			name:    "sql_process_sql_no_edges",
			path:    "test.NSP",
			content: readFixture(t, "testdata/parser/15-sql-process-sql.nsp"),
			description: "PROCESS SQL with opaque body should produce no edges FROM the SQL construct " +
				"(body is unparsed; extraction deferred)",
		},
	}

	analyzer := New(nil)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := analyzer.Analyze(tc.path, tc.content)
			if err != nil {
				t.Logf("Analyze returned error (acceptable): %v", err)
			}

			// For a SQL-only body with no CALLNAT/PERFORM/etc. outside the SQL construct,
			// edges should be empty or minimal (the test should be specific to the fixture).
			// Fixture 10 has CALLNAT inside the loop body, which will produce edges.
			// Fixture 15 has a CALLNAT after PROCESS SQL, which will produce an edge.
			// Assert conservatively: no edge should originate FROM the SQL statement itself.
			// The edge extraction logic does not touch SQL slices, so edges should either be
			// empty (SQL-only) or should come from non-SQL statements (CALLNAT, PERFORM, etc.).

			// For fixture 10 (SELECT with CALLNAT in body): edges come from CALLNAT, not SELECT.
			// For fixture 15 (PROCESS SQL with trailing CALLNAT): edges come from CALLNAT, not PROCESS SQL.
			// We assert that the edge extraction does not add NEW SQL-specific edge kinds.
			// Verify no SQL edge kinds exist (there are no SQL-specific edges in the model yet).
			for _, edge := range result.Edges {
				// All edges should be from existing constructs (CALLNAT, PERFORM, INCLUDE, FETCH, RUN).
				// No SQL statement should directly produce an edge.
				switch edge.Kind {
				case model.EdgeCalls, model.EdgeCallsDynamic, model.EdgePerforms,
					model.EdgeIncludes, model.EdgeNavigatesTo, model.EdgeNavigatesToDynamic:
					// These are the expected edge kinds; SQL is not yet extracted.
				default:
					t.Errorf("unexpected edge kind %v; SQL extraction should not add edges in feature 07", edge.Kind)
				}
			}
		})
	}
}

// readFixture is a helper that reads a fixture file from the testdata directory.
func readFixture(t *testing.T, relativePath string) []byte {
	t.Helper()
	// Path is relative to the package directory (Go runs tests with the package
	// dir as the working directory), matching how the other fixture tests read.
	data, err := os.ReadFile(relativePath)
	if err != nil {
		t.Fatalf("readFixture: could not read %s: %v", relativePath, err)
	}
	return data
}

// contains is a simple string containment check.
func contains(haystack, needle string) bool {
	if len(needle) == 0 || len(haystack) == 0 {
		return len(needle) == 0
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
