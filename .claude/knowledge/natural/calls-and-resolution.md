# Calls & Resolution

How Natural inter-module calls and references work, and how they map to analyzer edges.
Dialect note: facts below verified against Natural for Linux/Unix/Windows + Mainframe statement
references (syntax is stable across these for the call statements). Mode: applies to both structured
and reporting mode unless noted.

**Status: verified (2026-06-30)** — confirmed against official Software AG statement references.
CALLNAT/FETCH/RUN syntax, INCLUDE, PERFORM inline-first, and the steplib search sequence (flat,
NON-transitive, one-level per current library) are all verified; the prior transitive-steplib claim was
corrected on 2026-06-30 and the misleading "transitive" source citation was relabeled.

| Construct | Meaning | Analyzer edge | Resolution |
|-----------|---------|---------------|------------|
| `CALLNAT 'NAME'` | call subprogram by literal name | `CALLS` | static → definition (`.NSN`) via steplib chain; constant 1–32 chars (9.3.1+) |
| `CALLNAT name-var` | call subprogram by variable | unresolvable | call site retained; target cannot be determined statically; variable 1–8 chars |
| `PERFORM name` | invoke subroutine | `PERFORMS` | inline first, then external (`.NSS`) — see below |
| `FETCH 'NAME'` / `FETCH RETURN 'NAME'` | transfer to / call program | `NAVIGATES_TO` | static → program (`.NSP`); name max 8 chars |
| `FETCH name-var` | transfer to program by variable | unresolvable | call site retained; dynamic target; variable 1–8 chars |
| `RUN 'NAME'` | compile+execute source program | `NAVIGATES_TO` | primarily a SYSTEM COMMAND — see caveat |
| `INCLUDE NAME` | inline copycode at compile time | `INCLUDES` | literal name only → copycode (`.NSC`) |
| `name(<...>)` | user-defined function call | `CALLS` (to `.NS7`) | function objects; lower priority |

## CALLNAT (subprogram call) — verified (2026-06-23)

Canonical syntax:
```
CALLNAT operand1 [operand2 ... ] [USING] ...
```
- `operand1` (subprogram name) is EITHER:
  - an **alphanumeric constant of 1 to 32 characters** (static call) → `CALLS`, or
  - an **alphanumeric variable of length 1 to 8** (dynamic selection) → unresolvable; retain call site.
- `operand2 ...` are the parameters passed to the subprogram's `DEFINE DATA PARAMETER` / PDA.
- `USING` is an optional keyword before the parameter list. `AD=O|M|A` set attribute (by value /
  modifiable / input-only) per parameter; `nX` skips n parameters.
- **`&` gotcha:** the subprogram name may contain an ampersand `&`, which is replaced at runtime by the
  one-character code of `*LANGUAGE`. So a literal like `'MENU&'` is only *partially* static. The
  parser should treat a literal containing `&` as a dynamic/parametric target (retain as unresolvable,
  NOT as a clean `CALLS` to the literal text). See FR-18.

**Note on FETCH:** FETCH program name (constant or variable) is limited to **1–8 characters** only,
not 1–32. Both CALLNAT and FETCH support `&` substitution with `*LANGUAGE`.

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
- A variable subroutine-name → unresolvable; retain call site (no inline target to bind).

## FETCH / RUN (program transfer) — verified (2026-06-30)

**Library-qualifier summary (the parser question):** FETCH has **NO** source-level library qualifier —
library/steplib selection is runtime-only. RUN **DOES** have one — an optional bare `library-id` second
positional operand. Details below.

`FETCH` syntax (verbatim diagram, both nat911unx and nat911mf):
```
FETCH [REPEAT] [RETURN] operand1 [operand2 [(parameter)]] ...
```
- **NO source-level library qualifier on FETCH (decision-critical, verified 2026-06-30).** The FETCH
  syntax diagram has NO operand or keyword to name a target library or steplib. The ONLY operands are
  `operand1` (program name) and zero-or-more `operand2` parameter fields. Library/steplib resolution is
  purely a RUNTIME concern: per the doc, "Natural will attempt to locate the program in the library
  currently active … If the program is not found, Natural will attempt to locate the program in the
  steplibs." → The parser CANNOT and MUST NOT extract a library qualifier from FETCH text. `operand2`
  after the program name is a PARAMETER FIELD pushed onto the stack, never a library — do not misread
  the second operand as a library id.
- `operand1` (program name) is an **alphanumeric constant OR an alphanumeric variable (1–8)**.
  Variable form → unresolvable; retain call site. Name case is NOT translated. May contain `&`
  (`*LANGUAGE`) — same gotcha as CALLNAT (treat as unresolvable).
- **Important:** Unlike CALLNAT constants (1–32), FETCH names are limited to **1–8 characters** for both
  constants and variables.

**Sources:**
- FETCH (9.1.1): https://documentation.softwareag.com/natural/nat911unx/sm/fetch.htm
- `FETCH 'NAME'` (no RETURN): terminates the invoking object and starts NAME as a new main program
  (level 1). The caller is NOT re-activated → model as `NAVIGATES_TO` (transfer of control).
- `FETCH RETURN 'NAME'`: suspends caller, runs NAME as a subordinate; control returns at NAME's `END`
  or `ESCAPE ROUTINE`. Closer to a call → still `NAVIGATES_TO` is acceptable, but if the analyzer wants
  to distinguish, `FETCH RETURN` is call-like and plain `FETCH` is goto-like.
- `REPEAT` suppresses prompting for INPUT statements in the fetched program (not a control-flow edge).
- Parameters are pushed onto the Natural stack and read by the fetched program's `INPUT`. The fetched
  program also sees the established GDA.
- Target of FETCH is a cataloged program object (`.NSP`).

`RUN` caveat — verified (verbatim diagram nat912unx + nat912mf, 2026-06-30):
```
RUN [REPEAT] [program-name [library-id]]
```
- `RUN` is documented as a **SYSTEM COMMAND**, not a regular in-program statement. It compiles
  (catalogs) AND executes a source program. The official statement reference set does not list RUN as a
  program statement the way it lists FETCH.
- **YES, RUN HAS a source-level library qualifier (decision-critical).** Unlike FETCH, RUN takes an
  OPTIONAL second positional operand `library-id` after `program-name`. Per the doc: "The library in
  which the program to be run is contained. If both `program-name` and `library-id` are specified,
  Natural will retrieve, compile, and execute the specified program only if it is stored under the
  library ID specified." So with `library-id` present, RUN targets THAT library only (single library,
  no steplib fall-through) — it does NOT walk the steplib chain. Without `library-id`, RUN searches the
  current library only.
- **Lexical distinction (how the parser tells program-name from library-id):** both are bare
  positional tokens; there is NO keyword introducing the library. The grammar is
  `RUN [REPEAT] [token1 [token2]]`. `token1` = program-name, `token2` (if present) = library-id. There
  is no third positional operand on RUN, so a second token is unambiguously the library. Constraint
  the parser can assert: `library-id` must NOT begin with `SYS` (except exactly `SYSTEM`). Note RUN
  takes NO parameter-field operands (contrast FETCH), so a second token cannot be a parameter.
- Practical implication: RUN appearing inside object source is far less common than FETCH/CALLNAT.
  Emit a `NAVIGATES_TO` edge for `RUN program-name`; when `library-id` is present, resolution should
  honor that single library, NOT the steplib chain. Both `program-name` and `library-id` can be a
  constant or a variable (variable → dynamic/unresolvable, retain call site).

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
- Constraints the parser must respect:
  - A source line containing `INCLUDE` must contain NO other statement (one statement per line).
  - A copycode must NOT contain an `END` statement.
  - The copycode body is a source FRAGMENT, valid only when included; its symbols may resolve only in
    the includer's context. Parameter substitution (`&n&`) means the included text can differ per call
    site — symbol extraction from the raw copycode may be incomplete.

## Steplib resolution (critical) — verified (2026-06-30)

Natural resolves a module name by walking a **steplib chain**, not a file path. The authoritative
"Search Sequence for Object Execution" (Using Natural → Natural Libraries) defines a **flat, ordered
list** evaluated until the first hit. Two sequences, depending on whether the current library is a
user library (FUSER) or a system library (FNAT):

**User-library search (current lib in FUSER):**
1. The **current library** (system variable `*LIBRARY-ID`).
2. The **steplibs, in declared sequence** — as specified in the Natural Security profile for the
   current library, or in the steplib table.
3. The **default steplib** in system variable `*STEPLIB`.
4. Library **SYSTEM in FUSER**.
5. Library **SYSTEM in FNAT**.

**System-library search (current "SYS…" lib in FNAT):**
1. The current `SYS` library (`*LIBRARY-ID`).
2. The steplibs, in declared sequence.
3. **SYSLIBS in FNAT**.
4. **SYSTEM in FNAT**.
5. **SYSTEM in FUSER**.

Additional (user) steplibs are searched BEFORE the standard `SYSTEM` libraries. The same module name
can exist in multiple libraries; the search ORDER is what disambiguates. The analyzer models this via
`[resolution]` config; with no library map it falls back to a flat namespace and emits a diagnostic on
ambiguity. `RUN ... library-id` overrides this by naming a specific library.

### Steplib-of-steplib recursion — RESOLVED: NON-transitive (verified 2026-06-30)

**Correction of a prior (2026-06-23) wrong entry.** An earlier KB revision claimed the runtime search
is "transitive" and cited the Predict cross-reference "Steplib Support" page
(`prd84x/.../natxref_steplib_5.htm`). That was a **double error**: (1) that page documents the
**Predict / XRef cross-reference tool's** search, NOT the Natural runtime's object-execution search;
and (2) even that page does **not** describe transitive search — it describes a single-level approach
(it appends `*STEPLIB`/system refs to the structure) and notes a max of **20** steplibs for the XRef
tool. So it never supported the "transitive" claim in the first place.

**What the runtime actually does:** the documented object-execution search sequence is a **flat,
ordered list bound to the current library** (steps above). The list of steplibs searched is the set
**declared for the current library** plus `*STEPLIB` plus the SYSTEM terminals. There is **no
documented statement that a steplib's own steplibs are followed.** When control transfers into a module
that resides in steplib B (e.g. B's program issues a CALLNAT), the search context is the **invoking
runtime's** current library + ITS steplib chain — resolution is re-evaluated against the SAME flat
chain, NOT against B's own declared steplibs. Steplib assignment is a per-(current-library) runtime
property, not a property that chains library-to-library.

- **Max steplibs:** Natural Security allows up to **8** steplibs per library (Library/DBnr/Fnr
  entries). Without Natural Security, exactly **1** additional steplib via the `STEPLIB` profile
  parameter at session start. (The "20" figure belongs to the Predict XRef tool, not the runtime.)
- **For a static analyzer this means: one level.** Model resolution as: current library → that
  library's declared steplibs (in order) → implicit SYSTEM. Do NOT recursively expand a steplib's own
  steplibs. This also matches natls's implementation (`NaturalLibrary.findModuleByReferableName` walks
  one steplib level).
- **Caveat — environment dependence:** the *contents* of the chain are configuration-driven (Natural
  Security profile vs. `STEPLIB` parameter vs. NaturalONE `.natural` build file), and `*STEPLIB` can be
  reset at runtime. But the *shape* is consistently a flat per-current-library list in all documented
  variants; transitive expansion is not part of any documented variant.

**Sources:**
- Search Sequence for Object Execution (Using Natural Libraries, runtime — authoritative):
  https://documentation.softwareag.com/natural/nat911mf/using/use_mf_libs.htm
- STEPLIB system variable: https://documentation.softwareag.com/natural/nat912unx/parms/steplib.htm
- Defining Steplibs (Natural Security, max 8 per library):
  https://documentation.softwareag.com/natural/nbs531/admin/define-steplibs-and-domains.htm
- Predict XRef "Steplib Support" (NOT the runtime; max 20; single-level — the misread prior source):
  https://documentation.softwareag.com/natural/prd842/reference/natxref_steplib_5.htm

## Sources (calls-and-resolution)

- CALLNAT (9.3.3): https://documentation.softwareag.com/natux/9.3.3/en/webhelp/natux-webhelp/sm/callnat.htm
- PERFORM (9.1.4): https://documentation.softwareag.com/natural/nat914unx/sm/perform.htm
- FETCH (9.1.1): https://documentation.softwareag.com/natural/nat911unx/sm/fetch.htm
- RUN (system command): https://documentation.softwareag.com/natural/nat912unx/syscom/run.htm
- INCLUDE (9.1.1): https://documentation.softwareag.com/natural/nat911unx/sm/include.htm
- STEPLIB / object search order: https://documentation.softwareag.com/natural/nat912unx/parms/steplib.htm
- Programs and Subordinate Routines (FETCH vs FETCH RETURN levels):
  https://documentation.softwareag.com/natural/nat913unx/pg/pg_obj_pgm_routine.htm
- Predict XRef "Steplib Support" — NOTE: this is the cross-reference TOOL, not the runtime; it does
  NOT describe transitive resolution (single-level; max 20 for the XRef tool). Kept only to document
  the prior misread; see "Steplib-of-steplib recursion — RESOLVED: NON-transitive" above:
  https://documentation.softwareag.com/natural/prd842/reference/natxref_steplib_5.htm
- natls steplib resolution (`NaturalLibrary`):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalLibrary.java
- natls project/steplib wiring (`BuildFileProjectReader`):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/project/BuildFileProjectReader.java

## Cross-check against natls implementation — verified (2026-06-21)

natls (prior-art parser-based LSP; see natls-prior-art.md) implements resolution consistent with the
above and adds concrete detail:

- **One `IModuleReferencingNode` abstraction** covers CALLNAT, external PERFORM, FETCH, INCLUDE,
  user-defined function calls, AND `DEFINE DATA ... USING <area>`. So a data-area `USING` is the same
  kind of cross-module edge as a call — model the `USING` → LDA/GDA/PDA reference as a first-class edge.
- **PERFORM inline-first is structural in the AST:** internal PERFORM = a symbol reference
  (`IInternalPerformNode`/`ISymbolReferenceNode`); external PERFORM = a module reference
  (`IExternalPerformNode`). Confirms: scan `DEFINE SUBROUTINE` in-object before emitting an external
  `PERFORMS` edge.
- **FETCH RETURN vs plain FETCH** is distinguished (`IFetchNode.isFetchReturn()`), matching our
  call-like vs goto-like note.
- **Resolution order (from `NaturalLibrary.findModuleByReferableName`):** current library first; if the
  name resolves to multiple files, prefer the file whose type matches the call (CALLNAT→`.NSN` etc.);
  then steplibs **in order**. natls walks only ONE steplib level (no steplib-of-steplib recursion).
  RESOLVED 2026-06-30: the real runtime is ALSO one-level (flat per-current-library chain) — natls's
  approach is correct. See "Steplib-of-steplib recursion" above.
- **DDMs are a separate namespace** (`findDdmByReferableName`) — DDM names don't collide with module
  names; resolve READ/FIND/`VIEW OF` targets against DDMs only.
- **Project layout / steplib config:** root = directory of the `.natural` (or `_naturalBuild`) build
  file; libraries = subdirs of `Natural-Libraries/<LIB>/` (+ optional read-only `include/`); steplibs
  declared in the build-file XML; **`SYSTEM` is implicitly a steplib of every library**. This matches
  our `[resolution]` config model.
- natls additionally validates **call parameter count/type against the callee's PDA** (parser errors
  `NPP056`–`NPP058`) — a possible future feature once the parser is in place.

## Sources

- CALLNAT: https://documentation.softwareag.com/one/9.3.1/en/webhelp/one-webhelp/natux/sm/callnat.htm
- PERFORM: https://documentation.softwareag.com/natural/nat914unx/sm/perform.htm
- FETCH: https://documentation.softwareag.com/natural/nat911mf/sm/fetch.htm
- RUN (system command): https://documentation.softwareag.com/natural/nat912unx/syscom/run.htm
- INCLUDE: https://documentation.softwareag.com/natural/nat911unx/sm/include.htm
- STEPLIB / object search order: https://documentation.softwareag.com/natural/nat912unx/parms/steplib.htm
- Programs and Subordinate Routines (FETCH vs FETCH RETURN levels):
  https://documentation.softwareag.com/natural/nat913unx/pg/pg_obj_pgm_routine.htm
- natls steplib resolution (`NaturalLibrary`):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalLibrary.java
- natls project/steplib wiring (`BuildFileProjectReader`):
  https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/project/BuildFileProjectReader.java