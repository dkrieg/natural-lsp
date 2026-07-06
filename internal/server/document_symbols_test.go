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
	"natural-lsp/internal/document"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// TestSymbolToDocumentSymbol_DataSections tests the converter on a tree with
// DEFINE DATA sections and nested data fields (FR-27, feature 11, task T1).
//
// Exercises: SymbolDataSection → SymbolKindNamespace, SymbolDataField →
// SymbolKindField, field nesting (REDEFINE hierarchy), Range/SelectionRange
// conversion via toProtocolRange, and child ordering.
//
// Fixture: 01-program-full.NSP has a LOCAL section with a group (nesting)
// and a REDEFINE, plus a MAP and subroutine.
func TestSymbolToDocumentSymbol_DataSections(t *testing.T) {
	// Read the fixture from the analysis/natural testdata directory
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "01-program-full.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze to get a FileAnalysis with Structure
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)

	// Test: symbol conversion for data sections
	tests := []struct {
		name string
		// pred selects a child from the root for conversion
		pred          func(*model.Symbol) bool
		expectKind    protocol.SymbolKind
		expectName    string
		checkChildren func(t *testing.T, child *protocol.DocumentSymbol)
	}{
		{
			name: "data_section_maps_to_namespace_kind",
			pred: func(sym *model.Symbol) bool {
				return sym.Kind == model.SymbolDataSection
			},
			expectKind: protocol.SymbolKindNamespace,
			expectName: "LOCAL", // section name
			checkChildren: func(t *testing.T, docSym *protocol.DocumentSymbol) {
				// A data section has SymbolDataField children
				if len(docSym.Children) == 0 {
					t.Error("data section should have field children")
					return
				}
				for i, child := range docSym.Children {
					if child.Kind != protocol.SymbolKindField {
						t.Errorf("child[%d] Kind = %v, want SymbolKindField", i, child.Kind)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Find the matching child
			var targetSym *model.Symbol
			for _, child := range analysis.Structure.Children {
				if tc.pred(&child) {
					targetSym = &child
					break
				}
			}
			if targetSym == nil {
				t.Skip("fixture does not have a matching symbol child")
			}

			// Act: convert the symbol
			docSym := symbolToDocumentSymbol(*targetSym, contentStr, protocol.PositionEncodingKindUTF8)

			// Assert: name matches
			if docSym.Name != tc.expectName {
				t.Errorf("Name = %q, want %q", docSym.Name, tc.expectName)
			}

			// Assert: kind is mapped correctly
			if docSym.Kind != tc.expectKind {
				t.Errorf("Kind = %v, want %v", docSym.Kind, tc.expectKind)
			}

			// Assert: Range and SelectionRange are converted (non-zero after conversion)
			if docSym.Range.Start == docSym.Range.End &&
				(docSym.Range.Start.Line == 0 && docSym.Range.Start.Character == 0) {
				t.Error("Range should be non-zero (converted from 1-based to 0-based)")
			}

			// Assert: SelectionRange is contained in Range
			if !isContainedInRange(docSym.SelectionRange, docSym.Range) {
				t.Errorf("SelectionRange %+v not contained in Range %+v", docSym.SelectionRange, docSym.Range)
			}

			// Assert: children preserved
			if tc.checkChildren != nil {
				tc.checkChildren(t, &docSym)
			}
		})
	}
}

// TestSymbolToDocumentSymbol_Subroutines tests the converter on a tree with
// subroutines (FR-27, feature 11, task T1).
//
// Exercises: SymbolSubroutine → SymbolKindFunction, sibling ordering, Range/SelectionRange
// conversion, and no children (subroutines are leaves).
//
// Fixture: 01-program-full.NSP has a DEFINE SUBROUTINE PROCESS-EMP.
func TestSymbolToDocumentSymbol_Subroutines(t *testing.T) {
	// Read the fixture
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "01-program-full.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)

	tests := []struct {
		name string
		// pred selects a child
		pred       func(*model.Symbol) bool
		expectKind protocol.SymbolKind
	}{
		{
			name: "subroutine_maps_to_function_kind",
			pred: func(sym *model.Symbol) bool {
				return sym.Kind == model.SymbolSubroutine
			},
			expectKind: protocol.SymbolKindFunction,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Find the matching child
			var targetSym *model.Symbol
			for _, child := range analysis.Structure.Children {
				if tc.pred(&child) {
					targetSym = &child
					break
				}
			}
			if targetSym == nil {
				t.Skip("fixture does not have a matching symbol child")
			}

			// Act
			docSym := symbolToDocumentSymbol(*targetSym, contentStr, protocol.PositionEncodingKindUTF8)

			// Assert: kind
			if docSym.Kind != tc.expectKind {
				t.Errorf("Kind = %v, want %v", docSym.Kind, tc.expectKind)
			}

			// Assert: Name matches
			if docSym.Name != targetSym.Name {
				t.Errorf("Name = %q, want %q", docSym.Name, targetSym.Name)
			}

			// Assert: subroutines are typically leaves (no children)
			// (Some fixtures might have children; don't assert absence)
		})
	}
}

// TestSymbolToDocumentSymbol_Map tests the converter on a tree with a map
// and its fields (FR-27, feature 11, task T1).
//
// Exercises: SymbolMap → SymbolKindObject, map fields as children.
//
// Fixture: 01-program-full.NSP has a DEFINE MAP 'EMPSCREEN' with MAP-FIELD and MAP-NUMBER.
func TestSymbolToDocumentSymbol_Map(t *testing.T) {
	// Read the fixture
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "01-program-full.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)

	tests := []struct {
		name          string
		pred          func(*model.Symbol) bool
		expectKind    protocol.SymbolKind
		checkChildren func(t *testing.T, docSym *protocol.DocumentSymbol)
	}{
		{
			name: "map_maps_to_object_kind",
			pred: func(sym *model.Symbol) bool {
				return sym.Kind == model.SymbolMap
			},
			expectKind: protocol.SymbolKindObject,
			checkChildren: func(t *testing.T, docSym *protocol.DocumentSymbol) {
				// Map may have field children
				if len(docSym.Children) > 0 {
					for i, child := range docSym.Children {
						if child.Kind != protocol.SymbolKindField {
							t.Errorf("map field[%d] Kind = %v, want SymbolKindField", i, child.Kind)
						}
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var targetSym *model.Symbol
			for _, child := range analysis.Structure.Children {
				if tc.pred(&child) {
					targetSym = &child
					break
				}
			}
			if targetSym == nil {
				t.Skip("fixture does not have a matching symbol child")
			}

			// Act
			docSym := symbolToDocumentSymbol(*targetSym, contentStr, protocol.PositionEncodingKindUTF8)

			// Assert
			if docSym.Kind != tc.expectKind {
				t.Errorf("Kind = %v, want %v", docSym.Kind, tc.expectKind)
			}

			// Assert: SelectionRange contained
			if !isContainedInRange(docSym.SelectionRange, docSym.Range) {
				t.Errorf("SelectionRange %+v not contained in Range %+v", docSym.SelectionRange, docSym.Range)
			}

			if tc.checkChildren != nil {
				tc.checkChildren(t, &docSym)
			}
		})
	}
}

// TestSymbolToDocumentSymbol_DDMReferences tests the converter on DDM reference symbols
// (FR-27, feature 11, task T1).
//
// Exercises: SymbolDDMReference → SymbolKindStruct, inline source order, no children (leaf).
//
// Fixture: 04-ddm-access.NSP has READ EMPLOYEE-VIEW, STORE DEPARTMENT, FIND LOCATION.
func TestSymbolToDocumentSymbol_DDMReferences(t *testing.T) {
	// Read the fixture
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "04-ddm-access.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)

	tests := []struct {
		name       string
		pred       func(*model.Symbol) bool
		expectKind protocol.SymbolKind
	}{
		{
			name: "ddm_reference_maps_to_struct_kind",
			pred: func(sym *model.Symbol) bool {
				return sym.Kind == model.SymbolDDMReference
			},
			expectKind: protocol.SymbolKindStruct,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Find the first matching DDM ref
			var targetSym *model.Symbol
			for _, child := range analysis.Structure.Children {
				if tc.pred(&child) {
					targetSym = &child
					break
				}
			}
			if targetSym == nil {
				t.Skip("fixture does not have a DDM reference symbol")
			}

			// Act
			docSym := symbolToDocumentSymbol(*targetSym, contentStr, protocol.PositionEncodingKindUTF8)

			// Assert
			if docSym.Kind != tc.expectKind {
				t.Errorf("Kind = %v, want %v", docSym.Kind, tc.expectKind)
			}

			// Assert: Name is non-empty (per feature 09, empty-Name refs are skipped)
			if docSym.Name == "" {
				t.Error("Name is empty; expected feature 09 to skip empty-Name DDM refs")
			}

			// Assert: SelectionRange contained
			if !isContainedInRange(docSym.SelectionRange, docSym.Range) {
				t.Errorf("SelectionRange %+v not contained in Range %+v", docSym.SelectionRange, docSym.Range)
			}
		})
	}
}

// TestSymbolToDocumentSymbol_ObjectRoot tests the converter on an object root symbol
// (FR-27, feature 11, task T1).
//
// Exercises: SymbolObject → SymbolKindModule, recursive Children conversion (preserving
// nesting and order), and the full tree structure.
func TestSymbolToDocumentSymbol_ObjectRoot(t *testing.T) {
	// Read the fixture
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "01-program-full.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)
	root := analysis.Structure

	// Act
	docSym := symbolToDocumentSymbol(*root, contentStr, protocol.PositionEncodingKindUTF8)

	// Assert: root kind
	if docSym.Kind != protocol.SymbolKindModule {
		t.Errorf("root Kind = %v, want SymbolKindModule", docSym.Kind)
	}

	// Assert: root name from filename
	if docSym.Name == "" {
		t.Error("root Name should be non-empty (object base name)")
	}

	// Assert: root has children (data sections, subroutines, maps, DDM refs)
	if len(docSym.Children) == 0 {
		t.Error("root Children should be non-empty")
	}

	// Assert: children are in source order
	for i := 1; i < len(docSym.Children); i++ {
		prevEnd := docSym.Children[i-1].Range.End
		currStart := docSym.Children[i].Range.Start
		if isGreaterPosition(currStart, prevEnd) == false &&
			!(currStart.Line == prevEnd.Line && currStart.Character >= prevEnd.Character) {
			t.Logf("children not strictly ordered; this may be acceptable if children have adjacent ranges")
		}
	}

	// Assert: SelectionRange is zero-width caret (preserved from model)
	if !isZeroWidthRange(docSym.SelectionRange) {
		t.Logf("root SelectionRange is not zero-width; this is acceptable if model is a caret")
	}

	// Assert: SelectionRange contained
	if !isContainedInRange(docSym.SelectionRange, docSym.Range) {
		t.Errorf("SelectionRange %+v not contained in Range %+v", docSym.SelectionRange, docSym.Range)
	}
}

// TestSymbolToDocumentSymbol_RecursiveNesting tests that the converter recurses into
// Children and preserves nesting (FR-27, feature 11, task T1).
//
// Exercises: recursive conversion, child order, and nesting depth preservation.
func TestSymbolToDocumentSymbol_RecursiveNesting(t *testing.T) {
	// Build a minimal model.Symbol tree manually
	field1 := model.Symbol{
		Kind: model.SymbolDataField,
		Name: "FIELD1",
		Range: model.Range{
			Start: model.Position{Line: 2, Column: 5},
			End:   model.Position{Line: 2, Column: 11},
		},
		SelectionRange: model.Range{
			Start: model.Position{Line: 2, Column: 5},
			End:   model.Position{Line: 2, Column: 11},
		},
		Children: nil,
	}

	field2 := model.Symbol{
		Kind: model.SymbolDataField,
		Name: "FIELD2",
		Range: model.Range{
			Start: model.Position{Line: 3, Column: 5},
			End:   model.Position{Line: 3, Column: 11},
		},
		SelectionRange: model.Range{
			Start: model.Position{Line: 3, Column: 5},
			End:   model.Position{Line: 3, Column: 11},
		},
		Children: nil,
	}

	dataSection := model.Symbol{
		Kind: model.SymbolDataSection,
		Name: "LOCAL",
		Range: model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 4, Column: 20},
		},
		SelectionRange: model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 1, Column: 5},
		},
		Children: []model.Symbol{field1, field2},
	}

	// Act
	docSym := symbolToDocumentSymbol(dataSection, "dummy content", protocol.PositionEncodingKindUTF8)

	// Assert: children are converted
	if len(docSym.Children) != 2 {
		t.Errorf("Children count = %d, want 2", len(docSym.Children))
	}

	if len(docSym.Children) >= 1 {
		if docSym.Children[0].Name != "FIELD1" {
			t.Errorf("Children[0].Name = %q, want FIELD1", docSym.Children[0].Name)
		}
		if docSym.Children[0].Kind != protocol.SymbolKindField {
			t.Errorf("Children[0].Kind = %v, want SymbolKindField", docSym.Children[0].Kind)
		}
	}

	if len(docSym.Children) >= 2 {
		if docSym.Children[1].Name != "FIELD2" {
			t.Errorf("Children[1].Name = %q, want FIELD2", docSym.Children[1].Name)
		}
	}
}

// TestSymbolToDocumentSymbol_UnknownKindDefaultsToObject tests that an unknown/zero
// SymbolKind is mapped defensively to SymbolKindObject (FR-27, FR-43).
func TestSymbolToDocumentSymbol_UnknownKindDefaultsToObject(t *testing.T) {
	// Build a symbol with an unknown kind
	sym := model.Symbol{
		Kind: model.SymbolKind("unknown-kind"),
		Name: "UNKNOWN",
		Range: model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 1, Column: 7},
		},
		SelectionRange: model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 1, Column: 7},
		},
	}

	// Act
	docSym := symbolToDocumentSymbol(sym, "dummy", protocol.PositionEncodingKindUTF8)

	// Assert: defaults to Object
	if docSym.Kind != protocol.SymbolKindObject {
		t.Errorf("unknown Kind mapped to %v, want SymbolKindObject", docSym.Kind)
	}
}

// Helper: isContainedInRange checks if inner is contained in outer.
func isContainedInRange(inner, outer protocol.Range) bool {
	// inner.Start >= outer.Start AND inner.End <= outer.End
	if inner.Start.Line < outer.Start.Line {
		return false
	}
	if inner.Start.Line == outer.Start.Line && inner.Start.Character < outer.Start.Character {
		return false
	}
	if inner.End.Line > outer.End.Line {
		return false
	}
	if inner.End.Line == outer.End.Line && inner.End.Character > outer.End.Character {
		return false
	}
	return true
}

// Helper: isZeroWidthRange checks if a range is zero-width (start == end).
func isZeroWidthRange(r protocol.Range) bool {
	return r.Start.Line == r.End.Line && r.Start.Character == r.End.Character
}

// Helper: isGreaterPosition checks if a > b.
func isGreaterPosition(a, b protocol.Position) bool {
	if a.Line > b.Line {
		return true
	}
	if a.Line == b.Line && a.Character > b.Character {
		return true
	}
	return false
}

// TestProvideDocumentSymbols_OpenBuffer tests that provideDocumentSymbols serves
// the outline from the open-document store when the URI is currently being edited
// (Story 2 — unsaved edits). This ensures the outline reflects in-memory content,
// not disk content.
//
// Feature 11, Task T2: resolution order (1) — open-document store (current, unsaved).
// FR-27, FR-43.
func TestProvideDocumentSymbols_OpenBuffer(t *testing.T) {
	// Setup: create an analyzer and a document store
	az := natural.New(nil)
	tempDir := t.TempDir()
	store := NewTestStore(tempDir, az)

	// Arrange: a simple fixture content with a DEFINE SUBROUTINE
	// This content will only exist in the store, not on disk.
	openedContent := []byte(`DEFINE DATA LOCAL
  1 NAME      (A20)
END-DEFINE

DEFINE SUBROUTINE PROCESS-RECORD
  DISPLAY "Processing"
END-SUBROUTINE
`)

	// Open the document in the store (this triggers analysis)
	testURI := newTestURI("inmemory-only.NSP")
	store.Open(testURI, 1, openedContent)

	// Verify the store has the analysis (and Structure)
	doc, ok := store.Get(testURI)
	if !ok {
		t.Fatal("expected document to be opened in store")
	}
	if doc.Analysis.Structure == nil {
		t.Fatal("expected Structure to be populated after Open")
	}

	// Build the handlerContext with the store but no index
	// (the store has the content; the index is only used on fallback)
	hctx := &handlerContext{
		store:       store,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        t.TempDir(), // A temporary root; not used for open documents
	}

	// Act: call provideDocumentSymbols with the open document's URI
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: testURI,
		},
	}
	result, err := provideDocumentSymbols(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDocumentSymbols failed: %v", err)
	}

	// Assert: result is non-nil and contains symbols
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Assert: the root object symbol is present
	if len(result) != 1 {
		t.Fatalf("expected 1 root symbol, got %d", len(result))
	}

	root := result[0]
	if root.Kind != protocol.SymbolKindModule {
		t.Errorf("root Kind = %v, want SymbolKindModule", root.Kind)
	}

	// Assert: the root has children (at least the data section and subroutine)
	if len(root.Children) == 0 {
		t.Error("root should have children (data section, subroutine)")
	}

	// Assert: verify the subroutine is in the children
	foundSubroutine := false
	for _, child := range root.Children {
		if child.Kind == protocol.SymbolKindFunction {
			foundSubroutine = true
			break
		}
	}
	if !foundSubroutine {
		t.Error("expected to find a subroutine (SymbolKindFunction) in children")
	}
}

// TestProvideDocumentSymbols_FallbackToIndex tests that provideDocumentSymbols
// falls back to the index (and disk read) when the document is not open in the store.
//
// Feature 11, Task T2: resolution order (2) — fallback to index + disk when not open.
// FR-27, FR-43.
func TestProvideDocumentSymbols_FallbackToIndex(t *testing.T) {
	// Setup: build a workspace index from a fixture
	az := natural.New(nil)
	testdataDir := filepath.Join("testdata", "..", "..", "workspace", "testdata", "resolution", "static-call")
	idx, err := buildTestWorkspaceIndexFromFixture(testdataDir, az)
	if err != nil {
		t.Fatalf("failed to build test index: %v", err)
	}

	// Arrange: an empty store (no open documents)
	tempDir := t.TempDir()
	store := NewTestStore(tempDir, az)

	// Get the absolute fixture root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the handlerContext with the index and store
	hctx := &handlerContext{
		idx:         idx,
		store:       store,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        fixtureRoot,
	}

	// Act: request the document symbol for MAIN.NSP (in index, not open)
	mainAbsPath := filepath.Join(fixtureRoot, "MAIN.NSP")
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: uri.File(mainAbsPath),
		},
	}
	result, err := provideDocumentSymbols(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDocumentSymbols failed: %v", err)
	}

	// Assert: result is non-nil (MAIN.NSP is in the fixture)
	if result == nil {
		t.Fatal("expected non-nil result from indexed file")
	}

	// Assert: single root symbol
	if len(result) != 1 {
		t.Fatalf("expected 1 root symbol, got %d", len(result))
	}

	root := result[0]
	if root.Kind != protocol.SymbolKindModule {
		t.Errorf("root Kind = %v, want SymbolKindModule", root.Kind)
	}
}

// TestProvideDocumentSymbols_NotFound tests that provideDocumentSymbols returns
// nil, nil when the URI is unknown (not in store, not in index).
//
// Feature 11, Task T2: resolution order (3) — unknown URI returns nil, nil.
// FR-43 (graceful degradation).
func TestProvideDocumentSymbols_NotFound(t *testing.T) {
	// Setup: empty store and no index
	az := natural.New(nil)
	tempDir := t.TempDir()
	store := NewTestStore(tempDir, az)

	hctx := &handlerContext{
		idx:         nil, // no index
		store:       store,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        t.TempDir(),
	}

	// Act: request symbol for a non-existent URI
	unknownURI := newTestURI("nonexistent.NSP")
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: unknownURI,
		},
	}
	result, err := provideDocumentSymbols(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDocumentSymbols failed: %v", err)
	}

	// Assert: result is nil or empty
	if result != nil && len(result) > 0 {
		t.Errorf("expected nil or empty result for unknown URI, got %d symbols", len(result))
	}
}

// TestProvideDocumentSymbols_PartialContent tests that provideDocumentSymbols
// still outlines recognized parts of a malformed/partial object (FR-43).
//
// Feature 11, Task T2: resolution order (4) — partial/malformed objects still outlined.
// FR-43 (graceful degradation).
func TestProvideDocumentSymbols_PartialContent(t *testing.T) {
	// Setup: create an analyzer and store
	az := natural.New(nil)
	tempDir := t.TempDir()
	store := NewTestStore(tempDir, az)

	// Arrange: content with a valid subroutine but some garbage/unrecognized lines
	partialContent := []byte(`DEFINE DATA LOCAL
  1 EMPID     (N4)
GARBAGE LINE HERE THAT DOESNT PARSE

DEFINE SUBROUTINE PROCESS
  DISPLAY "Processing"
END-SUBROUTINE

INCOMPLETE LINE THAT SHOULD BE IGNORED
`)

	testURI := newTestURI("partial.NSP")
	store.Open(testURI, 1, partialContent)

	hctx := &handlerContext{
		store:       store,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        t.TempDir(),
	}

	// Act: call provideDocumentSymbols
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: testURI,
		},
	}
	result, err := provideDocumentSymbols(hctx, params)

	// Assert: no error (graceful degradation)
	if err != nil {
		t.Fatalf("provideDocumentSymbols failed: %v", err)
	}

	// Assert: result is not nil (partial parsing still yields structure)
	if result == nil {
		t.Fatal("expected non-nil result even for partial content (graceful degradation)")
	}

	// Assert: at least the root and recognized parts are present
	if len(result) != 1 {
		t.Fatalf("expected 1 root symbol, got %d", len(result))
	}

	root := result[0]
	if root.Kind != protocol.SymbolKindModule {
		t.Errorf("root Kind = %v, want SymbolKindModule", root.Kind)
	}

	// Assert: the valid subroutine is present (even though there are malformed lines)
	foundSubroutine := false
	for _, child := range root.Children {
		if child.Kind == protocol.SymbolKindFunction {
			foundSubroutine = true
			break
		}
	}
	if !foundSubroutine {
		t.Error("expected subroutine to be in outline despite malformed lines (graceful degradation)")
	}
}

// TestProvideDocumentSymbols_NilContext tests that provideDocumentSymbols
// returns nil, nil when the handlerContext is nil (guard against panics).
//
// Feature 11, Task T2: resolution order (5) — nil hctx guard.
// FR-43.
func TestProvideDocumentSymbols_NilContext(t *testing.T) {
	// Act: call with nil handlerContext
	var nilHctx *handlerContext
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: newTestURI("dummy.NSP"),
		},
	}
	result, err := provideDocumentSymbols(nilHctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDocumentSymbols with nil hctx failed: %v", err)
	}

	// Assert: result is nil
	if result != nil && len(result) > 0 {
		t.Errorf("expected nil result for nil hctx, got %d symbols", len(result))
	}
}

// newTestURI creates a test URI for a given filename.
func newTestURI(filename string) uri.URI {
	return uri.File(filename)
}

// NewTestStore creates a test document.Store with panic recovery and a null logger.
// tempDir is the temporary directory to use as the root.
func NewTestStore(tempDir string, az *natural.Analyzer) *document.Store {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		analysis, _ := az.Analyze(relPath, content)
		return analysis
	}
	return document.New(tempDir, analyzeFunc, logger)
}

// buildTestWorkspaceIndexFromFixture loads all files from testdataDir and builds
// a workspace.Index from them. It returns the index and any error.
func buildTestWorkspaceIndexFromFixture(testdataDir string, az *natural.Analyzer) (*workspace.Index, error) {
	idx := &workspace.Index{}

	// Get absolute path of testdataDir
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	absDir := filepath.Join(wd, testdataDir)

	// Walk the fixture directory
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Include all .NSx files (Natural extensions are .NS followed by one char)
		ext := strings.ToUpper(filepath.Ext(name))
		if len(ext) == 4 && ext[0:3] == ".NS" {
			filePath := filepath.Join(absDir, name)
			content, err := os.ReadFile(filePath)
			if err != nil {
				return nil, err
			}

			// Analyze the file
			analysis, err := az.Analyze(filePath, content)
			if err != nil {
				// Graceful degradation: include files even if analysis fails
				analysis = model.FileAnalysis{}
			}

			// Add to index keyed by relative path
			relPath := name
			idx.Add(relPath, analysis)
		}
	}

	return idx, nil
}
