# Tasks: Workspace Root Handshake (feature 20)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-1 (repair/extension), FR-41, FR-43, FR-46, NFR-11, NFR-13, NFR-14
**Priority:** P0 remediation (2026-07-14 assessment, defect #2)

This feature is **server-wiring + config-plumbing only**. It makes **no `internal/model`
change and no cache-format bump** (stays `0.6.0`), and stays entirely on the LSP-facing side
of the Analyzer seam — the analyzer, index, resolution, and DDM/SQL extraction are untouched.
The core defect: root/config are resolved at process startup from `os.Getwd()` and are fixed
*before* the LSP handshake; `initialize`'s `workspaceFolders`/`rootUri` are decoded but never
consulted for root discovery.

---

## Current-state findings & impact

Verified against the code on 2026-07-14 (not the CLAUDE.md summary).

### Where root/config are resolved today
- `cmd/natural-lsp/main.go:47-68` — the `--stdio` arm calls `start, _ := os.Getwd()` then
  `root, cfg, _ := config.Bootstrap(start, "", logger)` **before** `server.Run(...)`. Root and
  cfg are process-startup constants passed as `Run` arguments. **The server never reads
  `initialize` params for the root.** This is the whole bug.
- `config.Bootstrap(start, workspaceHint string, logger)` (`internal/config/config.go:230`)
  **already accepts a `workspaceHint`** (documented as "the LSP initialize rootUri /
  workspaceFolders fallback") that `main.go` passes as `""`. Precedence today: `FindRoot(start)`
  sentinel walk-up wins; **if no sentinel found**, the hint is used; else `start`. This is a
  partly-built seam — the hint parameter exists but is never fed a client value.
- `config.FindRoot(start)` (`config.go:182`) walks **up** from `start` looking for the
  `.natural-lsp.toml` sentinel; returns `(dir, true)` on hit, `(absStart, false)` at fs root.

### How root/cfg thread through the server
- `Run(ctx, r, w, version, root string, cfg config.Config, az, logger)` (`server.go:232`) captures
  `root`/`cfg` as arguments and wires them into **three places, all constructed eagerly at `Run`
  start, before any message is read**:
  1. `handlerContext.root`/`handlerContext.cfg` fields (`server.go:209-210`, set at `:271-272`);
  2. the `document.Store` via `document.New(root, analyzeFunc, logger)` (`server.go:267`) — the
     store is keyed on `root` for URI→relPath;
  3. the `document.NewWatcher(bgCtx, root, &cfg, analyzeFunc, logger)` (`server.go:280`) — the
     fsnotify watcher walks `root`.
- The workspace **index is built at `initialized`** (`server.go:398-412`) via
  `workspace.Build(root, cfg, az, logger, nil)` then `workspace.Resolve(hctx.idx, &cfg)`, using the
  `root`/`cfg` closure variables (not `hctx.root`/`hctx.cfg`).
- The `initialize` **request** handler (`server.go:642-658`) decodes `protocol.InitializeParams`
  and calls `handleInitialize(params, version)` (`server.go:127`), which negotiates
  `positionEncoding` and the `clientSupportsWatchedFilesReg` flag but **ignores
  `params.WorkspaceFolders` and `params.RootURI`**.

**Impact of Story 2 (the risky part):** to derive root from `initialize`, root/cfg can no longer
be fully-formed `Run` arguments used to build the store and watcher *before* the handshake. The
touch-points that consume `root` before `initialize` are the store and the watcher. See OQ-1 for
the seam decision that must be made before implementation.

### Protocol types (verified against go.lsp.dev/protocol@v1.0.0)
- `InitializeParams` embeds `WorkspaceFoldersInitializeParams` and `_InitializeParams`, so both
  fields are promoted:
  - `params.WorkspaceFolders` is `Nullable[[]WorkspaceFolder]`; read via `.Get() ([]WorkspaceFolder, bool)`.
  - `WorkspaceFolder.URI` is `uri.URI`; `params.RootURI` is `*uri.URI` (nil when absent).
- `uri.URI.FsPath()` converts a `file://` URI to an absolute fs path — **the repo already uses
  this** in `uriToRelPath` (`internal/server/definition.go:159-160`, `absPath = fileURI.FsPath()`)
  and in the watched-files handlers (`server.go:506,555`). No new conversion helper needed; reuse
  `.FsPath()`. `RootPath` (deprecated string) is out of scope — `rootUri` supersedes it and the
  plan does not ask for it.

### window/showMessage (Story 3 AC2)
- `window/showMessage` is a **unilateral server→client notification** (`protocol.ShowMessageParams`
  = `{Type MessageType, Message string}`). It adds **no server capability** — confirmed: capability
  advertising is only for request providers, and this is a notification. The `ShowMessage` field on
  `WindowClientCapabilities` gates `window/showMessageRequest` (the *interactive* variant), **not**
  the plain `window/showMessage` notification, which any client may receive. So there is no reliable
  capability flag to gate on; the plain notification is always safe to send. There is **no existing
  `window/showMessage` sender** in the codebase (only `progress.go` mentions the `window/` prefix).
  See OQ-3.

### TestInitialize / smoke lifecycle (Story 2 AC1/AC3)
- `TestInitialize` (`server_test.go:444`) drives `initialize` with `rootPath`/no root and asserts the
  negotiated encoding + a **required-provider** list (`server_test.go:604-613`) plus completion/
  signatureHelp shapes. It asserts *presence* of required providers; it does **not** assert the
  *absence* of others, so it is a "must-include" list, not a strict allow-list. Adding no capability
  keeps it green. The three test cases pass `/workspace` as the `Run` root argument and never send
  `workspaceFolders`/`rootUri` — so the deferred-bootstrap change must keep working when the client
  sends no root (falls back to the `Run`-supplied start).
- The stdio integration test `cmd/natural-lsp/stdio_integration_test.go` (build tag `integration`)
  launches the built binary with **`cmd.Dir = workspaceDir`** (cwd == workspace) and passes
  `rootPath` — so it does **not** cover the failing scenario. It provides the exact reusable harness
  (`readFramedMessageWithTimeout`, framed-write helpers) for Story 1's new regression test.
- `scripts/smoke.sh:54-73` runs `--stdio < /dev/null` (must exit cleanly on EOF) and an
  `initialize→initialized→shutdown→exit` round-trip with `"rootUri":null`. Both must stay green:
  the deferred bootstrap must tolerate a null/absent root at handshake time.

### Criterion reconciliation
- **Already partly built:** `config.Bootstrap`'s `workspaceHint` parameter (Story 1 fallback plumbing
  exists; it is just fed `""`). **Reuse it** — do not add a new bootstrap entry point.
- **Extend:** `handleInitialize` (decode the new fields), `Run`/`handlerContext` (defer/re-run
  bootstrap), the `initialized` index build (use the negotiated root/cfg).
- **New:** a pure `resolveRootStart(params)` helper; the cross-cwd integration regression test; the
  no-usable-root stderr signal (and optionally `window/showMessage`); doc updates.
- **No shared-contract change:** `internal/model`, the Analyzer interface, the index/resolution API,
  and the cache format are all untouched — so **no consumer-migration tasks and no `review-seam`**.

---

## Tasks

Ordering: pure param→start-path resolver (T1) → deferred-bootstrap seam (T2) → wire negotiated
root/cfg into store/watcher/index (T3) → cross-cwd integration regression (T4) → no-usable-root
stderr signal (T5) → optional showMessage (T6) → lifecycle/smoke guard (T7) → docs (T8).

---

### T1 — Pure resolver: initialize params → root-discovery start path
**Story/AC:** Story 1 AC1 (precedence order).
**Behavior:** Add a pure helper (no I/O), e.g. `resolveRootStart(params protocol.InitializeParams,
cwdFallback string) (start string)` in a new `internal/server/root.go`. Precedence, highest first:
1. First `params.WorkspaceFolders.Get()` entry (if the slice is non-empty) → its `.URI.FsPath()`.
2. Else `params.RootURI` (if non-nil) → `RootURI.FsPath()`.
3. Else `cwdFallback` (the current `os.Getwd()`-derived start — backward compatible).
An empty/whitespace `FsPath()` result at a given tier falls through to the next tier (defensive).
**Fixtures:** none (pure unit test with hand-built `InitializeParams`).
**Expected result (table-driven):**
- `workspaceFolders=[{uri:file:///ws/a}]`, `rootUri=file:///ws/b` → `/ws/a` (folder wins over
  rootUri — resolves OQ-2 as *prefer first workspaceFolder*, pending approval).
- `workspaceFolders=null`, `rootUri=file:///ws/b` → `/ws/b`.
- both absent/null → returns `cwdFallback` verbatim.
- `workspaceFolders=[]` (empty, non-null) → falls through to `rootUri`/fallback.
- garbage/non-file URI (e.g. `untitled:…` yielding empty `FsPath()`) → falls through (never panics).
**Reuses:** `uri.URI.FsPath()` (same conversion as `uriToRelPath`); `Nullable.Get()`.
**DoD:**
- [ ] Table-driven unit test covering all five precedence rows above.
- [ ] `FuzzResolveRootStart` guarding against panics on degenerate params/URIs (FR-43).
- [ ] Pure function, no filesystem access; `go vet`/`gofmt` clean.
- [ ] Deterministic.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** none.

---

### T2 — Defer root/config bootstrap into the initialize handler
**Story/AC:** Story 2 AC1 (lifecycle unchanged), Story 2 AC2 (CR-6 degradation preserved).
**Behavior:** Move the `config.Bootstrap` call off process startup and into the `initialize` request
path so the resolved start point can come from T1. Concretely (pending OQ-1 seam decision — default
plan is **variant A**):
- `main.go` no longer calls `Bootstrap`. It still computes the `cwdFallback` via `os.Getwd()` and
  passes it to `Run` as the *fallback start*, not as a final root. `Run`'s signature changes so it
  no longer takes a finished `root`/`cfg`; instead it takes the `cwdFallback` (and constructs the
  `slog` logger / analyzer as today). (Exact signature is OQ-1.)
- In the `initialize` handler (`server.go:642`), after decoding params, compute
  `start := resolveRootStart(params, cwdFallback)` (T1), then
  `root, cfg, _ := config.Bootstrap(start, "", logger)` — reusing the existing bootstrap so the
  sentinel walk-up still runs *from the client path* (Story 1 AC2 comes for free via `FindRoot`).
  Store `root`/`cfg` onto `handlerContext` (`hctx.root`, `hctx.cfg`) at this point.
- CR-6: `Bootstrap` already never hard-fails and logs config `Problem`s; the deferred call preserves
  that (Story 2 AC2). No error from bootstrap may fail the `initialize` response.
- **Observable lifecycle is unchanged:** `initialize`→`initialized` ordering, the response shape, and
  the capability set are identical (no capability added). `handleInitialize`'s existing return
  (encoding, watched-files flag) is preserved.
**Fixtures:** none (server-level unit test).
**Expected result:** With a client sending `rootUri=file:///tmp/ws` and a `Run` cwdFallback of some
*other* dir, `hctx.root == /tmp/ws` (or the sentinel ancestor of it) after `initialize`, and the
`initialize` response is byte-compatible with today's (same capabilities/encoding).
**Reuses:** `config.Bootstrap` (its `workspaceHint`/CR-6 contract), `handleInitialize`.
**DoD:**
- [ ] Unit test: `initialize` with `rootUri` set → `hctx.root` reflects the client path (assert via a
      test hook or by observing the index build target in T3's test).
- [ ] Unit test: `initialize` with **no** root fields → `hctx.root` falls back to the cwdFallback
      (backward-compat; the existing `TestInitialize` `/workspace` cases still pass unchanged).
- [ ] `TestInitialize` (`server_test.go:444`) passes **unchanged** (Story 2 AC1) — do not edit it.
- [x] CR-6: a malformed `.natural-lsp.toml` at the negotiated root logs a `config file error` Warn
      and still returns a successful `initialize` result (assert log contains the problem, response
      has no error). Proven through the deferred bootstrap by
      `TestInitializeCR6MalformedConfig` (`root_handshake_test.go`): asserts the `config file error`
      Warn naming the offending file, a JSON-RPC-error-free `initialize` result, degradation to
      `config.Defaults()` (via `initializeReadyHook`), and a non-empty index despite the bad config
      (via `indexReadyHook`).
- [ ] `go vet`/`gofmt` clean; no capability added to the allow-list.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1. **Blocks:** T3.

---

### T3 — Wire the negotiated root/config into store, watcher, and index build
**Story/AC:** Story 1 AC4 (config, cache, watcher all follow the negotiated root).
**Behavior:** The `document.Store` and `document.Watcher` are today constructed at `Run` start from
the startup `root` (`server.go:267,280`), and the index is built at `initialized` from the `root`/`cfg`
closure vars (`server.go:398-412`). After T2 the authoritative root/cfg are only known at
`initialize`. Rework so all three follow `hctx.root`/`hctx.cfg`:
- Construct (or re-target) the `document.Store` with the negotiated `root` — either build it in the
  `initialize` handler after bootstrap, or build it lazily and set its root then (OQ-1 variant
  detail). The store's URI→relPath keying must use the negotiated root.
- Start the `document.NewWatcher` against the negotiated `root`/`cfg` — moved from `Run` start to
  after bootstrap (at `initialize` or `initialized`). Watcher-start failure stays non-fatal (FR-43,
  `server.go:284-288`).
- The `initialized` index build must call `workspace.Build(hctx.root, hctx.cfg, az, logger, nil)` and
  `workspace.Resolve(hctx.idx, &hctx.cfg)` — using the negotiated values, **not** the removed startup
  closure vars. Cache location follows `hctx.cfg.Cache.Path` under `hctx.root`.
**Fixtures:** reuse the feature-15 sample workspace
(`docs/plans/features/15-editor-clients/sample-workspace/`: `HELLO.NSP` → `CALLNAT 'CALLGREET'`,
`CALLGREET.NSN`, `SAYHELLO.NSS`) copied into a `t.TempDir()`, or a minimal 2-file caller/callee pair
under a new `internal/server/testdata/roothandshake/`.
**Expected result:** After `initialize` (rootUri = temp workspace) + `initialized`, `hctx.idx` is
non-nil and contains the workspace's objects; the watcher is watching the negotiated root. A unit
test can assert via the `indexReadyHook` (`server.go:416-421`) that the index is populated from the
client-provided root even though the `Run` cwdFallback was elsewhere.
**Reuses:** `workspace.Build`/`Resolve`, `document.New`, `document.NewWatcher`, the `indexReadyHook`
test hook.
**DoD:**
- [ ] Unit test: negotiated root drives a non-empty index (via `indexReadyHook`), with the `Run`
      cwdFallback pointed at an unrelated empty dir.
- [ ] Watcher is started against the negotiated root (assert it starts without error; watcher
      failure remains non-fatal — FR-43).
- [ ] `-race` clean (touches the `idxResMu`-guarded fields and watcher goroutine).
- [ ] Existing didOpen/didChange/watched-file tests still green (store keying unchanged in behavior).
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T2. **Blocks:** T4.

---

### T4 — Integration regression: cwd OUTSIDE workspace + rootUri resolves cross-file definition
**Story/AC:** Story 1 AC3 (the exact live-probe failure, as a regression test).
**Behavior:** New integration test (build tag `integration`) in `cmd/natural-lsp/` that reproduces the
assessment defect: launch the built binary with **`cmd.Dir` set to a directory OUTSIDE the
workspace** (a separate `t.TempDir()` with no Natural files and no sentinel), and pass the sample
workspace as `rootUri` in `initialize`. Drive `initialize → initialized → didOpen(caller) →
textDocument/definition(on the CALLNAT target) → shutdown → exit` and assert the definition resolves
to the callee file's location. Before this feature, definition returns null/empty here.
**Fixtures:** the feature-15 sample workspace copied into a workspace `t.TempDir()` **with** a
`.natural-lsp.toml` sentinel; `HELLO.NSP`'s `CALLNAT 'CALLGREET'` resolving to `CALLGREET.NSN`.
**Expected result:** `textDocument/definition` at the `CALLGREET` call site returns a non-empty
`Location[]` whose URI resolves (`.FsPath()`) to the workspace's `CALLGREET.NSN` — proving the index
followed `rootUri`, not the (unrelated) process cwd.
**Reuses:** the `stdio_integration_test.go` harness (`readFramedMessageWithTimeout`, framed-write
pattern, module-root discovery, binary build). Extend/copy that scaffolding; **crucially set
`cmd.Dir` to a NON-workspace dir** (the existing test sets it to the workspace — the difference is the
whole point).
**DoD:**
- [ ] Integration test fails on `main` (pre-feature) and passes after T2/T3 (regression guard).
- [ ] cwd is provably outside the workspace (assert the two temp dirs differ and neither is an
      ancestor of the other).
- [ ] Runs under `just test-integration`; deterministic with the existing 5s/10s timeouts.
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T3.

---

### T5 — No-usable-root: mandatory actionable stderr signal
**Story/AC:** Story 3 AC1 (log an actionable stderr message naming paths tried), Story 3 AC3
(requests still degrade to null/empty, never error).
**Behavior:** After the deferred bootstrap + index build, detect the "no usable root" condition and
log an actionable stderr (`logger.Warn`) message. Two sub-conditions per AC1:
(a) **no workspace root could be established** — no workspaceFolders, no rootUri, and the cwd walk-up
found no sentinel (`FindRoot` returned `found=false` and there was no client path); OR
(b) **the established root contains no indexable files** — the index built successfully but is empty.
The message must **name the paths it tried** (the workspaceFolders/rootUri fs paths considered and
the cwd fallback) so an empty index is diagnosable. `Bootstrap` already logs the resolved root +
`sentinel found: false`; T5 adds the *actionable, path-naming* warning specifically for the
no-usable-root case (distinct from the routine Info line).
**Fixtures:** an empty `t.TempDir()` (no `.NSx`, no sentinel) as the negotiated root.
**Expected result:** `initialize`+`initialized` against an empty root → stderr contains a Warn line
naming the tried paths (assert substring: the temp path(s) and a phrase like "no indexable files" /
"could not establish workspace root"). Requests (e.g. `definition`) against files outside the root
still return null/empty with **no** JSON-RPC error (Story 3 AC3 / FR-43).
**Reuses:** `hctx.logger`, the empty-index observability via `indexReadyHook`; existing FR-43
graceful-degradation paths in the providers (unchanged).
**DoD:**
- [x] Unit test: empty negotiated root → stderr Warn names the tried paths
      (`TestNoUsableRoot_EmptyRoot_StderrWarn`; asserts the root path, the cwd fallback, and the
      "no indexable Natural files" phrase all appear, exactly once).
- [x] Unit test: an out-of-root `definition` request returns null/empty, no error
      (`TestNoUsableRoot_OutOfRootDefinition_NoError`); `handlers_robustness_test.go` unchanged.
- [x] The message is emitted **once** at index-build time (in "initialized"), not per-request.
- [x] `go vet`/`gofmt` clean.
**Implementation:** the pure decision `noUsableRootMessage(rootProbe, indexFileCount)` +
`clientRootPaths`/`triedPathsPhrase` live in `internal/server/root.go`; the probe is recorded in the
"initialize" handler (`hctx.probe`) and consumed once by `handlerContext.reportNoUsableRoot` after
the index build. `FuzzNoUsableRootMessage` guards the decision (FR-43).
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T3.

---

### T6 — window/showMessage warning on no usable root — IMPLEMENTED (OQ-3: in scope)
**Status:** IMPLEMENTED (not cut). Per OQ-3 (RESOLVED: implement `window/showMessage`), the
server sends a `window/showMessage` Warning (`type=2`) notification on the no-usable-root /
empty-index condition, in addition to the T5 stderr Warn. It is the codebase's first
`window/showMessage` sender, adds NO server capability (unilateral server→client notification),
and is emitted ONCE at index-build time alongside the T5 signal (see
`handlerContext.reportNoUsableRoot`/`sendShowMessage` in `internal/server/server.go`; the params
are marshaled via `(protocol.ShowMessageParams).MarshalJSONTo` through a `jsontext.Encoder`, the
same json/v2 path as `client/registerCapability`). A populated, healthy root sends nothing.
**Story/AC:** Story 3 AC2.
**Behavior:** When the no-usable-root condition from T5 fires, additionally send a
`window/showMessage` **notification** (`protocol.ShowMessageParams{Type: Warning, Message: …}`) to the
client. This is the first `window/showMessage` use. **No server capability is added** (confirmed: it
is a unilateral server→client notification; the `ShowMessage` client capability gates only the
interactive `window/showMessageRequest`, not this). Send unconditionally when the condition fires
(there is no reliable client-support flag for the plain notification); a client that ignores it is
harmless.
**Fixtures:** none (server-level test asserting an outbound notification is written).
**Expected result:** On the empty-root scenario, the server writes a `window/showMessage`
notification with `type=2` (Warning) and a message naming the empty/unresolved workspace. Normal
(populated-root) startup writes **no** such notification.
**Reuses:** the framed-write path (`stream.Write`), the marshaling pattern used for
`client/registerCapability` (`server.go:447-456`).
**DoD (implemented):**
- [x] Unit test: empty root → exactly one `window/showMessage` Warning notification on the wire
      (`TestNoUsableRoot_EmptyRoot_ShowMessage`, asserts `type=2` and the empty-condition message).
- [x] Unit test: populated root → no `window/showMessage` (`TestNoUsableRoot_PopulatedRoot_NoSignal`).
- [x] `TestInitialize` allow-list unchanged (no capability added) — still passing.
- [x] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T5. **Decision gate:** OQ-3 → RESOLVED in scope.

---

### T7 — Lifecycle & smoke guard: null/absent root still exits cleanly
**Story/AC:** Story 2 AC3 (`--stdio < /dev/null` and the smoke lifecycle exit cleanly).
**Behavior:** Confirm (and pin with a test) that the deferred bootstrap tolerates the smoke inputs:
`--stdio < /dev/null` (EOF before any message — bootstrap never runs, must exit 0) and the
`scripts/smoke.sh` round-trip with `"rootUri":null` (bootstrap runs with all root fields absent →
falls back to cwd start, must complete the lifecycle and exit 0). No `scripts/smoke.sh` change is
expected; this task guards against a regression where deferred bootstrap panics on absent params.
**Fixtures:** none (drives the built binary / `Run` directly).
**Expected result:** `--stdio < /dev/null` exits 0; `initialize({rootUri:null})→initialized→
shutdown→exit` completes and exits 0.
**Reuses:** `scripts/smoke.sh` (verify it still passes), the `TestInitialize`/handshake harness.
**DoD:**
- [ ] Unit or integration test: `initialize` with `{"processId":null,"rootUri":null,"capabilities":{}}`
      (the exact smoke params) completes the full lifecycle with no error and no panic.
- [ ] `scripts/smoke.sh` passes against the rebuilt binary (run it; document the pass).
- [ ] EOF-before-init path (`--stdio < /dev/null`) still exits 0.
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T3 (and T2).

---

### T8 — Docs: README editor sections + CLAUDE.md project-state note
**Story/AC:** Story 4 AC1 (README editor sections state the root contract; drop any implication that
server cwd determines the workspace), Story 4 AC2 (CLAUDE.md project-state reflects the new
root-resolution order).
**Behavior:** Update the README editor/setup sections (Neovim/Zed/Helix/JetBrains/VS Code) to state
that the server negotiates the workspace root from `initialize` (`workspaceFolders` → `rootUri` →
cwd walk-up), so `root_dir`/`root_markers` in client configs now take effect. Remove/adjust any text
implying the server cwd sets the workspace. Update the CLAUDE.md "Project state" note to record
feature 20 and the root-resolution precedence. (Executed at `/finalize-feature`, but tracked here so
`review-docs` anticipates it.)
**Fixtures:** none.
**Expected result:** README + CLAUDE.md match the shipped root contract; no stale "cwd determines
workspace" claim.
**DoD:**
- [ ] README editor sections describe root negotiation and drop the cwd implication.
- [ ] CLAUDE.md project-state note updated (feature 20, precedence order, "no model/cache change").
- [ ] `review-docs` clean at finalize.
**Agents:** docs edit (no TDD loop); verified by `review-docs`.
**Depends on:** T2–T7 landed.

---

## Reviews required (`/review-feature`)

- **review-protocol-conformance** — deferred `initialize` handling, `workspaceFolders`/`rootUri`
  precedence, and (if T6) the `window/showMessage` notification must conform to LSP; confirm no
  capability was added and the response shape is unchanged (NFR-11).
- **review-concurrency** — T3 moves store/watcher construction and touches the `idxResMu`-guarded
  index build and the watcher goroutine; verify `-race` and the build-then-publish discipline (F7).
- **review-robustness** — FR-43: degenerate/absent/garbage `initialize` params and URIs, CR-6 config
  degradation at deferred bootstrap, no-usable-root path, and out-of-root request degradation.
- **review-docs** — Story 4 doc sync (README editor sections + CLAUDE.md project-state).
- **No `review-seam`** — no `internal/model`, Analyzer-interface, index/resolution-API, or
  cache-format change.

---

## Open questions — RESOLVED (user, 2026-07-14)

- **OQ-1 (T2/T3 seam shape) — RESOLVED: Variant A.** `Run` no longer takes a finished `root`/`cfg`;
  it takes the `cwdFallback` start (plus logger/analyzer) and performs `config.Bootstrap` **inside**
  the `initialize` handler, then constructs the store/watcher at that point. This changes `Run`'s
  signature — migrate every `Run` test-caller (`server_test.go`, robustness/diagnostics tests) that
  currently passes a `root` string. Accept the wider edit for the cleaner deferred-resolution design.

- **OQ-2 (T1 precedence) — RESOLVED: first workspaceFolder wins.** `resolveRootStart` prefers the
  first `params.WorkspaceFolders` entry even when `rootUri` differs (LSP-spec-aligned: workspaceFolders
  authoritative, rootUri deprecated), then `rootUri`, then `cwdFallback`.

- **OQ-3 (T6) — RESOLVED: implement `window/showMessage`.** Send a `window/showMessage` Warning
  notification on the no-usable-root / empty-index condition (in addition to the T5 stderr signal).
  No capability added (unilateral notification). T6 is IN scope.

- **OQ-4 (config discovery) — RESOLVED: client path is the sole discovery origin.** When a client
  root is present, `Bootstrap(start=clientPath, …)` runs `FindRoot` walk-up **from the client path**
  only; the process cwd's own `.natural-lsp.toml` sentinel is **not** consulted. A stray sentinel in
  the launch cwd no longer influences discovery when the client sends a root.
