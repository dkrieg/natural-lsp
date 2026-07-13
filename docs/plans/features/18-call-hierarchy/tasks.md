# Tasks: Feature 18 — Call Hierarchy

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-49 (call hierarchy — `textDocument/prepareCallHierarchy`,
`callHierarchy/incomingCalls`, `callHierarchy/outgoingCalls`); FR-11/FR-17 (modeled gaps:
dynamic/unresolved never become false edges); FR-43 (never-panic / graceful degradation).
**Depends on:** features 05 (index), 07 (resolution + steplib chain), 09 (structure/symbol tree),
and especially 10 (references reverse-sweep, cursor lookup, position conversion, F7 snapshot).

This is the **last planned LSP provider**. It is **server-side LSP wiring only** — read-only queries
over existing `Edges`, `ResolutionSet`, and `Structure`. **No `internal/model` change, no
cache-format bump** (stays `0.6.0`) — like features 10/11/12/13/16/17. It does **not** cross the
Analyzer seam (LSP-facing `internal/server` depends only on `internal/model` + `internal/workspace`).

---

## Current-state findings & impact

Verified by reading the source (not the README) on the current `main`. Build on these; do not re-derive.

### Provider wiring & dispatch (`internal/server/server.go`)
- Each provider is a `case "<method>":` in the big dispatch `switch` inside `Run` (the goroutine that
  produces `respResult`). Every request handler **gates on `state != stateInitialized`** →
  `sendError(..., jsonrpc2.ServerNotInitialized, ...)`, decodes params via
  `params.UnmarshalJSONFrom(jsontext.NewDecoder(...))`, calls its `provideXxx(hctx, params)`, and sets
  `respResult`. Feature 18 adds **three** cases: `textDocument/prepareCallHierarchy`,
  `callHierarchy/incomingCalls`, `callHierarchy/outgoingCalls` → all in a new
  `internal/server/call_hierarchy.go`.
- **Marshaling hazard (confirmed).** All three result types have their own `MarshalJSONTo`
  (`encoders.gen.go` lines 146/194/270 for `CallHierarchyIncomingCall`/`CallHierarchyItem`/
  `CallHierarchyOutgoingCall`), and `CallHierarchyItem.Data` is `LSPAny`. The results are **arrays**
  (`[]CallHierarchyItem`, `[]CallHierarchyIncomingCall`, `[]CallHierarchyOutgoingCall`), which
  `jsontext.Encoder` handles element-by-element. **Precedent: signature help** (`server.go` ~886–920)
  marshals via `sig.MarshalJSONTo(jsontext.NewEncoder(&buf))`, NOT `json.Marshal`, precisely because
  of union/nullable fields. **Follow the same pattern here:** encode each result with the
  protocol encoder (iterate the slice into a `jsontext` array, or wrap and call the per-element
  `MarshalJSONTo`), and build every `CallHierarchyItem.Data` via the existing **`mustLSPAny`** helper
  (`code_lens.go` line 180 — `gojson.Marshal` → `jsontext.Value`). Assert the on-the-wire JSON in a
  dispatch test.
- **Empty-result contract:** `prepare`/`incoming`/`outgoing` all return **arrays**. For "no result",
  return `[]` (never `null`) — mirror the completion handler's `respResult = []byte("[]")` for a nil
  slice. (LSP allows `null` for these, but `[]` is safest and matches our completion convention.)

### Capability advertisement (`server.go` initialize result ~141–164)
- `ServerCapabilities` is a **locked allow-list** built as a struct literal and enforced by
  `TestInitialize` (`server_test.go` ~528–536 `requiredProviders` slice). `CallHierarchyProvider` is a
  **union interface** (`types_unions.gen.go` 3021: `Boolean | *CallHierarchyOptions |
  *CallHierarchyRegistrationOptions`). Set it to `protocol.Boolean(true)` (the plan permits either a
  boolean or a `CallHierarchyOptions`; boolean is the minimal, simplest form — the provider needs no
  options). The initialize result is marshaled via `initResult.MarshalJSONTo(enc)` already, so the
  union encodes correctly. **Adding it REQUIRES extending the `TestInitialize` lock** — an explicit DoD
  item in T1.

### Protocol types (`call_hierarchy.gen.go`, verified)
- `CallHierarchyItem{Name string, Kind SymbolKind, Tags []SymbolTag, Detail *string, URI uri.URI,
  Range, SelectionRange, Data LSPAny}`.
- `CallHierarchyPrepareParams{ TextDocumentPositionParams; WorkDoneProgressParams }` (has
  `.TextDocument.URI` and `.Position`).
- `CallHierarchyIncomingCallsParams{ ...; Item CallHierarchyItem }`;
  `CallHierarchyOutgoingCallsParams{ ...; Item CallHierarchyItem }`.
- `CallHierarchyIncomingCall{ From CallHierarchyItem, FromRanges []Range }`;
  `CallHierarchyOutgoingCall{ To CallHierarchyItem, FromRanges []Range }`.

### The `Data` round-trip (design decision — see T2)
- `CallHierarchyItem.Data` is "preserved between prepare and incoming/outgoing." The client echoes the
  whole `Item` back on `incomingCalls`/`outgoingCalls`. We must re-locate the symbol from the item.
- **Plan:** encode a small serializable identity into `Data` at prepare time — e.g.
  `{"path": "<workspace-rel path>", "name": "<UPPER object/subroutine name>", "kind":
  "<model.SymbolKind>"}` — marshaled via `mustLSPAny`. On incoming/outgoing, **decode `params.Item.Data`
  back** (`gojson.Unmarshal([]byte(item.Data), &ident)`), then locate the symbol via `idx.Get(path)` +
  match `name`/`kind` against `Structure` (object root or a `SymbolSubroutine` child).
- **Robustness fallback (FR-43):** tolerate a client that echoes only `Item.URI` + `Item.SelectionRange`
  and drops/garbles `Data`. When `Data` fails to decode or is absent, fall back to locating the symbol
  by `Item.URI` → relPath → `idx.Get` and matching `Item.SelectionRange` (converted back via
  `fromProtocolPosition`) against `Structure`'s object-root/subroutine `SelectionRange`. This is a
  distinct helper (`resolveItemIdentity`) so both incoming and outgoing reuse it and it is unit-tested
  directly.

### Reuse map (do not duplicate)
- **`referenceSites(idx, res, root, targetPath, targetName, targetType, includeDeclaration, enc)`**
  (`references.go` 138) — the reverse-reference sweep: `idx.ForEach` over every file, `res.Get(filePath,
  edge.Source)` + `edgeMatchesTarget(...)` binds each edge to the target, **excluding dynamic/unresolved**
  (FR-17). Incoming calls is this sweep, but must be **grouped by caller file** and carry per-call-site
  `fromRanges` — `referenceSites` returns flat `[]Location` and includes DDM data-access sites, so it is
  **not** directly reusable as-is. **Extract/parallel its inner edge loop** into a new
  `incomingCallSites(idx, res, root, targetPath, targetType, enc)` that returns per-caller-file grouped
  results (caller relPath → its call-site ranges), reusing `edgeMatchesTarget` verbatim. (Keep
  `referenceSites` untouched to avoid churning feature 10/13 consumers.)
- **`edgeMatchesTarget(resolution, targetPath, targetType)`** (`references.go` 235) — reuse verbatim for
  the incoming match predicate.
- **`findCursorTarget(fa, pos)`** (`cursor.go` 23) — cursor → `*EdgeEntry`/`*DataAccessEntry`; reuse in
  prepare to detect a call-site cursor (resolve to callee).
- **`fromProtocolPosition` / `toProtocolRange`** (`position.go`, ADR-008) — reuse for all position/range
  conversion (model 1-based inclusive ↔ protocol 0-based end-exclusive, encoding-aware).
- **`uriToRelPath(root, uri)`** (`definition.go` 159) — URI → (abs, rel) with out-of-root error.
- **`definitionLocation` pattern** (`definition.go` 182) — building a `Location`/item from an object
  root `Structure.SelectionRange` with a zero-range fallback (FR-43).
- **`modelSymbolKindToProtocol(k)`** (`document_symbols.go` 100) — reuse verbatim for `CallHierarchyItem.Kind`
  (`SymbolObject→Module`, `SymbolSubroutine→Function`, etc.).
- **`mustLSPAny(v)`** (`code_lens.go` 180) — build `Data`. `gojson.Marshal`/`gojson.Unmarshal`
  (`github.com/go-json-experiment/json`) is the decode counterpart.
- **F7 snapshot discipline:** snapshot `hctx.idx`/`hctx.res`/`hctx.posEncoding`/`hctx.root` under
  `hctx.idxResMu.RLock()`, release **before** any file I/O — established in every provider. `prepare` is
  cursor-based → **store-first** buffer read (like signature_help/code_lens) then index+disk.
  `incoming`/`outgoing` operate over the index (the item identifies the symbol) — snapshot then read.
- **Live freshness (Story 2 AC4):** the `idx`/`res` snapshot reflects incremental re-analysis
  (`applyDocumentChange` → `idx.Add` + `ResolveInto` swap under the write lock, F7 build-then-publish),
  so incoming/outgoing automatically track edits — assert with a test (T6).

### Model / edge facts (verified)
- `model.EdgeEntry{Source Range, Target Range, Kind EdgeKind, TargetName string, Library string}`.
- Edge kinds (`model.go` 15–22): `EdgeCalls`/`EdgeCallsDynamic` (CALLNAT), `EdgeNavigatesTo`/
  `EdgeNavigatesToDynamic` (FETCH/RUN), `EdgePerforms`, `EdgeIncludes` (copycode), `EdgeReads`/
  `EdgeWrites` (data access). **Outgoing-call kinds = `EdgeCalls`, `EdgePerforms`, `EdgeNavigatesTo`**
  (all resolved). The `*Dynamic` kinds and `EdgeReads`/`EdgeWrites` are excluded. **`EdgeIncludes` is
  excluded by default** (copycode is compile-time textual inclusion, not a runtime call) — see Open
  Question 1.
- **Inline PERFORM** (Story 3 AC4): resolution binds an inline `PERFORM` to a same-object
  `DEFINE SUBROUTINE`; `res.Get(relPath, edge.Source).IsResolved()` with `Path == the caller's own
  relPath`. `definition.go` (97–116) already demonstrates locating the matching `SymbolSubroutine`
  child in `Structure.Children`. Outgoing must emit an item pointing at that subroutine's
  `SelectionRange` in the same file.
- `Resolution` (`resolution.go` 54): `IsResolved()`, `.Path` (workspace-rel), `.Type`
  (`model.ObjectType`); `IsAmbiguous()`, `.Candidates`; `IsUnresolved()`/`IsDynamic()`. Only resolved
  edges become hierarchy edges.

### Which acceptance criteria are affected by existing code
Nothing is already satisfied — this is a net-new feature. But **every** behavior reuses an existing
primitive (sweep, cursor, resolution, structure, position conversion, symbol-kind map, mustLSPAny).
No greenfield foundation work.

---

## Ordered task list

Order: capability + 3-method dispatch skeleton & marshaling (T1) → `Data` round-trip + item-identity
resolution (T2) → prepare (T3, Story 1) → incoming (T4, Story 2) → outgoing static (T5, Story 3
AC1–3) → inline-PERFORM outgoing (T6a) → live-freshness + modeled-gap + fuzz (T6/T7). T1 lands the
skeleton so T3–T5 can assert against a wired dispatch; T2 lands the shared identity helper both
incoming and outgoing depend on.

Every task: `tdd-red` (failing test + minimal fixture) → `tdd-green` (minimal impl) → `tdd-refactor`
(F7 discipline, reuse the named primitive, never-panic). Fixtures live under
`internal/server/testdata/callhierarchy/` unless an existing navigation fixture already fits.

---

### T1 — Capability advertisement + 3-method dispatch skeleton + protocol marshaling
**Story:** 4 (AC1, AC2). **Depends on:** none.

**Behavior:** Advertise `callHierarchyProvider: true` in the initialize result and wire the three
dispatch cases so all three methods are handled without error and return `[]` for any input (stub
providers returning nil/empty at this stage). Establish the `MarshalJSONTo`-based array encoding.

- Add `CallHierarchyProvider: protocol.Boolean(true)` to the `ServerCapabilities` literal (~145–158).
- Add three `case` blocks in the dispatch switch, each: gate on `stateInitialized`; decode the params
  type (`CallHierarchyPrepareParams` / `CallHierarchyIncomingCallsParams` /
  `CallHierarchyOutgoingCallsParams`) via `UnmarshalJSONFrom`; call the provider; marshal the
  `[]CallHierarchy*` result with the protocol encoder (each element's `MarshalJSONTo`), emitting `[]`
  for nil/empty. On decode error → `jsonrpc2.InvalidParams`; on provider error → `jsonrpc2.InternalError`.
- Create `internal/server/call_hierarchy.go` with stub `providePrepareCallHierarchy`,
  `provideIncomingCalls`, `provideOutgoingCalls` (return `nil, nil`).

**Fixtures:** none (dispatch/capability-level test).
**Expected result:** initialize response JSON contains `"callHierarchyProvider":true`; each of the
three methods, invoked post-`initialized`, returns a valid JSON-RPC result of `[]` (never `null`,
never `MethodNotFound`); invoked pre-`initialized` returns `ServerNotInitialized`.
**Reuses:** signature-help marshaling pattern (`server.go` ~910–920); completion empty-array pattern.
**Migrates:** `TestInitialize` `requiredProviders` — **add `"callHierarchyProvider"`** (mandatory).

**DoD:**
- [ ] `TestInitialize` extended to assert `callHierarchyProvider == true`; passes.
- [ ] A dispatch test confirms all three methods handled without error, `[]` on empty, and
      `ServerNotInitialized` before initialize (Story 4 AC2).
- [ ] On-the-wire JSON asserted for at least one result via the protocol encoder path (marshaling
      hazard covered).
- [ ] `go vet` / `gofmt` clean; no `internal/model` or cache change.

---

### T2 — `Data` round-trip identity: encode at prepare, decode + fallback locate on incoming/outgoing
**Story:** enables 2 & 3 (`Item` re-location). **Depends on:** T1.

**Behavior:** Define the serializable item-identity carried in `CallHierarchyItem.Data` and the
`resolveItemIdentity` helper that maps a client-echoed `CallHierarchyItem` back to a concrete symbol
(`idx.Get` + `Structure` node). Purely a shared primitive with a focused unit test (no method dispatch
yet beyond T1's stubs).

- Define an internal struct (e.g. `callHierarchyItemData{Path, Name string, Kind model.SymbolKind}`).
- `buildCallHierarchyItem(root, relPath, fa, sym, content, enc) protocol.CallHierarchyItem` — builds an
  item from a `model.Symbol` (object root or subroutine): `Name = sym.Name`, `Kind =
  modelSymbolKindToProtocol(sym.Kind)`, `URI = uri.File(abs)`, `Range`/`SelectionRange` via
  `toProtocolRange`, `Data = mustLSPAny(callHierarchyItemData{relPath, sym.Name, sym.Kind})`.
- `resolveItemIdentity(idx, root, item, enc) (relPath string, fa model.FileAnalysis, sym *model.Symbol,
  ok bool)` — decode `item.Data` (via `gojson.Unmarshal([]byte(item.Data), &d)`); locate `idx.Get(d.Path)`;
  match `d.Name`/`d.Kind` against the object root or a `SymbolSubroutine` child. **Fallback (FR-43):**
  if `Data` is empty/undecodable, use `item.URI` → `uriToRelPath` → `idx.Get`, and match the object
  root/subroutine whose `SelectionRange` equals `item.SelectionRange` (convert back for comparison, or
  match by URI+object-root when only one object per file).

**Fixtures:** one subprogram `.NSN` with a `DEFINE SUBROUTINE` (reuse an existing navigation fixture if
one already has both an object root and an inline subroutine; else add `callhierarchy/callee.NSN`).
**Expected result:** round-trip test — build an item from an object root, marshal `Data`, feed the item
back into `resolveItemIdentity`, recover the same `(relPath, sym)`; same for a subroutine child; and a
fallback test where `Data` is cleared but `URI`+`SelectionRange` still resolve the symbol.
**Reuses:** `mustLSPAny` (`code_lens.go`), `gojson.Unmarshal`, `modelSymbolKindToProtocol`
(`document_symbols.go`), `uriToRelPath`, `toProtocolRange`, `definitionLocation` fallback style.

**DoD:**
- [ ] Round-trip test (object root + subroutine) passes; fallback (Data-less) test passes.
- [ ] `resolveItemIdentity` returns `ok=false` (never panics) for garbage `Data`, unknown path,
      out-of-root URI, nil `Structure` (FR-43).
- [ ] Deterministic; `go vet`/`gofmt` clean; no model/cache change.

---

### T3 — `prepareCallHierarchy` (Story 1)
**Story:** 1 (AC1, AC2, AC3). **Depends on:** T1, T2.

**Behavior:** `providePrepareCallHierarchy(hctx, params)` maps a cursor to a callable symbol and returns
a single-element `[]CallHierarchyItem` (or `[]` / nil). The cursor may be on **either**:
1. **a call site** — `findCursorTarget` finds an `EdgeEntry`; resolve it (`res.Get`); if resolved to a
   module → build the item from the target file's object-root `Structure`; if resolved to an inline
   subroutine (same-file PERFORM) → build from the matching `SymbolSubroutine` child. **Ambiguous →
   emit one item per `Candidate`** (each from its own object-root `Structure`, its own `Data.path` —
   approved decision OQ-4). Dynamic/unresolved → empty (AC2).
2. **a definition name** — no edge under cursor; consult the **current file's own** `Structure`: if the
   cursor's `SelectionRange` hits the object root or a `SymbolSubroutine` child, build the item from that
   node.

- **Store-first** buffer read (live edits), then index+disk; F7 snapshot.
- Non-callable position (cursor not on a call site and not on an object/subroutine name — e.g. on a data
  field, a comment, a DDM read) → `[]`/nil (AC3).

**Fixtures:**
- `callhierarchy/caller.NSP` — a program with `CALLNAT 'CALLEE'` and an inline `PERFORM SUB-A` + a
  `DEFINE SUBROUTINE SUB-A` (drives cursor-on-call-site and cursor-on-inline-subroutine-name).
- `callhierarchy/callee.NSN` — the resolved subprogram (from T2, reuse).
- A `.natural-lsp.toml` / flat-namespace setup consistent with existing navigation fixtures so
  resolution binds `CALLEE` (mirror `references_test.go`/`definition_test.go` harness).

**Expected result:**
- Cursor on `'CALLEE'` in caller → item `{Name:"CALLEE", Kind:Module, URI:callee.NSN,
  SelectionRange:<object root>}`.
- Cursor on the `DEFINE SUBROUTINE SUB-A` name → item `{Name:"SUB-A", Kind:Function, URI:caller.NSP,
  SelectionRange:<subroutine name>}`.
- Cursor on a dynamic `CALLNAT #TARGET` site → `[]` (AC2).
- Cursor on a plain data field / comment → `[]` (AC3).

**Reuses:** `findCursorTarget`, `res.Get`/`IsResolved`, `Structure` walk (per `definition.go`),
`buildCallHierarchyItem` (T2), store-first pattern (signature_help), `fromProtocolPosition`, F7 snapshot.

**DoD:**
- [ ] Table-driven test covers AC1 (call-site → callee item; definition-name → own item), AC2
      (dynamic/unresolved → empty), AC3 (non-callable → empty), and **ambiguous call-site → one item
      per candidate** (OQ-4: N candidates → N items, each with its own URI/Data.path).
- [ ] `Data` populated so a subsequent incoming/outgoing can re-locate (assert via `resolveItemIdentity`).
- [ ] F7 discipline (snapshot, release before I/O); never panics on nil `Structure`/unreadable file.
- [ ] `go vet`/`gofmt` clean; no model/cache change.

---

### T4 — `incomingCalls` — all static callers, grouped, with `fromRanges` (Story 2, AC1–3)
**Story:** 2 (AC1, AC2, AC3). **Depends on:** T1, T2.

**Behavior:** `provideIncomingCalls(hctx, params)` resolves `params.Item` to a symbol identity
(`resolveItemIdentity`), then performs the reverse sweep grouped **by caller file**: for each file's
edges, `res.Get(file, edge.Source)` + `edgeMatchesTarget(resolution, targetPath, targetType)` → the
edge is an incoming call. Group all matching call-site `edge.Source` ranges by the **caller file**,
build one `CallHierarchyIncomingCall` per distinct caller (its `From` = the caller object's
`CallHierarchyItem`, `FromRanges` = its call-site ranges, deterministically sorted). Dynamic/unresolved
sites never match (`edgeMatchesTarget` already enforces this — FR-17).

- Add `incomingCallSites(idx, res, root, targetPath, targetType, enc)` — parallels `referenceSites`'
  inner edge loop (reuse `edgeMatchesTarget`) but returns a caller-grouped structure and **omits** the
  DDM data-access branch and the include-declaration branch (call hierarchy is calls only). Keep
  `referenceSites` untouched.
- Each caller's `From` item: caller file's object-root `Structure` (name/kind/URI/selectionRange) +
  `Data` for further drill-down. `FromRanges` = each matching `edge.Source` converted via
  `toProtocolRange`, sorted by start.
- Deterministic ordering: sort callers by URI, ranges by start (mirror `referenceSites`' sort).

**Fixtures:**
- Reuse `callhierarchy/callee.NSN` as the target; add `callhierarchy/caller.NSP` (from T3, has
  `CALLNAT 'CALLEE'`) and a **second** caller `callhierarchy/caller2.NSP` with **two** `CALLNAT 'CALLEE'`
  sites (asserts grouping + multiple `fromRanges`).
- A caller with a `CALLNAT #DYN` dynamic site targeting the same name (asserts exclusion — AC3).

**Expected result:** for the `CALLEE` item → two `CallHierarchyIncomingCall`s (caller.NSP with 1
range, caller2.NSP with 2 ranges); the dynamic site is absent; each `From` carries name/kind/URI/range.

**Reuses:** `resolveItemIdentity` (T2), `edgeMatchesTarget` (references.go, verbatim), `idx.ForEach`,
`toProtocolRange`, `buildCallHierarchyItem`, F7 snapshot.

**DoD:**
- [ ] Test asserts all static callers workspace-wide (AC1), grouped `From`+`FromRanges` (AC2), dynamic
      excluded (AC3, FR-11/FR-17).
- [ ] Deterministic ordering (callers by URI, ranges by start).
- [ ] Unknown/garbage `Item` → `[]` (never panics, FR-43).
- [ ] F7 discipline; `go vet`/`gofmt` clean; no model/cache change.

---

### T5 — `outgoingCalls` — static CALLNAT/PERFORM(external)/FETCH/RUN, with `fromRanges` (Story 3, AC1–3)
**Story:** 3 (AC1, AC2, AC3). **Depends on:** T1, T2.

**Behavior:** `provideOutgoingCalls(hctx, params)` resolves `params.Item` to a symbol identity, then
walks **that file's own** `FileAnalysis.Edges`: for each edge of kind `EdgeCalls`, `EdgePerforms`, or
`EdgeNavigatesTo` (i.e. CALLNAT / external PERFORM / FETCH / program-transfer RUN), `res.Get(relPath,
edge.Source)` + `IsResolved()` → an outgoing call. Group by **callee** (resolved target path/name);
build one `CallHierarchyOutgoingCall` per distinct callee (`To` = callee item from the callee file's
object-root `Structure`; `FromRanges` = the call-site `edge.Source` ranges within the current module).
Dynamic (`EdgeCallsDynamic`/`EdgeNavigatesToDynamic`) and unresolved edges excluded (AC3, FR-17).
`EdgeIncludes` excluded (Open Question 1). `EdgeReads`/`EdgeWrites` excluded (not calls).

- Callee item URI/range from the resolved target file's `Structure.SelectionRange` (external) —
  inline-PERFORM handling is T6a.
- Deterministic ordering: callees by URI, ranges by start.

**Fixtures:**
- Reuse `callhierarchy/caller.NSP` (has `CALLNAT 'CALLEE'`) — add a `FETCH 'PGM'` and a
  `CALLNAT #DYN` (dynamic, excluded) and an `INCLUDE CC` (excluded).
- Target files for the resolved callees (`callee.NSN`, a `PGM.NSP`).

**Expected result:** for the caller item → outgoing calls to `CALLEE` (subprogram) and `PGM`
(program), each with its call-site `fromRanges`; the dynamic `CALLNAT #DYN` and the `INCLUDE CC` are
absent.

**Reuses:** `resolveItemIdentity` (T2), `res.Get`/`IsResolved`, `idx.Get`, `toProtocolRange`,
`buildCallHierarchyItem`, F7 snapshot.

**DoD:**
- [ ] Test asserts all static outgoing CALLNAT/external-PERFORM/FETCH/RUN (AC1), grouped
      `To`+`FromRanges` (AC2), dynamic/unresolved and INCLUDE excluded (AC3).
- [ ] Deterministic ordering.
- [ ] Unknown/garbage `Item`, nil `Structure`, unreadable callee → `[]`/skip (never panics, FR-43).
- [ ] F7 discipline; `go vet`/`gofmt` clean; no model/cache change.

---

### T6a — Inline PERFORM as an outgoing call to a same-object subroutine (Story 3, AC4)
**Story:** 3 (AC4). **Depends on:** T5.

**Behavior:** When an outgoing `EdgePerforms` edge resolves to a subroutine **in the same object**
(`resolution.Path == the item's own relPath` — inline PERFORM per feature 07), emit a
`CallHierarchyOutgoingCall` whose `To` is the matching `SymbolSubroutine` child's item (its
`SelectionRange`, `Kind=Function`, same URI), not the object root. Mirror `definition.go` 97–116's
inline-subroutine lookup in `Structure.Children`.

**Fixtures:** reuse `callhierarchy/caller.NSP` (already has `PERFORM SUB-A` + `DEFINE SUBROUTINE SUB-A`
from T3).
**Expected result:** the caller item's outgoing calls include an item for `SUB-A` pointing at the
inline subroutine's `SelectionRange` in `caller.NSP`.
**Reuses:** the T5 outgoing walk; the inline-subroutine `Structure.Children` lookup from `definition.go`.

**DoD:**
- [ ] Test asserts inline PERFORM → same-object subroutine item (AC4), distinct from an external
      module callee.
- [ ] Deterministic; never panics on missing child; `go vet`/`gofmt` clean; no model/cache change.

---

### T6 — Live freshness after incremental re-analysis (Story 2, AC4)
**Story:** 2 (AC4). **Depends on:** T4.

**Behavior:** Prove incoming calls reflect the current index after an incremental edit. Drive an
`applyDocumentChange` (a new caller's content adds a `CALLNAT 'CALLEE'`), then invoke `incomingCalls`
for `CALLEE` and assert the new caller appears — no server restart, no manual rebuild. This exercises
the F7 snapshot reading the freshly-swapped `(idx, res)`.

**Fixtures:** reuse the T4 `callhierarchy/` set; simulate a change adding a new/edited caller.
**Expected result:** the incoming-calls result grows to include the edited caller's new call site.
**Reuses:** `applyDocumentChange` (feature 10, `server.go`), `ResolveInto`, the T4 provider.

**DoD:**
- [ ] Test drives an incremental change and asserts the updated incoming set (AC4).
- [ ] Run with `-race` (touches the idx/res swap + read snapshot).
- [ ] `go vet`/`gofmt` clean; no model/cache change.

---

### T7 — Fuzz targets (FR-43)
**Story:** cross-cutting (FR-43). **Depends on:** T3, T4, T5.

**Behavior:** Add fuzz targets mirroring the existing `FuzzProvideXxx` convention (`fuzz_test.go`):
- `FuzzProvidePrepareCallHierarchy` — fuzz file content + cursor position (as
  `FuzzProvideDefinition`/`FuzzProvideHover`/`FuzzProvideSignatureHelp` do).
- `FuzzProvideIncomingCalls` / `FuzzProvideOutgoingCalls` — fuzz the `CallHierarchyItem`
  (`URI`, `SelectionRange`, and especially garbage `Data` bytes) to prove `resolveItemIdentity` and the
  providers never panic on malformed client input.

Assert: never panics; always returns a valid (possibly empty) slice + nil error.
**Fixtures:** seed corpus from the T3–T5 fixtures.
**Reuses:** the fuzz harness scaffolding in `fuzz_test.go`.

**DoD:**
- [ ] Three fuzz targets added; each runs a short `-fuzz` smoke without panic.
- [ ] `go vet`/`gofmt` clean.

---

## Reviews required (for `/review-feature`)
- **review-protocol-conformance** — new LSP methods + capability; assert `CallHierarchyItem.Data`
  round-trips, the union capability encodes as `true`, results are arrays (`[]` not `null`), and the
  `MarshalJSONTo` path is used (marshaling hazard).
- **review-concurrency** — F7 snapshot discipline in all three providers; the T6 live-freshness path
  (idx/res swap under write lock, read under RLock, released before I/O); `-race`.
- **review-robustness** — FR-43 never-panic on garbage `Data`, unknown paths, out-of-root URIs, nil
  `Structure`, unreadable files; the three fuzz targets.
- **review-seam** — confirm **no** `internal/model` / Analyzer-interface change (LSP-facing code depends
  only on `internal/model` + `internal/workspace`); confirm cache format stays `0.6.0`.
- **review-docs** — a new capability + three methods land → `CLAUDE.md` "Project state" + capability
  list and `README.md` feature set must sync at `/finalize-feature` (this is the last planned provider —
  update the "remaining unwired providers" note).

## Approved decisions (checkpoint)

Resolved with the user before implementation:

1. **Exclude INCLUDE/copycode** from outgoing calls (OQ-1 default). Call hierarchy = runtime calls
   (CALLNAT / external PERFORM / FETCH / RUN); `EdgeIncludes` is compile-time and stays out.
2. **Ambiguous target → one `CallHierarchyItem` per candidate** (OQ-4, NOT the plan default). At
   **prepare**, when a call-site resolution `IsAmbiguous()`, emit one item per `Candidate` (each built
   from that candidate's own object-root `Structure`, with its own `Data.path`), rather than returning
   empty. Because each item carries a concrete per-candidate `Data` path, `incomingCalls`/`outgoingCalls`
   operate on a single anchor per item and need no ambiguity handling of their own. `prepare` on a
   **definition name** (own symbol) is never ambiguous. **T3 covers this — add an ambiguous-call-site
   test asserting N items (one per candidate).** Dynamic/unresolved still → empty.
3. **No pagination / workDoneProgress** (OQ-2 default) — full results; noted as future-additive.
4. **Empty results as `[]`** not `null` (OQ-3 default) — matches the completion convention.

## Open questions
1. **INCLUDE/copycode as an outgoing call** (plan open question; Story 3 AC1 omits it). **Plan default:
   exclude `EdgeIncludes`** — copycode is compile-time textual inclusion, a distinct edge kind, not a
   runtime call. If you want it included, it is a one-line filter change in T5 (add `EdgeIncludes` to the
   outgoing-kind set, targeting the copycode `.NSC`). **Decide at the checkpoint.**
2. **Pagination / `workDoneProgress` for huge caller sets** (plan open question). **Plan default: no
   pagination** — return full results (in-memory index, fast; no AC requires it). The params types embed
   `PartialResultParams`/`WorkDoneProgressParams`, so partial results are a future additive enhancement.
   **Decide at the checkpoint.**
3. **Empty-result shape:** `[]` vs `null`. **Plan default: `[]`** (matches the completion convention and
   is unambiguous for clients). Confirm no target client requires `null`.
4. **Ambiguous-target prepare/incoming/outgoing:** when a call site resolves `Ambiguous` (flat-namespace
   collision), the plan says prepare returns empty on non-resolved. **Plan default: treat `Ambiguous`
   like unresolved for hierarchy** (empty) — call hierarchy needs a single anchor. (Definition returns
   all candidates, but a hierarchy panel anchors on one symbol.) Confirm this is acceptable, or emit one
   item per candidate at prepare.
