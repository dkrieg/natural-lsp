# Configuration & TOML parsing

**Status:** verified (2026-06-21) — go-toml/v2 version/API and the `go mod tidy` retention
pattern confirmed against pkg.go.dev and observed `go mod tidy` behavior in this repo.

## Facts (verified)

- **TOML decoder: `github.com/pelletier/go-toml/v2`**, adopted at **v2.4.0** (published
  **2026-06-16**). It is the project's first third-party dependency (ADR-013 in
  `docs/architecture-decisions.md`). The import is **confined to `internal/config`** — never
  `internal/model` or `internal/analysis` (the Analyzer seam).
  - Package name is `toml` (path ends in `/v2`); idiomatic alias `toml "github.com/pelletier/go-toml/v2"`.
  - `toml.Unmarshal(data []byte, v any) error` — shortcut for `Decoder.Decode()` with defaults.
  - **Strict decoding:** `toml.NewDecoder(r).DisallowUnknownFields()` makes unknown keys an
    error, returning a `StrictMissingError` with the offending line shown in context. Good fit
    for catching typos in `.natural-lsp.toml` (observability over silent ignore).
  - Supports the last two major Go releases (per its docs) — covers the `go 1.26` floor.
  - Chosen over **`github.com/BurntSushi/toml`** (the older de facto library, now widely
    reported as unmaintained; community is migrating to go-toml/v2). v2 has a cleaner API.

## `go mod tidy` retains a not-yet-imported dependency only if something references it

- `go mod tidy` **strips any `require` that no package in the module imports** under any build
  configuration. Adding a dep with `go get` then running `tidy` removes it again if no source
  imports it — observed in this repo (T0 had no consumer yet).
- To keep a dependency in `go.mod`/`go.sum` *before its first real use* while keeping `tidy`
  clean and excluding it from normal builds and the binary, use the `tools.go` pattern: a file
  with a constraint that excludes it from default builds plus a blank import.

  ```go
  //go:build tools

  package config

  import _ "github.com/pelletier/go-toml/v2"
  ```

  `go mod tidy` **does** consider ignored/constrained build tags when computing module
  requirements, so the `require` is retained; `go build ./...` and `go list` exclude the file
  (verified: `go list -f '{{.GoFiles}}'` showed only `config.go`, not the tagged file). Delete
  the anchor file once real code imports the package directly.

## Sources

- pkg.go.dev/github.com/pelletier/go-toml/v2 (verified 2026-06-21: v2.4.0, 2026-06-16;
  `DisallowUnknownFields` / `StrictMissingError`; last-two-Go-majors support).
- go-toml v2 README & discussions: github.com/pelletier/go-toml (strict mode; BurntSushi
  migration trend).
- `go help mod tidy` / observed behavior in this repo (tidy strips unimported requires; honors
  build-tagged files).
