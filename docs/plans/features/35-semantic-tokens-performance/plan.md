# Feature: Semantic-Tokens Classifier Performance

**Status:** Planned
**PRD requirements:** NFR-3 (interactive per-request latency); refines feature 29 (FR-56, semantic tokens)
**Priority / phase:** P1 (a shipped interactive provider has a latency cliff on large files)
**Depends on:** [29](../29-semantic-tokens/plan.md) (the classifier), [22](../22-performance-and-scale-verification/plan.md) (bench harness + `corpusgen`)

## Summary

`Analyzer.SemanticTokens` (feature 29) is on the interactive hot path — the LSP server calls it for
`textDocument/semanticTokens/full` and `.../range` on the **open buffer**, i.e. on file open and, for
clients that request full tokens per change, on edit. Its classifier `semanticTokensPhaseB`
(`internal/analysis/natural/semantictokens.go`) has two stacked performance defects:

1. **O(n²) merge/dedup (the headline defect).** After building a `(line,col) → token` precedence map,
   the reconstruction loop (≈L795–831) does, for each of the *n* merged tokens, a **linear `alreadyAdded`
   scan** over the growing output slice, and for every operator token a **second linear scan over all
   tokens** to decide system-var suppression. Both are O(n²). On a ~2,000-line program (~30k tokens) that
   is ~10⁹ operations per request — hundreds of milliseconds of highlight lag. The `tokenMap` already
   deduplicates by position; the quadratic scans are pure redundancy.

2. **~4× redundant re-parse (constant-factor).** The five phase functions (`…PhaseA`,
   `…PhaseBIdentifiers`, `…PhaseBCalls`, `…PhaseBDDMView`, `…PhaseBSystemVarsAndWrites`) **each
   independently** `NewLexer` + `NewParser` + `Parse()` (and two of them lex twice), so the file is fully
   parsed ~4× and lexed ~7× per request. Wasteful; the parse should happen once and be shared.

**This is a behavior-preserving performance fix.** The emitted token stream — every token's range, type,
modifiers, precedence/dedup outcome, and order — must stay **byte-identical** to today's output. It is
entirely **behind the Analyzer seam** (`internal/analysis/natural/semantictokens.go`): **no
`internal/model` change, no cache-format bump** (semantic tokens are non-persisted), and **no LSP
capability/legend change** (the locked `TestInitialize` allow-list and the semantic-tokens legend are
untouched).

## User stories

### Story 1 — Kill the O(n²) dedup (NFR-3)
**As a** developer editing a large Natural program, **I want** semantic-token highlighting to stay
responsive **so that** opening/editing a big file does not freeze the editor.

**Acceptance criteria:**
- [x] The `semanticTokensPhaseB` merge/dedup is **O(n log n)** (dominated by the existing sort): the
      `alreadyAdded` linear scan is replaced by a `seen` set keyed on `(line,col)`, and operator/system-var
      suppression uses a **precomputed set** of readonly-`variable` start positions (O(1) lookup on
      `(line, End.Column+1)`), not a rescan of all tokens.
- [x] Output is **byte-identical** to the pre-change classifier for a corpus of fixtures spanning every
      token class (keyword/comment/string/number/operator, variable/parameter, call/DDM/view/system-var,
      declaration/modification modifiers, and the `*DATX` operator-suppression case) — proven by a golden
      regression test comparing against captured current output.
- [x] `FuzzSemanticTokens` / `FuzzEncodeSemanticTokens` continue to pass (never-panic, `len%5==0`
      invariants — FR-43).

### Story 2 — Parse once, share the AST/token stream (NFR-3)
**As a** developer, **I want** the classifier to parse each file **once per request** **so that** semantic
tokens don't pay a ~4× parse tax.

**Acceptance criteria:**
- [x] The content is lexed/parsed **once** in `semanticTokensPhaseB`; the AST and the lexer token stream
      are threaded into the phase helpers (signature change is internal to the package — the Analyzer seam
      method `SemanticTokens(path, content)` is unchanged). No phase re-lexes/re-parses the same content.
- [x] Output remains byte-identical (same golden test as Story 1).

### Story 3 — Prove it (NFR-3, measure-and-record)
**As a** maintainer, **I want** the win quantified and guarded **so that** the latency cliff cannot silently
return.

**Acceptance criteria:**
- [x] A `//go:build bench` benchmark (`BenchmarkSemanticTokens`, run by `just bench`, **off** `just
      verify`) exercises `SemanticTokens` over a large synthetic file (reuse feature 22's `corpusgen`/a
      generated large object), at ≥2 sizes, so before/after latency and the linearithmic scaling are
      recorded in the plan's `## Results`.
- [x] The before→after improvement (and the O(n²)→O(n log n) scaling) is recorded; no absolute CI gate
      (measure-and-record, matching feature 22).

## Out of scope / deferred
- **Any change to token classification, precedence, the legend, or the encoding** — this is a pure
  performance/refactor; behavior is frozen by the golden test.
- **`semanticTokens/full/delta`** — still deferred (feature 29 decision), unaffected here.
- **Persisting semantic tokens / caching per file** — they remain computed on demand.
- Broader analyzer-wide parse-sharing (other providers) — out of scope; this feature touches only the
  semantic-tokens path.

## Open questions
- **OQ-1 — golden capture mechanism.** Capture current output as a checked-in golden (per fixture) vs.
  assert structural equality against a snapshot built in-test. Recommend a checked-in golden (table of
  fixtures → expected `[]SemanticToken`) so a future regression is a visible diff. Confirm during planning.
- **OQ-2 — phase signature shape.** Thread a single parsed `*Program` + `[]Token` into each phase, or fold
  the phases into one pass over the shared stream. Recommend the minimal change (share the parse; keep the
  five phase functions, changing only their inputs) to keep the byte-identical guarantee auditable;
  a deeper single-pass rewrite is a larger, riskier change and is **not** required to remove the O(n²).

## Notes
- Behind the Analyzer seam; **no `internal/model`/cache/capability change**. The `SemanticTokens` seam
  method signature is unchanged. json/v2 marshaling and the LSP encoding path (feature 29
  `semantic_tokens.go`) are untouched. Fuzz targets continue to guard never-panic (FR-43).

## Results

**Provenance.** Apple M4 Max (16 logical CPUs), `go version go1.26.4 darwin/arm64`, recorded
2026-08-04. Benchmark: `BenchmarkSemanticTokens` (`internal/analysis/natural/semantictokens_bench_test.go`,
`//go:build bench`, **off** `just verify`; reachable via `just bench`). It measures the classifier
alone — `Analyzer.SemanticTokens(path, content)` — over a deterministic, in-bench synthetic Natural
program (`generateLargeSyntheticNatural`) at three sizes. The LSP relative-5-int encoder is deliberately
**excluded** (unchanged by feature 35; covered by `internal/server`'s `BenchmarkSemanticTokensFull`).
Command:

```
go test -tags bench -bench BenchmarkSemanticTokens -benchmem -run='^$' -count=1 ./internal/analysis/natural/
```

"Before" = `main`/HEAD (pre-feature-35: O(n²) reconstruction loop **and** 4× parse / 7× lex both
present), captured by temporarily reverting `semantictokens.go` + `semantictokens_test.go` to HEAD and
running the same bench file (a test file, independent of the production change), then restoring fully
(goldens re-confirmed green). "After" = the T2 (O(n log n) dedup) + T3 (parse-once) tip. The synthetic
program (and therefore `src-bytes` / `tokens`) is identical between runs.

### Before (HEAD, pre-feature-35) vs After (T2 + T3)

| lines | tokens | src-bytes | ns/op (before) | ns/op (after) | ns/op speedup | allocs/op (before) | allocs/op (after) | allocs speedup |
|------:|-------:|----------:|---------------:|--------------:|--------------:|-------------------:|------------------:|---------------:|
|   500 |  2,015 |    16,653 |     20,392,567 |    18,433,042 |         1.11× |             21,042 |             6,280 |          3.35× |
| 2,000 |  6,515 |    51,223 |    208,010,492 |   185,802,847 |         1.12× |             59,218 |            17,153 |          3.45× |
| 8,000 | 24,515 |   190,303 |  2,895,860,583 | 2,600,910,583 |         1.11× |            211,786 |            60,596 |          3.49× |

**What T2 + T3 demonstrably won.**
- **Allocations dropped ~3.4× at every size** (21,042→6,280 / 59,218→17,153 / 211,786→60,596). This is
  the T3 parse-once win: per request the classifier now does **1 lex + 1 parse** instead of 7 lexes + 4
  parses (Story 2 AC1), so the allocation count falls by a flat constant factor across sizes.
- The T2 reconstruction-loop O(n²) (the `alreadyAdded` output-slice scan + the per-operator rescan of
  `allTokens`) is gone by construction — replaced by a `seen` set + a precomputed `readonlyVarStarts`
  set — and the output is **byte-identical** (T1 goldens + the 9 unit tests + both fuzz targets green).

### Scaling after T2+T3 only — the O(n²) was NOT yet gone (the finding that drove T5)

> **Note:** this subsection describes the **intermediate** state after T2+T3 *before* T5. The benchmark
> here is what surfaced that the plan's headline defect (the dedup loop) was not the real bottleneck. T5
> (below) fixes the actual O(n²). Kept as the measured record of why T5 was added.

Reading the ns/op ratio against the **token-count** ratio (tokens, not lines, are the classifier's `n`;
they scale ~linearly with lines here):

| step | token ratio | ns/op ratio (before) | ns/op ratio (after) | (token ratio)² |
|------|------------:|---------------------:|--------------------:|---------------:|
| 500 → 2,000 |       3.23× |               10.20× |              10.08× |         10.45× |
| 2,000 → 8,000 |     3.76× |               13.92× |              14.00× |         14.16× |

**Both before and after, the time ratio ≈ (token ratio)² — i.e. the classifier is still O(n²).** The T2
dedup fix and the T3 parse-once fix together buy only a **flat ~10–12% wall-clock** improvement and a
~3.4× alloc reduction; they do **not** change the scaling class. The NFR-3 latency cliff persists (an
8,000-line file still takes ~2.6 s per `SemanticTokens` request).

**Root cause (CPU profile, 2,000-line tier).** A third O(n²) that the plan (Story 1) never identified
dominates the wall-clock:

```
flat  flat%   cum   cum%   function
3.61s 94.01% 3.72s 96.88% natural.computeTokenRange
```

`computeTokenRange` (`internal/analysis/natural/semantictokens.go:111`) rescans `contentBytes` from
**offset 0** to locate each token's byte offset — O(content) per token, and it is called once per token
in Phase A and again per relevant token in the identifier/system-var phases, so overall it is
O(tokens × bytes) = **O(n²)**. It is untouched by T2 (reconstruction loop) and T3 (parse-once), which is
exactly why before ≈ after in ns/op: neither fix touches the function that owns 94% of the CPU. The T2
reconstruction loop, by contrast, is a negligible fraction of runtime at these sizes — real, but not the
bottleneck the headline defect assumed.

**Recommended follow-up (out of T4/feature-35 scope — surface for a plan amendment or new task).** To
actually remove the O(n²) and meet NFR-3, `computeTokenRange` must become O(1) per token. Two clean
options, both behind the seam and byte-identical-preserving (golden-guarded):
1. Precompute a **line-start byte-offset table** once per request (single O(content) pass), then resolve
   `(line, col) → byte offset` in O(1); the end-scan for strings/comments stays local and bounded.
2. Have the lexer **stamp the source byte offset (and end offset) on each `Token`**, eliminating the
   positional rescan entirely.
Either reduces the classifier to O(n log n) (dominated by the existing `sort.SliceStable`) and would show
the ~2×-per-2× scaling this feature set out to prove. This is recorded here as a measured, profiled gap
so it is a conscious decision rather than a silent miss.

### After T5 (`computeTokenRange` fix) — O(n²) eliminated

T5 implemented option 1: a **line-start byte-offset table** built once per request (single O(content)
pass, `buildLineStarts`), threaded into `computeTokenRange` so each token's `(line, col) → byte offset`
resolution scans only **within its own line** instead of from byte 0. Output is byte-identical (T1 golden
+ `TestSemanticTokens_ParseOnce` + both fuzz targets green; goldens untouched).

| lines | tokens | ns/op (before, HEAD) | ns/op (after T5) | speedup |
|------:|-------:|---------------------:|-----------------:|--------:|
|   500 |  2,015 |           18,492,264 |          866,907 |    ~21× |
| 2,000 |  6,515 |          185,965,000 |        2,876,188 |    ~65× |
| 8,000 | 24,515 |    2,602,821,125 (2.6 s) |   11,663,160 (11.7 ms) | **~223×** |

**Scaling proof (token-ratio vs ns/op-ratio):**

| step | token ratio | ns/op ratio (after T5) | (token ratio)² |
|------|------------:|-----------------------:|---------------:|
| 2,015 → 6,515  | 3.23× | 3.32× | 10.45× |
| 6,515 → 24,515 | 3.76× | 4.06× | 14.16× |

The post-T5 ns/op ratio now **tracks the token ratio (linear/linearithmic)**, not its square — the O(n²)
is gone. The 8,000-line request dropped from ~2.6 s to ~11.7 ms. Combined with T3 (1 lex + 1 parse instead
of 7/4) and T2 (O(n log n) dedup), the classifier now meets NFR-3 on large files.
