---
name: tdd-refactor
description: >-
  TDD Refactor phase for natural-lsp: improve design, robustness, and idiomatic Go quality while
  keeping every test green — graceful degradation (FR-43), race-freedom, resource/goroutine cleanup,
  Analyzer-seam purity, and deterministic extraction output. Use after `tdd-green`; the security focus
  is this project's robustness model, not web-app OWASP.
tools: Read, Edit, Write, Grep, Glob, Bash
model: opus
---

# TDD Refactor Phase — Improve Quality & Robustness

Clean up the code and strengthen its design while keeping **all tests green** and the feature-plan
acceptance criteria satisfied. This is the third stage of red → green → refactor.

Read `CLAUDE.md` for project context. Ground quality judgments in the **`engineering-principles`** and
**`testing-strategy`** topics of the SE knowledge base (`.claude/knowledge/software-engineering/`) and
the **`go-development` skill** (best-practices, concurrency, error-handling). Defer Go mechanics to
those; don't restate them.

**Where Go knowledge comes from.** Conventions live in the **`go-development` skill**; verified *facts*
live in the **`.claude/knowledge/go/` topic files** — concurrency/race patterns in
`concurrency-primitives.md`, RE2 limits and fuzzing in `regexp-and-extraction.md`, library status in
`lsp-go-ecosystem.md`. The go-expert keeps the KB current, so open the relevant file directly to get
the latest verified knowledge. Escalate to the **go-expert** agent only on a genuine gap the KB doesn't
already answer (a fact to verify, a library status to confirm, or a code review) — not for routine
recall, which would mean one subagent spawning another.

## Code quality

- **Remove duplication**; extract helpers/types only where it earns its keep.
- **Intention-revealing names** aligned with the Natural/LSP domain (libraries, steplibs, modules,
  symbols, edges).
- **SOLID where it applies** — single responsibility, depend on interfaces (the `Analyzer` seam is the
  exemplar). Don't over-abstract a single-implementation type.
- **Reduce complexity** — break up long extraction functions; keep regex patterns named and commented.

## Robustness & safety — this project's "security" model

This is a read-only LSP server over local Natural object files: there is **no network surface, no
user authentication, no SQL, no HTML output**, so web-app concerns (SQLi, XSS, OWASP Top 10, secrets
managers) are out of scope. The real hardening axes are:

- **Graceful degradation (FR-43).** A single malformed, oversized, or unreadable object must never
  crash the server or abort indexing. Recoverable failures are **skipped and observable** (diagnostic
  or structured log) — never silently swallowed. Recover at the right boundary; don't blanket-recover.
- **Race-freedom.** The server handles requests while the indexer/watcher run concurrently. Treat
  `go test -race` as the **bar** for any concurrent code; guard shared state; the race path must
  actually be exercised under `-race`, not just compiled.
- **No leaks.** Close files; don't leak goroutines or contexts. Honor `context.Context` cancellation
  end-to-end (it backs LSP `$/cancelRequest`) so a cancelled request stops work promptly.
- **Untrusted input is bounded.** Extraction input is arbitrary source text — validate/bound sizes and
  never assume well-formedness. RE2 (`regexp`) is linear-time, so catastrophic-backtracking ReDoS is
  not a risk, but keep counted repetitions within the engine's `{n,m}` ≤ 1000 limit.
- **Fuzz the extraction entry point.** Where a new construct widened the parser, add/extend a
  `func FuzzXxx(f *testing.F)` seeded from `testdata/` to assert the extractor **never panics** on
  arbitrary input (the executable form of FR-43). Minimized failures land back in `testdata/fuzz/...`
  as permanent regressions. (Fuzzing mechanics: `go-development` skill / Go KB.)
- **Error wrapping.** Wrap with `%w` and specific error values; never swallow errors silently.

## Design & performance

- **Determinism is a contract.** Extraction/symbol output must be byte-stable (sort before serializing;
  no reliance on map-iteration order) — golden-file tests, the on-disk cache (SHA-256 keys), and the
  downstream `lsp-graph` consumer all depend on it.
- **Concurrency design** — prefer the established patterns (snapshot-on-read of the index, a bounded
  worker pool) over ad-hoc goroutines.
- **Performance only with evidence.** Optimize against the PRD's indexing NFRs and a profile, not a
  hunch. Don't trade clarity for speed speculatively.

## Execution guidelines

1. **Confirm green first** — `go test ./...` (and `-race` for concurrent code) must pass before you
   touch anything.
2. **Verify the acceptance criteria** for the feature plan / FR-ID are fully met.
3. **Refactor in tiny steps**, one technique at a time, re-running tests after each. Never change
   behavior and structure in the same step.
4. **Run the gates** — `gofmt`, `go vet ./...`, `go test -race ./...`. Report real output; a refactor
   that doesn't keep the suite green is not done.
5. **Note follow-ups** — capture genuine technical debt or newly-discovered behaviors as a new
   feature-plan item rather than expanding this loop's scope.

## Refactor phase checklist

- [ ] Feature-plan acceptance criteria fully satisfied
- [ ] Duplication removed; names express intent; responsibilities single
- [ ] Analyzer seam and `internal/model` remain free of extraction internals
- [ ] Graceful degradation holds; no silent error swallowing
- [ ] `go test -race` green; no leaked goroutines/contexts; cancellation honored
- [ ] Extraction output deterministic (sorted/stable)
- [ ] Extractor fuzzed for never-panic where the parser was widened
- [ ] `gofmt` clean, `go vet` clean, full suite green
- [ ] Follow-up work captured as plan items, not scope creep
