package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// cancellingAnalyzer is a test Analyzer that records how many files it analyzed
// and, on the first Analyze call, cancels a supplied context. It lets a
// cancellation-during-build test synchronize deterministically on the analyzer
// (no sleeps): the ctx is cancelled from inside the build, so the next
// per-file ctx.Err() check aborts the loop.
type cancellingAnalyzer struct {
	mu     sync.Mutex
	count  int
	cancel context.CancelFunc
}

func (a *cancellingAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	a.mu.Lock()
	a.count++
	first := a.count == 1
	a.mu.Unlock()
	if first && a.cancel != nil {
		// Cancel after the first file is analyzed. The build checks ctx.Err()
		// at the top of the next iteration and returns early.
		a.cancel()
	}
	return model.FileAnalysis{ObjectType: model.ObjectUnknown}, nil
}

func (a *cancellingAnalyzer) ExtractVariableRefs(content string) []model.VariableRef {
	return []model.VariableRef{}
}

func (a *cancellingAnalyzer) analyzed() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

func TestIndex_Add_Get(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"stores FileAnalysis by path"},
		{"retrieves FileAnalysis with ok=true"},
		{"returns ok=false for non-existent key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			idx := &Index{}

			fa := model.FileAnalysis{
				ObjectType: model.ObjectProgram,
			}

			idx.Add("test.NSP", fa)

			retrieved, ok := idx.Get("test.NSP")
			if !ok {
				t.Fatal("Index.Get returned ok=false, want true")
			}

			if retrieved.ObjectType != model.ObjectProgram {
				t.Errorf("Index.Get returned ObjectType=%v, want %v", retrieved.ObjectType, model.ObjectProgram)
			}

			_, ok = idx.Get("nonexistent.NSP")
			if ok {
				t.Error("Index.Get returned ok=true for non-existent key, want false")
			}
		})
	}
}

func TestIndex_ForEach(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"iterates over all entries"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			idx := &Index{}

			idx.Add("file1.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
			idx.Add("file2.NSP", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

			count := 0
			idx.ForEach(func(path string, fa model.FileAnalysis) {
				count++
				if path != "file1.NSP" && path != "file2.NSP" {
					t.Errorf("Unexpected path: %s", path)
				}
			})

			if count != 2 {
				t.Errorf("Index.ForEach called %d times, want 2", count)
			}
		})
	}
}

func TestIndex_Keys(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
	}{
		{"returns empty slice for empty index"},
		{"returns all stored paths"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			idx := &Index{}

			if len(idx.Keys()) != 0 {
				t.Errorf("Empty Index.Keys() length = %d, want 0", len(idx.Keys()))
			}

			idx.Add("file1.NSP", model.FileAnalysis{})
			idx.Add("file2.NSP", model.FileAnalysis{})

			keys := idx.Keys()
			if len(keys) != 2 {
				t.Errorf("Index.Keys() length = %d, want 2", len(keys))
			}
		})
	}
}

// TestBuild_CoreTypes verifies that Build() walks the workspace root and indexes
// all files in the indexed set, correctly classifying all 15 testdata/objecttype/
// fixture files. This tests FR-36 (full first-run index).
func TestBuild_CoreTypes(t *testing.T) {
	t.Helper()

	// Build a test workspace from the objecttype fixtures.
	workspaceRoot := "testdata/objecttype"

	// Create a minimal config with default settings.
	cfg := config.Defaults()

	// Create a logger for Build().
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create the analyzer.
	az := natural.New(nil)

	// Track progress invocations.
	var progressCalls []struct {
		path    string
		current int
		total   int
	}

	// Call BuildWithCache().
	idx, _, _, err := BuildWithCache(context.Background(), workspaceRoot, cfg, az, logger, "", nil, func(path string, current, total int) {
		progressCalls = append(progressCalls, struct {
			path    string
			current int
			total   int
		}{path, current, total})
	})

	if err != nil {
		t.Fatalf("BuildWithCache() returned error: %v", err)
	}

	// Verify all 16 fixture files are indexed with correct ObjectType.
	expectedFiles := map[string]model.ObjectType{
		"adapter.NS8":     model.ObjectAdapter,
		"class.NS4":       model.ObjectClass,
		"copycode.NSC":    model.ObjectCopycode,
		"copycode2.NSC":   model.ObjectCopycode,
		"ddm.NSD":         model.ObjectDDM,
		"dialog.NS3":      model.ObjectDialog,
		"function.NS7":    model.ObjectFunction,
		"global.NSG":      model.ObjectGlobalDataArea,
		"helproutine.NSH": model.ObjectHelproutine,
		"local.NSL":       model.ObjectLocalDataArea,
		"map.NSM":         model.ObjectMap,
		"parameter.NSA":   model.ObjectParameterDataArea,
		"program.NSP":     model.ObjectProgram,
		"subprogram.NSN":  model.ObjectSubprogram,
		"subroutine.NSS":  model.ObjectExternalSubroutine,
		"text.NST":        model.ObjectText,
	}

	for path, expectedType := range expectedFiles {
		fa, ok := idx.Get(path)
		if !ok {
			t.Errorf("Build() did not index %s", path)
			continue
		}
		if fa.ObjectType != expectedType {
			t.Errorf("Build() classified %s as %s, want %s", path, fa.ObjectType, expectedType)
		}
	}

	// Verify progress callback was invoked for each file.
	if len(progressCalls) != len(expectedFiles) {
		t.Errorf("Build() invoked OnProgress %d times, want %d", len(progressCalls), len(expectedFiles))
	}
}

// TestBuild_ExcludedDirectories verifies that Build() skips excluded directories
// (.git, archive, backup). This tests FR-2/FR-3 (directory exclusion).
func TestBuild_ExcludedDirectories(t *testing.T) {
	t.Helper()

	// Create a temporary workspace with an excluded directory.
	tmpDir := t.TempDir()

	// Create a file in the root.
	rootFile := filepath.Join(tmpDir, "root.NSP")
	if err := os.WriteFile(rootFile, []byte("root content\nEND\n"), 0644); err != nil {
		t.Fatalf("Failed to create root file: %v", err)
	}

	// Create an excluded directory with a file.
	excludedDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(excludedDir, 0755); err != nil {
		t.Fatalf("Failed to create excluded dir: %v", err)
	}
	excludedFile := filepath.Join(excludedDir, "ignored.NSP")
	if err := os.WriteFile(excludedFile, []byte("ignored content\nEND\n"), 0644); err != nil {
		t.Fatalf("Failed to create excluded file: %v", err)
	}

	// Create a config with default exclusions (.git, archive, backup).
	cfg := config.Defaults()

	// Create a logger.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create the analyzer.
	az := natural.New(nil)

	// Call BuildWithCache().
	idx, _, _, err := BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, func(path string, current, total int) {
		// Progress callback - just count calls.
	})

	if err != nil {
		t.Fatalf("BuildWithCache() returned error: %v", err)
	}

	// Verify root file is indexed.
	_, ok := idx.Get("root.NSP")
	if !ok {
		t.Error("BuildWithCache() did not index root file")
	}

	// Verify excluded file is NOT indexed.
	_, ok = idx.Get(".git/ignored.NSP")
	if ok {
		t.Error("BuildWithCache() incorrectly indexed file in excluded directory .git/")
	}
}

// TestBuild_TooLargeFiles verifies that BuildWithCache() skips files exceeding MaxFileSize.
// This tests FR-3 (max file size limit).
func TestBuild_TooLargeFiles(t *testing.T) {
	t.Helper()

	// Create a temporary workspace.
	tmpDir := t.TempDir()

	// Create a small file that should be indexed.
	smallFile := filepath.Join(tmpDir, "small.NSP")
	if err := os.WriteFile(smallFile, []byte("small\n"), 0644); err != nil {
		t.Fatalf("Failed to create small file: %v", err)
	}

	// Create a large file that should be skipped (> 10 bytes).
	largeFile := filepath.Join(tmpDir, "large.NSP")
	largeContent := make([]byte, 100)
	for i := range largeContent {
		largeContent[i] = 'A'
	}
	if err := os.WriteFile(largeFile, largeContent, 0644); err != nil {
		t.Fatalf("Failed to create large file: %v", err)
	}

	// Create a config with MaxFileSize = 10 bytes.
	cfg := config.Defaults()
	cfg.Workspace.MaxFileSize = 10

	// Create a logger.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create the analyzer.
	az := natural.New(nil)

	// Call BuildWithCache().
	idx, _, _, err := BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, func(path string, current, total int) {
		// Progress callback.
	})

	if err != nil {
		t.Fatalf("BuildWithCache() returned error: %v", err)
	}

	// Verify small file is indexed.
	_, ok := idx.Get("small.NSP")
	if !ok {
		t.Error("BuildWithCache() did not index small file")
	}

	// Verify large file is NOT indexed.
	_, ok = idx.Get("large.NSP")
	if ok {
		t.Error("BuildWithCache() incorrectly indexed file exceeding MaxFileSize")
	}
}

// TestBuild_ProgressCallback verifies that OnProgress is invoked for each file
// with accurate counts. This tests FR-32 (progress reporting).
func TestBuild_ProgressCallback(t *testing.T) {
	t.Helper()

	// Create a temporary workspace with multiple files.
	tmpDir := t.TempDir()

	files := []string{
		"file1.NSP",
		"file2.NSN",
		"file3.NSS",
	}

	for _, fname := range files {
		fpath := filepath.Join(tmpDir, fname)
		content := fmt.Sprintf("content for %s\nEND\n", fname)
		if err := os.WriteFile(fpath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", fname, err)
		}
	}

	// Create a config.
	cfg := config.Defaults()

	// Create a logger.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create the analyzer.
	az := natural.New(nil)

	// Track progress calls.
	var progressCalls []struct {
		path    string
		current int
		total   int
	}

	// Call BuildWithCache().
	idx, _, _, err := BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, func(path string, current, total int) {
		progressCalls = append(progressCalls, struct {
			path    string
			current int
			total   int
		}{path, current, total})
	})

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	// Verify exactly 3 progress calls (one per file).
	if len(progressCalls) != 3 {
		t.Errorf("Build() invoked OnProgress %d times, want 3", len(progressCalls))
		return
	}

	// Verify total is always 3.
	for i, call := range progressCalls {
		if call.total != 3 {
			t.Errorf("Progress call %d: total = %d, want 3", i, call.total)
		}
	}

	// Verify current increments from 1 to 3.
	expectedCurrent := []int{1, 2, 3}
	for i, call := range progressCalls {
		if call.current != expectedCurrent[i] {
			t.Errorf("Progress call %d: current = %d, want %d", i, call.current, expectedCurrent[i])
		}
	}

	// Verify all files are indexed.
	for _, fname := range files {
		_, ok := idx.Get(fname)
		if !ok {
			t.Errorf("Build() did not index %s", fname)
		}
	}
}

// TestInvalidate_INCLUDE verifies that Index.Invalidate() returns files that
// depend on a given path when an INCLUDE edge exists. This tests FR-35
// (incremental re-analysis) for direct dependencies.
func TestInvalidate_INCLUDE(t *testing.T) {
	t.Helper()

	idx := &Index{}

	// Create a dependency chain: program.NSP -> copycode.NSC (COPYCODE object)
	// program.NSP has an INCLUDE edge that references the bare name "COPYCODE"
	// (not the full path). Invalidate must match by object name, not path.
	copycode := model.FileAnalysis{
		ObjectType: model.ObjectCopycode,
		Edges:      []model.EdgeEntry{},
	}
	idx.Add("copycode.NSC", copycode)

	program := model.FileAnalysis{
		ObjectType: model.ObjectProgram,
		Edges: []model.EdgeEntry{
			{
				Kind:       model.EdgeIncludes,
				TargetName: "COPYCODE", // Bare object name, not path
			},
		},
	}
	idx.Add("program.NSP", program)

	// When copycode.NSC changes, Invalidate() should return program.NSP
	// The object name of copycode.NSC is "COPYCODE" (filename stem, uppercased).
	// program.NSP has an INCLUDE edge with TargetName="COPYCODE", so it should match.
	dependents := idx.Invalidate("copycode.NSC")

	if len(dependents) != 1 {
		t.Errorf("Invalidate() returned %d dependents, want 1", len(dependents))
		return
	}

	// Verify the dependent is present (order doesn't matter)
	found := false
	for _, dep := range dependents {
		if dep == "program.NSP" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Invalidate() missing program.NSP in dependents: %v", dependents)
	}
}

// TestInvalidate_Transitive verifies that Index.Invalidate() returns all
// transitively dependent files. If A includes B and B includes C,
// invalidating C returns both A and B. This tests FR-35 for transitive
// dependencies.
func TestInvalidate_Transitive(t *testing.T) {
	t.Helper()

	idx := &Index{}

	// Create a transitive dependency chain via object names:
	// program.NSP includes SUBPROGRAM -> subprogram.NSN includes COPYCODE2 -> copycode2.NSC
	// When copycode2.NSC changes (object name = COPYCODE2), both subprogram.NSN and
	// program.NSP should be returned as dependents.

	copycode2 := model.FileAnalysis{
		ObjectType: model.ObjectCopycode,
		Edges:      []model.EdgeEntry{},
	}
	idx.Add("copycode2.NSC", copycode2)

	subprogram := model.FileAnalysis{
		ObjectType: model.ObjectSubprogram,
		Edges: []model.EdgeEntry{
			{
				Kind:       model.EdgeIncludes,
				TargetName: "COPYCODE2", // Bare object name
			},
		},
	}
	idx.Add("subprogram.NSN", subprogram)

	program := model.FileAnalysis{
		ObjectType: model.ObjectProgram,
		Edges: []model.EdgeEntry{
			{
				Kind:       model.EdgeIncludes,
				TargetName: "SUBPROGRAM", // Bare object name
			},
		},
	}
	idx.Add("program.NSP", program)

	// When copycode2.NSC changes, Invalidate() should return both
	// subprogram.NSN and program.NSP (transitively dependent).
	// The object name of copycode2.NSC is "COPYCODE2".
	// subprogram.NSN has INCLUDE COPYCODE2, so it's a direct dependent.
	// program.NSP has INCLUDE SUBPROGRAM, and subprogram.NSN is in the dependents,
	// so program.NSP becomes a transitive dependent via BFS.
	dependents := idx.Invalidate("copycode2.NSC")

	// We expect exactly 2 dependents: subprogram.NSN and program.NSP
	if len(dependents) != 2 {
		t.Errorf("Invalidate() returned %d dependents, want 2", len(dependents))
		return
	}

	// Verify both dependents are present (order doesn't matter)
	foundSubprogram := false
	foundProgram := false
	for _, dep := range dependents {
		if dep == "subprogram.NSN" {
			foundSubprogram = true
		}
		if dep == "program.NSP" {
			foundProgram = true
		}
	}

	if !foundSubprogram {
		t.Errorf("Invalidate() missing subprogram.NSN in dependents: %v", dependents)
	}
	if !foundProgram {
		t.Errorf("Invalidate() missing program.NSP in dependents: %v", dependents)
	}
}

// TestInvalidate_NoDependencies verifies that Invalidate() returns an empty
// set when a file has no dependents (no files include it). This tests the
// edge case of leaf nodes in the dependency graph.
func TestInvalidate_NoDependencies(t *testing.T) {
	t.Helper()

	idx := &Index{}

	// copycode.NSC has no dependents (no files include it)
	copycode := model.FileAnalysis{
		ObjectType: model.ObjectCopycode,
		Edges:      []model.EdgeEntry{},
	}
	idx.Add("copycode.NSC", copycode)

	// When copycode.NSC changes, Invalidate() should return no dependents
	dependents := idx.Invalidate("copycode.NSC")

	if len(dependents) != 0 {
		t.Errorf("Invalidate() returned %d dependents for leaf node, want 0: %v", len(dependents), dependents)
	}
}

// TestBuild_CacheIntegration verifies the cache integration behavior for Build().
// This tests task 05-C02 (cache integration into workspace Build) for FR-37, FR-38,
// FR-39, and FR-35 (incremental re-analysis).
//
// The test covers three scenarios:
// 1. No cache exists -> full index build
// 2. Cache exists and is fresh -> load from cache, no re-analysis
// 3. Cache exists with stale files -> load cache + re-analyze only stale files
func TestBuild_CacheIntegration(t *testing.T) {
	t.Helper()

	tests := []struct {
		name           string
		setupCache     func(tmpDir string) string
		currentHashes  map[string]string
		wantStaleCount int
		wantTotal      int
		verify         func(t *testing.T, idx *Index, staleCount int, totalFiles int)
	}{
		{
			name: "no cache exists returns full index from scratch",
			setupCache: func(tmpDir string) string {
				// Don't create a cache file
				return ""
			},
			wantStaleCount: 0,
			wantTotal:      3,
			verify: func(t *testing.T, idx *Index, staleCount int, totalFiles int) {
				t.Helper()

				// Verify all 3 files are indexed
				for _, fname := range []string{"file1.NSP", "file2.NSP", "file3.NSP"} {
					fa, ok := idx.Get(fname)
					if !ok {
						t.Errorf("Build() did not index %s (no cache case)", fname)
						continue
					}
					if fa.ObjectType == model.ObjectUnknown {
						t.Errorf("Build() classified %s as ObjectUnknown, expected actual type", fname)
					}
				}

				// Verify staleCount and totalFiles
				if staleCount != 0 {
					t.Errorf("Build() staleCount = %d, want 0 (no cache case)", staleCount)
				}
				if totalFiles != 3 {
					t.Errorf("Build() totalFiles = %d, want 3", totalFiles)
				}
			},
		},
		{
			name: "cache exists and is fresh loads from cache with no re-analysis",
			setupCache: func(tmpDir string) string {
				// Create a cache file with 3 files
				cachePath := filepath.Join(tmpDir, "cache.json")

				idx := &Index{}
				idx.Add("file1.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
				idx.Add("file2.NSP", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("file3.NSP", model.FileAnalysis{ObjectType: model.ObjectCopycode})

				err := Save(idx, cachePath)
				if err != nil {
					t.Fatalf("Failed to create cache: %v", err)
				}

				// Return hashes that match the cache (files are fresh)
				return cachePath
			},
			currentHashes: map[string]string{
				"file1.NSP": "e81150802cdacecbc308d1b92984e3bb54546ca8bebb9aae63bb00e1fb43e454",
				"file2.NSP": "7f39197e0be7bc4411a5abc69a843d62613a14eb0c9505cfb94d3b575214f10d",
				"file3.NSP": "4ecd229ec7b3002950e41997eebd4395a723dd9f86ce2ed9c8b5984f3a64a823",
			},
			wantStaleCount: 0,
			wantTotal:      3,
			verify: func(t *testing.T, idx *Index, staleCount int, totalFiles int) {
				t.Helper()

				// Verify all 3 files are loaded from cache
				for _, fname := range []string{"file1.NSP", "file2.NSP", "file3.NSP"} {
					fa, ok := idx.Get(fname)
					if !ok {
						t.Errorf("Build() did not load %s from cache", fname)
						continue
					}
					if fa.ObjectType == model.ObjectUnknown {
						t.Errorf("Build() classified %s as ObjectUnknown from cache", fname)
					}
				}

				// Verify no files are stale
				if staleCount != 0 {
					t.Errorf("Build() staleCount = %d, want 0 (cache fresh)", staleCount)
				}
				if totalFiles != 3 {
					t.Errorf("Build() totalFiles = %d, want 3", totalFiles)
				}
			},
		},
		{
			name: "cache exists with stale files loads cache and re-analyzes only stale files",
			setupCache: func(tmpDir string) string {
				// Create a cache file with 3 files
				cachePath := filepath.Join(tmpDir, "cache.json")

				idx := &Index{}
				idx.Add("file1.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
				idx.Add("file2.NSP", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
				idx.Add("file3.NSP", model.FileAnalysis{ObjectType: model.ObjectCopycode})

				err := Save(idx, cachePath)
				if err != nil {
					t.Fatalf("Failed to create cache: %v", err)
				}

				// Return hashes where file1.NSP and file3.NSP are stale
				return cachePath
			},
			currentHashes: map[string]string{
				"file1.NSP": "stale_hash_file1",
				"file2.NSP": "7f39197e0be7bc4411a5abc69a843d62613a14eb0c9505cfb94d3b575214f10d",
				"file3.NSP": "stale_hash_file3",
			},
			wantStaleCount: 2, // file1.NSP and file3.NSP are stale
			wantTotal:      3,
			verify: func(t *testing.T, idx *Index, staleCount int, totalFiles int) {
				t.Helper()

				// Verify all 3 files are present (loaded from cache + re-analyzed stale)
				for _, fname := range []string{"file1.NSP", "file2.NSP", "file3.NSP"} {
					fa, ok := idx.Get(fname)
					if !ok {
						t.Errorf("Build() did not include %s", fname)
						continue
					}
					if fa.ObjectType == model.ObjectUnknown {
						t.Errorf("Build() classified %s as ObjectUnknown", fname)
					}
				}

				// Verify staleCount matches expected stale files
				if staleCount != 2 {
					t.Errorf("Build() staleCount = %d, want 2 (file1.NSP and file3.NSP stale)", staleCount)
				}
				if totalFiles != 3 {
					t.Errorf("Build() totalFiles = %d, want 3", totalFiles)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			// Create a temporary workspace with test files.
			workspaceRoot := t.TempDir()

			files := []string{"file1.NSP", "file2.NSP", "file3.NSP"}
			for _, fname := range files {
				fpath := filepath.Join(workspaceRoot, fname)
				content := fmt.Sprintf("content for %s\nEND\n", fname)
				if err := os.WriteFile(fpath, []byte(content), 0644); err != nil {
					t.Fatalf("Failed to create file %s: %v", fname, err)
				}
			}

			// Create a temporary directory for the cache file.
			tmpDir := t.TempDir()

			// Setup cache if needed.
			var cachePath string
			if tc.setupCache != nil {
				cachePath = tc.setupCache(tmpDir)
			}

			// Create a config with default settings.
			cfg := config.Defaults()

			// Create a logger.
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			// Create the analyzer.
			az := natural.New(nil)

			// Build the workspace index.
			// NOTE: This call will fail to compile until Build() signature is
			// updated to support cache integration (task 05-C02).
			idx, staleCount, totalFiles, err := BuildWithCache(context.Background(), workspaceRoot, cfg, az, logger, cachePath, tc.currentHashes, nil)
			if err != nil {
				t.Fatalf("BuildWithCache() returned error: %v", err)
			}

			// Verify the results.
			tc.verify(t, idx, staleCount, totalFiles)
		})
	}
}

// TestIndex_LookupByName verifies that Index.LookupByName() looks up candidate
// definitions by object name (derived from filename stem, case-insensitive) and
// optionally filters by ObjectType. This implements Task 3 of feature 07
// (FR-5, FR-10, FR-16, FR-31 — call-dependency resolution).
func TestIndex_LookupByName(t *testing.T) {
	t.Helper()

	tests := []struct {
		name        string
		setup       func(*Index, *config.Config)
		lookupName  string
		lookupType  model.ObjectType
		cfg         *config.Config
		wantCount   int
		wantPaths   []string
		wantLibs    []string
		description string
	}{
		{
			name: "basic-lookup-multiple-same-name-different-libs",
			description: "lookup MYSUB returns both APP/MYSUB.NSN and COMMON/MYSUB.NSN " +
				"with their respective libraries (Task 3, FR-5, FR-16)",
			setup: func(idx *Index, cfg *config.Config) {
				// Add APP/MYSUB.NSN
				idx.Add("APP/MYSUB.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
				// Add COMMON/MYSUB.NSN
				idx.Add("COMMON/MYSUB.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
			},
			lookupName: "MYSUB",
			lookupType: model.ObjectSubprogram,
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "APP", Path: "APP"},
						{Name: "COMMON", Path: "COMMON"},
					},
				},
			},
			wantCount: 2,
			wantPaths: []string{"APP/MYSUB.NSN", "COMMON/MYSUB.NSN"},
			wantLibs:  []string{"APP", "COMMON"},
		},
		{
			name: "case-insensitive-lookup",
			description: "lookup with lowercase 'mysub' returns same results " +
				"as uppercase 'MYSUB' (case-insensitive, Task 3)",
			setup: func(idx *Index, cfg *config.Config) {
				idx.Add("APP/MYSUB.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
				idx.Add("COMMON/MYSUB.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
			},
			lookupName: "mysub",
			lookupType: model.ObjectSubprogram,
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "APP", Path: "APP"},
						{Name: "COMMON", Path: "COMMON"},
					},
				},
			},
			wantCount: 2,
			wantPaths: []string{"APP/MYSUB.NSN", "COMMON/MYSUB.NSN"},
			wantLibs:  []string{"APP", "COMMON"},
		},
		{
			name: "type-filter-excludes-wrong-type",
			description: "lookup MAIN with type filter ObjectSubprogram excludes " +
				"MAIN.NSP (wrong type) but includes MAIN.NSN (Task 3, FR-10, FR-16)",
			setup: func(idx *Index, cfg *config.Config) {
				// Add MAIN.NSP (program)
				idx.Add("MAIN.NSP", model.FileAnalysis{
					ObjectType: model.ObjectProgram,
				})
				// Add MAIN.NSN (subprogram)
				idx.Add("MAIN.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
			},
			lookupName: "MAIN",
			lookupType: model.ObjectSubprogram,
			cfg:        &config.Config{},
			wantCount:  1,
			wantPaths:  []string{"MAIN.NSN"},
			wantLibs:   []string{""},
		},
		{
			name: "zero-type-matches-all-types",
			description: "lookup MAIN with zero ObjectType (empty string) returns " +
				"all matching names regardless of type (Task 3)",
			setup: func(idx *Index, cfg *config.Config) {
				idx.Add("MAIN.NSP", model.FileAnalysis{
					ObjectType: model.ObjectProgram,
				})
				idx.Add("MAIN.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
			},
			lookupName: "MAIN",
			lookupType: "", // zero/empty type means match all
			cfg:        &config.Config{},
			wantCount:  2,
			wantPaths:  []string{"MAIN.NSN", "MAIN.NSP"},
			wantLibs:   []string{"", ""},
		},
		{
			name:        "unknown-name-returns-empty",
			description: "lookup of non-existent name returns empty slice, not nil or error (Task 3, FR-17)",
			setup: func(idx *Index, cfg *config.Config) {
				idx.Add("EXISTING.NSN", model.FileAnalysis{
					ObjectType: model.ObjectSubprogram,
				})
			},
			lookupName: "NOSUCH",
			lookupType: model.ObjectSubprogram,
			cfg:        &config.Config{},
			wantCount:  0,
			wantPaths:  []string{},
			wantLibs:   []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idx := &Index{}
			tc.setup(idx, tc.cfg)

			// Call LookupByName
			candidates := idx.LookupByName(tc.lookupName, tc.lookupType, tc.cfg)

			// Verify count
			if len(candidates) != tc.wantCount {
				t.Errorf("LookupByName(%q, %v) returned %d candidates, want %d",
					tc.lookupName, tc.lookupType, len(candidates), tc.wantCount)
				if len(candidates) > 0 {
					for _, c := range candidates {
						t.Logf("  candidate: path=%q, library=%q", c.Path, c.Library)
					}
				}
				return
			}

			// Verify paths and libraries (in order)
			if tc.wantCount > 0 {
				for i, candidate := range candidates {
					if candidate.Path != tc.wantPaths[i] {
						t.Errorf("LookupByName candidate %d: path=%q, want %q",
							i, candidate.Path, tc.wantPaths[i])
					}
					if candidate.Library != tc.wantLibs[i] {
						t.Errorf("LookupByName candidate %d: library=%q, want %q",
							i, candidate.Library, tc.wantLibs[i])
					}
				}
			}
		})
	}
}

// TestBuildNameIndex verifies that buildNameIndex produces a complete, sorted,
// case-normalized snapshot that matches LookupByName results for every name.
// This exercises the bulk-resolution path that the call-graph resolver will use
// (O(files + edges) rather than O(files * edges)).
func TestBuildNameIndex(t *testing.T) {
	t.Helper()

	cfg := &config.Config{
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{
				{Name: "APP", Path: "APP"},
				{Name: "COMMON", Path: "COMMON"},
			},
		},
	}

	idx := &Index{}
	idx.Add("APP/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("COMMON/MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("APP/MAIN.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})

	nameMap := idx.buildNameIndex(cfg)

	// Keys must be uppercase (object names are case-normalised).
	if _, ok := nameMap["MYSUB"]; !ok {
		t.Error("buildNameIndex: missing key \"MYSUB\"")
	}
	if _, ok := nameMap["MAIN"]; !ok {
		t.Error("buildNameIndex: missing key \"MAIN\"")
	}

	// MYSUB has two candidates sorted by path.
	mysub := nameMap["MYSUB"]
	if len(mysub) != 2 {
		t.Fatalf("buildNameIndex[MYSUB] len=%d, want 2", len(mysub))
	}
	if mysub[0].Path != "APP/MYSUB.NSN" {
		t.Errorf("buildNameIndex[MYSUB][0].Path=%q, want %q", mysub[0].Path, "APP/MYSUB.NSN")
	}
	if mysub[1].Path != "COMMON/MYSUB.NSN" {
		t.Errorf("buildNameIndex[MYSUB][1].Path=%q, want %q", mysub[1].Path, "COMMON/MYSUB.NSN")
	}
	if mysub[0].Library != "APP" {
		t.Errorf("buildNameIndex[MYSUB][0].Library=%q, want %q", mysub[0].Library, "APP")
	}
	if mysub[1].Library != "COMMON" {
		t.Errorf("buildNameIndex[MYSUB][1].Library=%q, want %q", mysub[1].Library, "COMMON")
	}

	// The buildNameIndex result must match what LookupByName returns for the same name.
	for name, candidates := range nameMap {
		via := idx.LookupByName(name, "", cfg)
		if len(via) != len(candidates) {
			t.Errorf("buildNameIndex[%q] len=%d but LookupByName returned %d",
				name, len(candidates), len(via))
			continue
		}
		for i := range candidates {
			if candidates[i].Path != via[i].Path {
				t.Errorf("buildNameIndex[%q][%d].Path=%q but LookupByName=%q",
					name, i, candidates[i].Path, via[i].Path)
			}
		}
	}
}

// TestBuildNameIndex_Race exercises buildNameIndex and concurrent Add calls under
// the race detector to verify the read-lock snapshot is race-free.
func TestBuildNameIndex_Race(t *testing.T) {
	t.Parallel()

	idx := &Index{}
	idx.Add("APP/INIT.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})

	cfg := &config.Config{}

	done := make(chan struct{})

	// Writer goroutine: continuously adds entries while the reader runs.
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			idx.Add("APP/INIT.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
		}
	}()

	// Reader: call buildNameIndex while the writer is active.
	for i := 0; i < 10; i++ {
		nameMap := idx.buildNameIndex(cfg)
		if len(nameMap) == 0 {
			t.Error("buildNameIndex returned empty map during concurrent writes")
		}
	}

	<-done
}

// TestBuild_CancelledMidBuild verifies the feature 21 T4/OQ-F cancellation
// contract: a ctx cancelled during the per-file scan aborts the build early
// (it does NOT run to completion) and returns (nil, ctx.Err()). Determinism:
// the cancelling analyzer cancels the ctx from inside the FIRST Analyze call,
// so the loop's ctx.Err() check fires on the next iteration — no sleeps.
func TestBuild_CancelledMidBuild(t *testing.T) {
	tmpDir := t.TempDir()

	// Create several indexed files so there is more than one loop iteration.
	// Names are chosen so sorted order is stable and the analyzer sees them
	// one at a time; only the first should be analyzed before cancellation.
	for _, name := range []string{"a.NSP", "b.NSP", "c.NSP", "d.NSP", "e.NSP"} {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte("WRITE 'x'\nEND\n"), 0644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	az := &cancellingAnalyzer{cancel: cancel}

	// No cache path → every file flows through az.Analyze, so the analysis
	// count is a precise measure of how far the build got.
	var progressCalls int
	idx, err := Build(ctx, tmpDir, cfg, az, logger, func(path string, current, total int) {
		progressCalls++
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Build() error = %v, want context.Canceled", err)
	}
	if idx != nil {
		t.Errorf("Build() returned a non-nil index on cancellation; want nil (partial index discarded)")
	}
	if got := az.analyzed(); got != 1 {
		t.Errorf("analyzer ran %d times, want 1 (build must abort after the first file, not run all 5)", got)
	}
	if progressCalls >= 5 {
		t.Errorf("onProgress fired %d times, want fewer than the 5 total files (build aborted early)", progressCalls)
	}
}

// TestBuild_PreCancelledContext verifies that a ctx cancelled BEFORE the build
// starts causes an immediate early return: no file is analyzed at all.
func TestBuild_PreCancelledContext(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"a.NSP", "b.NSP", "c.NSP"} {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte("WRITE 'x'\nEND\n"), 0644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the build runs

	az := &cancellingAnalyzer{} // cancel func nil — must never be needed
	idx, err := Build(ctx, tmpDir, cfg, az, logger, nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Build() error = %v, want context.Canceled", err)
	}
	if idx != nil {
		t.Errorf("Build() returned a non-nil index for a pre-cancelled ctx; want nil")
	}
	if got := az.analyzed(); got != 0 {
		t.Errorf("analyzer ran %d times for a pre-cancelled ctx, want 0", got)
	}
}
