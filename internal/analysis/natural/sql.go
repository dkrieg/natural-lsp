// Package natural: extraction of embedded-SQL data access and call-like edges
// — SELECT/SELECT SINGLE/INSERT/UPDATE/DELETE for DDM reads/writes,
// CALLDBPROC call-like edges, and host-variable references in SQL clauses.
// (Feature 08b-embedded-sql-extraction, Task 2–7).
package natural

import (
	"sort"
	"strings"

	"natural-lsp/internal/model"
)

// hasNaturalSigil reports whether name begins with a Natural user-variable
// sigil (#, & for CALLNAT/global vars, + for application-independent vars, or @).
// A sigil-prefixed identifier in a native-SQL clause is a host variable, never a
// column name.
func hasNaturalSigil(name string) bool {
	if name == "" {
		return false
	}
	switch name[0] {
	case '#', '&', '+', '@':
		return true
	default:
		return false
	}
}

// appendWriteEntries appends one EdgeWrites entry per non-empty table operand
// to dst and returns the updated slice. Used by extractSQLAccess to collapse
// the identical INSERT/UPDATE/DELETE/MERGE write-entry loops.
func appendWriteEntries(dst []model.DataAccessEntry, tables []OperandRef, start, end model.Position) []model.DataAccessEntry {
	src := stmtRange(start, end)
	for _, table := range tables {
		if table.Name == "" {
			continue // malformed table clause — no false edge
		}
		dst = append(dst, model.DataAccessEntry{
			Kind:      model.EdgeWrites,
			Name:      table.Name,
			NameRange: table.Range,
			Source:    src,
		})
	}
	return dst
}

// extractSQLAccess walks the parsed program and returns data-access entries for
// every recognized native SQL statement (SELECT, SELECT SINGLE, INSERT, UPDATE,
// DELETE, MERGE, READ RESULT SET) and their table operands.
//
// For SELECT and SELECT SINGLE, emits one EdgeReads entry per FromTables operand.
// For INSERT, emits one EdgeWrites entry for IntoTable.
// For SQL UPDATE and SQL DELETE, emits one EdgeWrites entry per table operand.
// For MERGE, emits one EdgeWrites entry for the target table.
// For READ RESULT SET, emits one EdgeReads entry for its result-set operand.
//
// Returns entries in source order (stable sort on Source.Start).
// Never panics over partial/nil ASTs (FR-43).
func extractSQLAccess(prog *Program) []model.DataAccessEntry {
	if prog == nil {
		return nil
	}

	var entries []model.DataAccessEntry

	// Task 2: SELECT and SELECT SINGLE statements emit EdgeReads for each
	// FromTables operand (the DDM/table name).

	// SelectStatement (cursor loop, ES-4): emit one EdgeReads per FromTables operand.
	for _, sel := range prog.Selects {
		if sel == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range sel.FromTables {
			if table.Name == "" {
				continue // malformed FROM clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeReads,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(sel.StartPos, sel.EndPos),
			})
		}
	}

	// SelectSingleStatement (singleton, ES-3): emit one EdgeReads per FromTables operand.
	for _, selSingle := range prog.SelectSingles {
		if selSingle == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range selSingle.FromTables {
			if table.Name == "" {
				continue // malformed FROM clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeReads,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(selSingle.StartPos, selSingle.EndPos),
			})
		}
	}

	// Task 3: INSERT, SQL UPDATE, SQL DELETE, and MERGE emit EdgeWrites for each
	// table operand (the DDM/table name). appendWriteEntries handles the shared logic.

	// InsertStatement (SQL): emit one EdgeWrites for IntoTable operand.
	for _, ins := range prog.Inserts {
		if ins == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		entries = appendWriteEntries(entries, ins.IntoTable, ins.StartPos, ins.EndPos)
	}

	// SQLUpdateStatement (SQL form with SET/WHERE): emit one EdgeWrites for Table operand.
	for _, upd := range prog.SQLUpdates {
		if upd == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		entries = appendWriteEntries(entries, upd.Table, upd.StartPos, upd.EndPos)
	}

	// SQLDeleteStatement (SQL form with FROM/WHERE): emit one EdgeWrites for FromTable operand.
	for _, del := range prog.SQLDeletes {
		if del == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		entries = appendWriteEntries(entries, del.FromTable, del.StartPos, del.EndPos)
	}

	// MergeStatement (SQL): emit one EdgeWrites for the MERGE INTO target table.
	for _, merge := range prog.Merges {
		if merge == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		entries = appendWriteEntries(entries, merge.Table, merge.StartPos, merge.EndPos)
	}

	// ReadResultSetStatement (SQL): record a read-access site. The result-set
	// operand is a handle established by a preceding CALLDBPROC, not a DDM, so the
	// entry carries an empty Name (site recorded, binding deferred) — never a
	// false DDM edge on the handle. This mirrors feature 08's empty-Name
	// record-form write sites.
	for _, rrs := range prog.ReadResultSets {
		if rrs == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		entries = append(entries, model.DataAccessEntry{
			Kind:   model.EdgeReads,
			Name:   "",
			Source: stmtRange(rrs.StartPos, rrs.EndPos),
		})
	}

	// Task 7: PROCESS SQL statements emit one EdgeReads for the DDM operand
	// (neutral read-style access per OQ-3). The opaque body is never scanned
	// for table names — only the DDMName operand becomes an edge. In-body table
	// names are pass-through text (modeled gap, OQ-3).
	for _, ps := range prog.ProcessSQLs {
		if ps == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		if ps.DDMName == "" {
			continue // malformed PROCESS SQL with no DDM operand — no false edge
		}
		entries = append(entries, model.DataAccessEntry{
			Kind:      model.EdgeReads,
			Name:      ps.DDMName,
			NameRange: ps.DDMNameRange,
			Source:    stmtRange(ps.StartPos, ps.EndPos),
		})
	}

	// Sort by source order (stable sort on Source.Start).
	sort.SliceStable(entries, func(i, j int) bool {
		return sourceStartLess(
			entries[i].Source.Start.Line, entries[i].Source.Start.Column,
			entries[j].Source.Start.Line, entries[j].Source.Start.Column,
		)
	})

	return entries
}

// extractSQLCalls walks the parsed program and returns call-like edges for
// embedded-SQL statements that invoke external code. Currently that is
// CALLDBPROC (a stored-procedure call): each emits one EdgeEntry with the proc
// name as TargetName and the call site as Source, the target left unbound
// (resolution is the resolver's job). A literal proc name is a static EdgeCalls;
// a variable (or an '&'-placeholder literal) downgrades to EdgeCallsDynamic,
// consistent with feature-06 CALLNAT handling. Never panics over partial/nil
// ASTs (FR-43).
func extractSQLCalls(prog *Program) []model.EdgeEntry {
	if prog == nil {
		return nil
	}

	var edges []model.EdgeEntry
	for _, cdp := range prog.CallDBProcs {
		if cdp == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		if cdp.ProcName == "" {
			continue // malformed CALLDBPROC with no proc operand — no false edge
		}
		edges = append(edges, model.EdgeEntry{
			Kind:       edgeKind(isStaticLiteral(cdp.ProcNameIsLiteral, cdp.ProcName), model.EdgeCalls, model.EdgeCallsDynamic),
			TargetName: cdp.ProcName,
			Source:     stmtRange(cdp.StartPos, cdp.EndPos),
		})
	}

	sort.SliceStable(edges, func(i, j int) bool {
		return sourceStartLess(
			edges[i].Source.Start.Line, edges[i].Source.Start.Column,
			edges[j].Source.Start.Line, edges[j].Source.Start.Column,
		)
	})

	return edges
}

// isOpaqueQualifierByte reports whether b is one of the single-letter PROCESS SQL
// host-var qualifiers (U=USING, G=GIVING, T=TEXT), case-insensitive.
func isOpaqueQualifierByte(b byte) bool {
	b |= 0x20 // fold to lower-case
	return b == 'u' || b == 'g' || b == 't'
}

// isNaturalNameByte reports whether b is a valid Natural identifier body character
// (letter, digit, or hyphen). Sigils (#, &, +, @) are not included because they
// are only valid as the FIRST character of a name.
func isNaturalNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '-'
}

// isNaturalSigilByte reports whether b is a Natural variable sigil.
func isNaturalSigilByte(b byte) bool {
	return b == '#' || b == '&' || b == '+' || b == '@'
}

// scanOpaqueHostVars (Task 7 helper) scans the raw opaque body of a PROCESS SQL
// statement for host-variable references and returns them as HostVarRef values.
//
// The opaque body is raw text: never re-tokenized or parsed. Rules (KB-grounded):
//   - Inside <<...>> the colon is MANDATORY: only :name sequences are host vars.
//   - Strip a leading USING/GIVING/TEXT qualifier: :U:, :G:, :T: (case-insensitive)
//     e.g. :U:#NAME binds #NAME. A qualifier letter not followed by ':' is NOT
//     consumed as a qualifier — the colon is treated as a bare name-start.
//   - Recognize and strip indicator prefixes INDICATOR/LINDICATOR if present.
//   - Recognize array notation :NAME(*)/:NAME(01:10): the base name is captured and
//     the (...) subscript (including any ':' inside it) is consumed and discarded.
//     This prevents array range colons from being mistaken for host-var starts.
//   - A digit-starting sequence after ':' (e.g. ':10' inside an array range) is
//     NOT a host-var reference: Natural names must start with a sigil or a letter.
//   - Keep the Natural sigil (#/&/+/@) on the captured name; upper-case the result.
//   - Compute each ref's Range from bodyStart (the position of <<) + interior offset,
//     tracking newlines within the body for multi-line accuracy.
//
// Returns host-var refs in scan order (approximately source order within the body).
// Never panics; skips malformed constructs gracefully (FR-43).
func scanOpaqueHostVars(body string, bodyStart model.Position) []model.HostVarRef {
	var refs []model.HostVarRef

	// Position tracking. bodyStart is the position of the << delimiter itself;
	// the interior of the body starts 2 columns past it (past the '<<').
	line := bodyStart.Line
	col := bodyStart.Column + 2

	// i is declared here so that the closures below can capture it by reference.
	i := 0

	// scanName advances i/col past a Natural identifier (optional sigil + name chars)
	// and returns the raw slice. Returns "" when no valid name starts at i.
	// A digit-starting sequence is rejected: Natural names require a sigil or letter first.
	scanName := func() string {
		start := i
		if i < len(body) && isNaturalSigilByte(body[i]) {
			i++
			col++
		}
		for i < len(body) && isNaturalNameByte(body[i]) {
			i++
			col++
		}
		return body[start:i]
	}

	// skipParenSubscript consumes a '(...)' subscript (array range or wildcard),
	// tracking newlines. Called after a name is captured to swallow ':NAME(01:10)'.
	// The colons inside (...) are part of the subscript and must not trigger a
	// new host-var scan.
	skipParenSubscript := func() {
		// caller has already confirmed body[i] == '('
		i++
		col++
		for i < len(body) && body[i] != ')' {
			switch body[i] {
			case '\n':
				line++
				col = 1
				i++
			case '\r':
				line++
				col = 1
				i++
				if i < len(body) && body[i] == '\n' {
					i++
				}
			default:
				i++
				col++
			}
		}
		if i < len(body) { // consume the closing ')'
			i++
			col++
		}
	}

	for i < len(body) {
		ch := body[i]

		// Advance position tracking for line terminators first.
		switch ch {
		case '\r':
			line++
			col = 1
			i++
			if i < len(body) && body[i] == '\n' {
				i++
			}
			continue
		case '\n':
			line++
			col = 1
			i++
			continue
		}

		if ch != ':' {
			// Non-colon: advance one column regardless of tab width (consistent
			// with the rest of the Natural position-tracking code).
			col++
			i++
			continue
		}

		// ':' found — potential host-var start.
		refStartLine := line
		refStartCol := col
		i++ // consume ':'
		col++

		// Strip optional single-letter qualifier (:U:, :G:, :T:, case-insensitive).
		// Only consume the qualifier letter when it is immediately followed by ':';
		// otherwise leave the cursor at the letter so the name-scan can read it.
		if i < len(body) && isOpaqueQualifierByte(body[i]) &&
			i+1 < len(body) && body[i+1] == ':' {
			i += 2 // qualifier letter + ':'
			col += 2
		}

		// A digit immediately after ':' (or after qualifier) is not a valid Natural
		// name start — this guards colons inside array ranges like (01:10) when
		// the subscript scanner hasn't consumed them (e.g. a bare '(01:10)' in SQL text).
		if i < len(body) && body[i] >= '0' && body[i] <= '9' {
			continue // not a host-var ref; keep scanning
		}

		name := scanName()
		if name == "" {
			// ':' not followed by a sigil or letter — not a host-var; keep scanning.
			continue
		}
		name = strings.ToUpper(name)

		// INDICATOR/LINDICATOR: this token is a prefix, not a host-var name.
		// Skip whitespace, expect another ':', then scan the real name.
		if name == "INDICATOR" || name == "LINDICATOR" {
			for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
				i++
				col++
			}
			if i >= len(body) || body[i] != ':' {
				// Malformed INDICATOR prefix (no following ':name') — skip.
				continue
			}
			i++ // consume ':'
			col++
			name = scanName()
			if name == "" {
				continue
			}
			name = strings.ToUpper(name)
		}

		// Array subscript: ':NAME(*)' or ':NAME(01:10)' — consume and discard.
		// The colons inside the subscript are array-range syntax, not host-var refs.
		if i < len(body) && body[i] == '(' {
			skipParenSubscript()
		}

		refs = append(refs, model.HostVarRef{
			Name: name,
			Range: model.Range{
				Start: model.Position{Line: refStartLine, Column: refStartCol},
				End:   model.Position{Line: line, Column: col},
			},
		})
	}

	return refs
}

// extractHostVarRefs walks the parsed program and returns host-variable references
// from embedded-SQL statements (SELECT, SELECT SINGLE, INSERT, UPDATE, DELETE).
//
// For each native SQL statement, collects host-var operands from the clauses
// that carry them: IntoTargets, WhereOperands, Values, SetTargets.
//
// Normalization: the colon prefix (if present) is stripped, Natural sigils
// (#, &, @, +) are preserved, and names are upper-cased (matching feature-08's
// DataDefinition.Name convention).
//
// Returns entries in source order.
// Never panics over partial/nil ASTs (FR-43).
//
// The parser already strips the leading colon and upper-cases native host-var
// names, so operand names are emitted verbatim; a column list (SELECT Columns)
// and table operands are DDM edges, not host-var references, and are excluded.
func extractHostVarRefs(prog *Program) []model.HostVarRef {
	if prog == nil {
		return nil
	}

	var refs []model.HostVarRef

	// addAll emits every operand as a host-var reference. Used for clauses whose
	// operands are host variables by definition (a SELECT INTO target list).
	addAll := func(ops []OperandRef) {
		for _, op := range ops {
			if op.Name == "" {
				continue // modeled gap: no false reference from an empty operand
			}
			refs = append(refs, model.HostVarRef{Name: op.Name, Range: op.Range})
		}
	}

	// addHostVars emits only the operands that are host variables. Used for
	// clauses (WHERE, VALUES, SET) that interleave host vars with column names,
	// operators, and literals: a host var is marked either by a leading colon
	// (op.HostVar) or by a Natural sigil in its name (#/&/+/@).
	addHostVars := func(ops []OperandRef) {
		for _, op := range ops {
			if op.Name == "" {
				continue
			}
			if op.HostVar || hasNaturalSigil(op.Name) {
				refs = append(refs, model.HostVarRef{Name: op.Name, Range: op.Range})
			}
		}
	}

	for _, sel := range prog.Selects {
		if sel == nil {
			continue
		}
		addAll(sel.IntoTargets)
		addHostVars(sel.WhereOperands)
	}
	for _, selSingle := range prog.SelectSingles {
		if selSingle == nil {
			continue
		}
		addAll(selSingle.IntoTargets)
		addHostVars(selSingle.WhereOperands)
	}
	for _, ins := range prog.Inserts {
		if ins == nil {
			continue
		}
		addHostVars(ins.Values)
	}
	for _, upd := range prog.SQLUpdates {
		if upd == nil {
			continue
		}
		addHostVars(upd.SetTargets)
		addHostVars(upd.WhereOperands)
	}
	for _, del := range prog.SQLDeletes {
		if del == nil {
			continue
		}
		addHostVars(del.WhereOperands)
	}

	// Task 7: Scan opaque body of PROCESS SQL statements for colon-mandatory
	// host-var references. The body is raw text (never re-tokenized): only
	// :name sequences are host vars; table names, SQL keywords, and other
	// pass-through text are ignored (modeled gap, OQ-3).
	for _, ps := range prog.ProcessSQLs {
		if ps == nil {
			continue
		}
		if ps.Body == "" {
			continue // empty opaque body; no host vars to scan
		}
		// scanOpaqueHostVars returns refs in scan order; the final sort below
		// will order them with native refs by source position.
		opaqueRefs := scanOpaqueHostVars(ps.Body, ps.BodyRange.Start)
		refs = append(refs, opaqueRefs...)
	}

	sort.SliceStable(refs, func(i, j int) bool {
		return sourceStartLess(
			refs[i].Range.Start.Line, refs[i].Range.Start.Column,
			refs[j].Range.Start.Line, refs[j].Range.Start.Column,
		)
	})

	return refs
}
