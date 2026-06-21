# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

**Early implementation — feature 01 (workspace & configuration) shipped; remaining features are stubs.**
`internal/config` is fully implemented: workspace-root discovery (`.natural-lsp.toml` sentinel walk-up),
config loading with decode-onto-defaults semantics, per-field validation with CR-6 fail-safe (bad value →
default + actionable `Problem`, never crash), directory-exclusion predicate (`IsExcluded`), skip-reason
surface (`SkipReason`), library-map parsing (declared order preserved), analysis-options parsing, and a
`Sample()` generator for `--init`. All other `internal/` packages (`server`, `document`, `workspace`,
`analysis`) remain documented stubs. The README describes the full *target* architecture and feature set;
update the "Project state" note here as each feature lands.

`natural-lsp` is the first open-source Language Server Protocol implementation for **Software AG Natural**, a 4GL widely
deployed on IBM z/OS mainframes. It indexes a Natural codebase and serves navigation, references, hover, document
outline, and workspace symbols to any LSP-capable editor.

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
./natural-lsp --stdio < /dev/null           # smoke test: resolves workspace root, loads config, then exits (LSP serving not yet implemented)
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
  drift during `/review-feature`. Update the "Project state" note as real source lands (it currently
  says pre-implementation).

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