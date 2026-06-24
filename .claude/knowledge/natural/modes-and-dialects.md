# Modes & Dialects

Why the same construct can have different syntax. Always tag a recorded fact with the dialect/mode/
version it applies to.

**Status: verified (2026-06-23)** — structured vs reporting differences confirmed against the official
"Natural Programming Modes" page. Column rules confirmed as free-format (NaturalONE is Eclipse-based);
reporting-mode grammar (DO/DOEND, loop-collapsing END/LOOP) confirmed.

## Axes that change syntax/behavior

- **Platform/dialect:** Natural for Mainframes (z/OS, z/VSE, BS2000) vs Natural for Linux/Unix/Windows
  (incl. NaturalONE/SPoD). Statement *grammar* for calls/data is largely shared; environment, file
  handling, GUI (dialogs), and some system commands differ. This analyzer is filesystem-scoped to
  NaturalONE/SPoD `.NSx` exports, so target that dialect first.
- **Programming mode:** *structured mode* vs *reporting mode*.
- **Version:** features vary across versions (6.x → 8.x → 9.x). Extension/object-type mapping is stable
  across recent versions.

## Structured mode vs reporting mode — verified (2026-06-23)

These differences directly affect line-oriented parsing:

| Aspect | Structured mode | Reporting mode |
|--------|-----------------|----------------|
| Block closing | EVERY loop/logical construct closed by an explicit `END-...`: `END-IF`, `END-READ`, `END-FIND`, `END-FOR`, `END-REPEAT`, `END-SORT`, `END-WHILE`, `END-SUBROUTINE`, `END-DEFINE`, etc. | Uses `DO ... DOEND` blocks and `(CLOSE) LOOP`; a single `END` / `LOOP` can close MULTIPLE active loops. `END-IF`/`END-READ`/`END-REPEAT` cause **ERRORS**. |
| Data definition | All data must be defined centrally (`DEFINE DATA` at top, or external data area). | Database fields usable without defining them; user variables may be declared ANYWHERE in the program. |
| DDM/field reference | Must appear in `DEFINE DATA`. | May reference DDMs/fields directly without prior definition. |
| Intended use | Complex, well-structured applications. | Ad-hoc reports / small programs. |

**Reporting-mode grammar details:**
- `DO ... DOEND` for multi-statement blocks
- Single `END` or `LOOP` can close multiple nested loops (loop-collapsing)
- `LOOP (r)` syntax closes loops up to a labeled statement
- `END-IF`, `END-READ`, `END-REPEAT` cause errors in reporting mode

**Sources:**
- Natural Programming Modes: https://documentation.softwareag.com/natural/nat841unx/pg/pg_mode.htm
- LOOP statement (reporting mode only): https://documentation.softwareag.com/natural/nat921unx/webhelp/natux-webhelp/sm/loop.htm

Analyzer implications:
- In structured mode, block nesting is explicit and reliably matchable via `END-...` tokens — good for
  scoping inline `DEFINE SUBROUTINE ... END-SUBROUTINE` (needed for PERFORM resolution).
- In reporting mode, `DO/DOEND` and loop-collapsing `END`/`LOOP` make block scope ambiguous to a naive
  the parser; an unmatched `END-IF`-style token actually signals reporting mode (or an error), not a
  parse failure — relevant for how the parser handles block scope in reporting mode.
- Variables can appear undeclared in reporting mode, so "undefined symbol" diagnostics must be
  mode-aware to avoid false positives.

## Case sensitivity — verified context

Natural keywords and identifiers are case-INSENSITIVE; extraction and cross-file resolution must
normalize case. EXCEPTION: `FETCH`/`RUN` program names — "the case of the specified name is NOT
translated" — so a fetched program name may be case-sensitive at runtime. For static resolution against
`.NSx` files (whose object names are conventionally upper-case), normalizing is still the safe default,
but record this as a known subtlety.

## Programming mode is DECLARED in the NaturalONE source header — verified

NaturalONE-exported `.NSx` source files begin with a machine-written header comment block that states
the programming mode explicitly, so the analyzer should READ the mode, not infer it from `END-IF`
presence:

```
* >Natural Source Header 000000
* :Mode S            <- S = structured, R = reporting
* :CP                <- code page
* :LineIncrement 10  <- editor line-number increment
* <Natural Source Header
```

- Delimited by `* >Natural Source Header` … `* <Natural Source Header`.
- `* :Mode S` → structured mode; `* :Mode R` → reporting mode. If the header (or `:Mode` line) is
  absent — hand-written or non-exported source — mode is UNKNOWN; structured is the safe default.
- DDM (`.NSD`) files carry an indented variant (`*      >Natural Source Header`).
- Confirmed from natls's lexer (`Lexer.consumeNaturalHeader`) and real exported fixtures. See
  natls-prior-art.md. Source:
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/lexing/Lexer.java

## Comment markers — verified (cross-checked against natls lexer)

- **Full-line comment:** `*` as the first non-blank character of a line, when the next character is a
  space, tab, `/`, `*`, or end-of-line. (The "next char" guard prevents eating an operand/label that
  legitimately starts with `*`.)
- **Inline comment:** `/*` anywhere on a line → comment to end of line.
- Gotcha: `/` is ambiguous between an inline comment (`/*`) and an array-bound separator inside a
  format spec like `(A10/1:5)`. The lexer must not treat the `/` in array bounds as a comment start.
  Source (natls Lexer `isSingleAsteriskComment` / `isInlineComment`): same URL as above.

## Column / continuation rules — verified (2026-06-23)

- Natural source is line-oriented; statements CAN span multiple lines (operands continue on following
  lines — the parser must support multi-line statements. The `INCLUDE` statement is an exception: it
  must be the only statement on its line.
- **NaturalONE uses free-format syntax** (Eclipse-based editor). No fixed-format column rules.
  Indentation corresponds to the `STRUCT` command. Multi-line continuation is supported but not
  column-dependent.
- **Mainframe Natural** (z/OS, BS2000) may use fixed-format columns; this analyzer targets NaturalONE/SPoD.

**Sources:**
- NaturalONE Source Editor: https://documentation.softwareag.com/naturalONE/natONE914/core/using/use-edis-source.htm

## Sources

- Natural Programming Modes (structured vs reporting):
  https://documentation.softwareag.com/natural/nat841unx/pg/pg_mode.htm
- DEFINE DATA basic rules (END-DEFINE, mode availability):
  https://documentation.softwareag.com/natural/nat911mf/sm/defineda_basic.htm
- FETCH (name case not translated):
  https://documentation.softwareag.com/natural/nat911mf/sm/fetch.htm