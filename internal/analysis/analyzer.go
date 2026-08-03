// Package analysis defines the Analyzer interface — the seam between the LSP
// layer and the extraction backend. LSP-facing code depends only on this
// interface, never on a concrete backend (e.g. the regex implementation in
// analysis/natural). This keeps the backend replaceable (PRD NFR-15).
package analysis

import "github.com/dkrieg/natural-lsp/internal/model"

// Analyzer extracts structured information from a single Natural source file.
type Analyzer interface {
	// Analyze extracts structure from one file's contents. Path is used for
	// object-type classification (by extension) and diagnostics.
	//
	// TODO: finalize the signature as requirements firm up.
	Analyze(path string, content []byte) (model.FileAnalysis, error)

	// ExtractVariableRefs extracts variable use-site references from source content.
	// This is a lightweight, on-demand scan that returns every variable identifier
	// occurrence in statement bodies (not including DEFINE DATA declarations).
	// The returned slice is always non-nil but may be empty.
	// This method is used by navigation providers to enable variable go-to-definition
	// and find-references (feature 27).
	ExtractVariableRefs(content string) []model.VariableRef

	// SemanticTokens classifies tokens in source content for syntax/semantic highlighting.
	// This is an on-demand, non-persisted operation that returns classified token spans
	// for display in the editor. Path is used for object-type classification (by extension).
	// The returned slice is always non-nil but may be empty.
	// This method is used by the LSP provider for semantic-tokens (feature 29).
	SemanticTokens(path string, content []byte) []model.SemanticToken
}
