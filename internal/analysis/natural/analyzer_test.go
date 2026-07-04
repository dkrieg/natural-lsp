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

// TestAnalyze_EdgesPopulatedInSourceOrder verifies that Analyze wires the call
// extractor into the analysis pipeline and returns FileAnalysis.Edges populated
// with all edge kinds in GLOBAL source order (Task 9 / NFR-6, M-6).
//
// Acceptance criteria (Task 9):
//   - Analyze returns FileAnalysis.Edges (not empty) containing all extracted edges
//   - Edges are ordered by source position (statement line, then column) — GLOBAL
//     source order across all edge kinds, not per-kind grouping
//   - All edge kinds are represented: CALLNAT static/dynamic, PERFORM inline/external,
//     INCLUDE, FETCH static/dynamic, RUN with library
//   - Inline PERFORM targets are marked in-file-resolved (Target = definition range)
//   - Placeholder literals (& runtime-substitution) are downgraded to dynamic
//   - Caller context (Source) is preserved on every edge
//   - FileAnalysis.Diagnostics is unchanged (only parser syntax diagnostics, no edge diagnostics)
//   - FileAnalysis.AST is still set
//
// Fixture: 09-mixed.NSP exercises all edge kinds interleaved in source order.
func TestAnalyze_EdgesPopulatedInSourceOrder(t *testing.T) {
	// Read the fixture from testdata/calls/
	fixturePath := filepath.Join("testdata", "calls", "09-mixed.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	// Assert FileAnalysis.Edges is populated (not empty)
	if len(result.Edges) == 0 {
		t.Fatal("FileAnalysis.Edges is empty; want populated edges from 09-mixed.NSP (all edge kinds)")
	}

	// Assert expected edge count: all edge kinds exercised in the fixture
	// Expected edges in source order:
	// 1. INCLUDE 'COMMON-DECLS' (line 11)
	// 2. CALLNAT 'PROG-A' (line 14) — static
	// 3. PERFORM LOCAL-SUB (line 17) — inline-resolved
	// 4. FETCH 'RPT001' (line 26) — static
	// 5. CALLNAT #PROG-NAME (line 29) — dynamic
	// 6. RUN 'BATCHJOB' 'MYLIB' (line 32) — with library
	// 7. CALLNAT 'PRG&LANG' (line 35) — placeholder literal, downgraded to dynamic
	// 8. FETCH #RPT-NAME (line 38) — dynamic
	if len(result.Edges) != 8 {
		t.Errorf("len(Edges) = %d, want 8", len(result.Edges))
		for i, edge := range result.Edges {
			t.Logf("  Edge[%d]: Kind=%s, TargetName=%q, Source={line %d col %d}",
				i, edge.Kind, edge.TargetName, edge.Source.Start.Line, edge.Source.Start.Column)
		}
	}

	// Assert edges are in source order (GLOBAL source order across kinds)
	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_EdgesPopulatedInSourceOrder_correctSequence",
			verify: func(t *testing.T, result model.FileAnalysis) {
				edges := result.Edges
				if len(edges) < 8 {
					t.Skip("not enough edges to verify sequence")
				}

				// Edge 0: INCLUDE 'COMMON-DECLS' @ line 11
				if edges[0].Kind != model.EdgeIncludes || edges[0].TargetName != "COMMON-DECLS" {
					t.Errorf("Edge[0]: Kind=%s TargetName=%q, want EdgeIncludes 'COMMON-DECLS'",
						edges[0].Kind, edges[0].TargetName)
				}

				// Edge 1: CALLNAT 'PROG-A' (static) @ line 14
				if edges[1].Kind != model.EdgeCalls || edges[1].TargetName != "PROG-A" {
					t.Errorf("Edge[1]: Kind=%s TargetName=%q, want EdgeCalls 'PROG-A'",
						edges[1].Kind, edges[1].TargetName)
				}

				// Edge 2: PERFORM LOCAL-SUB (inline-resolved) @ line 17
				// Target must be non-zero (pointing to DEFINE SUBROUTINE definition)
				if edges[2].Kind != model.EdgePerforms || edges[2].TargetName != "LOCAL-SUB" {
					t.Errorf("Edge[2]: Kind=%s TargetName=%q, want EdgePerforms 'LOCAL-SUB'",
						edges[2].Kind, edges[2].TargetName)
				}
				if edges[2].Target.Start.Line == 0 && edges[2].Target.Start.Column == 0 &&
					edges[2].Target.End.Line == 0 && edges[2].Target.End.Column == 0 {
					t.Error("Edge[2].Target is zero, want non-zero (inline definition range)")
				}

				// Edge 3: FETCH 'RPT001' (static) @ line 26
				if edges[3].Kind != model.EdgeNavigatesTo || edges[3].TargetName != "RPT001" {
					t.Errorf("Edge[3]: Kind=%s TargetName=%q, want EdgeNavigatesTo 'RPT001'",
						edges[3].Kind, edges[3].TargetName)
				}

				// Edge 4: CALLNAT #PROG-NAME (dynamic) @ line 29
				if edges[4].Kind != model.EdgeCallsDynamic || edges[4].TargetName != "#PROG-NAME" {
					t.Errorf("Edge[4]: Kind=%s TargetName=%q, want EdgeCallsDynamic '#PROG-NAME'",
						edges[4].Kind, edges[4].TargetName)
				}

				// Edge 5: RUN 'BATCHJOB' 'MYLIB' (with library) @ line 32
				if edges[5].Kind != model.EdgeNavigatesTo || edges[5].TargetName != "BATCHJOB" {
					t.Errorf("Edge[5]: Kind=%s TargetName=%q, want EdgeNavigatesTo 'BATCHJOB'",
						edges[5].Kind, edges[5].TargetName)
				}
				if edges[5].Library != "MYLIB" {
					t.Errorf("Edge[5].Library = %q, want 'MYLIB'", edges[5].Library)
				}

				// Edge 6: CALLNAT 'PRG&LANG' (placeholder, downgraded to dynamic) @ line 35
				if edges[6].Kind != model.EdgeCallsDynamic || edges[6].TargetName != "PRG&LANG" {
					t.Errorf("Edge[6]: Kind=%s TargetName=%q, want EdgeCallsDynamic 'PRG&LANG'",
						edges[6].Kind, edges[6].TargetName)
				}

				// Edge 7: FETCH #RPT-NAME (dynamic) @ line 38
				if edges[7].Kind != model.EdgeNavigatesToDynamic || edges[7].TargetName != "#RPT-NAME" {
					t.Errorf("Edge[7]: Kind=%s TargetName=%q, want EdgeNavigatesToDynamic '#RPT-NAME'",
						edges[7].Kind, edges[7].TargetName)
				}

				// Verify global source order: each edge's source line is <= next edge's source line
				for i := 0; i < len(edges)-1; i++ {
					currLine := edges[i].Source.Start.Line
					nextLine := edges[i+1].Source.Start.Line
					if currLine > nextLine {
						t.Errorf("edges not in source order: Edge[%d] at line %d > Edge[%d] at line %d",
							i, currLine, i+1, nextLine)
					}
					// If on the same line, verify column order
					if currLine == nextLine && edges[i].Source.Start.Column > edges[i+1].Source.Start.Column {
						t.Errorf("edges not in source order: Edge[%d] at col %d > Edge[%d] at col %d (same line)",
							i, edges[i].Source.Start.Column, i+1, edges[i+1].Source.Start.Column)
					}
				}
			},
		},
		{
			name: "Analyze_EdgesPopulatedInSourceOrder_diagnosticsUnchanged",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Diagnostics should be empty (no parse errors in 09-mixed.NSP)
				if len(result.Diagnostics) != 0 {
					t.Errorf("FileAnalysis.Diagnostics = %v, want empty (no parse errors in fixture)",
						result.Diagnostics)
				}
			},
		},
		{
			name: "Analyze_EdgesPopulatedInSourceOrder_ASTSet",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// AST must be non-nil (populated by parser)
				if result.AST == nil {
					t.Error("FileAnalysis.AST is nil, want non-nil (populated by parser)")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestAnalyze_ChannelSeparation_Malformed verifies that extraction over a
// partially-parsed AST still produces valid edges while malformed statements
// surface as diagnostics. No silent gaps; the two channels (edges vs. diagnostics)
// stay separate (Task 9 / M-6, NFR-6, FR-17).
//
// Acceptance criteria (Task 9, M-6/NFR-6):
//   - Valid CALLNAT and INCLUDE statements in the file produce edges
//   - Malformed statement (missing required operand) produces a diagnostic
//   - Valid edges are NOT suppressed or dropped due to parse errors
//   - Malformed statement is NOT emitted as an edge
//   - Channel separation: edges and diagnostics are distinct (no cross-contamination)
//
// Fixture: 09-malformed.NSP contains valid CALLNAT and INCLUDE edges plus a
// malformed FETCH statement (missing target).
func TestAnalyze_ChannelSeparation_Malformed(t *testing.T) {
	// Read the fixture from testdata/calls/
	fixturePath := filepath.Join("testdata", "calls", "09-malformed.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	// Assert FileAnalysis.Edges contains valid edges (not empty, not dropped)
	// Expected: CALLNAT 'PROG-A' (line 12) and INCLUDE 'COMMON' (line 19)
	if len(result.Edges) == 0 {
		t.Fatal("FileAnalysis.Edges is empty; want valid edges despite malformed statement")
	}
	if len(result.Edges) < 2 {
		t.Errorf("len(Edges) = %d, want at least 2 (CALLNAT + INCLUDE)", len(result.Edges))
	}

	// Assert FileAnalysis.Diagnostics contains the malformed statement's error
	if len(result.Diagnostics) == 0 {
		t.Fatal("FileAnalysis.Diagnostics is empty; want diagnostic for malformed FETCH")
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_ChannelSeparation_Malformed_validEdgesPreserved",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Expected edges in source order:
				// 1. CALLNAT 'PROG-A' (line 12)
				// 2. INCLUDE 'COMMON' (line 19)
				edges := result.Edges
				if len(edges) < 2 {
					t.Skip("not enough edges to verify")
				}

				// First edge: CALLNAT 'PROG-A'
				if edges[0].Kind != model.EdgeCalls || edges[0].TargetName != "PROG-A" {
					t.Errorf("Edge[0]: Kind=%s TargetName=%q, want EdgeCalls 'PROG-A'",
						edges[0].Kind, edges[0].TargetName)
				}
				// Source must be non-zero (call-site range with caller context)
				if edges[0].Source.Start.Line == 0 && edges[0].Source.Start.Column == 0 &&
					edges[0].Source.End.Line == 0 && edges[0].Source.End.Column == 0 {
					t.Error("Edge[0].Source is zero, want non-zero call-site range")
				}

				// Second edge: INCLUDE 'COMMON'
				if edges[1].Kind != model.EdgeIncludes || edges[1].TargetName != "COMMON" {
					t.Errorf("Edge[1]: Kind=%s TargetName=%q, want EdgeIncludes 'COMMON'",
						edges[1].Kind, edges[1].TargetName)
				}
				// Source must be non-zero
				if edges[1].Source.Start.Line == 0 && edges[1].Source.Start.Column == 0 &&
					edges[1].Source.End.Line == 0 && edges[1].Source.End.Column == 0 {
					t.Error("Edge[1].Source is zero, want non-zero statement range")
				}

				// Verify source order: CALLNAT before INCLUDE
				if edges[0].Source.Start.Line >= edges[1].Source.Start.Line {
					t.Errorf("edges not in source order: Edge[0] at line %d, Edge[1] at line %d",
						edges[0].Source.Start.Line, edges[1].Source.Start.Line)
				}
			},
		},
		{
			name: "Analyze_ChannelSeparation_Malformed_diagnosticsForMalformedStatement",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Must have at least one diagnostic for the malformed FETCH statement
				diagnostics := result.Diagnostics
				if len(diagnostics) == 0 {
					t.Error("FileAnalysis.Diagnostics is empty, want diagnostic(s) for malformed FETCH")
				}

				// The diagnostic should have a non-zero range (pointing to the malformed statement)
				if len(diagnostics) > 0 {
					diag := diagnostics[0]
					if diag.Range.Start.Line == 0 && diag.Range.Start.Column == 0 &&
						diag.Range.End.Line == 0 && diag.Range.End.Column == 0 {
						t.Error("Diagnostic range is zero, want real source position")
					}
				}
			},
		},
		{
			name: "Analyze_ChannelSeparation_Malformed_noEdgeAsaDiagnostic",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// CRITICAL: verify that no edge appears in Diagnostics (channel separation).
				// Valid edges (CALLNAT 'PROG-A', INCLUDE 'COMMON') must be in Edges,
				// not in Diagnostics.
				edges := result.Edges
				diags := result.Diagnostics

				// Verify that all diagnostics are syntax-level (parser) errors, not edge-extraction issues.
				// Parser diagnostics should not contain edge target names like "PROG-A" or "COMMON".
				for _, diag := range diags {
					// Heuristic: syntax diagnostics mention parse errors, not edge targets.
					// The malformed FETCH message should be about the statement structure,
					// not about call targets. Check that the diagnostic doesn't describe an edge.
					if strings.Contains(diag.Message, "PROG-A") ||
						strings.Contains(diag.Message, "COMMON") {
						t.Errorf("Diagnostic appears to describe an edge target, want syntax-level message: %q",
							diag.Message)
					}
				}

				// Verify the valid edges are in Edges, not dropped
				foundCallnat := false
				foundInclude := false
				for _, edge := range edges {
					if edge.Kind == model.EdgeCalls && edge.TargetName == "PROG-A" {
						foundCallnat = true
					}
					if edge.Kind == model.EdgeIncludes && edge.TargetName == "COMMON" {
						foundInclude = true
					}
				}
				if !foundCallnat {
					t.Error("CALLNAT 'PROG-A' edge not found in Edges (should not be dropped)")
				}
				if !foundInclude {
					t.Error("INCLUDE 'COMMON' edge not found in Edges (should not be dropped)")
				}
			},
		},
		{
			name: "Analyze_ChannelSeparation_Malformed_noDiagnosticAsanEdge",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// CRITICAL: verify that the malformed statement is NOT emitted as an edge.
				// Malformed FETCH should appear in Diagnostics, not in Edges.
				edges := result.Edges

				// Check that no edge has TargetName "FETCH" or similar malformed marker
				// (This is a heuristic; a real check would verify the malformed statement
				// produced a diagnostic, not an edge.)
				for _, edge := range edges {
					// Malformed statement should not produce an edge with empty TargetName
					// or incomplete data.
					if edge.TargetName == "" {
						t.Errorf("Edge with empty TargetName found (likely from malformed statement): %+v", edge)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestAnalyze_DataAccess verifies that Analyze wires the data-access extractor
// into the analysis pipeline and returns FileAnalysis.DataAccess populated with
// all extracted data-access relationships (Task 9 / FR-19, FR-20).
//
// Acceptance criteria (Task 9):
//   - Analyze calls extractDataAccess on the parsed AST
//   - FileAnalysis.DataAccess is populated with entries matching the extractor output
//   - End-to-end: raw bytes → Analyze → FileAnalysis.DataAccess with correct entries
//   - Data-access entries are returned in source order (same as extractEdges)
//   - Entry kind distinguishes reads (EdgeReads) from writes (EdgeWrites)
//   - Accessed names are normalized (uppercase) by the lexer
//
// Fixture: testdata/dataaccess/01-read-store.NSP contains READ EMPLOYEES,
// READ (5) VEHICLES BY MAKE, and STORE EMPLOYEES.
//
// Expected result: three entries in source order:
//  1. Kind:EdgeReads, Name:"EMPLOYEES" (READ, line 10)
//  2. Kind:EdgeReads, Name:"VEHICLES" (READ, line 12)
//  3. Kind:EdgeWrites, Name:"EMPLOYEES" (STORE, line 16)
func TestAnalyze_DataAccess(t *testing.T) {
	// Read the fixture from testdata/dataaccess/
	fixturePath := filepath.Join("testdata", "dataaccess", "01-read-store.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	// Assert FileAnalysis.DataAccess is populated (not empty)
	if len(result.DataAccess) == 0 {
		t.Fatalf("FileAnalysis.DataAccess is empty; want populated entries from 01-read-store.NSP (2 reads + 1 write)")
	}

	// Assert expected data-access count: 3 entries (2 READ + 1 STORE)
	if len(result.DataAccess) != 3 {
		t.Errorf("len(DataAccess) = %d, want 3", len(result.DataAccess))
		for i, da := range result.DataAccess {
			t.Logf("  DataAccess[%d]: Kind=%s, Name=%q, Source={line %d col %d}",
				i, da.Kind, da.Name, da.Source.Start.Line, da.Source.Start.Column)
		}
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_DataAccess_correctSequence",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				if len(da) < 3 {
					t.Skip("not enough data-access entries to verify sequence")
				}

				// Entry 0: READ EMPLOYEES @ line 10
				if da[0].Kind != model.EdgeReads || da[0].Name != "EMPLOYEES" {
					t.Errorf("DataAccess[0]: Kind=%s Name=%q, want EdgeReads 'EMPLOYEES'",
						da[0].Kind, da[0].Name)
				}

				// Entry 1: READ (5) VEHICLES BY MAKE @ line 12
				if da[1].Kind != model.EdgeReads || da[1].Name != "VEHICLES" {
					t.Errorf("DataAccess[1]: Kind=%s Name=%q, want EdgeReads 'VEHICLES'",
						da[1].Kind, da[1].Name)
				}

				// Entry 2: STORE EMPLOYEES @ line 16
				if da[2].Kind != model.EdgeWrites || da[2].Name != "EMPLOYEES" {
					t.Errorf("DataAccess[2]: Kind=%s Name=%q, want EdgeWrites 'EMPLOYEES'",
						da[2].Kind, da[2].Name)
				}

				// Verify source order (global source order by line number)
				for i := 0; i < len(da)-1; i++ {
					currLine := da[i].Source.Start.Line
					nextLine := da[i+1].Source.Start.Line
					if currLine > nextLine {
						t.Errorf("data-access entries not in source order: DataAccess[%d] at line %d > DataAccess[%d] at line %d",
							i, currLine, i+1, nextLine)
					}
				}
			},
		},
		{
			name: "Analyze_DataAccess_namesNormalized",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				// Assert all names are uppercase (normalized by lexer)
				for i, entry := range da {
					if entry.Name != strings.ToUpper(entry.Name) {
						t.Errorf("DataAccess[%d].Name = %q, want uppercase (normalized by lexer)", i, entry.Name)
					}
				}
			},
		},
		{
			name: "Analyze_DataAccess_readWriteDistinguishable",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				if len(da) < 3 {
					t.Skip("not enough entries to verify read/write distinction")
				}

				// Entry 0 and 1 are reads, entry 2 is a write (same view name EMPLOYEES)
				reads := []model.DataAccessEntry{}
				writes := []model.DataAccessEntry{}
				for _, entry := range da {
					if entry.Kind == model.EdgeReads {
						reads = append(reads, entry)
					} else if entry.Kind == model.EdgeWrites {
						writes = append(writes, entry)
					}
				}

				if len(reads) != 2 {
					t.Errorf("Expected 2 read entries, got %d", len(reads))
				}
				if len(writes) != 1 {
					t.Errorf("Expected 1 write entry, got %d", len(writes))
				}
			},
		},
		{
			name: "Analyze_DataAccess_matchesExtractorOutput",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Verify that Analyze's output matches a direct extractDataAccess call
				ast := result.AST
				if ast == nil {
					t.Skip("AST is nil, cannot compare with extractor")
				}

				prog, ok := ast.(*Program)
				if !ok {
					t.Skip("AST is not a *Program, cannot compare with extractor")
				}

				directOutput := extractDataAccess(prog)
				analyzeOutput := result.DataAccess

				if len(analyzeOutput) != len(directOutput) {
					t.Errorf("Analyze output len = %d, direct extractDataAccess len = %d (mismatch)",
						len(analyzeOutput), len(directOutput))
				}

				// Compare entries
				for i := range analyzeOutput {
					if i >= len(directOutput) {
						break
					}

					if analyzeOutput[i].Kind != directOutput[i].Kind {
						t.Errorf("DataAccess[%d].Kind: Analyze=%s, extractDataAccess=%s (mismatch)",
							i, analyzeOutput[i].Kind, directOutput[i].Kind)
					}
					if analyzeOutput[i].Name != directOutput[i].Name {
						t.Errorf("DataAccess[%d].Name: Analyze=%q, extractDataAccess=%q (mismatch)",
							i, analyzeOutput[i].Name, directOutput[i].Name)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestAnalyze_Definitions verifies that Analyze wires the definition extractor
// into the analysis pipeline and returns FileAnalysis.Definitions populated with
// extracted data definitions from DEFINE DATA sections (Task 13 / FR-21).
//
// Acceptance criteria (Task 13):
//   - Analyze calls extractDefinitions on the parsed AST
//   - FileAnalysis.Definitions is populated with entries matching the extractor output
//   - End-to-end: raw bytes → Analyze → FileAnalysis.Definitions with correct entries
//   - Definitions include local, parameter, and global data items
//   - Parameter items are tagged with SectionKind="parameter" so hover/signature can distinguish them
//   - Definitions are returned in declaration order
//
// Fixture: testdata/parser/23-data-sections.nsp contains:
//   - LOCAL section: 2 fields (#LOCAL-COUNTER, #LOCAL-NAME)
//   - PARAMETER section: 2 fields (#INPUT-VALUE, #OUTPUT-RESULT)
//   - GLOBAL USING section: no inline fields (external GDA reference)
//
// Expected result: 4 entries in declaration order (2 local + 2 parameter), with
// parameter items tagged SectionKind="parameter".
func TestAnalyze_Definitions(t *testing.T) {
	// Read the fixture from testdata/parser/ (reusing the parser fixture)
	fixturePath := filepath.Join("testdata", "parser", "23-data-sections.nsp")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	// Assert FileAnalysis.Definitions is populated (not empty)
	if len(result.Definitions) == 0 {
		t.Fatalf("FileAnalysis.Definitions is empty; want populated entries from 23-data-sections.nsp (2 local + 2 parameter = 4)")
	}

	// Assert expected definition count: 4 entries (2 LOCAL + 2 PARAMETER)
	if len(result.Definitions) != 4 {
		t.Errorf("len(Definitions) = %d, want 4 (2 local + 2 parameter)", len(result.Definitions))
		for i, def := range result.Definitions {
			t.Logf("  Definitions[%d]: Name=%q, SectionKind=%q, Level=%d",
				i, def.Name, def.SectionKind, def.Level)
		}
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_Definitions_correctSequence",
			verify: func(t *testing.T, result model.FileAnalysis) {
				defs := result.Definitions
				if len(defs) < 4 {
					t.Skip("not enough definitions to verify sequence")
				}

				// Definition 0: #LOCAL-COUNTER (LOCAL section)
				if defs[0].Name != "#LOCAL-COUNTER" || defs[0].SectionKind != "local" {
					t.Errorf("Definitions[0]: Name=%q SectionKind=%q, want '#LOCAL-COUNTER' 'local'",
						defs[0].Name, defs[0].SectionKind)
				}

				// Definition 1: #LOCAL-NAME (LOCAL section)
				if defs[1].Name != "#LOCAL-NAME" || defs[1].SectionKind != "local" {
					t.Errorf("Definitions[1]: Name=%q SectionKind=%q, want '#LOCAL-NAME' 'local'",
						defs[1].Name, defs[1].SectionKind)
				}

				// Definition 2: #INPUT-VALUE (PARAMETER section)
				if defs[2].Name != "#INPUT-VALUE" || defs[2].SectionKind != "parameter" {
					t.Errorf("Definitions[2]: Name=%q SectionKind=%q, want '#INPUT-VALUE' 'parameter'",
						defs[2].Name, defs[2].SectionKind)
				}

				// Definition 3: #OUTPUT-RESULT (PARAMETER section)
				if defs[3].Name != "#OUTPUT-RESULT" || defs[3].SectionKind != "parameter" {
					t.Errorf("Definitions[3]: Name=%q SectionKind=%q, want '#OUTPUT-RESULT' 'parameter'",
						defs[3].Name, defs[3].SectionKind)
				}
			},
		},
		{
			name: "Analyze_Definitions_namesNormalized",
			verify: func(t *testing.T, result model.FileAnalysis) {
				defs := result.Definitions
				// Assert all names (excluding the # prefix) are uppercase (normalized by lexer)
				for i, def := range defs {
					// Names should start with # (part of the identifier in Natural)
					if !strings.HasPrefix(def.Name, "#") {
						t.Errorf("Definitions[%d].Name = %q, want # prefix", i, def.Name)
					}
					// The part after # should be uppercase (normalized by lexer)
					nameWithoutPrefix := strings.TrimPrefix(def.Name, "#")
					if nameWithoutPrefix != strings.ToUpper(nameWithoutPrefix) {
						t.Errorf("Definitions[%d].Name = %q, want uppercase after # (normalized by lexer)", i, def.Name)
					}
				}
			},
		},
		{
			name: "Analyze_Definitions_parameterInterfaceTagged",
			verify: func(t *testing.T, result model.FileAnalysis) {
				defs := result.Definitions
				// Assert parameter section items are tagged SectionKind="parameter"
				paramCount := 0
				for _, def := range defs {
					if def.SectionKind == "parameter" {
						paramCount++
					}
				}
				if paramCount != 2 {
					t.Errorf("Parameter-section definitions count = %d, want 2", paramCount)
				}
			},
		},
		{
			name: "Analyze_Definitions_matchesExtractorOutput",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Verify that Analyze's output matches a direct extractDefinitions call
				ast := result.AST
				if ast == nil {
					t.Skip("AST is nil, cannot compare with extractor")
				}

				prog, ok := ast.(*Program)
				if !ok {
					t.Skip("AST is not a *Program, cannot compare with extractor")
				}

				directOutput := extractDefinitions(prog)
				analyzeOutput := result.Definitions

				if len(analyzeOutput) != len(directOutput) {
					t.Errorf("Analyze output len = %d, direct extractDefinitions len = %d (mismatch)",
						len(analyzeOutput), len(directOutput))
				}

				// Compare entries
				for i := range analyzeOutput {
					if i >= len(directOutput) {
						break
					}

					if analyzeOutput[i].Name != directOutput[i].Name {
						t.Errorf("Definitions[%d].Name: Analyze=%q, extractDefinitions=%q (mismatch)",
							i, analyzeOutput[i].Name, directOutput[i].Name)
					}
					if analyzeOutput[i].SectionKind != directOutput[i].SectionKind {
						t.Errorf("Definitions[%d].SectionKind: Analyze=%q, extractDefinitions=%q (mismatch)",
							i, analyzeOutput[i].SectionKind, directOutput[i].SectionKind)
					}
					if analyzeOutput[i].Level != directOutput[i].Level {
						t.Errorf("Definitions[%d].Level: Analyze=%d, extractDefinitions=%d (mismatch)",
							i, analyzeOutput[i].Level, directOutput[i].Level)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestAnalyze_Integration_Combined verifies end-to-end extraction of all data-access
// features (Task 16 / FR-19..22, FR-43): a combined fixture exercising reads (READ/FIND/GET),
// writes (STORE/record UPDATE/DELETE), DEFINE DATA sections (LOCAL/PARAMETER/GLOBAL),
// and DEFINE WORK FILE, returning deterministic combined FileAnalysis output and proving
// that all three channels (edges, definitions, work files) are populated and properly ordered.
//
// Acceptance criteria (Task 16):
//   - Analyze returns FileAnalysis with DataAccess populated (reads and writes)
//   - Analyze returns FileAnalysis with Definitions populated (local, parameter, global items)
//   - Analyze returns FileAnalysis with WorkFiles populated (DEFINE WORK FILE entries)
//   - All entries are in deterministic source order
//   - Reads and writes are distinguishable by Kind
//   - GET SAME (empty target) is skipped; produces no edge
//   - Record UPDATE/DELETE (no file operand) produce writes with empty Name
//   - Parameter section items are tagged SectionKind="parameter"
//   - Malformed input degrades gracefully: parser diagnostics present, but valid extractions preserved
//
// Fixture: testdata/dataaccess/06-combined.NSP contains:
//   - READ EMPLOYEES, FIND VEHICLES, GET DEPARTMENTS, GET SAME (skipped)
//   - STORE EMPLOYEES
//   - Record UPDATE inside READ EMPLOYEES loop
//   - Record DELETE inside READ LOCATIONS loop
//   - DEFINE DATA LOCAL (2 fields), PARAMETER (2 fields), GLOBAL USING (ref only)
//   - DEFINE WORK FILE 1 'REPORT.TXT', DEFINE WORK FILE 2 #LOGFILE
//
// Expected result: 7 data-access entries (4 reads + 3 writes; GET SAME skipped),
// 4 definitions (2 local + 2 parameter), 2 work files, all in source order,
// with correct Kind/SectionKind/Name values.
func TestAnalyze_Integration_Combined(t *testing.T) {
	// Read the combined fixture from testdata/dataaccess/
	fixturePath := filepath.Join("testdata", "dataaccess", "06-combined.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	// Assert FileAnalysis.DataAccess is populated
	if len(result.DataAccess) == 0 {
		t.Fatalf("FileAnalysis.DataAccess is empty; want entries for reads/writes from 06-combined.NSP")
	}

	// Assert FileAnalysis.Definitions is populated
	if len(result.Definitions) == 0 {
		t.Fatalf("FileAnalysis.Definitions is empty; want entries for LOCAL/PARAMETER fields from 06-combined.NSP")
	}

	// Assert FileAnalysis.WorkFiles is populated
	if len(result.WorkFiles) == 0 {
		t.Fatalf("FileAnalysis.WorkFiles is empty; want entries for DEFINE WORK FILE from 06-combined.NSP")
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_Integration_Combined_dataAccessCount",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Expected: 4 reads (READ EMPLOYEES, READ LOCATIONS, FIND VEHICLES, GET DEPARTMENTS;
				// GET SAME is skipped because it has no target) + 3 writes (STORE
				// EMPLOYEES, record UPDATE, record DELETE) = 7 total data-access entries.
				da := result.DataAccess
				if len(da) != 7 {
					t.Errorf("len(DataAccess) = %d, want 7 (4 reads + 3 writes)", len(da))
					for i, e := range da {
						t.Logf("  DataAccess[%d]: Kind=%s, Name=%q, Source=line %d",
							i, e.Kind, e.Name, e.Source.Start.Line)
					}
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_dataAccessSequence",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				if len(da) < 7 {
					t.Skip("not enough data-access entries to verify sequence")
				}

				// Verify source order: all entries sorted by line then column
				for i := 0; i < len(da)-1; i++ {
					currLine := da[i].Source.Start.Line
					nextLine := da[i+1].Source.Start.Line
					if currLine > nextLine {
						t.Errorf("data-access entries not in source order: DataAccess[%d] at line %d > DataAccess[%d] at line %d",
							i, currLine, i+1, nextLine)
					}
					if currLine == nextLine && da[i].Source.Start.Column > da[i+1].Source.Start.Column {
						t.Errorf("data-access entries not in source order (same line): DataAccess[%d] at col %d > DataAccess[%d] at col %d",
							i, da[i].Source.Start.Column, i+1, da[i+1].Source.Start.Column)
					}
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_readWriteDistinction",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				if len(da) < 7 {
					t.Skip("not enough data-access entries to verify read/write distinction")
				}

				// Count reads vs writes
				readCount := 0
				writeCount := 0
				for _, e := range da {
					if e.Kind == model.EdgeReads {
						readCount++
					} else if e.Kind == model.EdgeWrites {
						writeCount++
					}
				}

				// Expected: 4 reads (GET SAME is skipped), 3 writes
				if readCount != 4 {
					t.Errorf("read count = %d, want 4", readCount)
				}
				if writeCount != 3 {
					t.Errorf("write count = %d, want 3", writeCount)
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_getSameSkipped",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				// GET SAME should produce no edge (empty target is skipped).
				// Assert no data-access entry with an empty Name that corresponds to
				// the GET SAME statement (heuristic: check that we don't have an
				// unexpected EdgeReads with empty Name near the GET SAME line).
				for _, e := range da {
					if e.Kind == model.EdgeReads && e.Name == "" {
						t.Errorf("Found EdgeReads with empty Name (should be skipped for GET SAME): %+v", e)
					}
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_recordWriteEmptyName",
			verify: func(t *testing.T, result model.FileAnalysis) {
				da := result.DataAccess
				if len(da) < 7 {
					t.Skip("not enough data-access entries")
				}

				// Record UPDATE/DELETE should produce EdgeWrites with empty Name.
				// Count them: expect 2 (UPDATE inside READ EMPLOYEES, DELETE inside READ LOCATIONS).
				emptyNameWrites := 0
				for _, e := range da {
					if e.Kind == model.EdgeWrites && e.Name == "" {
						emptyNameWrites++
					}
				}

				if emptyNameWrites != 2 {
					t.Errorf("record-write entries with empty Name: got %d, want 2 (UPDATE + DELETE)",
						emptyNameWrites)
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_definitionsCount",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Expected: 4 definitions (2 LOCAL + 2 PARAMETER; GLOBAL USING has no inline fields).
				defs := result.Definitions
				if len(defs) != 4 {
					t.Errorf("len(Definitions) = %d, want 4 (2 local + 2 parameter)", len(defs))
					for i, d := range defs {
						t.Logf("  Definitions[%d]: Name=%q, SectionKind=%q, Level=%d",
							i, d.Name, d.SectionKind, d.Level)
					}
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_definitionsSequence",
			verify: func(t *testing.T, result model.FileAnalysis) {
				defs := result.Definitions
				if len(defs) < 4 {
					t.Skip("not enough definitions to verify sequence")
				}

				// LOCAL section should come before PARAMETER (declaration order).
				// Expect: #COUNTER, #REPORT-NAME (both "local"), then #INPUT-FILE,
				// #OUTPUT-PATH (both "parameter").
				if defs[0].SectionKind != "local" {
					t.Errorf("Definitions[0].SectionKind = %q, want 'local'", defs[0].SectionKind)
				}
				if defs[1].SectionKind != "local" {
					t.Errorf("Definitions[1].SectionKind = %q, want 'local'", defs[1].SectionKind)
				}
				if defs[2].SectionKind != "parameter" {
					t.Errorf("Definitions[2].SectionKind = %q, want 'parameter'", defs[2].SectionKind)
				}
				if defs[3].SectionKind != "parameter" {
					t.Errorf("Definitions[3].SectionKind = %q, want 'parameter'", defs[3].SectionKind)
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_workFilesCount",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Expected: 2 work files (DEFINE WORK FILE 1 and 2).
				wf := result.WorkFiles
				if len(wf) != 2 {
					t.Errorf("len(WorkFiles) = %d, want 2", len(wf))
					for i, w := range wf {
						t.Logf("  WorkFiles[%d]: Number=%d, Name=%q", i, w.Number, w.Name)
					}
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_workFilesSequence",
			verify: func(t *testing.T, result model.FileAnalysis) {
				wf := result.WorkFiles
				if len(wf) < 2 {
					t.Skip("not enough work files to verify sequence")
				}

				// Expected: DEFINE WORK FILE 1 'REPORT.TXT', then 2 #LOGFILE.
				if wf[0].Number != 1 {
					t.Errorf("WorkFiles[0].Number = %d, want 1", wf[0].Number)
				}
				if wf[0].Name != "REPORT.TXT" {
					t.Errorf("WorkFiles[0].Name = %q, want 'REPORT.TXT'", wf[0].Name)
				}
				if wf[1].Number != 2 {
					t.Errorf("WorkFiles[1].Number = %d, want 2", wf[1].Number)
				}
				if wf[1].Name != "#LOGFILE" {
					t.Errorf("WorkFiles[1].Name = %q, want '#LOGFILE'", wf[1].Name)
				}
			},
		},
		{
			name: "Analyze_Integration_Combined_allChannelsPopulated",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// All three extraction channels must be populated and non-empty.
				if len(result.DataAccess) == 0 {
					t.Error("DataAccess channel is empty; want populated")
				}
				if len(result.Definitions) == 0 {
					t.Error("Definitions channel is empty; want populated")
				}
				if len(result.WorkFiles) == 0 {
					t.Error("WorkFiles channel is empty; want populated")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestAnalyze_Integration_Malformed verifies graceful degradation (Task 16 / FR-43):
// malformed statements surface as parser diagnostics while valid extractions are preserved.
// Extraction never crashes and produces no false edges; diagnostics and extraction stay
// on separate channels.
//
// Acceptance criteria (Task 16, FR-43):
//   - Parser diagnostics are emitted for malformed lines
//   - Valid data-access entries (READ, STORE, FIND) are still extracted
//   - Valid definitions from well-formed DEFINE DATA are still extracted
//   - Malformed lines produce no edges (extraction skips them)
//   - Extraction never crashes or panics
//   - No extraction-level diagnostic cross-contamination
//
// Fixture: testdata/dataaccess/07-malformed.NSP contains:
//   - Valid READ EMPLOYEES, STORE EMPLOYEES, FIND VEHICLES (should extract)
//   - Malformed READ (no target) — parser diagnostic, no edge
//   - Malformed DEFINE WORK FILE (no number) — parser diagnostic, no work-file entry
//   - Well-formed DEFINE DATA LOCAL section — should extract definitions
//
// Expected result: 3 valid data-access entries (READ, STORE, FIND), 2 valid definitions
// (LOCAL fields), >= 2 parser diagnostics (for malformed READ and DEFINE WORK FILE),
// all in separate channels.
func TestAnalyze_Integration_Malformed(t *testing.T) {
	// Read the malformed fixture from testdata/dataaccess/
	fixturePath := filepath.Join("testdata", "dataaccess", "07-malformed.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_Integration_Malformed_validExtractionsPreserved",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Valid data-access entries should still be extracted despite
				// malformed statements in the same file.
				da := result.DataAccess
				if len(da) == 0 {
					t.Fatal("DataAccess is empty; want valid entries (READ, STORE, FIND) preserved")
				}

				// Expected: at least 3 entries (READ EMPLOYEES, STORE EMPLOYEES, FIND VEHICLES).
				if len(da) < 3 {
					t.Errorf("len(DataAccess) = %d, want at least 3 (valid entries preserved)", len(da))
				}
			},
		},
		{
			name: "Analyze_Integration_Malformed_validDefinitionsPreserved",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Valid definitions from the well-formed DEFINE DATA LOCAL section
				// should still be extracted.
				defs := result.Definitions
				if len(defs) == 0 {
					t.Fatal("Definitions is empty; want entries from well-formed LOCAL section")
				}

				// Expected: at least 2 entries (#VALID-FIELD, #ANOTHER).
				if len(defs) < 2 {
					t.Errorf("len(Definitions) = %d, want at least 2", len(defs))
				}
			},
		},
		{
			name: "Analyze_Integration_Malformed_diagnosticsPresent",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// Malformed statements should produce diagnostics.
				diags := result.Diagnostics
				if len(diags) == 0 {
					t.Error("Diagnostics is empty; want diagnostics for malformed statements")
				}

				// Expected: at least 2 diagnostics (malformed READ, malformed DEFINE WORK FILE).
				if len(diags) < 2 {
					t.Errorf("len(Diagnostics) = %d, want at least 2", len(diags))
				}
			},
		},
		{
			name: "Analyze_Integration_Malformed_noFalseEdges",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// No false data-access entries should be created for malformed lines.
				// Verify that entries have proper names and sources.
				da := result.DataAccess
				for i, e := range da {
					// Each entry should have a valid source position (non-zero start line)
					if e.Source.Start.Line == 0 {
						t.Errorf("DataAccess[%d] has zero source line (likely malformed)", i)
					}

					// Verify that each entry corresponds to a valid statement:
					// - READ, STORE, FIND should have non-empty Name
					// - Empty Name is only valid for record-write entries (UPDATE/DELETE),
					//   which don't appear in this fixture
					if e.Name == "" {
						t.Errorf("DataAccess[%d] has empty Name; expect entries to have names (READ/STORE/FIND)",
							i)
					}
				}
			},
		},
		{
			name: "Analyze_Integration_Malformed_noPanic",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// This is implicit in the test's successful completion, but make it
				// explicit: Analyze must never panic, even with malformed input.
				// If we got here, no panic occurred (FR-43 graceful degradation).
				if result.AST == nil {
					t.Log("AST is nil (expected for malformed input)")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
		})
	}
}

// TestAnalyze_SQLExtraction_EndToEnd (Task 8 RED / Feature 08b, Story 0 — integration)
// verifies that Analyze wires SQL extraction (extractSQLAccess, extractSQLCalls,
// extractHostVarRefs) into the analysis pipeline and returns FileAnalysis with:
//   - DataAccess entries for SQL DDM reads (SELECT, PROCESS SQL)
//   - Edges entries for SQL-side call-like edges (CALLDBPROC)
//   - HostVarRefs entries for host-variable uses in SQL clauses (native + opaque)
//   - Merged DataAccess and Edges remain in global source order
//   - Feature-06/07/08 extraction unchanged (CALLNAT, feature-06 edges intact)
//
// This test is RED: Analyze does not yet call the SQL extractors, so the returned
// FileAnalysis.DataAccess, FileAnalysis.Edges, and FileAnalysis.HostVarRefs will not
// contain the expected SQL entries. The test documents what needs to be wired in Task 8.
//
// Acceptance criteria (Task 8, derived from KB fixture `kb_minimal.NSP`):
//   - Analyze(kb_minimal.NSP) returns FileAnalysis with:
//   - DataAccess containing EdgeReads for SQL-PERSONNEL (SELECT FROM)
//     and EMPLOYEE-DATA (PROCESS SQL DDM operand), merged with any
//     Adabas-data-access entries in global source order
//   - HostVarRefs containing #NAME, #SALARY, #PERS-ID (SELECT INTO/WHERE),
//     and :#NAME (PROCESS SQL opaque body) in source order
//   - Edges containing the 'NDBERR' CALLNAT (feature-06, unchanged)
//   - DataAccess and Edges slices each maintain non-decreasing source order
//     (stable sort on Source.Start.Line, then Source.Start.Column)
//   - Existing feature-06/07/08 tests remain green (no regression)
func TestAnalyze_SQLExtraction_EndToEnd(t *testing.T) {
	// Locate the fixture file
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	moduleRoot := findModuleRoot(t, thisFile)
	fixturePath := filepath.Join(moduleRoot, "internal/analysis/natural/testdata/sqlaccess/kb_minimal.NSP")

	// Read the fixture
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) failed: %v", fixturePath, err)
	}

	// Analyze the fixture
	a := New(nil)
	result, err := a.Analyze(fixturePath, content)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Verify AST was parsed successfully (not nil)
	if result.AST == nil {
		t.Fatal("Analyze returned nil AST; expected a non-nil Program")
	}

	// === DataAccess entries: SQL DDM reads ===
	// Expected: EdgeReads for SQL-PERSONNEL (line 13 SELECT) and EMPLOYEE-DATA (line 18 PROCESS SQL)
	// Task 8 RED: these entries will be absent because SQL extraction is not yet wired into Analyze
	var sqlReads []model.DataAccessEntry
	for _, entry := range result.DataAccess {
		if entry.Kind == model.EdgeReads && (entry.Name == "SQL-PERSONNEL" || entry.Name == "EMPLOYEE-DATA") {
			sqlReads = append(sqlReads, entry)
		}
	}

	if len(sqlReads) != 2 {
		t.Errorf("DataAccess contains %d SQL read entries, want 2 (SQL-PERSONNEL from SELECT, EMPLOYEE-DATA from PROCESS SQL)",
			len(sqlReads))
		t.Logf("Got %d total DataAccess entries:", len(result.DataAccess))
		for i, e := range result.DataAccess {
			t.Logf("  [%d]: Kind=%v, Name=%q, Source=(L%d:C%d)-(L%d:C%d)",
				i, e.Kind, e.Name,
				e.Source.Start.Line, e.Source.Start.Column,
				e.Source.End.Line, e.Source.End.Column)
		}
	} else {
		// Verify the names and basic properties
		for i, entry := range sqlReads {
			if entry.Name != "SQL-PERSONNEL" && entry.Name != "EMPLOYEE-DATA" {
				t.Errorf("sqlReads[%d].Name = %q, want SQL-PERSONNEL or EMPLOYEE-DATA", i, entry.Name)
			}
			if entry.Kind != model.EdgeReads {
				t.Errorf("sqlReads[%d].Kind = %v, want EdgeReads", i, entry.Kind)
			}
			// NameRange should be non-zero (on the table-name token)
			if entry.NameRange.Start == entry.NameRange.End {
				t.Errorf("sqlReads[%d].NameRange is zero, want non-zero on table-name token", i)
			}
			// Source should be non-zero (on the full statement)
			if entry.Source.Start == entry.Source.End {
				t.Errorf("sqlReads[%d].Source is zero, want non-zero on statement range", i)
			}
		}
	}

	// === HostVarRefs: host-variable uses in SQL ===
	// Expected: #NAME, #SALARY, #PERS-ID (from SELECT INTO/WHERE), :#NAME (from PROCESS SQL opaque body)
	// Task 8 RED: these entries will be absent
	wantHostVarNames := []string{"#NAME", "#SALARY", "#PERS-ID", "#NAME"} // Last one from opaque body

	if len(result.HostVarRefs) == 0 {
		t.Error("HostVarRefs is empty, want 4 host-variable references")
	} else if len(result.HostVarRefs) < 4 {
		t.Errorf("HostVarRefs has %d entries, want at least 4", len(result.HostVarRefs))
		t.Logf("Got HostVarRefs:")
		for i, r := range result.HostVarRefs {
			t.Logf("  [%d]: Name=%q, Range=(L%d:C%d)-(L%d:C%d)",
				i, r.Name,
				r.Range.Start.Line, r.Range.Start.Column,
				r.Range.End.Line, r.Range.End.Column)
		}
	} else {
		// Verify the first 4 entries match the expected names
		for i := 0; i < 4 && i < len(result.HostVarRefs); i++ {
			if result.HostVarRefs[i].Name != wantHostVarNames[i] {
				t.Errorf("HostVarRefs[%d].Name = %q, want %q", i, result.HostVarRefs[i].Name, wantHostVarNames[i])
			}
			if result.HostVarRefs[i].Range.Start == result.HostVarRefs[i].Range.End {
				t.Errorf("HostVarRefs[%d].Range is zero, want non-zero", i)
			}
		}
	}

	// === Edges: call-like relationships ===
	// Expected: CALLNAT 'NDBERR' (feature-06 edge, unchanged), possibly CALLDBPROC (Task 6b, future)
	// Task 8 RED: CALLDBPROC will be absent; CALLNAT should still be present
	var callnatEdges []model.EdgeEntry
	for _, entry := range result.Edges {
		if entry.TargetName == "NDBERR" {
			callnatEdges = append(callnatEdges, entry)
		}
	}

	if len(callnatEdges) != 1 {
		t.Errorf("Edges contains %d 'NDBERR' CALLNAT edges, want 1 (feature-06 regression check)",
			len(callnatEdges))
	} else {
		if callnatEdges[0].Kind != model.EdgeCalls && callnatEdges[0].Kind != model.EdgeCallsDynamic {
			t.Errorf("NDBERR edge Kind = %v, want EdgeCalls or EdgeCallsDynamic", callnatEdges[0].Kind)
		}
	}

	// === Source ordering invariant ===
	// Verify that both DataAccess and Edges are in non-decreasing source order (if they have entries)
	if len(result.DataAccess) > 1 {
		for i := 1; i < len(result.DataAccess); i++ {
			prev := result.DataAccess[i-1].Source.Start
			curr := result.DataAccess[i].Source.Start
			if prev.Line > curr.Line || (prev.Line == curr.Line && prev.Column > curr.Column) {
				t.Errorf("DataAccess not in source order: entry[%d] (L%d:C%d) after entry[%d] (L%d:C%d)",
					i-1, prev.Line, prev.Column, i, curr.Line, curr.Column)
				break
			}
		}
	}

	if len(result.Edges) > 1 {
		for i := 1; i < len(result.Edges); i++ {
			prev := result.Edges[i-1].Source.Start
			curr := result.Edges[i].Source.Start
			if prev.Line > curr.Line || (prev.Line == curr.Line && prev.Column > curr.Column) {
				t.Errorf("Edges not in source order: entry[%d] (L%d:C%d) after entry[%d] (L%d:C%d)",
					i-1, prev.Line, prev.Column, i, curr.Line, curr.Column)
				break
			}
		}
	}
}

// TestAnalyze_StructurePopulated_EndToEnd (Task 5 RED / Feature 09)
// verifies that Analyze wires extractStructure into the analysis pipeline and
// returns FileAnalysis with Structure populated for parsed objects.
//
// Acceptance criteria (Task 5 / FR-23):
//   - Analyze(01-program-full.NSP) returns FileAnalysis with Structure != nil
//   - Structure.Kind = SymbolObject, Structure.Name = "01-program-full"
//   - Structure.Range spans the entire program (from prog.StartPos to prog.EndPos)
//   - Structure.Children contains expected child kinds in source order:
//     at least one SymbolDataSection (LOCAL), at least one SymbolSubroutine,
//     at least one SymbolMap
//   - Files with ast==nil (unknown extension, empty content) yield Structure==nil
//
// Fixture: testdata/structure/01-program-full.NSP (T2 fixture) contains:
//   - DEFINE DATA LOCAL with a group + REDEFINE nested field
//   - DEFINE MAP 'EMPSCREEN' with fields
//   - DEFINE SUBROUTINE PROCESS-EMP
//
// This test is RED: Analyze does not yet call extractStructure, so Structure
// will remain nil even though the AST is successfully parsed.
func TestAnalyze_StructurePopulated_EndToEnd(t *testing.T) {
	// Read the T2 fixture from testdata/structure/
	fixturePath := filepath.Join("testdata", "structure", "01-program-full.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Call Analyze through the public interface
	var a analysis.Analyzer = New(nil)
	result, err := a.Analyze(fixturePath, content)

	// Assert no error (graceful degradation per FR-43)
	if err != nil {
		t.Errorf("Analyze(%q, …) error = %v, want nil", fixturePath, err)
	}

	tests := []struct {
		name   string
		verify func(t *testing.T, result model.FileAnalysis)
	}{
		{
			name: "Analyze_StructurePopulated_structureNotNil",
			verify: func(t *testing.T, result model.FileAnalysis) {
				// FR-23: Structure must be populated (not nil) for a parsed object
				if result.Structure == nil {
					t.Fatal("FileAnalysis.Structure is nil; want populated Structure from 01-program-full.NSP")
				}
			},
		},
		{
			name: "Analyze_StructurePopulated_rootKind",
			verify: func(t *testing.T, result model.FileAnalysis) {
				if result.Structure == nil {
					t.Skip("Structure is nil")
				}

				// Root must have Kind = SymbolObject
				if result.Structure.Kind != model.SymbolObject {
					t.Errorf("Structure.Kind = %q, want %q (SymbolObject)", result.Structure.Kind, model.SymbolObject)
				}
			},
		},
		{
			name: "Analyze_StructurePopulated_rootName",
			verify: func(t *testing.T, result model.FileAnalysis) {
				if result.Structure == nil {
					t.Skip("Structure is nil")
				}

				// Root name must be derived from the filename (01-program-full.NSP → 01-program-full)
				if result.Structure.Name != "01-program-full" {
					t.Errorf("Structure.Name = %q, want '01-program-full' (from filename without extension)",
						result.Structure.Name)
				}
			},
		},
		{
			name: "Analyze_StructurePopulated_rootRange",
			verify: func(t *testing.T, result model.FileAnalysis) {
				if result.Structure == nil {
					t.Skip("Structure is nil")
				}

				// Root range must be non-zero (spanning the entire program)
				if result.Structure.Range.Start.Line == 0 && result.Structure.Range.Start.Column == 0 &&
					result.Structure.Range.End.Line == 0 && result.Structure.Range.End.Column == 0 {
					t.Error("Structure.Range is zero; want non-zero span of entire program")
				}
			},
		},
		{
			name: "Analyze_StructurePopulated_hasChildren",
			verify: func(t *testing.T, result model.FileAnalysis) {
				if result.Structure == nil {
					t.Skip("Structure is nil")
				}

				// Root must have children (data sections, subroutines, maps)
				if len(result.Structure.Children) == 0 {
					t.Error("Structure.Children is empty; want children (sections, subroutines, maps)")
				}
			},
		},
		{
			name: "Analyze_StructurePopulated_childKinds",
			verify: func(t *testing.T, result model.FileAnalysis) {
				if result.Structure == nil || len(result.Structure.Children) == 0 {
					t.Skip("Structure or Children is empty")
				}

				// Verify that children contain at least one of each expected kind
				hasDataSection := false
				hasSubroutine := false
				hasMap := false

				for _, child := range result.Structure.Children {
					if child.Kind == model.SymbolDataSection {
						hasDataSection = true
					}
					if child.Kind == model.SymbolSubroutine {
						hasSubroutine = true
					}
					if child.Kind == model.SymbolMap {
						hasMap = true
					}
				}

				if !hasDataSection {
					t.Error("Children do not contain SymbolDataSection; want at least one from LOCAL section")
				}
				if !hasSubroutine {
					t.Error("Children do not contain SymbolSubroutine; want at least one from DEFINE SUBROUTINE")
				}
				if !hasMap {
					t.Error("Children do not contain SymbolMap; want at least one from DEFINE MAP")
				}
			},
		},
		{
			name: "Analyze_StructurePopulated_childrenSourceOrdered",
			verify: func(t *testing.T, result model.FileAnalysis) {
				if result.Structure == nil || len(result.Structure.Children) < 2 {
					t.Skip("not enough children to verify order")
				}

				// Verify children are in source order (non-decreasing line/column)
				for i := 1; i < len(result.Structure.Children); i++ {
					prevStart := result.Structure.Children[i-1].Range.Start
					currStart := result.Structure.Children[i].Range.Start
					if prevStart.Line > currStart.Line ||
						(prevStart.Line == currStart.Line && prevStart.Column > currStart.Column) {
						t.Errorf("Children[%d] (L%d:C%d) not in source order after Children[%d] (L%d:C%d)",
							i-1, prevStart.Line, prevStart.Column, i, currStart.Line, currStart.Column)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, result)
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
