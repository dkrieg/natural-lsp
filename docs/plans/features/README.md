# Feature plans — natural-lsp

These files decompose [the PRD](../natural-lsp-prd.md) into distinct, independently-reviewable
features. Each plan is written as **user stories with concrete acceptance criteria** — *what* the
feature must do and how we know it's done. They are **not** implementation plans: no architecture, no
code, no sequencing of work (the per-feature `tasks.md`, produced at `/plan-feature` time, holds the
decomposition).

**How to read a plan.** Each file states the PRD requirements it covers, its priority/phase, and the
features it depends on, then lists user stories. Every acceptance criterion is meant to be
demonstrable — ideally as a test fixture or an observable editor behavior.

**Priority / phase legend** (from PRD §10):
- **P0 — MVP:** usable navigation on one editor.
- **P1 — v1.0 stable:** trustworthy at scale, multi-editor.
- **P2 — post-v1.0:** deeper coverage.

**Testing convention (applies to every plan).** When a construct is mishandled, a minimal sanitized
fixture is added under `testdata/` and kept as a permanent regression fixture (PRD NFR-9, M-5).
Acceptance criteria that reference extraction or resolution are expected to be backed by such
fixtures.

## Index

The `#`/link column matches the on-disk directory names exactly. **Status** is `shipped` (merged to
`main`) or `planned` (plan written, not yet implemented). Features 19–23 were assessment-remediation
follow-ups (see the `2026-07-14` assessment); 24–25 were post-release fixes; 26–34 are the current
planned backlog.

| # | Plan | Covers (PRD FR/CR/NFR) | Phase | Status |
|---|------|------------------------|-------|--------|
| **Foundation** ||||
| 00 | [Parser foundation](00-parser-foundation/plan.md) | NFR-15, FR-30, M-6 | P0 | shipped |
| 00 | [Parser — embedded SQL](00-parser-embedded-sql/plan.md) | FR-30, NFR-15 | P0 | shipped |
| 01 | [Workspace & configuration](01-workspace-and-configuration/plan.md) | FR-1–6, CR-1–6 | P0/P1 | shipped |
| 02 | [Object-type recognition](02-object-type-recognition/plan.md) | FR-7–9 | P0/P2 | shipped |
| 03 | [Server lifecycle & protocol](03-server-lifecycle-and-protocol/plan.md) | FR-41–43, NFR-11 | P0 | shipped |
| 04 | [Document lifecycle & sync](04-document-lifecycle-and-sync/plan.md) | FR-33–34 | P0/P1 | shipped |
| 05 | [Workspace indexing & cache](05-workspace-indexing-and-cache/plan.md) | FR-32, 35–40; NFR-1–5, 8 | P0/P1 | shipped |
| **Extraction & resolution** ||||
| 06 | [Call & dependency extraction](06-call-dependency-extraction/plan.md) | FR-10–15, 17, 18 | P0 | shipped |
| 07 | [Call & dependency resolution](07-call-dependency-resolution/plan.md) | FR-5, 10–18 | P0/P1/P2 | shipped |
| 08 | [Data-access extraction](08-data-access-extraction/plan.md) | FR-19–22 | P0/P1/P2 | shipped |
| 08b | [Embedded-SQL extraction](08b-embedded-sql-extraction/plan.md) | FR-19–21 | P1 | shipped |
| 09 | [Program-structure extraction](09-program-structure-extraction/plan.md) | FR-23 | P0 | shipped |
| **Editor features** ||||
| 10 | [Navigation & symbol search](10-navigation-and-symbol-search/plan.md) | FR-24–26 | P0 | shipped |
| 11 | [Document outline](11-document-outline/plan.md) | FR-27 | P0 | shipped |
| 12 | [Hover](12-hover/plan.md) | FR-28 | P1 | shipped |
| 13 | [Code lens](13-code-lens/plan.md) | FR-29 | P2 | shipped |
| 14 | [Diagnostics](14-diagnostics/plan.md) | FR-30–31, 17; NFR-14 | P0/P1 | shipped |
| **Clients** ||||
| 15 | [Editor clients](15-editor-clients/plan.md) | FR-44–46; NFR-10, 12, 13 | P0/P1 | shipped |
| **Interactive typing features** ||||
| 16 | [Completion](16-completion/plan.md) | FR-47 | P1 | shipped |
| 17 | [Signature help](17-signature-help/plan.md) | FR-48 | P1 | shipped |
| 18 | [Call hierarchy](18-call-hierarchy/plan.md) | FR-49 | P1 | shipped |
| **Assessment remediation (2026-07-14)** ||||
| 19 | [Protocol marshaling unification](19-protocol-marshaling-unification/plan.md) | FR-47 (repair), NFR-11 | P0 | shipped |
| 20 | [Workspace root handshake](20-workspace-root-handshake/plan.md) | FR-1, NFR-11/13/14 | P0 | shipped |
| 21 | [Async indexing & work-done progress](21-async-indexing-and-progress/plan.md) | FR-32, NFR-5 | P0 | shipped |
| 22 | [Performance & scale verification](22-performance-and-scale-verification/plan.md) | NFR-1–4 | P0/P1 | shipped |
| 23 | [Distribution hardening](23-distribution-hardening/plan.md) | NFR-10, 12; FR-42 | P1 | shipped |
| **Post-release fixes** ||||
| 24 | [Cache format compaction](24-cache-format-compaction/plan.md) | NFR-16, 2, 4; FR-37 | P1 | shipped |
| 25 | [LSP4IJ template validation](25-lsp4ij-template-validation/plan.md) | FR-45, 52; NFR-13 | P1 | shipped |
| **Planned follow-ups** ||||
| 26 | [LSP tracing & logging](26-lsp-tracing-and-logging/plan.md) | FR-53; NFR-14 | P1 | planned |
| 27 | [Variable & reference navigation](27-variable-navigation/plan.md) | FR-54 (refines FR-24/25/28) | P1 | planned |
| 28 | [Rich symbol detail & `VIEW OF` binding](28-symbol-detail-and-view-binding/plan.md) | FR-55 (refines FR-23/27) | P1 | planned |
| 29 | [Semantic tokens](29-semantic-tokens/plan.md) | FR-56 | P1 | planned |
| 30 | [Pull diagnostics](30-pull-diagnostics/plan.md) | FR-57 (complements FR-30/31) | P2 | planned |
| 31 | [Declaration & type-definition navigation](31-declaration-and-type-definition/plan.md) | FR-58 (refines FR-24) | P2 | planned |
| 32 | [Document links](32-document-links/plan.md) | FR-59 | P2 | planned |
| 33 | [Execute command](33-execute-command/plan.md) | FR-60 | P2 | planned |
| 34 | [Moniker](34-moniker/plan.md) | FR-61 | P2 | planned (deferred / non-goal) |
