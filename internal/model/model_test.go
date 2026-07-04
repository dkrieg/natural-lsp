package model

import (
	"testing"
)

func TestObjectTypeConstants(t *testing.T) {
	// FR-7: Assert exact string values of ObjectType constants
	// to guard against accidental renames and maintain stable serialization.
	tests := []struct {
		name     string
		typ      ObjectType
		expected string
	}{
		{"ObjectProgram", ObjectProgram, "program"},
		{"ObjectSubprogram", ObjectSubprogram, "subprogram"},
		{"ObjectExternalSubroutine", ObjectExternalSubroutine, "externalsubroutine"},
		{"ObjectCopycode", ObjectCopycode, "copycode"},
		{"ObjectMap", ObjectMap, "map"},
		{"ObjectLocalDataArea", ObjectLocalDataArea, "localdataarea"},
		{"ObjectGlobalDataArea", ObjectGlobalDataArea, "globaldataarea"},
		{"ObjectParameterDataArea", ObjectParameterDataArea, "parameterdataarea"},
		{"ObjectHelproutine", ObjectHelproutine, "helproutine"},
		{"ObjectDDM", ObjectDDM, "ddm"},
		{"ObjectClass", ObjectClass, "class"},
		{"ObjectFunction", ObjectFunction, "function"},
		{"ObjectDialog", ObjectDialog, "dialog"},
		{"ObjectAdapter", ObjectAdapter, "adapter"},
		{"ObjectText", ObjectText, "text"},
		{"ObjectUnknown", ObjectUnknown, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.typ != ObjectType(tc.expected) {
				t.Errorf("ObjectType constant %s = %q, want %q", tc.name, tc.typ, tc.expected)
			}
		})
	}
}

func TestDiagnosticSeverityConstants(t *testing.T) {
	// Assert exact string values of DiagnosticSeverity constants
	// to guard against accidental renames.
	tests := []struct {
		name     string
		sev      DiagnosticSeverity
		expected string
	}{
		{"DiagnosticInfo", DiagnosticInfo, "info"},
		{"DiagnosticWarning", DiagnosticWarning, "warning"},
		{"DiagnosticError", DiagnosticError, "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sev != DiagnosticSeverity(tc.expected) {
				t.Errorf("DiagnosticSeverity constant %s = %q, want %q", tc.name, tc.sev, tc.expected)
			}
		})
	}
}

func TestFileAnalysisObjectTypeAndDiagnostics(t *testing.T) {
	// Assert that FileAnalysis can hold ObjectType and Diagnostics fields
	// and round-trip values correctly. Regression test for the contract.
	fa := FileAnalysis{
		ObjectType: ObjectProgram,
		Diagnostics: []Diagnostic{
			{
				Message:  "test message",
				Severity: DiagnosticInfo,
			},
		},
	}

	if fa.ObjectType != ObjectProgram {
		t.Errorf("FileAnalysis.ObjectType = %v, want %v", fa.ObjectType, ObjectProgram)
	}
	if len(fa.Diagnostics) != 1 {
		t.Errorf("FileAnalysis.Diagnostics length = %d, want 1", len(fa.Diagnostics))
	}
	if fa.Diagnostics[0].Message != "test message" {
		t.Errorf("Diagnostic.Message = %q, want %q", fa.Diagnostics[0].Message, "test message")
	}
	if fa.Diagnostics[0].Severity != DiagnosticInfo {
		t.Errorf("Diagnostic.Severity = %v, want %v", fa.Diagnostics[0].Severity, DiagnosticInfo)
	}
}

// TestFileAnalysisSymbolEdgesDataAccessFields verifies that FileAnalysis
// supports the Symbols, Edges, and DataAccess fields required for the
// workspace index and future LSP handlers (FR-10, FR-19, FR-23).
//
// The test asserts:
//   - FileAnalysis has Symbols, Edges, and DataAccess fields
//   - These fields can be populated with appropriate types
//   - When not explicitly set, the fields are nil/empty
func TestFileAnalysisSymbolEdgesDataAccessFields(t *testing.T) {
	tests := []struct {
		name string
		// Initialize creates a FileAnalysis with the given configuration.
		initialize func() FileAnalysis
		// verify runs assertions on the initialized FileAnalysis.
		verify func(t *testing.T, fa FileAnalysis)
	}{
		{
			name: "Symbols_field_can_be_populated_with_symbol_entries",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					Symbols: []SymbolEntry{
						{
							Name:  "MYPROGRAM",
							Kind:  SymbolProgram,
							Range: Range{Start: Position{Line: 1}, End: Position{Line: 1}},
						},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Symbols == nil {
					t.Fatal("FileAnalysis.Symbols is nil, want non-nil slice")
				}
				if len(fa.Symbols) != 1 {
					t.Errorf("FileAnalysis.Symbols length = %d, want 1", len(fa.Symbols))
				}
				if fa.Symbols[0].Name != "MYPROGRAM" {
					t.Errorf("SymbolEntry.Name = %q, want %q", fa.Symbols[0].Name, "MYPROGRAM")
				}
				if fa.Symbols[0].Kind != SymbolProgram {
					t.Errorf("SymbolEntry.Kind = %q, want %q", fa.Symbols[0].Kind, SymbolProgram)
				}
			},
		},
		{
			name: "Edges_field_can_be_populated_with_relationship_entries",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					Edges: []EdgeEntry{
						{
							Source:     Range{Start: Position{Line: 10}, End: Position{Line: 10}},
							Target:     Range{Start: Position{Line: 20}, End: Position{Line: 20}},
							Kind:       EdgeCalls,
							TargetName: "CALLTARGET",
						},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Edges == nil {
					t.Fatal("FileAnalysis.Edges is nil, want non-nil slice")
				}
				if len(fa.Edges) != 1 {
					t.Errorf("FileAnalysis.Edges length = %d, want 1", len(fa.Edges))
				}
				if fa.Edges[0].Kind != EdgeCalls {
					t.Errorf("EdgeEntry.Kind = %q, want %q", fa.Edges[0].Kind, EdgeCalls)
				}
				if fa.Edges[0].TargetName != "CALLTARGET" {
					t.Errorf("EdgeEntry.TargetName = %q, want %q", fa.Edges[0].TargetName, "CALLTARGET")
				}
			},
		},
		{
			name: "DataAccess_field_can_be_populated_with_dataaccess_entries",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					DataAccess: []DataAccessEntry{
						{
							Name:   "MYFILE",
							Kind:   EdgeReads,
							Source: Range{Start: Position{Line: 15}, End: Position{Line: 15}},
						},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.DataAccess == nil {
					t.Fatal("FileAnalysis.DataAccess is nil, want non-nil slice")
				}
				if len(fa.DataAccess) != 1 {
					t.Errorf("FileAnalysis.DataAccess length = %d, want 1", len(fa.DataAccess))
				}
				if fa.DataAccess[0].Name != "MYFILE" {
					t.Errorf("DataAccessEntry.Name = %q, want %q", fa.DataAccess[0].Name, "MYFILE")
				}
				if fa.DataAccess[0].Kind != EdgeReads {
					t.Errorf("DataAccessEntry.Kind = %q, want %q", fa.DataAccess[0].Kind, EdgeReads)
				}
			},
		},
		{
			name: "Symbols_field_is_nil_when_not_set",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Symbols != nil {
					t.Errorf("FileAnalysis.Symbols = %v, want nil", fa.Symbols)
				}
			},
		},
		{
			name: "Edges_field_is_nil_when_not_set",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Edges != nil {
					t.Errorf("FileAnalysis.Edges = %v, want nil", fa.Edges)
				}
			},
		},
		{
			name: "DataAccess_field_is_nil_when_not_set",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.DataAccess != nil {
					t.Errorf("FileAnalysis.DataAccess = %v, want nil", fa.DataAccess)
				}
			},
		},
		{
			name: "All_three_fields_can_be_populated_together",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
					Symbols: []SymbolEntry{
						{Name: "PROG1", Kind: SymbolProgram},
					},
					Edges: []EdgeEntry{
						{TargetName: "CALLED", Kind: EdgeCalls},
					},
					DataAccess: []DataAccessEntry{
						{Name: "DATAFILE", Kind: EdgeReads},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if len(fa.Symbols) != 1 {
					t.Errorf("FileAnalysis.Symbols length = %d, want 1", len(fa.Symbols))
				}
				if len(fa.Edges) != 1 {
					t.Errorf("FileAnalysis.Edges length = %d, want 1", len(fa.Edges))
				}
				if len(fa.DataAccess) != 1 {
					t.Errorf("FileAnalysis.DataAccess length = %d, want 1", len(fa.DataAccess))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fa := tc.initialize()
			tc.verify(t, fa)
		})
	}
}

// TestFileAnalysisHostVarRefsField verifies that FileAnalysis has a HostVarRefs field
// and that HostVarRef{Name, Range} can be constructed and stored.
// This tests FR-21 (host-variable references) — Task 1 of feature 08b-embedded-sql-extraction.
//
// The test asserts:
//   - FileAnalysis has a HostVarRefs field of type []HostVarRef
//   - HostVarRef can be constructed with Name and Range fields
//   - HostVarRefs can be populated and round-trip correctly
func TestFileAnalysisHostVarRefsField(t *testing.T) {
	tests := []struct {
		name       string
		initialize func() FileAnalysis
		verify     func(t *testing.T, fa FileAnalysis)
	}{
		{
			name: "HostVarRefs_field_can_be_populated_with_hostvars",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
					HostVarRefs: []HostVarRef{
						{
							Name: "#EMPLOYEE",
							Range: Range{
								Start: Position{Line: 10, Column: 5},
								End:   Position{Line: 10, Column: 14},
							},
						},
						{
							Name: "#SALARY",
							Range: Range{
								Start: Position{Line: 12, Column: 8},
								End:   Position{Line: 12, Column: 15},
							},
						},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.HostVarRefs == nil {
					t.Fatal("FileAnalysis.HostVarRefs is nil, want non-nil slice")
				}
				if len(fa.HostVarRefs) != 2 {
					t.Errorf("FileAnalysis.HostVarRefs length = %d, want 2", len(fa.HostVarRefs))
				}
				if fa.HostVarRefs[0].Name != "#EMPLOYEE" {
					t.Errorf("HostVarRef[0].Name = %q, want %q", fa.HostVarRefs[0].Name, "#EMPLOYEE")
				}
				if fa.HostVarRefs[0].Range.Start.Line != 10 {
					t.Errorf("HostVarRef[0].Range.Start.Line = %d, want 10", fa.HostVarRefs[0].Range.Start.Line)
				}
				if fa.HostVarRefs[1].Name != "#SALARY" {
					t.Errorf("HostVarRef[1].Name = %q, want %q", fa.HostVarRefs[1].Name, "#SALARY")
				}
			},
		},
		{
			name: "HostVarRefs_field_is_nil_when_not_set",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.HostVarRefs != nil {
					t.Errorf("FileAnalysis.HostVarRefs = %v, want nil", fa.HostVarRefs)
				}
			},
		},
		{
			name: "HostVarRef_with_sigil_normalization",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
					HostVarRefs: []HostVarRef{
						{
							Name: "&FIELD",
							Range: Range{
								Start: Position{Line: 5, Column: 1},
								End:   Position{Line: 5, Column: 7},
							},
						},
						{
							Name: "@FIELD",
							Range: Range{
								Start: Position{Line: 6, Column: 1},
								End:   Position{Line: 6, Column: 7},
							},
						},
						{
							Name: "+AIV",
							Range: Range{
								Start: Position{Line: 7, Column: 1},
								End:   Position{Line: 7, Column: 5},
							},
						},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if len(fa.HostVarRefs) != 3 {
					t.Fatalf("HostVarRefs length = %d, want 3", len(fa.HostVarRefs))
				}
				if fa.HostVarRefs[0].Name != "&FIELD" {
					t.Errorf("HostVarRef[0] (ampersand sigil) = %q, want %q", fa.HostVarRefs[0].Name, "&FIELD")
				}
				if fa.HostVarRefs[1].Name != "@FIELD" {
					t.Errorf("HostVarRef[1] (at sigil) = %q, want %q", fa.HostVarRefs[1].Name, "@FIELD")
				}
				if fa.HostVarRefs[2].Name != "+AIV" {
					t.Errorf("HostVarRef[2] (plus sigil) = %q, want %q", fa.HostVarRefs[2].Name, "+AIV")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fa := tc.initialize()
			tc.verify(t, fa)
		})
	}
}

// TestFileAnalysis_ASTField verifies that FileAnalysis has an AST field
// for the parser foundation (Task 1 of feature 00-parser-foundation).
//
// The test asserts:
//   - FileAnalysis has an AST field of type interface{}
//   - The AST field can be nil (valid zero value)
//   - The AST field can be set to a non-nil value
func TestFileAnalysis_ASTField(t *testing.T) {
	tests := []struct {
		name       string
		initialize func() FileAnalysis
		verify     func(t *testing.T, fa FileAnalysis)
	}{
		{
			name: "AST_field_can_be_nil",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.AST != nil {
					t.Errorf("FileAnalysis.AST = %v, want nil", fa.AST)
				}
			},
		},
		{
			name: "AST_field_can_be_set_to_non_nil_value",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
					AST:        map[string]string{"NodeType": "program"},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.AST == nil {
					t.Fatal("FileAnalysis.AST is nil, want non-nil value")
				}
			},
		},
		{
			name: "AST_field_is_nil_when_not_explicitly_set",
			initialize: func() FileAnalysis {
				return FileAnalysis{}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.AST != nil {
					t.Errorf("FileAnalysis.AST = %v, want nil", fa.AST)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fa := tc.initialize()
			tc.verify(t, fa)
		})
	}
}

// TestDiagnosticRange verifies that Diagnostic carries a positional Range field
// for syntax diagnostics to have precise editor positions (Task 1a of
// feature 00-parser-foundation).
//
// The test asserts:
//   - Diagnostic has a Range field of type Range
//   - The Range field round-trips with Start/End Position values
func TestDiagnosticRange(t *testing.T) {
	tests := []struct {
		name string
		// Initialize creates a Diagnostic with the given Range.
		initialize func() Diagnostic
		// verify runs assertions on the initialized Diagnostic.
		verify func(t *testing.T, d Diagnostic)
	}{
		{
			name: "Diagnostic_Range_roundtrips_start_end_positions",
			initialize: func() Diagnostic {
				return Diagnostic{
					Message:  "test syntax error",
					Severity: DiagnosticError,
					Range: Range{
						Start: Position{Line: 3, Column: 5},
						End:   Position{Line: 3, Column: 12},
					},
				}
			},
			verify: func(t *testing.T, d Diagnostic) {
				if d.Range.Start.Line != 3 {
					t.Errorf("Diagnostic.Range.Start.Line = %d, want 3", d.Range.Start.Line)
				}
				if d.Range.Start.Column != 5 {
					t.Errorf("Diagnostic.Range.Start.Column = %d, want 5", d.Range.Start.Column)
				}
				if d.Range.End.Line != 3 {
					t.Errorf("Diagnostic.Range.End.Line = %d, want 3", d.Range.End.Line)
				}
				if d.Range.End.Column != 12 {
					t.Errorf("Diagnostic.Range.End.Column = %d, want 12", d.Range.End.Column)
				}
			},
		},
		{
			name: "Diagnostic_Range_can_span_multiple_lines",
			initialize: func() Diagnostic {
				return Diagnostic{
					Message:  "multi-line error",
					Severity: DiagnosticWarning,
					Range: Range{
						Start: Position{Line: 10, Column: 1},
						End:   Position{Line: 15, Column: 20},
					},
				}
			},
			verify: func(t *testing.T, d Diagnostic) {
				if d.Range.Start.Line != 10 {
					t.Errorf("Diagnostic.Range.Start.Line = %d, want 10", d.Range.Start.Line)
				}
				if d.Range.End.Line != 15 {
					t.Errorf("Diagnostic.Range.End.Line = %d, want 15", d.Range.End.Line)
				}
				if d.Range.Start.Column != 1 {
					t.Errorf("Diagnostic.Range.Start.Column = %d, want 1", d.Range.Start.Column)
				}
				if d.Range.End.Column != 20 {
					t.Errorf("Diagnostic.Range.End.Column = %d, want 20", d.Range.End.Column)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.initialize()
			tc.verify(t, d)
		})
	}
}

// TestSymbolKindConstants asserts exact string values of the new SymbolKind
// constants added for the hierarchical Symbol type (feature 09).
// These values are cache keys and must never be mutated.
func TestSymbolKindConstants(t *testing.T) {
	tests := []struct {
		name     string
		kind     SymbolKind
		expected string
	}{
		{"SymbolProgram", SymbolProgram, "program"},
		{"SymbolObject", SymbolObject, "object"},
		{"SymbolSubroutine", SymbolSubroutine, "subroutine"},
		{"SymbolDataSection", SymbolDataSection, "data-section"},
		{"SymbolDataField", SymbolDataField, "data-field"},
		{"SymbolMap", SymbolMap, "map"},
		{"SymbolDDMReference", SymbolDDMReference, "ddm-reference"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.kind != SymbolKind(tc.expected) {
				t.Errorf("SymbolKind constant %s = %q, want %q", tc.name, tc.kind, tc.expected)
			}
		})
	}
}

// TestSymbolHierarchicalStructure asserts that the Symbol type supports
// hierarchical, recursive nesting with Kind, Name, Range, SelectionRange,
// and Children fields. This test constructs a nested symbol tree (object root
// with data-section + subroutine children, and data-field children under the
// section) and verifies round-trip and structure integrity (feature 09, T1).
func TestSymbolHierarchicalStructure(t *testing.T) {
	tests := []struct {
		name   string
		symbol func() Symbol
		verify func(t *testing.T, s Symbol)
	}{
		{
			name: "Symbol_root_object_with_nested_section_and_fields",
			symbol: func() Symbol {
				return Symbol{
					Kind: SymbolObject,
					Name: "MYPROGRAM",
					Range: Range{
						Start: Position{Line: 1, Column: 1},
						End:   Position{Line: 50, Column: 1},
					},
					SelectionRange: Range{
						Start: Position{Line: 1, Column: 1},
						End:   Position{Line: 1, Column: 10},
					},
					Children: []Symbol{
						{
							Kind: SymbolDataSection,
							Name: "LOCAL",
							Range: Range{
								Start: Position{Line: 3, Column: 1},
								End:   Position{Line: 10, Column: 1},
							},
							SelectionRange: Range{
								Start: Position{Line: 3, Column: 1},
								End:   Position{Line: 3, Column: 5},
							},
							Children: []Symbol{
								{
									Kind: SymbolDataField,
									Name: "FIELD1",
									Range: Range{
										Start: Position{Line: 4, Column: 1},
										End:   Position{Line: 4, Column: 20},
									},
									SelectionRange: Range{
										Start: Position{Line: 4, Column: 1},
										End:   Position{Line: 4, Column: 7},
									},
									Children: nil,
								},
								{
									Kind: SymbolDataField,
									Name: "FIELD2",
									Range: Range{
										Start: Position{Line: 5, Column: 1},
										End:   Position{Line: 5, Column: 20},
									},
									SelectionRange: Range{
										Start: Position{Line: 5, Column: 1},
										End:   Position{Line: 5, Column: 7},
									},
									Children: nil,
								},
							},
						},
						{
							Kind: SymbolSubroutine,
							Name: "MYSUB",
							Range: Range{
								Start: Position{Line: 15, Column: 1},
								End:   Position{Line: 20, Column: 1},
							},
							SelectionRange: Range{
								Start: Position{Line: 15, Column: 18},
								End:   Position{Line: 15, Column: 23},
							},
							Children: nil,
						},
					},
				}
			},
			verify: func(t *testing.T, s Symbol) {
				if s.Kind != SymbolObject {
					t.Errorf("root Kind = %q, want %q", s.Kind, SymbolObject)
				}
				if s.Name != "MYPROGRAM" {
					t.Errorf("root Name = %q, want %q", s.Name, "MYPROGRAM")
				}
				if s.Range.Start.Line != 1 {
					t.Errorf("root Range.Start.Line = %d, want 1", s.Range.Start.Line)
				}
				if len(s.Children) != 2 {
					t.Errorf("root Children length = %d, want 2", len(s.Children))
				}

				// Verify first child (data section)
				section := s.Children[0]
				if section.Kind != SymbolDataSection {
					t.Errorf("section Kind = %q, want %q", section.Kind, SymbolDataSection)
				}
				if section.Name != "LOCAL" {
					t.Errorf("section Name = %q, want %q", section.Name, "LOCAL")
				}
				if len(section.Children) != 2 {
					t.Errorf("section Children length = %d, want 2", len(section.Children))
				}

				// Verify field children under section
				field1 := section.Children[0]
				if field1.Kind != SymbolDataField {
					t.Errorf("field1 Kind = %q, want %q", field1.Kind, SymbolDataField)
				}
				if field1.Name != "FIELD1" {
					t.Errorf("field1 Name = %q, want %q", field1.Name, "FIELD1")
				}

				field2 := section.Children[1]
				if field2.Kind != SymbolDataField {
					t.Errorf("field2 Kind = %q, want %q", field2.Kind, SymbolDataField)
				}
				if field2.Name != "FIELD2" {
					t.Errorf("field2 Name = %q, want %q", field2.Name, "FIELD2")
				}

				// Verify second child (subroutine)
				sub := s.Children[1]
				if sub.Kind != SymbolSubroutine {
					t.Errorf("subroutine Kind = %q, want %q", sub.Kind, SymbolSubroutine)
				}
				if sub.Name != "MYSUB" {
					t.Errorf("subroutine Name = %q, want %q", sub.Name, "MYSUB")
				}
				if len(sub.Children) != 0 {
					t.Errorf("subroutine Children length = %d, want 0", len(sub.Children))
				}
			},
		},
		{
			name: "Symbol_with_map_and_fields",
			symbol: func() Symbol {
				return Symbol{
					Kind: SymbolMap,
					Name: "MYMAP",
					Range: Range{
						Start: Position{Line: 1, Column: 1},
						End:   Position{Line: 10, Column: 1},
					},
					SelectionRange: Range{
						Start: Position{Line: 1, Column: 12},
						End:   Position{Line: 1, Column: 17},
					},
					Children: []Symbol{
						{
							Kind: SymbolDataField,
							Name: "MAPFIELD",
							Range: Range{
								Start: Position{Line: 2, Column: 1},
								End:   Position{Line: 2, Column: 15},
							},
							SelectionRange: Range{
								Start: Position{Line: 2, Column: 1},
								End:   Position{Line: 2, Column: 9},
							},
							Children: nil,
						},
					},
				}
			},
			verify: func(t *testing.T, s Symbol) {
				if s.Kind != SymbolMap {
					t.Errorf("map Kind = %q, want %q", s.Kind, SymbolMap)
				}
				if len(s.Children) != 1 {
					t.Errorf("map Children length = %d, want 1", len(s.Children))
				}
				if s.Children[0].Kind != SymbolDataField {
					t.Errorf("map child Kind = %q, want %q", s.Children[0].Kind, SymbolDataField)
				}
			},
		},
		{
			name: "Symbol_DDMReference_leaf_node",
			symbol: func() Symbol {
				return Symbol{
					Kind: SymbolDDMReference,
					Name: "EMPLOYEE",
					Range: Range{
						Start: Position{Line: 25, Column: 1},
						End:   Position{Line: 25, Column: 20},
					},
					SelectionRange: Range{
						Start: Position{Line: 25, Column: 6},
						End:   Position{Line: 25, Column: 14},
					},
					Children: nil,
				}
			},
			verify: func(t *testing.T, s Symbol) {
				if s.Kind != SymbolDDMReference {
					t.Errorf("DDM Kind = %q, want %q", s.Kind, SymbolDDMReference)
				}
				if s.Name != "EMPLOYEE" {
					t.Errorf("DDM Name = %q, want %q", s.Name, "EMPLOYEE")
				}
				if len(s.Children) != 0 {
					t.Errorf("DDM Children length = %d, want 0", len(s.Children))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sym := tc.symbol()
			tc.verify(t, sym)
		})
	}
}

// TestFileAnalysisStructureField verifies that FileAnalysis has a Structure field
// of type *Symbol (hierarchical symbol tree) and that it can be populated and
// round-trips correctly. Nil Structure indicates no structure extraction (unknown
// extension, complete parse failure). This test is for feature 09 (program-structure
// extraction), T1 (add model.Symbol type).
func TestFileAnalysisStructureField(t *testing.T) {
	tests := []struct {
		name       string
		initialize func() FileAnalysis
		verify     func(t *testing.T, fa FileAnalysis)
	}{
		{
			name: "Structure_field_can_be_populated_with_symbol_tree",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
					Structure: &Symbol{
						Kind: SymbolObject,
						Name: "PROG1",
						Range: Range{
							Start: Position{Line: 1, Column: 1},
							End:   Position{Line: 30, Column: 1},
						},
						SelectionRange: Range{
							Start: Position{Line: 1, Column: 1},
							End:   Position{Line: 1, Column: 6},
						},
						Children: []Symbol{
							{
								Kind: SymbolSubroutine,
								Name: "SUB1",
								Range: Range{
									Start: Position{Line: 10, Column: 1},
									End:   Position{Line: 15, Column: 1},
								},
								SelectionRange: Range{
									Start: Position{Line: 10, Column: 18},
									End:   Position{Line: 10, Column: 22},
								},
								Children: nil,
							},
						},
					},
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Structure == nil {
					t.Fatal("FileAnalysis.Structure is nil, want non-nil *Symbol")
				}
				if fa.Structure.Kind != SymbolObject {
					t.Errorf("Structure.Kind = %q, want %q", fa.Structure.Kind, SymbolObject)
				}
				if fa.Structure.Name != "PROG1" {
					t.Errorf("Structure.Name = %q, want %q", fa.Structure.Name, "PROG1")
				}
				if len(fa.Structure.Children) != 1 {
					t.Errorf("Structure.Children length = %d, want 1", len(fa.Structure.Children))
				}
				if fa.Structure.Children[0].Kind != SymbolSubroutine {
					t.Errorf("first child Kind = %q, want %q", fa.Structure.Children[0].Kind, SymbolSubroutine)
				}
			},
		},
		{
			name: "Structure_field_is_nil_when_not_set",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectProgram,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Structure != nil {
					t.Errorf("FileAnalysis.Structure = %v, want nil", fa.Structure)
				}
			},
		},
		{
			name: "Structure_field_is_nil_for_unknown_object",
			initialize: func() FileAnalysis {
				return FileAnalysis{
					ObjectType: ObjectUnknown,
					Structure:  nil,
				}
			},
			verify: func(t *testing.T, fa FileAnalysis) {
				if fa.Structure != nil {
					t.Errorf("FileAnalysis.Structure = %v, want nil for unknown object", fa.Structure)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fa := tc.initialize()
			tc.verify(t, fa)
		})
	}
}
