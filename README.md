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

> **Project status: early development.** The LSP lifecycle (`initialize`/`shutdown`/`exit`) and
> `Content-Length` framing are implemented (features 01–03). `textDocument/didOpen`,
> `textDocument/didChange`, `textDocument/didClose`, and `workspace/didChangeWatchedFiles` are wired to
> an in-memory document store; an `fsnotify`-based workspace watcher re-analyzes files on disk change
> (feature 04). **Workspace indexing and persistent cache are now implemented** (feature 05) with
> content-hash invalidation for git-safe cache. A **hand-written lexer and recursive-descent parser
> producing an AST with ranged syntax diagnostics is implemented** (feature 00) behind the `Analyzer`
> interface. **Per-file call/dependency extraction is now implemented** (feature 06): `CALLNAT`,
> `PERFORM`, `INCLUDE`, `FETCH`, and `RUN` produce relationship edges with caller context (static vs.
> dynamic distinguished). **Cross-file resolution of those edges is now implemented** (feature 07): the
> steplib chain (current library → declared steplibs → SYSTEM, non-transitive), explicit-library bypass
> for `RUN 'pgm' 'lib'`, inline-before-external `PERFORM`, `INCLUDE`→copycode binding, and flat-namespace
> ambiguity diagnostics — exposed as a workspace-index capability. **Adabas data-access extraction is now
> implemented** (feature 08): `READ`/`FIND`/`GET` (read relationships) and `STORE` plus record-form
> `UPDATE`/`DELETE` (write relationships) against DDMs, `DEFINE DATA` variable/parameter definitions, and
> `DEFINE WORK FILE` definitions. **Embedded-SQL extraction is now implemented** (feature 08b): native SQL
> and `PROCESS SQL` DDM read/write edges, `CALLDBPROC` call edges, and host-variable references. This is
> per-file extraction only — cross-file binding (name→DDM/host-var *resolution* across the steplib chain,
> and the record-form `UPDATE`/`DELETE` enclosing-loop file binding) remains future work.
> **Program-structure extraction is now implemented** (feature 09): a per-object hierarchical symbol tree
> (object root, data sections + fields, subroutines, maps, DDM references) with source ranges, backing
> document outline / workspace symbols / hover.
> **Navigation & symbol search is now implemented** (feature 10): the first shipped LSP providers —
> `textDocument/definition` (FR-24), `textDocument/references` (FR-25), and `workspace/symbol` (FR-26).
> The running server now builds and holds a workspace index + resolution set and updates them incrementally
> on document/watched-file changes.
> **Document outline is now implemented** (feature 11): the `textDocument/documentSymbol` provider (FR-27)
> renders feature 09's hierarchical symbol tree as a nested `DocumentSymbol[]`, served from the open-document
> buffer so it tracks unsaved edits.
> **Hover is now implemented** (feature 12): the `textDocument/hover` provider (FR-28) shows module
> metadata (name, location, inbound/outbound counts), subroutine parameter signatures on `PERFORM`
> targets, and DDM field details on data-access statements — the latter backed by a new `.NSD` DDM field
> parser.
> **Code lens is now implemented** (feature 13): the `textDocument/codeLens` provider (FR-29) renders an
> inbound call-count lens and a table-write-summary lens above each object, both activating
> find-references to reveal the call/write sites; enabled by default and disableable via
> `enable_code_lens`.
> **Diagnostics are now published** (feature 14): the server pushes `textDocument/publishDiagnostics`
> (FR-30 parse errors, FR-31 ambiguous resolution), aggregating the parser's ranged syntax diagnostics
> with the resolver's flat-namespace ambiguity warnings and tracking edits across
> didOpen/didChange/didClose/watched-file changes (clearing via an empty array on fix, close, or delete).
> Modeled gaps (dynamic/unresolved references) are never diagnostics (FR-17). **All planned LSP
> providers are now wired** (navigation, document outline, hover, code lens, diagnostics, completion,
> signature help, call hierarchy). There are no published binaries yet; build from source or via the
> VS Code extension.
>
> **A full independent assessment (2026-07-14) — [`docs/assessment-2026-07-14.md`](docs/assessment-2026-07-14.md) —
> confirmed the core is solid but identified five known issues, re-planned as features 19–23.**
> See [Known issues](#known-issues-2026-07-14-assessment) below before relying on completion details,
> non-VS-Code editors, or large-workspace performance.

---

## Features

The capabilities below define the **target** feature set for the first stable release.

**Navigation** *(providers shipped — feature 10)*

- Jump to definition for `CALLNAT`, `FETCH`, `RUN`, and `PERFORM` targets — **shipped**
- Find all references to a subroutine, program, or DDM field across the workspace — **shipped**
- Workspace symbol search by program name or subroutine — **shipped**

**Hover** *(provider shipped — feature 12)*

- Program metadata: module name, location, inbound call count (+ a single outbound-dependency count) — **shipped**
- Subroutine signatures on `PERFORM` targets (inline and external `.NSS`), from the target's `PARAMETER` section — **shipped**
- DDM field names and types on data-access statements, parsed from the referenced `.NSD` — **shipped**
  (physical Adabas/IMS runtime metadata remains out of scope; an un-indexed DDM shows an honest
  "unavailable" message rather than fabricated fields)

**Document outline** *(provider shipped — feature 11)*

- Full symbol tree: `DEFINE DATA` sections, subroutines, maps, DDM references — the underlying
  hierarchical symbol model (feature 09, `FileAnalysis.Structure`) and the `textDocument/documentSymbol`
  provider that renders it as a nested `DocumentSymbol[]` are both **shipped**. The outline is served from
  the open-document buffer, so it reflects unsaved edits.

**Diagnostics** *(provider shipped — feature 14)*

- Parse errors surfaced as diagnostics at the offending position, with a message identifying the
  construct — **shipped** (FR-30); blank lines and comments produce none
- Ambiguous resolution (a name matching objects in more than one library with no library map) reported
  as a distinct **warning**-severity diagnostic naming the candidates — **shipped** (FR-31); declaring a
  disambiguating library map removes it
- Diagnostics track edits and config (open-document `didChange` and external watched-file changes),
  clearing when the cause is fixed — **shipped**; modeled outcomes (dynamic/unresolved references) are
  **never** diagnostics — **shipped** (FR-17)

**Workspace indexing** *(full implementation shipped)*

- Open-document tracking via in-memory store (`textDocument/didOpen|didChange|didClose`) — **shipped**
- On-disk file change detection via `fsnotify` + `workspace/didChangeWatchedFiles` — **shipped**
- Per-file extraction of static `CALLNAT 'LITERAL'` calls — **shipped** (cross-file *resolution* of the edges — **shipped**, feature 07: steplib chain, explicit-library bypass, flat-namespace ambiguity diagnostics)
- `INCLUDE` / copycode dependency tracking — **shipped**
- Dynamic `CALLNAT #VARIABLE` calls flagged as unresolved with caller context preserved — **shipped**
- Persistent cache across sessions (sub-second startup after first index) — **shipped**

**LSP protocol compliance**

- `textDocument/definition` — **shipped** (feature 10)
- `textDocument/references` — **shipped** (feature 10)
- `workspace/symbol` — **shipped** (feature 10)
- `textDocument/hover` — **shipped** (feature 12)
- `textDocument/completion` (module names, subroutine names, DDM field names) — **shipped** (feature 16;
  known wire defect: `detail`/`sortText` serialize as `{}` — fix planned, [feature 19](docs/plans/features/19-protocol-marshaling-unification/plan.md))
- `textDocument/signatureHelp` (parameter interfaces at call sites) — **shipped** (feature 17)
- `textDocument/callHierarchy` (prepare + incoming/outgoing call panels) — **shipped** (feature 18)
- `textDocument/documentSymbol` — **shipped** (feature 11)
- `textDocument/codeLens` (call counts, table write summaries) — **shipped** (feature 13)
- `textDocument/publishDiagnostics` (parse errors, ambiguous resolution) — **shipped** (feature 14)
- `window/workDoneProgress` (indexing progress on first run) — **not yet implemented** (FR-32, P0;
  planned as [feature 21](docs/plans/features/21-async-indexing-and-progress/plan.md))

---

## Known issues (2026-07-14 assessment)

An independent full-project assessment — live stdio wire probes, four specialist reviews, and
LSP 3.17 spec verification — is recorded in
[`docs/assessment-2026-07-14.md`](docs/assessment-2026-07-14.md). The lifecycle, position
encoding, capability set, diagnostics semantics, robustness (FR-43), and the Analyzer seam all
verified clean. Five issues were confirmed and re-planned:

1. **Completion results are corrupted on the wire** — `CompletionItem.detail` and `sortText`
   reach the client as `{}` instead of strings (a stdlib-vs-json/v2 marshaling divergence), so
   the detail label is lost and inline-before-external ordering silently breaks.
   Fix: [feature 19](docs/plans/features/19-protocol-marshaling-unification/plan.md).
2. **The server ignores `rootUri`/`workspaceFolders`** — the workspace root is derived only from
   the server process's working directory (sentinel walk-up). VS Code works because
   `vscode-languageclient` sets the child cwd to the workspace folder; **other editors must be
   started from (or launch the server in) the workspace root**, or every index-backed feature
   silently returns nothing. Fix: [feature 20](docs/plans/features/20-workspace-root-handshake/plan.md).
3. **No indexing progress reporting** (FR-32, P0) — no `$/progress` is ever sent.
   Fix: [feature 21](docs/plans/features/21-async-indexing-and-progress/plan.md).
4. **The cold index build blocks all requests** — it runs synchronously in the `initialized`
   handler (NFR-5). Fix: also [feature 21](docs/plans/features/21-async-indexing-and-progress/plan.md).
5. **Performance/scale claims are unmeasured** (NFR-1/2/3/4) — no benchmarks exist; known hot
   spots at scale include `workspace/symbol` re-reading files per query.
   Fix: [feature 22](docs/plans/features/22-performance-and-scale-verification/plan.md).

Secondary: the `go install` path below does not match the module path in `go.mod` (install from
a clone for now), and `scripts/smoke.sh` needs an explicit binary path — both addressed by
[feature 23](docs/plans/features/23-distribution-hardening/plan.md).

---

## Parser-based extraction

`natural-lsp` uses a hand-written lexer and recursive-descent parser for Natural, modeled on
[natls](https://github.com/MarkusAmshove/natls) (the Java reference implementation). The parser produces a proper AST
that enables completion, signature help, call hierarchy, real syntax diagnostics, and accurate symbol tables — features
that regex extraction cannot deliver reliably.

The parser also handles **embedded SQL**: native Natural SQL statements (`SELECT`/`SELECT SINGLE`, `INSERT`,
SQL-form `UPDATE`/`DELETE`, `MERGE`, `COMMIT`, `ROLLBACK`, `CALLDBPROC`, `READ RESULT SET`) are parsed into the
AST with syntax diagnostics, accepting both the structured (`END-SELECT`/`END-RESULT`) and reporting-mode
(`LOOP`) loop terminators, and `PROCESS SQL` bodies are held as a single opaque, unparsed `<<…>>` span.
**Embedded-SQL extraction** (feature 08b) then walks that AST and emits the relationships those statements
carry: DDM read edges (`SELECT`/`SELECT SINGLE`/`READ RESULT SET`), DDM write edges (`INSERT`/SQL-`UPDATE`/
SQL-`DELETE`/`MERGE`), a `CALLDBPROC` call edge, and host-variable references — including a colon-mandatory
scan of the `PROCESS SQL` opaque body (in-body table names are pass-through text and are *not* bound). What
remains is cross-file **resolution** — binding those extracted SQL table names to DDMs and host-variables to
their declarations across the steplib chain.

Two kinds of analysis gap are handled separately, and neither is dropped silently:

- **Unresolvable references** — e.g. `CALLNAT #VARIABLE`, whose target cannot be determined statically — are noted as
  unresolvable with the call site preserved, so they appear in find-references and outline rather than disappearing.
  (Implemented for `CALLNAT`/`FETCH`/`RUN` as `*_DYNAMIC` edges that retain the call site; cross-file resolution (feature 07) binds resolvable references and records these as `Unresolved(dynamic)` outcomes that keep the call site.)
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

Each release also publishes a `checksums.txt` (SHA-256 of every binary) — verify
your download against it. Place the binary somewhere on your `PATH`:

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

> **Known issue:** the command below does not currently work — `go.mod` declares the module as
> bare `natural-lsp`, not `github.com/dkrieg/natural-lsp`, so the remote install path cannot
> resolve. Until [feature 23](docs/plans/features/23-distribution-hardening/plan.md) reconciles
> the module path, install from a clone: `git clone … && go build -o natural-lsp ./cmd/natural-lsp`.

```bash
go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest
```

`go install` is the intended package-style install path. A native OS
package-manager channel (Homebrew tap, Scoop) is **future work** and not yet
provided.

### Verify an install

Any binary — pre-built, built-from-source, or `go install`ed — can be smoke-checked
with the bundled script, which runs `--version` and a minimal
`initialize → initialized → shutdown → exit` stdio round-trip:

```bash
scripts/smoke.sh "$(command -v natural-lsp)"
```

(The underlying one-liner the script builds on is `natural-lsp --stdio < /dev/null`,
which must start and exit cleanly on EOF.)

---

## Editor setup

> **Workspace-root caveat (all editors except VS Code):** the server currently derives the
> workspace root from **its own working directory** (walking up to the `.natural-lsp.toml`
> sentinel) and ignores the `rootUri`/`workspaceFolders` your editor sends. Make sure the editor
> (and therefore the spawned `natural-lsp` process) is started **inside the workspace** — e.g.
> `cd` to the project before launching Neovim/Helix/Zed — or navigation will silently come up
> empty. VS Code is unaffected because its client library launches the server with the workspace
> folder as cwd. Proper `rootUri` handling is planned as
> [feature 20](docs/plans/features/20-workspace-root-handshake/plan.md).

### VS Code

The companion extension lives in this repo under [`editors/vscode/`](editors/vscode/). It is
distributed as a `.vsix` (not yet on the VS Code Marketplace). Build one, then install it:

```bash
cd editors/vscode
npm ci
npm run package                 # produces natural-lsp-vscode-<version>.vsix
code --install-extension natural-lsp-vscode-0.1.0.vsix
```

The extension handles launching the server automatically when a Natural source file (`.NSP`, `.NSN`, `.NSS`, `.NSC`,
`.NSM`, `.NSL`, `.NSG`, `.NSA`, `.NSH`, `.NSD`, `.NS4`, `.NS7`, `.NS3`, `.NS8`, `.NST`) is opened. No additional
configuration is required if `natural-lsp` is on your `PATH`.

To point at a specific binary location, add to `.vscode/settings.json`:

```json
{
  "naturalLsp.serverPath": "/path/to/natural-lsp"
}
```

The extension version tracks the server's release line. See
[`editors/vscode/README.md`](editors/vscode/README.md) for development, testing, and packaging details.

### Neovim

Neovim needs two things: a `natural` **filetype** for `.NSx` files (Neovim has no
built-in detection for them) and an LSP client that launches `natural-lsp --stdio`
for that filetype. Root detection uses the `.natural-lsp.toml` sentinel.

> Note: the `root_markers`/`root_dir` settings below shape the `rootUri` Neovim sends — which
> the server **currently ignores** (see the workspace-root caveat above). Until
> [feature 20](docs/plans/features/20-workspace-root-handshake/plan.md) ships, start Neovim from
> inside the workspace so the server's own sentinel walk-up finds the root.

**1. Associate `.NSx` files with the `natural` filetype** (required for either LSP
form below — without it the `filetypes`/`filetype` gate never matches):

```lua
vim.filetype.add({
  pattern = {
    ['.*%.[nN][sS].'] = 'natural',  -- matches .NSP/.NSN/…/.NST (any case)
  },
})
```

The equivalent autocmd form, if you prefer:

```lua
vim.api.nvim_create_autocmd({ 'BufRead', 'BufNewFile' }, {
  pattern = { '*.NS*', '*.ns*' },
  command = 'set filetype=natural',
})
```

**2a. Neovim 0.11+ (recommended — built-in `vim.lsp.config`/`vim.lsp.enable`):**

```lua
vim.lsp.config('natural_lsp', {
  cmd = { 'natural-lsp', '--stdio' },
  filetypes = { 'natural' },
  root_markers = { '.natural-lsp.toml', '.git' },
})
vim.lsp.enable('natural_lsp')
```

**2b. Alternative — `nvim-lspconfig`** (still supported; requires Neovim 0.11.3+):

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

**Verify** (against `docs/plans/features/15-editor-clients/sample-workspace/`):

- [ ] Server-side smoke first: `scripts/smoke.sh "$(command -v natural-lsp)"` passes.
- [ ] Open `HELLO.NSP` from the sample workspace; `:set filetype?` reports
      `filetype=natural` and `:LspInfo` (or `:checkhealth lsp`) shows `natural_lsp`
      attached.
- [ ] Caret on `CALLGREET` → `vim.lsp.buf.definition()` (default `grd`/`gd`)
      navigates to `CALLGREET.NSN`; on `SAYHELLO` it navigates to `SAYHELLO.NSS`.

### Zed

Zed's `settings.json` can associate file extensions with a language and point a
known language at a custom server binary, but it **cannot define a brand-new
language** (nor bind a language server to one) on its own — Zed only runs a
language server for a language it already knows, and adding a new language
requires a [Zed language extension](https://zed.dev/docs/extensions/languages).
A first-party Natural Zed extension (which would register the `natural` language,
its Tree-sitter grammar, and the `natural-lsp` server binding) is **future work**.

In the meantime you can associate the `.NSx` extensions and declare the server so
they are ready once a `natural` language is available. Add to your `settings.json`:

```json
{
  "file_types": {
    "Natural": [
      "NSP", "NSN", "NSS", "NSC", "NSM", "NSL", "NSG", "NSA",
      "NSH", "NSD", "NS4", "NS7", "NS3", "NS8", "NST"
    ]
  },
  "lsp": {
    "natural-lsp": {
      "binary": {
        "path": "natural-lsp",
        "arguments": ["--stdio"]
      }
    }
  },
  "languages": {
    "Natural": {
      "language_servers": ["natural-lsp"]
    }
  }
}
```

The server itself launches identically to every other editor
(`natural-lsp --stdio`) and detects the workspace root via the
`.natural-lsp.toml` sentinel.

**Verify:**

- [ ] Server-side smoke: `scripts/smoke.sh "$(command -v natural-lsp)"` passes
      (the automatable lower bound — full in-editor navigation depends on the
      pending Natural language extension above).
- [ ] `.NSx` files open and are associated with the `Natural` language entry
      (once the language extension is installed, go-to-definition on the sample
      workspace's `CALLGREET`/`SAYHELLO` navigates to their definitions).

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

This registers a `natural` language for all 15 `.NSx` file types and launches
`natural-lsp --stdio`. Helix does not pass explicit root markers here — the
server itself finds the workspace root by walking up for the `.natural-lsp.toml`
sentinel, so run Helix from within your Natural project.

**Verify** (against `docs/plans/features/15-editor-clients/sample-workspace/`):

- [ ] Server-side smoke: `scripts/smoke.sh "$(command -v natural-lsp)"` passes.
- [ ] Open `HELLO.NSP`; `:lsp-workspace-command` / the status line shows
      `natural-lsp` active, and `hx --health` lists the `natural` language.
- [ ] Caret on `CALLGREET` → **goto definition** (`gd`) navigates to
      `CALLGREET.NSN`; on `SAYHELLO` it navigates to `SAYHELLO.NSS`.

### JetBrains IDEs (IntelliJ, PyCharm, …)

JetBrains does not auto-discover LSP servers the way VS Code does. The recommended route — which works in
**all** JetBrains IDEs including the free **Community** editions — is the free
**[LSP4IJ](https://github.com/redhat-developer/lsp4ij)** plugin. This repo ships an importable LSP4IJ
server template (all 15 Natural file types, command `natural-lsp --stdio`) so setup is reproducible:

1. Install **LSP4IJ** from the JetBrains Marketplace.
2. **Settings/Preferences → Languages & Frameworks → Language Servers → [+] → Template → Import from
   custom template…** and select [`editors/jetbrains/lsp4ij-template/`](editors/jetbrains/lsp4ij-template/).
3. Open the project root containing your `.natural-lsp.toml` sentinel and open a Natural file.

Full step-by-step instructions (including a manual by-hand alternative and a Verify checklist) are in
**[`editors/jetbrains/README.md`](editors/jetbrains/README.md)**.

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

# When true, the server advertises and serves code lens providers
# (inbound call counts, table write summaries). Default: true
enable_code_lens = true

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

| Construct            | Resolution                       | Edge type             |
|----------------------|----------------------------------|-----------------------|
| `CALLNAT 'LITERAL'`  | Static — resolved to definition          | `CALLS`               |
| `CALLNAT #VARIABLE`  | Dynamic — surfaced as unresolvable       | `CALLS_DYNAMIC`       |
| `FETCH 'LITERAL'`    | Static — navigation edge                 | `NAVIGATES_TO`        |
| `FETCH #VARIABLE`    | Dynamic — surfaced as unresolvable       | `NAVIGATES_TO_DYNAMIC`|
| `RUN 'LITERAL'`      | Static — navigation edge                 | `NAVIGATES_TO`        |
| `RUN #VARIABLE`      | Dynamic — surfaced as unresolvable       | `NAVIGATES_TO_DYNAMIC`|
| `PERFORM subroutine` | Local scope first, then external         | `PERFORMS`            |
| `INCLUDE copycode`   | Resolved to copycode file                | `INCLUDES`            |

A literal `CALLNAT`/`FETCH`/`RUN` target containing an `&` runtime-substitution placeholder (e.g.
`CALLNAT 'PRG&LANG'`) is treated as **dynamic** — it produces the `*_DYNAMIC` edge rather than a false
static edge to a non-existent object. `RUN 'PGM' library-id` records the named target library on the
edge.

### Data access *(extraction shipped — feature 08)*

| Construct                     | Extracted                                                                 |
|-------------------------------|---------------------------------------------------------------------------|
| `READ` / `FIND` / `GET`       | File/DDM name, read relationship — **shipped** (`GET SAME` → no edge)     |
| `STORE`                       | File/DDM name, write relationship — **shipped**                           |
| record `UPDATE` / `DELETE`    | Write relationship at the site, **no file name** (bound from the enclosing loop — resolution deferred, OQ-4) — **shipped** |
| `DEFINE DATA`                 | Variable declarations + parameter interfaces (level/type/dimensions/section kind, REDEFINE nesting) — **shipped** |
| `DEFINE WORK FILE`            | Work-file definitions (number + name; dynamic names recorded verbatim) — **shipped** |
| `.NSD` DDM files              | Field name/type/level, group nesting, MU/PE arrays — parsed from the exported DDM report into `FileAnalysis.Definitions` (fixed-column line-scanner) — **shipped** (feature 12; DB short-name/descriptor/suppression columns dropped, SQL DDMs out of scope) |

Scope is Adabas-style data access against DDMs.

### Embedded-SQL access *(extraction shipped — feature 08b)*

| Construct                                   | Extracted                                                                  |
|---------------------------------------------|----------------------------------------------------------------------------|
| `SELECT` / `SELECT SINGLE`                  | `FROM` table → DDM **read** relationship — **shipped**                     |
| `READ RESULT SET`                           | Read-access site (result-set handle is not a DDM → empty name) — **shipped** |
| `INSERT` / SQL-`UPDATE` / SQL-`DELETE` / `MERGE` | Target table → DDM **write** relationship — **shipped**                |
| `CALLDBPROC`                                | Stored-procedure **call** edge (literal → `CALLS`; variable/`&`-placeholder → `CALLS_DYNAMIC`) — **shipped** |
| `PROCESS SQL`                               | `ddm-name` → DDM read edge; opaque `<<…>>` body scanned for colon host-vars (in-body table names *not* bound) — **shipped** |
| host variables (native + opaque body)       | `HostVarRef` references (bare-or-colon in native clauses; colon-mandatory in the opaque body) — **shipped** |

SQL table operands are `.NSD` DDM names (the same namespace as Adabas). Per-file extraction only —
binding these SQL-sourced DDM/host-var references to their definitions (cross-file **resolution**) is
future work.

### Program structure *(extraction shipped — feature 09)*

Each object is walked into a per-object hierarchical **symbol tree** (`FileAnalysis.Structure`), the
backbone for document outline, workspace symbols, and hover. Every node carries a source `Range` (the
whole construct) and a `SelectionRange` (the name token), mirroring LSP `DocumentSymbol`.

| Symbol kind          | Source                                                        |
|----------------------|---------------------------------------------------------------|
| object (root)        | the object itself (name = file base without extension) — **shipped** |
| data section         | each `DEFINE DATA` section (LOCAL/PARAMETER/…), fields nested as children incl. REDEFINE — **shipped** |
| data field           | `DEFINE DATA` items (level/array/REDEFINE nesting) — **shipped** |
| subroutine           | `DEFINE SUBROUTINE` — **shipped**                             |
| map                  | `DEFINE MAP` (with its fields) — **shipped**                  |
| DDM reference        | named `READ`/`FIND`/`GET`/`STORE` + SQL table references — **shipped** |

Children are deterministically source-ordered; fields are grouped into their owning section by source-range
containment (so multiple same-kind sections keep their own fields). Deferred: inline-vs-external subroutine
distinction, type-specific members for helproutines/classes/functions, and `INCLUDE` copycode as an outline
node. Both the `workspace/symbol` provider over this tree (feature 10) and the per-file
`textDocument/documentSymbol` (outline) provider that renders it (feature 11) are **shipped**.

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
    server.go              LSP lifecycle + dispatch; builds/holds the workspace
                           index + resolution; incremental update on change
    position.go            model<->protocol position/range conversion (ADR-008)
    cursor.go              cursor position -> reference-site (edge/data-access) lookup
    definition.go          textDocument/definition provider
    references.go          textDocument/references provider (reverse sweep)
    workspace_symbols.go   workspace/symbol provider (Structure name search)
    document_symbols.go    textDocument/documentSymbol provider (outline; store-first)
    degradation.go         per-file analyze + graceful-degradation helpers
    handlers.go            (stub) package doc + TODO
    progress.go            (stub) window/workDoneProgress helpers — TODO
    diagnostics.go         (stub) diagnostic aggregation/publish — TODO

  document/
    store.go               In-memory document store (didOpen/didChange/didClose)
    sync.go                File watcher for workspace files

  workspace/
    index.go               Cross-file symbol table
    cache.go               Serialize/deserialize index to disk
    resolution.go          Steplib-chain resolution: current library → ordered
                           steplibs → system; library map; ambiguity diagnostics

  model/
    model.go               Analyzer output types (FileAnalysis, symbols, edges,
                           data access, definitions, work files)
                           — the contract shared by analysis, workspace, server;
                           free of backend internals

  analysis/
    analyzer.go            Analyzer interface (the replaceable-backend seam)
    natural/
      analyzer.go          Parser-based extraction pipeline (hand-written
                           lexer + recursive-descent parser)
      hover.go             Hover content builders
      calls.go             CALLNAT / FETCH / RUN / PERFORM extraction
                           (produces unresolved references; see resolution.go)
      data.go              DEFINE DATA / READ / FIND / GET / STORE /
                           record UPDATE|DELETE / DEFINE WORK FILE extraction
      sql.go               Embedded-SQL extraction (DDM edges, CALLDBPROC,
                           host-var refs, PROCESS SQL opaque-body scan)
      structure.go         Program-structure extraction (per-object
                           hierarchical symbol tree for outline/symbols/hover)

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
> highest-risk logic out of the parser backend and behind the workspace index.

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
- **Adabas verbs** (`READ`, `FIND`, `GET` against Adabas files) are extracted structurally, and DDM **field
  definitions** are parsed from exported `.NSD` files (feature 12) so hover can show field name/type. The
  **physical/runtime** Adabas DDM metadata (occurrence counts, storage) is still not resolved, and cross-file
  resolution of DDM references is future work. IMS segment metadata requires external configuration.
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
