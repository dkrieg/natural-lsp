# Feature 28 — Rich Symbol Detail & `VIEW OF` Binding — Task Plan

**Spec:** `docs/plans/features/28-symbol-detail-and-view-binding/plan.md`
**PRD requirements:** FR-55 (new; refines FR-23/FR-27); connects FR-19 (DDM read edges), FR-28 (hover). FR-17/FR-43 (modeled gaps / never-crash) apply throughout.
**Depends on (all shipped):** 09 (`Symbol` tree), 11 (`documentSymbol` provider), 12 (`.NSD` `ddm.go` parser), 27 (DDM-object steplib resolution + intra-object field lookup — `workspace.ResolveDDMPath`, `workspace.ResolveDataAreaFieldLocation`/`findFieldInDefinitions`, cache `0.8.0`).

---

## Open-question resolutions (confirmed at planning)

- **OQ-1 (Detail rendering location):** **Carry raw metadata onto `model.Symbol`; render the `DocumentSymbol.Detail` string in `internal/server`.** New `model.Symbol` fields (all additive, persisted with `Structure`): `Type string`, `Dimensions []ArrayDimension`, `Level int`, plus the Phase-B `ViewOfDDM string`, and the REDEFINE marker from OQ-2. The compact detail string (`A26`, `P9,2 (1:5)`, `REDEFINE CUSTOMER-ID`, `VIEW OF EMPLOYEES`, `nX` filler) is composed **in `internal/server/document_symbols.go`** (and reused by hover), keeping presentation on the LSP side of the Analyzer seam — consistent with the hover cards. Rationale: matches the CLAUDE.md rule that LSP-facing hover-card presentation lives in `internal/server`, not the analyzer.
- **OQ-2 (REDEFINE representation):** Surface the AST `DataField.Redefines` target into `model.DataDefinition` (new `Redefines string` field) and onto `model.Symbol` (`Redefines string`), and **label via detail** (`REDEFINE <target>`). **No new `SymbolKind`** — the outline distinguishes a redefine block purely by its detail label; a distinct kind is not needed to render the tree correctly (revisit only if a client needs to filter). A `FILLER` sub-field is a normal `SymbolDataField` whose detail renders `nX` (no name emphasis).
- **OQ-3 (view symbol modeling):** Model a view via a **`ViewOfDDM string` binding on the existing node** (the view is a `SymbolDataField`/`DataDefinition` with `ViewOfDDM` set), **no new `SymbolKind`**. Minimizes churn; the outline distinguishes a view by its `VIEW OF <ddm>` detail. (If a future client needs a visually distinct view glyph, add `SymbolView` then — not now.)
- **OQ-4 (cache-version coordination):** Feature 27 shipped first at **`0.8.0`**. Feature 28 bumps **`0.8.0` → `0.9.0`** (a single one-time rebuild). Persisted additions: `Symbol.{Type,Dimensions,Level,Redefines,ViewOfDDM}` (via `Structure`) and `DataDefinition.{Redefines,ViewOfDDM}` (via `Definitions`). See T9 (DoD item).
- **OQ-5 (DDM type-inheritance source / fallback order):** For a **view field with no local format**: (1) pull the type from the resolved DDM field (source of truth); (2) if the DDM is unavailable/unresolved/absent-field, fall back to the **restated view-line format** if the view field carries one; (3) else show **no type**. Type inheritance for the **outline `Detail`** is resolved server-side (the analyzer has no cross-file index); the analyzer only records the view field's own restated format + the `ViewOfDDM` binding.

---

## Current-state findings & impact (code as-built, ground truth)

- **Metadata is extracted but dropped before the outline.** `model.DataDefinition{Name, Level, Type, Dimensions, SectionKind, Range, NameRange, Children}` fully captures type/level/array (`internal/analysis/natural/data.go`, `fieldToDefinition`). But `internal/analysis/natural/structure.go` `dataDefinitionToSymbol` copies **only** `Name` + ranges into `model.Symbol`; `internal/model/model.go` `Symbol` carries only `{Kind, Name, Range, SelectionRange, Children}`; `internal/server/document_symbols.go` `symbolToDocumentSymbol` never sets `DocumentSymbol.Detail` (a `*string`, present in `go.lsp.dev/protocol`, nil today). **Impact:** Phase A is a model-field addition + a renderer; no new extraction.
- **REDEFINE is flattened before the model.** The AST `DataField` has `Redefines string` (`ast.go:107`) and the parser populates it (`parser.go:344`). But `data.go` `extractDefinitions` **merges** a redefine block's children into the target field's `Children` and drops the block header (`data.go:262-272`), and `fieldToDefinition` never copies `Redefines` onto `model.DataDefinition` (no such field exists). **Impact:** to render `REDEFINE <target>` in the outline, T3 must add `DataDefinition.Redefines`, populate it in `fieldToDefinition`, and change the merge so the redefine relationship survives (see T3 for the exact approach — keep the block's subfields as marked children rather than silently flattening).
- **`VIEW OF` is entirely net-new.** No AST node, no parser branch, no model field, no fixture. `parseDataField` (`parser.go:243`) parses `<level> [REDEFINE target] <name> (<type>)`; there is **no** recognition of `<level> <name> VIEW [OF] <ddm>`. `VIEW`/`OF` are not treated specially anywhere. **Impact:** T5 is new parser/AST work; it must not disturb the existing non-view field paths (regression fixtures: `01-program-full.NSP`, `06-two-local-sections.NSP`).
- **DDM field resolution already exists and is reusable (feature 27).** `workspace.ResolveDDMPath(ddmName, idx, referencingPath, cfg)` (`internal/workspace/field_resolution.go:95`) resolves a DDM name → its `.NSD` path via the non-transitive steplib chain (unreachable-exclusion). `findFieldInDefinitions(name, defs)` (`field_resolution.go:134`) does the intra-object name→`DataDefinition` lookup (case-insensitive, group-qualified, REDEFINE-recursive) and returns the field's `NameRange`. `internal/server/definition.go` `provideDDMDefinition` (line 624) already resolves a DDM data-access entry to its `.NSD` object root. **Impact:** Phase B view-field navigation composes these; the DDM-object resolution + intra-object lookup are done — T8 wires a view-field cursor through them.
- **`ddm.go` populates DDM `FileAnalysis.Definitions` with verbatim `Type`.** `extractDDMDefinitions` (`internal/analysis/natural/ddm.go:55`) emits `model.DataDefinition{Name, Level, Type, Dimensions}` for `.NSD` fields and **skips `TYPE: SQL` DDMs** (`isSQLTypeLine`, line 124) → an SQL DDM has empty `Definitions`. **Impact:** OQ-5 type inheritance and view-field navigation both read `ddm.go`'s `Definitions`; a `TYPE: SQL` DDM yields no fields → modeled gap (T8 AC covers it — lists, no navigation, no error). Recorded limit (plan "Out of scope").
- **Renderer building blocks exist.** `internal/server/hover.go` `renderParamType(def)` (line 153) already renders `TYPE (lower:upper,...)` with `*` for unbounded — the Phase-A detail renderer should reuse/extend this exact convention so hover and the outline agree.
- **Cursor targeting is by reference site, not declaration.** `internal/server/cursor.go` `findCursorTarget` maps a cursor to an `EdgeEntry`/`DataAccessEntry`/`VariableRef`, NOT to a `Symbol`/`DataDefinition` declaration. A **view field is a declaration inside `DEFINE DATA`**, not a use-site. **Impact:** T8 must decide the cursor entry point for a view field. Feature 27 already resolves *variable use-sites* → declarations; a view field is itself a declaration line. See T8 for the chosen entry point (via the view-field declaration span, reusing the feature-27 field-resolution helpers), and OQ note there.
- **Analyzer seam intact.** `documentSymbol` reads `model.Structure`; navigation goes through `workspace.*` resolvers and `model` — `internal/server` imports no parser internals. Every task below preserves this: parser/AST/`Detail`-metadata population lives in `internal/analysis/natural`, `Detail`-string composition + navigation live in `internal/server`, cross-file resolution in `internal/workspace`.
- **No new LSP capability/method.** Extends `documentSymbol` (both phases), `hover` (Phase A field detail), and `definition` (Phase B view fields). The locked `TestInitialize` allow-list is **unchanged** — add a DoD assertion that it stays byte-identical.
- **Cache format is `0.8.0`** (`internal/workspace/cache.go:22`). This feature bumps to `0.9.0` (T9).

**Reconciliation of acceptance criteria:**
- Story 1 AC1–4, Story 2 AC1–3: **new** — model addition + renderer (Phase A, T1–T4).
- Story 3 AC1–3: **new** — parser/AST/model + outline node (Phase B, T5–T7).
- Story 4 AC1 (DDM-inherited type in outline/hover): **new**, composes `ddm.go` `Definitions` + resolution (T8).
- Story 4 AC2 (go-to-definition on view field → `.NSD`; view name → own line; view-local REDEFINE → own line): **extends existing** `provideDDMDefinition` + feature-27 field lookup (T8).
- Story 4 AC3 (modeled gaps: DDM outside chain / `TYPE: SQL` / field absent → list, no nav, no error): **new coverage** over existing FR-17 pathways (T8).

No shared-contract change breaks existing consumers: all `model` additions are additive fields (default zero value ⇒ existing renderers behave as today). The one behavioral change is T3's REDEFINE-merge adjustment — T3 must keep the existing outline node set intact for non-redefine data (regression assertion required).

---

## Phase A — Typed outline (field detail, arrays, REDEFINE)

### T1 — Add field metadata to `model.Symbol` (contract prep)

**Type:** model addition (foundation for T2–T8). No behavior change on its own.
**Files:** `internal/model/model.go`.
**What:** Add additive fields to `model.Symbol`: `Type string`, `Level int`, `Dimensions []ArrayDimension`, `Redefines string`, `ViewOfDDM string` (the last two used in T3/T7 but declared here so the struct changes once). Document each (verbatim type; nesting level; array bounds; REDEFINE target name, `""` when not a redefine; DDM name for a `VIEW OF` node, `""` otherwise).
**Test-first (RED):** a `model`-package (or `structure`) test asserting the zero value of a `Symbol` leaves all new fields empty (default-safe), and that a constructed `Symbol` round-trips the new fields — establishes the fields exist and are additive.
**Modeled-gap coverage:** none (pure struct).
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] New fields present, documented, additive (zero value = current behavior).
- [ ] `just verify` green; no existing test changes required (additive).

### T2 — Carry type/level/array into the outline; render `DocumentSymbol.Detail` (Story 1)

**Type:** extend `structure.go` (populate) + `document_symbols.go` (render).
**Files:** `internal/analysis/natural/structure.go` (`dataDefinitionToSymbol`); `internal/server/document_symbols.go` (`symbolToDocumentSymbol` + a new pure `symbolDetail(sym)` renderer); reuse the `renderParamType` convention from `internal/server/hover.go` (extract a shared helper if cleaner).
**Fixture:** new `internal/analysis/natural/testdata/structure/07-typed-fields.NSP` — a `DEFINE DATA LOCAL` with typed scalars (`A26`, `N8`, `P9,2`, `I4`, `(A) DYNAMIC`), a **group header** (level-1 group with level-2 children, no format), and a nested group. (Arrays + REDEFINE deferred to T4's fixture to keep this slice thin.)
**Expected result (exact):**
- `dataDefinitionToSymbol` copies `def.Type`, `def.Level`, `def.Dimensions` onto the emitted `model.Symbol`.
- `symbolToDocumentSymbol` sets `DocumentSymbol.Detail` to a `*string`:
  - scalar leaf → `"A26"`, `"N8"`, `"P9,2"`, `"I4"`, `"(A) DYNAMIC"` (verbatim `Type`, no invented spelling).
  - group header (`Type == ""`, has children) → **no detail** (nil `Detail`), so group structure is conveyed by nesting alone (AC: "a group header shows no type").
  - node set, ranges, `SelectionRange`, and hierarchy are otherwise **byte-identical** to today (pure enrichment).
- Level is legible via tree nesting (existing) and available on `Symbol.Level` for callers.
**Modeled-gap coverage:** a field with empty `Type` (group) → nil `Detail`, never a fabricated type (FR-17).
**Analyzer-seam note:** the `Detail` *string* is composed in `internal/server`; `structure.go` only carries raw metadata (OQ-1).
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] Fixture `07-typed-fields.NSP` added; a test asserts the emitted `Detail` per node (including nil for group headers).
- [ ] A `document_symbols` test asserts the outline node set/ranges for the fixture are unchanged vs. a name-only baseline (pure-enrichment invariant).
- [ ] Hover on a field still renders (or renders sharper) — assert no regression in an existing hover test.
- [ ] `just verify` green.

### T3 — Surface the REDEFINE relationship into the model (Story 2, contract change)

**Type:** shared-contract change in `data.go` extraction (with migration of the one consumer, `structure.go`).
**Files:** `internal/model/model.go` (`DataDefinition.Redefines string` — additive); `internal/analysis/natural/data.go` (`fieldToDefinition` + `extractDefinitions` merge logic); `internal/analysis/natural/structure.go` (`dataDefinitionToSymbol` carries `Redefines`).
**Problem being fixed:** today `extractDefinitions` (`data.go:262-272`) merges a redefine block's children into the target's `Children` and drops the block header, so the redefine relationship is flattened away before the outline.
**Approach (decision to encode):** keep flattening the subfields into the target's `Children` **but stamp `Redefines = <target>` on each merged subfield** (and set `Level` from the AST) so the outline can label them; OR (preferred if it reads cleaner) keep a single synthetic child node carrying `Redefines` whose own children are the subfields. **Choose whichever preserves the existing non-redefine outline node set unchanged** and keeps hover's parameter interface (which walks `Definitions.Children`) working. Whichever is chosen, `DataDefinition.Redefines` and the AST `DataField.Redefines` must agree.
**Fixture:** `internal/analysis/natural/testdata/structure/08-redefine.NSP` — a scalar (e.g. `1 #CUSTOMER-ID (A10)`), a `REDEFINE #CUSTOMER-ID` block with typed sub-fields and a `FILLER 3X` gap.
**Expected result (exact):**
- Each redefine sub-field's `DataDefinition.Redefines` == the target name (`"#CUSTOMER-ID"`, normalized as the AST carries it); non-redefine fields have `Redefines == ""`.
- A `FILLER` sub-field is represented as a normal `DataDefinition` (name `FILLER`/empty per parser, `Type`/count for `nX`).
- No `Diagnostic` emitted for legal partial/overlapping coverage (FR-17) — assert `Diagnostics` empty for the fixture.
**Migration:** the only in-tree consumer of `extractDefinitions` output shape for redefine children is `structure.go`/hover — both must still pass their existing tests (run them; adjust only if the chosen approach changes the child node set, and if so, update the assertions with a note).
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] `DataDefinition.Redefines` added, populated; AST/model agree.
- [ ] Fixture `08-redefine.NSP`; test asserts `Redefines` per sub-field + empty `Diagnostics`.
- [ ] Existing `data.go`/`structure.go`/hover tests pass unchanged (or changed-with-note per migration).
- [ ] `just verify` green.

### T4 — Render arrays + REDEFINE label + FILLER in `Detail` (Story 2)

**Type:** extend the `internal/server` detail renderer (builds on T2 + T3).
**Files:** `internal/server/document_symbols.go` (`symbolDetail`); reuse `renderParamType`'s dimension formatting.
**Fixture:** reuse T3's `08-redefine.NSP` plus a small array/multi-dim/unbounded fixture — either extend `07-typed-fields.NSP` or add `09-arrays.NSP` (fields `A10 (1:10)`, `P9,2 (1:5,1:10)`, `A20 (1:*)`).
**Expected result (exact) — emitted `DocumentSymbol.Detail`:**
- array field → `"A10 (1:10)"`, `"P9,2 (1:5,1:10)"`, `"A20 (1:*)"` (index ranges, **never** the word `OCCURS`).
- redefine sub-field (typed) → `"N2"` etc. (its own type).
- redefine block node → `"REDEFINE #CUSTOMER-ID"` (labels the target from `Symbol.Redefines`).
- `FILLER nX` gap → detail `"nX"` (e.g. `"3X"`), rendered as an unnamed/`FILLER` entry; no emphasis.
**Modeled-gap coverage:** legal partial/overlapping REDEFINE coverage → no diagnostic, renders as-is (FR-17); unbounded upper → `*` not a fabricated bound.
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] Fixture(s) pin every `Detail` variant above (array, multi-dim, unbounded, redefine block label, filler gap).
- [ ] No `OCCURS` string anywhere in output (assert absence).
- [ ] `just verify` green.

---

## Phase B — `VIEW OF` parsing + DDM binding + navigation

> Sequence strictly after Phase A: Phase B's outline nodes reuse the T2/T4 `Detail` renderer, and the view→DDM binding rides the same `model.Symbol`/`DataDefinition` fields added in T1/T3.

### T5 — Parse `level view-name VIEW [OF] ddm-name` (Story 3, parser/AST — net-new)

**Type:** new parser branch + AST field + model field.
**Files:** `internal/analysis/natural/ast.go` (add `DataField.ViewOfDDM string` + optional `ViewDDMRange model.Range`); `internal/analysis/natural/parser.go` (`parseDataField`: after the name is parsed, detect a `VIEW [OF] <ddm-name>` clause before the type-spec branch); `internal/analysis/natural/data.go` (`fieldToDefinition` copies `ViewOfDDM` onto `model.DataDefinition`; add `model.DataDefinition.ViewOfDDM string`).
**Grammar to recognize:** `<level> <view-name> VIEW [OF] <ddm-name>` followed by the view's selected fields on subsequent higher-level lines (parsed by the **existing** `parseDataFields` level-nesting loop — a view's fields are ordinary `<level> <name> [(<type>)]` lines, so no new field-body grammar is needed). `OF` is optional. `VIEW`/`OF` may be `TokenKeyword` or `TokenIdentifier` — use `matchesLiteral` (as `parseMap`/`parseSubroutine` do for `END-*`) so **no lexer/keyword change** is required.
**Fixture:** `internal/analysis/natural/testdata/parser/28-view-of.NSP` (or a `parser/` slot) — a `DEFINE DATA LOCAL` with `1 EMP-VIEW VIEW OF EMPLOYEES`, bare selected fields (`2 PERSONNEL-ID`, `2 NAME`), a restated-format field (`2 SALARY (P9,2)`), an array field, and an in-view `REDEFINE`. Include a `VIEW` (no `OF`) variant to prove `OF` optional.
**Expected result (exact):**
- The view field parses to a `DataField` with `Name == "EMP-VIEW"`, `ViewOfDDM == "EMPLOYEES"`, its selected fields as `Children` (via level nesting).
- `OF` present and absent both parse identically.
- Existing non-view `DEFINE DATA` fixtures (`01-program-full.NSP`, `06-two-local-sections.NSP`, and all `parser/20`–`27`) parse **unchanged** (regression).
- A malformed/partial view (`VIEW` with no ddm-name) still yields the recognized parts and never panics (FR-43); parser may record its usual ranged diagnostic but Phase B never invents structure.
**Modeled-gap coverage:** missing ddm-name → `ViewOfDDM == ""`, field still emitted (FR-17/FR-43).
**Fuzz:** extend/confirm `FuzzParse` covers the `VIEW OF` path (never panics — FR-43). Add a targeted `VIEW`-prefixed seed.
**Analyzer-seam note:** all parser/AST/model work stays in `internal/analysis/natural`.
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] `DataField.ViewOfDDM` + `DataDefinition.ViewOfDDM` added and populated.
- [ ] Parser fixture asserts view name, DDM binding, `OF`-optional, selected-field children.
- [ ] Regression: at least two existing non-view DEFINE DATA fixtures assert unchanged parse output.
- [ ] Fuzz seed for `VIEW` added; `FuzzParse` still never panics.
- [ ] `just verify` green.

### T6 — Carry `ViewOfDDM` into the outline node (Story 3)

**Type:** extend `structure.go` (populate) + `document_symbols.go` (render detail).
**Files:** `internal/analysis/natural/structure.go` (`dataDefinitionToSymbol` copies `ViewOfDDM` onto `model.Symbol.ViewOfDDM`); `internal/server/document_symbols.go` (`symbolDetail` renders `"VIEW OF EMPLOYEES"` when `ViewOfDDM != ""`).
**Fixture:** reuse T5's `28-view-of.NSP` via the structure test harness (or a copy under `internal/analysis/natural/testdata/structure/10-view.NSP`).
**Expected result (exact):**
- The view appears as a `SymbolDataField` node (per OQ-3, no new kind) named `EMP-VIEW`, with `DocumentSymbol.Detail == "VIEW OF EMPLOYEES"`.
- Its selected fields are children carrying Phase-A detail: a restated-format field shows its written type (`"P9,2"`); a bare field shows **no local type yet** (T8 resolves the inherited DDM type — here, before T8, bare view fields have nil `Detail`).
- A malformed/partial view still yields a node for the recognized parts (FR-17/FR-43).
**Modeled-gap coverage:** `ViewOfDDM == ""` → detail falls back to normal field rendering (not a fabricated `VIEW OF`).
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] Structure/outline test asserts the view node detail (`VIEW OF EMPLOYEES`) and its children's Phase-A detail.
- [ ] `just verify` green.

### T7 — (Rolled into T5/T6) view→DDM binding persisted in the model

**Note:** Story 3 AC3 ("view→DDM binding captured in the model, persisted") is satisfied by T5 (`DataDefinition.ViewOfDDM`) + T6 (`Symbol.ViewOfDDM`) + the T9 cache bump. No separate task; this entry records the traceability. The persistence assertion lives in **T9's** cache round-trip test.

### T8a — Extend `findCursorTarget` to declaration `NameRange`s (OQ-B, foundation for T8b)

**Type:** extend `internal/server/cursor.go` (additive — declaration targeting).
**Files:** `internal/server/cursor.go` (`findCursorTarget`); a new cursor-target case for a `Symbol`/`DataDefinition` declaration `NameRange` (data field, and a `VIEW OF` node).
**What:** today `findCursorTarget` maps a cursor only to a use-site (`EdgeEntry`/`DataAccessEntry`/`VariableRef`). Extend it to **also** target a declaration `NameRange` (walk `FileAnalysis.Structure`/`Definitions` for the smallest node whose `SelectionRange` contains the cursor). Keep it **additive and use-site-first**: where a use-site matches today, it must still win (regression assertion); a declaration target is returned only when no use-site matches. Preserve the smallest-containing-range tie-break. Carry enough on the returned target for T8b to know it's a view-field vs. a view-name vs. a view-local REDEFINE sub-field (e.g. the owning `Symbol`/`DataDefinition` + its `ViewOfDDM`/`Redefines`).
**Fixture:** reuse `07-typed-fields.NSP` / the view fixture; a cursor on a plain data-field name now resolves to that field's declaration.
**Expected result (exact):** a cursor on a data-field/view declaration name resolves to a declaration target carrying its `NameRange` + owning-node metadata; a cursor on a use-site still resolves to the use-site (unchanged); a cursor on neither → nil.
**Modeled-gap coverage:** cursor in whitespace / on a keyword → nil (no panic, FR-43).
**Fuzz:** `FuzzCursorLookup` still never panics over the extended path.
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] `findCursorTarget` targets declaration `NameRange`s, additively; use-site-first regression asserted.
- [ ] Test: plain data-field cursor → its declaration; use-site cursor → unchanged use-site; smallest-containing tie-break preserved.
- [ ] `FuzzCursorLookup` green.
- [ ] `just verify` green.

### T8b — View-field navigation + DDM-inherited type (Story 4)

**Type:** extend navigation (`definition.go`) + the outline/hover type-inheritance renderer, composing T8a + feature-27 resolution.
**Files:** `internal/server/definition.go` (view-field → `.NSD` field navigation); `internal/server/document_symbols.go` and/or `hover.go` (inherited-type resolution for a bare view field); reuse `workspace.ResolveDDMPath` + `workspace.findFieldInDefinitions` (already used by `provideDDMDefinition`/`resolveVariableDefinitionCrossFile`).
**Cursor entry point (RESOLVED — OQ-B):** consume T8a's declaration target. From the declaration target's owning view node (`ViewOfDDM`) run the DDM resolution:
  - **view field (bare or restated)** → `workspace.ResolveDDMPath(view.ViewOfDDM, idx, referencingRelPath, cfg)` → read the `.NSD` → `findFieldInDefinitions(fieldName, ddmFA.Definitions)` → `Location` at that field's `NameRange` (reuse the `provideDDMDefinition` file-read/range-convert tail).
  - **view name** → its own `VIEW OF` line (same-file) — the view node's `SelectionRange` (no DDM hop).
  - **view-local REDEFINE sub-field** → the view's own REDEFINE line (same-file, via `Symbol.Redefines`/T3), **not** the DDM.
**Inherited type (OQ-5):** for the outline `Detail`/hover of a **bare** view field (empty local `Type`): resolve the DDM (as above) and use the matched DDM field's `Type`; if unresolved/absent, fall back to the restated view-line format if present; else no type. This resolution is server-side (needs `idx`) — so the `documentSymbol` provider must resolve view-field detail at request time when a node has a parent `ViewOfDDM` and no local `Type`. Keep it store-first then index (features 10–13); snapshot `idx`/`res` under `RLock`, release before I/O (F7).
**Fixtures:** a `.NSP` view fixture paired with a DDM fixture — reuse `internal/server/testdata/hover/customer.NSD` (feature 12) as the DDM and add `internal/server/testdata/{view or definition}/emp-view.NSP` with a `VIEW OF CUSTOMER` selecting bare + restated + array + in-view-REDEFINE fields; plus a `TYPE: SQL` DDM (reuse `internal/analysis/natural/testdata/ddm/sql-type.NSD`) and a view over a DDM outside the chain / a view field absent from the DDM.
**Expected result (exact):**
- Go-to-definition on a **bare view field** → `Location` in the `.NSD` at the DDM field's `NameRange` (resolved through the same steplib chain as `READ`/`FIND`).
- Bare view field's outline/hover detail → the **DDM's** type (e.g. `A20`) when the DDM resolves; a restated field → its written type verbatim.
- Go-to-definition on the **view name** → its own `VIEW OF` line (same-file `SelectionRange`).
- Go-to-definition on a **view-local REDEFINE sub-field** → the view's own REDEFINE line (same-file), never the DDM.
**Modeled-gap coverage (Story 4 AC3, FR-17):**
- DDM outside the caller's chain (`ResolveDDMPath` returns `""`) → field still lists (with restated type if any), go-to-definition returns **empty**, no error/diagnostic.
- `TYPE: SQL` DDM (empty `Definitions` from `ddm.go`) → field lists, no inherited type, navigation empty, no error. **Record the limit** in the plan `## Results` / code comment (recorded non-goal: SQL-DDM parsing).
- View field absent from the DDM (`findFieldInDefinitions` zero range) → lists, navigation empty, no error.
**Fuzz:** confirm `FuzzProvideDefinition`/`FuzzProvideHover`/`FuzzDocumentSymbols` never panic on a view-field cursor / a view with unresolved DDM (FR-43).
**Analyzer-seam note:** navigation composes `workspace.*` + `model` only; no parser internals in `internal/server`.
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] View fixture + DDM fixture(s) added; tests pin: bare-field nav → `.NSD` field range; view-name nav → own line; view-local REDEFINE nav → own line.
- [ ] Inherited-type detail test: bare field shows DDM type; restated field shows written type; fallback order (OQ-5) covered.
- [ ] Three modeled-gap tests (outside-chain / `TYPE: SQL` / absent-field) assert list-but-no-nav, no error, no diagnostic.
- [ ] Fuzz targets still never panic on view-field inputs.
- [ ] `just verify` green.

### T9 — Cache-format bump `0.8.0` → `0.9.0` + persistence round-trip

**Type:** cache-version bump + persistence proof.
**Files:** `internal/workspace/cache.go` (`cacheFormatVersion = "0.9.0"`).
**Why:** T1/T3/T5/T6 add persisted fields to `model.Symbol` (via `Structure`) and `model.DataDefinition` (via `Definitions`) — `Symbol.{Type,Level,Dimensions,Redefines,ViewOfDDM}` and `DataDefinition.{Redefines,ViewOfDDM}`. A stale `0.8.0` cache must force a full rebuild (content-hash + format-version invalidation).
**Expected result (exact):**
- `cacheFormatVersion == "0.9.0"`; a fabricated `0.8.0` cache (build `CacheFile{Version:"0.8.0"}` in Go — the cache is gzip, no readable version substring, per feature 24) triggers a cold rebuild (mirror the feature-24 version-bump test migration).
- A save→load round-trip of a fixture with typed fields, a REDEFINE (`Redefines` set), and a `VIEW OF` (`ViewOfDDM` set) preserves all new fields (this is Story 3 AC3's persistence assertion — see T7).
- Corrupt/truncated cache still routes to a cold rebuild, never panics (existing `FuzzLoadCache`/`TestLoad_CorruptCompressedCache` stay green).
**Sequencing:** monotonic single bump (`0.8.0` → `0.9.0`) — confirmed by OQ-4 (feature 27 shipped first).
**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**DoD:**
- [ ] `cacheFormatVersion` = `0.9.0`.
- [ ] Version-bump test: `0.8.0` cache → cold rebuild.
- [ ] Round-trip test: `Symbol.Type/Level/Dimensions/Redefines/ViewOfDDM` and `DataDefinition.Redefines/ViewOfDDM` survive save→load.
- [ ] Existing corrupt-cache/fuzz tests unchanged and green.
- [ ] `just verify` green.

### T10 — Docs sync + `TestInitialize` invariant

**Type:** as-built doc sync (per CLAUDE.md workflow) + capability-lock assertion.
**Files:** `CLAUDE.md` (Project state + feature-28 note), `README.md` (feature set / outline), the feature `plan.md` `## Results` (record the `TYPE: SQL` limit and the byte-offset deferral).
**Expected result:**
- `TestInitialize` allow-list asserted **byte-identical** (no new capability — extends existing providers only). Add/keep an explicit assertion.
- CLAUDE.md "Project state" updated: feature 28 shipped, cache `0.9.0`, typed outline + `VIEW OF` binding, no new capability, recorded `TYPE: SQL` DDM limit.
**TDD agents:** n/a (docs) — but run `just verify` to confirm the `TestInitialize` lock.
**DoD:**
- [ ] `TestInitialize` unchanged/asserted.
- [ ] CLAUDE.md + README + plan `## Results` reflect as-built (done by `/finalize-feature`; flagged here for the `review-docs` reviewer).

---

## Reviews required (for `/review-feature`)

- **review-architecture / seam guard:** confirm `internal/server` still imports no `internal/analysis/natural` internals; `Detail`-string composition and view-field navigation live server-side; parser/AST/`ViewOfDDM`/`Redefines` extraction live in `internal/analysis/natural`; cross-file resolution in `internal/workspace` (reused, not duplicated). `seam_test.go` still passes.
- **review-correctness (natural-expert):** `VIEW [OF]` grammar (OF optional), format-inheritance-from-DDM semantics (OQ-5), REDEFINE/FILLER `nX` rendering, array index-range notation (not `OCCURS`), verbatim type spellings. Confirm the T3 REDEFINE-merge change preserves hover's parameter interface.
- **review-robustness (FR-43):** fuzz `VIEW OF` parse path, view-field cursor, unresolved-DDM navigation — never panic. Malformed/partial view yields partial structure.
- **review-modeled-gaps (FR-17):** DDM outside chain / `TYPE: SQL` DDM / view field absent from DDM → lists, no navigation, **no error, no diagnostic**. Legal partial/overlapping REDEFINE coverage → no diagnostic.
- **review-cache:** `0.9.0` bump monotonic; `0.8.0` → cold rebuild; new fields round-trip; corrupt cache never panics.
- **review-docs:** CLAUDE.md/README/plan `## Results` match as-built; `TestInitialize` allow-list unchanged.

---

## Open questions — RESOLVED at the approval checkpoint (2026-07-26)

- **OQ-A (T3 REDEFINE representation shape): DECIDE AT IMPLEMENTATION.** Choose flatten-with-`Redefines`-stamp on subfields **vs.** a retained synthetic redefine-block child node by running the existing `data.go`/hover tests first — pick whichever preserves the existing non-redefine outline node set and hover parameter-interface output unchanged. No distinct `SymbolKind` (OQ-2 stands) unless the retained-block-node shape clearly needs it.
- **OQ-B (T8 view-field cursor entry point): RESOLVED — extend `findCursorTarget` to declarations.** The user chose the **broader change**: extend `findCursorTarget` (`internal/server/cursor.go`) to also target `DataDefinition`/`Symbol` declaration `NameRange`s (view fields, and — as a beneficial side effect — go-to-definition on any data field). This is bigger than a view-field-only declaration-walk, so **carve it into its own slice before T8's navigation wiring: T8a extends `findCursorTarget` to resolve a cursor to a field/view declaration `NameRange` (with tests that a plain data-field cursor now targets its declaration); T8b wires the view-field → `.NSD` navigation + inherited type on top.** Keep the change additive: use-site targeting (`Edge`/`DataAccess`/`VariableRef`) must still win where it applies today (regression assertion), and a declaration target is returned only when no use-site matches. Preserve the smallest-containing-range tie-break.
- **OQ-C (inherited-type detail at request time): RESOLVED — outline + hover (per-request resolution).** The `documentSymbol` provider resolves a bare view field's inherited DDM type **per request** (store-first, snapshot `idx`/`res` under `RLock`, release before I/O — F7), satisfying Story 4 AC1 ("type inherited from its DDM field **in the outline/hover**"). Hover reuses the same resolution. This makes `documentSymbol` detail for view fields index-dependent (the analyzer stays extraction-pure — it records only the view's own restated format + `ViewOfDDM`).
- **OQ-D (fixture placement): RESOLVED — planner's placement.** Parser fixture `internal/analysis/natural/testdata/parser/28-view-of.NSP`; structure fixtures under `internal/analysis/natural/testdata/structure/`; the server nav fixture under `internal/server/testdata/definition/` (view `.NSP` + reuse the existing `internal/server/testdata/hover/customer.NSD` DDM). `TYPE: SQL` DDM reuses `internal/analysis/natural/testdata/ddm/sql-type.NSD`.
