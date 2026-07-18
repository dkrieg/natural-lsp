//go:build bench

package bench

import (
	"context"
	"runtime"
	"testing"

	"natural-lsp/internal/workspace"
)

// BenchmarkColdIndexMemory measures the peak heap held around a cold
// workspace.Build (NFR-4, "tens of thousands without memory exhaustion"). It
// reports peak HeapAlloc (and HeapInuse) via b.ReportMetric at each tier, so T9
// can check the heap-per-object stays within a generous band tier-to-tier
// (roughly-linear growth per OQ-F) — NOT an absolute MiB cap, which would be
// machine-dependent and brittle.
//
// Method: force a GC before reading MemStats so the figure reflects HELD (live,
// post-collection) memory rather than transient allocation churn. We snapshot
// before the build to establish a baseline, run one build, hold a reference to
// the resulting index across the read (so its heap is not collected early),
// GC + read after, and report the after-figure and the delta. Only one build
// iteration is meaningful for a peak-memory reading (b.N iterations would just
// re-measure the same held set), so the loop keeps a single index alive and the
// measurement is taken once.
func BenchmarkColdIndexMemory(b *testing.B) {
	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			root, cfg, az := buildCorpus(b, t.objects)

			b.ReportAllocs()

			// Baseline: held heap before the build.
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			// Build once and keep the index alive across the post-build read so
			// its retained heap is measured, not collected. b.N is honored by
			// rebuilding, but only the last index is held for the reading — the
			// peak of a single cold build is the NFR-4 figure.
			b.ResetTimer()
			indexed := 0
			var keep any
			for i := 0; i < b.N; i++ {
				idx, err := workspace.Build(context.Background(), root, cfg, az, discardLogger(), nil)
				if err != nil {
					b.Fatalf("workspace.Build: %v", err)
				}
				indexed = len(idx.Keys())
				keep = idx
			}
			b.StopTimer()

			// Peak held heap: GC then read, with the index still referenced.
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(keep)

			const miB = 1024 * 1024
			b.ReportMetric(float64(indexed), "objects")
			b.ReportMetric(float64(after.HeapAlloc)/miB, "peak-heap-MiB")
			b.ReportMetric(float64(after.HeapInuse)/miB, "heap-inuse-MiB")
			// Delta over baseline: the heap attributable to holding the index.
			if after.HeapAlloc > before.HeapAlloc {
				b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/miB, "index-heap-MiB")
			}
			// Per-object held heap: the roughly-linear-growth figure T9 compares
			// across tiers (generous band, no absolute cap — OQ-F).
			if indexed > 0 && after.HeapAlloc > before.HeapAlloc {
				b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/float64(indexed), "heap-bytes/object")
			}
		})
	}
}
