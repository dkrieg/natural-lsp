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

// provideDefinition handles the textDocument/definition request (feature 10, T7 + feature 27 T3).
// It is the LSP provider entry point: it decodes the cursor position from the params,
// looks up any reference at that position, resolves it to a definition, and returns
// the target location(s).
//
// For a resolved edge:
// - If the target is in the same file (inline PERFORM), use the subroutine's SelectionRange.
// - Otherwise, use the target file's object-root Structure.SelectionRange.
// For a variable reference:
// - Resolve the variable to a DEFINE DATA declaration (same-file, intra-object matcher).
// - Return the declaration's NameRange as Location.
// For unresolved or dynamic targets, returns empty (no error — FR-17, FR-43).
//
// Store-first: for variable references, reads the open buffer first (live edits), falling
// back to disk/index. This ensures the variable refs are consistent with the live content.
//
// Concurrency (F7): holds the read lock on idxResMu to ensure consistent access to idx/res
// (applyDocumentChange swaps these pointers under the write lock, so handlers see
// a stable snapshot for the duration of a request).
func provideDefinition(hctx *handlerContext, params protocol.DefinitionParams) ([]protocol.Location, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Convert LSP URI to workspace-relative path (forward-slash index key convention)
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root — no definition
		return nil, nil
	}

	// Acquire read lock to read idx/res safely (applyDocumentChange holds write lock when updating)
	hctx.idxResMu.RLock()
	idx, res := hctx.idx, hctx.res
	hctx.idxResMu.RUnlock()

	// Get the source file's analysis (store-first for variables only)
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
		// we can't serve disk files (variable navigation requires index context
		// or an open buffer). Return nil to degrade gracefully per FR-43.
		if idx == nil {
			return nil, nil
		}

		var ok bool
		sourceFA, ok = idx.Get(relPath)
		if !ok {
			// Source file not in index — no definition
			return nil, nil
		}

		// Read the source file content for position conversion
		var readErr error
		sourceContent, readErr = os.ReadFile(absPath)
		if readErr != nil {
			// Can't read source; no definition
			return nil, nil
		}
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), hctx.posEncoding)

	// Find the edge (data-access, or variable ref) at the cursor position
	// Pass content and analyzer for on-demand variable ref extraction
	edge, _, varRef := findCursorTarget(sourceFA, cursorPos, string(sourceContent), hctx.az)

	// Handle variable reference case (feature 27 T3)
	if varRef != nil {
		locations := resolveVariableDefinition(varRef, &sourceFA, string(sourceContent), absPath, hctx.posEncoding)
		return locations, nil
	}

	// Handle variable declaration case (feature 27 T3, idempotent):
	// When the cursor is on a declaration's NameRange, resolve to itself.
	// This handles the case where ExtractVariableRefs deliberately skips declarations.
	if decl := findVariableDeclarationAtCursor(&sourceFA, cursorPos); decl != nil {
		loc := protocol.Location{
			URI:   uri.File(absPath),
			Range: toProtocolRange(decl.NameRange, string(sourceContent), hctx.posEncoding),
		}
		return []protocol.Location{loc}, nil
	}

	if edge == nil {
		// No edge or variable at cursor position — no definition
		return nil, nil
	}

	// Edge case: if no resolution set, can't resolve edge
	if res == nil {
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

// provideDefinitionVariableFromStore is a store-first-only path for variable definitions
// when the index is not yet ready (cold start, feature 21). It extracts variable refs
// from the live buffer and resolves them to declarations.
func provideDefinitionVariableFromStore(hctx *handlerContext, params protocol.DefinitionParams, absPath, relPath string) ([]protocol.Location, error) {
	if hctx.store == nil {
		return nil, nil
	}

	doc, ok := hctx.store.Get(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}

	// Extract variable refs from the live buffer
	var varRefs []model.VariableRef
	if hctx.az != nil {
		varRefs = hctx.az.ExtractVariableRefs(string(doc.Content))
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(doc.Content), hctx.posEncoding)

	// Find the variable ref at the cursor
	var varRef *model.VariableRef
	for i := range varRefs {
		if containsPosition(varRefs[i].Range, cursorPos) {
			varRef = &varRefs[i]
			break
		}
	}

	if varRef == nil {
		return nil, nil
	}

	// Resolve the variable to a definition in the same file
	locations := resolveVariableDefinition(varRef, &doc.Analysis, string(doc.Content), absPath, hctx.posEncoding)
	return locations, nil
}

// containsPosition is a helper to check if a position is contained in a range (1-based, inclusive).
func containsPosition(r model.Range, p model.Position) bool {
	// Check if p is before r.Start
	if p.Line < r.Start.Line || (p.Line == r.Start.Line && p.Column < r.Start.Column) {
		return false
	}
	// Check if p is after r.End
	if p.Line > r.End.Line || (p.Line == r.End.Line && p.Column > r.End.Column) {
		return false
	}
	return true
}

// resolveVariableDefinition resolves a variable ref to its DEFINE DATA declaration.
// It searches the Definitions tree for a matching declaration and returns its NameRange.
// For group-qualified names (#GROUP.FIELD), it matches the sub-field within the group.
// Ambiguous unqualified names (>1 group with a sub-field of the same name) return ALL candidates.
// Modeled gaps (*-system, &-dynamic, undeclared) return empty (nil), per FR-17.
func resolveVariableDefinition(varRef *model.VariableRef, sourceFA *model.FileAnalysis, content, absPath string, enc protocol.PositionEncodingKind) []protocol.Location {
	if sourceFA == nil || len(sourceFA.Definitions) == 0 {
		return nil
	}

	// Parse the variable name: check for group qualification (#GROUP.FIELD)
	varName := varRef.Name

	// Check if the name contains a group qualifier (e.g., #GROUP.#FIELD)
	// The scanner emits group-qualified refs with the full span, so we need to parse it.
	// For now, use a simple approach: if the varRef.Name contains a dot, split on it.
	// (Note: the scanner may emit the full qualified name as a single string, or as separate tokens.
	// Check the actual behavior from ExtractVariableRefs.)
	// For phase A, we'll implement the simple case where varRef.Name is just the field name,
	// and group qualification is handled separately by the caller if needed.
	// TODO (T3): extend this to handle #GROUP.#FIELD parsing.

	// Simple matching: find the declaration with the matching name.
	// For ambiguous (unqualified) names that appear in multiple groups,
	// collect ALL matching declarations.
	var candidates []protocol.Location
	findDeclaration(sourceFA.Definitions, varName, "", func(decl *model.DataDefinition) {
		// Use NameRange if available (feature 27 T1), otherwise fall back to Range
		rng := decl.NameRange
		if rng.Start.Line == 0 && rng.End.Line == 0 {
			// NameRange not populated; use the whole field Range
			rng = decl.Range
		}
		if rng.Start.Line > 0 {
			loc := protocol.Location{
				URI:   uri.File(absPath),
				Range: toProtocolRange(rng, content, enc),
			}
			candidates = append(candidates, loc)
		}
	})

	return candidates
}

// findDeclaration recursively searches a Definitions tree for declarations matching the given name.
// For unqualified names, it returns all matching declarations (e.g., sub-fields in multiple groups).
// For group-qualified names (groupName != ""), it matches only within that group.
// The callback is invoked for each matching declaration.
// To collect all ambiguous candidates, always recurse into all children at all depths.
func findDeclaration(defs []model.DataDefinition, varName, groupName string, callback func(*model.DataDefinition)) {
	for i := range defs {
		decl := &defs[i]

		// If we're looking for a specific group, only match within that group
		if groupName != "" {
			if strings.EqualFold(decl.Name, groupName) && decl.Level == 1 {
				// Found the group; search its children for the field
				findDeclaration(decl.Children, varName, "", callback)
			}
			continue
		}

		// Unqualified name match: invoke callback for this declaration
		if strings.EqualFold(decl.Name, varName) {
			callback(decl)
		}

		// Always recursively search children at any depth (for groups and REDEFINE blocks).
		// This ensures we match declarations at any depth, including:
		// - REDEFINE sub-fields (children of redefined fields)
		// - Sub-fields in multiple groups (when searching unqualified)
		for j := range decl.Children {
			findDeclaration([]model.DataDefinition{decl.Children[j]}, varName, groupName, callback)
		}
	}
}

// findVariableDeclarationAtCursor searches the Definitions tree for a declaration
// whose NameRange contains the cursor position. Returns the first matching declaration,
// or nil if no declaration's NameRange contains the cursor.
// This handles the idempotent case where go-to-definition on a declaration resolves to itself.
func findVariableDeclarationAtCursor(fa *model.FileAnalysis, pos model.Position) *model.DataDefinition {
	if fa == nil || len(fa.Definitions) == 0 {
		return nil
	}

	// Helper to check if a position is contained in a range (1-based, inclusive).
	contains := func(r model.Range, p model.Position) bool {
		if p.Line < r.Start.Line || (p.Line == r.Start.Line && p.Column < r.Start.Column) {
			return false
		}
		if p.Line > r.End.Line || (p.Line == r.End.Line && p.Column > r.End.Column) {
			return false
		}
		return true
	}

	// Recursively search the Definitions tree
	var search func([]model.DataDefinition) *model.DataDefinition
	search = func(defs []model.DataDefinition) *model.DataDefinition {
		for i := range defs {
			decl := &defs[i]

			// Check if the cursor is within this declaration's NameRange
			if contains(decl.NameRange, pos) {
				return decl
			}

			// Recursively search children
			if result := search(decl.Children); result != nil {
				return result
			}
		}
		return nil
	}

	return search(fa.Definitions)
}
