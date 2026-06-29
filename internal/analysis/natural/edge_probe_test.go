package natural

import (
	"testing"
)

func TestLexerEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		desc  string
		check func(t *testing.T, tokens []Token)
	}{
		{
			name:  "lone_slash_at_end_of_input",
			input: "A /",
			desc:  "lone / at EOF should produce identifier + operator, not panic",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 2 {
					t.Fatalf("want 2 tokens, got %d: %v", len(tokens), tokens)
				}
				if tokens[1].Type != TokenOperator || tokens[1].Literal != "/" {
					t.Errorf("want '/' operator, got %+v", tokens[1])
				}
			},
		},
		{
			name:  "slash_star_at_end_of_input",
			input: "A /*",
			desc:  "/* at end of input (empty comment to EOL) should produce comment with literal '/*'",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 2 {
					t.Fatalf("want 2 tokens, got %d: %v", len(tokens), tokens)
				}
				if tokens[1].Type != TokenComment {
					t.Errorf("want TokenComment, got %+v", tokens[1])
				}
				if tokens[1].Literal != "/*" {
					t.Errorf("want literal '/*', got %q", tokens[1].Literal)
				}
			},
		},
		{
			name:  "slash_followed_by_non_star",
			input: "A / B",
			desc:  "/ followed by non-* is division operator, not comment",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 3 {
					t.Fatalf("want 3 tokens, got %d: %v", len(tokens), tokens)
				}
				if tokens[1].Type != TokenOperator || tokens[1].Literal != "/" {
					t.Errorf("want '/' operator, got %+v", tokens[1])
				}
			},
		},
		{
			name:  "star_as_last_char_of_line",
			input: "A * B\n",
			desc:  "* at non-line-start is multiplication operator",
			check: func(t *testing.T, tokens []Token) {
				// A, *, B — star is operator, not comment
				if len(tokens) != 3 {
					t.Fatalf("want 3 tokens, got %d: %v", len(tokens), tokens)
				}
				if tokens[1].Type != TokenOperator || tokens[1].Literal != "*" {
					t.Errorf("want '*' operator, got %+v", tokens[1])
				}
			},
		},
		{
			name:  "star_at_column_1",
			input: "* comment\nA",
			desc:  "* at column 1 (first non-blank) is a full-line comment",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 2 {
					t.Fatalf("want 2 tokens, got %d: %v", len(tokens), tokens)
				}
				if tokens[0].Type != TokenComment {
					t.Errorf("want TokenComment at [0], got %+v", tokens[0])
				}
				if tokens[0].Column != 1 {
					t.Errorf("want column 1, got %d", tokens[0].Column)
				}
			},
		},
		{
			name:  "star_after_leading_spaces",
			input: "   * comment here\n",
			desc:  "* after leading spaces (first non-blank) is a full-line comment",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 1 {
					t.Fatalf("want 1 token, got %d: %v", len(tokens), tokens)
				}
				if tokens[0].Type != TokenComment {
					t.Errorf("want TokenComment, got %+v", tokens[0])
				}
				// Column should be 4 (first non-blank position)
				if tokens[0].Column != 4 {
					t.Errorf("want column 4, got %d", tokens[0].Column)
				}
			},
		},
		{
			name:  "star_after_leading_tabs",
			input: "\t\t* comment here\n",
			desc:  "* after leading tabs (first non-blank) is a full-line comment",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 1 {
					t.Fatalf("want 1 token, got %d: %v", len(tokens), tokens)
				}
				if tokens[0].Type != TokenComment {
					t.Errorf("want TokenComment, got %+v", tokens[0])
				}
			},
		},
		{
			name:  "crlf_stops_rest_of_line_comment",
			input: "A /* comment here\r\nB",
			desc:  "CRLF: /* comment must stop before \\r, not include \\r in literal",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 3 {
					t.Fatalf("want 3 tokens, got %d: %v", len(tokens), tokens)
				}
				comment := tokens[1]
				if comment.Type != TokenComment {
					t.Errorf("want TokenComment, got %+v", comment)
				}
				// Literal should not contain \r
				for _, ch := range comment.Literal {
					if ch == '\r' {
						t.Errorf("comment literal contains \\r: %q", comment.Literal)
					}
				}
				if comment.Literal != "/* comment here" {
					t.Errorf("want '/* comment here', got %q", comment.Literal)
				}
				// The whitespace loop now correctly treats \r\n as a single line terminator
				// (Task R2 remediation), so B lands on line 2 with CRLF (same as LF).
				// The important invariant is that \r is NOT in the comment literal.
				if tokens[2].Line != 2 {
					t.Errorf("token after CRLF should be on line 2 (\\r\\n is a single terminator), got %d", tokens[2].Line)
				}
			},
		},
		{
			name:  "crlf_stops_full_line_star_comment",
			input: "* comment\r\nA",
			desc:  "CRLF: full-line * comment must stop before \\r",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 2 {
					t.Fatalf("want 2 tokens, got %d: %v", len(tokens), tokens)
				}
				comment := tokens[0]
				if comment.Type != TokenComment {
					t.Errorf("want TokenComment, got %+v", comment)
				}
				for _, ch := range comment.Literal {
					if ch == '\r' {
						t.Errorf("comment literal contains \\r: %q", comment.Literal)
					}
				}
				if comment.Literal != "* comment" {
					t.Errorf("want '* comment', got %q", comment.Literal)
				}
			},
		},
		{
			name:  "slash_star_at_very_end_no_newline",
			input: "/*",
			desc:  "/* with nothing after is an empty rest-of-line comment at EOF",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 1 {
					t.Fatalf("want 1 token, got %d: %v", len(tokens), tokens)
				}
				if tokens[0].Type != TokenComment || tokens[0].Literal != "/*" {
					t.Errorf("want comment '/*', got %+v", tokens[0])
				}
			},
		},
		{
			name:  "star_at_very_end_no_newline",
			input: "* comment",
			desc:  "* line comment without trailing newline still works",
			check: func(t *testing.T, tokens []Token) {
				if len(tokens) != 1 {
					t.Fatalf("want 1 token, got %d: %v", len(tokens), tokens)
				}
				if tokens[0].Type != TokenComment || tokens[0].Literal != "* comment" {
					t.Errorf("want comment '* comment', got %+v", tokens[0])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer(tc.input)
			var tokens []Token
			for {
				tok := lexer.NextToken()
				if tok.Type == TokenEOF || tok.Type == TokenError {
					break
				}
				tokens = append(tokens, tok)
			}
			tc.check(t, tokens)
		})
	}
}
