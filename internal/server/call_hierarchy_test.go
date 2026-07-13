package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// TestCallHierarchyItemRoundTrip tests the buildCallHierarchyItem and resolveItemIdentity
// helpers (feature 18, T2). It verifies that a CallHierarchyItem can be built from a symbol,
// and then recovered from the item via resolveItemIdentity using both the direct Data path
// and the fallback URI+SelectionRange path.
//
// Story: enables both incoming and outgoing call lookup. AC: data round-trip, fallback on
// missing/garbled Data (FR-43).
func TestCallHierarchyItemRoundTrip(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Analyze the fixture file CALLEE.NSN
	calleeRelPath := filepath.Join("testdata", "callhierarchy", "CALLEE.NSN")
	calleeAbsPath := filepath.Join(root, calleeRelPath)
	calleeContent, err := os.ReadFile(calleeAbsPath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", calleeAbsPath, err)
	}

	// Analyze the file
	az := natural.New(nil)
	calleeAnalysis, err := az.Analyze(calleeAbsPath, calleeContent)
	if err != nil {
		t.Fatalf("failed to analyze fixture: %v", err)
	}

	// Verify the Structure is non-nil and has the expected root + subroutine child
	if calleeAnalysis.Structure == nil {
		t.Fatalf("CALLEE.NSN Structure is nil; expected object root + subroutine")
	}
	if calleeAnalysis.Structure.Kind != model.SymbolObject {
		t.Fatalf("Structure root kind = %q, want %q", calleeAnalysis.Structure.Kind, model.SymbolObject)
	}

	// Find the SUB-A subroutine in the root's children
	var subAChild *model.Symbol
	for i := range calleeAnalysis.Structure.Children {
		if calleeAnalysis.Structure.Children[i].Kind == model.SymbolSubroutine &&
			strings.EqualFold(calleeAnalysis.Structure.Children[i].Name, "SUB-A") {
			subAChild = &calleeAnalysis.Structure.Children[i]
			break
		}
	}

	// Normalize calleeRelPath to forward slashes (index key convention)
	calleeRelPathNorm := strings.ReplaceAll(calleeRelPath, "\\", "/")

	// Build the index with the fixture
	idx := &workspace.Index{}
	idx.Add(calleeRelPathNorm, calleeAnalysis)

	enc := protocol.PositionEncodingKindUTF8

	tt := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "(a) round-trip object root",
			run: func(t *testing.T) {
				// Act: build an item from the object root
				item := buildCallHierarchyItem(root, calleeRelPathNorm, calleeAnalysis.Structure, enc)

				// Assert: basic fields are populated
				if item.Name != calleeAnalysis.Structure.Name {
					t.Errorf("item.Name = %q, want %q", item.Name, calleeAnalysis.Structure.Name)
				}
				if item.Kind != protocol.SymbolKindModule {
					t.Errorf("item.Kind = %v, want %v", item.Kind, protocol.SymbolKindModule)
				}

				// Act: resolve the item back
				relPath, _, sym, ok := resolveItemIdentity(idx, root, item, enc)

				// Assert: recovery succeeds
				if !ok {
					t.Fatalf("resolveItemIdentity returned ok=false")
				}
				if relPath != calleeRelPathNorm {
					t.Errorf("recovered relPath = %q, want %q", relPath, calleeRelPathNorm)
				}
				if sym == nil {
					t.Fatalf("recovered sym is nil")
				}
				if !strings.EqualFold(sym.Name, calleeAnalysis.Structure.Name) {
					t.Errorf("recovered sym.Name = %q, want %q", sym.Name, calleeAnalysis.Structure.Name)
				}
				if sym.Kind != model.SymbolObject {
					t.Errorf("recovered sym.Kind = %q, want %q", sym.Kind, model.SymbolObject)
				}
			},
		},
		{
			name: "(b) round-trip subroutine child",
			run: func(t *testing.T) {
				if subAChild == nil {
					t.Skip("SUB-A subroutine not found in Structure.Children")
				}

				// Act: build an item from the subroutine
				item := buildCallHierarchyItem(root, calleeRelPathNorm, subAChild, enc)

				// Assert: basic fields are populated
				if !strings.EqualFold(item.Name, subAChild.Name) {
					t.Errorf("item.Name = %q, want %q", item.Name, subAChild.Name)
				}
				if item.Kind != protocol.SymbolKindFunction {
					t.Errorf("item.Kind = %v, want %v", item.Kind, protocol.SymbolKindFunction)
				}

				// Act: resolve the item back
				relPath, _, sym, ok := resolveItemIdentity(idx, root, item, enc)

				// Assert: recovery succeeds
				if !ok {
					t.Fatalf("resolveItemIdentity returned ok=false")
				}
				if relPath != calleeRelPathNorm {
					t.Errorf("recovered relPath = %q, want %q", relPath, calleeRelPathNorm)
				}
				if sym == nil {
					t.Fatalf("recovered sym is nil")
				}
				if !strings.EqualFold(sym.Name, subAChild.Name) {
					t.Errorf("recovered sym.Name = %q, want %q", sym.Name, subAChild.Name)
				}
				if sym.Kind != model.SymbolSubroutine {
					t.Errorf("recovered sym.Kind = %q, want %q", sym.Kind, model.SymbolSubroutine)
				}
			},
		},
		{
			name: "(c) fallback: clear Data, recover via URI+SelectionRange",
			run: func(t *testing.T) {
				// Act: build an item from the object root and blank its Data
				item := buildCallHierarchyItem(root, calleeRelPathNorm, calleeAnalysis.Structure, enc)
				// Blank the Data field
				item.Data = protocol.LSPAny{}

				// Act: resolve the item back using the fallback path
				relPath, _, sym, ok := resolveItemIdentity(idx, root, item, enc)

				// Assert: fallback succeeds
				if !ok {
					t.Fatalf("resolveItemIdentity returned ok=false on fallback (Data-less)")
				}
				if relPath != calleeRelPathNorm {
					t.Errorf("recovered relPath = %q, want %q (fallback)", relPath, calleeRelPathNorm)
				}
				if sym == nil {
					t.Fatalf("recovered sym is nil (fallback)")
				}
				if !strings.EqualFold(sym.Name, calleeAnalysis.Structure.Name) {
					t.Errorf("recovered sym.Name = %q, want %q (fallback)", sym.Name, calleeAnalysis.Structure.Name)
				}
			},
		},
		{
			name: "(d) robustness: garbage Data + out-of-root URI → ok=false, no panic",
			run: func(t *testing.T) {
				// Arrange: an item with nonsense Data and an out-of-root URI
				badItem := protocol.CallHierarchyItem{
					Name: "GARBAGE",
					Kind: protocol.SymbolKindObject,
					URI:  uri.File("/nonexistent/path/GARBAGE.NSP"),
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 0},
					},
					SelectionRange: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 0},
					},
					Data: protocol.LSPAny("}{invalid json}"),
				}

				// Act: resolve the item (should fail gracefully)
				relPath, fa, sym, ok := resolveItemIdentity(idx, root, badItem, enc)

				// Assert: returns ok=false without panic
				if ok {
					t.Errorf("resolveItemIdentity on garbage Data returned ok=true, want false")
				}
				if relPath != "" || fa.ObjectType != "" || sym != nil {
					t.Errorf("resolveItemIdentity on garbage Data should return zero values, got relPath=%q, fa.ObjectType=%q, sym=%v",
						relPath, fa.ObjectType, sym)
				}
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, tc.run)
	}
}
