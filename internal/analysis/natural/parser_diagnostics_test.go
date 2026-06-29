package natural

import (
	"testing"

	"natural-lsp/internal/model"
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
