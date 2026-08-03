package server

import (
	"os"
	"path/filepath"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// provideTypeDefinition handles the textDocument/typeDefinition request (feature 31, T3).
// For a VIEW OF field, it resolves to the DDM field's NameRange in the .NSD file via the steplib chain.
// For non-VIEW fields (scalar-only variables), returns nil (FR-17, FR-43).
//
// This is the minimal implementation: it uses findDeclarationTarget to map the cursor to
// a declaration, then checks if the target is a VIEW field (OwningView.ViewOfDDM != "").
// If so, it resolves the DDM field via workspace.ResolveDDMFieldLocation and returns
// the Location of the DDM field's NameRange. Otherwise, it returns nil.
//
// Store-first buffer reads are inherited from the caller's setup (uriToRelPath, F7 RLock snapshot).
func provideTypeDefinition(hctx *handlerContext, params protocol.TypeDefinitionParams) ([]protocol.Location, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Ensure hctx.root is absolute for proper path calculations
	root := hctx.root
	if !filepath.IsAbs(root) {
		if absRoot, err := filepath.Abs(root); err == nil {
			root = absRoot
		}
	}

	// Convert LSP URI to workspace-relative path (forward-slash index key convention)
	absPath, relPath, err := uriToRelPath(root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root — no type definition
		return nil, nil
	}

	// Acquire read lock to read idx safely (applyDocumentChange holds write lock when updating)
	hctx.idxResMu.RLock()
	idx := hctx.idx
	hctx.idxResMu.RUnlock()

	// Get the source file's analysis (store-first for consistency)
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
		// Pre-publish graceful degradation: if the index isn't ready yet,
		// we can't serve disk files. Return nil to degrade gracefully per FR-43.
		if idx == nil {
			return nil, nil
		}

		var ok bool
		sourceFA, ok = idx.Get(relPath)
		if !ok {
			// Source file not in index — no type definition
			return nil, nil
		}

		// Read the source file content for position conversion
		var readErr error
		sourceContent, readErr = os.ReadFile(absPath)
		if readErr != nil {
			// Can't read source; no type definition
			return nil, nil
		}
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), hctx.posEncoding)

	// Map the cursor to a declaration target (data-field or VIEW OF node)
	declTarget := findDeclarationTarget(sourceFA, cursorPos)
	if declTarget == nil {
		// No declaration at cursor — no type definition
		return nil, nil
	}

	// Check if this is a VIEW field (within a view context with ViewOfDDM set)
	if declTarget.OwningView == nil || declTarget.OwningView.ViewOfDDM == "" {
		// Not a VIEW field; scalar-only or unrelated — return nil (FR-17, FR-43)
		return nil, nil
	}

	// This is a VIEW field; resolve it to the DDM field via steplib chain
	if idx == nil {
		// Index not ready; graceful degradation (FR-43)
		return nil, nil
	}

	// Resolve the DDM field location via the steplib chain
	ddmFieldRange, ddmPath := workspace.ResolveDDMFieldLocation(
		declTarget.Symbol.Name,
		declTarget.OwningView.ViewOfDDM,
		idx,
		relPath,
		&hctx.cfg,
	)

	if ddmFieldRange == (model.Range{}) || ddmPath == "" {
		// DDM unresolved, TYPE: SQL, or field absent → return nil (FR-17, FR-43)
		return nil, nil
	}

	// Read the DDM file content for range conversion.
	// Use the same abs-normalized root local as uriToRelPath above for consistency
	// (hctx.root is already absolute post-bootstrap, so behavior is unchanged).
	ddmAbsPath := filepath.Join(root, ddmPath)
	ddmContent, err := os.ReadFile(ddmAbsPath)
	if err != nil {
		// Can't read DDM file — graceful degradation
		return nil, nil
	}

	// Convert the field range to protocol coordinates
	loc := protocol.Location{
		URI:   uri.File(ddmAbsPath),
		Range: toProtocolRange(ddmFieldRange, string(ddmContent), hctx.posEncoding),
	}
	return []protocol.Location{loc}, nil
}
