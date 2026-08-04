package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestProvideDeclaration_CallMirror tests that textDocument/declaration returns the same locations
// as textDocument/definition for resolved call and subroutine references.
// Feature 31, T1 (declaration provider, call/transfer/subroutine mirror).
// FR-58.
func TestProvideDeclaration_CallMirror(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the navigation fixture (caller.NSP + helper.NSN)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "navigation")

	// Build the workspace index from the fixture
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext with the index
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	callerFile := filepath.Join(fixtureRoot, "caller.NSP")

	tt := []struct {
		name         string
		cursorLine   int
		cursorColumn int
		description  string
	}{
		{
			// Line 10: CALLNAT 'HELPER'
			// Cursor on 'HELPER' literal
			name:         "callnat_resolved",
			cursorLine:   10,
			cursorColumn: 11, // Within 'HELPER'
			description:  "cursor on CALLNAT 'HELPER' target → declaration and definition should match",
		},
		{
			// Line 11: PERFORM INLINE-SUB
			// Cursor on INLINE-SUB (inline subroutine)
			name:         "perform_inline",
			cursorLine:   11,
			cursorColumn: 17, // Within INLINE-SUB
			description:  "cursor on PERFORM INLINE-SUB target → declaration and definition should match",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare the cursor position (convert 1-based to 0-based)
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(callerFile),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			// Act: call provideDefinition to get the expected result
			definitionLocs, err := provideDefinition(hctx, params)
			if err != nil {
				t.Fatalf("provideDefinition failed: %v", err)
			}

			// Act: call provideDeclaration and compare
			declarationParams := protocol.DeclarationParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(callerFile),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			declarationLocs, err := provideDeclaration(hctx, declarationParams)
			if err != nil {
				t.Fatalf("provideDeclaration failed: %v", err)
			}

			// Assert: both should return the same locations
			if len(definitionLocs) != len(declarationLocs) {
				t.Errorf("%s: declaration returned %d locations, definition returned %d",
					tc.description, len(declarationLocs), len(definitionLocs))
				return
			}

			// Assert: each location matches exactly
			for i, defLoc := range definitionLocs {
				declLoc := declarationLocs[i]
				if defLoc.URI != declLoc.URI {
					t.Errorf("%s [loc %d]: URI mismatch: definition=%q, declaration=%q",
						tc.description, i, defLoc.URI, declLoc.URI)
				}
				if defLoc.Range != declLoc.Range {
					t.Errorf("%s [loc %d]: Range mismatch: definition=%v, declaration=%v",
						tc.description, i, defLoc.Range, declLoc.Range)
				}
			}

			// Assert: at least one location was returned (resolved targets)
			if len(definitionLocs) == 0 {
				t.Errorf("%s: expected at least one location, got none", tc.description)
			}
		})
	}
}

// TestProvideDeclaration_VariableAndGaps tests that textDocument/declaration resolves variable use-sites
// to their DEFINE DATA declarations, and that modeled gaps (system vars, unresolved, undeclared)
// return nil (no error, no diagnostic).
// Feature 31, T2 (declaration on variable uses + modeled gaps).
// FR-58, FR-17, FR-43.
func TestProvideDeclaration_VariableAndGaps(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the variable navigation fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index from the fixture
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext with the index
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	fixtureFile := filepath.Join(fixtureRoot, "VARTEST.NSP")

	tt := []struct {
		name         string
		cursorLine   int
		cursorColumn int
		wantNonEmpty bool // true if we expect a non-empty result (declaration found)
		description  string
	}{
		{
			// Line 22: WRITE #SCALAR-VAR
			// Cursor on #SCALAR-VAR use → should resolve to declaration at line 9
			name:         "variable_use_scalar",
			cursorLine:   22,
			cursorColumn: 12,
			wantNonEmpty: true,
			description:  "cursor on #SCALAR-VAR use → resolves to its DEFINE DATA declaration",
		},
		{
			// Line 9: 1 #SCALAR-VAR (A20)
			// Cursor on the declaration itself → idempotent, resolves to itself
			name:         "variable_declaration_idempotent",
			cursorLine:   9,
			cursorColumn: 12,
			wantNonEmpty: true,
			description:  "cursor on #SCALAR-VAR declaration → idempotent (resolves to itself)",
		},
		{
			// Line 28: MOVE #GROUP.#SUB-FIELD TO #ARRAY-FIELD (#INDEX)
			// Cursor on #SUB-FIELD (group-qualified) → should resolve to line 12 declaration
			name:         "variable_group_qualified",
			cursorLine:   28,
			cursorColumn: 22,
			wantNonEmpty: true,
			description:  "cursor on #GROUP.#SUB-FIELD (qualified) → resolves to the sub-field declaration",
		},
		{
			// Line 32: MOVE *DATE TO #SYS-VAR
			// Cursor on *DATE (system variable) → should return nil (modeled gap FR-17)
			name:         "gap_system_var",
			cursorLine:   32,
			cursorColumn: 12,
			wantNonEmpty: false,
			description:  "cursor on *DATE (system var) → returns nil (modeled gap)",
		},
		{
			// Line 24: MOVE #AMOUNT TO #OTHER-VAR
			// Cursor on #OTHER-VAR (undeclared) → should return nil (modeled gap FR-17)
			name:         "gap_undeclared_variable",
			cursorLine:   24,
			cursorColumn: 24,
			wantNonEmpty: false,
			description:  "cursor on #OTHER-VAR (undeclared) → returns nil (modeled gap)",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Act: call provideDeclaration with the cursor position
			params := protocol.DeclarationParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(fixtureFile),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			locations, err := provideDeclaration(hctx, params)

			// Assert: no error
			if err != nil {
				t.Fatalf("%s: provideDeclaration failed: %v", tc.description, err)
			}

			// Assert: expectation on empty vs. non-empty result
			isEmpty := locations == nil || len(locations) == 0

			if tc.wantNonEmpty && isEmpty {
				t.Errorf("%s: expected non-empty locations, got nil/empty", tc.description)
				return
			}

			if !tc.wantNonEmpty && !isEmpty {
				t.Errorf("%s: expected nil/empty result (modeled gap), got %d locations", tc.description, len(locations))
				return
			}

			// For non-empty results, pin the delegation exactly: declaration must
			// return the identical Location(s) as definition for the same cursor,
			// so we assert on the exact DEFINE DATA NameRange, not merely the file.
			if tc.wantNonEmpty && len(locations) > 0 {
				definitionParams := protocol.DefinitionParams{
					TextDocumentPositionParams: params.TextDocumentPositionParams,
				}
				definitionLocs, defErr := provideDefinition(hctx, definitionParams)
				if defErr != nil {
					t.Fatalf("%s: provideDefinition failed: %v", tc.description, defErr)
				}
				if len(definitionLocs) != len(locations) {
					t.Errorf("%s: declaration returned %d locations, definition returned %d",
						tc.description, len(locations), len(definitionLocs))
					return
				}
				for i, declLoc := range locations {
					defLoc := definitionLocs[i]
					if declLoc.URI != defLoc.URI {
						t.Errorf("%s [loc %d]: URI mismatch: declaration=%q, definition=%q",
							tc.description, i, declLoc.URI, defLoc.URI)
					}
					if declLoc.Range != defLoc.Range {
						t.Errorf("%s [loc %d]: Range mismatch: declaration=%v, definition=%v",
							tc.description, i, declLoc.Range, defLoc.Range)
					}
				}
			}
		})
	}
}

// TestProvideDeclaration_VariableAndGaps_Unresolved tests that declaration returns nil
// for an unresolved call target (dynamic variable).
// Feature 31, T2 (modeled gap: dynamic/unresolved references).
// FR-58, FR-17, FR-43.
func TestProvideDeclaration_VariableAndGaps_Unresolved(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the navigation fixture with unresolved cases
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "navigation")

	// Build the workspace index from the fixture
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext with the index
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	unresolvedFile := filepath.Join(fixtureRoot, "unresolved.NSP")

	// Act: cursor on a dynamic variable target (CALLNAT #SUB-NAME)
	// Line 14: CALLNAT #SUB-NAME
	params := protocol.DeclarationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(unresolvedFile),
			},
			Position: protocol.Position{
				Line:      uint32(14 - 1), // Line 14
				Character: uint32(12 - 1), // Column 12 (cursor on #SUB-NAME)
			},
		},
	}

	locations, err := provideDeclaration(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDeclaration failed: %v", err)
	}

	// Assert: expect nil/empty result for dynamic/unresolved target
	if locations != nil && len(locations) > 0 {
		t.Errorf("expected nil/empty result for dynamic call target, got %d locations", len(locations))
	}
}

// TestProvideDeclaration_VariableAndGaps_StoreFirst tests that declaration uses store-first buffer reads,
// so a variable added only in an unsaved buffer is resolved correctly.
// Feature 31, T2 (store-first path for variable declarations).
// FR-58, FR-43.
func TestProvideDeclaration_VariableAndGaps_StoreFirst(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: create a document store with a custom program that adds a variable
	// in the buffer (not on disk)
	tempDir := t.TempDir()
	store := NewTestStore(tempDir, az)

	// Create a custom buffer content with a new variable definition and use
	bufferContent := []byte(`PROGRAM TEST-BUFFER
DEFINE DATA
  LOCAL
    1 #NEW-VAR (A10)
  END
END-DEFINE
MOVE #NEW-VAR TO #NEW-VAR
END
`)

	// Open the document in the store (this triggers analysis).
	// Root the URI under tempDir so uriToRelPath succeeds on all platforms — a bare
	// relative filename can't be made relative to the workspace root on Windows, which
	// would make provideDefinition bail before ever consulting the store.
	testURI := uri.File(filepath.Join(tempDir, "test-buffer.NSP"))
	store.Open(testURI, 1, bufferContent)

	// Verify the store has the analysis
	doc, ok := store.Get(testURI)
	if !ok {
		t.Fatal("expected document to be opened in store")
	}
	if doc.Analysis.Definitions == nil || len(doc.Analysis.Definitions) == 0 {
		t.Fatal("expected Definitions to be populated after Open")
	}

	// Build the handlerContext with the store (no index, since the document is not on disk)
	hctx := &handlerContext{
		store:       store,
		posEncoding: enc,
		root:        tempDir,
		cfg:         config.Defaults(),
		logger:      logger,
		az:          az,
	}

	// Act: cursor on the variable use (line 7: MOVE #NEW-VAR TO #NEW-VAR)
	// This tests that declaration reads from the store, not from disk (which has no such file)
	params := protocol.DeclarationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: testURI,
			},
			Position: protocol.Position{
				Line:      uint32(7 - 1), // Line 7
				Character: uint32(8 - 1), // Column 8 (mid-token of first #NEW-VAR)
			},
		},
	}

	locations, err := provideDeclaration(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDeclaration failed: %v", err)
	}

	// Assert: expect a non-empty result (the store-only declaration)
	if locations == nil || len(locations) == 0 {
		t.Errorf("store-first: expected to resolve #NEW-VAR from the open buffer, got nil/empty")
		return
	}

	// Assert: the location should point to the store's file
	loc := locations[0]
	if loc.URI != testURI {
		t.Errorf("store-first: expected URI %q, got %q", testURI, loc.URI)
	}
}
