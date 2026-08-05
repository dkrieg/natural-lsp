# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

**Features 00–28 shipped, plus embedded-SQL parsing and extraction** — the parser foundation (feature 00: lexer + recursive-descent parser + AST), workspace indexing/persistent cache, call/dependency extraction (feature 06), call/dependency resolution (feature 07), Adabas data-access extraction (feature 08), and program-structure extraction (feature 09: a per-object hierarchical symbol tree) are implemented, as is embedded-SQL **parsing** (feature `00-parser-embedded-sql`: native Natural SQL + `PROCESS SQL` opaque-span into the AST, parse-only) and embedded-SQL **extraction** (feature `08b-embedded-sql-extraction`: DDM read/write edges, `CALLDBPROC` call edges, and host-var references — see the `sql.go` note below). **The LSP provider layer now spans navigation, document outline, hover, code lens, diagnostics, completion, signature help, and call hierarchy**: `textDocument/definition` (FR-24), `textDocument/references` (FR-25), and `workspace/symbol` (FR-26) shipped in feature 10, `textDocument/documentSymbol` (FR-27) shipped in feature 11, `textDocument/hover` (FR-28) shipped in feature 12, `textDocument/codeLens` (FR-29) shipped in feature 13, `textDocument/publishDiagnostics` (FR-30/FR-31) shipped in feature 14, `textDocument/completion` (FR-47) shipped in feature 16, `textDocument/signatureHelp` (FR-48) shipped in feature 17, and the three call-hierarchy methods (`textDocument/prepareCallHierarchy` + `callHierarchy/incomingCalls` + `callHierarchy/outgoingCalls`, FR-49) shipped in feature 18 — all wired and advertised; the running server builds and holds a `workspace.Index` + `ResolutionSet` and updates them incrementally (see the server note below). Feature 12 also added a `.NSD` **DDM field parser** (`internal/analysis/natural/ddm.go`) that populates `FileAnalysis.Definitions` for DDM files (see the ddm.go note below). **Feature 15 (editor clients & distribution)** ships the server in real editors — a first-party VS Code extension, a JetBrains path, documented configs for other LSP editors, and cross-platform binaries — with **no Go/`internal/model`/cache change** (see the feature-15 note below). The cross-file **resolution** of the SQL-sourced DDM/host-var references (binding them to definitions across the steplib chain) shipped in **feature 27** (see the feature-27 note below), which folds that binding together with variable/host-var go-to-definition/references and adds `textDocument/documentHighlight`. **All planned LSP providers are now wired** (navigation, outline, hover, code lens, diagnostics, completion, signature help, call hierarchy). **Feature 28 (rich symbol detail & `VIEW OF` binding)** then enriched the existing providers — a typed document outline (field type/level/array/REDEFINE detail), `VIEW OF` parsing + view→DDM binding, and view-field go-to-definition/hover that decode a view into its DDM's logical fields (see the feature-28 note below).

**Assessment (2026-07-14) — known defects and remediation plan.** An independent full-project
assessment (`docs/assessment-2026-07-14.md`: live wire probes, four specialist reviews, LSP-spec
verification) confirmed the core is sound (lifecycle/encoding/capabilities spec-correct,
robustness and the Analyzer seam PASS) but found five issues, re-planned as features **19–23**
(features 19–23 are all **shipped** — the entire assessment follow-up backlog is closed) — the
notes below record what each fixed: **(1) — FIXED by feature 19.**
`textDocument/completion` results were corrupted on the wire —
`CompletionItem.detail`/`sortText` serialized as `{}` because `protocol.Optional[T]` only
implements json/v2 `MarshalJSONTo` while the completion dispatch used stdlib `json.Marshal`;
struct-level tests missed it. Feature 19 unified all provider result marshaling on the json/v2
path and added wire-bytes tests (see the feature-19 note below). **(2) — FIXED by feature 20.**
The server ignored `InitializeParams.workspaceFolders`/`rootUri` — root came only from
`os.Getwd()` sentinel walk-up, so any client that didn't set the process cwd to the workspace got
a silently empty index. Feature 20 negotiates the root from the handshake (workspaceFolders →
rootUri → cwd fallback) and surfaces an empty/unresolved workspace via stderr + `window/showMessage`
(see the feature-20 note below). **(3) and (4) — FIXED by feature 21.** FR-32 (P0, indexing
progress) was never implemented (`progress.go` was a stub, `Build` ran with `onProgress: nil`) and
**(4)** the cold index build ran synchronously inside the `initialized` handler, blocking all
requests (NFR-5). Feature 21 runs the initial build on a background goroutine and wires
`window/workDoneProgress` create/begin/report/end (see the feature-21 note below); it also **wired
the on-disk cache into the server build path** (warm starts now real — FR-37/FR-38, partial NFR-2).
**(5) — FIXED by feature 22.** NFR-1/2/3/4 had zero benchmarks and two per-query hot spots
(`workspace/symbol` and `references` re-read every file from disk on each query; `NamesWithPrefix`
rebuilt the name index per keystroke). Feature 22 added a deterministic synthetic-corpus generator
+ a `//go:build bench` benchmark suite (`just bench`, off the gate), recorded NFR-1/2/3/4 verdicts
(measure-and-record), and landed the two fixes: an in-memory line-width table eliminating the
per-query disk sweep (~46×/~34× faster) and a cached name index (~87× / ~97,000× faster) — see the
feature-22 note below. Secondary: the README `go install` path contradicts the bare
`natural-lsp` module path in `go.mod`, and `scripts/smoke.sh` mis-resolves its no-arg default
binary — both **FIXED by feature 23** (see the feature-23 note below). **All assessment items
(1–5 + secondary) are now closed.**

**Post-release follow-ups (from real Windows usage).** After the initial release, running on Windows
surfaced a shipped bug and two improvements: **(i)** a **Windows path-separator bug** — index/resolution
keys were built with OS-native separators (backslash on Windows) but looked up forward-slash-normalized,
so go-to-definition/references/hover silently missed for any file in a subdirectory (document outline
still worked — it's served from the URI-keyed open-buffer store, which masked it). **Fixed** by a
stdlib-only `internal/paths.NormalizeKey` (`strings.ReplaceAll(rel, "\\", "/")`, **not** `filepath.ToSlash`)
routed through every key producer/consumer incl. cache load; `internal/document` no longer imports
`internal/workspace` (ADR-027). **(ii)** A **Windows CI job** (`windows-latest`, `go build`/`vet`/`test`
+ scoped integration, `-race` omitted with rationale) was added so platform bugs can't reach a release
again — CI was Linux-only, which is why (i) and the earlier CGO/`-race` release failure slipped through.
**(iii)** **Features 24 (cache-format-compaction), 25 (lsp4ij-template-validation), 26
(lsp-tracing-and-logging), 27 (variable & reference navigation), 28 (rich symbol detail & `VIEW OF`
binding), 29 (semantic tokens), 30 (pull diagnostics), 31 (declaration & type-definition
navigation), 32 (document links), 35 (semantic-tokens classifier performance — an O(n²)→O(n log n)
fix), and 36 (`DEFINE DATA … USING` data-area navigation — bug #58; cache `0.10.0`) are shipped**
(see their notes below). **The LSP capability
surface is now considered complete for v1.0** — the two remaining backlog candidates, **33
(execute-command)** and **34 (moniker)**, were evaluated and **dropped as non-goals** (their plan dirs
removed): moniker has no use case in a filesystem-scoped single-workspace product (cross-repo LSIF/SCIP
interchange only), and execute-command was a consumer-less enabler whose one concrete command (reindex) is
already covered by the content-hash cache invalidation + fsnotify watcher and the editor's built-in
"restart server". Revisit execute-command only if server-driven code actions are added, and moniker only
alongside an LSIF/SCIP exporter.

Feature 26 (lsp-tracing-and-logging) makes the server a well-behaved LSP logging/tracing citizen — its
only observability channel was previously stderr `slog` (plus feature 20's one-shot `window/showMessage`),
so nothing it did was visible inside the editor. Mirroring the conventions of other LSP4IJ servers
(gopls/typescript-language-server/clangd): it emits **`window/logMessage`** for six operational event
classes — index build **begin**/**end** (Info, with a **warm-cache-hit vs. rebuild** signal derived from a
pre-build `workspace.CacheExists` snapshot, since re-analysis counts alone can't tell a cold build from a
fully-warm one), a single build-end **aggregate per-file-skip Warning** (`N file(s) skipped: too_large=…`),
a build-end **resolution-ambiguity Warning**, and a per-request-panic **Error** — each **dual-sink**
(stderr line retained). It honors the **trace handshake**: reads `InitializeParams.trace`, handles the
inbound **`$/setTrace`** notification (unknown value ⇒ off + stderr Warn, malformed-shape params ⇒ ignored;
malformed JSON is dropped at the transport layer before reaching the handler), and emits **`$/logTrace`**
per request Call gated on the level (`off` ⇒ silent; `messages` ⇒ method+timing; `verbose` ⇒ a
byte-capped `traceSummary` at `maxTraceVerboseBytes=2048` with an elision marker). A `--log-level`
(`error|warn|info|debug`, `=` and space forms) CLI flag controls stderr `slog` verbosity, independent of
the LSP trace level (CR-6 fail-safe on bad values). Implementation: a `messageLogger` seam
(`internal/server/messagelog.go`) owning the stream + an **`atomic.Int32` trace level**; **no extra
outbound write mutex is needed** because `jsonrpc2.headerStream.Write` already serializes whole frames
(the atomic guards the only shared mutable state) — proven by a `-race` test + three fuzz targets
(`FuzzSetTraceValue`/`FuzzTraceSummary`/`FuzzLogFormat`). All emission is **fire-and-forget** (a failed
write is stderr-logged only, never blocks/fails/panics a request — FR-43). **No `internal/model` change and
no cache-format bump** (still `0.7.0` at the time) and **no new server capability** (logMessage/logTrace/setTrace are
notification-level, gated on the *client* window capability — the locked `TestInitialize` allow-list is
unchanged). Surfacing per-file skips required a small **additive, in-memory-only** workspace surface
(`workspace.Index.Skips()`/`addSkip`/`SkipRecord`, exported `workspace.CacheExists`, and two additive
`config.SkipReason` constants `SkipUnreadable`/`SkipAnalyzerPanic`) — not persisted, no cache-format change,
Analyzer seam untouched (ADR-028). FR-53, NFR-14. See `docs/plans/features/26-lsp-tracing-and-logging/`.

Feature 27 (variable & reference navigation) extends go-to-definition/find-references (FR-24/FR-25) to
**data variables and SQL host variables**, adds **`textDocument/documentHighlight`**, and **completes the
binding half of the SQL extraction** (FR-19/20/21 — closing feature 08b's deferred cross-file binding).
Delivered in two phases (T1–T9). **Phase A (same-file):** an additive `model.DataDefinition.NameRange`
(precise name-token span, mirroring `DataAccessEntry.NameRange`; parser-populated, and feeds
`SymbolDataField.SelectionRange`); a net-new **variable use-site scanner** `ExtractVariableRefs` — added to
the **Analyzer interface** (so `internal/server` calls it through the seam) — that walks the lexer token
stream (comments/strings/`DEFINE DATA` declarations/keywords/`*`-system vars excluded for free) and returns
`model.VariableRef` (in-memory only, not persisted); and the `definition`/`references`/`documentHighlight`
providers resolving a variable use (or the declaration itself, idempotent) to its `DEFINE DATA` line.
Correctness: case-insensitive, array-subscript stripping, `#GROUP.FIELD` **group-qualification**
(reconstructed from the source token left of the leaf → scoped to that level-1 group), REDEFINE sub-fields,
`*`-system/`&`-dynamic/undeclared → modeled gaps (empty, FR-17), and an unqualified name declared in >1
group → **all candidates**. **Phase B (cross-file & host-var/DDM binding):** variables from external data
areas (`LOCAL/PARAMETER/GLOBAL USING <.NSL/.NSA/.NSG>`, captured as the additive persisted
`model.DataAreaRef`), SQL **host variables** → their `DEFINE DATA` field, and SQL-sourced **DDM table
names** → their `.NSD` (same DDM namespace as Adabas) — all resolved through the existing **steplib chain**
via `workspace.ResolveDataAreaFieldLocation`/`ResolveDDMPath` (non-transitive, first-reachable-wins; the
server routes object location through these, never a raw `candidates[0]`). Two resolution kinds: locating
an *object* (data area/DDM) reuses feature 07's chain; resolving a *field within* it is a new intra-object
name→`DataDefinition` lookup. Closed the live `references.go` DDM-`targetPath` TODO. **Cache-format bump
`0.7.0` → `0.8.0`** (the first model/cache change since feature 24): persists the additive `NameRange` and
`DataAreaRefs`; resolution is recomputed from cached refs (feature 07 OQ-1). **One new capability
(`documentHighlightProvider`, `Kind: Text` — write-direction out of scope)**; otherwise extends the
existing providers. ADR-028 (chain-based server object-location) / ADR-029 (source-reconstructed group
qualification). See `docs/plans/features/27-variable-navigation/`.

Feature 28 (rich symbol detail & `VIEW OF` binding) enriches the existing providers — no new LSP
capability/method (extends `documentSymbol`, `definition`, and `hover`; the locked `TestInitialize`
allow-list is unchanged). **Phase A (typed outline, T1–T4):** `DEFINE DATA` field **type**
(`A26`/`P9,2`/`(A) DYNAMIC`), **level**, **array** index ranges (`(1:10)`/`(1:5,1:10)`/`(1:*)`, never
"OCCURS"), and **REDEFINE** overlays (incl. `FILLER nX` gaps) were **extracted but dropped** — they lived on
`model.DataDefinition` while `model.Symbol` carried only name+ranges and `document_symbols.go` left
`DocumentSymbol.Detail` nil. Phase A carries the metadata onto `model.Symbol` (additive
`Type`/`Level`/`Dimensions`/`Redefines`/`ViewOfDDM`) and composes the `Detail` **string in `internal/server`**
(OQ-1 — presentation stays on the LSP side, via `symbolDetail`; the array-dimension formatter
`formatDimensions` is now shared with hover's `renderParamType` so the two agree). A redefine sub-field
renders `"<type> REDEFINE <target>"`; a group header shows no type (nil detail). Surfacing REDEFINE required
an additive `model.DataDefinition.Redefines` and a **flatten-with-stamp** merge (OQ-A) in `data.go`
(`fieldToDefinition` stamps `Redefines` on each merged sub-field, now recursively so a REDEFINE **nested
inside a group** merges into its target sibling too); `FILLER nX` is captured as a `DataDefinition` with
`Type == "nX"`. **Phase B (`VIEW OF` binding, T5–T8b):** net-new parser recognition of `level view-name VIEW
[OF] ddm-name` (`matchesLiteral`, **OF optional, no lexer/keyword change**), with a **same-line guard** so a
malformed `VIEW`/`VIEW OF` with no ddm-name degrades to `ViewOfDDM == ""` without fabricating a binding or
swallowing the terminator (FR-17/FR-43). The view surfaces as an outline node whose detail is `VIEW OF
<ddm>`. View fields **decode into typed logical fields**: a bare view field inherits its type from the DDM
and **go-to-definition** on it reaches the `.NSD` field declaration, reusing feature 27's DDM-object steplib
resolution + a new intra-object field lookup — exposed as `workspace.ResolveDDMFieldLocation` /
`ResolveDDMFieldType` (the binding target is always the `.NSD`, which may map to Adabas or DB2; **`TYPE: SQL`
DDM parsing in `ddm.go` remains a recorded limit**). Cursor→declaration targeting is a new additive
companion `findDeclarationTarget` (`cursor.go`, use-site-first — `findCursorTarget` is unchanged); view name
→ its own `VIEW OF` line, a view-local REDEFINE sub-field → the view's own line (same-file, not the DDM). The
bare-view-field inherited type is resolved **per request** from the index in `documentSymbol`/`hover` (OQ-C,
F7-snapshot, store-first). Modeled gaps stay off the error/diagnostic channel (FR-17): a DDM outside the
chain, a `TYPE: SQL` DDM, or a view field absent from the DDM → the field still lists, navigation/inherited
type is empty, no error. Feature 28 also **populates `DataDefinition.NameRange` for DDM fields in `ddm.go`**
(inclusive-last-byte convention, ADR-008) so go-to-definition can land on the DDM field's name. **Cache-format
bump `0.8.0` → `0.9.0`** (persists `Symbol.{Type,Level,Dimensions,Redefines,ViewOfDDM}`,
`DataDefinition.{Redefines,ViewOfDDM}`, and the DDM-field `NameRange`) — a single one-time rebuild. See
`docs/plans/features/28-symbol-detail-and-view-binding/`.

Feature 29 (semantic tokens) ships a **`semanticTokensProvider`** for server-driven, AST-aware
highlighting (the biggest visible upgrade over the VS Code extension's basic TextMate grammar; JetBrains/
LSP4IJ has no Natural grammar today). Delivered as a new on-demand Analyzer-seam method
`Analyzer.SemanticTokens(path, content) []model.SemanticToken` (a **non-persisted** `model.SemanticToken{
Range, Type, Modifiers}` contract value — **no `internal/model` persisted field and no cache-format bump**,
cache stays `0.9.0`), classified in `internal/analysis/natural/semantictokens.go` and encoded to the LSP
relative 5-int stream by `internal/server/semantic_tokens.go` (encoding-aware via `position.go`, ADR-008,
both UTF-8/UTF-16 byte-exact; multi-line tokens split per line). Two tiers: **Phase A (lexical)** maps the
lexer's typed tokens (keyword/comment/string/number/operator) directly — works on any file, even
unparseable (FR-43); **Phase B (semantic)** reclassifies `TokenIdentifier` from the AST/extraction — data
variable → `variable` (PARAMETER-section → `parameter`), CALLNAT/FETCH/RUN literal target + PERFORM
subroutine → `function` (literal target overrides Phase-A `string`, OQ-C; `DEFINE SUBROUTINE` name gets
`definition`), DDM/view name → `type`, DDM/view field → `property`, `*`-system var → `variable` +
`readonly` + `defaultLibrary` (whole `*DATX` span, sigil-operator suppressed) — with `declaration` on
`DEFINE DATA` names and `modification` on write targets: DDM/view `EdgeWrites` reuse feature 27, and a
bounded statement-context detector (`:=` LHS / `MOVE … TO` dest / `COMPUTE … =` LHS) supplies the variable
write case (OQ-B). A single shared classifier (`semanticTokensPhaseB`) merges the tiers with a fixed
precedence (`function > type > property > parameter/variable > lexical`), recurses into grouped/nested
`DEFINE DATA` fields (propagating `SectionKind`), and keeps modeled gaps off every channel (FR-17/FR-18):
dynamic/`&`-placeholder targets and `INCLUDE` copycode are never `function`, a `*`-line-start comment is
never a system var, and an undeclared identifier falls back to nothing. The lexer gained `:=` as a single
operator token (no keyword change). Computed **on demand from the open buffer** (store-first, F7,
no workspace sweep, NFR-3 — the classifier was **O(n²)** as first shipped and was made **O(n log n)** by
feature 35, see its note below); the classifier lives entirely
behind the Analyzer seam so `internal/server` imports no parser internals. **Adds one new server
capability** (`semanticTokensProvider` + a fixed-order legend — types `keyword, comment, string, number,
operator, variable, parameter, function, type, property`; modifiers `declaration, definition, readonly,
modification, defaultLibrary`) — the first capability addition since feature 18 — so the locked
`TestInitialize` allow-list is extended and the legend pinned by a test. Serves **`textDocument/
semanticTokens/full`** and **`textDocument/semanticTokens/range`** (whole-token filtering);
**`full/delta`** is deferred (not advertised). `FuzzSemanticTokens` + `FuzzEncodeSemanticTokens` guard the
never-panic / `len%5==0` invariants (FR-43). See `docs/plans/features/29-semantic-tokens/`.

Feature 30 (pull diagnostics) exposes feature 14's existing diagnostics through the **LSP 3.17 pull
model** — `textDocument/diagnostic` (per-document) and `workspace/diagnostic` (workspace) — on top of a new
`diagnosticProvider` capability, **without removing push** (`publishDiagnostics` stays for push-only
clients). **Server-layer only: no `internal/model` change, no cache-format bump (stays `0.9.0`).** It
reuses feature 14's `aggregateDiagnostics`/`toProtocolDiagnostic` pipeline **verbatim** so pull content is
**byte-identical** to push. `internal/server/pull_diagnostics.go` holds `provideDocumentDiagnostic`
(returns a `RelatedFullDocumentDiagnosticReport`, `Kind:"full"`, `Items` always non-nil so the wire is
`"items":[]` never `null`, `ResultID` unset per OQ-2 — full report each time, no unchanged-report caching)
and `provideWorkspaceDiagnostic` (a **disk-free** index sweep via `workspace.Index.ForEachWithRange` +
`RangeConverter`, ADR-025; per-file `WorkspaceFullDocumentDiagnosticReport` items **sorted by URI** for
byte-stable output, clean files omitted). Both mirror feature 14's F7 snapshot + store-first + graceful
degradation: missing/unreadable/out-of-root/cold-index → an **empty full report, never an error** (FR-43);
a converter-based `toProtocolDiagnosticFromConverter` keeps the workspace path disk-free while remaining
byte-identical to the content-based `toProtocolDiagnostic` (both share `severityToProtocol`/`convertDiagnostics`).
The report types carry `MarshalJSONTo`, so the existing feature-19 `marshalResult` (`gojson`) path encodes
them — no bespoke wire assembly. **Push/pull coexistence (OQ-1):** when the client advertises pull support
(`textDocument.diagnostic`), the server **suppresses** its `publishDiagnostics` push (incl. the
clear-on-close/delete pushes) to avoid double delivery, relying on pull for those documents; push-only
clients are unchanged. Because it advertises `interFileDependencies: true` (FR-31 ambiguity is genuinely
cross-file) and suppresses push, it also sends **`workspace/diagnostic/refresh`** (fire-and-forget,
server→client, gated on the client's `workspace.diagnostics.refreshSupport`) on every index/resolution
republish — **and once after the initial cold build** — so a pull client re-pulls stale cross-file
diagnostics in unopened files. `workspace/diagnostic` honors a supplied **work-done progress token** via
feature 21's `progressReporter`; **partial-result streaming is deferred** (a single full response over the
bounded, non-re-analyzing index sweep is spec-legal). **Adds one new server capability**
(`diagnosticProvider{identifier:"natural-lsp", interFileDependencies:true, workspaceDiagnostics:true}`) — so
the locked `TestInitialize` allow-list gains one entry. `FuzzProvideDocumentDiagnostic` +
`FuzzProvideWorkspaceDiagnostic` guard the never-panic / non-nil-report invariants (FR-43). FR-57,
NFR-11. See `docs/plans/features/30-pull-diagnostics/`.

Feature 31 (declaration & type-definition navigation) wires the two sibling navigation methods
**`textDocument/declaration`** and **`textDocument/typeDefinition`** as thin providers over features
10/27/28 resolution — **server-layer only: no `internal/model` change, no cache-format bump (stays
`0.9.0`)**. **Declaration** is a **full delegation** to `provideDefinition` (approved OQ-1): Natural has no
header/impl split, so a symbol's declaration is its definition — `internal/server/declaration.go` maps
`protocol.DeclarationParams` → `DefinitionParams` (both embed `TextDocumentPositionParams`) and returns the
identical `[]protocol.Location` for call/transfer/subroutine targets, a variable use → its `DEFINE DATA`
line (feature 27), and view-field/DDM references — inheriting every FR-17 modeled gap
(dynamic/`*`-system/unresolved → `null`) for free. **Type definition**
(`internal/server/type_definition.go`) maps the cursor via feature 28's `findDeclarationTarget`; when the
target is a field inside a `VIEW OF` (`OwningView.ViewOfDDM != ""`) it resolves the DDM field via
`workspace.ResolveDDMFieldLocation` and returns a Location at the `.NSD` field's `NameRange` through the
steplib chain (a **restated** view field with an explicit scalar format still type-defines to the DDM
field — approved OQ-3). Every no-DDM-type-object case — a scalar-only variable (non-view early-return), a
view field absent from the DDM, a DDM outside the chain, a `TYPE: SQL` DDM, or no target at the cursor —
returns **`nil` (JSON `null`)**, never an error, never on the diagnostic channel (FR-17/FR-43). Both
providers mirror `provideDefinition`'s F7 `idxResMu` RLock snapshot + store-first buffer read +
encoding-aware positions (ADR-008), marshal via the feature-19 `marshalResult` (`gojson`) path with the
definition-family **`null`** empty sentinel (never `[]`), and are dispatched exactly like
`textDocument/definition`. **Adds two new server capabilities** (`declarationProvider`,
`typeDefinitionProvider`, both `Boolean(true)`) — so the locked `TestInitialize` allow-list gains exactly
two entries. `FuzzProvideDeclaration` + `FuzzProvideTypeDefinition` guard the never-panic invariant
(FR-43). FR-58 (refines FR-24). See `docs/plans/features/31-declaration-and-type-definition/`.

Feature 32 (document links) wires **`textDocument/documentLink`** — an always-visible, clickable
presentation of the same navigation go-to-definition already serves. `internal/server/document_links.go`
renders a `DocumentLink` over each **resolved** module/copycode/subroutine target span (CALLNAT / INCLUDE /
FETCH / RUN / external PERFORM) whose `Target` is the resolved object's file URI, reusing feature 06 edges +
feature 07 resolution (`ResolutionSet.Get(relPath, edge.Source)` → `IsResolved()`/`Path`) + feature 10
location conversion. **Server-layer only: no `internal/model` change, no cache-format bump (stays `0.9.0`).**
The link **Range is `edge.Source`** (the widened keyword→target span, approved OQ-A — `EdgeEntry` exposes no
target-token-only range without a model change); the `Target` is `uri.File(filepath.Join(root,
resolution.Path))` and is **range-less by protocol** (`DocumentLink.Target` carries no position — a click
opens the target file top, which is exactly why the feature is discoverability-only and redundant with the
precise-landing go-to-definition, OQ-C). **Modeled gaps produce no link (FR-17):** dynamic / unresolved /
**ambiguous** flat-namespace targets are skipped (a link to nowhere is worse than none, approved OQ-1 — no
arbitrary candidate pick), and an **inline same-file `PERFORM`** is skipped (`paths.NormalizeKey(resolution.Path)
== relPath` — a range-less link to the current doc has no navigation value; only an **external** `PERFORM` →
`.NSS` links). A pure, I/O-free `buildDocumentLinks(fa, res, relPath, root, content, enc)` helper does the
classification/assembly (sorted by `Range.Start` for a deterministic invariant), while `provideDocumentLink`
owns the URL/lock/store orchestration — mirroring `code_lens.go`'s F7 `idxResMu` RLock snapshot (released
before I/O) + store-first buffer read + encoding-aware ranges (ADR-008, UTF-8/UTF-16). Empty → **`null`**
sentinel via the feature-19 `marshalResult` (`gojson`) path (matching the `codeLens`/`definition` family).
**Adds one new server capability** (`documentLinkProvider{resolveProvider:false}` — eager targets, no
`documentLink/resolve`, OQ-1) — so the locked `TestInitialize` allow-list gains one entry.
`FuzzProvideDocumentLink` guards the never-panic invariant (FR-43). FR-59. See
`docs/plans/features/32-document-links/`.

**Features 33 (execute-command) and 34 (moniker) were dropped** (2026-08-04) after a value review — their
plan dirs were removed. **33 (execute-command)** was a consumer-less dispatch enabler: the only concrete
command it proposed (reindex workspace) is already covered by content-hash cache invalidation + the
fsnotify watcher for the routine case and by the editor's built-in "restart language server" as the manual
escape hatch, and there are no server-driven code actions to consume the substrate. **34 (moniker)** has
no use case in a filesystem-scoped single-workspace product (monikers exist for cross-repo LSIF/SCIP index
interchange, which this product does not participate in). Revisit execute-command only if code actions are
added, and moniker only alongside an LSIF/SCIP exporter. (Other capabilities still absent and NOT planned:
incremental `textDocumentSync` — a deliberate Full-sync choice; `implementation`, formatting, `inlineValue`,
`typeHierarchy`, `linkedEditingRange`, color — niche/N-A for Natural.)

Feature 35 (semantic-tokens classifier performance) fixes an **O(n²) latency cliff** in feature 29's
`Analyzer.SemanticTokens` — on the interactive hot path (`semanticTokens/full`+`/range` on the open buffer,
per open/edit). A CPU profile showed **~95% of the cost was `computeTokenRange` rescanning the file from
byte 0 for every token** (O(tokens × bytes)); the plan's originally-suspected hot spot (the merge/dedup
loop) turned out to be <3% — the `//go:build bench` `BenchmarkSemanticTokens` (Story 3) is what surfaced
the misdiagnosis. Three behavior-preserving fixes, all **byte-identical** (a checked-in golden test +
differential execution vs. `main` prove it): **(T5, the real fix)** a **line-start byte-offset table**
built once per request so `computeTokenRange` resolves each token within its own line → O(1)/token,
dropping the classifier to **O(n log n)** (8,000-line file **2.6 s → 11.7 ms, ~223×**; ns/op now scales
with token count, not its square); **(T3)** lex/parse **once** and thread the shared `*Program`+`[]Token`
into the five phases (was 7 lexes + 4 parses/request → 1+1, ~3.4× fewer allocs — a package-private
func-var seam lets a test assert the single parse); **(T2)** the O(n²) merge/dedup scans replaced by a
`seen` set + a precomputed readonly-`variable` start-position set. **Entirely behind the Analyzer seam: no
`internal/model` change, no cache-format bump (`0.9.0`), no capability/legend change, seam signature
unchanged.** `FuzzSemanticTokens`/`FuzzEncodeSemanticTokens` still guard never-panic (FR-43). NFR-3. See
`docs/plans/features/35-semantic-tokens-performance/` (`## Results` records the before/after + scaling).

Feature 36 (`DEFINE DATA … USING <data-area>` navigation) fixes real-user-reported **bug #58**:
go-to-definition (and `declaration`/`references`/`documentLink`) on the **data-area name token** of a
`DEFINE DATA {LOCAL|PARAMETER|GLOBAL} USING <name>` clause returned nothing — the name was captured as a
`model.DataAreaRef` (feature 27) but never wired as a navigable reference, so it was the one *module*
reference with no go-to-definition even though the object resolves identically to `INCLUDE`. **Fix
(bug #58's recommended approach): treat a `USING` reference as a first-class module edge.** A new
**additive** `model.EdgeUsesDataArea` kind is derived in `Analyze` — one edge per `DataAreaRef`
(`Source = ref.Range`, `TargetName = ref.Name`), appended and re-sorted into global source order (no fresh
AST walk, OQ-1). `workspace.Resolve` resolves it via the steplib chain across the data-area object
namespace (`.NSL`/`.NSA`/`.NSG`, first-reachable `ObjectLocalDataArea`→`Parameter`→`Global`, OQ-2, reusing
feature 27's resolver). Because `definition`/`references`/`documentLink` consume resolved edges
generically and a data-area object already gets a root `Structure.SelectionRange` (feature 09), all of them
work from that single edge addition with **no per-provider change**; `declaration` inherits it by delegating
to `definition` (feature 31). Modeled gaps stay off the error channel (FR-17): an unresolvable/out-of-chain
name → empty (no diagnostic), and a **flat-namespace** duplicate → `Ambiguous`. `typeDefinition` on the
`USING` name stays **empty** (a data area is a module, not a *type* — intentional, out of scope). The
`Resolve`/`ResolveInto` per-edge switches were **DRY'd into one shared `resolveEdge` helper** (OQ-5,
behavior-preserving for every existing kind — the full resolution suite stayed green). A guard the feature
added caught a **latent defect**: `providePrepareCallHierarchy` treated *any* resolved edge at the cursor as
a call site, so it built a bogus call-hierarchy item for a data area — fixed with an `isCallableEdgeKind`
gate that also correctly excludes `INCLUDE`/`READS`/`WRITES` (a data area is not callable). **Cache-format
bump `0.9.0` → `0.10.0`** (the new persisted `EdgeUsesDataArea` edges force a one-time rebuild; additive
`EdgeKind` value, no struct-shape change). No new capability. `FuzzParse` and the extraction/resolution
fuzz targets still guard never-panic (FR-43). Closes #58. See
`docs/plans/features/36-using-data-area-navigation/`.

Feature 24 (cache-format-compaction) fixes a real-user-reported bug: the on-disk workspace cache was
**~1 GB for ~7,790 files** because it was serialized as **indented** JSON (`json.MarshalIndent`) of the
full per-file `FileAnalysis`, where field-name/enum repetition and 4-space indentation dominated the
bytes. **Encoding-only change, no `internal/model` change and no persisted-shape change** — only how
`CacheFile`/`cacheEntry` become bytes. `internal/workspace/cache.go` gained two pure helpers:
`encodeCache` (compact `json.Marshal` → `gzip.NewWriterLevel(&buf, gzip.BestCompression)`) and
`decodeCache` (sniffs the gzip magic `0x1f 0x8b` → gunzip → unmarshal, else falls back to **plaintext**
JSON so a legacy/pre-24 cache still loads — FR-43). Both writers (`saveIndex`, `Save`) now emit gzip and
`Load` decodes via `decodeCache`; `cacheFormatVersion` bumped **`0.6.0` → `0.7.0`** (the first cache-format
bump since feature 09 — every intervening "still `0.6.0`" note remains correct for its own scope), so any
existing cache rebuilds once and is rewritten compact. A corrupt/truncated/valid-gzip-of-garbage/
plaintext-garbage cache routes to a cold rebuild and never panics (`TestLoad_CorruptCompressedCache`,
`TestBuildWithCache_CorruptCacheRebuilds`, `FuzzLoadCache`); the four version-bump tests that fabricated an
old cache via `strings.Replace` on plaintext bytes were migrated to build a `CacheFile{Version:"<old>"}` in
Go (gzip has no readable version substring). **Measured** on the feature-22 synthetic corpus (bench-tagged
`BenchmarkCacheSize`, off `just verify`; see the plan's `## Results`): **~118–126× smaller** (≈10 KB/object →
≈82 B/object, flat across a 50× object range — far exceeding the NFR-16 ≥10× target), warm-start **−27% to
−35%** vs the old format (NFR-2), and save time within ~2% (BestCompression / level 9 is off the request
path on the feature-21 background goroutine — approved decision OQ-2). Work is confined to
`internal/workspace` with stdlib-only additions (`bytes`, `compress/gzip`, `io`).

Feature 25 (lsp4ij-template-validation) fixes a real-user-reported bug: the shipped LSP4IJ template
(`editors/jetbrains/lsp4ij-template/template.json`) **did not import** because every field name was
invented (`serverName`/`command`/`mappings[{fileNamePatterns,languageId}]`) rather than LSP4IJ's real
schema. **No Go / `internal/model` / cache-format change** (mirrors feature 15 — editor-client + docs +
a Node test only; cache stays `0.7.0`, nothing crosses the Analyzer seam). The template is rewritten to
the externally-verified LSP4IJ schema — top-level `id`/`name`, an OS-keyed `programArgs`
(`default: "natural-lsp --stdio"` + `windows: "natural-lsp.exe --stdio"`), and
`fileTypeMappings[{fileType.{name,patterns},languageId}]` with all 15 `.NSx` patterns → `languageId`
`natural` (the misplaced inline `initializationOptions` was dropped; no sibling files are shipped — none
needed). A pure Mocha unit test (`editors/vscode/src/test/unit/template.test.ts`, no `vscode`/electron
API, no running IDE) asserts the real-schema shape, the **absence** of the old invented keys, and full
15-extension coverage; it runs in the existing `vscode-extension` CI job via the `out/test/unit/**` glob
with **no CI-yaml change** (FR-52). The three JetBrains doc surfaces (`editors/jetbrains/README.md`, the
template's `README.md`, and the root README JetBrains section) were aligned to the real field names, and
a manual GUI-import verification checklist was added (Story 1 AC3 — GUI import can't be CI-automated).

Feature 23 (distribution hardening) fixes the assessment's secondary findings and closes the
backlog. **No `internal/model` change, no cache-format bump (still `0.6.0`), and no production
logic change** — the Go changes are a mechanical module rename plus two test additions. **(a)** The
Go module was **renamed from bare `natural-lsp` to `github.com/dkrieg/natural-lsp`** (`go.mod` + all
182 internal import paths across 85 files rewritten), so the documented
`go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` now resolves (the module path
matches the install URL; a pushed release tag is the remaining prerequisite, cut via the manual
`release.yml`). The rename is rename-safe for versioning (the `-X main.version` ldflag is
package-relative, not module-qualified) and touched no workflow/justfile string; the seam guard
(`seam_test.go`) was updated to the new import prefix and still enforces the Analyzer boundary.
**(b)** `scripts/smoke.sh`'s no-arg binary resolution was fixed — it previously tested a
cwd-relative path (`[ -x natural-lsp ]`) but exec'd through PATH, giving a misleading failure when
the built binary sat in cwd but not on PATH; it now normalizes a bare cwd-local name to `./name`
before exec (else resolves via PATH, else fails accurately), guarded by a load-bearing
`//go:build integration` regression test (`cmd/natural-lsp/smoke_integration_test.go`, proven to
fail against the unfixed script). **(c)** A Homebrew tap (NFR-12 package-manager channel) is
**deferred with a recorded reason** (needs a separate tap repo + stable released binaries + a
release cadence; premature pre-v1.0) — the pre-built-binary and `go install`/from-clone paths cover
NFR-12 for now. FR-42 `--version` output shape is pinned by a tightened `TestVersionFlag`.

Feature 22 (performance & scale verification) fixes assessment defect #5 and adds the missing
performance evidence. **No `internal/model` change and no cache-format bump** (still `0.6.0`);
Analyzer seam intact. Three parts: **(a)** a deterministic, seeded synthetic-corpus generator
(`internal/workspace/corpusgen`, a normally-built test-support package emitting multi-library
`.NSx` objects with real CALLNAT/PERFORM/INCLUDE/DDM cross-refs, generated-not-committed) whose
correctness test proves the cross-refs actually *resolve*; **(b)** a `//go:build bench` benchmark
suite (`internal/workspace/bench/` + `internal/server/provider_bench_test.go`) run by a new
**`just bench`** recipe (a `BENCH_CORPUS_OBJECTS` knob scales the corpus; **excluded from `just
verify`** — the bench tag is off by default) covering cold-index scaling, peak memory, warm-start,
and interactive request latency, with NFR-1/2/3/4 verdicts **measured-and-recorded** (not absolute
CI gates) in the feature's `plan.md` `## Results` section; **(c)** two production hot-spot fixes.
**Fix 1 (ADR-025):** `workspace/symbol` and `references` previously did a per-query full-workspace
`os.ReadFile` sweep (only to convert byte columns → UTF-16 code units). That is replaced by an
**in-memory, encoding-agnostic per-file line-width table** on `workspace.Index`
(`internal/workspace/linewidth.go`, built at analyze time via `Index.PutContent`, recomputed once at
warm-cache load via `ensureLineWidths`, exposed through `Index.ForEachWithRange`/`RangeConverter`);
pure-ASCII lines retain no bytes (near-zero memory). Result: ~46×/~34× faster, ~7× less memory per
query, byte-identical output under both encodings (proven vs a disk-reading oracle incl. a
multibyte-prefixed emitted range, and by a files-deleted-after-build disk-free proof).
**In-memory-only — no cache/model/seam change.** `references` keeps a single bounded read of the
cursor's *own* document (to locate the cursor target), not a sweep. **Fix 2 (ADR-026):** the
name→`[]Candidate` index (used by completion's `NamesWithPrefix` and definition's `LookupByName`) was
rebuilt on every call; it is now **cached on `Index`** (`cachedNameIndex`, double-checked locking
under the existing `idx.mu`, invalidated in `Add` — the sole `entries` writer), making
`NamesWithPrefix` ~87× and `LookupByName` ~97,000× faster with proven-load-bearing invalidation
tests. Honest limitation recorded: benchmarks measured at ≤4k objects on one machine and
extrapolated; warm-start cost is hash-re-read + JSON-deserialization-bound (the projected wall at
tens of thousands — a future NFR-2 optimization). `workspace.Build`/`BuildWithCache` gained the
`context.Context` from feature 21 (unchanged here). ADRs 025/026 record the fixes.

Feature 21 (async indexing & work-done progress) fixes assessment defects #3 (FR-32) and #4
(NFR-5). **No `internal/model` change and no cache-format bump** (still `0.6.0`). Two behaviors:
**(a) the initial index build now runs on a background goroutine** tied to `bgCtx` (spawned in the
`initialized` handler) instead of synchronously on the serial dispatch loop, so the editor stays
responsive during a cold index — providers degrade to null/empty until the index publishes, and the
open-document store answers `documentSymbol` on live buffers meanwhile. The goroutine builds via
`buildIndex`, checks `bgCtx.Err()`, publishes `(idx,res)` atomically under `idxResMu` (F7
build-then-publish, mirroring `applyDocumentChange`), **replays open-buffer edits that arrived during
the build** into the published index (`replayOpenBuffers` — closes the OQ-B.1 window so index-backed
providers reflect mid-build edits, not just the store), then fires `reportNoUsableRoot` and the test
`indexReadyHook`. A `bgBuild sync.WaitGroup` is joined (via a deferred `bgCancel(); Wait()` ordered
before `stream.Close`) so the goroutine never writes to a closed stream and never leaks;
`workspace.Build`/`BuildWithCache` gained a `context.Context` (ADR-020) so shutdown aborts an
in-flight build mid-scan. **(b) work-done progress** is emitted when the client advertises
`window.workDoneProgress`: `internal/server/progress.go`'s `progressReporter` sends a
`window/workDoneProgress/create` request (fire-and-forget, OQ-A — response logged, not awaited, so
the serial loop never blocks) then `$/progress` begin → report(`N/M files` + percentage, clamped
`[0,100]`, omitted when total 0) → end, all sharing the `natural-lsp-index` token, wired to the
existing `workspace.Build(onProgress)` callback; a non-supporting client gets async indexing with
**no** progress messages. Progress `end` fires **before** feature-20's no-usable-root
`window/showMessage` (OQ-D). **No server capability is added** (work-done progress is gated on the
*client* capability, like `publishDiagnostics`); marshaling stays on the json/v2 path (feature 19).
Feature 21 also **wired the on-disk cache** (`BuildWithCache` with `cachePath = root/cfg.Cache.Path`)
into the server for the first time — fixing two latent `BuildWithCache` bugs (cold-start-with-cachePath
produced an empty index; the cache was never written back) so warm starts genuinely load the
content-hash-invalidated cache and re-analyze only changed files (ADR-024) — and **retired the stale
`handlers.go`/`progress.go` stubs** (Story 3). ADRs 019–024 record the decisions.
`FuzzResolveRootStart`/`FuzzNoUsableRootMessage` (feature 20) plus the workspace ctx-cancel tests and
the `progressReporter` wire tests guard the new paths. Fixtures reuse
`internal/server/testdata/roothandshake/`.

Feature 20 (workspace root handshake) fixes the assessment's defect #2. **No `internal/model`
change and no cache-format bump** (still `0.6.0`) — server-wiring + config-plumbing only, entirely
on the LSP-facing side of the Analyzer seam. Previously root/config were resolved at process
startup from `os.Getwd()` and fixed *before* the handshake; `initialize`'s
`workspaceFolders`/`rootUri` were decoded but never consulted, so any client that didn't launch
the server with cwd == workspace got a silently empty index. **The root is now negotiated from the
`initialize` request** via the pure `resolveRootStart(params, cwdFallback)` (`internal/server/root.go`):
precedence is **first `workspaceFolders` entry → `rootUri` → cwd fallback** (LSP-spec-aligned —
workspaceFolders authoritative, rootUri deprecated-but-honored; non-file/empty URIs fall through;
`uri.URI.IsFile()`/`.FsPath()` do the conversion). **`server.Run`'s signature changed** (ADR-019,
approved decision **Variant A**): it no longer takes a finished `root`/`cfg`; it takes a
`cwdFallback` and performs `config.Bootstrap(resolveRootStart(params, cwdFallback), "", logger)`
**inside the `initialize` handler**, so the sentinel walk-up (`config.FindRoot`) runs **from the
client-provided path** (a stray `.natural-lsp.toml` in the launch cwd is no longer consulted when
the client sends a root — approved OQ-4). The `document.Store`, fsnotify `Watcher`, and the
`initialized` index build are all constructed against the negotiated `hctx.root`/`hctx.cfg` (moved
out of `Run` startup; the dispatch loop stays serial so the eager→deferred construction is
race-free — concurrency review PASS). **Observable lifecycle is unchanged**: `initialize`
response shape, `positionEncoding`, `serverInfo`, and the locked `TestInitialize` capability
allow-list are byte-identical; **no capability added** (CR-6 config degradation is preserved — a
malformed `.natural-lsp.toml` logs a Warn and falls back to defaults without failing `initialize`).
**No-usable-root legibility (NFR-14):** when no root can be established (no client root + no cwd
sentinel) or the negotiated root has **no indexable files**, the server logs one actionable stderr
Warn naming the paths tried **and** sends a `window/showMessage` Warning notification (the first
`window/showMessage` sender — a unilateral server→client notification, marshaled via the json/v2
`MarshalJSONTo` path, adds no capability; emitted once at index-build time, never on a healthy
populated root). Out-of-root requests still degrade to null/empty, never an error (FR-43). The
headline regression `TestCrossWorkdirRootUri` (integration) launches the binary with cwd **outside**
the workspace + `rootUri` set and proves `textDocument/definition` resolves cross-file — the exact
assessment failure. `FuzzResolveRootStart`/`FuzzNoUsableRootMessage` guard the new entry points.
Fixtures live under `internal/server/testdata/roothandshake/`.

Feature 19 (protocol marshaling unification) fixes the assessment's defect #1 and eliminates its
systemic cause. **No `internal/model` change and no cache-format bump** (still `0.6.0`) — this is
**server-layer serialization only**. `protocol.Optional[T]`/`Nullable`/union types from
`go.lsp.dev/protocol` implement **only** the json/v2 `MarshalJSONTo`, not stdlib `MarshalJSON`, so
stdlib `json.Marshal` silently serializes them to `{}`; the completion dispatch hit exactly this
(`CompletionItem.detail`/`sortText` → `{}` on the wire). The fix routes **all** provider result
marshaling through a single `marshalResult` helper (`server.go`) that calls **`gojson.Marshal`**
(go-json-experiment) — replacing the stdlib `json.Marshal` calls in the seven previously-affected
cases (definition/references/workspace-symbol/documentSymbol/hover/codeLens/completion) and the
three call-hierarchy cases; signatureHelp stays on its correct `(*protocol.SignatureHelp).MarshalJSONTo`
path. `encoding/json` is fully removed from `server.go`. **Each method's empty-result sentinel is
preserved byte-for-byte** via the pre-existing explicit nil-guard branches (`null` for
definition/references/documentSymbol/hover/codeLens; `[]` for workspace-symbol/completion and the
three call-hierarchy methods) — a load-bearing detail, since `gojson.Marshal(nilSlice)` yields `[]`
where stdlib yields `null`, so the guards (not the marshaler) are what keep the sentinels correct.
The struct-vs-wire test blind spot is closed: new **wire-bytes tests** assert on emitted JSON via the
actual dispatch marshaler (`marshalResult` / `MarshalJSONTo`) — completion (`detail`/`sortText` are
strings, never `{}`; inline-vs-external `SortText` `0…`/`1…` ordering), signatureHelp
(`activeParameter` set-and-omitted, `label` union string), and call hierarchy (`data` object,
`fromRanges` arrays, empty → `[]`) — plus `dispatchResultBytes`-driven empty-result pins that catch a
nil-guard flip in either direction. A guard test (`marshal_guard_test.go`,
`TestNoStdlibJSONMarshalForResults`) fails if stdlib `json.Marshal` is reintroduced for a protocol
result in `internal/server` (runs under `just verify`). Fixtures reuse the existing
`testdata/{completion,signaturehelp,callhierarchy}/`.

Feature 18 (call hierarchy) wires the **three call-hierarchy methods** (`textDocument/prepareCallHierarchy`,
`callHierarchy/incomingCalls`, `callHierarchy/outgoingCalls`, FR-49) — the LAST planned provider. **No
`internal/model` change and no cache-format bump** (still `0.6.0`) — read-only queries over the existing
`Edges`/`ResolutionSet`/`Structure`. `internal/server/call_hierarchy.go` holds the three providers plus
`buildCallHierarchyItem` and `resolveItemIdentity`: the symbol identity (`{path,name,kind}`) is carried in
`CallHierarchyItem.Data` (an `LSPAny` union) via the shared `mustLSPAny` helper and recovered on the
follow-up requests by decoding `Data` (with a URI+`SelectionRange` fallback for clients that drop it).
**prepare** maps a cursor to a callable symbol — a call site → the resolved callee (module, or an inline
`PERFORM`'s same-object subroutine), or a definition name → its own object/subroutine symbol; an
**ambiguous** call-site emits **one item per candidate** (approved decision); dynamic/unresolved/
non-callable → empty. **incomingCalls** does a reverse sweep over the index reusing feature-10's
`edgeMatchesTarget` (so dynamic/unresolved sites are excluded — FR-11/FR-17), **grouped by caller file**
with per-call-site `fromRanges`. **outgoingCalls** walks the item's own edges for resolved
CALLNAT/external-`PERFORM`/`FETCH`/`RUN`, grouped by callee with `fromRanges`; an **inline `PERFORM`** →
an outgoing call to the same-object subroutine's item (Story 3 AC4); **`INCLUDE` copycode is excluded**
(approved decision — compile-time, not a runtime call), as are dynamic edges and data-access reads/writes.
The `callHierarchyProvider` capability (`Boolean(true)`) is advertised in `initialize` and pinned in the
locked `TestInitialize` allow-list. **Marshaling:** results are written via **`gojson.Marshal`**
(go-json-experiment, which honors the element `MarshalJSONTo` and the `Data` union), NOT stdlib
`json.Marshal`; empty results marshal to `[]` (never `null`). Incoming calls reflect incremental
re-analysis (feature-10 `applyDocumentChange`/`ResolveInto`) via the F7 snapshot, with no restart (Story 2
AC4). To give inline subroutines a precise anchor, the parser now captures the `DEFINE SUBROUTINE` name
token (`ast.Subroutine.NameRange`) and `structure.go` uses it for the subroutine `model.Symbol.SelectionRange`
(previously a zero-width range) — an analyzer refinement that also sharpens document outline / go-to-
definition, with no model/cache-format change. `FuzzProvidePrepareCallHierarchy`,
`FuzzProvideIncomingCalls`, and `FuzzProvideOutgoingCalls` guard the entry points (never panic on garbage
`Data`/positions — FR-43). Fixtures live under `internal/server/testdata/callhierarchy/`.

Feature 17 (signature help) wires the **`textDocument/signatureHelp`** provider (FR-48) — at a CALLNAT
or PERFORM site it shows the callee's parameter interface in the editor's signature UI. **No
`internal/model` change and no cache-format bump** (still `0.6.0`): it is a read-only query over the
existing `Definitions`/resolution, reusing hover's parameter extraction. `internal/server/signature_help.go`
holds the provider `provideSignatureHelp` (F7 snapshot of `idx`/`res`/`posEncoding`/`root` under `RLock`,
released before I/O; **store-first** buffer read since it fires while typing) plus a pure
`detectSignatureContext(line, cursorByteCol)` — a line scanner (sibling to feature 16's
`detectCompletionContext`) that classifies **only CALLNAT and PERFORM** as signature contexts and returns
the 0-based **argument index** (Natural has no parentheses — arguments are space-separated after the
target). Dispatch: `detectSignatureContext` → `enclosingCallEdge` (locates the CALLNAT/PERFORM
`EdgeEntry` on the cursor line so signature help fires when the cursor is in the **argument region**, not
only on the target) → resolve via `res.Get` → `buildSignatureInformation` from the callee's PARAMETER
`Definitions`. **CALLNAT** → the resolved subprogram's PARAMETER block; **PERFORM** → inline
`DEFINE SUBROUTINE` before external `.NSS` (FR-12; an inline subroutine has no PARAMETER block — shared
scope — so it yields an empty-`Parameters` signature). A subroutine with no parameters returns a non-nil
empty signature (Story 2 AC4). `activeParameter` tracks the cursor's argument position, **clamped** to the
last parameter past the end (never crashes) and omitted for param-less signatures. **Modeled gaps stay off
the diagnostic/error channel (FR-17):** dynamic/unresolved/ambiguous targets, **FETCH/RUN** targets
(programs have no declared parameter interface — they read the Natural stack via `INPUT`, natural-expert-
verified, so signature help there would fabricate metadata), and any non-call context all return **`nil`
(JSON `null`)**, no error, no diagnostic. Feature 17 **extracted a shared parameter-interface helper**
(`parameterInterface`/`renderParamType` in `hover.go`) that both hover and signature help consume so they
agree on the parameter set (hover output stays byte-identical). The `signatureHelpProvider` capability
(`TriggerCharacters: [" "]`, `RetriggerCharacters: [" "]` — approved decision) is advertised in
`initialize` and pinned in the locked `TestInitialize` allow-list. **Marshaling note:** unlike the other
providers (stdlib `json.Marshal`), the `SignatureHelp` result is written via
`(*protocol.SignatureHelp).MarshalJSONTo` because it carries `Nullable`/union fields (`ActiveParameter
Nullable[uint32]`, `ParameterInformation.Label` union); `Nullable[uint32]` (no exported constructor) is
built via its public `UnmarshalJSONFrom` — **no `unsafe`**. `FuzzProvideSignatureHelp` and
`FuzzSignatureContext` guard the entry points (never panic — FR-43). Fixtures live under
`internal/server/testdata/signaturehelp/`.

Feature 16 (completion) wires the **`textDocument/completion`** provider (FR-47) — context-aware
symbol-name completion drawn from the live workspace index. It is the server's first request provider
that **enumerates the index by prefix** (all prior lookups were exact-name). **No `internal/model`
change and no cache-format bump** (still `0.6.0`): completion is a read-only query over the existing
index/resolution, plus one additive `internal/workspace` surface. `internal/server/completion.go` holds
the provider and a pure **context detector** — `detectCompletionContext(line, cursorByteCol)` — a
line-prefix scanner (deliberately **not** `findCursorTarget`, which only maps to an *existing* extracted
edge) that classifies a partial, possibly-unparseable line by its leading keyword: `CALLNAT`→subprogram,
`FETCH`/`RUN`→program, `INCLUDE`→copycode (module contexts), `PERFORM`→subroutine, a data-access verb
(`READ`/`FIND`/`GET`/`STORE`/`UPDATE`/`DELETE`) + named view→DDM-field context (carrying the view name),
else none — returning the partial prefix (leading quote stripped, sigil preserved for dynamic detection).
`provideCompletion` snapshots `idx`/`posEncoding` under the `idxResMu` RLock and releases before I/O (F7),
reads the **open buffer store-first** (completion fires while typing, so the buffer is the only source of
the partial line), and dispatches: **module** contexts query the new `Index.NamesWithPrefix(prefix, typ,
referencingPath, cfg)` (index.go — an additive steplib-aware prefix enumeration reusing
`buildNameIndex`/`objectIdentity`/`buildSearchChain`, returning all chain-reachable candidates, flat
namespace when no library map), building `CompletionItem`s with an object-type→kind mapping
(subprogram→Module, program→File, copycode→Reference, external-subroutine→Function, DDM field→Field) and
the object-type/field-type label as `Detail`; **PERFORM** offers inline `DEFINE SUBROUTINE` candidates
from the buffer's `Structure` first, then external `.NSS` via the index, ordering enforced client-side via
`SortText` (`"0"` inline / `"1"` external); **DDM-field** resolves the view via `LookupByName(ObjectDDM)`
and offers its `Definitions` fields (verbatim `Type` as detail). Modeled gaps stay off the error/diagnostic
channel (FR-17/FR-18): a dynamic/variable target (`#`/`&`/`+` sigil) and an unresolved/ambiguous/nil-field
DDM both yield an **empty (non-nil) list, never an error** — as does any unrecognized context. The
`completionProvider` capability (`TriggerCharacters: [" "]`, `ResolveProvider: false` — eager detail, no
`completionItem/resolve`) is added to the `initialize` allow-list and the locked `TestInitialize` set.
Completion tracks incremental re-analysis (feature-10 `applyDocumentChange`/`ResolveInto`), so a
newly-added module appears without a restart. A refactor added an additive `Candidate.Name` field
(populated where the name is already computed) so the server consumes it rather than re-deriving object
names from paths (keeping name-derivation on the workspace side of the Analyzer seam).
`FuzzProvideCompletion` and `FuzzCompletionContext` guard the entry points (never panic — FR-43). Fixtures
live under `internal/server/testdata/completion/`.

Feature 15 (editor clients & distribution) gets the server in front of users in their editors — the
first feature with **no Go, `internal/model`, or cache-format change** (the server's existing `--stdio`
/ `--version` / `--init` CLI and the `just release` pipeline already sufficed; this feature adds clients,
docs, and a CI job around them). Covers FR-44/FR-45/FR-46 and NFR-10/NFR-12/NFR-13. The **VS Code
extension** lives in-repo at `editors/vscode/` (TypeScript, `vscode-languageclient`): on opening any
Natural file it launches `natural-lsp --stdio` over stdio, **zero-config when the binary is on `PATH`**,
overridable via the `naturalLsp.serverPath` setting (pure, host-free resolver in `src/serverPath.ts`);
a missing binary surfaces an actionable notification rather than crashing activation. It contributes the
`natural` language for **all 15** `.NSx` extensions (both an `extensions` list and case-insensitive
`filenamePatterns`, since mainframe exports are upper-case) plus a **basic TextMate grammar**
(`syntaxes/natural.tmLanguage.json`: keywords, `*`/`**` and `/*` comments, string/numeric literals,
aligned to the lexer). It is validated by a **full test harness** — pure Mocha unit tests
(`resolveServerPath`, grammar scopes via `vscode-textmate`) plus `@vscode/test-electron` integration
tests (activation, 15-type association, live server launch reaching `Running`) — and **packaged to a
`.vsix` via `vsce`** (`publisher: dkrieg`, version tracks the server release line; **no Marketplace
publish**). A **separate `vscode-extension` Node job** in `.github/workflows/ci.yml` runs the harness in
CI (builds the server, `npm ci`/compile/lint/unit, then the electron suite under `xvfb-run`); **`just
verify` stays Go-only**. **JetBrains** support is a reproducible LSP4IJ path (works in Community
editions) documented in `editors/jetbrains/README.md` with an importable server template
(`editors/jetbrains/lsp4ij-template/template.json`) covering all 15 file types. **Neovim/Zed/Helix** are
documented in the root README (Neovim gained the previously-missing `.NSx`→`natural` filetype
association; Zed honestly notes a full binding needs a Zed language extension). A **distribution smoke
check** (`scripts/smoke.sh`) verifies `--version` + an `initialize→…→exit` stdio round-trip against a
fresh binary; NFR-12's package-manager channel (Homebrew/Scoop) is documented as **future work**. A
sample workspace lives under `docs/plans/features/15-editor-clients/sample-workspace/`.

Feature 14 (diagnostics) wires **`textDocument/publishDiagnostics`** (FR-30 parse-error diagnostics,
FR-31 ambiguous-resolution diagnostics) — the server's first outbound-notification provider. It is
**push-based** with **no server capability** (publishDiagnostics is a unilateral server→client
notification; the `initialize` allow-list and its `TestInitialize` lock are unchanged). Both diagnostic
*producers* already existed: the parser's ranged `Program.Diagnostics` copied into
`FileAnalysis.Diagnostics`, and the resolver's flat-namespace ambiguity warnings via
`ResolutionSet.DiagnosticsFor(path)`; feature 14 is the aggregation + publishing layer that consumes
them. `internal/server/diagnostics.go` holds the pure pipeline: `severityToProtocol`
(`info`/`warning`/`error` → protocol 1/2/3, unknown → Information), `toProtocolDiagnostic` (encoding-aware
via the shared `toProtocolRange`, `Message` verbatim, `Source="natural-lsp"`, `Code` mapped to
`protocol.String`), `aggregateDiagnostics` (merges both channels into a **fresh** slice — never aliasing
the store/index-owned `FileAnalysis.Diagnostics` backing array — stable-sorted by `Range.Start`, dedups
the `(Message,Severity,Code,Range)` tuple, returns nil when empty), the `publishDiagnostics` notification
writer (empty/nil diagnostics marshal to `"diagnostics":[]` — a **full replace**, so clearing is
publishing an empty array, never a delta; threads the open-doc `Version` when store-served), and the
`publishFileDiagnostics` orchestrator (F7: snapshots `idx`/`res`/`posEncoding`/`root` once under `RLock`,
releases before all I/O; **store-first** then index/disk; missing/unreadable/out-of-root → publish `[]`
to clear; nil-`res`/nil-`store` safe). The four lifecycle handlers in `server.go` publish via a
`publishDiag` closure: `didOpen`/`didChange` (after `applyDocumentChange`, so the publish reflects the
post-change `(idx,res)` snapshot) republish the URI; `didClose` and watched-file **Deleted** events
publish `[]` (clear on close/delete — an approval-checkpoint decision); watched **Changed/Created**
events republish. **Modeled gaps stay off the diagnostic channel (FR-17):** dynamic/no-target references
are `Unresolved` outcomes, never diagnostics — only the parser and ambiguity channels are read.
`FuzzDiagnosticConversion` guards the converter (never panics — FR-43). This feature added one
**additive** `internal/model` member — `Diagnostic.Code` (a `model.DiagnosticCode` category, constants
`DiagnosticCodeSyntax`/`DiagnosticCodeAmbiguity`) stamped at both producers to make ambiguity diagnostics
machine-distinguishable from syntax ones (Story 2 AC3) — with **no cache-format bump** (still `0.6.0`:
`Diagnostics` is not persisted in the cache, so the field rides an in-memory-only slice). Fixtures live
under `internal/server/testdata/diagnostics/`.

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
(`Message`, `Severity`, a positional `Range` — added in feature 00 — and a `Code` category
distinguishing `syntax` from `ambiguity` diagnostics, added in feature 14), and
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
— a deliberately locked allow-list enforced by `TestInitialize`. (Feature 14's `textDocument/publishDiagnostics`
adds **no** capability — it is a unilateral server→client notification, so the allow-list is unchanged.)
Graceful degradation (FR-43): oversized files are skipped with
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

Module is `github.com/dkrieg/natural-lsp` (`go.mod`), targeting Go 1.26 — the module path matches
the repository, so `go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` resolves once a
release tag is pushed (feature 23 renamed the module from the former bare `natural-lsp`).

Task runner is **`just`** (install: `brew install just`; `just --list` shows all recipes). The same
gate — **`just verify`** — runs in the pre-push hook, in `/finalize-feature`, and in CI, so a local
pass means CI should pass.

```bash
just verify                                 # full gate: gofmt + vet + build + unit (-race) + integration (same as CI)
just test                                   # unit tests with the race detector
just test-integration                       # integration tests (builds binary, runs the `integration` tag)
just bench                                  # performance benchmarks (`//go:build bench`; NOT in `just verify`; BENCH_CORPUS_OBJECTS scales the corpus)
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