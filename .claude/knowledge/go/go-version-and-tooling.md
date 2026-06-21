# Go version & tooling

**Status:** verified (2026-06-20) — Go 1.26 release confirmed against go.dev release history and the
1.26 release notes; the command set is from the repo's CLAUDE.md.

## Facts (verified)

- **Go 1.26 is released and current**: 1.26.0 on **2026-02-10**, latest patch **1.26.4 (2026-06-02)**.
  Go 1.25.0 shipped 2025-08-12 (latest patch 1.25.11). Per Go's policy, a release is supported until
  two newer majors exist, so 1.26 and 1.25 are supported; 1.24 is end-of-support.
- **Go 1.26 notable language features** (from the 1.26 release notes): `new(expr)` — the built-in
  `new` now accepts an expression operand and returns a pointer to a copy of its value (handy for
  optional/pointer fields); self-referential generic type constraints (`type Adder[A Adder[A]] ...`).
  Stdlib additions include `crypto/hpke`, experimental `simd/archsimd` (amd64), experimental
  `runtime/secret`, default post-quantum hybrid TLS key exchange, and `reflect` field/method
  iterators. None of these are obviously required by this project (see open question — the go.mod
  floor is currently a choice, not a hard dependency).

## Project facts

- Module: `natural-lsp` (`go.mod`), `go 1.26`. The README's `go install` path uses
  `github.com/dkrieg/natural-lsp/cmd/natural-lsp` — module path vs. install path must be reconciled
  before publishing.
- Core commands (from CLAUDE.md):
  - `go build -o natural-lsp ./cmd/natural-lsp` — build the binary
  - `go test ./...` — unit tests; `go test -run TestName ./internal/analysis/natural` — single test
  - `go build -o natural-lsp ./cmd/natural-lsp && go test -tags integration ./...` — integration
    tests (require the built binary)
  - `make release` — release binaries
  - `./natural-lsp --stdio < /dev/null` — smoke test (initialize response shape)
- **Build tags:** integration tests are gated behind the `integration` tag.
- **Race detector:** `go test -race ./...` is the bar for concurrent code.
- `go vet ./...` and `gofmt` must be clean; CI/release should enforce both.

## Open question

- Identify any genuine Go 1.26-only feature the project relies on (vs. just the go.mod floor). Nothing
  in the 1.26 notes is obviously required; if none materializes, document that the floor is a choice
  and could be lowered (e.g. to 1.25, which has `slog`, generics, `errgroup` patterns, etc.) for
  broader buildability. Revisit if `encoding/json/v2` is adopted (see `stdlib-for-lsp-server.md`).

## Sources

- Internal: repo `CLAUDE.md`, `go.mod`, `README.md`.
- Go release history: https://go.dev/doc/devel/release (verified 2026-06-20: 1.26.0 = 2026-02-10,
  1.26.4 = 2026-06-02, 1.25.0 = 2025-08-12).
- Go 1.26 release notes: https://go.dev/doc/go1.26 (verified 2026-06-20: `new(expr)`, self-referential
  generics, stdlib additions).