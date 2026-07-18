package server

import (
	"context"
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
			idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureAbs, cfg, az, logger, "", nil, nil)
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

// TestProvideDefinition_AmbiguousReturnsAllCandidates tests that go-to-definition on
// an ambiguous name in a flat namespace (no library map) returns ALL candidate Locations
// (one per candidate file), not an empty result or an arbitrary single pick (feature 10, T9).
//
// This test uses the ambiguous-flat fixture: MAIN.NSP calls CALLNAT 'DUP' where DUP
// has two definitions (LIBA/DUP.NSN and LIBB/DUP.NSN) in a flat namespace. The resolver
// produces Resolution.IsAmbiguous() == true with Candidates = [LIBA/DUP.NSN, LIBB/DUP.NSN]
// (sorted). provideDefinition must return two Locations, each pointing at a distinct
// candidate file. It also asserts that an ambiguity diagnostic exists on the resolution set
// for the referencing file (the diagnostic is surfaced by a separate diagnostics path,
// but must be present to satisfy the feature plan's "diagnostic is present" clause).
//
// FR-24, M-5 (go-to-definition returns all candidates on ambiguity).
func TestProvideDefinition_AmbiguousReturnsAllCandidates(t *testing.T) {
	// Setup: position encoding and logger
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: build the workspace index from the ambiguous-flat fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "..", "..", "workspace", "testdata", "resolution", "ambiguous-flat")

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve all edges in the workspace (required by provideDefinition)
	resSet := workspace.Resolve(idx, &cfg)

	// Read the source file (MAIN.NSP)
	sourceFile := filepath.Join(fixtureRoot, "MAIN.NSP")
	_, err = os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("failed to read source file: %v", err)
	}

	// Get the source file's analysis from the index
	sourceFA, ok := idx.Get("MAIN.NSP")
	if !ok {
		t.Fatalf("MAIN.NSP not found in index")
	}

	// Build handlerContext (simulating the server state)
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Act: call provideDefinition with the cursor positioned on the ambiguous CALLNAT 'DUP'
	// Line 14 is "CALLNAT 'DUP'" (1-based); cursor at column 9 (the opening quote of 'DUP')
	// Note: The parser's EndPos for CALLNAT statements is set to the START column of
	// the last token (the operand), not its END column, so column 9 is the rightmost
	// position within the statement's source range.
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(sourceFile),
			},
			Position: protocol.Position{
				Line:      uint32(14 - 1), // Convert 1-based to 0-based
				Character: uint32(9 - 1),  // Column 9 (1-based) → char 8 (0-based), the opening quote
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: provideDefinition returns multiple Locations (one per candidate)
	// The fixture has two candidates: LIBA/DUP.NSN and LIBB/DUP.NSN
	if locations == nil || len(locations) != 2 {
		t.Errorf("provideDefinition: expected 2 locations (one per candidate), got %d", len(locations))
		if locations != nil {
			for i, loc := range locations {
				t.Logf("  location[%d]: %s", i, loc.URI)
			}
		}
	}

	// Assert: each location points to a distinct candidate file
	// Normalize and collect the relative paths from the returned URIs
	candidatePaths := make(map[string]bool)
	expectedCandidates := map[string]bool{
		"LIBA/DUP.NSN": true,
		"LIBB/DUP.NSN": true,
	}
	if locations != nil && len(locations) >= 2 {
		for i, loc := range locations {
			absPath := loc.URI.FsPath()
			relPath, err := filepath.Rel(fixtureRoot, absPath)
			if err != nil {
				t.Fatalf("failed to compute relative path for location[%d]: %v", i, err)
			}
			// Normalize to forward slashes for comparison
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			candidatePaths[relPath] = true
			t.Logf("location[%d]: %s", i, relPath)
		}

		// Verify both expected candidates are present
		for expected := range expectedCandidates {
			if !candidatePaths[expected] {
				t.Errorf("provideDefinition: expected candidate %q not in locations", expected)
			}
		}
	}

	// Assert: verify the edge is indeed ambiguous in the resolution set
	if len(sourceFA.Edges) > 0 {
		// Find the CALLNAT 'DUP' edge (should be EdgeCalls kind)
		for _, edge := range sourceFA.Edges {
			if edge.Kind == model.EdgeCalls && strings.EqualFold(edge.TargetName, "DUP") {
				res, ok := resSet.Get("MAIN.NSP", edge.Source)
				if !ok {
					t.Errorf("provideDefinition test: edge resolution not found in set")
					return
				}
				if !res.IsAmbiguous() {
					t.Errorf("provideDefinition test: expected IsAmbiguous() == true, got false (IsResolved=%v, IsDynamic=%v)", res.IsResolved(), res.IsDynamic())
				}
				// Verify candidates match expectations
				if len(res.Candidates) != 2 {
					t.Errorf("provideDefinition test: expected 2 candidates, got %d: %v", len(res.Candidates), res.Candidates)
				}
				break
			}
		}
	}

	// Assert: ambiguity diagnostic exists on the resolution set for MAIN.NSP
	diags := resSet.DiagnosticsFor("MAIN.NSP")
	if diags == nil || len(diags) == 0 {
		t.Errorf("provideDefinition test: expected ambiguity diagnostic for MAIN.NSP, got none")
	} else {
		// Verify the diagnostic mentions the ambiguity
		found := false
		for _, diag := range diags {
			if strings.Contains(strings.ToLower(diag.Message), "ambiguous") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("provideDefinition test: expected 'ambiguous' in diagnostic message, got %q", diags[0].Message)
		}
	}
}

// TestProvideDefinition_UnresolvedReturnsEmpty tests that go-to-definition on
// dynamic and unresolved-literal targets returns an empty result with no error
// (FR-17, OQ-4, FR-43). This is a characterization test for T8: both reason kinds
// (ReasonDynamic and ReasonNoTarget) must yield empty results, not errors or
// panics. The behavior is already implemented by provideDefinition (which checks
// resolution.IsResolved()); this test adds explicit regression coverage.
func TestProvideDefinition_UnresolvedReturnsEmpty(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Build the workspace index from the unresolved fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "navigation")

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve all edges in the workspace (required by provideDefinition)
	resSet := workspace.Resolve(idx, &cfg)

	// Read the fixture file
	fixtureFile := filepath.Join(fixtureRoot, "unresolved.NSP")
	sourceContent, err := os.ReadFile(fixtureFile)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	_ = sourceContent // Will be used if needed for position conversion

	// Get the FileAnalysis from the index
	sourceFA, ok := idx.Get("unresolved.NSP")
	if !ok {
		t.Fatalf("unresolved.NSP not found in index")
	}

	// Build handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Sub-test 1: CALLNAT #SUB-NAME (dynamic variable, ReasonDynamic)
	t.Run("dynamic_target", func(t *testing.T) {
		// Cursor position at the dynamic target: line 13, column 11 (inside #SUB-NAME)
		params := protocol.DefinitionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: uri.File(fixtureFile),
				},
				Position: protocol.Position{
					Line:      uint32(13 - 1), // 1-based to 0-based conversion
					Character: uint32(11 - 1),
				},
			},
		}

		locations, err := provideDefinition(hctx, params)

		// Assert: no error
		if err != nil {
			t.Fatalf("provideDefinition failed: %v", err)
		}

		// Assert: empty result (nil or len 0)
		if locations != nil && len(locations) > 0 {
			t.Errorf("expected empty result for dynamic target, got %v", locations)
		}

		// Verify that the edge is indeed dynamic
		if len(sourceFA.Edges) > 0 {
			// Find the dynamic edge (first CALLNAT)
			for _, edge := range sourceFA.Edges {
				if edge.Kind == model.EdgeCallsDynamic {
					res, ok := resSet.Get("unresolved.NSP", edge.Source)
					if !ok {
						t.Errorf("edge resolution not found in set")
						return
					}
					if !res.IsUnresolved() {
						t.Errorf("expected unresolved, got resolved")
						return
					}
					if !res.IsDynamic() {
						t.Errorf("expected IsDynamic() to be true")
						return
					}
					// Found and verified the dynamic edge
					break
				}
			}
		}
	})

	// Sub-test 2: CALLNAT 'MISSING' (literal not found, ReasonNoTarget)
	t.Run("literal_unresolved", func(t *testing.T) {
		// Cursor position at the literal target: line 16, column 11 (inside 'MISSING')
		params := protocol.DefinitionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: uri.File(fixtureFile),
				},
				Position: protocol.Position{
					Line:      uint32(16 - 1), // 1-based to 0-based conversion
					Character: uint32(11 - 1),
				},
			},
		}

		locations, err := provideDefinition(hctx, params)

		// Assert: no error
		if err != nil {
			t.Fatalf("provideDefinition failed: %v", err)
		}

		// Assert: empty result (nil or len 0)
		if locations != nil && len(locations) > 0 {
			t.Errorf("expected empty result for unresolved literal, got %v", locations)
		}

		// Verify that the edge is indeed unresolved (not dynamic)
		if len(sourceFA.Edges) > 1 {
			// Find the unresolved literal edge (second CALLNAT)
			callCount := 0
			for _, edge := range sourceFA.Edges {
				if edge.Kind == model.EdgeCalls {
					callCount++
					if callCount == 2 { // Second EdgeCalls should be 'MISSING'
						res, ok := resSet.Get("unresolved.NSP", edge.Source)
						if !ok {
							t.Errorf("edge resolution not found in set")
							return
						}
						if !res.IsUnresolved() {
							t.Errorf("expected unresolved, got resolved")
							return
						}
						if res.IsDynamic() {
							t.Errorf("expected IsDynamic() to be false (literal, not variable)")
							return
						}
						// Found and verified the unresolved literal edge
						break
					}
				}
			}
		}
	})
}

// TestProvideDefinition_MarshaledEmptyCase (T4) pins the wire bytes for an empty
// definition result by driving the REAL dispatch path end-to-end (initialize →
// initialized → textDocument/definition) against an empty workspace. The provider
// returns nil there, and the dispatch's nil-guard must emit "null" as the result.
//
// This is not a tautology: if someone drops the nil-guard in server.go's definition
// case (letting the json/v2 marshaler run on the nil slice, which yields "[]") or flips
// the sentinel to "[]", the emitted bytes change and this test goes red (Story 2 AC2).
func TestProvideDefinition_MarshaledEmptyCase(t *testing.T) {
	got := dispatchResultBytes(t, "textDocument/definition",
		`{"textDocument":{"uri":"file:///nonexistent/NOPE.NSP"},"position":{"line":0,"character":0}}`)

	if string(got) != "null" {
		t.Errorf("empty definition result: got %q, want %q", string(got), "null")
	}
}

// TestProvideDefinition_MarshaledNonEmptyCase (T4) pins the exact wire bytes for a
// non-empty definition result via marshalResult — the EXACT function the definition
// dispatch calls in its non-nil branch. Pinning the full bytes (not a substring)
// locks byte-for-byte preservation across the stdlib→gojson migration (Story 2 AC2).
func TestProvideDefinition_MarshaledNonEmptyCase(t *testing.T) {
	// Setup: one location result
	locations := []protocol.Location{
		{
			URI:   "file:///test/target.NSN",
			Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 5}},
		},
	}

	// Marshal via the dispatch's exact marshaler.
	got, err := marshalResult(locations)
	if err != nil {
		t.Fatalf("failed to marshal via marshalResult: %v", err)
	}

	want := `[{"uri":"file:///test/target.NSN","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}]`
	if string(got) != want {
		t.Errorf("non-empty definition wire bytes mismatch:\n got: %s\nwant: %s", string(got), want)
	}
}
