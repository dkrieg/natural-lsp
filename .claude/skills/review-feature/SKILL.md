---
description: Review a completed feature/diff — select the applicable review dimensions, run the reviewer subagents in parallel, and synthesize one verdict
argument-hint: "[optional: feature name, base ref, or files to review]"
---

Run an independent, multi-dimension review using the `review-orchestration` skill. **You** (the
top-level loop) do the dispatching — the reviewers are subagents and must not spawn each other.

Target: $ARGUMENTS

## Workflow

1. **Load** the `review-orchestration` skill.
2. **Scope** the change: `git diff --stat <base>...` (base defaults to `origin/main`), or the explicit
   files/feature given. If it maps to a feature, read `docs/plans/features/<feature>/tasks.md` and the
   feature plan to recover the acceptance criteria.
3. **Select** the applicable review dimensions from the skill's catalog (always include
   `review-acceptance`). State which you selected and which you skipped, with one line of reasoning each.
4. **Dispatch in parallel** — spawn the selected reviewer subagents concurrently (one message, multiple
   Agent calls). Give each: the changed files, the `tasks.md`/feature-plan path, and the base ref.
5. **Synthesize** into one report: overall verdict (`PASS` / `CONCERNS` / `FAIL`), findings grouped by
   severity and deduped across reviewers, leading with whether the acceptance criteria are met; note any
   reviewer that couldn't complete.
6. **Do not fix here** — report only. Fixes are a separate TDD loop (`tdd-red` → `tdd-green` →
   `tdd-refactor`).
