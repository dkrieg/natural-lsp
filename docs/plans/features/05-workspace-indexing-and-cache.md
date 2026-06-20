# Feature: Workspace indexing & cache

**Status:** Planned
**PRD requirements:** FR-32, FR-35, FR-36, FR-37, FR-38, FR-39, FR-40; NFR-1–5, NFR-8
**Priority / phase:** P0 (build, incremental, progress) · P1 (persistent cache)
**Depends on:** [01](01-workspace-and-configuration.md), [02](02-object-type-recognition.md),
[04](04-document-lifecycle-and-sync.md)

## Summary

Builds and maintains the cross-file index that every navigation and resolution feature queries. The
first open performs a full index with visible progress; later changes re-index only what's affected;
and the index persists to disk so subsequent startups are fast and valid across git checkouts.

## User stories

### Story 1 — Full first-run index (FR-36, NFR-1, NFR-4)
**As a** developer opening a codebase, **I want** the whole workspace indexed **so that** cross-file
features work everywhere.

**Acceptance criteria:**
- [ ] On first open with no valid cache, all files in the indexed set are analyzed and recorded in the
      cross-file index.
- [ ] Cold index time scales approximately linearly with file count.
- [ ] Indexing an enterprise-scale workspace (tens of thousands of objects) completes without
      exhausting typical developer-machine memory.

### Story 2 — Visible indexing progress (FR-32, NFR-5)
**As a** developer, **I want** to see indexing progress **so that** I know the server is working and
when results will be complete.

**Acceptance criteria:**
- [ ] During first-run/full indexing, progress is reported via the editor's standard progress
      mechanism, including a count or percentage.
- [ ] Indexing does not block the editor; the UI remains responsive while indexing runs.
- [ ] Progress reporting ends cleanly when indexing completes or is cancelled.

### Story 3 — Incremental re-analysis (FR-35, NFR-3)
**As a** developer editing one file, **I want** only the affected work redone **so that** results
update quickly.

**Acceptance criteria:**
- [ ] A change to one file re-analyzes that file and its dependents only — not the whole workspace.
- [ ] Dependents are correctly identified (e.g. changing a copycode re-evaluates files that INCLUDE
      it; changing a module re-evaluates inbound-call counts).
- [ ] After incremental update, queries reflect the change without a restart.

### Story 4 — Persistent cache (FR-37, NFR-2)
**As a** developer reopening a project, **I want** fast startup **so that** I'm productive
immediately.

**Acceptance criteria:**
- [ ] The completed index is serialized to the configured cache location.
- [ ] On subsequent startup with a valid cache, the server loads from cache and re-analyzes only files
      whose content changed since last run.
- [ ] Warm startup is sub-second regardless of codebase size.
- [ ] The cache directory is safe to delete at any time; deleting it triggers a clean full rebuild.

### Story 5 — Cache validity across checkouts & upgrades (FR-38, FR-39, FR-40, NFR-8)
**As a** developer using git, **I want** the cache to stay correct across branch switches and tool
upgrades **so that** I never see stale or wrong results.

**Acceptance criteria:**
- [ ] Cache entries are invalidated based on file **content** (content hash), not modification time.
- [ ] Switching branches such that a file's content reverts to a previously-seen state does not force
      unnecessary re-analysis, and changed content is always re-analyzed.
- [ ] A change in cache-format version forces a full rebuild on upgrade.
- [ ] The cache never serves results for content that has since changed.
- [ ] The cache is excluded from version control by default.

## Out of scope
- What is stored per file (the analysis model) — see extraction plans 06–08.
- The semantics of resolution that the index enables — see plan 06.

## Open questions
- Whether the index/cache must support concurrent multi-root workspaces in the first release.
- Memory-vs-speed trade-offs for very large repos (streaming vs. fully in-memory index).
