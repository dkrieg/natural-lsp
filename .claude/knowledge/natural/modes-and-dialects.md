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

## Comment markers — verified (2026-06-29, official Software AG doc + natls lexer)

Authoritatively confirmed against the **Natural Programming Guide → "User Comments"** page. There are
exactly two comment forms and BOTH are REST-OF-LINE; Natural has **NO C-style delimited `/* ... */`
block comment**. There is no closing `*/` delimiter and comments NEVER span physical lines.

**Verbatim from the doc:**
- Full-line comment: *"If you wish to use an entire source-code line for a user comment, you enter one
  of the following at the beginning of the line: an asterisk and a blank (`* `), two asterisks (`**`),
  or a slash and an asterisk (`/*`)."*
- Inline (latter-part-of-line) comment: *"If you wish to use only the latter part of a source-code line
  for a user comment, you enter a blank, a slash and an asterisk (`/*`); the remainder of the line
  after this notation is thus marked as a comment."*

### (1) `/*` is REST-OF-LINE, NOT a delimited block — DECISION-CRITICAL
- `/*` (anywhere on the line, but in practice preceded by a blank when trailing code) marks **the
  remainder of the physical line** as a comment. There is **no `*/` closer**. A `*/` appearing later on
  the line is just more comment text — code does NOT resume after it.
- Therefore a line like `MOVE 1 TO #VAR /* comment with /* inside */ ends here` is: code `MOVE 1 TO
  #VAR`, then a comment running `/* comment with /* inside */ ends here` to EOL. The trailing
  `*/ ends here` is **comment**, NOT code. A naive C-style `/* ... */` lexer would get this wrong.
- The inner `/*` does not nest and the `*/` does not close — there is nothing to nest or close.

### (2) Leading `*` — line-start guard
- `*` (or `**`) starts a full-line comment **only at the beginning of the line** (first non-blank).
  natls's lexer guard: `*` is a line comment only when the NEXT char is one of: space, tab, `/`, `*`,
  newline/CR, or EOF (`isSingleAsteriskComment`). The guard prevents eating an operand/label that
  legitimately starts with `*` (e.g. a system variable like `*OCC`, `*DATX`).
- A **mid-line `*`** is the **multiplication operator**, never a comment. In `COMPUTE #A = #B * #C`
  the `*` is multiplication. The comment interpretation is triggered ONLY by line-start position (for
  `*`/`**`) or by the two-char `/*` sequence (anywhere).

### (3) Multi-line
- Comments NEVER span lines. Each comment line needs its own `*`/`**`/`/*`; each trailing comment ends
  at its physical line end. (natls `consumeComment`: advance to `isLineEnd()`, no multi-line path.)

### Gotchas for the lexer
- `/` alone is the division operator / array-bound separator. Only the two-char `/*` is a comment
  start. Inside a format/array spec like `(A10/1:5)` the `/` is a bound separator — but note `(A10/*...`
  would still begin a comment at `/*`; in practice array bounds use `/1:n`, not `/*`. The lexer must
  scan for the exact `/*` digraph, and must NOT treat a lone `/` as a comment.
- `END-SUBROUTINE/*` = keyword `END-SUBROUTINE` immediately followed by an inline comment (no blank
  required between code and `/*`, though the doc shows a leading blank).

**Sources:**
- Natural Programming Guide, "User Comments": https://documentation.softwareag.com/natural/nat827mf/pg/pg_furth_ucom.htm
- natls Lexer (`isSingleAsteriskComment` / `consumeComment`):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/lexing/Lexer.java

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