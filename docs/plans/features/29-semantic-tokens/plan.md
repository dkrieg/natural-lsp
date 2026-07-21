# Feature: Semantic Tokens — Server-Driven Syntax & Semantic Highlighting

**Status:** Planned
**PRD requirements:** FR-56 (semantic highlighting — new); NFR-11 (LSP conformance), FR-43 (graceful degradation)
**Priority / phase:** P1 (v1.0 stable; the biggest visible upgrade over the current basic TextMate grammar)
**Depends on:** [00](../00-parser-foundation/plan.md) (lexer token stream + AST), [03](../03-server-lifecycle-and-protocol/plan.md) (capabilities/dispatch), [10](../10-navigation-and-symbol-search/plan.md) (encoding-aware position conversion, `position.go`), [19](../19-protocol-marshaling-unification/plan.md) (json/v2 marshaling). Phase B enriches from [06](../06-call-dependency-extraction/plan.md)/[07](../07-call-dependency-resolution/plan.md) (call edges) and [08](../08-data-access-extraction/plan.md)/[09](../09-program-structure-extraction/plan.md) (definitions/structure), and composes with [27](../27-variable-navigation/plan.md) (variable classification) and [28](../28-symbol-detail-and-view-binding/plan.md) (DDM view fields).

## Summary

Syntax highlighting today is **client-side only** — the VS Code extension ships a **basic TextMate
grammar** (keywords, comments, string/numeric literals) and JetBrains/LSP4IJ has no Natural grammar at
all. The server, which has a real lexer + AST, contributes **nothing** to coloring. LSP's
**`textDocument/semanticTokens`** is the standard way for a language server to drive precise, AST-aware
highlighting — distinguishing a **variable** from a **subroutine call** from a **DDM field** from a
**keyword** — and it's the single most visible "language server" capability we don't yet advertise.

This feature adds a **`semanticTokensProvider`** that classifies each token and emits the LSP semantic-token
stream, delivered in **two tiers** that degrade gracefully:

- **Tier/Phase A — lexical tokens (no AST needed).** The lexer already produces typed tokens with
  positions (`TokenKeyword`, `TokenIdentifier`, `TokenLiteralString`, `TokenLiteralNumeric`,
  `TokenOperator`, `TokenComment`, `TokenSQLOpaque`). Map the unambiguous ones directly —
  keyword/comment/string/number/operator — which works on **any** file, even one that doesn't parse
  (FR-43), and already beats the TextMate grammar for consistency (server and editor agree).
- **Tier/Phase B — semantic classification of identifiers (from the AST + extraction).** Reclassify
  `TokenIdentifier` occurrences using what the analyzer knows: a **data variable** → `variable` (a
  `PARAMETER`-section variable → `parameter`; an AIV/`+` independent → `variable` + a modifier); a
  **CALLNAT/FETCH/RUN target or PERFORM subroutine** → `function`; a **DDM/view name** → `type`; a **DDM
  field / view field** → `property`; a **system variable** (`*DATX`) → `variable` + `defaultLibrary`
  modifier. Modifiers convey more: `declaration`/`definition` on a `DEFINE DATA`/`DEFINE SUBROUTINE` name,
  `readonly` on system vars and constants, and `modification` on a variable write target (assignment /
  `MOVE … TO` / `STORE`) reusing feature 27's read/write knowledge.

The result: real semantic coloring in **any** LSP client (VS Code layers it over the TextMate grammar;
JetBrains/LSP4IJ gets Natural highlighting it otherwise lacks).

**Server-layer, computed on demand, no model/cache change.** Semantic tokens are only ever needed for
**open buffers** (you don't color a closed file), so they are computed from the live document on request —
never persisted — like completion/signatureHelp. Classification that needs the AST is produced **behind the
Analyzer seam** (a new analyzer entry point returning classified spans, so parser internals never leak into
`internal/server`); the server encodes those spans into the LSP wire format. **No `internal/model`
persisted field and no cache-format bump.** This feature **does add a new server capability**
(`semanticTokensProvider` with a legend) — so, unlike most recent features, the locked `TestInitialize`
allow-list **is extended** (an explicit, reviewed addition).

## Research findings (grounded)

- **Lexer already classifies** (`internal/analysis/natural/lexer.go`): `TokenType` ∈
  {Keyword, Identifier, LiteralString, LiteralNumeric, Operator, Punctuation, Comment, SQLOpaque}; every
  `Token` carries `Line`/`Column`. Phase A is a direct map; punctuation is typically left unclassified
  (clients color it structurally).
- **Protocol support is present** (`go.lsp.dev/protocol` v1.0.0): `SemanticTokenTypes` (namespace, type,
  class, enum, interface, struct, typeParameter, parameter, variable, property, enumMember, event,
  function, method, macro, keyword, modifier, comment, string, number, regexp, operator, decorator),
  `SemanticTokenModifiers`, `SemanticTokensLegend`, `SemanticTokensOptions` (with `range`/`full`/`full/delta`
  unions), `SemanticTokensParams`/`SemanticTokens` — all on the json/v2 `MarshalJSONTo` path.
- **The wire encoding** is the LSP relative 5-integer-per-token stream: `[deltaLine, deltaStartChar,
  length, tokenTypeIndex, tokenModifiersBitset]`, indices into the advertised **legend**, positions in the
  **negotiated encoding's code units** (UTF-16 default / UTF-8 — reuse `position.go`, ADR-008). Tokens must
  be emitted in document order; multi-line tokens are split per line (comments/strings).
- **No semantic-token handling exists today** (`grep` in `internal/server` is empty).

## User stories

### Story 1 — Lexical semantic tokens for any file (FR-56, Phase A)
**As a** developer in any LSP editor, **I want** consistent server-driven highlighting of keywords,
comments, strings, and numbers **so that** Natural files are colored even where no TextMate grammar exists
(JetBrains) and coloring matches the server's own lexer.

**Acceptance criteria:**
- [ ] The server advertises `semanticTokensProvider` with a **legend** (the token types + modifiers it
      uses) and handles **`textDocument/semanticTokens/full`**, returning the correctly **relative-encoded**
      token stream for the open document.
- [ ] Keyword/comment/string/number/operator tokens are classified from the lexer; positions are correct
      under both negotiated encodings (a multibyte-prefixed line is byte-exact — reuse `position.go`).
- [ ] A file that **fails to parse** still returns lexical tokens for the parts that lexed (FR-43) — no
      error, no panic. A `FuzzSemanticTokens` (or the encoder entry) never panics on arbitrary input.
- [ ] `TestInitialize`'s capability allow-list is updated to include `semanticTokensProvider` (explicit,
      reviewed); the legend is pinned by a test.

### Story 2 — Semantic identifier classification from the AST (FR-56, Phase B)
**As a** developer, **I want** identifiers colored by what they *are* — variable vs. call target vs. DDM
field — **so that** the code reads structurally, not just lexically.

**Acceptance criteria:**
- [ ] `TokenIdentifier` occurrences are reclassified using the analyzer: data variable → `variable`
      (PARAMETER-section → `parameter`); CALLNAT/FETCH/RUN target and PERFORM subroutine → `function`;
      DDM/view name → `type`; DDM/view field → `property`; system variable (`*`-prefixed) → `variable` with
      the `defaultLibrary` modifier.
- [ ] Modifiers are applied where known: `declaration`/`definition` on `DEFINE DATA`/`DEFINE SUBROUTINE`
      names; `readonly` on system variables; `modification` on a variable **write** target (assignment /
      `MOVE … TO` / `STORE`) — reusing feature 27's read/write distinction where available.
- [ ] Classification is produced **behind the Analyzer seam** (a new analyzer method returns classified
      spans); `internal/server` imports no parser internals. An unclassifiable identifier falls back to its
      Phase-A lexical class (never dropped).
- [ ] Fixtures assert the emitted (type, modifiers) for a representative program (keywords, a declared and
      used variable incl. a write, a CALLNAT target, a PERFORM subroutine, a DDM view + field, a system
      variable).

### Story 3 — Bounded cost & optional range/delta (FR-56, NFR-3)
**As a** developer editing a large file, **I want** semantic tokens to stay responsive.

**Acceptance criteria:**
- [ ] `full` is served store-first from the open buffer; computation is O(tokens) and bounded (no
      workspace sweep). A large-file bench figure is recorded (measure-and-record, off the gate).
- [ ] `textDocument/semanticTokens/range` is supported for the visible range if it is low-cost to add
      (recommended); **`full/delta`** is **deferred** unless measurements show `full` is too heavy
      (documented decision — the legend/options advertise only what's implemented).

## Out of scope / deferred
- **`full/delta`** (incremental token diffs) — advertise and add only if `full` proves too heavy at scale.
- **Multi-token theming beyond the chosen legend** (e.g. distinguishing every statement clause) — start
  with a compact, high-signal legend; extend later.
- **Replacing the TextMate grammar** — the VS Code grammar stays as the pre-semantic fallback (VS Code
  layers semantic tokens over it); this feature does not remove it.
- **Embedded-SQL interior highlighting** inside the `PROCESS SQL` opaque span — the opaque body is a single
  `TokenSQLOpaque`; fine-grained SQL coloring inside it is deferred.

## Open questions (resolve at `/plan-feature`)
- **OQ-1 — legend scope.** Which token types/modifiers to advertise? Recommend a compact set —
  types: `keyword, comment, string, number, operator, variable, parameter, function, type, property`;
  modifiers: `declaration, definition, readonly, modification, defaultLibrary` — and grow from feedback.
- **OQ-2 — analyzer seam entry.** Add `Analyzer.SemanticTokens(path, content) []model.SemanticToken`
  (on-demand, not persisted) vs. attach spans to `FileAnalysis` (persisted → cache bump, and wasteful since
  only open buffers need them). **Recommend the dedicated on-demand method** (no cache change; parser
  internals stay behind the seam). Note this adds a non-persisted `model.SemanticToken{Range, Type,
  Modifiers}` value type (a contract type, not a persisted field — no cache bump).
- **OQ-3 — range support in v1.** Include `semanticTokens/range` now (cheap, helps large files) or defer
  with `full/delta`? Recommend including `range`, deferring `delta`.
- **OQ-4 — write-modifier source.** `modification` on write targets needs the read/write knowledge feature
  27 introduces; if 29 lands first, ship Phase B without `modification` (add it when 27's write sites are
  available) — sequence or degrade gracefully.

## Notes
- **No `internal/model` persisted change and no cache-format bump** — semantic tokens are computed on
  demand from the open buffer; the only new model artifact is a non-persisted `SemanticToken` contract
  type (OQ-2). Contrast features 27/28, which do bump the cache.
- **New server capability** (`semanticTokensProvider` + legend) → the `TestInitialize` allow-list is
  extended (the first capability addition since feature 18). Marshaling on the json/v2 path (feature 19);
  the result is written via the shared `marshalResult`/`MarshalJSONTo` path. Provider is store-first
  (features 10–13) and encoding-aware (`position.go`, ADR-008).
- Phase A is independently shippable and already an improvement; Phase B is the semantic payoff and shares
  the classification with features 27 (variables) and 28 (view/DDM fields) — sequence so the identifier
  classifier is written once and reused.
- Testing: fixtures under `internal/analysis/natural/testdata/` for the classifier and
  `internal/server/testdata/semantictokens/` for wire-encoding tests (assert the relative 5-int stream and
  the legend indices, both encodings). Fuzz the encoder/classifier (never panic — FR-43).
