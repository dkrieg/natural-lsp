package server

import (
	"github.com/dkrieg/natural-lsp/internal/analysis"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// findCursorTarget returns the reference site (EdgeEntry, DataAccessEntry, or VariableRef)
// under the given cursor position, if any.
//
// Given a FileAnalysis, content (for on-demand variable ref extraction), an Analyzer
// (for variable ref extraction), and a 1-based model.Position, this function searches
// the file's Edges, DataAccess entries, and VariableRefs to find the one whose
// range contains the cursor. For edges, the search range is EdgeEntry.Source (the whole statement).
// For data-access entries, the search range is DataAccessEntry.NameRange (just
// the view/DDM name token, not the whole statement — this matches the semantic
// granularity for hovering on field names).
// For variable refs, the search range is VariableRef.Range (the identifier).
//
// VariableRefs are extracted on demand (in-memory only, not persisted) via the Analyzer,
// ensuring consistency with the current content.
//
// At most one of the returned values is non-nil; all are nil if the cursor is
// outside any reference range.
//
// Precedence (smallest-containing tie-break): Edges win over DataAccess, which win
// over VariableRef. This ensures a call/data reference on the same token wins the
// tie-break; only when neither contains the cursor do we consult variable refs.
//
// For overlapping ranges, the smallest containing range is returned (deterministic
// tie-break). Containment is inclusive on both ends per model.Range convention:
// a position P is contained in range R if:
//   - (P.Line > R.Start.Line || (P.Line == R.Start.Line && P.Column >= R.Start.Column))
//   - AND (P.Line < R.End.Line || (P.Line == R.End.Line && P.Column <= R.End.Column))
func findCursorTarget(fa model.FileAnalysis, pos model.Position, content string, az analysis.Analyzer) (*model.EdgeEntry, *model.DataAccessEntry, *model.VariableRef) {
	// Helper to check if a position is contained in a range (1-based, inclusive).
	// P is in R iff NOT (P < R.Start) AND NOT (P > R.End)
	contains := func(r model.Range, p model.Position) bool {
		// Check if p is before r.Start
		if p.Line < r.Start.Line || (p.Line == r.Start.Line && p.Column < r.Start.Column) {
			return false
		}
		// Check if p is after r.End
		if p.Line > r.End.Line || (p.Line == r.End.Line && p.Column > r.End.Column) {
			return false
		}
		return true
	}

	// spanSize computes (lineSpan, columnSpan) for deterministic tie-breaking.
	// Smaller values indicate tighter, more precise containment.
	spanSize := func(r model.Range) (int, int) {
		return r.End.Line - r.Start.Line, r.End.Column - r.Start.Column
	}

	// isSmallerSpan returns true if span1 is smaller (tighter) than span2.
	// Compare line span first, then column span.
	isSmallerSpan := func(line1, col1, line2, col2 int) bool {
		if line1 != line2 {
			return line1 < line2
		}
		return col1 < col2
	}

	var smallestEdge *model.EdgeEntry
	var smallestEdgeLineSpan, smallestEdgeColSpan int

	var smallestAccess *model.DataAccessEntry
	var smallestAccessLineSpan, smallestAccessColSpan int

	var smallestVar *model.VariableRef
	var smallestVarLineSpan, smallestVarColSpan int

	// Scan edges: match by Source range
	for i := range fa.Edges {
		if contains(fa.Edges[i].Source, pos) {
			lineSpan, colSpan := spanSize(fa.Edges[i].Source)
			if smallestEdge == nil || isSmallerSpan(lineSpan, colSpan, smallestEdgeLineSpan, smallestEdgeColSpan) {
				smallestEdge = &fa.Edges[i]
				smallestEdgeLineSpan = lineSpan
				smallestEdgeColSpan = colSpan
			}
		}
	}

	// Scan data-access: match by NameRange
	for i := range fa.DataAccess {
		if contains(fa.DataAccess[i].NameRange, pos) {
			lineSpan, colSpan := spanSize(fa.DataAccess[i].NameRange)
			if smallestAccess == nil || isSmallerSpan(lineSpan, colSpan, smallestAccessLineSpan, smallestAccessColSpan) {
				smallestAccess = &fa.DataAccess[i]
				smallestAccessLineSpan = lineSpan
				smallestAccessColSpan = colSpan
			}
		}
	}

	// Extract variable refs on demand from the content (in-memory only)
	var varRefs []model.VariableRef
	if az != nil {
		varRefs = az.ExtractVariableRefs(content)
	}

	// Scan variable refs: match by Range (lowest priority after edges/data-access)
	for i := range varRefs {
		if contains(varRefs[i].Range, pos) {
			lineSpan, colSpan := spanSize(varRefs[i].Range)
			if smallestVar == nil || isSmallerSpan(lineSpan, colSpan, smallestVarLineSpan, smallestVarColSpan) {
				smallestVar = &varRefs[i]
				smallestVarLineSpan = lineSpan
				smallestVarColSpan = colSpan
			}
		}
	}

	// Return the overall smallest containing range, with precedence: edge > access > variable.
	// If an edge is found, return it (and ignore access/variable).
	if smallestEdge != nil {
		return smallestEdge, nil, nil
	}
	// If access is found, return it (and ignore variable).
	if smallestAccess != nil {
		return nil, smallestAccess, nil
	}
	// Otherwise, return variable if found.
	if smallestVar != nil {
		return nil, nil, smallestVar
	}

	return nil, nil, nil
}
