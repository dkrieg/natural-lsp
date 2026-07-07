# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

**Features 00–13 shipped, plus embedded-SQL parsing and extraction** — the parser foundation (feature 00: lexer + recursive-descent parser + AST), workspace indexing/persistent cache, call/dependency extraction (feature 06), call/dependency resolution (feature 07), Adabas data-access extraction (feature 08), and program-structure extraction (feature 09: a per-object hierarchical symbol tree) are implemented, as is embedded-SQL **parsing** (feature `00-parser-embedded-sql`: native Natural SQL + `PROCESS SQL` opaque-span into the AST, parse-only) and embedded-SQL **extraction** (feature `08b-embedded-sql-extraction`: DDM read/write edges, `CALLDBPROC` call edges, and host-var references — see the `sql.go` note below). **The LSP provider layer now spans navigation, document outline, hover, and code lens**: `textDocument/definition` (FR-24), `textDocument/references` (FR-25), and `workspace/symbol` (FR-26) shipped in feature 10, `textDocument/documentSymbol` (FR-27) shipped in feature 11, `textDocument/hover` (FR-28) shipped in feature 12, and `textDocument/codeLens` (FR-29) shipped in feature 13 — all wired and advertised; the running server builds and holds a `workspace.Index` + `ResolutionSet` and updates them incrementally (see the server note below). Feature 12 also added a `.NSD` **DDM field parser** (`internal/analysis/natural/ddm.go`) that populates `FileAnalysis.Definitions` for DDM files (see the ddm.go note below). What remains as extraction follow-up is cross-file **resolution** of the SQL-sourced DDM/host-var references (binding them to definitions across the steplib chain). The remaining higher-level LSP providers (completion, signature help, call hierarchy) remain unwired.

Feature 13 (code lens) wires the **`textDocument/codeLens`** provider (FR-29) — server-side wiring only,
**no `internal/model` change and no cache-format bump** (stays `0.6.0`). `internal/server/code_lens.go`
holds the provider `provideCodeLens` plus pure builders: `buildCallCountLens` (an inbound-reference
count via `referenceSites`, pluralized `"N references"` title, rendered even at zero) and
`buildWriteSummaryLens` (distinct named `EdgeWrites` DDM/view targets, deduped/sorted, **skipping the
empty-`Name` record-form gap** — FR-17, never fabricates a table name; returns nil when there are no
named writes). Both anchor at the object root `Structure.SelectionRange` (single line) and carry a
`Command` with id `editor.action.showReferences` and arguments `[uri, position, []Location]` (the VS
Code / gopls convention — a documented client dependency) so activating a lens reveals the call/write
sites (find-references behavior). Command arguments are marshaled to `protocol.LSPAny`/`jsontext.Value`
via the shared `mustLSPAny` helper. `provideCodeLens` gates on the config toggle
(`Analysis.EnableCodeLens`, default **on**; disable via `enable_code_lens = false`), serves the
open-document buffer first (live edits) then the index, snapshots `idx`/`res` under `RLock` released
before I/O (F7), and returns `nil` on missing/unreadable/no-`Structure` targets (FR-43). The count
tracks incremental re-analysis via the feature-10 `applyDocumentChange`/`ResolveInto` path. Resolution
is **eager** (`CodeLensProvider{resolveProvider:false}` — no `codeLens/resolve` handler). `FuzzProvideCodeLens`
guards the provider (never panics — FR-43). Fixtures live under `internal/server/testdata/codelens/`.

`internal/config` is fully implemented (feature 01): workspace-root discovery (`.natural-lsp.toml`
sentinel walk-up), config loading with decode-onto-defaults semantics, per-field validation with CR-6
fail-safe (bad value → default + actionable `Problem`, never crash), directory-exclusion predicate
(`IsExcluded`), skip-reason surface (`SkipReason`), library-map parsing (declared order preserved),
analysis-options parsing, custom extension-type mapping (`[extension_types]` table), and a `Sample()`
generator for `--init`. Default indexed set: all 15 Natural extensions (10 core + 5 extended; see below).

`internal/model` and `internal/analysis/natural` have object-type recognition (feature 02):
`model.ObjectType` (16 constants with stable string values), `model.Diagnostic`
(`Message`, `Severity`, and a positional `Range` — added in feature 00), and
`model.FileAnalysis.ObjectType`/`Diagnostics`/`AST` fields. The `analysis/natural` backend classifies
every file by extension (case-insensitive, custom-mapping-aware) via `Analyze(path, content)`.
Regression fixtures for all 15 types live under `testdata/objecttype/`.

`internal/analysis/natural` has a hand-written **lexer** (`lexer.go`) and **recursive-descent parser**
(`parser.go`) producing a real **AST** (`ast.go`) — feature 00, the foundation for all extraction
features. The lexer normalizes case, lexes Natural identifiers (incl. `#`/`&` prefixes and embedded
hyphens) as single tokens, handles `*`/`**` full-line comments (line-start only) and `/*` rest-of-line
comments, string/numeric literals, operators, and treats `\r\n` as one line terminator. The parser is
**error-recovering**: it parses `CALLNAT`/`PERFORM`/`INCLUDE`/`FETCH [REPEAT|RETURN]`/`RUN`/`READ`/`STORE`,
`DEFINE DATA` (level numbers, types/formats, array dimensions, `REDEFINE`, group nesting), `DEFINE
SUBROUTINE`, and `DEFINE MAP` into AST nodes carrying real source positions, and emits ranged syntax
diagnostics (`Program.Diagnostics`) for malformed statements while retaining valid surrounding ones (no
silent gaps — FR-30/M-6). It also parses native **embedded SQL** (feature `00-parser-embedded-sql`) —
`SELECT`/`SELECT SINGLE`, `INSERT`, SQL-form `UPDATE`/`DELETE`, `MERGE`, `COMMIT`, `ROLLBACK`,
`CALLDBPROC`, and `READ RESULT SET` — into AST nodes, accepting BOTH the structured (`END-SELECT`/
`END-RESULT`) and reporting-mode (`LOOP`) loop terminators, disambiguating SQL-form `UPDATE`/`DELETE`
from their Adabas record forms by clause shape (`SET`/`WHERE`/`FROM` table ⇒ SQL; else Adabas), and
emitting ranged diagnostics for malformed/unterminated SQL. `PROCESS SQL` is captured with its `<<…>>`
flexible-SQL body held as a single **opaque, unparsed span** (`TokenSQLOpaque` via the lexer's
`ScanOpaqueSpan`/`ScanOpaqueSpanFrom`); native host-vars are accepted with or without the leading colon
and stored without it. This layer is **parse-only**: SQL nodes expose *unbound* `OperandRef` lists
(columns, tables, host-vars) with no `internal/model` or cache-format change. Edge extraction, DDM
edges, and host-var references (including scanning the opaque `<<…>>` body) are implemented on top of
this AST by feature `08b-embedded-sql-extraction` (see the `sql.go` note below); cross-file *resolution*
of those references remains future work. `Analyze` surfaces the parsed `*Program` as `FileAnalysis.AST`
and copies the parser's ranged
diagnostics into `FileAnalysis.Diagnostics`. A `FuzzParse` target guards the parser entry point (never
panics, always returns a non-nil `*Program`). Fixtures live under `testdata/parser/` (SQL fixtures
`09`–`19`).

`internal/analysis/natural/calls.go` implements **call/dependency extraction** (feature 06):
`extractEdges(*Program)` walks the AST and emits `model.EdgeEntry` values into `FileAnalysis.Edges` (wired
in by `Analyze`). Per-construct edge kinds: `CALLNAT` → `EdgeCalls` (literal) / `EdgeCallsDynamic`
(variable); `PERFORM` → `EdgePerforms`; `INCLUDE` → `EdgeIncludes`; `FETCH`/`RUN` → `EdgeNavigatesTo`
(literal) / `EdgeNavigatesToDynamic` (variable). Two modeled gaps are never silent and never become
diagnostics: variable targets become *dynamic* edges with caller context preserved, and a literal target
containing an `&` runtime-substitution placeholder (e.g. `CALLNAT 'PRG&LANG'`) is **downgraded to the
dynamic kind** rather than producing a false static edge (FR-18; CALLNAT/FETCH/RUN only — INCLUDE
copycode is compile-time and excluded). An inline `PERFORM` target that matches a same-object `DEFINE
SUBROUTINE` carries that definition's range in `EdgeEntry.Target` (else the zero range — cross-file
binding is deferred to the resolution feature). A `RUN program-id library-id` records the library
qualifier on `EdgeEntry.Library` (FETCH has no source-level library — its `operand2` is a stack
parameter, not a library). Edges are returned in global source order (stable sort on `Source.Start`).
This feature added two purely-additive `internal/model` members (`EdgeNavigatesToDynamic` and
`EdgeEntry.Library`); persisting `Library` bumped the cache-format version (`0.2.0` → `0.3.0`). Parse
errors continue to flow through `Program.Diagnostics`/`FileAnalysis.Diagnostics`, keeping the
edge/diagnostic channels separate (FR-17/M-6); extraction over a partial/malformed AST never panics and
retains the edges it could extract (FR-43). Fixtures live under `testdata/calls/`.

`internal/server/` implements the LSP lifecycle (feature 03): `Run(ctx, r, w, version, root, cfg, az,
logger)` serves JSON-RPC 2.0 over `Content-Length`-framed stdio (`go.lsp.dev/jsonrpc2` v1.0.0). The
server enforces the `initialize → initialized → shutdown → exit` lifecycle; the `initialize` response
advertises `textDocumentSync: Full`, `positionEncoding` (UTF-8 preferred, UTF-16 default — ADR-008), and
the `definitionProvider`, `referencesProvider`, and `workspaceSymbolProvider` capabilities (feature 10)
plus the `documentSymbolProvider` capability (feature 11), the `hoverProvider` capability (feature 12),
and the `codeLensProvider` capability (feature 13, `resolveProvider: false`)
— a deliberately locked allow-list enforced by `TestInitialize`. Graceful degradation (FR-43): oversized files are skipped with
`SkipTooLarge`, excluded paths with `SkipExcluded`, unrecognized extensions processed as `ObjectUnknown`,
and analyzer panics are recovered per-file without aborting the batch — every skip/recovery is logged to
stderr. Per-request panic recovery returns a JSON-RPC `InternalError` and keeps the loop alive. SIGTERM
is handled via a context-watcher goroutine that closes the stream to unblock the blocking bufio reader.
A `FuzzProcessFile` target guards the file-processing entry point (ADR-013). Feature 04 added
`textDocument/didOpen`, `textDocument/didChange` (Full-sync; partial-change attempts are logged and
skipped), `textDocument/didClose`, and `workspace/didChangeWatchedFiles` handlers. After `initialized`,
the server sends `client/registerCapability` for `workspace/didChangeWatchedFiles` when the client
advertises `Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration`. A background `fsnotify`
watcher (`document.NewWatcher`) is started at `initialized` and closed on shutdown.

Feature 10 (navigation & symbol search) wires the **first real LSP providers** into the server and, for
the first time, makes the running process build and hold a `workspace.Index` (feature 05's index was a
package that was never constructed by the server until now). At `initialized` the server builds the index
via `workspace.Build` and computes a `workspace.ResolutionSet` (`Resolve`), holding both on a
`handlerContext` guarded by an `RWMutex`; on `didChange`/watched-file changes, `applyDocumentChange`
re-analyzes, `idx.Add`s, and swaps in a freshly-recomputed resolution set (`workspace.ResolveInto`, the
scoped/incremental recompute) under the write lock — build-then-publish, so provider reads that snapshot
the `(idx, res)` pointers under the read lock are never racing an in-place mutation (F7). The three
providers live in new files: `position.go` (encoding-aware `model`↔`protocol` position/range conversion —
model is 1-based with byte-offset columns and inclusive-end ranges; protocol is 0-based, code-unit-counted
per the negotiated encoding, end-exclusive; ADR-008), `cursor.go` (`findCursorTarget` maps a cursor to the
`EdgeEntry`/`DataAccessEntry` reference site under it, smallest-containing-range tie-break), `definition.go`
(`textDocument/definition`: cursor → resolution → target `Location`; a module target lands on the object
root's `SelectionRange`, an inline `PERFORM` on the matching `DEFINE SUBROUTINE` child; dynamic/unresolved →
empty per FR-17; a flat-namespace ambiguity returns all candidate `Location`s), `references.go`
(`textDocument/references`: a reverse sweep over the index binding each edge to its resolved target, plus
DDM references matched by name; dynamic/unresolved sites are excluded, never falsely linked), and
`workspace_symbols.go` (`workspace/symbol`: case-insensitive name search over each file's `Structure`,
returning object roots and subroutines as `SymbolInformation`). This feature made **no `internal/model`
change and no cache-format bump** (still `0.6.0`) — server capabilities/wiring only. The feature-06 `PERFORM`
edge `Source` was widened to span through the target name so a cursor on the target resolves to the edge.
`FuzzPositionConversion`, `FuzzCursorLookup`, and `FuzzProvideDefinition` guard the primitives (FR-43).

Feature 11 (document outline) adds the **`textDocument/documentSymbol`** provider (FR-27) — provider
wiring only, rendering feature 09's `FileAnalysis.Structure *model.Symbol` tree as a hierarchical LSP
`DocumentSymbol[]`. `internal/server/document_symbols.go` holds a pure recursive converter
(`symbolToDocumentSymbol`): it maps each `model.SymbolKind` to a `protocol.SymbolKind`
(`SymbolObject→Module`, `SymbolSubroutine→Function`, `SymbolMap→Object`, `SymbolDataSection→Namespace`,
`SymbolDataField→Field`, `SymbolDDMReference→Struct`, unknown→`Object` — never drops a node, FR-43),
converts `Range`/`SelectionRange` via `toProtocolRange` (ADR-008), and recurses into `Children` in the
tree's source order. The `provideDocumentSymbols` handler serves the **open-document buffer first**
(`document.Store.Get` → the current, possibly-unsaved `Analysis.Structure`, so the outline tracks live
edits — Story 2), falling back to the on-disk index (`idx` snapshot under `RLock`, released before
`os.ReadFile` — F7) only when the document is not open; a missing/nil/unreadable target returns
`nil, nil` (empty outline, no error). This feature made **no `internal/model` change and no cache-format
bump** (still `0.6.0`) — server capabilities/wiring only. It also relocated data-section symbol-name
uppercasing into `structure.go` (aligning section names with the model's uppercase name convention),
shared the URI→relPath logic across providers via a `uriToRelPath` helper, and deleted the stale,
misplaced `internal/analysis/natural/symbols.go` package-doc stub (the `model.Symbol`→`protocol`
conversion is LSP-facing and lives in `internal/server/`). `FuzzDocumentSymbols` guards the converter
against panics over degenerate trees and both encodings (FR-43).

Feature 12 (hover) wires the **`textDocument/hover`** provider (FR-28) and, unplanned, adds a `.NSD` **DDM
field parser** so the DDM hover card can show real fields (an approved scope change — see `tasks.md`).
`internal/server/hover.go` holds the provider plus pure Markdown card builders: `buildModuleHover` (module
name + object-type label + relative path + inbound-call count via reused `referenceSites` + a single
outbound-dependency count restricted to calls/performs/includes), `buildSubroutineHover` (parameter
interface from the target's `SectionKind == "parameter"` `Definitions`, with array dims and group nesting),
`buildDDMHover` (view name + field list from the indexed DDM's `Definitions`, else an honest
"unavailable" line), and the modeled-gap builders `buildUnresolvedHover`/`buildDynamicHover`/
`buildAmbiguousHover` (distinct, no fabricated metadata — FR-17). `provideHover` snapshots `idx`/`res` under
`RLock` and releases before I/O (F7), maps the cursor via `findCursorTarget`, and dispatches on the
resolution outcome: a resolved module → module card; a `PERFORM` (inline same-file or external `.NSS`) →
subroutine signature; a data-access DDM ref → DDM card (name-matched, `NameRange` highlighted); dynamic/
unresolved/ambiguous → the honest message; empty-`Name` record-form sites and no-target/unreadable →
`nil` (→ JSON `null`). The DDM parser (`internal/analysis/natural/ddm.go`, wired into `Analyze` via an
early-return for `ObjectDDM`) is a dedicated **fixed-byte-offset line-scanner** for the exported DDM report
format (T@0/L@2/DB@4/Name@7/F@41/Leng@43; verified in `.claude/knowledge/natural/ddm-format.md`): it emits
`model.DataDefinition` values with verbatim `Type` (`N8`/`A50`/`P9,2`), group nesting by level containment,
and a single unbounded `*` `Dimensions` entry for MU (`T=M`) and PE (`T=P`) fields; it skips `TYPE: SQL`
DDMs, comment/header/terminator lines, and tolerates short/malformed rows without panicking (DB
short-name/descriptor/suppression columns are dropped — not needed for name/type hover; a DDM's `Structure`
stays nil, a documented deferral). Feature 12 also widened the CALLNAT/FETCH/RUN edge `Source` through the
target name (parity with feature 10's PERFORM widening) so a cursor on the target resolves to the edge.
**No `internal/model` change and no cache-format bump** (still `0.6.0`) — `Definitions` already persists;
DDM fields ride the existing shape. `FuzzProvideHover` and `FuzzParseDDM` guard the new entry points
(never panic — FR-43). Fixtures live under `internal/server/testdata/hover/` and
`internal/analysis/natural/testdata/ddm/`.

`internal/document/` (feature 04) is fully implemented. `Store` is a concurrency-safe in-memory map of
open documents keyed by LSP URI; it re-analyzes content on `Open`/`Update` via an `AnalyzeFunc`
injection (avoiding circular import with `internal/server`) and removes entries on `Close`, with panic
recovery on every analysis call (FR-43). `Watcher` uses `fsnotify` v1.10.1 for recursive workspace
watching — `filepath.WalkDir` + per-directory `Add`, extension filtering, and a 100 ms trailing-edge
debounce — with per-call panic recovery. `internal/workspace/` implements cross-file indexing (index.go) and persistent cache with content-hash invalidation (cache.go).

`internal/workspace/resolution.go` implements **call/dependency resolution** (feature 07): `Resolve(idx,
cfg)` walks every file's `FileAnalysis.Edges` (from feature 06) and binds each reference to a definition,
returning a `ResolutionSet` keyed by (referencing file, edge `Source`) whose outcomes are `Resolved(path,
ObjectType)` / `Unresolved(reason)` / `Ambiguous(candidates)`. Resolution follows Natural's **steplib
chain** — current library → declared steplibs (in declared order) → implicit `SYSTEM`, **non-transitive**
(a steplib's own steplibs are not followed; verified against Software AG docs), first library with a
matching object wins; a candidate outside the caller's chain is unreachable and never resolved. The
"current library" of a file is the longest-prefix match of its path against `config.Library.Path`; a file
under no declared path (or with no library map) resolves in a **flat namespace** (single match → resolved;
>1 match → `Ambiguous` + a warning diagnostic; 0 → unresolved). An explicit `RUN 'pgm' 'lib'` library-id
(`EdgeEntry.Library`) resolves against that one library only, bypassing the chain. `PERFORM` resolves an
inline `DEFINE SUBROUTINE` before an external `.NSS`. Per-kind target types: CALLNAT→subprogram,
external PERFORM→external-subroutine, FETCH/RUN→program, INCLUDE→copycode. Modeled gaps stay distinct from
parser diagnostics (FR-17): dynamic/`&`-placeholder targets are `Unresolved(dynamic)` (never bound, never
a diagnostic); an unresolvable literal is `Unresolved(no-target)` (also not a diagnostic); only a
flat-namespace ambiguity produces a resolution diagnostic, exposed via `ResolutionSet.DiagnosticsFor` (the
server merges it into the referencing file's `publishDiagnostics`) — resolution never mutates the index.
Per **ADR/OQ-1** this feature makes **no `internal/model` or cache-format change**: resolution is
recomputed from cached `Edges` on load. `index.go` gained `LookupByName`/`buildNameIndex`/`Candidate` for
name→definition lookup, and `Index.Invalidate` was migrated from a name-vs-path string compare to
resolved object-name matching (uppercased copycode name vs `EdgeIncludes.TargetName`, transitive). A
`FuzzResolve` target guards the resolver (never panics, always returns a non-nil `*ResolutionSet`).
Fixtures live under `internal/workspace/testdata/resolution/`. Feature 10 added
`ResolveInto(rs, idx, cfg, changedPaths)` — a scoped/incremental recompute that returns a **fresh**
`ResolutionSet` merging re-resolved changed files plus their affected callers (files whose edge
`TargetName` matches an object added-to/removed-from a changed file — a definition change affects callers,
not just INCLUDE dependents) into a copy of the prior set, leaving the input set immutable (build-then-
publish, F7). Its completeness invariant — the merged set equals a full `Resolve` for the mutated index —
is asserted against `Resolve` in tests. `Resolve` remains the initial-build path; still no cache-format
change (recomputed on load, OQ-1).

`internal/analysis/natural/data.go` implements **Adabas data-access extraction** (feature 08), wired into
`Analyze` alongside `extractEdges`. `extractDataAccess(*Program)` emits `model.DataAccessEntry` values:
`READ`/`FIND`/`GET` → `EdgeReads`, `STORE` + record-form `UPDATE`/`DELETE` → `EdgeWrites`. Each entry
carries the normalized (upper-case) view/DDM `Name`, a `NameRange` spanning just the view-name token, and
a whole-statement `Source` range. Two modeled gaps stay off the diagnostic channel (FR-17): `GET SAME` has
no view operand → no edge; record-form `UPDATE`/`DELETE` have no source-level file operand (they write the
record of the enclosing `READ`/`FIND`/`GET` loop) → a write edge with **empty `Name`** recording the site,
with file binding deferred (OQ-4). `extractDefinitions(*Program)` emits `model.DataDefinition` values from
each `DEFINE DATA` section (`LOCAL`/`PARAMETER`/`GLOBAL`/`LINKAGE`/`INDEPENDENT`/`CONTEXT`/`OBJECT`) with
level, verbatim type/format, array `Dimensions`, `SectionKind`, source `Range`, and nested `Children`
(REDEFINE subfields merged into the target field); the `PARAMETER` section is distinguishable via
`SectionKind` so it can back a subroutine/module signature. Variable identifiers keep their sigil
(`#`/`&`/`@`, and `+`-prefixed AIV names in `INDEPENDENT` sections — captured context-sensitively in
data-field position so arithmetic `+` is unaffected). `extractWorkFiles(*Program)` emits `model.WorkFile`
values from `DEFINE WORK FILE` (number + name; a variable/dynamic name is recorded verbatim as a modeled
gap). The parser was widened for this feature: `FIND`/`GET` (incl. `GET SAME`), Adabas record-form
`UPDATE`/`DELETE` (disambiguated from the SQL forms by clause shape — `SET`/`WHERE`/`FROM` table ⇒ SQL),
`DEFINE WORK FILE`, and per-keyword `DataSection.Kind` (one `DataSection` per section keyword). Scope is
Adabas-style data access only; embedded-SQL data-access extraction (native SQL tables as DDMs, host
vars) is implemented separately in `sql.go` (feature `08b-embedded-sql-extraction`, described next). New
`internal/model` members: `DataAccessEntry.Name`/`NameRange` (the former `File` field was renamed to
`Name`), `DataDefinition`, `ArrayDimension`, `WorkFile`, and `FileAnalysis.Definitions`/`WorkFiles`.
Persisting `Definitions` and `WorkFiles` (and the `DataAccessEntry` name/range) bumped the cache-format
version (`0.3.0` → `0.4.0`). Fixtures live under `internal/analysis/natural/testdata/dataaccess/` and
parser fixtures `20`–`27`.

`internal/analysis/natural/sql.go` implements **embedded-SQL extraction** (feature 08b), wired into
`Analyze` after the feature-06/08 extractors (SQL results are appended to `FileAnalysis.Edges`/`DataAccess`
and each combined slice is re-sorted into global source order). It consumes the SQL AST from feature
`00-parser-embedded-sql` (parse-only, unbound operands). `extractSQLAccess(*Program)` reuses feature 08's
read/write edge model verbatim: `SELECT`/`SELECT SINGLE` `FROM` tables and the `READ RESULT SET` site →
`model.EdgeReads`; `INSERT`/SQL-`UPDATE`/SQL-`DELETE`/`MERGE` target tables → `model.EdgeWrites`; a
`PROCESS SQL` `ddm-name` → one `EdgeReads` (neutral read-style access per OQ-3 — the opaque `<<…>>` body is
never scanned for table names, so in-body table names stay pass-through text; M-6). A native-SQL table
operand is a `.NSD` DDM name (same namespace as Adabas). `READ RESULT SET` records an **empty-`Name`** read
site (the result-set handle is not a DDM — binding deferred), following feature 08's empty-Name precedent.
`extractSQLCalls(*Program)` emits a `CALLDBPROC` proc name as `model.EdgeCalls` (literal) /
`EdgeCallsDynamic` (variable or `&`-placeholder literal), matching feature 06's CALLNAT downgrade rule.
`extractHostVarRefs(*Program)` emits the new `model.HostVarRef{Name, Range}` for host variables: in native
clauses (`INTO`/`WHERE`/`VALUES`/`SET`) a host var binds whether written bare or colon-prefixed (identified
by the parser's `OperandRef.HostVar` colon flag or a Natural sigil — columns, operators, and literals never
leak in); inside a `PROCESS SQL` opaque body, `scanOpaqueHostVars` string-scans the raw body for the
**colon-mandatory** `:host-var` form, stripping `:U:`/`:G:`/`:T:` qualifiers, `INDICATOR`/`LINDICATOR`
prefixes, and array subscripts, computing ranges from the body offset. The parser was widened for this
feature: `OperandRef.HostVar` (preserving the native host-var colon signal across `SELECT`/`UPDATE`/`DELETE`
WHERE, `VALUES`, and `SET`), `MergeStatement.Table` (the `MERGE INTO` target operand), and
`CallDBProcStatement.ProcName`/`ProcNameRange`/`ProcNameIsLiteral`. New `internal/model` member
`FileAnalysis.HostVarRefs` (host-var *use* references, contrast `DataDefinition` declarations); persisting
it bumped the cache-format version (`0.4.0` → `0.5.0`). Modeled gaps stay off the diagnostic channel
(FR-17) and binding SQL-sourced DDM/host-var references to declarations (resolution) is future work. A
`FuzzExtractSQL` target guards the extraction entry points (never panics — FR-43). Fixtures live under
`internal/analysis/natural/testdata/sqlaccess/`.

`internal/analysis/natural/structure.go` implements **program-structure extraction** (feature 09), wired
into `Analyze` (`result.Structure = extractStructure(path, ast, result.Definitions, result.DataAccess)`)
after all other extractors run. `extractStructure` builds a per-object, kind-tagged, walkable
`model.Symbol` tree — the backbone for document outline, workspace symbols, and hover (features 10/11).
The root is a `SymbolObject` (name = the file's base name without extension; the `Program` AST node has no
name); its children, deterministically source-ordered (`sort.SliceStable` on `Range.Start`), are:
`SymbolDataSection` per `DEFINE DATA` section (kind as name) with `SymbolDataField` children reusing
feature 08's `model.DataDefinition` tree (level, arrays, REDEFINE nesting) — **fields are matched to
their section by source-range containment**, not by section-kind string, so two same-kind sections (e.g.
two `LOCAL` blocks) keep their own fields; `SymbolSubroutine` per `DEFINE SUBROUTINE`; `SymbolMap` per
`DEFINE MAP` (with its fields); and `SymbolDDMReference` per **named** `FileAnalysis.DataAccess` entry
(READ/FIND/GET/STORE + SQL tables), skipping the empty-`Name` record-form gap (feature 08 OQ-4). Each
`model.Symbol` carries `Range` (whole construct) and `SelectionRange` (name token, always contained in
`Range`), mirroring LSP `DocumentSymbol`. New `internal/model` members: the recursive
`model.Symbol{Kind, Name, Range, SelectionRange, Children}`, six `SymbolKind` constants
(`object`/`subroutine`/`data-section`/`data-field`/`map`/`ddm-reference` — stable cache keys; the
pre-existing flat `SymbolEntry`/`SymbolProgram` stub is untouched), and `FileAnalysis.Structure *Symbol`;
persisting `Structure` bumped the cache-format version (`0.5.0` → `0.6.0`). The parser was widened for this
feature — `parseMap` now parses map fields via level-based nesting and accepts quoted/unquoted names, and
both `parseMap`/`parseSubroutine` accept the hyphenated `END-MAP`/`END-SUBROUTINE` and the two-token
`END MAP`/`END SUBROUTINE` terminators (via token-type-agnostic `matchesLiteral`, so **no lexer/keyword
change**). Extraction emits no diagnostics (FR-17): a partial/malformed object still yields structure for
the recognized parts while the parser's ranged diagnostics stay on `FileAnalysis.Diagnostics`. Deferred:
inline-vs-external subroutine distinction, type-specific members for helproutines/classes/functions,
`INCLUDE` copycode as a structure node. A `FuzzExtractStructure` target guards the entry point (never
panics over partial ASTs — FR-43). Fixtures live under `internal/analysis/natural/testdata/structure/`.

`natural-lsp` is a Go-based Language Server Protocol server for **Software AG Natural**, a 4GL widely deployed on IBM
z/OS mainframes. It uses a hand-written lexer + recursive-descent parser (modeled on
[natls](https://github.com/MarkusAmshove/natls), Java/MIT) to index a Natural codebase and serve navigation, completion,
references, hover, call hierarchy, document outline, and workspace symbols to any LSP-capable editor.

## Commands

Module is `natural-lsp` (`go.mod`), targeting Go 1.26. Note the README's `go install` path uses
`github.com/dkrieg/natural-lsp/cmd/natural-lsp` — reconcile the module path before publishing.

Task runner is **`just`** (install: `brew install just`; `just --list` shows all recipes). The same
gate — **`just verify`** — runs in the pre-push hook, in `/finalize-feature`, and in CI, so a local
pass means CI should pass.

```bash
just verify                                 # full gate: gofmt + vet + build + unit (-race) + integration (same as CI)
just test                                   # unit tests with the race detector
just test-integration                       # integration tests (builds binary, runs the `integration` tag)
just build                                  # build the server binary
just install-hooks                          # enable the pre-push hook (runs `just verify` before every push)
just release vX.Y.Z                          # cross-build all platforms into dist/ (releases are cut via the manual Release workflow)

# Underlying go commands, for ad-hoc use:
go build -o natural-lsp ./cmd/natural-lsp   # build the binary
go test -run TestName ./internal/analysis/natural   # single test
./natural-lsp --stdio < /dev/null           # smoke test: serves the LSP initialize handshake on empty input, then exits cleanly on EOF
./natural-lsp --init                        # write a fully-commented sample .natural-lsp.toml to stdout
./natural-lsp --version                     # print version and exit
```

## Development workflow

Product features go through a lifecycle, each phase driven by a slash command (defined under
`.claude/`): `/plan-feature` → `/implement-feature` → `/review-feature` ⇄ `/address-findings` →
`/finalize-feature`. Each feature is a directory under `docs/plans/features/<feature>/` holding
`plan.md` (the spec — user stories + acceptance criteria) and `tasks.md` (the planner's decomposition).
To run the whole chain in one go, use **`/ship-feature <feature>`** — it pauses once for plan approval,
then implements, reviews and remediates to a `PASS`, and opens the PR for you to merge.

- **Feature branches, never `main`.** Implement every product feature on a `feat/<feature>` branch off
  `main` — the task plan (`tasks.md`), the code, and the doc updates all live on that branch. Do not
  commit feature code directly to `main`. (Repo-infrastructure changes — `.claude/` tooling, CI, chores
  — are exempt and may go straight to `main`.) A reviewed feature lands via a **pull request that a
  human merges**: `/finalize-feature` opens the PR and stops there. After the PR merges, delete the
  branch and return to `main`.
- **Review is a loop.** If `/review-feature` returns `FAIL` (or `CONCERNS` worth addressing), run
  `/address-findings` — each finding becomes a regression-first fix through the TDD loop — and re-review
  until the verdict is `PASS`. Only a clean `PASS` unlocks `/finalize-feature`.
- **Docs track as-built.** By the time a feature merges, `CLAUDE.md` and `README.md` must already match
  what shipped — the "Project state" note below, the command list, and the architecture/feature set.
  `/finalize-feature` performs that sync before opening the PR, and the `review-docs` reviewer flags
  drift during `/review-feature`. Keep the "Project state" note current as each feature lands.

## Architecture

A single binary (`cmd/natural-lsp`) runs as a stdio LSP server. The intended package boundaries:

- `internal/model/` — the shared output contract (`model.go`: `ObjectType`, `Diagnostic`, `FileAnalysis`); consumed by
  analysis, workspace, and server; free of backend internals.
- `internal/server/` — LSP lifecycle and request dispatch (`textDocument/*`, `workspace/*`), work-done progress.
- `internal/document/` — in-memory document store (didOpen/didChange/didClose) and the workspace file watcher.
- `internal/workspace/` — the cross-file symbol table (`index.go`) and its on-disk cache (`cache.go`).
- `internal/analysis/` — `analyzer.go` defines the **Analyzer interface**; `analysis/natural/` is the parser-based
  implementation (lexer, recursive-descent parser, AST, call/data/SQL extraction, program-structure extraction
  (hierarchical symbol tree), and the `.NSD` DDM field line-scanner (`ddm.go`)). The LSP-facing hover card
  builders live in `internal/server/hover.go`, not here (presentation side of the Analyzer seam).

**The Analyzer interface is the key seam.** The parser backend sits behind it so it can evolve (e.g. to a tree-sitter
grammar) without touching the LSP layer. Keep LSP-facing code depending only on the interface, never on parser internals.

## Design decisions that constrain implementation

These are deliberate and easy to get wrong — read the README's "Parser-based extraction" and "Workspace
configuration" sections before changing related code.

- **Hand-written parser, not regex.** A lexer + recursive-descent parser for Natural, using
  [natls](https://github.com/MarkusAmshove/natls) as the reference implementation. This enables accurate symbol tables,
  real syntax diagnostics, completion, signature help, and call hierarchy — features that require a proper AST. Two
  failure modes are still modeled *separately* and neither is dropped silently:
  - *Unresolvable references* (e.g. `CALLNAT #VARIABLE`) are noted as unresolvable with the call site preserved — they
    appear in find-references and outline rather than disappearing.
  - *Parse errors* are surfaced as LSP **diagnostics** so they are visible in the editor, not silently discarded.

- **Module resolution follows Natural's steplib chain, not file paths.** `CALLNAT` / `PERFORM` / `FETCH` / `RUN` targets
  resolve current-library → steplibs (in declared order) → SYSTEM. The chain is **non-transitive** (a steplib's own
  steplibs are not followed — verified against Software AG docs); the first library in the chain with a matching object
  wins, and a candidate in a library outside the caller's chain is unreachable. The same module name can exist in
  multiple libraries; search order is what disambiguates. Library mapping is config-driven (`[resolution]` in
  `.natural-lsp.toml`; the current library is the longest-prefix path match). An explicit `RUN 'pgm' 'lib'` library-id
  resolves against that one library only, bypassing the chain (`FETCH` has no source-level library qualifier). With no
  library map — or for a file under no declared library path — fall back to a single flat namespace and emit an
  ambiguity diagnostic (not a silent pick) on a name matching more than one object. Do not assume globally-unique names.
  (Implemented in `internal/workspace/resolution.go`, feature 07.)

- **Filesystem-scoped to NaturalONE / SPoD `.NSx` files.** The server operates on exported object files, not objects
  living only in the mainframe Natural/Adabas library system. Each extension maps to a construct and several features
  depend on indexing the right ones: `.NSP` program, `.NSN` subprogram, `.NSS` external subroutine, `.NSC` copycode
  (INCLUDE targets), `.NSM` map, `.NSL`/`.NSG`/`.NSA` data areas, `.NSH` helproutine, `.NSD` DDM. Extended types:
  `.NS4` class, `.NS7` function, `.NS3` dialog, `.NS8` adapter, `.NST` text. All 15 are in the default indexed set.
  Keep the indexed extension set in sync with the features that consume them.

- **Natural is case-insensitive** for keywords and identifiers — the lexer must normalize case. Statements can span
  multiple lines; the parser must handle continuation correctly.

- **Workspace root** is located by walking up for a `.natural-lsp.toml` sentinel. The index is cached under
  `.natural-lsp-cache/`; invalidate on **content hash** (not mtime, which breaks across git checkouts) and force a full
  rebuild when the cache-format version changes.

## Testing convention

When the analyzer mishandles a construct: add a minimal reproducer `.NSP` (or relevant `.NSx`) under `testdata/`, write
a failing unit test in `internal/analysis/natural/`, then fix the analyzer. The testdata file stays as a permanent
regression fixture. Use only sanitized, non-proprietary Natural code.

Standalone LSP server usable with any LSP editor.