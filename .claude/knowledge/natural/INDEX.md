# Natural Knowledge Base — Index

Working reference on Software AG Natural, maintained by the `natural-expert` agent. Each topic file
holds verified facts with sources. Read this index first, then the relevant topics.

**Status legend:** `verified (date)` = corroborated against an authoritative source · `needs-verification`
= seeded belief, confirm before relying on it · `unverified` = recorded but unconfirmed.

## Topics

| File | Covers | Overall status |
|------|--------|----------------|
| [file-extensions.md](file-extensions.md) | `.NSx` object types and what each maps to | verified (2026-06-23) |
| [calls-and-resolution.md](calls-and-resolution.md) | CALLNAT / PERFORM / FETCH / RUN / INCLUDE, steplib resolution | verified (2026-06-23) |
| [data-definition.md](data-definition.md) | DEFINE DATA, LDA/GDA/PDA, level structure | verified (2026-06-20); array/REDEFINE grammar confirmed |
| [modes-and-dialects.md](modes-and-dialects.md) | structured vs reporting mode, mainframe vs Linux/NaturalONE | verified (2026-06-23); column rules confirmed free-format |
| [example-projects.md](example-projects.md) | public Natural source corpora & fixture candidates (licenses) | verified (2026-06-20) |
| [natls-prior-art.md](natls-prior-art.md) | MarkusAmshove/natls: prior-art Natural LSP — scope, file types, resolution, source header, lint/parser diagnostics, LSP features | verified (2026-06-21) |

## Open questions (to verify on next relevant task)

- **Reporting-mode fixtures don't exist publicly** — the public corpus is almost entirely structured
  mode (see example-projects.md). natls itself does NOT support reporting mode at all. Reporting-mode
  regression fixtures (DO/DOEND, loop-collapsing `END`/`LOOP`, undeclared vars) will have to be
  hand-authored from the docs.
- **License hygiene for fixtures** — only natls (MIT) and the Software AG sample/education repos
   (Apache-2.0) are safe to derive committed fixtures from; all community GitHub repos found are
  unlicensed. Decide on the attribution mechanism for derived fixtures before importing any.
- **Map (`.NSM`) / helproutine (`.NSH`) coverage is scarce** — only the NaturalCruise DevOps sample
  has a meaningful set; confirm the analyzer's map/INPUT-USING-MAP handling against it. (Note: natls's
  MAP type CAN carry a DEFINE DATA and a body.)
- **Project-file discovery** — natls locates the workspace by a `.natural` or `_naturalBuild` build
  file whose parent holds `Natural-Libraries/<LIB>/`. Our `.natural-lsp.toml` sentinel is our own; decide
  whether to also read the native `.natural` build file for steplib config.

## Changelog

- 2026-06-23 — VERIFIED critical open questions from KB:
    - **Steplib recursion:** Natural **does search transitively** through chained steplibs (up to 8 steplibs supported). Confirmed in Software AG performance docs.
    - **CALLNAT/FETCH name length:** CALLNAT constant = 1–32 chars (9.3.1+), variable = 1–8 chars. FETCH constant/variable = 1–8 chars only (not 1–32).
    - **&/LANGUAGE substitution:** Confirmed for both CALLNAT and FETCH; treat literals with `&` as dynamic/unresolvable.
    - **RESOURCE object type:** `.NKR` extension for shared resources; `.NR3` for private dialog resources.
    - **DDM column grammar:** Exact order is `C T L Name F Length S D` (DB is optional toggle).
    - **NaturalONE column rules:** Confirmed **free-format** (Eclipse-based); no fixed columns.
    - **Reporting-mode grammar:** `DO/DOEND` blocks, `END`/`LOOP` loop-collapsing, `END-IF` causes error in reporting mode.
    - **Array-bound grammar:** Inline syntax `(A10/1:5)` confirmed; multi-dim `(1:5,1:3)`; extensible `(A10/1:*)`.
    - **Project-file discovery:** `.natural` file used in NaturalONE for unsecured steplib config.
- 2026-06-21 — ADDED topic `natls-prior-art.md`: deep study of MarkusAmshove/natls (MIT), the mature
  prior-art parser-based Natural LSP, read from source. Key findings written back:
    - file-extensions: CONFIRMED the 11 core ext→type mappings independently; natls SKIPS NS4/NS8/NST/NS3
      (TODO in source) → lower priority. ADDED canHaveDefineData/canHaveBody per-type predicates (MAP can
      have both; data areas + DDM cannot have a body). ADDED that `.NSS`/`.NS7` referable names come from
      the in-source `DEFINE SUBROUTINE`/`DEFINE FUNCTION` identifier (not filename, can be >8 chars).
      ADDED that `.NSD` is a tabular non-source format needing a separate parser.
    - modes-and-dialects: RESOLVED the "how is mode known" question — NaturalONE source-header block
      (`* >Natural Source Header` … `* :Mode S|R` … `* <Natural Source Header`) declares mode, code page,
      line increment. ADDED verified comment markers (`*` line-start guard, `/*` inline; `/` vs array-bound
      ambiguity).
    - calls-and-resolution: CROSS-CHECKED resolution — one `IModuleReferencingNode` covers CALLNAT/PERFORM
      (external)/FETCH/INCLUDE/function-call/`USING`; PERFORM inline-first is structural; FETCH RETURN
      distinguished; current-lib→ordered-steplibs (ONE level in natls), DDM separate namespace, implicit
      SYSTEM steplib, `.natural`/`_naturalBuild` + `Natural-Libraries/<LIB>/` layout.
    - data-definition: CONFIRMED array bounds + REDEFINE clause are in-scope (natls parser errors);
      clarified "REDEFINE statement (reporting, not planned)" ≠ "REDEFINE clause in DEFINE DATA";
      ADDED comma-as-decimal-separator gotcha.
    - INDEX: resolved/refined 6 open questions; added 3 new (steplib transitivity, DDM tabular grammar,
      RESOURCE type, native `.natural` build-file reading).
- 2026-06-21 — file-extensions: re-verified full source table against NaturalONE editors "General
  Information" page. ADDED standalone-vs-fragment column (copycode/data-areas/DDM/text = fragments).
  ADDED source-vs-generated `NS*` / `NG*` distinction (NSP→NGP, map source→`.NGM`; LSP indexes `NS*` only).
  CONFIRMED `.NAT` is NOT an official Natural extension (only on third-party file sites). CONFIRMED
   `.NSX/.NSV/.NSE/.NSK/.NSB` do not exist as object types; `NSF` = product (Natural SAF Security), not an
  extension; `.SAG` = work-file binary extension, not source.
- 2026-06-20 — ADDED topic `example-projects.md`: catalog of public Natural source corpora and
  fixture candidates. Verified existence + inspected file counts via GitHub API / live docs.
  Strongest fixture sources: MarkusAmshove/natls (MIT, ~186 `.NSx` fixtures in standard library
  layout), and the two Software AG cruise demos (Apache-2.0). Documentation SYSEXPG/SYSEXSYN examples
  are authoritative but doc-licensed (read-only). All community repos found are unlicensed → reference
  only. Public corpus assessed as thin-but-adequate; reporting-mode and mainframe fixed-column code
  under-represented. Added 4 open questions.
- 2026-06-20 — Full sweep; all four topics verified against official Software AG docs.
    - file-extensions: ADDED missing types `.NS4` (class), `.NS7` (function), `.NS3` (dialog),
      `.NS8` (adapter), `.NST` (text); CONFIRMED `.NSD` = DDM. Set verified.
    - calls-and-resolution: CORRECTED/expanded several facts —
      (1) CALLNAT name = constant 1–32 OR variable 1–8 (variable form is the dynamic case);
      (2) `&`/`*LANGUAGE` substitution means a literal name containing `&` is NOT a clean static target;
      (3) FETCH/RUN program name CAN be a variable → also a `CALLS_DYNAMIC` source, not always
          `NAVIGATES_TO` (seed implied static only);
      (4) RUN is a SYSTEM COMMAND (`RUN [REPEAT] [program-name [library-id]]`), not a regular statement,
          and its optional library-id bypasses the steplib chain;
      (5) INCLUDE copycode-name is LITERAL-ONLY (never a variable) — always resolvable except for `&`;
      (6) PERFORM resolves INLINE subroutine first, then external (`.NSS`) — analyzer must scan
          `DEFINE SUBROUTINE` before emitting external edge;
      (7) documented exact 4-level steplib search order (current lib → steplibs → *STEPLIB →
          SYSTEM/FUSER then SYSTEM/FNAT).
    - data-definition: CONFIRMED clauses (LOCAL/GLOBAL/PARAMETER/INDEPENDENT/CONTEXT/OBJECT), mandatory
      `END-DEFINE`, GLOBAL-then-PARAMETER ordering rule, and format codes (A U N P I B F L D T C).
    - modes-and-dialects: CONFIRMED structured (`END-...` closers) vs reporting (`DO/DOEND`, loop-collapsing
      `END`/`LOOP`; `END-IF` etc. error) and reporting-mode undeclared variables/DDM refs; noted FETCH/RUN
      name case is NOT translated (exception to case-insensitivity).
- (seed) Created index and topic stubs from project README/CLAUDE.md. None web-verified yet.
