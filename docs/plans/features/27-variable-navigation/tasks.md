# Tasks: Variable & Reference Navigation (feature 27)

**Plan:** [`plan.md`](./plan.md)
**PRD requirements:** FR-54 (new; refines FR-24/FR-25); completes the binding half of FR-19/FR-20/FR-21
(SQL DDM/host-var references bound to declarations); refines FR-28; FR-17 (modeled gaps off the
diagnostic channel); FR-43 (graceful degradation).
**Depends on (shipped):** 07 (steplib-chain resolution), 08 (`DEFINE DATA` → `DataDefinition`), 08b
(`HostVarRef` + SQL DDM edges — extracted, unbound), 09 (`SymbolDataField` tree), 10
(definition/references providers + `findCursorTarget`), 12 (hover cards + `.NSD` parser), 19 (json/v2
marshaling), 22 (in-memory line-width table / `ForEachWithRange`).

**Two phases — Phase A (T1–T5) is independently shippable and delivers same-file variable navigation.**
Phase B (T6–T9) binds cross-file/host-var/DDM references and can be a second PR. The top-level loop may
choose to ship Phase A only; each task is a self-contained red → green → refactor slice.

Every task runs the TDD agents `tdd-red` → `tdd-green` → `tdd-refactor` in order.

---

## Current-state findings & impact (code surveyed 2026-07-25)

Planned against the code as it is, not as the README describes it. Citations are to as-built source.

- **`model.DataDefinition` has NO `NameRange` today** (`internal/model/model.go:239–264`). The mirror to
  add is `DataAccessEntry.NameRange` (`model.go:218–223`, feature 08). This is the sole persisted model
  addition for the whole feature (Phase A). — *drives T1.*
- **The parser already captures the field name token position implicitly.** `parseDataField`
  (`internal/analysis/natural/parser.go:232–321`) records `fieldStartPos` and consumes the name token
  via `p.current.Literal` at lines 279/290 (and the `+NAME` AIV branch at 279, adjusting `fieldStartPos`
  to the `+` sigil). Today it does **not** save the name token's own range — only the whole-field
  `StartPos`/`EndPos`. The `DEFINE SUBROUTINE` path already does exactly what we need for its own name
  (`parser.go:438–442`: `nameStartPos`/`nameEndPos` → `sub.NameRange`, feature 18); T1 mirrors that
  pattern for data fields. — *drives T1.*
- **`fieldToDefinition`** (`internal/analysis/natural/data.go:146–167`) is the single AST→model converter
  for fields (recursing into `Children`); it must copy the new AST `NameRange` into the model. REDEFINE
  children flow through the same converter (`data.go:261–271`). — *drives T1.*
- **`extractStructure`** (`internal/analysis/natural/structure.go`) sets `SymbolDataField.SelectionRange`
  from `def.Range` today (a whole-field span; `structure.go:221–230` `dataDefinitionToSymbol`). Story 3
  AC3 wants it set from `NameRange`. — *drives T1.*
- **Cache baseline is `cacheFormatVersion = "0.7.0"`** (`internal/workspace/cache.go:22`, set by feature
  24). `Definitions` is persisted (`cache.go:36`), so adding `NameRange` requires a bump to **`0.8.0`**.
  The version-gate rebuild is already exercised (`internal/workspace/cache_test.go:375–488, 825+`); T1
  adds a 0.7.0-rejection regression there. — *drives T1.*
- **Variable USE-SITES are not captured at all today.** The parser recovers past (discards) the bodies of
  the ~20 statement kinds it doesn't model (`parseStatement`, `parser.go:1645+`). The only use-site type
  that exists is SQL `HostVarRef` (feature 08b). The net-new Phase-A work is a **token-occurrence
  scanner**. The precedent is `scanOpaqueHostVars` (`internal/analysis/natural/sql.go:246–420`): a raw
  string/token scan that computes ranges, skips comments/subscripts, and normalizes names — reuse its
  shape. — *drives T2.*
- **The lexer tokenizes sigil-prefixed identifiers as single tokens**; `#GROUP.FIELD` = 3 tokens
  (`#GROUP`, `.`, `FIELD`), `#ARR(1)` = 4 tokens. Comment handling (`*`/`**` full-line, `/*`
  rest-of-line) and string literals are already lexed with positions — the scanner runs over the token
  stream (or lexer) so comment/string exclusion is free if we scan tokens, not raw text. — *drives T2, OQ-6.*
- **`findCursorTarget`** (`internal/server/cursor.go`) maps a cursor to an `*EdgeEntry`
  (by `Source`) or `*DataAccessEntry` (by `NameRange`), smallest-containing tie-break. It has no notion
  of a variable/host-var use-site. OQ-5: extend it to also return a variable use-site at
  **lowest-priority tie-break** after call/data targets (a call/data reference on the same token wins).
  — *drives T3.*
- **Providers `provideDefinition`/`provideReferences`** (`internal/server/definition.go`,
  `references.go`) currently read the on-disk index (`os.ReadFile` of the source for position conversion)
  and dispatch on `res.Get`. **They are NOT store-first** — feature 10's nav providers read `idx.Get` +
  disk, unlike hover/outline/completion which read the store first. Phase A variable navigation needs the
  **live open buffer** (uses are computed on demand from current text), so T3/T4 must add a store-first
  read path for the variable case (mirror `provideDocumentSymbols`/`provideHover` store-first). — *drives T3, T4.*
- **`referenceSites`** (`internal/server/references.go:139–223`) is the reverse index sweep, already using
  the feature-22 `idx.ForEachWithRange` line-width converter (no per-query disk sweep). It matches edges
  by resolution and DDM data-access by name. For Phase-A same-file variable references we do NOT sweep the
  workspace (uses are same-file only) — a separate same-file scan drives Story 2. — *drives T4.*
- **No `documentHighlight` capability today.** `initialize` advertises the eight providers listed in the
  locked allow-list (`server.go:223–237`; `server_test.go:598–607` `requiredProviders`). Story 2b adds
  exactly `documentHighlightProvider` (one entry). No dispatch case exists — add `textDocument/documentHighlight`
  to the request switch (near the callHierarchy cases at `server.go:1282+`). — *drives T5.*
- **Data-area files (`.NSL`/`.NSA`/`.NSG`) already route through the Natural parser** (`analyzer.go:70`
  early-returns only for `.NSD`; everything else parses). The parser handles a top-level `DEFINE DATA`
  construct (`parseDataSection`, `parser.go:126–181`), so a data-area export that is `DEFINE DATA … END-DEFINE`
  should already yield `Definitions`. **This is unverified against real data-area export shape** (a `.NSL`
  export may omit the program wrapper or use a bare section without `END-DEFINE`) and there are **no
  data-area fixtures in `testdata/`**. — *drives T6 (a verify-first task, resolving OQ-2).*
- **Phase-B object resolution reuses feature 07 unchanged:** `Resolve`/`ResolveInto`
  (`resolution.go:537,674`), `objectIdentity`/`buildSearchChain` (chain), and `idx.LookupByName`
  (`index.go:270`, name→`[]Candidate`). There is **no field-resolution** today — feature 07 resolves
  module edges, not fields. The new intra-object name→`DataDefinition` lookup and the `USING`-data-area
  field binding are net-new; OQ-3 recommends they live in `internal/workspace` alongside `resolution.go`
  (cross-file binding, same layer as feature 07, keeps the Analyzer seam clean). — *drives T7.*
- **SQL-sourced DDM table names** are already emitted as `EdgeReads`/`EdgeWrites` data-access entries by
  `extractSQLAccess` (feature 08b, `sql.go`), in the same DDM namespace as Adabas. `references.go:100`
  has a live `TODO (future): resolve data-access to DDM path` — this feature closes it. — *drives T9.*

**Seam check:** All LSP-facing code (T3–T5, T7 consumer, T8, T9) depends only on
`internal/analysis.Analyzer`/`internal/model` and `internal/workspace`. The scanner (T2) lives in
`internal/analysis/natural` behind a `model.VariableRef`-shaped result. The new field-resolution (T7)
lives in `internal/workspace`. No seam crossing.

---

## Phase A — same-file variable navigation (independently shippable)

### T1 — `DataDefinition.NameRange` (model + parser + structure + cache bump) — enabler

**Story 3.** Additive `model.DataDefinition.NameRange` populated by the parser for every field kind, wired
through to `SymbolDataField.SelectionRange`, with the cache bump `0.7.0 → 0.8.0`.

- **Reuse:** the `DEFINE SUBROUTINE` `nameStartPos`/`nameEndPos` → `NameRange` pattern
  (`parser.go:438–442`); `DataAccessEntry.NameRange` as the model mirror.
- **Model:** add `NameRange model.Range` to `DataDefinition` (`model/model.go`), documented as the
  name-token span ⊆ `Range`; note it is additive and bumps the cache format.
- **AST:** add `NameRange model.Range` to `DataField` (`ast.go:96–105`).
- **Parser:** in `parseDataField` (`parser.go:232–321`), capture the name token's range at each of the
  three name-assignment sites — the plain identifier/keyword branch (line 289–292), the `+NAME` AIV
  branch (267–288, range starts at the `+` sigil, matching the `fieldStartPos` adjustment), and leave
  REDEFINE blocks (no name) with a zero `NameRange`. Emit it on the returned `DataField`.
- **Converter:** copy `field.NameRange` → `def.NameRange` in `fieldToDefinition` (`data.go:146–167`),
  including for REDEFINE children (`data.go:261–271`).
- **Structure:** set `SymbolDataField.SelectionRange` from `def.NameRange` in `dataDefinitionToSymbol`
  (`structure.go:221–230`), falling back to `def.Range` when `NameRange` is zero (FR-43).
- **Cache:** bump `cacheFormatVersion` `"0.7.0" → "0.8.0"` (`cache.go:22`).

**Fixtures:** reuse/extend an existing `DEFINE DATA` fixture under
`internal/analysis/natural/testdata/dataaccess/` (or `structure/`) containing a scalar, a group with a
sub-field, a REDEFINE, and an array field; add a `+`-AIV field if not already present.

**RED:** parser test asserting each field's `NameRange` spans exactly the name token (not the level
number, not the type spec), for scalar / group sub-field / REDEFINE sub-field / array / `+`-AIV;
`fieldToDefinition` test asserting model `NameRange` is populated; `structure` test asserting
`SymbolDataField.SelectionRange == NameRange`; **cache regression test** asserting a `0.7.0` on-disk cache
is rejected as stale and forces rebuild (mirror `cache_test.go:375–488, 825+`).

**Modeled gaps / graceful:** REDEFINE block header (no name) → zero `NameRange`, no panic; malformed field
returning nil is unaffected (`parser.go:296–298`).

**DoD:**
- [ ] `model.DataDefinition.NameRange` added, documented as additive + name-token span ⊆ `Range`.
- [ ] Parser populates `NameRange` for scalar, group, sub-field, REDEFINE sub-field, array, `+`-AIV.
- [ ] `fieldToDefinition` copies `NameRange` (incl. REDEFINE children).
- [ ] `SymbolDataField.SelectionRange` set from `NameRange` (fallback to `Range` when zero).
- [ ] `cacheFormatVersion` = `"0.8.0"`; 0.7.0 cache rejected (regression test green).
- [ ] `just verify` green.

---

### T2 — variable use-site scanner (`extractVariableRefs`) + fuzz

**Stories 1/2 foundation (net-new work).** A token-occurrence scanner producing a `VariableRef`-shaped
result: every variable USE occurrence in the file, with a precise range, computed on demand (OQ-1, OQ-2:
scan not parser widening; OQ-4: not persisted).

- **Home:** `internal/analysis/natural` (behind the Analyzer seam), a new exported entry
  `ExtractVariableRefs(content string) []model.VariableRef` (or `[]VariableRef` if kept analyzer-internal
  and surfaced via a small server-side accessor — recommend a **model type** so the server can consume it
  without parser internals: add `model.VariableRef{Name, Range}` — additive, **in-memory only, NOT
  persisted, NO cache bump**; note this in the DoD).
- **Reuse:** `scanOpaqueHostVars` (`sql.go:246–420`) for the scan/range/normalization shape; run over the
  **lexer token stream** so comment (`*`/`**`/`/*`) and string-literal tokens are naturally excluded
  (OQ-6) rather than re-implementing comment skipping over raw text.

**Correctness rules to encode as test cases (each its own assertion, fixture-backed):**
- Case-insensitive collection (name normalized to upper-case, matching `DataDefinition.Name`).
- **Array subscripts stripped:** `#T(1:10)` and `#T(I)` → a ref to `#T`; the index var `I` is emitted as
  its **own** separate ref.
- **Group-qualified `#GROUP.FIELD`** (3 tokens) → captured such that the cursor on either `#GROUP` or
  `FIELD` resolves the same logical reference (T3 does the resolution; T2 must capture the qualified span
  and its parts — recommend emitting a ref carrying both the qualifier and the leaf, with the full
  `#GROUP.FIELD` range, so T3 can match on the level-1 group).
- **`*`-system variables excluded** (`*DATE`, `*TIME`, `*PROGRAM`, etc. — predefined read-only).
- **`&`-dynamic** substitution names excluded (or flagged as dynamic) — modeled gap.
- **Never match inside a comment or a string literal** (decoy fixture with `#FIELD` inside `'...'` and
  after `*`/`/*`).
- No substring false matches (`#FIELD` must not match inside `#FIELD-EXT`).

**Fixture:** `internal/analysis/natural/testdata/variablerefs/` — one program with an inline
`DEFINE DATA LOCAL` (a scalar, a group with a sub-field, a REDEFINE, an array `#T`) whose fields are used
across `MOVE`/`IF`/`WRITE`/`COMPUTE` and a `CALLNAT` argument, plus a `*`-system-var use, a `&`-dynamic
use, a `#FIELD` inside a string literal, and a `#FIELD` in a comment (decoys).

**RED:** table test over the fixture asserting the exact multiset of `VariableRef` (name + range) —
including the stripped-subscript cases, the separate index-var ref, the group-qualified capture, and the
**absence** of the three decoys and the `*`/`&` cases.

**Fuzz:** `FuzzExtractVariableRefs` (`internal/analysis/natural/fuzz_test.go`) — never panics on arbitrary
input, always returns a slice (FR-43), mirroring `FuzzExtractSQL`/`FuzzParseDDM`.

**DoD:**
- [ ] `model.VariableRef{Name, Range}` added (additive, **not persisted**, no cache bump — asserted).
- [ ] `ExtractVariableRefs` returns every use-site with a precise range, all rules above green.
- [ ] Comment/string/substring/`*`/`&` exclusions proven by decoy fixture.
- [ ] `FuzzExtractVariableRefs` green (no panic).
- [ ] `just verify` green.

---

### T3 — same-file go-to-definition for a variable (Story 1) + `findCursorTarget` extension

**Story 1.** Extend `provideDefinition` to resolve a variable use-site to its same-file `DEFINE DATA`
declaration, landing on `NameRange`.

- **Reuse:** T1's `NameRange`, T2's `ExtractVariableRefs`, `provideDefinition` shell (`definition.go`),
  `fromProtocolPosition`/`toProtocolRange` (`position.go`, encoding-aware).
- **OQ-5 — `findCursorTarget`:** extend `cursor.go` to also return a variable/host-var use-site as a third
  outcome (add a return value or a small struct), at **lowest priority** after edge/data-access
  (a resolved call/data reference on the same token still wins the tie-break; only when neither contains
  the cursor do we consult the variable refs). Preserve smallest-containing semantics. Update
  `FuzzCursorLookup` (`fuzz_test.go:139`) for the new return.
- **Store-first:** read the **open buffer** (document store) first for the source content and the on-demand
  variable refs (this is the first nav provider to go store-first — mirror `provideDocumentSymbols`),
  falling back to disk/index when the doc is not open.
- **Same-file field lookup (intra-object matcher):** a small helper (server-side is acceptable for Phase A
  since it is same-file and needs no chain; OQ-3 puts the *cross-file* variant in `internal/workspace` in
  T7) that, given the cursor's `VariableRef` and the file's `Definitions`, returns the matching
  declaration's `NameRange`:
  - bare name → the single declaration of that name (case-insensitive);
  - `#GROUP.FIELD` → the sub-field named `FIELD` under the level-1 group `GROUP`;
  - REDEFINE sub-field → its own declaration;
  - array/subscript already stripped by T2;
  - **ambiguous** unqualified name matching >1 group's sub-field → return **all** candidates (mirror
    feature 07 flat-namespace `Ambiguous`; `provideDefinition` already returns `[]Location`).
- **Idempotence:** invoking on the declaration's own name token resolves to itself (the cursor lands on a
  `Definitions` `NameRange`, which the matcher maps to itself).

**Fixtures:** reuse T2's `variablerefs/` program; add a copy under `internal/server/testdata/` (server
tests read via the store/index). Add an ambiguity fixture: two groups each with a sub-field of the same
unqualified name.

**RED:** provider tests — go-to-def on a use-site of a scalar/group-qualified/REDEFINE/array field lands on
the declaration `NameRange`; on the declaration (idempotent); ambiguous unqualified → multiple locations;
`findCursorTarget` unit tests for the new precedence.

**Modeled gaps (FR-17/FR-43):** `*`-system var, `&`-dynamic, undeclared name → empty `[]Location`, no
error. A call/data cursor still resolves via the existing edge path (regression: existing def tests stay
green).

**DoD:**
- [ ] `findCursorTarget` returns a variable use-site at lowest priority; existing edge/data behavior
      unchanged (regression green); `FuzzCursorLookup` updated + green.
- [ ] Go-to-def resolves scalar/group-qualified/REDEFINE/array variable to its declaration `NameRange`,
      same-file, store-first, encoding-aware.
- [ ] Idempotent on the declaration; ambiguous unqualified → all candidates.
- [ ] Modeled gaps → empty, no error.
- [ ] `just verify` green.

---

### T4 — same-file find-references for a variable (Story 2)

**Story 2.** Extend `provideReferences` to return all same-file use-sites of the variable under the cursor,
honoring `includeDeclaration`.

- **Reuse:** T2's `ExtractVariableRefs`, T3's cursor extension + intra-object matcher, `provideReferences`
  shell (`references.go`). Do **not** sweep the workspace (uses are same-file for Phase A).
- **Behavior:** identify the target declaration from the cursor (via T3's matcher — whether the cursor is
  on the declaration or a use), then return every `VariableRef` in the file that binds to that same
  declaration (case-insensitive; group-qualified and subscripted occurrences count; the declaration
  `NameRange` included iff `IncludeDeclaration`).
- **Store-first** as in T3.

**Fixtures:** reuse T2/T3's `variablerefs/` program (its multiple uses + decoys give complete coverage).

**RED:** find-refs on a variable returns exactly its use-sites (fixture-backed multiset), with/without the
declaration per `includeDeclaration`; **no** false match in a comment/string/substring/`*`-system var;
group-qualified and subscripted occurrences included; cursor on the declaration returns the same set as
cursor on a use.

**Modeled gaps:** `*`/`&`/undeclared cursor → empty, no error (FR-17). Existing edge/DDM find-refs
regression stays green.

**DoD:**
- [ ] Find-refs returns all same-file use-sites with precise ranges; `includeDeclaration` honored.
- [ ] Complete w.r.t. tokens; zero false matches in comment/string/substring/`*`-var (fixture-proven).
- [ ] Group-qualified/subscripted occurrences counted; declaration-cursor == use-cursor result.
- [ ] Modeled gaps → empty, no error; existing find-refs behavior unchanged.
- [ ] `just verify` green.

---

### T5 — document highlight + new `documentHighlightProvider` capability (Story 2b)

**Story 2b.** Advertise `documentHighlightProvider`, handle `textDocument/documentHighlight`, returning
every occurrence of the symbol under the cursor in the current file, reusing T4's scan.

- **Capability:** add `DocumentHighlightProvider: protocol.Boolean(true)` to `initialize`
  (`server.go:223–237`) and add `"documentHighlightProvider"` to the `requiredProviders` allow-list in
  `TestInitialize` (`server_test.go:598–607`) — **the one and only capability added by this feature.**
- **Dispatch:** add a `textDocument/documentHighlight` case to the request switch (near the callHierarchy
  cases, `server.go:1282+`); marshal the result via the json/v2 path (feature 19 `marshalResult`);
  empty result → `[]` (never `null`), matching the other list providers.
- **Provider:** `provideDocumentHighlight` (`internal/server/document_highlight.go`) — reuse T4's same-file
  variable occurrences; each highlight is a `DocumentHighlight{Range, Kind}`:
  - declaration + read use-sites → `Read`;
  - a write target (LHS of `MOVE … TO`, assignment `:=`/`COMPUTE … =`, `STORE`) → `Write` **where the
    read/write distinction is available** from the scan, else `Text`. (Read/write direction is
    best-effort — deriving it precisely is out of scope per the plan; default `Text` when unknown.)
  - Also works on a **call/subroutine name** by reusing the existing edge sites (so it is useful before
    variables — reuse `referenceSites`/`findCursorTarget` edge path, but scoped to the current file's
    occurrences).
- **Store-first**, encoding-aware ranges.

**Fixtures:** reuse `variablerefs/`; add a fixture exercising a `MOVE … TO #X` (Write) and reads of `#X`
(Read) to prove the Kind distinction; a call-name highlight fixture (reuse an existing `calls/` program
under `internal/server/testdata/`).

**RED:** documentHighlight on a variable returns all in-file occurrences with correct `Kind` (Read on
reads/decl, Write on the MOVE-TO target); on a call name returns the call sites; a `*`/`&`/no-target
cursor → empty; `TestInitialize` asserts the new `documentHighlightProvider` entry; a wire-bytes test
asserts empty → `[]`.

**DoD:**
- [ ] `documentHighlightProvider` advertised; `TestInitialize` allow-list updated (explicit, reviewed).
- [ ] `textDocument/documentHighlight` handled; json/v2 marshaling; empty → `[]`.
- [ ] Variable occurrences returned with Read/Write/Text kinds; call-name highlight works.
- [ ] Modeled gaps → empty, no error; no model/cache change (reuses T4's on-demand scan).
- [ ] `just verify` green.

**— Phase A complete and shippable here (Stories 1–3). —**

---

## Phase B — cross-file & host-var/DDM binding (second PR / increment)

### T6 — VERIFY & fix data-area field extraction (`.NSL`/`.NSA`/`.NSG`) — resolves OQ-2

**Story 4 foundation.** Confirm against as-built whether data-area exports already extract into
`FileAnalysis.Definitions`, and fix the parser only if a real data-area export shape is not covered.

- **Reason:** `.NSL`/`.NSA`/`.NSG` route through the Natural parser (`analyzer.go:70` only special-cases
  `.NSD`), and `parseDataSection` handles top-level `DEFINE DATA`. But there are **no data-area fixtures**
  and the real export shape (possibly a bare section, no program wrapper, or a `DEFINE DATA
  LOCAL/PARAMETER/GLOBAL … END-DEFINE` with no executable body) is unverified.
- **Fixtures:** add sanitized real-shape exports under `internal/analysis/natural/testdata/dataarea/`: an
  `.NSL` (LOCAL fields incl. a group), an `.NSA` (PARAMETER fields), an `.NSG` (GLOBAL, incl. a `WITH block`
  sub-block if the shape uses one).
- **RED:** `Analyze` on each data-area fixture yields the expected `Definitions` (name/level/type/NameRange
  — reusing T1). If green with no code change, the task is a **verify-only** confirmation (record that
  fact in the DoD); if red, apply the minimal parser tweak (e.g. accept a data-area file whose body is a
  bare section) and keep the fixture as a permanent regression.
- **GDA `WITH block`:** settle the sub-block question — for a first cut, treat whole-GDA fields as in scope
  (record the decision); a fixture documents current behavior.

**DoD:**
- [ ] Data-area fixtures added; `Analyze` extracts their `Definitions` with `NameRange` (T1).
- [ ] OQ-2 recorded: verify-only vs. minimal-parser-tweak, with the as-built finding.
- [ ] GDA `WITH block` handling decided and fixture-documented (whole-GDA in scope for first cut).
- [ ] `just verify` green.

---

### T7 — cross-file field resolution: `USING` data-area binding (Story 4) — resolves OQ-3

**Story 4.** Bind a variable whose declaration comes from a `LOCAL/PARAMETER/GLOBAL USING <name>` data
area to the field inside the referenced `.NSL`/`.NSA`/`.NSG`, via the steplib chain.

- **Home (OQ-3):** `internal/workspace` — a new field-resolution helper alongside `resolution.go`, reusing
  `objectIdentity`/`buildSearchChain`/`idx.LookupByName`. This keeps cross-file binding in the same layer
  as feature 07 and off the Analyzer seam.
- **Two-step resolution (the plan's key distinction):**
  1. **Locate the data-area object** (`.NSL`/`.NSA`/`.NSG`) by its `USING <name>` — reuse feature 07's
     steplib **chain** unchanged (current lib → steplibs → SYSTEM, non-transitive).
  2. **Locate the field within** the resolved data area — a **new intra-object name→`DataDefinition`
     lookup** (there is no field resolution today), matching the same rules as T3's same-file matcher
     (bare/group-qualified/REDEFINE), reusing T6's `Definitions`.
- **Parser prerequisite:** the analyzer must record each section's `USING <name>` reference so the server
  knows which fields are external. Check whether `DataSection` already captures the `USING` member name
  (feature 08 skips empty sections at `data.go:242`, implying `USING` sections currently yield no fields
  and no recorded reference). If not captured, add an additive AST/model field (e.g. `DataSection.Using` →
  a `model` surface) — **note any Phase-B model addition here** (this may be the one Phase-B model add; it
  is in-memory/edge-like, confirm whether it needs cache persistence — a `USING` name is a static edge, so
  prefer recording it as an `EdgeEntry`-style reference recomputed from cache, **no bump**, per feature 07
  OQ-1).
- **Provider wiring:** `provideDefinition`/`provideReferences` consult the new resolver when the same-file
  matcher (T3) finds no in-file declaration for the variable → fall through to the `USING` data-area
  binding → return the field's `NameRange` inside the data-area file (encoding-aware, cross-file).
- **Find-references scope:** returns uses **in the current module** only (workspace-wide shared-scope
  usage indexing is deferred — plan out-of-scope).

**Fixtures:** `internal/server/testdata/variablenav/` multi-file set — a subprogram with
`LOCAL USING MYLDA` + a `.NSL` (`MYLDA`) defining the field, under a library map (`.natural-lsp.toml`)
proving chain resolution; plus an out-of-chain data area (unresolved) and a field-absent case.

**RED:** go-to-def on a `USING`-sourced field lands on the field's `NameRange` inside the `.NSL`, located
via the chain; find-refs returns the current-module uses; unresolved data area / absent field → empty, no
error (FR-17); a regression proving a same-file field (T3) and a `USING` field don't cross-bind.

**Fuzz:** extend/add a resolver fuzz (or reuse `FuzzResolve`) so the new field-resolution entry never
panics (FR-43).

**DoD:**
- [ ] `USING <name>` reference captured by the analyzer (additive; note cache impact — expected none,
      recomputed from cache like feature 07).
- [ ] Field-resolution helper in `internal/workspace` reuses the chain for object location + new
      intra-object field lookup.
- [ ] Go-to-def/find-refs resolve `USING`-sourced fields cross-file (chain-located), encoding-aware.
- [ ] Unresolved data area / absent field → empty, no error; same-file vs `USING` don't cross-bind.
- [ ] Resolver fuzz green (no panic).
- [ ] `just verify` green.

---

### T8 — host-variable navigation binds to declarations (Story 5) — completes FR-21

**Story 5.** Go-to-definition, find-references, and hover on SQL host variables (native clauses and
`PROCESS SQL` bodies), binding each `HostVarRef` to its `DEFINE DATA` field (same-file or via T7's
`USING` binding).

- **Reuse:** `FileAnalysis.HostVarRefs` (feature 08b, already persisted, 0.5.0 — no new persistence),
  T3's same-file matcher + T7's cross-file field resolver, feature 12 hover card (`hover.go`).
- **Cursor:** extend `findCursorTarget` (T3) to also match a `HostVarRef` (by its `Range`), at the same
  lowest-priority tier as a variable use.
- **Binding:** a host var's name (already colon-stripped and normalized by 08b, incl. `:U:`/`:G:`/`:T:`
  qualifiers and `INDICATOR`/`LINDICATOR`/array stripped) matches a `DEFINE DATA` field like any variable
  use — route it through the same matcher/resolver.
- **Providers:** go-to-def on a `HostVarRef` (bare, colon-prefixed native, or `:host-var` in a `<<…>>`
  body) → declaration; find-refs includes host-var use-sites among a field's references (extend T4's
  same-file collection to also scan `HostVarRefs`); hover on a host var → the field's interface card
  (feature 12).

**Fixtures:** `internal/server/testdata/variablenav/` — a subprogram with a `SELECT … INTO :HV WHERE …`
and a `PROCESS SQL <<… :HV …>>` body, where `HV` is a `DEFINE DATA` field (same-file and a `USING` case).

**RED:** go-to-def on a native and an opaque-body host var → its declaration; find-refs on the field
includes the host-var sites; hover shows the field card; qualifier/indicator/array-stripped forms all
bind; unbindable/dynamic host var → empty, no error.

**DoD:**
- [ ] `findCursorTarget` matches `HostVarRef`; host vars bind via the same matcher/resolver.
- [ ] Go-to-def/find-refs/hover work on native and `PROCESS SQL`-body host vars.
- [ ] Qualifier/indicator/array forms tolerated; unbindable/dynamic → empty, no error.
- [ ] No model/cache change (reuses persisted `HostVarRefs`).
- [ ] `just verify` green.

---

### T9 — SQL-sourced DDM table names resolve to their DDM (Story 6) — completes FR-19/FR-20, closes 08b OQ

**Story 6.** A SQL `FROM`/`INTO`/`INSERT INTO`/SQL-`UPDATE`/`DELETE` table operand (and the `PROCESS SQL`
`ddm-name`) resolves to its `.NSD` DDM through the steplib chain — the same DDM namespace and code path as
an Adabas `READ`/`FIND` view.

- **Reuse:** these are already `DataAccessEntry` entries (feature 08b `extractSQLAccess`, same DDM
  namespace). Close the live `TODO (future): resolve data-access to DDM path` at `references.go:100` and
  the `targetPath = ""` DDM stub — implement DDM resolution via the shared chain (`idx.LookupByName(…,
  model.ObjectDDM, …)` + `buildSearchChain`), the **same path** feature-07-style resolution would use for
  Adabas views. **No separate SQL path.**
- **Providers:** go-to-def on a SQL table operand → the `.NSD` object root (reuse `definitionLocation`);
  find-refs → all read/write sites with that DDM name (the existing `referenceSites` DDM branch already
  matches by name — now backed by real resolution).
- **Scope guard:** in-body opaque `<<…>>` table names remain **pass-through text and are NOT bound** (08b
  M-6); only the `ddm-name` operand of `PROCESS SQL` binds (already an entry, not the body).

**Fixtures:** `internal/server/testdata/variablenav/` — a `.NSD` DDM plus a program that both `READ`s it
(Adabas) and `SELECT … FROM` it (SQL) under a library map; the headline regression asserts the Adabas view
and the SQL table with the **same DDM name resolve identically** (Story 6 AC2).

**RED:** go-to-def on a SQL `FROM` table → the `.NSD`; on a `PROCESS SQL` `ddm-name` → the `.NSD`; find-refs
groups the Adabas and SQL sites together; the shared-path regression (identical resolution) green; an
in-body table name is NOT resolved (pass-through).

**DoD:**
- [ ] SQL-sourced DDM operands resolve to `.NSD` via the shared chain (no separate SQL path); `references.go`
      TODO/`targetPath=""` stub closed.
- [ ] Go-to-def/find-refs on SQL tables behave identically to Adabas views (regression fixture proves it).
- [ ] `PROCESS SQL` opaque-body table names remain unbound (M-6).
- [ ] Binding recomputed from cached edges (feature 07 OQ-1) — no cache bump beyond T1's 0.8.0.
- [ ] `just verify` green.

---

## Reviews required (for `/review-feature`)

- **`review-analysis`** — T2 scanner correctness (subscript strip, group-qualified capture, `*`/`&`
  exclusion, comment/string exclusion, no substring match); T6 data-area extraction shape; T1 `NameRange`
  spans exactly the name token for every field kind incl. `+`-AIV and REDEFINE.
- **`review-workspace`** — T7 field-resolution home (`internal/workspace`, OQ-3), chain reuse
  non-transitive, `USING` capture; T9 shared DDM resolution path (no SQL fork); feature-07 OQ-1 (no extra
  cache bump for binding) upheld.
- **`review-server`** — `findCursorTarget` precedence (OQ-5, variable/host-var lowest priority, existing
  edge/data behavior unchanged); store-first for the new variable providers; `documentHighlightProvider`
  allow-list update (T5); json/v2 marshaling + empty→`[]` sentinels; encoding-aware ranges.
- **`review-cache`** — single `0.7.0 → 0.8.0` bump (T1 only); regression rejecting a 0.7.0 cache; confirm
  no Phase-B persistence added (VariableRef in-memory, USING recomputed, binding recomputed).
- **`review-seam`** — scanner + `NameRange` behind the Analyzer seam; field-resolution in
  `internal/workspace`; LSP providers depend only on `model`/`Analyzer`/`workspace`.
- **`review-docs`** — CLAUDE.md/README "Project state" updated (feature 27, cache 0.8.0, new
  `documentHighlightProvider` capability, 08b binding gap + OQs closed); cross-reference from the 08b
  follow-up note.

## Open questions (residual)

- **OQ-1 (impl) — RESOLVED (recommended):** token-occurrence scan behind a `model.VariableRef` seam
  (T2), not parser widening. Confirm during T2 that scanning the **lexer token stream** (vs. raw text) is
  used so comment/string exclusion is free (OQ-6).
- **OQ-2 (data-area extraction) — RESOLVE IN T6:** whether `.NSL`/`.NSA`/`.NSG` exports already extract to
  `Definitions` (likely yes — they route through the parser) or need a minimal parser tweak; also GDA
  `WITH block` handling (recommend whole-GDA in scope for a first cut). T6 is verify-first.
- **OQ-3 (field-resolution home) — RESOLVED (recommended):** `internal/workspace` alongside
  `resolution.go` (T7), reusing the chain, keeping the Analyzer seam clean.
- **OQ-4 (persist use-sites?) — RESOLVED:** Phase A computes uses on demand (no persistence); only
  `DataDefinition.NameRange` is the persisted addition (single `0.8.0` bump, T1). Cross-module usage
  indexing stays deferred. Confirm no Phase-B persistence sneaks in (T7 `USING` capture recomputed from
  cache like feature 07).
- **OQ-5 (`findCursorTarget`) — RESOLVED:** extend to a lowest-priority variable/host-var use-site tier
  (T3/T8), preserving smallest-containing behavior; existing edge/data outcomes win ties.
- **OQ-6 (comment/string exclusion & qualified spans) — RESOLVE IN T2:** scan tokens (not raw text) to
  exclude comments/strings; a `#GROUP.FIELD` reference (3 tokens) is one logical reference resolving from a
  cursor on either part.
- **NEW (T7) — `USING` reference capture shape:** whether the analyzer already records a section's
  `USING <name>` (feature 08 currently skips empty `USING` sections at `data.go:242`). If it must be added,
  decide the surface (recommend an `EdgeEntry`-style reference recomputed from cache — no bump). Settle in T7.
- **NEW (T5) — read/write Kind availability:** the DocumentHighlight `Read`/`Write` distinction is
  best-effort (default `Text` when the scan can't derive assignment direction — direction semantics are
  explicitly out of scope per the plan). Confirm the fixture coverage is acceptable to reviewers.
