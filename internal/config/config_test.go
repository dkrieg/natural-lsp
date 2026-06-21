package config_test

import (
	"bytes"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"natural-lsp/internal/config"
)

const sentinelName = ".natural-lsp.toml"

// update regenerates the committed golden files (e.g. sample.golden.toml) when
// set: go test ./internal/config/ -update. Without it, golden tests compare
// against the committed bytes. Standard testdata determinism pattern (T8).
var update = flag.Bool("update", false, "regenerate golden files")

// mustMkdirAll creates dir (and parents) under the test temp tree, failing the
// test on error.
func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
}

// mustWriteSentinel writes an empty .natural-lsp.toml sentinel into dir,
// failing the test on error.
func mustWriteSentinel(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, sentinelName), nil, 0o644); err != nil {
		t.Fatalf("WriteFile sentinel in %q: %v", dir, err)
	}
}

// TestDefaults verifies Defaults() returns a Config populated with every
// documented default from the .natural-lsp.toml schema (README "Workspace
// configuration"). Feature 01-workspace-and-configuration, FR-6 / CR-2:
// every configurable value has a documented default so the server runs with
// zero or minimal configuration.
func TestDefaults(t *testing.T) {
	cfg := config.Defaults()

	t.Run("extensions match the default object-type set in order", func(t *testing.T) {
		want := []string{
			".NSP", ".NSN", ".NSS", ".NSC", ".NSM",
			".NSL", ".NSG", ".NSA", ".NSH", ".NSD",
			".NS4", ".NS7", ".NS3", ".NS8", ".NST",
		}
		if !reflect.DeepEqual(cfg.Workspace.Extensions, want) {
			t.Errorf("Workspace.Extensions = %#v, want %#v", cfg.Workspace.Extensions, want)
		}
	})

	t.Run("exclude directories", func(t *testing.T) {
		want := []string{"archive", "backup", ".git"}
		if !reflect.DeepEqual(cfg.Workspace.Exclude, want) {
			t.Errorf("Workspace.Exclude = %#v, want %#v", cfg.Workspace.Exclude, want)
		}
	})

	t.Run("max file size", func(t *testing.T) {
		const want = 5_000_000
		if cfg.Workspace.MaxFileSize != want {
			t.Errorf("Workspace.MaxFileSize = %d, want %d", cfg.Workspace.MaxFileSize, want)
		}
	})

	t.Run("cache path", func(t *testing.T) {
		const want = ".natural-lsp-cache"
		if cfg.Cache.Path != want {
			t.Errorf("Cache.Path = %q, want %q", cfg.Cache.Path, want)
		}
	})

	t.Run("analysis flag dynamic calls", func(t *testing.T) {
		if !cfg.Analysis.FlagDynamicCalls {
			t.Errorf("Analysis.FlagDynamicCalls = %t, want true", cfg.Analysis.FlagDynamicCalls)
		}
	})

	t.Run("analysis dynamic call min length", func(t *testing.T) {
		const want = 6
		if cfg.Analysis.DynamicCallMinLength != want {
			t.Errorf("Analysis.DynamicCallMinLength = %d, want %d", cfg.Analysis.DynamicCallMinLength, want)
		}
	})

	t.Run("resolution library map is non-nil and empty", func(t *testing.T) {
		if cfg.Resolution.Libraries == nil {
			t.Errorf("Resolution.Libraries = nil, want non-nil empty slice")
		}
		if len(cfg.Resolution.Libraries) != 0 {
			t.Errorf("len(Resolution.Libraries) = %d, want 0", len(cfg.Resolution.Libraries))
		}
	})
}

// TestLoad verifies Load reads .natural-lsp.toml, decodes it onto a Defaults()
// base (unset keys keep their defaults), and fails safe: a syntactically
// invalid TOML file yields a non-nil error while still returning a usable
// Defaults() config so the server can start. Feature
// 01-workspace-and-configuration, Story 2 / Story 6, FR-6 / CR-2 / CR-6.
//
// Fixtures live under testdata/config/ relative to this package directory.
func TestLoad(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string // path relative to the package dir
		wantCfg  config.Config
		wantErr  bool
		problems int // expected number of Problems (0 for these cases)
	}{
		{
			// CR-2: an empty sentinel applies all documented defaults.
			name:    "empty file applies all defaults",
			fixture: "testdata/config/empty.toml",
			wantCfg: config.Defaults(),
		},
		{
			// CR-2: an unset key keeps its default; only the set key changes.
			// Decode-onto-defaults semantics: extensions is overridden,
			// everything else stays at Defaults().
			name:    "minimal file overrides only the set key",
			fixture: "testdata/config/minimal.toml",
			wantCfg: func() config.Config {
				c := config.Defaults()
				c.Workspace.Extensions = []string{".NSP"}
				return c
			}(),
		},
		{
			// CR-6 fail-safe: unparseable TOML is the one hard-fail path — a
			// non-nil error AND a usable Defaults() config so the server can
			// still start. Asserted as an outcome, not silently dropped.
			name:    "garbage file errors but returns usable defaults",
			fixture: "testdata/config/garbage.toml",
			wantCfg: config.Defaults(),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCfg, gotProblems, err := config.Load(tc.fixture)

			if tc.wantErr && err == nil {
				t.Errorf("Load(%q) error = nil, want non-nil (unparseable TOML must fail)", tc.fixture)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Load(%q) error = %v, want nil", tc.fixture, err)
			}
			if !reflect.DeepEqual(gotCfg, tc.wantCfg) {
				t.Errorf("Load(%q) config = %#v, want %#v", tc.fixture, gotCfg, tc.wantCfg)
			}
			if len(gotProblems) != tc.problems {
				t.Errorf("Load(%q) problems = %d (%#v), want %d", tc.fixture, len(gotProblems), gotProblems, tc.problems)
			}
		})
	}
}

// TestLoadValidation verifies the per-field validation Load applies through
// Validate for the core [workspace] and [cache] fields: extensions are
// normalized (leading dot enforced, upper-cased per Natural case-insensitivity,
// deduped in stable first-occurrence order); a non-positive max_file_size falls
// back to the default and is reported as a Problem; and a non-empty cache.path
// is taken as-is with no Problem. Feature 01-workspace-and-configuration,
// Story 3 / Story 6, FR-2, FR-3, CR-3, CR-6.
//
// Fixtures live under testdata/config/ relative to this package directory.
func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		fixture string // path relative to the package dir
		// check inspects the loaded config and the reported problems.
		check func(t *testing.T, cfg config.Config, problems []config.Problem)
	}{
		{
			// FR-2 / CR-3: a custom, mixed-case, dot-omitted, duplicated
			// extension list normalizes to dot-prefixed, upper-cased, deduped
			// entries in stable first-occurrence order, with no Problem.
			// "nsp" -> ".NSP"; ".NSN" -> ".NSN"; "NSN" -> ".NSN" (dup, dropped).
			name:    "extensions normalized deduped and dot-prefixed",
			fixture: "testdata/config/extensions-custom.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				want := []string{".NSP", ".NSN"}
				if !reflect.DeepEqual(cfg.Workspace.Extensions, want) {
					t.Errorf("Workspace.Extensions = %#v, want %#v", cfg.Workspace.Extensions, want)
				}
				if len(problems) != 0 {
					t.Errorf("problems = %#v, want none", problems)
				}
			},
		},
		{
			// FR-3 / CR-6: a non-positive max_file_size is rejected, falls back
			// to the documented default, and surfaces as a single Problem keyed
			// "workspace.max_file_size" — a reported degradation, not a failure.
			name:    "non-positive max_file_size falls back to default with a problem",
			fixture: "testdata/config/bad-maxsize.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				const want = 5_000_000
				if cfg.Workspace.MaxFileSize != want {
					t.Errorf("Workspace.MaxFileSize = %d, want %d (default)", cfg.Workspace.MaxFileSize, want)
				}
				var found int
				for _, p := range problems {
					if p.Key == "workspace.max_file_size" {
						found++
					}
				}
				if found != 1 {
					t.Errorf("problems with Key %q = %d, want 1 (problems = %#v)", "workspace.max_file_size", found, problems)
				}
			},
		},
		{
			// FR-6 / CR-3: a non-empty cache.path is taken verbatim with no
			// validation Problem.
			name:    "non-empty cache path taken as-is",
			fixture: "testdata/config/custom-cache.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				const want = "build/idx"
				if cfg.Cache.Path != want {
					t.Errorf("Cache.Path = %q, want %q", cfg.Cache.Path, want)
				}
				if len(problems) != 0 {
					t.Errorf("problems = %#v, want none", problems)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, problems, err := config.Load(tc.fixture)
			if err != nil {
				t.Fatalf("Load(%q) error = %v, want nil", tc.fixture, err)
			}
			tc.check(t, cfg, problems)
		})
	}
}

// TestDegenerateExtensions verifies that dot-only / whitespace-only
// workspace.extensions entries are rejected rather than silently accepted.
//
// Tokens like ".", "...", "  " and "" carry no extension body: after the
// current normalization (trim, dot-prefix, upper-case) "." and "..." survive as
// dot-only tokens that can never match a real object file. Per CR-6 ("bad value
// → Problem, never silently accepted"), such degenerate entries must be dropped
// AND the substitution surfaced as a Problem keyed "workspace.extensions" — not
// carried through to Workspace.Extensions unreported.
//
// Feature 01-workspace-and-configuration, remediation R2, CR-6.
//
// Fixture lives under testdata/config/ relative to this package directory.
func TestDegenerateExtensions(t *testing.T) {
	const fixture = "testdata/config/degenerate-extensions.toml"

	// Arrange + Act.
	cfg, problems, err := config.Load(fixture)
	if err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", fixture, err)
	}

	// Assert: no dot-only token leaks into the effective extension set.
	for _, ext := range cfg.Workspace.Extensions {
		if ext == "." || ext == "..." {
			t.Errorf("Workspace.Extensions contains degenerate token %q; want it dropped (got %#v)", ext, cfg.Workspace.Extensions)
		}
	}

	// Assert: the degradation is reported, keyed to the offending setting.
	if len(problems) < 1 {
		t.Fatalf("problems = %#v, want at least 1 (degenerate entries must be reported, CR-6)", problems)
	}
	var found int
	for _, p := range problems {
		if p.Key == "workspace.extensions" {
			found++
		}
	}
	if found < 1 {
		t.Errorf("problems with Key %q = %d, want >= 1 (problems = %#v)", "workspace.extensions", found, problems)
	}
}

// TestLoadAnalysis verifies the [analysis] table parses, defaults, and
// validates: flag_dynamic_calls (bool, default true) and
// dynamic_call_min_length (int, default 6) load from the file, and a
// non-positive dynamic_call_min_length falls back to the default and surfaces
// as a single Problem keyed "analysis.dynamic_call_min_length" — a reported
// degradation, not a failure (CR-6). The flag_dynamic_calls default of true is
// the CR-4 "dependency vs error" control: a dynamic CALLNAT is a modeled
// dependency (CALLS_DYNAMIC), not an error. Feature
// 01-workspace-and-configuration, T7, CR-4 / CR-6.
//
// Fixtures live under testdata/config/ relative to this package directory.
func TestLoadAnalysis(t *testing.T) {
	tests := []struct {
		name    string
		fixture string // path relative to the package dir
		// check inspects the loaded config and the reported problems.
		check func(t *testing.T, cfg config.Config, problems []config.Problem)
	}{
		{
			// CR-4: explicit [analysis] values load verbatim — flag turned off
			// and a valid (positive) threshold of 8 — with no Problem.
			name:    "explicit analysis values load with no problem",
			fixture: "testdata/config/analysis-off.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				if cfg.Analysis.FlagDynamicCalls {
					t.Errorf("Analysis.FlagDynamicCalls = %t, want false", cfg.Analysis.FlagDynamicCalls)
				}
				if want := 8; cfg.Analysis.DynamicCallMinLength != want {
					t.Errorf("Analysis.DynamicCallMinLength = %d, want %d", cfg.Analysis.DynamicCallMinLength, want)
				}
				if len(problems) != 0 {
					t.Errorf("problems = %#v, want none", problems)
				}
			},
		},
		{
			// CR-6 fail-safe: a non-positive dynamic_call_min_length is rejected,
			// falls back to the documented default 6, and surfaces as a single
			// Problem keyed "analysis.dynamic_call_min_length". flag_dynamic_calls,
			// unset in the file, keeps its default true.
			name:    "non-positive dynamic_call_min_length falls back to default with a problem",
			fixture: "testdata/config/analysis-bad.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				if want := 6; cfg.Analysis.DynamicCallMinLength != want {
					t.Errorf("Analysis.DynamicCallMinLength = %d, want %d (default)", cfg.Analysis.DynamicCallMinLength, want)
				}
				if !cfg.Analysis.FlagDynamicCalls {
					t.Errorf("Analysis.FlagDynamicCalls = %t, want true (default, unchanged)", cfg.Analysis.FlagDynamicCalls)
				}
				if len(problems) != 1 {
					t.Fatalf("problems = %d (%#v), want 1", len(problems), problems)
				}
				if want := "analysis.dynamic_call_min_length"; problems[0].Key != want {
					t.Errorf("problems[0].Key = %q, want %q", problems[0].Key, want)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, problems, err := config.Load(tc.fixture)
			if err != nil {
				t.Fatalf("Load(%q) error = %v, want nil", tc.fixture, err)
			}
			tc.check(t, cfg, problems)
		})
	}
}

// TestLoadLibraries verifies the [[resolution.library]] array-of-tables loads
// into Resolution.Libraries in declared order, preserving the original spelling
// of library and steplib names for display while remaining matchable
// case-insensitively (Natural is case-insensitive). A no-library config loads
// to an empty slice with no error and no problems. A [[resolution.library]]
// entry missing its required name is dropped and surfaces as a single Problem
// keyed "resolution.library.name" — a reported degradation, not a silent drop
// (CR-6). Feature 01-workspace-and-configuration, T6, FR-4 / CR-6.
//
// Fixtures live under testdata/config/ relative to this package directory.
func TestLoadLibraries(t *testing.T) {
	tests := []struct {
		name    string
		fixture string // path relative to the package dir
		// check inspects the loaded config and the reported problems.
		check func(t *testing.T, cfg config.Config, problems []config.Problem)
	}{
		{
			// FR-4: two libraries load in declared order; each preserves its
			// ordered steplib chain, with the second library's empty steplibs
			// represented as an empty (not nil-required) slice.
			name:    "libraries load in declared order with steplib chains",
			fixture: "testdata/config/libraries.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				if len(problems) != 0 {
					t.Errorf("problems = %#v, want none", problems)
				}
				libs := cfg.Resolution.Libraries
				if len(libs) != 2 {
					t.Fatalf("len(Resolution.Libraries) = %d, want 2 (libs = %#v)", len(libs), libs)
				}
				if libs[0].Name != "MYAPP" {
					t.Errorf("Libraries[0].Name = %q, want %q", libs[0].Name, "MYAPP")
				}
				if want := []string{"COMMON", "SYSTEM"}; !reflect.DeepEqual(libs[0].Steplibs, want) {
					t.Errorf("Libraries[0].Steplibs = %#v, want %#v (declared order)", libs[0].Steplibs, want)
				}
				if libs[1].Name != "COMMON" {
					t.Errorf("Libraries[1].Name = %q, want %q", libs[1].Name, "COMMON")
				}
				if len(libs[1].Steplibs) != 0 {
					t.Errorf("Libraries[1].Steplibs = %#v, want empty", libs[1].Steplibs)
				}
			},
		},
		{
			// FR-4 / Natural case-insensitivity: the original mixed-case spelling
			// is preserved for display, while an uppercase comparison matches the
			// canonical library/steplib names.
			name:    "library names preserve original spelling and match case-insensitively",
			fixture: "testdata/config/library-casevar.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				if len(problems) != 0 {
					t.Errorf("problems = %#v, want none", problems)
				}
				libs := cfg.Resolution.Libraries
				if len(libs) != 1 {
					t.Fatalf("len(Resolution.Libraries) = %d, want 1 (libs = %#v)", len(libs), libs)
				}
				if libs[0].Name != "MyApp" {
					t.Errorf("Libraries[0].Name = %q, want %q (original spelling preserved)", libs[0].Name, "MyApp")
				}
				if strings.ToUpper(libs[0].Name) != "MYAPP" {
					t.Errorf("ToUpper(Libraries[0].Name) = %q, want %q (case-insensitive match)", strings.ToUpper(libs[0].Name), "MYAPP")
				}
				if want := []string{"common", "System"}; !reflect.DeepEqual(libs[0].Steplibs, want) {
					t.Errorf("Libraries[0].Steplibs = %#v, want %#v (original spelling preserved)", libs[0].Steplibs, want)
				}
			},
		},
		{
			// FR-6 / CR-2: no library map -> load succeeds with an empty slice
			// and no problems; the workspace is a single flat namespace.
			name:    "no library map yields empty slice with no problems",
			fixture: "testdata/config/empty.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				if len(cfg.Resolution.Libraries) != 0 {
					t.Errorf("len(Resolution.Libraries) = %d, want 0 (libs = %#v)", len(cfg.Resolution.Libraries), cfg.Resolution.Libraries)
				}
				if len(problems) != 0 {
					t.Errorf("problems = %#v, want none", problems)
				}
			},
		},
		{
			// CR-6 fail-safe: a [[resolution.library]] entry missing its required
			// name is dropped and surfaces as a single Problem keyed
			// "resolution.library.name" — a reported degradation, asserted as an
			// outcome rather than a silent drop.
			name:    "library entry missing name is dropped and reported",
			fixture: "testdata/config/no-library-name.toml",
			check: func(t *testing.T, cfg config.Config, problems []config.Problem) {
				if len(cfg.Resolution.Libraries) != 0 {
					t.Errorf("len(Resolution.Libraries) = %d, want 0 (nameless entry must be dropped; libs = %#v)", len(cfg.Resolution.Libraries), cfg.Resolution.Libraries)
				}
				var found int
				for _, p := range problems {
					if p.Key == "resolution.library.name" {
						found++
					}
				}
				if found != 1 {
					t.Errorf("problems with Key %q = %d, want 1 (problems = %#v)", "resolution.library.name", found, problems)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, problems, err := config.Load(tc.fixture)
			if err != nil {
				t.Fatalf("Load(%q) error = %v, want nil", tc.fixture, err)
			}
			tc.check(t, cfg, problems)
		})
	}
}

// TestIsExcluded verifies (*Config).IsExcluded honors workspace.exclude with
// segment-anchored, case-insensitive matching: a path is excluded when any of
// its '/'- or '\'-separated segments equals an exclude entry (Natural is
// case-insensitive), and never on a mere substring. This is the indexer-facing
// surface for directory exclusions. Feature 01-workspace-and-configuration,
// Story 3, FR-2 / FR-3.
func TestIsExcluded(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workspace.Exclude = []string{"archive", ".git"}

	tests := []struct {
		name    string
		relPath string
		want    bool
	}{
		{
			name:    "first segment matches exclude entry",
			relPath: "archive/X.NSP",
			want:    true,
		},
		{
			name:    "middle segment matches exclude entry",
			relPath: "a/.git/Y",
			want:    true,
		},
		{
			name:    "no segment matches any exclude entry",
			relPath: "src/MYAPP/X.NSP",
			want:    false,
		},
		{
			// CR-6 / Natural case-insensitivity: "ARCHIVE" matches "archive".
			name:    "match is case-insensitive on directory names",
			relPath: "ARCHIVE/X.NSP",
			want:    true,
		},
		{
			// Segment-anchored, not substring: "archived" != "archive".
			name:    "segment-anchored not substring",
			relPath: "archived/X.NSP",
			want:    false,
		},
		{
			name:    "single filename segment is never excluded",
			relPath: "X.NSP",
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.IsExcluded(tc.relPath); got != tc.want {
				t.Errorf("IsExcluded(%q) = %t, want %t", tc.relPath, got, tc.want)
			}
		})
	}
}

// TestSkipReason verifies the SkipReason type distinguishes the indexer's skip
// reasons (excluded vs. too large) by stable string value, so a skip can be
// reported via logs/diagnostics rather than dropped silently. Feature
// 01-workspace-and-configuration, Story 3, NFR-6.
func TestSkipReason(t *testing.T) {
	tests := []struct {
		name   string
		reason config.SkipReason
		want   string
	}{
		{name: "excluded", reason: config.SkipExcluded, want: "excluded"},
		{name: "too large", reason: config.SkipTooLarge, want: "too_large"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(tc.reason); got != tc.want {
				t.Errorf("SkipReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFindRoot verifies workspace-root discovery walks up parent directories
// from start looking for the .natural-lsp.toml sentinel, returning the first
// (nearest/deepest) directory that contains it, and falls back to (start,
// false) when none exists. Feature 01-workspace-and-configuration, Story 1 /
// FR-1.
//
// Each case builds an isolated directory tree under t.TempDir(); the case's
// build func returns the start directory passed to FindRoot and the wantRoot
// it should resolve to.
func TestFindRoot(t *testing.T) {
	tests := []struct {
		name string
		// build constructs the tree under base and returns (start, wantRoot).
		build     func(t *testing.T, base string) (start, wantRoot string)
		wantFound bool
	}{
		{
			// FR-1: sentinel two levels up — walk-up finds the grandparent.
			name: "sentinel two levels up resolves to grandparent",
			build: func(t *testing.T, base string) (string, string) {
				root := filepath.Join(base, "ws")
				start := filepath.Join(root, "lib", "sub")
				mustMkdirAll(t, start)
				mustWriteSentinel(t, root)
				return start, root
			},
			wantFound: true,
		},
		{
			// FR-1: sentinel in start dir itself — start is the root.
			name: "sentinel in start directory resolves to start",
			build: func(t *testing.T, base string) (string, string) {
				start := filepath.Join(base, "ws")
				mustMkdirAll(t, start)
				mustWriteSentinel(t, start)
				return start, start
			},
			wantFound: true,
		},
		{
			// FR-1: nearest wins — two sentinels on the path, the deepest
			// (closest to start) is the root.
			name: "nearest sentinel wins over ancestor sentinel",
			build: func(t *testing.T, base string) (string, string) {
				outer := filepath.Join(base, "outer")
				inner := filepath.Join(outer, "inner")
				start := filepath.Join(inner, "sub")
				mustMkdirAll(t, start)
				mustWriteSentinel(t, outer)
				mustWriteSentinel(t, inner)
				return start, inner
			},
			wantFound: true,
		},
		{
			// FR-1 fallback: no sentinel anywhere up to the filesystem root —
			// return (start, false), not an error.
			name: "no sentinel falls back to start",
			build: func(t *testing.T, base string) (string, string) {
				start := filepath.Join(base, "ws", "lib")
				mustMkdirAll(t, start)
				return start, start
			},
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, wantRoot := tc.build(t, t.TempDir())

			gotRoot, gotFound := config.FindRoot(start)

			if gotFound != tc.wantFound {
				t.Errorf("FindRoot(%q) found = %t, want %t", start, gotFound, tc.wantFound)
			}
			if gotRoot != wantRoot {
				t.Errorf("FindRoot(%q) root = %q, want %q", start, gotRoot, wantRoot)
			}
			if !filepath.IsAbs(gotRoot) {
				t.Errorf("FindRoot(%q) root = %q, want an absolute path", start, gotRoot)
			}
		})
	}
}

// TestFindRootRelativeStartResolvesAbsolute verifies the FindRoot contract that
// the returned root is always absolute even when start is a relative path: a
// relative start is resolved with filepath.Abs before (and as) the fallback.
// Feature 01-workspace-and-configuration, FR-1.
func TestFindRootRelativeStartResolvesAbsolute(t *testing.T) {
	// chdir into an isolated temp dir so the relative "." has a known, sentinel-free base.
	base := t.TempDir()
	t.Chdir(base)

	gotRoot, gotFound := config.FindRoot(".")

	if gotFound {
		t.Errorf("FindRoot(\".\") found = true, want false (no sentinel in temp dir)")
	}
	if !filepath.IsAbs(gotRoot) {
		t.Errorf("FindRoot(\".\") root = %q, want an absolute path", gotRoot)
	}
}

// sampleGoldenPath is the committed golden sample .natural-lsp.toml, relative
// to the package directory. It is a permanent regression fixture: the
// deterministic, fully-commented output of Sample() is pinned here and
// regenerated only behind -update.
const sampleGoldenPath = "testdata/config/sample.golden.toml"

// TestSample verifies Sample() emits a fully-commented sample .natural-lsp.toml
// that (a) round-trips through Load back to Defaults() with no problems, and
// (b) deterministically byte-matches the committed golden file. Making the
// documented defaults discoverable this way is the second S2 acceptance
// criterion. Feature 01-workspace-and-configuration, T8, CR-2 / Story 2.
func TestSample(t *testing.T) {
	// Round-trip: Sample() -> file -> Load() must reproduce Defaults() exactly,
	// with no validation problems. This is what guarantees the emitted sample is
	// itself a valid, default-equivalent config (T8 DoD: round-trip green).
	t.Run("round-trips through Load to Defaults", func(t *testing.T) {
		sample := config.Sample()

		// Arrange: write the sample to a temp .natural-lsp.toml so Load reads it
		// the same way it reads a real workspace sentinel.
		tmpFile := filepath.Join(t.TempDir(), sentinelName)
		if err := os.WriteFile(tmpFile, []byte(sample), 0o644); err != nil {
			t.Fatalf("WriteFile sample to %q: %v", tmpFile, err)
		}

		// Act.
		gotCfg, problems, err := config.Load(tmpFile)

		// Assert: a clean parse, the exact Defaults() Config, and no degradations.
		if err != nil {
			t.Fatalf("Load(sample) error = %v, want nil", err)
		}
		if want := config.Defaults(); !reflect.DeepEqual(gotCfg, want) {
			t.Errorf("Load(Sample()) config = %#v, want Defaults() %#v", gotCfg, want)
		}
		if len(problems) != 0 {
			t.Errorf("Load(Sample()) problems = %#v, want none", problems)
		}
	})

	// Golden: Sample() output is deterministic and pinned to a committed file,
	// regenerated only via -update. T8 DoD: deterministic golden output.
	t.Run("byte-matches the committed golden file", func(t *testing.T) {
		got := []byte(config.Sample())

		if *update {
			if err := os.WriteFile(sampleGoldenPath, got, 0o644); err != nil {
				t.Fatalf("WriteFile golden %q: %v", sampleGoldenPath, err)
			}
			return
		}

		want, err := os.ReadFile(sampleGoldenPath)
		if err != nil {
			t.Fatalf("ReadFile golden %q: %v (run with -update to regenerate)", sampleGoldenPath, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Sample() output does not match golden %s\n got:\n%s\nwant:\n%s\n(run go test ./internal/config/ -update to regenerate)",
				sampleGoldenPath, got, want)
		}
	})
}

// TestBootstrap verifies the process-startup wiring that resolves the workspace
// root, loads the config, and makes both the resolved root and any degradation
// observable on an injected logger — never crashing on a missing sentinel or a
// bad config (graceful degradation).
//
// Bootstrap(start, workspaceHint, logger) composes FindRoot + Load:
//   - FindRoot found  -> use that root.
//   - FindRoot missing + workspaceHint != "" -> use the hint (the LSP
//     initialize rootUri/workspaceFolders fallback, plan Open question 5).
//   - FindRoot missing + workspaceHint == "" -> use start (existing fallback).
//
// It logs "resolved workspace root: <root> (sentinel found: true/false)" at
// Info and one Warn line per Problem. It always returns a usable Config and a
// nil error.
//
// Feature 01-workspace-and-configuration, T9 (FR-1 Story 1 criterion 3, CR-6).
func TestBootstrap(t *testing.T) {
	tests := []struct {
		name string
		// build prepares the workspace under base and returns the
		// (start, workspaceHint) passed to Bootstrap plus the expected root.
		build func(t *testing.T, base string) (start, hint, wantRoot string)
		// assertCfg checks the effective Config (beyond the root/log checks).
		assertCfg func(t *testing.T, cfg config.Config)
		// wantLogSubstrings must all appear in the captured log output.
		wantLogSubstrings []string
	}{
		{
			// FR-1 S1c3 + Story 2: sentinel present, valid override loaded.
			name: "sentinel present uses found root and loads its config",
			build: func(t *testing.T, base string) (string, string, string) {
				root := base
				toml := "[workspace]\nextensions = [\".NSP\"]\n"
				if err := os.WriteFile(filepath.Join(root, sentinelName), []byte(toml), 0o644); err != nil {
					t.Fatalf("WriteFile sentinel: %v", err)
				}
				return root, "", root
			},
			assertCfg: func(t *testing.T, cfg config.Config) {
				want := []string{".NSP"}
				if !reflect.DeepEqual(cfg.Workspace.Extensions, want) {
					t.Errorf("cfg.Workspace.Extensions = %#v, want %#v", cfg.Workspace.Extensions, want)
				}
			},
			wantLogSubstrings: []string{"sentinel found: true"},
		},
		{
			// FR-1 S1c2 / Open question 5: no sentinel -> fall back to the
			// editor-provided workspace folder (workspaceHint), not start.
			name: "no sentinel falls back to workspace hint with defaults",
			build: func(t *testing.T, base string) (string, string, string) {
				start := filepath.Join(base, "sub")
				mustMkdirAll(t, start)
				// base has no sentinel; hint is base.
				return start, base, base
			},
			assertCfg: func(t *testing.T, cfg config.Config) {
				if want := config.Defaults(); !reflect.DeepEqual(cfg, want) {
					t.Errorf("cfg = %#v, want Defaults() %#v", cfg, want)
				}
			},
			wantLogSubstrings: []string{"sentinel found: false"},
		},
		{
			// CR-6: a bad value degrades to its default and the substitution is
			// reported as an actionable log line; startup still proceeds.
			name: "bad config degrades to default and logs the problem",
			build: func(t *testing.T, base string) (string, string, string) {
				root := base
				toml := "[workspace]\nmax_file_size = -1\n"
				if err := os.WriteFile(filepath.Join(root, sentinelName), []byte(toml), 0o644); err != nil {
					t.Fatalf("WriteFile sentinel: %v", err)
				}
				return root, "", root
			},
			assertCfg: func(t *testing.T, cfg config.Config) {
				if cfg.Workspace.MaxFileSize != 5_000_000 {
					t.Errorf("cfg.Workspace.MaxFileSize = %d, want default 5000000", cfg.Workspace.MaxFileSize)
				}
			},
			wantLogSubstrings: []string{"workspace.max_file_size"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: a capture logger so log lines are asserted without
			// touching os.Stderr (T9 DoD: injected logger, no real stdio).
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))
			start, hint, wantRoot := tc.build(t, t.TempDir())

			// Act.
			gotRoot, gotCfg, err := config.Bootstrap(start, hint, logger)

			// Assert: never hard-fails (CR-6 graceful degradation).
			if err != nil {
				t.Fatalf("Bootstrap(%q, %q) error = %v, want nil", start, hint, err)
			}
			if gotRoot != wantRoot {
				t.Errorf("Bootstrap(%q, %q) root = %q, want %q", start, hint, gotRoot, wantRoot)
			}
			tc.assertCfg(t, gotCfg)

			logged := logBuf.String()
			for _, want := range tc.wantLogSubstrings {
				if !strings.Contains(logged, want) {
					t.Errorf("Bootstrap log missing %q; got:\n%s", want, logged)
				}
			}
		})
	}
}

// TestLoad_ExtensionTypes_ValidEntry verifies that a valid [workspace.extension_types]
// entry parses and the map contains the normalized key-value pair. Feature
// 02-object-type-recognition, Task 7 (Behavior B).
//
// A valid entry like [workspace.extension_types] ".NAT" = "program" must result in
// ExtensionTypes[".NAT"] == "program" (with key normalized to upper-case
// dot-prefixed form on load).
func TestLoad_ExtensionTypes_ValidEntry(t *testing.T) {
	const fixture = "testdata/config/extension-types-valid.toml"

	// Arrange: create the fixture.
	tmpDir := t.TempDir()
	if err := os.WriteFile(tmpDir+"/"+sentinelName, []byte(
		"[workspace.extension_types]\n"+
			"\".NAT\" = \"program\"\n"+
			"\".myext\" = \"subprogram\"\n",
	), 0o644); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}

	// Act.
	cfg, problems, err := config.Load(tmpDir + "/" + sentinelName)

	// Assert: parse succeeds, no problems, extension_types contains both entries
	// with normalized keys (upper-case dot-prefixed).
	if err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", fixture, err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %#v, want none", problems)
	}
	if got, want := cfg.Workspace.ExtensionTypes[".NAT"], "program"; got != want {
		t.Errorf("ExtensionTypes[\".NAT\"] = %q, want %q", got, want)
	}
	if got, want := cfg.Workspace.ExtensionTypes[".MYEXT"], "subprogram"; got != want {
		t.Errorf("ExtensionTypes[\".MYEXT\"] = %q, want %q", got, want)
	}
}

// TestLoad_ExtensionTypes_InvalidValue verifies that an invalid object-type
// value in [workspace.extension_types] is rejected, the entry is dropped, and a
// Problem is reported (CR-6 fail-safe). Feature 02-object-type-recognition,
// Task 7 (Behavior B).
//
// A bad value like [workspace.extension_types] ".NAT" = "widget" (where "widget" is not
// a recognized object type) must result in: the entry absent from ExtensionTypes,
// and one Problem added with Key "workspace.extension_types".
func TestLoad_ExtensionTypes_InvalidValue(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(tmpDir+"/"+sentinelName, []byte(
		"[workspace.extension_types]\n"+
			"\".NAT\" = \"widget\"\n",
	), 0o644); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}

	// Act.
	cfg, problems, err := config.Load(tmpDir + "/" + sentinelName)

	// Assert: parse succeeds, entry is dropped, and one Problem is reported.
	if err != nil {
		t.Fatalf("Load error = %v, want nil", err)
	}

	// Verify the bad entry is not in the map.
	if len(cfg.Workspace.ExtensionTypes) != 0 {
		t.Errorf("ExtensionTypes = %#v, want empty (invalid entry dropped)", cfg.Workspace.ExtensionTypes)
	}

	// Verify exactly one Problem is reported with Key "workspace.extension_types".
	var found int
	for _, p := range problems {
		if p.Key == "workspace.extension_types" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("problems with Key %q = %d, want 1 (problems = %#v)", "workspace.extension_types", found, problems)
	}
}

// TestSample_IncludesExtensionTypes verifies that Sample() output includes a
// commented-out [workspace.extension_types] example block. Feature
// 02-object-type-recognition, Task 7 (Behavior B).
func TestSample_IncludesExtensionTypes(t *testing.T) {
	sample := config.Sample()

	// The sample must contain the [workspace.extension_types] section (as a comment).
	if !strings.Contains(sample, "[workspace.extension_types]") {
		t.Errorf("Sample() does not contain [workspace.extension_types] section:\n%s", sample)
	}

	// It must also include an example mapping.
	if !strings.Contains(sample, "\".NAT\"") {
		t.Errorf("Sample() does not contain example \".NAT\" key:\n%s", sample)
	}
}

// TestDefaults_IncludesExtendedExtensions verifies that Defaults().Workspace.Extensions
// includes all 15 extensions: the 10 core types (.NSP .NSN .NSS .NSC .NSM .NSL .NSG
// .NSA .NSH .NSD) plus the 5 extended types (.NS4 .NS7 .NS3 .NS8 .NST). Feature
// 02-object-type-recognition, Task 7 (Behavior A).
func TestDefaults_IncludesExtendedExtensions(t *testing.T) {
	cfg := config.Defaults()

	wantExtensions := []string{
		".NSP", ".NSN", ".NSS", ".NSC", ".NSM",
		".NSL", ".NSG", ".NSA", ".NSH", ".NSD",
		".NS4", ".NS7", ".NS3", ".NS8", ".NST",
	}

	// Assert: length matches.
	if len(cfg.Workspace.Extensions) != len(wantExtensions) {
		t.Errorf("len(Workspace.Extensions) = %d, want %d", len(cfg.Workspace.Extensions), len(wantExtensions))
	}

	// Assert: all expected extensions are present.
	if !reflect.DeepEqual(cfg.Workspace.Extensions, wantExtensions) {
		t.Errorf("Workspace.Extensions = %#v, want %#v", cfg.Workspace.Extensions, wantExtensions)
	}
}

// TestValidate_ExtensionTypes_ValueNormalized verifies that extension-type values
// in [workspace.extension_types] are case-normalized to lowercase before storage.
// Natural is case-insensitive; stored values must match the lowercase canonical
// forms so they can be reliably compared to model.ObjectType constants (which are
// lowercase strings). Feature 02-object-type-recognition, Remediation R1.
//
// A mixed-case config entry like [workspace.extension_types] ".NAT" = "PROGRAM"
// must normalize both key (to upper-cased, dot-prefixed ".NAT") and value (to
// lowercase "program") so ExtensionTypes[".NAT"] == "program", not "PROGRAM".
// Similarly ".NSX" = "SubProgram" normalizes to "subprogram"; an invalid value
// like ".NSZ" = "UNKNOWN_TYPE" is rejected and the entry absent, with one Problem
// reported (CR-6).
func TestValidate_ExtensionTypes_ValueNormalized(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(tmpDir+"/"+sentinelName, []byte(
		"[workspace.extension_types]\n"+
			"\".NAT\" = \"PROGRAM\"\n"+
			"\".NSX\" = \"SubProgram\"\n"+
			"\".NSZ\" = \"UNKNOWN_TYPE\"\n",
	), 0o644); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}

	// Act.
	cfg, problems, err := config.Load(tmpDir + "/" + sentinelName)

	// Assert: parse succeeds.
	if err != nil {
		t.Fatalf("Load error = %v, want nil", err)
	}

	// Assert: ".NAT" = "PROGRAM" normalizes to "program" (lowercase).
	if got, want := cfg.Workspace.ExtensionTypes[".NAT"], "program"; got != want {
		t.Errorf("ExtensionTypes[\".NAT\"] = %q, want %q (mixed-case value must normalize to lowercase)", got, want)
	}

	// Assert: ".NSX" = "SubProgram" normalizes to "subprogram" (lowercase).
	if got, want := cfg.Workspace.ExtensionTypes[".NSX"], "subprogram"; got != want {
		t.Errorf("ExtensionTypes[\".NSX\"] = %q, want %q (mixed-case value must normalize to lowercase)", got, want)
	}

	// Assert: ".NSZ" = "UNKNOWN_TYPE" is rejected and entry absent.
	if _, exists := cfg.Workspace.ExtensionTypes[".NSZ"]; exists {
		t.Errorf("ExtensionTypes contains invalid entry .NSZ (should be rejected); got %#v", cfg.Workspace.ExtensionTypes)
	}

	// Assert: exactly one Problem reported for the invalid value.
	var found int
	for _, p := range problems {
		if p.Key == "workspace.extension_types" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("problems with Key %q = %d, want 1 (problems = %#v)", "workspace.extension_types", found, problems)
	}
}

// TestValidate_ExtensionTypes_CollisionReported verifies that when two or more
// TOML keys in [workspace.extension_types] normalize to the same extension (e.g.
// ".nat" and ".NAT" both become ".NAT"), exactly one Problem is emitted for the
// collision and the duplicate is rejected. First-seen wins: in sorted order of
// the original keys, the first key's value is kept; later duplicates are dropped.
// Feature 02-object-type-recognition, Remediation R2 (CR-6 fail-safe).
//
// TOML key normalization is case-insensitive and enforces a leading dot. Two
// keys like ".nat" = "program" and ".NAT" = "map" both normalize to ".NAT", so
// one is a duplicate. Go map iteration is random, but key normalization is
// deterministic: sorted order of the original string keys is used to ensure
// first-seen wins (alphabetically, ".NAT" < ".nat", so ".NAT"'s value "map" is
// kept and ".nat"'s value "program" is the duplicate that must trigger a Problem).
func TestValidate_ExtensionTypes_CollisionReported(t *testing.T) {
	tests := []struct {
		name             string
		tomlContent      string
		wantCollisionKey string // the normalized key that was collided on
		wantKeptValue    string // the value from the first-seen (sorted) key
		wantDroppedValue string // the value from the colliding (second) key
		wantProblemCount int    // should be exactly 1 for each collision
	}{
		{
			// ".NAT" (upper) < ".nat" (lower) in ASCII order (dot=46, N=78, n=110).
			// ".NAT" is seen first in sorted order, so ".NAT"'s value "map" is kept
			// and ".nat"'s value "program" is the duplicate.
			name: "collision on .nat / .NAT uses first-seen-in-sorted-order",
			tomlContent: "[workspace.extension_types]\n" +
				"\".nat\" = \"program\"\n" +
				"\".NAT\" = \"map\"\n",
			wantCollisionKey: ".NAT",
			wantKeptValue:    "map",     // from ".NAT" (first in sorted order)
			wantDroppedValue: "program", // from ".nat" (second in sorted order)
			wantProblemCount: 1,
		},
		{
			// "nat" < ".NAT" in ASCII order (n=110 > dot=46).
			// ".NAT" is seen first in sorted order, so ".NAT"'s value "ddm" is kept
			// and "nat"'s value "subprogram" is the duplicate.
			name: "collision on nat / .NAT uses first-seen-in-sorted-order",
			tomlContent: "[workspace.extension_types]\n" +
				"\"nat\" = \"subprogram\"\n" +
				"\".NAT\" = \"ddm\"\n",
			wantCollisionKey: ".NAT",
			wantKeptValue:    "ddm",        // from ".NAT" (first in sorted order: dot < n)
			wantDroppedValue: "subprogram", // from "nat" (second in sorted order)
			wantProblemCount: 1,
		},
		{
			// No collision: only ".NAT" is present, no duplicate key after normalization.
			name: "no collision: single extension present",
			tomlContent: "[workspace.extension_types]\n" +
				"\".NAT\" = \"program\"\n",
			wantCollisionKey: ".NAT",
			wantKeptValue:    "program",
			wantDroppedValue: "", // no collision
			wantProblemCount: 0,  // no Problem
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(tmpDir+"/"+sentinelName, []byte(tc.tomlContent), 0o644); err != nil {
				t.Fatalf("WriteFile fixture: %v", err)
			}

			// Act.
			cfg, problems, err := config.Load(tmpDir + "/" + sentinelName)

			// Assert: parse succeeds.
			if err != nil {
				t.Fatalf("Load error = %v, want nil", err)
			}

			// Assert: the collision key's value is the first-seen (sorted) one.
			if got, want := cfg.Workspace.ExtensionTypes[tc.wantCollisionKey], tc.wantKeptValue; got != want {
				t.Errorf("ExtensionTypes[%q] = %q, want %q (should keep first-seen value in sorted key order)",
					tc.wantCollisionKey, got, want)
			}

			// Assert: exactly the expected number of Problems.
			var collisionProblems int
			for _, p := range problems {
				if p.Key == "workspace.extension_types" {
					collisionProblems++
				}
			}
			if collisionProblems != tc.wantProblemCount {
				t.Errorf("problems with Key %q = %d, want %d (problems = %#v)",
					"workspace.extension_types", collisionProblems, tc.wantProblemCount, problems)
			}

			// Assert: if there was a collision, verify the dropped value doesn't appear elsewhere
			// (as a regression check that the duplicate was truly dropped, not stored with a different key).
			if tc.wantDroppedValue != "" {
				for key, value := range cfg.Workspace.ExtensionTypes {
					if value == tc.wantDroppedValue && key != tc.wantCollisionKey {
						t.Errorf("ExtensionTypes[%q] = %q (the dropped collision value should not appear elsewhere; got %#v)",
							key, value, cfg.Workspace.ExtensionTypes)
					}
				}
			}
		})
	}
}

// TestValidate_ExtensionTypes_DegenerateKeysDropped verifies that degenerate
// keys in [workspace.extension_types] (empty strings, whitespace-only, or
// dot-only entries) are rejected, absent from ExtensionTypes, and reported as
// Problems (CR-6 fail-safe). Feature 02-object-type-recognition, Remediation R3.
//
// A "degenerate" key is one whose normalized form after strings.TrimSpace +
// strings.ToUpper + ensuring leading dot results in just "." (no extension
// component beyond the dot). Such entries can never match a real file extension
// and must be dropped with a reported Problem per CR-6.
//
// Test cases:
// 1. "" = "program" → absent from ExtensionTypes, one Problem with Key "workspace.extension_types"
// 2. "  " = "subprogram" → absent, one Problem
// 3. "." = "copycode" → absent, one Problem
// 4. Regression: ".NAT" = "program" present alongside degenerate ones; only valid keys kept.
func TestValidate_ExtensionTypes_DegenerateKeysDropped(t *testing.T) {
	tests := []struct {
		name                string
		tomlContent         string
		wantValidKey        string // expected valid key that should be kept (if any)
		wantValidValue      string // expected value for the valid key
		wantProblemCountMin int    // at least this many problems expected (1 per degenerate key)
	}{
		{
			name: "empty string key is dropped with problem",
			tomlContent: "[workspace.extension_types]\n" +
				"\"\" = \"program\"\n",
			wantValidKey:        "",
			wantValidValue:      "",
			wantProblemCountMin: 1,
		},
		{
			name: "whitespace-only key is dropped with problem",
			tomlContent: "[workspace.extension_types]\n" +
				"\"  \" = \"subprogram\"\n",
			wantValidKey:        "",
			wantValidValue:      "",
			wantProblemCountMin: 1,
		},
		{
			name: "dot-only key is dropped with problem",
			tomlContent: "[workspace.extension_types]\n" +
				"\".\" = \"copycode\"\n",
			wantValidKey:        "",
			wantValidValue:      "",
			wantProblemCountMin: 1,
		},
		{
			name: "valid key kept alongside degenerate ones",
			tomlContent: "[workspace.extension_types]\n" +
				"\"\" = \"program\"\n" +
				"\".NAT\" = \"program\"\n" +
				"\"  \" = \"subprogram\"\n" +
				"\".\" = \"copycode\"\n",
			wantValidKey:        ".NAT",
			wantValidValue:      "program",
			wantProblemCountMin: 3, // three degenerate keys should be reported
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(tmpDir+"/"+sentinelName, []byte(tc.tomlContent), 0o644); err != nil {
				t.Fatalf("WriteFile fixture: %v", err)
			}

			// Act.
			cfg, problems, err := config.Load(tmpDir + "/" + sentinelName)

			// Assert: parse succeeds.
			if err != nil {
				t.Fatalf("Load error = %v, want nil", err)
			}

			// Assert: degenerate keys are absent — ExtensionTypes should not contain ".", "..", etc.
			for key := range cfg.Workspace.ExtensionTypes {
				trimmedKey := strings.Trim(key, ".")
				if trimmedKey == "" {
					t.Errorf("ExtensionTypes contains degenerate key %q; degenerate keys must be dropped (got %#v)", key, cfg.Workspace.ExtensionTypes)
				}
			}

			// Assert: if a valid key was expected, it is present with the correct value.
			if tc.wantValidKey != "" {
				if got, want := cfg.Workspace.ExtensionTypes[tc.wantValidKey], tc.wantValidValue; got != want {
					t.Errorf("ExtensionTypes[%q] = %q, want %q (valid key must be kept)", tc.wantValidKey, got, want)
				}
			}

			// Assert: at least the expected number of Problems with Key "workspace.extension_types".
			var degenerateProblems int
			for _, p := range problems {
				if p.Key == "workspace.extension_types" {
					degenerateProblems++
				}
			}
			if degenerateProblems < tc.wantProblemCountMin {
				t.Errorf("problems with Key %q = %d, want >= %d (degenerate keys must be reported; problems = %#v)",
					"workspace.extension_types", degenerateProblems, tc.wantProblemCountMin, problems)
			}
		})
	}
}
