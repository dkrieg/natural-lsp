# Feature: LSP Tracing & Structured Server Logging

**Status:** Planned
**PRD requirements:** FR-53 (LSP-native observability — new), NFR-14 (observability/legibility), FR-43 (graceful degradation)
**Priority / phase:** P1 (v1.0 stable; improves multi-editor trust — real JetBrains/LSP4IJ usage)
**Depends on:** [03](../03-server-lifecycle-and-protocol/plan.md) (lifecycle/dispatch), [20](../20-workspace-root-handshake/plan.md) (the `window/showMessage` sender + no-usable-root legibility), [21](../21-async-indexing-and-progress/plan.md) (background build + `$/progress` + the outbound-notification pattern), [19](../19-protocol-marshaling-unification/plan.md) (json/v2 marshaling discipline)

## Summary

Today the server's only observability channel is **stderr `slog`** (plus a single one-shot
`window/showMessage` Warning from feature 20 when no usable root is found). Nothing the server does —
indexing progress detail, cache hit/rebuild, per-file skips, resolution ambiguities, request errors — is
visible **inside the editor**. For JetBrains users on **LSP4IJ** this is a real gap: LSP4IJ has a rich
**LSP Consoles** view whose **Logs tab surfaces `window/logMessage`** notifications and whose per-server
**Trace** setting (`off`/`messages`/`verbose`) is meant to drive standard LSP tracing — but our server
emits neither `window/logMessage` nor `$/logTrace`, and it ignores the `trace` handshake entirely, so
those panes stay empty and troubleshooting falls back to hunting the stderr stream (which LSP4IJ does not
show prominently).

This feature makes the server a **well-behaved LSP tracing/logging citizen**, mirroring what
`gopls`/`typescript-language-server`/`clangd` do and what LSP4IJ expects:

1. **`window/logMessage`** for operational events (index start/finish with counts, cache hit vs. rebuild,
   per-file skips/degradation, resolution ambiguity, request errors) — severity-tagged via `MessageType`
   (`Error`/`Warning`/`Info`/`Log`). This is the primary win: it populates the LSP4IJ **Logs tab** and the
   VS Code output channel with the same story the stderr log already tells (NFR-14 — "make its own limits
   legible: what was indexed, what was skipped, what could not be resolved, and why").
2. **The LSP trace handshake** — honor `InitializeParams.trace` (initial level, default `off`), handle the
   inbound **`$/setTrace`** notification to change the level at runtime, and emit **`$/logTrace`**
   notifications for per-RPC tracing gated on the level (`messages` = method + timing; `verbose` = plus a
   bounded params/result summary). This mirrors `gopls -rpc.trace` and is exactly what LSP4IJ's Trace combo
   toggles.

It is **server-layer only** — **no `internal/model` change, no cache-format bump, no new server
capability** (`window/logMessage`, `$/logTrace`, and `$/setTrace` are unilateral/notification-level, gated
on nothing in `ServerCapabilities`, just like `publishDiagnostics` and `window/showMessage`), so the locked
`TestInitialize` capability allow-list is unchanged. It sits entirely on the LSP-facing side of the
Analyzer seam.

## Research: how LSP4IJ and other LSP servers do tracing (sourced)

**LSP4IJ (Red Hat).** LSP4IJ exposes a per-server **Trace** level (`off` / `messages` / `verbose`) in
*Settings → Languages & Frameworks → Language Servers → (server) → Debug*. Its **LSP Consoles** tool window
shows two things: the **JSON-RPC traffic** the client itself exchanges (this is rendered *client-side* by
LSP4IJ from the frames it sends/receives — the server does nothing for it) and a **Logs tab that displays
`window/logMessage` notifications** emitted by the server (in verbose mode entries expand for detail).
LSP4IJ maps the Trace combo to the standard LSP mechanism — the `trace` value in `initialize` and the
`$/setTrace` notification — so a server that participates emits `$/logTrace` accordingly.
Sources: LSP4IJ `docs/UserGuide.md` (LSP Consoles / Logs tab / Debug-Trace); `docs/UserDefinedLanguageServer.md`.

**gopls.** Emits `window/logMessage` for operational logging and supports **`-rpc.trace`**, which prints the
full RPC trace ("lsp inspector format") — the server-side equivalent of `verbose` tracing.
Source: gopls troubleshooting docs (`-rpc.trace`).

**typescript-language-server / clangd.** Both emit `window/logMessage` and honor the standard `trace`
handshake; clangd additionally has `--log=verbose`. The common, portable denominator across all of them is:
**`window/logMessage` for events** + **honor `trace`/`$/setTrace` and emit `$/logTrace` for per-RPC detail**.

**The exact wire shapes** (already present in the vendored `go.lsp.dev/protocol` v1.0.0 — *no new
dependency, no new types to define*):
- `InitializeParams.Trace TraceValue` (`json:"trace"`); `TraceValue` ∈ `TraceValueOff`="off" /
  `TraceValueMessages`="messages" / `TraceValueVerbose`="verbose". Omitted ⇒ off.
- `$/setTrace` (client→server notification): `SetTraceParams{ Value TraceValue }`.
- `$/logTrace` (server→client notification): `LogTraceParams{ Message string; Verbose *string }`.
- `window/logMessage` (server→client notification): `LogMessageParams{ Type MessageType; Message string }`;
  `MessageType` ∈ `Error`=1 / `Warning`=2 / `Info`=3 / `Log`=4.

All four already implement the json/v2 `MarshalJSONTo` path used by feature 19; the outbound-notification
mechanism (`jsonrpc2.NewNotification(method, rawJSON)` → `stream.Write`) already exists (progress,
diagnostics, showMessage).

## User stories

### Story 1 — Operational events are visible in the editor (NFR-14, FR-53)
**As a** JetBrains/LSP4IJ (or VS Code) user, **I want** the server's indexing/cache/resolution/skip/error
events to appear in the editor's LSP log **so that** I can see what was indexed, what was skipped, and what
couldn't be resolved without reading a hidden stderr stream.

**Acceptance criteria:**
- [ ] The server emits `window/logMessage` for the key operational events, each with an appropriate
      `MessageType`: index build **begin** (root + file count) and **end** (indexed/skipped/total counts) —
      `Info`; **cache** outcome (warm hit vs. version-mismatch/corrupt rebuild) — `Info`; **per-file skip**
      (too-large / excluded / unreadable / analyzer-panic-recovered) — `Warning`; **resolution ambiguity**
      — `Warning`; **request-dispatch panic / internal error** — `Error`.
- [ ] The messages carry the **same information** the existing stderr `slog` lines carry (the stderr log is
      retained; `window/logMessage` is an additional sink, not a replacement — a **dual sink**).
- [ ] Emitting a `window/logMessage` **never blocks or fails a request** and never panics if the write
      fails (fire-and-forget; a write error is logged to stderr only — FR-43), mirroring the progress
      reporter's contract.
- [ ] No new server capability is advertised; the locked `TestInitialize` allow-list is unchanged.
- [ ] A test asserts the **wire bytes** of a representative `window/logMessage` (correct method, `type`
      integer, `message` string) via the json/v2 marshal path (feature 19 discipline).

### Story 2 — Standard trace handshake: honor `trace`, `$/setTrace`; emit `$/logTrace` (FR-53)
**As a** power user debugging server behavior, **I want** the editor's Trace level (`off`/`messages`/`verbose`)
to control per-RPC tracing from the server **so that** I get gopls-style RPC traces on demand and silence
by default.

**Acceptance criteria:**
- [ ] The server reads `InitializeParams.trace` at `initialize` and adopts it as the initial trace level
      (absent ⇒ `off`).
- [ ] The server handles the inbound **`$/setTrace`** notification and updates the active level at runtime
      (no restart), with graceful handling of an unknown value (treat as `off`, log a stderr Warn — CR-6
      style fail-safe, never crash).
- [ ] When the level is **`messages`** or **`verbose`**, the server emits **`$/logTrace`** for
      request/response activity: `messages` = method name + direction + duration; `verbose` = additionally a
      **bounded** params/result summary (size-capped so a huge payload can't flood the console).
- [ ] When the level is **`off`**, **no `$/logTrace` is emitted** (the default — zero console noise).
- [ ] `$/logTrace` emission is fire-and-forget and never blocks/fails/panics a request (FR-43).
- [ ] A test asserts: level `off` ⇒ no `$/logTrace` bytes on the wire; level `messages` ⇒ a `$/logTrace`
      with a non-empty `message` and **no** `verbose`; level `verbose` ⇒ `verbose` populated; and that
      `$/setTrace` flips the behavior at runtime.

### Story 3 — Startup log-level control (NFR-14) *(secondary; see OQ-3)*
**As a** user launching the server, **I want** to control stderr verbosity (and the default trace level)
**so that** I can get more or less detail without editing code.

**Acceptance criteria:**
- [ ] A `--log-level` flag (and/or a `[logging]` block in `.natural-lsp.toml`) sets the stderr `slog` level
      (`error`/`warn`/`info`/`debug`), defaulting to today's behavior. Invalid value ⇒ default + actionable
      `Problem` (CR-6 fail-safe), never a crash.
- [ ] The setting is documented (README + `--init` sample); `--version`/existing CLI shape is unchanged.
- [ ] *(Deferred-or-included per OQ-3.)* If the trace default should be configurable independently of the
      `initialize` `trace` value, it is called out; otherwise the client-supplied `trace` is authoritative.

## Out of scope / deferred
- **Client-side rendering of JSON-RPC traffic** — LSP4IJ/VS Code render the raw frames they exchange
  themselves; the server neither can nor should drive that pane.
- **A `logTrace`/`logMessage` *reading* path** — the server only *emits*; it does not consume client logs.
- **Telemetry / `telemetry/event`** — analytics is a separate concern, explicitly not part of observability.
- **Structured/JSON stderr log formatting overhaul** — retain the current `slog` handler; this feature adds
  the LSP sinks, not a logging-framework migration (unless OQ-3 pulls in a minimal level flag).
- **Per-request cancellation tracing** beyond method+timing.

## Open questions (to resolve at `/plan-feature` / task decomposition)
- **OQ-1 — logMessage gating.** Should `window/logMessage` always emit (severity-filtered — e.g. always
  `Warning`/`Error`, `Info` for major lifecycle) regardless of trace level, or be gated by trace level?
  **Recommendation:** always emit operational events (severity-filtered), independent of the trace level —
  LSP4IJ's Logs tab shows `logMessage` regardless of Trace, and this is the NFR-14 win. The trace level
  gates only the `$/logTrace` per-RPC firehose. (Optionally also gate the noisiest `Log`/`Info`-level
  messages behind `messages`/`verbose`.)
- **OQ-2 — outbound-write serialization (concurrency).** `window/logMessage` and `$/progress` are emitted
  from the **feature-21 background build goroutine**, while request responses and `$/logTrace` are written
  from the serial dispatch loop — two goroutines writing the same `jsonrpc2.Stream`. Confirm whether writes
  are already serialized; if not, this feature should add a small **outbound write mutex** (a
  `messageLogger`/notifier that owns the stream and guards `Write`) so frames can't interleave. This is the
  main concurrency design point and should get a `review-concurrency` pass and a `-race` test.
- **OQ-3 — scope of Story 3.** Include the `--log-level` flag / `[logging]` config now, or defer it and ship
  only the LSP sinks (Stories 1–2)? **Recommendation:** ship Stories 1–2 as the core; include the
  `--log-level` flag as a small addition only if it stays trivial, else defer to a follow-up.
- **OQ-4 — verbose payload bounding.** What is the size cap and redaction policy for `verbose` `$/logTrace`
  params/result summaries (avoid flooding the console and avoid leaking large source bodies)? Propose a
  fixed byte cap with an elision marker; confirm at planning.

## Notes
- Analysis basis (code is ground truth): the server has **no** `trace`/`$/setTrace`/`$/logTrace`/
  `window/logMessage` handling today — `grep` for these in `internal/server/*.go` returns nothing; the only
  outbound message-ish notification is feature 20's one-shot `window/showMessage`. The dispatch switch
  (`internal/server/server.go`) lists ~20 handled methods; `$/setTrace` would be a new **notification** case
  (like `initialized`/`didOpen`). The outbound pattern to reuse is `jsonrpc2.NewNotification` +
  `stream.Write` (see `progress.go`, `diagnostics.go`).
- Marshaling MUST use the json/v2 `MarshalJSONTo` path (feature 19); the protocol types
  (`LogMessageParams`/`LogTraceParams`/`SetTraceParams`) implement it.
- Suggested shape: a small `messageLogger` type (sibling to `progressReporter`) that owns the stream, holds
  the atomic trace level, exposes `logMessage(type, msg)` and `logTrace(msg, verbose)`, and serializes
  writes (OQ-2). Consider routing the existing `slog` sites through a thin dual-sink wrapper so stderr and
  the LSP log stay in lockstep without duplicating call sites.
- Record no cache/model/version change in the feature's completion note (mirrors features 19/20/21).
