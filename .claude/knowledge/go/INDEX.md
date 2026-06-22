# Go Knowledge Base — Index

Working reference on the Go language, standard library, and ecosystem **as they bear on
`natural-lsp`**, maintained by the `go-expert` agent. This complements the `go-development` skill: the
skill is *how we write Go here* (prescriptive); this KB is *verified facts about Go* (reference). Read
this index first, then the relevant topics.

**Status legend:** `verified (date)` = corroborated against an authoritative source · `needs-verification`
= seeded belief, confirm before relying on it · `unverified` = recorded but unconfirmed.

## Topics

| File | Covers | Overall status |
|------|--------|----------------|
| [go-version-and-tooling.md](go-version-and-tooling.md) | Go 1.26, build/test/vet, modules, build tags, `-race` | verified (2026-06-21) |
| [regexp-and-extraction.md](regexp-and-extraction.md) | `regexp` (RE2): capabilities, limits, multiline, perf; testing the extractor (fuzzing, golden files, test doubles) | verified (2026-06-20) |
| [concurrency-primitives.md](concurrency-primitives.md) | context, sync, channels, errgroup, race detector | verified (2026-06-20) |
| [stdlib-for-lsp-server.md](stdlib-for-lsp-server.md) | stdio framing, encoding/json, signals, slog; json/v2 (experimental) | verified (2026-06-20) |
| [lsp-go-ecosystem.md](lsp-go-ecosystem.md) | Go LSP/JSON-RPC libraries and trade-offs; LSP 3.18 coverage; **`go.lsp.dev/protocol` pulls json/v2 transitively** | verified (2026-06-21) |
| [filesystem-and-watching.md](filesystem-and-watching.md) | WalkDir, io/fs, fsnotify, content hashing | verified (2026-06-20) |
| [config-and-toml.md](config-and-toml.md) | `pelletier/go-toml/v2` (TOML decoder, strict mode); `go mod tidy` retaining a not-yet-imported dep via the `tools`-tag blank-import pattern | verified (2026-06-21) |

## Open questions (to verify on next relevant task)

- **`go.lsp.dev/protocol` json/v2 acceptability (NEW, 2026-06-21)** — now that we know v1.0.0 pulls the
  experimental `github.com/go-json-experiment/json` in as a runtime dependency, the open question is a
  *policy* one for the ADR: is taking an unstable third-party json/v2 (breaking changes, govulncheck
  exposure) acceptable, or does it tip the LSP decision to the hand-rolled Option B? Needs a
  project-owner call, not further Go-fact verification. See `lsp-go-ecosystem.md`.
- **Determinism of extraction output ordering** — golden-file tests assume a stable serialization. It
  is not yet decided whether the extractor emits symbols/edges in a guaranteed order or whether tests
  must sort. Settle this when the extraction output model lands (affects whether `cmpopts.SortMaps`/
  `SortSlices` is needed). See the testing section of `regexp-and-extraction.md`.

## Decisions now ready to make

- **LSP/JSON-RPC dependency:** maintenance AND spec coverage are verified — `go.lsp.dev/protocol`
  implements **LSP 3.18** and covers every method this project needs. **Revised 2026-06-21:** the
  recommendation is no longer a clean "Option A by default." v1.0.0 transitively depends on the
  experimental json/v2 library (`github.com/go-json-experiment/json`, "do not depend on this in
  publicly available modules") and declares `go 1.26`. That contradicts this project's standing
  json/v2-avoidance decision and forces the module floor to ≥ 1.26. Option A (depend on
  `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2`, both v1.0.0) and Option B (hand-rolled JSON-RPC on
  stable `encoding/json`, optionally `sourcegraph/jsonrpc2` for transport) are now genuinely balanced;
  the json/v2 transitive pull is the deciding factor for the ADR. `sourcegraph/go-lsp` is archived;
  `tliron/glsp` is pre-1.0 and framework-heavy. See `lsp-go-ecosystem.md`.
- **`encoding/json/v2`:** decided — **do not adopt.** Verified still experimental and
  `GOEXPERIMENT=jsonv2`-gated in Go 1.26; adopting it would break default-toolchain builds and pin an
  unstable API. Use `encoding/json`. See `stdlib-for-lsp-server.md`.

## Resolved (2026-06-20)

- Multi-line Natural statements: use a **logical-line pre-pass**, not the `s` flag alone (RE2 has no
  lookaround to bound multi-line constructs). See `regexp-and-extraction.md`.
- Cache-key hash: **deterministic** `crypto/sha256` (default) or `hash/fnv`; **never** `hash/maphash`
  (random per-process seed, not stable across runs). See `filesystem-and-watching.md`.
- fsnotify recursive watching: no native support — `Add` each dir and newly-created dirs explicitly.
- Go 1.26 is released (2026-02-10); nothing in it is obviously a hard dependency. See
  `go-version-and-tooling.md`.
- `encoding/json/v2`: still experimental / `GOEXPERIMENT=jsonv2`-gated in Go 1.26 → do not adopt.
  See `stdlib-for-lsp-server.md`.
- `go.lsp.dev/protocol` targets **LSP 3.18** and covers all needed methods → dependency ADR
  unblocked. See `lsp-go-ecosystem.md`.

## Changelog

- 2026-06-22 — FR-34 (external file watching) prep. Two topics updated, both verified against the
  module cache / repo:
  - `lsp-go-ecosystem.md`: documented the exact `go.lsp.dev/protocol` v1.0.0 watched-files types
    (`DidChangeWatchedFilesParams`/`FileEvent`/`FileChangeType` for A2; `RegistrationParams`/
    `Registration`/`DidChangeWatchedFilesRegistrationOptions`/`FileSystemWatcher`/`WatchKind`/
    `GlobPattern`/`Pattern` for dynamic registration). Key fact: **there is no static
    ServerCapabilities flag** for didChangeWatchedFiles — the spec only supports dynamic
    `client/registerCapability`, gated on the client's `DynamicRegistration=true`. Also noted the
    `internal/server` hand-rolled single-reader dispatch loop complicates outbound registration.
  - `filesystem-and-watching.md`: **correction** — fsnotify is NOT yet in go.mod/go.sum/module cache
    (contradicts CLAUDE-context); add via `go get` when first imported. Added the concrete recursive
    watch recipe, Op-bitmask gotcha, and bgCtx/`Close()`/drain lifecycle pattern.
- 2026-06-21 — Full verification sweep (go-improve). Headline finding in `lsp-go-ecosystem.md`:
  - **Resolved the `go.lsp.dev/protocol` transitive-footprint open question — and found a json/v2
    dependency.** v1.0.0's `go.mod` declares `go 1.26` and has 4 direct requires; the package imports
    only 4 third-party packages. But two of them are `github.com/go-json-experiment/json` +
    `/jsontext` — the experimental json/v2 module ("do not depend on this in publicly available
    modules") — and the generated types serialize via its `MarshalJSONTo`/`UnmarshalJSONFrom` API.
    This is a runtime dependency (builds with a default toolchain — it's the standalone module, not
    the `GOEXPERIMENT=jsonv2` stdlib gate), but it's an unstable third-party API. Consequence: choosing
    Option A pulls json/v2 in transitively, contradicting the project's "do not adopt json/v2"
    decision and weakening Option A's low-risk framing. Cross-referenced into
    `stdlib-for-lsp-server.md`. The old open question is replaced by a *policy* open question.
  - **Module `go`-directive max rule (new fact in `go-version-and-tooling.md`):** since Go 1.21 a
    module's `go` directive must be ≥ that of every dependency. So adopting `go.lsp.dev/protocol`
    (`go 1.26`) pins the project floor to ≥ 1.26 — the "could lower to 1.25" option only survives under
    the hand-rolled Option B. Source: go.dev/ref/mod.
  - **Versions re-confirmed, no drift:** Go 1.26.4 (2026-06-02) still latest, no 1.27 yet; fsnotify
    v1.10.1, x/sync v0.21.0, go-toml/v2 v2.4.0, go.lsp.dev/protocol v1.0.0 all unchanged.
  - No facts were found to be wrong on re-check; the regexp, concurrency, filesystem, and config
    topics needed no changes this sweep.
- 2026-06-21 — T0 of feature 01: adopted `github.com/pelletier/go-toml/v2 v2.4.0` (project's first
  third-party dep; ADR-013). New `config-and-toml.md` records the decoder facts (strict mode via
  `DisallowUnknownFields`, BurntSushi/toml unmaintained) and the verified `go mod tidy` retention
  pattern (a `//go:build tools` blank import keeps an unimported require through tidy while staying
  out of normal builds).
- 2026-06-20 — Second sweep: closed both prior open questions and folded in three routed testing items.
  - **json/v2 resolved (`unverified` → verified):** `encoding/json/v2` is still experimental in Go
    1.26, exists only under `GOEXPERIMENT=jsonv2`, and its godoc says "most users should use
    encoding/json." Decision recorded: do not adopt. (`stdlib-for-lsp-server.md`)
  - **LSP spec coverage resolved:** `go.lsp.dev/protocol` README states **LSP 3.18**; godoc confirms
    WorkspaceSymbol, DocumentSymbol, CodeLens, and WorkDoneProgress types — all needed methods
    covered. Dependency ADR unblocked. (`lsp-go-ecosystem.md`)
  - **Routed item 1 — native fuzzing:** added to the new "Testing the extraction backend" section of
    `regexp-and-extraction.md`. Verified Go 1.18+, `FuzzXxx(*testing.F)`, `f.Add`, `f.Fuzz`,
    `go test -fuzz`, `testdata/fuzz/{FuzzName}/{hash}` layout, `$GOCACHE/fuzz` for generated entries
    (go.dev/security/fuzz, pkg.go.dev/testing#F).
  - **Routed item 2 — golden files:** same section. Verified `-update` is a community convention
    (not a testing-package feature), `cmp.Diff` from `github.com/google/go-cmp/cmp` (v0.7.0,
    2025-01-14, test-only), and the sort-before-serialize / `cmpopts.SortMaps` fix for map-iteration
    flakiness.
  - **Routed item 3 — fake vs. generated mock:** added as a "Test doubles" subsection, framed around
    the `analysis.Analyzer` consumer-defined seam: prefer hand-written fakes for small interfaces,
    reserve generated mocks for large interfaces or genuine call-sequence assertions.
- 2026-06-20 — Full verification sweep; all six topics promoted to `verified (2026-06-20)`.
  - regexp: confirmed RE2 linear-time guarantee, no backreferences/lookaround, `i`/`m`/`s`/`U` flag
    semantics, 1000-count repetition cap, concurrency-safe except config methods (`Longest`). Added
    the resolved logical-line pre-pass guidance for multi-line statements.
  - lsp-ecosystem: verified versions via the Go module proxy — `go.lsp.dev/protocol` and
    `go.lsp.dev/jsonrpc2` both at **v1.0.0** and maintained; `sourcegraph/go-lsp` **archived**;
    `tliron/glsp` v0.2.2 (pre-1.0); gopls protocol types are `internal/` (not importable). Marked the
    dependency decision ready (recommend Option A).
  - concurrency: verified `errgroup.WithContext`/`SetLimit`/`TryGo` (errgroup v0.21.0) and
    `signal.NotifyContext`; noted errgroup propagates only the first error (collect all for FR-43).
  - stdlib: verified `encoding/json` unknown-field/case handling, `slog` to stderr, `NotifyContext`;
    left `encoding/json/v2` **unverified** (not in the 1.26 notes page).
  - filesystem: **correction** — `hash/maphash` is unsuitable for persisted cache keys (random
    per-process seed); use `crypto/sha256` or `hash/fnv`. Confirmed fsnotify v1.10.1 has no native
    recursive watch. Resolved both prior open questions.
  - version: confirmed Go 1.26.0 (2026-02-10), 1.26.4 (2026-06-02), and 1.26 notable features.
- 2026-06-20 — (seed) Created index and six topic stubs from project knowledge and the README/PRD.