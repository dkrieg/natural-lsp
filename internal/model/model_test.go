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
