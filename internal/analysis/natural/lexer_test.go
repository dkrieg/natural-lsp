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

// TestLexer_EmbeddedSQLKeywords (ES-1) verifies that embedded-SQL keywords
// lex as TokenKeyword, not TokenIdentifier. This includes single-word keywords
// (SINGLE, INTO, VALUES, MERGE, COMMIT, ROLLBACK, CALLDBPROC, PROCESS, RESULT)
// and hyphenated keywords (END-SELECT, END-RESULT) which must be lexed as a
// single token with the full hyphenated literal.
//
// Acceptance (from plan.md ES-1):
//   - Each new SQL keyword → TokenKeyword with uppercased literal
//   - END-SELECT/END-RESULT lex as ONE token (whole hyphenated word), not two
//   - Existing keywords (SELECT, FROM, WHERE, SET, etc.) remain unchanged (regression)
func TestLexer_EmbeddedSQLKeywords(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTokens []Token
	}{
		{
			name:  "single_keyword",
			input: "SINGLE",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "SINGLE", Line: 1, Column: 1},
			},
		},
		{
			name:  "into_keyword",
			input: "INTO",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "INTO", Line: 1, Column: 1},
			},
		},
		{
			name:  "values_keyword",
			input: "VALUES",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "VALUES", Line: 1, Column: 1},
			},
		},
		{
			name:  "merge_keyword",
			input: "MERGE",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "MERGE", Line: 1, Column: 1},
			},
		},
		{
			name:  "commit_keyword",
			input: "COMMIT",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "COMMIT", Line: 1, Column: 1},
			},
		},
		{
			name:  "rollback_keyword",
			input: "ROLLBACK",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "ROLLBACK", Line: 1, Column: 1},
			},
		},
		{
			name:  "calldbproc_keyword",
			input: "CALLDBPROC",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "CALLDBPROC", Line: 1, Column: 1},
			},
		},
		{
			name:  "process_keyword",
			input: "PROCESS",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "PROCESS", Line: 1, Column: 1},
			},
		},
		{
			name:  "result_keyword",
			input: "RESULT",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "RESULT", Line: 1, Column: 1},
			},
		},
		{
			name:  "end_select_as_single_token",
			input: "END-SELECT",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "END-SELECT", Line: 1, Column: 1},
			},
		},
		{
			name:  "end_result_as_single_token",
			input: "END-RESULT",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "END-RESULT", Line: 1, Column: 1},
			},
		},
		{
			name:  "all_sql_keywords_mixed",
			input: "SINGLE INTO VALUES MERGE COMMIT ROLLBACK CALLDBPROC PROCESS RESULT END-SELECT END-RESULT",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "SINGLE", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "INTO", Line: 1, Column: 8},
				{Type: TokenKeyword, Literal: "VALUES", Line: 1, Column: 13},
				{Type: TokenKeyword, Literal: "MERGE", Line: 1, Column: 20},
				{Type: TokenKeyword, Literal: "COMMIT", Line: 1, Column: 26},
				{Type: TokenKeyword, Literal: "ROLLBACK", Line: 1, Column: 33},
				{Type: TokenKeyword, Literal: "CALLDBPROC", Line: 1, Column: 42},
				{Type: TokenKeyword, Literal: "PROCESS", Line: 1, Column: 53},
				{Type: TokenKeyword, Literal: "RESULT", Line: 1, Column: 61},
				{Type: TokenKeyword, Literal: "END-SELECT", Line: 1, Column: 68},
				{Type: TokenKeyword, Literal: "END-RESULT", Line: 1, Column: 79},
			},
		},
		{
			name:  "regression_existing_sql_keywords",
			input: "SELECT FROM WHERE SET INSERT UPDATE DELETE STORE LOOP",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "SELECT", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "FROM", Line: 1, Column: 8},
				{Type: TokenKeyword, Literal: "WHERE", Line: 1, Column: 13},
				{Type: TokenKeyword, Literal: "SET", Line: 1, Column: 19},
				{Type: TokenKeyword, Literal: "INSERT", Line: 1, Column: 23},
				{Type: TokenKeyword, Literal: "UPDATE", Line: 1, Column: 30},
				{Type: TokenKeyword, Literal: "DELETE", Line: 1, Column: 37},
				{Type: TokenKeyword, Literal: "STORE", Line: 1, Column: 44},
				{Type: TokenKeyword, Literal: "LOOP", Line: 1, Column: 50},
			},
		},
		{
			name:  "case_insensitive_single",
			input: "single SiNgLe sInGlE",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "SINGLE", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "SINGLE", Line: 1, Column: 8},
				{Type: TokenKeyword, Literal: "SINGLE", Line: 1, Column: 15},
			},
		},
		{
			name:  "case_insensitive_end_select",
			input: "end-select END-SELECT EnD-sElEcT",
			wantTokens: []Token{
				{Type: TokenKeyword, Literal: "END-SELECT", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "END-SELECT", Line: 1, Column: 12},
				{Type: TokenKeyword, Literal: "END-SELECT", Line: 1, Column: 23},
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
					t.Errorf("token[%d] type: got %d, want %d (literal %q)", i, got.Type, want.Type, got.Literal)
				}
				if got.Literal != want.Literal {
					t.Errorf("token[%d] literal: got %q, want %q", i, got.Literal, want.Literal)
				}
				if got.Line != want.Line {
					t.Errorf("token[%d] line: got %d, want %d", i, got.Line, want.Line)
				}
				if got.Column != want.Column {
					t.Errorf("token[%d] column: got %d, want %d", i, got.Column, want.Column)
				}
			}
		})
	}
}

// TestLexer_SQLOpaqueSpan_Regression (ES-2) verifies that outside SQL context,
// <, >, <>, <=, >= lex unchanged (regression assertion).
// This is the critical AC-1 from the ES-2 task: non-SQL `<`/`>` operators
// must be byte-for-byte unchanged from today's lexer behavior.
func TestLexer_SQLOpaqueSpan_Regression(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTokens []Token
	}{
		{
			name:  "regression_less_than_outside_sql",
			input: "A < B",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "A", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "<", Line: 1, Column: 3},
				{Type: TokenIdentifier, Literal: "B", Line: 1, Column: 5},
			},
		},
		{
			name:  "regression_greater_than_outside_sql",
			input: "A > B",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "A", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: ">", Line: 1, Column: 3},
				{Type: TokenIdentifier, Literal: "B", Line: 1, Column: 5},
			},
		},
		{
			name:  "regression_not_equal_outside_sql",
			input: "A <> B",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "A", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "<>", Line: 1, Column: 3},
				{Type: TokenIdentifier, Literal: "B", Line: 1, Column: 6},
			},
		},
		{
			name:  "regression_less_equal_outside_sql",
			input: "A <= B",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "A", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: "<=", Line: 1, Column: 3},
				{Type: TokenIdentifier, Literal: "B", Line: 1, Column: 6},
			},
		},
		{
			name:  "regression_greater_equal_outside_sql",
			input: "A >= B",
			wantTokens: []Token{
				{Type: TokenIdentifier, Literal: "A", Line: 1, Column: 1},
				{Type: TokenOperator, Literal: ">=", Line: 1, Column: 3},
				{Type: TokenIdentifier, Literal: "B", Line: 1, Column: 6},
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
					t.Errorf("token[%d] type: got %d, want %d (literal %q)", i, got.Type, want.Type, got.Literal)
				}
				if got.Literal != want.Literal {
					t.Errorf("token[%d] literal: got %q, want %q", i, got.Literal, want.Literal)
				}
				if got.Line != want.Line {
					t.Errorf("token[%d] line: got %d, want %d", i, got.Line, want.Line)
				}
				if got.Column != want.Column {
					t.Errorf("token[%d] column: got %d, want %d", i, got.Column, want.Column)
				}
			}
		})
	}
}

// TestLexer_SQLOpaqueSpan_OpaqueCapture (ES-2) verifies that a ScanOpaqueSpan
// entry point (called by the parser after detecting "PROCESS SQL") captures
// the <<...>> region as a single opaque token with the raw interior text.
// AC-2: Opaque span captures multi-line interior verbatim.
// AC-3: Embedded newlines and comment characters (* / /* etc.) are preserved, not re-lexed.
func TestLexer_SQLOpaqueSpan_OpaqueCapture(t *testing.T) {
	tests := []struct {
		name                string
		input               string
		wantTokensBeforeSQL []Token
		wantOpaqueInterior  string
	}{
		{
			name:  "opaque_span_single_line",
			input: "PROCESS SQL DDMNAME << UPDATE table SET col = #var >>",
			wantTokensBeforeSQL: []Token{
				{Type: TokenKeyword, Literal: "PROCESS", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "SQL", Line: 1, Column: 9},
				{Type: TokenIdentifier, Literal: "DDMNAME", Line: 1, Column: 13},
			},
			wantOpaqueInterior: " UPDATE table SET col = #var ",
		},
		{
			name:  "opaque_span_multi_line_with_comments",
			input: "PROCESS SQL DDMNAME << UPDATE table\n  SET col = #var\n  /* this is NOT lexed as comment */\n  * asterisk also not lexed\n>>",
			wantTokensBeforeSQL: []Token{
				{Type: TokenKeyword, Literal: "PROCESS", Line: 1, Column: 1},
				{Type: TokenKeyword, Literal: "SQL", Line: 1, Column: 9},
				{Type: TokenIdentifier, Literal: "DDMNAME", Line: 1, Column: 13},
			},
			wantOpaqueInterior: " UPDATE table\n  SET col = #var\n  /* this is NOT lexed as comment */\n  * asterisk also not lexed\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)
			var gotTokens []Token

			// Collect tokens up to DDMNAME.
			for {
				token := lexer.NextToken()
				if token.Type == TokenEOF || token.Type == TokenError {
					break
				}
				gotTokens = append(gotTokens, token)
				if token.Type == TokenIdentifier && token.Literal == "DDMNAME" {
					break
				}
			}

			// Verify tokens before opaque span match expectations.
			if len(gotTokens) != len(tc.wantTokensBeforeSQL) {
				t.Fatalf("tokens before ScanOpaqueSpan: got %d, want %d", len(gotTokens), len(tc.wantTokensBeforeSQL))
			}

			// Call ScanOpaqueSpan — this method does not exist yet (ES-2 green will add it).
			// The test assumes the signature: ScanOpaqueSpan() (Token, bool)
			// where the bool indicates whether the span was unterminated.
			opaqueToken, unterminated := lexer.ScanOpaqueSpan()

			// Verify the opaque token type.
			if opaqueToken.Type != TokenSQLOpaque {
				t.Errorf("ScanOpaqueSpan returned type %d, want TokenSQLOpaque", opaqueToken.Type)
			}

			// Verify the interior is verbatim (including newlines, *, /*, etc. without re-lexing).
			if opaqueToken.Literal != tc.wantOpaqueInterior {
				t.Errorf("opaque literal mismatch.\nGot:\n%q\nWant:\n%q", opaqueToken.Literal, tc.wantOpaqueInterior)
			}

			// Verify it's not unterminated (proper >> closure).
			if unterminated {
				t.Errorf("ScanOpaqueSpan reported unterminated=true, but test input has closing >>")
			}
		})
	}
}

// TestLexer_SQLOpaqueSpan_LineStateAfterClose (ES-2 refactor) verifies that the
// lexer's line-tracking state is consistent after ScanOpaqueSpan returns:
//
//   - startLine/startCol of the returned token point to << (not pre-whitespace).
//   - lineHasNonWhitespace is true after >> (so a subsequent * on the same line is
//     NOT treated as a full-line comment by NextToken).
//   - lineHasNonWhitespace is false after crossing a newline inside the span, so a
//   - at the start of the line following the >> IS treated as a full-line comment.
func TestLexer_SQLOpaqueSpan_LineStateAfterClose(t *testing.T) {
	t.Run("token_position_marks_open_delimiter", func(t *testing.T) {
		// << is at line 1 col 1 (no leading whitespace after DDMNAME).
		input := "PROCESS SQL DDMNAME << body >>"
		lexer := NewLexer(input)
		for {
			tok := lexer.NextToken()
			if tok.Type == TokenEOF || tok.Type == TokenError {
				break
			}
			if tok.Type == TokenIdentifier && tok.Literal == "DDMNAME" {
				break
			}
		}
		opaqueToken, unterminated := lexer.ScanOpaqueSpan()
		if unterminated {
			t.Fatal("expected terminated span, got unterminated=true")
		}
		// The token position must mark the << delimiter, not pre-whitespace.
		if opaqueToken.Line != 1 {
			t.Errorf("opaqueToken.Line = %d, want 1 (position of <<)", opaqueToken.Line)
		}
		// Column of << = column of 'D'+len("DDMNAME")+" " = 21 (PROCESS=1..7 + space + SQL=9..11 + space + DDMNAME=13..19 + space=20 + <<starts at 21)
		if opaqueToken.Column != 21 {
			t.Errorf("opaqueToken.Column = %d, want 21 (column of <<)", opaqueToken.Column)
		}
	})

	t.Run("star_on_line_after_close_is_comment", func(t *testing.T) {
		// >> closes the span; the next line starts with *, which must be a full-line comment.
		input := "PROCESS SQL DDMNAME << body\n>>\n* this must be a comment\nCALLNAT 'X'"
		lexer := NewLexer(input)
		for {
			tok := lexer.NextToken()
			if tok.Type == TokenEOF || tok.Type == TokenError {
				break
			}
			if tok.Type == TokenIdentifier && tok.Literal == "DDMNAME" {
				break
			}
		}
		_, unterminated := lexer.ScanOpaqueSpan()
		if unterminated {
			t.Fatal("expected terminated span, got unterminated=true")
		}

		// Next token must be the full-line comment (not a * operator).
		next := lexer.NextToken()
		if next.Type != TokenComment {
			t.Errorf("NextToken after >>: got type %d literal %q, want TokenComment", next.Type, next.Literal)
		}
	})

	t.Run("star_on_same_line_as_close_is_operator", func(t *testing.T) {
		// >> and * appear on the same physical line; * must be an operator (not a comment).
		input := "PROCESS SQL DDMNAME << body >> * 2"
		lexer := NewLexer(input)
		for {
			tok := lexer.NextToken()
			if tok.Type == TokenEOF || tok.Type == TokenError {
				break
			}
			if tok.Type == TokenIdentifier && tok.Literal == "DDMNAME" {
				break
			}
		}
		_, unterminated := lexer.ScanOpaqueSpan()
		if unterminated {
			t.Fatal("expected terminated span, got unterminated=true")
		}

		// Next token must be * as an operator (>> is non-whitespace, so not line-start).
		next := lexer.NextToken()
		if next.Type != TokenOperator || next.Literal != "*" {
			t.Errorf("NextToken after >>: got type %d literal %q, want TokenOperator '*'", next.Type, next.Literal)
		}
	})
}

// TestLexer_SQLOpaqueSpan_Unterminated (ES-2) verifies that an unterminated << span
// (no closing >>) does not panic, does not loop forever, and returns the interior-to-EOF
// as the literal with an unterminated signal. AC-4 from the ES-2 task.
func TestLexer_SQLOpaqueSpan_Unterminated(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		expectedLiteral    string
		expectUnterminated bool
	}{
		{
			name:               "unterminated_double_lt_at_eof",
			input:              "PROCESS SQL DDMNAME << UPDATE table SET col = #var",
			expectedLiteral:    " UPDATE table SET col = #var",
			expectUnterminated: true,
		},
		{
			name:               "unterminated_double_lt_multi_line",
			input:              "PROCESS SQL DDMNAME << UPDATE table\nSET col = #var\n* comment chars stay\n/* comment not lexed",
			expectedLiteral:    " UPDATE table\nSET col = #var\n* comment chars stay\n/* comment not lexed",
			expectUnterminated: true,
		},
		{
			name:               "unterminated_empty_span",
			input:              "PROCESS SQL DDMNAME <<",
			expectedLiteral:    "",
			expectUnterminated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)

			// Consume tokens up to DDMNAME.
			for {
				token := lexer.NextToken()
				if token.Type == TokenEOF || token.Type == TokenError {
					break
				}
				if token.Type == TokenIdentifier && token.Literal == "DDMNAME" {
					break
				}
			}

			// Call ScanOpaqueSpan — this method does not exist yet.
			// If it panics or loops forever, the test framework detects it.
			// The signature is assumed: ScanOpaqueSpan() (Token, bool)
			opaqueToken, unterminated := lexer.ScanOpaqueSpan()

			// Verify the token type.
			if opaqueToken.Type != TokenSQLOpaque {
				t.Errorf("ScanOpaqueSpan type: got %d, want TokenSQLOpaque", opaqueToken.Type)
			}

			// Verify the literal matches expected interior-to-EOF.
			if opaqueToken.Literal != tc.expectedLiteral {
				t.Errorf("ScanOpaqueSpan literal mismatch.\nGot:\n%q\nWant:\n%q", opaqueToken.Literal, tc.expectedLiteral)
			}

			// Verify the unterminated signal.
			if unterminated != tc.expectUnterminated {
				t.Errorf("ScanOpaqueSpan unterminated: got %v, want %v", unterminated, tc.expectUnterminated)
			}
		})
	}
}
