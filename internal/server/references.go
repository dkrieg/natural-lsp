package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// provideReferences handles the textDocument/references request (feature 10, T10–T11).
// It is the LSP provider entry point: given a cursor position, it finds the symbol under
// the cursor and returns all reference sites across the workspace.
//
// Returns nil (no error) for a cursor with no symbol under it (no edge/data-access found).
// For a resolved symbol, returns the set of reference sites whose resolved targets match
// that symbol. When ReferenceContext.IncludeDeclaration is true, includes the declaration
// site itself in the result.
//
// Dynamic and unresolved references are excluded from a specific symbol's reference list
// (they cannot claim a resolved link to the target). This implements FR-17 modeled gap:
// dynamic sites remain visible via diagnostics/outline, not falsely linked in references.
//
// Concurrency (F7): holds the read lock on idxResMu to ensure consistent access to idx/res
// (applyDocumentChange swaps these pointers under the write lock, so handlers see
// a stable snapshot for the duration of a request).
func provideReferences(hctx *handlerContext, params protocol.ReferenceParams) ([]protocol.Location, error) {
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
		// URI outside workspace root — no references
		return nil, nil
	}

	// Get the source file's analysis from the index
	sourceFA, ok := idx.Get(relPath)
	if !ok {
		// Source file not in index — no references
		return nil, nil
	}

	// Read the source file content for position conversion
	sourceContent, err := os.ReadFile(absPath)
	if err != nil {
		// Can't read source; no references
		return nil, nil
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), hctx.posEncoding)

	// Find the edge (or data-access) at the cursor position
	edge, dataAccess := findCursorTarget(sourceFA, cursorPos)
	if edge == nil && dataAccess == nil {
		// No edge or data-access at cursor position — no references
		return nil, nil
	}

	// Determine the target symbol identity based on what we found at the cursor
	var targetPath string
	var targetName string
	var targetType model.ObjectType

	if edge != nil {
		// We found an edge; resolve it to get the target identity
		resolution, ok := res.Get(relPath, edge.Source)
		if !ok || !resolution.IsResolved() {
			// Edge is not resolved (dynamic or unresolved) — no references
			return nil, nil
		}

		// Extract the target identity from the resolution
		targetPath = resolution.Path
		targetName = edge.TargetName
		targetType = resolution.Type
	} else if dataAccess != nil {
		// We found a data-access entry; treat it as a reference to a DDM/view
		// For now, use the data-access name as the target
		// DDM resolution is not yet implemented; matching is by name only.
		targetPath = "" // TODO (future): resolve data-access to DDM path
		targetName = dataAccess.Name
		targetType = model.ObjectDDM
		// Proceed with name-based matching (DDM resolution is future work)
	} else {
		// Should not reach here (both are nil)
		return nil, nil
	}

	// Call the sweep primitive to find all reference sites
	locations := referenceSites(idx, res, hctx.root, targetPath, targetName, targetType, params.Context.IncludeDeclaration, hctx.posEncoding)

	// Return the locations (empty slice if none found)
	if len(locations) == 0 {
		return nil, nil
	}
	return locations, nil
}

// referenceSites is the reverse-reference sweep primitive (feature 10, T10).
// Given a target symbol identity (name + optional type/path), it scans every file
// in the index and collects all reference sites (EdgeEntry.Source, DataAccessEntry.NameRange)
// whose RESOLVED target matches that symbol.
//
// Parameters:
//   - idx: the workspace index to scan
//   - res: the resolution set (pre-computed resolution outcomes for all edges)
//   - root: the workspace root (absolute path) for constructing file URIs
//   - targetPath: workspace-relative path of the target definition (e.g., "APP/SHARED.NSN")
//   - includeDeclaration: if true, include the declaration site itself
//   - enc: the negotiated PositionEncodingKind for range conversion
//
// Returns protocol.Location slice sorted by URI then range (deterministic).
// An empty slice (not nil) is returned when no references are found.
//
// This function inverts resolution: for each file's edges, it checks if the
// Resolution's Path (the resolved target file) and target name match the target symbol.
// It uses the edge's Source range (EdgeEntry.Source) for calls and NameRange
// (DataAccessEntry.NameRange) for data-access sites.
func referenceSites(idx *workspace.Index, res *workspace.ResolutionSet, root string, targetPath string, targetName string, targetType model.ObjectType, includeDeclaration bool, enc protocol.PositionEncodingKind) []protocol.Location {
	// Guard: idx and res must be initialized
	if idx == nil || res == nil {
		return []protocol.Location{}
	}

	// Collect all reference sites as protocol.Location values
	var locations []protocol.Location

	// Iterate every file in the index
	idx.ForEach(func(filePath string, fa model.FileAnalysis) {
		// Read the file content for range conversion (FR-43: graceful degradation if read fails)
		absPath := filepath.Join(root, filePath)
		fileContent, err := os.ReadFile(absPath)
		if err != nil {
			// Can't read file; skip range conversion (this file has no references)
			return
		}

		// Scan edges: for each edge, check if its resolution matches the target.
		// The matching predicate is shared with inboundCallCount via edgeMatchesTarget.
		for _, edge := range fa.Edges {
			resolution, ok := res.Get(filePath, edge.Source)
			if !ok {
				continue
			}
			if !edgeMatchesTarget(resolution, targetPath, targetType) {
				continue
			}
			fileURI := uri.File(absPath)
			protocolRng := toProtocolRange(edge.Source, string(fileContent), enc)
			locations = append(locations, protocol.Location{
				URI:   fileURI,
				Range: protocolRng,
			})
		}

		// Scan data-access entries for DDM-field references when targetType == ObjectDDM
		// DDM resolution is not yet implemented; matching is by name only.
		// For a DDM target, scan all DataAccessEntry whose Name matches targetName (case-insensitive).
		if targetType == model.ObjectDDM {
			for _, dataAccess := range fa.DataAccess {
				// Match by normalized name (case-insensitive)
				if strings.EqualFold(dataAccess.Name, targetName) {
					// Record the reference site using NameRange (the DDM-name token, not the whole statement)
					fileURI := uri.File(absPath)
					protocolRng := toProtocolRange(dataAccess.NameRange, string(fileContent), enc)

					locations = append(locations, protocol.Location{
						URI:   fileURI,
						Range: protocolRng,
					})
				}
			}
		}
	})

	// If includeDeclaration is true, add the declaration site
	if includeDeclaration {
		// The declaration site is the target object's own definition in targetPath
		// Use the target file's Structure.SelectionRange if available
		targetFA, ok := idx.Get(targetPath)
		if ok && targetFA.Structure != nil {
			// Read the target file content for range conversion
			targetAbsPath := filepath.Join(root, targetPath)
			targetContent, err := os.ReadFile(targetAbsPath)
			if err == nil {
				// Successfully read the target file; build the declaration location
				fileURI := uri.File(targetAbsPath)
				protocolRng := toProtocolRange(targetFA.Structure.SelectionRange, string(targetContent), enc)

				locations = append(locations, protocol.Location{
					URI:   fileURI,
					Range: protocolRng,
				})
			}
		}
	}

	// Sort by URI (as string) then by range Start position (deterministic)
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].URI != locations[j].URI {
			return string(locations[i].URI) < string(locations[j].URI)
		}
		if locations[i].Range.Start.Line != locations[j].Range.Start.Line {
			return locations[i].Range.Start.Line < locations[j].Range.Start.Line
		}
		return locations[i].Range.Start.Character < locations[j].Range.Start.Character
	})

	return locations
}

// edgeMatchesTarget reports whether a resolution outcome matches the target symbol.
// An edge matches when the resolution is Resolved and its Path (normalized to
// forward slashes) and Type match the target. Dynamic/unresolved/ambiguous
// resolutions never match (FR-17). Shared by referenceSites and inboundCallCount.
func edgeMatchesTarget(resolution workspace.Resolution, targetPath string, targetType model.ObjectType) bool {
	if !resolution.IsResolved() {
		return false
	}
	normalizedResPath := strings.ReplaceAll(resolution.Path, "\\", "/")
	normalizedTargetPath := strings.ReplaceAll(targetPath, "\\", "/")
	if normalizedResPath != normalizedTargetPath {
		return false
	}
	if targetType != "" && resolution.Type != targetType {
		return false
	}
	return true
}

// inboundCallCount returns the number of resolved reference sites for a target
// object without materializing Locations or reading file content (feature 13, T2).
//
// It is a count-only sibling of referenceSites: it mirrors the matching semantics
// (resolved-only, path+type match via edgeMatchesTarget, plus case-insensitive DDM
// name match) but skips os.ReadFile and range conversion, so the call-count lens
// need not pay the full sweep's per-file I/O cost. Dynamic/unresolved/ambiguous
// references are never counted (FR-17).
//
// The root parameter is unused for counting (no I/O); it is kept for signature
// parity with the count-only test's call site.
func inboundCallCount(idx *workspace.Index, res *workspace.ResolutionSet, root string, targetPath string, targetName string, targetType model.ObjectType) int {
	if idx == nil || res == nil {
		return 0
	}

	count := 0
	idx.ForEach(func(filePath string, fa model.FileAnalysis) {
		for _, edge := range fa.Edges {
			resolution, ok := res.Get(filePath, edge.Source)
			if !ok {
				continue
			}
			if edgeMatchesTarget(resolution, targetPath, targetType) {
				count++
			}
		}

		// DDM references match by name only (DDM resolution is future work),
		// mirroring referenceSites' ObjectDDM branch so the count equals
		// len(referenceSites(...)) for a DDM target too.
		if targetType == model.ObjectDDM {
			for _, dataAccess := range fa.DataAccess {
				if strings.EqualFold(dataAccess.Name, targetName) {
					count++
				}
			}
		}
	})

	return count
}
