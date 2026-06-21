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
| [lsp-protocol.md](lsp-protocol.md) | JSON-RPC base, lifecycle, capabilities per method, position-encoding negotiation, sync kind, ranges, `$/cancelRequest`→context | verified (2026-06-20) (LSP 3.17 spec) |
| [architecture-decisions.md](architecture-decisions.md) | ADR log: regex extraction, Analyzer seam, extraction↔resolution split, cache invalidation, position encoding, sync kind, transport lib, hash, index concurrency model, extractor fuzzing | verified (2026-06-20) (internal docs + Go KB) |
| [testing-strategy.md](testing-strategy.md) | Pyramid, table-driven, testdata fixtures, golden files (+determinism contract), Analyzer-seam fakes, fuzzing the extractor | verified (2026-06-20) (internal docs; Go-fuzz fact: go.dev) |
| [engineering-principles.md](engineering-principles.md) | SOLID, DRY/YAGNI/KISS, quality gates, reviews | verified (2026-06-20) (recognized literature + NFRs) |

## Open questions (to verify on next relevant task)

- Should the server implement LSP 3.17 **pull diagnostics** (`diagnosticProvider`) in addition to /
  instead of the current push model (`textDocument/publishDiagnostics`)? Push is decided for v1;
  revisit if a target client only supports pull. (cross-ref `lsp-protocol.md`)
- `codeLens/resolve` — will codeLenses be resolved lazily (`CodeLensOptions.resolveProvider: true`) or
  computed eagerly? Decide when feature plan for code lens is implemented.
- `go.lsp.dev/protocol` defaults its `Position.Character` semantics to UTF-16; confirm how it exposes
  the negotiated `positionEncoding` so ADR-008's single conversion point can honor UTF-8 when offered.

## Changelog

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