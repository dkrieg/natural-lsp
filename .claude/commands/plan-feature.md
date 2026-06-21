---
description: Plan a feature — convert a feature plan into an ordered, TDD-structured task list with traceable acceptance criteria and a definition of done
argument-hint: "<feature> (name or path under docs/plans/features/)"
---

Use the feature-planner subagent to turn a feature plan into an implementable, test-first task plan.

Feature: $ARGUMENTS

If no feature is given above, ask which feature plan under `docs/plans/features/` to plan before
proceeding.

Instruct the planner to:

1. Read the feature plan under `docs/plans/features/<feature>/`, the FR-/NFR-IDs it references in
   `docs/plans/natural-lsp-prd.md`, and the architecture constraints in `CLAUDE.md` / `README.md`.
   Apply the `feature-planning` skill.
2. Decompose into dependency-ordered tasks, each a red → green → refactor slice naming the `testdata/`
   fixtures it needs, the exact expected extraction/LSP result, and explicit modeled-gap coverage
   (`CALLS_DYNAMIC`, diagnostics). Every acceptance criterion must map to at least one task.
3. Write `docs/plans/features/<feature>/tasks.md` — header with the FR/NFR-IDs covered; ordered tasks
   with DoD checklists and the TDD agents to run; a "Reviews required" section for `/review-feature`;
   an "Open questions" section.
4. Report the task list and surface every open question or decision — do **not** start implementing.

Once the planner reports back, relay the task summary and open questions, and let the user review the
plan before any implementation begins.
