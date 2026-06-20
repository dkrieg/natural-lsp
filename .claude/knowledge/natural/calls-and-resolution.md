# Calls & Resolution

How Natural inter-module calls and references work, and how they map to analyzer edges.
Dialect note: facts below verified against Natural for Linux/Unix/Windows + Mainframe statement
references (syntax is stable across these for the call statements). Mode: applies to both structured
and reporting mode unless noted.

**Status: verified (2026-06-20)** — confirmed against official Software AG statement references.

| Construct | Meaning | Analyzer edge | Resolution |
|-----------|---------|---------------|------------|
| `CALLNAT 'NAME'` | call subprogram by literal name | `CALLS` | static → definition (`.NSN`) via steplib chain |
| `CALLNAT name-var` | call subprogram by variable | `CALLS_DYNAMIC` | unresolvable statically; preserve caller context |
| `PERFORM name` | invoke subroutine | `PERFORMS` | inline first, then external (`.NSS`) — see below |
| `FETCH 'NAME'` / `FETCH RETURN 'NAME'` | transfer to / call program | `NAVIGATES_TO` | static → program (`.NSP`) |
| `FETCH name-var` | transfer to program by variable | `CALLS_DYNAMIC` | dynamic; preserve context |
| `RUN 'NAME'` | compile+execute source program | `NAVIGATES_TO` | primarily a SYSTEM COMMAND — see caveat |
| `INCLUDE NAME` | inline copycode at compile time | `INCLUDES` | literal name only → copycode (`.NSC`) |
| `name(<...>)` | user-defined function call | `CALLS` (to `.NS7`) | function objects; lower priority |

## CALLNAT (subprogram call) — verified

Canonical syntax:
```
CALLNAT operand1 [operand2 ... ] [USING] ...
```
- `operand1` (subprogram name) is EITHER:
  - an **alphanumeric constant of 1 to 32 characters** (static call) → `CALLS`, or
  - an **alphanumeric variable of length 1 to 8** (dynamic selection) → `CALLS_DYNAMIC`.
- `operand2 ...` are the parameters passed to the subprogram's `DEFINE DATA PARAMETER` / PDA.
- `USING` is an optional keyword before the parameter list. `AD=O|M|A` set attribute (by value /
  modifiable / input-only) per parameter; `nX` skips n parameters.
- **`&` gotcha:** the subprogram name may contain an ampersand `&`, which is replaced at runtime by the
  one-character code of `*LANGUAGE`. So a literal like `'MENU&'` is only *partially* static. The
  analyzer should treat a literal containing `&` as a dynamic/parametric target (emit `CALLS_DYNAMIC`
  or a resolved-with-wildcard edge), NOT a clean `CALLS` to the literal text.

## PERFORM (subroutine) — verified

Canonical syntax:
```
PERFORM subroutine-name [operand1 ...]
```
- `subroutine-name` up to 32 chars; can be a constant or a variable (operand class C/S).
- **Resolution order (critical):** Natural first looks for an INLINE (internal) subroutine with that
  name defined via `DEFINE SUBROUTINE` in the same object. If none is found, it AUTOMATICALLY performs
  an EXTERNAL subroutine (`.NSS`) of the same name. So the analyzer must check intra-object
  `DEFINE SUBROUTINE` definitions before emitting an external `PERFORMS` edge.
- Internal subroutines take NO explicit parameters (they share GDA/scope). External subroutines CAN
  take parameters passed directly on the PERFORM (matching the subroutine's `DEFINE DATA PARAMETER`/PDA).
- A variable subroutine-name → `CALLS_DYNAMIC` (no inline target to bind).

## FETCH / RUN (program transfer) — verified

`FETCH` syntax:
```
FETCH [REPEAT|RETURN] operand1 [operand2 [(parameter)] ...]
```
- `operand1` (program name) is an **alphanumeric constant OR an alphanumeric variable (1–8)**.
  Variable form → `CALLS_DYNAMIC`. Name case is NOT translated. May contain `&` (`*LANGUAGE`) — same
  gotcha as CALLNAT.
- `FETCH 'NAME'` (no RETURN): terminates the invoking object and starts NAME as a new main program
  (level 1). The caller is NOT re-activated → model as `NAVIGATES_TO` (transfer of control).
- `FETCH RETURN 'NAME'`: suspends caller, runs NAME as a subordinate; control returns at NAME's `END`
  or `ESCAPE ROUTINE`. Closer to a call → still `NAVIGATES_TO` is acceptable, but if the analyzer wants
  to distinguish, `FETCH RETURN` is call-like and plain `FETCH` is goto-like.
- `REPEAT` suppresses prompting for INPUT statements in the fetched program (not a control-flow edge).
- Parameters are pushed onto the Natural stack and read by the fetched program's `INPUT`. The fetched
  program also sees the established GDA.
- Target of FETCH is a cataloged program object (`.NSP`).

`RUN` caveat — verified:
- `RUN [REPEAT] [program-name [library-id]]` is documented as a **SYSTEM COMMAND**, not a regular
  in-program statement. It compiles (catalogs) AND executes a source program. The official statement
  reference set does not list RUN as a program statement the way it lists FETCH.
- Practical implication: RUN appearing inside object source is far less common than FETCH/CALLNAT.
  Keep a `NAVIGATES_TO` edge for `RUN 'NAME'` if encountered, but prioritize FETCH/CALLNAT/PERFORM.
  The optional `library-id` second token means RUN can target a SPECIFIC library, bypassing the normal
  steplib search — if present, resolution should honor that library, not the steplib chain.

## INCLUDE (copycode) — verified

Canonical syntax:
```
INCLUDE copycode-name [operand1 ... up to 99]
```
- `copycode-name` is a **literal name only** — it CANNOT be a variable. (It may contain `&` for
  multilingual variants, a compile-time substitution feature, not dynamic dispatch.) → always emit a
  resolvable `INCLUDES` edge to the `.NSC` (unless the name contains `&`).
- Operands supply values substituted into the copycode at `&1&`, `&2&`, … `&99&` at COMPILE time.
  Substitution is textual: `&1&&2&abc` concatenates; a string may FOLLOW a parameter but must not
  precede or sit between parameters.
- Constraints a naive regex must respect:
  - A source line containing `INCLUDE` must contain NO other statement (one statement per line).
  - A copycode must NOT contain an `END` statement.
  - The copycode body is a source FRAGMENT, valid only when included; its symbols may resolve only in
    the includer's context. Parameter substitution (`&n&`) means the included text can differ per call
    site — symbol extraction from the raw copycode may be incomplete.

## Steplib resolution (critical) — verified

Natural resolves a module name by walking a **steplib chain**, not a file path. Documented search
order for object execution:

1. The **current library** in the FUSER system file (system variable `*LIBRARY-ID`).
2. The **steplibs in sequence** — as specified in the Natural Security profile for the current
   library, or in the steplib table.
3. The **default steplib** in system variable `*STEPLIB`.
4. Library **SYSTEM in FUSER**, then library **SYSTEM in FNAT**.

Additional (user) steplibs are searched BEFORE the standard `SYSTEM` libraries. The same module name
can exist in multiple libraries; the search ORDER is what disambiguates. The analyzer models this via
`[resolution]` config; with no library map it falls back to a flat namespace and emits a diagnostic on
ambiguity. `RUN ... library-id` overrides this by naming a specific library.

## Sources

- CALLNAT: https://documentation.softwareag.com/one/9.3.1/en/webhelp/one-webhelp/natux/sm/callnat.htm
- PERFORM: https://documentation.softwareag.com/natural/nat914unx/sm/perform.htm
- FETCH: https://documentation.softwareag.com/natural/nat911mf/sm/fetch.htm
- RUN (system command): https://documentation.softwareag.com/natural/nat912unx/syscom/run.htm
- INCLUDE: https://documentation.softwareag.com/natural/nat911unx/sm/include.htm
- STEPLIB / object search order: https://documentation.softwareag.com/natural/nat912unx/parms/steplib.htm
- Programs and Subordinate Routines (FETCH vs FETCH RETURN levels):
  https://documentation.softwareag.com/natural/nat913unx/pg/pg_obj_pgm_routine.htm