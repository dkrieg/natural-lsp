//go:build bench

// This file holds the semantic-tokens classifier latency benchmark (feature 35, T4).
// It measures Analyzer.SemanticTokens — the interactive hot path the LSP server calls
// for textDocument/semanticTokens/full and .../range on the open buffer — over a large,
// deterministic synthetic Natural program at several sizes, so the O(n²)→O(n log n) dedup
// win (T2) and the parse-once win (T3) are visible as a scaling curve.
//
// The classifier alone is benchmarked (NOT the LSP relative-5-int encoder — that is
// unchanged by feature 35 and is already covered by internal/server's
// BenchmarkSemanticTokensFull). Measuring az.SemanticTokens in isolation makes the
// classifier's scaling the thing under test.
//
// The `//go:build bench` tag keeps this OUT of `just verify` (the tag is off by default),
// matching feature 22's convention. Run via `just bench` (which covers
// ./internal/analysis/natural/... too) or directly:
//
//	go test -tags bench -bench BenchmarkSemanticTokens -benchmem ./internal/analysis/natural/
//
// The synthetic program is generated in-bench and fully deterministic (no rand / no time),
// so token counts and therefore ns/op are reproducible across runs at a given size.
package natural

import (
	"fmt"
	"strings"
	"testing"
)

// semanticTokensBenchSizes are the line counts benchmarked. They span a >16× range so the
// classifier's scaling (linearithmic, post-T2) is visible: doubling the input should roughly
// double ns/op, NOT quadruple it (the pre-T2 O(n²) reconstruction loop).
var semanticTokensBenchSizes = []int{500, 2000, 8000}

// BenchmarkSemanticTokens exercises the classifier (Analyzer.SemanticTokens) over a large
// synthetic Natural program at several sizes. Each sub-benchmark reports allocs/op (via
// b.ReportAllocs) plus the source byte size and the emitted semantic-token count, so the
// ns/op scaling can be read against a known token count per size. Feature 35, T4; NFR-3;
// measure-and-record.
func BenchmarkSemanticTokens(b *testing.B) {
	az := New(nil)

	for _, lines := range semanticTokensBenchSizes {
		content := []byte(generateLargeSyntheticNatural(lines))
		// One warm call to record the emitted token count for the Results table.
		tokenCount := len(az.SemanticTokens("bench.NSP", content))

		b.Run(fmt.Sprintf("lines=%d", lines), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = az.SemanticTokens("bench.NSP", content)
			}
			b.StopTimer()
			// Custom metrics (reported after the timed loop so they surface on the
			// result line): source byte size and emitted semantic-token count, both
			// deterministic per size, so ns/op can be read against a known token count.
			b.ReportMetric(float64(len(content)), "src-bytes")
			b.ReportMetric(float64(tokenCount), "tokens")
		})
	}
}

// generateLargeSyntheticNatural builds a deterministic Natural program whose token count
// scales linearly with lines. It repeats a realistic mix of constructs — a DEFINE DATA
// block with many declared variables, then a body of CALLNAT / PERFORM / MOVE / COMPUTE /
// READ / STORE / system-var (`*DATX`) statements — so every classifier phase (lexical,
// identifier, call, DDM/view, system-var) and the merge/dedup path are all exercised at
// scale. It mirrors internal/server's generateLargeSyntheticProgram but lives in-package so
// the classifier can be benchmarked behind the Analyzer seam without importing server.
func generateLargeSyntheticNatural(lines int) string {
	var sb strings.Builder

	// DEFINE DATA: a large declaration block so buildVarLookup and the identifier phase have
	// many variables to resolve (declared-variable classification is the common case).
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

	// Body: a repeating mix of statement kinds so the token count grows linearly with `lines`
	// and every classification path is represented.
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
			sb.WriteString(fmt.Sprintf("COMPUTE #VAR%-4d = #VAR%-4d + 1.\n", (i+1)%50, i%50))
		case 9:
			sb.WriteString(fmt.Sprintf("MOVE *DATX TO #VAR%-4d.\n", i%50))
		}
	}

	// Subroutines so the PERFORM targets resolve to same-object definitions.
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf("\nDEFINE SUBROUTINE SUBR-%d.\n", i))
		sb.WriteString("  MOVE *DATX TO #VAR0.\n")
		sb.WriteString("  EXIT.\n")
		sb.WriteString("END-SUBROUTINE.\n")
	}

	sb.WriteString("\nEND.\n")
	return sb.String()
}
