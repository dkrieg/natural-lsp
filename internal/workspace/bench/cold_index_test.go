//go:build bench

package bench

import (
	"testing"
)

// BenchmarkColdIndex measures a cold (no-cache) workspace.Build over the
// generated corpus at ≥3 tiers (NFR-1, linear cold scaling). One sub-benchmark
// per tier via b.Run, so `go test -bench=. -benchmem` prints ns/op and
// allocs/op for each size; T9 divides ns/op by the object count to check the
// per-object time stays roughly flat across tiers (linear-ish).
//
// The corpus is generated fresh per tier OUTSIDE the timed region (before
// b.ResetTimer), so only workspace.Build is timed. Generation is deterministic
// (fixed seed) for reproducibility. b.ReportAllocs is enabled so the allocation
// figure is recorded per OQ-C.
//
// Per OQ-C this is measure-and-record: no absolute wall-clock assertion. The
// only guard is coldBuild failing the benchmark on a build error (a corpus that
// does not index would make the numbers meaningless). The relative
// per-object-time band check across tiers is left to the T9 results analysis,
// not gated here.
func BenchmarkColdIndex(b *testing.B) {
	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			// Setup (generation + config load) is excluded from the timed
			// region: build the corpus once per sub-benchmark, then reset.
			root, cfg, az := buildCorpus(b, t.objects)

			b.ReportAllocs()
			b.ResetTimer()

			var indexed int
			for i := 0; i < b.N; i++ {
				indexed = coldBuild(b, root, cfg, az)
			}

			b.StopTimer()
			// Report the actual indexed-file count as a metric so the recorded
			// output shows the corpus really produced ~t.objects files (the
			// generator also emits the .natural-lsp.toml, which is not indexed).
			b.ReportMetric(float64(indexed), "objects")
			// Per-object nanoseconds: the headline scaling figure. b.Elapsed()
			// covers only the timed region (build loop), divided by total work.
			if indexed > 0 && b.N > 0 {
				nsPerObject := float64(b.Elapsed().Nanoseconds()) / float64(indexed) / float64(b.N)
				b.ReportMetric(nsPerObject, "ns/object")
			}
		})
	}
}
