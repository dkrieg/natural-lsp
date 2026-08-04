package natural

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestSemanticTokens_Golden is the characterization test for feature 35, Task T1.
// It captures the CURRENT byte-exact output of Analyzer.SemanticTokens across a
// comprehensive corpus: all 7 existing fixtures plus 3 inline cases covering
// numeric-range, unparseable fallback, and *DATX comment-vs-sysvar suppression.
//
// The test serializes each result to a stable JSON golden file (under
// testdata/semantictokens/golden/<name>.json) and compares the live output
// against the checked-in golden. A regression is visible as a golden diff.
//
// To regenerate goldens, run with UPDATE_GOLDEN=1:
//
//	UPDATE_GOLDEN=1 go test -run TestSemanticTokens_Golden ./internal/analysis/natural
//
// Then commit the updated golden files. Subsequent runs without UPDATE_GOLDEN
// compare against the golden and fail on any diff.
func TestSemanticTokens_Golden(t *testing.T) {
	// Golden directory where test outputs are stored.
	const goldenDir = "testdata/semantictokens/golden"

	// Create the golden directory if it doesn't exist.
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(goldenDir, 0755); err != nil {
			t.Fatalf("failed to create golden directory: %v", err)
		}
	}

	// Define test cases: fixture name, content, and the analyzer path (for classification).
	testCases := []struct {
		name    string
		path    string
		content []byte
	}{
		// Existing fixtures (load from testdata/semantictokens/*.NSP).
		{"lexical", "lexical.NSP", readFixture(t, "testdata/semantictokens/lexical.NSP")},
		{"variables", "variables.NSP", readFixture(t, "testdata/semantictokens/variables.NSP")},
		{"calls", "calls.NSP", readFixture(t, "testdata/semantictokens/calls.NSP")},
		{"ddm", "ddm.NSP", readFixture(t, "testdata/semantictokens/ddm.NSP")},
		{"sysvar", "sysvar.NSP", readFixture(t, "testdata/semantictokens/sysvar.NSP")},
		{"grouped", "grouped.NSP", readFixture(t, "testdata/semantictokens/grouped.NSP")},
		{"paramwrite", "paramwrite.NSP", readFixture(t, "testdata/semantictokens/paramwrite.NSP")},

		// Inline case (a): numeric-range (FINDING C) — "COMPUTE #A = 5-3".
		// Tests that numeric literals don't over-extend and consume operators.
		{"numeric-range", "numrange.NSP", []byte("COMPUTE #A = 5-3\n")},

		// Inline case (b): unparseable fallback (FR-43) — garbage input with lexable tokens.
		// Tests that Phase A falls back gracefully when the input doesn't form a valid program.
		{"unparseable", "broken.NSP", []byte("%%% @@@ !!!\nMOVE 42 'HELLO' ###junk\n<< dangling")},

		// Inline case (c): *DATX in comment and as sysvar (comment-vs-sysvar + suppression).
		// Tests that a *DATX at line start is a comment (not a system var), and a mid-line
		// *DATX is classified as a system variable with readonly+defaultLibrary modifiers.
		{"comment-sysvar", "comment-sysvar.NSP", []byte("* comment with *DATX ...\nMOVE *DATX TO #X\n")},
	}

	az := New(nil)
	updateMode := os.Getenv("UPDATE_GOLDEN") != ""

	// Track union of Types and Modifiers seen across all fixtures to verify coverage.
	seenTypes := make(map[model.SemanticTokenType]bool)
	seenModifiers := make(map[model.SemanticTokenModifier]bool)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call SemanticTokens to get the current output.
			tokens := az.SemanticTokens(tc.path, tc.content)

			// Serialize tokens to a stable JSON form for the golden.
			goldenData := serializeTokensForGolden(tokens)

			// Golden file path.
			goldenPath := filepath.Join(goldenDir, tc.name+".json")

			if updateMode {
				// Write the golden file.
				if err := writeJSONGolden(goldenPath, goldenData); err != nil {
					t.Fatalf("failed to write golden %q: %v", goldenPath, err)
				}
				t.Logf("updated golden: %s", goldenPath)
			} else {
				// Compare against the golden file.
				expected, err := readJSONGolden(goldenPath)
				if err != nil {
					t.Fatalf("failed to read golden %q: %v", goldenPath, err)
				}

				if !bytes.Equal(goldenData, expected) {
					t.Errorf("golden mismatch for %q\nExpected:\n%s\n\nGot:\n%s",
						tc.name, string(expected), string(goldenData))
				}
			}

			// Track types and modifiers for meta-coverage assertion.
			for _, tok := range tokens {
				seenTypes[tok.Type] = true
				if tok.Modifiers != 0 {
					seenModifiers[tok.Modifiers] = true
				}
				// Also track individual modifier bits.
				for bit := uint32(0); bit < 5; bit++ {
					if tok.Modifiers&(1<<bit) != 0 {
						seenModifiers[1<<bit] = true
					}
				}
			}
		})
	}

	// Meta-assertion: the corpus covers all legend token types and at least
	// one instance of each modifier. This ensures the golden captures the
	// full feature scope.
	if !updateMode {
		expectedTypes := map[model.SemanticTokenType]bool{
			model.SemanticTokenTypeKeyword:   true,
			model.SemanticTokenTypeComment:   true,
			model.SemanticTokenTypeString:    true,
			model.SemanticTokenTypeNumber:    true,
			model.SemanticTokenTypeOperator:  true,
			model.SemanticTokenTypeVariable:  true,
			model.SemanticTokenTypeParameter: true,
			model.SemanticTokenTypeFunction:  true,
			model.SemanticTokenTypeType:      true,
			model.SemanticTokenTypeProperty:  true,
		}

		for tt := range expectedTypes {
			if !seenTypes[tt] {
				t.Errorf("golden corpus missing token type %q — coverage incomplete", tt)
			}
		}

		// Check that we've seen at least one instance of each modifier flag.
		expectedModifiers := []struct {
			name string
			bit  model.SemanticTokenModifier
		}{
			{"declaration", model.SemanticTokenModifierDeclaration},
			{"definition", model.SemanticTokenModifierDefinition},
			{"readonly", model.SemanticTokenModifierReadonly},
			{"modification", model.SemanticTokenModifierModification},
			{"defaultLibrary", model.SemanticTokenModifierDefaultLibrary},
		}

		for _, em := range expectedModifiers {
			if !seenModifiers[em.bit] {
				t.Errorf("golden corpus missing modifier %q — coverage incomplete", em.name)
			}
		}
	}
}

// serializeTokensForGolden converts a []SemanticToken to a stable JSON form
// suitable for golden comparison. The JSON includes all fields (Range, Type, Modifiers)
// and is deterministic across runs (tokens must already be in order).
func serializeTokensForGolden(tokens []model.SemanticToken) []byte {
	// Encode tokens as a JSON array of objects with readable structure.
	type tokenJSON struct {
		Range struct {
			Start struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
			End struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"end"`
		} `json:"range"`
		Type      string   `json:"type"`
		Modifiers uint32   `json:"modifiers"`
		ModNames  []string `json:"modifierNames"` // decoded modifier names for readability
	}

	goldenTokens := make([]tokenJSON, len(tokens))
	for i, tok := range tokens {
		goldenTokens[i].Range.Start.Line = tok.Range.Start.Line
		goldenTokens[i].Range.Start.Column = tok.Range.Start.Column
		goldenTokens[i].Range.End.Line = tok.Range.End.Line
		goldenTokens[i].Range.End.Column = tok.Range.End.Column
		goldenTokens[i].Type = string(tok.Type)
		goldenTokens[i].Modifiers = uint32(tok.Modifiers)

		// Decode modifiers to their names for readability.
		var modNames []string
		if tok.Modifiers&model.SemanticTokenModifierDeclaration != 0 {
			modNames = append(modNames, "declaration")
		}
		if tok.Modifiers&model.SemanticTokenModifierDefinition != 0 {
			modNames = append(modNames, "definition")
		}
		if tok.Modifiers&model.SemanticTokenModifierReadonly != 0 {
			modNames = append(modNames, "readonly")
		}
		if tok.Modifiers&model.SemanticTokenModifierModification != 0 {
			modNames = append(modNames, "modification")
		}
		if tok.Modifiers&model.SemanticTokenModifierDefaultLibrary != 0 {
			modNames = append(modNames, "defaultLibrary")
		}
		goldenTokens[i].ModNames = modNames
	}

	// Marshal to JSON with indentation for readability.
	data, err := json.MarshalIndent(goldenTokens, "", "  ")
	if err != nil {
		// This should never happen, but handle gracefully.
		panic("failed to marshal tokens to JSON: " + err.Error())
	}

	return data
}

// writeJSONGolden writes JSON data to a golden file.
func writeJSONGolden(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// readJSONGolden reads JSON data from a golden file.
func readJSONGolden(path string) ([]byte, error) {
	return os.ReadFile(path)
}
