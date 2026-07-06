package server

import (
	"fmt"
	"os"
	"sort"
	"strings"

	gojson "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// buildCallCountLens returns a code lens showing the number of resolved inbound
// references to an object (feature 13, T3, Story 1).
//
// Parameters:
//   - idx: the workspace index for counting references
//   - res: the resolution set for matching resolved targets
//   - root: the workspace root (absolute path, unused for counting)
//   - targetPath: workspace-relative path of the target definition
//   - targetName: the target object name
//   - targetType: the target object type
//   - sel: the object root's SelectionRange (zero-width point at object start)
//   - docURI: the document URI for the command arguments
//   - content: the file content for range conversion
//   - enc: the negotiated PositionEncodingKind
//
// Returns a CodeLens with:
//   - Range = toProtocolRange(sel, content, enc)
//   - Command.Title = pluralized count ("0 references", "1 reference", "N references")
//   - Command.Command = "editor.action.showReferences"
//   - Command.Arguments = [uri, position, []Location] where []Location are the caller sites
//
// A zero-count target still emits a lens (not suppressed) per Story 1 AC #1.
//
// No I/O, no locks — a pure function that uses idx/res passed by the caller.
func buildCallCountLens(idx *workspace.Index, res *workspace.ResolutionSet, root string, targetPath string, targetName string, targetType model.ObjectType, sel model.Range, docURI uri.URI, content string, enc protocol.PositionEncodingKind) *protocol.CodeLens {
	// Compute caller locations using referenceSites (same sweep as provideReferences uses).
	locations := referenceSites(idx, res, root, targetPath, targetName, targetType, false, enc)

	// Count inbound references
	count := len(locations)

	// Build the code lens Range from sel (the object root's SelectionRange).
	lensRange := toProtocolRange(sel, content, enc)

	// Build the title with correct pluralization
	var title string
	if count == 1 {
		title = "1 reference"
	} else {
		title = strings.Join([]string{fmt.Sprintf("%d", count), "references"}, " ")
	}

	// Build the showReferences command.
	// Command.Arguments is [uri, position, locations].
	// position is the start of the lens Range (where the code lens appears).
	position := lensRange.Start

	// Convert arguments to []protocol.LSPAny via mustLSPAny helper.
	args := []protocol.LSPAny{
		mustLSPAny(docURI),
		mustLSPAny(position),
		mustLSPAny(locations),
	}

	return &protocol.CodeLens{
		Range: lensRange,
		Command: protocol.Command{
			Title:     title,
			Command:   "editor.action.showReferences",
			Arguments: args,
		},
	}
}

// buildWriteSummaryLens returns a code lens summarizing the DDMs/files an object
// writes to (feature 13, T4, Story 2).
//
// Collects distinct named write targets (Kind == model.EdgeWrites with non-empty Name),
// dedupes case-insensitively on Name, and sorts for determinism. Skips empty-Name entries
// (feature-08 record-form gap, FR-17 — never fabricates a table name).
//
// If the object has no named writes (only empty-Name record-form writes), returns nil
// (no lens; contrast the call-count lens which renders at zero).
//
// The returned CodeLens has:
//   - Range = toProtocolRange(root.SelectionRange, content, enc) (single line)
//   - Command.Title = "Writes: CUSTOMER, ORDERS" (comma-space-joined sorted names)
//   - Command.Command = "editor.action.showReferences"
//   - Command.Arguments = [uri, position, []Location] where []Location are the write sites
//     (DataAccessEntry.NameRange for each named write, converted via toProtocolRange)
//
// No I/O, no locks — a pure function.
func buildWriteSummaryLens(dataAccess []model.DataAccessEntry, root *model.Symbol, documentURI uri.URI, content string, enc protocol.PositionEncodingKind) *protocol.CodeLens {
	// Collect named write targets, deduped case-insensitively.
	// Use a map with uppercase keys for deduplication, but retain original case for display.
	seenMap := make(map[string]string) // uppercase -> original case
	for _, entry := range dataAccess {
		// Only consider write edges with non-empty names (skip record-form gap).
		if entry.Kind != model.EdgeWrites || entry.Name == "" {
			continue
		}
		upper := strings.ToUpper(entry.Name)
		// Dedupe: if we haven't seen this name (case-insensitive), record it.
		if _, exists := seenMap[upper]; !exists {
			seenMap[upper] = entry.Name
		}
	}

	// If no named writes, return nil.
	if len(seenMap) == 0 {
		return nil
	}

	// Extract unique names and sort for determinism.
	var names []string
	for _, name := range seenMap {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build the code lens Range from root's SelectionRange.
	lensRange := toProtocolRange(root.SelectionRange, content, enc)

	// Build the title: "Writes: NAME1, NAME2, ..."
	title := "Writes: " + strings.Join(names, ", ")

	// Collect write-site locations: one per named write entry.
	// Re-iterate the input entries to find each named write's NameRange.
	var locations []protocol.Location
	for _, entry := range dataAccess {
		if entry.Kind != model.EdgeWrites || entry.Name == "" {
			continue
		}
		// Only include each distinct name once. Check if this entry's (case-insensitive) name is in our set.
		upper := strings.ToUpper(entry.Name)
		if _, exists := seenMap[upper]; !exists {
			continue
		}
		// Build a Location from the entry's NameRange.
		entryRange := toProtocolRange(entry.NameRange, content, enc)
		locations = append(locations, protocol.Location{
			URI:   documentURI,
			Range: entryRange,
		})
		// Mark this name as "used" so we don't include it again.
		delete(seenMap, upper)
	}

	// Build the showReferences command.
	// Command.Arguments is [uri, position, locations].
	// position is the start of the lens Range (where the code lens appears).
	position := lensRange.Start

	// Convert arguments to []protocol.LSPAny via mustLSPAny helper.
	args := []protocol.LSPAny{
		mustLSPAny(documentURI),
		mustLSPAny(position),
		mustLSPAny(locations),
	}

	return &protocol.CodeLens{
		Range: lensRange,
		Command: protocol.Command{
			Title:     title,
			Command:   "editor.action.showReferences",
			Arguments: args,
		},
	}
}

// mustLSPAny converts a Go value to a protocol.LSPAny (jsontext.Value).
// Used for encoding showReferences command arguments.
func mustLSPAny(v any) jsontext.Value {
	b, err := gojson.Marshal(v)
	if err != nil {
		return jsontext.Value("null")
	}
	return jsontext.Value(b)
}

// provideCodeLens handles the textDocument/codeLens request (feature 13, T5).
// It is the LSP provider entry point for code lens support.
//
// Behavior:
//  1. If hctx.cfg.Analysis.EnableCodeLens == false, return nil, nil (no lenses).
//  2. Resolve the document: open-document store first (live edits), else index snapshot.
//  3. If Structure == nil or file unreadable → nil, nil (FR-43).
//  4. Build the call-count lens (T3) and, if there are named writes, the write-summary
//     lens (T4), using the object root SelectionRange as the anchor.
//  5. Return the assembled []protocol.CodeLens in deterministic order (call-count then
//     write-summary), or nil when neither applies.
//
// Concurrency (F7): For the open-document buffer, no locking is needed (store is
// self-synchronized). For the index snapshot, idxResMu.RLock is acquired briefly,
// released before any file I/O, and the snapshot (pointers to idx/res) is safe to use
// afterward (Index and ResolutionSet are immutable post-snapshot; applyDocumentChange
// swaps the pointers under write lock — build-then-publish semantics).
func provideCodeLens(hctx *handlerContext, params protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Check if code lens is enabled in the configuration
	if !hctx.cfg.Analysis.EnableCodeLens {
		return nil, nil
	}

	// Snapshot idx/res under the read lock; release before any file I/O (F7).
	// Both the open-document and index paths need these for the call-count lens.
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	hctx.idxResMu.RUnlock()

	// Convert LSP URI to workspace-relative path (forward-slash index key
	// convention). This doubles as the call-count target identity.
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root
		return nil, nil
	}

	// Resolution order 1: open-document store (current, unsaved edits — Story 2).
	// The open buffer supplies content, but the call-count lens still needs the
	// idx/res snapshot, so pass it through.
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil && doc.Analysis.Structure != nil {
			return buildCodeLenses(&doc.Analysis, string(doc.Content), params.TextDocument.URI, hctx, relPath, idx, res)
		}
	}

	// Resolution order 2: index + disk (document not open).
	if idx == nil {
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

	return buildCodeLenses(&fa, string(content), params.TextDocument.URI, hctx, relPath, idx, res)
}

// buildCodeLenses is a helper that constructs the code lenses from a FileAnalysis.
// It takes the analysis, content, document URI, relative path, and snapshot of idx/res
// (which may be nil for open-document buffers) and returns the assembled lenses in order
// (call-count first, write-summary second).
func buildCodeLenses(fa *model.FileAnalysis, content string, docURI uri.URI, hctx *handlerContext, relPath string, idx *workspace.Index, res *workspace.ResolutionSet) ([]protocol.CodeLens, error) {
	// Guard: Structure must be present
	if fa.Structure == nil {
		return nil, nil
	}

	root := fa.Structure
	var lenses []protocol.CodeLens

	// Build the call-count lens (T3). This needs idx/res for counting references.
	// If idx/res are available (from the index snapshot), use them.
	// If idx/res are nil (open-document case without index snapshot), skip the call-count lens
	// but still build the write-summary lens if applicable.
	if idx != nil && res != nil && relPath != "" {
		targetName := root.Name
		targetType := fa.ObjectType

		callCountLens := buildCallCountLens(idx, res, hctx.root, relPath, targetName, targetType, root.SelectionRange, docURI, content, hctx.posEncoding)
		if callCountLens != nil {
			lenses = append(lenses, *callCountLens)
		}
	}

	// Build the write-summary lens (T4) if there are named writes.
	writeSummaryLens := buildWriteSummaryLens(fa.DataAccess, root, docURI, content, hctx.posEncoding)
	if writeSummaryLens != nil {
		lenses = append(lenses, *writeSummaryLens)
	}

	// Return lenses, or nil if none were built
	if len(lenses) == 0 {
		return nil, nil
	}

	return lenses, nil
}
