# Feature: Program-structure extraction

**Status:** Planned
**PRD requirements:** FR-23
**Priority / phase:** P0
**Depends on:** [02](../02-object-type-recognition/plan.md)

## Summary

Produces a structural model of each object — its root, subroutines, data sections, maps, and DDM
references — with source positions. This model is the backbone for the [document outline](../10-document-outline/plan.md),
[workspace symbol search](../09-navigation-and-symbol-search/plan.md), and [hover](../11-hover/plan.md).

## User stories

### Story 1 — Per-object structural model (FR-23)
**As a** developer, **I want** each object's structure identified **so that** symbols and outline have
something to show.

**Acceptance criteria:**
- [ ] For each object, the model identifies at minimum: the object root (e.g. program), its
      subroutines, its data sections, its maps, and its DDM references.
- [ ] Every structural symbol carries an accurate source position (range) for navigation.
- [ ] Symbol names are captured as written but matched case-insensitively.
- [ ] Nested structure (e.g. a subroutine within an object, sections within a data definition) is
      represented hierarchically.
- [ ] A fixture per symbol kind demonstrates correct extraction, including ranges.

### Story 2 — Robust to partial/legacy formatting
**As a** developer with legacy code, **I want** structure extracted even when some lines are unusual
**so that** outline still works.

**Acceptance criteria:**
- [ ] An object with some unrecognized lines still yields structure for the parts that are recognized.
- [ ] Unrecognized statement-like lines are surfaced as diagnostics (see [13](../13-diagnostics/plan.md)),
      not dropped, and do not prevent extraction of the rest of the structure.

## Out of scope
- Rendering symbols into LSP responses — see plans 09 and 10.
- Multi-line continuation handling specifics and column/fixed-format rules (implementation detail;
  see open question).

## Open questions
- The required symbol kinds beyond the minimum set for the first release.
- How much fixed-format/column-sensitive legacy syntax must be supported before structure extraction
  is considered complete.
