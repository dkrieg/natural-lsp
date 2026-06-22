# Public Natural Example Projects & Source Corpora

Catalog of publicly available Software AG Natural source code on the web, for use as reference
material and as a source of sanitized test fixtures for the analyzer. Each entry records the URL,
content, constructs exercised, dialect/platform, license/usage terms (critical for committing
fixtures), and a representativeness/trust assessment.

**Status: verified (2026-06-20)** — every repository/page below was confirmed to exist and its
contents/file counts inspected via the GitHub API or the live documentation site on 2026-06-20.
File counts are point-in-time and may drift. License facts are load-bearing — re-verify before
committing any derived fixture.

## Reusability summary (read this first)

We can only turn **non-proprietary, appropriately-licensed** code into committed fixtures. Of the
public corpus:

- **Permissively licensed (usable, with attribution):** the `natls` test corpus (MIT) and the
  Software AG sample/education repos (Apache-2.0). These are the only safe bases for committed
  fixtures.
- **No license stated (NOT usable as committed fixtures):** every community/individual GitHub repo
  found (foleymd, coursework repos, etc.). "Public on GitHub" is **not** a license — absent an
  explicit license these are all-rights-reserved. Use for reading/learning only; do not copy into
  `testdata/`.
- **Documentation example programs:** viewable inline on documentation.softwareag.com but governed
  by Software AG documentation terms, not an open-source license. Safe to *read and learn from*;
  re-typing a trivial 5-line snippet as an original fixture is fine, but do not bulk-copy.

The public corpus is **thin but adequate**: a handful of high-quality permissively-licensed sources,
surrounded by many tiny unlicensed coursework repos. There is no large open Natural codebase.

---

## Tier 1 — best fixture candidates (permissive license, realistic layout)

### 1. MarkusAmshove/natls — Natural Language Server test corpus  ★ strongest source
- **URL:** https://github.com/MarkusAmshove/natls
- **What:** A mature, independent open-source Language Server + linter for Natural (Java). Directly
  analogous to this project. Its strength for us is its **test fixture corpus**.
- **How much / what kind:** ~186 real `.NSx` fixture files under
  `libs/natlint/src/test/resources/projects/.../Natural-Libraries/<LIB>/<OBJECT>.NSx`, i.e. the exact
  `Natural-Libraries/LIBRARY/OBJECT.NSx` on-disk layout this analyzer targets. Breakdown (2026-06-20):
  `.NSN` 89, `.NSC` 26, `.NS7` 15, `.NSP` 10, `.NSS` 10, `.NSD` 10, `.NSA` 9, `.NSL` 9, `.NSG` 4,
  `.NSH` 2, `.NSM` 2.
- **Constructs covered:** very broad and deliberately edge-case-rich — CALLNAT, external + inline
  subroutines (incl. long subroutine names, the >8-char edge case), INCLUDE/copycode, DEFINE DATA
  (LDA/GDA/PDA/USING), DDMs, user-defined functions (`.NS7`), NATUnit test cases, editorconfig/format
  variants. Organized into many small focused `projects/` each isolating one behavior — ideal for
  targeted regression fixtures.
- **Dialect/mode/platform:** Natural for Linux/Unix/Windows source layout (NaturalONE-style on-disk
  `Natural-Libraries`); structured mode. Same target shape as natural-lsp.
- **License:** **MIT** (permissive; attribution required). Usable as a base for committed fixtures.
- **Representativeness/trust:** High. Built by a Natural practitioner specifically to test a parser,
  so it concentrates exactly the constructs and edge cases an analyzer must get right. The fixtures
  are small and synthetic (not production business logic), which is a plus for sanitization but means
  they under-represent messy real-world style.
- **Fixture candidate:** YES — primary. Mirror its `projects/` isolation approach. Attribute MIT
  origin where a fixture is derived.

---

## Tier 2 — Software AG official samples (Apache-2.0, realistic application code)

### 2. SoftwareAG/adabas-natural-for-ajax-devops-sample-application — "Sunny Islands" cruise app
- **URL:** https://github.com/SoftwareAG/adabas-natural-for-ajax-devops-sample-application
- **Path of interest:** `SunnyIslands/Natural-Libraries/RDCRUISE/`
- **What:** A complete sample business application (cruise booking) used by Software AG to demo
  DevOps workflows with Natural for Ajax.
- **How much / what kind (2026-06-20):** `.NSN` 11, `.NSA` 9, `.NSD` 4, `.NSP` 3, `.NSL` 2, `.NSC` 1,
  `.NSG` 1. Richest *application-shaped* mix with a real GDA, PDAs, LDAs and a copycode.
- **Constructs covered:** DEFINE DATA GLOBAL/LOCAL USING, CALLNAT (naming convention `XXGET-N`,
  `XXLIST-N`), PERFORM, FETCH RETURN, PROCESS PAGE (Natural for Ajax UI), DECIDE ON, MOVE/COMPRESS/
  SEPARATE/EXAMINE, EXPAND/REDUCE ARRAY. Multi-library resolution across RDCRUISE + shared libs.
- **Dialect/mode/platform:** Natural for Linux/Unix/Windows + Natural for Ajax (web), NaturalONE
  import format; structured mode. Note: web/PROCESS-PAGE constructs are *less* relevant to a
  mainframe-leaning analyzer than the data/call layer.
- **License:** **Apache-2.0**. Usable for committed fixtures (preserve NOTICE/attribution).
- **Representativeness/trust:** High for "what a real NaturalONE app library looks like" — proper
  library structure, naming conventions, cross-object calls. The Ajax UI layer is a distraction for
  our purposes; the Subprograms/data-areas are the valuable part.
- **Fixture candidate:** YES — best source for a realistic multi-object library + steplib-resolution
  fixture. Prefer the Subprograms and data areas over the PROCESS PAGE programs.

### 3. SoftwareAG/adabas-natural-devops-sample-application — "NaturalCruise"
- **URL:** https://github.com/SoftwareAG/adabas-natural-devops-sample-application
- **What:** Sibling DevOps sample (NaturalCruise), more classic (maps + helproutines, no Ajax).
- **How much / what kind (2026-06-20):** `.NSP` 7, `.NSM` 3, `.NSD` 2, `.NSH` 2, `.NSA` 1, `.NSL` 1,
  `.NSN` 1. **The best public source of `.NSM` maps and `.NSH` helproutines** found.
- **Constructs covered:** programs driving maps (INPUT USING MAP), helproutines, DDM-backed database
  access, PDA/LDA. Adabas EMPLOYEES/VEHICLES-style demo data assumed (DB not bundled).
- **Dialect/mode/platform:** NaturalONE import format; structured mode; map-and-helproutine
  (3270-style) UI rather than web. Closer to mainframe-style interactive apps.
- **License:** **Apache-2.0**. Usable for committed fixtures.
- **Representativeness/trust:** High. Use this when you need map / helproutine / INPUT-USING-MAP
  coverage, which natls barely has (only 2 `.NSM`).
- **Fixture candidate:** YES — go-to for map (`.NSM`) and helproutine (`.NSH`) fixtures.

### 4. SoftwareAG/adabas-natural-code-samples — community snippet collection
- **URL:** https://github.com/SoftwareAG/adabas-natural-code-samples
- **What:** ~65+ folders of focused Natural pattern snippets (arrays, date/time, strings, dynamic
  variables, windowing, XML parsing, file I/O), each with a README + the code **as text snippets in
  the README**, NOT as standalone `.NSx` files.
- **Constructs covered:** broad statement-level coverage; good for "does the regex match this
  statement form" checks. Weak for cross-file/resolution because there are no real object files or
  library structure.
- **Dialect/mode/platform:** mixed LUW/mainframe, structured mode; stated as community-contributed,
  as-is.
- **License:** **Apache-2.0**.
- **Representativeness/trust:** Medium. Authoritative-ish (Software AG org) but snippet-form and
  uneven. Best as a checklist of statement variants, not as drop-in object fixtures.
- **Fixture candidate:** PARTIAL — extract individual statement forms into hand-built `.NSx` fixtures;
  cannot use as-is for file/resolution tests.

### 5. SoftwareAG/adabas-natural-education-package (archived)
- **URL:** https://github.com/SoftwareAG/adabas-natural-education-package
- **What:** Archived (Jan 2023) teaching package: tutorial programs (Hello World, conditionals,
  arrays, DB retrieval), a VM image, HTML course material, CSV demo data.
- **How much / what kind (2026-06-20):** `.NSP` 19, `.NSD` 4 (e.g. `HISTOPGM.NSP`, `WORKOPGM.NSP`,
  `WORKFPGM.NSP`, `CRUISE.NSD`). Programs only — no subprograms/maps/data areas to speak of.
- **Constructs covered:** WRITE/DISPLAY, DECIDE ON, 2-D arrays, REPEAT + ESCAPE, READ/HISTOGRAM,
  WRITE/READ WORK FILE. Beginner-level, single-object programs.
- **Dialect/mode/platform:** explicitly mainframe + LUW; structured mode. Simplest, cleanest examples.
- **License:** **Apache-2.0**.
- **Representativeness/trust:** Medium — pedagogical, simple, very clean. Good for basic
  statement-coverage and reporting-vs-structured baseline fixtures; not for cross-file resolution.
- **Fixture candidate:** YES (low complexity) — good for simple, unambiguous single-program fixtures.

---

## Tier 3 — official documentation example programs (read/learn, restricted reuse)

### 6. Natural Programming Guide example library SYSEXPG (+ SYSEXSYN for statements)
- **URLs:**
  - Programming Guide referenced examples: https://documentation.softwareag.com/naturalONE/natONE912/natmf/pg/pg_exas.htm
  - Statements referenced examples (SYSEXSYN): https://documentation.softwareag.com/natural/nat6313win/sm/sm-over.htm
- **What:** ~30+ canonical example programs from the official Programming Guide, shown inline as
  source + expected output. Shipped in the on-mainframe libraries `SYSEXPG` (PG examples, incl.
  functions) and `SYSEXSYN` (statement examples) — those libraries are only on a licensed install,
  but the *source is viewable in the public docs*.
- **Constructs covered:** the authoritative reference set — READ/FIND with clauses, ACCEPT/REJECT,
  AT START/END OF DATA, AT BREAK, nested loops, DISPLAY/WRITE with edit masks/headers/page breaks,
  COMPUTE/MOVE/COMPRESS, system variables (`*NUMBER`, `*COUNTER`, `*ISN`). Uses EMPLOYEES/VEHICLES
  demo files.
- **Dialect/mode/platform:** Natural for Mainframes (NaturalONE docs set); predominantly structured
  mode; canonical/idiomatic.
- **License/usage:** Software AG **documentation terms**, NOT an open-source license. Read and learn
  freely; do not bulk-copy into the repo. Re-authoring a tiny, generic snippet as an original fixture
  is defensible; copying whole programs is not.
- **Representativeness/trust:** Highest *authoritativeness* (Software AG's own canonical examples) —
  the reference for "what idiomatic Natural looks like." Trust for language correctness; treat
  cautiously for licensing.
- **Fixture candidate:** NO (don't copy) — but invaluable as the gold standard to *model* hand-written
  fixtures on.

---

## Tier 4 — community/individual repos (reference only, NO usable license)

Many small repos exist (mostly Portuguese/Spanish coursework + a few practitioner samples). Searched
GitHub 2026-06-20; representative examples:

- **foleymd/natural-work-samples** — https://github.com/foleymd/natural-work-samples — the most
  realistic of the bunch: large (~1,200+ line) production-style **mainframe** batch programs, files
  stored *extensionless* (named by object), README states data sources were obscured. Constructs:
  DEFINE DATA with multiple views/REDEFINEs, 20+ PERFORM subroutines, CALLNAT to shared modules,
  READ MULTI-FETCH, FIND, DECIDE FOR FIRST CONDITION, WRITE WORK FILE, EXAMINE/COMPRESS/SUBSTRING.
  **No license stated → not usable as a committed fixture.** Excellent *reading* sample for real
  mainframe batch idiom.
- Coursework/lab repos (e.g. `MatiDiyo/Curso-Natural-Adabas`, `fmarqueseti/SAG-NaturalAdabasBasics`,
  `rosivaldocamjr/natural_adabas`, numerous `*natural-adabas*` forks): small, beginner-level, mixed
  quality, **no licenses**, often store code as `.txt` or extensionless. Reference only.

**Rule:** none of Tier 4 may be copied into `testdata/`. They are useful only to corroborate what
real-world Natural looks like.

---

## Notable gaps / honest assessment

- No large, permissively-licensed, *production-scale* Natural codebase exists publicly. The realistic
  application examples are the two Software AG cruise demos (small, ~10–25 objects each).
- **Reporting-mode** code is under-represented everywhere — almost all public samples are structured
  mode. We will likely have to **hand-author** reporting-mode fixtures (DO/DOEND, loop-collapsing
  `END`/`LOOP`, undeclared variables) modeled on the docs. (See modes-and-dialects.md.)
- **Mainframe fixed-column / mainframe-editor** source is scarce; the permissive samples are all
  NaturalONE LUW free-format layout. Confirms why the analyzer should not encode mainframe column
  rules yet (open question in INDEX).
- Dynamic calls (`CALLNAT #VAR`) and `&`/`*LANGUAGE` substitution are rare in public samples — these
  dynamic/unresolvable call scenarios will mostly need hand-built fixtures.

## Sources

- https://github.com/MarkusAmshove/natls (MIT; file counts via GitHub API 2026-06-20)
- https://github.com/SoftwareAG/adabas-natural-for-ajax-devops-sample-application (Apache-2.0)
- https://github.com/SoftwareAG/adabas-natural-devops-sample-application (Apache-2.0)
- https://github.com/SoftwareAG/adabas-natural-code-samples (Apache-2.0)
- https://github.com/SoftwareAG/adabas-natural-education-package (Apache-2.0, archived)
- https://documentation.softwareag.com/naturalONE/natONE912/natmf/pg/pg_exas.htm (SYSEXPG examples)
- https://documentation.softwareag.com/natural/nat6313win/sm/sm-over.htm (SYSEXSYN statement examples)
- https://github.com/foleymd/natural-work-samples (no license — reference only)