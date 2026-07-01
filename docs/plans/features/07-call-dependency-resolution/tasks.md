# Tasks: Call & Dependency Resolution (feature 07)

**Source plan:** [plan.md](./plan.md)
**PRD requirements covered:** FR-5, FR-10, FR-11, FR-12, FR-13, FR-14, FR-15, FR-16, FR-17, FR-18,
FR-31; NFR-6, NFR-7; M-3, M-4, M-6
**Depends on:** feature 06 (call/dependency extraction — merged), feature 05 (workspace index + cache —
merged), feature 01 (`internal/config` — merged)

This feature is **cross-file resolution**. It consumes the per-file `model.EdgeEntry` edges that feature
06 already extracts and binds each one to a definition (a file/object in the workspace) using the
workspace index and Natural's steplib semantics. The home is `internal/workspace/resolution.go` (already
a doc-only stub). It is wholly on the **workspace side** of the Analyzer seam — no `internal/analysis`
or LSP-handler changes are required by this feature (the LSP query surface — find-references, call
hierarchy — is later features that *consume* the resolution result this feature produces).

---

## Current-state findings & impact

### What feature 06 already produced (verified in `internal/analysis/natural/calls.go`, `internal/model/model.go`)
- `model.EdgeEntry{Source, Target Range, Kind EdgeKind, TargetName string, Library string}` and
  `FileAnalysis.Edges []EdgeEntry`. Edge kinds present: `EdgeCalls`/`EdgeCallsDynamic`,
  `EdgeNavigatesTo`/`EdgeNavigatesToDynamic`, `EdgePerforms`, `EdgeIncludes` (plus `EdgeReads`/`EdgeWrites`
  reserved for feature 08).
- **Dynamic classification is already done at extraction.** `CALLNAT #VAR` / `FETCH #VAR` / `RUN #VAR`
  already emit the `*_DYNAMIC` kind, and `isStaticLiteral` already **downgrades `&`-placeholder literals
  to dynamic at extraction** (FR-18). So by the time an edge reaches resolution, a `*_DYNAMIC` kind
  already means "do not attempt to bind." **Impact on Story 2, Story 5 (dynamic), Story 8:** resolution
  does *not* re-classify; it simply records `*_DYNAMIC` edges as unresolved-by-design and never binds
  them. The only resolution-side work for these stories is asserting that behavior in fixtures and that
  caller context is preserved on the un-bound edge.
- **Inline-PERFORM is already bound at extraction.** For `EdgePerforms`, `EdgeEntry.Target` carries the
  in-file `DEFINE SUBROUTINE` definition `Range` when an inline match exists, else the zero `Range`.
  **Impact on Story 3 (FR-12, M-4):** the "inline" half is done; resolution implements only the
  *external fallback* — when `Target` is the zero `Range`, resolve `TargetName` to an external `.NSS`
  via the steplib chain.
- **RUN library-id is captured** on `EdgeEntry.Library`; FETCH never sets it (verified — FETCH has no
  source-level library qualifier per the calls-and-resolution knowledge note). **Impact on Story 5/6:**
  resolution honors a non-empty `Library` by resolving against that single library only, bypassing the
  steplib chain.

### What feature 05 provides (verified in `internal/workspace/index.go`, `cache.go`)
- `Index` keys `FileAnalysis` by **workspace-relative file path** (`idx.Add(relPath, fa)`). Query
  surface today: `Get(path)`, `ForEach`, `Keys`, `Invalidate(path)`. There is **no lookup by module
  name or library** — resolution must add one.
- `Invalidate(path)` already walks `EdgeIncludes` to compute transitive dependents (Story 4's
  re-analysis requirement). **Impact on Story 4:** the incremental-re-analysis half is already wired;
  this feature only adds the *resolved INCLUDE relationship* (binding the copycode `TargetName` to a
  `.NSC` file). **Caveat / divergence to flag:** `Invalidate` matches `edge.TargetName == path`, i.e. it
  compares a copycode *name* against a file *path*. That comparison can only succeed if names happen to
  equal relative paths; resolution introduces proper name→path binding, so a small migration (Task 9)
  is warranted to make `Invalidate` use resolved targets.
- The cache (`cache.go`, `cacheFormatVersion = "0.3.0"`) serializes `Edges` directly. **Any change to
  `EdgeEntry` or any new persisted resolution structure forces a cache-format version bump** and a
  migration of `cacheEntry` save/load — see Task 1 and Open Question 1.

### What config provides (verified in `internal/config/config.go`)
- `config.ResolutionConfig.Libraries []Library` with `Library{Name, Path, Steplibs []string}`, **declared
  order preserved**, nameless entries already dropped by `Validate`. `Path` is the workspace-relative
  directory holding the library's objects. **Impact on Story 6 / Open Question 3:** "current library" for
  a file is determined by matching the file's relative path against each `Library.Path` prefix (longest /
  declared-order match). `SYSTEM` is an implicit terminal steplib of every library (per ADR-004 / natls
  cross-check). With an empty `Libraries` slice, the whole workspace is one flat namespace (FR-5).

### Gaps this feature must close
- **`Symbols` is never populated for Natural files** (verified: no `extractSymbols` in
  `internal/analysis/natural`). An object's *definition identity* today is only its file: its **name** is
  the filename stem (e.g. `MYSUB.NSN` → `MYSUB`), its **type** is `FileAnalysis.ObjectType`, its
  **library** is derived from the config path mapping. Resolution therefore binds `TargetName` to a
  *file/object*, keyed by (name, type, library) derived from the index keys + ObjectType + config — it
  does **not** depend on per-file symbol extraction. (Inline subroutine binding is the one definition
  that already exists, and it is in-file, supplied by extraction.)
- **`EdgeEntry.Target` has no target file/object identity** — only a `Range`. Recording *which object* a
  reference resolved to is the central contract decision (Open Question 1). The plan below assumes the
  decision is **a separate resolution result/index built over the existing edges** (model-pure, no
  `EdgeEntry` change, no immediate cache bump) — but this is gated on your answer to OQ-1; tasks are
  written so the binding step (Task 5) is where the chosen representation lands.
- **Resolution-time diagnostics** (ambiguity, FR-5/FR-31) are a cross-file outcome; today all diagnostics
  originate per-file in the parser. Where they surface is Open Question 2. The plan below assumes
  resolution produces diagnostics attached to the *referencing file* (so the existing
  `FileAnalysis.Diagnostics` → LSP `publishDiagnostics` channel carries them) — gated on OQ-2.

### Seam note
All work is in `internal/workspace`. LSP-facing code continues to depend only on `analysis.Analyzer` +
`internal/model`. If OQ-1 is resolved as "new `internal/model` type," that type stays backend-free
(pure data) to preserve model purity; `review-seam` is required in that case.

---

## Decisions locked at plan approval (2026-06-30)

- **OQ-1 → (a) separate resolution index.** A `Resolution`/`ResolutionResult` type in
  `internal/workspace` keyed by (referencing file path, edge `Source`), with outcome
  {Resolved(path, ObjectType) | Unresolved(reason) | Ambiguous(candidates)}. **`internal/model` and the
  cache are UNTOUCHED — no cache-format bump**; resolution is recomputed from cached `Edges` on load.
  (Removes the cache-bump sub-task from Task 1 and the bump branches in Tasks 5/6/11.)
- **OQ-2 → (a), refined during impl for idempotence:** resolution produces the ambiguity
  `model.Diagnostic` for the **referencing file** (range = call-site `Source`), but exposes it on the
  `ResolutionSet` via `DiagnosticsFor(filePath) []model.Diagnostic` rather than mutating the index's
  `FileAnalysis.Diagnostics` in place. This preserves OQ-2(a)'s intent (the diagnostic surfaces on the
  referencing file's `publishDiagnostics` — the server merges resolution diagnostics with parser
  diagnostics) while keeping `Resolve` non-mutating and idempotent under the recompute-on-load model
  (OQ-1(a)). Feature 13 owns broader diagnostics policy/formatting later; this feature only *produces*
  the modeled ambiguity outcome, kept a disjoint channel from parser diagnostics.
- **OQ-3 → (a)** "current library" = longest-prefix match of the file's workspace-relative path against
  each `config.Library.Path` (declared order); a file under no declared path → flat namespace. `SYSTEM`
  is an implicit terminal steplib.
- **OQ-4 → dynamic is sufficient** (settled in feature 06 — `&`-placeholder downgraded to `*_DYNAMIC`
  at extraction). Task 8c only asserts it.
- **OQ-5 → NON-transitive steplib chain** (confirmed by `natural-expert` against Software AG runtime
  docs, 2026-06-30): resolve `current library → declared steplibs in declared order → implicit SYSTEM`;
  do **not** follow a steplib's own steplibs. `RUN program-id library-id` resolves against that one
  library only, bypassing the chain. (Matches natls; corrected a prior wrong KB entry.)
- **OQ-6 → out of scope:** `.NS7` user-defined function calls (no `EdgeEntry` kind exists).

## Open questions (resolved above — retained for context)

**OQ-1 — Where does the resolved target live? (central contract decision)**
`EdgeEntry.Target` is a bare `Range` with no file/object identity. Options:
  - **(a) Separate resolution index** (recommended default): a `Resolver`/`ResolutionResult` type in
    `internal/workspace` that maps each edge (by referencing file + `Source` range) to a resolved target
    `{Path, ObjectType}` or an unresolved/ambiguous outcome. `internal/model` and the cache are
    untouched; resolution is recomputed from cached edges on load. Lowest blast radius; no cache bump.
  - **(b) New field on `EdgeEntry`** (e.g. `ResolvedPath string` / `Outcome`). Pollutes the per-file
    extraction contract with cross-file state, and **forces a cache-format bump** (0.3.0 → 0.4.0) plus
    `cacheEntry` migration. Persists resolution but couples extraction and resolution.
  - **(c) New `internal/model` resolution type** (e.g. `model.Resolution`) stored alongside `Edges` in
    the index/cache. Model-pure but **forces a cache bump** and a `review-seam`.
The plan is written for **(a)**. Confirm or redirect; Task 5/6 land the chosen representation.

**OQ-2 — Where do resolution-time (ambiguity) diagnostics surface? (Story 6 / FR-5 / FR-31)**
Per-file diagnostics today flow `FileAnalysis.Diagnostics` → server `publishDiagnostics`. Resolution is
cross-file. Options: (a) resolution appends a `model.Diagnostic` to the *referencing* file's analysis in
the index (reuses the existing LSP channel, range = the call-site `Source`); (b) resolution emits a
separate diagnostic stream the server publishes independently. The plan assumes **(a)**. Also confirm the
split with feature 13 (diagnostics): this feature should *produce* the ambiguity diagnostic as a modeled
resolution outcome; feature 13 owns the broader diagnostics policy/formatting. Confirm the boundary.

**OQ-3 — How is "current library" determined for a file?**
Plan assumes: match the file's workspace-relative path against each `config.Library.Path` (declared
order; longest-prefix wins); a file under no declared `Path` belongs to the flat namespace. `SYSTEM` is
an implicit terminal steplib. Confirm this is the intended mapping (vs. e.g. a per-file directive).

**OQ-4 — Relationship type for `&`-placeholder literals (plan's own open question, FR-18).**
Already resolved *at extraction* as `*_DYNAMIC` (verified). Confirm that "represented as dynamic" is the
accepted answer so Task 8 only needs to assert it (vs. introducing a distinct `*_PARAMETRIC` kind, which
would be a model change + cache bump). The plan assumes **dynamic is sufficient**.

**OQ-5 — Steplib-of-steplib recursion depth.** Knowledge note records the real runtime searches
transitively, but natls walks only one steplib level (recorded as unverified). Confirm whether Task 6
resolves through one steplib level or transitively. The plan assumes **declared steplibs in order,
non-recursive** (matches natls), with the question flagged.

**OQ-6 — User-defined function (`.NS7`) calls** (plan's open question). The plan treats function calls as
out of scope for this feature (no `EdgeEntry` kind exists for them yet); flagged, not planned.

---

## Tasks

Ordering: foundation (object-name/library mapping + lookup API) → static binding → scope/order →
navigation/library-qualifier → flat-namespace ambiguity → dynamic/placeholder/channel-separation →
INCLUDE binding + Invalidate migration → integration. Each task is one red → green → refactor slice.

Fixtures live under `testdata/workspace/resolution/<case>/` as multi-file mini-workspaces (sanitized
`.NSx` + a `.natural-lsp.toml` where library mapping matters), per the testdata convention. Build the
index via `workspace.Build`/`BuildWithCache` with the `natural` analyzer, then run the resolver.

---

### Task 1 — Decide & scaffold the resolution result representation (OQ-1)
**Behavior:** Introduce the type(s) that represent a resolved edge outcome, per the OQ-1 decision. Under
default (a): a `ResolutionResult` / per-edge `Resolution` value capturing one of {Resolved(path,
objectType), Unresolved(reason), Ambiguous(candidates)} keyed by (referencing file path, edge `Source`).
No production resolution logic yet — just the type, its zero value, and a constructor, so later tasks
assert against a stable shape.
**Fixtures:** none (pure type).
**Expected result:** a documented, model-pure type in `internal/workspace` (or `internal/model` if OQ-1
→ c) with table-driven tests over its constructors/predicates (e.g. `IsResolved`, `IsDynamic`).
**Reuses/migrates:** if OQ-1 → (b)/(c), this task ALSO bumps `cacheFormatVersion` (0.3.0 → 0.4.0) and
migrates `cacheEntry` save/load — add a sub-task and `review-seam`.
**DoD:** tests pass; `go vet`/`gofmt` clean; type is backend-free; doc comment states the OQ-1 decision.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** OQ-1 answered.

### Task 2 — Object identity: name + library from index key + config (foundation)
**Behavior:** A helper that, given a workspace-relative file path, `FileAnalysis.ObjectType`, and
`config.Config`, derives the object's **name** (filename stem, uppercased — Natural is case-insensitive)
and its **owning library** (match path against `config.Library.Path`, declared order, longest-prefix
wins; empty when no library map / no match → flat namespace). (OQ-3.)
**Fixtures:** `testdata/workspace/resolution/libmap-basic/` — two library dirs (`APP/`, `COMMON/`) each
with one `.NSN`, plus a `.natural-lsp.toml` declaring both. (Reused by Tasks 6–7.)
**Expected result:** table-driven cases: `APP/MYSUB.NSN` → name `MYSUB`, library `APP`; a file outside any
declared path → empty library; case-insensitive stem (`mysub.nsn` → `MYSUB`).
**Reuses/migrates:** `config.Library{Name,Path}`, `model.ObjectType`. No contract change.
**DoD:** table-driven tests incl. no-libmap and unmatched-path cases; deterministic; vet/gofmt clean.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 1 (for result types it may reference).

### Task 3 — Index lookup-by-name API
**Behavior:** Add an `Index` method to look up candidate definitions by object name (optionally filtered
by expected `ObjectType` — CALLNAT→`.NSN`, FETCH→`.NSP`, PERFORM-external→`.NSS`, INCLUDE→`.NSC`),
returning the matching files with their derived library (using Task 2). Built once from `ForEach`;
concurrency-safe (`-race`).
**Fixtures:** reuse `libmap-basic/`; add a `dup-name/` fixture with the same module name in two libraries.
**Expected result:** lookup `MYSUB` returns both `APP/MYSUB.NSN` and `COMMON/MYSUB.NSN` with their
libraries; type filter excludes a same-named `.NSP`. Empty result for unknown name (no error).
**Reuses/migrates:** `Index.ForEach`/`Keys`; Task 2 helper. Additive to the index API — no breakage.
**DoD:** `-race` tests; deterministic (sorted) candidate order; vet/gofmt clean; seam preserved.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 2.

### Task 4 — Resolver skeleton: iterate edges, pass through dynamic/inline (Story 2, 3-inline)
**Behavior:** A `Resolve(idx, cfg)` (or `NewResolver(...).Resolve()`) that walks every file's `Edges` and
produces a `ResolutionResult` (Task 1). In this slice: `*_DYNAMIC` edges → `Unresolved(reason=dynamic)`
with caller context (referencing file + `Source` range + `TargetName`) preserved; `EdgePerforms` with a
non-zero `Target` (inline match) → `Resolved` to *this same file* at that range. No external binding yet.
**Fixtures:** `testdata/workspace/resolution/dynamic-and-inline/` — one `.NSP` with a `CALLNAT #VAR`,
a `PERFORM LOCAL-SUB` matching an inline `DEFINE SUBROUTINE LOCAL-SUB`.
**Expected result:** the dynamic CALLNAT → unresolved (dynamic) outcome, call-site preserved; the PERFORM
→ resolved to the in-file inline definition. No false static edges.
**Reuses/migrates:** the extraction-side inline `Target` (already populated). No re-classification of
dynamic edges (verified already done at extraction).
**DoD:** table-driven over the result; asserts caller context fields; deterministic; vet/gofmt clean.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 1, Task 3.

### Task 5 — Static CALLNAT binding, flat namespace, single match (Story 1, FR-10, M-3, NFR-7)
**Behavior:** Resolve `EdgeCalls` with a literal `TargetName` to the matching `.NSN` via the
lookup API. This slice covers the **flat-namespace, exactly-one-match** case (no library map). Record the
resolved target {path, ObjectType} + the call site (referencing file + `Source`) on the result.
**Fixtures:** `testdata/workspace/resolution/static-call/` — `MAIN.NSP` with `CALLNAT 'MYSUB'`, and
`MYSUB.NSN`. No `.natural-lsp.toml` library map (flat).
**Expected result:** the `CALLS` edge resolves to `MYSUB.NSN`; call site recorded; zero false
relationships. A `CALLNAT 'NOSUCH'` (no matching object) → `Unresolved(reason=no-target)` (FR-17:
modeled outcome, NOT a diagnostic).
**Reuses/migrates:** Task 3 lookup; Task 1 result. Asserts NFR-7 (static call → correct definition).
**DoD:** static-resolves + unresolvable-literal cases both covered; M-3 fixture; vet/gofmt; deterministic.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 4.

> **Adjustment (during impl):** the **explicit-library bypass** sub-behavior moved to **Task 8b**.
> Only `RUN` (`EdgeNavigatesTo`) carries `EdgeEntry.Library`, and navigation edges aren't resolved
> until Task 8 — so the `Library`-bypass logic lands when navigation resolution does. Task 6 covers the
> CALLNAT steplib chain only (current-library-wins, order-matters, steplib-fallback). The
> `explicit-library-bypass/` fixture is created here but asserted in Task 8b.

### Task 6 — Steplib-chain resolution (Story 6, FR-16, FR-5-chain; OQ-3, OQ-5) — explicit-library bypass → Task 8b
**Behavior:** With a library map present, resolve a `TargetName` by searching **current library → declared
steplibs in order → SYSTEM** (non-recursive per OQ-5), returning the first match. When the resolved edge
carries a non-empty `Library` (RUN library-id), resolve against **that library only**, bypassing the
chain. "Current library" from the referencing file's path (Task 2).
**Fixtures:** reuse `dup-name/` (same module in `APP` and `COMMON`) with a `.natural-lsp.toml` where
`APP` declares `steplibs = ["COMMON"]`; provide a second config variant (or second fixture
`dup-name-reordered/`) where the caller's current library differs so the **same name resolves to a
different file** — proving order matters (M-3, NFR-7).
**Expected result:** caller in `APP` → resolves to `APP/MYSUB.NSN` (current library wins over steplib);
a caller whose current library lacks `MYSUB` → resolves to `COMMON/MYSUB.NSN` via steplib. RUN with an
explicit `library-id` resolves to that library's object, ignoring the chain.
**Reuses/migrates:** Task 2 (current-library + library mapping), Task 3 (lookup), `config.Library.Steplibs`
(declared order), `EdgeEntry.Library`.
**DoD:** order-matters fixture proves a different winner under reordering; explicit-library-bypass case;
SYSTEM-fallback case; deterministic; vet/gofmt. **Agents:** tdd-red → tdd-green → tdd-refactor.
**Depends on:** Task 5. **Gated on:** OQ-3, OQ-5.

### Task 7 — Ambiguity diagnostic in flat namespace (Story 6 last criterion, FR-5, FR-31; OQ-2)
**Behavior:** With **no** library map, when a literal `TargetName` matches objects in more than one
location, do **not** silently pick — emit an ambiguous-resolution `model.Diagnostic` (severity
warning/info per FR-31) on the referencing file at the call-site `Source`, and mark the outcome
`Ambiguous(candidates)`. (Surface per OQ-2 decision — default: append to the referencing file's
`FileAnalysis.Diagnostics` in the index.)
**Fixtures:** reuse `dup-name/` **without** a library map (flat) + `MAIN.NSP` calling the duplicated name.
**Expected result:** one diagnostic on `MAIN.NSP` at the CALLNAT site listing the candidates; result =
ambiguous; no arbitrary winner chosen.
**Reuses/migrates:** `model.Diagnostic` (existing); the existing diagnostics→`publishDiagnostics` channel
(OQ-2). No new model type if OQ-2 → (a).
**DoD:** exactly-one-diagnostic assertion; message names candidate libraries/paths; deterministic ordering
of candidates; vet/gofmt. **Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 6.
**Gated on:** OQ-2.

### Task 8 — External PERFORM fallback + navigation (FETCH/RUN) + placeholder/dynamic confirmation (Story 3-external, Story 5, Story 8; FR-12 M-4, FR-14, FR-15, FR-18)
> Splits into 8a/8b/8c if any sub-slice grows past one red-green loop.

**8a — External PERFORM fallback (Story 3, FR-12, M-4):** an `EdgePerforms` edge with a **zero** `Target`
(no inline match) resolves to an external `.NSS` of the same name via the chain (Task 6).
Fixture `perform-external/`: `MAIN.NSP` with `PERFORM SHARED-SUB` (no inline def) + `SHARED-SUB.NSS`;
plus a variant `perform-inline-wins/` with BOTH an inline `DEFINE SUBROUTINE SHARED-SUB` and an external
`SHARED-SUB.NSS` — proves inline wins (already bound at extraction → resolution leaves it in-file), and
removing the inline makes it resolve externally (M-4 acceptance criterion).
**8b — Navigation statements + explicit-library bypass (Story 5, FR-14/FR-15; Story 6 criterion 3):**
`EdgeNavigatesTo` (literal FETCH/RUN) resolves to a `.NSP`, recorded as a navigation relationship
*distinct* from a `CALLS` relationship (the `EdgeKind` already distinguishes them — assert the resolved
outcome carries the navigation kind). `*_DYNAMIC` navigation → unresolved (dynamic), caller context
preserved (consistent with Task 4). **Explicit-library bypass (moved here from Task 6):** when a RUN
edge's `Library` is non-empty, resolve against THAT library only, bypassing the steplib chain — assert
via the `explicit-library-bypass/` fixture (`RUN 'PGM' 'COMMON'` in current-library APP → resolves to
`COMMON/PGM.NSP`, not `APP/PGM.NSP`). Fixture `navigation/`: `MAIN.NSP` with `FETCH 'TARGETPG'` +
`TARGETPG.NSP`, and a `FETCH #VAR`.
**8c — Placeholder confirmation (Story 8, FR-18; OQ-4):** an edge whose `TargetName` contained `&`
arrives already as `*_DYNAMIC` (extraction downgrade — verified). Assert resolution **never** binds it to
a `'PRG&LANG'`-named object and records it as dynamic/unresolved, caller context preserved.
Fixture `placeholder/`: `MAIN.NSP` with `CALLNAT 'PRG&LANG'` and NO object named literally that way.
**Expected result:** external PERFORM resolves to `.NSS`; inline beats external; navigation resolves to
`.NSP` with the navigation kind; dynamic/placeholder targets stay unresolved with no false edge.
**Reuses/migrates:** Tasks 3/6 (lookup + chain), extraction's existing dynamic classification & inline
`Target`. **DoD:** each sub-behavior fixture; M-4 remove-inline case; no false static edge for 8c;
deterministic; vet/gofmt. **Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 6.
**Gated on (8c):** OQ-4.

### Task 9 — INCLUDE binding + migrate `Index.Invalidate` to resolved targets (Story 4, FR-13)
**Behavior:** Resolve `EdgeIncludes` literal `TargetName` to a `.NSC` copycode object (always resolvable
when available; never dynamic — INCLUDE is exempt from the `&` downgrade, verified). Then **migrate
`Index.Invalidate`** so its dependent-detection uses resolved copycode→path binding instead of the
current `edge.TargetName == path` string compare (flagged divergence above), keeping its existing
transitive-BFS behavior and tests green.
**Fixtures:** `testdata/workspace/resolution/include/` — `MAIN.NSP` with `INCLUDE SHARED` + `SHARED.NSC`;
plus a transitive case (`A.NSP` includes `B.NSC`, `B.NSC` includes `C.NSC`) reusing/adapting feature 05's
invalidation fixture.
**Expected result:** INCLUDE resolves to `SHARED.NSC`; an unavailable copycode → unresolved (modeled,
not a diagnostic, FR-17); changing `C.NSC` returns `{A, B}` from `Invalidate` via resolved targets.
**Reuses/migrates:** **migrates** `Index.Invalidate` (existing consumer of `EdgeIncludes`) — its current
tests must stay green. Task 3 lookup.
**DoD:** include-resolves + transitive-invalidate cases; existing `index_test.go` invalidation tests still
green; `-race`; deterministic; vet/gofmt; seam preserved.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Task 5.

### Task 10 — Channel-separation corpus (Story 7, FR-17, NFR-6, M-6)
**Behavior:** An integration-style test over a representative mini-corpus asserting the two-channel
invariant: every reference is **either** a resolution outcome (resolved / unresolved-dynamic /
unresolved-no-target / ambiguous) **or** a parse-error diagnostic — never both, never silently dropped.
**Fixtures:** `testdata/workspace/resolution/corpus/` — one workspace mixing: a resolvable static call, a
dynamic call, a literal call with no target, an ambiguous call, a `&`-placeholder call, and a genuinely
malformed `CALLNAT` (no operand → parser diagnostic, no edge). 
**Expected result:** counts reconcile — N statement-like lines = (resolution outcomes) + (parse
diagnostics), with the malformed line accounted for as a diagnostic only and the unresolvable/dynamic
ones as outcomes only.
**Reuses/migrates:** all prior tasks; the parser's existing diagnostics (no edge emitted for malformed —
verified in `extractEdges` guards).
**DoD:** reconciliation assertion (no line unaccounted); M-6/NFR-6 fixture; deterministic; vet/gofmt.
**Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Tasks 5–9.

### Task 11 — Persistence/recompute integration & docs anticipation
**Behavior:** Verify resolution behaves correctly across a cache round-trip: under OQ-1 (a), resolution
recomputes from cached `Edges` on `BuildWithCache` load and yields identical results to a cold build;
under (b)/(c) verify the bumped cache format persists/restores resolution. Wire any `resolution.go`
public entry point the later LSP-query features (find-references, call hierarchy) will call, keeping it
behind `internal/workspace` (LSP layer still depends only on the seam).
**Fixtures:** reuse `static-call/` + `libmap-basic/` through a `BuildWithCache` save/load cycle.
**Expected result:** cold-build resolution == cache-loaded resolution; (if bump) version handling forces
rebuild on mismatch.
**Reuses/migrates:** `BuildWithCache`, `Save`/`Load`. **DoD:** round-trip equality test; `-race`;
vet/gofmt; `CLAUDE.md`/`README.md` "Project state" + architecture notes updated at finalize (resolution
now implemented). **Agents:** tdd-red → tdd-green → tdd-refactor. **Depends on:** Tasks 5–10.

---

## Remediation tasks (from /review-feature round 1)

Each is regression-first: RED writes a failing test that reproduces the finding, GREEN fixes it.

### R1 — [MAJOR, review-extraction] Flat-namespace fallback unreachable when a library map is present
**Finding:** `resolveByName` branches solely on `hasLibraryMap := len(cfg.Resolution.Libraries) > 0`. When
a map exists but the referencing file is under NO declared library path, `objectIdentity` returns
`currentLibrary == ""` → `buildSearchChain("")` returns an empty chain → `resolveViaChain` returns nil →
`Unresolved(ReasonNoTarget)`, even when the target genuinely exists. This contradicts OQ-3(a) ("a file
under no declared path → flat namespace") and `buildSearchChain`'s own doc ("callers must treat an empty
chain as the flat-namespace path"). Real NaturalONE exports have root/undeclared-dir files → their calls
all silently fail to bind.
**RED:** fixture `libmap-plus-undeclared/`: library map declares `APP`/`COMMON`; a caller at an
UNDECLARED path (e.g. `SCRATCH/CALLER.NSP`) does `CALLNAT 'ONLYSUB'` where `ONLYSUB.NSN` exists exactly
once (e.g. in `APP/`). Assert it RESOLVES (currently returns false no-target). Add a multi-match variant
under an undeclared path → expect `Ambiguous` (+ diagnostic), matching the flat-namespace rule.
**GREEN:** in `resolveByName`, when the derived `currentLibrary == ""` / `searchChain` is empty even
though a library map exists, fall through to the flat-namespace resolution branch (single→Resolved,
multi→Ambiguous+diagnostic, zero→no-target). **DoD:** new fixture(s) resolve/ambiguate correctly; all
prior tests green; `-race`/gofmt/vet clean.

### R2 — [minor, review-robustness] `resolveByName` does not guard an empty `TargetName`
**Finding:** with no empty-target guard, `nameIndex[""]` matches any indexed empty-stem file (e.g. a
dotfile `A/.NSN`), so an edge with empty `TargetName` could falsely resolve. Currently defended only by
the upstream extractor's `Target == ""` skip — the resolver doesn't defend its own boundary (FR-43
defense-in-depth).
**RED:** unit test that builds an index containing an empty-stem file and calls the resolver path (or
`resolveByName` directly) with an empty `TargetName`; assert `Unresolved(ReasonNoTarget)`, NOT a false
Resolved. **GREEN:** early guard in `resolveByName` — empty (uppercased) target → `Unresolved(ReasonNoTarget)`.

### R3 — [minor, review-robustness] No committed `FuzzResolve` never-panic guard
**Finding:** the widened resolution surface (`Resolve`/`resolveByName`/`objectIdentity`/`buildSearchChain`/
`resolveViaChain`/migrated `Invalidate`) consumes parser-derived `EdgeEntry` values but has no fuzz/
never-panic regression guard (the project has `FuzzParse`/`FuzzLoad`/`FuzzProcessFile`).
**RED/GREEN:** add `FuzzResolve` that builds an in-memory `Index` from arbitrary edge/path bytes and
asserts `Resolve` never panics and always returns a non-nil `*ResolutionSet`; seed with a few corpus
cases. (Behavior already correct — this is a committed guard; if it surfaces a crasher, fix it.)

### R4 — [minor, review-acceptance] Corpus reconciliation is lenient
**Finding:** the Task-10 corpus test pins `resolutionOutcomes==extractedEdges` and `total==6` but does
not independently assert `extractedEdges==5` and `parserDiagnostics==1`; a parser regression that turned
the malformed bare `CALLNAT` into an edge (no diagnostic) would still pass.
**RED/GREEN:** tighten `TestResolve_ChannelSeparationCorpus_Task10` with hard assertions
`extractedEdges==5` and `parserDiagnostics==1` (replace the non-failing `t.Logf`s).

### R5 — [minor, review-concurrency] New read paths lack a committed concurrent test
**Finding:** only `buildNameIndex` is exercised under `-race` concurrently (same-key writer). `Resolve`,
`LookupByName`, and the migrated `Invalidate` are not driven concurrently by the committed suite.
**RED/GREEN:** add a `-race` test driving `Resolve`/`LookupByName`/`Invalidate` against concurrent `Add`
writers with varying keys/edge content; assert no race and non-nil results.

(review-docs CONCERNS = documentation drift → synced in /finalize-feature, not a code fix. review-seam
nit = tasks.md OQ-2 wording, already reconciled. review-extraction Invalidate-mechanism nit and
acceptance SYSTEM-positive-path/nav-kind notes = no code change required.)

---

## Traceability (criterion → task)

| Story / FR | Tasks |
|---|---|
| Story 1 / FR-10, M-3, NFR-7 | 2, 3, 5, 6 |
| Story 2 / FR-11 (dynamic, already classified at extraction) | 4, 10 |
| Story 3 / FR-12, M-4 (inline already bound; external fallback) | 4 (inline), 8a |
| Story 4 / FR-13 (INCLUDE; Invalidate already wired) | 9 |
| Story 5 / FR-14, FR-15 (navigation static/dynamic) | 8b |
| Story 6 / FR-16, FR-5 (steplib chain, explicit lib, flat-ns ambiguity) | 2, 6, 7 |
| Story 7 / FR-17, NFR-6, M-6 (unresolvable vs unrecognized) | 5, 7, 10 |
| Story 8 / FR-18 (placeholder — already dynamic at extraction) | 8c |
| FR-31 (ambiguity diagnostic) | 7 |
| Cache/persistence (links to feature 05) | 1 (if bump), 9, 11 |

Already satisfied at extraction (no task, asserted only): dynamic classification of `*_DYNAMIC` edges
(Story 2/5); `&`-placeholder downgrade (Story 8/OQ-4); inline-PERFORM `Target` binding (Story 3 inline
half); RUN library-id capture (Story 5/6 input); INCLUDE-driven transitive `Invalidate` (Story 4
re-analysis half — refined by Task 9).

---

## Reviews required (for `/review-feature`)

- **review-correctness** — resolution correctness is NFR-7, the top quality bar (steplib order, inline-vs-
  external, no false edges, no silent drops).
- **review-seam** — required if OQ-1 → (b)/(c) (new/changed `internal/model` or `EdgeEntry`), or whenever
  a cache-format bump lands. Confirm LSP layer still depends only on `analysis.Analyzer` + `model`.
- **review-concurrency** — Task 3 lookup index and any resolver state touch the indexer; `-race`.
- **review-robustness** — resolution over malformed/missing-target corpora (Task 10); graceful
  degradation (no panic on unresolved/missing/ambiguous; FR-43 already at index level).
- **review-performance** — name-lookup index is built over the whole workspace and consulted per edge
  (indexing-adjacent hot path); confirm linear behavior (NFR-1/NFR-4 spirit).
- **review-docs** — feature changes capability (resolution now implemented); anticipate `CLAUDE.md` /
  `README.md` "Project state" + "Parser-based extraction"/resolution notes sync at `/finalize-feature`.
