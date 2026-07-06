package server

import (
	"os"

	"go.lsp.dev/protocol"

	"natural-lsp/internal/model"
)

// provideDocumentSymbols handles textDocument/documentSymbol requests (feature 11, FR-27).
// It returns the document outline (hierarchical Symbol tree) for the given URI.
//
// Resolution order:
//  1. Open-document store — if the document is open (possibly with unsaved edits), serve from
//     the store's current in-memory content and Structure. This differs from the feature-10
//     disk-first providers (definition/references) which go directly to the index; document
//     outline must reflect in-flight edits (Story 2).
//  2. Index + disk fallback — when the document is not open, snapshot idx under the read
//     lock (F7: applyDocumentChange swaps the pointer under the write lock), release the
//     lock, then read the file and convert the indexed Structure. The lock is released before
//     any I/O so reads cannot block in-progress index updates.
//
// Returns nil, nil (no error) when hctx is nil, the URI is not found, or Structure is absent
// (FR-43 graceful degradation — a file that failed extraction still returns nothing, not a panic).
func provideDocumentSymbols(hctx *handlerContext, params protocol.DocumentSymbolParams) ([]protocol.DocumentSymbol, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Resolution order 1: open-document store (current, unsaved edits — Story 2).
	doc, ok := hctx.store.Get(params.TextDocument.URI)
	if ok && doc != nil && doc.Analysis.Structure != nil {
		return []protocol.DocumentSymbol{
			symbolToDocumentSymbol(*doc.Analysis.Structure, string(doc.Content), hctx.posEncoding),
		}, nil
	}

	// Resolution order 2: index + disk (document not open).
	// Snapshot idx under read lock; release before any I/O (F7).
	hctx.idxResMu.RLock()
	idx := hctx.idx
	hctx.idxResMu.RUnlock()

	if idx == nil {
		return nil, nil
	}

	// Convert LSP URI to workspace-relative path (forward-slash index key convention)
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root
		return nil, nil
	}

	fa, ok := idx.Get(relPath)
	if !ok || fa.Structure == nil {
		return nil, nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		// Can't read file — FR-43
		return nil, nil
	}

	return []protocol.DocumentSymbol{
		symbolToDocumentSymbol(*fa.Structure, string(content), hctx.posEncoding),
	}, nil
}

// symbolToDocumentSymbol converts a model.Symbol tree to a protocol.DocumentSymbol,
// mapping SymbolKind constants to protocol.SymbolKind, converting Range and
// SelectionRange via toProtocolRange, and recursing into Children.
//
// Names are passed through verbatim from model.Symbol.Name; the extraction layer
// (structure.go) is responsible for normalizing names to the model's uppercase
// convention before they reach this converter.
//
// This is a pure function: no I/O, no locks, no handler state.
func symbolToDocumentSymbol(sym model.Symbol, content string, enc protocol.PositionEncodingKind) protocol.DocumentSymbol {
	kind := modelSymbolKindToProtocol(sym.Kind)
	protoRange := toProtocolRange(sym.Range, content, enc)
	protoSelectionRange := toProtocolRange(sym.SelectionRange, content, enc)
	children := symbolsToDocumentSymbols(sym.Children, content, enc)

	return protocol.DocumentSymbol{
		Name:           sym.Name,
		Kind:           kind,
		Range:          protoRange,
		SelectionRange: protoSelectionRange,
		Children:       children,
	}
}

// modelSymbolKindToProtocol maps a model.SymbolKind to the corresponding
// protocol.SymbolKind for the textDocument/documentSymbol response.
// Unrecognized kinds fall back to SymbolKindObject (FR-43 defensive default).
func modelSymbolKindToProtocol(k model.SymbolKind) protocol.SymbolKind {
	switch k {
	case model.SymbolObject:
		return protocol.SymbolKindModule
	case model.SymbolSubroutine:
		return protocol.SymbolKindFunction
	case model.SymbolMap:
		return protocol.SymbolKindObject
	case model.SymbolDataSection:
		return protocol.SymbolKindNamespace
	case model.SymbolDataField:
		return protocol.SymbolKindField
	case model.SymbolDDMReference:
		return protocol.SymbolKindStruct
	default:
		return protocol.SymbolKindObject
	}
}

// symbolsToDocumentSymbols converts a slice of model.Symbol to a slice of
// protocol.DocumentSymbol, preserving order.
func symbolsToDocumentSymbols(syms []model.Symbol, content string, enc protocol.PositionEncodingKind) []protocol.DocumentSymbol {
	if len(syms) == 0 {
		return nil
	}
	result := make([]protocol.DocumentSymbol, len(syms))
	for i, sym := range syms {
		result[i] = symbolToDocumentSymbol(sym, content, enc)
	}
	return result
}
