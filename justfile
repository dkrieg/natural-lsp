# natural-lsp task runner. Install: `brew install just`. Run `just --list` for recipes.
# `just verify` is the single gate run locally (pre-push hook), in /finalize-feature, and in CI.

# Show available recipes
default:
    @just --list

# Build the server binary
build:
    go build -o natural-lsp ./cmd/natural-lsp

# Fail if any file is not gofmt-formatted
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    out="$(gofmt -l .)"
    if [ -n "$out" ]; then echo "gofmt needed on:"; echo "$out"; exit 1; fi

# Apply gofmt in place
fmt:
    gofmt -w .

# Reject hard-coded absolute home paths in test files. Such paths resolve on the
# author's machine (so `just verify` and the pre-push hook pass locally) but break
# in CI, where the repo is checked out elsewhere. Tests must read fixtures via
# package-relative paths (Go runs tests with the package dir as the working dir).
lint-tests:
    #!/usr/bin/env bash
    set -euo pipefail
    hits="$(grep -rnE '"(/Users/|/home/|/root/)|[A-Za-z]:\\\\' --include='*_test.go' . || true)"
    if [ -n "$hits" ]; then
      echo "absolute machine paths found in test files — use package-relative paths instead:"
      echo "$hits"
      exit 1
    fi

# Static analysis
vet:
    go vet ./...

# Unit tests with the race detector
test:
    go test -race ./...

# Integration tests (builds the binary, runs the `integration` build tag)
test-integration:
    go build -o natural-lsp ./cmd/natural-lsp
    go test -tags integration ./...

# Full pre-push / CI gate: format + test-path lint + vet + build + unit (race) + integration
verify: fmt-check lint-tests vet build test test-integration
    @echo "verify: OK — safe to push"

# Scale/performance benchmarks — bench-tagged, excluded from verify; BENCH_CORPUS_OBJECTS=N to scale
bench:
    # Runs the `//go:build bench` package over a deterministic synthetic corpus.
    # Excluded from `just verify`: the bench tag is off by default, so
    # `go build`/`go vet`/`go test ./...` never compile or run it. Corpus size is
    # tunable via BENCH_CORPUS_OBJECTS (default: small/medium/large tiers, fast):
    #   just bench                             # small/medium/large default tiers
    #   BENCH_CORPUS_OBJECTS=10000 just bench  # add a manual large tier
    #   BENCH_CORPUS_OBJECTS=30000 just bench  # headline-figure run
    # Covers the workspace bench package (cold/warm/memory + name-index
    # micro-benchmarks), the internal/server request-latency baselines
    # (workspace/symbol + references providers, which need the unexported
    # handlerContext so they live in-package behind the bench tag), and the
    # internal/analysis/natural semantic-tokens classifier latency benchmark
    # (feature 35 — BenchmarkSemanticTokens, lives in-package behind the seam).
    go test -tags bench -bench=. -benchmem -run=^$ ./internal/workspace/bench/... ./internal/server/... ./internal/analysis/natural/...

# Enable the repo git hooks (pre-push then runs `just verify`)
install-hooks:
    git config core.hooksPath .githooks
    @echo "Installed: pre-push now runs 'just verify' (set core.hooksPath=.githooks)."

# Cross-compile release binaries into dist/ (runs verify first). Usage: just release v1.2.3
release version: verify
    #!/usr/bin/env bash
    set -euo pipefail
    ver="{{version}}"
    rm -rf dist && mkdir -p dist
    ldflags="-s -w -X main.version=${ver}"
    for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
      os="${t%/*}"; arch="${t#*/}"
      out="dist/natural-lsp-${os}-${arch}"
      [ "$os" = windows ] && out="${out}.exe"
      echo "building ${out} (${ver})"
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "${ldflags}" -o "${out}" ./cmd/natural-lsp
    done
    ( cd dist && shasum -a 256 natural-lsp-* > checksums.txt )
    echo "release artifacts written to dist/"
