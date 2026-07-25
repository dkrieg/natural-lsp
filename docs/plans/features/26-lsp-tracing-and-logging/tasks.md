# Tasks: LSP Tracing & Structured Server Logging (feature 26)

**Spec:** `docs/plans/features/26-lsp-tracing-and-logging/plan.md`
**PRD requirements:** FR-53 (LSP-native observability — new), NFR-14 (observability/legibility), FR-43 (graceful degradation)
**Depends on:** 03 (lifecycle/dispatch), 19 (json/v2 marshaling), 20 (`window/showMessage` sender), 21 (background build + `$/progress` + outbound-notification pattern)

**Hard constraints (assert in every task):**
- **No `internal/model` change.** No new `model.*` types/fields.
- **No cache-format bump.** Stays `0.6.0`. (Nothing here is persisted.)
- **NO new server capability.** `window/logMessage`, `$/logTrace`, `$/setTrace` are notification-level
  (gated on the *client* window capability, like `publishDiagnostics`/`window/showMessage`). The locked
  `TestInitialize` allow-list (`internal/server/server_test.go`, `requiredProviders` at ~line 598 + the
  completion/signatureHelp shape asserts) is **UNCHANGED** — a task explicitly re-asserts this.
- **json/v2 marshaling only** (feature 19). Outbound params marshal via `(*T).MarshalJSONTo(jsontext.NewEncoder(&buf))`
  (the `progress.go`/`diagnostics.go`/`root.go` pattern), never stdlib `json.Marshal`. The
  `marshal_guard_test.go` `TestNoStdlibJSONMarshalForResults` guard must keep passing (no `encoding/json`
  import in any new production file).
- **Fire-and-forget (FR-43).** A failed/marshal-erroring `window/logMessage`/`$/logTrace` write must never
  block, fail, or panic a request — log the write error to stderr only (mirror `progressReporter`'s
  `pr.logger.Warn("failed to send …", "err", err)` contract and `sendShowMessage`'s non-fatal path).
- **LSP-facing side of the Analyzer seam only.** No `internal/analysis/*` change; `seam_test.go` untouched.

---

## Current-state findings & impact

Surveyed `internal/server/server.go` (full dispatch + `handleInitialize` + `buildIndex`/`publishIndex`/
`reportNoUsableRoot`/`applyDocumentChange`), `internal/server/progress.go`, `internal/server/diagnostics.go`,
`internal/server/root.go`, `cmd/natural-lsp/main.go`, `internal/config/config.go`, and the vendored
`go.lsp.dev/protocol@v1.0.0` + `go.lsp.dev/jsonrpc2@v1.0.0`.

1. **No trace/log handling exists today (confirmed).** `grep` for `setTrace`/`logTrace`/`logMessage`/
   `params.Trace` in `internal/server` is empty. `handleInitialize` (server.go:156) decodes
   `protocol.InitializeParams` but never reads `params.Trace`. The dispatch notification switch
   (server.go:456) handles `initialized`/`didOpen`/`didChange`/`didClose`/`workspace/didChangeWatchedFiles`
   + test hooks; `$/setTrace` is absent → **new notification case** (sibling to `initialized`). This is all
   **NEW** work, no migration of existing consumers.

2. **The protocol types all exist — NO new dependency, NO new types.** Verified in `go.lsp.dev/protocol@v1.0.0`:
   - `InitializeParams.Trace TraceValue` (`json:"trace,omitzero"`, lifecycle.gen.go:89). `TraceValue` ∈
     `TraceValueOff="off"` / `TraceValueMessages="messages"` / `TraceValueVerbose="verbose"`
     (basic_structures.gen.go:137-146). Omitted ⇒ empty string.
   - `SetTraceParams{Value TraceValue}` (lifecycle.gen.go:660) — decodes via `UnmarshalJSONFrom`.
   - `LogTraceParams{Message string; Verbose *string}` (lifecycle.gen.go:666) — `Verbose` is a `*string`
     (omitted when nil via `omitzero`), so "no verbose" = leave nil.
   - `LogMessageParams{Type MessageType; Message string}` (window_features.gen.go:75); `MessageType` ∈
     `Error=1`/`Warning=2`/`Info=3`/`Log=4`.
   - All implement `MarshalJSONTo`/`UnmarshalJSONFrom` (json/v2 path — feature 19 compliant).

3. **Outbound-notification pattern to reuse (confirmed).** `jsonrpc2.NewNotification(method, jsonrpc2.RawMessage(buf.Bytes()))`
   → `stream.Write(ctx, notif)`, params pre-marshaled through `jsontext.NewEncoder`. Exactly the shape of
   `progressReporter.begin/report/end` and `sendShowMessage` (root.go/server.go:1333). `reportNoUsableRoot`
   is the model for a one-shot notifier that owns `logger` and treats write errors as non-fatal.

4. **CONCURRENCY — OQ-2 resolved by code inspection (load-bearing).** Frame writes are ALREADY serialized:
   `headerStream.Write` takes its own `writeMu` for the whole frame (jsonrpc2@v1.0.0 `framer.go:171-177`),
   and server.go's own comment at lines 579-581 relies on this ("headerStream serializes writes under its
   own writeMu, so this never races the dispatch loop's response writes"). **Therefore a separate outbound
   write mutex is NOT needed for frame integrity** — two goroutines calling `stream.Write` cannot interleave
   bytes. The genuine shared-mutable-state concern is **the trace level**: it is written by `$/setTrace` on
   the serial dispatch loop and read by `$/logTrace` emitters that (for background-goroutine RPCs, e.g. the
   feature-21 async build path) run on another goroutine. That single value needs atomic/mutex-guarded
   access and a `-race` test. This is the plan's "main concurrency design point" restated correctly against
   the code, and gets a `review-concurrency` pass (T3/T5).

5. **`window/logMessage` emission points already log to stderr (dual-sink candidates).** Existing `slog`
   sites that Story 1 mirrors: index-build begin/end (`buildIndex` `logger.Info("workspace index built", …)`
   server.go:1378; progress `begin`/`end` bracket it), cache warm-vs-rebuild (folded into that Info line's
   `warm` field), per-file skips/degradation and analyzer-panic recovery (in `analyzeOne`/`processFile` —
   `internal/server/*` degradation path), resolution ambiguity (surfaced via `res.DiagnosticsFor`, already
   Warn-logged), and per-request panic/InternalError (server.go:817 `logger.Error("panic in request dispatch", …)`).
   The plan's suggested "thin dual-sink wrapper" routes these through one helper so stderr + LSP log stay in
   lockstep — recommended, scoped in T2.

6. **`--log-level` (Story 3 / OQ-3) is NON-trivial.** `cmd/natural-lsp/main.go` builds the logger as
   `slog.Default()` (well, `main()` passes `slog.Default()` into `run`); there is no level knob. Adding
   `--log-level` means constructing a `slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})` in
   `runWithIO`, parsing/validating the flag (CR-6 fail-safe), and documenting it (README + `--init` sample).
   `config.Config` has **no `[logging]` block** today (`Workspace`/`Cache`/`Analysis`/`Resolution` only), so
   a config-driven level would be an additive `LoggingConfig` + `Validate` + `Sample()` change. **Recommend
   shipping Stories 1–2 as core and structuring Story 3 as the LAST, optional task (T6) that can be dropped
   at review** without affecting T1–T5. Per OQ-3, keep the `--log-level` *flag* (not the config block) as
   the minimal-if-included form.

**No acceptance criterion is already satisfied** — the entire feature is greenfield server-layer wiring.
No shared-contract change, so **no migration tasks**. Traceability matrix at the end.

---

## Task ordering rationale

T1 builds the `messageLogger` seam (the notifier owning stream + atomic trace level) — everything else
depends on it. T2 wires operational `window/logMessage` (Story 1). T3 reads the initial `trace` and handles
`$/setTrace` (Story 2, level plumbing). T4 emits `$/logTrace` gated on the level (Story 2, the firehose).
T5 is the concurrency `-race` + fuzz hardening pass. T6 is the optional Story 3 flag. Each is a thin
red→green→refactor slice.

---

## T1 — `messageLogger`: the notifier seam + atomic trace level (foundation)

**Story:** foundation for Stories 1 & 2. **Kind:** NEW.

Introduce `internal/server/messagelog.go` with a `messageLogger` type — a sibling of `progressReporter` —
that owns the `jsonrpc2.Stream` + `*slog.Logger` and holds the **atomically-accessed trace level**. This is
the single seam through which all `window/logMessage` and `$/logTrace` frames are emitted, and the single
owner of the mutable trace level.

- `type messageLogger struct { stream jsonrpc2.Stream; logger *slog.Logger; traceLevel atomic.Int32 }`
  (encode `TraceValue` as an int32 enum: off=0/messages=1/verbose=2 via a small `traceValueToLevel` /
  `levelToTraceValue` pure pair, so the level is a lock-free `atomic.Int32`). Do NOT store the `TraceValue`
  string in an atomic pointer unless simpler — the int enum keeps `setTrace`/reads trivially race-free.
- `newMessageLogger(stream, logger, initial TraceValue) *messageLogger` — seeds the level.
- `func (m *messageLogger) setTrace(v TraceValue)` — stores; an **unknown value maps to off** (CR-6
  fail-safe — see T3) but the mapping itself lives in the pure `traceValueToLevel` (unknown ⇒ off).
- `func (m *messageLogger) trace() TraceValue` — atomic read → `levelToTraceValue`.
- `func (m *messageLogger) logMessage(ctx, typ protocol.MessageType, msg string)` — build
  `protocol.LogMessageParams`, marshal via `MarshalJSONTo`, `jsonrpc2.NewNotification("window/logMessage", …)`,
  `stream.Write`; on marshal OR write error, `m.logger.Warn("failed to send window/logMessage", "err", err)`
  and **return without panicking** (FR-43). Never returns an error to the caller (fire-and-forget).
- `func (m *messageLogger) logTrace(ctx, message string, verbose *string)` — same shape for
  `protocol.LogTraceParams` / method `"$/logTrace"`. `verbose == nil` ⇒ field omitted.
- A `nil`-safe guard: if `m == nil` or `m.stream == nil`, both emitters are no-ops (so tests / not-yet-wired
  paths never panic).

**RED:** unit tests, no server round-trip yet.
- `traceValueToLevel`/`levelToTraceValue` round-trip for off/messages/verbose; unknown/garbage string ⇒ off.
- `logMessage` writes ONE `window/logMessage` frame whose params, decoded back, have `type == 3` (Info) and
  the exact `message` — **wire-bytes assertion** via a fake `jsonrpc2.Stream` capturing the written
  `jsonrpc2.Message` (reuse the progress/showMessage test harness in `server_test.go` if one exists; else a
  minimal capturing stream). Assert method is `window/logMessage`.
- `logTrace` with `verbose == nil` ⇒ decoded params have non-empty `message` and **no `verbose` key** in the
  raw JSON; with `verbose != nil` ⇒ `verbose` present with the given string.
- A `logMessage`/`logTrace` whose `stream.Write` returns an error does NOT panic and does NOT return an
  error (capturing stream configured to fail); the stderr logger records a Warn.

**GREEN:** implement `messagelog.go` as above.

**REFACTOR:** extract the shared "marshal-params-then-write-notification" step if it duplicates
`progressReporter`'s pattern enough to warrant a private helper; keep both readable.

**DoD:**
- [ ] `messageLogger` compiles; emitters are nil-safe and fire-and-forget (never return error, never panic).
- [ ] Trace level is an `atomic.Int32` with pure enum mappers; unknown ⇒ off.
- [ ] Wire-bytes tests pass for `window/logMessage` (`type`=int, `message`=string) and `$/logTrace`
      (verbose present/absent), marshaled via `MarshalJSONTo`.
- [ ] Write-failure path logged-to-stderr-only, proven by a failing-stream test.
- [ ] `go test -race ./internal/server` green; `TestNoStdlibJSONMarshalForResults` still passes (no
      `encoding/json` import in `messagelog.go`).
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

## T2 — Operational `window/logMessage` for lifecycle/skip/error events (Story 1)

**Story:** Story 1 (NFR-14, FR-53). **Kind:** NEW.
**Acceptance criteria covered:** S1-AC1, S1-AC2 (dual sink), S1-AC3 (fire-and-forget), S1-AC4 (no capability),
S1-AC5 (wire-bytes).

Wire `m.logMessage(...)` at the existing stderr `slog` sites so the same story appears in the editor's LSP
log, **always emitted, severity-filtered, independent of trace level** (OQ-1 approved). Construct the
`messageLogger` once in `Run` (right after `stream` is created, alongside the existing progress wiring) and
store it on `handlerContext` (`hctx.mlog *messageLogger`); pass it where events fire.

Emission points + `MessageType` (mirror, don't replace, the stderr line):
- **Index build begin** — `Info` — root + file count (emit from the background build goroutine right before/
  after `reporter.begin`, or inside `buildIndex`).
- **Index build end** — `Info` — indexed/reanalyzed/total counts + warm-vs-rebuild (mirror the existing
  `logger.Info("workspace index built", "total", …, "warm", …)` at server.go:1378).
- **Cache outcome** — `Info` — warm hit vs. version-mismatch/corrupt rebuild (the `warm` boolean already
  distinguishes; fold into the build-end message or a dedicated line).
- **Per-file skip / degradation** — `Warning` — too-large / excluded / unreadable / analyzer-panic-recovered
  (the degradation path in `analyzeOne`/`processFile`). If threading `mlog` into that path is awkward,
  aggregate a single "N files skipped (…)" `Warning` at build-end rather than one-per-file (avoids console
  flood — consistent with OQ-4's spirit); **decide in the RED test which shape** and pin it.
- **Resolution ambiguity** — `Warning` — mirror the ambiguity Warn (drawn from `res.DiagnosticsFor`; a
  build-end summary line is acceptable to avoid per-site spam).
- **Request-dispatch panic / internal error** — `Error` — at the panic-recovery site (server.go:817) emit an
  `Error` `window/logMessage` alongside the existing `logger.Error("panic in request dispatch", …)` and the
  `InternalError` response. Emitted from the dispatch loop goroutine.

**Design note (dual-sink, plan §Notes):** prefer a thin wrapper so a call site logs to stderr AND the LSP
log in one call (e.g. `hctx.logEvent(typ, msg, args...)` that does `logger.Log(...)` + `mlog.logMessage(...)`),
keeping the two sinks in lockstep and avoiding duplicated call sites. Keep the wrapper pure of I/O beyond the
two writes; it must be fire-and-forget on the `logMessage` half.

**RED:**
- Fixture: reuse `internal/server/testdata/roothandshake/` (a populated multi-file root) for the
  build-begin/end path; reuse an existing malformed fixture (or `testdata/navigation/`) for a skip case; and
  the existing `test/panic` request case for the Error path.
- Drive an `initialize → initialized → (await indexReadyHook) → shutdown → exit` round-trip through `Run`
  with a capturing stream; assert a `window/logMessage` with `type==3` (Info) and a message naming the file
  count appears for build-end.
- **Wire-bytes** assertion (S1-AC5): decode the captured `window/logMessage` frame → `type` is the integer 3,
  `message` is the expected non-empty string, method is `window/logMessage`.
- Fire a `test/panic` request; assert a `window/logMessage` with `type==1` (Error) is emitted AND the
  JSON-RPC `InternalError` response still returns AND the loop survives (S1-AC3 fire-and-forget: request not
  blocked/failed by the log emission).
- **Dual-sink (S1-AC2):** assert the stderr `slog` buffer still contains the same event (the log line is
  retained, not replaced).
- **No-capability (S1-AC4):** re-run `TestInitialize`'s allow-list assertion path — the advertised
  capabilities map is byte-identical to before this feature (add a sub-assertion, or a new
  `TestInitialize_NoLogTraceCapability`, that neither `window/logMessage` nor any trace key appears in
  `ServerCapabilities`).
- **Modeled-gap coverage (FR-17):** assert that dynamic/unresolved *references* do NOT produce a
  `window/logMessage` (they are not operational events — only parser/ambiguity/skip/error channels are
  mirrored); a fixture with a `CALLNAT #VAR` dynamic edge emits no log for that edge.

**GREEN:** construct `mlog` in `Run`, store on `hctx`, add `logEvent` wrapper, wire the six emission points.

**REFACTOR:** dedup the `logEvent` call sites; ensure the build-goroutine emissions happen before the
`indexReadyHook` so tests observe them deterministically.

**DoD:**
- [ ] All six event classes emit a `window/logMessage` with the correct `MessageType`, in addition to (not
      instead of) the stderr line (dual sink).
- [ ] Emission is fire-and-forget: a failing capturing stream does not block/fail/panic any request (proven).
- [ ] `TestInitialize` allow-list unchanged; explicit assertion that no capability/trace key was added.
- [ ] Wire-bytes test for a representative `window/logMessage`.
- [ ] Modeled gaps (dynamic/unresolved refs) produce no operational log (FR-17).
- [ ] `go test -race ./...` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

## T3 — Honor `initialize` `trace` + handle `$/setTrace` (Story 2, level plumbing)

**Story:** Story 2 (FR-53). **Kind:** NEW. **Acceptance criteria covered:** S2-AC1, S2-AC2.

- In `handleInitialize` (server.go:156), read `params.Trace` and return it in `initializeNegotiation`
  (add an `initialTrace protocol.TraceValue` field). Absent/empty ⇒ `TraceValueOff` (via `traceValueToLevel`
  from T1). Seed `mlog` with this initial level in `Run`'s `initialize` case (the `mlog` may be constructed
  earlier with `off` and re-seeded here via `mlog.setTrace(negotiation.initialTrace)`).
- Add a **`$/setTrace`** notification case to the dispatch switch (sibling to `initialized`): decode
  `protocol.SetTraceParams` via `UnmarshalJSONFrom`, call `mlog.setTrace(params.Value)`. An **unknown value
  ⇒ off + a stderr Warn** (CR-6 fail-safe; the off-mapping is in `traceValueToLevel`, and the case logs a
  `logger.Warn("unknown $/setTrace value; treating as off", "value", params.Value)` when the decoded value
  is not one of the three known constants). Malformed params ⇒ `logger.Error(... invalid $/setTrace params …)`
  and ignore (notifications get no response; never crash). Wrap is already inside the notification
  panic-recovery func.

**RED:**
- `handleInitialize` unit test: params with `trace: "verbose"` ⇒ `negotiation.initialTrace == TraceValueVerbose`;
  omitted `trace` ⇒ `TraceValueOff`.
- Round-trip through `Run`: send `initialize` with `trace: "messages"`, then observe `mlog.trace() ==
  messages` (via a test hook or by asserting `$/logTrace` behavior in T4 — here assert the level via a
  minimal exported-for-test accessor or an `initializeReadyHook`-style hook).
- Send a `$/setTrace {"value":"verbose"}` notification after `initialized`; assert `mlog.trace()` flipped to
  verbose (runtime change, no restart — S2-AC2).
- Send `$/setTrace {"value":"bogus"}`; assert level becomes off AND a stderr Warn was logged; the loop
  survives.
- Send `$/setTrace` with malformed JSON params; assert no panic, loop survives, an Error logged.

**GREEN:** add the `initialTrace` field, wire it into `handleInitialize`/`Run`, add the `$/setTrace` case.

**REFACTOR:** keep the unknown-value detection (for the Warn) DRY with `traceValueToLevel` (e.g. a
`isKnownTraceValue` predicate).

**DoD:**
- [ ] `initialize`'s `trace` becomes the initial level; absent ⇒ off (S2-AC1).
- [ ] `$/setTrace` changes the level at runtime; unknown ⇒ off + stderr Warn; malformed ⇒ ignored, no crash
      (S2-AC2, CR-6).
- [ ] `TestInitialize` allow-list still unchanged (no capability added for setTrace).
- [ ] `go test -race ./internal/server` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

## T4 — Emit `$/logTrace` per-RPC, gated on the trace level (Story 2, the firehose)

**Story:** Story 2 (FR-53). **Kind:** NEW.
**Acceptance criteria covered:** S2-AC3, S2-AC4 (off ⇒ silent), S2-AC5 (fire-and-forget), S2-AC6 (wire test).

At the request-dispatch boundary in `Run` (around the `switch method` block), bracket each **Call** with
trace emission gated on `mlog.trace()`:
- Level **off** ⇒ emit nothing (the default; zero console noise — S2-AC4).
- Level **messages** ⇒ emit ONE `$/logTrace` per request with `message` = method name + direction +
  duration (e.g. `"[Trace] Received request 'textDocument/hover - (12)' in 1.3ms"` — gopls/LSP-inspector
  style; capture start time before dispatch, elapsed after). `verbose == nil`.
- Level **verbose** ⇒ additionally populate `verbose` with a **bounded** params/result summary (OQ-4):
  a pure `traceSummary(raw []byte) string` that truncates to a **fixed byte cap** (propose `2048` bytes,
  pin the exact constant in the RED test as `maxTraceVerboseBytes`) with an **elision marker**
  (e.g. `… (N bytes elided)`). It must NOT re-parse or leak large source bodies verbatim — cap first, then
  attach. Redaction policy: cap only (no field-level redaction in v1); note in the summary function's doc.
- Emission is **fire-and-forget** via `mlog.logTrace` (never blocks/fails/panics the request — S2-AC5).
- Scope: apply to the request Calls in the dispatch switch. Notifications and Responses may optionally be
  traced at `verbose`; keep v1 to **request Calls** unless trivial, and pin the chosen scope in the RED test.
  Do NOT trace the `$/logTrace` emission itself (no recursion).

**Concurrency note:** `mlog.trace()` is an atomic read; emitting from the dispatch loop is on the serial
goroutine, but `$/setTrace` on that same loop is what mutates it — so within one request the level is stable.
The background-build path already emits `window/logMessage`/`$/progress` (T2/feature 21) from another
goroutine; those are NOT `$/logTrace` per-RPC traces, so no cross-goroutine level read races a request
trace. The atomic still guarantees safety if a future path emits a trace off-loop (T5 proves it).

**RED:**
- Reuse `testdata/navigation/` or `testdata/hover/` for a real request to trace.
- Level **off** round-trip: drive an `initialize(trace:off) → initialized → textDocument/hover → shutdown`;
  assert **zero** `$/logTrace` frames on the wire (S2-AC4).
- Level **messages**: `initialize(trace:messages)`; assert a `$/logTrace` frame appears with a non-empty
  `message` (contains the method name) and **no `verbose`** key (S2-AC3 + wire-bytes S2-AC6).
- Level **verbose**: assert `verbose` is populated (non-nil) with a bounded string.
- **OQ-4 bound:** feed `traceSummary` a payload larger than the cap; assert the output length ≤ cap +
  marker and ends with the elision marker; a small payload passes through unchanged.
- **Runtime flip:** `initialize(trace:off)` → hover (no trace) → `$/setTrace{messages}` → hover (trace
  appears). Proves S2-AC2's effect on `$/logTrace` at runtime.
- **Fire-and-forget (S2-AC5):** failing capturing stream during a traced request → the request's own
  response still returns, loop survives, no panic.

**GREEN:** add the `traceSummary` bound + the request-bracket emission in `Run`.

**REFACTOR:** extract the "format request trace message" into a pure helper so it is unit-testable without a
round-trip; keep the dispatch-loop change minimal (start-time capture + one deferred/post-dispatch emit).

**DoD:**
- [ ] off ⇒ no `$/logTrace`; messages ⇒ method+timing, no verbose; verbose ⇒ bounded summary (S2-AC3/AC4).
- [ ] `$/setTrace` flips `$/logTrace` behavior mid-session (S2-AC2 effect).
- [ ] `traceSummary` is byte-capped with an elision marker (OQ-4); pure + unit-tested.
- [ ] Fire-and-forget: traced request unaffected by a log-write failure (S2-AC5).
- [ ] Wire-bytes test for a `$/logTrace` (method, `message`, `verbose` present/absent — S2-AC6).
- [ ] `TestInitialize` allow-list unchanged.
- [ ] `go test -race ./internal/server` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

## T5 — Concurrency `-race` proof + fuzz hardening (FR-43, OQ-2)

**Story:** cross-cutting (OQ-2, FR-43). **Kind:** NEW.

Prove the two-goroutine safety and the never-panic contract, and get the `review-concurrency` pass the plan
calls for.

- **`-race` test (OQ-2):** a test that runs the background build goroutine (which emits `window/logMessage`
  via `mlog` — T2) concurrently with dispatch-loop `$/setTrace` updates and `$/logTrace` emissions, driving
  many iterations, asserting no data race on the trace level and no interleaved/corrupt frames. Rely on the
  fact (documented in Findings §4) that `headerStream.writeMu` serializes frames — the test's job is to
  prove the **trace-level atomic** is race-free under concurrent read (emit) + write (`setTrace`) and that
  `messageLogger` needs no extra write mutex. If `-race` surfaces a frame-integrity issue against the real
  `headerStream` (it should not), THEN add an outbound write mutex to `messageLogger` and re-run — record
  the outcome either way. **Record the OQ-2 decision** ("no extra mutex needed; headerStream serializes; the
  trace level is the only shared state, guarded by atomic.Int32") in the feature's completion note.
- **Fuzz targets (FR-43, per the ask):**
  - `FuzzSetTraceValue` — feed arbitrary strings through `traceValueToLevel`/`messageLogger.setTrace`;
    assert never panics and always yields a valid level (unknown ⇒ off).
  - `FuzzTraceSummary` — feed arbitrary `[]byte` (incl. invalid UTF-8, huge, empty) through `traceSummary`;
    assert never panics and output length ≤ cap + marker.
  - `FuzzLogFormat` — feed arbitrary method/message strings through the trace-message formatter and
    `logMessage`/`logTrace` param-marshal path; assert never panics and always produces valid JSON
    (decodable back to the param type).

**RED:** the `-race` test fails (or panics) without the atomic/guard; the fuzz targets are added (they pass
trivially once the pure functions are total, but must be present and run under the corpus).

**GREEN:** already-correct T1–T4 code satisfies these; add any missing guard the fuzz/-race surfaces.

**REFACTOR:** none expected beyond doc comments recording the concurrency contract.

**DoD:**
- [ ] `-race` concurrency test passes; OQ-2 decision recorded (no extra mutex; atomic trace level).
- [ ] `FuzzSetTraceValue`, `FuzzTraceSummary`, `FuzzLogFormat` present, run clean in the gate's short mode,
      never panic (FR-43).
- [ ] `review-concurrency` PASS.
- [ ] `just verify` green.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Reviews:** `review-concurrency`.

---

## T6 — *(OPTIONAL, LAST — droppable)* `--log-level` startup flag (Story 3, OQ-3)

**Story:** Story 3 (NFR-14). **Kind:** NEW. **Include only if it stays trivial (OQ-3); else DEFER to a
follow-up feature.** This task is intentionally last and independent so it can be cut without touching
T1–T5.

Scope kept **minimal**: a `--log-level` CLI flag only (the `[logging]` config block is **deferred** — it
would need an additive `LoggingConfig` + `Validate` + `Sample()` in `internal/config`, which pushes past
"trivial"; note this explicitly).

- In `cmd/natural-lsp/main.go` / `runWithIO`: parse `--log-level=<error|warn|info|debug>` (and/or its
  space-separated form to match the existing `--stdio` arg-loop style). Map to `slog.Level`; construct
  `slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))` and pass it as the `logger`
  into `server.Run`. Default (flag absent) ⇒ today's behavior (whatever `slog.Default()` yields — keep
  byte-compatible with the current default level).
- **CR-6 fail-safe:** an invalid value ⇒ default level + an actionable stderr `Problem`-style message,
  **never a crash** (mirror config's Validate convention).
- `--version` / existing CLI shape unchanged (S3-AC2). Document in README + `--init` sample only if the
  flag is included (the `--init` sample is config, so a flag-only version may just document in README —
  pin the doc surface in the RED test if practical, else in the finalize doc-sync).
- Per OQ-3 S3-AC3: the client-supplied `initialize` `trace` remains authoritative for the *trace* level;
  `--log-level` controls only the **stderr `slog`** verbosity. State this in the flag's help/doc.

**RED (in `cmd/natural-lsp/main_test.go`):**
- `--log-level=debug` ⇒ logger emits Debug lines (drive a `runWithIO` that logs at Debug and assert the
  buffer contains it); default ⇒ Debug suppressed.
- `--log-level=bogus` ⇒ default level + an actionable message on stderr, exit code unchanged (no crash).
- `--version` output shape unchanged (existing `TestVersionFlag` still passes).

**GREEN:** implement the flag parse + handler construction in `runWithIO`.

**REFACTOR:** keep the level-string→`slog.Level` mapping a pure helper.

**DoD (only if included):**
- [ ] `--log-level` sets stderr verbosity; invalid ⇒ default + actionable message, no crash (S3-AC1).
- [ ] `--version` / CLI shape unchanged (S3-AC2); documented (S3-AC3 note on trace vs. log-level authority).
- [ ] `just verify` green.
- **Decision gate:** if implementing exceeds "trivial", **drop this task and record Story 3 as deferred**
      in the completion note (Stories 1–2 fully satisfy FR-53/NFR-14's editor-visibility win).
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

## Reviews required (for `/review-feature`)

- **`review-concurrency`** — MANDATORY. The trace level is shared across the dispatch loop (`$/setTrace`
  writer, `$/logTrace` reader) and the background build goroutine (`window/logMessage` emitter). Verify the
  `atomic.Int32` trace level is the only shared mutable state, that `headerStream.writeMu` already serializes
  frames (Findings §4), and that no extra outbound mutex is needed (or, if T5 surfaced one, that it is
  correct). Confirm the `-race` test in T5 exercises the real concurrent paths.
- **`review-lsp-spec`** — verify `trace`/`$/setTrace`/`$/logTrace`/`window/logMessage` wire shapes match LSP
  3.17: `LogMessageParams.type` integer semantics (Error=1…Log=4), `$/logTrace` `verbose` omitted when
  absent, no `ServerCapabilities` entry added (correctly notification-gated on the client `window`
  capability).
- **`review-code`** — fire-and-forget discipline at every emit site (no error propagation into request
  handling); `marshal_guard` (no stdlib `json.Marshal`); the dual-sink wrapper not duplicating call sites;
  nil-safety of `messageLogger`.
- **`review-docs`** — CLAUDE.md "Project state" note + README updated to describe the LSP logging/tracing
  sinks; record **no model/cache/version change** (mirror features 19/20/21) and the OQ-2 concurrency
  decision. If T6 shipped, document `--log-level` (README + `--init` sample as applicable).

---

## Traceability matrix

| Acceptance criterion | Task(s) | Notes |
|---|---|---|
| S1-AC1 `window/logMessage` for 6 event classes w/ correct `MessageType` | T2 | Info/Warning/Error mapping |
| S1-AC2 dual sink (stderr retained) | T2 | `logEvent` wrapper |
| S1-AC3 fire-and-forget, never blocks/fails/panics | T1, T2 | failing-stream test |
| S1-AC4 no new capability; `TestInitialize` unchanged | T2 (assert), all | explicit allow-list re-assert |
| S1-AC5 wire-bytes `window/logMessage` | T1, T2 | `type` int / `message` str via `MarshalJSONTo` |
| S2-AC1 honor `initialize` `trace`, absent ⇒ off | T3 | `handleInitialize` reads `params.Trace` |
| S2-AC2 `$/setTrace` runtime level change; unknown ⇒ off + Warn | T3 | CR-6 fail-safe |
| S2-AC3 messages=method+timing; verbose=+bounded summary | T4 | `traceSummary` OQ-4 cap |
| S2-AC4 off ⇒ no `$/logTrace` | T4 | zero-frame assertion |
| S2-AC5 `$/logTrace` fire-and-forget | T4 | failing-stream test |
| S2-AC6 wire test: off/messages/verbose + runtime flip | T4 | wire-bytes |
| S3-AC1 `--log-level` flag, invalid ⇒ default + Problem | T6 (optional) | flag only; config block deferred |
| S3-AC2 `--version`/CLI shape unchanged | T6 (optional) | `TestVersionFlag` |
| S3-AC3 trace vs. log-level authority | T6 (optional) | client `trace` authoritative |
| FR-43 never-panic (setTrace value, trace/log formatting) | T5 | 3 fuzz targets |
| OQ-2 outbound-write serialization | T5 | `-race` proof + recorded decision |

---

## Open questions (residual)

- **OQ-1 (logMessage gating) — RESOLVED as planned:** `window/logMessage` is always emitted, severity-
  filtered, independent of trace level (T2). Optional future refinement: gate the noisiest `Info`/`Log`
  events behind `messages`/`verbose` — deferred, not needed for the NFR-14 win.
- **OQ-2 (outbound-write serialization) — RESOLVED by code inspection:** `headerStream.writeMu` already
  serializes whole frames (jsonrpc2 `framer.go:171-177`); server.go already relies on this (lines 579-581).
  **No extra outbound write mutex is planned.** The only shared mutable state is the trace level, guarded by
  `atomic.Int32` (T1) and proven race-free (T5). *Residual:* if T5's `-race` run against the real
  `headerStream` unexpectedly surfaces frame interleaving, T5 adds a mutex to `messageLogger` and records it.
- **OQ-3 (Story 3 scope) — DEFERRED-BY-DEFAULT:** ship Stories 1–2 as core (T1–T5); T6 (`--log-level` flag
  only, config block deferred) is included ONLY if trivial, else dropped and recorded as deferred. **Decision
  needed at plan-approval:** include T6 or defer?
- **OQ-4 (verbose payload bounding) — PROPOSED:** fixed byte cap (proposed `maxTraceVerboseBytes = 2048`)
  with an elision marker; cap-only redaction (no field-level redaction in v1). **Confirm the exact cap value
  at plan-approval** (pinned in T4's RED test).
- **New residual — per-file skip logMessage shape:** one-per-file `Warning` vs. a single build-end aggregate
  "N skipped" line. **Recommendation:** aggregate at build-end to avoid console flood (consistent with OQ-4's
  anti-flood intent). Pinned in T2's RED test.
