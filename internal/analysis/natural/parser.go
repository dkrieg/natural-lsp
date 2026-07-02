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
		p.dispatchStatement(ast)
	}

	ast.EndPos = p.prevPos()
	ast.Diagnostics = p.diagnostics
	return ast, nil
}

// dispatchStatement handles statement dispatch for both Parse() top-level and loop bodies.
// It dispatches to the appropriate parser method and appends results to the Program.
func (p *Parser) dispatchStatement(ast *Program) {
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
	case p.matches(TokenKeyword, "COMMIT"):
		p.parseCommitStatement(ast)
	case p.matches(TokenKeyword, "ROLLBACK"):
		p.parseRollbackStatement(ast)
	case p.matches(TokenKeyword, "CALLDBPROC"):
		p.parseCallDBProcStatement(ast)
	case p.matches(TokenKeyword, "SELECT"):
		p.parseSelectStatement(ast)
	case p.matches(TokenKeyword, "INSERT"):
		p.parseInsertStatement(ast)
	case p.matches(TokenKeyword, "UPDATE"):
		p.parseUpdateStatement(ast)
	case p.matches(TokenKeyword, "DELETE"):
		p.parseDeleteStatement(ast)
	case p.matches(TokenKeyword, "MERGE"):
		p.parseMergeStatement(ast)
	case p.matches(TokenKeyword, "PROCESS"):
		p.parseProcessSQLStatement(ast)
	default:
		// Skip unrecognized tokens (partial parsing)
		p.advance()
	}
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
		if p.matches(TokenKeyword, "END", "CALLNAT", "PERFORM", "INCLUDE", "FETCH", "RUN", "DEFINE", "READ", "STORE", "COMMIT", "ROLLBACK", "CALLDBPROC", "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "PROCESS") {
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
// statement or loop terminator, stopping at the first statement keyword, loop terminator, or EOF.
// Loop terminators (END-SELECT, LOOP, END-RESULT) are included in the stop set because they
// must be preserved for the enclosing parseLoopBodyWithDiagnostic (ES-10 fix: prevent recovery
// from consuming terminators that should close the loop). This is safe because loop terminators
// never appear at top level in valid Natural code; in malformed code a stray terminator is
// skipped by dispatchStatement's default branch, which always advances past unrecognized tokens.
// Also stops at END and END-DEFINE (hard program boundaries) to prevent operand over-extension.
//
// This is the single authoritative stop-set for recovery. Every new top-level
// statement keyword must be added in three places to keep them in sync:
//  1. isStatementKeyword (here, via the predicate)
//  2. The Parse() dispatch switch
//  3. The DEFINE DATA break-set (parseDataSection loop guard)
//
// Omitting any of the three causes recovery or data-section termination to drift.
func (p *Parser) skipToNextStatement() {
	for p.current.Type != TokenEOF {
		if p.current.Type == TokenKeyword {
			if isStatementKeyword(p.current.Literal) {
				return
			}
			// Also stop at loop terminators to preserve them for loop body parsing.
			if p.matchesLiteral("END-SELECT", "LOOP", "END-RESULT") {
				return
			}
			// Stop at program terminators (hard boundary).
			if p.isProgramBoundary() {
				return
			}
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
	readToken := p.current // Save READ token for loop body diagnostics

	// Consume READ keyword.
	p.advance()

	// Check for READ RESULT SET form by peeking ahead.
	// If the next token is RESULT followed by SET, parse as READ RESULT SET.
	if p.matchesLiteral("RESULT") {
		// Look ahead to verify SET follows RESULT.
		// Peek by checking current and hypothetically advancing.
		savedCurrent := p.current
		savedPrev := p.prev
		p.advance() // Tentatively consume RESULT

		if p.matchesLiteral("SET") {
			// Confirmed: READ RESULT SET form
			p.advance() // Consume SET

			rrs := &ReadResultSetStatement{
				StartPos: startPos,
			}

			// Parse the result-set operand (identifier after SET).
			if p.matches(TokenIdentifier) {
				rrs.ResultSetOperand = OperandRef{
					Name:  p.current.Literal,
					Range: tokenRange(p.current),
				}
				p.advance()
			}

			// Parse optional INTO clause and other operands until loop body.
			p.skipToNextLoopBody()

			// Parse loop body with terminators END-RESULT or LOOP.
			rrs.Body = p.parseLoopBodyWithDiagnostic([]string{"END-RESULT", "LOOP"}, "READ RESULT SET", readToken)

			rrs.EndPos = p.prevPos()
			ast.ReadResultSets = append(ast.ReadResultSets, rrs)
			return
		}

		// Not READ RESULT SET; restore and fall through to plain READ parsing.
		p.current = savedCurrent
		p.prev = savedPrev
	}

	// Plain READ statement logic.
	read := &ReadStatement{
		StartPos: startPos,
	}

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

// parseCommitStatement parses a COMMIT statement (SQL transaction).
// COMMIT takes no operands.
func (p *Parser) parseCommitStatement(ast *Program) {
	// Capture position of COMMIT keyword before advancing.
	startPos := p.currentPos()
	keyword := p.current

	commit := &CommitStatement{
		StartPos: startPos,
	}

	// Consume COMMIT keyword.
	p.advance()

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	// Set EndPos to cover the keyword text (not prevPos which collapses to column 1).
	commit.EndPos = model.Position{
		Line:   keyword.Line,
		Column: keyword.Column + len(keyword.Literal) - 1,
	}
	ast.Commits = append(ast.Commits, commit)
}

// parseRollbackStatement parses a ROLLBACK statement (SQL transaction).
// ROLLBACK takes no operands.
func (p *Parser) parseRollbackStatement(ast *Program) {
	// Capture position of ROLLBACK keyword before advancing.
	startPos := p.currentPos()
	keyword := p.current

	rollback := &RollbackStatement{
		StartPos: startPos,
	}

	// Consume ROLLBACK keyword.
	p.advance()

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	// Set EndPos to cover the keyword text (not prevPos which collapses to column 1).
	rollback.EndPos = model.Position{
		Line:   keyword.Line,
		Column: keyword.Column + len(keyword.Literal) - 1,
	}
	ast.Rollbacks = append(ast.Rollbacks, rollback)
}

// parseCallDBProcStatement parses a CALLDBPROC statement.
// Consumes proc-name operand and remaining operands via skipToNextStatement.
func (p *Parser) parseCallDBProcStatement(ast *Program) {
	// Capture position of CALLDBPROC keyword before advancing.
	startPos := p.currentPos()

	calldbproc := &CallDBProcStatement{
		StartPos: startPos,
	}

	// Consume CALLDBPROC keyword.
	p.advance()

	// Skip remaining tokens in this statement until the next statement keyword.
	p.skipToNextStatement()

	calldbproc.EndPos = p.prevPos()
	ast.CallDBProcs = append(ast.CallDBProcs, calldbproc)
}

// parseSelectStatement parses a SELECT statement (embedded SQL).
// Grammar: SELECT [SINGLE] ... [INTO ...] [FROM ...] [WHERE ...] [... loop body ...] [END-SELECT|LOOP]
// Two forms:
// 1. SELECT SINGLE ...: singleton (no loop body, no terminator) -> SelectSingleStatement
// 2. SELECT ... [loop body] (END-SELECT|LOOP): cursor loop -> SelectStatement with Body
func (p *Parser) parseSelectStatement(ast *Program) {
	// Capture position of SELECT keyword before advancing.
	startPos := p.currentPos()
	selectToken := p.current // Save SELECT token for loop body diagnostics

	// Consume SELECT keyword.
	p.advance()

	// Check for SINGLE form: SELECT SINGLE ... (no body, no terminator)
	if p.matchesLiteral("SINGLE") {
		p.advance()
		sel := &SelectSingleStatement{
			StartPos: startPos,
		}

		// Parse operands until line end or statement end.
		sel.Columns = p.parseSelectColumns()
		sel.IntoTargets = p.parseSelectInto()
		sel.FromTables = p.parseSelectFrom()
		sel.WhereOperands = p.parseSelectWhere()

		// Skip any remaining tokens on the statement until the next statement keyword.
		p.skipToNextStatement()

		sel.EndPos = p.prevPos()
		ast.SelectSingles = append(ast.SelectSingles, sel)
		return
	}

	// Cursor loop form: SELECT ... [loop body] [END-SELECT|LOOP]
	sel := &SelectStatement{
		StartPos: startPos,
	}

	// Parse operands until loop body or terminator.
	sel.Columns = p.parseSelectColumns()
	sel.IntoTargets = p.parseSelectInto()
	sel.FromTables = p.parseSelectFrom()
	sel.WhereOperands = p.parseSelectWhere()

	// Parse loop body: statements until END-SELECT or LOOP terminator.
	sel.Body = p.parseSelectBodyWithToken(selectToken)

	sel.EndPos = p.prevPos()
	ast.Selects = append(ast.Selects, sel)
}

// parseSelectColumns extracts column operands from SELECT ... until INTO, FROM, or WHERE.
func (p *Parser) parseSelectColumns() []OperandRef {
	var columns []OperandRef

	// Collect column names until we hit INTO, FROM, WHERE, END-SELECT, LOOP, or next statement keyword.
	for p.current.Type != TokenEOF && !p.isSelectStopKeyword() {
		if p.matchesLiteral("INTO", "FROM", "WHERE") {
			break
		}

		// Capture operand (identifier or keyword that acts as a column name).
		if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
			columns = append(columns, OperandRef{
				Name:  p.current.Literal,
				Range: tokenRange(p.current),
			})
		}

		p.advance()
	}

	return columns
}

// parseSelectInto extracts host-variable targets from INTO clause.
func (p *Parser) parseSelectInto() []OperandRef {
	var targets []OperandRef

	if !p.matchesLiteral("INTO") {
		return targets
	}

	p.advance() // Consume INTO keyword.

	// Collect host-vars until FROM, WHERE, END-SELECT, LOOP, or next statement keyword.
	for p.current.Type != TokenEOF && !p.isSelectStopKeyword() {
		if p.matchesLiteral("FROM", "WHERE") {
			break
		}

		// Host-vars can be identifiers starting with # or keywords (in rare cases).
		if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
			targets = append(targets, OperandRef{
				Name:  p.current.Literal,
				Range: tokenRange(p.current),
			})
		}

		p.advance()
	}

	return targets
}

// parseSelectFrom extracts table operands from FROM clause.
func (p *Parser) parseSelectFrom() []OperandRef {
	var tables []OperandRef

	if !p.matchesLiteral("FROM") {
		return tables
	}

	p.advance() // Consume FROM keyword.

	// Collect table names until WHERE, END-SELECT, LOOP, or next statement keyword.
	for p.current.Type != TokenEOF && !p.isSelectStopKeyword() {
		if p.matchesLiteral("WHERE") {
			break
		}

		// Table names are identifiers or keywords.
		if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
			tables = append(tables, OperandRef{
				Name:  p.current.Literal,
				Range: tokenRange(p.current),
			})
		}

		p.advance()
	}

	return tables
}

// parseSelectWhere extracts host-variable operands from WHERE clause.
func (p *Parser) parseSelectWhere() []OperandRef {
	var operands []OperandRef

	if !p.matchesLiteral("WHERE") {
		return operands
	}

	p.advance() // Consume WHERE keyword.

	// Collect operands until END-SELECT, LOOP, or next statement keyword.
	// Skip GROUP BY, HAVING, ORDER BY with their operands.
	for p.current.Type != TokenEOF && !p.isSelectStopKeyword() {
		if p.matchesLiteral("GROUP", "HAVING", "ORDER") {
			// Skip these clauses and their operands until we reach a terminator or new statement.
			p.skipSQLClause()
			continue
		}

		// Consume optional leading colon before host-var operand.
		if p.matches(TokenPunctuation, ":") {
			p.advance()
		}

		// Capture operand (identifier or keyword).
		if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
			operands = append(operands, OperandRef{
				Name:  p.current.Literal,
				Range: tokenRange(p.current),
			})
			p.advance()
		} else {
			p.advance()
		}
	}

	return operands
}

// parseSelectBodyWithToken parses the loop body statements until END-SELECT or LOOP terminator,
// passing the opening SELECT token for diagnostic range reporting.
func (p *Parser) parseSelectBodyWithToken(selectToken Token) []Node {
	return p.parseLoopBodyWithDiagnostic([]string{"END-SELECT", "LOOP"}, "SELECT", selectToken)
}

// parseLoopBodyWithDiagnostic parses statements in a loop body until one of the given terminators
// is reached, emitting a diagnostic if EOF is reached before the terminator.
// openingKeyword is the name of the statement (e.g., "SELECT", "READ RESULT SET") for the diagnostic message.
// openingToken is the opening statement's token for range reporting.
func (p *Parser) parseLoopBodyWithDiagnostic(terminators []string, openingKeyword string, openingToken Token) []Node {
	var body []Node

	for p.current.Type != TokenEOF {
		// Check if we've reached a terminator.
		if p.matchesLiteral(terminators...) {
			break
		}

		// Record position before dispatching so we can detect a stuck state.
		prevLine, prevCol := p.current.Line, p.current.Column

		// Parse a single statement using the full dispatch.
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}

		// Defense-in-depth: if parseStatement returned without advancing (possible only
		// if a new parser path is added that neither consumes a token nor returns early),
		// force-advance one token to prevent spinning.
		if p.current.Type != TokenEOF &&
			p.current.Line == prevLine && p.current.Column == prevCol {
			p.advance()
		}
	}

	// ES-10: emit diagnostic if EOF reached without finding terminator.
	if p.current.Type == TokenEOF && !p.matchesLiteral(terminators...) {
		var termStr string
		if len(terminators) == 2 && terminators[0] == "END-SELECT" {
			termStr = "END-SELECT/LOOP"
		} else if len(terminators) == 2 && terminators[0] == "END-RESULT" {
			termStr = "END-RESULT/LOOP"
		} else {
			termStr = strings.Join(terminators, "/")
		}
		p.addDiagnostic(
			"unterminated "+openingKeyword+" loop: missing "+termStr,
			tokenRange(openingToken).Start,
			p.prevPos(),
			model.DiagnosticError,
		)
	}

	// Consume the terminator.
	if p.matchesLiteral(terminators...) {
		p.advance()
	}

	return body
}

// skipToNextLoopBody skips tokens until a statement keyword that starts a loop body.
// Used between operand parsing (SELECT clauses, READ RESULT SET INTO) and the body itself.
func (p *Parser) skipToNextLoopBody() {
	for p.current.Type != TokenEOF {
		// Stop at statement keywords that can appear in loop bodies.
		if p.current.Type == TokenKeyword && isStatementKeyword(p.current.Literal) {
			return
		}
		// Also stop at loop terminators.
		if p.matchesLiteral("END-SELECT", "LOOP", "END-RESULT") {
			return
		}
		p.advance()
	}
}

// parseStatement parses a single statement inside a loop body (e.g. a SELECT or READ RESULT SET
// loop) and returns the AST node. It uses the full statement dispatch via a throwaway Program,
// so all statement types are supported uniformly.
func (p *Parser) parseStatement() Node {
	// DEFINE blocks are not allowed inside loop bodies. Skip to the next statement keyword
	// using skipToNextLoopBody (not skipToNextStatement) so that loop terminators such as
	// END-RESULT, END-SELECT, and LOOP are never consumed during the skip — otherwise the
	// enclosing parseLoopBodyWithDiagnostic would never see the terminator and run to EOF.
	if p.matches(TokenKeyword, "DEFINE") {
		p.skipToNextLoopBody()
		return nil
	}

	// Use the full dispatch with a throwaway Program to parse the statement.
	tmp := &Program{}
	p.dispatchStatement(tmp)

	// Extract the single statement node that was appended to tmp.
	return firstStatementNode(tmp)
}

// firstStatementNode extracts the single statement node from a throwaway Program
// populated by dispatchStatement. Checks all statement slices and returns the one
// non-empty slice's first element (or nil if no statements were appended).
//
// MAINTAINER NOTE: this function MUST be kept in sync with dispatchStatement.
// Whenever a new statement kind is added to dispatchStatement (e.g., ES-8 INSERT/
// UPDATE/DELETE/MERGE, ES-8 PROCESS SQL), add a corresponding check here or that
// statement type will silently disappear from loop bodies (SELECT body, READ RESULT
// SET body) with no compile-time error. The current set covers every case that
// dispatchStatement can populate: see the checks below in dispatch order.
func firstStatementNode(tmp *Program) Node {
	// Check each statement type in the order they appear in dispatchStatement.
	if len(tmp.Calls) > 0 {
		return tmp.Calls[0]
	}
	if len(tmp.Performs) > 0 {
		return tmp.Performs[0]
	}
	if len(tmp.Includes) > 0 {
		return tmp.Includes[0]
	}
	if len(tmp.Fetches) > 0 {
		return tmp.Fetches[0]
	}
	if len(tmp.Runs) > 0 {
		return tmp.Runs[0]
	}
	if len(tmp.Reads) > 0 {
		return tmp.Reads[0]
	}
	if len(tmp.Stores) > 0 {
		return tmp.Stores[0]
	}
	if len(tmp.Commits) > 0 {
		return tmp.Commits[0]
	}
	if len(tmp.Rollbacks) > 0 {
		return tmp.Rollbacks[0]
	}
	if len(tmp.CallDBProcs) > 0 {
		return tmp.CallDBProcs[0]
	}
	if len(tmp.Selects) > 0 {
		return tmp.Selects[0]
	}
	if len(tmp.SelectSingles) > 0 {
		return tmp.SelectSingles[0]
	}
	if len(tmp.ReadResultSets) > 0 {
		return tmp.ReadResultSets[0]
	}
	if len(tmp.ProcessSQLs) > 0 {
		return tmp.ProcessSQLs[0]
	}
	if len(tmp.Inserts) > 0 {
		return tmp.Inserts[0]
	}
	if len(tmp.SQLUpdates) > 0 {
		return tmp.SQLUpdates[0]
	}
	if len(tmp.SQLDeletes) > 0 {
		return tmp.SQLDeletes[0]
	}
	if len(tmp.Merges) > 0 {
		return tmp.Merges[0]
	}
	// Statements not reachable from loop bodies: DataSections, Subroutines, Maps
	// are omitted because DEFINE is filtered out in parseStatement before dispatch.
	return nil
}

// skipSQLClause skips tokens that are part of a SQL clause (GROUP BY, HAVING, ORDER BY)
// until a SELECT terminator or statement keyword.
// FIRST consumes the clause keyword (GROUP [BY], ORDER [BY], HAVING), then skips
// operands until the next clause keyword, SELECT terminator, or statement keyword.
// Guarantees forward progress: always advances at least one token.
func (p *Parser) skipSQLClause() {
	// Consume the clause keyword itself (GROUP, ORDER, or HAVING).
	// This must happen first to guarantee we advance past the keyword that triggered this call.
	if p.matchesLiteral("GROUP", "HAVING", "ORDER") {
		p.advance()
		// Optional: skip "BY" keyword after GROUP or ORDER.
		if p.matchesLiteral("BY") {
			p.advance()
		}
	}

	// Now skip operand tokens until we hit a stop condition.
	for p.current.Type != TokenEOF {
		// Stop at SELECT terminators.
		if p.matchesLiteral("END-SELECT", "LOOP") {
			return
		}
		// Stop at program terminator (hard boundary).
		if p.isProgramBoundary() {
			return
		}
		// Stop at statement keywords.
		if p.current.Type == TokenKeyword && isStatementKeyword(p.current.Literal) {
			return
		}
		// Stop at next SQL clause keyword (would start a new clause).
		if p.matchesLiteral("GROUP", "HAVING", "ORDER", "WHERE", "FROM", "INTO") {
			return
		}
		p.advance()
	}
}

// isProgramBoundary returns true if the current token is a hard program
// boundary keyword (END or END-DEFINE). These are stop sentinels shared by all
// operand-scanning loops and the clause-skip helper — defined once here so the
// boundary set cannot drift across the eight call sites.
func (p *Parser) isProgramBoundary() bool {
	return p.matchesLiteral("END", "END-DEFINE")
}

// isSelectStopKeyword returns true if the current token is a terminator or stop keyword for SELECT operand parsing.
func (p *Parser) isSelectStopKeyword() bool {
	return p.matchesLiteral("END-SELECT", "LOOP") || p.isProgramBoundary() || (p.current.Type == TokenKeyword && isStatementKeyword(p.current.Literal))
}

// isStatementKeyword checks if a literal is a top-level statement keyword.
func isStatementKeyword(literal string) bool {
	return literal == "DEFINE" || literal == "CALLNAT" || literal == "PERFORM" ||
		literal == "INCLUDE" || literal == "FETCH" || literal == "RUN" ||
		literal == "READ" || literal == "STORE" || literal == "COMMIT" ||
		literal == "ROLLBACK" || literal == "CALLDBPROC" || literal == "SELECT" ||
		literal == "INSERT" || literal == "UPDATE" || literal == "DELETE" ||
		literal == "MERGE" || literal == "PROCESS"
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

// parseInsertStatement parses an INSERT statement (SQL DML, ES-9).
// Syntax: INSERT INTO <table> [<columns>] VALUES (:<host-vars>)
// Captures table operand and all VALUES clause operands (stored without leading colon).
//
// Disambiguation: INSERT is always SQL DML in Natural/SQL; there is no Adabas
// INSERT statement. If INTO is absent the statement is malformed and skipped
// (no node produced) — residual ambiguous shapes are never silently mis-produced.
// Infinite-loop safety: p.advance() is called unconditionally before any early
// return, so dispatchStatement always makes forward progress on this keyword.
func (p *Parser) parseInsertStatement(ast *Program) {
	startPos := p.currentPos()
	ins := &InsertStatement{
		StartPos: startPos,
	}

	// Consume INSERT keyword
	p.advance()

	if !p.matchesLiteral("INTO") {
		p.skipToNextStatement()
		ins.EndPos = p.prevPos()
		return
	}

	// Consume INTO
	p.advance()

	// Parse table operand (identifier)
	if p.matches(TokenIdentifier) {
		ins.IntoTable = append(ins.IntoTable, OperandRef{
			Name:  p.current.Literal,
			Range: tokenRange(p.current),
		})
		p.advance()
	}

	// Parse column list (optional, skip for now)
	// Skip to VALUES keyword, stopping at program terminators (hard boundary)
	for p.current.Type != TokenEOF && !p.matchesLiteral("VALUES") {
		// Stop at program terminators.
		if p.isProgramBoundary() {
			break
		}
		// Stop at next statement keyword
		if p.current.Type == TokenKeyword && isStatementKeyword(p.current.Literal) {
			break
		}
		p.advance()
	}

	// Parse VALUES clause
	if p.matchesLiteral("VALUES") {
		p.advance()

		// Skip to opening paren
		if p.matches(TokenPunctuation, "(") {
			p.advance()

			// Collect operands until closing paren
			for p.current.Type != TokenEOF && !p.matches(TokenPunctuation, ")") {
				// Consume optional leading colon
				if p.matches(TokenPunctuation, ":") {
					p.advance()
				}

				// Capture operand (identifier, keyword, or literal)
				if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword || p.current.Type == TokenLiteralNumeric {
					ins.Values = append(ins.Values, OperandRef{
						Name:  p.current.Literal,
						Range: tokenRange(p.current),
					})
					p.advance()
				} else {
					p.advance()
				}
			}

			// Consume closing paren
			if p.matches(TokenPunctuation, ")") {
				p.advance()
			}
		}
	}

	// Skip to next statement
	p.skipToNextStatement()

	ins.EndPos = p.prevPos()
	ast.Inserts = append(ast.Inserts, ins)
}

// parseUpdateStatement parses an UPDATE statement (SQL DML, ES-9).
// Disambiguates SQL form (UPDATE <table> SET ... [WHERE ...]) from Adabas UPDATE.
//
// Disambiguation heuristic: identifier (table name) immediately after UPDATE AND
// followed by SET or WHERE ⇒ SQL form. Any other shape (keyword, variable, missing
// SET/WHERE) ⇒ Adabas / genuinely-ambiguous — skipped, no node produced. Residual
// ambiguous shapes are never silently mis-produced as SQL nodes.
// Infinite-loop safety: p.advance() is called unconditionally on entry, so
// dispatchStatement always makes forward progress on the UPDATE keyword regardless
// of which branch is taken.
func (p *Parser) parseUpdateStatement(ast *Program) {
	startPos := p.currentPos()

	// Consume UPDATE keyword
	p.advance()

	// Check if this looks like SQL UPDATE: identifier followed by SET/WHERE
	if p.current.Type != TokenIdentifier {
		// Not SQL form
		p.skipToNextStatement()
		return
	}

	// Save table identifier for potential consumption
	tableName := p.current.Literal
	tableRange := tokenRange(p.current)
	p.advance()

	// Now check if we see SET or WHERE
	isSQL := p.matchesLiteral("SET") || p.matchesLiteral("WHERE")

	// If it doesn't look like SQL, just skip and don't produce a node
	if !isSQL {
		p.skipToNextStatement()
		return
	}

	// Parse as SQL UPDATE
	// Table name was already consumed above during disambiguation check
	upd := &SQLUpdateStatement{
		StartPos: startPos,
	}

	// Add the already-consumed table name to Table list
	upd.Table = append(upd.Table, OperandRef{
		Name:  tableName,
		Range: tableRange,
	})

	// Parse SET clause
	if p.matchesLiteral("SET") {
		p.advance() // Now at COL

		// Collect operands in SET until WHERE or next statement
		for p.current.Type != TokenEOF && !p.matchesLiteral("WHERE") && !isStatementKeyword(p.current.Literal) {
			if p.current.Type == TokenEOF || p.matchesLiteral("WHERE") {
				break
			}
			// Stop at program terminators (hard boundary).
			if p.isProgramBoundary() {
				break
			}

			// Consume optional leading colon
			if p.matches(TokenPunctuation, ":") {
				p.advance()
				continue
			}

			// Skip operators (=, <, >, etc.)
			if p.matches(TokenOperator) {
				p.advance()
				continue
			}

			// Capture operand (identifier or keyword)
			if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
				upd.SetTargets = append(upd.SetTargets, OperandRef{
					Name:  p.current.Literal,
					Range: tokenRange(p.current),
				})
				p.advance()
			} else {
				// Skip any other token type (punctuation, literals, etc.)
				p.advance()
			}
		}
	}

	// Parse WHERE clause
	if p.matchesLiteral("WHERE") {
		p.advance()

		// Collect operands until next statement keyword or program terminator
		for p.current.Type != TokenEOF && !isStatementKeyword(p.current.Literal) {
			// Check for end of WHERE clause
			if p.current.Type == TokenEOF || isStatementKeyword(p.current.Literal) {
				break
			}
			// Stop at program terminators (hard boundary).
			if p.isProgramBoundary() {
				break
			}

			// Consume optional leading colon
			if p.matches(TokenPunctuation, ":") {
				p.advance()
				continue
			}

			// Skip operators (=, <, >, etc.)
			if p.matches(TokenOperator) {
				p.advance()
				continue
			}

			// Capture operand (identifier or keyword)
			if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
				upd.WhereOperands = append(upd.WhereOperands, OperandRef{
					Name:  p.current.Literal,
					Range: tokenRange(p.current),
				})
				p.advance()
			} else {
				// Skip any other token type
				p.advance()
			}
		}
	}

	// Skip to next statement
	p.skipToNextStatement()

	upd.EndPos = p.prevPos()
	ast.SQLUpdates = append(ast.SQLUpdates, upd)
}

// parseDeleteStatement parses a DELETE statement (SQL DML, ES-9).
// Disambiguates SQL form (DELETE FROM <table> [WHERE ...]) from Adabas DELETE.
//
// Disambiguation heuristic: FROM immediately after DELETE ⇒ SQL form (Adabas
// DELETE never uses FROM). Any other shape ⇒ Adabas / genuinely-ambiguous —
// skipped, no node produced. Residual ambiguous shapes are never silently
// mis-produced as SQL nodes.
// Infinite-loop safety: p.advance() is called unconditionally on entry, so
// dispatchStatement always makes forward progress on the DELETE keyword regardless
// of which branch is taken.
func (p *Parser) parseDeleteStatement(ast *Program) {
	startPos := p.currentPos()

	// Consume DELETE keyword
	p.advance()

	// Check for FROM keyword (SQL indicator)
	if !p.matchesLiteral("FROM") {
		// Not SQL DELETE form; skip and don't produce a node
		p.skipToNextStatement()
		return
	}

	p.advance() // Consume FROM

	del := &SQLDeleteStatement{
		StartPos: startPos,
	}

	// Parse table operand
	if p.matches(TokenIdentifier) {
		del.FromTable = append(del.FromTable, OperandRef{
			Name:  p.current.Literal,
			Range: tokenRange(p.current),
		})
		p.advance()
	}

	// Parse WHERE clause
	if p.matchesLiteral("WHERE") {
		p.advance()

		// Collect operands until next statement or program terminator
		for p.current.Type != TokenEOF && !isStatementKeyword(p.current.Literal) {
			// Stop at program terminators (hard boundary).
			if p.isProgramBoundary() {
				break
			}

			// Consume optional leading colon
			if p.matches(TokenPunctuation, ":") {
				p.advance()
			}

			// Capture operand
			if p.current.Type == TokenIdentifier || p.current.Type == TokenKeyword {
				del.WhereOperands = append(del.WhereOperands, OperandRef{
					Name:  p.current.Literal,
					Range: tokenRange(p.current),
				})
				p.advance()
			} else {
				p.advance()
			}
		}
	}

	// Skip to next statement
	p.skipToNextStatement()

	del.EndPos = p.prevPos()
	ast.SQLDeletes = append(ast.SQLDeletes, del)
}

// parseMergeStatement parses a MERGE statement (SQL DML, ES-9).
// Syntax: MERGE INTO <table> [... internals not modeled ...]
// Captures table operand only; MERGE internals are not modeled.
//
// Disambiguation: MERGE is always SQL DML in Natural/SQL; there is no Adabas
// MERGE statement. The table operand is captured; all MERGE WHEN/THEN internals
// are skipped (not modeled). If INTO is absent the statement is malformed and
// skipped — residual ambiguous shapes are never silently mis-produced.
// Infinite-loop safety: p.advance() is called unconditionally on entry, so
// dispatchStatement always makes forward progress on the MERGE keyword.
func (p *Parser) parseMergeStatement(ast *Program) {
	startPos := p.currentPos()

	merge := &MergeStatement{
		StartPos: startPos,
	}

	// Consume MERGE keyword
	p.advance()

	// Expect INTO keyword
	if !p.matchesLiteral("INTO") {
		p.skipToNextStatement()
		merge.EndPos = p.prevPos()
		return
	}

	// Consume INTO
	p.advance()

	// Skip table operand (captured but not stored in model)
	if p.matches(TokenIdentifier) {
		p.advance()
	}

	// Skip to next statement (merge internals not modeled)
	p.skipToNextStatement()

	merge.EndPos = p.prevPos()
	ast.Merges = append(ast.Merges, merge)
}

// parseProcessSQLStatement parses a PROCESS SQL statement (ES-8).
// Syntax: PROCESS SQL <ddm-name> << <opaque-body> >>
// The opaque body is captured as raw text (no tokenization of interior).
func (p *Parser) parseProcessSQLStatement(ast *Program) {
	// Capture position of PROCESS keyword before advancing.
	startPos := p.currentPos()
	startLine := p.current.Line

	ps := &ProcessSQLStatement{
		StartPos: startPos,
	}

	// Consume PROCESS keyword.
	p.advance()

	// Expect SQL literal on the same line.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		// PROCESS at EOF or without SQL on the same line — malformed (FR-30/M-6).
		p.addDiagnostic(
			"malformed PROCESS SQL statement: expected SQL keyword",
			startPos,
			p.prevPos(),
			model.DiagnosticError,
		)
		ps.EndPos = p.prevPos()
		return
	}

	if !p.matchesLiteral("SQL") {
		// Not a PROCESS SQL statement; skip and bail.
		p.skipToNextStatement()
		ps.EndPos = p.prevPos()
		return
	}

	// Consume SQL literal.
	p.advance()

	// Expect DDM name (identifier) on the same line.
	if p.current.Type == TokenEOF || p.current.Line != startLine {
		// PROCESS SQL with DDM name missing — malformed (FR-30/M-6).
		p.addDiagnostic(
			"malformed PROCESS SQL statement: expected DDM name",
			startPos,
			p.prevPos(),
			model.DiagnosticError,
		)
		ps.EndPos = p.prevPos()
		return
	}

	if !p.matches(TokenIdentifier) {
		// PROCESS SQL followed by a non-identifier (e.g. a lone '<') — malformed (FR-30/M-6).
		p.addDiagnostic(
			"malformed PROCESS SQL statement: expected DDM name identifier",
			startPos,
			p.prevPos(),
			model.DiagnosticError,
		)
		ps.EndPos = p.prevPos()
		return
	}

	// Capture DDM name and its range.
	ps.DDMName = p.current.Literal
	ps.DDMNameRange = tokenRange(p.current)

	// Consume the DDM name token. The parser keeps a one-token lookahead, so after
	// this advance p.current holds the token that begins the `<<` opener — which the
	// lexer has already lexed as a lone `<` operator (the lexer does not treat `<<`
	// as a delimiter outside this SQL context). p.lexer.ScanOpaqueSpanFrom rewinds
	// past that `<` and captures the raw span; the interior is never tokenized
	// (Option B). If there is no `<<` opener, this is a malformed PROCESS SQL with no
	// body — leave Body empty and let ES-10 own the diagnostic; never panic.
	p.advance()

	if p.current.Type == TokenOperator && p.current.Literal == "<" {
		opaqueToken, endLine, endCol, unterminated := p.lexer.ScanOpaqueSpanFrom(p.current)

		ps.Body = opaqueToken.Literal
		// ScanOpaqueSpanFrom returns the position just after the closing `>>`
		// (or EOF for an unterminated span), so `>>` ends at endCol-1.
		ps.BodyRange = model.Range{
			Start: model.Position{Line: opaqueToken.Line, Column: opaqueToken.Column},
			End:   model.Position{Line: endLine, Column: endCol - 1},
		}
		ps.EndPos = ps.BodyRange.End

		// ES-10: emit diagnostic for unterminated << ... >> span.
		if unterminated {
			p.addDiagnostic(
				"unterminated PROCESS SQL << ... >> block: missing >>",
				ps.BodyRange.Start,
				ps.BodyRange.End,
				model.DiagnosticError,
			)
		}

		// Re-prime the lookahead so parsing resumes after the closing `>>`.
		p.current = p.lexer.NextToken()
	} else {
		// ES-10: PROCESS SQL missing its << ... >> body.
		p.addDiagnostic(
			"PROCESS SQL requires a << ... >> body",
			startPos,
			ps.DDMNameRange.End,
			model.DiagnosticError,
		)
		ps.EndPos = p.prevPos()
	}

	// Append the ProcessSQLStatement to the AST.
	ast.ProcessSQLs = append(ast.ProcessSQLs, ps)
}
