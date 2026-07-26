package server

import (
	"github.com/dkrieg/natural-lsp/internal/analysis"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// DeclarationTarget carries a cursor target's declaration context (NameRange + owning Symbol/DataDefinition).
// It is returned by findDeclarationTarget when a cursor is positioned on a data-field declaration
// or VIEW OF node name (feature 28, T8a).
//
// Fields:
//   - NameRange: the declaration's name-token span
//   - Definition: the owning DataDefinition (for ViewOfDDM, Redefines, etc.)
//   - Symbol: the owning Symbol (for tree structure, SelectionRange for same-file nav)
//   - OwningView: the owning VIEW OF node's Symbol (if this field is part of a view), else nil.
//     Used to determine if a field should resolve to a DDM field or a same-file structure.
type DeclarationTarget struct {
	NameRange  model.Range
	Definition *model.DataDefinition
	Symbol     *model.Symbol
	OwningView *model.Symbol // The enclosing VIEW OF node, if any (feature 28, T8b)
}

// rangeContains reports whether position p falls within range r (1-based, inclusive on both ends).
// A position P is contained in range R if it is not before R.Start and not after R.End:
//
//	P.Line > R.Start.Line || (P.Line == R.Start.Line && P.Column >= R.Start.Column)
//	AND
//	P.Line < R.End.Line   || (P.Line == R.End.Line   && P.Column <= R.End.Column)
func rangeContains(r model.Range, p model.Position) bool {
	if p.Line < r.Start.Line || (p.Line == r.Start.Line && p.Column < r.Start.Column) {
		return false
	}
	if p.Line > r.End.Line || (p.Line == r.End.Line && p.Column > r.End.Column) {
		return false
	}
	return true
}

// rangeSpanSize returns (lineSpan, columnSpan) for a range, used for smallest-span tie-breaking.
// Smaller values indicate a tighter, more precisely enclosing span.
func rangeSpanSize(r model.Range) (lines int, cols int) {
	return r.End.Line - r.Start.Line, r.End.Column - r.Start.Column
}

// isSmallerSpan reports whether (line1, col1) is a strictly smaller span than (line2, col2).
// Line span is compared first; column span is the tie-breaker.
func isSmallerSpan(line1, col1, line2, col2 int) bool {
	if line1 != line2 {
		return line1 < line2
	}
	return col1 < col2
}

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
	var smallestEdge *model.EdgeEntry
	var smallestEdgeLineSpan, smallestEdgeColSpan int

	var smallestAccess *model.DataAccessEntry
	var smallestAccessLineSpan, smallestAccessColSpan int

	var smallestVar *model.VariableRef
	var smallestVarLineSpan, smallestVarColSpan int

	// Scan edges: match by Source range.
	for i := range fa.Edges {
		if rangeContains(fa.Edges[i].Source, pos) {
			lineSpan, colSpan := rangeSpanSize(fa.Edges[i].Source)
			if smallestEdge == nil || isSmallerSpan(lineSpan, colSpan, smallestEdgeLineSpan, smallestEdgeColSpan) {
				smallestEdge = &fa.Edges[i]
				smallestEdgeLineSpan = lineSpan
				smallestEdgeColSpan = colSpan
			}
		}
	}

	// Scan data-access entries: match by NameRange (the view/DDM name token, not the whole statement).
	for i := range fa.DataAccess {
		if rangeContains(fa.DataAccess[i].NameRange, pos) {
			lineSpan, colSpan := rangeSpanSize(fa.DataAccess[i].NameRange)
			if smallestAccess == nil || isSmallerSpan(lineSpan, colSpan, smallestAccessLineSpan, smallestAccessColSpan) {
				smallestAccess = &fa.DataAccess[i]
				smallestAccessLineSpan = lineSpan
				smallestAccessColSpan = colSpan
			}
		}
	}

	// Extract variable refs on demand from the content (in-memory only).
	var varRefs []model.VariableRef
	if az != nil {
		varRefs = az.ExtractVariableRefs(content)
	}

	// Scan variable refs: match by Range (lowest priority after edges/data-access).
	for i := range varRefs {
		if rangeContains(varRefs[i].Range, pos) {
			lineSpan, colSpan := rangeSpanSize(varRefs[i].Range)
			if smallestVar == nil || isSmallerSpan(lineSpan, colSpan, smallestVarLineSpan, smallestVarColSpan) {
				smallestVar = &varRefs[i]
				smallestVarLineSpan = lineSpan
				smallestVarColSpan = colSpan
			}
		}
	}

	// Scan persisted host-var refs (feature 27 T8): same lowest priority as variable refs.
	for i := range fa.HostVarRefs {
		if rangeContains(fa.HostVarRefs[i].Range, pos) {
			lineSpan, colSpan := rangeSpanSize(fa.HostVarRefs[i].Range)
			if smallestVar == nil || isSmallerSpan(lineSpan, colSpan, smallestVarLineSpan, smallestVarColSpan) {
				// Convert HostVarRef to VariableRef for uniform handling downstream.
				smallestVar = &model.VariableRef{
					Name:  fa.HostVarRefs[i].Name,
					Range: fa.HostVarRefs[i].Range,
				}
				smallestVarLineSpan = lineSpan
				smallestVarColSpan = colSpan
			}
		}
	}

	// Return the overall smallest containing range with precedence: edge > access > variable.
	if smallestEdge != nil {
		return smallestEdge, nil, nil
	}
	if smallestAccess != nil {
		return nil, smallestAccess, nil
	}
	if smallestVar != nil {
		return nil, nil, smallestVar
	}

	return nil, nil, nil
}

// findDeclarationTarget maps a cursor to a Symbol/DataDefinition declaration NameRange under it
// (data fields + VIEW OF nodes), smallest-containing-range wins.
// Returns nil when no declaration is under the cursor (feature 28, T8a, OQ-B).
//
// This is a COMPANION function to findCursorTarget (not a replacement). It is called by
// providers only when findCursorTarget returns nil (no use-site), implementing use-site-first
// precedence at the call site rather than changing findCursorTarget's signature.
//
// The function walks fa.Structure (the recursive Symbol tree) to find the smallest-containing
// SelectionRange that encloses the cursor position. When found, it populates DeclarationTarget
// with the node's NameRange, the matching DataDefinition from fa.Definitions (if resolvable),
// the matched Symbol itself (always set when a declaration is found), and the OwningView
// (if the matched Symbol is a field within a VIEW OF) — feature 28, T8b.
//
// Modeled gaps (missing or unresolved definitions) are gracefully handled: Symbol is always
// set on a match, but Definition and OwningView may be nil if not applicable (FR-43).
func findDeclarationTarget(fa model.FileAnalysis, pos model.Position) *DeclarationTarget {
	// findDefinitionByName searches fa.Definitions (and their Children) for a definition
	// whose Name matches the given name. Returns nil when not found (graceful, FR-43).
	var findDefinitionByName func(name string, defs []model.DataDefinition) *model.DataDefinition
	findDefinitionByName = func(name string, defs []model.DataDefinition) *model.DataDefinition {
		for i := range defs {
			if defs[i].Name == name {
				return &defs[i]
			}
			if found := findDefinitionByName(name, defs[i].Children); found != nil {
				return found
			}
		}
		return nil
	}

	// Walk the Symbol tree to find the Symbol whose SelectionRange is the smallest
	// range that still contains pos. This mirrors findCursorTarget's span tie-break.
	// Also track the owning VIEW OF node (if any) during the walk.
	var smallestSymbol *model.Symbol
	var smallestLineSpan, smallestColSpan int
	var owningView *model.Symbol

	var walk func(*model.Symbol, *model.Symbol) // (current sym, owning view or nil)
	walk = func(sym *model.Symbol, view *model.Symbol) {
		// Update the owning view context when we enter a VIEW OF node
		newView := view
		if sym.ViewOfDDM != "" {
			newView = sym
		}

		if rangeContains(sym.SelectionRange, pos) {
			lineSpan, colSpan := rangeSpanSize(sym.SelectionRange)
			if smallestSymbol == nil || isSmallerSpan(lineSpan, colSpan, smallestLineSpan, smallestColSpan) {
				smallestSymbol = sym
				smallestLineSpan = lineSpan
				smallestColSpan = colSpan
				owningView = newView
			}
		}

		// Recurse into children, passing down the view context
		for i := range sym.Children {
			walk(&sym.Children[i], newView)
		}
	}

	if fa.Structure != nil {
		walk(fa.Structure, nil)
	}

	if smallestSymbol == nil {
		return nil
	}

	result := &DeclarationTarget{
		NameRange:  smallestSymbol.SelectionRange,
		Symbol:     smallestSymbol,
		OwningView: owningView,
	}
	// Resolve the backing DataDefinition by name; may be nil for structural symbols
	// (e.g. data-section nodes, subroutines) that have no corresponding DataDefinition (FR-43).
	if smallestSymbol.Name != "" {
		result.Definition = findDefinitionByName(smallestSymbol.Name, fa.Definitions)
	}
	return result
}
