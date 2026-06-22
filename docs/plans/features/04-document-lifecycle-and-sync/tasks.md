# Tasks: Document lifecycle & sync

**Feature plan:** [plan.md](./plan.md)
**PRD requirements:** FR-33 (P0, open-document handling), FR-34 (P1, external file watching)
**Upholds:** FR-43 (graceful degradation), ADR-009 (Full text sync), the Analyzer seam (NFR-15)
**Depends on:** Feature 03 (server lifecycle) — merged; `internal/server/Run` JSON-RPC loop is live.

---

## Current-state findings & impact

Surveyed `internal/server/`, `internal/document/`, `internal/analysis/`, `internal/config/`, the
protocol library (`go.lsp.dev/protocol@v1.0.0`), and the knowledge base. Findings that shape the plan:

### What already exists (reuse, don't rebuild)

- **`internal/server/server.go` — `Run(ctx, r, w, version, root, cfg, az, logger)`** has the full
  JSON-RPC 2.0 loop with a notification-dispatch `switch notif.Method()` (currently `initialized` /
  `exit` / default-ignore) and a request-dispatch `switch method`. New `textDocument/did*` handlers
  register as **notifications** in the notification switch. The per-request panic-recovery wrapper
  guards only the *request* switch; notifications currently have **no panic guard** — relevant to
  FR-43 (see Task 7).
- **`az analysis.Analyzer` is already threaded into `Run` but currently unused** (only `_ = bgCtx`
  and the lifecycle use the params). The document store is the first real consumer of `az` inside
  `Run`. No signature change to `Run` is needed for FR-33.
- **`internal/server/degradation.go` — `ProcessFiles` / `analyzeFile`** already implement the
  FR-43-safe single-file analysis path: size check vs `cfg.Workspace.MaxFileSize`, `cfg.IsExcluded`,
  unrecognized-extension → `ObjectUnknown`, and **`recover()` around `az.Analyze`**. The document
  store must route its re-analysis through this same logic so degradation guarantees hold uniformly.
  `analyzeFile` is unexported; `ProcessFiles` takes a batch (`paths`, `contents map`). Decide whether
  to reuse `ProcessFiles` for a single document or extract a shared single-file helper (see Task 3).
- **`internal/analysis/analyzer.go` — `Analyzer.Analyze(path, content) (model.FileAnalysis, error)`**
  is the seam. The document store hands content *to* `az`; it never imports `analysis/natural`. No
  seam change required — confirm purity in DoD.
- **`internal/config`** provides everything FR-34 filtering needs: `cfg.Workspace.Extensions`
  (ordered, normalized upper-case with leading dot), `cfg.Workspace.MaxFileSize`, and
  `cfg.IsExcluded(relPath)`. `degradation.go` already builds the recognized-extension set this way —
  reuse that pattern.
- **`go.lsp.dev/protocol`** supplies the typed params: `DidOpenTextDocumentParams` (→
  `TextDocumentItem{URI, Version, Text}`), `DidChangeTextDocumentParams` (→
  `VersionedTextDocumentIdentifier{URI, Version}` + `[]TextDocumentContentChangeEvent`),
  `DidCloseTextDocumentParams`, `DidChangeWatchedFilesParams` (→ `[]FileEvent{URI, Type}` with
  `FileChangeTypeCreated/Changed/Deleted`). `TextDocumentContentChangeEvent` is a **union interface**
  with two concrete impls: `*TextDocumentContentChangeWholeDocument{Text}` and
  `*TextDocumentContentChangePartial{Range, Text}`.
- **`go.lsp.dev/uri`** converts URIs to/from filesystem paths (`uri.File(path)`, `URI.Filename()`).
  Document store keys are LSP URIs; FR-34 filtering (exclude/extension) needs the workspace-relative
  *path* derived from the URI and `root`.

### Spec-vs-reality reconciliation (FR-33)

- **`initialize` already advertises `textDocumentSync: Full` (ADR-009).** This is locked by
  `TestInitialize`'s allow-list. Consequence: each `didChange` carries the **whole document text** in
  a single `*TextDocumentContentChangeWholeDocument` content change. The store does **not** need to
  apply incremental range edits — it replaces stored content wholesale. (If a client erroneously sends
  a `*TextDocumentContentChangePartial`, that is an open question — see Open questions.)

| FR-33 acceptance criterion | Disposition |
|---|---|
| On open, in-memory content is source of truth over disk | **New** — Task 4 (store) + Task 5 (didOpen handler) |
| On change, in-memory content updates; analysis refreshed | **New** — Task 6 (didChange handler); incremental re-index of *dependents* is **out of scope** (FR-35 / plan 05), only the changed file is re-analyzed |
| On close, server reverts to on-disk content | **New** — Task 6 (didClose handler removes the override) |
| Editor features against open unsaved doc reflect unsaved content | **Partially satisfied by the store contract** — Task 4 exposes a `Get`/lookup that downstream feature-09+ handlers will consult; no feature provider exists yet to assert end-to-end, so this is validated at the store/integration level (Task 8), not via an LSP query response |

### Spec-vs-reality reconciliation (FR-34)

- **No file watcher exists.** `internal/document/sync.go` is a stub. `internal/workspace/index.go` is a
  stub — **the index does not yet exist**, so "re-analysis of dependents" and "keeping the index
  consistent" (plan-05 territory) cannot be wired here. FR-34's *detection + filtering + per-file
  re-analysis dispatch* is in scope; *index update* is not.
- **The server advertises no `workspace` capability and no file-watching registration.** A real
  `fsnotify`-backed watcher is server-side and needs no capability. Client-pushed
  `workspace/didChangeWatchedFiles` requires either a static capability or dynamic registration
  (`client/registerCapability`) — the server currently does neither, and the choice (watcher vs.
  editor events vs. both) is an **open question** flagged in the plan.

| FR-34 acceptance criterion | Disposition |
|---|---|
| Added / modified / removed files in workspace + indexed set detected | **New** — Task 9 (watcher or `didChangeWatchedFiles` handler), pending Open-question A |
| Detected change triggers re-analysis of affected file(s) and dependents | **Partial** — Task 9 re-analyzes the changed file; *dependents* depend on the index (plan 05) and are out of scope |
| Changes in excluded dirs / non-indexed types ignored | **New** — Task 10 (event filtering, reuses `IsExcluded` + extension set) |
| Bulk on-disk change handled without overwhelming / wrong partial state | **New** — Task 11 (debounce/coalesce), pending Open-question B |

### Divergence / notes

- `cmd/natural-lsp/main.go` constructs `az := natural.New(nil)` and passes it to `Run`. No wiring
  change needed in `main.go` for FR-33; FR-34's watcher (if server-side) is spawned inside `Run` off
  `bgCtx` (already created for exactly this, with the ADR-012 cancel-on-shutdown hook) — no new
  goroutine plumbing into `main` required.
- Knowledge confirms `fsnotify` v1.10.1 (no native recursive watch; `Add` each indexed dir, `Add`
  new dirs on `Create`; debounce bursts) — relevant only if Open-question A resolves to a server-side
  watcher.

---

## Ordered task list

> FR-33 (Tasks 1–8) is **P0 and self-contained** — it should ship even if FR-34 is deferred. Tasks
> 9–11 (FR-34, P1) are gated on Open-question A and may be split into a follow-up if the watcher
> decision isn't ready.

### Task 1 — `document.Store`: type, construction, URI keying

**Behavior:** A concurrency-safe in-memory store keyed by LSP document URI. Construct an empty store;
look up a missing URI returns "not present".

**Reuses:** Replaces the `internal/document/store.go` stub. `go.lsp.dev/uri` for URI type.
**Fixtures:** none (pure unit; no `.NSx` needed).
**Expected result:** `New() *Store`; `Get(uri) (doc, ok bool)` returns `ok=false` for an unknown URI.
A stored document carries at least: URI, version, content (`[]byte`), and the last
`model.FileAnalysis`.
**Concurrency:** guard with `sync.RWMutex` (LSP notifications are processed on one loop today, but the
store will be read by future feature handlers and the FR-34 watcher goroutine — design for concurrent
readers now). `-race` in DoD.

- DoD: table-driven unit test; `-race`; `go vet`/`gofmt` clean; no import of `analysis/natural`;
  exported API documented.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: none

### Task 2 — `document.Store`: open / update / close content transitions

**Behavior:** Open stores content + version as the source of truth; update replaces content and bumps
version; close removes the override (subsequent `Get` returns `ok=false`, i.e. "fall back to disk").

**Reuses:** Task 1 store.
**Fixtures:** none (content is arbitrary bytes supplied by the test).
**Expected result:**
- `Open(uri, version, content)` → `Get` returns the content, `ok=true`.
- `Update(uri, version, content)` → `Get` returns the new content/version; updating an unopened URI is
  handled deterministically (open-on-update or ignore — pick and document; LSP guarantees didOpen
  precedes didChange, so ignore-with-log is acceptable).
- `Close(uri)` → `Get` returns `ok=false` (reverts to disk per FR-33).
- Idempotent/duplicate close does not panic.

- DoD: table-driven transitions; `-race`; vet/gofmt; deterministic.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Task 1

### Task 3 — Single-file FR-43-safe analysis helper (refactor of `degradation.go`)

**Behavior:** Extract the per-file logic in `analyzeFile`/`ProcessFiles` (size check, exclude check,
extension recognition, panic-recovered `az.Analyze`) into a reusable single-file function the document
store and the watcher both call, so degradation rules are not duplicated.

**Reuses / migrates:** `internal/server/degradation.go`. Existing `ProcessFiles` must keep working
(re-implement it in terms of the extracted helper, or have the helper sit alongside). `degradation_test.go`
and `FuzzProcessFile` must stay green.
**Fixtures:** reuse `testdata/objecttype/` fixtures already exercised by `degradation_test.go`.
**Expected result:** a function (e.g. `analyzeOne(cfg, az, relPath, content, logger) FileProcessResult`)
returning `ObjectType`, `SkipReason`, `FileAnalysis`; `ProcessFiles` delegates to it; behavior
(including log lines with `path`+`reason`) is unchanged.
**Decision point:** if the store lives in `internal/document`, this helper may belong there or stay in
`server` and be passed a callback — note the seam: the helper depends only on `analysis.Analyzer` +
`config`, never `analysis/natural`. (See Open-question C on package placement.)

- DoD: existing `degradation_test.go` + `FuzzProcessFile` green; new helper unit-tested directly;
  `-race`; vet/gofmt; seam purity preserved.
- Agents: `tdd-red` (characterization test on extracted helper) → `tdd-green` → `tdd-refactor`
- Depends on: none (can run in parallel with Tasks 1–2)

### Task 4 — Store re-analyzes on open/update; exposes latest `FileAnalysis`

**Behavior:** When a document is opened or updated, the store runs the FR-43-safe single-file analysis
(Task 3) over the new content and caches the resulting `model.FileAnalysis`, so a `Get` exposes both
the unsaved content **and** its analysis. A panic in `az.Analyze` must not propagate out of the store
(degradation).

**Reuses:** Task 2 store + Task 3 helper.
**Fixtures:** a minimal recognized `.NSP` and a deliberately malformed/garbage file under
`testdata/` (reuse an existing objecttype fixture for the happy path; add one minimal malformed
fixture only if none exists).
**Expected result:**
- Open/Update of a recognized file → stored `FileAnalysis.ObjectType` matches the extension.
- Open/Update of content that makes `az.Analyze` panic → store still holds the content,
  `FileAnalysis` is the zero/`ObjectUnknown` value, no panic escapes, recovery is logged.
- Store needs `cfg` + `az` + `logger` at construction (extend `New`); derive workspace-relative path
  from URI + `root` for the exclude/size checks.

- DoD: `-race`; table-driven incl. panic case; vet/gofmt; seam purity; degradation held.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Tasks 2, 3

### Task 5 — `textDocument/didOpen` handler wired into `Run`

**Behavior:** Register `textDocument/didOpen` in the notification switch in `server.go`. Decode
`DidOpenTextDocumentParams`, call `store.Open(uri, version, []byte(text))`. Construct the store once
in `Run` (off `cfg`/`az`/`logger`, before the loop) and keep it for the connection's lifetime.

**Reuses:** Task 4 store; existing notification-dispatch structure; `az` already passed to `Run`.
**Fixtures:** none at the handler level (drives a JSON `didOpen` notification, like `server_test.go`'s
`jsonrpc2.NewNotification`).
**Expected result:** after a `didOpen`, the store reports the document present with the sent content;
malformed `didOpen` params are logged and ignored (notifications get no JSON-RPC error response).
**Protocol:** didOpen is valid only after `initialized`; received in `statePreInit` it should be
ignored/logged (consistent with current notification handling). Confirm `TestInitialize` allow-list is
**not** affected (sync capability already advertised — no change).

- DoD: integration-style test driving the notification through `Run` (extend `server_test.go`
  patterns); `-race`; vet/gofmt; protocol-conformant (no response to a notification).
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Task 4

### Task 6 — `textDocument/didChange` and `textDocument/didClose` handlers

**Behavior:** `didChange` decodes `DidChangeTextDocumentParams`; since sync kind is **Full**, take the
`Text` from the (single) `*TextDocumentContentChangeWholeDocument` content change and call
`store.Update(uri, version, content)`. `didClose` decodes `DidCloseTextDocumentParams` and calls
`store.Close(uri)`, reverting to disk.

**Reuses:** Task 5 wiring + Task 4 store.
**Fixtures:** none at handler level.
**Expected result:**
- didOpen → didChange sequence: store reflects the changed content/version.
- didOpen → didClose sequence: store reports the document absent (`ok=false`).
- A `didChange` whose content change is a `*TextDocumentContentChangePartial` (range edit) under
  Full-sync is unexpected — handle defensively: log and skip rather than mis-apply (ties to
  Open-question D).
- Empty `contentChanges`, or didChange/didClose for an unknown URI: logged, no panic.

- DoD: table-driven over sequences; `-race`; vet/gofmt; protocol conformance.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Task 5

### Task 7 — Panic guard for notification dispatch (FR-43)

**Behavior:** The request-dispatch switch in `server.go` is wrapped in `recover()`; the
notification-dispatch path is **not**. A panic while handling `didOpen/didChange/didClose` (e.g. a
nil-map deref) must not crash the server loop — recover, log, and continue reading.

**Reuses / migrates:** mirror the existing request-side recovery closure in `server.go`. Notifications
have no `id`, so there is **no** error response to send — recovery is log-and-continue only.
**Fixtures:** none; use a synthetic panic trigger analogous to the existing `test/panic` request case,
or inject a panicking analyzer via the store.
**Expected result:** a notification that panics during dispatch is logged (`logger.Error`) and the
loop continues to process the next message; a subsequent valid request still gets a response.

- DoD: test drives panic-then-valid-request and asserts loop survival; `-race`; vet/gofmt; FR-43 held.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Task 6 (a real notification handler that can panic)

### Task 8 — FR-33 end-to-end integration: open → change → close source-of-truth

**Behavior:** Drive a full lifecycle (`initialize` → `initialized` → `didOpen` → `didChange` →
`didClose` → `shutdown` → `exit`) through `Run` and assert the store's view of source-of-truth at each
step matches FR-33's criteria (open overrides disk, change updates, close reverts).

**Reuses:** the `server_test.go` harness (`stubAnalyzer`, `jsonrpc2.NewNotification`, framed buffers).
A test-only accessor or hook to inspect the store may be needed — prefer asserting via an observable
side effect (e.g. an analyzer spy recording the content it was handed) over exporting internals.
**Fixtures:** one minimal recognized `.NSP` (reuse objecttype fixture).
**Expected result:** the analyzer receives the open content on didOpen, the changed content on
didChange, and is not invoked / store cleared on didClose. Clean shutdown, exit code path unaffected.

- DoD: integration test; `-race`; vet/gofmt; FR-33 criteria each mapped to an assertion.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Tasks 5, 6

---

### FR-34 tasks (P1 — gated on Open-question A)

### Task 9 — External-change detection + per-file re-analysis dispatch

**Behavior:** Detect added/modified/removed indexed files in the workspace and dispatch FR-43-safe
re-analysis of each changed file. Mechanism per Open-question A:
- **(A1) server-side `fsnotify` watcher:** spawn off `bgCtx` inside `Run` (uses the existing
  ADR-012 cancel-on-shutdown context). Walk indexed dirs and `Add` each (no native recursion — per
  knowledge base); `Add` new dirs on `Create`. On event, read the file and run the Task-3 helper.
- **(A2) client-pushed `workspace/didChangeWatchedFiles`:** add the notification handler + advertise
  the capability / register `client/registerCapability` with a glob for the indexed extensions; on
  each `FileEvent`, dispatch re-analysis (delete → drop from store/index).

**Reuses:** Task 3 helper; `internal/document/sync.go` stub (the watcher home); `bgCtx`.
**Fixtures:** an `fstest.MapFS` or a temp dir with `.NSP` files (knowledge: `fstest.MapFS` makes this
testable without real disk for the filtering logic; the watcher itself needs a temp dir).
**Expected result:** creating/modifying/deleting an indexed file in a watched dir triggers exactly one
re-analysis dispatch for that file with the correct content (or a removal for delete).
**Out of scope:** updating a cross-file index / re-analyzing *dependents* — the index doesn't exist
yet (plan 05). This task only proves detection + single-file dispatch.

- DoD: `-race` (watcher is concurrent); vet/gofmt; seam purity; degradation held; deterministic test
  (poll/await the dispatch rather than sleep).
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Task 3; **blocked on Open-question A**

### Task 10 — Filter external events to workspace + indexed set

**Behavior:** Ignore events under excluded directories (`cfg.IsExcluded`) and for non-indexed
extensions (`cfg.Workspace.Extensions`), and events outside the workspace root. Mirrors the
recognized-extension set + exclude logic already in `degradation.go`.

**Reuses:** `cfg.IsExcluded`, the extension-set construction pattern from `degradation.go`/Task 3;
`uri`/`root` path derivation.
**Fixtures:** temp dir with one indexed file, one file under an excluded dir, one non-indexed
extension.
**Expected result:** only the indexed, non-excluded, in-root file triggers re-analysis; the others are
dropped silently (or debug-logged), never dispatched.

- DoD: table-driven over (excluded / non-indexed / out-of-root / valid); `-race`; vet/gofmt.
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Task 9

### Task 11 — Debounce / coalesce bulk changes

**Behavior:** A burst of events (e.g. a branch checkout touching many files, or an editor's
rename = remove+create) is coalesced so the server isn't overwhelmed and doesn't produce incorrect
partial state. Per knowledge base: editors emit bursts; debounce/coalesce per-path.

**Reuses:** Task 9 watcher loop.
**Fixtures:** synthetic burst of events in a test (time-controlled, not wall-clock sleeps).
**Expected result:** N rapid events for the same path within the debounce window collapse to a single
re-analysis with the final content; a large multi-file burst processes all distinct paths without
unbounded concurrent analysis (bounded worker / serialized drain).

- DoD: `-race`; deterministic timing (injectable clock or channel-driven, no real sleeps); vet/gofmt;
  no goroutine leak (watcher exits on `bgCtx` cancel).
- Agents: `tdd-red` → `tdd-green` → `tdd-refactor`
- Depends on: Tasks 9, 10; **shaped by Open-question B**

---

## Reviews required (`/review-feature`)

- **review-protocol** — new LSP methods (`textDocument/didOpen|didChange|didClose`, possibly
  `workspace/didChangeWatchedFiles`): notification semantics (no response), Full-sync handling,
  capability advertisement if A2 is chosen, lifecycle gating (post-`initialized`).
- **review-concurrency** — the `Store` is shared across the request loop and (FR-34) a watcher
  goroutine; the watcher runs off `bgCtx`. `-race`, no goroutine leak on shutdown, debounce
  correctness.
- **review-robustness / degradation** — FR-43: malformed params, oversized/excluded/unrecognized
  content, analyzer panics on open/change and on watched-file events; the new notification panic guard
  (Task 7); reuse of the `FuzzProcessFile`-guarded path.
- **review-seam** — confirm the document store and watcher depend only on `analysis.Analyzer` +
  `internal/model` + `internal/config`, never on `analysis/natural`. (No `internal/model` or
  `Analyzer`-interface contract change is planned — flag if any task introduces one.)
- **review-docs** — feature adds document-sync capability and (FR-34) external watching; `CLAUDE.md`
  "Project state" and `README.md` must be updated at `/finalize-feature` to mark `internal/document`
  as implemented and describe the sync model.

---

## Open questions

- **A — FR-34 detection mechanism (plan open question).** Server-side `fsnotify` watcher (A1),
  client-pushed `workspace/didChangeWatchedFiles` (A2), or both? This gates Tasks 9–11 and decides
  whether a capability/dynamic-registration is added. Knowledge base favors `fsnotify` v1.10.1 for the
  watcher path. **Recommendation:** A1 (server-side watcher) for editor-agnostic correctness, since
  not all clients send watched-file events; optionally also accept A2 events if sent. Needs user
  decision before Tasks 9–11.
- **B — Debounce/coalesce window (plan open question).** What window/strategy for Task 11 (fixed
  debounce interval? coalesce-per-path? bounded worker pool)? Affects Task 11's design and test.
- **C — Package placement of the document store and single-file helper.** Store clearly belongs in
  `internal/document`. The FR-43 single-file helper currently lives in `internal/server/degradation.go`;
  reusing it from `internal/document` means either moving it (and migrating `degradation_test.go` /
  `FuzzProcessFile`) or having `server` own the store. Either keeps the seam intact; pick to minimize
  churn. Recommendation: keep the helper in `server` if the store also lives in `server`, else lift the
  helper to a neutral spot both import. Decide at Task 3.
- **D — Defensive handling of a `Partial` content change under Full sync.** Under advertised
  Full-sync the spec says clients send whole-document changes; if a client sends a range
  (`*TextDocumentContentChangePartial`) anyway, do we (i) log-and-skip (Task 6 default), (ii) treat
  `.Text` as the whole document, or (iii) implement minimal range application? Recommendation: (i) for
  this release; revisit if/when ADR-009 moves to Incremental.
- **E — "Dependents" re-analysis (FR-34 criterion 2).** The criterion says "re-analysis of the
  affected file(s) **and dependents**," but no cross-file index exists yet (plan 05). Confirm that
  this feature scopes to single-file detection + dispatch, deferring dependent re-analysis to plan 05.
  (Planned that way here.)
