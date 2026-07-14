# Feature: Async Indexing & Work-Done Progress

**Status:** Planned
**PRD requirements:** FR-32 (P0 — unimplemented), NFR-5
**Priority / phase:** P0 remediation (2026-07-14 assessment, defects #3 and #4)
**Depends on:** [05](../05-workspace-indexing-and-cache/plan.md), [20](../20-workspace-root-handshake/plan.md)

## Summary

Deliver the two halves of feature 05 that never shipped. FR-32 (P0) requires indexing progress
"via the editor's standard progress mechanism": `internal/server/progress.go` is a TODO stub, no
`window/workDoneProgress/create` or `$/progress` is ever sent, and `workspace.Build` is invoked
with `onProgress: nil` (`server.go:387`) even though the workspace-side callback (task 05-S01)
shipped with tests. NFR-5 requires indexing not to block interaction: `Build` currently runs
synchronously inside the `initialized` handler on the single-threaded dispatch loop, so a cold
index stalls every request. This feature moves the initial build to a background goroutine
publishing under the existing `idxResMu` write lock (the F7 build-then-publish pattern
`applyDocumentChange` already uses) and wires the existing `Build(onProgress)` callback to LSP
work-done progress.

## User stories

### Story 1 — Visible indexing progress (FR-32)
**As a** developer opening a large workspace, **I want** a progress indicator with a count or
percentage in my editor **so that** I know the server is working and how far along it is.

**Acceptance criteria:**
- [ ] When the client advertises `window.workDoneProgress`, the server sends
      `window/workDoneProgress/create` after `initialized`, then `$/progress` `begin` →
      `report` (with a files-processed count/percentage from the existing `Build` callback) →
      `end` when the index is ready.
- [ ] When the client does not advertise work-done progress support, no progress requests are
      sent (capability-gated, like dynamic watcher registration) and indexing proceeds normally.
- [ ] A server test observes the full create/begin/report/end sequence over the wire (the
      `TestServer_ProgressReporting` the feature-05 plan specified but never got).
- [ ] Warm starts served from cache emit begin → end without a misleading long report phase.

### Story 2 — Indexing off the dispatch loop (NFR-5)
**As a** developer, **I want** the editor to stay responsive during the cold index build
**so that** early requests are answered instead of queued behind indexing.

**Acceptance criteria:**
- [ ] The initial `workspace.Build` runs on a background goroutine (tied to the existing `bgCtx`
      so shutdown still cancels it — ADR-012); the dispatch loop keeps servicing messages while
      it runs.
- [ ] The built `(idx, res)` pair is published atomically under the `idxResMu` write lock
      (build-then-publish, F7); provider snapshots never observe a torn pair.
- [ ] A test proves a request (e.g. `documentSymbol` on an open buffer) is answered while a
      deliberately slow build is in flight, and index-backed requests degrade gracefully
      (null/empty per FR-43) before publication rather than blocking or erroring.
- [ ] `didOpen`/`didChange` received during the build are not lost: the store serves live
      buffers immediately, and post-publication the index reflects them (document the chosen
      reconciliation — replay or re-analyze — in the plan decisions).
- [ ] `go test -race` passes; shutdown during an in-flight build exits cleanly (no goroutine
      leak, no publish-after-shutdown).

### Story 3 — Stub retirement
**As a** maintainer, **I want** the stale scaffolding removed **so that** the code matches the
as-built architecture.

**Acceptance criteria:**
- [ ] `internal/server/progress.go` holds the real progress helpers (or is deleted in favor of
      their actual location); the `handlers.go` package-doc stub and the stale `test/panic`
      "will be removed once features 09–13 land" comment are cleaned up.
