---
name: feature-finalize
description: >-
  How to land a reviewed natural-lsp feature: sync CLAUDE.md and README.md to as-built, run the final
  gates, and open a pull request for a human to merge. Use after /review-feature passes (e.g. via
  /finalize-feature). The pipeline opens the PR and stops — it does not merge.
---

# Finalizing a feature for natural-lsp

This is the **land** phase, after `/review-feature` returns `PASS` (or `CONCERNS` you've accepted). You
bring the docs in line with what shipped, prove the tree is green, and open a PR for a human to merge.
Read `CLAUDE.md`'s "Development workflow" section for the branching/merge policy.

## Preconditions

- You are on the feature's `feat/<feature>` branch (not `main`).
- `/review-feature` has been run and its verdict is `PASS`, or `CONCERNS` the user explicitly accepted.
  If review hasn't run or returned `FAIL`, stop — finalize doesn't paper over an unreviewed/failing
  feature.

## Steps

1. **Refresh against `main`.** `git fetch origin`; if `feat/<feature>` is behind `origin/main`,
   integrate `main` (rebase or merge) so the PR will merge cleanly and the gates run against integrated
   code. On conflicts, stop and hand them to the user — do not resolve them blindly.
2. **Sync the docs to as-built.** Apply the `review-docs` findings if that reviewer ran; otherwise check
   the same surfaces yourself. Bring `CLAUDE.md` and `README.md` in line with what now ships:
   - `CLAUDE.md` **"Project state"** — once real Go source exists, retire the "pre-implementation"
     framing and state the actual shipped capabilities.
   - `CLAUDE.md` **"Commands"** — the `just` recipes, build/test/run commands, and the module/install path match reality.
   - `CLAUDE.md` **"Architecture"** — package boundaries and the Analyzer-seam description match the
     actual layout and any contract change.
   - `CLAUDE.md` design-decision / extension / config notes still match the code.
   - `README.md` — the supported feature set, and the "target vs shipped" framing, are honest for what
     now ships; install/usage examples work.
   Make the **smallest accurate** edits; don't rewrite docs wholesale.
3. **Run the full gate — `just verify`** (gofmt + vet + build + unit `-race` + **integration tests** —
   the exact gate the pre-push hook and CI run, so a local pass means CI should pass). Report real
   output. If anything fails, stop and route back to the TDD loop — do not finalize a red tree.
4. **Commit** the feature branch: stage the code + docs, write a conventional commit
   (`feat: <feature>`, body noting the FR-IDs covered and the doc sync).
5. **Open the PR (do not merge).** `git push -u origin feat/<feature>`, then `gh pr create --fill`
   (target `main`). In the PR body, summarize what shipped, the FR-IDs covered, the `/review-feature`
   verdict, and the doc updates.
6. **Stop and hand back.** Report the PR URL and that it awaits a human merge. **Never merge the PR or
   push to `main` yourself** — landing on `main` is the human's call (per `CLAUDE.md`). Once they merge,
   they can clean up with `git checkout main && git pull && git branch -d feat/<feature>`.

## Boundary

You may edit only `CLAUDE.md` / `README.md` (and other docs) here — no production code or test changes
(those belong in the TDD loop). You open the PR; you do not merge it.
