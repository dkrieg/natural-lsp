// Package natural: extraction of program structure — objects, subroutines,
// data sections, fields, maps, and DDM references into a hierarchical tree
// (feature 09, T2).
package natural

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// extractStructure builds a hierarchical symbol tree from the parsed program.
// It returns a Symbol rooted at the object level, with children for data sections,
// subroutines, maps, and DDM references, all in stable source order.
//
// path is the file path (used to derive the object name).
// prog is the parsed AST (nullable — returns nil gracefully).
// defs are the already-extracted DataDefinitions (for field children; used in T3).
// access are the data-access entries for DDM references (feature 08/08b).
//
// Never panics over partial ASTs (FR-43).
func extractStructure(path string, prog *Program, defs []model.DataDefinition, access []model.DataAccessEntry) *model.Symbol {
	if prog == nil {
		return nil
	}

	// Derive the object name from the file path (remove extension).
	fileName := filepath.Base(path)
	objectName := fileName[:len(fileName)-len(filepath.Ext(fileName))]

	// Create root symbol for the object.
	root := &model.Symbol{
		Kind: model.SymbolObject,
		Name: objectName,
		Range: model.Range{
			Start: prog.StartPos,
			End:   prog.EndPos,
		},
		SelectionRange: model.Range{
			Start: prog.StartPos,
			End:   prog.StartPos, // non-zero point at the start
		},
		Children: make([]model.Symbol, 0),
	}

	// Build a map of which definitions belong to which data section.
	// Match by RANGE CONTAINMENT (the def's start position falls within the
	// section's span), not by section-kind string: two sections can share a
	// kind (e.g. two DEFINE DATA LOCAL blocks), and a kind-string match would
	// bucket every same-kind field into the first section. A kind-string match
	// is kept only as a fallback so a field is never dropped if a parser range
	// is imprecise.
	sectionToDefsMap := make(map[int][]model.DataDefinition)
	for i := range prog.DataSections {
		sectionToDefsMap[i] = []model.DataDefinition{}
	}

	for _, def := range defs {
		start := def.Range.Start
		assigned := false
		// Primary: the section whose [StartPos, EndPos] span contains the def.
		for i, section := range prog.DataSections {
			if section == nil {
				continue
			}
			withinStart := !sourceStartLess(start.Line, start.Column, section.StartPos.Line, section.StartPos.Column)
			withinEnd := !sourceStartLess(section.EndPos.Line, section.EndPos.Column, start.Line, start.Column)
			if withinStart && withinEnd {
				sectionToDefsMap[i] = append(sectionToDefsMap[i], def)
				assigned = true
				break
			}
		}
		if assigned {
			continue
		}
		// Fallback: first section of the matching kind (defensive — should not
		// happen when the parser's section ranges are accurate).
		for i, section := range prog.DataSections {
			if section != nil && def.SectionKind == section.Kind {
				sectionToDefsMap[i] = append(sectionToDefsMap[i], def)
				break
			}
		}
	}

	// Collect all children that will be added to the root.
	var childrenToSort []model.Symbol

	// Add data sections as symbols with field children.
	for i, section := range prog.DataSections {
		if section == nil {
			continue
		}

		sectionSym := model.Symbol{
			Kind: model.SymbolDataSection,
			Name: strings.ToUpper(section.Kind), // Uppercase to match model's NAME CONVENTION (e.g., "LOCAL", "PARAMETER")
			Range: model.Range{
				Start: section.StartPos,
				End:   section.EndPos,
			},
			SelectionRange: model.Range{
				Start: section.StartPos,
				End:   section.StartPos,
			},
			Children: make([]model.Symbol, 0),
		}

		// Convert the definitions for this section into SymbolDataField children.
		for _, def := range sectionToDefsMap[i] {
			sectionSym.Children = append(sectionSym.Children,
				dataDefinitionToSymbol(def)...)
		}

		childrenToSort = append(childrenToSort, sectionSym)
	}

	// Add subroutines as symbols.
	for _, sub := range prog.Subroutines {
		if sub == nil {
			continue
		}

		subSym := model.Symbol{
			Kind: model.SymbolSubroutine,
			Name: sub.Name,
			Range: model.Range{
				Start: sub.StartPos,
				End:   sub.EndPos,
			},
			SelectionRange: sub.NameRange, // Use the captured name range (feature 18, T6a)
		}
		childrenToSort = append(childrenToSort, subSym)
	}

	// Add maps as symbols with field children.
	for _, mapDef := range prog.Maps {
		if mapDef == nil {
			continue
		}

		mapSym := model.Symbol{
			Kind: model.SymbolMap,
			Name: mapDef.Name,
			Range: model.Range{
				Start: mapDef.StartPos,
				End:   mapDef.EndPos,
			},
			SelectionRange: model.Range{
				Start: mapDef.StartPos,
				End:   mapDef.StartPos,
			},
			Children: make([]model.Symbol, 0),
		}

		// Convert map fields to SymbolDataField children.
		// Map fields are raw AST DataField nodes, not DataDefinitions.
		for _, field := range mapDef.Fields {
			if field != nil {
				mapSym.Children = append(mapSym.Children,
					dataFieldToSymbol(field)...)
			}
		}

		childrenToSort = append(childrenToSort, mapSym)
	}

	// Add DDM references as symbols (T3: feature 09).
	// Skip entries with empty Name (feature-08 modeled gap: record-form UPDATE/DELETE, OQ-4).
	for _, entry := range access {
		if entry.Name == "" {
			continue
		}

		// The whole-construct Range must contain the SelectionRange (LSP
		// DocumentSymbol invariant). entry.Source spans only the access verb
		// (e.g. READ) and ends before the name token, so widen its end to cover
		// the name range.
		rangeEnd := entry.Source.End
		if sourceStartLess(rangeEnd.Line, rangeEnd.Column, entry.NameRange.End.Line, entry.NameRange.End.Column) {
			rangeEnd = entry.NameRange.End
		}
		ddmSym := model.Symbol{
			Kind: model.SymbolDDMReference,
			Name: entry.Name,
			Range: model.Range{
				Start: entry.Source.Start,
				End:   rangeEnd,
			},
			SelectionRange: model.Range{
				Start: entry.NameRange.Start,
				End:   entry.NameRange.End,
			},
		}

		childrenToSort = append(childrenToSort, ddmSym)
	}

	// Sort children by source order (Range.Start). Stable, to match the sibling
	// extractors (extractDataAccess/extractWorkFiles) and honor the documented
	// deterministic-ordering guarantee when two children share a start position.
	sort.SliceStable(childrenToSort, func(i, j int) bool {
		iLine := childrenToSort[i].Range.Start.Line
		iCol := childrenToSort[i].Range.Start.Column
		jLine := childrenToSort[j].Range.Start.Line
		jCol := childrenToSort[j].Range.Start.Column
		return sourceStartLess(iLine, iCol, jLine, jCol)
	})

	root.Children = childrenToSort

	return root
}

// dataDefinitionToSymbol converts a DataDefinition (and its children) into one or more
// Symbol entries. Returns a slice of SymbolDataField symbols that can be appended to a
// parent's Children slice.
//
// Feature 28 T2: copies Type, Level, and Dimensions metadata from the DataDefinition
// onto the emitted Symbol for use in the typed outline (DocumentSymbol.Detail).
func dataDefinitionToSymbol(def model.DataDefinition) []model.Symbol {
	var symbols []model.Symbol

	// Create the field symbol itself.
	// SelectionRange should point at the name token (feature 27 T1).
	// Use NameRange if available, fall back to Range.Start for graceful degradation (FR-43).
	selectionRange := def.NameRange
	if selectionRange.Start.Line == 0 && selectionRange.Start.Column == 0 &&
		selectionRange.End.Line == 0 && selectionRange.End.Column == 0 {
		// NameRange is zero; fall back to the range start for backward compatibility
		selectionRange = model.Range{
			Start: def.Range.Start,
			End:   def.Range.Start,
		}
	}

	sym := model.Symbol{
		Kind: model.SymbolDataField,
		Name: def.Name,
		Range: model.Range{
			Start: def.Range.Start,
			End:   def.Range.End,
		},
		SelectionRange: selectionRange,
		Children:       make([]model.Symbol, 0),
		// Feature 28 T2: carry metadata for typed outline
		Type:       def.Type,
		Level:      def.Level,
		Dimensions: def.Dimensions,
		// Feature 28 T4: carry REDEFINE target for outline detail
		Redefines: def.Redefines,
		// Feature 28 T6: carry VIEW OF target for outline detail
		ViewOfDDM: def.ViewOfDDM,
	}

	// Recursively convert child definitions into SymbolDataField symbols.
	for _, childDef := range def.Children {
		sym.Children = append(sym.Children, dataDefinitionToSymbol(childDef)...)
	}

	symbols = append(symbols, sym)
	return symbols
}

// dataFieldToSymbol converts a DataField (and its children) into one or more
// Symbol entries for map fields. Returns a slice of SymbolDataField symbols.
func dataFieldToSymbol(field *DataField) []model.Symbol {
	var symbols []model.Symbol

	// Create the field symbol itself.
	sym := model.Symbol{
		Kind: model.SymbolDataField,
		Name: field.Name,
		Range: model.Range{
			Start: field.StartPos,
			End:   field.EndPos,
		},
		SelectionRange: model.Range{
			Start: field.StartPos,
			End:   field.StartPos,
		},
		Children: make([]model.Symbol, 0),
	}

	// Recursively convert child fields.
	for _, childField := range field.Children {
		if childField != nil {
			sym.Children = append(sym.Children, dataFieldToSymbol(childField)...)
		}
	}

	symbols = append(symbols, sym)
	return symbols
}
