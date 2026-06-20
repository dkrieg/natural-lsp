# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

**Pre-implementation / design stage.** As of this writing the repository contains no Go source — only `README.md`
(the design spec and source of truth), `go.mod`, and `LICENSE`. The README describes the *target* architecture and
feature set, not shipped behavior. When building, treat the README's "Architecture" section as the intended package
layout and keep new code consistent with it.

`natural-lsp` is the first open-source Language Server Protocol implementation for **Software AG Natural**, a 4GL widely
deployed on IBM z/OS mainframes. It indexes a Natural codebase and serves navigation, references, hover, document
outline, and workspace symbols to any LSP-capable editor.

## Commands

Module is `natural-lsp` (`go.mod`), targeting Go 1.26. Note the README's `go install` path uses
`github.com/dkrieg/natural-lsp/cmd/natural-lsp` — reconcile the module path before publishing.

```bash
go build -o natural-lsp ./cmd/natural-lsp   # build the binary
go test ./...                               # unit tests
go test -run TestName ./internal/analysis/natural   # single test
go build -o natural-lsp ./cmd/natural-lsp && go test -tags integration ./...   # integration tests (need built binary)
make release                                # release binaries
./natural-lsp --stdio < /dev/null           # smoke test: prints initialize response shape
```

## Architecture

A single binary (`cmd/natural-lsp`) runs as a stdio LSP server. The intended package boundaries:

- `internal/server/` — LSP lifecycle and request dispatch (`textDocument/*`, `workspace/*`), work-done progress.
- `internal/document/` — in-memory document store (didOpen/didChange/didClose) and the workspace file watcher.
- `internal/workspace/` — the cross-file symbol table (`index.go`) and its on-disk cache (`cache.go`).
- `internal/analysis/` — `analyzer.go` defines the **Analyzer interface**; `analysis/natural/` is the regex-based
  implementation (extraction pipeline, symbol mapping, hover builders, call/data extraction).

**The Analyzer interface is the key seam.** The extraction backend (currently regex) sits behind it so it can later be
swapped for a hand-written parser or tree-sitter grammar without touching the LSP layer. Keep LSP-facing code depending
only on the interface, never on regex internals.

## Design decisions that constrain implementation

These are deliberate and easy to get wrong — read the README's "Why regex-based extraction" and "Workspace
configuration" sections before changing related code.

- **Regex extraction, not a grammar.** Chosen for fast usable coverage of production patterns over slow complete
  coverage. Two failure modes are modeled *separately* and neither is dropped silently:
  - *Unresolvable references* (e.g. `CALLNAT #VARIABLE`) are a modeled outcome → `CALLS_DYNAMIC` edges with caller
    context preserved.
  - *Unrecognized syntax* (a statement-like line matching no pattern) is a parser limitation → surface it as an LSP
    **diagnostic**. An unmatched regex is a silent no-op unless the analyzer explicitly flags it, so this flagging must
    be built on purpose.

- **Module resolution follows Natural's steplib chain, not file paths.** `CALLNAT` / `PERFORM` / `FETCH` targets resolve
  current-library → steplibs (in order) → SYSTEM. The same module name can exist in multiple libraries; search order is
  what disambiguates. Library mapping is config-driven (`[resolution]` in `.natural-lsp.toml`). With no library map,
  fall back to a single flat namespace and emit a diagnostic on ambiguous resolution. Do not assume globally-unique
  names.

- **Filesystem-scoped to NaturalONE / SPoD `.NSx` files.** The server operates on exported object files, not objects
  living only in the mainframe Natural/Adabas library system. Each extension maps to a construct and several features
  depend on indexing the right ones: `.NSP` program, `.NSN` subprogram, `.NSS` external subroutine, `.NSC` copycode
  (INCLUDE targets), `.NSM` map, `.NSL`/`.NSG`/`.NSA` data areas, `.NSH` helproutine, `.NSD` DDM. Keep the indexed
  extension set in sync with the features that consume them.

- **Natural is case-insensitive** for keywords and identifiers — extraction and cross-file resolution must normalize
  case. Statements can span multiple lines, which stresses line-oriented regex.

- **Workspace root** is located by walking up for a `.natural-lsp.toml` sentinel. The index is cached under
  `.natural-lsp-cache/`; invalidate on **content hash** (not mtime, which breaks across git checkouts) and force a full
  rebuild when the cache-format version changes.

## Testing convention

When the analyzer mishandles a construct: add a minimal reproducer `.NSP` (or relevant `.NSx`) under `testdata/`, write
a failing unit test in `internal/analysis/natural/`, then fix the analyzer. The testdata file stays as a permanent
regression fixture. Use only sanitized, non-proprietary Natural code.

## Relation to lsp-graph

Standalone LSP server usable with any LSP editor. Also designed to feed `lsp-graph`, a multi-language workspace-graph
coordinator — keep extracted structure (calls, data access, external deps) clean enough to be consumed by an external
graph builder. Batch export was considered and explicitly dropped from scope; do not reintroduce it.