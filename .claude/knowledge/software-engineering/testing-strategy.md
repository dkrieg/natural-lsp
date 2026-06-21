# Testing strategy

**Status:** verified (2026-06-20) against CLAUDE.md and docs/plans (internal, authoritative). Go
mechanics live in the `go-development` skill's testing reference; this topic is the *strategy*.

## The pyramid (weighted to fast unit tests)

```
E2E / full-server (few)  →  integration at boundaries (focused)  →  unit (many, fast, isolated)
```

- **Unit**: per-construct extraction and resolution in `internal/analysis/natural` and
  `internal/workspace`. Table-driven, fast, no I/O where avoidable (use `fstest.MapFS`).
- **Integration**: cross-file resolution over a multi-object workspace; gated behind the
  `integration` build tag and requiring the built binary. Exercises the LSP request/response path.
- **E2E**: a small number of full initialize→request→response journeys against a sample workspace.

## Interface-seam testing: fake the Analyzer (architecture-driven)

ADR-002 makes `internal/analysis.Analyzer` the replaceable-backend seam; that seam is also the
project's primary **testing seam**. Exploit it:

- **Test `internal/server` against a fake/in-memory `Analyzer`**, not the real regex backend. A fake
  returning canned `internal/model` values lets server tests assert protocol behavior (capability
  advertisement, range mapping, request→response shape, error codes, cancellation) deterministically
  and without `.NSx` fixtures or I/O. This is *why* the seam earns its keep beyond backend
  swappability.
- **Test the regex backend (`analysis/natural`) directly** against `testdata/` fixtures (below) —
  that is where extraction correctness lives.
- Keep the fake in the consumer's test package; do not add a production "mock" type. The interface is
  small by design (Go proverb — accept interfaces), so a hand-written fake is cheaper than a
  generated mock. (Go mechanics: `go-development` skill testing reference.)

## Golden-file testing for extraction output (with a determinism contract)

For the structured extraction result of a whole object (symbols + edges + ranges), prefer a
**golden file** over many hand-written assertions: serialize the analyzer output deterministically
and compare against a committed `.golden`, regenerated behind a `-update` flag.

- **Determinism is a hard contract, not a convenience.** Golden output — and the on-disk cache
  (ADR-005/011) and any structure fed to the lsp-graph consumer — must be byte-stable across runs.
  That means: **sort** all collections before emitting (never rely on Go map iteration order, which
  is randomized), use stable IDs, and pin the serialization. A flaky golden test almost always means
  nondeterministic ordering leaked out of the analyzer, which is itself a bug for the cache/graph
  consumers. (Go mechanics of golden tests / `-update` live in the Go KB / skill.)
- Use golden files for *aggregate* output; keep targeted table-driven assertions for single
  constructs and edge kinds, where an exact assert reads better than a diff.

## Fuzzing the extractor (graceful-degradation guard)

The extractor parses **untrusted source files** and FR-43 requires that no single file ever crashes
the server (secure-by-design, `engineering-principles.md`). A Go native fuzz target
(`FuzzXxx(*testing.F)`, Go 1.18+) over the extraction entry point is the strategic test that proves
the "never panic on any input" invariant against inputs no hand-written fixture would think to try.

- The property under test is **liveness/safety, not correctness**: the assertion is "extraction
  returns (possibly with a recovered error/diagnostic) and does not panic," not "extraction is
  correct" — fuzz inputs have no known-good output.
- **The fuzz corpus dovetails with the testdata convention.** When fuzzing finds a crasher, Go
  minimizes it and commits it under `testdata/fuzz/FuzzXxx/`, where it is replayed by plain
  `go test` thereafter — i.e. it becomes a permanent regression fixture by the same rule as a
  hand-authored `.NSx` reproducer. Seed the corpus with `f.Add` from representative sanitized
  fixtures. (Verified: go.dev/security/fuzz — native fuzzing since Go 1.18, corpus committed as
  regression seed.)

## The testdata regression-fixture convention (project rule)

When the analyzer mishandles a construct: add a **minimal sanitized** `.NSx` reproducer under
`testdata/`, write a **failing** test, then fix the analyzer. The fixture stays as a **permanent**
regression guard — never deleted to make a test pass. Use only non-proprietary Natural code. (Public
sources suitable for deriving fixtures are catalogued in the Natural KB `example-projects.md`:
licensing matters — only MIT/Apache sources may be copied.)

## What "done" means for a work item

- Every acceptance criterion in the relevant feature plan is demonstrated by a test or observable
  behavior (PRD metrics M-3/M-4/M-6 in particular: correct resolution, inline-before-external, no
  silent gaps).
- `go build ./...`, `go vet ./...`, the unit suite, and `-race` for concurrent code are green — with
  real output, not assumed.

## Known fixture gaps to hand-author (from the Natural KB)

- **Reporting mode** (public code is almost all structured mode).
- **Mainframe fixed-column** source (public samples are NaturalONE free-format).
- **Dynamic calls** (`CALLNAT #VAR`, `&`/`*LANGUAGE` substitution) — rare in the wild; synthesize.

## Sources
- Internal: `CLAUDE.md`, `docs/plans/features/` (esp. plans 05, 06, 13), Natural KB
  `example-projects.md`; ADR-002 (Analyzer seam), ADR-013 (fuzz the extractor) in
  `architecture-decisions.md`.
- Go native fuzzing — corpus under `testdata/fuzz/...` committed as a regression seed, native since
  Go 1.18: https://go.dev/security/fuzz/ (verified 2026-06-20). Go *mechanics* of fuzz/golden/fake
  tests are owned by the `go-development` skill testing reference and the Go KB.