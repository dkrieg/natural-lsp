# Tasks: Object-type recognition

**Source plan:** [plan.md](./plan.md)
**PRD requirements:** FR-7 (P0, core types), FR-8 (P2, extended types), FR-9 (P0, indexed-set consistency)
**Related:** FR-43 / NFR-6 (graceful degradation), NFR-15 (replaceable backend / seam), NFR-9 (regression fixtures)
**Branch:** `feat/02-object-type-recognition`

---

## Current-state findings & impact

Surveyed `internal/config`, `internal/model`, `internal/analysis`, `internal/analysis/natural`,
`testdata/`, and `.claude/knowledge/natural/file-extensions.md`. The feature lives entirely on the
**extraction-backend side of the Analyzer seam** plus one shared-contract change to `internal/model`.

### What already exists (reuse, do not duplicate)

- **`internal/config` owns the *configured* indexed extension set, not classification.**
  `WorkspaceConfig.Extensions` (config.go:47) defaults to the ten core extensions
  (`.NSP .NSN .NSS .NSC .NSM .NSL .NSG .NSA .NSH .NSD`, config.go:226-229) and
  `normalizeExtensions` (config.go:436) canonicalizes every entry to **upper-case, dot-prefixed**
  form, deduped in first-occurrence order. So by the time a path reaches the analyzer, the *decision
  to index a file* is already made by config; this feature decides **what construct an indexed file
  is**. Classification must match config's normalization (upper-case, leading dot) so the two agree.
- **`config.SkipReason`** (config.go:275) is the existing "observable skip" surface
  (`SkipExcluded`, `SkipTooLarge`). FR-9's "unrecognized extension is ignored, observable in logs"
  belongs to this surface — but the indexer that consumes `SkipReason` is feature 03, not this one.
  This plan adds the *analyzer-side* signal (an unknown-type classification + diagnostic) and flags
  the indexer wiring as out-of-scope/handoff, rather than inventing an indexer here.
- **The Analyzer seam is defined** (analysis/analyzer.go): `Analyze(path string, content []byte)
  (model.FileAnalysis, error)`. Its doc comment already states *"Path is used for object-type
  classification (by extension)"* — this feature is the first to honor that. The regex backend
  (`analysis/natural/analyzer.go`) `Analyze` is a stub returning `model.FileAnalysis{}`.
- **KB resolves the FR-8 open question (verified 2026-06-21 against official NaturalONE docs).**
  `.claude/knowledge/natural/file-extensions.md` confirms the full mapping including all five extended
  types: `.NS4` Class, `.NS7` Function, `.NS3` Dialog, `.NS8` Adapter, `.NST` Text. The plan's open
  question *"which extended types are in scope"* is answered; the default-vs-opt-in judgment (Q1) is
  **resolved: all fifteen types ship default-on** (see Q1 decision below).
- **`.NAT` is NOT a valid Natural source extension.** Confirmed absent from SPoD/NaturalONE/batch
  export tooling; appears only on generic file-encyclopedia sites. Do not add it to the classifier or
  the default set.

### Shared-contract change (with migration)

- **`model.FileAnalysis` is an empty stub** (model.go:27-30) — no fields, no consumers yet
  (`workspace` and `server` are stubs). Adding `ObjectType` is a contract change *by definition*, but
  because there are **no real downstream consumers today**, the migration cost is limited to the
  `analysis/natural` backend that populates it. No `workspace`/`server`/`lsp-graph` migration tasks
  are needed yet. `review-seam` is still required because the model is the shared contract.

### Criteria already satisfied (skipped)

- *Story 1 — "classification normalizes case"* is **partially pre-satisfied at the config layer**: any
  configured extension is already upper-cased before indexing. But a *path* arriving at `Analyze`
  carries whatever case the filesystem has (`Customer.nsp`), so the **classifier itself must still
  normalize** the path's own extension. Not skippable — covered by Task 2.
- *Story 1 — "recognized-but-unreadable file is skipped gracefully"* is **out of scope here**: the
  malformed-content skip path is owned by the indexer (feature 03, FR-43). This feature only
  guarantees classification *succeeds from the path alone* and never depends on content being
  readable. Covered as an explicit non-dependency in Task 2's DoD; full skip behavior handed to 03.

### Code/README/plan divergences flagged

1. **Default extended-type gap (FR-8 vs config defaults) — RESOLVED.** Config's default `Extensions`
   is the ten *core* extensions only; the five extended types are absent. **Decision: all fifteen
   types ship default-on.** Task 7 adds `.NS4 .NS7 .NS3 .NS8 .NST` to `config.Defaults()` and
   updates the `Sample()` comment and docs. (Classification in Task 6 is config-independent regardless.)
2. **`.NSC` dual role.** KB notes `.NSC` is Copycode (INCLUDE target) and is a *fragment*, not a
   standalone object. The Context note in the prompt floated `.NSX` for class — that is wrong per
   the verified KB; class is `.NS4`. Task 1's type table uses the KB values, not the prompt's guess.
3. **No `testdata/` fixtures or analysis tests exist** — this is greenfield within the analysis
   package. Every task creates its own minimal `.NSx` fixture per the CLAUDE.md convention.

---

## Ordered task list

Dependency order: **model contract → classifier core → wire into backend → core-type table →
unknown-type handling → extended types → config-defaults reconciliation → docs**. Each task is one
red → green → refactor loop run by `tdd-red` → `tdd-green` → `tdd-refactor`.

---

### Task 1 — Add `ObjectType` to the model contract

**Behavior:** Introduce a typed `model.ObjectType` enumeration and add an `ObjectType` field to
`model.FileAnalysis`. Values cover the ten core constructs plus an explicit "unknown/unrecognized"
sentinel. Pure type/contract task — no classification logic yet.

- **Reuses / migrates:** Extends `model.FileAnalysis` (model.go:27). Shared-contract change; no
  downstream consumers exist yet (`workspace`, `server` are stubs), so the only migrant is the
  `analysis/natural` backend in later tasks. Keep the type **free of backend internals** (NFR-15/16).
- **Fixtures:** none (type-only).
- **Expected result:** `model.ObjectType` is a string-backed type (mirroring `EdgeKind`, model.go:12)
  with named constants for: Program, Subprogram, ExternalSubroutine, Copycode, Map, LocalDataArea,
  GlobalDataArea, ParameterDataArea, Helproutine, DDM, plus `ObjectUnknown` (stable machine-readable
  string values, e.g. `"program"`, `"subprogram"`, …, `"unknown"`). `FileAnalysis` gains an
  `ObjectType ObjectType` field. Package compiles; existing `EdgeKind` consts untouched.
- **DoD:**
  - [ ] `model.ObjectType` + 10 core constants + `ObjectUnknown` defined with documented stable values.
  - [ ] `FileAnalysis.ObjectType` field added and documented.
  - [ ] A table-driven test asserts each constant's stable string value (guards against accidental
        renames that would break future consumers / `lsp-graph`).
  - [ ] `gofmt`/`go vet` clean; model stays free of backend internals.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** none.

---

### Task 2 — Classifier: derive object type from a path's extension (built-in + custom mappings, case-insensitive)

**Behavior:** A pure function in `internal/analysis/natural` that maps a file path to its
`model.ObjectType` using only the path's extension, normalizing case. Accepts an optional custom
extension map (from config) that is consulted **first**, before the built-in table — this is the
mechanism by which users can map non-standard extensions (e.g. `.NAT`) to a known construct type.
Decides construct from the extension alone — never reads or depends on file content.

- **Reuses / migrates:** New helper
  `classify(path string, custom map[string]model.ObjectType) model.ObjectType` in a new file
  `internal/analysis/natural/objecttype.go`. Passing `nil` (or empty map) for `custom` gives the
  built-in behavior. Normalizes the path extension to **upper-case + leading dot** to agree with
  `config.normalizeExtensions` (config.go:436); custom-map keys must also be normalized before lookup.
  Consumes Task 1's `model.ObjectType`.
- **Fixtures:** none for the unit (path strings drive it). Fixtures arrive in Task 4.
- **Expected result (table-driven):**
  - Built-in: `CUSTOMER.NSP`→Program, `customer.nsp`→Program, `Sub.Nsn`→Subprogram,
    `.NSS`→ExternalSubroutine, `.NSC`→Copycode, `.NSM`→Map, `.NSL`→LocalDataArea,
    `.NSG`→GlobalDataArea, `.NSA`→ParameterDataArea, `.NSH`→Helproutine, `.NSD`→DDM.
  - Custom override: `classify("file.NAT", map{".NAT": ObjectProgram})` → Program.
  - Custom overrides built-in: `classify("file.NSP", map{".NSP": ObjectSubprogram})` → Subprogram.
  - Unknown/foreign/no-extension → `ObjectUnknown`.
- **DoD:**
  - [ ] Table-driven test over all 15 extensions (core + extended) in lower/upper/mixed case → correct construct.
  - [ ] Custom map tested: nil/empty → built-in; non-nil → custom-first lookup.
  - [ ] Unknown/foreign/no-extension → `ObjectUnknown`.
  - [ ] Classifier is content-independent (no file I/O); documented as such.
  - [ ] Deterministic; `gofmt`/`go vet` clean; lives on the backend side of the seam.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** Task 1.

---

### Task 3 — Wire classification into the Analyzer backend (`Analyze` populates `ObjectType`)

**Behavior:** `analysis/natural.Analyze` calls the Task 2 classifier and sets
`FileAnalysis.ObjectType` from the path, regardless of content. This is the seam-level guarantee:
the LSP layer gets the object type through `analysis.Analyzer` only, never by inspecting paths itself.

- **Reuses / migrates:** Edits the stub `Analyze` (analysis/natural/analyzer.go:23). The backend
  struct is initialized with `config.WorkspaceConfig` (or the derived custom extension map extracted
  from it) so the classifier can consult user-defined mappings at call time. No other consumers to
  migrate.
- **Fixtures:** minimal `testdata/objecttype/program.NSP` (a one-line trivial Natural program, e.g.
  `WRITE 'HELLO'` + `END`, sanitized). One fixture is enough — this task asserts the *wiring*, not
  the full table (that's Task 4).
- **Expected result:** `Analyze("…/program.NSP", content)` returns `FileAnalysis{ObjectType:
  ObjectProgram}` with `err == nil`. `Analyze` does **not** error when content is empty/garbage — it
  still classifies from the path (proves content-independence at the seam). A custom-mapped path is
  also tested: backend initialized with `ExtensionTypes: {".NAT": "program"}` + `Analyze("x.NAT", nil)`
  → ObjectProgram.
- **DoD:**
  - [ ] Test through the `analysis.Analyzer` interface (not the concrete helper) confirms `ObjectType`
        is set from the path.
  - [ ] Empty/garbage `content` still yields the correct `ObjectType` and no error.
  - [ ] Custom mapping round-trip test: config-sourced override reaches `Analyze` output.
  - [ ] Compile-time seam assertion (`var _ analysis.Analyzer`) still holds; LSP-facing purity intact.
  - [ ] `gofmt`/`go vet` clean.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** Task 2.

---

### Task 4 — Fixture-backed classification for all ten core types (FR-7 acceptance)

**Behavior:** Satisfy Story 1's *"each classification is backed by a fixture under `testdata/`"* by
driving `Analyze` over one minimal real `.NSx` fixture per core construct.

- **Reuses / migrates:** Reuses the Task 3 wiring and the `testdata/objecttype/` directory. No new
  production code expected (this is acceptance coverage); if a construct misclassifies, fix the
  Task 2 table per the NFR-9 regression convention.
- **Fixtures (minimal, sanitized, one per type) under `testdata/objecttype/`:**
  `program.NSP`, `subprogram.NSN`, `subroutine.NSS`, `copycode.NSC` (a fragment, no `END`),
  `map.NSM`, `local.NSL`, `global.NSG`, `parameter.NSA`, `helproutine.NSH`, `ddm.NSD`. Contents may
  be near-empty; classification depends on extension only, so a one-line placeholder per file is fine.
- **Expected result:** a table-driven test reads each fixture through `Analyze` and asserts the
  matching `model.ObjectType`. All ten pass.
- **DoD:**
  - [ ] Ten fixtures created; each is sanitized, non-proprietary, minimal.
  - [ ] Table-driven test maps each fixture → expected construct via `Analyze`.
  - [ ] Fixtures committed as permanent regression assets (NFR-9).
  - [ ] `gofmt`/`go vet` clean.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** Task 3 (and Task 2's table).

---

### Task 5 — Unknown extension: classify as `ObjectUnknown` + emit extraction diagnostic (FR-9 observability)

**Behavior:** A file whose extension is not a recognized object type is classified `ObjectUnknown`,
and the analyzer surfaces that as an **observable signal** (not a silent no-op): an extraction-level
note/diagnostic on the `FileAnalysis` recording the unrecognized type. This is the analyzer half of
FR-9's *"unrecognized/unconfigured extension is ignored without error and the fact is observable in
logs."* Per CLAUDE.md, an unmatched pattern must be flagged on purpose — silence is a bug.

- **Reuses / migrates:** Builds on Task 1/2/3. **Adds `Diagnostics []Diagnostic` field to
  `model.FileAnalysis`** (Q2 decision: richer signal, does not depend on the feature-03 indexer to
  surface it). Also defines `model.Diagnostic` (a message + severity struct). When the extension is
  unknown, `Analyze` returns `ObjectUnknown` + a `Diagnostic{Message: "unrecognized extension …",
  Severity: DiagnosticInfo}`. This is a second shared-contract touch → `review-seam` required.
  Feature-03 indexer later reads `FileAnalysis.Diagnostics` to emit `SkipReason`/logs; that wiring is
  out of scope here and noted in a comment.
- **Fixtures:** `testdata/objecttype/notes.txt` (a non-`.NSx` file) and `testdata/objecttype/data.NSZ`
  (a plausible-but-unrecognized `.NS?` extension).
- **Expected result:** `Analyze` on each → `FileAnalysis{ObjectType: ObjectUnknown}`, `err == nil`
  (no crash, no error — graceful per FR-43/NFR-6). The unknown classification is distinguishable from
  every recognized type.
- **DoD:**
  - [ ] Unrecognized extension → `ObjectUnknown`, no error.
  - [ ] `ObjectUnknown` is a distinct, assertable value (Task 1 guarantees the constant).
  - [ ] Decision on diagnostic surface recorded (Q2); if deferred to the indexer, note the handoff
        in the test/comment so feature 03 wires it to `SkipReason`/logs.
  - [ ] `gofmt`/`go vet` clean; graceful degradation held.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** Task 3.

---

### Task 6 — Extended object types: classify class / function / dialog / adapter / text (FR-8)

**Behavior:** Extend the classifier to recognize the five extended constructs from the verified KB,
adding their `model.ObjectType` constants. Classification is **config-independent** — recognizing the
extension is separate from whether the indexer indexes it (Task 7).

- **Reuses / migrates:** Extends Task 1's `model.ObjectType` (5 new constants) and Task 2's
  classifier table. Per `file-extensions.md`: `.NS4` Class, `.NS7` Function, `.NS3` Dialog,
  `.NS8` Adapter, `.NST` Text.
- **Fixtures (minimal) under `testdata/objecttype/`:** `class.NS4`, `function.NS7`, `dialog.NS3`,
  `adapter.NS8`, `text.NST`.
- **Expected result:** each extended extension (lower/upper/mixed case) → its construct via `Analyze`;
  table-driven test passes for all five. The Task 4/5 core+unknown behavior is unchanged
  (regression-guarded by re-running those tables).
- **DoD:**
  - [ ] 5 extended constants added with stable string values + value-assertion test (extends Task 1).
  - [ ] Classifier recognizes all five, case-insensitively.
  - [ ] 5 fixtures created (sanitized, minimal); near-empty content acceptable.
  - [ ] Core-type and unknown-type tests still green (enabling extended types does not alter existing
        classification — Story 3 acceptance).
  - [ ] `gofmt`/`go vet` clean.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** Task 5 (so the unknown path is settled before widening the recognized set).

---

### Task 7 — Config: add extended types to defaults + `ExtensionTypes` custom-mapping field

**Behavior:** Two config changes in one task (both touch the same `WorkspaceConfig` struct):
1. Add the five extended extensions to `config.Defaults().Workspace.Extensions` (all 15 now default-on).
2. Add a new `ExtensionTypes map[string]string` field (TOML: `[extension_types]`) that lets users map
   arbitrary extensions to known construct types — e.g. `".NAT" = "program"`. Values are validated
   against the stable `model.ObjectType` string values; an unrecognized value → `Problem` + default
   (CR-6 fail-safe pattern, same as feature 01). The `Sample()` generator shows an example entry.

- **Reuses / migrates:**
  - `config.Defaults()` (config.go:226-229): add `.NS4 .NS7 .NS3 .NS8 .NST`.
  - `WorkspaceConfig` struct: new `ExtensionTypes map[string]string` field.
  - Config validation (config.go validation pass): each key → normalized extension form; each value →
    must match a known `model.ObjectType` stable string. Invalid key or value → `Problem`, entry
    dropped (fail-safe; indexing continues with the remaining valid mappings).
  - `Sample()` (config.go:372-373): add commented example `[extension_types]` block.
  - Crosses into `internal/config` (feature 01's surface) — keep `normalizeExtensions` semantics.
- **Fixtures:** none new; config tests are table-driven in `internal/config`.
- **Expected result:**
  - `Defaults().Workspace.Extensions` has 15 entries (core + extended).
  - Parsing `[extension_types]\n".NAT" = "program"` → `ExtensionTypes = map{".NAT": "program"}`.
  - Parsing an unknown value `".NAT" = "widget"` → `Problem` added, entry omitted (fail-safe).
  - `Sample()` output contains an `[extension_types]` example block.
- **DoD:**
  - [ ] 15-extension default verified in existing config test (extended, not broken).
  - [ ] `ExtensionTypes` round-trip: valid TOML → parsed map, invalid value → Problem, no crash.
  - [ ] `Sample()` includes `[extension_types]` example.
  - [ ] `normalizeExtensions` semantics preserved; existing config tests green.
  - [ ] `gofmt`/`go vet` clean.
- **Agents:** `tdd-red` → `tdd-green` → `tdd-refactor`
- **Depends on:** Task 6.

---

### Task 8 — Documentation sync (CLAUDE.md "Project state" + README object-type set)

**Behavior:** Update `CLAUDE.md` "Project state" to record that object-type recognition shipped and
`model.ObjectType`/`Analyze` classification now exists; ensure the README's extension→construct
table and any default/optional-set wording match what landed (informed by Task 7's decision).

- **Reuses / migrates:** Doc-only. Anticipates the `review-docs` reviewer and `/finalize-feature`
  sync. No code.
- **Fixtures:** none.
- **Expected result:** docs describe the as-built classification capability, the recognized core +
  extended types, and the default/optional indexed sets consistently with config.
- **DoD:**
  - [ ] CLAUDE.md "Project state" updated (analysis layer now classifies object types).
  - [ ] README extension table consistent with the KB and config defaults.
  - [ ] No code change; no test impact.
- **Agents:** none (doc task; verified by `review-docs` in `/review-feature`).
- **Depends on:** Task 7.

---

## Reviews required (`/review-feature`)

- **`review-seam`** — `model.FileAnalysis` (the shared contract) changed; verify LSP-facing purity
  and that the backend remains behind `analysis.Analyzer`. (Tasks 1, 3, and possibly 5.)
- **`review-robustness`** — the backend now parses a new input dimension (file paths/extensions);
  verify case-insensitivity, graceful handling of unknown/empty/dotfile/multi-dot paths, and that
  classification never depends on content (FR-43 / NFR-6).
- **`review-docs`** — the feature changes capability (object-type recognition) and possibly the
  default indexed set; verify CLAUDE.md/README sync (Tasks 7–8).
- *(Not required: concurrency — no indexer/watcher touched here; protocol conformance — no LSP method
  added; performance — classification is O(1) string work, not a hot-path concern at this scope.)*

---

## Decisions recorded (was Open Questions)

- **Q1 (RESOLVED) — All fifteen extensions default-on.** All five extended types (`.NS4 .NS7 .NS3
  .NS8 .NST`) ship in the default `Extensions` alongside the ten core types. Task 7 implements this.
- **Q2 (RESOLVED) — Add `Diagnostics []Diagnostic` to `FileAnalysis`.** An unrecognized extension
  produces `ObjectUnknown` + a `Diagnostic` on the returned `FileAnalysis`. Feature-03 indexer later
  reads `Diagnostics` to emit `SkipReason`/logs. Task 5 implements this, Task 1 defines the type.
- **Q3 (DEFERRED) — sub-classification.** Sub-classification (e.g. inline maps vs `.NSM`, inline
  vs external subroutines) is out of scope here. Inline-vs-external subroutine resolution is a
  feature-06 concern (`PERFORM` binding).
- **`.NAT` is NOT a valid Natural source extension** (verified 2026-06-21 against official NaturalONE
  docs) — do not add to the built-in classifier table or default `Extensions` set. Users who have
  `.NAT` files can map them via the new `[extension_types]` config field (e.g. `".NAT" = "program"`).
- **Custom extension mappings ship in Task 7 via `WorkspaceConfig.ExtensionTypes`.** The classifier
  (Task 2) accepts custom mappings as a parameter; the backend (Task 3) passes the config-derived map
  at initialization. CR-6 fail-safe: unknown ObjectType value → Problem + entry dropped, never crash.
