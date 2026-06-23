# Feature: Hover

**Status:** Planned
**PRD requirements:** FR-28
**Priority / phase:** P1
**Depends on:** [07](../07-call-dependency-resolution/plan.md),
[08](../08-data-access-extraction/plan.md), [09](../09-program-structure-extraction/plan.md)

## Summary

On-demand context at the cursor: program metadata, subroutine signatures, and DDM field details —
surfaced where the developer is looking, without navigating away.

## User stories

### Story 1 — Program metadata on hover (FR-28)
**As a** developer, **I want** hovering a module reference to show its metadata **so that** I get
context without opening it.

**Acceptance criteria:**
- [ ] Hovering a module/program reference shows at least: the module name, its location, and an
      inbound call count (how many sites call it).
- [ ] The inbound call count reflects the current index and updates with incremental re-analysis.
- [ ] Hovering an unresolved/dynamic target shows a sensible message rather than fabricated metadata.

### Story 2 — Subroutine signatures on hover (FR-28)
**As a** developer, **I want** a subroutine's signature on its PERFORM site **so that** I know its
parameters.

**Acceptance criteria:**
- [ ] Hovering a PERFORM target shows the subroutine's signature/parameter interface (from
      [07](../07-data-access-extraction/plan.md) parameter extraction).
- [ ] The hover reflects inline-before-external resolution (it describes the subroutine that would
      actually be performed).

### Story 3 — DDM field details on hover (FR-28)
**As a** developer, **I want** DDM field info on data-access statements **so that** I understand the
data being touched.

**Acceptance criteria:**
- [ ] Hovering a data-access statement shows the relevant DDM field name(s), type(s), and file
      association(s) available from the indexed source.
- [ ] When the underlying physical metadata isn't available (Adabas/IMS), hover shows what's known
      from source and does not fabricate the rest.

## Out of scope
- Resolving physical Adabas/IMS metadata beyond source — out of PRD scope.
- Navigation actions (handled by plan 09).

## Open questions
- Exact content and formatting of each hover card.
- Whether hover should include outbound dependency summaries in addition to inbound counts.
