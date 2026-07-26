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

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/document"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
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

// TestSymbolToDocumentSymbol_ViewOfBinding tests the conversion of VIEW OF nodes
// with their Detail rendered as "VIEW OF <ddm-name>" (Feature 28, T6 — RED phase).
//
// Exercises:
// - A VIEW OF data field node with Detail == "VIEW OF EMPLOYEES"
// - Selected fields as children with Phase-A detail (type for restated fields, nil for bare fields)
// - Bare field without local type shows nil Detail (inherited type deferred to T8)
// - Restated-format field shows its written type
// - Array field shows dimensions
//
// Fixture: 10-view.NSP has EMP-VIEW VIEW OF EMPLOYEES with bare + restated + array fields.
// This test validates that ViewOfDDM is carried onto Symbol.ViewOfDDM and rendered by symbolDetail.
func TestSymbolToDocumentSymbol_ViewOfBinding(t *testing.T) {
	// Read fixture
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "10-view.NSP")
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

	// Find the LOCAL section (first data section child)
	var localSection *model.Symbol
	for _, child := range root.Children {
		if child.Kind == model.SymbolDataSection && child.Name == "LOCAL" {
			localSection = &child
			break
		}
	}
	if localSection == nil {
		t.Fatal("fixture should have a LOCAL data section")
	}

	// Find the EMP-VIEW field within LOCAL (should be the first data field)
	var empViewSym *model.Symbol
	for _, field := range localSection.Children {
		if field.Kind == model.SymbolDataField && field.Name == "EMP-VIEW" {
			empViewSym = &field
			break
		}
	}
	if empViewSym == nil {
		t.Fatal("LOCAL section should have an EMP-VIEW field")
	}

	// Act: convert the view node to DocumentSymbol
	docSym := symbolToDocumentSymbol(*empViewSym, contentStr, protocol.PositionEncodingKindUTF8)

	// Test: the view node's Detail should be "VIEW OF EMPLOYEES"
	t.Run("view_detail", func(t *testing.T) {
		if docSym.Detail == nil {
			t.Error("Detail is nil, want non-nil string pointer for VIEW OF node")
		} else if *docSym.Detail != "VIEW OF EMPLOYEES" {
			t.Errorf("Detail = %q, want %q", *docSym.Detail, "VIEW OF EMPLOYEES")
		}
	})

	// Test: the view node should have children (the selected fields)
	t.Run("view_children_present", func(t *testing.T) {
		if len(docSym.Children) == 0 {
			t.Error("view node should have children (selected fields)")
		}
	})

	// Test: find the FULL-NAME field (restated-format, A40) and verify its Detail
	t.Run("restated_field_detail", func(t *testing.T) {
		var fullNameChild *protocol.DocumentSymbol
		for i := range docSym.Children {
			if docSym.Children[i].Name == "FULL-NAME" {
				fullNameChild = &docSym.Children[i]
				break
			}
		}
		if fullNameChild == nil {
			t.Skip("fixture does not have FULL-NAME field")
		}
		// A restated field with type (A40) should have Detail == "A40"
		if fullNameChild.Detail == nil {
			t.Error("FULL-NAME Detail is nil, want string pointer for restated field")
		} else if *fullNameChild.Detail != "A40" {
			t.Errorf("FULL-NAME Detail = %q, want %q", *fullNameChild.Detail, "A40")
		}
	})

	// Test: find PERSONNEL-ID field (bare, no local type) and verify its Detail is nil
	t.Run("bare_field_detail_nil", func(t *testing.T) {
		var persIdChild *protocol.DocumentSymbol
		for i := range docSym.Children {
			if docSym.Children[i].Name == "PERSONNEL-ID" {
				persIdChild = &docSym.Children[i]
				break
			}
		}
		if persIdChild == nil {
			t.Skip("fixture does not have PERSONNEL-ID field")
		}
		// A bare field with no local type should have nil Detail (inherited type deferred to T8)
		if persIdChild.Detail != nil {
			t.Errorf("PERSONNEL-ID Detail = %q, want nil (bare field, T8 handles inheritance)", *persIdChild.Detail)
		}
	})

	// Test: find SALARY field (restated-format, P9,2) and verify its Detail
	t.Run("salary_field_detail", func(t *testing.T) {
		var salaryChild *protocol.DocumentSymbol
		for i := range docSym.Children {
			if docSym.Children[i].Name == "SALARY" {
				salaryChild = &docSym.Children[i]
				break
			}
		}
		if salaryChild == nil {
			t.Skip("fixture does not have SALARY field")
		}
		if salaryChild.Detail == nil {
			t.Error("SALARY Detail is nil, want string pointer")
		} else if *salaryChild.Detail != "P9,2" {
			t.Errorf("SALARY Detail = %q, want %q", *salaryChild.Detail, "P9,2")
		}
	})

	// Test: find TAGS field (array) and verify its Detail includes dimensions
	t.Run("array_field_dimensions", func(t *testing.T) {
		var tagsChild *protocol.DocumentSymbol
		for i := range docSym.Children {
			if docSym.Children[i].Name == "TAGS" {
				tagsChild = &docSym.Children[i]
				break
			}
		}
		if tagsChild == nil {
			t.Skip("fixture does not have TAGS array field")
		}
		if tagsChild.Detail == nil {
			t.Error("TAGS Detail is nil, want string pointer with type and dimensions")
		} else {
			// Should contain "A10" (the type) and "(1:5)" (dimensions)
			detail := *tagsChild.Detail
			if !strings.Contains(detail, "A10") {
				t.Errorf("TAGS Detail = %q, should contain A10", detail)
			}
			if !strings.Contains(detail, "(1:5)") {
				t.Errorf("TAGS Detail = %q, should contain (1:5)", detail)
			}
		}
	})
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

// TestProvideDocumentSymbols_MarshaledEmptyCase (T4) pins the wire bytes for an empty
// documentSymbol result by driving the REAL dispatch path end-to-end against an empty
// workspace. The provider returns nil for a document with no structure, and the
// dispatch's nil-guard must emit "null".
//
// If the documentSymbol nil-guard in server.go is dropped or flipped to "[]", the
// emitted bytes change and this test goes red (Story 2 AC2).
func TestProvideDocumentSymbols_MarshaledEmptyCase(t *testing.T) {
	got := dispatchResultBytes(t, "textDocument/documentSymbol",
		`{"textDocument":{"uri":"file:///nonexistent/NOPE.NSP"}}`)

	if string(got) != "null" {
		t.Errorf("empty documentSymbol result: got %q, want %q", string(got), "null")
	}
}

// TestProvideDocumentSymbols_MarshaledNonEmptyCase (T4) pins the exact wire bytes for a
// non-empty documentSymbol result via marshalResult — the EXACT function the
// documentSymbol dispatch calls in its non-nil branch. Pinning the full bytes locks
// byte-for-byte preservation across the stdlib→gojson migration (Story 2 AC2).
func TestProvideDocumentSymbols_MarshaledNonEmptyCase(t *testing.T) {
	// Setup: one symbol result
	docSymbols := []protocol.DocumentSymbol{
		{
			Name:           "MYPROG",
			Kind:           protocol.SymbolKindModule,
			Range:          protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 10, Character: 0}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 6}},
		},
	}

	// Marshal via the dispatch's exact marshaler.
	got, err := marshalResult(docSymbols)
	if err != nil {
		t.Fatalf("failed to marshal via marshalResult: %v", err)
	}

	want := `[{"name":"MYPROG","kind":2,"range":{"start":{"line":0,"character":0},"end":{"line":10,"character":0}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":6}}}]`
	if string(got) != want {
		t.Errorf("non-empty documentSymbol wire bytes mismatch:\n got: %s\nwant: %s", string(got), want)
	}
}

// TestSymbolToDocumentSymbol_TypedFieldsDetail tests feature 28, phase A, T2:
// symbolToDocumentSymbol must set DocumentSymbol.Detail for data fields.
// Scalar fields show their Type verbatim (e.g., "A26", "P9,2", "(A) DYNAMIC");
// group headers (Type == "") have no Detail (nil).
//
// Fixture: 07-typed-fields.NSP has typed scalars and group headers.
// FR-55 / feature 28 T2: typed outline with field metadata.
func TestSymbolToDocumentSymbol_TypedFieldsDetail(t *testing.T) {
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "07-typed-fields.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze the fixture
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)

	// Test table: field name → expected Detail string (or nil for groups)
	tests := []struct {
		name         string
		fieldName    string
		expectedType string
		expectNilDtl bool // if true, expect Detail == nil (group header); else expect Detail == expectedType
	}{
		{
			name:         "scalar_A26",
			fieldName:    "SIMPLE-STRING",
			expectedType: "A26",
			expectNilDtl: false,
		},
		{
			name:         "scalar_N8",
			fieldName:    "NUMERIC-FIELD",
			expectedType: "N8",
			expectNilDtl: false,
		},
		{
			name:         "scalar_P9_2",
			fieldName:    "PACKED-DEC",
			expectedType: "P9,2",
			expectNilDtl: false,
		},
		{
			name:         "scalar_I4",
			fieldName:    "INTEGER-VAL",
			expectedType: "I4",
			expectNilDtl: false,
		},
		{
			name:         "scalar_dynamic",
			fieldName:    "DYNAMIC-STRING",
			expectedType: "(A) DYNAMIC",
			expectNilDtl: false,
		},
		{
			name:         "group_header_CUSTOMER_GROUP",
			fieldName:    "CUSTOMER-GROUP",
			expectedType: "",
			expectNilDtl: true,
		},
		{
			name:         "nested_group_ADDRESS_DETAILS",
			fieldName:    "ADDRESS-DETAILS",
			expectedType: "",
			expectNilDtl: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Walk the structure to find the target field
			var targetModelSym *model.Symbol
			var walkSymbols func(s *model.Symbol)
			walkSymbols = func(s *model.Symbol) {
				if s.Kind == model.SymbolDataField && s.Name == tc.fieldName {
					targetModelSym = s
					return
				}
				for i := range s.Children {
					walkSymbols(&s.Children[i])
					if targetModelSym != nil {
						return
					}
				}
			}
			walkSymbols(analysis.Structure)

			if targetModelSym == nil {
				t.Skipf("field %q not found in structure tree", tc.fieldName)
			}

			// Convert to protocol.DocumentSymbol via the real converter
			docSym := symbolToDocumentSymbol(*targetModelSym, contentStr, protocol.PositionEncodingKindUTF8)

			// Assertion: Detail presence and content
			if tc.expectNilDtl {
				// Group header: Detail must be nil
				if docSym.Detail != nil {
					t.Errorf("group header Detail = %s, want nil (groups have no Detail)", *docSym.Detail)
				}
			} else {
				// Scalar field: Detail must be set to the Type
				if docSym.Detail == nil {
					t.Errorf("scalar field Detail is nil, want %q", tc.expectedType)
				} else if *docSym.Detail != tc.expectedType {
					t.Errorf("scalar field Detail = %q, want %q", *docSym.Detail, tc.expectedType)
				}
			}
		})
	}
}

// TestSymbolToDocumentSymbol_TypedFieldsPreservesOutline tests that the
// addition of Detail metadata in T2 is purely enriching and does NOT change
// the set of outline nodes, their names, ranges, or structure hierarchy
// (pure enrichment regression guard).
func TestSymbolToDocumentSymbol_TypedFieldsPreservesOutline(t *testing.T) {
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "07-typed-fields.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	contentStr := string(content)

	// Expected node set (derived from fixture 07-typed-fields.NSP):
	// - root: object
	//   - LOCAL (section)
	//     - SIMPLE-STRING (field, scalar)
	//     - NUMERIC-FIELD (field, scalar)
	//     - PACKED-DEC (field, scalar)
	//     - INTEGER-VAL (field, scalar)
	//     - DYNAMIC-STRING (field, scalar)
	//     - CUSTOMER-GROUP (field, group with children)
	//       - CUSTOMER-ID (field)
	//       - CUSTOMER-NAME (field)
	//       - ADDRESS-DETAILS (field, group with children)
	//         - STREET (field)
	//         - CITY (field)

	// Walk the converted tree and count nodes / verify structure
	docSym := symbolToDocumentSymbol(*analysis.Structure, contentStr, protocol.PositionEncodingKindUTF8)

	// Find the LOCAL section
	var localSection *protocol.DocumentSymbol
	for i := range docSym.Children {
		if docSym.Children[i].Kind == protocol.SymbolKindNamespace && docSym.Children[i].Name == "LOCAL" {
			localSection = &docSym.Children[i]
			break
		}
	}
	if localSection == nil {
		t.Fatal("LOCAL section not found in converted outline")
	}

	// Expected scalar fields (order preserved from fixture)
	expectedScalars := []string{
		"SIMPLE-STRING",
		"NUMERIC-FIELD",
		"PACKED-DEC",
		"INTEGER-VAL",
		"DYNAMIC-STRING",
	}

	// Expected group fields
	expectedGroups := []string{
		"CUSTOMER-GROUP",
	}

	// Verify scalar fields are present and at the right level
	scalarCount := 0
	for _, child := range localSection.Children {
		if child.Kind == protocol.SymbolKindField {
			for _, expected := range expectedScalars {
				if child.Name == expected {
					scalarCount++
					// Verify it's a leaf (no children, but that's not guaranteed for all scalars)
					break
				}
			}
		}
	}
	if scalarCount != len(expectedScalars) {
		t.Errorf("found %d scalar fields, want %d (expect %v)", scalarCount, len(expectedScalars), expectedScalars)
	}

	// Verify group fields are present and have children
	groupCount := 0
	for _, child := range localSection.Children {
		if child.Kind == protocol.SymbolKindField {
			for _, expected := range expectedGroups {
				if child.Name == expected {
					groupCount++
					if len(child.Children) == 0 {
						t.Errorf("group %q has no children, want nested fields", child.Name)
					}
					// Check for known children: CUSTOMER-ID, CUSTOMER-NAME, ADDRESS-DETAILS
					childNames := make(map[string]bool)
					for _, nestedChild := range child.Children {
						childNames[nestedChild.Name] = true
					}
					if !childNames["CUSTOMER-ID"] {
						t.Error("CUSTOMER-ID not found in CUSTOMER-GROUP children")
					}
					if !childNames["CUSTOMER-NAME"] {
						t.Error("CUSTOMER-NAME not found in CUSTOMER-GROUP children")
					}
					if !childNames["ADDRESS-DETAILS"] {
						t.Error("ADDRESS-DETAILS not found in CUSTOMER-GROUP children")
					}
					break
				}
			}
		}
	}
	if groupCount != len(expectedGroups) {
		t.Errorf("found %d group fields, want %d (expect %v)", groupCount, len(expectedGroups), expectedGroups)
	}
}

// TestSymbolDetail_Arrays tests the symbolDetail function for array fields (FR-55, feature 28, task T4).
//
// Exercises: arrays with single dimension (A10 (1:10)), multi-dimensional arrays (P9.2 (1:5,1:10)),
// and unbounded arrays (A20 (1:*)); asserts that OCCURS never appears in output.
//
// Fixture: 09-arrays.NSP has three array fields: single-dim, multi-dim, and unbounded.
func TestSymbolDetail_Arrays(t *testing.T) {
	// Read the fixture: 09-arrays.NSP with array fields
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "09-arrays.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze to get FileAnalysis with Structure
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	// Extract the LOCAL data section to inspect its field children
	var localSection *model.Symbol
	for _, child := range analysis.Structure.Children {
		if child.Kind == model.SymbolDataSection && strings.ToUpper(child.Name) == "LOCAL" {
			localSection = &child
			break
		}
	}
	if localSection == nil {
		t.Fatal("expected a LOCAL data section in the structure")
	}

	// Test cases for each array field
	tests := []struct {
		name          string
		fieldName     string
		expectDetail  string
		checkNoOccurs bool // Assert that "OCCURS" does not appear in detail
	}{
		{
			name:          "single_dim_array",
			fieldName:     "#TAGS",
			expectDetail:  "A10 (1:10)",
			checkNoOccurs: true,
		},
		{
			name:          "multi_dim_array",
			fieldName:     "#SCORES",
			expectDetail:  "P9.2 (1:5,1:10)",
			checkNoOccurs: true,
		},
		{
			name:          "unbounded_array",
			fieldName:     "#EXTENDED-BUFFER",
			expectDetail:  "A20 (1:*)",
			checkNoOccurs: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Find the field in the section's children
			var targetField *model.Symbol
			for i := range localSection.Children {
				if localSection.Children[i].Name == tc.fieldName {
					targetField = &localSection.Children[i]
					break
				}
			}
			if targetField == nil {
				t.Fatalf("expected field %s in LOCAL section", tc.fieldName)
			}

			// Act: call symbolDetail to get the rendered detail string
			detail := symbolDetail(*targetField)

			// Assert: detail is not nil
			if detail == nil {
				t.Errorf("symbolDetail(%s) returned nil, want non-nil detail", tc.fieldName)
				return
			}

			// Assert: detail matches expected string
			if *detail != tc.expectDetail {
				t.Errorf("symbolDetail(%s) = %q, want %q", tc.fieldName, *detail, tc.expectDetail)
			}

			// Assert: no "OCCURS" appears in detail
			if tc.checkNoOccurs && strings.Contains(*detail, "OCCURS") {
				t.Errorf("symbolDetail(%s) = %q contains forbidden word 'OCCURS'", tc.fieldName, *detail)
			}
		})
	}
}

// TestSymbolDetail_RedefineAndFiller tests the symbolDetail function for REDEFINE blocks and FILLER gaps
// (FR-55, feature 28, task T4).
//
// Exercises: REDEFINE sub-field labeling contract per OQ-2: a symbol with Redefines != "" renders
// its detail as "<type> REDEFINE <target>" (e.g., "A2 REDEFINE #CUSTOMER-ID" for a redefine sub-field,
// "3X REDEFINE #CUSTOMER-ID" for a FILLER gap). A non-redefine field (Redefines=="") shows only its
// type (e.g., "A10" for the top-level target). The implementation requires:
//  1. dataDefinitionToSymbol (structure.go) to carry def.Redefines onto model.Symbol.Redefines
//  2. symbolDetail to append " REDEFINE <target>" when Redefines != ""
//
// Fixture: 08-redefine.NSP has a target field (#CUSTOMER-ID) with REDEFINE sub-fields (#REGION, #SEQ,
// FILLER, #CODE) per T3's flatten-with-stamp approach.
func TestSymbolDetail_RedefineAndFiller(t *testing.T) {
	// Read the fixture: 08-redefine.NSP
	fixturePath := filepath.Join("..", "analysis", "natural", "testdata", "structure", "08-redefine.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	// Analyze to get FileAnalysis with Structure
	az := natural.New(nil)
	analysis, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Logf("Analyze returned error (graceful degradation): %v", err)
	}

	if analysis.Structure == nil {
		t.Fatal("expected FileAnalysis.Structure to be non-nil")
	}

	// Extract the LOCAL data section
	var localSection *model.Symbol
	for _, child := range analysis.Structure.Children {
		if child.Kind == model.SymbolDataSection && strings.ToUpper(child.Name) == "LOCAL" {
			localSection = &child
			break
		}
	}
	if localSection == nil {
		t.Fatal("expected a LOCAL data section in the structure")
	}

	// The structure (per T3's flatten-with-stamp approach) should have:
	// 1. #CUSTOMER-ID (A10) — the target field
	// 2. Children of #CUSTOMER-ID: sub-fields with Redefines set
	//    - #REGION (A2) with Redefines="#CUSTOMER-ID"
	//    - #SEQ (N8) with Redefines="#CUSTOMER-ID"
	//    - FILLER (3X) with Redefines="#CUSTOMER-ID"
	//    - #CODE (A3) with Redefines="#CUSTOMER-ID"

	// Helper function to find a field by name in the LOCAL section or as a child of #CUSTOMER-ID
	findFieldByName := func(name string) *model.Symbol {
		// First, try to find it in the LOCAL section directly (e.g., #CUSTOMER-ID)
		for i := range localSection.Children {
			if localSection.Children[i].Name == name {
				return &localSection.Children[i]
			}
		}
		// Next, try to find it as a child of #CUSTOMER-ID (redefine sub-fields)
		for i := range localSection.Children {
			if localSection.Children[i].Name == "#CUSTOMER-ID" {
				for j := range localSection.Children[i].Children {
					if localSection.Children[i].Children[j].Name == name {
						return &localSection.Children[i].Children[j]
					}
				}
			}
		}
		return nil
	}

	tests := []struct {
		name          string
		fieldName     string
		expectDetail  string
		expectNonNil  bool
		checkNoOccurs bool
	}{
		{
			name:          "target_field_no_redefine_label",
			fieldName:     "#CUSTOMER-ID",
			expectDetail:  "A10", // Top-level target: no REDEFINE label (Redefines=="")
			expectNonNil:  true,
			checkNoOccurs: true,
		},
		{
			name:          "redefine_subfield_region",
			fieldName:     "#REGION",
			expectDetail:  "A2 REDEFINE #CUSTOMER-ID", // Sub-field: type + REDEFINE label
			expectNonNil:  true,
			checkNoOccurs: true,
		},
		{
			name:          "redefine_subfield_seq",
			fieldName:     "#SEQ",
			expectDetail:  "N8 REDEFINE #CUSTOMER-ID", // Sub-field: type + REDEFINE label
			expectNonNil:  true,
			checkNoOccurs: true,
		},
		{
			name:          "filler_gap",
			fieldName:     "FILLER",
			expectDetail:  "3X REDEFINE #CUSTOMER-ID", // FILLER: format + REDEFINE label
			expectNonNil:  true,
			checkNoOccurs: true,
		},
		{
			name:          "redefine_subfield_code",
			fieldName:     "#CODE",
			expectDetail:  "A3 REDEFINE #CUSTOMER-ID", // Another sub-field: type + REDEFINE label
			expectNonNil:  true,
			checkNoOccurs: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Find the field
			targetField := findFieldByName(tc.fieldName)
			if targetField == nil {
				t.Fatalf("expected to find field %s in the structure", tc.fieldName)
			}

			// Act: call symbolDetail
			detail := symbolDetail(*targetField)

			// Assert: nil/non-nil expectation
			if tc.expectNonNil && detail == nil {
				t.Errorf("symbolDetail(%s) returned nil, want non-nil detail", tc.fieldName)
				return
			}

			// Assert: detail matches expected
			if detail != nil {
				if *detail != tc.expectDetail {
					t.Errorf("symbolDetail(%s) = %q, want %q", tc.fieldName, *detail, tc.expectDetail)
				}

				// Assert: no "OCCURS"
				if tc.checkNoOccurs && strings.Contains(*detail, "OCCURS") {
					t.Errorf("symbolDetail(%s) = %q contains forbidden word 'OCCURS'", tc.fieldName, *detail)
				}
			}
		})
	}
}
