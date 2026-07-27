package natural

import (
	"github.com/dkrieg/natural-lsp/internal/model"
)

// Phase A: Lexical token classification (Phase B identifier/call/DDM semantic classification is deferred).
// This implementation walks the lexer and emits SemanticToken for unambiguous lexical classes:
// keyword, comment, string, number, operator. Identifiers, punctuation, SQL opaque, and error
// tokens are NOT emitted in Phase A (identifiers are deferred to Phase B).
//
// Each token's Range is computed from actual source bytes (not Token.Literal length),
// because Literal is normalized (upper-cased, quotes stripped).

// semanticTokensPhaseA classifies tokens from source content via lexical analysis.
// It returns a non-nil, possibly-empty slice of classified tokens in document order (FR-43).
func semanticTokensPhaseA(path string, content string) []model.SemanticToken {
	var tokens []model.SemanticToken

	lexer := NewLexer(content)
	contentBytes := []byte(content)

	for {
		tok := lexer.NextToken()
		if tok.Type == TokenEOF {
			break
		}

		// Determine the semantic type and whether to emit this token.
		var semanticType model.SemanticTokenType
		shouldEmit := true

		switch tok.Type {
		case TokenKeyword:
			semanticType = model.SemanticTokenTypeKeyword
		case TokenComment:
			semanticType = model.SemanticTokenTypeComment
		case TokenLiteralString:
			semanticType = model.SemanticTokenTypeString
		case TokenLiteralNumeric:
			semanticType = model.SemanticTokenTypeNumber
		case TokenOperator:
			semanticType = model.SemanticTokenTypeOperator
		case TokenIdentifier, TokenPunctuation, TokenSQLOpaque, TokenError:
			// Phase A excludes these; Phase B will add identifier classification.
			shouldEmit = false
		default:
			shouldEmit = false
		}

		if !shouldEmit {
			continue
		}

		// Compute the real source Range from the actual bytes in content.
		// The token's Line and Column are 1-based, matching model.Position convention.
		// We must find the token's actual byte position and width in the source.
		rangeStart, rangeEnd := computeTokenRange(contentBytes, tok)

		tokens = append(tokens, model.SemanticToken{
			Range: model.Range{
				Start: rangeStart,
				End:   rangeEnd,
			},
			Type:      semanticType,
			Modifiers: 0, // Phase A has no modifiers.
		})
	}

	return tokens
}

// computeTokenRange computes the Range of a token from its actual source bytes.
// The lexer provides Line and Column (1-based); we scan the source to find the
// token's actual byte span and compute the end position.
//
// The model.Range uses inclusive-end semantics: both Start and End point to actual
// positions within the token.
func computeTokenRange(contentBytes []byte, tok Token) (model.Position, model.Position) {
	startLine := tok.Line
	startCol := tok.Column

	// Scan the content to find the byte offset of the token's start position.
	line := 1
	bytePos := 0
	colInLine := 1

	// Advance byte-by-byte until we reach (line, col).
	for bytePos < len(contentBytes) {
		if line == startLine && colInLine == startCol {
			break // Found the token's start position.
		}

		if contentBytes[bytePos] == '\r' {
			line++
			colInLine = 1
			bytePos++
			// Skip \n of CRLF.
			if bytePos < len(contentBytes) && contentBytes[bytePos] == '\n' {
				bytePos++
			}
		} else if contentBytes[bytePos] == '\n' {
			line++
			colInLine = 1
			bytePos++
		} else {
			colInLine++
			bytePos++
		}
	}

	// Now bytePos is at the token's starting byte.
	tokenStart := bytePos

	// Find the token's end byte by scanning forward based on token type.
	tokenEnd := tokenStart
	switch tok.Type {
	case TokenComment:
		// A comment runs to EOL (either * at line start or /* anywhere).
		// Scan until we hit \n, \r, or EOF.
		for tokenEnd < len(contentBytes) {
			if contentBytes[tokenEnd] == '\n' || contentBytes[tokenEnd] == '\r' {
				break
			}
			tokenEnd++
		}
		// tokenEnd now points to the newline (or past EOF). Back up to the last byte of the comment.
		if tokenEnd > tokenStart {
			tokenEnd--
		}

	case TokenLiteralString:
		// A string literal is delimited by single quotes. The token starts at the opening quote.
		if tokenEnd < len(contentBytes) && contentBytes[tokenEnd] == '\'' {
			tokenEnd++ // Move past opening quote.
			// Scan for the closing quote.
			for tokenEnd < len(contentBytes) {
				if contentBytes[tokenEnd] == '\'' {
					// Found closing quote. Include it in the token.
					break
				}
				tokenEnd++
			}
		}
		// tokenEnd now points to the closing quote (or past EOF if unterminated).
		// The inclusive-end position is the closing quote itself (or last byte if unterminated).

	case TokenLiteralNumeric:
		// Numeric literals can include digits, optional '.', and scientific notation.
		// Scan while we see digit characters, '.', '+', '-', 'E', 'e'.
		for tokenEnd < len(contentBytes) {
			ch := contentBytes[tokenEnd]
			if (ch >= '0' && ch <= '9') || ch == '.' || ch == '+' || ch == '-' || ch == 'e' || ch == 'E' {
				tokenEnd++
			} else {
				break
			}
		}
		// tokenEnd now points past the last digit/operator. Back up.
		if tokenEnd > tokenStart {
			tokenEnd--
		}

	case TokenOperator:
		// Operators are 1 or 2 bytes: =, +, -, *, /, <>, <=, >=, :=, etc.
		// Scan to consume the operator.
		if tokenEnd < len(contentBytes) {
			ch := contentBytes[tokenEnd]
			tokenEnd++
			// Check for two-character operators.
			if tokenEnd < len(contentBytes) {
				next := contentBytes[tokenEnd]
				if (ch == '<' && (next == '>' || next == '=')) ||
					(ch == '>' && next == '=') ||
					(ch == ':' && next == '=') ||
					(ch == '*' && next == '*') { // ** might exist, though unlikely in operators
					tokenEnd++
				}
			}
		}
		// tokenEnd now points past the operator. Back up to the last byte.
		if tokenEnd > tokenStart {
			tokenEnd--
		}

	case TokenKeyword:
		// Keywords are continuous alphanumeric sequences (no hyphens in mid-token for keywords).
		// Scan while we see letters, digits, underscores, and hyphens (if followed by letter).
		for tokenEnd < len(contentBytes) {
			ch := contentBytes[tokenEnd]
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
				tokenEnd++
			} else if ch == '-' && tokenEnd+1 < len(contentBytes) {
				// Check if hyphen is followed by an identifier body character.
				next := contentBytes[tokenEnd+1]
				if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') {
					tokenEnd++
				} else {
					break
				}
			} else {
				break
			}
		}
		// tokenEnd now points past the last character. Back up.
		if tokenEnd > tokenStart {
			tokenEnd--
		}

	default:
		// For other token types, assume single byte (should not reach here in Phase A).
		if tokenEnd < len(contentBytes) {
			tokenEnd++
		}
		if tokenEnd > tokenStart {
			tokenEnd--
		}
	}

	// Now convert the end byte offset back to a (line, column) position.
	endLine := startLine
	endCol := startCol
	bytePos = tokenStart

	// Scan from token start to token end, tracking line/column.
	for bytePos < tokenEnd && bytePos < len(contentBytes) {
		if contentBytes[bytePos] == '\r' {
			endLine++
			endCol = 1
			bytePos++
			// Skip \n of CRLF.
			if bytePos < len(contentBytes) && contentBytes[bytePos] == '\n' {
				bytePos++
			}
		} else if contentBytes[bytePos] == '\n' {
			endLine++
			endCol = 1
			bytePos++
		} else {
			endCol++
			bytePos++
		}
	}

	return model.Position{Line: startLine, Column: startCol},
		model.Position{Line: endLine, Column: endCol}
}
