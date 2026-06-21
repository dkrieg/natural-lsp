# Go error handling & graceful degradation (natural-lsp)

The product's reliability bar is "no silent data loss" (PRD NFR-6) and "one bad file never crashes
the server" (FR-43). Error handling is how those are achieved.

## Wrapping & context

- Wrap errors with `fmt.Errorf("...: %w", err)` to add context while preserving the chain. Add what
  the caller can't already know (which file, which object, which operation).
- Don't wrap with `%w` if callers should not depend on the wrapped type; use `%v` to deliberately
  flatten.
- Inspect with `errors.Is` (sentinel comparison) and `errors.As` (type extraction) — never compare
  error strings.
- Define sentinel errors (`var ErrNotFound = errors.New("...")`) or typed errors for conditions
  callers branch on; keep them in the package that owns the condition.

## Where errors go in this architecture

- **Per-file analysis failures** are expected on legacy/messy code. They must be *recoverable*: skip
  the file, record the failure observably (log and/or diagnostic), and continue indexing — never
  abort the run or panic the process (FR-43).
- **Distinguish the two modeled outcomes** (do not conflate with Go errors):
  - An *unresolvable reference* (dynamic call, missing target) is a normal modeled result on the
    analysis output, **not** an `error`.
  - An *unrecognized statement-like line* is a **diagnostic**, **not** an `error`.
  - Reserve Go `error` values for genuine failures (I/O, malformed input the analyzer can't proceed
    past, invalid config).
- **Config errors** should be clear and actionable, and fall back to defaults where a safe default
  exists rather than refusing to start (PRD CR-6).

## Panics

- Do not use panic for ordinary control flow or expected bad input.
- If a parser path can panic on pathological input, `recover()` at the per-file boundary, convert it
  to a recoverable error/diagnostic, and keep the server alive. Log enough to reproduce.
- A `recover()` must be in a deferred function in the same goroutine as the panic — ensure worker
  goroutines protect their own boundary.

## Style

- Handle errors once: either handle it or return it, not both (don't log-and-return the same error at
  every level).
- Check and handle errors immediately; keep the happy path un-indented.
- Don't discard errors with `_` unless deliberate and commented.
- Make error messages lowercase, no trailing punctuation, no "failed to" noise where the chain
  already conveys it.