package server

import (
	"os"
	"path/filepath"
	"strings"

	gojson "github.com/go-json-experiment/json"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// callHierarchyItemData is the serializable identity packed into CallHierarchyItem.Data
// at prepare time, and decoded when the client echoes the item back on incoming/outgoing.
// It allows resolveItemIdentity to re-locate the symbol efficiently (feature 18, T2).
type callHierarchyItemData struct {
	Path string           `json:"path"`
	Name string           `json:"name"`
	Kind model.SymbolKind `json:"kind"`
}

// buildCallHierarchyItem builds a CallHierarchyItem from a model.Symbol (feature 18, T2).
//
// Given a symbol (an object root or a subroutine), it constructs an item carrying:
//   - Name = sym.Name
//   - Kind = modelSymbolKindToProtocol(sym.Kind)
//   - URI = file:///absolute/path
//   - Range/SelectionRange = converted via toProtocolRange
//   - Data = mustLSPAny(callHierarchyItemData{Path, Name, Kind})
//
// This is a pure function: no I/O, no locks. The relPath is workspace-relative
// (forward-slash convention, as used in the index).
func buildCallHierarchyItem(root, relPath string, sym *model.Symbol, enc protocol.PositionEncodingKind) protocol.CallHierarchyItem {
	// Build the absolute file path
	absPath := filepath.Join(root, relPath)

	// Map symbol kind to protocol kind
	kind := modelSymbolKindToProtocol(sym.Kind)

	// Build the Data: encode the identity for later decode/locate
	data := callHierarchyItemData{
		Path: relPath,
		Name: sym.Name,
		Kind: sym.Kind,
	}

	// We need content to convert ranges, but ranges don't need actual content
	// for pure position conversion when they're zero-width; buildCallHierarchyItem
	// must work with symbol ranges which may not have content available.
	// Use empty string for content since we're not decoding multi-byte chars.
	emptyContent := ""

	return protocol.CallHierarchyItem{
		Name:           sym.Name,
		Kind:           kind,
		URI:            uri.File(absPath),
		Range:          toProtocolRange(sym.Range, emptyContent, enc),
		SelectionRange: toProtocolRange(sym.SelectionRange, emptyContent, enc),
		Data:           mustLSPAny(data),
	}
}

// resolveItemIdentity recovers a symbol from a client-echoed CallHierarchyItem (feature 18, T2).
//
// Primary path: decode item.Data via gojson.Unmarshal, then idx.Get(data.Path),
// and match data.Name/data.Kind against the file's Structure (object root or a
// SymbolSubroutine child, case-insensitive per Natural).
//
// Fallback (FR-43): if Data is empty/undecodable, use item.URI → uriToRelPath → idx.Get,
// and match the object root or a subroutine child whose SelectionRange (converted via
// toProtocolRange) equals item.SelectionRange.
//
// Returns (relPath, FileAnalysis, *Symbol, ok). Returns ok=false (never panics) on:
// garbage/undecodable Data with no usable URI, unknown path, out-of-root URI,
// nil Structure, no matching symbol.
func resolveItemIdentity(idx *workspace.Index, root string, item protocol.CallHierarchyItem, enc protocol.PositionEncodingKind) (relPath string, fa model.FileAnalysis, sym *model.Symbol, ok bool) {
	// Primary path: try to decode Data
	if !isLSPAnyEmpty(item.Data) {
		var data callHierarchyItemData
		if err := gojson.Unmarshal([]byte(item.Data), &data); err == nil {
			// Successfully decoded; try to locate the symbol
			if fileAnalysis, ok := idx.Get(data.Path); ok && fileAnalysis.Structure != nil {
				// Try to match the symbol: object root first, then subroutines
				if strings.EqualFold(fileAnalysis.Structure.Name, data.Name) && fileAnalysis.Structure.Kind == data.Kind {
					return data.Path, fileAnalysis, fileAnalysis.Structure, true
				}

				// Search in children for a matching subroutine
				if sym := findSymbolInChildren(fileAnalysis.Structure.Children, data.Name, data.Kind); sym != nil {
					return data.Path, fileAnalysis, sym, true
				}
			}
		}
	}

	// Fallback: use item.URI + item.SelectionRange
	absPath, relPath, err := uriToRelPath(root, item.URI)
	if err != nil {
		// URI is out-of-root or invalid
		return "", model.FileAnalysis{}, nil, false
	}

	fileAnalysis, ok := idx.Get(relPath)
	if !ok || fileAnalysis.Structure == nil {
		return "", model.FileAnalysis{}, nil, false
	}

	// Read the file to get content for range conversion
	content, err := readFileIfNeeded(absPath)
	if err != nil {
		// Can't read file; try matching by structure alone (single object root fallback)
		// If there's only an object root and the selection ranges would match, return it
		sym := fileAnalysis.Structure
		if sym != nil {
			return relPath, fileAnalysis, sym, true
		}
		return "", model.FileAnalysis{}, nil, false
	}

	// Try to match the object root by SelectionRange
	if sym := fileAnalysis.Structure; sym != nil {
		symSelRange := toProtocolRange(sym.SelectionRange, content, enc)
		if rangesEqual(symSelRange, item.SelectionRange) {
			return relPath, fileAnalysis, sym, true
		}
	}

	// Try to match a subroutine child by SelectionRange
	for i := range fileAnalysis.Structure.Children {
		child := &fileAnalysis.Structure.Children[i]
		childSelRange := toProtocolRange(child.SelectionRange, content, enc)
		if rangesEqual(childSelRange, item.SelectionRange) {
			return relPath, fileAnalysis, child, true
		}
	}

	// No match found
	return "", model.FileAnalysis{}, nil, false
}

// isLSPAnyEmpty checks if an LSPAny value is effectively empty.
func isLSPAnyEmpty(val protocol.LSPAny) bool {
	if len(val) == 0 {
		return true
	}
	// Check for protocol-level empty values like "null" or "{}"
	str := string(val)
	return str == "null" || str == "{}"
}

// findSymbolInChildren searches the children slice for a symbol matching the given name and kind.
// Returns the first match (case-insensitive for name), or nil if not found.
func findSymbolInChildren(children []model.Symbol, name string, kind model.SymbolKind) *model.Symbol {
	for i := range children {
		if children[i].Kind == kind && strings.EqualFold(children[i].Name, name) {
			return &children[i]
		}
	}
	return nil
}

// rangesEqual checks if two protocol ranges are equal.
func rangesEqual(r1, r2 protocol.Range) bool {
	return r1.Start.Line == r2.Start.Line &&
		r1.Start.Character == r2.Start.Character &&
		r1.End.Line == r2.End.Line &&
		r1.End.Character == r2.End.Character
}

// readFileIfNeeded reads a file at the given absolute path.
// Returns empty string and nil error on failure (graceful degradation, FR-43).
func readFileIfNeeded(absPath string) (string, error) {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// providePrepareCallHierarchy (feature 18, T1) is a stub provider that returns
// an empty array for the textDocument/prepareCallHierarchy request.
func providePrepareCallHierarchy(hctx *handlerContext, params protocol.CallHierarchyPrepareParams) ([]protocol.CallHierarchyItem, error) {
	return nil, nil
}

// provideIncomingCalls (feature 18, T1) is a stub provider that returns
// an empty array for the callHierarchy/incomingCalls request.
func provideIncomingCalls(hctx *handlerContext, params protocol.CallHierarchyIncomingCallsParams) ([]protocol.CallHierarchyIncomingCall, error) {
	return nil, nil
}

// provideOutgoingCalls (feature 18, T1) is a stub provider that returns
// an empty array for the callHierarchy/outgoingCalls request.
func provideOutgoingCalls(hctx *handlerContext, params protocol.CallHierarchyOutgoingCallsParams) ([]protocol.CallHierarchyOutgoingCall, error) {
	return nil, nil
}
