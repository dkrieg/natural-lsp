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

	// Find the token's end byte.
	//
	// For NON-string, NON-comment tokens (keyword/identifier/number/operator/punctuation)
	// the lexer does NOT quote-strip and these tokens are ASCII, so len(tok.Literal) is the
	// true source byte width — using it is exact and avoids the greedy character-class rescan
	// that previously over-extended a numeric literal (e.g. "5-3": the "5" token wrongly spanned
	// "5-3" because the numeric branch consumed '-'/'+'/'e'/'E'/'.'). See FINDING C.
	//
	// A source-scan is retained ONLY for strings (Literal has reconstructed quotes / is
	// quote-stripped) and comments (Literal differs from the verbatim source span), where
	// len(tok.Literal) is genuinely not the source width.
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

	default:
		// Keyword/identifier/number/operator/punctuation (and any other verbatim token):
		// the source width equals len(tok.Literal) exactly. tokenEnd is the last source byte
		// (inclusive), clamped to the buffer. A zero-length literal collapses to tokenStart.
		width := len(tok.Literal)
		if width <= 0 {
			width = 1
		}
		tokenEnd = tokenStart + width - 1
		if tokenEnd >= len(contentBytes) {
			tokenEnd = len(contentBytes) - 1
		}
		if tokenEnd < tokenStart {
			tokenEnd = tokenStart
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

// buildVarLookup builds a name → *DataDefinition lookup over a definition slice, recursing into
// each definition's Children so grouped and REDEFINE sub-fields (level ≥ 2, which extractDefinitions
// nests under Children) are classified consistently with top-level fields (FINDING B).
//
// data.go stamps SectionKind only on top-level fields, leaving it empty on children; this helper
// propagates the effective SectionKind down so a grouped field in a PARAMETER section is still
// classified `parameter`, not `variable`. Each stored entry is a copy with the effective
// SectionKind stamped (the original NameRange is preserved for declaration-site detection).
//
// On a duplicate name (an unqualified field declared in more than one group) the first occurrence
// in document order wins — a bounded, deterministic choice; qualified resolution is out of scope
// for lexical-stream classification.
func buildVarLookup(defs []model.DataDefinition) map[string]*model.DataDefinition {
	lookup := make(map[string]*model.DataDefinition)
	var walk func(items []model.DataDefinition, inherited string)
	walk = func(items []model.DataDefinition, inherited string) {
		for i := range items {
			effective := items[i].SectionKind
			if effective == "" {
				effective = inherited
			}
			if items[i].Name != "" {
				if _, exists := lookup[items[i].Name]; !exists {
					entry := items[i]
					entry.SectionKind = effective
					lookup[items[i].Name] = &entry
				}
			}
			if len(items[i].Children) > 0 {
				walk(items[i].Children, effective)
			}
		}
	}
	walk(defs, "")
	return lookup
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
	// Names are already upper-cased in definitions. Grouped/REDEFINE sub-fields live in
	// DataDefinition.Children (level ≥ 2) and MUST be included so nested fields are classified at
	// both declaration and use sites (FINDING B) — with the parent's SectionKind propagated down
	// (data.go does not repeat SectionKind on children).
	varLookup := buildVarLookup(definitions)

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

// semanticTokensPhaseBDDMView classifies DDM/view names and fields as `type`/`property`.
// It processes:
// - VIEW definition names: a DataDefinition with ViewOfDDM set → the name is `type`
// - DDM names from VIEW OF clause: extract from the view's ViewOfDDM field → `type`
// - DDM/view operands in data-access statements: DataAccess entries → `type` + `modification` on writes
// - Fields of views: child DataDefinitions of a view → `property`
//
// The precedence rule: a view name that was classified as `variable` by T7 is overridden to `type`.
// Write targets (EdgeWrites) receive the `modification` modifier.
func semanticTokensPhaseBDDMView(path string, content string) []model.SemanticToken {
	contentBytes := []byte(content)

	// Parse to get the AST and extract data.
	lexer := NewLexer(content)
	parser := NewParser(lexer)
	ast, _ := parser.Parse()

	if ast == nil {
		return []model.SemanticToken{}
	}

	definitions := extractDefinitions(ast)
	dataAccess := extractDataAccess(ast)

	var tokens []model.SemanticToken

	// 1. Classify VIEW definition names and their DDM targets.
	// A VIEW is a DataDefinition with ViewOfDDM != "".
	for _, def := range definitions {
		if def.ViewOfDDM == "" {
			continue // not a view
		}

		// Emit the view name as `type`.
		if def.NameRange.Start.Line > 0 {
			tokens = append(tokens, model.SemanticToken{
				Range:     def.NameRange,
				Type:      model.SemanticTokenTypeType,
				Modifiers: 0,
			})
		}

		// Emit the DDM name from "VIEW OF <ddm-name>".
		// The DDM name appears on the same line as the view definition.
		// We need to find the position of the DDM name in the source.
		// The line contains: "1 SOME-VIEW VIEW OF SOME-DDM"
		// We'll locate the occurrence of ViewOfDDM after the view name.
		ddmNameRange := extractViewOfDDMRange(contentBytes, def.NameRange, def.ViewOfDDM)
		if ddmNameRange.Start.Line > 0 {
			tokens = append(tokens, model.SemanticToken{
				Range:     ddmNameRange,
				Type:      model.SemanticTokenTypeType,
				Modifiers: 0,
			})
		}
	}

	// 2. Classify data-access view/DDM operands.
	// Build a map of view/DDM names to their edges so we can check if a write access.
	writeTargets := make(map[string]bool)
	for _, da := range dataAccess {
		if da.Kind == model.EdgeWrites && da.Name != "" {
			writeTargets[da.Name] = true
		}
	}

	// Emit tokens for each data-access entry.
	for _, da := range dataAccess {
		if da.Name == "" {
			continue // skip empty-name record-form writes (no view name token)
		}

		// Determine if this is a write access (to add modification modifier).
		isWrite := da.Kind == model.EdgeWrites
		var modifiers model.SemanticTokenModifier
		if isWrite {
			modifiers = model.SemanticTokenModifierModification
		}

		tokens = append(tokens, model.SemanticToken{
			Range:     da.NameRange,
			Type:      model.SemanticTokenTypeType,
			Modifiers: modifiers,
		})
	}

	// 3. Classify DDM/view field declarations as `property`.
	// Walk all definitions and emit child fields of views as `property`.
	classifyViewFieldsRecursive(definitions, tokens, func(def *model.DataDefinition) {
		tokens = append(tokens, model.SemanticToken{
			Range:     def.NameRange,
			Type:      model.SemanticTokenTypeProperty,
			Modifiers: 0,
		})
	})

	return tokens
}

// classifyViewFieldsRecursive emits `property` tokens for fields of views.
// A view is identified by having ViewOfDDM set.
func classifyViewFieldsRecursive(defs []model.DataDefinition, tokens []model.SemanticToken, emit func(*model.DataDefinition)) {
	for i := range defs {
		def := &defs[i]
		if def.ViewOfDDM != "" && len(def.Children) > 0 {
			// This is a view; emit its children as properties.
			for j := range def.Children {
				child := &def.Children[j]
				if child.NameRange.Start.Line > 0 {
					emit(child)
				}
			}
		}
		// Recurse into children to find nested views.
		classifyViewFieldsRecursive(def.Children, tokens, emit)
	}
}

// extractViewOfDDMRange locates the DDM name token in the "VIEW OF <ddm>" clause.
// viewNameRange is the range of the view name (e.g., "SOME-VIEW" at line 3, col 5-13).
// viewOfDDM is the extracted DDM name (e.g., "SOME-DDM").
// We scan the source line to find where viewOfDDM appears after the view name.
func extractViewOfDDMRange(contentBytes []byte, viewNameRange model.Range, viewOfDDM string) model.Range {
	if viewOfDDM == "" {
		return model.Range{}
	}

	// Find the line in the source using the same line-scanning logic as computeTokenRange.
	line := viewNameRange.Start.Line
	currentLine := 1
	bytePos := 0

	// Scan to the start of the target line.
	for bytePos < len(contentBytes) && currentLine < line {
		if contentBytes[bytePos] == '\r' {
			currentLine++
			bytePos++
			// Skip \n of CRLF.
			if bytePos < len(contentBytes) && contentBytes[bytePos] == '\n' {
				bytePos++
			}
		} else if contentBytes[bytePos] == '\n' {
			currentLine++
			bytePos++
		} else {
			bytePos++
		}
	}

	// Now bytePos is at the start of the target line.
	lineStart := bytePos

	// Find the end of the line.
	lineEnd := lineStart
	for lineEnd < len(contentBytes) {
		if contentBytes[lineEnd] == '\n' || contentBytes[lineEnd] == '\r' {
			break
		}
		lineEnd++
	}

	// Extract the line text.
	lineText := string(contentBytes[lineStart:lineEnd])

	// Find the DDM name in the line. It appears after "VIEW [OF]" and after the view name.
	// We'll search for the DDM name substring after the view name's end column.
	// Columns are 1-based; convert to 0-based index.
	searchFromCol := viewNameRange.End.Column // 1-based column where we should start searching
	searchFrom := searchFromCol - 1           // Convert to 0-based index

	ddmPos := -1

	// Search for the DDM name substring.
	for i := searchFrom; i <= len(lineText)-len(viewOfDDM); i++ {
		if i < 0 {
			continue
		}
		// Check if we have a match at position i.
		if i+len(viewOfDDM) <= len(lineText) {
			candidate := lineText[i : i+len(viewOfDDM)]
			if candidate == viewOfDDM {
				// Verify it's a whole word (not part of a larger identifier).
				// Check boundaries: the char before and after should not be alphanumeric/hyphen.
				okBefore := i == 0 || !isIdentifierChar(rune(lineText[i-1]))
				okAfter := i+len(viewOfDDM) >= len(lineText) || !isIdentifierChar(rune(lineText[i+len(viewOfDDM)]))
				if okBefore && okAfter {
					ddmPos = i
					break
				}
			}
		}
	}

	if ddmPos < 0 {
		// Not found; return empty range.
		return model.Range{}
	}

	// Compute the 1-based column for the start of the DDM name.
	// lineText[i] (0-based) corresponds to column i+1 (1-based).
	startCol := ddmPos + 1
	endCol := ddmPos + len(viewOfDDM)

	return model.Range{
		Start: model.Position{Line: line, Column: startCol},
		End:   model.Position{Line: line, Column: endCol},
	}
}

// isIdentifierChar checks if a rune is a valid identifier character (letter, digit, hyphen, underscore, sigil).
func isIdentifierChar(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' ||
		ch == '#' || ch == '&' || ch == '@' || ch == '+'
}

// semanticTokensPhaseBSystemVarsAndWrites classifies system variables (*DATX, *TIME, etc.)
// and variable write targets with the `modification` modifier.
//
// (a) System variables: A `*` operator token immediately followed by an identifier token
// on the same line (no intervening space) indicates a system variable. The classifier
// emits a SINGLE token spanning the full `*IDENTIFIER` span with Type `variable` and
// Modifiers `readonly | defaultLibrary`. The lexer splits `*DATX` into two tokens
// (`*` operator at col N, `DATX` identifier at col N+1), but the semantic token must
// span both (col N to col N+4 for 5-byte `*DATX`).
//
// Distinction: A `*` at line start is a full-line comment (TokenComment), not a system var.
// The adjacency check (next token is an identifier) naturally filters this out.
//
// (b) Variable write targets: Detect from statement context in the token stream:
// - Assignment `#X := …`: the identifier immediately before `:=` → write target
// - `MOVE … TO #X`: the identifier immediately after `TO` → write target
// - `COMPUTE #X = …`: the identifier after `COMPUTE` and before `=` → write target
// Add the `modification` modifier to variable/parameter tokens that are write targets.
// Read operands (RHS of `:=`, MOVE source, COMPUTE RHS) get NO modification.
//
// OQ-E: DataDefinition has NO const flag, so `readonly` is applied only to system vars.
func semanticTokensPhaseBSystemVarsAndWrites(path string, content string) []model.SemanticToken {
	contentBytes := []byte(content)

	// Parse to get the AST and extract definitions.
	lexer := NewLexer(content)
	parser := NewParser(lexer)
	ast, _ := parser.Parse()

	if ast == nil {
		return []model.SemanticToken{}
	}

	definitions := extractDefinitions(ast)

	// Build a lookup: map variable name → definition info (for write-target detection).
	// Recurse into Children so grouped/REDEFINE sub-fields are recognized as write targets too
	// (FINDING B); SectionKind is propagated from the parent.
	varLookup := buildVarLookup(definitions)

	var tokens []model.SemanticToken

	// Walk the token stream to detect system variables and write targets.
	// First pass: collect all tokens so we can do lookahead/lookbehind.
	lexer = NewLexer(content)
	var allLexerTokens []Token

	for {
		tok := lexer.NextToken()
		if tok.Type == TokenEOF {
			break
		}
		allLexerTokens = append(allLexerTokens, tok)
	}

	// Detect system variables: a TokenOperator "*" immediately followed by a TokenIdentifier
	// on the same line.
	systemVarSpans := make(map[struct{ line, startCol, endCol int }]bool)

	for i := 0; i < len(allLexerTokens)-1; i++ {
		curTok := allLexerTokens[i]
		nextTok := allLexerTokens[i+1]

		// Check if current is `*` operator and next is identifier on the same line, adjacent.
		if curTok.Type == TokenOperator && curTok.Literal == "*" &&
			nextTok.Type == TokenIdentifier &&
			curTok.Line == nextTok.Line &&
			nextTok.Column == curTok.Column+1 {
			// System variable detected: emit a token spanning both tokens.
			// curTok is the `*` at (Line, Column).
			// nextTok is the identifier at (Line, Column+1).
			// The identifier's end is Column + len(Literal) - 1.

			startPos := model.Position{Line: curTok.Line, Column: curTok.Column}
			endPos := model.Position{Line: nextTok.Line, Column: nextTok.Column + len(nextTok.Literal) - 1}

			tokens = append(tokens, model.SemanticToken{
				Range:     model.Range{Start: startPos, End: endPos},
				Type:      model.SemanticTokenTypeVariable,
				Modifiers: model.SemanticTokenModifierReadonly | model.SemanticTokenModifierDefaultLibrary,
			})

			// Mark this span so we can suppress the operator token later.
			systemVarSpans[struct{ line, startCol, endCol int }{curTok.Line, curTok.Column, endPos.Column}] = true
		}
	}

	// Detect variable write targets from statement context.
	// Now scan for write-target patterns.
	for i := 0; i < len(allLexerTokens); i++ {
		tok := allLexerTokens[i]

		// Skip non-identifiers (write targets must be identifiers or data vars).
		if tok.Type != TokenIdentifier {
			continue
		}

		// Skip system variables (already classified above).
		if i > 0 && allLexerTokens[i-1].Type == TokenOperator && allLexerTokens[i-1].Literal == "*" &&
			tok.Line == allLexerTokens[i-1].Line && tok.Column == allLexerTokens[i-1].Column+1 {
			continue // Already handled as system var.
		}

		// Skip if not a declared variable.
		_, exists := varLookup[tok.Literal]
		if !exists {
			continue
		}

		// Pattern 1: Assignment `#X := …`
		// Check if the next non-whitespace token is `:=`.
		if i+1 < len(allLexerTokens) && allLexerTokens[i+1].Type == TokenOperator && allLexerTokens[i+1].Literal == ":=" {
			// This identifier is the LHS of an assignment → write target.
			startRange, endRange := computeTokenRange(contentBytes, tok)
			tokens = append(tokens, model.SemanticToken{
				Range:     model.Range{Start: startRange, End: endRange},
				Type:      model.SemanticTokenTypeVariable,
				Modifiers: model.SemanticTokenModifierModification,
			})
			continue
		}

		// Pattern 2: `MOVE … TO #X`
		// Check if the previous non-whitespace token is `TO` keyword.
		if i > 0 && allLexerTokens[i-1].Type == TokenKeyword && allLexerTokens[i-1].Literal == "TO" {
			// This identifier is after TO → write target.
			startRange, endRange := computeTokenRange(contentBytes, tok)
			tokens = append(tokens, model.SemanticToken{
				Range:     model.Range{Start: startRange, End: endRange},
				Type:      model.SemanticTokenTypeVariable,
				Modifiers: model.SemanticTokenModifierModification,
			})
			continue
		}

		// Pattern 3: `COMPUTE #X = …`
		// Check if the next non-whitespace token is `=` operator (not `:=`).
		if i+1 < len(allLexerTokens) && allLexerTokens[i+1].Type == TokenOperator &&
			allLexerTokens[i+1].Literal == "=" && len(allLexerTokens[i+1].Literal) == 1 {
			// Need to verify we're in a COMPUTE statement.
			// Scan backward to see if there's a COMPUTE keyword on the same line.
			foundCompute := false
			for j := i - 1; j >= 0 && allLexerTokens[j].Line == tok.Line; j-- {
				if allLexerTokens[j].Type == TokenKeyword && allLexerTokens[j].Literal == "COMPUTE" {
					foundCompute = true
					break
				}
			}
			if foundCompute {
				// This identifier is the LHS of a COMPUTE statement → write target.
				startRange, endRange := computeTokenRange(contentBytes, tok)
				tokens = append(tokens, model.SemanticToken{
					Range:     model.Range{Start: startRange, End: endRange},
					Type:      model.SemanticTokenTypeVariable,
					Modifiers: model.SemanticTokenModifierModification,
				})
				continue
			}
		}
	}

	return tokens
}

// semanticTokensPhaseB merges Phase A lexical tokens with Phase B identifier classification.
// It returns a single document-ordered slice with no duplicates.
// Phase B includes:
// - T7: variable/parameter reclassification from identifiers
// - T8: call targets (CALLNAT/FETCH/RUN/PERFORM -> function)
// - T9+: other semantic classifications (DDM, system vars, etc.)
//
// Precedence (when multiple classifications exist for the same span):
// function (T8) > type (T9 DDM/view) > property (T9 field) > parameter/variable (T7) > Phase-A lexical
func semanticTokensPhaseB(path string, content string) []model.SemanticToken {
	phaseATokens := semanticTokensPhaseA(path, content)
	phaseBTokens := semanticTokensPhaseBIdentifiers(path, content)
	phaseBCallTokens := semanticTokensPhaseBCalls(path, content)
	phaseBDDMTokens := semanticTokensPhaseBDDMView(path, content)
	phaseBSysVarTokens := semanticTokensPhaseBSystemVarsAndWrites(path, content)

	_ = phaseBSysVarTokens // Ensure it's used; will be merged below

	// Merge all slices. Precedence: system-var > calls > DDM > identifiers > Phase A.
	allTokens := append(phaseATokens, phaseBTokens...)
	allTokens = append(allTokens, phaseBCallTokens...)
	allTokens = append(allTokens, phaseBDDMTokens...)
	allTokens = append(allTokens, phaseBSysVarTokens...)

	// Sort by start position, then by type/modifiers for stability.
	sort.SliceStable(allTokens, func(i, j int) bool {
		if allTokens[i].Range.Start.Line != allTokens[j].Range.Start.Line {
			return allTokens[i].Range.Start.Line < allTokens[j].Range.Start.Line
		}
		return allTokens[i].Range.Start.Column < allTokens[j].Range.Start.Column
	})

	// Deduplicate: if two tokens have the same start position, apply precedence rules.
	// Precedence: function > type > property > parameter > variable > Phase-A lexical
	// Special case: system-var tokens (variable+readonly+defaultLibrary) must suppress
	// overlapping Phase-A operator tokens (the `*` in `*DATX`).
	precedence := map[model.SemanticTokenType]int{
		model.SemanticTokenTypeFunction:  5,
		model.SemanticTokenTypeType:      4,
		model.SemanticTokenTypeProperty:  3,
		model.SemanticTokenTypeParameter: 2,
		model.SemanticTokenTypeVariable:  2,
		model.SemanticTokenTypeKeyword:   1,
		model.SemanticTokenTypeComment:   1,
		model.SemanticTokenTypeString:    1,
		model.SemanticTokenTypeNumber:    1,
		model.SemanticTokenTypeOperator:  1,
	}

	// Build a map of the highest-precedence token at each position.
	// When multiple tokens share the same start position, keep the highest-precedence one
	// and merge modifiers if they are the same type.
	tokenMap := make(map[struct{ line, col int }]model.SemanticToken)

	for _, tok := range allTokens {
		key := struct{ line, col int }{tok.Range.Start.Line, tok.Range.Start.Column}

		existing, hasExisting := tokenMap[key]
		if !hasExisting {
			tokenMap[key] = tok
		} else {
			// Same start position: choose by precedence or merge modifiers if same type.
			if precedence[tok.Type] > precedence[existing.Type] {
				// tok has higher precedence; replace.
				tokenMap[key] = tok
			} else if precedence[tok.Type] == precedence[existing.Type] {
				// Same precedence (same span). Merge modifiers, keeping the existing (already-
				// chosen) Type. This is what fixes a PARAMETER write target (FINDING A): T7 emits
				// the span as `parameter`+0 while the write-detector emits it as `variable`+
				// modification — same span, same precedence, differing only because the detector
				// defaults to `variable`. The write-detector's role is solely to CONTRIBUTE the
				// `modification` bit, so OR its modifiers onto the existing token regardless of the
				// parameter-vs-variable type mismatch, and never let its default `variable` type
				// override the more-specific `parameter` classification.
				merged := existing
				merged.Modifiers |= tok.Modifiers
				tokenMap[key] = merged
			}
			// Otherwise, keep the existing token (stable).
		}
	}

	// Reconstruct in sorted order from the tokenMap (which has already deduplicated by precedence).
	// We need to iterate in sorted order of (line, col).
	var dedupedTokens []model.SemanticToken
	for _, tok := range allTokens {
		key := struct{ line, col int }{tok.Range.Start.Line, tok.Range.Start.Column}
		selectedTok, exists := tokenMap[key]
		if exists {
			// Check if we've already added this key to the result.
			alreadyAdded := false
			for _, added := range dedupedTokens {
				if added.Range.Start.Line == selectedTok.Range.Start.Line &&
					added.Range.Start.Column == selectedTok.Range.Start.Column {
					alreadyAdded = true
					break
				}
			}
			if alreadyAdded {
				continue // Skip; already added.
			}

			// Filter out operator tokens that are immediately before system-var tokens.
			if selectedTok.Type == model.SemanticTokenTypeOperator {
				isSuppressed := false
				for _, other := range allTokens {
					if other.Range.Start.Line == selectedTok.Range.Start.Line &&
						other.Range.Start.Column == selectedTok.Range.End.Column+1 &&
						other.Type == model.SemanticTokenTypeVariable &&
						(other.Modifiers&model.SemanticTokenModifierReadonly) != 0 {
						isSuppressed = true
						break
					}
				}
				if isSuppressed {
					continue // Skip this operator token.
				}
			}

			dedupedTokens = append(dedupedTokens, selectedTok)
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
