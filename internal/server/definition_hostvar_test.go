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

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestProvideDefinitionHostVar_NativeSQL tests go-to-definition on host-variable
// references in native SQL clauses (feature 27, T8).
// A host variable like :HOST-FIELD or bare #HOST-FIELD in INTO/WHERE
// should navigate to the DEFINE DATA declaration's NameRange.
//
// FR-54, FR-24 (refines go-to-definition), FR-17/FR-43 (modeled gaps, graceful degradation).
func TestProvideDefinitionHostVar_NativeSQL(t *testing.T) {
	fixtureRoot, err := filepath.Abs(filepath.Join("testdata", "variablenav"))
	if err != nil {
		t.Fatalf("failed to resolve fixture root: %v", err)
	}
	sourceFile := "SQLHOST.NSP"

	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Load the fixture workspace configuration
	_, cfg, err := config.Bootstrap(fixtureRoot, "", logger)
	if err != nil {
		t.Fatalf("failed to load config from %s: %v", fixtureRoot, err)
	}

	// Build the workspace index
	idx, err := workspace.Build(context.Background(), fixtureRoot, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build workspace index: %v", err)
	}

	// Resolve edges and DDM accesses
	res := workspace.Resolve(idx, &cfg)

	// Read the source file content for position conversion
	sourceAbsPath := filepath.Join(fixtureRoot, sourceFile)
	sourceContent, err := os.ReadFile(sourceAbsPath)
	if err != nil {
		t.Fatalf("failed to read source file %s: %v", sourceAbsPath, err)
	}

	// Get the source file's analysis
	// In a flat namespace fixture, files at the root are indexed by filename
	sourceFA, ok := idx.Get(sourceFile)
	if !ok {
		t.Fatalf("source file %s not in index", sourceFile)
	}

	// Create a handlerContext for the test
	hctx := &handlerContext{
		root:        fixtureRoot,
		cfg:         cfg,
		idx:         idx,
		res:         res,
		posEncoding: enc,
		az:          az,
	}

	tt := []struct {
		name             string
		cursorLine       int // 1-based model line
		cursorColumn     int // 1-based model column
		expectedDeclName string
		wantResolved     bool
		description      string
	}{
		{
			// T8a: Cursor on HOST-NAME in SELECT INTO clause
			// Line 19: "  INTO HOST-NAME, HOST-SALARY"
			// HOST-NAME is at columns 8-17 (1-based)
			// Expected: resolves to HOST-NAME declaration
			name:             "native_sql_INTO_bare_form",
			cursorLine:       19, // INTO clause with HOST-NAME
			cursorColumn:     12, // Within HOST-NAME token
			expectedDeclName: "HOST-NAME",
			wantResolved:     true,
			description:      "cursor on HOST-NAME in INTO → resolves to HOST-NAME declaration",
		},
		{
			// T8b: Cursor on PERS-ID in WHERE clause (bare form, no colon)
			// Line 21: "  WHERE EMP_ID = PERS-ID"
			// PERS-ID is at columns 20-27 (1-based)
			// Expected: resolves to PERS-ID declaration
			name:             "native_sql_WHERE_bare_form",
			cursorLine:       21, // WHERE EMP_ID = PERS-ID
			cursorColumn:     23, // Within PERS-ID token
			expectedDeclName: "PERS-ID",
			wantResolved:     true,
			description:      "cursor on PERS-ID in WHERE → resolves to PERS-ID declaration",
		},
		{
			// T8c: Cursor on :START-DATE (colon-prefixed) in WHERE clause
			// Line 22: "    AND HIRE_DATE >= :START-DATE"
			// :START-DATE is at columns 23-34 (1-based, colon included)
			// Host var ref is extracted as START-DATE (colon stripped)
			// Expected: resolves to START-DATE declaration
			name:             "native_sql_WHERE_colon_prefixed",
			cursorLine:       22,           // AND HIRE_DATE >= :START-DATE
			cursorColumn:     26,           // Within :START-DATE token
			expectedDeclName: "START-DATE", // Colon stripped by parser
			wantResolved:     true,
			description:      "cursor on :START-DATE in WHERE → resolves to START-DATE declaration",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Convert cursor position to model.Position (1-based)
			cursorPos := model.Position{Line: tc.cursorLine, Column: tc.cursorColumn}

			// Act: call findCursorTarget to locate the host var ref
			_, _, varRef := findCursorTarget(sourceFA, cursorPos, string(sourceContent), az)

			// Assert: varRef should be found
			if !tc.wantResolved {
				if varRef != nil {
					t.Errorf("expected no reference found, got varRef with name %q", varRef.Name)
				}
				return
			}

			if varRef == nil {
				t.Fatalf("expected to find a host var ref at {%d,%d}, got nil", tc.cursorLine, tc.cursorColumn)
			}

			// The host var name should match (case-insensitive, sigil preserved)
			if !strings.EqualFold(varRef.Name, tc.expectedDeclName) {
				t.Errorf("host var name = %q, want %q", varRef.Name, tc.expectedDeclName)
			}

			// Now test the full definition resolution via provideDefinition
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(sourceAbsPath),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert to 0-based for protocol
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			locations, err := provideDefinition(hctx, params)
			if err != nil {
				t.Errorf("provideDefinition failed: %v", err)
				return
			}

			if len(locations) == 0 {
				t.Errorf("expected at least one location, got empty")
				return
			}

			// Verify the first location points to the source file itself
			if locations[0].URI != params.TextDocument.URI {
				t.Errorf("definition URI = %q, want %q", locations[0].URI, params.TextDocument.URI)
			}

			// Verify the range is within the DEFINE DATA section (should be the declaration)
			// The declaration should be in lines 5-10 (approximately)
			if locations[0].Range.Start.Line < 4 || locations[0].Range.Start.Line > 10 {
				t.Errorf("definition range start line = %d, expected within DEFINE DATA (approx 5-10)",
					locations[0].Range.Start.Line+1)
			}
		})
	}
}

// TestProvideDefinitionHostVar_ProcessSQL tests go-to-definition on host-variable
// references in PROCESS SQL opaque bodies (feature 27, T8).
// A host variable like :#PERS-ID inside the <<...>> body should navigate to the
// DEFINE DATA declaration's NameRange.
//
// FR-54, FR-24 (refines go-to-definition), FR-17/FR-43 (modeled gaps, graceful degradation).
func TestProvideDefinitionHostVar_ProcessSQL(t *testing.T) {
	fixtureRoot, err := filepath.Abs(filepath.Join("testdata", "variablenav"))
	if err != nil {
		t.Fatalf("failed to resolve fixture root: %v", err)
	}
	sourceFile := "SQLHOST.NSP"

	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Load the fixture workspace configuration
	_, cfg, err := config.Bootstrap(fixtureRoot, "", logger)
	if err != nil {
		t.Fatalf("failed to load config from %s: %v", fixtureRoot, err)
	}

	// Build the workspace index
	idx, err := workspace.Build(context.Background(), fixtureRoot, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build workspace index: %v", err)
	}

	// Resolve edges and DDM accesses
	res := workspace.Resolve(idx, &cfg)

	// Read the source file content for position conversion
	sourceAbsPath := filepath.Join(fixtureRoot, sourceFile)
	sourceContent, err := os.ReadFile(sourceAbsPath)
	if err != nil {
		t.Fatalf("failed to read source file %s: %v", sourceAbsPath, err)
	}

	// Get the source file's analysis
	// In a flat namespace fixture, files at the root are indexed by filename
	sourceFA, ok := idx.Get(sourceFile)
	if !ok {
		t.Fatalf("source file %s not in index", sourceFile)
	}

	// Create a handlerContext for the test
	hctx := &handlerContext{
		root:        fixtureRoot,
		cfg:         cfg,
		idx:         idx,
		res:         res,
		posEncoding: enc,
		az:          az,
	}

	tt := []struct {
		name             string
		cursorLine       int // 1-based model line
		cursorColumn     int // 1-based model column
		expectedDeclName string
		wantResolved     bool
		description      string
	}{
		{
			// T8d: Cursor on :PERS-ID in PROCESS SQL opaque body
			// Line 29: "     WHERE EMP_ID = :PERS-ID"
			// :PERS-ID is at columns 22-30 (1-based, colon included)
			// Host var ref is extracted as PERS-ID (colon stripped)
			// Expected: resolves to PERS-ID declaration
			name:             "process_sql_opaque_hostvar",
			cursorLine:       29, // WHERE EMP_ID = :PERS-ID
			cursorColumn:     25, // Within :PERS-ID token
			expectedDeclName: "PERS-ID",
			wantResolved:     true,
			description:      "cursor on :PERS-ID in PROCESS SQL body → resolves to PERS-ID declaration",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Convert cursor position to model.Position (1-based)
			cursorPos := model.Position{Line: tc.cursorLine, Column: tc.cursorColumn}

			// Act: call findCursorTarget to locate the host var ref
			_, _, varRef := findCursorTarget(sourceFA, cursorPos, string(sourceContent), az)

			// Assert: varRef should be found
			if !tc.wantResolved {
				if varRef != nil {
					t.Errorf("expected no reference found, got varRef with name %q", varRef.Name)
				}
				return
			}

			if varRef == nil {
				t.Fatalf("expected to find a host var ref at {%d,%d}, got nil", tc.cursorLine, tc.cursorColumn)
			}

			// The host var name should match (case-insensitive, sigil preserved)
			if !strings.EqualFold(varRef.Name, tc.expectedDeclName) {
				t.Errorf("host var name = %q, want %q", varRef.Name, tc.expectedDeclName)
			}

			// Now test the full definition resolution via provideDefinition
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(sourceAbsPath),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert to 0-based for protocol
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			locations, err := provideDefinition(hctx, params)
			if err != nil {
				t.Errorf("provideDefinition failed: %v", err)
				return
			}

			if len(locations) == 0 {
				t.Errorf("expected at least one location, got empty")
				return
			}

			// Verify the first location points to the source file itself
			if locations[0].URI != params.TextDocument.URI {
				t.Errorf("definition URI = %q, want %q", locations[0].URI, params.TextDocument.URI)
			}

			// Verify the range is within the DEFINE DATA section (should be the declaration)
			// The declaration should be in lines 5-10 (approximately)
			if locations[0].Range.Start.Line < 4 || locations[0].Range.Start.Line > 10 {
				t.Errorf("definition range start line = %d, expected within DEFINE DATA (approx 5-10)",
					locations[0].Range.Start.Line+1)
			}
		})
	}
}
