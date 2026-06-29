package natural

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLexer_NextToken(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTokens []Token
	}{
		{
			name:  "keyword_callnat",
			input: "CALLNAT 'PROG'",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "CALLNAT", Line: 1, Column: 1},
				{Type: TokenLiteralString, Literal: "'PROG'", Line: 1, Column: 9},
			},
		},
		{
			name:  "identifier",
			input: "MYVAR = 10",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "MYVAR", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "=", Line: 1, Column: 7},
				{Type: TokenLiteralNumeric, Literal: "10", Line: 1, Column: 9},
			},
		},
		{
			name:  "string_literal",
			input: "'Hello World'",
			wantTokens: []Token{
				{Type: TokenLiteralString, Literal: "'Hello World'", Line: 1, Column: 1},
			},
		},
		{
			name:  "numeric_literal",
			input: "123.456",
			wantTokens: []Token{
				{Type: TokenLiteralNumeric, Literal: "123.456", Line: 1, Column: 1},
			},
		},
		{
			name:  "operator",
			input: "= <> <= >= !",
			wantTokens: []Token{
				{Type: TokenOperator, Literal: "=", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "<>", Line: 1, Column: 3},
				{Type: TokenOperator, Literal: "<=", Line: 1, Column: 6},
				{Type: TokenOperator, Literal: ">=", Line: 1, Column: 9},
				{Type: TokenOperator, Literal: "!", Line: 1, Column: 12},
			},
		},
		{
			name:  "punctuation",
			input: ", ; : ( ) [ ]",
			wantTokens: []Token{
				{Type: TokenPunctuation, Literal: ",", Line: 1, Column: 1},
				{Type: TokenPunctuation, Literal: ";", Line: 1, Column: 3},
				{Type: TokenPunctuation, Literal: ":", Line: 1, Column: 5},
				{Type: TokenPunctuation, Literal: "(", Line: 1, Column: 7},
				{Type: TokenPunctuation, Literal: ")", Line: 1, Column: 9},
				{Type: TokenPunctuation, Literal: "[", Line: 1, Column: 11},
				{Type: TokenPunctuation, Literal: "]", Line: 1, Column: 13},
			},
		},
		{
			name:  "single_line_comment",
			input: "* This is a comment\nCALLNAT",
			wantTokens: []Token{
				{Type: TokenComment, Literal: "* This is a comment", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "CALLNAT", Line: 2, Column: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)
			var gotTokens []Token

			for {
				token := lexer.NextToken()

				if token.Type == TokenEOF || token.Type == TokenError {
					break
				}
				gotTokens = append(gotTokens, token)
			}

			if len(gotTokens) != len(tc.wantTokens) {
				t.Fatalf("got %d tokens, want %d tokens: got=%v", len(gotTokens), len(tc.wantTokens), gotTokens)
			}

			for i, want := range tc.wantTokens {
				if gotTokens[i].Type != want.Type {
					t.Errorf("token[%d] type = %d, want %d", i, gotTokens[i].Type, want.Type)
				}
				if gotTokens[i].Literal != want.Literal {
					t.Errorf("token[%d] literal = %q, want %q", i, gotTokens[i].Literal, want.Literal)
				}
				if gotTokens[i].Line != want.Line {
					t.Errorf("token[%d] line = %d, want %d", i, gotTokens[i].Line, want.Line)
				}
				if gotTokens[i].Column != want.Column {
					t.Errorf("token[%d] column = %d, want %d", i, gotTokens[i].Column, want.Column)
				}
			}
		})
	}
}

func TestLexer_PositionTracking(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantPos []Token
	}{
		// Multi-line input with tokens on different lines
		{
			name:  "multi_line_tokens",
			input: "CALLNAT 'PROG'\nPERFORM SUB",
			wantPos: []Token{
				{Type: TokenKeyword, Literal: "CALLNAT", Line: 1, Column: 1},
				{Type: TokenLiteralString, Literal: "'PROG'", Line: 1, Column: 9},
				{Type: TokenKeyword, Literal: "PERFORM", Line: 2, Column: 1},
				{Type: TokenIdentifier, Literal: "SUB", Line: 2, Column: 9},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: create a new lexer with the multi-line input
			lexer := NewLexer(tc.input)

			// Act & Assert: collect tokens and verify positions
			var gotPos []Token

			for {
				token := lexer.NextToken()

				if token.Type == TokenEOF || token.Type == TokenError {
					break
				}
				gotPos = append(gotPos, token)
			}

			// Assert positions match
			if len(gotPos) != len(tc.wantPos) {
				t.Fatalf("got %d tokens, want %d tokens: got=%v", len(gotPos), len(tc.wantPos), gotPos)
			}

			for i, wantPos := range tc.wantPos {
				if gotPos[i].Line != wantPos.Line {
					t.Errorf("token[%d] Line = %d, want %d", i, gotPos[i].Line, wantPos.Line)
				}
				if gotPos[i].Column != wantPos.Column {
					t.Errorf("token[%d] Column = %d, want %d", i, gotPos[i].Column, wantPos.Column)
				}
			}
		})
	}
}

// TestLexer_NaturalIdentifiers verifies the hyphenated-name and prefix-char
// behaviour required by Natural's variable-naming conventions.
//
// Key contracts (Task 6 refactor / lexer correctness):
//   - "#EMPLOYEE-ID" → one TokenIdentifier with literal "#EMPLOYEE-ID"
//     (the # prefix and embedded hyphen are part of the identifier, not operators)
//   - "MY-SUB" → one TokenIdentifier with literal "MY-SUB"
//   - "&PARM&" → one TokenIdentifier with literal "&PARM&"
//   - "A - B" (spaces around hyphen) → identifier / operator / identifier
//     (spaces prevent the hyphen from being absorbed into the preceding name)
//   - "#X-Y-Z" → one identifier covering all three segments
func TestLexer_NaturalIdentifiers(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTokens []Token
	}{
		{
			name:  "hash_prefix_with_hyphenated_name",
			input: "#EMPLOYEE-ID",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "#EMPLOYEE-ID", Line: 1, Column: 1},
			},
		},
		{
			name:  "plain_hyphenated_name",
			input: "MY-SUB",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "MY-SUB", Line: 1, Column: 1},
			},
		},
		{
			name:  "ampersand_prefix_with_trailing_ampersand",
			input: "&PARM&",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "&PARM&", Line: 1, Column: 1},
			},
		},
		{
			// Spaces around the hyphen: lexer returns three tokens, not one.
			name:  "arithmetic_subtraction_not_absorbed_into_identifier",
			input: "A - B",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "A", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "-", Line: 1, Column: 3},
				{Type: TokenIdentifier, Literal: "B", Line: 1, Column: 5},
			},
		},
		{
			name:  "multi_segment_hyphenated_name",
			input: "#X-Y-Z",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "#X-Y-Z", Line: 1, Column: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)
			var got []Token
			for {
				tok := lexer.NextToken()
				if tok.Type == TokenEOF || tok.Type == TokenError {
					break
				}
				got = append(got, tok)
			}

			if len(got) != len(tc.wantTokens) {
				t.Fatalf("got %d tokens, want %d: got=%v", len(got), len(tc.wantTokens), got)
			}
			for i, want := range tc.wantTokens {
				if got[i].Type != want.Type {
					t.Errorf("token[%d] Type = %v, want %v (literal %q)", i, got[i].Type, want.Type, got[i].Literal)
				}
				if got[i].Literal != want.Literal {
					t.Errorf("token[%d] Literal = %q, want %q", i, got[i].Literal, want.Literal)
				}
				if got[i].Line != want.Line {
					t.Errorf("token[%d] Line = %d, want %d", i, got[i].Line, want.Line)
				}
				if got[i].Column != want.Column {
					t.Errorf("token[%d] Column = %d, want %d", i, got[i].Column, want.Column)
				}
			}
		})
	}
}

func TestLexer_FixtureTokenTypes(t *testing.T) {
	// Arrange: Read the fixture file containing one instance of each token type.
	fixturePath := filepath.Join("testdata", "parser", "01-lexer-token-types.nsp")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture %q: %v", fixturePath, err)
	}

	lexer := NewLexer(string(content))

	// Expected tokens from the fixture, in order, with exact Type, Literal, Line, Column.
	// Story 1 (FR-00) asserts: one instance of each token type with expected token values,
	// including the correct source positions (Line + Column) per the fixture's character layout.
	wantTokens := []Token{
		// Line 1: * Task 2 fixture: Lexer token types
		{Type: TokenComment, Literal: "* Task 2 fixture: Lexer token types", Line: 1, Column: 1},
		// Line 2: * This file contains minimal examples of each token type.
		{Type: TokenComment, Literal: "* This file contains minimal examples of each token type.", Line: 2, Column: 1},
		// Line 4: CALLNAT 'PROGRAM'           * Keyword + string literal
		{Type: TokenKeyword, Literal: "CALLNAT", Line: 4, Column: 1},
		{Type: TokenLiteralString, Literal: "'PROGRAM'", Line: 4, Column: 9},
		{Type: TokenComment, Literal: "/* Keyword + string literal", Line: 4, Column: 29},
		// Line 5: PERFORM SUBROUTINE          * Keyword + identifier
		{Type: TokenKeyword, Literal: "PERFORM", Line: 5, Column: 1},
		{Type: TokenIdentifier, Literal: "SUBROUTINE", Line: 5, Column: 9},
		{Type: TokenComment, Literal: "/* Keyword + identifier", Line: 5, Column: 29},
		// Line 6: FETCH 'MYPROG'              * Keyword + string literal
		{Type: TokenKeyword, Literal: "FETCH", Line: 6, Column: 1},
		{Type: TokenLiteralString, Literal: "'MYPROG'", Line: 6, Column: 7},
		{Type: TokenComment, Literal: "/* Keyword + string literal", Line: 6, Column: 29},
		// Line 7: INCLUDE 'COPYBOOK'          * Keyword + string literal
		{Type: TokenKeyword, Literal: "INCLUDE", Line: 7, Column: 1},
		{Type: TokenLiteralString, Literal: "'COPYBOOK'", Line: 7, Column: 9},
		{Type: TokenComment, Literal: "/* Keyword + string literal", Line: 7, Column: 29},
		// Line 8: RUN PROGRAM                 * Keyword + identifier
		{Type: TokenKeyword, Literal: "RUN", Line: 8, Column: 1},
		{Type: TokenIdentifier, Literal: "PROGRAM", Line: 8, Column: 5},
		{Type: TokenComment, Literal: "/* Keyword + identifier", Line: 8, Column: 29},
		// Line 10: MOVE 12345 TO VAR           * Numeric literal
		{Type: TokenKeyword, Literal: "MOVE", Line: 10, Column: 1},
		{Type: TokenLiteralNumeric, Literal: "12345", Line: 10, Column: 6},
		{Type: TokenKeyword, Literal: "TO", Line: 10, Column: 12},
		{Type: TokenIdentifier, Literal: "VAR", Line: 10, Column: 15},
		{Type: TokenComment, Literal: "/* Numeric literal", Line: 10, Column: 29},
		// Line 11: MOVE 3.14159 TO PI          * Decimal literal
		{Type: TokenKeyword, Literal: "MOVE", Line: 11, Column: 1},
		{Type: TokenLiteralNumeric, Literal: "3.14159", Line: 11, Column: 6},
		{Type: TokenKeyword, Literal: "TO", Line: 11, Column: 14},
		{Type: TokenIdentifier, Literal: "PI", Line: 11, Column: 17},
		{Type: TokenComment, Literal: "/* Decimal literal", Line: 11, Column: 29},
		// Line 13: IF X <> Y THEN              * Operators
		{Type: TokenKeyword, Literal: "IF", Line: 13, Column: 1},
		{Type: TokenIdentifier, Literal: "X", Line: 13, Column: 4},
		{Type: TokenOperator, Literal: "<>", Line: 13, Column: 6},
		{Type: TokenIdentifier, Literal: "Y", Line: 13, Column: 9},
		{Type: TokenKeyword, Literal: "THEN", Line: 13, Column: 11},
		{Type: TokenComment, Literal: "/* Operators", Line: 13, Column: 29},
		// Line 14: SET @VAR = 'value'          * Punctuation
		{Type: TokenKeyword, Literal: "SET", Line: 14, Column: 1},
		{Type: TokenIdentifier, Literal: "@VAR", Line: 14, Column: 5},
		{Type: TokenOperator, Literal: "=", Line: 14, Column: 10},
		{Type: TokenLiteralString, Literal: "'value'", Line: 14, Column: 12},
		{Type: TokenComment, Literal: "/* Punctuation", Line: 14, Column: 29},
		// Line 16: * This is a comment          * Comment
		{Type: TokenComment, Literal: "* This is a comment          * Comment", Line: 16, Column: 1},
		// Line 18: END                         * EOF
		{Type: TokenKeyword, Literal: "END", Line: 18, Column: 1},
		{Type: TokenComment, Literal: "/* EOF", Line: 18, Column: 29},
	}

	// Act: Collect all tokens from the lexer until EOF.
	var gotTokens []Token
	for {
		token := lexer.NextToken()
		if token.Type == TokenEOF {
			break
		}
		gotTokens = append(gotTokens, token)
	}

	// Assert: Verify the count and each token's Type, Literal, Line, Column.
	if len(gotTokens) != len(wantTokens) {
		t.Fatalf("token count mismatch: got %d tokens, want %d tokens", len(gotTokens), len(wantTokens))
	}

	for i, want := range wantTokens {
		got := gotTokens[i]
		if got.Type != want.Type {
			t.Errorf("token[%d] Type: got %d, want %d (literal %q)", i, got.Type, want.Type, got.Literal)
		}
		if got.Literal != want.Literal {
			t.Errorf("token[%d] Literal: got %q, want %q", i, got.Literal, want.Literal)
		}
		if got.Line != want.Line {
			t.Errorf("token[%d] Line: got %d, want %d (literal %q)", i, got.Line, want.Line, got.Literal)
		}
		if got.Column != want.Column {
			t.Errorf("token[%d] Column: got %d, want %d (literal %q at line %d)", i, got.Column, want.Column, got.Literal, got.Line)
		}
	}
}

// TestLexer_InlineComments tests the recognition of /* rest-of-line style
// comments (Task 12 / Story 1, FR-30). The lexer must:
//  1. Recognize /* as the start of a REST-OF-LINE comment (everything to EOL)
//  2. NOT have a /* closer: text after */ is still comment (no code resumes)
//  3. Preserve correct position tracking (Line and Column)
//  4. NOT break preceding tokens on the same line
//  5. Treat mid-line * (not preceded by /) as the multiplication operator
func TestLexer_InlineComments(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTokens []Token
	}{
		{
			// REST-OF-LINE comment after CALLNAT target.
			// Asserts: CALLNAT, 'MYPROG', then ONE TokenComment from col 18 to EOL (no closer).
			// The comment literal includes /* and everything to EOL; no */ closer.
			name:  "rest_of_line_comment_after_callnat",
			input: "CALLNAT 'MYPROG' /* call to myprog",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "CALLNAT", Line: 1, Column: 1},
				{Type: TokenLiteralString, Literal: "'MYPROG'", Line: 1, Column: 9},
				{Type: TokenComment, Literal: "/* call to myprog", Line: 1, Column: 18},
			},
		},
		{
			// Mid-line * is multiplication operator; later /* starts the comment.
			// Asserts: COMPUTE, #A, =, #B, * (operator at col 17), #C, then comment from col 22 to EOL.
			// The literal text includes */ at EOL but that's comment text (no code after).
			name:  "multiplication_operator_then_rest_of_line_comment",
			input: "COMPUTE #A = #B * #C /* product, then comment to end of line",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "COMPUTE", Line: 1, Column: 1},
				{Type: TokenIdentifier, Literal: "#A", Line: 1, Column: 9},
				{Type: TokenOperator, Literal: "=", Line: 1, Column: 12},
				{Type: TokenIdentifier, Literal: "#B", Line: 1, Column: 14},
				{Type: TokenOperator, Literal: "*", Line: 1, Column: 17},
				{Type: TokenIdentifier, Literal: "#C", Line: 1, Column: 19},
				{Type: TokenComment, Literal: "/* product, then comment to end of line", Line: 1, Column: 22},
			},
		},
		{
			// ONE rest-of-line comment; /* with */ text inside is still ONE token to EOL.
			// Asserts: MOVE, 1, TO, #A, then ONE TokenComment from col 14 to EOL.
			// The comment text includes inner /* and */, but there's only ONE comment token.
			name:  "rest_of_line_with_nested_syntax_still_one_comment",
			input: "MOVE 1 TO #A /* comment with /* inside */ still all comment",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "MOVE", Line: 1, Column: 1},
				{Type: TokenLiteralNumeric, Literal: "1", Line: 1, Column: 6},
				{Type: TokenKeyword, Literal: "TO", Line: 1, Column: 8},
				{Type: TokenIdentifier, Literal: "#A", Line: 1, Column: 11},
				{Type: TokenComment, Literal: "/* comment with /* inside */ still all comment", Line: 1, Column: 14},
			},
		},
		{
			// Line-start * comment (existing behavior, must not break).
			// Asserts: * at start of line is a full-line TokenComment.
			name:  "full_line_comment_at_start",
			input: "* This is a full-line comment\nCALLNAT",
			wantTokens: []Token{
				{Type: TokenComment, Literal: "* This is a full-line comment", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "CALLNAT", Line: 2, Column: 1},
			},
		},
		{
			// Mid-line * with no / before it is multiplication, not a comment.
			// Asserts: #B, *, #C are three separate tokens (no comment starts).
			name:  "mid_line_star_is_multiplication",
			input: "#B * #C",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "#B", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "*", Line: 1, Column: 4},
				{Type: TokenIdentifier, Literal: "#C", Line: 1, Column: 6},
			},
		},
		{
			// Line-start ** is also a full-line comment.
			// Asserts: ** at col 1 starts a TokenComment to EOL.
			name:  "double_star_full_line_comment",
			input: "** This is a double-asterisk comment\nCALLNAT",
			wantTokens: []Token{
				{Type: TokenComment, Literal: "** This is a double-asterisk comment", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "CALLNAT", Line: 2, Column: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)
			var gotTokens []Token

			for {
				token := lexer.NextToken()
				if token.Type == TokenEOF || token.Type == TokenError {
					break
				}
				gotTokens = append(gotTokens, token)
			}

			if len(gotTokens) != len(tc.wantTokens) {
				t.Fatalf("got %d tokens, want %d tokens: got=%v", len(gotTokens), len(tc.wantTokens), gotTokens)
			}

			for i, want := range tc.wantTokens {
				got := gotTokens[i]
				if got.Type != want.Type {
					t.Errorf("token[%d] Type: got %d, want %d (literal %q)", i, got.Type, want.Type, got.Literal)
				}
				if got.Literal != want.Literal {
					t.Errorf("token[%d] Literal: got %q, want %q", i, got.Literal, want.Literal)
				}
				if got.Line != want.Line {
					t.Errorf("token[%d] Line: got %d, want %d (literal %q)", i, got.Line, want.Line, got.Literal)
				}
				if got.Column != want.Column {
					t.Errorf("token[%d] Column: got %d, want %d (literal %q at line %d)", i, got.Column, want.Column, got.Literal, got.Line)
				}
			}
		})
	}
}

// TestLexer_CRLFLineCounting verifies that CRLF and LF line endings produce
// identical line numbers (Task R2 remediation). This is critical because
// mainframe-exported .NSx files commonly use CRLF line endings, and the
// lexer must not double-increment the line counter on \r\n sequences.
//
// Task R2 (review-extraction finding 3): The lexer whitespace loop currently
// increments the line counter for \r AND \n independently, causing each \r\n
// to advance the line by 2. This corrupts AST positions and diagnostic ranges
// for CRLF-line-ending files (the primary target format).
//
// Acceptance: CRLF and LF inputs must report identical line numbers.
func TestLexer_CRLFLineCounting(t *testing.T) {
	tests := []struct {
		name  string
		input string
		desc  string
	}{
		{
			name:  "crlf_two_statement_lines",
			input: "CALLNAT 'A'\r\nPERFORM SUB",
			desc:  "CRLF: PERFORM on line 2 (not line 3)",
		},
		{
			name:  "lf_two_statement_lines",
			input: "CALLNAT 'A'\nPERFORM SUB",
			desc:  "LF: PERFORM on line 2 (baseline)",
		},
		{
			name:  "crlf_three_statement_lines",
			input: "CALLNAT 'A'\r\nPERFORM SUB\r\nINCLUDE 'COPY'",
			desc:  "CRLF: three statements on lines 1, 2, 3 (not 1, 3, 5)",
		},
		{
			name:  "lf_three_statement_lines",
			input: "CALLNAT 'A'\nPERFORM SUB\nINCLUDE 'COPY'",
			desc:  "LF: three statements on lines 1, 2, 3 (baseline)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)

			// Collect all tokens (non-EOF).
			var tokens []Token
			for {
				tok := lexer.NextToken()
				if tok.Type == TokenEOF || tok.Type == TokenError {
					break
				}
				tokens = append(tokens, tok)
			}

			// For CRLF/LF pairs (name suffixes _crlf / _lf), assert they have the same line numbers.
			// For standalone tests, assert specific line values.
			switch tc.name {
			case "crlf_two_statement_lines":
				// CALLNAT on line 1, PERFORM on line 2
				if len(tokens) < 4 {
					t.Fatalf("want at least 4 tokens, got %d: %v", len(tokens), tokens)
				}
				callnat := tokens[0]
				perform := tokens[2]
				if callnat.Type != TokenKeyword || callnat.Literal != "CALLNAT" {
					t.Errorf("token[0] want CALLNAT keyword, got %+v", callnat)
				}
				if perform.Type != TokenKeyword || perform.Literal != "PERFORM" {
					t.Errorf("token[2] want PERFORM keyword, got %+v", perform)
				}
				if callnat.Line != 1 {
					t.Errorf("CALLNAT want Line=1, got Line=%d", callnat.Line)
				}
				// CRITICAL ASSERTION: PERFORM must be on line 2, not line 3 (the bug reports 3).
				if perform.Line != 2 {
					t.Errorf("PERFORM want Line=2 (not 3, which is the bug), got Line=%d", perform.Line)
				}

			case "lf_two_statement_lines":
				// LF baseline: CALLNAT on line 1, PERFORM on line 2
				if len(tokens) < 4 {
					t.Fatalf("want at least 4 tokens, got %d: %v", len(tokens), tokens)
				}
				callnat := tokens[0]
				perform := tokens[2]
				if callnat.Line != 1 {
					t.Errorf("CALLNAT want Line=1, got Line=%d", callnat.Line)
				}
				if perform.Line != 2 {
					t.Errorf("PERFORM want Line=2, got Line=%d", perform.Line)
				}

			case "crlf_three_statement_lines":
				// CALLNAT on line 1, PERFORM on line 2, INCLUDE on line 3
				if len(tokens) < 6 {
					t.Fatalf("want at least 6 tokens, got %d: %v", len(tokens), tokens)
				}
				callnat := tokens[0]
				perform := tokens[2]
				include := tokens[4]
				if callnat.Line != 1 {
					t.Errorf("CALLNAT want Line=1, got Line=%d", callnat.Line)
				}
				if perform.Line != 2 {
					t.Errorf("PERFORM want Line=2 (not 3), got Line=%d", perform.Line)
				}
				if include.Line != 3 {
					t.Errorf("INCLUDE want Line=3 (not 5), got Line=%d", include.Line)
				}

			case "lf_three_statement_lines":
				// LF baseline: CALLNAT on line 1, PERFORM on line 2, INCLUDE on line 3
				if len(tokens) < 6 {
					t.Fatalf("want at least 6 tokens, got %d: %v", len(tokens), tokens)
				}
				callnat := tokens[0]
				perform := tokens[2]
				include := tokens[4]
				if callnat.Line != 1 {
					t.Errorf("CALLNAT want Line=1, got Line=%d", callnat.Line)
				}
				if perform.Line != 2 {
					t.Errorf("PERFORM want Line=2, got Line=%d", perform.Line)
				}
				if include.Line != 3 {
					t.Errorf("INCLUDE want Line=3, got Line=%d", include.Line)
				}
			}
		})
	}
}
