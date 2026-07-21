# Tasks: Cache Format Compaction (feature 24)

**Feature:** `24-cache-format-compaction`
**Spec:** `docs/plans/features/24-cache-format-compaction/plan.md`
**PRD requirements:** NFR-16 (cache compactness), NFR-2 (warm-start latency), NFR-4 (memory/scale),
FR-37 (persist index), FR-38 (content-hash invalidation), FR-39 (format-version gating), FR-43
(graceful degradation).

This is an **encoding-only change** to the on-disk workspace cache: replace **indented JSON**
(`json.MarshalIndent(cache, "", "    ")`) with **compact JSON + gzip** (stdlib `compress/gzip`,
`encoding/json` plain `json.Marshal`), and bump `cacheFormatVersion` `0.6.0` → `0.7.0`. **No
`internal/model` change, no persisted-shape change** (`CacheFile`/`cacheEntry` and every `model` type
stay byte-identical in structure) — only how those structs are turned into bytes on disk and back.

---

## Current-state findings & impact

Read against the code as it is (`internal/workspace/cache.go`, `internal/workspace/index.go`,
`internal/workspace/cache_test.go`, `internal/workspace/bench/`), not the README.

- **Three encode/decode sites, all in `cache.go`:**
  - `saveIndex(idx, root, cachePath)` (cache.go:54) — the **production** writer, called by
    `BuildWithCache` (index.go:764–767) on the feature-21 background build goroutine. Does
    `json.MarshalIndent(cache, "", "    ")` → `os.MkdirAll` → `os.WriteFile`. **This is the site that
    matters for the size/warm-start wins.**
  - `Save(idx, path)` (cache.go:92) — a legacy CWD-relative writer retained only for the existing test
    round-trips (`cache_test.go`). Also `MarshalIndent`. Not on the server path.
  - `Load(path, currentHashes, logger)` (cache.go:139) — `os.ReadFile` → `json.Unmarshal`. Used by
    `BuildWithCache` (index.go:653) and the tests. Two exit paths already exist: **version mismatch**
    → `(nil, allStale, nil)` (index.go:659 treats `idx==nil` as "discard, cold rebuild"); **read/parse
    error** → `(nil, nil, err)` (index.go:654 treats a non-nil err as "cold rebuild from scratch").
    **Both degradation channels the plan needs already exist** — the new code just has to route into
    them.
- **`cacheFormatVersion = "0.6.0"`** (cache.go:19). The version check (cache.go:152) already forces a
  full rebuild on any mismatch — so bumping to `0.7.0` alone makes every existing `0.6.0` cache
  rebuild once (Story 3 AC1). The gzip-magic detection in Load is still needed for **robustness**
  (a corrupt/truncated `0.7.0` file) and to actually **read a `0.7.0` file back** (round-trip).
- **Existing tests to keep green (they must not regress):** `cache_test.go` has 9 tests that
  `Save`→`Load` round-trip every persisted field (`TestSave_Load`, `TestSave_Load_DataAccessWithNameRange`,
  `TestSave_Load_Definitions`, `TestSave_Load_WorkFiles`, `TestSave_Load_Structure`,
  `TestSave_Load_HostVarRefs`, `TestLoad_ContentHashInvalidation`), and **three version-bump tests**
  (`TestLoad_CacheVersionBumpedForDataAccessRefactoring`, `…ForHostVarRefs`, `…ForStructure`,
  `TestLoad_FormatVersionMismatch`) that downgrade the on-disk `version` string via
  `strings.Replace(content, cacheFormatVersion, oldVersion, 1)` on the **raw file bytes**. **Impact:
  those version-bump tests operate on plaintext bytes and will break once Save/saveIndex gzip the
  output** (a gzip stream has no readable `0.6.0`/`0.7.0` substring to `strings.Replace`). This is a
  **shared-contract change to the test fixtures' assumption**, not to production code — see the T3
  migration note below. `TestLoad_CanonicalizesBackslashKeys` hand-serializes a `CacheFile` with
  `json.MarshalIndent` and writes it as the cache file, then calls `Load`; it exercises Load's
  **legacy-plaintext** path and must keep passing (proving graceful fallback is real).
- **Bench harness (feature 22, `//go:build bench`, off `just verify`):**
  `internal/workspace/bench/warm_start_test.go` has `BenchmarkWarmStart_fullhit`/`_partial` driving the
  real `BuildWithCache` over a `buildCorpus(b, N)` synthetic corpus (`corpusgen.Generate`), scaled by
  `BENCH_CORPUS_OBJECTS`; `bench_helpers_test.go` has `cachePathFor`, `buildCorpus`, `diskHashes`.
  **There is no cache-*size* benchmark yet** — T5 adds one. `just bench` runs the whole bench package.
- **No fuzz target guards the cache** today (`FuzzResolve` is the only workspace fuzz target). A corrupt
  cache is currently only guarded by the graceful-degradation path in `BuildWithCache`, not by a unit
  test on `Load`. T4 adds the regression coverage the plan requires (Story 3 AC2).
- **Seam:** all work is inside `internal/workspace`. No `internal/model`, no `internal/server`, no
  Analyzer-interface contact. Seam is untouched.

**Traceability at a glance:**

| Acceptance criterion | Task(s) |
|---|---|
| S1 AC1 — cache ≥10× smaller (measured, before/after bytes/file) | T5 (measure), Results section |
| S1 AC2 — round-trip fidelity (save→load→compare) | T2 (RED/GREEN), T6 (corpus-level fidelity) |
| S1 AC3 — linear scaling | T5 (measure across tiers) |
| S2 AC1 — warm-start load ≤ old at measured tiers | T5 (measure via existing warm-start bench) |
| S2 AC2 — save time not pathologically worse | T5 (measure), OQ-2 (compression level) |
| S3 AC1 — version bump → one-time rebuild then rewrite in new format | T3 (RED/GREEN) |
| S3 AC2 — corrupt/truncated/unexpected-encoding → rebuild, no panic; regression test for corrupt *compressed* cache | T4 (RED/GREEN), T7 (fuzz) |
| S3 AC3 — no `internal/model` change | enforced across all tasks; T1 asserts shapes unchanged |

---

## Tasks

Ordered by dependency. Each is a thin red → green → refactor slice. Run the TDD agents in order per
task: `tdd-red` (write the failing test), `tdd-green` (minimal code to pass), `tdd-refactor` (clean up,
tests stay green). `just verify` (gofmt + vet + build + unit `-race` + integration) is the gate for
every gated task; the bench tasks (T5) run under `just bench` and are **measure-and-record, NOT gated**.

---

### T1 — Introduce compact-gzip encode/decode helpers behind the existing sites

**Goal:** add two pure, unexported helpers in `cache.go` — `encodeCache(cache CacheFile) ([]byte, error)`
(compact `json.Marshal` → gzip) and `decodeCache(data []byte) (CacheFile, error)` (gzip-magic sniff →
gunzip-or-plaintext → `json.Unmarshal`) — without yet rewiring Save/saveIndex/Load. This is the
foundation the later tasks build on; keeping it a separate helper makes the three call sites a
one-line swap and gives the round-trip test one seam to hit.

- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **RED test (`cache_test.go`):** `TestEncodeDecodeCache_RoundTrip` — build a `CacheFile` with a
  representative `cacheEntry` (ObjectType + a small `Structure` tree + Edges + Definitions +
  HostVarRefs + DataAccess with NameRange), `encodeCache` then `decodeCache`, assert the decoded
  `CacheFile` deep-equals the input (`reflect.DeepEqual` on the two `CacheFile` values). Also assert
  the encoded bytes **start with the gzip magic `0x1f 0x8b`** (proves it is actually compressed) and
  are **smaller than** `json.MarshalIndent(cache, "", "    ")` of the same value (sanity that
  compaction happened at all, at unit scale).
- **GREEN:** implement `encodeCache` (`json.Marshal` → `gzip.NewWriter` over a `bytes.Buffer` → `Close`
  → return buffer bytes) and `decodeCache` (if `len(data) >= 2 && data[0]==0x1f && data[1]==0x8b` →
  `gzip.NewReader` → `io.ReadAll` → unmarshal; else unmarshal `data` directly). Add `bytes`,
  `compress/gzip`, `io` imports. **Do not** touch Save/saveIndex/Load yet.
- **DoD:**
  - [ ] `encodeCache`/`decodeCache` exist as pure helpers (no filesystem I/O).
  - [ ] Round-trip test passes; encoded output carries the gzip magic bytes.
  - [ ] `CacheFile`/`cacheEntry` structs and every `model` type are **unchanged** (no field
        added/removed/retyped) — reviewer confirms diff touches only encode/decode, not shapes.
  - [ ] `just verify` green.

---

### T2 — `Load` decodes gzip; plaintext legacy caches still load (graceful fallback, FR-43)

**Goal:** rewire `Load` to decode via `decodeCache`, so a `0.7.0` gzip cache reads back and a legacy
plaintext cache (as written by any pre-24 binary, and as the tests hand-write) still loads. Save is
still on the old indented path after this task, so this proves Load's **backward-compat** independently.

- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **RED test (`cache_test.go`):** `TestLoad_ReadsGzipAndPlaintext` — two sub-cases:
  1. **gzip case:** hand-build a `CacheFile` (current `cacheFormatVersion`), `encodeCache` it, write to
     `cachePath`, `Load`, assert the entry round-trips (ObjectType/Structure/Edges) and no error.
  2. **plaintext case:** write the same `CacheFile` via `json.MarshalIndent` (legacy shape), `Load`,
     assert it still loads with the same result (FR-43 — an old uncompressed cache degrades
     gracefully, does not error).
- **GREEN:** in `Load`, replace `json.Unmarshal(data, &cache)` with `cache, err := decodeCache(data)`;
  on error return `(nil, nil, err)` (unchanged error contract — index.go:654 already routes a non-nil
  err to a cold rebuild). Everything downstream of the decode (version check, key normalization, hash
  compare) is unchanged.
- **DoD:**
  - [ ] `Load` reads both a gzip cache and a legacy plaintext cache without error.
  - [ ] `TestLoad_CanonicalizesBackslashKeys` (plaintext, hand-serialized) still passes — the
        legacy-plaintext path is proven still live.
  - [ ] `just verify` green.

---

### T3 — `saveIndex` + `Save` write compact gzip; bump `cacheFormatVersion` → `0.7.0`; migrate the version-bump tests

**Goal:** flip the two writers to `encodeCache` and bump the version, so a `0.6.0` cache rebuilds once
and is rewritten in the new format (Story 3 AC1). This is the task that changes the **on-disk bytes**,
so it must also migrate the existing version-bump tests that assumed plaintext bytes.

- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **RED test (`cache_test.go`):** `TestLoad_CacheVersionBumpedTo070` — arrange a cache written by the
  **old (0.6.0) encoding**: hand-build a `CacheFile{Version: "0.6.0", …}` and write it via
  `json.MarshalIndent` (a genuine pre-24 on-disk artifact), then `Load` at the new `0.7.0` version and
  assert `loaded == nil` and `len(stale) > 0` (version mismatch → full rebuild). Also assert
  `cacheFormatVersion == "0.7.0"` after the bump (a small pinning assertion).
- **GREEN:**
  - Change `const cacheFormatVersion = "0.6.0"` → `"0.7.0"`.
  - In `saveIndex` and `Save`, replace `json.MarshalIndent(cache, "", "    ")` with
    `encodeCache(cache)`.
- **MIGRATION (required — shared test-fixture contract change):** the three pre-existing version-bump
  tests and `TestLoad_FormatVersionMismatch` do
  `strings.Replace(content, cacheFormatVersion, oldVersion, 1)` on the **raw file bytes** to fabricate
  an old cache — that only works while Save writes plaintext. After this task Save writes gzip, so
  those `strings.Replace` calls will no longer find the version substring and the tests will silently
  stop testing what they claim (or fail). **Rewrite each to fabricate the old cache the robust way:**
  build a `CacheFile{Version: "<old>"}` in Go and write it via `json.MarshalIndent` (plaintext is fine
  — Load handles it, and these tests only care that an old *version string* is rejected), rather than
  `strings.Replace`-ing the bytes of a freshly-`Save`d file. Consumers to migrate:
  `TestLoad_CacheVersionBumpedForDataAccessRefactoring`, `TestLoad_CacheVersionBumpedForHostVarRefs`,
  `TestLoad_CacheVersionBumpedForStructure`, `TestLoad_FormatVersionMismatch`.
- **DoD:**
  - [ ] `cacheFormatVersion == "0.7.0"`.
  - [ ] `saveIndex` and `Save` both emit gzip (verifiable: first two bytes of the written file are
        `0x1f 0x8b`).
  - [ ] All four migrated version-bump tests pass and still assert version-mismatch → rebuild.
  - [ ] Every existing `TestSave_Load*` round-trip test in `cache_test.go` still passes unchanged
        (they call Save then Load — both now gzip — so they exercise the full new path end-to-end).
  - [ ] `just verify` green.

---

### T4 — Corrupt / truncated / unexpected-encoding cache → full rebuild, no panic (Story 3 AC2, FR-43)

**Goal:** the load-bearing robustness test. A corrupt **compressed** cache (gzip magic present but body
truncated/garbage) must make `Load` return an error (routing `BuildWithCache` to a cold rebuild) and
never panic. The plan explicitly calls for "a regression test [that] covers a corrupt compressed cache."

- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **RED test (`cache_test.go`):** `TestLoad_CorruptCompressedCache` — three sub-cases, each writes a
  bad cache file, calls `Load`, and asserts **no panic** and that `BuildWithCache`'s contract is
  satisfiable (either a non-nil `err` OR `idx == nil` — both route to a cold rebuild at index.go:654/659):
  1. **gzip magic + truncated body:** write `[]byte{0x1f, 0x8b, 0x08, 0x00, 0x01, 0x02}` (looks gzip,
     body invalid) → expect a decode error, no panic.
  2. **gzip magic + valid gzip of non-JSON:** gzip-compress the literal `not json at all` → gunzip
     succeeds but `json.Unmarshal` fails → error, no panic.
  3. **plaintext garbage (no gzip magic):** write `{ this is not json` → plaintext-branch unmarshal
     fails → error, no panic (guards the legacy-encoding branch too).
- **GREEN:** likely already satisfied by T1's `decodeCache` returning the gzip/unmarshal error and T2
  routing it to `(nil, nil, err)`. If any sub-case panics (e.g. `gzip.NewReader` on truncated input),
  wrap the failure into a returned error rather than letting it propagate. No new production behavior
  expected beyond honest error return.
- **Also add** an integration-level assertion (in `index_test.go` or via the existing `BuildWithCache`
  test surface if one exists — otherwise a `workspace`-package test): write a corrupt compressed cache
  at `root/cfg.Cache.Path`, run `BuildWithCache`, assert it returns a **valid non-empty index** (cold
  rebuild) with no error — proving the end-to-end FR-43 path, not just `Load`'s return value.
- **DoD:**
  - [ ] `TestLoad_CorruptCompressedCache` covers truncated-gzip, valid-gzip-of-garbage, and
        plaintext-garbage; none panic; each yields the rebuild signal.
  - [ ] `BuildWithCache` over a corrupt compressed cache produces a full rebuild, no error surfaced to
        the caller.
  - [ ] `just verify` green.

---

### T5 — Measure & record: cache size (≥10×) + warm-start latency (≤ old) + save time — `just bench`, NOT gated

**Goal:** produce the recorded evidence for S1 AC1/AC3 and S2 AC1/AC2. Mirror feature 22's
measure-and-record pattern: add a bench, run it across tiers, write the numbers into a `## Results`
section of `plan.md`. **`just bench` is off `just verify`** (the `bench` build tag is off by default),
so these are recorded figures, not CI gates.

- **Agents:** no TDD loop (measurement task). Add bench code, run, record.
- **New bench file `internal/workspace/bench/cache_size_test.go` (`//go:build bench`, `package bench`):**
  `BenchmarkCacheSize` — for each tier: `buildCorpus(b, t.objects)`, run one `BuildWithCache` to write
  the **new-format** cache, `os.Stat` the cache file → record `ReportMetric(bytesPerFile, "bytes/object")`
  and total bytes. To get the **before** number, also serialize the same built index via
  `json.MarshalIndent` in-memory (or gunzip-then-re-indent) and record its byte size → compute and
  `ReportMetric(ratio, "compaction-x")`. Assert (a **generous** sanity gate, not a tight one) that the
  ratio is ≥ some floor (e.g. `> 3`) so a regression that silently disables gzip is caught; the ≥10×
  headline stays a recorded figure per the plan.
- **Warm-start** is already covered by the existing `BenchmarkWarmStart_fullhit`/`_partial` — no new
  bench needed; just **re-run and record** the new-format ns/op and contrast against the pre-change
  figure (obtain the "old" figure by running the bench on the tip-of-`main`/pre-T3 commit, or note it
  from feature-22's recorded Results). **Save time:** `BenchmarkColdIndex` (which writes the cache)
  already covers the write path; record its ns/op delta to confirm compression is not pathologically
  slow (S2 AC2). Note OQ-2 (compression level) may be tuned here based on the save-time number.
- **Record** in a new `## Results` section in `plan.md`: tier, object count, old bytes/object, new
  bytes/object, compaction ratio, warm-start ns/op old vs new, cold/save ns/op old vs new, machine +
  date + `benchSeed`. Run at ≥1 large tier (`BENCH_CORPUS_OBJECTS=10000` or `30000`) so the ratio is
  representative of the reported 7,790-file scale.
- **DoD:**
  - [ ] `BenchmarkCacheSize` exists under the `bench` tag, records bytes/object + compaction ratio, and
        carries only a generous floor assertion.
  - [ ] Recorded compaction ratio at ≥1 tier is **≥10×** (Story 1 AC1) — written into `## Results`.
  - [ ] Recorded bytes/object across ≥3 tiers shows roughly linear scaling (Story 1 AC3).
  - [ ] Recorded warm-start ns/op is **≤ old** at the measured tiers (Story 2 AC1); save/cold ns/op not
        pathologically worse (Story 2 AC2).
  - [ ] `plan.md` gains a `## Results` section with machine/date/seed and old-vs-new numbers.
  - [ ] `go build -tags bench ./...` compiles (the bench file builds); `just verify` unaffected.

---

### T6 — Corpus-level round-trip fidelity (save→load→compare over a real built index) — Story 1 AC2

**Goal:** the plan's AC2 asks for fidelity "over the corpus," not just a hand-built struct (T1/T2 cover
the struct level). Prove that a **real analyzer-built index** survives save→load with identical
behavior. This is a normally-built (non-bench) test so it runs under `just verify`, using a small
committed fixture set rather than the large synthetic corpus.

- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor` (expected GREEN on arrival — the encoding is
  lossless — but written test-first so it is a genuine regression guard).
- **RED test (`internal/workspace/cache_test.go` or `index_test.go`):** `TestBuildWithCache_RoundTripFidelity`
  — point `BuildWithCache` at a small existing fixture workspace (reuse
  `internal/workspace/testdata/resolution/` or a minimal handful of `.NSx` files with edges +
  definitions + a subroutine so `Structure` is non-trivial), let it write the new-format cache; then
  build a **second** index by loading that cache (`BuildWithCache` again over the same unchanged root →
  a full warm hit, `staleCount == 0`), and assert the two indices are equivalent: for every key,
  `reflect.DeepEqual` on the `FileAnalysis` (ObjectType, Edges, DataAccess, Definitions, WorkFiles,
  HostVarRefs, Structure). Optionally also run `Resolve` on both and compare `ResolutionSet` outcomes
  (the plan lists "resolution" among the equivalence dimensions).
- **GREEN:** no production change expected — the encoding is lossless. If a discrepancy surfaces (e.g. a
  `nil` vs empty-slice difference introduced by JSON round-trip that `reflect.DeepEqual` catches), fix
  it in `decodeCache`/`Load` normalization, or relax the comparison to the behavioral fields the
  providers actually read (document the choice).
- **DoD:**
  - [ ] A real built index round-trips through the new cache format with identical per-file analysis
        (and, if included, identical resolution outcomes).
  - [ ] The warm reload reports `staleCount == 0` (proves the cache was hit, not silently rebuilt).
  - [ ] Uses a small committed fixture (no dependency on the `bench`-tagged corpus).
  - [ ] `just verify` green.

---

### T7 — `FuzzLoadCache`: never panic on arbitrary cache bytes (FR-43 hardening)

**Goal:** match the project convention (every risky entry point has a fuzz target — `FuzzParse`,
`FuzzResolve`, `FuzzProcessFile`, etc.). `Load`/`decodeCache` now consume attacker/garbage-shaped bytes
(gzip streams, truncated data), so a fuzz target that asserts "never panics, always returns" closes the
robustness gap the plan flags for corrupt caches.

- **Agents:** `tdd-red` (author the fuzz target) → `tdd-green` (any fix if a seed panics) → `tdd-refactor`.
- **New (`cache_test.go` or `cache_fuzz_test.go`):** `FuzzLoadCache` — seed corpus includes: valid
  gzip-of-valid-JSON, gzip magic + truncated body, plaintext valid JSON, plaintext garbage, empty
  input, `[]byte{0x1f,0x8b}` alone. Body writes the fuzz bytes to a temp cache file and calls `Load`
  (with an empty `currentHashes` and a nil logger); asserts it **returns** (no panic) — the return
  values may be any combination, the invariant is only non-panic (FR-43). A quick seeded run
  (`go test -run FuzzLoadCache` executes the seeds) is part of `just verify`; extended fuzzing is
  manual.
- **DoD:**
  - [ ] `FuzzLoadCache` exists with a seed corpus covering gzip/plaintext/truncated/empty.
  - [ ] Seeds pass (no panic) under `just verify`.
  - [ ] `just verify` green.

---

### T8 — Docs sync (as-built)

**Goal:** keep `CLAUDE.md` "Project state" and any cache-format mention in `README.md` matching what
shipped, per the "Docs track as-built" rule. (`/finalize-feature` performs the sync, but the planner
records what must change so it is not missed.)

- **Agents:** none (doc edit, done during finalize).
- **DoD:**
  - [ ] `CLAUDE.md` records feature 24: cache is now compact JSON + gzip, `cacheFormatVersion` bumped
        `0.6.0` → `0.7.0`, no `internal/model` change, gzip-magic-sniffing Load with plaintext
        fallback, and the recorded compaction/warm-start figures (or a pointer to `## Results`).
  - [ ] Any README line stating the cache is "indented JSON" (README "Workspace configuration"
        section, if present) is updated.
  - [ ] The feature memory note is added on finalize.

---

## Reviews required (for `/review-feature`)

- **review-code / review-tests:** confirm the encoding change is isolated to `cache.go`
  (encode/decode helpers + three call sites + one const), that **no `internal/model` type and neither
  `CacheFile` nor `cacheEntry` changed shape** (Story 3 AC3), and that the migrated version-bump tests
  still genuinely assert version-mismatch → rebuild (not accidentally weakened by the fixture rewrite).
- **review-robustness:** verify the corrupt/truncated/unexpected-encoding paths (T4) and the fuzz
  target (T7) actually route to a cold rebuild and never panic; verify `gzip.NewReader`/`ReadAll`
  errors are all captured, not propagated as panics.
- **review-concurrency:** low surface — `saveIndex` runs on the feature-21 background build goroutine
  as before; confirm the gzip buffering introduces no shared state (each call uses a local
  `bytes.Buffer`).
- **review-docs:** confirm T8 sync landed and no doc still describes the cache as indented JSON.
- **Performance evidence:** confirm the `## Results` section (T5) is filled with old-vs-new numbers at
  ≥1 representative tier and that the ≥10× claim is backed by a recorded figure.

---

## Open questions

1. **Gzip compression level.** `gzip.NewWriter` defaults to `DefaultCompression` (level -1 ≈ 6). Save
   runs off the request path (feature-21 background goroutine), so `BestCompression` (9) is defensible
   for the smallest file; `BestSpeed` (1) minimizes save latency at a modest size cost. **Recommend
   starting at `DefaultCompression` and revisiting only if T5's recorded save-time or size number
   argues otherwise.** Decide from T5 data. (Affects T3/T5.)
2. **Keep the legacy `Save` on the new format, or leave it plaintext?** `Save` is test-only
   (CWD-relative, not on the server path). Plan chooses to move **both** `Save` and `saveIndex` to
   `encodeCache` (T3) so the test round-trips exercise the real new path. Alternative: leave `Save`
   plaintext and only convert `saveIndex`. **Recommend converting both** (keeps one encoder, and the
   existing `TestSave_Load*` tests then cover the production encoding for free). Confirm at review.
3. **How should `Load` signal "corrupt → rebuild"?** The current contract already gives two channels:
   `(nil, nil, err)` for read/parse failure and `(nil, allStale, nil)` for version mismatch, both
   handled at index.go:654/659. Plan keeps a decode/parse failure on the **error** channel
   (`(nil, nil, err)`) — matching how the pre-change `json.Unmarshal` failure behaved — so no
   caller-side change in `BuildWithCache` is needed. Confirm no caller other than `BuildWithCache` and
   the tests relies on the old exact tuple. (Affects T2/T4.)
4. **Deep-compaction fallback (enum-int / gob / binary).** Explicitly **out of scope** unless T5's
   recorded numbers fall short of ~10× at the large tier. The plan documents this as a data-driven
   fallback; if pursued it would require an `internal/model`/enum-encoding change and must be
   re-planned (it would violate Story 3 AC3's "no model change" — a separate feature). No task here
   pursues it; flagged so measurements decide.
5. **Fixture choice for T6.** Reuse `internal/workspace/testdata/resolution/` (already has cross-file
   edges) vs. author a minimal purpose-built set with a non-trivial `Structure` tree. Recommend
   reusing `resolution/` if it yields non-empty `Structure`/`Definitions`; otherwise add ~3 small
   `.NSx` files. Confirm at implementation.
