# Feature plans — natural-lsp

These files decompose [the PRD](../natural-lsp-prd.md) into distinct, independently-reviewable
features. Each plan is written as **user stories with concrete acceptance criteria** — *what* the
feature must do and how we know it's done. They are **not** implementation plans: no architecture, no
code, no sequencing of work.

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

| # | Plan | Covers (PRD FR/CR) | Phase |
|---|------|--------------------|-------|
| **Foundation** ||||
| 01 | [Workspace & configuration](01-workspace-and-configuration/plan.md) | FR-1–6, CR-1–6 | P0/P1 |
| 02 | [Object-type recognition](02-object-type-recognition/plan.md) | FR-7–9 | P0/P2 |
| 03 | [Server lifecycle & protocol](03-server-lifecycle-and-protocol/plan.md) | FR-41–43 | P0 |
| 04 | [Document lifecycle & sync](04-document-lifecycle-and-sync/plan.md) | FR-33–34 | P0/P1 |
| 05 | [Workspace indexing & cache](05-workspace-indexing-and-cache/plan.md) | FR-32, 35–40 | P0/P1 |
| **Extraction & resolution** ||||
| 06 | [Call & dependency resolution](06-call-and-dependency-resolution/plan.md) | FR-10–18, FR-5 | P0/P1/P2 |
| 07 | [Data-access extraction](07-data-access-extraction/plan.md) | FR-19–22 | P0/P1/P2 |
| 08 | [Program-structure extraction](08-program-structure-extraction/plan.md) | FR-23 | P0 |
| **Editor features** ||||
| 09 | [Navigation & symbol search](09-navigation-and-symbol-search/plan.md) | FR-24–26 | P0 |
| 10 | [Document outline](10-document-outline/plan.md) | FR-27 | P0 |
| 11 | [Hover](11-hover/plan.md) | FR-28 | P1 |
| 12 | [Code lens](12-code-lens/plan.md) | FR-29 | P2 |
| 13 | [Diagnostics](13-diagnostics/plan.md) | FR-30–31, FR-17 | P0/P1 |
| **Clients** ||||
| 14 | [Editor clients](14-editor-clients/plan.md) | FR-44–46 | P0/P1 |
| **Interactive typing features** ||||
| 15 | [Completion](15-completion/plan.md) | FR-47 | P1 |
| 16 | [Signature help](16-signature-help/plan.md) | FR-48 | P1 |
| 17 | [Call hierarchy](17-call-hierarchy/plan.md) | FR-49 | P1 |