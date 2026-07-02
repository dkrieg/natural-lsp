# Feature: Parser — Embedded SQL

**Status:** Planned  
**PRD requirements:** FR-30 (syntax diagnostics), FR-43 (graceful degradation); NFR-15 (replaceable backend); M-5, M-6 (regression fixtures / no silent gaps)  
**Priority / phase:** P0 (parser foundation extension; groundwork for data-access extraction)  
**Depends on:** [00-parser-foundation](../00-parser-foundation/plan.md)  

## Summary

Teach the hand-written lexer and recursive-descent parser to handle Software AG Natural's **embedded SQL** so that both native Natural SQL statements and the `PROCESS SQL` / flexible-SQL escape hatch are lexed and parsed into the AST with real source positions, instead of tripping the parser or being swallowed as unrecognized lines.

This is deliberately a **lex + parse + AST** feature only. It gives every subsequent feature a real syntax tree to work from; it does **not** yet emit edges, bind DDM table names, or bind host variables to their `DEFINE DATA` fields. Those belong to the later data-access / SQL-extraction feature (feature 08). The knowledge base (`.claude/knowledge/natural/embedded-sql.md`, verified 2026-06-30) is the authoritative reference for the syntax modeled here.

Natural embeds SQL in **two distinct styles**, and the parser must handle both:

- **Native Natural SQL statements** — `SELECT`/`SELECT SINGLE`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`, `READ RESULT SET` — written as first-class Natural statements (no `EXEC SQL … END-EXEC` block, no trailing semicolon, Natural continuation, and a mode-dependent loop terminator). The Natural compiler parses these, so we parse them into proper AST nodes.
- **Flexible SQL** — a raw SQL string carried by `PROCESS SQL` and delimited by `<<` … `>>` that the Natural compiler does **not** parse. The parser captures this as an **opaque multi-line region** — a raw text span with its source range — and does **not** look inside it at all. Discovering the host-variable references the block contains (and binding them) is deferred wholesale to the extraction feature.

Two design facts run through the feature and neither produces a silent gap:

- **Opaque ≠ dropped.** A flexible-SQL `<<…>>` body is retained on the AST as an opaque span with its full source range, not discarded. The parser does not tokenize its interior as Natural, nor scan it for host-variable references — that span is handed to the extraction feature verbatim.
- **Parse errors surface as diagnostics.** Malformed SQL statements produce ranged syntax diagnostics (FR-30) and error recovery keeps the surrounding valid statements (M-6) — the same channel and contract as the existing parser.

## User Stories

### Story 1 — Lexer: flexible-SQL delimiters and host-variable colon (NFR-15, M-6)

**As a** parser implementer, **I want** the lexer to recognize the flexible-SQL delimiters and the colon host-variable prefix **so that** the parser can consume embedded SQL without mis-tokenizing it.

**Acceptance criteria:**

- [ ] `<<` and `>>` are recognized as flexible-SQL delimiters **only in SQL context** (inside a `PROCESS SQL` / SQL statement). Outside SQL context, `<` and `>` retain their meaning as Natural comparison operators — a bare `<`/`>` comparison must still lex as it does today.
- [ ] The span between `<<` and `>>` is treated as a single **opaque region**: it may span multiple physical lines with no continuation character and may contain end-of-line and full-line comments, and its interior is **not** tokenized as Natural keywords/identifiers **nor scanned for host-variable references** — it is captured as a raw text span with its source range only.
- [ ] In **native** SQL context (tokenized clauses such as `INTO`/`WHERE`/`VALUES`/`SET`), a host-variable operand may carry an **optional leading colon** (`:name`) as well as the bare Natural name; the lexer accepts both so native clauses parse. (The richer qualifier forms — `:U:`/`:G:`/`:T:`, `INDICATOR`/`LINDICATOR`, array index notation — occur inside the opaque `<<…>>` body and are therefore **not** lexed here; see Out of scope.)
- [ ] There is **no** `EXEC SQL … END-EXEC` block construct and **no** trailing-semicolon statement terminator — the lexer/parser must not look for either.
- [ ] A fixture suite covers the delimiter, opaque-span, and optional-colon token forms with expected token values, and confirms the non-SQL `<`/`>` comparison case is unchanged.

### Story 2 — AST nodes for embedded SQL (NFR-15)

**As a** parser implementer, **I want** AST node types for the embedded-SQL constructs **so that** the tree can later be traversed for extraction.

**Acceptance criteria:**

- [ ] AST nodes exist for the native SQL statements in scope: `SELECT` (cursor loop), `SELECT SINGLE`, `INSERT`, SQL-form `UPDATE`, SQL-form `DELETE`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`, and `READ RESULT SET`, plus a `PROCESS SQL` node.
- [ ] Each SQL node carries source position information (start/end line/column) for the whole statement, consistent with existing AST nodes.
- [ ] The `SELECT`/`READ RESULT SET` loop nodes model a **loop body** (child statements) the way the existing DB-loop-style nodes do; `SELECT SINGLE` has no loop body.
- [ ] The `SELECT` node exposes the operands a later feature will need without yet binding them: the selected columns, the `INTO` target host variables, the `FROM` table operand(s), and the host-variable operands appearing in tokenized clauses (`WHERE`, etc.). (Represented structurally; **no** binding to `DEFINE DATA` or DDM in this feature.)
- [ ] The `PROCESS SQL` node exposes its leading `ddm-name` operand and its opaque `<<…>>` body as a **raw text span with source range** — and nothing else. It does **not** carry a parsed list of the host-variable references inside the body; extracting those from the opaque span is the extraction feature's job.
- [ ] A fixture per node kind demonstrates correct structure and positions.

### Story 3 — Parser: native SQL statements and mode-dependent loop terminators (FR-30, M-6)

**As a** user, **I want** the parser to parse native Natural SQL statements into the AST **so that** embedded SQL no longer breaks parsing and downstream features have a tree to work from.

**Acceptance criteria:**

- [ ] The parser parses each in-scope native SQL statement into its AST node with correct positions.
- [ ] `SELECT … END-SELECT` is parsed as a **database loop** whose body contains the intervening statements — structurally like the existing `READ`-style loops (Natural manages the cursor; there is no application-level `DECLARE`/`OPEN`/`FETCH`/`CLOSE` to parse).
- [ ] **Both** loop terminators are accepted: `END-SELECT` (structured mode) **and** `LOOP` (reporting mode) close a `SELECT` loop; likewise `END-RESULT` and `LOOP` close a `READ RESULT SET` loop.
- [ ] `SELECT SINGLE` is parsed as a singleton with **no** loop body / no terminator.
- [ ] Native `SELECT` parses `INTO` on the cursor-oriented form (Natural allows `INTO` on cursor selects, unlike ISO SQL), and host-variable operands are accepted **with or without** the leading colon (bare Natural name is the idiomatic native form; colon is also accepted).
- [ ] `PROCESS SQL ddm-name << … >>` parses with the DDM-name operand and the opaque body captured as a raw span per Story 1/Story 2; the parser does **not** interpret the body's interior (including any host-variable references) — it is passed through untouched to the extraction feature.
- [ ] The parser disambiguates the shared keywords `UPDATE` / `DELETE` (and `STORE`) between their **SQL form** (`SET …`, `WHERE …`, a table operand) and their existing **Adabas form** (operating on a `READ`/`FIND` loop record), choosing the SQL AST node only for the SQL clause shape.
- [ ] A fixture per statement type demonstrates correct AST production, including one structured-mode and one reporting-mode loop fixture.

### Story 4 — Syntax diagnostics and error recovery for embedded SQL (FR-30, FR-43, M-6)

**As a** user, **I want** malformed embedded SQL to produce visible diagnostics without derailing the rest of the file **so that** parse errors are surfaced, not silently dropped, and the analyzer never crashes.

**Acceptance criteria:**

- [ ] A malformed native SQL statement (e.g. a `SELECT` loop with no terminator, or a `PROCESS SQL` with an unterminated `<<` region) produces a ranged syntax diagnostic on `Program.Diagnostics`, consistent with the existing parser's diagnostic contract.
- [ ] Error recovery retains the valid statements surrounding a malformed SQL statement — no silent gaps (M-6).
- [ ] An unterminated flexible-SQL region (`<<` with no closing `>>` before end of source) is reported as a diagnostic rather than consuming the remainder of the file silently or panicking.
- [ ] Extraction/parsing over partial or malformed embedded SQL never panics (FR-43); the existing `FuzzParse` target continues to hold with SQL inputs added to the corpus.
- [ ] A fixture per SQL parse-error case demonstrates diagnostic emission and surrounding-statement retention.

### Story 5 — Analyzer integration and regression fixtures (NFR-15, M-5)

**As a** developer, **I want** the embedded-SQL parsing integrated behind the `Analyzer` interface and covered by permanent fixtures **so that** the backend stays swappable and regressions are caught.

**Acceptance criteria:**

- [ ] `Analyzer.Analyze(path, content)` returns `FileAnalysis` whose `AST` contains the parsed SQL nodes, and whose `Diagnostics` include any SQL syntax errors — via the existing wiring, with **no** change to the `Analyzer` interface signature.
- [ ] The `internal/model` output contract requires no change for this feature (parse/AST only; no new edge kinds or cache-format bump). If any addition proves unavoidable it is called out as a shared-contract change with a migration note.
- [ ] Every embedded-SQL construct and parse-error case has at least one fixture under `testdata/parser/` (SQL subdirectory), stored as permanent regression tests; fixtures use only sanitized, non-proprietary Natural code (the KB's minimal fixture is a suitable starting point).
- [ ] Tests verify AST structure (node types, positions, loop-body nesting, opaque-region span) and diagnostics for malformed input.

## Out of scope

- **Edge extraction and DDM binding.** Emitting read/write edges for native SQL `FROM`/`INTO`/`INSERT INTO`/SQL-`UPDATE`/`DELETE` table operands (which resolve to `.NSD` DDMs), and `CALLDBPROC` call-like edges, belong to the data-access / SQL-extraction feature (feature 08). This feature only produces the AST those edges will be read from.
- **All host-variable handling inside flexible SQL.** Scanning the opaque `<<…>>` body for colon host-variable references — *and* the `:U:`/`:G:`/`:T:` qualifier, `INDICATOR`/`LINDICATOR`, and array-index (`:NAME(*)`) forms that occur there — is entirely the extraction feature's job. This feature hands the body over as a raw span and does not look inside it.
- **Host-variable binding.** Binding any host-var reference (native-clause or flexible-SQL) back to its `DEFINE DATA` field, and modeling USING/GIVING/text data-flow, is deferred to the extraction/data-access feature. This feature captures native-clause operands structurally only, and does not touch flexible-SQL host-vars at all.
- **Semantic / Common-Set vs Extended-Set diagnostics.** Flagging an Extended-Set construct on a non-supporting backend, or any DBMS-specific validity checking, is not part of parsing.
- **Tokenizing the interior of flexible SQL.** The `<<…>>` body is opaque; nothing inside it is parsed, tokenized, or scanned in this feature.
- **Deep SQL-clause grammar.** Fully modeling `GROUP BY` / `HAVING` / `ORDER BY` / `MERGE` internals, scalar functions, and the `FOR n ROWS` / `DB2ARRY` bulk-array clause beyond what is needed to delimit the statement and capture host-var / table operands.
- **Resolution and transaction-edge modeling** (`COMMIT`/`ROLLBACK` as transaction edges) — later features.

## Open questions

- **Reserved-word colon requirement.** The KB notes the colon is *mandatory* in native SQL when a host-variable name is identical to an SQL reserved word (e.g. `:DATE`, `:USER`). Should the parser attempt to enforce/flag this (a diagnostic), or simply accept both forms and leave reserved-word awareness to a later feature?
- **Depth of SQL-clause parsing.** How much of `WHERE` / `GROUP BY` / `HAVING` / `ORDER BY` should be structured in the AST now versus captured as a token span, given extraction only needs table names and host-var references? (Leaning: capture operands, don't fully model clause grammar — confirm.)
- **UPDATE/DELETE/STORE disambiguation reliability.** Is clause-shape disambiguation (SQL `SET`/`WHERE`/table operand vs Adabas record form) sufficient in all common cases, or are there ambiguous shapes that need a heuristic or a diagnostic? Which form should the parser default to when the shape is genuinely ambiguous?
- **Reporting-mode scope.** The parser has no general reporting-mode support today; this feature adds `LOOP` as an SQL-loop terminator. Is accepting `LOOP` for SQL loops in isolation sufficient, or does it force broader reporting-mode handling that should be its own feature?

**Resolved during planning:** the flexible-SQL host-var scan is *not* owned here. The `<<…>>` body is captured as a raw opaque span and all host-var discovery/qualifier handling/binding is deferred to the extraction feature (Option B). Host-var *qualifier representation* therefore moves to that feature's plan.
