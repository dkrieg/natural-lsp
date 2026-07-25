package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// provideReferences handles the textDocument/references request (feature 10, T10–T11; feature 27 T4).
// It is the LSP provider entry point: given a cursor position, it finds the symbol under
// the cursor and returns all reference sites across the workspace.
//
// Returns nil (no error) for a cursor with no symbol under it (no edge/data-access/variable found).
// For a resolved symbol, returns the set of reference sites whose resolved targets match
// that symbol. When ReferenceContext.IncludeDeclaration is true, includes the declaration
// site itself in the result.
//
// For variables (feature 27 T4):
//   - If the cursor is on a variable use-site, find all use-sites in the same file
//     with the same name (case-insensitive), each as a Location with its precise Range.
//   - Alternatively, if the cursor is on a declaration's NameRange, resolve it and return
//     all use-sites (same as if the cursor were on a use-site).
//   - If includeDeclaration is true, add the declaration's NameRange to the result.
//   - Store-first: read the open buffer's live content first, falling back to disk/index.
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

	// Convert LSP URI to workspace-relative path (forward-slash index key convention)
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root — no references
		return nil, nil
	}

	// Get the source file's analysis (store-first for variables, like T3)
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
		// Acquire read lock to read idx/res safely (applyDocumentChange holds write lock when updating)
		hctx.idxResMu.RLock()
		idx := hctx.idx
		hctx.idxResMu.RUnlock()

		// Pre-publish graceful degradation: if the index isn't ready yet,
		// we can't serve anything (no index, no disk read). Return nil per FR-43.
		if idx == nil {
			return nil, nil
		}

		var ok bool
		sourceFA, ok = idx.Get(relPath)
		if !ok {
			// Source file not in index — no references
			return nil, nil
		}

		// Read the source file content for position conversion
		var readErr error
		sourceContent, readErr = os.ReadFile(absPath)
		if readErr != nil {
			// Can't read source; no references
			return nil, nil
		}

		// Note: for variable cases, we'll need idx/res later for any cross-file lookups.
		// But Phase A T4 is same-file only, so we'll handle that inline.
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), hctx.posEncoding)

	// Find the edge (data-access, or variable ref) at the cursor position
	edge, dataAccess, varRef := findCursorTarget(sourceFA, cursorPos, string(sourceContent), hctx.az)

	// Extract variable refs on demand from the content (in-memory only, not persisted)
	var allVarRefs []model.VariableRef
	if hctx.az != nil {
		allVarRefs = hctx.az.ExtractVariableRefs(string(sourceContent))
	}

	// Handle variable reference case (feature 27 T4) — same-file only for Phase A
	if varRef != nil {
		locations := findVariableReferencesInFile(varRef, &sourceFA, allVarRefs, string(sourceContent), absPath, params.Context.IncludeDeclaration, hctx.posEncoding)
		if len(locations) == 0 {
			return nil, nil
		}
		return locations, nil
	}

	// Handle variable declaration case (feature 27 T4, idempotent):
	// When the cursor is on a declaration's NameRange, find all use-sites of that variable.
	if decl := findVariableDeclarationAtCursor(&sourceFA, cursorPos); decl != nil {
		// Create a synthetic VariableRef to pass to the reference finder
		varRef := model.VariableRef{
			Name:  decl.Name,
			Range: decl.NameRange,
		}
		locations := findVariableReferencesInFile(&varRef, &sourceFA, allVarRefs, string(sourceContent), absPath, params.Context.IncludeDeclaration, hctx.posEncoding)
		if len(locations) == 0 {
			return nil, nil
		}
		return locations, nil
	}

	if edge == nil && dataAccess == nil {
		// No edge, data-access, or variable at cursor position — no references
		return nil, nil
	}

	// Acquire read lock to read idx/res safely for edge/data-access cases
	hctx.idxResMu.RLock()
	idx, res := hctx.idx, hctx.res
	hctx.idxResMu.RUnlock()

	if idx == nil || res == nil {
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
		// Should not reach here (all cases covered above)
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

	// Iterate every file in the index. Range conversion uses the in-memory
	// line-width table (feature 22 T8) via the converter ForEachWithRange hands
	// each callback, so there is NO per-query disk read here — the previous
	// os.ReadFile sweep over every indexed file is gone.
	idx.ForEachWithRange(func(filePath string, fa model.FileAnalysis, toRange rangeConverter) {
		absPath := filepath.Join(root, filePath)

		// Scan edges: for each edge, check if its resolution matches the target.
		// The matching predicate is factored into edgeMatchesTarget.
		for _, edge := range fa.Edges {
			resolution, ok := res.Get(filePath, edge.Source)
			if !ok {
				continue
			}
			if !edgeMatchesTarget(resolution, targetPath, targetType) {
				continue
			}
			fileURI := uri.File(absPath)
			protocolRng := protocolRangeVia(toRange, edge.Source, enc)
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
					protocolRng := protocolRangeVia(toRange, dataAccess.NameRange, enc)

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
			// Convert via the in-memory line-width table — no disk read (T8).
			targetAbsPath := filepath.Join(root, targetPath)
			fileURI := uri.File(targetAbsPath)
			protocolRng := indexProtocolRange(idx, targetPath, targetFA.Structure.SelectionRange, enc)

			locations = append(locations, protocol.Location{
				URI:   fileURI,
				Range: protocolRng,
			})
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
// resolutions never match (FR-17). Used by referenceSites to invert resolution.
func edgeMatchesTarget(resolution workspace.Resolution, targetPath string, targetType model.ObjectType) bool {
	if !resolution.IsResolved() {
		return false
	}
	normalizedResPath := paths.NormalizeKey(resolution.Path)
	normalizedTargetPath := paths.NormalizeKey(targetPath)
	if normalizedResPath != normalizedTargetPath {
		return false
	}
	if targetType != "" && resolution.Type != targetType {
		return false
	}
	return true
}

// findVariableReferencesInFile returns all same-file occurrences of a variable
// (feature 27 T4). It matches variable use-sites from the extracted variable references
// against the target variable name (case-insensitive) and includes the declaration's
// NameRange if includeDeclaration is true.
//
// Parameters:
//   - varRef: the variable reference at the cursor (containing the target name)
//   - sourceFA: the source file's FileAnalysis (containing Definitions)
//   - allVarRefs: all extracted variable references from the file (in-memory only)
//   - content: the source file's content (for range conversion)
//   - absPath: absolute path to the file (for URI construction)
//   - includeDeclaration: if true, include the variable's declaration NameRange
//   - enc: the negotiated PositionEncodingKind
//
// Returns protocol.Location slice sorted by line then column (deterministic).
// Returns nil (no error) if no references are found (FR-17, FR-43).
func findVariableReferencesInFile(varRef *model.VariableRef, sourceFA *model.FileAnalysis, allVarRefs []model.VariableRef, content, absPath string, includeDeclaration bool, enc protocol.PositionEncodingKind) []protocol.Location {
	if varRef == nil || sourceFA == nil {
		return nil
	}

	var locations []protocol.Location

	// Check if this is a *-system variable or &-dynamic (modeled gap — return empty per FR-17)
	varName := varRef.Name
	if strings.HasPrefix(varName, "*") || strings.HasPrefix(varName, "&") {
		return nil
	}

	// Find the declaration for this variable name (to get the canonical Name for matching)
	// This ensures group-qualified and unqualified refs bind to the same declaration.
	var targetDecl *model.DataDefinition
	findDeclaration(sourceFA.Definitions, varName, "", func(decl *model.DataDefinition) {
		if targetDecl == nil {
			// Take the first match (in case of ambiguity, we'll still match all uses)
			targetDecl = decl
		}
	})

	// If no declaration found, return empty (undeclared variable — FR-17)
	if targetDecl == nil {
		return nil
	}

	fileURI := uri.File(absPath)

	// Collect all matching variable references from the extracted refs
	// A variable ref matches if its name (case-insensitive) equals the target declaration's name
	for _, ref := range allVarRefs {
		if strings.EqualFold(ref.Name, targetDecl.Name) {
			protocolRng := toProtocolRange(ref.Range, content, enc)
			locations = append(locations, protocol.Location{
				URI:   fileURI,
				Range: protocolRng,
			})
		}
	}

	// If includeDeclaration is true, add the declaration's NameRange to the result
	if includeDeclaration {
		declRange := targetDecl.NameRange
		if declRange.Start.Line == 0 && declRange.End.Line == 0 {
			// NameRange not populated; fall back to Range (FR-43)
			declRange = targetDecl.Range
		}
		if declRange.Start.Line > 0 {
			protocolRng := toProtocolRange(declRange, content, enc)
			locations = append(locations, protocol.Location{
				URI:   fileURI,
				Range: protocolRng,
			})
		}
	}

	// Sort by line then column (deterministic)
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].Range.Start.Line != locations[j].Range.Start.Line {
			return locations[i].Range.Start.Line < locations[j].Range.Start.Line
		}
		return locations[i].Range.Start.Character < locations[j].Range.Start.Character
	})

	return locations
}
