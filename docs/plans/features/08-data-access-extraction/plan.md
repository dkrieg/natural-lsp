# Feature: Data-access extraction

**Status:** Planned
**PRD requirements:** FR-19, FR-20, FR-21, FR-22
**Priority / phase:** P0 (read relationships) · P1 (write relationships, data definitions) ·
P2 (work files)
**Depends on:** [00-parser-foundation](../00-parser-foundation/plan.md)

## Summary

Extracts how each object touches data: which files/DDMs it reads and writes, and what it declares in
its data-definition blocks (including parameter interfaces). This feeds [hover](../11-hover/plan.md),
[outline](../10-document-outline/plan.md), and references. Physical Adabas/IMS metadata is out of scope —
extraction is structural (names and relationships present in the source).

## User stories

### Story 1 — Read relationships (FR-19)
**As a** developer, **I want** to see what data an object reads **so that** I can trace data flow.

**Acceptance criteria:**
- [ ] Read-style data access produces a read relationship recording the accessed file/DDM name and the
      access site.
- [ ] The accessed name is normalized for case so it can be matched to a DDM object.
- [ ] A fixture per read-style construct demonstrates correct extraction.

### Story 2 — Write relationships (FR-20)
**As a** developer doing impact analysis, **I want** to see what data an object writes **so that** I
can assess change risk.

**Acceptance criteria:**
- [ ] Write-style data access produces a write relationship recording the file/DDM name and the access
      site.
- [ ] Read and write relationships to the same file/DDM are distinguishable.
- [ ] A fixture per write-style construct demonstrates correct extraction.

### Story 3 — Data definitions & parameter interfaces (FR-21)
**As a** developer, **I want** variable declarations and parameter interfaces extracted **so that**
outline and hover can show structure and signatures.

**Acceptance criteria:**
- [ ] Data-definition blocks (local, global, parameter, and related sections) are extracted as
      declared variables with their identifying attributes.
- [ ] Parameter interfaces are captured so they can back subroutine/module signatures in hover.
- [ ] Mandatory block terminators and ordering rules are respected such that a well-formed block is
      fully extracted; a malformed block degrades gracefully (no crash; see
      [13](../13-diagnostics/plan.md)).
- [ ] Fixtures cover each data-section kind.

### Story 4 — Work-file definitions (FR-22)
**As a** developer, **I want** work-file definitions extracted **so that** work-file usage is visible
in structure.

**Acceptance criteria:**
- [ ] Work-file definitions are extracted and associated with the declaring object.
- [ ] A fixture demonstrates extraction.

## Out of scope
- Resolving physical Adabas DDM metadata or IMS segment metadata beyond names present in source.
- **Embedded-SQL data access.** This feature covers Adabas-style data access (`READ`/`FIND`/`GET`/`STORE`/record-form `UPDATE`/`DELETE`) against DDMs. Native Natural SQL (`SELECT`/`INSERT`/SQL-form `UPDATE`/`DELETE`/`MERGE`/`PROCESS SQL`/`CALLDBPROC`) and its host variables are extracted by [08b-embedded-sql-extraction](../08b-embedded-sql-extraction/plan.md), which reuses this feature's DDM read/write edge model. (SQL table operands *are* DDM names, so the two share the DDM namespace — they differ only in which statements produce the edges.)
- Resolving the *physical* SQL table behind a DDM, or column-level DB metadata (out of PRD scope).

## Open questions
- Exact array-bound and redefinition grammar inside data-definition blocks — depth required for the
  first release.
- Whether field-level references (individual DDM fields) must be extracted for references/hover in the
  first release, or only file/DDM-level.
