// Package natural provides Natural language analysis via a hand-written lexer
// and recursive-descent parser. This file contains the parser implementation.
package natural

import (
	"strconv"
	"strings"

	"natural-lsp/internal/model"
)

// Parser implements a recursive-descent parser for Natural source code.
type Parser struct {
	lexer       *Lexer
	current     Token // next token to consume
	prev        Token // most recently consumed token
	diagnostics []model.Diagnostic
}

// NewParser creates a new parser for the given lexer.
func NewParser(lexer *Lexer) *Parser {
	p := &Parser{lexer: lexer}
	p.current = p.lexer.NextToken()
	// Seed prev with the first token so prevPos() is never below line 1 on
	// empty input (where no advance() call has yet occurred).
	p.prev = p.current
	return p
}

// Parse parses the input and returns the AST.
func (p *Parser) Parse() (*Program, error) {
	ast := &Program{
		StartPos: p.currentPos(),
	}

	for p.current.Type != TokenEOF {
		switch {
		case p.matches(TokenKeyword, "DEFINE"):
			p.parseDefine(ast)
		case p.matches(TokenKeyword, "CALLNAT"):
			p.parseCallStatement(ast)
		case p.matches(TokenKeyword, "PERFORM"):
			p.parsePerformStatement(ast)
		case p.matches(TokenKeyword, "INCLUDE"):
			p.parseIncludeStatement(ast)
		case p.matches(TokenKeyword, "FETCH"):
			p.parseFetchStatement(ast)
		case p.matches(TokenKeyword, "RUN"):
			p.parseRunStatement(ast)
		case p.matches(TokenKeyword, "READ"):
			p.parseReadStatement(ast)
		case p.matches(TokenKeyword, "STORE"):
			p.parseStoreStatement(ast)
		default:
			// Skip unrecognized tokens (partial parsing)
			p.advance()
		}
	}

	ast.EndPos = p.prevPos()
	ast.Diagnostics = p.diagnostics
	return ast, nil
}

// parseDefine handles DEFINE DATA, DEFINE SUBROUTINE, and DEFINE MAP.
func (p *Parser) parseDefine(ast *Program) {
	if !p.matches(TokenKeyword, "DEFINE") {
		return
	}

	// Capture position of DEFINE keyword before advancing
	defStartPos := p.currentPos()

	// Consume DEFINE keyword
	p.advance()

	if p.matches(TokenKeyword, "DATA") {
		p.parseDataSection(ast, defStartPos)
	} else if p.matchesLiteral("SUBROUTINE") {
		p.parseSubroutine(ast, defStartPos)
	} else if p.matchesLiteral("MAP") {
		p.parseMap(ast, defStartPos)
	} else {
		// Unknown DEFINE, skip to next statement
		p.skipToNextStatement()
	}
}

// parseDataSection parses a DEFINE DATA block.
func (p *Parser) parseDataSection(ast *Program, startPos model.Position) {
	section := &DataSection{
		StartPos: startPos,
	}

	// Consume DATA keyword
	p.advance()

	// Skip section keywords (LOCAL, PARAMETER, GLOBAL, etc.)
	for p.matchesLiteral("LOCAL", "PARAMETER", "GLOBAL", "LINKAGE") {
		p.advance()
	}

	// Parse fields until END keyword
	// Track parent stack for nesting by level
	parentStack := make([]*DataField, 0)

	for p.current.Type != TokenEOF {
		// Stop at END keyword or other statement keywords (if DEFINE DATA block wasn't properly closed)
		if p.matches(TokenKeyword, "END", "CALLNAT", "PERFORM", "INCLUDE", "FETCH", "RUN", "DEFINE", "READ", "STORE") {
			break
		}

		// Expect a numeric level number
		if !p.matches(TokenLiteralNumeric) {
			p.advance()
			continue
		}

		// Parse level
		level, _ := strconv.Atoi(p.current.Literal)
		fieldStartPos := p.currentPos()
		p.advance()

		// Check for an optional REDEFINE clause immediately after the level number.
		// Syntax: <level> REDEFINE <target-field>
		// The redefine node itself carries no name; its Children hold the subfields.
		// If the target identifier is absent (malformed input), redefineTarget stays "".
		// A diagnostic for that case is deferred to Task 7; we never panic here.
		var isRedefine bool
		var redefineTarget string
		if p.matches(TokenKeyword, "REDEFINE") {
			isRedefine = true
			p.advance()
			// The token after REDEFINE is the field being redefined.
			if p.matches(TokenIdentifier) || p.matches(TokenKeyword) {
				redefineTarget = p.current.Literal
				p.advance()
			}
		}

		// Parse the field name for normal (non-redefine) fields.
		// The lexer yields the full hyphenated name including any # prefix as a
		// single token (e.g. "#EMPLOYEE-ID"). Keywords like ID are also accepted
		// here because they can legally appear inside Natural variable names.
		// For REDEFINE nodes this branch is skipped; name stays "".
		var name string
		if !isRedefine {
			if p.matches(TokenIdentifier) || p.matches(TokenKeyword) {
				name = p.current.Literal
				p.advance()
			}
		}

		// Skip fields that have neither a name nor a redefine target (e.g. a bare
		// level number with no following tokens).
		if !isRedefine && name == "" {
			continue
		}

		// Parse the optional type/format specification: "(TYPE-CODE)" or
		// "(TYPE-CODE/DIM1,DIM2)".  REDEFINE nodes carry no type — their
		// subfields (Children) carry individual types instead.
		fieldType := ""
		dimensions := []ArrayBound{}
		if !isRedefine && p.matches(TokenPunctuation, "(") {
			p.advance()
			spec := p.parseTypeSpec()
			fieldType, dimensions = p.parseTypeAndDimensions(spec)
			if p.matches(TokenPunctuation, ")") {
				p.advance()
			}
		}

		// Create field
		field := &DataField{
			Level:      level,
			Name:       name,
			Type:       fieldType,
			Dimensions: dimensions,
			Redefines:  redefineTarget,
			StartPos:   fieldStartPos,
			EndPos:     p.prevPos(),
			Children:   make([]*DataField, 0),
		}

		// Handle nesting: trim parentStack to have only parents with level < current level
		for len(parentStack) > 0 && parentStack[len(parentStack)-1].Level >= level {
			parentStack = parentStack[:len(parentStack)-1]
		}

		// Add to parent or top-level
		if len(parentStack) == 0 {
			section.Fields = append(section.Fields, field)
		} else {
			parentStack[len(parentStack)-1].Children = append(parentStack[len(parentStack)-1].Children, field)
		}

		// Add to stack as potential parent for next fields
		parentStack = append(parentStack, field)
	}

	section.EndPos = p.prevPos()
	ast.DataSections = append(ast.DataSections, section)
}

// parseTypeSpec reads tokens until closing paren and concatenates them without spaces
func (p *Parser) parseTypeSpec() string {
	var spec string
	for p.current.Type != TokenEOF && !p.matches(TokenPunctuation, ")") {
		spec += p.current.Literal
		p.advance()
	}
	return spec
}

// parseTypeAndDimensions splits a spec like "N7", "P9.2", "A3/1:12", "N3/1:5,1:3"
// into type (before /) and dimensions (after /)
func (p *Parser) parseTypeAndDimensions(spec string) (string, []ArrayBound) {
	if spec == "" {
		return "", nil
	}

	// Find / separator
	slashIdx := strings.Index(spec, "/")
	var typeStr, dimStr string
	if slashIdx >= 0 {
		typeStr = strings.TrimSpace(spec[:slashIdx])
		dimStr = strings.TrimSpace(spec[slashIdx+1:])
	} else {
		typeStr = strings.TrimSpace(spec)
		dimStr = ""
	}

	// Parse dimensions if present
	var dimensions []ArrayBound
	if dimStr != "" {
		dimensions = p.parseDimensions(dimStr)
	}

	return typeStr, dimensions
}

// parseDimensions parses comma-separated dimension specs like "1:12" or "1:5,1:3"
func (p *Parser) parseDimensions(spec string) []ArrayBound {
	var bounds []ArrayBound
	for _, dimStr := range strings.Split(spec, ",") {
		dimStr = strings.TrimSpace(dimStr)
		if dimStr == "" {
			continue
		}

		colonIdx := strings.Index(dimStr, ":")
		var lower, upper int
		var unbounded bool

		if colonIdx >= 0 {
			// Format: lower:upper
			lowerStr := strings.TrimSpace(dimStr[:colonIdx])
			upperStr := strings.TrimSpace(dimStr[colonIdx+1:])
			lower, _ = strconv.Atoi(lowerStr)
			if upperStr == "*" {
				unbounded = true
			} else {
				upper, _ = strconv.Atoi(upperStr)
			}
		} else {
			// Format: just number → 1:N
			lower = 1
			if dimStr == "*" {
				unbounded = true
			} else {
				upper, _ = strconv.Atoi(dimStr)
			}
		}

		bounds = append(bounds, ArrayBound{
			Lower:          lower,
			Upper:          upper,
			UpperUnbounded: unbounded,
		})
	}
	return bounds
}

// parseSubroutine parses a DEFINE SUBROUTINE block.
func (p *Parser) parseSubroutine(ast *Program, startPos model.Position) {
	sub := &Subroutine{
		StartPos: startPos,
	}

	// Consume SUBROUTINE keyword
	p.advance()

	if p.matches(TokenIdentifier) {
		sub.Name = p.current.Literal
		p.advance()
	}

	for p.current.Type != TokenEOF {
		if p.matches(TokenKeyword, "END") {
			p.advance()
			if p.matches(TokenIdentifier, "SUBROUTINE") {
				p.advance()
			}
			break
		}
		p.advance()
	}

	sub.EndPos = p.prevPos()
	ast.Subroutines = append(ast.Subroutines, sub)
}

// parseMap parses a DEFINE MAP block.
func (p *Parser) parseMap(ast *Program, startPos model.Position) {
	m := &Map{
		StartPos: startPos,
	}

	// Consume MAP keyword
	p.advance()

	if p.matches(TokenIdentifier) {
		m.Name = p.current.Literal
		p.advance()
	}

	for p.current.Type != TokenEOF {
		if p.matches(TokenKeyword, "END") {
			p.advance()
			if p.matches(TokenKeyword, "MAP") {
				p.advance()
			}
			break
		}
		p.advance()
	}

	m.EndPos = p.prevPos()
	ast.Maps = append(ast.Maps, m)
}

// parseCallStatement parses a CALLNAT statement.
func (p *Parser) parseCallStatement(ast *Program) {
	// Capture position of CALLNAT keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	// keywordEndCol is the column of the last character of "CALLNAT" (7 chars).
	keywordEndCol := startPos.Column + len("CALLNAT") - 1

	call := &CallStatement{
		StartPos: startPos,
	}

	// Consume CALLNAT keyword.
	p.advance()

	// The target operand must appear on the same line as the CALLNAT keyword.
	// If the next token is EOF or on a different line, the operand is missing.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"CALLNAT requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		call.EndPos = p.prevPos()
		// Do not append: a CALLNAT with no target is not a useful AST node.
		return
	}

	if p.matches(TokenLiteralString) {
		tok := p.current
		call.TargetIsLiteral = true
		call.TargetRange = tokenRange(tok)
		call.Target = p.consumeStringTarget()
	} else if p.matches(TokenIdentifier) {
		tok := p.current
		call.TargetIsLiteral = false
		call.TargetRange = tokenRange(tok)
		call.Target = tok.Literal
		p.advance()
	} else {
		// A token is present on the same line but is not a valid operand.
		p.addDiagnostic(
			"CALLNAT requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		call.EndPos = p.prevPos()
		return
	}

	// Skip tokens that belong to this statement (parameters, modifiers, etc.)
	// until the next top-level statement keyword or EOF.
	p.skipToNextStatement()

	call.EndPos = p.prevPos()
	// Only append calls that carry a resolved target name.
	if call.Target != "" {
		ast.Calls = append(ast.Calls, call)
	}
}

// parsePerformStatement parses a PERFORM statement.
func (p *Parser) parsePerformStatement(ast *Program) {
	// Capture position of PERFORM keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	keywordEndCol := startPos.Column + len("PERFORM") - 1

	perform := &PerformStatement{
		StartPos: startPos,
	}

	// Consume PERFORM keyword.
	p.advance()

	// The subroutine name must appear on the same line as PERFORM.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"PERFORM requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		perform.EndPos = p.prevPos()
		return
	}

	if p.matches(TokenIdentifier) {
		tok := p.current
		perform.Target = tok.Literal
		perform.TargetRange = tokenRange(tok)
		p.advance()
	}

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	perform.EndPos = p.prevPos()
	ast.Performs = append(ast.Performs, perform)
}

// parseIncludeStatement parses an INCLUDE statement.
func (p *Parser) parseIncludeStatement(ast *Program) {
	// Capture position of INCLUDE keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	keywordEndCol := startPos.Column + len("INCLUDE") - 1

	inc := &IncludeStatement{
		StartPos: startPos,
	}

	// Consume INCLUDE keyword.
	p.advance()

	// The copycode name must appear on the same line as INCLUDE.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"INCLUDE requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		inc.EndPos = p.prevPos()
		return
	}

	if p.matches(TokenLiteralString) {
		tok := p.current
		inc.TargetIsLiteral = true
		inc.TargetRange = tokenRange(tok)
		inc.Target = p.consumeStringTarget()
	} else if p.matches(TokenIdentifier) {
		tok := p.current
		inc.TargetIsLiteral = false
		inc.TargetRange = tokenRange(tok)
		inc.Target = tok.Literal
		p.advance()
	}

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	inc.EndPos = p.prevPos()
	ast.Includes = append(ast.Includes, inc)
}

// parseFetchStatement parses a FETCH statement.
func (p *Parser) parseFetchStatement(ast *Program) {
	// Capture position of FETCH keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	keywordEndCol := startPos.Column + len("FETCH") - 1

	fetch := &FetchStatement{
		StartPos: startPos,
	}

	// Consume FETCH keyword.
	p.advance()

	// Optional REPEAT or RETURN modifier: skip if present.
	if p.current.Line == startLine && p.matchesLiteral("REPEAT", "RETURN") {
		p.advance()
	}

	// The target operand must be on the same line.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"FETCH requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		fetch.EndPos = p.prevPos()
		ast.Fetches = append(ast.Fetches, fetch)
		return
	}

	// Consume the target (either a string literal or identifier).
	if p.matches(TokenLiteralString) {
		tok := p.current
		fetch.TargetIsLiteral = true
		fetch.TargetRange = tokenRange(tok)
		fetch.Target = p.consumeStringTarget()
	} else if p.matches(TokenIdentifier) {
		tok := p.current
		fetch.TargetIsLiteral = false
		fetch.TargetRange = tokenRange(tok)
		fetch.Target = tok.Literal
		p.advance()
	}

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	fetch.EndPos = p.prevPos()
	ast.Fetches = append(ast.Fetches, fetch)
}

// parseRunStatement parses a RUN statement.
//
// Grammar: RUN [REPEAT] [program-name [library-id]]
//
// Both program-name and library-id must appear on the same source line as the
// RUN keyword; a token on the next line belongs to a following statement and is
// never consumed here. library-id is the second positional operand and may be
// a quoted literal or an identifier; it is placed in RunStatement.Library.
func (p *Parser) parseRunStatement(ast *Program) {
	// Capture position of RUN keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	keywordEndCol := startPos.Column + len("RUN") - 1

	run := &RunStatement{
		StartPos: startPos,
	}

	// Consume RUN keyword.
	p.advance()

	// The target program name must appear on the same line as RUN.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"RUN requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		run.EndPos = p.prevPos()
		return
	}

	if p.matches(TokenLiteralString) {
		tok := p.current
		run.TargetIsLiteral = true
		run.TargetRange = tokenRange(tok)
		run.Target = p.consumeStringTarget()
	} else if p.matches(TokenIdentifier) {
		tok := p.current
		run.TargetIsLiteral = false
		run.TargetRange = tokenRange(tok)
		run.Target = tok.Literal
		p.advance()
	}

	// Capture the optional library-id: second positional operand, same-line only.
	if p.current.Type != TokenEOF && p.current.Line == startLine {
		if p.matches(TokenLiteralString) {
			run.Library = p.consumeStringTarget()
		} else if p.matches(TokenIdentifier) {
			run.Library = p.current.Literal
			p.advance()
		}
	}

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	run.EndPos = p.prevPos()
	ast.Runs = append(ast.Runs, run)
}

// advance moves to the next token, saving the consumed token in p.prev.
func (p *Parser) advance() {
	p.prev = p.current
	p.current = p.lexer.NextToken()
}

// prevPos returns the position of the most recently consumed token.
// Use this for EndPos so it reflects the last real token of a statement,
// not the first token of whatever follows.
func (p *Parser) prevPos() model.Position {
	return model.Position{Line: p.prev.Line, Column: p.prev.Column}
}

// addDiagnostic records a syntax diagnostic at the given position range.
func (p *Parser) addDiagnostic(message string, start, end model.Position, severity model.DiagnosticSeverity) {
	p.diagnostics = append(p.diagnostics, model.Diagnostic{
		Message:  message,
		Severity: severity,
		Range: model.Range{
			Start: start,
			End:   end,
		},
	})
}

// matches checks if the current token matches the expected token type and literal.
// If only a TokenType is provided, it returns true if the token type matches.
// If only a string is provided, it returns true if the token literal matches.
// If both TokenType and string are provided, both must match.
func (p *Parser) matches(expected ...interface{}) bool {
	if len(expected) == 0 {
		return false
	}

	// If both TokenType and string are provided, both must match
	if len(expected) == 2 {
		if tok, ok := expected[0].(TokenType); ok {
			if lit, ok := expected[1].(string); ok {
				return p.current.Type == tok && p.current.Literal == lit
			}
		}
	}

	// Otherwise, check if any expected value matches
	for _, e := range expected {
		switch exp := e.(type) {
		case TokenType:
			if p.current.Type == exp {
				return true
			}
		case string:
			if p.current.Literal == exp {
				return true
			}
		}
	}
	return false
}

// matchesLiteral checks if the current token's literal matches any of the provided strings,
// regardless of token type.
func (p *Parser) matchesLiteral(literals ...string) bool {
	for _, lit := range literals {
		if p.current.Literal == lit {
			return true
		}
	}
	return false
}

// skipToNextStatement advances past tokens that do not start a new top-level
// statement, stopping at the first statement keyword or EOF.
// This is the single authoritative stop-set for recovery; add new top-level
// keywords here (and in the Parse dispatch switch) to keep them in sync.
func (p *Parser) skipToNextStatement() {
	for p.current.Type != TokenEOF {
		if p.current.Type == TokenKeyword && isStatementKeyword(p.current.Literal) {
			return
		}
		p.advance()
	}
}

// currentPos returns the current position from the current token.
func (p *Parser) currentPos() model.Position {
	return model.Position{Line: p.current.Line, Column: p.current.Column}
}

// tokenRange returns the inclusive source span of a single token.
// The convention used throughout the AST for TargetRange:
//   - Start is the first character of the token (tok.Column).
//   - End is the last character, computed as tok.Column + len(tok.Literal) - 1.
//
// For TokenLiteralString the lexer stores the surrounding quotes in tok.Literal
// (e.g. "'MYPROG'" for the source text 'MYPROG'), so the span includes both
// quotes, matching the visible source range.
// For TokenIdentifier tok.Literal is the bare identifier text (e.g. "#PROGNAME"),
// so End points to the last identifier character.
func tokenRange(tok Token) model.Range {
	return model.Range{
		Start: model.Position{Line: tok.Line, Column: tok.Column},
		End:   model.Position{Line: tok.Line, Column: tok.Column + len(tok.Literal) - 1},
	}
}

// parseReadStatement parses a READ statement.
func (p *Parser) parseReadStatement(ast *Program) {
	// Capture position of READ keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	keywordEndCol := startPos.Column + len("READ") - 1

	read := &ReadStatement{
		StartPos: startPos,
	}

	// Consume READ keyword.
	p.advance()

	// Skip optional same-line parenthesized row-limit: READ (10) <view>.
	if p.current.Line == startLine && p.matches(TokenPunctuation, "(") {
		p.advance()
		for p.current.Type != TokenEOF && !p.matches(TokenPunctuation, ")") {
			p.advance()
		}
		if p.matches(TokenPunctuation, ")") {
			p.advance()
		}
	}

	// The view/DDM name must be on the same line as READ (possibly after a row-limit paren).
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"READ requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		read.EndPos = p.prevPos()
		ast.Reads = append(ast.Reads, read)
		return
	}

	if p.matches(TokenIdentifier) {
		read.Target = p.current.Literal
		p.advance()
	}

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	read.EndPos = p.prevPos()
	ast.Reads = append(ast.Reads, read)
}

// parseStoreStatement parses a STORE statement.
func (p *Parser) parseStoreStatement(ast *Program) {
	// Capture position of STORE keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line
	keywordEndCol := startPos.Column + len("STORE") - 1

	store := &StoreStatement{
		StartPos: startPos,
	}

	// Consume STORE keyword.
	p.advance()

	// Skip optional same-line clause keywords (RECORD, IN, FILE) that precede the target.
	for p.current.Line == startLine && p.matchesLiteral("RECORD", "IN", "FILE") {
		p.advance()
	}

	// The view/file name must be on the same line as STORE (possibly after clause keywords).
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		p.addDiagnostic(
			"STORE requires a target operand",
			startPos,
			model.Position{Line: startPos.Line, Column: keywordEndCol},
			model.DiagnosticError,
		)
		store.EndPos = p.prevPos()
		ast.Stores = append(ast.Stores, store)
		return
	}

	if p.matches(TokenIdentifier) {
		store.Target = p.current.Literal
		p.advance()
	}

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	store.EndPos = p.prevPos()
	ast.Stores = append(ast.Stores, store)
}

// isStatementKeyword checks if a literal is a top-level statement keyword.
func isStatementKeyword(literal string) bool {
	return literal == "DEFINE" || literal == "CALLNAT" || literal == "PERFORM" ||
		literal == "INCLUDE" || literal == "FETCH" || literal == "RUN" ||
		literal == "READ" || literal == "STORE"
}

// consumeStringTarget extracts the target name from the current TokenLiteralString
// token, emits a diagnostic if the string is unterminated, advances past the token,
// and returns the unquoted name.
//
// The lexer contract for string literals:
//   - Terminated:   literal == "'content'" (surrounded by single quotes)
//   - Unterminated: literal == "content"   (no surrounding quotes; closing quote was absent)
//
// isTerminatedString detects which case applies; unquoteString strips the quotes
// from terminated literals.
func (p *Parser) consumeStringTarget() string {
	tok := p.current
	if !isTerminatedString(tok.Literal) {
		tokPos := model.Position{Line: tok.Line, Column: tok.Column}
		p.addDiagnostic(
			"Unterminated string literal",
			tokPos,
			tokPos,
			model.DiagnosticError,
		)
	}
	target := unquoteString(tok.Literal)
	p.advance()
	return target
}

// unquoteString removes a matched pair of surrounding single quotes from a
// string literal token value. Specifically:
//   - "'content'" → "content"
//   - "" (empty) → "" (returned as-is; len < 2)
//   - "MYPROG" (no quotes) → "MYPROG" (returned as-is; first char is not a quote)
//   - "'" (single quote char) → "'" (returned as-is; len < 2)
//
// The lexer always wraps TokenLiteralString values in single quotes, so the
// common case is the first one; the remaining cases are safety guards.
func unquoteString(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// isTerminatedString checks if a TokenLiteralString token is properly terminated.
// The lexer wraps terminated string literals in single quotes; unterminated ones
// carry only the raw scanned content without surrounding quotes.
func isTerminatedString(s string) bool {
	return len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\''
}
