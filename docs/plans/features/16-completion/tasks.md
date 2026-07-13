# Tasks: Completion (`textDocument/completion`)

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements:** FR-47 (context-aware symbol-name completion), FR-43 (graceful
degradation / never-panic), FR-17/FR-18 (modeled gaps: dynamic targets, unresolved DDMs →
empty completions, never diagnostics), FR-10..16 (steplib resolution order — reused, not
reimplemented).

This feature adds the server's **first request-serving provider that must enumerate the index
by prefix** (all prior name lookups were exact-name). The core new logic is a **completion
context detector** that classifies a partial, possibly-unparseable line — deliberately NOT
`findCursorTarget`, which only maps a cursor to an *existing* extracted edge.

---

## Current-state findings & impact

Surveyed `internal/server/{server.go,server_test.go,code_lens.go,hover.go,cursor.go,position.go}`,
`internal/workspace/{index.go,resolution.go}`, `internal/model/model.go`, and the protocol package
`go.lsp.dev/protocol@v1.0.0`. Findings, and how the plan pivots to them:

- **Provider wiring pattern is fixed and repeatable.** Each provider is a `case
  "textDocument/..."` in the dispatch switch in `server.go` (definition ~655, references ~687,
  workspace/symbol ~719, documentSymbol ~747, hover ~779, codeLens ~811) calling a
  `provideXxx(hctx, params)` function in its own file. Completion follows this exactly: a new
  `internal/server/completion.go` + a `case "textDocument/completion"` gated on
  `stateInitialized`, decoding `protocol.CompletionParams`, marshaling `[]protocol.CompletionItem`
  (or `[]` when empty — never `null`, because an empty completion list is a valid, non-error
  response and clients treat `null` inconsistently).

- **Capability advertisement is a locked allow-list.** `initialize` (`server.go` ~139-149)
  advertises a deliberately minimal capability set enforced by `TestInitialize`
  (`server_test.go` ~528-541, a `requiredProviders` list). Unlike every prior provider
  (all `protocol.Boolean(true)` or the codeLens options object), completion needs
  `CompletionProvider *protocol.CompletionOptions` with `TriggerCharacters` and/or
  `ResolveProvider`. **Adding it REQUIRES extending the `TestInitialize` lock** — that
  extension is an explicit DoD item on the capability task, not incidental.

- **`protocol` completion types confirmed present** (`completion.gen.go`): `CompletionParams`
  (embeds `TextDocumentPositionParams`), `CompletionItem{Label, Kind CompletionItemKind, Detail
  Optional[string], SortText Optional[string], ...}`, and `CompletionOptions{TriggerCharacters
  []string, ResolveProvider *bool}`. Kind constants exist for `CompletionItemKindModule` (9),
  `CompletionItemKindFunction` (3), `CompletionItemKindField` (5), `CompletionItemKindFile` (17),
  `CompletionItemKindReference` (18) — enough to distinguish object-type kinds (Story 1 AC4).

- **Index has EXACT-name lookup only — no prefix/enumeration.** `Index.LookupByName(name, typ,
  cfg) []Candidate` and `buildNameIndex(cfg) map[string][]Candidate` (`index.go`) key on the
  *exact* uppercased filename stem via `objectIdentity`. Completion needs "all reachable objects
  of type T whose name starts with prefix P". **This is a new, additive `internal/workspace`
  method** (T2) — a query over existing `FileAnalysis`/`Candidate` data. **No `internal/model`
  change and no cache-format bump** (still `0.6.0`): completion is a read-only query over the
  already-indexed data, exactly like feature 10/11/13 (all made no model/cache change).

- **Steplib chain logic must be REUSED, not reimplemented.** `resolution.go` holds
  `objectIdentity` (current-library longest-prefix match), `buildSearchChain` (non-transitive:
  current lib → declared steplibs in order → SYSTEM; empty ⇒ flat namespace), and
  `resolveViaChain`. These are unexported package-internal helpers. Completion's candidate
  filtering (Story 1 AC2/AC3) lives **inside `internal/workspace`** so it can call them directly
  — the new prefix method (T2) returns candidates already filtered to the caller's reachable
  chain, mirroring `resolveByName`'s Mode-2/Mode-3 branching. The server never re-derives chain
  logic; it passes the referencing file's relPath + cfg and receives reachable candidates.

- **F7 snapshot discipline is universal.** `handlerContext` guards `idx`/`res`/`posEncoding`
  under `idxResMu sync.RWMutex`; every provider (`provideCodeLens` is the clearest model,
  ~219-224) snapshots the pointers under `RLock`, releases, then does I/O. `applyDocumentChange`
  swaps fresh `(idx, res)` under the write lock (build-then-publish). Completion follows this;
  because it reads the live `idx`, Story 1 AC5 ("newly added module appears without restart")
  holds for free — the incremental `idx.Add` + `ResolveInto` path already refreshes what the
  prefix query sees.

- **Store-first pattern for the active buffer.** Hover/codeLens/documentSymbol serve
  `document.Store.Get(uri)` (current unsaved edits) before the on-disk index. Completion fires
  *while typing*, so the open buffer is the ONLY source of the partial line under the cursor —
  the store-first read is mandatory here, not just preferred. The prefix scan reads
  `doc.Content` around `params.Position`.

- **Position/line access exists.** `position.go` has `lineAt(content, line)`,
  `fromProtocolPosition(p, content, enc)` (protocol 0-based → model 1-based byte-column), and
  `toProtocolRange`. The context detector uses `fromProtocolPosition` + `lineAt` to get the
  raw line prefix up to the cursor's byte column.

- **`findCursorTarget` (`cursor.go`) is the WRONG tool for context.** It matches a cursor to an
  already-extracted `EdgeEntry.Source`/`DataAccessEntry.NameRange`. During completion the token
  is partial (`CALLNAT MYSU|`) and typically has NOT parsed into a valid edge, so
  `findCursorTarget` returns nil. The context detector (T1) is therefore a **new, lightweight
  line-prefix scanner**: it reads the text before the cursor on the current logical line,
  finds the nearest preceding statement keyword, and classifies the completion context +
  extracts the partial prefix already typed. A prefix scan (not a re-parse) is chosen because it
  is robust to incomplete/unparseable input — the exact situation completion runs in. It is the
  single hardest piece and gets its own RED tests + fixtures.

- **DDM fields for Story 3 live in `FileAnalysis.Definitions`.** `internal/analysis/natural/ddm.go`
  populates a `.NSD`'s `Definitions []model.DataDefinition` with `Name` and verbatim `Type`
  (`N8`, `A50`, `P9,2`). A resolved data-access `EdgeReads`/`EdgeWrites` entry names the DDM
  (`DataAccessEntry.Name`); field completion looks that DDM up in the index by name (reusing
  `LookupByName(name, model.ObjectDDM, cfg)`) and draws its `Definitions`, using `Type` as
  `Detail` (Story 3 AC2).

- **Inline subroutine names for Story 2** are in the current file's `FileAnalysis.Structure`
  tree as `SymbolSubroutine` children (`structure.go`, feature 09) — enumerable directly from
  the open buffer's analysis, offered before external `.NSS` candidates from the prefix query
  (mirrors FR-12 inline-before-external, already how `Resolve` handles PERFORM).

- **Fuzz-target convention:** every provider has a `FuzzProvideXxx` in `fuzz_test.go`
  (`FuzzProvideHover` ~386, `FuzzProvideCodeLens` ~781). Completion adds `FuzzProvideCompletion`
  (T8) and, if the context detector is non-trivial, a `FuzzCompletionContext` guard on the
  detector.

**No acceptance criterion is already satisfied** — this is a wholly new provider. **No shared
contract changes** except the additive `internal/workspace` prefix-query method (a new exported
method; the only external consumer, `lsp-graph`, does not call it, so no migration). No
`internal/model`/cache-format change; no Analyzer-seam crossing (completion is entirely
LSP-facing, reading only `internal/model` values via the index + `document.Store`).

---

## Open questions (resolve at approval checkpoint — plan carries both variants)

- **OQ-1 — Trigger characters (Story 4 AC2).** Fire automatically on space after a keyword,
  only on explicit invocation, or on a quote character? This sets
  `CompletionOptions.TriggerCharacters`. **Plan default: advertise `TriggerCharacters: [" "]`
  (space) plus explicit-invocation** so `CALLNAT ` opens the list, matching the story's "space
  after a keyword" example. If the user prefers quote-triggered (`CALLNAT '`), T3 flips the set
  and the context detector's prefix stripping (which already tolerates a leading quote) is
  unaffected. Either way the detector logic in T1 is trigger-agnostic — it classifies from the
  preceding keyword, not from the trigger char.

- **OQ-2 — `completionItem/resolve` (Story 4 AC2 alternative).** Compute `Detail` eagerly or add
  a `completionItem/resolve` handler for lazy detail? **Plan default: eager detail, no resolve
  handler** (`ResolveProvider: false`) — result sets are workspace-object-name-scale (hundreds,
  not thousands), detail is cheap (object-type label / DDM field type already in hand), and this
  matches feature 13's `resolveProvider: false` choice. If the user wants lazy resolve, it is an
  additive follow-up task (advertise `ResolveProvider: true` + a `completionItem/resolve`
  dispatch case) that does not change T1-T8; noted so the capability shape (T3) is decided once.

---

## Fixtures

New fixtures under `internal/server/testdata/completion/` (minimal, sanitized `.NSx`), plus
reuse of DDM fixtures under `internal/analysis/natural/testdata/ddm/` where a `.NSD` with known
fields is needed. Each task names its own. A completion fixture is typically a small workspace:
a caller object with a partial statement + one or more target objects (subprogram/program/
copycode/`.NSS`/`.NSD`) in the same or a steplib-mapped directory, indexed via `workspace.Build`
in the test harness (the existing provider tests show the pattern).

---

## Task list

Ordering: **spine first** (context detector + capability), then Story 1 (module completion —
the widest surface and the one that exercises the new prefix query), then Story 2 (PERFORM),
Story 3 (DDM fields), Story 4 (empty-context + fuzz). Foundations (T1 detector, T2 index query)
land before the provider tasks that consume them.

---

### T1 — Completion context detector (the spine)
**Behavior:** A new pure function in `completion.go`, e.g.
`detectCompletionContext(line string, cursorByteCol int) (kind completionKind, prefix string)`,
that inspects the text of the current line up to the cursor and classifies what to complete:
- preceding keyword `CALLNAT` → module context, expecting subprogram;
- `FETCH` (incl. `FETCH REPEAT`/`FETCH RETURN`) / `RUN` → module context, expecting program;
- `INCLUDE` → module context, expecting copycode;
- `PERFORM` → subroutine context;
- a data-access verb (`READ`/`FIND`/`GET`/`STORE`/`UPDATE`/`DELETE`) with a resolvable
  view/DDM already named on the statement → DDM-field context (carries the DDM name);
- anything else → none.
It also returns the **partial prefix already typed** (the token under/before the cursor, with a
leading quote stripped if present, uppercased for matching) so callers filter candidates.
Case-insensitive keyword matching (reuse the lexer's normalization convention — Natural is
case-insensitive). Robust to incomplete input: a bare `CALLNAT ` (no partial name yet) yields
module context with empty prefix (offer all reachable). This is a **prefix scan, not a re-parse**
(chosen for robustness on unparseable partial lines — see current-state findings).
**Fixtures:** none required (pure string/table-driven test); optionally a `.NSP` fixture if a
multi-line-continuation case needs a realistic line.
**Expected result:** a table-driven test asserting `(kind, prefix)` for representative prefixes:
`"CALLNAT MYSU"` → `(ctxModule/subprogram, "MYSU")`; `"CALLNAT "` → `(..., "")`;
`"perform "` → `(ctxSubroutine, "")`; `"INCLUDE SHAR"` → `(ctxModule/copycode, "SHAR")`;
`"RUN 'PRO"` → `(ctxModule/program, "PRO")`; `"COMPUTE X = 1"` → `(ctxNone, "")`;
`"MOVE 'X' TO Y"` → `(ctxNone, "")`.
**Reuses/migrates:** `lineAt`/`fromProtocolPosition` (`position.go`) at the call site to derive
`(line, cursorByteCol)`; no code migrated.
**DoD:**
- [ ] Table-driven test covers all six context kinds + `none` + empty-prefix (bare keyword).
- [ ] Detector never panics on empty/whitespace/degenerate lines (short cursor col, cursor past
  end) — asserted.
- [ ] Case-insensitive keyword match verified (`callnat`, `CallNat`).
- [ ] `go vet`/`gofmt` clean; pure function, no I/O, no locks.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** none.

### T2 — Index prefix/enumeration query (additive `internal/workspace`)
**Behavior:** A new exported `Index` method that returns reachable candidates matching a name
prefix and expected type, reusing the steplib chain. Proposed signature:
`Index.NamesWithPrefix(prefix string, typ model.ObjectType, referencingPath string, cfg *config.Config) []Candidate`.
It builds the name map (reuse `buildNameIndex`), filters keys by uppercased-prefix match, filters
each candidate by `typ`, then applies **the same reachability logic as `resolveByName`**: with a
library map, keep only candidates reachable via `buildSearchChain(currentLibrary, cfg)` (empty
prefix ⇒ current-lib longest-prefix of `referencingPath`); with no map (or undeclared-path
caller), flat namespace (all matches). Results deduped by object name **keeping the steplib
winner** for a name that appears in multiple reachable libraries (mirror
`resolveViaChain`'s first-wins), and sorted by name for determinism. Empty prefix ⇒ all reachable
names of that type.
**Fixtures:** none (unit-tested against an in-memory `Index` built from `FileAnalysis` values, as
`resolution_test.go` does).
**Expected result:** unit tests: (a) flat namespace returns all prefix matches of the type;
(b) library-map case returns only chain-reachable candidates and picks the steplib winner for a
cross-library name collision; (c) empty prefix returns all reachable of the type; (d) type filter
excludes other object kinds; (e) unknown prefix → empty (non-nil) slice; (f) deterministic sort.
**Reuses/migrates:** `buildNameIndex`, `objectIdentity`, `buildSearchChain`, `resolveViaChain`,
`filterByType` — all existing in `resolution.go`/`index.go`. **No `internal/model`/cache change.**
The method is additive; no existing consumer (`lsp-graph`) calls it, so no migration task.
**DoD:**
- [ ] Reachability reuses `buildSearchChain`/`resolveViaChain` (no duplicated chain logic) —
  asserted by a test mirroring a `resolveByName` steplib fixture.
- [ ] Deterministic, sorted, non-nil-on-empty output.
- [ ] `-race`-clean (reads the index under its own `RWMutex`, same discipline as `LookupByName`).
- [ ] `go vet`/`gofmt` clean; `internal/workspace` stays model-pure (no LSP/parser imports).
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** none (can run parallel to T1).

### T3 — Advertise `completionProvider` capability + dispatch skeleton
**Behavior:** Add `CompletionProvider: &protocol.CompletionOptions{...}` to the `initialize`
result (`server.go` ~139-149) and a `case "textDocument/completion"` in the dispatch switch,
gated on `stateInitialized`, decoding `protocol.CompletionParams`, calling
`provideCompletion(hctx, params)` (a stub returning `nil, nil` in this task), and marshaling the
result as a JSON array (`[]` when nil/empty — never `null`). Extend the `TestInitialize` locked
allow-list (`server_test.go` ~528) to require `completionProvider` and assert its
`triggerCharacters`/`resolveProvider` shape per OQ-1/OQ-2 defaults.
**Fixtures:** none (protocol-conformance test on `initialize` + a smoke request).
**Expected result:** `TestInitialize` passes with `completionProvider` present and carrying the
decided trigger set (default `[" "]`) / `resolveProvider:false`; a `textDocument/completion`
request before `initialized` returns `ServerNotInitialized`; after init it returns `[]` (empty
list, no error) from the stub.
**Reuses/migrates:** the dispatch/marshal idiom from the codeLens case (~811-839); **migrates the
`TestInitialize` `requiredProviders` lock** (mandatory — the allow-list is deliberately locked).
**DoD:**
- [ ] `TestInitialize` extended to assert `completionProvider` + its options shape.
- [ ] Dispatch case gates on `stateInitialized`, decodes params, marshals `[]` on empty.
- [ ] Server still builds and the full lifecycle test suite is green.
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** none for the capability; the stub `provideCompletion` is fleshed out in T4-T7.

### T4 — Module-name completion at CALLNAT / FETCH / INCLUDE (Story 1, AC1/AC4)
**Behavior:** Implement `provideCompletion` for the module contexts. Snapshot `idx`/`posEncoding`
under `RLock`, release before I/O (F7). Read the **open buffer first** (`store.Get`) for the
partial line; derive `(line, cursorByteCol)` via `fromProtocolPosition`+`lineAt`; call
`detectCompletionContext` (T1). For a module context, map the expected object type per keyword
(CALLNAT→`ObjectSubprogram`, FETCH/RUN→`ObjectProgram`, INCLUDE→`ObjectCopycode`) and query
`idx.NamesWithPrefix(prefix, typ, relPath, &cfg)` (T2). Build a `protocol.CompletionItem` per
candidate: `Label` = object name, `Kind` = object-type→`CompletionItemKind` (subprogram→Module,
program→File, copycode→Reference — decided/documented mapping; Story 1 AC4), `Detail` = the
object-type label (reuse `objectTypeLabel` from `hover.go`). Deterministic order (candidates
already sorted). Never panics; unreadable/missing/out-of-root → empty list.
**Fixtures:** `internal/server/testdata/completion/module/` — a caller `.NSP` with a partial
`CALLNAT MYSU` (and separate fixtures/cases for `FETCH`/`INCLUDE`), plus target objects `MYSUB.NSN`,
`MYPROG.NSP`, `SHARED.NSC`.
**Expected result:** completion for `CALLNAT MYSU|` offers `MYSUB` with kind Module / detail
"subprogram"; `INCLUDE SHAR|` offers `SHARED` (copycode); `FETCH MYP|` offers `MYPROG` (program);
each restricted to the expected object type (a subprogram is NOT offered for INCLUDE).
**Reuses/migrates:** T1 detector, T2 query, `objectTypeLabel` (`hover.go`), store-first pattern,
F7 snapshot. No migration.
**DoD:**
- [ ] Fixture-backed test per keyword (CALLNAT/FETCH/INCLUDE) asserting label + kind + detail.
- [ ] Type filtering asserted (wrong-type objects excluded).
- [ ] Object-type→`CompletionItemKind` mapping documented in code and covered (AC4).
- [ ] F7 snapshot + store-first; empty list (never `null`, never error) on unreadable target.
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T2, T3.

### T5 — Steplib-filtered vs flat-namespace module candidates (Story 1, AC2/AC3/AC5)
**Behavior:** Assert the reachability behavior end-to-end through the provider (T2 tested the
query in isolation; this proves the provider passes the right `relPath`/`cfg`). With a library
map configured, only chain-reachable names are offered (AC2); with no map, all flat-namespace
matches are offered (AC3). Add a test for AC5 (live freshness): after `applyDocumentChange` adds
a new module to the index, a subsequent completion offers it — no restart. This confirms the
provider reads the live `idx` snapshot, not a stale copy.
**Fixtures:** `internal/server/testdata/completion/steplib/` — two library dirs mapped in a test
`config.Config` (e.g. `APP/` with a steplib `COMMON/`), each holding a same-prefix subprogram, so
one is reachable and one is not from the caller in `APP/`.
**Expected result:** caller in `APP` (steplib `COMMON`) offers `APP`+`COMMON` subprograms but NOT
one in an unreachable `OTHER/` library; with the library map removed, all three are offered
(flat). After indexing a newly-added `NEWSUB.NSN`, completing `CALLNAT NEW|` offers it.
**Reuses/migrates:** T4 provider, T2 query, `applyDocumentChange`/`idx.Add`/`ResolveInto` (feature
10) for the freshness test. No migration.
**DoD:**
- [ ] Steplib-reachable-only asserted with a library map (AC2).
- [ ] Flat-namespace all-matches asserted with no map (AC3).
- [ ] Live-freshness test: newly added module appears without restart (AC5).
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T4.

### T6 — PERFORM subroutine completion: inline-first, then external, dynamic-excluded (Story 2)
**Behavior:** Extend `provideCompletion` for the subroutine context. Offer, in order:
(1) **inline** subroutines from the open buffer's `FileAnalysis.Structure` `SymbolSubroutine`
children whose name matches the prefix (`Kind` = Function), then (2) **external** subroutines via
`idx.NamesWithPrefix(prefix, model.ObjectExternalSubroutine, relPath, &cfg)`. Inline candidates
precede external ones (FR-12 parity). A **dynamic PERFORM target** — the partial token is a
variable operand (leading `#`/`&`/`+` sigil, or the detector already classified the line as a
dynamic operand) — yields **no completions** (AC3; a modeled gap, FR-17/FR-18: no error, no
diagnostic, empty list). The detector (T1) must expose enough to distinguish a name-typed prefix
from a sigil-typed one; extend T1's test coverage for the sigil case if not already present.
**Fixtures:** `internal/server/testdata/completion/perform/` — a `.NSP`/`.NSS` caller with an
inline `DEFINE SUBROUTINE MY-INLINE` plus a partial `PERFORM MY`, and an external `MYEXT.NSS` in
the workspace.
**Expected result:** `PERFORM MY|` offers `MY-INLINE` (inline, Function) before `MYEXT`
(external); `PERFORM #DYN|` offers nothing (empty list, no error).
**Reuses/migrates:** `FileAnalysis.Structure` walk (`SymbolSubroutine` children), T2 query, T1
detector. No migration.
**DoD:**
- [ ] Inline-before-external order asserted (AC1/AC2).
- [ ] Dynamic (sigil) PERFORM target → empty list, no diagnostic/error asserted (AC3, FR-17/18).
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T2, T4 (shares the provider body).

### T7 — DDM field-name completion at data-access statements (Story 3)
**Behavior:** Extend `provideCompletion` for the DDM-field context. The detector (T1) supplies the
DDM/view name named on the current data-access statement. Resolve that name to a `.NSD` in the
index via `idx.LookupByName(ddmName, model.ObjectDDM, &cfg)` (exact-name — a data-access statement
names the view explicitly). On a unique resolved DDM, read its `FileAnalysis.Definitions` and emit
a `protocol.CompletionItem` per field: `Label` = field `Name`, `Kind` = `CompletionItemKindField`,
`Detail` = verbatim `Type` (`N8`/`A50`/`P9,2` — Story 3 AC2), filtered by the field-name prefix.
**Unresolved/unindexed DDM → empty list, no error** (Story 3 AC3, FR-17): a name that matches no
`.NSD`, an ambiguous match, or a `.NSD` with nil/empty `Definitions` all yield an empty list.
**Fixtures:** a caller `.NSP` under `internal/server/testdata/completion/ddmfield/` with e.g.
`READ CUSTOMER ... ` and a partial field reference, plus a `CUSTOMER.NSD` (reuse or copy a fixture
from `internal/analysis/natural/testdata/ddm/` with known field names + types). A second case: a
data-access statement naming a DDM not present in the workspace.
**Expected result:** field completion offers the DDM's fields with kind Field and detail = the
field's type; a data-access statement on an unindexed DDM offers nothing (empty list, no error).
**Reuses/migrates:** `LookupByName` (exact, existing), `ddm.go`-populated `Definitions`, T1
detector. No migration.
**DoD:**
- [ ] Field label + Field kind + type-as-detail asserted (AC1/AC2).
- [ ] Unresolved/unindexed/no-`Definitions` DDM → empty list, no error/diagnostic (AC3, FR-17).
- [ ] `go vet`/`gofmt` clean.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T1, T4 (shares the provider body).

### T8 — Unrecognized context → empty; fuzz guard (Story 4, FR-43)
**Behavior:** Assert the `ctxNone` path: a cursor outside any call/PERFORM/INCLUDE/data-access
position returns an empty list, not an error (Story 4 AC1). Add `FuzzProvideCompletion` to
`fuzz_test.go` (matching `FuzzProvideHover`/`FuzzProvideCodeLens`): feed arbitrary content +
cursor positions + both position encodings through `provideCompletion` and assert it never panics
and always returns a valid (possibly empty) list, no error (FR-43). Add a `FuzzCompletionContext`
guard on the T1 detector if it is non-trivial (arbitrary line + cursor col → never panics).
**Fixtures:** a `.NSP` with plain non-triggering statements (`COMPUTE`, `MOVE`, comments) under
`internal/server/testdata/completion/none/`; fuzz seeds drawn from the T4-T7 fixtures.
**Expected result:** completion at a `COMPUTE`/comment/blank line returns `[]`, no error; the fuzz
targets survive the corpus + generated inputs without panicking.
**Reuses/migrates:** the fuzz-target convention (`fuzz_test.go`); T1 detector; the assembled
provider. No migration.
**DoD:**
- [ ] Unrecognized-context empty-list-no-error asserted (AC1).
- [ ] `FuzzProvideCompletion` added and green over the seed corpus (FR-43).
- [ ] `FuzzCompletionContext` (if detector warrants) green.
- [ ] `go vet`/`gofmt` clean; `just verify` (incl. `-race`) passes.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.
**Depends on:** T4, T6, T7.

---

## Reviews required (`/review-feature`)

- **review-protocol-conformance** — new LSP method (`textDocument/completion`) + a new capability
  shape (`CompletionOptions` with trigger chars / resolve): verify the `initialize` allow-list, the
  empty-list-vs-`null` response contract, and `CompletionItem` field usage.
- **review-concurrency** — completion reads the live `idx` under the F7 RLock snapshot while
  `applyDocumentChange` mutates it; verify the snapshot-release-before-I/O discipline and the
  store-first read hold under `-race` (T5's freshness test is the load-bearing case).
- **review-robustness** — new partial-line context detector parsing incomplete/unparseable input;
  verify FR-43 (fuzz targets, degenerate lines) and the modeled-gap discipline (FR-17/FR-18:
  dynamic PERFORM + unresolved DDM → empty, never diagnostics).
- **review-seam** — an additive `internal/workspace` public method (`NamesWithPrefix`) is a new
  shared-contract surface; confirm it stays model-pure and reuses the chain helpers rather than
  forking resolution logic. (No `internal/model`/Analyzer change.)
- **review-docs** — new advertised capability + provider: `CLAUDE.md` "Project state" and
  `README.md` feature list must gain completion before `/finalize-feature`.

## Approved decisions (checkpoint)

Resolved with the user before implementation — both confirm the plan defaults:

- **OQ-1 → space-trigger + explicit invocation.** `CompletionOptions.TriggerCharacters: [" "]`
  (no quote character). The T1 detector stays trigger-agnostic.
- **OQ-2 → eager detail, no resolve handler.** `CompletionOptions.ResolveProvider: false`; `Detail`
  is computed up front. No `completionItem/resolve` dispatch case.

These fix T3's capability shape; T1/T2 and the provider bodies (T4–T8) are unaffected.

## Open questions

Consolidated above under **OQ-1 (trigger characters)** and **OQ-2 (`completionItem/resolve`)** —
both to be confirmed at the plan-approval checkpoint. The plan carries defaults (space-trigger +
eager detail, no resolve handler) so implementation is unblocked; if the user chooses otherwise,
only T3's capability shape (and, for lazy resolve, one additive dispatch case) changes — T1/T2 and
the provider bodies (T4-T8) are unaffected.
