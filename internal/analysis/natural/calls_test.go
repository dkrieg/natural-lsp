package natural

import (
	"os"
	"path/filepath"
	"testing"

	"natural-lsp/internal/model"
)

// TestExtractEdges_CallnatStatic verifies that CALLNAT with literal targets
// are extracted as static call edges (Task 2 / FR-10, M-3).
//
// Acceptance criteria:
//   - Emit exactly one EdgeCalls edge for each CALLNAT 'LITERAL' statement
//   - TargetName is the unquoted literal value
//   - Source is the statement range
//   - Target is the operand range
//   - Edges are in source order
//   - Zero edges are produced for non-call statements (DEFINE DATA, WRITE, MOVE)
func TestExtractEdges_CallnatStatic(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "calls", "01-callnat-static.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor (stub function for RED phase)
	edges := extractEdges(prog)

	// Test table-driven assertions (AAA)
	tests := []struct {
		name      string
		wantCount int
		verify    func(t *testing.T, edges []model.EdgeEntry)
	}{
		{
			name:      "extractEdges_CallnatStatic_exactlyTwoEdges",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry) {
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2", len(edges))
				}

				// Assert first edge: CALLNAT 'A'
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgeCalls {
						t.Errorf("edges[0].Kind = %s, want %s", edge0.Kind, model.EdgeCalls)
					}
					if edge0.TargetName != "A" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "A")
					}
					// Source must be non-zero (call-site range)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero call-site range")
					}
					// Target must be non-zero (operand range)
					if edge0.Target.Start.Line == 0 && edge0.Target.Start.Column == 0 &&
						edge0.Target.End.Line == 0 && edge0.Target.End.Column == 0 {
						t.Error("edges[0].Target is zero, want non-zero operand range")
					}
				}

				// Assert second edge: CALLNAT 'B'
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgeCalls {
						t.Errorf("edges[1].Kind = %s, want %s", edge1.Kind, model.EdgeCalls)
					}
					if edge1.TargetName != "B" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "B")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero call-site range")
					}
					// Target must be non-zero
					if edge1.Target.Start.Line == 0 && edge1.Target.Start.Column == 0 &&
						edge1.Target.End.Line == 0 && edge1.Target.End.Column == 0 {
						t.Error("edges[1].Target is zero, want non-zero operand range")
					}
				}

				// Assert source order: edge0 comes before edge1
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
				}
			},
		},
		{
			name:      "extractEdges_CallnatStatic_noFalseEdgesFromNonCallLines",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry) {
				// DEFINE DATA, WRITE, and MOVE should produce zero edges
				// (already verified by count == 2, but document the intent)
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d; expected only 2 edges from CALLNAT calls, zero from DEFINE/WRITE/MOVE", len(edges))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if len(edges) != tc.wantCount {
				t.Errorf("len(edges) = %d, want %d", len(edges), tc.wantCount)
			}
			tc.verify(t, edges)
		})
	}
}

// TestExtractEdges_CallnatDynamic verifies that CALLNAT with variable targets
// are extracted as dynamic call edges (Task 3 / FR-11, FR-17, M-6).
//
// Acceptance criteria:
//   - Emit exactly one EdgeCallsDynamic edge for each CALLNAT #VARIABLE statement
//   - TargetName is the variable/expression text (e.g. "#PROGNAME")
//   - Source is the statement range (call-site context preserved)
//   - Static and dynamic edges coexist in the same file
//   - No diagnostic is produced for the variable target (channel separation: FR-17, M-6)
func TestExtractEdges_CallnatDynamic(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "calls", "02-callnat-dynamic.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	edges := extractEdges(prog)

	// Test table-driven assertions (AAA)
	tests := []struct {
		name      string
		wantCount int
		verify    func(t *testing.T, edges []model.EdgeEntry, prog *Program)
	}{
		{
			name:      "extractEdges_CallnatDynamic_exactlyTwoEdges_oneStaticOneDynamic",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2 (one static + one dynamic)", len(edges))
				}

				// Assert first edge: CALLNAT #PROGNAME (dynamic)
				// In source order, #PROGNAME (line ~10) comes before 'STATIC' (line ~12)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgeCallsDynamic {
						t.Errorf("edges[0].Kind = %s, want %s (dynamic CALLNAT)", edge0.Kind, model.EdgeCallsDynamic)
					}
					if edge0.TargetName != "#PROGNAME" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "#PROGNAME")
					}
					// Source must be non-zero (call-site range with caller context)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero call-site range (caller context)")
					}
				}

				// Assert second edge: CALLNAT 'STATIC' (static)
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgeCalls {
						t.Errorf("edges[1].Kind = %s, want %s (static CALLNAT)", edge1.Kind, model.EdgeCalls)
					}
					if edge1.TargetName != "STATIC" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "STATIC")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero call-site range")
					}
				}

				// Assert source order: dynamic call (#PROGNAME) at line ~10, static call ('STATIC') at line ~12
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
				}
			},
		},
		{
			name:      "extractEdges_CallnatDynamic_noDiagnosticForVariableTarget",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// CRITICAL: A variable target must NOT produce a diagnostic.
				// It is a modeled outcome (EdgeCallsDynamic), not a parse error.
				// Channel separation (FR-17, M-6): edges and diagnostics are distinct.
				if len(prog.Diagnostics) > 0 {
					t.Errorf("prog.Diagnostics = %v; want empty for valid program with variable target",
						prog.Diagnostics)
					for i, diag := range prog.Diagnostics {
						t.Logf("  diag[%d]: %q at %v", i, diag.Message, diag.Range)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if len(edges) != tc.wantCount {
				t.Errorf("len(edges) = %d, want %d", len(edges), tc.wantCount)
			}
			tc.verify(t, edges, prog)
		})
	}
}

// TestExtractEdges_Perform verifies that PERFORM statements are extracted as
// PERFORMS edges with caller context (Task 4 / FR-12).
//
// Acceptance criteria:
//   - Emit exactly one EdgePerforms edge for each PERFORM statement
//   - TargetName is the subroutine name (identifiers only, never literals)
//   - Source is the PERFORM statement range (caller context preserved)
//   - Target follows Task 5's finalized PERFORM convention: the inline DEFINE SUBROUTINE
//     definition range if the target matches an in-file definition; otherwise the zero Range
//     (unresolved, deferred to resolution feature)
//   - Edges are in source order
//   - Zero edges are produced for non-PERFORM statements (DEFINE DATA, MOVE, WRITE)
//
// This test uses fixture 03-perform.NSP which contains NO inline DEFINE SUBROUTINE blocks.
// Both PERFORM targets (CHECK-INPUT, PROCESS-RECORD) are external only and must have
// Target == model.Range{} (zero range). See Task 5 (inline-subroutine identification).
func TestExtractEdges_Perform(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "calls", "03-perform.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor (stub function for RED phase)
	edges := extractEdges(prog)

	// Test table-driven assertions (AAA)
	tests := []struct {
		name      string
		wantCount int
		verify    func(t *testing.T, edges []model.EdgeEntry)
	}{
		{
			name:      "extractEdges_Perform_exactlyTwoEdges",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry) {
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2", len(edges))
				}

				// Assert first edge: PERFORM CHECK-INPUT (external only, no inline def)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgePerforms {
						t.Errorf("edges[0].Kind = %s, want %s", edge0.Kind, model.EdgePerforms)
					}
					if edge0.TargetName != "CHECK-INPUT" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "CHECK-INPUT")
					}
					// Source must be non-zero (PERFORM statement range with caller context)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range (caller context)")
					}
					// Target MUST be zero (Task 5: external-only PERFORM has zero Target, unresolved)
					if edge0.Target.Start.Line != 0 || edge0.Target.Start.Column != 0 ||
						edge0.Target.End.Line != 0 || edge0.Target.End.Column != 0 {
						t.Errorf("edges[0].Target = %v, want zero Range (external-only target per Task 5 finalized convention)", edge0.Target)
					}
				}

				// Assert second edge: PERFORM PROCESS-RECORD (external only, no inline def)
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgePerforms {
						t.Errorf("edges[1].Kind = %s, want %s", edge1.Kind, model.EdgePerforms)
					}
					if edge1.TargetName != "PROCESS-RECORD" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "PROCESS-RECORD")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero statement range")
					}
					// Target MUST be zero (Task 5: external-only PERFORM has zero Target, unresolved)
					if edge1.Target.Start.Line != 0 || edge1.Target.Start.Column != 0 ||
						edge1.Target.End.Line != 0 || edge1.Target.End.Column != 0 {
						t.Errorf("edges[1].Target = %v, want zero Range (external-only target per Task 5 finalized convention)", edge1.Target)
					}
				}

				// Assert source order: CHECK-INPUT before PROCESS-RECORD
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
				}
			},
		},
		{
			name:      "extractEdges_Perform_noFalseEdgesFromNonPerformLines",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry) {
				// DEFINE DATA, MOVE, and WRITE should produce zero edges
				// (already verified by count == 2, but document the intent)
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d; expected only 2 edges from PERFORM calls, zero from DEFINE/MOVE/WRITE", len(edges))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if len(edges) != tc.wantCount {
				t.Errorf("len(edges) = %d, want %d", len(edges), tc.wantCount)
			}
			tc.verify(t, edges)
		})
	}
}

// TestExtractEdges_PerformInlineMarking verifies that PERFORM targets are marked
// when they match an inline DEFINE SUBROUTINE in the same file (Task 5 / FR-12, M-4).
//
// Acceptance criteria (Task 5, DECISION 2, user-approved):
//   - PERFORM target that matches an in-file DEFINE SUBROUTINE is marked in-file-resolved:
//     Target = the definition's range (start/end of the DEFINE SUBROUTINE node).
//   - PERFORM target with no in-file definition is left unresolved: Target = zero Range.
//   - Two fixtures: one with inline def (04-perform-inline-and-external.NSP) showing
//     both inline-matched and external-only targets; one without inline def (04b-perform-no-inline.NSP)
//     proving the inline-marking is data-driven.
//   - The inline-before-external precedence is representable for the resolution feature.
func TestExtractEdges_PerformInlineMarking(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		verifyFunc func(t *testing.T, edges []model.EdgeEntry, prog *Program)
	}{
		{
			name:    "extractEdges_PerformInlineMarking_inlineAndExternalTargets",
			fixture: "04-perform-inline-and-external.NSP",
			verifyFunc: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Fixture contains two PERFORM statements:
				// 1. PERFORM SHARED-LOGIC (matches DEFINE SUBROUTINE SHARED-LOGIC)
				// 2. PERFORM EXTERNAL-ONLY (no inline definition)

				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2 (one inline-matched, one external-only)", len(edges))
				}

				// First edge: PERFORM SHARED-LOGIC (matches inline definition)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgePerforms {
						t.Errorf("edges[0].Kind = %s, want %s", edge0.Kind, model.EdgePerforms)
					}
					if edge0.TargetName != "SHARED-LOGIC" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "SHARED-LOGIC")
					}
					// Source must be non-zero (call-site range with caller context)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range (caller context)")
					}

					// Target MUST be non-zero and match the DEFINE SUBROUTINE definition range
					// (proof that inline matching occurred)
					if edge0.Target.Start.Line == 0 && edge0.Target.Start.Column == 0 &&
						edge0.Target.End.Line == 0 && edge0.Target.End.Column == 0 {
						t.Error("edges[0].Target is zero, want non-zero range pointing to DEFINE SUBROUTINE SHARED-LOGIC definition")
					}

					// Verify the target range actually points to a subroutine in the AST
					// Find the SHARED-LOGIC subroutine definition
					var foundDef *Subroutine
					for _, sub := range prog.Subroutines {
						if sub.Name == "SHARED-LOGIC" {
							foundDef = sub
							break
						}
					}
					if foundDef == nil {
						t.Error("DEFINE SUBROUTINE SHARED-LOGIC not found in AST; inline definition must be parsed")
					} else {
						// Target range should match the definition's range
						defStart, defEnd := foundDef.Position()
						if edge0.Target.Start != defStart || edge0.Target.End != defEnd {
							t.Errorf("edges[0].Target = {%v, %v}, want definition range {%v, %v}",
								edge0.Target.Start, edge0.Target.End, defStart, defEnd)
						}
					}
				}

				// Second edge: PERFORM EXTERNAL-ONLY (no inline definition)
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgePerforms {
						t.Errorf("edges[1].Kind = %s, want %s", edge1.Kind, model.EdgePerforms)
					}
					if edge1.TargetName != "EXTERNAL-ONLY" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "EXTERNAL-ONLY")
					}
					// Source must be non-zero (call-site range)
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero statement range")
					}

					// Target MUST be zero (unresolved, deferred to external resolution)
					if edge1.Target.Start.Line != 0 || edge1.Target.Start.Column != 0 ||
						edge1.Target.End.Line != 0 || edge1.Target.End.Column != 0 {
						t.Errorf("edges[1].Target = %v, want zero Range (external-only, unresolved)", edge1.Target)
					}
				}

				// Assert source order: SHARED-LOGIC before EXTERNAL-ONLY
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
				}
			},
		},
		{
			name:    "extractEdges_PerformInlineMarking_noInlineDefMarksAsUnresolved",
			fixture: "04b-perform-no-inline.NSP",
			verifyFunc: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Fixture is the same as 04-perform-inline-and-external.NSP but WITHOUT
				// the DEFINE SUBROUTINE block. This proves inline-marking is data-driven:
				// both PERFORM statements have no inline definitions and must have
				// Target == zero Range (unresolved).

				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2", len(edges))
				}

				// First edge: PERFORM SHARED-LOGIC (no inline def in this fixture)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgePerforms {
						t.Errorf("edges[0].Kind = %s, want %s", edge0.Kind, model.EdgePerforms)
					}
					if edge0.TargetName != "SHARED-LOGIC" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "SHARED-LOGIC")
					}
					// Source must be non-zero
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range")
					}

					// Target MUST be zero (no inline def in this fixture; unresolved)
					if edge0.Target.Start.Line != 0 || edge0.Target.Start.Column != 0 ||
						edge0.Target.End.Line != 0 || edge0.Target.End.Column != 0 {
						t.Errorf("edges[0].Target = %v, want zero Range (no inline definition present)", edge0.Target)
					}
				}

				// Second edge: PERFORM EXTERNAL-ONLY (still no inline def)
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgePerforms {
						t.Errorf("edges[1].Kind = %s, want %s", edge1.Kind, model.EdgePerforms)
					}
					if edge1.TargetName != "EXTERNAL-ONLY" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "EXTERNAL-ONLY")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero statement range")
					}

					// Target MUST be zero (no inline def; unresolved)
					if edge1.Target.Start.Line != 0 || edge1.Target.Start.Column != 0 ||
						edge1.Target.End.Line != 0 || edge1.Target.End.Column != 0 {
						t.Errorf("edges[1].Target = %v, want zero Range (no inline definition present)", edge1.Target)
					}
				}

				// Assert no subroutine definitions in the AST (verifying fixture construction)
				if len(prog.Subroutines) > 0 {
					t.Errorf("prog.Subroutines has %d entries, want 0 (fixture should not define any inline subroutines)",
						len(prog.Subroutines))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Read the fixture
			content, err := os.ReadFile(filepath.Join("testdata", "calls", tc.fixture))
			if err != nil {
				t.Fatalf("Failed to read fixture %s: %v", tc.fixture, err)
			}

			// Parse to AST
			lexer := NewLexer(string(content))
			parser := NewParser(lexer)
			prog, err := parser.Parse()

			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}
			if err != nil {
				t.Logf("Parse returned error %v; expected graceful degradation", err)
			}

			// Call the extractor
			edges := extractEdges(prog)

			// Run the verify function
			tc.verifyFunc(t, edges, prog)
		})
	}
}

// TestExtractEdges_Include verifies that INCLUDE statements are extracted as
// INCLUDES edges with caller context (Task 6 / FR-13).
//
// Acceptance criteria:
//   - Emit exactly one EdgeIncludes edge for each INCLUDE statement
//   - TargetName is the copycode name (unquoted)
//   - Source is the INCLUDE statement range (caller context preserved)
//   - Target is the operand range (the copycode target)
//   - Both quoted ('COMMON-DECLS') and unquoted (ERRHANDLER) forms are supported
//   - Copycode targets are treated as literal names regardless of quoting
//   - Edges are in source order
//   - Zero edges are produced for non-INCLUDE statements (DEFINE DATA, MOVE)
//   - Note: incremental re-analysis on copycode change is already handled by
//     workspace/index.go:Invalidate (it walks EdgeIncludes edges), so this feature
//     only needs to emit the edges for that machinery to work.
func TestExtractEdges_Include(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "calls", "05-include.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	edges := extractEdges(prog)

	// Test table-driven assertions (AAA)
	tests := []struct {
		name      string
		wantCount int
		verify    func(t *testing.T, edges []model.EdgeEntry)
	}{
		{
			name:      "extractEdges_Include_exactlyTwoEdges",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry) {
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2", len(edges))
				}

				// Assert first edge: INCLUDE 'COMMON-DECLS' (quoted form)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgeIncludes {
						t.Errorf("edges[0].Kind = %s, want %s", edge0.Kind, model.EdgeIncludes)
					}
					if edge0.TargetName != "COMMON-DECLS" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "COMMON-DECLS")
					}
					// Source must be non-zero (INCLUDE statement range)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range")
					}
					// Target must be non-zero (operand range — the copycode target)
					if edge0.Target.Start.Line == 0 && edge0.Target.Start.Column == 0 &&
						edge0.Target.End.Line == 0 && edge0.Target.End.Column == 0 {
						t.Error("edges[0].Target is zero, want non-zero operand range")
					}
				}

				// Assert second edge: INCLUDE ERRHANDLER (unquoted form)
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgeIncludes {
						t.Errorf("edges[1].Kind = %s, want %s", edge1.Kind, model.EdgeIncludes)
					}
					if edge1.TargetName != "ERRHANDLER" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "ERRHANDLER")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero statement range")
					}
					// Target must be non-zero
					if edge1.Target.Start.Line == 0 && edge1.Target.Start.Column == 0 &&
						edge1.Target.End.Line == 0 && edge1.Target.End.Column == 0 {
						t.Error("edges[1].Target is zero, want non-zero operand range")
					}
				}

				// Assert source order: 'COMMON-DECLS' before ERRHANDLER
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
				}
			},
		},
		{
			name:      "extractEdges_Include_noFalseEdgesFromNonIncludeLines",
			wantCount: 2,
			verify: func(t *testing.T, edges []model.EdgeEntry) {
				// DEFINE DATA, MOVE should produce zero edges
				// (already verified by count == 2, but document the intent)
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d; expected only 2 edges from INCLUDE statements, zero from DEFINE/MOVE", len(edges))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if len(edges) != tc.wantCount {
				t.Errorf("len(edges) = %d, want %d", len(edges), tc.wantCount)
			}
			tc.verify(t, edges)
		})
	}
}

// TestExtractEdges_FetchRun verifies that FETCH and RUN statements are extracted as
// navigation edges with static vs dynamic classification and library marking
// (Task 7 / FR-14, FR-15, Story 5).
//
// Acceptance criteria:
//   - FETCH 'LITERAL' → static EdgeNavigatesTo (not dynamic, not error)
//   - FETCH #VARIABLE → EdgeNavigatesToDynamic (not static, not error)
//   - RUN 'LITERAL' → static EdgeNavigatesTo
//   - RUN 'LITERAL' 'LIBRARY' → EdgeNavigatesTo with Library field populated
//   - No diagnostic is produced for dynamic FETCH (channel separation: FR-15, FR-17)
//   - FETCH has NO library field on the edge (FETCH operand2 is a stack parameter, not a library)
//   - Source order is preserved (FETCH/RUN order in source matches edge order)
//
// Fixtures:
//   - 06-fetch-run.NSP: FETCH 'RPT001', FETCH #DYNRPT, RUN 'BATCHJOB'
//   - 07-run-library.NSP: RUN 'BATCHJOB' 'MYLIB'
func TestExtractEdges_FetchRun(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		verifyFunc func(t *testing.T, edges []model.EdgeEntry, prog *Program)
	}{
		{
			name:    "extractEdges_FetchRun_staticAndDynamic",
			fixture: "06-fetch-run.NSP",
			verifyFunc: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Fixture contains three navigation edges in source order:
				// 1. FETCH 'RPT001' (static)
				// 2. FETCH #DYNRPT (dynamic)
				// 3. RUN 'BATCHJOB' (static)

				if len(edges) != 3 {
					t.Errorf("len(edges) = %d, want 3 (two FETCH + one RUN)", len(edges))
				}

				// First edge: FETCH 'RPT001' (static)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgeNavigatesTo {
						t.Errorf("edges[0].Kind = %s, want %s (static FETCH)", edge0.Kind, model.EdgeNavigatesTo)
					}
					if edge0.TargetName != "RPT001" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "RPT001")
					}
					// Source must be non-zero (FETCH statement range with caller context)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range (caller context)")
					}
					// FETCH must NOT have a Library field (FETCH operand2 is a parameter, not a library)
					if edge0.Library != "" {
						t.Errorf("edges[0].Library = %q, want empty (FETCH has no library qualifier)", edge0.Library)
					}
				}

				// Second edge: FETCH #DYNRPT (dynamic)
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgeNavigatesToDynamic {
						t.Errorf("edges[1].Kind = %s, want %s (dynamic FETCH)", edge1.Kind, model.EdgeNavigatesToDynamic)
					}
					if edge1.TargetName != "#DYNRPT" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "#DYNRPT")
					}
					// Source must be non-zero (FETCH statement range with caller context)
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero statement range (caller context)")
					}
					// Dynamic FETCH also must NOT have Library
					if edge1.Library != "" {
						t.Errorf("edges[1].Library = %q, want empty (FETCH has no library qualifier)", edge1.Library)
					}
				}

				// Third edge: RUN 'BATCHJOB' (static)
				if len(edges) > 2 {
					edge2 := edges[2]
					if edge2.Kind != model.EdgeNavigatesTo {
						t.Errorf("edges[2].Kind = %s, want %s (static RUN)", edge2.Kind, model.EdgeNavigatesTo)
					}
					if edge2.TargetName != "BATCHJOB" {
						t.Errorf("edges[2].TargetName = %q, want %q", edge2.TargetName, "BATCHJOB")
					}
					// Source must be non-zero (RUN statement range)
					if edge2.Source.Start.Line == 0 && edge2.Source.Start.Column == 0 &&
						edge2.Source.End.Line == 0 && edge2.Source.End.Column == 0 {
						t.Error("edges[2].Source is zero, want non-zero statement range")
					}
					// RUN without library has empty Library field
					if edge2.Library != "" {
						t.Errorf("edges[2].Library = %q, want empty (RUN without library-id)", edge2.Library)
					}
				}

				// Assert source order: FETCH 'RPT001' → FETCH #DYNRPT → RUN 'BATCHJOB'
				if len(edges) >= 3 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					edge2Start := edges[2].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
					if edge1Start >= edge2Start {
						t.Errorf("edges not in source order: edge[1] at line %d, edge[2] at line %d",
							edge1Start, edge2Start)
					}
				}
			},
		},
		{
			name:    "extractEdges_FetchRun_noDiagnosticForDynamicFetch",
			fixture: "06-fetch-run.NSP",
			verifyFunc: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// CRITICAL: A dynamic FETCH target must NOT produce a diagnostic.
				// It is a modeled outcome (EdgeNavigatesToDynamic), not a parse error.
				// Channel separation (FR-15, FR-17, M-6): edges and diagnostics are distinct.
				if len(prog.Diagnostics) > 0 {
					t.Errorf("prog.Diagnostics = %v; want empty for valid program with dynamic FETCH",
						prog.Diagnostics)
					for i, diag := range prog.Diagnostics {
						t.Logf("  diag[%d]: %q at %v", i, diag.Message, diag.Range)
					}
				}
			},
		},
		{
			name:    "extractEdges_FetchRun_runWithLibrary",
			fixture: "07-run-library.NSP",
			verifyFunc: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Fixture contains one RUN edge with library-id: RUN 'BATCHJOB' 'MYLIB'

				if len(edges) != 1 {
					t.Errorf("len(edges) = %d, want 1 (one RUN with library)", len(edges))
				}

				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgeNavigatesTo {
						t.Errorf("edges[0].Kind = %s, want %s (static RUN)", edge0.Kind, model.EdgeNavigatesTo)
					}
					if edge0.TargetName != "BATCHJOB" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "BATCHJOB")
					}
					// Source must be non-zero (RUN statement range)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range")
					}
					// RUN with library-id has Library field populated
					if edge0.Library != "MYLIB" {
						t.Errorf("edges[0].Library = %q, want %q (library-id from RUN statement)", edge0.Library, "MYLIB")
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Read the fixture
			content, err := os.ReadFile(filepath.Join("testdata", "calls", tc.fixture))
			if err != nil {
				t.Fatalf("Failed to read fixture %s: %v", tc.fixture, err)
			}

			// Parse to AST
			lexer := NewLexer(string(content))
			parser := NewParser(lexer)
			prog, err := parser.Parse()

			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}
			if err != nil {
				t.Logf("Parse returned error %v; expected graceful degradation", err)
			}

			// Call the extractor
			edges := extractEdges(prog)

			// Run the verify function
			tc.verifyFunc(t, edges, prog)
		})
	}
}

// TestExtractEdges_PlaceholderLiteral verifies that literal call targets
// containing runtime-substitution placeholders are downgraded to dynamic edges
// (Task 8 / FR-18).
//
// Acceptance criteria:
//   - A literal CALLNAT/FETCH/RUN target containing '&' is downgraded to the dynamic
//     edge kind (EdgeCallsDynamic for CALLNAT, EdgeNavigatesToDynamic for FETCH/RUN),
//     preserving caller context.
//   - A clean literal target (no '&') remains static.
//   - The placeholder target is represented as a dynamic edge with TargetName =
//     the literal value including the placeholder (e.g., "PRG&LANG").
//   - No false static edge is produced (i.e., exactly one dynamic edge for the
//     placeholder, no second static edge to the raw text).
//   - No diagnostic is produced for the placeholder target (it is a modeled outcome,
//     not a parse error — channel separation FR-18, M-6).
//
// Fixture: 08-placeholder-literal.NSP contains:
//   - CALLNAT 'PRG&LANG' (placeholder literal, should be downgraded to dynamic)
//   - CALLNAT 'PLAINPROG' (clean literal, stays static)
func TestExtractEdges_PlaceholderLiteral(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "calls", "08-placeholder-literal.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Parse to AST
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; expected graceful degradation", err)
	}

	// Call the extractor
	edges := extractEdges(prog)

	// Test table-driven assertions (AAA)
	tests := []struct {
		name   string
		verify func(t *testing.T, edges []model.EdgeEntry, prog *Program)
	}{
		{
			name: "extractEdges_PlaceholderLiteral_exactlyTwoEdges",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// We expect exactly 2 edges in source order:
				// 1. CALLNAT 'PRG&LANG' (downgraded to dynamic)
				// 2. CALLNAT 'PLAINPROG' (stays static)
				// No false static edge to the raw text "PRG&LANG" should exist.
				if len(edges) != 2 {
					t.Errorf("len(edges) = %d, want 2 (one dynamic + one static)", len(edges))
				}
			},
		},
		{
			name: "extractEdges_PlaceholderLiteral_placeholderDowngradedToDynamic",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// First edge: CALLNAT 'PRG&LANG' should be downgraded to EdgeCallsDynamic
				// (not EdgeCalls, which is the current bug — placeholder detection is missing)
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgeCallsDynamic {
						t.Errorf("edges[0].Kind = %s, want %s (placeholder literal should be dynamic, not static)",
							edge0.Kind, model.EdgeCallsDynamic)
					}
					if edge0.TargetName != "PRG&LANG" {
						t.Errorf("edges[0].TargetName = %q, want %q", edge0.TargetName, "PRG&LANG")
					}
					// Source must be non-zero (call-site range with caller context preserved)
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero call-site range (caller context preserved)")
					}
				}
			},
		},
		{
			name: "extractEdges_PlaceholderLiteral_cleanLiteralStaysStatic",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Second edge: CALLNAT 'PLAINPROG' should remain static (EdgeCalls)
				// because it contains no placeholder
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgeCalls {
						t.Errorf("edges[1].Kind = %s, want %s (clean literal should be static)",
							edge1.Kind, model.EdgeCalls)
					}
					if edge1.TargetName != "PLAINPROG" {
						t.Errorf("edges[1].TargetName = %q, want %q", edge1.TargetName, "PLAINPROG")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero call-site range")
					}
				}
			},
		},
		{
			name: "extractEdges_PlaceholderLiteral_noFalseStaticEdge",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// CRITICAL: verify there is NO false static edge whose target is "PRG&LANG"
				// The placeholder literal should be downgraded to dynamic, not duplicated
				// as a static edge to a non-existent object with that literal name.
				staticEdgesForPlaceholder := 0
				for _, edge := range edges {
					if edge.Kind == model.EdgeCalls && edge.TargetName == "PRG&LANG" {
						staticEdgesForPlaceholder++
					}
				}
				if staticEdgesForPlaceholder > 0 {
					t.Errorf("found %d static EdgeCalls edges for 'PRG&LANG', want 0 (placeholder should only be dynamic)",
						staticEdgesForPlaceholder)
				}
			},
		},
		{
			name: "extractEdges_PlaceholderLiteral_noDiagnosticForPlaceholder",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// CRITICAL: A placeholder literal must NOT produce a diagnostic.
				// It is a modeled outcome (EdgeCallsDynamic), not a parse error.
				// Channel separation (FR-18, M-6): edges and diagnostics are distinct.
				if len(prog.Diagnostics) > 0 {
					t.Errorf("prog.Diagnostics = %v; want empty for valid program with placeholder literal",
						prog.Diagnostics)
					for i, diag := range prog.Diagnostics {
						t.Logf("  diag[%d]: %q at %v", i, diag.Message, diag.Range)
					}
				}
			},
		},
		{
			name: "extractEdges_PlaceholderLiteral_sourceOrder",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Edges must be in source order: placeholder CALLNAT before clean CALLNAT
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] at line %d, edge[1] at line %d",
							edge0Start, edge1Start)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, edges, prog)
		})
	}

	// Additional table: verify that '&' at the start of the literal also downgrades
	// (FR-18: placeholder can appear anywhere — leading, mid-word, trailing).
	// Uses a minimal inline source so no fixture file is needed.
	leadingPlaceholderCases := []struct {
		name   string
		src    string
		target string
		kind   model.EdgeKind
	}{
		{
			name:   "extractEdges_PlaceholderLiteral_leadingAmpersand_CALLNAT",
			src:    "CALLNAT '&PROG'\nEND",
			target: "&PROG",
			kind:   model.EdgeCallsDynamic,
		},
		{
			name:   "extractEdges_PlaceholderLiteral_FETCH_placeholder",
			src:    "FETCH 'RPT&1'\nEND",
			target: "RPT&1",
			kind:   model.EdgeNavigatesToDynamic,
		},
		{
			name:   "extractEdges_PlaceholderLiteral_RUN_placeholder",
			src:    "RUN 'JOB&X'\nEND",
			target: "JOB&X",
			kind:   model.EdgeNavigatesToDynamic,
		},
	}
	for _, lp := range leadingPlaceholderCases {
		t.Run(lp.name, func(t *testing.T) {
			lpLexer := NewLexer(lp.src)
			lpParser := NewParser(lpLexer)
			lpProg, _ := lpParser.Parse()
			if lpProg == nil {
				t.Fatal("Parser returned nil AST")
			}
			lpEdges := extractEdges(lpProg)
			if len(lpEdges) != 1 {
				t.Fatalf("len(edges) = %d, want 1", len(lpEdges))
			}
			if lpEdges[0].Kind != lp.kind {
				t.Errorf("edges[0].Kind = %s, want %s (placeholder must be dynamic)",
					lpEdges[0].Kind, lp.kind)
			}
			if lpEdges[0].TargetName != lp.target {
				t.Errorf("edges[0].TargetName = %q, want %q", lpEdges[0].TargetName, lp.target)
			}
		})
	}
}
