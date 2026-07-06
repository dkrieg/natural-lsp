# Tasks: Document outline (`textDocument/documentSymbol`)

**Feature:** `11-document-outline`
**PRD requirements:** FR-27 (document outline), FR-43 (graceful degradation)
**Depends on:** feature 09 (program-structure extraction — the `model.Symbol` tree), feature 10
(the LSP-provider wiring precedent).

## Scope note

This feature is **provider wiring only**. The structural model it renders —
`FileAnalysis.Structure *model.Symbol`, a hierarchical, source-ordered, kind-tagged tree — is
**already produced** by feature 09 (`internal/analysis/natural/structure.go`). Do **not** re-plan
extraction, add `model` fields, or bump the cache-format version. The work is: a pure
`model.Symbol` → `protocol.DocumentSymbol[]` converter, a provider handler, capability advertisement,
dispatch wiring, and serving from the open-document store so the outline tracks unsaved edits.

The two plan open questions are **already resolved by feature 09** and are not re-litigated here:
- *External calls inline vs. grouped* → DDM references are per-entry `SymbolDDMReference` children of the
  object root, inline (not grouped under a dedicated node). We render the tree as-built.
- *Source order vs. grouped-by-kind* → the tree's children are in deterministic source order
  (`sort.SliceStable` on `Range.Start`), applied recursively. We preserve that order.

---

## Current-state findings & impact

Investigated the live code (not the README). Ground-truth facts the plan must build on:

- **The structural model already exists and is complete** — `model.Symbol{Kind, Name, Range,
  SelectionRange, Children}` (`internal/model/model.go:151`) with six `model.SymbolKind` constants
  (`internal/model/model.go:117-134`): `SymbolObject` (`"object"`), `SymbolSubroutine`
  (`"subroutine"`), `SymbolDataSection` (`"data-section"`), `SymbolDataField` (`"data-field"`),
  `SymbolMap` (`"map"`), `SymbolDDMReference` (`"ddm-reference"`). `extractStructure`
  (`internal/analysis/natural/structure.go`) already builds the tree, sets it on
  `FileAnalysis.Structure`, guarantees `SelectionRange ⊆ Range`, orders children by source, and
  **skips empty-`Name` DDM refs** — so every node has a non-empty `Name` (satisfies the LSP
  `DocumentSymbol.Name` "must not be empty" rule) and a well-formed contained selection range.
- **The provider must serve from the open-document store, not disk** — Story 2 requires the outline to
  reflect unsaved edits. The `document.Store` (`internal/document/store.go`) already re-analyzes on
  `Open`/`Update` and exposes `Get(uri) (*Document, bool)` where `Document{Content []byte, Analysis
  model.FileAnalysis}` — i.e. the current unsaved content **and** its freshly-computed
  `Analysis.Structure`. This differs from the feature-10 providers, which read from `idx` + `os.ReadFile`
  (on-disk). documentSymbol should **prefer `hctx.store.Get(uri)`** (open buffer) and fall back to the
  index + disk read only when the document is not open. `hctx.store` is on `handlerContext`
  (`internal/server/handlers.go:175`).
- **Position/range conversion already exists** — `toProtocolRange(model.Range, content, enc)`
  (`internal/server/position.go:133`) does exactly the ADR-008-correct mapping (model 1-based
  byte-offset inclusive-end → protocol 0-based code-unit end-exclusive), and correctly preserves a
  zero-width caret range (e.g. an object root's `SelectionRange`). Reuse verbatim; no new position code.
- **The wiring precedent is `provideWorkspaceSymbols`** (`internal/server/workspace_symbols.go`) and
  `provideDefinition` (`internal/server/definition.go`): provider function takes `*handlerContext` +
  decoded params, guards `hctx == nil`, snapshots shared state under `hctx.idxResMu.RLock()`, returns
  a `protocol` slice. The dispatch switch in `Run` (`internal/server/server.go:583`) decodes params,
  gates on `stateInitialized`, calls the provider, and `json.Marshal`s the result. Follow this shape.
- **Capabilities are a locked allow-list** — advertised in `handleInitialize`
  (`internal/server/server.go:134`, currently `Definition`/`References`/`WorkspaceSymbol` providers)
  and asserted by `TestInitialize` (`internal/server/server_test.go:330`, `requiredProviders` list at
  ~line 490). Adding `documentSymbolProvider` requires editing **both** — the test convention demands
  additions be explicit.
- **The `symbols.go` stub is misplaced and effectively dead** — the only "symbols" stub is
  `internal/analysis/natural/symbols.go` (a 6-line package-doc + TODO mentioning documentSymbol). That
  is in the **analysis backend**, on the wrong side of the seam: the `model.Symbol` → `protocol.*`
  conversion is LSP-facing and must live in `internal/server/`. Do **not** implement the provider in
  the analysis package. Plan a new file `internal/server/document_symbols.go` next to the other
  providers, and (housekeeping, T5) delete or correct the stale analysis-package stub so it doesn't
  mislead future readers.
- **No index change needed for the primitive** — the converter is a pure function of a
  `*model.Symbol` + content + encoding. It does not touch `idx`/`res`. Only the fallback path (document
  not open) reads `idx` under the read lock.
- **Available `protocol.SymbolKind` constants** (`go.lsp.dev/protocol@v1.0.0`,
  `basic_structures.gen.go`): `Module=2`, `Namespace=3`, `Class=5`, `Method=6`, `Field=8`,
  `Function=12`, `Variable=13`, `Object=19`, `Struct=23`, `EnumMember=22`. Feature 10 mapped
  `SymbolObject → Module` and `SymbolSubroutine → Function`; keep those consistent (see T1 for the full
  proposed mapping and OQ-1).

Reconciliation of each acceptance criterion:

| Criterion | Classification | Task |
| --- | --- | --- |
| Outline shows sections, subroutines, maps, external calls (DDM refs), etc. | New (render existing tree) | T1, T2 |
| Outline is hierarchical (nesting reflected) | New (recursive convert) | T1 |
| Selecting an entry navigates to source position | New (range mapping via `toProtocolRange`) | T1 |
| Each entry has an appropriate symbol kind | New (`SymbolKind` mapping) | T1 |
| Outline reflects current (unsaved) content | New (serve from `document.Store`) | T2 |
| Partial/malformed object still outlines recognized parts | Already satisfied by feature 09 extraction; assert end-to-end | T2, T4 |
| Advertise `documentSymbolProvider` | New | T3 |
| Robustness — never panic on arbitrary input (FR-43) | New (fuzz the converter) | T4 |

---

## Tasks

Tasks are dependency-ordered. Each lists the TDD agents to run (`tdd-red` → `tdd-green` →
`tdd-refactor`), a Definition-of-Done checklist, and the fixtures/expected results.

### T1 — `model.Symbol` tree → `protocol.DocumentSymbol[]` converter (the primitive)

**Goal:** a pure, recursive converter `symbolToDocumentSymbol(model.Symbol, content string, enc
protocol.PositionEncodingKind) protocol.DocumentSymbol` (and a slice helper over `Children`) in a new
file `internal/server/document_symbols.go`. Maps `Kind`, `Name`, `Range`, `SelectionRange`, and recurses
into `Children`, preserving source order.

**Kind mapping (proposed; see OQ-1):**
- `SymbolObject` → `protocol.SymbolKindModule` (consistent with feature 10's workspace-symbol mapping)
- `SymbolSubroutine` → `protocol.SymbolKindFunction` (consistent with feature 10)
- `SymbolMap` → `protocol.SymbolKindObject` (or `Class` — OQ-1)
- `SymbolDataSection` → `protocol.SymbolKindNamespace` (a named grouping of fields)
- `SymbolDataField` → `protocol.SymbolKindField` (or `Variable` — OQ-1)
- `SymbolDDMReference` → `protocol.SymbolKindStruct` (a record/table shape; or `Object` — OQ-1)
- Unknown/zero kind → `protocol.SymbolKindObject` (defensive default; never drop a node — FR-43)

**Reuses:** `toProtocolRange` (`position.go`) for both `Range` and `SelectionRange`. No new position code.

**Fixtures:** reuse feature 09's structure fixtures under
`internal/analysis/natural/testdata/structure/` (drive them through `natural.Analyze` in the test to get
a real `FileAnalysis.Structure`, mirroring how `workspace_symbols_test.go`/`fuzz_test.go` build inputs).
Cover, in **separate** test cases (thin slices): (a) an object with data sections + fields (nesting +
`Field`/`Namespace` kinds), (b) an object with subroutines (`Function` kind, sibling ordering), (c) an
object with a map + map fields, (d) an object with DDM references (inline, source order). If a single
existing fixture spans several of these, one fixture may back several assertion *cases*, but each case
asserts one behavior.

**Expected results per case:** the returned `DocumentSymbol` mirrors the input `Symbol` — same `Name`,
mapped `Kind`, `Range`/`SelectionRange` converted to 0-based end-exclusive protocol coords via
`toProtocolRange`, `SelectionRange` contained in `Range`, `Children` in the same source order as the
model tree, nesting depth preserved.

**DoD:**
- [ ] `tdd-red`: failing tests for the four cases above, asserting name, kind, converted ranges,
      selection-range containment, and child order/nesting.
- [ ] `tdd-green`: implement `symbolToDocumentSymbol` + slice helper; all cases pass.
- [ ] `tdd-refactor`: tidy; ensure the kind mapping is a single readable switch; no dead code.
- [ ] Pure function: no `hctx`, no I/O, no locks.
- [ ] `just verify` green.

### T2 — `provideDocumentSymbols` handler (serve open buffer, fall back to index)

**Goal:** `provideDocumentSymbols(hctx *handlerContext, params protocol.DocumentSymbolParams)
([]protocol.DocumentSymbol, error)` in `internal/server/document_symbols.go`. Resolves the document's
current `Structure` and converts it via T1's primitive.

**Resolution order (the Story-2 requirement):**
1. Guard `hctx == nil` → return `nil, nil` (precedent).
2. Try the open-document store first: `doc, ok := hctx.store.Get(params.TextDocument.URI)`. If open, use
   `doc.Analysis.Structure` (current, possibly-unsaved) and `string(doc.Content)` for range conversion.
   This is what makes the outline track unsaved edits.
3. Fallback (document not open): snapshot `idx` under `hctx.idxResMu.RLock()`, convert URI → relPath
   (mirror `provideDefinition`'s `filepath.Rel` + backslash normalization), `idx.Get(relPath)`, and
   `os.ReadFile` the absolute path for content.
4. If no `Structure` (nil) after both attempts → return `nil, nil` (FR-43: empty outline, no error).
5. Convert `*Structure` via T1 and return a single-element (object-root) `[]protocol.DocumentSymbol`
   whose `Children` are the sections/subroutines/maps/DDM refs.

**Reuses:** `hctx.store` (`document.Store.Get`), `hctx.idx`, `toProtocolRange`, T1's converter, the
URI→relPath idiom from `definition.go`.

**Concurrency (F7):** only the fallback path touches `idx`; snapshot the pointer under `RLock` exactly as
the other providers do. The store is independently concurrency-safe.

**Fixtures/expected:**
- Open-buffer case: `store.Open`/`Update` a URI with content whose `Structure` differs from any on-disk
  version (or use an in-memory-only URI); assert the returned outline reflects the buffer content.
- Fallback case: a file present only in the index (not opened); assert the outline is served from disk.
- Not-found case: an unknown URI → `nil, nil`.
- Partial/malformed case: content with some unrecognized lines (reuse a feature-09 partial fixture, or a
  minimal reproducer with a valid subroutine + a garbage line) → outline still lists the recognized
  parts (FR-43 end-to-end).

**DoD:**
- [ ] `tdd-red`: failing tests for open-buffer, fallback, not-found, and partial-object cases.
- [ ] `tdd-green`: implement the handler; all cases pass.
- [ ] `tdd-refactor`: dedup the URI→relPath/content-read logic with existing providers if it reads
      cleanly; otherwise leave a short comment noting the store-first divergence from feature 10.
- [ ] `just verify` green.

### T3 — Advertise `documentSymbolProvider` and wire dispatch

**Goal:** advertise the capability and route `textDocument/documentSymbol` to the T2 handler.

**Changes:**
- `handleInitialize` (`internal/server/server.go:134`): add
  `DocumentSymbolProvider: protocol.Boolean(true)` to `ServerCapabilities`.
- `TestInitialize` (`internal/server/server_test.go`, `requiredProviders` ~line 490): add
  `"documentSymbolProvider"` to the asserted allow-list (the convention requires provider additions be
  explicit in this test).
- Dispatch switch in `Run` (`internal/server/server.go`, add a `case "textDocument/documentSymbol"`
  alongside the other providers): gate on `stateInitialized` (else `ServerNotInitialized`), decode
  `protocol.DocumentSymbolParams`, call `provideDocumentSymbols`, `json.Marshal` the result. Marshal
  a nil result as `[]byte("null")` (matching definition/references) so an empty/unopened file returns
  a JSON `null` rather than erroring.

**Fixtures/expected:** an end-to-end server test (mirroring the initialize→initialized→request flow in
`server_test.go`/`workspace_symbols_test.go`): send `initialize`, assert `documentSymbolProvider: true`
in the response; then `initialized` + `textDocument/didOpen` a fixture + `textDocument/documentSymbol`,
assert the response is a hierarchical `DocumentSymbol[]` for that file.

**DoD:**
- [ ] `tdd-red`: `TestInitialize` updated to require `documentSymbolProvider` (fails until advertised);
      an end-to-end documentSymbol request test (fails until dispatch wired).
- [ ] `tdd-green`: advertise capability + add dispatch case; both pass.
- [ ] `tdd-refactor`: dispatch case matches the structure/comment style of neighboring cases.
- [ ] `just verify` green.

### T4 — Fuzz the conversion primitive (FR-43)

**Goal:** `FuzzDocumentSymbols` in `internal/server/fuzz_test.go` (the existing fuzz file), proving the
converter/handler primitive never panics over arbitrary content and arbitrary/degenerate `model.Symbol`
trees (empty names, zero/huge/negative ranges, `SelectionRange` outside `Range`, deep nesting, unknown
`Kind`, both encodings). Follow the shape of the existing `FuzzPositionConversion`/`FuzzProvideDefinition`
targets in that file.

**DoD:**
- [ ] `tdd-red`: a fuzz target that fails (or a seed that panics) before the guards exist — or, if T1
      already guards defensively, add the target as a regression guard with a seed corpus (empty tree,
      degenerate ranges, non-ASCII content).
- [ ] `tdd-green`: converter/handler survives all seeds; `go test -run=Fuzz... -fuzz` for a short burst
      finds no panic.
- [ ] `tdd-refactor`: seed corpus documented in a comment (like the existing fuzz targets).
- [ ] `just verify` green.

### T5 — Housekeeping: correct the stale analysis-package stub

**Goal:** the `internal/analysis/natural/symbols.go` package-doc stub claims to own the FileAnalysis →
documentSymbol mapping, which now lives (correctly) in `internal/server/`. Delete the file, or reduce its
doc comment to only what the analysis backend actually owns (the `model.Symbol` tree via
`structure.go`), so it no longer misleads. No behavior change.

**DoD:**
- [ ] `tdd-refactor` only (no test change — dead package-doc): remove/correct the stub.
- [ ] `just verify` green (nothing imports it; confirm no build break).

---

## Reviews required (`/review-feature`)

- **Seam integrity:** the `model.Symbol` → `protocol.DocumentSymbol` conversion lives in
  `internal/server/`, depends only on `internal/model` + `go.lsp.dev/protocol`, and does not reach into
  parser internals. The analysis backend is untouched except the T5 stub cleanup.
- **No model/cache change:** confirm no `internal/model` field added and no cache-format bump (feature 09
  already ships `Structure`; cache is at `0.6.0`).
- **ADR-008 correctness:** ranges converted via `toProtocolRange` only; `SelectionRange ⊆ Range` in every
  emitted `DocumentSymbol`; object-root zero-width selection caret preserved.
- **Story 2 (unsaved edits):** the handler prefers `document.Store` over the on-disk index.
- **Capability allow-list:** `documentSymbolProvider` advertised **and** asserted in `TestInitialize`.
- **FR-43:** partial-object outline works end-to-end; fuzz target present and passing.
- **Docs (review-docs):** CLAUDE.md "Project state" note and README updated to list documentSymbol as a
  shipped provider (done in `/finalize-feature`).

---

## Open questions

- **OQ-1 (kind mapping — non-blocking):** the LSP `SymbolKind` values chosen per `model.SymbolKind`
  affect only editor icon/grouping, not behavior. Proposed mapping is in T1. The genuinely debatable
  ones: `SymbolMap → Object` vs `Class`; `SymbolDataField → Field` vs `Variable`; `SymbolDDMReference →
  Struct` vs `Object`. `SymbolObject → Module` and `SymbolSubroutine → Function` should stay as-is for
  consistency with feature 10's workspace-symbol mapping. **Does the user want a specific mapping, or is
  the T1 proposal acceptable?** (Default if no answer: implement the T1 proposal.)
- **OQ-2 (data fields in the outline — non-blocking):** feature 09's tree includes `SymbolDataField`
  leaves under each data section, so a large `DEFINE DATA` will produce a deep/verbose outline. This is
  faithful to the extracted structure and matches the plan ("sections under a data definition"). **Ship
  the full field tree as-built?** (Default: yes — render the tree unchanged; do not filter fields.)
