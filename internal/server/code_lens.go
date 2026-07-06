package server

import (
	"fmt"
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
