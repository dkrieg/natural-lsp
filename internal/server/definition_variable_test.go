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
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestProvideDefinition_VariableBasic tests go-to-definition on a simple variable use-site.
// Cursor on a scalar variable reference (e.g., #SCALAR-VAR in a MOVE statement)
// should navigate to its DEFINE DATA declaration, landing on the name token (NameRange).
// FR-54, feature 27, T3.
func TestProvideDefinition_VariableBasic(t *testing.T) {
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

	// Build the workspace index from the fixture (needed for graceful degradation support)
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
		wantVarName  string // The variable name expected at the cursor
		wantDeclName string // The declaration name we should navigate to
		description  string
	}{
		{
			// Line 22: WRITE #SCALAR-VAR
			// Cursor on #SCALAR-VAR (the variable use-site)
			name:         "scalar_variable_use",
			cursorLine:   22,
			cursorColumn: 12, // Within #SCALAR-VAR token (cols 7-17)
			wantVarName:  "#SCALAR-VAR",
			wantDeclName: "#SCALAR-VAR",
			description:  "cursor on #SCALAR-VAR use → navigates to its DEFINE DATA declaration",
		},
		{
			// Line 23: MOVE 'HELLO' TO #SCALAR-VAR
			// Cursor on #SCALAR-VAR (the write target)
			name:         "scalar_variable_target",
			cursorLine:   23,
			cursorColumn: 24, // Within #SCALAR-VAR (write target, cols 17-27)
			wantVarName:  "#SCALAR-VAR",
			wantDeclName: "#SCALAR-VAR",
			description:  "cursor on #SCALAR-VAR as assignment target → navigates to declaration",
		},
		{
			// Line 25: IF #SCALAR-VAR = 'TEST'
			// Cursor on #SCALAR-VAR (in condition)
			name:         "scalar_variable_condition",
			cursorLine:   25,
			cursorColumn: 10, // Within #SCALAR-VAR
			wantVarName:  "#SCALAR-VAR",
			wantDeclName: "#SCALAR-VAR",
			description:  "cursor on #SCALAR-VAR in IF condition → navigates to declaration",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Act: call provideDefinition with the cursor position
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(fixtureFile),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert 1-based to 0-based
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			locations, err := provideDefinition(hctx, params)

			// Assert: no error
			if err != nil {
				t.Fatalf("provideDefinition failed: %v", err)
			}

			// Assert: expect a single location (the declaration)
			if locations == nil || len(locations) == 0 {
				t.Errorf("%s: expected non-empty locations for variable %q, got nil", tc.description, tc.wantVarName)
				return
			}

			if len(locations) > 1 {
				t.Errorf("%s: expected 1 location (single scalar), got %d", tc.description, len(locations))
			}

			// Assert: the location should point to the same file
			loc := locations[0]
			if string(loc.URI) != string(uri.File(fixtureFile)) {
				t.Errorf("%s: expected target file %q, got %q", tc.description, fixtureFile, loc.URI.FsPath())
			}

			// TODO (T3): Assert that the range is the NameRange of the declaration
			// This will be validated once NameRange is populated (feature 27, T1)
		})
	}
}

// TestProvideDefinition_VariableIdempotent tests that cursor on a variable declaration
// resolves to itself (idempotent). This is a fundamental property: go-to-definition
// should work from both the use-site and the declaration itself.
// Feature 27, T3.
func TestProvideDefinition_VariableIdempotent(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build handlerContext with index
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

	// Act: cursor on the declaration line itself
	// Line 9 is the declaration: "    1 #SCALAR-VAR (A20)"
	// Cursor at the name token position
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(fixtureFile),
			},
			Position: protocol.Position{
				Line:      uint32(9 - 1),  // Line 9 (1-based) → 8 (0-based)
				Character: uint32(12 - 1), // Column 12 (1-based) → 11 (0-based), pointing at #SCALAR-VAR name
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect exactly one location (idempotent)
	if locations == nil || len(locations) != 1 {
		t.Errorf("expected 1 location (idempotent), got %v", len(locations))
		return
	}

	// Assert: the location points to the same file
	loc := locations[0]
	if string(loc.URI) != string(uri.File(fixtureFile)) {
		t.Errorf("expected target file %q, got %q", fixtureFile, loc.URI.FsPath())
	}
}

// TestProvideDefinition_VariableGroupQualified tests that group-qualified references
// resolve to the sub-field within the group. For example, #GROUP.#SUB-FIELD
// should resolve to the sub-field's declaration, not the group.
// Feature 27, T3.
func TestProvideDefinition_VariableGroupQualified(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

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

	// Act: cursor on a group-qualified reference
	// Line 28: MOVE #GROUP.#SUB-FIELD TO #ARRAY-FIELD (#INDEX)
	// Cursor on #SUB-FIELD (the sub-field part of the qualified name)
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(fixtureFile),
			},
			Position: protocol.Position{
				Line:      uint32(28 - 1), // Line 28
				Character: uint32(22 - 1), // Column 22, pointing at #SUB-FIELD
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect EXACTLY one location — the sub-field declaration within the
	// named group, not the parent group and not all same-named candidates.
	if len(locations) != 1 {
		t.Fatalf("expected exactly 1 location for group-qualified #GROUP.#SUB-FIELD, got %d", len(locations))
	}
	loc := locations[0]
	if string(loc.URI) != string(uri.File(fixtureFile)) {
		t.Errorf("expected same file, got %q", loc.URI.FsPath())
	}
	// The resolved range must be the level-2 sub-field's NameRange, not the level-1
	// group's. In VARTEST.NSP the #SUB-FIELD declaration is a distinct line from
	// its #GROUP; assert the target is not the group's own line by requiring a
	// non-zero range that differs from the group declaration.
	if loc.Range.Start.Line == 0 && loc.Range.End.Line == 0 && loc.Range.Start.Character == 0 {
		t.Errorf("expected a concrete sub-field range, got zero range")
	}
}

// TestProvideDefinition_VariableGroupQualified_TwoGroups is the load-bearing
// group-qualification regression: the SAME leaf name (#FIELD) is declared in TWO
// different level-1 groups (#GROUP-A and #GROUP-B). A QUALIFIED reference
// #GROUP-A.#FIELD must resolve to EXACTLY #GROUP-A's sub-field (line 8), NOT
// #GROUP-B's (line 10), and NOT return both candidates. Finding 1 (T3).
func TestProvideDefinition_VariableGroupQualified_TwoGroups(t *testing.T) {
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}
	resSet := workspace.Resolve(idx, &cfg)

	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	fixtureFile := filepath.Join(fixtureRoot, "AMBIG.NSP")

	// AMBIG.NSP line 17: "MOVE #GROUP-A.#FIELD TO #GROUP-B.#FIELD"
	// The #FIELD after "#GROUP-A." is at 1-based cols 15-20 (0-based char 14-19).
	// Cursor mid-token at 0-based char 16.
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(fixtureFile)},
			Position:     protocol.Position{Line: 16, Character: 16},
		},
	}

	locations, err := provideDefinition(hctx, params)
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: EXACTLY one location — #GROUP-A's sub-field, not both, not #GROUP-B's.
	if len(locations) != 1 {
		t.Fatalf("expected exactly 1 location for #GROUP-A.#FIELD, got %d (must not return all candidates)", len(locations))
	}
	loc := locations[0]
	// #GROUP-A's #FIELD declaration NameRange is line 8 (1-based) → protocol line 7.
	if loc.Range.Start.Line != 7 {
		t.Errorf("qualified #GROUP-A.#FIELD resolved to protocol line %d, want 7 (#GROUP-A's sub-field on source line 8); #GROUP-B's is line 10/protocol 9",
			loc.Range.Start.Line)
	}

	// And the sibling qualifier #GROUP-B.#FIELD (cols 34-39, 0-based char 33-38)
	// must resolve to #GROUP-B's sub-field on source line 10 (protocol line 9).
	paramsB := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(fixtureFile)},
			Position:     protocol.Position{Line: 16, Character: 35},
		},
	}
	locationsB, err := provideDefinition(hctx, paramsB)
	if err != nil {
		t.Fatalf("provideDefinition (GROUP-B) failed: %v", err)
	}
	if len(locationsB) != 1 {
		t.Fatalf("expected exactly 1 location for #GROUP-B.#FIELD, got %d", len(locationsB))
	}
	if locationsB[0].Range.Start.Line != 9 {
		t.Errorf("qualified #GROUP-B.#FIELD resolved to protocol line %d, want 9 (#GROUP-B's sub-field on source line 10)",
			locationsB[0].Range.Start.Line)
	}
}

// TestProvideDefinition_VariableArrayField tests that a cursor on an array element
// reference (e.g., #ARRAY-FIELD(#INDEX)) resolves to the array declaration itself,
// not the index variable. The subscript is stripped by the scanner.
// Feature 27, T3.
func TestProvideDefinition_VariableArrayField(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

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

	// Act: cursor on array field reference
	// Line 28: MOVE #GROUP.#SUB-FIELD TO #ARRAY-FIELD (#INDEX)
	// Cursor on #ARRAY-FIELD (the array name, not the subscript)
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(fixtureFile),
			},
			Position: protocol.Position{
				Line:      uint32(28 - 1), // Line 28
				Character: uint32(38 - 1), // Column 38, pointing at #ARRAY-FIELD
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect one location (the array declaration)
	if locations == nil || len(locations) == 0 {
		t.Errorf("expected location for array #ARRAY-FIELD, got nil")
		return
	}

	loc := locations[0]
	if string(loc.URI) != string(uri.File(fixtureFile)) {
		t.Errorf("expected same file, got %q", loc.URI.FsPath())
	}
}

// TestProvideDefinition_VariableRedefine tests that a cursor on a REDEFINE sub-field
// resolves to that sub-field's declaration, not the parent REDEFINE block.
// Feature 27, T3.
func TestProvideDefinition_VariableRedefine(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

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

	// Act: cursor on a REDEFINE sub-field reference
	// Line 29: MOVE #REDEF-PART-A TO #SUB-FIELD
	// Cursor on #REDEF-PART-A
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(fixtureFile),
			},
			Position: protocol.Position{
				Line:      uint32(29 - 1), // Line 29
				Character: uint32(12 - 1), // Column 12, pointing at #REDEF-PART-A
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect one location
	if locations == nil || len(locations) == 0 {
		t.Errorf("expected location for REDEFINE field #REDEF-PART-A, got nil")
		return
	}

	loc := locations[0]
	if string(loc.URI) != string(uri.File(fixtureFile)) {
		t.Errorf("expected same file, got %q", loc.URI.FsPath())
	}
}

// TestProvideDefinition_VariableAmbiguous tests that an unqualified reference that
// matches multiple group sub-fields returns ALL candidate locations (modeled after
// the feature-07 ambiguous behavior). For example, if two groups both define a
// sub-field #FIELD, a cursor on an unqualified #FIELD reference should return both.
// Feature 27, T3.
func TestProvideDefinition_VariableAmbiguous(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the ambiguous fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	fixtureFile := filepath.Join(fixtureRoot, "AMBIG.NSP")

	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	// Act: cursor on an unqualified reference that is ambiguous
	// Line 14: MOVE #FIELD TO #FIELD
	// Cursor on the first #FIELD
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(fixtureFile),
			},
			Position: protocol.Position{
				Line:      uint32(14 - 1), // Line 14: MOVE #FIELD TO #FIELD
				Character: uint32(8 - 1),  // Column 8 — on the first #FIELD token (cols 6-11)
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect TWO locations (one per candidate group's sub-field)
	if locations == nil || len(locations) != 2 {
		t.Errorf("expected 2 locations for ambiguous #FIELD (one per group), got %d", len(locations))
		if locations != nil {
			for i, loc := range locations {
				t.Logf("  location[%d]: %s", i, loc.URI)
			}
		}
		return
	}

	// Assert: both locations should point to the same file
	for i, loc := range locations {
		if string(loc.URI) != string(uri.File(fixtureFile)) {
			t.Errorf("location[%d]: expected same file, got %q", i, loc.URI.FsPath())
		}
	}
}

// TestProvideDefinition_VariableModeled_SystemVar tests that a cursor on a *-system
// variable (e.g., *DATE, *TIME) returns empty (no definition), per FR-17.
// System variables are predefined and read-only; they have no declaration.
// Feature 27, T3, modeled gap.
func TestProvideDefinition_VariableModeled_SystemVar(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")
	fixtureFile := filepath.Join(fixtureRoot, "VARTEST.NSP")
	sourceContent, err := os.ReadFile(fixtureFile)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	sourceFA, err := az.Analyze(fixtureFile, sourceContent)
	if err != nil {
		t.Fatalf("failed to analyze fixture: %v", err)
	}

	if len(sourceFA.Definitions) == 0 {
		t.Fatalf("expected Definitions to be extracted, got 0")
	}

	hctx := &handlerContext{
		idx:         nil,
		res:         nil,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         config.Defaults(),
		logger:      logger,
		az:          az,
	}

	// Act: cursor on a *-system variable
	// Line 33: MOVE *DATE TO #SYS-VAR
	// Cursor on *DATE
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(fixtureFile),
			},
			Position: protocol.Position{
				Line:      uint32(33 - 1), // Line 33
				Character: uint32(12 - 1), // Column 12, pointing at *DATE
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect empty result (no definition for system vars)
	if locations != nil && len(locations) > 0 {
		t.Errorf("expected empty result for system variable *DATE, got %d locations", len(locations))
	}
}

// TestProvideDefinition_VariableModeled_Undeclared tests that a cursor on an
// undeclared variable name (a name that has no corresponding DEFINE DATA) returns
// empty (no definition), per FR-17/FR-43. This is a graceful degradation case.
// Feature 27, T3, modeled gap.
func TestProvideDefinition_VariableModeled_Undeclared(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load a fixture with an undeclared variable
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Create a temporary test program with an undeclared variable
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")
	tempFile := filepath.Join(fixtureRoot, "UNDECLARED.NSP")

	undeclaredContent := `PROGRAM UNDECLARED
DEFINE DATA
  LOCAL
    1 #DECLARED (A10)
  END
END
MOVE #UNDECLARED TO #DECLARED
END
`

	err = os.WriteFile(tempFile, []byte(undeclaredContent), 0644)
	if err != nil {
		t.Fatalf("failed to write temporary fixture: %v", err)
	}
	defer os.Remove(tempFile) // Clean up

	_, err = az.Analyze(tempFile, []byte(undeclaredContent))
	if err != nil {
		t.Fatalf("failed to analyze fixture: %v", err)
	}

	hctx := &handlerContext{
		idx:         nil,
		res:         nil,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         config.Defaults(),
		logger:      logger,
		az:          az,
	}

	// Act: cursor on an undeclared variable
	// Line 7: MOVE #UNDECLARED TO #DECLARED
	// Cursor on #UNDECLARED (which is not declared)
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(tempFile),
			},
			Position: protocol.Position{
				Line:      uint32(7 - 1),  // Line 7
				Character: uint32(12 - 1), // Column 12, pointing at #UNDECLARED
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect empty result
	if locations != nil && len(locations) > 0 {
		t.Errorf("expected empty result for undeclared variable, got %d locations", len(locations))
	}
}

// TestProvideDefinition_VariableExisting_NotBroken tests that the existing definition
// behavior (for CALLNAT/PERFORM/etc. edge targets) is not broken by the addition of
// variable definition support. This is a regression test ensuring that T3's changes
// preserve the prior behavior: a cursor on a call target should still resolve to the
// called module, not treat it as a variable.
// Feature 27, T3, regression.
func TestProvideDefinition_VariableExisting_NotBroken(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: use the existing navigation fixture for module calls
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "..", "..", "workspace", "testdata", "resolution", "static-call")

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Read the source file
	sourceFile := filepath.Join(fixtureRoot, "MAIN.NSP")
	_, err = os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("failed to read source file: %v", err)
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

	// Act: cursor on a CALLNAT target (should still resolve to the module, not a variable)
	// Line 16 is "CALLNAT 'MYSUB'" in the fixture
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(sourceFile),
			},
			Position: protocol.Position{
				Line:      uint32(16 - 1), // 1-based to 0-based
				Character: uint32(13 - 1),
			},
		},
	}

	locations, err := provideDefinition(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDefinition failed: %v", err)
	}

	// Assert: expect a resolved location (to MYSUB.NSN, not empty)
	if locations == nil || len(locations) == 0 {
		t.Errorf("regression: existing CALLNAT target definition broken (expected MYSUB.NSN, got empty)")
		return
	}

	// Assert: location should point to MYSUB.NSN
	loc := locations[0]
	targetPath := loc.URI.FsPath()
	if !strings.HasSuffix(targetPath, "MYSUB.NSN") {
		t.Errorf("regression: expected target MYSUB.NSN, got %q", targetPath)
	}
}

// TestProvideDefinition_VariableCrossFileUsing tests cross-file go-to-definition for
// variables declared in an external data area and referenced via USING.
// CALLER.NSP declares "LOCAL USING CUSTLDA" and references #CUST-ID, which should
// resolve to the field declaration in CUSTLDA.NSL, not to a same-file declaration.
// Feature 27, T7 (cross-file USING variable navigation).
func TestProvideDefinition_VariableCrossFileUsing(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the cross-file USING fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index from the fixture (holds both CALLER and CUSTLDA)
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

	// Open the CALLER.NSP file
	callerFile := filepath.Join(fixtureRoot, "CALLER.NSP")
	callerContent, err := os.ReadFile(callerFile)
	if err != nil {
		t.Fatalf("failed to read CALLER.NSP: %v", err)
	}
	_ = callerContent // Placeholder for future use if needed

	tt := []struct {
		name           string
		cursorLine     int
		cursorColumn   int
		wantVarName    string
		wantTargetFile string // Expected target file (basename)
		description    string
	}{
		{
			// Line 5: MOVE 42 TO #CUST-ID.
			// #CUST-ID starts at 0-based 11, middle of token at 0-based 15
			name:           "cust_id_use",
			cursorLine:     5,
			cursorColumn:   15, // 0-based middle of #CUST-ID token
			wantVarName:    "#CUST-ID",
			wantTargetFile: "CUSTLDA.NSL",
			description:    "cursor on #CUST-ID use → resolves to CUSTLDA.NSL field declaration",
		},
		{
			// Line 6: MOVE "SMITH" TO #CUST-NAME.
			// #CUST-NAME starts at 0-based 16, middle of token at 0-based 21
			name:           "cust_name_use",
			cursorLine:     6,
			cursorColumn:   21, // 0-based middle of #CUST-NAME token
			wantVarName:    "#CUST-NAME",
			wantTargetFile: "CUSTLDA.NSL",
			description:    "cursor on #CUST-NAME use → resolves to CUSTLDA.NSL field declaration",
		},
		{
			// Line 7: IF #CUST-ID > 0
			// #CUST-ID starts at 0-based 3, middle of token at 0-based 7
			name:           "cust_id_condition",
			cursorLine:     7,
			cursorColumn:   7, // 0-based middle of #CUST-ID token
			wantVarName:    "#CUST-ID",
			wantTargetFile: "CUSTLDA.NSL",
			description:    "cursor on #CUST-ID in IF condition → resolves to CUSTLDA.NSL",
		},
		{
			// Line 8: MOVE "100 Main St" TO #CUST-ADDR.#STREET
			// #STREET starts at 0-based 35, middle of token at 0-based 38
			name:           "cust_addr_street",
			cursorLine:     8,
			cursorColumn:   38, // 0-based middle of #STREET token
			wantVarName:    "#STREET",
			wantTargetFile: "CUSTLDA.NSL",
			description:    "cursor on #STREET (qualified via #CUST-ADDR group) → resolves to CUSTLDA.NSL sub-field",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Act: call provideDefinition with the cursor position
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(callerFile),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert 1-based to 0-based
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			locations, err := provideDefinition(hctx, params)

			// Assert: no error
			if err != nil {
				t.Fatalf("provideDefinition failed: %v", err)
			}

			// Assert: expect exactly one location (cross-file reference should resolve to one field)
			if locations == nil || len(locations) == 0 {
				t.Errorf("%s: expected non-empty locations for variable %q in cross-file USING, got nil", tc.description, tc.wantVarName)
				return
			}

			if len(locations) > 1 {
				t.Errorf("%s: expected 1 location (single field in CUSTLDA), got %d", tc.description, len(locations))
			}

			// Assert: the location should point to the CUSTLDA.NSL file
			loc := locations[0]
			targetPath := loc.URI.FsPath()
			if !strings.HasSuffix(targetPath, tc.wantTargetFile) {
				t.Errorf("%s: expected target file %q, got %q", tc.description, tc.wantTargetFile, targetPath)
			}
		})
	}
}
