package workspace

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestResolve_AmbiguityDiagnosticCode_FlatNamespace verifies that the resolver producer
// stamps `Code = DiagnosticCodeAmbiguity` on flat-namespace ambiguity diagnostics
// (Feature 14, Task 0, Story 2 AC3, FR-31).
//
// Fixture: testdata/resolution/ambiguous-flat/
//   - LIBA/DUP.NSN and LIBB/DUP.NSN (same module name in two locations)
//   - MAIN.NSP with CALLNAT 'DUP' (calls the ambiguous name)
//   - NO .natural-lsp.toml [resolution] library map (flat namespace)
//
// Expected outcomes:
//   - ResolutionSet.DiagnosticsFor("MAIN.NSP") returns a diagnostic
//   - The diagnostic's Code == DiagnosticCodeAmbiguity
//   - The diagnostic is distinct from syntax diagnostics (different Code value)
func TestResolve_AmbiguityDiagnosticCode_FlatNamespace(t *testing.T) {
	// Build the index from the fixture using the real analyzer.
	workspaceRoot := "testdata/resolution/ambiguous-flat"
	cfg := config.Defaults()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	idx, _, _, err := BuildWithCache(context.Background(), workspaceRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("BuildWithCache failed: %v", err)
	}

	// Call Resolve with the flat (no-library-map) config.
	resSet := Resolve(idx, &cfg)

	t.Run("ambiguity_diagnostic_has_ambiguity_code", func(t *testing.T) {
		// Get diagnostics for the referencing file (MAIN.NSP)
		diags := resSet.DiagnosticsFor("MAIN.NSP")

		// Should have at least one diagnostic (the ambiguity)
		if len(diags) == 0 {
			t.Fatal("DiagnosticsFor(\"MAIN.NSP\") returned empty, want ambiguity diagnostic")
		}

		// Find the ambiguity diagnostic (it should mention "ambiguous" or both candidates)
		var ambigDiag *model.Diagnostic
		for i, diag := range diags {
			// Heuristic: ambiguity diagnostics are warnings and mention the candidates
			if diag.Severity == model.DiagnosticWarning {
				ambigDiag = &diags[i]
				break
			}
		}

		if ambigDiag == nil {
			t.Fatal("No warning-severity diagnostic found in DiagnosticsFor(\"MAIN.NSP\")")
		}

		// ASSERTION: The diagnostic must have Code == DiagnosticCodeAmbiguity
		// This will compile-fail in RED phase until T0 GREEN adds model.DiagnosticCode
		// and the resolver sets it (line ~499 in resolution.go)
		if ambigDiag.Code != model.DiagnosticCodeAmbiguity {
			t.Errorf("ambiguity diagnostic Code = %q, want %q (DiagnosticCodeAmbiguity)",
				ambigDiag.Code, model.DiagnosticCodeAmbiguity)
		}
	})
}
