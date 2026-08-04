package server

import (
	"os"
	"path/filepath"
	"sort"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// provideDocumentLink handles the textDocument/documentLink request (feature 32).
// It returns a list of clickable document links for all resolved edges in the
// document (CALLNAT, INCLUDE, FETCH, RUN, external PERFORM targets).
//
// Each link points to the resolved target's file URI with no position (inherent
// to the LSP DocumentLink.Target contract). Same-file inline PERFORM links are
// omitted as they would only re-open the current document — no navigation value.
//
// Dynamic, unresolved, and ambiguous targets produce no links per FR-17 (modeled
// gaps stay off every diagnostic/link channel).
//
// Graceful degradation (FR-43): missing/unreadable/out-of-root/cold-index →
// returns nil (no links), never panics.
//
// Concurrency (F7): snapshots idx/res under RLock before any I/O; store-first
// ensures live buffer content is used.
func provideDocumentLink(hctx *handlerContext, params protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
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
		// URI outside workspace root — no links
		return nil, nil
	}

	// Acquire read lock to read idx/res safely (applyDocumentChange holds write lock when updating)
	hctx.idxResMu.RLock()
	idx, res := hctx.idx, hctx.res
	hctx.idxResMu.RUnlock()

	// If res is nil (cold index, pre-first-build), degrade gracefully
	if res == nil {
		return nil, nil
	}

	// Get the source file's analysis (store-first for live edits)
	var fa model.FileAnalysis
	var content []byte

	if hctx.store != nil {
		// Try store first (live buffer)
		doc, ok := hctx.store.Get(params.TextDocument.URI)
		if ok {
			fa = doc.Analysis
			content = doc.Content
		}
	}

	// If not in store, fall back to index/disk
	if content == nil {
		// Pre-publish graceful degradation: if the index isn't ready yet,
		// return nil (no links).
		if idx == nil {
			return nil, nil
		}

		var ok bool
		fa, ok = idx.Get(relPath)
		if !ok {
			// Source file not in index — no links
			return nil, nil
		}

		// Read the source file content for range conversion
		var readErr error
		content, readErr = os.ReadFile(absPath)
		if readErr != nil {
			// Can't read source; no links
			return nil, nil
		}
	}

	// Build the list of document links from resolved edges
	links := buildDocumentLinks(fa, res, relPath, root, string(content), hctx.posEncoding)

	return links, nil
}

// buildDocumentLinks is a pure builder that converts resolved edges into
// document links. It performs no I/O and holds no locks — it is fuzzable
// and safe to call from tests.
//
// For each edge in fa.Edges:
//   - Looks up the resolution via res.Get(relPath, edge.Source)
//   - Skips if not found or not resolved
//   - Skips if the target is in the same file (inline PERFORM — no navigation value)
//   - Otherwise appends a DocumentLink with the edge's Range converted to
//     the negotiated encoding and the Target set to the resolved object URI
//
// The returned slice is sorted by Range.Start (Line then Character) for
// deterministic ordering. Returns nil if there are no links (empty slice
// converted to nil for the LSP null sentinel).
func buildDocumentLinks(fa model.FileAnalysis, res *workspace.ResolutionSet, relPath, root string, content string, enc protocol.PositionEncodingKind) []protocol.DocumentLink {
	var links []protocol.DocumentLink

	// Iterate over all edges in the file
	for _, edge := range fa.Edges {
		// Look up the resolution for this edge
		resolution, ok := res.Get(relPath, edge.Source)
		if !ok || !resolution.IsResolved() {
			// Skip if not found or not resolved (unresolved, dynamic, or ambiguous)
			continue
		}

		// Skip same-file targets (inline PERFORM would only re-open current doc)
		if paths.NormalizeKey(resolution.Path) == relPath {
			continue
		}

		// Build the link Range from the edge's Source span
		linkRange := toProtocolRange(edge.Source, content, enc)

		// Build the target URI
		targetURI := uri.File(filepath.Join(root, resolution.Path))

		// Append the link
		links = append(links, protocol.DocumentLink{
			Range:  linkRange,
			Target: &targetURI,
		})
	}

	// Sort by Range.Start for deterministic ordering (Line then Character)
	sort.Slice(links, func(i, j int) bool {
		if links[i].Range.Start.Line != links[j].Range.Start.Line {
			return links[i].Range.Start.Line < links[j].Range.Start.Line
		}
		return links[i].Range.Start.Character < links[j].Range.Start.Character
	})

	// Return nil (not empty slice) when there are no links, so the null
	// sentinel fires on the wire
	if len(links) == 0 {
		return nil
	}

	return links
}
