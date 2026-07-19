package server

import (
	"path/filepath"
	"slices"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
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
//
// Concurrency (F7): holds the read lock on idxResMu to ensure consistent access to idx
// (applyDocumentChange swaps the index pointer under the write lock, so handlers see
// a stable snapshot for the duration of a request).
func provideWorkspaceSymbols(hctx *handlerContext, query string) []protocol.SymbolInformation {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil
	}

	// Acquire read lock to read idx safely (applyDocumentChange holds write lock when updating)
	hctx.idxResMu.RLock()
	idx := hctx.idx
	hctx.idxResMu.RUnlock()

	if idx == nil {
		return nil
	}

	var results []protocol.SymbolInformation

	// Walk every indexed file (idx is stable for the duration of this function).
	// Range conversion uses the in-memory line-width table (feature 22 T8) via
	// the converter ForEachWithRange hands each callback, so there is NO
	// per-query disk read here — the previous os.ReadFile sweep over every
	// indexed file is gone.
	idx.ForEachWithRange(func(relPath string, fa model.FileAnalysis, toRange rangeConverter) {
		// Skip if no Structure (object root)
		if fa.Structure == nil {
			return
		}

		absPath := filepath.Join(hctx.root, relPath)

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
			symbolRange := protocolRangeVia(toRange, fa.Structure.SelectionRange, hctx.posEncoding)
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
					symbolRange := protocolRangeVia(toRange, child.SelectionRange, hctx.posEncoding)
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
