# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

**Features 00–06 shipped** — the parser foundation (feature 00: lexer + recursive-descent parser + AST), workspace indexing/persistent cache, and call/dependency extraction (feature 06) are implemented. Features 07–08 (data-access extraction, completion, signature help, call hierarchy) remain as stubs (`data.go`, `hover.go`, `symbols.go` are package-doc + TODO only).

`internal/config` is fully implemented (feature 01): workspace-root discovery (`.natural-lsp.toml`
sentinel walk-up), config loading with decode-onto-defaults semantics, per-field validation with CR-6
fail-safe (bad value → default + actionable `Problem`, never crash), directory-exclusion predicate
(`IsExcluded`), skip-reason surface (`SkipReason`), library-map parsing (declared order preserved),
analysis-options parsing, custom extension-type mapping (`[extension_types]` table), and a `Sample()`
generator for `--init`. Default indexed set: all 15 Natural extensions (10 core + 5 extended; see below).

`internal/model` and `internal/analysis/natural` have object-type recognition (feature 02):
`model.ObjectType` (16 constants with stable string values), `model.Diagnostic`
(`Message`, `Severity`, and a positional `Range` — added in feature 00), and
`model.FileAnalysis.ObjectType`/`Diagnostics`/`AST` fields. The `analysis/natural` backend classifies
every file by extension (case-insensitive, custom-mapping-aware) via `Analyze(path, content)`.
Regression fixtures for all 15 types live under `testdata/objecttype/`.

`internal/analysis/natural` has a hand-written **lexer** (`lexer.go`) and **recursive-descent parser**
(`parser.go`) producing a real **AST** (`ast.go`) — feature 00, the foundation for all extraction
features. The lexer normalizes case, lexes Natural identifiers (incl. `#`/`&` prefixes and embedded
hyphens) as single tokens, handles `*`/`**` full-line comments (line-start only) and `/*` rest-of-line
comments, string/numeric literals, operators, and treats `\r\n` as one line terminator. The parser is
**error-recovering**: it parses `CALLNAT`/`PERFORM`/`INCLUDE`/`FETCH [REPEAT|RETURN]`/`RUN`/`READ`/`STORE`,
`DEFINE DATA` (level numbers, types/formats, array dimensions, `REDEFINE`, group nesting), `DEFINE
SUBROUTINE`, and `DEFINE MAP` into AST nodes carrying real source positions, and emits ranged syntax
diagnostics (`Program.Diagnostics`) for malformed statements while retaining valid surrounding ones (no
silent gaps — FR-30/M-6). `Analyze` surfaces the parsed `*Program` as `FileAnalysis.AST` and copies the
parser's ranged diagnostics into `FileAnalysis.Diagnostics`. A `FuzzParse` target guards the parser
entry point (never panics, always returns a non-nil `*Program`). Fixtures live under `testdata/parser/`.

`internal/analysis/natural/calls.go` implements **call/dependency extraction** (feature 06):
`extractEdges(*Program)` walks the AST and emits `model.EdgeEntry` values into `FileAnalysis.Edges` (wired
in by `Analyze`). Per-construct edge kinds: `CALLNAT` → `EdgeCalls` (literal) / `EdgeCallsDynamic`
(variable); `PERFORM` → `EdgePerforms`; `INCLUDE` → `EdgeIncludes`; `FETCH`/`RUN` → `EdgeNavigatesTo`
(literal) / `EdgeNavigatesToDynamic` (variable). Two modeled gaps are never silent and never become
diagnostics: variable targets become *dynamic* edges with caller context preserved, and a literal target
containing an `&` runtime-substitution placeholder (e.g. `CALLNAT 'PRG&LANG'`) is **downgraded to the
dynamic kind** rather than producing a false static edge (FR-18; CALLNAT/FETCH/RUN only — INCLUDE
copycode is compile-time and excluded). An inline `PERFORM` target that matches a same-object `DEFINE
SUBROUTINE` carries that definition's range in `EdgeEntry.Target` (else the zero range — cross-file
binding is deferred to the resolution feature). A `RUN program-id library-id` records the library
qualifier on `EdgeEntry.Library` (FETCH has no source-level library — its `operand2` is a stack
parameter, not a library). Edges are returned in global source order (stable sort on `Source.Start`).
This feature added two purely-additive `internal/model` members (`EdgeNavigatesToDynamic` and
`EdgeEntry.Library`); persisting `Library` bumped the cache-format version (`0.2.0` → `0.3.0`). Parse
errors continue to flow through `Program.Diagnostics`/`FileAnalysis.Diagnostics`, keeping the
edge/diagnostic channels separate (FR-17/M-6); extraction over a partial/malformed AST never panics and
retains the edges it could extract (FR-43). Fixtures live under `testdata/calls/`.

`internal/server/` implements the LSP lifecycle (feature 03): `Run(ctx, r, w, version, root, cfg, az,
logger)` serves JSON-RPC 2.0 over `Content-Length`-framed stdio (`go.lsp.dev/jsonrpc2` v1.0.0). The
server enforces the `initialize → initialized → shutdown → exit` lifecycle; the `initialize` response
advertises `textDocumentSync: Full` and `positionEncoding` (UTF-8 preferred, UTF-16 default — ADR-008)
with no feature providers yet. Graceful degradation (FR-43): oversized files are skipped with
`SkipTooLarge`, excluded paths with `SkipExcluded`, unrecognized extensions processed as `ObjectUnknown`,
and analyzer panics are recovered per-file without aborting the batch — every skip/recovery is logged to
stderr. Per-request panic recovery returns a JSON-RPC `InternalError` and keeps the loop alive. SIGTERM
is handled via a context-watcher goroutine that closes the stream to unblock the blocking bufio reader.
A `FuzzProcessFile` target guards the file-processing entry point (ADR-013). Feature 04 added
`textDocument/didOpen`, `textDocument/didChange` (Full-sync; partial-change attempts are logged and
skipped), `textDocument/didClose`, and `workspace/didChangeWatchedFiles` handlers. After `initialized`,
the server sends `client/registerCapability` for `workspace/didChangeWatchedFiles` when the client
advertises `Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration`. A background `fsnotify`
watcher (`document.NewWatcher`) is started at `initialized` and closed on shutdown.

`internal/document/` (feature 04) is fully implemented. `Store` is a concurrency-safe in-memory map of
open documents keyed by LSP URI; it re-analyzes content on `Open`/`Update` via an `AnalyzeFunc`
injection (avoiding circular import with `internal/server`) and removes entries on `Close`, with panic
recovery on every analysis call (FR-43). `Watcher` uses `fsnotify` v1.10.1 for recursive workspace
watching — `filepath.WalkDir` + per-directory `Add`, extension filtering, and a 100 ms trailing-edge
debounce — with per-call panic recovery. `internal/workspace/` implements cross-file indexing (index.go) and persistent cache with content-hash invalidation (cache.go).

`natural-lsp` is a Go-based Language Server Protocol server for **Software AG Natural**, a 4GL widely deployed on IBM
z/OS mainframes. It uses a hand-written lexer + recursive-descent parser (modeled on
[natls](https://github.com/MarkusAmshove/natls), Java/MIT) to index a Natural codebase and serve navigation, completion,
references, hover, call hierarchy, document outline, and workspace symbols to any LSP-capable editor.

## Commands

Module is `natural-lsp` (`go.mod`), targeting Go 1.26. Note the README's `go install` path uses
`github.com/dkrieg/natural-lsp/cmd/natural-lsp` — reconcile the module path before publishing.

Task runner is **`just`** (install: `brew install just`; `just --list` shows all recipes). The same
gate — **`just verify`** — runs in the pre-push hook, in `/finalize-feature`, and in CI, so a local
pass means CI should pass.

```bash
just verify                                 # full gate: gofmt + vet + build + unit (-race) + integration (same as CI)
just test                                   # unit tests with the race detector
just test-integration                       # integration tests (builds binary, runs the `integration` tag)
just build                                  # build the server binary
just install-hooks                          # enable the pre-push hook (runs `just verify` before every push)
just release vX.Y.Z                          # cross-build all platforms into dist/ (releases are cut via the manual Release workflow)

# Underlying go commands, for ad-hoc use:
go build -o natural-lsp ./cmd/natural-lsp   # build the binary
go test -run TestName ./internal/analysis/natural   # single test
./natural-lsp --stdio < /dev/null           # smoke test: serves the LSP initialize handshake on empty input, then exits cleanly on EOF
./natural-lsp --init                        # write a fully-commented sample .natural-lsp.toml to stdout
./natural-lsp --version                     # print version and exit
```

## Development workflow

Product features go through a lifecycle, each phase driven by a slash command (defined under
`.claude/`): `/plan-feature` → `/implement-feature` → `/review-feature` ⇄ `/address-findings` →
`/finalize-feature`. Each feature is a directory under `docs/plans/features/<feature>/` holding
`plan.md` (the spec — user stories + acceptance criteria) and `tasks.md` (the planner's decomposition).
To run the whole chain in one go, use **`/ship-feature <feature>`** — it pauses once for plan approval,
then implements, reviews and remediates to a `PASS`, and opens the PR for you to merge.

- **Feature branches, never `main`.** Implement every product feature on a `feat/<feature>` branch off
  `main` — the task plan (`tasks.md`), the code, and the doc updates all live on that branch. Do not
  commit feature code directly to `main`. (Repo-infrastructure changes — `.claude/` tooling, CI, chores
  — are exempt and may go straight to `main`.) A reviewed feature lands via a **pull request that a
  human merges**: `/finalize-feature` opens the PR and stops there. After the PR merges, delete the
  branch and return to `main`.
- **Review is a loop.** If `/review-feature` returns `FAIL` (or `CONCERNS` worth addressing), run
  `/address-findings` — each finding becomes a regression-first fix through the TDD loop — and re-review
  until the verdict is `PASS`. Only a clean `PASS` unlocks `/finalize-feature`.
- **Docs track as-built.** By the time a feature merges, `CLAUDE.md` and `README.md` must already match
  what shipped — the "Project state" note below, the command list, and the architecture/feature set.
  `/finalize-feature` performs that sync before opening the PR, and the `review-docs` reviewer flags
  drift during `/review-feature`. Keep the "Project state" note current as each feature lands.

## Architecture

A single binary (`cmd/natural-lsp`) runs as a stdio LSP server. The intended package boundaries:

- `internal/model/` — the shared output contract (`model.go`: `ObjectType`, `Diagnostic`, `FileAnalysis`); consumed by
  analysis, workspace, and server; free of backend internals.
- `internal/server/` — LSP lifecycle and request dispatch (`textDocument/*`, `workspace/*`), work-done progress.
- `internal/document/` — in-memory document store (didOpen/didChange/didClose) and the workspace file watcher.
- `internal/workspace/` — the cross-file symbol table (`index.go`) and its on-disk cache (`cache.go`).
- `internal/analysis/` — `analyzer.go` defines the **Analyzer interface**; `analysis/natural/` is the parser-based
  implementation (lexer, recursive-descent parser, AST, symbol extraction, hover builders, call/data extraction).

**The Analyzer interface is the key seam.** The parser backend sits behind it so it can evolve (e.g. to a tree-sitter
grammar) without touching the LSP layer. Keep LSP-facing code depending only on the interface, never on parser internals.

## Design decisions that constrain implementation

These are deliberate and easy to get wrong — read the README's "Parser-based extraction" and "Workspace
configuration" sections before changing related code.

- **Hand-written parser, not regex.** A lexer + recursive-descent parser for Natural, using
  [natls](https://github.com/MarkusAmshove/natls) as the reference implementation. This enables accurate symbol tables,
  real syntax diagnostics, completion, signature help, and call hierarchy — features that require a proper AST. Two
  failure modes are still modeled *separately* and neither is dropped silently:
  - *Unresolvable references* (e.g. `CALLNAT #VARIABLE`) are noted as unresolvable with the call site preserved — they
    appear in find-references and outline rather than disappearing.
  - *Parse errors* are surfaced as LSP **diagnostics** so they are visible in the editor, not silently discarded.

- **Module resolution follows Natural's steplib chain, not file paths.** `CALLNAT` / `PERFORM` / `FETCH` targets resolve
  current-library → steplibs (in order) → SYSTEM. The same module name can exist in multiple libraries; search order is
  what disambiguates. Library mapping is config-driven (`[resolution]` in `.natural-lsp.toml`). With no library map,
  fall back to a single flat namespace and emit a diagnostic on ambiguous resolution. Do not assume globally-unique
  names.

- **Filesystem-scoped to NaturalONE / SPoD `.NSx` files.** The server operates on exported object files, not objects
  living only in the mainframe Natural/Adabas library system. Each extension maps to a construct and several features
  depend on indexing the right ones: `.NSP` program, `.NSN` subprogram, `.NSS` external subroutine, `.NSC` copycode
  (INCLUDE targets), `.NSM` map, `.NSL`/`.NSG`/`.NSA` data areas, `.NSH` helproutine, `.NSD` DDM. Extended types:
  `.NS4` class, `.NS7` function, `.NS3` dialog, `.NS8` adapter, `.NST` text. All 15 are in the default indexed set.
  Keep the indexed extension set in sync with the features that consume them.

- **Natural is case-insensitive** for keywords and identifiers — the lexer must normalize case. Statements can span
  multiple lines; the parser must handle continuation correctly.

- **Workspace root** is located by walking up for a `.natural-lsp.toml` sentinel. The index is cached under
  `.natural-lsp-cache/`; invalidate on **content hash** (not mtime, which breaks across git checkouts) and force a full
  rebuild when the cache-format version changes.

## Testing convention

When the analyzer mishandles a construct: add a minimal reproducer `.NSP` (or relevant `.NSx`) under `testdata/`, write
a failing unit test in `internal/analysis/natural/`, then fix the analyzer. The testdata file stays as a permanent
regression fixture. Use only sanitized, non-proprietary Natural code.

Standalone LSP server usable with any LSP editor.