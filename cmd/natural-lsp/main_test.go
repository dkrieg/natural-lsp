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
}

// TestRunStdioCallsBootstrap verifies that the real entry point wires
// config.Bootstrap into the --stdio path: invoking run with --stdio against a
// workspace that has a .natural-lsp.toml sentinel must resolve the workspace
// root via Bootstrap and emit its logging contract ("sentinel found: true")
// on the injected logger.
//
// Remediation R3 of 01-workspace-and-configuration: T9 DoD requires Bootstrap
// to be called from the --stdio path, not only from its unit test.
// (FR-1 Story 1 criterion 3, CR-6.)
func TestRunStdioCallsBootstrap(t *testing.T) {
	// Arrange: a temp dir with a sentinel, made the process working directory
	// so run's bootstrap start ("." / os.Getwd) resolves to it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sentinelName), nil, 0o644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}
	// Resolve symlinks: macOS t.TempDir() is under /var -> /private/var, and
	// Getwd reports the resolved path, so compare against the resolved dir.
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

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act.
	run([]string{"--stdio"}, logger)

	// Assert: Bootstrap's logging contract surfaced on the injected logger.
	got := logBuf.String()
	if !strings.Contains(got, "sentinel found: true") {
		t.Errorf("run(--stdio) log = %q, want substring %q (Bootstrap not wired into --stdio)", got, "sentinel found: true")
	}
}

// TestStdioExitCodes_cleansShutdown pins the exit-code mapping behavior for
// FR-41 Story 4 (T10): the --stdio path must not print "not yet implemented"
// once server.Run is wired. RED: the current stub prints it, so this fails.
func TestStdioExitCodes_cleansShutdown(t *testing.T) {
	// Arrange: a temp workspace with a sentinel, so Bootstrap succeeds.
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

	// Capture os.Stderr to detect the stub message.
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = stderrW

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Act.
	exitCode := run([]string{"--stdio"}, logger)

	stderrW.Close()
	os.Stderr = origStderr
	var stderrBuf bytes.Buffer
	if _, err := stderrBuf.ReadFrom(stderrR); err != nil {
		t.Fatalf("ReadFrom stderr pipe: %v", err)
	}
	stderrOut := stderrBuf.String()

	// Assert: once T10 wires server.Run the stub message must be gone.
	if strings.Contains(stderrOut, "not yet implemented") {
		t.Errorf("--stdio still prints stub message; T10 must replace it with server.Run: %q", stderrOut)
	}
	if exitCode != 0 {
		t.Errorf("run([--stdio]) = %d, want 0", exitCode)
	}
	// Regression: Bootstrap must still be called.
	if !strings.Contains(logBuf.String(), "sentinel found: true") {
		t.Errorf("Bootstrap not called; log = %q", logBuf.String())
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
