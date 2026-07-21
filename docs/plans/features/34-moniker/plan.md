# Feature: Moniker (`textDocument/moniker`) — Deferred / Not Recommended

**Status:** Planned (documented decision: **defer — do not build pre-v1.0**)
**PRD requirements:** FR-61 (cross-repository symbol monikers — new); NFR-11 (LSP conformance)
**Priority / phase:** P2 (lowest; a decision record, not scheduled work)
**Depends on:** [07](../07-call-dependency-resolution/plan.md) (symbol identity), [10](../10-navigation-and-symbol-search/plan.md) (symbols)

## Summary

`textDocument/moniker` emits **monikers** — stable, scheme-qualified identities for a symbol —
so tooling can correlate symbols **across repositories / independent indexes** (the LSIF/SCIP
interchange use case: "this symbol here is the same as that symbol in another project's index"). It exists
for large multi-repo code-intelligence pipelines, not for interactive single-workspace editing.

**Recommendation: do not build this.** This plan is a **decision record** capturing why, so the option is
documented rather than silently ignored:

1. **No use case for this product.** natural-lsp is **filesystem-scoped to one exported `.NSx` workspace**
   (PRD §4). It does not participate in a cross-repository index-interchange pipeline, and Natural objects
   resolve through the **steplib chain within the workspace**, not via global monikers. There is no second
   index for a moniker to correlate against.
2. **No client demand.** Interactive editors (VS Code, LSP4IJ, Neovim, Zed, Helix) do not use monikers for
   any user-facing gesture; they'd be emitted into a void.
3. **Cost without payoff.** A correct, *stable* moniker scheme (unique, versioned identities that survive
   refactors) is real design work for zero observed benefit here.

If natural-lsp ever feeds an **LSIF/SCIP exporter** for cross-repo code intelligence, revisit — that is the
only scenario that justifies it.

## User story (only if the above changes)

### Story 1 — Cross-index symbol monikers (FR-61) — *not scheduled*
**As a** code-intelligence pipeline, **I want** stable monikers for exported symbols **so that** symbols
correlate across independently-produced indexes.

**Acceptance criteria (deferred):**
- [ ] `textDocument/moniker` returns a `Moniker[]` for the symbol under the cursor with a stable
      `scheme`/`identifier` and appropriate `unique`/`kind`, for **exported** (workspace-visible) symbols.
- [ ] `monikerProvider` advertised; `TestInitialize` updated. (Only if a concrete consumer exists.)

## Out of scope
- Everything, currently — this is a deferral record. Building it requires a defined moniker scheme and an
  actual cross-index consumer (an LSIF/SCIP exporter), neither of which exists.

## Notes
- **No work scheduled.** Recorded so the capability is a **conscious non-goal for the foreseeable future**,
  not an oversight. Revisit only alongside an LSIF/SCIP export feature. If built, it is server-layer only
  (symbol identity from resolution) with no `internal/model`/cache change and a new `monikerProvider`
  capability.
