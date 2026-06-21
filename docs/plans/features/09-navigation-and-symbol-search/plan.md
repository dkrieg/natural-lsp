# Feature: Navigation & symbol search

**Status:** Planned
**PRD requirements:** FR-24, FR-25, FR-26
**Priority / phase:** P0
**Depends on:** [05](../05-workspace-indexing-and-cache/plan.md),
[06](../06-call-and-dependency-resolution/plan.md), [08](../08-program-structure-extraction/plan.md)

## Summary

The headline editor features: jump to a definition, find everything that references a symbol, and
search the whole workspace by name. These are the primary reason the product exists and define the MVP.

## User stories

### Story 1 — Go to definition (FR-24)
**As a** developer, **I want** to jump from a call/transfer/PERFORM site to its definition **so that**
I can navigate code quickly.

**Acceptance criteria:**
- [ ] Invoking "go to definition" on a module-call target navigates to the resolved definition.
- [ ] Invoking it on a transfer (FETCH/RUN) target and on a PERFORM target navigates to the resolved
      target, honoring inline-before-external and steplib resolution (plan 06).
- [ ] Invoking it on a dynamic/unresolved target yields no destination (and does not error); the
      user-facing result is "no definition found," consistent with it being a modeled gap.
- [ ] When a name resolves ambiguously with no library map, the behavior is defined (e.g. offer all
      candidates) and an ambiguity diagnostic is present (plan 13).

### Story 2 — Find all references (FR-25)
**As a** developer assessing impact, **I want** to find every reference to a symbol across the
workspace **so that** I know what a change affects.

**Acceptance criteria:**
- [ ] "Find references" on a subroutine, program, or DDM field returns all call/transfer/PERFORM/data
      sites across the workspace that reference it.
- [ ] Results include cross-file references, each with file + position.
- [ ] References found via dynamic/unresolved relationships are represented in a way that does not
      falsely claim a resolved link (or are clearly distinguished).
- [ ] Results are complete with respect to the index (no missing known references) — backed by a
      multi-file fixture.

### Story 3 — Workspace symbol search (FR-26)
**As a** developer, **I want** to search the workspace by program or subroutine name **so that** I can
find objects without knowing their file path.

**Acceptance criteria:**
- [ ] A workspace symbol query returns matching programs and subroutines by name.
- [ ] Matching is case-insensitive (consistent with Natural identifiers).
- [ ] Each result carries its location and symbol kind.
- [ ] Results are available as soon as indexing completes and reflect incremental updates.

## Out of scope
- The per-file outline view — see plan 10.
- Hover content shown at a symbol — see plan 11.

## Open questions
- Desired behavior of go-to-definition on ambiguous (no-library-map) names: first candidate vs. a
  picker.
- Whether references must include comment/string occurrences or only true references.
