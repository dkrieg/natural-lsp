package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sentinelName = ".natural-lsp.toml"

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
