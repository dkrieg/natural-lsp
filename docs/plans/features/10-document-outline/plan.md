# Feature: Document outline

**Status:** Planned
**PRD requirements:** FR-27
**Priority / phase:** P0
**Depends on:** [08](../08-program-structure-extraction/plan.md)

## Summary

Presents the structure of the currently open file as a navigable symbol tree, so developers can see
and jump around an object's parts. It renders the structural model from plan 08 into the editor's
outline/breadcrumb UI.

## User stories

### Story 1 — Full symbol tree for a file (FR-27)
**As a** developer, **I want** an outline of the open object **so that** I can see its parts and jump
to them.

**Acceptance criteria:**
- [ ] The outline shows, for the open file, its data sections, subroutines, maps, external calls, and
      other extracted structural symbols.
- [ ] The outline is hierarchical, reflecting nesting (e.g. sections under a data definition,
      subroutines under the object).
- [ ] Selecting an outline entry navigates to that symbol's source position.
- [ ] Each entry has an appropriate symbol kind so the editor renders correct icons/grouping.

### Story 2 — Outline stays current
**As a** developer editing a file, **I want** the outline to track my edits **so that** it stays
accurate.

**Acceptance criteria:**
- [ ] The outline reflects the current (possibly unsaved) content of the open document.
- [ ] An object with some unrecognized lines still produces an outline for the recognized parts.

## Out of scope
- Cross-file navigation and references — see plan 09.
- The structural extraction itself — see plan 08.

## Open questions
- Whether external calls should appear inline in the outline or be grouped under a dedicated node.
- Ordering: source order vs. grouped-by-kind.
