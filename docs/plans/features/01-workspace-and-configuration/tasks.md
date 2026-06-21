# Tasks: Workspace & configuration

**Source plan:** [`plan.md`](./plan.md)
**PRD requirements covered:** FR-1, FR-2, FR-3, FR-4 (config portion), FR-6; CR-1, CR-2, CR-3, CR-4, CR-5, CR-6
**Phase:** P0 (root discovery, core config, defaults) · P1 (library map, dynamic-call config)
**Primary package:** `internal/config` · **Touches:** `cmd/natural-lsp`, `internal/server` (root logging only)

---

## Current-state findings & impact

The repository is at the pre-implementation stage for this feature: every package under
`internal/...` is a documented stub with a package doc and a `TODO`, no executable code, and no tests.
Specifically:

- **`internal/config/config.go`** — package doc only. The `TODO` already names the intended surface:
  `Config` struct (extensions / excludes / max-file-size, cache path, analysis options, library map),
  plus `Load`, `Defaults`, `Validate`, `FindRoot`. **All new.** This feature builds this package.
- **`internal/model/model.go`** — `EdgeKind` constants exist; `FileAnalysis` is an empty `TODO`
  struct. **Not touched by this feature** — config does not depend on the model, and the library map
  the resolver consumes is a `config`-owned type (see ADR-003: resolution is a separate step in
  `internal/workspace`, which is plan 06, not here).
- **`internal/analysis/analyzer.go`** — `Analyzer` interface stub. **Not touched.** The Analyzer seam
  is unaffected: config sits entirely on the LSP-facing / process-bootstrap side and is consumed by
  `cmd/main.go`, `internal/server`, and (later) `internal/workspace`. No code in this feature crosses
  the seam.
- **`internal/workspace/{index,cache,resolution}.go`** — stubs. The resolver (`resolution.go`) is the
  declared **consumer** of the library map produced here, but its *algorithm* is explicitly out of
  scope for this plan (plan 06). This feature only produces the parsed, ordered library map and exposes
  it; it does not walk the steplib chain.
- **`internal/server/server.go`, `cmd/natural-lsp/main.go`** — stubs. `main.go` already has a `--version`
  path and a `--stdio` `TODO` that says "construct config, document store, workspace index, and
  analyzer." This feature supplies the `config` construction step and the root-resolved log line.
- **`testdata/`** — only `testdata/workspace/.gitkeep`. No fixtures yet. Config fixtures are TOML files
  and small directory trees, **not** `.NSx` source, so they live under `testdata/config/...` rather
  than mixing with extraction fixtures. (The `.NSx` regression-fixture convention applies to the
  analyzer, not to config parsing.)
- **No TOML dependency.** `go.mod` has zero non-stdlib requires. CR-1 / the README schema commit us to
  TOML (`.natural-lsp.toml`), which stdlib cannot parse. Adding a TOML decoder is a prerequisite (see
  Open questions — needs approval before T1, as it is the project's first third-party dependency).

**README is the config-schema source of truth.** README lines 228–286 document the exact TOML shape —
section names (`[workspace]`, `[cache]`, `[analysis]`, `[resolution]`), keys
(`extensions`, `exclude`, `max_file_size`, `path`, `flag_dynamic_calls`, `dynamic_call_min_length`,
`[[resolution.library]]` with `name` / `path` / `steplibs`), and defaults (`max_file_size = 5_000_000`,
cache `path = ".natural-lsp-cache"`, `flag_dynamic_calls = true`, `dynamic_call_min_length = 6`, and the
ten-extension default set). Tasks assert against these exact names/defaults; any deviation is a
README-vs-code divergence to flag and reconcile (the `review-docs` reviewer covers this).

### Criterion reconciliation

| Story / criterion | Disposition | Task |
|---|---|---|
| S1 walk-up to sentinel → root | new | T2 |
| S1 fallback root when no sentinel | new | T2 |
| S1 resolved root reported in logs | new | T9 |
| S2 empty sentinel → defaults applied | new | T3, T4 |
| S2 every value has documented default + discoverable | new (defaults in T3; sample-config gen in T8) | T3, T8 |
| S3 configurable indexed extension set | new | T4 |
| S3 directory exclusions honored | new (predicate exposed by config; *enforcement* of "never read" is the indexer's job, plan unrelated) | T5 |
| S3 max file size enforced + skip reported | **partial here** — config supplies the limit + a skip-reporting hook; the indexer (FR-2/FR-36, separate feature) does the skipping. See note. | T5 |
| S3 cache location configurable | new | T4 |
| S4 map dirs → libraries | new (P1) | T6 |
| S4 per-library ordered steplibs | new (P1) | T6 |
| S4 library map exposed to resolver in declared order | new (P1) — expose only; consumption is plan 06 | T6 |
| S4 no library map → loading still succeeds (flat namespace is plan 06) | new (P1) | T6 |
| S5 dynamic-call treat-as-dependency toggle (default on) | new (P1) | T7 |
| S5 dynamic-call heuristic thresholds configurable + defaulted | new (P1) | T7 |
| S6 invalid config → clear actionable message | new | T3 (validation scaffold), T4–T7 (per-field) |
| S6 invalid value → fall back to default when safe | new | T3, T4–T7 |
| S6 case-insensitive Natural identifiers (library/module names) | new (P1) | T6 |

**Scope note on S3 "skip is reported":** enforcing max-file-size and directory exclusions at index time
belongs to the indexer (FR-2/FR-36), which is a different feature. This feature's responsibility is to
(a) parse and default the limit and exclude list, and (b) expose a clean predicate/limit the indexer
will call, plus the *reporting channel contract* (skips go to logs/diagnostics, never silent — NFR-6).
T5 builds and tests that predicate/limit surface; it does **not** crawl a real workspace tree. Flagged
so the downstream indexer feature wires it.

---

## Ordered task list

Dependency order: dependency approval → root discovery → defaults → core config fields → library map →
analysis options → sample-config generation → bootstrap wiring + root logging.

Each task runs `tdd-red` → `tdd-green` → `tdd-refactor` unless noted. All tests live in
`internal/config/` (package `config` / `config_test.go`), table-driven, using `fstest.MapFS` or
`t.TempDir()` for filesystem inputs to keep them fast and hermetic.

---

### T0 — Approve & add the TOML decoder dependency (decision gate)

**Behavior:** Not a TDD slice — a prerequisite decision. CR-1 + the README commit the project to
`.natural-lsp.toml`. Select and add a TOML decoder (candidate: `github.com/BurntSushi/toml`, the de
facto Go standard, or `github.com/pelletier/go-toml/v2`). This is the project's first third-party
dependency, so it needs sign-off and probably an ADR entry.

**DoD:**
- [ ] Decoder chosen and recorded as an ADR in `architecture-decisions.md` (rationale, alternative).
- [ ] `go.mod` / `go.sum` updated; `go mod tidy` clean; `just verify` still builds.
- [ ] Dependency confined to `internal/config` (never imported by `internal/model`/`analysis` — seam).

**Reuses/migrates:** none. **Depends on:** user approval (Open question).
**Agents:** none (mechanical + ADR).

---

### T1 — `Config` type + `Defaults()` returns the documented default config

**Behavior:** Define the `Config` struct mirroring the README schema (workspace extensions/exclude/
max_file_size, cache path, analysis flag_dynamic_calls/dynamic_call_min_length, resolution library
list). `Defaults()` returns a `Config` populated with every documented default. This is the FR-6 / CR-2
"every value has a documented default" foundation, asserted independently of any file parsing.

**Fixtures:** none (pure constructor).

**Expected result (test asserts):** `Defaults()` returns exactly:
- `Extensions` = the ten-element default set `[".NSP",".NSN",".NSS",".NSC",".NSM",".NSL",".NSG",".NSA",".NSH",".NSD"]` (order as documented).
- `Exclude` = `["archive","backup",".git"]`.
- `MaxFileSize` = `5_000_000`.
- `Cache.Path` = `".natural-lsp-cache"`.
- `Analysis.FlagDynamicCalls` = `true`.
- `Analysis.DynamicCallMinLength` = `6`.
- `Resolution.Libraries` = empty (non-nil) slice.

**Modeled-gap coverage:** n/a (no parsing yet).

**DoD:**
- [ ] Struct fields documented; defaults match README exactly (or divergence flagged in PR).
- [ ] Table/equality test on `Defaults()`; `go vet`/`gofmt` clean.

**Reuses/migrates:** fills the `Config` `TODO` in `config.go`. **Depends on:** T0.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T2 — `FindRoot`: walk up to the sentinel; documented fallback when absent (FR-1)

**Behavior:** `FindRoot(start string) (root string, found bool)` walks up parent directories from
`start` looking for `.natural-lsp.toml`; the first directory containing it is the root (nearest wins).
When none is found up to the filesystem root, return the documented fallback (`found=false`, root =
the editor-provided workspace folder / `start`'s directory per the documented rule) without error.

**Fixtures:** temp dir trees via `t.TempDir()` (or `fstest.MapFS` if `FindRoot` is refactored to take
an `fs.FS`): (a) sentinel two levels up from an opened file; (b) sentinel in the same dir; (c) **two**
sentinels on the path (nested) → asserts nearest wins; (d) no sentinel anywhere.

**Expected result (test asserts):**
- (a) returns the grandparent dir, `found=true`.
- (b) returns the file's own dir, `found=true`.
- (c) returns the **nearest** (deepest) sentinel dir, `found=true`.
- (d) returns the documented fallback root, `found=false`, no error.

**Modeled-gap coverage:** missing sentinel is a *documented fallback*, not an error (S1 criterion 2) —
asserted as `found=false` + usable root, never a failure.

**DoD:**
- [ ] All four cases table-driven; symlink/permission edge handled (don't panic on unreadable parent).
- [ ] Deterministic; no reliance on CWD; absolute paths returned.
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** fills the `FindRoot` `TODO`. **Depends on:** T0.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T3 — `Load` over an empty/minimal sentinel applies defaults; `Validate` scaffold (FR-6, CR-2, CR-6)

**Behavior:** `Load(path string) (Config, []Problem, error)` reads `.natural-lsp.toml`, decodes it onto
a `Defaults()` base (so unset keys keep their defaults), then runs `Validate`, which collects
field-level problems. `Validate` is the CR-6 scaffold: it returns a slice of actionable `Problem`
values (offending setting + message + the default it fell back to) rather than failing. A genuinely
unreadable/unparseable-as-TOML file returns a single clear error (the only hard-fail path; a malformed
*value* must not be).

**Fixtures (`testdata/config/`):** (a) `empty.toml` (zero bytes); (b) `minimal.toml` (only
`[workspace]\nextensions = [".NSP"]`); (c) `garbage.toml` (not valid TOML at all).

**Expected result (test asserts):**
- (a) empty file → `Load` returns `Defaults()` verbatim, no problems, no error.
- (b) minimal file → `Extensions == [".NSP"]`, every *other* field equals its default; no problems.
- (c) syntactically invalid TOML → non-nil `error` with a message naming the file; `Config` is still
  usable (returns `Defaults()`) so the server can start (CR-6 fail-safe).

**Modeled-gap coverage:** establishes the CR-6 contract — invalid input yields actionable
`Problem`/`error`, never a silent or crashing failure. Subsequent tasks add per-field problems to this
channel rather than panicking.

**DoD:**
- [ ] `Problem` type defined (setting key, human message, defaulted value); deterministic ordering of
      the returned slice.
- [ ] Decode-onto-defaults semantics tested (unset key keeps default).
- [ ] Hard error only for unparseable TOML; everything else degrades to defaults + `Problem`.
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** consumes T1 `Defaults()`; fills `Load`/`Validate` `TODO`s. **Depends on:** T1.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T4 — Core `[workspace]` + `[cache]` fields parse & validate (FR-2, FR-3, CR-3)

**Behavior:** Parse and validate `extensions`, `max_file_size`, and `cache.path`. Validation: extensions
normalized (leading-dot enforced, upper-cased to match Natural case-insensitivity, deduped); a
non-positive or non-numeric `max_file_size` falls back to the default with a `Problem`; an empty
`cache.path` falls back to the default with a `Problem`. (`exclude` parsing here too, but its
*predicate* behavior is T5.)

**Fixtures (`testdata/config/`):** (a) `extensions-custom.toml` with a custom, mixed-case, dot-omitted
extension list (e.g. `extensions = ["nsp", ".NSN", "NSN"]`); (b) `bad-maxsize.toml` with
`max_file_size = -1`; (c) `custom-cache.toml` with `path = "build/idx"`.

**Expected result (test asserts):**
- (a) → `Extensions` normalized to `[".NSP",".NSN"]` (dot added, upper-cased, deduped, stable order).
- (b) → `MaxFileSize` falls back to `5_000_000`, with a `Problem` naming `max_file_size`.
- (c) → `Cache.Path == "build/idx"`, no problem.

**Modeled-gap coverage:** invalid `max_file_size` → defaulted + reported `Problem` (CR-6), not a fail.

**DoD:**
- [ ] Case/dot/dedup normalization table-driven and matches the case-insensitivity rule (CLAUDE.md).
- [ ] Invalid numeric/empty values produce a `Problem` and a safe default — no error returned.
- [ ] Deterministic normalized output ordering.
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** extends T3 `Load`/`Validate`. **Depends on:** T3.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T5 — Exclude predicate + max-file-size limit surface for the indexer (FR-3, NFR-6)

**Behavior:** Expose the config's indexing decisions as a clean, testable surface the future indexer
will call: `(*Config).IsExcluded(relPath string) bool` (true for any path under a configured exclude
directory) and the `MaxFileSize` limit with a documented "report, don't drop" contract — i.e. the
config provides a `SkipReason` helper/enum so the indexer reports skips to logs/diagnostics rather than
silently dropping (NFR-6). This task builds and tests the predicate/limit only; it does **not** crawl a
real tree (that is the indexer feature).

**Fixtures:** none (pure predicate over an in-memory `Config` from T1/T4 plus path strings).

**Expected result (test asserts):**
- With `Exclude = ["archive",".git"]`: `IsExcluded("archive/X.NSP")` and `IsExcluded("a/.git/Y")`
  are true; `IsExcluded("src/MYAPP/X.NSP")` is false. Matching is case-insensitive on directory names
  and segment-anchored (not substring — `"archived/"` does not match `"archive"`).
- A `SkipReason`-style result distinguishes "too large" from "excluded dir" so the indexer can report
  the right message; both are non-silent by contract (documented, asserted via the enum/string).

**Modeled-gap coverage:** **this is the NFR-6 no-silent-loss seam for skipped files** — the config
hands the indexer a typed skip reason so a skipped file is always reportable, never dropped silently.

**DoD:**
- [ ] Segment-anchored, case-insensitive directory matching table-driven (incl. the `archived` vs
      `archive` false-match guard).
- [ ] Skip-reason surface documented as "report via logs/diagnostics, never drop" and referenced by
      the downstream indexer task (cross-link in this file).
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** extends T4 (uses parsed `Exclude`/`MaxFileSize`). **Depends on:** T4.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T6 — `[[resolution.library]]` map parses in declared order, case-insensitive (FR-4, CR-5)

**Behavior:** Parse the library array: each `[[resolution.library]]` yields a `Library{Name, Path,
Steplibs []string}`, **preserving declared order** both for the library list and each `steplibs` list
(order is what disambiguates — ADR-004). Library and steplib names normalize case (Natural
case-insensitive — S6 / CR-6 final criterion) while preserving the canonical form for display. Expose
the parsed map via a `(*Config).Libraries()` accessor (or field) for the resolver. With no
`[[resolution.library]]` blocks, loading succeeds and the library list is empty (flat-namespace
*behavior* is plan 06 — here we only assert "load succeeds, list empty").

**Fixtures (`testdata/config/`):** (a) `libraries.toml` — the README two-library example
(`MYAPP` with `steplibs = ["COMMON","SYSTEM"]`, then `COMMON`); (b) `library-casevar.toml` — a library
whose name/steplibs use mixed case (`name = "MyApp"`, `steplibs = ["common","System"]`);
(c) reuse `empty.toml` from T3 (no libraries).

**Expected result (test asserts):**
- (a) → two libraries in declared order `[MYAPP, COMMON]`; `MYAPP.Steplibs == ["COMMON","SYSTEM"]`
  in that order; paths preserved.
- (b) → name/steplibs normalized for matching (e.g. upper-cased canonical key) so `MyApp`/`MYAPP`
  compare equal, while the original spelling is still retrievable for display.
- (c) empty → `Libraries()` returns empty (non-nil) slice, no error, no problem.

**Modeled-gap coverage:** no library map is a *valid* state (S4 criterion 4 / FR-5 setup), not an
error. A malformed library entry (e.g. missing `name`) yields a `Problem` for that entry and the entry
is dropped — never aborts loading (CR-6).

**DoD:**
- [ ] Declared order preserved for both library list and per-library steplibs (asserted by index).
- [ ] Case-insensitive identifier matching with canonical display form, table-driven.
- [ ] Missing-`name`/empty entry → `Problem` + dropped, not an error.
- [ ] `Libraries()` accessor documented as "exposed to resolver in declared order; consumed in plan 06."
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** extends T3 `Load`. The `Library` type is **config-owned** (not `internal/model`),
consistent with ADR-003 (resolution is a workspace-package step). **Depends on:** T3.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T7 — `[analysis]` options parse & validate (CR-4)

**Behavior:** Parse `flag_dynamic_calls` (bool, default true) and `dynamic_call_min_length` (int,
default 6). Validation: a non-positive `dynamic_call_min_length` falls back to the default with a
`Problem`. These values are *carried* by config for the call-resolution feature (plan 06); this task
only parses/defaults/validates and exposes them.

**Fixtures (`testdata/config/`):** (a) `analysis-off.toml` with `flag_dynamic_calls = false` and
`dynamic_call_min_length = 8`; (b) `analysis-bad.toml` with `dynamic_call_min_length = 0`.

**Expected result (test asserts):**
- (a) → `FlagDynamicCalls == false`, `DynamicCallMinLength == 8`, no problem.
- (b) → `DynamicCallMinLength` falls back to `6`, with a `Problem` naming `dynamic_call_min_length`;
  `FlagDynamicCalls` stays default `true`.

**Modeled-gap coverage:** the toggle is the CR-4 "dependency vs error" control whose default treats
dynamic calls as dependencies (consistent with `CALLS_DYNAMIC` being a modeled outcome, ADR-001). Bad
threshold → defaulted + reported, never a fail.

**DoD:**
- [ ] Bool + int parsing/defaulting table-driven; invalid threshold → `Problem` + default.
- [ ] Default `flag_dynamic_calls = true` asserted (dynamic calls = dependency, not error).
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** extends T3 `Load`/`Validate`. **Depends on:** T3.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T8 — Sample-config generation makes defaults discoverable (CR-2, S2 criterion 2)

**Behavior:** Provide a way to emit a fully-commented sample `.natural-lsp.toml` populated from
`Defaults()` (a `Sample() string` / `WriteSample(io.Writer)` function), so the defaults are
discoverable as the spec requires. (Whether this is also surfaced as a CLI subcommand is an Open
question; the generator itself is in scope and testable now.)

**Fixtures:** a committed golden `testdata/config/sample.golden.toml` (the determinism contract from
the testing-strategy KB applies — stable ordering, regenerated behind `-update`).

**Expected result (test asserts):**
- `Sample()` output, when re-parsed by `Load`, yields a `Config` equal to `Defaults()` (round-trip).
- `Sample()` output byte-matches `sample.golden.toml` (golden, `-update`-regenerable).

**Modeled-gap coverage:** n/a.

**DoD:**
- [ ] Round-trip test (sample → `Load` → `Defaults()` equality) green.
- [ ] Golden file committed; deterministic (sorted/pinned) output.
- [ ] Every documented key present with its default and an explanatory comment.
- [ ] `go vet`/`gofmt` clean.

**Reuses/migrates:** consumes T1 `Defaults()` and the T3/T4/T6/T7 schema. **Depends on:** T1, T4, T6, T7.
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### T9 — Bootstrap wiring: resolve root, load config, log the resolved root (FR-1 S1 criterion 3)

**Behavior:** Wire config into the process startup so the resolved root and effective config are
established before serving, and the resolved root + any `Problem`s are written to the server log
(observability — S1 criterion 3, CR-6 actionable messages reach the user). `cmd/natural-lsp/main.go`'s
`--stdio` path (or an `internal/server` constructor it calls) invokes `config.FindRoot` then
`config.Load`, logs `resolved workspace root: <path> (sentinel found: true/false)` and one log line per
`Problem`. This stays on the LSP-facing side of the seam (no analyzer/model import).

**Fixtures:** a `t.TempDir()` workspace with a `.natural-lsp.toml`; tested via the
constructor/wiring function, not a full LSP session (keep it a unit/seam test — assert on an injected
logger, not stdout).

**Expected result (test asserts):**
- Given a temp workspace with a sentinel, the bootstrap function returns the resolved root and a
  `Config`, and the injected logger received the `resolved workspace root: <dir> (sentinel found:
  true)` line.
- Given a temp dir with **no** sentinel, it logs `sentinel found: false` and still returns a usable
  default `Config` (no crash, server can proceed).
- Given a config with a `Problem` (e.g. bad `max_file_size`), each problem is logged as an actionable
  line and startup proceeds.

**Modeled-gap coverage:** the no-sentinel and bad-config paths are exercised as *observable, non-fatal*
(FR-43 graceful-degradation alignment, CR-6).

**DoD:**
- [ ] Logger injected (interface or `*slog.Logger` with a capture handler) — no reliance on real stdio.
- [ ] Root + problems logged; bad config does not abort startup.
- [ ] No import of `internal/analysis`/`internal/model` from the config path (seam preserved).
- [ ] `go vet`/`gofmt` clean; existing `--version` path unaffected.

**Reuses/migrates:** consumes T2 `FindRoot` + T3 `Load`; fills the `--stdio` `TODO` in `main.go`
(config construction step only — document store/index/analyzer remain `TODO` for their own features).
**Depends on:** T2, T3 (and benefits from T4–T7 being present so problems are realistic).
**Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`.

---

## Reviews required (`/review-feature`)

- **`review-acceptance`** — every Story 1–6 criterion maps to a task (or the documented scope note for
  the indexer-owned half of S3); confirm coverage.
- **`review-robustness`** — config parses untrusted/hand-edited TOML; CR-6 fail-safe (bad value →
  default + actionable `Problem`, never crash/abort) is the core robustness property. Consider a small
  fuzz target over `Load`/decode asserting "never panics on any TOML-ish input."
- **`review-seam`** — confirm the TOML decoder dependency and the config types stay inside
  `internal/config` and never leak into `internal/model`/`internal/analysis`; the `Library` type is
  config-owned, not a model type (ADR-002, ADR-003).
- **`review-docs`** — the feature defines the config schema/defaults the README already documents;
  verify code matches README lines 228–286 (section names, keys, defaults) and that the "Project state"
  note advances from pre-implementation. Confirm the sample-config (T8) and the documented defaults
  agree.
- *(No `review-concurrency` — this feature adds no goroutines/shared state; the index concurrency model,
  ADR-012, belongs to the indexer feature.)*
- *(No `review-lsp-protocol` — no LSP method added here; T9 only logs.)*

## Open questions

1. **TOML decoder dependency (T0)** — approve adding the project's first third-party dependency, and
   which (`BurntSushi/toml` vs `pelletier/go-toml/v2`)? Needs an ADR. Blocks all tasks.
2. **Nearest-sentinel rule (plan Open question)** — plan asks to confirm "nearest wins" when multiple
   sentinels lie on the path. T2 assumes nearest (deepest) wins — confirm.
3. **Sample-config delivery (T8)** — is a CLI subcommand (e.g. `natural-lsp --init` / `config sample`)
   required for the first release, or is the in-code generator + docs sufficient? Plan/PRD do not
   mandate a CLI surface; the generator is built regardless, the CLI wiring is the open part.
4. **Env/CLI config overrides (plan Open question)** — plan asks whether environment/CLI overrides of
   config are required for the first release. No task assumes them; if required, add a follow-up task
   layering overrides on top of `Load`.
5. **Fallback root definition (S1 criterion 2)** — the plan says "editor-provided workspace folder."
   T2 currently falls back to the opened file's directory when no editor workspace folder is available;
   confirm the precedence (editor `rootUri`/`workspaceFolders` from `initialize` vs. opened-file dir),
   since the `initialize` params are an LSP-layer input that T9 would have to thread into `FindRoot`.

---

## Remediation tasks (from review round 1)

### R1 — Fuzz target over `Load` (review-robustness major)

**Finding:** `tasks.md` "Reviews required" explicitly required a fuzz target over `Load` asserting
"never panics on any TOML-ish input." No `FuzzLoad` exists. This is the executable proof of CR-6.

**RED:** Write `FuzzLoad(f *testing.F)` in `internal/config/` seeded from the existing
`testdata/config/*.toml` corpus, asserting `Load` never panics and always returns a usable `Config`.
Confirm the fuzz function compiles and `go test -run=^$ -fuzz=FuzzLoad -fuzztime=5s ./internal/config/`
runs without crashing (5 seconds is enough for a smoke run; commit any minimized failures under
`testdata/fuzz/FuzzLoad/`).

**Expected result:** `FuzzLoad` exists, compiles, runs 5 s without crash, never panics.

**DoD:**
- [ ] `FuzzLoad` in `internal/config/fuzz_test.go` (or `config_test.go`), seeded from corpus.
- [ ] `go test -run=^$ -fuzz=FuzzLoad -fuzztime=5s ./internal/config/` exits 0.
- [ ] `go test -race ./internal/config/` still green.

---

### R2 — Degenerate extensions silently accepted (review-robustness minor)

**Finding:** `extensions = ["", "   ", ".", "..."]` normalizes to `[".", "..."]` with no `Problem`
emitted. Dot-only entries have no extension body and are not credible object-file extensions — violates
CR-6's spirit ("bad value → Problem").

**RED:** Add a test case to `TestLoadValidation` (or a new `TestNormalizeExtensionsDegenerate`) that
loads a config with `extensions = [".", "...", "  ", ""]` and asserts: (a) those degenerate entries
are dropped from the final `Extensions` slice, (b) at least one `Problem` with Key
`"workspace.extensions"` is returned. This test must fail before the fix.

**Expected result:** degenerate dot-only entries dropped + Problem emitted.

**DoD:**
- [ ] Failing test that reproduces the finding (before fix).
- [ ] `normalizeExtensions` rejects entries whose body after the leading dot is empty (len == 0).
- [ ] Problem emitted when entries are dropped.
- [ ] `go test -race ./internal/config/` green.

---

### R3 — `Bootstrap` not wired into `--stdio` entry point (review-acceptance minor)

**Finding:** T9 DoD states Bootstrap fills the `--stdio` TODO in `main.go` (config construction step).
The `--stdio` case is still an empty TODO; `Bootstrap` is only exercised by its unit test, never called
from the real entry point.

**RED:** Write an integration-style test (or update `TestBootstrap`) that imports `main`'s logic and
confirms `--stdio` calls `Bootstrap` — OR, simpler: directly test that `main.go`'s `--stdio` path
calls `config.Bootstrap` by extracting a `run(args, logger)` function from `main` and testing it. The
test must fail before wiring (i.e. currently the --stdio path does nothing config-related).

**Expected result:** `--stdio` path calls `config.Bootstrap`, logs resolved root.

**DoD:**
- [ ] Failing test that reproduces the finding.
- [ ] `cmd/natural-lsp/main.go` `--stdio` path calls `config.Bootstrap`.
- [ ] `go build ./cmd/natural-lsp/` clean.
- [ ] `go test -race ./...` green.