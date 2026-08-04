# Feature 32 — Document Links (`textDocument/documentLink`) — Task Plan

**Spec:** `docs/plans/features/32-document-links/plan.md`
**PRD:** FR-59 (document links — new); relates to FR-10/FR-12/FR-13 (call/include/navigation edges), FR-17
(modeled gaps), FR-43 (graceful degradation), ADR-008 (encoding-aware positions).
**Depends on (all shipped):** feature 06 (edges), feature 07 (resolution / `ResolutionSet`), feature 10
(location conversion — `position.go`, `uriToRelPath`, `uri.File(filepath.Join(root, path))`), feature 19
(json/v2 `marshalResult`).

**Scope:** server-layer only. **No `internal/model` change, no cache-format bump** (stays `0.9.0`). One new
LSP capability (`documentLinkProvider`) → the locked `TestInitialize` allow-list gains one entry.

---

## Current-state findings & impact

Planned against the code as it actually is (verified this run), not the README.

1. **The provider does not exist yet.** There is no `documentLinkProvider` capability, no
   `textDocument/documentLink` dispatch case, and no `internal/server/document_links.go`. This is net-new
   server wiring over existing extraction/resolution — no foundation to build first.

2. **Every seam this feature needs already exists and is reused verbatim** (no contract changes, no
   consumer migrations):
   - **Edges + kinds** — `model.EdgeEntry{Source, TargetName, Kind, Library}`, kinds `EdgeCalls` /
     `EdgeCallsDynamic` / `EdgeIncludes` / `EdgePerforms` / `EdgeNavigatesTo` / `EdgeNavigatesToDynamic`
     (`internal/model/model.go`, `internal/analysis/natural/calls.go`). `EdgeEntry.Source` was widened by
     features 10/12 to span the **whole statement through the target name** (`stmtRange(startPos, stmtEnd)`
     in `calls.go`) — see OQ-A on what that means for the link range.
   - **Resolution lookup** — `res.Get(relPath, edge.Source) (Resolution, bool)` keyed by (referencing file,
     edge `Source`); `Resolution.IsResolved()` / `.IsAmbiguous()` / `.IsUnresolved()`, and `Resolution.Path`
     / `.Type` for a resolved outcome (`internal/workspace/resolution.go`). This is exactly the lookup
     `provideDefinition` (`definition.go`) and `referenceSites` (`references.go`) use.
   - **URI/path helpers** — `uriToRelPath(root, uri) (absPath, relPath, err)` and the target-URI idiom
     `uri.File(filepath.Join(root, resolution.Path))` (`definition.go`).
   - **Encoding-aware range conversion** — `toProtocolRange(model.Range, content, enc)` (`position.go`,
     ADR-008), already fuzzed never-panic by `FuzzPositionConversion`.
   - **F7 snapshot + store-first + `marshalResult`** — pattern in `code_lens.go`/`definition.go`:
     `idxResMu.RLock()` to snapshot `idx`/`res`, release before I/O; try `hctx.store.Get` (live buffer)
     first, else `idx.Get` + `os.ReadFile`; marshal via `marshalResult` (json/v2, feature 19).

3. **`DocumentLink.Target` is a bare `*uri.URI` with no range/position** (`go.lsp.dev/protocol@v1.0.0`
   `document_link.gen.go`: `Target *uri.URI` json:"target,omitzero"`). A click opens the target **file**,
   not a specific line. Consequences baked into the plan:
   - Links point at the resolved **object file** URI only (no landing range) — go-to-definition remains the
     precise-landing gesture (this is the "largely redundant, discoverability-only" value the plan states).
   - A **same-file inline `PERFORM`** link would resolve to the *current* document's URI with no position —
     i.e. a link that re-opens the file you're already in. That is useless/confusing, so **inline PERFORM
     gets NO link** (decision in T5). Only an **external** `PERFORM` (resolved to a different `.NSS`) links.

4. **`DocumentLink[] | null` empty sentinel** — matching the dispatch convention of the closest sibling
   provider `textDocument/codeLens` (also document-anchored, edge-derived, spec type `X[] | null`): the
   provider returns `nil` for "no links" and the dispatch emits the `null` sentinel (`respResult =
   []byte("null")`), exactly as the `codeLens`/`definition` cases do. Decided in T1 (see OQ-B).

5. **Doc divergence (flag, do not act on here):** `CLAUDE.md` still lists features **31–34** as "planned",
   but `internal/server/server.go` already advertises `DeclarationProvider`/`TypeDefinitionProvider` and
   `server_test.go`'s `requiredProviders` already locks `declarationProvider`/`typeDefinitionProvider` —
   i.e. **feature 31 is shipped**. This plan targets 32 only; `/finalize-feature` should reconcile the
   "Project state" note.

6. **Fixtures already cover almost every case — reuse, don't create.** No new fixtures needed except one
   small encoding fixture (T9). Available resolved/gap fixtures:
   - `internal/server/testdata/callhierarchy/` (flat namespace, no library map): `CALLER.NSP` has
     `CALLNAT 'CALLEE'` → `CALLEE.NSN` (resolved), `FETCH 'PGM'` → `PGM.NSP` (resolved, `EdgeNavigatesTo`),
     `PERFORM SUB-A` (inline, same-file), `CALLNAT #DYN` (dynamic), `INCLUDE CC` → `CC.NSC` (resolved).
   - `internal/server/testdata/roothandshake/` (flat namespace, sentinel `.natural-lsp.toml`): `HELLO.NSP`
     has `CALLNAT 'CALLGREET'` → `CALLGREET.NSN` **and** external `PERFORM SAYHELLO` → `SAYHELLO.NSS` — two
     resolved links in one file (ideal for the multi-link ordering test).
   - `internal/server/testdata/references/multi-caller/`: `CALLER2.NSP` has `RUN 'CALLER1'` → `CALLER1.NSP`
     (resolved `EdgeNavigatesTo` via RUN).
   - `internal/server/testdata/ambiguity/`: `CALLER.NSP` `CALLNAT 'DUP'` → two `DUP.NSN` (flat-namespace
     **Ambiguous**).
   - `internal/server/testdata/navigation/unresolved.NSP`: `CALLNAT #SUB-NAME` (dynamic) + `CALLNAT
     'MISSING'` (no-target).

**Net:** this is a genuinely thin, single-file provider (`document_links.go`) with a pure builder. No
cursor decoding needed (document-wide, unlike definition/hover), which makes it simpler than every prior
provider.

---

## Tasks

Each task is TDD: run **`tdd-red`** (write the failing test first), then **`tdd-green`** (minimal code to
pass), then **`tdd-refactor`** where noted. All tests live in `internal/server/`; the provider lives in a
new `internal/server/document_links.go`. Run `just verify` as the gate.

### T1 — Advertise the capability, lock it, and wire an empty provider

**Pins:** Story 1 AC4 (capability advertised, `resolveProvider:false`, `TestInitialize` updated); OQ-1
(eager, no resolve); the `null` empty sentinel (OQ-B).

**Behavior:** `initialize` advertises `documentLinkProvider = DocumentLinkOptions{ResolveProvider:
&falseVal}` (reuse the existing `falseVal` local in `handleInitialize`, `server.go`). A new
`provideDocumentLink(hctx, protocol.DocumentLinkParams) ([]protocol.DocumentLink, error)` is added returning
`nil, nil` for now, and a `case "textDocument/documentLink"` dispatch (gated on `stateInitialized`, decode
`DocumentLinkParams`, on `nil` result emit `respResult = []byte("null")`, else `marshalResult` — copy the
`codeLens` case structure at `server.go` ~L1315).

**RED:** extend `server_test.go` `TestInitialize` `requiredProviders` with `"documentLinkProvider"` and
assert it is present in the advertised `capabilities` map (currently absent → fails). Also assert the
`documentLinkProvider` value is an **object** carrying `resolveProvider: false` (not a bare boolean), mirroring
the `codeLensProvider` shape assertion already in that test.

**GREEN:** add the capability field, the `provideDocumentLink` stub, and the dispatch case.

**DoD:**
- [ ] `documentLinkProvider` advertised as `DocumentLinkOptions{ResolveProvider: false}`.
- [ ] `TestInitialize` `requiredProviders` includes `documentLinkProvider`; shape asserted (object, resolve=false).
- [ ] Dispatch case decodes `DocumentLinkParams`, gates on `stateInitialized`, emits `null` on nil result via `marshalResult` path.
- [ ] `just verify` green.

---

### T2 — Resolved `CALLNAT` link (Story 1 AC1)

**Pins:** AC1 — a resolved module target span becomes a `DocumentLink` whose `Target` is the resolved
object's URI.

**Fixture (reuse):** `testdata/callhierarchy/` built into an `Index` + `Resolve` (flat namespace). Follow
the harness in `code_lens_test.go` (analyze each file, `idx.Add(relPath, analysis)`, `workspace.Resolve`).

**Expected:** `provideDocumentLink` on `CALLER.NSP` returns a link for `CALLNAT 'CALLEE'` (line 12):
- `Range == toProtocolRange(edge.Source, content, enc)` — the `EdgeCalls` edge's `Source` span (see OQ-A;
  the whole `CALLNAT 'CALLEE' #A` statement span through the target name).
- `Target == uri.File(filepath.Join(root, "CALLEE.NSN"))` (the resolved `resolution.Path`, no range).
- Exactly one link for this edge; no link for the `CALLNAT #DYN` / `INCLUDE` / `FETCH` / `PERFORM` lines yet
  is asserted here (those are their own tasks) — assert the CALLEE link is present with the exact Range/Target.

**Implementation:** in `provideDocumentLink`, resolve `absPath`/`relPath` via `uriToRelPath`; F7-snapshot
`idx`/`res`; store-first then index+`os.ReadFile` for content; iterate `fa.Edges`; for each edge call
`res.Get(relPath, edge.Source)`; when `resolution.IsResolved()`, build a `DocumentLink{Range:
toProtocolRange(edge.Source, content, enc), Target: &targetURI}` with `targetURI =
uri.File(filepath.Join(hctx.root, resolution.Path))`. Extract a **pure** `buildDocumentLinks(fa, res,
relPath, root, content, enc) []protocol.DocumentLink` helper (no I/O, no locks) — this is the fuzz/refactor
target in T11.

**DoD:**
- [ ] Resolved `CALLNAT` → one link, exact `Range` (== `edge.Source` converted) and `Target` URI.
- [ ] Pure `buildDocumentLinks` helper introduced (I/O-free).
- [ ] `just verify` green.

---

### T3 — Resolved `INCLUDE` copycode link (Story 1 AC1)

**Pins:** AC1 — copycode target (`EdgeIncludes`) links to the resolved `.NSC`.

**Fixture (reuse):** `testdata/callhierarchy/CALLER.NSP` `INCLUDE CC` (line 24) → `CC.NSC`.

**Expected:** a link with `Range == toProtocolRange(<INCLUDE CC edge.Source>)` and `Target ==
uri.File(filepath.Join(root, "CC.NSC"))`. (`EdgeIncludes` `Source` = `stmtRange(include.StartPos,
include.EndPos)`.) Confirms the resolved branch is edge-kind-agnostic — it keys off the resolution outcome,
not the kind.

**DoD:**
- [ ] Resolved `INCLUDE` → one link to the `.NSC` URI, exact Range.
- [ ] `just verify` green.

---

### T4 — Resolved `FETCH` / `RUN` navigation link (Story 1 AC1)

**Pins:** AC1 — program-transfer targets (`EdgeNavigatesTo`) link to the resolved `.NSP`.

**Fixtures (reuse):**
- `FETCH`: `testdata/callhierarchy/CALLER.NSP` `FETCH 'PGM'` (line 15) → `PGM.NSP`.
- `RUN`: `testdata/references/multi-caller/CALLER2.NSP` `RUN 'CALLER1'` → `CALLER1.NSP` (built as its own
  index over `testdata/references/multi-caller/`).

**Expected:** each → one link with `Range == toProtocolRange(edge.Source)` and `Target ==
uri.File(root/PGM.NSP)` / `uri.File(root/CALLER1.NSP)` respectively. `FETCH` and `RUN` share the
`EdgeNavigatesTo` kind, so one test covering both proves the navigation kind; assert both explicitly.

**DoD:**
- [ ] Resolved `FETCH` → link to `.NSP` URI, exact Range.
- [ ] Resolved `RUN` → link to `.NSP` URI, exact Range.
- [ ] `just verify` green.

---

### T5 — External `PERFORM` links; inline `PERFORM` does **not** (Story 1 AC1 + decision)

**Pins:** AC1 external-subroutine link; the **inline-PERFORM = no link** decision (finding 3).

**Fixtures (reuse):**
- External: `testdata/roothandshake/HELLO.NSP` `PERFORM SAYHELLO` (line 15) → `SAYHELLO.NSS`.
- Inline: `testdata/callhierarchy/CALLER.NSP` `PERFORM SUB-A` (line 18) — resolves to the **same file**
  (`resolution.Path` == the caller's own relPath, via the same-object subroutine).

**Expected:**
- External `PERFORM SAYHELLO` → one link, `Target == uri.File(root/SAYHELLO.NSS)`, exact Range.
- Inline `PERFORM SUB-A` → **NO link** (asserted: the returned slice contains no link whose Range covers
  line 18). Rationale (assert in a comment): `DocumentLink.Target` is a bare URI with no position, so a
  same-file target link only re-opens the current document — no navigation value; go-to-definition covers
  the same-file jump precisely.

**Implementation:** in `buildDocumentLinks`, after a resolved outcome, **skip** the link when
`paths.NormalizeKey(resolution.Path) == relPath` (same-file target) — reusing the same normalized-compare
`definition.go` already does for its inline-PERFORM branch.

**DoD:**
- [ ] External `PERFORM` → link to `.NSS` URI, exact Range.
- [ ] Inline (same-file) `PERFORM` → no link; same-file skip implemented via `paths.NormalizeKey` compare.
- [ ] `just verify` green.

---

### T6 — Modeled gaps: dynamic, unresolved (no-target), ambiguous → **no link** (Story 1 AC2, FR-17)

**Pins:** AC2 / FR-17 — a link to nowhere is worse than none.

**Fixtures (reuse):**
- Dynamic: `testdata/navigation/unresolved.NSP` `CALLNAT #SUB-NAME` (`EdgeCallsDynamic`) — and/or
  `testdata/callhierarchy/CALLER.NSP` `CALLNAT #DYN`.
- No-target: `testdata/navigation/unresolved.NSP` `CALLNAT 'MISSING'` (`Unresolved(ReasonNoTarget)`).
- Ambiguous: `testdata/ambiguity/CALLER.NSP` `CALLNAT 'DUP'` (flat-namespace `Ambiguous`, two `DUP.NSN`) —
  build the `ambiguity/` tree with no library map so `Resolve` yields `IsAmbiguous()`.

**Expected:** `provideDocumentLink` returns **no link** for any of these sites. For `unresolved.NSP` (only a
dynamic + a no-target edge, both gaps) the result is `nil` → `null` on the wire. For the ambiguous fixture,
the `CALLNAT 'DUP'` line produces no link (per OQ-1: prefer no link over an arbitrary candidate pick).

**Implementation:** `buildDocumentLinks` only emits when `resolution.IsResolved()`; `IsUnresolved()`
(dynamic or no-target) and `IsAmbiguous()` fall through with no link. (No special-casing of edge kind
needed — the resolution outcome is the sole gate.)

**DoD:**
- [ ] Dynamic edge → no link.
- [ ] Unresolved no-target literal → no link.
- [ ] Ambiguous flat-namespace literal → no link (no arbitrary pick).
- [ ] A document with only gaps → `provideDocumentLink` returns `nil` (→ `null`).
- [ ] `just verify` green.

---

### T7 — Multiple resolved links in one document, sorted by position (Story 1 AC1)

**Pins:** AC1 — a document with several resolved targets yields all links, deterministically ordered.

**Fixture (reuse):** `testdata/roothandshake/HELLO.NSP` — `CALLNAT 'CALLGREET'` (line 12) and external
`PERFORM SAYHELLO` (line 15) both resolve.

**Expected:** exactly two links, **sorted by `Range.Start`** (line 12 link before line 15 link), with
`Target`s `uri.File(root/CALLGREET.NSN)` and `uri.File(root/SAYHELLO.NSS)` respectively. Assert the order is
stable/deterministic (sort by `Range.Start.Line` then `.Character`, mirroring `referenceSites`).

**Implementation:** `buildDocumentLinks` builds in edge iteration order then `sort.Slice` by `Range.Start`
(edges are already source-sorted by `extractEdges`, but sort explicitly for a documented invariant).

**DoD:**
- [ ] Two resolved edges → two links in `Range.Start` order.
- [ ] Deterministic ordering asserted.
- [ ] `just verify` green.

---

### T8 — Store-first (live buffer) resolution (Story 1 AC1; FR-43 liveness)

**Pins:** links reflect the **open buffer** content, not stale disk, matching every other provider.

**Fixture (reuse):** `testdata/callhierarchy/CALLER.NSP` opened via a `document.Store` (inject an
`AnalyzeFunc`), then served.

**Expected:** with the URI open in the store, `provideDocumentLink` reads the buffer's `Analysis`/`Content`
first (not `os.ReadFile`) and returns the same resolved links as T2 from the live content. A minimal assertion:
open the buffer with content identical to disk and confirm the CALLEE link is produced via the store path
(e.g. assert it still works when the on-disk file is absent but the buffer is present, mirroring the
disk-free proof style used elsewhere), demonstrating store-first precedence.

**Implementation:** in `provideDocumentLink`, `hctx.store.Get(uri)` first (use `doc.Analysis` + `doc.Content`),
falling back to `idx.Get` + `os.ReadFile`; the resolution snapshot (`res`) still comes from the F7 `idx/res`
snapshot (resolution is index-wide). Mirror `code_lens.go`'s store-first ordering.

**DoD:**
- [ ] Open buffer served from the store (not disk); links correct from live content.
- [ ] Index+disk fallback still works when the doc is not open (covered by T2–T7).
- [ ] `just verify` green.

---

### T9 — Encoding-awareness: UTF-8 vs UTF-16 link ranges (Story 1 AC3, ADR-008)

**Pins:** AC3 — links are encoding-aware and correct under both negotiated encodings.

**Fixture (new — the only new fixture):** `internal/server/testdata/documentlinks/ENCODING.NSP` — a program
with a **multi-byte character before an edge on the edge's line** so the UTF-8 byte column differs from the
UTF-16 code-unit column (e.g. a comment/label containing an accented char or emoji on the same line as, or
the target literal adjacent to, a resolved `CALLNAT 'X'`). Add a resolved target object (e.g. reuse/point at
a small `.NSN` in the same dir, or a two-file `documentlinks/` set: `ENCODING.NSP` + the callee). Keep it
minimal and non-proprietary per the testing convention.

**Expected:** run `provideDocumentLink` with `hctx.posEncoding == UTF-16` and again with `UTF-8`; assert the
link's `Range.Start.Character` / `End.Character` differ per encoding exactly as `toProtocolRange` computes
(the multi-byte char shifts the UTF-8 byte columns vs UTF-16 code units). This proves the provider threads
`content` + `enc` through, not that `toProtocolRange` is correct (that is already fuzzed).

**DoD:**
- [ ] Link `Range` differs correctly between UTF-8 and UTF-16 for a multi-byte-bearing line.
- [ ] New fixture is minimal and sanitized.
- [ ] `just verify` green.

---

### T10 — Graceful degradation (Story 1 AC3, FR-43)

**Pins:** AC3 — missing/unreadable/out-of-root/cold-index → empty result, never an error.

**Cases (assert each returns `nil, nil` → `null`, no panic, no error):**
- URI **outside** the workspace root (`uriToRelPath` returns err) → `nil, nil`.
- File **not in the index** and not in the store → `nil, nil`.
- **Unreadable** file (in index but `os.ReadFile` fails — e.g. a path that no longer exists on disk) → `nil, nil`.
- **Cold index** (`hctx.idx == nil` / `hctx.res == nil`, pre-first-build) and doc not open → `nil, nil`.
- `hctx == nil` guard → `nil, nil`.

**Implementation:** the guards mirror `code_lens.go`/`definition.go` exactly (nil `hctx`, `uriToRelPath`
error, nil `idx`/`res`, `idx.Get` miss, `os.ReadFile` error).

**DoD:**
- [ ] All five degradation cases return empty + nil error (no panic).
- [ ] `just verify` green.

---

### T11 — Refactor + `FuzzProvideDocumentLink` (FR-43)

**Pins:** FR-43 — the builder never panics on arbitrary content/edges/resolution.

**Refactor:** ensure `buildDocumentLinks` is a clean, pure, I/O-free function (the classification/assembly)
with `provideDocumentLink` owning only URI/lock/store/read orchestration — so the builder is directly
fuzzable, matching the `code_lens.go` `buildCallCountLens`/`buildWriteSummaryLens` split.

**Fuzz:** add `FuzzProvideDocumentLink` (in `fuzz_test.go`, following `FuzzProvideCodeLens`/
`FuzzProvideDefinition` seed-and-run style): feed arbitrary content bytes (empty, multi-byte,
CRLF, garbage, Natural snippets) analyzed into a `FileAnalysis`, an arbitrary `ResolutionSet` (including
resolutions pointing at same-file and missing paths), and both encodings; assert the builder/provider
**never panics** and always returns a well-formed slice (each `DocumentLink` has a valid `Range`; `Target`,
when set, is a valid `uri.URI`).

**DoD:**
- [ ] `buildDocumentLinks` is pure/I/O-free and unit-tested in isolation.
- [ ] `FuzzProvideDocumentLink` added; runs clean in the corpus; never panics (FR-43).
- [ ] `just verify` green.

---

## Traceability

| Acceptance criterion (plan.md Story 1) | Task(s) |
|---|---|
| AC1 — link over each **resolved** module/copycode/subroutine target span, Target = resolved object URI | T2 (CALLNAT), T3 (INCLUDE), T4 (FETCH/RUN), T5 (external PERFORM), T7 (multiple, ordered) |
| AC2 — dynamic/unresolved/ambiguous → **no link** (FR-17) | T6 |
| AC3 — encoding-aware; missing/unreadable/out-of-root → empty, no error (FR-43) | T9 (encoding), T10 (degradation), T11 (fuzz) |
| AC4 — `documentLinkProvider` advertised (`resolveProvider:false`); `TestInitialize` updated | T1 |
| Inline PERFORM = no link (finding 3 / DocumentLink has no target range) | T5 |
| Store-first liveness (matches sibling providers) | T8 |

**Out of scope (recorded, per the ask — do not plan):** DDM/view/host-var links; `documentLink/resolve`
(lazy targets) — OQ-1 decided **eager**.

---

## Reviews required (`/review-feature`)

- **review-lsp-spec:** `documentLinkProvider` capability shape (`DocumentLinkOptions{resolveProvider:false}`);
  `textDocument/documentLink` result is a valid `DocumentLink[] | null`; `DocumentLink.Target` is a proper
  file URI; the `null` empty sentinel is spec-legal (OQ-B). Wire bytes via `marshalResult` (json/v2, feature 19).
- **review-analyzer-seam:** `internal/server` continues to depend only on `internal/analysis.Analyzer`,
  `internal/model`, and `internal/workspace` public API — the new file imports no parser internals.
- **review-concurrency:** F7 snapshot correctness — `idx`/`res` snapshotted under `idxResMu.RLock` and
  released before I/O; store path self-synchronized; no torn `(idx,res)` read.
- **review-robustness:** all degradation branches (T10) + fuzz (T11) — never panic, never error on bad input.
- **review-docs:** `CLAUDE.md` "Project state" + capability list gain feature 32; reconcile the stale
  "features 31–34 planned" note (finding 5 — feature 31 is already shipped).

---

## Open questions (genuinely new — the pre-decided OQ-1/OQ-2 are settled: build it, eager, resolve=false,
no link on ambiguity)

- **OQ-A — link `Range` granularity: whole statement (`edge.Source`) vs. target-token sub-range.**
  `EdgeEntry` exposes only `Source` (the widened **whole-statement** span, keyword→target) and `TargetName`
  (a string) — there is **no** target-name-only range on the edge. This plan uses `edge.Source` (robust,
  already the range `references` uses, already fuzzed). The trade-off: the whole `CALLNAT 'X'` statement
  becomes the underlined link region rather than just the `'X'` literal. **Recommendation:** ship with
  `edge.Source`; if the underline reads poorly in editors, add a follow-up refinement that narrows the range
  by scanning `Source` for `TargetName` (a source-reconstruction akin to `qualifierBeforeRef` in
  `definition.go`). Flagging for the reviewer's UX call — not blocking.

- **OQ-B — empty sentinel `null` vs `[]`.** Decided **`null`** to mirror `codeLens` (the closest sibling;
  spec type `DocumentLink[] | null`; the dispatch nil-guard convention from feature 19). Most clients treat
  `null` and `[]` identically for `documentLink`. Confirm `null` is acceptable, or switch the guard to emit
  `[]` (both are one-line changes in the dispatch case) — recorded, not blocking.

- **OQ-C — a link's target opens the file at its top, not the object's declaration line** (inherent:
  `DocumentLink.Target` is a range-less URI). This is why the plan frames document links as
  discoverability-only and keeps go-to-definition as the precise-landing gesture. No action; recorded so the
  redundancy-with-go-to-definition expectation is explicit for reviewers/users.
