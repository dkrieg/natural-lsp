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
