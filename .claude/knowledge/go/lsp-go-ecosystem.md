# Go LSP / JSON-RPC ecosystem

**Status:** verified (2026-06-21) — versions/maintenance confirmed via the Go module proxy
(`proxy.golang.org/<module>/@latest`) and the GitHub API. **Major update (2026-06-21):** enumerated
`go.lsp.dev/protocol`'s transitive dependencies (the prior open question) and found it now depends on
the **experimental json/v2 library** (`github.com/go-json-experiment/json`) at runtime — a
design-relevant caveat that changes the Option A recommendation. See "Transitive footprint" below.

## Candidate libraries (verified versions as of 2026-06-20)

- **`go.lsp.dev/protocol`** (repo `github.com/go-language-server/protocol`) + **`go.lsp.dev/jsonrpc2`**
  (repo `github.com/go-language-server/jsonrpc2`) — Go types for LSP structures plus a JSON-RPC 2.0
  layer. **Both reached `v1.0.0`** (proxy reports tag `refs/tags/v1.0.0` for each; jsonrpc2 v1.0.0 and
  protocol v1.0.0). Prior tags went v0.11.x → v0.12.0 → v1.0.0, so this is a freshly stabilized 1.0.
  Actively maintained. Best fit for "maintained types + JSON-RPC without a full framework."
  - **Spec coverage (verified 2026-06-20):** the repo README states it is "a fast, spec-faithful
    implementation of the Language Server Protocol **3.18** for Go — types generated from the official
    LSP meta-model." 3.18 is a superset of 3.17, so the older client/editor capability negotiations
    still apply. The godoc index confirms types for every method this project needs:
    `workspace/symbol` (WorkspaceSymbol/WorkspaceSymbolParams), `textDocument/documentSymbol`
    (DocumentSymbol/DocumentSymbolParams + `DocumentSymbolSlice`), `textDocument/codeLens`
    (CodeLens/CodeLensParams/CodeLensOptions), and work-done progress (WorkDoneProgress* types),
    plus definition/references/hover and publishDiagnostics. This resolves the prior open question
    on spec/method coverage — the dependency ADR is unblocked on this point.
  - **Transitive footprint (verified 2026-06-21 — resolves the prior open question):** at v1.0.0 the
    package's own `go.mod` declares `go 1.26` and just **four** direct requires:
    `github.com/go-json-experiment/json` (a pseudo-version, e.g. `v0.0.0-20260601182631-...`),
    `github.com/google/go-cmp v0.7.0` (test-only), `go.lsp.dev/jsonrpc2 v1.0.0`, and
    `go.lsp.dev/uri v1.0.0`. The package *itself* imports only 4 non-stdlib packages:
    `go.lsp.dev/uri`, `go.lsp.dev/jsonrpc2`, `github.com/go-json-experiment/json`, and
    `github.com/go-json-experiment/json/jsontext`. So the runtime tree is small — but see the
    json/v2 caveat next; the "22 imports" figure cited in the old open question was the package's
    *total* import count (stdlib + third-party), not a transitive-module weight concern.
  - **⚠ Depends on the experimental json/v2 library (verified 2026-06-21):** v1.0.0's serialization is
    built on `github.com/go-json-experiment/json` — the upstream experimental implementation of the
    proposed `encoding/json/v2`. Its generated types expose the json/v2 API surface
    (`MarshalJSONTo(enc)` / `UnmarshalJSONFrom(dec)` methods, confirmed on pkg.go.dev). That library's
    own README warns: *"The API is unstable and breaking changes will regularly be made. Do not depend
    on this in publicly available modules."* This **directly tensions** the project's standing
    decision to NOT adopt json/v2 (see `stdlib-for-lsp-server.md`): choosing Option A pulls the
    experimental json/v2 in **transitively** as a hard runtime dependency, with breaking-change risk
    on every upgrade and govulncheck/dependency-policy friction (cf. chromedp issue #1595, which
    removed go-json-experiment for govulncheck failures). Note this is the standalone
    `go-json-experiment/json` *module*, not the `GOEXPERIMENT=jsonv2` stdlib gate — so it does build
    with a default toolchain, but it remains an unstable third-party API.
- **`github.com/tliron/glsp`** — an LSP server framework/scaffold. Latest tag **v0.2.2 (2024-03-09)**;
  repo had non-tagged commits through mid-2025. Pre-1.0; supports LSP **3.16 and 3.17**
  (`protocol_3_16`, `protocol_3_17` packages). Provides JSON-RPC over stdio/TCP/WebSocket/Node IPC.
  Heavier than this project needs if only a handful of methods are implemented.
- **`github.com/sourcegraph/go-lsp`** — **ARCHIVED (read-only since 2024-02-23). Do not adopt.**
  Older, partial type coverage; superseded.
- **`github.com/sourcegraph/jsonrpc2`** — still maintained (commits into 2026-06); a minimal,
  battle-tested JSON-RPC 2.0 layer (used by sourcegraph tooling). Viable if pairing a hand-rolled
  protocol-types package with a maintained transport.
- **`gopls`** (`golang.org/x/tools/gopls`) — the official Go language server. Its internal
  `golang.org/x/tools/internal/lsp/protocol` types are **`internal/`** and therefore **not
  importable**. Treat gopls as a **reference implementation to learn from**, not a dependency.

## Decision lens for this project

- The PRD wants a small, dependency-light binary with a replaceable analysis backend; a heavy LSP
  framework may not pay off if only a handful of methods are implemented
  (definition/references/hover/documentSymbol/workspace-symbol/codeLens/diagnostics/progress).
- Option A: depend on a maintained `protocol` types package + JSON-RPC lib. Option B: hand-roll
  minimal JSON-RPC + only the message types used. Record the chosen trade-off as an ADR in the
  software-engineering KB.

## Recommendation (now that maintenance is verified)

- The decision is **ready to make**, but the json/v2 finding above **weakens Option A's "low-risk
  default" framing** — weigh it explicitly in the ADR. Two defensible paths:
  - **Option A (lean on `go.lsp.dev`):** depend on `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2`
    (both v1.0.0). Lowest *implementation* cost and maintained, BUT it pulls
    `github.com/go-json-experiment/json` (experimental json/v2, "do not depend on this in publicly
    available modules") in transitively as a runtime dependency. That contradicts the project's
    decision to avoid json/v2 and adds breaking-change/govulncheck exposure on upgrades. Acceptable
    only if you accept that transitive risk; otherwise it argues *for* Option B.
  - **Option B (hand-roll):** implement minimal JSON-RPC 2.0 framing yourself (it is small — see
    `stdlib-for-lsp-server.md`) and either hand-write only the LSP message types used, or pair a
    hand-written types file with `github.com/sourcegraph/jsonrpc2` for transport. Maximizes control,
    minimizes the dependency surface, and **keeps the project on stable `encoding/json`** (no
    transitive json/v2). Costs more code and ongoing spec-tracking. Given the project's small-footprint
    goal *and* its explicit json/v2 avoidance, Option B is now at least as attractive as Option A —
    the json/v2 transitive pull is the deciding factor the ADR should call out.
- Either way the choice lives **behind the `internal/server` boundary** and must not leak into
  `internal/analysis`/`internal/model`. Record the final pick as an ADR in the software-engineering
  KB. `tliron/glsp` is not recommended (pre-1.0, framework-heavy for our method set).

## `workspace/didChangeWatchedFiles` types in `go.lsp.dev/protocol` v1.0.0 (verified 2026-06-22)

Confirmed against the module cache (`go.lsp.dev/protocol@v1.0.0`):

- **A2 (client-pushed events)** notification: method `workspace/didChangeWatchedFiles`, params
  `DidChangeWatchedFilesParams{ Changes []FileEvent }`. `FileEvent{ URI uri.URI; Type FileChangeType }`.
  `FileChangeType` (uint32): `FileChangeTypeCreated=1`, `FileChangeTypeChanged=2`, `FileChangeTypeDeleted=3`.
- **Static capability:** there is **no** `ServerCapabilities` field that statically advertises
  watched-files interest — the LSP spec only supports dynamic registration for this method.
  `ServerCapabilities.Workspace *WorkspaceOptions` covers workspace folders / file operations, not
  didChangeWatchedFiles. So A2 must use **dynamic registration** (`client/registerCapability`).
- **Dynamic registration (A2):** send an outbound `client/registerCapability` request after
  `initialized`, with `RegistrationParams{ Registrations: []Registration{{ ID: "<uuid>",
  Method: "workspace/didChangeWatchedFiles", RegisterOptions: <DidChangeWatchedFilesRegistrationOptions> }} }`.
  `DidChangeWatchedFilesRegistrationOptions{ Watchers []FileSystemWatcher }`;
  `FileSystemWatcher{ GlobPattern GlobPattern; Kind WatchKind }`.
  `WatchKind` (uint32, bitmask): `WatchKindCreate=1`, `WatchKindChange=2`, `WatchKindDelete=4`
  (omitted ⇒ 7 = all). `GlobPattern` is a union interface: a bare `Pattern` (`type Pattern string`,
  e.g. `Pattern("**/*.{NSP,NSN,NSS,...}")`) or `*RelativePattern{ BaseURI, Pattern }`.
- **Client capability gate:** only register if the client advertised
  `ClientCapabilities.Workspace.DidChangeWatchedFiles` with `DynamicRegistration=true`
  (`DidChangeWatchedFilesClientCapabilities`). Capture this from `InitializeParams` and skip
  registration if absent — otherwise A2 silently does nothing and A1 (fsnotify) carries the load.
- **This server hand-rolls dispatch.** `internal/server` does NOT implement the generated
  `protocol.Server` interface; it reads `jsonrpc2.Call`/`jsonrpc2.Notification` off a
  `jsonrpc2.HeaderStream` and `switch`es on `Method()`. Consequences for FR-34:
  - To accept A2: add `case "workspace/didChangeWatchedFiles":` to the notification switch, decode
    `DidChangeWatchedFilesParams`, feed events to the same coalescer as A1.
  - To register A2: build the request via `jsonrpc2.NewCall(id, "client/registerCapability", params)`
    and `stream.Write(ctx, ...)`, then read the matching response — but the current loop has a single
    reader, so a clean outbound request/response needs care (either fire-and-forget the registration
    notification-style and tolerate the client's response arriving in the loop, or refactor to a
    `jsonrpc2.Conn` with a handler). Simplest robust path given the current architecture: prefer A1
    as the primary watcher and treat A2 as best-effort.

## Sources

- Module proxy (authoritative version/tag data, verified 2026-06-20):
  - `https://proxy.golang.org/go.lsp.dev/protocol/@latest` → v1.0.0
  - `https://proxy.golang.org/go.lsp.dev/jsonrpc2/@latest` → v1.0.0
  - `https://proxy.golang.org/github.com/tliron/glsp/@latest` → v0.2.2 (2024-03-09)
- LSP spec/method coverage (verified 2026-06-20):
  - https://github.com/go-language-server/protocol (README: "Language Server Protocol 3.18")
  - https://pkg.go.dev/go.lsp.dev/protocol (godoc index: WorkspaceSymbol, DocumentSymbol, CodeLens,
    WorkDoneProgress types present)
- Transitive footprint / json/v2 dependency (verified 2026-06-21):
  - https://github.com/go-language-server/protocol/blob/main/go.mod (`go 1.26`; requires
    `github.com/go-json-experiment/json`, `github.com/google/go-cmp v0.7.0`, `go.lsp.dev/jsonrpc2
    v1.0.0`, `go.lsp.dev/uri v1.0.0`)
  - https://pkg.go.dev/go.lsp.dev/protocol?tab=imports (3 third-party package imports:
    `go.lsp.dev/uri`, `go.lsp.dev/jsonrpc2`, `github.com/go-json-experiment/json` + `/jsontext`)
  - https://github.com/go-json-experiment/json/blob/master/README.md ("API is unstable… Do not
    depend on this in publicly available modules"; mirror of the upstream Go json/v2 experiment)
  - https://pkg.go.dev/go.lsp.dev/protocol (generated types expose `MarshalJSONTo`/`UnmarshalJSONFrom`
    — the json/v2 method set)
- GitHub API: `sourcegraph/go-lsp` `archived: true` (pushed 2024-02-23); `sourcegraph/jsonrpc2`
  active (pushed 2026-06).
- gopls protocol types are under `golang.org/x/tools/internal/` (not importable) — Go import rules
  for `internal/` packages: https://go.dev/doc/go1.4#internalpackages