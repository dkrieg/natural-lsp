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
