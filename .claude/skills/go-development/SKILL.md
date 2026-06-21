---
name: go-development
description: >-
  Idiomatic, production-grade Go for the natural-lsp codebase. Use when writing, reviewing, testing,
  or documenting Go in this repo — package design and conventions, table-driven testing with the
  testdata regression-fixture rule, godoc documentation, safe concurrency (the LSP server, background
  indexer, and file watcher), and error handling with graceful degradation. Covers Go 1.26 and the
  internal/{config,server,document,workspace,model,analysis} layout.
---

# Go development for natural-lsp

Keep this codebase idiomatic, correct, and maintainable. This skill bundles the project's Go
conventions; read the focused reference for the task at hand rather than loading everything.

## Always (regardless of task)

- Read `CLAUDE.md` at the repo root first for project context (Go 1.26, module `natural-lsp`, the
  `internal/...` layout, the **Analyzer interface** seam, steplib resolution, the `testdata/`
  fixture convention, graceful degradation). Stay consistent with `README.md` Architecture and the
  feature plans under `docs/plans/`.
- **Guard the Analyzer seam.** LSP-facing code (`internal/server`) depends only on
  `internal/analysis.Analyzer` and `internal/model` — never on the regex backend in
  `analysis/natural`. Flag any import that violates this.
- Match the surrounding file's style, naming, and comment density. The standard library is the
  default; justify any new dependency.
- **Verify before claiming done:** run `go build ./...`, `go vet ./...`, and the relevant `go test`
  target (with `-race` for concurrent code). Report real output — never assert a build passes
  without running it.

## Pick the reference for your task

| Task | Reference |
|------|-----------|
| Writing/reviewing general Go; package & interface design; naming; resources | [references/best-practices.md](references/best-practices.md) |
| Writing/improving tests; the `testdata` fixture convention; what to assert | [references/testing.md](references/testing.md) |
| Doc comments / godoc / pkg.go.dev standards | [references/docs.md](references/docs.md) |
| Goroutines, channels, context, shared state (server/indexer/watcher) | [references/concurrency.md](references/concurrency.md) |
| Error wrapping, sentinels, panics, graceful degradation | [references/error-handling.md](references/error-handling.md) |

For deeper or cross-cutting Go review and design work, the `go-expert` agent applies all of these.

## Deliverable

Report findings as a concise list with `file:line` references and the smallest correct fix for each;
if you change code, verify as above and report the real output.