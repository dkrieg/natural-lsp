---
name: review-orchestration
description: >-
  Decide which reviews a change needs and dispatch them. Use when reviewing a completed feature or diff
  in natural-lsp (e.g. via /review-feature) — selects the applicable review dimensions, runs the
  matching reviewer subagents in parallel, and synthesizes their findings into one verdict. Run by the
  top-level loop, which does the spawning (reviewers must not spawn each other).
---

# Review orchestration for natural-lsp

You are coordinating an **independent** review of a change. You (the top-level agent) select the
dimensions, spawn the reviewer subagents **in parallel**, and synthesize their results. The reviewers
are independent on purpose — they did not write the code, so they verify against the spec and source of
truth rather than trusting intent.

## 1. Scope the change

Determine what changed: `git diff --stat <base>...` (base defaults to `origin/main`), or the explicit
files/feature given. If the change maps to a feature, read `docs/plans/features/<feature>/tasks.md` and
the feature plan so you carry the **acceptance criteria** into the review.

## 2. Select dimensions

Run a reviewer when its trigger matches the change. **Always run `review-acceptance`.**

| Reviewer | Run when the change touches… | Focus |
|---|---|---|
| `review-acceptance` | anything (always) | does it meet the feature plan's acceptance criteria / FR-IDs |
| `review-extraction` | `internal/analysis/natural`, `testdata/` | Natural extraction fidelity (symbols, edges, ranges, modeled gaps, resolution order) |
| `review-concurrency` | `internal/server`, the workspace index, the document/watcher, or any goroutine | races, leaks, context cancellation, bounded concurrency |
| `review-robustness` | the analyzer/IO path or any new input parsing | graceful degradation (FR-43), fuzzing, input bounds |
| `review-seam` | imports across the `internal/analysis` boundary, or `internal/model` types | Analyzer-seam and model purity, package boundaries |
| `review-lsp-protocol` | a `textDocument/*` or `workspace/*` method, capabilities, ranges, or position encoding | LSP 3.x conformance |
| `review-docs` | a feature changes capability, commands, architecture, or the indexed feature set (most completed features) | `CLAUDE.md` / `README.md` match as-built (flags drift; the fix lands in `/finalize-feature`) |

Performance is **not** a routine pass: only escalate (to `go-expert` for a hot-path profile) when the
change is in the indexing path and an NFR is at risk.

State which dimensions you selected and which you skipped, with one line of reasoning each.

## 3. Dispatch in parallel

Spawn the selected reviewer subagents concurrently — **one message, multiple Agent calls**. Give each
reviewer: the list of changed files, the `tasks.md`/feature-plan path (so it knows the criteria), and
the base ref. Reviewers are read-only and return a structured result.

## 4. Synthesize

Each reviewer returns findings — `{severity, location, issue, evidence, recommendation}` — and a
verdict (`PASS` / `CONCERNS` / `FAIL`). Produce one report:

- **Overall verdict:** `FAIL` if any reviewer FAILs or any blocker finding exists; `CONCERNS` if only
  non-blocking findings; `PASS` otherwise.
- **Lead with acceptance** — whether the feature plan's criteria are met.
- **Findings grouped by severity** (blocker / major / minor / nit), **deduped** across reviewers
  (multiple reviewers often flag the same line from different angles — merge them).
- **Coverage note:** any dimension that couldn't complete, and why.

Do **not** fix anything in this flow — report. Code fixes are a separate TDD loop
(`tdd-red` → `tdd-green` → `tdd-refactor`); documentation drift flagged by `review-docs` is fixed in
`/finalize-feature`.

**Review is a loop.** On a `FAIL` (or `CONCERNS` the user wants addressed), route to `/address-findings`
— each finding becomes a regression-first fix — then re-run this review. Repeat until the verdict is
`PASS` before `/finalize-feature` runs. A clean `PASS` is the only thing that unlocks finalize.
