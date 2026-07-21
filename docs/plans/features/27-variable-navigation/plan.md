# Feature: Variable & Reference Navigation — Definition/References for Data Variables & Host Variables, Same-File and Cross-File

**Status:** Planned
**PRD requirements:** FR-54 (variable navigation — new; refines FR-24/FR-25); completes the **binding half** of FR-19/FR-20/FR-21 (SQL DDM/host-var references bound to declarations); refines FR-28 (hover on a variable/host-var); FR-17 (modeled gaps off the diagnostic channel), FR-43 (graceful degradation)
**Priority / phase:** P1 (v1.0 stable; everyday navigation + closes the last extraction-binding gap)
**Depends on:** [07](../07-call-dependency-resolution/plan.md) (steplib-chain resolution machinery), [08](../08-data-access-extraction/plan.md) (`DEFINE DATA` → `DataDefinition`), [08b](../08b-embedded-sql-extraction/plan.md) (`HostVarRef` + SQL DDM edges — extracted, unbound), [09](../09-program-structure-extraction/plan.md) (`SymbolDataField` tree), [10](../10-navigation-and-symbol-search/plan.md) (definition/references providers + `findCursorTarget`), [12](../12-hover/plan.md) (hover cards + the `.NSD` DDM parser). Independent of [26](../26-lsp-tracing-and-logging/plan.md).

## Summary

Navigation today works on **module targets** (CALLNAT/FETCH/RUN/PERFORM) and **DDM view** access sites, but
**not on data variables or host variables**, and several references the analyzer already *extracts* are
left **unbound** — a documented gap that spans three places:

1. **Data variables** — a cursor on `#CUSTOMER-NAME` (a `MOVE`/`IF`/`WRITE`/`CALLNAT`-argument operand)
   has no go-to-definition and no find-references, even though the *declaration* side is fully extracted
   (`FileAnalysis.Definitions` + the `SymbolDataField` tree). Use-sites are simply never captured — the
   parser recovers past (discards) the bodies of the statements it doesn't model.
2. **SQL host variables** — feature 08b **extracts** `HostVarRef{Name, Range}` from native SQL clauses and
   the `PROCESS SQL` opaque body, but explicitly leaves them **unbound** ("binding … to declarations
   (resolution) is future work"). So there's no go-to-definition/references/hover on a host var.
3. **SQL-sourced DDM table names** — feature 08b emits read/write edges for SQL `FROM`/`INTO`/… tables but
   defers their cross-library resolution to "the resolution feature" (08b out-of-scope), which predated
   08b and was never wired for SQL-sourced DDM names.

This feature makes **variable and reference navigation work end-to-end**, and in doing so **closes feature
08b's outstanding binding gap and open questions**. It is delivered in **two phases** so the highest-value
same-file navigation ships first:

- **Phase A — same-file variable navigation** (Stories 1–3). A local variable's declaration and all its
  uses are intra-file, so this needs **no cross-file resolution and no workspace usage index** — only a
  variable **use-site scanner** (the net-new work) plus a same-file name→declaration matcher, served
  store-first from the open buffer like the other providers.
- **Phase B — cross-file & host-var/DDM binding** (Stories 4–6). Bind (i) variables declared in external
  data areas (`LOCAL/PARAMETER/GLOBAL USING <.NSL/.NSA/.NSG>`), (ii) SQL **host variables** → their
  `DEFINE DATA` field, and (iii) SQL-sourced **DDM table names** → their `.NSD`, all through the existing
  **steplib chain** (data-area/DDM member names resolve exactly like CALLNAT targets). This is where the
  08b deferral lands.

**Two resolution kinds are involved (important distinction):** locating an *object* (a data area `.NSL`/…,
or a DDM `.NSD`) reuses feature 07's steplib-**chain** machinery unchanged; resolving a *field within* the
current object or a resolved data area is a **new intra-object name→`DataDefinition` lookup** (there is no
"field resolution" today — feature 07 resolves module edges, not fields).

**Model/cache impact:** to land go-to-definition **precisely on the name token**, `model.DataDefinition`
gains an additive **`NameRange`** (mirroring `DataAccessEntry.NameRange`); because `Definitions` is
persisted this bumps **`cacheFormatVersion` `0.7.0` → `0.8.0`** (one-time rebuild via the existing version
gate) — the first model/cache change since feature 24. Binding itself is **recomputed from cached
edges/refs** (per feature 07 ADR/OQ-1, resolution is not persisted), so Phase B adds **no further cache
bump**. Variable use-sites are computed **on demand from the buffer** (not persisted — Phase A). No new LSP
capability or method (extends the existing `textDocument/definition`/`references`/`hover` providers), so the
locked `TestInitialize` allow-list is unchanged.

## Research findings (grounded; `.claude/knowledge/natural/data-definition.md`, features 07/08b as-built)

**Variable scoping (what "the definition" is).** Bare `#CUSTOMER-NAME` → its `DEFINE DATA` line;
group-qualified `#GROUP.FIELD` → the sub-field within level-1 group `GROUP`; array `#T(1:10)`/`#T(I)` → the
declaration of `#T` (subscript stripped; an index var `I` is its own reference); a `REDEFINE` sub-field is
its own declaration. Identifiers are **case-insensitive**. Sigils: `#` user var; `+` AIV/independent; `&`
dynamic substitution (**dynamic → no static target**); `*` **system variable** (predefined, read-only →
**excluded**). **No lexical shadowing** — the DEFINE DATA namespace is flat per module and qualification
disambiguates; a valid module has ≤1 declaration per unqualified name path, and a genuinely ambiguous
unqualified reference → **surface all candidates** (mirror feature 07's flat-namespace `Ambiguous`).

**Cross-file declaration sources.** Only `LOCAL/PARAMETER/GLOBAL USING <name>` force cross-file lookup —
the fields live in a separate `.NSL`/`.NSA`/`.NSG` object; everything else (inline LOCAL/PARAMETER,
INDEPENDENT/CONTEXT/OBJECT) is in-file. **Data-area member names resolve through the same steplib chain as
CALLNAT** (a `USING` reference is a module reference). Caveats: GDA `WITH block` selects a sub-block (treat
whole-GDA fields as in scope for a first cut); PDA is bound positionally at the *call* site but by name
*inside* the referenced PDA (relevant to signature help, not to a field's own definition).

**Host-var / SQL-DDM state (feature 08b).** `HostVarRef{Name, Range}` is extracted (colon-stripped; from
`INTO`/`WHERE`/`VALUES`/`SET` and the `PROCESS SQL` `<<…>>` body). A native host-var binds to a `DEFINE
DATA` field like any variable use. A SQL table operand is a **`.NSD` DDM name in the same DDM namespace as
Adabas** — resolve it exactly like an Adabas DDM access. 08b's open questions this feature answers: whether
the DDM namespace accepts SQL-sourced names (yes — same namespace), and `:U:`/`:G:`/`:T:` direction (kept as
a plain reference; direction semantics stay out of scope — see below).

**Analyzer state.** Declarations: `DataDefinition{Name,Level,Type,Dimensions,SectionKind,Range,Children}` —
**no `NameRange`**. Use-sites: **not captured** for general statements (only SQL `HostVarRef`); the lexer
tokenizes everything with positions, so a token-occurrence scan (à la `scanOpaqueHostVars`) collects uses
without a parser rewrite. `#GROUP.FIELD` = 3 tokens, `#ARR(1)` = 4 tokens.

## User stories

### Phase A — same-file variable navigation

#### Story 1 — Go to definition of a variable (FR-54, refines FR-24)
**As a** developer, **I want** to jump from a variable reference to its `DEFINE DATA` declaration.

**Acceptance criteria:**
- [ ] "Go to Definition" on a variable **use-site** in a procedural statement navigates to its `DEFINE
      DATA` declaration in the **same file**, landing on the **name token** (`NameRange`).
- [ ] Invoking it **on the declaration** is idempotent (resolves to itself).
- [ ] Case-insensitive; **array subscripts stripped** (`#T(1:10)`, `#T(I)` → `#T`; index var `I` resolves
      to its own declaration); **group-qualified** `#GROUP.FIELD` → the sub-field in that level-1 group;
      **REDEFINE** sub-fields → their own line.
- [ ] Modeled gaps yield empty + no error (FR-17/FR-43): `*`-system var, `&`-dynamic name, undeclared name.
      Ambiguous unqualified name (in >1 group) → all candidate declarations offered.

#### Story 2 — Find references to a variable (FR-54, refines FR-25)
**As a** developer, **I want** all uses of a variable, so I know what a change affects.

**Acceptance criteria:**
- [ ] "Find References" on a variable (declaration or use) returns **all use-sites in the file** with
      precise ranges; declaration included/excluded per `includeDeclaration`.
- [ ] Complete w.r.t. the file's tokens (fixture-backed); never a false match inside a **comment** or
      **string literal**, a substring, or a `*`-system variable; group-qualified/subscripted occurrences
      count as references.

#### Story 3 — Declarations gain a precise name range (enabler)
**As a** maintainer, **I want** each `DEFINE DATA` field to carry the range of just its **name token**.

**Acceptance criteria:**
- [ ] Additive `model.DataDefinition.NameRange` (name-token span ⊆ `Range`), populated by the parser for
      fields, groups, REDEFINE sub-fields, and array fields.
- [ ] `cacheFormatVersion` bumped `0.7.0` → `0.8.0`; older cache rebuilds once (regression test).
- [ ] Feature 09's `SymbolDataField.SelectionRange` is set from `NameRange` (sharper outline/hover
      selection; no structural change).

### Phase B — cross-file & host-var/DDM binding

#### Story 4 — Variables declared in external data areas resolve cross-file (FR-54)
**As a** developer using `LOCAL/PARAMETER/GLOBAL USING <data-area>`, **I want** go-to-definition/references
on those fields to reach the data-area object.

**Acceptance criteria:**
- [ ] A variable whose declaration comes from a `USING <name>` data area resolves to the field's
      declaration **inside the referenced `.NSL`/`.NSA`/`.NSG`**, located via the **steplib chain**
      (current library → steplibs → SYSTEM, non-transitive — reusing feature 07).
- [ ] Find-references for such a field returns uses **in the current module** (workspace-wide usage
      indexing for shared GDA/AIV scopes remains deferred — see out of scope).
- [ ] Unresolved data area (outside the chain) or a field absent from it → empty, no error (FR-17). GDA
      `WITH block` sub-block selection is honored if present, else whole-GDA fields are in scope (OQ-2).

#### Story 5 — Host-variable navigation binds SQL host vars to declarations (completes FR-21; refines FR-24/25/28)
**As a** developer, **I want** go-to-definition, find-references, and hover to work on SQL **host
variables** (native clauses and `PROCESS SQL` bodies).

**Acceptance criteria:**
- [ ] Go-to-definition on a `HostVarRef` (bare or colon-prefixed native, or a `:host-var` in a `<<…>>`
      body) navigates to its `DEFINE DATA` declaration (same-file or via a `USING` data area — Story 4).
- [ ] Find-references includes host-var use-sites among a field's references; hover on a host var shows the
      field's interface (reusing feature 12's card).
- [ ] The `:U:`/`:G:`/`:T:` qualifier and `INDICATOR`/`LINDICATOR`/array notation are tolerated (stripped)
      when matching (as 08b already normalizes); an unbindable/dynamic host var → empty, no error.

#### Story 6 — SQL-sourced DDM table names resolve to their DDM (completes FR-19/FR-20; closes 08b OQ)
**As a** developer, **I want** the DDM tables referenced by native/flexible SQL to resolve like Adabas DDM
accesses.

**Acceptance criteria:**
- [ ] A SQL `FROM`/`INTO`/`INSERT INTO`/SQL-`UPDATE`/`DELETE` table operand (and the `PROCESS SQL`
      `ddm-name`) resolves to its `.NSD` DDM through the steplib chain — the **same DDM namespace** as
      Adabas `READ`/`FIND` — so go-to-definition/references on it behave identically to an Adabas view.
- [ ] The DDM-namespace resolution path is **shared** with feature 07 (no separate SQL path); a
      regression fixture proves an Adabas view and a SQL table with the same DDM name resolve identically.
- [ ] In-body (opaque `<<…>>`) table names remain pass-through text and are **not** bound (per 08b M-6);
      only the `ddm-name` operand binds.

## Phasing
Phase A (Stories 1–3) is **independently shippable** and delivers the highest-value everyday navigation;
Story 3's `NameRange` + `0.8.0` bump is its foundation. Phase B (Stories 4–6) builds on A and on feature
07's chain; it can be a second PR (or a second feature increment) if A proves large. Both phases share the
same LSP providers and the same intra-object field-lookup helper.

## Out of scope / deferred
- **Workspace-wide variable-usage indexing** for shared scopes — cross-*module* find-references of a GDA /
  AIV / CONTEXT variable used across many files. Phase B binds a use to its declaration cross-file, but
  enumerating *all uses across the workspace* of a shared variable needs a persisted usage index; deferred.
- **`:U:`/`:G:`/`:T:` read-vs-write direction semantics** for host vars (08b OQ) — capture/strip the
  qualifier; deriving precise read/write direction stays out (a later refinement).
- **Rename / rename-across-file** for variables.
- **Full parser widening** to model every statement's operands as AST — Phase A uses a token-occurrence
  scan behind a `VariableRef`-shaped seam; an accurate expression AST can replace it later without touching
  the providers.
- **Transaction edges** (`COMMIT`/`ROLLBACK`) — unrelated 08b deferral, stays out.

## Open questions (resolve at `/plan-feature`)
- **OQ-1 — reference-scanner impl.** Token-occurrence scan (recommended; robust on unparseable bodies,
  precedent `scanOpaqueHostVars`) vs. parser widening. Recommend the scan behind a `VariableRef` seam.
- **OQ-2 — data-area field extraction.** Are `.NSL`/`.NSA`/`.NSG` files already analyzed into
  `FileAnalysis.Definitions` (they contain `DEFINE DATA`-shaped content, so the existing extractor may
  already cover them), or do data-area exports need a parsing tweak (e.g. no program wrapper)? Confirm
  against as-built before Phase B; also settle GDA `WITH block` sub-block handling.
- **OQ-3 — field-resolution home.** The new intra-object name→`DataDefinition` lookup and the
  `USING`-data-area binding: live in `internal/workspace` (alongside `resolution.go`, reusing the chain) or
  in the analyzer? Recommend `internal/workspace` — it's cross-file binding, same layer as feature 07 —
  keeping the Analyzer seam clean.
- **OQ-4 — persist use-sites?** Phase A computes uses on demand (no cache growth). Phase B's host-var refs
  are already persisted (`HostVarRefs`, 0.5.0); variable use-sites for cross-module find-references would
  need persistence — deferred with the workspace usage index. Confirm only `DataDefinition.NameRange` is
  the persisted addition (single `0.8.0` bump).
- **OQ-5 — `findCursorTarget` integration.** Extend feature 10's cursor mapper to also return a variable /
  host-var use-site (lowest-priority tie-break after call/data targets), preserving smallest-containing
  behavior.
- **OQ-6 — comment/string exclusion & qualified spans.** Scanner must exclude `*`/`**`/`/*` comments and
  string contents; a `#GROUP.FIELD` reference (3 tokens) is one logical reference with the cursor on either
  part resolving correctly.

## Notes
- **First model/cache-format change since feature 24** (`0.7.0` → `0.8.0`, additive `DataDefinition.NameRange`
  only). Binding/resolution is recomputed from cached edges/refs (feature 07 OQ-1) → no extra cache bump.
- No new LSP capability/method — extends `textDocument/definition`/`references`/`hover`; json/v2 marshaling
  (feature 19); providers stay store-first (features 10–13).
- **Closes feature 08b's binding deferral and its OQs** (DDM-namespace reuse; host-var direction) — reference
  this plan from the 08b follow-up note when it lands.
- Testing: sanitized fixtures under `internal/analysis/natural/testdata/` (a program with an inline
  `DEFINE DATA LOCAL` incl. a group/REDEFINE/array used across `MOVE`/`IF`/`WRITE`/`CALLNAT` + a
  `*`-system-var and comment/string decoy) and, for Phase B, a multi-file set (a module + a `USING` `.NSL`
  and a `.NSD`, plus a SQL `SELECT`/`PROCESS SQL` with host vars) proving cross-file binding through a
  library map. Fuzz the scanner (`FuzzExtractVariableRefs`) and any new resolution entry (never panic — FR-43).
