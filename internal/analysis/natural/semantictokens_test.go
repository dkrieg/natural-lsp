package natural

import (
	"os"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestSemanticTokens_Interface verifies that Analyzer.SemanticTokens exists
// on the analysis.Analyzer interface and always returns a non-nil slice
// (feature 29, T2 — the seam contract; Phase A classification is asserted by
// TestSemanticTokens_PhaseALexical).
func TestSemanticTokens_Interface(t *testing.T) {
	az := New(nil)

	// Verify the analyzer can be assigned to analysis.Analyzer without error
	// (compile-time assertion that the method exists).
	var _ analysis.Analyzer = az

	// Call SemanticTokens and verify it returns non-nil even on simple input.
	got := az.SemanticTokens("foo.NSP", []byte("MOVE 1 TO #X\n"))

	if got == nil {
		t.Fatal("SemanticTokens returned nil, expected non-nil slice")
	}
}

// TestSemanticTokens_PhaseALexical tests Phase A lexical classification
// (feature 29, T3): keyword/comment/string/number/operator tokens are emitted
// with correct source ranges. Identifiers are NOT emitted (Phase B deferred).
// Each token's Range must span the actual source bytes (not derived from Literal length).
func TestSemanticTokens_PhaseALexical(t *testing.T) {
	// Load fixture: a small program with one of each Phase-A token type.
	content := []byte("* Full-line comment\nMOVE 42 TO #X /* rest-of-line comment\nCOMPUTE #Y := 'HELLO'\n")

	az := New(nil)
	got := az.SemanticTokens("lexical.NSP", content)

	// Expected tokens (in document order):
	// Line 1: "* Full-line comment"
	//   1. Col 1-19: "*" (full-line comment)
	// Line 2: "MOVE 42 TO #X /* rest-of-line comment"
	//   2. Col 1-4: "MOVE" (keyword)
	//   3. Col 6-7: "42" (number)
	//   4. Col 9-10: "TO" (keyword)
	//   5. Col 15-37: "/* rest-of-line comment" (rest-of-line comment; starts at /, not the space before)
	// Line 3: "COMPUTE #Y := 'HELLO'"
	//   6. Col 1-7: "COMPUTE" (keyword)
	//   7. Col 12-13: ":=" (operator)
	//   8. Col 15-21: "'HELLO'" (string, including quotes)
	//
	// NOT emitted (Phase A excludes):
	// - #X and #Y (identifiers) — deferred to Phase B
	// - Any punctuation

	expectedCount := 8
	if len(got) != expectedCount {
		t.Fatalf("got %d tokens, want %d", len(got), expectedCount)
	}

	// Helper to check a token's type and range (1-based, inclusive-end).
	checkToken := func(i int, wantType model.SemanticTokenType, wantStartLine, wantStartCol, wantEndLine, wantEndCol int) {
		t.Helper()
		if i >= len(got) {
			t.Fatalf("token %d: out of bounds", i)
		}
		tok := got[i]
		if tok.Type != wantType {
			t.Errorf("token %d: Type = %q, want %q", i, tok.Type, wantType)
		}
		if tok.Range.Start.Line != wantStartLine || tok.Range.Start.Column != wantStartCol {
			t.Errorf("token %d: Start = (%d, %d), want (%d, %d)", i, tok.Range.Start.Line, tok.Range.Start.Column, wantStartLine, wantStartCol)
		}
		if tok.Range.End.Line != wantEndLine || tok.Range.End.Column != wantEndCol {
			t.Errorf("token %d: End = (%d, %d), want (%d, %d)", i, tok.Range.End.Line, tok.Range.End.Column, wantEndLine, wantEndCol)
		}
	}

	// Line 1, Col 1-19: full-line comment "* Full-line comment"
	checkToken(0, model.SemanticTokenTypeComment, 1, 1, 1, 19)

	// Line 2, Col 1-4: "MOVE" keyword
	checkToken(1, model.SemanticTokenTypeKeyword, 2, 1, 2, 4)

	// Line 2, Col 6-7: "42" number
	checkToken(2, model.SemanticTokenTypeNumber, 2, 6, 2, 7)

	// Line 2, Col 9-10: "TO" keyword
	checkToken(3, model.SemanticTokenTypeKeyword, 2, 9, 2, 10)

	// Line 2, Col 15-37: "/* rest-of-line comment" (rest-of-line comment)
	checkToken(4, model.SemanticTokenTypeComment, 2, 15, 2, 37)

	// Line 3, Col 1-7: "COMPUTE" keyword
	checkToken(5, model.SemanticTokenTypeKeyword, 3, 1, 3, 7)

	// Line 3, Col 12-13: ":=" operator
	checkToken(6, model.SemanticTokenTypeOperator, 3, 12, 3, 13)

	// Line 3, Col 15-21: "'HELLO'" string including quotes
	checkToken(7, model.SemanticTokenTypeString, 3, 15, 3, 21)
}

// TestSemanticTokens_PhaseB_Variables tests Phase B semantic classification (feature 29, T7):
// data variables and parameters are reclassified from TokenIdentifier to `variable` / `parameter`.
// Declaration sites receive the `declaration` modifier; use sites do not.
// This test fixture includes a LOCAL variable (#X) and a PARAMETER variable (#P),
// used in both declaration and in statement bodies.
//
// (FR-56: semantic highlighting; FR-17: modeled gaps on mismatches — undeclared identifiers
// fall back to lexical/no-emit, never falsely classified.)
func TestSemanticTokens_PhaseB_Variables(t *testing.T) {
	// Load fixture: a program with DEFINE DATA LOCAL and PARAMETER sections,
	// and statements that use those variables.
	content := readTestData(t, "variables.NSP")

	az := New(nil)
	got := az.SemanticTokens("variables.NSP", content)

	// Phase B expects identifiers (#X, #P) to be classified as variable/parameter.
	// The DEFINE DATA declaration site of #X should have the `declaration` modifier.
	// The DEFINE DATA declaration site of #P should have the `declaration` modifier and type `parameter`.
	// Use sites in statement bodies should NOT have the `declaration` modifier.

	// Helper to find a token by its Range (start line/col).
	findToken := func(startLine, startCol int) *model.SemanticToken {
		for i := range got {
			if got[i].Range.Start.Line == startLine && got[i].Range.Start.Column == startCol {
				return &got[i]
			}
		}
		return nil
	}

	// Line 3, Col 5-6: declaration of #X in LOCAL section
	// Expected: Type = variable, Modifiers includes declaration
	tokenDeclX := findToken(3, 5)
	if tokenDeclX == nil {
		t.Fatalf("expected token at line 3, col 5 (#X declaration), not found")
	}
	if tokenDeclX.Type != model.SemanticTokenTypeVariable {
		t.Errorf("line 3, col 5 (#X declaration): Type = %q, want %q", tokenDeclX.Type, model.SemanticTokenTypeVariable)
	}
	if tokenDeclX.Modifiers&model.SemanticTokenModifierDeclaration == 0 {
		t.Errorf("line 3, col 5 (#X declaration): missing declaration modifier (got %d)", tokenDeclX.Modifiers)
	}

	// Line 5, Col 5-6: declaration of #P in PARAMETER section
	// Expected: Type = parameter, Modifiers includes declaration
	tokenDeclP := findToken(5, 5)
	if tokenDeclP == nil {
		t.Fatalf("expected token at line 5, col 5 (#P declaration), not found")
	}
	if tokenDeclP.Type != model.SemanticTokenTypeParameter {
		t.Errorf("line 5, col 5 (#P declaration): Type = %q, want %q", tokenDeclP.Type, model.SemanticTokenTypeParameter)
	}
	if tokenDeclP.Modifiers&model.SemanticTokenModifierDeclaration == 0 {
		t.Errorf("line 5, col 5 (#P declaration): missing declaration modifier (got %d)", tokenDeclP.Modifiers)
	}

	// Line 8, Col 6-7: use of #P in "MOVE #P TO #X"
	// Layout: "MOVE " (5 chars) + "#P" (cols 6-7) + ...
	// Expected: Type = parameter (because #P is declared in PARAMETER section)
	// NOT declaration modifier (this is a use, not a declaration)
	tokenUseP := findToken(8, 6)
	if tokenUseP == nil {
		t.Fatalf("expected token at line 8, col 6 (#P use in MOVE), not found")
	}
	if tokenUseP.Type != model.SemanticTokenTypeParameter {
		t.Errorf("line 8, col 6 (#P use): Type = %q, want %q", tokenUseP.Type, model.SemanticTokenTypeParameter)
	}
	if tokenUseP.Modifiers&model.SemanticTokenModifierDeclaration != 0 {
		t.Errorf("line 8, col 6 (#P use): should NOT have declaration modifier (got %d)", tokenUseP.Modifiers)
	}

	// Line 8, Col 12-13: use of #X in "MOVE #P TO #X"
	// Layout: "MOVE " (5) + "#P" (2) + " TO " (4) + "#X" (cols 12-13)
	// Expected: Type = variable (because #X is declared in LOCAL section)
	// NOT declaration modifier (this is a use, not a declaration)
	tokenUseX := findToken(8, 12)
	if tokenUseX == nil {
		t.Fatalf("expected token at line 8, col 12 (#X use in MOVE), not found")
	}
	if tokenUseX.Type != model.SemanticTokenTypeVariable {
		t.Errorf("line 8, col 12 (#X use): Type = %q, want %q", tokenUseX.Type, model.SemanticTokenTypeVariable)
	}
	if tokenUseX.Modifiers&model.SemanticTokenModifierDeclaration != 0 {
		t.Errorf("line 8, col 12 (#X use): should NOT have declaration modifier (got %d)", tokenUseX.Modifiers)
	}

	// Line 9, Col 9-10: use of #X in "COMPUTE #X := #P"
	// Layout: "COMPUTE " (8) + "#X" (cols 9-10)
	// Expected: Type = variable
	tokenUseX2 := findToken(9, 9)
	if tokenUseX2 == nil {
		t.Fatalf("expected token at line 9, col 9 (#X use in COMPUTE), not found")
	}
	if tokenUseX2.Type != model.SemanticTokenTypeVariable {
		t.Errorf("line 9, col 9 (#X use in COMPUTE): Type = %q, want %q", tokenUseX2.Type, model.SemanticTokenTypeVariable)
	}

	// Line 9, Col 15-16: use of #P in "COMPUTE #X := #P"
	// Layout: "COMPUTE " (8) + "#X" (2) + " " (1) + ":=" (2) + " " (1) + "#P" (cols 15-16)
	// Expected: Type = parameter
	tokenUseP2 := findToken(9, 15)
	if tokenUseP2 == nil {
		t.Fatalf("expected token at line 9, col 15 (#P use in COMPUTE), not found")
	}
	if tokenUseP2.Type != model.SemanticTokenTypeParameter {
		t.Errorf("line 9, col 15 (#P use in COMPUTE): Type = %q, want %q", tokenUseP2.Type, model.SemanticTokenTypeParameter)
	}
}

// TestSemanticTokens_PhaseB_Calls tests Phase B semantic classification for call targets
// (feature 29, T8): literal CALLNAT/FETCH/RUN targets are classified as `function`,
// PERFORM subroutine names are classified as `function`, and inline DEFINE SUBROUTINE
// definition names are classified as `function` + `definition` modifier.
// Dynamic targets (CALLNAT #VAR) fall back to variable classification if declared.
//
// (FR-56: semantic highlighting; FR-18: dynamic targets never become `function`;
// FR-43: modeled gaps degrade gracefully.)
func TestSemanticTokens_PhaseB_Calls(t *testing.T) {
	// Load fixture: a program with literal CALLNAT, a PERFORM with inline DEFINE SUBROUTINE,
	// and a dynamic CALLNAT with a declared variable.
	content := readTestData(t, "calls.NSP")

	az := New(nil)
	got := az.SemanticTokens("calls.NSP", content)

	// Helper to find a token by its Range (start line/col).
	findToken := func(startLine, startCol int) *model.SemanticToken {
		for i := range got {
			if got[i].Range.Start.Line == startLine && got[i].Range.Start.Column == startCol {
				return &got[i]
			}
		}
		return nil
	}

	// Fixture layout (calls.NSP):
	// Line 1: DEFINE DATA
	// Line 2: LOCAL
	// Line 3:   1 #DYN (A10)
	// Line 4: END-DEFINE
	// Line 5: (blank)
	// Line 6: CALLNAT 'SUBPROG' PARM1 PARM2
	//         Col 1-7: "CALLNAT" (keyword)
	//         Col 9-17: "'SUBPROG'" (string literal target)
	// Line 7: PERFORM MY-ROUTINE
	//         Col 1-7: "PERFORM" (keyword)
	//         Col 9-18: "MY-ROUTINE" (subroutine target identifier)
	// Line 8: DEFINE SUBROUTINE MY-ROUTINE
	//         Col 1-6: "DEFINE" (keyword)
	//         Col 7: space
	//         Col 8-17: "SUBROUTINE" (keyword)
	//         Col 18: space
	//         Col 19-28: "MY-ROUTINE" (subroutine name definition)
	// Line 9:   CALLNAT #DYN
	//          Col 1-2: spaces
	//          Col 3-9: "CALLNAT" (keyword)
	//          Col 10: space
	//          Col 11-14: "#DYN" (variable identifier, dynamic target)
	// Line 10: END-SUBROUTINE

	// Test 1: Line 6, Col 9-17: literal CALLNAT target 'SUBPROG' → Type = function (overrides string)
	tokenCallnatTarget := findToken(6, 9)
	if tokenCallnatTarget == nil {
		t.Fatalf("expected token at line 6, col 9 (CALLNAT 'SUBPROG' target), not found")
	}
	if tokenCallnatTarget.Type != model.SemanticTokenTypeFunction {
		t.Errorf("line 6, col 9 (literal CALLNAT target): Type = %q, want %q",
			tokenCallnatTarget.Type, model.SemanticTokenTypeFunction)
	}

	// Test 2: Line 7, Col 9-18: PERFORM MY-ROUTINE (use site) → Type = function, no definition modifier
	tokenPerformTarget := findToken(7, 9)
	if tokenPerformTarget == nil {
		t.Fatalf("expected token at line 7, col 9 (PERFORM MY-ROUTINE target), not found")
	}
	if tokenPerformTarget.Type != model.SemanticTokenTypeFunction {
		t.Errorf("line 7, col 9 (PERFORM subroutine use): Type = %q, want %q",
			tokenPerformTarget.Type, model.SemanticTokenTypeFunction)
	}
	if tokenPerformTarget.Modifiers&model.SemanticTokenModifierDefinition != 0 {
		t.Errorf("line 7, col 9 (PERFORM use): should NOT have definition modifier (got %d)",
			tokenPerformTarget.Modifiers)
	}

	// Test 3: Line 8, Col 19-28: DEFINE SUBROUTINE MY-ROUTINE (definition) → Type = function + definition modifier
	tokenSubroutineDefName := findToken(8, 19)
	if tokenSubroutineDefName == nil {
		t.Fatalf("expected token at line 8, col 19 (DEFINE SUBROUTINE MY-ROUTINE definition name), not found")
	}
	if tokenSubroutineDefName.Type != model.SemanticTokenTypeFunction {
		t.Errorf("line 8, col 19 (DEFINE SUBROUTINE name): Type = %q, want %q",
			tokenSubroutineDefName.Type, model.SemanticTokenTypeFunction)
	}
	if tokenSubroutineDefName.Modifiers&model.SemanticTokenModifierDefinition == 0 {
		t.Errorf("line 8, col 19 (DEFINE SUBROUTINE name): missing definition modifier (got %d)",
			tokenSubroutineDefName.Modifiers)
	}

	// Test 4: Line 9, Col 11-14: dynamic CALLNAT #DYN → Type = variable (declared), NOT function
	tokenDynTarget := findToken(9, 11)
	if tokenDynTarget == nil {
		t.Fatalf("expected token at line 9, col 11 (dynamic CALLNAT #DYN target), not found")
	}
	if tokenDynTarget.Type != model.SemanticTokenTypeVariable {
		t.Errorf("line 9, col 11 (dynamic CALLNAT #DYN): Type = %q, want %q (declared variable, not function)",
			tokenDynTarget.Type, model.SemanticTokenTypeVariable)
	}
	if tokenDynTarget.Type == model.SemanticTokenTypeFunction {
		t.Errorf("line 9, col 11 (dynamic CALLNAT #DYN): should NOT be classified as function (it is a variable)")
	}
}

// readTestData loads a fixture file from testdata/semantictokens/<name>.
// It simplifies test setup for fixtures that don't fit neatly inline.
func readTestData(t *testing.T, name string) []byte {
	t.Helper()
	path := "testdata/semantictokens/" + name
	// We will read the fixture file using Go's standard test approach.
	// For now, we read it from the project's actual testdata location.
	//
	// This helper intentionally uses a relative path that will be resolved
	// relative to the Go package when tests run.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %q: %v", path, err)
	}
	return content
}
