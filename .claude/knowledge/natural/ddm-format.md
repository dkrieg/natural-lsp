# DDM (`.NSD`) exported file format

The on-disk representation of a Data Definition Module as exported by NaturalONE / SPoD. **A `.NSD`
file is NOT Natural source** — it is a tabular, **fixed-column** field listing (the "DDM report"
layout), so the statement lexer/parser must NOT run over it. It needs a separate line-scanner.

**Status: verified (2026-07-06)** — column offsets confirmed against (a) the natls DDM parser
(`parsing/ddm/FieldParser.java`, MIT reference implementation, byte-exact column indices) and its real
exported fixtures (`CompleteDdm.NSD`), and (b) the official Software AG DDM Editor column reference.
Dialect: NaturalONE / Natural for Linux-Unix-Windows local files; the report layout is dialect-stable.

## File anatomy

```
DB: 000 FILE: 100  - CUSTOMER                          DEFAULT SEQUENCE:
TYPE: ADABAS
<blank line>
T L DB Name                              F Leng  S D Remark
- - -- --------------------------------  - ----  - - ------------------------
  1 AA CUSTOMER-ID                       N    8  N U
G 1 AC ADDRESS
  2 AD STREET                            A   40  N
...
******DDM OUTPUT TERMINATED******
```

1. **Header line** — starts with `DB:`. Grammar (natls regex):
   `DB:\s+(\d+).*FILE:\s+(\d+).*-\s+(\S+).*DEFAULT SEQUENCE:\s?([\w\s$]*)`
   → database number, file number, DDM name, default read sequence (by DB short name; may be empty).
2. **`TYPE:` line** (optional) — `TYPE: ADABAS` | `TYPE: SQL` | (DL/I …). Selects Adabas vs SQL field
   parser. If absent, treat as Adabas.
3. **Blank line(s)** — skipped.
4. **Column-header line** `T L DB Name … F Leng  S D Remark` and the dashed **separator line**
   `- - -- --- …` — both skipped (matched by prefix). Header prefix to skip: `T L DB Name`.
5. **Field rows** — the data. One physical line per field. Fixed-column (see below).
6. **Comment / metadata lines** start with `*` (Predict comments, the NaturalONE Source Header block
   `* >Natural Source Header … * <Natural Source Header`, `* :CP`, generation timestamps) — skipped,
   EXCEPT that the `*      … SOURCE FIELD(S) …` block and the `*      NAME (from-to)` lines that
   follow a superdescriptor row are consumed as that descriptor's source-field references (see below).
7. **Terminator** `******DDM OUTPUT TERMINATED******` and other Predict noise
   (`DDM OUTPUT TERMINATED`, `Cataloged by`, `EM=`, `HD=`, `CODEPAGE:`, `:CP`) — skipped.

## Field row — EXACT fixed-column layout (0-based byte offsets)

These are the substring offsets used by the reference parser (`FieldParser.java`). The format is
**positional, not whitespace-delimited** — parse by column, do not tokenize on spaces.

| Field       | Offset | Len | Notes |
|-------------|--------|-----|-------|
| **T** (type)| 0      | 1   | ` `=elementary, `G`=group, `P`=periodic group (PE), `M`=multiple-value (MU), `C`=coupled, `*`=comment line |
| (space)     | 1      | 1   | |
| **L** (level)| 2     | 1   | level number 1–7 (single digit in this layout) |
| (space)     | 3      | 1   | |
| **DB** (short name)| 4 | 2 | 2-char Adabas short name (e.g. `AA`); `.trim()`ed |
| (space)     | 6      | 1   | |
| **Name**    | 7      | 32  | long field name (3–32 chars for Adabas/SQL); `.trim()`ed |
| (spaces)    | 39–40  | 2   | |
| **F** (format)| 41   | 1   | `A U N P I B F L D T C` … Natural format code |
| (space)     | 42     | 1   | |
| **Leng**    | 43     | 4   | right-justified; may hold a comma decimal, e.g. `9,2` or `12,7` (regional decimal); `DYNAMIC` won't fit 4 cols and appears as a keyword — treat non-numeric length verbatim |
| (spaces)    | 47–48  | 2   | |
| **S** (suppression)| 49 | 1 | ` `=standard, `N`=null-value suppression, `F`=fixed storage, `M`=SQL NOT NULL |
| (space)     | 50     | 1   | |
| **D** (descriptor)| 51 | 1 | ` `=none, `D`=descriptor, `S`=superdescriptor, `U`=subdescriptor(*)/unique, `P`=phonetic, `H`=hyperdescriptor, `N`=non-descriptor |
| (space)     | 52     | 1   | |
| **Remark**  | 53+    | —   | rest of line; `.trim()`ed; free comment/attributes |

(*) natls maps `U`→UNIQUE in its `DescriptorType` enum; the Software AG DDM-editor doc labels the
subdescriptor line command `U`. In practice `U` on a field row is a **unique** descriptor; a true
*subdescriptor* is a superdescriptor (`S`) whose source-field range references DON'T resolve to
existing fields (see below). Don't over-interpret `U` for hover — show it verbatim.

**Trailing-whitespace tolerance:** exported DDMs are saved WITHOUT trailing spaces, so a row may be
shorter than 53 chars (e.g. a bare group row like `G 1 AC ADDRESS` ends at the name). The parser must
pad short lines: if a column's start offset is past end-of-line, treat it as blank. So a group/PE row
with no format/length is legal and common.

## Group / PE / MU

- **Group** (`T=G`): a header row; child rows follow with a **higher L (level) number**. The group
  row has NO format/length (blank). Grouping is by level containment, exactly like `DEFINE DATA`:
  children have level > the group's level; the group ends when a row with level ≤ the group's level
  appears. Groups nest (a `G` child inside a group).
- **Periodic group** (`T=P`): same as a group structurally (header + higher-level children), but the
  whole group repeats. The occurrence count is NOT in the report columns.
- **Multiple-value field** (`T=M`): an elementary field that repeats (an array of scalars). It has a
  format/length like any elementary field; the `M` in column T is the only array signal.
- **CRITICAL for arrays:** the DDM report gives NO numeric occurrence bounds for MU/PE. You know a
  field is an array (`M`/`P`) but not how many occurrences. This differs from `DEFINE DATA` where
  bounds are explicit (`(A10/1:5)`).

## Superdescriptors / subdescriptors / phonetic

A **superdescriptor** row has `D=S` and its own format/length (the concatenated key), then is
IMMEDIATELY followed by a Predict comment block naming the source fields:

```
  1 AI NAME-CITY-SUPER                   A   80  N S
*      -------- SOURCE FIELD(S) -------
*      CUSTOMER-NAME (1-50)
*      CITY          (1-30)
```

- Source-field lines: `*\s*(NAME)\((from)-(to)\)` — field name plus a byte-range slice of that field
  contributing to the key. The `------- SOURCE FIELD(S) -------` line is a separator.
- **Sub- vs super- disambiguation (natls logic):** if EVERY named source field resolves to an
  existing field in the DDM → it's a true **superdescriptor** (multi-field composite). If a source
  name does NOT resolve → natls reclassifies it as a **subdescriptor**: each `(from-to)` slice becomes
  its own derived field of length `to-from+1`, and the header row is demoted to a plain elementary
  field. For hover you can keep it simpler: show the descriptor kind + the source-field list.
- **Phonetic** (`D=P`), **hyperdescriptor** (`D=H`), **non-descriptor** (`D=N`), **unique** (`D=U`)
  are single-column flags with no source-field block (phonetic references one field but is emitted as
  a plain descriptor row here).

## What the analyzer should extract (map onto `model.DataDefinition`)

Reuse the feature-08 `model.DataDefinition{Name, Level, Type, Dimensions, SectionKind, Range, Children}`
tree — a DDM maps onto it cleanly for hover:

| DDM report column | DataDefinition field | Value |
|-------------------|----------------------|-------|
| Name              | `Name`               | upper-case long name (matches convention) |
| L                 | `Level`              | the level int |
| F + Leng          | `Type`               | verbatim `"<F><Leng>"`, e.g. `"N8"`, `"A50"`, `"P9,2"` (or normalize comma→dot: `"P9.2"`) |
| T=G / T=P         | (group)              | empty `Type`, children via `Children` by level containment |
| T=M / T=P         | `Dimensions`         | *see gap below* |
| —                 | `SectionKind`        | set to a constant like `"ddm"` (or leave empty) — no DEFINE-DATA section applies |
| whole line span   | `Range`              | 1-based, byte-column, inclusive-end per the model convention |
| group children    | `Children`           | nested by level, same as REDEFINE/group nesting |

- **`Type`**: `"N8"`, `"A50"`, `"P9,2"`. This is exactly the verbatim-format-string contract already
  used for `DEFINE DATA` fields. A group has empty `Type` (consistent with the model note "Empty for
  group headers"). Good fit.
- **Groups nest via `Children` + `Level`**: identical to the DEFINE-DATA group/REDEFINE nesting the
  model already supports. Good fit.

### Does a `model.DataDefinition` change / cache-format bump apply? — NO (recommended)

`DataDefinition` covers Name/Level/Type/Children with no change. The two DDM-only facts that don't
have a home are:

1. **DB short name** (`AA`, `AB`, …) — there is no field for it. It is NOT needed for hover of
   field name/type, and is not part of the Natural-program-facing view (programs use the long name).
   **Recommendation: drop it** for hover. If a later feature (e.g. SQL/Adabas short-name mapping)
   needs it, add it then.
2. **MU/PE array-ness with unknown bounds** — `Dimensions []ArrayDimension` exists, but an
   `ArrayDimension` carries numeric bounds the DDM report doesn't provide. Options that need NO model
   change: (a) emit a single `ArrayDimension` with an unbounded/`*` upper (mirrors `(A10/1:*)` extensible
   arrays — the analyzer already handles `*` bounds) to signal "this is an array, count unknown"; or
   (b) encode it in `Type` verbatim (e.g. `"A20 (MU)"`). Either avoids a model change. Only if you want
   a *typed* MU/PE flag distinct from a real bounded array would you add a bool — not required for hover.
3. **Descriptor kind (D/S/U/P/H/N) and suppression (S)** — not representable, and not needed for the
   stated hover goal (field name + type). If hover should annotate "descriptor", the lowest-cost path
   is appending to the verbatim `Type`/a remark string rather than a new typed field. **Not required.**

**Verdict: no `model` change and no cache-format bump are needed** to ship DDM field-name/type hover.
Represent MU/PE as an unbounded `Dimensions` entry (reusing the existing `*`-bound support) and drop
DB short name + descriptor flags for now. A model change becomes *unavoidable* only if a future
feature must expose the DB short name or a typed descriptor classification — flag that as a separate,
deliberate decision, not a hover prerequisite.

## Parsing notes (for the implementer)

- **Line-oriented, fixed-column** — NOT whitespace-delimited and NOT token-parseable. Write a small
  dedicated line-scanner keyed on the byte offsets in the table above; do NOT route `.NSD` through the
  Natural lexer/recursive-descent parser (which is for statement source). natls does exactly this
  (separate `parsing/ddm/` package).
- **Robust minimal algorithm:**
  1. Split on `[\r\n]+`.
  2. Skip: blank lines; lines starting with `*` (comments/header block) — but capture the
     `SOURCE FIELD(S)` block if you implement superdescriptors; the known Predict skip-prefixes
     (`T L DB Name`, `- - --`, `DDM OUTPUT TERMINATED`, `Cataloged by`, `EM=`, `HD=`, `CODEPAGE:`,
     `:CP`, `SOURCE FIELD(S)`).
  3. `DB:` line → header metadata. `TYPE:` line → Adabas vs SQL.
  4. Otherwise it's a field row: slice by column offsets, `.trim()` each, pad short lines.
  5. Build the tree by level containment (group/PE if `T∈{G,P}`).
- **Comma-in-length**: `9,2`/`12,7` — regional decimal separator; normalize `,`→`.` if you want a
  canonical `Type`, or keep verbatim.
- **SQL DDMs** (`TYPE: SQL`) use a DIFFERENT field layout (natls has a separate `SqlFieldParser` with
  its own column offsets, and SQL-type/length rows). If you only target Adabas DDMs for hover, gate on
  `TYPE: ADABAS`/absent and treat SQL DDMs as out of scope for now (don't misparse them with Adabas
  offsets). Verify the SQL offsets separately before supporting them.
- **The `.7` / `.NSD` question**: there is no `.7` DDM variant. `.NSD` is the single DDM local-file
  extension (see file-extensions.md); the digit-suffixed types are `.NS3/.NS4/.NS7/.NS8` (dialog/
  class/function/adapter), none of which are DDMs.
- **Redefinitions**: the Adabas DDM report has no `REDEFINE` construct (that's a `DEFINE DATA`/view
  feature). REDEFINEs appear when a program declares a `VIEW OF ddm`, not in the DDM itself.
- **Superdescriptor source ranges** parse with `\*\s*(NAME)\((\d+)-(\d+)\)`.

## Minimal fixture (byte-correct; lives at `internal/server/testdata/hover/customer.NSD`)

Exercises: two scalars of different formats (N8 unique-descriptor id, A50 descriptor name), a group
(`ADDRESS`) with elementary children, a packed decimal (`P9,2`), a multiple-value field (`M`, PHONE),
and a superdescriptor with a source-field block. Column offsets match the table above exactly.

```
DB: 000 FILE: 100  - CUSTOMER                          DEFAULT SEQUENCE:
TYPE: ADABAS

T L DB Name                              F Leng  S D Remark
- - -- --------------------------------  - ----  - - ------------------------
  1 AA CUSTOMER-ID                       N    8  N U
  1 AB CUSTOMER-NAME                     A   50  N D
G 1 AC ADDRESS
  2 AD STREET                            A   40  N
  2 AE CITY                              A   30  N D
  2 AF ZIP-CODE                          A   10  N D
  1 AG BALANCE                           P  9,2  N
M 1 AH PHONE                             A   20  N
  1 AI NAME-CITY-SUPER                   A   80  N S
*      -------- SOURCE FIELD(S) -------
*      CUSTOMER-NAME (1-50)
*      CITY          (1-30)
******DDM OUTPUT TERMINATED******
```

## Sources

- natls `FieldParser.java` (exact 0-based column offsets: T=0, L=2, DB=4, Name=7 len32, F=41,
  Leng=43 len4, S=49, D=51, Remark=53; short-line padding; comma→dot length):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/ddm/FieldParser.java
- natls `DdmParser.java` (line-skip list, DB:/TYPE: handling, group-by-level, superdescriptor block):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/ddm/DdmParser.java
- natls `DdmMetadataParser.java` (header regex): 
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/ddm/DdmMetadataParser.java
- natls `FieldType`/`DescriptorType`/`NullValueSuppression` enums (T/D/S column value meanings):
  https://github.com/MarkusAmshove/natls/tree/main/libs/natparse/src/main/java/org/amshove/natparse/natural/ddm
- natls real exported fixture `CompleteDdm.NSD` (byte-exact reference layout):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/test/resources/org/amshove/natparse/parsing/ddm/CompleteDdm.NSD
- Software AG "Using the DDM Editor" (T/L/DB/Name/F/Leng/S/D/Remark column meanings, header
  DBID/FNR/Def.Seq., descriptor and suppression codes):
  https://documentation.softwareag.com/natural/nat827mf/edis/ddm_use_editor.htm
- Software AG "Data Definition Module (DDM)" concept (short name vs external name, formats/lengths):
  https://documentation.softwareag.com/natural/nat828mf/pg/pg_obj_ddm.htm
