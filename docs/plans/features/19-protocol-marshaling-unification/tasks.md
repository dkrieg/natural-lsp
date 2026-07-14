# Tasks: Protocol Marshaling Unification (feature 19)

**Spec:** `docs/plans/features/19-protocol-marshaling-unification/plan.md`
**PRD requirements:** FR-47 (repair), NFR-9, NFR-11
**Constraints:** server-layer serialization only — **no `internal/model` change, no cache-format bump**
(stays `0.6.0`). Preserve existing wire behavior **byte-for-byte** for already-correct providers. Respect
the Analyzer seam (this feature never touches `internal/analysis/*`; it edits only `internal/server/`).

---

## Current-state findings & impact

Verified against the code on `main` (commit 3663fd9) — plan against this, not the README.

1. **The defect is confirmed.** `internal/server/server.go` dispatches `textDocument/completion` (case at
   line 856) and marshals the non-nil result with **stdlib `json.Marshal`** (line 881). But
   `internal/server/completion.go` sets `Detail` (lines 436, 505) and `SortText` (lines 368, 387) via
   `protocol.NewOptional[string](...)`. `protocol.Optional[T]`
   (`/Users/daniel/go/pkg/mod/go.lsp.dev/protocol@v1.0.0/optional.go`) has only unexported fields
   (`value`, `set`) and implements **only** the json/v2 `MarshalJSONTo` — no stdlib `MarshalJSON`. Under
   stdlib `json.Marshal` these fields serialize to `{}`. **This is a NEW/repair task** (completion is the
   only broken provider today).

2. **The correct pattern already exists in-repo.**
   - **gojson path (slices):** call hierarchy ×3 use `gojson.Marshal(items)` (server.go lines 950, 983,
     1016) — `gojson` is the `github.com/go-json-experiment/json` import aliased at server.go line 20. It
     honors element `MarshalJSONTo` and the `CallHierarchyItem.Data` union.
   - **MarshalJSONTo path (single pointer):** signatureHelp uses `(*protocol.SignatureHelp).MarshalJSONTo`
     via a `jsontext.NewEncoder(&buf)` (server.go lines 916–922).
   - `jsontext` is imported at server.go line 21; `bytes` is already imported.

3. **`gojson.Marshal(nilSlice)` yields `[]`, not `null`** (verified empirically). This is the load-bearing
   fact for preserving wire behavior: **the existing `if x == nil { respResult = []byte("null"|"[]") }`
   guard branches must be kept exactly as-is.** Only the *non-nil* `json.Marshal(x)` call inside each
   `else` gets swapped to `gojson.Marshal(x)`. That leaves the empty-result wire bytes (`null` for
   definition/references/documentSymbol/hover/codeLens; `[]` for workspace/symbol/completion; `[]` for the
   three call-hierarchy methods; `null` for signatureHelp) **byte-identical** to today.

4. **Providers currently on stdlib `json.Marshal` that must migrate (Story 2 AC1):**
   | Method | server.go non-nil marshal line | provider return type | nil branch emits |
   |---|---|---|---|
   | textDocument/definition | 692 | `[]protocol.Location` | `null` |
   | textDocument/references | 724 | `[]protocol.Location` | `null` |
   | workspace/symbol | 752 | `[]protocol.SymbolInformation` | `[]` |
   | textDocument/documentSymbol | 784 | `[]protocol.DocumentSymbol` | `null` |
   | textDocument/hover | 816 | `*protocol.Hover` | `null` |
   | textDocument/codeLens | 848 | `[]protocol.CodeLens` | `null` |
   | textDocument/completion | 881 | `[]protocol.CompletionItem` | `[]` |
   Already on the json/v2 path (do **not** touch marshaling): signatureHelp (916–922, `MarshalJSONTo`),
   prepareCallHierarchy (950), incomingCalls (983), outgoingCalls (1016).

5. **`hover` returns a pointer** (`*protocol.Hover`), not a slice. `gojson.Marshal(hoverPtr)` works and
   honors any nested `MarshalJSONTo`; the nil-guard stays. No need to switch hover to the encoder form —
   `gojson.Marshal` is the uniform slice-and-pointer choice for the seven migrating cases. (signatureHelp
   stays on `MarshalJSONTo` because that idiom is already correct and tested; unifying it to `gojson` is
   optional and out of scope unless review requests it — see Open questions.)

6. **Tests miss the bug because they assert on the Go struct, not the wire.**
   `internal/server/completion_test.go` reads `found.Detail.Get()` (lines 599, 898, 1468) *before*
   marshaling — the `Optional` is fully populated in memory, so the struct-level assertion passes while the
   emitted JSON is `{}`. Story 1/3 require **wire-bytes** assertions (marshal exactly as the dispatch does,
   then inspect the JSON). A wire-level idiom already exists at
   `internal/server/signature_help_test.go:1288–1300` (`TestProvideSignatureHelp_MarshaledActiveParameter`)
   — reuse that shape.

7. **`just verify` = `fmt-check lint-tests vet build test test-integration`** (justfile line 51). There is
   an existing `lint-tests` grep-based recipe (justfile lines 27–36) to model a `json.Marshal` guard on if
   we choose the recipe route for Story 2 AC3.

**No consumers break:** this is internal serialization; the JSON-RPC wire contract is *preserved* (and, for
completion, *corrected*). No `internal/model` or Analyzer-interface surface changes, so no migration tasks
for downstream packages.

---

## Traceability (every AC → task)

| AC | Task |
|---|---|
| Story 1 AC1 (`detail`/`sortText` are strings, never `{}`) | T2 (fix), asserted by T1 (RED) |
| Story 1 AC2 (wire-bytes regression test, red then green) | T1 → T2 |
| Story 1 AC3 (inline-vs-external `SortText` ordering at wire level) | T1 / T3 |
| Story 2 AC1 (all handlers on json/v2 path) | T2 (completion) + T4 (the other six) |
| Story 2 AC2 (empty results preserved byte-for-byte) | T4 (pin tests) + T5 confirm |
| Story 2 AC3 (guard against `json.Marshal` reintroduction) | T6 |
| Story 3 AC1 (wire tests for completion + signatureHelp + call-hierarchy ×3) | T1, T3 |
| Story 3 AC2 (`just verify` green; no model/cache change) | T7 |

---

## Task list

### T1 — RED: reproduce the completion `{}` corruption at the wire level
**Story 1 AC2 / AC1 (red half); Story 3 AC1 (completion).**
Add a wire-bytes test in `internal/server/completion_test.go` (new `TestProvideCompletion_WireBytes...`
functions). The test must **marshal the provider result exactly as the current dispatch does** — i.e. via
stdlib `json.Marshal([]protocol.CompletionItem)` — so it captures today's real emitted bytes, then assert:
- for a module completion (e.g. a `CALLNAT` prefix), the JSON contains `"detail":"subprogram"`
  (a JSON **string**), and specifically does **not** contain `"detail":{}`.
- for the PERFORM inline-vs-external case, the JSON contains `"sortText":"0..."` on the inline item and
  `"sortText":"1..."` on the external item (JSON string values), asserting the ordering group at the wire
  level (Story 1 AC3).

Reuse an existing completion fixture — the `CALLNAT` module case already exercised by
`completion_test.go` (the `CALLGREET`/subprogram fixtures under
`internal/server/testdata/completion/`) and the PERFORM inline+external fixture used by the existing
`SortText` test around line 898. **No new fixtures required** (confirm the needed ones exist during RED; if
the inline+external PERFORM fixture is absent, add a minimal one under
`internal/server/testdata/completion/`).

**Expected in RED:** the test **fails** — asserting on bytes produced by `json.Marshal`, `detail`/`sortText`
serialize to `{}` (matches the live `{"label":"CALLGREET","kind":9,"detail":{}}` capture).

**DoD**
- [ ] New wire-bytes test(s) added to `completion_test.go` asserting on emitted JSON, not `Detail.Get()`.
- [ ] Test marshals via the *same call the dispatch uses today* (stdlib `json.Marshal`) so RED reproduces
      the real defect (do not pre-fix the marshaler in the test helper).
- [ ] Test **fails** for the documented reason (`{}` instead of the string).
- **Agent:** `tdd-red`.

### T2 — GREEN: fix completion dispatch to marshal via the json/v2 path
**Story 1 AC1/AC2 (green); Story 2 AC1 (completion).**
In `internal/server/server.go`, `case "textDocument/completion"` (the `else` branch at line 881), replace
`json.Marshal(items)` with `gojson.Marshal(items)`. **Leave the `if items == nil { respResult =
[]byte("[]") }` branch untouched** (nil → `[]` preserved; gojson would also yield `[]` but keep the explicit
branch so empty-behavior stays provable and unchanged). Then flip the T1 test's marshal call to the dispatch
path (`gojson.Marshal`) so the assertion now reflects the fixed dispatch — OR, cleaner: have T1's test call
a tiny shared test helper that mirrors the dispatch's marshal choice, and update that helper. Keep the
existing struct-level completion tests passing unchanged.

**Expected in GREEN:** completion JSON now contains `"detail":"subprogram"` and `"sortText":"0..."/"1..."`
as strings; T1 passes.

**DoD**
- [ ] `completion` non-nil branch uses `gojson.Marshal`; nil branch still `[]byte("[]")`.
- [ ] T1 wire-bytes test passes (detail/sortText are JSON strings; no `{}`).
- [ ] All pre-existing completion struct-level tests still pass (no regression).
- [ ] `go vet ./...` clean; `stdlib json` import still used elsewhere or removed if now unused (see T4/T5).
- **Agent:** `tdd-green`.

### T3 — RED→GREEN: wire-bytes coverage for the remaining high-risk (Optional/Nullable/union) types
**Story 3 AC1 (signatureHelp + call-hierarchy ×3).**
signatureHelp and the three call-hierarchy providers already marshal correctly, so these tests are
**characterization/lock tests** (they should pass immediately once written against the correct dispatch
path — a "green-on-arrival" guard, no production change). Add wire-bytes tests asserting presence and JSON
type of the optional/union fields:
- **signatureHelp** (`signature_help_test.go`): marshal via `(*protocol.SignatureHelp).MarshalJSONTo`
  (as the dispatch does) and assert `"activeParameter":<number>` for a set case and its **omission** for a
  param-less signature — extend/parallel the existing `TestProvideSignatureHelp_MarshaledActiveParameter`
  (line 1209) rather than duplicate it. Assert `ParameterInformation.label` is a JSON **string** (union
  carrier), not `{}`/`null`.
- **prepareCallHierarchy / incomingCalls / outgoingCalls** (`call_hierarchy_test.go`): marshal the result
  via `gojson.Marshal` (as the dispatch does) and assert the `CallHierarchyItem.data` union field is
  present and is a JSON **object** (the `{path,name,kind}` identity), and that `fromRanges` is an array on
  incoming/outgoing results. Confirm an **empty** result marshals to `[]` (never `null`) — locking Story 2
  AC2 for the call-hierarchy trio.

Reuse existing fixtures under `internal/server/testdata/signaturehelp/` and
`internal/server/testdata/callhierarchy/`.

**Expected:** all four tests **pass** without touching production (they characterize the already-correct
path). If any fails, that surfaces a latent defect — escalate rather than silently paper over.

**DoD**
- [ ] Wire-bytes tests for signatureHelp `activeParameter` (set + omitted) and `label` union string.
- [ ] Wire-bytes tests for the three call-hierarchy results: `data` union object present; `fromRanges`
      arrays present; empty result → `[]`.
- [ ] Tests marshal via the *exact* path the dispatch uses (`MarshalJSONTo` / `gojson.Marshal`).
- [ ] All pass with no production change.
- **Agent:** `tdd-red` then `tdd-green` (expected trivially green; if not, a real fix is a separate slice).

### T4 — RED→GREEN: migrate the six remaining stdlib-`json.Marshal` handlers, pinning empty-result bytes
**Story 2 AC1 (definition, references, workspace/symbol, documentSymbol, hover, codeLens); Story 2 AC2.**
First (RED-ish): add **pin tests** that marshal each provider's result via the current dispatch path and
lock the wire bytes for representative cases — specifically the **empty/nil** case for each
(definition→`null`, references→`null`, workspace/symbol→`[]`, documentSymbol→`null`, hover→`null`,
codeLens→`null`) and one non-empty case each. Place them beside the relevant existing tests
(`definition_test.go`, `references_test.go`, `workspace_symbols_test.go`, `document_symbols_test.go`,
`hover_test.go`, `code_lens_test.go`). These characterize current bytes.

Then (GREEN): in `server.go`, swap `json.Marshal(x)` → `gojson.Marshal(x)` in the **non-nil `else`
branch** of each of the six cases (lines 692, 724, 752, 784, 816, 848). **Do not alter any `if x == nil {
respResult = []byte("null"|"[]") }` branch.** Re-run the pin tests to confirm bytes are **unchanged** (this
is the byte-for-byte preservation proof for Story 2 AC2). These providers carry no Optional/union today, so
non-empty bytes are also expected to be identical.

Because these providers have no `Optional` field yet, the RED tests pass on both old and new marshalers;
their value is the **regression pin** proving the swap is behavior-neutral. Sequence them before the swap so
a diff would be caught.

**DoD**
- [ ] Pin tests added for all six providers (empty case bytes + one non-empty case).
- [ ] All six non-nil branches use `gojson.Marshal`; every nil-guard branch byte-identical to before.
- [ ] Pin tests pass **after** the swap with the same asserted bytes as before (no wire diff).
- [ ] No handler in `server.go` uses stdlib `json.Marshal` for a protocol result type anymore.
- **Agents:** `tdd-red` (pins) → `tdd-green` (swap).

### T5 — REFACTOR: remove now-dead stdlib `json` usage / de-duplicate the marshal idiom
**Story 2 AC1 hygiene.**
After T2+T4, audit `server.go` for remaining `json.Marshal`/`encoding/json` usage. If the `encoding/json`
import is no longer used **anywhere** in server.go, remove it (else leave it and note where it's still
needed — e.g. non-result uses). Optionally extract the repeated `if x == nil { <null|[]> } else { b, err :=
gojson.Marshal(x); ... }` into a small local `marshalResult`/`marshalArrayResult` helper to reduce the
copy-paste across the nine cases — **only if** it does not change emitted bytes (keep the per-case nil
sentinel: `null` vs `[]` differs per method). Refactor must keep every existing test (T1/T3/T4 + prior)
green.

**DoD**
- [ ] No unused imports; `gofmt`/`go vet` clean.
- [ ] If a helper is introduced, it preserves each method's exact nil sentinel and all tests stay green.
- [ ] No behavior change (all wire/pin tests unchanged).
- **Agent:** `tdd-refactor`.

### T6 — Guard against reintroducing `json.Marshal` for protocol result types
**Story 2 AC3.**
Add an automated guard that **fails** if stdlib `json.Marshal` (or `encoding/json` used to marshal) is
reintroduced in `internal/server` for a `go.lsp.dev/protocol` result type. Prefer a **Go test** over a lint
recipe for portability with `just verify` (which runs `test`), but either satisfies the AC:
- **Option A (test, preferred):** a test in `internal/server` that scans the package's own source files
  (read `server.go` and any dispatch file) and fails if it finds a `json.Marshal(` call in a request-result
  marshaling context. Keep it robust: allow-list any legitimate non-result `json.Marshal` uses (there may be
  none after T5), or assert simply that `server.go` contains no `json.Marshal(` token at all if T5 removed
  them. Document the intent in the test comment.
- **Option B (lint recipe):** a `justfile` recipe modeled on `lint-tests` (lines 27–36) that greps
  `internal/server` for `json\.Marshal` and exits non-zero on a hit, wired into `verify` before `test`.

Choose Option A unless the user prefers the recipe (see Open questions). The guard must **fail on a
deliberately planted `json.Marshal` on a result** (demonstrate in the DoD by temporarily reintroducing one
locally and confirming red, then reverting).

**DoD**
- [ ] Guard exists and **fails** when `json.Marshal` is used for a protocol result in `internal/server`
      (verified by a temporary reintroduction, then reverted).
- [ ] Guard **passes** on the clean tree.
- [ ] Guard runs as part of `just verify` (via `test`, or via `verify` deps if a recipe).
- **Agent:** `tdd-red` (guard fails on planted regression) → `tdd-green` (clean tree passes). Purely a
  test/tooling slice — no production code.

### T7 — Verify & docs sync
**Story 3 AC2.**
Run `just verify` (fmt-check + lint-tests + vet + build + test -race + integration). Confirm **no
`internal/model` change and no cache-format bump** (grep for the cache version constant — must still be
`0.6.0`). Update `CLAUDE.md` "Project state" and `README.md` if they describe the marshaling path (note the
completion `{}` fix and the unified `gojson.Marshal` path for all provider results); this is done by
`/finalize-feature` but list it so it isn't missed.

**DoD**
- [ ] `just verify` passes clean.
- [ ] Cache-format version unchanged (`0.6.0`); no `internal/model` diff.
- [ ] Docs reflect the unified marshaling path (handled at finalize).
- **Agent:** none (verification/docs).

---

## Reviews required (for `/review-feature`)

- **code-reviewer:** confirm every migrating case swapped `json.Marshal`→`gojson.Marshal` in the *non-nil*
  branch only; confirm no nil-guard sentinel (`null` vs `[]`) was altered per method; confirm hover
  (pointer) and the slice cases all round-trip; confirm signatureHelp/call-hierarchy marshaling untouched.
- **test-reviewer:** confirm the new tests assert on **emitted bytes**, not `Optional.Get()`; confirm the
  completion RED test reproduced `{}` before the fix; confirm empty-result pins prove byte-for-byte
  preservation; confirm the T6 guard actually fails on a planted `json.Marshal`.
- **review-docs:** confirm CLAUDE.md/README describe the corrected completion behavior and the single
  json/v2 marshaling path; confirm the "no model/cache change" claim.

---

## Open questions — RESOLVED (user, 2026-07-14)

1. **T6 guard mechanism — RESOLVED: Go test.** Implement the guard as a Go test in `internal/server`
   (Option A) that scans the package source and fails on a `json.Marshal(` call for a protocol result
   type. Runs under `just verify`'s existing `test` step; no `justfile` change.
2. **signatureHelp unification — RESOLVED: leave as-is.** signatureHelp stays on
   `(*protocol.SignatureHelp).MarshalJSONTo`, which satisfies Story 2 AC1's "json/v2 path" wording. Do
   **not** convert it to `gojson.Marshal` (avoids risking the currently-correct `Nullable`/union bytes).
   T3 characterization tests still lock its wire bytes.
3. **Refactor helper (T5) — RESOLVED: extract if byte-neutral.** Introduce a shared
   `marshalResult`/`marshalArrayResult` helper that takes the per-method nil sentinel (`null` vs `[]`) as
   a parameter — only if it keeps every wire/pin test byte-identical. If any byte differs, keep the cases
   inline instead.
