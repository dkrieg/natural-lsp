# Feature: Server lifecycle & protocol

**Status:** Planned
**PRD requirements:** FR-41, FR-42, FR-43; NFR-11
**Priority / phase:** P0
**Depends on:** none (foundation)

## Summary

The server runs as a standard stdio LSP server. It completes the initialize/shutdown handshake,
advertises exactly the capabilities it supports, reports its version, and degrades gracefully so a
single bad object never takes the server down. Any compliant LSP client must be able to drive it.

## User stories

### Story 1 — Standard LSP lifecycle (FR-41, NFR-11)
**As an** editor integrator, **I want** a spec-compliant stdio LSP server **so that** any LSP client
can use it.

**Acceptance criteria:**
- [ ] The server communicates over stdio using the LSP message framing.
- [ ] On `initialize`, it returns server capabilities that list **only** the features it actually
      supports at the current phase; clients do not advertise-then-fail.
- [ ] The `initialize` → `initialized` → `shutdown` → `exit` sequence is handled correctly.
- [ ] A smoke invocation (`--stdio` with no work) produces a well-formed initialize response shape.

### Story 2 — Report version (FR-42)
**As a** user filing a bug, **I want** to know exactly which build I'm running **so that** issues are
reproducible.

**Acceptance criteria:**
- [ ] A version flag prints a clear version identifier and exits.
- [ ] The same version identifier is discoverable from the running server (e.g. server info in the
      initialize result).

### Story 3 — Graceful degradation (FR-43)
**As a** developer with messy legacy code, **I want** one broken file not to break everything **so
that** the server stays useful.

**Acceptance criteria:**
- [ ] A malformed, oversized, or unrecognized object is skipped; indexing and request handling
      continue for all other files.
- [ ] An error while handling one request does not crash the server or corrupt the index.
- [ ] Skips and recoverable errors are observable (logs and/or diagnostics), never silent.

### Story 4 — Clean shutdown
**As an** editor, **I want** the server to exit cleanly **so that** resources and locks are released.

**Acceptance criteria:**
- [ ] On shutdown/exit, in-flight work is stopped and any cache writes are completed or safely
      abandoned without corrupting the cache (see [05](05-workspace-indexing-and-cache.md)).
- [ ] The process exits with a success status on a normal shutdown sequence and non-zero on a
      protocol violation.

## Out of scope
- The specific feature handlers (definition, hover, etc.) — see plans 09–13.
- Transport modes other than stdio (not in scope for the first release).

## Open questions
- Whether cancellation of in-flight requests must be supported in the first release.
- Behavior expected when the client omits the shutdown handshake (hard exit tolerance).
