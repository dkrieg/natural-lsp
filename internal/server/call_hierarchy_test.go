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

// TestProvideIncomingCalls tests the callHierarchy/incomingCalls handler (Feature 18, T4).
//
// FR-ID: FR-49 (call hierarchy), Story 2, AC1–3:
//
//	AC1: "callHierarchy/incomingCalls at a symbol returns all call sites across the workspace
//	     that resolve to that symbol, grouped by caller file."
//	AC2: "Each incoming call entry carries the caller's CallHierarchyItem and a fromRanges
//	     array with all call-site ranges within that caller."
//	AC3: "Dynamic/unresolved call sites are excluded (FR-11/FR-17); only resolved edges count."
func TestProvideIncomingCalls(t *testing.T) {
	testdata := filepath.Join("testdata", "callhierarchy")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"CALLER.NSP", "CALLER2.NSP", "CALLEE.NSN"}
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

	// Read CALLEE.NSN to build the target item
	calleeAbsPath := filepath.Join(root, testdata, "CALLEE.NSN")
	calleeContent, err := os.ReadFile(calleeAbsPath)
	if err != nil {
		t.Fatalf("failed to read CALLEE.NSN: %v", err)
	}

	calleeAnalysis, ok := idx.Get("testdata/callhierarchy/CALLEE.NSN")
	if !ok {
		t.Fatalf("CALLEE.NSN not in index")
	}

	if calleeAnalysis.Structure == nil {
		t.Fatalf("CALLEE.NSN Structure is nil")
	}

	// Build the target item from CALLEE's object root
	targetItem := buildCallHierarchyItem(root, "testdata/callhierarchy/CALLEE.NSN", calleeContent, calleeAnalysis.Structure, protocol.PositionEncodingKindUTF8)

	// Create handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
	}

	// Call provideIncomingCalls
	params := protocol.CallHierarchyIncomingCallsParams{
		Item: targetItem,
	}

	result, err := provideIncomingCalls(hctx, params)

	// Check error
	if err != nil {
		t.Errorf("provideIncomingCalls returned error: %v", err)
	}

	// Assert the result (AC1–AC3)
	t.Run("AC1: returns all static callers", func(t *testing.T) {
		// Expect exactly 2 incoming calls (CALLER.NSP with 1 site, CALLER2.NSP with 2 sites)
		if len(result) != 2 {
			t.Errorf("result length = %d, want 2 (CALLER.NSP + CALLER2.NSP)", len(result))
		}

		// The result should be sorted by caller URI
		if len(result) >= 1 {
			// First caller should be CALLER.NSP
			firstCallerName := result[0].From.Name
			if firstCallerName != "CALLER" {
				t.Errorf("result[0].From.Name = %q, want %q", firstCallerName, "CALLER")
			}

			// First caller should have 1 fromRange (one CALLNAT 'CALLEE' site)
			if len(result[0].FromRanges) != 1 {
				t.Errorf("result[0].FromRanges length = %d, want 1", len(result[0].FromRanges))
			}
		}

		if len(result) >= 2 {
			// Second caller should be CALLER2.NSP
			secondCallerName := result[1].From.Name
			if secondCallerName != "CALLER2" {
				t.Errorf("result[1].From.Name = %q, want %q", secondCallerName, "CALLER2")
			}

			// Second caller should have 2 fromRanges (two CALLNAT 'CALLEE' sites)
			if len(result[1].FromRanges) != 2 {
				t.Errorf("result[1].FromRanges length = %d, want 2", len(result[1].FromRanges))
			}
		}
	})

	t.Run("AC2: each caller has populated From and sorted FromRanges", func(t *testing.T) {
		for i, incoming := range result {
			// Check From is populated
			if incoming.From.Name == "" {
				t.Errorf("result[%d].From.Name is empty", i)
			}
			if incoming.From.URI == "" {
				t.Errorf("result[%d].From.URI is empty", i)
			}
			if len(incoming.From.Data) == 0 {
				t.Errorf("result[%d].From.Data is empty (should be round-trippable)", i)
			}

			// Check FromRanges are sorted by start position
			for j := 0; j < len(incoming.FromRanges)-1; j++ {
				curr := incoming.FromRanges[j]
				next := incoming.FromRanges[j+1]
				if curr.Start.Line > next.Start.Line ||
					(curr.Start.Line == next.Start.Line && curr.Start.Character > next.Start.Character) {
					t.Errorf("result[%d].FromRanges[%d] not sorted before [%d]", i, j, j+1)
				}
			}
		}
	})

	t.Run("AC3: dynamic calls are excluded", func(t *testing.T) {
		// CALLER.NSP has:
		//   - One static CALLNAT 'CALLEE' (should be included)
		//   - One dynamic CALLNAT #DYN (should be excluded)
		// So we expect only the static call site in the result.
		// Since we got result[0] as CALLER with 1 fromRange, the dynamic is correctly excluded.

		// Verify the FromRanges in CALLER do NOT include the dynamic call site.
		// The dynamic CALLNAT #DYN is on line ~18 in CALLER.NSP
		// The static CALLNAT 'CALLEE' is on line ~12
		// So the range should only cover the static call.
		if len(result) >= 1 {
			// The single fromRange in CALLER should be for the static call on line 11 (0-indexed = line 11)
			// Just check that we have exactly 1 range, not 2 (which would indicate the dynamic was included)
			if len(result[0].FromRanges) != 1 {
				t.Errorf("CALLER.NSP has %d fromRanges, want 1 (dynamic should be excluded)", len(result[0].FromRanges))
			}
		}
	})

	t.Run("deterministic ordering by caller URI", func(t *testing.T) {
		// Callers should be sorted by URI
		if len(result) >= 2 {
			uri1 := string(result[0].From.URI)
			uri2 := string(result[1].From.URI)
			if uri1 > uri2 {
				t.Errorf("result not sorted by URI: %s > %s", uri1, uri2)
			}
		}
	})

	t.Run("FR-43: garbage/unknown Item → empty, no panic", func(t *testing.T) {
		// Create a garbage item with invalid data
		badItem := protocol.CallHierarchyItem{
			Name: "NONEXISTENT",
			Kind: protocol.SymbolKindObject,
			URI:  uri.File(filepath.Join(root, "testdata", "callhierarchy", "NONEXISTENT.NSP")),
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			SelectionRange: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Data: protocol.LSPAny("invalid json data"),
		}

		badParams := protocol.CallHierarchyIncomingCallsParams{
			Item: badItem,
		}

		// Should not panic and should return empty (or nil, which dispatch converts to [])
		badResult, badErr := provideIncomingCalls(hctx, badParams)

		// No error expected
		if badErr != nil {
			t.Errorf("provideIncomingCalls on garbage Item returned error: %v (expected nil)", badErr)
		}

		// Should be empty
		if len(badResult) != 0 {
			t.Errorf("provideIncomingCalls on garbage Item returned %d results, want 0", len(badResult))
		}
	})
}

// TestProvideOutgoingCalls tests the callHierarchy/outgoingCalls handler (Feature 18, T5).
//
// FR-ID: FR-49 (call hierarchy), Story 3, AC1–3:
//
//	AC1: "callHierarchy/outgoingCalls for a resolved item returns all static outgoing call
//	     relationships from that module: CALLNAT, external PERFORM, FETCH, and program-transfer
//	     statements where the target is statically resolved."
//	AC2: "Each outgoing call entry carries the callee's CallHierarchyItem and the fromRanges
//	     (call-site positions within the current module)."
//	AC3: "Dynamic/unresolvable and INCLUDE (copycode) outgoing calls are excluded."
func TestProvideOutgoingCalls(t *testing.T) {
	testdata := filepath.Join("testdata", "callhierarchy")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"CALLER.NSP", "CALLEE.NSN", "PGM.NSP", "CC.NSC"}
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

	// Read CALLER.NSP to build the source item
	callerAbsPath := filepath.Join(root, testdata, "CALLER.NSP")
	callerContent, err := os.ReadFile(callerAbsPath)
	if err != nil {
		t.Fatalf("failed to read CALLER.NSP: %v", err)
	}

	callerAnalysis, ok := idx.Get("testdata/callhierarchy/CALLER.NSP")
	if !ok {
		t.Fatalf("CALLER.NSP not in index")
	}

	if callerAnalysis.Structure == nil {
		t.Fatalf("CALLER.NSP Structure is nil")
	}

	// Build the source item from CALLER's object root
	sourceItem := buildCallHierarchyItem(root, "testdata/callhierarchy/CALLER.NSP", callerContent, callerAnalysis.Structure, protocol.PositionEncodingKindUTF8)

	// Create handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
	}

	// Call provideOutgoingCalls
	params := protocol.CallHierarchyOutgoingCallsParams{
		Item: sourceItem,
	}

	result, err := provideOutgoingCalls(hctx, params)

	// Check error
	if err != nil {
		t.Errorf("provideOutgoingCalls returned error: %v", err)
	}

	// Test sub-cases
	t.Run("AC1: returns all static outgoing CALLNAT/FETCH/external-PERFORM", func(t *testing.T) {
		// CALLER.NSP has:
		//   - CALLNAT 'CALLEE' (static, should resolve to CALLEE.NSN)
		//   - FETCH 'PGM' (static, should resolve to PGM.NSP)
		//   - PERFORM SUB-A (inline/external, depends on whether it's same-file; this is T6a, may be skipped here)
		//   - CALLNAT #DYN (dynamic, excluded)
		//   - INCLUDE CC (excluded by OQ-1)
		// Expect at least 2 items: CALLEE and PGM (PERFORM is handled in T6a)

		if len(result) < 2 {
			t.Errorf("result length = %d, want ≥2 (CALLEE + PGM)", len(result))
		}

		// Extract the names of all callees
		calleeNames := make(map[string]int) // name -> count
		for _, call := range result {
			calleeNames[call.To.Name]++
		}

		// Verify CALLEE is present
		if calleeNames["CALLEE"] < 1 {
			t.Errorf("CALLEE not found in outgoing calls; found: %v", calleeNames)
		}

		// Verify PGM is present
		if calleeNames["PGM"] < 1 {
			t.Errorf("PGM not found in outgoing calls; found: %v", calleeNames)
		}
	})

	t.Run("AC2: each callee has populated To and FromRanges", func(t *testing.T) {
		for i, outgoing := range result {
			// Check To is populated
			if outgoing.To.Name == "" {
				t.Errorf("result[%d].To.Name is empty", i)
			}
			if outgoing.To.URI == "" {
				t.Errorf("result[%d].To.URI is empty", i)
			}
			if len(outgoing.To.Data) == 0 {
				t.Errorf("result[%d].To.Data is empty (should be round-trippable)", i)
			}

			// Check FromRanges are present and sorted by start position
			if len(outgoing.FromRanges) == 0 {
				t.Errorf("result[%d].FromRanges is empty", i)
			}

			for j := 0; j < len(outgoing.FromRanges)-1; j++ {
				curr := outgoing.FromRanges[j]
				next := outgoing.FromRanges[j+1]
				if curr.Start.Line > next.Start.Line ||
					(curr.Start.Line == next.Start.Line && curr.Start.Character > next.Start.Character) {
					t.Errorf("result[%d].FromRanges[%d] not sorted before [%d]", i, j, j+1)
				}
			}
		}
	})

	t.Run("AC3: dynamic calls and INCLUDE are excluded", func(t *testing.T) {
		// CALLER.NSP has:
		//   - CALLNAT #DYN (dynamic, should be excluded)
		//   - INCLUDE CC (copycode, should be excluded by OQ-1)
		// So result should NOT contain "DYN" or "CC"

		calleeNames := make(map[string]int)
		for _, call := range result {
			calleeNames[call.To.Name]++
		}

		if calleeNames["DYN"] > 0 {
			t.Errorf("dynamic CALLNAT #DYN was included in outgoing calls; excluded expected per FR-17")
		}

		if calleeNames["CC"] > 0 {
			t.Errorf("INCLUDE CC was included in outgoing calls; excluded expected per OQ-1 (copycode is compile-time)")
		}
	})

	t.Run("deterministic ordering by callee URI, ranges by start", func(t *testing.T) {
		if len(result) >= 2 {
			// Check callee URIs are sorted
			for i := 0; i < len(result)-1; i++ {
				uri1 := string(result[i].To.URI)
				uri2 := string(result[i+1].To.URI)
				if uri1 > uri2 {
					t.Errorf("result[%d].To.URI > result[%d].To.URI, not sorted", i, i+1)
				}
			}
		}

		// Check each result's FromRanges are sorted by start
		for i, outgoing := range result {
			for j := 0; j < len(outgoing.FromRanges)-1; j++ {
				curr := outgoing.FromRanges[j]
				next := outgoing.FromRanges[j+1]
				if curr.Start.Line > next.Start.Line ||
					(curr.Start.Line == next.Start.Line && curr.Start.Character > next.Start.Character) {
					t.Errorf("result[%d].FromRanges not sorted", i)
				}
			}
		}
	})

	t.Run("FR-43: garbage/unknown Item → empty, no panic", func(t *testing.T) {
		// Create a garbage item
		badItem := protocol.CallHierarchyItem{
			Name: "NONEXISTENT",
			Kind: protocol.SymbolKindObject,
			URI:  uri.File(filepath.Join(root, "testdata", "callhierarchy", "NONEXISTENT.NSP")),
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			SelectionRange: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Data: protocol.LSPAny("invalid json data"),
		}

		badParams := protocol.CallHierarchyOutgoingCallsParams{
			Item: badItem,
		}

		// Should not panic and should return empty
		badResult, badErr := provideOutgoingCalls(hctx, badParams)

		// No error expected
		if badErr != nil {
			t.Errorf("provideOutgoingCalls on garbage Item returned error: %v (expected nil)", badErr)
		}

		// Should be empty
		if len(badResult) != 0 {
			t.Errorf("provideOutgoingCalls on garbage Item returned %d results, want 0", len(badResult))
		}
	})
}

// Helper function to extract callee names from outgoing call results
func getCalleeNames(result []protocol.CallHierarchyOutgoingCall) map[string]int {
	names := make(map[string]int)
	for _, call := range result {
		names[call.To.Name]++
	}
	return names
}

// TestProvideOutgoingCalls_InlinePerform tests inline PERFORM as an outgoing call to a same-object
// subroutine (Feature 18, T6a, Story 3, AC4).
//
// FR-ID: FR-49, AC4 — "When an outgoing EdgePerforms edge resolves to a subroutine in the same
// object, emit a CallHierarchyOutgoingCall whose To is the matching SymbolSubroutine child's item
// (its SelectionRange, Kind=Function, same URI), not the object root."
func TestProvideOutgoingCalls_InlinePerform(t *testing.T) {
	testdata := filepath.Join("testdata", "callhierarchy")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"CALLER.NSP", "CALLEE.NSN", "PGM.NSP", "CC.NSC"}
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

	// Read CALLER.NSP to build the source item
	callerAbsPath := filepath.Join(root, testdata, "CALLER.NSP")
	callerContent, err := os.ReadFile(callerAbsPath)
	if err != nil {
		t.Fatalf("failed to read CALLER.NSP: %v", callerAbsPath)
	}

	callerAnalysis, ok := idx.Get("testdata/callhierarchy/CALLER.NSP")
	if !ok {
		t.Fatalf("CALLER.NSP not in index")
	}

	if callerAnalysis.Structure == nil {
		t.Fatalf("CALLER.NSP Structure is nil")
	}

	// Build the source item from CALLER's object root
	sourceItem := buildCallHierarchyItem(root, "testdata/callhierarchy/CALLER.NSP", callerContent, callerAnalysis.Structure, protocol.PositionEncodingKindUTF8)

	// Create handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
	}

	// Call provideOutgoingCalls
	params := protocol.CallHierarchyOutgoingCallsParams{
		Item: sourceItem,
	}

	result, err := provideOutgoingCalls(hctx, params)

	// Check error
	if err != nil {
		t.Errorf("provideOutgoingCalls returned error: %v", err)
	}

	// Sub-test: inline PERFORM should appear as an outgoing call (AC4)
	t.Run("AC4: inline PERFORM → same-object subroutine item", func(t *testing.T) {
		// Find the SUB-A item in the result
		var subAItem *protocol.CallHierarchyItem
		for i := range result {
			if strings.EqualFold(result[i].To.Name, "SUB-A") {
				subAItem = &result[i].To
				break
			}
		}

		// Assert SUB-A was found
		if subAItem == nil {
			t.Errorf("SUB-A not found in outgoing calls; found: %v", getCalleeNames(result))
		} else {
			// Assert SUB-A's properties
			if subAItem.Name != "SUB-A" {
				t.Errorf("SUB-A.Name = %q, want %q", subAItem.Name, "SUB-A")
			}

			// Assert Kind is Function (subroutine, not object/module)
			if subAItem.Kind != protocol.SymbolKindFunction {
				t.Errorf("SUB-A.Kind = %v, want %v (Function for subroutine)", subAItem.Kind, protocol.SymbolKindFunction)
			}

			// Assert URI points to CALLER.NSP (same file)
			if !strings.Contains(string(subAItem.URI), "CALLER.NSP") {
				t.Errorf("SUB-A.URI = %q, should contain CALLER.NSP (same file)", subAItem.URI)
			}

			// Assert SelectionRange is not zero (should be the subroutine's name range, not object root)
			if isZeroRange(model.Range{
				Start: model.Position{
					Line:   int(subAItem.SelectionRange.Start.Line),
					Column: int(subAItem.SelectionRange.Start.Character),
				},
				End: model.Position{
					Line:   int(subAItem.SelectionRange.End.Line),
					Column: int(subAItem.SelectionRange.End.Character),
				},
			}) {
				t.Errorf("SUB-A.SelectionRange is zero-range; expected non-zero subroutine-name range")
			}

			// Assert SelectionRange is distinct from the object root's range
			if subAItem.SelectionRange == sourceItem.SelectionRange {
				t.Errorf("SUB-A.SelectionRange equals object root SelectionRange; should point at subroutine's name")
			}

			// Assert this outgoing call has a fromRange (the PERFORM SUB-A statement)
			// Find the outgoing call for SUB-A
			var subAOutgoing *protocol.CallHierarchyOutgoingCall
			for i := range result {
				if strings.EqualFold(result[i].To.Name, "SUB-A") {
					subAOutgoing = &result[i]
					break
				}
			}
			if subAOutgoing == nil {
				t.Errorf("SUB-A outgoing call not found")
			} else {
				if len(subAOutgoing.FromRanges) == 0 {
					t.Errorf("SUB-A outgoing call has no fromRanges (PERFORM SUB-A site)")
				}
			}
		}
	})

	// Sub-test: inline PERFORM (SUB-A) should be distinct from external callees
	t.Run("AC4: inline PERFORM distinct from external callees", func(t *testing.T) {
		// Collect all callee names
		calleeNames := getCalleeNames(result)

		// Assert we have CALLEE (external subprogram)
		if calleeNames["CALLEE"] < 1 {
			t.Errorf("CALLEE not found in outgoing calls; expected static CALLNAT 'CALLEE'")
		}

		// Assert we have PGM (external program)
		if calleeNames["PGM"] < 1 {
			t.Errorf("PGM not found in outgoing calls; expected static FETCH 'PGM'")
		}

		// Assert we have SUB-A (inline subroutine)
		if calleeNames["SUB-A"] < 1 {
			t.Errorf("SUB-A not found in outgoing calls; expected inline PERFORM SUB-A")
		}

		// Verify these are distinct items (at least 3 items in result, or grouped correctly)
		distinctCallees := len(calleeNames)
		if distinctCallees < 3 {
			t.Errorf("expected at least 3 distinct callees (CALLEE, PGM, SUB-A), got %d", distinctCallees)
		}
	})
}
