//go:build bench

package bench

import "testing"

// TestParseCorpusObjects covers the BENCH_CORPUS_OBJECTS knob parser. It lives
// in the bench-tagged package alongside the code it tests; it runs on demand via
// `go test -tags bench ./internal/workspace/bench/` (the `just bench` recipe
// passes -run=^$ to skip tests and run only benchmarks, so this does not slow a
// routine bench run). `just verify` never sees it (tag off).
func TestParseCorpusObjects(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		wantN  int
		wantOK bool
	}{
		{name: "empty", in: "", wantN: 0, wantOK: false},
		{name: "positive small", in: "200", wantN: 200, wantOK: true},
		{name: "positive large", in: "30000", wantN: 30000, wantOK: true},
		{name: "zero", in: "0", wantN: 0, wantOK: false},
		{name: "negative", in: "-5", wantN: 0, wantOK: false},
		{name: "non-numeric", in: "lots", wantN: 0, wantOK: false},
		{name: "trailing garbage", in: "100x", wantN: 0, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotN, gotOK := parseCorpusObjects(tc.in)
			if gotN != tc.wantN || gotOK != tc.wantOK {
				t.Fatalf("parseCorpusObjects(%q) = (%d, %v), want (%d, %v)",
					tc.in, gotN, gotOK, tc.wantN, tc.wantOK)
			}
		})
	}
}

// TestTiersIncludesThreeDefaults asserts the built-in tier set gives ≥3 tiers so
// a scaling ratio can be computed without any env knob (T3 DoD).
func TestTiersIncludesThreeDefaults(t *testing.T) {
	if len(defaultTiers) < 3 {
		t.Fatalf("want ≥3 default tiers for a scaling ratio, got %d", len(defaultTiers))
	}
	// Tiers must be strictly increasing so the ratio is meaningful.
	for i := 1; i < len(defaultTiers); i++ {
		if defaultTiers[i].objects <= defaultTiers[i-1].objects {
			t.Fatalf("default tiers must be strictly increasing: %+v", defaultTiers)
		}
	}
}
