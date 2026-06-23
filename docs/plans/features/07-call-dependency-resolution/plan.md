# Feature: Call & Dependency Resolution

**Status:** Planned  
**PRD requirements:** FR-10, FR-11, FR-12, FR-13, FR-14, FR-15, FR-16, FR-17, FR-18; FR-5; NFR-6,
NFR-7; M-3, M-4, M-6  
**Priority / phase:** P0 (static calls, dynamic modeling, subroutine scope, INCLUDE) · P1 (steplib
resolution, navigation statements) · P2 (runtime name substitution)  
**Depends on:** [06-call-dependency-extraction](../06-call-dependency-extraction/plan.md), [05-workspace-indexing-and-cache](../05-workspace-indexing-and-cache/plan.md)

## Summary

Resolve call and dependency relationships using the extracted references from feature 06 and the
workspace index from feature 05. This feature binds extracted references to their definitions using
the library/steplib semantics.

Two ideas run through every story:

- **Extraction vs. resolution.** Per-file extraction produces references with caller context;
  cross-file resolution binds them to definitions using the library/steplib semantics.
- **Two kinds of gap, never silent.** A reference whose target can't be determined statically is a
  *modeled outcome* (a dynamic/unresolved relationship). A statement-like line the analyzer can't
  parse is a *tool limitation* and becomes a [diagnostic](../13-diagnostics/plan.md). They are reported
  through different channels.

## User stories

### Story 1 — Resolve static module calls (FR-10, M-3)
**As a** developer, **I want** static module calls resolved to their definitions **so that** I can
navigate and find callers reliably.

**Acceptance criteria:**
- [ ] A module call with a literal target name produces a static call relationship from caller to the
      resolved definition.
- [ ] The relationship records the call site (file + position) and the resolved target object.
- [ ] In a fixture suite covering common call patterns, every static call resolves to the correct
      definition with **zero** false relationships.

### Story 2 — Model dynamic calls as unresolved (FR-11)
**As a** developer, **I want** variable-target calls surfaced rather than dropped **so that** dynamic
dependencies remain visible.

**Acceptance criteria:**
- [ ] A module call whose target is a variable produces an explicit *dynamic/unresolved* relationship,
      not an error and not a silent omission.
- [ ] The calling context (caller object, call site, and the variable/expression used) is preserved on
      the relationship.
- [ ] Dynamic relationships are visibly distinct from resolved static ones.

### Story 3 — Subroutine scope: inline before external (FR-12, M-4)
**As a** developer, **I want** PERFORM to resolve to a local subroutine before an external one **so
that** navigation matches Natural's runtime behavior.

**Acceptance criteria:**
- [ ] When a PERFORM target matches a subroutine defined inline in the same object, it resolves to the
      inline definition.
- [ ] Only when no inline definition exists does it resolve to an external subroutine of the same name
      (subject to steplib resolution, Story 6).
- [ ] A fixture with both an inline and a same-named external subroutine resolves to the inline one;
      removing the inline definition makes it resolve externally.

### Story 4 — INCLUDE / copycode dependencies (FR-13)
**As a** developer, **I want** INCLUDE targets tracked **so that** copycode dependencies are navigable
and drive incremental re-analysis.

**Acceptance criteria:**
- [ ] An INCLUDE statement produces a dependency relationship to the resolved copycode object.
- [ ] Copycode targets are treated as literal names (not variables); an INCLUDE is always a resolvable
      reference (subject to availability and the substitution case in Story 8).
- [ ] Changing a copycode file re-evaluates the files that INCLUDE it (links to
      [05](../05-workspace-indexing-and-cache/plan.md) incremental re-analysis).

### Story 5 — Navigation statements (FETCH / RUN) (FR-14, FR-15)
**As a** developer, **I want** program-transfer statements treated as navigable relationships,
distinct from module calls **so that** control-flow transfers are visible.

**Acceptance criteria:**
- [ ] A transfer statement with a literal target produces a navigation relationship distinct from a
      module-call relationship.
- [ ] A transfer statement with a variable target produces a dynamic/unresolved navigation
      relationship with caller context preserved (consistent with Story 2).
- [ ] A transfer statement that explicitly names a target library is resolved against that library,
      bypassing the normal steplib chain (Story 6).

### Story 6 — Steplib-chain resolution (FR-16, FR-5)
**As a** maintainer of a multi-library codebase, **I want** names resolved using the steplib chain
**so that** the same name in different libraries resolves the way Natural would.

**Acceptance criteria:**
- [ ] With a library map declared, a name resolves by searching: the current library first, then each
      declared steplib in order, then system libraries.
- [ ] Where the same module name exists in multiple libraries, the search order determines the winner
      (a fixture proves order changes the resolved target).
- [ ] A statement that explicitly targets a specific library resolves against that library directly,
      not via the chain.
- [ ] With **no** library map, the workspace is treated as a single flat namespace, and a name that
      matches objects in more than one location is reported as an ambiguous-resolution diagnostic
      (see [13](../13-diagnostics/plan.md)) rather than silently picking one.

### Story 7 — Distinguish unresolvable from unrecognized (FR-17, NFR-6, M-6)
**As a** user, **I want** "can't resolve this reference" and "can't parse this line" reported
differently **so that** I can tell a modeled gap from a tool limitation.

**Acceptance criteria:**
- [ ] An unresolvable reference (e.g. a dynamic target, or a literal target with no matching object)
      is recorded as a relationship/outcome, not a diagnostic.
- [ ] A statement-like line the analyzer matches no pattern for is recorded as a diagnostic, not a
      relationship.
- [ ] In a representative corpus, every non-extracted statement-like line is accounted for as exactly
      one of those two outcomes — none are silently dropped.

### Story 8 — Runtime name substitution in literal targets (FR-18)
**As a** developer with language-dependent or generated call targets, **I want** literal names
containing runtime-substitution placeholders handled correctly **so that** the analyzer doesn't invent
edges to non-existent objects.

**Acceptance criteria:**
- [ ] A literal target containing a runtime-substitution placeholder is **not** resolved to a
      non-existent object formed by the raw literal text.
- [ ] Such a target is represented in a way that reflects its runtime-variable nature (its exact
      relationship type is an open question, below) and preserves caller context.
- [ ] A fixture with a placeholder-bearing literal does not produce a false static relationship.

## Out of scope
- Data-access relationships (READ/STORE/etc.) — see plan 08.
- The on-disk index/cache mechanics that store these relationships — see plan 05.
- Resolving the *physical* metadata behind external files (Adabas/IMS) — out of scope per PRD.

## Open questions
- The relationship type for literal targets containing runtime-substitution placeholders
  (dynamic vs. resolved-with-wildcard) — affects many menu/language-dependent calls.
- Whether user-defined function calls warrant a relationship type distinct from module calls.
- Constraints from target-name length limits (e.g. variable vs. literal name length) — whether these
  should influence classification of a reference as static vs. dynamic.
