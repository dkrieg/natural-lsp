# Tasks: Semantic-Tokens Classifier Performance (feature 35)

**Plan:** [`plan.md`](./plan.md)
**PRD requirements:** NFR-3 (interactive per-request latency); refines feature 29 (FR-56).
**Scope (approved):** behavior-preserving performance fix, entirely behind the Analyzer seam in
`internal/analysis/natural/semantictokens.go`. **No `internal/model` change, no cache-format bump
(stays `0.9.0`), no LSP capability/legend change.** The seam method
`Analyzer.SemanticTokens(path string, content []byte) []model.SemanticToken`
(`internal/analysis/natural/analyzer.go:158`) signature is **unchanged**.

Two defects, both frozen against a golden regression test written **first**:
1. O(n²) merge/dedup in `semanticTokensPhaseB` → O(n log n) (Story 1).
2. ~4× redundant re-parse / ~7× re-lex → parse-once, thread the shared `*Program` + `[]Token` into
   the phase helpers (Story 2).
Then a `//go:build bench` benchmark records the win (Story 3).

---

## Current-state findings & impact

Read against `internal/analysis/natural/semantictokens.go` as it actually is (whole file, 929 lines):

### F1 — The five phases each independently lex/parse the same content
`semanticTokensPhaseB` (L719–834) calls five helpers, each of which re-derives the token stream / AST
from the same `content` string:

| Phase helper | `NewLexer` calls | `NewParser`+`Parse` | extraction |
|---|---|---|---|
| `semanticTokensPhaseA` (L19) | 1 (L22) | 0 | none |
| `semanticTokensPhaseBIdentifiers` (L251) | **2** (L255 for the parse, L278 to walk the stream) | 1 (L256–257) | `extractDefinitions` (L264), `buildVarLookup` (L274) |
| `semanticTokensPhaseBCalls` (L844) | 1 (L846) | 1 (L847–848) | `extractEdges` (L857); reads `ast.Performs`/`ast.Subroutines` |
| `semanticTokensPhaseBDDMView` (L339) | 1 (L343) | 1 (L344–345) | `extractDefinitions` (L351), `extractDataAccess` (L352) |
| `semanticTokensPhaseBSystemVarsAndWrites` (L567) | **2** (L571 for the parse, L590 to build `allLexerTokens`) | 1 (L572–573) | `extractDefinitions` (L579), `buildVarLookup` (L584) |

**Totals per `SemanticTokens` request: 7 `NewLexer`, 4 `NewParser`+`Parse`, 3 `extractDefinitions`,
1 `extractDataAccess`, 1 `extractEdges`, 2 `buildVarLookup`.** `Parse()` internally lexes, so raw lexes
≈ 7 explicit + 4 in-parser = 11. Confirmed against `parser.go:31` (`Parse() (*Program, error)`),
`lexer.go:61` (`NewLexer(string) *Lexer`), `parser.go:21` (`NewParser(*Lexer)`).

**Impact:** Story 2 threads a single lex (`[]Token`) and a single parse (`*Program`) from
`semanticTokensPhaseB` into each helper. The token-stream phases (A, identifiers-walk, sysvars-walk)
consume the shared `[]Token`; the AST phases consume the shared `*Program`. Because lex/parse are
deterministic pure functions of `content`, a single shared token slice / AST is byte-for-byte what each
phase produces independently — the merge output is unchanged.

### F2 — The O(n²) reconstruction loop is the headline defect (L792–831)
The `tokenMap` (L762–790) already deduplicates by `(line,col)` start position with the precedence +
modifier-merge rules — that part is O(n). The **reconstruction loop** (L795–831) is the quadratic one:
- For each of the *n* sorted tokens it does a **linear `alreadyAdded` scan** over the growing
  `dedupedTokens` slice (L800–807) — O(n²).
- For every operator token it does a **second linear scan over all `allTokens`** (L815–823) to decide
  system-var suppression — O(n²).

**Impact:** Story 1 replaces the `alreadyAdded` scan with a `seen map[struct{line,col int}]bool`, and the
operator-suppression rescan with a **precomputed set** of the start positions of every
`variable`+`readonly` token in `allTokens` (built in one O(n) pass), looked up at
`(selectedTok.line, selectedTok.End.Column+1)`. Both are exact O(1) restatements of the existing
predicates — see F3.

### F3 — The operator-suppression predicate may be effectively dead, but must NOT be "simplified away"
The system-var token (built in `…SystemVarsAndWrites`, L619–626) starts at the **same column** as the
`*` operator (`startPos.Column = curTok.Column`, the `*`), so in `tokenMap` the `variable` (precedence 2)
already displaces the `operator` (precedence 1) **at that start position** — the operator never survives
to reach the L813 suppression branch via that collision. Whether the L813–823 rescan is therefore dead
code or guards some other arrangement is **out of scope to decide**: T2 must reproduce it exactly (as the
O(1) precomputed-set lookup), and the golden test (T1) is what proves equivalence. See OQ-2 below — do
not delete or "clean up" the suppression on the assumption it is dead.

### F4 — Merge ordering is load-bearing and must be preserved
Concatenation order in `semanticTokensPhaseB` is A → identifiers → calls → DDM → sysvars (L729–732);
the sort is `sort.SliceStable` keyed **only** on start `(line, col)` (L735–740), so equal-start tokens
keep concatenation order. That order drives the modifier-merge tie-break (same precedence ⇒ keep first,
OR modifiers — the FINDING-A parameter-write fix at L775–787). **Story 2 must not reorder the five
helper calls nor change any helper's internal emission order.** Frozen by the golden test.

### F5 — Existing safety nets to keep green
- Unit tests: `internal/analysis/natural/semantictokens_test.go` — 9 tests covering every token class,
  every modifier, `*DATX`, the comment-vs-sysvar distinction, grouped/nested fields (FINDING B), the
  parameter-write merge (FINDING A), the numeric-range fix (FINDING C), and unparseable fallback.
- Fuzz: `FuzzSemanticTokens` (`internal/analysis/natural/fuzz_test.go:647`) and `FuzzEncodeSemanticTokens`
  (`internal/server/semantic_tokens_test.go:583`) — never-panic / non-nil / `len%5==0`.
- Fixtures: `internal/analysis/natural/testdata/semantictokens/` — `lexical.NSP`, `variables.NSP`,
  `calls.NSP`, `ddm.NSP`, `sysvar.NSP`, `grouped.NSP`, `paramwrite.NSP` (7 files).
- Bench precedent: `BenchmarkSemanticTokensFull` already exists
  (`internal/server/provider_bench_test.go:247`, one size = 5000 lines, full lex+classify+encode) with
  `generateLargeSyntheticProgram(lines)` (L269) and the `benchTiers()`/`BENCH_CORPUS_OBJECTS` convention.

### Reconciliation of acceptance criteria
- **Story 1 AC1** (O(n log n) dedup) → **T2** (new). **Story 1 AC2** (byte-identical) → guaranteed by **T1**.
  **Story 1 AC3** (fuzz green) → verified in T2/T3.
- **Story 2 AC1** (parse once, thread AST/tokens) → **T3** (new, extends the five existing helpers per OQ-2).
  **Story 2 AC2** (byte-identical) → guaranteed by **T1**.
- **Story 3 AC1/AC2** (bench ≥2 sizes, record Results) → **T4** (extends the existing bench).
- No criterion is already satisfied and none is skipped; no shared-contract change (seam signature and
  `model.SemanticToken` are untouched), so no consumer-migration tasks are needed.

---

## Tasks

> Sequencing is critical: **T1 (the golden safety net) lands first and passes on the unchanged code**, so
> every later task is proven behavior-preserving against it. T2 (dedup) before T3 (parse-once) — each is an
> independent, golden-guarded step; keeping them separate makes each diff auditable. T4 (bench) can be
> authored any time after T1 but its "before" figure is captured against the pre-T2 code (see its DoD).

### T1 — Golden characterization test of current `SemanticTokens` output (SAFETY NET, no production change)
**Pins:** Story 1 AC2 / Story 2 AC2 — the byte-identical contract. This test **must pass on today's
unchanged code** (it captures current behavior; it is not red).

**Behavior/AC:** For a fixture corpus spanning every token class (keyword/comment/string/number/operator),
every semantic type (variable/parameter/function/type/property), every modifier
(declaration/definition/readonly/modification/defaultLibrary), and the `*DATX` operator-suppression case,
`SemanticTokens(path, content)` produces exactly the currently-emitted `[]SemanticToken` (Range, Type,
Modifiers, order).

**Expected result / mechanism (OQ-1 — checked-in golden recommended):** A table-driven white-box test in
`internal/analysis/natural/semantictokens_golden_test.go` that, for each fixture, calls
`New(nil).SemanticTokens(name, content)` and compares the result against a **checked-in golden file** —
one JSON per fixture under `testdata/semantictokens/golden/<name>.json` (or a single golden table), a
diff-friendly serialization of `[]model.SemanticToken` (Range line/col, Type string, Modifiers as the
raw bitset int **and** decoded modifier names for readability). Provide a `-update` flag that regenerates
the goldens from current output, then commit them. A future regression is then a visible golden diff.

**Fixtures:** reuse all 7 existing files (`lexical/variables/calls/ddm/sysvar/grouped/paramwrite.NSP`)
**plus** three inline cases so coverage is complete and self-contained:
(a) the numeric-range case `"COMPUTE #A = 5-3\n"` (FINDING C),
(b) the unparseable-fallback input from `TestSemanticTokens_PhaseA_UnparseableFallback`,
(c) the adversarial `"* comment with *DATX ...\nMOVE *DATX TO #X\n"` (comment-vs-sysvar + suppression).
No new fixture files are required beyond the golden outputs.

**How byte-identical is proven:** the golden is generated from the current implementation; the test fails
if any later task changes any token's Range/Type/Modifiers/position or the ordering. Coverage of every
class/modifier is asserted by a meta-check in the test (fail if the union of Types/Modifiers across the
corpus is missing any legend entry that the fixtures are meant to exercise).

**TDD agents:** `tdd-red` writes the test and the golden-capture harness; because it must pass on
unchanged code, run `tdd-green` only to generate/commit the goldens (no production edit) — this task
produces **no change to `semantictokens.go`**.

**DoD:**
- [ ] `semantictokens_golden_test.go` + `testdata/semantictokens/golden/*.json` committed; test **green**
      on the unmodified `semantictokens.go`.
- [ ] Corpus covers every token type and every modifier (meta-assertion) and the `*DATX` suppression case.
- [ ] `-update` regeneration path documented in a test comment; goldens are deterministic across runs.
- [ ] `just verify` green (golden test runs under the normal gate).

### T2 — O(n²) → O(n log n) dedup in `semanticTokensPhaseB` (Story 1)
**Pins:** Story 1 AC1. **Golden (T1) stays green — this is the equivalence proof.**

**Behavior/AC:** The reconstruction loop (L792–831) no longer scans the output slice or rescans
`allTokens` per token. Replace:
- the `alreadyAdded` linear scan (L800–807) with a `seen map[struct{ line, col int }]bool` — skip a key
  already emitted, mark on first emit;
- the operator-suppression rescan (L813–823) with a **precomputed set**
  `readonlyVarStarts map[struct{ line, col int }]bool`, populated in one pass over `allTokens` with every
  `Type == variable && Modifiers&readonly != 0` token's `Range.Start` (line, col); the suppression check
  becomes an O(1) membership test at `{selectedTok.End.Line/Column' }` keyed exactly as the old predicate
  (same line, `Start.Column == selectedTok.End.Column+1`, readonly variable).
The `tokenMap` build (L762–790), the precedence map, and the modifier-merge tie-break are **unchanged**.
Net complexity is dominated by the existing `sort.SliceStable` (O(n log n)).

**Expected result:** identical `[]SemanticToken` for every T1 corpus entry; no change to Range/Type/
Modifiers/order. Do **not** delete or alter the suppression semantics (see F3/OQ-2), only its data
structure.

**Fixtures:** none new — proven against T1's goldens.

**How byte-identical is proven:** T1 golden test green; the 9 existing unit tests green; `FuzzSemanticTokens`
green (never-panic, document order).

**TDD agents:** `tdd-refactor` (behavior-preserving rewrite guarded by T1) → re-run `tdd-green` to confirm
the full suite + goldens. Add a `tdd-red` micro-assertion only if a targeted equal-start / operator-before-
sysvar unit case is wanted beyond the golden (optional).

**DoD:**
- [ ] `alreadyAdded` slice scan and the operator rescan removed; `seen` + `readonlyVarStarts` sets in place.
- [ ] T1 goldens unchanged and green; all 9 `semantictokens_test.go` tests green.
- [ ] `FuzzSemanticTokens` / `FuzzEncodeSemanticTokens` green (`go test -run=FuzzSemanticTokens` smoke +
      the fuzz corpus).
- [ ] `just verify` green.

### T3 — Parse once; thread `*Program` + `[]Token` into the phase helpers (Story 2)
**Pins:** Story 2 AC1. **Golden (T1) stays green.** Minimal change per OQ-2: keep the five phase
functions; change only their **inputs** — no single-pass rewrite.

**Behavior/AC:** `semanticTokensPhaseB` lexes the content **once** into a shared `[]Token` and parses it
**once** into a shared `*Program`, then passes both into the helpers. Each helper's signature changes from
`(path, content string)` to accept the pre-computed `ast *Program` and/or `lexTokens []Token` (plus
`content`/`contentBytes` where `computeTokenRange` still needs the raw bytes):
- `semanticTokensPhaseA` consumes the shared `[]Token` instead of `NewLexer` at L22.
- `semanticTokensPhaseBIdentifiers` consumes the shared `*Program` (drop L255–257 parse) and the shared
  `[]Token` (drop the L278 re-lex).
- `semanticTokensPhaseBCalls` consumes the shared `*Program` (drop L846–848).
- `semanticTokensPhaseBDDMView` consumes the shared `*Program` (drop L343–345).
- `semanticTokensPhaseBSystemVarsAndWrites` consumes the shared `*Program` (drop L571–573) and the shared
  `[]Token` in place of the L590 re-lex used to build `allLexerTokens`.
The shared `[]Token` is produced by a single `NewLexer(content)` full drain (byte-identical to the
independent drains today); the shared `*Program` from a single `NewParser(NewLexer(content)).Parse()`.
`extractDefinitions`/`extractDataAccess`/`extractEdges`/`buildVarLookup` may still be called inside each
helper from the shared AST (deterministic, cheap) — sharing those too is an **optional** follow-on and not
required to satisfy the AC; keep the diff minimal and auditable. **Do not reorder the helper calls or any
helper's internal emission order (F4).** The `Analyzer.SemanticTokens` seam signature is untouched
(`analyzer.go:158` still calls `semanticTokensPhaseB(path, string(content))`).

**Expected result:** after the change, per request: **1 explicit lex** (shared `[]Token`) + **1 parse**
(shared `*Program`, one internal lex), down from 7 explicit lexes + 4 parses. Output identical to T1
goldens.

**Parse-once assertion (Story 2 AC1):** add a white-box test that counts lex/parse invocations for one
`SemanticTokens` call and asserts parse count == 1 and explicit-lex count == 1. Recommended mechanism: a
package-private indirection the test can wrap — e.g. small unexported vars
`lexAllForSemanticTokens func(string) []Token` and `parseForSemanticTokens func(string) *Program` that
`semanticTokensPhaseB` calls and the test replaces with counting wrappers (restored via `t.Cleanup`).
This keeps the counter out of production hot-path logic and race-free (the test is serial). If a counting
seam is undesirable, the alternative acceptable proof is a compile-time/structural assertion that no
phase helper takes `content` as its sole source (i.e. every helper now requires the pre-parsed inputs, so
re-parsing is structurally impossible) — but the counting seam is preferred as it is a direct behavioral
assertion.

**Fixtures:** none new — proven against T1's goldens.

**How byte-identical is proven:** T1 goldens green; 9 unit tests green; both fuzz targets green; the
parse-once counter test green.

**TDD agents:** `tdd-red` writes the parse-once counter test (red: today it counts 4 parses / 7 lexes) →
`tdd-green` performs the parse-once threading → `tdd-refactor` tidies helper signatures. Re-run the full
suite + goldens.

**DoD:**
- [ ] Single lex + single parse in `semanticTokensPhaseB`; all five helpers take `*Program`/`[]Token`.
- [ ] Parse-once counter test green (parse == 1, explicit lex == 1); no helper re-lexes/re-parses.
- [ ] Helper call order and internal emission order unchanged; T1 goldens unchanged and green.
- [ ] Seam signature `SemanticTokens(path, []byte)` unchanged; `FuzzSemanticTokens`/`FuzzEncodeSemanticTokens`
      green.
- [ ] `just verify` green.

### T4 — `BenchmarkSemanticTokens` at ≥2 sizes; record Results (Story 3, `//go:build bench`, OFF `just verify`)
**Pins:** Story 3 AC1/AC2 — measure-and-record, no CI gate (matches feature 22).

**Behavior/AC:** A `//go:build bench` benchmark exercises `Analyzer.SemanticTokens` over a large
synthetic Natural file at **≥2 sizes**, so the before→after latency and the O(n²)→O(n log n) scaling are
recorded in the plan's `## Results` section. It runs via `just bench` and is excluded from `just verify`.

**Expected result / mechanism:** Extend the existing `BenchmarkSemanticTokensFull`
(`internal/server/provider_bench_test.go:247`) — either generalize it to sub-benchmarks over ≥2 line
counts (e.g. `2000`, `5000`, and a `BENCH_CORPUS_OBJECTS`/lines-scaled custom tier) using the existing
`generateLargeSyntheticProgram(lines)` (L269), or add a sibling `BenchmarkSemanticTokens` measuring the
**classifier alone** (`az.SemanticTokens(...)`, excluding `encodeSemanticTokens`, since the encoder is
unchanged) so the linearithmic scaling of the classifier is visible. A single large file is the correct
shape (the hot path is per-open-buffer), so `generateLargeSyntheticProgram` is preferred over
`corpusgen` (which emits a multi-file workspace); reuse `corpusgen` only if a single large object is
generated from it. Report `bytes` / lines and `allocs/op`.

**How before→after is recorded:** run the bench on the branch point (pre-T2 code) to capture the "before"
figures, and again at the T3 tip for "after"; paste both into `plan.md`'s `## Results` with the machine,
Go version, and date. The O(n²)→O(n log n) claim is evidenced by the ratio of ns/op across the two (or
more) sizes shrinking from ~quadratic to ~linearithmic.

**Fixtures:** none checked in — the synthetic program is generated in-bench. If a very large fixture is
ever wanted it would live under `testdata/`, but this task does not require one.

**TDD agents:** `tdd-green` (a benchmark, not a red/green behavior test) — author the bench, run it, and
record Results. No golden/unit assertion (benchmarks are measure-and-record).

**DoD:**
- [ ] `BenchmarkSemanticTokens` (or generalized `BenchmarkSemanticTokensFull`) runs at ≥2 sizes under
      `just bench`; **not** part of `just verify`.
- [ ] `## Results` in `plan.md` records before/after ns/op + allocs/op at each size, the scaling
      observation, machine/Go-version/date.
- [ ] No production behavior change; T1 goldens + fuzz still green.

---

## Reviews required (for `/review-feature`)
- **code review** — the T2 dedup rewrite is exactly equivalent (seen-set + precomputed readonly-var-start
  set restate the old predicates; F3 suppression preserved, not deleted); the T3 parse-once threading
  changes only helper inputs, preserves helper call order and internal emission order (F4), and does not
  touch the Analyzer seam signature.
- **test review** — T1 golden corpus covers every token class + every modifier + the `*DATX` suppression
  case; goldens are checked in and diff-friendly; the parse-once counter test genuinely fails on the
  pre-T3 code; `FuzzSemanticTokens`/`FuzzEncodeSemanticTokens` still pass.
- **perf review** — the T4 bench demonstrates the O(n²)→O(n log n) improvement across sizes; Results are
  recorded; bench is off `just verify`.
- **review-docs** — the CLAUDE.md "Project state" feature-29 note is updated to record that the O(n²)
  classifier limit (called out in the feature-29 memory note) is fixed by feature 35; the feature dir is
  consistent. No README capability/legend change (none occurred).

## Open questions
- **OQ-1 (planner recommendation, confirm in review):** golden capture mechanism — **checked-in JSON
  golden per fixture** with a `-update` regeneration flag, so a regression is a visible diff. Adopted in
  T1.
- **OQ-2 (planner recommendation, confirm in review):** phase-signature shape — **minimal change: share
  the parse, keep the five phase functions, change only their inputs.** A single-pass rewrite is **not**
  required to remove the O(n²) and is deliberately out of scope (larger, riskier, harder to prove
  byte-identical). Adopted in T3.
- **OQ-3 (genuinely new — surfaced from the code, needs a reviewer note, not a blocker):** the
  operator-suppression rescan (L813–823) appears **effectively dead** because the system-var token shares
  the `*` operator's start column and already wins in `tokenMap` at that position (F3). T2 preserves it
  verbatim as an O(1) lookup regardless. **Recommendation:** do NOT remove it in this feature (the golden
  test cannot prove the absence of an input that exercises it); if a reviewer wants it gone, that is a
  separate, explicitly-scoped change with its own fixture demonstrating the suppression path is
  unreachable. Flagging so it is a conscious decision, not an accidental "cleanup."
- **OQ-4 (optional scope, defer unless trivial):** extraction sharing — `extractDefinitions` is called 3×
  and `buildVarLookup` 2× even after T3. Deduplicating them (extract once in `semanticTokensPhaseB`, pass
  the results down) is a further constant-factor win but is **not** needed for NFR-3's headline fix and
  widens the diff. Recommend deferring unless the T4 bench shows extraction dominates after T2+T3.
