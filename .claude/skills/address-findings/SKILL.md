---
description: Turn /review-feature findings into regression-first fixes — reproduce each as a failing test, fix it through the TDD loop, and re-review until the verdict is PASS
argument-hint: "<feature> (addresses the latest review findings for it)"
---

Close the review → fix → re-review loop for a feature, using the `tdd-implementation` skill in
remediation mode.

Feature: $ARGUMENTS

## Workflow

1. **Ensure the feature branch.** Per `CLAUDE.md`, you must be on `feat/<feature>` (not `main`).
2. **Collect the findings.** Take the actionable findings from the latest `/review-feature` for this
   feature — every `blocker`, plus any `major`/`minor` the user chose to address. If no findings were
   provided, ask for the review report rather than guessing.
3. **Append regression-first remediation tasks** to `docs/plans/features/<feature>/tasks.md`: one task
   per finding, each noting the originating finding and severity, and structured red → green → refactor
   — the RED step writes a **failing test that reproduces the finding** (the test that *should* have
   caught it), then GREEN fixes it.
4. **Run the `tdd-implementation` loop** over the new remediation tasks only (one at a time, gated:
   fail-for-the-right-reason → green → `gofmt`/`vet`/`-race`/DoD).
5. **Re-review.** When the remediation tasks are green, recommend re-running `/review-feature`. Repeat
   this loop until the review verdict is `PASS` (or `CONCERNS` the user explicitly accepts). Do **not**
   proceed to `/finalize-feature` until then.
6. **Never mark a finding resolved without a test** that would have caught it — a fix without a
   reproducing test isn't done.
