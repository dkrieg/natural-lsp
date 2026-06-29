// Package natural is the parser-based implementation of analysis.Analyzer.
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

// Analyzer is the parser-based extraction backend.
type Analyzer struct {
	// custom maps file extensions to model.ObjectType for user-defined overrides.
	// Keys must be normalized upper-case with leading dot (e.g., ".NAT").
	// Passed from config at construction time; consulted by classify().
	custom map[string]model.ObjectType
}

// New returns a parser-based Analyzer. custom maps normalized upper-case
// extensions (e.g. ".NAT") to ObjectType overrides sourced from config;
// pass nil for the default built-in table only.
func New(custom map[string]model.ObjectType) *Analyzer {
	return &Analyzer{custom: custom}
}

// compile-time assertion that Analyzer satisfies the analysis seam (NFR-15).
// This constraint must be preserved: LSP-facing code in internal/server,
// internal/workspace, and internal/document must consume FileAnalysis (including
// AST) only through the internal/model contract and the analysis.Analyzer
// interface. Type-asserting FileAnalysis.AST to concrete natural.* node types
// in LSP-facing code is forbidden and couples the LSP layer to the parser
// implementation, violating backend replaceability. See seam_test.go for
// architectural guard tests.
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
		// Range is a placeholder; the extension diagnostic has no meaningful source span.
		result.Diagnostics = append(result.Diagnostics, model.Diagnostic{
			Severity: model.DiagnosticInfo,
			Message:  fmt.Sprintf("unrecognized extension %q", normalizeExt(path)),
			Range:    model.Range{Start: model.Position{Line: 1, Column: 1}, End: model.Position{Line: 1, Column: 1}},
		})
	}

	// Parse the content into an AST. Parse always returns a non-nil AST and a nil
	// error; malformed input is surfaced through ast.Diagnostics with real token
	// positions rather than a returned error.
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	ast, _ := parser.Parse()
	result.AST = ast

	// Transfer parser diagnostics (syntax errors with real ranges) to the result.
	if ast != nil && len(ast.Diagnostics) > 0 {
		result.Diagnostics = append(result.Diagnostics, ast.Diagnostics...)
	}

	return result, nil
}
