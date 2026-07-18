package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// discardNameIdxLogger is a no-op logger for the name-index-cache tests.
func discardNameIdxLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeNameIdxCorpusFile writes relPath (with intermediate dirs) under root.
func writeNameIdxCorpusFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// flatCfg is a config with no library map, so every lookup runs in the flat
// namespace (no steplib filtering) — the simplest surface to assert
// behavior-preservation of the name-index cache.
func flatCfg() *config.Config {
	return &config.Config{
		Resolution: config.ResolutionConfig{Libraries: []config.Library{}},
	}
}

// seedFlatIndex returns an index populated with a mix of object types across a
// flat namespace, matching the shapes used by the completion/definition tests.
func seedFlatIndex() *Index {
	idx := &Index{}
	idx.Add("MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("MYUTIL.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("SHARED.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	idx.Add("MAIN.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
	idx.Add("REPORT.NSP", model.FileAnalysis{ObjectType: model.ObjectProgram})
	idx.Add("CUST.NSD", model.FileAnalysis{ObjectType: model.ObjectDDM})
	return idx
}

// uncachedLookupByName is an INDEPENDENT reference computation of LookupByName
// that consults idx.buildNameIndex(cfg) — the pure, snapshot-based builder that
// NEVER touches the cached name index (idx.cachedNameIndex) — and applies the
// same trivial bucket + type filter the method does. Because buildNameIndex
// sorts each name bucket by Path (documented contract) and LookupByName does not
// re-sort, this reproduces the method's contract without going through the cache.
// It is the "uncached" oracle so the equality test genuinely proves cached ==
// uncached, not merely "second cached call == first cached call".
func uncachedLookupByName(idx *Index, name string, typ model.ObjectType, cfg *config.Config) []Candidate {
	bucket := idx.buildNameIndex(cfg)[strings.ToUpper(name)]
	out := make([]Candidate, 0, len(bucket))
	for _, cand := range bucket {
		if typ != "" && cand.Type != typ {
			continue
		}
		out = append(out, cand)
	}
	return out
}

// TestNameIndexCache_NamesWithPrefixIdentical proves the cached NamesWithPrefix
// returns results byte-identical to a fresh (uncached) buildNameIndex-derived
// computation, across various prefixes, types, and referencing paths. The cache
// is a pure performance optimization — the observable behavior must not change
// (feature 22 T7).
//
// Note on the oracle: NamesWithPrefix layers reachability filtering + sorting on
// top of the name map. Rather than duplicate that whole filter here (which would
// risk masking a bug by diverging from the method), this test keeps the fresh vs.
// cached-index comparison for the full method AND strengthens the guarantee by
// asserting the cached result also equals the result computed from a map built by
// the UNCACHED buildNameIndex for the flat-namespace cases (where NamesWithPrefix
// reduces to prefix+type filter + Path sort — no steplib chain). The dedicated
// cached==uncached proof for the trivial-filter path lives in
// TestNameIndexCache_LookupByNameIdentical below.
func TestNameIndexCache_NamesWithPrefixIdentical(t *testing.T) {
	cfg := flatCfg()

	cases := []struct {
		prefix  string
		typ     model.ObjectType
		refPath string
	}{
		{"MY", model.ObjectSubprogram, "CALLER.NSP"},
		{"", model.ObjectSubprogram, "CALLER.NSP"},
		{"MA", model.ObjectProgram, "CALLER.NSP"},
		{"CUST", model.ObjectDDM, "CALLER.NSP"},
		{"ZZZ", model.ObjectSubprogram, "CALLER.NSP"}, // no matches
	}

	for _, tc := range cases {
		// A fresh index computes the answer from scratch (cache built on first call).
		freshIdx := seedFlatIndex()
		want := freshIdx.NamesWithPrefix(tc.prefix, tc.typ, tc.refPath, cfg)

		// A second index, queried twice: the second call is served from the cache.
		cachedIdx := seedFlatIndex()
		_ = cachedIdx.NamesWithPrefix(tc.prefix, tc.typ, tc.refPath, cfg) // populate cache
		got := cachedIdx.NamesWithPrefix(tc.prefix, tc.typ, tc.refPath, cfg)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("prefix=%q typ=%q: cached result differs\n got=%#v\nwant=%#v", tc.prefix, tc.typ, got, want)
		}

		// Independent (cached==uncached) cross-check for the flat namespace: build
		// the expected candidate set directly from the UNCACHED buildNameIndex map,
		// applying NamesWithPrefix's flat-namespace filter (prefix + type, Path sort).
		// flatCfg has no library map, so searchChain is empty and this matches the
		// method's flat-namespace branch exactly.
		uncachedMap := cachedIdx.buildNameIndex(cfg)
		upper := strings.ToUpper(tc.prefix)
		var wantUncached []Candidate
		for name, cands := range uncachedMap {
			if !strings.HasPrefix(name, upper) {
				continue
			}
			for _, c := range cands {
				if c.Type == tc.typ {
					wantUncached = append(wantUncached, c)
				}
			}
		}
		sort.Slice(wantUncached, func(i, j int) bool { return wantUncached[i].Path < wantUncached[j].Path })
		if len(wantUncached) == 0 {
			wantUncached = []Candidate{}
		}
		if !reflect.DeepEqual(got, wantUncached) {
			t.Errorf("prefix=%q typ=%q: cached result differs from UNCACHED buildNameIndex-derived\n got=%#v\nwant=%#v",
				tc.prefix, tc.typ, got, wantUncached)
		}
	}
}

// TestNameIndexCache_LookupByNameIdentical proves the cached LookupByName returns
// results identical to an INDEPENDENT, UNCACHED computation across names and type
// filters, including case-insensitive matching and the any-type sentinel.
//
// The oracle (uncachedLookupByName) consults idx.buildNameIndex — the pure
// snapshot builder that never touches the cache — so this genuinely asserts
// cached == uncached, not "second cached call == first cached call". (The earlier
// version compared two indices that both routed through cachedNameIndex, so it
// could not have caught a cache that diverged from the uncached path.)
func TestNameIndexCache_LookupByNameIdentical(t *testing.T) {
	cfg := flatCfg()

	cases := []struct {
		name string
		typ  model.ObjectType
	}{
		{"MYSUB", model.ObjectSubprogram},
		{"mysub", model.ObjectSubprogram}, // case-insensitive
		{"MAIN", model.ObjectProgram},
		{"CUST", model.ObjectDDM},
		{"MYSUB", ""}, // any-type sentinel
		{"NOPE", model.ObjectSubprogram},
	}

	for _, tc := range cases {
		// Independent oracle: computed from the UNCACHED buildNameIndex map.
		oracleIdx := seedFlatIndex()
		want := uncachedLookupByName(oracleIdx, tc.name, tc.typ, cfg)

		cachedIdx := seedFlatIndex()
		_ = cachedIdx.LookupByName(tc.name, tc.typ, cfg) // populate cache
		got := cachedIdx.LookupByName(tc.name, tc.typ, cfg)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("name=%q typ=%q: cached result differs from UNCACHED oracle\n got=%#v\nwant=%#v", tc.name, tc.typ, got, want)
		}
	}
}

// TestNameIndexCache_InvalidatedOnAdd is the critical correctness guard: a stale
// name-index cache serving deleted/renamed symbols would be a regression. It
// builds the index, queries (populating the cache), then Adds a new object and
// asserts the new object appears on the next query — proving Add invalidated the
// cache rather than the query serving stale data (feature 22 T7).
func TestNameIndexCache_InvalidatedOnAdd(t *testing.T) {
	cfg := flatCfg()
	idx := seedFlatIndex()

	// First query populates the cache. NEWSUB is not present yet.
	before := idx.NamesWithPrefix("NEW", model.ObjectSubprogram, "CALLER.NSP", cfg)
	if len(before) != 0 {
		t.Fatalf("precondition: NEW* should not match yet, got %#v", before)
	}
	// Also populate the LookupByName path.
	if got := idx.LookupByName("NEWSUB", model.ObjectSubprogram, cfg); len(got) != 0 {
		t.Fatalf("precondition: NEWSUB should not exist yet, got %#v", got)
	}

	// Mutate the index: the cache must be invalidated.
	idx.Add("NEWSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	after := idx.NamesWithPrefix("NEW", model.ObjectSubprogram, "CALLER.NSP", cfg)
	if len(after) != 1 || after[0].Name != "NEWSUB" {
		t.Errorf("NamesWithPrefix after Add: cache was stale, expected NEWSUB, got %#v", after)
	}
	if got := idx.LookupByName("NEWSUB", model.ObjectSubprogram, cfg); len(got) != 1 || got[0].Name != "NEWSUB" {
		t.Errorf("LookupByName after Add: cache was stale, expected NEWSUB, got %#v", got)
	}
}

// TestNameIndexCache_ReplaceOnAdd proves that replacing an existing entry (same
// path, new ObjectType) via Add invalidates the cache so the changed type is
// reflected — a rename/retype must never be served stale.
func TestNameIndexCache_ReplaceOnAdd(t *testing.T) {
	cfg := flatCfg()
	idx := &Index{}
	idx.Add("THING.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	// Populate cache: THING is a subprogram.
	if got := idx.LookupByName("THING", model.ObjectSubprogram, cfg); len(got) != 1 {
		t.Fatalf("precondition: THING should be a subprogram, got %#v", got)
	}

	// Re-add THING as a DDM (retype). Cache must invalidate.
	idx.Add("THING.NSN", model.FileAnalysis{ObjectType: model.ObjectDDM})

	if got := idx.LookupByName("THING", model.ObjectSubprogram, cfg); len(got) != 0 {
		t.Errorf("after retype: THING should no longer match ObjectSubprogram (stale cache), got %#v", got)
	}
	if got := idx.LookupByName("THING", model.ObjectDDM, cfg); len(got) != 1 {
		t.Errorf("after retype: THING should match ObjectDDM, got %#v", got)
	}
}

// TestNameIndexCache_ConcurrentLookupsAndAdd exercises the cache under concurrent
// readers and a writer, to be run with -race. The Index's own RWMutex protects
// the cache; a lookup building the cache under a full Lock must not race an Add
// invalidating it. Correctness of results is not asserted here (Add ordering is
// nondeterministic) — this test guards the memory model only.
func TestNameIndexCache_ConcurrentLookupsAndAdd(t *testing.T) {
	cfg := flatCfg()
	idx := seedFlatIndex()

	var readers sync.WaitGroup
	stop := make(chan struct{})

	// Readers: repeated lookups that build/read the cache.
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = idx.NamesWithPrefix("MY", model.ObjectSubprogram, "CALLER.NSP", cfg)
					_ = idx.LookupByName("MYSUB", model.ObjectSubprogram, cfg)
				}
			}
		}()
	}

	// Writer: repeated Adds that invalidate the cache. When the writer is done,
	// signal readers to stop, then join them.
	for i := 0; i < 500; i++ {
		idx.Add("MYSUB.NSN", model.FileAnalysis{ObjectType: model.ObjectSubprogram})
	}
	close(stop)
	readers.Wait()
}

// TestNameIndexCache_IdenticalOnCorpus proves behavior-preservation on a realistic
// analyzer-built corpus (not hand-seeded entries): the cached NamesWithPrefix and
// LookupByName results equal a fresh computation over a Build'd index, exercising
// the steplib chain via a library map.
func TestNameIndexCache_IdenticalOnCorpus(t *testing.T) {
	root := t.TempDir()
	writeNameIdxCorpusFile(t, root, "APP/CALLER.NSP", "* main\nCALLNAT 'MYSUB'\nEND\n")
	writeNameIdxCorpusFile(t, root, "APP/MYSUB.NSN", "* sub\nEND\n")
	writeNameIdxCorpusFile(t, root, "COMMON/SHARED.NSN", "* shared\nEND\n")
	writeNameIdxCorpusFile(t, root, "COMMON/MYUTIL.NSN", "* util\nEND\n")

	cfg := config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions:  []string{".NSP", ".NSN"},
			MaxFileSize: 1 << 20,
		},
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{
				{Name: "APP", Path: "APP", Steplibs: []string{"COMMON"}},
				{Name: "COMMON", Path: "COMMON"},
			},
		},
	}

	az := natural.New(nil)

	// Fresh index — each query builds/uses its own cache from scratch.
	freshIdx, err := Build(context.Background(), root, cfg, az, discardNameIdxLogger(), nil)
	if err != nil {
		t.Fatalf("Build fresh: %v", err)
	}
	// Cached index — pre-warm the cache with a throwaway query, then query for real.
	cachedIdx, err := Build(context.Background(), root, cfg, az, discardNameIdxLogger(), nil)
	if err != nil {
		t.Fatalf("Build cached: %v", err)
	}

	refPath := "APP/CALLER.NSP"
	prefixes := []struct {
		prefix string
		typ    model.ObjectType
	}{
		{"MY", model.ObjectSubprogram},
		{"", model.ObjectSubprogram},
		{"SH", model.ObjectSubprogram},
	}
	for _, p := range prefixes {
		want := freshIdx.NamesWithPrefix(p.prefix, p.typ, refPath, &cfg)
		_ = cachedIdx.NamesWithPrefix(p.prefix, p.typ, refPath, &cfg)
		got := cachedIdx.NamesWithPrefix(p.prefix, p.typ, refPath, &cfg)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("corpus NamesWithPrefix prefix=%q: got=%#v want=%#v", p.prefix, got, want)
		}
	}

	for _, name := range []string{"MYSUB", "SHARED", "MYUTIL", "NOPE"} {
		want := freshIdx.LookupByName(name, "", &cfg)
		_ = cachedIdx.LookupByName(name, "", &cfg)
		got := cachedIdx.LookupByName(name, "", &cfg)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("corpus LookupByName name=%q: got=%#v want=%#v", name, got, want)
		}
	}
}
