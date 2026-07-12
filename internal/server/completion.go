package server

import (
	"strings"
	"unicode"

	"go.lsp.dev/protocol"

	"natural-lsp/internal/model"
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

// provideCompletion is the stub provider for the textDocument/completion request (feature 16, T3).
// During the RED phase, it returns an empty list.
// In T4–T8, this function will be expanded to compute real completions based on context.
//
// Parameters:
//   - hctx: handler context (will be used in T4+ for index/store access)
//   - params: the CompletionParams from the client
//
// Returns:
//   - []protocol.CompletionItem: completion items (empty list for RED phase stub)
//   - error: nil for success, error message otherwise
func provideCompletion(hctx *handlerContext, params protocol.CompletionParams) ([]protocol.CompletionItem, error) {
	// Feature 16, T3 RED phase: stub returns empty list.
	// T4–T8 will implement real completion logic.
	return []protocol.CompletionItem{}, nil
}
