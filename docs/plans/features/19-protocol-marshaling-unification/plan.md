# Feature: Protocol Marshaling Unification

**Status:** Planned
**PRD requirements:** FR-47 (repair), NFR-11
**Priority / phase:** P0 remediation (2026-07-14 assessment, defect #1)
**Depends on:** [16](../16-completion/plan.md)

## Summary

Fix the confirmed wire-format defect in `textDocument/completion` and eliminate its systemic
cause. `protocol.Optional[T]` (go.lsp.dev/protocol v1.0.0) implements only the json/v2
`MarshalJSONTo`, but the completion dispatch (`internal/server/server.go:881`) marshals results
with stdlib `json.Marshal` — so `CompletionItem.detail` and `sortText` reach the client as `{}`
instead of strings (captured live against the sample workspace). The detail label is corrupted
and the `SortText`-based inline-subroutine-before-external ordering silently breaks. SignatureHelp
and call hierarchy already use the correct json/v2 path; this feature unifies **all** provider
result marshaling on that path and adds wire-bytes regression tests so struct-level tests can
never again mask a serialization defect.

## User stories

### Story 1 — Completion items are correct on the wire (FR-47, NFR-11)
**As an** editor user, **I want** completion items to carry their real `detail` and `sortText`
strings **so that** I see the object-type label and inline subroutines sort before external ones.

**Acceptance criteria:**
- [ ] The serialized JSON for a completion result contains `"detail":"<string>"` and
      `"sortText":"<string>"` — never `{}` — for items where those fields are set.
- [ ] A regression test asserts on the **emitted bytes** (marshal the provider result exactly as
      the dispatch does and inspect the JSON), reproducing the `{}` corruption first (red), then
      passing after the fix.
- [ ] Inline-vs-external PERFORM ordering via `SortText` (`"0…"`/`"1…"`) is asserted at the wire
      level, not only on Go structs.

### Story 2 — One marshaling path for every provider result (NFR-11)
**As a** maintainer, **I want** all LSP result marshaling to go through the json/v2-aware path
(`gojson.Marshal` / `MarshalJSONTo`) **so that** adding an `Optional`/`Nullable`/union field to
any provider can never silently corrupt the wire again.

**Acceptance criteria:**
- [ ] Every request handler in `internal/server/server.go` (definition, references,
      workspace/symbol, documentSymbol, hover, codeLens, completion, signatureHelp, call
      hierarchy ×3) marshals its result via the json/v2 path; stdlib `json.Marshal` is no longer
      used for protocol result types.
- [ ] Existing wire behavior is preserved byte-for-byte for the already-correct providers
      (empty results still marshal to `[]`/`null` exactly as before — pinned by tests).
- [ ] A guard (test or lint recipe) fails if `json.Marshal` is reintroduced for a
      `go.lsp.dev/protocol` result type in `internal/server`.

### Story 3 — Wire-bytes coverage for high-risk types (NFR-9)
**As a** maintainer, **I want** wire-level assertions for every provider whose result type
contains `Optional`/`Nullable`/union fields **so that** the struct-vs-wire blind spot identified
by the assessment is closed permanently.

**Acceptance criteria:**
- [ ] Wire-bytes tests exist for completion, signatureHelp, and the three call-hierarchy
      results (the union/Nullable carriers), asserting presence and JSON type of the
      optional fields.
- [ ] `just verify` passes; no `internal/model` change and no cache-format bump (server-layer
      serialization only).
