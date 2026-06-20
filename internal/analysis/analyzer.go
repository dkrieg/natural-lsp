// Package analysis defines the Analyzer interface — the seam between the LSP
// layer and the extraction backend. LSP-facing code depends only on this
// interface, never on a concrete backend (e.g. the regex implementation in
// analysis/natural). This keeps the backend replaceable (PRD NFR-15).
package analysis

import "natural-lsp/internal/model"

// Analyzer extracts structured information from a single Natural source file.
type Analyzer interface {
	// Analyze extracts structure from one file's contents. Path is used for
	// object-type classification (by extension) and diagnostics.
	//
	// TODO: finalize the signature as requirements firm up.
	Analyze(path string, content []byte) (model.FileAnalysis, error)
}
