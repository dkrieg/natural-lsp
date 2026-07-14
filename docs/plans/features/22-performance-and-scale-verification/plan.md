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
