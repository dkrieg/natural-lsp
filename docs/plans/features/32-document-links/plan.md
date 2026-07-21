# Feature: Document Links (`textDocument/documentLink`)

**Status:** Planned
**PRD requirements:** FR-59 (document links — new); relates to FR-10/FR-12/FR-13 (call/include/navigation edges)
**Priority / phase:** P2 (post-v1.0; largely redundant with go-to-definition — value is discoverability)
**Depends on:** [06](../06-call-dependency-extraction/plan.md) (edges), [07](../07-call-dependency-resolution/plan.md) (resolution), [10](../10-navigation-and-symbol-search/plan.md) (location conversion)

## Summary

`textDocument/documentLink` renders spans as **clickable links** (a link decoration + a target the client
opens on click). For Natural, the module/copycode targets of **CALLNAT / INCLUDE / FETCH / RUN / PERFORM
(external)** are the natural candidates — clicking the target name opens the resolved object. We already
resolve those targets (features 06/07) and expose them via go-to-definition; `documentLink` is the
**always-visible** presentation of the same navigation (an underline + click, without placing the cursor
and invoking "Go to Definition").

**Honest value assessment:** this is **largely redundant with go-to-definition** — the incremental value
is discoverability (links are visibly clickable). Hence **P2** and gated on whether users ask for it. It is
cheap given resolution already exists.

**Server-layer only — no `internal/model` change, no cache-format bump.** Adds a `documentLinkProvider`;
`TestInitialize` gains one entry.

## User stories

### Story 1 — Clickable module/copycode targets (FR-59)
**As a** developer, **I want** CALLNAT/INCLUDE/FETCH/RUN targets shown as clickable links **so that** I can
open a referenced object without the go-to-definition gesture.

**Acceptance criteria:**
- [ ] `textDocument/documentLink` returns a link over each **resolved** module/copycode/subroutine target
      span in the document, whose target is the resolved object's URI (reusing feature 07 resolution →
      feature 10 location conversion).
- [ ] **Dynamic/unresolved** targets produce **no link** (FR-17) — a link that goes nowhere is worse than
      none; ambiguous flat-namespace targets either link to nothing or resolve lazily (see OQ-1).
- [ ] Links are encoding-aware and correct; missing/unreadable/out-of-root → empty, no error (FR-43).
- [ ] `documentLinkProvider` advertised (with `resolveProvider` false unless lazy resolution is used —
      OQ-1); `TestInitialize` updated.

## Out of scope / deferred
- **Linking DDM/view names or host vars** — go-to-definition (feature 27/28) covers these; add to links
  only if the discoverability win is requested.
- **`documentLink/resolve`** (lazy target computation) unless OQ-1 argues for it.

## Open questions
- **OQ-1 — eager vs lazy targets, and ambiguity.** Compute targets eagerly (simpler) or via
  `documentLink/resolve`? Recommend eager (targets are already resolved). For an ambiguous flat-namespace
  name, prefer **no link** over an arbitrary pick.
- **OQ-2 — worth building at all?** Given the overlap with go-to-definition, confirm demand before
  scheduling; this plan captures the design so it's ready if asked.

## Notes
- No `internal/model`/cache change. New capability (`documentLinkProvider`) → `TestInitialize` extended.
  json/v2 marshaling (feature 19); store-first; encoding-aware (`position.go`). Fuzz the builder (never
  panic — FR-43).
