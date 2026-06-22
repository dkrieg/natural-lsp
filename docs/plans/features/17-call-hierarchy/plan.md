# Feature: Call Hierarchy

**Status:** Planned
**PRD requirements:** FR-49
**Priority / phase:** P1
**Depends on:** [05](../05-workspace-indexing-and-cache/plan.md),
[06](../06-call-and-dependency-resolution/plan.md), [08](../08-program-structure-extraction/plan.md)

## Summary

Incoming and outgoing call panels for any program, subprogram, or subroutine — backed by the
cross-file call graph. Lets developers understand who calls a module and what it calls, without
manually tracing references.

## User stories

### Story 1 — Prepare call hierarchy item (FR-49)
**As a** developer, **I want** to invoke "call hierarchy" on a module or subroutine name **so that**
I get the call hierarchy panel anchored on that symbol.

**Acceptance criteria:**
- [ ] `textDocument/prepareCallHierarchy` at a program, subprogram, or subroutine name returns a
      `CallHierarchyItem` with the symbol's name, kind, file URI, and selection range.
- [ ] Invoking on a dynamic/unresolved target returns an empty result (not an error).
- [ ] Invoking at a position that does not resolve to a callable symbol returns an empty result.

### Story 2 — Incoming calls (FR-49)
**As a** developer, **I want** to see all callers of a module or subroutine **so that** I can assess
the impact of changing it.

**Acceptance criteria:**
- [ ] `callHierarchy/incomingCalls` for a resolved item returns all call sites across the workspace
      that statically call that module or subroutine.
- [ ] Each `CallHierarchyIncomingCall` carries the caller's `CallHierarchyItem` (name, kind, URI,
      range) and the list of `fromRanges` (the exact positions of the call sites in the caller).
- [ ] Dynamic call sites (unresolved) are not included as false incoming edges.
- [ ] Results reflect the current index and update after incremental re-analysis.

### Story 3 — Outgoing calls (FR-49)
**As a** developer, **I want** to see all calls made by a module **so that** I can understand its
dependencies at a glance.

**Acceptance criteria:**
- [ ] `callHierarchy/outgoingCalls` for a resolved item returns all static outgoing call
      relationships from that module: CALLNAT, PERFORM (external), FETCH, and program-transfer
      statements where the target is statically resolved.
- [ ] Each `CallHierarchyOutgoingCall` carries the callee's `CallHierarchyItem` and the
      `fromRanges` (call-site positions within the current module).
- [ ] Dynamic/unresolvable outgoing calls are not included as false outgoing edges.
- [ ] Inline PERFORM calls (resolved to a subroutine in the same object) are included as outgoing
      calls to that subroutine's item.

### Story 4 — Capability advertisement (FR-49)
**As a** client, **I want** the server to advertise call hierarchy support **so that** the editor
enables the call hierarchy UI.

**Acceptance criteria:**
- [ ] `ServerCapabilities.callHierarchyProvider` is `true` (or a `CallHierarchyOptions` object) in
      the initialize response.
- [ ] All three call-hierarchy methods (`prepare`, `incomingCalls`, `outgoingCalls`) are handled
      without error; unknown or out-of-range positions return an empty result.

## Out of scope
- Type hierarchy — not applicable to Natural.
- Displaying dynamic call chains — dynamic targets are excluded from the call graph (FR-11) and
  therefore absent from the hierarchy; their absence is a modeled gap, not a missing feature.

## Open questions
- Whether to include INCLUDE/copycode as an "outgoing call" relationship or treat it separately.
- Pagination: for modules with thousands of callers, whether to support `workDoneProgress` or
  partial results.