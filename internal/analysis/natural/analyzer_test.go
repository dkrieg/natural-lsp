package natural

import (
	"natural-lsp/internal/analysis"
	"natural-lsp/internal/model"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAnalyze_ObjectType verifies that Analyze sets FileAnalysis.ObjectType from the path
// using the classify function, independent of content (Task 3 / FR-7).
func TestAnalyze_ObjectType(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		content          []byte
		customExtensions map[string]model.ObjectType
		expectedType     model.ObjectType
		expectErr        bool
	}{
		// Core behavior: extension-based classification
		{
			name:             "NSP_classifies_as_Program",
			path:             "path/to/program.NSP",
			content:          nil,
			customExtensions: nil,
			expectedType:     model.ObjectProgram,
			expectErr:        false,
		},
		{
			name:             "NSP_content_independent",
			path:             "path/to/program.NSP",
			content:          []byte("garbage content not a valid program"),
			customExtensions: nil,
			expectedType:     model.ObjectProgram,
			expectErr:        false,
		},
		{
			name:             "NSN_classifies_as_Subprogram",
			path:             "sub.nsn",
			content:          nil,
			customExtensions: nil,
			expectedType:     model.ObjectSubprogram,
			expectErr:        false,
		},
		// Custom extension mapping
		{
			name:             "custom_NAT_to_Program",
			path:             "x.NAT",
			content:          nil,
			customExtensions: map[string]model.ObjectType{".NAT": model.ObjectProgram},
			expectedType:     model.ObjectProgram,
			expectErr:        false,
		},
		{
			name:             "custom_override_NSP_to_Subprogram",
			path:             "file.NSP",
			content:          nil,
			customExtensions: map[string]model.ObjectType{".NSP": model.ObjectSubprogram},
			expectedType:     model.ObjectSubprogram,
			expectErr:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Call through the analysis.Analyzer interface to ensure the seam is exercised.
			var a analysis.Analyzer = &Analyzer{custom: tc.customExtensions}
			result, err := a.Analyze(tc.path, tc.content)

			// Assert error expectation.
			if (err != nil) != tc.expectErr {
				t.Errorf("Analyze(%q, %q) error = %v, wantErr %v", tc.path, tc.content, err, tc.expectErr)
			}

			// Assert ObjectType.
			if result.ObjectType != tc.expectedType {
				t.Errorf("Analyze(%q, …) ObjectType = %q, want %q", tc.path, result.ObjectType, tc.expectedType)
			}
		})
	}
}

// TestAnalyze_CoreTypeFixtures verifies that core object types are correctly
// classified for real fixture files under testdata/objecttype/ (FR-7 acceptance).
// Each fixture is a minimal but valid Natural source file that demonstrates the
// mapping from file extension to ObjectType.
func TestAnalyze_CoreTypeFixtures(t *testing.T) {
	tests := []struct {
		name         string
		fixturePath  string
		expectedType model.ObjectType
	}{
		// Core types (FR-7): each extension maps to its corresponding ObjectType
		{
			name:         "NSP_Program",
			fixturePath:  "program.NSP",
			expectedType: model.ObjectProgram,
		},
		{
			name:         "NSN_Subprogram",
			fixturePath:  "subprogram.NSN",
			expectedType: model.ObjectSubprogram,
		},
		{
			name:         "NSS_ExternalSubroutine",
			fixturePath:  "subroutine.NSS",
			expectedType: model.ObjectExternalSubroutine,
		},
		{
			name:         "NSC_Copycode",
			fixturePath:  "copycode.NSC",
			expectedType: model.ObjectCopycode,
		},
		{
			name:         "NSM_Map",
			fixturePath:  "map.NSM",
			expectedType: model.ObjectMap,
		},
		{
			name:         "NSL_LocalDataArea",
			fixturePath:  "local.NSL",
			expectedType: model.ObjectLocalDataArea,
		},
		{
			name:         "NSG_GlobalDataArea",
			fixturePath:  "global.NSG",
			expectedType: model.ObjectGlobalDataArea,
		},
		{
			name:         "NSA_ParameterDataArea",
			fixturePath:  "parameter.NSA",
			expectedType: model.ObjectParameterDataArea,
		},
		{
			name:         "NSH_Helproutine",
			fixturePath:  "helproutine.NSH",
			expectedType: model.ObjectHelproutine,
		},
		{
			name:         "NSD_DDM",
			fixturePath:  "ddm.NSD",
			expectedType: model.ObjectDDM,
		},
	}

	// Find the module root to construct the proper fixture path.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	moduleRoot := findModuleRoot(t, thisFile)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixturePath := filepath.Join(moduleRoot, "testdata/objecttype", tc.fixturePath)

			// Read the fixture file from disk
			content, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) failed: %v", fixturePath, err)
			}

			// Normalize the path to an absolute path for consistent analysis
			absPath, err := filepath.Abs(fixturePath)
			if err != nil {
				t.Fatalf("filepath.Abs(%q) failed: %v", fixturePath, err)
			}

			// Call through the analysis.Analyzer interface to exercise the seam
			var a analysis.Analyzer = New(nil)
			result, err := a.Analyze(absPath, content)

			// Assert no error
			if err != nil {
				t.Errorf("Analyze(%q, …) error = %v, want nil", absPath, err)
			}

			// Assert ObjectType is correct
			if result.ObjectType != tc.expectedType {
				t.Errorf("Analyze(%q, …) ObjectType = %q, want %q", absPath, result.ObjectType, tc.expectedType)
			}
		})
	}
}

// TestAnalyze_UnknownExtension verifies that Analyze classifies unknown extensions as
// ObjectUnknown and surfaces an extraction-level diagnostic for observability (Task 5 / FR-9).
// Per FR-43 (graceful degradation), no error is returned — the fact is observable in
// FileAnalysis.Diagnostics. For recognized extensions, Diagnostics remains empty (regression).
func TestAnalyze_UnknownExtension(t *testing.T) {
	tests := []struct {
		name              string
		path              string
		content           []byte
		expectedType      model.ObjectType
		expectDiagnostics bool   // true if diagnostics should be non-empty, false if empty/nil
		expectMessage     string // substring to match in diagnostic message if expectDiagnostics
	}{
		// Unknown extensions: should be classified ObjectUnknown with a diagnostic
		{
			name:              "txt_file_unknown_extension",
			path:              "notes.txt",
			content:           []byte("this is a text note"),
			expectedType:      model.ObjectUnknown,
			expectDiagnostics: true,
			expectMessage:     ".TXT", // message should contain normalized extension
		},
		{
			name:              "unrecognized_NSZ_extension",
			path:              "data.NSZ",
			content:           []byte("* unknown NSZ object\n"),
			expectedType:      model.ObjectUnknown,
			expectDiagnostics: true,
			expectMessage:     ".NSZ",
		},
		// Regression: recognized extension should not have diagnostics
		{
			name:              "program_NSP_no_diagnostics",
			path:              "program.NSP",
			content:           []byte("WRITE 'HELLO'\nEND"),
			expectedType:      model.ObjectProgram,
			expectDiagnostics: false,
			expectMessage:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var a analysis.Analyzer = New(nil)
			result, err := a.Analyze(tc.path, tc.content)

			// Assert no error (graceful degradation per FR-43)
			if err != nil {
				t.Errorf("Analyze(%q, …) error = %v, want nil", tc.path, err)
			}

			// Assert ObjectType
			if result.ObjectType != tc.expectedType {
				t.Errorf("Analyze(%q, …) ObjectType = %q, want %q", tc.path, result.ObjectType, tc.expectedType)
			}

			// Assert diagnostic expectations
			if tc.expectDiagnostics {
				// Should have at least one diagnostic
				if len(result.Diagnostics) == 0 {
					t.Errorf("Analyze(%q, …) Diagnostics = empty, want at least 1 entry", tc.path)
				} else {
					// Check the first diagnostic
					diag := result.Diagnostics[0]

					// Assert severity is DiagnosticInfo
					if diag.Severity != model.DiagnosticInfo {
						t.Errorf("Analyze(%q, …) Diagnostics[0].Severity = %q, want %q", tc.path, diag.Severity, model.DiagnosticInfo)
					}

					// Assert message contains the expected extension (normalized)
					if !strings.Contains(diag.Message, tc.expectMessage) {
						t.Errorf("Analyze(%q, …) Diagnostics[0].Message = %q, does not contain %q", tc.path, diag.Message, tc.expectMessage)
					}
				}
			} else {
				// Should have no diagnostics
				if len(result.Diagnostics) != 0 {
					t.Errorf("Analyze(%q, …) Diagnostics = %v, want empty/nil", tc.path, result.Diagnostics)
				}
			}
		})
	}
}

// TestAnalyze_ExtendedTypeFixtures verifies that extended object types (Task 6 / FR-8)
// are correctly classified for real fixture files under testdata/objecttype/.
// This test confirms that the five extended types (Class, Function, Dialog, Adapter, Text)
// are handled by the full classification pipeline and that enabling them does not break
// core-type or unknown-type behavior (Story 3 acceptance criterion).
func TestAnalyze_ExtendedTypeFixtures(t *testing.T) {
	tests := []struct {
		name         string
		fixturePath  string
		expectedType model.ObjectType
	}{
		// Extended types (FR-8): verified via fixture files
		{
			name:         "NS4_Class",
			fixturePath:  "class.NS4",
			expectedType: model.ObjectClass,
		},
		{
			name:         "NS7_Function",
			fixturePath:  "function.NS7",
			expectedType: model.ObjectFunction,
		},
		{
			name:         "NS3_Dialog",
			fixturePath:  "dialog.NS3",
			expectedType: model.ObjectDialog,
		},
		{
			name:         "NS8_Adapter",
			fixturePath:  "adapter.NS8",
			expectedType: model.ObjectAdapter,
		},
		{
			name:         "NST_Text",
			fixturePath:  "text.NST",
			expectedType: model.ObjectText,
		},
		// Regression: core types still work after extended types added (Story 3)
		{
			name:         "NSP_Program_regression",
			fixturePath:  "program.NSP",
			expectedType: model.ObjectProgram,
		},
		{
			name:         "NSN_Subprogram_regression",
			fixturePath:  "subprogram.NSN",
			expectedType: model.ObjectSubprogram,
		},
		{
			name:         "NSC_Copycode_regression",
			fixturePath:  "copycode.NSC",
			expectedType: model.ObjectCopycode,
		},
		{
			name:         "NSM_Map_regression",
			fixturePath:  "map.NSM",
			expectedType: model.ObjectMap,
		},
		// Regression: unknown type still handled correctly
		{
			name:         "txt_unknown_regression",
			fixturePath:  "notes.txt",
			expectedType: model.ObjectUnknown,
		},
	}

	// Find the module root to construct the proper fixture path.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	moduleRoot := findModuleRoot(t, thisFile)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixturePath := filepath.Join(moduleRoot, "testdata/objecttype", tc.fixturePath)

			// Read the fixture file from disk
			content, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) failed: %v", fixturePath, err)
			}

			// Normalize the path to an absolute path for consistent analysis
			absPath, err := filepath.Abs(fixturePath)
			if err != nil {
				t.Fatalf("filepath.Abs(%q) failed: %v", fixturePath, err)
			}

			// Call through the analysis.Analyzer interface to exercise the seam
			var a analysis.Analyzer = New(nil)
			result, err := a.Analyze(absPath, content)

			// Assert no error
			if err != nil {
				t.Errorf("Analyze(%q, …) error = %v, want nil", absPath, err)
			}

			// Assert ObjectType is correct
			if result.ObjectType != tc.expectedType {
				t.Errorf("Analyze(%q, …) ObjectType = %q, want %q", absPath, result.ObjectType, tc.expectedType)
			}

			// For extended and core types (not unknown), assert no diagnostics
			if tc.expectedType != model.ObjectUnknown {
				if len(result.Diagnostics) != 0 {
					t.Errorf("Analyze(%q, …) Diagnostics = %v, want empty for recognized types", absPath, result.Diagnostics)
				}
			}
		})
	}
}

// findModuleRoot walks up the directory tree from a file to find the module root
// by locating the go.mod file.
func findModuleRoot(t *testing.T, fromFile string) string {
	t.Helper()
	dir := filepath.Dir(fromFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// reached filesystem root
			t.Fatalf("could not find go.mod starting from %q", fromFile)
		}
		dir = parent
	}
}
