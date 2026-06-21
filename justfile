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

# Full pre-push / CI gate: format + vet + build + unit (race) + integration
verify: fmt-check vet build test test-integration
    @echo "verify: OK — safe to push"

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
