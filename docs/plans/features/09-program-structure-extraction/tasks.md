# Tasks: Program-structure extraction (feature 09)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-23 (P0). Robustness/graceful-degradation obligations: FR-43, M-6, FR-30.
**Depends on:** 00-parser-foundation (shipped), features 06/07/08/08b (shipped — this feature reuses
their extractors and their model members but changes no existing extractor behavior).
**Downstream consumers (not in scope here):** document outline (feature 10/11), workspace symbol
search (feature 09-navigation), hover (feature 11). This feature only produces the model.

---

## Current-state findings & impact

Ground truth was established by reading `internal/analysis/natural/ast.go`, `analyzer.go`, `data.go`,
`calls.go`, `sql.go`, `parser.go`, `internal/model/model.go`, and `internal/workspace/cache.go`.

### What the parser/AST already exposes (with ranges) — REUSE, do not re-parse

The recursive-descent parser already produces, for every file, a `*natural.Program` (`FileAnalysis.AST`)
whose nodes carry real source positions (`StartPos`/`EndPos`, and finer `*Range` fields):

- **`Program`** — the root node, with `StartPos`/`EndPos`. It has **no `Name` field**; object identity
  is not in the AST today (see gap below).
- **`Subroutine`** (`ast.Subroutines`) — `Name`, `StartPos`, `EndPos`, optional inline `DataSection`.
  Parsed from `DEFINE SUBROUTINE … END-SUBROUTINE`. Note: the parser does **not** currently distinguish
  an inline subroutine from an external one — every `DEFINE SUBROUTINE` becomes a `Subroutine` node
  regardless of enclosing object type (see OQ-1 answer for how we treat this).
- **`DataSection`** (`ast.DataSections`) — `Kind` (`local`/`parameter`/`global`/`linkage`, lower-cased),
  `StartPos`/`EndPos`, `Fields []*DataField`. One node per section keyword.
- **`DataField`** (nested inside `DataSection`/`Map`) — `Level`, `Name`, `Type`, `Dimensions`,
  `Redefines`, `Children`, with positions. Already flattened into `model.DataDefinition` by feature 08.
- **`Map`** (`ast.Maps`) — `Name`, `StartPos`/`EndPos`, `Fields`. Parsed from `DEFINE MAP … END-MAP`.
- **DDM-reference sources:** `ReadStatement`/`FindStatement`/`GetStatement`/`StoreStatement` (Adabas
  views), SQL `FromTables`/`IntoTable`/`Table`/`FromTable`, `ProcessSQLStatement.DDMName`,
  `IncludeStatement` (copycode). These are already flattened into `FileAnalysis.DataAccess`
  (`model.DataAccessEntry` with `Name`/`NameRange`/`Kind`) by feature 08 (`extractDataAccess`) and 08b
  (`extractSQLAccess`), and into `FileAnalysis.Edges` (`EdgeIncludes`) by feature 06.

### What `internal/model` already carries — and why it is NOT sufficient as-is

`FileAnalysis` already has: `ObjectType`, `AST`, `Diagnostics`, `Symbols` ([]`SymbolEntry`), `Edges`,
`DataAccess`, `Definitions` ([]`DataDefinition` — hierarchical fields with ranges, section kind,
REDEFINE nesting), `WorkFiles`, `HostVarRefs`.

The outline/symbol/hover consumers need a **single, kind-tagged, walkable hierarchy rooted at the object**
that unifies: object root → data sections → (fields) , subroutines, maps → (fields), and DDM references.
The existing members do **not** provide this:

- `Definitions`, `DataAccess`, `Edges` are **flat, parallel slices**, each backend-oriented and keyed by
  a different concept (data items vs. access edges vs. call edges). None carries the object root, and no
  slice ties subroutines + data sections + maps + DDM refs into one tree.
- `SymbolEntry`/`SymbolKind` exist but are a near-stub: `SymbolKind` has a single constant
  (`SymbolProgram`), `SymbolEntry` is **flat** (`Name`, `Kind`, `Range` — no children, no
  selection-range), and `FileAnalysis.Symbols` is currently populated by nobody in this backend. It
  cannot represent nested structure (AC "nested structure represented hierarchically").
- The object **root name** is not derived anywhere: `Program` has no `Name`, and no code computes it
  from the file path. A structural model must supply it.

**Decision — this feature adds a NEW model type.** See OQ-1. We add `model.Symbol` (a recursive,
kind-tagged node: `Kind`, `Name`, `Range`, `SelectionRange`, `Children`), a `model.SymbolKind` constant
set, and `FileAnalysis.Structure *model.Symbol` (the per-object root, nil for files that produce no
structure). We do **not** repurpose the flat `SymbolEntry`/`Symbols` (it stays as-is to avoid churning
an unrelated stub; a later navigation feature may retire it). `model.Symbol` is populated *from* the
existing `Definitions`/`DataAccess`/`Edges`/`AST` — no existing extractor changes behavior.

### Cache-format impact — CONFIRMED bump 0.5.0 → 0.6.0

`internal/workspace/cache.go` is at `cacheFormatVersion = "0.5.0"`. `cacheEntry` persists
`Symbols/Edges/DataAccess/Definitions/WorkFiles/HostVarRefs` but **not** the new `Structure`. Persisting
`Structure` (so the outline/symbol providers can serve from cache without re-analysis) requires adding a
`Structure` field to `cacheEntry` and its Save/Load round-trip, which **bumps `cacheFormatVersion` to
`"0.6.0"`** and forces a full rebuild on upgrade (the existing version-mismatch path already handles
this — Task 6 adds a regression test).

### Wiring pattern (REUSE)

The new extractor wires into `Analyzer.Analyze` (`analyzer.go`) exactly like `extractEdges`/
`extractDataAccess`/`extractDefinitions`: after the AST is parsed and the other extractors run,
`result.Structure = extractStructure(path, ast, result.Definitions, result.DataAccess, result.Edges)`.
It runs only when `ast != nil`, never panics over a partial AST (FR-43), and emits **no diagnostics**
(syntax diagnostics already flow via `Program.Diagnostics` → `FileAnalysis.Diagnostics`).

### Seam

All work is on the **extraction-backend side** of the Analyzer seam plus a purely-additive `internal/model`
change. LSP-facing packages keep consuming only `internal/model` + `analysis.Analyzer` (they will read
`FileAnalysis.Structure`, never `natural.*`). `seam_test.go` guards this — Task 5 confirms it still passes.

### Story-2 robustness — already satisfied by existing behavior (confirm, don't rebuild)

Story 2 requires: (a) an object with some unrecognized lines still yields structure for recognized parts,
and (b) unrecognized statement-like lines surface as diagnostics, not dropped. Both are **already true**
of the parser: it is error-recovering (emits ranged `Program.Diagnostics` for malformed statements and
retains valid surrounding nodes — CLAUDE.md, FR-30/M-6). Because `extractStructure` walks the AST the
parser already produced, partial input yields partial structure for free. **This feature adds no new
diagnostic logic** — Task 4 only *verifies* the behavior with a partial fixture and asserts the diagnostic
channel and the structure channel stay separate (FR-17/M-6).

---

## Open questions — resolved

### OQ-1 — Required symbol kinds for the first release, and the new model type

**Answer.** Add `model.Symbol` + `model.SymbolKind` with this **first-release kind set**, each grounded
in an AST node that already exists:

| `SymbolKind`             | Source (already in AST/model)                              | Name source                    |
|--------------------------|------------------------------------------------------------|--------------------------------|
| `SymbolObject`           | the object root                                            | filename base (no extension)   |
| `SymbolSubroutine`       | `ast.Subroutines` (`DEFINE SUBROUTINE`)                    | `Subroutine.Name`              |
| `SymbolDataSection`      | `ast.DataSections` (`Definitions` grouped by section)      | section kind (e.g. `LOCAL`)    |
| `SymbolDataField`        | `model.DataDefinition` (incl. REDEFINE-nested children)    | `DataDefinition.Name`          |
| `SymbolMap`              | `ast.Maps` (`DEFINE MAP`)                                  | `Map.Name`                     |
| `SymbolDDMReference`     | `FileAnalysis.DataAccess` READ/FIND/GET/STORE + SQL tables | `DataAccessEntry.Name`         |

`model.Symbol` shape: `{ Kind SymbolKind; Name string; Range Range; SelectionRange Range; Children []Symbol }`.
`Range` is the whole-construct span; `SelectionRange` is the name/identifier span (mirrors LSP
`DocumentSymbol` so feature 10 maps 1:1; for the object root and data sections, `SelectionRange` == the
best available name token or the construct start). The root object node's `Children` are (in this order):
data sections (each with its fields as children), subroutines, maps (each with its fields as children),
and DDM references — a deterministic, source-ordered tree.

**Kind-tagging & case:** names captured **as written** (using the value already normalized by the AST/
model — note the existing extractors upper-case data/DDM names; the object/subroutine/map names are kept
as the parser produced them). Matching is the consumer's concern and is case-insensitive by convention;
this feature stores names, it does not match.

**Deferred (call out explicitly, not in first release):**
- **Inline vs. external subroutine distinction** — the parser does not currently separate them; every
  `DEFINE SUBROUTINE` is one `SymbolSubroutine`. Distinguishing inline (within a program) from external
  (`.NSS` file) is deferred; it is not needed for outline and would require parser changes.
- **Helproutine / class / function / dialog / adapter (.NSH/.NS4/.NS7/.NS3/.NS8) object-specific
  structure** — these get a `SymbolObject` root and whatever generic constructs (data sections,
  subroutines, maps, DDM refs) the parser already recognizes, but **no type-specific members** (e.g.
  class methods/properties, function signatures). Deferred to their own features.
- **INCLUDE (copycode) as a structure node** — `EdgeIncludes` stays an edge, not a `Symbol`; copycode is
  a compile-time textual dependency, better shown as a reference than an outline child. Deferred.
- **Work files / host-var refs** — remain their existing flat members; not surfaced in the structure tree.

### OQ-2 — Fixed-format / column-sensitive legacy syntax scope

**Answer.** **No new column/fixed-format handling.** Structure extraction **rides on the existing
parser**, which already normalizes case and handles multi-line continuation (CLAUDE.md). We add zero
column rules. Story 2's two requirements are met by the parser's existing error-recovery (partial AST +
ranged diagnostics), which Task 4 verifies rather than extends. Anything the parser cannot recognize
today remains a parser diagnostic (FR-30/M-6) and simply does not appear as a structure node — which is
the specified behavior. Extending the parser's legacy-syntax coverage is out of scope for this feature.

---

## Acceptance-criteria traceability

| Criterion (plan) | Tasks |
|------------------|-------|
| S1: model identifies object root, subroutines, data sections, maps, DDM refs | T2, T3, T4 (per-kind fixtures) |
| S1: every symbol carries accurate range | T1 (type), T2/T3 (assert ranges per kind) |
| S1: names captured as written, matched case-insensitively | T2 (name capture assertions) |
| S1: nested structure represented hierarchically | T2 (sections→fields, maps→fields, root→children) |
| S1: a fixture per symbol kind demonstrates extraction incl. ranges | T2, T3 |
| S2: partial object still yields structure for recognized parts | T4 |
| S2: unrecognized lines → diagnostics, not dropped, don't block extraction | T4 (verify existing behavior; channels stay separate) |
| FR-43: never panics over partial AST | T4, T7 (fuzz) |
| Persistence for downstream providers | T6 (cache round-trip, 0.6.0) |

---

## Ordered task list

### T1 — Add `model.Symbol` + `SymbolKind` (new shared contract)

- **Behavior:** Introduce `model.SymbolKind` constants (`SymbolObject`, `SymbolSubroutine`,
  `SymbolDataSection`, `SymbolDataField`, `SymbolMap`, `SymbolDDMReference`) and the recursive
  `model.Symbol{ Kind; Name; Range; SelectionRange; Children []Symbol }`, plus a new
  `FileAnalysis.Structure *Symbol` field (nil = no structure). Purely additive; changes no existing
  member or extractor.
- **Fixtures:** none (pure type + doc-comment addition).
- **Expected result:** compiles; `go vet`/`gofmt` clean; existing model tests still green; the existing
  `SymbolKind`/`SymbolEntry` stub is left untouched (documented as separate/legacy).
- **Reuses/migrates:** extends `internal/model/model.go`. No consumer migration needed yet — additive
  field. Guard doc comment: stable string values (cache keys), never mutate existing values.
- **DoD:** type + constants + field added with doc comments explaining LSP `DocumentSymbol` alignment;
  `go build ./...`, `go vet`, `gofmt` clean; model purity preserved (no backend imports).
- **TDD agents:** `tdd-red` (a test asserting the zero-value/shape and constant string values) →
  `tdd-green` → `tdd-refactor`.
- **Depends on:** none.

### T2 — `extractStructure`: object root + data sections + fields + subroutines + maps hierarchy

- **Behavior:** Add `extractStructure(path string, prog *Program, defs []model.DataDefinition) *model.Symbol`
  in a new `internal/analysis/natural/structure.go`. Build the root `SymbolObject` (name = filename base
  without extension via `filepath`, range = `prog` span). Children, source-ordered: one
  `SymbolDataSection` per `ast.DataSections` entry (kind as name, its fields as `SymbolDataField`
  children built by reusing the already-computed `model.DataDefinition` tree incl. REDEFINE nesting),
  one `SymbolSubroutine` per `ast.Subroutines` (name, range), one `SymbolMap` per `ast.Maps` (name,
  range, its fields as children). Deterministic ordering (stable sort on `Range.Start`, mirroring
  `extractEdges`/`extractDataAccess`).
- **Fixtures (new dir `internal/analysis/natural/testdata/structure/`):**
  - `01-program-full.NSP` (`.NSP` program): `DEFINE DATA LOCAL` with a group + REDEFINE, a
    `DEFINE SUBROUTINE`, and a `DEFINE MAP` (inline or via reference) — exercises root + section + fields
    + subroutine + map in one program.
  - `02-subprogram-params.NSN` (`.NSN` subprogram): `DEFINE DATA PARAMETER` section — exercises
    `SymbolDataSection` kind = PARAMETER and hierarchical fields.
  - `03-map.NSM` (`.NSM` map): a `DEFINE MAP` with fields — exercises the map-object root + `SymbolMap`
    (or fields directly under the object root, per parser output; assert what the AST yields).
- **Expected result (assert exactly):** root kind/name/range; ordered children with kinds; section kind
  names (`LOCAL`, `PARAMETER`); nested field names/levels/ranges including a REDEFINE child; subroutine
  and map names + ranges; `SelectionRange` = name-token span. Names captured as the AST produced them.
- **Reuses/migrates:** reuses `prog.Subroutines`, `prog.DataSections`, `prog.Maps`, and the
  `model.DataDefinition` tree from `extractDefinitions`; reuses `stmtRange`/`sourceStartLess` helpers.
  No consumer migration.
- **DoD:** table-driven + fixture unit test; deterministic sorted output; never panics on nil `prog`;
  `go test ./internal/analysis/natural -race` green; `go vet`/`gofmt` clean; seam preserved (only imports
  `internal/model`).
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T1.

### T3 — DDM references in the structure tree (`SymbolDDMReference`)

- **Behavior:** Extend `extractStructure` to append `SymbolDDMReference` children to the object root, one
  per **named** DDM/view/table reference, built from `FileAnalysis.DataAccess` (READ/FIND/GET/STORE +
  SQL FROM/INTO/table entries produced by `extractDataAccess`/`extractSQLAccess`). Use
  `DataAccessEntry.Name` (already upper-cased) as the name and `NameRange` as the `SelectionRange`,
  `Source` as `Range`. **Skip empty-Name entries** (record-form UPDATE/DELETE — the modeled gap from
  feature 08, OQ-4) so no false/empty DDM node appears. De-duplicate is **not** applied (each reference
  site is a distinct structural occurrence for navigation); keep all, source-ordered.
- **Fixtures (new dir):**
  - `04-ddm-access.NSP` (`.NSP` program): a `READ view` + a `FIND view` + a `STORE view` (Adabas) —
    exercises multiple `SymbolDDMReference` nodes with ranges, and confirms the empty-Name record-write
    is not emitted as a DDM ref.
- **Expected result:** ordered `SymbolDDMReference` children with correct names/ranges; a record-form
  write (empty `Name`) produces **no** DDM-reference node; DDM refs are ordered by source among the root
  children per T2's stable sort.
- **Reuses/migrates:** reuses `FileAnalysis.DataAccess` (so `extractStructure` takes the computed
  `[]model.DataAccessEntry` as an argument — wire the arg in T5). No consumer migration.
- **DoD:** fixture + table test; empty-Name skip asserted; deterministic order; `-race` green; vet/fmt
  clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T2.

### T4 — Story-2 robustness: partial/legacy object still yields structure

- **Behavior:** Verify (do not add diagnostic logic) that a file with a malformed/unrecognized statement
  still produces a structure tree for the recognized constructs, and that the malformed line remains a
  parser diagnostic (`FileAnalysis.Diagnostics`), never dropped and never leaking into the structure
  tree. Confirms FR-17/M-6 channel separation and FR-43 non-panic over the partial AST.
- **Fixtures (new dir):**
  - `05-partial.NSP` (`.NSP` program): a valid `DEFINE DATA LOCAL` + a valid `DEFINE SUBROUTINE`,
    interleaved with one garbled statement-like line the parser cannot parse.
- **Expected result:** `Structure` contains the valid section (+ fields) and the valid subroutine with
  correct ranges; the garbled line produces at least one entry in `Diagnostics` (severity error/warning
  with a real range) and **no** structure node; extraction does not panic and returns the partial tree.
- **Reuses/migrates:** none new — asserts existing parser error-recovery + `extractStructure`.
- **DoD:** fixture + test asserting both channels; `-race` green; vet/fmt clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T2, T3.

### T5 — Wire `extractStructure` into `Analyzer.Analyze`

- **Behavior:** In `analyzer.go`, after the existing extractors run (so `result.DataAccess`/`Definitions`
  are ready), set `result.Structure = extractStructure(path, ast, result.Definitions, result.DataAccess)`
  inside the `if ast != nil` block. `Structure` stays nil when `ast == nil`.
- **Fixtures:** reuses T2/T3 fixtures through the public `Analyze(path, content)` entry point.
- **Expected result:** an end-to-end `Analyze` test on `01-program-full.NSP` returns a populated
  `FileAnalysis.Structure` matching the T2/T3 assertions; unknown-extension and empty-content files yield
  `Structure == nil` (or a bare root — assert the chosen behavior; recommend a root for any parsed
  object, nil only when `ast == nil`).
- **Reuses/migrates:** integrates with the existing `Analyze` pipeline; confirms `seam_test.go` still
  passes (LSP-facing packages read `FileAnalysis.Structure`, never `natural.*`).
- **DoD:** end-to-end `Analyze` test; existing `analyzer_test.go` still green; seam test green; `-race`,
  vet, fmt clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T3.

### T6 — Persist `Structure` in the workspace cache (bump 0.5.0 → 0.6.0)

- **Behavior:** Add `Structure *model.Symbol` to `cacheEntry` (`internal/workspace/cache.go`), populate
  it in `Save`, restore it in `Load`, and bump `cacheFormatVersion` to `"0.6.0"`. The existing
  version-mismatch path (returns all entries stale, forces rebuild) already covers the upgrade — add a
  regression test that a `0.5.0` cache file is treated as fully stale under `0.6.0`.
- **Fixtures:** a small in-test `Index` with a populated `Structure`, saved and reloaded (existing cache
  tests use in-memory indices, not `.NSx` files).
- **Expected result:** save→load round-trips `Structure` exactly (deep-equal, incl. nested children and
  ranges); a cache written with `version: "0.5.0"` (or any non-`0.6.0`) yields all-stale on `Load` and a
  nil index; JSON tag `"structure"` present.
- **Reuses/migrates:** migrates the **only** consumer of the persisted contract (the cache). No other
  persisted-contract consumer exists. `lsp-graph` (external) reads the model, not the cache — no action.
- **DoD:** round-trip test + version-mismatch regression test; existing cache tests green; `-race`,
  vet, fmt clean.
- **TDD agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
- **Depends on:** T1 (needs the model type). Independent of T2–T5 but land after so real structures exist
  to round-trip.

### T7 — Fuzz target for the structure-extraction entry point (FR-43)

- **Behavior:** Add `FuzzExtractStructure` in `internal/analysis/natural/fuzz_test.go` (mirroring
  `FuzzParse`/`FuzzExtractSQL`): parse arbitrary input, run `extractStructure` over the resulting AST,
  assert it never panics and always returns either nil or a well-formed tree (no nil `*Symbol` children).
  Seed with the T2–T5 fixtures plus malformed inputs.
- **Fixtures:** seed corpus from the new `testdata/structure/` files.
- **Expected result:** fuzz target builds and runs; no panic on adversarial/partial input; graceful
  degradation held (FR-43).
- **Reuses/migrates:** mirrors the existing fuzz-target pattern.
- **DoD:** `go test -run=Fuzz -fuzz=FuzzExtractStructure -fuzztime=Xs` starts cleanly; short seeded run
  green in `just verify`; vet/fmt clean.
- **TDD agents:** `tdd-red` (failing/absent fuzz target) → `tdd-green` → `tdd-refactor`.
- **Depends on:** T5.

---

## Reviews required (`/review-feature`)

- **`review-seam`** — a shared contract changed (new `model.Symbol`/`SymbolKind`/`FileAnalysis.Structure`
  and the cache format). Confirm LSP-facing packages read only `internal/model`, and `model` stays free
  of backend internals.
- **`review-robustness`** — new AST-walking extractor + fuzz target; confirm no panics over partial
  ASTs and that Story-2 channel separation (structure vs. diagnostics) holds (FR-43/FR-17/M-6).
- **`review-docs`** — bumps cache-format version and adds a capability (structural model); the
  CLAUDE.md "Project state" note + README must be synced at `/finalize-feature`.
- **(Not concurrency)** — no indexer/watcher change. **(Not protocol)** — no new LSP method (rendering is
  feature 10/11).

---

## Open questions (for the plan-approval checkpoint)

Both plan open questions are **resolved above** (OQ-1 kind set + new `model.Symbol` type; OQ-2 no new
column/fixed-format handling). Remaining decisions to confirm with the user:

1. **New model type vs. reuse the `SymbolEntry` stub.** Recommendation: add the new recursive
   `model.Symbol` and leave the flat `SymbolEntry`/`SymbolKind` stub untouched. Confirm we are not
   expected to unify/retire the stub as part of this feature.
2. **Cache bump to `0.6.0`.** Recommendation: persist `Structure` and bump to `0.6.0` (T6) so
   outline/symbol providers serve from cache. Alternative: recompute `Structure` on load from cached
   `Definitions`/`DataAccess`/`AST` (as feature 07 chose for resolution) and add **no** cache field or
   version bump. Recommend persisting (structure is cheap, self-contained, and the downstream providers
   want it hot) — confirm the bump is acceptable.
3. **`.NSM` map object root shape.** The parser emits a `Map` node for `DEFINE MAP`; for a standalone
   `.NSM` file, confirm whether the object root's single child should be the `SymbolMap` (recommended,
   consistent) or whether the map's fields hang directly off the object root. Task 2's `03-map.NSM`
   assertion will lock this to whatever the AST actually produces.
4. **Object-root name source.** Recommendation: derive from `filepath.Base(path)` minus extension (the
   AST has no object name today). Confirm this is acceptable rather than adding a parser change to read a
   name from source.
