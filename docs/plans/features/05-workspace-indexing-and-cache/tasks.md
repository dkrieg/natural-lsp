# Feature: Workspace Indexing & Cache

**Plan:** [docs/plans/features/05-workspace-indexing-and-cache/plan.md](../../05-workspace-indexing-and-cache/plan.md)
**PRD Requirements:** FR-32, FR-35, FR-36, FR-37, FR-38, FR-39, FR-40; NFR-1, NFR-2, NFR-3, NFR-5, NFR-8
**Priority:** P0 (full index, incremental, progress) P1 (persistent cache)
**Depends on:** [01 Workspace & Configuration](../01-workspace-and-configuration/plan.md), [02 Object Type Recognition](../02-object-type-recognition/plan.md), [04 Document Lifecycle & Sync](../04-document-lifecycle-and-sync/plan.md)

---

## Current-State Findings & Impact

### What Already Exists

1. **internal/model/model.go**: FileAnalysis struct exists with ObjectType and Diagnostics fields. The Symbols, Edges, and DataAccess fields are **missing** (marked with TODO). This is a **shared-contract change** the workspace index and future LSP handlers depend on.

2. **internal/analysis/natural/analyzer.go**: Basic Analyze(path, content) method exists and correctly classifies object types via classify(). The extraction pipeline (calls.go, data.go, symbols.go, hover.go) contains **TODO stubs** no actual extraction logic exists yet.

3. **internal/workspace/index.go**: **Empty TODO stub**. No Index type, no Build(), no Query() API. This is **new code**.

4. **internal/workspace/cache.go**: **Empty TODO stub**. No Save(), Load(), content-hash invalidation, or format-version gating. This is **new code**.

5. **internal/document/store.go**: **Fully implemented** for FR-33 (open document tracking via didOpen/didChange/didClose). No changes needed here.

6. **internal/document/sync.go**: **Fully implemented** for FR-34 (file watcher via fsnotify). No changes needed here.

7. **testdata/objecttype/**: Contains fixtures for all 15 Natural object types (.NSP, .NSN, .NSS, .NSC, .NSM, .NSL, .NSG, .NSA, .NSH, .NSD, .NS4, .NS7, .NS3, .NS8, .NST). Can be reused for indexing tests.

### Acceptance Criteria Classification

| Criterion | Status | Notes |
|-----------|--------|-------|
| FR-32 (progress reporting) | New | Requires workspace indexer + LSP progress hooks |
| FR-35 (incremental re-analysis) | New | Requires index + content-hash cache |
| FR-36 (full first-run index) | New | Requires index build + cache save |
| FR-37 (persistent cache) | New | Requires Save()/Load() |
| FR-38 (content-hash invalidation) | New | Requires content hashing in cache |
| FR-39 (format-version gating) | New | Requires version field in cache |
| FR-40 (safe cache deletion) | New | Implied by content-hash design |
| NFR-1 (linear cold index) | New | Requires efficient index build |
| NFR-2 (sub-second warm startup) | New | Requires fast cache load |
| NFR-3 (interactive requests) | New | Requires in-memory index |
| NFR-5 (non-blocking indexing) | New | Requires background indexing |
| NFR-8 (cache never stale) | New | Requires content-hash design |

### Shared Contract Change

**internal/model/model.go**: The FileAnalysis struct must be extended with Symbols, Edges, and DataAccess fields. This is a **shared contract** consumed by:
- internal/workspace/index.go (the cross-file symbol table)
- internal/server/ (LSP handlers for definition, references, hover, outline)
- Potential external tools (e.g., lsp-graph builder mentioned in README)

**Migration required**: After extending FileAnalysis, no existing consumers exist yet (workspace/index.go is empty, server handlers are stubs), so no migration tasks are needed. However, the extension must be done **before** any workspace indexing tasks.

---

## Ordered Task List

### Foundation: Extend FileAnalysis Contract

#### Task 05-F01: Extend FileAnalysis with Symbols, Edges, and DataAccess Fields

**Behavior**: Add Symbols, Edges, and DataAccess fields to model.FileAnalysis to support the workspace index and future LSP handlers.

**Testdata fixtures**: None (pure model change).

**Expected result**: model.FileAnalysis struct has:
```go
type FileAnalysis struct {
    ObjectType  ObjectType
    Diagnostics []Diagnostic
    Symbols     []Symbol        // TODO: define Symbol type
    Edges       []Edge          // TODO: define Edge type with Caller, Target, Kind
    DataAccess  []DataAccess    // TODO: define DataAccess type
}
```

**Reuses/migrates**: Extends existing FileAnalysis in internal/model/model.go. No existing consumers to migrate (workspace/index.go is empty).

**DoD**:
- [ ] go vet ./internal/model/ passes
- [ ] go build ./... passes
- [ ] Existing internal/analysis/natural/analyzer_test.go tests still pass
- [ ] model.FileAnalysis is free of backend internals (no parser state, no regex types)

**Agents**: tdd-green (documentation-only change, no test needed)

**Dependencies**: None

---

### Workspace Index: Core Data Structure

#### Task 05-W01: Define Index Type and Basic Query API

**Behavior**: Create workspace.Index type as an in-memory map of file paths to FileAnalysis results, with basic query methods.

**Testdata fixtures**: None (unit test with synthetic data).

**Expected result**: workspace.Index type with:
- type Index struct { entries map[string]model.FileAnalysis }
- func (idx *Index) Add(path string, analysis model.FileAnalysis)
- func (idx *Index) Get(path string) (model.FileAnalysis, bool)
- func (idx *Index) ForEach(f func(path string, analysis model.FileAnalysis))
- func (idx *Index) Keys() []string

**Reuses/migrates**: New code; internal/model.FileAnalysis from Task 05-F01.

**DoD**:
- [ ] Unit tests in workspace/index_test.go cover Add, Get, ForEach, Keys
- [ ] Table-driven tests for empty index, single entry, multiple entries
- [ ] go vet ./internal/workspace/ passes
- [ ] Concurrency-safe: sync.RWMutex guards the map

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-F01

---

#### Task 05-W02: Implement Index Build from Workspace Files

**Behavior**: Create workspace.Build(root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger) (*Index, error) that walks the workspace and indexes all indexed files.

**Testdata fixtures**: testdata/objecttype/ fixtures (15 files).

**Expected result**: Build() returns an Index containing all 15 fixture files, each with correct ObjectType and empty Symbols/Edges/DataAccess (since extraction is not implemented yet). Excluded directories (.git, archive, backup) are skipped. Files exceeding MaxFileSize are skipped with SkipTooLarge.

**Reuses/migrates**: config.Config.IsExcluded(), internal/analysis.Analyzer.Analyze(), internal/model.ObjectType.

**DoD**:
- [ ] Unit test TestBuild_CoreTypes verifies all 15 testdata/objecttype/ files are indexed
- [ ] Unit test TestBuild_ExcludedDirectories verifies .git/ is skipped
- [ ] Unit test TestBuild_TooLargeFiles verifies files > MaxFileSize are skipped
- [ ] Progress callback invoked per file (see Task 05-S01)
- [ ] go vet ./internal/workspace/ passes
- [ ] Graceful degradation: analyzer panics are recovered per-file (FR-43)

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W01, Task 05-F01

---

### Workspace Index: Dependency Tracking

#### Task 05-W03: Add Dependency-Aware Invalidation to Index

**Behavior**: Extend Index to track INCLUDE dependencies and provide a method to invalidate only affected files when a file changes.

**Testdata fixtures**: Need fixtures for INCLUDE relationships (e.g., program.NSP that INCLUDEs copycode.NSC).

**Expected result**: Index tracks INCLUDES edges; Index.Invalidate(path string) returns set of files that depend on path (transitively).

**Reuses/migrates**: Extends Index from Task 05-W02; Edges field from Task 05-F01.

**DoD**:
- [ ] Unit test TestInvalidate_INCLUDE verifies changing a copycode invalidates includers
- [ ] Unit test TestInvalidate_Transitive verifies transitive invalidation
- [ ] go vet ./internal/workspace/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W02, extraction of INCLUDE edges (Task 05-E03)

---

### Persistent Cache

#### Task 05-C01: Implement Cache Save/Load with Content-Hash Invalidation

**Behavior**: Create workspace.Save(idx *Index, path string) error and workspace.Load(path string) (*Index, bool, error) that serializes the index to disk, keyed by content hash per file.

**Testdata fixtures**: Synthetic index with 3-5 files.

**Expected result**: 
- Save() writes index to JSON file at path.
- Load() reads JSON, computes content hash for each file, returns (loadedIndex, staleFiles[]) where staleFiles are files whose content hash changed.
- Cache format includes a version field.

**Reuses/migrates**: New code; internal/model.FileAnalysis.

**DoD**:
- [ ] Unit test TestSave_Load verifies round-trip preservation
- [ ] Unit test TestLoad_ContentHashInvalidation verifies stale files are detected
- [ ] Unit test TestLoad_FormatVersionMismatch verifies full rebuild on version change
- [ ] go vet ./internal/workspace/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W02

---

#### Task 05-C02: Integrate Cache into Workspace Build

**Behavior**: Modify workspace.Build() to optionally load from cache first, then incrementally re-analyze only stale files.

**Testdata fixtures**: Same as Task 05-W02.

**Expected result**: Build(root, cfg, az, logger, cachePath string) returns:
- Full index if no cache exists
- Loaded index + incremental re-analysis of changed files if cache exists
- staleCount and totalFiles returned for progress reporting

**Reuses/migrates**: Extends Build() from Task 05-W02; Save()/Load() from Task 05-C01.

**DoD**:
- [ ] Unit test TestBuild_WithCache verifies incremental re-analysis
- [ ] Unit test TestBuild_CacheMiss verifies full rebuild when cache missing
- [ ] go vet ./internal/workspace/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-C01

---

### Progress Reporting

#### Task 05-S01: Add Progress Callback to Index Build

**Behavior**: Add OnProgress func(path string, current, total int) callback to workspace.Build() that reports per-file progress.

**Testdata fixtures**: None (unit test with synthetic data).

**Expected result**: OnProgress is invoked for each file during indexing with accurate counts. Callback is optional (can be nil).

**Reuses/migrates**: Extends Build() from Task 05-W02.

**DoD**:
- [ ] Unit test TestBuild_ProgressCallback verifies callback invoked for each file
- [ ] Unit test TestBuild_ProgressCounts verifies accurate current/total values
- [ ] go vet ./internal/workspace/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W02

---

### Integration: Wire Index into Server

#### Task 05-I01: Wire Workspace Index into Server Initialize

**Behavior**: Modify server.Run() to build the workspace index on startup and expose it to LSP handlers.

**Testdata fixtures**: None (integration test).

**Expected result**: On initialize, the server builds the workspace index (using cache if available) and stores it in a field accessible to handlers.

**Reuses/migrates**: Extends server.Run() from feature 03; workspace.Build() from Task 05-W02.

**DoD**:
- [ ] Integration test TestServer_IndexBuiltOnStartup verifies index is built
- [ ] Progress reported via window/workDoneProgress (see Task 05-I02)
- [ ] go vet ./internal/server/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W02, Task 05-S01

---

#### Task 05-I02: Expose Progress to LSP via window/workDoneProgress

**Behavior**: Wire the OnProgress callback to send LSP progress notifications via window/workDoneProgress/create.

**Testdata fixtures**: None (integration test).

**Expected result**: During indexing, the server sends window/workDoneProgress/create notifications with messages like "Indexing workspace... 1,243 / 2,891 files (43%)".

**Reuses/migrates**: Extends server.Run() from feature 03; OnProgress from Task 05-S01.

**DoD**:
- [ ] Integration test TestServer_ProgressReporting verifies progress notifications
- [ ] Progress ends cleanly when indexing completes or is cancelled
- [ ] go vet ./internal/server/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-I01

---

### Incremental Re-Analysis

#### Task 05-R01: Wire Incremental Re-Analysis for Open Documents

**Behavior**: When an open document changes (textDocument/didChange), re-analyze only that file and update the index.

**Testdata fixtures**: testdata/objecttype/program.NSP fixture.

**Expected result**: After didChange on a document, the index reflects the new content; Get() returns updated analysis.

**Reuses/migrates**: Extends document.Store (already implemented); workspace.Index.Add() from Task 05-W01.

**DoD**:
- [ ] Integration test TestDocumentStore_IncrementalReanalysis verifies index update
- [ ] go vet ./internal/document/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W01

---

#### Task 05-R02: Wire Incremental Re-Analysis for External File Changes

**Behavior**: When the file watcher detects an external change (workspace/didChangeWatchedFiles), re-analyze the affected file and dependents.

**Testdata fixtures**: testdata/objecttype/ fixtures + INCLUDE relationship.

**Expected result**: When a file changes on disk, the index is updated; when a copycode changes, includers are also re-analyzed.

**Reuses/migrates**: Extends document.Watcher (already implemented); workspace.Index.Invalidate() from Task 05-W03.

**DoD**:
- [ ] Integration test TestWatcher_ExternalChangeReanalysis verifies file re-analysis
- [ ] Integration test TestWatcher_INCLUDEInvalidation verifies dependent re-analysis
- [ ] go vet ./internal/document/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-W03

---

### Extraction: Minimal Coverage for Indexing

#### Task 05-E01: Implement CALLNAT Extraction (Static and Dynamic)

**Behavior**: Extract CALLNAT statements from Natural source files, producing CALLS edges for literal targets and CALLS_DYNAMIC edges for variable targets.

**Testdata fixtures**: testdata/objecttype/program.NSP with CALLNAT 'LITERAL' and CALLNAT #VARIABLE examples.

**Expected result**: FileAnalysis.Edges contains:
- EdgeKind="CALLS" for literal targets with caller + target info
- EdgeKind="CALLS_DYNAMIC" for variable targets with caller + variable expression

**Reuses/migrates**: Extends natural.Analyzer.Analyze() from existing stub; model.EdgeKind constants.

**DoD**:
- [ ] Unit test TestAnalyze_CALLNAT_Static verifies static call extraction
- [ ] Unit test TestAnalyze_CALLNAT_Dynamic verifies dynamic call modeling
- [ ] go vet ./internal/analysis/natural/ passes
- [ ] Regression: existing TestAnalyze_ObjectType tests still pass

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-F01

---

#### Task 05-E02: Implement PERFORM and INCLUDE Extraction

**Behavior**: Extract PERFORM (subroutine calls) and INCLUDE (copycode dependencies) statements.

**Testdata fixtures**: program.NSP with PERFORM subroutine and INCLUDE copycode.NSC.

**Expected result**: FileAnalysis.Edges contains:
- EdgeKind="PERFORMS" for subroutine calls
- EdgeKind="INCLUDES" for copycode dependencies

**Reuses/migrates**: Extends natural.Analyzer.Analyze() from Task 05-E01.

**DoD**:
- [ ] Unit test TestAnalyze_PERFORM verifies subroutine extraction
- [ ] Unit test TestAnalyze_INCLUDE verifies copycode extraction
- [ ] go vet ./internal/analysis/natural/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-E01

---

#### Task 05-E03: Implement Data Access Extraction (READ/FIND/GET, STORE/UPDATE/DELETE)

**Behavior**: Extract data-access statements and DEFINE DATA sections.

**Testdata fixtures**: program.NSP with READ/FIND/GET and STORE/UPDATE/DELETE statements; DEFINE DATA LOCAL/GLOBAL/PARAMETER sections.

**Expected result**: FileAnalysis.DataAccess contains read/write relationships; FileAnalysis.Symbols contains data section symbols.

**Reuses/migrates**: Extends natural.Analyzer.Analyze() from Task 05-E02.

**DoD**:
- [ ] Unit test TestAnalyze_DataAccess_Read verifies READ/FIND/GET extraction
- [ ] Unit test TestAnalyze_DataAccess_Write verifies STORE/UPDATE/DELETE extraction
- [ ] Unit test TestAnalyze_DefineData verifies DEFINE DATA extraction
- [ ] go vet ./internal/analysis/natural/ passes

**Agents**: tdd-red -> tdd-green -> tdd-refactor

**Dependencies**: Task 05-E02

---

### Verification and Regression

#### Task 05-V01: Integration Test Suite for Workspace Indexing

**Behavior**: Comprehensive integration tests for the full indexing pipeline.

**Testdata fixtures**: Multi-file fixture suite with CALLNAT, PERFORM, INCLUDE, and data access relationships.

**Expected result**: Tests verify:
- Full index build from workspace
- Cache save/load round-trip
- Incremental re-analysis on file change
- Progress reporting

**Reuses/migrates**: New integration tests in internal/workspace/ and internal/server/.

**DoD**:
- [ ] Integration tests in workspace/index_integration_test.go
- [ ] Integration tests in server/server_integration_test.go
- [ ] All tests pass with -race detector
- [ ] just verify passes

**Agents**: tdd-green (integration tests are greenfield)

**Dependencies**: All previous tasks

---

## Reviews Required

Run /review-feature 05-workspace-indexing-and-cache with the following reviewers:

| Review Type | Trigger |
|-------------|---------|
| review-concurrency | Tasks 05-W01 (Index mutex), 05-W02 (Build goroutine if added), 05-R01/R02 (incremental re-analysis) |
| review-seam | Task 05-F01 (FileAnalysis extension), Task 05-I01 (server integration) |
| review-robustness | Tasks 05-W02, 05-C01, 05-R01/R02 (graceful degradation, panic recovery) |
| review-docs | Feature completion (CLAUDE.md/README.md sync for indexing/caching features) |

---

## Open Questions

1. **Progress reporting granularity**: Should progress be per-file (current task) or batched? Per-file provides more frequent updates but more LSP traffic.

2. **Cache file format**: JSON is human-readable but slower; consider binary format (e.g., gob or custom) for large workspaces. Trade-off: human-debuggability vs. performance.

3. **Dependency tracking scope**: Should Index.Invalidate() track all edge types (CALLS, PERFORMS, INCLUDES, READS, WRITES) or only INCLUDE? INCLUDE is the primary incremental trigger; others may be less frequent.

4. **Memory limits**: Should Build() enforce a maximum memory budget and fail gracefully for extremely large workspaces? Current design assumes in-memory index is acceptable.

5. **Symbol type definition**: What fields should Symbol contain? (name, kind, position, container?) Should it be defined in model or workspace?

6. **Edge type definition**: Should Edge include source/target positions or just file paths? Position info enables "go to definition" but increases memory.

7. **Cache invalidation on file rename**: Should the cache track file renames (via content hash) or just path-based invalidation? Content hash enables rename detection but requires tracking all file hashes.

---

## Traceability Matrix

| Acceptance Criterion | Task(s) |
|---------------------|---------|
| FR-32 (progress reporting) | Task 05-S01, Task 05-I02 |
| FR-35 (incremental re-analysis) | Task 05-W03, Task 05-R01, Task 05-R02 |
| FR-36 (full first-run index) | Task 05-W02 |
| FR-37 (persistent cache) | Task 05-C01, Task 05-C02 |
| FR-38 (content-hash invalidation) | Task 05-C01 |
| FR-39 (format-version gating) | Task 05-C01 |
| FR-40 (safe cache deletion) | Task 05-C01 (implied) |
| NFR-1 (linear cold index) | Task 05-W02 |
| NFR-2 (sub-second warm startup) | Task 05-C02 |
| NFR-3 (interactive requests) | Task 05-W01 (in-memory index) |
| NFR-5 (non-blocking indexing) | Task 05-S01 (progress callback) |
| NFR-8 (cache never stale) | Task 05-C01 (content-hash design) |

---

## Notes

- **Skip rule**: Tasks 05-E01-05-E03 (extraction) are minimal coverage for indexing; full extraction is out of scope for this feature per the plan's "Out of scope" section.
- **Fixture reuse**: testdata/objecttype/ fixtures from feature 02 can be reused for indexing tests. Additional fixtures may be needed for INCLUDE relationships and data access patterns.
- **Model-first approach**: Task 05-F01 extends the shared contract before any workspace/index code is written, ensuring downstream tasks have the needed types.
- **Incremental scope**: The feature plan explicitly defers full extraction to plans 06-08; this feature focuses on the index and cache mechanics.