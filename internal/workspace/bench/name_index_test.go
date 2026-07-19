//go:build bench

package bench

import (
	"context"
	"sort"
	"strings"
	"testing"

	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// buildIndexForQueries generates a corpus, cold-builds the index, and returns the
// index, config, and a representative referencing path (a program file, so the
// steplib chain is exercised for NamesWithPrefix). All of this is OUTSIDE the
// timed region — callers invoke it before b.ResetTimer(). It fails the benchmark
// on any setup error.
func buildIndexForQueries(b *testing.B, objects int) (idx *workspace.Index, cfg config.Config, referencingPath string) {
	b.Helper()

	root, cfg, az := buildCorpus(b, objects)
	var err error
	idx, err = workspace.Build(context.Background(), root, cfg, az, discardLogger(), nil)
	if err != nil {
		b.Fatalf("workspace.Build: %v", err)
	}

	// Pick a deterministic referencing path: the lexicographically-first program
	// file (PROGnnnn.NSP under some LIBnn). Programs are the callers in the
	// generated corpus, so this drives NamesWithPrefix through a real caller's
	// steplib chain (current library → its one steplib).
	keys := idx.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(k, "/PROG") {
			referencingPath = k
			break
		}
	}
	if referencingPath == "" && len(keys) > 0 {
		referencingPath = keys[0]
	}
	return idx, cfg, referencingPath
}

// BenchmarkNamesWithPrefix isolates the completion prefix-enumeration cost
// (Index.NamesWithPrefix) at each tier (feature 22, T6 — the OQ-E measurement).
// NamesWithPrefix rebuilds the whole name index (buildNameIndex, O(files)) on
// EVERY call — this benchmark measures whether that per-keystroke cost is hot at
// scale, feeding the T7 conditional-cache decision.
//
// The prefix "SUBPRG" matches every subprogram in the corpus (a realistic,
// non-empty completion prefix), and the type filter is ObjectSubprogram (the
// CALLNAT completion context). The index build is excluded from the timed region.
func BenchmarkNamesWithPrefix(b *testing.B) {
	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			idx, cfg, refPath := buildIndexForQueries(b, t.objects)

			b.ReportAllocs()
			b.ResetTimer()

			var got int
			for i := 0; i < b.N; i++ {
				out := idx.NamesWithPrefix("SUBPRG", model.ObjectSubprogram, refPath, &cfg)
				got = len(out)
			}

			b.StopTimer()
			b.ReportMetric(float64(len(idx.Keys())), "index-files")
			b.ReportMetric(float64(got), "matches")
		})
	}
}

// BenchmarkLookupByName isolates the definition-lookup cost (Index.LookupByName)
// at each tier (feature 22, T6 — the OQ-E measurement). LookupByName is O(files)
// per call: it ForEach-walks the whole index computing objectIdentity for each
// entry. This benchmark measures that per-request cost at scale.
//
// The looked-up name is "SUBPRG0000" (the first subprogram, guaranteed present in
// the generated corpus) with the ObjectSubprogram type filter — the DDM/module
// resolve pattern the completion and hover providers use (one call per request).
// The index build is excluded from the timed region.
func BenchmarkLookupByName(b *testing.B) {
	for _, t := range tiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			idx, cfg, _ := buildIndexForQueries(b, t.objects)

			b.ReportAllocs()
			b.ResetTimer()

			var got int
			for i := 0; i < b.N; i++ {
				out := idx.LookupByName("SUBPRG0000", model.ObjectSubprogram, &cfg)
				got = len(out)
			}

			b.StopTimer()
			b.ReportMetric(float64(len(idx.Keys())), "index-files")
			b.ReportMetric(float64(got), "matches")
		})
	}
}
