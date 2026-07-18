# Architecture decisions (ADR log)

**Status:** verified (2026-06-23) against the repo's own README.md, docs/plans/natural-lsp-prd.md, and
CLAUDE.md — these are the authoritative source for project decisions. Append a new dated entry
whenever a significant decision is made; never silently reverse one — supersede it with a new entry.
2026-06-23 sweep: added ADR-016 (CodeLens eager resolution for v1).
2026-06-21: Added ADR-014 (push diagnostics for v1) and downgraded ADR-010 to provisional
pending user sign-off on its transitive json/v2 dependency.
2026-06-21: ADR-010 sign-off received — user accepted Option A (go.lsp.dev/protocol + jsonrpc2) with
full awareness of the transitive json/v2 dependency. Pending decision removed.
2026-06-21: ADR-001 superseded by ADR-015 (parser pivot); ADR-007 simplified (lsp-graph removed).

## ADR-001 — ~~Regex-based extraction~~ **(superseded by ADR-015)**
**Original decision:** Extract Natural constructs with tuned regexes rather than a full parser/grammar.
**Superseded:** The project pivoted to a hand-written parser (ADR-015). The regex approach was the
original design; it is recorded here for history. Go KB `regexp-and-extraction.md` documents RE2
mechanics but is now reference-only.

## ADR-015 — Hand-written parser, not regex (2026-06-21)
**Decision:** Extract Natural constructs via a hand-written lexer and recursive-descent parser,
modeled on [natls](https://github.com/MarkusAmshove/natls) (the Java reference implementation).
**Rationale:** (1) natls demonstrates that a full parser for Natural is achievable and the reference
implementation is openly available (MIT); (2) a parser enables completion, signature help, call
hierarchy, real syntax diagnostics, and accurate symbol tables — features regex cannot deliver
reliably; (3) the original regex rationale ("no mature grammar exists") no longer holds when a
reference parser can be studied directly. **Consequence:** Two gap types are still handled separately:
unresolvable references (e.g. `CALLNAT #VARIABLE`) are noted with call-site context rather than
discarded; parse errors are surfaced as LSP diagnostics. `CALLS_DYNAMIC` as a named edge type is
removed — dynamic calls are simply unresolvable calls. **See:** Natural KB `natls-prior-art.md` for
the reference parser study; ADR-002 (Analyzer seam remains unchanged).

## ADR-002 — Analyzer interface as the replaceable-backend seam
**Decision:** LSP-facing code depends only on `internal/analysis.Analyzer` + `internal/model`, never
on the parser backend in `internal/analysis/natural`. **Rationale:** the backend can later become a
tree-sitter grammar or other implementation without touching the LSP layer.

## ADR-003 — Extraction and resolution are separate steps
**Decision:** Per-file extraction produces *unresolved* references with caller context; cross-file
**resolution** (`internal/workspace/resolution.go`) binds them via the library/steplib chain.
**Rationale:** keeps the highest-risk logic (resolution) out of the regex backend and behind the
index. (Added when the README architecture was aligned with the PRD.)

## ADR-004 — Module resolution follows the steplib chain, not file paths
**Decision:** Resolve CALLNAT/PERFORM/FETCH targets current-library → ordered steplibs → SYSTEM,
config-driven. With no library map, treat the workspace as one flat namespace and emit an ambiguity
diagnostic. **Rationale:** the same module name can exist in multiple libraries; search order
disambiguates. Names are not globally unique.

## ADR-005 — Cache invalidation by content hash, not mtime
**Decision:** Invalidate the on-disk index cache on file **content hash**; a cache-format version
forces a full rebuild. **Rationale:** mtime breaks across git checkouts. (Hash algorithm decided in
ADR-011: `crypto/sha256`.)

## ADR-006 — Filesystem-scoped to NaturalONE/SPoD `.NSx` files
**Decision:** Operate on exported object files, not mainframe-resident objects. The indexed extension
set maps to constructs and must stay in sync with the features that consume each type.

## ADR-007 — Batch export dropped from scope
**Decision:** No batch/bulk export feature. **Rationale:** the server is interactive/editor-driven.

## ADR-008 — Position encoding: negotiate UTF-8, default to UTF-16 (2026-06-20)
**Decision:** Advertise `general.positionEncodings`-aware behavior: pick **UTF-8** when the client
offers it (return `positionEncoding: "utf-8"` in `ServerCapabilities`), otherwise fall back to the
mandatory **UTF-16** baseline. Centralize the byte/rune↔LSP-column conversion in one place keyed off
the negotiated encoding. **Rationale:** Go source is held as UTF-8 bytes/runes; serving UTF-8 columns
when the client supports them avoids the UTF-16 surrogate conversion entirely for those clients, and
Natural source is overwhelmingly ASCII (UTF-8 and UTF-16 columns coincide except on non-ASCII lines),
so correctness risk is confined to multibyte literals/comments — handled by the one conversion point.
UTF-16 must remain implemented because it is the spec default and the only encoding a client lacking
`positionEncodings` accepts. **Alternatives considered:** (a) UTF-16 only — simplest to advertise but
forces surrogate math on every range even though most clients (incl. VS Code) now offer UTF-8;
(b) UTF-8 only — non-conformant, breaks clients that don't offer UTF-8. **Source:** LSP 3.17 spec,
see `lsp-protocol.md`.

## ADR-009 — Document sync kind: Full for the first release (2026-06-20)
**Decision:** Advertise `TextDocumentSyncKind.Full` (with `openClose: true`) initially; revisit
`Incremental` only if profiling shows full-text `didChange` payloads are a bottleneck.
**Rationale:** Natural objects are small single files; full-document sync is far simpler and removes a
whole class of range-application bugs (incremental requires correctly applying `TextDocumentContent-
ChangeEvent` ranges in order). The analyzer already re-extracts whole files, so incremental sync would
yield no analysis-side win. **Alternatives considered:** `Incremental` (2) — less data on the wire but
more complex and error-prone, unjustified for small files. **Source:** LSP 3.17 spec, `TextDocument-
SyncKind`; CLAUDE.md note that full is simpler.

## ADR-010 — LSP transport/types: depend on `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` (2026-06-20)
**Decision:** Use `go.lsp.dev/protocol` (LSP message types) + `go.lsp.dev/jsonrpc2` (JSON-RPC 2.0
transport), both **v1.0.0**, as the default rather than hand-rolling JSON-RPC framing and message
types. The dependency lives **behind the `internal/server` boundary** and must not leak into
`internal/analysis` or `internal/model` (preserves the Analyzer seam, ADR-002). **Rationale:** both
modules are at a freshly stabilized v1.0.0 and actively maintained (verified via the Go module proxy
in Go KB `lsp-go-ecosystem.md`); lowest implementation cost for the project's small method set, and
the 1.0 tag limits churn. **Alternatives considered:** (a) hand-roll minimal JSON-RPC + only the LSP
types used — maximum control, smallest dependency surface, but more code and ongoing spec-tracking;
(b) `tliron/glsp` — pre-1.0 and framework-heavy for our handful of methods; (c) `sourcegraph/go-lsp`
— **archived, rejected**. The hand-roll path (a) remains the fallback if the ~22 transitive deps of
`go.lsp.dev` become a concern. **Source:** Go KB `lsp-go-ecosystem.md` (verified 2026-06-20).

> **Re-evaluation 2026-06-21 — signed off by user 2026-06-21.** The 2026-06-21 go-improve sweep
> found that `go.lsp.dev/protocol@v1.0.0` transitively pulls **`github.com/go-json-experiment/json`**
> (the standalone experimental json/v2 module) as a hard runtime dependency, whose README warns
> "Do not depend on this in publicly available modules." The project's json/v2-avoidance stance was
> noted and the trade-off was presented; the user explicitly **accepted Option A** with full awareness
> of this dependency. The standing "avoid json/v2" note in `stdlib-for-lsp-server.md` applies to
> *direct* adoption in project code — not to transitive dependencies of an otherwise-appropriate
> library. The `go.lsp.dev/*` dependency remains confined behind `internal/server` (ADR-002).
> **Sources:** Go KB `lsp-go-ecosystem.md` and `stdlib-for-lsp-server.md` (verified 2026-06-21).

## ADR-011 — Cache-key content hash: `crypto/sha256` (2026-06-20)
**Decision:** Key the on-disk index cache (ADR-005) on **`crypto/sha256`** of file content.
**Rationale:** the cache key must be **deterministic and stable across program runs and git
checkouts** (FR-38). SHA-256 is deterministic, collision-resistant, gives a zero-collision-worry
auditable key, and is fast enough for file-sized inputs. Crucially, **`hash/maphash` is unsuitable**
— its seed is random per process and not serializable, so identical content hashes differently every
run (Go KB `filesystem-and-watching.md`). **Alternatives considered:** `hash/fnv` (FNV-1a 64) — also
deterministic/stable and faster, acceptable if profiling later shows hashing is hot, but trades
collision margin for speed with no present need; `hash/maphash` — rejected (non-serializable seed).
**Source:** Go KB `filesystem-and-watching.md`; `hash/maphash` godoc (verified 2026-06-20).

## ADR-012 — Index concurrency model: snapshot-on-read + bounded worker pool (2026-06-20)
**Decision:** The workspace index is read by LSP request handlers **concurrently** with background
(re)indexing and watcher-driven updates. Adopt two structural rules: **(1) queries read an immutable
snapshot** — a handler obtains a consistent view that a concurrent update cannot tear, by swapping a
new index value/pointer in atomically (or returning copies for query results) rather than mutating a
shared map under readers; **(2) full-workspace indexing fans out over a bounded worker pool** (≈ CPU
count, e.g. `errgroup` with `SetLimit`), never one unbounded goroutine per file, with every
background goroutine tied to a shutdown-cancelled context. **Rationale:** satisfies responsiveness
(NFR-3) and no-torn-results (NFR-8) without coarse locking that would stall the request loop; bounds
memory/goroutines on large repos (NFR-4); and gives a clean shutdown path (FR-43). The race detector
(`-race`) is the standing correctness bar for any change here. **Alternatives considered:** (a) one
big `RWMutex` around a mutating index — simple but readers block during rebuild and torn-read risk
returns the moment a read spans multiple map ops; (b) one goroutine per file — simplest fan-out but
unbounded memory/goroutines on a 30k-file repo; (c) single owner goroutine + channel queries (actor)
— viable and race-free, kept as a fallback if snapshot swapping proves awkward, but adds latency and
serializes reads. **Source:** PRD NFR-3/4/8, FR-43; Go KB `concurrency-primitives.md` (errgroup
`SetLimit`, snapshot/immutable guidance) and skill concurrency reference (mechanics).

## ADR-013 — Fuzz the parser entry point as the FR-43 safety guard (2026-06-20)
**Decision:** Maintain a Go native fuzz target over the parser's entry point asserting
**"never panics on any input"** (a safety/liveness property, not output correctness). Crashers found
by fuzzing are committed under `testdata/fuzz/...` and replay under plain `go test`, becoming
permanent regression fixtures by the same rule as hand-authored `.NSx` reproducers. **Rationale:**
the parser consumes untrusted source files and FR-43 forbids any single file crashing the server;
fuzzing reaches pathological inputs no hand-written fixture would; the committed-corpus model
integrates with the existing `testdata/` regression convention at zero extra process cost.
**Alternatives considered:** hand-authored adversarial fixtures only — necessary but cannot match a
fuzzer's coverage of the malformed-input space; property-based libraries (e.g. gopter) — extra
dependency where stdlib fuzzing already fits the "no panic" property. **Source:**
https://go.dev/security/fuzz/ (native fuzzing since Go 1.18, corpus committed as regression seed;
verified 2026-06-20); `testing-strategy.md`, `engineering-principles.md` (secure-by-design).

## ADR-014 — Diagnostics delivery: push (`publishDiagnostics`) for v1 (2026-06-21)
**Decision:** Deliver diagnostics via the **push** model — the server→client
`textDocument/publishDiagnostics` notification, published after each (re)extraction — and do **not**
advertise the 3.17 **pull** `diagnosticProvider` capability for the first release. **Rationale:** the
analyzer already re-extracts whole files on every change (ADR-009 Full sync), so the server always
knows a file's complete diagnostic set at extraction time and can publish immediately; push has no
provider capability to advertise and is the least machinery. Pull's advantage is *client-controlled
timing* (compute only for visible files), which matters for expensive cross-file analysis on huge
repos — a real concern this project may hit at 30k files (NFR-4), but not one v1 needs to pay for up
front. Push and pull should not both be active for the same documents, so this is an either/or for
v1. **Alternatives considered:** (a) pull diagnostics (`diagnosticProvider` +
`textDocument/diagnostic` / `workspace/diagnostic`) — better client-side control and lazy compute,
but more protocol surface and a refresh-coordination burden, unjustified while extraction is cheap
and eager; (b) support both — needless complexity and risks double-reporting. **Revisit** if
profiling shows eager push of cross-file diagnostics is a responsiveness drag on large workspaces
(NFR-3), or if a target client supports only pull. **Source:** LSP 3.17 spec (push vs. pull
diagnostics), see `lsp-protocol.md`; PRD NFR-3/NFR-4; ADR-009 (Full sync → whole-file re-extraction).

## Pending decisions (record here when made)
<!-- none currently open -->

## ADR-016 — CodeLens resolve: eager for v1 (2026-06-23)
**Decision:** Implement CodeLens with **eager resolution** (`resolveProvider: false` or omitted in
`CodeLensOptions`) for the first release. **Rationale:** The lenses for natural-lsp are simple
counts/summaries from the index (inbound call counts, table-write summaries) — computation is cheap
and fast. Lazy resolution (`resolveProvider: true`) adds protocol complexity (additional
`textDocument/codeLens/resolve` handler, per-lens caching, client-side refresh logic) without
benefit for this scope. Eager resolution keeps the implementation minimal and predictable for v1.
**Consequence:** All CodeLens are computed during the initial index build and whenever the index
is re-built; no `codeLens/resolve` handler is needed for v1. **Revisit:** If lenses grow to
expensive computations (e.g. cross-file data-flow analysis) or profiling shows eager computation
degrades responsiveness on large workspaces. **Source:** LSP 3.17 spec (CodeLensOptions), PRD FR-29
(CodeLens summaries), project scope (v1 minimal implementation).

## ADR-017 — Editor-client integration strategy (feature 15) (2026-07-12)
**Decision:** For non-VS-Code editors the client is **configuration + docs**, not bespoke plugins,
launching the single stdio entry point `natural-lsp --stdio` and relying on the server's own
`.natural-lsp.toml` sentinel walk-up for workspace-root detection. Per-editor specifics recorded
here because they are real, easy-to-get-wrong integration facts (not Go craft):
- **Neovim** requires an explicit **filetype association** (`vim.filetype.add` glob
  `.*%.[nN][sS].` → `natural`, or a `BufRead/BufNewFile *.NS*` autocmd) because Neovim has no
  built-in `.NSx` detection; the LSP client (`vim.lsp.config`/`vim.lsp.enable` on 0.11+, or
  `nvim-lspconfig`) only attaches on the `natural` filetype, so without the association nothing
  starts. Native `vim.lsp.config` is the current recommendation (Neovim 0.11+); nvim-lspconfig
  remains a supported alternative (needs 0.11.3+).
- **Zed** **cannot** bind a custom LSP to a brand-new language via `settings.json` alone —
  `file_types` only maps extensions to *existing* languages, and defining a new language + server
  binding requires a **Zed language extension**. Documented honestly: a Natural Zed extension is
  future work; `settings.json` `file_types`/`lsp`/`languages` blocks are provided as forward-ready
  scaffolding, with the stdio smoke as the automatable lower bound.
- **Helix** works fully via `languages.toml` (registers the `natural` language + all 15 file-types
  + the `natural-lsp` server); no explicit root markers needed since the server does the sentinel
  walk-up.
- **JetBrains** (incl. Community editions) via **LSP4IJ** (Red Hat), not the paid native LSP API.
  LSP4IJ maps files by **file-name pattern** (case-sensitive; e.g. `*.NSP`) with a `languageId`,
  and supports an **importable user-defined-server template** (a directory with `template.json`
  holding `serverName`/`command`/`mappings[{fileNamePatterns,languageId}]`, optional `README.md`).
  Committed at `editors/jetbrains/lsp4ij-template/` so setup is reproducible by import.
**Verification ceiling:** Full editor-GUI verification (filetype recognized + go-to-definition
navigates) is a documented **human** step and cannot be automated in this repo's CI. The
**automatable lower bound** for every editor is the server-side stdio smoke — `scripts/smoke.sh`
runs `--version` plus a minimal `initialize → initialized → shutdown → exit` Content-Length round
trip and asserts a `capabilities` result and clean exit. **Distribution (NFR-10/12/13):** the 5
cross-built artifacts (`natural-lsp-{linux,darwin}-{amd64,arm64}`, `natural-lsp-windows-amd64.exe`)
+ `checksums.txt` from `just release` already satisfy NFR-10; NFR-12 "package-style install" is met
by `go install` + pre-built binary, with a native package-manager channel (Homebrew/Scoop) noted as
future work — deliberately not built. **Source:** feature-15 `plan.md`/`tasks.md` (Stories 2–4,
FR-45/46, NFR-10/12/13); LSP4IJ `UserDefinedLanguageServer.md`
(https://github.com/redhat-developer/lsp4ij); Zed `configuring-languages.md`
(https://github.com/zed-industries/zed); Neovim 0.11 LSP docs (https://neovim.io/doc/user/lsp/).

## ADR-018 — VS Code extension: file-association casing, test tiering, engine baseline (feature 15, VS Code client)
**Status:** verified (2026-07-12) — implemented and all gates green (`npm run compile`/`lint`/`test:unit`
plus the `@vscode/test-electron` suite ran headless on macOS: 16 integration tests passing, incl. all 15
upper-case extensions and a live server launch).
**Context:** The first-party VS Code extension (`editors/vscode/`, TypeScript) launches the existing server
as `natural-lsp --stdio` over stdio — no server change, no bespoke transport. Three client-side decisions
were non-obvious:
- **File-extension casing (was OQ #5).** Mainframe/NaturalONE exports are conventionally UPPER-case
  (`.NSP`), but VS Code's language-detection path is effectively case-sensitive on
  `contributes.languages.extensions` (documented ambiguity + open VS Code issues). **Decision:** declare
  BOTH the lower-case `extensions` list AND `filenamePatterns` character-class globs matching each
  extension case-insensitively (e.g. `**/*.[nN][sS][pP]`). Verified by opening upper-case fixtures for all
  15 types in the electron suite and asserting `languageId === 'natural'`. Alternative (extensions-only)
  rejected: it silently fails to associate the exact upper-case files users actually have.
- **Test tiering.** TWO tiers: pure Mocha **unit** tests (`src/test/unit/**`, no VS Code host — cover
  `resolveServerPath` and TextMate grammar scopes via `vscode-textmate` + `vscode-oniguruma`) run
  everywhere including this sandbox; **integration** tests (`src/test/suite/**`, `@vscode/test-electron`,
  pinned VS Code `1.85.0`) cover association + a live launch and run in CI (Linux + xvfb) and here.
  Rationale: keep a fast, host-free gate that always runs; isolate the heavyweight electron download. Unit
  tests run against **compiled JS** (`tsc` → `out/`, then mocha), not ts-node — Node 25 loads bare `.ts`
  via ESM `import()` (`__dirname` undefined), which the CJS `ts-node/register` hook does not intercept;
  compile-first sidesteps the whole ESM/CJS interop question.
- **Server-binary discovery for the launch test.** The launch test reads `NATURAL_LSP_SERVER_PATH`
  (a freshly `go build`-ed binary), points `naturalLsp.serverPath` at it, and asserts the client reaches
  `State.Running`. If the var is unset/missing the test **skips** (not fails) so association tests still
  run — CI builds the Go binary and sets the var.
- **Engine baseline `^1.85.0`** (Nov 2023) — recent enough for `vscode-languageclient` v9, old enough for
  broad compatibility; the `@vscode/test-electron` download is pinned to `1.85.0` for reproducibility.
- **Graceful degradation (client-side FR-43 analogue).** A missing/unstartable server surfaces an
  actionable `showErrorMessage` (naming `naturalLsp.serverPath` + PATH) and clears the client — activation
  never throws/crashes. A `naturalLsp.restart` command is provided.
- **Packaging.** `@vscode/vsce` produces `natural-lsp-vscode-<version>.vsix` (publisher `dkrieg`,
  version tracks the server release line); `.vscodeignore` keeps the artifact lean (only `out/extension.js`,
  `out/serverPath.js`, grammar, manifest, README, LICENSE — no `src/`, tests, or node artifacts). NO
  Marketplace publish / no `VSCE_PAT`. `*.vsix`/`out/`/`node_modules/`/`.vscode-test*` are gitignored.
**Source:** feature-15 `tasks.md` T1–T4/T9; VS Code language-identifiers docs
(https://code.visualstudio.com/docs/languages/identifiers) and contribution-points reference
(https://code.visualstudio.com/api/references/contribution-points#contributes.languages), verified 2026-07-12.

## ADR-019 — Workspace root negotiated at `initialize`, not process startup (feature 20, Variant A) (2026-07-14)

**Status:** verified (2026-07-14) (feature-20 tasks.md OQ-1/OQ-4 RESOLVED; implemented in T2).

**Decision.** Root/config discovery is deferred off process startup into the LSP `initialize` request
handler. `server.Run` no longer takes a finished `root string, cfg config.Config`; its signature is now
`Run(ctx, r, w, version, cwdFallback string, az analysis.Analyzer, logger)`. Inside the `initialize`
dispatch case, after decoding params, the server computes `start := resolveRootStart(params, cwdFallback)`
(precedence: first `workspaceFolders` → `rootUri` → `cwdFallback`; `internal/server/root.go`) and runs
`config.Bootstrap(start, "", logger)` **from the client-supplied path** (OQ-4: the client path is the sole
discovery origin — the launch cwd's own sentinel is not consulted when a client root is present). The
negotiated `root`/`cfg` are stored on `handlerContext`; the `document.Store`, the `document.Watcher`, and
the `workspace.Build`/`Resolve` index build all read `hctx.root`/`hctx.cfg` instead of former startup
closure vars. The store/watcher are constructed **in the initialize handler** (no didOpen/didChange can
arrive before `initialized`, so nothing needs them earlier); the watcher is closed via a deferred closure
in `Run` that reads the possibly-updated `hctx.watcher`.

**Rationale.** The prior design fixed root/config at `os.Getwd()` before the handshake, so a client
launching the server from an arbitrary cwd (the common editor case) indexed the wrong tree and cross-file
navigation silently failed (assessment defect #2). `initialize` params (`workspaceFolders`/`rootUri`) are
the LSP-authoritative source of the workspace root and must drive discovery.

**Observable-lifecycle invariant.** No capability added; `initialize`→`initialized` ordering and the
response shape are byte-compatible with before (`handleInitialize` stays a pure encoding/capability
negotiator with no I/O). CR-6 preserved: `config.Bootstrap` never hard-fails, so a malformed
`.natural-lsp.toml` at the negotiated root logs a Warn and still returns a successful `initialize` result.
EOF-before-`initialize` runs no bootstrap and exits 0 (smoke `--stdio < /dev/null`).

**Alternatives considered.** Variant B (keep `Run(root,cfg)` and re-target the store/watcher/index lazily
after `initialize`) was rejected in favor of the cleaner deferred-resolution seam despite the wider
test-caller edit (every `Run` caller drops its `cfg` arg). Consulting the launch-cwd sentinel *in addition*
to the client path was rejected (OQ-4) — a stray sentinel in the launch dir must not override an explicit
client root.

**Source:** feature-20 `docs/plans/features/20-workspace-root-handshake/tasks.md` (T1/T2, OQ-1/OQ-4);
LSP 3.17 `initialize` (`workspaceFolders`/`rootUri`), see `lsp-protocol.md`.

## ADR-020 — Workspace build is context-cancellable; cancel discards the partial index (feature 21 T11, OQ-F) (2026-07-17)

**Status:** verified (2026-07-17) (feature-21 tasks.md OQ-F RESOLVED; implemented in T11).

**Decision.** `workspace.Build` and `workspace.BuildWithCache` gained a leading `ctx context.Context`
parameter: `Build(ctx, root, cfg, az, logger, onProgress)` and
`BuildWithCache(ctx, root, cfg, az, logger, cachePath, currentHashes, onProgress)`. The per-file scan
loop (`index.go`) checks `ctx.Err()` **once per file** at the top of each iteration; on a non-nil error
it stops immediately and returns `(nil, 0, totalFiles, ctx.Err())`. The partial index is **discarded**
(nil, not returned) — a non-nil error unambiguously means "no usable index." The server's `buildIndex`
threads its ctx through (`buildIndex(ctx, onProgress)`); the initialized handler passes the handler ctx
today and will pass `bgCtx` in T4 so shutdown aborts an in-flight build mid-scan rather than running to
completion (retiring OQ-F's original run-to-completion MVP limitation).

**Rationale.** Before this, a shutdown that raced the initial index build only skipped the *publish*;
the `workspace.Build` call still ran every file to completion (it took no ctx), wasting work and delaying
`Run`'s return on a large cold workspace. Checking `ctx.Err()` once per file is the right cadence: it is
cheap relative to per-file analysis and never becomes a throughput bottleneck (contrast a per-token or
per-line check).

**Discard-vs-partial contract.** Returning `(nil, ctx.Err())` rather than `(partialIndex, ctx.Err())` was
chosen because the only caller that cancels is the background build goroutine, which checks `bgCtx` before
`publishIndex` and skips publish on cancel — so a partial index would never be published anyway. Discarding
keeps the contract simple and prevents any caller from accidentally consuming a half-built index.

**Seam note (review-seam applies).** This is a **workspace-package API change** — the `Build` entry point
is part of the workspace API the server depends on. The `analysis.Analyzer` interface itself is untouched,
`internal/model` is unchanged, and the cache format stays `0.6.0` (only the build entry-point signature
gains a ctx). All Build/BuildWithCache callers (server `buildIndex` + ~30 workspace/server test sites)
were migrated to pass `context.Background()` (tests) / the handler ctx (server).

**Alternatives considered.** (a) Return a partial index on cancel — rejected (see contract above).
(b) Check ctx inside the WalkDir enumeration too — unnecessary; enumeration is fast and the analysis loop
is where time is spent. (c) Leave Build uncancellable and only skip publish (the original OQ-F MVP) —
rejected once OQ-F was approved: it leaves the build goroutine burning CPU past shutdown.

**Source:** feature-21 `docs/plans/features/21-async-indexing-and-progress/tasks.md` (T11, Decisions →
OQ-F).

## ADR-021 — Initial index build runs on a background goroutine, joined at Run exit (feature 21 T4, NFR-5) (2026-07-17)

**Status:** verified (2026-07-17) (feature-21 tasks.md T4; implemented + `-race` green).

**Decision.** The `initialized` notification handler no longer builds the workspace index synchronously on
the dispatch loop. Instead it spawns a single background goroutine (tracked by a `sync.WaitGroup`,
`bgBuild`) that runs `hctx.buildIndex(bgCtx, …)` **off** the loop, then — guarded by a `bgCtx.Err()` check
— `publishIndex`, `reportNoUsableRoot`, and the test-only `indexReadyHook`. The handler returns
immediately after `bgBuild.Add(1); go …`, so the strictly-serial dispatch loop keeps servicing messages
while the build is in flight (NFR-5). `client/registerCapability` for watched files stays **on the loop**
(it does not depend on the index, so watched-files registration is not delayed by the build).

**bgCtx.Err() guard placement.** The guard sits **after `buildIndex` returns and before `publishIndex`**.
`buildIndex` passes `bgCtx` into `workspace.Build` (ADR-020), so a shutdown that races the build aborts it
mid-scan; but even a build that completed just as `bgCancel` fired must not publish. Checking `bgCtx.Err()`
directly (not the build error) after the build is the single decision point: cancelled → skip
publish/report/hook and return; else publish. This gives Story 2 AC5 "no publish-after-shutdown."

**Goroutine lifecycle — joined, never leaked.** `Run` registers `defer func(){ bgCancel(); bgBuild.Wait() }`
**after** `defer stream.Close()`, so by LIFO it runs *first*: on any exit path `bgCancel` aborts the
in-flight build (ADR-020 returns `ctx.Err()` at the next per-file check) and `bgBuild.Wait()` blocks until
the goroutine has observed cancellation and returned — *then* the stream is closed. This guarantees the
goroutine never (a) writes to a torn-down stream, (b) touches `hctx` after `Run` returns, or (c) leaks. It
is the reason `-race` stays clean with the build goroutine and the read loop both live.

**Stream-write safety from the goroutine.** `reportNoUsableRoot`/`sendShowMessage` `stream.Write` now runs
on the background goroutine, concurrently with the dispatch loop's response writes. This is safe because
`go.lsp.dev/jsonrpc2`'s `headerStream.Write` serializes each frame under its own `writeMu` (verified in
`framer.go@v1.0.0`: `wbuf`/`writeMu` guard the compose-and-write; reads use a separate `rbuf`/path). No
extra serialization is needed in the server; documented inline at the call site.

**indexReadyHook fires last (test-sync ordering).** The test-only `indexReadyHook` fires as the goroutine's
**final** action — after `publishIndex` AND `reportNoUsableRoot` — so a pre-fed lifecycle test can use it
as an "everything done" gate and withhold `shutdown` (which cancels `bgCtx`) until the build has fully
published and emitted its no-usable-root signal. This does **not** violate OQ-D end-first (progress `end`
before the no-usable-root `window/showMessage` is a T7 concern on the client-facing channel); the hook is a
non-production seam, and firing it last is strictly safer for synchronization.

**Test adaptation (async timing).** Pre-fed `initialize→initialized→shutdown→exit` harnesses could no
longer assume the index is ready the instant `initialized` is processed — shutdown would cancel `bgCtx`
before the build published. A shared test harness (`internal/server/async_build_test.go`:
`indexReadyGate`/`gatedHandshakeReader`/`runGatedHandshake`/`runGatedLifecycle`) serves the pre-shutdown
messages, blocks a gated reader until `indexReadyHook` fires, then serves shutdown/exit — making the build
deterministically complete first. Index/resolution-dependent messages (e.g. a `didOpen` whose ambiguity
diagnostic needs the resolution set) are scheduled in a post-ready "mid" phase. Over-the-wire integration
tests (no hooks) instead **retry** the index-backed request with a bounded deadline, mirroring how a real
editor sees the definition become available once indexing finishes.

**Alternatives considered.** (a) Fire-and-forget goroutine with no join, relying only on `bgCtx` — rejected:
leaves a window where the goroutine writes to the stream/hctx after `Run` returns (use-after-return, a
`-race` flake source). (b) Await goroutine via a done-channel select in the loop — rejected: the join must
happen on *every* exit path (EOF, ctx-cancel, exit-notification), which a single deferred `WaitGroup.Wait`
expresses cleanly and a per-return-site select does not. (c) Keep the synchronous build but move it to a
worker before returning the initialize response — rejected: violates NFR-5 (the loop still stalls).

**Source:** feature-21 `docs/plans/features/21-async-indexing-and-progress/tasks.md` (T4); jsonrpc2
`framer.go` (`headerStream.writeMu`), `go.lsp.dev/jsonrpc2@v1.0.0`.

## ADR-022 — Replay open buffers into the index after the background build publishes (feature 21 T13, OQ-B.1) (2026-07-17)

**Status:** verified (2026-07-17) (feature-21 tasks.md T13, Decision OQ-B; implemented + `-race -count=2` green).

**Problem.** ADR-021 made the initial index build asynchronous. A `didOpen`/`didChange` that arrives **before**
the background build publishes calls `applyDocumentChange` while `hctx.idx == nil`; it logs "called before
index initialized" and the edit lands in the `document.Store` but **not** the index. The subsequent full-build
publish then swaps in fresh **disk** content, so **index-backed** providers (`workspace/symbol`, `definition`,
`references`) serve stale disk analysis and miss the in-flight edit until the next change. Store-first providers
(`documentSymbol`) are unaffected because they read the store directly. This is the OQ-B.1 window.

**Decision (OQ-B → replay).** After `publishIndex(idx, res)` on the background goroutine — and only when NOT
cancelled — the goroutine calls `hctx.replayOpenBuffers()`, which re-applies every open-document buffer's
**already-computed** `model.FileAnalysis` (from `Store.Open`/`Update`) into the freshly-published index via the
same merge machinery as `applyDocumentChange`: `idx.Add(relPath, analysis)` per buffer, then a single
`workspace.ResolveInto` over all replayed paths. No re-analysis happens (the store's `Analysis` is reused), so
no analyzer work runs under the lock.

**Lock discipline (F7, mirrors `applyDocumentChange`).** Per-document `relPath` derivation runs **off** the
lock (`Store.OpenDocuments()` returns a snapshot; `uriToRelPath` is pure). A **single** `idxResMu.Lock()`
section then does all the `idx.Add`s plus one `ResolveInto`, so handlers reading under `RLock` never observe a
torn "some-buffers-merged" state. A `didChange` arriving during replay is serialized by the strictly-serial
dispatch loop and the mutex — it cannot interleave with the replay's lock section.

**Store snapshot must be by value, not by pointer (the `-race` fix).** The first cut of `Store.OpenDocuments()`
returned the live `[]*document.Document` pointers. `-race` immediately flagged a write/read race:
`Store.Update` reassigns a live `Document`'s fields **in place** under the store's write lock (a concurrent
`didChange`), while `replayOpenBuffers` read `doc.Analysis`/`doc.URI` **off** the store lock. Fix:
`OpenDocuments()` returns **value copies** (`[]document.Document`) taken under the store `RLock`. Because
`Update` assigns *fresh* `Content`/`Analysis` values (never mutates the backing arrays in place), a value
snapshot is a stable, race-free view. This is the load-bearing correctness detail of the task.

**Ordering vs. `reportNoUsableRoot` / `indexReadyHook`.** Replay runs **after `publishIndex` and before
`reportNoUsableRoot` and `indexReadyHook`**. Two consequences: (1) a test gating on `indexReadyHook` observes
the fully-realized index (including replayed buffers); (2) the no-usable-root file-count check reflects the
post-replay index, so a workspace whose only content is an open buffer counts as **usable** (no spurious
"no usable root" warning). This is a deliberate, sensible resolution of the "open buffer is the only content"
note in the task; `reportNoUsableRoot`'s own logic is otherwise unchanged.

**Regression test.** `internal/server/replay_test.go::TestReplayDirtyBufferAfterPublish` uses a live-pipe
harness and a `gatedDiskAnalyzer` (wraps the real analyzer; blocks only on content carrying a disk-only
marker, so the disk build stalls while buffer analysis is never gated). It `didOpen`+`didChange`es a file to
buffer content that adds a subroutine **absent from disk** while the build is blocked, uses a barrier request
to guarantee the edit is processed before releasing the build, then asserts an **index-backed**
`workspace/symbol` for that subroutine returns a hit. Verified failing (`[]`, disk content wins) before the
replay and passing after — the direct proof the OQ-B.1 window is closed.

**Alternatives considered.** (a) MVP "store-first is enough; index catches up on next change" (OQ-B recommended
option) — rejected by user decision OQ-B: index-backed providers would silently serve stale content until an
unrelated edit, a confusing correctness gap. (b) Re-analyze each buffer during replay (like `applyDocumentChange`
does on the change path) — rejected: the store already analyzed on Open/Update, so reusing `doc.Analysis` avoids
redundant analyzer work under the lock. (c) Return live `*Document` pointers from `OpenDocuments` — rejected by
`-race` (see above).

**Server + document change only:** no `internal/model`, `Analyzer`-interface, or cache-format change. One
additive `document.Store.OpenDocuments()` accessor.

**Source:** feature-21 `docs/plans/features/21-async-indexing-and-progress/tasks.md` (T13, Decision OQ-B / OQ-B.1).

## ADR-023 — Fire-and-forget `window/workDoneProgress/create`, capability-gated, end-before-no-usable-root (feature 21 T5/T6, FR-32) (2026-07-17)

**Status:** verified (2026-07-17) (feature-21 tasks.md T5/T6, Decisions OQ-A/OQ-C/OQ-D; implemented + `-race -count=2` green).

**Problem.** The async background build (ADR-021) must report indexing progress (FR-32) via LSP work-done
progress. Two constraints collide: (1) `window/workDoneProgress/create` is a server→client **request**, but the
strictly-serial dispatch loop cannot block awaiting its response (the Response branch only logs — there is no
correlation/wait primitive, `server.go:625`), and (2) progress must not appear at all for a client that did not
advertise `window.workDoneProgress`, while the build itself must still run (Story 1 AC2).

**Decision.**
- **OQ-A → (i) fire-and-forget create, then begin.** On the background build goroutine the reporter sends
  `create` then immediately `begin` with **no await**; the create response stays logged-only in the existing
  Response branch. A client that advertised `window.workDoneProgress` is expected to accept server-initiated
  progress right after create; a create rejection means the client ignores the (cosmetic, non-fatal) progress —
  FR-43. The wire order is `create (request)` → `$/progress begin` → `report*` (T7) → `$/progress end`, all
  sharing one `protocol.String("natural-lsp-index")` token.
- **OQ-C → gate on `Capabilities.Window.WorkDoneProgress`.** The reporter is constructed in the `initialized`
  handler as `newProgressReporter(stream, token, logger, clientSupportsWorkDoneProgress)`. The T1 flag is
  negotiated at `initialize` (which always precedes `initialized`), so it is available. When the capability is
  absent the reporter is the **disabled no-op** (ADR/T2): every create/begin/report/end method returns nil
  writing zero bytes, so a non-supporting client sees NO `window/workDoneProgress/create` and NO `$/progress` on
  the wire, yet the async build still runs and `indexReadyHook` still fires. **No server capability is advertised
  for progress** — like publishDiagnostics, it needs none; the `TestInitialize` allow-list is unchanged.
- **OQ-D → end before no-usable-root.** The single `end` fires **after** `publishIndex`+`replayOpenBuffers` but
  **before** `reportNoUsableRoot`, so the progress UI retires before feature-20's actionable no-usable-root
  `window/showMessage` warning surfaces.

**Cancellation (no progress for an aborted build).** The existing `bgCtx.Err()` guard after `buildIndex` returns
early on a shutdown-raced build, skipping publish/replay/hook AND the progress `end`. `create`/`begin` may
already be on the wire; that is a harmless orphaned token the client discards (non-fatal). `end` is deliberately
NOT written on the cancel path — writing to a stream `Run` is tearing down is exactly what the guard avoids. The
`bgBuild.Wait()` join before `stream.Close` (ADR-021) still guarantees no progress write after `Run` returns.
`stream.Write` from the goroutine is safe (headerStream serializes frames under its own `writeMu`, ADR-021).

**Regression tests** (`internal/server/progress_wire_test.go`, reusing T4's `runGatedHandshake`/gated harness and
a new `decodeAllFramed` ordered-stream decoder): `TestProgressSequence_CreateBeforeBegin` asserts
create<begin<end sharing one token (create is a request, begin/end are notifications);
`TestProgressCreateIsFireAndForget` asserts begin is written though no create-response is ever fed (proves no
await); `TestProgressCapabilityGating` is the two-branch proof — supporting client → create+`$/progress` present,
non-supporting → neither — and BOTH reach a populated index. Forcing `enabled=true` unconditionally makes the
non-supporting branch fail (confirmed the gate is load-bearing).

**Server-only change:** no `internal/model`, `Analyzer`-interface, or cache-format change. Marshaling stays
json/v2 (T2's reporter: `MarshalJSONTo` for params, `mustLSPAny`/`gojson` for the `$/progress` value union).

**Source:** feature-21 `docs/plans/features/21-async-indexing-and-progress/tasks.md` (T5/T6, Decisions OQ-A/OQ-C/OQ-D).

## ADR-024 — Wire the on-disk cache into the server build path; fix `BuildWithCache` cold-start & write-back (feature 21 T12, OQ-E, FR-37/FR-38/NFR-2) (2026-07-17)

**Status:** verified (2026-07-17) (feature-21 tasks.md T12 / Decision OQ-E; implemented + `-race -count=2` green).

**Problem.** The server always **cold-built** the index: `buildIndex` called `workspace.Build` (no cache path),
so every start re-analyzed every file — Story 1 AC4's "warm start served from cache" could not be exercised
through `Run`. OQ-E resolved to wire `BuildWithCache` in now. Investigating revealed `BuildWithCache` had never
actually served the server's use case: it was only ever exercised in tests that **pre-seed a cache and pass an
explicit matching `currentHashes` map**. Two latent defects fell out (both confirmed with a throwaway probe test
before fixing):
1. **Cold-start-with-cachePath produced an empty index.** On a missing/corrupt/version-mismatched cache the
   fallback set `staleFiles = files` (**absolute** paths) but the scan loop matched `staleMap[relPath]`
   (**relative**) — every lookup missed, and the only from-scratch analyze branch was gated on `cachePath == ""`,
   which is false when a cachePath is supplied. Net: nothing analyzed.
2. **`BuildWithCache` never wrote the cache back.** `Save` existed but was called only by tests, and it reads
   `os.ReadFile(indexKey)` on the relative key — correct only when CWD == root.

**Decision.**
- `buildIndex` calls `workspace.BuildWithCache(ctx, root, cfg, az, logger, cachePath, nil, onProgress)` with
  `cachePath = filepath.Join(hctx.root, hctx.cfg.Cache.Path)` (default `.natural-lsp-cache`, workspace-relative).
  It passes **`currentHashes = nil`**: `BuildWithCache` now computes content hashes from disk itself (sha256,
  keyed by relPath) when nil and a cachePath is set, so invalidation is content-based (FR-38), not mtime.
  Explicit-map callers (the workspace tests) are unchanged.
- **Staleness unified on relPath.** A file is (re-)analyzed when it is `staleMap[relPath]` OR absent from the
  loaded cache (`!idx.Get(relPath)` — cold start / newly-added file). The old absolute-path fallback and the
  `cachePath == ""`-gated second analyze branch are gone.
- **`staleCount` semantics preserved:** it counts ONLY files stale against a *loaded* cache (warm-start
  invalidations); a cold build reports 0 (honoring `TestBuild_CacheIntegration`'s "no cache → staleCount 0"). A
  separate `analyzedAny` flag drives write-back.
- **Write-back:** a new root-aware `saveIndex(idx, root, cachePath)` (cache.go) computes hashes from
  `root/relPath` (CWD-independent, unlike the retained `Save`), `os.MkdirAll`s the cache dir, and writes.
  Triggered when `analyzedAny || !cacheExists(cachePath)` — so a fully-warm build with zero re-analysis does not
  rewrite an unchanged cache, but a cold build or any re-analysis persists.
- **Graceful degradation (FR-43):** a missing/corrupt/version-mismatched cache → full rebuild, no error (the
  `Load` error/`idx==nil` branches reset to an empty index); a write-back or mkdir failure is **logged, never
  fatal** — the in-memory index is still valid for the session.

**Cache format is UNCHANGED (`0.6.0`)** — this is pure wiring + two correctness fixes to an existing function; no
`internal/model`, `Analyzer`-interface, or on-disk-format change.

**Regression tests** (`internal/server/cache_wiring_test.go`, a `countingAnalyzer` wrapping the real analyzer to
observe exactly which files are re-analyzed): `TestBuildIndexCacheColdThenWarm` (cold writes the cache file under
`root/cfg.Cache.Path` + analyzes all; warm loads it + re-analyzes ZERO); `TestBuildIndexCacheChangedFile` (change
one file → only that file re-analyzed); `TestBuildIndexCorruptCacheFallsBack` (garbage cache → full rebuild, no
error/panic, cache repaired so a follow-up build is warm). A pre-existing **T4** async-timing defect surfaced when
running the full suite — `TestLifecycleDiagnosticPublishing_DidChangeWatchedFiles_Change` pre-fed
`didChangeWatchedFiles` into one buffer and raced the now-async build (the index/disk diagnostics path saw a
not-yet-published index; confirmed it fails with cold `Build` too, so NOT a cache regression). Fixed by converting
that test to the pipe + `indexReadyGate` harness so the change is sent only after the index publishes.

**Source:** feature-21 `docs/plans/features/21-async-indexing-and-progress/tasks.md` (T12, Decision OQ-E).

## Sources
- Internal (authoritative): `README.md`, `docs/plans/natural-lsp-prd.md`, `CLAUDE.md`,
  `docs/plans/features/`.
- Cross-referenced Go KB (verified 2026-06-20): `.claude/knowledge/go/lsp-go-ecosystem.md`,
  `.claude/knowledge/go/filesystem-and-watching.md`.
- LSP 3.17 spec for ADR-008/009/014/016 — see `lsp-protocol.md`:
  https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/
    (verified 2026-06-23).
