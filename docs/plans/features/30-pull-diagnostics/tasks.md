# Tasks: Pull Diagnostics (`textDocument/diagnostic` + `workspace/diagnostic`)

**Feature dir:** `docs/plans/features/30-pull-diagnostics/`
**PRD requirements:** FR-57 (pull diagnostics — new); complements FR-30/FR-31 (push, feature 14);
NFR-11 (LSP conformance), FR-43 (graceful degradation)
**Scope:** server-layer only. **No `internal/model` change, no cache-format bump** (stays `0.9.0`).
Reuses feature 14's `aggregateDiagnostics` / `toProtocolDiagnostic` pipeline verbatim. Adds **one** new
`diagnosticProvider` server capability → the locked `TestInitialize` allow-list gains one entry.

---

## Current-state findings & impact

Investigated `internal/server/{diagnostics.go,server.go,progress.go}`, the `TestInitialize` allow-list,
`internal/workspace/index.go`, and the vendored `go.lsp.dev/protocol@v1.0.0`.

**1. All required protocol pull-diagnostic types already exist in the vendored package — none must be
hand-constructed.** In `go.lsp.dev/protocol@v1.0.0/diagnostic.gen.go` / `types_unions.gen.go` /
`lifecycle.gen.go`:
- `ServerCapabilities.DiagnosticProvider DiagnosticProvider` (a union interface; members
  `*DiagnosticOptions`, `*DiagnosticRegistrationOptions`). We advertise `*protocol.DiagnosticOptions{
  Identifier, InterFileDependencies bool, WorkspaceDiagnostics bool}` (embeds `WorkDoneProgressOptions`).
  `InterFileDependencies`/`WorkspaceDiagnostics` are plain non-omitzero bools → always serialize.
- `DocumentDiagnosticParams{WorkDoneProgressParams, PartialResultParams, TextDocument
  TextDocumentIdentifier, Identifier *string, PreviousResultID *string}`.
- `RelatedFullDocumentDiagnosticReport` (embeds `FullDocumentDiagnosticReport{Kind string, ResultID
  *string, Items []Diagnostic}` + `RelatedDocuments` map). `Kind` must be `"full"`
  (`protocol.DocumentDiagnosticReportKindFull`). The `DocumentDiagnosticReport` union is discriminated
  on `kind`.
- `RelatedUnchangedDocumentDiagnosticReport` (`Kind:"unchanged"`) — available but **not used** (OQ-2).
- `WorkspaceDiagnosticParams{WorkDoneProgressParams, PartialResultParams, Identifier *string,
  PreviousResultIds []PreviousResultId}`.
- `WorkspaceDiagnosticReport{Items []WorkspaceDocumentDiagnosticReport}` (union members
  `*WorkspaceFullDocumentDiagnosticReport{FullDocumentDiagnosticReport, URI uri.URI, Version *int32}`,
  `*WorkspaceUnchangedDocumentDiagnosticReport`).
- `WorkspaceDiagnosticReportPartialResult{Items ...}` and
  `DocumentDiagnosticReportPartialResult{RelatedDocuments ...}` for `$/progress` partial streaming
  (deferred — see T6).
- All carry `MarshalJSONTo` (json/v2), so the existing `marshalResult` (`gojson.Marshal`, feature 19)
  encodes them correctly — **no bespoke wire assembly, unlike `code_lens`/`call_hierarchy` `Data`.**

**2. `aggregateDiagnostics(fa, resDiags)` + `toProtocolDiagnostic(d, content, enc)` are directly
reusable.** `internal/server/diagnostics.go`'s `publishFileDiagnostics` already implements exactly the
snapshot/lookup shape the per-document pull provider needs: F7 snapshot of `idx/res/posEncoding/root`
under `idxResMu.RLock` released before I/O; **store-first** (`hctx.store.Get`) then index + `os.ReadFile`;
`res.DiagnosticsFor(relPath)` (nil-safe); out-of-root / not-indexed / unreadable → clear. The pull
provider mirrors this but **returns a report value** instead of writing a `publishDiagnostics`
notification. **Content is unchanged (byte-identical to push) — only the delivery mechanism differs.**

**3. Workspace sweep must stay disk-free (feature 22 / ADR-025).** `workspace.Index.ForEachWithRange`
yields per-file `(path, analysis, toRange RangeConverter)` where `RangeConverter func(r model.Range,
utf16 bool) (startLine, startChar, endLine, endChar uint32)` converts ranges from the in-memory
line-width table with **no `os.ReadFile`**. `toProtocolDiagnostic` currently converts ranges from file
*content*; the workspace path needs a converter-based sibling so the sweep does not re-read every file.
This is a small **additive** helper in `diagnostics.go`, no model change. Resolution ambiguity diagnostics
come from `res.DiagnosticsFor(relPath)` per file (snapshot `res` alongside `idx`).

**4. Dispatch + capability wiring is mechanical.** New `case` arms in the `Run` switch decode params via
`params.UnmarshalJSONFrom(jsontext.NewDecoder(bytes.NewReader(call.Params())))`, gate on
`stateInitialized`, call the provider, and assign `respResult` via `marshalResult`. **Unlike other
providers, a pull-diagnostic response is NEVER a `null`/`[]` sentinel** — it is always a report *object*
(an empty document report is `{"kind":"full","items":[]}`). Initialize `Items` to
`[]protocol.Diagnostic{}` so it serializes as `[]` not `null`.

**5. OQ-1 push/pull coexistence has a clean seam.** The client's pull support is
`params.Capabilities.TextDocument.Diagnostic != nil` (`*DiagnosticClientCapabilities`). The negotiation
struct (`initializeNegotiation`) already carries `clientSupportsWatchedFilesReg` /
`clientSupportsWorkDoneProgress` flags plumbed into `Run`; add a `clientSupportsPullDiagnostics` flag the
same way and gate the existing `publishDiag` closure (server.go ~L453) so push is suppressed for
pull-capable clients. Push wiring (`didOpen`/`didChange`/watched-file `publishDiag`, and the explicit
`publishDiagnostics(...nil...)` clear-on-close/delete calls) is otherwise untouched.

**6. Progress plumbing exists but is workDone-shaped.** `progress.go`'s `progressReporter`
(`create`/`begin`/`report`/`end`) sends `window/workDoneProgress/create` + `$/progress` on a token — it
honors a **workDone** token, not a **partialResult** token. `WorkspaceDiagnosticParams` supplies both
`WorkDoneToken` and `PartialResultToken`. Recommended plan (T6): honor `WorkDoneToken` for a
begin/report/end progress bar over the bounded index sweep; return the **full** `WorkspaceDiagnosticReport`
in one response. Partial-result streaming (`$/progress` carrying `WorkspaceDiagnosticReportPartialResult`
on the `PartialResultToken`) is **deferred** — the index sweep is bounded and non-re-analyzing, so a
single response is spec-legal.

**7. Test/fuzz conventions.** `internal/server/diagnostics_test.go` holds the feature-14 unit tests;
`fuzz_test.go` holds `FuzzDiagnosticConversion` (+ the `FuzzProvide*` provider fuzzers). Fixtures live at
`internal/server/testdata/diagnostics/{clean.NSP, parse-error.NSP, modeled-gaps.NSP}` — **all three are
reused as-is** (`parse-error.NSP` → non-empty items; `clean.NSP` / `modeled-gaps.NSP` → empty report,
since modeled gaps stay off the diagnostic channel per FR-17). The `TestInitialize` allow-list lives in
`server_test.go` (`requiredProviders` slice + per-provider shape sub-asserts, e.g. the
`TestInitialize_SemanticTokensLegend` pattern).

**No acceptance criterion is already satisfied** — pull diagnostics are wholly new. Every criterion below
maps to a task. No shared-contract change and no consumer migration is required (push path is gated
additively, not modified).

---

## Open questions to confirm at the approval checkpoint

- **OQ-1 (push/pull coexistence).** *Planned answer (T4):* keep pushing `publishDiagnostics` for
  push-only clients; **suppress push** when the client advertises pull support
  (`params.Capabilities.TextDocument.Diagnostic != nil`), relying on pull for those documents to avoid
  double delivery. **Confirm** this is the desired behavior (vs. always-push-and-also-serve-pull).
- **OQ-2 (`resultId` / unchanged reports).** *Planned answer (T2/T5):* always return a **full** report;
  leave `ResultID` unset and never emit `unchanged` reports. No per-document content-hash result-id cache.
  **Confirm** full-each-time is acceptable for v1 (unchanged-report caching added later only if a client
  thrashes).

---

## Tasks

Sequenced so each of (capability + `TestInitialize`), the per-document provider, the push/pull gate, and
the workspace provider lands as an independent red→green→refactor slice. Agents to run per task:
`tdd-red` → `tdd-green` → `tdd-refactor`.

### T1 — Advertise `diagnosticProvider` + extend the `TestInitialize` allow-list
**Story/AC:** Story 1 AC1 (partial: advertisement), Story 1 AC4. **FR-57, NFR-11.**

- **RED:** In `server_test.go`, add `"diagnosticProvider"` to the `requiredProviders` slice in
  `TestInitialize`, and add a shape sub-assertion (mirroring the completion/semanticTokens blocks): the
  `diagnosticProvider` value is a JSON **object** (not a bool) with `interFileDependencies == true`,
  `workspaceDiagnostics == true`, and an `identifier` string present. A dedicated
  `TestInitialize_DiagnosticProvider` may hold the shape assertions. Test fails: the capability is not yet
  advertised.
- **GREEN:** In `handleInitialize` (server.go, the `ServerCapabilities` literal ~L221), add
  `DiagnosticProvider: &protocol.DiagnosticOptions{ Identifier: <ptr to "natural-lsp">,
  InterFileDependencies: true, WorkspaceDiagnostics: true }`. `InterFileDependencies: true` is the
  **honest** value — a resolution-ambiguity diagnostic (FR-31) in file A depends on the presence of
  objects in other files, so editing one file can change another's diagnostics. `WorkspaceDiagnostics:
  true` because Story 2 serves `workspace/diagnostic`.
- **Expected result:** `initialize` response `capabilities.diagnosticProvider ==
  {"identifier":"natural-lsp","interFileDependencies":true,"workspaceDiagnostics":true}`; all
  pre-existing capabilities byte-identical; `positionEncoding`/`serverInfo` unchanged.
- **Fixtures:** none (initialize handshake only).
- **FR-43/modeled-gap:** n/a (capability advertisement).
- **DoD:**
  - [ ] `diagnosticProvider` present in the `initialize` result with the exact shape above.
  - [ ] `requiredProviders` in `TestInitialize` includes `diagnosticProvider`; shape sub-assert passes.
  - [ ] No other capability changed; `just verify` green.

### T2 — Per-document provider `provideDocumentDiagnostic` (report builder)
**Story/AC:** Story 1 AC1 (byte-identical content), Story 1 AC3 (graceful degradation), OQ-2.
**FR-57, FR-30/FR-31 (content), FR-43, FR-17.**

- **RED:** New `internal/server/pull_diagnostics_test.go` (or extend `diagnostics_test.go`) unit tests on
  `provideDocumentDiagnostic(hctx, params protocol.DocumentDiagnosticParams)
  (*protocol.RelatedFullDocumentDiagnosticReport, error)`:
  - `parse-error.NSP` (indexed/on-disk) → report `Kind=="full"`, non-empty `Items`, each item
    `Source=="natural-lsp"`, and the item set **byte-identical** to what the push path produces for the
    same file (assert equality against `aggregateDiagnostics` → `toProtocolDiagnostic` output).
  - store-first: an open buffer with a fresh (unsaved) parse error → items reflect the buffer, not disk.
  - `clean.NSP` and `modeled-gaps.NSP` → `Kind=="full"`, `Items == []` (empty, non-nil). (Modeled gaps
    stay off the diagnostic channel — FR-17.)
  - missing / unreadable / out-of-root URI → `Kind=="full"`, `Items == []`, `err == nil` (FR-43 — an
    empty full report, **never** an error, **never** `null`).
  - OQ-2: `ResultID` is unset (nil) in every case.
- **GREEN:** Implement in a new `internal/server/pull_diagnostics.go`. Mirror `publishFileDiagnostics`'
  F7 snapshot + store-first + index/disk resolution + `res.DiagnosticsFor(relPath)` +
  `aggregateDiagnostics` + `toProtocolDiagnostic(d, content, posEncoding)`. Build and return
  `&protocol.RelatedFullDocumentDiagnosticReport{ FullDocumentDiagnosticReport:
  protocol.FullDocumentDiagnosticReport{ Kind: string(protocol.DocumentDiagnosticReportKindFull), Items:
  <non-nil slice> } }`. Initialize `Items` to `[]protocol.Diagnostic{}` on the empty paths so it
  serializes as `[]`. Never return a nil report.
- **Expected result:** a full document report whose `Items` equal the push path's diagnostics for the
  same document snapshot; empty full report on every degradation path.
- **Fixtures:** reuse `internal/server/testdata/diagnostics/{parse-error,clean,modeled-gaps}.NSP`.
- **FR-43/modeled-gap:** missing/unreadable/out-of-root/no-diags → empty full report; modeled gaps
  (dynamic/unresolved refs) never appear.
- **DoD:**
  - [ ] Provider returns a non-nil `*RelatedFullDocumentDiagnosticReport` in all cases (incl. errors).
  - [ ] Content byte-identical to the push path (asserted).
  - [ ] Store-first honored; degradation paths return empty full report, `err == nil`.
  - [ ] `just verify` green.

### T3 — Dispatch + marshal `textDocument/diagnostic`
**Story/AC:** Story 1 AC1 (handles the request; wire shape). **FR-57, NFR-11.**

- **RED:** Wire-bytes test (mirroring the feature-19 completion/semanticTokens wire tests): drive an
  `initialize → initialized → textDocument/diagnostic` round-trip against a fixture workspace and assert
  the raw response JSON has `result.kind == "full"`, `result.items` is an array, each item carries
  `source == "natural-lsp"` and a valid range, and an empty document serializes `"items":[]` (not
  `null`). Also assert the method is dispatched only after `initialized` (pre-init →
  `ServerNotInitialized`), and malformed params → `InvalidParams`.
- **GREEN:** Add a `case "textDocument/diagnostic":` arm in the `Run` switch: gate on `stateInitialized`;
  decode `protocol.DocumentDiagnosticParams` via
  `params.UnmarshalJSONFrom(jsontext.NewDecoder(bytes.NewReader(call.Params())))` (→ `InvalidParams` on
  error); call `provideDocumentDiagnostic(hctx, params)`; on provider error → `InternalError`; else
  `respResult, marshalErr = marshalResult(report)` (report is provably non-nil per T2 — **no
  null/`[]` sentinel branch**, unlike the other providers).
- **Expected result:** a well-formed `RelatedFullDocumentDiagnosticReport` on the wire for any indexed
  or open Natural document.
- **Fixtures:** reuse a `testdata/diagnostics/` fixture as the workspace root (or the existing
  round-trip fixture setup used by the feature-14 lifecycle tests).
- **FR-43/modeled-gap:** unreadable/out-of-root URI still yields a `200`-style success response carrying
  an empty full report (from T2), never a JSON-RPC error.
- **DoD:**
  - [ ] `textDocument/diagnostic` dispatched, gated on `stateInitialized`, params-validated.
  - [ ] Response marshaled via `marshalResult`; empty report serializes `"kind":"full","items":[]`.
  - [ ] Wire-bytes test asserts `kind`/`source`/array shape.
  - [ ] `just verify` green.

### T4 — OQ-1: gate push diagnostics off for pull-capable clients
**Story/AC:** Story 1 (out-of-scope note: no double-reporting), OQ-1. **FR-57, FR-30/FR-31.**

- **RED:** Extend the feature-14 lifecycle-publishing tests (or add a sibling): with an `initialize`
  whose `capabilities.textDocument.diagnostic` is present, a `didOpen` of a parse-error file emits **no**
  `textDocument/publishDiagnostics` notification (push suppressed). With a client that does **not**
  advertise `textDocument.diagnostic`, `didOpen` still pushes (feature-14 behavior preserved — assert an
  existing lifecycle test still passes unchanged). Also assert the clear-on-close/delete push
  (`publishDiagnostics(...nil...)`) is likewise suppressed for pull-capable clients.
- **GREEN:** Add `clientSupportsPullDiagnostics bool` to `initializeNegotiation`; set it in
  `handleInitialize` from `params.Capabilities.TextDocument != nil &&
  params.Capabilities.TextDocument.Diagnostic != nil` (nil-chain deref pattern matching
  `WorkDoneProgress` detection ~L204). Plumb it into `Run` alongside the other negotiation flags (~L928)
  and gate the `publishDiag` closure (~L453) **and** the two explicit `publishDiagnostics(...nil...)`
  clear calls (close/delete) so they no-op when pull is supported.
- **Expected result:** pull-capable clients receive diagnostics only via `textDocument/diagnostic`;
  push-only clients are unaffected. No document ever gets both.
- **Fixtures:** reuse `testdata/diagnostics/parse-error.NSP`.
- **FR-43/modeled-gap:** suppression is a no-op guard; no new failure mode.
- **DoD:**
  - [ ] `clientSupportsPullDiagnostics` negotiated and plumbed into `Run`.
  - [ ] Push suppressed (incl. clear-on-close/delete) for pull-capable clients; preserved for others.
  - [ ] **Flagged for confirmation (OQ-1).**
  - [ ] `just verify` green.

### T5 — Workspace provider `provideWorkspaceDiagnostic` (disk-free index sweep)
**Story/AC:** Story 2 AC1. **FR-57, FR-43, ADR-025 (no per-query disk sweep).**

- **RED:** Unit tests on `provideWorkspaceDiagnostic(hctx, params
  protocol.WorkspaceDiagnosticParams) (*protocol.WorkspaceDiagnosticReport, error)` over a small
  multi-file workspace fixture (one file with a parse error, one clean): the returned `Items` contain a
  `*WorkspaceFullDocumentDiagnosticReport` for the error file with `Kind=="full"`, correct `URI`,
  non-empty `Items`, `Source=="natural-lsp"`; the clean file is **omitted** (recommended: report only
  files with ≥1 diagnostic — bounded and spec-legal). Empty workspace → `Items == []` (non-nil), `err ==
  nil`. Include a resolution-ambiguity fixture so `res.DiagnosticsFor` contributes an item. Assert the
  sweep does **not** call `os.ReadFile` (disk-free — e.g. delete files after build, mirroring feature 22's
  disk-free proof).
- **GREEN:** New `provideWorkspaceDiagnostic` in `pull_diagnostics.go`. F7-snapshot `idx`/`res`/
  `posEncoding` under `RLock`, release before building. Iterate `idx.ForEachWithRange(func(path, fa,
  toRange){...})`: `agg := aggregateDiagnostics(fa, res.DiagnosticsFor(path))`; skip files with no
  diagnostics; convert each `model.Diagnostic` to `protocol.Diagnostic` via a new **additive** helper
  `toProtocolDiagnosticFromConverter(d, toRange, utf16)` (utf16 = `posEncoding ==
  protocol.PositionEncodingKindUTF16`) that builds the range from the disk-free `RangeConverter` instead
  of file content; append a `&protocol.WorkspaceFullDocumentDiagnosticReport{ FullDocumentDiagnosticReport:
  {Kind:"full", Items: <non-nil>}, URI: <file URI from root+relPath> }` (Version left nil — on-disk).
  Return `&protocol.WorkspaceDiagnosticReport{Items: items}` (`items` initialized non-nil).
- **Expected result:** one full report per indexed file that has diagnostics, drawn from the index with
  no re-analysis and no disk read; files with none omitted.
- **Fixtures:** a small workspace fixture under `internal/server/testdata/diagnostics/` (reuse
  `parse-error.NSP` + `clean.NSP`; add an ambiguity fixture pair if not already covered by the
  feature-14 `FlatNamespaceAmbiguity` fixtures — reuse those if present).
- **FR-43/modeled-gap:** empty/unindexed workspace → empty non-nil `Items`, `err == nil`; modeled gaps
  never surface.
- **DoD:**
  - [ ] Returns a non-nil `*WorkspaceDiagnosticReport`; per-file `Kind=="full"`, correct `URI`.
  - [ ] Bounded, disk-free (asserted); reuses `aggregateDiagnostics` + `res.DiagnosticsFor`.
  - [ ] Content byte-identical to per-document/push for the same file.
  - [ ] `just verify` green.

### T6 — Dispatch `workspace/diagnostic` + honor work-done progress token
**Story/AC:** Story 2 AC1 (handles request), Story 2 AC2 (progress tokens honored). **FR-57, NFR-11.**

- **RED:** Round-trip wire test: `workspace/diagnostic` after `initialized` returns
  `result.items` as an array of full reports (`kind:"full"`, per-item `uri`); pre-init →
  `ServerNotInitialized`; malformed params → `InvalidParams`. Progress test: when the request supplies a
  `workDoneToken`, the server (for a client that advertised `window.workDoneProgress`) emits `$/progress`
  begin→(report)→end on that token before/around the response; when no token is supplied, no `$/progress`
  is emitted and a single full report is returned.
- **GREEN:** Add `case "workspace/diagnostic":` — gate `stateInitialized`; decode
  `protocol.WorkspaceDiagnosticParams`; if `params.WorkDoneToken` is non-empty and the client supports
  work-done progress, construct a `progressReporter` on that token (reusing `progress.go`) and wrap the
  provider call in `begin`/optional `report`/`end`; call `provideWorkspaceDiagnostic`; on error →
  `InternalError`; else `marshalResult(report)`. **Partial-result streaming
  (`WorkspaceDiagnosticReportPartialResult` on `partialResultToken`) is deferred** — a single full
  response over the bounded, non-re-analyzing index sweep is spec-legal; document this decision inline.
- **Expected result:** `workspace/diagnostic` returns the full report from T5; a supplied work-done token
  drives a progress bar; large workspaces are served from the in-memory index without a disk sweep or
  re-analysis (bounded — Story 2 AC1).
- **Fixtures:** reuse the T5 workspace fixture.
- **FR-43/modeled-gap:** progress emission is fire-and-forget (reuses `progressReporter`'s
  log-not-fail behavior); a write failure never fails the request.
- **DoD:**
  - [ ] `workspace/diagnostic` dispatched, gated, params-validated, marshaled via `marshalResult`.
  - [ ] Work-done token honored via `progressReporter` when present + client-supported; absent → single
        response, no progress.
  - [ ] Partial-result streaming deferral documented inline. **Flagged (Story 2 AC2 scope).**
  - [ ] `just verify` green.

### T7 — Fuzz + FR-43 hardening of the report builders
**Story/AC:** Story 1 AC3, Story 2 (robustness). **FR-43.**

- **RED:** Add `FuzzProvideDocumentDiagnostic` and `FuzzProvideWorkspaceDiagnostic` in `fuzz_test.go`
  (mirroring `FuzzProvideHover`/`FuzzDiagnosticConversion`): seed with garbage URIs, degenerate/negative
  positions, non-file URIs, out-of-root paths, empty/nil index, nil `res`, nil `store`, and both
  encodings; assert the providers **never panic** and always return a non-nil report with `Kind=="full"`
  and a non-nil `Items` slice.
- **GREEN:** Fix any panic/nil-deref the fuzzers surface (expected: none if T2/T5 followed the
  `publishFileDiagnostics` nil-guards — this task ratifies the invariant).
- **Expected result:** report builders are total functions over arbitrary input.
- **Fixtures:** none (fuzz corpus is generated; seed from existing testdata paths).
- **FR-43/modeled-gap:** this is the FR-43 guard task.
- **DoD:**
  - [ ] Both fuzz targets present and green in the short fuzz budget used by CI/`just verify`.
  - [ ] Never-panic / non-nil-report / non-nil-`Items` invariants asserted.
  - [ ] OQ-2 (full-report-each-time, `ResultID` unset) reconfirmed in a builder test comment.
  - [ ] `just verify` green.

---

## Traceability

| Acceptance criterion | Task(s) |
|---|---|
| S1-AC1 advertise `diagnosticProvider` (identifier/interFileDependencies/workspaceDiagnostics) | T1 |
| S1-AC1 handle `textDocument/diagnostic` → full report, byte-identical content, `source` | T2, T3 |
| S1-AC2 unchanged report optional (OQ-2: full each time) | T2 (documented), T7 |
| S1-AC3 missing/unreadable/out-of-root/no-diags → empty full report, never error | T2, T3, T7 |
| S1-AC4 `TestInitialize` allow-list includes `diagnosticProvider` | T1 |
| S2-AC1 `workspace/diagnostic` bounded index report, per-file, empties omitted | T5, T6 |
| S2-AC2 honor work-done/partial-result progress tokens | T6 (workDone honored; partial deferred) |
| Out-of-scope: no double-reporting (push/pull coexistence) — OQ-1 | T4 |
| FR-43 never-panic report builders | T7 |

---

## Reviews required (for `/review-feature`)

- **review-lsp-conformance:** `diagnosticProvider` capability shape (`interFileDependencies` honesty),
  `RelatedFullDocumentDiagnosticReport` / `WorkspaceDiagnosticReport` wire shape (`kind:"full"`,
  `items:[]` never `null`, per-item `uri`), and the push/pull coexistence rule (no double delivery).
- **review-architecture / seam:** confirm the feature stays LSP-side — `internal/server` reuses
  `aggregateDiagnostics` and the `workspace.Index` `RangeConverter` seam; **no crossing into parser
  internals**, no `internal/model` change, no cache-format bump.
- **review-concurrency:** the F7 snapshot discipline in both providers (snapshot `idx/res/posEncoding`
  under `RLock`, release before building) matches `publishFileDiagnostics`; `progressReporter` usage in
  the serial dispatch loop does not block.
- **review-tests:** byte-identical-content assertions vs. the push path; disk-free workspace sweep proof
  (ADR-025); fuzz never-panic; `TestInitialize` allow-list lock updated.
- **review-docs:** `CLAUDE.md` "Project state" + capability list and `README.md` updated to note pull
  diagnostics and the new capability (done in `/finalize-feature`).

## Open questions (restated for the approval checkpoint)

1. **OQ-1** — Suppress push for pull-capable clients (planned: yes, T4)? Confirm.
2. **OQ-2** — Full report each time, no `resultId`/unchanged caching (planned: yes, T2/T5/T7)? Confirm.
3. **Story 2 partial-result streaming** — deferring `$/progress`
   `WorkspaceDiagnosticReportPartialResult` on `partialResultToken` in favor of a single full response
   over the bounded index sweep (planned: defer, T6). Confirm acceptable for v1.
4. **Workspace empties** — report only files with ≥1 diagnostic (omit clean files) rather than emitting
   empty per-file reports (planned: omit, T5). Confirm.
