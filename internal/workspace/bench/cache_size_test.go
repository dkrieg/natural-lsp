//go:build bench

package bench

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// BenchmarkCacheSize measures the on-disk cache size of the NEW format (compact
// JSON + gzip, feature 24) against the OLD format (indented JSON) it replaced,
// per tier — the recorded evidence for Story 1 AC1 (≥10× smaller) and AC3
// (roughly linear scaling).
//
// This is a measure-and-record bench (feature-22 pattern): the timed region is
// nominal (the numbers of interest are the reported byte metrics, not ns/op), and
// the only hard assertion is a GENEROUS floor on the compaction ratio (>3) so a
// regression that silently disables gzip is caught — the ≥10× headline stays a
// recorded figure in plan.md's ## Results, not a tight CI gate.
//
// The OLD baseline is computed deterministically IN-MEMORY without any git
// checkout: the new gzip cache is read back, gunzipped to recover the compact
// JSON bytes, and re-indented via json.Indent — reproducing byte-for-byte what
// json.MarshalIndent(cache, "", "    ") would have written, since the persisted
// JSON structure is identical between the two formats (only whitespace and
// compression differ).
func BenchmarkCacheSize(b *testing.B) {
	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			root, cfg, az := buildCorpus(b, t.objects)
			cachePath := cachePathFor(root, cfg)

			// One cold build writes the new-format (gzip) cache. Done once,
			// outside the (nominal) timed region.
			idx, _, _, err := workspace.BuildWithCache(
				context.Background(), root, cfg, az, discardLogger(), cachePath, nil, nil)
			if err != nil {
				b.Fatalf("BuildWithCache: %v", err)
			}
			objects := len(idx.Keys())
			if objects == 0 {
				b.Fatalf("empty index — corpus produced no indexed objects")
			}

			// NEW: the actual on-disk gzip cache size.
			info, err := os.Stat(cachePath)
			if err != nil {
				b.Fatalf("stat cache %q: %v", cachePath, err)
			}
			newBytes := info.Size()

			// OLD: recover the compact JSON (gunzip the new cache) then re-indent
			// it — byte-identical to the pre-24 json.MarshalIndent output.
			raw, err := os.ReadFile(cachePath)
			if err != nil {
				b.Fatalf("read cache %q: %v", cachePath, err)
			}
			if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
				b.Fatalf("cache is not gzip (first bytes %v) — new format not active?", raw[:min(2, len(raw))])
			}
			gz, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				b.Fatalf("gzip.NewReader: %v", err)
			}
			compactJSON, err := io.ReadAll(gz)
			if err != nil {
				b.Fatalf("gunzip: %v", err)
			}
			if err := gz.Close(); err != nil {
				b.Fatalf("gzip close: %v", err)
			}
			var indented bytes.Buffer
			if err := json.Indent(&indented, compactJSON, "", "    "); err != nil {
				b.Fatalf("json.Indent: %v", err)
			}
			oldBytes := int64(indented.Len())

			ratio := float64(oldBytes) / float64(newBytes)

			// Generous sanity floor (NOT the ≥10× headline gate): catches a
			// regression that silently disables compaction.
			if ratio < 3 {
				b.Fatalf("compaction ratio %.2fx below floor 3x (old=%d new=%d) — gzip/compaction regressed?",
					ratio, oldBytes, newBytes)
			}

			// The measured work above dominates; the loop is nominal (the byte
			// metrics are the recorded result, not ns/op). Report AFTER the loop
			// so testing does not overwrite the custom metrics.
			for i := 0; i < b.N; i++ {
				_ = ratio
			}
			b.ReportMetric(float64(newBytes)/float64(objects), "new-bytes/object")
			b.ReportMetric(float64(oldBytes)/float64(objects), "old-bytes/object")
			b.ReportMetric(ratio, "compaction-x")
			b.ReportMetric(float64(newBytes), "new-total-bytes")
			b.ReportMetric(float64(oldBytes), "old-total-bytes")
			b.ReportMetric(float64(objects), "objects")
		})
	}
}
