package server

import (
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
	"github.com/dkrieg/natural-lsp/internal/workspace"
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

	// Handle VIEW OF field navigation FIRST (feature 28, T8b, priority before use-sites).
	// If the cursor is on a data-field declaration inside a VIEW OF, resolve per the view binding.
	if declTarget := findDeclarationTarget(sourceFA, cursorPos); declTarget != nil {
		if locations, err := provideDefinitionForViewField(hctx, declTarget, absPath, relPath); locations != nil {
			// VIEW field navigation succeeded; return the location
			return locations, err
		}
		// Not a VIEW field, or VIEW resolution failed; continue to other handlers below
	}

	// Find the edge (data-access, or variable ref) at the cursor position
	// Pass content and analyzer for on-demand variable ref extraction
	edge, dataAccess, varRef := findCursorTarget(sourceFA, cursorPos, string(sourceContent), hctx.az)

	// Handle DDM data-access case (feature 27 T9): resolve SQL-sourced DDM table names
	if dataAccess != nil {
		// Acquire read lock to read idx safely
		hctx.idxResMu.RLock()
		idx := hctx.idx
		hctx.idxResMu.RUnlock()

		if idx != nil {
			loc := provideDDMDefinition(hctx.root, relPath, dataAccess, idx, &hctx.cfg, hctx.posEncoding)
			if loc != nil {
				return []protocol.Location{*loc}, nil
			}
		}
		// Unresolved DDM; return empty (FR-17)
		return nil, nil
	}

	// Handle variable reference case (feature 27 T3)
	if varRef != nil {
		// First try same-file resolution
		locations := resolveVariableDefinition(varRef, &sourceFA, string(sourceContent), absPath, hctx.posEncoding)
		if len(locations) > 0 {
			return locations, nil
		}

		// If not found in same file and we have an index, try cross-file USING resolution (feature 27 T7)
		if idx != nil && len(sourceFA.DataAreaRefs) > 0 {
			locations := resolveVariableDefinitionCrossFile(varRef, &sourceFA, relPath, idx, hctx.root, string(sourceContent), hctx.posEncoding, &hctx.cfg)
			if len(locations) > 0 {
				return locations, nil
			}
		}

		// Not found anywhere; return empty (FR-17)
		return nil, nil
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

	// If root is relative, convert to absolute for proper path computation
	var rootAbs string
	if filepath.IsAbs(root) {
		rootAbs = root
	} else {
		var absErr error
		rootAbs, absErr = filepath.Abs(root)
		if absErr != nil {
			return "", "", absErr
		}
	}

	// Ensure absPath is absolute (uri.FsPath yields an absolute path for a valid
	// file URI, but be defensive against a relative path slipping through).
	if !filepath.IsAbs(absPath) {
		var absErr error
		absPath, absErr = filepath.Abs(absPath)
		if absErr != nil {
			return "", "", absErr
		}
	}

	relPath, err = filepath.Rel(rootAbs, absPath)
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

	// The leaf variable name at the cursor. The T2 scanner emits a group-qualified
	// reference #GROUP.#FIELD as SEPARATE tokens (#GROUP, then #FIELD) with the '.'
	// dropped, so varRef.Name is just the leaf ("#FIELD"). Recover the qualifier by
	// reading back through the source: if the token immediately preceding this ref
	// (across a single '.') is an identifier, that identifier is the level-1 group
	// qualifier and resolution must be scoped to that group's sub-fields.
	varName := varRef.Name
	groupName := qualifierBeforeRef(content, varRef.Range)

	// Matching: find the declaration(s) with the matching name.
	//   - Qualified (groupName != ""): match only the sub-field WITHIN that level-1
	//     group, so #GROUP-A.#FIELD resolves to #GROUP-A's #FIELD, not #GROUP-B's.
	//   - Unqualified (groupName == ""): collect ALL matching declarations (a name
	//     present in multiple groups is genuinely ambiguous → all candidates).
	var candidates []protocol.Location
	findDeclaration(sourceFA.Definitions, varName, groupName, func(decl *model.DataDefinition) {
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

// qualifierBeforeRef reconstructs the level-1 group qualifier of a group-qualified
// variable reference (#GROUP.#FIELD) from the source content.
//
// The T2 scanner emits #GROUP and #FIELD as separate tokens with the '.' dropped,
// so a leaf ref carries no group context. This helper reads the source immediately
// to the left of leafRange.Start: it skips at most a single '.' (optionally
// surrounded by inline whitespace) and, if found, reads back the contiguous
// identifier before it. That identifier (uppercased, matching the Name convention)
// is returned as the qualifier. When there is no preceding '.', it returns "" —
// the reference is unqualified.
//
// leafRange is 1-based inclusive (model coordinates). Only the reference's own line
// is inspected (a qualified reference never spans a line break between name and dot
// in practice). Robust against out-of-range positions (returns "" — FR-43).
func qualifierBeforeRef(content string, leafRange model.Range) string {
	lines := strings.Split(content, "\n")
	lineIdx := leafRange.Start.Line - 1
	if lineIdx < 0 || lineIdx >= len(lines) {
		return ""
	}
	line := lines[lineIdx]
	// Strip a trailing '\r' so column math matches the source (CRLF tolerance).
	line = strings.TrimSuffix(line, "\r")

	// Byte index just before the leaf token's first character (1-based → 0-based,
	// then step one left). Column is a 1-based byte offset per the model convention.
	i := leafRange.Start.Column - 2
	if i < 0 || i >= len(line) {
		return ""
	}

	// Skip inline whitespace immediately before the leaf.
	for i >= 0 && (line[i] == ' ' || line[i] == '\t') {
		i--
	}
	// Require a single '.' separator.
	if i < 0 || line[i] != '.' {
		return ""
	}
	i--
	// Skip inline whitespace between the '.' and the group identifier.
	for i >= 0 && (line[i] == ' ' || line[i] == '\t') {
		i--
	}
	// Read back the contiguous identifier (Natural identifiers: letters, digits,
	// '-', and the sigils '#', '&', '@', '+'). end is inclusive.
	end := i
	for i >= 0 && isIdentifierByte(line[i]) {
		i--
	}
	start := i + 1
	if start > end {
		return ""
	}
	return strings.ToUpper(line[start : end+1])
}

// isIdentifierByte reports whether b can appear in a Natural identifier token as
// emitted by the lexer (letters, digits, hyphen, and the identifier sigils).
func isIdentifierByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z',
		b >= 'a' && b <= 'z',
		b >= '0' && b <= '9':
		return true
	case b == '-' || b == '#' || b == '&' || b == '@' || b == '+' || b == '_':
		return true
	default:
		return false
	}
}

// resolveVariableDefinitionCrossFile resolves a variable via cross-file USING references.
// It searches the source file's DataAreaRefs and attempts to resolve each USING reference
// via the workspace resolver (workspace.ResolveDataAreaField), returning the field's location
// in the resolved data-area object if found.
// Returns nil if no USING reference resolves the variable, per FR-17.
// Feature 27, T7.
func resolveVariableDefinitionCrossFile(
	varRef *model.VariableRef,
	sourceFA *model.FileAnalysis,
	referencingRelPath string,
	idx *workspace.Index,
	root string,
	content string,
	enc protocol.PositionEncodingKind,
	cfg *config.Config,
) []protocol.Location {
	if idx == nil || cfg == nil || len(sourceFA.DataAreaRefs) == 0 {
		return nil
	}

	// Try each USING reference in the source file's data-area refs
	for _, dataAreaRef := range sourceFA.DataAreaRefs {
		// Resolve the field AND the chain-selected data-area object path together.
		// The location-aware helper keeps the field range and the object path
		// consistent (both come from the same steplib-chain winner) and avoids a
		// separate unfiltered candidates[0] pick that would bypass the chain
		// (unreachable-exclusion).
		fieldRange, dataAreaPath := workspace.ResolveDataAreaFieldLocation(varRef.Name, dataAreaRef, idx, referencingRelPath, cfg)

		// If the field was found, build a Location in the data-area object
		if fieldRange.Start.Line > 0 || fieldRange.End.Line > 0 {
			if dataAreaPath == "" {
				// Couldn't locate the data-area object via the chain; skip
				continue
			}

			// Read the data-area object's content for position conversion
			dataAreaAbsPath := filepath.Join(root, dataAreaPath)
			dataAreaContent, err := os.ReadFile(dataAreaAbsPath)
			if err != nil {
				// Can't read data-area file; skip
				continue
			}

			// Build a Location pointing to the field in the data-area object
			loc := protocol.Location{
				URI:   uri.File(dataAreaAbsPath),
				Range: toProtocolRange(fieldRange, string(dataAreaContent), enc),
			}
			return []protocol.Location{loc}
		}
	}

	return nil
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

// provideDDMDefinition resolves a DDM data-access entry to its .NSD definition.
// It resolves the DDM name via idx.LookupByName and returns a Location pointing
// to the DDM file's object root (Structure.SelectionRange).
// Returns nil if the DDM cannot be resolved (FR-17).
// Feature 27, T9.
func provideDDMDefinition(
	root string,
	referencingRelPath string,
	dataAccess *model.DataAccessEntry,
	idx *workspace.Index,
	cfg *config.Config,
	enc protocol.PositionEncodingKind,
) *protocol.Location {
	if dataAccess == nil || dataAccess.Name == "" {
		return nil
	}

	// Resolve the DDM name to its .NSD path via the steplib chain (non-transitive).
	// idx.LookupByName returns ALL name+type matches UNFILTERED — picking candidates[0]
	// bypasses the chain and can land on a same-named DDM in a non-chain library.
	// workspace.ResolveDDMPath applies buildSearchChain/resolveViaChain so an
	// unreachable copy is excluded.
	targetPath := workspace.ResolveDDMPath(dataAccess.Name, idx, referencingRelPath, cfg)
	if targetPath == "" {
		// DDM not found or not reachable via the chain; return nil (FR-17)
		return nil
	}

	// Fetch the DDM file's analysis
	ddmFA, ok := idx.Get(targetPath)
	if !ok {
		return nil
	}

	// Read the DDM file content for range conversion
	ddmAbsPath := filepath.Join(root, targetPath)
	ddmContent, err := os.ReadFile(ddmAbsPath)
	if err != nil {
		return nil
	}

	// Build the Location using definitionLocation helper
	loc := definitionLocation(root, targetPath, ddmFA, string(ddmContent), enc)
	return &loc
}

// provideDefinitionForViewField handles go-to-definition for VIEW OF field declarations (feature 28, T8b).
// Given a declaration target (from findDeclarationTarget), it determines whether the cursor is on:
//
// 1. A VIEW NAME node (Symbol.ViewOfDDM != ""): navigate to the view's own line (same-file).
// 2. A bare view field (no local type): navigate to the DDM field's NameRange in the .NSD.
// 3. A restated view field: navigate to the DDM field.
// 4. A view-local REDEFINE sub-field (Symbol.Redefines != ""): navigate to same-file REDEFINE line.
// 5. Non-view field: no special handling, return nil (let the caller handle edges).
//
// Returns a Location slice on success, nil on no-match or modeled gap (FR-17, FR-43).
func provideDefinitionForViewField(hctx *handlerContext, declTarget *DeclarationTarget, absPath, relPath string) ([]protocol.Location, error) {
	if declTarget == nil || declTarget.Symbol == nil {
		return nil, nil
	}

	sym := declTarget.Symbol

	// Case 1: VIEW NAME node (symbol itself has ViewOfDDM) — navigate to own SelectionRange (same-file)
	if sym.ViewOfDDM != "" && sym.Kind == model.SymbolDataField {
		// Read the source file content for position conversion
		content, err := os.ReadFile(absPath)
		if err != nil {
			return nil, nil // FR-43: graceful degradation
		}

		loc := protocol.Location{
			URI:   uri.File(absPath),
			Range: toProtocolRange(sym.SelectionRange, string(content), hctx.posEncoding),
		}
		return []protocol.Location{loc}, nil
	}

	// For the remaining cases (field within a view), we need an owning VIEW OF node
	if declTarget.OwningView == nil || declTarget.OwningView.ViewOfDDM == "" {
		// Not a view field, or view context not available — return nil (no special handling)
		return nil, nil
	}

	// Case 4: view-local REDEFINE sub-field (Redefines != "" inside a view)
	// Navigate to the REDEFINE line itself (same-file), not the DDM
	if sym.Redefines != "" {
		content, err := os.ReadFile(absPath)
		if err != nil {
			return nil, nil // FR-43
		}

		loc := protocol.Location{
			URI:   uri.File(absPath),
			Range: toProtocolRange(sym.SelectionRange, string(content), hctx.posEncoding),
		}
		return []protocol.Location{loc}, nil
	}

	// Cases 2 & 3: bare or restated view field — resolve to the DDM field via steplib chain
	// Acquire read lock to access idx safely
	hctx.idxResMu.RLock()
	idx := hctx.idx
	hctx.idxResMu.RUnlock()

	if idx == nil {
		return nil, nil // Index not ready; graceful degradation (FR-43)
	}

	// Resolve the DDM field location via the steplib chain
	ddmFieldRange, ddmPath := workspace.ResolveDDMFieldLocation(
		sym.Name,
		declTarget.OwningView.ViewOfDDM,
		idx,
		relPath,
		&hctx.cfg,
	)

	if ddmFieldRange == (model.Range{}) || ddmPath == "" {
		// DDM unresolved, TYPE: SQL, or field absent → return empty (FR-17, FR-43)
		return nil, nil
	}

	// Read the DDM file content for range conversion
	ddmAbsPath := filepath.Join(hctx.root, ddmPath)
	ddmContent, err := os.ReadFile(ddmAbsPath)
	if err != nil {
		return nil, nil
	}

	// Convert the field range to protocol coordinates
	loc := protocol.Location{
		URI:   uri.File(ddmAbsPath),
		Range: toProtocolRange(ddmFieldRange, string(ddmContent), hctx.posEncoding),
	}
	return []protocol.Location{loc}, nil
}
