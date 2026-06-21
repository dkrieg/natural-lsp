# Feature: Code lens

**Status:** Planned
**PRD requirements:** FR-29
**Priority / phase:** P2
**Depends on:** [06](../06-call-and-dependency-resolution/plan.md),
[07](../07-data-access-extraction/plan.md)

## Summary

Inline, actionable summaries rendered above relevant constructs — e.g. how many sites call a module,
or a summary of the tables an object writes — giving at-a-glance insight without a separate query.

## User stories

### Story 1 — Call-count lens (FR-29)
**As a** developer, **I want** an inbound call count shown inline **so that** I can gauge a module's
importance at a glance.

**Acceptance criteria:**
- [ ] A code lens above a callable object shows its inbound call count from the current index.
- [ ] Activating the lens reveals or navigates to the calling sites (find-references behavior, plan
      09).
- [ ] The count updates with incremental re-analysis.

### Story 2 — Table-write summary lens (FR-29)
**As a** developer, **I want** a summary of what an object writes **so that** I can spot data
mutation quickly.

**Acceptance criteria:**
- [ ] A code lens summarizes the files/DDMs an object writes (from [07](../07-data-access-extraction/plan.md)).
- [ ] Activating the lens reveals or navigates to the write sites.

### Story 3 — Non-intrusive rendering
**As a** developer, **I want** lenses to be optional/cheap **so that** they don't clutter or slow the
editor.

**Acceptance criteria:**
- [ ] Code lenses can be disabled via configuration.
- [ ] Lens computation reuses the index and does not noticeably degrade editor responsiveness.

## Out of scope
- The underlying counts/relationships (provided by plans 06 and 07).

## Open questions
- The full set of lenses to ship (call counts and write summaries are explicit; others TBD).
- Whether lenses are on or off by default.
