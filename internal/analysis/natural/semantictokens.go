package natural

import (
	"sort"

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

// semanticTokensPhaseBIdentifiers classifies variable and parameter identifiers.
// It builds a lookup of declared variable names and their SectionKind, then walks
// the token stream to classify matched identifiers as variable or parameter.
// Declaration sites receive the `declaration` modifier; use sites do not.
// Undeclared identifiers are not emitted (fall back to nothing).
//
// This is a shared Phase-B classifier (T7) that is extended by T8-T10 for other
// identifier categories (calls, DDM/view, system vars, etc.).
func semanticTokensPhaseBIdentifiers(path string, content string) []model.SemanticToken {
	contentBytes := []byte(content)

	// Parse to get the AST and extract definitions.
	lexer := NewLexer(content)
	parser := NewParser(lexer)
	ast, _ := parser.Parse()

	if ast == nil {
		return []model.SemanticToken{}
	}

	// Extract data definitions to build a name → (SectionKind, NameRange) lookup.
	definitions := extractDefinitions(ast)
	if len(definitions) == 0 {
		return []model.SemanticToken{}
	}

	// Build a lookup: map variable name → definition info (SectionKind, NameRange for declaration site).
	// Names are already upper-cased in definitions.
	varLookup := make(map[string]*model.DataDefinition)
	for i := range definitions {
		name := definitions[i].Name
		// Strip sigils (#, &, @, +) for lookup key, but keep them in the name.
		lookupKey := name
		if len(name) > 0 && (name[0] == '#' || name[0] == '&' || name[0] == '@' || name[0] == '+') {
			// Keep sigil in the key; exact match is needed.
		}
		varLookup[lookupKey] = &definitions[i]
	}

	// Walk the token stream and emit variable/parameter tokens.
	var tokens []model.SemanticToken
	lexer = NewLexer(content)

	for {
		tok := lexer.NextToken()

		if tok.Type == TokenEOF {
			break
		}

		// Only classify identifiers (not comments, strings, keywords, etc.).
		if tok.Type != TokenIdentifier {
			continue
		}

		// Exclude *-system variables and &-dynamic variables.
		if len(tok.Literal) > 0 && (tok.Literal[0] == '*' || tok.Literal[0] == '&') {
			continue
		}

		// Check if this identifier matches a declared variable.
		def, exists := varLookup[tok.Literal]
		if !exists {
			continue // Not a declared variable; don't emit.
		}

		// Determine the semantic type based on SectionKind.
		var semanticType model.SemanticTokenType
		if def.SectionKind == "parameter" {
			semanticType = model.SemanticTokenTypeParameter
		} else {
			semanticType = model.SemanticTokenTypeVariable
		}

		// Compute the real source Range for this identifier token.
		startRange, endRange := computeTokenRange(contentBytes, tok)

		// Determine if this is a declaration site (computed range matches the NameRange).
		var modifiers model.SemanticTokenModifier
		if startRange.Line == def.NameRange.Start.Line && startRange.Column == def.NameRange.Start.Column {
			modifiers = model.SemanticTokenModifierDeclaration
		}

		tokens = append(tokens, model.SemanticToken{
			Range:     model.Range{Start: startRange, End: endRange},
			Type:      semanticType,
			Modifiers: modifiers,
		})
	}

	return tokens
}

// semanticTokensPhaseB merges Phase A lexical tokens with Phase B identifier classification.
// It returns a single document-ordered slice with no duplicates.
// Phase B includes:
// - T7: variable/parameter reclassification from identifiers
// - T8: call targets (CALLNAT/FETCH/RUN/PERFORM -> function)
// - T9+: other semantic classifications (DDM, system vars, etc.)
func semanticTokensPhaseB(path string, content string) []model.SemanticToken {
	phaseATokens := semanticTokensPhaseA(path, content)
	phaseBTokens := semanticTokensPhaseBIdentifiers(path, content)
	phaseBCallTokens := semanticTokensPhaseBCalls(path, content)

	// Merge all slices. Phase B tokens (identifiers, calls) override Phase A tokens at the same span.
	allTokens := append(phaseATokens, phaseBTokens...)
	allTokens = append(allTokens, phaseBCallTokens...)

	// Sort by start position, then by type/modifiers for stability.
	sort.SliceStable(allTokens, func(i, j int) bool {
		if allTokens[i].Range.Start.Line != allTokens[j].Range.Start.Line {
			return allTokens[i].Range.Start.Line < allTokens[j].Range.Start.Line
		}
		return allTokens[i].Range.Start.Column < allTokens[j].Range.Start.Column
	})

	// Deduplicate: if two tokens have the same start position, keep the LAST (Phase B wins over Phase A).
	seen := make(map[struct{ line, col int }]bool)
	var dedupedTokens []model.SemanticToken
	// Build a map of the last token at each position
	tokenMap := make(map[struct{ line, col int }]model.SemanticToken)
	for _, tok := range allTokens {
		key := struct{ line, col int }{tok.Range.Start.Line, tok.Range.Start.Column}
		tokenMap[key] = tok
	}
	// Reconstruct in sorted order
	seen = make(map[struct{ line, col int }]bool)
	for _, tok := range allTokens {
		key := struct{ line, col int }{tok.Range.Start.Line, tok.Range.Start.Column}
		if !seen[key] {
			dedupedTokens = append(dedupedTokens, tokenMap[key])
			seen[key] = true
		}
	}

	return dedupedTokens
}

// semanticTokensPhaseBCalls classifies call targets (CALLNAT/FETCH/RUN/PERFORM) as `function`.
// It extracts edges from the AST and emits function tokens for:
// - CALLNAT/FETCH/RUN literal targets (the target operand range -> function, overrides Phase A string)
// - PERFORM subroutine names (the identifier at the PERFORM site -> function)
// - DEFINE SUBROUTINE definition names (the name token -> function + definition modifier)
//
// Dynamic targets (EdgeCallsDynamic, EdgeNavigatesToDynamic) are NOT emitted here
// (they fall back to variable classification if declared, per T7).
func semanticTokensPhaseBCalls(path string, content string) []model.SemanticToken {
	// Parse to get the AST.
	lexer := NewLexer(content)
	parser := NewParser(lexer)
	ast, _ := parser.Parse()

	if ast == nil {
		return []model.SemanticToken{}
	}

	var tokens []model.SemanticToken

	// Extract edges to identify call targets.
	edges := extractEdges(ast)

	// Build a lookup: for PERFORM static targets, map the target name to whether it's resolvable.
	// (We'll skip dynamic PERFORM targets to let them fall back to variable classification.)
	performTargets := make(map[string]bool) // name -> is static (true) or dynamic (false)
	for _, edge := range edges {
		if edge.Kind == model.EdgePerforms {
			performTargets[edge.TargetName] = true // PERFORM targets are always identifiers, treated as static
		}
	}

	// 1. Classify static call targets (CALLNAT/FETCH/RUN with EdgeCalls/EdgeNavigatesTo).
	//    These are either quoted literals or (for FETCH/RUN) bare identifiers.
	//    The Target range on these edges points to the operand span.
	for _, edge := range edges {
		switch edge.Kind {
		case model.EdgeCalls, model.EdgeNavigatesTo:
			// These are static (literal) targets. The Target field is their span.
			// Emit a function token that OVERRIDES the Phase A string classification.
			if edge.Target.Start.Line > 0 { // valid Target range
				tokens = append(tokens, model.SemanticToken{
					Range:     edge.Target,
					Type:      model.SemanticTokenTypeFunction,
					Modifiers: 0,
				})
			}
		}
	}

	// 2. Classify PERFORM subroutine target names at the PERFORM site.
	//    For each PERFORM edge, find the identifier token in the statement that names the target.
	//    The edge.Source spans the whole PERFORM statement. We need to find the target identifier
	//    after the PERFORM keyword within that source range.
	//
	//    Since extractEdges widens the Source to include the target, we can use edge.Source.End
	//    to find the target name token. But we need to be careful: the Source might be
	//    "PERFORM MY-ROUTINE" where "MY-ROUTINE" is an identifier. We'll scan from the end of Source
	//    backwards or use the edge's TargetName to locate it.
	//
	//    Better approach: walk the AST's PerformStatements directly and classify each target.
	for _, perform := range ast.Performs {
		if perform.Target == "" {
			continue // skip malformed
		}
		// perform.TargetRange is the range of the target identifier in the PERFORM statement.
		// Emit a function token for this identifier (use site, no definition modifier).
		if perform.TargetRange.Start.Line > 0 {
			tokens = append(tokens, model.SemanticToken{
				Range:     perform.TargetRange,
				Type:      model.SemanticTokenTypeFunction,
				Modifiers: 0, // use site, not definition
			})
		}
	}

	// 3. Classify DEFINE SUBROUTINE definition names as function + definition modifier.
	for _, sub := range ast.Subroutines {
		if sub.Name == "" {
			continue // skip malformed
		}
		// sub.NameRange is the range of the subroutine name token.
		if sub.NameRange.Start.Line > 0 {
			tokens = append(tokens, model.SemanticToken{
				Range:     sub.NameRange,
				Type:      model.SemanticTokenTypeFunction,
				Modifiers: model.SemanticTokenModifierDefinition,
			})
		}
	}

	return tokens
}
