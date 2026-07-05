package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/model"
)

// TestDefinitionLocation tests that definitionLocation builds the protocol.Location
// correctly given a resolved definition: the target file URI and the Structure's
// SelectionRange (or {1,1}→{1,1} fallback if Structure is nil). FR-43.
//
// This is T6: Definition target range from the resolved file's Structure.
func TestDefinitionLocation(t *testing.T) {
	testdata := filepath.Join("testdata", "navigation")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Read the target fixture (helper.NSN with a Structure)
	targetPath := filepath.Join(testdata, "helper.NSN")
	targetContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target fixture %s: %v", targetPath, err)
	}

	// Analyze the target to get its FileAnalysis (including Structure)
	az := natural.New(nil)
	targetAnalysis, err := az.Analyze(targetPath, targetContent)
	if err != nil {
		t.Fatalf("failed to analyze target fixture: %v", err)
	}

	// Define the relative path as it would appear in the index
	relPath := filepath.Join("testdata", "navigation", "helper.NSN")

	tt := []struct {
		name          string
		relPath       string
		analysis      model.FileAnalysis
		content       string
		enc           protocol.PositionEncodingKind
		wantURIPrefix string
		checkRange    func(t *testing.T, got protocol.Range)
	}{
		{
			// A parsed object's Structure root carries a zero-width SelectionRange
			// at file top: a Natural object's name is its filename, not a source
			// token, so go-to-definition on a module target reveals the file at
			// its start. This exercises the fa.Structure != nil branch (reads
			// Structure.SelectionRange) and asserts the honest {0,0} caret.
			name:     "with Structure (object-root caret at file top)",
			relPath:  relPath,
			analysis: targetAnalysis,
			content:  string(targetContent),
			enc:      protocol.PositionEncodingKindUTF8,
			checkRange: func(t *testing.T, got protocol.Range) {
				if got.Start.Line != 0 || got.Start.Character != 0 {
					t.Errorf("Start = {%d,%d}, want {0,0} (object-root caret)", got.Start.Line, got.Start.Character)
				}
				if got.End.Line != 0 || got.End.Character != 0 {
					t.Errorf("End = {%d,%d}, want {0,0} (object-root caret)", got.End.Line, got.End.Character)
				}
			},
		},
		{
			name:    "nil Structure fallback to {1,1}→{1,1}",
			relPath: filepath.Join("testdata", "navigation", "empty.NSP"),
			analysis: model.FileAnalysis{
				ObjectType: model.ObjectProgram,
				Structure:  nil, // Simulating an unparseable file or no AST
			},
			content: "",
			enc:     protocol.PositionEncodingKindUTF8,
			checkRange: func(t *testing.T, got protocol.Range) {
				// Fallback: {1,1}→{1,1} in model → {0,0}→{0,0} in protocol
				if got.Start.Line != 0 || got.Start.Character != 0 {
					t.Errorf("Start = {%d,%d}, want {0,0} (fallback)", got.Start.Line, got.Start.Character)
				}
				if got.End.Line != 0 || got.End.Character != 0 {
					t.Errorf("End = {%d,%d}, want {0,0} (fallback)", got.End.Line, got.End.Character)
				}
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Act: call definitionLocation
			loc := definitionLocation(root, tc.relPath, tc.analysis, tc.content, tc.enc)

			// Assert: URI is correct
			if loc.URI == "" {
				t.Error("Location.URI is empty")
			}
			if !strings.HasPrefix(string(loc.URI), "file://") {
				t.Errorf("Location.URI = %q, does not start with file://", loc.URI)
			}

			// Assert: range is correct (delegated to the checkRange func per case)
			tc.checkRange(t, loc.Range)
		})
	}
}
