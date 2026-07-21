# Feature: Cache Format Compaction

**Status:** Planned
**PRD requirements:** NFR-16 (cache compactness), NFR-2 (warm-start latency), NFR-4 (memory/scale), FR-37 (persist index)
**Priority / phase:** P1 (post-assessment; real-user-reported)
**Depends on:** [05](../05-workspace-indexing-and-cache/plan.md) (cache), [21](../21-async-indexing-and-progress/plan.md)/[22](../22-performance-and-scale-verification/plan.md) (cache wired into the server + warm-start measured)

## Summary

The on-disk workspace cache is far too large: a real user reports **~1 GB for 7,790 files (~135 KB/file)**.
Root cause (verified in `internal/workspace/cache.go`): the cache is serialized as **indented** JSON
(`json.MarshalIndent(cache, "", "    ")`) of the full per-file `FileAnalysis` — the recursive `Structure`
symbol tree, `Definitions`, `Edges`, `DataAccess`, `HostVarRefs`, etc. — where JSON field-name repetition
(`"range"`,`"start"`,`"end"`,`"line"`,`"column"`,`"selectionRange"`,`"children"`,`"kind"`,`"name"` …),
4-space indentation, and repeated enum strings (`"CALLS"`, `"data-field"`, …) dominate the bytes; the
actual data is a small fraction. This feature replaces the encoding with a compact one, targeting a
**~10–20× size reduction** (1 GB → ~50–100 MB), and — because feature 22 found warm start is
hash+JSON-deserialization-bound — it also **improves NFR-2 warm-start latency** (less disk I/O, faster
decode). The cache is an **internal, per-machine, disposable artifact** (content-hash-invalidated, rebuilt
on format-version mismatch), so the on-disk format may change freely with a version bump.

## Recommended approach (to be confirmed at planning)

1. **Stop indenting + gzip the JSON** (primary; minimal, stdlib `compress/gzip`, no new deps). Compact
   JSON removes indentation waste; gzip's LZ77 deduplicates the massive field-name/enum repetition
   (JSON of this shape compresses ~10–20×). ~a-few-lines change on each of `Save`/`Load`. Load detects the
   gzip magic bytes (`0x1f 0x8b`) so a legacy/uncompressed cache degrades gracefully (FR-43).
2. **Deeper compaction (documented fallback, only if #1 is insufficient at 30k–50k files):** encode enum
   kinds as integers, and/or a custom length-prefixed binary encoding (or `encoding/gob`) for the
   `Symbol`/`DataDefinition` trees. More code and version-fragility; pursue only if measurements justify it.

Bump `cacheFormatVersion` (`0.6.0` → `0.7.0`) so existing caches rebuild once; the new key canonicalization
(`internal/paths.NormalizeKey`, ADR-027) is unaffected.

## User stories

### Story 1 — A compact cache that scales (NFR-16, NFR-4)
**As a** user of a large workspace, **I want** the on-disk cache to be a small fraction of its current
size **so that** it doesn't consume gigabytes of disk for tens of thousands of files.

**Acceptance criteria:**
- [ ] The cache encoding is changed so that a representative corpus's cache is **≥10× smaller** than the
      current indented-JSON cache (measured via the feature-22 synthetic corpus at ≥1 tier; record
      before/after bytes-per-file).
- [ ] Round-trip fidelity: an index built, saved, and reloaded from the new format is **byte-for-byte
      equivalent in behavior** to the pre-change index (same symbols/edges/definitions/structure/resolution
      — a save→load→compare test over the corpus).
- [ ] Cache size scales roughly linearly with workspace size (no super-linear blow-up).

### Story 2 — Warm-start latency improves, never regresses (NFR-2)
**As a** developer reopening a large workspace, **I want** warm start to stay sub-second (and ideally
faster) **so that** the smaller cache also speeds startup.

**Acceptance criteria:**
- [ ] A warm-start benchmark (feature-22 `just bench` harness) shows the new format's load time is **≤ the
      old format's** at the measured tiers (record the numbers; the expectation is a reduction from less
      disk I/O + compact decode).
- [ ] Save time is not pathologically worse (compression cost is bounded and off the request path — it
      runs on the background build goroutine, feature 21).

### Story 3 — Safe format migration & graceful fallback (FR-37, FR-43)
**As a** user upgrading the binary, **I want** the cache to migrate transparently **so that** an old cache
never corrupts results or crashes the server.

**Acceptance criteria:**
- [ ] `cacheFormatVersion` is bumped; a cache written by the old version is detected as stale and triggers
      a one-time full rebuild (existing behavior), then is rewritten in the new format.
- [ ] Load of a corrupt/truncated/unexpected-encoding cache falls back to a full rebuild with no error or
      panic (FR-43); a regression test covers a corrupt compressed cache.
- [ ] No `internal/model` change is required (the persisted shapes are unchanged; only the *encoding*
      changes). If deeper compaction (Story fallback) is pursued, any model/enum-encoding change is called
      out explicitly.

## Out of scope / deferred
- Cross-language cache consumption (the cache is internal and Go-only).
- Removing data from the cache (all persisted fields remain; this is an encoding change, not a schema cut).
- The custom binary/gob deep-compaction path unless measurements after gzip justify it (documented as a
  fallback, decided from data).

## Notes
- Analysis basis: `internal/workspace/cache.go` (`CacheFile`/`cacheEntry`, `MarshalIndent`,
  `cacheFormatVersion = "0.6.0"`), `internal/model/model.go` (recursive `Symbol`/`DataDefinition` trees),
  and feature-22 warm-start findings (warm start is hash+deserialization-bound). Record the concrete
  before/after in a `## Results` section, like feature 22.

## Results

Measure-and-record (T5), the feature-22 `just bench` pattern — these are recorded figures, not CI
gates (`just bench` is off `just verify`; the `bench` tag is off by default). The **new** format is
compact JSON + gzip (`gzip.BestCompression`, `cacheFormatVersion 0.7.0`, `internal/workspace/cache.go`).

- **Machine / environment:** `goos: darwin`, `goarch: arm64`, `cpu: Apple M4 Max` (from the `go test`
  bench header).
- **Date recorded:** 2026-07-20.
- **Corpus:** deterministic synthetic corpus (`internal/workspace/corpusgen.Generate`), `benchSeed =
  0x2026_0722`.
- **New bench:** `internal/workspace/bench/cache_size_test.go` — `BenchmarkCacheSize`. The **old**
  (indented-JSON) baseline is computed deterministically in-memory with no git checkout: the new gzip
  cache is read back, gunzipped to recover the compact JSON, then re-indented via `json.Indent(…, "",
  "    ")` — byte-identical to what pre-24 `json.MarshalIndent(cache, "", "    ")` would have written
  (the persisted JSON structure is unchanged; only whitespace + compression differ). Run with
  `-benchtime=1x` for the size run (the byte metrics are the result, not ns/op); the metrics are
  identical at the default benchtime.

### Story 1 AC1 (≥10× smaller) + AC3 (linear scaling) — cache size

`BenchmarkCacheSize` (bytes are the on-disk gzip cache vs. the re-indented old baseline):

| Tier | Objects | Old bytes/obj | New bytes/obj | Old total | New total | Compaction-x |
|---|---|---|---|---|---|---|
| small | 200 | 10,088 | 85.8 | 2,017,633 | 17,157 | **117.6×** |
| medium | 1000 | 10,269 | 81.9 | 10,268,746 | 81,857 | **125.4×** |
| large | 4000 | 10,288 | 81.7 | 41,152,282 | 326,788 | **125.9×** |
| custom | 10000 | 10,332 | 82.3 | 103,318,660 | 822,629 | **125.6×** |

- **S1 AC1 (≥10× smaller): MET — by a wide margin.** Compaction is **~118–126×** at every tier, far
  above the ≥10× headline and the ~10–20× the plan projected. The bench carries only a generous floor
  (`ratio < 3 → Fatal`) so a silent gzip regression is caught; the ≥10× figure is recorded, not gated.
- **S1 AC3 (linear scaling): MET.** Both old and new bytes/object are flat across a 50× object range
  (old ~10.1k–10.3k, new ~81.7–85.8 bytes/object; the small-tier new figure is slightly higher from
  the fixed `version` header + per-file gzip overhead amortizing away as the corpus grows). No
  super-linear blow-up — total cache size scales ~linearly with object count.
- **Real-world projection:** at the user-reported 7,790-file scale, ~82 bytes/object ⇒ a **~640 KB**
  cache vs. the reported **~1 GB** old cache (~1,570×), because the reported figure came from a much
  richer real corpus than the synthetic one; even on the synthetic corpus the ≥10× target is met ~12×
  over.

### Story 2 AC1 (warm ≤ old) — warm-start latency

`BenchmarkWarmStart_fullhit` (full cache hit, `staleCount == 0` asserted so the warm path is proven
hit). **Old-format baseline** is feature-22's recorded `## Results` (indented-JSON cache, same harness,
same machine class); **new-format** is this run:

| Tier | Objects | Old ns/op (feature-22) | New ns/op | Δ |
|---|---|---|---|---|
| small | 200 | 13,635,980 (≈13.6 ms) | 9,061,654 (≈9.1 ms) | **−34%** |
| medium | 1000 | 71,503,450 (≈71.5 ms) | 46,713,734 (≈46.7 ms) | **−35%** |
| large | 4000 | 299,551,000 (≈300 ms) | 218,598,764 (≈218.6 ms) | **−27%** |
| custom | 10000 | — (not recorded in feature-22) | 527,947,208 (≈528 ms) | new figure |

`BenchmarkWarmStart_partial` (K=5 changed files, `staleCount == 5` asserted), new format: small
≈12.5 ms, medium ≈64.5 ms, large ≈271.7 ms.

- **S2 AC1 (warm ≤ old at measured tiers): MET.** New-format warm-start is **lower at every
  comparable tier** (−27% to −35%), consistent with the plan's expectation (less disk I/O + faster
  compact decode). **Comparison basis:** the "old" column is feature-22's recorded warm-start figures
  (the last measurement taken on the old indented-JSON format on this machine class); the new column
  is this 2026-07-20 run. The 10000-object tier had no feature-22 baseline, so only the new figure
  (≈528 ms) is recorded. Warm start stays sub-second through 10000 objects. The feature-22
  future-scale caveat still applies: on a full hit no file is re-analyzed, so the residual cost is
  the SHA-256 content-hash re-read (unchanged) plus JSON decode (now over ~120× fewer bytes) — the
  win is exactly the reduced I/O + decode this feature targeted.

### Story 2 AC2 (save time not pathologically worse) — cold/save path

`BenchmarkColdIndex` (cold `BuildWithCache` — this is the save/write path, since a cold build writes
the cache):

| Tier | Objects | Old ns/op (feature-22, no-cache Build) | New ns/op (BuildWithCache, writes gzip) | ns/object (new) |
|---|---|---|---|---|
| small | 200 | 3,060,649 | 3,120,659 (≈3.1 ms) | 15,603 |
| medium | 1000 | 16,726,316 | 17,190,732 (≈17.2 ms) | 17,191 |
| large | 4000 | 73,607,206 | 73,073,106 (≈73.1 ms) | 18,268 |
| custom | 10000 | — | 205,582,808 (≈205.6 ms) | 20,558 |

- **S2 AC2 (save not pathologically worse): MET.** `BestCompression` (level 9) adds **negligible**
  overhead — cold `BuildWithCache` is within ~2% of feature-22's no-cache `Build` at every tier (and
  the large tier is even marginally faster, inside noise). The dominant cost is analysis, not
  serialization; gzip runs on the feature-21 background build goroutine (off the request path), so
  even the level-9 cost is not on any latency-sensitive path. **OQ-2 (compression level) decided from
  this data: keep `BestCompression`** — it delivers the ~125× size win with no measurable save-time
  penalty. Per-object cold cost stays in a ~15.6–20.6 µs/object band from 200→10000 objects (roughly
  linear, matching feature-22's NFR-1 finding).

**Verdicts summary:** S1 AC1 (≥10×) **MET** (~118–126×); S1 AC3 (linear) **MET**; S2 AC1 (warm ≤ old)
**MET** (−27% to −35%); S2 AC2 (save not pathologically worse) **MET** (within ~2%, level 9 kept).
