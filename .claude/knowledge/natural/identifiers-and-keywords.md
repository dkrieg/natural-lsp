# Identifiers & Keywords (context-sensitivity)

How Natural names user-defined identifiers (variables, **subroutine names**), and whether a word that is
also a Natural keyword may be used as a name. Directly grounds the `DEFINE SUBROUTINE <name>` /
`PERFORM <name>` parser positions (issue #41).

Dialect note: naming rules verified against Natural for Mainframes (nat827mf) and NaturalONE
(natONE912) statement/using references; the rule is stable across mainframe and Linux/Unix/Windows.
Mode: applies to both structured and reporting mode.

**Status: verified (2026-07-20)** — Software AG "Rules and Naming Conventions", "Natural Reserved
Keywords" (KCHECK), and "DEFINE SUBROUTINE" docs, cross-checked against natls source.

## Identifier naming rules (user-defined variables AND subroutine names) — verified

- **Length:** 1 to 32 characters; only the first 32 are significant.
- **First character** must be one of: an upper-case letter `A`–`Z`, `#`, `+`, or `&`. If the first
  character is `#`, `+`, or `&`, the name must have **at least one more character**.
- **Subsequent characters:** letters (upper/lower — lower is internally upper-cased subject to the
  `LOWSRCE` option), digits `0`–`9`, and the special characters `-` (hyphen), `_` (underscore), `/`,
  `@`, `$`, `&`, `#`, `+`.
- **`+` prefix** is only valid as the first character AND only for application-independent variables
  (AIVs) and GDA variables. **`&`** is used for dynamic source substitution / dynamically-replaceable
  characters. (Matches our lexer's `#`/`&`/`+`-prefix + embedded-hyphen identifier handling.)
- **Case-insensitive:** names are internally upper-cased; a keyword-spelled name compares
  case-insensitively (`clear` == `CLEAR`).
- **Subroutine name = a user-defined variable name.** The DEFINE SUBROUTINE doc: *"For a subroutine
  name (maximum 32 characters), the same naming conventions apply as for user-defined variables"*, and
  *"The subroutine name is independent of the name of the module in which the subroutine is defined."*
  So `DEFINE SUBROUTINE <name>` and `PERFORM <name>` both take a plain **identifier** (up to 32 chars),
  by the rules above — NOT an alphanumeric string constant. (Contrast CALLNAT/FETCH, whose target is a
  string *constant* or a variable.) The name may also be a variable (dynamic PERFORM), but the common
  inline case is a bare identifier token.

## Are keywords reserved? — NUANCED, and this is the crux

Natural keywords are **context-sensitive, not hard-reserved.** There is **no absolute compiler
prohibition** on spelling an identifier the same as a keyword; the docs give a *recommendation* plus a
narrower *ambiguity* concern:

- **General rule (recommendation, not prohibition):** *"To avoid any naming conflicts, you are strongly
  recommended not to use Natural reserved keywords as names for variables."* (Reserved-Keywords doc.)
  Note one *naming-rules* page phrases it more strongly ("must not be a Natural reserved keyword"), but
  the dedicated Reserved-Keywords page — the authority on the topic — frames it as a strong
  recommendation and describes the actual failure mode (below), i.e. it is context-sensitive, not a
  blanket lex-time rejection.
- **The real problem is a SUBSET (statement/system-function keywords), and only in specific positions:**
  *"There is a subset of Natural keywords which, when used as names for variables, would be ambiguous.
  These are in particular keywords which identify Natural statements (`ADD`, `FIND`, etc.) or system
  functions (`ABS`, `SUM`, etc.). If you use such a keyword as the name of a variable, you cannot use
  this variable in the context of optional operands (with `CALLNAT`, `WRITE`, etc.)."* Example: with
  `1 ADD (A10)` defined, `CALLNAT 'MYSUB' ADD 4` parses `ADD` as the **ADD statement**, not the
  variable — that is the ambiguity, and it is *positional* (it bites where a statement could legally
  start).
- **KCHECK** (profile parameter / `COMPOPT` option) only *checks/flags* names against this critical
  subset at compile time; it does not change what is or isn't legal. It is off by default. So a program
  using a keyword-spelled name compiles unless KCHECK is on.

**Takeaway for the two bug positions:** after `DEFINE SUBROUTINE` the parser is in a **name-required**
position (the grammar mandates an identifier next); after `PERFORM` the parser is in a
**target-required** position (an identifier or variable next). In BOTH positions there is no competing
interpretation — a following token cannot start a *different* statement, because the grammar demands a
name here. So the statement-keyword ambiguity above does NOT apply, and the name may legitimately be a
keyword-spelled word (`CLEAR`, `RESET`, `PRINT`, `VALUE`, …). `PERFORM PRINT` (with `PRINT` a keyword)
is even used as an example in the DEFINE SUBROUTINE doc.

## Rule the parser should implement (issue #41)

In the `DEFINE SUBROUTINE <name>` and `PERFORM <name>` positions, **accept a keyword token as the name,
treating it as an identifier** (re-tag it to an identifier). These are name/target-mandatory positions;
no keyword here can begin a different construct, so there is no ambiguity to preserve.

**Guardrails / exceptions:**
1. Only re-tag when the position *requires* a name. Do NOT globally make keywords identifiers — that
   would break statement parsing elsewhere (the `CALLNAT 'MYSUB' ADD 4` optional-operand ambiguity is
   real *in operand position*).
2. `PERFORM` also accepts a **variable** name (dynamic PERFORM) → unresolvable/dynamic, retain the call
   site (existing rule). A keyword-spelled *literal* name is a normal identifier, resolvable normally.
3. `PERFORM BREAK [PROCESSING] ...` is a **different statement** (PERFORM BREAK). If the token after
   PERFORM is `BREAK` *and* the following tokens fit the break-processing form, that is not a subroutine
   name. natls disambiguates PERFORM BREAK before treating the token as a subroutine name — mirror that
   (a subroutine literally named `BREAK` is a pathological edge; PERFORM BREAK takes precedence, matching
   natls). This is the one real exception in the PERFORM position.
4. Enforce the identifier char/length rules above (≤32 significant chars; valid first char) — a
   keyword-spelled word trivially satisfies them, so this is just the normal identifier check.
5. Do not emit a diagnostic for a keyword-spelled name (it is legal Natural). At most this is a lint
   (see natls NL011 below), never a parse error.

## Cross-check against natls — verified (2026-07-20)

natls implements exactly this **context-sensitive** approach:

- `SyntaxKind` carries a per-keyword `canBeIdentifier` flag. `AbstractParser.consumeIdentifierTokenOnly()`
  accepts a token when `kind() == IDENTIFIER || kind().canBeIdentifier()`, then **re-tags it**:
  `currentToken.withKind(SyntaxKind.IDENTIFIER)`. So in name positions a keyword becomes an identifier.
- Both name positions use this path: `subroutine()` → `consumeMandatoryIdentifier(...)` (→
  `consumeIdentifierTokenOnly`); `perform()` → `var symbolName = consumeIdentifierTokenOnly();`. natls
  checks `performBreak()` (PERFORM BREAK) *before* `perform()` — the guardrail-3 exception.
- Which keywords are flaggable-as-identifier: of ~776 kinds, ~444 have `canBeIdentifier=true` and ~332
  `false`. Statement-starting keywords are `false` (`CALLNAT`, `DEFINE`, `END`, `IF`, `FOR`, `PERFORM`,
  `REPEAT`, `RESET`, `PRINT`, `ADD`, `FIND`, `SORT`, `INPUT`, `INCLUDE`, `FETCH`, `RUN`, `ESCAPE`,
  `IGNORE`, `DECIDE`, `SUM`, `ABS`, `COUNT`, `VALUE`, …); non-statement keywords are `true` (`DATE`,
  `TIME`, `LENGTH`, …). NOTE this `false` set is for *operand* contexts; in the mandatory-name positions
  natls uses `consumeIdentifierTokenOnly`, which ignores the flag distinction for the *name itself* only
  where the grammar forces a name — see the caveat below.
- **`CLEAR` and `HALT` are NOT in natls's keyword table at all** — natls lexes them as ordinary
  `IDENTIFIER`s (natls simply hasn't implemented the CLEAR statement). So `DEFINE SUBROUTINE CLEAR` /
  `PERFORM CLEAR` parse trivially there. For OUR lexer, if `CLEAR`/`RESET`/`HALT`/etc. ARE promoted to
  keyword tokens, the name-position parser must re-tag them to identifiers (the rule above), or those
  exact fixtures will break — which is the issue #41 failure mode.
- natls surfaces keyword-as-identifier only as a **lint** (`NL011` "keyword used as identifier (prefix
  with `#`)"), never a parse error — confirming it is legal, discouraged, not illegal.

## Sources

- Rules and Naming Conventions (identifier char/length/first-char rules; "must not be a reserved keyword"):
  https://documentation.softwareag.com/natural/nat827mf/using/use_rules.htm
- Natural Reserved Keywords (recommendation + ambiguity subset + KCHECK; the authoritative nuance):
  https://documentation.softwareag.com/natural/nat841unx/pg/pg_keyw.htm
- User-Defined Variables (naming): https://documentation.softwareag.com/natural/nat841unx/pg/pg_defi_dv.htm
- DEFINE SUBROUTINE (name = user-defined-variable conventions, max 32; PERFORM PRINT / PERFORM DSREX2-SUB
  examples; name independent of module): https://documentation.softwareag.com/naturalONE/natONE912/natov/sm/definesu.htm
- KCHECK reserved-keyword list (SMA reserved-words reference):
  https://documentation.softwareag.com/natural/sma221/reserved/sma-reserved.htm
- natls `AbstractParser.consumeIdentifierTokenOnly` / `consumeMandatoryIdentifier`:
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/AbstractParser.java
- natls `StatementListParser.subroutine()` / `perform()` / `performBreak()`:
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/StatementListParser.java
- natls `SyntaxKind` (per-keyword `canBeIdentifier` flag):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/lexing/SyntaxKind.java
- natls NL011 (keyword-as-identifier is a lint, not an error): see natls-prior-art.md
