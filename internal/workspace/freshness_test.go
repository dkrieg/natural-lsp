package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// TestBuildWithCache_NeverServesStaleContent is the NFR-8 freshness guard
// (feature 22, T5): after a file's content changes on disk, a rebuild via
// BuildWithCache must reflect the NEW content and never serve the stale cached
// analysis. This is a NORMAL (non-bench-tagged) unit test so it runs in
// `just test` as a permanent regression fixture for content-hash invalidation
// (the cache is keyed on content hash, not mtime — FR-38).
//
// The scenario exercises the exact server warm-start path: a first
// BuildWithCache writes the on-disk cache; the source is then mutated on disk
// (a CALLNAT target name changes); a second BuildWithCache over the same root
// (currentHashes=nil ⇒ hashes recomputed from disk) must re-analyze the changed
// file and return the new edge, with staleCount reporting exactly the one
// re-analyzed file.
func TestBuildWithCache_NeverServesStaleContent(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, ".natural-lsp-cache")

	// A minimal caller program with one static CALLNAT to OLDTARG, plus the two
	// subprogram targets so the workspace is self-consistent. Flat namespace (no
	// library map) — the freshness property is independent of resolution.
	callerPath := filepath.Join(root, "MAIN.NSP")
	writeFileForTest(t, callerPath, ""+
		"DEFINE DATA LOCAL\n"+
		"END-DEFINE\n"+
		"CALLNAT 'OLDTARG'\n"+
		"END\n")
	writeFileForTest(t, filepath.Join(root, "OLDTARG.NSN"), ""+
		"DEFINE DATA PARAMETER\n"+
		"END-DEFINE\n"+
		"END\n")
	writeFileForTest(t, filepath.Join(root, "NEWTARG.NSN"), ""+
		"DEFINE DATA PARAMETER\n"+
		"END-DEFINE\n"+
		"END\n")

	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// First build: cold, writes the cache. currentHashes=nil ⇒ computed from disk.
	idx1, stale1, _, err := BuildWithCache(context.Background(), root, cfg, az, logger, cachePath, nil, nil)
	if err != nil {
		t.Fatalf("first BuildWithCache: %v", err)
	}
	if stale1 != 0 {
		t.Fatalf("cold build staleCount = %d, want 0", stale1)
	}
	if got := callnatTarget(t, idx1, "MAIN.NSP"); got != "OLDTARG" {
		t.Fatalf("before change: CALLNAT target = %q, want OLDTARG", got)
	}

	// Mutate MAIN.NSP on disk: the CALLNAT now targets NEWTARG.
	writeFileForTest(t, callerPath, ""+
		"DEFINE DATA LOCAL\n"+
		"END-DEFINE\n"+
		"CALLNAT 'NEWTARG'\n"+
		"END\n")

	// Second build over the SAME root and cache. currentHashes=nil ⇒ recomputed
	// from the (now-changed) disk content, so MAIN.NSP's hash no longer matches
	// the cache entry and it is re-analyzed.
	idx2, stale2, _, err := BuildWithCache(context.Background(), root, cfg, az, logger, cachePath, nil, nil)
	if err != nil {
		t.Fatalf("second BuildWithCache: %v", err)
	}

	// Freshness: the rebuilt analysis must reflect the NEW content, never stale.
	if got := callnatTarget(t, idx2, "MAIN.NSP"); got != "NEWTARG" {
		t.Fatalf("after change: CALLNAT target = %q, want NEWTARG (stale content served)", got)
	}
	// Exactly one file (MAIN.NSP) changed content, so exactly one re-analyzed.
	if stale2 != 1 {
		t.Fatalf("warm build staleCount = %d, want 1 (only MAIN.NSP changed)", stale2)
	}
	// The unchanged subprograms must still be present (retained from cache).
	if _, ok := idx2.Get("OLDTARG.NSN"); !ok {
		t.Fatal("OLDTARG.NSN missing from rebuilt index")
	}
	if _, ok := idx2.Get("NEWTARG.NSN"); !ok {
		t.Fatal("NEWTARG.NSN missing from rebuilt index")
	}
}

// writeFileForTest writes content to path, creating parent dirs, failing the test
// on error.
func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// callnatTarget returns the TargetName of the first EdgeCalls edge in the indexed
// file at relPath, or "" if none. It fails the test if the file is not indexed.
func callnatTarget(t *testing.T, idx *Index, relPath string) string {
	t.Helper()
	fa, ok := idx.Get(relPath)
	if !ok {
		t.Fatalf("file %q not in index", relPath)
	}
	for _, e := range fa.Edges {
		if e.Kind == model.EdgeCalls {
			return e.TargetName
		}
	}
	return ""
}
