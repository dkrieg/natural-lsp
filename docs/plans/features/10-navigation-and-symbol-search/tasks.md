# Tasks: Navigation & symbol search (feature 10)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-24 (go-to-definition), FR-25 (find-all-references), FR-26 (workspace-symbol
search). Related: FR-17 (modeled gaps), FR-30/M-6 (nothing silently dropped), ADR-008 (position
encoding), FR-43 (graceful degradation).
**Depends on:** feature 05 (workspace index — `internal/workspace`), 07 (resolution —
`internal/workspace/resolution.go`), 09 (program-structure `Structure *Symbol`). All merged to `main`.

This feature adds the **first real LSP providers**. Everything shipped so far (00–09 + embedded-SQL) is
extraction/backend that populates `model.FileAnalysis`; this feature is the LSP-facing layer that
surfaces it to the editor via `textDocument/definition`, `textDocument/references`, and
`workspace/symbol`.

---

## Current-state findings & impact

This is the **most important section** — the code diverges materially from what the plan assumes.

### F1 — The running server has NO workspace index (blocking gap)

`internal/workspace` (Index, `Build`/`BuildWithCache`, `Resolve`, `ResolutionSet`) is fully implemented
and unit-tested, **but it is never constructed by the running server**. `cmd/natural-lsp/main.go` calls
`server.Run(ctx, r, w, version, root, cfg, az, logger)` with no index; `server.Run` (server.go:157)
builds only a `document.Store` and a `document.Watcher`, both of which re-analyze *single files* in
isolation. There is no `*workspace.Index` and no `*workspace.ResolutionSet` anywhere in the server
process. `grep` for `workspace.Build`/`workspace.Resolve` in `cmd/` and `internal/server` returns
nothing.

**Impact:** go-to-definition, find-references, and workspace-symbol all require a workspace-wide view.
The very first task of this feature is to **build and hold a `workspace.Index` in the server** and make
it queryable from request handlers — this is net-new plumbing, not an extension of an existing seam.
`server.Run`'s signature must change (add the root-derived index, or build it inside `Run`), and
`main.go` must be updated. This ripples into the lifecycle: the index build is the "indexing" phase the
plan's Story-3 criterion ("available as soon as indexing completes") refers to, and incremental updates
(Story-3 last criterion) must flow from the document store / watcher back into the index.

### F2 — No LSP method handlers exist beyond lifecycle + document sync

`internal/server/handlers.go` is a package-doc + TODO stub. The dispatch switch in `Run` (server.go:481)
handles `initialize`, `shutdown`, `test/panic`, and the notifications
(`initialized`/`didOpen`/`didChange`/`didClose`/`didChangeWatchedFiles`); every other method falls to
`default → MethodNotFound` (server.go:520). The three new methods plug in as new `case` arms in that
switch, each wrapped by the existing per-request panic-recovery closure (server.go:473–479 already
recovers and sends `InternalError`) — so per-request panic recovery is **inherited for free**; a task
must add a *test* proving it for the new handlers, not new recovery code.

### F3 — The initialize capability set is a deliberately locked allow-list

`handleInitialize` (server.go:98–138) advertises ONLY `textDocumentSync: Full` and `positionEncoding`.
The doc comment explicitly says features 09–13 MUST extend this allow-list and update `TestInitialize`
(server_test.go:321) when they add a provider. `TestInitialize` currently only positively asserts
`textDocumentSync` and `positionEncoding` (it does not assert the *absence* of providers), so adding
providers will not break it — but the test must be extended to positively assert
`definitionProvider`/`referencesProvider`/`workspaceSymbolProvider` per the allow-list convention.
`protocol.ServerCapabilities` has `DefinitionProvider`, `ReferencesProvider`, `WorkspaceSymbolProvider`
fields (verified in `go.lsp.dev/protocol@v1.0.0`), each a union type accepting a bool.

### F4 — No position conversion helper exists (model 1-based → LSP 0-based, encoding-aware)

`model.Position` is **1-based line/column** (model.go:181-185). LSP `protocol.Position` is **0-based**,
and columns are counted in the *negotiated position encoding* (UTF-8 or UTF-16 code units — ADR-008).
`grep` confirms there is **no existing conversion** between the two anywhere in `internal/` (non-test).
Every provider returns `protocol.Location`/`protocol.Range`, so a conversion helper — `model.Range` +
the negotiated encoding + document content → `protocol.Range` — must be built as an early foundation
task. The negotiated encoding is currently a local in `handleInitialize` and is **not retained**; `Run`
must persist it so handlers can convert correctly. This is a genuine correctness concern for
multi-byte identifiers and the `review-lsp-protocol` reviewer will check it.

### F5 — No position→node ("what is under the cursor") lookup exists

Go-to-definition and find-references receive a `TextDocumentPositionParams` (a URI + a 0-based
Position). Nothing in the codebase maps a cursor position to the reference site (`EdgeEntry.Source` /
`DataAccessEntry.NameRange`) or symbol under it. `ResolutionSet.Get(filePath, source)` is keyed by the
*exact* `EdgeEntry.Source` range, so the handler must first find which edge's `Source` range **contains**
the cursor, then look it up. This is net-new and must be an early task (F5 lookup). It runs in
*model coordinates*: convert the incoming LSP position to a 1-based `model.Position` (inverse of F4),
then test range-containment against the file's `Edges`/`DataAccess`.

### F6 — Resolution is recomputed, not cached (OQ-1 from feature 07)

Per feature 07's ADR/OQ-1, `ResolutionSet` is deliberately **not** persisted; it is recomputed from
cached `Edges` via `Resolve(idx, cfg)`. This feature computes it once after the index build and
**recomputes incrementally** on document/watched-file updates (user decision, 2026-07-02): rather than a
full `Resolve(idx, cfg)` over the whole workspace on every mutation, only the affected file and its
dependents are re-resolved. `Resolve` is currently whole-index only — see F6a for the required extension.
This feature makes **no cache-format change** (consistent with OQ-1).

### F6a — `Resolve` is whole-index only; incremental recompute needs a scoped variant

`Resolve(idx, *config.Config)` (resolution.go:536) walks **every** file's edges via `idx.ForEach`, builds
the name index once, and returns a full `ResolutionSet`. There is no per-file or per-dependent-set
recompute entry point today. Incremental recompute (per the user decision) requires **extending the
resolution API** — the change task plus its consumer migration:

- Add a scoped recompute (e.g. `ResolveInto(rs *ResolutionSet, idx, cfg, changedPaths []string)` or a
  method that re-resolves a given set of files and merges the result into an existing set). It must
  reuse `buildNameIndex` (the name index depends on the *whole* workspace, since a target can live in
  any file — so the name index is rebuilt or updated even when only one file's edges are re-resolved),
  and re-key only the changed files' `(filePath, source)` entries.
- The dependent set comes from `Index.Invalidate(path)` (index.go:207, INCLUDE-transitive) — but note
  it is scoped to INCLUDE dependents, whereas a *definition* change (e.g. a subprogram renamed) can
  affect **callers** whose CALLNAT now resolves differently. For correctness the simplest safe scope is:
  re-resolve the changed file's own edges **and** re-resolve every file whose existing Resolution
  pointed at (or whose unresolved edge could now match) the changed object's name. The `review-acceptance`
  and `review-concurrency` reviewers must confirm the incremental set is complete (no stale entry left
  claiming a since-removed target). If a fully-correct minimal set proves subtle, the fallback is a full
  `Resolve` — but the user asked for incremental, so plan the scoped path and flag the completeness
  risk explicitly.
- **Publish discipline (F7):** build the updated set (or a copy), then publish behind the lock; never
  mutate a `ResolutionSet` that handlers may be reading.

### F7 — Concurrency: the index is read by handlers while the watcher/store mutate it

`workspace.Index` is `sync.RWMutex`-guarded (index.go:36) with snapshot-on-read (`ForEach`,
`buildNameIndex`, `Invalidate` all hold `RLock`). The document store and watcher run on background
goroutines. Once handlers read the index concurrently with watcher-driven updates, the read/write
discipline must be verified under `-race`. `ResolutionSet` itself is documented "not safe for concurrent
mutation; read-only after Resolve" — so the pattern must be: build a fresh `ResolutionSet`, then publish
it behind a lock / atomic pointer; never mutate a published set. This makes `review-concurrency`
**required**.

### F8 — What already exists and is reused (no new extraction)

- **Index & lookup:** `Index.ForEach`, `Index.Get`, `Index.Keys`, `Index.LookupByName`,
  `Index.buildNameIndex` (unexported), `Candidate{Path, Library, Type}`, `objectIdentity`.
- **Resolution:** `Resolve(idx, cfg) *ResolutionSet`; `ResolutionSet.Get(filePath, source)`,
  `.All()`, `.DiagnosticsFor(filePath)`; `Resolution` with `IsResolved/IsUnresolved/IsAmbiguous/IsDynamic`
  and `Path`/`Type`/`Reason`/`Candidates`. **Go-to-def = F5 lookup → `ResolutionSet.Get` → Resolution's
  `Path`+definition range.** Note `Resolution.Path` gives the *file*, not the definition's *range within*
  the file — the target range comes from that file's `Structure.SelectionRange` (feature 09) or
  `{1,1}` fallback.
- **Structure tree:** `model.FileAnalysis.Structure *model.Symbol` (feature 09) with `Kind`, `Name`,
  `Range`, `SelectionRange`, `Children`. **Workspace-symbol = walk `Structure` across all indexed files.**
- **Data-access refs:** `model.DataAccessEntry{Name, Kind, Source, NameRange}` — the DDM-field reference
  sites for find-references (Story-2 "DDM field").
- **Model contract:** all consumed read-only. **No `internal/model` change and no cache-format bump.**

### F9 — Divergence from CLAUDE.md / README

CLAUDE.md's "Project state" says "higher-level LSP providers … remain as stubs." That is accurate — this
feature is the first to change it. No misleading divergence, but `review-docs` applies: CLAUDE.md's
initialize-capabilities note ("no feature providers yet") and README's feature list must be updated at
`/finalize-feature`.

### Criterion → disposition map

| Story | Criterion | Disposition |
|---|---|---|
| 1 | go-to-def on module-call target | new (T4/T7) |
| 1 | go-to-def on FETCH/RUN + PERFORM (inline-before-external, steplib) | new (T7); resolution already handles the rules |
| 1 | dynamic/unresolved → no destination, no error | new (T8); `Resolution.IsUnresolved/IsDynamic` already models it |
| 1 | ambiguous (no lib map) → defined behavior + diagnostic | new (T9); **OQ-1 answered → return all candidates** |
| 2 | find-refs on subroutine/program/DDM field across workspace | new (T10/T11) |
| 2 | cross-file refs, each with file + position | new (T11, multi-file fixture) |
| 2 | dynamic/unresolved refs not falsely claiming a link | new (T12) |
| 2 | complete w.r.t. index (multi-file fixture) | new (T11) |
| 3 | workspace symbol returns programs & subroutines by name | new (T13) |
| 3 | case-insensitive matching | new (T13) |
| 3 | each result carries location + kind | new (T13) |
| 3 | available after indexing; reflects incremental updates | new (T2 build + T13a scoped resolve + T14 incremental) |
| all | capability advertisement in initialize | new (T3) |
| all | position encoding correctness (ADR-008) | new (T1 helper + asserted in every provider test) |
| all | per-request panic recovery for new handlers | inherited (F2); proven by test (T15) |

---

## Open-question answers (resolved)

**OQ-1 (plan): go-to-definition on an ambiguous, no-library-map name — first candidate vs picker.**
**Decision: return ALL candidates.** `textDocument/definition` may return a `Location[]` (LSP allows an
array). This is consistent with feature 07's `Ambiguous` outcome, which already carries
`Resolution.Candidates []string` (sorted, deterministic) and already emits an ambiguity diagnostic via
`ResolutionSet.DiagnosticsFor`. Returning all candidate Locations lets the editor present a picker; we do
not silently pick one. (Confirmed against as-built `Resolution.Candidates` + `Ambiguous(...)`.)

**OQ-2 (plan): do references include comment/string occurrences, or only true references.**
**Decision: TRUE references only.** This is an index-backed reference search: the extractor indexes
`Edges`/`DataAccess`/`Structure`, never comment or string-literal text. FR-25 defines completeness "with
respect to the index," so a reference the index does not know about is out of scope by construction. No
text scanning is added.

**OQ-3 (harness): does go-to-definition need a NEW position→edge/symbol lookup?**
**Yes (F5).** `ResolutionSet.Get` is keyed by the *exact* `EdgeEntry.Source` range; the handler receives
only a cursor position. A containment lookup (position ∈ which edge's `Source`) is net-new — planned as
early task **T5**.

**OQ-4 (harness): how do dynamic/unresolved targets yield "no definition found"?**
Go-to-def on a `Resolution` that `IsUnresolved()` (either `ReasonDynamic` or `ReasonNoTarget`) returns an
**empty result** (`null` / empty `Location[]`), never a JSON-RPC error. This is the FR-17 modeled-gap
contract: the site still appears in find-references, but there is no destination to jump to.

---

## Task list

Ordering: **foundations (server holds index + position conversion + cursor lookup) → definition →
references → workspace-symbol → cross-cutting (capabilities, panic-recovery, incremental, robustness).**

Every LSP-handler task drives either the exported handler or an internal provider function and asserts
the concrete `protocol` response (Locations/SymbolInformation with correct URIs, ranges, kinds).
Ranges must be asserted in the **negotiated encoding** (T1). Fixtures are minimal, sanitized, multi-file
Natural workspaces under a new `internal/server/testdata/navigation/` tree (or reuse
`internal/workspace/testdata/resolution/` fixtures where they already cover the input — check them
first). All new fixtures are `.NSx` with realistic extensions so `objectIdentity`/ObjectType classify
them correctly.

---

### T1 — Position/Range conversion: `model` (1-based) ↔ `protocol` (0-based, encoding-aware)

- **Behavior:** A helper that converts a `model.Range` to a `protocol.Range` given the document content
  and the negotiated `PositionEncodingKind`, and the inverse (a `protocol.Position` → 1-based
  `model.Position`) for the cursor lookup (T5). Column translation must count UTF-16 code units when the
  negotiated encoding is UTF-16 and bytes/UTF-8 code points when UTF-8 (ADR-008). Line is a simple −1/+1.
- **Fixtures:** none (unit test with inline content, incl. a line containing a multi-byte character to
  prove UTF-16 vs UTF-8 column divergence).
- **Expected result:** `{Line:1,Column:1}` (model) ↔ `{Line:0,Character:0}` (LSP); a multi-byte char
  before the column yields different LSP `Character` under UTF-8 vs UTF-16.
- **Reuses/migrates:** new file (suggest `internal/server/position.go`). Depends only on `internal/model`
  + `go.lsp.dev/protocol` — LSP-facing side of the seam, correct.
- **DoD:** table-driven test incl. multi-byte case for both encodings; boundary (col 1, empty line);
  `gofmt`/`vet` clean; deterministic; no model change.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** none.

### T2 — Server builds and holds a `workspace.Index` (+ retained position encoding)

- **Behavior:** `server.Run` builds the workspace index at startup via
  `workspace.BuildWithCache` (or `Build`) using `root`, `cfg`, `az`, and holds it for the request loop.
  Retain the negotiated `PositionEncodingKind` (from `handleInitialize`) on the server so handlers can
  convert (T1). Wire `main.go` accordingly. The index build is the "indexing" phase Story-3 references.
  **This is the F1 plumbing task and is a prerequisite for every provider.**
- **Fixtures:** a small multi-file workspace fixture (2–3 `.NSx`) to prove the index is populated and
  queryable after `Run` reaches `initialized`.
- **Expected result:** after `initialized`, `Index.Keys()` (or a test seam) returns the fixture files;
  encoding is retained and matches the negotiated value.
- **Reuses/migrates:** `workspace.Build`/`BuildWithCache` (existing, unit-tested). **Migrates
  `server.Run`'s signature/wiring and `cmd/natural-lsp/main.go`** — existing server tests that call
  `Run(...)` must still compile/pass (update call sites). Keep the `document.Store`/`Watcher` wiring;
  decide whether the index build happens before or lazily after `initialized` (recommend: kick off after
  `initialized`, guard handlers to return empty until ready — Story-3 "available as soon as indexing
  completes").
- **DoD:** existing server tests green after signature change; index reachable from dispatch; `-race`
  clean (background build vs request loop); `vet`/`gofmt`; Analyzer seam preserved (server depends on
  `analysis.Analyzer` + `workspace`, not parser internals).
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** none (but T3–T15 depend on it).

### T3 — Advertise the three providers in `initialize` (extend the locked allow-list)

- **Behavior:** `handleInitialize` sets `DefinitionProvider`, `ReferencesProvider`,
  `WorkspaceSymbolProvider` (bool `true`) in `ServerCapabilities`. Extend `TestInitialize` to positively
  assert all three per the F3 allow-list convention. Update the `handleInitialize` doc comment.
- **Fixtures:** none (uses the existing `TestInitialize` JSON).
- **Expected result:** initialize result JSON contains `definitionProvider`, `referencesProvider`,
  `workspaceSymbolProvider`; `textDocumentSync`/`positionEncoding` unchanged.
- **Reuses/migrates:** `handleInitialize` (server.go:98), `TestInitialize` (server_test.go:321).
- **DoD:** `TestInitialize` extended and green; no other capability regressed; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T2 (encoding retention lands alongside).

### T4 — `textDocument/definition` handler skeleton + dispatch wiring

- **Behavior:** Add a `case "textDocument/definition"` to the dispatch switch (server.go:481), gated on
  `stateInitialized`, decoding `protocol.DefinitionParams`, calling a `provideDefinition(...)` provider
  function, and marshalling the result. For this slice, wire the plumbing and return an **empty result
  for a not-found cursor** (no edge under the position). Real resolution lands in T5–T9.
- **Fixtures:** single-file fixture with a cursor position that hits no reference.
- **Expected result:** valid JSON-RPC response with `null`/empty `Location[]`; no error.
- **Reuses/migrates:** dispatch switch, per-request panic recovery closure (inherited, F2). New provider
  in `handlers.go` (or `definition.go`).
- **DoD:** unknown-cursor returns empty, not error; `vet`/`gofmt`; unmarshalling of DefinitionParams
  covered.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T2, T1.

### T5 — Position→edge/DataAccess containment lookup (F5)

- **Behavior:** Given a file's `FileAnalysis` and a 1-based `model.Position` (converted from the LSP
  position via T1), return the `EdgeEntry` (or `DataAccessEntry`) whose reference range **contains** the
  position, if any. Reference range for edges is `EdgeEntry.Source`; for data access it is
  `DataAccessEntry.NameRange` (the view-name token, not the whole statement). Deterministic tie-break
  (smallest containing range) if ranges overlap.
- **Fixtures:** a `.NSP` with a `CALLNAT 'SUB'` and a `READ VIEW` so both a call site and a data-access
  site can be probed; cursor inside the target token vs outside it.
- **Expected result:** cursor on the `CALLNAT` target → that `EdgeEntry`; cursor on the DDM name → that
  `DataAccessEntry`; cursor on whitespace → nothing.
- **Reuses/migrates:** T1 inverse conversion; reads `FileAnalysis.Edges`/`DataAccess`. New function
  (suggest `internal/server/cursor.go`).
- **DoD:** table-driven (inside/edge/outside a token, edge boundaries); deterministic; `vet`/`gofmt`; no
  model change.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T1.

### T6 — Definition target range from the resolved file's `Structure`

- **Behavior:** Given a resolved definition (`Resolution.Path` + `Type`), produce the target
  `protocol.Location`: URI from path (workspace-relative → `file://` via `root`), and range from the
  target file's `Structure.SelectionRange` (the object root's name span, feature 09), falling back to
  `{1,1}→{1,1}` if `Structure` is nil (FR-43). Converted to LSP coords via T1 using the *target* file's
  content (from the index / on-disk).
- **Fixtures:** a two-file fixture: caller `.NSP` + target `.NSN` subprogram with a `Structure` root.
- **Expected result:** Location URI = target file, range = target's object-name selection span in the
  negotiated encoding.
- **Reuses/migrates:** `model.Symbol.SelectionRange` (feature 09), T1. Reads the target `FileAnalysis`
  from the index.
- **DoD:** nil-`Structure` fallback covered; URI round-trips through `go.lsp.dev/uri`; `-race` if it
  reads the index concurrently; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T2, T1.

### T7 — Go-to-definition end-to-end: CALLNAT / FETCH-RUN / PERFORM (Story 1, criteria 1–2)

- **Behavior:** `provideDefinition` composes T5 (cursor→edge) → `ResolutionSet.Get(filePath,
  edge.Source)` → if `IsResolved`, T6 (target Location). Covers CALLNAT→subprogram, FETCH/RUN→program,
  PERFORM→(inline subroutine in same file, else external `.NSS`) — the resolution rules are already
  implemented in feature 07; this task only surfaces them. **Inline PERFORM** resolves to the *same file*
  (`Resolution.Path == filePath`) at the subroutine's `DEFINE SUBROUTINE` range — use the file's
  `Structure` child of kind `SymbolSubroutine` matching the target name for the range (not the object
  root). Confirm the resolution set is queried, not recomputed per request.
- **Fixtures:** multi-file workspace: a program that `CALLNAT`s a subprogram, `FETCH`es/`RUN`s a program,
  `PERFORM`s both an inline and an external subroutine (reuse `internal/workspace/testdata/resolution/`
  fixtures if they cover these — check first).
- **Expected result:** each cursor → correct target Location (file + range + encoding); inline PERFORM →
  same-file subroutine range.
- **Reuses/migrates:** T5, T6, `Resolve`/`ResolutionSet.Get`, `Structure` subroutine child lookup.
- **DoD:** one assertion per edge kind (split into sub-tests, not one fat test); inline-vs-external
  distinguished; `-race`; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T5, T6, T2.

### T8 — Go-to-definition on dynamic/unresolved target → empty (Story 1, criterion 3; OQ-4)

- **Behavior:** When the edge under the cursor resolves to `IsUnresolved()` (either `ReasonDynamic`,
  e.g. `CALLNAT #VAR`, or `ReasonNoTarget`, a literal matching nothing), return an **empty result**
  (`null`/empty `Location[]`), never an error. FR-17 modeled gap.
- **Fixtures:** a `.NSP` with `CALLNAT #DYN` (dynamic) and `CALLNAT 'MISSING'` (no target).
- **Expected result:** both cursors → empty result; response is well-formed, no JSON-RPC error.
- **Reuses/migrates:** `Resolution.IsUnresolved`/`IsDynamic`.
- **DoD:** both reason kinds asserted; no error path; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T7.

### T9 — Go-to-definition on ambiguous name → all candidate Locations (Story 1, criterion 4; OQ-1)

- **Behavior:** When the edge resolves to `IsAmbiguous()`, return **all** `Resolution.Candidates` as a
  `Location[]` (each with its target-file range via T6). Confirm the ambiguity diagnostic already
  produced by `Resolve` is available via `ResolutionSet.DiagnosticsFor` (surfaced by the diagnostics
  path, not this handler — assert its presence to satisfy the plan's "diagnostic is present" clause).
- **Fixtures:** a no-library-map workspace with two objects of the same name/type in different dirs (a
  flat-namespace ambiguity) plus a caller — reuse a resolution ambiguity fixture if one exists.
- **Expected result:** `definition` returns 2 Locations (sorted, deterministic per `Candidates` sort);
  `DiagnosticsFor(caller)` is non-empty with a warning.
- **Reuses/migrates:** `Resolution.Candidates`, `Ambiguous`, `ResolutionSet.DiagnosticsFor`, T6.
- **DoD:** multi-candidate order deterministic; diagnostic presence asserted; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T7.

### T10 — `textDocument/references` handler skeleton + reverse-reference sweep primitive

- **Behavior:** Add `case "textDocument/references"` (decode `protocol.ReferenceParams`), gated on
  `stateInitialized`, calling `provideReferences(...)`. Add the underlying **reverse-reference sweep**:
  given a target symbol identity (object name + optional type, from the definition under the cursor OR
  the cursor's own symbol), scan `Index.ForEach` for every `EdgeEntry`/`DataAccessEntry` whose *resolved*
  target is that symbol, collecting their `Source`/`NameRange` sites as Locations. Include the
  declaration site itself when `ReferenceContext.IncludeDeclaration` is true.
- **Fixtures:** single-file fixture for the skeleton path (cursor with no symbol → empty result).
- **Expected result:** plumbing returns empty for a no-symbol cursor; the sweep function returns the
  correct sites for a known symbol (unit-tested directly on a built index).
- **Reuses/migrates:** `Index.ForEach`, `Resolve`/`ResolutionSet` (invert: for each edge, check if its
  Resolution `.Path`/name equals the target), T5 (identify the symbol under the cursor). New provider
  (`references.go`).
- **DoD:** empty for no-symbol; sweep is O(files·edges) but single-pass acceptable for MVP; deterministic
  (sorted by URI then range); `-race`; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T2, T5, T1.

### T11 — Find-all-references across the workspace, cross-file (Story 2, criteria 1, 2, 4)

- **Behavior:** `provideReferences` end-to-end: cursor on a subroutine / program / DDM-field definition
  (or a reference to it) → all referencing sites across the workspace, each a `protocol.Location` (file +
  range in negotiated encoding). Completeness "with respect to the index" (OQ-2: true references only).
- **Fixtures:** **multi-file workspace** where ≥3 files reference the same subprogram/program, plus a DDM
  field referenced from multiple files' `READ`/`FIND`. This is the completeness anchor.
- **Expected result:** exactly the set of known reference sites (no more, no fewer), sorted; cross-file
  Locations carry correct URIs and ranges.
- **Reuses/migrates:** T10 sweep; `DataAccessEntry.NameRange` for DDM-field refs; T6/T1 for Locations.
- **DoD:** completeness asserted against the fixture (exact set); cross-file URIs correct; DDM-field
  case included; deterministic order; `-race`; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T10.

### T12 — References include dynamic/unresolved sites without a false link (Story 2, criterion 3)

- **Behavior:** Dynamic (`CALLS_DYNAMIC`/`NAVIGATES_TO_DYNAMIC`) and unresolved-literal reference sites
  are **not** attributed as resolved references to a specific target (they can't be — the target is
  unknown). Decide representation: for MVP, a dynamic/unresolved site is **excluded** from a specific
  symbol's reference list (it does not falsely claim a link), which satisfies "does not falsely claim a
  resolved link." Document this explicitly in the provider. (They remain visible via the diagnostics /
  outline paths per FR-17/M-6, not invented here.)
- **Fixtures:** a workspace with a real `CALLNAT 'SUB'` and a `CALLNAT #DYN` to the same conceptual name;
  find-refs on `SUB` returns only the static site.
- **Expected result:** the dynamic site is absent from `SUB`'s reference list; no false link.
- **Reuses/migrates:** `Resolution.IsDynamic`/`IsUnresolved`.
- **DoD:** dynamic site excluded and asserted; comment documents the modeled-gap rationale; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T11.

### T13 — `workspace/symbol` search over the index `Structure` (Story 3, criteria 1–3)

- **Behavior:** Add `case "workspace/symbol"` (decode `protocol.WorkspaceSymbolParams`, `Query` field),
  gated on `stateInitialized`, calling `provideWorkspaceSymbols(query)`. Walk every indexed file's
  `Structure` tree; match program/subprogram objects and subroutines by name **case-insensitively**
  (Natural identifiers) against the query (relaxed substring per LSP guidance is acceptable; at minimum
  case-insensitive contains). Return `[]protocol.SymbolInformation` with `Name`, `Kind`
  (map `SymbolObject`→`SymbolKindModule`/`Class` appropriately, `SymbolSubroutine`→`SymbolKindFunction`
  or `Method`), and `Location` (file URI + `SelectionRange` in negotiated encoding).
- **Fixtures:** multi-file workspace with programs and subroutines of varied casing; query in lower/upper.
- **Expected result:** matching programs + subroutines returned with correct kind + location; matching is
  case-insensitive; empty query behavior defined (return all or empty — pick and assert).
- **Reuses/migrates:** `model.FileAnalysis.Structure`, `Index.ForEach`, `model.SymbolKind`→
  `protocol.SymbolKind` mapping (new small helper), T1/T6 for Locations.
- **DoD:** case-insensitivity asserted (upper query matches lower name); kind mapping asserted;
  deterministic order (sort by name then URI); `-race`; `vet`/`gofmt`; no model change.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T2, T1.

### T13a — Scoped/incremental resolution recompute (F6a — resolution API extension)

- **Behavior:** Add a scoped recompute to `internal/workspace/resolution.go` that re-resolves a given set
  of changed files (and the dependent set that a definition change can affect) and merges the outcome
  into an existing `ResolutionSet`, rather than re-walking the whole index. Rebuild/refresh the name
  index (targets can live anywhere), re-key only the affected `(filePath, source)` entries, and drop
  stale entries for edges that no longer exist. Keep `Resolve` (whole-index) as the initial-build path.
- **Fixtures:** reuse `internal/workspace/testdata/resolution/` — a multi-file set where changing one
  file's content flips a resolution outcome (e.g. adding the missing target file, or renaming it away).
- **Expected result:** after recompute for `changedPaths`, the merged `ResolutionSet` equals what a full
  `Resolve` would produce for that state (assert equality against a full `Resolve` on the same index) —
  this is the completeness guard F6a calls out.
- **Reuses/migrates:** `buildNameIndex`, `resolveByName`, `Index.Invalidate` (INCLUDE dependents) plus a
  caller-scope pass for definition changes; **migrates** any test that assumed `Resolve` was the only
  entry point. Model-pure (no cache change, OQ-1).
- **DoD:** merged set == full `Resolve` for the changed state (completeness); no stale entry retained;
  deterministic; `-race`; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T2.

### T14 — Incremental updates reflected in symbol search & navigation (Story 3, criterion 4)

- **Behavior:** When a document changes (`didChange`) or a watched file changes, wire the
  `document.Store` / `Watcher` re-analysis back into the server-held `Index` (`Index.Add(relPath, fa)`),
  then **incrementally recompute** resolution via T13a for the changed file + its affected dependents
  (user decision: incremental, not full re-`Resolve`), and publish the updated `ResolutionSet` behind the
  lock (F6/F7: build-then-publish; never mutate a published set). Subsequent
  `workspace/symbol`/`definition`/`references` return updated results.
- **Fixtures:** a fixture where an initial `workspace/symbol` result changes after a simulated `didChange`
  adds/renames a subroutine, AND a `definition` outcome that flips after the change (proving the
  incremental resolution scope caught the affected caller, not just the changed file).
- **Expected result:** pre-change query omits the new symbol and the caller's definition is unresolved;
  post-change query includes it and the caller now resolves — matching a full-rebuild baseline.
- **Reuses/migrates:** `Index.Add`, T13a scoped recompute, `Index.Invalidate`, the store/watcher analyze
  callbacks (server.go:185/193).
- **DoD:** before/after assertion for both symbol search and definition; incremental result matches a
  full-rebuild baseline (no stale/missed entry); `-race` (concurrent update vs query); `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T13a, T13 (and T7 for the definition-flip assertion).

### T15 — Per-request panic recovery + malformed-params handling for the new handlers

- **Behavior:** Prove the three new handlers are covered by the existing per-request panic-recovery
  closure (F2): a handler panic returns a JSON-RPC `InternalError` and keeps the loop alive; malformed
  params return `InvalidParams` (or empty result, per existing convention) without crashing. No new
  recovery *code* — this is a test task confirming inherited behavior + explicit malformed-params guards.
- **Fixtures:** none (crafted malformed JSON params per method).
- **Expected result:** malformed `DefinitionParams`/`ReferenceParams`/`WorkspaceSymbolParams` →
  well-formed error/empty response, server continues; an induced panic → `InternalError`, loop alive.
- **Reuses/migrates:** the recovery closure (server.go:473); existing `TestRequestPanicRecovery` pattern.
- **DoD:** all three methods covered for malformed input; server processes a follow-up request after the
  fault; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T4, T10, T13.

### T16 — Robustness: fuzz the cursor lookup + providers over arbitrary positions (optional-but-recommended)

- **Behavior:** A `FuzzProvide*` target (or one guarding the cursor-containment lookup) that feeds
  arbitrary positions/content and asserts the providers never panic and always return a well-formed
  result (FR-43, ADR-013 precedent: `FuzzProcessFile`, `FuzzResolve`, `FuzzParse`, `FuzzExtractSQL`).
- **Fixtures:** fuzz seed corpus of a couple of `.NSx` snippets + positions.
- **Expected result:** no panic; non-nil well-formed result for any input.
- **Reuses/migrates:** existing fuzz-target conventions.
- **DoD:** fuzz target compiles and runs a short corpus in CI; `vet`/`gofmt`.
- **Agents:** tdd-red → tdd-green → tdd-refactor.
- **Depends on:** T5, T7, T11, T13.

---

## Model / cache-format impact

**No `internal/model` change and no cache-format bump.** All three providers read existing members
(`Edges`, `DataAccess`, `Structure`, `Definitions`) and existing resolution APIs. Resolution is
recomputed (OQ-1), not persisted. This feature adds **server capabilities only** (initialize response) —
no persisted-format change.

**Seam note:** all new code is LSP-facing and depends only on `internal/model`, `internal/workspace`
(Index/Resolution — model-pure), `internal/document`, and `go.lsp.dev/{protocol,jsonrpc2,uri}` — never
on parser internals. The Analyzer seam is preserved. Two contract changes, both internal to their
packages: **`server.Run`'s signature/wiring** (T2, server + `main.go`) and the **`internal/workspace`
resolution API** (T13a adds a scoped/incremental recompute alongside `Resolve`; model-pure, no cache
change). Existing resolution tests that treated `Resolve` as the sole entry point are the migration
consumers for T13a.

---

## Reviews required (for `/review-feature`)

- **review-lsp-protocol** — REQUIRED (first providers): capability advertisement matches handled methods
  (T3 vs T4/T10/T13); position-encoding correctness in every returned range (ADR-008, T1); `Location[]`
  vs single `Location` for definition (OQ-1); lifecycle gating (handlers only in `stateInitialized`);
  MethodNotFound no longer returned for the three methods; malformed-params handling (T15).
- **review-concurrency** — REQUIRED (F7): server-held Index read by handlers concurrently with
  store/watcher writes and the incremental resolution recompute (T13a/T14); `-race`; build-then-publish
  discipline for `ResolutionSet` (never mutate a set handlers may read).
- **review-acceptance** — every criterion in the disposition map traced to a task and asserted.
- **review-seam** — REQUIRED: confirm `server.Run` signature change (T2) and the `internal/workspace`
  resolution API extension (T13a scoped recompute) stay model-pure with no cache-format change, and that
  all new providers keep the Analyzer seam (no parser-internal imports; depend on `analysis.Analyzer` +
  `internal/workspace`).
- **review-robustness** — cursor lookup / providers over arbitrary positions (T16, FR-43).
- **review-docs** — CLAUDE.md "Project state" (providers no longer stubs; initialize now advertises three
  providers) and README feature list must sync at `/finalize-feature` (F9).

---

## Open questions (for the plan-approval checkpoint)

1. **`server.Run` index wiring (biggest decision).** F1: the server currently holds no index. Two
   shapes: (a) build the index *inside* `Run` after `initialized` (self-contained, matches Story-3
   "available as soon as indexing completes"); or (b) build it in `main.go` and pass `*workspace.Index`
   into `Run` (testable, but changes the exported signature more). Recommendation: **(a)** — build inside
   `Run`, expose a test seam. Needs your confirmation as it sets the pattern for features 11–13.

2. **RESOLVED (user, 2026-07-02): incremental resolution recompute.** Full `Resolve` runs once after the
   initial index build; document/watched-file mutations recompute **incrementally** (T13a + T14) for the
   changed file and its affected dependents, published behind the lock. F6a flags the completeness risk
   (a definition change affects callers, not just INCLUDE dependents) — T13a's DoD guards it by asserting
   the merged set equals a full `Resolve` for the changed state. `review-acceptance` + `review-concurrency`
   must confirm the incremental scope is complete and race-free.

3. **Definition target range granularity.** T6/T7: jump to the *object* selection range (`Structure`
   root name span) for module targets, and to the *subroutine* child's range for inline PERFORM. Confirm
   this is the desired cursor landing (vs. always the file top). Feature 09's `SelectionRange` supports
   both.

4. **`workspace/symbol` scope.** T13: MVP returns program/subprogram *objects* and *subroutines* (the
   plan's Story-3 wording). Confirm data-fields/maps are **out of scope** for this feature (they belong
   to outline, plan 10-outline / FR-27, explicitly out-of-scope here).

5. **References representation of dynamic sites.** T12: MVP **excludes** dynamic/unresolved sites from a
   specific symbol's reference list (they cannot claim a resolved link). The plan allows "or are clearly
   distinguished" — confirm exclusion is acceptable vs. a future richer representation.

6. **Empty `workspace/symbol` query.** T13: return all symbols vs. empty for `query == ""`. LSP allows
   empty query to mean "all." Recommendation: return all (bounded by index). Confirm.
