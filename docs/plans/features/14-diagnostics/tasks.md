# Feature 14 — Diagnostics — Task Plan

**Spec:** `docs/plans/features/14-diagnostics/plan.md`
**PRD requirements:** FR-30 (surface parse errors as diagnostics), FR-31 (surface ambiguous
resolution), supports FR-17 (modeled gaps are NOT diagnostics), NFR-6 / NFR-14 (never silently
drop), metric M-6 (no silent gaps).
**Depends on:** feature 00 (parser diagnostics), feature 07 (resolution ambiguity diagnostics),
feature 10 (server holds `idx`/`res`, `applyDocumentChange`, F7 build-then-publish).

This feature is **server-side aggregation + push publishing wiring only**. Both diagnostic
*producers* already exist and are tested; nothing is re-planned for them. What is missing is: a
`model.Diagnostic` → `protocol.Diagnostic` converter, an aggregator that merges the two producer
channels, a `textDocument/publishDiagnostics` notification writer, and the wiring into the four
document lifecycle paths with clear-on-fix semantics.

---

## Current-state findings & impact

Surveyed fresh from the code (trust these over the README):

1. **`internal/server/diagnostics.go` is a stub** — package doc + a single
   `// TODO: diagnostic aggregation and textDocument/publishDiagnostics.` The server has **never**
   sent `textDocument/publishDiagnostics`. This is where the new code lands.

2. **Story 1 producer — DONE, do not touch.** `Analyze()`
   (`internal/analysis/natural/analyzer.go`) copies the parser's ranged `Program.Diagnostics` into
   `model.FileAnalysis.Diagnostics []model.Diagnostic`. Parse errors are error-severity; the
   unrecognized-extension note is `DiagnosticInfo`. Blank lines / comments produce none (parser
   property). **New work reuses this channel read-only.**

3. **Story 2 producer — DONE, do not touch.** `ResolutionSet.DiagnosticsFor(filePath)
   []model.Diagnostic` (`internal/workspace/resolution.go`) returns flat-namespace ambiguity
   warnings keyed by referencing file path: `Severity: DiagnosticWarning`, message
   `"ambiguous reference 'X': matches A, B"`, `Range: edge.Source`. Only produced in the
   no-library-map (flat namespace) path — declaring a disambiguating library map removes them via
   the existing `ResolveInto`/`Resolve` recompute (Story 2 AC2 satisfied for free). **Nothing in
   production calls `DiagnosticsFor` yet — this feature is its first caller.**

4. **Model → protocol conversion — MISSING.** `internal/server/position.go` has an encoding-aware
   `toProtocolRange(r model.Range, content string, enc protocol.PositionEncodingKind)` (ADR-008,
   1-based inclusive model → 0-based exclusive protocol). There is **no**
   `model.Diagnostic`→`protocol.Diagnostic` converter and **no**
   `model.DiagnosticSeverity`(`"info"/"warning"/"error"`)→`protocol.DiagnosticSeverity`(1/2/3/4)
   mapper. Library is `go.lsp.dev/protocol`.

5. **Handler context & hooks — READY.** `handlerContext` (`internal/server/server.go`) holds
   `idx`, `res`, `posEncoding`, `store`, `root`, guarded by `idxResMu sync.RWMutex`; the
   established F7 pattern is snapshot-under-RLock, release, then do I/O + writes (see
   `provideHover`). `applyDocumentChange(relPath, content)` re-analyzes, `idx.Add`s, and swaps a
   fresh `ResolveInto` result under the write lock — the didChange + watched-file update path.
   `didOpen`/`didChange` update `store`; `didClose` closes it. The server writes notifications on
   the jsonrpc2 stream (`stream.Write(ctx, jsonrpc2.NewCall(...))` — see the
   `client/registerCapability` block at server.go ~line 423, which is the notification/call-write
   pattern to mirror; publishDiagnostics is a **Notification**, not a Call).

6. **Cache does NOT persist `Diagnostics`.** `cacheEntry`/`Save`/`Load`
   (`internal/workspace/cache.go`) round-trip `ObjectType`, `Symbols`, `Edges`, `DataAccess`,
   `Definitions`, `WorkFiles`, `HostVarRefs`, `Structure` — **not `Diagnostics`**. A cache-loaded
   index has empty `Diagnostics`. However, the server builds its index via `workspace.Build(root,
   cfg, az, logger, nil)` at `initialized` (the **no-cache** path, `cachePath=""`), which runs
   `Analyze` fresh, so the running index **does** carry `Diagnostics`. `applyDocumentChange` and
   `store.Open`/`Update` also re-analyze live. **Consequence:** diagnostics are recomputed on load,
   exactly like resolution (feature 07 OQ-1). **No `model` change and no cache-format bump is
   required** to satisfy this feature. (See Open Question 1 for the `Code`/`Source` distinctness
   question, which is the only thing that *could* force a model change — resolved below as NOT
   needed.)

7. **Capabilities — nothing to advertise.** `textDocument/publishDiagnostics` is a unilateral
   server→client notification with no corresponding *server* capability (the client advertises
   `textDocument.publishDiagnostics`). The `initialize` allow-list in `handleInitialize` and its
   lock in `TestInitialize` therefore **do not change**. (Pull-diagnostics `textDocument/diagnostic`
   *would* add a capability, but the plan is push-based — see Open Question 2.)

**Impact classification of the acceptance criteria:**

| AC | Classification | Where handled |
|----|----------------|---------------|
| S1-AC1 parse error → diagnostic at position | new wiring; producer done | T2 (converter), T4/T6 (publish + server test) |
| S1-AC2 useful message (unexpected token) | already satisfied by parser | T6 asserts message content flows through |
| S1-AC3 blanks/comments → none | already satisfied by parser | T6 regression at publish level |
| S1-AC4 corpus: errors surface, refs retained, none dropped | new wiring + regression | T5 (aggregator dedup/order) + T6 (modeled-gap-free publish) |
| S2-AC1 no-map ambiguity → diagnostic w/ candidates | producer done; first consumer | T5 (aggregate `DiagnosticsFor`), T6 (server test) |
| S2-AC2 library map removes it | free via recompute | T6 (server test: disambiguated → cleared) |
| S2-AC3 ambiguity distinct (severity/category) from syntax | already: warning vs error | T2 preserves severity; T6 asserts distinctness; OQ-1 |
| S3-AC1 update on change (open + external) | new wiring | T6 (didChange/watched publish), T7 |
| S3-AC2 fixing cause clears diagnostic | new wiring (empty-array publish) | T6 (fix-the-line → empty publish) |
| S3-AC3 never include modeled outcomes | verify + regression | T5 + T6 (dynamic/unresolved fixture → zero) |

---

## Tasks (dependency-ordered, TDD red → green → refactor)

Each task runs the three TDD agents (`tdd-red` → `tdd-green` → `tdd-refactor`) unless noted. Keep the
Analyzer seam pure: **all `model`→`protocol` conversion and publishing lives in
`internal/server/`** (like `hover.go` / `document_symbols.go` / `position.go`), never behind the
`Analyzer` interface. New code lands in `internal/server/diagnostics.go` (replacing the stub) and its
test file `internal/server/diagnostics_test.go`.

> **Approval-checkpoint decisions (2026-07-07):**
> 1. **Add a machine-readable category field to `model.Diagnostic`** (OQ-1 — human approved the model
>    change over severity-only). New task **T0** below adds it and sets it at both producers.
> 2. **Clear on close** — didClose publishes `[]` (OQ-3). Baked into T7.
> 3. **Defer the syntax opt-in toggle** — parse-error diagnostics are always on; no config change
>    (OQ-5).

### T0 — Add a category to the `model.Diagnostic` contract; set it at both producers

**Covers:** S2-AC3 (machine-readable distinctness between syntax and ambiguity diagnostics). FR-30,
FR-31. This is a shared-contract change and must land first (T1/T2 map it through).

- **RED:** add a `Code` field (stable machine-readable category string) to `model.Diagnostic` — e.g.
  `Code model.DiagnosticCode` with two constants `DiagnosticCodeSyntax = "syntax"` and
  `DiagnosticCodeAmbiguity = "ambiguity"` (empty allowed for back-compat / uncategorized). Write
  failing tests asserting:
  - The parser/analyzer path stamps `Code = DiagnosticCodeSyntax` on every diagnostic it emits from
    `Program.Diagnostics` (in `internal/analysis/natural/analyzer.go`); the unrecognized-extension
    note keeps its info severity and gets `DiagnosticCodeSyntax` (or a dedicated code — decide in RED,
    but it is a "source problem" category, not ambiguity).
  - The resolver path stamps `Code = DiagnosticCodeAmbiguity` on the flat-namespace ambiguity
    diagnostic in `internal/workspace/resolution.go` (line ~499).
- **GREEN:** add the field + constants to `internal/model/model.go`; set `Code` at the two producer
  sites. Purely additive.
- **REFACTOR:** none expected.
- **DoD:** field + constants exist; both producers set the right code; existing model/analyzer/
  resolver tests still green. **Cache-format check:** `internal/workspace/cache.go` does **not**
  serialize `Diagnostics` (finding 6), so this additive field needs **no cache-format bump** (still
  `0.6.0`) — assert/confirm the cache round-trip test is unaffected. `just verify` clean.

### T1 — Severity mapper: `model.DiagnosticSeverity` → `protocol.DiagnosticSeverity`

**Covers:** S1-AC1, S2-AC3 (preserves the warning-vs-error distinction end to end). FR-30/FR-31.

- **RED:** table test `severityToProtocol(model.DiagnosticSeverity) protocol.DiagnosticSeverity`:
  `DiagnosticError`("error")→`protocol.DiagnosticSeverityError` (1),
  `DiagnosticWarning`("warning")→`protocol.DiagnosticSeverityWarning` (2),
  `DiagnosticInfo`("info")→`protocol.DiagnosticSeverityInformation` (3). Unknown/empty →
  `protocol.DiagnosticSeverityError` **or** a defined safe default (choose Information to be least
  alarming for an unclassified value — decide in RED and assert it; never 0/unset). No fixtures.
- **GREEN:** a pure `switch` in `diagnostics.go`.
- **REFACTOR:** none expected; keep it a small pure function.
- **DoD:** all four severities mapped; unknown handled deterministically; pure (no I/O, no lock);
  `go test ./internal/server` green; `just verify` clean.

### T2 — Converter: `model.Diagnostic` → `protocol.Diagnostic` (encoding-aware, never-panic)

**Covers:** S1-AC1 (position), S1-AC2 (message passthrough), S2-AC3 (severity preserved). FR-30,
FR-31, FR-43.

- **RED:** test `toProtocolDiagnostic(d model.Diagnostic, content string, enc
  protocol.PositionEncodingKind) protocol.Diagnostic`:
  - Range converted via `toProtocolRange` (reuse — do not reimplement); assert a known model
    range on a known `content` line maps to the expected 0-based, end-exclusive protocol range in
    **both** UTF-8 and UTF-16 encodings (mirror the position-test convention).
  - `Message` copied verbatim (S1-AC2: the parser's "unexpected token X" text must survive).
  - `Severity` via `severityToProtocol` (T1).
  - `Source` set to a stable constant string `"natural-lsp"` (identifies the producer to the
    client; harmless, standard).
  - `Code` mapped from `model.Diagnostic.Code` (T0) to `protocol.Diagnostic.Code` — assert a syntax
    diagnostic surfaces `"syntax"` and an ambiguity diagnostic `"ambiguity"` in the protocol value,
    so the two categories are machine-distinguishable client-side (S2-AC3). Empty `Code` → leave the
    protocol `Code` unset.
  - Degenerate input (empty content, out-of-range range, zero-width range) does not panic and
    yields a clamped range (relies on `toProtocolRange`'s existing clamping).
- **GREEN:** compose `severityToProtocol` + `toProtocolRange` into a `protocol.Diagnostic`.
- **REFACTOR:** none expected.
- **DoD:** message/severity/range correct in both encodings; `Source="natural-lsp"`; pure; never
  panics; `just verify` clean.

### T3 — Fuzz guard for the converter/severity mapper (FR-43, project never-panic convention)

**Covers:** FR-43 (graceful degradation), NFR-6.

- **RED:** `FuzzDiagnosticConversion` in `internal/server/` feeding arbitrary
  `Message`/`Severity`/`Range` (line/column arbitrary ints, incl. negative and huge) plus arbitrary
  `content` bytes and both encodings into `toProtocolDiagnostic`; asserts it returns without
  panicking. (Follows `FuzzProvideHover`/`FuzzPositionConversion` precedent.)
- **GREEN:** covered by T1/T2 clamping; add any missing guard the fuzzer finds and a regression
  fixture/case for it.
- **REFACTOR:** none.
- **DoD:** `go test -run xxx -fuzz FuzzDiagnosticConversion -fuzztime 20s ./internal/server`
  finds no crash; committed as a permanent guard; `just verify` clean.

### T4 — Pure aggregator: merge the two producer channels for one file

**Covers:** S1-AC4 (nothing dropped, both channels merged), S2-AC1 (ambiguity included), S3-AC3
(modeled gaps excluded — by construction, since only these two channels are read), M-6.

- **RED:** test `aggregateDiagnostics(fa model.FileAnalysis, resDiags []model.Diagnostic)
  []model.Diagnostic` (pure — takes the file's `FileAnalysis.Diagnostics` and the slice returned by
  `res.DiagnosticsFor(relPath)`, so it is unit-testable with no server/index):
  - Returns the concatenation of `fa.Diagnostics` + `resDiags`.
  - **Order:** sort stably by `Range.Start` (line, then column) so publish order is deterministic
    (mirrors the extractor "global source order" convention); assert two out-of-order inputs come
    back position-ordered.
  - **Dedup:** decide and assert — dedup exact duplicates (same Message+Severity+Range) since a
    file could in principle be both its own referencer and have a matching parser diag; keep it
    simple (map/seen-set on the triple). If RED shows duplicates cannot occur in practice, still
    assert the no-op case. (Record the decision in the test doc comment.)
  - **Empty inputs** → empty (non-nil-vs-nil: return `nil` for no diagnostics so callers can
    publish an empty array — see T5/T6 clearing).
  - **Modeled-gap channel-purity assertion:** a `FileAnalysis` whose `Edges` contain
    `EdgeCallsDynamic`/`NAVIGATES_TO_DYNAMIC` and whose resolution outcomes are
    `Unresolved(dynamic)`/`Unresolved(no-target)` contributes **zero** diagnostics here — because
    neither `fa.Diagnostics` nor `DiagnosticsFor` carries them (verify the resolver invariant holds
    at this layer). This is the S3-AC3 unit-level guard.
- **GREEN:** concat + stable sort + dedup in `diagnostics.go`.
- **REFACTOR:** extract the sort key helper if it clarifies.
- **DoD:** deterministic order; dedup decided + asserted; modeled-gap purity asserted; pure; `just
  verify` clean.

### T5 — `publishDiagnostics` notification writer (URI + protocol diagnostics [+ version])

**Covers:** S3-AC2 (clearing via empty array), S1-AC1 (delivery). FR-30/FR-31, FR-43.

- **RED:** test a writer `publishDiagnostics(ctx, stream, uri protocol.DocumentURI, diags
  []protocol.Diagnostic, version ...)` (exact signature decided in RED; the stream is the jsonrpc2
  conn — inject a capture writer / `bytes.Buffer`-backed stream as the lifecycle tests do). Assert:
  - It writes a JSON-RPC **Notification** with method `"textDocument/publishDiagnostics"` (no id),
    marshaled via `protocol.PublishDiagnosticsParams` (mirror the `MarshalJSONTo` pattern used for
    register options), carrying the given `uri` and `diagnostics`.
  - Passing an **empty** `diags` slice publishes `"diagnostics": []` (not `null`) so the client
    **clears** stale diagnostics for that URI (Story 3 AC2 / LSP replace-semantics — publish is a
    full replace, never a delta).
  - Version: decide whether to thread the open-doc version into
    `PublishDiagnosticsParams.Version` (open buffers have one; on-disk/watched changes do not).
    Recommended: thread it when available (from `store`), omit otherwise; assert both. Record the
    decision in the doc comment.
  - Write failure is logged, not fatal (FR-43) — mirror `sendError`'s log-don't-crash discipline.
- **GREEN:** marshal `PublishDiagnosticsParams`, `stream.Write(ctx, jsonrpc2.NewNotification(...))`.
- **REFACTOR:** none.
- **DoD:** correct method + params shape; empty slice → `[]` clears; version decision made +
  asserted; log-on-write-failure; `just verify` clean.

### T6 — Orchestrator: per-file publish under F7 lock discipline

**Covers:** wires T4 aggregate + T2 convert + T5 write together for a single relPath/URI. Drives
S3-AC1/AC2. FR-43, F7.

- **RED:** test a method e.g. `hctx.publishFileDiagnostics(ctx, stream, uri)` (or a free function
  taking the pieces) that:
  1. Resolves URI → `relPath` via `uriToRelPath` (reuse); URI outside root → no-op.
  2. Snapshots `idx`/`res`/`posEncoding`/`root` under **RLock, then releases** before I/O (F7 —
     mirror `provideHover` lines 249–253).
  3. Obtains the file's `FileAnalysis`: **open-document buffer first** (`store.Get` → live,
     possibly-unsaved analysis, so diagnostics track live edits — Story 3 AC1) then the index
     (`idx.Get(relPath)`); reads content from `store` for open docs else `os.ReadFile(absPath)`
     (needed for `toProtocolRange` encoding-aware column math). Missing/unreadable file → publish an
     **empty** set (clear), never error.
  4. `resDiags := res.DiagnosticsFor(relPath)` (nil-safe — `res` may be nil early).
  5. `aggregateDiagnostics` (T4) → `toProtocolDiagnostic` each (T2) → `publishDiagnostics` (T5).
  - Assert with a captured stream: a source with a parse error publishes one error-severity
    protocol diagnostic at the expected position; a clean source publishes `[]`.
- **GREEN:** implement per the steps above in `diagnostics.go`.
- **REFACTOR:** factor the "get FA + content for relPath (store-first, index fallback)" logic if it
  duplicates hover/document-symbol's store-first pattern — consider a shared helper, but only if it
  reads cleanly; do not over-abstract.
- **DoD:** store-first; F7 snapshot-release-before-I/O; missing file clears; nil-`res` safe;
  `just verify` clean.

### T7 — Wire publishing into the document lifecycle (didOpen / didChange / didClose / watched-files)

**Covers:** S3-AC1 (update on change, open + external), S3-AC2 (clear on fix), S3-AC3 (still no
modeled gaps), FR-30/FR-31. This is the behavioral integration task.

The dispatch handlers in `server.go` currently have **no** stream reference available inside the
notification switch except the `stream` in `Run`'s scope (it is in scope — `client/registerCapability`
already uses it). Use it directly, or capture a small `publish := func(uri){...}` closure over
`ctx`/`stream`/`hctx` near the top of `Run` to keep call sites terse.

- **RED (server-level integration tests, in `internal/server/`):** drive the full lifecycle over an
  in-memory stream (mirror `TestInitialize`/existing lifecycle tests) with `testdata` fixtures:
  - **didOpen a parse-error fixture** → server publishes `textDocument/publishDiagnostics` for that
    URI with one error diagnostic at the fixture's error position (S1-AC1, S1-AC2 message content).
  - **didOpen a clean fixture** (only blank lines/comments + valid statements) → publishes `[]`
    (S1-AC3).
  - **Flat-namespace ambiguity** (workspace with no library map, a name matching two objects) →
    after `initialized` and/or on didOpen, the referencing file's publish includes one
    **warning**-severity ambiguity diagnostic naming the candidates, distinct from any error diag
    (S2-AC1, S2-AC3).
  - **Library-map-disambiguated** (same fixtures + a `.natural-lsp.toml` library map that resolves
    uniquely) → the ambiguity diagnostic is **absent** (empty or non-ambiguity set) (S2-AC2).
  - **didChange fixes the line** (open a parse-error fixture, then didChange to valid content) →
    the second publish is `[]` for that URI, clearing the stale diagnostic (S3-AC2).
  - **workspace/didChangeWatchedFiles** create/change of a fixture → publishes for that URI
    (S3-AC1 external-change path); delete → publishes `[]` (clear).
  - **Modeled-gap fixture** (a program full of `CALLNAT #VAR` dynamic calls + a literal call to a
    non-existent target) → publish is `[]` (S3-AC3 / FR-17: zero diagnostics; dynamic and
    no-target are `Unresolved` outcomes, never diagnostics).
  - **didClose** → decide and assert the convention (see OQ-3): recommended = publish `[]` on close
    to clear the editor (a workspace server re-derives from disk on demand); document the choice.
- **GREEN:** call the T6 orchestrator from each of the four notification handlers in `server.go`:
  - `textDocument/didOpen`: after `store.Open`, publish for the opened URI.
  - `textDocument/didChange`: after `applyDocumentChange` (so `res` reflects the change), publish
    for the changed URI.
  - `textDocument/didClose`: after `store.Close`, publish per OQ-3 decision (empty to clear).
  - `workspace/didChangeWatchedFiles`: after each `applyDocumentChange`, publish for that event's
    URI (empty on delete).
- **REFACTOR:** collapse duplicated publish call sites behind the `publish` closure if it improves
  readability; keep the F7 discipline inside T6, not scattered.
- **DoD:** all seven integration scenarios green; publish fires on all four paths; clear-on-fix and
  clear-on-delete produce `[]`; modeled-gap fixture yields zero; `TestInitialize` **unchanged**
  (no new capability); `just verify` + `just test-integration` clean.

### T8 — Docs sync (CLAUDE.md / README.md) — done at `/finalize-feature`, listed for completeness

- Add a feature-14 "Project state" note: `diagnostics.go` now aggregates
  `FileAnalysis.Diagnostics` + `ResolutionSet.DiagnosticsFor` and pushes
  `textDocument/publishDiagnostics` on didOpen/didChange/didClose/watched-files; no `model` change,
  no cache-format bump (still `0.6.0`); severity map + encoding-aware converter live in
  `internal/server/`; `FuzzDiagnosticConversion` guards it; note the didClose-clear convention and
  the `Source="natural-lsp"` tag. Update the capabilities note to record that publishDiagnostics is
  unilateral (no server capability added).
- **DoD:** README + CLAUDE.md match as-built; `review-docs` finds no drift.

---

## Reviews required (for `/review-feature`)

- **review-architecture:** confirm the Analyzer seam is intact — all `model`→`protocol` conversion
  and stream writes are in `internal/server/`; nothing new crosses into `internal/analysis`. Confirm
  F7 lock discipline (snapshot under RLock, I/O + writes outside) at every publish site.
- **review-correctness:** the two-channel aggregation drops nothing (M-6); modeled gaps
  (dynamic/no-target) never appear (FR-17); empty-array clearing is a full replace not a delta;
  order deterministic.
- **review-tests:** every AC traced to a test; both encodings exercised in the converter; fuzz guard
  present; the modeled-gap zero-diagnostics regression exists.
- **review-docs:** CLAUDE.md/README "Project state" updated; capability allow-list note accurate.

---

## Open questions — RESOLVED at the plan-approval checkpoint (2026-07-07)

1. **Story 2 AC3 distinctness — RESOLVED: add a `model.Diagnostic.Code` category field.** The human
   approved the shared-contract model change over severity-only. See **T0**: `Code` carries a
   machine-readable `"syntax"`/`"ambiguity"` category, set at both producers and mapped to
   `protocol.Diagnostic.Code` in T2. No cache-format bump — `Diagnostics` is not cached (finding 6),
   so the additive field does not touch the persisted format (still `0.6.0`).

2. **Push vs pull diagnostics — RESOLVED: push only.** `textDocument/publishDiagnostics`, no server
   capability (finding 7). Pull-diagnostics (`textDocument/diagnostic`) is out of scope.

3. **didClose behavior — RESOLVED: publish `[]` on close.** The editor stops showing diagnostics for
   the closed file; they re-derive on demand. Baked into T7.

4. **Plan Open Question — "what qualifies a line as statement-like."** A **parser** concern owned by
   feature 00, already implemented. This feature only forwards what the parser produces — no decision
   here. Noted for traceability.

5. **Plan Open Question — opt-in/configurable unrecognized-syntax diagnostics — RESOLVED: deferred.**
   Parse-error diagnostics are always on (P0 is "show parse errors"). No `Analysis.EnableSyntaxDiagnostics`
   toggle in this feature; can be added later mirroring `Analysis.EnableCodeLens` if noise proves a
   real problem.
