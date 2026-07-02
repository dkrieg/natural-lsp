# Tasks: Parser — Embedded SQL

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-30 (syntax diagnostics), FR-43 (graceful degradation), NFR-15 (replaceable backend), M-5 (permanent regression fixtures), M-6 (no silent gaps)
**Scope:** lex + parse + AST + syntax diagnostics ONLY. No edge extraction, no DDM binding, no host-variable binding — those are deferred to feature `08b-embedded-sql-extraction`. The `PROCESS SQL` `<<…>>` body is captured as a single **raw opaque span** (text + range); its interior is never tokenized or scanned.

---

## Current-state findings & impact

Surveyed `internal/analysis/natural/` (`lexer.go`, `parser.go`, `ast.go`, `analyzer.go`, `fuzz_test.go`, the `parser_*_test.go` suite) and `internal/model/model.go`. Everything below is planned against the code as it is.

### What already exists and will be reused

- **Lexer (`lexer.go`).** Case-normalizing (uppercases identifiers/keywords), lexes `#`/`&`/`@`-prefixed hyphenated identifiers as single tokens, handles `*`/`**` full-line comments (line-start only via `lineHasNonWhitespace`/`isAtLineStart`), `/*` rest-of-line comments, string/numeric literals, `\r\n` as one terminator. Multi-char operators are handled in an explicit block: `<>`, `<=`, `>=` are already recognized; `<` and `>` fall through to the single-char operator branch. `:` is already a `TokenPunctuation`. **There is no `<<`/`>>` recognition and no notion of "SQL context" — this is what Story 1 adds.** `consumeToEOL` is the helper for scanning to end-of-line; a multi-line opaque-span scanner is new.
- **Parser (`parser.go`).** Recursive-descent, error-recovering. `Parse()` is a top-level `switch` dispatching on statement keywords; `default` skips unrecognized tokens (partial parse). Reused helpers: `matches`/`matchesLiteral`, `advance`, `currentPos`/`prevPos`, `tokenRange`, `addDiagnostic`, `skipToNextStatement`, `consumeStringTarget`/`unquoteString`/`isTerminatedString`. **`skipToNextStatement` and `isStatementKeyword` share one authoritative stop-set** (documented in `skipToNextStatement`) — every new top-level SQL keyword must be added to `isStatementKeyword`, the `Parse()` dispatch switch, AND the `DEFINE DATA` break-set at `parser.go:109`, or recovery/data-section termination will drift. This three-place sync is a load-bearing constraint for every parser task below.
- **The keyword table already contains** `SELECT`, `FROM`, `WHERE`, `SET`, `INSERT`, `UPDATE`, `DELETE`, `STORE`, `LOOP`, `DATE`, `TIME` (see `isKeyword` in `lexer.go:331`). It does **not** contain `SINGLE`, `INTO`, `VALUES`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`, `RESULT`, `PROCESS`, `END-SELECT`, `END-RESULT`, or `VIEW`. Note the lexer lexes `END-SELECT` as a single hyphenated identifier token (hyphen-followed-by-body-char rule) — it is currently a `TokenIdentifier` with literal `"END-SELECT"`, not two tokens; parser tasks must match it as a literal, and it should be added to `isKeyword` for consistency with `ENDIF`.
- **AST (`ast.go`).** Every node embeds `StartPos`/`EndPos` (`model.Position`) and implements `Position()`. `Program` holds **separate typed slices per statement kind** (`Calls`, `Reads`, `Stores`, etc.) — there is no generic statement list and **no loop-body nesting anywhere** (even `ReadStatement` has no body; it is a flat node with just a `Target`). Modeling a **loop body** for `SELECT`/`READ RESULT SET` is therefore a genuinely new AST shape for this codebase, not an extension of an existing loop node. New SQL nodes follow the established pattern (embedded positions + `Position()` + a dedicated `Program` slice).
- **Analyzer wiring (`analyzer.go`).** `Analyze` builds `NewLexer`→`NewParser`→`Parse()`, sets `result.AST = ast`, and copies `ast.Diagnostics` into `result.Diagnostics`. **No wiring change is needed for this feature** — new nodes flow through `result.AST` and new diagnostics through the existing copy. `extractEdges(ast)` runs after; it iterates only the statement slices it knows and will simply not touch the new SQL slices (extraction is out of scope), so it is unaffected.
- **Fuzz (`fuzz_test.go`).** `FuzzParse` seeds from the `testdata/parser/01-08` fixtures plus hand-written edge cases and asserts non-nil `*Program`. New SQL fixtures and an unterminated-`<<` seed are added to the corpus (Task on Story 4).
- **Fixture harness (`parser_testdata_test.go`).** `TestParser_TestDataFixtures` **auto-discovers every `.nsp`** under `testdata/parser/` and asserts **zero diagnostics** except for a hard-coded exclusion `switch` (currently only `04-parser-parse-errors.nsp`). Any new malformed SQL fixture MUST be added to that exclusion switch or the suite fails. Valid SQL fixtures need no harness change — they are picked up automatically and must parse clean.

### Shared-contract impact

- **`internal/model` requires NO change** (confirms plan Story 5). New AST node types live in `internal/analysis/natural/ast.go` (behind the Analyzer seam), carried through the existing `FileAnalysis.AST interface{}` field. No new `EdgeKind`, no `model` type, no cache-format bump (edges are unchanged; extraction is deferred). The Analyzer interface signature is unchanged. **If any task discovers a `model` change is unavoidable, stop and escalate** — it would be a shared-contract change requiring migration tasks for `internal/workspace` and `internal/server` consumers, and the plan explicitly forecloses it.
- **Analyzer seam preserved.** No LSP-facing package (`server`, `document`, `workspace`) may type-assert the new node types; `seam_test.go` guards this. No task touches those packages.

### Divergences flagged

- **README/KB vs code on host-var colon:** the KB says the colon is *optional* in native SQL. In the code, a leading `:` currently lexes as standalone `TokenPunctuation ":"` followed by an identifier token — so a native clause operand `:NAME` is today **two tokens**. Story 1/Story 3 tasks must consume the optional `:` in the parser (recommended) rather than fusing it in the lexer, to avoid perturbing the existing `:` punctuation used elsewhere; this is called out in Task ES-2.
- **Loop-body gap:** `CLAUDE.md`/KB describe `SELECT` as "structurally like the existing `READ`-style loops," but the existing `ReadStatement` has **no body** — there is no prior loop-body node to mirror. Task ES-4 introduces the first body-bearing node; do not assume a template exists.

---

## Ordered task list

Order: lexer foundation → AST node types → parser (singleton statements first, then body-bearing loops, then the disambiguation and opaque-span cases) → diagnostics/recovery → integration & fuzz. Each task is one red → green → refactor slice run by `tdd-red` → `tdd-green` → `tdd-refactor`. All fixtures live under `internal/analysis/natural/testdata/parser/` and are permanent regression fixtures (M-5), sanitized non-proprietary Natural.

---

### ES-1 — Keyword table: add embedded-SQL keywords

**Story:** 1, 3 (NFR-15)
**Behavior:** Extend `isKeyword` (`lexer.go`) so the new SQL statement/clause words lex as `TokenKeyword`: `SINGLE`, `INTO`, `VALUES`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`, `PROCESS`, `RESULT`, and (for consistency with `ENDIF`) `END-SELECT` and `END-RESULT`. `SELECT`/`FROM`/`WHERE`/`SET`/`INSERT`/`UPDATE`/`DELETE`/`STORE`/`LOOP` are already present — do NOT re-add. This task changes tokenization only; no parser behavior yet.
**Fixtures:** none (unit test asserts token types directly, matching the style of `lexer_test.go`).
**Expected result:** `NewLexer("SELECT SINGLE INTO VALUES MERGE COMMIT ROLLBACK CALLDBPROC PROCESS RESULT END-SELECT END-RESULT")` yields each as `TokenKeyword` with the uppercased literal. Existing keyword tests still pass.
**Reuses/migrates:** `isKeyword` switch only. No consumer migration.
**Modeled gaps:** n/a (foundation).
**DoD:**
- [ ] Table-driven lexer test asserts each new word → `TokenKeyword`.
- [ ] `END-SELECT`/`END-RESULT` lex as a single keyword token (hyphen rule) — assert the literal is the whole word.
- [ ] Existing `lexer_test.go` and full package tests green; `gofmt`/`go vet` clean.
- [ ] No `internal/model` or seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** none.

---

### ES-2 — Lexer: `<<`/`>>` delimiters + opaque multi-line span, SQL-context-gated

**Story:** 1 (NFR-15, M-6)
**Behavior:** Teach the lexer to recognize `<<` and `>>` and to capture the region between them as a **single opaque token** (a raw text span) that:
- may span multiple physical lines with no continuation char;
- may contain `*`/`**`/`/*` comment text (NOT re-lexed as comments — pass-through);
- is **not** tokenized or scanned inside (no host-var scan, no keyword recognition).
Introduce a new `TokenType` (e.g. `TokenSQLOpaque`) whose `Literal` is the raw interior text and whose `Line`/`Column` mark the start; the closing `>>` position is recoverable for range construction (see AST task). **SQL-context gating:** `<<`/`>>` carry delimiter meaning ONLY when the lexer/parser is in SQL context (inside a `PROCESS SQL`). Recommended design: the lexer exposes a scan-opaque-span entry point the parser calls after it has consumed `PROCESS SQL ddm-name` (parser-driven), rather than the lexer self-detecting context — decide in `tdd-green`. Outside SQL context, `<` and `>` must lex **exactly as today** (single-char operators; `<>`/`<=`/`>=` unchanged).
**Fixtures:** none for the lexer unit test (assert tokens directly); the multi-line behavior is also covered end-to-end by ES-9's `PROCESS SQL` fixture.
**Expected result:**
- A bare `A < B` / `A > B` / `A <> B` still lexes as today (regression assertion — this is the AC that non-SQL `<`/`>` is unchanged).
- The parser, after `PROCESS SQL DDMNAME`, obtains one opaque token whose literal is the exact interior text of `<< … >>` including embedded newlines and comment characters, with a start position at the char after `<<` (or at `<<` — pin the convention in the test) and a recoverable end at `>>`.
- An unterminated `<<` with no closing `>>` before EOF returns the interior-to-EOF as the opaque literal **and signals unterminated** (a flag/sentinel the parser turns into a diagnostic in ES-8) — it must NOT loop forever or panic.
**Reuses/migrates:** the multi-char-operator block in `NextToken`; `consumeToEOL` as a reference for line scanning (the span scanner is new and multi-line). No consumer migration.
**Modeled gaps:** opaque span is **retained, never dropped** (plan "Opaque ≠ dropped"); unterminated span is a diagnostic, not a silent consume (wired in ES-8).
**DoD:**
- [ ] Table-driven test: non-SQL `<`/`>`/`<>`/`<=`/`>=` tokenization is byte-for-byte unchanged (explicit regression case).
- [ ] Test: opaque span captures multi-line interior verbatim incl. newlines and `/*`/`*` characters, no inner tokenization.
- [ ] Test: unterminated `<<` terminates the scan at EOF and reports unterminated (no panic, no infinite loop).
- [ ] `FuzzParse` still green (extended in ES-10).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-1.

---

### ES-3 — AST: singleton SQL statement nodes

**Story:** 2 (NFR-15)
**Behavior:** Add AST node types (in `ast.go`) for the **non-loop** SQL statements, each embedding `StartPos`/`EndPos` and implementing `Position()`, each with a dedicated `Program` slice (mirroring `Calls`/`Reads`): `SelectSingleStatement`, `InsertStatement`, `SQLUpdateStatement`, `SQLDeleteStatement`, `MergeStatement`, `CommitStatement`, `RollbackStatement`, `CallDBProcStatement`, `ProcessSQLStatement`. For `Select*`/`Insert`/`SQLUpdate`/`SQLDelete`, expose operands **structurally, unbound**: selected columns, `INTO` target host-var names, `FROM`/table operand(s), and clause host-var operands (e.g. `WHERE`) — as name+range lists, NO binding. `ProcessSQLStatement` exposes only `DDMName` (+ range) and `Body` as a raw-text span with a `model.Range` — and nothing else (no host-var list). Naming (`SQLUpdateStatement` vs `UpdateSQLStatement`) is a `tdd-green` decision; keep it distinct from the Adabas `UpdateStatement`/`DeleteStatement` names.
**Fixtures:** none (AST-shape unit tests construct nodes and assert fields, mirroring `ast_test.go`).
**Expected result:** each node type exists, embeds positions, implements `Node`, and appears as a `Program` slice; `ProcessSQLStatement.Body` is a raw string + range with no parsed sub-structure.
**Reuses/migrates:** the AST node pattern from `ast.go` (embedded positions + `Position()`). Add the new slices to `Program`. No consumer migration (`internal/model` unchanged; `extractEdges` ignores unknown slices).
**Modeled gaps:** the `ProcessSQL` body being a raw span with range (not a parsed host-var list) IS the modeled-gap representation for Option B — assert it structurally.
**DoD:**
- [ ] `ast_test.go`-style tests assert each node implements `Node` and round-trips its fields/positions.
- [ ] `ProcessSQLStatement` carries `DDMName` + `Body` (raw text + `model.Range`) and no host-var collection.
- [ ] Loop nodes are NOT added here (they are ES-4).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** none (can run parallel to ES-1/ES-2).

---

### ES-4 — AST: body-bearing loop nodes (`SELECT`, `READ RESULT SET`)

**Story:** 2 (NFR-15)
**Behavior:** Add `SelectStatement` (cursor loop) and `ReadResultSetStatement` (result-set loop) node types. Each embeds positions, implements `Position()`, gets a `Program` slice, and — new for this codebase — carries a **loop `Body`**. Decide the body representation in `tdd-green`: because `Program` uses per-kind slices (no generic statement interface), the simplest faithful body is a slice of child statements typed via a small statement-node interface or a struct of the same per-kind slices; pick the lightest option that lets downstream traversal find nested statements. `SelectStatement` also exposes the same unbound operand lists as ES-3's `SelectSingleStatement` (columns, `INTO`, `FROM`, `WHERE` host-vars). `ReadResultSetStatement` exposes the result-set operand it reads.
**Fixtures:** none (AST-shape unit tests).
**Expected result:** `SelectStatement`/`ReadResultSetStatement` exist with a populated `Body` field; `SelectSingleStatement` (from ES-3) has NO body — assert the distinction.
**Reuses/migrates:** AST pattern from `ast.go`. **This is the first loop-body node** — note in the task that no existing node to mirror; introduce a minimal statement-node abstraction only if needed.
**Modeled gaps:** n/a (structural).
**DoD:**
- [ ] Tests assert `Body` nesting exists and holds child statements; `SelectSingleStatement` has none.
- [ ] Both nodes implement `Node` and carry whole-statement positions.
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change; seam_test.go still green.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-3 (shares operand-list types).

---

### ES-5 — Parser: transaction & call statements (`COMMIT`, `ROLLBACK`, `CALLDBPROC`)

**Story:** 3 (FR-30, M-6)
**Behavior:** Parse the three simplest, unambiguous SQL statements into their ES-3 nodes. Add `COMMIT`/`ROLLBACK`/`CALLDBPROC` to the `Parse()` dispatch switch, to `isStatementKeyword`, and to the `DEFINE DATA` break-set (three-place sync). `COMMIT`/`ROLLBACK` take no operands; `CALLDBPROC` consumes its proc-name operand + remaining operands via `skipToNextStatement`. Positions span the whole statement.
**Fixtures:** `09-sql-txn-calldbproc.nsp` — a `DEFINE DATA`/`END-DEFINE`, then `COMMIT`, `ROLLBACK`, and a `CALLDBPROC` line, then `END` (auto-picked-up as a valid fixture → must parse with zero diagnostics).
**Expected result:** one `CommitStatement`, one `RollbackStatement`, one `CallDBProcStatement` on `Program`, correct positions; no diagnostics.
**Reuses/migrates:** `skipToNextStatement`, `currentPos`/`prevPos`, dispatch switch. Add keywords to the three sync points.
**Modeled gaps:** n/a.
**DoD:**
- [ ] Fixture parses to the three nodes with correct positions; zero diagnostics.
- [ ] `isStatementKeyword` + dispatch + DEFINE-DATA break-set all updated (verify recovery still stops correctly).
- [ ] `TestParser_TestDataFixtures` auto-covers the new valid fixture (zero-diagnostics).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-1, ES-3.

---

### ES-6 — Parser: `SELECT SINGLE` and `SELECT … END-SELECT`/`LOOP` cursor loop

**Story:** 3 (FR-30, M-6)
**Behavior:** Parse `SELECT`. Distinguish `SELECT SINGLE` (→ `SelectSingleStatement`, **no** body/terminator) from the cursor form `SELECT … END-SELECT` (→ `SelectStatement` with a loop body). **Accept BOTH terminators:** `END-SELECT` (structured) AND `LOOP` (reporting) close the cursor loop. Parse `INTO` on the cursor form (Natural allows it). Capture columns, `INTO` targets, `FROM` table operand(s), and `WHERE` host-var operands as unbound name+range lists (ES-3/ES-4 fields). Host-var operands accepted **with or without** a leading colon — consume an optional `:` punctuation token before the name in the parser (per divergence note). Do NOT deep-parse clause grammar (`GROUP BY`/`HAVING`/`ORDER BY`) — capture operands, skip the rest to the terminator or next statement. Loop body: recursively parse intervening statements into `Body` until the terminator.
**Fixtures:**
- `10-sql-select-loop.nsp` — structured-mode: `SELECT cols INTO #a,#b FROM DDMNAME WHERE col = #k` … a body statement (e.g. a `PERFORM` or `CALLNAT`) … `END-SELECT`; both colon-less and (optionally) colon host-vars represented.
- `11-sql-select-single.nsp` — `SELECT SINGLE cols INTO #a FROM DDMNAME WHERE …` with no terminator.
- `12-sql-select-loop-reporting.nsp` — same shape as fixture 10 but closed with `LOOP` (reporting mode).
**Expected result:** fixture 10 → one `SelectStatement` with body child(ren), populated operand lists, positions spanning `SELECT`…`END-SELECT`; fixture 12 identical but closed by `LOOP`; fixture 11 → one `SelectSingleStatement`, no body. All three parse with zero diagnostics.
**Reuses/migrates:** dispatch switch + three-place sync for the loop terminators; `matchesLiteral` for `SINGLE`/`INTO`/`FROM`/`WHERE`/`END-SELECT`/`LOOP`; `skipToNextStatement` for intra-clause skip. Loop-body parse recurses into the same statement dispatch used by `Parse()` (refactor the dispatch into a reusable `parseStatement` helper in `tdd-refactor` if the loop needs it).
**Modeled gaps:** colon-optional host-var handling (accept both forms) is the modeled behavior — assert both parse. The reporting-mode `LOOP` acceptance covers the mode-dependent terminator AC.
**DoD:**
- [ ] Three fixtures each parse to the expected node(s) with correct positions and body nesting; zero diagnostics.
- [ ] Both `END-SELECT` and `LOOP` close the cursor loop (fixtures 10 & 12).
- [ ] Host-vars parse with and without leading `:` (assert both forms in fixture 10).
- [ ] `INTO` captured on the cursor form.
- [ ] `TestParser_TestDataFixtures` auto-covers all three (zero-diagnostics).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-1, ES-4.

---

### ES-7 — Parser: `READ RESULT SET … END-RESULT`/`LOOP`

**Story:** 3 (FR-30, M-6)
**Behavior:** Parse `READ RESULT SET` into `ReadResultSetStatement` with a loop body. Because `READ` already has a parser path (`parseReadStatement`), this task must **branch inside the READ parse** (or before it) on the `RESULT SET` continuation and route to the SQL result-set node instead of the Adabas `ReadStatement`. **Accept BOTH terminators:** `END-RESULT` and `LOOP`. Capture the result-set operand it reads; parse the loop body recursively. Ensure the existing Adabas `READ view` path (fixture `06-read-store.nsp`) is unaffected — a plain `READ` with no `RESULT SET` still produces a `ReadStatement`.
**Fixtures:**
- `13-sql-read-result-set.nsp` — a `CALLDBPROC` then `READ RESULT SET … END-RESULT` with a body statement.
- `14-sql-read-result-set-loop.nsp` — same, closed with `LOOP`.
**Expected result:** each fixture → one `ReadResultSetStatement` with body; plain `READ` regressions unchanged; zero diagnostics.
**Reuses/migrates:** `parseReadStatement` (branch on `RESULT SET`); the loop-body parse helper from ES-6; the loop-terminator handling. `END-RESULT`/`LOOP` recovery sync.
**Carried-over follow-up from ES-6 (do in this task):** the ES-6 loop-body helper `parseStatement` is a PARTIAL dispatch — it handles only CALLNAT/PERFORM and re-implements them instead of sharing `Parse()`'s switch. As the second loop-body consumer, ES-7 must **unify loop-body dispatch with the top-level dispatch** so a loop body parses the full statement set and the two paths cannot drift (recommended: make the per-statement parsers return a `Node` that `Parse()` appends by type). Add a body fixture nesting a non-CALLNAT/PERFORM statement to prove the unification.
**Modeled gaps:** mode-dependent terminator (both accepted).
**DoD:**
- [ ] Both fixtures parse to `ReadResultSetStatement` with body; both terminators accepted.
- [ ] Plain-`READ` regression (`06-read-store.nsp`) still produces `ReadStatement`, unchanged.
- [ ] `TestParser_TestDataFixtures` auto-covers both new fixtures (zero-diagnostics).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-4, ES-6 (body-parse helper).

---

### ES-8 — Parser: `PROCESS SQL ddm-name <<…>>` with opaque body

**Story:** 3 (FR-30, M-6), Story 1 (opaque span)
**Behavior:** Parse `PROCESS SQL`. Add `PROCESS` to dispatch + three-place sync; after `PROCESS` require the `SQL` literal, then the `ddm-name` operand (identifier), then invoke the ES-2 opaque-span scan to capture the `<<…>>` body as the raw span (text + range) into `ProcessSQLStatement.Body`. The parser does **not** interpret the body interior (no host-var extraction — Option B). Set the statement range from `PROCESS` to the closing `>>`.
**Fixtures:** `15-sql-process-sql.nsp` — the KB minimal fixture body: `PROCESS SQL DDMNAME << UPDATE … SET … WHERE PERS_ID = :#PERS-ID >>` spanning multiple lines, with a `:#PERS-ID` inside the body that MUST remain uninterpreted (opaque). Include a trailing `CALLNAT 'NDBERR' …` and `END` to prove surrounding statements still parse.
**Expected result:** one `ProcessSQLStatement` with `DDMName == "DDMNAME"`, `Body` == the verbatim interior text (multi-line, incl. the `:#PERS-ID` text) with a range covering `<<`…`>>`, and NO host-var list. The `CALLNAT 'NDBERR'` still becomes a `CallStatement`. Zero diagnostics for the well-formed case.
**Reuses/migrates:** ES-2 opaque-span scanner; `skipToNextStatement`; dispatch + three-place sync for `PROCESS`.
**Modeled gaps:** body is raw opaque span, retained not dropped; interior `:#PERS-ID` NOT scanned (assert the body is verbatim text and no host-var collection exists).
**DoD:**
- [ ] Fixture parses to `ProcessSQLStatement` with correct `DDMName`, verbatim multi-line `Body`, and range covering the delimiters.
- [ ] The interior `:host-var` is present in `Body` text and NOT parsed into any structured field.
- [ ] Trailing `CALLNAT`/`END` parse normally (no gap).
- [ ] `TestParser_TestDataFixtures` auto-covers the valid fixture (zero-diagnostics).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-1, ES-2, ES-3.

---

### ES-9 — Parser: `INSERT`/`UPDATE`/`DELETE`/`MERGE` and SQL-vs-Adabas disambiguation

**Story:** 3 (FR-30, M-6)
**Behavior:** Parse SQL-form `INSERT` (→ `InsertStatement`, `INTO` table + `VALUES`/host-vars), `MERGE` (→ `MergeStatement`, table operand; do NOT model merge internals), and — the sensitive part — **disambiguate the shared keywords `UPDATE`/`DELETE` (and `STORE`)** between their SQL form (`SET …`/`WHERE …`/table operand) and their existing Adabas form (operating on a `READ`/`FIND` loop record). Choose the SQL node (`SQLUpdateStatement`/`SQLDeleteStatement`) only when the clause shape is SQL (presence of `SET`/`WHERE`/a table operand rather than a bare view record). `STORE` currently parses to `StoreStatement` (Adabas) — leave that path intact; only route to a SQL node on an unambiguous SQL shape (see Open questions — default-to-Adabas when genuinely ambiguous unless the reviewer decides otherwise). This is a lookahead/shape-check task: implement the minimal disambiguation heuristic and cover the concrete shapes with fixtures; ambiguous residual shapes are recorded as an open question, not silently guessed.
**Fixtures:**
- `16-sql-insert.nsp` — `INSERT INTO DDMNAME (col,col) VALUES (:#a,:#b)` (or bare host-vars); zero diagnostics.
- `17-sql-update-delete.nsp` — SQL `UPDATE DDMNAME SET col = :#a WHERE …` and SQL `DELETE FROM DDMNAME WHERE …`; zero diagnostics.
- `18-sql-merge.nsp` — a minimal `MERGE` statement; zero diagnostics.
- Reuse `06-read-store.nsp` as the Adabas-form regression (its `STORE`/`READ` must stay Adabas nodes).
**Expected result:** fixtures 16–18 produce `InsertStatement`/`SQLUpdateStatement`/`SQLDeleteStatement`/`MergeStatement` with table + operand lists; `06-read-store.nsp` unchanged (still `StoreStatement`/`ReadStatement`). Zero diagnostics for all valid fixtures.
**Reuses/migrates:** existing `INSERT`/`UPDATE`/`DELETE` keywords (already in table); dispatch + three-place sync; `parseStoreStatement`/`parseReadStatement` remain the Adabas paths. The disambiguation may need a small bounded lookahead helper — add it near `matches`.
**Modeled gaps:** disambiguation choice is explicit (SQL shape → SQL node); the ambiguous-residual case is surfaced as an open question, never a silent pick.
**DoD:**
- [ ] Fixtures 16–18 parse to the correct SQL nodes; `06-read-store.nsp` stays Adabas (regression assertion).
- [ ] Disambiguation heuristic documented in code; ambiguous default behavior matches the resolved open question.
- [ ] `TestParser_TestDataFixtures` auto-covers the valid fixtures (zero-diagnostics).
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-1, ES-3, ES-6 (`FROM`/operand helpers).

---

### ES-10 — Diagnostics & error recovery for malformed embedded SQL

**Story:** 4 (FR-30, FR-43, M-6)
**Behavior:** Emit ranged diagnostics on `Program.Diagnostics` for malformed SQL and prove surrounding statements survive (M-6), consistent with the existing `addDiagnostic` contract:
- a `SELECT` loop with no terminator before EOF → diagnostic, surrounding statements retained;
- an unterminated `<<` region (no closing `>>` before EOF) → diagnostic (from the ES-2 unterminated signal), does not consume the rest of the file silently and does not panic;
- (optional) a `PROCESS SQL` missing the `<<` body → diagnostic.
Add these SQL seeds to `FuzzParse`'s corpus, including the fixtures below and a hand-written unterminated-`<<` byte seed; assert the parser still never panics / always returns non-nil `*Program`.
**Fixtures:**
- `19-sql-parse-errors.nsp` — an unterminated `SELECT` (no `END-SELECT`/`LOOP`) AND/OR an unterminated `PROCESS SQL <<`, followed by a valid `CALLNAT 'PROG'` / `END` proving retention. **This fixture is malformed → add it to the `TestParser_TestDataFixtures` exclusion `switch`** (alongside `04-parser-parse-errors.nsp`).
**Expected result:** the malformed statement yields a ranged diagnostic with a sensible span; the trailing valid `CALLNAT` is still parsed (no silent gap); `FuzzParse` green with the new seeds.
**Reuses/migrates:** `addDiagnostic`, `skipToNextStatement`, the ES-2 unterminated-span signal; the `TestParser_TestDataFixtures` exclusion switch (must be updated); `fuzz_test.go` corpus.
**Modeled gaps:** unterminated span/loop → diagnostic (never silent/never panic) is the FR-30/FR-43 contract; assert both the diagnostic and the retained surrounding statement.
**DoD:**
- [ ] Unterminated `SELECT` → diagnostic + surrounding `CALLNAT` retained.
- [ ] Unterminated `<<` → diagnostic, no infinite loop, no panic, remainder not silently swallowed.
- [ ] `19-sql-parse-errors.nsp` added to the exclusion switch; suite green.
- [ ] `FuzzParse` corpus extended (SQL fixtures + unterminated-`<<` seed); fuzz target green.
- [ ] `gofmt`/`go vet` clean; no `internal/model`/seam change.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-2, ES-6, ES-8.

---

### ES-11 — Analyzer integration & end-to-end fixture assertions

**Story:** 5 (NFR-15, M-5)
**Behavior:** Prove the SQL nodes and diagnostics flow through `Analyzer.Analyze(path, content)` unchanged-signature (they should, via the existing `result.AST = ast` + diagnostics copy — confirm no wiring change is needed and add a regression test if a gap surfaces). Add analyzer-level tests (mirroring `TestAnalyzer_ASTPopulation` / `TestAnalyzer_DiagnosticsForParseErrors` in `parser_diagnostics_test.go`) asserting that analyzing a SQL fixture yields a `FileAnalysis` whose `AST` contains the SQL nodes and whose `Diagnostics` include SQL syntax errors for the malformed fixture. Confirm `extractEdges` is unaffected (no new edges — extraction deferred) and `seam_test.go` still passes.
**Fixtures:** reuse `10-sql-select-loop.nsp`, `15-sql-process-sql.nsp`, `19-sql-parse-errors.nsp`.
**Expected result:** `Analyze` on the valid fixtures returns `FileAnalysis.AST` containing the SQL nodes (type-asserted within the `natural` package, which is allowed — the seam only forbids LSP-facing packages from doing so); `Analyze` on `19-sql-parse-errors.nsp` returns non-empty `Diagnostics`; `FileAnalysis.Edges` gains no new SQL edges (deferred).
**Reuses/migrates:** `analyzer.go` (verify, no change expected); `parser_diagnostics_test.go` patterns; `seam_test.go` (unchanged, must stay green).
**Modeled gaps:** confirms deferral — no edges emitted for SQL constructs in this feature.
**DoD:**
- [ ] Analyzer test asserts SQL nodes present in `FileAnalysis.AST` for valid fixtures.
- [ ] Analyzer test asserts SQL diagnostics present for the malformed fixture.
- [ ] `FileAnalysis.Edges` unchanged for SQL constructs (no extraction).
- [ ] `seam_test.go` and full package suite green; `internal/model` unchanged.
- [ ] `gofmt`/`go vet` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
**Depends on:** ES-5..ES-10.

---

## Reviews required

Run via `/review-feature`:

- **`review-robustness`** — the feature widens the parser with new input (opaque multi-line spans, new loop bodies, disambiguation lookahead). Focus: unterminated `<<`/loops never panic or loop forever; malformed shapes degrade to diagnostics (FR-30/FR-43); `FuzzParse` corpus covers the new surface.
- **`review-seam`** — confirm no `internal/model` change and no LSP-facing package type-asserts the new AST nodes (NFR-15). *(Included even though no contract change is planned, to certify the seam held.)*
- **`review-docs`** — feature changes parser capability; the `CLAUDE.md` "Project state" note and `README.md` "Parser-based extraction" section must gain the embedded-SQL constructs at `/finalize-feature`.
- **Protocol conformance / concurrency / performance** — **not required** (no LSP method added, no indexer/watcher change, not in the indexing hot path beyond normal parse).

---

## Open questions

Carried forward from the plan; none block starting ES-1..ES-4, but ES-6/ES-9/ES-10 need decisions before their `tdd-green`:

1. **Reserved-word colon enforcement.** The KB says the colon is *mandatory* in native SQL when a host-var name equals an SQL reserved word (`:DATE`, `:USER`). Should the parser flag a bare reserved-word host-var (a diagnostic), or accept both forms and defer reserved-word awareness to the extraction feature? *(Planner recommendation: accept both, defer — enforcement needs a reserved-word set that belongs with binding. Affects ES-6.)*
2. **Depth of SQL-clause parsing.** How much of `WHERE`/`GROUP BY`/`HAVING`/`ORDER BY` should be structured now vs captured as an operand list + skipped span? *(Planner recommendation, per plan's lean: capture table + host-var operands, do NOT model clause grammar. Affects ES-6/ES-9.)*
3. **UPDATE/DELETE/STORE disambiguation default.** Is clause-shape disambiguation (SQL `SET`/`WHERE`/table vs Adabas record form) sufficient for all common cases, and which form does the parser default to when the shape is genuinely ambiguous? *(Planner recommendation: default to the existing Adabas node when ambiguous, since that path already exists and this feature must not regress it; record any residual ambiguous shape. Blocks ES-9 `tdd-green`.)*
4. **Reporting-mode scope.** This feature adds `LOOP` as an SQL-loop terminator only. Is accepting `LOOP` for SQL loops in isolation sufficient, or does it pull in broader reporting-mode handling that should be its own feature? *(Planner recommendation: accept `LOOP` for SQL loops in isolation; broader reporting mode is out of scope. Affects ES-6/ES-7.)*

**Resolved during planning (do not re-open):** feature is lex+parse+AST+diagnostics only (extraction/binding deferred to `08b`); the `<<…>>` body is a single raw opaque span, never scanned inside (Option B); both `END-SELECT`/`LOOP` and both `END-RESULT`/`LOOP` terminate their loops; `internal/model` is not changed.
