package server

import (
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
)

// provideDefinition handles the textDocument/definition request (feature 10, T7).
// It is the LSP provider entry point: it decodes the cursor position from the params,
// looks up any reference at that position, resolves it to a definition, and returns
// the target location(s).
//
// For a resolved edge:
// - If the target is in the same file (inline PERFORM), use the subroutine's SelectionRange.
// - Otherwise, use the target file's object-root Structure.SelectionRange.
// For unresolved or dynamic targets, returns empty (no error — FR-17, FR-43).
//
// Concurrency (F7): holds the read lock on idxResMu to ensure consistent access to idx/res
// (applyDocumentChange swaps these pointers under the write lock, so handlers see
// a stable snapshot for the duration of a request).
func provideDefinition(hctx *handlerContext, params protocol.DefinitionParams) ([]protocol.Location, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Acquire read lock to read idx/res safely (applyDocumentChange holds write lock when updating)
	hctx.idxResMu.RLock()
	idx, res := hctx.idx, hctx.res
	hctx.idxResMu.RUnlock()

	if idx == nil || res == nil {
		return nil, nil
	}

	// Convert LSP URI to workspace-relative path (forward-slash index key convention)
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root — no definition
		return nil, nil
	}

	// Get the source file's analysis from the index
	sourceFA, ok := idx.Get(relPath)
	if !ok {
		// Source file not in index — no definition
		return nil, nil
	}

	// Read the source file content for position conversion
	sourceContent, err := os.ReadFile(absPath)
	if err != nil {
		// Can't read source; no definition
		return nil, nil
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), hctx.posEncoding)

	// Find the edge (or data-access) at the cursor position
	edge, _ := findCursorTarget(sourceFA, cursorPos)
	if edge == nil {
		// No edge at cursor position — no definition
		return nil, nil
	}

	// Look up the resolution for this edge
	resolution, ok := res.Get(relPath, edge.Source)
	if !ok {
		// Edge not found in resolution set — no definition
		return nil, nil
	}

	// Handle resolved case: single definition
	if resolution.IsResolved() {
		// Resolution succeeded; read the target file's analysis
		targetFA, ok := idx.Get(resolution.Path)
		if !ok {
			// Target file not in index (shouldn't happen after successful resolution)
			return nil, nil
		}

		// Read the target file content for range conversion
		targetAbsPath := filepath.Join(hctx.root, resolution.Path)
		targetContent, err := os.ReadFile(targetAbsPath)
		if err != nil {
			// Can't read target file — no definition
			return nil, nil
		}

		// Handle inline PERFORM (target in same file): use the subroutine's SelectionRange
		// Normalize both paths for comparison (canonical index keyspace)
		normalizedResPath := paths.NormalizeKey(resolution.Path)
		if strings.EqualFold(normalizedResPath, relPath) {
			// Same file: find the matching subroutine in Structure.Children
			if targetFA.Structure != nil && targetFA.Structure.Children != nil {
				targetName := strings.ToUpper(edge.TargetName)
				for _, child := range targetFA.Structure.Children {
					if child.Kind == model.SymbolSubroutine && strings.EqualFold(child.Name, targetName) {
						// Found the inline subroutine; use its SelectionRange
						loc := protocol.Location{
							URI:   uri.File(targetAbsPath),
							Range: toProtocolRange(child.SelectionRange, string(targetContent), hctx.posEncoding),
						}
						return []protocol.Location{loc}, nil
					}
				}
			}
			// Fallback: use object root
		}

		// External target: use the object-root Structure.SelectionRange
		loc := definitionLocation(hctx.root, resolution.Path, targetFA, string(targetContent), hctx.posEncoding)
		return []protocol.Location{loc}, nil
	}

	// Handle ambiguous case: multiple candidates
	if resolution.IsAmbiguous() {
		locations := make([]protocol.Location, 0, len(resolution.Candidates))
		for _, candidatePath := range resolution.Candidates {
			// Fetch the candidate file's analysis
			candidateFA, ok := idx.Get(candidatePath)
			if !ok {
				// Candidate not in index; skip (defensive, FR-43)
				continue
			}

			// Read the candidate file content
			candidateAbsPath := filepath.Join(hctx.root, candidatePath)
			candidateContent, err := os.ReadFile(candidateAbsPath)
			if err != nil {
				// Can't read candidate file; skip (defensive)
				continue
			}

			// Build the Location for this candidate (object-root range)
			loc := definitionLocation(hctx.root, candidatePath, candidateFA, string(candidateContent), hctx.posEncoding)
			locations = append(locations, loc)
		}
		if len(locations) > 0 {
			return locations, nil
		}
		// If no locations could be built, fall through to return empty
	}

	// Unresolved case (dynamic or no-target): return empty (FR-17)
	return nil, nil
}

// uriToRelPath converts an LSP file URI to an (absPath, relPath) pair relative
// to the workspace root. relPath is canonicalized to the index keyspace
// (forward-slash separators) via paths.NormalizeKey — the single source of
// truth for the canonical form — so lookups match keys built on any OS.
// Returns a non-nil error if the URI is outside the workspace root.
func uriToRelPath(root string, fileURI uri.URI) (absPath, relPath string, err error) {
	absPath = fileURI.FsPath()
	relPath, err = filepath.Rel(root, absPath)
	if err != nil {
		return "", "", err
	}
	relPath = paths.NormalizeKey(relPath)
	return absPath, relPath, nil
}

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
