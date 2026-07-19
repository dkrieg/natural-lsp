package workspace

import (
	"testing"

	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestNamesWithPrefix_FlatNamespace_AllPrefixMatches verifies that NamesWithPrefix
// returns all prefix-matching candidates of the given type in a flat namespace
// (no library map configured).
//
// Behavior (Task 2, T2 AC(a)):
// - With no library map: all prefix matches of the given type are returned
// - Empty prefix returns all reachable of that type
// - Deterministic sorted order by name
func TestNamesWithPrefix_FlatNamespace_AllPrefixMatches(t *testing.T) {
	t.Helper()

	tests := []struct {
		name            string
		setup           func(*Index)
		prefix          string
		typ             model.ObjectType
		referencingPath string
		cfg             *config.Config
		wantCount       int
		wantNames       []string
		description     string
	}{
		{
			name: "flat-namespace-prefix-match",
			description: "no library map: all subprograms starting with 'MY' are returned " +
				"(Task 2 AC(a))",
			setup: func(idx *Index) {
				idx.Add("MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("MYUTIL.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("SHARED.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("MAIN.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
			},
			prefix:          "MY",
			typ:             model.ObjectSubprogram,
			referencingPath: "CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{}, // no library map
				},
			},
			wantCount: 2,
			wantNames: []string{"MYSUB", "MYUTIL"},
		},
		{
			name: "flat-namespace-empty-prefix",
			description: "empty prefix returns all reachable of that type " +
				"(Task 2 AC(e))",
			setup: func(idx *Index) {
				idx.Add("SUB1.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("SUB2.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("PROG1.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
			},
			prefix:          "",
			typ:             model.ObjectSubprogram,
			referencingPath: "CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{},
				},
			},
			wantCount: 2,
			wantNames: []string{"SUB1", "SUB2"},
		},
		{
			name: "flat-namespace-unknown-prefix",
			description: "unknown prefix returns empty non-nil slice " +
				"(Task 2 AC(f))",
			setup: func(idx *Index) {
				idx.Add("MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("SHARED.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
			},
			prefix:          "UNKNOWN",
			typ:             model.ObjectSubprogram,
			referencingPath: "CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{},
				},
			},
			wantCount: 0,
			wantNames: []string{},
		},
		{
			name: "flat-namespace-type-filter",
			description: "type filter excludes wrong-type objects " +
				"(Task 2 AC(d))",
			setup: func(idx *Index) {
				idx.Add("MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("MYPROG.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
			},
			prefix:          "MY",
			typ:             model.ObjectSubprogram,
			referencingPath: "CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{},
				},
			},
			wantCount: 1,
			wantNames: []string{"MYSUB"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			idx := &Index{}
			tc.setup(idx)

			// Call NamesWithPrefix
			candidates := idx.NamesWithPrefix(tc.prefix, tc.typ, tc.referencingPath, tc.cfg)

			// Verify non-nil slice
			if candidates == nil {
				t.Errorf("NamesWithPrefix returned nil, want non-nil slice")
				return
			}

			// Verify count
			if len(candidates) != tc.wantCount {
				t.Errorf("NamesWithPrefix returned %d candidates, want %d",
					len(candidates), tc.wantCount)
				if len(candidates) > 0 {
					for _, c := range candidates {
						t.Logf("  got: %q (type=%v)", c.Path, c.Type)
					}
				}
				return
			}

			// Verify object names (deterministic sorted order)
			for i, candidate := range candidates {
				// Extract object name from path
				var objName string
				for j := len(candidate.Path) - 1; j >= 0; j-- {
					if candidate.Path[j] == '/' {
						objName = candidate.Path[j+1:]
						break
					}
				}
				if objName == "" {
					objName = candidate.Path
				}
				// Strip extension to get just the name stem
				for j := len(objName) - 1; j >= 0; j-- {
					if objName[j] == '.' {
						objName = objName[:j]
						break
					}
				}

				if objName != tc.wantNames[i] {
					t.Errorf("NamesWithPrefix candidate %d: name=%q, want %q",
						i, objName, tc.wantNames[i])
				}
			}
		})
	}
}

// TestNamesWithPrefix_WithLibraryMap verifies that NamesWithPrefix respects the
// steplib chain and filters to reachable candidates only.
//
// Behavior (Task 2, T2 AC(b), AC(c)):
// - Only chain-reachable candidates are returned
// - Same-named object in an unreachable library is excluded
// - Cross-library name collision: the steplib winner (first in chain) is kept
func TestNamesWithPrefix_WithLibraryMap(t *testing.T) {
	t.Helper()

	tests := []struct {
		name              string
		setup             func(*Index)
		prefix            string
		typ               model.ObjectType
		referencingPath   string
		cfg               *config.Config
		wantCount         int
		wantPaths         []string
		wantWinnerLibrary string
		description       string
	}{
		{
			name: "library-map-chain-reachable-only",
			description: "only steplib-chain reachable candidates returned; " +
				"unreachable library excluded (Task 2 AC(b))",
			setup: func(idx *Index) {
				// APP library
				idx.Add("APP/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				// COMMON library (in the chain)
				idx.Add("COMMON/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				// UNREACHABLE library (not in chain)
				idx.Add("UNREACHABLE/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				// Another subprogram in COMMON
				idx.Add("COMMON/UTIL.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
			},
			prefix:          "MY",
			typ:             model.ObjectSubprogram,
			referencingPath: "APP/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "APP", Path: "APP", Steplibs: []string{"COMMON"}},
						{Name: "COMMON", Path: "COMMON", Steplibs: []string{}},
						// UNREACHABLE is not declared, so it's not in the chain
					},
				},
			},
			wantCount: 2,
			wantPaths: []string{"APP/MYSUB.NSN", "COMMON/MYSUB.NSN"},
		},
		{
			name: "library-map-cross-library-collision-steplib-winner",
			description: "same-named object in multiple reachable libraries: " +
				"steplib winner (first in chain) is kept (Task 2 AC(c))",
			setup: func(idx *Index) {
				// COMMON has the object first
				idx.Add("COMMON/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				// SYSTEM has the same object (implicit)
				idx.Add("SYSTEM/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				// Another object that doesn't collide
				idx.Add("COMMON/OTHER.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
			},
			prefix:          "MY",
			typ:             model.ObjectSubprogram,
			referencingPath: "APP/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "APP", Path: "APP", Steplibs: []string{"COMMON"}},
						{Name: "COMMON", Path: "COMMON", Steplibs: []string{}},
						// SYSTEM is implicit
					},
				},
			},
			wantCount:         1,
			wantPaths:         []string{"COMMON/MYSUB.NSN"},
			wantWinnerLibrary: "COMMON",
		},
		{
			name: "library-map-undeclared-path-fallback-to-flat",
			description: "caller in undeclared path falls back to flat-namespace " +
				"(Task 2, OQ-3(a))",
			setup: func(idx *Index) {
				idx.Add("APP/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("COMMON/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("OTHER/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
			},
			prefix:          "MY",
			typ:             model.ObjectSubprogram,
			referencingPath: "UNDECLARED/CALLER.NSP", // not under any declared library
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "APP", Path: "APP", Steplibs: []string{"COMMON"}},
						{Name: "COMMON", Path: "COMMON", Steplibs: []string{}},
					},
				},
			},
			// In flat namespace, all three should be returned
			wantCount: 3,
			wantPaths: []string{"APP/MYSUB.NSN", "COMMON/MYSUB.NSN", "OTHER/MYSUB.NSN"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			idx := &Index{}
			tc.setup(idx)

			// Call NamesWithPrefix
			candidates := idx.NamesWithPrefix(tc.prefix, tc.typ, tc.referencingPath, tc.cfg)

			// Verify non-nil slice
			if candidates == nil {
				t.Errorf("NamesWithPrefix returned nil, want non-nil slice")
				return
			}

			// Verify count
			if len(candidates) != tc.wantCount {
				t.Errorf("NamesWithPrefix returned %d candidates, want %d",
					len(candidates), tc.wantCount)
				if len(candidates) > 0 {
					for _, c := range candidates {
						t.Logf("  got: path=%q, lib=%q", c.Path, c.Library)
					}
				}
				return
			}

			// Verify paths (in sorted order)
			for i, candidate := range candidates {
				if candidate.Path != tc.wantPaths[i] {
					t.Errorf("NamesWithPrefix candidate %d: path=%q, want %q",
						i, candidate.Path, tc.wantPaths[i])
				}
			}

			// If collision test, verify steplib winner
			if tc.wantWinnerLibrary != "" && len(candidates) > 0 {
				if candidates[0].Library != tc.wantWinnerLibrary {
					t.Errorf("NamesWithPrefix steplib winner: lib=%q, want %q",
						candidates[0].Library, tc.wantWinnerLibrary)
				}
			}
		})
	}
}

// TestNamesWithPrefix_DeterministicSorted verifies that results are deterministic
// and sorted by object name.
//
// Behavior (Task 2, T2 AC(g)):
// - Results are sorted by name for determinism
// - Multiple calls with same args return identical ordering
func TestNamesWithPrefix_DeterministicSorted(t *testing.T) {
	t.Helper()

	idx := &Index{}
	idx.Add("ZEBRA.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("APPLE.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("MANGO.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("BANANA.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	cfg := &config.Config{
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{},
		},
	}

	// Call multiple times and verify consistent ordering
	for call := 0; call < 3; call++ {
		candidates := idx.NamesWithPrefix("", model.ObjectSubprogram, "CALLER.NSP", cfg)

		if len(candidates) != 4 {
			t.Fatalf("call %d: got %d candidates, want 4", call, len(candidates))
		}

		// Names should be alphabetically sorted
		expectedOrder := []string{"APPLE", "BANANA", "MANGO", "ZEBRA"}
		for i, expected := range expectedOrder {
			// Extract name from path
			path := candidates[i].Path
			name := path[:len(path)-4] // strip .NSN extension

			if name != expected {
				t.Errorf("call %d, position %d: got %q, want %q",
					call, i, name, expected)
			}
		}
	}
}

// TestNamesWithPrefix_CaseInsensitivePrefix verifies that the prefix matching
// is case-insensitive, following Natural's convention.
func TestNamesWithPrefix_CaseInsensitivePrefix(t *testing.T) {
	t.Helper()

	idx := &Index{}
	idx.Add("MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("MYUTIL.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("SHARED.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	cfg := &config.Config{
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{},
		},
	}

	// Test with different case variants of the same prefix
	testCases := []string{"MY", "my", "My", "mY"}

	for _, prefix := range testCases {
		candidates := idx.NamesWithPrefix(prefix, model.ObjectSubprogram, "CALLER.NSP", cfg)

		if len(candidates) != 2 {
			t.Errorf("prefix %q: got %d candidates, want 2", prefix, len(candidates))
		}
	}
}

// TestNamesWithPrefix_EmptyIndex verifies that querying an empty index returns
// a non-nil empty slice.
func TestNamesWithPrefix_EmptyIndex(t *testing.T) {
	t.Helper()

	idx := &Index{}

	cfg := &config.Config{
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{},
		},
	}

	candidates := idx.NamesWithPrefix("ANY", model.ObjectSubprogram, "CALLER.NSP", cfg)

	if candidates == nil {
		t.Error("NamesWithPrefix on empty index returned nil, want non-nil slice")
	}
	if len(candidates) != 0 {
		t.Errorf("NamesWithPrefix on empty index returned %d candidates, want 0",
			len(candidates))
	}
}

// TestNamesWithPrefix_ReusesResolutionLogic verifies that NamesWithPrefix
// reuses the same reachability logic as resolveByName (Task 2 DoD).
// This is tested by mirroring a resolveByName fixture: a caller in APP
// should see objects in APP and its steplib COMMON, but not SYSTEM-only
// or undeclared-library objects.
func TestNamesWithPrefix_ReusesResolutionLogic(t *testing.T) {
	t.Helper()

	cfg := &config.Config{
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{
				{Name: "APP", Path: "APP", Steplibs: []string{"COMMON"}},
				{Name: "COMMON", Path: "COMMON", Steplibs: []string{}},
			},
		},
	}

	idx := &Index{}
	// Objects in the caller's chain (APP -> COMMON -> SYSTEM)
	idx.Add("APP/VISIBLE.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("COMMON/VISIBLE.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	// Object only in SYSTEM (represented as unDeclaration, thus unreachable)
	idx.Add("SYSTEM/VISIBLE.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	// Object in an unreachable library
	idx.Add("OTHER/VISIBLE.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	candidates := idx.NamesWithPrefix("VIS", model.ObjectSubprogram, "APP/CALLER.NSP", cfg)

	// Should return only APP and COMMON, not SYSTEM or OTHER (OQ-5: non-transitive,
	// and OTHER is not in the chain)
	if len(candidates) != 2 {
		t.Errorf("NamesWithPrefix reachability: got %d candidates, want 2 (APP+COMMON only)",
			len(candidates))
		for _, c := range candidates {
			t.Logf("  candidate: %q lib=%q", c.Path, c.Library)
		}
		return
	}

	// Verify the candidates are the reachable ones (APP first by chain order)
	expectedLibs := []string{"APP", "COMMON"}
	for i, expected := range expectedLibs {
		if candidates[i].Library != expected {
			t.Errorf("NamesWithPrefix candidate %d: lib=%q, want %q (chain order)",
				i, candidates[i].Library, expected)
		}
	}
}
