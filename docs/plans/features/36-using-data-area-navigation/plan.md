# Feature: `DEFINE DATA … USING <data-area>` Navigation (bug #58)

**Status:** Planned
**PRD requirements:** FR-24 (definition), FR-25 (references) — closes a navigation gap; relates to FR-58
(declaration/type-definition, feature 31) and FR-59 (document links, feature 32).
**Priority / phase:** P1 bug fix — a shipped navigation gap ([issue #58](https://github.com/dkrieg/natural-lsp/issues/58)).
**Depends on:** [06](../06-call-dependency-extraction/plan.md) (edges), [07](../07-call-dependency-resolution/plan.md)
(resolution / steplib chain), [10](../10-navigation-and-symbol-search/plan.md) (definition/references),
[27](../27-variable-navigation/plan.md) (`DataAreaRef` + the data-area object resolver), [32](../32-document-links/plan.md)
(document links).

## Summary

Go-to-definition (and `declaration`, `typeDefinition`, `documentLink`) on the **data-area name token** of a
`DEFINE DATA {LOCAL|PARAMETER|GLOBAL} USING <name>` clause returns nothing — the editor reports "Cannot
find declaration to go to." The referenced data-area object (`.NSL`/`.NSA`/`.NSG`) exists, is indexed, and
already resolves through the steplib chain for field navigation (feature 27) and for every other module
reference kind (`INCLUDE`), so this is a **navigation gap specific to the `USING` name**, not a missing
file. (Issue #58.)

**Root cause (verified against the code):** the `USING` name is captured as a `model.DataAreaRef{Name,
SectionKind, Range}` (feature 27, persisted since cache `0.8.0`) but is **never wired as a cursor target**:
`extractEdges` emits no edge for a `USING` clause; `ExtractVariableRefs` skips everything inside a
`DEFINE DATA` block (`if inDataSection { continue }`); the server consumes `DataAreaRefs` only as a
*lookup source* for cross-file field resolution, never checking whether the cursor is *on* a
`DataAreaRef.Range`. So `findCursorTarget` returns `(nil, nil, nil)` and `provideDefinition` falls through
to `nil`.

**Fix (issue #58's recommended approach — reuses existing machinery, fixes the whole navigation family
uniformly):** treat a `USING` data-area reference as a first-class **module dependency edge**, exactly like
`INCLUDE`. Add a static edge kind `EdgeUsesDataArea`, emit it with `Source = DataAreaRef.Range` /
`TargetName = <name>`, and resolve it through the steplib chain to the data-area object. Because
`definition`/`references`/`documentLink` all operate over resolved edges, and a data-area object already
gets a root `Structure.SelectionRange` from feature 09's `extractStructure`, the fix lands go-to-definition
**and** find-references **and** document-links from a single edge addition; `declaration` inherits it by
delegating to `definition` (feature 31).

**Cache-format bump `0.9.0` → `0.10.0`** — the new `EdgeUsesDataArea` edges are persisted in
`FileAnalysis.Edges`, so a warm cache from a prior version would silently lack `USING` edges for unchanged
files until re-analyzed; the bump forces a one-time rebuild. `model.EdgeUsesDataArea` is an **additive**
`EdgeKind` constant (no struct-shape change). No new LSP capability (extends existing providers).

## User stories

### Story 1 — Go-to-definition / declaration on the `USING` name (FR-24, closes #58)
**As a** developer, **I want** "Go to Definition" on the data-area name in `DEFINE DATA LOCAL USING X` to
open the data-area object **so that** navigating a `USING` reference works like `INCLUDE`.

**Acceptance criteria:**
- [x] `textDocument/definition` with the cursor on the `USING` name of a `LOCAL`, `PARAMETER`, or `GLOBAL`
      section navigates to the resolved `.NSL` / `.NSA` / `.NSG` object's root `Structure.SelectionRange`,
      via the steplib chain (chain-aware, not `candidates[0]`).
- [x] `textDocument/declaration` (feature 31, delegates to definition) resolves the same.
- [x] Dynamic/unresolvable/out-of-chain data-area name → empty, no error (FR-17/FR-43).

### Story 2 — Find-references and document-link on the `USING` reference (FR-25, FR-59)
**As a** developer, **I want** find-references on a data-area object to list its `USING` sites, and a
clickable document link on the `USING` name.

**Acceptance criteria:**
- [x] `textDocument/references` on a data-area object (or from a `USING` site) includes every
      `USING` reference to it across the workspace (reverse edge sweep, dynamic/unresolved excluded).
- [x] `textDocument/documentLink` renders a link over each **resolved** `USING` name span → the data-area
      object file (reusing feature 32's resolved-edge link path; unresolved → no link, FR-17).

### Story 3 — Extraction + resolution of the `USING` edge (foundation)
**As a** maintainer, **I want** the `USING` clause extracted as a resolvable edge **so that** the providers
above work from one uniform path.

**Acceptance criteria:**
- [x] A new additive `model.EdgeUsesDataArea` kind; extraction emits one edge per `USING` data-area
      reference with `Source = DataAreaRef.Range` and `TargetName = <name>` (derived from the existing
      `DataAreaRef`s so the range matches feature 27's field resolution). Edges stay in global source order.
- [x] `workspace.Resolve` resolves `EdgeUsesDataArea` via the data-area object namespace
      (`.NSL`/`.NSA`/`.NSG`, reusing the feature-27 data-area steplib resolver), mirroring `INCLUDE`'s
      resolution outcome shape (`Resolved`/`Unresolved`/`Ambiguous`).
- [x] Cache-format bumped `0.9.0` → `0.10.0`; a prior cache rebuilds once (existing corrupt/old-cache tests
      updated). `FuzzParse`/extraction fuzz still never panic (FR-43).
- [x] Call hierarchy is unaffected — `EdgeUsesDataArea` is not a call kind, so it is excluded from
      prepare/incoming/outgoing (a data area is not callable). Verified by a test.

## Out of scope / deferred
- **`typeDefinition` on the `USING` name** — a data area is a module, not a *type* of the name, so
  `textDocument/typeDefinition` stays **empty** there (modeled gap, FR-17), consistent with feature 31's
  "no separate type object → empty". Definition/declaration are the correct gestures. (Recorded so the
  issue's mention of `typeDefinition` is a conscious decision, not an oversight.)
- **Navigating to a specific field via the `USING` name** — the name resolves to the *object*; field
  navigation is already covered by feature 27 (cursor on a variable that came from the data area).
- **`GLOBAL USING … WITH <block>` block-name nuances** — resolve the data-area object; the `WITH` block
  operand is not a separate navigable target here.

## Open questions
- **OQ-1 — where to emit the edge.** Derive `EdgeUsesDataArea` from the already-computed `DataAreaRef`s in
  `Analyze` (append to `Edges`, re-sort by `Source.Start`), vs. emit inside `extractEdges` by re-walking the
  AST's `DEFINE DATA … USING` clauses. Recommend **deriving from `DataAreaRef`s** — the `Name`+`Range` are
  already computed there, so it avoids duplicate AST-walking and guarantees the edge `Source` matches the
  range feature 27 uses for field resolution. Confirm during planning.
- **OQ-2 — resolved object type.** `resolveDataAreaCandidate` already tries `ObjectLocalDataArea` →
  `Parameter` → `Global` in order regardless of the section keyword. Keep that (first-reachable-wins across
  the three data-area namespaces), or narrow by `DataAreaRef.SectionKind` (LOCAL→`.NSL` only, etc.)?
  Recommend **keep the existing three-namespace order** (matches feature 27 field resolution and real
  Natural, where a `LOCAL USING` may reference an `.NSL`); narrowing risks missing a validly-reachable
  object. Confirm.

## Notes
- Additive `internal/model` change (`EdgeUsesDataArea` constant) + **cache-format bump `0.10.0`**. Resolution
  reuses the feature-27 data-area steplib resolver (export a `ResolveDataAreaPath` analog of `ResolveDDMPath`
  if the server/resolution needs it, keeping the Analyzer seam intact — providers stay on
  `internal/analysis.Analyzer` + `internal/workspace` public API). No new capability. Encoding-aware ranges
  (ADR-008), store-first, json/v2 marshaling — all inherited from the existing providers. Add a minimal
  reproducer fixture set (a caller with `LOCAL`/`PARAMETER`/`GLOBAL USING` + the three data-area objects)
  under the server testdata, mirroring the INCLUDE/CALLNAT navigation fixtures (per the testing convention).
