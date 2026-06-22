# Feature: Diagnostics

**Status:** Planned
**PRD requirements:** FR-30, FR-31; supports FR-17; NFR-6, NFR-14; metric M-6
**Priority / phase:** P0 (parse-error diagnostics) · P1 (ambiguous-resolution diagnostics)
**Depends on:** [06](../06-call-and-dependency-resolution/plan.md),
[08](../08-program-structure-extraction/plan.md)

## Summary

Makes the parser's limits legible. The parser surfaces source it cannot interpret as LSP diagnostics
rather than silently discarding it; the resolver flags ambiguous names. Diagnostics are strictly the
"source problem" channel — unresolvable references (dynamic calls, missing targets) are **not**
diagnostics; they are retained call-site records (that distinction is owned by plan 06 / FR-17).

## User stories

### Story 1 — Surface parse errors (FR-30, NFR-6, M-6)
**As a** developer, **I want** to see where the parser encountered source it could not interpret
**so that** I know about gaps rather than getting silently incomplete results.

**Acceptance criteria:**
- [ ] Source the parser cannot interpret produces a diagnostic at the relevant position.
- [ ] The diagnostic message is useful: it identifies the unexpected token or construct.
- [ ] Blank lines, comments, and intentionally-skipped content do **not** produce diagnostics.
- [ ] In a representative corpus, parse errors surface as diagnostics and unresolvable references
      are retained as call records — none are silently dropped.

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
