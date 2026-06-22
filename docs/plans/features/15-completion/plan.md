# Feature: Completion

**Status:** Planned
**PRD requirements:** FR-47
**Priority / phase:** P1
**Depends on:** [05](../05-workspace-indexing-and-cache/plan.md),
[06](../06-call-and-dependency-resolution/plan.md),
[07](../07-data-access-extraction/plan.md), [08](../08-program-structure-extraction/plan.md)

## Summary

Context-aware completions while typing: module names at call sites, subroutine names within scope,
and DDM field names at data-access statements — drawn from the live workspace index so the list is
always current.

## User stories

### Story 1 — Module-name completion at call sites (FR-47)
**As a** developer, **I want** completions for module names when typing a CALLNAT, PERFORM (external),
INCLUDE, or FETCH target **so that** I don't have to remember or type exact names.

**Acceptance criteria:**
- [ ] Typing a partial name after `CALLNAT`, `FETCH`, or `INCLUDE` triggers completions drawn from
      the workspace index (subprograms, external subroutines, copycodes, programs respectively).
- [ ] Completion candidates respect the steplib resolution order when a library map is configured:
      only names reachable from the current library are offered.
- [ ] When no library map is present, all matching names in the flat namespace are offered.
- [ ] Each candidate shows its object type (subprogram, subroutine, copycode, etc.) as the completion
      kind.
- [ ] Completions update after incremental re-analysis — a newly added module appears without a
      server restart.

### Story 2 — Subroutine-name completion at PERFORM sites (FR-47)
**As a** developer, **I want** completions for subroutine names when typing a PERFORM target **so
that** I see both in-scope inline subroutines and reachable external subroutines.

**Acceptance criteria:**
- [ ] Inline subroutines defined in the current object are offered first (consistent with
      inline-before-external resolution in FR-12).
- [ ] External subroutines reachable via steplib are offered after inline candidates.
- [ ] Dynamic PERFORM targets (variable operands) do not trigger module-name completions.

### Story 3 — DDM field-name completion at data-access statements (FR-47)
**As a** developer, **I want** completions for DDM field names when typing a field reference in a
READ/FIND/STORE/UPDATE/DELETE statement **so that** I can fill in exact field names without switching
to the DDM file.

**Acceptance criteria:**
- [ ] After a data-access verb with a file/DDM reference resolved, field-name completions are drawn
      from the indexed DDM source.
- [ ] Completions include the field type as detail text.
- [ ] When the DDM cannot be resolved (unindexed or unresolvable), no completions are offered and no
      error is surfaced to the user.

### Story 4 — No completions on unrecognised context (FR-47)
**As a** developer, **I want** completion to stay silent when the cursor is not in a meaningful
completion context **so that** the list doesn't pollute unrelated positions.

**Acceptance criteria:**
- [ ] Completion outside a call site, PERFORM, INCLUDE, or data-access position returns an empty
      list, not an error.
- [ ] The server advertises `textDocument/completion` capability with a trigger-character set (e.g.
      space/quote after a keyword) or uses resolve-on-request.

## Out of scope
- Keyword/statement completions (Natural language keywords) — language-server clients typically
  supply these via snippets; this feature covers symbol-name completions from the index.
- Signature display at the completion item — that is feature 16 (signature help).

## Open questions
- Trigger characters: should completions fire automatically on space after a keyword, or only on
  explicit invocation / a quote character?
- Whether to include `completionItem/resolve` for lazy detail fetching on large result sets.