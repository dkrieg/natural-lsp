# Natural LSP — VS Code extension

Language support for **Software AG Natural** in Visual Studio Code, backed by the
[`natural-lsp`](../../README.md) language server. The extension launches the server
over stdio (`natural-lsp --stdio`) — the same way every other editor client does —
and lets VS Code deliver navigation, references, hover, document outline, code lens,
and diagnostics for Natural source objects.

## What it does

- **Language association** for all 15 Natural object extensions:
  `.NSP .NSN .NSS .NSC .NSM .NSL .NSG .NSA .NSH .NSD .NS4 .NS7 .NS3 .NS8 .NST`
  (matched case-insensitively — mainframe exports are typically upper-case; see
  [File-extension casing](#file-extension-casing)).
- **Syntax highlighting** via a bundled TextMate grammar covering keywords, `*`/`**`
  full-line comments, `/*` rest-of-line comments, and string / numeric literals.
- **Server-backed language features** — whatever the running `natural-lsp` server
  advertises (go-to-definition, find-references, workspace symbols, document outline,
  hover, code lens, publish-diagnostics).

The extension activates when the first Natural document is opened
(`onLanguage:natural`).

## Requirements

The **platform-specific `.vsix`** published on each GitHub Release **bundles the `natural-lsp` server
binary**, so no separate server install is needed — just install the `.vsix` matching your OS/arch. If you
use a source-built or generic `.vsix` (no bundled binary), you need the `natural-lsp` server installed some
[documented way](../../README.md#installation) and reachable on your `PATH` (or via `naturalLsp.serverPath`).

The extension resolves the server as: **`naturalLsp.serverPath` (if set) → bundled binary in the `.vsix`
(if present) → `natural-lsp` on `PATH`**.

## Install

Install the packaged `.vsix` (this extension is not published to the Marketplace) — prefer the
platform-specific one from the release:

```bash
# platform ∈ linux-x64 | linux-arm64 | darwin-x64 | darwin-arm64 | win32-x64
code --install-extension natural-lsp-vscode-<version>-<platform>.vsix
```

Then open any Natural file (e.g. a `.NSP`) in a workspace that has a
`.natural-lsp.toml` sentinel at its root, and the server starts automatically.

## Configuration

| Setting | Default | Description |
| --- | --- | --- |
| `naturalLsp.serverPath` | `"natural-lsp"` | Path to the `natural-lsp` server binary. Defaults to looking it up on `PATH`; set it to an absolute path if the binary lives elsewhere. |

Commands:

- **Natural LSP: Restart Language Server** (`naturalLsp.restart`) — stops and
  restarts the client (handy after installing/updating the server binary).

### Graceful degradation

If the server binary cannot be found or started (e.g. not on `PATH` and no
`naturalLsp.serverPath` set), the extension surfaces an actionable error
notification pointing you at the setting and `PATH` — it does **not** crash the
editor. This mirrors the server's own graceful-degradation stance.

### File-extension casing

VS Code's language-detection path treats the `contributes.languages.extensions`
list case-sensitively in some code paths, while Natural exports from the mainframe
are conventionally upper-case (`.NSP`). To guarantee association regardless of
casing or platform, the manifest declares **both** the lower-case `extensions` list
**and** `filenamePatterns` globs that match each extension case-insensitively (e.g.
`**/*.[nN][sS][pP]`). The integration tests open upper-case fixtures for all 15
types and assert `languageId === 'natural'`.

## Versioning

The extension `version` starts at `0.1.0` and tracks the `natural-lsp` **server
release line** going forward (publisher `dkrieg`, matching the
`github.com/dkrieg/natural-lsp` module path). A given extension release is built
against, and expects to talk to, the correspondingly-versioned server.

## Develop / build / test / package

From `editors/vscode/`:

```bash
npm install          # install dependencies (commits package-lock.json)
npm run compile      # tsc typecheck + emit to out/
npm run watch        # tsc in watch mode
npm run lint         # eslint over src/
npm run test:unit    # pure Mocha unit tests (no VS Code host):
                     #   resolveServerPath + TextMate grammar scopes
npm test             # @vscode/test-electron integration suite (downloads a
                     #   pinned VS Code, runs the file-association + launch tests)
npm run package      # vsce package → natural-lsp-vscode-<version>.vsix
```

### Test tiers

- **Unit** (`src/test/unit/**`) — run with plain Mocha against compiled JS, no VS
  Code host required. Covers `resolveServerPath` resolution and grammar scope
  assertions (loaded via `vscode-textmate` + `vscode-oniguruma`).
- **Integration** (`src/test/suite/**`) — run inside a real, pinned VS Code
  instance via `@vscode/test-electron`. Covers the 15-extension language
  association and a live server launch.

### Server binary for the launch test

The integration launch test needs a real `natural-lsp` binary to talk to. Build it
from the repo root and point the harness at it via the `NATURAL_LSP_SERVER_PATH`
environment variable:

```bash
# from the repository root
go build -o editors/vscode/.vscode-test-server/natural-lsp ./cmd/natural-lsp
cd editors/vscode
NATURAL_LSP_SERVER_PATH="$(pwd)/.vscode-test-server/natural-lsp" npm test
```

If `NATURAL_LSP_SERVER_PATH` is unset (or points at a missing file), the launch
test **skips** (rather than fails) so the rest of the suite still runs; the
file-association tests need no server. CI builds the Go binary and sets this
variable, running the electron suite headless under `xvfb`.

The `@vscode/test-electron` download is pinned to VS Code `1.85.0` for
reproducibility (see `src/test/runTest.ts`).
