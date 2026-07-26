package natural

import "github.com/dkrieg/natural-lsp/internal/model"

// ExtractVariableRefs scans the source content for variable use-site references
// and returns them as a list of VariableRef values.
//
// This is a token-occurrence scanner that:
// - Scans the lexer token stream (so comments/string-literal tokens are excluded for free)
// - Captures every variable identifier occurrence in statement bodies
// - Normalizes names to upper-case
// - Strips array subscripts (e.g., #T(1:10) → #T, with index #I captured separately)
// - Excludes *-system variables (#DATE, #TIME, etc.)
// - Handles group-qualified names (#GROUP.FIELD) as specified by the design
//
// Returns a slice of VariableRef values, one per occurrence (may be empty, never nil).
// Never panics on arbitrary input (FR-43).
//
// Task 27 T2 (net-new work): variable use-site scanner behind the Analyzer seam.
// Reuses the scanOpaqueHostVars pattern from sql.go for range/normalization shape.
func ExtractVariableRefs(content string) []model.VariableRef {
	// Edge case: empty content.
	if content == "" {
		return []model.VariableRef{}
	}

	lexer := NewLexer(content)
	var refs []model.VariableRef
	inDataSection := false // Track whether we're inside a DEFINE DATA block.

	for {
		tok := lexer.NextToken()

		// Stop at EOF.
		if tok.Type == TokenEOF {
			break
		}

		// Track entry/exit of DEFINE DATA blocks.
		if tok.Type == TokenKeyword {
			if tok.Literal == "DEFINE" {
				// Peek ahead to see if the next token is "DATA"
				nextTok := lexer.NextToken()
				if nextTok.Type == TokenKeyword && nextTok.Literal == "DATA" {
					inDataSection = true
				}
				// Continue to next iteration, skipping any processing of DEFINE or nextTok
				continue
			}
		}

		// END-DEFINE is tokenized as an IDENTIFIER (not a keyword) because
		// the lexer doesn't have it in the keyword list. Check for it here.
		if tok.Type == TokenIdentifier && (tok.Literal == "END-DEFINE" || tok.Literal == "END-DATA") {
			inDataSection = false
			continue
		}

		// Skip all tokens if we're in a DEFINE DATA block.
		if inDataSection {
			continue
		}

		// Skip everything except identifiers.
		if tok.Type != TokenIdentifier {
			continue
		}

		// Exclude *-system variables (first character is *).
		if len(tok.Literal) > 0 && tok.Literal[0] == '*' {
			continue
		}

		// Exclude &-dynamic variables (first character is &).
		// These are modeled gaps per OQ-1.
		if len(tok.Literal) > 0 && tok.Literal[0] == '&' {
			continue
		}

		// Build the Range for this identifier.
		// Token.Column is 1-based; Token.Line is 1-based.
		// The token's end column is start column + length of the literal.
		startRange := model.Range{
			Start: model.Position{
				Line:   tok.Line,
				Column: tok.Column,
			},
			End: model.Position{
				Line:   tok.Line,
				Column: tok.Column + len(tok.Literal) - 1,
			},
		}

		// Emit a ref for this identifier.
		refs = append(refs, model.VariableRef{
			Name:  tok.Literal, // Already normalized to upper-case by the lexer.
			Range: startRange,
		})
	}

	// Ensure we always return a non-nil slice (even if empty).
	if refs == nil {
		return []model.VariableRef{}
	}
	return refs
}
