# Product Requirements Document — natural-lsp

**Status:** Draft
**Last updated:** 2026-06-20
**Owner:** Daniel Krieg

---

## 1. Overview

`natural-lsp` is a Go-based Language Server Protocol (LSP) server for **Software AG Natural**, a 4GL
language widely deployed on IBM z/OS mainframes alongside COBOL, Adabas, and IMS. An existing
open-source LSP server for Natural — [natls](https://github.com/MarkusAmshove/natls) (Java, MIT) —
delivers broad editor intelligence via a full recursive-descent parser and is the reference
implementation studied during the design of this server. `natural-lsp` is a Go alternative with a
hand-written lexer + recursive-descent parser, delivering the same breadth of editor features while
adding config-driven NaturalONE-independent library mapping, explicit `CALLS_DYNAMIC` edge modeling,
a git-safe content-hash cache, and a clean extracted graph for
[lsp-graph](https://github.com/dkrieg/lsp-graph) integration.

This product delivers a single server binary that indexes a filesystem-based Natural codebase and
serves navigation, completion, references, hover, call hierarchy, document outline, and
workspace-symbol features to any LSP-capable editor, plus first-party editor clients for the two
most common environments.

This document defines **what** the product must do. It intentionally avoids prescribing **how**
features are implemented.

---

## 2. Goals & non-goals

### 2.1 Goals

- Give Natural developers modern code-intelligence (navigation, references, hover, outline, symbol
  search) inside their existing editors.
- Resolve relationships across files — calls, includes, and data access — reliably enough to trust
  for code comprehension and impact analysis.
- Deliver comprehensive coverage of the Natural constructs that appear in real production code via a
  hand-written lexer and recursive-descent parser, using [natls](https://github.com/MarkusAmshove/natls)
  as the reference implementation for statement coverage and parser structure.
- Make the boundaries of analysis observable: when something cannot be resolved, the product must
  say so explicitly rather than fail silently.
- Run fast and predictably on large enterprise codebases (tens of thousands of objects).
- Be installable and configurable with minimal friction across major platforms and editors.

### 2.2 Non-goals

- **Not** a Natural compiler, runtime, or interpreter.
- **Not** a code formatter, refactoring engine, or code-generation tool.
- Not a full static-analysis linter (style rules, dead-code detection, etc.) — syntax diagnostics and
  ambiguous-resolution diagnostics are in scope, but broad correctness checking is not.
- **Not** a connection to live mainframe Natural/Adabas libraries — the product operates on exported
  filesystem objects only.
- **Not** a batch/bulk export or reporting tool — analysis is interactive and editor-driven.
- **Not** responsible for resolving Adabas DDM physical metadata or IMS segment metadata beyond what
  is present in the indexed source files.
- Completeness of obscure legacy/preprocessor constructs is explicitly out of scope for the first
  stable release.

---

## 3. Target users

| User | Need |
|------|------|
| **Natural maintenance developer** | Navigate unfamiliar codebases, find callers/callees, understand data flow before making a change. |
| **Modernization / migration engineer** | Build an accurate picture of cross-module dependencies and data access to plan refactors or migrations. |
| **Team lead / architect** | Assess blast radius of a change; understand module coupling. |
| **Editor/tooling integrator** | Wire the server into an editor or internal developer platform with predictable configuration. |

---

## 4. Operating context & assumptions

- The product analyzes **filesystem-based Natural sources** — the `.NSx` object files produced by
  NaturalONE / SPoD exports — not objects living only in the mainframe library system. Mainframe-only
  code must be exported to files first.
- Natural is **case-insensitive** for keywords and identifiers; analysis must normalize case for
  extraction and cross-file resolution.
- Natural module resolution follows the **steplib chain** (current library → ordered steplibs →
  system libraries), **not** file paths. The same module name can legitimately exist in multiple
  libraries; search order is what disambiguates.
- Statements may span multiple lines, and some legacy source is column-sensitive (fixed-format).
- The product supports both **structured mode** and **reporting mode** Natural to the extent each
  appears in production code.
- A workspace is rooted at a sentinel configuration file discovered by walking up from an opened
  file.

---

## 5. Product scope summary

The product comprises three deliverables:

1. **The server** — a standalone LSP server binary (the primary deliverable).
2. **A VS Code client extension** — first-party, zero-config-when-possible launch of the server.
3. **A JetBrains client integration** — first-party support for JetBrains IDEs.

Additional editors (Neovim, Zed, Helix, and any other LSP-capable client) are supported via standard
LSP configuration and documentation, but are not first-party deliverables.

---

## 6. Functional requirements

Each requirement carries a **priority**: **P0** (MVP — must ship in the first usable release),
**P1** (required for v1.0 stable), **P2** (post-v1.0 / later). Phasing is summarized in §10.

### 6.1 Workspace & project model

- **FR-1 (P0)** — Discover the workspace root by walking up from an opened file to a sentinel
  configuration file.
- **FR-2 (P0)** — Index a configurable set of Natural object file types across the workspace.
- **FR-3 (P0)** — Honor configuration for: indexed file extensions, excluded directories, and a
  maximum indexable file size.
- **FR-4 (P1)** — Support a configuration-driven **library map** that assigns workspace directories
  to Natural libraries and declares each library's steplib search order.
- **FR-5 (P1)** — When no library map is declared, treat the workspace as a single flat namespace and
  emit a diagnostic whenever a name resolves ambiguously.
- **FR-6 (P0)** — Provide sensible defaults for all configuration so the product is usable with an
  empty or minimal configuration file.

### 6.2 Object-type coverage

- **FR-7 (P0)** — Recognize and correctly classify the core indexable object types and map each to
  its construct: program, subprogram, external subroutine, copycode (include targets), map, local
  data area, global data area, parameter data area, helproutine, and DDM.
- **FR-8 (P2)** — Extend recognition to additional object types as needed (e.g. class, function,
  dialog, adapter, text), keeping the indexed extension set in sync with the features that consume
  each type.
- **FR-9 (P0)** — The set of indexed file types must remain consistent with the features that depend
  on them; adding a feature that consumes a new type requires that type to be indexed.

### 6.3 Call & dependency resolution

> This is the highest-impact area: incorrect resolution produces incorrect relationships, which
> undermines the product's core value.

- **FR-10 (P0)** — Resolve **static** module calls (literal target names) to their definitions across
  the workspace and expose them as navigable, queryable relationships.
- **FR-11 (P0)** — Identify **dynamic** module calls (variable target names) as a *modeled outcome*,
  not a failure: surface them as explicit unresolved relationships that preserve the calling context.
- **FR-12 (P0)** — Resolve subroutine invocations using correct scope order: a matching **inline**
  subroutine in the same object is resolved before falling back to an **external** subroutine of the
  same name.
- **FR-13 (P0)** — Resolve include/copycode references to their target files and track them as
  dependencies.
- **FR-14 (P1)** — Resolve static program-transfer/navigation statements (literal targets) as
  navigable relationships, distinct from module calls.
- **FR-15 (P1)** — Identify **dynamic** program-transfer statements (variable targets) as unresolved
  relationships with caller context preserved, consistent with FR-11.
- **FR-16 (P1)** — Resolve calls using the **steplib chain** ordering when a library map is present
  (current library → ordered steplibs → system), and correctly handle statements that explicitly
  target a specific library outside the normal chain.
- **FR-17 (P1)** — Correctly distinguish unresolvable references (a modeled outcome, e.g.
  `CALLNAT #VARIABLE`) from parse errors (a source-level problem); the two must be reported through
  different channels (see FR-30, FR-31).
- **FR-18 (P2)** — Account for runtime name-substitution constructs (e.g. language-dependent
  placeholders inside literal target names) so that such names are not mis-resolved to a
  non-existent target.

### 6.4 Data-access extraction

- **FR-19 (P0)** — Extract data-read relationships (read/find/get-style statements): the file/DDM
  name accessed and the read relationship.
- **FR-20 (P1)** — Extract data-write relationships (store/update/delete-style statements): the
  file/DDM name and the write relationship.
- **FR-21 (P1)** — Extract data definitions and parameter interfaces from data-definition blocks
  (local, global, parameter, and related sections).
- **FR-22 (P2)** — Extract work-file definitions.

### 6.5 Program-structure extraction

- **FR-23 (P0)** — Produce a structural model of each object identifying at minimum: the object root
  (e.g. program), its subroutines, its data sections, maps, and DDM references.

### 6.6 LSP capabilities (editor-facing features)

- **FR-24 (P0)** — **Go to definition** for module-call, program-transfer, and subroutine targets.
- **FR-25 (P0)** — **Find all references** to a subroutine, program, or DDM field across the
  workspace.
- **FR-26 (P0)** — **Workspace symbol search** by program name or subroutine.
- **FR-27 (P0)** — **Document outline** — a full symbol tree per file (data sections, subroutines,
  maps, external calls, and other structural symbols).
- **FR-28 (P1)** — **Hover** information, including: program metadata (module name, location, inbound
  call count), subroutine signatures on invocation targets, and DDM field names/types/file
  associations on data-access statements.
- **FR-29 (P2)** — **Code lens** summaries (e.g. inbound call counts, table-write summaries).
- **FR-30 (P0)** — **Syntax diagnostics** surfaced as LSP diagnostics when the parser cannot
  interpret source (parse errors), distinct from modeled unresolved references (FR-11).
- **FR-31 (P1)** — **Diagnostics** for ambiguous name resolution when operating without a library map
  (per FR-5).
- **FR-32 (P0)** — **Indexing progress reporting** during first-run/full indexing, surfaced through
  the editor's standard progress mechanism.
- **FR-47 (P1)** — **Completion** (`textDocument/completion`) — context-aware completions for
  `CALLNAT`/`PERFORM`/`INCLUDE`/`FETCH` targets (module names from the workspace index), subroutine
  names within scope, and DDM field names at data-access statements.
- **FR-48 (P1)** — **Signature help** (`textDocument/signatureHelp`) — display the parameter
  interface (PDA or inline `DEFINE DATA PARAMETER`) of a `CALLNAT` or `PERFORM` target when the
  cursor is on the call site.
- **FR-49 (P1)** — **Call hierarchy** (`textDocument/callHierarchy`) — incoming and outgoing call
  panels showing callers and callees of a program, subprogram, or subroutine, backed by the
  cross-file call graph from FR-10–16.
- **FR-50 (P2)** — **Folding ranges** (`textDocument/foldingRange`) — fold `DEFINE DATA` sections,
  `DEFINE SUBROUTINE` bodies, loops (`FOR`/`FIND`/`READ`), and `DECIDE` blocks.
- **FR-51 (P2)** — **Inlay hints** (`textDocument/inlayHint`) — inline annotations for parameter
  names at `CALLNAT`/`PERFORM` call sites and DDM field types at data-access statements.

### 6.7 Document lifecycle & freshness

- **FR-33 (P0)** — Maintain an in-memory view of open documents and keep analysis consistent as
  documents are opened, edited, and closed.
- **FR-34 (P1)** — Detect changes to workspace files made outside the editor and keep the index
  consistent with on-disk state.
- **FR-35 (P0)** — **Incremental re-analysis**: when a file changes, re-index only what is affected,
  not the whole workspace.

### 6.8 Indexing & persistence

- **FR-36 (P0)** — Build a cross-file index of the workspace on first open.
- **FR-37 (P1)** — Persist the index to an on-disk cache so subsequent startups are fast.
- **FR-38 (P1)** — Invalidate cached entries based on **file content** (not modification time), so the
  cache stays valid across version-control checkouts.
- **FR-39 (P1)** — Force a full rebuild when the cache format changes between product versions.
- **FR-40 (P1)** — The cache must be safe to delete at any time and to exclude from version control.

### 6.9 Server lifecycle & protocol

- **FR-41 (P0)** — Operate as a standard stdio LSP server: complete the initialize/shutdown
  lifecycle and advertise exactly the capabilities it supports.
- **FR-42 (P0)** — Report a version identifier on request.
- **FR-43 (P0)** — Degrade gracefully: a single malformed or unrecognized object must not abort
  indexing or crash the server.

### 6.10 Editor clients

- **FR-44 (P0)** — **VS Code client**: automatically launch the server when a Natural source file is
  opened, with no additional configuration required when the server is discoverable on the system
  path; allow overriding the server location via settings.
- **FR-45 (P1)** — **JetBrains client**: provide a first-party path to run the server in JetBrains
  IDEs (including Community editions) and associate it with Natural file types.
- **FR-46 (P1)** — **Other editors**: document a supported configuration for additional LSP-capable
  editors (at minimum Neovim, Zed, and Helix), including file-type association and workspace-root
  detection.

---

## 7. Configuration requirements

- **CR-1 (P0)** — All configuration lives in a single human-editable workspace configuration file at
  the codebase root, which also serves as the workspace-root sentinel.
- **CR-2 (P0)** — Every configurable value must have a documented default; the product must run
  without an explicit configuration file present beyond the sentinel.
- **CR-3 (P0)** — Configurable: indexed object extensions, excluded directories, maximum indexable
  file size, and cache location.
- **CR-4 (P1)** — Configurable: whether dynamic module calls are treated as unresolved external
  dependencies versus errors, and heuristics governing dynamic-call handling.
- **CR-5 (P1)** — Configurable: the library map (directory-to-library mapping and per-library steplib
  search order).
- **CR-6 (P0)** — Invalid or partially invalid configuration must produce a clear, actionable message
  and fall back to defaults where possible rather than failing to start.

---

## 8. Non-functional requirements

### 8.1 Performance & scalability

- **NFR-1 (P0)** — Cold (first) index time should scale roughly linearly with codebase size.
- **NFR-2 (P1)** — Subsequent startups (warm cache) should be sub-second regardless of codebase size,
  re-analyzing only changed files.
- **NFR-3 (P1)** — Editor-facing requests (definition, references, hover, outline, symbol search)
  should feel interactive on a large codebase once indexing is complete.
- **NFR-4 (P0)** — The product must handle enterprise-scale workspaces (tens of thousands of objects)
  without exhausting typical developer-machine memory.
- **NFR-5 (P1)** — Indexing must not block editor responsiveness; progress must be visible while it
  runs.

> Indicative cold-index targets (design goals, not guaranteed benchmarks):
> ~3s @ 500 files · ~25s @ 5,000 files · ~3min @ 30,000 files; warm startup <1s in all cases.

### 8.2 Reliability & correctness

- **NFR-6 (P0)** — No silent data loss: every statement-like line that is not extracted is either a
  modeled outcome (an unresolved relationship) or a reported diagnostic.
- **NFR-7 (P0)** — Resolution correctness is the top quality bar: a static call must resolve to the
  correct definition under the configured library/steplib semantics, and inline-before-external scope
  order must hold.
- **NFR-8 (P1)** — The cache must never serve stale results for changed content.
- **NFR-9 (P1)** — Regression fixtures: any construct found to be mishandled becomes a permanent
  test fixture once fixed.

### 8.3 Portability & compatibility

- **NFR-10 (P0)** — Distribute as native binaries for the major desktop platforms (Linux, macOS,
  Windows; common CPU architectures).
- **NFR-11 (P0)** — Conform to the LSP specification such that any compliant client can consume the
  server.
- **NFR-12 (P1)** — Provide multiple installation paths (pre-built binary, build-from-source,
  package-manager-style install).

### 8.4 Usability & observability

- **NFR-13 (P0)** — Setup for a new workspace should require only placing the sentinel file and
  installing the binary/client.
- **NFR-14 (P1)** — The product must make its own limits legible: what was indexed, what was skipped,
  what could not be resolved, and why.

### 8.5 Maintainability & extensibility

- **NFR-15 (P0)** — The extraction backend must be replaceable without changing editor-facing
  behavior — the `Analyzer` interface seam allows the hand-written parser to be replaced with a
  tree-sitter grammar or other backend as the ecosystem matures.
- **NFR-16 (P1)** — Extracted structure (calls, data access, external dependencies) must be clean and
  well-formed enough to be consumed by external tooling, not only the editor.

---

## 9. Known limitations (accepted for the first stable release)

These are explicitly acknowledged and do not block release:

- Without a declared library map, modules sharing a name across libraries cannot be disambiguated.
- Dynamic (variable-target) calls are not resolved; they are surfaced as unresolved relationships
  with calling context preserved.
- Adabas access is extracted structurally, but physical Adabas DDM metadata and IMS segment metadata
  are not resolved beyond what the source files contain.
- Preprocessor/macro and code-generation constructs may not extract correctly.
- Unusual legacy fixed-format/column-sensitive source may yield incomplete extraction rather than an
  error.

---

## 10. Phasing & priorities

### Phase 0 — MVP (P0): "usable navigation on one editor"

Deliver an installable server plus the VS Code client that can index a workspace and answer the core
navigation questions, with limits made visible.

- Workspace discovery, indexing, and core configuration with defaults (FR-1–3, FR-6, CR-1–3, CR-6).
- Core object-type recognition (FR-7, FR-9).
- Static call resolution, dynamic-call modeling, inline-vs-external subroutine scope, include
  tracking (FR-10–13, FR-17 partial).
- Read-relationship and program-structure extraction (FR-19, FR-23).
- Editor features: go-to-definition, find references, workspace symbols, document outline,
  unrecognized-syntax diagnostics, indexing progress (FR-24–27, FR-30, FR-32).
- Document lifecycle and incremental re-analysis (FR-33, FR-35).
- First-build indexing (FR-36), stdio LSP lifecycle, version reporting, graceful degradation
  (FR-41–43).
- VS Code client with zero-config launch (FR-44).
- Cross-platform binaries, LSP conformance (NFR-10, NFR-11); core correctness and no-silent-loss
  guarantees (NFR-6, NFR-7); linear cold-index scaling and enterprise-scale memory behavior
  (NFR-1, NFR-4); replaceable extraction backend (NFR-15).

### Phase 1 — v1.0 stable (P1): "trustworthy at scale, multi-editor"

Make resolution library-aware, add write/data-definition extraction and hover, persist the index,
broaden editor support, and deliver the parser-enabled interactive features.

- Library map and steplib-aware resolution; ambiguity diagnostics (FR-4, FR-5, FR-16, FR-31, CR-5).
- Navigation-statement resolution, static and dynamic (FR-14, FR-15).
- Write relationships, data-definition/parameter interfaces (FR-20, FR-21).
- Hover (FR-28).
- Completion: module names, subroutine names, DDM field names (FR-47).
- Signature help for CALLNAT/PERFORM (FR-48).
- Call hierarchy: incoming and outgoing call panels (FR-49).
- External file-change watching (FR-34).
- Persistent, content-hash-invalidated, version-gated cache (FR-37–40).
- JetBrains client and documented config for other editors (FR-45, FR-46).
- Dynamic-call configuration (CR-4).
- Warm-startup, request-latency, non-blocking-indexing, cache-freshness, and regression-fixture
  NFRs (NFR-2, NFR-3, NFR-5, NFR-8, NFR-9); installation paths and observability
  (NFR-12, NFR-14, NFR-16).

### Phase 2 — post-v1.0 (P2): "deeper coverage"

- Extended object types (FR-8).
- Code-lens summaries (FR-29).
- Work-file extraction (FR-22).
- Runtime name-substitution handling in literal targets (FR-18).
- Folding ranges (FR-50).
- Inlay hints (FR-51).

---

## 11. Success metrics

### 11.1 Adoption & outcome

- **M-1** — A new user can go from "binary installed" to "working go-to-definition in their editor"
  in a single short setup session, with only the sentinel file and client install.
- **M-2** — Used on a representative enterprise codebase, navigation and reference features cover the
  large majority of everyday call/include/data-access patterns developers actually encounter.

### 11.2 Correctness

- **M-3** — Static calls resolve to the correct definition under configured library/steplib semantics
  in a high-coverage fixture suite, with zero known false edges in that suite at release.
- **M-4** — Inline-before-external subroutine resolution holds for every fixture exercising the case.
- **M-5** — Every construct ever reported as mishandled has a permanent regression fixture; the suite
  only grows.
- **M-6** — No silent gaps: in test corpora, every non-extracted statement-like line is accounted for
  as either a modeled unresolved relationship or a reported diagnostic.

### 11.3 Performance

- **M-7** — Cold index time scales approximately linearly with file count and meets the indicative
  targets in §8.1 on reference hardware.
- **M-8** — Warm startup is sub-second across all tested codebase sizes.
- **M-9** — Core editor requests return fast enough to feel interactive on a large indexed workspace.

### 11.4 Reach

- **M-10** — The server runs on all targeted platforms and is consumable by VS Code, JetBrains, and
  at least the documented additional LSP editors without source changes.

---

## 12. Open questions

These do not block drafting but should be resolved during planning/implementation:

- Intended relationship type for literal target names containing runtime substitution placeholders
  (dynamic vs. resolved-with-wildcard).
- Whether user-defined function calls warrant a relationship type distinct from ordinary module
  calls.
- Exact handling of column/fixed-format source: how much fixed-format legacy syntax must be supported
  for the first stable release versus deferred.
- The concrete acceptance corpus (which sanitized, non-proprietary codebases) used to measure the
  coverage and correctness metrics above.