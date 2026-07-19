package natural

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestAnalyzer_DiagnosticCode_SyntaxErrors verifies that the parser/analyzer producer
// stamps `Code = DiagnosticCodeSyntax` on every diagnostic it emits from parse errors
// (Feature 14, Task 0, Story 2 AC3, FR-30).
//
// This test covers:
// - Parse errors from ast.Diagnostics (transferred to FileAnalysis.Diagnostics)
// - The unrecognized-extension diagnostic (also a source-level issue, not ambiguity)
//
// Expected outcomes:
// - Every diagnostic from analyzer.Analyze() for a parser error has Code == DiagnosticCodeSyntax
// - The unrecognized-extension diagnostic (ObjectUnknown) has Code == DiagnosticCodeSyntax
// - Diagnostics from valid input have empty (or consistent) Code values, preserving back-compat
func TestAnalyzer_DiagnosticCode_SyntaxErrors(t *testing.T) {
	tests := []struct {
		name                string
		path                string
		content             []byte
		expectDiagnostics   int
		expectAllCodeSyntax bool
	}{
		{
			name:                "parse_error_callnat_missing_operand",
			path:                "test.NSP",
			content:             []byte("CALLNAT\nMALFORMED"),
			expectDiagnostics:   1, // at least one diagnostic for missing operand
			expectAllCodeSyntax: true,
		},
		{
			name:                "unrecognized_extension",
			path:                "test.XYZ",
			content:             []byte("some content"),
			expectDiagnostics:   1, // one diagnostic for unrecognized extension
			expectAllCodeSyntax: true,
		},
		{
			name:                "fixture_parse_errors_04",
			path:                filepath.Join("testdata", "parser", "04-parser-parse-errors.nsp"),
			expectDiagnostics:   4, // fixture contains multiple malformed statements
			expectAllCodeSyntax: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var content []byte
			if len(tc.content) == 0 && tc.path == filepath.Join("testdata", "parser", "04-parser-parse-errors.nsp") {
				// Read the fixture file from disk
				data, err := os.ReadFile(tc.path)
				if err != nil {
					t.Fatalf("Failed to read fixture %s: %v", tc.path, err)
				}
				content = data
			} else {
				content = tc.content
			}

			// Analyze the content
			analyzer := New(nil)
			result, err := analyzer.Analyze(tc.path, content)

			// Per FR-43, Analyze never returns an error (graceful degradation)
			if err != nil {
				t.Errorf("Analyze(%q, …) error = %v, want nil (graceful degradation)", tc.path, err)
			}

			// Assert we have the expected number of diagnostics
			if len(result.Diagnostics) < tc.expectDiagnostics {
				t.Errorf("Analyze(%q, …) produced %d diagnostics, want >= %d",
					tc.path, len(result.Diagnostics), tc.expectDiagnostics)
			}

			// ASSERTION: Every diagnostic must have Code set (RED phase will fail here)
			// Once T0 GREEN phase adds the field and sets it, this assertion should pass.
			if tc.expectAllCodeSyntax {
				for i, diag := range result.Diagnostics {
					// The Code field must exist and be set to DiagnosticCodeSyntax
					// This will be a compile error until T0 GREEN adds model.DiagnosticCode and Code field
					if diag.Code != model.DiagnosticCodeSyntax {
						t.Errorf("Analyze(%q, …) Diagnostics[%d].Code = %q, want %q (DiagnosticCodeSyntax)",
							tc.path, i, diag.Code, model.DiagnosticCodeSyntax)
					}
				}
			}
		})
	}
}

// TestAnalyzer_UnrecognizedExtension_DiagnosticCode verifies that the unrecognized-extension
// diagnostic (emitted for ObjectUnknown classification) is stamped with Code == DiagnosticCodeSyntax
// (Feature 14, Task 0, FR-30).
//
// This is a dedicated test for the unrecognized-extension path to ensure it gets the right code.
func TestAnalyzer_UnrecognizedExtension_DiagnosticCode(t *testing.T) {
	analyzer := New(nil)
	result, err := analyzer.Analyze("unknown.XYZ", []byte("anything"))

	if err != nil {
		t.Errorf("Analyze error = %v, want nil", err)
	}

	// Should classify as ObjectUnknown
	if result.ObjectType != model.ObjectUnknown {
		t.Errorf("ObjectType = %v, want ObjectUnknown", result.ObjectType)
	}

	// Should have at least one diagnostic
	if len(result.Diagnostics) == 0 {
		t.Fatal("Diagnostics is empty for unknown extension, want at least 1")
	}

	// ASSERTION: The diagnostic must have Code == DiagnosticCodeSyntax
	// (This will compile-fail in RED phase until T0 GREEN adds the field)
	diag := result.Diagnostics[0]
	if diag.Code != model.DiagnosticCodeSyntax {
		t.Errorf("unrecognized-extension diagnostic Code = %q, want %q",
			diag.Code, model.DiagnosticCodeSyntax)
	}
}
