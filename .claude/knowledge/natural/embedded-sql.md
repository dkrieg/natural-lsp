# Embedded SQL

How Software AG Natural embeds SQL, and what the analyzer must know to lex/parse it (or treat parts as
opaque). This is groundwork for a future data-access / SQL extraction feature — no parser code touches
it yet.

Dialect/mode: facts verified against Natural for Mainframes (nat827mf/nat911mf) and Natural for
Linux/Unix/Windows + NaturalONE (nat838unx/nat841unx/natONE). SQL statements exist in BOTH structured
and reporting mode; the loop terminator differs by mode (see SELECT below).

**Status: verified (2026-06-30)** — statement list, `PROCESS SQL` / flexible-SQL `<<...>>` delimiters,
colon host-variable rule (now fully confirmed, see below), SELECT loop semantics, backends, the
`FROM`-table-is-a-DDM binding, and error-handling model all confirmed against official Software AG
documentation. No remaining needs-verification items in this topic.

## The big picture — TWO embedding styles, not one

Natural does **not** use the host-language `EXEC SQL ... END-EXEC` block construct of COBOL/PL/I/C
precompilers. Instead it has its own integrated SQL, in two forms:

1. **Native Natural SQL statements** — SQL DML written as first-class Natural statements
   (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`,
   `READ RESULT SET`). These look like SQL but follow Natural statement conventions (no trailing
   semicolon, Natural continuation, `END-SELECT`/`LOOP` loop closer). The Natural compiler parses these.
2. **`PROCESS SQL` + "flexible SQL"** — an escape hatch carrying a raw SQL string that the Natural
   compiler does **not** parse; it is copied through to the database. Delimited by `<<` and `>>`.

A static analyzer must handle both: parse the native statements (they reference host variables and DDM
table names the analyzer may want as edges/symbols), and treat flexible-SQL `<<...>>` regions as
**largely opaque** text (scanning only for `:host-variable` references inside).

**This is an ADD-ON / DBMS-interface feature, not the Adabas core language.** Native SQL + `PROCESS SQL`
are provided by the SQL database interfaces — **Natural for DB2**, **Natural SQL Gateway** (a.k.a.
Adabas SQL Gateway), and historically **Natural for SQL/DS**. Adabas itself is accessed by the *other*
DML statements (`READ`, `FIND`, `HISTOGRAM`, `GET`, `STORE`, `UPDATE`-by-ISN, `DELETE`-by-ISN), which
go against **DDMs**, not SQL tables. Do not conflate the two. The same Natural keywords `UPDATE` /
`DELETE` / `STORE` exist in both worlds; SQL vs Adabas form is distinguished by syntax (SQL forms use
`SET`, `WHERE`, table names; Adabas forms operate on a record held by a `READ`/`FIND` loop).

## Statement list — verified

Native Natural SQL statements (Natural for DB2 / SQL Gateway / SQL/DS):

| Statement | Purpose | Loop? | Analyzer interest |
|-----------|---------|-------|-------------------|
| `SELECT … END-SELECT` | cursor-oriented query (DB loop) | yes | reads table(s); INTO target host vars; WHERE host vars |
| `SELECT SINGLE` | non-cursor singleton query | no | same, no loop body |
| `INSERT` | add row(s) | no | writes table; VALUES/`:host` |
| `UPDATE` (SQL form, with `SET`/`WHERE`) | modify rows | no | writes table; `:host` |
| `DELETE` (SQL form, with `WHERE`) | remove rows | no | writes table |
| `MERGE` | upsert | no | writes table |
| `PROCESS SQL ddm-name <<text>>` | arbitrary SQL passed through | no | opaque text + `:host` refs |
| `COMMIT` / `ROLLBACK` | transaction control | no | (transaction edges, low priority) |
| `CALLDBPROC` | call a stored procedure | no | call-like edge to a DB procedure |
| `READ RESULT SET … END-RESULT` | read a stored-proc result set | yes | only valid after `CALLDBPROC` |

Notes:
- `READ RESULT SET` is **only** valid in conjunction with a preceding `CALLDBPROC` (DB2 / SQL Gateway
  only).
- Positioned `UPDATE`/`DELETE` use `WHERE CURRENT OF cursor` semantics inside a `SELECT … END-SELECT`
  loop (update/delete the row the cursor sits on).

## SELECT as a Natural database loop — verified

`SELECT … END-SELECT` is a **database loop**, structurally like `READ … END-READ` / `FIND … END-FIND`
(Natural manages the cursor automatically — no explicit DECLARE/OPEN/FETCH/CLOSE in native form).
Skeleton:
```
SELECT  col1, col2, …
  INTO  host-var1, host-var2, …
  FROM  table-expression
  [WHERE …] [GROUP BY …] [HAVING …] [ORDER BY …]
  … loop-body statements …
END-SELECT          /* structured mode */
```
- **Mode-dependent terminator (decision-critical for the parser):** in **structured mode** the loop
  ends with `END-SELECT`; in **reporting mode** it ends with `LOOP` (same pattern as other DB loops).
- `SELECT SINGLE` retrieves at most one row and has **no** loop body / no `END-SELECT`.
- Unusual vs ISO SQL: Natural puts `INTO` on **cursor-oriented** selects too (ISO SQL only allows
  `INTO` on singleton selects). So `SELECT … INTO … FROM …` is normal and the `INTO` operands are the
  target host variables.

## Host variables — the colon rule (decision-critical)

Natural/host variables embedded in SQL are referenced with a **colon prefix** `:` — same convention as
classic embedded SQL. The variables are ordinary Natural fields declared in `DEFINE DATA`
(usually `LOCAL`). Optional qualifiers seen in `PROCESS SQL` / flexible SQL:

- `:host-variable` — plain host variable (USING/input by default).
- `:U:host-variable` — explicit **USING** (value passed *to* the database). Default.
- `:G:host-variable` — **GIVING** (value received *from* the database).
- `:T:host-variable` — **text** variable (the variable's *contents* are spliced into the SQL string as
  literal SQL text, not bound as a parameter).
- `INDICATOR :host-variable` / `LINDICATOR :host-variable` — null / LOB-length indicator variables.
- Array host variables appear with Natural index notation, e.g. `:NAME(*)`, `:SALARY(01:10)`,
  `:DATE(1:10)` — used with the array/bulk `FOR :n ROWS` clause (requires the `DB2ARRY` compiler
  option) to insert/select many rows in one statement.

**Native-vs-flexible colon rule — VERIFIED (2026-06-30).** Confirmed verbatim against the official
Natural SQL "Basic Syntactical Items" reference (`sm/sql-bsi.htm`):
- A `host-variable` is "a Natural user-defined variable (no system variable) which is referenced in an
  SQL statement. It can be either an individual field or defined as part of a Natural view."
- **In native Natural SQL the colon is OPTIONAL:** *"To comply with SQL standards, a `host-variable`
  can also be prefixed by a colon (:)."* The word **"can also"** is decisive — the bare Natural name is
  the normal native form, and the colon form is merely *also* accepted. So `SELECT NAME, ACCOUNT INTO
  #NAME, #MONEY FROM …` (bare `#`-prefixed names, no colons) is the idiomatic native form; the example
  programs in the SELECT (SQL) reference uniformly write host vars WITHOUT colons.
- **Reserved-word exception (decision-critical for the lexer):** *"The colon is always required if the
  variable name is identical to an SQL reserved word."* So a host var literally named e.g. `:DATE` /
  `:USER` MUST carry the colon even in native SQL. A lexer/binder must therefore accept a host-var
  reference in native SQL *with or without* the leading colon.
- **Flexible SQL still requires the colon:** *"When used with flexible SQL, `host-variables` must be
  qualified by colons."* (The compiler can't otherwise distinguish a Natural field from an SQL
  identifier inside the opaque `<<…>>` text.)

Net rule for the analyzer: inside `<<…>>` / `PROCESS SQL`, scan for `:host-var` (colon mandatory);
inside native Natural SQL clauses (`INTO`/`WHERE`/`VALUES`/`SET`), a host-var operand may be a bare
Natural name OR `:name` — bind either form back to its `DEFINE DATA` field.

### Interaction with DEFINE DATA
Host variables must be declared in `DEFINE DATA` with a Natural format that maps to the column type:

| Natural format | DB2/SQL type |
|----------------|--------------|
| `A`n | CHAR(n) |
| `N`nn.m / `P`nn.m | NUMERIC |
| `I2` / `I4` | SMALLINT / INT |
| `F4` / `F8` | REAL / DOUBLE PRECISION |
| `D` / `T` | DATE / TIME |

So a SQL host-variable reference is a **read/write of a `DEFINE DATA` field** — the analyzer can bind
`:FIELD` (or the bare name in native SQL) back to its declaration like any other variable use.

## Lexer / parser implications (the load-bearing part)

- **No `EXEC SQL … END-EXEC`.** Do not look for that block construct. Native SQL = ordinary Natural
  statements; the escape hatch = `PROCESS SQL` / `<<…>>`.
- **No trailing semicolon.** Native SQL statements obey Natural statement termination, not SQL `;`.
- **`<<` and `>>` are flexible-SQL delimiters** — treat the span between them as an opaque SQL string.
  The *only* things to extract inside are colon-prefixed host-variable references; everything else is
  pass-through text the Natural compiler itself ignores ("not recognized by the Natural compiler …
  copied into the SQL string … syntax errors detected at runtime"). A naive lexer must NOT try to tokenize
  the inner SQL as Natural. NOTE: `<<`/`>>` only carry this meaning in SQL context (PROCESS SQL /
  flexible SQL inside an SQL statement); `<` and `>` are otherwise Natural comparison operators — the
  parser must be in SQL context before treating `<<`/`>>` as delimiters.
- **Flexible-SQL string can span multiple physical lines with NO continuation character**, and may
  contain comments (end-of-line and full-line). So the opaque region is multi-line by nature.
- **Mode-sensitive loop close:** `END-SELECT` (structured) vs `LOOP` (reporting). Same for
  `READ RESULT SET … END-RESULT` vs `LOOP`.
- **`PROCESS SQL ddm-name <<…>>`** names a DDM as its first operand — that `ddm-name` resolves to a
  `.NSD` DDM (separate namespace, like other DDM refs), useful as a read/write edge target even though
  the SQL body is opaque.
- **Native-SQL `FROM`/`INTO`/`UPDATE`/`DELETE` table name resolves to a `.NSD` DDM** (verified — see
  "File extensions / object types" below). Bind it in the DDM namespace, same as Adabas `READ`/`FIND`.
- **Host-var colon is OPTIONAL in native SQL, MANDATORY in flexible SQL** (verified — see "Host
  variables" above). The binder must accept a native-SQL host var with OR without the leading colon, and
  must require the colon for `:host-var` refs found inside `<<…>>`.
- **Same-keyword ambiguity:** `UPDATE` / `DELETE` / `STORE` exist as both Adabas (DDM-record) and SQL
  (table) statements. Disambiguate by clause shape (`SET … WHERE …` / table name ⇒ SQL; operating on a
  `READ`/`FIND` loop record ⇒ Adabas).

## Cursors / WHENEVER / error handling — verified

- **No application-managed cursors in native form.** Native `SELECT … END-SELECT` hides
  DECLARE/OPEN/FETCH/CLOSE — Natural generates and manages the cursor automatically (one of its main
  selling points vs raw embedded SQL). (Explicit DB2 cursor verbs like DECLARE CURSOR / OPEN / CLOSE
  belong to raw DB2 embedded SQL, not to Natural's native SQL surface; if they appear at all it's
  inside flexible-SQL text.)
- **No SQL `WHENEVER` construct.** Natural does **not** use `WHENEVER … GOTO/CONTINUE`. SQL errors flow
  through **Natural's normal error handling**: the `ON ERROR` statement, or default Natural error
  processing. Two supplied subprograms toggle/inspect it:
  - `NDBNOERR` — suppress Natural's automatic SQL error handling for the next SQL statement.
  - `NDBERR` — after the statement, return `SQLCODE` (I4), `SQLSTATE` (A5), `SQLCA` (A136) for
    programmatic decisions. (So `CALLNAT 'NDBNOERR'` / `CALLNAT 'NDBERR'` are the idiomatic
    error-handling calls around SQL — they'd appear as ordinary `CALLNAT` edges.)
- Relevant system variables with SQL-specific meaning: `*ROWCOUNT` (rows affected by DML),
  `*NUMBER`, `*ISN` (restricted under DB2).
- **`COMMIT` / `ROLLBACK` are forbidden inside `PROCESS SQL`** (to avoid transaction-sync problems);
  use the native `COMMIT`/`ROLLBACK` statements instead.

## Common Set vs Extended Set — verified

Natural SQL syntax is split:
- **Common Set** — statements/clauses that conform to standard SQL and are available on **all** SQL
  backends/platforms.
- **Extended Set** — Natural-specific restrictions/enhancements added on top, often **DBMS-specific**
  (e.g. DB2-only comparison operators, scalar functions, `OVERRIDING USER VALUE`, the array/`FOR n ROWS`
  bulk clause). Code using the Extended Set is not portable across all backends.
This matters for diagnostics later (an Extended-Set construct on a non-supporting backend is an error),
but does not change lexing.

## Backends this applies to — verified

- **Natural for DB2** (DB2 for z/OS; the primary, best-documented target).
- **Natural SQL Gateway / Adabas SQL Gateway** (lets Natural SQL reach Adabas and other RDBMS via the
  gateway; "CONNX"-based).
- **Natural for SQL/DS** (historical, DB2-family on VM/VSE).
- Note: Oracle/SQL Server etc. are reached via the SQL Gateway, not a dedicated "Natural for Oracle"
  product. Plain Adabas access does **not** use these SQL statements (it uses `READ`/`FIND` on DDMs).

## File extensions / object types carrying embedded SQL — verified

No special object type. Embedded SQL is just statements, so it can appear in any **source object that
has a procedural body**: `.NSP` program, `.NSN` subprogram, `.NSS` external subroutine, `.NSH`
helproutine (and copycode `.NSC` fragments included into them). Host vars live in `DEFINE DATA`, which
may be inline or pulled from `.NSL`/`.NSG`/`.NSA` data areas.

**`FROM`-table IS a Natural DDM (`.NSD`) — VERIFIED (2026-06-30).** Confirmed against the Natural SQL
"Basic Syntactical Items" reference: *"The item `ddm-name` always refers to the name of a Natural data
definition module (DDM) as created with the Natural DDM Editor"* and *"A Natural data definition module
(DDM) must have been created for a table to be used. The name of such a DDM must be the same as the
corresponding database table name or view name."* So in **native** Natural SQL the table operand in
`FROM` / `INSERT INTO` / SQL `UPDATE`/`DELETE` is a **DDM name** — the analyzer binds it to a `.NSD`
DDM (the **same separate DDM namespace** used by Adabas `READ`/`FIND`/`VIEW OF`), NOT the raw physical
SQL table name. Natural for DB2 / SQL Gateway require a DDM to be generated (via Predict or SYSDDM SQL
Services) before any Natural SQL statement can reference the table; the DDM name equals the table/view
name. Caveat: this binding is for *native* SQL only — inside opaque `<<…>>` flexible-SQL text the table
name is pass-through SQL text the compiler never resolves, so do NOT try to bind table names there.

## Minimal testdata fixture (future) — `.NSP`

Sanitized, exercises native SELECT loop + host vars + PROCESS SQL flexible block:
```
DEFINE DATA LOCAL
1 #PERS-ID   (N8)
1 #NAME      (A20)
1 #SALARY    (P7.2)
END-DEFINE
*
* Native SQL: host vars written WITHOUT colons (the idiomatic native form); FROM names a DDM.
SELECT NAME, SALARY
  INTO #NAME, #SALARY
  FROM SQL-PERSONNEL                 /* SQL-PERSONNEL is a .NSD DDM (== table/view name) */
  WHERE PERS_ID = #PERS-ID
  DISPLAY #NAME #SALARY
END-SELECT
*
* Flexible SQL: host vars MUST carry the colon; body is opaque pass-through text.
PROCESS SQL SQL-PERSONNEL
  <<  UPDATE SQL_PERSONNEL
        SET SALARY = SALARY * 1.05
      WHERE PERS_ID = :#PERS-ID  >>
*
CALLNAT 'NDBERR' #SQLCODE #SQLSTATE #SQLCA
END
```
(Expected extraction once the feature exists: read edge on the **DDM** `SQL-PERSONNEL` (DDM namespace),
bare host vars `#NAME`/`#SALARY`/`#PERS-ID` in the native SELECT bound to their `DEFINE DATA` fields;
write via the `PROCESS SQL` opaque body, whose `<<…>>` is opaque except the `:#PERS-ID` host-var ref;
`CALLNAT 'NDBERR'` call edge. Note both colon-less (native) and colon-prefixed (flexible) host-var
forms appear — the binder must handle both.)

## Sources

- Using Natural SQL Statements (full statement list — CALLDBPROC/COMMIT/DELETE/INSERT/MERGE/PROCESS
  SQL/READ RESULT SET/ROLLBACK/SELECT/UPDATE):
  https://documentation.softwareag.com/natural/nat911mf/sm/sql_use.htm
- PROCESS SQL (syntax, `<<>>` delimiters, `:U:`/`:G:` qualifiers, multi-line, no COMMIT/ROLLBACK):
  https://documentation.softwareag.com/natural/nat828dmf/sm/process-sql.htm
- Flexible SQL (`<<>>`, colon required, multi-line + comments, not compiler-parsed, copied at runtime):
  https://documentation.softwareag.com/natural/nat841unx/sm/sql-flexible-sql.htm
- SELECT (SQL) — cursor loop vs SELECT SINGLE, INTO on cursor selects, END-SELECT/LOOP by mode:
  https://documentation.softwareag.com/natural/nat841unx/sm/select-sql.htm
  https://documentation.softwareag.com/natural/nat838unx/sm/select-sql.htm
- INSERT (SQL) — colon host vars, `:NAME(*)` arrays, FOR n ROWS / DB2ARRY, backends:
  https://documentation.softwareag.com/natural/nat827mf/sm/insert-sql.htm
- READ RESULT SET (SQL) — only after CALLDBPROC, DB2 / SQL Gateway only:
  https://documentation.softwareag.com/natural/nat826mf/sm/read-result-set-sql.htm
- Dynamic and Static SQL Support (DB2; SELECT/FETCH/UPDATE/DELETE/INSERT, static vs dynamic):
  https://documentation.softwareag.com/natural/nat911mf/dbms/ndb-sqlsupp.htm
- Using Natural Statements and System Variables (no WHENEVER; NDBERR/NDBNOERR; SQLCODE/SQLSTATE/SQLCA;
  *ROWCOUNT/*NUMBER/*ISN; host-var format mapping):
  https://documentation.softwareag.com/natural/nat911mf/dbms/ndb-natsm.htm
  https://documentation.softwareag.com/natural/nat911dmf/dbms/ndb-natsm.htm
- Accessing Data in an SQL Database (programming guide; SELECT INTO host-var example):
  https://documentation.softwareag.com/naturalONE/natONE838/natwin/pg/pg_dbms_sqlos.htm
- Natural and Database Access (DBMS interfaces overview; backends):
  https://documentation.softwareag.com/natural/nat911mf/pg/pg_dbms_dbgen.htm
- Adabas SQL Gateway (product; gateway backend):
  https://www.softwareag.com/en/resources/adabas-natural/adabas-sql-gateway/
- Basic Syntactical Items (SQL) — host-variable definition, colon OPTIONAL in native SQL ("can also be
  prefixed by a colon"), reserved-word exception, flexible-SQL colon requirement, AND `ddm-name` =
  Natural DDM / "DDM must have been created for a table … name must be the same as the … table or view
  name" (the FROM-table → `.NSD` binding):
  https://documentation.softwareag.com/natural/nat912win/sm/sql-bsi.htm
- Accessing a DB2 Table (DDM must be generated via Predict/SYSDDM before Natural SQL can use the table):
  https://documentation.softwareag.com/natural/nat912mf/dbms/ndb-tableaccess.htm
- Generating Natural DDMs for DB2 (DDM-name-with-creator option; DDM built from DB2 catalog):
  https://documentation.softwareag.com/natural/nat828mf/dbms/ndb-ddm.htm