package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/model"
)

// provideWorkspaceSymbols handles the workspace/symbol search (feature 10, T13).
// It walks every indexed file's Structure tree, matching program/subprogram objects
// and subroutines by name case-insensitively against the query.
//
// Scope (decision #4): returns only the object root (SymbolObject) and its direct
// SymbolSubroutine children, NOT data-sections, data-fields, maps, or DDM refs.
//
// Matching (decision #6): matches case-insensitively using strings.Contains on
// uppercased symbol name and query. An empty query matches ALL in-scope symbols.
//
// Location: builds a file:// URI for each match and converts the SelectionRange
// using toProtocolRange with the file's content. Nil-safe: missing file/content → skip.
//
// Returns results in deterministic order: sorted by Name, then URI.
func provideWorkspaceSymbols(hctx *handlerContext, query string) []protocol.SymbolInformation {
	// Guard: hctx must be initialized
	if hctx == nil || hctx.idx == nil {
		return nil
	}

	var results []protocol.SymbolInformation

	// Walk every indexed file
	hctx.idx.ForEach(func(relPath string, fa model.FileAnalysis) {
		// Skip if no Structure (object root)
		if fa.Structure == nil {
			return
		}

		// Read the file content for range conversion
		absPath := filepath.Join(hctx.root, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			// Can't read file; skip (FR-43)
			return
		}

		contentStr := string(content)

		// Determine the protocol kind for the object root
		// SymbolObject → SymbolKindModule (per test assertion)
		objectKind := protocol.SymbolKindModule

		// Check if object root matches the query
		upperObjName := strings.ToUpper(fa.Structure.Name)
		upperQuery := strings.ToUpper(query)
		objMatches := query == "" || strings.Contains(upperObjName, upperQuery)

		if objMatches {
			// Build Location for the object root
			fileURI := uri.File(absPath)
			symbolRange := toProtocolRange(fa.Structure.SelectionRange, contentStr, hctx.posEncoding)
			results = append(results, protocol.SymbolInformation{
				BaseSymbolInformation: protocol.BaseSymbolInformation{
					Name: fa.Structure.Name,
					Kind: objectKind,
				},
				Location: protocol.Location{URI: fileURI, Range: symbolRange},
			})
		}

		// Check subroutine children (direct SymbolSubroutine children only)
		for _, child := range fa.Structure.Children {
			if child.Kind == model.SymbolSubroutine {
				upperChildName := strings.ToUpper(child.Name)
				childMatches := query == "" || strings.Contains(upperChildName, upperQuery)

				if childMatches {
					// Subroutine → SymbolKindFunction (per test assertion)
					fileURI := uri.File(absPath)
					symbolRange := toProtocolRange(child.SelectionRange, contentStr, hctx.posEncoding)
					results = append(results, protocol.SymbolInformation{
						BaseSymbolInformation: protocol.BaseSymbolInformation{
							Name: child.Name,
							Kind: protocol.SymbolKindFunction,
						},
						Location: protocol.Location{URI: fileURI, Range: symbolRange},
					})
				}
			}
		}
	})

	// Sort results by Name, then URI for deterministic order
	slices.SortFunc(results, func(a, b protocol.SymbolInformation) int {
		// First sort by name (case-insensitive)
		aName := strings.ToLower(a.BaseSymbolInformation.Name)
		bName := strings.ToLower(b.BaseSymbolInformation.Name)
		if aName != bName {
			if aName < bName {
				return -1
			}
			return 1
		}
		// Same name: sort by URI
		aURI := string(a.Location.URI)
		bURI := string(b.Location.URI)
		if aURI < bURI {
			return -1
		} else if aURI > bURI {
			return 1
		}
		return 0
	})

	return results
}
