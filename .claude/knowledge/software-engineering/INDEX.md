# Software Engineering Knowledge Base — Index

Cross-cutting engineering reference for `natural-lsp`, maintained by the `software-engineer-expert`
agent. Scope: the **LSP protocol** this server must honor, this project's **architecture decisions**
(an ADR log), the **testing strategy**, and general **engineering principles**. Go-syntax craft lives
in the `go-development` skill; Natural-language facts live in `.claude/knowledge/natural/`. Read this
index first, then the relevant topic.

**Status legend:** `verified (date)` = corroborated against an authoritative source (for project
decisions, the repo's own README/PRD/CLAUDE are authoritative) · `needs-verification` = seeded
belief, confirm before relying on it · `unverified` = recorded but unconfirmed.

## Topics

| File | Covers | Overall status |
|------|--------|----------------|
| [lsp-protocol.md](lsp-protocol.md) | JSON-RPC base, lifecycle, capabilities per method, position-encoding negotiation, sync kind, ranges, push-vs-pull diagnostics, `$/cancelRequest`→context | verified (2026-06-21) (LSP 3.17 spec) |
| [architecture-decisions.md](architecture-decisions.md) | ADR log: parser-based extraction (ADR-015 supersedes ADR-001), Analyzer seam, extraction↔resolution split, cache invalidation, position encoding, sync kind, transport lib, hash, index concurrency model, parser fuzzing, push diagnostics, in-memory line-width table (ADR-025), cached name index (ADR-026), canonical forward-slash index keyspace / `NormalizeKey` (ADR-027, Windows fix) | verified (2026-06-21) (internal docs + Go KB) — ADR-001 superseded 2026-06-21 |
| [testing-strategy.md](testing-strategy.md) | Pyramid, table-driven, testdata fixtures, golden files (+determinism contract), Analyzer-seam fakes, fuzzing the parser | verified (2026-06-20) (internal docs; Go-fuzz fact: go.dev) |
| [engineering-principles.md](engineering-principles.md) | SOLID, DRY/YAGNI/KISS, quality gates, reviews | verified (2026-06-20) (recognized literature + NFRs) |

## Open questions (to verify on next relevant task)

- ~~`codeLens/resolve` — will codeLenses be resolved lazily (`CodeLensOptions.resolveProvider: true`) or
  computed eagerly?~~ **Resolved (2026-06-23):** eager resolution (`resolveProvider: false`) for v1 —
  lenses are simple counts/summaries from the index, computation is cheap, and lazy resolution adds
  complexity without benefit for this scope. See `lsp-protocol.md` for details.

## Changelog

- 2026-07-20 — **architecture-decisions.md**: **ADR-027 refined** (two review findings on branch
  `fix/windows-path-separator-index-keys`). **Finding 2 (cache self-heal):** `internal/workspace/cache.go`
  `Load` now routes every stored entry key (and the content-hash-comparison key, and version-mismatch stale
  keys) through `NormalizeKey` **at load time**, so a pre-fix Windows backslash key becomes the canonical
  forward-slash key on load → the scan loop's forward-slash `currentHashes` lookup HITS (warm hit / re-analyze),
  the old cache upgrades **in place** (corrected the prior "one-time full rebuild" wording), and there is **no
  orphaned backslash entry** (which in a flat namespace would have doubled an object → a permanent spurious
  ambiguity). New regression `TestLoad_CanonicalizesBackslashKeys` (hand-serialized backslash-key cache;
  canonical-key + single-candidate assertions; proven load-bearing). **Finding 1 (relocation):** moved
  `NormalizeKey` from `internal/workspace/paths.go` into a new stdlib-only **leaf package `internal/paths`**
  (chosen over polluting `model`), so `internal/document` no longer imports `internal/workspace`
  (`go list -deps` cycle-free; document now imports only config/model/paths). All importers
  (workspace, document, server) + `TestNormalizeKey` moved to `internal/paths`. No `internal/model` change;
  cache FORMAT/version still `0.6.0` (only cache CONTENT keys become canonical).
- 2026-07-20 — **architecture-decisions.md**: recorded **ADR-027** (branch
  `fix/windows-path-separator-index-keys`) — a Windows-only correctness fix. The index/resolution/
  line-width/content-hash keyspace was inconsistent across OS: producers used `filepath.Rel` raw
  (backslash keys on Windows) while the server's `uriToRelPath` normalized to forward slashes, so
  `idx.Get`/`res.Get` MISSED for any subdirectory file on Windows → definition/references/module-hover
  silently returned empty (masked on macOS/Linux where `filepath.Rel` yields `/`). Fix: ONE exported
  canonical normalizer `workspace.NormalizeKey(rel) = strings.ReplaceAll(rel, "\\", "/")` (NOT
  `filepath.ToSlash`, which is a no-op on backslashes off-Windows and untestable on CI), routed through
  every `filepath.Rel`→key producer and lookup (workspace index.go, document sync.go/store.go, server
  definition.go/server.go/diagnostics.go + the defensive index-path comparisons). Cache self-heals on
  first post-fix Windows run (backslash keys treated as changed → one-time full rebuild → rewritten
  canonical); **no cache-format bump (0.6.0), no model change.** Platform-independent regression tests
  (literal-backslash `TestNormalizeKey` + subfolder-build invariant + Windows-mismatch simulation),
  proven load-bearing by neutering `NormalizeKey` → tests fail → restore → pass.
- 2026-07-18 — **architecture-decisions.md**: recorded **ADR-026** (feature 22 T7 / OQ-E, NFR-3) —
  cached the name→[]Candidate map on `workspace.Index`, invalidated (set nil) on `Add` (the sole
  `idx.entries` mutator; `Invalidate` is read-only). Turns `NamesWithPrefix` (completion, per-keystroke)
  and `LookupByName` (definition/hover) from O(files)-per-call into a warm map read. Deadlock-free via
  **double-checked locking** (release RLock → take Lock → re-check → build from `idx.entries` → publish),
  because Go's RWMutex has no lock-upgrade; cache read only under RLock, written only under Lock. Keyed
  on `*config.Config` identity (cfg is session-fixed). Before/after (4k objects, warm): NamesWithPrefix
  3.63 ms→41 µs/query (~87×, 12,082→17 allocs, 2.26 MB→81 KB); LookupByName 2.96 ms→30 ns (~97,000×,
  now O(matches), flat across tiers). In-memory-only — **no cache bump (0.6.0), no model change, no seam
  change.** Critical guard: an invalidation test (fails when invalidation removed) proving a mutation is
  never served stale.
- 2026-07-18 — **architecture-decisions.md**: recorded **ADR-025** (feature 22 T8 / OQ-B B-i, NFR-3) —
  eliminated the `workspace/symbol` & `references` per-query full-workspace `os.ReadFile` sweep by keeping
  an **in-memory, encoding-agnostic per-file line-width table** on `workspace.Index` (ASCII fast path:
  ASCII lines store only a byte length, retain no bytes; only non-ASCII lines keep raw bytes for exact
  UTF-16 surrogate counting). In-memory-only — **no cache bump (0.6.0), no model change, no seam change.**
  Populated inline where content is in hand (build scan loop, applyDocumentChange, replay) + a one-time
  `ensureLineWidths` sweep at warm cache load (amortized, not per-query). New `Index.ForEachWithRange`
  hands each callback a disk-free `RangeConverter` under a single RLock (avoids a nested-RLock deadlock
  hazard under writer contention). Before/after (4k objects): workspace/symbol 44.6 ms→0.97 ms (~46×),
  references 49 ms→1.46 ms (~34×), ~7× less memory each; heap-per-object stayed flat (NFR-4 band held).
  Correctness guards are untagged (`just test`): line-width-table-vs-UTF-16-oracle + byte-identical
  provider output incl. non-ASCII/UTF-16 + files-deleted-after-build disk-free proof. `-race -count=2` clean.
- 2026-07-18 — **architecture-decisions.md**: recorded feature-22 **T5/T6 measured baselines** (Apple M4 Max,
  Go 1.26). Warm start (NFR-2) is dominated by per-file SHA-256 hashing + full-cache JSON unmarshal, NOT
  analysis — full-hit ~57–61 µs/object (≈ cold's 14–17.5 µs/object, actually worse), still sub-second at 4k
  (246 ms). Request latency (NFR-3): `workspace/symbol` ~44.6 ms and `references` ~49 ms per query at 4k
  objects (~11 µs/file, ~32k/24k allocs) — the per-query disk sweep, the T8 target (pre-fix baseline).
  **OQ-E decided: name index IS hot → do T7.** `NamesWithPrefix` (rebuilds `buildNameIndex` on every
  keystroke) = ~3.68 ms + 12k allocs + 2.26 MB/call at 4k; `LookupByName` cheaper (~2.98 ms, 2 allocs,
  once-per-request). Provider baselines live in `internal/server` behind `//go:build bench` (need the
  unexported `handlerContext`); `just bench` now covers `./internal/server/...` too. NFR-8 freshness is a
  normal (untagged) unit test (`internal/workspace/freshness_test.go`) in `just test`. No model/Analyzer/cache
  change.
- 2026-07-17 — **architecture-decisions.md**: recorded **ADR-024** (feature 21 T12 / OQ-E, FR-37/FR-38/NFR-2) —
  the server now builds via `workspace.BuildWithCache(root/cfg.Cache.Path, currentHashes=nil, …)` for real warm
  starts (was always cold `Build`). Fixed two latent `BuildWithCache` defects it exposed: cold-start-with-cachePath
  produced an EMPTY index (absolute-vs-relative `staleMap` mismatch + a `cachePath==""`-gated analyze branch), and
  `BuildWithCache` never wrote the cache back. Now: hashes computed from disk when nil (content-based, FR-38);
  staleness unified on relPath (stale OR not-in-cache → analyze); root-aware `saveIndex` write-back on
  `analyzedAny || !cacheExists`; corrupt/version-mismatch → full rebuild, write failures logged-not-fatal (FR-43).
  Cache format UNCHANGED (`0.6.0`); no model/Analyzer change. Regression: `internal/server/cache_wiring_test.go`
  (cold→warm zero-reanalysis, changed-file single-reanalysis, corrupt-cache fallback+repair) via a counting
  analyzer. Also fixed a pre-existing **T4** async-timing flake (`TestLifecycleDiagnosticPublishing_DidChangeWatchedFiles_Change`
  pre-fed a watched-file change that raced the async build → converted to the pipe+`indexReadyGate` harness; fails
  with cold `Build` too, so not a cache regression). `-race -count=2` green.
- 2026-07-17 — **architecture-decisions.md**: recorded **ADR-023** (feature 21 T5/T6 / FR-32, OQ-A/OQ-C/OQ-D) —
  the async build goroutine constructs a `progressReporter` gated on the client's `window.workDoneProgress`
  capability (disabled no-op when absent → zero create/`$/progress` bytes, build still runs), and sequences
  **fire-and-forget** `window/workDoneProgress/create` → `$/progress begin` → `end` sharing one
  `natural-lsp-index` token with no response-await (the create response stays logged-only). `end` fires after
  publish+replay but before feature-20's no-usable-root `window/showMessage` (OQ-D end-first). On a
  shutdown-raced build the `bgCtx.Err()` guard skips publish AND `end` (no progress for an aborted build). No
  server capability added (like publishDiagnostics); no model/Analyzer/cache change. Regression:
  `internal/server/progress_wire_test.go` (ordering, fire-and-forget, two-branch gating) — `-race -count=2`
  green.
- 2026-07-17 — **architecture-decisions.md**: recorded **ADR-022** (feature 21 T13 / OQ-B.1) — after the
  async background build publishes, the goroutine calls `hctx.replayOpenBuffers()` to re-apply every open
  buffer's already-computed `FileAnalysis` into the freshly-published index (`idx.Add` + one `ResolveInto`
  under a single `idxResMu` lock, mirroring `applyDocumentChange`), closing the window where a `didChange`
  racing the cold build landed only in the store and index-backed providers served stale disk content. The
  load-bearing detail: `document.Store.OpenDocuments()` (new additive accessor) returns **value copies**, not
  live `*Document` pointers — returning pointers raced `Store.Update`'s in-place field reassignment
  (`-race`-caught). Replay runs before `reportNoUsableRoot`/`indexReadyHook`, so an open-buffer-only workspace
  counts as usable content. Server + document change only; no model/Analyzer/cache change.
- 2026-07-17 — **architecture-decisions.md**: recorded **ADR-021** (feature 21 T4 / NFR-5) — the initial
  index build now runs on a background goroutine (tracked by a `sync.WaitGroup`, tied to `bgCtx`) so the
  serial dispatch loop stays responsive during a cold build; the goroutine publishes under `idxResMu`
  (F7), guards `bgCtx.Err()` before publish (no publish-after-shutdown), fires `indexReadyHook` last as
  the test-sync point, and is **joined** in `Run`'s deferred cleanup (`bgCancel(); bgBuild.Wait()`
  registered after `defer stream.Close()`) so it never leaks or writes after `Run` returns. Documented
  that `stream.Write` from the goroutine is safe because `jsonrpc2.headerStream.Write` serializes frames
  under its own `writeMu` (verified in `framer.go@v1.0.0`). Server-only change: no `internal/model`,
  `Analyzer`-seam, or cache-format change.
- 2026-07-17 — **architecture-decisions.md**: recorded **ADR-020** (feature 21 T11 / OQ-F) — `workspace.Build`
  and `BuildWithCache` gained a leading `ctx context.Context`; the per-file scan loop checks `ctx.Err()`
  once per file and returns `(nil, ctx.Err())` on cancel (partial index discarded). Workspace-package API
  change (review-seam), but Analyzer interface / `internal/model` / cache format `0.6.0` all unchanged.
- 2026-06-23 — Full LSP 3.17 verification sweep. **lsp-protocol.md**: verified all claims against the
  live spec at https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/;
  added explicit source citations for position encoding (Section 5.3, Section 6.1.1) and diagnostics
  (Section 18); resolved the codeLens/resolve open question (eager resolution for v1). **INDEX.md**:
  marked codeLens/resolve as resolved. All other topics (`architecture-decisions.md`, `testing-strategy.md`,
  `engineering-principles.md`) remain `verified` — no changes needed.
- 2026-06-21 (addendum) — ADR-010 user sign-off received: Option A (`go.lsp.dev/protocol` +
  `go.lsp.dev/jsonrpc2`) accepted with full awareness of the transitive json/v2 dependency. Pending
  decision removed; ADR-010 re-evaluation block updated to record sign-off. HIGH open question cleared.
- 2026-06-21 — Full verification sweep. **lsp-protocol.md**: re-confirmed the position-encoding
  default (`"If omitted it defaults to 'utf-16'."`), the mandatory-UTF-16 baseline,
  `general.positionEncodings` negotiation, and the no-encodings→`utf-16` rule verbatim against the
  live LSP 3.17 spec (→ `verified (2026-06-21)`); expanded the diagnostics section with the **push
  vs. pull** model (pull = `diagnosticProvider`/`textDocument/diagnostic`/`workspace/diagnostic`,
  motivation = client-controlled timing); narrowed the negotiated-encoding plumbing open question to
  "ordinary capabilities field, no library magic." **architecture-decisions.md**: recorded **ADR-014**
  (push diagnostics for v1 — resolves the prior push-vs-pull open question with a dated rationale);
  **downgraded ADR-010 to provisional** after the go-improve sweep found `go.lsp.dev/protocol@v1.0.0`
  pulls experimental json/v2 (`go-json-experiment/json`) transitively, contradicting the project's
  json/v2-avoidance stance — flagged as a human-in-the-loop dependency sign-off, not silently
  reversed. **Open questions**: closed pull-diagnostics (→ADR-014) and negotiated-encoding plumbing
  (resolved in `lsp-protocol.md`); promoted the ADR-010 sign-off to the top HIGH open question;
  retained codeLens/resolve. testing-strategy.md and engineering-principles.md re-reviewed, no change
  needed (still `verified`).
- 2026-06-20 — Go-pattern boundary review (SE vs. Go KB vs. go-development skill). Added SE-level
  testing patterns that sit at the LSP-server↔Go seam: **Analyzer-seam fake testing** (test
  `internal/server` against a fake Analyzer, per ADR-002), **golden-file testing with a determinism
  contract** (sorted/stable output — also a cache/lsp-graph requirement), and **fuzzing the extractor**
  as the FR-43 "never panic" guard (corpus committed under `testdata/fuzz/` as a regression seed) — all
  in `testing-strategy.md`. Added the **`$/cancelRequest`→`context` cancellation contract** and error
  codes `-32800`/`-32801` to `lsp-protocol.md` (verified vs. LSP 3.17). Recorded **ADR-012** (index
  concurrency: snapshot-on-read + bounded worker pool) and **ADR-013** (fuzz the extraction entry
  point). Reinforced `-race`/`vet`/`gofmt` as **enforced CI gates** in `engineering-principles.md`. Go
  *mechanics* of fakes/golden/fuzz/worker-pool deliberately left to the Go KB / skill (routed, not
  duplicated).
- 2026-06-20 — Verification sweep across all topics. **lsp-protocol.md**: verified base framing,
  lifecycle, half-open zero-based ranges, `TextDocumentSyncKind` enum, position-encoding default
  (UTF-16) + `general.positionEncodings`/`positionEncoding` negotiation, and exact per-method
  ServerCapabilities field names/types, all against the **LSP 3.17** spec → status `verified`.
  **architecture-decisions.md**: recorded ADR-008 (negotiate UTF-8, default UTF-16), ADR-009 (Full
  document sync for v1), ADR-010 (`go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` v1.0.0), ADR-011
  (`crypto/sha256` cache key); cleared all three Pending decisions. **engineering-principles.md**:
  grounded SOLID / Go proverbs / DRY in primary sources → status `verified`. Replaced the three
  resolved open questions with three new lower-risk ones (pull diagnostics, codeLens resolve, how the
  chosen lib exposes negotiated encoding).
- 2026-06-20 — (seed) Created index and four topics.