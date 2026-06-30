# Tasks: Call & Dependency Extraction (feature 06)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-10, FR-11, FR-12, FR-13, FR-14, FR-15, FR-17, FR-18; NFR-6; M-3, M-4, M-6
**Scope:** per-file *extraction* of call/dependency relationships into `FileAnalysis.Edges`.
Cross-file *resolution* (steplib binding, inline-before-external selection, copycode file binding) is
explicitly **out of scope** — it lands in the resolution feature. This feature emits unresolved edges
with caller context and the correct `EdgeKind`, plus the static-vs-dynamic classification.

**Decisions locked at plan approval (2026-06-30):**

1. **Dynamic FETCH/RUN edge kind** → **add a new additive `model` constant `EdgeNavigatesToDynamic`
   (`"NAVIGATES_TO_DYNAMIC"`)**, mirroring the static `EdgeNavigatesTo`. This keeps the dynamic kinds
   symmetric with the static ones (`CALLS`/`CALLS_DYNAMIC` ∥ `NAVIGATES_TO`/`NAVIGATES_TO_DYNAMIC`) and
   keeps call vs navigation distinct. It is the feature's **only** `internal/model` change — a one-line
   additive constant with **zero existing consumers to migrate**. (Resolves Open Question 2.)
2. **Inline-subroutine marking** → **set `EdgeEntry.Target` to the inline `DEFINE SUBROUTINE`
   definition's range** when the PERFORM target matches an in-file subroutine; leave it zero/unresolved
   otherwise. No `EdgeEntry` field added. (Resolves Open Question 1, option (a).)
3. **Library-qualified FETCH/RUN** → **CONFIRMED by `natural-expert` (verified 2026-06-30 against
   Software AG docs):**
   - **FETCH has NO source-level library qualifier.** Syntax: `FETCH [REPEAT] [RETURN] operand1
     [operand2 [(parameter)]] ...` — `operand1` is the program name; any `operand2` is a **parameter
     field pushed on the stack, NOT a library**. The parser must not treat a FETCH operand2 as a
     library. Library/steplib resolution for FETCH is purely runtime → defer to resolution feature.
   - **RUN HAS a library qualifier.** Syntax: `RUN [REPEAT] [program-name [library-id]]`. `library-id`
     is an optional second **bare positional operand** (no keyword, not dotted). When present,
     resolution targets that single library and **bypasses the steplib chain**. RUN takes no
     parameter-field operands, so a second token is unambiguously the library. (RUN is a system command,
     rare in source.)
   - **Decision (user-approved):** add a `Library string` (+ range) field to `RunStatement` (parser),
     capture `library-id` when present, AND add a **second additive `model` field
     `EdgeEntry.Library string`** so the marking survives the on-disk cache (the AST is NOT persisted —
     only `FileAnalysis.Edges` is; `cache.go` serializes `[]model.EdgeEntry`). FETCH gets no library
     field. Adding `EdgeEntry.Library` changes the cache serialization → **bump the cache-format version
     constant** so stale caches rebuild cleanly (CLAUDE.md). (Resolves Open Question 3.)

   **This feature therefore makes TWO additive `internal/model` changes** (both with zero existing
   consumers): the `EdgeNavigatesToDynamic` constant (DECISION 1) and the `EdgeEntry.Library` field
   (DECISION 3).

---

## Current-state findings & impact

Surveyed `internal/model/model.go`, `internal/analysis/natural/{analyzer.go,parser.go,ast.go,lexer.go,calls.go}`,
`internal/workspace/{index.go,cache.go}`, and the seam/test conventions. Findings, in priority order:

### The shared model contract already exists — no model change, no consumer migration

`internal/model/model.go` already defines the full edge contract this feature emits:

- `EdgeKind` with all the constants needed: `EdgeCalls` (`"CALLS"`), `EdgeCallsDynamic`
  (`"CALLS_DYNAMIC"`), `EdgeNavigatesTo` (`"NAVIGATES_TO"`), `EdgePerforms` (`"PERFORMS"`),
  `EdgeIncludes` (`"INCLUDES"`). (`EdgeReads`/`EdgeWrites` belong to feature 07.)
- `EdgeEntry{Source Range, Target Range, Kind EdgeKind, TargetName string}`.
- `FileAnalysis.Edges []EdgeEntry`.

The workspace index and cache already round-trip `FileAnalysis.Edges` end-to-end
(`internal/workspace/cache.go` serializes `Edges`; `index.go` `Invalidate` already walks
`EdgeIncludes` edges to compute INCLUDE dependents transitively). **No `internal/model` change and no
consumer migration is required** — populating `Edges` is sufficient for the index/cache/invalidation
machinery to work. This means Story 4's "changing a copycode re-evaluates includers" acceptance
criterion is *already satisfied at the index layer* (see `index.go:Invalidate`, `EdgeIncludes` branch);
this feature only needs to emit the `EdgeIncludes` edges so that machinery has data to traverse.

`EdgeEntry.Source`/`Target` are `model.Range` (`{Start,End Position}`), but the existing parser stores
single `model.Position` start/end pairs on each statement node. `Source` is the call-site range
(start→end of the statement); `Target` should be the range of the *target operand* token (for an intra-file
inline-PERFORM target it may later be filled by resolution — for extraction, set it to the operand's
range and leave cross-file binding to resolution). Decide the exact `Source`/`Target` convention in
Task 1 and hold it constant across all edge kinds.

### Critical AST gap: literal-vs-variable distinction is discarded by the parser

This is the central blocker and must be fixed *first*. The static-vs-dynamic classification that
Stories 1, 2, 5, and 6 all depend on requires knowing whether a call/transfer target was written as a
**string literal** (`CALLNAT 'MYPROG'` → static) or an **identifier/variable** (`CALLNAT #VARIABLE` →
dynamic). The parser currently collapses both into a bare `Target string`:

- `parseCallStatement` (parser.go:372–387) sets `call.Target` from *either* `TokenLiteralString` (via
  `consumeStringTarget`, which strips the quotes) *or* `TokenIdentifier` — the literalness is lost.
- Same pattern in `parseFetchStatement`, `parseRunStatement`, `parseIncludeStatement`.
- `parsePerformStatement` only reads `TokenIdentifier` (PERFORM targets are subroutine names, not
  quoted literals — that is correct), but it provides no inline-subroutine marking.

So `CALLNAT 'MYPROG'` and `CALLNAT MYPROG` (identifier) and `CALLNAT #VAR` are today indistinguishable
in the AST. **An internal AST contract change is required**: add a literalness flag (proposed
`TargetIsLiteral bool`) and a target-operand range to `CallStatement`, `FetchStatement`, `RunStatement`,
and `IncludeStatement`, populated by the parser from the token type. This change is **entirely behind
the Analyzer seam** — the `natural.*` AST node types are not part of `internal/model` and are not
consumed by any LSP-facing package (guarded by `seam_test.go`), so no cross-package migration is needed.
The only consumers of the AST nodes are the parser's own tests and the new `calls.go` extractor.

Sequencing consequence: **Task 1 (parser AST refactor) lands before any extraction task.**

### Other AST gaps

- **No `Library` field** for library-qualified transfer targets (Story 5, criterion 3: "a statement
  that explicitly names a target library is marked for library-specific resolution"). FETCH/RUN can name
  a library. Needs investigation in Task 7 — see Open Questions on the exact Natural syntax; if a
  reliable surface form exists, add a `Library string` field; otherwise record as an open question and
  scope it out.
- **No runtime-substitution placeholder detection** (Story 6, FR-18). The lexer treats `&` as an
  identifier-*prefix/body* character (lexer.go:281,288), so an ampersand variable like `&PROG` lexes as
  a `TokenIdentifier` and is already naturally classified dynamic. But a *literal containing* a
  placeholder — e.g. `CALLNAT 'PRG&LANG'` or `'PRG-&1'` inside quotes — lexes as a `TokenLiteralString`
  and would otherwise become a *false static edge* to a non-existent object literally named `PRG&LANG`.
  The extractor must detect `&` inside an otherwise-literal target and downgrade it to dynamic. This is
  a pure extraction-layer concern (Task 8); no lexer change is needed.

### `calls.go` is a stub — this is where extraction belongs

`internal/analysis/natural/calls.go` is package-doc + TODO only. All extraction logic in this feature
lands there, walking the `*Program` AST and appending `model.EdgeEntry` values. `analyzer.go:Analyze`
already parses to `*Program` and assigns it to `result.AST`; extraction is wired in by having `Analyze`
call the new extractor and append its edges to `result.Edges` (Task 9).

### Diagnostics channel is already separate (FR-17 / M-6 / NFR-6)

Parse errors already flow through `Program.Diagnostics` → `FileAnalysis.Diagnostics` with real ranges
(analyzer.go:66–69). Dynamic/unresolved references are a *modeled outcome* and must flow through
`Edges` as `EdgeCallsDynamic`, **never** as a diagnostic. The two channels are structurally distinct
already; the extraction tasks must preserve that separation (no task may emit a diagnostic for a
variable target).

### Conventions to follow

- Table-driven tests in `internal/analysis/natural/`; minimal sanitized `.NSx` reproducer fixtures
  under `internal/analysis/natural/testdata/` (existing parser fixtures live in `testdata/parser/`; put
  this feature's fixtures under `testdata/calls/`).
- Deterministic output: `Edges` must be emitted in source order (statement start position), so the
  index/cache produce stable results.
- The fuzz target `FuzzParse` already guards the parser; if Task 1 widens parser behavior, the existing
  fuzz target covers it. The extractor (`calls.go`) should get a `FuzzExtractCalls`-style target only if
  it parses raw input directly — it does not (it walks an existing AST), so a fuzz target is optional;
  pin robustness instead with malformed-AST table cases.

---

## Ordered task list

### Task 1 — Parser: capture literal-vs-variable distinction and operand range (foundation)

**Behavior:** Extend the AST so the extractor can tell a quoted literal target from an
identifier/variable target, and so it can locate the target operand. Add `TargetIsLiteral bool` and a
target-operand `model.Range` (proposed `TargetRange`) to `CallStatement`, `FetchStatement`,
`RunStatement`, and `IncludeStatement`. Populate them in `parseCallStatement`, `parseFetchStatement`,
`parseRunStatement`, `parseIncludeStatement`: set `TargetIsLiteral=true` when the operand token was
`TokenLiteralString`, `false` when it was `TokenIdentifier`; record the operand token's start/end as
`TargetRange`. No behavior change to `Target` (still the unquoted name). No change to extraction yet.

**Fixtures:** none new — drive with inline source strings in the parser test (e.g. `CALLNAT 'MYPROG'`,
`CALLNAT #VAR`, `FETCH 'RPT'`, `FETCH #DEST`, `INCLUDE 'CC'`).

**Expected result:** parsing `CALLNAT 'MYPROG'` yields `CallStatement{Target:"MYPROG",
TargetIsLiteral:true, TargetRange: range of 'MYPROG'}`; parsing `CALLNAT #VAR` yields
`{Target:"#VAR", TargetIsLiteral:false}`. Likewise for FETCH/RUN/INCLUDE.

**Reuses/migrates:** extends existing `ast.go` node structs and the four `parse*Statement` funcs in
`parser.go`. Internal to `natural` package — behind the seam, no cross-package migration. Existing
parser tests (`parser_test.go`, `ast_test.go`, `parser_testdata_test.go`) must stay green (they assert
`Target`, which is unchanged).

**DoD:** new fields populated for all four statement kinds; existing parser/AST tests green; `FuzzParse`
still passes; `gofmt`/`go vet` clean; seam preserved (no `internal/model` change).

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** none.

---

### Task 2 — CALLNAT static extraction (Story 1; FR-10, M-3)

**Behavior:** In `calls.go`, add the extractor entry point (proposed `extractEdges(prog *Program)
[]model.EdgeEntry`) and emit a static `EdgeCalls` edge for every `CallStatement` with
`TargetIsLiteral==true`. `TargetName` = the unquoted literal; `Source` = statement range; `Target` =
`TargetRange`; `Kind` = `model.EdgeCalls`.

**Fixtures:** `testdata/calls/01-callnat-static.NSP` — a small program with two literal `CALLNAT 'A'`
and `CALLNAT 'B'` calls plus surrounding non-call statements (so we prove zero false edges).

**Expected result:** exactly two edges, both `EdgeCalls`, `TargetName` `A` and `B`, in source order,
each carrying the correct call-site range; no edges for the non-call lines.

**Reuses/migrates:** uses Task 1's `TargetIsLiteral`/`TargetRange`. New `calls.go` body.

**DoD:** table-driven test asserts edge count, kinds, target names, ranges, ordering; zero false edges
(M-3); deterministic source-order output; `gofmt`/`go vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Task 1.

---

### Task 3 — Dynamic CALLNAT modeling (Story 2; FR-11, FR-17, M-6)

**Behavior:** In the same extractor, emit `EdgeCallsDynamic` for every `CallStatement` with
`TargetIsLiteral==false` (variable target). Preserve caller context: `Source` = call-site range,
`TargetName` = the variable/expression text (e.g. `#VARIABLE`), `Kind` = `model.EdgeCallsDynamic`. Must
**not** emit any diagnostic for a variable target — it is a modeled outcome, not a parse error.

**Fixtures:** `testdata/calls/02-callnat-dynamic.NSP` — `CALLNAT #PROGNAME` plus a sibling
`CALLNAT 'STATIC'` (to prove static and dynamic are visibly distinct in one file).

**Expected result:** two edges — one `EdgeCalls` (`STATIC`) and one `EdgeCallsDynamic` (`#PROGNAME`);
`FileAnalysis.Diagnostics` empty (no diagnostic for the dynamic call). The dynamic edge carries the
variable name in `TargetName` and the correct call-site `Source`.

**Reuses/migrates:** extends Task 2's extractor.

**DoD:** test asserts the dynamic edge kind, target name, and call-site range; asserts **no diagnostic**
is produced for the variable target (FR-17 channel separation); static and dynamic edges coexist;
`gofmt`/`go vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Task 2.

---

### Task 4 — PERFORM extraction with caller context (Story 3, part 1; FR-12)

**Behavior:** Emit an `EdgePerforms` edge for every `PerformStatement`, `TargetName` = the subroutine
name, `Source` = the PERFORM statement range, `Target` = operand range. PERFORM targets are always
identifiers (never quoted) so there is no literal/dynamic split for the edge *kind* here — all PERFORMs
emit `EdgePerforms`. (Inline-subroutine marking is Task 5.)

**Fixtures:** `testdata/calls/03-perform.NSP` — a program with `PERFORM CHECK-INPUT` and
`PERFORM PROCESS-RECORD`.

**Expected result:** two `EdgePerforms` edges with the two subroutine names, in source order, correct
call-site ranges.

**Reuses/migrates:** extends the extractor; uses existing `PerformStatement.Target`.

**DoD:** test asserts edge count/kinds/names/ranges; `gofmt`/`go vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Task 1 (no new AST field needed,
but ordered after the foundation).

---

### Task 5 — Inline-subroutine identification for PERFORM (Story 3, part 2; FR-12, M-4)

**Behavior:** Mark whether a PERFORM target matches an **inline** `DEFINE SUBROUTINE` defined in the
*same* object, so the later resolution feature can apply inline-before-external ordering. The AST
already captures inline subroutines in `Program.Subroutines` (each with a `Name` and a definition
range). The extractor builds a **map of inline subroutine name → definition range** (single pass, no
quadratic lookup) and, for each `EdgePerforms` edge, looks the target name up.

**`EdgeEntry.Target` convention for PERFORM (DECISION 2, user-approved — option (a), no model change):**
- PERFORM target **matches** an in-file `DEFINE SUBROUTINE` → set `Target` to that definition's range
  (intra-file resolved).
- PERFORM target has **no** in-file match → set `Target` to the **zero `model.Range`** (unresolved →
  deferred to the resolution feature's external binding).

This **supersedes** Task 4's interim PERFORM `Target` = operand-range behavior, so the Task 4 test
(`TestExtractEdges_Perform`, fixture `03-perform.NSP` which has no inline defs) must be updated in this
task's RED phase to assert `Target == zero Range` for its two (external) performs, with a comment noting
Task 5 finalized the PERFORM `Target` semantics. **No change to the CALLNAT/FETCH/RUN/INCLUDE `Target`
convention** (those targets are inherently cross-file; `Target` stays the operand-span at the reference
site, definition binding deferred to resolution). Document the full convention in the `extractEdges`
godoc:
> `Target` = the inline `DEFINE SUBROUTINE` definition range for an in-file-resolved PERFORM, else the
> zero Range; for CALLNAT/FETCH/RUN/INCLUDE it is the operand-span at the reference site (cross-file
> binding deferred to resolution).

**Fixtures:** `testdata/calls/04-perform-inline-and-external.NSP` — a program that both `DEFINE
SUBROUTINE SHARED-LOGIC ... END-SUBROUTINE` (inline) *and* `PERFORM SHARED-LOGIC` *and* `PERFORM
EXTERNAL-ONLY` (no inline def). This satisfies the plan's "fixture with both an inline and a same-named
external subroutine marks both for resolution" — the inline one is marked resolvable in-file, the
external-only one is left for cross-file resolution.

**Expected result:** the `PERFORM SHARED-LOGIC` edge is marked as having an in-file inline definition
(target range points at the `DEFINE SUBROUTINE SHARED-LOGIC` node); the `PERFORM EXTERNAL-ONLY` edge is
marked unresolved-in-file (left for external resolution). Removing the inline definition (a separate
test case with a fixture lacking the `DEFINE SUBROUTINE`) makes `SHARED-LOGIC` also unmarked — proving
the inline-before-external precedence is data-driven, with the actual external binding deferred to the
resolution feature (plan: "Removing the inline definition makes it resolve externally (resolution
feature)").

**Reuses/migrates:** uses `Program.Subroutines` from the existing parser. Extends Task 4's extractor.

**DoD:** test asserts the inline target is marked in-file-resolved and the external-only target is not;
a second case (no inline def) shows the same name unmarked; M-4 precedence representable; `gofmt`/`go
vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Task 4.

---

### Task 6 — INCLUDE extraction (Story 4; FR-13)

**Behavior:** Emit an `EdgeIncludes` edge for every `IncludeStatement`, `TargetName` = the copycode
name, `Source` = statement range, `Target` = operand range, `Kind` = `model.EdgeIncludes`. Copycode
targets are treated as literal names (the placeholder case is handled by Task 8); INCLUDE never produces
a dynamic edge from a plain name.

**Fixtures:** `testdata/calls/05-include.NSP` — a program with `INCLUDE 'COMMON-DECLS'` and `INCLUDE
ERRHANDLER` (Natural allows the unquoted form).

**Expected result:** two `EdgeIncludes` edges with the two copycode names, source order, correct ranges.
Note that incremental re-analysis on copycode change is already handled by
`workspace/index.go:Invalidate` (it walks `EdgeIncludes`) — **no index change in this feature**; add a
note to the test that the edge kind is exactly what `Invalidate` consumes, and confirm via an existing
`index_test.go` case (no new index test needed unless coverage is missing).

**Reuses/migrates:** uses existing `IncludeStatement`; the index `Invalidate`/`EdgeIncludes` machinery
already exists and is reused unchanged.

**DoD:** test asserts edge count/kind/names/ranges; documents that `Invalidate` already consumes these
edges (Story 4 incremental criterion satisfied at the index layer); `gofmt`/`go vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Task 1.

---

### Task 7 — FETCH/RUN extraction, static + dynamic + library marking (Story 5; FR-14, FR-15)

**Behavior:** Emit navigation edges for `FetchStatement` and `RunStatement`. A literal target
(`TargetIsLiteral==true`) → static `EdgeNavigatesTo`; a variable target (`TargetIsLiteral==false`) →
**`EdgeNavigatesToDynamic`** (the new additive `model` constant `"NAVIGATES_TO_DYNAMIC"` — DECISION 1).
Add this constant to `internal/model/model.go` in this task (the feature's only `model` change; no
existing consumers). Caller context preserved on every edge. Add `EdgeNavigatesToDynamic` (`"NAVIGATES_TO_DYNAMIC"`) to
`internal/model` in this task. **Library marking** (criterion 3, DECISION 3 — confirmed by
`natural-expert`): **FETCH has no source-level library qualifier** (its `operand2` is a stack parameter
field, not a library — must NOT be parsed as one); FETCH library resolution defers to the resolution
feature. **RUN takes an optional second positional `library-id`** (`RUN [REPEAT] [program-name
[library-id]]`): add a `Library string` (+ range) field to `RunStatement` and capture `library-id` when
present; add a `Library string` field to `model.EdgeEntry`; set the RUN edge's `Library` to the
captured library-id (empty when absent). Because `EdgeEntry` is cache-serialized, **bump the
cache-format version constant** (find it in `internal/workspace/cache.go`) so stale caches rebuild. A
RUN with a library-id resolves against that single library and bypasses the steplib chain (note for the
resolution feature). Add a fixture `testdata/calls/07-run-library.NSP` with `RUN 'BATCHJOB' 'MYLIB'`.

**Fixtures:** `testdata/calls/06-fetch-run.NSP` — `FETCH 'RPT001'`, `RUN 'BATCHJOB'`, `FETCH #DYNRPT`
(dynamic). If library syntax is confirmed, a second fixture
`testdata/calls/07-fetch-library.NSP` with the library-qualified form.

**Expected result:** two static `EdgeNavigatesTo` edges (`RPT001`, `BATCHJOB`), one dynamic edge
(`#DYNRPT`) following the Task 3 dynamic convention; caller context on each; the library case (if in
scope) marked. No diagnostic for the dynamic FETCH (channel separation, FR-15/FR-17).

**Reuses/migrates:** uses Task 1's literalness flag; mirrors Task 2/3 logic.

**DoD:** test asserts static vs dynamic split, edge kinds, names, ranges; no diagnostic for variable
target; library marking decided (in scope or recorded as open question); `gofmt`/`go vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Tasks 1, 3.

---

### Task 8 — Runtime-substitution placeholders in literal targets (Story 6; FR-18)

**Behavior:** A literal target that *contains* a runtime-substitution placeholder (`&`) must **not**
become a false static edge to the raw-text object. In the extractor, after extracting a literal target
(for CALLNAT/FETCH/RUN/INCLUDE), detect `&` inside the literal value; when present, downgrade the edge
to the dynamic representation (the same one Task 3/7 use) with caller context preserved, reflecting the
runtime-variable nature. Pure extraction-layer change — no lexer change (the lexer already lexes
`'PRG&LANG'` as one `TokenLiteralString`). Decide the relationship type per Open Question (dynamic vs.
resolved-with-wildcard); default to dynamic.

**Fixtures:** `testdata/calls/08-placeholder-literal.NSP` — `CALLNAT 'PRG&LANG'` (placeholder literal)
alongside `CALLNAT 'PLAINPROG'` (clean literal, proves no false-negative downgrade).

**Expected result:** `PLAINPROG` → static `EdgeCalls`; `PRG&LANG` → dynamic edge (no static edge to a
literal object named `PRG&LANG`), caller context preserved; no diagnostic. Asserts "does not produce a
false static relationship."

**Reuses/migrates:** extends the literal-extraction path from Tasks 2/6/7.

**DoD:** test asserts the placeholder literal yields a dynamic edge (not static), the clean literal stays
static, no false edge to the raw text, no diagnostic; `gofmt`/`go vet` clean.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Tasks 2, 7.

---

### Task 9 — Wire extraction into `Analyze` (integration; all stories, NFR-6)

**Behavior:** In `analyzer.go:Analyze`, after parsing to `*Program`, call the `calls.go` extractor and
append its edges to `result.Edges` (preserving source order; appending after any future extractors).
Verify the full `FileAnalysis` contract: `Edges` populated, `Diagnostics` unchanged (parse-error channel
untouched), `AST` still set. Confirm graceful degradation: extraction over a malformed/partial AST never
panics and still returns whatever edges were extractable (M-6/NFR-6 — no silent gaps, partial output
retained).

**Fixtures:** reuse a mixed fixture `testdata/calls/09-mixed.NSP` exercising CALLNAT (static+dynamic),
PERFORM (inline+external), INCLUDE, FETCH, RUN, and a placeholder literal in one file; plus a malformed
fixture (reuse the partial-parse pattern from `testdata/parser/04-parser-parse-errors.nsp` style) to
prove extraction survives parse errors.

**Expected result:** `Analyze` returns a `FileAnalysis` whose `Edges` contains the full expected set
across all kinds in source order; `Diagnostics` still carries only parser syntax diagnostics (no edge
shows up as a diagnostic); for the malformed input, valid edges are still extracted while bad statements
appear as diagnostics — the two channels stay separate (M-6).

**Reuses/migrates:** wires Tasks 2–8 into the existing `Analyze` pipeline. The seam test
(`seam_test.go:TestSeam_AnalyzerUsableThroughInterface`) already exercises `Analyze("test.NSP",
"CALLNAT 'MYPROG'")` — it will now observe a populated `Edges` slice; update/extend that expectation if
needed (still through the `model` contract only).

**DoD:** end-to-end test through `Analyze`; channel separation asserted (edges vs diagnostics);
extraction over malformed AST does not panic and retains valid edges; seam test green; deterministic
ordering; `gofmt`/`go vet` clean; `-race` on any test that exercises the index round-trip.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Depends on:** Tasks 2–8.

---

## Traceability

| Acceptance criterion | FR/M | Task(s) |
|---|---|---|
| CALLNAT literal → static call relationship | FR-10, M-3 | 2 |
| Relationship records call site + target name | FR-10 | 1, 2 |
| Fixture suite, all static calls, zero false edges | M-3 | 2, 9 |
| CALLNAT variable → explicit dynamic/unresolved (not error, not dropped) | FR-11, FR-17, M-6 | 3 |
| Dynamic relationship preserves caller object/site/variable | FR-11 | 3 |
| Dynamic visibly distinct from static | FR-11 | 3 |
| PERFORM targets extracted with caller context | FR-12 | 4 |
| Inline subroutines identified for inline-before-external | FR-12, M-4 | 5 |
| Fixture with inline + same-named external marks both | M-4 | 5 |
| Removing inline def → resolves externally (resolution feature) | M-4 | 5 (extraction marking only) |
| INCLUDE → dependency relationship to copycode | FR-13 | 6 |
| Copycode treated as literal names, always resolvable ref | FR-13 | 6, 8 |
| Changing copycode re-evaluates includers (incremental) | FR-13 | 6 (already satisfied by `index.Invalidate`) |
| FETCH/RUN literal → navigation relationship | FR-14 | 7 |
| FETCH/RUN variable → dynamic/unresolved with caller context | FR-15 | 7 |
| Statement naming a target library marked for library resolution | FR-16-adjacent | 7 (pending syntax — see Open Questions) |
| Placeholder literal not resolved to raw-text object | FR-18 | 8 |
| Placeholder target represented as dynamic, caller context preserved | FR-18 | 8 |
| Placeholder fixture produces no false static relationship | FR-18 | 8 |
| Parse errors vs unresolved refs reported via different channels | FR-17, NFR-6, M-6 | 3, 7, 9 |

---

## Reviews required (for `/review-feature`)

- **review-robustness** — the parser AST change (Task 1) and the extractor parse/walk new input shapes;
  malformed-AST and placeholder edge cases must not panic and must not drop valid edges.
- **review-seam** — confirm the AST contract change (Task 1) stays behind the Analyzer seam and that
  `internal/model` is unchanged (or, if Task 7 adds a dynamic-navigation constant, that it is a pure
  additive `model` constant with no parser internals leaking in). Run `seam_test.go`.
- **review-docs** — this feature changes analyzer capability (calls/dependency extraction now emits
  edges); the `CLAUDE.md` "Project state" note (currently says `calls.go` is a stub) and `README.md`
  "Parser-based extraction" section must be synced at `/finalize-feature`.
- **review-performance** — extraction runs inside `Analyze`, which is on the cold-index hot path
  (`workspace.Build`); confirm the extractor is a single linear AST walk with no quadratic
  inline-subroutine lookups (use a set, per Task 5).

(No concurrency review needed — the extractor is pure/stateless over a single AST; the index/watcher
that consume `Edges` are unchanged.)

---

## Open questions

1. **Inline-subroutine marking representation (Task 5).** How should "this PERFORM resolves to an
   in-file inline subroutine" be carried on `model.EdgeEntry` without adding parser internals to
   `internal/model`? Options: (a) set `Target` to the inline definition's `Range` (intra-file resolved)
   and leave it zero/unresolved otherwise; (b) add a boolean/enum field to `EdgeEntry`. Option (a) needs
   no model change and is preferred — confirm it is sufficient for the resolution feature's
   inline-before-external logic.
2. **Dynamic FETCH/RUN edge kind (Task 7).** Reuse `EdgeCallsDynamic` for dynamic navigation targets, or
   add a new `model` constant (e.g. `NAVIGATES_TO_DYNAMIC`)? Plan's Story 6 open question (dynamic vs.
   resolved-with-wildcard) overlaps. Reusing `EdgeCallsDynamic` avoids a model change but conflates call
   and navigation semantics; a new constant is cleaner but is the feature's only `model` touch. **Needs
   a decision before Task 7.**
3. **Library-qualified FETCH/RUN syntax (Task 7, criterion 3).** What is the exact Natural surface form
   for "explicitly names a target library" on FETCH/RUN, and does the corpus contain it? If unconfirmed,
   scope library marking out of this feature and defer to the resolution feature (FR-16).
4. **Placeholder relationship type (Task 8).** Plan open question: dynamic vs. resolved-with-wildcard for
   `&`-bearing literals. Default chosen here is **dynamic**; confirm.
5. **User-defined function calls (plan open question).** Whether `.NS7` function calls warrant a
   distinct edge type vs. ordinary module calls. Out of scope for this feature unless the corpus shows a
   distinct call syntax; flagged for the resolution/hover features.
6. **Target-name length limits (plan open question).** Whether Natural's name-length limits should
   influence static-vs-dynamic classification. Treated as **not** influencing classification here
   (length is a resolution/validation concern, not extraction); confirm.