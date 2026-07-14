# Project Assessment — natural-lsp vs. its PRD and the LSP Specification

**Date:** 2026-07-14
**Scope:** Full repository as of `main` @ `3663fd9` (feature 18 merged — all planned features 00–18 shipped).
**Method:** Independent verification, not doc-trust: ran `just verify` (race + integration) and
`scripts/smoke.sh`; drove the built binary over stdio with scripted JSON-RPC against
`docs/plans/features/15-editor-clients/sample-workspace/`, capturing raw wire bytes; four
specialist reviews (LSP protocol conformance, PRD acceptance, robustness, architecture seam);
spec claims verified against LSP 3.17 and the `vscode-languageclient` sources.

---

## Executive verdict

**A genuinely strong, unusually disciplined implementation — roughly 90% of a very good LSP
server — with two confirmed protocol-level defects, one unimplemented P0 requirement, and a set
of performance claims that were never measured.** The functional core (parsing, extraction,
steplib resolution, all navigation/intelligence providers) is real, correct on the wire, and
backed by substantive tests. The gaps concentrate at the edges the test suite doesn't see: raw
wire bytes, the workspace-root handshake, progress UX, and scale.

Remediation is planned as features [19](plans/features/19-protocol-marshaling-unification/plan.md),
[20](plans/features/20-workspace-root-handshake/plan.md),
[21](plans/features/21-async-indexing-and-progress/plan.md),
[22](plans/features/22-performance-and-scale-verification/plan.md), and
[23](plans/features/23-distribution-hardening/plan.md).

## What is verifiably excellent

**Protocol fundamentals most LSP servers get wrong are correct here** (live-probe evidence):

- **Lifecycle is spec-perfect.** Request before `initialize` → error `-32002`; duplicate
  `initialize` rejected; `exit` without `shutdown` → process exit code **1** (exactly what the
  spec mandates); `shutdown`+`exit` → code 0; unknown method → `-32601`; `$/cancelRequest`
  ignored without harm (legal for `$/`-prefixed notifications).
- **Position encoding is done properly** — a rarity. UTF-8 negotiated when the client offers it;
  UTF-16 (real surrogate-pair-aware code-unit counting, `internal/server/position.go`) as the
  default. Model↔protocol range conversion (1-based/inclusive → 0-based/exclusive) is
  centralized and fuzzed.
- **Capabilities exactly match handlers**, enforced by the locked `TestInitialize` allow-list —
  no orphan capabilities, no unadvertised methods. Push diagnostics correctly advertise nothing,
  publish full-replace arrays, clear on close/delete, and thread document versions. Dynamic
  registration of file watching is capability-guarded and degrades gracefully.
- **Every provider works end-to-end on the wire.** Definition/references resolve across files;
  hover returns a correct Markdown card; documentSymbol returns a real hierarchy; signature help
  returns `CALLGREET (#NAME)` with `#NAME A20` and live `activeParameter` tracking; call
  hierarchy round-trips `prepare → incomingCalls (fromRanges) → outgoingCalls` carrying identity
  through `Data` correctly.

**Robustness (FR-43) genuinely holds** — reviewer verdict PASS. Per-file and per-request panic
recovery at the right boundaries with every skip logged; 17+ fuzz targets exercising the
actually-risky entry points (garbage bytes, unterminated SQL, malformed DDM rows, hostile `Data`
payloads); corrupted/truncated caches fall back to full rebuild; parser loops provably terminate
at EOF; zero `regexp` usage (no ReDoS surface).

**The Analyzer seam (NFR-15) is real, not aspirational** — reviewer verdict PASS. The concrete
parser backend is wired exactly once (`cmd/natural-lsp/main.go:57`); nothing in
server/workspace/document imports parser internals; `seam_test.go` AST-parses every production
file and **fails CI if the boundary is ever crossed**. A tree-sitter backend could be swapped in
at one line. (One contained nit: `model.FileAnalysis.AST any` carries a concrete parser type; no
production code outside the parser package reads it and the seam test guards it.)

**Domain rigor is above the norm.** Steplib resolution (non-transitive, declared order, `SYSTEM`
fallback, flat-namespace ambiguity diagnostics) was verified against Software AG documentation;
the modeled-gaps philosophy — dynamic `CALLNAT #VAR` and `&`-placeholder targets are *never*
falsely bound and *never* pollute the diagnostics channel — is applied consistently from
extraction through every provider.

**Engineering process:** 360 test functions (test code ≈ 77% of the repo), race detector always
on, one `just verify` gate shared by pre-push/CI/review, regression-fixture convention,
content-hash cache with format versioning, a real VS Code extension with its own CI job
including electron integration tests, and a curated, source-verified knowledge base + ADR log.

## Confirmed defects (ranked)

### 1. Completion results are corrupted on the wire — protocol violation → feature 19

Captured live: `{"label":"CALLGREET","kind":9,"detail":{}}` — `detail` and `sortText` serialize
as **empty objects** where the spec requires strings. Root cause: `protocol.Optional[T]`
implements only the json/v2 `MarshalJSONTo`, but `server.go:881` marshals completion results
with stdlib `json.Marshal` (unlike signatureHelp and call hierarchy, which use the correct
path). Editor impact: the detail label is garbage, and the `SortText`-based
inline-subroutine-before-external ordering (an explicit feature-16 design decision) **silently
doesn't work**. Tests miss it because `completion_test.go` asserts on Go structs, never on
emitted JSON. The dual-marshaling design (stdlib for some providers, gojson for others) is the
systemic cause; the trap re-fires the first time any other provider gains an `Optional` field.

### 2. The server ignores `rootUri`/`workspaceFolders` entirely → feature 20

Neither appears anywhere in `internal/server` or `cmd`; the workspace root comes solely from
`os.Getwd()` + sentinel walk-up (`cmd/natural-lsp/main.go:51-55`). Demonstrated live: launched
with the wrong cwd, the server initializes fine but **every index-backed feature silently
returns null/empty** — no error, no diagnostic (also cuts against NFR-14's "make limits
legible"). VS Code works only because `vscode-languageclient` defaults the child-process cwd to
the first workspace folder (verified in `_getServerWorkingDir`, vscode-languageserver-node). The
README's own Neovim config sets `root_markers`/`root_dir` — which only affect the `rootUri` the
server discards — so a Neovim user who starts vim anywhere but the workspace root gets a dead
server. Multi-root and single-file scenarios are similarly broken. Biggest practical threat to
NFR-11 ("consumable by any compliant client").

### 3. FR-32 (P0) — indexing progress reporting — was never implemented → feature 21

`internal/server/progress.go` is a 6-line TODO stub; no `window/workDoneProgress/create` or
`$/progress` is sent anywhere; `workspace.Build` is invoked with `onProgress: nil`
(`server.go:387`). The workspace half (the `Build` progress callback, task 05-S01) shipped
*with tests* — only the LSP wiring half (task 05-I02) was dropped, and feature 05 was still
marked shipped. This is the one place the process failed: a P0 acceptance criterion was silently
lost between planning and "done."

### 4. NFR-5 — the cold index build blocks everything → feature 21

`workspace.Build` runs synchronously inside the `initialized` handler on the single-threaded
dispatch loop (`server.go:386-394`). Invisible on a 3-file sample; on the PRD's target of tens
of thousands of objects, the editor gets no responses (and, per #3, no progress) for the entire
cold build.

### 5. Performance/scale claims are unmeasured → feature 22

NFR-4 (P0, enterprise scale without memory exhaustion), NFR-2 (sub-second warm start), NFR-1,
NFR-3: **zero benchmarks or scale tests exist**. Concrete warning signs: `LookupByName` is
O(all files) per query, and `workspace/symbol` re-reads every indexed file from disk on each
query while holding the index read lock. Fine at 3 files; unknown at 30,000.

## Secondary findings

- **Distribution (NFR-12) is broken as documented → feature 23:** README says
  `go install github.com/dkrieg/natural-lsp/cmd/natural-lsp@latest` but `go.mod` declares module
  `natural-lsp` — the documented install cannot work until the module path is reconciled. No
  package-manager channel, no Marketplace publish (both documented as future work). FR-45 is
  honestly a "documented LSP4IJ path," not first-party JetBrains support.
- **`scripts/smoke.sh` no-arg mode is buggy → feature 23:** `[ -x natural-lsp ]` checks the
  binary in the *current directory*, but execution then goes through PATH — a misleading
  "--version exited non-zero" failure when the binary sits in cwd but isn't installed. With an
  explicit path, all smoke checks pass.
- **Doc drift (fixed 2026-07-14 alongside this report):** all 21 feature `plan.md` files still
  read `Status: Planned` despite being merged. Statuses now record shipped + PR. Acceptance
  checkboxes inside shipped plans remain unticked (historical artifacts). Stale stubs
  `internal/server/handlers.go` and `progress.go` and a stale `test/panic` comment remain in
  code (cleanup rides features 21/23).
- **Deferred-by-design, honestly tracked:** FR-50 folding ranges and FR-51 inlay hints (both
  P2, never planned), SQL-sourced DDM/host-var cross-file resolution, `$/cancelRequest` +
  concurrent dispatch. The space-character completion/signature trigger is spec-legal but
  aggressive; revisit with real users.
- **Success metrics M-1–M-10** (adoption, corpus coverage) are unmeasurable today — there is no
  acceptance corpus, which the PRD itself lists as an open question.

## Scorecard

| Group | Verdict |
|---|---|
| FR-1–FR-31, FR-33–FR-49 (parsing, extraction, resolution, providers, lifecycle, clients) | **Met**, with substantive tests — spot-audited FR-24/25/26/35/36/41/42/47/48/49 each traced to passing, meaningful assertions |
| FR-32 (P0, progress) | **Not met** — nothing satisfies it |
| FR-47 on the wire | **Impaired** by the completion marshaling bug |
| FR-50/FR-51 (P2) | Not attempted (known deferral) |
| NFR-6/7/8/9/13/15 (no-silent-loss, resolution correctness, cache freshness, fixtures, setup, seam) | **Met** with evidence |
| NFR-11 (LSP conformance, any client) | **Mostly met**; undermined by defects 1–2 |
| NFR-5 (non-blocking indexing) | **Not met** |
| NFR-1/2/3/4 (performance/scale) | **Unproven** — no measurement exists |
| NFR-10/12 (distribution) | Partial — release pipeline exists; documented install path broken, no package manager |

## Run evidence

- `just verify`: PASS — gofmt, vet, build, `go test -race ./...`, integration suite, all green.
- `scripts/smoke.sh ./natural-lsp`: PASS — `--version`, EOF exit, full
  `initialize → initialized → shutdown → exit` round-trip. (No-arg invocation fails; see
  secondary findings.)
- Live stdio probes: capability set matches the locked allow-list byte-for-byte;
  `positionEncoding` `utf-8` when offered, `utf-16` otherwise; all provider round-trips above;
  malformed params and unknown URIs answered gracefully (null result, no crash).

## Recommended order of work

1. **Feature 19** — fix completion marshaling; unify all providers on the json/v2 path; add
   wire-bytes regression tests. Small, high-impact, restores protocol conformance.
2. **Feature 20** — honor `workspaceFolders`/`rootUri` with cwd walk-up as fallback. Unlocks
   every non-VS-Code client, multi-root, and single-file scenarios.
3. **Feature 21** — background index build + wire the already-tested `Build(onProgress)` to
   `$/progress` work-done progress (FR-32 + NFR-5 together).
4. **Feature 22** — synthetic 10k–50k-object benchmark for cold/warm index, memory, and
   `workspace/symbol` latency; fix the per-query disk-read hot spot it will expose.
5. **Feature 23** — reconcile the module path so `go install` works; fix `smoke.sh` no-arg
   resolution; then the remaining doc/stub cleanup.
