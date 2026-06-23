# Feature: Editor clients

**Status:** Planned
**PRD requirements:** FR-44, FR-45, FR-46; NFR-10, NFR-12, NFR-13
**Priority / phase:** P0 (VS Code) · P1 (JetBrains, documented configs for other editors)
**Depends on:** [03](../03-server-lifecycle-and-protocol/plan.md)

## Summary

Gets the server in front of users in their actual editors. The VS Code extension is a first-party,
zero-config-when-possible launcher; JetBrains is a first-party integration; and other LSP editors are
supported via documented configuration. The server itself ships as native binaries for the major
platforms.

## User stories

### Story 1 — VS Code extension (FR-44, NFR-13)
**As a** VS Code user, **I want** the server to start automatically **so that** features just work
when I open a Natural file.

**Acceptance criteria:**
- [ ] Opening a Natural source file activates the extension and launches the server.
- [ ] With the server binary discoverable on the system path, no additional configuration is required.
- [ ] A setting allows pointing at a specific server binary location.
- [ ] Natural file types are associated so the language is recognized.

### Story 2 — JetBrains integration (FR-45)
**As a** JetBrains user (including Community editions), **I want** a supported way to run the server
**so that** I get the same features.

**Acceptance criteria:**
- [ ] A documented, first-party path runs the server in JetBrains IDEs, including Community editions.
- [ ] The integration associates the Natural file types with the server.
- [ ] Setup steps are documented and reproducible.

### Story 3 — Other LSP editors (FR-46)
**As a** Neovim/Zed/Helix user, **I want** documented configuration **so that** I can use the server
without a bespoke plugin.

**Acceptance criteria:**
- [ ] Documented, working configuration exists for at least Neovim, Zed, and Helix.
- [ ] Each documents file-type association and workspace-root detection (via the sentinel file).
- [ ] Following the docs yields working navigation against a sample workspace.

### Story 4 — Distribution & install (NFR-10, NFR-12)
**As a** user on any major platform, **I want** easy installation **so that** I can get started
quickly.

**Acceptance criteria:**
- [ ] Native binaries are published for the major desktop platforms and common CPU architectures.
- [ ] Multiple install paths are documented (pre-built binary, build-from-source, package-style
      install).
- [ ] A freshly installed binary reports its version and passes the stdio smoke check.

## Out of scope
- Server features themselves (plans 03, 05–14).
- IDE-specific syntax highlighting/grammars beyond what's needed for file-type association (TBD).

## Open questions
- Whether the clients live in this repository or separate repositories (versioning trade-off).
- Whether a basic syntax grammar (for highlighting) is in-scope for the first client releases.
- Marketplace/distribution channels for the VS Code and JetBrains clients.
