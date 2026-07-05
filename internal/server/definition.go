package server

import (
	"path/filepath"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/model"
)

// definitionLocation builds a protocol.Location for a resolved definition.
//
// Given:
//   - root: workspace root (absolute path)
//   - relPath: target file path relative to workspace root
//   - fa: the target file's FileAnalysis (containing Structure for the object root)
//   - content: the target file's source content
//   - enc: the negotiated PositionEncodingKind
//
// Returns a Location with:
//   - URI: the target file as a file:// URI
//   - Range: the Structure.SelectionRange (the object name span) in protocol coords,
//     or {0,0}→{0,0} (the zero-width fallback) if Structure is nil (FR-43).
func definitionLocation(root, relPath string, fa model.FileAnalysis, content string, enc protocol.PositionEncodingKind) protocol.Location {
	// Construct the absolute path and convert to file:// URI
	absPath := filepath.Join(root, relPath)
	fileURI := uri.File(absPath)

	// Determine the Range: use Structure.SelectionRange if available,
	// otherwise fall back to a zero-width range at {1,1}→{1,1} in model coords,
	// which converts to {0,0}→{0,0} in protocol coords.
	var rng protocol.Range
	if fa.Structure != nil {
		rng = toProtocolRange(fa.Structure.SelectionRange, content, enc)
	} else {
		fallbackRange := model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 1, Column: 1},
		}
		rng = toProtocolRange(fallbackRange, content, enc)
	}

	return protocol.Location{
		URI:   fileURI,
		Range: rng,
	}
}
