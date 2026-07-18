//go:build bench

package bench

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"natural-lsp/internal/config"
	"natural-lsp/internal/workspace"
)

// cachePathFor returns the on-disk cache path the server uses (root/cfg.Cache.Path).
func cachePathFor(root string, cfg config.Config) string {
	return filepath.Join(root, cfg.Cache.Path)
}

// BenchmarkWarmStart_fullhit measures a SECOND BuildWithCache over an UNCHANGED
// corpus — a full cache hit — after a first cold pass wrote the cache (NFR-2,
// sub-second warm start). The cold priming build and cache write happen OUTSIDE
// the timed region (before b.ResetTimer). The benchmark asserts the returned
// staleCount == 0 on the warm build to PROVE the cache path was actually hit (no
// silent rebuild), then records ns/op and allocs/op for the warm path.
//
// Per OQ-C this is measure-and-record: the sub-second claim is a recorded figure
// (ns/op at the large tier), not a gated assertion — the only hard assertion is
// staleCount == 0 (a correctness invariant of the warm path, not a wall-clock
// threshold).
func BenchmarkWarmStart_fullhit(b *testing.B) {
	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			root, cfg, az := buildCorpus(b, t.objects)
			cachePath := cachePathFor(root, cfg)

			// Prime: a first cold build that writes the cache (currentHashes=nil ⇒
			// hashes computed from disk). Excluded from the timed region. A cold
			// build reports staleCount 0 (nothing was in the cache to be stale
			// against) — the documented contract.
			coldIdx, coldStale, coldTotal, err := workspace.BuildWithCache(
				context.Background(), root, cfg, az, discardLogger(), cachePath, nil, nil)
			if err != nil {
				b.Fatalf("prime BuildWithCache: %v", err)
			}
			if coldStale != 0 {
				b.Fatalf("prime cold build staleCount = %d, want 0 (cold build reports 0)", coldStale)
			}
			b.Logf("primed: %d files indexed, %d total (cold)", len(coldIdx.Keys()), coldTotal)

			b.ReportAllocs()
			b.ResetTimer()

			var warmStale, warmTotal, warmIndexed int
			for i := 0; i < b.N; i++ {
				idx, stale, total, err := workspace.BuildWithCache(
					context.Background(), root, cfg, az, discardLogger(), cachePath, nil, nil)
				if err != nil {
					b.Fatalf("warm BuildWithCache: %v", err)
				}
				warmStale, warmTotal, warmIndexed = stale, total, len(idx.Keys())
			}

			b.StopTimer()

			// PROVE the warm path was hit: a full cache hit re-analyzes nothing.
			if warmStale != 0 {
				b.Fatalf("warm full-hit staleCount = %d, want 0 (cache not hit — silent rebuild?)", warmStale)
			}
			b.ReportMetric(float64(warmIndexed), "objects")
			b.ReportMetric(float64(warmStale), "reanalyzed")
			b.ReportMetric(float64(warmTotal), "total-files")
			// Warm ns/object: contrast against BenchmarkColdIndex's ns/object at the
			// same tier to see the warm-start speedup.
			if warmIndexed > 0 && b.N > 0 {
				nsPerObject := float64(b.Elapsed().Nanoseconds()) / float64(warmIndexed) / float64(b.N)
				b.ReportMetric(nsPerObject, "ns/object")
			}
		})
	}
}

// BenchmarkWarmStart_partial measures a warm BuildWithCache where a HANDFUL of
// files' content changed — only the changed files re-analyze (NFR-2 incremental
// warm start). After a cold priming pass writes the cache, each timed build is
// driven with an explicit currentHashes map that marks exactly K files stale (a
// bogus hash that never equals the real on-disk hash saveIndex stores), so every
// iteration re-analyzes the SAME K files regardless of the cache rewrite between
// iterations. This uses the documented currentHashes test seam (index.go:406) to
// make the changed set deterministic, and asserts the returned staleCount == K to
// prove only the changed set was re-analyzed.
func BenchmarkWarmStart_partial(b *testing.B) {
	const changed = 5

	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			root, cfg, az := buildCorpus(b, t.objects)
			cachePath := cachePathFor(root, cfg)

			// Prime the cache with a cold build over the corpus (currentHashes=nil ⇒
			// hashes computed from disk and stored). Excluded from the timed region.
			primeIdx, _, _, err := workspace.BuildWithCache(
				context.Background(), root, cfg, az, discardLogger(), cachePath, nil, nil)
			if err != nil {
				b.Fatalf("prime BuildWithCache: %v", err)
			}

			// Pick K indexed relative paths deterministically (sorted) and build a
			// currentHashes map: every indexed file keeps its true disk hash EXCEPT
			// the K chosen files, which get a bogus hash so Load flags exactly them
			// stale. saveIndex re-stores the true disk hash for the re-analyzed K
			// files, so on the next iteration our bogus hash still mismatches ⇒ the
			// same K stay stale (stable across b.N).
			keys := primeIdx.Keys()
			sort.Strings(keys)
			if len(keys) < changed {
				b.Skip("corpus too small to mark K files stale")
			}
			hashes := diskHashes(b, root, keys)
			for i := 0; i < changed; i++ {
				hashes[keys[i]] = "bogus-changed-hash-forcing-reanalysis"
			}

			b.ReportAllocs()
			b.ResetTimer()

			var stale, total, indexed int
			for i := 0; i < b.N; i++ {
				idx, s, tot, err := workspace.BuildWithCache(
					context.Background(), root, cfg, az, discardLogger(), cachePath, hashes, nil)
				if err != nil {
					b.Fatalf("warm partial BuildWithCache: %v", err)
				}
				stale, total, indexed = s, tot, len(idx.Keys())
			}

			b.StopTimer()

			if stale != changed {
				b.Fatalf("warm partial staleCount = %d, want %d (only the marked files should re-analyze)", stale, changed)
			}
			b.ReportMetric(float64(indexed), "objects")
			b.ReportMetric(float64(stale), "reanalyzed")
			b.ReportMetric(float64(total), "total-files")
		})
	}
}
