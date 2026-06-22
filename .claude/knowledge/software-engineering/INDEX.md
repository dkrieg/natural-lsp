# Software Engineering Knowledge Base — Index

Cross-cutting engineering reference for `natural-lsp`, maintained by the `software-engineer-expert`
agent. Scope: the **LSP protocol** this server must honor, this project's **architecture decisions**
(an ADR log), the **testing strategy**, and general **engineering principles**. Go-syntax craft lives
in the `go-development` skill; Natural-language facts live in `.claude/knowledge/natural/`. Read this
index first, then the relevant topic.

**Status legend:** `verified (date)` = corroborated against an authoritative source (for project
decisions, the repo's own README/PRD/CLAUDE are authoritative) · `needs-verification` = seeded
belief, confirm before relying on it · `unverified` = recorded but unconfirmed.

## Topics

| File | Covers | Overall status |
|------|--------|----------------|
| [lsp-protocol.md](lsp-protocol.md) | JSON-RPC base, lifecycle, capabilities per method, position-encoding negotiation, sync kind, ranges, push-vs-pull diagnostics, `$/cancelRequest`→context | verified (2026-06-21) (LSP 3.17 spec) |
| [architecture-decisions.md](architecture-decisions.md) | ADR log: parser-based extraction (ADR-015 supersedes ADR-001), Analyzer seam, extraction↔resolution split, cache invalidation, position encoding, sync kind, transport lib, hash, index concurrency model, parser fuzzing, push diagnostics | verified (2026-06-21) (internal docs + Go KB) — ADR-001 superseded 2026-06-21 |
| [testing-strategy.md](testing-strategy.md) | Pyramid, table-driven, testdata fixtures, golden files (+determinism contract), Analyzer-seam fakes, fuzzing the parser | verified (2026-06-20) (internal docs; Go-fuzz fact: go.dev) |
| [engineering-principles.md](engineering-principles.md) | SOLID, DRY/YAGNI/KISS, quality gates, reviews | verified (2026-06-20) (recognized literature + NFRs) |

## Open questions (to verify on next relevant task)

- `codeLens/resolve` — will codeLenses be resolved lazily (`CodeLensOptions.resolveProvider: true`) or
  computed eagerly? Decide when the code-lens feature plan is implemented.

## Changelog

- 2026-06-21 (addendum) — ADR-010 user sign-off received: Option A (`go.lsp.dev/protocol` +
  `go.lsp.dev/jsonrpc2`) accepted with full awareness of the transitive json/v2 dependency. Pending
  decision removed; ADR-010 re-evaluation block updated to record sign-off. HIGH open question cleared.
- 2026-06-21 — Full verification sweep. **lsp-protocol.md**: re-confirmed the position-encoding
  default (`"If omitted it defaults to 'utf-16'."`), the mandatory-UTF-16 baseline,
  `general.positionEncodings` negotiation, and the no-encodings→`utf-16` rule verbatim against the
  live LSP 3.17 spec (→ `verified (2026-06-21)`); expanded the diagnostics section with the **push
  vs. pull** model (pull = `diagnosticProvider`/`textDocument/diagnostic`/`workspace/diagnostic`,
  motivation = client-controlled timing); narrowed the negotiated-encoding plumbing open question to
  "ordinary capabilities field, no library magic." **architecture-decisions.md**: recorded **ADR-014**
  (push diagnostics for v1 — resolves the prior push-vs-pull open question with a dated rationale);
  **downgraded ADR-010 to provisional** after the go-improve sweep found `go.lsp.dev/protocol@v1.0.0`
  pulls experimental json/v2 (`go-json-experiment/json`) transitively, contradicting the project's
  json/v2-avoidance stance — flagged as a human-in-the-loop dependency sign-off, not silently
  reversed. **Open questions**: closed pull-diagnostics (→ADR-014) and negotiated-encoding plumbing
  (resolved in `lsp-protocol.md`); promoted the ADR-010 sign-off to the top HIGH open question;
  retained codeLens/resolve. testing-strategy.md and engineering-principles.md re-reviewed, no change
  needed (still `verified`).
- 2026-06-20 — Go-pattern boundary review (SE vs. Go KB vs. go-development skill). Added SE-level
  testing patterns that sit at the LSP-server↔Go seam: **Analyzer-seam fake testing** (test
  `internal/server` against a fake Analyzer, per ADR-002), **golden-file testing with a determinism
  contract** (sorted/stable output — also a cache/lsp-graph requirement), and **fuzzing the extractor**
  as the FR-43 "never panic" guard (corpus committed under `testdata/fuzz/` as a regression seed) — all
  in `testing-strategy.md`. Added the **`$/cancelRequest`→`context` cancellation contract** and error
  codes `-32800`/`-32801` to `lsp-protocol.md` (verified vs. LSP 3.17). Recorded **ADR-012** (index
  concurrency: snapshot-on-read + bounded worker pool) and **ADR-013** (fuzz the extraction entry
  point). Reinforced `-race`/`vet`/`gofmt` as **enforced CI gates** in `engineering-principles.md`. Go
  *mechanics* of fakes/golden/fuzz/worker-pool deliberately left to the Go KB / skill (routed, not
  duplicated).
- 2026-06-20 — Verification sweep across all topics. **lsp-protocol.md**: verified base framing,
  lifecycle, half-open zero-based ranges, `TextDocumentSyncKind` enum, position-encoding default
  (UTF-16) + `general.positionEncodings`/`positionEncoding` negotiation, and exact per-method
  ServerCapabilities field names/types, all against the **LSP 3.17** spec → status `verified`.
  **architecture-decisions.md**: recorded ADR-008 (negotiate UTF-8, default UTF-16), ADR-009 (Full
  document sync for v1), ADR-010 (`go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` v1.0.0), ADR-011
  (`crypto/sha256` cache key); cleared all three Pending decisions. **engineering-principles.md**:
  grounded SOLID / Go proverbs / DRY in primary sources → status `verified`. Replaced the three
  resolved open questions with three new lower-risk ones (pull diagnostics, codeLens resolve, how the
  chosen lib exposes negotiated encoding).
- 2026-06-20 — (seed) Created index and four topics.