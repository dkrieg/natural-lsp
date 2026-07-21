# Feature: Declaration & Type-Definition Navigation

**Status:** Planned
**PRD requirements:** FR-58 (declaration & type-definition navigation — new); refines FR-24 (definition)
**Priority / phase:** P2 (post-v1.0; thin providers over data other features already produce)
**Depends on:** [10](../10-navigation-and-symbol-search/plan.md) (definition provider + cursor mapping), [27](../27-variable-navigation/plan.md) (variable declarations + host-var binding), [28](../28-symbol-detail-and-view-binding/plan.md) (view field → DDM field / type)

## Summary

We advertise `definitionProvider` (feature 10) but not the two sibling navigation methods:
**`textDocument/declaration`** and **`textDocument/typeDefinition`**. Both are **thin providers over data
features 27/28 already compute**, so this feature is small and best done *after* them.

- **Declaration** — for most Natural constructs "declaration" and "definition" coincide (there is no
  header/impl split), so `declaration` largely mirrors `definition`. The one place it can differ: a
  **variable use** whose "declaration" is its `DEFINE DATA` line — exactly what feature 27 resolves.
  Advertising `declarationProvider` lets clients that bind a distinct "Go to Declaration" gesture reach it.
- **Type definition** — for a **data variable or view field**, "type" means its **DDM** (for a `VIEW OF`
  field or a DDM-typed variable) — jump from a field to the `.NSD` field/DDM that defines its type. This is
  precisely feature 28's view-field→DDM binding (and feature 27's DDM resolution) surfaced under the
  `typeDefinition` gesture. For a variable with only a scalar format (`A26`), there is no separate type
  object → empty (modeled gap, FR-17).

**Server-layer only — no `internal/model` change, no cache-format bump** (reuses features 10/27/28
resolution). Adds two capabilities (`declarationProvider`, `typeDefinitionProvider`); `TestInitialize`
gains two entries.

## User stories

### Story 1 — Go to declaration (FR-58, refines FR-24)
**As a** developer, **I want** "Go to Declaration" to reach a symbol's declaration.

**Acceptance criteria:**
- [ ] `textDocument/declaration` resolves a call/transfer/subroutine target like `definition` (same
      result), and a **variable use** to its `DEFINE DATA` declaration (reusing feature 27).
- [ ] Dynamic/unresolved/`*`-system → empty, no error (FR-17/FR-43). `declarationProvider` advertised;
      `TestInitialize` updated.

### Story 2 — Go to type definition (FR-58)
**As a** developer, **I want** "Go to Type Definition" on a field to reach the DDM that types it.

**Acceptance criteria:**
- [ ] `textDocument/typeDefinition` on a **`VIEW OF` field** or a DDM-typed variable navigates to the DDM
      field/`.NSD` that defines its type, through the same DDM namespace/steplib resolution as feature 28.
- [ ] A field with only a scalar format (no DDM type object) → empty, no error (FR-17). `typeDefinitionProvider`
      advertised; `TestInitialize` updated.

## Out of scope / deferred
- **`implementation`** (`textDocument/implementation`) — no clear Natural mapping outside NaturalX
  classes/interfaces (`.NS4`); deferred until class extraction exists.
- Any new extraction — this feature is pure provider wiring over features 10/27/28.

## Open questions
- **OQ-1 — declaration vs definition divergence.** Confirm the only intended divergence is the variable
  use → `DEFINE DATA` case; otherwise `declaration` == `definition`. If they'd always be identical for a
  given cursor, still advertise `declarationProvider` (some clients only bind the declaration gesture).
- **OQ-2 — sequencing.** This should land **after** features 27 (variable/DDM binding) and 28 (view→DDM),
  since it surfaces their data; if built earlier, ship only the definition-mirroring subset.

## Notes
- No `internal/model`/cache change; reuses features 10/27/28 resolution. Two new capabilities →
  `TestInitialize` extended. json/v2 marshaling (feature 19); store-first; encoding-aware. Fuzz the entry
  points (never panic — FR-43).
