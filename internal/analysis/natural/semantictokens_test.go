package natural

import (
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
