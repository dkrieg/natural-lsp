package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"natural-lsp/internal/config"
)

// FuzzLoad is the executable proof of the CR-6 fail-safe contract for
// [config.Load]: no byte sequence — valid TOML, malformed TOML, or arbitrary
// binary — may make Load panic, and Load must always hand back a *usable*
// Config (never a zero struct), whether it succeeds or hard-errors on
// unparseable input. Returning an error on bad TOML is expected and fine; what
// is forbidden is a crash or a Config the server cannot run on.
//
// The seed corpus is drawn from the committed testdata/config fixtures so the
// real, known-interesting shapes (empty, minimal, pure garbage, a library map)
// anchor the mutation engine.
//
// Feature 01-workspace-and-configuration remediation R1; FR-6, CR-2, CR-6.
func FuzzLoad(f *testing.F) {
	// Seed from the existing testdata corpus: read each fixture's bytes and
	// add them so the fuzzer starts from representative valid/invalid inputs.
	for _, name := range []string{
		"empty.toml",
		"minimal.toml",
		"garbage.toml",
		"libraries.toml",
	} {
		data, err := os.ReadFile(filepath.Join("testdata", "config", name))
		if err != nil {
			f.Fatalf("seed %s: %v", name, err)
		}
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Arrange: materialize the fuzz input as a sentinel file Load can read.
		path := filepath.Join(t.TempDir(), ".natural-lsp.toml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write fuzz input: %v", err)
		}

		// Act: a hard error on unparseable TOML is an expected outcome, not a
		// failure — capture it but do not fail on it. The only forbidden
		// outcomes are a panic (caught by the fuzzing harness automatically) or
		// an unusable Config.
		cfg, _, _ := config.Load(path)

		// Assert: even on the error path, Load returns Defaults(), so the
		// effective Config is always usable. A zero struct would have an empty
		// extension set, an empty cache path, and a non-positive max file size —
		// any of those proves Load handed back something the server cannot run
		// on. Validate guarantees these post-conditions for every input.
		if cfg.Workspace.MaxFileSize <= 0 {
			t.Errorf("Load returned unusable Config: Workspace.MaxFileSize = %d, want positive", cfg.Workspace.MaxFileSize)
		}
		if len(cfg.Workspace.Extensions) == 0 {
			t.Errorf("Load returned unusable Config: Workspace.Extensions is empty, want at least the default object-type set")
		}
		if cfg.Cache.Path == "" {
			t.Errorf("Load returned unusable Config: Cache.Path is empty, want a default cache directory")
		}
	})
}
