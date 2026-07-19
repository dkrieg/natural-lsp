package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis"
	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// countingAnalyzer wraps a delegate analyzer and records, per invocation, the
// path passed to Analyze. It lets a cache-wiring test observe EXACTLY which
// files the workspace build re-analyzed (cache miss / stale) versus served from
// the persisted cache (never re-analyzed). Concurrency-safe: the workspace build
// analyzes files serially today, but the counter is guarded so a future worker
// pool would not race the test.
type countingAnalyzer struct {
	delegate analysis.Analyzer
	mu       sync.Mutex
	analyzed []string
}

func (c *countingAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	c.mu.Lock()
	c.analyzed = append(c.analyzed, filepath.Base(path))
	c.mu.Unlock()
	return c.delegate.Analyze(path, content)
}

// paths returns the sorted set of base filenames analyzed since the last reset.
func (c *countingAnalyzer) paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.analyzed...)
	sort.Strings(out)
	return out
}

func (c *countingAnalyzer) reset() {
	c.mu.Lock()
	c.analyzed = nil
	c.mu.Unlock()
}

// newCacheWiringHCtx builds a handlerContext rooted at a temp workspace with the
// given .NSx files and a counting analyzer, using default config (cache path
// ".natural-lsp-cache" under root). It returns the context, the counting
// analyzer, and the absolute cache-file path buildIndex will use.
func newCacheWiringHCtx(t *testing.T, files map[string]string) (*handlerContext, *countingAnalyzer, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	cfg := config.Defaults()
	ca := &countingAnalyzer{delegate: natural.New(nil)}
	hctx := &handlerContext{
		root:   root,
		cfg:    cfg,
		az:     ca,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	cachePath := filepath.Join(root, cfg.Cache.Path)
	return hctx, ca, cachePath
}

// TestBuildIndexCacheColdThenWarm proves the server's build path (buildIndex →
// workspace.BuildWithCache) writes the on-disk cache on a COLD start and, on a
// subsequent WARM start over an unchanged workspace, loads that cache and
// re-analyzes NO files (FR-37/FR-38/NFR-2, feature 21 T12/OQ-E).
func TestBuildIndexCacheColdThenWarm(t *testing.T) {
	hctx, ca, cachePath := newCacheWiringHCtx(t, map[string]string{
		"prog.NSP": "DEFINE DATA LOCAL\nEND-DEFINE\nCALLNAT 'SUB'\nEND\n",
		"sub.NSN":  "DEFINE DATA PARAMETER\nEND-DEFINE\nEND\n",
	})

	// COLD build: cache does not exist yet.
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("cache should not exist before the cold build, stat err = %v", err)
	}
	idx, res, err := hctx.buildIndex(context.Background(), nil)
	if err != nil {
		t.Fatalf("cold buildIndex: %v", err)
	}
	if idx == nil || res == nil {
		t.Fatal("cold build returned nil idx/res")
	}
	// Both files must be in the index.
	for _, f := range []string{"prog.NSP", "sub.NSN"} {
		if _, ok := idx.Get(f); !ok {
			t.Errorf("cold build did not index %s", f)
		}
	}
	// The cache file must have been created under root/cfg.Cache.Path.
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cold build did not write cache at %s: %v", cachePath, err)
	}
	// Cold build analyzed both files.
	if got, want := ca.paths(), []string{"prog.NSP", "sub.NSN"}; !equalStrings(got, want) {
		t.Errorf("cold build analyzed %v, want %v", got, want)
	}

	// WARM build over the unchanged workspace: cache is loaded, nothing re-analyzed.
	ca.reset()
	warmIdx, warmRes, err := hctx.buildIndex(context.Background(), nil)
	if err != nil {
		t.Fatalf("warm buildIndex: %v", err)
	}
	if warmIdx == nil || warmRes == nil {
		t.Fatal("warm build returned nil idx/res")
	}
	for _, f := range []string{"prog.NSP", "sub.NSN"} {
		if _, ok := warmIdx.Get(f); !ok {
			t.Errorf("warm build missing %s (should come from cache)", f)
		}
	}
	if got := ca.paths(); len(got) != 0 {
		t.Errorf("warm build re-analyzed %v, want NONE (all served from cache)", got)
	}
}

// TestBuildIndexCacheChangedFile proves that after a cold build, changing ONE
// file and rebuilding re-analyzes ONLY that file — content-hash invalidation
// (FR-38), not a full rebuild.
func TestBuildIndexCacheChangedFile(t *testing.T) {
	hctx, ca, cachePath := newCacheWiringHCtx(t, map[string]string{
		"a.NSP": "DEFINE DATA LOCAL\nEND-DEFINE\nEND\n",
		"b.NSN": "DEFINE DATA PARAMETER\nEND-DEFINE\nEND\n",
		"c.NSC": "* copycode\n",
	})

	// COLD build writes the cache.
	if _, _, err := hctx.buildIndex(context.Background(), nil); err != nil {
		t.Fatalf("cold buildIndex: %v", err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cold build did not write cache: %v", err)
	}

	// Change ONLY b.NSN's content.
	if err := os.WriteFile(filepath.Join(hctx.root, "b.NSN"),
		[]byte("DEFINE DATA PARAMETER\n1 #X (A10)\nEND-DEFINE\nEND\n"), 0o644); err != nil {
		t.Fatalf("rewrite b.NSN: %v", err)
	}

	// WARM build: only b.NSN should be re-analyzed.
	ca.reset()
	idx, _, err := hctx.buildIndex(context.Background(), nil)
	if err != nil {
		t.Fatalf("warm buildIndex: %v", err)
	}
	if got, want := ca.paths(), []string{"b.NSN"}; !equalStrings(got, want) {
		t.Errorf("changed-file build re-analyzed %v, want only [b.NSN]", got)
	}
	// All three files still present; a.NSP and c.NSC came from the cache.
	for _, f := range []string{"a.NSP", "b.NSN", "c.NSC"} {
		if _, ok := idx.Get(f); !ok {
			t.Errorf("changed-file build missing %s", f)
		}
	}
}

// TestBuildIndexCorruptCacheFallsBack proves that a corrupt/garbage cache file
// triggers a full rebuild with NO error and NO panic (FR-43 graceful
// degradation), and rewrites a valid cache.
func TestBuildIndexCorruptCacheFallsBack(t *testing.T) {
	hctx, ca, cachePath := newCacheWiringHCtx(t, map[string]string{
		"x.NSP": "DEFINE DATA LOCAL\nEND-DEFINE\nEND\n",
		"y.NSN": "DEFINE DATA PARAMETER\nEND-DEFINE\nEND\n",
	})

	// Write garbage to the cache location (create the dir if the default path
	// contains one — the default ".natural-lsp-cache" is a file directly under root).
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{ this is not valid json ]["), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	// Build must not error or panic; it falls back to a full rebuild.
	idx, res, err := hctx.buildIndex(context.Background(), nil)
	if err != nil {
		t.Fatalf("corrupt-cache buildIndex returned error, want graceful fallback: %v", err)
	}
	if idx == nil || res == nil {
		t.Fatal("corrupt-cache build returned nil idx/res")
	}
	// Full rebuild: both files analyzed.
	if got, want := ca.paths(), []string{"x.NSP", "y.NSN"}; !equalStrings(got, want) {
		t.Errorf("corrupt-cache build analyzed %v, want full rebuild %v", got, want)
	}
	for _, f := range []string{"x.NSP", "y.NSN"} {
		if _, ok := idx.Get(f); !ok {
			t.Errorf("corrupt-cache rebuild missing %s", f)
		}
	}
	// The cache was rewritten with valid content: a follow-up warm build reads it.
	ca.reset()
	if _, _, err := hctx.buildIndex(context.Background(), nil); err != nil {
		t.Fatalf("post-recovery warm buildIndex: %v", err)
	}
	if got := ca.paths(); len(got) != 0 {
		t.Errorf("post-recovery warm build re-analyzed %v, want NONE (cache repaired)", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
