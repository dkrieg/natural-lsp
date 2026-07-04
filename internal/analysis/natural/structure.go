// Package natural: extraction of program structure — objects, subroutines,
// data sections, fields, maps, and DDM references into a hierarchical tree
// (feature 09, T2).
package natural

import (
	"path/filepath"
	"sort"

	"natural-lsp/internal/model"
)

// extractStructure builds a hierarchical symbol tree from the parsed program.
// It returns a Symbol rooted at the object level, with children for data sections,
// subroutines, maps, and DDM references, all in stable source order.
//
// path is the file path (used to derive the object name).
// prog is the parsed AST (nullable — returns nil gracefully).
// defs are the already-extracted DataDefinitions (for field children; used in T3).
//
// Never panics over partial ASTs (FR-43).
func extractStructure(path string, prog *Program, defs []model.DataDefinition) *model.Symbol {
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
	// We'll group defs by their SectionKind and range containment.
	sectionToDefsMap := make(map[int][]model.DataDefinition)
	for i := range prog.DataSections {
		sectionToDefsMap[i] = []model.DataDefinition{}
	}

	// Distribute defs to sections by matching their section kind.
	for _, def := range defs {
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
			Name: section.Kind, // Use the section kind verbatim (e.g., "local", "parameter")
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
			SelectionRange: model.Range{
				Start: sub.StartPos,
				End:   sub.StartPos,
			},
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

	// Sort children by source order (Range.Start).
	sort.Slice(childrenToSort, func(i, j int) bool {
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
func dataDefinitionToSymbol(def model.DataDefinition) []model.Symbol {
	var symbols []model.Symbol

	// Create the field symbol itself.
	sym := model.Symbol{
		Kind: model.SymbolDataField,
		Name: def.Name,
		Range: model.Range{
			Start: def.Range.Start,
			End:   def.Range.End,
		},
		SelectionRange: model.Range{
			Start: def.Range.Start,
			End:   def.Range.Start, // non-zero point at the start
		},
		Children: make([]model.Symbol, 0),
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
