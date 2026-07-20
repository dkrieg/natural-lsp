package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/uri"
)

const sentinelName = ".natural-lsp.toml"

// writeFramedMessage writes a Content-Length-framed JSON-RPC message to buf.
// The format is: Content-Length: N\r\n\r\n<N bytes of JSON>
func writeFramedMessage(buf *bytes.Buffer, msg jsonrpc2.Message) error {
	encoded, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return err
	}
	contentLen := len(encoded)
	_, err = buf.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLen))
	if err != nil {
		return err
	}
	_, err = buf.Write(encoded)
	return err
}

// TestVersionFlag verifies that the `--version` flag prints a version identifier
// and exits with code 0, locking FR-42 (version reporting on CLI).
//
// Rename-safety note (feature 23, T2): the release build injects the version via
// `-ldflags "-X main.version=vX.Y.Z"` (justfile). The `-X` target `main.version`
// is package-relative to package `main` in this directory and is NOT qualified by
// the module path, so the 2026-07 module rename (`natural-lsp` →
// `github.com/dkrieg/natural-lsp`) does not change the injection target. This test
// guards the `--version` output shape (`natural-lsp <version>`) survives the rename.
func TestVersionFlag(t *testing.T) {
	// Arrange.
	var outBuf bytes.Buffer
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act.
	exitCode := run([]string{"--version"}, logger)

	// Restore stdout and read the captured output.
	w.Close()
	os.Stdout = origStdout
	if _, err := outBuf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom pipe: %v", err)
	}

	// Assert.
	output := outBuf.String()
	if exitCode != 0 {
		t.Errorf("run([--version]) exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "natural-lsp") {
		t.Errorf("run([--version]) output = %q, want substring %q", output, "natural-lsp")
	}
	// FR-42 shape: the line is `natural-lsp <version>` (a leading tool name so an
	// operator / the smoke check can grep for it — see scripts/smoke.sh check 1).
	if !strings.HasPrefix(strings.TrimSpace(output), "natural-lsp ") {
		t.Errorf("run([--version]) output = %q, want prefix %q", output, "natural-lsp ")
	}
}

// TestRunStdioCallsBootstrap verifies that the --stdio path wires config.Bootstrap
// into the "initialize" request handler (feature 20, Variant A: deferred bootstrap):
// driving a full initialize→initialized→shutdown→exit lifecycle with a rootUri that
// points at a workspace containing a .natural-lsp.toml sentinel must resolve the
// workspace root via Bootstrap FROM THE CLIENT PATH and emit its logging contract
// ("sentinel found: true") on the injected logger.
//
// Before feature 20, Bootstrap ran at process startup from os.Getwd; it now runs
// from the client-negotiated root inside the initialize handler. This test drives
// the handshake (rather than relying on EOF-immediately) so Bootstrap actually runs.
// (FR-1 Story 1 criterion 3, CR-6; feature 20 FR-46/NFR-14.)
func TestRunStdioCallsBootstrap(t *testing.T) {
	// Arrange: a temp workspace with a sentinel, referenced via rootUri. The
	// process working directory is deliberately NOT this dir (feature 20: the
	// client path, not the cwd, drives discovery).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sentinelName), nil, 0o644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	rootURI := uri.File(resolved)
	initParams := fmt.Sprintf(`{"processId":1234,"rootUri":%q,"capabilities":{}}`, string(rootURI))
	var inBuf bytes.Buffer
	msgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "shutdown", jsonrpc2.RawMessage(`{}`)),
		jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`)),
	}
	for i, m := range msgs {
		if err := writeFramedMessage(&inBuf, m); err != nil {
			t.Fatalf("writeFramedMessage %d: %v", i, err)
		}
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act: drive the handshake so the deferred Bootstrap runs from rootUri.
	exit := runWithIO([]string{"--stdio"}, &inBuf, &bytes.Buffer{}, logger)
	if exit != 0 {
		t.Fatalf("runWithIO([--stdio]) exit = %d, want 0", exit)
	}

	// Assert: Bootstrap's logging contract surfaced, and it resolved the sentinel
	// from the client's rootUri (not the cwd).
	got := logBuf.String()
	if !strings.Contains(got, "sentinel found: true") {
		t.Errorf("run(--stdio) log = %q, want substring %q (Bootstrap not wired into initialize)", got, "sentinel found: true")
	}
	// Prove the root came from the client rootUri (not the cwd) by matching the
	// temp dir's leaf segment. We deliberately do NOT match the full absolute
	// path: on Windows the logged path uses a lowercase drive letter and 8.3
	// short names (e.g. c:\Users\RUNNER~1\...\001) that will not string-match the
	// long, uppercase-drive form of `resolved`. The leaf segment is stable across
	// that variance, and combined with "sentinel found: true" (the cwd fallback
	// has no sentinel) it proves discovery ran against the client root.
	leaf := filepath.Base(resolved)
	if !strings.Contains(got, leaf) {
		t.Errorf("run(--stdio) log = %q, want it to name the client-root leaf %q (from %q)", got, leaf, resolved)
	}
}

// TestStdioExitCodes_cleansShutdown pins the exit-code mapping behavior for
// FR-41 Story 4 (T10): the --stdio path must not print "not yet implemented"
// and must exit 0 on a clean EOF.
//
// Feature 20 (T7): with EOF-before-initialize the deferred Bootstrap never runs
// (no client params to resolve a root from) — the server must still exit 0 without
// panicking. Bootstrap-wiring into the initialize handler is covered separately by
// TestRunStdioCallsBootstrap, which drives a full handshake.
func TestStdioExitCodes_cleansShutdown(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act: use runWithIO with an empty reader so the server hits EOF and returns
	// without blocking on os.Stdin (which may be a terminal in local dev).
	var outBuf bytes.Buffer
	exitCode := runWithIO([]string{"--stdio"}, &bytes.Buffer{}, &outBuf, logger)

	// Assert: the stub message must be gone and the clean EOF exits 0.
	logOut := logBuf.String()
	if strings.Contains(logOut, "not yet implemented") {
		t.Errorf("--stdio still logs stub message; expected server.Run: %q", logOut)
	}
	if exitCode != 0 {
		t.Errorf("runWithIO([--stdio]) = %d, want 0", exitCode)
	}
}

// TestStdioExitCodes_protocolViolation pins that a protocol violation
// (exit-without-shutdown) causes a non-zero exit code (FR-41 Story 4).
// Uses runWithIO to inject an "exit" notification without a prior shutdown.
func TestStdioExitCodes_protocolViolation(t *testing.T) {
	// Arrange: a temp workspace with a sentinel.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sentinelName), nil, 0o644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(resolved); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	// Build a protocol-violation sequence: "exit" without prior "shutdown".
	var inBuf bytes.Buffer
	if err := writeFramedMessage(&inBuf, jsonrpc2.NewNotification("exit", nil)); err != nil {
		t.Fatalf("writeFramedMessage exit: %v", err)
	}

	var outBuf bytes.Buffer
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act: inject the violation sequence via runWithIO.
	exitCode := runWithIO([]string{"--stdio"}, &inBuf, &outBuf, logger)

	// Assert: a protocol violation must produce exit code 1.
	if exitCode == 0 {
		t.Errorf("runWithIO([--stdio]) with exit-without-shutdown = 0, want non-zero (FR-41 Story 4)")
	}
}

// TestSmokeLifecycleNullRootUri verifies that the deferred bootstrap (feature 20)
// tolerates the exact smoke-script parameters: initialize with rootUri:null,
// complete the full lifecycle (initialized → shutdown → exit), and exit cleanly.
// This pins feature 20 T7 (lifecycle/smoke guard): the server must not panic
// when root params are absent and must complete the LSP lifecycle successfully.
//
// The smoke script (scripts/smoke.sh) uses `{"processId":null,"rootUri":null,"capabilities":{}}`
// as the strongest lifecycle test before editors can use a binary. This test
// pins that exact scenario as a regression guard.
//
// Note: With rootUri:null and (in a temp, empty cwd) no sentinel, the
// no-usable-root condition from feature 20 T5/T6 fires. The test must assert
// the lifecycle STILL completes cleanly despite window/showMessage notifications
// being emitted (i.e. they are non-fatal).
func TestSmokeLifecycleNullRootUri(t *testing.T) {
	// Arrange: use an empty temp dir as the cwd (no sentinel, no Natural files).
	// This triggers the no-usable-root condition from T5/T6, which may emit
	// window/showMessage, but the lifecycle must still complete.
	cwdDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(cwdDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	// Build the exact smoke-script message sequence:
	// initialize with processId:null, rootUri:null, capabilities:{}
	// followed by initialized → shutdown → exit.
	var inBuf bytes.Buffer
	msgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(`{"processId":null,"rootUri":null,"capabilities":{}}`)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "shutdown", jsonrpc2.RawMessage(`null`)),
		jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`null`)),
	}
	for i, m := range msgs {
		if err := writeFramedMessage(&inBuf, m); err != nil {
			t.Fatalf("writeFramedMessage %d: %v", i, err)
		}
	}

	var outBuf bytes.Buffer
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act: run the full lifecycle with the smoke params.
	exitCode := runWithIO([]string{"--stdio"}, &inBuf, &outBuf, logger)

	// Assert: the process must exit 0 (clean shutdown).
	if exitCode != 0 {
		t.Errorf("runWithIO([--stdio]) exit = %d, want 0 (clean exit); log: %s", exitCode, logBuf.String())
	}

	// Assert: the server output contains an initialize response (proof of capabilities).
	outStr := outBuf.String()
	if !strings.Contains(outStr, "capabilities") {
		t.Errorf("server output does not contain 'capabilities'; output: %q", outStr)
	}

	// Assert: no panic or JSON-RPC error in the output.
	logStr := logBuf.String()
	if strings.Contains(logStr, "panic") {
		t.Errorf("log contains panic: %s", logStr)
	}
}
