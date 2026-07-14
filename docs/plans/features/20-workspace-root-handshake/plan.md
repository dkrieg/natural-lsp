# Feature: Workspace Root Handshake

**Status:** Planned
**PRD requirements:** FR-1 (repair/extension), NFR-11, NFR-13, NFR-14
**Priority / phase:** P0 remediation (2026-07-14 assessment, defect #2)
**Depends on:** [03](../03-server-lifecycle-and-protocol/plan.md), [15](../15-editor-clients/plan.md)

## Summary

Honor the workspace root the client sends in `initialize`. Today the server determines the
workspace root **solely** from `os.Getwd()` + `.natural-lsp.toml` sentinel walk-up
(`cmd/natural-lsp/main.go:51-55`); `InitializeParams.workspaceFolders` and `rootUri` are never
read. Demonstrated live: launched with any other cwd, the server initializes normally but every
index-backed feature silently returns null/empty — no error, no diagnostic. VS Code works only
because `vscode-languageclient` defaults the child process cwd to the first workspace folder;
the README's documented Neovim config sets `root_dir`/`root_markers`, which only shape the
`rootUri` the server discards. After this feature the root derives, in order: first
`workspaceFolders` entry → `rootUri` → cwd walk-up (backward-compatible fallback), with the
sentinel walk-up still applied *from* the client-provided path so nested opens find the true
workspace root.

## User stories

### Story 1 — Root from the initialize handshake (FR-1, NFR-11)
**As a** user of any LSP client (Neovim, Zed, Helix, JetBrains), **I want** the server to index
the workspace my editor told it about **so that** navigation works regardless of the server
process's working directory.

**Acceptance criteria:**
- [ ] When `initialize` carries `workspaceFolders`, the first folder is used as the root-discovery
      start point; when only `rootUri` is present, it is used; only when neither is present does
      the server fall back to cwd (current behavior).
- [ ] The sentinel walk-up (`config.FindRoot`) runs from the client-provided path, so opening a
      subdirectory of a configured workspace still lands on the `.natural-lsp.toml` root.
- [ ] An integration-style test launches the server with a cwd **outside** the workspace, passes
      `rootUri` for the sample workspace, and proves `textDocument/definition` resolves across
      files (the exact live-probe failure from the assessment, as a regression test).
- [ ] Config, cache location, and file watching all follow the negotiated root (not cwd).

### Story 2 — Deferred root resolution in the lifecycle (FR-41)
**As a** maintainer, **I want** root/config bootstrap moved from `main` startup to the
`initialize` request **so that** the root can depend on handshake parameters without changing
observable lifecycle behavior.

**Acceptance criteria:**
- [ ] `initialize` → `initialized` ordering, the locked capability allow-list, and the existing
      `TestInitialize` expectations are unchanged.
- [ ] Config problems discovered at the deferred bootstrap are still logged/degraded per CR-6
      (never crash the handshake).
- [ ] `--stdio < /dev/null` and the full smoke lifecycle still exit cleanly (scripts/smoke.sh).

### Story 3 — An unusable root is legible, not silent (NFR-14)
**As a** user whose editor sent no usable root and whose cwd has no sentinel, **I want** an
explicit signal **so that** an empty index is diagnosable instead of features silently returning
nothing.

**Acceptance criteria:**
- [ ] When no workspace root can be established (or the established root contains no indexable
      files), the server logs an actionable stderr message naming the paths it tried.
- [ ] A `window/showMessage` warning is sent when the client supports it (first use of
      `window/showMessage`; optional — if cut, the stderr path above is mandatory and the cut is
      recorded in the plan).
- [ ] Requests against files outside the established root keep degrading gracefully (null/empty,
      never an error response) — existing FR-43 tests still pass.

### Story 4 — Editor docs updated to match (FR-46)
**As a** reader of the README, **I want** the editor sections to state the root contract
**so that** setup instructions are truthful for each client.

**Acceptance criteria:**
- [ ] README editor sections describe root negotiation (and drop any implication that server cwd
      determines the workspace once this ships).
- [ ] CLAUDE.md project-state note reflects the new root-resolution order.
