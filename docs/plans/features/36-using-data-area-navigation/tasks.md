# Feature 36 — `DEFINE DATA … USING <data-area>` Navigation (bug #58): Task Plan

**Spec:** `docs/plans/features/36-using-data-area-navigation/plan.md` (authoritative; approach approved).
**PRD:** FR-24 (definition), FR-25 (references), FR-58 (declaration/typeDefinition, feature 31),
FR-59 (document links, feature 32), FR-17/FR-43 (modeled gaps / never-crash).
**Additive model change:** one new `EdgeKind` constant `model.EdgeUsesDataArea` (no struct-shape change).
**Cache-format bump:** `0.9.0` → `0.10.0` (the new edges are persisted in `FileAnalysis.Edges`).
**No new LSP capability** — extends existing definition/declaration/references/documentLink providers;
the locked `TestInitialize` allow-list is unchanged.
**Seam:** all work stays on the correct side of the Analyzer seam. Extraction lives in
`internal/analysis/natural`; resolution in `internal/workspace`; providers in `internal/server` consume
`analysis.Analyzer` + the `workspace` public API only.

---

## Current-state findings & impact

Surveyed the code as it is on `main` (features 00–34 shipped). Findings against each acceptance criterion:

### The machinery already exists — this feature wires one missing edge into it

- **`model.DataAreaRef{Name, SectionKind, Range}`** (`internal/model/model.go` L491) is already produced by
  `extractDataAreaRefs` (`internal/analysis/natural/data.go` L338) for every `LOCAL|PARAMETER|GLOBAL
  USING <name>` clause — `Range` is the source span of the *name token*, and a malformed `USING` with no
  name is already skipped (`section.Using == ""` → no ref). Persisted since cache `0.8.0`. Today it is
  consumed only as a *lookup source* for cross-file field resolution (feature 27), never as a cursor
  target.
- **`extractEdges`** (`internal/analysis/natural/calls.go`) emits `EdgeEntry{Source, Target, Kind,
  TargetName, Library}` with `Source` = the reference span and `TargetName` = the (upper-cased) target
  name. `USING` clauses emit **no** edge today — the root cause of #58.
- **`Analyze`** (`internal/analysis/natural/analyzer.go` L96–136) already has the append-then-re-sort
  pattern: SQL call edges are appended to `result.Edges` and re-sorted by `Source.Start` (L104–111);
  `result.DataAreaRefs = extractDataAreaRefs(ast)` is assigned at L131. **This is where the derived
  `USES_DATA_AREA` edges go** (OQ-1: derive from `DataAreaRefs`, do not re-walk the AST).
- **`workspace.Resolve`** (`internal/workspace/resolution.go` L537) and **`ResolveInto`** (L646) each
  carry a per-edge `switch edge.Kind` (L554 / L753) with cases for `EdgeCalls`/`EdgeNavigatesTo`/
  `EdgePerforms`/`EdgeIncludes` and a `default: continue` (L629 / L805). The two switches are **duplicated**
  — a new kind must be added to **both** (or factored into a shared helper). `resolveByName` (L412) is the
  reusable primitive (explicit-library bypass → steplib chain → flat-namespace with ambiguity diagnostic).
- **`resolveDataAreaCandidate`** (`internal/workspace/field_resolution.go` L73) already resolves a
  data-area name across the three namespaces **in order `ObjectLocalDataArea → ObjectParameterDataArea →
  ObjectGlobalDataArea`** via the steplib chain (`resolveCandidateViaChain` L219, non-transitive,
  first-reachable-wins). This is exactly OQ-2's recommended order — but it returns a `*Candidate` and does
  **not** produce an `Ambiguous` outcome. For the resolution switch we need `Resolved`/`Unresolved`/
  `Ambiguous`, so we reuse `resolveByName` (which produces all three) once per namespace in that order.
- **`findCursorTarget`** (`internal/server/cursor.go` L83) scans `fa.Edges` by `Source` range
  (smallest-containing tie-break), edges winning over data-access/variable refs. **Once a `USES_DATA_AREA`
  edge has `Source` = the `USING` name span, a cursor on that name maps to it with no cursor-code change.**
  (Variable refs are excluded inside `DEFINE DATA`, so there is no precedence conflict.)
- **`provideDefinition`** (`internal/server/definition.go` L156+) is edge-kind-agnostic: it does
  `res.Get(relPath, edge.Source)` (L183) → `IsResolved` (L190) → external-target
  `definitionLocation(... Structure.SelectionRange)` (L228, L316). A resolved `USES_DATA_AREA` edge to a
  `.NSL/.NSA/.NSG` flows through **unchanged** and lands on the data-area object's root
  `Structure.SelectionRange`.
- **`extractStructure`** (`internal/analysis/natural/structure.go` L34) builds a root `SymbolObject` for
  **every** object type (name = filename stem), with `SelectionRange = {prog.StartPos, prog.StartPos}`
  (a zero-width landing point). So `.NSL/.NSA/.NSG` objects already have a definition-landing range.
- **`provideDeclaration`** (`internal/server/declaration.go` L12) delegates verbatim to
  `provideDefinition` — Story 1 AC2 is satisfied for free the moment definition works.
- **`buildDocumentLinks`** (`internal/server/document_links.go` L120) iterates **all** `fa.Edges`, links
  every `IsResolved()` edge whose target is a different file, and skips same-file. A resolved
  `USES_DATA_AREA` edge yields a link automatically; unresolved → no link (FR-17).
- **`referenceSites`** (`internal/server/references.go` L242) inverts resolution via `edgeMatchesTarget`
  (L332: resolved-only, path+type match — dynamic/unresolved/ambiguous never match). `provideReferences`
  (L42) with a cursor on the `USING` name will hit the *edge* branch (L185), resolve it, and sweep all
  edges resolving to the same data-area path/type — collecting every `USING` site to that data area.
- **`provideTypeDefinition`** (`internal/server/type_definition.go` L24) maps the cursor via
  `findDeclarationTarget` (structure/VIEW-based) and only returns a location for a **VIEW-OF field**
  (`OwningView.ViewOfDDM != ""`). A `USING` name is not a VIEW field, so typeDefinition already returns
  `nil` there. The out-of-scope decision is thus the *current* behavior — T7 pins it with a regression test.
- **`call_hierarchy.go`** outgoing-call enumeration (L601) gates on `EdgeCalls || EdgeNavigatesTo ||
  EdgePerforms`; `USES_DATA_AREA` is excluded by construction. T7 pins it.

### Classification of each acceptance criterion

| AC | Classification | Notes |
|---|---|---|
| S3 AC1 (new edge kind + emit from `DataAreaRef`s) | **New** (additive constant + derive in `Analyze`) | The only genuinely new extraction work. |
| S3 AC2 (`Resolve` resolves the edge) | **Extend** | Add a case to the two `switch`es; reuse `resolveByName` × 3 namespaces (OQ-2 order). |
| S3 AC3 (cache bump `0.10.0`) | **Shared-contract change** | Persisted `Edges` shape gains a new kind value → migration = one-time rebuild; update version-bump test(s). |
| S3 AC4 (call hierarchy unaffected) | **Already satisfied** (guard only) | Excluded by the existing kind gate; add a regression test. |
| S1 AC1 (definition on `USING` name) | **Already satisfied by the seam** once T1+T2 land | `findCursorTarget`/`provideDefinition` need no change; add server tests + fixtures. |
| S1 AC2 (declaration) | **Already satisfied** (delegates) | Covered by the same fixtures. |
| S1 AC3 (modeled gaps → empty) | **Extend/verify** | Unresolvable/out-of-chain → `nil`; assert. |
| S2 AC1 (references) | **Already satisfied by the seam** once T1+T2 land | Reverse sweep via `edgeMatchesTarget`; add server tests. |
| S2 AC2 (document link) | **Already satisfied by the seam** once T1+T2 land | `buildDocumentLinks` iterates all resolved edges; add server tests. |
| Out-of-scope: typeDefinition stays empty | **Already satisfied** (guard only) | Pin with a regression test (T7). |

**Net:** the only *production* changes are (T1) the constant + a derive-and-re-sort block in `Analyze`,
(T2) a resolution case in two switches, and (T3) the version-constant bump. Everything else (definition/
declaration/references/documentLink) already flows through the seam and is pinned by tests + fixtures.

### Seam check

No violation. `internal/server` continues to depend only on `analysis.Analyzer` (via `findCursorTarget`'s
`ExtractVariableRefs`, unchanged) and the `workspace` public API (`ResolutionSet`, `Index`). No parser
internals cross the boundary. **`ResolveDataAreaPath` (the exported `ResolveDDMPath` analog mentioned in
the plan) is NOT required:** `resolution.go` calls the unexported `resolveByName` in-package, and the
server reads the outcome from the `ResolutionSet` (`res.Get`), not from a separate resolver. Do **not**
add the export unless a later task surfaces a concrete server need (see OQ-3).

---

## Fixtures

Reuse existing where possible; add minimal reproducers per the testing convention (sanitized, non-proprietary).

- **Reuse for T1 (extraction):** add one new program fixture with all three sections —
  `internal/analysis/natural/testdata/dataarea/using_edges.NSP` (a `DEFINE DATA` with `LOCAL USING
  LDACUST`, `PARAMETER USING PDAPARM`, `GLOBAL USING GDAGLOB` and a trivial body). Existing
  `testdata/dataarea/{local.NSL,parameter.NSA,global.NSG}` have **no** `USING` clause, so a new caller
  fixture is required. Also add a malformed-gap fixture line (`LOCAL USING` with no name) either inline
  in the test source or as `using_gap.NSP`.
- **Reuse for T2 & T4 (chain-aware resolution + definition):** the existing multi-library fixture
  `internal/server/testdata/multilib/` is ideal — `APP/CALLER.NSP` already contains `LOCAL USING CUSTLDA`,
  and `CUSTLDA.NSL` exists in both `COMMON/` (chain winner: `APP` steplibs `["COMMON"]`) and `ALT/`
  (unreachable copy; `ALT` sorts *before* `COMMON`, so a naive `candidates[0]` picks the WRONG file).
  This proves chain-aware resolution + unreachable-exclusion in one fixture. Extend `APP/CALLER.NSP` (or
  add a sibling `APP/USERALL.NSP`) so it also carries `PARAMETER USING` and `GLOBAL USING` clauses if the
  per-section positive cases need distinct data-area objects; add `COMMON/*.NSA` / `COMMON/*.NSG` as
  needed. Keep `.natural-lsp.toml` unchanged.
- **Reuse for T2 (flat-namespace Ambiguous):** the `internal/workspace/testdata/resolution/` tree (flat,
  no library map) — add two same-named same-type data areas in different subdirs (e.g.
  `dirA/SHARED.NSL` + `dirB/SHARED.NSL`) plus a caller `LOCAL USING SHARED` → asserts `Ambiguous` with 2
  candidates. (Cross-namespace collisions — one `.NSL`, one `.NSA` — resolve first-reachable-wins by the
  OQ-2 order, NOT ambiguous; add that as a second case in the same fixture set if a distinct dir keeps it
  minimal.)
- **Reuse for T5/T6 (references + document link):** the same `multilib/` fixtures (`APP/CALLER.NSP` +
  `COMMON/CUSTLDA.NSL`), optionally a second caller `APP/CALLER2.NSP` with `LOCAL USING CUSTLDA` to prove
  the reverse sweep lists **multiple** `USING` sites.
- **T3 (cache):** no fixture — the version-bump test fabricates a `CacheFile{Version:"0.9.0"}` in Go
  (feature 24 convention; gzip has no readable version substring, so `strings.Replace` on bytes is not used).
- **T7 (guards):** reuse `multilib/APP/CALLER.NSP` for the typeDefinition-empty and call-hierarchy-ignores
  assertions.

---

## Tasks (dependency-ordered, red → green → refactor)

### T1 — `model.EdgeUsesDataArea` constant + emit the edge (derived from `DataAreaRef`s)

**Pins:** Story 3 AC1. The extraction foundation the whole family builds on.

- **RED** (`tdd-red`): add failing analyzer tests in `internal/analysis/natural/data_test.go` (or a new
  `dataarea_edges_test.go`) that call `Analyze("…/using_edges.NSP", content)` and assert
  `result.Edges` contains **exactly three** `EdgeEntry` with `Kind == model.EdgeUsesDataArea`, one per
  section, each with:
  - `TargetName == "LDACUST"` / `"PDAPARM"` / `"GDAGLOB"` (upper-cased, matching the `DataAreaRef.Name`),
  - `Source ==` the corresponding `DataAreaRef.Range` (the name-token span, byte-exact) — assert equality
    against `result.DataAreaRefs[i].Range` so the edge `Source` is proven to match feature 27's field-
    resolution range (OQ-1 guarantee),
  - the combined `result.Edges` slice is in **global source order** (assert `Source.Start` non-decreasing
    across all edges, incl. any body edges).
  - **Modeled-gap:** a `DEFINE DATA LOCAL USING` with **no name** (fixture `using_gap.NSP` or inline) emits
    **no** `EdgeUsesDataArea` (no fabricated empty-`TargetName` edge) — mirrors `extractDataAreaRefs`'
    `section.Using == ""` skip.
- **GREEN** (`tdd-green`):
  - Add `EdgeUsesDataArea EdgeKind = "USES_DATA_AREA"` to the `const` block in
    `internal/model/model.go` (after L22), with a doc comment (`DEFINE DATA … USING <data-area>` — static
    module dependency, resolved like `INCLUDE`).
  - In `internal/analysis/natural/analyzer.go`, **after** `result.DataAreaRefs = extractDataAreaRefs(ast)`
    (L131): iterate `result.DataAreaRefs`, append one `EdgeEntry{Source: ref.Range, Target:
    model.Range{}, Kind: model.EdgeUsesDataArea, TargetName: ref.Name}` per ref to `result.Edges`, then
    re-sort `result.Edges` with the same `sort.SliceStable` + `sourceStartLess` comparator used at
    L106–111. (Do **not** re-walk the AST — OQ-1.) Structure extraction at L136 is unaffected (it reads
    `Definitions`/`DataAccess`, not `Edges`).
- **REFACTOR** (`tdd-refactor`): if the append+re-sort block is non-trivial, extract a tiny local helper
  `deriveDataAreaEdges([]model.DataAreaRef) []model.EdgeEntry` in `data.go` (keeps `analyzer.go` thin and
  gives the derivation a direct unit test). Keep names/ordering consistent with `extractDataAreaRefs`.
- **DoD:** three USES_DATA_AREA edges emitted with correct `Source`/`TargetName`; global source order
  preserved; malformed `USING` emits none; `just verify` green; new fixture(s) committed under
  `internal/analysis/natural/testdata/dataarea/`.

### T2 — `workspace.Resolve`/`ResolveInto` resolve `EdgeUsesDataArea` via the data-area namespace

**Pins:** Story 3 AC2. **Depends on T1** (the edge kind must exist).

- **RED** (`tdd-red`): failing tests in `internal/workspace/resolution_test.go`:
  1. **Chain positive + unreachable-exclusion** (multilib fixture): build the index over
     `internal/server/testdata/multilib/` with its `.natural-lsp.toml`; `Resolve`; assert the resolution
     for `APP/CALLER.NSP`'s `USING CUSTLDA` edge (key = `(relPath, edge.Source)`) is
     `Resolved(path == "COMMON/CUSTLDA.NSL", Type == model.ObjectLocalDataArea)` — **not** `ALT/CUSTLDA.NSL`
     (proves chain-aware, not `candidates[0]`).
  2. **Unresolvable** → `Unresolved(ReasonNoTarget)` for a `USING NOSUCHDA` (add to a caller or a small
     fixture); assert **no** ambiguity diagnostic is recorded (FR-17).
  3. **Flat-namespace ambiguity** (new `resolution/` fixture with two `SHARED.NSL`) → `Ambiguous` with 2
     sorted candidate paths + one `DiagnosticCodeAmbiguity` diagnostic on the referencing file.
  4. **`ResolveInto` parity:** mutate a caller (re-`Add`), `ResolveInto(rs, idx, cfg, changedPaths)`, and
     assert the merged set's `USES_DATA_AREA` outcome equals a full `Resolve` (the existing completeness
     invariant).
- **GREEN** (`tdd-green`):
  - Add a helper `resolveDataAreaEdge(targetName, filePath, edge, nameIndex, cfg, ambigDiagnostics)
    Resolution` in `resolution.go` that calls `resolveByName` for `model.ObjectLocalDataArea`, then
    `ObjectParameterDataArea`, then `ObjectGlobalDataArea` (OQ-2 order), returning the **first** outcome
    that `IsResolved()` **or** `IsAmbiguous()`; if all three are `Unresolved`, return
    `Unresolved(ReasonNoTarget)`. (This mirrors `resolveDataAreaCandidate`'s three-namespace order while
    producing the full `Resolved`/`Unresolved`/`Ambiguous` shape. Because `resolveByName` only appends an
    ambiguity diagnostic in the flat `default` case and we stop at the first non-`Unresolved`, we never
    double-emit.)
  - Add `case model.EdgeUsesDataArea:` to **both** switches — `Resolve` (before `default` at L629) and
    `ResolveInto` (before `default` at L805) — each calling `resolveDataAreaEdge` with that switch's
    `filePath`/`affectedPath`, `edge`, `nameIndex`, `cfg`, and `rs.ambigDiagnostics`/`newRS.ambigDiagnostics`.
- **REFACTOR** (`tdd-refactor`): the two duplicated switches are a divergence hazard now that a fifth kind
  is shared. If in scope, extract a single `resolveEdge(edge, filePath, nameIndex, cfg, ambig) Resolution`
  used by both `Resolve` and `ResolveInto`; otherwise leave a `// keep in sync with ResolveInto` comment
  on both cases. Do not change existing outcomes.
- **DoD:** all four resolution cases pass; ambiguity diagnostic present only for the flat multi-match;
  `ResolveInto` parity holds; `just verify` green; new `resolution/` fixture committed.

### T3 — Cache-format bump `0.9.0` → `0.10.0`

**Pins:** Story 3 AC3. **Depends on T1** (the persisted `Edges` now carry a new kind value). Independent of
T2 (resolution is recomputed on load, not persisted — OQ-1 of feature 07).

- **RED** (`tdd-red`): update the version-bump regression test in `internal/workspace/cache_test.go`
  (follow the `TestLoad_CacheVersionBumpedForFeature28` pattern, L2487): assert `cacheFormatVersion ==
  "0.10.0"`, and fabricate a `CacheFile{Version: "0.9.0", …}` in Go (NOT `strings.Replace`) whose entry's
  `Edges` includes a `USES_DATA_AREA` edge; assert `Load` at the new version treats it as **stale** (all
  files marked for rebuild). Add a save→load round-trip proving a `FileAnalysis` whose `Edges` carry a
  `USES_DATA_AREA` edge survives (the edge kind persists intact).
- **GREEN** (`tdd-green`): bump the constant in `internal/workspace/cache.go` (L27) to `"0.10.0"` and add
  a history line to the comment block (L23–26): `// 0.10.0: feature 36 — persists model.EdgeUsesDataArea
  edges (DEFINE DATA … USING navigation).`
- **REFACTOR** (`tdd-refactor`): none expected. Confirm the corrupt/old-cache rebuild tests
  (`TestBuildWithCache_CorruptCacheRebuilds`, `FuzzLoadCache`) still pass unchanged.
- **DoD:** stale `0.9.0` cache rebuilds once; round-trip preserves the new edge; `just verify` green.

### T4 — Definition (+ declaration delegation) on the `USING` name

**Pins:** Story 1 AC1, AC2, AC3. **Depends on T1+T2** (edge emitted + resolved). No production change
expected — this task is fixtures + server tests proving the seam carries it.

- **RED** (`tdd-red`): failing end-to-end tests in `internal/server/definition_test.go` (mirror
  `TestProvideDefinitionEndToEnd`, L128; build via `workspace.BuildWithCache(ctx, fixtureAbs, cfg, az,
  logger, "", nil, nil)` and construct a `handlerContext`):
  - **Positive (per section):** cursor on the `USING` name in `APP/CALLER.NSP`'s `LOCAL USING CUSTLDA`
    resolves to a single `Location` whose URI is `COMMON/CUSTLDA.NSL` (NOT `ALT/`) and whose `Range` is
    that object's root `Structure.SelectionRange` (the zero-width landing point at line 1). Add analogous
    `PARAMETER USING`/`GLOBAL USING` cases if distinct `.NSA`/`.NSG` objects are added (else at minimum
    one per namespace using dedicated data-area objects).
  - **Declaration delegation** (Story 1 AC2): the same cursor via `provideDeclaration` returns the
    identical `Location` (delegates to `provideDefinition`).
  - **Modeled gaps** (Story 1 AC3): cursor on an unresolvable `USING NOSUCHDA` → `nil`; cursor on a
    `USING` name whose only match is the out-of-chain `ALT` copy from a caller not in that chain → `nil`
    (no error, no panic). *(Note: a data-area `USING` name is always a literal identifier, so the "dynamic"
    gap is vacuous here; cover unresolvable + out-of-chain, and record the dynamic case as N/A.)*
- **GREEN** (`tdd-green`): expected to pass on the strength of T1+T2 with no `internal/server` change. If a
  test reveals a real defect (e.g. the `USING` name span does not contain the cursor, or the same-file
  branch mis-fires because the data area is external), fix it minimally in `definition.go`/`cursor.go` and
  document why.
- **REFACTOR** (`tdd-refactor`): none expected; tidy fixtures/test helpers only.
- **DoD:** definition + declaration land on the data-area object root for each section; gaps return `nil`;
  chain-correctness proven (COMMON not ALT); `just verify` green; fixtures committed under
  `internal/server/testdata/multilib/` (and any new data-area objects).

### T5 — References: a data area's `USING` sites are found

**Pins:** Story 2 AC1. **Depends on T1+T2.** No production change expected.

- **RED** (`tdd-red`): failing test in `internal/server/references_test.go` (build the `multilib/` index):
  add a second caller `APP/CALLER2.NSP` with `LOCAL USING CUSTLDA`; place the cursor on the `USING CUSTLDA`
  name in `APP/CALLER.NSP` and assert `provideReferences` returns Locations covering **both** callers'
  `USING` name spans (reverse sweep via `edgeMatchesTarget`, sorted by URI). With
  `IncludeDeclaration=true`, also expect the data-area object's own `Structure.SelectionRange`. Assert an
  **unresolved** `USING NOSUCHDA` site (from another file) is **excluded** (FR-17: dynamic/unresolved
  never match).
- **GREEN** (`tdd-green`): expected to pass unchanged (the edge branch at `references.go` L185 +
  `referenceSites` L242 handle it). Fix minimally only if a defect surfaces.
- **REFACTOR** (`tdd-refactor`): none expected.
- **DoD:** all `USING` sites to the data area are listed, unresolved excluded, deterministic order;
  `just verify` green.
- **Note (scope):** "find-references *on a data-area object*" (cursor on the object's own name in the
  `.NSL`) is **not** a path this feature adds — no module kind supports references from its own
  definition name today (references start from a *reference* site; `referenceSites` already adds the
  declaration via `IncludeDeclaration`). The reverse sweep from any `USING` site is the supported gesture
  and fully satisfies the intent ("list every `USING` reference to it"). Recorded as OQ-4.

### T6 — Document link on the resolved `USING` name

**Pins:** Story 2 AC2. **Depends on T1+T2.** No production change expected.

- **RED** (`tdd-red`): failing test in `internal/server/document_links_test.go` (mirror
  `TestProvideDocumentLink_ResolvedINCLUDE`, L123): over `multilib/`, assert `provideDocumentLink` for
  `APP/CALLER.NSP` returns a `DocumentLink` whose `Range` is the `USING CUSTLDA` name span (encoding-aware)
  and whose `Target` URI is `COMMON/CUSTLDA.NSL`. Add a gap case: an **unresolved** `USING` name → **no**
  link (part of the existing gaps test or a new one).
- **GREEN** (`tdd-green`): expected to pass unchanged (`buildDocumentLinks` L120 iterates all resolved
  edges; the data area is a different file so the same-file skip at L133 does not fire). Fix minimally only
  if a defect surfaces.
- **REFACTOR** (`tdd-refactor`): none expected.
- **DoD:** resolved `USING` name yields a link to the data-area object; unresolved yields none;
  `just verify` green.

### T7 — Guard tests: typeDefinition stays empty, call hierarchy ignores the edge, never-panic

**Pins:** Story 3 AC4 + the out-of-scope typeDefinition decision + FR-43. **Depends on T1+T2.**

- **RED** (`tdd-red`):
  - **typeDefinition empty** (`internal/server/type_definition_test.go`): cursor on a `USING` name →
    `provideTypeDefinition` returns `nil` (intentional; a data area is a module, not a type of the name —
    `findDeclarationTarget` yields no VIEW-OF field). Regression pin so the deliberate gap isn't "fixed" later.
  - **Call hierarchy ignores `USES_DATA_AREA`** (`internal/server/call_hierarchy_test.go`): from a caller
    with a `USING` clause, assert `outgoingCalls` contains **no** item for the data area (the kind gate at
    `call_hierarchy.go` L601 excludes it), and `prepareCallHierarchy` on the `USING` name yields no callable
    item.
  - **Never-panic** (`internal/analysis/natural`): assert extraction over a malformed/partial `DEFINE
    DATA … USING` (e.g. truncated) returns cleanly with no `USES_DATA_AREA` edge and no panic. Confirm the
    existing `FuzzParse` still passes (the derivation is a pure map over `DataAreaRefs`, introducing no new
    AST walk); optionally add a `USING`-clause seed to `FuzzParse`'s corpus.
- **GREEN** (`tdd-green`): expected to pass unchanged; fix minimally only if a guard fails.
- **REFACTOR** (`tdd-refactor`): none.
- **DoD:** typeDefinition empty on `USING`; call hierarchy excludes the edge; extraction never panics on
  malformed `USING`; `just verify` green.

---

## Traceability

| Story / AC | Requirement | Task(s) |
|---|---|---|
| S1 AC1 — definition on `USING` name → data-area object root, chain-aware | FR-24 | T1, T2, **T4** |
| S1 AC2 — declaration resolves the same (delegates) | FR-58 | **T4** |
| S1 AC3 — dynamic/unresolvable/out-of-chain → empty, no error | FR-17/FR-43 | **T4** |
| S2 AC1 — references lists every `USING` site (reverse sweep, unresolved excluded) | FR-25 | T1, T2, **T5** |
| S2 AC2 — document link over resolved `USING` name → object file; unresolved → none | FR-59/FR-17 | T1, T2, **T6** |
| S3 AC1 — `model.EdgeUsesDataArea` + emit from `DataAreaRef`s (Source/TargetName; global order) | — | **T1** |
| S3 AC2 — `Resolve` resolves via the data-area namespace (Resolved/Unresolved/Ambiguous) | FR-13/FR-31 | **T2** |
| S3 AC3 — cache bump `0.10.0`; stale cache rebuilds; fuzz never panics | — | **T3** (+ fuzz in T7) |
| S3 AC4 — call hierarchy unaffected | FR-49 | **T7** |
| Out-of-scope — typeDefinition on `USING` name stays empty | FR-17 | **T7** |

Every acceptance criterion maps to at least one task. No AC is left unaddressed.

---

## Open questions

- **OQ-1 (resolved — where to emit the edge):** derive `EdgeUsesDataArea` from the already-computed
  `result.DataAreaRefs` in `Analyze`, append to `result.Edges`, and re-sort by `Source.Start` (do NOT
  re-walk the AST). This guarantees the edge `Source` is byte-identical to the `DataAreaRef.Range` that
  feature 27 uses for field resolution, and reuses the existing append-then-re-sort pattern. **Decided:
  derive from `DataAreaRef`s** (per plan recommendation; pinned by a T1 assertion `edge.Source ==
  DataAreaRef.Range`).
- **OQ-2 (resolved — resolved object type / namespace order):** keep the existing three-namespace order
  `ObjectLocalDataArea → ObjectParameterDataArea → ObjectGlobalDataArea` (first-reachable-wins via the
  steplib chain), matching `resolveDataAreaCandidate` and feature 27 field resolution, rather than
  narrowing by `DataAreaRef.SectionKind`. This mirrors real Natural (a `LOCAL USING` may reference an
  `.NSL`, but the object namespace is shared) and avoids missing a validly-reachable object. **Decided:
  keep the existing order.**
- **OQ-3 (new — `ResolveDataAreaPath` export):** the plan says "export a `ResolveDataAreaPath` analog of
  `ResolveDDMPath` *if the server/resolution needs it*." The survey shows it is **not** needed:
  `resolution.go` reuses `resolveByName` in-package, and the server reads `res.Get`. Recommend **not**
  adding the export (avoid dead public API / seam surface). Flag for the reviewer to confirm no later task
  (e.g. a future server-side direct-resolve path) surfaces a need.
- **OQ-4 (new — references *on the data-area object's own name*):** Story 2 AC1's phrasing "on a data-area
  object (or from a `USING` site)" — this feature supports the **from-a-`USING`-site** direction (reverse
  sweep collects every `USING` reference to the data area, matching how CALLNAT/INCLUDE references work
  today). Cursor-on-the-object's-own-name references is not a path any module kind supports currently and
  is out of scope here. Confirm this reading satisfies the AC (it satisfies the stated intent: "includes
  every `USING` reference to it across the workspace"), else it becomes a separate follow-up.
- **OQ-5 (new — duplicated resolution switches):** `Resolve` and `ResolveInto` each carry a duplicated
  per-edge `switch`. Adding a fifth shared kind raises the divergence risk. Should T2's refactor phase
  extract a single shared `resolveEdge(...)` helper (cleaner, removes the hazard), or is a
  `// keep in sync` comment + adding the case to both sufficient for this bug-fix's scope? Recommend the
  extraction if it stays behavior-preserving; defer if it balloons the diff.

---

## Reviews required (for `/review-feature`)

- **Analyzer-seam guard:** confirm no new `internal/server` dependency on parser internals; `internal/model`
  change is the single additive constant only.
- **Contract/cache reviewer:** the `0.9.0 → 0.10.0` bump + the migration (one-time rebuild) is the only
  persisted-shape change; version-bump test present; corrupt/old-cache rebuild tests still pass.
- **Resolution correctness:** chain-aware (COMMON not ALT), non-transitive, flat-namespace ambiguity
  diagnostic, `ResolveInto` completeness parity; OQ-2 order honored.
- **FR-17/FR-43:** modeled gaps (unresolvable/out-of-chain/malformed `USING`) stay off the error/diagnostic
  channels and never panic; typeDefinition-empty and call-hierarchy-exclusion pinned.
- **review-docs:** `CLAUDE.md` "Project state" + cache-version history + README feature set updated to
  reflect `USES_DATA_AREA` navigation and cache `0.10.0` before merge.

## Checkpoint decisions (approved)
- **OQ-1:** derive `EdgeUsesDataArea` from `DataAreaRef`s (not a fresh AST walk). ✅
- **OQ-2:** keep the three-namespace resolver order (`.NSL`→`.NSA`→`.NSG`, first-reachable-wins). ✅
- **OQ-3:** do NOT add a `ResolveDataAreaPath` export — resolution reuses in-package `resolveByName`. ✅
- **OQ-4:** find-references works from a `USING` site (reverse sweep), consistent with all module kinds — satisfies Story 2 AC1. ✅
- **OQ-5 (user decision):** **extract a shared `resolveEdge` helper** so `Resolve` and `ResolveInto` share one per-edge resolver (removes the divergence hazard), rather than adding the case to both switches. T2 includes this refactor, guarded by the existing resolution tests (must stay green — behavior-preserving for all existing edge kinds).
- **Dynamic gap:** N/A (a `USING` name is always a literal identifier).
