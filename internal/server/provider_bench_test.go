//go:build bench

// This file holds the interactive request-latency benchmarks for the
// workspace/symbol and references LSP providers (feature 22, T6/T9). They live
// in the internal/server package (not internal/workspace/bench) because the real
// providers take an unexported *handlerContext; benchmarking the genuine provider
// is preferred over replicating its query path. The `//go:build bench` tag keeps
// them out of `just verify` (tag off by default), exactly like the workspace
// bench package.
//
// These benchmarks measure the CURRENT (post-T8) providers: after T8, both
// workspace/symbol and references serve range conversions from the in-memory
// line-width table, so there is NO per-query disk sweep. The pre-fix baseline
// (the slow disk-reading behavior these benchmarks used to capture) is recorded
// verbatim in the plan's `## Results (recorded)` section, alongside the post-fix
// figures produced by these same benchmarks, so the before/after contrast is
// preserved. Run via `just bench` (which covers ./internal/server/... too).
//
// Corpus size is tunable via BENCH_CORPUS_OBJECTS (mirroring the workspace bench
// package); an unset value uses the small/medium/large default tiers.
package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
	"github.com/dkrieg/natural-lsp/internal/workspace/corpusgen"
)

// discardBenchLogger drops all log output so the analyzer's per-file logging does
// not pollute benchmark output.
func discardBenchLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// benchSeed fixes the corpus RNG (matches the workspace bench package seed) so
// every run over a given tier is reproducible. The value is an arbitrary fixed
// constant, NOT a recording date — the results-recording date lives in the plan's
// Results section and need not match this literal.
const benchSeed int64 = 0x2026_0722

// benchTier names a corpus size to benchmark.
type benchTier struct {
	name    string
	objects int
}

// benchTiers returns the built-in small/medium/large tiers plus a "custom" tier
// when BENCH_CORPUS_OBJECTS is a positive integer (for manual large runs). Kept
// deliberately in sync with internal/workspace/bench's defaults.
func benchTiers() []benchTier {
	ts := []benchTier{
		{name: "small", objects: 200},
		{name: "medium", objects: 1000},
		{name: "large", objects: 4000},
	}
	if v := os.Getenv("BENCH_CORPUS_OBJECTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ts = append(ts, benchTier{name: "custom" + strconv.Itoa(n), objects: n})
		}
	}
	return ts
}

// buildProviderContext generates a corpus, cold-builds the index + resolution
// set, and returns a handlerContext wired the way the server wires it after
// `initialized`. Everything here is OUTSIDE the timed region — callers invoke it
// before b.ResetTimer(). posEncoding is UTF-16 (the LSP default), the encoding
// that forces the disk read in the current providers (byte→UTF-16 unit
// conversion), so the baseline captures the real cost. It fails the benchmark on
// any setup error.
func buildProviderContext(b *testing.B, objects int) (*handlerContext, *workspace.Index) {
	b.Helper()

	root := b.TempDir()
	if err := corpusgen.Generate(root, objects, benchSeed); err != nil {
		b.Fatalf("corpusgen.Generate(%d): %v", objects, err)
	}

	cfg, _, err := config.Load(root + "/.natural-lsp.toml")
	if err != nil {
		b.Fatalf("config.Load: %v", err)
	}

	az := natural.New(nil)
	idx, err := workspace.Build(context.Background(), root, cfg, az, discardBenchLogger(), nil)
	if err != nil {
		b.Fatalf("workspace.Build: %v", err)
	}
	res := workspace.Resolve(idx, &cfg)

	hctx := &handlerContext{
		idx:         idx,
		res:         res,
		posEncoding: protocol.PositionEncodingKindUTF16,
		root:        root,
		cfg:         cfg,
		az:          az,
	}
	return hctx, idx
}

// BenchmarkProvide_WorkspaceSymbol benchmarks the REAL workspace/symbol provider
// as it exists CURRENTLY (post-T8): provideWorkspaceSymbols walks every indexed
// file's Structure and converts byte-offset columns to code units via the
// in-memory line-width table handed to the ForEachWithRange callback — there is
// NO per-query os.ReadFile sweep (T8 eliminated it). The recorded ns/op and
// allocs/op are the post-fix figures; the pre-fix disk-sweep baseline is recorded
// in the plan's Results section for the before/after contrast.
//
// The query "SUBPRG" matches every subprogram object root (a realistic non-empty
// symbol search). Index build is excluded from the timed region.
func BenchmarkProvide_WorkspaceSymbol(b *testing.B) {
	for _, t := range benchTiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			hctx, idx := buildProviderContext(b, t.objects)

			b.ReportAllocs()
			b.ResetTimer()

			var got int
			for i := 0; i < b.N; i++ {
				got = len(provideWorkspaceSymbols(hctx, "SUBPRG"))
			}

			b.StopTimer()
			b.ReportMetric(float64(len(idx.Keys())), "index-files")
			b.ReportMetric(float64(got), "matches")
		})
	}
}

// BenchmarkProvide_References benchmarks the REAL references provider as it exists
// CURRENTLY (post-T8): provideReferences finds the edge under the cursor, resolves
// it, then calls referenceSites, which walks every indexed file and converts
// ranges via the in-memory line-width table (references.go) — there is NO per-file
// os.ReadFile sweep (T8 eliminated it). The recorded numbers are the post-fix
// figures; the pre-fix disk-sweep baseline is recorded in the plan's Results
// section for the before/after contrast.
//
// The cursor is positioned on a program's first CALLNAT edge (a resolved static
// reference), so the sweep runs over the whole corpus looking for callers of the
// target subprogram. Index build and cursor selection are excluded from the timed
// region.
func BenchmarkProvide_References(b *testing.B) {
	for _, t := range benchTiers() {
		t := t
		b.Run(t.name, func(b *testing.B) {
			hctx, idx := buildProviderContext(b, t.objects)

			// Pick a program file with a resolvable CALLNAT edge and position the
			// cursor on that edge's start (ASCII corpus ⇒ byte col == UTF-16 char).
			params, ok := referenceParamsForProgram(hctx, idx)
			if !ok {
				b.Skip("no program with a CALLNAT edge found in corpus")
			}

			b.ReportAllocs()
			b.ResetTimer()

			var got int
			for i := 0; i < b.N; i++ {
				locs, err := provideReferences(hctx, params)
				if err != nil {
					b.Fatalf("provideReferences: %v", err)
				}
				got = len(locs)
			}

			b.StopTimer()
			b.ReportMetric(float64(len(idx.Keys())), "index-files")
			b.ReportMetric(float64(got), "references")
		})
	}
}

// referenceParamsForProgram finds a program (.NSP) file in the index that has a
// static CALLNAT edge and builds ReferenceParams whose cursor sits on that edge's
// start position. Returns ok=false if no such file exists. The corpus is ASCII, so
// the model byte column maps directly to a UTF-16 character offset.
func referenceParamsForProgram(hctx *handlerContext, idx *workspace.Index) (protocol.ReferenceParams, bool) {
	var chosenRel string
	var chosenEdge model.EdgeEntry
	found := false
	for _, rel := range idx.Keys() {
		if !strings.HasSuffix(rel, ".NSP") {
			continue
		}
		fa, ok := idx.Get(rel)
		if !ok {
			continue
		}
		for _, e := range fa.Edges {
			if e.Kind == model.EdgeCalls {
				chosenRel, chosenEdge, found = rel, e, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return protocol.ReferenceParams{}, false
	}

	absPath := hctx.root + "/" + chosenRel
	pos := protocol.Position{
		Line:      uint32(chosenEdge.Source.Start.Line - 1),
		Character: uint32(chosenEdge.Source.Start.Column - 1),
	}
	return protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(absPath)},
			Position:     pos,
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	}, true
}

// BenchmarkSemanticTokensFull measures the latency and throughput of the
// semantic-token classifier + encoder on large synthetic files (feature 29, T12).
//
// The benchmark classifies all tokens (lexical + semantic) in a buffer and encodes
// to the wire format. It tests:
//   - Classification latency (Phase A lexical + Phase B identifier/call/DDM reclassification)
//   - Encoding latency (LSP relative 5-int stream generation)
//   - End-to-end throughput on large programs
//
// Run via `just bench` (off `just verify`).
//
// Feature 29 T12, NFR-3 (interactive latency), measure-and-record.
func BenchmarkSemanticTokensFull(b *testing.B) {
	// Create a large synthetic Natural program with many token types.
	// This simulates a real medium-to-large program.
	content := generateLargeSyntheticProgram(5000)

	az := natural.New(nil)

	// Benchmark semantic token classification + encoding.
	// This measures the full pipeline: lexical + semantic classification, encoding.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokens := az.SemanticTokens("benchmark.NSP", []byte(content))
		_ = encodeSemanticTokens(tokens, content, protocol.PositionEncodingKindUTF8)
	}
	b.StopTimer()

	// Report metrics
	b.ReportMetric(float64(len(content)), "bytes")
}

// generateLargeSyntheticProgram creates a large Natural program for benchmarking.
// It includes various constructs: DEFINE DATA, variables, CALLNAT, PERFORM, etc.
func generateLargeSyntheticProgram(lines int) string {
	var sb strings.Builder

	// Header: DEFINE DATA section with many variables
	sb.WriteString("DEFINE DATA\n")
	sb.WriteString("LOCAL\n")
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf("  1 #VAR%-4d (A10)\n", i))
	}
	sb.WriteString("PARAMETER\n")
	for i := 0; i < 30; i++ {
		sb.WriteString(fmt.Sprintf("  1 #PARM%-4d (N5)\n", i))
	}
	sb.WriteString("END-DEFINE.\n\n")

	// Main body: various statements
	for i := 0; i < lines; i++ {
		switch i % 10 {
		case 0:
			sb.WriteString(fmt.Sprintf("CALLNAT 'PROG%-4d' #VAR1 #VAR2.\n", i%100))
		case 1:
			sb.WriteString(fmt.Sprintf("PERFORM SUBR-%d.\n", i%50))
		case 2:
			sb.WriteString(fmt.Sprintf("MOVE 'STRING-%d' TO #VAR0.\n", i))
		case 3:
			sb.WriteString(fmt.Sprintf("MOVE %d TO #VAR1.\n", i))
		case 4:
			sb.WriteString(fmt.Sprintf("/* Comment on line %d\n", i))
		case 5:
			sb.WriteString(fmt.Sprintf("* Full-line comment %d\n", i))
		case 6:
			sb.WriteString(fmt.Sprintf("READ #DDM-%d.\n", i%20))
		case 7:
			sb.WriteString(fmt.Sprintf("STORE #DDM-%d.\n", i%20))
		case 8:
			sb.WriteString(fmt.Sprintf("#VAR%-4d := #VAR%-4d + 1.\n", (i+1)%50, i%50))
		case 9:
			sb.WriteString(fmt.Sprintf("IF #VAR0 = %d THEN\n  MOVE 'YES' TO #VAR1.\nEND-IF.\n", i))
		}
	}

	// Subroutines
	for i := 0; i < 10; i++ {
		sb.WriteString(fmt.Sprintf("\nDEFINE SUBROUTINE SUBR-%d.\n", i))
		sb.WriteString(fmt.Sprintf("  MOVE *DATX TO #VAR0.\n"))
		sb.WriteString(fmt.Sprintf("  EXIT.\n"))
		sb.WriteString(fmt.Sprintf("END-SUBROUTINE.\n"))
	}

	sb.WriteString("\nEND.\n")
	return sb.String()
}
