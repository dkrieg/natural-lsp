# Tasks: Bundle the server binary in platform-specific `.vsix` files (feature 37)

**Plan:** [`plan.md`](./plan.md) (authoritative; approach approved)
**PRD:** relates to FR-44/FR-45/FR-46, NFR-10/NFR-12/NFR-13 (editor clients & distribution, feature 15).
**Kind:** Editor-client (TypeScript) + release-workflow (CI) + docs. **No Go / `internal/model` /
analyzer / cache change; no LSP protocol/capability change; no `testdata/*.NSx` fixtures.**

Red→green→refactor is applied **at the TypeScript unit-test level** (the extension's Mocha suite under
`editors/vscode/src/test/unit/`, pure Node/`fs`, no `vscode` host API — mirrors features 15/25 and PR #60:
`serverPath.test.ts`, `template.test.ts`, `packaging.test.ts`). These run in the `vscode-extension` CI job
via `npm run test:unit` (mocha over `out/test/unit/**/*.test.js`). The release-workflow task (T4) has **no
unit test** — the workflow's own verification step is its guard (called out explicitly, github-actions-expert
review at `/review-feature`).

---

## Current-state findings & impact

Surveyed the real code before decomposing. Ground truth:

- **`editors/vscode/src/serverPath.ts`** — host-free (`vscode`-free) resolver.
  `DEFAULT_SERVER_PATH = "natural-lsp"`; `interface ConfigGetter { get(section): unknown }`;
  `resolveServerPath(config)` returns the trimmed non-empty `serverPath` setting else `DEFAULT_SERVER_PATH`
  (bare name on PATH). Today there are exactly **two** tiers (override → PATH). This is where the bundled
  tier is inserted, staying `vscode`-free via **injected** inputs (an fs-exists probe + the bundled path),
  exactly like `ConfigGetter`.
- **`editors/vscode/src/test/unit/serverPath.test.ts`** — 6 pure tests using a `stubConfig(value)` helper.
  Extend here for the new tier.
- **`editors/vscode/src/extension.ts`** — `buildClient()` calls
  `resolveServerPath(vscode.workspace.getConfiguration("naturalLsp"))`, builds `ServerOptions`
  (`command`, `args:["--stdio"]`, stdio transport), and `startClient()` catches a start failure and shows
  an actionable `showErrorMessage` (feature 15 missing-binary UX). `activate(context)` receives
  `context.extensionPath` (available, currently unused). **No chmod today.** This is where the bundled path
  is computed from `context.extensionPath`, chmod-on-unix is added, and the resolver is fed the injected
  bundled candidate.
- **`editors/vscode/.vscodeignore`** — does **NOT** exclude `node_modules` (PR #60; load-bearing comment).
  There is **no `bin/` line**, so `bin/` is already included by default — the guard must assert it stays
  that way (no exclusion is ever added).
- **`editors/vscode/.gitignore`** — `node_modules/`, `out/`, `.vscode-test/`, `.vscode-test-server/`,
  `*.vsix`. **`bin/` is not yet ignored** — must be added (the bundled binary is populated only at package
  time from `dist/`, never committed).
- **`editors/vscode/src/test/unit/packaging.test.ts`** (PR #60) — pure-fs guards reading `.vscodeignore` /
  `package.json`. Extend (or add a sibling `describe`) for the `bin/`-inclusion invariant.
- **`.github/workflows/release.yml`** — a **single** `.vsix` step (`npm ci` → `npm version <tag>` →
  `npm run compile` → `npm run package`) + a verify step (`unzip -l | grep vscode-languageclient/`) +
  `gh release create dist/natural-lsp-* dist/checksums.txt editors/vscode/*.vsix`. This becomes a
  **per-target loop over 5 targets**, each embedding its matching `dist/` binary.
- **`justfile` `release` recipe** — cross-builds into `dist/natural-lsp-<os>-<arch>[.exe]` for
  `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64` + `dist/checksums.txt`. Confirmed source
  of the 5 per-target binaries. **Unchanged by this feature.**
- **Integration seam** — `src/test/runTest.ts` + `src/test/suite/launch.test.ts` inject the server via the
  **`NATURAL_LSP_SERVER_PATH` env var → `naturalLsp.serverPath` setting**; the launch test skips if unset.
  Because that path sets the **override** tier, the electron integration test exercises the override, **not**
  the bundled tier (dev `bin/` is empty). The bundled tier is therefore verified by (a) `serverPath.test.ts`
  unit tests and (b) T4's release-workflow verification — **not** the electron suite. No integration-test
  change is required for this feature.

**Target ↔ `dist/` binary ↔ `vsce --target` matrix** (from the plan; confirmed against the justfile):

| `vsce --target` | `dist/` binary                  | bundled as (`bin/`)   |
|-----------------|---------------------------------|-----------------------|
| `linux-x64`     | `natural-lsp-linux-amd64`       | `bin/natural-lsp`     |
| `linux-arm64`   | `natural-lsp-linux-arm64`       | `bin/natural-lsp`     |
| `darwin-x64`    | `natural-lsp-darwin-amd64`      | `bin/natural-lsp`     |
| `darwin-arm64`  | `natural-lsp-darwin-arm64`      | `bin/natural-lsp`     |
| `win32-x64`     | `natural-lsp-windows-amd64.exe` | `bin/natural-lsp.exe` |

**Impact classification of each AC:** all ACs are **new** work except that AC1.3's missing-binary
notification and AC2.1/2.2's `vscode-languageclient` verify already exist and are **reused/extended**. No
shared-contract change crosses the Analyzer seam (this is entirely LSP-client + release side). The one
in-code contract touched is the exported `resolveServerPath` signature (T1) — its **only** consumer is
`extension.ts` (T2), migrated in the same feature; the extended signature is designed backward-compatible
(new bundled input **optional**), so no other call site breaks.

---

## Tasks

### T1 — `serverPath.ts`: bundled-binary tier (RED → GREEN → REFACTOR)
**Pins:** Story 1 AC1.1 (precedence override → bundled-if-exists → PATH), AC1.3 (missing bundled → PATH
fallback), Story 3 AC3.1 (resolver unit tests incl. Windows `.exe`). OQ-1 (bundled at `bin/`), OQ-3 (a
pure, unit-testable "which server was chosen" helper the extension can log).

**Behavior to implement (adjust names to fit the real code; keep the module `vscode`-free):**
- A pure `bundledServerPath(extensionPath: string, platform: NodeJS.Platform): string` returning
  `join(extensionPath, "bin", platform === "win32" ? "natural-lsp.exe" : "natural-lsp")` (Node `path` is
  host-free — allowed). This makes the Windows `.exe` suffix unit-testable without a Windows host.
- Extend `resolveServerPath` to accept an **optional injected** bundled candidate, e.g.
  `resolveServerPath(config: ConfigGetter, bundled?: { path: string; exists: (p: string) => boolean }): string`.
  Precedence — **exactly**:
  1. `serverPath` setting, trimmed & non-empty → return it (override wins even when a bundled binary exists).
  2. else if `bundled` provided **and** `bundled.exists(bundled.path)` → return `bundled.path`.
  3. else → `DEFAULT_SERVER_PATH` (bare `natural-lsp` on PATH — current behavior preserved).
  Calling `resolveServerPath(config)` with **no** bundled arg must behave **byte-identically** to today
  (backward-compatible for any caller not yet migrated).
- (OQ-3) Optionally a pure `describeServerChoice(...)`-style helper returning a stable label
  (`"bundled"` / `"serverPath setting"` / `"PATH"`) so T2 can emit a client-side info log without host
  coupling; unit-test the label mapping here.

**Expected results / edge cases (each an `it(...)` in `serverPath.test.ts`):**
- `serverPath` set → returns it, **even when** `bundled.exists` returns `true` (override beats bundled).
- `serverPath` unset + `bundled.exists` returns `true` → returns `bundled.path`.
- `serverPath` unset + `bundled.exists` returns `false` → returns `DEFAULT_SERVER_PATH` (PATH fallback).
- `serverPath` unset + **no** `bundled` arg → returns `DEFAULT_SERVER_PATH` (regression: today's behavior).
- `bundledServerPath("/ext", "win32")` ends in `bin/natural-lsp.exe`; `bundledServerPath("/ext", "linux")`
  (and `"darwin"`) ends in `bin/natural-lsp` (no `.exe`).
- (if added) `describeServerChoice` returns the correct label for each of the three tiers.

**TDD agents:** `tdd-red` (add the failing `it(...)`s above to `serverPath.test.ts`) → `tdd-green`
(implement in `serverPath.ts`) → `tdd-refactor`.

**Definition of done:**
- [ ] New `it(...)`s added to `editors/vscode/src/test/unit/serverPath.test.ts` covering all bullets above;
      they failed before the implementation and pass after.
- [ ] `serverPath.ts` implements `bundledServerPath` + the 3-tier `resolveServerPath`; module imports **no**
      `vscode` (fs-exists is injected).
- [ ] Existing 6 resolver tests still pass unchanged (no-bundled-arg path preserved).
- [ ] `npm run test:unit` + `npm run lint` pass in `editors/vscode`.

---

### T2 — `extension.ts`: activation wiring (bundled path, unix chmod, resolver feed, choice log)
**Pins:** Story 1 AC1.1 (activation uses the new tier), AC1.2 (unix `chmod 0o755`, failure-tolerant),
AC1.3 (reuse feature 15 missing-binary notification), OQ-3 (log which server was chosen).

**Behavior to implement:**
- In `buildClient()` (or a small helper it calls), compute the bundled candidate from
  `context.extensionPath` (thread `context` — or just `extensionPath` — into `buildClient`/`startClient`;
  today they take no args): `path = bundledServerPath(context.extensionPath, process.platform)`,
  `exists = (p) => fs.existsSync(p)` (this is the host-side wiring — `fs`/`process` live here in
  `extension.ts`, not in `serverPath.ts`). Pass `{ path, exists }` to `resolveServerPath`.
- **Unix chmod:** on `process.platform !== "win32"`, before launch, if the resolver chose the bundled path,
  `fs.chmodSync(bundledPath, 0o755)` wrapped in try/catch — a failure (read-only fs, etc.) is swallowed
  (log at most a warning) and activation **falls through** to launching whatever the resolver returned;
  never throws, never blocks activation (AC1.2). Only chmod the bundled path (don't chmod a
  user-configured `serverPath` or the bare PATH name).
- **Missing-binary UX (AC1.3):** unchanged reuse of `startClient()`'s existing catch →
  `showErrorMessage`. When neither an override, an existing bundled binary, nor a PATH `natural-lsp` works,
  `client.start()` rejects and the existing notification fires — no new code path needed beyond keeping it.
- **(OQ-3) Choice log:** emit one client-side **info** log (e.g. via the LanguageClient output channel or
  `console.info`) at activation naming the resolved server and the tier
  (`describeServerChoice` label from T1), mirroring feature 26's logging spirit on the client side.
  Fire-and-forget; never fails activation.

**Testability note (read the integration seam finding above):** the chmod / host-fs wiring runs only in the
extension host, so it is **not** unit-tested (no `vscode`/`fs`-host in the pure Mocha suite). Keep all pure
logic (path computation, tier selection, choice label) in `serverPath.ts` (T1) so it *is* unit-tested; the
`extension.ts` glue is verified by the existing electron integration test (still exercises the **override**
tier via `NATURAL_LSP_SERVER_PATH`, proving activation still reaches `Running`) + T4's packaged-artifact
verification + `/review-feature` manual/expert review. **Do not** change `runTest.ts`/`launch.test.ts` — the
override seam is intentional and the bundled tier is covered elsewhere.

**Expected results / edge cases:** bundled binary present & executable → launched with no notification;
bundled missing → silent fall-through to PATH; chmod throws → caught, still launches; nothing resolvable →
existing actionable notification (no crash).

**TDD agents:** No *new* pure unit test is the natural home for the host glue. Run `tdd-red` **only if** a
unit-testable helper is extracted here (prefer extracting into `serverPath.ts` under T1 instead) — otherwise
this is a wiring task: `tdd-green` (implement) → `tdd-refactor`, relying on T1's tests + the electron suite +
`npm run compile`/`lint` as the gate. Call this out in the task hand-off so no agent fabricates a fake-red.

**Definition of done:**
- [ ] `activate` threads `context.extensionPath` into client construction; `buildClient` feeds
      `{ path: bundledServerPath(...), exists: fs.existsSync }` to `resolveServerPath`.
- [ ] Unix-only `chmod 0o755` on the chosen **bundled** path, in try/catch, never throwing.
- [ ] Feature-15 missing-binary `showErrorMessage` preserved and still reachable.
- [ ] One info-level "chose server X (tier)" log at activation (OQ-3).
- [ ] `npm run compile` (tsc typecheck), `npm run lint`, `npm run test:unit`, and the electron
      integration suite (via `npm test` with `NATURAL_LSP_SERVER_PATH` set) all pass.

---

### T3 — `.vscodeignore` keeps `bin/` in the package; `.gitignore` excludes `bin/`; packaging guard (RED → GREEN → REFACTOR)
**Pins:** Story 2 AC2.1 (`.vscodeignore` must include `bin/`), Story 3 AC3.2 (packaging guard), OQ-1
(`bin/` gitignored, populated only at package time).

**Behavior to implement:**
- **`editors/vscode/.vscodeignore`:** confirm/keep `bin/` **not** excluded. Add a load-bearing comment
  (mirroring the `node_modules` note) explaining `bin/` carries the bundled server binary and must ship —
  do **not** add any `bin/`-excluding glob.
- **`editors/vscode/.gitignore`:** add `bin/` so the package-time-populated binary is never committed.
- **`packaging.test.ts`:** add an `it(...)` (or sibling `describe`) asserting `.vscodeignore` contains no
  line whose first path segment excludes `bin/` — i.e. no non-comment line matching `^!?bin(\/|$)`
  (reuse the file's existing read+filter approach, analogous to the `node_modules` guard).

**Expected results / edge cases:** the guard passes on the current/updated `.vscodeignore`; it fails if a
future edit adds `bin/` (or `bin`) as an exclusion. (Optionally also assert `.gitignore` lists `bin/` so the
two files stay consistent — a nice-to-have, not required by an AC.)

**TDD agents:** `tdd-red` (add the failing `bin/`-inclusion guard to `packaging.test.ts` — make it fail
first by temporarily reasoning about an excluding line, or assert the new comment/shape) → `tdd-green`
(adjust `.vscodeignore`/`.gitignore`) → `tdd-refactor`.

**Definition of done:**
- [ ] `packaging.test.ts` has a `bin/`-not-excluded guard that runs under `npm run test:unit`.
- [ ] `.vscodeignore` includes `bin/` in the package (no excluding glob) + explanatory comment.
- [ ] `.gitignore` (editors/vscode) lists `bin/`.
- [ ] `npm run test:unit` passes.

---

### T4 — `release.yml`: per-target packaging loop + extended verification + attach all 5 `.vsix`
**Pins:** Story 2 AC2.1 (per-target build: copy matching `dist/` binary → `editors/vscode/bin/natural-lsp[.exe]`
→ `vsce package --target <target>`), AC2.2 (verify each `.vsix` contains **both** the bundled binary **and**
`vscode-languageclient`), AC2.3 (attach all 5 `.vsix` to the Release alongside the standalone binaries +
`checksums.txt`).

**Behavior to implement (workflow only — no unit test; see the guard note below):**
- Replace the single "Package VS Code extension (.vsix)" step with a **loop over the 5 targets**. For each
  `target` with its `dist/` binary name (per the matrix above):
  - `rm -rf editors/vscode/bin && mkdir -p editors/vscode/bin`
  - copy `dist/<binary>` → `editors/vscode/bin/natural-lsp` (or `natural-lsp.exe` for `win32-x64`)
  - `npm version "${VERSION#v}" --no-git-tag-version --allow-same-version` (keep; run `npm ci` +
    `npm run compile` **once** before the loop — sources don't change per target)
  - `vsce package --target <target>` → yields a target-suffixed `.vsix`
    (e.g. `natural-lsp-vscode-<ver>-linux-x64.vsix` — `vsce` appends the target).
- **Extended verify step** (replaces the current single-`.vsix` grep): for **each** produced `.vsix`,
  `unzip -l` and assert it contains **both** `extension/node_modules/vscode-languageclient/` (PR #60 guard,
  kept) **and** the bundled binary `extension/bin/natural-lsp` (or `extension/bin/natural-lsp.exe` for the
  win32 target). Fail the release (`::error::` + `exit 1`) if either is missing from any target `.vsix`.
- **Release attach:** `gh release create` uploads `dist/natural-lsp-* dist/checksums.txt` **plus all 5**
  `editors/vscode/*.vsix` (glob already matches; ensure all 5 land in that dir). Standalone binaries stay
  (go install / JetBrains / other editors — plan "out of scope"/notes).

**Edge cases to handle in the script:** a missing `dist/<binary>` (justfile matrix drift) must fail loudly,
not silently ship a no-binary `.vsix`; `set -euo pipefail` retained; the win32 `.exe` naming handled in both
the copy and the verify.

**No unit test — the workflow's verify step IS the guard (explicit).** This mirrors how the repo has always
guarded packaging correctness in `release.yml` (PR #60). A **github-actions-expert review** is expected at
`/review-feature` (matrix/loop correctness, artifact naming, no accidental fat `.vsix`, all 5 attached).
Optionally lint the YAML with `actionlint` if available locally, but there is no Go/Mocha gate for this task.

**TDD agents:** Not a TDD-loop task (no automated test to red/green). Implement directly (a single
green-style edit), then `tdd-refactor` for script clarity. Flag in the hand-off that `tdd-red` is **N/A**
here so no agent invents a fake failing test.

**Definition of done:**
- [ ] `release.yml` builds one `.vsix` per target (5), each embedding the matching `dist/` binary at
      `extension/bin/natural-lsp[.exe]`.
- [ ] Verify step asserts **both** the bundled binary **and** `vscode-languageclient` in **every** target
      `.vsix`; fails the release if any is missing.
- [ ] `gh release create` attaches all 5 `.vsix` + the standalone binaries + `checksums.txt`.
- [ ] `.vsix` version still pinned to the tag; `npm ci`/`compile` run once; `set -euo pipefail` retained.
- [ ] github-actions-expert review is requested at `/review-feature`.

---

### T5 — Docs (README + CLAUDE.md) — DEFERRED to `/finalize-feature`
**Pins:** Story 3 AC3.3.

Per the repo workflow, doc sync happens in `/finalize-feature` (and `review-docs` flags drift). Surfaces to
update **at finalize**, listed here for traceability (do **not** write them during implementation):
- **Root `README.md`** — VS Code section: the extension now **bundles** the server; download the `.vsix`
  matching your OS/arch (the 5 targets); `naturalLsp.serverPath` remains the override for a custom/dev build;
  arches outside the 5 use the standalone binary + `PATH`/`serverPath`; JetBrains/other editors unchanged.
- **`editors/vscode/README.md`** (if present) — same bundling note + which `.vsix` to pick.
- **`CLAUDE.md`** — a feature-37 "Project state" note: platform-specific `.vsix` bundling, bundled-binary
  tier in `serverPath.ts`, per-target `release.yml`, **no Go/model/cache/protocol change**.

**Definition of done:** deferred; completed and verified during `/finalize-feature`.

---

## AC → task traceability

| AC | Task |
|----|------|
| Story 1 AC1.1 (bundled tier + precedence) | T1 (logic), T2 (activation uses it) |
| Story 1 AC1.2 (unix chmod, guarded)       | T2 |
| Story 1 AC1.3 (missing → PATH + notification) | T1 (fallback), T2 (reuse feature-15 notification) |
| Story 2 AC2.1 (per-target build + `.vscodeignore` includes `bin/`) | T4 (build), T3 (`.vscodeignore`) |
| Story 2 AC2.2 (verify bundled binary + `vscode-languageclient` in each `.vsix`) | T4 |
| Story 2 AC2.3 (attach all 5 `.vsix`)      | T4 |
| Story 3 AC3.1 (`serverPath.test.ts` new tier) | T1 |
| Story 3 AC3.2 (`packaging.test.ts` `bin/` guard) | T3 |
| Story 3 AC3.3 (README/CLAUDE.md)          | T5 (finalize) |

Dependency order: **T1 → T2** (T2 consumes T1's signature), **T3** (independent, can run any time),
**T4** (independent of T1/T2 at the YAML level, but logically last so it packages the finished extension),
**T5** at finalize. Suggested execution: T1, T2, T3 (unit-tested loop), then T4 (workflow), then T5.

## Reviews required (for `/review-feature`)
- **github-actions-expert** — T4 `release.yml`: the per-target matrix/loop, artifact naming
  (`--target` suffix), the extended two-assertion verify, all-5-attached, no accidental fat/universal
  `.vsix`, `dist/` binary-name mapping, `set -euo pipefail` robustness.
- **typescript / editor-client reviewer** — T1/T2: resolver precedence correctness, `vscode`-free
  boundary preserved, chmod failure-tolerance, backward-compatible signature, no regression to the
  feature-15 missing-binary UX.
- **review-docs** — T5 at finalize.

## Open questions
- **OQ-1 (bundled location `bin/`)** — plan recommends `<extensionPath>/bin/natural-lsp[.exe]`, gitignored,
  populated at package time. **Adopted** in T1/T3/T4. Confirm the `bin/` convention is acceptable (no
  objection found in the code).
- **OQ-2 (generic no-binary `.vsix`)** — plan recommends **no**. **Adopted:** T4 builds only the 5
  targeted `.vsix`; other arches use the standalone binary + `serverPath`/`PATH` (documented in T5).
  Revisit only if requested.
- **OQ-3 (log which server was chosen)** — plan recommends **yes** (client-side info log). **Adopted:**
  T1 provides a pure choice-label helper; T2 emits the info log at activation. Confirm the log channel
  (LanguageClient output channel vs `console.info`) at implementation.

### Genuinely-new open questions surfaced by the code survey
- **NOQ-1 — `vsce package --target` output filename.** `vsce` appends the target to the `.vsix` name; the
  5 files must be **distinctly named** so `gh release create editors/vscode/*.vsix` attaches all 5 (a shared
  name would overwrite). Confirm `vsce`'s default suffixing is sufficient, or set `--out` explicitly per
  target in T4 (recommend explicit `--out natural-lsp-vscode-<ver>-<target>.vsix` for determinism).
- **NOQ-2 — bundled tier is not exercised by the electron integration test.** By design the integration
  seam sets the **override** tier (`NATURAL_LSP_SERVER_PATH` → `serverPath`), so the bundled path has no
  automated end-to-end coverage; it's covered by T1 unit tests + T4's packaged-artifact verification +
  manual/expert review. Accept this, or (larger, likely out of scope) add an electron test that stages a
  fake `bin/natural-lsp` and asserts the bundled tier is chosen. **Recommend accept** (keeps scope at the
  approved editor-client + CI + docs boundary).
