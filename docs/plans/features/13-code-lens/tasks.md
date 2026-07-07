# Tasks: Code lens (feature 13)

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements:** FR-29 (code lens provider). Cross-cutting: FR-17 (modeled gaps kept
distinct — dynamic/unresolved sites never fabricate a count), FR-43 (graceful degradation /
never-panic), NFR "does not noticeably degrade editor responsiveness" (Story 3 AC #2), ADR-008
(position encoding).
**Depends on (shipped):** feature 07 resolution (`internal/workspace/resolution.go`,
`ResolutionSet`, `ResolveInto`), feature 08 data-access (`model.DataAccessEntry`, `EdgeWrites`,
`FileAnalysis.DataAccess`), feature 09 structure (`FileAnalysis.Structure *model.Symbol`), feature
10 navigation (`internal/server/references.go` `referenceSites`, `position.go`, `handlerContext`
snapshot pattern), feature 12 hover (reuses `referenceSites` for inbound counts).

---

## Current-state findings & impact

The provider layer is mature; this feature is **server-side wiring only** and requires **no
`internal/model` change and no cache-format bump** (stays `0.6.0`) — every input it needs already
exists in `FileAnalysis`.

**What already exists and is reused directly:**

- **Inbound-call counting** — `referenceSites(idx, res, root, targetPath, targetName, targetType,
  includeDeclaration, enc) []protocol.Location` in `internal/server/references.go` is the exact
  reverse-sweep the call-count lens needs; feature 12's `buildModuleHover` already calls
  `len(referenceSites(...))` for its "Inbound calls" line. Story 1's count is the same primitive
  applied at the object root rather than at a cursor edge. **Note:** `referenceSites` reads every
  file's content from disk (`os.ReadFile` in the `ForEach` loop) purely to convert ranges — for a
  *count* we don't need the `Location`s at all, so a lens that only needs the number should not pay
  that I/O cost per object (see T2 and Reviews/performance).
- **Write edges** — `FileAnalysis.DataAccess []model.DataAccessEntry` with `Kind == model.EdgeWrites`
  (STORE + record-form UPDATE/DELETE from feature 08; INSERT/SQL-UPDATE/SQL-DELETE/MERGE from feature
  08b). Each entry carries `Name` (normalized upper-case DDM/view name), `NameRange`, and `Source`.
  The feature-08 record-form gap is an **empty-`Name`** write entry (OQ-4) — the write summary must
  handle that gap explicitly (FR-17): count/list only named writes, never fabricate a name.
- **The object anchor** — `FileAnalysis.Structure.SelectionRange` is a zero-width point at
  `prog.StartPos` (the object root; `structure.go` lines 41–44). This is the natural single-line
  anchor for an object-level lens (call-count + write-summary), matching how `documentSymbol` and
  `workspace/symbol` already use the root `SelectionRange`.
- **Position conversion** — `toProtocolRange` / `modelPositionToProtocol` (`position.go`, ADR-008).
  A CodeLens `Range` "should only span a single line" (LSP); the root `SelectionRange` is zero-width
  on one line, satisfying that.
- **Handler wiring pattern** — `server.go` dispatch switch + `handlerContext` (RWMutex snapshot of
  `idx`/`res`, F7 build-then-publish). A new `textDocument/codeLens` case follows the
  `textDocument/documentSymbol` case verbatim (gate on `stateInitialized`, decode params, call
  provider, marshal `[]protocol.CodeLens` or `null`).
- **URI/path + doc-store-first pattern** — `uriToRelPath` helper (`definition.go`) and the
  open-document-buffer-first resolution order (`document_symbols.go`) — code lens, like document
  outline, is document-scoped and should reflect live edits when the doc is open.
- **Protocol types present** — `protocol.CodeLensParams`, `protocol.CodeLens{Range, Command, Data}`,
  `protocol.Command{Title, Command, Arguments}`, `protocol.CodeLensOptions{ResolveProvider *bool}`,
  and `ServerCapabilities.CodeLensProvider *CodeLensOptions` (go.lsp.dev/protocol v1.0.0).
- **Config** — `AnalysisConfig` (TOML `[analysis]`) is the right home for the Story 3 toggle;
  config is decoded onto `Defaults()` (decode-onto-defaults), `Sample()` documents each key, and a
  bool needs no `Validate` entry (its zero value is meaningful). `handlerContext.cfg` already
  carries `config.Config` into every handler.

**TestInitialize allow-list:** `codeLensProvider` is currently in the **negative** allow-list
(`server_test.go` ~line 505: asserted *absent*). This feature moves it to the required-providers
list — an explicit, test-enforced capability addition (T5).

**Criteria already satisfied (no task):** Story 3 AC #2 ("reuses the index, does not noticeably
degrade responsiveness") is largely satisfied by design — lens computation reads the already-built
`idx`/`res` snapshot. The one caveat is the per-object disk I/O inside `referenceSites` (see T2):
we add a count-only path rather than materializing `Location`s, which keeps the hot path cheap.

**Divergence found:** none between code and CLAUDE.md for the touched surface. `handlers.go`'s
package doc already anticipates codeLens (FR-24..29).

**Seam:** entirely LSP-facing (`internal/server/`) plus one additive `internal/config` field. The
Analyzer seam is untouched; providers depend only on `internal/model` + `workspace.Index`/
`ResolutionSet`, as required.

---

## Design decisions folded into the tasks (see Open Questions for the two needing your call)

- **Lens set to ship:** the two explicit ones — (1) inbound call-count on the object root, (2)
  table-write summary on the object root. No speculative extras (OQ-A).
- **Anchor:** both object-level lenses attach at `Structure.SelectionRange` (object root, one line).
  This keeps the response tiny (≤2 lenses/file) and avoids per-write-site clutter. The
  *write-summary command target* still navigates to the write sites (below).
- **Command wiring:** activating a lens fires the standard client command
  `editor.action.showReferences` with arguments `[uri, position, []Location]` (the de-facto VS
  Code / LSP convention for "reveal references" — the same shape used by go's gopls call-count
  lens). The provider computes the `[]Location` and embeds them as the command's `Arguments`. This
  is a **client-command** assumption flagged in OQ-C.
- **Resolve strategy:** compute lenses fully in `textDocument/codeLens` (title + command +
  arguments all populated); advertise `CodeLensOptions{ResolveProvider: false}` and **do not**
  implement `codeLens/resolve`. Rationale: our counts come from the in-memory index with no
  expensive lazy work to defer, and eager resolution is simpler and correct. (Revisit only if
  responsiveness reviews show the per-object `Location` materialization is too costly — then move
  the `Location` computation into `codeLens/resolve` and ship unresolved lenses first. Noted in
  Reviews/performance.)
- **Default on/off:** recommend **lenses ON by default** (`Analysis.EnableCodeLens = true` in
  `Defaults()`), matching every other provider (all advertised unconditionally) and Story 1/2's
  "at-a-glance" intent; Story 3 requires only that they *can* be disabled. Confirm in OQ-B.

---

## Ordered task list

Order: config foundation → pure lens builders (count, write-summary) → provider assembly →
capability advertisement + allow-list → integration + doc-open freshness → fuzz. Each task is one
red → green → refactor loop.

---

### T1 — Config toggle: `Analysis.EnableCodeLens` (Story 3 AC #1 foundation)

**Behavior:** add an additive bool `EnableCodeLens` to `config.AnalysisConfig` (TOML key
`enable_code_lens`), defaulting to `true` in `Defaults()`, documented in `Sample()`. No `Validate`
entry (a bool zero value is meaningful; decode-onto-defaults gives `false` only when explicitly set).

**Fixtures:** none (config unit tests are in-memory TOML strings — follow existing
`config_test.go` table style).

**Expected result:**
- `Defaults().Analysis.EnableCodeLens == true`.
- Parsing `[analysis]\nenable_code_lens = false` yields `EnableCodeLens == false`; omitting the key
  yields `true` (decode-onto-defaults).
- `Sample()` output contains `enable_code_lens = true` under `[analysis]`, and re-parsing the
  sample round-trips to `Defaults()` (the existing sample round-trip test must stay green).

**Reuses/migrates:** extends `AnalysisConfig`, `Defaults()`, `Sample()`. Consumer to migrate: the
sample round-trip test in `config_test.go` (add the new line).

**DoD:** table-driven config tests (default, explicit-false, omitted, sample round-trip); existing
config tests green; `go vet`/`gofmt` clean; additive-only (no removal/rename).

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** none.

---

### T2 — `inboundCallCount` count-only primitive (Story 1 AC #1 core, NFR responsiveness)

**Behavior:** add a pure helper in `internal/server/` (e.g. in a new `code_lens.go`) that returns
the **number** of resolved reference sites for a target object without materializing `Location`s or
reading file content — a count-only sibling of `referenceSites`. Signature sketch:
`inboundCallCount(idx *workspace.Index, res *workspace.ResolutionSet, targetPath string, targetName string, targetType model.ObjectType) int`. It mirrors `referenceSites`' matching (resolved-only,
path+type match, FR-17: dynamic/unresolved/ambiguous never counted) but skips `os.ReadFile` and
range conversion.

**Fixtures:** reuse existing feature-10 reference fixtures (e.g. the multi-caller workspace used by
`references_test.go` / `TestWorkspaceIndexBuiltAfterInitialized`'s `caller.NSP`+`helper.NSN`); no
new fixture needed for the pure count.

**Expected result:** for a target called from N resolved sites, returns N; a target with only
dynamic (`CALLNAT #VAR`) or unresolved callers returns 0; matches
`len(referenceSites(..., includeDeclaration=false, ...))` for the same target (assert equality in
the test as a cross-check).

**Reuses/migrates:** factor the matching predicate out of `referenceSites` if it can be shared
without churn; otherwise duplicate the small predicate and note it. Does **not** change
`referenceSites`' signature (hover + references depend on it).

**DoD:** table-driven test (multi-caller, dynamic-only, unresolved-only, zero-caller); equality
cross-check against `referenceSites`; no I/O in the function; FR-17 gap coverage explicit;
deterministic; `go vet`/`gofmt` clean.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** none (T1 not required).

---

### T3 — `buildCallCountLens` pure builder (Story 1 AC #1, AC #2)

**Behavior:** a pure function that, given the object root's `SelectionRange`, the object name, the
inbound count, the file URI, and the encoding, returns a `protocol.CodeLens` with:
- `Range` = `toProtocolRange(root.SelectionRange, content, enc)` (single line);
- `Command.Title` = e.g. `"3 references"` / `"1 reference"` / `"0 references"` (singular/plural);
- `Command.Command` = `"editor.action.showReferences"`;
- `Command.Arguments` = `[uri, position, []Location]` where the `[]Location` are the caller sites
  (from `referenceSites`, so activation reveals them — AC #2).

Zero count still emits a lens titled `"0 references"` (an at-a-glance signal, not omission) — or per
OQ-A confirm whether to suppress zero-count lenses.

**Fixtures:** reuse T2's multi-caller fixture.

**Expected result:** a `CodeLens` whose title matches the count with correct pluralization, whose
command id is `editor.action.showReferences`, and whose third argument is the sorted caller
`[]Location` (deterministic, matching `referenceSites` order).

**Reuses/migrates:** `referenceSites` (for the `[]Location` command argument), `toProtocolRange`,
`inboundCallCount` (T2, for the title) — or call `referenceSites` once and use `len(...)` for the
title to avoid a double sweep (decide in green; note it). Pure: no locks, no I/O beyond what the
caller passes.

**DoD:** table-driven test (0/1/N references, pluralization, command shape, argument order);
FR-17: a target with only dynamic/unresolved callers yields `"0 references"` and empty
`[]Location`; `go vet`/`gofmt`; deterministic.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** T2.

---

### T4 — `buildWriteSummaryLens` pure builder (Story 2 AC #1, AC #2)

**Behavior:** a pure function that, given an object's `FileAnalysis` (or its
`DataAccess []model.DataAccessEntry`), the root `SelectionRange`, file URI, content, and encoding,
returns a `protocol.CodeLens` summarizing the DDMs/files the object **writes** (`Kind ==
model.EdgeWrites`):
- collect distinct **named** write targets (dedupe case-insensitively on `Name`, sorted for
  determinism); **skip empty-`Name` entries** (feature-08 record-form gap, OQ-4 / FR-17 — never
  fabricate a table name);
- `Command.Title` = e.g. `"Writes: CUSTOMER, ORDERS"` (or `"Writes 2 tables"` — pick one; a named
  list is more useful and cheap for small N — confirm no truncation policy needed);
- `Command.Command` = `"editor.action.showReferences"` with `Arguments` = `[uri, position,
  []Location]` where the `[]Location` are the **write sites** (`DataAccessEntry.NameRange` for
  named writes, converted via `toProtocolRange`), so activation reveals the mutations (AC #2).

If the object has **no named writes**, emit **no** write-summary lens (contrast the call-count lens,
which is meaningful at zero; a "writes nothing" lens is noise — confirm in OQ-A).

**Fixtures:** a small `.NSP` under `internal/server/testdata/codelens/` writing two distinct DDMs
(e.g. `STORE CUSTOMER` + `UPDATE ORDERS`) plus at least one record-form `UPDATE` (empty-`Name` gap)
to prove the gap is skipped. Reuse a feature-08 dataaccess fixture if one already covers this shape;
otherwise add one minimal sanitized fixture.

**Expected result:** a `CodeLens` listing `CUSTOMER, ORDERS` (sorted, deduped, gap-excluded), command
id `editor.action.showReferences`, and write-site `[]Location`s; an object with only a record-form
(empty-`Name`) write and no named writes yields **no** lens.

**Reuses/migrates:** `FileAnalysis.DataAccess`, `model.EdgeWrites`, `DataAccessEntry.Name`/
`NameRange`, `toProtocolRange`. Pure.

**DoD:** table-driven test (two named writes, dedupe, sort, empty-`Name`-only → no lens, mixed);
FR-17 gap coverage explicit; deterministic sorted output; `go vet`/`gofmt`.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** none (independent of T2/T3).

---

### T5 — `provideCodeLens` provider assembly (Stories 1+2, Story 3 AC #1)

**Behavior:** the LSP entry point `provideCodeLens(hctx *handlerContext, params
protocol.CodeLensParams) ([]protocol.CodeLens, error)`:
1. If `hctx.cfg.Analysis.EnableCodeLens == false`, return `nil, nil` (Story 3 disable — no lenses).
2. Resolve the document: **open-document store first** (live edits, mirroring
   `provideDocumentSymbols`), else index snapshot under `RLock` released before I/O (F7).
3. If `Structure == nil` or file unreadable → `nil, nil` (FR-43).
4. Build the call-count lens (T3) and, if there are named writes, the write-summary lens (T4),
   using the object root `SelectionRange` as the anchor.
5. Return the assembled `[]protocol.CodeLens` (deterministic order: call-count then write-summary),
   or `nil` when neither applies.

**Fixtures:** reuse T4's `testdata/codelens/` object (has both callers-of-it context via the
index and writes); a caller file so the object has a non-zero inbound count.

**Expected result:** for the fixture object: two lenses (call-count + write-summary) on line 1 with
correct titles and commands; with `EnableCodeLens=false`, `nil`; for a file not in the
index/without `Structure`, `nil`.

**Reuses/migrates:** T3, T4, `uriToRelPath`, `document.Store.Get`, the `idxResMu` snapshot pattern,
`hctx.cfg`. Concurrency: snapshot `idx`/`res` under `RLock`, release before `referenceSites`/I/O
(F7) — same discipline as `provideHover`.

**DoD:** table-driven/provider test (both lenses present; disabled→nil; no-structure→nil;
open-doc-first path); `-race` on the concurrency-touching path if the test exercises a swap;
FR-17/FR-43 held; `go vet`/`gofmt`.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** T1, T3, T4.

---

### T6 — Capability advertisement + dispatch wiring + TestInitialize allow-list (FR-29)

**Behavior:**
- In `handleInitialize`, set `Capabilities.CodeLensProvider = &protocol.CodeLensOptions{
  ResolveProvider: ptr(false)}` (eager resolution — no `codeLens/resolve`). Update the surrounding
  allow-list comment (feature 13).
- Add a `case "textDocument/codeLens":` to the `server.go` dispatch switch, mirroring
  `textDocument/documentSymbol`: gate on `stateInitialized`, decode `protocol.CodeLensParams`, call
  `provideCodeLens`, marshal `[]protocol.CodeLens` (or `null` when nil).
- **Migrate `TestInitialize`:** move `"codeLensProvider"` out of the `otherProviderFlags`
  (asserted-absent) list into `requiredProviders` (asserted-present). Because `CodeLensProvider` is
  an *object* (`CodeLensOptions`), not a bare `true`, adjust the assertion: it must assert the key
  is **present and non-nil** (an object), not `== true` (the existing `requiredProviders` loop
  checks `val == false` — a non-empty object passes that, but confirm the object serializes to a
  JSON object, not `false`/`null`).

**Fixtures:** none (uses `TestInitialize`'s in-JSON params).

**Expected result:** the `initialize` response advertises `codeLensProvider` as an object with
`resolveProvider: false`; `TestInitialize` asserts its presence; a `textDocument/codeLens` request
after `initialized` returns the provider result; before `initialized` returns
`ServerNotInitialized`.

**Reuses/migrates:** `handleInitialize`, the dispatch switch, `TestInitialize` (the locked
allow-list — this is the explicit capability-addition step). Consumer to migrate:
`TestInitialize` (required), any golden `initialize`-response test in `server_test.go` (search for
capability assertions and update).

**DoD:** `TestInitialize` updated and green; a request-level test drives
`initialize→initialized→textDocument/codeLens` and asserts the lenses; the not-initialized gate
tested; `go vet`/`gofmt`.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** T5.

---

### T7 — Incremental freshness: count updates on re-analysis (Story 1 AC #3)

**Behavior:** verify (not new code) that after a `didChange`/watched-file change that adds or
removes a caller, a subsequent `textDocument/codeLens` reflects the updated inbound count. This
rides on feature 10's `applyDocumentChange` → `ResolveInto` build-then-publish; the lens reads the
fresh `res` snapshot on each request.

**Fixtures:** reuse T5's fixture; the test mutates a caller file's content (add/remove a `CALLNAT`)
via the store/index update path.

**Expected result:** count in the returned lens title increases/decreases to match the mutated
caller set after the change is applied; the open-document-first path reflects unsaved edits when the
target's own file is the open buffer.

**Reuses/migrates:** `applyDocumentChange`, `ResolveInto`, `provideCodeLens`. No production change
expected — if the test fails, the fix belongs in T5's snapshot handling, not a new mechanism.

**DoD:** integration-style test through the handler with an index update between two codeLens
requests; `-race`; deterministic; `go vet`/`gofmt`. If it passes with no code change, record that
AC #3 is satisfied by the existing incremental path.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** T6.

---

### T8 — `FuzzProvideCodeLens` (FR-43 never-panic)

**Behavior:** a fuzz target guarding `provideCodeLens` (and, if separately reachable, the pure
builders) against degenerate input — malformed content, empty/nil `Structure`, out-of-range
positions, both encodings — asserting it never panics and always returns cleanly, matching the
existing `FuzzProvideHover`/`FuzzDocumentSymbols` pattern in `fuzz_test.go`.

**Fixtures:** fuzz seed corpus from the codelens testdata + degenerate strings (follow existing
fuzz seeding).

**Expected result:** no panic across the corpus; a valid `[]protocol.CodeLens` or `nil`.

**Reuses/migrates:** the `fuzz_test.go` harness conventions (`handlerContext` construction, encoding
matrix).

**DoD:** fuzz target added, seeds committed, short fuzz run clean; `go vet`/`gofmt`.

**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** T5 (T6/T7 not required).

---

## Reviews required (for `/review-feature`)

- **review-protocol-conformance** — new LSP method `textDocument/codeLens`, `CodeLensProvider`
  capability shape (`CodeLensOptions` object vs bare bool), CodeLens `Range` single-line rule,
  `Command` shape, and the decision to skip `codeLens/resolve` (resolveProvider:false). Confirm the
  `editor.action.showReferences` command contract (OQ-C).
- **review-concurrency** — `provideCodeLens` reads the `idx`/`res` snapshot under `RLock` and calls
  `referenceSites` (which does per-file I/O); verify the F7 lock-release-before-I/O discipline and
  that T7's incremental-update path is race-clean (`-race`).
- **review-performance** — the per-object `referenceSites` sweep (full index scan + per-file
  `os.ReadFile`) runs per `textDocument/codeLens` request; confirm it meets Story 3 AC #2, and
  validate the count-only path (T2) plus the eager-vs-resolve decision. This is the responsiveness
  risk.
- **review-robustness** — FR-43 graceful degradation (nil Structure, unreadable file, empty-`Name`
  write gap) and the new fuzz target (T8).
- **review-docs** — capability + provider addition; `CLAUDE.md` "Project state" and `README.md`
  feature/capability list must gain code lens; `Sample()`/config docs gain `enable_code_lens`
  (anticipated at `/finalize-feature`).
- *(review-seam not required — no shared-contract change; `internal/config` add is additive and
  self-contained.)*

---

## Open questions (need your decision before/at implementation)

- **OQ-A — Lens set + zero-value rendering.** Ship exactly the two lenses (call-count +
  write-summary)? And: (a) render a `"0 references"` call-count lens or suppress it at zero? (b)
  suppress the write-summary lens when the object writes nothing (recommended) vs render "Writes: —"?
  Plan assumes: ship both lenses; render `"0 references"`; suppress the empty write-summary.
- **OQ-B — Default on/off (Story 3).** Recommend **on by default** (`EnableCodeLens = true`),
  consistent with every other provider and the "at-a-glance" intent; Story 3 only requires the
  *ability* to disable. Confirm, or set default off (opt-in) if you'd rather lenses be quiet until
  requested.
- **OQ-C — Command contract for activation (Stories 1/2 AC #2).** Plan assumes the client command
  `editor.action.showReferences` with `Arguments = [uri, position, []Location]` (the de-facto
  VS Code / gopls convention). This is **client-specific** — a non-VS-Code editor may not bind that
  command. Alternatives: (a) keep it (works for the primary target editor), (b) emit a plain
  `Command.Title` with an empty/omitted command (display-only lens, no navigation — weaker AC #2),
  or (c) define a server-owned command id and rely on `workspace/executeCommand` (heavier, needs a
  command-provider capability we don't yet advertise). Recommend (a) for now, documented as a known
  client dependency.
- **OQ-D — Resolve split.** Plan computes lenses eagerly and advertises `resolveProvider:false`
  (simple, correct, cheap given in-memory counts). Confirm we don't need `codeLens/resolve` for
  responsiveness; if the performance review flags the per-object `referenceSites` I/O, the fallback
  is to ship unresolved lenses and move `[]Location` materialization into `codeLens/resolve`
  (adds a T-resolve task and a `resolveProvider:true` capability).
