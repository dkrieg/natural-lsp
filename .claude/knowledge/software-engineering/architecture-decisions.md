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
  holding `id`/`name`/`programArgs.{default,windows}`/`fileTypeMappings[{fileType.{name,patterns},languageId}]`,
  optional `README.md`). Committed at `editors/jetbrains/lsp4ij-template/` so setup is reproducible by
  import. (Feature 25 corrected an earlier template that used invented field names —
  `serverName`/`command`/`mappings`/`fileNamePatterns` — and would not import; a Mocha schema-validation
  test now guards the real schema in CI.)
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

### ADR: Synthetic-corpus generator lives in a normally-built package, not the bench-tagged one (feature 22, T1)
`Status: verified` — 2026-07-18

**Decision.** The deterministic synthetic-corpus generator is `internal/workspace/corpusgen` — a **normally-built**
package (no `//go:build bench` tag). Rationale: T1's small-corpus correctness test must run in `just test` (the
generator is the trust anchor for every benchmark, so it needs permanent CI coverage), while the heavy benchmarks
(T2+) live behind `//go:build bench`. Both must import the generator, so it cannot itself be bench-tagged. Placing it
under `internal/workspace` keeps it next to its only consumers (`workspace.Build`/`Resolve`) and its config/model
dependencies without a new top-level package.

**API.** `Generate(targetDir string, objectCount int, seed int64) error` (convenience) over
`GenerateParams(targetDir string, p Params) error` where `Params{Objects, Libraries, Seed, CrossRefDensity}`.
Zero-valued `Libraries`/`CrossRefDensity` get size-appropriate defaults in `normalize`.

**Determinism contract (NFR-9).** All randomness is drawn from a single `rand.New(rand.NewSource(seed))` — never the
global unseeded source, never a wall-clock value. Same `(objectCount, seed)` → byte-identical tree (asserted by a
hash-of-tree equality test across two temp dirs). Critically, the **object plan** (`planObjects`: kind/name/library
assignment via fixed round-robin) does NOT consume the rng — it is a pure function of `(Objects, Libraries)` — so the
per-object content rng draws stay aligned regardless of `CrossRefDensity`, and object *count* is exact and
density-independent.

**Corpus layout.** N libraries `LIB00..LIBnn` (top-level dirs). Phase 1 seeds each library with a baseline of
subprogram(.NSN)/subroutine(.NSS)/copycode(.NSC)/DDM(.NSD) so a caller always has a same-library target; phase 2 fills
the remaining budget with programs(.NSP). A `.natural-lsp.toml` declares each library with a **single-hop steplib
chain** (`LIBk` → `LIB(k+1 mod N)`), so resolution exercises both current-library and cross-library-via-steplib
paths. Programs emit guaranteed CALLNAT/INCLUDE/external-PERFORM/READ plus an inline `DEFINE SUBROUTINE` (a same-file
PERFORM target), all against `reachableTargets` (current lib + declared steplibs) so every static reference resolves.
DDMs are emitted in the exported fixed-column report format the `ddm.go` line-scanner parses (T@0/L@2/DB@4/Name@7/
F@41/Leng@43).

**Trust guard.** The correctness test builds via `workspace.Build` + `workspace.Resolve` and asserts: exact object
count indexed, zero happy-path syntax diagnostics, and that CALLNAT/PERFORM/INCLUDE edges actually **resolve** (not
dangling) plus named DDM read sites and parsed DDM field definitions exist. This is what makes the generator
trustworthy for benchmarks.

**No contract impact.** No `internal/model`, Analyzer-interface, or cache-format change — the generator only emits
`.NSx` source consumed by the existing analyzer. Output dir patterns (`/bench-corpus/`, `*.bench-corpus/`) added to
`.gitignore`; the corpus is generated, never committed.

**Source:** feature-22 `docs/plans/features/22-performance-and-scale-verification/tasks.md` (T1, Decisions OQ-A/OQ-D).

### ADR: Warm-start & request-latency baselines — measured findings (feature 22, T5/T6) (2026-07-18)
`Status: verified` — 2026-07-18 (measured on Apple M4 Max, Go 1.26, darwin/arm64)

**Warm-start (NFR-2) is NOT dominated by analysis — it is dominated by hashing + JSON cache
deserialization.** Full-cache-hit `BuildWithCache` (`staleCount==0`) measured ~57–61 µs/object across
tiers (small 11.4 ms/200 obj, medium 61 ms/1000, large 246 ms/4000) — i.e. **roughly the same or worse
per-object than a COLD build** (~14–17.5 µs/object). Reason: on a full hit no file is re-analyzed, but
the build still (1) walks the whole tree, (2) with `currentHashes==nil` re-reads every file from disk to
recompute its SHA-256, and (3) `Load` unmarshals the ENTIRE JSON cache into `FileAnalysis`. Alloc counts
confirm the cache-JSON cost dominates (warm large ≈220k allocs vs cold ≈332k). **Implication for a future
NFR-2 pass:** the warm path's win is skipping *analysis*, not I/O — sub-second warm start at 4k objects
holds on this hardware (246 ms), but at tens of thousands the per-file hash+unmarshal will be the wall.
Partial-invalidation (`staleCount==K`, K=5) tracks the full-hit cost plus the K re-analyses and a cache
rewrite.

**Interactive request latency (NFR-3) — the disk sweep is the cost.** The two providers that do a
per-query full-workspace `os.ReadFile` sweep are clearly the hot ones at scale (4k-object large tier):
`workspace/symbol` ~44.6 ms/query, 31.8k allocs; `references` ~49 ms/query, 24k allocs — both grow
~linearly with index size (≈11 µs/file), because each query re-reads every indexed file solely to convert
byte columns → UTF-16 code units. This is the T8 target; these figures are the pre-fix baseline for the T9
before/after contrast.

**OQ-E decision — the name index IS hot enough to warrant T7's cache.** `NamesWithPrefix` (rebuilds the
whole name index via `buildNameIndex` on every call) measured ~3.68 ms/query with **12k allocs / 2.26 MB**
per call at the large tier (44 µs at small, 400 µs at medium — clean linear growth in files). Since it
fires on **every completion keystroke**, ~3.7 ms + 2.3 MB of garbage per character at only 4k objects (and
proportionally worse at 10k–50k) is a real interactive cost. `LookupByName` is cheaper (~2.98 ms/query but
only **2 allocs** — it does not build a map, just walks and appends matches) and fires **once per request**
(DDM/module resolve), so it is far less pressing. **Recommendation:** proceed with T7 to cache the name
index (invalidated on `Add`/`Invalidate` under the lock), primarily to kill `NamesWithPrefix`'s
per-keystroke O(files) rebuild + allocation churn; `LookupByName` benefits too (it can consult the same
cached map) but was not the deciding factor.

**Test placement.** The `workspace/symbol` and `references` provider baselines live in the
`internal/server` package behind `//go:build bench` (they need the unexported `*handlerContext`, so
benchmarking the *real* provider beats replicating its query path); `just bench` now covers both
`./internal/workspace/bench/...` and `./internal/server/...`. The `NamesWithPrefix`/`LookupByName`
micro-benchmarks and the warm-start benchmarks are index-level and live in `internal/workspace/bench`. The
NFR-8 freshness guard is a **normal (untagged) unit test** (`internal/workspace/freshness_test.go`,
`TestBuildWithCache_NeverServesStaleContent`) so it runs in `just test` — content-hash invalidation is a
gating correctness property, not a benchmark.

**Source:** feature-22 tasks.md (T5/T6, Decisions OQ-C/OQ-E); measured `just bench` output 2026-07-18.

### ADR-025 — Eliminate the workspace/symbol & references per-query disk sweep via an in-memory per-file line-width table (feature 22 T8, OQ-B B-i) (2026-07-18)
`Status: verified` — 2026-07-18 (measured on Apple M4 Max, Go 1.26, darwin/arm64)

**Problem.** `provideWorkspaceSymbols` and `referenceSites` called `os.ReadFile` for **every indexed
file on every query**, inside `idx.ForEach` under the Index RLock. The disk read existed *solely* to feed
`toProtocolRange`'s byte-offset-column → code-unit conversion: UTF-16 (the LSP default) needs the source
line's bytes to count surrogate pairs; UTF-8 does not. The symbol data itself (`FileAnalysis.Structure`)
was already fully in memory. At scale this is tens of thousands of disk reads per keystroke (baseline:
~44.6 ms / ~49 ms per query at 4k objects — ADR above).

**Decision (OQ-B B-i, user-approved).** Keep an **in-memory, encoding-agnostic per-file line-width table**
keyed by workspace-relative path, and convert ranges from it — never reading the file per query. The table
maps `(0-based line, byte offset)` → code-unit count for both UTF-8 and UTF-16. It is
**in-memory-only**: NOT persisted, so **no cache-format bump (still 0.6.0), no `model.FileAnalysis` change,
no Analyzer-seam change.**

**Representation (ASCII fast path).** Natural source is overwhelmingly ASCII (identifiers A-Z/0-9/#/&/-/@/+),
so an ASCII line stores only its `uint32` byte length (byte == UTF-8 unit == UTF-16 unit) and retains **no
bytes**; only a non-ASCII line retains its raw bytes for exact UTF-16 surrogate-pair counting. Memory cost
is therefore near-zero for the common case — confirmed by the T4 cold-memory benchmark: heap-per-object
stayed flat across tiers (~4,702 B/object at 4k), so the table does not perturb NFR-4's linear-growth band.

**Placement & population (seam-safe).** The table lives on `workspace.Index` (`lineWidths map[string]*lineWidthTable`,
guarded by the existing `idx.mu`). It is populated wherever content is already in hand — the
`BuildWithCache` scan loop (`idx.PutContent(relPath, content)` right after `idx.Add`), the server's
`applyDocumentChange` and `replayOpenBuffers` (live-edit content). On a **warm cache load**, cached-but-unchanged
files were never re-read, so a one-time `ensureLineWidths` sweep reads each such file exactly once at
build/load time (an **amortized one-time cost, NOT per-query**; FR-43 — a read failure leaves the file with
no table and falls back to byte==code-unit, exact for ASCII).

**Query path.** New `Index.ForEachWithRange(f func(path, fa, RangeConverter))` walks entries and the
line-width tables **under a single RLock**, handing each callback a disk-free `RangeConverter`. This avoids
a nested-lock re-entrancy hazard: a plain `ForEach` + a per-file `idx.ProtocolRange` (which re-acquires the
RLock) can deadlock if a writer is queued between the two RLock acquisitions (Go RWMutex is not
recursively-safe under writer contention). `lineWidthTable.protocolRange` reproduces `toProtocolRange`'s
exact semantics (0-based, end-exclusive, zero-width caret preserved).

**Result (T9 before/after, 4k-object large tier, same hardware).**
- `workspace/symbol`: **44.6 ms → 0.97 ms/query (~46×), 5.29 MB → 0.74 MB (~7×), ~31.8k → 13.8k allocs.**
- `references`: **49 ms → 1.46 ms/query (~34×), 4.33 MB → 0.58 MB (~7.5×), ~24k → 8.0k allocs.**

**Correctness guards (untagged, run in `just test`).** `internal/workspace/linewidth_test.go` asserts the
table's conversion equals an independent UTF-16 oracle for every byte offset on ASCII + BMP +
supplementary-plane lines under both encodings. `internal/server/linewidth_regression_test.go` proves the
providers return byte-identical `Location` ranges vs. the disk-reading oracle (incl. a non-ASCII/UTF-16
corpus) under both encodings, AND that results are unchanged after the source files are **deleted from
disk** (the disk-free proof). `-race -count=2` clean.

**Source:** feature-22 tasks.md (T8, Decision OQ-B B-i); measured `just bench` output 2026-07-18.

---

### ADR-026 — Cache the name→[]Candidate index on `workspace.Index`, invalidate on `Add` (feature 22 T7, OQ-E) (2026-07-18)

**Status:** verified (2026-07-18) — measured `just bench` before/after; `-race -count=2` clean.

**Problem.** `Index.NamesWithPrefix` (completion, fires per keystroke) and `Index.LookupByName`
(definition/hover DDM resolve) each rebuilt the full name index on **every call**:
`NamesWithPrefix` called `buildNameIndex` (a fresh O(files) `map[name][]Candidate` pass);
`LookupByName` did its own O(files) `ForEach` walk computing `objectIdentity` per entry. T6 measured
`NamesWithPrefix` at **3.68 ms / 12,082 allocs / 2.26 MB per keystroke at 4k objects** — the deciding
data that turned the conditional T7 into an unconditional GO (OQ-E: measure-first, fix-if-hot).

**Decision.** Cache the built `map[string][]Candidate` on `Index` (`nameIndex` + a `nameIndexCfg`
identity guard), reuse it across calls, and invalidate it (set nil) on **`Add` — the sole mutator of
`idx.entries`** (`Build`, cache `Load`, and the server's `applyDocumentChange` all funnel through
`Add`; `Invalidate` is read-only and does NOT mutate entries, so it is not an invalidation point).
`buildNameIndex` was refactored to share a pure `buildNameMapFrom(entries, cfg)` with the new
lock-held `cachedNameIndex`. `LookupByName` now does an O(matches) bucket lookup + copy instead of an
O(files) walk (the cache's per-name bucket is already Path-sorted, so no re-sort). **No `internal/model`
change, no cache-format bump (0.6.0), no seam change — the cache is in-memory-only Index state.**

**Lock discipline (deadlock-free, the crux).** Go's `sync.RWMutex` has **no lock upgrade** — an RLock
holder that needs to build (write) cannot atomically upgrade to Lock (self-deadlock). So `cachedNameIndex`
uses **double-checked locking**: fast path takes `RLock`, returns the cache if present for this `cfg`;
otherwise it **releases the RLock**, takes the full `Lock`, re-checks (another goroutine may have built
it in the gap), builds from `idx.entries` directly if still nil, and publishes `nameIndex`+`nameIndexCfg`
atomically. The cache is thus **only ever read under RLock or written under Lock**; `Add` nils it under
the write lock it already holds (no upgrade). No new mutex, no re-entrancy.

**`cfg` identity guard.** `buildNameIndex` is `cfg`-dependent (library map → `objectIdentity`/`Library`).
`cfg` is fixed for a server session (loaded once at startup), so the cache keys on the `*config.Config`
pointer identity; a caller passing a different pointer forces a one-time rebuild rather than serving
results computed under the wrong library map. Documented as a defensive guard for a non-occurring
in-session case.

**Result (before/after, 4k-object large tier, Apple M4 Max / darwin/arm64 / Go 1.26; warm path — no
mutation between lookups, the realistic keystroke case).**
- `NamesWithPrefix/large`: **3,628,850 → 41,475 ns/op (~87.5×), 12,082 → 17 allocs, 2.26 MB → 81 KB.**
  (Residual cost is the prefix scan across all name buckets + reachability filter + result copy; the
  O(files) rebuild-per-call is gone.)
- `LookupByName/large`: **2,960,497 → 30.46 ns/op (~97,000×), now O(matches) and flat (~30 ns) across
  all tiers.** The first call after an `Add` pays a one-time O(files) rebuild; between mutations it is
  the warm figure.

**Correctness guards (untagged, run in `just test`).** `internal/workspace/name_index_cache_test.go`:
(1) cached results == fresh computation across prefixes/types/refPaths + names/type-filters (hand-seeded
+ a real analyzer-built multi-library corpus exercising the steplib chain); (2) **the critical
invalidation guard** — build → query (populate cache) → `Add` a new/retyped object → query again asserts
the change is reflected (not served stale); verified to FAIL when the invalidation is removed (the tests
have teeth); (3) `TestNameIndexCache_ConcurrentLookupsAndAdd` runs 8 concurrent lookups vs. a mutating
writer under `-race`. A stale name-index cache serving deleted/renamed symbols would be the regression
this guards against (freshness, NFR-8-adjacent).

**Alternatives considered.** (a) Skip T7 ("measured, not hot") — rejected: T6 proved it hot. (b) Lazy
build via `sync.Once` — rejected: `Once` can't be reset on invalidation without replacing the whole
`Once`, which reintroduces a race; the nil-means-stale + double-checked build is simpler and
invalidation is a single nil assignment. (c) Eager rebuild inside `Add` — rejected: `Add` is called
per-file during a build (O(files) rebuilds = O(files²)); lazy rebuild-on-next-lookup rebuilds at most
once per lookup burst.

**Source:** feature-22 tasks.md (T7, Decision OQ-E); measured `just bench` output 2026-07-18;
`.claude/knowledge/go/` RWMutex non-upgradability.

## ADR-027 — Single canonical index keyspace: `workspace.NormalizeKey` (forward-slash on every OS) (Windows path-separator fix) (2026-07-20)
`Status: verified` — 2026-07-20

**Decision.** The workspace index / resolution / line-width / content-hash maps use ONE canonical key
form — a workspace-relative path with **forward-slash separators on every OS** — and every site that
turns a `filepath.Rel` result into such a key (or into a lookup against one) routes through a single
exported helper `paths.NormalizeKey(rel string) string`, defined as `strings.ReplaceAll(rel, "\\", "/")`.

**Home of the helper (Finding 1, 2026-07-20).** The helper lives in a dedicated **leaf package**
`internal/paths` (`internal/paths/paths.go`), which imports nothing internal — only stdlib `strings`. This
was chosen over `internal/model` (keep `model` a pure data contract, not a home for string utilities) and
over the original `internal/workspace/paths.go` home, which forced `internal/document` to import
`internal/workspace` merely to reach the normalizer. A leaf package everyone may import keeps
`internal/document` free of a `workspace` dependency with **no import cycle** (`go list -deps` clean;
`internal/document` now imports only `config`, `model`, `paths`). The core cross-platform test
`TestNormalizeKey` moved to `internal/paths/paths_test.go`.

**Problem (root-caused, user-confirmed on Windows).** The keyspace was inconsistent across OS. Producers
(`workspace.BuildWithCache`'s scan loop, `ensureLineWidths`, the content-hash map; the `document`
watcher's re-analyze path) used `filepath.Rel` **raw**, which on Windows yields backslash keys
(`code\LIB1\MYSUB.NSN`). But the server's lookup (`uriToRelPath`) already normalized with
`strings.ReplaceAll(..., "\\", "/")` → forward slashes. On Windows the two sides never matched for any
file in a subdirectory, so `idx.Get`/`res.Get` MISSED → `textDocument/definition`, `references`, and
module `hover` silently returned empty (FR-17 makes a miss non-erroring, so no crash, no diagnostic — a
silent correctness failure). `documentSymbol` masked the bug because it is served from the URI-keyed
open-buffer store, not the index. On macOS/Linux `filepath.Rel` already returns `/`, so both sides agreed
— which is why it escaped CI.

**Why `strings.ReplaceAll`, NOT `filepath.ToSlash`.** `filepath.ToSlash` replaces only the *current OS*
separator, so on Linux/macOS (separator `/`) it is a **no-op on backslashes** — it would neither fix
Windows behavior nor be testable on non-Windows CI, and it would drift from the server's existing
`ReplaceAll`. `ReplaceAll` on the literal backslash byte is unconditional and OS-independent, so the
regression tests are genuine cross-platform guards. One `filepath.ToSlash` call in
`internal/server/diagnostics.go` was replaced by `NormalizeKey` for the same reason (it happened to work
on Windows but was an inconsistent second definition of "canonical").

**Sites routed through the helper.** Producers: `internal/workspace/index.go` (scan-loop key, content-hash
map, `ensureLineWidths`, directory-exclusion), `internal/document/sync.go` (watcher re-analyze +
directory-exclusion), `internal/document/store.go` `deriveRelPath` (the relPath passed to the analyzer).
Server lookups: `definition.go` `uriToRelPath` (the single canonical definition — all other server
providers route through it), `server.go` didChange/didChangeWatchedFiles, `diagnostics.go`. Defensive
index-path comparisons in `references.go`/`hover.go`/`call_hierarchy.go`/`definition.go` (previously raw
`ReplaceAll`) also route through it. The cache-load canonicalization in `internal/workspace/cache.go`
`Load` (Finding 2, above) also routes through it. All of these now call `paths.NormalizeKey`;
`internal/document` imports `paths` (a leaf), NOT `workspace` — cycle-free.

**Cache self-heal (no format change, load-time normalization).** `cacheFormatVersion` stays `0.6.0` — no
bump. A pre-existing Windows user's old cache holds backslash keys. `Load` (in `internal/workspace/cache.go`)
now routes **every stored entry key through `paths.NormalizeKey` before `idx.Add`** (and normalizes the
key used for the `currentHashes` content-hash comparison, and the stale keys in the version-mismatch
branch). Consequence: a loaded backslash key becomes the canonical forward-slash key, so the scan loop's
forward-slash `currentHashes` lookup **HITS** — a proper warm hit when the content is unchanged (no
re-analysis, no forced full rebuild), or a normal re-analyze when it changed. The old cache thus **upgrades
in place**. Crucially this leaves **no orphaned backslash entry**: without load-time normalization the old
backslash key would never match the forward-slash scan key, would never be pruned, and `saveIndex`
(`idx.ForEach`) would re-persist it indefinitely — and in a flat namespace `objectIdentity` would then map
BOTH `code\LIB1\MYSUB.NSN` and the real `code/LIB1/MYSUB.NSN` to the same `("MYSUB","")` → two candidates →
a spurious ambiguity diagnostic + double-location definition, forever. With normalization the index holds
only canonical keys and `saveIndex` re-persists only canonical keys. (Earlier wording claimed a "one-time
full rebuild"; that was the pre-refinement behavior and is no longer accurate — the upgrade is in place with
a warm hit, no forced rebuild, no orphan.) Regression guard: `internal/workspace/cache_test.go`
`TestLoad_CanonicalizesBackslashKeys` hand-serializes a `CacheFile` with a literal backslash key, `Load`s it
against a forward-slash `currentHashes`, and asserts the index has the canonical key and NOT the backslash
key, no spurious staleness, and exactly ONE `LookupByName` candidate — platform-independent, proven
load-bearing (neutering the load-time `NormalizeKey` retains the backslash key and produces the duplicate →
the test fails; restoring it passes).

**Regression guards (platform-independent — run on Linux/macOS CI).**
`internal/paths/paths_test.go` `TestNormalizeKey` (literal-backslash → forward-slash, idempotent on
already-slash, mixed, empty — the core cross-platform teeth). `internal/workspace/windows_path_keys_test.go`:
`TestBuildIndexKeys_NoBackslash_SubfolderResolves` (build over a temp workspace with a `code/LIB1/`
subfolder; assert no index key contains `\` and the same-folder CALLNAT resolves to the subfolder callee
key) and `TestBuildIndexKeys_WindowsMismatchSimulation` (add under a `NormalizeKey`-ed key, look up via a
normalized backslash-form path — designed to FAIL if normalization is removed from either side; a raw
backslash lookup must MISS the forward-slash key). Load-bearing proof: neutering `NormalizeKey` to a
passthrough made both `TestNormalizeKey` and the simulation test fail; restoring it made them pass.

**Source:** user-confirmed Windows bug report; branch `fix/windows-path-separator-index-keys`;
`internal/server/definition.go` pre-existing `uriToRelPath` normalization as the canonical reference form.

**Follow-up — Windows TEST-suite portability (branch `ci/windows-job`, PR #39, 2026-07-20).** After the
production fix above, a new Windows CI job (`go test ./...` on windows-latest) went red — build+vet passed,
so the failures were **test-artifact non-portability, NOT product bugs** (each failure was confirmed to be a
Windows-unaware assertion/harness; production path handling via `paths.NormalizeKey` + `uri.File`/`filepath`
is correct on Windows). Five failure classes, all fixed test-side + a `.gitattributes`:
(1) **Forward-slash hardcoded expectations** — `TestResolveRootStart` compared against literal `"/ws/a"` but
`resolveRootStart` returns `uri.FsPath()` (OS-native → backslash on Windows, correct, it feeds
`config.Bootstrap`/`FindRoot`). Fixed by running only the URI-derived expectations through
`filepath.FromSlash` (a new per-case `fromURI` flag; cwd-fallback cases pass through verbatim, so they stay
literal). (2) **Drive-letter case + 8.3 short names in log-substring asserts** — `TestRunStdioCallsBootstrap`
and `TestNoUsableRoot_EmptyRoot_StderrWarn` matched full absolute paths, but Windows logs a lowercase drive
+ `RUNNER~1`-style short names. Fixed by asserting on the tempdir's **`filepath.Base` leaf segment** (stable
across that variance) plus a stable phrase (`"sentinel found: true"` / `"no indexable Natural files"`) — still
proves the root/warn behavior. (3) **CRLF line endings** — `TestSample` golden byte-compare and
`TestProvideCodeLens_IncrementalFreshness`'s `\n`-needle `strings.Replace` broke when git's autocrlf rewrote
committed files to CRLF on Windows checkout. **Two-pronged, defense-in-depth:** a repo-root **`.gitattributes`
(`* text=auto eol=lf` + explicit source/testdata/fixture pins; `*.bat`/`*.cmd` left CRLF)** so checkouts are
LF on every OS, AND both tests made line-ending-tolerant (normalize `\r\n`→`\n` before compare/replace). The
`.gitattributes` caused **zero renormalization on the existing tree** (all tracked files already LF, verified
via `git ls-files --eol`) — its effect is purely on future Windows checkouts. (4) **Unix-style fabricated
roots** — `TestTextDocumentDidOpen`/`TestTextDocumentDidChange`/`TestFR33DocumentLifecycle` used a hardcoded
`"/workspace"` root + `file:///workspace/...` URIs; on Windows `/workspace` is drive-less so
`filepath.Rel` cannot relativize the doc path → the relPath isn't stripped (analyzer path wrong) and the
`didChange` handler's `filepath.Rel` error `continue`s past `applyDocumentChange` (analyze count 3→2). Fixed
by using a real `t.TempDir()` root and building URIs via `uri.File(filepath.Join(root, …))`; expected relPath
is forward-slash (the `NormalizeKey` canonical key). (5) **`"file://"+path` hand-built URIs** —
`TestRootHandshake*` string-concatenated the rootUri, unparseable on Windows (backslashes + drive letter) so
`FsPath()` returned nothing and root negotiation never fired. Fixed by `uri.File(tempDir)` (correct Windows
`file:///C:/…` formatting) — the same pattern `TestInitializeCR6MalformedConfig` already used. A shared test
helper `internal/server/pathtest_test.go` holds `testFileURI(t, path)` (URI-from-path via `uri.File`) and
`samePath(a,b)` (case+slash-insensitive path compare). **NO product bug found** — all five are test/harness
artifacts; the only production-adjacent change is the repo-root `.gitattributes`. Windows-green can only be
confirmed by re-running Windows CI; the suite stays green on Linux/macOS (`just verify` OK, `-race` + `-tags
integration` clean).

## ADR-028 — Operational `window/logMessage` events: in-memory skip surface + pre-build cache-presence warm signal (feature 26 Story 1) (2026-07-21)
`Status: verified` — 2026-07-21

**Decision.** The server emits the six Story-1 operational events as `window/logMessage` notifications
(dual sink: each also keeps its stderr `slog` line) from `handlerContext.buildIndex` and the
request-dispatch panic-recovery site. Two data-plumbing sub-decisions were needed and are recorded here
because both were non-obvious and had rejected alternatives.

**(1) Per-file skip surface — an in-memory field on `workspace.Index`, NOT a new `BuildWithCache` return.**
The build-end aggregate Warning needs a *reason breakdown* ("N file(s) skipped: too_large=1, unreadable=2"),
which the counts already returned by `BuildWithCache` cannot express. `BuildWithCache` has ~60 call sites
(mostly `idx, _, _, err :=` in tests), so widening its return signature is high-churn and error-prone.
**Chosen:** add an in-memory `[]SkipRecord` field + `Index.Skips()`/`addSkip()` to the already-returned
`*Index`, populated from the three existing degradation branches in the scan loop (unreadable / too-large /
recovered-analyzer-panic). Two additive `config.SkipReason` constants (`SkipUnreadable`, `SkipAnalyzerPanic`)
join the existing `SkipExcluded`/`SkipTooLarge`. **In-memory only — NOT persisted (no cache-format bump,
still `0.6.0`), no `internal/model` change.** A warm-cache hit is not a skip (only files the scan tried and
could not fully index are recorded), so the count is honest. Rejected: (a) extra return value (churn);
(b) deriving `skipCount = totalFiles - len(idx.Keys())` at the server (loses the reason breakdown the AC
requires).

**(2) Warm-vs-rebuild signal keys on PRE-build cache presence, not on the re-analysis counts.** The
`changedCount`/`staleCount` returned by `BuildWithCache` is documented to be **0 for BOTH** a cold first
build (no cache to invalidate against) and a fully-warm build (nothing changed) — so counts alone cannot
distinguish cold from warm. **Chosen:** snapshot `workspace.CacheExists(cachePath)` (new exported wrapper
over the existing unexported `cacheExists`) *before* the build; `warm := cacheWasPresent`. A build is
"warm cache hit" iff a persisted cache existed going in, else "rebuild". Regression
`TestLogMessage_WarmVsRebuild_ColdThenWarm` drives `buildIndex` twice on one root (cold→"rebuild",
warm→"warm cache hit") and is load-bearing.

**Constraints honored (mirrors features 19/20/21).** NO new server capability — `window/logMessage` is a
unilateral notification gated on the client `window` capability, `TestInitialize` allow-list unchanged.
json/v2 marshaling only (`marshalguard`/`TestNoStdlibJSONMarshalForResults` green; no `encoding/json` in
any production file). Fire-and-forget (FR-43) via the feature-26 `messageLogger` (`context.Background()` so
a shutdown-cancelled `bgCtx` still lets the message reach the client). Skip/ambiguity Warnings are a SINGLE
build-end aggregate each (emit nothing when zero — no console flood); modeled gaps (dynamic/unresolved
references) stay OFF the operational-log channel (FR-17), guarded by `TestLogMessage_NoLogForDynamicReferences`.
Fixtures: `internal/server/testdata/skipfiles/` (tiny `max_file_size` → too_large skip) and
`internal/server/testdata/ambiguity/` (flat-namespace `CALLNAT 'DUP'` with two `DUP.NSN`).

**Source:** feature 26 tasks.md Story 1; `internal/server/server.go` `buildIndex`;
`internal/workspace/index.go` (`SkipRecord`/`Skips`/`addSkip`), `internal/workspace/cache.go` (`CacheExists`);
`internal/config/config.go` (`SkipUnreadable`/`SkipAnalyzerPanic`).

## ADR-028 — Server cross-file object-location goes through the steplib chain, never candidates[0] (2026-07-26)
**Context:** feature 27 (variable navigation), SERVER-RESOLUTION review cluster. The LSP definition
and references providers located cross-file targets by calling `idx.LookupByName(name, type, cfg)` and
taking `candidates[0]`. `LookupByName` returns ALL name+type matches UNFILTERED, sorted by Path — so
`candidates[0]` is whichever library's copy sorts first alphabetically, NOT the library reachable from
the caller. A same-named object in a library outside the caller's steplib chain (unreachable) could be
picked over the true chain winner. This affected `provideDDMDefinition`, `references.go`'s DDM branch,
and the USING data-area path (`lookupDataAreaPath`). The stale comment "LookupByName respects library
map" was FALSE.
**Decision:** Route ALL server cross-file object-location through the SAME chain helper the
field-resolver already used (`buildSearchChain`/`resolveViaChain`, ADR-004, non-transitive). Two new
exported workspace helpers on top of a shared `resolveCandidateViaChain`:
`ResolveDataAreaFieldLocation` (returns field range AND chain-resolved object path together, so the
two never disagree) and `ResolveDDMPath` (DDM name → `.NSD` path via the chain). `ResolveDataAreaField`
now delegates to the location-aware sibling. The server's `lookupDataAreaPath` (candidates[0]) was
deleted; `provideDDMDefinition`/`references.go` consume `ResolveDDMPath`.
**Rationale:** One resolution path means the definition provider, references provider, and the
field-resolver can never diverge on which library's object wins; unreachable same-named copies are
excluded exactly as Natural's runtime would (first chain library with a match wins). Flat-namespace
callers (no library map) keep the first-candidate pick unchanged.
**Consequence / test:** New multi-library fixture (`internal/{server,workspace}/testdata/multilib/`):
caller in `APP` with steplib `COMMON`; the data-area `.NSL` and DDM `.NSD` exist in BOTH `COMMON`
(reachable) and `ALT` (NOT in APP's chain). `ALT` deliberately sorts BEFORE `COMMON`, so the old
`candidates[0]` resolves to the UNREACHABLE `ALT` copy — the regression is genuinely load-bearing
(proven RED: both server tests resolved to `…/ALT/…` before the fix). Tests assert the chain winner
(`COMMON`) AND explicit unreachable-exclusion (`/ALT/` never chosen), covering both the USING (T7)
and SQL-DDM (T9) paths, at the workspace level (helpers) and the server level (providers). The server
tests build cfg from the fixture `.natural-lsp.toml` via `config.Bootstrap` (NOT `config.Defaults()`)
so the library map is actually exercised. No `internal/model` change, no cache-format bump (0.8.0),
Analyzer seam intact.

## ADR-029 — Group-qualification `#GROUP.#FIELD` reconstructed from source, not from the ref token (2026-07-26)
**Context:** feature 27, same review cluster. The T2 variable-ref scanner emits a group-qualified
reference `#GROUP.#FIELD` as SEPARATE tokens (`#GROUP`, then `#FIELD`) with the `.` dropped, so a leaf
`VariableRef` carries only the bare leaf name. `resolveVariableDefinition` never reconstructed the
qualifier (a `TODO`), so it searched the whole `Definitions` tree for the bare leaf and only worked
when the leaf was unique — a leaf declared in two level-1 groups returned BOTH candidates even when the
reference was qualified.
**Decision:** Reconstruct the qualifier from the source at resolve time (`qualifierBeforeRef`): read
left of the leaf token's start, skip a single `.` (with optional inline whitespace), and read back the
preceding identifier; that identifier (uppercased) is the level-1 group qualifier. When present,
`findDeclaration(defs, leaf, groupName, …)` scopes matching to the sub-field WITHIN that level-1 group;
when absent, the unqualified path is unchanged (all candidates → genuine ambiguity).
**Rationale:** Source reconstruction is the minimal correct fix given the scanner's token shape (no
model change, no scanner change owned by a parallel agent). Only the reference's own line is inspected
(a name/dot never spans a line break in practice); out-of-range positions return "" (FR-43). CRLF
tolerated via a trailing `\r` strip.
**Consequence / test:** `AMBIG.NSP` (two level-1 groups each with `#FIELD`, plus a qualified reference
`#GROUP-A.#FIELD TO #GROUP-B.#FIELD`) drives the new `TestProvideDefinition_VariableGroupQualified_TwoGroups`:
`#GROUP-A.#FIELD` resolves to EXACTLY `#GROUP-A`'s sub-field (source line 8) and `#GROUP-B.#FIELD` to
`#GROUP-B`'s (line 10) — not both, not the wrong group. The pre-existing weak group-qualified test now
asserts exactly one sub-field location; the unqualified-ambiguous test (all candidates) still passes.
Also in this cluster: deleted the `uriToRelPath` `os.Getwd()` cwd-munging fallback (dead vs. absolute-path
tests, reintroduced the feature-20-removed cwd dependency), and pinned `documentHighlight` Kind to
EXACTLY `DocumentHighlightKindText` (the provider is best-effort and derives no read/write direction —
write-direction is out of scope for feature 27, now a test-visible decision).

## Sources
- Internal (authoritative): `README.md`, `docs/plans/natural-lsp-prd.md`, `CLAUDE.md`,
  `docs/plans/features/`.
- Cross-referenced Go KB (verified 2026-06-20): `.claude/knowledge/go/lsp-go-ecosystem.md`,
  `.claude/knowledge/go/filesystem-and-watching.md`.
- LSP 3.17 spec for ADR-008/009/014/016 — see `lsp-protocol.md`:
  https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/
    (verified 2026-06-23).
