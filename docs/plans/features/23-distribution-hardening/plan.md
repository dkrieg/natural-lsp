# Feature: Distribution Hardening

**Status:** Planned
**PRD requirements:** NFR-10, NFR-12, FR-42
**Priority / phase:** P1 remediation (2026-07-14 assessment, secondary findings)
**Depends on:** [15](../15-editor-clients/plan.md)

## Summary

Make the documented install paths actually work. The README instructs
`go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest`, but `go.mod` declares the
module as bare `natural-lsp`, so the documented install cannot succeed (CLAUDE.md has flagged
this mismatch since feature 00). `scripts/smoke.sh` invoked with no argument resolves the binary
incorrectly: `[ -x natural-lsp ]` tests the file in the *current directory* while execution goes
through PATH lookup, producing a misleading `--version exited non-zero` failure when the binary
sits in cwd but is not installed. This feature reconciles the module path, fixes the smoke
script, and (stretch) opens the first package-manager channel promised by NFR-12.

## User stories

### Story 1 — `go install` works as documented (NFR-12)
**As a** Go-equipped user, **I want** the README's `go install` command to produce a working
binary **so that** the from-source install path is real.

**Acceptance criteria:**
- [ ] The module path in `go.mod` and all internal import paths are reconciled with the
      published repository path (e.g. `github.com/dkrieg/natural-lsp`), or the README is
      corrected to the path that actually resolves — one of the two, decided and recorded.
- [ ] After the change, `go build ./...`, `just verify`, and the VS Code extension CI job all
      pass; `--version` still reports correctly (FR-42).
- [ ] The README install section is verified against a clean-machine simulation (module proxy
      fetch or `go install` from a local clone path documented as the interim).

### Story 2 — Smoke script binary resolution (NFR-10)
**As a** release verifier, **I want** `scripts/smoke.sh` to resolve its default binary the same
way it executes it **so that** the no-argument invocation cannot mis-report.

**Acceptance criteria:**
- [ ] With no argument, the script either resolves `./natural-lsp` explicitly when present in
      cwd or reports "not found on PATH" accurately — the `[ -x name ]`-vs-PATH-exec divergence
      is eliminated.
- [ ] Regression check: running from the repo root right after `just build` with the binary not
      on PATH passes all smoke checks.

### Story 3 — First package-manager channel (NFR-12, stretch)
**As a** user without a Go toolchain, **I want** a package-manager install (Homebrew tap first)
**so that** installation is one command on macOS/Linux.

**Acceptance criteria:**
- [ ] A Homebrew tap formula (or equivalent) installs the released binary and passes
      `scripts/smoke.sh`; the README installation section documents it.
- [ ] If deferred, the deferral is recorded here with the reason (release cadence, signing,
      etc.) rather than left implied.
