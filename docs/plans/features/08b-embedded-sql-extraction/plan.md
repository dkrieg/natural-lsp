# Feature: Embedded-SQL Extraction

**Status:** Planned  
**PRD requirements:** FR-19 (read relationships), FR-20 (write relationships), FR-21 (data definitions / host-var references); FR-10/FR-14 (call-like edge, for `CALLDBPROC`); FR-17, FR-18 (modeled gaps distinct from parse errors); FR-43 (graceful degradation); M-6 (no silent gaps)  
**Priority / phase:** P1 (embedded SQL is a DBMS-interface add-on, less universal than Adabas-core data access)  
**Depends on:** [00-parser-embedded-sql](../00-parser-embedded-sql/plan.md) (the SQL AST + opaque `<<…>>` span), [08-data-access-extraction](../08-data-access-extraction/plan.md) (the DDM read/write edge model this reuses), [06-call-dependency-extraction](../06-call-dependency-extraction/plan.md) (the edge model + extraction pattern)  

## Summary

Walk the embedded-SQL AST produced by the parser feature and emit the relationships that Natural's SQL statements carry: the DDM tables they read and write, the `DEFINE DATA` fields their host variables reference, and the stored-procedure calls they make. This is the extraction half of embedded-SQL support; the parser feature ([00-parser-embedded-sql](../00-parser-embedded-sql/plan.md)) already turned the source into an AST and left every host-var and table operand **unbound**.

Grounding facts (all verified in `.claude/knowledge/natural/embedded-sql.md`, 2026-06-30):

- **Native SQL table operands are DDM names.** A `FROM` / `INTO` / `INSERT INTO` / SQL-`UPDATE` / SQL-`DELETE` table name resolves to a `.NSD` DDM — the **same DDM namespace** used by Adabas `READ`/`FIND` — not a raw physical SQL table. Bind and edge it exactly like an Adabas DDM access, reusing feature 08's read/write edge model.
- **Host variables are read/writes of `DEFINE DATA` fields.** A SQL host-var (`:FIELD`, or a bare Natural name in native SQL) binds back to its declaration like any other variable use.
- **This feature owns the flexible-SQL interior.** Because the parser hands `PROCESS SQL`'s `<<…>>` body over as a single opaque span (Option B), *this* feature scans that span for `:host-var` references — everything else in the body stays pass-through text the compiler never resolves.

Two modeled gaps carry over from the parser feature and stay distinct from parse-error diagnostics (FR-17):

- **The opaque body is scanned for one thing only.** Inside `<<…>>` we extract colon host-variable references and the `PROCESS SQL` `ddm-name` operand; the SQL table names *inside* the opaque body are pass-through text and are **not** bound (the KB is explicit — do not bind table names found in flexible SQL).
- **Colon rule differs by context.** In native SQL a host-var may be bare or colon-prefixed (both bind); inside `<<…>>` the colon is mandatory, so the scan keys on `:name`.

## User Stories

### Story 1 — Native SQL read edges (FR-19)

**As a** developer, **I want** native SQL reads to show which DDM tables an object queries **so that** SQL data flow is traceable alongside Adabas.

**Acceptance criteria:**

- [ ] `SELECT` (cursor loop), `SELECT SINGLE`, and `READ RESULT SET` produce a **read** relationship to the `FROM` table operand, recorded as a DDM name (normalized for case) with the access site.
- [ ] The relationship reuses feature 08's read-edge representation so SQL and Adabas reads are uniform to downstream consumers (references, hover, outline).
- [ ] A fixture per read-style SQL construct extracts the DDM read edge with zero false edges.

### Story 2 — Native SQL write edges (FR-20)

**As a** developer doing impact analysis, **I want** native SQL writes surfaced **so that** I can assess change risk on SQL-touched tables.

**Acceptance criteria:**

- [ ] `INSERT`, SQL-form `UPDATE`, SQL-form `DELETE`, and `MERGE` produce a **write** relationship to their target table operand (a DDM name), distinguishable from read relationships to the same DDM.
- [ ] A fixture per write-style SQL construct extracts the DDM write edge correctly.

### Story 3 — Host-variable references in native SQL (FR-21)

**As a** developer, **I want** the host variables used by native SQL bound to their declarations **so that** references and hover work on them.

**Acceptance criteria:**

- [ ] Host-variable operands in native clauses (`INTO`, `WHERE`, `VALUES`, `SET`) bind back to their `DEFINE DATA` field, whether written bare or colon-prefixed.
- [ ] The reserved-word case (a native host-var identical to an SQL reserved word, which *must* carry the colon — e.g. `:DATE`) binds correctly.
- [ ] A fixture with both bare and colon-prefixed native host vars binds all of them.

### Story 4 — Host-variable and DDM extraction from flexible SQL (FR-19/FR-20/FR-21, M-6)

**As a** developer, **I want** the host variables and DDM inside a `PROCESS SQL` block surfaced **so that** flexible SQL isn't a blind spot.

**Acceptance criteria:**

- [ ] The `PROCESS SQL` `ddm-name` operand produces a DDM relationship (read/write intent per the block, or a neutral access if intent can't be determined without parsing the opaque body).
- [ ] The opaque `<<…>>` body is scanned for **colon-mandatory** `:host-var` references, and each binds to its `DEFINE DATA` field; the qualifier forms `:U:` / `:G:` / `:T:`, the `INDICATOR` / `LINDICATOR` indicator vars, and array notation (`:NAME(*)`, `:SALARY(01:10)`) are recognized during that scan.
- [ ] SQL **table names appearing inside** the `<<…>>` body are **not** bound (they are pass-through text) — no false DDM edge from opaque-body table names.
- [ ] A fixture with a `PROCESS SQL` block exercising bare-vs-colon, a qualifier form, and an in-body table name confirms only the host vars and the `ddm-name` operand are extracted.

### Story 5 — CALLDBPROC / stored-procedure calls (FR-10/FR-14)

**As a** developer, **I want** `CALLDBPROC` surfaced as a call-like relationship **so that** stored-procedure dependencies are visible.

**Acceptance criteria:**

- [ ] `CALLDBPROC` produces a call-like relationship to the named DB procedure, with the call site preserved.
- [ ] `READ RESULT SET` is associated with its preceding `CALLDBPROC` (it is only valid in that pairing).
- [ ] A fixture demonstrates the `CALLDBPROC` edge and the `READ RESULT SET` association.

### Story 6 — Modeled gaps and robustness (FR-17, FR-43, M-6)

**As a** maintainer, **I want** SQL extraction to degrade gracefully and never invent edges **so that** partial or unusual SQL doesn't crash or mislead.

**Acceptance criteria:**

- [ ] Extraction over a partial/malformed embedded-SQL AST never panics and retains the edges it could extract (FR-43); a fuzz target guards the SQL extraction entry point.
- [ ] SQL parse errors remain on the diagnostics channel (owned by the parser feature) and are never conflated with extraction edges (FR-17).
- [ ] Fixtures live under the extraction package's `testdata/`, using only sanitized, non-proprietary Natural code (the KB's minimal fixture is the seed).

## Out of scope

- **Parsing embedded SQL.** The lexer/parser/AST and SQL syntax diagnostics are [00-parser-embedded-sql](../00-parser-embedded-sql/plan.md). This feature consumes that AST.
- **Cross-library resolution.** Binding an extracted SQL edge across the steplib chain (and DDM-namespace resolution) belongs to the resolution feature ([07](../07-call-dependency-resolution/plan.md)); this feature extracts the reference with caller context.
- **Physical table/DDM metadata.** Resolving the physical SQL table behind a DDM, or column-level DB metadata, is out of PRD scope.
- **Extended-Set / Common-Set semantic diagnostics.** Flagging an Extended-Set construct on a non-supporting backend is not extraction.
- **Transaction-edge modeling.** `COMMIT` / `ROLLBACK` as transaction edges — low priority, deferred.
- **USING/GIVING data-flow direction.** Interpreting `:U:`/`:G:`/`:T:` to drive precise read-vs-write direction per host var — capture the qualifier; deriving direction semantics is a later refinement (see open questions).

## Open questions

- **Model changes.** Does the DDM read/write edge kind already exist from feature 08, and can SQL reuse it verbatim? Is `CALLDBPROC` a new edge kind or a reuse of the existing call edge? Any new edge kind / persisted host-var reference likely bumps the cache-format version — confirm against `internal/model` as-built when this is planned.
- **DDM-name resolution reuse.** Does the resolver's DDM namespace (feature 07) already accept SQL-sourced DDM names, or does SQL need a distinct resolution path?
- **`PROCESS SQL` intent.** Without parsing the opaque body, can read-vs-write intent for the `ddm-name` operand be inferred (e.g. from a leading verb token), or should it be a neutral "accesses" edge?
- **Host-var direction.** Should `:U:` (USING/input) vs `:G:` (GIVING/output) drive read-vs-write classification of the host-var field reference, or is a plain reference sufficient for the first release?
- **`:T:` text variables.** A `:T:` host var splices its *contents* into the SQL as literal text — is it still a plain field reference for extraction purposes, or does it need distinct treatment?
