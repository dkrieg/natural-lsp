# Engineering principles

**Status:** verified (2026-06-20) — the named principles below are grounded in recognized primary
sources (cited under Sources): Martin's SOLID, the Go proverbs / Effective Go for the Go lens, and the
project's own NFRs. Apply them *through the lens of idiomatic Go* (the `go-development` skill), which
deliberately differs from OO-centric phrasings (e.g. Go favors small consumer-defined interfaces and
composition, and "the bigger the interface, the weaker the abstraction" — Go proverb).

## Design

- **SOLID**, read pragmatically for Go: single responsibility per package/type; small,
  consumer-defined interfaces (interface segregation + dependency inversion via accepting
  interfaces); composition over inheritance. Don't force class-oriented patterns onto Go.
- **DRY / YAGNI / KISS**: remove duplication, but don't abstract speculatively. Add a pattern only to
  solve a real, present problem; record why (ADR).
- **Separation of concerns**: explicit, documented boundaries — here, the Analyzer seam and the
  extraction↔resolution split (see `architecture-decisions.md`).
- **Secure-by-design**: the server reads untrusted source files; treat input defensively (size
  limits, no panics on malformed input, no execution of file contents).

## Quality gates (before "done")

- Readable (low cognitive load), maintainable (comments explain *why*), testable (mockable seams),
  correct error paths (graceful degradation — one bad file never crashes the server, FR-43), and
  race-free for concurrent code.
- **Enforce the gates mechanically, not by habit.** `gofmt`, `go vet ./...`, the unit suite, and
  `go test -race ./...` for concurrent code are **CI gates** — green-or-block, with real output, never
  assumed. `-race` in particular only catches a race if the racy path is actually exercised under it,
  so concurrent tests must run *under* `-race` in CI, not merely exist. (The race detector being "the
  bar" is a Go fact in the Go KB / skill; treating it as an enforced gate is the engineering stance.)

## Process

- **Handle errors once**; wrap with context; reserve `error` for genuine failures (modeled gaps and
  unrecognized syntax are NOT errors — see Go KB `error`-handling guidance and the Natural KB).
- **Smallest correct change**; match surrounding style; the standard library is the default
  dependency.
- **Review discipline**: cite `file:line`, prefer evidence (test output) over assertion, separate
  correctness findings from style/cleanup.

## Sources
- **SOLID**: Robert C. Martin, "Design Principles and Design Patterns" (2000) and the consolidated
  treatment in *Clean Architecture* (2017); origin summary: https://en.wikipedia.org/wiki/SOLID
- **Go lens** (interfaces, composition, error handling, simplicity): Go proverbs
  (https://go-proverbs.github.io/) and Effective Go (https://go.dev/doc/effective_go). Defer to the
  `go-development` skill for the binding Go craft.
- **Internal (authoritative)**: project PRD non-functional requirements (NFR-6, NFR-7) and FR-43
  (graceful degradation — one bad file never crashes the server).
- KISS/YAGNI/DRY are widely-attributed industry heuristics (DRY: Hunt & Thomas, *The Pragmatic
  Programmer*); applied here only to solve a present problem, with the rationale recorded as an ADR.