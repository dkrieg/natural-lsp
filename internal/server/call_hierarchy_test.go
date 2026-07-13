package server

import (
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
				item := buildCallHierarchyItem(root, calleeRelPathNorm, calleeContent, calleeAnalysis.Structure, enc)

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
				item := buildCallHierarchyItem(root, calleeRelPathNorm, calleeContent, subAChild, enc)

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
				item := buildCallHierarchyItem(root, calleeRelPathNorm, calleeContent, calleeAnalysis.Structure, enc)
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

// TestProvidePrepareCallHierarchyCallSite tests the textDocument/prepareCallHierarchy handler
// for cursor on a call site (Feature 18, Story 1, AC1).
//
// FR-ID: FR-49 (call hierarchy), AC1 — "textDocument/prepareCallHierarchy at a ... call site
// returns a CallHierarchyItem with the symbol's name, kind, file URI, and selection range."
func TestProvidePrepareCallHierarchyCallSite(t *testing.T) {
	testdata := filepath.Join("testdata", "callhierarchy")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"CALLER.NSP", "CALLEE.NSN"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdata, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "callhierarchy", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Read CALLER.NSP to find cursor positions
	callerPath := filepath.Join(testdata, "CALLER.NSP")
	callerContent, err := os.ReadFile(callerPath)
	if err != nil {
		t.Fatalf("failed to read CALLER.NSP: %v", err)
	}
	callerContentStr := string(callerContent)

	// Create handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
	}

	tt := []struct {
		name           string
		filePath       string
		findText       string // Text to find in the file to determine cursor position
		expectItemName string // Expected CallHierarchyItem.Name
		expectItemKind protocol.SymbolKind
		expectCount    int // Expected number of items returned (1 for single, 2 for ambiguous, 0 for empty)
		story          string
	}{
		{
			name:           "cursor on 'CALLEE' call site → one item",
			filePath:       filepath.Join("testdata", "callhierarchy", "CALLER.NSP"),
			findText:       "CALLNAT 'CALLEE'", // Find the static call
			expectItemName: "CALLEE",
			expectItemKind: protocol.SymbolKindModule,
			expectCount:    1,
			story:          "Story 1, AC1: call-site → callee item",
		},
		{
			name:           "cursor on inline DEFINE SUBROUTINE name → one item",
			filePath:       filepath.Join("testdata", "callhierarchy", "CALLER.NSP"),
			findText:       "DEFINE SUBROUTINE SUB-A",
			expectItemName: "SUB-A",
			expectItemKind: protocol.SymbolKindFunction,
			expectCount:    1,
			story:          "Story 1, AC1: definition-name → own item",
		},
		{
			name:           "cursor on dynamic CALLNAT #DYN target → empty",
			filePath:       filepath.Join("testdata", "callhierarchy", "CALLER.NSP"),
			findText:       "CALLNAT #DYN",
			expectItemName: "",
			expectItemKind: 0,
			expectCount:    0,
			story:          "Story 1, AC2: dynamic/unresolved → empty",
		},
		{
			name:           "cursor on a comment/data field → empty",
			filePath:       filepath.Join("testdata", "callhierarchy", "CALLER.NSP"),
			findText:       "* A program with",
			expectItemName: "",
			expectItemKind: 0,
			expectCount:    0,
			story:          "Story 1, AC3: non-callable position → empty",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Find the cursor position by searching for the text
			content := callerContentStr
			idx_pos := strings.Index(content, tc.findText)
			if idx_pos < 0 {
				t.Fatalf("could not find text '%s' in file", tc.findText)
			}

			// Convert byte offset to line/char
			// Count newlines before the position to get the line number
			line := 0
			char := 0
			for i := 0; i < idx_pos; i++ {
				if content[i] == '\n' {
					line++
					char = 0
				} else {
					char++
				}
			}

			// Call providePrepareCallHierarchy
			params := protocol.CallHierarchyPrepareParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(root, tc.filePath)),
					},
					Position: protocol.Position{Line: uint32(line), Character: uint32(char)},
				},
			}

			items, err := providePrepareCallHierarchy(hctx, params)

			// Check error
			if err != nil {
				t.Errorf("providePrepareCallHierarchy returned error: %v", err)
			}

			// Check count of items
			if len(items) != tc.expectCount {
				t.Errorf("providePrepareCallHierarchy returned %d items, want %d (%s)",
					len(items), tc.expectCount, tc.story)
			}

			// If expecting items, verify their properties
			if tc.expectCount > 0 {
				for i, item := range items {
					if item.Name != tc.expectItemName {
						t.Errorf("item[%d].Name = %q, want %q", i, item.Name, tc.expectItemName)
					}
					if item.Kind != tc.expectItemKind {
						t.Errorf("item[%d].Kind = %v, want %v", i, item.Kind, tc.expectItemKind)
					}

					// Verify URI is set and matches the expected file
					if item.URI == "" {
						t.Errorf("item[%d].URI is empty", i)
					}

					// Verify Data is populated (can round-trip via resolveItemIdentity)
					if len(item.Data) == 0 {
						t.Errorf("item[%d].Data is empty", i)
					}
				}
			}
		})
	}
}

// TestProvidePrepareCallHierarchyAmbiguous tests the textDocument/prepareCallHierarchy handler
// for an ambiguous call site (Feature 18, T3 — OQ-4 approved decision).
//
// FR-ID: FR-49, AC1 — "returns a CallHierarchyItem ... at a call site",
// with OQ-4 approved decision: "Ambiguous target → one CallHierarchyItem per candidate".
func TestProvidePrepareCallHierarchyAmbiguous(t *testing.T) {
	testdata := filepath.Join("testdata", "callhierarchy", "AMBIGUOUS_CALLER")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture (flat namespace, no library map)
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"MAIN.NSP", "LIBA/AMBIG.NSN", "LIBB/AMBIG.NSN"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdata, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "callhierarchy", "AMBIGUOUS_CALLER", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Compute the resolution set (flat namespace will produce Ambiguous for AMBIG)
	resSet := workspace.Resolve(idx, &cfg)

	// Read MAIN.NSP to find cursor position
	mainPath := filepath.Join(testdata, "MAIN.NSP")
	mainContent, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("failed to read MAIN.NSP: %v", err)
	}
	mainContentStr := string(mainContent)

	// Create handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
	}

	// Find cursor position on "CALLNAT 'AMBIG'" target
	findText := "CALLNAT 'AMBIG'"
	idx_pos := strings.Index(mainContentStr, findText)
	if idx_pos < 0 {
		t.Fatalf("could not find text '%s' in MAIN.NSP", findText)
	}

	// Convert byte offset to line/char
	line := 0
	char := 0
	for i := 0; i < idx_pos; i++ {
		if mainContentStr[i] == '\n' {
			line++
			char = 0
		} else {
			char++
		}
	}

	// Call providePrepareCallHierarchy
	params := protocol.CallHierarchyPrepareParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(root, "testdata", "callhierarchy", "AMBIGUOUS_CALLER", "MAIN.NSP")),
			},
			Position: protocol.Position{Line: uint32(line), Character: uint32(char)},
		},
	}

	items, err := providePrepareCallHierarchy(hctx, params)

	// Check error
	if err != nil {
		t.Errorf("providePrepareCallHierarchy returned error: %v", err)
	}

	// Expect 2 items (one per candidate) per OQ-4 approved decision
	if len(items) != 2 {
		t.Errorf("providePrepareCallHierarchy returned %d items, want 2 (ambiguous → one per candidate)",
			len(items))
	}

	// Verify each item
	expectedNames := []string{"AMBIG", "AMBIG"}
	expectedKinds := []protocol.SymbolKind{protocol.SymbolKindFunction, protocol.SymbolKindFunction}

	// Both should have the name AMBIG
	for i, item := range items {
		if item.Name != expectedNames[0] {
			t.Errorf("item[%d].Name = %q, want %q", i, item.Name, expectedNames[0])
		}
		if item.Kind != expectedKinds[0] {
			t.Errorf("item[%d].Kind = %v, want %v", i, item.Kind, expectedKinds[0])
		}

		// Verify Data is populated
		if len(item.Data) == 0 {
			t.Errorf("item[%d].Data is empty", i)
		}

		// Verify each item has a URI set
		if item.URI == "" {
			t.Errorf("item[%d].URI is empty", i)
		}
	}

	// Verify the items have distinct URIs (one per candidate)
	if len(items) >= 2 {
		if items[0].URI == items[1].URI {
			t.Errorf("items[0].URI == items[1].URI, want distinct URIs for each candidate")
		}
	}
}
