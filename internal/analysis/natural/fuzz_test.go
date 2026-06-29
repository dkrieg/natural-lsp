package natural

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse is the executable proof of the parser's robustness (M-6, ADR-013):
// the parser must NEVER panic and must ALWAYS return a non-nil *Program for
// arbitrary input — even malformed, garbage, or edge-case bytes.
//
// The seed corpus is drawn from the committed testdata/parser fixtures
// (01-08) representing real, known-interesting Natural constructs (lexer
// tokens, statements, READ/STORE, DEFINE DATA arrays/redefine, parse errors,
// inline comments), plus hand-written edge cases (empty, unterminated string,
// lone parentheses, deeply nested parens, multi-byte UTF-8, very long line).
//
// Feature 00 Task 11; M-6, FR-43, ADR-013.
func FuzzParse(f *testing.F) {
	// Seed from the existing testdata/parser fixtures (01-08).
	// Read at fuzz-setup time; if a read fails, skip that seed with a warning
	// (fixture not found is not a test failure — it's a missing file that the
	// build will have flagged already).
	fixtureNames := []string{
		"01-lexer-token-types.nsp",
		"02-lexer-multi-line.nsp",
		"03-parser-statements.nsp",
		"04-parser-parse-errors.nsp",
		"05-inline-comments.nsp",
		"06-read-store.nsp",
		"07-data-arrays.nsp",
		"08-data-redefine.nsp",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("testdata", "parser", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure; the test framework
			// will report missing testdata separately).
			continue
		}
		f.Add(data)
	}

	// Hand-written tricky seeds exercising edge cases.

	// Empty input: the lexer must consume EOF without panic.
	f.Add([]byte(""))

	// Lone unterminated string literal.
	f.Add([]byte("'unterminated"))

	// Lone opening parenthesis (not closed).
	f.Add([]byte("("))

	// Deeply nested parentheses (tests parser recursion limits gracefully).
	f.Add([]byte("(((((()))))))"))

	// Bare CALLNAT with no target.
	f.Add([]byte("CALLNAT"))

	// Multi-byte UTF-8 characters (Natural is ASCII, but the lexer must
	// handle arbitrary bytes without panic).
	f.Add([]byte("CALLNAT 'café'"))

	// Very long line (tests lexer buffer handling).
	longLine := make([]byte, 10000)
	for i := range longLine {
		longLine[i] = 'A'
	}
	f.Add(longLine)

	// Mixed valid and invalid: valid statement followed by garbage.
	f.Add([]byte("CALLNAT 'MYPROG'\n\x00\x01\x02"))

	// Newline-heavy input.
	f.Add([]byte("\n\n\n\nCALLNAT\n\n\n"))

	// Leading/trailing whitespace.
	f.Add([]byte("  \t\n  CALLNAT 'PROG'  \t\n  "))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: construct the lexer and parser from the arbitrary input.
		lexer := NewLexer(string(input))
		parser := NewParser(lexer)

		// Act: parse the input. The fuzzer automatically catches panics,
		// but we assert here that prog is non-nil as an additional safeguard.
		prog, _ := parser.Parse()

		// Assert: parser must ALWAYS return a non-nil *Program, even for
		// arbitrary/garbage input. A nil return is a violation of the
		// contract (M-6, FR-43: no silent gaps, graceful degradation).
		if prog == nil {
			t.Fatal("Parse() returned nil *Program for arbitrary input; want non-nil *Program")
		}
	})
}
