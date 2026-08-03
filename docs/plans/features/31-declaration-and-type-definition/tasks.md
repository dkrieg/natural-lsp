# Tasks: Declaration & Type-Definition Navigation (feature 31)

**Plan:** [plan.md](./plan.md)
**PRD requirements:** FR-58 (declaration & type-definition navigation — new); refines FR-24 (definition). FR-17 (modeled gaps), FR-43 (graceful degradation / never-panic).
**Depends on:** feature 10 (definition provider + cursor mapping), feature 27 (variable declarations + host-var/DDM binding), feature 28 (view-field → DDM binding).

**Scope invariants (hold for every task):**
- **Server-layer only.** No `internal/model` change, no cache-format bump (stays `0.9.0`). Everything is provider wiring over features 10/27/28. The Analyzer seam is untouched — `internal/server` keeps depending only on `internal/analysis.Analyzer` and `internal/workspace` exported helpers.
- **Marshal via the feature-19 json/v2 path** — `marshalResult(...)` in `server.go`. Both new methods are definition-family, so the **empty-result sentinel is `null`** (byte `[]byte(`null`)` when the provider returns `nil`), matching `textDocument/definition` (server.go ~L1119). Never `[]`.
- **Store-first buffer reads, encoding-aware positions** (ADR-008): reuse `uriToRelPath`, `fromProtocolPosition`, `toProtocolRange`, and the F7 `idxResMu` RLock snapshot exactly as `provideDefinition` does.

---

## Current-state findings & impact

Surveyed `internal/server/{definition.go,cursor.go,server.go,server_test.go}`, `internal/workspace/field_resolution.go`, existing `testdata/`, and the `go.lsp.dev/protocol` capability/param types. Findings that shape the decomposition:

1. **`provideDefinition` already does everything declaration needs.** `internal/server/definition.go` handles, in one function: resolved call/transfer edges (CALLNAT/FETCH/RUN), inline + external PERFORM, DDM data-access, VIEW-OF field → DDM, same-file variable-use → `DEFINE DATA` (`resolveVariableDefinition`), cross-file `USING` variable resolution (`resolveVariableDefinitionCrossFile`), and all the FR-17 modeled gaps (`*`-system / `&`-dynamic / unresolved → `nil`). **Declaration is therefore a thin delegation, not new logic** — the only intended divergence (OQ-1) is that declaration is *advertised under a distinct gesture*; the resolution is identical (superset of the plan's "call/transfer/subroutine + variable use", which is acceptable and matches OQ-1).
2. **`DeclarationParams` and `TypeDefinitionParams` both embed `TextDocumentPositionParams`** (`.TextDocument.URI`, `.Position`) plus work-done/partial-result params — structurally identical to `DefinitionParams`. Mapping between them is a trivial field copy; **no shared-core extraction is strictly required**, though T1's refactor step may introduce one small `provideDefinitionCore(hctx, uri, pos)` seam to avoid a params-shuffle.
3. **Type-definition data already exists behind the workspace seam.** `workspace.ResolveDDMFieldLocation(fieldName, ddmName, idx, referencingPath, cfg) (Range, ddmPath)` (field_resolution.go L114) returns the DDM field's `NameRange` + `.NSD` path via the steplib chain, and already returns zero/empty for the FR-17 gaps (DDM outside chain, `TYPE: SQL` DDM, field absent). Feature 28's `findDeclarationTarget` (cursor.go L181) already resolves a cursor to a `DeclarationTarget{Symbol, OwningView, ...}` and populates `OwningView` when the field sits inside a `VIEW OF`. **Type-definition = `findDeclarationTarget` → if `OwningView.ViewOfDDM != ""` → `ResolveDDMFieldLocation` → Location; else empty.** This closely parallels `provideDefinitionForViewField` (definition.go L697) but is *cursor-driven from scratch* rather than reusing definition's dispatch (definition's view path also navigates the view NAME node and view-local REDEFINEs to *same-file* lines — type-definition must NOT do that; type of a field is always the DDM field or empty).
4. **Capability registration is a locked allow-list.** `handleInitialize` (server.go ~L245) sets the `protocol.ServerCapabilities` literal; `TestInitialize` (server_test.go L466, `requiredProviders` slice L624) enforces it. `protocol.ServerCapabilities` has `DeclarationProvider DeclarationProvider` and `TypeDefinitionProvider TypeDefinitionProvider` union fields (lifecycle.gen.go L126/L132); both accept `protocol.Boolean(true)` exactly like `DefinitionProvider` does today. **Adding the two providers means two new `requiredProviders` entries.**
5. **Dispatch + marshaling pattern is uniform** (server.go L1099 for definition): gate on `stateInitialized`, decode params with `params.UnmarshalJSONFrom(jsontext.NewDecoder(...))`, call provider, `null` sentinel on `nil`, else `marshalResult`. The two new `case` strings are `"textDocument/declaration"` and `"textDocument/typeDefinition"`.
6. **Fixtures already cover every case — no new `testdata/` needed:**
   - `testdata/navigation/caller.NSP` (+ `helper.NSN`): `CALLNAT 'HELPER'` (L10, resolved) and `PERFORM INLINE-SUB` (L11, inline sub). `unresolved.NSP` for a dynamic/unresolved gap.
   - `testdata/variablenav/VARTEST.NSP`: variable use `WRITE #SCALAR-VAR` (L22 → decl L9); `MOVE *DATE …` (L32, `*`-system gap); and `#SCALAR-VAR (A20)` (L9) is a **plain scalar var with no DDM type** — the type-definition scalar-only-empty gap.
   - `testdata/viewdef/empview.NSP` (+ `customer.NSD`): `EMP-VIEW VIEW OF CUSTOMER` with bare view field `CUSTOMER-ID` (L6 → `customer.NSD` DDM field `CUSTOMER-ID` L6) and `NOT-A-FIELD` (L12, absent-from-DDM gap). `view-missing-ddm.NSP` (DDM outside chain) and `view-sql-ddm.NSP` (`TYPE: SQL`) for the remaining type gaps.

**No contract changes → no migration tasks.** No existing consumer is affected; both additions are purely additive server capabilities + handlers.

**Traceability map:**
| Acceptance criterion | Task(s) |
|---|---|
| Story 1 AC1 — declaration resolves call/transfer/subroutine like definition | T1 |
| Story 1 AC1 — declaration resolves a variable use → `DEFINE DATA` | T2 |
| Story 1 AC2 — dynamic/unresolved/`*`-system → empty; `declarationProvider` advertised; `TestInitialize` updated | T1 (advertise + `TestInitialize`), T2 (gaps) |
| Story 2 AC1 — typeDefinition on VIEW-OF field → DDM field/`.NSD` via steplib | T3 |
| Story 2 AC2 — scalar-only field → empty; `typeDefinitionProvider` advertised; `TestInitialize` updated | T3 (advertise + `TestInitialize`), T4 (scalar-only + absent/unresolved/SQL gaps) |
| FR-43 never-panic on garbage input | T5 (fuzz) |

---

## T1 — `textDocument/declaration`: call/transfer/subroutine mirror + capability

**Pins:** Story 1 AC1 (call/transfer/subroutine parity with definition); Story 1 AC2 (capability advertised + `TestInitialize` updated).

**Behavior:** `textDocument/declaration` returns, for a cursor on a resolved call/transfer/subroutine reference, **the same `[]protocol.Location` as `textDocument/definition`**. Advertise `declarationProvider: true`.

**Expected results (fixture `testdata/navigation/caller.NSP` + `helper.NSN`, reuse):**
- Cursor on `CALLNAT 'HELPER'` (L10) → one Location at `helper.NSN` object-root `Structure.SelectionRange` (the `{0,0}` caret) — byte-identical to `provideDefinition` for the same cursor.
- Cursor on `PERFORM INLINE-SUB` (L11) → one Location at the same-file `DEFINE SUBROUTINE INLINE-SUB` `SelectionRange` (L13).
- `initialize` response advertises `declarationProvider`; `TestInitialize`'s `requiredProviders` gains `"declarationProvider"`.

**Modeled-gap coverage:** none new here (call gaps are folded into T2's gap table); this slice is the resolved-path mirror only.

**Reuse / seams:** new `internal/server/declaration.go` holding `provideDeclaration(hctx, protocol.DeclarationParams) ([]protocol.Location, error)` that **delegates to the existing definition logic** by mapping `DeclarationParams.TextDocumentPositionParams` → `protocol.DefinitionParams` and calling `provideDefinition` (or a small extracted `provideDefinitionCore(hctx, uri, pos)` seam — refactor step). Wire dispatch `case "textDocument/declaration"` in `server.go` (mirror the definition case: `stateInitialized` gate, `UnmarshalJSONFrom`, `null` sentinel on `nil`, else `marshalResult`). Add `DeclarationProvider: protocol.Boolean(true)` to the `handleInitialize` capabilities literal.

**TDD agents:**
- `tdd-red`: table test `TestProvideDeclaration_CallMirror` in a new `declaration_test.go` (harness pattern from `definition_variable_test.go`: `BuildWithCache` + `Resolve` + hand-built `handlerContext`), asserting declaration Locations equal definition Locations for the CALLNAT and PERFORM cursors; plus a `TestInitialize` assertion extension for `declarationProvider`. A dispatch/round-trip assertion may reuse the `server_test.go` initialize harness.
- `tdd-green`: add `declaration.go`, the dispatch case, and the capability + `requiredProviders` entry.
- `tdd-refactor`: if duplication warrants, extract `provideDefinitionCore(hctx, uri, pos)` shared by definition + declaration; keep `provideDefinition`'s signature stable.

**DoD:**
- [ ] `provideDeclaration` returns definition-identical Locations for CALLNAT + PERFORM cursors (test asserts equality against `provideDefinition`).
- [ ] `"textDocument/declaration"` dispatch case wired; `null` sentinel on empty; result via `marshalResult`.
- [ ] `declarationProvider` advertised in `initialize`; `TestInitialize` `requiredProviders` includes `"declarationProvider"` and passes.
- [ ] No `internal/model`/cache change; Analyzer seam untouched; `just verify` green.

---

## T2 — `textDocument/declaration`: variable use → `DEFINE DATA` + modeled gaps

**Pins:** Story 1 AC1 (variable use → its `DEFINE DATA` declaration, reusing feature 27); Story 1 AC2 (dynamic/unresolved/`*`-system → empty, no error).

**Behavior:** declaration on a variable use resolves to the variable's `DEFINE DATA` line (same delegation to `provideDefinition`, which already runs `resolveVariableDefinition` / `resolveVariableDefinitionCrossFile`). Modeled gaps return `null`.

**Expected results (fixture `testdata/variablenav/VARTEST.NSP`, reuse):**
- Cursor on `WRITE #SCALAR-VAR` use (L22) → Location at the `#SCALAR-VAR (A20)` declaration `NameRange` (L9).
- Cursor on the declaration itself (L9) → idempotent, resolves to itself (same as definition).
- Cursor on `#GROUP.#SUB-FIELD` (L28) → the group-scoped sub-field declaration (L12) — confirms the feature-27 group-qualification path flows through declaration.

**Modeled-gap coverage (return `null`, no error, no diagnostic — FR-17/FR-43):**
- `MOVE *DATE …` (`*`-system var, VARTEST L32) → empty.
- A dynamic / unresolved call target (`testdata/navigation/unresolved.NSP`) → empty.
- Undeclared variable (`MOVE #AMOUNT TO #OTHER-VAR`, L24, `#OTHER-VAR`) → empty.

**Reuse / seams:** no new production code expected if T1 delegates fully — this slice is **characterization of the inherited behavior** (record in the task that no delegation gap exists; if a gap surfaces, fix in the delegation, not by forking logic). Store-first read is inherited from `provideDefinition`.

**TDD agents:**
- `tdd-red`: `TestProvideDeclaration_VariableAndGaps` table (cursors above), asserting the variable-use Location and `nil` for each gap. If the assertions pass without production change, keep them as permanent regression coverage and note "already satisfied by T1 delegation" in the task log.
- `tdd-green`: only if a gap is found (e.g. a params-mapping bug dropping the store-first buffer) — fix in `declaration.go`/the shared core.
- `tdd-refactor`: none expected.

**DoD:**
- [ ] Variable-use, idempotent-declaration, and group-qualified cursors return the correct `DEFINE DATA` Locations (equal to definition).
- [ ] `*`-system, dynamic/unresolved, and undeclared cursors return `nil` (→ wire `null`); no error, no diagnostic emitted.
- [ ] Store-first buffer read verified (a test that opens a modified buffer and navigates a variable added only in the buffer, mirroring definition's store-first tests).
- [ ] `just verify` green.

---

## T3 — `textDocument/typeDefinition`: VIEW-OF field → DDM field + capability

**Pins:** Story 2 AC1 (typeDefinition on a VIEW-OF field navigates to the DDM field/`.NSD` via the feature-28 DDM namespace/steplib resolution); Story 2 AC2 (capability advertised + `TestInitialize` updated).

**Behavior:** `textDocument/typeDefinition` maps the cursor to a declaration target (`findDeclarationTarget`); if the target is a field inside a `VIEW OF` (`OwningView.ViewOfDDM != ""`), resolve the DDM field via `workspace.ResolveDDMFieldLocation(sym.Name, OwningView.ViewOfDDM, idx, relPath, cfg)` and return a Location at the DDM field's `NameRange` in the `.NSD`. Advertise `typeDefinitionProvider: true`.

**Expected results (fixture `testdata/viewdef/empview.NSP` + `customer.NSD`, reuse):**
- Cursor on bare view field `CUSTOMER-ID` (empview L6) → one Location at `customer.NSD` DDM field `CUSTOMER-ID` (L6, the field-name span; feature 28 populated DDM-field `NameRange`).
- Cursor on `BALANCE` (empview L7) → Location at `customer.NSD` `BALANCE` (L12).
- `initialize` advertises `typeDefinitionProvider`; `TestInitialize` `requiredProviders` gains `"typeDefinitionProvider"`.

**Reuse / seams:** new `internal/server/type_definition.go` holding `provideTypeDefinition(hctx, protocol.TypeDefinitionParams) ([]protocol.Location, error)`: reuse `uriToRelPath`, the F7 `idxResMu` RLock snapshot of `idx`/`res`, store-first content read (`hctx.store.Get` → fall back to index/disk exactly as `provideDefinition`), `fromProtocolPosition`, `findDeclarationTarget`, `workspace.ResolveDDMFieldLocation`, and `toProtocolRange`. **Do NOT** reuse `provideDefinitionForViewField` wholesale — type-definition must resolve *only* to the DDM field (a bare or restated view field), never to a same-file view-name / REDEFINE line. Wire dispatch `case "textDocument/typeDefinition"` (definition-family: `null` sentinel, `marshalResult`). Add `TypeDefinitionProvider: protocol.Boolean(true)` to the capabilities literal.

**TDD agents:**
- `tdd-red`: `TestProvideTypeDefinition_ViewField` in a new `type_definition_test.go` (harness over `testdata/viewdef`), asserting the CUSTOMER-ID and BALANCE cursors land on the correct `customer.NSD` field ranges; plus a `TestInitialize` extension for `typeDefinitionProvider`.
- `tdd-green`: add `type_definition.go`, dispatch case, capability + `requiredProviders` entry.
- `tdd-refactor`: extract any small shared cursor→content snapshot helper if it reduces duplication with definition without crossing the seam.

**DoD:**
- [ ] View-field cursors resolve to the correct `.NSD` DDM field `NameRange` Locations via the steplib chain (chain-aware, not `candidates[0]`).
- [ ] `"textDocument/typeDefinition"` dispatch wired; `null` sentinel on empty; `marshalResult` path.
- [ ] `typeDefinitionProvider` advertised; `TestInitialize` includes `"typeDefinitionProvider"` and passes.
- [ ] No `internal/model`/cache change; Analyzer seam untouched; `just verify` green.

---

## T4 — `textDocument/typeDefinition`: scalar-only & DDM modeled gaps → empty

**Pins:** Story 2 AC2 (a field with only a scalar format / no DDM type object → empty, no error — FR-17).

**Behavior:** typeDefinition returns `null` for every case where there is no DDM type object.

**Expected results (return `nil` → wire `null`, no error, no diagnostic):**
- **Scalar-only variable** — cursor on `#SCALAR-VAR (A20)` (VARTEST L9) or its use (L22): a plain `DEFINE DATA` field with a scalar format and no owning view → empty. (Guard: `findDeclarationTarget` returns a target with `OwningView == nil` / `ViewOfDDM == ""` → return `nil` before any DDM resolution.)
- **View field absent from DDM** — cursor on `NOT-A-FIELD` (empview L12): view resolves but `ResolveDDMFieldLocation` returns zero Range → empty.
- **DDM outside the chain** — `testdata/viewdef/view-missing-ddm.NSP`: `ResolveDDMFieldLocation` returns `("", zero)` → empty.
- **`TYPE: SQL` DDM** — `testdata/viewdef/view-sql-ddm.NSP` (+ `sql-table.NSD`): no parsed DDM fields → empty.
- **No target at cursor** (blank line / keyword) → empty.

**Reuse / seams:** the gap handling largely falls out of `ResolveDDMFieldLocation` already returning empties (finding #3); the one explicit guard to add is the scalar-only / non-view early-return in `provideTypeDefinition`. No new production paths beyond that guard.

**TDD agents:**
- `tdd-red`: `TestProvideTypeDefinition_Gaps` table over the five gap cursors, each asserting `nil` result and `nil` error, and asserting no diagnostic is produced (type-definition never touches the diagnostic channel).
- `tdd-green`: add the scalar-only / non-view early-return guard if T3 didn't already cover it.
- `tdd-refactor`: none expected.

**DoD:**
- [ ] All five gap cursors return `nil` (→ `null`), no error, no diagnostic.
- [ ] Scalar-only early-return guard present and unit-covered.
- [ ] `just verify` green.

---

## T5 — Fuzz the two entry points (FR-43, never panic)

**Pins:** FR-43 (graceful degradation / never-panic on garbage input) for both new providers.

**Behavior:** `provideDeclaration` and `provideTypeDefinition` never panic on arbitrary URIs, positions (out-of-range lines/columns, negative-adjacent), or content, mirroring `FuzzProvideDefinition` (fuzz_test.go L269).

**Reuse / seams:** add `FuzzProvideDeclaration` and `FuzzProvideTypeDefinition` to `internal/server/fuzz_test.go`, reusing the existing `FuzzProvideDefinition` scaffold (build a minimal `handlerContext` over a small fixture index, feed fuzzed position/URI, assert no panic and a well-typed result). Seed with the fixtures from T1–T4 (caller.NSP, VARTEST.NSP, empview.NSP) plus degenerate inputs.

**TDD agents:**
- `tdd-red`: add the two fuzz targets with seed corpus; run `go test -run=Fuzz... ` (short) to confirm they execute.
- `tdd-green`: fix any panic surfaced (defensive nil/range guards in the new providers).
- `tdd-refactor`: none expected.

**DoD:**
- [ ] `FuzzProvideDeclaration` and `FuzzProvideTypeDefinition` present, seeded, and passing the short run.
- [ ] No panic on any seed or discovered input; both always return a well-typed `([]protocol.Location, error)`.
- [ ] `just verify` green.

---

## Reviews required (`/review-feature`)

- **review-lsp-spec** — `declarationProvider` / `typeDefinitionProvider` capability shapes are spec-correct (`Boolean(true)` unions); both methods return `Location | Location[] | null` with the **`null`** empty sentinel (not `[]`); params decode via `UnmarshalJSONFrom`; result marshaled via the json/v2 `marshalResult` path. `TestInitialize` allow-list extended by exactly two entries.
- **review-architecture / seam** — `internal/server` still imports only `internal/analysis.Analyzer` + `internal/workspace` helpers; no parser internals; no `internal/model` or cache-format change. `seam_test.go` still passes.
- **review-concurrency** — both providers snapshot `idx`/`res` under the `idxResMu` RLock and release before I/O (F7), matching `provideDefinition`.
- **review-correctness (natural-expert)** — declaration == definition is the correct Natural semantics (no header/impl split); type-definition of a view field is its DDM field, and a plain scalar variable genuinely has no separate type object (empty is correct, not a defect).
- **review-tests** — every AC has a test; modeled gaps assert `null` + no-diagnostic; store-first path covered; fuzz targets present.
- **review-docs** — `CLAUDE.md` "Project state" + `README.md` capability list updated to note the two new providers and the two-entry `TestInitialize` growth (done at `/finalize-feature`).

---

## Open questions

- **OQ-1 (from plan) — declaration vs definition divergence.** This plan advertises declaration as a **full delegation to `provideDefinition`** (so it also covers view-field → DDM and DDM data-access, a superset of the plan's "call/transfer/subroutine + variable use"). Confirm that superset behavior is desired (it is the natural "declaration of a symbol" answer and satisfies AC1). If a strict subset is wanted instead, T1 would need to fork logic — not recommended.
- **OQ-2 — "DDM-typed variable" beyond view fields.** The plan mentions "a `VIEW OF` field *or a DDM-typed variable*". In the current extraction model the only DDM-typed variables are `VIEW OF` fields (there is no standalone scalar bound to a DDM type). T3/T4 treat the view-field path as the whole of "DDM-typed variable" and send every non-view scalar to the empty gap. Confirm no additional standalone-DDM-typed-variable construct is expected; if one is, it needs an extraction change (out of this feature's server-only scope) and should be a separate feature.
- **OQ-3 — restated view field with an explicit scalar format** (e.g. `CUSTOMER-NAME (A50)` in empview L8, which restates a DDM field with a local format). Intended type-definition target is still the **DDM field** (the field is part of the view, so its type object is the DDM field), matching feature 28's view-field navigation. Confirm this is desired rather than treating the explicit `(A50)` as "scalar-only → empty". Recommendation: DDM field (consistent with feature 28); add a T3 assertion for this cursor once confirmed.
