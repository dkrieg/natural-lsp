# Tasks: Data-access extraction (feature 08)

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-19 (P0), FR-20 (P1), FR-21 (P1), FR-22 (P2)
**Depends on:** 00-parser-foundation (lexer/parser/AST), 06-call-extraction (edge model + `extractEdges` pattern)
**Branch:** `feat/08-data-access-extraction`

This plan mirrors feature 06's extraction pattern: a syntactic walk of the parsed `*Program`
producing `model` output that `Analyze` wires into `FileAnalysis`, with binding to definitions
deferred (there is no resolution work in this feature — DDM name→file resolution is a later
feature). Where the parser does not yet recognize an Adabas data-access statement, a parser
task lands first so downstream extraction has an AST node to walk.

---

## Current-state findings & impact

### Model contract is partly pre-provisioned (reconciliation)

- `internal/model/model.go` **already declares** `EdgeReads` (`"READS"` — READ/FIND/GET) and
  `EdgeWrites` (`"WRITES"` — STORE/UPDATE/DELETE), and a `DataAccessEntry{File string, Kind
  EdgeKind, Source Range}` struct, plus `FileAnalysis.DataAccess []DataAccessEntry` and
  `FileAnalysis.Symbols []SymbolEntry`. These are **defined but never populated** today.
- `internal/workspace/cache.go` **already persists** `DataAccess` and `Symbols` on `cacheEntry`
  (JSON `dataAccess`/`symbols`), plumbed through `Save`/`Load`. So persisting *new values* in the
  existing `DataAccessEntry` shape needs **no cache-format bump** — the field is already in the
  format at `cacheFormatVersion = "0.3.0"`.
- **Implication for cache version (important):** a bump is required only if we **add or change a
  persisted field**. `DataAccessEntry` as it stands has no field-operand `Range`, no case-normalized
  name field, and no way to express work-file definitions or data definitions. This plan
  **extends `DataAccessEntry`** (adds a `Name` normalized field + a `NameRange`) and adds a new
  persisted `Definitions`/parameter surface — both are persisted, so the cache-format version
  **bumps `0.3.0` → `0.4.0`** (Task 2 / Task 9). Migration is trivial (additive JSON fields; the
  version bump forces a clean rebuild), but it must be an explicit task.
- **No existing consumer reads `DataAccess`, `EdgeReads`, or `EdgeWrites`** outside the cache
  round-trip (grep confirms: only `model.go` and `cache.go` reference them, no server/index/
  resolution logic). So this is additive: nothing downstream must be *migrated in behavior*, only
  the cache-format version and its round-trip test.

### Parser gap (the load-bearing finding)

The parser recognizes only a subset of the Adabas data-access statements this feature needs.
Inventory of `dispatchStatement` (parser.go:47) and the AST (ast.go):

| Statement | Lexed as keyword? | Parser dispatch? | AST node? | Status for FR-19/20 |
|---|---|---|---|---|
| `READ view` | yes (`READ`) | yes (`parseReadStatement`) | `ReadStatement{Target}` | **parses** (read) |
| `STORE view` | yes (`STORE`) | yes (`parseStoreStatement`) | `StoreStatement{Target}` | **parses** (write) |
| `FIND view` | yes (`FIND`, `FINDNAT`) | **no** — falls to `default:` skip | **none** | **parser gap (read)** |
| `GET view` / `GET SAME` | yes (`GET`) | **no** — falls to `default:` skip | **none** | **parser gap (read)** |
| record-form `UPDATE` | yes (`UPDATE`) | dispatched, but `parseUpdateStatement` **actively skips** non-SQL shapes (no node) | none for Adabas form | **parser gap (write)** |
| record-form `DELETE` | yes (`DELETE`) | dispatched, but `parseDeleteStatement` **actively skips** non-`FROM` shapes (no node) | none for Adabas form | **parser gap (write)** |
| `DEFINE DATA` sections | yes | yes (`parseDataSection`) | `DataSection{Fields}` | **parses fields**, but **discards the section kind** (LOCAL/PARAMETER/GLOBAL/LINKAGE are skipped at parser.go:123) |
| `DEFINE WORK FILE` | `WORK`/`FILE` not keywords | **no** | **none** | **parser gap (FR-22)** |

Consequences that shape the task order:

1. **FIND and GET need new parser dispatch + AST nodes** before their read edges can be extracted.
   They are lexed as keywords already, so this is dispatch + a parse method, mirroring
   `parseReadStatement`.
2. **Record-form UPDATE/DELETE are silently dropped.** `parseUpdateStatement`/`parseDeleteStatement`
   deliberately produce *no node* for the Adabas form (correct for the SQL feature's needs, but it
   means the write relationship is invisible). We must extend these two methods to emit an Adabas
   record-form node (or a shared write node) for the non-SQL shape, **without regressing the SQL
   disambiguation** (this is a shared-contract change to two existing functions — regression tests
   for the SQL path are part of DoD). Note the Adabas UPDATE/DELETE operate on the *record read by a
   preceding READ/FIND/GET loop*, not on a named file operand at the statement — see OQ resolution
   below for how we represent the target.
3. **`DataSection` carries no section kind.** Story 3's "parameter interfaces" acceptance criterion
   requires distinguishing PARAMETER from LOCAL/GLOBAL. The parser currently throws that away at
   parser.go:123. We add a `Kind` to `DataSection` and stop discarding it — a parser change that
   precedes definition extraction.
4. **`Subroutine.DataSection` field exists but is never populated** (`parseSubroutine` at
   parser.go:310 skips the whole body to `END-SUBROUTINE`). Parameter interfaces for external
   subroutines are file-level (a `.NSS` with a top-level `DEFINE DATA PARAMETER`), so first release
   reads the *file-level* parameter section, not inline-subroutine data — no `parseSubroutine`
   change needed. Flagged so a future feature knows.

### Statement forms reference (for fixture authors)

- **READ**: `READ view-name` / `READ (n) view-name` / `READ view BY key` / `READ view WITH descriptor`.
  Already parsed; only the view name is captured (`read.Target`).
- **FIND**: `FIND view-name WITH descriptor = value` / `FIND NUMBER view` / `FIND (n) view`.
  New parse method captures the view name.
- **GET**: `GET view-name` / `GET SAME` / `GET view-name (ISN)`. `GET SAME` has no view operand
  (re-reads the current record) — a modeled gap (no read target, no edge; must not crash).
- **STORE**: `STORE view` / `STORE RECORD IN FILE view`. Already parsed.
- **record UPDATE**: `UPDATE` (updates current record) / `UPDATE (label)` — no file operand; the
  written file is the view of the referenced READ/FIND loop.
- **record DELETE**: `DELETE` / `DELETE (label)` — same, no file operand.
- **DEFINE WORK FILE**: `DEFINE WORK FILE n 'name' [attributes]` — n is the work-file number,
  the string is the physical file name/path.

### Divergence flagged

- `data.go` and `symbols.go` are package-doc + `TODO` stubs — this feature fills `data.go` (the
  extractor). `symbols.go` (FileAnalysis→LSP symbol tree) is a **later feature (outline/FR-27)**;
  this feature only populates `FileAnalysis.Symbols`/`DataAccess`/definitions, not the LSP mapping.
- Analyzer seam: all work is behind the seam (`internal/analysis/natural`); LSP-facing code is not
  touched. Only `internal/model` (contract) and `internal/workspace/cache.go` (persistence) change,
  both already consume the model — the seam is preserved.

---

## Open-question resolutions (recommended first-release scope — for approval)

- **OQ-1 — array-bound & redefinition grammar depth.** The parser **already** models array bounds
  (`ArrayBound{Lower, Upper, UpperUnbounded}`, incl. `1:*`) and `REDEFINE` with nested `Children`
  (parser.go:148-223, fixtures `07-data-arrays`, `08-data-redefine`). **Recommendation: reuse the
  existing depth as-is for first release** — extract fields with their level, verbatim type/format,
  dimensions, and redefine grouping exactly as the AST already provides. Do **not** deepen the
  grammar (no dynamic bounds, no `EM=`/`HD=` edit-mask parsing) this release. Recorded as a
  first-release scope decision, not a blocker.
- **OQ-2 — field-level vs file/DDM-level references.** **Recommendation: file/DDM-level only for
  first release.** A read/write edge records the accessed *view/DDM name* and the access site;
  individual DDM field references (for find-references on a field) are deferred. This matches the
  plan's "structural… names and relationships present in the source" scope and keeps
  `DataAccessEntry` file-scoped. Field-level extraction becomes a follow-up when hover/references on
  DDM fields (FR-28/FR-25) are built. Recorded, not a blocker.

Both resolutions are reflected in the task fixtures and expected results below.

---

## Task list

Ordering: model/contract → parser gaps → extraction (read, then write) → data definitions →
work files → wiring/cache/integration. Parser gaps land before the extraction that walks them.

### Task 1 — Data-access extraction scaffold + READ/STORE (already-parsed) reads & writes
**FR:** FR-19, FR-20 (the already-parsed subset) · **Story 1 & 2**
**Behavior:** Create `extractDataAccess(prog *Program) []model.DataAccessEntry` in `data.go`,
mirroring `extractEdges` (source-ordered, stable-sorted, panic-free over partial ASTs). Walk
`prog.Reads` → `EdgeReads` and `prog.Stores` → `EdgeWrites`, emitting one `DataAccessEntry` per
statement with the accessed view name and the statement `Source` range.
**Fixtures:** `testdata/dataaccess/01-read-store.NSP` — a program with `READ EMPLOYEES`,
`READ (5) VEHICLES BY MAKE`, `STORE EMPLOYEES`. (Reuse the shapes in `testdata/parser/06-read-store.nsp`
as a starting point; this fixture is data-access-specific.)
**Expected result:** three entries in source order: `{Name:"EMPLOYEES", Kind:EdgeReads}`,
`{Name:"VEHICLES", Kind:EdgeReads}`, `{Name:"EMPLOYEES", Kind:EdgeWrites}`, each with the correct
statement `Source` range and normalized (upper-case) name (lexer already uppercases identifiers —
assert it).
**Reuses/migrates:** the `extractEdges` structure (sort helper, `stmtRange`); populates the existing
`DataAccessEntry`/`EdgeReads`/`EdgeWrites` model members.
**Modeled gaps:** a `READ`/`STORE` with an empty `Target` (parser emitted a diagnostic already) is
**skipped** (no entry) — assert with a malformed line in the fixture; extraction never emits a
diagnostic (channel separation FR-17/M-6).
**DoD:** table-driven fixture test; source-ordered deterministic output; no panic on partial AST;
`go vet`/`gofmt` clean; not yet wired into `Analyze` (Task 9 wires it).
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** —

### Task 2 — Extend `DataAccessEntry` contract (`Name`, `NameRange`) + cache bump to 0.4.0
**FR:** FR-19, FR-20 (traceability: "accessed name normalized"; "records the access site")
· **Story 1 & 2**
**Behavior:** The current `DataAccessEntry` has `File`/`Kind`/`Source` only. Rename/clarify the name
carrier and add a range for the accessed operand so hover/references can point at the DDM name (not
just the whole statement). Add `NameRange Range`; keep `Source` as the whole-statement range; keep
`File` as the normalized name **or** rename to `Name` for clarity (decide in review — this task
records both options; recommendation: add `Name` + keep `File` as an alias-free rename since no
consumer reads it yet). Update Task 1's extractor to populate `NameRange`.
**Contract/consumer migration:** `internal/workspace/cache.go` persists `DataAccessEntry` verbatim
(JSON) — additive fields serialize automatically, but the **cache-format version must bump
`0.3.0` → `0.4.0`** (cache.go:16) so stale caches rebuild. Update the cache round-trip test.
**Fixtures:** reuse `01-read-store.NSP`.
**Expected result:** each entry carries a `NameRange` covering just the view-name token; cache
save/load round-trips the new fields; a `0.3.0` cache is treated as stale (forces rebuild).
**Reuses/migrates:** `model.DataAccessEntry`, `internal/workspace/cache.go` (version + round-trip test).
**DoD:** model change + extractor update + cache-version bump + cache round-trip test green; existing
cache tests still green; seam purity preserved (model stays backend-free).
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 1

### Task 3 — Parser: recognize `FIND` (new AST node + dispatch)
**FR:** FR-19 (parser prerequisite) · **Story 1**
**Behavior:** Add `FindStatement{StartPos, EndPos, Target string}` to ast.go and a
`FuzzParse`-safe `parseFindStatement` mirroring `parseReadStatement` (capture the view name on the
same line as `FIND`; skip descriptor/WITH/NUMBER clauses to next statement). Add `case
p.matches(TokenKeyword, "FIND", "FINDNAT")` to `dispatchStatement`, and add `FIND`/`FINDNAT` to
`isStatementKeyword` and the `parseDataSection` break-set so a `FIND` correctly terminates a
malformed `DEFINE DATA`.
**Fixtures:** `testdata/parser/20-find.nsp` — `FIND EMPLOYEES WITH NAME = 'SMITH'`,
`FIND NUMBER VEHICLES`, `FIND (3) EMPLOYEES`.
**Expected result:** three `FindStatement` nodes with `Target` = the view name; malformed
`FIND` (no operand) emits a ranged diagnostic and a node with empty `Target` (parser convention,
matching READ).
**Reuses/migrates:** `parseReadStatement` as the template; `isStatementKeyword`; the data-section
break-set (parser.go:133).
**Modeled gaps:** `FIND` with no same-line operand → diagnostic + empty-target node (never dropped
silently, never crashes).
**DoD:** parser fixture test; `FuzzParse` still non-panicking (widened parser — run the fuzz target
briefly); data-section termination test proving `FIND` breaks a block.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** —

### Task 4 — Extraction: FIND → read edge
**FR:** FR-19 · **Story 1**
**Behavior:** Extend `extractDataAccess` to walk `prog.Finds` → `EdgeReads`.
**Fixtures:** reuse `testdata/parser/20-find.nsp` **or** add `testdata/dataaccess/02-find.NSP`
(prefer the data-access dir for consistency).
**Expected result:** one `EdgeReads` `DataAccessEntry` per valid `FIND` with the normalized view
name and `NameRange`.
**Reuses/migrates:** Task 1's extractor; Task 3's AST node.
**Modeled gaps:** empty-target `FindStatement` skipped.
**DoD:** fixture test; source order preserved when interleaved with READ/STORE.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 1, Task 3

### Task 5 — Parser: recognize `GET` (new AST node + dispatch), incl. `GET SAME` gap
**FR:** FR-19 (parser prerequisite) · **Story 1**
**Behavior:** Add `GetStatement{StartPos, EndPos, Target string}` and `parseGetStatement`. Capture
the view name after `GET`; handle `GET SAME` (no view operand → `Target` stays empty, **no
diagnostic** — it is valid Natural that re-reads the current record, a modeled read with no static
target). Add `GET` to `dispatchStatement`, `isStatementKeyword`, and the data-section break-set.
**Fixtures:** `testdata/parser/21-get.nsp` — `GET EMPLOYEES (#ISN)`, `GET SAME`.
**Expected result:** two `GetStatement` nodes: first `Target:"EMPLOYEES"`, second `Target:""` with
**no** diagnostic (distinguish from malformed).
**Reuses/migrates:** `parseReadStatement` template.
**Modeled gaps:** `GET SAME` → valid, no target, no diagnostic (contrast with malformed READ/FIND).
**DoD:** parser fixture test asserting `GET SAME` produces no diagnostic; `FuzzParse` non-panicking.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** —

### Task 6 — Extraction: GET → read edge (with `GET SAME` skipped)
**FR:** FR-19 · **Story 1**
**Behavior:** Walk `prog.Gets` → `EdgeReads`, **skipping** entries with empty `Target` (covers
`GET SAME`: a read with no resolvable file/DDM name produces no file-scoped edge — the record's
file is the enclosing loop's view, deferred like record UPDATE/DELETE).
**Fixtures:** reuse `testdata/parser/21-get.nsp` or `testdata/dataaccess/03-get.NSP`.
**Expected result:** one `EdgeReads` entry for `GET EMPLOYEES`; **none** for `GET SAME`.
**Reuses/migrates:** Task 1 extractor; Task 5 node.
**Modeled gaps:** `GET SAME` deliberately produces no edge (documented in the extractor comment).
**DoD:** fixture test asserting exactly one entry.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 1, Task 5

### Task 7 — Parser: record-form UPDATE/DELETE → write nodes (without regressing SQL)
**FR:** FR-20 (parser prerequisite) · **Story 2**
**Behavior:** `parseUpdateStatement`/`parseDeleteStatement` currently emit **no node** for the
Adabas record form (they skip). Extend both so the **non-SQL** branch produces an Adabas write node.
Add `RecordUpdateStatement`/`RecordDeleteStatement` (or a single `RecordWriteStatement{Kind}`) to
ast.go carrying `StartPos/EndPos` and an optional `Label` (`UPDATE (LABEL)`), and, per OQ-2/record
semantics, **no file operand** (the written file is the referenced READ/FIND loop's view — binding
deferred). **Critical:** the SQL disambiguation heuristics (UPDATE+identifier+SET/WHERE ⇒ SQL;
DELETE+FROM ⇒ SQL) must be **preserved unchanged** — only the previously-skipped branch now emits an
Adabas node.
**Fixtures:** `testdata/parser/22-record-update-delete.nsp` — `UPDATE`, `UPDATE (0250)`, `DELETE`,
`DELETE (RD.)`; plus one SQL `UPDATE T SET C = 1 WHERE ...` and `DELETE FROM T WHERE ...` to prove
the SQL branch is untouched.
**Expected result:** two record-write nodes (UPDATE, DELETE) with labels captured where present;
the SQL forms still produce `SQLUpdateStatement`/`SQLDeleteStatement` and **no** record node.
**Reuses/migrates:** the two existing parse methods (shared-contract change — SQL regression tests in
DoD); `isProgramBoundary`/`skipToNextStatement`.
**Modeled gaps:** genuinely ambiguous shapes (e.g. `UPDATE #VAR` with no SET) — decide in review
whether to treat as Adabas write or skip; **recommendation: treat bare `UPDATE`/`DELETE` and
`UPDATE (label)`/`DELETE (label)` as Adabas record writes, and any identifier-led shape without
SET/WHERE/FROM as ambiguous → skip (no node, no false write)**, preserving current safety.
**DoD:** parser fixture test for both record and SQL forms; **existing `parser_sql_test.go` for
UPDATE/DELETE still green** (`17-sql-update-delete.nsp`); `FuzzParse` non-panicking; forward-progress
guaranteed (advance-on-entry preserved).
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** —

### Task 8 — Extraction: record UPDATE/DELETE → write edges
**FR:** FR-20 · **Story 2**
**Behavior:** Walk the new record-write nodes → `EdgeWrites`. Because record UPDATE/DELETE carry no
statement-level file operand, the `DataAccessEntry.Name` is **empty** and `Source` is the statement
range — the entry records *that a write occurs at this site* (impact-analysis value) even though the
file is bound later. Confirm read/write to the same view are distinguishable by `Kind`
(FR-20 acceptance criterion: STORE `EmployEES` write vs READ `EMPLOYEES` read).
**Fixtures:** `testdata/dataaccess/04-write-mix.NSP` — a READ loop with `UPDATE` inside, plus a
`STORE`, plus a `DELETE`.
**Expected result:** `EdgeWrites` entries for STORE (Name set), record UPDATE (Name empty), record
DELETE (Name empty); read/write kinds distinct on the same view name.
**Reuses/migrates:** Task 1 extractor; Task 7 nodes.
**Modeled gaps:** empty-name write is intentional and documented (not a diagnostic).
**DoD:** fixture test asserting kind separation and empty-vs-set Name; source order preserved.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 1, Task 7

### Task 9 — Wire data-access extraction into `Analyze`
**FR:** FR-19, FR-20 · **Story 1 & 2**
**Behavior:** In `analyzer.go:Analyze`, after `extractEdges`, call `extractDataAccess(ast)` and
assign to `result.DataAccess` (guarded by `ast != nil`, panic-recovered consistent with FR-43 —
verify the existing recovery covers this or add it). No diagnostics from extraction (channel
separation).
**Fixtures:** reuse the data-access fixtures via an `Analyze`-level test.
**Expected result:** `Analyze` returns a `FileAnalysis` whose `DataAccess` matches the extractor
output; end-to-end from raw bytes.
**Reuses/migrates:** `Analyze` (adds one call, mirrors the `extractEdges` wiring).
**DoD:** `Analyze`-level test; cache round-trip test (Task 2) now exercises real values;
seam test still green.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 1, Task 4, Task 6, Task 8

### Task 10 — Parser: capture data-section `Kind` (stop discarding LOCAL/PARAMETER/GLOBAL)
**FR:** FR-21 (parser prerequisite) · **Story 3**
**Behavior:** `parseDataSection` skips the section keywords at parser.go:123. Add `Kind string` (or a
typed `DataSectionKind`) to `DataSection` and set it from the LOCAL/PARAMETER/GLOBAL/LINKAGE keyword
before skipping. A `DEFINE DATA` may contain multiple sections (`LOCAL` then `PARAMETER`); decide in
review whether to emit one `DataSection` per keyword or one per `DEFINE DATA`. **Recommendation:
first release emits one `DataSection` per section keyword** so PARAMETER is cleanly isolable for
signatures; adjust the parse loop to close a section at the next section keyword.
**Fixtures:** `testdata/parser/23-data-sections.nsp` — a `DEFINE DATA` with `LOCAL`, `PARAMETER`, and
`GLOBAL USING GDA` sections, each with a couple of fields.
**Expected result:** `DataSection` nodes tagged `local`/`parameter`/`global`; fields correctly
assigned to their section.
**Reuses/migrates:** `parseDataSection`; array/redefine parsing unchanged (OQ-1 reuse).
**Modeled gaps:** a section with no fields is valid (empty `Fields`); a malformed field line degrades
per existing behavior (no crash) — assert with one bad line.
**DoD:** parser fixture test; existing `parser_test`/`ast_test` for DEFINE DATA still green;
`FuzzParse` non-panicking.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** —

### Task 11 — Extraction: data definitions → symbols, parameter interface captured
**FR:** FR-21 · **Story 3**
**Behavior:** Add `extractDefinitions(prog *Program)` producing declared variables from every
`DataSection` (flattening groups/redefines with level, verbatim type/format, dimensions), and a
distinguishable **parameter interface** (the fields of the `parameter`-kind section) so hover can
back a signature. Represent these as `model.SymbolEntry` values (populate `FileAnalysis.Symbols`)
and/or a small typed definition/parameter structure on `FileAnalysis` — see Task 12 for the model
decision. Well-formed block fully extracted; malformed block degrades gracefully (no crash).
**Fixtures:** reuse `testdata/parser/23-data-sections.nsp` and the existing
`07-data-arrays.nsp`/`08-data-redefine.nsp` to prove arrays and redefines survive extraction.
**Expected result:** every declared field appears as a definition with its identifying attributes
(name, level, type, dimensions); the parameter section's fields are tagged/queryable as the
interface; array bounds and redefine nesting preserved per OQ-1.
**Reuses/migrates:** the AST `DataField`/`ArrayBound` structures (no grammar deepening);
`model.SymbolEntry` (extend `SymbolKind` with data-item/parameter kinds if needed — Task 12).
**Modeled gaps:** malformed field lines already produce partial `DataField`s; extraction tolerates
empty names/types without panicking.
**DoD:** fixture test covering local/param/global, arrays, redefines; deterministic order;
graceful degradation asserted.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 10

### Task 12 — Model: data-definition / parameter surface + `SymbolKind` values (+ cache)
**FR:** FR-21 · **Story 3**
**Behavior:** Add the model members Task 11 needs. Options (decide in review):
(a) extend `SymbolKind` with `SymbolDataItem`/`SymbolParameter` and carry level/type/dims on an
extended `SymbolEntry`; or (b) add a dedicated `DataDefinition`/`Parameter` type +
`FileAnalysis.Definitions`. **Recommendation: (b)** — a typed `DataDefinition{Name, Level, Type,
Dimensions, SectionKind, Range, Children}` and `FileAnalysis.Definitions []DataDefinition`, keeping
`SymbolEntry` lean for the later LSP-symbol mapping. Keep it backend-free (no parser types).
**Contract/consumer migration:** persist `Definitions` in `internal/workspace/cache.go` (new JSON
field) — additive, but confirm the **cache-format version bump from Task 2 (0.4.0) covers it**
(land Task 2 and Task 12 under the same version if both persist; otherwise bump again). Update the
cache round-trip test to include definitions.
**Fixtures:** reuse Task 11 fixtures via a cache round-trip test.
**Expected result:** definitions round-trip through the cache; model stays free of backend internals;
seam test green.
**Reuses/migrates:** `internal/model`, `internal/workspace/cache.go` (+ its tests).
**DoD:** model + cache change; round-trip test; seam purity test green; single coherent
cache-format version across Tasks 2 & 12.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 11 (and coordinate version with Task 2)

### Task 13 — Wire definition extraction into `Analyze`
**FR:** FR-21 · **Story 3**
**Behavior:** Call `extractDefinitions(ast)` in `Analyze` and assign to
`result.Definitions`/`result.Symbols` (panic-recovered, FR-43).
**Fixtures:** `Analyze`-level test over `23-data-sections.nsp`.
**Expected result:** `FileAnalysis` carries the definitions/parameter interface end-to-end.
**DoD:** `Analyze` test; cache round-trip exercises real values; seam test green.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 11, Task 12

### Task 14 — Parser: recognize `DEFINE WORK FILE`
**FR:** FR-22 (parser prerequisite) · **Story 4**
**Behavior:** `WORK`/`FILE` are not currently keywords and there is no dispatch. In `parseDefine`
(after `DATA`/`SUBROUTINE`/`MAP`), add a branch for `WORK FILE` (match on the identifier/keyword
literals — no lexer keyword change strictly required if matched by literal, but confirm the lexer
tokenizes `WORK` as an identifier so `matchesLiteral("WORK")` works; add to the keyword set only if
needed). Add `WorkFileDefinition{StartPos, EndPos, Number int, Name string, NameRange Range}` to
ast.go and `prog.WorkFiles`. Parse `DEFINE WORK FILE n 'name' [attrs]`.
**Fixtures:** `testdata/parser/24-work-file.nsp` — `DEFINE WORK FILE 1 'REPORT.TXT'`,
`DEFINE WORK FILE 2 #DYNNAME`.
**Expected result:** two `WorkFileDefinition` nodes; number captured; literal name captured;
variable name (`#DYNNAME`) captured as-is (dynamic — flagged in Task 15).
**Reuses/migrates:** `parseDefine` dispatch; `consumeStringTarget`.
**Modeled gaps:** malformed `DEFINE WORK FILE` (missing number/name) → diagnostic + partial node,
no crash.
**DoD:** parser fixture test; `FuzzParse` non-panicking.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** —

### Task 15 — Extraction: work-file definitions associated with the object (+ model/cache)
**FR:** FR-22 · **Story 4**
**Behavior:** Extract `prog.WorkFiles` into a model surface associated with the declaring file.
Add `model.WorkFile{Number int, Name string, Range Range}` and `FileAnalysis.WorkFiles []WorkFile`
(backend-free); persist in cache (same version family as Tasks 2/12, bump if it lands separately).
Wire into `Analyze` (panic-recovered). A dynamic/variable work-file name is recorded verbatim (a
modeled gap — not a diagnostic).
**Fixtures:** reuse `24-work-file.nsp` via an `Analyze`-level test; add
`testdata/dataaccess/05-work-file.NSP` if a dedicated fixture is cleaner.
**Expected result:** work files extracted, associated with the object, number + name present;
literal vs variable name distinguishable.
**Reuses/migrates:** `internal/model`, `internal/workspace/cache.go` (+ tests), `Analyze`.
**Modeled gaps:** variable work-file name = dynamic, recorded not diagnosed.
**DoD:** extraction + `Analyze` test; cache round-trip; seam purity; deterministic order.
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 14 (coordinate cache version)

### Task 16 — Integration & regression sweep
**FR:** FR-19..22, FR-43 · **all stories**
**Behavior:** A combined fixture exercising reads (READ/FIND/GET), writes (STORE/record
UPDATE/DELETE), a `DEFINE DATA` with LOCAL+PARAMETER+GLOBAL, and a `DEFINE WORK FILE`, plus one
deliberately malformed statement of each kind, to prove: (1) all edges/definitions/work-files
extracted and source-ordered; (2) malformed input degrades gracefully with parser diagnostics but no
extraction crash and no false edges (FR-43); (3) `FuzzParse`/`FuzzProcessFile` still non-panicking.
**Fixtures:** `testdata/dataaccess/06-combined.NSP` and a `09-malformed`-style companion.
**Expected result:** deterministic combined `FileAnalysis`; diagnostics and extraction on separate
channels.
**DoD:** integration-style analyzer test; `just verify` green (fmt/vet/build/unit-race/integration).
**Agents:** tdd-red → tdd-green → tdd-refactor · **Depends on:** Task 9, Task 13, Task 15

---

## Traceability (acceptance criteria → tasks)

| Story / criterion | Task(s) |
|---|---|
| S1 — read relationship + access site (READ/FIND/GET) | 1, 3–6 |
| S1 — accessed name normalized for case | 1 (lexer uppercases; asserted), 2 (`Name`) |
| S1 — fixture per read construct | 1 (READ), 4 (FIND), 6 (GET) |
| S2 — write relationship + access site (STORE/UPDATE/DELETE) | 1, 7, 8 |
| S2 — read vs write distinguishable | 8 (Kind separation) |
| S2 — fixture per write construct | 1 (STORE), 8 (record UPDATE/DELETE) |
| S3 — data-definition blocks (local/global/parameter/related) | 10, 11 |
| S3 — parameter interfaces for signatures | 10 (Kind), 11 (parameter section) |
| S3 — well-formed fully extracted; malformed degrades gracefully | 10, 11 (graceful degradation asserted) |
| S3 — fixtures per data-section kind | 10, 11 |
| S4 — work-file definitions extracted & associated with object | 14, 15 |
| S4 — fixture | 14, 15 |
| FR-43 graceful degradation / channel separation | 1, 3, 5, 7, 10, 14, 16 |

---

## Reviews required (`/review-feature`)

- **review-seam** — `internal/model` gains members (`DataAccessEntry` fields, `DataDefinition`,
  `WorkFile`, possibly `SymbolKind` values) and `cache.go` changes; verify model stays backend-free
  and LSP-facing code still depends only on the interface/model.
- **review-robustness** — the parser is widened (FIND, GET, record UPDATE/DELETE, DEFINE WORK FILE,
  data-section Kind); verify malformed-input handling, `FuzzParse`/`FuzzProcessFile` still
  non-panicking, and no regression to the existing SQL UPDATE/DELETE disambiguation.
- **review-docs** — this feature changes capability ("Project state" in CLAUDE.md/README, the
  data-access bullet, cache-format version); anticipate the `/finalize-feature` sync.
- **(not concurrency)** — no indexer/watcher/server changes; extraction is pure and per-file.

---

## Review remediation (round 1 — 2026-07-02)

### Task 17 (regression) — no silent drop of INDEPENDENT/CONTEXT/OBJECT DEFINE DATA sections
**Finding:** review-extraction (major). `isSectionKeyword` recognizes only LOCAL/PARAMETER/GLOBAL/LINKAGE;
a `DEFINE DATA` `INDEPENDENT` (AIVs, `+` sigil), `CONTEXT` (RPC), or `OBJECT` (NaturalX) section causes
the section loop to terminate and its fields to **vanish with no diagnostic** — violates the no-silent-drop
invariant (M-6/FR-17) and the plan's "and related sections" scope.
**Fix (regression-first):** recognize INDEPENDENT/CONTEXT/OBJECT as section keywords (+ `Kind`), extract
their fields via the existing path so definitions are not dropped. Verify the exact section syntax and AIV
`+`-sigil lexing with `natural-expert`; if AIV names don't lex, ensure at minimum the section is recognized
and no field is silently lost. Add a fixture + failing test reproducing the drop, then fix.
**Also fold in nits:** `data.go` package-doc "FR-19..23" → "FR-19..22"; combined-test comment omits
`READ LOCATIONS`.
**DoD:** RED test proving the drop is fixed; existing DEFINE DATA tests green; `just verify` green.

### Task 18 (regression, round 2) — no silent drop of `+`-prefixed AIV fields in INDEPENDENT sections
**Finding:** review-extraction round 2 (major). Task 17 made the parser RECOGNIZE INDEPENDENT/CONTEXT/OBJECT
sections, but a real `1 +AIV-NAME (A20)` line still yields 0 definitions + 0 diagnostics — the lexer
(`lexer.go`) doesn't lex `+` as an identifier-start sigil (only `#`/`&`/`@`), so the field is silently
dropped. Since AIV names are ALWAYS `+`-prefixed, this drops the practical content of every INDEPENDENT
section — the same M-6/FR-17 violation. Task 17's fixture used `#`-names and missed it.
**Fix (regression-first):** a `+`-prefixed AIV field is either EXTRACTED as a `model.DataDefinition`
(preferred) OR produces a diagnostic — never silently dropped. Confirm exact AIV sigil rules with
`natural-expert`. Prefer a CONTAINED parser-side capture of `+ident` in data-field-name position
(context-sensitive) to avoid a global lexer change that could break arithmetic (`#A+#B`); add a diagnostic
fallback for any still-unparseable field line inside a recognized data section. Fixture must use a REAL
`+AIV` name.
**DoD:** RED test with a `+`-prefixed AIV asserting no-silent-drop (definition present, SectionKind
"independent"); no regression to arithmetic/expression lexing or existing DEFINE DATA tests; `just verify`
green; FuzzParse non-panicking.

---

## Open questions — RESOLVED (approved 2026-07-02)

1. **OQ-1 (array/redefine depth):** ✅ reuse existing AST depth as-is (no deepening).
2. **OQ-2 (field-level vs file-level references):** ✅ file/DDM-level only this release.
3. **`DataAccessEntry` shape (Task 2):** ✅ add `Name`+`NameRange`.
4. **Record UPDATE/DELETE representation (Task 7/8):** ✅ **empty-name write edge** recording the
   site; the file is bound later (no enclosing-loop back-reference this release). Confirmed acceptable
   for first-release impact analysis.
5. **Definition model shape (Task 12):** ✅ dedicated `DataDefinition` + `FileAnalysis.Definitions`.
6. **Single cache-format bump:** ✅ land Tasks 2, 12, 15 persisted fields under one `0.4.0` bump.
