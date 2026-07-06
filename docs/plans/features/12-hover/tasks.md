# Feature 12 — Hover: TDD task decomposition

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements:** FR-28 (hover). Also holds FR-17 (modeled gaps stay off the diagnostic channel),
FR-43 (graceful degradation), ADR-008 (encoding-aware position/range conversion), F7 (build-then-publish
snapshot discipline).
**Depends on (shipped):** feature 07 (resolution), feature 08 (data-access + `DataDefinition` w/
`SectionKind`), feature 09 (`model.Symbol` structure tree), feature 10 (first LSP providers, `handlerContext`
holding `idx`+`res` under `RWMutex`, `position.go`, `cursor.go`, `referenceSites` sweep), feature 11
(`document_symbols.go` provider pattern, store-first fallback).

Wiring the third real LSP provider, `textDocument/hover`. This mirrors features 10/11 on the LSP side: no
`internal/model` change, no `Analyzer`-interface change, and **no cache-format bump** (stays `0.6.0`) —
hover reads the already-extracted `FileAnalysis` fields (`Edges`, `DataAccess`, `Structure`, `Definitions`)
and the live `Index`/`ResolutionSet` snapshots. Provider code lives in `internal/server/` (LSP-facing, on
the presentation side of the Analyzer seam), depending only on `internal/model`/`internal/workspace`.

### Scope change (approved 2026-07-06) — DDM field extraction added

During T3 (DDM hover) it was found that the Natural analyzer extracts **zero field `Definitions`** from
`.NSD` files — it only classifies the object as type `ddm`. Story 3 AC #1 (show DDM field name(s)/type(s))
therefore had **no source of field data**. The user chose to **add `.NSD` tabular parsing** rather than
descope to name-only. This adds one **analysis-layer** task (**T2A**) below: a dedicated fixed-column
line-scanner in `internal/analysis/natural/` that parses the exported DDM report format into
`FileAnalysis.Definitions`, wired into `Analyze` for `.NSD` files. Verified against natls'
`parsing/ddm/FieldParser.java` + Software AG's DDM Editor docs (see
`.claude/knowledge/natural/ddm-format.md`): the format is a **fixed-byte-offset** table (`T L DB Name … F
Leng S D Remark`), NOT Natural source — it must NOT be routed through the statement lexer/parser. The
extraction maps cleanly onto the existing `model.DataDefinition` (`Name`, `Level`, verbatim `Type` like
`"A50"`/`"N8"`/`"P9,2"`, group nesting via `Children`, MU/PE arrays via an unbounded `*` `Dimensions`
entry) — so **still no `model` change and no cache-format bump**; the DB short-name, descriptor-kind, and
suppression columns are dropped for now (not needed for name/type hover). SQL DDMs (`TYPE: SQL`, different
column layout) are out of scope. The seam still holds — the DDM scanner is a parser-backend internal
behind the `Analyzer`; the server provider still touches only `model`/`workspace`.

---

## Current-state findings & impact

### What already exists and is reused

- **`internal/server/hover.go` is a stub** — a package-doc + `// TODO: hover builders.` at
  `internal/analysis/natural/hover.go`. Note the misplacement: the stub sits under `analysis/natural`, but
  the `model.Symbol`→`protocol` conversion for document symbols was deliberately relocated to
  `internal/server/` in feature 11 (the old `analysis/natural/symbols.go` stub was deleted for exactly
  this reason — the CLAUDE.md note records it). **The hover provider must live in
  `internal/server/hover.go`; the `analysis/natural/hover.go` stub should be deleted** (mirror feature
  11's symbols.go cleanup). This is a code/doc divergence to flag: the stub's current location contradicts
  the seam discipline established in feature 11.
- **Cursor lookup — reuse verbatim.** `internal/server/cursor.go` `findCursorTarget(fa, pos)` already maps
  a 1-based `model.Position` to the `*EdgeEntry` (by `Source`) or `*DataAccessEntry` (by `NameRange`) under
  the cursor, smallest-containing-range tie-break. The three hover stories map exactly onto its two return
  values: EdgeEntry (module refs + PERFORM sites) and DataAccessEntry (DDM refs). No change needed.
- **Position/range conversion — reuse verbatim.** `internal/server/position.go`
  `fromProtocolPosition`/`toProtocolRange` (ADR-008). Hover reuses `fromProtocolPosition` to decode the
  cursor and `toProtocolRange` to build the optional `Hover.Range`.
- **Resolution — reuse verbatim.** `res.Get(relPath, edge.Source)` returns a `workspace.Resolution` with
  `IsResolved()`/`IsUnresolved()`/`IsAmbiguous()`/`IsDynamic()` predicates and `Path`/`Type`/`Candidates`
  fields. This drives Story 1 (module metadata) and Story 2 (inline-vs-external PERFORM — resolution
  already prefers an inline `DEFINE SUBROUTINE` before an external `.NSS`, so hover inherits the correct
  "what would actually be performed" semantics for free).
- **Reverse sweep — reuse for inbound count (Story 1).** `internal/server/references.go` `referenceSites`
  already walks the whole index and returns every site whose resolved target matches a `(targetPath,
  targetName, targetType)` identity, honoring FR-17 (dynamic/unresolved excluded). The inbound call count
  = `len(referenceSites(..., includeDeclaration=false, ...))`. **Reuse it** rather than writing a second
  reverse walk. (Story 1 AC: the count "updates with incremental re-analysis" — this holds automatically
  because `referenceSites` reads the live `idx`/`res` snapshot that `applyDocumentChange` swaps in.)
- **Provider handler pattern — mirror.** `provideDocumentSymbols` (feature 11) is the closest template:
  it does the store-first / index-fallback resolution, snapshots under `RLock`, releases before I/O (F7),
  and returns `nil, nil` on any not-found/degenerate case. `provideDefinition`/`provideReferences` show
  the `uriToRelPath` + `os.ReadFile` + `fromProtocolPosition` + `findCursorTarget` + `res.Get` sequence.
- **`uriToRelPath` helper** (in `definition.go`) — reuse for URI→(abs, rel) conversion.
- **`Definitions` with `SectionKind`** — feature 08 populates `FileAnalysis.Definitions` with
  `SectionKind` lowercase (`"parameter"`, `"local"`, `"global"`, …), `Level`, `Type`, `Dimensions`, and
  `Children` (verified in `data.go:152` and `analyzer_test.go`). Story 2's signature is built by filtering
  the target subprogram/subroutine's `Definitions` to `SectionKind == "parameter"`. No extraction change.

### Shared contracts — none change

Confirmed against `internal/model/model.go`: hover needs no new field. `EdgeEntry`, `DataAccessEntry`,
`DataDefinition` (already documented as "for use in hover"), and `Symbol` all carry what hover renders.
**No `internal/model` change, no `Analyzer`-interface change, no cache-format bump** — so no consumer
migration and no `review-seam`. (This matches features 10 and 11.)

### Server capability — one locked-allow-list change

`handleInitialize` (`server.go:136`) advertises the capability set as a "deliberately locked allow-list."
Hover must add `HoverProvider: protocol.Boolean(true)` (verified: `Boolean` satisfies the `HoverProvider`
union in `types_unions.gen.go:2172`). **`TestInitialize` (`server_test.go:490`+) must be updated**: move
`"hoverProvider"` out of the `otherProviderFlags` (asserted absent) list and into the `requiredProviders`
(asserted present) list. The test comment already anticipates this ("When features 12–13 add further
providers (hover, …), they MUST update TestInitialize"). This is a deliberate, explicit change, not drift.

### LSP result shape (verified against `go.lsp.dev/protocol@v1.0.0`)

- Request: `textDocument/hover`, params `protocol.HoverParams` (embeds `TextDocumentPositionParams`).
- Response: `*protocol.Hover` with `Contents HoverContents` and optional `Range *Range`. `HoverContents`
  is a union; `*protocol.MarkupContent{Kind: MarkupKindMarkdown, Value: "..."}` satisfies it
  (`types_unions.gen.go:1714`). A no-hover result is JSON `null` (return `nil, nil` from the provider,
  marshal to `null` — same pattern as `provideDocumentSymbols`/`provideDefinition`).

### Criteria already satisfied / out of scope

- Story 2 AC "reflects inline-before-external resolution" — **already satisfied by feature 07's resolver**;
  hover only has to read `res.Get(...)` and render whatever it resolved to (a same-file subroutine vs an
  external `.NSS`). Covered incidentally by T4/T5, no dedicated foundation task.
- Story 3 AC "when physical Adabas/IMS metadata isn't available, show what's known from source and don't
  fabricate" — hover renders only source-derived DDM field info (from the referenced `.NSD`'s
  `Definitions`/`Structure` if indexed, else the view name alone). Physical metadata is explicitly out of
  scope (plan "Out of scope").

### Open-question resolutions (proposed defaults — flag to confirm; see Open questions)

- **OQ-A (card content/formatting):** use **Markdown** (`MarkupKindMarkdown`) with a small, fixed layout
  per target kind (defined per task below). Markdown is the richest format editors render and degrades to
  plaintext-ish display in clients that don't; the natls-style convention is a bolded title line + detail
  lines. Content builders are **pure functions** returning a Markdown string, so formatting is unit-tested
  independent of the handler.
- **OQ-B (outbound summaries):** **include a one-line outbound summary** in the module-metadata card
  (Story 1) — the count of outbound edges (calls/performs/includes) declared *in* the hovered target —
  because it's cheap (read the target `FileAnalysis.Edges`) and directly answers "what does this module
  depend on." Kept to a single count line, not a full list, to avoid noise. Flagged as a decision.

---

## Ordered task list

Ordering: pure content-builder primitives first (testable without a handler), then the handler that wires
them, then capability advertisement + dispatch, then the fuzz guard. Each task is one red→green→refactor
loop unless noted.

---

### T1 — Markdown builder: module-metadata card (Story 1)

**Behavior:** A pure function (e.g. `buildModuleHover(targetName string, targetType model.ObjectType,
targetPath string, inboundCount, outboundCount int) string`) returns a Markdown string for a resolved
module target: a title line with the module name and its object-type label, a location line
(workspace-relative path), an inbound-call-count line, and a one-line outbound-dependency count (OQ-B).
For an **unresolved/dynamic** target, a separate branch (or a sibling builder) returns a sensible message
("Unresolved reference — target could not be located" / "Dynamic call target — resolved at runtime") with
**no fabricated metadata** (Story 1 AC #3, FR-17). No I/O, no locks.

**Fixtures:** none — pure function, table-driven inputs.

**Expected result:** deterministic Markdown for (resolved program, resolved subprogram, unresolved,
dynamic) input rows; object-type label derives from `model.ObjectType` (map to a human string, e.g.
`ObjectSubprogram`→"subprogram"). Counts render literally (`0` shown, not omitted).

**Reuses/migrates:** `model.ObjectType` constants. New file `internal/server/hover.go` (created here;
delete `internal/analysis/natural/hover.go` in this task).

**DoD:**
- [ ] Table-driven test covering resolved/unresolved/dynamic and count formatting.
- [ ] `internal/analysis/natural/hover.go` stub deleted; new `internal/server/hover.go` holds the builder.
- [ ] Pure (no I/O/locks); deterministic output.
- [ ] `gofmt`/`go vet` clean; `just test` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** none.

---

### T2 — Markdown builder: subroutine-signature card (Story 2)

**Behavior:** A pure function (e.g. `buildSubroutineHover(name string, params []model.DataDefinition)
string`) renders a subroutine/subprogram signature: a title line with the routine name and a parameter
list built from the `SectionKind == "parameter"` definitions (name, level, type; array `Dimensions`
rendered as `(lower:upper)` / `(lower:*)`; group headers with no `Type` shown as nesting parents). When
there is no PARAMETER section, render a "no declared parameters" line rather than an empty card.

**Fixtures:** reuse `internal/analysis/natural/testdata/structure/02-subprogram-params.NSN` (PARAMETER
section with scalar + nested group fields) — analyze it in the test to obtain real `Definitions`, then
assert the rendered card. (No new fixture; this file already exercises scalar + group + array-free params.
If an array param is needed for dimension rendering, add one minimal `.NSN` fixture under
`internal/server/testdata/hover/` — split into its own row, don't overload the reused fixture.)

**Expected result:** Markdown listing each parameter with its type/format, in declaration order (feature
08 preserves order); nested group children indented/nested under their parent.

**Reuses/migrates:** `model.DataDefinition` (`SectionKind`, `Level`, `Type`, `Dimensions`, `Children`).

**DoD:**
- [ ] Table-driven test over analyzed fixture(s): with-params and no-params cases.
- [ ] Array dimension rendering covered (bounded and unbounded `*`).
- [ ] Pure; deterministic; declaration order preserved.
- [ ] `gofmt`/`go vet` clean; `just test` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** T1 (shares `hover.go`).

---

### T2A — DDM tabular parser: extract `.NSD` fields into `Definitions` (Story 3 enabler)

**Behavior:** A dedicated fixed-column line-scanner in a new `internal/analysis/natural/ddm.go` (e.g.
`extractDDMDefinitions(content string) []model.DataDefinition` or a small `parseDDM`) that reads the
exported DDM report format and populates `FileAnalysis.Definitions`. Wired into `Analyze` **only** for
`.NSD` objects (`ObjectType == ObjectDDM`), gated on `TYPE: ADABAS`/absent (skip `TYPE: SQL`). It must NOT
go through the Natural lexer/recursive-descent parser. Per the verified spec
(`.claude/knowledge/natural/ddm-format.md`): parse by **0-based byte offsets** — T@0 (` `/`G`/`P`/`M`/`C`),
L@2 (level), DB@4 (2-char, dropped), Name@7 (len 32), F@41 (format), Leng@43 (len 4, accept comma-decimal
`9,2`), S@49 (dropped), D@51 (dropped), Remark@53+. Skip blank lines, `*` comments (except a
superdescriptor's `SOURCE FIELD(S)` block, which may be ignored for now), the `DB:`/`TYPE:` headers, the
`T L DB Name…` column header + dashed separator, and Predict/terminator noise
(`******DDM OUTPUT TERMINATED******`, `Cataloged by`, `EM=`, `HD=`, etc.). Build the tree by **level
containment** (a `G`/`P` group's children are the following higher-level rows), exactly like the existing
`DEFINE DATA` group nesting. Map each field to `model.DataDefinition`: `Name` (upper-case long name),
`Level`, verbatim `Type` (`"N8"`, `"A50"`, `"P9,2"`), `Children` for groups, and a single **unbounded** `*`
`Dimensions` entry for `M`(MU)/`P`(PE) array fields. `Range` = the field's line (1-based, byte-column,
inclusive-end). Tolerate trailing-whitespace-trimmed short rows (bare group lines) by treating missing
columns as blank. **No `model`/cache-format change.**

**Fixtures:** the byte-correct `internal/server/testdata/hover/customer.NSD` already written by the
natural-expert (CUSTOMER-ID N8 unique, CUSTOMER-NAME A50 descriptor, ADDRESS group → STREET/CITY/ZIP-CODE,
BALANCE P9,2, PHONE A20 MU, NAME-CITY-SUPER superdescriptor). Also add a copy (or the primary regression
fixture) under `internal/analysis/natural/testdata/ddm/` per the testdata regression convention, plus at
least one degenerate fixture (empty/headers-only, and a `TYPE: SQL` skip case).

**Expected result:** analyzing `customer.NSD` yields `Definitions` containing CUSTOMER-ID (`N8`),
CUSTOMER-NAME (`A50`), ADDRESS (group, no `Type`) with STREET/CITY/ZIP-CODE children, BALANCE (`P9,2`),
PHONE (`A20`, unbounded `*` dimension), NAME-CITY-SUPER (`A80`), in source order, correctly nested. A
`TYPE: SQL` DDM yields no `Definitions` (skipped). Malformed/short rows never panic.

**Reuses/migrates:** `model.DataDefinition` (no change), `model.ObjectDDM`, `Analyze` wiring. New file
`internal/analysis/natural/ddm.go`.

**DoD:**
- [ ] Table-driven test over `customer.NSD`: field names, verbatim types, group nesting, MU array dim.
- [ ] `TYPE: SQL` and header-only inputs → no definitions, no panic.
- [ ] Wired into `Analyze` for `ObjectDDM` only; other object types unaffected (existing tests green).
- [ ] Line-scanner, NOT the statement parser; seam intact (analysis-backend internal).
- [ ] `FuzzParseDDM` (or extend an existing fuzz target) — never panics over arbitrary bytes (FR-43).
- [ ] `gofmt`/`go vet` clean; `just test -race` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** none (independent of T1/T2). Must land
before T3 green.

---

### T3 — Markdown builder: DDM field-details card (Story 3)

**Behavior:** A pure function (e.g. `buildDDMHover(viewName string, ddmFA *model.FileAnalysis) string`)
renders DDM/view field info for a data-access site. When the referenced DDM (`.NSD`) is indexed, render its
field name(s)/type(s) from the DDM file's `Definitions` (or `Structure` if that's the richer source — pick
one and document it). When the DDM is **not** indexed (physical metadata unavailable — Adabas/IMS), render
only the view name and an honest "field details unavailable from source" line — **no fabrication** (Story 3
AC #2, FR-17). Empty-`Name` data-access sites (feature 08 record-form gap) yield `nil`/no card.

**Fixtures:** a minimal `.NSD` DDM fixture + a `.NSP` that reads it, under
`internal/server/testdata/hover/` (e.g. `customer.NSD` + `reader.NSP` with `READ CUSTOMER`). Sanitized,
non-proprietary. One fixture pair; the "not indexed" case is exercised by omitting the `.NSD` from the
index in the test, not by a second fixture.

**Expected result:** Markdown with the view name and its known fields (indexed case); view-name-only with
the honest-unavailable line (not-indexed case).

**Reuses/migrates:** `model.FileAnalysis.Definitions`/`Structure` of the DDM object; `model.DataAccessEntry`.

**DoD:**
- [ ] Table-driven test: DDM-indexed (fields shown) and DDM-absent (view-name-only, no fabrication).
- [ ] Empty-`Name` data-access entry → no card (FR-17 modeled gap).
- [ ] Pure; deterministic.
- [ ] `gofmt`/`go vet` clean; `just test` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** T1, **T2A** (the indexed-fields case is
only satisfiable once `.NSD` parsing populates `Definitions`). The RED test's field-name assertions must
match the real `customer.NSD` fixture (CUSTOMER-ID / CUSTOMER-NAME / ADDRESS group / BALANCE / PHONE /
NAME-CITY-SUPER), not the placeholder `CUST-ID`/`CUST-NAME`.

---

### T4 — `provideHover` handler: module & PERFORM targets (Stories 1 & 2)

**Behavior:** `provideHover(hctx *handlerContext, params protocol.HoverParams) (*protocol.Hover, error)`.
Guard `hctx == nil`; snapshot `idx, res` under `RLock`, release before I/O (F7); `uriToRelPath`;
`idx.Get(relPath)`; `os.ReadFile` the source; `fromProtocolPosition` to decode the cursor;
`findCursorTarget`. When an **EdgeEntry** is under the cursor: `res.Get(relPath, edge.Source)`, then:
- resolved module (CALLNAT/FETCH/RUN → program/subprogram/…): compute inbound count via
  `referenceSites(idx, res, root, resolution.Path, edge.TargetName, resolution.Type, false, enc)` (len),
  compute outbound count from the target file's `Edges`, call `buildModuleHover` (T1);
- resolved PERFORM to a subroutine: locate the target routine's PARAMETER `Definitions` (same-file inline
  subroutine or external `.NSS` per `resolution.Path`) and call `buildSubroutineHover` (T2). For an inline
  subroutine with no separate PARAMETER section, fall back to the module/subroutine name card;
- unresolved/dynamic/ambiguous: `buildModuleHover`'s unresolved branch (T1) — sensible message, no
  fabrication (FR-17).
Set `Hover.Range` to `toProtocolRange(edge.Source, ...)` (or the target-name sub-range if available) so
the client highlights the reference. Return `nil, nil` for no cursor target / not-in-index / unreadable
(FR-43).

**Fixtures:** reuse `internal/server/testdata/navigation/{caller.NSP, helper.NSN, unresolved.NSP}` —
`caller.NSP` has `CALLNAT 'HELPER'` (→ resolved subprogram, Story 1) and `PERFORM INLINE-SUB` (inline
subroutine, Story 2); `helper.NSN` has a PARAMETER section; `unresolved.NSP` covers dynamic + no-target.
Build a multi-file `handlerContext` exactly as `definition_test.go`/`references_test.go` do (analyze,
`idx.Add`, `workspace.Resolve`, temp root with files written).

**Expected result:** hover on `CALLNAT 'HELPER'` → module card naming HELPER, its path, inbound count ≥1
(the caller site), outbound count; hover on `PERFORM INLINE-SUB` → subroutine card; hover on
`CALLNAT #SUB-NAME` / `CALLNAT 'MISSING'` → unresolved/dynamic message, no fabricated metadata; hover on
whitespace / out-of-range → `nil`.

**Reuses/migrates:** `findCursorTarget`, `uriToRelPath`, `fromProtocolPosition`, `toProtocolRange`,
`res.Get`, `referenceSites`, builders T1/T2. Handler shape mirrors `provideDocumentSymbols`.

**DoD:**
- [ ] Table-driven handler test over the reused fixtures (resolved module, inline PERFORM, dynamic,
      no-target, no-cursor-target).
- [ ] Inbound count reuses `referenceSites` (no duplicate reverse walk).
- [ ] F7: `RLock` snapshot released before `os.ReadFile`; run with `-race`.
- [ ] Returns `nil, nil` (→ `null`) on all not-found/degenerate paths (FR-43).
- [ ] `gofmt`/`go vet` clean; `just test -race` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** T1, T2.

---

### T5 — `provideHover` handler: data-access / DDM targets (Story 3)

**Behavior:** Extend `provideHover` for the **DataAccessEntry** branch of `findCursorTarget`: look up the
referenced DDM in the index by name (name-based, mirroring `references.go`'s current DDM matching — DDM
resolution is still future work), pass the DDM's `FileAnalysis` (or `nil` if absent) plus the view name to
`buildDDMHover` (T3). Set `Hover.Range` to `toProtocolRange(dataAccess.NameRange, ...)`. Empty-`Name`
entries → `nil, nil`.

**Fixtures:** the T3 `customer.NSD` + `reader.NSP` pair (add `reader.NSP` to the handler-test workspace).

**Expected result:** hover on the `CUSTOMER` view name in `READ CUSTOMER` → DDM field card (DDM indexed);
same with the `.NSD` omitted from the index → view-name-only honest card; hover on a record-form
empty-`Name` write site → `nil`.

**Reuses/migrates:** `buildDDMHover` (T3), `findCursorTarget` data-access branch, DDM name-match approach
from `references.go`.

**DoD:**
- [ ] Handler test: DDM-indexed and DDM-absent data-access hover; empty-`Name` → nil.
- [ ] `Hover.Range` uses `NameRange` (points at the view name, not the whole statement).
- [ ] F7 (`-race`); FR-43 nil-safety.
- [ ] `gofmt`/`go vet` clean; `just test -race` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** T3, T4.

---

### T6 — Advertise `hoverProvider` + route `textDocument/hover` (FR-28 wiring)

**Behavior:** (a) In `handleInitialize`, add `HoverProvider: protocol.Boolean(true)` to
`ServerCapabilities` and update the comment. (b) In the dispatch switch (`server.go`), add a
`case "textDocument/hover":` that gates on `stateInitialized`, decodes `protocol.HoverParams`, calls
`provideHover`, and marshals `*Hover` to JSON (or `null` when nil), mirroring the
`textDocument/documentSymbol` case exactly (including the `sendError` paths for `InvalidParams` /
`InternalError`). (c) **Update `TestInitialize`**: move `"hoverProvider"` from `otherProviderFlags` into
`requiredProviders`.

**Fixtures:** none (server-level test drives the JSON-RPC handshake).

**Expected result:** `initialize` response advertises `hoverProvider: true`; a `textDocument/hover` request
before `initialized` returns `ServerNotInitialized`; a well-formed request after `initialized` returns a
`Hover` (or `null`); malformed params return `InvalidParams`.

**Reuses/migrates:** `handleInitialize`, the dispatch switch, `TestInitialize` — all in `server.go` /
`server_test.go`.

**DoD:**
- [ ] `TestInitialize` updated (hover now required, allow-list still locked otherwise) and green.
- [ ] End-to-end server test: hover request routed, gated on lifecycle, marshals correctly.
- [ ] `null` returned for a no-hover result; `InvalidParams`/`ServerNotInitialized` paths asserted.
- [ ] Per-request panic recovery still returns `InternalError` (existing behavior, unchanged).
- [ ] `gofmt`/`go vet` clean; `just verify` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** T4, T5.

---

### T7 — Fuzz guard: `FuzzProvideHover` (FR-43)

**Behavior:** Add `FuzzProvideHover` to `internal/server/fuzz_test.go`, mirroring
`FuzzProvideDefinition`: analyze arbitrary input, build a minimal single-file `handlerContext`
(`idx.Add`, `workspace.Resolve`, temp root, file written), call `provideHover` at a spread of arbitrary /
out-of-range cursor positions and both encodings, and assert **no panic** and a well-formed
`*Hover`-or-`nil` result. Seed from the hover/navigation fixtures plus the hand-written edge-case seeds
(empty, bare CALLNAT, dynamic, malformed DEFINE DATA, data-access statements). Optionally also fuzz the
pure builders (T1–T3) directly against degenerate `Definitions`/`FileAnalysis`.

**Fixtures:** seed from `testdata/navigation/*` and `testdata/hover/*`.

**Expected result:** fuzzer runs clean (no panic) over the corpus; well-formedness assertions hold.

**Reuses/migrates:** `FuzzProvideDefinition` structure.

**DoD:**
- [ ] `FuzzProvideHover` added and passes a short `-run` smoke plus a brief `-fuzz` burst locally.
- [ ] Never panics; returns nil or a valid `*Hover`.
- [ ] `gofmt`/`go vet` clean; `just verify` green.

**TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`. **Deps:** T4, T5, T6.

---

## Reviews required (for `/review-feature`)

- **`review-protocol`** — a new LSP method (`textDocument/hover`) and capability are added; verify the
  `Hover`/`HoverContents` shape, `null` result semantics, lifecycle gating, and the locked-allow-list
  update.
- **`review-concurrency`** — the handler reads the `idx`/`res` snapshot under `RLock` and must release
  before I/O (F7); confirm no lock held across `os.ReadFile` and that `referenceSites` reuse doesn't
  re-enter the lock.
- **`review-robustness`** — FR-43: hover over arbitrary cursor positions / malformed source / absent
  index entries must never panic (the fuzz target is the executable proof); FR-17 modeled gaps
  (dynamic/unresolved/empty-`Name`) must render honest messages, never fabricated metadata.
- **`review-docs`** — capability set changed (hover now advertised); the CLAUDE.md "Project state" note
  (currently "hover.go is package-doc + TODO only" and "remaining higher-level LSP providers … remain
  unwired") and README feature/capability list must sync at `/finalize-feature`, including the moved
  `hover.go` location.
- **No `review-seam`** — no shared contract (`internal/model`, `Analyzer`, index API) changes, no
  cache-format bump.

---

## Open questions (decisions to confirm)

1. **OQ-A — hover card content/formatting.** Proposed default: **Markdown**, per-kind fixed layout
   (module card = name+type / path / inbound count / outbound count; subroutine card = name + parameter
   list with type/level/array dims; DDM card = view name + field list, or honest-unavailable line).
   Confirm Markdown vs plaintext and the exact fields per card.
2. **OQ-B — outbound dependency summary.** Proposed default: **include a single outbound-count line** in
   the module card (count of the target's own calls/performs/includes), not a full dependency list.
   Confirm whether to include it, and whether a count suffices or a short list is wanted.
3. **Hover.Range highlight granularity.** Proposed default: highlight the reference site — `EdgeEntry.Source`
   for calls (or the target-name sub-range if cheaply available), `DataAccessEntry.NameRange` for DDM refs.
   Confirm whether whole-statement or name-only highlighting is preferred.
4. **DDM field source.** For Story 3, confirm whether the DDM field list should be read from the DDM
   object's `Definitions` or its `Structure` tree (T3 picks one and documents it — flag if the other is
   preferred).
