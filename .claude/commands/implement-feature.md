---
description: Implement a planned feature by driving each task through the red-green-refactor TDD loop, gating between phases, until the feature is done and green
argument-hint: "<feature> [optional: a task ID to implement just that one task]"
---

Implement a planned feature using the `tdd-implementation` skill. **You** (the top-level loop) sequence
and gate the TDD subagents — they implement; you enforce the phase gates. Do not write code or tests
yourself, and do not run reviews here.

Target: $ARGUMENTS

1. **Load** the `tdd-implementation` skill.
2. **Read** `docs/plans/features/<feature>/tasks.md`. If it doesn't exist, stop and recommend
   `/plan-feature` first. If a task ID was given above, scope to that task; otherwise implement all
   tasks in plan order.
3. **Run the loop per task** (one task at a time, never starting the next on a red suite):
   spawn `tdd-red` → verify the test fails for the right reason (`go test -run <TestName>`) → spawn
   `tdd-green` → verify `go test ./...` is green (and `-race` if concurrent) → spawn `tdd-refactor` →
   verify the gates (`gofmt`, `go vet ./...`, `go test -race ./...`, the task's DoD).
4. **Stop and ask the user** on an ambiguous acceptance criterion, a design decision not in the plan, a
   phase that can't complete after a reasonable retry, or a plan that looks wrong (route back to
   `/plan-feature`). Never modify a test to make it pass.
5. **When done**, summarize what was implemented and the FR-IDs covered, and recommend `/review-feature`
   for the dimensions in the plan's "Reviews required" section. Do not self-certify — review is
   independent.
