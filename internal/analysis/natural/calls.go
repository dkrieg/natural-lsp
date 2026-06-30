// Package natural: extraction of call/dependency constructs — CALLNAT, FETCH,
// RUN, PERFORM, INCLUDE. Produces references with caller context and the
// appropriate EdgeKind; literal targets are emitted as static references and
// variable targets as dynamic (unresolved) ones. Binding to definitions is
// done later by workspace/resolution.go (PRD FR-10..18).
package natural

import (
	"sort"
	"strings"

	"natural-lsp/internal/model"
)

// isStaticLiteral reports whether a target can be resolved as a static
// (index-time) reference, as opposed to a dynamic (runtime) one.
//
// FR-18: a literal containing a '&' character carries a Natural
// runtime-substitution placeholder (e.g. 'PRG&LANG', '&PFX-RPT', 'A&1').
// Treating such a literal as a static reference would produce a false edge to
// a non-existent object whose name is the raw text including the '&'; the
// actual target is only known at runtime. The placeholder can appear anywhere
// in the literal, so a simple substring check suffices.
//
// Returns true only when targetIsLiteral is true and target contains no '&'.
//
// Examples:
//
//	isStaticLiteral(true, "PROGNAME")      → true  (clean literal, static)
//	isStaticLiteral(true, "PRG&LANG")      → false (mid-word placeholder, dynamic)
//	isStaticLiteral(true, "&PFX-RPT")      → false (leading placeholder, dynamic)
//	isStaticLiteral(true, "A&1")           → false (trailing placeholder, dynamic)
//	isStaticLiteral(false, "#PROGNAME")    → false (variable reference, dynamic)
func isStaticLiteral(targetIsLiteral bool, target string) bool {
	return targetIsLiteral && !strings.Contains(target, "&")
}

// edgeKind selects between a static and a dynamic EdgeKind based on whether
// the statement's target is a literal (static, resolved at index time) or a
// variable reference (dynamic, unresolvable until runtime — FR-17/M-6).
//
// Pass the static/dynamic pair for the statement kind:
//
//	CALLNAT: edgeKind(isLiteral, model.EdgeCalls,       model.EdgeCallsDynamic)
//	FETCH:   edgeKind(isLiteral, model.EdgeNavigatesTo, model.EdgeNavigatesToDynamic)
//	RUN:     edgeKind(isLiteral, model.EdgeNavigatesTo, model.EdgeNavigatesToDynamic)
func edgeKind(targetIsLiteral bool, static, dynamic model.EdgeKind) model.EdgeKind {
	if targetIsLiteral {
		return static
	}
	return dynamic
}

// stmtRange builds a model.Range from the start and end positions carried on
// every parsed statement node (StartPos/EndPos). It is a named constructor so
// the repeated struct literal doesn't obscure the per-kind differences between
// CALLNAT, PERFORM, and INCLUDE loops below.
func stmtRange(start, end model.Position) model.Range {
	return model.Range{Start: start, End: end}
}

// extractEdges walks the parsed program and returns call/dependency edges for
// every recognized call statement.
//
// CALLNAT with a literal target produces an EdgeCalls edge (static reference).
// CALLNAT with a variable target produces an EdgeCallsDynamic edge (dynamic,
// unresolvable at index time — modeled separately, never surfaced as a
// diagnostic; channel separation FR-17/M-6).
//
// PERFORM produces an EdgePerforms edge (FR-12). PERFORM targets are always
// identifiers (subroutine names), never quoted literals.
//
// INCLUDE produces an EdgeIncludes edge (copycode dependency). The target is
// always a literal copycode name. Incremental re-analysis on copycode change is
// handled downstream by workspace/index.go:Invalidate, which walks EdgeIncludes
// to find all dependents — no index change is required in this feature.
// Note: INCLUDE is NOT subject to the FR-18 placeholder downgrade — copycode
// is a compile-time textual inclusion resolved before execution, so its name is
// always a literal with no runtime substitution.
//
// FETCH with a literal target produces an EdgeNavigatesTo edge; with a variable
// target it produces EdgeNavigatesToDynamic. FETCH has no library qualifier —
// its optional second operand is a parameter field, not a library-id — so the
// Library field on FETCH edges is always empty.
//
// RUN with a literal target produces an EdgeNavigatesTo edge; with a variable
// target it produces EdgeNavigatesToDynamic. When RUN carries an optional
// library-id as its second positional operand, that value is placed in the
// Library field on the edge; otherwise Library is empty.
//
// FR-18 placeholder downgrade: for CALLNAT, FETCH, and RUN only, a literal
// target whose text contains a '&' runtime-substitution placeholder is
// downgraded to the dynamic edge kind. This prevents false static edges to
// objects named by the raw text (which would not exist). See isStaticLiteral.
// INCLUDE is explicitly excluded from this rule (see above).
//
// Field conventions for each returned EdgeEntry:
//
//   - Source: the statement range (CALLNAT/PERFORM/FETCH/RUN/INCLUDE keyword
//     through the end of the statement), for all edge kinds.
//
//   - TargetName: the referenced name exactly as written in the source (already
//     uppercased by the lexer for identifiers; unquoted string value for literals).
//
//   - Target:
//     -- PERFORM: the range of the matching inline DEFINE SUBROUTINE definition
//     within this file if the target name resolves to an inline subroutine;
//     otherwise the zero Range (model.Range{}), signalling that the target is
//     external or unresolved and binding is deferred to the resolution feature.
//     -- CALLNAT / FETCH / RUN / INCLUDE: the operand-span at the reference site
//     (the token range of the literal or identifier that names the target);
//     cross-file binding is deferred to workspace/resolution.go.
//
// Contract — source order: each per-kind slice on *Program (prog.Calls,
// prog.Performs, prog.Includes, etc.) is already in source order because the
// parser appends nodes as it encounters them. extractEdges combines all
// per-kind slices and returns them in global source order via a stable sort on
// Source.Start (line, then column), so callers always receive a deterministic,
// globally source-ordered edge list regardless of the interleaving of statement
// kinds in the source file.
//
// Binding edges to their definitions is the responsibility of
// workspace/resolution.go (PRD FR-10..18); extractEdges is purely syntactic.
func extractEdges(prog *Program) []model.EdgeEntry {
	var edges []model.EdgeEntry

	// Build a map of inline subroutine names to their definition ranges.
	// Both Subroutine.Name and PerformStatement.Target are set from TokenIdentifier
	// literals, which the lexer normalizes to uppercase unconditionally (lexer.go:
	// uppercase()). Case-insensitive matching is therefore correct by construction:
	// "shared-logic", "Shared-Logic", and "SHARED-LOGIC" all produce the same map
	// key, so no explicit normalization is needed at lookup time.
	inlineSubs := make(map[string]model.Range)
	for _, sub := range prog.Subroutines {
		defStart, defEnd := sub.Position()
		inlineSubs[sub.Name] = model.Range{
			Start: defStart,
			End:   defEnd,
		}
	}

	// CALLNAT: static (literal) or dynamic (variable) call edge.
	// The parser only appends a CallStatement when call.Target is non-empty, so
	// the guard below is defensive. It ensures no silent empty edge is ever
	// emitted regardless of future parser changes.
	// FR-18: literal targets containing '&' are downgraded to dynamic edges.
	// Channel separation: parser emits diagnostic; no edge emitted here (FR-17/M-6).
	for _, call := range prog.Calls {
		if call.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		edges = append(edges, model.EdgeEntry{
			Kind:       edgeKind(isStaticLiteral(call.TargetIsLiteral, call.Target), model.EdgeCalls, model.EdgeCallsDynamic),
			TargetName: call.Target,
			Source:     stmtRange(call.StartPos, call.EndPos),
			Target:     call.TargetRange,
		})
	}

	// PERFORM: intra-file subroutine call edge.
	// If the parser encounters a same-line token that is not a TokenIdentifier,
	// it appends the node with Target == "" — skip those to avoid empty edges.
	// Channel separation: parser emits diagnostic; no edge emitted here (FR-17/M-6).
	// FR-12: Target is set to the inline subroutine definition range when found;
	// otherwise it is left as the zero Range, deferring binding to resolution.
	for _, perform := range prog.Performs {
		if perform.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		// Look up the target in the inline subroutines map.
		// If found, set Target to the definition's range; otherwise leave it as zero (model.Range{}).
		targetRange := inlineSubs[perform.Target]

		edges = append(edges, model.EdgeEntry{
			Kind:       model.EdgePerforms,
			TargetName: perform.Target,
			Source:     stmtRange(perform.StartPos, perform.EndPos),
			Target:     targetRange,
		})
	}

	// INCLUDE: copycode dependency edge.
	// If the parser encounters a same-line token that is neither a string literal
	// nor an identifier, it appends the node with Target == "" — skip those.
	// Channel separation: parser emits diagnostic; no edge emitted here (FR-17/M-6).
	// Note: INCLUDE is NOT subject to FR-18 placeholder downgrade — copycode is a
	// compile-time textual inclusion; its name is always a literal with no
	// runtime substitution.
	for _, include := range prog.Includes {
		if include.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		edges = append(edges, model.EdgeEntry{
			Kind:       model.EdgeIncludes,
			TargetName: include.Target,
			Source:     stmtRange(include.StartPos, include.EndPos),
			Target:     include.TargetRange,
		})
	}

	// FETCH: navigation edge (EdgeNavigatesTo or EdgeNavigatesToDynamic).
	// FETCH has no source-level library qualifier; Library is always empty
	// (FETCH's optional second operand is a parameter field, not a library-id).
	// The parser always appends FetchStatement nodes — even malformed ones — so
	// empty-target guards are required here to skip diagnostic-only entries.
	// FR-18: literal targets containing '&' are downgraded to dynamic edges.
	// Channel separation: parser emits diagnostic; no edge emitted here (FR-17/M-6).
	for _, fetch := range prog.Fetches {
		if fetch.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		edges = append(edges, model.EdgeEntry{
			Kind:       edgeKind(isStaticLiteral(fetch.TargetIsLiteral, fetch.Target), model.EdgeNavigatesTo, model.EdgeNavigatesToDynamic),
			TargetName: fetch.Target,
			Source:     stmtRange(fetch.StartPos, fetch.EndPos),
			Target:     fetch.TargetRange,
		})
	}

	// RUN: navigation edge (EdgeNavigatesTo or EdgeNavigatesToDynamic).
	// RUN carries an optional library-id as its second positional operand;
	// when present it is placed in Library on the edge.
	// If the parser encounters a same-line token that is not a valid operand,
	// it appends the node with Target == "" — skip those to avoid empty edges.
	// FR-18: literal targets containing '&' are downgraded to dynamic edges.
	// Channel separation: parser emits diagnostic; no edge emitted here (FR-17/M-6).
	for _, run := range prog.Runs {
		if run.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		edges = append(edges, model.EdgeEntry{
			Kind:       edgeKind(isStaticLiteral(run.TargetIsLiteral, run.Target), model.EdgeNavigatesTo, model.EdgeNavigatesToDynamic),
			TargetName: run.Target,
			Source:     stmtRange(run.StartPos, run.EndPos),
			Target:     run.TargetRange,
			Library:    run.Library,
		})
	}

	// Combine per-kind slices into a single globally source-ordered slice.
	// Each per-kind slice is already source-ordered (parser appends in encounter
	// order); the stable sort on (line, column) produces a deterministic total
	// order across all edge kinds and satisfies the source-order contract above.
	sort.SliceStable(edges, func(i, j int) bool {
		iLine := edges[i].Source.Start.Line
		jLine := edges[j].Source.Start.Line
		if iLine != jLine {
			return iLine < jLine
		}
		return edges[i].Source.Start.Column < edges[j].Source.Start.Column
	})

	return edges
}
