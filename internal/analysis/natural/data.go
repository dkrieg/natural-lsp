// Package natural: extraction of data access and data definitions — READ/FIND/
// GET (reads), STORE/UPDATE/DELETE (writes), DEFINE DATA sections and parameter
// interfaces, and DEFINE WORK FILE (PRD FR-19..22).
package natural

import (
	"sort"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// extractDataAccess walks the parsed program and returns data-access entries
// for every recognized Adabas data-access statement (READ, FIND, GET, STORE,
// record UPDATE, record DELETE).
//
// Name-bearing statements (READ/FIND/GET/STORE) produce an entry whose Name is
// the accessed view/DDM name (normalized to upper-case by the lexer) and whose
// NameRange spans just that token.
//
// Record-form writes (UPDATE/DELETE inside a READ/FIND/GET loop) have no file
// operand in the source; they produce an entry with empty Name and zero NameRange
// but a non-zero Source. The Source records that a write occurred at this site for
// impact analysis; file binding (matching the enclosing loop's DDM) is deferred to
// feature 08b-embedded-sql-extraction (OQ-4).
//
// Returns entries in global source order (stable sort on Source.Start, mirroring
// extractEdges in calls.go). Never panics over partial ASTs.
func extractDataAccess(prog *Program) []model.DataAccessEntry {
	if prog == nil {
		return nil
	}

	var entries []model.DataAccessEntry

	// appendNamedEntry adds one DataAccessEntry for a name-bearing statement
	// (READ, FIND, GET, STORE). Callers must skip empty targets before calling —
	// an empty target means the statement is malformed and the parser has already
	// emitted a diagnostic; no partial entry is produced for those.
	appendNamedEntry := func(name string, nameRange model.Range, start, end model.Position, kind model.EdgeKind) {
		entries = append(entries, model.DataAccessEntry{
			Kind:      kind,
			Name:      name,
			Source:    stmtRange(start, end),
			NameRange: nameRange,
		})
	}

	// appendUnboundWrite adds one EdgeWrites entry for a record-form write
	// (Adabas record UPDATE or DELETE). These statements carry no file operand —
	// the target is implicitly the record from the enclosing READ/FIND/GET loop.
	// Name is intentionally empty; NameRange is zero (no name token exists).
	// This is distinct from the empty-target skip in appendNamedEntry: here the
	// empty Name is correct-by-design, not a sign of a malformed statement (OQ-4).
	appendUnboundWrite := func(start, end model.Position) {
		entries = append(entries, model.DataAccessEntry{
			Kind:   model.EdgeWrites,
			Name:   "", // no file operand — file binding deferred to feature 08b
			Source: stmtRange(start, end),
			// NameRange is zero: there is no name token to point at.
		})
	}

	// READ statements → EdgeReads (sequential / ISN-order read of a view).
	for _, read := range prog.Reads {
		if read.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		appendNamedEntry(read.Target, read.TargetRange, read.StartPos, read.EndPos, model.EdgeReads)
	}

	// STORE statements → EdgeWrites (insert a new record into the view).
	for _, store := range prog.Stores {
		if store.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		appendNamedEntry(store.Target, store.TargetRange, store.StartPos, store.EndPos, model.EdgeWrites)
	}

	// FIND statements → EdgeReads (Task 4 / FR-19): cursor-based read, treated
	// identically to READ at the edge level.
	for _, find := range prog.Finds {
		if find.Target == "" {
			continue // malformed — diagnostic already emitted by parser
		}
		appendNamedEntry(find.Target, find.TargetRange, find.StartPos, find.EndPos, model.EdgeReads)
	}

	// GET statements → EdgeReads (Task 6 / FR-19): fetch record by ISN, treated
	// identically to READ at the edge level. GET SAME (empty Target) is skipped.
	for _, get := range prog.Gets {
		if get.Target == "" {
			continue // GET SAME or malformed — no resolvable file/DDM name
		}
		appendNamedEntry(get.Target, get.TargetRange, get.StartPos, get.EndPos, model.EdgeReads)
	}

	// Record UPDATE statements → EdgeWrites (Task 8 / FR-20). Adabas record
	// UPDATE has no file operand; the target is the record from the enclosing
	// READ/FIND/GET loop. Use appendUnboundWrite (not appendNamedEntry) because
	// the empty Name here is correct-by-design, not a malformed-statement signal.
	for _, update := range prog.RecordUpdates {
		appendUnboundWrite(update.StartPos, update.EndPos)
	}

	// Record DELETE statements → EdgeWrites (Task 8 / FR-20). Same design as
	// record UPDATE: no file operand, file binding deferred to feature 08b.
	for _, del := range prog.RecordDeletes {
		appendUnboundWrite(del.StartPos, del.EndPos)
	}

	// Combine per-kind slices into a single globally source-ordered slice.
	// Each per-kind slice is already source-ordered (parser appends in encounter
	// order); the stable sort on (line, column) produces a deterministic total
	// order across all data-access kinds and satisfies the source-order contract.
	sort.SliceStable(entries, func(i, j int) bool {
		return sourceStartLess(
			entries[i].Source.Start.Line, entries[i].Source.Start.Column,
			entries[j].Source.Start.Line, entries[j].Source.Start.Column,
		)
	})

	return entries
}

// convertArrayBounds maps AST ArrayBound slice to model.ArrayDimension slice.
// Returns nil for an empty input — callers can rely on nil meaning "scalar".
func convertArrayBounds(bounds []ArrayBound) []model.ArrayDimension {
	if len(bounds) == 0 {
		return nil
	}
	dims := make([]model.ArrayDimension, len(bounds))
	for i, b := range bounds {
		dims[i] = model.ArrayDimension{
			Lower:          b.Lower,
			Upper:          b.Upper,
			UpperUnbounded: b.UpperUnbounded,
		}
	}
	return dims
}

// fieldToDefinition converts a single AST DataField into a model.DataDefinition.
// sectionKind is propagated only at the top level; children inherit a blank kind
// (callers descend only one level — the section-level kind is not repeated on subfields).
// redefines is set on the output definition when the field is a redefine subfield
// (i.e., merged from a REDEFINE block). For non-redefine children, pass "".
// Recursively converts child fields. Never panics on nil children slices.
//
// REDEFINE handling (Feature 28, T4): When a child is a REDEFINE block (Name="" && Redefines!=""),
// its sub-fields are recursively merged into the target field's Children (not emitted as a
// separate definition). This handles REDEFINE blocks at any nesting level, not just top-level.
func fieldToDefinition(field *DataField, sectionKind, redefines string) model.DataDefinition {
	def := model.DataDefinition{
		Name:        field.Name,
		Level:       field.Level,
		Type:        field.Type,
		Dimensions:  convertArrayBounds(field.Dimensions),
		SectionKind: sectionKind,
		Range:       model.Range{Start: field.StartPos, End: field.EndPos},
		NameRange:   field.NameRange,
		Redefines:   redefines,
		ViewOfDDM:   field.ViewOfDDM,
	}
	if len(field.Children) > 0 {
		def.Children = make([]model.DataDefinition, 0, len(field.Children))
		for _, child := range field.Children {
			if child == nil {
				continue // graceful degradation: skip malformed AST child nodes
			}

			// REDEFINE block handling (Feature 28, T4): a child with empty Name and
			// non-empty Redefines is a redefine block — its sub-fields are merged into
			// the parent (target field) instead of being emitted as a separate definition.
			// Find the target field in the current definition's Children and merge there.
			if child.Name == "" && child.Redefines != "" {
				// Scan the already-built Children for the target field
				targetIdx := -1
				for i := range def.Children {
					if def.Children[i].Name == child.Redefines {
						targetIdx = i
						break
					}
				}

				if targetIdx >= 0 {
					// Found the target; merge REDEFINE sub-fields into it
					for _, redefSubChild := range child.Children {
						if redefSubChild == nil {
							continue
						}
						// Recursively convert the REDEFINE sub-field with Redefines set
						subDef := fieldToDefinition(redefSubChild, "", child.Redefines)
						def.Children[targetIdx].Children = append(def.Children[targetIdx].Children, subDef)
					}
				}
				// No entry emitted for the REDEFINE block itself (continue to next child)
				continue
			}

			// Children do not carry a SectionKind — the section kind is a
			// top-level property and is not repeated on nested subfields.
			// Children also do not inherit parent's Redefines — each field
			// carries its own redefines relationship.
			def.Children = append(def.Children, fieldToDefinition(child, "", ""))
		}
	}
	return def
}

// extractWorkFiles walks the parsed program and returns work-file definitions
// for every DEFINE WORK FILE statement encountered.
//
// Number is the work-file slot number from the source. Name is the file name as it
// appears in source (quoted string with quotes stripped, or variable verbatim with sigil).
// Range is the source span of the entire DEFINE WORK FILE statement.
//
// A variable/dynamic work-file name (starting with '#') is recorded verbatim — a modeled
// gap (not a diagnostic) — allowing callers to distinguish dynamic from static file names.
//
// Returns entries in source order (stable sort on Source.Start). Never panics over partial ASTs.
func extractWorkFiles(prog *Program) []model.WorkFile {
	if prog == nil {
		return nil
	}

	var workFiles []model.WorkFile

	for _, wf := range prog.WorkFiles {
		if wf == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		workFiles = append(workFiles, model.WorkFile{
			Number: wf.Number,
			Name:   wf.Name,
			Range:  stmtRange(wf.StartPos, wf.EndPos),
		})
	}

	// Sort by source order (stable sort on Range.Start).
	sort.SliceStable(workFiles, func(i, j int) bool {
		return sourceStartLess(
			workFiles[i].Range.Start.Line, workFiles[i].Range.Start.Column,
			workFiles[j].Range.Start.Line, workFiles[j].Range.Start.Column,
		)
	})

	return workFiles
}

// extractDefinitions walks the parsed program and returns data definitions
// for every declared variable in every DEFINE DATA section (LOCAL, PARAMETER,
// GLOBAL, LINKAGE).
//
// Each data field (scalar, group, or redefine block) produces a DataDefinition
// with its name, level, type, array dimensions, section kind, and source range.
// Group and redefine children are nested in the Children slice while preserving
// declaration order and level hierarchy.
//
// REDEFINE handling: a DataField with an empty Name and a non-empty Redefines
// target is a redefine block — its children are merged into the target field's
// Children slice; no separate top-level definition is emitted for the block itself.
//
// The section kind (local/parameter/global/linkage) is captured to allow callers
// (especially hover/signature builders) to distinguish parameter interfaces from
// other data sections.
//
// Returns definitions in declaration order (per-section and per-field).
// Never panics over nil programs, nil sections, or nil/malformed field nodes.
func extractDefinitions(prog *Program) []model.DataDefinition {
	if prog == nil {
		return nil
	}

	var defs []model.DataDefinition

	for _, section := range prog.DataSections {
		if section == nil {
			continue
		}
		// Skip sections that have no inline fields — an empty section contributes
		// nothing (e.g. a "GLOBAL USING <name>" reference whose fields live in
		// an external GDA file, or any other section the parser left empty).
		if len(section.Fields) == 0 {
			continue
		}

		// fieldIdx maps a top-level field name to its position in defs so that a
		// subsequent REDEFINE block can locate and extend the target field's Children.
		// The map is scoped to the current section: REDEFINE can only target a field
		// declared in the same section (Natural language rule).
		fieldIdx := make(map[string]int)

		for _, field := range section.Fields {
			if field == nil {
				continue // graceful degradation: skip nil AST field nodes
			}

			// A REDEFINE block has an empty Name and a non-empty Redefines pointer.
			// Its children are merged into the target field rather than emitting a
			// standalone definition — the block itself is not a separately addressable
			// variable; it is a layout overlay on an existing one.
			if field.Name == "" && field.Redefines != "" {
				if idx, ok := fieldIdx[field.Redefines]; ok {
					// Append redefine subfields to the target field's Children.
					// Each child is converted with Redefines set to the target name
					// (the AST field.Redefines, normalized by the lexer).
					for _, child := range field.Children {
						if child == nil {
							continue
						}
						// Convert the child with its Redefines relationship stamped
						childDef := fieldToDefinition(child, "", field.Redefines)
						defs[idx].Children = append(defs[idx].Children, childDef)
					}
				}
				// No entry emitted for the REDEFINE block itself.
				continue
			}

			fieldIdx[field.Name] = len(defs)
			defs = append(defs, fieldToDefinition(field, section.Kind, ""))
		}
	}

	return defs
}

// extractDataAreaRefs walks the parsed program and returns data-area references
// from USING clauses in DEFINE DATA sections.
//
// Each USING <name> clause in a LOCAL/PARAMETER/GLOBAL section produces a
// DataAreaRef capturing the external data-area name, the section kind, and the
// source range of the name token. The USING reference is used for cross-file
// field resolution (feature 27, T7).
//
// Returns references in encounter order (sections appear in the order they are
// declared). Never panics over partial ASTs.
func extractDataAreaRefs(prog *Program) []model.DataAreaRef {
	if prog == nil {
		return nil
	}

	var refs []model.DataAreaRef

	for _, section := range prog.DataSections {
		if section == nil {
			continue
		}
		// Only emit a ref if the section has a non-empty USING clause.
		if section.Using == "" {
			continue
		}

		refs = append(refs, model.DataAreaRef{
			Name:        section.Using,
			SectionKind: section.Kind,
			Range:       section.UsingRange,
		})
	}

	return refs
}
