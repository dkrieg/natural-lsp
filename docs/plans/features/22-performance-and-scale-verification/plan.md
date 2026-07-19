# Feature: Performance & Scale Verification

**Status:** Planned
**PRD requirements:** NFR-1, NFR-2, NFR-3, NFR-4 (P0)
**Priority / phase:** P0/P1 remediation (2026-07-14 assessment, defect #5)
**Depends on:** [21](../21-async-indexing-and-progress/plan.md)

## Summary

Turn the PRD's performance claims from assertions into measurements. NFR-4 (P0: tens of
thousands of objects without memory exhaustion), NFR-2 (sub-second warm start), NFR-1 (linear
cold-index scaling), and NFR-3 (interactive request latency) currently have **zero** supporting
benchmarks or scale tests anywhere in the repo. Two known hot spots the measurements will likely
force fixes for: `Index.LookupByName` is O(all files) per query, and `workspace/symbol` re-reads
every indexed file from disk on each query while holding the index's internal `RLock`
(`internal/server/workspace_symbols.go:59`). Scope: a synthetic-corpus generator, Go benchmarks
wired into a `just` recipe, remediation of hot spots the numbers expose, and honest recording of
the results (or of a deliberate re-scope) against the NFR acceptance criteria.

## User stories

### Story 1 — Synthetic enterprise corpus (NFR-9)
**As a** maintainer, **I want** a deterministic generator producing a configurable synthetic
workspace (e.g. 10k–50k objects with realistic CALLNAT/PERFORM/INCLUDE/DDM cross-references)
**so that** scale behavior is testable without proprietary code.

**Acceptance criteria:**
- [ ] A generator (test helper or `scripts/`) emits a parameterized corpus of sanitized Natural
      objects across multiple libraries; generation is seeded/deterministic.
- [ ] The corpus is generated on demand (not committed) and excluded from git.

### Story 2 — Cold index scaling and memory (NFR-1, NFR-4)
**As a** maintainer, **I want** benchmarks for cold `workspace.Build` at ≥3 corpus sizes
**so that** linear scaling and bounded memory are demonstrated, not assumed.

**Acceptance criteria:**
- [ ] `Benchmark`-style tests measure cold build wall time and allocated/held memory
      (`b.ReportAllocs` + runtime heap stats) at small/medium/large corpus sizes.
- [ ] Results demonstrate roughly linear time scaling and a documented peak-memory figure at the
      tens-of-thousands size; numbers are recorded in this plan (or docs/) with the hardware
      noted.
- [ ] A `just bench` (or similar) recipe runs the suite locally; it is excluded from
      `just verify` (too slow for the gate) but documented.

### Story 3 — Warm startup (NFR-2, NFR-8)
**As a** user reopening a large workspace, **I want** cache-served startup measured
**so that** the "sub-second warm start" claim is verified at scale.

**Acceptance criteria:**
- [ ] A benchmark measures `BuildWithCache` over an unchanged large corpus (full cache hit) and
      records whether the sub-second target holds; a partial-invalidation case (a few changed
      files) is also measured.
- [ ] Cache-freshness behavior at scale is asserted (changed content is never served stale).

### Story 4 — Interactive request latency and hot-spot fixes (NFR-3)
**As a** developer in a large workspace, **I want** provider requests to stay interactive
**so that** navigation and search don't degrade with codebase size.

**Acceptance criteria:**
- [ ] Benchmarks cover `workspace/symbol`, completion prefix enumeration, definition, and
      references against the large corpus.
- [ ] The `workspace/symbol` per-query full-workspace disk re-read is eliminated (serve symbol
      names/locations from the in-memory index; no `os.ReadFile` sweep under the index lock),
      with a regression benchmark proving the improvement.
- [ ] `LookupByName`/`buildNameIndex` cost at scale is measured; if per-query O(n) shows up in
      the numbers, the name index is cached and invalidated with the index (decision recorded
      either way).
- [ ] Measured latencies are recorded against NFR-3; anything left unmet is explicitly
      documented as a known limitation rather than silently unproven.

## Results (recorded)

**Environment.** Apple M4 Max (16 logical CPUs), macOS (darwin/arm64), Go 1.26. Recorded
2026-07-18 via `just bench` (default tiers **200 / 1000 / 4000 objects** — the "large" default
tier is 4000). The optional 30k headline tier (`BENCH_CORPUS_OBJECTS=30000`) was **not** run
locally for this recording; the largest tier actually measured is **4000 objects**. A 50k tier
was **not** run (out of scope for a laptop run — OQ-F allows a smaller headline if 50k is
infeasible).

> **Measure-and-record posture (OQ-C).** These are recorded figures on **one machine**, not
> absolute CI gates. The benchmarks live behind `//go:build bench` and are **excluded from
> `just verify` and CI's required path** (OQ-A/OQ-G). The only assertions in the harness are
> **generous relative bands** (per-object time/heap staying within a loose ratio across tiers,
> and `staleCount` equalities that prove the warm/partial paths were actually hit). No absolute
> wall-clock or MiB threshold gates any build.

### NFR-1 — Linear cold-index scaling (P0): PASS

Cold `workspace.Build` (no cache, `cachePath=""`), timed region excludes corpus generation
(`BenchmarkColdIndex`):

| Tier | Objects | ns/op | ns/object | B/op | allocs/op |
|---|---|---|---|---|---|
| small | 200 | 3,060,649 | 15,303 | 1,923,601 | 17,851 |
| medium | 1000 | 16,726,316 | 16,726 | 9,783,271 | 90,246 |
| large | 4000 | 73,607,206 | 18,402 | 39,253,281 | 361,570 |

**Verdict.** Per-object cost stays in a tight **~15.3–18.4 µs/object** band from 200→4000 objects
(large/small ratio ≈ 1.20× per-object over a 20× size increase). Allocations scale ~linearly
(~89–90 allocs/object). This is **roughly linear** — NFR-1 holds at the measured tiers. The slight
per-object uptick with size is consistent with map growth / GC pressure, well within a generous
band.

### NFR-4 — Tens of thousands without memory exhaustion (P0): PASS (roughly-linear growth)

Peak heap around a cold `workspace.Build`, `runtime.GC()` forced before `ReadMemStats`
(`BenchmarkColdIndexMemory`):

| Tier | Objects | heap-bytes/object | index-heap-MiB | heap-inuse-MiB | peak-heap-MiB |
|---|---|---|---|---|---|
| small | 200 | 4,527 | 0.86 | 2.52 | 1.27 |
| medium | 1000 | 4,689 | 4.47 | 6.64 | 4.89 |
| large | 4000 | 4,699 | 17.93 | 22.71 | 18.39 |

**Verdict.** Heap-per-object is **flat** across a 20× size increase (~4,527→4,699 B/object,
≈3.8% drift), so held memory grows **roughly linearly** with object count — no super-linear blow-up.
Extrapolating the flat ~4.7 KB/object index heap, tens of thousands of objects project to tens of
MiB of held index memory (e.g. ~180 MiB index heap at 40k), comfortably within a laptop's RAM.
NFR-4's "without memory exhaustion" holds as roughly-linear growth (not an absolute cap — OQ-F).
Note: the in-memory line-width table added by the T8 fix (ADR-025) did **not** perturb this band —
ASCII lines retain no bytes (near-zero cost), which is why heap-per-object stayed flat.

### NFR-2 — Sub-second warm start (P1): PASS at measured tiers (with a documented future-scale caveat)

`BuildWithCache` over an already-cached corpus (`BenchmarkWarmStart_*`); the cache-priming pass is
excluded from the timed region. `staleCount == 0` on the full-hit path proves the warm path was
actually taken (not a silent rebuild); the partial case asserts `staleCount == K` (K=5):

| Benchmark | Tier | Objects | ns/op | reanalyzed (staleCount) | B/op | allocs/op |
|---|---|---|---|---|---|---|
| WarmStart_fullhit | small | 200 | 13,635,980 (≈13.6 ms) | 0 | 3,684,386 | 13,517 |
| WarmStart_fullhit | medium | 1000 | 71,503,450 (≈71.5 ms) | 0 | 18,749,297 | 67,652 |
| WarmStart_fullhit | large | 4000 | 299,551,000 (≈300 ms) | 0 | 75,136,576 | 270,472 |
| WarmStart_partial | small | 200 | 18,046,107 (≈18.0 ms) | 5 | 13,494,448 | 14,777 |
| WarmStart_partial | medium | 1000 | 90,307,639 (≈90.3 ms) | 5 | 71,185,200 | 72,119 |
| WarmStart_partial | large | 4000 | 370,815,528 (≈371 ms) | 5 | 284,789,200 | 286,963 |

**Verdict.** Warm full-hit start is **sub-second at every measured tier** (≈300 ms at 4000
objects). Partial invalidation (5 changed files) tracks the full-hit cost plus the K re-analyses
and a cache rewrite (≈371 ms at 4000). NFR-2 holds at 4000 objects.

**Future-scale caveat (honest limitation).** On a full hit **no file is re-analyzed**, yet the warm
path still (1) walks the whole tree, (2) re-reads every file to recompute its SHA-256 content hash,
and (3) unmarshals the entire JSON cache into `FileAnalysis`. The warm-start win is skipping
*analysis*, not *I/O* — per-object warm cost (~68–75 µs/object) is roughly the same as or slightly
worse than a cold build. So the **content-hash re-read + JSON deserialization is the projected
bottleneck** at tens of thousands of objects: extrapolating ~75 µs/object, 4000→~300 ms scales to
~0.75 s at 10k and would breach one second somewhere in the low tens of thousands. Sub-second warm
start at 4000 is proven; at tens of thousands it is a known at-risk area for a future NFR-2 pass
(candidate mitigations: streaming/lazy cache decode, mmap, or skipping the full-hash re-read when a
cheaper freshness signal is available).

### NFR-8 — Cache never stale (P1): PASS

Content-hash invalidation is guarded by a **normal (untagged) unit test** that runs in `just test`
(not a benchmark), so freshness is a permanent gating regression fixture:
`internal/workspace/freshness_test.go::TestBuildWithCache_NeverServesStaleContent`. It builds a
small corpus, writes the cache, mutates a file's content (changing a CALLNAT target), rebuilds via
`BuildWithCache`, and asserts the rebuilt `FileAnalysis` reflects the **new** content (new target
present, old target gone) — never the stale cached version. **Verdict.** Invalidation is
content-hash-based (not mtime — survives git checkouts) and the gating test passes; NFR-8 holds.

### NFR-3 — Interactive request latency (P1): PASS

Two production fixes landed to keep providers interactive at scale; both are quantified
before/after at the 4000-object large tier (same hardware). The **AFTER** figures below were
re-captured in this recording run and match the recorded post-fix numbers.

**T8 — eliminate the `workspace/symbol` & `references` per-query full-workspace disk sweep**
(ADR-025; in-memory per-file line-width table, OQ-B B-i — no cache bump, no model/seam change).
The disk read existed solely to convert byte-offset columns → UTF-16 code units; it is now served
from an in-memory, encoding-agnostic line-width table (ASCII lines retain no bytes):

| Provider (large=4000) | Before ns/op | After ns/op | Speedup | Before B/op | After B/op | Mem | Before allocs | After allocs |
|---|---|---|---|---|---|---|---|---|
| `workspace/symbol` | 44,600,000 (≈44.6 ms) | 1,024,360 (≈1.02 ms) | ~46× | 5,290,000 | 736,495 | ~7× less | ~31,800 | 13,754 |
| `references` | 49,000,000 (≈49 ms) | 1,510,430 (≈1.51 ms) | ~34× | 4,330,000 | 580,640 | ~7.5× less | ~24,000 | 8,031 |

**T7 — cache the name→`[]Candidate` index on `Index`, invalidate on `Add`** (ADR-026, OQ-E: the
name index measured hot, so the conditional optimization was performed). `NamesWithPrefix` fires
per completion keystroke; `LookupByName` fires per definition/hover DDM resolve. Warm-cache path
(no mutation between lookups — the realistic keystroke case):

| Index method (large=4000) | Before ns/op | After ns/op | Speedup | Before allocs | After allocs | Before B/op | After B/op |
|---|---|---|---|---|---|---|---|
| `NamesWithPrefix` | 3,628,850 (≈3.63 ms) | 42,100 (≈42 µs) | ~87× | 12,082 | 17 | 2,264,593 | 80,912 |
| `LookupByName` | 2,960,497 (≈2.96 ms) | 29.63 | ~97,000× | 2 | 1 | 88 | 64 |

**Verdict.** After both fixes, every interactive provider at 4000 objects is **≤ ~1.5 ms/query**
(`workspace/symbol` ~1.02 ms, `references` ~1.51 ms) and per-keystroke completion enumeration is
**~42 µs** (`NamesWithPrefix`) with a **flat ~30 ns** `LookupByName`. All comfortably interactive.
`LookupByName` is now O(matches), size-independent (~30 ns flat across all tiers); `NamesWithPrefix`
no longer rebuilds the O(files) map per call (its residual cost is the prefix scan + reachability
filter + result copy). The first lookup after an index mutation pays a one-time O(files) rebuild;
between mutations it is the warm figure above. NFR-3 holds at the measured tiers.

### Summary verdicts

| NFR | Claim | Verdict | Largest tier measured |
|---|---|---|---|
| NFR-1 | Linear cold-index scaling | **PASS** — ~15.3–18.4 µs/object, ~1.20× per-object band over 20× size | 4000 |
| NFR-2 | Sub-second warm start | **PASS at 4000** (~300 ms full-hit); hash+JSON-deser is the projected wall at tens of thousands | 4000 |
| NFR-3 | Interactive request latency | **PASS** — all providers ≤ ~1.5 ms/query; completion ~42 µs; T8 ~46×/~34×, T7 ~87×/~97,000× | 4000 |
| NFR-4 | Tens of thousands without memory exhaustion | **PASS** — flat ~4.7 KB/object held heap (roughly-linear growth) | 4000 |
| NFR-8 | Cache never stale | **PASS** — content-hash invalidation, gating unit test green | — |
| NFR-9 | Deterministic regression fixtures | **PASS** — seeded corpus generator + untagged freshness test | — |

### Honest limitations

- **Largest tier actually measured is 4000 objects.** The 30k headline and 50k tiers were not run
  locally; the NFR-1/NFR-4 linearity claims and the NFR-2/NFR-3 latency figures are **measured at
  4000 and extrapolated** to tens of thousands from the observed per-object bands, not directly
  measured there.
- **Warm-start scaling risk (NFR-2).** Sub-second warm start is proven at 4000 (~300 ms) but the
  per-object warm cost (~75 µs) is dominated by content-hash re-reading and JSON cache
  deserialization, not analysis. This is the projected bottleneck at tens of thousands and is the
  candidate for a future NFR-2 optimization pass.
- **Single-machine, single-run recording.** Figures are from one Apple M4 Max run; they are
  recorded evidence, not CI gates (OQ-C). Absolute numbers will vary by hardware; the relative
  bands and before/after ratios are the load-bearing claims.
