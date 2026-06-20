# Modes & Dialects

Why the same construct can have different syntax. Always tag a recorded fact with the dialect/mode/
version it applies to.

**Status: verified (2026-06-20)** — structured vs reporting differences confirmed against the official
"Natural Programming Modes" page. Column/continuation rules partially verified (see open item).

## Axes that change syntax/behavior

- **Platform/dialect:** Natural for Mainframes (z/OS, z/VSE, BS2000) vs Natural for Linux/Unix/Windows
  (incl. NaturalONE/SPoD). Statement *grammar* for calls/data is largely shared; environment, file
  handling, GUI (dialogs), and some system commands differ. This analyzer is filesystem-scoped to
  NaturalONE/SPoD `.NSx` exports, so target that dialect first.
- **Programming mode:** *structured mode* vs *reporting mode*.
- **Version:** features vary across versions (6.x → 8.x → 9.x). Extension/object-type mapping is stable
  across recent versions.

## Structured mode vs reporting mode — verified

These differences directly affect line-oriented parsing:

| Aspect | Structured mode | Reporting mode |
|--------|-----------------|----------------|
| Block closing | EVERY loop/logical construct closed by an explicit `END-...`: `END-IF`, `END-READ`, `END-FIND`, `END-FOR`, `END-REPEAT`, `END-SORT`, `END-WHILE`, `END-SUBROUTINE`, `END-DEFINE`, etc. | Uses `DO ... DOEND` blocks and `(CLOSE) LOOP`; a single `END` / `LOOP` can close MULTIPLE active loops. `END-IF`/`END-READ`/`END-REPEAT` cause ERRORS. |
| Data definition | All data must be defined centrally (`DEFINE DATA` at top, or external data area). | Database fields usable without defining them; user variables may be declared ANYWHERE in the program. |
| DDM/field reference | Must appear in `DEFINE DATA`. | May reference DDMs/fields directly without prior definition. |
| Intended use | Complex, well-structured applications. | Ad-hoc reports / small programs. |

Analyzer implications:
- In structured mode, block nesting is explicit and reliably matchable via `END-...` tokens — good for
  scoping inline `DEFINE SUBROUTINE ... END-SUBROUTINE` (needed for PERFORM resolution).
- In reporting mode, `DO/DOEND` and loop-collapsing `END`/`LOOP` make block scope ambiguous to a naive
  regex; an unmatched `END-IF`-style token actually signals reporting mode (or an error), not a parser
  miss — relevant for the "unrecognized syntax → diagnostic" policy.
- Variables can appear undeclared in reporting mode, so "undefined symbol" diagnostics must be
  mode-aware to avoid false positives.

## Case sensitivity — verified context

Natural keywords and identifiers are case-INSENSITIVE; extraction and cross-file resolution must
normalize case. EXCEPTION: `FETCH`/`RUN` program names — "the case of the specified name is NOT
translated" — so a fetched program name may be case-sensitive at runtime. For static resolution against
`.NSx` files (whose object names are conventionally upper-case), normalizing is still the safe default,
but record this as a known subtlety.

## Column / continuation rules — partially verified

- Natural source is line-oriented; statements CAN span multiple lines (operands continue on following
  lines), which stresses line-oriented regex. The `INCLUDE` statement is an exception: it must be the
  only statement on its line.
- Exact fixed-format column sensitivity (e.g. label/structured-indentation rules in the mainframe
  editor) was NOT fully confirmed in this pass. **Status: unverified** for precise column rules — do
  not assume fixed columns for the NaturalONE/free-format source; confirm before encoding column
  positions into regex.

## Sources

- Natural Programming Modes (structured vs reporting):
  https://documentation.softwareag.com/natural/nat841unx/pg/pg_mode.htm
- DEFINE DATA basic rules (END-DEFINE, mode availability):
  https://documentation.softwareag.com/natural/nat911mf/sm/defineda_basic.htm
- FETCH (name case not translated):
  https://documentation.softwareag.com/natural/nat911mf/sm/fetch.htm