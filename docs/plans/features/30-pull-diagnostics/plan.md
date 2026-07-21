# Feature: Pull Diagnostics (`textDocument/diagnostic`)

**Status:** Planned
**PRD requirements:** FR-57 (pull diagnostics — new); complements FR-30/FR-31 (push diagnostics, feature 14); NFR-11 (LSP conformance), FR-43 (graceful degradation)
**Priority / phase:** P2 (post-v1.0; protocol modernization — push diagnostics already deliver the content)
**Depends on:** [14](../14-diagnostics/plan.md) (the diagnostic producers + `aggregateDiagnostics`), [03](../03-server-lifecycle-and-protocol/plan.md) (capabilities/dispatch), [19](../19-protocol-marshaling-unification/plan.md) (json/v2 marshaling)

## Summary

Diagnostics are already produced and delivered — feature 14 pushes `textDocument/publishDiagnostics`
(parse-error FR-30 + ambiguity FR-31) on open/change/watch. What we do **not** implement is the LSP 3.17
**pull** model: `textDocument/diagnostic` (per-document) and `workspace/diagnostic` (workspace), advertised
via a `diagnosticProvider`. Some clients prefer pull (they request diagnostics on their own schedule and
can dedupe/refresh deterministically). This feature adds the pull provider **on top of the existing
aggregation** — the *content* is unchanged; only the *delivery mechanism* is added.

**Server-layer only — no `internal/model` change, no cache-format bump.** It reuses feature 14's
`aggregateDiagnostics` pipeline verbatim (parser `Diagnostics` + resolver ambiguity), served store-first
from the open buffer / index. It **adds a `diagnosticProvider` capability**, so the locked `TestInitialize`
allow-list gains one entry.

## User stories

### Story 1 — Per-document pull diagnostics (FR-57, refines FR-30/FR-31)
**As a** user on a pull-diagnostics client, **I want** `textDocument/diagnostic` to return the file's
diagnostics on request.

**Acceptance criteria:**
- [ ] The server advertises a `diagnosticProvider` (with an `identifier`, `interFileDependencies` set
      honestly, `workspaceDiagnostics` per Story 2) and handles `textDocument/diagnostic`, returning a
      **full** `RelatedFullDocumentDiagnosticReport` built from the same `aggregateDiagnostics` output as
      the push path — byte-identical diagnostic content (message/severity/code/range, `source="natural-lsp"`).
- [ ] An unchanged document may return an **unchanged** report (`resultId` reuse) if cheap; otherwise a
      full report each time (documented decision — unchanged-report is an optimization, not required).
- [ ] Missing/unreadable/out-of-root/no-diagnostics → an empty full report, never an error (FR-43).
- [ ] `TestInitialize`'s allow-list is updated to include `diagnosticProvider`.

### Story 2 — Workspace pull diagnostics (FR-57)
**As a** user, **I want** `workspace/diagnostic` to report diagnostics across the indexed workspace.

**Acceptance criteria:**
- [ ] `workspace/diagnostic` returns a report per file with diagnostics, drawn from the index (bounded —
      no re-analysis sweep beyond what the index already holds); files with none are omitted or reported
      empty per spec.
- [ ] Work-done/partial-result progress tokens are honored if the client supplies them (reuse feature 21's
      progress plumbing); large workspaces stream partial results rather than blocking.

## Out of scope / deferred
- **Removing push diagnostics** — push (feature 14) stays; a client uses whichever it advertises. The two
  must not double-report (the server responds to pull *or* pushes; document the interaction — typically a
  pull-capable client suppresses the need for push, but the server keeps pushing for push-only clients).
- **Related-information graph** beyond what feature 14 already models.

## Open questions
- **OQ-1 — push/pull coexistence.** Should the server stop pushing when the client advertises pull support
  (`textDocument.diagnostic`)? Recommend: keep pushing unless the client advertises pull, then rely on pull
  for those documents to avoid double delivery — confirm against real client behavior.
- **OQ-2 — `resultId`/unchanged reports.** Implement unchanged-report caching (needs a per-document result
  id keyed on content hash) or always return full? Recommend full first; add unchanged-reports only if a
  client thrashes.

## Notes
- No `internal/model`/cache change; reuses feature 14's `aggregateDiagnostics`. New capability
  (`diagnosticProvider`) → `TestInitialize` allow-list extended. json/v2 marshaling (feature 19);
  store-first; encoding-aware ranges (`position.go`). Fuzz the report builder (never panic — FR-43).
