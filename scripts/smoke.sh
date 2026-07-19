#!/usr/bin/env bash
#
# smoke.sh — distribution smoke check for a natural-lsp binary.
#
# Given a natural-lsp binary path, this verifies the two things a freshly
# installed binary must do before any editor can use it (feature 15, Story 4,
# NFR-10/NFR-12/NFR-13):
#
#   1. `natural-lsp --version` prints a version line and exits 0.
#   2. The stdio LSP transport starts and exits cleanly on EOF
#      (`natural-lsp --stdio < /dev/null`), which is the smoke command the
#      README documents. As an optional stronger check, it also feeds a
#      minimal `initialize` -> `initialized` -> `shutdown` -> `exit` JSON-RPC
#      sequence over Content-Length framing and confirms the server responds
#      with an `initialize` result and exits 0.
#
# Usage:
#   scripts/smoke.sh [path-to-binary]
#
# If no path is given, defaults to `natural-lsp` on PATH.
#
# Exit status: 0 = all checks passed, non-zero = a check failed.

set -euo pipefail

BIN="${1:-natural-lsp}"

pass() { printf '  PASS  %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; exit 1; }

# Resolve the binary so that what we TEST for existence is what we EXEC.
#
# The subtlety: `[ -x name ]` tests a path relative to the current directory,
# but exec'ing a bare `name` (no slash) resolves via PATH — the two disagree for
# a bare cwd-local name. So:
#   * A path containing a slash is treated literally (relative or absolute).
#   * A bare name is first probed as a cwd-local file; if it exists and is
#     executable there, it is normalized to an explicit `./name` BEFORE exec so
#     the test and the exec refer to the same file.
#   * Otherwise a bare name is resolved via PATH (`command -v`).
#   * If neither locates an executable, fail with an accurate message.
case "$BIN" in
  */*)
    # Explicit path (relative or absolute): test and exec agree already.
    if [ ! -x "$BIN" ]; then
      fail "binary not found or not executable: $BIN"
    fi
    ;;
  *)
    # Bare name: prefer a cwd-local executable, normalized to an explicit path.
    if [ -x "./$BIN" ]; then
      BIN="./$BIN"
    elif command -v "$BIN" >/dev/null 2>&1; then
      BIN="$(command -v "$BIN")"
    else
      fail "binary not found on PATH and not executable in current directory: $BIN"
    fi
    ;;
esac

printf 'natural-lsp smoke check\n'
printf 'binary: %s\n\n' "$BIN"

# ---------------------------------------------------------------------------
# Check 1: --version
# ---------------------------------------------------------------------------
version_out="$("$BIN" --version 2>&1)" || fail "--version exited non-zero"
if printf '%s' "$version_out" | grep -qi 'natural-lsp'; then
  pass "--version -> $version_out"
else
  fail "--version output did not mention natural-lsp: $version_out"
fi

# ---------------------------------------------------------------------------
# Check 2a: --stdio starts and exits cleanly on EOF (the documented smoke)
# ---------------------------------------------------------------------------
if "$BIN" --stdio < /dev/null > /dev/null 2>&1; then
  pass "--stdio < /dev/null exits cleanly on EOF"
else
  fail "--stdio < /dev/null did not exit cleanly"
fi

# ---------------------------------------------------------------------------
# Check 2b: minimal LSP lifecycle round-trip over stdio (optional, stronger)
#   initialize -> initialized -> shutdown -> exit
# We assert the server writes an `initialize` result (its response contains a
# capabilities object) and exits 0.
# ---------------------------------------------------------------------------
frame() { # frame <json> -> Content-Length header + body on stdout
  local body="$1"
  printf 'Content-Length: %d\r\n\r\n%s' "${#body}" "$body"
}

init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":null,"capabilities":{}}}'
initialized='{"jsonrpc":"2.0","method":"initialized","params":{}}'
shutdown='{"jsonrpc":"2.0","id":2,"method":"shutdown","params":null}'
exitn='{"jsonrpc":"2.0","method":"exit","params":null}'

seq_out="$( { frame "$init"; frame "$initialized"; frame "$shutdown"; frame "$exitn"; } | "$BIN" --stdio 2>/dev/null )" \
  && lifecycle_rc=0 || lifecycle_rc=$?

if [ "${lifecycle_rc:-0}" -ne 0 ]; then
  fail "LSP lifecycle sequence exited non-zero ($lifecycle_rc)"
fi
if printf '%s' "$seq_out" | grep -q 'capabilities'; then
  pass "LSP lifecycle: initialize returned a capabilities result, clean exit"
else
  fail "LSP lifecycle: no initialize result (capabilities) in server output"
fi

printf '\nall smoke checks passed\n'
