---
name: feature-planning
description: >-
  How to decompose a natural-lsp feature plan into an ordered, TDD-structured task list. Use when
  turning a feature plan under docs/plans/features/ (and its PRD FR-IDs) into implementable tasks —
  each a red-green-refactor slice with explicit expected results and a definition of done. Shared by
  the feature-planner agent and software-engineer-expert.
---

# Feature planning for natural-lsp

Turn a specification into a sequence of small, independently-verifiable tasks that drive test-first
implementation. The output is a plan that *other* actors (the TDD agents) execute — optimize it for
that, not for your own understanding.

## Inputs (authoritative, in order)

- The **feature plan** `docs/plans/features/<feature>/plan.md` — its acceptance criteria are the spec.
  (Each feature is a directory holding `plan.md` and the `tasks.md` you produce.)
- The **PRD** `docs/plans/natural-lsp-prd.md` — the FR-/NFR-IDs the plan references.
- `CLAUDE.md` and `README.md` — architecture/layout constraints (the **Analyzer seam**, the
  `internal/{config,server,document,workspace,model,analysis}` layout, steplib resolution, file
  extensions, graceful degradation).
- The knowledge bases (`.claude/knowledge/`) for verified facts and ADRs, and the `go-development`
  skill for how we build and test here.
- **The existing codebase itself** — the current `internal/...` source, its tests, and `testdata/`.
  This is **ground truth**: when the code and the `README`/`CLAUDE.md` description diverge, the code
  wins, and the divergence is itself something to flag. Never plan as if the repo were greenfield once
  code exists.

## Decomposition method

1. **Restate the goal** and enumerate **every acceptance criterion** as a checklist, each tagged with
   its FR/NFR-ID. If a criterion is ambiguous, list it as an open question — don't invent intent.
2. **Establish current state.** Before decomposing, survey the code the feature will touch — Grep/read
   the relevant packages and inventory what already exists: the `Analyzer`/`internal/model` surface,
   existing extraction helpers and regex patterns, the index/resolution API, existing tests and
   `testdata/` fixtures, and the conventions actually in use. Plan against the code as it *is*. (At the
   pre-implementation stage there may be little to find — say so explicitly rather than skipping the
   step.)
3. **Reconcile spec with reality.** Classify each acceptance criterion against what exists:
   - **already satisfied** → no task; record it so the plan shows why it's skipped;
   - **extend existing code** → task that names the function/type/pattern to *reuse or extend* rather
     than duplicate;
   - **new** → task built from scratch;
   - **changes a shared contract** (an `internal/model` type, the `Analyzer` interface, the index API)
     → the change task **plus an explicit migration task for every existing consumer**. The model and
     seam are consumed by the workspace index, the server, and the external `lsp-graph` builder, so a
     contract change ripples — surface that ripple as tasks, don't bury it.
4. **Map the touch-points** across `internal/{server,document,workspace,model,analysis/natural}`,
   informed by step 2, and note which **side of the Analyzer seam** each change lives on (LSP-facing vs
   extraction backend).
5. **Slice into tasks.** Each task is the smallest behavior that can be driven red → green → refactor:
   - one observable behavior / acceptance criterion (or a tight cluster);
   - the **`testdata/` fixture(s)** it needs (minimal, sanitized `.NSx`) — reuse an existing fixture
     where one already covers the input;
   - the **expected result** — exact extraction (symbols, edges, ranges) or the LSP response;
   - **what it reuses or migrates** — name existing code it extends, and any consumer it must update;
   - **modeled gaps covered explicitly** (`CALLS_DYNAMIC` for unresolved refs, diagnostics for
     unrecognized statement-like lines) — never as afterthoughts.
6. **Order by dependency, adjusted for what already exists:** model/types → extraction →
   index/resolution → server/LSP handler → integration. **Skip foundations that already exist**; where
   existing code diverges from what the feature needs, **sequence the refactor/migration first** so
   downstream tasks build on the adjusted contract. Foundations land before the features that consume
   them.
7. **Definition of Done per task:** unit tests (table-driven + fixture; `-race` if it touches
   concurrency; a fuzz target if it widens the parser); existing tests for any migrated consumer still
   green; `go vet`/`gofmt` clean; Analyzer-seam and `internal/model` purity preserved; graceful
   degradation held; deterministic (sorted) output where it emits collections.
8. **Flag the cross-cutting reviews** the feature will need (handed to `review-orchestration`):
   concurrency if it touches the indexer/watcher, protocol conformance if it adds an LSP method,
   robustness if it parses new input, performance if it's in the indexing hot path. Add `review-seam`
   whenever a shared contract changed, and `review-docs` when the feature changes capability, commands,
   or architecture (so the `CLAUDE.md`/`README.md` sync at `/finalize-feature` is anticipated).
9. **Surface decisions** for the user rather than guessing.

## Output

Write `docs/plans/features/<feature>/tasks.md`:

- **Header:** feature name, link to the source plan, FR/NFR-IDs covered.
- **"Current-state findings & impact"** section: what already exists in the touched packages, which
  criteria are already satisfied (and skipped), what existing code is reused/extended, what shared
  contracts change and which consumers must migrate, and any code/README divergence found. This is how
  the plan shows it pivoted to the codebase rather than the spec alone.
- **Ordered task list.** Each task: ID, title, the behavior, fixtures needed, expected result, what it
  reuses/migrates, a DoD checklist, the TDD agents to run (`tdd-red` → `tdd-green` → `tdd-refactor`),
  and dependencies.
- **"Reviews required"** section listing the dimensions to run via `/review-feature`.
- **"Open questions"** section for anything unresolved.

Keep tasks small enough that one red → green → refactor loop completes each. Prefer many thin slices
over few fat ones — a task that needs two fixtures or asserts two unrelated behaviors should split.
