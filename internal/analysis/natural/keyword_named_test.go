package natural

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestExtractEdges_KeywordNamedSubroutinePerform verifies that PERFORM correctly
// resolves to subroutines named after Natural keywords (Issue #41, RED phase).
//
// Acceptance criteria:
//   - A DEFINE SUBROUTINE named after a keyword (CLEAR, RESET) is parsed with the
//     keyword as the subroutine's name (captured NameRange is non-zero, name is uppercased).
//   - A PERFORM statement targeting a keyword-named subroutine produces an EdgePerforms
//     with TargetName matching the subroutine name (e.g., "CLEAR").
//   - External PERFORM targets (no inline definition) have zero Target range per Task 5.
//   - Non-keyword control names (MY-SUB) still work correctly (regression).
//   - Parser/extractor handles all keyword names uniformly.
//
// The bug (issue #41): parseSubroutine checks only TokenIdentifier for the name,
// missing TokenKeyword cases. Similarly, parsePerformStatement checks only
// TokenIdentifier for the target.
func TestExtractEdges_KeywordNamedSubroutinePerform(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "calls", "keyword-named-subroutine-perform.NSP"))
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
		t.Logf("Parse returned error %v; expecting graceful degradation", err)
	}

	// Extract edges
	edges := extractEdges(prog)

	// Test table-driven assertions
	tests := []struct {
		name   string
		verify func(t *testing.T, edges []model.EdgeEntry, prog *Program)
	}{
		{
			name: "keyword_named_subroutines_parsed_correctly",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// The fixture defines three subroutines: CLEAR, RESET, MY-SUB
				// All three should be parsed with non-empty names and non-zero NameRanges
				if len(prog.Subroutines) != 3 {
					t.Errorf("len(prog.Subroutines) = %d, want 3 (CLEAR, RESET, MY-SUB)", len(prog.Subroutines))
					return
				}

				// Check CLEAR subroutine
				clearFound := false
				for _, sub := range prog.Subroutines {
					if sub.Name == "CLEAR" {
						clearFound = true
						// NameRange must be non-zero
						if (sub.NameRange.Start.Line == 0 && sub.NameRange.Start.Column == 0) &&
							(sub.NameRange.End.Line == 0 && sub.NameRange.End.Column == 0) {
							t.Error("CLEAR subroutine NameRange is zero, want non-zero (issue #41: keyword-named subroutine bug)")
						}
						break
					}
				}
				if !clearFound {
					t.Error("DEFINE SUBROUTINE CLEAR not found in prog.Subroutines; name is empty (issue #41)")
				}

				// Check RESET subroutine
				resetFound := false
				for _, sub := range prog.Subroutines {
					if sub.Name == "RESET" {
						resetFound = true
						// NameRange must be non-zero
						if (sub.NameRange.Start.Line == 0 && sub.NameRange.Start.Column == 0) &&
							(sub.NameRange.End.Line == 0 && sub.NameRange.End.Column == 0) {
							t.Error("RESET subroutine NameRange is zero, want non-zero (issue #41: keyword-named subroutine bug)")
						}
						break
					}
				}
				if !resetFound {
					t.Error("DEFINE SUBROUTINE RESET not found in prog.Subroutines; name is empty (issue #41)")
				}

				// Check MY-SUB control (non-keyword name)
				mysubFound := false
				for _, sub := range prog.Subroutines {
					if sub.Name == "MY-SUB" {
						mysubFound = true
						// NameRange must be non-zero
						if (sub.NameRange.Start.Line == 0 && sub.NameRange.Start.Column == 0) &&
							(sub.NameRange.End.Line == 0 && sub.NameRange.End.Column == 0) {
							t.Error("MY-SUB subroutine NameRange is zero, want non-zero")
						}
						break
					}
				}
				if !mysubFound {
					t.Error("DEFINE SUBROUTINE MY-SUB not found in prog.Subroutines")
				}
			},
		},
		{
			name: "perform_keyword_named_targets_extracted",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Fixture contains three PERFORM statements: PERFORM CLEAR, PERFORM RESET, PERFORM MY-SUB
				// All three should produce EdgePerforms with the correct TargetName
				if len(edges) != 3 {
					t.Errorf("len(edges) = %d, want 3 (PERFORM CLEAR, PERFORM RESET, PERFORM MY-SUB)", len(edges))
					return
				}

				// First edge: PERFORM CLEAR
				if len(edges) > 0 {
					edge0 := edges[0]
					if edge0.Kind != model.EdgePerforms {
						t.Errorf("edges[0].Kind = %s, want %s", edge0.Kind, model.EdgePerforms)
					}
					if edge0.TargetName != "CLEAR" {
						t.Errorf("edges[0].TargetName = %q, want %q (issue #41: PERFORM target name is empty for keyword)", edge0.TargetName, "CLEAR")
					}
					// Source must be non-zero
					if edge0.Source.Start.Line == 0 && edge0.Source.Start.Column == 0 &&
						edge0.Source.End.Line == 0 && edge0.Source.End.Column == 0 {
						t.Error("edges[0].Source is zero, want non-zero statement range")
					}
				}

				// Second edge: PERFORM RESET
				if len(edges) > 1 {
					edge1 := edges[1]
					if edge1.Kind != model.EdgePerforms {
						t.Errorf("edges[1].Kind = %s, want %s", edge1.Kind, model.EdgePerforms)
					}
					if edge1.TargetName != "RESET" {
						t.Errorf("edges[1].TargetName = %q, want %q (issue #41: PERFORM target name is empty for keyword)", edge1.TargetName, "RESET")
					}
					// Source must be non-zero
					if edge1.Source.Start.Line == 0 && edge1.Source.Start.Column == 0 &&
						edge1.Source.End.Line == 0 && edge1.Source.End.Column == 0 {
						t.Error("edges[1].Source is zero, want non-zero statement range")
					}
				}

				// Third edge: PERFORM MY-SUB (control)
				if len(edges) > 2 {
					edge2 := edges[2]
					if edge2.Kind != model.EdgePerforms {
						t.Errorf("edges[2].Kind = %s, want %s", edge2.Kind, model.EdgePerforms)
					}
					if edge2.TargetName != "MY-SUB" {
						t.Errorf("edges[2].TargetName = %q, want %q", edge2.TargetName, "MY-SUB")
					}
					// Source must be non-zero
					if edge2.Source.Start.Line == 0 && edge2.Source.Start.Column == 0 &&
						edge2.Source.End.Line == 0 && edge2.Source.End.Column == 0 {
						t.Error("edges[2].Source is zero, want non-zero statement range")
					}
				}
			},
		},
		{
			name: "edges_in_source_order",
			verify: func(t *testing.T, edges []model.EdgeEntry, prog *Program) {
				// Verify edges are in source order: CLEAR, RESET, MY-SUB
				if len(edges) >= 2 {
					edge0Start := edges[0].Source.Start.Line
					edge1Start := edges[1].Source.Start.Line
					edge2Start := edges[2].Source.Start.Line
					if edge0Start >= edge1Start {
						t.Errorf("edges not in source order: edge[0] (CLEAR) at line %d, edge[1] (RESET) at line %d",
							edge0Start, edge1Start)
					}
					if edge1Start >= edge2Start {
						t.Errorf("edges not in source order: edge[1] (RESET) at line %d, edge[2] (MY-SUB) at line %d",
							edge1Start, edge2Start)
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
}

// TestExtractStructure_KeywordNamedSubroutines verifies that extractStructure
// produces SymbolSubroutine children with correct names for subroutines named
// after Natural keywords (Issue #41, RED phase).
//
// Acceptance criteria:
//   - Root is SymbolObject derived from filename
//   - SymbolSubroutine children exist for CLEAR, RESET, and MY-SUB
//   - Each SymbolSubroutine has Name and SelectionRange correctly set (non-zero)
//   - SelectionRange spans the subroutine name token (uppercased)
//   - Range is non-zero for all three subroutines
//   - Non-keyword control names still work (regression)
//
// The bug (issue #41): parseSubroutine checks only TokenIdentifier for the name,
// missing TokenKeyword cases, so keyword-named subroutines have empty Name and
// zero NameRange, which causes extractStructure to produce SymbolSubroutine with
// empty Name.
func TestExtractStructure_KeywordNamedSubroutines(t *testing.T) {
	// Read the fixture
	fixturePath := filepath.Join("testdata", "structure", "keyword-named-subroutines.NSP")
	content, err := os.ReadFile(fixturePath)
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
		t.Logf("Parse returned error %v; expecting graceful degradation", err)
	}

	// Extract definitions and data access (required by extractStructure)
	defs := extractDefinitions(prog)
	access := extractDataAccess(prog)

	// Call extractStructure
	sym := extractStructure(fixturePath, prog, defs, access)

	// Test table-driven assertions
	tests := []struct {
		name   string
		verify func(t *testing.T, sym *model.Symbol)
	}{
		{
			name: "root_is_object",
			verify: func(t *testing.T, sym *model.Symbol) {
				if sym == nil {
					t.Fatal("extractStructure returned nil, want *Symbol")
				}
				if sym.Kind != model.SymbolObject {
					t.Errorf("root.Kind = %s, want %s", sym.Kind, model.SymbolObject)
				}
			},
		},
		{
			name: "subroutine_clear_present",
			verify: func(t *testing.T, sym *model.Symbol) {
				if sym == nil || len(sym.Children) == 0 {
					t.Skip("no children, skipping")
				}

				// Find CLEAR subroutine
				clearFound := false
				var clearSym model.Symbol
				for _, child := range sym.Children {
					if child.Kind == model.SymbolSubroutine && child.Name == "CLEAR" {
						clearFound = true
						clearSym = child
						break
					}
				}

				if !clearFound {
					t.Error("SymbolSubroutine 'CLEAR' not found in root children; name is empty (issue #41)")
					return
				}

				// SelectionRange must be non-zero
				if (clearSym.SelectionRange.Start.Line == 0 && clearSym.SelectionRange.Start.Column == 0) &&
					(clearSym.SelectionRange.End.Line == 0 && clearSym.SelectionRange.End.Column == 0) {
					t.Error("CLEAR subroutine SelectionRange is zero, want non-zero")
				}

				// Range must be non-zero
				if (clearSym.Range.Start.Line == 0 && clearSym.Range.Start.Column == 0) ||
					(clearSym.Range.End.Line == 0 && clearSym.Range.End.Column == 0) {
					t.Error("CLEAR subroutine Range is zero, want non-zero span")
				}
			},
		},
		{
			name: "subroutine_reset_present",
			verify: func(t *testing.T, sym *model.Symbol) {
				if sym == nil || len(sym.Children) == 0 {
					t.Skip("no children, skipping")
				}

				// Find RESET subroutine
				resetFound := false
				var resetSym model.Symbol
				for _, child := range sym.Children {
					if child.Kind == model.SymbolSubroutine && child.Name == "RESET" {
						resetFound = true
						resetSym = child
						break
					}
				}

				if !resetFound {
					t.Error("SymbolSubroutine 'RESET' not found in root children; name is empty (issue #41)")
					return
				}

				// SelectionRange must be non-zero
				if (resetSym.SelectionRange.Start.Line == 0 && resetSym.SelectionRange.Start.Column == 0) &&
					(resetSym.SelectionRange.End.Line == 0 && resetSym.SelectionRange.End.Column == 0) {
					t.Error("RESET subroutine SelectionRange is zero, want non-zero")
				}

				// Range must be non-zero
				if (resetSym.Range.Start.Line == 0 && resetSym.Range.Start.Column == 0) ||
					(resetSym.Range.End.Line == 0 && resetSym.Range.End.Column == 0) {
					t.Error("RESET subroutine Range is zero, want non-zero span")
				}
			},
		},
		{
			name: "subroutine_mysub_present_control",
			verify: func(t *testing.T, sym *model.Symbol) {
				if sym == nil || len(sym.Children) == 0 {
					t.Skip("no children, skipping")
				}

				// Find MY-SUB subroutine (control case, non-keyword)
				mysubFound := false
				var mysubSym model.Symbol
				for _, child := range sym.Children {
					if child.Kind == model.SymbolSubroutine && child.Name == "MY-SUB" {
						mysubFound = true
						mysubSym = child
						break
					}
				}

				if !mysubFound {
					t.Error("SymbolSubroutine 'MY-SUB' not found in root children (control case)")
					return
				}

				// SelectionRange must be non-zero
				if (mysubSym.SelectionRange.Start.Line == 0 && mysubSym.SelectionRange.Start.Column == 0) &&
					(mysubSym.SelectionRange.End.Line == 0 && mysubSym.SelectionRange.End.Column == 0) {
					t.Error("MY-SUB subroutine SelectionRange is zero, want non-zero")
				}

				// Range must be non-zero
				if (mysubSym.Range.Start.Line == 0 && mysubSym.Range.Start.Column == 0) ||
					(mysubSym.Range.End.Line == 0 && mysubSym.Range.End.Column == 0) {
					t.Error("MY-SUB subroutine Range is zero, want non-zero span")
				}
			},
		},
		{
			name: "three_subroutines_total",
			verify: func(t *testing.T, sym *model.Symbol) {
				if sym == nil || len(sym.Children) == 0 {
					t.Skip("no children, skipping")
				}

				// Count SymbolSubroutine children
				subroutineCount := 0
				for _, child := range sym.Children {
					if child.Kind == model.SymbolSubroutine {
						subroutineCount++
					}
				}

				if subroutineCount != 3 {
					t.Errorf("len(subroutines) = %d, want 3 (CLEAR, RESET, MY-SUB)", subroutineCount)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, sym)
		})
	}
}

// TestPerformBreakGuardrail verifies that PERFORM BREAK (a break-processing statement,
// not a subroutine call) does NOT produce a PERFORMS edge with target "BREAK" (issue #41 guardrail).
//
// Acceptance criteria:
//   - PERFORM BREAK should NOT produce any EdgePerforms edge (no Target captured).
//   - PERFORM BREAK PROCESSING should also NOT produce any EdgePerforms edge.
//   - The parsePerformStatement guard must check for TokenKeyword "BREAK" before capturing
//     the target as a subroutine name, to avoid creating a bogus edge.
//   - This guards against future regressions where keyword-name acceptance breaks the
//     PERFORM BREAK statement parsing.
func TestPerformBreakGuardrail(t *testing.T) {
	// Small fixture with PERFORM BREAK statements
	content := `
PERFORM BREAK
PERFORM BREAK PROCESSING
PERFORM CLEAR
END
`

	// Parse to AST
	lexer := NewLexer(content)
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Logf("Parse returned error %v; expecting graceful degradation", err)
	}

	// Extract edges
	edges := extractEdges(prog)

	// Verify: we should have exactly 1 edge (PERFORM CLEAR), not 3.
	// PERFORM BREAK and PERFORM BREAK PROCESSING must NOT produce PERFORMS edges.
	if len(edges) != 1 {
		t.Errorf("len(edges) = %d, want 1 (only PERFORM CLEAR should produce an edge)", len(edges))
		// Log extracted edges for debugging
		for i, e := range edges {
			t.Logf("  edges[%d]: Kind=%s, TargetName=%q, Source=%v", i, e.Kind, e.TargetName, e.Source)
		}
		return
	}

	// The only edge should be PERFORM CLEAR with TargetName "CLEAR".
	edge := edges[0]
	if edge.Kind != model.EdgePerforms {
		t.Errorf("edges[0].Kind = %s, want %s", edge.Kind, model.EdgePerforms)
	}
	if edge.TargetName != "CLEAR" {
		t.Errorf("edges[0].TargetName = %q, want %q", edge.TargetName, "CLEAR")
	}
}
