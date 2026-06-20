// Package model defines the analyzer's output types — the contract shared
// between the extraction backend (internal/analysis), the workspace index
// (internal/workspace), and the LSP layer (internal/server).
//
// Keep these types free of backend internals (regex, parser state, etc.) so
// the extraction backend can be replaced and so the extracted structure stays
// clean enough for external consumers. See docs/plans/natural-lsp-prd.md
// (NFR-15, NFR-16).
package model

// EdgeKind classifies a relationship extracted from Natural source.
type EdgeKind string

const (
	EdgeCalls        EdgeKind = "CALLS"         // CALLNAT 'LITERAL' — static
	EdgeCallsDynamic EdgeKind = "CALLS_DYNAMIC" // CALLNAT #VARIABLE — unresolved
	EdgeNavigatesTo  EdgeKind = "NAVIGATES_TO"  // FETCH / RUN 'LITERAL'
	EdgePerforms     EdgeKind = "PERFORMS"      // PERFORM subroutine
	EdgeIncludes     EdgeKind = "INCLUDES"      // INCLUDE copycode
	EdgeReads        EdgeKind = "READS"         // READ / FIND / GET
	EdgeWrites       EdgeKind = "WRITES"        // STORE / UPDATE / DELETE
)

// FileAnalysis is the structured result of analyzing a single Natural source
// file. Fields are intentionally left to be filled in as features land; see
// the functional requirements in docs/plans/natural-lsp-prd.md.
type FileAnalysis struct {
	// TODO: object metadata, symbols, references/edges, data access,
	// program structure, and extraction diagnostics.
}
