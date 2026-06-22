# Feature: Signature Help

**Status:** Planned
**PRD requirements:** FR-48
**Priority / phase:** P1
**Depends on:** [06](../06-call-and-dependency-resolution/plan.md),
[07](../07-data-access-extraction/plan.md), [08](../08-program-structure-extraction/plan.md)

## Summary

When the cursor is on or after a CALLNAT or PERFORM target, display the callee's parameter interface
in the editor's signature-help UI — so the developer can see what arguments are expected without
leaving the call site.

## User stories

### Story 1 — Signature help for CALLNAT targets (FR-48)
**As a** developer, **I want** to see the parameter interface of a subprogram when I am writing a
CALLNAT statement **so that** I know what arguments to supply.

**Acceptance criteria:**
- [ ] When the cursor is positioned on or after a resolved CALLNAT target, the server returns a
      `SignatureInformation` describing the callee's `DEFINE DATA PARAMETER` block.
- [ ] Each parameter appears as a separate `ParameterInformation` with at minimum its name and type.
- [ ] The active parameter is highlighted as the cursor moves through the argument list.
- [ ] If the target is unresolved (dynamic or missing), no signature is returned and no error is
      surfaced.

### Story 2 — Signature help for PERFORM targets (FR-48)
**As a** developer, **I want** to see a subroutine's parameter interface when I am writing a PERFORM
statement **so that** I know what parameters to pass.

**Acceptance criteria:**
- [ ] When the cursor is on or after a resolved PERFORM target, the server returns a
      `SignatureInformation` for the subroutine (inline or external).
- [ ] Inline-before-external resolution (FR-12) is honoured: the signature reflects whichever
      subroutine would actually be performed.
- [ ] Each parameter appears as a `ParameterInformation` with its name and type from the extracted
      parameter interface (plan 07).
- [ ] If the subroutine has no parameters, an empty signature is returned (not an error).

### Story 3 — Active-parameter tracking (FR-48)
**As a** developer, **I want** the current parameter to be highlighted as I move through the argument
list **so that** I know which parameter I am currently filling in.

**Acceptance criteria:**
- [ ] The `activeParameter` index returned in `SignatureHelp` matches the logical position of the
      cursor within the argument list.
- [ ] Moving past the last declared parameter does not crash the server; the response either clamps
      to the last parameter or clears the active index.

### Story 4 — Capability advertisement (FR-48)
**As a** client, **I want** the server to advertise signature-help support in `ServerCapabilities`
**so that** I know when to trigger the `textDocument/signatureHelp` request.

**Acceptance criteria:**
- [ ] `ServerCapabilities.signatureHelpProvider` is populated with appropriate trigger characters
      (e.g. space or open-paren where Natural uses them syntactically).
- [ ] The server handles `textDocument/signatureHelp` requests without error at any cursor position,
      returning `null` when not in a call context.

## Out of scope
- Completion of the argument values themselves — that is feature 15 (completion).
- Hover at non-call positions — that is feature 11 (hover).

## Open questions
- Natural does not use parentheses for argument lists; what trigger characters are appropriate for
  signatureHelp (space after keyword? explicit invocation only)?
- Whether signature help should activate for FETCH/program-transfer statements as well.