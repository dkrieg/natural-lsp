package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/document"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestProvideTypeDefinition_ViewField tests that typeDefinition on a VIEW OF field
// navigates to the DDM field in the .NSD file via the steplib chain (feature 31, T3).
// It asserts the exact DDM field NameRange coordinates match the expected values.
// FR-58, FR-17 (modeled gaps), FR-43 (graceful degradation).
func TestProvideTypeDefinition_ViewField(t *testing.T) {
	// Setup: get working directory and build index from viewdef fixtures
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	fixtureRoot := filepath.Join(wd, "testdata", "viewdef")

	// Initialize workspace config and analyzer
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build the index with cache
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the index to enable DDM field location lookups
	res := workspace.Resolve(idx, &cfg)

	// Create a handler context with the index and resolution set
	hctx := &handlerContext{
		root:        fixtureRoot,
		cfg:         cfg,
		idx:         idx,
		res:         res,
		az:          az,
		store:       document.New(fixtureRoot, nil, logger), // Store with nil analyze (tests use disk)
		posEncoding: protocol.PositionEncodingKindUTF8,
		logger:      logger,
		idxResMu:    sync.RWMutex{},
	}

	tt := []struct {
		name              string
		sourceFile        string
		sourceLine        uint32 // 0-based in protocol
		sourceCharacter   uint32 // 0-based in protocol
		wantDDMFile       string // filename in testdata/viewdef
		wantLocationLine  uint32
		wantLocationStart uint32
		wantLocationEnd   uint32
		description       string
	}{
		{
			// VIEW field CUSTOMER-ID (empview line 6, bare field)
			// Should resolve to customer.NSD line 6, field name span (CUSTOMER-ID)
			name:              "VIEW_field_CUSTOMER_ID",
			sourceFile:        "empview.NSP",
			sourceLine:        5, // line 6 (1-based) → 5 (0-based)
			sourceCharacter:   8, // middle of "CUSTOMER-ID" at columns 5-16 (1-based) → 4-15 (0-based)
			wantDDMFile:       "customer.NSD",
			wantLocationLine:  5,  // line 6 (1-based) → 5 (0-based)
			wantLocationStart: 7,  // column 8 (1-based) → 7 (0-based)
			wantLocationEnd:   18, // end-exclusive at byte 18
			description:       "CUSTOMER-ID bare view field → DDM field NameRange",
		},
		{
			// VIEW field BALANCE (empview line 7, bare field)
			// Should resolve to customer.NSD line 12, field name span (BALANCE)
			name:              "VIEW_field_BALANCE",
			sourceFile:        "empview.NSP",
			sourceLine:        6, // line 7 (1-based) → 6 (0-based)
			sourceCharacter:   7, // middle of "BALANCE" at columns 5-11 (1-based) → 4-10 (0-based)
			wantDDMFile:       "customer.NSD",
			wantLocationLine:  11, // line 12 (1-based) → 11 (0-based)
			wantLocationStart: 7,  // column 8 (1-based) → 7 (0-based)
			wantLocationEnd:   14, // end-exclusive at byte 14
			description:       "BALANCE bare view field → DDM field NameRange",
		},
		{
			// RESTATED view field CUSTOMER-NAME (A50) at empview line 8 (0-based 7).
			// Approved decision OQ-3: a view field with an explicit scalar format still
			// type-defines to the DDM field. Should resolve to customer.NSD line 7
			// (0-based 6), field name span (CUSTOMER-NAME, 13 bytes at Name@7 → 7..20).
			name:              "VIEW_field_CUSTOMER_NAME_restated",
			sourceFile:        "empview.NSP",
			sourceLine:        7,  // line 8 (1-based) → 7 (0-based)
			sourceCharacter:   10, // middle of "CUSTOMER-NAME" at 0-based bytes 6-18
			wantDDMFile:       "customer.NSD",
			wantLocationLine:  6,  // line 7 (1-based) → 6 (0-based)
			wantLocationStart: 7,  // Name@7 (0-based)
			wantLocationEnd:   20, // end-exclusive: 7 + len("CUSTOMER-NAME")=13 = 20
			description:       "CUSTOMER-NAME restated view field with explicit A50 → DDM field NameRange (OQ-3)",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Build the source file URI
			sourceURI := uri.File(filepath.Join(fixtureRoot, tc.sourceFile))

			// Call provideTypeDefinition
			params := protocol.TypeDefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: sourceURI},
					Position: protocol.Position{
						Line:      tc.sourceLine,
						Character: tc.sourceCharacter,
					},
				},
			}

			locations, err := provideTypeDefinition(hctx, params)

			// Assert: no error
			if err != nil {
				t.Errorf("provideTypeDefinition returned error: %v", err)
			}

			// Assert: result is not nil and has exactly one location
			if locations == nil {
				t.Errorf("provideTypeDefinition returned nil; want one Location")
				return
			}
			if len(locations) != 1 {
				t.Errorf("provideTypeDefinition returned %d Location(s); want 1", len(locations))
				return
			}

			// Assert: URI is the DDM file
			expectedDDMURI := uri.File(filepath.Join(fixtureRoot, tc.wantDDMFile))
			if locations[0].URI != expectedDDMURI {
				t.Errorf("Location.URI = %q; want %q", locations[0].URI, expectedDDMURI)
			}

			// Assert: range matches the expected DDM field NameRange
			if locations[0].Range.Start.Line != tc.wantLocationLine {
				t.Errorf("Location.Range.Start.Line = %d; want %d", locations[0].Range.Start.Line, tc.wantLocationLine)
			}
			if locations[0].Range.Start.Character != tc.wantLocationStart {
				t.Errorf("Location.Range.Start.Character = %d; want %d", locations[0].Range.Start.Character, tc.wantLocationStart)
			}
			if locations[0].Range.End.Line != tc.wantLocationLine {
				t.Errorf("Location.Range.End.Line = %d; want %d", locations[0].Range.End.Line, tc.wantLocationLine)
			}
			if locations[0].Range.End.Character != tc.wantLocationEnd {
				t.Errorf("Location.Range.End.Character = %d; want %d", locations[0].Range.End.Character, tc.wantLocationEnd)
			}
		})
	}
}

// TestProvideTypeDefinition_Gaps tests that typeDefinition returns nil (→ wire null)
// for all FR-17 modeled gaps: scalar-only fields, view fields absent from DDM,
// DDM outside the chain, TYPE: SQL DDMs, and no-target cursors (feature 31, T4).
// Each case asserts a nil result and a nil error. provideTypeDefinition is
// structurally read-only and never touches the diagnostic channel, so keeping
// modeled gaps off diagnostics (FR-17) is guaranteed by construction, not asserted here.
// FR-58, FR-17 (modeled gaps), FR-43 (graceful degradation / never-panic).
func TestProvideTypeDefinition_Gaps(t *testing.T) {
	// Setup: get working directory
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	tt := []struct {
		name            string
		fixture         string // subdirectory under testdata
		sourceFile      string
		sourceLine      uint32
		sourceCharacter uint32
		description     string
	}{
		{
			// Scalar-only variable: #SCALAR-VAR (A20) at VARTEST line 9 (0-based 8)
			// No owning view, scalar format only → typeDefinition returns nil
			name:            "scalar_var_declaration",
			fixture:         "variablenav",
			sourceFile:      "VARTEST.NSP",
			sourceLine:      8, // line 9 (1-based) → 8 (0-based)
			sourceCharacter: 8, // middle of #SCALAR-VAR
			description:     "scalar-only variable declaration → nil",
		},
		{
			// Scalar-only variable use: WRITE #SCALAR-VAR at line 22 (0-based 21)
			// Still no owning view, still scalar only → typeDefinition returns nil
			name:            "scalar_var_use",
			fixture:         "variablenav",
			sourceFile:      "VARTEST.NSP",
			sourceLine:      21, // line 22 (1-based) → 21 (0-based)
			sourceCharacter: 8,  // middle of #SCALAR-VAR
			description:     "scalar-only variable use → nil",
		},
		{
			// View field absent from DDM: NOT-A-FIELD at empview line 12 (0-based 11)
			// View resolves (EMP-VIEW VIEW OF CUSTOMER), but NOT-A-FIELD is not in customer.NSD → nil
			name:            "view_field_absent_from_ddm",
			fixture:         "viewdef",
			sourceFile:      "empview.NSP",
			sourceLine:      11, // line 12 (1-based) → 11 (0-based)
			sourceCharacter: 7,  // middle of NOT-A-FIELD
			description:     "view field absent from DDM → nil",
		},
		{
			// DDM outside chain: NONEXISTENT-DDM in view-missing-ddm line 5
			// MISSING-VIEW VIEW OF NONEXISTENT-DDM, but NONEXISTENT-DDM is not in the steplib chain → nil
			name:            "ddm_outside_chain",
			fixture:         "viewdef",
			sourceFile:      "view-missing-ddm.NSP",
			sourceLine:      5, // line 6 (1-based) → 5 (0-based)
			sourceCharacter: 6, // middle of SOME-FIELD
			description:     "DDM outside steplib chain → nil",
		},
		{
			// TYPE: SQL DDM: TABLE-COL in view-sql-ddm at line 6 (0-based 5)
			// S-VIEW VIEW OF SQLTABLE, but SQLTABLE is TYPE: SQL (not parsed) → nil
			name:            "sql_ddm",
			fixture:         "viewdef",
			sourceFile:      "view-sql-ddm.NSP",
			sourceLine:      5, // line 6 (1-based) → 5 (0-based)
			sourceCharacter: 7, // middle of TABLE-COL
			description:     "TYPE: SQL DDM (no parsed fields) → nil",
		},
		{
			// No target at cursor: blank line (line 1 in view-sql-ddm, 0-based 0)
			name:            "blank_line",
			fixture:         "viewdef",
			sourceFile:      "view-sql-ddm.NSP",
			sourceLine:      0, // blank line
			sourceCharacter: 0,
			description:     "blank line → nil",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			fixtureRoot := filepath.Join(wd, "testdata", tc.fixture)

			// Initialize workspace config and analyzer
			cfg := config.Defaults()
			az := natural.New(nil)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			// Build the index
			idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
			if err != nil {
				t.Fatalf("failed to build index: %v", err)
			}

			// Resolve the index
			res := workspace.Resolve(idx, &cfg)

			// Create a handler context
			hctx := &handlerContext{
				root:        fixtureRoot,
				cfg:         cfg,
				idx:         idx,
				res:         res,
				az:          az,
				store:       document.New(fixtureRoot, nil, logger),
				posEncoding: protocol.PositionEncodingKindUTF8,
				logger:      logger,
				idxResMu:    sync.RWMutex{},
			}

			// Build the source file URI
			sourceURI := uri.File(filepath.Join(fixtureRoot, tc.sourceFile))

			// Call provideTypeDefinition
			params := protocol.TypeDefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: sourceURI},
					Position: protocol.Position{
						Line:      tc.sourceLine,
						Character: tc.sourceCharacter,
					},
				},
			}

			locations, err := provideTypeDefinition(hctx, params)

			// Assert: no error
			if err != nil {
				t.Errorf("provideTypeDefinition returned error: %v; want nil", err)
			}

			// Assert: result is nil (→ wire null, not [] or an error)
			if locations != nil {
				t.Errorf("provideTypeDefinition returned %d Location(s); want nil", len(locations))
			}
		})
	}
}

// TestProvideTypeDefinition_StoreFirst tests that typeDefinition reads the live buffer
// via the document store (type_definition.go store-first branch) rather than disk:
// a VIEW OF field present only in an unsaved buffer still type-defines to its DDM field.
// Feature 31, T3 (store-first path). FR-58, FR-43.
func TestProvideTypeDefinition_StoreFirst(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "viewdef")

	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build the index so customer.NSD is resolvable via the steplib chain.
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}
	res := workspace.Resolve(idx, &cfg)

	// Open a buffer that exists only in the store (no such file on disk) holding a
	// VIEW OF CUSTOMER with a bare CUSTOMER-ID field on line 4.
	store := NewTestStore(fixtureRoot, az)
	bufferContent := []byte("IDENTIFY PROGRAM EMPBUF.\n" +
		"DEFINE DATA LOCAL\n" +
		"  1 EMP-VIEW VIEW OF CUSTOMER\n" +
		"    2 CUSTOMER-ID\n" +
		"END-DEFINE.\n" +
		"WRITE 'Done'.\n" +
		"STOP.\n")
	bufURI := uri.File(filepath.Join(fixtureRoot, "empview-buffer.NSP"))
	store.Open(bufURI, 1, bufferContent)

	hctx := &handlerContext{
		root:        fixtureRoot,
		cfg:         cfg,
		idx:         idx,
		res:         res,
		az:          az,
		store:       store,
		posEncoding: protocol.PositionEncodingKindUTF8,
		logger:      logger,
		idxResMu:    sync.RWMutex{},
	}

	// Cursor on CUSTOMER-ID in the buffer (line 4 → 0-based 3, mid-token).
	params := protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: bufURI},
			Position:     protocol.Position{Line: 3, Character: 10},
		},
	}

	locations, err := provideTypeDefinition(hctx, params)
	if err != nil {
		t.Fatalf("provideTypeDefinition returned error: %v", err)
	}
	if len(locations) != 1 {
		t.Fatalf("store-first: got %d Location(s); want 1", len(locations))
	}

	// It must resolve through the buffer to customer.NSD's CUSTOMER-ID NameRange
	// (line 6 → 0-based 5, Name@7, 11 bytes → 7..18 end-exclusive).
	wantURI := uri.File(filepath.Join(fixtureRoot, "customer.NSD"))
	if locations[0].URI != wantURI {
		t.Errorf("store-first: Location.URI = %q; want %q", locations[0].URI, wantURI)
	}
	wantRange := protocol.Range{
		Start: protocol.Position{Line: 5, Character: 7},
		End:   protocol.Position{Line: 5, Character: 18},
	}
	if locations[0].Range != wantRange {
		t.Errorf("store-first: Location.Range = %v; want %v", locations[0].Range, wantRange)
	}
}
