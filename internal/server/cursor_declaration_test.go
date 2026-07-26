package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestCursorLookup_declarationNameRange verifies that findDeclarationTarget maps a cursor
// positioned on a data-field declaration's NameRange to a DeclarationTarget carrying the
// field's NameRange and owning Symbol/DataDefinition (feature 28, T8a — OQ-B).
//
// Expected behavior:
//  1. Cursor on a plain data-field declaration name → findDeclarationTarget returns non-nil
//     DeclarationTarget with NameRange and Definition/Symbol populated
//  2. Cursor in whitespace / on a keyword → findDeclarationTarget returns nil (FR-43)
//  3. findCursorTarget (existing) is unaffected — still returns use-sites as before
//  4. Smallest-containing-range tie-break is applied for declarations (like use-sites)
//
// The companion-function pattern preserves use-site-first precedence at the CALL SITE:
// providers call findCursorTarget first, only falling back to findDeclarationTarget when
// no use-site is found (no signature change to findCursorTarget needed).
//
// This RED-phase test FAILS on assertion because findDeclarationTarget's stub returns nil.
func TestCursorLookup_declarationNameRange(t *testing.T) {
	// Arrange: read and analyze the fixture
	fixturePath := filepath.Join("testdata", "navigation", "data-fields.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Analyze the fixture using the parser backend
	az := natural.New(nil)
	fa, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if fa.Structure == nil || fa.Structure.Children == nil {
		t.Fatal("Expected structure with children; got nil")
	}

	if len(fa.Definitions) == 0 {
		t.Fatal("No data definitions extracted from fixture; expected at least one")
	}

	// Helper: recursively search for a definition by name in Definitions and nested Children
	var findDefRecursive func(name string, defs []model.DataDefinition) *model.DataDefinition
	findDefRecursive = func(name string, defs []model.DataDefinition) *model.DataDefinition {
		for i := range defs {
			if defs[i].Name == name {
				return &defs[i]
			}
			if found := findDefRecursive(name, defs[i].Children); found != nil {
				return found
			}
		}
		return nil
	}

	// Find a simple data field to test declaration targeting
	fieldDef := findDefRecursive("EMP-ID", fa.Definitions)
	if fieldDef == nil {
		t.Fatal("Data field 'EMP-ID' not found in definitions; check fixture")
	}

	if fieldDef.NameRange.Start == (model.Position{}) || fieldDef.NameRange.End == (model.Position{}) {
		t.Fatalf("EMP-ID NameRange is zero: %v; fixture may not have been extracted properly", fieldDef.NameRange)
	}

	// Table-driven test cases
	tests := []struct {
		name              string
		pos               model.Position
		wantDeclaration   bool   // true if we expect a non-nil declaration target
		expectedFieldName string // expected Definition.Name if declaration is found
		description       string
	}{
		{
			name:              "cursor_on_datafield_declaration_name",
			pos:               fieldDef.NameRange.Start,
			wantDeclaration:   true,
			expectedFieldName: "EMP-ID",
			description:       "Cursor on a data-field declaration name should resolve to declaration target",
		},
		{
			name:            "cursor_on_whitespace",
			pos:             model.Position{Line: 100, Column: 1},
			wantDeclaration: false,
			description:     "Cursor on unrelated whitespace should return nil",
		},
		{
			name:            "cursor_on_keyword",
			pos:             model.Position{Line: 4, Column: 1}, // "DEFINE" keyword
			wantDeclaration: false,
			description:     "Cursor on a keyword should return nil (FR-43)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: call findDeclarationTarget
			decl := findDeclarationTarget(fa, tc.pos)

			// Assert: declaration target is returned (or not) as expected
			if tc.wantDeclaration && decl == nil {
				t.Errorf("Expected non-nil declaration target, got nil")
			}
			if !tc.wantDeclaration && decl != nil {
				t.Errorf("Expected nil declaration target, got one: %+v", decl)
			}

			// Assert: declaration target carries the expected field context
			if tc.wantDeclaration && decl != nil {
				if decl.NameRange == (model.Range{}) {
					t.Errorf("Declaration target NameRange is zero")
				}
				if decl.Definition == nil && decl.Symbol == nil {
					t.Errorf("Declaration target must carry at least Definition or Symbol context")
				}
				if tc.expectedFieldName != "" && decl.Definition != nil {
					if decl.Definition.Name != tc.expectedFieldName {
						t.Errorf("Declaration field name = %q, expected %q", decl.Definition.Name, tc.expectedFieldName)
					}
				}
			}
		})
	}
}

// TestCursorLookup_useSiteFirstRegression verifies that use-site targeting (existing behavior)
// is unchanged — findCursorTarget still returns use-sites and is not affected by the new
// findDeclarationTarget companion function (feature 28, T8a, OQ-B regression).
//
// This ensures that the companion-function pattern preserves existing behavior: providers
// call findCursorTarget first, and only fall back to findDeclarationTarget when no use-site
// is found (use-site-first at the call site, not in the function signature).
func TestCursorLookup_useSiteFirstRegression(t *testing.T) {
	// Arrange: read and analyze the fixture
	fixturePath := filepath.Join("testdata", "navigation", "cursor-lookup.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Analyze the fixture using the parser backend
	az := natural.New(nil)
	fa, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(fa.Edges) == 0 {
		t.Fatal("No edges extracted from fixture; expected at least one")
	}

	// Find a CALLNAT edge to test use-site-first precedence
	var callnatEdge *model.EdgeEntry
	for i := range fa.Edges {
		if fa.Edges[i].Kind == model.EdgeCalls {
			callnatEdge = &fa.Edges[i]
			break
		}
	}

	if callnatEdge == nil {
		t.Skip("No CALLNAT edge found in fixture; skipping use-site-first regression test")
	}

	// Act: call findCursorTarget on the CALLNAT edge's source
	edge, access, varRef := findCursorTarget(fa, callnatEdge.Source.Start, string(content), az)

	// Assert: use-site is still found by findCursorTarget (regression)
	if edge == nil {
		t.Errorf("Expected edge, got nil; findCursorTarget regression: edge not found")
	}
	if access != nil || varRef != nil {
		t.Errorf("Expected only edge, got access=%v, varRef=%v; findCursorTarget precedence broken", access != nil, varRef != nil)
	}
	if edge != nil && edge.Kind != model.EdgeCalls {
		t.Errorf("Expected EdgeCalls, got %s; findCursorTarget returned wrong edge kind", edge.Kind)
	}

	// Assert: findDeclarationTarget is independent and returns nil for the stub
	// (the test documents that even though we have a use-site, findDeclarationTarget
	// would return nil in the stub; in green, we never call it when a use-site exists)
	decl := findDeclarationTarget(fa, callnatEdge.Source.Start)
	if decl != nil {
		t.Logf("findDeclarationTarget returned %+v; stub should return nil until implemented", decl)
		// Note: we don't fail here — the stub returning nil is expected.
		// The real assertion is that findCursorTarget still works and providers call it first.
	}
}
