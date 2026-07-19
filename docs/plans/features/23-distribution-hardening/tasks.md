# Tasks: Distribution Hardening (feature 23)

## Decisions (user-approved 2026-07-19)

- **OQ-1 → Option A: RENAME the module to `github.com/dkrieg/natural-lsp`** (rewrite go.mod + all 182
  internal imports across 85 files). Clarified with the user: running `release.yml` produces binaries
  and pushes a tag, but does NOT make remote `go install <url>@latest` resolve — a bare `module
  natural-lsp` mismatches the install URL. The rename is REQUIRED for the documented `go install` to
  work; the release (user-run) supplies the tag. Do both: this feature renames + completes the README;
  the user cuts the initial tagged release after merge.
- **OQ-2 → DEFER the Homebrew tap** with a recorded reason (separate tap repo + stable released
  binaries + cadence commitment; premature pre-v1.0). This feature still fixes `go install` + smoke.sh.
- **OQ-3 → smoke-regression test as a `//go:build integration` Go test** in `cmd/natural-lsp/`, reusing
  the module-root-walk harness.
- **OQ-4 → README documents `go install ...@latest` works after the initial tagged release** (which the
  user will run); interim note = build from a local clone. Also add pre-built-binary download
  instructions pointing at the release assets.
- **RELEASE = human action.** The assistant will NOT run `release.yml` or push a tag. It prepares the
  branch release-ready and documents the exact release steps for the user to run post-merge.

---

Source plan: [`plan.md`](./plan.md)
PRD requirements: **NFR-10** (native binaries — already met), **NFR-12** (multiple install
paths — the gap), **FR-42** (version report — regression-guarded).
Depends on: [feature 15](../15-editor-clients/plan.md).

This is a **distribution/packaging** feature: no `internal/model`, no `Analyzer`-seam, and no
cache-format change. The largest change (T1) is a mechanical module-path rename that touches
almost every `.go` file's import block but changes no behavior. The Analyzer seam and
`internal/model` purity are preserved by construction — nothing about the type surface changes,
only the import prefix that names it.

---

## Current-state findings & impact

Surveyed: `go.mod`, `cmd/natural-lsp/main.go`, `justfile`, `.github/workflows/{ci,release}.yml`,
`scripts/smoke.sh`, `README.md`, `cmd/natural-lsp/stdio_integration_test.go`, and the full
`grep` of internal import paths.

### Module-path mismatch (Story 1 — the key decision)

- `go.mod` line 1 declares **`module natural-lsp`** (bare).
- **Every internal import uses the prefix `"natural-lsp/internal/...`** — verified: `182`
  import occurrences across `85` `.go` files. Breakdown by package:
  `model` 73, `config` 36, `analysis/natural` 31, `workspace` 27, `analysis` 8,
  `workspace/corpusgen` 3, `document` 3, `server` 1. **No `cmd` self-imports.**
- README §"go install" (lines 243–256) documents
  `go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` and *already carries a
  "Known issue" admonition* pointing at this feature — the documented intent is clearly the
  **`github.com/dkrieg/natural-lsp`** path. CLAUDE.md line 603 and
  `.claude/knowledge/go/go-version-and-tooling.md:24` both flag the same reconcile-before-publish
  requirement.

**Two options (open question OQ-1 — needs a user decision before T1):**

- **Option A — rename the module to `github.com/dkrieg/natural-lsp`** (recommended, matches
  documented intent). Blast radius: `go.mod` module line + all `182` import statements across
  `85` files rewritten `natural-lsp/internal/... → github.com/dkrieg/natural-lsp/internal/...`.
  After this, remote `go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` resolves.
  This is a large but purely-mechanical, tool-assistable change (`go mod edit -module` +
  `find … -exec sed` / `gofmt -r`-style rewrite) with a compiler-verified end state.
- **Option B — correct the README to the bare module** (docs-only, small). But
  `go install <remote>@latest` **cannot** work with a bare module path — the README would have
  to *remove* the remote-install claim entirely and document only build-from-clone. This leaves
  NFR-12's "package-manager-style install" gap unclosed for Go users. **Not recommended** given
  the PRD/README intent, but it is the cheap path if the repo will never publish under the
  github module path.

**Version injection is unaffected by the rename** (verified): `justfile:80` uses
`ldflags="-s -w -X main.version=${ver}"` — the ldflags target is **`main.version`**
(package-relative to `cmd/natural-lsp`, `main.go:21 var version`), *not* module-path-qualified.
A module rename does not change the `main` package's import path as an ldflags `-X` target, so
`--version` / FR-42 is unaffected by construction; T2 guards it anyway.

**Release / CI workflows are unaffected by the rename** (verified): `release.yml` builds via
`just release` and packages `dist/natural-lsp-*` artifacts by filename; `ci.yml` builds
`./cmd/natural-lsp` by directory path and passes `NATURAL_LSP_SERVER_PATH`. Neither embeds the
module path as a string. No workflow edit is required for the rename (T4 adds a *new* install
verification, separately).

### smoke.sh no-arg bug (Story 2)

`scripts/smoke.sh` (default `BIN="${1:-natural-lsp}"`, line 26). The resolver at lines 31–38:

```sh
if [ -x "$BIN" ]; then          # line 32
  :
elif command -v "$BIN" >/dev/null 2>&1; then
  BIN="$(command -v "$BIN")"
else
  fail "binary not found or not executable: $BIN"
fi
```

**Confirmed bug:** with no argument `BIN` is the bare name `natural-lsp`. `[ -x natural-lsp ]`
tests a **relative path in the current directory** — so when the just-built binary sits in cwd
and is executable, the `if` branch is taken and `BIN` is **left as the bare name** `natural-lsp`.
Later, `"$BIN" --version` (line 46) runs a **command with no slash**, which the shell resolves
via **PATH**, *not* cwd. If `natural-lsp` is not on PATH (the exact just-built-in-repo case), the
exec fails and the script reports the misleading `--version exited non-zero`. The `[ -x name ]`
test (cwd semantics) and the exec (PATH semantics) disagree for a bare relative name.

**Fix:** when `$BIN` contains no `/` and a matching executable exists in cwd, normalize it to an
explicit `./`-prefixed (or absolute) path *before* exec so the test and the exec agree; when the
bare name is only found on PATH, keep the `command -v` resolution; when neither, fail with an
accurate "not found on PATH" message. (The `command -v` elif branch already normalizes correctly
for genuinely-on-PATH names — the bug is purely the first branch leaving a bare name unresolved.)

### Homebrew tap (Story 3 — stretch)

README lines 254–256 already document a package-manager channel (Homebrew/Scoop) as **future
work**. The plan (Story 3) explicitly permits deferral *if the deferral is recorded with a
reason*. There is no tap repo, no formula, and `just release` produces artifacts but does not
publish a formula. **Recommendation (OQ-2): defer** and formalize the deferral in the README with
a reason (a Homebrew tap needs a separate `homebrew-tap` repo, stable released binaries with
checksums to point a formula at, and release-cadence commitment — premature pre-v1.0). T5 records
the deferral rather than building the tap, unless the user elects to build it (see OQ-2).

### Criteria already satisfied (skipped, recorded)

- **NFR-10 (native binaries)** — met by `just release` cross-build (5 targets) + `release.yml`;
  no task.
- **FR-42 (version report)** — `main.go` `--version` works today; T2 only *guards* it survives
  the rename, it does not implement it.

---

## Tasks

Ordering: the module rename (T1) is a foundation that everything else builds/verifies on top of,
so it lands first; its verification (T2) is paired immediately after. The smoke fix (T3) and its
regression test are independent of the rename and could run in parallel, but are sequenced after
so the smoke regression (T4) exercises the renamed build. Docs (T6) land last, after the behavior
they describe is real.

> **T1 is gated on OQ-1.** Do not start T1 until the user confirms Option A vs Option B. The task
> below is written for **Option A** (recommended). If Option B is chosen, T1/T2 collapse into a
> docs-only change folded into T6 and this task list shortens accordingly.

### T1 — Rename the Go module to `github.com/dkrieg/natural-lsp` (Story 1, NFR-12) — Option A

**Behavior:** the module and all internal imports use the published repository path, so
`go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` can resolve.

**Change:**
- `go.mod` line 1: `module natural-lsp` → `module github.com/dkrieg/natural-lsp` (via
  `go mod edit -module github.com/dkrieg/natural-lsp`).
- Rewrite all `182` import statements: prefix `"natural-lsp/internal/...` →
  `"github.com/dkrieg/natural-lsp/internal/...` across the `85` files (mechanical, tool-assisted;
  `go mod edit` does not rewrite imports, so a repo-wide find/replace + `gofmt -w` is required).
- No `main.version` / ldflags change (verified package-relative).
- No workflow, justfile, or cache change.

**Fixtures:** none — this is a repo-wide refactor, not a behavior fixture. Verification is the
compiler + the existing full suite.

**Expected result:** `go build ./...` succeeds; `grep -rho '"natural-lsp/' --include='*.go'`
returns **zero** matches (all rewritten); `go.mod` module line is the github path.

**Reuses/migrates:** migrates every consumer of every internal package (they are all in-repo).
Because the change is prefix-only and the package surface is identical, no test logic changes —
the migration is proven green by the *unchanged* existing tests still passing.

**DoD:**
- [ ] `go.mod` module line is `github.com/dkrieg/natural-lsp`.
- [ ] No remaining `"natural-lsp/internal/...` import (grep clean).
- [ ] `gofmt`/`go vet` clean; `go build ./...` succeeds.
- [ ] `just verify` (fmt-check + lint-tests + vet + build + `test -race` + integration) passes —
      the whole existing suite green, unchanged, proves the migration.
- [ ] Analyzer seam + `internal/model` purity preserved (unchanged type surface; prefix-only).
- [ ] Cache-format version unchanged (no `internal/model` change).

**TDD agents:** This is a mechanical refactor with no new behavior. Run `tdd-refactor` only
(the "test" is the pre-existing full suite staying green before and after). `tdd-red`/`tdd-green`
do not apply — there is no new assertion to introduce first.

**Depends on:** OQ-1 resolved (Option A confirmed).

---

### T2 — Guard `--version` (FR-42) and build survive the rename

**Behavior:** after T1, `natural-lsp --version` still prints a version line mentioning
`natural-lsp`, and `-X main.version` injection still works.

**Change:** add/confirm a unit test in `cmd/natural-lsp/main_test.go` asserting the `--version`
output format (`natural-lsp <version>`), and confirm the release ldflags path
(`-X main.version=…`) still targets `main.version` post-rename (a build-with-ldflags assertion,
or a note that `main.version` is unqualified by module path so it is unaffected).

**Fixtures:** none (invokes the built binary / the `version` var directly).

**Expected result:** test asserts stdout matches `^natural-lsp ` and, when built with
`-ldflags "-X main.version=vX.Y.Z"`, reports that injected value. `main_test.go` already exercises
lifecycle; extend it minimally for the `--version` format if not already asserted.

**Reuses/migrates:** reuses the existing `cmd/natural-lsp/main_test.go` harness.

**DoD:**
- [ ] Test asserts `--version` output shape (FR-42) and passes post-rename.
- [ ] A note (in the test or a comment) records that `-X main.version` is package-relative and
      thus rename-safe.
- [ ] `go test ./cmd/natural-lsp` green.

**TDD agents:** `tdd-red` (add the `--version` format assertion — should pass immediately post-T1;
if it already exists, this becomes a verification note) → `tdd-green` (no code change expected) →
`tdd-refactor`.

**Depends on:** T1.

---

### T3 — Fix `scripts/smoke.sh` binary resolution (Story 2, NFR-10)

**Behavior:** the no-argument invocation resolves the default binary the same way it executes it —
either normalizing a cwd-local `./natural-lsp` to an explicit path before exec, or failing with an
accurate "not found on PATH" message. The `[ -x name ]`-vs-PATH-exec divergence is eliminated.

**Change:** in `scripts/smoke.sh` lines 31–38, fix the first resolver branch so a bare name that
exists as an executable in cwd is rewritten to `./natural-lsp` (or its absolute path) *before*
being exec'd; a bare name found only on PATH keeps the `command -v` normalization; a name with a
`/` is treated as a literal path as today. Preserve `set -euo pipefail` and the existing `pass`/
`fail` contract.

**Fixtures:** none in the shell (a Go-side fixture binary is created by T4).

**Expected result:** `scripts/smoke.sh` with no arg, run from a directory containing an executable
`./natural-lsp` **not on PATH**, resolves and runs it (does not mis-report `--version exited
non-zero`); with no arg and no cwd binary and none on PATH, exits non-zero with a message naming
"not found on PATH".

**Reuses/migrates:** reuses the existing `pass`/`fail`/`frame` helpers and both existing checks
(`--version`, `--stdio` EOF, lifecycle round-trip) unchanged.

**DoD:**
- [ ] No-arg + cwd-local-not-on-PATH binary → all checks pass (the plan's Story 2 regression).
- [ ] No-arg + genuinely absent → accurate "not found on PATH" failure, non-zero exit.
- [ ] Explicit-path and on-PATH invocations behave as before (no regression).
- [ ] `shellcheck` clean if available; `set -euo pipefail` retained.

**TDD agents:** `tdd-red` (the failing case is proven by T4's regression test written first) →
`tdd-green` (apply the resolver fix) → `tdd-refactor`.

**Depends on:** T1 (so the regression runs against the renamed build; behavior-independent of the
rename otherwise).

---

### T4 — Regression test for the smoke-script resolution bug (Story 2)

**Behavior:** a repeatable, in-repo test reproduces the no-arg cwd-binary-not-on-PATH failure
(RED before T3) and confirms the fix (GREEN after T3).

**Change:** add a Go test that shells out to `scripts/smoke.sh` — the natural home is a new
`//go:build integration` test in `cmd/natural-lsp/` (sibling to `stdio_integration_test.go`,
reusing its module-root-walk-up + `t.TempDir()` build pattern). The test:
1. Builds the binary into a temp dir named `natural-lsp`.
2. Runs `scripts/smoke.sh` with **no argument**, with the test's `cmd.Dir` set to that temp dir
   and a `PATH` **scrubbed of that temp dir** (so the binary is in cwd but not on PATH — the exact
   Story 2 case).
3. Asserts exit code 0 and stdout contains `all smoke checks passed`.
4. (Second case) Runs from an empty dir with a scrubbed PATH and asserts non-zero exit with a
   "not found on PATH" message.

Locate `scripts/smoke.sh` via the same module-root walk-up the existing integration test uses
(no absolute machine paths — respects `just lint-tests`).

**Fixtures:** the built binary in `t.TempDir()` (no committed `.NSx` fixture — this is a
distribution test, not an analyzer test).

**Expected result:** RED against current `smoke.sh` (misleading `--version exited non-zero`);
GREEN after T3.

**Reuses/migrates:** reuses `stdio_integration_test.go`'s build-and-locate-module-root helper
pattern (extract a shared helper if duplication warrants, per `tdd-refactor`).

**DoD:**
- [ ] Test is `//go:build integration` (runs under `just test-integration` / `just verify`).
- [ ] Package-relative script resolution (no absolute paths — `just lint-tests` clean).
- [ ] RED on pre-T3 script, GREEN on post-T3 script.
- [ ] Skips gracefully (`t.Skip`) if `bash` is unavailable (Windows CI runners) rather than
      failing — document the platform assumption.

**TDD agents:** `tdd-red` (write this test first, watch it fail against the current script) →
`tdd-green` (T3 makes it pass) → `tdd-refactor` (share the module-root helper).

**Depends on:** T1. Pairs with T3 (red-first).

---

### T5 — Record the Homebrew-tap deferral, or build the tap (Story 3, NFR-12, stretch)

**Behavior (recommended — defer, per OQ-2):** the README installation section records the
Homebrew-tap deferral **with a reason** (needs a separate `homebrew-tap` repo, stable published
binaries + checksums to reference, and a release-cadence commitment — premature pre-v1.0), so the
deferral is explicit rather than implied. This satisfies the plan's Story 3 "if deferred, record
the deferral" clause.

**Behavior (only if OQ-2 elects to build it):** add a Homebrew formula (in a `homebrew-tap` repo
or a `HomebrewFormula/` dir) that downloads a released `natural-lsp-<os>-<arch>` binary + verifies
against `checksums.txt`, installs it, and passes `scripts/smoke.sh`. This is a *new* task tree
(formula authoring + a `brew install`/`brew test` CI check) that would be split into its own
sub-tasks — flag for re-planning if elected.

**Fixtures:** none.

**Expected result (defer path):** README "go install"/"future work" prose names Homebrew/Scoop as
deferred with the reason and links this feature.

**DoD (defer path):**
- [ ] README records the deferral + reason (folds into T6).
- [ ] No dangling "coming soon" without a reason.

**TDD agents:** none for the defer path (docs-only — folds into T6). If built: full red→green→
refactor on the formula + CI check (re-plan).

**Depends on:** OQ-2 resolved.

---

### T6 — Reconcile install docs with as-built (Story 1 AC3, Story 3, docs sync)

**Behavior:** the README installation section reflects reality post-rename: the `go install`
"Known issue" admonition is **removed** (the command now works under Option A) and the section
documents the verified install; the Homebrew deferral (T5) is recorded; and a clean-machine
verification note is added (Story 1 AC3).

**Change:**
- README §"go install": drop the "Known issue" block (lines 245–248), keep the working
  `go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` command, and document how it
  was verified (module-proxy fetch, or `go install` from a local clone as the documented interim
  until the first tagged release is on the proxy — Story 1 AC3).
- CLAUDE.md line 603 ("reconcile the module path before publishing"): update to note the module is
  now `github.com/dkrieg/natural-lsp` and `go install` works.
- `.claude/knowledge/go/go-version-and-tooling.md:24`: update the reconcile note to reflect the
  rename landed.
- editors/vscode/README.md:74 already references the github module path — confirm it now matches.

**Fixtures:** none.

**Expected result:** README/CLAUDE.md/knowledge no longer describe the module mismatch as an open
issue; the documented `go install` matches the actual module path; the Homebrew deferral is
recorded with a reason.

**Reuses/migrates:** docs only.

**DoD:**
- [ ] README `go install` block works as documented (Option A) — Known-issue admonition removed.
- [ ] Story 1 AC3 clean-machine verification method documented.
- [ ] CLAUDE.md + knowledge note updated (no stale "reconcile before publishing").
- [ ] Homebrew deferral recorded with reason (from T5).

**TDD agents:** none (docs). Verified by `review-docs`.

**Depends on:** T1, T5.

---

## Reviews required (for `/review-feature`)

- **review-docs** — README/CLAUDE.md/knowledge install-path sync is a core deliverable (T6);
  capability/command surface unchanged but install instructions changed materially.
- **review-robustness** — `scripts/smoke.sh` resolver fix (T3): confirm the branch logic handles
  bare-name/relative/absolute/PATH cases and the accurate-failure path; `set -euo pipefail` held.
- **review-seam** — *confirm no seam change.* The rename (T1) is prefix-only; verify Analyzer +
  `internal/model` type surface is byte-identical modulo import path, and no cache-format bump.
- **build/release integrity** — confirm `just verify`, the VS Code extension CI job, and
  `release.yml` still pass post-rename (Story 1 AC2); confirm `-X main.version` still injects.

No concurrency/performance/protocol-conformance review needed — no indexer/watcher, hot-path, or
LSP-method change.

---

## Open questions (need a user decision before implementation)

- **OQ-1 (blocks T1 — the key decision): Module rename (Option A) vs README-correction
  (Option B)?** Recommendation: **Option A** — rename `go.mod` to
  `github.com/dkrieg/natural-lsp` and rewrite all 182 imports across 85 files. This is what the
  README/CLAUDE.md/PRD intent points at (remote `go install` should work) and is the only option
  that actually closes NFR-12's Go install path; the change is large but mechanical and
  compiler-verified, and does **not** touch version injection, workflows, or the cache. Option B
  is docs-only but cannot make `go install <remote>@latest` work (bare modules are not remotely
  installable) and would require *removing* the remote-install claim entirely. **Confirm Option A
  before starting T1.**
- **OQ-2 (blocks T5): Homebrew tap — build now or defer?** Recommendation: **defer** and record
  the reason (needs a separate tap repo, stable released binaries to reference, signing/cadence
  commitment — premature pre-v1.0). The plan explicitly allows deferral with a recorded reason. If
  the user wants it now, T5 expands into a formula-authoring + `brew install/test` CI sub-tree
  (re-plan).
- **OQ-3 (T4, minor): where should the smoke-script regression test live** — a
  `//go:build integration` Go test in `cmd/natural-lsp/` (recommended; reuses the existing
  build-and-locate harness, runs under `just verify`), or a standalone shell "smoke-of-the-smoke"?
  Recommendation: the Go integration test, so it runs in the existing gate and is skipped cleanly
  where `bash` is absent.
- **OQ-4 (T6/Story 1 AC3, minor): clean-machine verification method** — verify remote `go install`
  against the Go module proxy (requires a pushed tag the proxy can fetch), or document
  `go install` from a local clone path as the interim until the first tag is published?
  Recommendation: document the local-clone interim now and note proxy verification follows the
  first tagged release.
