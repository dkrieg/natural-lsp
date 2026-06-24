---
description: >-
  Ship a feature end-to-end — branch, plan (you approve), implement via TDD, review and remediate until PASS, then 
  open a PR for you to merge
argument-hint: "<feature> (a docs/plans/features/<feature>/ directory)"
---

Drive a feature from its plan to an **open pull request**, in one flow. **You** (the top-level loop)
orchestrate the phases by running the same skills and agents the individual phase commands use — you do
not write code or tests yourself, and you never merge. There is **one** human checkpoint (plan
approval) plus the merge at the end.

Feature: $ARGUMENTS

If no feature is given above, ask which `docs/plans/features/<feature>/` to ship before proceeding.

## Workflow

Run these phases in order:

1. **Branch.** Per `CLAUDE.md`'s branching policy, ensure you are on `feat/<feature>` (create it from
   `main` if you're on `main`).
2. **Plan** (as `/plan-feature`). Validate `docs/plans/features/<feature>/plan.md` exists — if not, list
   the available feature directories and stop. Spawn `feature-planner` (it applies the
   `feature-planning` skill) to produce `docs/plans/features/<feature>/tasks.md`.
    - **🧍 Checkpoint — stop and present the plan** (task list, current-state findings, open questions)
      and wait for the user to approve or amend. **Do not write any code before approval.**
3. **Implement** (as `/implement-feature`). With the `tdd-implementation` skill, drive every task
   through `tdd-red` → `tdd-green` → `tdd-refactor`, enforcing the gates between phases (fails for the
   right reason → suite green → `gofmt`/`vet`/`-race`/DoD). One task at a time; never start the next on
   a red suite.
4. **Review and remediate until PASS** (as `/review-feature`, `/address-findings`). Run the review
   fan-out via the `review-orchestration` skill. If the verdict is `FAIL` (or `CONCERNS` with blockers),
   run `/address-findings` — regression-first fixes through the TDD loop — then re-review. **Cap this at
   3 rounds.** If it is still not `PASS` after 3 rounds, **stop** and hand the outstanding findings back
   to the user — do not finalize.
5. **Finalize** (as `/finalize-feature`). Once the verdict is `PASS`, run the `feature-finalize` skill:
   refresh against `main`, sync `CLAUDE.md`/`README.md` to as-built, run **`just verify`**, commit, push,
   and open the PR with `gh`.
6. **🛑 Stop at the PR.** Report the PR URL. **Never merge or push to `main`** — the merge is the user's
   call. Note they can clean up after merging with `git checkout main && git pull && git branch -d
   feat/<feature>`.

Throughout, honor the per-phase **stop-and-ask** rules: pause for the user on an ambiguous acceptance
criterion, a design decision not in the plan, or a phase that can't complete after a reasonable retry.
Emit a concise progress note as each phase completes so the run is followable.
