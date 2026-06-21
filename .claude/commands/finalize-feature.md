---
description: Land a reviewed feature — sync CLAUDE.md/README.md to as-built, run final gates, and open a PR for a human to merge (does not merge)
argument-hint: "<feature>"
---

Finalize and land a reviewed feature using the `feature-finalize` skill. The pipeline opens a pull
request and **stops** — a human merges it (per `CLAUDE.md`'s branching policy).

Feature: $ARGUMENTS

1. **Load** the `feature-finalize` skill.
2. **Check preconditions:** you are on `feat/<feature>` (not `main`), and `/review-feature` has returned
   `PASS` (or `CONCERNS` the user accepted). If review hasn't run or returned `FAIL`, stop and say so —
   don't finalize an unreviewed or failing feature.
3. **Sync the docs to as-built:** apply the `review-docs` findings (or check the same surfaces) and make
   the smallest accurate edits to `CLAUDE.md` and `README.md` — project state, command list,
   architecture, supported feature set.
4. **Refresh against `main`, then run the full gate:** `git fetch origin`; if the branch is behind
   `origin/main`, integrate it (rebase/merge), stopping for the user on conflicts. Then run
   **`just verify`** (gofmt + vet + build + unit `-race` + integration — the same gate as CI). Report
   real output; if anything fails, stop and route back to the TDD loop.
5. **Commit, push, and open the PR:** conventional commit (`feat: <feature>` noting FR-IDs + the doc
   sync), `git push -u origin feat/<feature>`, then `gh pr create --fill` targeting `main` with a body
   summarizing what shipped, FR-IDs covered, the review verdict, and the doc updates.
6. **Stop and hand back** the PR URL for the user to merge. **Never merge the PR or push to `main`
   yourself.** Note that after they merge they can clean up with
   `git checkout main && git pull && git branch -d feat/<feature>`.
