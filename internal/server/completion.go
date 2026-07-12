package server

import (
	"os"
	"path"
	"strings"
	"unicode"

	"go.lsp.dev/protocol"

	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// completionKind classifies the context in which completion is triggered.
// It is a discriminated union that carries context-specific metadata
// (object type expectation for module contexts, DDM name for DDM-field contexts).
type completionKind struct {
	kind    completionKindType
	objType model.ObjectType // populated for module contexts to encode which object type is expected
	ddmName string           // set only for ctxDDMField
}

// completionKindType is the underlying enum for completionKind.
type completionKindType int

const (
	// ctxNoneType indicates no recognized completion context.
	ctxNoneType completionKindType = iota
	// ctxSubroutineType indicates PERFORM context (external subroutine).
	// Note: CALLNAT uses a different approach - see newSubroutineContext.
	ctxSubroutineType
	// ctxProgramType indicates FETCH or RUN context.
	ctxProgramType
	// ctxCopycodeType indicates INCLUDE context.
	ctxCopycodeType
	// ctxDDMFieldType indicates a data-access context with a named view.
	ctxDDMFieldType
)

// Convenience constructors for the sentinels.
var (
	ctxNone       = completionKind{kind: ctxNoneType, objType: model.ObjectUnknown}
	ctxSubroutine = completionKind{kind: ctxSubroutineType, objType: model.ObjectSubprogram}
	ctxProgram    = completionKind{kind: ctxProgramType, objType: model.ObjectProgram}
	ctxCopycode   = completionKind{kind: ctxCopycodeType, objType: model.ObjectCopycode}
)

// newDDMFieldContext creates a ctxDDMField context carrying a DDM name.
func newDDMFieldContext(ddmName string) completionKind {
	return completionKind{kind: ctxDDMFieldType, ddmName: ddmName, objType: model.ObjectUnknown}
}

// newSubroutineContext creates a subroutine context with the specified object type.
// Used to distinguish CALLNAT (ObjectSubprogram) from PERFORM (ObjectExternalSubroutine).
func newSubroutineContext(objType model.ObjectType) completionKind {
	return completionKind{kind: ctxSubroutineType, objType: objType}
}

// ObjectType returns the expected object type for this completion context.
// For module contexts, this is populated to distinguish the specific target type.
func (k completionKind) ObjectType() model.ObjectType {
	return k.objType
}

// DDMName returns the view/DDM name if this is a ctxDDMField context, else empty string.
func (k completionKind) DDMName() string {
	return k.ddmName
}

// detectCompletionContext inspects the text of the current line up to the cursor
// and classifies what to complete. It is a pure function, never panics.
//
// Returns:
// - completionKind: the classification (ctxNone, ctxSubroutine, ctxProgram, ctxCopycode, ctxDDMField)
// - prefix: the partial token already typed, with leading quote stripped and uppercased
//
// This is a prefix scan (not a re-parse), chosen for robustness on incomplete/unparseable input.
func detectCompletionContext(line string, cursorByteCol int) (completionKind, string) {
	// Clamp cursor position to valid range
	if cursorByteCol < 0 {
		cursorByteCol = 0
	}
	if cursorByteCol > len(line) {
		cursorByteCol = len(line)
	}

	// Extract the substring up to the cursor
	prefix := line[:cursorByteCol]

	// Tokenize and find the first (leading) keyword
	tokens := tokenizeLine(prefix)
	if len(tokens) == 0 {
		return ctxNone, ""
	}

	// Normalize the first token for keyword matching
	firstKeyword := strings.ToUpper(tokens[0])

	// Extract the partial prefix (the last token after the first keyword, or empty if cursor is right after whitespace)
	var partialPrefix string
	if len(tokens) > 1 {
		// There are tokens after the keyword
		// Check if the prefix ends with whitespace (meaning cursor is after whitespace)
		endsWithWhitespace := len(prefix) == 0 || unicode.IsSpace(rune(prefix[len(prefix)-1]))
		if endsWithWhitespace {
			partialPrefix = ""
		} else {
			lastToken := tokens[len(tokens)-1]
			partialPrefix = extractPartialPrefix(lastToken)
		}
	} else if len(tokens) == 1 {
		// Only the keyword, no tokens after it
		// Check if the prefix ends with whitespace
		endsWithWhitespace := len(prefix) == 0 || unicode.IsSpace(rune(prefix[len(prefix)-1]))
		if endsWithWhitespace {
			partialPrefix = ""
		}
		// If doesn't end with whitespace, the keyword itself is the only token, so prefix is ""
	}

	// Classify context based on the leading keyword
	// But only if there's something after the keyword (at least whitespace)
	if len(tokens) == 1 && len(prefix) == len(tokens[0]) {
		// Only the keyword, no space or operand after it
		return ctxNone, ""
	}

	switch firstKeyword {
	case "CALLNAT":
		return ctxSubroutine, partialPrefix
	case "FETCH":
		// Handle FETCH REPEAT and FETCH RETURN (they are just modifiers, not used in context detection)
		return ctxProgram, partialPrefix
	case "RUN":
		return ctxProgram, partialPrefix
	case "INCLUDE":
		return ctxCopycode, partialPrefix
	case "PERFORM":
		return newSubroutineContext(model.ObjectExternalSubroutine), partialPrefix
	}

	// Check for data-access verbs (READ, FIND, GET, STORE, UPDATE, DELETE)
	if isDataAccessVerb(firstKeyword) {
		// Extract the DDM/view name (second token)
		if len(tokens) >= 2 {
			ddmName := strings.ToUpper(tokens[1])
			// Strip leading quote if present
			ddmName = strings.TrimPrefix(ddmName, "'")
			return newDDMFieldContext(ddmName), partialPrefix
		}
		// If no second token yet, return DDM field context with empty DDM name
		return newDDMFieldContext(""), partialPrefix
	}

	// Unrecognized context
	return ctxNone, ""
}

// tokenizeLine splits a line into whitespace-delimited tokens.
// A token is a sequence of non-whitespace characters.
func tokenizeLine(prefix string) []string {
	var tokens []string
	var current strings.Builder

	for i, ch := range prefix {
		if unicode.IsSpace(ch) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(ch)
		}

		// Handle end of string
		if i == len(prefix)-1 && current.Len() > 0 {
			tokens = append(tokens, current.String())
		}
	}

	return tokens
}

// extractPartialPrefix extracts the partial token, stripping a leading quote if present, and uppercasing it.
// Natural identifiers may have sigils (#, &, +), which are preserved.
func extractPartialPrefix(token string) string {
	// Strip leading quote if present
	if strings.HasPrefix(token, "'") {
		token = token[1:]
	}

	// Uppercase the entire token (preserves sigils)
	return strings.ToUpper(token)
}

// isDataAccessVerb returns true if the keyword is a data-access verb.
func isDataAccessVerb(keyword string) bool {
	switch keyword {
	case "READ", "FIND", "GET", "STORE", "UPDATE", "DELETE":
		return true
	}
	return false
}

// objectTypeToCompletionKind maps model.ObjectType to protocol.CompletionItemKind.
// Used to distinguish completion items by the object they represent.
// Mapping for module contexts (T4):
//   - ObjectSubprogram → CompletionItemKindModule
//   - ObjectProgram → CompletionItemKindFile
//   - ObjectCopycode → CompletionItemKindReference
func objectTypeToCompletionKind(ot model.ObjectType) protocol.CompletionItemKind {
	switch ot {
	case model.ObjectSubprogram:
		return protocol.CompletionItemKindModule
	case model.ObjectProgram:
		return protocol.CompletionItemKindFile
	case model.ObjectCopycode:
		return protocol.CompletionItemKindReference
	case model.ObjectExternalSubroutine:
		return protocol.CompletionItemKindFunction
	case model.ObjectDDM:
		return protocol.CompletionItemKindStruct
	default:
		return protocol.CompletionItemKindText
	}
}

// isDynamicPrefix checks if a prefix starts with a sigil (#, &, or +),
// indicating a dynamic (variable) operand.
// Dynamic operands are unresolvable and excluded from completion (FR-17/FR-18).
func isDynamicPrefix(prefix string) bool {
	if len(prefix) == 0 {
		return false
	}
	first := prefix[0]
	return first == '#' || first == '&' || first == '+'
}

// provideCompletion handles the textDocument/completion request (feature 16, T4).
// It is the LSP provider entry point for completion support.
//
// Behavior (T4 — module contexts only):
// - Snapshot idx/posEncoding under RLock, release before I/O (F7).
// - Read the open buffer first (store-first pattern) for the partial line.
// - Derive (line, cursorByteCol) from params.Position using fromProtocolPosition + lineAt.
// - Call detectCompletionContext(line, cursorByteCol) to classify the context.
// - For module contexts (CALLNAT, FETCH, RUN, INCLUDE):
//   - Map the context's expected object type.
//   - Query idx.NamesWithPrefix(prefix, objType, relPath, &cfg).
//   - Build CompletionItem per candidate with Label, Kind, and Detail.
//
// - For PERFORM/DDM-field contexts and ctxNone, return an empty (non-nil) list for now.
// - Never panics; unreadable/missing/out-of-root/no-context → empty list, nil error.
//
// Concurrency (F7): snapshots idx/posEncoding under RLock and releases before any I/O.
func provideCompletion(hctx *handlerContext, params protocol.CompletionParams) ([]protocol.CompletionItem, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return []protocol.CompletionItem{}, nil
	}

	// Snapshot idx/posEncoding under the read lock; release before any file I/O (F7).
	hctx.idxResMu.RLock()
	idx := hctx.idx
	posEncoding := hctx.posEncoding
	hctx.idxResMu.RUnlock()

	// Convert LSP URI to workspace-relative path
	absPath, relPath, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root
		return []protocol.CompletionItem{}, nil
	}

	// Resolve the document: open buffer first (store-first pattern — required because
	// completion fires while typing), else index snapshot.
	var content string
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil {
			content = string(doc.Content)
		}
	}

	// Fallback to reading from disk if not open
	if content == "" {
		if idx == nil {
			return []protocol.CompletionItem{}, nil
		}
		b, err := os.ReadFile(absPath)
		if err != nil {
			return []protocol.CompletionItem{}, nil
		}
		content = string(b)
	}

	// Derive the cursor position in model coordinates (1-based line, byte column)
	modelPos := fromProtocolPosition(params.Position, content, posEncoding)
	line := lineAt(content, modelPos.Line-1) // lineAt takes 0-based line index
	cursorByteCol := modelPos.Column - 1     // Convert 1-based column to 0-based byte offset

	// Clamp cursor to line bounds
	if cursorByteCol < 0 {
		cursorByteCol = 0
	}
	if cursorByteCol > len(line) {
		cursorByteCol = len(line)
	}

	// Detect the completion context
	kind, prefix := detectCompletionContext(line, cursorByteCol)

	// T4: Handle module contexts (CALLNAT, FETCH, RUN, INCLUDE)
	// T6: Handle PERFORM subroutine context
	// For other contexts, return empty list (T7/T8 will implement them)
	switch kind.kind {
	case ctxSubroutineType:
		if kind.objType == model.ObjectSubprogram {
			// T4: CALLNAT context (ObjectSubprogram)
			return provideModuleCompletion(idx, &hctx.cfg, relPath, prefix, kind.objType)
		}
		// T6: PERFORM context (ObjectExternalSubroutine)
		if kind.objType == model.ObjectExternalSubroutine {
			// Dynamic guard (AC3, FR-17/FR-18): if prefix is dynamic, return empty (no error, no diagnostic)
			if isDynamicPrefix(prefix) {
				return []protocol.CompletionItem{}, nil
			}
			// Get the open buffer's analysis for inline subroutines (store-first)
			var callerAnalysis *model.FileAnalysis
			if hctx.store != nil {
				if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil {
					callerAnalysis = &doc.Analysis
				}
			}
			// Fall back to index snapshot if store miss or store is nil
			if callerAnalysis == nil && idx != nil {
				if entry, ok := idx.Get(relPath); ok {
					callerAnalysis = &entry
				}
			}
			// Collect inline subroutines from the caller's structure (AC1)
			var items []protocol.CompletionItem
			if callerAnalysis != nil && callerAnalysis.Structure != nil {
				for _, child := range callerAnalysis.Structure.Children {
					if child.Kind == model.SymbolSubroutine {
						// Case-insensitive prefix match
						childNameUpper := strings.ToUpper(child.Name)
						prefixUpper := strings.ToUpper(prefix)
						if strings.HasPrefix(childNameUpper, prefixUpper) {
							item := protocol.CompletionItem{
								Label: child.Name,
								Kind:  protocol.CompletionItemKindFunction,
							}
							items = append(items, item)
						}
					}
				}
			}
			// Append external subroutines (AC2)
			externalItems, err := provideModuleCompletion(idx, &hctx.cfg, relPath, prefix, model.ObjectExternalSubroutine)
			if err != nil {
				// Non-nil error means serious problem; return what we have (inline candidates at minimum)
				if len(items) == 0 {
					return []protocol.CompletionItem{}, nil
				}
				return items, nil
			}
			// Append external items after inline items (ensures inline-before-external ordering, AC1/AC2)
			items = append(items, externalItems...)
			// Return non-nil even if empty
			if items == nil {
				items = []protocol.CompletionItem{}
			}
			return items, nil
		}
		// Should not reach here, but return empty for any other ObjectType
		return []protocol.CompletionItem{}, nil

	case ctxProgramType:
		// FETCH / RUN contexts
		return provideModuleCompletion(idx, &hctx.cfg, relPath, prefix, kind.objType)

	case ctxCopycodeType:
		// INCLUDE context
		return provideModuleCompletion(idx, &hctx.cfg, relPath, prefix, kind.objType)

	default:
		// ctxNone, ctxDDMFieldType: deferred to T7/T8
		return []protocol.CompletionItem{}, nil
	}
}

// provideModuleCompletion handles the module-context cases (CALLNAT, FETCH, RUN, INCLUDE).
// It queries the index for candidates matching the prefix and expected object type,
// and builds CompletionItem entries for each.
func provideModuleCompletion(idx *workspace.Index, cfg *config.Config, relPath, prefix string, objType model.ObjectType) ([]protocol.CompletionItem, error) {
	if idx == nil {
		return []protocol.CompletionItem{}, nil
	}

	// Query the index for reachable candidates matching the prefix and type
	candidates := idx.NamesWithPrefix(prefix, objType, relPath, cfg)

	// Build CompletionItem for each candidate
	var items []protocol.CompletionItem
	for _, cand := range candidates {
		// Extract object name from the candidate's path (mirroring objectIdentity logic)
		objName := extractObjectName(cand.Path)

		item := protocol.CompletionItem{
			Label:  objName,
			Kind:   objectTypeToCompletionKind(cand.Type),
			Detail: protocol.NewOptional[string](objectTypeLabel(cand.Type)),
		}
		items = append(items, item)
	}

	// Return non-nil even if empty (never nil)
	if items == nil {
		items = []protocol.CompletionItem{}
	}
	return items, nil
}

// extractObjectName derives the object name from a workspace-relative path.
// It mirrors the logic of objectIdentity (internal/workspace/resolution.go) but
// takes only the path, not the config (we don't need library info for the name).
// path.Base / path.Ext operate on slash-separated keys, not OS file paths.
func extractObjectName(relPath string) string {
	base := path.Base(relPath)
	ext := path.Ext(base)
	return strings.ToUpper(strings.TrimSuffix(base, ext))
}
