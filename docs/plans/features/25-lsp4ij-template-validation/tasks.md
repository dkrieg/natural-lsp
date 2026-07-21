# Tasks: LSP4IJ Template Validation & Fix (feature 25)

**Plan:** [`plan.md`](./plan.md)
**PRD requirements:** FR-45 (JetBrains client), FR-52 (importable LSP4IJ template), NFR-13 (minimal setup)
**Priority / phase:** P1 (post-assessment; real-user-reported — the shipped template does not import)
**Depends on:** feature 15 (editor clients & distribution)

---

## Current-state findings & impact

Surveyed the exact files this feature touches. Findings against the code as it *is* (code is ground truth):

- **No Go / `internal/model` / cache-format change.** This feature mirrors feature 15: it is
  editor-client + docs + a Node test only. Nothing crosses the Analyzer seam; nothing touches
  `internal/*`, `cmd/*`, `go.mod`, or the cache format (stays `0.6.0`). `just verify` remains Go-only
  and needs no change.

- **The template is genuinely broken (matches the plan's root cause).**
  `editors/jetbrains/lsp4ij-template/template.json` currently uses the invented schema
  `{ "serverName", "command", "mappings": [ { "fileNamePatterns", "languageId" } ], "initializationOptions" }`.
  None of these are LSP4IJ's real field names, so import produces an empty/invalid definition. The
  correct schema (externally verified against upstream `typescript-language-server` and `clangd`
  templates) is `{ "id", "name", "programArgs": { "default", "windows" }, "fileTypeMappings": [ { "fileType": { "name", "patterns" }, "languageId" } ] }`.
  The 15-extension pattern set (`*.NSP … *.NST`) is already present and correct — it only needs to move
  under `fileType.patterns` and be paired with the new top-level keys.

- **The validation test has a natural home in the existing VS Code Node/Mocha job.** CI's
  `vscode-extension` job (`.github/workflows/ci.yml` lines 97–146) runs `working-directory: editors/vscode`:
  `npm ci` → `npm run compile` → `npm run lint` → `npm run test:unit`. `test:unit` is
  `npm run compile && mocha "out/test/unit/**/*.test.js"` (`.mocharc.json` spec matches). Existing pure
  Mocha unit tests live at `editors/vscode/src/test/unit/{grammar,serverPath}.test.ts`; `grammar.test.ts`
  already reads a repo file from disk via `path.resolve(__dirname, "../../../syntaxes/…")`, establishing
  the pattern for a filesystem-reading unit test. A new `template.test.ts` there runs in CI **without a
  running IDE** and needs **no CI-yaml change** (the glob already picks it up). This is the recommended
  approach (Story 2 AC3 explicitly allows it). **Load-bearing path detail:** the compiled test runs from
  `editors/vscode/out/test/unit/`, so the template is reached at
  `path.resolve(__dirname, "../../../../jetbrains/lsp4ij-template/template.json")` (verified: four `..`
  segments — `unit → test → out → vscode → editors`, then down into `jetbrains/…`). Getting this relative
  path wrong is the single most likely implementation error; the RED step guards it (a wrong path throws
  on read, failing the test).

- **Docs describe the old field names.** Three doc surfaces reference the template's pre-filled fields
  with the invented names / vague phrasing:
  - `editors/jetbrains/README.md` — Option A step 4 says "Confirm the **Command** shows … and the
    **Mappings** tab…"; the intro para says import "pre-fills the server name, the … command, and all 15
    … mappings." (The **Option B manual** GUI steps stay valid — they describe IntelliJ's *GUI* labels,
    not JSON keys — but the "case-sensitive patterns" note should be aligned to `fileType.patterns`.)
  - `editors/jetbrains/lsp4ij-template/README.md` — "The server name, command, and the 15 … mappings …
    are pre-filled" (vague; fine to keep but worth aligning to real field names).
  - Root `README.md` JetBrains section (lines 452–468) — "ships an importable LSP4IJ server template
    (all 15 Natural file types, command `natural-lsp --stdio`)". Phrasing is field-name-agnostic and
    largely already correct; a light touch to name the real schema is enough.

- **Classification of acceptance criteria:**
  - Story 1 AC1/AC2 (rewrite schema; add `programArgs.windows`) → **new** (template rewrite).
  - Story 1 AC3 (GUI import verified in an IDE) → **cannot be automated** — recorded as a manual human
    checklist in docs (Task 4), per plan "Out of scope: automating the GUI import."
  - Story 2 AC1/AC2/AC3 (automated schema+coverage check in CI, no IDE) → **new** (Mocha unit test).
  - Story 3 AC1/AC2 (docs reflect real field names; document optional siblings) → **extend existing docs**.

- **Optional siblings decision (feeds Story 3 AC2):** the plan lists optional siblings
  (`initializationOptions.json`, `settings.json`, …). The current `template.json` carries an inline
  `"initializationOptions": {}` that does **not** belong in `template.json`. **Open question OQ-1**
  (below) asks whether to ship a separate `initializationOptions.json` (`{}`) or omit it entirely. Task 2
  drops the inline key regardless; Task 4 documents whatever is shipped.

- **Seam / guardrail check:** no LSP-facing Go code, no `internal/analysis` fixture convention
  (`testdata/*.NSx`) applies here — the "fixture" is `template.json` itself, and the RED step is a
  failing Mocha assertion against the current (wrong) file. Recorded so the reviewer does not expect Go
  testdata.

---

## Ordered tasks

Tasks are dependency-ordered. Task 1 (RED test) must land its failing assertions **before** Task 2
rewrites the template, so the test is proven to fail against the broken file and pass against the fix
(true red→green). Tasks 3 and 4 (docs) depend only on the final template shape from Task 2.

---

### Task 1 — Schema-validation unit test (Story 2: AC1, AC2, AC3)

**TDD agents:** `tdd-red` → `tdd-refactor`
(No `tdd-green` for production code in this task — the test's GREEN transition is produced by Task 2's
template rewrite. This task's deliverable is the *failing* test that pins the contract. Sequence: land
this test red, then Task 2 turns it green.)

**Type:** new (Node/Mocha unit test in the existing `vscode-extension` job).

**What it does:** adds `editors/vscode/src/test/unit/template.test.ts` — a pure Mocha unit test (no
`vscode` API, no electron, no running IDE) that reads the shipped LSP4IJ `template.json` from disk and
validates it against LSP4IJ's real schema and the Natural extension coverage requirement.

**File read (load-bearing path):**
`path.resolve(__dirname, "../../../../jetbrains/lsp4ij-template/template.json")`
(from compiled `editors/vscode/out/test/unit/` → `editors/jetbrains/lsp4ij-template/template.json`;
verified relative path). Parse with `JSON.parse(fs.readFileSync(..., "utf8"))` — mirrors
`grammar.test.ts`'s disk-read pattern.

**Assertions (expected results — all must hold against the *corrected* template):**
- `id` is a non-empty string.
- `name` is a string.
- `programArgs` is an object; `programArgs.default` is a non-empty string.
  (Recommended: also assert it contains `--stdio`. `programArgs.windows`, if present, is a non-empty
  string — see Task 2 AC.)
- `fileTypeMappings` is a non-empty array; **for each** entry: `entry.fileType` is an object,
  `entry.fileType.patterns` is a non-empty array of strings, and `entry.languageId` is a string.
- **Absence of the invented keys** — assert the top-level object has **no** `serverName`, `command`,
  `mappings`, or `initializationOptions` key, and that no mapping entry has a `fileNamePatterns` key
  (guards against a partial/regressed rewrite).
- **Coverage** — the union of all `fileType.patterns` across mappings, upper-cased, is a **superset** of
  the 15 documented extensions: `*.NSP *.NSN *.NSS *.NSC *.NSM *.NSL *.NSG *.NSA *.NSH *.NSD *.NS4 *.NS7
  *.NS3 *.NS8 *.NST`. (Superset, not exact-set, so a future addition of lower-case variants — e.g.
  `*.nsp` — does not break the test; compare a normalized/upper-cased set for the 15 required patterns.)
- **`languageId` is `natural`** for the Natural mapping (assert at least one mapping maps to `natural`).

**Expected RED (proof the test is load-bearing):** run against the **current** broken `template.json`,
the test must **fail** — the `id`/`programArgs.default`/`fileTypeMappings` assertions fail (those keys are
absent) and the "absence of `serverName`/`command`/`mappings`" assertions fail (those keys are present).
The RED agent must demonstrate this failure (`npm run test:unit` in `editors/vscode`, or `mocha` on the
compiled file) before the template is touched.

**Refactor:** extract small helpers (e.g. `patternSet(template)`, the required-extensions constant) for
readability; keep the test dependency-free (only `assert`, `fs`, `path` — all already used by sibling
tests, no new npm dependency, no `package.json` change).

**Definition of done:**
- [ ] `editors/vscode/src/test/unit/template.test.ts` exists and compiles under the existing `tsc`
      config (`npm run compile` clean; `noUnusedLocals`/`strict` satisfied).
- [ ] The test **fails** against the current (unfixed) `template.json` — RED demonstrated and recorded.
- [ ] The test asserts every item in the "Assertions" list above (real-schema keys present, invented
      keys absent, 15-extension coverage, `languageId: natural`).
- [ ] No `package.json` / `.mocharc.json` / `.github/workflows/ci.yml` change is required (the existing
      `out/test/unit/**/*.test.js` glob picks it up; the `vscode-extension` CI job runs it).
- [ ] `npm run lint` passes on the new file.

---

### Task 2 — Rewrite `template.json` to LSP4IJ's real schema (Story 1: AC1, AC2)

**TDD agents:** `tdd-green` → `tdd-refactor`
(This is the GREEN step for Task 1's test — the deliverable is the corrected data file that turns the
red assertions green.)

**Type:** new (full rewrite of the data file; no production code).

**What it does:** replaces `editors/jetbrains/lsp4ij-template/template.json` with the verified LSP4IJ
schema:

```json
{
  "id": "natural-lsp",
  "name": "Natural LSP",
  "programArgs": {
    "default": "natural-lsp --stdio",
    "windows": "natural-lsp.exe --stdio"
  },
  "fileTypeMappings": [
    {
      "fileType": {
        "name": "Natural",
        "patterns": ["*.NSP", "*.NSN", "*.NSS", "*.NSC", "*.NSM",
                     "*.NSL", "*.NSG", "*.NSA", "*.NSH", "*.NSD",
                     "*.NS4", "*.NS7", "*.NS3", "*.NS8", "*.NST"]
      },
      "languageId": "natural"
    }
  ]
}
```

**Key decisions embedded (from the plan, verified):**
- `programArgs` is an **OS-keyed object** with a `default` (Linux/macOS) and a `windows` entry
  (`natural-lsp.exe --stdio`) — satisfies Story 1 AC2.
- The inline `"initializationOptions": {}` is **removed** — it does not belong inside `template.json`
  (it lives in a separate `initializationOptions.json` sibling if shipped; see OQ-1 / Task 4).
- All 15 patterns move under `fileType.patterns`; `languageId: "natural"` stays at the mapping root.

**Expected GREEN:** with this file in place, Task 1's `template.test.ts` **passes** every assertion
(real keys present, invented keys absent, 15-extension coverage, `languageId: natural`).

**Refactor:** ensure valid, minimally-formatted JSON (matches upstream example-template style; trailing
newline; no comments — `template.json` must be strict JSON since LSP4IJ deserializes it). Optionally
mirror the `clangd/` example template's directory layout exactly to minimize future drift (plan Note).

**Definition of done:**
- [ ] `template.json` is valid JSON in the exact LSP4IJ schema (`id`, `name`, `programArgs.default` +
      `programArgs.windows`, `fileTypeMappings[].fileType.patterns` + `languageId`).
- [ ] All 15 `.NSx` extensions are present under `fileType.patterns`, mapped to `languageId: natural`.
- [ ] The invented keys (`serverName`, `command`, `mappings`, `fileNamePatterns`) and the inline
      `initializationOptions` are gone.
- [ ] Task 1's `template.test.ts` now **passes** (GREEN demonstrated via `npm run test:unit`).

---

### Task 3 — Align the two `editors/jetbrains` READMEs with the corrected template (Story 3: AC1, AC2)

**TDD agents:** `tdd-refactor` (docs-only; no test/code cycle — this is documentation maintenance, tracked
as a DoD checklist per the `/finalize-feature` docs-sync convention).

**Type:** extend existing docs.

**What it does:** updates the two JetBrains-specific docs to describe the **real** field names and the
optional-sibling layout.

- **`editors/jetbrains/README.md`:**
  - Option A intro paragraph: reword "pre-fills the server name, the `natural-lsp --stdio` command, and
    all 15 … mappings" to name the real fields — e.g. the template pre-fills the server `name`/`id`, the
    `programArgs` command (`natural-lsp --stdio`, with a `windows` variant), and the 15
    `fileType.patterns` mapped to language id `natural`.
  - Option A step 4: keep the GUI-label references (**Command**, **Mappings** tab) — those are IntelliJ's
    UI labels after import, still accurate — but make the "Command shows `natural-lsp --stdio`" and
    "Mappings lists the 15 patterns" wording consistent with the corrected template.
  - Option B (manual) steps: **leave the GUI steps as-is** (they describe the dialog, not JSON, and remain
    valid). Align the trailing "patterns are **case-sensitive**" note to say the patterns live under
    `fileType.patterns` in the template and that lower-case variants can be added there.
  - Document the optional siblings per OQ-1's resolution (either "the template dir may also carry an
    `initializationOptions.json` (`{}`) and `settings.json`" or note they are omitted).

- **`editors/jetbrains/lsp4ij-template/README.md`:**
  - Reword "The server name, command, and the 15 … mappings … are pre-filled" to the real field names
    (`id`/`name`, `programArgs`, `fileTypeMappings`), keeping it concise.
  - Keep the case-sensitivity note (align to `fileType.patterns`).

**Definition of done:**
- [ ] `editors/jetbrains/README.md` describes the template's pre-filled fields with correct LSP4IJ field
      names; the case-sensitivity note references `fileType.patterns`; the Option-B GUI steps are
      unchanged where still valid.
- [ ] `editors/jetbrains/lsp4ij-template/README.md` uses the real field names.
- [ ] Optional siblings (`initializationOptions.json`/`settings.json`) are documented consistent with
      OQ-1's resolution (present-and-documented, or omitted-and-noted).
- [ ] No dangling reference to `serverName`/`command`/`mappings`/`fileNamePatterns` remains in either
      README.

---

### Task 4 — Root README JetBrains section + manual-import verification checklist (Story 1: AC3; Story 3: AC1)

**TDD agents:** `tdd-refactor` (docs-only; DoD-checklist tracked).

**Type:** extend existing docs.

**What it does:**
- **Root `README.md` (JetBrains section, lines ~452–468):** the current phrasing ("ships an importable
  LSP4IJ server template (all 15 Natural file types, command `natural-lsp --stdio`)") is largely correct
  and field-name-agnostic; give it a light touch so it is consistent with the corrected schema (mention
  the `windows` launch variant if concise). No structural change — the pointer to
  `editors/jetbrains/README.md` stays.
- **Manual GUI-import checklist (Story 1 AC3):** since GUI import cannot be automated in CI, record a
  human verification checklist in `editors/jetbrains/README.md` (extend the existing **Verify** section)
  — after importing the template, confirm in a JetBrains IDE that the server's **Command** is populated
  (`natural-lsp --stdio`) and the **Mappings** tab lists the 15 patterns → `natural`, and the server
  reaches **started/connected** on opening a `.NSP` in the sample workspace. (The existing Verify section
  already has the go-to-definition steps; this adds the explicit "Command + Mappings populated after
  import" confirmation that AC3 calls for, marked clearly as a manual/human step.)

**Definition of done:**
- [ ] Root `README.md` JetBrains section is consistent with the corrected template (no invented field
      names; `windows` variant noted if included).
- [ ] `editors/jetbrains/README.md` Verify section includes a manual checklist item confirming
      **Command** and **Mappings** are populated after import (Story 1 AC3), explicitly marked as a human
      step (GUI import is not CI-automatable — plan Out-of-scope).
- [ ] All doc cross-links between root README ↔ `editors/jetbrains/README.md` ↔ template README remain
      valid.

---

## Reviews required (for `/review-feature`)

- **review-docs** — the primary review surface here: three doc files (`editors/jetbrains/README.md`,
  `editors/jetbrains/lsp4ij-template/README.md`, root `README.md`) plus the `CLAUDE.md` "Project state"
  note must all reflect the corrected template and carry no stale invented field names. This feature is
  docs-heavy (mirrors feature 15's no-code profile).
- **review-tests** — confirm `template.test.ts` is genuinely load-bearing: it fails against the old
  template (RED recorded) and passes against the new one, asserts both presence-of-real-keys and
  absence-of-invented-keys, and covers all 15 extensions; confirm it needs no running IDE and runs in the
  `vscode-extension` CI job.
- **CI verification** — confirm the new test executes under `npm run test:unit` in the `vscode-extension`
  job (no CI-yaml change needed) and that `just verify` (Go gate) is unaffected.
- **No seam/analysis/backend review needed** — no Go, no `internal/*`, no `internal/model`/cache change,
  nothing crosses the Analyzer seam.

---

## Open questions

- **OQ-1 (Story 3 AC2 — optional siblings):** should the template directory ship a separate
  `initializationOptions.json` containing `{}` (replacing the removed inline key), or omit it entirely
  (an empty `{}` is a no-op and arguably clutter)? The plan lists it as *optional*. **Recommendation:**
  omit it (the server needs no initialization options; an empty file adds nothing), and document in Task 3
  that no `initializationOptions.json`/`settings.json` is shipped because none is needed — add one later
  only if a real option arises. Confirm at plan-approval; Task 2 removes the inline key regardless, and
  Task 3/4 document whichever way this resolves.

- **OQ-2 (`programArgs.windows` command form):** the plan suggests `natural-lsp.exe --stdio`. Confirm this
  matches how the Windows binary is named/distributed (feature 15 / release pipeline). If the Windows
  artifact is not literally `natural-lsp.exe` on PATH, adjust the `windows` value accordingly. Low risk —
  `.exe` is the conventional Windows binary name and LSP4IJ resolves it via PATH.

- **OQ-3 (`fileType.name` value):** the verified schema nests `patterns` under `fileType` alongside a
  `name` (`"Natural"`). Upstream examples use a display name here. Confirm `"Natural"` is the intended
  file-type display label (no functional impact — it is a UI label; the `languageId: natural` is what
  binds to the server). Recommendation: keep `"Natural"`.
