# Tasks: Editor clients (feature 15)

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements:** FR-44, FR-45, FR-46; NFR-10, NFR-12, NFR-13
**Nature of this feature:** UNUSUAL for this repo — it is about **editor clients and
distribution**, not Go analyzer/extraction work. The primary net-new artifact is a real
**VS Code extension in TypeScript** under `editors/vscode/`. There are **no** `.NSx`
`testdata/` extraction fixtures here; the RED signals are TypeScript integration/unit tests
(`@vscode/test-electron`), compile/typecheck gates, and concrete verifiable checks (config
parses, sample workspace launches, `--version`/stdio smoke passes) rather than Go tests. The
Analyzer seam and `internal/model` are **not touched** — no server code changes, no cache-format
bump. Docs-only tasks are marked **[doc]**; code tasks are marked **[code]**.

---

## Approved scope decisions (checkpoint)

Resolved with the user before implementation — these override the "Open questions" below:

1. **Clients live in this repo** (`editors/vscode/`, `editors/jetbrains/`).
2. **Ship a basic TextMate grammar** (T3), not association-only.
3. **Full `@vscode/test-electron` test harness**, wired into CI as a separate Node job (T4/T5).
4. **Publisher `dkrieg`; extension version tracks the server release line** (matches the
   `github.com/dkrieg/natural-lsp` module path).
5. **Package a `.vsix` via `vsce`** (build + package, attachable to a GitHub Release) — **no
   Marketplace publish** step and no publisher-account/`VSCE_PAT` dependency in this feature (T9).
6. **NFR-12:** `go install` + pre-built binary are the documented install paths; a native
   package-manager channel (Homebrew/Scoop/apt) is **future work**, noted not built (T8).

---

## Current-state findings & impact

Surveyed the repo as it actually is (code is ground truth):

**Already satisfied — record and skip, do not rebuild:**
- **Server CLI surface (Story 1/4 dependency).** `cmd/natural-lsp/main.go` already implements
  `--version` (`natural-lsp 0.0.0-dev`, ldflag-overridden), `--stdio` (Content-Length-framed
  JSON-RPC over stdio, clean EOF exit), and `--init`. No CLI change is needed by any client — the
  VS Code extension launches `natural-lsp --stdio`, exactly the Neovim/Zed/Helix/JetBrains config.
- **Distribution build infra (Story 4, NFR-10).** `justfile` `release <version>` already
  cross-builds all 5 targets (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
  `windows/amd64`) with `-trimpath -ldflags "-s -w -X main.version=…"` into `dist/` plus a
  `checksums.txt`. `.github/workflows/release.yml` (workflow_dispatch) validates the version,
  guards against an existing tag, and publishes all binaries + checksums via `gh release create`.
  **This is done and works** — Story 4's "publish native binaries" AC is already met. Feature 15
  only *verifies + documents* it; it does not rebuild it.
- **Install-path docs (Story 4, NFR-12).** Root `README.md` §Installation already documents the
  pre-built binary, build-from-source, and `go install` paths.
- **Other-editor docs scaffolding (Story 3, FR-46).** Root `README.md` §"Editor setup" already
  contains config blocks for Neovim (nvim-lspconfig, `root_pattern('.natural-lsp.toml', '.git')`),
  Zed, Helix (all 15 file-types listed), and JetBrains (LSP4IJ). These exist but are **unverified**
  — feature 15's Story-3/Story-2 work is to *validate them against a sample workspace* and correct
  any drift, not to author them from scratch.

**Divergences / gaps found (drive the tasks):**
- The **VS Code extension does not exist** — `editors/vscode/README.md` is a stub
  ("Status: stub — not yet implemented"). No `package.json`, no `src/extension.ts`, no grammar, no
  language contribution. This is the main build (Story 1) and the bulk of the work.
- The **JetBrains integration is a doc stub** — `editors/jetbrains/README.md` says "not yet
  implemented." The root README has an LSP4IJ walkthrough, but there is no reproducible,
  version-controlled config artifact under `editors/jetbrains/` and the steps are unverified
  (Story 2).
- **No Node/TypeScript toolchain in the repo or CI.** `just verify` is Go-only (fmt-check,
  lint-tests, vet, build, `go test -race`, integration). Story 1's test-harness AC (per the design
  decision: `@vscode/test-electron` headless tests + tsc typecheck + lint, wired into CI) requires
  adding a **separate Node/npm CI job** — it must NOT be folded into `just verify` (that gate stays
  Go-only and fast). This is a CI-surface change → flag `review-docs` and note it for `review-ci`.
- README §Editor setup lists the VS Code file associations informally ("`.NSP`, `.NSN`, … and other
  `.NSx` types"); the extension's `package.json` `languages.contributes` must enumerate **all 15**
  extensions explicitly: `.NSP .NSN .NSS .NSC .NSM .NSL .NSG .NSA .NSH .NSD .NS4 .NS7 .NS3 .NS8 .NST`.

**Shared-contract impact:** none. No `internal/model`, `Analyzer`, index, or cache change.
No server Go code changes. The Analyzer seam is untouched.

**Criterion → task traceability:**

| Criterion | Task(s) |
|---|---|
| S1: opening a Natural file activates + launches server | T1, T2 |
| S1: zero-config when `natural-lsp` on PATH | T2 |
| S1: `naturalLsp.serverPath` overrides binary location | T2 |
| S1: Natural file types associated (all 15) | T1 |
| S1 design decision: TextMate grammar ships | T3 |
| S1 design decision: `@vscode/test-electron` harness + tsc + lint in CI | T4, T5 |
| S2: reproducible first-party JetBrains/LSP4IJ path (incl. Community) | T6 |
| S2: file-type association | T6 |
| S2: documented reproducible setup | T6 |
| S3: working docs for Neovim, Zed, Helix | T7 |
| S3: file-type association + sentinel root detection documented | T7 |
| S3: following docs yields working navigation | T7 |
| S4: native binaries for major platforms/arches | T8 (verify only — already built) |
| S4: multiple documented install paths | T8 |
| S4: fresh binary reports `--version` + passes stdio smoke | T8 |
| S1 decision: `.vsix` package built (no Marketplace publish) | T9 |

---

## Ordered task list

Order: VS Code extension scaffold + activation (Story 1, the main build) → grammar → test
harness + CI wiring → JetBrains reproducible config (Story 2) → other-editor docs verification
(Story 3) → distribution verification/docs (Story 4).

> The TDD agents (`tdd-red` → `tdd-green` → `tdd-refactor`) apply to code tasks. For TypeScript
> tasks the "test" is a Node test (Mocha/`@vscode/test-electron` or a unit test) plus the tsc
> typecheck; for doc-only tasks the RED→GREEN is a concrete verifiable check (config parses, sample
> workspace launches, smoke script passes) — write that check first so "done" is observable.

---

### T1 — VS Code extension scaffold + language contribution (all 15 file types) [code]

**Behavior:** A minimal but real VS Code extension exists under `editors/vscode/` that contributes
a `natural` language associated with **all 15** Natural extensions, so opening any `.NSx` file
makes VS Code recognize the language (the precondition for activation and the `onLanguage`
trigger). No server launch yet (that is T2).

**Deliverables:**
- `editors/vscode/package.json` — extension manifest: `name`, `publisher`, `version`,
  `engines.vscode`, `main` (`./out/extension.js`), `activationEvents` (`onLanguage:natural`),
  `contributes.languages` declaring language id `natural`, `aliases` `["Natural"]`, and
  `extensions` listing **all 15**: `.nsp .nsn .nss .nsc .nsm .nsl .nsg .nsa .nsh .nsd .ns4 .ns7
  .ns3 .ns8 .nst` (case-insensitive — VS Code matches lowercased; document that mainframe exports
  are typically upper-case and confirm VS Code's extension match is case-insensitive, else add
  `filenamePatterns`).
- `editors/vscode/tsconfig.json`, `editors/vscode/.vscodeignore`, `editors/vscode/.eslintrc*`
  (or flat config) — build/typecheck/lint scaffolding.
- `editors/vscode/src/extension.ts` — a stub `activate`/`deactivate` (real launch lands in T2).

**RED signal:** a unit test (`editors/vscode/src/test/language.test.ts`, run under
`@vscode/test-electron`) that opens a fixture file for each of the 15 extensions and asserts
`vscode.window.activeTextEditor.document.languageId === 'natural'`. Fails before the language
contribution exists. (T4 wires the harness; if authored before T4, the test is written here and
first *executed* in T4 — note the dependency.)

**GREEN:** the `package.json` `contributes.languages` block covering all 15 extensions.

**Refactor considerations:** keep the extension-list as a single source; consider generating the
README's file-type lists from it later (out of scope now). Ensure the manifest `version` tracks
the server release line (document the versioning choice — see Open questions).

**Reuse/migrate:** replaces the `editors/vscode/README.md` stub content (updated in T2). No Go
code touched.

**DoD:**
- [ ] `package.json` declares language `natural` with all 15 extensions (verified against the
      canonical list `.NSP .NSN .NSS .NSC .NSM .NSL .NSG .NSA .NSH .NSD .NS4 .NS7 .NS3 .NS8 .NST`).
- [ ] `npm install && npm run compile` (tsc) succeeds with no type errors.
- [ ] Lint passes.
- [ ] The 15-extension language-association test is written (executed once the harness lands in T4).
- [ ] No Go code changed; Analyzer seam untouched.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** none (harness execution depends on T4).

---

### T2 — Server launch + `naturalLsp.serverPath` resolution (zero-config on PATH) [code]

**Behavior:** On activation (opening a Natural file), the extension starts the `natural-lsp`
language server over stdio using `vscode-languageclient`. It launches `natural-lsp --stdio` from
`PATH` with **no configuration** when the binary is discoverable; the `naturalLsp.serverPath`
setting overrides the binary location. This is the core of FR-44 / Story 1 and NFR-13 (zero-config).

**Deliverables:**
- Add `vscode-languageclient` dependency; `src/extension.ts` builds a `LanguageClient` with
  `ServerOptions` = `{ command: resolveServerPath(), args: ['--stdio'], transport: stdio }` and
  `DocumentSelector` `{ language: 'natural' }`.
- `contributes.configuration` in `package.json`: a `naturalLsp.serverPath` string setting
  (default `"natural-lsp"`, description: "Path to the natural-lsp server binary; defaults to
  looking it up on PATH").
- A pure `resolveServerPath(config)` function (own module, unit-testable without the VS Code host)
  returning the configured path when set, else `"natural-lsp"` (relying on PATH lookup).
- Wire client start in `activate`, `client.stop()` in `deactivate`.

**RED signal — two thin, separable tests (split if they grow):**
1. Unit test on `resolveServerPath`: returns `"natural-lsp"` when the setting is empty/unset;
   returns the configured absolute path when `naturalLsp.serverPath` is set. (Pure — no VS Code
   host needed; plain Mocha.)
2. Integration test (`@vscode/test-electron`): opening a `.NSP` fixture in a sample workspace
   (containing a `.natural-lsp.toml` sentinel) starts the client and the client reaches the
   `running`/`ready` state (assert on `LanguageClient.state` or a successful `initialize`
   round-trip). Fails before the launch wiring exists.

**GREEN:** minimal `LanguageClient` wiring + `resolveServerPath`.

**Refactor considerations:** surface a clear error/notification when the binary is not found on
PATH and no `serverPath` is set (graceful degradation, mirrors server-side FR-43 philosophy on the
client side); keep it a *notification*, not a crash. Consider a "restart server" command as a nicety
(defer unless trivial). Do NOT re-open transport choice — stdio is the only supported transport
(matches every other editor config).

**Reuse/migrate:** the server's existing `--stdio` entry point (`cmd/natural-lsp/main.go`) — no
server change. Update `editors/vscode/README.md` from "stub" to real install/usage + the
`serverPath` setting.

**DoD:**
- [ ] Opening a Natural file activates the extension and launches `natural-lsp --stdio`.
- [ ] Zero-config works when `natural-lsp` is on PATH (default `serverPath`).
- [ ] `naturalLsp.serverPath` override is honored (unit test) and documented.
- [ ] `resolveServerPath` unit test + activation/launch integration test pass under the harness.
- [ ] Missing-binary case surfaces an actionable notification, not a crash.
- [ ] `editors/vscode/README.md` updated to as-built (no longer a stub).

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1 (language contribution); T4 (harness) to execute the integration test.

---

### T3 — TextMate syntax-highlighting grammar [code]

**Behavior:** The extension ships a basic TextMate grammar for `natural` (design decision #2):
keywords, comments (`*`/`**` full-line, `/*` rest-of-line), and string & numeric literals are
tokenized/highlighted. This is a self-contained contribution independent of the server.

**Deliverables:**
- `editors/vscode/syntaxes/natural.tmLanguage.json` — a TextMate grammar with `scopeName`
  `source.natural`, patterns for: line comments `*`/`**` (line-start only, matching the lexer's
  rule — see `internal/analysis/natural/lexer.go` for the exact comment semantics), `/*`
  rest-of-line comments, single-quoted string literals, numeric literals, and a keyword set
  (drawn from the parser's recognized statements: `CALLNAT`, `PERFORM`, `INCLUDE`, `FETCH`, `RUN`,
  `READ`, `FIND`, `GET`, `STORE`, `UPDATE`, `DELETE`, `DEFINE DATA`/`SUBROUTINE`/`MAP`/`WORK FILE`,
  `SELECT`, `INSERT`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`, etc.). Case-insensitive matching
  (Natural is case-insensitive).
- Wire `contributes.grammars` in `package.json` (language `natural` → the tmLanguage file).

**RED signal:** a `vscode-tmgrammar-test`-style grammar snapshot/scope test (or a
`@vscode/test-electron` test using `vscode.commands` token inspection) over a small sample `.NSP`
asserting expected scopes on a comment line, a string, a number, and a keyword. Fails before the
grammar exists. (If a dedicated tmgrammar test runner is undesirable, an acceptable RED is a
scope-assertion unit test loading the grammar JSON via `vscode-textmate`.)

**GREEN:** the minimal grammar patterns to make the scope assertions pass.

**Refactor considerations:** keep the grammar deliberately *basic* (out-of-scope: full semantic
highlighting — the plan flags richer grammars as TBD). Keep comment rules exactly consistent with
the lexer so highlighting doesn't contradict the server's tokenization. Do not chase completeness;
cover the four required categories (keywords, comments, strings, numbers) and stop.

**Reuse/migrate:** align comment/literal rules with `internal/analysis/natural/lexer.go`
(reference, not import). No Go change.

**DoD:**
- [ ] Grammar highlights keywords, `*`/`**` and `/*` comments, string literals, numeric literals.
- [ ] `contributes.grammars` wired; `.NSx` files show highlighting.
- [ ] Grammar scope test passes.
- [ ] Comment rules match the lexer's semantics (line-start `*`/`**`, rest-of-line `/*`).

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1.

---

### T4 — VS Code test harness (`@vscode/test-electron`) + tsc typecheck + lint gate [code]

**Behavior:** A headless test harness runs the T1–T3 tests (activation, `serverPath` resolution,
file-association, grammar scopes) in a real VS Code instance via `@vscode/test-electron`, plus a
tsc typecheck and lint. This is the local, runnable gate that makes the extension's behavior
regression-safe (design decision #3).

**Deliverables:**
- `@vscode/test-electron` + `mocha` (+ `@vscode/test-cli` if used) dev-dependencies.
- `editors/vscode/src/test/runTest.ts` (or `.vscode-test.mjs`) launcher, `src/test/suite/`
  index wiring, and the sample test workspace under `editors/vscode/src/test/fixtures/`
  (a `.natural-lsp.toml` sentinel + one small `.NSP` and one of each other extension needed for
  T1's association test — sanitized, non-proprietary Natural).
- npm scripts: `compile` (tsc), `lint`, `test` (build + `@vscode/test-electron`), and a `pretest`
  that compiles. A local `natural-lsp` binary is built and placed on PATH (or `serverPath` pointed
  at it) so the launch test in T2 has a real server to talk to — document how the harness locates it.

**RED signal:** running `npm test` fails (harness not present / tests can't run) before this task;
after this task, `npm test` executes the T1–T3 tests and they pass. The harness itself is verified
by "the suite runs green in headless VS Code."

**GREEN:** minimal harness + scripts so `npm run compile && npm run lint && npm test` all pass.

**Refactor considerations:** make the server-binary discovery in the launch test robust in CI
(build the Go binary in the CI job, export its dir onto PATH, or set `naturalLsp.serverPath`).
Keep the fixtures minimal. Ensure `@vscode/test-electron` downloads a pinned VS Code version for
reproducibility.

**Reuse/migrate:** builds the existing server via `go build ./cmd/natural-lsp` for the launch
test's dependency. No Go change.

**DoD:**
- [ ] `npm run compile` (tsc, no `any`-escape hatch abuse), `npm run lint`, and `npm test` all pass
      locally in headless VS Code.
- [ ] The T1 (15-extension association), T2 (`serverPath` + launch), and T3 (grammar scopes) tests
      execute and pass under the harness.
- [ ] A sample test workspace with a `.natural-lsp.toml` sentinel + minimal `.NSx` fixtures exists.
- [ ] The harness builds/locates the real `natural-lsp` binary for the launch test.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T2, T3 (their tests run here).

---

### T5 — CI job for the VS Code extension (Node/npm) [code]

**Behavior:** CI runs the extension's typecheck + lint + `@vscode/test-electron` suite on every PR,
in a **separate Node/npm job** alongside the existing Go `build-test` job. The Go-only `just verify`
gate stays unchanged (fast, Go-only). This satisfies design decision #3's "wired into CI."

**Deliverables:**
- A new job in `.github/workflows/ci.yml` (e.g. `vscode-extension`): checkout →
  `actions/setup-node` (pinned) → `actions/setup-go` + `go build -o natural-lsp ./cmd/natural-lsp`
  (so the launch test has a server) → put the binary on PATH → `npm ci` in `editors/vscode` →
  `npm run compile` → `npm run lint` → `npm test` under `xvfb-run` (headless VS Code needs a
  display on Linux).
- Update the CI `paths-ignore` reasoning: today `main` pushes ignore `**.md`/`docs/**`; ensure the
  extension job runs when `editors/vscode/**` changes (PR runs are unfiltered, so this is mainly
  about the `push: main` filter — confirm the extension job triggers appropriately).

**RED signal:** the workflow, when added, runs the extension suite green in CI (verified on the
feature PR's CI run). Before the task, no Node job exists; after, the PR shows a passing
`vscode-extension` check.

**GREEN:** the minimal job that installs Node, builds the server, and runs `compile`/`lint`/`test`.

**Refactor considerations:** pin action SHAs (matches the repo's existing convention — see
`ci.yml`/`release.yml` using pinned SHAs). Cache `npm` on `package-lock.json`. Keep least-privilege
`permissions: contents: read`. Use `xvfb-run` for headless Electron. Do NOT add Node to
`just verify` — keep that gate Go-only; the extension gate is CI-only (and locally via `npm test`).

**Reuse/migrate:** extends `.github/workflows/ci.yml`. No Go change. Note `editors/vscode/` needs a
committed `package-lock.json` for `npm ci`.

**DoD:**
- [ ] `.github/workflows/ci.yml` has a `vscode-extension` job that builds the server, then runs
      tsc + lint + `@vscode/test-electron` (via `xvfb-run`) on PRs.
- [ ] The job passes on the feature PR.
- [ ] `just verify` remains Go-only and unchanged.
- [ ] Action SHAs pinned; npm cache configured; `permissions: contents: read`.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T4 (the suite it runs).

---

### T6 — JetBrains reproducible first-party config (LSP4IJ, incl. Community) [doc + config]

**Behavior:** A documented, **reproducible** first-party path runs `natural-lsp` in JetBrains IDEs
including Community editions, with the Natural file types associated. Turn the root README's
LSP4IJ walkthrough into a verified, version-controlled artifact under `editors/jetbrains/`.

**Deliverables:**
- `editors/jetbrains/README.md` — replace the stub with step-by-step LSP4IJ setup: install LSP4IJ
  from the Marketplace; New Language Server → Command `natural-lsp --stdio`; associate all 15 file
  types (`.NSP …/.NST`); note it works in Community editions (LSP4IJ, not the paid native LSP API).
- If LSP4IJ supports importable server templates/config export, include the exported config file
  (e.g. a template JSON) under `editors/jetbrains/` so setup is reproducible by import, not just by
  hand. If not supported, document exact click-path steps and the exact file-type mask string
  covering all 15 extensions.

**RED signal (verifiable check, not a Go test):** following the documented steps against the
sample workspace (from T4's fixtures or `docs`' sample) yields a running server — checked by:
opening a `.NSP`, confirming LSP4IJ shows the server "started"/"connected", and that
`textDocument/definition` navigates. The check is written as a manual verification checklist in the
README's "Verify" subsection; "done" = the checklist was executed and passed on at least one
JetBrains Community IDE. (Automated GUI testing of JetBrains is out of scope.)

**GREEN:** the reproducible README + any exportable config artifact.

**Refactor considerations:** keep the root README §"JetBrains" and `editors/jetbrains/README.md`
consistent (root points to the detailed doc). Confirm the exact file-type association mechanism in
current LSP4IJ (mappings by file-name pattern vs language) and pin the doc to that.

**Reuse/migrate:** extends the existing root README LSP4IJ section (verify + correct drift). No Go
change.

**DoD:**
- [ ] `editors/jetbrains/README.md` is no longer a stub; steps are reproducible and cover Community
      editions.
- [ ] All 15 file types are associated per the documented steps.
- [ ] The "Verify" checklist (server connects, definition navigates) was executed and passed on a
      JetBrains Community IDE.
- [ ] Root README §JetBrains reconciled with `editors/jetbrains/README.md` (no drift).

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor` (RED/GREEN = the verification checklist,
doc-only).
**Depends on:** none (independent of the VS Code build).

---

### T7 — Verify & correct Neovim / Zed / Helix docs against a sample workspace [doc]

**Behavior:** The existing root README config blocks for Neovim, Zed, and Helix are **verified to
actually work** against a sample workspace, and each documents file-type association + sentinel
(`.natural-lsp.toml`) workspace-root detection. Following the docs yields working navigation
(FR-46 / Story 3).

**Deliverables:**
- A small sample workspace (reuse T4's fixtures or add `docs/plans/features/15-editor-clients/
  sample-workspace/` with a `.natural-lsp.toml` + 2–3 `.NSx` files exercising a `CALLNAT`/`PERFORM`
  so definition navigation is demonstrable).
- Verified/corrected README blocks for Neovim (nvim-lspconfig), Zed, Helix. Confirm: (a) the
  filetype/extension association covers the 15 types (Helix already lists all 15; verify Neovim's
  `filetypes = { 'natural' }` is backed by an `au BufRead/BufNewFile *.NS* set filetype=natural`
  autocmd — **the README's Neovim block currently omits the filetype autocmd; add it** so `.NSx`
  files actually get the `natural` filetype), (b) root detection uses the `.natural-lsp.toml`
  sentinel, (c) the server launches as `natural-lsp --stdio`.

**RED signal (verifiable check):** for each of Neovim, Zed, Helix, applying the documented config to
the sample workspace and opening a `.NSP` yields (i) the correct filetype/language recognized and
(ii) `go to definition` on a `CALLNAT`/`PERFORM` target navigating to the definition. Written as a
per-editor "Verify" checklist; "done" = executed and passed for all three. The stdio smoke
(`natural-lsp --stdio` responds to `initialize`) is the automatable lower bound if a given editor
can't be scripted.

**GREEN:** corrected README blocks + sample workspace.

**Refactor considerations:** note that Neovim's config API is in flux (`nvim-lspconfig` vs the
built-in `vim.lsp.config` in newer Neovim) — document the currently-recommended form and mention the
alternative. Keep all three blocks consistent on the `--stdio` arg and the sentinel root pattern.

**Reuse/migrate:** extends README §"Editor setup" (Neovim/Zed/Helix). No Go change. The
`internal/config` sentinel walk-up (`.natural-lsp.toml`) is the root-detection contract each editor
must mirror — reference it, don't reimplement.

**DoD:**
- [ ] Neovim block includes filetype association for the 15 `.NSx` types (autocmd added) + sentinel
      root detection; verified navigation on the sample workspace.
- [ ] Zed and Helix blocks verified for association + root detection + navigation.
- [ ] Each block launches `natural-lsp --stdio`; stdio smoke passes.
- [ ] Sample workspace committed for reproducibility.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor` (RED/GREEN = the per-editor verification
checklists, doc-only).
**Depends on:** none.

---

### T8 — Distribution verification + install-path docs reconciliation [doc]

**Behavior:** Confirm the already-built distribution meets Story 4 and that the install docs match
as-built: native binaries for the major platforms/arches are published, multiple install paths are
documented, and a **freshly installed binary reports `--version` and passes the stdio smoke check**
(NFR-10, NFR-12, NFR-13).

**Deliverables:**
- A verification checklist / small script (`editors/` or `docs/plans/features/15-editor-clients/`)
  that, given a release binary, runs `natural-lsp --version` and the stdio smoke
  (`natural-lsp --stdio < /dev/null` → clean initialize/EOF exit — the README already documents this
  smoke command).
- Reconcile root README §Installation with `justfile release`'s actual 5 outputs
  (`natural-lsp-{linux,darwin}-{amd64,arm64}`, `natural-lsp-windows-amd64.exe`) and `checksums.txt`
  — confirm the filenames listed match, and that the "package-manager-style install" path (NFR-12)
  is either documented (e.g. `go install`, which is already present and counts) or explicitly noted
  as future work.

**RED signal (verifiable check):** the smoke script fails/absent before the task; after, running it
against each platform's binary (or at least the host platform's) passes `--version` and the stdio
smoke. This is the automatable core of Story 4's "fresh install reports version + passes smoke."

**GREEN:** the smoke/verify script + reconciled README.

**Refactor considerations:** do NOT rebuild the release pipeline — `justfile release` +
`release.yml` already produce and publish the artifacts and checksums. This task is verification +
doc reconciliation only. Consider adding the smoke check to `release.yml` as a post-build guard
(optional, note it) so a broken binary can't be published.

**Reuse/migrate:** verifies existing `justfile release` / `.github/workflows/release.yml`; reconciles
existing README §Installation. No Go change (unless the optional release-smoke guard is added).

**DoD:**
- [ ] The 5 published artifact names + `checksums.txt` are confirmed and match README §Installation.
- [ ] A smoke check runs `--version` and the stdio smoke against a fresh binary and passes.
- [ ] Install-path docs (pre-built, source, `go install`) reconciled with as-built; NFR-12's
      "package-manager-style install" documented or flagged as future.
- [ ] (Optional, noted) release-smoke guard considered for `release.yml`.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor` (RED/GREEN = the smoke/verify check, doc-only).
**Depends on:** none.

---

### T9 — Package the VS Code extension as a `.vsix` (no Marketplace publish) [code]

**Behavior:** The extension can be packaged into an installable `.vsix` via `vsce` (approved
decision #5). No Marketplace publish step, no `VSCE_PAT` secret, no publisher-account dependency —
packaging only, so the `.vsix` can be installed via `code --install-extension` and optionally
attached to a GitHub Release. `publisher` is `dkrieg` and the extension `version` tracks the server
release line (approved decision #4).

**Deliverables:**
- `@vscode/vsce` dev-dependency and an npm `package`/`vsix` script (`vsce package`) in
  `editors/vscode/package.json`; `publisher: "dkrieg"`; a `.vscodeignore` that keeps the `.vsix`
  lean (excludes `src/`, tests, node dev artifacts).
- Ensure `contributes` (languages, grammars, configuration), `main`, `engines.vscode`, `repository`,
  `license`, and an `icon`/`README`-for-Marketplace-listing (README is fine) are present so
  `vsce package` succeeds with no fatal warnings.
- Reconcile root README §"Editor setup → VS Code": the `.vsix` filename produced
  (`natural-lsp-vscode-<version>.vsix`) matches the documented `code --install-extension …` command.

**RED signal (verifiable check):** `npm run package` (→ `vsce package`) fails/absent before the task;
after, it produces a `natural-lsp-vscode-<version>.vsix` with no fatal errors. Add a lightweight
check (script or CI step) asserting the `.vsix` is produced.

**GREEN:** minimal `vsce` wiring + manifest fields so `vsce package` succeeds.

**Refactor considerations:** do NOT add a publish step. Optionally note (not implement) how a future
task could attach the `.vsix` to `release.yml` and/or publish to the Marketplace. Keep the packaged
artifact out of git (`.gitignore` the `*.vsix`).

**Reuse/migrate:** builds on T1–T4's manifest/compile. No Go change.

**DoD:**
- [ ] `npm run package` produces `natural-lsp-vscode-<version>.vsix` with no fatal `vsce` errors.
- [ ] `publisher` is `dkrieg`; extension `version` tracks the server release line (documented).
- [ ] No Marketplace publish step / no `VSCE_PAT` dependency added.
- [ ] Root README §VS Code `code --install-extension` command matches the produced `.vsix` name.
- [ ] `*.vsix` is gitignored.

**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1–T4 (a compiling, contributing manifest).

---

## Reviews required

Run via `/review-feature`:
- **`review-docs`** (primary) — this feature is doc-heavy and changes install/editor-setup surface,
  the `editors/` READMEs, and CI. `CLAUDE.md` "Project state" + README §Installation/§Editor setup
  must sync at `/finalize-feature` (VS Code extension now real; JetBrains no longer a stub; CI has a
  Node job).
- **CI / build review** (via `review-orchestration`) — a new Node/npm CI job is added and must not
  regress the Go `just verify` gate; verify pinned action SHAs, least-privilege `permissions`, npm
  caching, `xvfb-run` for headless Electron.
- **Client robustness** — the VS Code extension's graceful handling of a missing/mis-pathed server
  binary (actionable notification, not a crash) — the client-side analogue of FR-43.
- **Protocol conformance (light)** — confirm the client launches the server exactly as every other
  editor does (`natural-lsp --stdio`, stdio transport, `natural` document selector); no bespoke
  protocol assumptions.
- **NOT needed:** `review-seam` (no shared-contract change), analyzer/extraction robustness (no
  parser/model change), concurrency (no indexer/watcher change).

## Open questions

> **Resolved at the checkpoint** (see "Approved scope decisions" above): #1 publisher/version
> (`dkrieg`, tracks server line), #2 packaging (`.vsix` via `vsce`, no Marketplace publish — T9),
> #4 NFR-12 (`go install` + binary suffice, tap is future work). #3 (JetBrains reproducibility
> ceiling) and #5 (VS Code file-association case sensitivity) are implementation-time confirmations
> handled in T6 and T1 respectively.

1. **Extension versioning & publisher.** What `publisher` id and version scheme does the VS Code
   extension use — does it track the server release line (e.g. same `vX.Y.Z`), or version
   independently? Affects `package.json` and Marketplace identity. (Plan open question:
   marketplace/distribution channels.)
2. **Marketplace publishing.** Is publishing to the VS Code Marketplace (and the `.vsix`
   artifact in GitHub Releases) in scope for feature 15, or deferred? The plan lists
   "marketplace/distribution channels" as an open question. Current tasks build + test + document
   the extension but do **not** assume a publish step; if publishing is wanted, add a task to
   package `.vsix` (via `vsce`) and attach it to the release (extend `release.yml`).
3. **JetBrains reproducibility ceiling.** Does the current LSP4IJ version support an importable
   server-config artifact (so setup is reproducible by import, not hand-steps)? If not, T6 is a
   documented click-path only — acceptable per the AC ("documented and reproducible") but confirm
   that satisfies "first-party path."
4. **NFR-12 "package-manager-style install."** `go install` is documented and arguably satisfies
   this. Is a true package-manager channel (Homebrew tap, Scoop, apt/rpm) expected within feature
   15, or is `go install` + pre-built binary sufficient? T8 documents/flags this rather than
   building a tap.
5. **Case sensitivity of VS Code file associations.** Mainframe exports are typically upper-case
   (`.NSP`); VS Code's `contributes.languages.extensions` matching is case-insensitive on the
   extension — confirm during T1, and fall back to `filenamePatterns` (e.g. `**/*.{NSP,nsp,…}`) if
   any platform mismatches.
