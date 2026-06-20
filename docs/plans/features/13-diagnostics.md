# Feature: Diagnostics

**Status:** Planned
**PRD requirements:** FR-30, FR-31; supports FR-17; NFR-6, NFR-14; metric M-6
**Priority / phase:** P0 (unrecognized-syntax diagnostics) · P1 (ambiguous-resolution diagnostics)
**Depends on:** [06](06-call-and-dependency-resolution.md),
[08](08-program-structure-extraction.md)

## Summary

Makes the analyzer's limits legible. Because an unmatched regex is otherwise a silent no-op, the
analyzer must *deliberately* flag statement-like lines it failed to extract, and the resolver must
flag ambiguous names. Diagnostics are strictly the "tool limitation" channel — modeled gaps (dynamic
or unresolvable references) are **not** diagnostics (that distinction is owned by plan 06 / FR-17).

## User stories

### Story 1 — Surface unrecognized syntax (FR-30, NFR-6, M-6)
**As a** developer, **I want** to see where the analyzer couldn't parse a line **so that** I know
coverage gaps instead of getting silently wrong results.

**Acceptance criteria:**
- [ ] A statement-like line that matches no extraction pattern produces a diagnostic at that line.
- [ ] The diagnostic clearly communicates that this is a parser/coverage limitation (not a code
      error in the user's program).
- [ ] Blank lines, comments, and lines intentionally ignored do **not** produce diagnostics.
- [ ] In a representative corpus, every non-extracted statement-like line is accounted for as either a
      modeled relationship (plan 06) or a diagnostic — none silently dropped.

### Story 2 — Surface ambiguous resolution (FR-31)
**As a** maintainer without a library map, **I want** to be told when a name is ambiguous **so that**
I know resolution is uncertain.

**Acceptance criteria:**
- [ ] When operating without a library map and a name matches objects in more than one location, a
      diagnostic reports the ambiguity and identifies the candidates.
- [ ] Declaring a library map that disambiguates the name removes the diagnostic.
- [ ] The ambiguity diagnostic is distinct (severity/category) from unrecognized-syntax diagnostics.

### Story 3 — Diagnostics stay current
**As a** developer, **I want** diagnostics to track my edits and config **so that** they remain
trustworthy.

**Acceptance criteria:**
- [ ] Diagnostics for a file update on change (open-document and external-change paths).
- [ ] Resolving the underlying cause (fixing the line, adding a library map) clears the diagnostic.
- [ ] Diagnostics never include modeled outcomes (dynamic/unresolvable references).

## Out of scope
- The relationship modeling that distinguishes modeled gaps from limitations — see plan 06.
- Reporting of language correctness errors (the product is not a linter — PRD non-goal).

## Open questions
- What qualifies a line as "statement-like" (the trigger for an unrecognized-syntax diagnostic).
- Whether unrecognized-syntax diagnostics should be opt-in/severity-configurable to avoid noise on
  very legacy codebases.
