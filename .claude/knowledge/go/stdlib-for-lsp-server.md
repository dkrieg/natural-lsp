# Standard library for an LSP/stdio server

**Status:** verified (2026-06-20) — `os/signal` (NotifyContext), framing, slog, and the
`encoding/json/v2` status all confirmed against pkg.go.dev.

## Facts (verified unless noted)

- **stdio transport / framing**: LSP messages use HTTP-like headers (`Content-Length: N\r\n\r\n` +
  body). Read with `bufio.Reader` over `os.Stdin`; parse headers, then read exactly N bytes of body;
  write to `os.Stdout`. Keep stdout for protocol only — send logs to **stderr** so they don't corrupt
  the stream.
- **`encoding/json`**: marshals/unmarshals message bodies. `json.RawMessage` to defer decoding the
  `params`/`result` until the method is known. Field tags control names; unknown fields are **ignored
  by default**, and `Decoder.DisallowUnknownFields()` makes them an error (use sparingly — LSP clients
  send optional/extension fields, so being lenient is the more robust default). JSON object-key
  matching is **case-insensitive but prefers an exact tag match** when present — fine for LSP since
  the spec uses exact lowerCamelCase keys.
  - **`encoding/json/v2` — verified (2026-06-20): still experimental in Go 1.26.** Its godoc
    (checked at go1.26.4) states verbatim that the package "is experimental, and not subject to the
    Go 1 compatibility promise. It only exists when building with the `GOEXPERIMENT=jsonv2`
    environment variable set. Most users should use `encoding/json`." So in Go 1.26 it is **NOT
    promoted, NOT default**, and the API may change without notice. The 1.26 release notes do not
    mention it. **Decision: do not adopt it for this project** — stick to `encoding/json`. Gating the
    build on `GOEXPERIMENT=jsonv2` would make the binary non-buildable with a default toolchain and
    pin us to an unstable API; the lenient `encoding/json` behavior already documented here is
    sufficient for LSP message bodies. Revisit only if json/v2 is promoted in a later release.
- **`log/slog`** (stdlib since Go 1.21): structured logging. Write the handler to **stderr**
  (`slog.NewJSONHandler(os.Stderr, ...)` or text) — never stdout, which carries the LSP stream. Good
  for the "observable, never silent" requirement (skips, recoverable errors, ambiguity).
- **`os/signal`** (verified): prefer `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` →
  `(ctx, stop)`; `ctx` cancels on signal, `defer stop()`. Drives clean shutdown (cancel background
  indexing, finish/abandon cache writes safely). `context.Cause(ctx)` returns an error describing the
  signal. See `concurrency-primitives.md`.
- **`io` / `bufio`**: buffered reads/writes around the transport; `io.Reader`/`io.Writer` seams make
  the transport testable without real stdio.

## Implications

- The protocol/transport layer should be isolated and testable via in-memory readers/writers (no real
  stdin/stdout in unit tests).
- Logging must never write to stdout.

## Sources

- https://pkg.go.dev/encoding/json (verified 2026-06-20: unknown-field handling,
  `DisallowUnknownFields`, case-insensitive key matching)
- https://pkg.go.dev/log/slog , https://pkg.go.dev/bufio (verified 2026-06-20)
- https://pkg.go.dev/os/signal (verified 2026-06-20: NotifyContext)
- https://pkg.go.dev/encoding/json/v2 (verified 2026-06-20 at go1.26.4: experimental, requires
  `GOEXPERIMENT=jsonv2`, not Go 1 compatible, "most users should use encoding/json")
- Go 1.26 release notes: https://go.dev/doc/go1.26 (checked 2026-06-20 — no json/v2 mention,
  consistent with it remaining a GOEXPERIMENT)
- LSP base protocol framing: see the software-engineering KB `lsp-protocol.md`.