# Tasks — 03 Server lifecycle & protocol

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements covered:** FR-41 (stdio LSP lifecycle + capability advertisement), FR-42 (version
reporting), FR-43 (graceful degradation); NFR-11 (LSP conformance).
**ADRs in force:** ADR-002 (Analyzer seam), ADR-008 (position encoding negotiate UTF-8 / default
UTF-16), ADR-009 (`TextDocumentSyncKind.Full`), ADR-010 (`go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2`
v1.0.0), ADR-012 (snapshot-on-read + shutdown-cancelled background context), ADR-013 (fuzz the
extraction entry point as the FR-43 safety guard).

---

## Current-state findings & impact

Surveyed `cmd/natural-lsp/`, `internal/server/`, `internal/analysis/`, `internal/model/`,
`internal/config/`, `go.mod`, the `justfile`, and the LSP/Go knowledge bases. Findings drive the
decomposition below.

### What already exists

- **`internal/server/` is a pure stub.** `server.go`, `handlers.go`, `diagnostics.go`, `progress.go`
  contain only package docs and `TODO`s — **no `Server` type, no transport, no handlers.** Everything
  in this feature is net-new code in this package.
- **`cmd/natural-lsp/main.go` already owns flag parsing and the `--stdio` path.** `run(args,
  logger) int` is the testable entry point. It handles `--version` / `-version` (prints
  `natural-lsp %s\n` with the `version` build var), `--init`, and `--stdio`. The `--stdio` branch
  already calls `config.Bootstrap(start, "", logger)` (CR-6 fail-safe wiring is done and tested by
  `TestRunStdioCallsBootstrap`) but then prints `"stdio LSP server not yet implemented"` to stderr and
  returns 0. **This feature replaces that TODO with the real server run.**
- **`var version = "0.0.0-dev"` in `main.go`** is overridden at release via `-ldflags` (see the
  `justfile` `release` recipe). This is the single source of truth for the version string — FR-42's
  "same identifier from the running server" must read *this* value, so the server's `serverInfo` must
  be fed the version from `main`, not a second constant.
- **`internal/config` is complete (feature 01)** and exposes everything the server needs to construct
  itself: `config.Bootstrap(start, hint, logger) (root string, cfg Config, err error)`, `Config` with
  `Workspace.MaxFileSize int64`, `(*Config).IsExcluded(relPath) bool`, and the `SkipReason` surface
  (`SkipExcluded`, `SkipTooLarge`) plus `Problem`. **No config change is required** by this feature.
- **`internal/model` (feature 02)** defines `FileAnalysis{ObjectType, Diagnostics}`, `Diagnostic{Message,
  Severity}`, and `ObjectUnknown`. **No model change is required** by this feature — graceful
  degradation reuses the existing `Diagnostic`/`ObjectUnknown` surface.
- **`internal/analysis.Analyzer`** is `Analyze(path string, content []byte) (model.FileAnalysis,
  error)`; `analysis/natural` implements it and classifies by extension (feature 02). It is the only
  seam the server may depend on.
- **`go.mod` has no LSP/JSON-RPC dependency yet** — only `go-toml/v2`. ADR-010 selects
  `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` v1.0.0; adding them is part of this feature.
- **No integration-test harness exists yet.** The `justfile` runs `go test -tags integration ./...`
  in `verify`, but no `//go:build integration` test files are present. This feature introduces the
  first one (an end-to-end stdio handshake).

### Reconciliation of acceptance criteria against reality

| Criterion (story) | Classification | Plan |
|---|---|---|
| Stdio LSP message framing (S1) | new | provided by `go.lsp.dev/jsonrpc2` transport over stdin/stdout; T2 wires it, T9 asserts it end-to-end |
| `initialize` returns only supported capabilities (S1, FR-41, NFR-11) | new | T3 builds the capability set; at this phase **no feature providers are advertised** (definition/hover/etc. ship in 09–13) — only `textDocumentSync` (Full, ADR-009) and `positionEncoding` (ADR-008) |
| `initialize→initialized→shutdown→exit` sequence (S1) | new | T4 (shutdown/exit state machine) |
| Smoke `--stdio` produces well-formed initialize response (S1) | new | T9 integration test; T2/T3 make it possible |
| `--version` flag prints identifier and exits (S2, FR-42) | **already satisfied** | `main.go` handles `--version`; recorded, no task. T7 only verifies/locks it with a test if one is missing |
| Same version discoverable from running server (S2, FR-42) | extend | T3 threads `main.go`'s `version` into `serverInfo.version` in the initialize result |
| Malformed/oversized/unrecognized object skipped, others continue (S3, FR-43) | new + reuse | T5 per-file recover + reuse `config.MaxFileSize`/`SkipReason`/`model.ObjectUnknown`; T8 fuzz guard (ADR-013) |
| One request error does not crash server / corrupt index (S3, FR-43) | new | T6 per-request panic recovery → JSON-RPC error response |
| Skips/recoverable errors observable, never silent (S3, FR-43, NFR-6/14) | new + reuse | T5/T6 log via `slog` to **stderr** (never stdout); reuse `SkipReason` strings |
| In-flight work stopped on shutdown; cache writes safe (S4, FR-43) | partial | T4 cancels the shutdown-scoped `context.Context` (ADR-012); **cache writes are out of scope** here (feature 05) — T4 only owns the cancellation hook and clean goroutine teardown |
| Success exit on normal shutdown, non-zero on protocol violation (S4) | new | T4 + T10 map exit codes |

### Shared-contract impact / seam

- **No `internal/model` or `internal/analysis.Analyzer` change.** This feature lives entirely on the
  **LSP-facing side of the seam.** The server depends only on `analysis.Analyzer` + `internal/model`
  + `internal/config`. The `go.lsp.dev` dependency must stay **inside `internal/server`** and must not
  leak into `internal/analysis` or `internal/model` (ADR-002/010) — `review-seam` enforces this.
- **`cmd/natural-lsp/main.go` is the one consumer that migrates:** its `--stdio` TODO branch is
  replaced by a call into the new `server` package, passing `version`, `root`, `cfg`, and `logger`.
  `TestRunStdioCallsBootstrap` must stay green (Bootstrap still called on the `--stdio` path).

### Divergence flagged

- **CLAUDE.md / README describe a parser-based backend; ADR-001 (in the KB) still says "regex".** This
  is a known doc/KB divergence but **does not affect this feature** — feature 03 only depends on the
  `Analyzer` *interface*, not the backend strategy. Noted for `review-docs`; not actioned here.
- The README's `go install` path uses a `github.com/dkrieg/...` module path while `go.mod` is
  `natural-lsp`. Out of scope for this feature; noted.

---

## Ordered task list

Dependency order: dependency/scaffolding → transport → initialize → lifecycle state machine →
degradation (file + request) → version wiring → fuzz guard → integration → exit codes. Each task is one
red → green → refactor loop unless noted.

### T1 — Add the LSP transport/types dependency (ADR-010)

- **Behavior:** Add `go.lsp.dev/protocol` and `go.lsp.dev/jsonrpc2` (both v1.0.0) to `go.mod`; confirm
  the build resolves them. No production logic yet — this is the scaffolding step the rest depends on.
- **Fixtures:** none.
- **Expected result:** `go.mod`/`go.sum` updated; `just build` succeeds; `go vet ./...` clean. A
  trivial compile-only reference in `internal/server` (e.g. an unexported `var _ protocol.ServerCapabilities`
  in T2) proves importability.
- **Reuses/migrates:** none.
- **DoD:** `go mod tidy` run; `go build ./...` green; deps appear only under `internal/server`'s import
  graph (verified in later tasks); `gofmt`/`vet` clean.
- **TDD agents:** `tdd-green` (dependency add has no behavior to red-test) → `tdd-refactor`.
- **Depends on:** none.

### T2 — Transport: read/respond over an in-memory stream

- **Behavior:** Introduce the `Server` type in `internal/server/server.go` and a `Run(ctx, conn,
  version, root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger) error` entry
  point that serves a JSON-RPC connection. Drive it over an **in-memory `io.Reader`/`io.Writer` pair**
  (not real stdio) so it is unit-testable, per the stdlib KB. For this task the server need only decode
  a JSON-RPC request and write a well-formed response (echo/empty result is fine); the `initialize`
  payload arrives in T3.
- **Fixtures:** none (synthetic JSON-RPC messages built in-test).
- **Expected result:** Given a framed JSON-RPC request on the in-memory reader, the server writes a
  framed, well-formed JSON-RPC 2.0 response with the matching `id` to the writer. Logs go to the
  injected `slog` logger (stderr in production), **never to the protocol writer.**
- **Reuses/migrates:** depends only on `analysis.Analyzer` (passed in), `internal/config`, `internal/model`.
- **DoD:** table-driven unit test over the in-memory transport; response framing/`id` asserted; no
  write to stdout in code; seam purity (no `analysis/natural` import); `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T1.

### T3 — `initialize` handler: advertise only supported capabilities + serverInfo (FR-41, FR-42, NFR-11)

- **Behavior:** Handle the `initialize` request. Return `ServerCapabilities` advertising **only**:
  `textDocumentSync` = `Full` with `openClose: true` (ADR-009) and `positionEncoding` chosen from the
  client's `general.positionEncodings` — UTF-8 if offered, else UTF-16 (ADR-008). Advertise **no**
  feature providers (definition/references/hover/documentSymbol/workspaceSymbol/codeLens) — they do not
  exist yet and advertising them would make clients advertise-then-fail (FR-41). Populate
  `serverInfo` with name `natural-lsp` and the `version` passed from `main` (FR-42 — same identifier
  the `--version` flag prints).
- **Fixtures:** none (synthetic `InitializeParams`).
- **Expected result:**
  - Client offers `["utf-8","utf-16"]` → result `positionEncoding == "utf-8"`.
  - Client offers `["utf-16"]` or omits encodings → result `positionEncoding == "utf-16"`.
  - `capabilities` contains `textDocumentSync` (Full, openClose true) and **no** provider flags set.
  - `serverInfo.name == "natural-lsp"`, `serverInfo.version == <injected version>`.
- **Reuses/migrates:** none new; threads `version` from T2's `Run` signature.
- **DoD:** table-driven test covering the encoding-negotiation cases and the "no provider advertised"
  invariant; assert serverInfo version equals the injected value; seam purity; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T2.
- **Note:** the "no provider advertised" assertion is a regression guard — when features 09–13 add a
  provider, they will edit this test deliberately. Keep it as a single explicit allow-list.

### T4 — Lifecycle state machine: initialized / shutdown / exit + shutdown context (S1, S4)

- **Behavior:** Enforce the LSP lifecycle: `initialize` may be handled only once and must precede all
  other handling (requests before `initialize` → JSON-RPC error per spec); accept the `initialized`
  notification; on `shutdown` (request) stop accepting new feature requests and return a success
  result; on `exit` (notification) terminate the serve loop. On `shutdown`, **cancel the
  server's background `context.Context`** so in-flight/background work stops (ADR-012) — the
  cancellation hook only; **cache-write safety is feature 05's responsibility** (note it, don't build
  it). Return value distinguishes normal exit from protocol violation (for T10).
- **Fixtures:** none (scripted message sequences in-test).
- **Expected result:**
  - `initialize → initialized → shutdown → exit` → `Run` returns nil (clean), background context
    cancelled before return.
  - A request method before `initialize` → server responds with a JSON-RPC error (spec
    `ServerNotInitialized`), no panic.
  - A second `initialize` → JSON-RPC error (`InvalidRequest`), server still alive.
  - `exit` received **without** a prior `shutdown` → `Run` returns a non-nil/protocol-violation signal
    (drives T10's non-zero exit).
- **Reuses/migrates:** builds on T2's serve loop and T3's initialize handler.
- **DoD:** table-driven test scripting each sequence over the in-memory transport; assert returned
  error/nil per case and that the background context is cancelled on shutdown (`-race`, since it
  touches context/goroutine teardown); `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T3.

### T5 — Graceful degradation: per-file skip continues indexing, observably (FR-43, NFR-6/14)

- **Behavior:** Add the per-file analysis step the server uses when it processes a set of files (the
  loop the future indexer/document-open path calls). For each file: skip if oversized
  (`> cfg.Workspace.MaxFileSize`) with `SkipReason` `too_large`; skip if excluded via
  `cfg.IsExcluded`; classify unrecognized objects as `model.ObjectUnknown` (still processed, not
  dropped); and **recover from any panic in `Analyzer.Analyze` for one file without aborting the
  batch.** Every skip/recovery is logged via `slog` (stderr) with the file path and reason — never
  silent (NFR-6). A single bad file must not stop the others.
- **Fixtures:**
  - reuse an existing `testdata/objecttype/` fixture for the "recognized, succeeds" case;
  - one minimal unrecognized-extension fixture (e.g. `testdata/degradation/notnatural.txt`) for the
    `ObjectUnknown` path;
  - the oversized case is driven by setting a tiny `MaxFileSize` against an existing fixture (no large
    file committed);
  - the panic case is driven by a **stub `Analyzer` that panics** (test double), not a real backend —
    keeps the test at the seam.
- **Expected result:** Given a batch `[good, oversized, unknown, panicking]`, the server processes
  `good` and `unknown`, records `too_large` for the oversized one, recovers from the panic, continues,
  and emits one observable log line per skip/recovery. Returned per-file results never include a
  torn/partial entry for the panicking file.
- **Reuses/migrates:** reuses `config.MaxFileSize`, `config.IsExcluded`, `config.SkipReason`,
  `model.ObjectUnknown`, `analysis.Analyzer`. No model change.
- **DoD:** table-driven test with the stub analyzer covering all four file outcomes; assert logs carry
  reason+path; `-race` (batch may fan out per ADR-012, though a serial impl is acceptable here); seam
  purity; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T2.

### T6 — Graceful degradation: per-request panic recovery (FR-43, S3)

- **Behavior:** Wrap request dispatch so a panic while handling one request is recovered, logged
  (stderr), and converted into a JSON-RPC error response for that request id — the serve loop survives
  and keeps handling subsequent messages. The index/server state is not corrupted.
- **Fixtures:** none (a test handler that panics is registered in-test).
- **Expected result:** A request whose handler panics → client receives a JSON-RPC error response with
  the matching id (internal-error code); a subsequent valid request on the same connection is handled
  normally; `Run` does not return/crash.
- **Reuses/migrates:** builds on T2/T4 dispatch.
- **DoD:** table-driven test (`panic then valid request`) over the in-memory transport asserting the
  error response and continued service; `-race`; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T4.

### T7 — Lock the `--version` contract with a test (FR-42)

- **Behavior:** The `--version` flag is **already implemented** in `main.go`. Add the missing
  regression test (if none asserts it) that `run(["--version"], logger)` prints a line containing the
  version identifier and returns exit code 0, so FR-42's CLI half stays locked and stays consistent
  with the version the server reports (T3).
- **Fixtures:** none.
- **Expected result:** `run(["--version"], logger)` returns 0 and writes `natural-lsp <version>`.
  (If a test already covers this, record it as already-satisfied and skip.)
- **Reuses/migrates:** asserts existing `main.go` behavior; no production change expected.
- **DoD:** test added under `cmd/natural-lsp/main_test.go`; existing `main_test.go` still green;
  `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` (write the locking test) → `tdd-green` (likely already green — confirm no
  production change needed) → `tdd-refactor` (none).
- **Depends on:** none (parallel to T2–T6). Sequenced here because it pairs conceptually with T3.

### T8 — Fuzz guard: server file-processing never panics on arbitrary input (FR-43, ADR-013)

- **Behavior:** Add a Go native fuzz target over the per-file processing path from T5 (file path +
  arbitrary bytes), asserting the **"never panics / always returns"** safety property — the FR-43
  liveness guard at the LSP layer. Use the real `analysis/natural` analyzer behind the seam so the
  fuzz exercises classification + the T5 recovery wrapper together. Any crasher found is committed
  under `testdata/fuzz/...` and replays under plain `go test`.
- **Fixtures:** seed corpus = a couple of existing `testdata/objecttype/` fixtures + an empty input;
  crashers (if any) land in `testdata/fuzz/`.
- **Expected result:** `go test -run=Fuzz... -fuzz=... -fuzztime=...` finds no panic; the corpus seeds
  replay green under `just test`.
- **Reuses/migrates:** reuses T5's processing function and the real `natural` analyzer.
- **DoD:** fuzz target compiles and replays its seed corpus under plain `go test` (so CI exercises the
  committed corpus); `-race` on the seed replay; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` (target that would catch a panic) → `tdd-green` → `tdd-refactor`.
- **Depends on:** T5.

### T9 — Integration test: end-to-end stdio initialize handshake (FR-41, NFR-11, S1)

- **Behavior:** First `//go:build integration` test (the harness `just verify` already expects).
  Launch the built `natural-lsp --stdio` binary as a subprocess in a temp workspace containing a
  `.natural-lsp.toml` sentinel, write a framed `initialize` request to its stdin, and read a
  well-formed framed `initialize` response from its stdout. Then drive `initialized → shutdown → exit`
  and assert the process exits 0. This is the smoke criterion from Story 1.
- **Fixtures:** a temp workspace with an empty `.natural-lsp.toml` sentinel (created in-test, mirroring
  `TestRunStdioCallsBootstrap`); no `.NSx` files required for the handshake.
- **Expected result:** the response is valid JSON-RPC 2.0, carries the `initialize` `id`,
  `result.capabilities` advertises `textDocumentSync` and `positionEncoding` and **no** provider flags,
  `result.serverInfo.version` matches the binary's `--version` output; the process exits 0 after
  `exit`. Logs appear on stderr only; stdout carries protocol bytes only.
- **Reuses/migrates:** requires T2–T4 and the `main.go` migration (T10) to be in place to spawn the
  real server.
- **DoD:** `//go:build integration` file; `just test-integration` green; stdout never contains log
  lines; deterministic (no flakiness — bounded read timeout).
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T3, T4, T10.

### T10 — Wire the server into `main.go --stdio`; map exit codes (FR-41, S4)

- **Behavior:** Replace the `--stdio` TODO branch in `main.go`: after `config.Bootstrap`, construct the
  `analysis/natural` analyzer, build a stdio JSON-RPC connection over `os.Stdin`/`os.Stdout`, install a
  `signal.NotifyContext(ctx, os.Interrupt, SIGTERM)` shutdown context (stdlib KB), and call
  `server.Run(...)` passing `version`, `root`, `cfg`, analyzer, and the logger. Map the run outcome to
  the process exit code: **0 on a normal shutdown sequence, non-zero on a protocol violation** (S4,
  using T4's return signal). `TestRunStdioCallsBootstrap` must remain green (Bootstrap still called).
- **Fixtures:** none (covered end-to-end by T9).
- **Expected result:** `run(["--stdio"], logger)` no longer prints "not yet implemented"; on a clean
  scripted lifecycle it returns 0; on a protocol violation it returns non-zero. `slog` output stays on
  stderr; `os.Stdout` carries only protocol bytes.
- **Reuses/migrates:** **migrates the one consumer** — the `main.go --stdio` branch — and reuses
  `config.Bootstrap` (keep the `"sentinel found: true"` logging contract intact for the existing test).
- **DoD:** `TestRunStdioCallsBootstrap` and the T7 version test still green; unit-level exit-code mapping
  test (clean vs protocol-violation) via an injectable run; `gofmt`/`vet` clean; the `go.lsp.dev` deps
  do not leak outside `internal/server` (verify import graph).
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T2, T3, T4, T6.

---

## Reviews required (for `/review-feature`)

- **`review-protocol`** — FR-41/NFR-11 conformance: framing, lifecycle ordering
  (`initialize` once → `initialized` → `shutdown` → `exit`), capability advertisement matches
  implemented methods, position-encoding negotiation (ADR-008), sync kind (ADR-009), JSON-RPC error
  codes (`ServerNotInitialized`, `InvalidRequest`, internal error) and exit-code mapping.
- **`review-seam`** — the `go.lsp.dev` dependency and all LSP types stay inside `internal/server`; the
  server depends only on `analysis.Analyzer` + `internal/model` + `internal/config`; **no** import of
  `analysis/natural` from the LSP-facing code (T10's `main.go` wiring is the only place the concrete
  analyzer is constructed, which is allowed — it's the composition root).
- **`review-concurrency`** — `-race` clean; the shutdown-scoped context cancels background/in-flight
  work (ADR-012); no goroutine leak on `exit`; per-request and per-file recovery do not corrupt shared
  state.
- **`review-robustness`** — graceful degradation (FR-43): oversized/excluded/unknown/panicking inputs;
  the fuzz guard (ADR-013) replays its corpus; no skip is silent (NFR-6/14); logs go to stderr only.
- **`review-docs`** — CLAUDE.md "Project state", README capability list, and the command list must
  reflect that the stdio server now serves `initialize`/`shutdown` with `textDocumentSync` +
  `positionEncoding` and no feature providers yet; flag the ADR-001 regex/parser KB divergence and the
  README module-path mismatch (pre-existing) without necessarily fixing them in this feature.

---

## Remediation tasks (post-review-feature round 1)

Findings from review verdict FAIL. Each task is one red→green→refactor cycle; the RED step writes a
failing test that *would have caught* the finding.

### R1 — LSP `Content-Length` base-protocol framing (BLOCKER — B1)

- **Finding:** `internal/server/server.go` reads bare JSON via `jsontext.Decoder.ReadValue()` and
  writes bare JSON-RPC envelopes. No `Content-Length: N\r\n\r\n` header block is emitted or parsed.
  A real LSP client cannot communicate with this server (FR-41, NFR-11).
- **RED:** A failing unit test that writes a `Content-Length`-framed request to the server and asserts
  it receives a `Content-Length`-framed response — fails today because the server reads bare JSON.
- **GREEN:** Wrap the reader/writer pair in `go.lsp.dev/jsonrpc2` header-stream framing (use
  `jsonrpc2.NewHeaderStream` or equivalent from the library) and drive the serve loop through it.
  The existing `TestInitialize` / `TestLifecycle` / `TestRequestPanicRecovery` tests must be updated
  to also use framed encoding on the test-client side.
- **DoD:** A new test `TestFramedTransport` passes; all existing server tests adapted to use framed
  encoding; `go test -race ./internal/server/` green; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** none.

### R2 — Integration test must exercise real LSP wire format (MAJOR — M1)

- **Finding:** `TestStdioHandshake` uses `jsonrpc2.EncodeMessage`/`jsontext.Decoder` (unframed) on
  both ends. It validates the server's private dialect, not the LSP `Content-Length` wire format.
- **RED:** Fails once R1 is applied (the server now requires framed input; the unframed test breaks).
  If not already broken, write an assertion that the subprocess stdout starts with `Content-Length:`.
- **GREEN:** Rewrite `TestStdioHandshake` to write `Content-Length: N\r\n\r\n{json}` frames to the
  subprocess stdin and parse frames from stdout. Use `go.lsp.dev/jsonrpc2` header framing on the
  test-client side.
- **DoD:** `just test-integration` green; `TestStdioHandshake` reads and writes real LSP frames.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** R1.

### R3 — Assert JSON-RPC error codes in `TestLifecycle` (MAJOR — M2)

- **Finding:** `TestLifecycle` declares `expectErrCode` but never decodes the response — a regression
  in error codes would pass undetected (`server_test.go:436-507`).
- **RED:** Extend `TestLifecycle`'s assertion loop to decode the response from `outBuf` for the
  erroring-message cases and assert `resp.Err().Code == tc.expectErrCode`. The current test will fail
  because the assertion doesn't exist.
- **GREEN:** Add the response-decode + code-assertion to the test body. No production change expected.
- **DoD:** `TestLifecycle` asserts `ServerNotInitialized` (-32002) and `InvalidRequest` (-32600) codes;
  `-race` green.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** R1 (framing changes how responses are read in tests).

### R4 — Cancel background context on `shutdown`, not on loop exit (MAJOR — M3)

- **Finding:** `bgCancel` is `defer`red at function scope, so it fires when `Run` returns (on `exit`
  or EOF), not when `shutdown` is received. ADR-012 requires cancellation at `shutdown`
  (`server.go:122,230-236`).
- **RED:** A test that starts a goroutine listening on the server's background context, drives the
  lifecycle to `shutdown` (but not yet `exit`), and asserts the background context is cancelled
  before `exit` is sent. Fails today because `bgCancel` is deferred.
- **GREEN:** Call `bgCancel()` explicitly in the `shutdown` handler; keep the `defer` as the
  catch-all for error/EOF paths.
- **DoD:** The new test passes; `bgCancel()` called at `shutdown` proven; `-race` green.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** R1 (server transport changes).

### R5 — Assert `-32603` code in `TestRequestPanicRecovery` (MINOR — m1)

- **Finding:** `server_test.go:648` asserts `panicResp.Err() != nil` but not `-32603` (`InternalError`).
- **RED:** Add `assert code == jsonrpc2.InternalError`; fails before adding the assertion.
- **GREEN:** Add the code assertion. No production change.
- **DoD:** Test explicitly asserts code `-32603`; `-race` green.
- **TDD agents:** `tdd-red` → `tdd-green` (likely already satisfied by production code) → skip refactor.
- **Depends on:** R1.

### R6 — Remove dead `readAll` function (MINOR — m2)

- **Finding:** `readAll` (`server.go:37-53`) is defined but never called. Dead code with a documented
  goroutine-leak risk if ever wired naively.
- **RED:** A `go vet` or compile-check test that would catch the unused function. In practice: remove it
  and confirm the build stays green (no test needed for a removal — the build is the gate).
- **GREEN:** Delete `readAll`.
- **DoD:** `go build ./...` green; `readAll` absent; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-green` (removal; no red phase needed) → `tdd-refactor`.
- **Depends on:** R1 (may be relevant if R1 introduces a new read helper).

### R7 — Fix degradation log assertion to use `t.Errorf` (MINOR — m3)

- **Finding:** `degradation_test.go:182-192` uses `t.Logf` on mismatch — the FR-43 "never silent"
  guarantee is not actually pinned by the test.
- **RED:** Change `t.Logf` to `t.Errorf` (the test should fail when path/reason is absent from logs).
- **GREEN:** No production change — `degradation.go` already emits the correct log fields.
- **DoD:** Test fails when logging is suppressed; `-race` green.
- **TDD agents:** `tdd-red` → `tdd-green` → skip refactor.
- **Depends on:** none.

---

---

## Remediation tasks (post-review-feature round 2)

Findings from review verdict CONCERNS (round 2). R8 is MAJOR; R9–R11 are MINOR.

### R8 — Fix busy-spin on context cancellation (MAJOR — New-M1 from review-lsp-protocol)

- **Finding:** `internal/server/server.go` read loop (`for { stream.Read(ctx); ... }`) `continue`s on
  any non-EOF error. When `ctx` is cancelled (e.g. SIGTERM via `signal.NotifyContext` in `main.go`),
  `stream.Read` returns `ctx.Err()` immediately on every iteration — the loop spins indefinitely,
  flooding stderr. The server cannot be stopped cleanly by a real editor or OS signal.
- **RED:** A test that cancels the server's context mid-serve and asserts `Run` returns `nil` (clean exit)
  rather than spinning. Use a blocking reader (`blockingReader`) so the only way out is context
  cancellation. Fails today because `continue` loops forever.
- **GREEN:** In the read loop error branch, check `errors.Is(err, context.Canceled) ||
  errors.Is(err, context.DeadlineExceeded)` → `return nil`. Also check `errors.Is(err,
  io.ErrUnexpectedEOF)` → `return fmt.Errorf("stream closed: %w", err)` (unrecoverable transport error,
  not a per-message parse failure). Only `continue` on body-decode errors where the frame was fully
  consumed and the stream position is known.
- **DoD:** New test `TestContextCancellationExitsCleanly` passes; `go test -race ./internal/server/`
  green; server exits cleanly on context cancel in existing tests; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** none.

### R9 — Fix stale comment in TestLifecycle NormalSequence (MINOR — m3 from review-acceptance)

- **Finding:** `server_test.go` `TestLifecycle` NormalSequence comment says bg-context cancellation is
  "deferred to feature 05" but `TestShutdownCancelsBgContext` already covers it. The comment is
  misleading.
- **RED:** No test needed — it is a comment. Update the comment directly to reference
  `TestShutdownCancelsBgContext`. Confirm the build and test suite stay green.
- **DoD:** Comment updated; `go test ./internal/server/` green.
- **TDD agents:** `tdd-green` only (comment fix, no behavior change).
- **Depends on:** none.

### R10 — Remove orphaned testdata fixture (MINOR — m2 from review-acceptance)

- **Finding:** `testdata/degradation/notnatural.txt` exists but no test references it. It was originally
  planned as a fixture for T5's unrecognized-extension path, but `TestProcessFiles` uses inline bytes.
- **RED:** No test needed — the file is simply unused. Remove it to avoid future confusion.
- **DoD:** File removed; `go test ./...` green.
- **TDD agents:** `tdd-green` only (file removal, no behavior change).
- **Depends on:** none.

### R11 — Remove dead `Server` struct (MINOR — m4 from review-acceptance)

- **Finding:** `internal/server/server.go` declares `type Server struct { // TODO: fields to be added as
  features land }` which is never instantiated. `Run` is a package function. Dead code.
- **RED:** No test needed — build/vet gate. Remove the struct and confirm the build stays green.
- **DoD:** `Server` struct removed; `go build ./...` green; `gofmt`/`vet` clean.
- **TDD agents:** `tdd-green` only (dead-code removal, no behavior change).
- **Depends on:** none.

---

## Open questions

1. **Cancellation in this release (from plan):** Should `$/cancelRequest` (`-32800
   RequestCancelled`) be implemented now? This feature has no long-running feature handlers yet, so
   cancellation has nothing to cancel. **Proposed:** defer real cancellation to the first feature that
   adds a heavy handler (09–13); T4 only establishes the shutdown-scoped context that those handlers
   will thread. Confirm.
2. **Missing-shutdown tolerance (from plan):** If the client sends `exit` without a prior `shutdown`,
   the spec says exit non-zero (protocol violation). If the client just drops the connection (EOF on
   stdin) without either, should the server exit 0 (tolerant) or non-zero? **Proposed:** EOF →
   clean exit 0 (editor closed); `exit`-without-`shutdown` → non-zero. Confirm.
3. **Does the server proactively index on startup in this feature, or only on document open?** The
   per-file degradation logic (T5) is needed regardless, but the *driver* (full-workspace scan) is
   feature 05 (indexing). **Proposed:** T5 delivers the reusable per-file processing function and its
   degradation guarantees; no workspace-wide scan is wired in feature 03. Confirm the boundary with 05.
4. **Exit code value for protocol violation** — `1` sufficient, or a distinct code? **Proposed:** `1`.
