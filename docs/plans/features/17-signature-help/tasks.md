# Tasks: Signature Help (feature 17)

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements covered:** FR-48 (signature help). Cross-cutting: FR-12 (inline-before-external
PERFORM resolution), FR-17 (modeled gaps stay off the diagnostic/error channel), FR-43 (never panic /
graceful degradation), ADR-008 (position encoding).

This is a **server-side LSP provider** feature (`textDocument/signatureHelp`) — standard Go TDD with
`testdata/` fixtures under `internal/server/testdata/signaturehelp/`. It lives entirely on the
LSP-facing side of the Analyzer seam (`internal/server/`), reading the existing
`internal/model`/index/resolution surface. **No `internal/model` change and no cache-format bump**
(stays `0.6.0`) — like features 10/11/12/13/16, it is a read-only query over existing `Definitions`
and the `ResolutionSet`.

---

## Current-state findings & impact

Verified against the code on `main` (features 00–16 shipped; feature 16 completion is in-tree).

### What already exists and is reused

- **Provider wiring pattern (server.go).** Every provider is a `case "textDocument/..."` in the dispatch
  switch in `internal/server/server.go` gated on `state != stateInitialized`, decoding params with the
  protocol type's `UnmarshalJSONFrom`, calling a `provideXxx(hctx, params)` in its own file, and writing
  `respResult`. Signature help follows this exactly: a new `internal/server/signature_help.go` +
  `case "textDocument/signatureHelp"`.

- **Capability advertisement + the locked allow-list.** Capabilities are set in `handleInitialize`
  (`server.go` ~140-154). `ServerCapabilities.SignatureHelpProvider` is a
  `*protocol.SignatureHelpOptions{TriggerCharacters, RetriggerCharacters}` (confirmed in
  `lifecycle.gen.go:123`). The allow-list is **locked by `TestInitialize`** (`server_test.go` ~526-580);
  its `requiredProviders` list and the `completionProvider` shape assertion must be **extended** to add
  `signatureHelpProvider` — an explicit DoD item, or the build's own gate fails.

- **HUGE reuse — the parameter interface already exists (hover.go).** Feature 12's
  `buildSubroutineHover(name string, params []model.DataDefinition)` (`hover.go:72`) filters `Definitions`
  to `SectionKind == "parameter"` and renders name + type + array dims + group nesting (via
  `buildParamLines`). Signature help needs the **same parameter set** rendered as structured
  `ParameterInformation` instead of Markdown. **Plan extracts a shared helper** so hover and signature
  help agree on "the parameter interface"; hover's tests must stay green after the refactor.

- **Cursor → target → resolution machinery (cursor.go, hover.go, definition.go).**
  `findCursorTarget(fa, pos)` (`cursor.go:23`) maps a 1-based cursor to the smallest-containing
  `EdgeEntry`/`DataAccessEntry` (matching edges by `EdgeEntry.Source`, the whole statement — feature
  12 widened CALLNAT/FETCH/RUN/PERFORM `Source` **through the target name**). `provideHover`
  (`hover.go:242`) shows the full dispatch: snapshot idx/res under RLock, `fromProtocolPosition`,
  `findCursorTarget`, `res.Get(relPath, edge.Source)`, then for a resolved target: inline PERFORM
  (walk `sourceFA.Structure.Children` for a matching `SymbolSubroutine`, params from
  `sourceFA.Definitions`) → external PERFORM (`buildSubroutineHover(edge.TargetName, targetFA.Definitions)`)
  → module (CALLNAT → `idx.Get(resolution.Path)`, params from that file's `Definitions`). Signature help
  reuses this branching wholesale; the *only* new mechanics are (a) firing when the cursor is **on or
  after** the target (in the argument region), and (b) the active-parameter index.

- **Pure line-context detector template (completion.go).** `detectCompletionContext(line, cursorByteCol)`
  (`completion.go:79`) + `tokenizeLine` (`completion.go:162`) are a pure, table-tested prefix scan of the
  current line — exactly the shape the active-parameter/context detector needs. `lineAt` + the
  `fromProtocolPosition` → `modelPos.Line-1` / `modelPos.Column-1` derivation (`completion.go:298-308`)
  is the reusable "get (line, cursorByteCol)" recipe. `isDynamicPrefix` (`completion.go:232`) recognizes
  `#`/`&`/`+` sigils.

- **F7 snapshot discipline + store-first.** `provideCompletion` and `provideHover` both snapshot
  `idx`/`res`/`posEncoding`/`root` under `hctx.idxResMu.RLock()` and release before any I/O; completion
  reads the **open buffer first** (`hctx.store.Get(uri)`) then disk. Signature help fires while typing,
  so it MUST be store-first like completion.

- **Fuzz-target convention (fuzz_test.go).** Each provider has a `FuzzProvideXxx` and each pure detector
  a fuzz target (`FuzzCompletionContext`, `FuzzProvideCompletion`, `FuzzProvideHover`, …). Signature help
  adds `FuzzProvideSignatureHelp` and a fuzz guard on the arg-index detector.

### Divergences / hazards to flag (code is ground truth)

- **Marshaling: `SignatureHelp` is NOT stdlib-`json.Marshal`-safe.** Every other provider writes its
  result via stdlib `json.Marshal(...)` into `respResult` (server.go ~684/716/808/873). That works for
  them because their result types are plain structs with json tags. **`SignatureHelp` contains union /
  Nullable fields** — `SignatureHelp.ActiveParameter Nullable[uint32]`, `SignatureInformation.Parameters
  []ParameterInformation`, and `ParameterInformation.Label ParameterInformationLabel` (an
  `interface{ isParameterInformationLabel() }` union whose string form is `protocol.String`). `Nullable[T]`
  has **unexported fields** and marshals only via its `MarshalJSONTo(*jsontext.Encoder)`; the union label
  needs the protocol union encoder. Stdlib `json.Marshal` would emit wrong/empty output for these. The
  handler MUST marshal the result with `(*protocol.SignatureHelp).MarshalJSONTo(jsontext.NewEncoder(&buf))`
  (the pattern `handleInitialize` already uses at server.go:162-163), NOT `json.Marshal`. **Call this out
  in the T1 handler task and assert the on-the-wire JSON shape in a test.** A `null` result (not in a call
  context) still writes `[]byte("null")` like the other providers.
- **`ParameterInformation.Label`** must be constructed as `protocol.String("<name> <type>")` (the union's
  string arm), never a bare Go string, or it won't type-assert to the union interface.
- **`activeParameter` clearing** uses `Nullable[uint32]` — an unset/absent index is the zero
  `Nullable[uint32]{}` (omitted via `omitzero`); an explicit "no active parameter" would be
  `protocol.Nullable[uint32]` in its null state. Prefer clamping to last index (Story 3 AC2 permits
  either clamp or clear); if clearing, leave the field zero so it is omitted.

### Acceptance criteria → disposition

| Story / AC | Disposition | Task |
|---|---|---|
| S4 AC1 `signatureHelpProvider` advertised w/ trigger chars | new (extend allow-list lock) | T1 |
| S4 AC2 handles any position, `null` when not in call context | new | T1, T6 |
| S3 AC1 activeParameter index = cursor's arg position | new (novel logic) | T2 (detector), T5 (wire) |
| S3 AC2 past-last clamps/clears, never crashes | new | T2, T5 |
| (shared) parameter interface → `ParameterInformation[]` | extract from hover | T3 |
| S1 AC1/AC2 CALLNAT → subprogram signature, params name+type | extend hover dispatch | T4 |
| S1 AC3 active parameter highlighted (CALLNAT) | new | T5 |
| S1 AC4 unresolved/dynamic → no signature, no error (FR-17) | new | T6 |
| S2 AC1/AC2 PERFORM signature, inline-before-external (FR-12) | reuse hover dispatch | T4a |
| S2 AC3 params name+type | shared helper | T3/T4a |
| S2 AC4 no-parameter subroutine → empty signature, not error | new | T4a |
| FR-43 never panic | fuzz targets | T2, T6 |

### Out of scope (deferred — see Open questions)

- **FETCH / RUN program-transfer signature help.** Planned default: **deferred** (programs' stack-parameter
  semantics differ from CALLNAT/PERFORM formal parameters). The detector classifies **only CALLNAT + PERFORM**
  as signature contexts. Confirm at checkpoint (OQ-2).
- Argument-value completion (feature 16) and non-call hover (feature 12) — explicitly out per plan.

---

## Task list

Ordered so the spine (capability + dispatch + pure detector) lands first, then the shared extraction
(refactor hover), then per-story behavior, then modeled-gaps + fuzz.

---

### T1 — Capability advertisement + dispatch skeleton (S4 AC1, S4 AC2 partial)

**Behavior.** Advertise `signatureHelpProvider` in `handleInitialize` and route
`textDocument/signatureHelp` to a stub `provideSignatureHelp(hctx, params) (*protocol.SignatureHelp, error)`
that returns `nil, nil` for now. A `null` result serializes to JSON `null`; a non-nil result serializes
via the protocol encoder (see divergence note).

**Changes.**
- `server.go` `handleInitialize`: add
  `SignatureHelpProvider: &protocol.SignatureHelpOptions{TriggerCharacters: []string{" "}, RetriggerCharacters: []string{" "}}`
  (default per OQ-1; single point of change if the user picks otherwise).
- `server.go` dispatch: `case "textDocument/signatureHelp"` gated on `stateInitialized`, decode
  `protocol.SignatureHelpParams` via `UnmarshalJSONFrom`, call `provideSignatureHelp`, and **marshal the
  result with `(*protocol.SignatureHelp).MarshalJSONTo(jsontext.NewEncoder(&buf))`** — not `json.Marshal`
  (divergence note); `nil` → `respResult = []byte("null")`.
- `signature_help.go`: stub `provideSignatureHelp` returning `nil, nil`.

**Fixtures.** None (skeleton).

**Expected result.** `initialize` response includes `signatureHelpProvider` with `triggerCharacters:[" "]`
and `retriggerCharacters:[" "]`; a `textDocument/signatureHelp` request at any position returns JSON `null`
without error; a request before `initialized` returns `ServerNotInitialized`.

**Reuse / migrate.** Mirrors the completion `case` (server.go ~848); **must extend `TestInitialize`'s
`requiredProviders` / provider-shape assertions** (`server_test.go` ~526-580) to assert the new capability
and its trigger/retrigger characters — otherwise the locked-allow-list test fails.

**DoD.**
- [ ] RED: `TestInitialize` (extended) asserts `signatureHelpProvider` present with the trigger/retrigger
      chars; a dispatch test asserts a `signatureHelp` request returns `null` (verify against the wire JSON
      produced by `MarshalJSONTo`).
- [ ] GREEN: capability + case + stub.
- [ ] gofmt / go vet clean; `-race` on the server tests.
- [ ] Seam preserved (LSP-facing only; depends on `Analyzer`/`model`, not parser internals).
- [ ] No `internal/model` / cache-format change.

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** none.

---

### T2 — Pure signature-context + active-parameter detector (S3 AC1, S3 AC2, FR-43)

**Behavior.** A pure function that, given the current line text and the byte column of the cursor,
classifies whether the cursor is in a **CALLNAT or PERFORM** call context and (if so) returns the
0-based **argument index** = the count of whitespace-separated argument tokens between the target token
and the cursor. Natural has **no parentheses**: `CALLNAT 'SUB' arg1 arg2 arg3` — tokens after the target
are arguments; the active index advances by one per completed argument (space-separated).

Suggested shape (mirror `detectCompletionContext`):
`detectSignatureContext(line string, cursorByteCol int) (sigContextKind, argIndex int)` where
`sigContextKind` distinguishes `sigNone` / `sigCallnat` / `sigPerform`. Reuse `tokenizeLine`.

**Rules (table cases):**
- Leading keyword `CALLNAT` → `sigCallnat`; `PERFORM` → `sigPerform`; anything else (incl. FETCH/RUN per
  OQ-2) → `sigNone`, argIndex 0.
- Token[0]=keyword, token[1]=target name; args start at token[2]. argIndex = (number of arg tokens fully
  before the cursor). Cursor right after target + one space → argIndex 0 (filling first arg); after
  `arg1 ` → argIndex 1; mid-typing `arg2` (no trailing space) → still index 1 (the token under construction).
- Cursor **on** the target token (not yet in args) → context recognized, argIndex 0 (Story: fires on OR
  after the target).
- Trailing-whitespace handling: a trailing space means the cursor has moved to the next arg slot (index +1),
  matching completion's `endsWithWhitespace` logic.
- Clamp: argIndex is never negative; callers clamp against the parameter count (T5).
- Never panics on empty line, keyword-only, out-of-range column (clamp like `detectCompletionContext`).

**Fixtures.** None (pure, table-driven).

**Expected result.** Table covering: `sigNone` for a plain line / FETCH / RUN / data-access verb; correct
argIndex progression for CALLNAT and PERFORM with 0..N args, quoted and bare targets, trailing space vs
mid-token, and column clamping.

**Reuse / migrate.** Reuse `tokenizeLine` (completion.go). Do NOT reuse `detectCompletionContext` directly
(different classification + it computes a prefix, not an arg index) — a sibling function keeps both cohesive.

**DoD.**
- [ ] RED: table-driven test for `detectSignatureContext`.
- [ ] GREEN: implementation.
- [ ] `FuzzSignatureContext` (arbitrary line + column) never panics (FR-43).
- [ ] gofmt / go vet clean; pure (no I/O, no locks).

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** none (parallel with T1).

---

### T3 — Extract shared parameter-interface helper (refactor; S1 AC2, S2 AC3)

**Behavior.** Extract the "filter `Definitions` to `SectionKind == "parameter"` and enumerate them
(name + type, honoring array dims and group nesting)" logic that `buildSubroutineHover`/`buildParamLines`
currently do for Markdown, into a **shared, presentation-neutral** helper both hover and signature help
consume. Signature help renders each parameter as a `protocol.ParameterInformation` (Label =
`protocol.String("<name> <type-with-dims>")`); hover keeps its Markdown rendering.

Suggested shape: a pure `parameterInterface(defs []model.DataDefinition) []paramItem` (or return the
filtered `[]model.DataDefinition` in flat declaration order with a rendered type string), so both
renderers agree on the parameter set and the "name + type" string. The empty-parameters case yields an
empty slice (Story 2 AC4).

**Fixtures.** None (pure); exercised via existing hover fixtures + a new signature-help builder test.

**Expected result.** Given a `.NSN`/`.NSS` PARAMETER block, the helper yields one entry per formal
parameter with name and rendered type (`N8`, `A50 (1:10)`, group nesting flattened consistently with how
hover shows them). **Hover's existing tests stay green** — the refactor must not change hover output
(assert byte-identical hover strings for the existing hover fixtures).

**Reuse / migrate.** Refactor `buildSubroutineHover`/`buildParamLines` (`hover.go:72-130`) to call the new
helper. This is an **internal (same-package) refactor**, not a shared-contract change — no consumer outside
`internal/server` is touched, no seam/model change. Add a `buildSignatureInformation(name, defs)` builder
(pure) that produces a `protocol.SignatureInformation` with `Label` (a rendered header like
`SUB (P1 N8, P2 A50)`), `Parameters []ParameterInformation`, and leaves active-index to the caller.

**DoD.**
- [ ] RED: test for the shared helper + `buildSignatureInformation` (name+type per param; empty-param case).
- [ ] GREEN: extract + implement; **rerun hover tests — must stay green (byte-identical output)**.
- [ ] gofmt / go vet clean; helper is pure (no I/O, no locks).
- [ ] No `internal/model` / cache change.

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** T2 not required; independent, but land
before T4/T4a which consume `buildSignatureInformation`.

---

### T4 — CALLNAT signature help (S1 AC1, S1 AC2)

**Behavior.** Flesh out `provideSignatureHelp`: snapshot idx/res/posEncoding/root under RLock (F7),
release before I/O; read the **open buffer first** (store-first) else disk; derive `(line, cursorByteCol)`
via `fromProtocolPosition` + `lineAt`; run `detectSignatureContext` (T2). For `sigCallnat`, find the
enclosing CALLNAT edge and resolve it, then return `buildSignatureInformation` for the resolved
subprogram's PARAMETER `Definitions`.

**Cursor→edge for the arg region:** the cursor may be *after* the target (in the arg list), so
`findCursorTarget` alone (which matches `Source` containment) may not hit if `Source` ends at the target
name. Determine the enclosing call by matching the recognized `sigCallnat` line to the CALLNAT
`EdgeEntry` on that line (e.g. edge whose `Source.Start.Line == cursor line` and kind
`EdgeCalls`/`EdgeCallsDynamic`). If `findCursorTarget` already returns the edge (cursor still on the
widened `Source`), use it; otherwise fall back to the line-matched edge. Document which path is taken and
why (single small helper `enclosingCallEdge(fa, line, kind)`).

**Resolve + fetch callee params:** reuse `provideHover`'s resolved-module path — `res.Get(relPath,
edge.Source)`, on `IsResolved()` do `idx.Get(resolution.Path)`, params from that file's `Definitions`
filtered to `SectionKind=="parameter"` (via T3 helper). Wrap in `SignatureHelp{Signatures: [...],
ActiveSignature: &0}`; activeParameter wired in T5.

**Fixtures.** `internal/server/testdata/signaturehelp/`:
- a caller `.NSP` with `CALLNAT 'SUBPRG' #A #B` and cursor positions on the target and in the arg list;
- the callee `SUBPRG.NSN` with a `DEFINE DATA PARAMETER` block (≥2 params of distinct types, incl. one
  array to exercise dim rendering).

**Expected result.** Cursor on/after the CALLNAT target → `SignatureHelp` with one
`SignatureInformation` whose `Parameters` list matches the subprogram's PARAMETER block (name + type,
array dims rendered), `ActiveSignature = 0`.

**Reuse / migrate.** Reuse `provideHover`'s resolved-module branch (hover.go:300-359), `findCursorTarget`,
`fromProtocolPosition`/`lineAt`, and the T3 `buildSignatureInformation`. Store-first like completion.

**DoD.**
- [ ] RED: test asserting the CALLNAT signature (params, ActiveSignature) from the fixtures.
- [ ] GREEN: implement the CALLNAT path.
- [ ] F7 snapshot discipline (RLock snapshot, release before I/O); store-first buffer read.
- [ ] Never panics on missing callee / unreadable file (returns `nil, nil`).
- [ ] gofmt / go vet clean; `-race`. No model/cache change.

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** T1 (dispatch), T2 (detector), T3 (builder).

---

### T4a — PERFORM signature help, inline-before-external + empty-params (S2 AC1, S2 AC2, S2 AC3, S2 AC4)

**Behavior.** For `sigPerform`, resolve the enclosing PERFORM edge and return the subroutine's parameter
signature, honoring **inline-before-external** (FR-12): an inline `DEFINE SUBROUTINE` in the same file
wins over an external `.NSS`. Mirror `provideHover`'s PERFORM handling: inline → walk
`sourceFA.Structure.Children` for the matching `SymbolSubroutine`, params from `sourceFA.Definitions`;
external → `idx.Get(resolution.Path)`, params from `targetFA.Definitions`. A subroutine with **no
parameters** returns a `SignatureInformation` with an **empty** `Parameters` slice (not an error, not
`nil`) — Story 2 AC4.

**Fixtures.** `internal/server/testdata/signaturehelp/`:
- a `.NSP`/`.NSS` with an inline `DEFINE SUBROUTINE` (with a PARAMETER-backed signature) AND an external
  `.NSS` of the same name reachable via the steplib chain, to prove inline wins (FR-12);
- a subroutine fixture with **zero** parameters (empty-signature case).

**Expected result.** Cursor on/after a PERFORM target with an inline definition → the inline subroutine's
signature (external ignored). PERFORM to a param-less subroutine → `SignatureInformation` with empty
`Parameters`.

**Reuse / migrate.** Reuse `provideHover`'s inline/external PERFORM branch (hover.go:308-345), the T3
builder, and `enclosingCallEdge` (T4) parameterized by `EdgePerforms`.

**DoD.**
- [ ] RED: inline-wins test + empty-params test.
- [ ] GREEN: PERFORM path.
- [ ] FR-12 inline-before-external asserted; empty-params yields non-nil empty signature.
- [ ] gofmt / go vet clean; `-race`. No model/cache change.

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** T4.

---

### T5 — Active-parameter tracking, clamp/clear (S1 AC3, S3 AC1, S3 AC2)

**Behavior.** Wire the T2 `argIndex` into the response: set `SignatureHelp.ActiveParameter` (and/or
`SignatureInformation.ActiveParameter`) to the cursor's argument index. When the index exceeds the number
of declared parameters, **clamp to the last parameter** (chosen default; Story 3 AC2 permits clamp OR
clear) — a param-less signature leaves active-parameter unset (omitted). Never crash on any index.

Encode the index with the correct union type: `protocol.Nullable[uint32]` set to the clamped value; the
absent/omitted state is the zero `Nullable[uint32]{}` (relies on `omitzero`).

**Fixtures.** Reuse T4's CALLNAT fixture; add cursor positions at arg 0, arg 1, arg N, and **past** the
last declared parameter.

**Expected result.** activeParameter tracks the cursor across the space-separated arg list; a cursor past
the last parameter clamps to the last index (does not panic); a param-less signature omits activeParameter.

**Reuse / migrate.** Uses T2's `argIndex`; clamps against `len(params)` from the T3 helper. Verify the
serialized JSON reflects the union/Nullable field correctly (part of T1's `MarshalJSONTo` path).

**DoD.**
- [ ] RED: table/fixture test asserting activeParameter at arg 0/1/N/past-last (clamp) and param-less (omit).
- [ ] GREEN: wire + clamp.
- [ ] Assert the serialized JSON (via `MarshalJSONTo`) carries `activeParameter` correctly.
- [ ] Never panics on out-of-range index.
- [ ] gofmt / go vet clean.

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** T4 (+ T4a for the PERFORM clamp case).

---

### T6 — Modeled gaps + null-when-not-in-context + fuzz (S1 AC4, S4 AC2, FR-17, FR-43)

**Behavior.** Cover the honest-degradation cases, all returning `nil, nil` (→ JSON `null`), never a
diagnostic or JSON-RPC error (FR-17):
- **dynamic** target (`CALLNAT #VAR` or an `&`-placeholder literal) → resolution `IsDynamic()` → no signature;
- **unresolved** literal (no match in steplib chain) → `IsUnresolved()` → no signature;
- **ambiguous** flat-namespace match → `IsAmbiguous()` → no signature (do not fabricate/pick one);
- **not in a call context** (`sigNone`) → no signature (S4 AC2);
- **FETCH / RUN target** (`sigNone` by design — no declared parameter interface, natural-expert-verified) → `null`, no error, no fabricated signature (explicit negative test);
- missing/unreadable file, URI outside root, nil idx/res, no cursor target → `nil, nil` (FR-43).

Add fuzz guards.

**Fixtures.** `internal/server/testdata/signaturehelp/`: a caller with `CALLNAT #VAR` (dynamic) and a
`CALLNAT 'NOPE'` (unresolved). Ambiguity can reuse an existing flat-namespace resolution fixture pattern
if one exists under `testdata/`, else a minimal two-library collision fixture.

**Expected result.** Each modeled-gap case returns JSON `null` with no error and **no diagnostic** emitted;
a `signatureHelp` request on a plain non-call line returns `null`.

**Reuse / migrate.** Mirror `provideHover`'s dynamic/ambiguous/unresolved branches (hover.go:362-399) but
returning `nil` instead of an honest-message card. Confirm signature help reads only resolution outcomes,
never the diagnostic channel (FR-17).

**DoD.**
- [ ] RED: dynamic / unresolved / ambiguous / not-in-context / missing-file cases → `nil, nil` (JSON `null`).
- [ ] GREEN: gap handling.
- [ ] `FuzzProvideSignatureHelp` (arbitrary content + position + encoding) never panics (FR-43), matching
      the other `FuzzProvideXxx` targets.
- [ ] Assert no diagnostic is produced by a signature-help request (FR-17).
- [ ] gofmt / go vet clean; `-race`. No model/cache change.

**Agents.** tdd-red → tdd-green → tdd-refactor. **Depends on:** T4, T4a, T5.

---

## Reviews required (`/review-feature`)

- **protocol-conformance** — a new LSP method + capability; verify the `signatureHelpProvider` shape,
  trigger/retrigger characters, `SignatureHelp` on-the-wire JSON (union/Nullable fields via
  `MarshalJSONTo`, NOT stdlib `json.Marshal`), and `null` on no-context.
- **robustness / graceful degradation** — FR-43 fuzz targets; modeled gaps (FR-17) return `null`, never a
  diagnostic or error; clamp on past-last activeParameter; store-first + F7 snapshot discipline.
- **concurrency** — provider snapshots idx/res under RLock and releases before I/O (F7); no in-place
  mutation raced. (Light — no new indexer/watcher code.)
- **review-docs** — capability set changed (new `signatureHelpProvider`); the "Project state" note and
  README capability/feature list must sync at `/finalize-feature`.
- (Not needed: **review-seam** — no `internal/model`/Analyzer/index contract change.)

---

## Approved decisions (checkpoint)

Resolved with the user before implementation (OQ-2 confirmed against the natural-expert):

- **OQ-1 → space trigger + retrigger.** `SignatureHelpOptions{TriggerCharacters:[" "], RetriggerCharacters:[" "]}`, plus explicit invoke. Fixes T1's capability literal + the `TestInitialize` assertion.
- **OQ-2 → defer FETCH/RUN (confirmed correct, not just scoped out).** The natural-expert verified that programs invoked via FETCH/RUN receive data via the **Natural stack (INPUT)** — positional, untyped, with **no declared parameter interface**; a `DEFINE DATA PARAMETER` block in a program has no caller to bind it. Signature help for FETCH/RUN would fabricate non-existent metadata (FR-17 violation). So the detector classifies **only CALLNAT + PERFORM** as signature contexts; a cursor on/after a FETCH/RUN target returns `null` (via the `sigNone` path). **T6 adds an explicit FETCH/RUN → null negative test** to document the category-error decision. (Sourcing recorded in `.claude/knowledge/natural/calls-and-resolution.md`.)
- **OQ-3 → clamp to last** parameter past the last arg; param-less signature omits activeParameter.
- **Function-call (`.NS7`) signature help is explicitly OUT of scope** — a valid future feature (functions bind operands onto a PARAMETER block and use parentheses), but it needs function-call-site extraction verified first. Not in feature 17.

## Open questions

1. **Trigger characters (OQ-1, plan open question / Story 4).** Natural has no parentheses.
   **Planned default:** `TriggerCharacters: [" "]` + explicit invocation (consistent with feature 16
   completion) and `RetriggerCharacters: [" "]` so the signature updates (re-fires) as each
   space-separated argument is typed, driving activeParameter. If the user picks otherwise, only T1's
   capability literal and `TestInitialize`'s assertion change. **Resolve at checkpoint.**
2. **FETCH / RUN program-transfer signature help (OQ-2, plan open question).** **Planned default:
   defer** — out of scope for this feature (program stack-parameter semantics differ from CALLNAT/PERFORM
   formal parameters); the T2 detector classifies only CALLNAT + PERFORM as signature contexts, noted as
   future work. **Resolve at checkpoint.**
3. **activeParameter clamp vs clear (Story 3 AC2 permits either).** Planned default: **clamp to last
   parameter**; a param-less signature omits the field. Called out in T5; flip is a one-line change.
