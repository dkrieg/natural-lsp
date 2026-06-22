# natural-lsp

A Language Server Protocol implementation for [Software AG Natural](https://www.softwareag.com/en_corporate/platform/adabas-natural.html) —
a Go-based LSP server with a hand-written parser delivering navigation, completion, references, hover, and call hierarchy
for Natural codebases on any LSP-capable editor.

Natural is a 4GL language widely deployed on IBM z/OS mainframes, typically alongside COBOL, Adabas, and IMS.
[natls](https://github.com/MarkusAmshove/natls) (Java, MIT) is the existing parser-based LSP server for Natural.
`natural-lsp` is a Go alternative built around a hand-written lexer and recursive-descent parser, with
config-driven library mapping independent of NaturalONE project files and a git-safe content-hash cache.

It operates on **filesystem-based Natural sources** — the `.NSx` object files used by NaturalONE / SPoD — rather than
objects stored only in the mainframe Natural/Adabas library system. Natural that lives solely on the mainframe must be
exported to files before it can be indexed.

> **Project status: early development / design stage.** This README describes the **target** design and feature set.
> The server is not yet released — there are no published binaries, and the benchmarks and capabilities below are
> goals, not all implemented today. Implemented behavior will be marked as the code lands.

---

## Features

The capabilities below define the **target** feature set for the first stable release.

**Navigation**

- Jump to definition for `CALLNAT`, `FETCH`, `RUN`, and `PERFORM` targets
- Find all references to a subroutine, program, or DDM field across the workspace
- Workspace symbol search by program name or subroutine

**Hover**

- Program metadata: module name, location, inbound call count
- Subroutine signatures on `PERFORM` targets
- DDM field names, types, and file associations on data access statements

**Document outline**

- Full symbol tree: `DEFINE DATA` sections, subroutines, maps, external calls

**Workspace indexing**

- Cross-file resolution of static `CALLNAT 'LITERAL'` calls
- `INCLUDE` / copycode dependency tracking
- Dynamic `CALLNAT #VARIABLE` calls flagged as unresolved with caller context preserved
- Incremental re-analysis on file change — only changed files re-indexed
- Persistent cache across sessions (sub-second startup after first index)

**LSP protocol compliance**

- `textDocument/definition`
- `textDocument/references`
- `textDocument/hover`
- `textDocument/completion` (module names, subroutine names, DDM field names)
- `textDocument/signatureHelp` (parameter interfaces at call sites)
- `textDocument/callHierarchy` (incoming/outgoing call panels)
- `textDocument/documentSymbol`
- `workspace/symbol`
- `textDocument/codeLens` (call counts, table write summaries)
- `window/workDoneProgress` (indexing progress on first run)

---

## Parser-based extraction

`natural-lsp` uses a hand-written lexer and recursive-descent parser for Natural, modeled on
[natls](https://github.com/MarkusAmshove/natls) (the Java reference implementation). The parser produces a proper AST
that enables completion, signature help, call hierarchy, real syntax diagnostics, and accurate symbol tables — features
that regex extraction cannot deliver reliably.

Two kinds of analysis gap are handled separately, and neither is dropped silently:

- **Unresolvable references** — e.g. `CALLNAT #VARIABLE`, whose target cannot be determined statically — are noted as
  unresolvable with the call site preserved, so they appear in find-references and outline rather than disappearing.
- **Parse errors** — source the parser cannot interpret — are surfaced as LSP diagnostics so they are visible in the
  editor, not silently discarded.

The parser sits behind the `Analyzer` interface so the backend can evolve (e.g. to a tree-sitter grammar) without
touching the LSP layer.

---

## Installation

### Pre-built binary (recommended)

Download the appropriate binary for your platform
from [GitHub Releases](https://github.com/dkrieg/natural-lsp/releases):

```
natural-lsp-linux-amd64
natural-lsp-linux-arm64
natural-lsp-darwin-amd64
natural-lsp-darwin-arm64
natural-lsp-windows-amd64.exe
```

Place it somewhere on your `PATH`:

```bash
# Linux / macOS
chmod +x natural-lsp-linux-amd64
mv natural-lsp-linux-amd64 /usr/local/bin/natural-lsp

# Verify
natural-lsp --version
```

### Build from source

Requires Go 1.26+.

```bash
git clone https://github.com/dkrieg/natural-lsp
cd natural-lsp
go build -o natural-lsp ./cmd/natural-lsp
```

### go install

```bash
go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest
```

---

## Editor setup

### VS Code

Install the companion extension from the VS Code Marketplace or directly from a `.vsix`:

```bash
code --install-extension natural-lsp-vscode.vsix
```

The extension handles launching the server automatically when a Natural source file (`.NSP`, `.NSN`, `.NSS`, `.NSC`,
`.NSM`, `.NS4`, `.NS7`, and other `.NSx` types) is opened. No additional configuration required if `natural-lsp` is on your `PATH`.

To point at a specific binary location, add to `.vscode/settings.json`:

```json
{
  "naturalLsp.serverPath": "/path/to/natural-lsp"
}
```

### Neovim (nvim-lspconfig)

```lua
require('lspconfig').configs['natural_lsp'] = {
  default_config = {
    cmd = { 'natural-lsp', '--stdio' },
    filetypes = { 'natural' },
    root_dir = require('lspconfig.util').root_pattern(
      '.natural-lsp.toml', '.git'
    ),
  }
}
require('lspconfig').natural_lsp.setup({})
```

### Zed

```json
{
  "lsp": {
    "natural-lsp": {
      "binary": {
        "path": "natural-lsp",
        "arguments": [
          "--stdio"
        ]
      }
    }
  },
  "languages": {
    "Natural": {
      "language_servers": [
        "natural-lsp"
      ]
    }
  }
}
```

### Helix (`languages.toml`)

```toml
[[language]]
name = "natural"
scope = "source.natural"
file-types = ["NSP", "NSN", "NSS", "NSC", "NSM", "NSL", "NSG", "NSA", "NSH", "NSD", "NS4", "NS7", "NS3", "NS8", "NST"]
language-servers = ["natural-lsp"]

[language-server.natural-lsp]
command = "natural-lsp"
args = ["--stdio"]
```

### JetBrains IDEs (IntelliJ, PyCharm, …)

JetBrains does not auto-discover LSP servers the way VS Code does. The recommended route is the free
**[LSP4IJ](https://github.com/redhat-developer/lsp4ij)** plugin, which works in all JetBrains IDEs — including the
Community editions:

1. Install **LSP4IJ** from the JetBrains Marketplace.
2. Add a new language server (*New Language Server → Command*) with the command:

   ```
   natural-lsp --stdio
   ```

3. Associate it with the Natural file types (`.NSP`, `.NSN`, `.NSS`, `.NSC`, `.NSM`, `.NSL`, `.NSG`, `.NSA`, `.NSH`,
   `.NSD`, `.NS4`, `.NS7`, `.NS3`, `.NS8`, `.NST`).

The native JetBrains LSP API (`com.intellij.platform.lsp`) is an alternative, but it requires a paid/Ultimate-tier IDE
and a custom plugin — LSP4IJ is the simpler, more portable path.

---

## Workspace configuration

The server locates the workspace root by walking up from the opened file (or the LSP `initialize` workspace root)
looking for a `.natural-lsp.toml` sentinel file. Place this file at your Natural codebase root.

To generate a fully-commented starter config with every key shown at its default:

```bash
natural-lsp --init > .natural-lsp.toml
```

All keys are optional — the server applies defaults for any key you omit. The full schema:

```toml
# .natural-lsp.toml

[workspace]
# Object types to index (defaults shown). The default set covers all 15
# recognized Natural constructs. Exact extensions depend on how your objects
# were exported — adjust to match your tooling.
extensions = [
  # Core types
  ".NSP",  # program
  ".NSN",  # subprogram
  ".NSS",  # external subroutine
  ".NSC",  # copycode (INCLUDE targets)
  ".NSM",  # map
  ".NSL",  # local data area
  ".NSG",  # global data area
  ".NSA",  # parameter data area
  ".NSH",  # helproutine
  ".NSD",  # DDM
  # Extended types
  ".NS4",  # class (NaturalX)
  ".NS7",  # function (user-defined)
  ".NS3",  # dialog (Natural for Windows)
  ".NS8",  # adapter (Natural Ajax)
  ".NST",  # text
]

# Map non-standard extensions to their construct type. Useful when files were
# exported with a different suffix (e.g. legacy tools using .NAT for programs).
# Valid values: program, subprogram, externalsubroutine, copycode, map,
#   localdataarea, globaldataarea, parameterdataarea, helproutine, ddm,
#   class, function, dialog, adapter, text
# [workspace.extension_types]
# ".NAT" = "program"

# Directories to exclude from indexing
exclude = ["archive", "backup", ".git"]

# Maximum file size to index (bytes)
max_file_size = 5_000_000

[cache]
# Where to write the workspace index cache
# Defaults to .natural-lsp-cache/ at workspace root
path = ".natural-lsp-cache"

[analysis]
# Treat CALLNAT #VARIABLE as an unresolved external dependency
# rather than an error. Default: true
flag_dynamic_calls = true

# Minimum token length to consider a string literal a potential
# module name in dynamic CALLNAT resolution heuristics
dynamic_call_min_length = 6

[resolution]
# Natural resolves CALLNAT / PERFORM / FETCH targets by walking a steplib
# chain — current library first, then each steplib in order, then SYSTEM —
# NOT by file path. The same module name can exist in multiple libraries,
# so the search order is what disambiguates.
#
# Map workspace directories to Natural libraries and declare each library's
# steplib search order. If no libraries are declared, the server treats the
# whole workspace as a single flat namespace and emits a diagnostic when a
# name resolves ambiguously.
[[resolution.library]]
name = "MYAPP"
path = "src/MYAPP"
steplibs = ["COMMON", "SYSTEM"]

[[resolution.library]]
name = "COMMON"
path = "src/COMMON"
```

---

## Workspace indexing

On first open, the server indexes the entire workspace. Progress is reported via `window/workDoneProgress` — your editor
will show a status bar indicator:

```
Natural LSP: Indexing workspace… 1,243 / 2,891 files (43%)
```

The completed index is serialized to `.natural-lsp-cache/` (gitignored by default). Subsequent startups load from cache
and re-analyze only files whose content hash has changed since the last run (content hashing rather than mtime keeps the
cache valid across git checkouts; a cache-format version forces a full rebuild on upgrade). The figures below are
**design targets**, not measured benchmarks — cold index time is expected to scale roughly linearly with codebase size:

| Codebase size | First index | Subsequent startup |
|---------------|-------------|--------------------|
| 500 files     | ~3s         | <1s                |
| 5,000 files   | ~25s        | <1s                |
| 30,000 files  | ~3min       | <1s                |

---

## Supported Natural constructs

### Call relationships

| Construct            | Resolution                       | Edge type       |
|----------------------|----------------------------------|-----------------|
| `CALLNAT 'LITERAL'`  | Static — resolved to definition          | `CALLS`       |
| `CALLNAT #VARIABLE`  | Dynamic — surfaced as unresolvable       | `CALLS`       |
| `FETCH 'LITERAL'`    | Static — navigation edge                 | `NAVIGATES_TO`|
| `RUN 'LITERAL'`      | Static — navigation edge                 | `NAVIGATES_TO`|
| `PERFORM subroutine` | Local scope first, then external         | `PERFORMS`    |
| `INCLUDE copycode`   | Resolved to copycode file                | `INCLUDES`    |

### Data access

| Construct                     | Extracted                                   |
|-------------------------------|---------------------------------------------|
| `READ` / `FIND` / `GET`       | File/DDM name, read relationship            |
| `STORE` / `UPDATE` / `DELETE` | File/DDM name, write relationship           |
| `DEFINE DATA`                 | Variable declarations, parameter interfaces |
| `DEFINE WORK FILE`            | Work file definitions                       |

### Program structure

| Construct                            | Symbol kind   |
|--------------------------------------|---------------|
| Program file root                    | `Program`     |
| `DEFINE SUBROUTINE`                  | `Subroutine`  |
| `DEFINE DATA LOCAL/GLOBAL/PARAMETER` | `DataSection` |
| `DEFINE MAP` / `.NSM` files          | `Map`         |
| DDM references                       | `DDM`         |

---

## Architecture

```
cmd/natural-lsp/
  main.go                  Binary entrypoint — stdio LSP server

internal/
  config/
    config.go              .natural-lsp.toml parsing, defaults, validation,
                           workspace-root discovery, library map

  server/
    server.go              LSP lifecycle: initialize, shutdown
    handlers.go            textDocument/* and workspace/* dispatch
    progress.go            window/workDoneProgress helpers
    diagnostics.go         Collects diagnostics from extraction + resolution
                           and publishes them

  document/
    store.go               In-memory document store (didOpen/didChange/didClose)
    sync.go                File watcher for workspace files

  workspace/
    index.go               Cross-file symbol table
    cache.go               Serialize/deserialize index to disk
    resolution.go          Steplib-chain resolution: current library → ordered
                           steplibs → system; library map; ambiguity diagnostics

  model/
    model.go               Analyzer output types (FileAnalysis, symbols, edges)
                           — the contract shared by analysis, workspace, server;
                           free of backend internals

  analysis/
    analyzer.go            Analyzer interface (the replaceable-backend seam)
    natural/
      analyzer.go          Regex-based extraction pipeline
      symbols.go           Map FileAnalysis → LSP SymbolInformation
      hover.go             Hover content builders
      calls.go             CALLNAT / FETCH / RUN / PERFORM extraction
                           (produces unresolved references; see resolution.go)
      data.go              DEFINE DATA / READ / STORE extraction

editors/
  vscode/                  VS Code companion extension (TypeScript)
  jetbrains/               JetBrains integration (LSP4IJ config / plugin)
                           Neovim / Zed / Helix are configured via the docs above

testdata/
  workspace/               Sanitized Natural programs for integration tests
                           (include multi-library cases for resolution)
  *.NSP                    Unit test fixtures per construct
```

> **Extraction vs. resolution.** Per-file extraction (`analysis/natural/`) produces *unresolved*
> references with caller context. Cross-file **resolution** (`workspace/resolution.go`) walks the
> steplib chain and the configured library map to bind those references to definitions — keeping the
> highest-risk logic out of the regex backend and behind the workspace index.

---

## Development

### Required software

All tooling is cross-platform — install via your OS package manager or the official instructions linked
below. Versions are specified, not install commands, so this is OS-independent.

| Tool | Version | Purpose | Install |
|------|---------|---------|---------|
| [Go](https://go.dev) | 1.26 or newer | build and test the server | <https://go.dev/doc/install> |
| [just](https://just.systems) | 1.0 or newer | task runner for the dev commands below | <https://just.systems/man/en/packages.html> |
| [Git](https://git-scm.com) | any recent | version control and the pre-push hook | <https://git-scm.com/downloads> |
| [actionlint](https://github.com/rhysd/actionlint) | optional | lint the GitHub Actions workflow locally | <https://github.com/rhysd/actionlint/blob/main/docs/install.md> |

After cloning, enable the pre-push gate once:

```bash
just install-hooks   # configures the pre-push hook to run `just verify`
```

### Common tasks

```bash
just --list             # list all recipes
just verify             # full gate: gofmt + vet + build + unit (-race) + integration tests
just test               # unit tests with the race detector
just test-integration   # integration tests (builds the binary, runs the `integration` build tag)
just build              # build the server binary
./natural-lsp --stdio < /dev/null   # smoke test: should print initialize response shape
```

`just verify` is the **single gate** that runs locally (via the pre-push hook) and in CI — so if it
passes locally, CI should pass. There is no need to memorize the underlying `go` commands; `just --list`
is the entry point.

### Releases

Releases are cut by maintainers from the GitHub **Actions → Release → Run workflow** button (a manual
`workflow_dispatch`). Enter the version tag (e.g. `v1.2.3`); the workflow runs the full `just verify`
gate, cross-compiles every platform via `just release`, then creates the git tag and a GitHub Release
with the binaries and `checksums.txt` attached. Dispatch it from the `main` commit you intend to release.

To produce the same artifacts locally (into `dist/`):

```bash
just release v1.2.3
```

### Adding a test case

When you encounter a Natural construct that the analyzer handles incorrectly:

1. Create a minimal `.NSP` file in `testdata/` that reproduces the issue
2. Write a unit test in `internal/analysis/natural/analyzer_test.go` asserting the expected extraction
3. Fix the analyzer
4. The testdata file becomes a permanent regression fixture

---

## Known limitations

- **Library / steplib resolution** is configuration-driven (see `[resolution]`). Without a declared library map, the
  workspace is treated as a single flat namespace, and modules sharing a name across libraries cannot be disambiguated.
- **Dynamic `CALLNAT #VARIABLE`** calls cannot be statically resolved. The call site is retained so they appear in
  find-references and outline rather than disappearing silently.
- **Adabas verbs** (`READ`, `FIND`, `GET` against Adabas files) are extracted structurally but Adabas DDM metadata is
  not resolved. IMS segment metadata requires external configuration.
- **Natural preprocessor macros** and code generation constructs may not extract correctly.
- **Column-sensitive syntax** (fixed-format Natural) is handled for common patterns; unusual legacy formatting may
  produce incomplete extraction rather than errors.

---

## License

MIT. See [LICENSE](LICENSE).

---

## Contributing

Issues and PRs welcome. If you encounter a Natural construct that the analyzer mishandles, opening an issue with a
minimal reproducer is the most useful contribution. Testdata fixtures of sanitized (non-proprietary) Natural code that
exercise edge cases are particularly valuable.
