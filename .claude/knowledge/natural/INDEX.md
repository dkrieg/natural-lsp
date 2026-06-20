# Natural Knowledge Base — Index

Working reference on Software AG Natural, maintained by the `natural-expert` agent. Each topic file
holds verified facts with sources. Read this index first, then the relevant topics.

**Status legend:** `verified (date)` = corroborated against an authoritative source · `needs-verification`
= seeded belief, confirm before relying on it · `unverified` = recorded but unconfirmed.

## Topics

| File | Covers | Overall status |
|------|--------|----------------|
| [file-extensions.md](file-extensions.md) | `.NSx` object types and what each maps to | verified (2026-06-20) |
| [calls-and-resolution.md](calls-and-resolution.md) | CALLNAT / PERFORM / FETCH / RUN / INCLUDE, steplib resolution | verified (2026-06-20) |
| [data-definition.md](data-definition.md) | DEFINE DATA, LDA/GDA/PDA, level structure | verified (2026-06-20); array/REDEFINE grammar partial |
| [modes-and-dialects.md](modes-and-dialects.md) | structured vs reporting mode, mainframe vs Linux/NaturalONE | verified (2026-06-20); column rules unverified |
| [example-projects.md](example-projects.md) | public Natural source corpora & fixture candidates (licenses) | verified (2026-06-20) |

## Open questions (to verify on next relevant task)

- **Exact array-bound grammar inside DEFINE DATA** — placement variants of `(format/lower:upper)` and
  multi-dimensional arrays, and how they interleave with REDEFINE. Recorded but only partially confirmed.
- **Column-sensitivity / fixed-format rules** for NaturalONE free-format source vs the mainframe editor.
  Multi-line continuation is confirmed; precise column positions are NOT — do not encode columns yet.
- **User-defined function call syntax** (`.NS7`, `name(<...>)`) — confirm exact call grammar and whether
  it should be a `CALLS` edge distinct from CALLNAT; not deeply verified this pass.
- **CALLNAT/FETCH name length mismatch** — CALLNAT variable name limited to 1–8 chars but constant up to
  32; confirm whether object names longer than 8 chars can ever be reached dynamically (affects whether
  long-named subprograms are ever `CALLS_DYNAMIC` candidates).
- **`&`/`*LANGUAGE` substitution** — confirm the analyzer's intended edge type for literals containing
  `&` (treat as dynamic vs resolved-with-wildcard).
- **Reporting-mode fixtures don't exist publicly** — the public corpus is almost entirely structured
  mode (see example-projects.md). Reporting-mode regression fixtures (DO/DOEND, loop-collapsing
  `END`/`LOOP`, undeclared vars) will have to be hand-authored from the docs.
- **License hygiene for fixtures** — only natls (MIT) and the Software AG sample/education repos
  (Apache-2.0) are safe to derive committed fixtures from; all community GitHub repos found are
  unlicensed. Decide on the attribution mechanism for derived fixtures before importing any.
- **Map (`.NSM`) / helproutine (`.NSH`) coverage is scarce** — only the NaturalCruise DevOps sample
  has a meaningful set; confirm the analyzer's map/INPUT-USING-MAP handling against it.

## Changelog

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
    (4) RUN is a SYSTEM COMMAND (`RUN [REPEAT] [program-name [library-id]]`), not a normal statement,
        and its optional library-id bypasses the steplib chain;
    (5) INCLUDE copycode-name is LITERAL-ONLY (never a variable) — always resolvable except for `&`;
    (6) PERFORM resolves INLINE subroutine first, then external `.NSS` — analyzer must scan
        `DEFINE SUBROUTINE` before emitting external edge;
    (7) documented exact 4-level steplib search order (current lib → steplibs → *STEPLIB →
        SYSTEM/FUSER then SYSTEM/FNAT).
  - data-definition: CONFIRMED clauses (LOCAL/GLOBAL/PARAMETER/INDEPENDENT/CONTEXT/OBJECT), mandatory
    `END-DEFINE`, GLOBAL-then-PARAMETER ordering rule, and format codes (A U N P I B F L D T C).
  - modes-and-dialects: CONFIRMED structured (`END-...` closers) vs reporting (`DO/DOEND`, loop-collapsing
    `END`/`LOOP`; `END-IF` etc. error) and reporting-mode undeclared variables/DDM refs; noted FETCH/RUN
    name case is NOT translated (exception to case-insensitivity).
- (seed) Created index and topic stubs from project README/CLAUDE.md. None web-verified yet.