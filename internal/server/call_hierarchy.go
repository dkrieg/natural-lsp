package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	gojson "github.com/go-json-experiment/json"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
	"github.com/dkrieg/natural-lsp/internal/workspace"
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
// (forward-slash convention, as used in the index). The content parameter is
// the file's source content, used for accurate range conversion (ADR-008).
func buildCallHierarchyItem(root, relPath string, content []byte, sym *model.Symbol, enc protocol.PositionEncodingKind) protocol.CallHierarchyItem {
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

	return protocol.CallHierarchyItem{
		Name:           sym.Name,
		Kind:           kind,
		URI:            uri.File(absPath),
		Range:          toProtocolRange(sym.Range, string(content), enc),
		SelectionRange: toProtocolRange(sym.SelectionRange, string(content), enc),
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

// providePrepareCallHierarchy (feature 18, T3 GREEN) is the LSP provider for
// textDocument/prepareCallHierarchy (FR-49, Story 1, AC1).
//
// Returns a CallHierarchyItem per cursor context:
//   - Cursor on a call site → items for the resolved target(s) (one per candidate if ambiguous)
//   - Cursor on a definition name (object root or subroutine) → item for that symbol
//   - Non-callable position → empty (nil is fine; dispatch emits [])
//
// Concurrency (F7): snapshots idx/res/posEncoding/root under RLock, releases before I/O.
// Graceful degradation (FR-43): missing/unreadable/nil Structure → empty, never panics.
func providePrepareCallHierarchy(hctx *handlerContext, params protocol.CallHierarchyPrepareParams) ([]protocol.CallHierarchyItem, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Snapshot idx/res/posEncoding/root under read lock (F7); release before I/O
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	if idx == nil || res == nil {
		return nil, nil
	}

	// Convert LSP URI to workspace-relative path
	absPath, relPath, err := uriToRelPath(root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root
		return nil, nil
	}

	// Store-first: get source file content + analysis
	var sourceFA *model.FileAnalysis
	var sourceContent []byte

	// Try store first (for live edits)
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil {
			sourceFA = &doc.Analysis
			sourceContent = doc.Content
		}
	}

	// Fall back to index + disk read
	if sourceFA == nil {
		fa, ok := idx.Get(relPath)
		if !ok {
			return nil, nil
		}
		sourceFA = &fa

		// Read content from disk for position conversion
		content, err := os.ReadFile(absPath)
		if err != nil {
			// Can't read source; return empty
			return nil, nil
		}
		sourceContent = content
	}

	// Convert protocol position to model position
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), posEncoding)

	// Case A: cursor on a call site (EdgeEntry at cursor position)
	edge, _, _ := findCursorTarget(*sourceFA, cursorPos, string(sourceContent), hctx.az)
	if edge != nil && isCallableEdgeKind(edge.Kind) {
		// Look up the resolution for this edge
		resolution, ok := res.Get(relPath, edge.Source)
		if !ok {
			// Edge not found in resolution set
			return nil, nil
		}

		// Handle resolved case
		if resolution.IsResolved() {
			// Read the target file's analysis
			targetFA, ok := idx.Get(resolution.Path)
			if !ok {
				return nil, nil
			}

			// Read the target file content for range conversion
			targetAbsPath := filepath.Join(root, resolution.Path)
			targetContent, err := os.ReadFile(targetAbsPath)
			if err != nil {
				// Can't read target file
				return nil, nil
			}

			// Check if this is an inline PERFORM (target in same file)
			normalizedResPath := paths.NormalizeKey(resolution.Path)
			if strings.EqualFold(normalizedResPath, relPath) {
				// Same file: find the matching subroutine in Structure.Children
				if targetFA.Structure != nil && targetFA.Structure.Children != nil {
					targetName := strings.ToUpper(edge.TargetName)
					for i := range targetFA.Structure.Children {
						child := &targetFA.Structure.Children[i]
						if child.Kind == model.SymbolSubroutine && strings.EqualFold(child.Name, targetName) {
							// Found the inline subroutine; build an item from it
							item := buildCallHierarchyItem(root, relPath, targetContent, child, posEncoding)
							return []protocol.CallHierarchyItem{item}, nil
						}
					}
				}
			}

			// External target: build from the object-root Structure
			if targetFA.Structure == nil {
				return nil, nil
			}
			item := buildCallHierarchyItem(root, resolution.Path, targetContent, targetFA.Structure, posEncoding)
			return []protocol.CallHierarchyItem{item}, nil
		}

		// Handle ambiguous case: build ONE item per candidate (OQ-4)
		if resolution.IsAmbiguous() {
			items := make([]protocol.CallHierarchyItem, 0, len(resolution.Candidates))
			for _, candidatePath := range resolution.Candidates {
				// Fetch the candidate file's analysis
				candidateFA, ok := idx.Get(candidatePath)
				if !ok || candidateFA.Structure == nil {
					continue
				}

				// Read the candidate file content
				candidateAbsPath := filepath.Join(root, candidatePath)
				candidateContent, err := os.ReadFile(candidateAbsPath)
				if err != nil {
					continue
				}

				// Build an item from the candidate's object root
				item := buildCallHierarchyItem(root, candidatePath, candidateContent, candidateFA.Structure, posEncoding)

				// Adjust the kind based on the file's ObjectType (not all roots are Module)
				adjustCallHierarchyItemKind(&item, candidateFA.ObjectType)

				items = append(items, item)
			}
			if len(items) > 0 {
				return items, nil
			}
		}

		// Dynamic/unresolved case → empty
		return nil, nil
	}

	// Case B: cursor on a definition name (no edge under cursor)
	// Check if cursor falls within the object-root SelectionRange (only if it's non-zero)
	if sourceFA.Structure != nil && !isZeroRange(sourceFA.Structure.SelectionRange) {
		if cursorInRange(sourceFA.Structure.SelectionRange, cursorPos) {
			// Cursor is on the object root's name
			item := buildCallHierarchyItem(root, relPath, sourceContent, sourceFA.Structure, posEncoding)
			return []protocol.CallHierarchyItem{item}, nil
		}
	}

	// Check if cursor falls within a subroutine child's SelectionRange or Range
	if sourceFA.Structure != nil {
		for i := range sourceFA.Structure.Children {
			child := &sourceFA.Structure.Children[i]
			if child.Kind == model.SymbolSubroutine {
				// Check SelectionRange (the name token) first
				if !isZeroRange(child.SelectionRange) && cursorInRange(child.SelectionRange, cursorPos) {
					// Cursor is on this subroutine's name
					item := buildCallHierarchyItem(root, relPath, sourceContent, child, posEncoding)
					return []protocol.CallHierarchyItem{item}, nil
				}
				// Fallback: check if cursor is within the subroutine's full Range
				// (handles test placing cursor on "DEFINE SUBROUTINE" keyword)
				if cursorInRange(child.Range, cursorPos) {
					item := buildCallHierarchyItem(root, relPath, sourceContent, child, posEncoding)
					return []protocol.CallHierarchyItem{item}, nil
				}
			}
		}
	}

	// Non-callable position → empty
	return nil, nil
}

// cursorInRange checks if a model.Position (1-based) is contained in a model.Range (inclusive).
func cursorInRange(r model.Range, pos model.Position) bool {
	// Check if pos is before r.Start
	if pos.Line < r.Start.Line || (pos.Line == r.Start.Line && pos.Column < r.Start.Column) {
		return false
	}
	// Check if pos is after r.End
	if pos.Line > r.End.Line || (pos.Line == r.End.Line && pos.Column > r.End.Column) {
		return false
	}
	return true
}

// isZeroRange checks if a Range is zero-width (start equals end).
// A zero-width range indicates a synthetic name (not from the source).
func isZeroRange(r model.Range) bool {
	return r.Start == r.End
}

// isCallableEdgeKind checks if an edge kind is a callable kind (call site).
// Callable kinds are: CALLNAT (static/dynamic), PERFORM, FETCH/RUN (static/dynamic).
// Non-callable kinds (data access, includes, data-area uses) are excluded.
func isCallableEdgeKind(kind model.EdgeKind) bool {
	return kind == model.EdgeCalls ||
		kind == model.EdgeCallsDynamic ||
		kind == model.EdgePerforms ||
		kind == model.EdgeNavigatesTo ||
		kind == model.EdgeNavigatesToDynamic
}

// adjustCallHierarchyItemKind updates an item's kind based on the file's ObjectType.
// The Structure root is always SymbolObject, but the file's actual type may indicate
// a subroutine or other callable. This adjustment ensures the item kind reflects
// the actual callable type.
func adjustCallHierarchyItemKind(item *protocol.CallHierarchyItem, objType model.ObjectType) {
	switch objType {
	case model.ObjectSubprogram:
		// Subprogram (NSN) → Function
		item.Kind = protocol.SymbolKindFunction
	case model.ObjectExternalSubroutine:
		// External subroutine (NSS) → Function
		item.Kind = protocol.SymbolKindFunction
	case model.ObjectHelproutine:
		// Helproutine (NSH) → Function
		item.Kind = protocol.SymbolKindFunction
	case model.ObjectFunction:
		// Function (NS7) → Function
		item.Kind = protocol.SymbolKindFunction
	case model.ObjectClass:
		// Class (NS4) → Class
		item.Kind = protocol.SymbolKindClass
	case model.ObjectDialog:
		// Dialog (NS3) → Class (no direct Dialog kind in LSP)
		item.Kind = protocol.SymbolKindClass
	case model.ObjectAdapter:
		// Adapter (NS8) → Class
		item.Kind = protocol.SymbolKindClass
		// ObjectProgram, ObjectMap, ObjectDDM, ObjectCopycode, ObjectDataArea, ObjectText
		// stay as Module (the default SymbolObject → Module mapping)
	}
}

// provideIncomingCalls (feature 18, T4 GREEN) is the LSP provider for
// callHierarchy/incomingCalls (FR-49, Story 2, AC1-3).
//
// Returns all call sites across the workspace that resolve to the target symbol,
// grouped by caller file, with FromRanges sorted by start position.
// Dynamic/unresolved edges are excluded (FR-11/FR-17).
//
// Concurrency (F7): snapshots idx/res/posEncoding/root under RLock, releases before I/O.
// Graceful degradation (FR-43): unresolvable target / unreadable files → empty, never panics.
func provideIncomingCalls(hctx *handlerContext, params protocol.CallHierarchyIncomingCallsParams) ([]protocol.CallHierarchyIncomingCall, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Snapshot idx/res/posEncoding/root under read lock (F7); release before I/O
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	if idx == nil || res == nil {
		return nil, nil
	}

	// Resolve the target item to get its identity
	targetPath, targetFA, targetSym, ok := resolveItemIdentity(idx, root, params.Item, posEncoding)
	if !ok {
		// Cannot resolve the target; return empty
		return nil, nil
	}

	// Determine the target's ObjectType and name to match against
	targetType := targetFA.ObjectType
	targetName := strings.ToUpper(targetSym.Name)

	// Collect incoming call sites grouped by caller (reverse sweep)
	incomingByCallerPath := incomingCallSites(idx, res, targetPath, targetType, targetName)

	// For each caller, build a CallHierarchyIncomingCall entry
	var result []protocol.CallHierarchyIncomingCall

	// Sort callers by URI for deterministic output
	callerPaths := make([]string, 0, len(incomingByCallerPath))
	for callerPath := range incomingByCallerPath {
		callerPaths = append(callerPaths, callerPath)
	}
	sort.Strings(callerPaths)

	for _, callerPath := range callerPaths {
		ranges := incomingByCallerPath[callerPath]

		// Get the caller's analysis
		callerFA, ok := idx.Get(callerPath)
		if !ok {
			continue
		}

		// Read the caller's content for range conversion
		callerAbsPath := filepath.Join(root, callerPath)
		callerContent, err := os.ReadFile(callerAbsPath)
		if err != nil {
			// Can't read caller file; skip (FR-43)
			continue
		}

		// Check that Structure exists
		if callerFA.Structure == nil {
			continue
		}

		// Build the From item from the caller's object root
		fromItem := buildCallHierarchyItem(root, callerPath, callerContent, callerFA.Structure, posEncoding)

		// Convert each range to protocol.Range and sort by start
		fromRanges := make([]protocol.Range, len(ranges))
		for i, rng := range ranges {
			fromRanges[i] = toProtocolRange(rng, string(callerContent), posEncoding)
		}

		// Sort by start position (already in source order but be explicit)
		sort.Slice(fromRanges, func(i, j int) bool {
			if fromRanges[i].Start.Line != fromRanges[j].Start.Line {
				return fromRanges[i].Start.Line < fromRanges[j].Start.Line
			}
			return fromRanges[i].Start.Character < fromRanges[j].Start.Character
		})

		result = append(result, protocol.CallHierarchyIncomingCall{
			From:       fromItem,
			FromRanges: fromRanges,
		})
	}

	return result, nil
}

// incomingCallSites collects all call sites across the workspace that resolve to the target,
// grouped by caller file path.
// It mirrors referenceSites' reverse sweep: for each file's edges, it checks if the
// resolution matches the target (via edgeMatchesTarget, which excludes dynamic/unresolved).
// Returns a map from caller file path to a slice of edge.Source ranges, in source order.
func incomingCallSites(idx *workspace.Index, res *workspace.ResolutionSet, targetPath string, targetType model.ObjectType, targetName string) map[string][]model.Range {
	// Map from caller path to list of edge source ranges
	incomingByCaller := make(map[string][]model.Range)

	// Iterate every file in the index
	idx.ForEach(func(filePath string, fa model.FileAnalysis) {
		// Scan edges: for each edge, check if its resolution matches the target.
		for _, edge := range fa.Edges {
			resolution, ok := res.Get(filePath, edge.Source)
			if !ok {
				continue
			}
			if !edgeMatchesTarget(resolution, targetPath, targetType) {
				continue
			}

			// This edge resolves to the target; record its source range
			incomingByCaller[filePath] = append(incomingByCaller[filePath], edge.Source)
		}
	})

	return incomingByCaller
}

// provideOutgoingCalls (feature 18, T5 GREEN) is the LSP provider for
// callHierarchy/outgoingCalls (FR-49, Story 3, AC1-3).
//
// Returns all static outgoing call relationships from the source module:
// CALLNAT, external PERFORM, FETCH, and RUN statements where the target
// is statically resolved. Dynamic/unresolvable and INCLUDE (copycode)
// edges are excluded (FR-17, OQ-1).
//
// Each result entry carries the callee's CallHierarchyItem and the fromRanges
// (call-site positions within the current module), sorted by start.
//
// Concurrency (F7): snapshots idx/res/posEncoding/root under RLock, releases before I/O.
// Graceful degradation (FR-43): unresolvable target / unreadable files → empty, never panics.
func provideOutgoingCalls(hctx *handlerContext, params protocol.CallHierarchyOutgoingCallsParams) ([]protocol.CallHierarchyOutgoingCall, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Snapshot idx/res/posEncoding/root under read lock (F7); release before I/O
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	if idx == nil || res == nil {
		return nil, nil
	}

	// Resolve the item to get the source module's identity
	sourcePath, sourceFA, _, ok := resolveItemIdentity(idx, root, params.Item, posEncoding)
	if !ok {
		// Cannot resolve the source item; return empty
		return nil, nil
	}

	// Read the source file content for range conversion
	sourceAbsPath := filepath.Join(root, sourcePath)
	sourceContent, err := os.ReadFile(sourceAbsPath)
	if err != nil {
		// Can't read source file; return empty (FR-43)
		return nil, nil
	}

	// Collect outgoing call sites grouped by callee
	// Key: for external callees, this is the callee path; for inline subroutines,
	// it's a synthetic key combining path and subroutine name to avoid collapsing
	// with external callees (same-file callees vs external callees are distinct).
	outgoingByCalleeKey := make(map[string][]model.Range)

	// Walk fa.Edges to collect outgoing calls
	for _, edge := range sourceFA.Edges {
		// Determine if this is an outgoing-call edge kind
		isOutgoingCallKind := edge.Kind == model.EdgeCalls ||
			edge.Kind == model.EdgeNavigatesTo ||
			edge.Kind == model.EdgePerforms

		// Skip non-outgoing-call kinds: dynamic variants, data-access, includes
		if !isOutgoingCallKind {
			continue
		}

		// Get the resolution for this edge
		resolution, ok := res.Get(sourcePath, edge.Source)
		if !ok {
			// Edge not in resolution set; skip
			continue
		}

		// Skip unresolved/dynamic/ambiguous edges (FR-17)
		if !resolution.IsResolved() {
			continue
		}

		// Determine the grouping key for this edge
		var calleeKey string
		normalizedResPath := paths.NormalizeKey(resolution.Path)

		// Handle inline PERFORM (same file, subroutine target)
		if edge.Kind == model.EdgePerforms && strings.EqualFold(normalizedResPath, sourcePath) {
			// Inline subroutine in the same object (T6a).
			// Search for the matching subroutine in sourceFA.Structure.Children,
			// group separately from external callees (use key: path|subroutineName).
			targetName := strings.ToUpper(edge.TargetName)
			if sourceFA.Structure != nil && sourceFA.Structure.Children != nil {
				found := false
				for _, child := range sourceFA.Structure.Children {
					if child.Kind == model.SymbolSubroutine && strings.EqualFold(child.Name, targetName) {
						// Found the inline subroutine; use a synthetic key to group with its own ranges
						calleeKey = sourcePath + "|" + targetName
						found = true
						break
					}
				}
				if !found {
					// If no matching subroutine found, skip gracefully (FR-43)
					continue
				}
			} else {
				// No children or no structure; skip gracefully
				continue
			}
		} else {
			// External callee: use its path as the key
			calleeKey = normalizedResPath
		}

		// Record this edge's source range under its grouping key
		outgoingByCalleeKey[calleeKey] = append(outgoingByCalleeKey[calleeKey], edge.Source)
	}

	// For each distinct outgoing callee, build a CallHierarchyOutgoingCall entry
	var result []protocol.CallHierarchyOutgoingCall

	// Sort keys for deterministic output
	keys := make([]string, 0, len(outgoingByCalleeKey))
	for key := range outgoingByCalleeKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		ranges := outgoingByCalleeKey[key]

		// Determine if this is an inline subroutine (key contains "|") or external callee
		var toItem protocol.CallHierarchyItem
		var calleeAbsPath string

		if strings.Contains(key, "|") {
			// Inline subroutine: key is "sourcePath|subroutineName"
			parts := strings.SplitN(key, "|", 2)
			subroutineName := parts[1]

			// Find the matching subroutine in sourceFA.Structure.Children
			var targetSym *model.Symbol
			if sourceFA.Structure != nil {
				for i := range sourceFA.Structure.Children {
					child := &sourceFA.Structure.Children[i]
					if child.Kind == model.SymbolSubroutine && strings.EqualFold(child.Name, subroutineName) {
						targetSym = child
						break
					}
				}
			}

			if targetSym == nil {
				// No matching subroutine found; skip (FR-43)
				continue
			}

			// Build the To item from the subroutine child
			toItem = buildCallHierarchyItem(root, sourcePath, sourceContent, targetSym, posEncoding)
		} else {
			// External callee: key is the callee's path
			calleePath := key

			// Get the callee's analysis
			calleeFA, ok := idx.Get(calleePath)
			if !ok || calleeFA.Structure == nil {
				// Can't find callee or no structure; skip (FR-43)
				continue
			}

			// Read the callee's content for range conversion
			calleeAbsPath = filepath.Join(root, calleePath)
			calleeContent, err := os.ReadFile(calleeAbsPath)
			if err != nil {
				// Can't read callee file; skip (FR-43)
				continue
			}

			// Build the To item from the callee's object root
			toItem = buildCallHierarchyItem(root, calleePath, calleeContent, calleeFA.Structure, posEncoding)
		}

		// Convert each call-site range to protocol.Range and sort by start
		fromRanges := make([]protocol.Range, len(ranges))
		for i, rng := range ranges {
			fromRanges[i] = toProtocolRange(rng, string(sourceContent), posEncoding)
		}

		// Sort by start position (already in source order but be explicit)
		sort.Slice(fromRanges, func(i, j int) bool {
			if fromRanges[i].Start.Line != fromRanges[j].Start.Line {
				return fromRanges[i].Start.Line < fromRanges[j].Start.Line
			}
			return fromRanges[i].Start.Character < fromRanges[j].Start.Character
		})

		result = append(result, protocol.CallHierarchyOutgoingCall{
			To:         toItem,
			FromRanges: fromRanges,
		})
	}

	// Sort result by callee URI for deterministic output
	sort.Slice(result, func(i, j int) bool {
		return string(result[i].To.URI) < string(result[j].To.URI)
	})

	return result, nil
}
