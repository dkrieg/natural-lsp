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
							File:   "MYFILE",
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
				if fa.DataAccess[0].File != "MYFILE" {
					t.Errorf("DataAccessEntry.File = %q, want %q", fa.DataAccess[0].File, "MYFILE")
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
						{File: "DATAFILE", Kind: EdgeReads},
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
