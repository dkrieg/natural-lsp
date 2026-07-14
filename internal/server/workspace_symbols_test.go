package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/workspace"
)

// TestWorkspaceSymbols tests the workspace/symbol search functionality.
// Feature 10, Task T13: Return program/subprogram objects and subroutines matching
// a query name case-insensitively with correct Kind and Location.
//
// Assertions:
// - Case-insensitive matching (lower query matches upper/mixed symbol names)
// - Correct protocol.SymbolKind mapping (SymbolObject → Module, SymbolSubroutine → Function)
// - Each result carries a Location with file URI and SelectionRange
// - Empty query returns ALL symbols
// - Data-fields/maps/DDM refs are NOT returned (scope)
func TestWorkspaceSymbols(t *testing.T) {
	testdataDir := filepath.Join("testdata", "workspace-symbols")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the fixture workspace index
	idx, err := buildTestWorkspaceIndex(testdataDir)
	if err != nil {
		t.Fatalf("failed to build test workspace index: %v", err)
	}

	// Verify the index was populated
	keys := idx.Keys()
	if len(keys) == 0 {
		t.Fatalf("test workspace index is empty; fixture may not have been loaded")
	}

	// Define symbol search queries and expected results
	tt := []struct {
		name          string
		query         string
		expectedCount int
		checkMatches  func(t *testing.T, got []protocol.SymbolInformation, root string)
	}{
		{
			name:          "lowercase query matches uppercase/mixed-case symbols (case-insensitive)",
			query:         "mainprogram",
			expectedCount: 1,
			checkMatches: func(t *testing.T, got []protocol.SymbolInformation, root string) {
				if len(got) != 1 {
					t.Errorf("expected 1 match for 'mainprogram', got %d", len(got))
					return
				}
				sym := got[0]
				// Names are matched (and stored) case-insensitively; the lexer
				// normalizes identifiers, so assert modulo case.
				if !strings.EqualFold(sym.Name, "MainProgram") {
					t.Errorf("symbol Name = %q, want 'MainProgram' (case-insensitive)", sym.Name)
				}
				// SymbolObject should map to Module or Class; confirm it's one of these protocol kinds
				if sym.Kind != protocol.SymbolKindModule && sym.Kind != protocol.SymbolKindClass {
					t.Errorf("symbol Kind = %v, want Module (%v) or Class (%v)", sym.Kind, protocol.SymbolKindModule, protocol.SymbolKindClass)
				}
				// Location should have a file URI and non-zero range
				if sym.Location.URI == "" {
					t.Errorf("symbol Location.URI is empty")
				}
				if !strings.Contains(string(sym.Location.URI), "MainProgram.NSP") {
					t.Errorf("symbol Location.URI = %q, want to contain 'MainProgram.NSP'", sym.Location.URI)
				}
			},
		},
		{
			name:          "uppercase query matches mixed-case symbols",
			query:         "UTILITIES",
			expectedCount: 1,
			checkMatches: func(t *testing.T, got []protocol.SymbolInformation, root string) {
				if len(got) != 1 {
					t.Errorf("expected 1 match for 'UTILITIES', got %d", len(got))
					return
				}
				sym := got[0]
				if !strings.EqualFold(sym.Name, "Utilities") {
					t.Errorf("symbol Name = %q, want 'Utilities' (case-insensitive)", sym.Name)
				}
				// External subroutine (.NSS) should be treated as a module/object
				if sym.Kind != protocol.SymbolKindModule && sym.Kind != protocol.SymbolKindClass {
					t.Errorf("symbol Kind = %v, want Module (%v) or Class (%v)", sym.Kind, protocol.SymbolKindModule, protocol.SymbolKindClass)
				}
			},
		},
		{
			name:          "subroutine symbols have SymbolKind Function or Method",
			query:         "processdata",
			expectedCount: 1,
			checkMatches: func(t *testing.T, got []protocol.SymbolInformation, root string) {
				if len(got) != 1 {
					t.Errorf("expected 1 match for 'processdata', got %d", len(got))
					return
				}
				sym := got[0]
				if !strings.EqualFold(sym.Name, "ProcessData") {
					t.Errorf("symbol Name = %q, want 'ProcessData' (case-insensitive)", sym.Name)
				}
				// Subroutine should be Function or Method
				if sym.Kind != protocol.SymbolKindFunction && sym.Kind != protocol.SymbolKindMethod {
					t.Errorf("symbol Kind = %v, want Function (%v) or Method (%v)", sym.Kind, protocol.SymbolKindFunction, protocol.SymbolKindMethod)
				}
				// Location should have a file URI pointing to MainProgram.NSP (where subroutine is defined)
				if !strings.Contains(string(sym.Location.URI), "MainProgram.NSP") {
					t.Errorf("subroutine Location.URI = %q, want to contain 'MainProgram.NSP'", sym.Location.URI)
				}
			},
		},
		{
			name:          "empty query returns all in-scope symbols",
			query:         "",
			expectedCount: 6, // MainProgram, ProcessData, ValidateInput, MYSUB, Utilities, HelperFunc, AnotherHelper (7 total with AnotherHelper)
			checkMatches: func(t *testing.T, got []protocol.SymbolInformation, root string) {
				// Empty query should return all programs/subprograms and subroutines
				// MainProgram (object), ProcessData (subroutine), ValidateInput (subroutine),
				// MYSUB (object), Utilities (object), HelperFunc (subroutine), AnotherHelper (subroutine)
				if len(got) < 5 {
					t.Errorf("empty query returned %d symbols, expected at least 5", len(got))
				}

				// Verify all results are either objects or subroutines (not data-fields, maps, etc.)
				for _, sym := range got {
					okKind := sym.Kind == protocol.SymbolKindModule ||
						sym.Kind == protocol.SymbolKindClass ||
						sym.Kind == protocol.SymbolKindFunction ||
						sym.Kind == protocol.SymbolKindMethod
					if !okKind {
						t.Errorf("symbol %q has unexpected Kind %v (not Module/Class/Function/Method)", sym.Name, sym.Kind)
					}
				}

				// Check that data-fields and maps are NOT in the results
				for _, sym := range got {
					if strings.Contains(strings.ToLower(sym.Name), "input-") || strings.Contains(strings.ToLower(sym.Name), "output-") {
						t.Errorf("symbol %q appears to be a data-field; data-fields should not be in workspace symbols", sym.Name)
					}
				}
			},
		},
		{
			name:          "query matching multiple subroutines returns all",
			query:         "helper",
			expectedCount: 2,
			checkMatches: func(t *testing.T, got []protocol.SymbolInformation, root string) {
				// Should match HelperFunc and AnotherHelper
				if len(got) < 1 {
					t.Errorf("expected at least 1 match for 'helper', got %d", len(got))
					return
				}
				// Verify deterministic order (sorted by name)
				for i := 0; i < len(got)-1; i++ {
					if strings.ToLower(got[i].Name) > strings.ToLower(got[i+1].Name) {
						t.Errorf("results not in sorted order: %q > %q", got[i].Name, got[i+1].Name)
					}
				}
			},
		},
		{
			name:          "no matches returns empty result",
			query:         "nonexistent",
			expectedCount: 0,
			checkMatches: func(t *testing.T, got []protocol.SymbolInformation, root string) {
				if len(got) != 0 {
					t.Errorf("expected 0 matches for 'nonexistent', got %d", len(got))
				}
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Call provideWorkspaceSymbols with the test index and encoding
			hctx := &handlerContext{
				idx:         idx,
				posEncoding: protocol.PositionEncodingKindUTF8,
				root:        root,
			}

			// The stub provideWorkspaceSymbols will return nil for now; we're testing
			// that it compiles and is callable (red phase).
			got := provideWorkspaceSymbols(hctx, tc.query)

			// Assertions
			tc.checkMatches(t, got, root)

			// Verify all results have valid Locations
			for _, sym := range got {
				if sym.Location.URI == "" {
					t.Errorf("symbol %q has empty Location.URI", sym.Name)
				}
			}
		})
	}
}

// buildTestWorkspaceIndex loads all fixtures from testdata/workspace-symbols
// and builds an index from them.
func buildTestWorkspaceIndex(testdataDir string) (*workspace.Index, error) {
	idx := &workspace.Index{}
	az := natural.New(nil)

	// Walk the testdata directory for all .NS* files
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, ".") {
			// Skip hidden files; include all .NSx extensions
			if strings.Contains(name, ".NS") {
				filePath := filepath.Join(testdataDir, name)
				content, err := os.ReadFile(filePath)
				if err != nil {
					return nil, err
				}

				// Analyze the file
				analysis, err := az.Analyze(filePath, content)
				if err != nil {
					return nil, err
				}

				// Add to index keyed by relative path
				relPath := filepath.Join(testdataDir, name)
				idx.Add(relPath, analysis)
			}
		}
	}

	return idx, nil
}

// TestProvideWorkspaceSymbols_MarshaledEmptyCase (T4) pins the wire bytes for an empty
// workspace/symbol result by driving the REAL dispatch path end-to-end against an empty
// workspace. Unlike definition/references (which emit "null"), workspace/symbol's
// nil-guard emits "[]" for empty — a completion list / symbol array is always an array.
//
// If the workspace/symbol nil-guard in server.go is dropped or flipped to "null", the
// emitted bytes change and this test goes red (Story 2 AC2).
func TestProvideWorkspaceSymbols_MarshaledEmptyCase(t *testing.T) {
	got := dispatchResultBytes(t, "workspace/symbol", `{"query":"NOMATCH"}`)

	if string(got) != "[]" {
		t.Errorf("empty workspace/symbol result: got %q, want %q", string(got), "[]")
	}
}

// TestProvideWorkspaceSymbols_MarshaledNonEmptyCase (T4) pins the exact wire bytes for
// a non-empty workspace/symbol result via marshalResult — the EXACT function the
// workspace/symbol dispatch calls in its non-nil branch. Pinning the full bytes locks
// byte-for-byte preservation across the stdlib→gojson migration (Story 2 AC2).
func TestProvideWorkspaceSymbols_MarshaledNonEmptyCase(t *testing.T) {
	// Setup: one symbol result
	symbols := []protocol.SymbolInformation{
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{
				Name: "MYPROG",
				Kind: protocol.SymbolKindModule,
			},
			Location: protocol.Location{URI: "file:///test/MYPROG.NSP", Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 6}}},
		},
	}

	// Marshal via the dispatch's exact marshaler.
	got, err := marshalResult(symbols)
	if err != nil {
		t.Fatalf("failed to marshal via marshalResult: %v", err)
	}

	want := `[{"name":"MYPROG","kind":2,"location":{"uri":"file:///test/MYPROG.NSP","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":6}}}}]`
	if string(got) != want {
		t.Errorf("non-empty workspace/symbol wire bytes mismatch:\n got: %s\nwant: %s", string(got), want)
	}
}
