# Natural Knowledge Base — Index

Working reference on Software AG Natural, maintained by the `natural-expert` agent. Each topic file
holds verified facts with sources. Read this index first, then the relevant topics.

**Status legend:** `verified (date)` = corroborated against an authoritative source · `needs-verification`
= seeded belief, confirm before relying on it · `unverified` = recorded but unconfirmed.

## Topics

| File | Covers | Overall status |
|------|--------|----------------|
| [file-extensions.md](file-extensions.md) | `.NSx` object types and what each maps to | verified (2026-06-23) |
| [calls-and-resolution.md](calls-and-resolution.md) | CALLNAT / PERFORM / FETCH / RUN / INCLUDE, steplib resolution | verified (2026-06-30) |
| [identifiers-and-keywords.md](identifiers-and-keywords.md) | Identifier naming rules (length/first-char/`#`/`&`/`+`/hyphen/case); keywords are **context-sensitive not hard-reserved**; keyword-spelled subroutine names in `DEFINE SUBROUTINE`/`PERFORM` name positions (issue #41); KCHECK; natls `canBeIdentifier` re-tagging | verified (2026-07-20) |
| [embedded-sql.md](embedded-sql.md) | Native Natural SQL (SELECT/INSERT/…) + PROCESS SQL / flexible `<<…>>`, host-var colon rule (optional in native / mandatory in flexible), FROM-table→`.NSD` DDM binding, backends, error handling | verified (2026-06-30); no open items |
| [data-definition.md](data-definition.md) | DEFINE DATA, LDA/GDA/PDA, level structure; **variable referencing/scope/qualification** (`group.field` period rule, array-subscript stripping, REDEFINE aliases, same-object-vs-cross-file scopes, `*`-sysvar exclusion) for variable go-to-definition/references; **`VIEW OF` view-definition** (DDM subset, format/length inherited-from-DDM, REDEFINE-in-view, array index notation, DB2-DDM angle, view-field→`.NSD` binding) for outline enrichment | verified (2026-07-21) |
| [ddm-format.md](ddm-format.md) | `.NSD` exported DDM file: fixed-column report layout (byte offsets), header/TYPE lines, group/PE/MU, super/sub/phonetic descriptors, `DataDefinition` mapping (no model change), line-scanner parsing notes | verified (2026-07-06) |
| [modes-and-dialects.md](modes-and-dialects.md) | structured vs reporting mode, mainframe vs Linux/NaturalONE | verified (2026-06-23); column rules confirmed free-format |
| [example-projects.md](example-projects.md) | public Natural source corpora & fixture candidates (licenses) | verified (2026-06-20) |
| [natls-prior-art.md](natls-prior-art.md) | MarkusAmshove/natls: prior-art Natural LSP — scope, file types, resolution, source header, lint/parser diagnostics, LSP features | verified (2026-06-21) |

## Open questions (to verify on next relevant task)

- **Reporting-mode fixtures don't exist publicly** — the public corpus is almost entirely structured
  mode (see example-projects.md). natls itself does NOT support reporting mode at all. Reporting-mode
  regression fixtures (DO/DOEND, loop-collapsing `END`/`LOOP`, undeclared vars) will have to be
  hand-authored from the docs.
- **License hygiene for fixtures** — only natls (MIT) and the Software AG sample/education repos
   (Apache-2.0) are safe to derive committed fixtures from; all community GitHub repos found are
  unlicensed. Decide on the attribution mechanism for derived fixtures before importing any.
- **Map (`.NSM`) / helproutine (`.NSH`) coverage is scarce** — only the NaturalCruise DevOps sample
  has a meaningful set; confirm the analyzer's map/INPUT-USING-MAP handling against it. (Note: natls's
  MAP type CAN carry a DEFINE DATA and a body.)
- **Project-file discovery** — natls locates the workspace by a `.natural` or `_naturalBuild` build
  file whose parent holds `Natural-Libraries/<LIB>/`. Our `.natural-lsp.toml` sentinel is our own; decide
  whether to also read the native `.natural` build file for steplib config.

## Changelog

- 2026-07-21 — EXTENDED `data-definition.md` with a "VIEW OF (view-definition)" section to scope an
  **outline-enrichment + VIEW OF binding** feature. VERIFIED findings: (1) `level view-name VIEW [OF]
  ddm-name` binds to a **DDM (`.NSD`)**; view field lines are an explicit **SELECTION (subset)** of the
  DDM's fields. (2) **Format/length are inherited from the DDM** when omitted on the view line (exact
  rule quoted: *"If omitted, these are taken from the DDM. In structured mode, the definition of format
  and length (if supplied) must be the same as those in the DDM."*) — so the authoritative type lives
  in the DDM; "decoding a record layout into logical fields" = resolving each bare view field to its
  DDM field definition (`ddm.go` `DataDefinition`). (3) A DDM can be over **Adabas OR a DB2 table/view**
  (SYSDDM/NSB generation) — the "DB2 view" angle; the `.NSD` is always the binding target, never reach
  past it to the DB2 catalog; SQL-typed DDMs (`TYPE: SQL`) use a different column layout that `ddm.go`
  currently skips (scoping limit). (4) **REDEFINE inside a view** carves a DDM field into user
  sub-fields (verified `REDEFINE BIRTH → BIRTH-YEAR/MONTH/DAY`); `FILLER nX` = skipped bytes,
  trailing-filler optional; sub-fields are aliases over the same storage; view-local redefine
  sub-fields have NO DDM declaration (definition is the view's own line). (5) Arrays use **index ranges,
  NOT `OCCURS`**: `(1:2)`, `(A20/1:2)`, multi-dim `(1:10,1:5)`, single `(2)`, variable `(#K:#K+1)`;
  render compactly as `A20 (1:2)`/`P9,2`. (6) **Go-to-definition on a view field → the DDM field
  declaration in `.NSD`**, resolved via the **same DDM namespace/steplib chain as READ/FIND** (feature
  27 binding); view name → its own VIEW OF line. Sources: defineda_view.htm (view syntax + format/length
  rule + EMP examples), defineda_redef.htm (REDEFINE/FILLER/BIRTH example), pg_obj_ddm.htm (DDM =
  logical view; ddm-name vs view-name), ndb-ddm.htm/nsb-ddm.htm (DB2-DDM generation).
- 2026-07-21 — EXTENDED `data-definition.md` with a "Variable referencing, scope & qualification"
  section to scope a **variable go-to-definition / find-references** LSP feature. VERIFIED findings:
  (1) **Scopes:** inline `LOCAL`/`PARAMETER`/`INDEPENDENT`(+AIV)/`CONTEXT`/`OBJECT` defs are
  same-object; `LOCAL USING`/`PARAMETER USING`/`GLOBAL USING` live in separate `.NSL`/`.NSA`/`.NSG`
  data-area objects → cross-file bind. (2) **Qualification is a period:** `group-name.field-name`,
  qualifier must be a **level-1** element; required only when a sub-field name is non-unique across
  groups (verified `FULL-NAME.LAST-NAME` / `OUTPUT-NAME.LAST-NAME` example). A non-unique unqualified
  reference is genuinely ambiguous → surface all candidates. (3) **Array `#T(1:10)`/`#T(I)`** →
  definition is `#T`; strip subscript; an index variable `I` is its own reference. (4) **REDEFINE**
  sub-fields are distinct named aliases (own declaration lines; feature-08 models them as `Children`).
  (5) **No lexical shadowing** — namespace is flat per run unit, disambiguated by qualification not
  nesting; a valid module has ≤1 declaration per unqualified name path. (6) **System variables**
  (`*DATX`/`*LANGUAGE`/…) are predefined + read-only, never in `DEFINE DATA` → EXCLUDE from
  user-variable navigation. Recommended first-version scope: same-file `DEFINE DATA` inline blocks
  only; defer `USING` LDA/PDA/GDA cross-file binding. Sources: pg_defi.htm (qualify/REDEFINE),
  pg_defi_array.htm + pg_output_index.htm (index notation), vari/sysenv.htm (sysvars read-only).
- 2026-07-20 — ADDED topic `identifiers-and-keywords.md` to ground a parser fix (issue #41: keyword-
  spelled subroutine names, e.g. `DEFINE SUBROUTINE CLEAR ... END-SUBROUTINE` / `PERFORM CLEAR`). Key
  VERIFIED findings: (1) Natural keywords are **context-sensitive, not hard-reserved** — no absolute
  lex-time prohibition; the docs give a *strong recommendation* against keyword-spelled names plus a
  narrower **ambiguity** concern for the *statement/system-function* subset (`ADD`, `FIND`, `ABS`,
  `SUM`, …) that bites ONLY in *optional-operand* positions (`CALLNAT 'MYSUB' ADD 4` reads `ADD` as the
  statement). KCHECK only *flags* the subset; it doesn't change legality; off by default. (2) A
  subroutine name follows **user-defined-variable naming rules** (identifier, ≤32 significant chars,
  first char `A`–`Z`/`#`/`+`/`&`, embedded `-`/`_`/`/`/`@`/`$`) — it is an *identifier*, not a string
  constant. `PERFORM PRINT` (PRINT a keyword) appears verbatim in the DEFINE SUBROUTINE doc. (3) **Parser
  rule:** in the `DEFINE SUBROUTINE <name>` and `PERFORM <name>` name-mandatory positions, ACCEPT a
  keyword token as the name (re-tag it to identifier) — no keyword can start a different construct there,
  so no ambiguity. Guardrails: do NOT globally treat keywords as identifiers (breaks operand parsing);
  `PERFORM BREAK` is a distinct statement (disambiguate first); keyword-spelled variable target →
  dynamic; never emit a parse diagnostic for it (at most a lint). (4) Cross-checked natls: it does
  exactly this via `SyntaxKind.canBeIdentifier` + `consumeIdentifierTokenOnly().withKind(IDENTIFIER)` in
  both `subroutine()` and `perform()`; `CLEAR`/`HALT` aren't even keyword tokens in natls (lexed as
  identifiers), and keyword-as-identifier is only its lint `NL011`, never a parse error. Sources:
  use_rules.htm, pg_keyw.htm (KCHECK/ambiguity subset), pg_defi_dv.htm, definesu.htm, natls
  AbstractParser/StatementListParser/SyntaxKind.
- 2026-07-13 — VERIFIED FETCH/RUN parameter-passing model for a **signature-help** design decision.
  Key finding: a program invoked by `FETCH`/`FETCH RETURN`/`FETCH REPEAT`/`RUN` receives caller data
  as **untyped positional values on the Natural stack, read via `INPUT`** — there is **NO declared
  parameter interface** (no PARAMETER block is bound). `DEFINE DATA PARAMETER` is documented ONLY for
  subprogram / external subroutine / helproutine / function (bound by CALLNAT / external PERFORM /
  function-call). A PROGRAM (`.NSP`) *can* syntactically carry `DEFINE DATA` (natls
  `canHaveDefineData()` = true for PROGRAM), but PARAMETER scope in a program has no caller to bind it
  (a program is not CALLNAT'd) and is not populated by FETCH/RUN. **Decision: signature help for
  FETCH/RUN is a category error — implement it for CALLNAT / external PERFORM / function-calls only.**
  Updated calls-and-resolution.md (new "FETCH/RUN parameter passing" + "Can a PROGRAM contain
  DEFINE DATA PARAMETER" sections, sources) and data-definition.md (PARAMETER-scope note). Sources:
  fetch.htm, pg_furth_stack_process.htm, defineda_pda.htm, callnat.htm, techcommunity FETCH-RETURN
  thread, natls NaturalFileType.java.
- 2026-07-06 — ADDED topic `ddm-format.md` (for `.NSD` DDM field extraction → hover). VERIFIED the exact
  exported DDM file format byte-for-byte against the natls DDM parser (`parsing/ddm/FieldParser.java` +
  real `CompleteDdm.NSD` fixture, MIT) and the official Software AG DDM Editor column reference. Key
  findings: (1) `.NSD` is a **fixed-column** "DDM report" (NOT whitespace-delimited, NOT Natural source
  — needs a dedicated line-scanner). Exact 0-based offsets: T=0, L=2, DB=4(len2), Name=7(len32), F=41,
  Leng=43(len4), S=49, D=51, Remark=53+. Short lines are saved without trailing spaces → pad missing
  columns. (2) Header `DB: n FILE: n  - NAME  DEFAULT SEQUENCE:`, optional `TYPE: ADABAS|SQL`, dashed
  separator, `*` comment/Source-Header lines, `******DDM OUTPUT TERMINATED******`. (3) T column:
  ` `=elem, `G`=group, `P`=periodic(PE), `M`=multiple(MU); groups nest by level containment (like
  DEFINE DATA). (4) D column: `D`=descriptor `S`=super `U`=sub/unique `P`=phonetic `H`=hyper `N`=non;
  a superdescriptor row is followed by a `*  SOURCE FIELD(S)` block of `NAME(from-to)` refs. (5)
  **Mapping onto `model.DataDefinition` needs NO model change and NO cache-format bump** for hover:
  `Type`="N8"/"A50"/"P9,2", groups via `Children`+`Level`, MU/PE as an unbounded `*` `Dimensions` entry
  (reusing existing `*`-bound support). DB short name + descriptor flags are dropped for hover (would
  only force a model change if a *future* feature needs them). (6) SQL DDMs use a DIFFERENT column
  layout (natls `SqlFieldParser`) — out of scope until separately verified. REPLACED the guessed
  placeholder `internal/server/testdata/hover/customer.NSD` with a byte-correct fixture (scalars N8/A50,
  ADDRESS group w/ children, P9,2, MU PHONE, superdescriptor + source block). Corrected the stale
  "column order `C T L Name F Length S D`" note in file-extensions.md (the real header is
  `T L DB Name F Leng S D Remark`; there is no `C` column — the `C`/coupled marker is a `T`-column
  value, not a separate column).
- 2026-06-30 (KB audit sweep) — Full re-read of INDEX.md + all 7 topic files. NO `needs-verification`
  or `unverified` markers remain anywhere; all topics are `verified`, and every remaining INDEX open
  question is a corpus/license/tooling DECISION, not an unconfirmed language fact. Rather than
  rubber-stamp, re-checked four decision-critical "verified" claims against the current live Software AG
  docs; all four still match verbatim (no staleness found):
    1. **Steplib search sequence & non-transitivity** (`use_mf_libs.htm`) — confirmed the exact
       user-lib order (current → steplibs in sequence → `*STEPLIB` → SYSTEM/FUSER → SYSTEM/FNAT) and
       system-lib order; doc describes single-level concatenation ("a library … concatenated with the
       current library"), corroborating NON-transitive. The 2026-06-30 correction stands.
    2. **CALLNAT name lengths + `&` substitution** (`natux 9.3.3 sm/callnat.htm`) — constant 1–32,
       variable 1–8, `&` replaced at runtime by the `*LANGUAGE` one-char code. Verbatim match.
    3. **SQL host-var colon rule + `ddm-name`** (`nat912win sm/sql-bsi.htm`) — colon OPTIONAL in native
       SQL ("can also be prefixed by a colon"), MANDATORY in flexible SQL, always required for
       reserved-word names; `ddm-name` "always refers to the name of a Natural data definition module
       (DDM)". Verbatim match.
    4. **All 15 `.NSx` extension→object-type mappings** (`natONE912 use-edis-geninfo.htm`) — the current
       "Types of Natural Editors" table confirms all 15 (NSP/NSN/NSS/NS7/NSC/NSH/NST/NS3/NS8/NS4/NSG/
       NSL/NSA/NSM/NSD) verbatim. `.NKR`/`.NR3` are not on this page but remain sourced from the
       dedicated RESOURCE object-type page cited in file-extensions.md.
  No corrections needed; no new language-fact open questions surfaced. KB confirmed clean.
- 2026-06-30 (KB sweep) — RESOLVED both embedded-SQL open questions against the official Natural SQL
  "Basic Syntactical Items" reference (`sm/sql-bsi.htm`), plus the DB2 table-access / DDM-generation
  pages. Two decision-critical resolution facts confirmed:
    - **Host-var colon is OPTIONAL in native Natural SQL** (doc: a host-variable "can *also* be prefixed
      by a colon"), **MANDATORY in flexible SQL** (`<<…>>`), and **always required when the variable
      name equals an SQL reserved word**. The binder must accept native-SQL host vars with OR without the
      leading colon; bare `#`-prefixed names are the idiomatic native form. (Was needs-verification.)
    - **A native-SQL `FROM`/`INTO`/SQL-`UPDATE`/`DELETE` table name IS a Natural DDM (`.NSD`)** — doc:
      "`ddm-name` always refers to the name of a Natural data definition module (DDM)" and "A … DDM must
      have been created for a table to be used. The name of such a DDM must be the same as the
      corresponding database table name or view name." Bind it in the **same DDM namespace** as Adabas
      `READ`/`FIND`/`VIEW OF`. Inside opaque `<<…>>` text, do NOT bind table names. (Was an open
      question.)
  Updated embedded-sql.md (status → no open items, host-var section, FROM-table section, lexer-impl
  bullets, fixture rewritten to show colon-less native + colon flexible forms + DDM FROM, sources).
  Removed both open questions. Also corrected calls-and-resolution.md citation hygiene: the bottom
  "Sources" list still labeled the Predict XRef page "(transitive resolution)" — relabeled to match the
  body's verified NON-transitive finding; bumped that topic's status header date to 2026-06-30. No new
  Natural-fact open questions surfaced (remaining open questions are corpus/license/tooling decisions,
  not language facts).
- 2026-06-30 — ADDED topic `embedded-sql.md`: how Natural embeds SQL (groundwork for a future
  data-access/SQL extraction feature). Key verified findings:
    - **No `EXEC SQL … END-EXEC` block.** Natural has TWO embedding styles: (1) native Natural SQL
      statements (`SELECT … END-SELECT`/`SELECT SINGLE`, `INSERT`, SQL-form `UPDATE`/`DELETE`, `MERGE`,
      `COMMIT`, `ROLLBACK`, `CALLDBPROC`, `READ RESULT SET`) parsed by the Natural compiler; and
      (2) `PROCESS SQL ddm-name <<text>>` + "flexible SQL" `<<…>>` — an opaque pass-through SQL string
      the compiler does NOT parse (errors caught at runtime).
    - **Host variables use a colon prefix** `:host-var`, optional qualifiers `:U:` (USING/in, default),
      `:G:` (GIVING/out), `:T:` (text spliced as SQL), `INDICATOR`/`LINDICATOR`; arrays `:NAME(*)`.
      Colon is mandatory in flexible SQL; appears OPTIONAL in native Natural SQL (open question added).
      Host vars are ordinary `DEFINE DATA` fields (format→DB2-type mapping recorded).
    - **`SELECT` is a DB loop** closed by `END-SELECT` (structured) / `LOOP` (reporting); Natural manages
      the cursor automatically — no app-level DECLARE/OPEN/FETCH/CLOSE in native form. `INTO` is used on
      cursor selects too (unlike ISO SQL).
    - **No SQL `WHENEVER`.** Errors flow through Natural `ON ERROR`; `NDBNOERR`/`NDBERR` subprograms
      suppress/inspect SQL errors (`SQLCODE`/`SQLSTATE`/`SQLCA`). `*ROWCOUNT` etc.
    - **Lexer notes:** `<<`/`>>` are flexible-SQL delimiters ONLY in SQL context (`<`/`>` are otherwise
      comparison ops); inner SQL is multi-line with no continuation char, may contain comments, must be
      treated as opaque except for `:host-var` refs; no trailing `;`; same-keyword Adabas-vs-SQL
      ambiguity for `UPDATE`/`DELETE`/`STORE`.
    - **ADD-ON, not Adabas core:** provided by Natural for DB2 / Natural SQL Gateway (Adabas SQL Gateway)
      / Natural for SQL/DS. Plain Adabas uses `READ`/`FIND` on DDMs, not these SQL statements. Common
      Set (portable) vs Extended Set (DBMS-specific) split noted.
    - **No special object type** — SQL appears in any procedural-body source (`.NSP`/`.NSN`/`.NSS`/`.NSH`
      + `.NSC` fragments). Added 2 open questions (native colon optionality; `FROM`-table → `.NSD` DDM).
- 2026-06-30 — CORRECTED a wrong "verified" steplib fact. The 2026-06-23 entry claimed the runtime
  steplib search is **transitive** (steplib-of-steplib recursion), citing the Predict XRef "Steplib
  Support" page. That was wrong on two counts: the cited page is the **XRef/cross-reference tool**, not
  the runtime, AND it itself describes single-level (not transitive) search. The authoritative runtime
  "Search Sequence for Object Execution" page defines a **flat ordered list bound to the current
  library**: current lib → declared steplibs (in sequence) → `*STEPLIB` → SYSTEM(FUSER) → SYSTEM(FNAT)
  [user-lib case]. **Steplib search is NON-transitive / one-level.** A steplib's own steplibs are not
  followed; the invoking runtime's current-library chain is what's searched. Max steplibs = 8 per
  library under Natural Security (1 via the STEPLIB param without NSC); the "20" figure was the XRef
  tool's limit. Explicitly library-qualified `RUN program-id library-id` bypasses the chain (single
  library). Matches natls (one level). Updated calls-and-resolution.md; closed the steplib-transitivity
  open question below.
- 2026-06-29 — VERIFIED comment syntax against official Software AG "User Comments" doc
  (decision-critical for the comment lexer). Both Natural comment forms are **REST-OF-LINE**; Natural
  has **NO C-style delimited `/* ... */` block comment** and no `*/` closer. `/*` marks the remainder
  of the physical line; a later `*/` is just comment text, code does NOT resume after it; comments
  never span lines. Mid-line `*` is always multiplication. Leading `*`/`**` only at line start (with
  natls's next-char guard). Written to modes-and-dialects.md (full verbatim quotes + lexer rules) and
  natls-prior-art.md. Source: https://documentation.softwareag.com/natural/nat827mf/pg/pg_furth_ucom.htm
- 2026-06-23 — VERIFIED critical open questions from KB:
    - **Steplib recursion:** Natural **does search transitively** through chained steplibs (up to 8 steplibs supported). Confirmed in Software AG performance docs.
    - **CALLNAT/FETCH name length:** CALLNAT constant = 1–32 chars (9.3.1+), variable = 1–8 chars. FETCH constant/variable = 1–8 chars only (not 1–32).
    - **&/LANGUAGE substitution:** Confirmed for both CALLNAT and FETCH; treat literals with `&` as dynamic/unresolvable.
    - **RESOURCE object type:** `.NKR` extension for shared resources; `.NR3` for private dialog resources.
    - **DDM column grammar:** Exact order is `C T L Name F Length S D` (DB is optional toggle).
    - **NaturalONE column rules:** Confirmed **free-format** (Eclipse-based); no fixed columns.
    - **Reporting-mode grammar:** `DO/DOEND` blocks, `END`/`LOOP` loop-collapsing, `END-IF` causes error in reporting mode.
    - **Array-bound grammar:** Inline syntax `(A10/1:5)` confirmed; multi-dim `(1:5,1:3)`; extensible `(A10/1:*)`.
    - **Project-file discovery:** `.natural` file used in NaturalONE for unsecured steplib config.
- 2026-06-21 — ADDED topic `natls-prior-art.md`: deep study of MarkusAmshove/natls (MIT), the mature
  prior-art parser-based Natural LSP, read from source. Key findings written back:
    - file-extensions: CONFIRMED the 11 core ext→type mappings independently; natls SKIPS NS4/NS8/NST/NS3
      (TODO in source) → lower priority. ADDED canHaveDefineData/canHaveBody per-type predicates (MAP can
      have both; data areas + DDM cannot have a body). ADDED that `.NSS`/`.NS7` referable names come from
      the in-source `DEFINE SUBROUTINE`/`DEFINE FUNCTION` identifier (not filename, can be >8 chars).
      ADDED that `.NSD` is a tabular non-source format needing a separate parser.
    - modes-and-dialects: RESOLVED the "how is mode known" question — NaturalONE source-header block
      (`* >Natural Source Header` … `* :Mode S|R` … `* <Natural Source Header`) declares mode, code page,
      line increment. ADDED verified comment markers (`*` line-start guard, `/*` inline; `/` vs array-bound
      ambiguity).
    - calls-and-resolution: CROSS-CHECKED resolution — one `IModuleReferencingNode` covers CALLNAT/PERFORM
      (external)/FETCH/INCLUDE/function-call/`USING`; PERFORM inline-first is structural; FETCH RETURN
      distinguished; current-lib→ordered-steplibs (ONE level in natls), DDM separate namespace, implicit
      SYSTEM steplib, `.natural`/`_naturalBuild` + `Natural-Libraries/<LIB>/` layout.
    - data-definition: CONFIRMED array bounds + REDEFINE clause are in-scope (natls parser errors);
      clarified "REDEFINE statement (reporting, not planned)" ≠ "REDEFINE clause in DEFINE DATA";
      ADDED comma-as-decimal-separator gotcha.
    - INDEX: resolved/refined 6 open questions; added 3 new (steplib transitivity, DDM tabular grammar,
      RESOURCE type, native `.natural` build-file reading).
- 2026-06-21 — file-extensions: re-verified full source table against NaturalONE editors "General
  Information" page. ADDED standalone-vs-fragment column (copycode/data-areas/DDM/text = fragments).
  ADDED source-vs-generated `NS*` / `NG*` distinction (NSP→NGP, map source→`.NGM`; LSP indexes `NS*` only).
  CONFIRMED `.NAT` is NOT an official Natural extension (only on third-party file sites). CONFIRMED
   `.NSX/.NSV/.NSE/.NSK/.NSB` do not exist as object types; `NSF` = product (Natural SAF Security), not an
  extension; `.SAG` = work-file binary extension, not source.
- 2026-06-20 — ADDED topic `example-projects.md`: catalog of public Natural source corpora and
  fixture candidates. Verified existence + inspected file counts via GitHub API / live docs.
  Strongest fixture sources: MarkusAmshove/natls (MIT, ~186 `.NSx` fixtures in standard library
  layout), and the two Software AG cruise demos (Apache-2.0). Documentation SYSEXPG/SYSEXSYN examples
  are authoritative but doc-licensed (read-only). All community repos found are unlicensed → reference
  only. Public corpus assessed as thin-but-adequate; reporting-mode and mainframe fixed-column code
  under-represented. Added 4 open questions.
- 2026-06-20 — Full sweep; all four topics verified against official Software AG docs.
    - file-extensions: ADDED missing types `.NS4` (class), `.NS7` (function), `.NS3` (dialog),
      `.NS8` (adapter), `.NST` (text); CONFIRMED `.NSD` = DDM. Set verified.
    - calls-and-resolution: CORRECTED/expanded several facts —
      (1) CALLNAT name = constant 1–32 OR variable 1–8 (variable form is the dynamic case);
      (2) `&`/`*LANGUAGE` substitution means a literal name containing `&` is NOT a clean static target;
      (3) FETCH/RUN program name CAN be a variable → also a `CALLS_DYNAMIC` source, not always
          `NAVIGATES_TO` (seed implied static only);
      (4) RUN is a SYSTEM COMMAND (`RUN [REPEAT] [program-name [library-id]]`), not a regular statement,
          and its optional library-id bypasses the steplib chain;
      (5) INCLUDE copycode-name is LITERAL-ONLY (never a variable) — always resolvable except for `&`;
      (6) PERFORM resolves INLINE subroutine first, then external (`.NSS`) — analyzer must scan
          `DEFINE SUBROUTINE` before emitting external edge;
      (7) documented exact 4-level steplib search order (current lib → steplibs → *STEPLIB →
          SYSTEM/FUSER then SYSTEM/FNAT).
    - data-definition: CONFIRMED clauses (LOCAL/GLOBAL/PARAMETER/INDEPENDENT/CONTEXT/OBJECT), mandatory
      `END-DEFINE`, GLOBAL-then-PARAMETER ordering rule, and format codes (A U N P I B F L D T C).
    - modes-and-dialects: CONFIRMED structured (`END-...` closers) vs reporting (`DO/DOEND`, loop-collapsing
      `END`/`LOOP`; `END-IF` etc. error) and reporting-mode undeclared variables/DDM refs; noted FETCH/RUN
      name case is NOT translated (exception to case-insensitivity).
- (seed) Created index and topic stubs from project README/CLAUDE.md. None web-verified yet.
