---
name: tdd-implementation
description: >-
  Drive a planned feature through implementation, one task at a time, via the red-green-refactor TDD
  loop. Use when implementing tasks from a docs/plans/features/<feature>/tasks.md (e.g. via
  /implement-feature) — the top-level loop dispatches the tdd-red, tdd-green, and tdd-refactor subagents
  per task and enforces the gates between phases. Planning and review are separate phases.
---

# TDD implementation loop for natural-lsp

You (the top-level agent) drive implementation of a **planned** feature. You spawn the TDD subagents in
sequence per task and enforce the discipline between phases — **the agents do the work; you sequence and
gate them.** Read `CLAUDE.md` for context.

## Inputs

- `docs/plans/features/<feature>/tasks.md` — the ordered task list from `feature-planner`, the source
  of truth for *what* to build and *in what order*. If it doesn't exist, stop and run `/plan-feature`
  first; don't improvise a plan here.
- Each task's Definition-of-Done checklist, and the plan's "Reviews required" section (handed to
  `/review-feature` at the end).

## The loop — for each task, in plan order

Work strictly **one task at a time**, and within a task **one behavior at a time**. Do not start a task
until the previous task's DoD is met and the suite is green.

1. **RED** — spawn `tdd-red` with the task: its behavior, the `testdata/` fixture(s) it names, and the
   expected result. It writes one failing test (plus a minimal stub so it compiles, plus the fixture
   for analyzer work).
   - **Gate:** confirm the test exists and **fails for the right reason** — an assertion failure, not a
     build error. Run `go test -run <TestName> ./internal/...` yourself to verify. If it doesn't fail as
     expected, send it back to `tdd-red`; do not proceed.
2. **GREEN** — spawn `tdd-green` with the failing test. It writes the minimal code to pass.
   - **Gate:** run `go test ./...` (and `-race` if the task touches concurrency). The target test passes,
     nothing else broke, and the test was **not modified**. If green can't be reached, return to
     `tdd-red` to reconsider the test or surface a blocker — **never weaken the test to make it pass.**
3. **REFACTOR** — spawn `tdd-refactor`. It improves design and robustness while keeping the suite green.
   - **Gate:** `gofmt` clean, `go vet ./...` clean, `go test -race ./...` green, and the task's DoD
     satisfied (seam purity, graceful degradation, deterministic output, a fuzz target where the parser
     widened).
4. Mark the task done; record which acceptance criteria / FR-IDs it satisfied. Move to the next task.

## Checkpoints & stopping

- **Multi-behavior tasks:** iterate red → green → refactor once per behavior before marking the task done.
- **Stop and ask the user** when: an acceptance criterion is ambiguous, a task reveals a design decision
  not in the plan, a phase can't be completed after a reasonable retry, or the plan itself looks wrong
  (route back to `/plan-feature`). Don't guess past a spec gap.
- **Invariants:** never modify a test to make it pass; never skip the refactor gate; never start the
  next task on a red suite.

## Handoff

When all tasks (or the requested subset) are done and green, summarize what was implemented and which
FR-IDs are now covered, and recommend running **`/review-feature`** for the dimensions listed in the
plan's "Reviews required" section. Implementation does **not** self-certify — review is independent.

## Boundary

You sequence and gate; the TDD subagents implement. Do not write production code or tests yourself in
this flow, and do not run the reviews here (that is `/review-feature`).
