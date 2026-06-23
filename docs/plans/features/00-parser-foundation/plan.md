# Feature: Parser Foundation

**Status:** Planned  
**PRD requirements:** NFR-15 (replaceable backend), FR-30 (syntax diagnostics), M-6 (no silent gaps)  
**Priority / phase:** P0 (required for all subsequent features)  
**Depends on:** None  

## Summary

This feature implements the hand-written lexer and recursive-descent parser for Natural that the PRD and README describe but do not yet exist. The parser produces a proper AST that enables completion, signature help, call hierarchy, real syntax diagnostics, and accurate symbol tables — features that regex extraction cannot deliver reliably.

Two kinds of analysis gap are handled separately, and neither is dropped silently:

- **Unresolvable references** — e.g. `CALLNAT #VARIABLE`, whose target cannot be determined statically — are noted as unresolvable with the call site preserved, so they appear in find-references and outline rather than disappearing.
- **Parse errors** — source the parser cannot interpret — are surfaced as LSP diagnostics so they are visible in the editor, not silently discarded.

The parser sits behind the `Analyzer` interface so the backend can evolve (e.g. to a tree-sitter grammar) without touching the LSP layer.

## User Stories

### Story 1 — Lexer (FR-30, NFR-15)

**As a** parser implementer, **I want** a lexer that tokenizes Natural source **so that** the parser can consume tokens.

**Acceptance criteria:**

- [ ] The lexer handles Natural's case-insensitivity by normalizing keywords and identifiers to upper-case.
- [ ] The lexer handles multi-line statements (Natural statements can span lines).
- [ ] The lexer produces tokens for: keywords, identifiers, literals (string, numeric), operators, punctuation, comments.
- [ ] A fixture suite covers each token type with expected token values.

### Story 2 — AST Nodes (NFR-15)

**As a** parser implementer, **I want** a set of AST node types representing Natural constructs **so that** the tree can be traversed for extraction.

**Acceptance criteria:**

- [ ] AST nodes include: Program, Subroutine, DataSection, DataField, Map, IncludeStatement, CallStatement, FetchStatement, RunStatement, PerformStatement.
- [ ] Each node carries source position information (start/end line/column).
- [ ] Nodes form a tree structure (parent/child relationships).
- [ ] A fixture per node kind demonstrates correct structure.

### Story 3 — Recursive-Descent Parser (FR-30, M-6)

**As a** user, **I want** the parser to correctly parse Natural source into an AST **so that** extraction and resolution can operate on the tree.

**Acceptance criteria:**

- [ ] The parser handles all common Natural statements (CALLNAT, PERFORM, INCLUDE, FETCH, RUN, READ, STORE, DEFINE DATA, DEFINE SUBROUTINE, DEFINE MAP).
- [ ] The parser produces syntax diagnostics for malformed statements rather than crashing or dropping them.
- [ ] The parser handles partial parsing: unrecognized lines produce diagnostics but do not prevent parsing of recognized lines.
- [ ] A fixture per statement type demonstrates correct AST production.
- [ ] A fixture per parse error demonstrates diagnostic emission.

### Story 4 — Analyzer Integration (NFR-15)

**As a** developer, **I want** the parser integrated into the `Analyzer` interface **so that** the backend can be swapped without changing LSP-facing code.

**Acceptance criteria:**

- [ ] `Analyzer.Analyze(path, content)` returns `FileAnalysis` with an `AST` field containing the parsed tree.
- [ ] `FileAnalysis.Diagnostics` contains syntax errors from the parser.
- [ ] The `Analyzer` interface remains unchanged (only implementation changes).
- [ ] Tests verify that the parser-backed `Analyzer` satisfies `analysis.Analyzer`.

### Story 5 — Parser Tests (M-5, M-6)

**As a** maintainer, **I want** a comprehensive test suite for the parser **so that** regressions are caught early.

**Acceptance criteria:**

- [ ] Every Natural statement type has at least one test fixture.
- [ ] Every parse error case has at least one test fixture.
- [ ] Tests verify AST structure (node types, positions, relationships).
- [ ] Tests verify diagnostics for malformed input.
- [ ] Fixtures are stored in `testdata/parser/` as permanent regression tests.

## Out of scope

- Semantic validation (e.g. whether a called module actually exists) — that's resolution, not parsing.
- Support for obscure legacy constructs not found in common production code.
- Column/fixed-format syntax (handled for common patterns; unusual legacy formatting may yield incomplete extraction).

## Open questions

- What is the exact grammar for Natural statements (reference: natls, Software AG documentation)?
- How should the parser handle inline comments vs. line comments?
- What is the required level of detail for data-definition parsing (arrays, redefinitions)?
- Should the parser be error-recovering (continue after error) or fail-fast?
