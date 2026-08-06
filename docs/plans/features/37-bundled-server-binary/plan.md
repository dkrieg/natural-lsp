# Feature: Bundle the server binary in platform-specific `.vsix` files

**Status:** Planned
**PRD requirements:** relates to FR-44/FR-45/FR-46 + NFR-10/NFR-12/NFR-13 (editor clients & distribution, feature 15).
**Priority / phase:** P1 distribution improvement (removes the separate-server-deployment step users hit).
**Depends on:** [15](../15-editor-clients/plan.md) (VS Code extension + `serverPath` resolver), the `release.yml` `.vsix` packaging (PR #57) and the runtime-dependency fix (PR #60).

## Summary

Today the VS Code extension launches the `natural-lsp` server resolved from the `naturalLsp.serverPath`
setting → bare `natural-lsp` on `PATH` (`editors/vscode/src/serverPath.ts`). So a user must **separately
deploy** the server binary (put it on `PATH` or configure `serverPath`). This feature ships the server
**inside the extension** so opening a Natural file works with **zero separate deployment**.

**Approach (approved): platform-specific `.vsix`.** Use VS Code's native mechanism — `vsce package
--target <target>` — to produce one `.vsix` per platform, each embedding **only** that platform's
`natural-lsp` binary (~12–15 MB), rather than one fat multi-platform `.vsix`. The release workflow already
cross-builds the 5 binaries, so each target `.vsix` just embeds its matching one. The extension prefers the
bundled binary at activation; `serverPath` stays as an explicit override.

**Target ↔ Go build matrix** (the 5 the release already produces):

| `vsce --target` | GOOS/GOARCH   | release artifact                 |
|-----------------|---------------|----------------------------------|
| `linux-x64`     | linux/amd64   | `natural-lsp-linux-amd64`        |
| `linux-arm64`   | linux/arm64   | `natural-lsp-linux-arm64`        |
| `darwin-x64`    | darwin/amd64  | `natural-lsp-darwin-amd64`       |
| `darwin-arm64`  | darwin/arm64  | `natural-lsp-darwin-arm64`       |
| `win32-x64`     | windows/amd64 | `natural-lsp-windows-amd64.exe`  |

**No Go / `internal/model` / cache-format change.** Editor-client + release-workflow + docs only (mirrors
features 15/25). No LSP protocol change.

## User stories

### Story 1 — Zero-deploy activation from a bundled binary
**As a** developer, **I want** the extension to launch a server binary it ships with **so that** opening a
Natural file works without separately installing `natural-lsp`.

**Acceptance criteria:**
- [ ] `serverPath.ts` gains a **bundled-binary tier**. Precedence: (1) `naturalLsp.serverPath` (non-empty,
      trimmed) — explicit override; (2) the **bundled** binary shipped in the extension
      (`<extensionPath>/bin/natural-lsp` or `natural-lsp.exe` on Windows) **if it exists**; (3) bare
      `natural-lsp` on `PATH` (the current last-resort fallback). The resolver stays `vscode`-free and
      unit-tested (the fs-exists check + extension path are injected, like `ConfigGetter`).
- [ ] On activation on a unix host, the extension makes the bundled binary executable (`chmod 0o755`) before
      launch, because the exec bit is not reliably preserved through a `.vsix`. Guarded so a read-only/edge
      case never crashes activation (fall through to `PATH`).
- [ ] A missing bundled binary (e.g. a generic/un-targeted `.vsix`) degrades to the `PATH` fallback with an
      actionable notification if that also fails (reuse feature 15's missing-binary UX) — never a hard crash.

### Story 2 — Platform-specific `.vsix` built and verified in the release
**As a** maintainer, **I want** the release to produce a per-platform `.vsix` each embedding the matching
server **so that** users download one artifact and it just works.

**Acceptance criteria:**
- [ ] `release.yml` builds one `.vsix` **per target** (the 5 above): copy the matching pre-built binary from
      `dist/` into `editors/vscode/bin/natural-lsp[.exe]`, then `vsce package --target <target>` (version
      pinned to the tag as today). `.vscodeignore` must include `bin/` in the package (i.e. NOT ignore it).
- [ ] The existing `.vsix` verification step is extended to assert **each** target `.vsix` contains BOTH the
      bundled server binary (`extension/bin/natural-lsp*`) AND `vscode-languageclient` (the PR-#60 guard) —
      fail the release otherwise.
- [ ] All 5 platform `.vsix` are attached to the GitHub Release alongside the standalone binaries +
      `checksums.txt`. (The standalone binaries stay — they serve the `go install`/manual and non-VS-Code
      paths.)

### Story 3 — Tests & docs
**Acceptance criteria:**
- [ ] `serverPath.test.ts` covers the new tier: bundled path returned when it exists; `serverPath` overrides
      even when a bundled binary exists; `PATH` fallback when neither; Windows `.exe` suffix.
- [ ] `packaging.test.ts` (PR #60) extended (or a sibling) asserting `.vscodeignore` does **not** exclude
      `bin/`, so the bundled binary always ships.
- [ ] README/`CLAUDE.md` updated: the VS Code extension now bundles the server; download the `.vsix`
      matching your OS/arch; `serverPath` remains the override for a custom/dev build; non-bundled platforms
      use the standalone binary + `PATH`/`serverPath`.

## Out of scope / deferred
- **Marketplace publishing** of platform-specific extensions (still no Marketplace publish — feature 15).
- **Download-on-activation** (rust-analyzer style) — rejected in favor of bundling (approved).
- **JetBrains/LSP4IJ bundling** — that path still uses the separately-installed binary; unchanged.
- **A universal/fat `.vsix`** — not built; platform-specific only.
- **Extra arches** not in the release matrix (e.g. windows-arm64, linux-armhf) — users there use the
  standalone binary + `serverPath`/`PATH`. (See OQ-2 on whether to also ship one generic no-binary `.vsix`.)

## Open questions
- **OQ-1 — bundled-binary location & lookup.** Ship at `<extensionPath>/bin/natural-lsp[.exe]` and resolve
  it from `context.extensionPath`. Confirm `bin/` as the convention (kept out of git — it's populated only
  at package time from `dist/`; `.gitignore` the extension's `bin/`). Recommend yes.
- **OQ-2 — also ship a generic (no-binary) `.vsix`?** A 6th `vsce package` with no `--target` and no bundled
  binary, for platforms outside the 5 (falls back to `PATH`/`serverPath`). Cheap insurance for
  discoverability, but adds an artifact that crashes if the user has no binary on PATH. Recommend **no** for
  now (document the standalone-binary path for other arches); revisit if requested.
- **OQ-3 — version/skew guard.** The bundled binary version is pinned to the release tag (same as the
  extension). Should activation also log the resolved server path + `--version` for diagnosability?
  Recommend a `window/logMessage`-style info log of which server was chosen (bundled vs serverPath vs PATH),
  reusing feature 26's logging conventions on the client side. Confirm.

## Notes
- Editor-client + CI + docs only; no Go/`internal/model`/cache change, no LSP protocol/capability change.
  `serverPath.ts` stays host-free and unit-tested; the extension host supplies `extensionPath` + an
  fs-exists probe. `chmod` is unix-only and failure-tolerant. The release `.vsix` verification (PR #60)
  is extended to also assert the bundled binary. Keep the standalone binaries on the release for the
  `go install`/JetBrains/other-editor paths.
