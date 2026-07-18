# Tasks: Async Indexing & Work-Done Progress

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-32 (P0 — indexing progress reporting), NFR-5 (P1 — indexing must not block responsiveness)
**Depends on:** feature 05 (workspace indexing/cache — `Build(onProgress)` shipped), feature 20 (workspace-root handshake — restructured the initialize/initialized path)

This feature is **server-wiring only**: no `internal/model` change, no `Analyzer`-seam change, no
cache-format bump. All work lives in `internal/server/` (LSP-facing side of the seam), consuming the
already-shipped `workspace.Build(..., onProgress)` callback.

---

## Current-state findings & impact

Verified against `main` after features 19 and 20 merged (the 2026-07-14 assessment snapshot is stale —
feature 20 moved bootstrap into the `initialize` handler and added `reportNoUsableRoot`).

### Where the index build lives NOW (post-feature-20)

- **`internal/server/server.go`** — the lifecycle is split across two handlers:
  - The **`initialize` request handler** (`server.go` ~667–740) runs `config.Bootstrap` (deferred,
    Variant A), stores `hctx.root`/`hctx.cfg`, records `hctx.probe` (root-discovery inputs), constructs
    the `document.Store` (`document.New`, keyed on the negotiated root), and starts the fsnotify
    `document.Watcher` against `bgCtx`. All of this is fast, no index build.
  - The **`initialized` notification handler** (`server.go` ~405–487) sets `state = stateInitialized`
    (line 410) and then, **synchronously on the single-threaded dispatch goroutine**, calls
    `workspace.Build(hctx.root, hctx.cfg, az, logger, nil)` (**line 416** — `onProgress` is `nil`),
    then `workspace.Resolve(hctx.idx, &hctx.cfg)` (line 428), publishing `hctx.idx`/`hctx.res` directly
    (no lock — safe today because nothing else runs concurrently). It then calls
    `hctx.reportNoUsableRoot(ctx, stream)` (line 437, feature 20), fires the `indexReadyHook`, and sends
    the `client/registerCapability` request for watched files (~451–486).
  - **This synchronous build on the dispatch loop is exactly the NFR-5 defect**: a cold index stalls
    the loop, so every request queued behind `initialized` waits until the build finishes.

- **`internal/server/progress.go`** — a 6-line TODO stub (`// TODO: progress begin/report/end helpers.`).
  This is where the FR-32 work-done-progress helpers land (Story 3 retires the stub in favor of the real
  helpers).

### The workspace-side callback is already done (feature 05, task 05-S01)

- **`internal/workspace/index.go:377`** — `Build(root, cfg, az, logger, onProgress func(path string,
  current, total int)) (*Index, error)` already accepts the callback and delegates to
  `BuildWithCache(..., cachePath, currentHashes, onProgress)` (line 397).
- **`index.go:478-480`** — inside `BuildWithCache`, `onProgress(relPath, i+1, totalFiles)` is invoked
  **once per file in sorted order**, with `current = i+1` (1-based) and `total = totalFiles`. Confirmed
  signature: `func(path string, current, total int)` — `path` is the workspace-**relative** path,
  `current` is 1-based, `total` is the full matched-file count (known up front, before the loop).
- **`index_test.go:306` `TestBuild_ProgressCallback`** already proves the callback fires per file with
  accurate counts. The LSP-wiring half (feature-05 task 05-I02) was never implemented — **that is this
  feature.** No workspace change is needed; this feature only supplies a non-nil callback.

### Cache is NOT wired into the server today

- The server calls `workspace.Build` (no `cachePath`), so it always does a **cold** build. `BuildWithCache`
  with a real `cachePath` (warm start) is exercised only in tests, never by `Run`/`cmd/natural-lsp`.
  Story 1 AC4 ("warm starts served from cache emit begin → end without a misleading long report phase")
  therefore **cannot be exercised via the server's own build path** as-built — see Open Question OQ-E.
  The progress helper is designed to be correct whether the build reports 0, 1, or N files; the "warm"
  path is realized by the callback simply reporting few/zero stale files (`onProgress` still fires per
  *scanned* file — see OQ-E for the honesty concern).

### Concurrency model & the F7 pattern to mirror

- The dispatch loop is **strictly serial** — notification/request handlers run inline on the read
  goroutine, never in spawned goroutines (`server.go` `for { ... }`).
- **`applyDocumentChange`** (`server.go:1202-1229`) is the F7 build-then-publish reference: re-analyze
  off-lock, then `hctx.idxResMu.Lock()`, `idx.Add`, swap `hctx.res = workspace.ResolveInto(...)`, unlock.
  The background index build must publish the same way: build the fresh `(idx, res)` off-loop, then take
  `idxResMu.Lock()` once to publish both pointers atomically.
- **`bgCtx`** (`server.go:272`, ADR-012) is derived from the caller ctx and **cancelled in the `shutdown`
  handler** (`server.go:748 bgCancel()`), not just deferred. The background build goroutine must be tied
  to `bgCtx` so shutdown cancels it, and must not publish after cancellation (no publish-after-shutdown).

### Providers already degrade gracefully before the index publishes

- `definition.go:38` (`idx == nil || res == nil → nil`), `workspace_symbols.go:44`, `completion.go`,
  `code_lens.go`, `hover.go`, `call_hierarchy.go` all guard on `idx == nil` and return null/empty.
- **`document_symbols.go:33`** serves the **open-document store first** (`hctx.store.Get`), returning the
  buffer's `Analysis.Structure` **without touching the index** — so `documentSymbol` on an open buffer
  answers correctly even while the background build is in flight and `idx` is still nil. This is the
  ideal probe for Story 2 AC3. The store is constructed at `initialize`, so it is non-nil by the time any
  `didOpen`/request arrives after `initialized`.

### Protocol types (verified against `go.lsp.dev/protocol@v1.0.0`)

- Client capability: `params.Capabilities.Window` is `*protocol.WindowClientCapabilities`
  (`lifecycle.gen.go:278`), whose `WorkDoneProgress *bool` (`lifecycle.gen.go:540`) advertises
  server-initiated progress support. Gate exactly like `clientSupportsWatchedFilesReg` does for
  `Workspace.DidChangeWatchedFiles.DynamicRegistration` (nil-chain + deref).
- `protocol.WorkDoneProgressCreateParams{ Token ProgressToken }` (`window_features.gen.go:126`).
- `protocol.ProgressParams{ Token ProgressToken; Value LSPAny }` (`window_features.gen.go:138`).
- `protocol.ProgressToken` is an **interface** (`types_unions.gen.go:350`) satisfied by
  `protocol.Integer` and `protocol.String`. Use a stable `protocol.String("natural-lsp-index")` token.
- `protocol.WorkDoneProgressBegin{ Kind, Title, Cancellable *bool, Message *string, Percentage *uint32 }`,
  `WorkDoneProgressReport{ Kind, Cancellable, Message, Percentage }`,
  `WorkDoneProgressEnd{ Kind, Message *string }` (`basic_structures.gen.go:585/617/644`). `Kind` is the
  discriminator: `"begin"`/`"report"`/`"end"`. `Percentage` is `[0,100]`.
- **Marshaling (feature 19 unified on json/v2 — no stdlib `json.Marshal` for protocol types):**
  - The `WorkDoneProgressCreateParams` for the `window/workDoneProgress/create` **request** marshals via
    its `MarshalJSONTo(jsontext.NewEncoder(&buf))` — mirror `sendShowMessage` (`server.go:1174`) and the
    `client/registerCapability` params path (`server.go:472-474`).
  - `$/progress` `ProgressParams.Value` is `LSPAny`. Build the begin/report/end struct, wrap it into
    `LSPAny` via the existing **`mustLSPAny` helper** (`code_lens.go:180`, `gojson.Marshal`), then marshal
    the `ProgressParams` via its `MarshalJSONTo`. (Confirm in T2's green phase that `ProgressParams` has a
    `MarshalJSONTo`; if not, marshal the whole notification body via `gojson.Marshal(params)` per the
    `marshalResult` path.)

### Server→client request precedent (for the `create` request)

- `client/registerCapability` (`server.go:451-486`) is the existing server-initiated **request**: it
  builds params, marshals via `MarshalJSONTo`, wraps in `jsonrpc2.NewCall(stringID, method, raw)`, and
  `stream.Write`s it. The client's response is handled in the **Response branch** (`server.go:625-635`),
  which currently only logs (`Debug`/`Warn`) — it does **not** correlate the response back to any waiter.
  So today there is **no mechanism to await** a server-initiated request's response inside the dispatch
  loop. This is the crux of OQ-A.

### Stub / stale-scaffolding inventory (Story 3)

- `internal/server/progress.go` — TODO stub (retire → real helpers).
- `internal/server/handlers.go` — package-doc-only stub (`// TODO: definition, references, ... FR-24..29`),
  now stale (all those providers shipped). Clean up.
- `server.go:751-760` `case "test/panic"` and `server.go:488-496` `case "test/panic-notification"` carry
  a "will be removed once features 09–13 land" comment. Those features landed; the comment is stale.
  These cases still back `TestRequestPanicRecovery`/`TestNotificationPanicRecovery`, so **keep the cases,
  correct the comments** (do not delete the test hooks).

### Criteria already satisfied (skipped, with note)

- **Story 1 AC2** (no progress when client omits `window.workDoneProgress`, but indexing still proceeds)
  — the capability-gating *pattern* exists (watched-files registration); this task applies it. Not
  pre-satisfied, but trivially built by mirroring. Covered by T1/T6.
- **Story 2 AC2** (atomic publish under `idxResMu`) — the F7 mechanism exists in `applyDocumentChange`;
  T4 reuses it, does not reinvent it.

---

## Ordered task list

Order: progress helpers (T1–T2) → background build + F7 publish (T3–T4) → capability gating & the
create-request sequencing (T5–T6) → wire the callback to progress + warm-cache honesty (T7) →
over-the-wire sequence test (T8) → concurrency/shutdown tests (T9) → Story 3 cleanup (T10).

T1–T2 and T3–T4 are independent tracks that converge at T7; if parallelizing, keep T7 after both.

---

### T1 — Detect `window.workDoneProgress` client capability

**Behavior:** In `handleInitialize`, parse whether the client advertises
`params.Capabilities.Window.WorkDoneProgress == true` and return it as a new bool (mirror the existing
`clientSupportsWatchedFilesReg` return value and its `Run`-local variable). Store it so the `initialized`
handler can gate progress.

**Fixtures:** none (pure param decoding; drive with in-test `InitializeParams`).

**Expected result:**
- Client sends `capabilities.window.workDoneProgress = true` → flag is `true`.
- Client omits `window`, or `window.workDoneProgress` absent/`false`/`null` → flag is `false`.

**Reuses/migrates:** extends `handleInitialize`'s signature (already returns
`([]byte, PositionEncodingKind, bool, error)`) — add a second bool OR return a small struct to avoid a
confusing two-bool signature (recommend a `initializeNegotiation` struct; flag as a refactor of the
call-site in `Run` ~679). Mirror the nil-chain deref at `server.go:156-161`.

**DoD:**
- [ ] Table-driven unit test over `handleInitialize` covering advertised / absent / explicit-false.
- [ ] Call-site in `Run` updated; `TestInitialize` allow-list still green (no capability *added* to the
      server's advertised set — progress needs no server capability, like publishDiagnostics).
- [ ] `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** none

---

### T2 — Progress helper: `progressReporter` (create/begin/report/end senders)

**Behavior:** Replace the `progress.go` stub with a small helper that, given a `jsonrpc2.Stream`, a
`context.Context`, a logger, and a `ProgressToken`, can send:
- the `window/workDoneProgress/create` **request** (server→client `jsonrpc2.NewCall`),
- a `$/progress` **notification** carrying `WorkDoneProgressBegin` (Title `"Indexing Natural workspace"`,
  optional `Percentage`),
- a `$/progress` `WorkDoneProgressReport` (percentage + a `"N/M files"` message),
- a `$/progress` `WorkDoneProgressEnd` (final message).

All senders marshal via the json/v2 path (`MarshalJSONTo` for params; `mustLSPAny` for the `Value`
payload). Write failures are **logged, never fatal** (FR-43). A **disabled** reporter (client didn't
advertise support) is a no-op object so callers need no `if` at every call site.

**Fixtures:** none (assert on the framed bytes written to a `bytes.Buffer` stream).

**Expected result (exact wire shape):**
- create → `{"jsonrpc":"2.0","id":<...>,"method":"window/workDoneProgress/create","params":{"token":"natural-lsp-index"}}`
- begin → `{"jsonrpc":"2.0","method":"$/progress","params":{"token":"natural-lsp-index","value":{"kind":"begin","title":"Indexing Natural workspace",...}}}`
- report → `value.kind == "report"`, `value.message == "37/128 files"`, `value.percentage == 28` (integer,
  clamped to `[0,100]`; guard divide-by-zero when `total == 0` → omit percentage / infinite progress).
- end → `value.kind == "end"`, optional `message`.
- A disabled reporter writes **nothing** to the stream for any call.

**Reuses/migrates:** `mustLSPAny` (`code_lens.go:180`), the `sendShowMessage` marshal-and-write shape
(`server.go:1174`), `jsonrpc2.NewCall`/`NewNotification`, `jsonrpc2.NewStringID`. Token type
`protocol.String("natural-lsp-index")`.

**DoD:**
- [ ] Unit tests decode the framed output and assert `kind`, `title`, `message`, `percentage`, token, and
      method per call; a disabled reporter emits zero bytes.
- [ ] Percentage rounding is deterministic and clamped; `total == 0` path never divides by zero.
- [ ] Write/marshal failure logs and returns without panicking (inject a failing writer).
- [ ] `go vet`/`gofmt` clean; no stdlib `json.Marshal` on protocol types.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** none (independent of T1)

---

### T3 — Extract the index build into a reusable `buildAndPublishIndex` method

**Behavior:** Factor the current inline build (`server.go:415-429`) into a method on `handlerContext`,
e.g. `func (hctx *handlerContext) buildIndex(onProgress func(path string, current, total int)) (*workspace.Index, *workspace.ResolutionSet, error)` that runs `workspace.Build` + `workspace.Resolve`
**without** touching `hctx.idx`/`hctx.res` (pure build, no publish), and a separate
`publishIndex(idx, res)` that takes `idxResMu.Lock()` and swaps both pointers atomically (mirror
`applyDocumentChange`'s publish half). At this task the `initialized` handler still calls them
**synchronously** (behavior-preserving refactor) — the goroutine move is T4.

**Fixtures:** reuse an existing multi-file server fixture (e.g. a `t.TempDir()` with a couple `.NSP`/`.NSN`
files, as in `TestTextDocumentDefinitionNoEdge`).

**Expected result:** identical index/resolution contents to today; `indexReadyHook` still fires with a
populated index; all existing server tests stay green.

**Reuses/migrates:** the publish half mirrors `applyDocumentChange` (`server.go:1202-1229`). No behavior
change — this is the seam that T4 makes asynchronous.

**DoD:**
- [ ] Existing lifecycle tests (`TestFramedTransport`, `TestTextDocumentDefinitionNoEdge`,
      `indexReadyHook` observers, feature-20 `initializeReadyHook`/`reportNoUsableRoot` tests) green.
- [ ] `publishIndex` holds `idxResMu.Lock()` for the atomic swap; no torn pair observable.
- [ ] `go vet`/`gofmt` clean; `go test -race ./internal/server/` green.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** none

---

### T4 — Run the initial build on a background goroutine (NFR-5)

**Behavior:** In the `initialized` handler, after `state = stateInitialized`, spawn the build on a
goroutine tied to `bgCtx`: it calls `buildIndex(...)` off the dispatch loop, then `publishIndex(idx, res)`
under `idxResMu.Lock()`, then fires the `indexReadyHook` and `reportNoUsableRoot`. The dispatch loop
returns from the `initialized` handler immediately and keeps servicing messages. **Before publishing,
check `bgCtx.Err()`** — if cancelled (shutdown raced the build), skip publish and return (no
publish-after-shutdown). The goroutine must not leak: on `bgCtx` cancellation the in-flight
`workspace.Build` runs to completion (it is not itself cancellable — see OQ, acceptable for MVP) but the
publish/hook/report are skipped.

**Sequencing decision (see OQ-D):** `reportNoUsableRoot` currently runs inline after the sync build.
Move it **inside the goroutine, after publish**, so the no-usable-root signal still fires exactly once
and reflects the built index's file count. The `client/registerCapability` request (~451) stays on the
**dispatch loop** (it does not depend on the index) so watched-files registration is not delayed by the
build.

**Fixtures:** a multi-file `t.TempDir()` fixture; plus a **slow analyzer stub** (a `stubAnalyzer`
variant whose `Analyze` blocks on a channel/sleep) to make the build observably in-flight — add to the
test file, not production.

**Expected result:**
- `initialized` handler returns before the build completes (assert the dispatch loop processes a
  subsequent message while the slow build is still blocked).
- After the build completes, `hctx.idx`/`hctx.res` are populated exactly as in T3 (via `indexReadyHook`).
- Shutdown during an in-flight build cancels `bgCtx`; no publish occurs after cancellation; `Run` returns
  cleanly; `go test -race` reports no data race and no goroutine leak (goroutine observes cancellation).

**Reuses/migrates:** `bgCtx`/`bgCancel` (ADR-012, `server.go:272,748`), `publishIndex` (T3),
`indexReadyHook`, `reportNoUsableRoot`.

**DoD:**
- [ ] Test proves the loop services a message (e.g. a `documentSymbol` on an open buffer, or a `shutdown`)
      while the slow build is blocked — see T9 for the responsiveness assertion; this task's DoD is the
      goroutine + guarded publish mechanics.
- [ ] `bgCtx.Err()` checked before publish; no publish-after-shutdown (unit test with a build that
      finishes after `bgCancel`).
- [ ] `go test -race ./internal/server/` green; no goroutine leak (goroutine exits on ctx cancel).
- [ ] Existing `indexReadyHook`/`reportNoUsableRoot` tests adapted for async timing (hook may now fire
      after `initialized` returns — tests must wait on a signal, not assume synchronous).
- [ ] `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** T3

---

### T5 — Sequence the `create` request before `$/progress` (resolve OQ-A)

**Behavior:** Implement the chosen approach from OQ-A (recommended: **fire-and-forget create, then
begin** — send `window/workDoneProgress/create`, then immediately send `begin`, without blocking the
dispatch loop awaiting the response). Because the serial dispatch loop cannot both block on a response
and keep reading, and because the LSP progress flow tolerates the server sending progress right after
issuing create when the client advertised `window.workDoneProgress`, the reporter sends
create → begin → report* → end in order on the background goroutine. The create **response** continues to
be handled by the existing Response branch (`server.go:625-635`), which logs it; a create rejection is
logged at Warn (progress may then be ignored by the client — acceptable, non-fatal, FR-43).

**Fixtures:** none.

**Expected result:** the ordered wire sequence for a supporting client is
`create (request)` → `$/progress begin` → `$/progress report`* → `$/progress end`, all carrying the same
token. No response-await deadlock; the loop keeps reading throughout.

**Reuses/migrates:** T2 reporter; existing Response branch. **Do not** add a correlation/wait mechanism
unless OQ-A is decided in favor of the await variant — that would be a larger change to the dispatch loop
(flag it in Reviews if chosen).

**DoD:**
- [ ] Test asserts create precedes begin in the output stream (ordering), same token throughout.
- [ ] Dispatch loop is never blocked awaiting the create response (covered by T9 responsiveness test).
- [ ] `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** T2, T4

---

### T6 — Gate progress on the client capability (Story 1 AC2)

**Behavior:** In the `initialized` handler, construct the reporter as **enabled** only when the T1 flag is
true; otherwise construct the **disabled** (no-op) reporter. Either way, the background build (T4) runs.
So a non-supporting client gets async indexing with **zero** progress messages; a supporting client gets
the full create/begin/report/end sequence.

**Fixtures:** reuse a multi-file `t.TempDir()`.

**Expected result:**
- Client without `window.workDoneProgress` → output stream contains **no** `window/workDoneProgress/create`
  and **no** `$/progress`; the index still builds and `indexReadyHook` still fires.
- Client with `window.workDoneProgress` → full sequence present.

**Reuses/migrates:** T1 flag, T2 reporter (enabled/disabled), the `clientSupportsWatchedFilesReg`
capability-gating pattern.

**DoD:**
- [ ] Two-branch test (supporting / non-supporting client) asserts presence/absence of create+progress.
- [ ] Non-supporting client still reaches a populated index.
- [ ] `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** T1, T5

---

### T7 — Wire the `Build` callback to `report` + warm-cache honesty (Story 1 AC1, AC4)

**Behavior:** Pass a non-nil `onProgress` into the build (replacing the `nil` at the former `server.go:416`)
that forwards `(path, current, total)` into `reporter.report(current, total, path)`. The callback fires
on the **background goroutine** (T4), so the reporter's writes happen off the dispatch loop — confirm
`stream.Write` is safe to call from the goroutine (it is: `jsonrpc2.Stream` writes are independent of the
read loop; document this). Address AC4 (warm cache): the report message is derived from
`current`/`total` the callback provides; for a warm/mostly-cached build the callback still fires per
*scanned* file (fast), so `begin → end` bracket a short report burst rather than a long phase. See OQ-E
for whether to suppress reports below a file-count/elapsed threshold.

**Fixtures:** a multi-file `t.TempDir()` fixture (>=3 indexed files) so multiple `report`s fire with a
rising count.

**Expected result:**
- Supporting client: `report`s carry rising `current`/`total` (e.g. `"1/3 files"`, `"2/3 files"`,
  `"3/3 files"`) and non-decreasing percentages; `end` fires exactly once after the last file.
- Progress reflects **files-processed count** from the real `Build` callback (AC1).

**Reuses/migrates:** `workspace.Build(..., onProgress)` (already accepts the callback), the T2 reporter.

**DoD:**
- [ ] Test decodes the `$/progress` stream and asserts a rising report sequence matched to fixture file
      count and a single terminal `end`.
- [ ] `total == 0` (empty root) → begin then end, no report, no divide-by-zero (ties to feature-20
      no-usable-root path).
- [ ] Callback runs on the background goroutine; `go test -race` green (writes from the goroutine do not
      race the read loop).
- [ ] `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** T4, T5, T6

---

### T8 — `TestServer_ProgressReporting`: full create → begin → report → end over the wire (Story 1 AC3)

**Behavior:** The end-to-end lifecycle test the feature-05 plan specified but never got. Drive
`initialize` (advertising `window.workDoneProgress`) → `initialized` → shutdown → exit through the
pre-fed-buffer harness (as `TestTextDocumentDefinitionNoEdge` does), then decode **all** framed outgoing
messages and assert the ordered progress sequence for a multi-file fixture.

**Fixtures:** a `t.TempDir()` with >=2 indexed `.NSx` files.

**Expected result:** outgoing messages include, in order and sharing one token:
`window/workDoneProgress/create` (request) → `$/progress` begin → `$/progress` report(s) → `$/progress`
end. (Because the pre-fed harness runs the build to completion before `Run` returns, the async goroutine
still completes before EOF — verify the harness drains the goroutine; if not deterministic, use the live
goroutine harness from T9.)

**Reuses/migrates:** `writeFramedMessage`/output-decoding test helpers in `server_test.go`; the
`indexReadyHook` (to know the build finished).

**DoD:**
- [ ] Ordered sequence asserted with a single stable token.
- [ ] Test is deterministic (no flaky timing — synchronize on `indexReadyHook` or drain the goroutine
      before reading output).
- [ ] `go test -race` green.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** T7

---

### T9 — Responsiveness + clean shutdown during in-flight build (Story 2 AC3, AC5)

**Behavior:** Two assertions using a **slow analyzer stub** and a **live goroutine harness** (mirror
`TestShutdownCancelsBgContext`'s `go Run(...)` + framed input, with a bidirectional pipe or a
`blockingReaderAfter`):
1. **Responsiveness (AC3):** with the build deliberately slow (analyzer blocks), send `initialize` →
   `initialized` → `didOpen` (an open buffer) → `documentSymbol` on that buffer, and assert the
   `documentSymbol` **response comes back while the build is still blocked** (the store answers without
   the index). Also assert an index-backed request that arrives pre-publish degrades to null/empty (e.g.
   `workspace/symbol` → `[]`, `definition` → `null`), not an error, not a hang.
2. **Clean shutdown (AC5):** send `shutdown` while the build is blocked; assert `bgCancel` fires
   (`bgCtx.Done()`), the build goroutine exits without publishing, `Run` returns cleanly, and
   `go test -race` reports no data race / no leaked goroutine.

**Fixtures:** slow `stubAnalyzer` variant (channel-gated `Analyze`); a one-file open buffer.

**Expected result:**
- `documentSymbol` on the open buffer returns the outline while the slow build blocks (store-first path).
- Pre-publish `workspace/symbol` → `[]`, `definition` → `null` (graceful degradation, FR-43).
- Shutdown mid-build: clean `Run` return, no publish-after-cancel, `-race` clean.

**Reuses/migrates:** the `TestShutdownCancelsBgContext` harness pattern, `bgCtxHook`, `document_symbols.go`
store-first path, the slow stub from T4.

**DoD:**
- [ ] Responsiveness assertion passes deterministically (synchronize the slow build's start/block via a
      channel, not a sleep race).
- [ ] Pre-publish index-backed requests degrade (null/`[]`), never error/hang.
- [ ] Shutdown-mid-build test: clean return, no goroutine leak, `go test -race ./internal/server/` green.
- [ ] `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor
**Depends on:** T4 (and T7 for the progress-during-build coexistence)

---

### T10 — Story 3: retire stubs / correct stale comments

**Behavior:** With the real helpers landed:
- `internal/server/progress.go` now holds the real `progressReporter` (from T2) — the TODO stub is gone
  (satisfied by T2's write; this task confirms no stub text remains and the package doc is accurate).
- `internal/server/handlers.go` — replace the stale `// TODO: definition, references, ... FR-24..29`
  package-doc stub with an accurate one-line package doc (or delete the file if it holds nothing else —
  confirm no symbols live there).
- `server.go` `test/panic` (~751) and `test/panic-notification` (~488) — **keep the cases** (they back
  `TestRequestPanicRecovery`/`TestNotificationPanicRecovery`) but delete the stale "will be removed once
  features 09–13 land" sentences.

**Fixtures:** none (comment/scaffolding hygiene).

**Expected result:** no stub/TODO scaffolding referencing unshipped features; panic-recovery tests still
green; build clean.

**Reuses/migrates:** —

**DoD:**
- [ ] `grep` for "will be removed once features" and the `handlers.go`/`progress.go` TODO text returns
      nothing.
- [ ] `TestRequestPanicRecovery`/`TestNotificationPanicRecovery` still green (cases retained).
- [ ] `just verify` green.

**Agents:** tdd-refactor (no behavior change — no red phase; guarded by existing tests)
**Depends on:** T2, T7

---

## Reviews required (for `/review-feature`)

- **review-concurrency** — the core of this feature: a background build goroutine, F7 build-then-publish
  under `idxResMu`, `bgCtx` cancellation on shutdown, no publish-after-shutdown, `stream.Write` from the
  goroutine, `-race`. Highest-priority dimension.
- **review-protocol** — `window/workDoneProgress/create` request + `$/progress` notification shape,
  `ProgressToken` union, capability gating on `window.workDoneProgress`, no server capability added,
  json/v2 marshaling (no stdlib `json.Marshal` on protocol types).
- **review-robustness** — FR-43: write/marshal failures logged not fatal; `total == 0` divide-by-zero;
  disabled-reporter no-op; degradation of index-backed requests before publish.
- **review-docs** — `CLAUDE.md` "Project state" note + README: async indexing + work-done progress now
  shipped; FR-32/NFR-5 closed; `progress.go` is real. No new server capability to document, but the
  behavior change (background build, progress messages) should be noted.

Not required: **review-seam** (no `internal/model`/`Analyzer`/index-API contract change),
**review-performance** (not the indexing hot path — the build algorithm is unchanged; only *where* it
runs changes).

---

## Decisions (user-approved 2026-07-17)

- **OQ-A → (i) fire-and-forget create, then begin.** No response-await; the create response stays
  logged-only in the existing Response branch. Implemented in T5 as written.
- **OQ-B → REPLAY dirty buffers after publish.** After the background build publishes, re-apply any
  open-buffer changes that arrived during the build into the index (not store-first-only). See new
  **T13**.
- **OQ-C → confirmed:** gate on `Capabilities.Window.WorkDoneProgress`; no progress when absent, still
  build async. (T1/T6.)
- **OQ-D → confirmed end-first:** progress `end` fires before feature-20's no-usable-root
  `window/showMessage`. (T4 sequencing.)
- **OQ-E → WIRE the on-disk cache into the server now.** `Run` must build via
  `BuildWithCache(cfg.Cache.Path under root, …)` for real warm starts (not always cold `Build`), with
  content-hash invalidation. See new **T12**. AC4 is then met by a genuine warm path.
- **OQ-F → MAKE `workspace.Build`/`BuildWithCache` context-cancellable.** Add a `context.Context` so
  shutdown aborts an in-flight build mid-scan. This is a **workspace-package API change** — see new
  **T11** — and it means **`review-seam` IS now required** (the `Build` signature is part of the
  workspace API the server depends on; confirm the Analyzer-interface seam itself is untouched — only
  the workspace build entry point changes).

### New tasks from the approved expansions (integrate into the order)

- **T11 — Make `workspace.Build`/`BuildWithCache` accept `context.Context` and abort mid-scan.**
  Ordered **before T4** (T4's goroutine passes `bgCtx` so shutdown aborts the build early, not just
  skips publish — resolves OQ-F's run-to-completion limitation). Workspace-package change: thread
  `ctx` into the per-file scan loop (`index.go` ~478), check `ctx.Err()` between files, return
  `ctx.Err()` (or a partial+err) on cancel. Update `Build`/`BuildWithCache` callers (server + any
  test). DoD: a workspace unit test cancels the ctx mid-build and asserts early return without
  finishing all files; `go test -race ./internal/workspace/` green; existing `TestBuild_ProgressCallback`
  and cache tests adapted to the new signature. Agents: tdd-red → tdd-green → tdd-refactor.
  **review-seam applies.**

- **T12 — Wire the on-disk cache (`BuildWithCache`) into the server build path (OQ-E, Story 1 AC4).**
  Ordered after T3 (uses `buildIndex`) / with T7. `buildIndex` must call
  `workspace.BuildWithCache(ctx, hctx.root, hctx.cfg, az, logger, cachePath, currentHashes, onProgress)`
  where `cachePath` derives from `hctx.cfg.Cache.Path` under `hctx.root` (the config already carries
  it), so warm starts load the persisted index and only re-analyze changed files (content-hash
  invalidation, FR-38/NFR-2). Cold first run writes the cache. DoD: a test builds once (cold, cache
  written), then rebuilds (warm) over an unchanged workspace and asserts the warm build reads the cache
  (few/zero files re-analyzed → progress `begin`→`end` with a short/empty report burst, honest AC4);
  a changed-file case re-analyzes only that file. `go test -race` green; cache format still `0.6.0`
  (no format change — just wiring the existing `BuildWithCache`). Agents: tdd-red → tdd-green →
  tdd-refactor.

- **T13 — Replay dirty open buffers into the index after the background publish (OQ-B, Story 2 AC4).**
  Ordered after T4 (the publish path). After `publishIndex(idx,res)` on the background goroutine,
  re-apply any open-document store buffers whose content differs from disk (or simply all open buffers)
  into the freshly-published index via the existing `applyDocumentChange`/`idx.Add`+`ResolveInto` path,
  under `idxResMu`, so an edit that arrived during the cold build is reflected in index-backed providers
  (not just store-first ones). DoD: a test opens a buffer and sends a `didChange` **while a slow build
  is in flight**, then after the build publishes asserts an **index-backed** request (e.g.
  `workspace/symbol` or `definition`) reflects the edited content — proving the replay closed the window
  OQ-B.1 identified. `go test -race` green (the replay takes `idxResMu` correctly, no torn state).
  Agents: tdd-red → tdd-green → tdd-refactor.
  **DONE (2026-07-17):** `document.Store.OpenDocuments()` added (returns value-copy snapshots under RLock —
  returning live pointers raced `Store.Update`'s in-place field reassignment, `-race`-caught and fixed).
  `handlerContext.replayOpenBuffers()` merges each open buffer's already-computed `FileAnalysis` into the
  published index (`idx.Add` + one `ResolveInto` under a single `idxResMu` lock, derive off-lock — mirrors
  `applyDocumentChange`). Wired in the background goroutine after `publishIndex`, before
  `reportNoUsableRoot`/`indexReadyHook`. Regression: `TestReplayDirtyBufferAfterPublish`
  (`internal/server/replay_test.go`) — proven RED (`workspace/symbol` → `[]`, disk wins) then GREEN.
  `go test -race -count=2 ./internal/server/ ./internal/document/ ./cmd/...` green; integration `-race`
  green; `go vet ./...` clean; `gofmt -l` empty. See ADR-022. No model/Analyzer/cache change.

**Revised order:** T1, T2 (progress helpers) · T3 (extract build) · **T11 (ctx-cancellable Build)** ·
T4 (background goroutine, passes bgCtx) · **T13 (replay dirty buffers)** · T5, T6 (create sequencing +
gating) · **T12 (cache wiring)** · T7 (callback→report) · T8 (wire sequence test) · T9 (responsiveness +
shutdown) · T10 (cleanup). **review-seam added** to the required reviews for T11.

---

## Open questions (RESOLVED above — original text retained for context)

**OQ-A (crux) — How to sequence the server→client `create` request vs. the first `$/progress`.**
The serial dispatch loop cannot block awaiting a response (the Response branch, `server.go:625-635`, only
logs — there is no correlation/wait mechanism). Options:
- **(i) Recommended — fire-and-forget create, then begin** (no await). Send `create`, immediately send
  `begin`/`report`/`end` on the background goroutine. Simplest; matches how the codebase already treats
  server-initiated requests (registerCapability's response is only logged). Risk: a spec-strict client
  could theoretically drop progress sent before it acked `create` — but a client that advertised
  `window.workDoneProgress` is expected to accept server-initiated progress, and the downside (progress
  silently ignored) is cosmetic and non-fatal (FR-43).
- **(ii) Await the create response** via a new correlation mechanism (a pending-request map keyed by the
  request ID, signaled from the Response branch, awaited by the background goroutine — the loop keeps
  reading, only the goroutine waits). Spec-cleanest, but adds a correlation primitive to the dispatch
  loop (larger change; would warrant its own task + review-concurrency scrutiny).
- **Also verify during green:** whether the client-advertised `window.workDoneProgress` truly licenses
  sending progress without a completed `create` handshake (LSP 3.15 wording). If it does not, (ii) becomes
  mandatory. **Decision needed: (i) or (ii)?** (Plan currently assumes (i).)

**OQ-B — Start the build at `initialized` (recommended) or elsewhere?** The plan and this decomposition
start the background build in the `initialized` handler (after bootstrap, which already ran at
`initialize`). `didOpen`/`didChange` arriving during the build are served by the store immediately
(store built at `initialize`); post-publish the index reflects them because... **needs confirmation
(OQ-B.1):** if a `didChange` calls `applyDocumentChange` *before* the background build publishes,
`applyDocumentChange` sees `hctx.idx == nil` and logs "called before index initialized" (`server.go:1211`)
— the change is applied to the store but **not** to the index, and the subsequent full-build publish may
**overwrite** it with disk content (losing an unsaved edit in the index; the store still has it, so
store-first providers are correct, but index-backed providers would miss it until the next change).
**Decision needed:** is "store serves live buffers, index catches up on next change" acceptable for MVP
(document as the chosen reconciliation per Story 2 AC4), or should the background publish **re-apply any
changes that arrived during the build** (replay the store's dirty buffers into the index after publish)?
Recommended: MVP = store-first is sufficient; document the small window. Flag if reviewers disagree.

**OQ-C — Capability gating confirmation.** Confirmed by code inspection: gate on
`Capabilities.Window.WorkDoneProgress`. When absent/false, send no progress but still build async
(Story 1 AC2). No decision needed unless you want progress *suppressed entirely* for tiny workspaces even
on supporting clients (see OQ-E).

**OQ-D — Sequencing progress-end vs. feature-20 `reportNoUsableRoot`.** Both fire after the build. This
plan moves `reportNoUsableRoot` inside the background goroutine, **after** `publishIndex` and **after**
progress `end`, so: (1) progress `end` always fires (even for an empty root), then (2) the no-usable-root
`window/showMessage` warning fires if the index is empty. **Decision needed:** confirm this ordering
(progress-end before the no-usable-root warning) is desired, vs. warning-first. Recommended: end-first
(progress UI closes, then the actionable warning surfaces).

**OQ-E — Warm-cache honesty (Story 1 AC4).** The server does **not** wire `BuildWithCache(cachePath)`
today (it always cold-builds via `Build`), so there is no true warm path to exercise through `Run`.
Even if a cache were wired, `onProgress` fires per *scanned* file (including cache hits), so a warm build
reports `N/N files` quickly. Options: (i) accept per-scanned-file reporting — begin/end bracket a short
burst, satisfying "no misleading long report phase" because it *is* fast; (ii) suppress `report`s (send
only begin→end) below a threshold (e.g. `total < 10` or elapsed `< 50ms`). **Decision needed:** is
AC4 satisfied by (i) "fast because cached" alone (recommended, minimal), or do you want the threshold
suppression (ii)? And separately: **is wiring the on-disk cache into the server in-scope for this
feature, or out-of-scope** (it was never wired — see Current-state findings)? Recommended: out-of-scope;
AC4 met by (i). Confirm.

**OQ-F — `workspace.Build` is not cancellable mid-flight.** On shutdown, `bgCtx` is cancelled but an
in-flight `workspace.Build` runs to completion (it takes no ctx); only the publish/report is skipped.
For MVP workspaces this is a bounded delay. **Decision needed:** acceptable for MVP (recommended), or do
you want `Build` to accept a `context.Context` and abort early (a workspace-package change — out of the
server-only scope this feature otherwise stays within, and would need a workspace test + review-seam)?
Recommended: MVP accepts the run-to-completion; note it.
