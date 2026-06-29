# Tasks: Parser Foundation

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements covered:** NFR-15 (replaceable backend), FR-30 (syntax diagnostics), M-6 (no silent gaps), M-5 (permanent regression fixtures)
**Branch:** `feat/00-parser-foundation` (work already in progress; substantial uncommitted implementation present)

> This is a **stabilization + completion** plan, not a greenfield build. The lexer, AST node set,
> recursive-descent parser, and Analyzer integration already exist on the branch and the test suite
> passes. However, several acceptance criteria are only *superficially* satisfied — the code compiles
> and "doesn't crash," but does not actually do what the criterion requires. Tasks below are scoped to
> close those real gaps and to harden the weak tests so the suite proves the criteria rather than
> merely exercising the code.

> **Plan-checkpoint decisions (applied below).** Four open questions were resolved toward the more
> expansive option:
> - **Q4 → `READ`/`STORE` get real AST nodes** (`ReadStatement`, `StoreStatement`) with parser support,
>   fixtures, and exact assertions — at foundation depth (target/clause names + positions), not full
>   data-access semantics. → **Task 5**.
> - **Q3 → DEFINE DATA parsed in full**: field names, level numbers, **types, array dimensions, and
>   redefinitions**, with fixtures and exact structural assertions. → **Task 6**.
> - **Q5 → `model.Diagnostic` gains a positional `Range`** (shared-contract change in `internal/model`),
>   reusing the existing `model.Range`/`model.Position` types; feature-02's diagnostic usage and
>   `model_test.go` migrate. → **Task 1a** (lands early; Tasks 7/9 depend on it).
> - **Q1 → inline `/* */` comments supported** (non-nesting), making fixture 05 meaningful, with exact
>   token assertions. → **Task 12** (now non-optional).
> - Parser posture is **error-recovering** (Story 3 mandates it). String-literal `Target` fields store
>   the **unquoted** value, asserted consistently (Task 4 / Q6).

---

## Current-state findings & impact

### What already exists (files on branch)

- `lexer.go` (`Lexer`, `Token`, `TokenType`, `NextToken`, `isKeyword`, `uppercase`) — tokenizes
  keywords/identifiers/string+numeric literals/operators/punctuation/`*` line comments; normalizes
  identifiers and keywords to upper-case. Covered by `lexer_test.go` with real, exact assertions.
- `ast.go` — 10 node types (`Program`, `Subroutine`, `DataSection`, `DataField`, `Map`,
  `IncludeStatement`, `CallStatement`, `FetchStatement`, `RunStatement`, `PerformStatement`), each with
  `StartPos`/`EndPos model.Position` and a `Position()` method; `Node` interface; parent/child slices on
  `Program`, `DataSection`, `Map`. **No `ReadStatement`/`StoreStatement` yet** (added in Task 5).
- `parser.go` (`Parser`, `NewParser`, `Parse`) — recursive-descent dispatch for `DEFINE DATA/SUBROUTINE/
  MAP`, `CALLNAT`, `PERFORM`, `INCLUDE`, `FETCH`, `RUN`; unrecognized tokens are skipped
  (partial-parse posture).
- `analyzer.go` — `Analyzer` implements `analysis.Analyzer` (compile-time assertion present); `Analyze`
  classifies object type, runs lexer+parser, sets `result.AST`, and has wiring to append a
  `DiagnosticError` from a parse error.
- `model.go` — `FileAnalysis.AST interface{}` and `FileAnalysis.Diagnostics []Diagnostic` already exist
  (the AST/Diagnostics carrying contract is in place). **`model.Range` and `model.Position` already
  exist** (model.go lines 112–122) and will be reused by the Diagnostic-Range change. `model.Diagnostic`
  currently has only `Message`+`Severity` (no position) — changed in Task 1a.
- Stub files `calls.go`, `data.go`, `hover.go`, `symbols.go` — package-doc + TODO only; belong to later
  features (06–08), **out of scope here**.
- Fixtures under `testdata/parser/`: `01-lexer-token-types.nsp`, `02-lexer-multi-line.nsp`,
  `03-parser-statements.nsp`, `04-parser-parse-errors.nsp`, `05-inline-comments.nsp`.
- Tests: `lexer_test.go`, `ast_test.go`, `parser_test.go`, `parser_diagnostics_test.go`,
  `parser_testdata_test.go`, plus `analyzer_test.go`, `objecttype_test.go` (the latter two belong to
  feature 02 and are otherwise unrelated; `analyzer_test.go` will be touched only by the Task 1a
  Diagnostic migration if it constructs diagnostics).

### Real gaps behind "passing" tests (code is ground truth)

1. **Parser never emits diagnostics (blocks FR-30 / M-6 / Story 3 & 4).** `Parse()` returns a hardcoded
   `nil` error and the dispatch loop silently `advance()`s past anything it does not recognize. The
   analyzer's diagnostic-append branch is therefore **dead code**. `parser_diagnostics_test.go` "passes"
   only because it `t.Log`s instead of asserting. No `Diagnostics` accumulator exists.
2. **AST positions are fabricated.** `parser.currentPos()` returns a constant `{Line:1, Column:1}` for
   every node. `ast_test.go` masks this by testing hand-constructed struct literals only;
   `parser_test.go`'s "position_accuracy" only checks `Line >= 1`.
3. **Lexer comment column bug.** The `*` line-comment branch reports the *end* column, not the start
   column; `lexer_test.go` asserts the buggy value (`Column: 20` for a col-1 comment).
4. **Inline `/* */` comments are not lexed.** Only `*` line comments are handled; fixture
   `05-inline-comments.nsp` is unused. Now in scope (Q1 → Task 12).
5. **`READ`/`STORE` have no AST nodes.** They are lexed as keywords and skipped. Now in scope (Q4 →
   Task 5) — they get real nodes.
6. **DEFINE DATA parsing is shallow.** `parseDataSection` only collects bare identifier names into
   `DataField.Name`; level numbers, `DataField.Type`, array dimensions, and redefinitions are not parsed.
   `DataField` has only `Name`+`Type string`. Now in scope (Q3 → Task 6) — requires AST extension.
7. **Statement extraction is loose.** `CallStatement.Parameters` and `FetchStatement.Into` never
   populated; string-literal `Target` quoting is inconsistent (lexer strips quotes only when content
   starts with a space). Q6 → store unquoted targets (Task 4).
8. **Weak/over-permissive tests** across `parser_test.go` (presence not value), `ast_test.go` (struct
   construction not parser behavior), `parser_testdata_test.go` + `parser_diagnostics_test.go` (`t.Log`).
9. **Scratch test files** `debug_test.go` and `debug2_test.go` assert nothing — remove (Task 0).

### Shared-contract / seam impact

- **`internal/model` changes once (Task 1a): `model.Diagnostic` gains a `Range model.Range` field.** This
  is the only shared-contract change. Reuse the existing `model.Range`/`model.Position` types — do **not**
  introduce a parallel position type (keeps the seam clean). Consumers/migration:
  - the **only** existing constructors of `model.Diagnostic` are the two in
    `internal/analysis/natural/analyzer.go` (feature-02 unrecognized-extension diagnostic + the
    parse-error branch) — both migrate in Task 1a;
  - `internal/model/model_test.go` (`TestFileAnalysisObjectTypeAndDiagnostics`) constructs a
    `Diagnostic` literal and must be updated;
  - no `internal/server`/`internal/workspace`/`internal/document` code constructs or reads
    `Diagnostic.Range` today, so there is no further downstream migration.
- **No `analysis.Analyzer` signature change.** `Analyze(path, content)` is unchanged.
- **`FileAnalysis.AST` stays `interface{}` to consumers.** Adding `ReadStatement`/`StoreStatement` and
  extending `DataField` are internal to `internal/analysis/natural`; LSP/workspace code must not
  type-assert `AST` to concrete `natural.*` nodes (Task 10 guards this). No consumer does today.

### Criterion → status summary

| Story / criterion | Status | Task |
|---|---|---|
| S1: case-insensitive normalization | satisfied | — |
| S1: multi-line statements | satisfied (lexer) | — |
| S1: tokens for all types | mostly satisfied; comment-column bug | Task 1 |
| S1: inline vs line comment handling | NOT satisfied | Task 12 |
| S1: fixture per token type w/ expected values | partial (exercised, not all asserted) | Task 2, 12 |
| S2: 10 AST node types | satisfied | — |
| S2: + READ/STORE nodes (Q4) | NOT satisfied | Task 5 |
| S2: each node carries position | NOT satisfied (fabricated) | Task 3 |
| S2: tree structure (parent/child) | satisfied | — |
| S2: fixture per node kind | partial | Task 4, 5, 6 |
| S3: parse common statements (incl. READ/STORE, DEFINE DATA) | partial | Task 4, 5, 6 |
| S3: diagnostics for malformed statements | NOT satisfied | Task 7 |
| S3: partial parse (error-recovering) | NOT proven | Task 7, 8 |
| S3: fixture per statement type | partial | Task 4, 5 |
| S3: fixture per parse error | fixture exists, unasserted | Task 7 |
| S4: `Analyze` returns `FileAnalysis.AST` | satisfied | Task 9 strengthens |
| S4: `Diagnostics` contains syntax errors (with Range) | NOT satisfied | Task 1a, 7, 9 |
| S4: interface unchanged | satisfied | — |
| S4: parser-backed analyzer satisfies seam | satisfied | Task 10 |
| S5: every statement type has a fixture | satisfied / extended | Task 5, 6 |
| S5: every parse error has a fixture | fixture exists; assertion missing | Task 7 |
| S5: tests verify AST structure/positions/relationships | NOT satisfied | Task 3, 4, 5, 6 |
| S5: tests verify diagnostics | NOT satisfied | Task 7, 9 |
| S5: fixtures permanent in `testdata/parser/` | satisfied | — |

---

## Ordered task list

> Each task is one red → green → refactor loop. Agents: `tdd-red` writes the failing test, `tdd-green`
> makes it pass minimally, `tdd-refactor` cleans up. DoD common to all: `go vet` + `gofmt` clean;
> `go test -race ./internal/...` green; the `analysis.Analyzer` signature unchanged; deterministic
> output. Only Task 1a changes `internal/model`.

### Task 0 — Remove scratch test files (cleanup, no TDD loop)

- **Behavior:** Delete `debug_test.go` and `debug2_test.go` (they only `fmt.Println`).
- **Expected result:** files gone; `go test ./internal/analysis/natural/` still green.
- **DoD:** files removed; suite green; `go vet` clean.
- **Agents:** none (mechanical).
- **Depends on:** none.

### Task 1a — Add `Range` to `model.Diagnostic` + migrate consumers (Q5, shared-contract change)

- **Behavior:** Add a `Range model.Range` field to `model.Diagnostic` so diagnostics carry precise
  positions. **Reuse the existing `model.Range`/`model.Position` types** (model.go lines 112–122) — do
  not invent a parallel type. Migrate every existing constructor and test.
- **Fixtures:** none.
- **Expected result:** `model.Diagnostic` is `{Message, Severity, Range}`. `model_test.go`'s
  `TestFileAnalysisObjectTypeAndDiagnostics` constructs and asserts a `Range`. The two existing
  constructors in `analyzer.go` compile (the feature-02 unrecognized-extension diagnostic may use a
  zero/`{1,1}` range, documented; the parse-error branch is rewired in Task 7 to carry a real range).
- **Reuses/migrates:** `internal/model/model.go`, `internal/model/model_test.go`,
  `internal/analysis/natural/analyzer.go` (both `model.Diagnostic{...}` sites). Grep-verify no other
  consumer constructs `model.Diagnostic`.
- **Modeled gaps:** sets up FR-30 diagnostics to carry editor-precise ranges.
- **DoD:** model + all consumers compile; `model_test.go` asserts the `Range` field; existing
  feature-02 `analyzer_test.go`/`objecttype_test.go` green; `go test -race ./internal/...` green;
  string values of existing `DiagnosticSeverity` constants unchanged (stable-contract rule).
- **Agents:** `tdd-red` (add a `model_test.go` assertion for `Range` that fails) → `tdd-green` →
  `tdd-refactor`.
- **Depends on:** none. **Land early — Tasks 7 and 9 depend on it.**

### Task 1 — Fix lexer comment start-column (regression-first)

- **Behavior:** `*` line-comment tokens must report the **start** column, like every other token.
- **Expected result:** `* This is a comment` at line 1, col 1 → `Token{TokenComment, "* This is a
  comment", Line:1, Column:1}`. Flip the existing `single_line_comment` assertion in `lexer_test.go`
  (currently `Column: 20`) to `1` in the red step.
- **Reuses/migrates:** comment branch in `lexer.go` (capture `startCol` before scanning); `lexer_test.go`.
- **DoD:** corrected assertion; other lexer cases green; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 0.

### Task 2 — Lexer fixture-driven token assertions (S1: fixture per token type with expected values)

- **Behavior:** Add a test that lexes `01-lexer-token-types.nsp` and asserts the exact token stream
  (type + literal + line/column) for one instance of each token type: keyword, identifier, string
  literal, numeric (int + decimal), operator (`<>`), `@` punctuation, `*` comment.
- **Fixtures:** existing `01-lexer-token-types.nsp` (reuse).
- **Expected result:** asserted token slice matches the fixture (`CALLNAT`→keyword, `'PROGRAM'`→string,
  `12345`/`3.14159`→numeric, `<>`→operator).
- **Reuses/migrates:** `lexer_test.go`.
- **DoD:** exact assertions; green with Task 1's column fix.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 1.

### Task 3 — Real source positions on AST nodes (S2/S3/S5)

- **Behavior:** Replace constant `currentPos()` with positions from the current token; each node's
  `StartPos` is its leading keyword's position, `EndPos` the last consumed token; `Program` spans the file.
- **Fixtures:** existing `03-parser-statements.nsp` + small inline multi-line inputs.
- **Expected result:** `CALLNAT 'MYPROG'` → `prog.Calls[0].StartPos == {1,1}`; a `PERFORM MYSUB` on line
  3 → `prog.Performs[0].StartPos.Line == 3`; assert a later-line statement reports that line (proving no
  hardcoded `(1,1)`).
- **Reuses/migrates:** `currentPos()` reads `p.current.Line/Column`; capture start/end in each
  `parseXxx`. Tighten `parser_test.go` "position_accuracy" to assert exact line.
- **DoD:** parser-driven position assertions for ≥3 statement kinds across lines; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 0.

### Task 4 — Exact structural assertions for existing statement/node kinds + unquoted-target convention (S2/S3/S5, Q6)

- **Behavior:** Assert exact extracted values (not presence) for the already-modeled statements:
  `CallStatement.Target`, `PerformStatement.Target`, `IncludeStatement.Target`, `FetchStatement.Target`
  (+ `Into`), `RunStatement.Target`, `Map.Name`, `Subroutine.Name`. **Standardize string-literal
  targets to the unquoted value** and fix the lexer's leading-space special case so `Target` is always
  the bare name.
- **Fixtures:** existing `03-parser-statements.nsp` (reuse); add a minimal fixture only if a kind can't
  be asserted cleanly from it.
- **Expected result:** `CALLNAT 'MYPROG'` → `prog.Calls[0].Target == "MYPROG"` (unquoted);
  `PERFORM MYSUB` → `"MYSUB"`; `DEFINE MAP / MY_MAP` → `prog.Maps[0].Name == "MY_MAP"`;
  `FETCH DATABASE MYDB` → `Target`/`Into` populated per a documented convention. Replace `len(...) != 0`
  checks in `parser_test.go`.
- **Reuses/migrates:** `parser_test.go`; minimal parser/lexer fixes (unquoting; correct `Subroutine.Name`
  and `FetchStatement` field population).
- **DoD:** exact value + count assertions for all listed kinds; unquoting convention asserted
  consistently; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 3.

### Task 5 — Add `READ`/`STORE` AST nodes with parser support (Q4, S2/S3/S5)

- **Behavior:** Add `ReadStatement` and `StoreStatement` AST node types (with `StartPos`/`EndPos` +
  `Position()` and slices on `Program`: `Reads []*ReadStatement`, `Stores []*StoreStatement`). Parse
  `READ` and `STORE` into them, capturing the target/clause names a foundation needs (e.g.
  `ReadStatement.Target` = the view/DDM name, `StoreStatement.Target` = the file/view name). **Do not**
  model full data-access semantics (record buffers, WHERE clauses) — those belong to the FR-19–23
  extraction feature.
- **Fixtures:** add `06-read-store.nsp` — minimal `READ <view>` and `STORE <view>` examples (sanitized).
- **Expected result:** parsing the fixture → `len(prog.Reads) == N`, `len(prog.Stores) == M` with exact
  `Target` and accurate positions per statement.
- **Reuses/migrates:** `ast.go` (new node types + Program slices), `parser.go` (new dispatch cases +
  `parseReadStatement`/`parseStoreStatement`), `ast_test.go` (struct cases for the new nodes),
  `parser_test.go`. `READ`/`STORE` are already lexed as keywords.
- **Modeled gaps:** ensure adding these does not make valid `READ`/`STORE` emit spurious diagnostics
  (Task 7 covers diagnostics; verify clean here).
- **DoD:** new nodes exist with positions; fixture-driven exact assertions; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 3 (positions), Task 4 (target/value conventions reused).

### Task 6 — Full DEFINE DATA detail: levels, types, arrays, redefinitions (Q3, S2/S3/S5)

- **Behavior:** Extend `DataField` (and add a redefinition representation) so `parseDataSection`
  captures: **level number**, **field name**, **type/format** (e.g. `A10`, `N7.2`, `P5`), **array
  dimensions** (e.g. `(1:10)` / multi-dimensional), and **redefinitions** (`REDEFINE <field>` with the
  redefining subfields). Stay within DEFINE DATA structure parsing — no value/semantic validation.
- **Fixtures:** add two fixtures (keep slices thin):
  - `07-data-arrays.nsp` — fields with level numbers, types, and array dimensions (1-D and multi-D);
  - `08-data-redefine.nsp` — a `REDEFINE` block with subfields.
- **Expected result:** for `07`, assert each `DataField`'s `Level`, `Name`, `Type`, and parsed
  dimensions (e.g. `[]Bound{{1,10}}`); for `08`, assert the redefine target name and its subfields'
  names/types. Exact structural assertions, not presence.
- **Reuses/migrates:** `ast.go` (`DataField` gains `Level int`, dimension representation; add a
  `Redefinition`/nested-field structure or `DataField.Redefines`/`Children`), `parser.go`
  (`parseDataSection` rewritten to parse level/type/array/redefine), `ast_test.go`, `parser_test.go`.
  Reconcile with the existing loose `parseDataSection`; the existing `DEFINE DATA` test cases tighten to
  assert the new fields.
- **Modeled gaps:** malformed data definitions feed Task 7 diagnostics — keep error-recovering.
- **DoD:** levels/types/arrays/redefinitions asserted exactly across both fixtures; existing DEFINE DATA
  assertions updated; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **This is the largest task — if a single
  red→green loop gets unwieldy, split into 6a (levels/types), 6b (arrays), 6c (redefinitions).**
- **Depends on:** Task 3 (positions), Task 4 (conventions).

### Task 7 — Parser emits syntax diagnostics with Range for malformed statements (FR-30, M-6, S3/S4/S5)

- **Behavior:** Add a diagnostics accumulator on the parser (e.g. `Program.Diagnostics
  []model.Diagnostic` or `Parser.Errors()`); record a diagnostic **with a populated `Range`** when a
  statement is malformed — `CALLNAT`/`FETCH DATABASE`/`READ`/`STORE` with a missing target, `DEFINE`
  with an unknown sub-keyword, unterminated string, malformed DEFINE DATA field. Recovery posture is
  **error-recovering** (record + continue) per Story 3.
- **Fixtures:** existing `04-parser-parse-errors.nsp` (reuse) + focused inline malformed inputs.
- **Expected result:** parsing `04` yields ≥1 diagnostic, each with a `Severity` and a `Range` pointing
  at the offending line/column; valid input yields **zero** diagnostics. Convert
  `parser_diagnostics_test.go` from `t.Log` to hard assertions, including asserting the diagnostic
  `Range` (e.g. correct `Start.Line`).
- **Reuses/migrates:** parser malformed branches; `model.Diagnostic` (now with `Range` from Task 1a);
  `DiagnosticError`/`DiagnosticWarning`.
- **Modeled gaps:** the FR-30 "parse errors surface as diagnostics, not silently discarded" guarantee.
  (Unresolvable references / `CALLS_DYNAMIC` are a later resolution-layer concern — out of scope here.)
- **DoD:** ≥1 ranged diagnostic per malformed case in fixture 04; zero for valid input; range asserted;
  recovery posture documented; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 1a (Range field), Task 3 (positions), Task 5 + Task 6 (so READ/STORE and
  DEFINE DATA malformations are diagnosed, not skipped).

### Task 8 — Partial-parse guarantee: bad lines don't drop good lines (S3, M-6)

- **Behavior:** Prove a malformed statement between two valid ones does not prevent extraction of the
  valid ones, and that a ranged diagnostic is recorded for the bad line.
- **Fixtures:** reuse `04-parser-parse-errors.nsp`.
- **Expected result:** trailing `CALLNAT 'PROG'` present in `prog.Calls` **and** ≥1 diagnostic for the
  malformed region; preceding valid constructs also present. Convert `parser_testdata_test.go`'s
  `t.Log` into real per-fixture assertions (no panic + expected node counts / diagnostic presence).
- **Reuses/migrates:** `parser_diagnostics_test.go`, `parser_testdata_test.go`.
- **Modeled gaps:** good lines retained **and** bad lines flagged (no silent gap).
- **DoD:** recovered-good + flagged-bad both asserted; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 7.

### Task 9 — Analyzer surfaces AST + ranged parser diagnostics through `FileAnalysis` (S4, FR-30)

- **Behavior:** `Analyzer.Analyze` copies the parser's diagnostics (with their `Range`) into
  `FileAnalysis.Diagnostics`; verify `FileAnalysis.AST` is the populated `*Program`.
- **Fixtures:** reuse `04` (malformed) and `03`/`06`/`07` (valid).
- **Expected result:** `Analyze("x.NSP", malformed)` → `Diagnostics` includes ranged parser error(s) in
  addition to any object-type diagnostic, plus non-nil `AST`; `Analyze("x.NSP", valid)` → non-nil `AST`,
  no parser diagnostics. Convert `TestAnalyzer_DiagnosticsForParseErrors`/`TestAnalyzer_ASTPopulation`
  from `t.Log` to hard assertions including the diagnostic `Range`.
- **Reuses/migrates:** `analyzer.go` reads the parser's accumulator (keep `err` only for catastrophic
  failure); preserve the feature-02 unrecognized-extension diagnostic ordering deterministically.
- **DoD:** analyzer-level assertions for AST + ranged diagnostics; feature-02 tests green; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 1a, Task 7.

### Task 10 — Seam-purity guard: AST consumed only via `model` (NFR-15, S4)

- **Behavior:** Keep the compile-time `var _ analysis.Analyzer = (*Analyzer)(nil)` and add a test that
  the parser-backed analyzer satisfies the seam and that `FileAnalysis.AST` is retrievable as
  `*natural.Program` **only within the natural package**. Document the constraint that LSP/workspace code
  must not type-assert `AST` to concrete nodes.
- **Expected result:** test instantiates `analysis.Analyzer = New(nil)`, calls `Analyze`, confirms result
  type + AST presence; doc comment records the seam constraint.
- **Reuses/migrates:** existing assertion; no model change.
- **DoD:** seam test green; no concrete `natural.*` import in `internal/server` or `internal/workspace`
  (grep guard noted for reviewer); `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 9.

### Task 11 — Fuzz target for the file-parsing entry point (robustness, M-6, ADR-013 parity)

- **Behavior:** Add `FuzzParse` (or `FuzzAnalyze`) asserting the parser never panics and always returns
  a non-nil `*Program` for arbitrary input — mirroring `FuzzProcessFile`. Seed corpus reuses fixtures
  01–08.
- **Expected result:** `go test -run=Fuzz -fuzz=FuzzParse -fuzztime=10s` finds no panic; plain
  `go test` runs the seed corpus.
- **Reuses/migrates:** new fuzz test in `internal/analysis/natural`.
- **Modeled gaps:** guards graceful degradation at the parser boundary.
- **DoD:** fuzz target compiles + runs seed corpus under plain `go test`; no panic; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 9 (and Tasks 5/6 so seeds exercise the new nodes).

### Task 12 — Inline `/* */` comment lexing (Q1, Story 1 comment handling)

- **Behavior:** Teach the lexer to recognize non-nesting `/* ... */` as `TokenComment` (terminate at the
  first `*/`; Natural does not nest these). The parser must skip inline comments between tokens so they
  don't break statement parsing. Wire up the previously-unused `05-inline-comments.nsp`.
- **Fixtures:** existing `05-inline-comments.nsp` (reuse).
- **Expected result:** `CALLNAT 'MYPROG' /* call to myprog */` lexes the trailing `/* ... */` as one
  `TokenComment`; `CallStatement.Target` is still `MYPROG` (unquoted per Task 4); the "nested-looking"
  line terminates at the first `*/` (asserted). Bare `/` and `*` operator/punctuation tests still pass.
- **Reuses/migrates:** lexer comment handling; parser comment-skip; `lexer_test.go`.
- **DoD:** fixture-driven exact token assertions for inline comments; existing operator/comment tests
  green; `-race` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** Task 2, Task 4.

---

## Reviews required (for `/review-feature`)

- **review-seam (NFR-15)** — **now elevated**: this feature changes a shared contract (`model.Diagnostic`
  gains `Range`, Task 1a). Confirm the `model.Range`/`Position` reuse, that all `Diagnostic` consumers
  migrated, that parser types stay behind `model.FileAnalysis`, and that no LSP/workspace code
  type-asserts concrete `natural.*` nodes (Task 10).
- **review-robustness** — the parser ingests arbitrary editor input, now emits ranged diagnostics, and
  parses richer DEFINE DATA / READ / STORE; focus on Task 11 fuzzing and partial-parse recovery
  (Tasks 7/8).
- **review-docs** — `CLAUDE.md` "Project state" says features 06–08 are stubs and doesn't mention a
  working parser; update the project-state note, the README "Parser-based extraction" section, and note
  the `model.Diagnostic` Range addition at `/finalize-feature`.
- **review-protocol** — *not required*: no LSP method is added (diagnostics land in `FileAnalysis` only;
  publishing to the editor is a later feature). Recorded so the reviewer doesn't expect a
  `publishDiagnostics` handler.

---

## Sequencing notes

- **Task 1a (model.Diagnostic Range) lands early** — Tasks 7 and 9 populate/assert the `Range`, so the
  field must exist first. It has no dependency and can run right after Task 0 (in parallel with the
  lexer/position work if desired).
- **Tasks 5 and 6 (new nodes + DEFINE DATA depth) precede Task 7** — so the diagnostics task can flag
  malformed `READ`/`STORE`/DEFINE-DATA constructs rather than silently skipping them, and so valid
  instances of those constructs are verified not to emit spurious diagnostics.
- **Task 6 is the heaviest** (levels + types + arrays + redefinitions); split into 6a/6b/6c if one
  red→green loop can't hold it.
- **Tasks 1/2/3/4 (lexer fixes + positions + structural assertions)** are independent of the model
  change and can proceed in parallel with Task 1a.
- **Recommended order:** 0 → 1a → 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12. (1a may run
  concurrently with 1–4; 12 depends only on 2+4 and may move earlier once those land.)

## Open questions

All four plan-level open questions (Q1 inline comments, Q3 data depth, Q4 READ/STORE, Q5 Diagnostic
Range) were resolved at the checkpoint and are folded into the tasks above. Remaining smaller decisions
to confirm within the relevant task:

- **Q6 (Task 4): unquoted target convention** — adopted (store the bare name; fix the lexer leading-space
  special case). Confirm the same convention applies to `INCLUDE`/`FETCH`/`RUN` literal targets.
- **DEFINE DATA dimension representation (Task 6):** confirm the AST shape for array bounds (e.g. a
  `[]Bound{Lower,Upper}` slice) and for redefinitions (nested `DataField.Children` vs a dedicated
  `Redefinition` node) before the green step, so `ast.go` settles once.
- **Diagnostic range granularity (Task 7):** confirm whether a diagnostic range should span the whole
  offending statement or just the offending token (recommend: the offending token/keyword, widened to
  the statement when the token is missing).

---

## Remediation tasks (from /review-feature — Round 1)

> Regression-first: each RED step writes a test that reproduces the finding (the test that should have
> caught it), then GREEN fixes it. DoD per task: gofmt/vet clean, `go test -race` green, FuzzParse no panic.

### Task R1 — Fix FETCH statement modeling (blocker; review-extraction findings 1, 2, 4 + 5)

- **Finding:** The parser invents non-existent `DATABASE`/`RECORD` FETCH clauses — `FETCH DATABASE MYDB`
  yields `Target="DATABASE"` and discards `MYDB`. Real Natural: `FETCH [REPEAT|RETURN] operand1` where
  operand1 is the program name (string literal or identifier). `FETCH RETURN 'PGM'` / `FETCH REPEAT 'PGM'`
  drop the target silently (no diagnostic — violates M-6). The diagnostic message presupposes the bogus
  DATABASE clause; `FetchStatement.Into` is dead.
- **RED:** assert `FETCH 'MYPROG'` → Target `"MYPROG"`; `FETCH RETURN 'MYPROG'` → Target `"MYPROG"`
  (return flag recorded); `FETCH REPEAT 'MYPROG'` → Target `"MYPROG"`; `FETCH` with no operand on its
  line → 1 diagnostic "FETCH requires a target operand". These fail today (DATABASE clause logic).
- **GREEN:** rewrite `parseFetchStatement` to skip optional leading `REPEAT`/`RETURN` then read operand1
  (unquoted); remove DATABASE/RECORD special-casing; fix the diagnostic message; remove or document the
  dead `FetchStatement.Into`. Rewrite the invalid `FETCH DATABASE MYDB` in fixtures 01/03/04 to valid
  Natural (`FETCH 'MYPROG'` / `FETCH RETURN 'MYPROG'`) and update affected lexer/parser/ast tests.
- **DoD:** FETCH extraction matches Natural grammar; no silent drop; fixtures valid; suite green.

### Task R2 — Fix CRLF line-counter double-increment (blocker; review-extraction finding 3)

- **Finding:** The lexer whitespace loop increments the line counter for `\r` AND `\n` independently, so
  each `\r\n` advances the line by 2 — corrupting every AST position and diagnostic Range for
  mainframe-exported (CRLF) `.NSx` files (the primary target format).
- **RED:** parse a CRLF source (e.g. `CALLNAT 'A'\r\nPERFORM SUB`) and assert the second statement is on
  line 2 (currently reports line 3). Add a CRLF regression fixture/test.
- **GREEN:** treat `\r\n` as a single line terminator in the whitespace loop (consume `\n` after `\r`
  without a second increment); fix the misleading "correct for CRLF" comment.
- **DoD:** CRLF and LF inputs report identical line numbers; positions correct.

### Task R3 — Parser-driven Subroutine.Name assertion (CONCERNS; review-acceptance finding 1)

- **Finding:** Story 3 / Task 4 require exact `Subroutine.Name`, but no parser-driven test asserts it
  (only hand-built struct literals). Behavior is correct but unproven.
- **RED→GREEN:** add a parse-and-assert test (e.g. from fixture 03) that `prog.Subroutines[0].Name` is the
  extracted name. Likely passes immediately (behavior exists) — coverage hardening; if it fails, fix.

### Task R4 — Targeted tests read the fixtures they cite (CONCERNS; review-acceptance finding 2)

- **Finding:** Several targeted tests claim a fixture is "the source" but parse inline string copies that
  can drift from the file. Make them read the cited fixture, OR correct the comments to say they use an
  inline mirror. (The R1 FETCH fixture rewrite intersects here.)
- **DoD:** no test comment claims to parse a fixture it does not read.

> Deferred (non-blocking, accepted): review-docs drift → fixed in /finalize-feature; review-seam nits
> (seam_test WalkDir/subpackages); review-robustness minor (explicit `Terminated` flag on string tokens).
>
> Round-1 re-review verdict: **PASS** (all 3 blockers fixed; both acceptance CONCERNS resolved).
> Deferred to later features (reviewers agreed these are not foundation blockers):
> - **FETCH RETURN/REPEAT flag** — `FetchStatement` records the program name but not whether it was
>   `FETCH RETURN` (call-like) vs plain `FETCH` (goto-like). Add `IsReturn`/`IsRepeat` when the
>   call-dependency edge-emission feature (06/07) needs the call-like vs goto-like distinction.
> - **`FETCH (` (present-but-non-operand) emits no diagnostic** — consistent with PERFORM/READ/STORE;
>   a uniform "non-operand token after keyword" diagnostic is a future hardening item (CALLNAT is stricter).
> - **CRLF whitespace-loop**: fixed for line counting (R2); explicit `TokenError` for unterminated
>   strings remains a future lexer-contract cleanup.
