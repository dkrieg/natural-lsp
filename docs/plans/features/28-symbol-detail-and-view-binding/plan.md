# Feature: Rich Symbol Detail & `VIEW OF` Binding — Typed Record Layouts in the Outline

**Status:** Planned
**PRD requirements:** FR-55 (rich symbol detail & view binding — new; refines FR-23/FR-27); connects FR-19 (DDM read edges) and FR-28 (hover)
**Priority / phase:** P1 (v1.0 stable; turns the outline from a name skeleton into a typed record layout)
**Depends on:** [09](../09-program-structure-extraction/plan.md) (the `Symbol` tree), [11](../11-document-outline/plan.md) (`documentSymbol` provider), [12](../12-hover/plan.md) (the `.NSD` DDM parser `ddm.go`), and — for Phase B field→DDM resolution — [27](../27-variable-navigation/plan.md) (DDM-object steplib resolution + the intra-object field lookup). Coordinates its cache-format bump with feature 27 (see Notes).

## Summary

The document outline (`textDocument/documentSymbol`, feature 11) already renders the per-object
`Symbol` tree, but each `SymbolDataField` node is a **bare name** — the field's **type**, **level**,
**array dimensions**, and **REDEFINE** relationship are **extracted but dropped**: they live on
`model.DataDefinition` (`Type` = verbatim `A26`/`P7.2`, `Dimensions`, `Level`, and the AST's `Redefines`
target) and are persisted in the index, but `model.Symbol` carries only `{Kind, Name, Range,
SelectionRange, Children}`, and `document_symbols.go` never populates the LSP `DocumentSymbol.Detail`
field (which exists and is nil today). So an outline of a `DEFINE DATA` block shows a nameless skeleton
where it could show a typed record layout.

Separately, **`VIEW OF` is entirely unparsed.** A `1 EMP-VIEW VIEW OF EMPLOYEES` declaration — the
construct that turns a flat DDM record into the handful of logical fields a program actually uses — is
skipped by the parser (no AST node, no model field, no fixture). Because a view's fields are a
**selection of the DDM's fields whose format/length are inherited from the DDM when omitted**, a bare
`2 CUSTOMER-ID` under a view carries **no local type at all** — its layout only becomes legible once each
view field is joined to its DDM field definition. That join is the "decode a record layout into logical
fields" the outline should deliver.

This feature enriches the symbol export in **two phases**:

- **Phase A — field detail in the outline (light; metadata already extracted).** Carry the field
  metadata into `model.Symbol` and render it into `DocumentSymbol.Detail`: a compact, authentic string
  like `A26`, `P9,2 (1:5)`, `N2` for a redefine sub-field, or `REDEFINE CUSTOMER-ID` for a redefine
  block header — plus `FILLER nX` gaps. No new extraction (the data is in `DataDefinition`); the work is
  a model addition + a renderer. Also sharpens hover on a field.
- **Phase B — `VIEW OF` parsing + DDM binding (net-new).** Parse `level view-name VIEW [OF] ddm-name`,
  record the view→DDM binding, surface the view as an outline node showing its DDM (`VIEW OF EMPLOYEES`),
  and **resolve each view field to its DDM field** so a bare view field shows the DDM's type and
  go-to-definition on it jumps to the `.NSD` field declaration — reusing feature 27's DDM-object steplib
  resolution and its intra-object field lookup. A view-local `REDEFINE` sub-field resolves to the view's
  own REDEFINE line (same-file), not the DDM.

**Model/cache impact:** Phase A adds field metadata to `model.Symbol` (and Phase B adds a view→DDM
binding to `model.DataDefinition`/the view symbol) — both persisted (`Structure`/`Definitions`), so this
**bumps the cache-format version** (coordinate the exact version with feature 27's `0.8.0` bump by merge
order — see Notes). No new LSP capability or method (extends `documentSymbol`, and — Phase B —
`definition`/`hover`), so the locked `TestInitialize` allow-list is unchanged.

## Research findings (grounded; `.claude/knowledge/natural/data-definition.md`, code as-built)

**Metadata is extracted-but-dropped.** `model.DataDefinition{Name, Level, Type, Dimensions, SectionKind,
Range, Children}` fully captures type/level/array (`data.go`); `structure.go`'s `dataDefinitionToSymbol`
copies only `Name` + ranges into `model.Symbol`; `document_symbols.go` leaves `DocumentSymbol.Detail`
(a `*string`, present in `go.lsp.dev/protocol`) nil. **REDEFINE**: the AST `DataField` has a `Redefines
<target>` field and `data.go` merges redefine children into the target — but the `model.DataDefinition`
carries no redefine marker/target, so the redefine relationship is flattened away before the outline.

**`VIEW OF` is net-new** (zero parser/AST/model/fixture coverage today).

**Natural semantics (sourced):**
- `level view-name VIEW [OF] ddm-name` — `ddm-name` is always a **Natural DDM (`.NSD`)**; the DDM may be
  generated from **Adabas or from a DB2 table/view** (the "DB2 view" angle), but the analyzer's binding
  target is always the `.NSD` (never the DB2 catalog). View fields are a **selection** of DDM fields.
- **Format/length inherited from the DDM if omitted** (in structured mode a restated format must match the
  DDM) — so a bare view field's type comes from the DDM; prefer the DDM as source of truth, fall back to a
  restated view-line format if the DDM is unavailable.
- **Arrays** use index ranges, **not `OCCURS`**: `(1:10)`, multi-dim `(1:5,1:10)`, extensible `(1:*)`,
  single `(2)`. Compact render: `A10 (1:10)`.
- **Type notation** (verbatim, reuse `ddm.go`'s convention): `A26`, `N7.2`/`N7,2`, `P7.2`, `I2`/`I4`, `B`,
  `F4`/`F8`, `L`, `D`, `T`, `U`, `C`, `(A) DYNAMIC`.
- **REDEFINE** shows as a child of the redefined field; sub-fields are storage aliases (level > target;
  total ≤ target length); `FILLER nX` = `n` unnamed skipped bytes (render as an `nX` gap; don't warn on
  legal partial/overlapping coverage).
- **Navigation:** go-to-definition on a **view name** → its own `VIEW OF` line (same-file); on a **view
  field** → the **DDM field declaration in the `.NSD`** (same DDM namespace + steplib resolution as
  `READ`/`FIND` and SQL `FROM`-tables — feature 27); on a **view-local REDEFINE sub-field** → the view's
  own REDEFINE line (no DDM declaration). Limit: `TYPE: SQL` (DB2-backed) DDMs are **not parsed by
  `ddm.go` yet**, so view fields over an SQL DDM won't type-resolve until SQL-DDM parsing lands (recorded
  limit / possible `ddm.go` extension).

## User stories

### Phase A — typed outline

#### Story 1 — Field type & level in the outline (FR-55, refines FR-27)
**As a** developer reading a `DEFINE DATA` block, **I want** each field's type and level shown in the
outline **so that** I can read the record layout without opening the source.

**Acceptance criteria:**
- [ ] Each `SymbolDataField` in `documentSymbol` carries a `Detail` string rendering the field's **type**
      verbatim (`A26`, `P9,2`, `N8`, `I4`, `(A) DYNAMIC`, …); a group header (no format) shows no type.
- [ ] The rendering is authentic to Natural (reuses the verbatim `Type` convention; no invented spelling).
- [ ] Level is conveyed (via the existing tree nesting and/or the detail) so group structure is legible.
- [ ] `model.Symbol` gains the metadata needed to render this; the outline’s structure (node set, ranges)
      is otherwise unchanged (a pure enrichment). A fixture asserts the emitted `Detail` per field.

#### Story 2 — Arrays and REDEFINE are legible (FR-55, refines FR-27)
**As a** developer, **I want** array dimensions and REDEFINE overlays shown **so that** I understand
repeating fields and storage re-maps.

**Acceptance criteria:**
- [ ] An array field renders its index ranges compactly next to the type: `A10 (1:10)`, `P9,2 (1:5,1:10)`,
      `A20 (1:*)` (unbounded) — **not** the word "OCCURS".
- [ ] A REDEFINE block is shown as a child of the redefined field, labeled as a redefinition of its target
      (e.g. detail `REDEFINE CUSTOMER-ID`), with its sub-fields' types; a `FILLER nX` gap renders as an
      unnamed `nX` entry. No diagnostic on legal partial/overlapping coverage (FR-17).
- [ ] The `model.DataDefinition`/`Symbol` gains the minimal marker needed to distinguish a REDEFINE block
      from a normal group and to name its target. A fixture (group + REDEFINE + array + FILLER) pins output.

### Phase B — `VIEW OF` binding

#### Story 3 — `VIEW OF` is parsed and shown in the outline (FR-55, refines FR-23/FR-27)
**As a** developer, **I want** a `VIEW OF` declaration recognized and shown with the DDM it views **so
that** the outline reflects that the block is a database record view.

**Acceptance criteria:**
- [ ] The parser recognizes `level view-name VIEW [OF] ddm-name` (with `OF` optional) and its selected
      fields (including restated formats, arrays, and in-view REDEFINE), without breaking on the existing
      non-view `DEFINE DATA` paths.
- [ ] The view appears as an outline node (view name) whose detail names the DDM (`VIEW OF EMPLOYEES`); its
      selected fields are children (with Phase-A detail).
- [ ] The view→DDM binding is captured in the model (`DataDefinition.ViewOfDDM` or a view symbol kind),
      persisted; a malformed/partial view still yields structure for the recognized parts (FR-17/FR-43).

#### Story 4 — View fields decode to their DDM logical fields (FR-55; reuses feature 27)
**As a** developer, **I want** a bare view field to show its DDM type and navigate to the DDM field **so
that** a flat record decodes into typed logical fields.

**Acceptance criteria:**
- [ ] A view field with **no local format** shows the **type inherited from its DDM field** in the
      outline/hover (resolved by matching the field name against the DDM's `FileAnalysis.Definitions` from
      `ddm.go`); a restated format is shown as written (and must agree with the DDM in structured mode).
- [ ] Go-to-definition on a view field navigates to the **DDM field declaration in the `.NSD`**, resolved
      through the **same DDM namespace + steplib chain** as `READ`/`FIND` (reusing feature 27's DDM-object
      resolution + intra-object field lookup); go-to-definition on the **view name** lands on its `VIEW OF`
      line; a **view-local REDEFINE sub-field** resolves to the view's own REDEFINE line (same-file).
- [ ] Modeled gaps stay off the error channel (FR-17): a DDM outside the chain, a `TYPE: SQL` DDM not yet
      parsed by `ddm.go`, or a view field absent from the DDM → the field still lists (with its restated
      type if any), navigation yields empty, no error.

## Out of scope / deferred
- **`TYPE: SQL` (DB2-backed) DDM parsing** in `ddm.go` — until it lands, view fields over a DB2 DDM list
  but don't type-resolve/navigate to the DDM. A clean follow-up (extend `ddm.go`), flagged as a limit.
- **Byte-offset / physical-position computation** for REDEFINE overlays (showing each sub-field's exact
  byte range) — show the redefine structure and types; precise offsets are a later refinement.
- **Editing/rename** of fields; **hover on every field** as a full card (Phase A adds detail; a richer
  per-field hover card is optional/tracked separately).
- **Workspace-wide find-references of a DDM field via views** — belongs with feature 27's usage indexing.

## Open questions (resolve at `/plan-feature`)
- **OQ-1 — where does `Detail` rendering live?** Carry raw metadata (`Type`, `Dimensions`, a REDEFINE
  marker/target, `Level`) onto `model.Symbol` and render the `DocumentSymbol.Detail` **string in
  `internal/server`** (keeps presentation on the LSP side, consistent with hover cards) — recommended — vs.
  storing a pre-rendered `Detail string` on `Symbol` (one field, one bump, but nudges presentation into the
  analyzer). Confirm the field set added to `Symbol`.
- **OQ-2 — REDEFINE representation.** Add a `Redefines` target (+ marker) to `model.DataDefinition` (the AST
  already has it) and a distinct `SymbolKind` (e.g. `SymbolRedefine`) or reuse `SymbolDataField` with a
  detail label? Recommend surfacing the AST `Redefines` into the model and labeling via detail; a distinct
  kind only if the outline benefits.
- **OQ-3 — view symbol modeling.** Model a view as a `SymbolDataField`/section with a `ViewOfDDM` binding,
  or a new `SymbolKind` (`SymbolView`)? Recommend a `ViewOfDDM` binding on the existing field/section node
  to minimize churn; a `SymbolView` kind if the outline should visually distinguish views.
- **OQ-4 — cache-version coordination with feature 27.** Both features add persisted model fields. If 28
  merges after 27 (`0.8.0`), 28 → `0.9.0`; if the order flips, swap. Confirm the sequencing at planning so
  the bump is monotonic and each is a single one-time rebuild.
- **OQ-5 — DDM type inheritance source.** When a view field has no local format, always pull the type from
  the DDM (source of truth); if the DDM is unavailable/unresolved, fall back to the restated view-line
  format if present, else show no type. Confirm the fallback order.

## Notes
- **Model/cache-format change** (Phase A: `model.Symbol` field metadata; Phase B: `DataDefinition.ViewOfDDM`
  + REDEFINE marker) → a cache bump **coordinated with feature 27** (OQ-4). No new LSP capability/method —
  extends `documentSymbol` (both phases) and `definition`/`hover` (Phase B); json/v2 marshaling (feature
  19); providers stay store-first (features 10–13).
- **Reuses feature 27 Phase B** for view-field→DDM resolution (DDM-object steplib resolution + intra-object
  `name→DataDefinition` field lookup). If 28 is scheduled before 27's DDM work, Phase B depends on it —
  sequence 27 Phase B → 28 Phase B, or share the DDM-resolution helper.
- Testing (sanitized fixtures): Phase A — a `DEFINE DATA LOCAL` with typed scalars, a group, an array, and
  a REDEFINE-with-FILLER under `internal/analysis/natural/testdata/structure/`, asserting emitted `Detail`
  per node. Phase B — a `.NSP` view fixture (bare + restated + array + in-view REDEFINE) paired with the
  existing `customer.NSD` DDM fixture (from feature 12), proving a bare field resolves its DDM type and
  go-to-definition reaches the `.NSD` field; place under a new `testdata/view/` (or `dataaccess/`).
  Fuzz the `VIEW OF` parser path (never panic — FR-43).

## Results (as-built)

**Status: SHIPPED.** All ten task slices (T1–T10, with T8 split into T8a/T8b) landed; `just verify`
green (`-race` + integration). Review verdict **PASS** after one remediation round (seam/robustness PASS
first round; acceptance/extraction/LSP-protocol PASS on re-review). No new LSP capability — the locked
`TestInitialize` allow-list is byte-identical.

**Delivered**
- **Phase A — typed outline.** `model.Symbol` gained additive `Type`/`Level`/`Dimensions`/`Redefines`/
  `ViewOfDDM`; `DocumentSymbol.Detail` is composed **server-side** (OQ-1) in `symbolDetail`
  (`internal/server/document_symbols.go`), sharing the array-dimension formatter (`formatDimensions`) with
  hover's `renderParamType`. Detail renders verbatim type + index ranges (`A10 (1:10)`, `(1:*)`, never
  "OCCURS"), a redefine sub-field as `"<type> REDEFINE <target>"`, and a `FILLER nX` gap; a group header
  shows no type. REDEFINE is surfaced via additive `model.DataDefinition.Redefines` with a
  **flatten-with-stamp** merge (OQ-A) that now also handles a REDEFINE **nested inside a group**.
- **Phase B — `VIEW OF`.** Net-new parser branch for `level view-name VIEW [OF] ddm-name` (OQ: `OF`
  optional, `matchesLiteral`, no lexer/keyword change) with a **same-line guard** so a malformed view
  degrades to `ViewOfDDM == ""` without fabricating a binding or swallowing the block terminator
  (FR-17/FR-43). View→DDM binding persisted (`DataField`/`DataDefinition`/`Symbol.ViewOfDDM`), rendered as
  a `VIEW OF <ddm>` outline node. View-field **go-to-definition** and **DDM-inherited type** compose
  feature 27's steplib DDM-object resolution with new intra-object field resolvers
  `workspace.ResolveDDMFieldLocation`/`ResolveDDMFieldType`; cursor→declaration targeting is an additive
  companion `findDeclarationTarget` (OQ-B, use-site-first — `findCursorTarget` unchanged); inherited type
  is resolved **per request** from the index (OQ-C, F7-snapshot). `ddm.go` now populates DDM-field
  `NameRange` (inclusive-last-byte, ADR-008) so navigation lands on the field name.
- **Cache-format bump `0.8.0` → `0.9.0`** (T9) — persists the new `Symbol`/`DataDefinition` fields and the
  DDM-field `NameRange`; a stale `0.8.0` cache forces one cold rebuild.

**Recorded limits / deferrals (as planned)**
- **`TYPE: SQL` (DB2-backed) DDMs** are not parsed by `ddm.go`, so view fields over a DB2 DDM list in the
  outline but do not type-resolve or navigate to the DDM — a modeled gap (field lists, navigation/type
  empty, no error), not a defect. Clean follow-up: extend `ddm.go`.
- **Byte-offset / physical-position computation** for REDEFINE overlays (exact sub-field byte ranges) is a
  later refinement — the outline shows the redefine structure and types, not physical offsets.
- Workspace-wide **find-references of a DDM field via views** stays with feature 27's usage indexing.
