# Feature: Execute Command (`workspace/executeCommand`)

**Status:** Planned
**PRD requirements:** FR-60 (server commands — new); enabler for FR-29 (code lens) actions and any future code actions
**Priority / phase:** P2 (post-v1.0; an **enabler** — build when a command actually needs a server round-trip)
**Depends on:** [03](../03-server-lifecycle-and-protocol/plan.md) (capabilities/dispatch), [13](../13-code-lens/plan.md) (current client-side command convention), [21](../21-async-indexing-and-progress/plan.md) (index rebuild + progress)

## Summary

`workspace/executeCommand` lets the client ask the **server** to run a named command (advertised via
`executeCommandProvider.commands`), typically triggered from a code lens or code action. Today we need
none: the code-lens command is the **client-side** `editor.action.showReferences` (feature 13), so the
server does no command execution. This feature adds the mechanism **only when a command genuinely needs
server state** — the clearest near-term candidate is **rebuild/refresh the index** (a "Natural LSP: Reindex
workspace" command that drops the cache and re-runs the feature-21 background build, reporting
work-done progress). It is the prerequisite for server-driven **code actions** later.

**Honest scoping:** this is an **enabler**, not a user-facing feature on its own — a command registry with
no commands is pointless. So the plan is: land the `executeCommandProvider` plumbing **together with the
first real command** (reindex), and add more commands as code-actions/lenses need them.

**Server-layer only — no `internal/model` change, no cache-format bump.** Adds an `executeCommandProvider`
capability; `TestInitialize` gains one entry.

## User stories

### Story 1 — Command dispatch plumbing + "Reindex workspace" (FR-60)
**As a** user, **I want** a "Reindex workspace" command **so that** I can force a fresh index without
restarting the server.

**Acceptance criteria:**
- [ ] The server advertises `executeCommandProvider` listing its supported command id(s) (namespaced, e.g.
      `naturalLsp.reindexWorkspace`) and handles `workspace/executeCommand`, dispatching by id with a
      recovered per-command panic guard (unknown id → a JSON-RPC error, never a crash — FR-43).
- [ ] `naturalLsp.reindexWorkspace` invalidates the cache and re-runs the feature-21 background build on
      `bgCtx`, publishing work-done progress; providers degrade to null/empty until it republishes
      (build-then-publish, F7), exactly like the initial build.
- [ ] The VS Code extension contributes the command to the palette (and optionally a status action);
      `TestInitialize`'s allow-list is updated to include `executeCommandProvider`.

## Out of scope / deferred
- **Code actions** (`textDocument/codeAction`) that would *use* commands — a separate feature; this one is
  the dispatch substrate + one concrete command.
- **`workspace/applyEdit`** (server-initiated edits) — deferred until a command needs to mutate source.
- Speculative commands with no consumer — add commands only as lenses/actions require them.

## Open questions
- **OQ-1 — command surface.** Ship only `reindexWorkspace` first? Recommend yes (one real, testable
  command); grow the registry as code actions arrive.
- **OQ-2 — concurrency.** Reindex shares the `bgBuild`/`idxResMu` machinery (feature 21) — confirm a
  reindex triggered mid-build cancels/joins cleanly (a `review-concurrency` pass + `-race` test).

## Notes
- No `internal/model`/cache change. New capability (`executeCommandProvider`) → `TestInitialize` extended.
  Reuses feature 21's background-build + progress; json/v2 marshaling (feature 19). Fuzz the command
  dispatcher on arbitrary id/args (never panic — FR-43).
