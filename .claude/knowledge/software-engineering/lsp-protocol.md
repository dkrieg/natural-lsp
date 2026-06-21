# LSP protocol essentials

**Status:** verified (2026-06-20) against the official **LSP 3.17** specification
(microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/). All facts below
were confirmed there unless explicitly noted otherwise.

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

- **Diagnostics** (`textDocument/publishDiagnostics`) are a **server→client notification, not a
  ServerCapabilities provider field** — there is no provider flag to set for push diagnostics; the
  server simply publishes. (Note: 3.17 added a separate *pull*-diagnostics model with a
  `diagnosticProvider` capability; this project uses **push**.)
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
