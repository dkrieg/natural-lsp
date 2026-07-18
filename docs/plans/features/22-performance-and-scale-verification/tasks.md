# Tasks: Performance & Scale Verification (feature 22)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** NFR-1 (P0, linear cold scaling), NFR-2 (P1, sub-second warm start),
NFR-3 (P1, interactive request latency), NFR-4 (P0, tens-of-thousands without memory exhaustion),
NFR-8 (P1, cache never stale), NFR-9 (P1, deterministic/regression fixtures).
**Depends on:** feature 21 (async indexing + on-disk cache wired into the server via `BuildWithCache`).

---

## Current-state findings & impact

Grounded on `main` after features 19/20/21 merged (NOT the stale 2026-07-14 assessment snapshot —
feature 21 wired the on-disk cache into the server and made the build ctx-cancellable/async).

### The `workspace/symbol` per-query disk-read hot spot — CONFIRMED, and the fix is a pure in-memory switch

`internal/server/workspace_symbols.go:51-109` still does exactly what the assessment flagged, unchanged
by feature 21: inside `idx.ForEach(...)` — which holds the `Index`'s internal `RLock` for the **entire
callback duration** (`internal/workspace/index.go:68-74`) — it calls `os.ReadFile(absPath)` (line 59)
**for every indexed file on every `workspace/symbol` query**. At tens of thousands of objects that is
tens of thousands of disk reads per keystroke-driven symbol search, all under the index read lock.

**The symbol data is already fully in memory.** `Index` is `map[string]model.FileAnalysis`
(`index.go:43-46`) and `FileAnalysis.Structure *model.Symbol` (`model/model.go`) — the hierarchical
symbol tree with names, kinds, `Range`, and `SelectionRange` — is populated at index/analyze time and
persisted in the cache (0.6.0). `ForEach` already hands the callback the full `model.FileAnalysis`
(including `Structure`). So `provideWorkspaceSymbols` re-reads and effectively re-derives from disk data
it already holds in memory.

**Why the disk read exists at all:** the *only* consumer of the file content is
`toProtocolRange(sym.SelectionRange, contentStr, hctx.posEncoding)` (line 79/98). `toProtocolRange`
needs the source line text **only** to convert the model's byte-offset columns into the negotiated
position encoding's code units (`position.go:57-78`): for **UTF-8** the byte offset *is* the column (no
content needed); for **UTF-16** (the LSP default) it must count code units across the relevant line,
which needs that line's bytes. So the fix cannot naively drop content for UTF-16 clients — it must make
the per-line text available *without* a per-query full-workspace disk sweep. **This is the central
design decision of the production fix (OQ-B below).**

**Same pattern in `references.go`.** `provideReferences` (`references.go:148-193`) has the identical
full-workspace `idx.ForEach` + `os.ReadFile(absPath)` sweep (line 151), for the same range-conversion
reason. It is a second instance of the hot spot. `definition.go` and `hover.go` do **not** sweep — they
`idx.Get` a bounded set of specific paths and read only those (acceptable; bounded per query).
`documentSymbol` (`document_symbols.go`) is store-first and reads at most one file. So the
full-workspace disk sweep lives in exactly two providers: `workspace/symbol` and `references`.

### `LookupByName` / `buildNameIndex` complexity — CONFIRMED O(files) per call, not yet a proven bottleneck

- `Index.LookupByName(name, typ, cfg)` (`index.go:110-149`) is **O(all files)** per call: it
  `ForEach`-walks the whole index computing `objectIdentity` for each. Its own doc comment says so
  (lines 103-108). Server callers: `completion.go:464` (DDM resolve) and `hover.go:472` (DDM resolve) —
  **one call per request**, not in a loop. So each request is O(files) once, not O(files²).
- `Index.NamesWithPrefix(prefix, typ, refPath, cfg)` (`index.go:170-232`) calls
  `idx.buildNameIndex(cfg)` (`index.go:247-277`) — a full O(files) pass building a fresh
  `map[name][]Candidate` — **on every completion keystroke**. This is the more likely hot spot (fires
  while typing), rebuilding the entire name index per character.
- The resolver (`resolution.go:545,745`) already does the right thing: it calls `buildNameIndex` **once**
  before its edge loop, so resolution is O(files+edges), not O(files·edges). Only the interactive
  per-query callers (`NamesWithPrefix`, `LookupByName`) pay the repeated O(files) cost.

**Decision posture (OQ-E):** benchmark first, optimize only if the numbers justify it. A cached name
index (built once, invalidated on `Add`/`Invalidate`) is the obvious fix but is speculative until the
NFR-3 benchmark shows `NamesWithPrefix`/`LookupByName` is actually hot at scale. Task T6 measures; T7 is
**conditional** on T6's numbers.

### Cache / warm-start is now a real, benchmarkable path (feature 21)

`workspace.BuildWithCache(ctx, root, cfg, az, logger, cachePath, currentHashes, onProgress)
(*Index, staleCount, totalFiles, error)` (`index.go:424-605`) is wired into the server via
`handlerContext.buildIndex` (`server.go:1372-1383`, `cachePath = root/cfg.Cache.Path`). Warm start loads
the cache and re-analyzes only content-hash-changed files (`index.go:537-590`); `staleCount` reports
files re-analyzed. `currentHashes` may be supplied explicitly (the workspace tests do) to control the
changed-set deterministically — this is exactly the seam the warm-start and freshness benchmarks need.
So NFR-2 warm start and NFR-8 freshness are now measurable end-to-end.

### Zero existing benchmarks

`grep "func Benchmark"` across the repo returns **0**. There is no synthetic-corpus generator (only the
tiny hand-written fixtures under `internal/workspace/testdata/resolution/corpus/`). `just verify` =
`fmt-check + lint-tests + vet + build + test + test-integration`; benchmarks are far too slow for that
gate and must be excluded (OQ-A). `.gitignore` already ignores `.natural-lsp-cache/` and `*.test`; the
generated corpus directory must be added to it (Story 1 AC2).

### Contract impact

- **No `internal/model` change.** `Structure` already carries everything `workspace/symbol` needs.
- **No cache-format bump** (stays 0.6.0) — this feature reads existing shapes.
- **Analyzer seam preserved.** All work is LSP-facing (`internal/server`) or index-internal
  (`internal/workspace`); no parser-internal dependency crosses the seam. The corpus generator emits
  `.NSx` *source*, so it exercises the real analyzer end-to-end.
- **One likely additive `internal/workspace` surface** (OQ-B): to serve `workspace/symbol` without a
  disk sweep, either (i) store per-file content/line-index in the index and expose it via `ForEach`
  (memory cost — tension with NFR-4), or (ii) precompute the protocol-space `SelectionRange` at
  analyze/index time so the provider needs no content. Recommendation and options in OQ-B; T4 is written
  to accept whichever the user picks.

---

## Ordered task list

Tasks are ordered: corpus generator (foundation) → cold/memory benchmarks → warm/freshness → the
production hot-spot fix (regression-first) → the request-latency benchmarks that prove the fix →
conditional name-index optimization → results recording.

Benchmark placement/gating follows the **recommended** OQ-A resolution (a build-tagged `bench` package
+ a `just bench` recipe, small default corpus with an env/flag scale knob). If the user overrides OQ-A,
T1's DoD changes accordingly.

---

### T1 — Deterministic synthetic-corpus generator (Story 1: NFR-9)

**Behavior.** A seeded, deterministic generator emits a parameterized workspace of sanitized `.NSx`
objects across multiple libraries with realistic cross-references (CALLNAT → `.NSN`, PERFORM → `.NSS`,
INCLUDE → `.NSC`, READ/FIND → `.NSD`), plus a `.natural-lsp.toml` with a library map so the steplib
chain and flat-namespace paths are both exercised. Given the same seed and size it produces byte-identical
output.

**Fixtures / generator needs.** This task *is* the generator — no `testdata/` fixture. Emit into a
run-chosen dir (a `b.TempDir()` in benchmarks, or a `--out` dir for manual large runs); the corpus is
**generated, never committed**. Parameters: object count (tiers), library count, cross-ref density, seed.
Object-name scheme must be collision-controlled so resolution has both unique and (deliberately)
ambiguous names to walk.

**Expected result.** `GenerateCorpus(dir, Params{Objects, Libraries, Seed, ...}) error` writes N files
across the libraries with a manifest count matching `Objects`; a unit test asserts (a) determinism
(same seed → identical file set + contents, e.g. by hashing the tree), (b) that a `workspace.Build` over
a small generated corpus indexes exactly the emitted objects and that generated CALLNAT/PERFORM/INCLUDE
targets resolve (sanity that the corpus is realistic, not garbage), (c) generated source parses without
producing *syntax* diagnostics on the happy path.

**What it reuses/migrates.** Emits real Natural source consumed by the existing `analysis/natural`
analyzer and `workspace.Build`; reuses `config` library-map format. New code, no existing generator.

**Placement.** Recommended: `internal/workspace/bench/` (or `internal/bench/`) behind a `//go:build bench`
tag so it is invisible to `just verify` (OQ-A). The correctness unit test in T1 may live in a normally-
built `_test.go` using a **tiny** corpus (few dozen objects) so the generator itself is covered by
`just test`; the *heavy* benchmarks that consume it are the tagged ones.

**DoD.**
- [ ] Generator is deterministic (seeded) — asserted by a hash-of-tree equality test.
- [ ] Emits multi-library corpus with CALLNAT/PERFORM/INCLUDE/DDM cross-refs + a `.natural-lsp.toml`.
- [ ] Small-corpus correctness test: builds + resolves cleanly, no syntax diagnostics on happy path.
- [ ] Corpus output dir added to `.gitignore`; generator writes only under a caller-provided dir.
- [ ] Sanitized, non-proprietary content only.
- [ ] `gofmt`/`go vet` clean; deterministic output; `just verify` unaffected (heavy path is tagged).

**TDD:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** none.

---

### T2 — `just bench` recipe + gating harness (Story 2 AC3; enables all benchmarks)

**Behavior.** A `just bench` recipe runs the benchmark suite (the `bench`-tagged package) with a small
default corpus, and is **excluded from `just verify`**. It supports scaling up via an env var / flag
(e.g. `BENCH_CORPUS_OBJECTS=50000 just bench`) for manual large runs.

**Fixtures / generator needs.** None (drives T1's generator).

**Expected result.** `just bench` compiles and runs `go test -tags bench -bench . -benchmem
-run '^$' ./...` (or the specific bench package), passing the corpus-size knob through. Default tier is
small (a few hundred objects) so a routine `just bench` completes quickly; large tiers are opt-in.
Verified by running `just bench` locally and confirming (a) it executes at least one Benchmark, (b)
`just verify` still runs zero benchmarks (grep the verify recipe / confirm the `bench` tag is off by
default).

**What it reuses/migrates.** Extends `justfile` (currently `build/fmt/vet/test/test-integration/verify/
release`). Mirrors the existing tag convention (`-tags integration`).

**DoD.**
- [ ] `just bench` recipe added; runs the tagged benchmark package with `-benchmem`.
- [ ] Corpus size is env/flag-tunable; documented small default + large-run instructions.
- [ ] `just verify` demonstrably runs **no** benchmarks (tag off; `bench` not in the `verify` chain).
- [ ] `justfile` documented (recipe comment) and `--list`-discoverable.

**TDD:** primarily a harness task — `tdd-red`/`tdd-green` apply to any thin Go glue (e.g. an env-knob
parser with a unit test); the recipe wiring is verified by execution. `tdd-refactor` optional.
**Depends on:** T1.

---

### T3 — Cold-index scaling benchmark (Story 2: NFR-1)

**Behavior.** Benchmark cold `workspace.Build` (no cache; `cachePath=""`) over the generated corpus at
**≥3 tiers** (small / medium / large), reporting wall time and allocations per tier.

**Fixtures / generator needs.** T1 generator, one temp corpus per tier (regenerated per benchmark, or
generated once per tier and reused across `b.N` — generation excluded from the timed region via
`b.ResetTimer()` / `b.StopTimer()`).

**Expected result.** `BenchmarkColdIndex_<tier>` functions (or one table-driven benchmark with sub-
benchmarks per tier via `b.Run`) call `b.ReportAllocs()` and time only `workspace.Build`. The recorded
output lets T9 compute a **scaling ratio** across tiers (time-per-object should stay roughly flat →
linear-ish). No absolute wall-clock assertion by default (environment-sensitive, OQ-C); if any assertion
is added it must be a **generous relative** one (e.g. per-object time at the large tier ≤ K× the small
tier, K comfortably loose), guarded so it does not run in the `just verify` gate.

**What it reuses/migrates.** `workspace.Build` (`index.go:386`); the analyzer; T1 generator.

**DoD.**
- [ ] `b.ReportAllocs()` enabled; timed region is `Build` only (generation excluded).
- [ ] ≥3 corpus tiers benchmarked (sub-benchmarks or separate funcs).
- [ ] Any threshold is relative + generous + non-gating (OQ-C); default is measure-and-record.
- [ ] Lives in the `bench`-tagged package; not run by `just verify`.

**TDD:** `tdd-red` (benchmark that must at least compile/run and report) → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T2.

---

### T4 — Cold-index peak-memory benchmark (Story 2: NFR-4)

**Behavior.** Measure held memory (not just per-op allocs) at the largest feasible tier, to substantiate
NFR-4 ("tens of thousands without memory exhaustion").

**Fixtures / generator needs.** T1 generator at the large tier.

**Expected result.** Around a `workspace.Build` at the large tier, capture `runtime.ReadMemStats` (force
`runtime.GC()` before reading), report **peak `HeapAlloc`** (and `HeapInuse`) via `b.ReportMetric(...,
"heapMiB")`. NFR-4 is asserted as **roughly-linear growth across tiers** (heap-per-object stays within a
generous band tier-to-tier), NOT an absolute cap (OQ-F) — an absolute MiB ceiling is
machine/config-dependent and would be a brittle gate. The large-tier peak figure is recorded verbatim in
T9's results doc with hardware noted.

**What it reuses/migrates.** `workspace.Build`; `runtime` memstats; T1 generator.

**DoD.**
- [ ] Peak `HeapAlloc` reported via `b.ReportMetric` at ≥2 tiers (to compute growth).
- [ ] `runtime.GC()` before each `ReadMemStats` so the figure reflects held (not transient) memory.
- [ ] NFR-4 expressed as roughly-linear heap growth, not an absolute cap (OQ-F); non-gating.
- [ ] Large-tier peak figure captured for T9's results doc.

**TDD:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T2.

---

### T5 — Warm-start + freshness benchmarks (Story 3: NFR-2, NFR-8)

**Behavior.** (a) Benchmark `BuildWithCache` over an **unchanged** large corpus (full cache hit) —
the sub-second warm-start claim. (b) Benchmark a **partial-invalidation** case (a handful of files
changed) — only-changed-files re-analysis. (c) Assert freshness at scale: after a file's content
changes, its analysis is never served stale.

**Fixtures / generator needs.** T1 generator; a cache written by a first `BuildWithCache` pass (excluded
from the timed region). For the partial case and freshness case, mutate a few generated files and supply
`currentHashes` reflecting the change (the workspace-tests seam) so the changed set is deterministic.

**Expected result.**
- Warm hit: `BenchmarkWarmStart_fullhit` times a second `BuildWithCache` where `staleCount == 0`
  (assert the returned `staleCount` is 0 to prove the cache path was actually hit, not a silent rebuild);
  record whether the median run is sub-second at the large tier (recorded figure, non-gating per OQ-C).
- Partial: `BenchmarkWarmStart_partial` mutates K files, asserts returned `staleCount == K`, times the
  re-analysis.
- Freshness: a **non-benchmark unit test** (so it runs in `just test`, guarding NFR-8 permanently)
  builds a small corpus, writes cache, changes a file's content, rebuilds via `BuildWithCache`, and
  asserts the rebuilt `FileAnalysis` reflects the new content (e.g. a changed CALLNAT target now present
  / old one gone) — never the stale cached version. This is the regression fixture NFR-9 wants.

**What it reuses/migrates.** `BuildWithCache` + `staleCount`/`currentHashes` contract (`index.go:424`,
server wiring `server.go:1374`); existing cache Load/Save.

**DoD.**
- [ ] Full-hit benchmark asserts `staleCount==0` (proves the warm path) and records the timing.
- [ ] Partial benchmark asserts `staleCount==K` and times only the rebuild.
- [ ] Freshness is a **normal** (non-tagged) unit test that runs in `just test` (NFR-8/NFR-9).
- [ ] Sub-second result recorded (or its miss documented) for T9; benchmarks non-gating.

**TDD:** `tdd-red` → `tdd-green` → `tdd-refactor`. Freshness test drives red-green like a normal fixture.
**Depends on:** T1, T2.

---

### T6 — Interactive request-latency benchmarks (Story 4 AC1, AC3-measure: NFR-3)

**Behavior.** Benchmark the interactive providers against the large corpus **as they are today**
(before the T8 fix): `workspace/symbol`, completion prefix enumeration (`NamesWithPrefix`), `definition`,
and `references`. This establishes the baseline the T8 fix must beat and measures whether
`NamesWithPrefix`/`LookupByName` O(files) is actually hot (feeds the OQ-E decision).

**Fixtures / generator needs.** T1 large corpus; a built `handlerContext`/`Index` (build excluded from
the timed region). Realistic query inputs generated from known object names (so matches are non-empty).

**Expected result.** `BenchmarkProvide_WorkspaceSymbol`, `BenchmarkProvide_Completion`,
`BenchmarkProvide_Definition`, `BenchmarkProvide_References` with `b.ReportAllocs()`. Record per-op time
and allocs at the large tier for T9. Because `workspace/symbol` and `references` do a per-query disk
sweep, expect high time/alloc — that is the baseline. Also add a focused
`BenchmarkNamesWithPrefix`/`BenchmarkLookupByName` (index-level, no server) so the O(files) cost is
isolated for the OQ-E decision.

**What it reuses/migrates.** `provideWorkspaceSymbols`, `provideCompletion`, `provideDefinition`,
`provideReferences`; `Index.NamesWithPrefix`/`LookupByName`. No production change in this task — pure
measurement.

**DoD.**
- [ ] Four provider benchmarks + two index-method benchmarks, all `-benchmem`.
- [ ] Timed region excludes corpus build and index construction.
- [ ] Baseline numbers recorded for T9 (pre-fix), to contrast with T8's post-fix numbers.
- [ ] `bench`-tagged; not run by `just verify`.

**TDD:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T2.

---

### T8 — Eliminate the `workspace/symbol` (and `references`) per-query disk sweep (Story 4 AC2: NFR-3)

> **Numbered T8 deliberately — it is the production fix and must follow the baseline benchmark (T6) and
> precede the improvement benchmark (T9's re-run). T7 (name-index cache) is conditional and slots after.**

**Behavior.** Serve `workspace/symbol` symbol names/locations from the **in-memory** index — remove the
`os.ReadFile` sweep inside `idx.ForEach` (`workspace_symbols.go:59`) — while returning **byte-identical**
results (same symbols, kinds, ranges, ordering). Apply the same treatment to `provideReferences`'s
full-workspace sweep (`references.go:151`). This is a **behavior-preserving** change: the output must not
change, only the disk I/O.

**The design decision (OQ-B) determines the exact mechanism.** The disk read exists solely to convert
byte-offset columns to encoding code units for UTF-16 clients. Options (pick one; T8 is written to accept
the user's choice):
- **(B-i) Precompute protocol ranges at index time.** Store the encoding-converted
  `SelectionRange`/`Range` (or a per-line code-unit table) on the symbol/index entry so the provider
  needs no content. Cleanest at query time; but the negotiated encoding is a server-runtime property
  (initialize), not known at analyze time — so this likely means storing a per-line UTF-16 width table
  (encoding-agnostic) rather than final protocol ranges. Modest per-file memory.
- **(B-ii) Store raw content (or just line-start offsets + a UTF-16 width cache) in the in-memory
  index** and expose it through a new additive accessor so `ForEach` callbacks can convert ranges without
  a disk read. Simpler code; larger memory footprint (tension with NFR-4 — measure the delta in T4/T9).
- **(B-iii) ASCII fast-path only:** most Natural source is ASCII, where byte offset == UTF-16 unit; keep
  content off the index and only fall back to a bounded per-file read when a line contains non-ASCII.
  Avoids the memory cost but is not a *guaranteed* elimination of all disk reads (only the common case) —
  weaker against the AC's "no `os.ReadFile` sweep" wording. Recommend against unless memory is decisive.

**Recommended:** **(B-i)** storing a per-file line-width table (encoding-agnostic, computed once at
analyze time), because it eliminates the disk read for all encodings with bounded, measurable memory —
and its memory delta is captured by T4/T9 so the NFR-4 tension is quantified, not hand-waved.

**Fixtures / generator needs.** A small **regression** corpus/fixture including at least one file with a
**non-ASCII** character on a symbol line (so UTF-16 conversion is genuinely exercised) — this is the
fixture that proves "identical output without disk read" is correct, not just correct-for-ASCII.

**Expected result.**
- A **regression test** (normal, non-tagged — runs in `just test`) asserts `provideWorkspaceSymbols`
  returns **exactly** the same `[]SymbolInformation` (names, kinds, ranges, order) as the current
  disk-reading implementation, including for the non-ASCII UTF-16 case, for a query hitting object roots
  and subroutines. Same for `provideReferences` on a corpus with cross-refs.
- A test proving the disk read is gone: either (a) build the index, then **delete the corpus files from
  disk**, and assert `workspace/symbol`/`references` still return full results (impossible if it read
  disk); or (b) inject a read-counter and assert zero reads during the query. Approach (a) is the
  strongest, self-contained proof.

**What it reuses/migrates.** `provideWorkspaceSymbols` (rewrite the body), `provideReferences` (rewrite
the sweep), `toProtocolRange`/`position.go` (feed it the in-memory line data instead of disk content),
and whatever additive `Index`/`FileAnalysis`/`model.Symbol` surface OQ-B selects. **Migration note:** if
B-i/B-ii adds a field to `model.Symbol` or `FileAnalysis` and it is persisted, that is a cache-format
bump and a seam change (add `review-seam`); if the line-width table is in-memory-only (recomputed on
load, like resolution), **no cache bump** — **recommended** to keep it in-memory-only.

**DoD.**
- [x] No `os.ReadFile` (or any disk read) in `provideWorkspaceSymbols` or `provideReferences`' sweep.
- [x] Regression test: byte-identical results vs. the prior implementation, incl. a non-ASCII/UTF-16 file.
- [x] Disk-read-gone proof (delete-files-then-query, or zero-read counter).
- [x] Existing `workspace/symbol` and `references` tests still green (no behavior change).
- [x] Deterministic ordering preserved; graceful degradation preserved (nil-safe).
- [x] Seam/model/cache impact resolved per OQ-B (in-memory-only line-width table on `workspace.Index`; no cache bump, no model change, no seam change).
- [x] `gofmt`/`go vet`/`-race` clean.

**TDD:** `tdd-red` (regression + disk-gone tests fail against a naive change) → `tdd-green` →
`tdd-refactor`.
**Depends on:** T6 (baseline captured first). Enables the T9 re-run.

---

### T7 — [CONDITIONAL] Cache the name index (Story 4 AC3: NFR-3)

> **Conditional on T6's numbers (OQ-E).** Only do this if T6 shows `NamesWithPrefix`/`LookupByName`
> O(files) is actually hot at the large tier. If T6 shows it is negligible, **skip T7 and record the
> decision** ("measured, not hot, not optimized") — that itself satisfies Story 4 AC3's "decision
> recorded either way."

**Behavior.** Precompute the name→`[]Candidate` map once and cache it on `Index`, invalidating it on
`Add`/`Invalidate` (the mutation points) under the existing lock, so `NamesWithPrefix` and `LookupByName`
become O(matches) instead of O(all files) per call.

**Fixtures / generator needs.** Reuse existing `index_test.go` fixtures + the T1 corpus for the
before/after benchmark.

**Expected result.** A cached `nameIndex` field on `Index`, rebuilt lazily or on mutation; `Add` and
`Invalidate` invalidate/refresh it under `idx.mu.Lock()`. `NamesWithPrefix`/`LookupByName` consult the
cache. **Correctness must be identical** — a test asserts the cached path returns the same candidates
(same order, same reachability filtering) as a fresh `buildNameIndex`, including after an `Add` and after
an `Invalidate`. The T9 re-run shows the improved latency. Note the `cfg` dependency: `buildNameIndex`
takes `cfg`; the cache key or rebuild must account for `cfg` not changing mid-session (it doesn't — cfg
is fixed at startup), which the task documents.

**What it reuses/migrates.** `buildNameIndex` (`index.go:247`), `NamesWithPrefix`, `LookupByName`, `Add`,
`Invalidate`. Concurrency-sensitive → `-race` required; add `review-concurrency`.

**DoD.**
- [x] Cached name index invalidated on `Add` (the sole `idx.entries` writer — `Build`, cache `Load`,
      and the server's `applyDocumentChange` all funnel through `Add`; `Invalidate` is read-only and
      does not mutate entries) under the write lock already held.
- [x] Correctness test: cached results == fresh `buildNameIndex`-derived results (hand-seeded + real
      analyzer-built corpus), incl. post-`Add` invalidation and post-retype (`TestNameIndexCache_*` in
      `internal/workspace/name_index_cache_test.go`). Invalidation tests verified to FAIL when the
      invalidation is removed (they have teeth).
- [x] `-race` clean; no torn reads — cache is read only under `RLock`, built/nilled only under `Lock`;
      double-checked build on the slow path (no `RLock→Lock` upgrade → deadlock-free). Targeted
      `TestNameIndexCache_ConcurrentLookupsAndAdd` runs concurrent lookups vs. `Add` under `-race`.
- [x] Before/after latency recorded (see T9 results below).

**TDD:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T6 (gate decision). Complements T8.

**Recorded before/after (Apple M4 Max, darwin/arm64, Go 1.26; corpus tiers 200/1000/4000 objects;
warm-cache path — no mutation between lookups, the realistic keystroke case).** Feed into T9.

| Benchmark (large=4000) | Before (T6) ns/op | After (T7) ns/op | Speedup | Before allocs | After allocs | Before B/op | After B/op |
|---|---|---|---|---|---|---|---|
| `NamesWithPrefix/large` | 3,628,850 | 41,475 | ~87.5× | 12,082 | 17 | 2,264,593 | 80,911 |
| `NamesWithPrefix/medium` | 389,159 | 10,900 | ~35.7× | 3,042 | 15 | 561,028 | 19,434 |
| `NamesWithPrefix/small` | 45,667 | 2,168 | ~21.1× | 629 | 13 | 92,232 | 4,840 |
| `LookupByName/large` | 2,960,497 | 30.46 | ~97,000× | 2 | 1 | 88 | 64 |
| `LookupByName/medium` | 221,480 | 30.76 | ~7,200× | 2 | 1 | 88 | 64 |
| `LookupByName/small` | 14,521 | 29.50 | ~490× | 2 | 1 | 88 | 64 |

`LookupByName` is now O(matches) (a single map lookup + a small copy), independent of index size
(~30 ns flat across tiers). `NamesWithPrefix`' residual cost is the prefix scan across all name
buckets + reachability filter + result copy (still scales with total name count, but no longer
rebuilds the O(files) map per call — the per-object allocation is eliminated). The first call after a
mutation pays a one-time O(files) rebuild; between mutations (per keystroke) it is the warm figure
above.

---

### T9 — Re-run benchmarks + record results against the NFRs (Story 2 AC2; Story 3 AC1; Story 4 AC4)

**Behavior.** Re-run the T3/T4/T5/T6 benchmarks after T8 (and T7 if done), and record the numbers —
honestly — against each NFR, with hardware noted. Anything unmet is documented as a known limitation,
not left silently unproven.

**Fixtures / generator needs.** T1 generator at all tiers (including a manual large-tier run for the
headline figures).

**Expected result.** A results section (recommended: a `## Results (recorded)` section appended to this
plan, or a `docs/perf/results.md` — OQ-D placement) capturing, with CPU/RAM/OS/Go-version noted:
- NFR-1: cold-build time at each tier + the computed scaling ratio (linear-ish confirmation).
- NFR-4: peak `HeapAlloc` at the large tier + heap-growth ratio across tiers.
- NFR-2: warm-start full-hit time (sub-second? recorded yes/no) + partial-invalidation time.
- NFR-3: `workspace/symbol`/`references`/completion/definition latency **before vs after T8**
  (the disk-sweep-elimination improvement, quantified), plus the `NamesWithPrefix`/`LookupByName`
  before/after (or the "measured, not hot" note if T7 was skipped).
- Any NFR target not met → an explicit "known limitation" note.

**What it reuses/migrates.** All prior benchmarks; no new production code.

**DoD.**
- [x] Every NFR (1/2/3/4) has a recorded figure or an explicit known-limitation note (plus NFR-8/NFR-9).
- [x] Before/after contrast for the T8 fix is shown (proves the improvement, Story 4 AC2/AC4); T7
      before/after included; T7 was performed because T6 measured the name index hot (OQ-E).
- [x] Hardware + Go version + date noted (Apple M4 Max, darwin/arm64, Go 1.26, 2026-07-18).
- [x] Results recorded in a `## Results (recorded)` section of plan.md (OQ-D default location).

**TDD:** documentation/measurement task — no red-green code loop; `tdd-refactor` N/A. (A reviewer, not a
TDD agent, validates the recording.)
**Depends on:** T3, T4, T5, T6, T8, and T7 (if performed).

---

## Traceability (every acceptance criterion → task)

| Story / AC | Criterion | Task(s) |
|---|---|---|
| S1 AC1 | Deterministic multi-library generator | T1 |
| S1 AC2 | Generated-not-committed, git-ignored | T1 |
| S2 AC1 | Cold build time + memory benchmarks | T3, T4 |
| S2 AC2 | Linear scaling + peak-memory figure recorded | T9 (measured by T3/T4) |
| S2 AC3 | `just bench` recipe, excluded from `just verify` | T2 |
| S3 AC1 | Warm full-hit + partial-invalidation benchmark | T5 |
| S3 AC2 | Freshness at scale (never stale) | T5 (freshness unit test) |
| S4 AC1 | Benchmarks for symbol/completion/definition/references | T6 |
| S4 AC2 | `workspace/symbol` disk sweep eliminated + regression benchmark | T8 (fix) + T6/T9 (before/after) |
| S4 AC3 | `LookupByName`/`buildNameIndex` cost measured; cache if hot (decision recorded) | T6 (measure) + T7 (conditional) + T9 (decision) |
| S4 AC4 | Latencies recorded vs NFR-3; unmet documented | T9 |

---

## Reviews required (`/review-feature`)

- **review-performance** — the whole point; validate the benchmarks measure what they claim (timed
  regions exclude setup, `-benchmem` on, tiers meaningful) and the T8 fix genuinely removes the disk
  sweep.
- **review-concurrency** — T8 changes the index-lock-held read path; T7 (if done) mutates a cached name
  index under the lock. `-race`.
- **review-seam** — only if OQ-B lands a persisted `model`/`FileAnalysis`/`Symbol` field (recommended
  path avoids this by keeping the line-width data in-memory-only → then this review is a no-op confirm).
- **review-robustness** — T1 generator + T8's nil-safety/graceful-degradation on a corpus with malformed
  or non-ASCII files.
- **review-docs** — new `just bench` recipe (command list) and the recorded results doc; the CLAUDE.md/
  README "Project state" note must mention the benchmark harness and the `workspace/symbol` fix.

---

## Decisions (user-approved 2026-07-18)

- **OQ-A → tagged-package + `just bench`** (`//go:build bench`, off the `just verify` gate). Confirmed.
- **OQ-B → B-i: in-memory per-file line-width table**, built at analyze time and recomputed once at
  cache-load, encoding-agnostic (maps byte-offset→code-unit for UTF-8/UTF-16). Eliminates ALL per-query
  disk reads in `workspace/symbol` and `references`. **In-memory only — no cache-format bump, no seam
  change.** (T8.)
- **OQ-C → measure-and-record** with only GENEROUS RELATIVE assertions (per-object time/heap within a
  loose band across tiers). No absolute wall-clock/MiB gate.
- **OQ-D → small default corpus** (a few hundred objects) for routine `just bench`; `BENCH_CORPUS_OBJECTS`
  env/flag knob to scale to 10k–50k for manual runs; headline figures recorded in a `## Results` section
  of this plan.
- **OQ-E → measure first (T6), fix the name index (T7) ONLY if hot.** T7 is conditional.
- **OQ-F → record peak `HeapAlloc` + assert roughly-linear growth** across tiers (generous band), no
  absolute cap. Largest tier is whatever the hardware allows (30k headline acceptable if 50k infeasible).
- **OQ-G → benchmarks stay OFF CI's required path.** Confirmed.

---

## Open questions (RESOLVED above — original text retained for context)



- **OQ-A — Benchmark placement & gating.** *Recommendation:* a dedicated `//go:build bench`-tagged
  package (`internal/workspace/bench/` or `internal/bench/`) run by a new `just bench` recipe, mirroring
  the existing `-tags integration` convention. This keeps benchmarks entirely out of `just verify` (the
  tag is off by default) while making them first-class and CI-runnable on demand. Alternative:
  in-package `Benchmark*` funcs guarded by `testing.Short()` — rejected because `go test ./...` still
  compiles+lists them and a stray `-bench` in the gate would run them. **Decision needed:** confirm the
  tagged-package approach (and the package path).

- **OQ-B — How to eliminate the `workspace/symbol`/`references` disk read (the core of T8).** The disk
  read exists only to convert byte columns → encoding code units for UTF-16. Options B-i (in-memory
  per-file line-width table, encoding-agnostic), B-ii (store raw content in the index), B-iii (ASCII
  fast-path + bounded fallback). *Recommendation:* **B-i, in-memory-only (recomputed on cache load, not
  persisted)** — eliminates all disk reads for every encoding, bounded memory delta (quantified by
  T4/T9), and **no cache-format bump / no seam change**. **Decision needed:** approve B-i (and the
  in-memory-only, no-cache-bump stance), or pick B-ii/B-iii and accept their trade-offs.

- **OQ-C — Assert thresholds, or measure-and-record?** *Recommendation:* **measure-and-record** the
  numbers (documented figures with hardware), with any hard assertion being **generous and relative**
  (e.g. per-object time / heap stays within a loose band across tiers → linear-ish), never an absolute
  wall-clock or MiB gate — those are environment-sensitive and would flake in CI. **Decision needed:**
  confirm no absolute-threshold gating; confirm whether even the relative assertions should exist or be
  omitted entirely (pure record).

- **OQ-D — CI vs local corpus size + results-doc location.** *Recommendation:* small default tier (a few
  hundred objects) for a routine `just bench`, with an env/flag knob
  (`BENCH_CORPUS_OBJECTS=…`) to scale to 10k–50k for **manual** large runs whose headline figures are
  recorded once. CI (if it runs `just bench` at all) uses the small tier only. Results recorded in a
  `## Results (recorded)` section of this plan (or `docs/perf/`). **Decision needed:** confirm the small
  default + manual-large-run split, and the results-doc location.

- **OQ-E — Precompute the name index (T7) now, or only if hot?** *Recommendation:* **benchmark first
  (T6), fix only if the numbers justify it** — avoid speculative optimization. If done, cache is
  invalidated on `Add`/`Invalidate` under the existing lock. **Decision needed:** confirm the
  measure-then-decide gate for T7 (vs. mandating the optimization up front).

- **OQ-F — What does NFR-4 "without memory exhaustion" assert?** *Recommendation:* record **peak
  `HeapAlloc`** at the largest tier and assert **roughly-linear heap growth** across tiers (a generous
  band), NOT an absolute memory cap. **Decision needed:** confirm relative-growth over absolute-cap; and
  confirm the largest tier to attempt on the available hardware (50k may exceed a laptop's RAM/time
  budget — is a 30k headline acceptable if 50k is infeasible locally?).

- **OQ-G — CI wiring for `just bench`.** Not required by any AC (the ACs only require a local recipe).
  *Recommendation:* out of scope for this feature (keep benchmarks manual/local); optionally a
  non-gating scheduled CI job later. **Decision needed:** confirm benchmarks stay off CI's required
  path.
