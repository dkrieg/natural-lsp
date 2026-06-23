# Feature: Workspace & configuration

**Status:** Planned
**PRD requirements:** FR-1, FR-2, FR-3, FR-4 (config portion), FR-6; CR-1–CR-6
**Priority / phase:** P0 (root discovery, core config, defaults) · P1 (library map config)
**Depends on:** none (foundation)

## Summary

Establishes how the server finds a Natural codebase and how its behavior is configured. The workspace
root is located by a sentinel file, which also carries all configuration. Every setting has a
documented default so the server is usable with little or no explicit configuration. The library map
defined here is *consumed* by [call & dependency resolution](../07-call-dependency-resolution/plan.md).

## User stories

### Story 1 — Locate the workspace root (FR-1)
**As a** developer, **I want** the server to find my codebase root automatically **so that** I don't
have to configure paths per file.

**Acceptance criteria:**
- [ ] Given a file is opened, the server walks up parent directories looking for the sentinel
      configuration file and treats the first directory containing it as the workspace root.
- [ ] Given no sentinel file exists above the opened file, the server falls back to a documented
      default root (e.g. the editor-provided workspace folder) and continues to function.
- [ ] The resolved root is reported in server logs so it is observable.

### Story 2 — Run with zero or minimal configuration (FR-6, CR-2)
**As a** new user, **I want** sensible defaults **so that** I can start without learning every option.

**Acceptance criteria:**
- [ ] Given only an empty sentinel file, the server indexes the default object-type set and operates
      with all defaults applied.
- [ ] Every configurable value has a documented default, and the defaults are discoverable (docs
      and/or a generated sample config).

### Story 3 — Control what gets indexed (FR-2, FR-3, CR-3)
**As a** developer on a large repo, **I want** to control which files are indexed **so that** indexing
stays fast and relevant.

**Acceptance criteria:**
- [ ] The indexed object-type extension set is configurable; only matching files are indexed.
- [ ] Directory exclusions are honored; excluded directories are never read or indexed.
- [ ] A maximum indexable file size is enforced; files above it are skipped and the skip is reported
      (see [diagnostics](../13-diagnostics/plan.md) / logs), not silently dropped.
- [ ] The cache location is configurable (CR-3).

### Story 4 — Declare libraries and steplib order (FR-4, CR-5)
**As a** maintainer of a multi-library codebase, **I want** to map directories to Natural libraries
and declare each library's steplib search order **so that** module names resolve the way Natural
resolves them.

**Acceptance criteria:**
- [ ] Configuration can map one or more workspace directories to named Natural libraries.
- [ ] Each library can declare an ordered steplib list.
- [ ] The parsed library map is exposed to the resolver in the declared order (consumed in plan 06).
- [ ] Given no library map is declared, configuration loading still succeeds; the resolver then
      treats the workspace as a single flat namespace (behavior specified in plan 06 / FR-5).

### Story 5 — Configure analysis behavior (CR-4)
**As a** maintainer, **I want** to tune how dynamic calls are handled **so that** they're treated as
dependencies, not errors.

**Acceptance criteria:**
- [ ] A setting controls whether dynamic (variable-target) calls are treated as unresolved external
      dependencies vs. errors; default treats them as dependencies.
- [ ] Heuristic thresholds governing dynamic-call handling are configurable with documented defaults.

### Story 6 — Fail safe on bad configuration (CR-6)
**As a** user, **I want** clear feedback on misconfiguration **so that** a typo doesn't break the
server.

**Acceptance criteria:**
- [ ] Given invalid or partially invalid configuration, the server emits a clear, actionable message
      identifying the offending setting.
- [ ] Where a value is invalid, the server falls back to that value's default rather than refusing to
      start, when a safe default exists.
- [ ] Configuration is case-insensitive for Natural identifiers (library/module names) consistent
      with Natural semantics.

## Out of scope
- The resolution *algorithm* (steplib walk, ambiguity reporting) — see plan 06.
- The concrete file format/syntax of the configuration file (implementation detail).

## Open questions
- Default root behavior when multiple sentinel files exist on the path (nearest wins — confirm).
- Whether environment/CLI overrides of config are required for the first release.