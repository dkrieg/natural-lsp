//go:build bench

// Package bench holds the scale and performance benchmarks for natural-lsp
// (feature 22). It is guarded by the `bench` build tag so it is invisible to
// `just verify` — the tag is off by default, so `go build ./...`, `go vet
// ./...`, and `go test ./...` never compile or run anything here. Run it via
// `just bench` (which passes `-tags bench`).
//
// The benchmarks drive the real analyzer and the real workspace index over a
// deterministic synthetic corpus produced by internal/workspace/corpusgen, so
// they exercise the production build path end-to-end (NFR-1 cold scaling, NFR-4
// peak memory). Per the feature's OQ-C/OQ-F decisions the benchmarks are
// measure-and-record: they report ns/op, allocs/op, and peak heap, and add only
// generous, relative sanity assertions (never an absolute wall-clock or MiB
// gate).
//
// Corpus size is tunable via the BENCH_CORPUS_OBJECTS environment variable so a
// routine `just bench` stays fast on a small default tier while a manual large
// run can scale up:
//
//	just bench                                  # small default tier (fast)
//	BENCH_CORPUS_OBJECTS=10000 just bench       # single large tier
//	BENCH_CORPUS_OBJECTS=30000 just bench       # headline-figure run
//
// When BENCH_CORPUS_OBJECTS is set, the multi-tier benchmarks additionally run
// that explicit size as an extra "custom" tier so a manual run records a figure
// at the requested scale.
package bench

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/workspace"
	"natural-lsp/internal/workspace/corpusgen"
)

// benchSeed fixes the corpus RNG so every run over a given tier is reproducible
// and diffable (corpusgen determinism contract, NFR-9). The value is an arbitrary
// fixed constant, NOT a recording date — the results-recording date lives in the
// plan's Results section and need not match this literal.
const benchSeed int64 = 0x2026_0722

// envCorpusObjects is the environment knob for the corpus size. Setting it adds
// a "custom" tier at that size (see customTier).
const envCorpusObjects = "BENCH_CORPUS_OBJECTS"

// tier names a corpus size to benchmark. Keeping the small default tier a few
// hundred objects keeps a routine `just bench` fast; medium/large give the
// scaling ratio T3/T4 need, and the env knob adds a manual headline tier.
type tier struct {
	name    string
	objects int
}

// defaultTiers are the built-in corpus sizes. Small is the routine default;
// medium and large give ≥3 tiers so a scaling ratio can be computed without any
// env knob. They are deliberately modest so an unattended `just bench` finishes
// in seconds — scale up via BENCH_CORPUS_OBJECTS for real headline numbers.
var defaultTiers = []tier{
	{name: "small", objects: 200},
	{name: "medium", objects: 1000},
	{name: "large", objects: 4000},
}

// tiers returns the tiers to benchmark: the built-in small/medium/large set,
// plus a "custom" tier when BENCH_CORPUS_OBJECTS is set to a positive integer
// (so a manual `BENCH_CORPUS_OBJECTS=30000 just bench` records that size). An
// unset or unparseable/non-positive value is ignored (small default only added
// via the built-ins).
func tiers() []tier {
	ts := make([]tier, len(defaultTiers))
	copy(ts, defaultTiers)
	if n, ok := customTier(); ok {
		ts = append(ts, tier{name: "custom" + strconv.Itoa(n), objects: n})
	}
	return ts
}

// customTier reads BENCH_CORPUS_OBJECTS and returns (n, true) when it parses to
// a positive integer, else (0, false). It is pure over the environment so the
// unit test can exercise the parsing via parseCorpusObjects.
func customTier() (int, bool) {
	return parseCorpusObjects(os.Getenv(envCorpusObjects))
}

// parseCorpusObjects parses the corpus-size knob value. A blank, non-numeric, or
// non-positive value yields (0, false); a positive integer yields (n, true).
// Split out from customTier so it is unit-testable without mutating the
// environment.
func parseCorpusObjects(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// discardLogger is a logger that drops everything, so benchmark output is not
// polluted by the analyzer's per-file skip/recovery logging.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildCorpus generates a fresh deterministic corpus of objectCount objects into
// a benchmark-scoped temp dir and loads its .natural-lsp.toml, returning the
// root, the parsed config, and a fresh analyzer. Generation and config loading
// happen OUTSIDE any timed region — callers invoke this before b.ResetTimer().
// It fails the benchmark on any setup error (a broken corpus would make the
// numbers meaningless).
func buildCorpus(b *testing.B, objectCount int) (root string, cfg config.Config, az *natural.Analyzer) {
	b.Helper()

	root = b.TempDir()
	if err := corpusgen.Generate(root, objectCount, benchSeed); err != nil {
		b.Fatalf("corpusgen.Generate(%d): %v", objectCount, err)
	}

	cfg, problems, err := config.Load(root + "/.natural-lsp.toml")
	if err != nil {
		b.Fatalf("config.Load: %v", err)
	}
	for _, p := range problems {
		b.Logf("config problem: %v", p)
	}

	return root, cfg, natural.New(nil)
}

// coldBuild runs a single cold (no-cache) workspace build over root and returns
// the indexed-file count. It is the exact production build path (Build ⇒
// BuildWithCache with an empty cachePath), so the benchmarks measure real work.
// It fails the benchmark on a build error.
func coldBuild(b *testing.B, root string, cfg config.Config, az *natural.Analyzer) int {
	b.Helper()

	idx, err := workspace.Build(context.Background(), root, cfg, az, discardLogger(), nil)
	if err != nil {
		b.Fatalf("workspace.Build: %v", err)
	}
	return len(idx.Keys())
}

// diskHashes computes the sha256 content hash for each workspace-relative path in
// keys, keyed by the same relative path used as an index key. It mirrors the hash
// BuildWithCache/saveIndex compute from disk, so a map built here reflects the
// true "unchanged" hashes; callers overwrite selected entries with a bogus value
// to force those files stale on a warm build. Unreadable files are omitted (they
// are treated as not-in-cache and re-analyzed — acceptable for the benchmark).
func diskHashes(b *testing.B, root string, keys []string) map[string]string {
	b.Helper()
	hashes := make(map[string]string, len(keys))
	for _, rel := range keys {
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		hashes[rel] = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	return hashes
}
