// Package natural is the regex-based implementation of analysis.Analyzer.
// It extracts Natural constructs (calls, data access, structure) tuned to
// patterns found in production code. Per-file extraction produces *unresolved*
// references with caller context; cross-file binding happens in
// workspace/resolution.go, not here.
package natural

import (
	"natural-lsp/internal/analysis"
	"natural-lsp/internal/model"
)

// Analyzer is the regex-based extraction backend.
type Analyzer struct{}

// New returns a regex-based Analyzer.
func New() *Analyzer { return &Analyzer{} }

// compile-time assertion that Analyzer satisfies the analysis seam.
var _ analysis.Analyzer = (*Analyzer)(nil)

// Analyze runs the extraction pipeline over one file's contents.
func (a *Analyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	// TODO: extraction pipeline — object classification, calls (calls.go),
	// data access and definitions (data.go), structure, and diagnostics for
	// statement-like lines that match no pattern (PRD FR-30).
	return model.FileAnalysis{}, nil
}
