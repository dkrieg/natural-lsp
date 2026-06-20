# Feature: Object-type recognition

**Status:** Planned
**PRD requirements:** FR-7, FR-8, FR-9
**Priority / phase:** P0 (core types) · P2 (extended types)
**Depends on:** [01 Workspace & configuration](01-workspace-and-configuration.md)

## Summary

Every indexed file is classified into the Natural construct it represents, based on its object type.
This mapping is foundational: features such as INCLUDE resolution, map handling, and data-area lookup
depend on the right files being recognized as the right kind of object. The indexed extension set must
stay in lockstep with the features that consume each type.

## User stories

### Story 1 — Classify core object types (FR-7)
**As a** developer, **I want** each Natural source file recognized as its correct construct **so that**
navigation and resolution operate on the right objects.

**Acceptance criteria:**
- [ ] The server classifies, at minimum: program, subprogram, external subroutine, copycode (INCLUDE
      target), map, local data area, global data area, parameter data area, helproutine, and DDM.
- [ ] Classification is driven by object type and normalizes case (object types are recognized
      regardless of letter case).
- [ ] A file whose type is recognized but whose contents are unreadable/malformed is classified and
      skipped gracefully (does not abort indexing — see [03](03-server-lifecycle-and-protocol.md)).
- [ ] Each classification is backed by a fixture under `testdata/` demonstrating the mapping.

### Story 2 — Keep the indexed set consistent with consumers (FR-9)
**As a** maintainer, **I want** the set of indexed types to match the features that need them **so
that** a feature never silently lacks its inputs.

**Acceptance criteria:**
- [ ] For every feature that consumes a given object type (e.g. INCLUDE → copycode, data-area
      lookups → LDA/GDA/PDA), that type is in the default indexed set.
- [ ] Adding a feature that requires a new type is gated on that type being indexable and documented.
- [ ] A file with an unrecognized/unconfigured extension is ignored without error and the fact is
      observable in logs.

### Story 3 — Extended object types (FR-8)
**As a** developer on a codebase using less common objects, **I want** additional object types
recognized **so that** they appear in the index when relevant.

**Acceptance criteria:**
- [ ] Additional types (e.g. class, function, dialog, adapter, text) can be recognized and classified.
- [ ] Enabling an extended type does not change behavior for codebases that don't use it.
- [ ] Each added type ships with a fixture and is reflected in the documented default/optional sets.

## Out of scope
- The concrete extension → type table values (maintained in the knowledge base and config defaults).
- Function *call* semantics for user-defined functions — see plan 06 open questions.

## Open questions
- Which extended types (FR-8) are in-scope for the first stable release vs. deferred.
- Whether any object type needs sub-classification (e.g. distinguishing inline maps from `.NSM`).
