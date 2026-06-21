// Package natural is the regex-based implementation of analysis.Analyzer.
// It extracts Natural constructs (calls, data access, structure) tuned to
// patterns found in production code. Per-file extraction produces *unresolved*
// references with caller context; cross-file binding happens in
// workspace/resolution.go, not here.
package natural

import (
	"fmt"
	"natural-lsp/internal/analysis"
	"natural-lsp/internal/model"
)

// Analyzer is the regex-based extraction backend.
type Analyzer struct {
	// custom maps file extensions to model.ObjectType for user-defined overrides.
	// Keys must be normalized upper-case with leading dot (e.g., ".NAT").
	// Passed from config at construction time; consulted by classify().
	custom map[string]model.ObjectType
}

// New returns a regex-based Analyzer. custom maps normalized upper-case
// extensions (e.g. ".NAT") to ObjectType overrides sourced from config;
// pass nil for the default built-in table only.
func New(custom map[string]model.ObjectType) *Analyzer {
	return &Analyzer{custom: custom}
}

// compile-time assertion that Analyzer satisfies the analysis seam.
var _ analysis.Analyzer = (*Analyzer)(nil)

// Analyze runs the extraction pipeline over one file's contents.
func (a *Analyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	result := model.FileAnalysis{}
	result.ObjectType = classify(path, a.custom)

	// If the extension is unknown, append a diagnostic for observability (FR-9).
	// FileAnalysis.Diagnostics is the analyzer-side signal for unrecognized files;
	// the feature-03 indexer reads Diagnostics to emit SkipReason entries and
	// structured log output — that wiring is out of scope here.
	if result.ObjectType == model.ObjectUnknown {
		result.Diagnostics = append(result.Diagnostics, model.Diagnostic{
			Severity: model.DiagnosticInfo,
			Message:  fmt.Sprintf("unrecognized extension %q", normalizeExt(path)),
		})
	}

	// TODO: extraction pipeline — calls (calls.go), data access and definitions (data.go),
	// structure, and diagnostics for statement-like lines that match no pattern (PRD FR-30).
	return result, nil
}
