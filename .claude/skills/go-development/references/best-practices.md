# Idiomatic Go conventions (natural-lsp)

Standing conventions for new and changed Go code. Read `CLAUDE.md` for project context first.

## Project layout & packages

- Keep package boundaries as designed: `cmd/natural-lsp` (entrypoint only), `internal/{config,
  server, document, workspace, model, analysis, analysis/natural}`. Business logic lives under
  `internal/`, not in `main`.
- LSP-facing code depends only on the `analysis.Analyzer` interface and `internal/model` — never on
  the regex backend in `analysis/natural`. The interface is the replaceable-backend seam; do not let
  backend types leak across it.
- Package names are short, lowercase, no underscores, and not stuttering (`workspace.Index`, not
  `workspace.WorkspaceIndex`). One concept per package.

## Idioms

- Make the zero value useful where practical; avoid mandatory constructors when a struct can be used
  as-is. Use constructors (`New...`) only when invariants must be established.
- Accept interfaces, return concrete types. Define interfaces in the consumer package, kept small.
- Composition over inheritance; embed sparingly and deliberately.
- Use `context.Context` as the first parameter for any operation that can block, do I/O, or be
  cancelled (indexing, file reads, request handling). Never store a context in a struct.
- Prefer slices/maps and standard-library types over custom containers. Pre-size with `make` when the
  length is known.
- Keep exported surface minimal; unexport anything that doesn't need to be public.

## Correctness & resources

- Always handle errors; never discard with `_` unless deliberate and commented. Wrap with `%w` and
  context (see error-handling reference).
- Close everything you open (`defer f.Close()`); ensure goroutines have a clear exit path and don't
  leak (see concurrency reference).
- Guard shared state; design for the race detector (`go test -race`).
- A single bad input file must never panic the process — recover at the right boundary and degrade
  gracefully (PRD FR-43).

## Style & tooling

- Code must pass `gofmt`/`go vet` cleanly. Run `go build ./...` and `go vet ./...` after changes.
- Names: `MixedCaps`, acronyms stay uppercase (`LSP`, `DDM`, `URI`, `ID`). Receiver names short and
  consistent per type.
- Match the surrounding file's style and comment density. Comment the *why*, not the *what*.
- Don't add dependencies casually; the standard library is the default. Justify any new module.