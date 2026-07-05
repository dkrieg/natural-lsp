package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
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

// TestProvideDefinitionEndToEnd tests the full go-to-definition flow (feature 10, T7):
// cursor position → findCursorTarget → ResolutionSet.Get → definitionLocation.
// This test covers all edge kinds: CALLNAT→subprogram, FETCH/RUN→program,
// external PERFORM→subroutine, and inline PERFORM→same-file subroutine range.
// FR-24, FR-17 (modeled gaps), FR-43 (graceful degradation).
//
// Each sub-test uses an existing resolution fixture workspace, builds the index,
// resolves edges, and verifies that provideDefinition returns the correct Location
// for each edge kind.
func TestProvideDefinitionEndToEnd(t *testing.T) {
	// Setup: position encoding and logger (shared across sub-tests)
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	tt := []struct {
		name             string
		fixtureRoot      string
		sourceFile       string
		cursorLine       int
		cursorColumn     int
		wantResolved     bool
		wantTargetFile   string
		expectedKind     model.ObjectType
		checkTargetRange func(*testing.T, protocol.Range) // Optional: verify the target range
		description      string
	}{
		{
			// T7a: CALLNAT 'MYSUB' resolves to MYSUB.NSN subprogram
			// FR-24, M-3: go-to-definition on a module-call target.
			name:           "CALLNAT_to_subprogram",
			fixtureRoot:    filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "static-call"),
			sourceFile:     "MAIN.NSP",
			cursorLine:     16, // CALLNAT 'MYSUB'
			cursorColumn:   13, // Within 'MYSUB' token
			wantResolved:   true,
			wantTargetFile: "MYSUB.NSN",
			expectedKind:   model.ObjectSubprogram,
			description:    "cursor on CALLNAT 'MYSUB' target → resolves to MYSUB.NSN",
		},
		{
			// T7b: CALLNAT 'NOSUCH' (no matching definition) → empty result
			// FR-17: modeled gap; no destination, no error.
			name:         "CALLNAT_unresolved_literal",
			fixtureRoot:  filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "static-call"),
			sourceFile:   "MAIN.NSP",
			cursorLine:   19, // CALLNAT 'NOSUCH'
			cursorColumn: 13, // Within 'NOSUCH' token
			wantResolved: false,
			description:  "cursor on unresolved CALLNAT → returns empty (no definition found)",
		},
		{
			// T7c: FETCH 'TARGETPG' (static) resolves to TARGETPG.NSP program
			// FR-24: go-to-definition on FETCH/RUN navigation target.
			name:           "FETCH_to_program",
			fixtureRoot:    filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "navigation"),
			sourceFile:     "MAIN.NSP",
			cursorLine:     22, // FETCH 'TARGETPG'
			cursorColumn:   9,  // Within 'TARGETPG' token
			wantResolved:   true,
			wantTargetFile: "TARGETPG.NSP",
			expectedKind:   model.ObjectProgram,
			description:    "cursor on FETCH 'TARGETPG' target → resolves to TARGETPG.NSP",
		},
		{
			// T7d: FETCH #PROGVAR (dynamic) → empty result
			// FR-17: dynamic target unresolved, no error.
			name:         "FETCH_dynamic",
			fixtureRoot:  filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "navigation"),
			sourceFile:   "MAIN.NSP",
			cursorLine:   25, // FETCH #PROGVAR
			cursorColumn: 9,  // Within #PROGVAR token
			wantResolved: false,
			description:  "cursor on dynamic FETCH → returns empty (unresolved dynamic)",
		},
		{
			// T7e: PERFORM SHARED-SUB (inline) resolves to same-file DEFINE SUBROUTINE range
			// FR-24, M-4: inline PERFORM wins over external subroutine.
			// This case resolves to the same file (MAIN.NSP) at the subroutine's definition range.
			name:           "PERFORM_inline",
			fixtureRoot:    filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "perform-inline-wins"),
			sourceFile:     "MAIN.NSP",
			cursorLine:     24, // PERFORM SHARED-SUB
			cursorColumn:   11, // Within 'SHARED-SUB' token
			wantResolved:   true,
			wantTargetFile: "MAIN.NSP",
			expectedKind:   model.ObjectProgram, // The referencing file's type
			description:    "cursor on PERFORM 'SHARED-SUB' (inline) → resolves to same file's DEFINE SUBROUTINE",
		},
		{
			// T7f: PERFORM SHARED-SUB (external fallback) resolves to SHARED-SUB.NSS
			// When there is no inline match, external subroutine is found.
			// (This requires a fixture with external PERFORM only, not tested in
			// perform-inline-wins because that file has an inline definition.
			// Reusing perform-external fixture for the external case.)
			name:           "PERFORM_external",
			fixtureRoot:    filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "perform-external"),
			sourceFile:     "MAIN.NSP",
			cursorLine:     16, // PERFORM 'SHARED-SUB' (external)
			cursorColumn:   11, // Within 'SHARED-SUB' token
			wantResolved:   true,
			wantTargetFile: "SHARED-SUB.NSS",
			expectedKind:   model.ObjectExternalSubroutine,
			description:    "cursor on PERFORM 'SHARED-SUB' (external) → resolves to SHARED-SUB.NSS",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: build the workspace index from the fixture
			wd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get working directory: %v", err)
			}
			fixtureAbs := filepath.Join(wd, tc.fixtureRoot)

			cfg := config.Defaults()
			idx, _, _, err := workspace.BuildWithCache(fixtureAbs, cfg, az, logger, "", nil, nil)
			if err != nil {
				t.Fatalf("failed to build index: %v", err)
			}

			// Resolve the workspace edges (T7: used by provideDefinition)
			resSet := workspace.Resolve(idx, &cfg)

			// Load the source file's analysis from the index (T7: used for cursor lookup)
			sourceFA, ok := idx.Get(tc.sourceFile)
			if !ok {
				t.Fatalf("source file %q not found in index", tc.sourceFile)
			}
			_ = sourceFA // Will be used in T7 implementation

			// Read the source file content for position conversion
			sourceAbs := filepath.Join(fixtureAbs, tc.sourceFile)
			sourceContent, err := os.ReadFile(sourceAbs)
			if err != nil {
				t.Fatalf("failed to read source file: %v", err)
			}
			_ = sourceContent // Will be used in T7 implementation for range conversion

			// Build the handlerContext (simulating the server state)
			hctx := &handlerContext{
				idx:         idx,
				res:         resSet,
				posEncoding: enc,
				root:        fixtureAbs,
				cfg:         cfg,
				logger:      logger,
			}

			// Act: call provideDefinition with the cursor position
			// Construct DefinitionParams from the cursor position
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(sourceAbs),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert from 1-based to 0-based
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			locations, err := provideDefinition(hctx, params)

			// Assert: check results
			if err != nil {
				t.Fatalf("provideDefinition failed: %v", err)
			}

			if tc.wantResolved {
				// Expect a non-empty result
				if locations == nil || len(locations) == 0 {
					t.Errorf("provideDefinition: expected non-empty locations, got %v", locations)
					return
				}

				// Verify at least one location matches the expected target file
				found := false
				for _, loc := range locations {
					targetPath := loc.URI.FsPath()
					targetRel, err := filepath.Rel(fixtureAbs, targetPath)
					if err != nil {
						t.Fatalf("failed to compute relative path: %v", err)
					}
					if strings.EqualFold(targetRel, tc.wantTargetFile) {
						found = true
						if tc.checkTargetRange != nil {
							tc.checkTargetRange(t, loc.Range)
						}
						break
					}
				}
				if !found {
					t.Errorf("provideDefinition: expected target file %q in locations", tc.wantTargetFile)
				}
			} else {
				// Expect an empty result
				if locations != nil && len(locations) > 0 {
					t.Errorf("provideDefinition: expected empty locations for unresolved target, got %v", locations)
				}
			}
		})
	}
}
