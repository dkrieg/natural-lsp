# Feature: Document lifecycle & sync

**Status:** Planned
**PRD requirements:** FR-33, FR-34
**Priority / phase:** P0 (open document handling) · P1 (external file watching)
**Depends on:** [03 Server lifecycle](03-server-lifecycle-and-protocol.md)

## Summary

Keeps the server's view of source consistent with what the user is editing and what's on disk. Open
documents are tracked in memory and re-analyzed as they change; files changed outside the editor are
detected so the index never serves stale results.

## User stories

### Story 1 — Track open documents (FR-33)
**As a** developer, **I want** edits I make to be reflected immediately **so that** navigation and
hover match what I see.

**Acceptance criteria:**
- [ ] On open, the document's current content becomes the source of truth for that file (over the
      on-disk copy).
- [ ] On change, the in-memory content updates and dependent analysis is refreshed (incremental
      re-analysis specified in [05](05-workspace-indexing-and-cache.md)).
- [ ] On close, the server reverts to the on-disk content for that file.
- [ ] Editor features queried against an open, unsaved document reflect the unsaved content.

### Story 2 — Detect external changes (FR-34)
**As a** developer who switches git branches or edits files outside the editor, **I want** the index
to stay correct **so that** I don't get stale results.

**Acceptance criteria:**
- [ ] Files added, modified, or removed on disk within the workspace (and within the indexed set) are
      detected.
- [ ] A detected change triggers re-analysis of the affected file(s) and dependents, keeping the
      index consistent with disk.
- [ ] Changes within excluded directories or to non-indexed types are ignored.
- [ ] A bulk on-disk change (e.g. a branch checkout touching many files) is handled without
      overwhelming the server or producing incorrect partial state.

## Out of scope
- The index data structure and invalidation strategy — see plan 05.
- Cache persistence across sessions — see plan 05.

## Open questions
- Whether change detection relies on editor-sent file events, a filesystem watcher, or both.
- Debounce/coalescing expectations for rapid successive changes.
