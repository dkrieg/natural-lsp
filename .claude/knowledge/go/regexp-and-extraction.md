# Go `regexp` and the extraction backend

**Status:** verified (2026-06-20) — `regexp`/RE2 facts confirmed against pkg.go.dev/regexp and
pkg.go.dev/regexp/syntax; testing-the-extractor section (native fuzzing, golden files, test doubles)
confirmed against go.dev/security/fuzz, pkg.go.dev/testing, and pkg.go.dev/github.com/google/go-cmp.

This is the highest-value Go topic for this project: the analyzer's extraction backend is
regex-based, so the capabilities and limits of Go's `regexp` package directly shape what is and isn't
extractable.

## Key facts (verified)

- Go's `regexp` implements **RE2** semantics: matching is **guaranteed to run in time linear in the
  size of the input** ("a property not guaranteed by most open source implementations") — no
  catastrophic backtracking. Safe to run over large source files. (pkg.go.dev/regexp)
- **RE2 does NOT support backreferences or lookaround** (no `\1` in the pattern, no `(?=...)`,
  `(?!...)`, `(?<=...)`, `(?<!...)`). Patterns that "depend on what was matched elsewhere" cannot be
  expressed in a single regex — this constrains how Natural constructs can be matched and may force
  multi-step extraction. *(This is the single most important fact for the analyzer design.)*
- Flags via `(?flags)` / `(?flags:re)`, all non-capturing groups:
  - `i` — case-insensitive (`FoldCase`). **Relevant: Natural is case-insensitive.** Default off.
  - `m` — multi-line (`regexp/syntax` constant is `Multiline`): `^` and `$` match at the begin/end of
    *each line* (after/before `\n`) in addition to begin/end of text. Default off (so by default `^`
    and `$` match only the absolute start/end of the whole input).
  - `s` — `.` matches `\n` too (`DotNL`). Default off (default `.` matches any char except newline).
  - `U` — ungreedy (`NonGreedy`): swaps greedy/non-greedy meaning of `x*` vs `x*?`, etc. Default off.
  - Multi-line Natural statements likely need `s` and/or a line-joining pre-pass — but see the
    resolved note below: a logical-line pre-pass is the recommended approach.
- **Counted repetition is capped at 1000**: `x{n}`, `x{n,}`, `x{n,m}` reject any minimum or maximum
  count above 1000 (a compile-time restriction). Unbounded `*`/`+` are not affected. (regexp/syntax)
- Compile patterns once with `regexp.MustCompile` at package init, not per call. `regexp.Regexp` is
  **safe for concurrent use by multiple goroutines, except for configuration methods** such as
  `Regexp.Longest` — call any such configurator once at init, before sharing the value.
- `Longest()` switches to leftmost-**longest** semantics (POSIX-style) instead of the default
  leftmost-first. It is a configuration method and must not be called concurrently with matching.
- Useful APIs: `FindAllStringSubmatchIndex` (byte-offset positions for LSP ranges),
  `FindAllStringSubmatch` (captured group text), named groups via `(?P<name>...)` + `SubexpNames()`
  (returns `["", "first", "last", ...]`; index `i` of a match slice maps to `SubexpNames()[i]`).

## Implications for the analyzer

- Because there are no backreferences/lookahead, "unrecognized statement-like line" detection and
  multi-line statement handling must be built deliberately (matches the PRD's design note that an
  unmatched regex is a silent no-op unless explicitly flagged).
- Prefer index-returning APIs so extracted symbols/edges carry exact source ranges for LSP.
- **Multi-line statements (resolved open question):** prefer a **logical-line pre-pass** that
  assembles Natural's continued statements into a single logical line before matching, rather than
  relying solely on the `s` flag. Rationale: with RE2 (no lookaround), a single pattern cannot
  reliably bound a multi-line construct without over- or under-matching, and matching against
  pre-joined logical lines keeps each pattern simple and keeps byte offsets mappable back to the
  original source. The pre-pass owns the source-position bookkeeping; the regexes stay line-oriented.

## Minimal example (intent, not committed code)

A case-insensitive pattern for a literal `CALLNAT 'NAME'` would use the `i` flag and a named capture
for the target; positions come from the `...Index` variants. (Confirm exact pattern needs against
real fixtures from the Natural KB.)

## Testing the extraction backend

The extractor is the natural target for both native fuzzing (does any byte sequence make it panic or
loop?) and golden-file tests (does a fixture still extract the same symbols/edges?). These are pure
Go testing mechanics; the repo's `testdata/` fixture convention layers on top of them.

### Native fuzzing (verified — Go 1.18+)

`go test` has built-in fuzzing. Write a fuzz target in a `*_test.go` file:

```go
func FuzzExtract(f *testing.F) {
    f.Add([]byte("CALLNAT 'SUB1'\n"))           // seed corpus entry; types must match the Fuzz args
    f.Fuzz(func(t *testing.T, src []byte) {       // first param *testing.T, then the fuzzed args
        _ = analyzer.Extract(src)                 // assert it never panics / hangs / corrupts state
    })
}
```

- Signature: `func FuzzXxx(f *testing.F)`, no return. `f.Add(args ...any)` seeds the corpus; seed arg
  types must exactly match the `f.Fuzz` target's fuzzed parameters, in order.
- `f.Fuzz(ff any)` takes a function whose first parameter is `*testing.T` followed by the fuzzed
  args. Allowed fuzzed types: `[]byte`, `string`, the int/uint families, `float32/64`, `bool`,
  `rune`/`byte`. (For the extractor, fuzz `[]byte` or `string` source.)
- Run as a normal unit test (replays the seed + regression corpus) with `go test`; run the actual
  fuzzing engine with `go test -fuzz=FuzzExtract` (optionally `-fuzztime=30s`). Only one fuzz target
  per `FuzzXxx`.
- **On-disk corpus layout:** seed/regression entries live committed in
  `testdata/fuzz/{FuzzName}/{hash}` (one file per input; each begins `go test fuzz v1`). The engine's
  generated/mutated entries live in `$GOCACHE/fuzz` (not committed). When fuzzing finds a failure it
  writes the minimized input to `testdata/fuzz/{FuzzName}/{hash}`, which then runs as a permanent
  regression case under plain `go test` — this dovetails with the repo's "fixture is a permanent
  regression guard" rule.
- Because RE2 guarantees linear-time matching (above), the regex layer itself won't catastrophically
  backtrack, but fuzzing still guards the surrounding logical-line pre-pass, offset bookkeeping, and
  panic-safety of the extraction pipeline.

### Golden-file tests (verified)

For "extract this fixture → expect this structured output," compare against a committed golden file
and gate regeneration on a flag. The conventional idiom is a package-level `-update` flag:

```go
var update = flag.Bool("update", false, "update golden files")

func TestExtractGolden(t *testing.T) {
    got := mustSerialize(t, analyzer.Extract(readFixture(t, "callnat.NSP")))
    golden := filepath.Join("testdata", "callnat.golden")
    if *update {
        os.WriteFile(golden, got, 0o644)
    }
    want, _ := os.ReadFile(golden)
    if diff := cmp.Diff(string(want), string(got)); diff != "" {
        t.Errorf("extract mismatch (-want +got):\n%s", diff)
    }
}
```

Run `go test -run TestExtractGolden ./... -update` to regenerate, then review the diff in git.

- `flag.Bool("update", ...)` is a **community convention**, not part of the `testing` package — the
  testing godoc does not define `-update`. Custom flags work because `go test` calls `flag.Parse()`;
  if you use `TestMain`, you must call `flag.Parse()` yourself before reading the flag.
- Compare with **`github.com/google/go-cmp/cmp`** (`cmp.Diff(want, got)` returns a readable, empty-on-
  equal diff; convention is `(-want +got)`). go-cmp is at **v0.7.0 (2025-01-14)**, BSD-3-Clause, and
  is explicitly **test-only** — its own docs warn it "is intended to only be used in tests" and "may
  panic," so never use it on a production hot path. Use it in tests; use plain comparisons/`reflect`
  guards in non-test code.
- **Determinism — sort before serialize.** Go map iteration order is randomized by design, so any
  golden output derived from a map (symbol tables, edge sets) must be made deterministic or the test
  flakes. Either sort the keys before serializing (e.g. collect to a slice and `slices.Sort`), or, if
  comparing live values rather than serialized bytes, use `cmpopts.SortMaps` / `cmpopts.SortSlices`
  to normalize order inside `cmp.Diff`. This applies to slice-of-edge output too whenever extraction
  order isn't guaranteed stable.

### Test doubles: hand-written fake vs. generated mock

This applies beyond the extractor — most directly to the **`analysis.Analyzer` seam**, which
`internal/server` consumes. It is a pure Go testing-mechanics question (the `go-development`
best-practices reference implies it; stated explicitly here).

- A **fake** is a real (if simplified) implementation of an interface — e.g. an in-memory
  `analysis.Analyzer` that returns canned symbols/edges for fixed inputs. A **mock** records calls and
  asserts they happened in a prescribed order/with prescribed args, usually code-generated
  (mockgen/gomock, moq).
- **Prefer a hand-written fake when the interface is small and consumer-defined** — which is exactly
  the idiomatic Go shape: the consumer (`internal/server`) declares the narrow `Analyzer` interface it
  needs, and a few-line fake in the test package satisfies it. A fake is readable, has no codegen step
  or extra dependency, doesn't go stale when the interface changes (the compiler catches it), and
  tests *behavior/state* (what the server does with the results) rather than *interaction* (which
  exact methods were called). For "given this Analyzer output, the server produces these LSP
  responses," a fake is clearly better.
- **Reach for a generated mock only when** the interface is large, you genuinely need to assert *call
  sequencing/argument expectations* (e.g. a protocol where ordering is the contract), or hand-writing
  the double would be substantial boilerplate across many methods. Mocks couple tests to call
  structure, which makes refactors brittle — a real cost for a still-evolving extractor.
- Practical rule for this repo: keep interfaces narrow at the consumer (per the Analyzer-seam
  guardrail) and use hand-written fakes in `_test.go`; treat generated mocks as the exception, added
  with justification, not the default. The standard library is the default — no mock framework unless
  a specific test need justifies it.

## Sources

- https://pkg.go.dev/regexp (verified 2026-06-20: linear-time guarantee, concurrency safety,
  `Longest`, named-capture/`SubexpNames`, `FindAll*SubmatchIndex`)
- https://pkg.go.dev/regexp/syntax (verified 2026-06-20: no backreferences/lookaround; `i`/`m`/`s`/`U`
  flag semantics; 1000-count repetition cap)
- RE2 syntax reference: https://github.com/google/re2/wiki/Syntax (corroborating)
- Native fuzzing (verified 2026-06-20): https://go.dev/security/fuzz/ (FuzzXxx signature, `f.Add`,
  `f.Fuzz`, `go test -fuzz`, `testdata/fuzz/{FuzzName}/{hash}` layout, `$GOCACHE/fuzz`, Go 1.18+) and
  https://pkg.go.dev/testing#F (`F.Add(args ...any)`, `F.Fuzz(ff any)` signatures)
- Golden files / go-cmp (verified 2026-06-20): https://pkg.go.dev/github.com/google/go-cmp/cmp
  (`cmp.Diff`, test-only / may panic) and https://proxy.golang.org/github.com/google/go-cmp/@latest
  → v0.7.0 (2025-01-14); https://pkg.go.dev/github.com/google/go-cmp/cmp/cmpopts (`SortMaps`,
  `SortSlices` for deterministic comparison); https://pkg.go.dev/testing (custom flags via
  `flag.Parse` in `TestMain`; `-update` is a community convention, not a testing-package feature)