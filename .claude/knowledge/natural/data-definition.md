# Data Definition

`DEFINE DATA` and the data-area object types.
Dialect/mode: verified against Natural for Linux/Unix/Windows + Mainframe references. `DEFINE DATA` is
available in BOTH structured and reporting mode.

**Status: verified (2026-06-20)** — section clauses, terminator, and format codes confirmed against
official Software AG documentation. Detailed array/REDEFINE grammar partially verified (see below).

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
  multi-dimensional `(1:5,1:3)`. *Status: array bound syntax recorded from the syntax overview but the
  exact placement variants (format-then-bounds vs separate) should be re-confirmed on a parsing task.*
- **REDEFINE:** `level REDEFINE field` introduces an alternate layout over an already-defined field;
  sub-levels carve up the storage. The analyzer should treat REDEFINE sub-fields as aliases, not new
  storage.
- **VIEW (DDM access):** `level name VIEW OF ddm-name` followed by the DDM fields to use; this is a
  read edge to a DDM (`.NSD`).

## PARAMETER data and callable interface

- A subprogram's / external subroutine's callable signature is its `DEFINE DATA PARAMETER` block (or a
  `PARAMETER USING pda` referencing a `.NSA`). This is what `CALLNAT`/`PERFORM` parameters bind to —
  useful for hover/signature features and for validating parameter counts.
- Attributes `BY VALUE` / `BY VALUE RESULT` (vs default by reference) appear on parameter definitions
  and correspond to `AD=` on the call site.

## Sources

- DEFINE DATA general / clauses / END-DEFINE:
  https://documentation.softwareag.com/natural/nat911mf/sm/defineda_basic.htm
- DEFINE DATA (statement page): https://documentation.softwareag.com/natural/nat912unx/sm/defineda.htm
- Syntax Overview: https://documentation.softwareag.com/natural/nat6312unx/sm/defineda_synt.htm
- CONTEXT variables: https://documentation.softwareag.com/natural/nat827mf/sm/defineda_cv.htm
- Format codes / packed limits (Natural & Adabas field defs):
  https://documentation.softwareag.com/natural/nsn828/ug/fields9.htm