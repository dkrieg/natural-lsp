# Tasks: Semantic Tokens — Server-Driven Syntax & Semantic Highlighting

**Feature:** 29 — semantic tokens
**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-56 (semantic highlighting — new); NFR-11 (LSP conformance), FR-43 (graceful degradation), NFR-3 (interactive latency, measure-and-record)

Converts `plan.md` into an ordered, TDD-structured task list. Phase A (lexical) is independently
shippable; Phase B (semantic identifier classification) is the payoff. Each task is one
red → green → refactor slice.

---

## Current-state findings & impact

Surveyed `internal/analysis/{analyzer.go,natural/*}`, `internal/server/{server.go,position.go,completion.go}`,
`internal/model/model.go`, and the `go.lsp.dev/protocol` v1.0.0 semantic-token types. Plan against the
code as it *is*:

### The Analyzer seam (shared-contract change)
- `analysis.Analyzer` (`internal/analysis/analyzer.go`) currently declares exactly two methods:
  `Analyze(path, content) (model.FileAnalysis, error)` and `ExtractVariableRefs(content string) []model.VariableRef`.
  **This feature adds a third method** (OQ-2). Recommended signature:
  `SemanticTokens(path string, content []byte) []model.SemanticToken` — on-demand, non-persisted,
  path taken for object-type/extension awareness, mirroring `Analyze`.
- **A method added to `analysis.Analyzer` is a shared-contract change.** Every implementation and every
  test double of the interface must gain the method or the build breaks. Consumers to migrate:
  - `internal/analysis/natural.Analyzer` — the real backend (implements the method).
  - Any `analysis.Analyzer` **test double / spy** in `internal/server` (the tests that inject a fake
    analyzer, e.g. the `Analyze`-recording spy referenced around `server_test.go`). Each needs a
    `SemanticTokens` stub. **T2 owns this migration.**
  - The external `lsp-graph` builder consumes `internal/model` and may embed/implement the seam — flag
    for `review-seam`; if it only calls `Analyze` it is source-compatible (interface grows, callers of
    the old methods unaffected), but any *implementer* of the interface must add the method.

### The lexer (Phase A source — reuse, don't rebuild)
- `internal/analysis/natural/lexer.go`: `NewLexer(input string) *Lexer` + `(*Lexer).NextToken() Token`.
  `TokenType` ∈ {`TokenKeyword`, `TokenIdentifier`, `TokenLiteralString`, `TokenLiteralNumeric`,
  `TokenOperator`, `TokenPunctuation`, `TokenComment`, `TokenSQLOpaque`, `TokenEOF`, `TokenError`}. Each
  `Token` carries **1-based `Line` and 1-based byte `Column`** (matches `model.Position` convention) plus
  `Literal`.
- **Load-bearing correctness caveat:** `Token.Literal` is **not** the verbatim source slice — the lexer
  *normalizes case* (identifiers/keywords upper-cased via `uppercase`) and *strips the surrounding quotes*
  from a string literal (`TokenLiteralString.Literal` = `'content'` reconstructed, and `/* rest-of-line`
  vs `*`/`**` full-line comments differ). Therefore the emitted **token `length`** (in code units) must be
  derived from the **source span**, not `len(Token.Literal)`. The classifier must record a real
  `model.Range` (start = `Line`/`Column`, end = last byte of the actual source token) so `position.go`
  can length-encode it correctly. **T1's contract type carries a `Range`, not a length**, precisely so the
  server owns encoding.
- `ExtractVariableRefs` (`variablerefs.go`) already demonstrates the exact token-walk-with-`DEFINE DATA`-
  tracking pattern the Phase-B classifier reuses (identifier stream, `DEFINE DATA` / `END-DEFINE`
  boundary tracking, `*`-system-var exclusion, sigil handling). **Reuse this pattern; do not duplicate.**

### Position/encoding (reuse `position.go`, ADR-008)
- `internal/server/position.go` gives `byteOffsetToCharacter(lineText, byteOffset, enc)` and
  `toProtocolRange(...)` — encoding-aware, both UTF-8 and UTF-16. The semantic-token wire format needs a
  per-line **code-unit `startChar` and `length`**; both derive from `byteOffsetToCharacter` on the token's
  line text. The server holds the buffer content already (store-first), so no line-width table is needed.
- **Multi-line tokens** (a `/*`…EOL comment is single-line, but a `TokenSQLOpaque` `<<…>>` span or a
  continued string can span lines) must be **split per line** in the wire encoding (LSP requires each token
  entirely on one line). The classifier returns whole-token `Range`s; the **encoder** (server, T5) splits
  any multi-line range into per-line tokens. Natural comments/strings are effectively single-line in
  practice, but the encoder must not assume it (FR-43).

### The capability (locked allow-list is extended — explicit, reviewed)
- `internal/server/server.go` builds `protocol.ServerCapabilities` in the `initialize` handler; the field
  is `SemanticTokensProvider SemanticTokensProvider` (a union; concrete type `*protocol.SemanticTokensOptions`
  with `Legend`, `Range ClientSemanticTokensRequestOptionsRange`, `Full SemanticTokensOptionsFull`).
- `TestInitialize` (`server_test.go` ~L602) pins a **locked `requiredProviders` allow-list**. This feature
  **extends it** to include `semanticTokensProvider` — the first capability addition since feature 18 — and
  **pins the legend** (token-type list + modifier list, in order) by a dedicated test. This is the
  explicit, reviewed allow-list change the plan calls out.

### Marshaling & dispatch (reuse feature 19 path)
- Provider results marshal through `marshalResult` (`server.go` L136, json/v2 `gojson.Marshal`).
  `protocol.SemanticTokens{Data []uint32}` marshals cleanly on this path. `marshal_guard_test.go` forbids
  stdlib `json.Marshal` for results — the new handlers must use `marshalResult`.
- Method dispatch is the big `switch` in `server.go` (`case "textDocument/…"`), gated on `stateInitialized`,
  params decoded via `UnmarshalJSONFrom`. Two new cases: `textDocument/semanticTokens/full` and
  `textDocument/semanticTokens/range`.

### OQ-4 nuance — `modification` is NOT free from feature 27
- Feature 27's shipped write knowledge is **DDM/data-access `EdgeWrites`** (`data.go`) and the untyped
  `model.VariableRef` (which carries **no read/write direction** — verified: no `IsWrite`/`Direction`
  field). So "reuse feature 27's write knowledge" applies cleanly to **DDM/view field writes** (STORE /
  record-form UPDATE/DELETE targets → `property` + `modification`), but a **variable write target**
  (`MOVE … TO #X`, `#X := …`, `COMPUTE #X = …`) needs the classifier to detect the write **from statement
  context in the token stream** — it is not readable off `VariableRef`. **Resolution of OQ-4:** implement
  `modification` where the analyzer can detect it — (a) DDM/data-access write edges for `property`, and
  (b) a bounded statement-context write detector for variables (assignment `:=`, `MOVE … TO`, `COMPUTE
  … =`, and STORE/UPDATE-into host vars) in the classifier. This is scoped to **T9** so Phase B ships
  without it if the detector proves fiddly (graceful degrade: no `modification`, never wrong).

### Criteria already satisfied / not applicable
- None already satisfied — `grep semanticToken internal/server` is empty; this is net-new.
- `full/delta` is **deferred** (OQ-3) — not advertised, not implemented.

### Legend (OQ-1, resolved)
- **Token types** (order fixed — indices are the wire contract): `keyword`, `comment`, `string`,
  `number`, `operator`, `variable`, `parameter`, `function`, `type`, `property`.
- **Token modifiers** (order fixed — bitset positions): `declaration`, `definition`, `readonly`,
  `modification`, `defaultLibrary`.
- These map 1:1 onto `protocol.SemanticTokenTypes*` / `protocol.SemanticTokenModifiers*` string values.

---

## Ordered task list

Dependency order: model contract → seam + real backend (Phase A classify) → server encoder + capability +
`full` handler (Phase A end-to-end) → Phase B identifier reclassification (variables → parameters → calls
→ DDM/view → system vars → modifiers) → `range` handler → fuzz + bench. Phase A (T1–T6) is shippable on
its own; Phase B is T7–T12.

---

### T1 — `model.SemanticToken` contract type (no cache bump)
**Behavior:** Add a non-persisted value type to `internal/model` describing a classified token span.
**Fixtures:** none (pure type).
**Shape (recommended):**
```go
// SemanticToken is an on-demand, NON-persisted classification of one source token
// (feature 29). It is NOT a FileAnalysis field and never enters the cache.
type SemanticToken struct {
    Range     Range               // source span (1-based, inclusive-end, byte columns) — server encodes to wire units
    Type      SemanticTokenType   // stable classification (keyword, variable, function, …)
    Modifiers SemanticTokenModifier // bitset of declaration/definition/readonly/modification/defaultLibrary
}
type SemanticTokenType string        // stable string values matching the legend
type SemanticTokenModifier uint32    // bit flags; positions match the legend modifier order
```
Define the type/modifier constants (values equal to the legend strings / bit positions from OQ-1).
`Modifiers` as a bitset (uint32 flags) because a token can carry several (`declaration`+`readonly`).
**Expected result:** compiles; constants present; **no `FileAnalysis` field added, no `cacheFormatVersion`
bump** (assert the version constant is unchanged — still `0.9.0` — in the DoD).
**Reuses/migrates:** additive to `internal/model`; nothing to migrate (new type).
**Modeled gaps:** n/a.
**DoD:** type + constants defined; a table test pins each constant's string value / bit position;
`grep cacheFormatVersion` unchanged; `go vet`/`gofmt` clean; `internal/model` stays backend-free.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** —

---

### T2 — Add `SemanticTokens` to the `Analyzer` seam + migrate every implementer (Phase A skeleton)
**Behavior:** Add `SemanticTokens(path string, content []byte) []model.SemanticToken` to the
`analysis.Analyzer` interface and implement a **skeleton** in `internal/analysis/natural.Analyzer` that
returns an empty (non-nil) slice — plus stub the method on **every test double** of the interface so the
build stays green. (Real Phase-A classification is T3; splitting keeps the seam/migration change isolated
and reviewable.)
**Fixtures:** none.
**Expected result:** interface has three methods; `natural.Analyzer.SemanticTokens` returns
`[]model.SemanticToken{}`; every `analysis.Analyzer` test double compiles with a `SemanticTokens` stub;
`just build` + existing test suites green.
**Reuses/migrates:** **migration task** — grep for every type that satisfies `analysis.Analyzer`
(the real backend + all server-test spies/fakes) and add the method. Mirror the doc-comment style of
`ExtractVariableRefs`.
**Modeled gaps:** empty return is a legal "nothing classified" result (never nil, never error).
**DoD:** interface + real skeleton + all doubles migrated; the seam guard (`seam_test.go`) still passes
(no parser internals leak); existing tests green; `go vet`/`gofmt` clean.
**Agents:** tdd-red (interface-shape/compile test) → tdd-green → tdd-refactor.
**Depends on:** T1

---

### T3 — Phase A lexical classification (keyword/comment/string/number/operator)
**Behavior:** Implement the real Phase-A body of `natural.SemanticTokens`: run `NewLexer` over `content`,
walk `NextToken()`, and emit one `model.SemanticToken` per lexed token for the unambiguous lexical classes
— `TokenKeyword`→`keyword`, `TokenComment`→`comment`, `TokenLiteralString`→`string`,
`TokenLiteralNumeric`→`number`, `TokenOperator`→`operator`. **`TokenIdentifier`, `TokenPunctuation`,
`TokenSQLOpaque`, `TokenError`, `TokenEOF` are NOT emitted in Phase A** (identifiers become Phase B;
punctuation/SQL-opaque are left to client structural coloring; the opaque interior is out of scope per the
plan). Each emitted token's `Range` spans the **real source bytes** (start = token `Line`/`Column`; end =
last source byte of the token — computed from source, NOT `len(Literal)`, since Literal is
normalized/quote-stripped). Emit in document order.
**Fixtures:** `internal/analysis/natural/testdata/semantictokens/lexical.NSP` — a small program mixing a
`*` full-line comment, a `/*` rest-of-line comment, a keyword (`CALLNAT`/`MOVE`), a quoted string
(`'HELLO'`), a numeric literal (`42`), and an operator (`:=` or `=`).
**Expected result:** the returned `[]SemanticToken` lists exactly the keyword/comment/string/number/
operator tokens with correct 1-based Line/Column ranges and correct source lengths (e.g. the `'HELLO'`
token's Range covers all 7 source bytes including quotes; the `*` comment covers the whole line).
**Reuses/migrates:** reuses the `NewLexer`/`NextToken` walk pattern from `ExtractVariableRefs`.
**Modeled gaps:** a file that only partially lexes still yields tokens for the parts that lexed (FR-43); an
unrecognized/error token is skipped, not fabricated.
**DoD:** fixture-backed table test asserts each token's (Type, Range); document order; identifiers absent
(deferred to Phase B); never panics on the fixture; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T2

---

### T4 — Server capability advertisement + legend (extend the locked allow-list)
**Behavior:** In `initialize` (`server.go`), advertise
`SemanticTokensProvider: &protocol.SemanticTokensOptions{ Legend: <OQ-1 legend>, Full: protocol.Boolean(true), Range: protocol.Boolean(true) }`.
The `Legend.TokenTypes`/`TokenModifiers` are the fixed-order OQ-1 lists. `Full` = true; `Range` = true
(T11 implements the handler — advertise together so the option shape is pinned once). **`full/delta` is
NOT advertised** (deferred). Define the legend as a single shared package-level value (so the encoder in T5
and the test in this task index into the *same* ordering — the wire contract).
**Fixtures:** none (uses the `TestInitialize` request path).
**Expected result:** `initialize` response `capabilities.semanticTokensProvider` is an object with the exact
legend arrays (both in the fixed order), `full: true`, `range: true`, no `delta`.
**Reuses/migrates:** **migration** — extend `TestInitialize`'s `requiredProviders` allow-list to include
`semanticTokensProvider`, and add a dedicated `TestInitialize_SemanticTokensLegend` pinning the legend
arrays and ordering. Marshaling via the existing json/v2 capability path (no change).
**Modeled gaps:** n/a.
**DoD:** capability advertised; allow-list extended; legend pinned by test (both arrays, order); the shared
legend value is the single source of truth reused by the encoder; `go vet`/`gofmt` clean.
**Agents:** tdd-red (legend + allow-list test) → tdd-green → tdd-refactor.
**Depends on:** T1 (constants), T3 (so an end-to-end `full` follows immediately in T5/T6)

---

### T5 — Wire encoder: `[]model.SemanticToken` → LSP relative 5-int stream (encoding-aware, both encodings)
**Behavior:** Add a pure encoder in `internal/server` (e.g. `semantic_tokens.go`) that converts a document-
ordered `[]model.SemanticToken` + the buffer `content` + negotiated encoding into
`protocol.SemanticTokens{Data: []uint32}`. For each token: compute `deltaLine` and `deltaStartChar`
relative to the previous token (per LSP: `deltaStartChar` resets to absolute char when `deltaLine>0`),
`length` = code units of the token's source span **on its line** (via `byteOffsetToCharacter` on the line
text), `tokenType` = the legend index of `Type`, `tokenModifiers` = the modifier bitset (legend bit
positions). **Split any multi-line token into per-line tokens** before delta-encoding. Sort defensively by
(line, startChar) if input order is not guaranteed.
**Fixtures:** unit-level (Go-constructed `[]model.SemanticToken` + content string) — no `.NSx` needed for
the pure encoder.
**Expected result:** exact `Data []uint32`. Include a case with a **multibyte-prefixed line** (e.g. an
identifier/comment after a non-ASCII string) asserted **byte-exact under BOTH encodings** (UTF-8:
byte counts; UTF-16: code-unit counts differ) — proving `position.go` reuse. Include a multi-line token
split case.
**Reuses/migrates:** reuses `byteOffsetToCharacter`/`lineAt` from `position.go`; reuses the shared legend
value from T4 for index/bit lookup.
**Modeled gaps:** empty input → `Data: []uint32{}` (never nil-panic).
**DoD:** table test covering delta reset across lines, both encodings byte-exact, multibyte prefix,
multi-line split, empty; `go vet`/`gofmt` clean; no stdlib `json.Marshal`.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T4

---

### T6 — `textDocument/semanticTokens/full` handler (store-first, F7, end-to-end Phase A)
**Behavior:** Add the dispatch case in `server.go`. Gate on `stateInitialized`; decode
`protocol.SemanticTokensParams`. Snapshot `posEncoding` (and `idx` if needed for future Phase B) under the
`idxResMu` RLock and release before I/O (F7). Read the document **store-first** (open buffer), fall back to
disk/index only if not open; out-of-root/missing → return an **empty tokens result** (`{"data":[]}`),
never an error (FR-43). Call the analyzer seam `SemanticTokens(absPath, content)`, encode via T5, marshal
via `marshalResult`.
**Fixtures:** `internal/server/testdata/semantictokens/` — reuse `lexical.NSP` (or a copy) opened as a
buffer.
**Expected result:** for the open lexical fixture, the response `data` equals the T5-encoded stream
for the Phase-A tokens; a request for an unopened/out-of-root URI returns `{"data":[]}`.
**Reuses/migrates:** mirrors `provideCompletion`'s snapshot/store-first/F7 shape; `uriToRelPath`;
`marshalResult`. Wires the seam method through the server (imports only `analysis.Analyzer` + `model`).
**Modeled gaps:** unopened doc / unreadable / out-of-root → empty result, no error; a file that fails to
parse still returns lexical tokens (Phase A doesn't parse).
**DoD:** wire-bytes test on the dispatch result asserting `data`; empty-result path; F7 snapshot pattern;
seam purity (no parser internals in `internal/server`); `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T3, T5
**— Phase A end-to-end complete and shippable here. —**

---

### T7 — Phase B: reclassify data variables (`variable`) + parameters (`parameter`)
**Behavior:** Extend `natural.SemanticTokens` so that a `TokenIdentifier` occurrence matching a declared
data variable is emitted as `variable`, and one declared in a `PARAMETER` section is emitted as
`parameter`. Build the classification from the AST/extraction already available in `Analyze`
(`FileAnalysis.Definitions` with `SectionKind`, and the `ExtractVariableRefs` name/scope logic). An
identifier that matches no known category **falls back to nothing** (not emitted in Phase B — client keeps
its own coloring) — never dropped incorrectly, never fabricated. The `DEFINE DATA` declaration-name token
gets the `declaration` modifier (declaration vs use is knowable from position within a `DEFINE DATA`
section — reuse the section-boundary tracking).
**Fixtures:** `internal/analysis/natural/testdata/semantictokens/variables.NSP` — a `DEFINE DATA LOCAL`
with a variable (`#X`), a `PARAMETER` section with a param (`#P`), and both used in a statement body.
**Expected result:** the declaration site of `#X` → `variable`+`declaration`; each use of `#X` →
`variable`; declaration of `#P` → `parameter`+`declaration`; use of `#P` → `parameter`. Group-qualified
`#GROUP.FIELD` follows `ExtractVariableRefs` semantics (group and field emitted independently).
**Reuses/migrates:** reuses `FileAnalysis.Definitions`/`SectionKind` (feature 08) and the
`ExtractVariableRefs` scope/qualification logic (feature 27) — **write the identifier classifier once,
behind the seam**, so it is shared by all Phase-B tasks.
**Modeled gaps:** an undeclared identifier → not emitted (falls back to lexical/none); `&`-dynamic → not a
false variable.
**DoD:** fixture table test asserts (Type, Modifiers) for declaration + use of a LOCAL var and a PARAMETER
var; document order preserved; never panics; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T3

---

### T8 — Phase B: call targets (`function`) — CALLNAT/FETCH/RUN + PERFORM subroutine
**Behavior:** Emit a `function` token for the **target-name identifier** of a CALLNAT/FETCH/RUN edge and a
PERFORM subroutine. Reuse feature 06's `FileAnalysis.Edges` (`EdgeCalls`/`EdgeNavigatesTo`/`EdgePerforms`)
which already carry the target `Source`/name range. A **dynamic target** (`CALLNAT #VAR`,
`&`-placeholder → `EdgeCallsDynamic`/`EdgeNavigatesToDynamic`) is a modeled gap: the identifier is
classified by its **variable** rule (T7) if it's a known variable, else not emitted — **never** falsely
`function` (FR-17/FR-18).
**Fixtures:** `internal/analysis/natural/testdata/semantictokens/calls.NSP` — a literal `CALLNAT 'SUBPROG'`,
a `PERFORM MY-ROUTINE` with an inline `DEFINE SUBROUTINE MY-ROUTINE`, and a dynamic `CALLNAT #DYN`.
**Expected result:** `'SUBPROG'`… wait — CALLNAT target is a string literal, so its span is the literal;
the literal target of CALLNAT/FETCH/RUN → `function` (overriding the Phase-A `string` class for that
specific literal-target span). `MY-ROUTINE` (both the PERFORM site and the `DEFINE SUBROUTINE` name) →
`function` (definition name also gets `definition` modifier). `#DYN` → `variable` (if declared) or not
emitted — never `function`.
**Reuses/migrates:** `FileAnalysis.Edges` + edge `Source`/target ranges (feature 06); the inline-subroutine
match (feature 06/18 `DEFINE SUBROUTINE` NameRange). Extends the shared identifier classifier from T7 to
consult edges. Note the literal-target override precedence over Phase-A `string`.
**Modeled gaps:** dynamic/unresolved target never becomes `function`; INCLUDE copycode target is **not**
classified `function` (compile-time include, consistent with call-hierarchy exclusion) — record as a
deliberate skip.
**DoD:** fixture test asserts literal call target → `function`, PERFORM + inline subroutine name →
`function`(+`definition` on the def), dynamic target not `function`; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T7

---

### T9 — Phase B: DDM/view names (`type`) + DDM/view fields (`property`), with `modification` on writes
**Behavior:** Emit `type` for a DDM/view name (the view operand of READ/FIND/GET/STORE and SQL tables, and
a `VIEW OF <ddm>` name) and `property` for a DDM/view field reference. Reuse feature 08's
`FileAnalysis.DataAccess` (name + `NameRange`), feature 28's `VIEW OF` binding/view fields, and feature
09's `Structure`. **`modification` modifier:** apply to a **write-target** — a DDM/view field or view name
that is the target of a write edge (`EdgeWrites`: STORE, record-form UPDATE/DELETE, SQL INSERT/UPDATE/
DELETE) — reusing feature 27's shipped write-edge knowledge. (See OQ-4 note: DDM/data-access writes ARE
directly available from `EdgeWrites`; the variable-write case is T-deferred below.)
**Fixtures:** `internal/analysis/natural/testdata/semantictokens/ddm.NSP` — a `VIEW OF SOME-DDM`, a
`READ SOME-VIEW` (read), and a `STORE SOME-VIEW` (write), referencing a field.
**Expected result:** view name in `VIEW OF` and in READ → `type`; the STORE target view/field →
`property`/`type` + `modification`; a read-only field reference → `property` (no `modification`).
**Reuses/migrates:** `FileAnalysis.DataAccess`+`NameRange` (feature 08), `EdgeWrites` (feature 08/27),
`VIEW OF` binding + view-field decode (feature 28). Extends the shared classifier.
**Modeled gaps:** empty-`Name` record-form write site (feature 08 OQ-4) has no view name to color → not
emitted; a DDM outside the steplib chain / `TYPE: SQL` DDM → field still not fabricated (feature 28 limit).
**DoD:** fixture test asserts view→`type`, read field→`property`, write target→`property/type`+
`modification`; empty-name write site not emitted; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T7

---

### T10 — Phase B: system variables (`variable` + `readonly` + `defaultLibrary`) + variable `modification`
**Behavior:** (a) Emit a `*`-prefixed system variable (`*DATX`, `*TIME`, …) as `variable` with the
`defaultLibrary` **and** `readonly` modifiers (system vars are read-only, built-in). `ExtractVariableRefs`
already *excludes* `*`-system vars — the classifier must instead **recognize** them (a `TokenIdentifier`
whose first source char is `*`, distinguished from the `*` full-line comment which is a `TokenComment`).
(b) **Variable `modification`** (the OQ-4 variable half): detect a variable that is a **write target** from
statement context in the token stream — assignment (`#X := …`), `MOVE … TO #X`, `COMPUTE #X = …` — and add
`modification` to its `variable`/`parameter` token. This is a **bounded statement-context detector** in the
classifier (NOT readable off `VariableRef`, which has no direction — see OQ-4 finding). If the detector
proves fiddly, ship the system-var half and record variable-`modification` as a follow-up (graceful
degrade — never a wrong modifier).
**Fixtures:** `internal/analysis/natural/testdata/semantictokens/sysvar.NSP` — a `MOVE *DATX TO #TODAY`
(system var read + variable write) and a `#X := #Y` assignment.
**Expected result:** `*DATX` → `variable`+`readonly`+`defaultLibrary`; `#TODAY` (MOVE … TO target) →
`variable`+`modification`; `#X` (assignment LHS) → `variable`+`modification`; `#Y` (RHS) → `variable`
(no modification).
**Reuses/migrates:** extends the shared classifier; `readonly` also applies to declared constants
(`CONST`) if cheaply detectable — otherwise scope to system vars only and note it.
**Modeled gaps:** `*`-comment line must NOT be misread as a system var (it's a `TokenComment`); an
`&`-dynamic write target is not a fabricated variable.
**DoD:** fixture test asserts system var modifiers and variable write-target `modification`; `*`-comment not
misclassified; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T7 (variable base), and composes with T8/T9

---

### T11 — `textDocument/semanticTokens/range` handler (visible-range subset)
**Behavior:** Add the `textDocument/semanticTokens/range` dispatch case (decode
`protocol.SemanticTokensRangeParams`). Compute the full token list store-first (as T6), then **filter to
tokens intersecting the requested `Range`** (convert the requested protocol range to model coords via
`fromProtocolPosition`), then encode (T5) — the relative stream is re-based on the filtered set.
Store-first, F7, empty result for out-of-root/missing (FR-43).
**Fixtures:** reuse `internal/server/testdata/semantictokens/` fixtures.
**Expected result:** for a request scoped to a subrange, `data` contains only the tokens whose spans
intersect the range, correctly delta-encoded from the first in-range token.
**Reuses/migrates:** reuses T6's provider body + T5 encoder; `fromProtocolPosition` (position.go). Only the
filter differs.
**Modeled gaps:** a range with no tokens → `{"data":[]}`; a range partially covering a token — include the
whole token (LSP clients expect whole tokens).
**DoD:** wire-bytes test asserts the filtered/re-based stream; empty subrange; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T6

---

### T12 — Fuzz + bench: `FuzzSemanticTokens` never panics (FR-43) + large-file latency (NFR-3, measure-and-record)
**Behavior:** (a) Add `FuzzSemanticTokens` over the classifier entry (`natural.SemanticTokens(path,
content)`) **and** a fuzz over the server encoder (T5) with arbitrary `content` + Go-fabricated token
lists — asserting **no panic**, always a non-nil result, and (for the encoder) that `len(Data) % 5 == 0`
(well-formed stream). (b) Add a `//go:build bench` benchmark (off `just verify`) measuring `full` on a
large synthetic file (reuse the feature-22 corpus generator or a large fixture) and **record the figure**
in this file's `## Results` (NFR-3 measure-and-record — not a gate).
**Fixtures:** fuzz seeds from the T3/T7–T10 fixtures; bench uses a large generated buffer.
**Expected result:** fuzz green (no panic on garbage/truncated/multibyte input, incl. an unterminated
`<<` opaque span and a lone `*`); bench figure recorded.
**Reuses/migrates:** mirrors existing `Fuzz*` guards (`FuzzParse`, `FuzzProvideCompletion`); the feature-22
bench harness pattern.
**Modeled gaps:** malformed/partial input → partial tokens, never panic (FR-43).
**DoD:** both fuzz targets green in a short run; `len(Data)%5==0` invariant; bench recipe runs and a figure
is recorded in `## Results`; `go vet`/`gofmt` clean.
**Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** T6 (encoder), T3 (classifier); T11 optional

---

## Sequencing summary

```
T1 (model type) → T2 (seam + migrate doubles) → T3 (Phase A classify)
T3 → T4 (capability+legend) → T5 (encoder) → T6 (full handler)   ← Phase A shippable
T3 → T7 (variables/params) → T8 (call fns)
                           → T9 (DDM/view + write modification)
                           → T10 (system vars + variable modification)
T6 → T11 (range handler)
T6,T3 → T12 (fuzz + bench)
```

Write the identifier classifier **once** (T7) and extend it in T8–T10 — do not fork per category.

## Reviews required

- **`review-seam`** — a method is added to the `Analyzer` interface (shared contract). Confirm every
  implementer migrated, the external `lsp-graph` builder is source-compatible or updated, and
  `internal/server` imports only `analysis.Analyzer` + `internal/model` (no parser internals; the classifier
  lives behind the seam).
- **`review-protocol-conformance`** — new LSP methods (`semanticTokens/full`, `semanticTokens/range`), new
  capability + legend, relative-5-int encoding correctness, delta reset across lines, whole-tokens-on-range,
  empty-result sentinels (`{"data":[]}`), marshaling on the json/v2 path.
- **`review-robustness`** — the classifier parses/lexes new input paths (identifier reclassification,
  statement-context write detection, `*`-system-var recognition, multi-line token splitting); fuzz-backed
  no-panic (FR-43).
- **`review-performance`** — `full`/`range` are on the interactive request path (store-first, O(tokens),
  no workspace sweep); NFR-3 figure recorded.
- **`review-docs`** — new capability + two new methods → `CLAUDE.md` "Project state" note + capability list
  and `README.md` must sync at `/finalize-feature`; `TestInitialize` allow-list change is documented.

## Open questions

- **OQ-A (was OQ-1 — resolved, confirm scope):** legend fixed as types
  `[keyword, comment, string, number, operator, variable, parameter, function, type, property]` and
  modifiers `[declaration, definition, readonly, modification, defaultLibrary]`. **Confirm this is the v1
  legend** (the ordering is a wire contract — changing it later is a breaking change requiring a client
  refresh).
- **OQ-B (was OQ-4 — variable `modification`):** the plan assumed feature 27's write knowledge makes
  `modification` free. **Correction from current-state survey:** feature 27's write knowledge is
  DDM/data-access `EdgeWrites` only; `model.VariableRef` carries **no** read/write direction. So DDM/view
  field `modification` is free (T9), but **variable `modification` requires a new bounded statement-context
  write detector** (T10). **Decision requested:** ship variable `modification` in T10, or defer it (system
  vars only in T10, variable-`modification` as a fast-follow) if the detector adds risk? Recommend
  attempting it in T10 with graceful degrade (drop the modifier rather than emit a wrong one).
- **OQ-C (literal-target precedence):** a CALLNAT/FETCH/RUN target is a **string literal**, so T8 must
  **override** the Phase-A `string` classification for that specific span with `function`. **Confirm** we
  want the literal call-target colored as `function` (recommended — it reads as a call, matches most LSP
  servers) rather than left as `string`.
- **OQ-D (range vs delta, was OQ-3 — resolved):** `range` is included (T11), `full/delta` deferred and not
  advertised. Confirm no `delta` in v1.
- **OQ-E (`readonly` on constants):** T10 applies `readonly` to system vars; extending it to declared
  `CONST` variables is optional and depends on whether const-ness is cheaply available from
  `DataDefinition`. **Decision:** in-scope if trivial, else deferred with a note. (Non-blocking.)

## Results

**NFR-3 — large-file `semanticTokens/full` latency (measure-and-record, off the gate).**
`BenchmarkSemanticTokensFull` (`internal/server/provider_bench_test.go`, `//go:build bench`, run via
`just bench`) measures the full classifier (Phase A lexical + Phase B identifier/call/DDM/system-var
reclassification) plus the LSP relative-5-int encoder on a **5,000-line** synthetic Natural program:

```
BenchmarkSemanticTokensFull-16    1    1988823792 ns/op    128152 B/op
```

≈**1.99 s/op** and ≈**128 KB/op** for a 5,000-line file. The cost is dominated by the on-demand
re-parse + full extraction inside `SemanticTokens` (it calls `Analyze` internally). Real Natural
objects are typically far smaller (tens–hundreds of lines), where the request is sub-millisecond;
the request is store-first and O(tokens) with no workspace sweep. Recorded, not gated (NFR-3); a
future optimization could cache/parse-once per open-buffer version if large single files prove slow.

**FR-43 — never-panic (fuzz-backed).** `FuzzSemanticTokens` (classifier) and
`FuzzEncodeSemanticTokens` (server encoder) each ran a 30 s active fuzz plus their seed corpora
(fixtures + adversarial seeds: empty, lone `*`, unterminated `<<` opaque span, unterminated string,
multibyte blobs, `DEFINE DATA` with no `END-DEFINE`) with **no panics/crashes**; the encoder fuzz
also asserts the `len(Data) % 5 == 0` stream invariant.
