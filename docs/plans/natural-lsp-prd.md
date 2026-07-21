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
hand-written lexer + recursive-descent parser, config-driven NaturalONE-independent library mapping,
and a git-safe content-hash cache.

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
- **Not** a batch/bulk export tool — analysis is interactive and editor-driven.
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
- **FR-11 (P0)** — Handle **dynamic** module calls (variable target names) as unresolvable: note
  the call site with whatever context is available, so they appear in find-references and outline
  rather than being silently discarded.
- **FR-12 (P0)** — Resolve subroutine invocations using correct scope order: a matching **inline**
  subroutine in the same object is resolved before falling back to an **external** subroutine of the
  same name.
- **FR-13 (P0)** — Resolve include/copycode references to their target files and track them as
  dependencies.
- **FR-14 (P1)** — Resolve static program-transfer/navigation statements (literal targets) as
  navigable relationships, distinct from module calls.
- **FR-15 (P1)** — Handle **dynamic** program-transfer statements (variable targets) as unresolvable,
  consistent with FR-11.
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
  (Its on-disk encoding must be compact — see NFR-16.)

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
- **FR-52 (P1)** — The shipped JetBrains/**LSP4IJ template must be importable** — it must conform to
  LSP4IJ's user-defined-language-server template schema (`id`, `name`, `programArgs`,
  `fileTypeMappings[].fileType.patterns`/`languageId`) so "Import from custom template…" produces a
  working server, and it must be validated against that schema so it cannot silently drift.
  (Refines FR-45; shipped as feature 25.)

- **FR-53 (P1)** — **LSP-native observability & tracing** — the server must surface its operational
  activity through the standard LSP log/trace channels, mirroring the conventions of other LSP4IJ
  servers (gopls, typescript-language-server, clangd): emit **`window/logMessage`** (severity-tagged)
  for index build begin/end, cache hit vs. rebuild, per-file skips, resolution ambiguities, and request
  errors — so events appear in the LSP4IJ **Logs tab** / VS Code output channel, not only on stderr —
  and honor the **trace handshake** (`InitializeParams.trace`, the `$/setTrace` notification) by emitting
  **`$/logTrace`** for per-RPC tracing gated on the `off`/`messages`/`verbose` level (off by default).
  Emission is fire-and-forget and never blocks, fails, or panics a request (FR-43), and adds no server
  capability. (Realizes the editor-facing side of NFR-14; planned as feature 26.)

- **FR-54 (P1)** — **Variable & reference navigation** — extend go-to-definition, find-references, and
  hover to **data variables and host variables**, and complete the **binding half** of the SQL extraction
  (FR-19/FR-20/FR-21): from a variable use-site (`#CUSTOMER-NAME`, group-qualified `#GROUP.FIELD`, array
  `#T(I)`) or a SQL host variable navigate to its `DEFINE DATA` declaration and find all use-sites, and
  resolve SQL-sourced DDM table names to their `.NSD` like an Adabas view. Matching is case-insensitive,
  strips array subscripts, honors group qualification, excludes `*`-system variables, and treats
  `&`-dynamic/undeclared names as modeled gaps (empty, not errors — FR-17). Delivered in two phases:
  **Phase A** is **same-file** variable navigation (declaration and uses are intra-file); **Phase B** binds
  cross-file — variables declared in external data areas (`LOCAL/PARAMETER/GLOBAL USING
  <.NSL/.NSA/.NSG>`), SQL host variables, and SQL-sourced DDM names — through the existing steplib chain
  (reusing feature 07), closing feature 08b's deferred binding gap. It also adds
  **`textDocument/documentHighlight`** — highlighting every occurrence of the symbol under the cursor in
  the file (read/write kinds), reusing the same reference machinery. (Refines FR-24/FR-25/FR-28; planned as
  feature 27.)

- **FR-55 (P1)** — **Rich symbol detail & `VIEW OF` binding** — enrich the document-outline export
  (`textDocument/documentSymbol`) so `DEFINE DATA` fields show their **type** (`A26`, `P9,2`, `(A)
  DYNAMIC`), **level**, **array** index ranges (`(1:10)`), and **REDEFINE** overlays (incl. `FILLER nX`
  gaps) — metadata already extracted but currently dropped before the outline — and parse the **`VIEW
  OF <ddm>`** construct so a database view's fields **decode into typed logical fields**: a bare view
  field inherits its type from the DDM and go-to-definition on it reaches the `.NSD` field declaration
  (through the same DDM namespace/steplib chain as `READ`/`FIND`, reusing feature 27). The binding target
  is always the `.NSD` (which may map to Adabas or DB2); `TYPE: SQL` DDM parsing is a recorded limit.
  (Refines FR-23/FR-27, connects FR-19/FR-28; planned as feature 28.)

- **FR-56 (P1)** — **Semantic tokens** (`textDocument/semanticTokens`) — server-driven, AST-aware syntax
  highlighting, delivered in two tiers: a lexical tier (keyword/comment/string/number/operator, works on
  any file incl. unparseable) and a semantic tier that classifies identifiers by role (data **variable** /
  **parameter**, call/subroutine → **function**, DDM/view → **type**, DDM/view field → **property**,
  system variable → `defaultLibrary`), with modifiers (`declaration`/`definition`/`readonly`/
  `modification`). Computed on demand for the open buffer (no persisted state); adds a
  `semanticTokensProvider` capability with a legend. Gives real highlighting in editors that lack a Natural
  grammar (JetBrains/LSP4IJ) and consistent coloring in VS Code. (New capability; planned as feature 29.)

- **FR-57 (P2)** — **Pull diagnostics** (`textDocument/diagnostic` + `workspace/diagnostic`) — expose the
  existing diagnostics (FR-30/FR-31) through the LSP 3.17 pull model via a `diagnosticProvider`, for
  clients that prefer pull over the current push (`publishDiagnostics`). Same content, added delivery
  mechanism. (Complements feature 14; planned as feature 30.)

- **FR-58 (P2)** — **Declaration & type-definition navigation** (`textDocument/declaration`,
  `textDocument/typeDefinition`) — thin providers over existing resolution: `declaration` mirrors
  `definition` (plus variable-use → `DEFINE DATA` line), and `typeDefinition` jumps a field to the DDM that
  types it (reusing features 27/28). (Refines FR-24; planned as feature 31.)

- **FR-59 (P2)** — **Document links** (`textDocument/documentLink`) — render resolved CALLNAT/INCLUDE/
  FETCH/RUN/PERFORM targets as clickable links (largely redundant with go-to-definition; value is
  discoverability). (Planned as feature 32.)

- **FR-60 (P2)** — **Server commands** (`workspace/executeCommand`) — a command-dispatch substrate plus a
  first concrete command (reindex workspace); the enabler for future server-driven code actions. (Planned
  as feature 33.)

- **FR-61 (P2, deferred)** — **Monikers** (`textDocument/moniker`) — cross-repository symbol identities for
  LSIF/SCIP-style index interchange. **Documented non-goal for the foreseeable future** — no use case in a
  filesystem-scoped single-workspace product; revisit only alongside an index-export feature. (Decision
  record; feature 34.)

---

## 7. Configuration requirements

- **CR-1 (P0)** — All configuration lives in a single human-editable workspace configuration file at
  the codebase root, which also serves as the workspace-root sentinel.
- **CR-2 (P0)** — Every configurable value must have a documented default; the product must run
  without an explicit configuration file present beyond the sentinel.
- **CR-3 (P0)** — Configurable: indexed object extensions, excluded directories, maximum indexable
  file size, and cache location.
- **CR-4 (P1)** — Configurable: the library map (directory-to-library mapping and per-library steplib
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
- **NFR-16 (P1)** — The persistent cache must use a **compact on-disk encoding**. Cache size scales
  reasonably with workspace size (a small fraction of the current indented-JSON footprint) and does
  not consume gigabytes for tens of thousands of objects; the encoding also should not regress — and
  ideally improves — warm-start load time (NFR-2). (Planned as feature 24.)

> Indicative cold-index targets (design goals, not guaranteed benchmarks):
> ~3s @ 500 files · ~25s @ 5,000 files · ~3min @ 30,000 files; warm startup <1s in all cases.

### 8.2 Reliability & correctness

- **NFR-6 (P0)** — No silent gaps: parse errors are surfaced as diagnostics; unresolvable references
  are retained with their call site rather than discarded.
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
  what could not be resolved, and why. This legibility must reach the editor through the standard LSP
  log/trace channels (`window/logMessage`, `$/logTrace`), not only the stderr stream (see FR-53,
  feature 26).

### 8.5 Maintainability & extensibility

- **NFR-15 (P0)** — The extraction backend must be replaceable without changing editor-facing
  behavior — the `Analyzer` interface seam allows the hand-written parser to be replaced with a
  tree-sitter grammar or other backend as the ecosystem matures.

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

- Library map and steplib-aware resolution; ambiguity diagnostics (FR-4, FR-5, FR-16, FR-31, CR-4).
- Navigation-statement resolution, static and dynamic (FR-14, FR-15).
- Write relationships, data-definition/parameter interfaces (FR-20, FR-21).
- Hover (FR-28).
- Completion: module names, subroutine names, DDM field names (FR-47).
- Signature help for CALLNAT/PERFORM (FR-48).
- Call hierarchy: incoming and outgoing call panels (FR-49).
- External file-change watching (FR-34).
- Persistent, content-hash-invalidated, version-gated cache (FR-37–40); compact cache encoding (NFR-16, feature 24).
- JetBrains client and documented config for other editors (FR-45, FR-46); importable/validated LSP4IJ template (FR-52, feature 25).
- LSP-native observability & tracing: `window/logMessage` events + the `trace`/`$/setTrace`/`$/logTrace`
  handshake, mirroring other LSP4IJ servers (FR-53, NFR-14, feature 26).
- Variable & reference navigation: go-to-definition/find-references/hover for data variables and SQL
  host variables (same-file first, then cross-file `USING`/host-var/DDM binding via the steplib chain —
  completes the FR-19/20/21 binding half) (FR-54, refines FR-24/FR-25/FR-28, feature 27).
- Rich symbol detail & `VIEW OF` binding: typed/leveled/array/REDEFINE detail in the outline, and view
  fields decoded to their DDM logical fields (FR-55, refines FR-23/FR-27, feature 28).
- Semantic tokens: server-driven AST-aware highlighting (lexical + semantic identifier classification)
  (FR-56, feature 29); document highlight ships with feature 27.
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
- Pull diagnostics (FR-57, feature 30); declaration & type-definition navigation (FR-58, feature 31);
  document links (FR-59, feature 32); server commands / execute-command (FR-60, feature 33).
- Monikers (FR-61, feature 34) — documented non-goal; revisit only with an LSIF/SCIP export.

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
- **M-6** — No silent gaps: in test corpora, parse errors surface as diagnostics and unresolvable
  references are retained rather than dropped.

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