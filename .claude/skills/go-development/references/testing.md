# Go testing & the testdata fixture convention (natural-lsp)

How tests are written in this project. Read `CLAUDE.md` for the testing convention first.

## Table-driven tests (default)

- Use table-driven tests with a slice of named cases; iterate and run each via `t.Run(tc.name, ...)`
  so failures identify the case.
- Name cases descriptively (the scenario, not "case1"). One behavior per case.
- Keep cases independent and order-independent. No shared mutable state between cases.
- Use `t.Helper()` in assertion helpers so failures point at the call site.
- Prefer standard-library testing; reach for a third-party assertion library only if the repo already
  uses one. Compare with `reflect.DeepEqual` or `cmp.Diff`-style helpers for structs; print expected
  vs. got clearly.

## The testdata fixture convention (project rule)

This is how the analyzer is tested and must be followed:

1. When the analyzer mishandles a Natural construct, add a **minimal** reproducer `.NSP` (or the
   relevant `.NSx`) under `testdata/`.
2. Write a **failing** unit test in the appropriate `internal/analysis/natural/*_test.go` asserting
   the expected extraction.
3. Fix the analyzer until the test passes.
4. The fixture stays as a **permanent regression fixture** — never delete it to make a test pass.
- Use only **sanitized, non-proprietary** Natural code in fixtures.
- Fixtures live in `testdata/` (unit) and `testdata/workspace/` (cross-file/integration). Load them
  with relative paths; `go test` runs with the package directory as the working dir.

## What to assert for analyzer/extraction code

- Extraction is exact: the right symbols, edges (`CALLS`, `CALLS_DYNAMIC`, `NAVIGATES_TO`,
  `PERFORMS`, `INCLUDES`, reads/writes), and source ranges — not just "no error."
- Cover the modeled-gap cases explicitly: dynamic/unresolved references are produced as such;
  unrecognized statement-like lines surface as diagnostics. Neither is silently dropped.
- For resolution, include multi-library fixtures proving steplib search order changes the result and
  that inline subroutines win over same-named external ones.
- Case-insensitivity: assert that identifiers/keywords match regardless of letter case.

## Concurrency & integration

- For concurrent code (indexer, watcher, request handling), run with `-race`.
- Integration tests that need the built binary use the `integration` build tag: `go test -tags
  integration ./...` (build the binary first).

## Commands

```bash
go test ./...                                       # unit tests
go test -run TestName ./internal/analysis/natural   # a single test
go test -race ./...                                 # race detection
```