# Feature: Call & Dependency Extraction

**Status:** Planned  
**PRD requirements:** FR-10, FR-11, FR-12, FR-13, FR-14, FR-15, FR-17, FR-18; NFR-6; M-3, M-4, M-6  
**Priority / phase:** P0 (MVP)  
**Depends on:** [00-parser-foundation](../00-parser-foundation/plan.md)  

## Summary

Extract call and dependency relationships from Natural source using the parser. This feature produces unresolved references with caller context; cross-file binding happens later in the resolution feature.

Two ideas run through every story:

- **Extraction vs. resolution.** Per-file extraction produces references with caller context; cross-file resolution binds them to definitions using the library/steplib semantics.
- **Two kinds of gap, never silent.** A reference whose target can't be determined statically is a *modeled outcome* (a dynamic/unresolved relationship). A statement-like line the parser can't parse is a *tool limitation* and becomes a diagnostic. They are reported through different channels.

## User Stories

### Story 1 — CALLNAT extraction (FR-10, M-3)

**As a** developer, **I want** `CALLNAT` targets extracted **so that** I can navigate to called modules.

**Acceptance criteria:**

- [ ] A `CALLNAT` with a literal target name produces a static call relationship.
- [ ] The relationship records the call site (file + position) and the target name.
- [ ] A fixture suite covering common call patterns extracts all static calls correctly with zero false edges.

### Story 2 — Dynamic CALLNAT modeling (FR-11, M-6)

**As a** developer, **I want** variable-target calls surfaced rather than dropped **so that** dynamic dependencies remain visible.

**Acceptance criteria:**

- [ ] A `CALLNAT` whose target is a variable produces an explicit *dynamic/unresolved* relationship, not an error and not a silent omission.
- [ ] The calling context (caller object, call site, and the variable/expression used) is preserved on the relationship.
- [ ] Dynamic relationships are visibly distinct from resolved static ones.

### Story 3 — PERFORM extraction (FR-12, M-4)

**As a** developer, **I want** `PERFORM` targets extracted with scope awareness **so that** subroutine navigation works correctly.

**Acceptance criteria:**

- [ ] `PERFORM` targets are extracted with their caller context.
- [ ] Inline subroutines (`DEFINE SUBROUTINE`) in the same object are identified for later inline-before-external resolution.
- [ ] A fixture with both an inline and a same-named external subroutine marks both for resolution.
- [ ] Removing the inline definition makes it resolve externally (resolution feature).

### Story 4 — INCLUDE extraction (FR-13)

**As a** developer, **I want** `INCLUDE` targets tracked **so that** copycode dependencies are navigable.

**Acceptance criteria:**

- [ ] An `INCLUDE` statement produces a dependency relationship to the target copycode name.
- [ ] Copycode targets are treated as literal names (not variables); an `INCLUDE` is always a resolvable reference (subject to availability and the substitution case in Story 8).
- [ ] Changing a copycode file re-evaluates the files that `INCLUDE` it (incremental re-analysis).

### Story 5 — FETCH/RUN extraction (FR-14, FR-15)

**As a** developer, **I want** program-transfer statements extracted **so that** control-flow transfers are visible.

**Acceptance criteria:**

- [ ] A `FETCH` or `RUN` statement with a literal target produces a navigation relationship.
- [ ] A `FETCH` or `RUN` statement with a variable target produces a dynamic/unresolved relationship with caller context preserved.
- [ ] A statement that explicitly names a target library is marked for library-specific resolution.

### Story 6 — Dynamic call edge types (FR-18)

**As a** developer with language-dependent or generated call targets, **I want** literal names containing runtime-substitution placeholders handled correctly **so that** the analyzer doesn't invent edges to non-existent objects.

**Acceptance criteria:**

- [ ] A literal target containing a runtime-substitution placeholder is **not** resolved to a non-existent object formed by the raw literal text.
- [ ] Such a target is represented as a dynamic relationship that reflects its runtime-variable nature and preserves caller context.
- [ ] A fixture with a placeholder-bearing literal does not produce a false static relationship.

## Out of scope

- Data-access relationships (READ/STORE/etc.) — see plan 07.
- The on-disk index/cache mechanics that store these relationships — see plan 05.
- Resolving the *physical* metadata behind external files (Adabas/IMS) — out of scope per PRD.

## Open questions

- The relationship type for literal targets containing runtime-substitution placeholders (dynamic vs. resolved-with-wildcard).
- Whether user-defined function calls warrant a relationship type distinct from ordinary module calls.
- Constraints from target-name length limits — whether these should influence classification of a reference as static vs. dynamic.
