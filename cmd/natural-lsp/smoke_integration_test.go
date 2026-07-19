//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot walks up from the test's working directory (go test sets cwd to the
// package directory) until go.mod is found. This mirrors the harness in
// stdio_integration_test.go and keeps the test free of absolute machine paths
// (respecting `just lint-tests`).
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from %s", dir)
		}
		dir = parent
	}
}

// buildSmokeBinary builds the natural-lsp binary into dir/natural-lsp and returns
// its path. dir is typically t.TempDir() so the binary is NOT on PATH.
func buildSmokeBinary(t *testing.T, root, dir string) string {
	t.Helper()
	binPath := filepath.Join(dir, "natural-lsp")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/natural-lsp")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	return binPath
}

// scrubbedPATH returns the current PATH with any element equal to `remove`
// stripped, so a binary living in `remove` is reachable via cwd/explicit path but
// NOT via PATH. This is the exact Story 2 case: the just-built binary is in the
// working dir but not on PATH.
func scrubbedPATH(remove string) string {
	sep := string(os.PathListSeparator)
	var kept []string
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == remove || p == "" {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, sep)
}

// runSmoke invokes scripts/smoke.sh with the given args, working dir, and PATH,
// returning combined output and any exec error.
func runSmoke(t *testing.T, root, workdir, path string, args ...string) (string, error) {
	t.Helper()
	script := filepath.Join(root, "scripts", "smoke.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = workdir
	// A minimal, deterministic environment: keep HOME (bash startup) but control PATH.
	env := os.Environ()
	var filtered []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered, "PATH="+path)
	cmd.Env = filtered
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// requireBash skips the test where bash is unavailable (e.g. some Windows CI
// runners). The smoke script is a bash script; without an interpreter the test
// cannot run and is not a meaningful failure. Documented platform assumption.
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; scripts/smoke.sh is a bash script")
	}
}

// TestSmokeScriptExplicitPath verifies the baseline: passing an explicit binary
// path to scripts/smoke.sh (the way the release/CI pipeline calls it) passes.
func TestSmokeScriptExplicitPath(t *testing.T) {
	requireBash(t)
	root := moduleRoot(t)
	binDir := t.TempDir()
	binPath := buildSmokeBinary(t, root, binDir)

	// Explicit path, PATH scrubbed of the binary's dir — this branch has always
	// worked (a path containing "/" is exec'd literally, matching the -x test).
	out, err := runSmoke(t, root, binDir, scrubbedPATH(binDir), binPath)
	if err != nil {
		t.Fatalf("smoke.sh %s failed: %v\n%s", binPath, err, out)
	}
	if !strings.Contains(out, "all smoke checks passed") {
		t.Errorf("smoke.sh output missing success line:\n%s", out)
	}
}

// TestSmokeScriptCwdLocalNotOnPATH is the Story 2 regression (feature 23, T3/T4).
//
// It reproduces the exact bug: the just-built binary sits in the working
// directory named `natural-lsp` but is NOT on PATH. Invoking the script with no
// argument makes it default to the bare name `natural-lsp`. The pre-T3 resolver
// tested `[ -x natural-lsp ]` (cwd-relative, succeeds) but then exec'd
// `natural-lsp --version` (bare name → PATH lookup, fails), mis-reporting
// "--version exited non-zero". After the T3 fix, the script normalizes the
// cwd-local bare name to an explicit `./natural-lsp` before exec, so the test and
// the exec agree and all checks pass.
//
// This test is load-bearing: it FAILS against the pre-T3 script and PASSES after.
func TestSmokeScriptCwdLocalNotOnPATH(t *testing.T) {
	requireBash(t)
	root := moduleRoot(t)
	binDir := t.TempDir()
	buildSmokeBinary(t, root, binDir)

	// No argument (defaults to the bare name `natural-lsp`), cwd = binDir (so the
	// binary is cwd-local), PATH scrubbed of binDir (so it is NOT on PATH).
	out, err := runSmoke(t, root, binDir, scrubbedPATH(binDir))
	if err != nil {
		t.Fatalf("smoke.sh (no arg, cwd-local, not on PATH) failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "all smoke checks passed") {
		t.Errorf("smoke.sh output missing success line:\n%s", out)
	}
}

// TestSmokeScriptGenuinelyAbsent verifies the accurate-failure path: no argument,
// no cwd-local binary, nothing named natural-lsp on PATH → non-zero exit with a
// "not found on PATH" message (not a misleading "--version exited non-zero").
func TestSmokeScriptGenuinelyAbsent(t *testing.T) {
	requireBash(t)
	root := moduleRoot(t)
	emptyDir := t.TempDir()
	binDir := t.TempDir()
	// Build the binary somewhere OTHER than the working dir and scrub its dir from
	// PATH, so no `natural-lsp` is reachable from emptyDir at all.
	buildSmokeBinary(t, root, binDir)

	out, err := runSmoke(t, root, emptyDir, scrubbedPATH(binDir))
	if err == nil {
		t.Fatalf("smoke.sh (no binary anywhere) unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "not found on PATH") {
		t.Errorf("smoke.sh failure message = %q, want to mention %q", out, "not found on PATH")
	}
}
