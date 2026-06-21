# Go LSP / JSON-RPC ecosystem

**Status:** verified (2026-06-20) — versions/maintenance confirmed via the Go module proxy
(`proxy.golang.org/<module>/@latest`) and the GitHub API. One caveat: the sandbox's GitHub/proxy
mirror rewrote `go-language-server/*` commit timestamps to today's date, so for those two modules the
*tag version* (v1.0.0) is the trustworthy signal, not the timestamp. Other repos returned varied,
plausible dates and are fully trusted.

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

- The decision is **ready to make**. Two defensible paths:
  - **Option A (lean on `go.lsp.dev`):** depend on `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2`
    (both v1.0.0). Lowest implementation cost, maintained, and the 1.0 tag reduces churn risk. Best
    default unless the dependency footprint or its 22 transitive deps are a concern.
  - **Option B (hand-roll):** implement minimal JSON-RPC 2.0 framing yourself (it is small — see
    `stdlib-for-lsp-server.md`) and either hand-write only the LSP message types used, or pair a
    hand-written types file with `github.com/sourcegraph/jsonrpc2` for transport. Maximizes control
    and minimizes the dependency surface; costs more code and ongoing spec-tracking.
- Either way the choice lives **behind the `internal/server` boundary** and must not leak into
  `internal/analysis`/`internal/model`. Record the final pick as an ADR in the software-engineering
  KB. `tliron/glsp` is not recommended (pre-1.0, framework-heavy for our method set).

## Sources

- Module proxy (authoritative version/tag data, verified 2026-06-20):
  - `https://proxy.golang.org/go.lsp.dev/protocol/@latest` → v1.0.0
  - `https://proxy.golang.org/go.lsp.dev/jsonrpc2/@latest` → v1.0.0
  - `https://proxy.golang.org/github.com/tliron/glsp/@latest` → v0.2.2 (2024-03-09)
- LSP spec/method coverage (verified 2026-06-20):
  - https://github.com/go-language-server/protocol (README: "Language Server Protocol 3.18")
  - https://pkg.go.dev/go.lsp.dev/protocol (godoc index: WorkspaceSymbol, DocumentSymbol, CodeLens,
    WorkDoneProgress types present)
- GitHub API: `sourcegraph/go-lsp` `archived: true` (pushed 2024-02-23); `sourcegraph/jsonrpc2`
  active (pushed 2026-06).
- gopls protocol types are under `golang.org/x/tools/internal/` (not importable) — Go import rules
  for `internal/` packages: https://go.dev/doc/go1.4#internalpackages