// Package natural provides Natural language analysis via a hand-written lexer
// and recursive-descent parser. This file contains the lexer implementation.
package natural

// TokenType represents the type of a lexer token.
type TokenType int

const (
	// TokenKeyword represents Natural keywords (CALLNAT, PERFORM, FETCH, etc.)
	TokenKeyword TokenType = iota

	// TokenIdentifier represents identifiers (variable names, program names, etc.)
	TokenIdentifier

	// TokenLiteralString represents string literals
	TokenLiteralString

	// TokenLiteralNumeric represents numeric literals
	TokenLiteralNumeric

	// TokenOperator represents operators (=, <>, +, -, etc.)
	TokenOperator

	// TokenPunctuation represents punctuation (, ; : ( ) [ ] { }, etc.)
	TokenPunctuation

	// TokenComment represents comments
	TokenComment

	// TokenSQLOpaque represents an opaque SQL span (<<...>>) — ES-2 feature.
	// The Literal field contains the raw interior text between << and >>,
	// with no inner tokenization or comment scanning. Used by the parser
	// when in SQL context (PROCESS SQL body).
	TokenSQLOpaque

	// TokenEOF represents end of file
	TokenEOF

	// TokenError represents a lexical error
	TokenError
)

// Token represents a single lexer token.
type Token struct {
	Type    TokenType
	Literal string
	Column  int
	Line    int
}

// Lexer tokenizes Natural source code.
type Lexer struct {
	input                string
	pos                  int
	line                 int
	col                  int
	lineHasNonWhitespace bool // tracks if current line has produced a non-whitespace token
}

// NewLexer creates a new lexer for the given input.
func NewLexer(input string) *Lexer {
	return &Lexer{input: input, pos: 0, line: 1, col: 1, lineHasNonWhitespace: false}
}

// skipWhitespace advances past whitespace characters (space, tab, LF, CR, CRLF),
// updating line/column counters and resetting lineHasNonWhitespace on each new line.
// It stops at the first non-whitespace character or EOF. Both NextToken and
// ScanOpaqueSpan use this helper so CRLF handling and position tracking stay
// consistent in one place.
func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\r' {
			l.line++
			l.col = 1
			l.lineHasNonWhitespace = false // reset for new line
			l.pos++
			// Consume \n of a CRLF pair without a second line increment.
			if l.pos < len(l.input) && l.input[l.pos] == '\n' {
				l.pos++
			}
		} else if ch == '\n' {
			l.line++
			l.col = 1
			l.lineHasNonWhitespace = false // reset for new line
			l.pos++
		} else if ch == ' ' || ch == '\t' {
			l.col++
			l.pos++
		} else {
			break
		}
	}
}

// NextToken returns the next token from the input.
func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	// Check for EOF
	if l.pos >= len(l.input) {
		return Token{Type: TokenEOF, Line: l.line, Column: l.col}
	}

	ch := l.input[l.pos]

	// Check if we are at the start of a line (no non-whitespace tokens yet on this line).
	isAtLineStart := !l.lineHasNonWhitespace

	// Full-line comment (* or ** at start of line)
	if ch == '*' && isAtLineStart {
		startCol := l.col
		start := l.pos
		l.consumeToEOL()
		return l.returnToken(Token{Type: TokenComment, Literal: l.input[start:l.pos], Line: l.line, Column: startCol})
	}

	// Identifier or keyword.
	// Natural identifiers may start with a letter, #, &, or @ (user/system variable
	// prefixes) and continue with letters, digits, _, or - (hyphens).  A hyphen is
	// consumed as part of the identifier only when the very next character is also an
	// identifier-body character (no surrounding whitespace), so that arithmetic
	// expressions like "A - B" (spaces around the minus) still tokenise as three
	// tokens: identifier, operator, identifier.
	if l.isIdentStart(ch) {
		startCol := l.col
		start := l.pos
		for l.pos < len(l.input) {
			c := l.input[l.pos]
			if l.isIdentBody(c) {
				l.pos++
				l.col++
			} else if c == '-' && l.pos+1 < len(l.input) && l.isIdentBody(l.input[l.pos+1]) {
				// Hyphen immediately followed by an identifier-body character: part of the name.
				l.pos++
				l.col++
			} else {
				break
			}
		}
		literal := l.input[start:l.pos]
		normalized := l.uppercase(literal)
		if l.isKeyword(normalized) {
			return l.returnToken(Token{Type: TokenKeyword, Literal: normalized, Line: l.line, Column: startCol})
		}
		return l.returnToken(Token{Type: TokenIdentifier, Literal: normalized, Line: l.line, Column: startCol})
	}

	// String literal: collect the content between single quotes and include the
	// surrounding quotes in the token literal (e.g. 'MYPROG' → "'MYPROG'").
	// The parser's unquoteString helper strips the quotes when extracting targets.
	if ch == '\'' {
		startCol := l.col
		start := l.pos
		l.pos++ // skip opening quote
		l.col++ // advance column for opening quote
		for l.pos < len(l.input) {
			if l.input[l.pos] == '\'' {
				l.pos++ // skip closing quote
				l.col++ // advance column for closing quote
				content := l.input[start+1 : l.pos-1]
				return l.returnToken(Token{Type: TokenLiteralString, Literal: "'" + content + "'", Line: l.line, Column: startCol})
			}
			l.pos++
			l.col++
		}
		// Unterminated string: no closing quote found; return the content without
		// surrounding quotes (caller must tolerate a non-quoted literal here).
		return l.returnToken(Token{Type: TokenLiteralString, Literal: l.input[start+1:], Line: l.line, Column: startCol})
	}

	// Numeric literal
	if ch >= '0' && ch <= '9' {
		startCol := l.col
		start := l.pos
		// Integer part
		for l.pos < len(l.input) {
			c := l.input[l.pos]
			if c >= '0' && c <= '9' {
				l.pos++
				l.col++
			} else {
				break
			}
		}
		// Fractional part
		if l.pos < len(l.input) && l.input[l.pos] == '.' {
			l.pos++
			l.col++
			for l.pos < len(l.input) {
				c := l.input[l.pos]
				if c >= '0' && c <= '9' {
					l.pos++
					l.col++
				} else {
					break
				}
			}
		}
		// Scientific notation
		if l.pos < len(l.input) {
			c := l.input[l.pos]
			if c == 'E' || c == 'e' {
				l.pos++
				l.col++
				if l.pos < len(l.input) {
					c = l.input[l.pos]
					if c == '+' || c == '-' {
						l.pos++
						l.col++
					}
				}
				for l.pos < len(l.input) {
					c := l.input[l.pos]
					if c >= '0' && c <= '9' {
						l.pos++
						l.col++
					} else {
						break
					}
				}
			}
		}
		return l.returnToken(Token{Type: TokenLiteralNumeric, Literal: l.input[start:l.pos], Line: l.line, Column: startCol})
	}

	// Rest-of-line comment (/* ... to EOL).
	// The /* sequence starts the comment; everything to the physical end of the line
	// is part of the comment literal — there is no */ closer and the comment never
	// spans lines.  This applies regardless of where /* appears on the line.
	if ch == '/' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '*' {
		startCol := l.col
		start := l.pos
		l.consumeToEOL()
		return l.returnToken(Token{Type: TokenComment, Literal: l.input[start:l.pos], Line: l.line, Column: startCol})
	}

	// Multi-character operators
	if ch == '<' && l.pos+1 < len(l.input) {
		next := l.input[l.pos+1]
		if next == '>' {
			startCol := l.col
			l.pos += 2
			l.col += 2
			return l.returnToken(Token{Type: TokenOperator, Literal: "<>", Line: l.line, Column: startCol})
		}
		if next == '=' {
			startCol := l.col
			l.pos += 2
			l.col += 2
			return l.returnToken(Token{Type: TokenOperator, Literal: "<=", Line: l.line, Column: startCol})
		}
	}
	if ch == '>' && l.pos+1 < len(l.input) {
		next := l.input[l.pos+1]
		if next == '=' {
			startCol := l.col
			l.pos += 2
			l.col += 2
			return l.returnToken(Token{Type: TokenOperator, Literal: ">=", Line: l.line, Column: startCol})
		}
	}
	if ch == ':' && l.pos+1 < len(l.input) {
		next := l.input[l.pos+1]
		if next == '=' {
			startCol := l.col
			l.pos += 2
			l.col += 2
			return l.returnToken(Token{Type: TokenOperator, Literal: ":=", Line: l.line, Column: startCol})
		}
	}

	// Single-character operators.  Note: '&' was removed from this list because
	// Natural uses & as an identifier-prefix character for ampersand variables.
	if ch == '=' || ch == '+' || ch == '-' || ch == '*' || ch == '/' ||
		ch == '|' || ch == '!' || ch == '^' || ch == '<' || ch == '>' {
		startCol := l.col
		l.pos++
		l.col++
		return l.returnToken(Token{Type: TokenOperator, Literal: string(ch), Line: l.line, Column: startCol})
	}

	// Punctuation.  Note: '@' was removed from this list because it is handled by
	// the identifier branch above (Natural uses @ as an identifier-continuation char).
	if ch == ',' || ch == ';' || ch == ':' || ch == '(' || ch == ')' ||
		ch == '[' || ch == ']' || ch == '{' || ch == '}' || ch == '.' {
		startCol := l.col
		l.pos++
		l.col++
		return l.returnToken(Token{Type: TokenPunctuation, Literal: string(ch), Line: l.line, Column: startCol})
	}

	// Unknown character - skip it
	l.pos++
	l.col++
	return l.NextToken()
}

// isIdentStart reports whether c may begin a Natural identifier.
// Identifiers may start with a letter or one of the standard Natural
// variable-prefix characters: # (user vars), & (ampersand vars), @ (system vars).
func (l *Lexer) isIdentStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '#' || c == '&' || c == '@'
}

// isIdentBody reports whether c may appear in the body of a Natural identifier
// (after the first character).  Hyphens are handled separately in the scan loop
// because they require a lookahead check (the next char must also be a body char).
func (l *Lexer) isIdentBody(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '#' || c == '&' || c == '@'
}

// uppercase converts a string to uppercase.
func (l *Lexer) uppercase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			result[i] = c - 'a' + 'A'
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// returnToken marks that a non-whitespace token is being returned and returns it.
// This updates the line-start tracker so future tokens on this line know they're not at line-start.
func (l *Lexer) returnToken(token Token) Token {
	l.lineHasNonWhitespace = true
	return token
}

// consumeToEOL advances pos/col up to but not including the next line terminator
// (\n or \r) or EOF, whichever comes first. Both comment scan paths use this
// helper so CRLF line endings (\r\n) are handled uniformly: the \r is excluded
// from the comment literal, and the outer whitespace loop correctly treats \r\n
// as a single line terminator (incrementing the line counter once, then consuming
// the \n without a second increment). Lone \r and lone \n each count as one line
// terminator.
func (l *Lexer) consumeToEOL() {
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == '\n' || c == '\r' {
			break
		}
		l.pos++
		l.col++
	}
}

// isKeyword returns true if the string is a Natural keyword.
func (l *Lexer) isKeyword(s string) bool {
	switch s {
	case "CALLNAT", "PERFORM", "INCLUDE", "FETCH", "RUN", "DEFINE", "END",
		"IF", "THEN", "ELSE", "ENDIF", "MOVE", "COMPUTE", "TO", "WRITE", "READ", "STORE",
		"DELETE", "UPDATE", "INSERT", "SELECT", "FROM", "WHERE", "SET",
		"GET", "PUT", "EXIT", "RETURN", "WHILE", "WEND", "FOR", "NEXT",
		"DO", "LOOP", "UNTIL", "BREAK", "CONTINUE", "GOTO", "CALL",
		"EXITNAT", "PERFORMNAT", "DISPLAY", "PROMPT", "ACCEPT", "INPUT",
		"OUTPUT", "PRINT", "OPEN", "CLOSE", "LOCK", "UNLOCK", "LOCKNAT",
		"UNLOCKNAT", "SEARCH", "SEARCHNAT", "FIND", "FINDNAT", "REPLACE",
		"REPLACENAT", "SUBSTITUTE", "SUBST", "CONCAT", "CONCATENATE",
		"LENGTH", "LEN", "TRIM", "LTRIM", "RTRIM", "UPPER", "LOWER",
		"SUBSTR", "SUBSTRING", "INDEX", "POSITION", "ASSERT", "ASSERTNAT",
		"ERROR", "RAISE", "CATCH", "TRY", "ABORT", "TERMINATE", "STOP",
		"HALT", "RESUME", "RETRY", "RESTART", "REINIT", "RESET", "CLEAR",
		"FLUSH", "SYNC", "SYNCNAT", "FLUSHNAT", "ID", "IDNAT", "VERSION",
		"VERSIONNAT", "DATE", "DATENAT", "TIME", "TIMENAT", "DATETIME",
		"DATETIMENAT", "DATA", "LOCAL", "PARAMETER", "REDEFINE",
		// Embedded-SQL keywords (ES-1): statement/clause words that must lex as
		// TokenKeyword so the parser can dispatch on them.  SELECT/FROM/WHERE/SET/
		// INSERT/UPDATE/DELETE/STORE/LOOP are already listed above.
		"SINGLE", "INTO", "VALUES", "MERGE", "COMMIT", "ROLLBACK", "CALLDBPROC",
		"PROCESS", "RESULT", "END-SELECT", "END-RESULT":
		return true
	default:
		return false
	}
}

// ScanOpaqueSpan (ES-2) scans a <<...>> opaque SQL span.
// It is called by the parser after detecting "PROCESS SQL" to capture the raw
// interior text (no inner tokenization) as a single TokenSQLOpaque token.
// The method returns:
//   - Token: a TokenSQLOpaque with Literal = raw interior text (everything between << and >>)
//   - bool: true if the span was unterminated (no closing >> before EOF)
//
// Precondition: the lexer's current position is immediately after the DDM name
// (or wherever << is expected to be lexed next). The method consumes the << and >>
// delimiters and returns the interior.
//
// Unterminated spans: if << is found but no >> appears before EOF, the interior-to-EOF
// is returned as the Literal with unterminated=true. The method never panics or loops.
func (l *Lexer) ScanOpaqueSpan() (Token, bool) {
	// Skip leading whitespace first so that startLine/startCol mark the << delimiter,
	// consistent with NextToken's convention of recording position after whitespace skip.
	l.skipWhitespace()

	// Record position of the << delimiter (or EOF if nothing found).
	startLine := l.line
	startCol := l.col

	// Expect << at the current position. The guard handles both EOF (pos >= len)
	// and single-remaining-char (pos+1 >= len) cases, ensuring l.input[pos+1] is safe.
	if l.pos+1 >= len(l.input) || l.input[l.pos] != '<' || l.input[l.pos+1] != '<' {
		// Parser precondition violated or no opening delimiter; return empty without
		// signalling unterminated (the << body was never opened).
		return Token{Type: TokenSQLOpaque, Literal: "", Line: startLine, Column: startCol}, false
	}

	// Consume the << opening delimiter.
	l.pos += 2
	l.col += 2
	l.lineHasNonWhitespace = true // << is non-whitespace on its line

	// Record start of interior content (everything between << and >>).
	interiorStart := l.pos

	// Scan for the closing >>. Each iteration advances l.pos by at least 1, so
	// termination is guaranteed within len(l.input) steps — no infinite loop is possible.
	for l.pos < len(l.input) {
		ch := l.input[l.pos]

		switch {
		case ch == '\r':
			// CR or CRLF: advance line, reset column and line-start tracker.
			l.line++
			l.col = 1
			l.lineHasNonWhitespace = false
			l.pos++
			// Consume \n of a CRLF pair without a second line increment.
			if l.pos < len(l.input) && l.input[l.pos] == '\n' {
				l.pos++
			}
		case ch == '\n':
			// LF: advance line, reset column and line-start tracker.
			l.line++
			l.col = 1
			l.lineHasNonWhitespace = false
			l.pos++
		case ch == '>' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '>':
			// Found closing >>: capture interior, consume delimiter, update state.
			interior := l.input[interiorStart:l.pos]
			l.pos += 2
			l.col += 2
			l.lineHasNonWhitespace = true // >> is non-whitespace on its line
			return Token{Type: TokenSQLOpaque, Literal: interior, Line: startLine, Column: startCol}, false
		default:
			// Interior character: advance position and column.
			l.pos++
			l.col++
		}
	}

	// Reached EOF without finding >>. Return interior-to-EOF with unterminated=true.
	interior := l.input[interiorStart:]
	return Token{Type: TokenSQLOpaque, Literal: interior, Line: startLine, Column: startCol}, true
}

// ScanOpaqueSpanFrom captures a PROCESS SQL <<...>> opaque span in the situation
// where the parser's one-token lookahead has already lexed the FIRST '<' of the
// '<<' opener (as a lone operator token). It rewinds the lexer past that opener
// token — by the token literal's length, guarded against underflow — so that
// ScanOpaqueSpan sees the intact '<<', then delegates to it. A '<' operator token
// is always a single character on one line, so rewinding pos/col by its length is
// exact and cannot cross a line boundary.
//
// In addition to the opaque token and the unterminated flag, it returns the source
// position (line, column) immediately AFTER the closing '>>' (or at EOF for an
// unterminated span), so the caller can build an accurate body/statement range
// without re-deriving it by counting newlines. The '>>' therefore ends at
// (endLine, endCol-1).
func (l *Lexer) ScanOpaqueSpanFrom(opener Token) (tok Token, endLine, endCol int, unterminated bool) {
	n := len(opener.Literal)
	if n > 0 {
		if l.pos >= n {
			l.pos -= n
		} else {
			l.pos = 0
		}
		if l.col > n {
			l.col -= n
		} else {
			l.col = 1
		}
	}
	tok, unterminated = l.ScanOpaqueSpan()
	return tok, l.line, l.col, unterminated
}
