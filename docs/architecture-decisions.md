# Architecture Decision Records

This log records significant, hard-to-reverse technical decisions for `natural-lsp`:
the choice made, the alternatives considered, the rationale, and any constraints the
decision imposes on the code.

> **Note on numbering.** The feature plans under `docs/plans/` cite several decisions
> inline as "ADR-001" … "ADR-012" (e.g. CALLS_DYNAMIC as a modeled outcome, the
> model/config/resolution separation, steplib search order). Those are conceptual
> references the planners use as shorthand; the formal records below are being
> backfilled. To avoid colliding with that reserved range, formal records added now
> start at **ADR-013**. Earlier numbers will be backfilled with the decisions the plans
> already reference.

---

## ADR-013 — Use pelletier/go-toml/v2 for TOML configuration parsing

- **Status:** Accepted (2026-06-21)
- **Context:** CR-1 and the README config schema commit the project to a
  `.natural-lsp.toml` workspace-configuration file. The Go standard library cannot
  parse TOML, so a third-party decoder is required. This is the project's first
  non-stdlib dependency, so it needs an explicit, recorded decision.

### Decision

Adopt `github.com/pelletier/go-toml/v2` (added at v2.4.0) as the TOML decoder.

### Alternatives considered

- **`github.com/BurntSushi/toml`** — the long-time de facto Go TOML library. Rejected:
  it is widely reported as no longer actively maintained, and migration away from it
  toward go-toml/v2 is a documented community trend. Its API also predates the cleaner
  v2 surface below.

### Rationale

- **Strict spec compliance.** go-toml/v2 offers strict decoding via
  `Decoder.DisallowUnknownFields()`, which returns a `StrictMissingError` that points at
  the offending line. This directly serves the config requirements: unknown/typo'd keys
  in `.natural-lsp.toml` become an actionable error rather than a silently ignored
  setting — consistent with the project's "recoverable failures are observable, never
  silent" principle.
- **Cleaner v2 API.** The v2 `Marshal`/`Unmarshal` + `Encoder`/`Decoder` surface is
  simpler and more idiomatic than v1 / BurntSushi.
- **Active maintenance & compatibility.** v2.4.0 was published 2026-06-16; the library
  supports the last two major Go releases, covering the project's `go 1.26` floor.

### Constraint (the seam)

The TOML decoder import is **confined to `internal/config`**. It must never be imported
by `internal/model` or `internal/analysis` (nor anything behind the `analysis.Analyzer`
seam). Configuration is a process-bootstrap / LSP-facing concern consumed by
`cmd/natural-lsp`, `internal/server`, and (later) `internal/workspace`; the model and the
extraction backend stay free of config/parser internals. `internal/config/config.go`
imports the decoder directly; no tools-build-tag anchor file is needed.
