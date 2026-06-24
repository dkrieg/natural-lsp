# LSP protocol essentials

**Status:** verified (2026-06-21) against the official **LSP 3.17** specification
(microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/). All facts below
were confirmed there unless explicitly noted otherwise. The position-encoding default and negotiation
sentences were re-confirmed verbatim against the live 3.17 spec on 2026-06-21 (see Sources).

## Base protocol (verified)

- **JSON-RPC 2.0 over a stream.** Each message is a header part + content part separated by `\r\n`.
  Each header field is `name: value` terminated by `\r\n`; the `Content-Length` header is **required**
  and gives the byte count of the content. Framing:
  `Content-Length: <bytes>\r\n\r\n<json>`. Headers are ASCII; content is UTF-8 by default.
- **Requests** carry an `id` and expect a response; **notifications** have no `id` and get none.

## Lifecycle (verified)

`initialize` (request, may be sent **only once**) → server returns its `capabilities` → `initialized`
(notification from client) → normal operation → `shutdown` (request; server stops processing further
requests) → `exit` (notification; server terminates). The server must not send requests/notifications
to the client before it has responded to `initialize`, and must advertise only capabilities it
actually supports.

## Position encoding (verified — highest-risk LSP detail)

- A `Position` is `{line, character}`, both **zero-based**. The `character` offset is measured in
  **UTF-16 code units by default**, NOT bytes and NOT runes/code points. Spec: "If the server omits
  the position encoding in its initialize result the encoding defaults to the string value `utf-16`."
  UTF-16 is the mandatory baseline every conforming client supports.
- **Negotiation (3.17):** the client advertises `general.positionEncodings` — an array of supported
  encodings (`utf-8` | `utf-16` | `utf-32`) in **decreasing preference order**. The server picks one
  and returns it as the **`positionEncoding`** field in `ServerCapabilities`. Spec: "If the client
  didn't provide any position encodings the only valid value that a server can return is `utf-16`."
- **Implication:** Natural source for this project is overwhelmingly ASCII/single-byte, so UTF-16 vs.
  UTF-8 columns coincide for the common case; they diverge only on lines containing non-ASCII (e.g.
  multibyte text in literals/comments). Mishandling still produces off-by-column ranges there. See
  ADR-008 in `architecture-decisions.md` for the decision (negotiate UTF-8 when offered, else UTF-16).

**Source:** LSP 3.17 specification, Section 5.3 (Initialize Params) and Section 6.1.1 (PositionEncodingKind):
- "Prior to 3.17 the offsets were always based on a UTF-16 string representation."
- "If the server omits the position encoding in its initialize result the encoding defaults to the string value `utf-16`."
- "The client announces it's supported encoding via the client capability `general.positionEncodings`."
- "If the value 'utf-16' is missing from the client's capability `general.positionEncodings` servers can safely assume that the client supports UTF-16."
- Verified 2026-06-23 against https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/

## Ranges (verified)

- A `Range` is `{start, end}`, **zero-based** line/character, **half-open** (end position is
  **exclusive**). Spec: "the end position is exclusive." Example spanning a line ending:
  `{start:{line:5,character:23}, end:{line:6,character:0}}`.

## Document synchronization (verified)

- Notifications: `textDocument/didOpen` / `didChange` / `didClose`.
- **`TextDocumentSyncKind`**: `None = 0`, `Full = 1`, `Incremental = 2`. Advertised via
  `textDocumentSync` in `ServerCapabilities`, which is either a `TextDocumentSyncKind` value or a
  `TextDocumentSyncOptions` object with fields `openClose?: boolean` and `change?: TextDocumentSyncKind`
  (plus save/willSave options not needed here).
- With `Full`, each `didChange` carries the entire new document text; with `Incremental`, it carries
  only the changed ranges (`TextDocumentContentChangeEvent` with a `range`). See ADR-009 for the
  project's choice (Full for first release).

## Capabilities to advertise, per implemented method (verified field names)

The PRD's feature set maps to these `ServerCapabilities` fields. Each may be a bare `boolean` or an
`*Options` object; advertise only when the method is actually implemented (phase-gated per feature
plan 03):

| Method (PRD)                  | ServerCapabilities field   | Type                              |
|-------------------------------|----------------------------|-----------------------------------|
| `textDocument/definition`     | `definitionProvider`       | `boolean \| DefinitionOptions`    |
| `textDocument/references`     | `referencesProvider`       | `boolean \| ReferenceOptions`     |
| `textDocument/hover`          | `hoverProvider`            | `boolean \| HoverOptions`         |
| `textDocument/documentSymbol` | `documentSymbolProvider`   | `boolean \| DocumentSymbolOptions`|
| `workspace/symbol`            | `workspaceSymbolProvider`  | `boolean \| WorkspaceSymbolOptions`|
| `textDocument/codeLens`       | `codeLensProvider`         | `CodeLensOptions` (object; `resolveProvider?: boolean`) |
| document sync                 | `textDocumentSync`         | `TextDocumentSyncKind \| TextDocumentSyncOptions` |
| position encoding             | `positionEncoding`         | `PositionEncodingKind` (chosen from client's offer) |

- **Diagnostics — push vs. pull (verified vs. LSP 3.17).** Two models exist:
  - **Push** (`textDocument/publishDiagnostics`): a **server→client notification, not a
    ServerCapabilities provider field** — there is no provider flag to set; the server simply
    publishes whenever it (re)computes diagnostics. This is what this project ships for v1 (ADR-014).
  - **Pull** (added in 3.17): the server advertises a **`diagnosticProvider`** capability
    (`DiagnosticOptions` / `DiagnosticRegistrationOptions`, with fields `identifier?: string`,
    `interFileDependencies: boolean`, `workspaceDiagnostics: boolean`) and answers
    **`textDocument/diagnostic`** (document pull) and, when `workspaceDiagnostics` is true,
    **`workspace/diagnostic`** requests. The motivation for pull is **client-controlled timing** —
    the client decides *when* and *for which documents* diagnostics are computed (e.g. only visible
    files), instead of the server pushing on its own schedule. A server may support **either or
    both**, but if it advertises `diagnosticProvider` it should not also push for the same documents
    (the client manages refresh via `workspace/diagnostic/refresh`). For `natural-lsp`'s
    re-extract-whole-file model and small objects, push is the simpler, sufficient choice (ADR-014).
**Source:** LSP 3.17 specification, Section 18 (Diagnostics):
- `DiagnosticOptions`: `{ interFileDependencies?: boolean, workspaceDiagnostics?: boolean, triggerKind?: number }`
- `textDocument/diagnostic`: Pulls diagnostics for a specific document
- `workspace/diagnostic`: Pulls diagnostics for documents in the workspace (when `workspaceDiagnostics: true`)
- "The either/or rule": A server should not advertise both push and pull for the same documents
- Verified 2026-06-23 against https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_diagnostic
- **Work-done progress**: the client advertises support via the `window.workDoneProgress: boolean`
  client capability. The server creates a progress token with the `window/workDoneProgress/create`
  request and reports begin/report/end via the generic `$/progress` notification
  (`ProgressParams` = `{token, value}`). Methods that support progress accept a `workDoneToken` in
  their params.

## Request cancellation (verified) — the LSP↔Go-context seam

- The client cancels an in-flight request with the **`$/cancelRequest`** notification, whose
  `CancelParams` is a single field `id: integer | string` (the request id to cancel).
- **A cancelled request must still return a response** — it cannot be left hanging. If the server
  returns an error on cancellation, the spec advises error code
  **`ErrorCodes.RequestCancelled = -32800`**. The related **`ContentModified = -32801`** is returned
  when the document content changed out from under an in-flight computation (e.g. the index was
  rebuilt mid-request); a client may silently retry on `ContentModified`.
- **Design contract for this project:** map `$/cancelRequest(id)` to **cancelling the
  `context.Context` of the handler running that id**. Every handler that does non-trivial work
  (definition/references/symbol over a large index) must thread that context and return promptly with
  `-32800` when it is cancelled, rather than completing wasted work. This is the concrete point where
  the LSP protocol obligation and the Go cancellation mechanics (Go KB `concurrency-primitives.md`,
  skill concurrency reference) meet — the protocol owns *what* must happen, Go owns *how*.

## Implications for this project

- **Centralize position-encoding conversion** (source byte/rune offsets ↔ LSP columns) in exactly one
  place; every feature returning a `Range` depends on it. The conversion must key off the negotiated
  `positionEncoding`, defaulting to UTF-16.
- **Capability advertisement is phase-gated** — only advertise a provider once its handler exists
  (feature plan 03). Adding a capability you don't serve causes client requests you'll fail.
- The chosen JSON-RPC/types library (ADR-010) lives behind `internal/server` and must not leak into
  `internal/analysis` / `internal/model`.
- **Negotiated-encoding plumbing (open question now narrowed).** Whatever transport library lands per
  ADR-010, the negotiation is the same shape: read `general.positionEncodings` off the client's
  `InitializeParams`, pick the project's preferred order (UTF-8 then UTF-16 per ADR-008), and set the
  single `positionEncoding` field on the returned `ServerCapabilities`. `go.lsp.dev/protocol` (the
  ADR-010 default) generates a `PositionEncodingKind` type and the `ClientCapabilities.General.
  PositionEncodings` / `ServerCapabilities.PositionEncoding` fields directly from the LSP meta-model
  (Go KB `lsp-go-ecosystem.md`), so the field is a plain struct field — there is no library-specific
  magic to discover; the conversion point (ADR-008) reads that one negotiated value. If ADR-010 is
  re-decided toward a hand-rolled types layer, this field must be hand-modeled the same way. This
  resolves the prior open question about *how* the library surfaces the negotiated encoding: it is an
  ordinary capabilities field on both sides, not a library-specific callback.

## Sources

- LSP 3.17 specification (verified 2026-06-20):
  https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/
  - Position encoding default `utf-16` and `general.positionEncodings` negotiation; `positionEncoding`
    server capability.
  - Base protocol `Content-Length` framing; lifecycle (`initialize` once → `initialized` → `shutdown`
    → `exit`); `TextDocumentSyncKind` enum; half-open zero-based `Range` ("end position is exclusive").
  - ServerCapabilities provider field names/types (definition/references/hover/documentSymbol/
    workspaceSymbol/codeLens/textDocumentSync/positionEncoding).
  - `$/cancelRequest` (`CancelParams.id`), the "must still respond" rule, and error codes
    `RequestCancelled = -32800` / `ContentModified = -32801` (verified 2026-06-20).
- `workspaceSymbolProvider: boolean | WorkspaceSymbolOptions` confirmed present since LSP 3.15:
  https://microsoft.github.io/language-server-protocol/specifications/specification-3-15/
- **Re-verified 2026-06-21** against the live 3.17 spec: position-encoding default
  (`"If omitted it defaults to 'utf-16'."`), the mandatory-UTF-16 baseline, `general.positionEncodings`
  client capability, and `"If the client didn't provide any position encodings the only valid value
  that a server can return is 'utf-16'."` Same URL as above.
- Pull-diagnostics model (`diagnosticProvider`, `textDocument/diagnostic`, `workspace/diagnostic`)
  added in 3.17 — table-of-contents and capability presence confirmed; full section text could not be
  re-fetched verbatim (page exceeds single-fetch size). 3.17 spec, Language Features → Pull
  Diagnostics.

**CodeLens resolve decision (2026-06-23):** For natural-lsp v1, use **eager resolution** (`resolveProvider: false` or omitted). Lenses are simple counts/summaries from the index (call counts, write summaries), computation is cheap, and lazy resolution adds complexity without benefit for this scope. Revisit if lenses grow to expensive computations.
