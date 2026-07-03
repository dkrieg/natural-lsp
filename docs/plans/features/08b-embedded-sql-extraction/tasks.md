# Tasks: Embedded-SQL Extraction (`08b-embedded-sql-extraction`)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-19 (read edges), FR-20 (write edges), FR-21 (data definitions / host-var
references), FR-10/FR-14 (`CALLDBPROC` call-like edge), FR-17/FR-18 (modeled gaps distinct from parse
errors), FR-43 (graceful degradation), M-6 (no silent gaps).
**Seam:** entirely on the **extraction-backend** side of the Analyzer seam
(`internal/analysis/natural`), plus additive `internal/model` members and a cache-format bump. No
LSP-facing (`internal/server`, `internal/document`) code changes. `internal/workspace` changes only for
the cache-version bump (mechanical) — SQL edge resolution is explicitly out of scope (belongs to the
resolution feature).

---

## Current-state findings & impact

Surveyed `internal/analysis/natural/{ast.go,parser.go,data.go,calls.go,analyzer.go}`,
`internal/model/model.go`, `internal/workspace/cache.go`, and `.claude/knowledge/natural/embedded-sql.md`.

### What already exists and is reused verbatim

- **The SQL AST is in place (feature `00-parser-embedded-sql`).** `ast.go` defines
  `SelectStatement`, `SelectSingleStatement`, `InsertStatement`, `SQLUpdateStatement`,
  `SQLDeleteStatement`, `ReadResultSetStatement`, `ProcessSQLStatement`, plus stubs `MergeStatement`,
  `CallDBProcStatement`, `CommitStatement`, `RollbackStatement`. Each is wired into `Program`'s slices
  (`Selects`, `SelectSingles`, `Inserts`, `SQLUpdates`, `SQLDeletes`, `Merges`, `CallDBProcs`,
  `ProcessSQLs`, `ReadResultSets`, ...). `OperandRef{Name, Range}` is the unbound operand type.
- **The read/write edge model (feature 08) is reusable verbatim.** `model.DataAccessEntry{Name, Kind,
  Source, NameRange}` with `model.EdgeReads` / `model.EdgeWrites`, produced by `extractDataAccess` in
  `data.go`, is exactly the representation SQL DDM reads/writes reuse (the KB confirms a native-SQL
  `FROM`/`INTO`/`INSERT INTO`/SQL-`UPDATE`/`DELETE` table operand **is a `.NSD` DDM name**, same
  namespace as Adabas). SQL table operands are already upper-cased by the lexer (case normalization),
  matching feature 08's `Name` contract.
- **The call-edge model (feature 06) is reusable for `CALLDBPROC`.** `model.EdgeEntry{Source, Target,
  Kind, TargetName, Library}` with `model.EdgeCalls` is the same shape; `TargetName` carries the
  proc/target name. `isStaticLiteral`/`edgeKind` helpers in `calls.go` are reusable if the proc name
  can be a variable.
- **Wiring pattern is fixed.** `Analyzer.Analyze` (`analyzer.go:75-80`) calls `extractEdges`,
  `extractDataAccess`, `extractDefinitions`, `extractWorkFiles` over the AST guarded by `if ast != nil`.
  New SQL extraction wires in identically.
- **Shared helpers** in `calls.go`: `stmtRange(start,end)`, `sourceStartLess(...)`, and the
  stable-sort-by-`Source.Start` pattern used by both `extractEdges` and `extractDataAccess`.
- **`FuzzParse`** exists in `fuzz_test.go`; the new fuzz target for SQL extraction follows its shape.
- **Fixture convention:** `internal/analysis/natural/testdata/{calls,dataaccess,parser}/`. This feature
  adds `internal/analysis/natural/testdata/sqlaccess/`.

### Divergences from the spec / doc comments (code is ground truth — FLAGGED)

1. **`CallDBProcStatement` does NOT capture its proc-name operand.** Despite the doc comment ("Consumes
   proc-name operand ... via skipToNextStatement", `parser.go:1278-1296`) and the AST comment
   ("Procedure-name and remaining operand fields are added by the parser task", `ast.go:402-408`), the
   struct has **only** `StartPos`/`EndPos`. The parser `p.skipToNextStatement()`s past the proc name
   without storing it. **Story 5's `CALLDBPROC` call edge therefore requires a parser change** to
   capture the proc-name operand (and its range) — this is *not* a pure extraction task. Sequenced
   first (Task 6a) before the extraction that consumes it (Task 6b).
2. **`MergeStatement` does NOT capture its table operand.** Same divergence: the doc comment
   (`ast.go:368-370`, `parser.go:2080-2119`) claims a table operand is captured, but the struct has
   only `StartPos`/`EndPos` and the parser advances past the `INTO <table>` identifier without storing
   it. **Story 2's `MERGE` write edge therefore requires a parser change** (Task 5a) before extraction
   (Task 5b).
3. **`ReadResultSetStatement` carries a `ResultSetOperand` but no link to its `CALLDBPROC`.** There is
   no positional/pairing association in the AST between a `READ RESULT SET` and the preceding
   `CALLDBPROC`. Story 5's "READ RESULT SET associated with its preceding CALLDBPROC" is satisfiable
   only positionally (source order); see Task 6c and OQ-5.
4. **There is no host-variable / field-reference member in `internal/model`.** `DataDefinition` models
   *declarations*; there is no type for a *use/reference* of a field. Stories 3 and 4 (bind host vars
   back to `DEFINE DATA`) therefore require a **new additive `internal/model` member** and a
   **cache-format bump** (see OQ-2). Binding-to-declaration across files is resolution's job and out of
   scope; this feature emits the *reference site + name* only.
5. **The opaque `PROCESS SQL` body is a single `Body string` + `BodyRange`** (`ast.go:419-426`), never
   tokenized. Scanning it for `:host-var` refs is this feature's responsibility (Story 4) and is a
   **string scan over `Body`**, not an AST walk.

### Open-question resolutions (answered against as-built code — confirm at plan approval)

- **OQ-1 (reuse feature-08 read/write edge? `CALLDBPROC` new or reuse?)** — **RESOLVED.** SQL DDM
  reads/writes **reuse `model.DataAccessEntry` + `EdgeReads`/`EdgeWrites` verbatim** (no new kind).
  `CALLDBPROC` **reuses `model.EdgeEntry` + `model.EdgeCalls`** (no new kind) — a stored-proc call is a
  call-like edge, indistinguishable in kind from `CALLNAT` for downstream consumers, matching the KB
  ("call-like edge to a DB procedure"). No new `EdgeKind` constant is introduced.
- **OQ-2 (new model member? cache bump?)** — **RESOLVED: YES, both.** Host-var references (Stories 3/4)
  have no existing representation. Add **one new additive member** to `internal/model`
  (`FileAnalysis.HostVarRefs []HostVarRef`, where `HostVarRef{Name, Range}`; `Name` upper-cased,
  colon/sigil-normalized). This is persisted, so the cache-format version **bumps `0.4.0` → `0.5.0`**
  (Task 1 + Task 8). The SQL DDM reads/writes and `CALLDBPROC` edges reuse existing persisted members
  (`DataAccess`, `Edges`) and add no field of their own.
- **OQ-3 (`PROCESS SQL` intent — read vs write vs neutral?)** — **RESOLVED: neutral read-style access.**
  The opaque body is not parsed, so verb-sniffing to infer read-vs-write intent would be unreliable and
  is out of scope. The `ddm-name` operand is emitted as a **`model.EdgeReads` `DataAccessEntry`** (the
  most conservative "this object touches this DDM" edge; a write cannot be asserted without parsing the
  body). Recorded as a decision, revisitable if verb-sniffing is later added. (No new "neutral" edge
  kind is introduced — adding one would ripple through every `DataAccessEntry` consumer for marginal
  value; `EdgeReads` is the safe under-approximation.)
- **OQ-4 (host-var direction `:U:`/`:G:`/`:T:`)** — **RESOLVED: capture qualifier only, plain reference
  sufficient.** Per the plan's "out of scope" (USING/GIVING data-flow direction), first release records
  the host-var **name + range** as a plain reference and *recognizes/strips* the `:U:`/`:G:`/`:T:`
  qualifier and `INDICATOR`/`LINDICATOR` prefixes during scanning so the bound name is clean; it does
  **not** classify read-vs-write from the qualifier. `:T:` (text-splice) is treated as a plain field
  reference (OQ-5 in plan). No qualifier field is persisted in this release.
- **OQ-5 (`READ RESULT SET` ↔ `CALLDBPROC` association)** — **PARTIALLY OPEN.** The AST has no explicit
  link; association can only be positional (nearest preceding `CALLDBPROC` in source order). Task 6c
  extracts the `READ RESULT SET` as a read-style DDM/result-set access with its site; the *explicit
  pairing* is recorded as a modeled note, not a bound edge. Flag for user decision (see Open questions).

---

## Ordered task list

Dependency order: model/types → parser widenings (for the two divergences) → extraction slices →
wiring + cache bump → robustness/fuzz. Each task is one red → green → refactor loop
(`tdd-red` → `tdd-green` → `tdd-refactor`).

### Task 1 — Add `HostVarRef` model member + cache-format bump (shared-contract change)

- **Behavior:** Add `model.HostVarRef{Name string; Range model.Range}` and
  `FileAnalysis.HostVarRefs []HostVarRef` to `internal/model/model.go` (additive, purity preserved — no
  backend internals). Bump `cacheFormatVersion` `"0.4.0"` → `"0.5.0"` in
  `internal/workspace/cache.go`.
- **Fixtures:** none (type-level change).
- **Expected result:** package compiles; `model` doc comment describes `HostVarRef` as a host-var *use*
  (contrast with `DataDefinition` declarations); cache round-trips a `FileAnalysis` carrying
  `HostVarRefs` and rejects a `0.4.0` cache (forces rebuild).
- **Reuses/migrates:** consumers of `FileAnalysis` (`internal/workspace` index/cache) recompile against
  the additive field; existing cache tests updated for the new version string. No `internal/server`
  change. This is the shared-contract change — `review-seam` required.
- **DoD:** unit test asserting cache version mismatch invalidates; `go vet`/`gofmt` clean; existing
  workspace/cache tests green against `0.5.0`; no consumer reads `HostVarRefs` yet (populated in later
  tasks).
- **Depends on:** none.

### Task 2 — Native SQL read edges: SELECT / SELECT SINGLE / (READ RESULT SET table) (Story 1, FR-19)

- **Behavior:** New `extractSQLAccess(*Program) []model.DataAccessEntry` (in a new
  `internal/analysis/natural/sql.go`). For `SelectStatement` and `SelectSingleStatement`, emit one
  `EdgeReads` `DataAccessEntry` per `FromTables` operand: `Name` = operand name (already upper-cased),
  `NameRange` = operand `Range`, `Source` = statement `stmtRange(StartPos, EndPos)`.
- **Fixtures:** `testdata/sqlaccess/select_loop.NSP` (a `SELECT … INTO … FROM SQL-PERSONNEL … WHERE …
  END-SELECT`), `testdata/sqlaccess/select_single.NSP` (a `SELECT SINGLE … FROM <DDM>`).
- **Expected result:** each fixture yields exactly one `EdgeReads` entry with `Name` = the DDM name,
  correct `NameRange` on the `FROM` table token, zero false edges (columns / INTO / WHERE operands do
  **not** become DDM edges here — they are host vars, handled in Task 4).
- **Reuses/migrates:** reuses `model.DataAccessEntry`, `model.EdgeReads`, `stmtRange`,
  `sourceStartLess`, the stable-sort pattern (source-order output). Not yet wired into `Analyze`
  (Task 8).
- **Modeled gaps:** a `FROM` table that is empty/absent (malformed) → no edge (parser already
  diagnosed); never a false edge.
- **DoD:** table-driven fixture tests; deterministic source-ordered output; `go vet`/`gofmt` clean.
- **Depends on:** none (uses existing `DataAccessEntry`).

### Task 3 — Native SQL write edges: INSERT / SQL-UPDATE / SQL-DELETE (Story 2, FR-20)

- **Behavior:** Extend `extractSQLAccess` to emit `EdgeWrites` `DataAccessEntry` for
  `InsertStatement.IntoTable`, `SQLUpdateStatement.Table`, and `SQLDeleteStatement.FromTable` (one per
  table operand), distinguishable from reads to the same DDM by `Kind`.
- **Fixtures:** `testdata/sqlaccess/insert.NSP`, `testdata/sqlaccess/sql_update.NSP` (SET/WHERE form —
  confirm parser classified it as `SQLUpdates`, not the Adabas `RecordUpdates`),
  `testdata/sqlaccess/sql_delete.NSP` (WHERE/FROM-table form → `SQLDeletes`).
- **Expected result:** one `EdgeWrites` per fixture, correct `Name`/`NameRange`/`Source`; a fixture
  that reads and writes the *same* DDM produces one `EdgeReads` + one `EdgeWrites` (distinct by kind).
- **Reuses/migrates:** reuses `model.EdgeWrites`. Confirms (does not re-implement) the parser's
  SQL-vs-Adabas `UPDATE`/`DELETE` disambiguation (already shipped in feature 08 / parser).
- **Modeled gaps:** empty table operand → no edge.
- **DoD:** fixture tests; kinds distinct; `go vet`/`gofmt` clean.
- **Depends on:** Task 2 (shares `extractSQLAccess`).

### Task 4 — Host-variable references in native SQL clauses (Story 3, FR-21)

- **Behavior:** New `extractHostVarRefs(*Program) []model.HostVarRef`. For each native SQL statement
  (`SelectStatement`, `SelectSingleStatement`, `InsertStatement`, `SQLUpdateStatement`,
  `SQLDeleteStatement`), collect the host-var operands from the clauses that carry them
  (`IntoTargets`, `WhereOperands`, `Values`, `SetTargets`) and emit one `HostVarRef` per operand that
  is a host var. Normalize: strip a leading colon if present (bare-vs-colon both bind); keep the
  Natural sigil (`#`/`&`/`+`/`@`) as feature-08 `DataDefinition.Name` does; upper-case. Reserved-word
  case (`:DATE`) is handled by colon-stripping.
- **Fixtures:** `testdata/sqlaccess/hostvars_native.NSP` — a SELECT with **bare** `#`-prefixed INTO
  targets and WHERE operands (the idiomatic native form from the KB) plus at least one colon-prefixed
  reserved-word host var (e.g. `:DATE`) proving both forms bind.
- **Expected result:** every host var appears as a `HostVarRef` with the colon stripped and range on
  the operand token; `#NAME`, `#SALARY`, `#PERS-ID`, and `DATE` (from `:DATE`) all present; no SQL
  column names, no DDM table names, and no SQL keywords leak in as host-var refs.
- **Reuses/migrates:** reuses `model.HostVarRef` (Task 1). Distinguishing "column" vs "host var" in a
  clause is by operand list (the parser already separated `Columns` from `IntoTargets`/`WhereOperands`)
  — a column operand (`SelectStatement.Columns`) is **not** a host-var ref.
- **Modeled gaps:** a literal in a `Values`/`WHERE` operand (not a field) is not a host-var ref; only
  identifier-shaped operands bind. Bare-vs-colon equivalence explicitly asserted (FR-17: the colon is a
  syntactic form, not a distinct reference).
- **DoD:** fixture test asserting the exact set of refs and their normalized names; deterministic
  source order; `go vet`/`gofmt` clean.
- **Depends on:** Task 1.

### Task 5a — Parser: capture the `MERGE` target-table operand (divergence fix, precedes Story 2 MERGE)

- **Behavior:** Add `Table []OperandRef` (or a single `Table OperandRef`) to `MergeStatement`
  (`ast.go`) and populate it in `parseMergeStatement` (`parser.go:2090-2119`) — capture the identifier
  after `INTO` as an `OperandRef{Name, Range}` instead of advancing past it. Keep MERGE internals
  skipped/unmodeled.
- **Fixtures:** parser-level fixture under `testdata/parser/` following the existing SQL numbering
  (next free `NN`) — a minimal `MERGE INTO <DDM> …`.
- **Expected result:** parser test asserts `Merges[0].Table` carries the DDM name + range; malformed
  `MERGE` without `INTO` still produces a diagnostic (unchanged) and an empty table operand.
- **Reuses/migrates:** widens the parser → **fuzz coverage already exists via `FuzzParse`** (verify it
  still never panics on MERGE inputs). No `model` change.
- **Modeled gaps:** MERGE with a variable/malformed table → operand recorded verbatim or empty; parser
  diagnostic unchanged; extraction (Task 5b) handles empty → no edge.
- **DoD:** parser fixture test; `FuzzParse` re-run seed added; `go vet`/`gofmt` clean; feature-00 SQL
  parser tests still green.
- **Depends on:** none (parser layer).

### Task 5b — MERGE write edge (Story 2, FR-20)

- **Behavior:** Extend `extractSQLAccess` to emit an `EdgeWrites` `DataAccessEntry` for
  `MergeStatement.Table`.
- **Fixtures:** `testdata/sqlaccess/merge.NSP`.
- **Expected result:** one `EdgeWrites` entry with the MERGE target DDM name/range/source.
- **Reuses/migrates:** reuses `model.EdgeWrites`; consumes the parser field added in Task 5a.
- **Modeled gaps:** empty table operand → no edge.
- **DoD:** fixture test; `go vet`/`gofmt` clean.
- **Depends on:** Task 3, Task 5a.

### Task 6a — Parser: capture the `CALLDBPROC` proc-name operand (divergence fix, precedes Story 5)

- **Behavior:** Add `ProcName string` + `ProcNameRange model.Range` (and a `ProcNameIsLiteral bool` if
  a quoted literal vs identifier distinction is needed) to `CallDBProcStatement` (`ast.go`); populate
  in `parseCallDBProcStatement` (`parser.go:1278-1296`) — capture the first operand after `CALLDBPROC`
  before `skipToNextStatement`.
- **Fixtures:** parser fixture under `testdata/parser/` (next free `NN`) — a minimal
  `CALLDBPROC 'MYPROC' …` and a variant with an unquoted proc identifier.
- **Expected result:** parser test asserts `CallDBProcs[0].ProcName`/`ProcNameRange`; malformed
  `CALLDBPROC` with no operand → empty name + existing behavior.
- **Reuses/migrates:** widens the parser → covered by `FuzzParse` (verify no panic).
- **Modeled gaps:** variable proc name recorded verbatim (handled at extraction as a dynamic edge if we
  adopt the `edgeKind` helper).
- **DoD:** parser fixture test; `FuzzParse` seed; `go vet`/`gofmt` clean; feature-00 SQL tests green.
- **Depends on:** none (parser layer).

### Task 6b — `CALLDBPROC` call-like edge (Story 5, FR-10/FR-14)

- **Behavior:** New `extractSQLCalls(*Program) []model.EdgeEntry` (in `sql.go`). For each
  `CallDBProcStatement`, emit a `model.EdgeEntry{Kind: EdgeCalls, TargetName: ProcName,
  Source: stmtRange(...), Target: zero range}` — the call site preserved, target unbound (resolution
  deferred). If the proc name is a variable/placeholder, reuse `edgeKind`/`isStaticLiteral` to downgrade
  to `EdgeCallsDynamic` (consistent with feature-06 `CALLNAT`).
- **Fixtures:** `testdata/sqlaccess/calldbproc.NSP` (literal proc name) — reuse the KB's `NDBERR`
  pattern as a companion `CALLNAT` if useful, but the primary assertion is the `CALLDBPROC` edge.
- **Expected result:** one `EdgeCalls` edge with `TargetName` = proc name and the call-site `Source`;
  variable proc name → `EdgeCallsDynamic` (modeled gap, not a diagnostic).
- **Reuses/migrates:** reuses `model.EdgeEntry`, `model.EdgeCalls`/`EdgeCallsDynamic`,
  `isStaticLiteral`, `edgeKind`, `stmtRange`, `sourceStartLess`. Consumes Task 6a's parser fields.
- **Modeled gaps:** dynamic proc name → dynamic edge (FR-17/FR-18); no diagnostic.
- **DoD:** fixture test; deterministic source order; `go vet`/`gofmt` clean.
- **Depends on:** Task 6a.

### Task 6c — `READ RESULT SET` read access + `CALLDBPROC` association (Story 5, Story 1)

- **Behavior:** In `extractSQLAccess`, treat `ReadResultSetStatement` as a read-style access recording
  its site: emit an `EdgeReads` `DataAccessEntry` whose `Name` is the `ResultSetOperand.Name`
  (normalized) if it is a resolvable name, else record the site with empty `Name` (a result-set handle,
  not a DDM — modeled gap). Record the positional association to the nearest preceding `CALLDBPROC` per
  OQ-5 (as source-order proximity only; no explicit bound link in the model this release).
- **Fixtures:** `testdata/sqlaccess/read_result_set.NSP` — a `CALLDBPROC` immediately followed by
  `READ RESULT SET … END-RESULT`.
- **Expected result:** the `CALLDBPROC` edge (from Task 6b) and a `READ RESULT SET` read-access entry
  both present; test documents the positional pairing. No false DDM edge from a result-set handle that
  is not a DDM name.
- **Reuses/migrates:** reuses `model.DataAccessEntry`/`EdgeReads`. Depends on Task 6b's fixture pattern.
- **Modeled gaps:** result-set operand is a handle, not a DDM → recorded as a site (empty/verbatim
  name), not a bound DDM edge; the CALLDBPROC pairing is positional (OQ-5 open).
- **DoD:** fixture test asserting both entries and documenting the association; `go vet`/`gofmt` clean.
- **Depends on:** Task 2, Task 6b.

### Task 7 — Flexible-SQL (`PROCESS SQL`) DDM edge + opaque-body host-var scan (Story 4, FR-19/21, M-6)

- **Behavior:** In `extractSQLAccess`, emit one **`EdgeReads`** `DataAccessEntry` for
  `ProcessSQLStatement.DDMName` (`Name`=DDMName normalized, `NameRange`=`DDMNameRange`,
  `Source`=statement range) — the neutral read-style access per OQ-3. Additionally, a new
  `scanOpaqueHostVars(body string, bodyStart model.Position) []model.HostVarRef` scans the opaque
  `Body` string for **colon-mandatory** `:host-var` references and feeds them into
  `extractHostVarRefs`' output. The scanner recognizes and strips `:U:`/`:G:`/`:T:` qualifiers and
  `INDICATOR`/`LINDICATOR` indicator prefixes, and recognizes array notation (`:NAME(*)`,
  `:SALARY(01:10)`) capturing the base name. It computes each ref's `Range` from `BodyRange`/scan
  offset.
- **Fixtures:** `testdata/sqlaccess/process_sql.NSP` — a `PROCESS SQL <DDM> << … >>` whose opaque body
  contains: a bare table name (must NOT be bound), a plain `:#PERS-ID`, a qualifier form (`:G:#NAME` or
  `:U:#X`), and an in-body SQL keyword — proving only host vars + the `ddm-name` operand are extracted.
- **Expected result:** exactly one `EdgeReads` for the `ddm-name` operand; host-var refs for each
  `:name` in the body (qualifier-stripped); the in-body **table name is NOT emitted as a DDM edge**
  (the load-bearing modeled gap — KB: opaque-body table names are pass-through text); in-body SQL
  keywords are not host-var refs.
- **Reuses/migrates:** reuses `model.DataAccessEntry`/`EdgeReads`, `model.HostVarRef`. The opaque-body
  scan is a **string scan over `Body`**, not a re-tokenization (the body must never be parsed as
  Natural — KB §"Lexer/parser implications").
- **Modeled gaps (explicit):** (a) in-body table names never bound; (b) colon **mandatory** inside
  `<<…>>` (bare names in the body are pass-through text, not host-var refs) — contrast Task 4's native
  bare-or-colon rule; (c) a `PROCESS SQL` with no `ddm-name` → no DDM edge, host-var scan still runs.
- **DoD:** fixture test asserting the exact edge set and the *absence* of an in-body table edge;
  qualifier/array/indicator forms covered; `go vet`/`gofmt` clean.
- **Depends on:** Task 1, Task 4.

### Task 8 — Wire SQL extraction into `Analyze` + merge into `FileAnalysis` (all stories)

- **Behavior:** In `analyzer.go` `Analyze`, after the existing extractors, call `extractSQLAccess`,
  `extractSQLCalls`, and the host-var collectors; **append** SQL `DataAccessEntry`s to
  `result.DataAccess`, SQL `EdgeEntry`s to `result.Edges`, and host-var refs to `result.HostVarRefs`;
  re-establish global source order on the merged `DataAccess` and `Edges` slices (stable sort on
  `Source.Start`, matching the existing contract) so SQL and Adabas/call edges interleave correctly.
- **Fixtures:** the KB's combined minimal fixture `testdata/sqlaccess/kb_minimal.NSP` (SELECT loop +
  bare host vars + `PROCESS SQL` flexible block + `CALLNAT 'NDBERR'`) as the end-to-end assertion.
- **Expected result:** `Analyze` over the KB fixture yields: read edge on DDM `SQL-PERSONNEL`; host-var
  refs `#NAME`/`#SALARY`/`#PERS-ID` from the native SELECT; a read edge for the `PROCESS SQL` DDM;
  `:#PERS-ID` from the opaque body; the `CALLNAT 'NDBERR'` edge (from feature 06, unchanged) — all in
  one source-ordered result. Existing feature-06/08 extraction over non-SQL fixtures unchanged.
- **Reuses/migrates:** mirrors the existing `Analyze` wiring; **migration:** confirm merged
  `DataAccess`/`Edges` remain source-ordered so feature-06/07/08 consumers and tests are unaffected.
- **DoD:** end-to-end analyzer test; all feature-06/07/08 tests still green; deterministic ordering;
  `go vet`/`gofmt` clean.
- **Depends on:** Tasks 2, 3, 4, 5b, 6b, 6c, 7.

### Task 9 — Fuzz target for SQL extraction entry point (Story 6, FR-43)

- **Behavior:** Add `FuzzExtractSQL` (in `fuzz_test.go` or a sibling) that parses arbitrary input and
  runs `extractSQLAccess`/`extractSQLCalls`/host-var scanning over the resulting (possibly partial)
  AST, asserting it never panics and always returns non-nil (nil-safe) slices — including over
  truncated `PROCESS SQL` bodies, unterminated `<<`, and malformed SQL. Seed with the `sqlaccess/`
  fixtures.
- **Fixtures:** reuse the `sqlaccess/` corpus as fuzz seeds.
- **Expected result:** no panic on any input; partial ASTs yield the edges that could be extracted
  (FR-43); parse-error diagnostics stay on the diagnostics channel and never appear as SQL edges
  (FR-17 channel separation asserted).
- **Reuses/migrates:** mirrors `FuzzParse` shape (`fuzz_test.go`).
- **DoD:** fuzz target compiles and runs a short corpus clean; nil-guards on every extractor confirmed;
  `go vet`/`gofmt` clean.
- **Depends on:** Task 8.

---

## Reviews required (for `/review-feature`)

- **`review-seam`** — Task 1 changes a shared contract (`internal/model` new member) and bumps the
  cache-format version; confirm `internal/model` purity (no backend internals) and that LSP-facing code
  still depends only on the interface + `model`.
- **`review-robustness`** — the feature widens the parser (Tasks 5a, 6a) and adds an opaque-body string
  scanner (Task 7); FR-43 graceful degradation + the new fuzz target (Task 9).
- **`review-docs`** — CLAUDE.md "Project state" and README must gain the embedded-SQL *extraction*
  paragraph, the new `model.HostVarRef` member, the `0.5.0` cache bump, and the `sqlaccess/` fixture
  dir; anticipate the `/finalize-feature` sync.
- **(Not concurrency/performance/protocol)** — no indexer/watcher change, no LSP method added, not in a
  new hot path beyond the existing per-file extraction pipeline.

---

## Open questions (for the plan-approval checkpoint)

1. **`READ RESULT SET` ↔ `CALLDBPROC` explicit pairing (OQ-5).** The AST has no link between them, and
   this release records only a positional/source-order association (Task 6c). Is positional association
   acceptable for the first release, or should a bound association be modeled now (would require an AST
   and/or `model` addition)? **Recommendation:** positional-only this release; defer explicit pairing.
2. **`PROCESS SQL` intent as `EdgeReads` (OQ-3).** We emit the `ddm-name` operand as a conservative
   *read* edge because the opaque body isn't parsed. Confirm this under-approximation is acceptable
   (vs. attempting leading-verb sniffing inside `<<…>>`, which the KB warns against). **Recommendation:**
   `EdgeReads`, revisit later.
3. **New `model.HostVarRef` + cache bump to `0.5.0` (OQ-2).** This is the only shared-contract change;
   it is required for Stories 3/4 (no existing member models a field *use*). Confirm the additive member
   name/shape (`HostVarRef{Name, Range}`; qualifier NOT persisted per OQ-4) and the version bump.
4. **Divergence: parser must be widened (Tasks 5a, 6a).** `MergeStatement` and `CallDBProcStatement` do
   NOT currently capture their operands despite doc comments to the contrary — Stories 2 (MERGE) and 5
   (CALLDBPROC) each require a small parser change first. Confirm this parser work belongs in *this*
   feature (it is prerequisite to the two edges) rather than being deferred.
5. **Host-var sigil normalization.** Task 4/7 keep the Natural sigil (`#`/`&`/`+`/`@`) on
   `HostVarRef.Name` and strip only the SQL colon, matching feature-08 `DataDefinition.Name`. Confirm
   this is the desired normalization so resolution can match refs to declarations later.
