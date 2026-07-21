# Data Definition

`DEFINE DATA` and the data-area object types.
Dialect/mode: verified against Natural for Linux/Unix/Windows + Mainframe references. `DEFINE DATA` is
available in BOTH structured and reporting mode.

**Status: verified (2026-06-23)** — section clauses, terminator, format codes, and array-bound grammar
confirmed against official Software AG documentation. Array bounds placed inline after format with `/` separator.

## DEFINE DATA structure — verified

```
DEFINE DATA
  [GLOBAL  USING gda-name [WITH block] | <inline global defs>]
  [PARAMETER USING pda-name | <inline parameter defs>]
  [LOCAL   USING lda-name | <inline local defs>]
  [INDEPENDENT <AIV defs>]
  [CONTEXT <context-variable defs>]
  [OBJECT  <NaturalX object defs>]
END-DEFINE
```

- At least ONE clause is required (empty `DEFINE DATA` is illegal).
- Valid clauses: `GLOBAL`, `PARAMETER`, `LOCAL`, `INDEPENDENT`, `CONTEXT`, `OBJECT`.
  - `INDEPENDENT` defines Application-Independent Variables (AIVs), names start with `+`.
  - `CONTEXT` defines RPC context variables (shared across remote subprograms in a conversation).
  - `OBJECT` defines NaturalX object data.
- Ordering rule: if `GLOBAL` is used it must come FIRST; if `PARAMETER` is also used it must follow
  `GLOBAL` (or come first if no GLOBAL). Other clauses in any order.
- The statement MUST be terminated by `END-DEFINE` (reserved word).
- `USING` binds a clause to an external data area object: LDA (`.NSL`), GDA (`.NSG`), PDA (`.NSA`).
  These `... USING name` lines are the read/include edges the analyzer should extract (target the
  corresponding `.NSx` data area).

## Variable definition syntax — verified (format codes) / partially verified (grammar detail)

General form of a field line:
```
level  name  (format-length [/array-bounds]) [options]
```
Example LOCAL block:
```
DEFINE DATA LOCAL
1 #CUSTOMER
  2 #ID        (N7)
  2 #NAME      (A30)
  2 #BALANCE   (P9.2)
1 #FLAGS       (L/1:10)        /* array of 10 logicals
1 #ID-CHARS    (A7)
1 REDEFINE #ID-CHARS
  2 #ID-PREFIX (A3)
  2 #ID-REST   (A4)
END-DEFINE
```

- **Level numbers** (1, 2, 3, …) express group/field hierarchy. A field with sub-levels is a group.
  (Leading zeros optional: `01` and `1` both occur.)
- **Format codes** (verified):
  - `A` alphanumeric, `U` Unicode alphanumeric
  - `N` numeric unpacked, `P` packed numeric (up to 29 digits, max 7 after decimal)
  - `I` integer (binary integer, e.g. I2/I4), `B` binary
  - `F` floating point (F4/F8), `L` logical (boolean)
  - `D` date, `T` time, `C` attribute control
- **Length notation:** `(A20)` = alpha 20; `(N7.2)` = 7 digits integer part, 2 fractional;
  `(P5)` packed 5 digits. Dynamic length: `(A) DYNAMIC`.
- **Arrays/occurrences:** bound syntax `(1:10)` after the format, e.g. `(A10/1:5)` or `(N3/1:12)`;
  multi-dimensional `(1:5,1:3)`. **Confirmed:** bounds are placed **inline** after the format with `/` separator.
  Single bound defaults lower to 1: `(A10/10)` = `(A10/1:10)`. Extensible arrays use `*`: `(A10/1:*)`.
  Comma is both a valid decimal separator AND a dimension separator (must disambiguate).
- **REDEFINE:** `level REDEFINE field` introduces an alternate layout over an already-defined field;
  sub-levels carve up the storage. The analyzer should treat REDEFINE sub-fields as aliases, not new
  storage.
- **VIEW (DDM access):** `level name VIEW OF ddm-name` followed by the DDM fields to use; this is a
  read edge to a DDM (`.NSD`). Full semantics in the "VIEW OF" section below.

## VIEW OF (view-definition) — verified (2026-07-21)

Grounds an **outline-enrichment + VIEW OF binding** feature (view field → DDM field). A view is a
Natural-program-local *subset* of a DDM's fields, bound in `DEFINE DATA` for Adabas/SQL record access.
Dialect: Natural for Mainframes + Linux/Unix/Windows (NaturalONE); rule differences noted per mode.

### Syntax

```
level  view-name  VIEW [OF] ddm-name
  level  ddm-field  [ (format-length) ] [ /array-definition ] [emhdpm]
  ...
  [ level REDEFINE ddm-field
      level rfield (format-length [/array-definition])
      ... ]
```

- `view-name` — the name of the view as declared here (a `DEFINE DATA` symbol, level typically `1`).
- `OF` is documented as optional (`VIEW OF` vs `VIEW`), though `VIEW OF` is the near-universal form.
- `ddm-name` — **always the name of a Natural DDM** (`.NSD`). Same namespace/resolution as
  `READ`/`FIND`/`GET` and native-SQL `FROM`-tables.
- The field lines are **DDM field names** (each names a field that exists in the DDM). They are an
  explicit **SELECTION (subset)** of the DDM's fields — a view lists only the fields the program uses.
- The `2 field` lines under the view are usually level-2 (the view header being level-1); the level
  hierarchy mirrors the DDM's group/PE structure for the selected fields.

### Format/length are INHERITED from the DDM — verified (exact rule)

> *"If omitted, these are taken from the DDM. In structured mode, the definition of format and
> length (if supplied) must be the same as those in the DDM."* — View Definition, DEFINE DATA.

So a view field line can be written **bare** (name only) → its format/length come from the DDM; or
**restated** (`NAME (A20)`) → in **structured mode** it must match the DDM exactly (a restatement,
not a redefinition of the type); in **reporting mode** it may differ but must be type-compatible.
Verified multi-form example (all three EMP views select the same DDM fields, differing only in whether
format/array bounds are restated):

```
DEFINE DATA LOCAL
1 EMP1 VIEW OF EMPLOYEES
  2 NAME              (A20)      /* format restated (must match DDM)
  2 ADDRESS-LINE      (A20/1:2)  /* format + array bounds restated
1 EMP2 VIEW OF EMPLOYEES
  2 NAME                         /* bare — format inherited from DDM
  2 ADDRESS-LINE      (1:2)      /* only array bounds restated
1 EMP3 VIEW OF EMPLOYEES
  2 NAME
  2 ADDRESS-LINE      (2)        /* single-occurrence array element
END-DEFINE
```

**Implication for outline enrichment / "decode a record layout into logical fields":** a view field's
authoritative type/length lives in the **DDM**, not always at the view's own field line (it's optional
there). To render a complete, correct `detail` (`A20`, `P9,2`, MU array bounds, descriptor-ness) for a
bare view field, you must **resolve the field name into the DDM's field definition** (`ddm.go`'s
`DataDefinition` list) and pull the type from there. That resolution IS the value of "decoding the
record layout into logical fields": a flat `VIEW OF EMPLOYEES` with a handful of field names becomes a
typed, structured field tree by joining against the DDM. When the view restates the format, prefer the
DDM as the source of truth (they must agree in structured mode) and only fall back to the view-line
format if the DDM field is unavailable.

### DDM over Adabas OR DB2/SQL — verified

- `ddm-name` is a DDM regardless of backend. A DDM is *"a logical view of a physical database file"*;
  it can be generated from an **Adabas** file OR from a **DB2 table or view** (via SYSDDM's SQL
  Services / NSB function, from the DB2 catalog). All DB2 data types and referential integrity are
  supported. So `VIEW OF <ddm>` where the DDM was generated from a DB2 view is the "DB2 view" angle —
  the Natural view binds to a `.NSD` DDM, which itself maps to a DB2 table/view. **The `.NSD` is always
  the binding target; the analyzer never needs to reach past the DDM to the DB2 catalog.**
- SQL-backed DDMs use the different `TYPE: SQL` column layout (see ddm-format.md — Adabas offsets do
  NOT apply; `SqlFieldParser` in natls). The current `ddm.go` scanner skips `TYPE: SQL` DDMs, so view
  fields over an SQL DDM won't resolve until SQL-DDM parsing is added — flag as a scoping limit.

### REDEFINE inside a view — verified

A view can carve a DDM field into user sub-fields with the `REDEFINE` clause immediately after the
field. Verified example (redefining an Adabas `BIRTH` field into Y/M/D):

```
DEFINE DATA LOCAL
1 MYVIEW VIEW OF STAFF
  2 NAME
  2 BIRTH
  2 REDEFINE BIRTH
    3 BIRTH-YEAR   (N4)
    3 BIRTH-MONTH  (N2)
    3 BIRTH-DAY    (N2)
END-DEFINE
```

REDEFINE syntax (general, from the Redefinition page):
```
REDEFINE field-name
  [level] rgroup [(array-definition)]
    rfield (format-length [/array-definition])
    FILLER nX
```
- Redefines *"a group, a view, a DDM field or a single field/variable (scalar or array)"*.
- **`FILLER nX`** defines `n` filler bytes — a skipped segment not to be used — inside the redefined
  field; **trailing filler is optional**. (natls error `NPP021 FILLER_MISSING_X` guards the `X`.)
- Level rules: sub-fields carry a level `>` the redefined field's level (01–99, 1- or 2-digit); a
  field of level ≥2 belongs to the immediately-preceding lower-level group.
- Handles, X-arrays and dynamic variables cannot be redefined; a view/DDM-field redefinition is not
  allowed in a `PARAMETER` data definition (`NPP022/023/024`, `NPP050`).
- The sum of the redefine sub-field lengths must not exceed the redefined field's length
  (`NPP015 REDEFINE_LENGTH_EXCEEDS_TARGET_LENGTH`).

**Outline rendering for REDEFINE:** show the redefine block as a child of the field it redefines,
listing sub-fields with their types/positions (a `FILLER nX` becomes an unnamed `nX` gap). The redefine
sub-fields are **aliases over the same storage**, each its own named declaration (matches feature-08's
`Children` model). Overlapping/partial coverage is legal — do not warn.

### Array / multiple-value / periodic notation in a view — verified

Natural uses **index ranges**, NOT the COBOL `OCCURS` keyword. A view field that is an MU (multiple-
value) or belongs to a PE (periodic group) is written with an index range after the (optional) format:

- One-dimensional: `ADDRESS-LINE (1:2)` (bare, bounds only) or `ADDRESS-LINE (A20/1:2)` (format+bounds).
- Two-dimensional (MU within PE): `(1:10,1:5)` — comma separates dimensions.
- Single element: `ADDRESS-LINE (2)` selects occurrence 2 (a single-occurrence reference).
- Variable index: `(#K:#K+1)` — a range using Natural variables (reporting/runtime index).
- The lower bound defaults to 1 when a single number is a *count*, but in a view a bare `(n)` is a
  single-occurrence *index* — disambiguate carefully (see below).

**Compact rendering:** `<format> (<bounds>)`, e.g. `A20 (1:2)`, `P9,2 (1:10,1:5)`. This is consistent
with the DEFINE DATA / DDM rendering already used for scalars. NOTE the DDM report itself gives NO
numeric occurrence bounds for MU/PE (ddm-format.md) — so for a **bare** view MU field the bounds may
have to come from the view line (if restated) or be shown as unbounded/unknown.

### What the outline-enrichment + binding feature must get right

1. **Emit the view as a group symbol** with the DDM name in its `detail` (e.g. `VIEW OF EMPLOYEES`),
   its selected fields as children.
2. **Resolve each view field's type from the DDM** when the view line omits the format (the common
   case). Do NOT assume the type is on the view line. This is the join that turns a record layout into
   typed logical fields.
3. **Render type+array compactly** (`A20`, `P9,2`, `A20 (1:2)`), reusing the existing verbatim `Type`
   convention.
4. **Model REDEFINE-in-view** as sub-field children (aliases), with `FILLER nX` as an unnamed gap.
5. **Bind `ddm-name` in the DDM namespace** (same steplib/current-library resolution as READ/FIND —
   see calls-and-resolution.md + item 5 below).
6. **Bind each view field name to the DDM field declaration** for go-to-definition (item 5 below).

### Go-to-definition on a view field — verified reasoning

- Go-to-definition on the **view name** → the `VIEW OF` line (its own `DEFINE DATA` symbol). Nothing new.
- Go-to-definition on a **view field name** → the corresponding **DDM field declaration in the `.NSD`**.
  The view field is a *reference to* a DDM field (name-matched), so the authoritative declaration lives
  in the DDM. This is exactly the DDM-binding of feature 27.
- **Namespace/resolution:** `ddm-name` resolves in the **same DDM namespace and steplib chain** as
  `READ`/`FIND`/`GET` and native-SQL `FROM`-tables (current library → declared steplibs in order →
  SYSTEM, non-transitive; longest-prefix current library; flat namespace + ambiguity diagnostic when
  no library map). DDMs are indexed as `.NSD` objects; the field-name match is against the DDM's
  `FileAnalysis.Definitions` (from `ddm.go`). A REDEFINE sub-field defined *in the view* (e.g.
  `BIRTH-YEAR`) has NO DDM declaration — its definition is the view's own REDEFINE line (same-file),
  not the `.NSD`. So: original DDM field name → jump to `.NSD`; view-local redefine sub-field →
  jump to the view's REDEFINE line.

### Terminology note

Natural has **no `OCCURS` keyword** (that is COBOL/PL-I). Natural arrays are declared with **index
ranges** in parentheses: `(lower:upper)`, multi-dimension comma-separated `(1:5,1:10)`, extensible
`(1:*)`. MU/PE in a DDM are Adabas concepts (multiple-value field / periodic group); a view exposes
them with the same index-range notation.

## Cross-check against natls — verified (2026-06-21)

natls's parser (natparse) fully parses the `DEFINE DATA` body including arrays and the REDEFINE
*clause*, with dedicated parser errors for each — corroborating that these are in-scope grammar (not
just our partial-verify guesses):
- Array bounds: `NPP009 INVALID_ARRAY_BOUND`, `NPP010 INCOMPLETE_ARRAY_DEFINITION`,
  `NPP017 ARRAY_DIMENSION_MUST_BE_CONST_OR_INIT`. Unbounded arrays use `*` (real fixture: `(A10/*)`).
- REDEFINE clause: `NPP014 NO_TARGET_VARIABLE_FOR_REDEFINE_FOUND`,
  `NPP015 REDEFINE_LENGTH_EXCEEDS_TARGET_LENGTH`, `NPP022/023/024` (REDEFINE target can't be an X-array
  / dynamic / contain a dynamic). So `REDEFINE` *inside* `DEFINE DATA` is fully supported.
- `FILLER nX` carving: `NPP021 FILLER_MISSING_X`.
- Dynamic length: `NPP004 INVALID_DATA_TYPE_FOR_DYNAMIC_LENGTH`, `NPP008 DYNAMIC_AND_FIXED_LENGTH`.
- Scope checks: `NPP018 BY_VALUE_NOT_ALLOWED_IN_SCOPE`, `NPP019 OPTIONAL_NOT_ALLOWED_IN_SCOPE`,
  `NPP050 INVALID_SCOPE_FOR_FILE_TYPE` (e.g. PARAMETER scope where the file type forbids it).
- `IUsingNode` exposes `isLocalUsing()/isGlobalUsing()/isParameterUsing()` — i.e. `... USING name` is a
  resolvable module reference to the LDA/GDA/PDA, the read/include edge we extract.

**Important "REDEFINE" disambiguation:** natls's `docs/implemented-statements.md` lists `REDEFINE` as a
"reporting-mode-only, not planned" *statement*. That is the standalone reporting-mode `REDEFINE`
statement, NOT the `REDEFINE` clause inside `DEFINE DATA` (which natparse parses fully, per the errors
above). Don't conflate the two.

**Regional decimal separator gotcha:** numeric length specs can use a COMMA as the decimal point
depending on regional settings, e.g. `(N12,7)` = 12 integer + 7 fractional digits (seen in natls
fixtures), equivalent to `(N12.7)`. The parser must accept both `.` and `,` as the decimal separator.
natls notes it currently hardcodes separator assumptions (a known limitation). The parser also
disambiguates the comma in `(1:5,2:5)` (two array dimensions) from a decimal comma.

Source: natls `ParserError.java` and `IUsingNode.java`; see natls-prior-art.md.

## Variable referencing, scope & qualification — verified (2026-07-21)

Grounds go-to-definition / find-references for **variables** (cursor on a usage → its `DEFINE DATA`
declaration). Verified against Software AG Natural programming-guide "Use and Structure of DEFINE DATA"
+ "Qualifying Data Structures" and the System Variables reference.

### Where a referenced variable can be declared (scopes)

| Clause | Declared in… | Cross-file? | In scope for the module |
|--------|--------------|-------------|--------------------------|
| `LOCAL` (inline defs) | same source file | no | yes |
| `LOCAL USING lda` | external LDA (`.NSL`) | **yes** — parse & bind the `.NSL` | yes |
| `PARAMETER` (inline) | same source file | no | yes (the callable interface) |
| `PARAMETER USING pda` | external PDA (`.NSA`) | **yes** — parse & bind the `.NSA` | yes |
| `GLOBAL USING gda` | external GDA (`.NSG`) | **yes** — parse & bind the `.NSG` | yes (shared across the run unit) |
| `INDEPENDENT` (AIVs, `+var`) | same source file | no | yes; AIVs are visible across the whole application run (shared by name, `+` prefix) |
| `CONTEXT` (RPC context vars) | same source file | no | yes; shared across remote subprograms in a conversation |
| `OBJECT` (NaturalX) | same source file | no | yes |

- Inline `LOCAL`/`PARAMETER`/`GLOBAL` defs and `INDEPENDENT`/`CONTEXT`/`OBJECT` are **same-object** —
  the declaration is in the file being edited; a purely intra-file symbol pass resolves them.
- The `... USING name` forms live in a **separate data-area object** (`.NSL`/`.NSA`/`.NSG`) and require
  **cross-file** resolution: parse the referenced data area, expose its fields, bind the usage there.
- **Group fields:** a group (level-1 with sub-levels) contributes ALL its sub-fields to the module's
  namespace — a reference can name the group OR any sub-field directly (no qualification needed if the
  sub-field name is unique). See qualification below.

### What the user expects "the definition" to be

- **Bare reference `#CUSTOMER-NAME`** → the `DEFINE DATA` field line declaring that name (the
  `SelectionRange` on the field's name token). This maps 1:1 to feature-08's `model.DataDefinition` /
  feature-09's `SymbolDataField` tree.
- **Group-qualified `#CUSTOMER.NAME`** (period syntax) → the sub-field `NAME` **within** the group
  `#CUSTOMER` (not some other `NAME`). The qualifier is a level-1 element name; landing target is the
  qualified sub-field's declaration.
- **Array `#TABLE(1:10)` / `#TABLE(I)`** → the declaration of `#TABLE` itself. The subscript/index is a
  *reference expression* (a range, a constant, or another variable used as an index), NOT part of the
  name — strip it before matching. Note `#TABLE(I)` means the index `I` is itself a separate variable
  reference (its own go-to-definition target).
- **REDEFINE field** → REDEFINE sub-fields are **aliases over existing storage**, but each is its own
  named declaration with its own source line. A reference to a redefine sub-field lands on that
  sub-field's `REDEFINE`-block line (feature-08 already models redefine sub-fields as `Children`). The
  original (redefined) field and the redefine sub-fields are DISTINCT names → distinct definitions.

### Qualification rule — verified

> *"To identify a field, you may qualify the field; that is, before the field name, you specify the
> name of the level-1 data element in which the field is located and a period."* — syntax is
> **`group-name.field-name`** with a **period** (`.`). The qualifier **must be a level-1 element**.

Qualification is **required** only when a field name is **not unique** (the same sub-field name appears
in more than one group). Verified example:
```
DEFINE DATA LOCAL
1 FULL-NAME
  2 LAST-NAME  (A20)
  2 FIRST-NAME (A15)
1 OUTPUT-NAME
  2 LAST-NAME  (A20)
  2 FIRST-NAME (A15)
END-DEFINE
...
MOVE FULL-NAME.LAST-NAME TO OUTPUT-NAME.LAST-NAME
```
Here `LAST-NAME` is ambiguous, so `FULL-NAME.LAST-NAME` / `OUTPUT-NAME.LAST-NAME` disambiguate.
**Implication for the analyzer:** a non-unique unqualified reference is genuinely ambiguous → surface
all candidate declarations (mirroring the flat-namespace `Ambiguous` outcome), not a silent pick. A
group-qualified reference binds to the sub-field under the named level-1 group.

### Shadowing / uniqueness

- Two same-kind blocks (e.g. two `LOCAL` sections) can coexist; feature-09 already matches fields to
  their section by **source-range containment**, not by section-kind string.
- There is **no "nearest-declaration"/lexical-shadowing model** as in block-scoped languages — Natural's
  DEFINE DATA namespace is essentially flat per run unit; the disambiguator is **qualification**, not
  scope nesting. A duplicate *unqualified* name that can't be qualified away is a compile-time
  referencing error, so in a **valid** module you can assume at most one declaration per unqualified
  name path (a repeated sub-field name across groups is only legal because it's reachable via the
  distinct group qualifier). Practical rule: resolve unique names directly; for a name appearing in >1
  group, require/honor the `group.` qualifier and otherwise report ambiguity.

### System variables (`*`-prefixed) — EXCLUDE from user-variable navigation

- System variables (`*DATX`, `*DATE`, `*LANGUAGE`, `*OS`, `*ROWCOUNT`, …) are **predefined by Natural**,
  read-only (mostly "Content modifiable: No"), and are **never declared in `DEFINE DATA`**. They have no
  user declaration to jump to.
- **Go-to-definition/references should skip `*`-prefixed names** (there is no user definition). At most,
  a future feature could hover them with the doc description — but they are out of scope for
  user-variable navigation.

### Sigil quick-reference (for the reference scanner)

- `#name` — conventional user-variable prefix (not mandatory, but idiomatic; a plain `A`–`Z` start is
  also a valid user variable).
- `+name` — AIV (INDEPENDENT) / GDA variable prefix; first-char-only.
- `&name` — dynamic source substitution / dynamically-replaceable — treat a `&`-bearing target as
  dynamic (already the rule for call targets); as a *data* name it's a runtime-substituted identifier.
- `*name` — **system variable**, predefined, no user declaration → exclude (above).
- Names are **case-insensitive** (internally upper-cased); match declarations case-insensitively.

## PARAMETER data and callable interface

- A subprogram's / external subroutine's callable signature is its `DEFINE DATA PARAMETER` block (or a
  `PARAMETER USING pda` referencing a `.NSA`). This is what `CALLNAT`/`PERFORM` parameters bind to —
  useful for hover/signature features and for validating parameter counts.
- Attributes `BY VALUE` / `BY VALUE RESULT` (vs default by reference) appear on parameter definitions
  and correspond to `AD=` on the call site.
- **PARAMETER scope is documented only for subprogram / external subroutine / helproutine / function**
  (defineda_pda.htm). It is bound positionally by `CALLNAT` / external `PERFORM` / function-call.
  **FETCH/RUN do NOT bind a PARAMETER block** — a program receives FETCH/RUN data as untyped
  positional values on the Natural stack, read by `INPUT` statements. So signature help / a declared
  parameter interface applies to CALLNAT / external PERFORM / function-calls, NOT to FETCH/RUN. See
  calls-and-resolution.md "FETCH/RUN parameter passing — NO declared interface" (verified 2026-07-13).

## Sources

- DEFINE DATA general / clauses / END-DEFINE:
  https://documentation.softwareag.com/natural/nat911mf/sm/defineda_basic.htm
- DEFINE DATA (statement page): https://documentation.softwareag.com/natural/nat912unx/sm/defineda.htm
- Array Dimension Definition (inline syntax `(A10/1:5)`):
  https://documentation.softwareag.com/natux/9.3.3/en/webhelp/natux-webhelp/sm/defineda_array.htm
- CONTEXT variables: https://documentation.softwareag.com/natural/nat827mf/sm/defineda_cv.htm
- Format codes / packed limits (Natural & Adabas field defs):
  https://documentation.softwareag.com/natural/nsn828/ug/fields9.htm
- natls parser errors (array/REDEFINE/scope grammar):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/ParserError.java
- Qualifying data structures (group.field period rule, level-1 qualifier, FULL-NAME.LAST-NAME example)
  + Use and Structure of DEFINE DATA (REDEFINE same-level rule, LOCAL USING):
  https://documentation.softwareag.com/natural/nat911mf/pg/pg_defi.htm
- Arrays / index notation for multiple-value fields & periodic groups:
  https://documentation.softwareag.com/natural/nat841unx/pg/pg_defi_array.htm ,
  https://documentation.softwareag.com/natural/nat426mf2/pg/pg_output_index.htm
- System variables predefined & read-only ("Content modifiable: No"):
  https://documentation.softwareag.com/natural/nat828mf/vari/sysenv.htm
- View Definition (VIEW OF ddm-name; field selection; format/length inherited-from-DDM /
  must-match-in-structured-mode rule; EMP1/EMP2/EMP3 example; array notation):
  https://documentation.softwareag.com/natural/nat912win/sm/defineda_view.htm
- Redefinition (REDEFINE clause; can redefine group/view/DDM-field/field; FILLER nX; level rules;
  BIRTH → BIRTH-YEAR/MONTH/DAY view example; parameter/handle/X-array/dynamic restrictions):
  https://documentation.softwareag.com/natural/nat914unx/sm/defineda_redef.htm
- DDM = "logical view of a physical database file"; ddm-name vs view-name distinction:
  https://documentation.softwareag.com/natural/nat828mf/pg/pg_obj_ddm.htm
- DDM generation from DB2 tables/views via SYSDDM SQL Services (NSB); DB2 data types supported:
  https://documentation.softwareag.com/natural/nat828mf/dbms/ndb-ddm.htm ,
  https://documentation.softwareag.com/natural/nat827mf/dbms/nsb-ddm.htm