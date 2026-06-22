# natls — Prior-Art Natural LSP (MarkusAmshove/natls)

The most directly comparable project to `natural-lsp`: an independent, mature, open-source Language
Server + linter for Software AG Natural, written in Java. Studying it tells us what a *complete*
Natural analyzer has to handle, what it deliberately skips, and how it models the language. Unlike our
project, natls uses a **hand-written lexer + recursive-descent parser** (not regex), so its scope is a
useful "ceiling" reference rather than a drop-in design.

**Status: verified (2026-06-21)** — read directly from the repository source at the commit cloned
2026-06-21. Source paths cited are `libs/<module>/src/main/java/org/amshove/...`. License: MIT.

## Components (Gradle multi-module)

- `natparse` — lexer, parser, AST (the `org.amshove.natparse.natural.*` `I…Node` interfaces), project/
  build-file model, DDM parser.
- `natlint` — static analysis: 41 lint rules (`NL001`–`NL041`).
- `natls` — the LSP server (uses Eclipse **lsp4j**, unmodified).
- `natgen` — code generation utilities.
- `natqube` — SonarQube plugin (aggregates natlint diagnostics).
- `natdoc` — planned, not implemented.

## Scope / deliberate limitations (from README + source)

- **Structured mode only.** "Currently only the structured mode syntax of statements is parsed
  correctly." Reporting mode is explicitly out of scope. Parser raises `UNSUPPORTED_PROGRAMMING_MODE`
  (`NPP040`) when it sees reporting mode. These reporting-only statements are listed as "not planned":
  `DO`/`DOEND`, `LOOP`, `MOVE INDEXED`, `OBTAIN`, **`REDEFINE`** (the standalone reporting-mode
  statement — NOT the `REDEFINE` *clause* inside `DEFINE DATA`, which natparse does parse; see below).
- ~95 statements implemented, ~22 not. **Not implemented** (notable): `CALLDBPROC`, `CREATE OBJECT`,
  `DEFINE CLASS`, `INTERFACE`, `METHOD`, `PROPERTY`, `SEND METHOD` (the NaturalX/OO statements),
  `PROCESS`, `PROCESS COMMAND`, `PROCESS PAGE` (Natural for Ajax UI), `REINPUT`, `RETRY`, `SORT`,
  `STORE`, `READ RESULT SET`, `READLOB`/`UPDATELOB`, `REQUEST DOCUMENT`, `SET CONTROL`, `SET GLOBALS`,
  `SUSPEND IDENTICAL SUPPRESS`, `UPLOAD PC FILE`.
- **Implemented**, and relevant to us: `CALLNAT`, `PERFORM`, `FETCH`, `RUN`, `INCLUDE`, `DEFINE DATA`,
  `DEFINE SUBROUTINE`, `DEFINE FUNCTION`, `DEFINE PROTOTYPE`, all the SQL embedded statements,
  `PARSE XML`/`PARSE JSON`, `INPUT`, `READ`/`FIND`/`HISTOGRAM`/`GET`, work-file I/O.
- Source: `docs/implemented-statements.md`, `README.md`.

## File-extension → object-type mapping (authoritative cross-check)

`NaturalFileType.java` enumerates exactly **11** object types and maps each from its uppercase
extension. This corroborates our `file-extensions.md` mapping for the 11 it covers, and tells us which
4 a real parser-based LSP currently *skips*:

| Ext | natls type | Our KB | Match |
|-----|-----------|--------|-------|
| NSD | DDM | DDM | ✓ |
| NSN | SUBPROGRAM | Subprogram | ✓ |
| NSP | PROGRAM | Program | ✓ |
| NSS | SUBROUTINE | External Subroutine | ✓ |
| NSH | HELPROUTINE | Helproutine | ✓ |
| NSG | GDA | Global data area | ✓ |
| NSL | LDA | Local data area | ✓ |
| NSA | PDA | Parameter data area | ✓ |
| NSM | MAP | Map | ✓ |
| NSC | COPYCODE | Copycode | ✓ |
| NS7 | FUNCTION | Function | ✓ |

natls has **TODO comments for `TEXT`, `CLASS (NS4)`, `ADAPTER`, `RESOURCE`** — i.e. it does NOT yet
handle `.NS4`/`.NS8`/`.NST` (and a "RESOURCE" type). Our default indexed set includes those plus
`.NS3` (dialog); natls covers none of them. Takeaway: NS4/NS8/NST/NS3 are genuinely lower-value/edge
types — even the mature prior-art parser punts on them.

Two source-level facts worth copying:
- `canHaveDefineData()` → true for SUBPROGRAM, PROGRAM, SUBROUTINE, HELPROUTINE, LDA, PDA, GDA,
  FUNCTION, **MAP**; false for DDM, COPYCODE. (Note MAP *can* carry a `DEFINE DATA`.)
- `canHaveBody()` → true for SUBPROGRAM, PROGRAM, SUBROUTINE, HELPROUTINE, FUNCTION, COPYCODE, MAP;
  false for the data areas + DDM.
- Source: `libs/natparse/.../natural/project/NaturalFileType.java`.

## Referable name ≠ filename for SUBROUTINE and FUNCTION (important)

How a module is *referred to* (what `PERFORM` / function-call / `CALLNAT` resolve against) is derived
per type (`NaturalProjectFileIndexer.getReferableName`):
- SUBPROGRAM, DDM, LDA, PDA, GDA, PROGRAM, COPYCODE, MAP, HELPROUTINE → **the file base name**.
- **SUBROUTINE (`.NSS`)** → the name is **lexed out of the source**: the identifier after
  `DEFINE SUBROUTINE`. The external subroutine name can DIFFER from the filename and can exceed 8
  chars. (Confirmed by fixture `EXTSUB.NSS` whose body is `DEFINE SUBROUTINE EXTERNAL-SUB`.)
- **FUNCTION (`.NS7`)** → the name is lexed out: the identifier after `DEFINE FUNCTION`.

Analyzer implication: to resolve `PERFORM EXTERNAL-SUB` and function calls, you cannot rely on the
filename — you must read the `DEFINE SUBROUTINE` / `DEFINE FUNCTION` name from the file. For all other
types the filename is the referable name. Source:
`libs/natparse/.../natural/project/NaturalProjectFileIndexer.java`.

## Module-reference model (maps cleanly onto our edges)

natparse has a single interface `IModuleReferencingNode` (a `referencingToken()` = the name token, an
optional resolved `reference()`, and `providedParameter()`). The following all implement it — i.e.
natls treats them as the same kind of cross-module edge:

- `ICallnatNode` (CALLNAT → subprogram)
- `IExternalPerformNode` (PERFORM → external `.NSS`) — distinct from `IInternalPerformNode`, which is a
  `ISymbolReferenceNode` (inline `DEFINE SUBROUTINE`), confirming inline-first PERFORM resolution.
- `IFetchNode` (FETCH; `isFetchReturn()` flags FETCH RETURN vs plain FETCH)
- `IIncludeNode` (INCLUDE → copycode; also a `IStatementWithBodyNode`, i.e. the copycode body is
  inlined into the AST)
- `IFunctionCallNode` (user-defined function call → `.NS7`; also an `IOperandNode` — a function call
  is an expression operand, not just a statement)
- `IUsingNode` (`DEFINE DATA ... USING <area>` → LDA/GDA/PDA). natls models the data-area `USING` as a
  module reference too — i.e. our read/include edge to a `.NSx` data area is the same resolution path.

Maps onto our edge vocabulary: CALLNAT→`CALLS`/`CALLS_DYNAMIC`, external PERFORM→`PERFORMS`,
FETCH→`NAVIGATES_TO`, INCLUDE→`INCLUDES`, function call→`CALLS` (to `.NS7`), `USING`→read/include.
Source: `libs/natparse/.../natural/I{Callnat,Fetch,Include,ExternalPerform,InternalPerform,FunctionCall,Using}Node.java`.

## Steplib resolution as implemented (concrete reference)

`NaturalLibrary.findModuleByReferableName(name, includeStepLibs, requestedType)`:
1. Look in the **current library**'s own modules first (by referable name).
2. If `requestedType` is given and the name maps to several files, prefer the file whose type matches
   (e.g. a `CALLNAT` prefers the `.NSN`); else return the first.
3. Then search **steplibs in order** — but recursion passes `includeStepLibs=false`, so natls searches
   only **one level** of steplibs (steplibs-of-steplibs are NOT walked). Worth confirming against the
   real Natural runtime, but it's a pragmatic choice.
4. **DDMs live in a separate namespace** (`ddmsByReferableName`) resolved by
   `findDdmByReferableName` — DDM names don't collide with module names.

Project/steplib wiring (`BuildFileProjectReader`):
- Project root = directory of the build file (`.natural` or `_naturalBuild`). Libraries are the
  subdirectories of `Natural-Libraries/` (plus optional `include/` for read-only included libraries).
- Steplibs are declared in the build-file XML (`<LibrarySteplibName>`, `<LibrarySteplibNSV>`,
  `<LibrarySteplibExtensions>`), parsed by `XmlLibraryVisitor`; numeric and `[...]` noise is stripped,
  `;`-separated.
- **`SYSTEM` is implicitly added as a steplib to every non-SYSTEM library.** Steplibs that resolve to
  no on-disk source folder are dropped ("system steplibs").
- Sources: `libs/natparse/.../parsing/project/BuildFileProjectReader.java`,
  `XmlLibraryVisitor.java`, `infrastructure/ActualFilesystem.java` (project-file names).

Our analog: this is exactly the steplib chain our `[resolution]` config models. natls confirms
(a) current-lib-first, ordered-steplibs-next, (b) DDMs in a separate namespace, (c) the on-disk
`Natural-Libraries/<LIB>/` layout, and (d) an implicit SYSTEM fallback.

## NaturalONE source header (resolves our "how is mode known" question)

NaturalONE-exported source objects begin with a machine-written header *comment block*. natls lexes it
(`Lexer.consumeNaturalHeader`) and real fixtures confirm the exact shape:

```
* >Natural Source Header 000000
* :Mode S
* :CP
* :LineIncrement 10
* <Natural Source Header
DEFINE DATA
...
```

- Block is delimited by `* >Natural Source Header` … `* <Natural Source Header`.
- `* :Mode S` → structured, `* :Mode R` → reporting. **This is how programming mode is determined —
  it is declared in the file, not inferred.** (`NaturalProgrammingMode`: S/R/?.)
- `* :CP` = code page, `* :LineIncrement` = editor line-number increment.
- DDM headers use a slightly different indented variant (`*      >Natural Source Header`).

Analyzer implication: **read `* :Mode` from the header** to decide structured vs reporting, instead of
guessing from `END-IF` presence. If absent (hand-written / non-exported source), mode is UNKNOWN and
structured is the safe default. Source: `libs/natparse/.../lexing/Lexer.java` (consumeNaturalHeader),
fixtures under `libs/*/src/test/resources/`.

## Comment lexing (confirms markers for our regex)

`Lexer`:
- **Line comment:** `*` at line start *followed by* space, tab, newline, `/`, `*`, or EOF
  (`isSingleAsteriskComment`). So `*` begins a full-line comment only when it's the first non-blank and
  the next char isn't an identifier char — important so a label/operand starting with `*` isn't eaten.
- **Inline comment:** `/*` anywhere (`isInlineComment`) — runs to end of line.
- Gotcha the lexer handles explicitly: `END-SUBROUTINE/*` = the keyword *plus* a comment; and `/` is
  ambiguous between an inline comment and an array-bound separator like `(A10/1:5)` — disambiguated by
  lexer mode. A naive regex must treat `/*` carefully inside array definitions.
- Source: `libs/natparse/.../lexing/Lexer.java`.

## Parser diagnostics (`NPP…`) — what a full parser flags

`ParserError.java` defines ~60 parser errors (`NPP000`–`NPP059+`). The ones most relevant to our
"unresolvable reference vs unrecognized syntax" policy:
- `NPP016 UNRESOLVED_REFERENCE` (variable/symbol not found) and `NPP026 UNRESOLVED_MODULE` (called
  module not found) — these are natls's analog of our `CALLS_DYNAMIC` / unresolved-call surface.
- `NPP001 NO_DEFINE_DATA_FOUND`, `NPP002 MISSING_END_DEFINE`, `NPP030 UNCLOSED_STATEMENT`,
  `NPP055 END_STATEMENT_MISSING`, `NPP054 NO_SOURCE_ALLOWED_AFTER_END_STATEMENT`.
- `NPP040 UNSUPPORTED_PROGRAMMING_MODE` (reporting mode), `NPP041 INVALID_MODULE_TYPE`,
  `NPP050 INVALID_SCOPE_FOR_FILE_TYPE` (e.g. PARAMETER scope in a program).
- `NPP056 PARAMETER_COUNT_MISMATCH`, `NPP057 PARAMETER_NOT_OPTIONAL`,
  `NPP058 PARAMETER_TYPE_MISMATCH_BY_REFERENCE` (call-signature checking against the callee's PDA —
  natls cross-resolves the callee to validate the call!).
- `NPP047 CYCLOMATIC_INCLUDE` (circular copycode INCLUDE).
- DEFINE DATA detail: `NPP009 INVALID_ARRAY_BOUND`, `NPP010 INCOMPLETE_ARRAY_DEFINITION`,
  `NPP014/015 REDEFINE target/length`, `NPP022/023/024` (REDEFINE target can't be X-array / dynamic /
  contain dynamic), `NPP021 FILLER_MISSING_X`. So natparse fully parses the REDEFINE *clause* and array
  bounds — confirming these are in-scope `DEFINE DATA` grammar (our partial-verify items).
- Source: `libs/natparse/.../parsing/ParserError.java`.

## natlint rules (`NL001`–`NL041`) — the static-analysis catalog

A ready-made backlog of lint rules a Natural LSP can offer. Selected (id → message):

- NL001 Variable %s is unused · NL027 modified but never accessed · NL040 redefinition var unused ·
  NL022 Using %s is unused · NL039 Subroutine %s is unused · NL026 Prototype defined but not used ·
  NL025 Unreachable code · NL014 IGNORE is unnecessary.
- NL037 Called function has no prototype (compile risk) · NL036 Prototype defined more than once ·
  NL033 File name and function name differ (the referable-name mismatch above, as a lint) ·
  NL023 Module missing the source header (compile risk).
- NL006/NL007 discouraged operators (use EQ etc. / NatUnit) · NL010 prefer X over Y for consistency ·
  NL011 keyword used as identifier (prefix with `#`) · NL018 lowercase code discouraged ·
  NL019 variable should be qualified · NL003 module name has leading/trailing whitespace.
- NL031 END/GET/BACKOUT TRANSACTION outside a Program discouraged · NL002 WORK FILE stmt outside a
  Program · NL035 database statement found in copycode · NL028 INDEPENDENT (AIV) discouraged ·
  NL016 inline parameter discouraged · NL029 code between subroutines.
- NL020/NL032 condition always true / always false · NL005 value truncated at runtime ·
  NL009 FOR upper bound shouldn't be `*OCC` · NL041 duplicate attribute.
- NL012/NL013/NL015 COMPRESS heuristics (forgot NUMERIC / building CSV → ALL / work-file path needs
  LEAVING NO SPACE) · NL034 long line (mainframe visibility) · NL017 git-merge markers in comments ·
  NL008/NL024 NatUnit test rules.
- Source: `libs/natlint/.../analyzing/*Analyzer.java` (one analyzer per rule, IDs in
  `DiagnosticDescription.create`).

## LSP features natls serves (the full target surface)

From `NaturalDocumentService` + `ServerCapabilities`: definition, references, hover, completion (+
resolve), documentSymbol, workspaceSymbol, foldingRange, formatting, rename (+ prepareRename),
signatureHelp, codeAction (quickfix/refactor), codeLens (+ resolve), inlayHint (+ resolve),
**callHierarchy** (prepare + incoming + outgoing). The call-hierarchy support is notable for
lsp-graph: natls already builds a cross-module call graph. File watching is registered as
`**/Natural-Libraries/**/*.<EXT>`. Source: `libs/natls/.../languageserver/`.

## What this confirms / corrects in our KB

- CONFIRMS the 11 core extension→type mappings; tells us NS4/NS8/NST(/NS3/RESOURCE) are skipped even by
  the mature parser → lower priority for us.
- CONFIRMS steplib chain: current lib first, ordered steplibs, DDM separate namespace, implicit SYSTEM,
  `Natural-Libraries/<LIB>/` layout, `.natural`/`_naturalBuild` project file.
- CONFIRMS PERFORM inline-first (internal = symbol ref) vs external (= module ref).
- CONFIRMS FETCH RETURN is distinguished from plain FETCH.
- CORRECTS/ADDS: external SUBROUTINE and FUNCTION referable names come from the `DEFINE SUBROUTINE`/
  `DEFINE FUNCTION` source identifier, NOT the filename (and can be >8 chars).
- RESOLVES the "how is programming mode known" question: the NaturalONE `* :Mode S|R` source header.
- ADDS the `USING` data-area reference as a first-class module edge.
- ADDS a full lint-rule backlog and a parser-error taxonomy to model our diagnostics on.

## Open questions raised by natls

- Does the real Natural runtime walk **steplibs-of-steplibs** (natls only goes one level)? Affects deep
  resolution chains.
- natls validates **call parameter count/type** against the callee's PDA (`NPP056`–`NPP058`). Worth it
  for us, or out of scope for a regex extractor?
- `RESOURCE` is a natls TODO file type with no extension assigned in source — what extension/role does a
  Natural "resource" object have? (Not in our 15.)

## Sources

- Repo: https://github.com/MarkusAmshove/natls (MIT)
- File types: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalFileType.java
- Referable name: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalProjectFileIndexer.java
- Steplib resolution: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalLibrary.java
- Project/build file: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/project/BuildFileProjectReader.java
- Source header / comments: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/lexing/Lexer.java
- Programming mode: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/natural/project/NaturalProgrammingMode.java
- Parser errors: https://github.com/MarkusAmshove/natls/blob/main/libs/natparse/src/main/java/org/amshove/natparse/parsing/ParserError.java
- Implemented statements: https://github.com/MarkusAmshove/natls/blob/main/docs/implemented-statements.md
- Module-reference node interfaces: https://github.com/MarkusAmshove/natls/tree/main/libs/natparse/src/main/java/org/amshove/natparse/natural
