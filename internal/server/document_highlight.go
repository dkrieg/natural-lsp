package server

import (
	"os"
	"sort"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// provideDocumentHighlight handles the textDocument/documentHighlight request (FR-54, feature 27, T5).
// It returns all occurrences of the symbol under the cursor in the current file,
// with each occurrence as a DocumentHighlight entry containing a Range and a Kind.
//
// For variables (same-file):
//   - If the cursor is on a variable use-site or declaration, returns all same-file
//     occurrences of that variable with appropriate Kind (Read/Write/Text).
//   - Store-first: reads the open buffer first, falling back to disk/index.
//
// For call/subroutine names (same-file):
//   - If the cursor is on a CALLNAT/PERFORM/FETCH/RUN target name, returns that
//     target name's occurrences in the file.
//
// Modeled gaps (dynamic, unresolved, no-target) return empty (no error), per FR-17.
// Empty result returns [] (never null), matching list-provider conventions.
//
// Concurrency (F7): holds the read lock on idxResMu to ensure consistent access to idx/res.
func provideDocumentHighlight(hctx *handlerContext, params protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return []protocol.DocumentHighlight{}, nil
	}

	// Convert LSP URI to workspace-relative path
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root — no highlights
		return []protocol.DocumentHighlight{}, nil
	}

	// Get the source file's analysis (store-first for variables, like T3/T4)
	var sourceFA model.FileAnalysis
	var sourceContent []byte

	if hctx.store != nil {
		// Try store first (live buffer)
		doc, ok := hctx.store.Get(params.TextDocument.URI)
		if ok {
			sourceFA = doc.Analysis
			sourceContent = doc.Content
		}
	}

	// If not in store, fall back to index/disk
	if sourceContent == nil {
		// Acquire read lock to read idx/res safely
		hctx.idxResMu.RLock()
		idx := hctx.idx
		hctx.idxResMu.RUnlock()

		// Pre-publish graceful degradation: if the index isn't ready yet, return empty
		if idx == nil {
			return []protocol.DocumentHighlight{}, nil
		}

		var ok bool
		sourceFA, ok = idx.Get(relPath)
		if !ok {
			// Source file not in index — no highlights
			return []protocol.DocumentHighlight{}, nil
		}

		// Read the source file content for position conversion
		var readErr error
		sourceContent, readErr = os.ReadFile(absPath)
		if readErr != nil {
			// Can't read source; no highlights
			return []protocol.DocumentHighlight{}, nil
		}
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), hctx.posEncoding)

	// Find the reference site (edge, data-access, or variable) at the cursor position
	edge, _, varRef := findCursorTarget(sourceFA, cursorPos, string(sourceContent), hctx.az)

	// Extract variable refs on demand from the content (in-memory only, not persisted)
	var allVarRefs []model.VariableRef
	if hctx.az != nil {
		allVarRefs = hctx.az.ExtractVariableRefs(string(sourceContent))
	}

	// Handle variable reference case (feature 27 T5) — same-file only for Phase A
	if varRef != nil {
		locations := findVariableReferencesInFile(varRef, &sourceFA, allVarRefs, string(sourceContent), absPath, true, hctx.posEncoding)
		return locationsToHighlights(locations, string(sourceContent), hctx.posEncoding), nil
	}

	// Handle variable declaration case (feature 27 T5, idempotent):
	// When the cursor is on a declaration's NameRange, find all use-sites of that variable.
	if decl := findVariableDeclarationAtCursor(&sourceFA, cursorPos); decl != nil {
		// Create a synthetic VariableRef to pass to the reference finder
		varRef := model.VariableRef{
			Name:  decl.Name,
			Range: decl.NameRange,
		}
		locations := findVariableReferencesInFile(&varRef, &sourceFA, allVarRefs, string(sourceContent), absPath, true, hctx.posEncoding)
		return locationsToHighlights(locations, string(sourceContent), hctx.posEncoding), nil
	}

	// Handle edge (call/dependency) case — same-file highlights for the call target name
	if edge != nil {
		hctx.idxResMu.RLock()
		idx, res := hctx.idx, hctx.res
		hctx.idxResMu.RUnlock()

		if idx != nil && res != nil {
			// Check if the edge is resolved
			resolution, ok := res.Get(relPath, edge.Source)
			if ok && resolution.IsResolved() {
				// Get all reference sites for this target
				locations := referenceSites(idx, res, hctx.root, resolution.Path, edge.TargetName, resolution.Type, true, hctx.posEncoding)
				// Filter to only same-file locations
				var sameFileHighlights []protocol.DocumentHighlight
				fileURI := uri.File(absPath)
				for _, loc := range locations {
					if loc.URI == fileURI {
						sameFileHighlights = append(sameFileHighlights, protocol.DocumentHighlight{
							Range: loc.Range,
							Kind:  protocol.DocumentHighlightKindText, // Best-effort default
						})
					}
				}
				return sameFileHighlights, nil
			}
		}
	}

	// No reference, modeled gap, or unresolved — return empty
	return []protocol.DocumentHighlight{}, nil
}

// locationsToHighlights converts a list of Locations to DocumentHighlights.
// Each location is converted to a DocumentHighlight with a kind (defaulting to Text).
// This is used to transform the output of findVariableReferencesInFile into highlights.
func locationsToHighlights(locations []protocol.Location, content string, enc protocol.PositionEncodingKind) []protocol.DocumentHighlight {
	if len(locations) == 0 {
		return []protocol.DocumentHighlight{}
	}

	var highlights []protocol.DocumentHighlight
	for _, loc := range locations {
		highlight := protocol.DocumentHighlight{
			Range: loc.Range,
			Kind:  protocol.DocumentHighlightKindText, // Best-effort default; refine if direction info is available
		}
		highlights = append(highlights, highlight)
	}

	// Sort by line then column for determinism
	sort.Slice(highlights, func(i, j int) bool {
		if highlights[i].Range.Start.Line != highlights[j].Range.Start.Line {
			return highlights[i].Range.Start.Line < highlights[j].Range.Start.Line
		}
		return highlights[i].Range.Start.Character < highlights[j].Range.Start.Character
	})

	return highlights
}
