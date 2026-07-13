package server

import (
	"strings"
	"unicode"

	"go.lsp.dev/protocol"
)

// sigContextKind classifies the context in which signature help is triggered.
// It distinguishes CALLNAT, PERFORM, and non-call contexts.
type sigContextKind int

const (
	// sigNone indicates no recognized signature context (not CALLNAT or PERFORM).
	sigNone sigContextKind = iota
	// sigCallnat indicates CALLNAT context.
	sigCallnat
	// sigPerform indicates PERFORM context.
	sigPerform
)

// String returns a human-readable name for the signature context kind.
func (k sigContextKind) String() string {
	switch k {
	case sigNone:
		return "sigNone"
	case sigCallnat:
		return "sigCallnat"
	case sigPerform:
		return "sigPerform"
	default:
		return "unknown"
	}
}

// provideSignatureHelp is the LSP signature help provider stub (feature 17, T1 RED phase).
// It is called when the client requests textDocument/signatureHelp.
// Currently returns nil, nil (no signature at any position), which marshals to JSON "null".
//
// CRITICAL: When wiring the dispatch in server.go, the result MUST be marshaled via
// (*protocol.SignatureHelp).MarshalJSONTo(jsontext.NewEncoder(&buf)) — NOT json.Marshal —
// because SignatureHelp contains union/Nullable fields that require the protocol encoder.
// See the divergence note in tasks.md and server.go handleInitialize for the pattern.
func provideSignatureHelp(hctx *handlerContext, params protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	// Stub: return nil, nil (no signature help at any position during RED phase).
	return nil, nil
}

// detectSignatureContext inspects the text of the current line up to the cursor
// and classifies whether the cursor is in a CALLNAT or PERFORM call context,
// returning the context kind and the 0-based argument index.
//
// The argument index is the count of whitespace-separated argument tokens
// between the target token and the cursor. Natural has no parentheses:
// CALLNAT 'SUB' arg1 arg2 arg3 — tokens after the target are arguments.
//
// Returns:
// - sigContextKind: the classification (sigNone, sigCallnat, sigPerform)
// - argIndex: the 0-based position in the argument list (never negative)
//
// This is a pure function, never panics. Degenerate input (empty line, cursor
// past end, out-of-range column) is clamped and returns sigNone.
func detectSignatureContext(line string, cursorByteCol int) (sigContextKind, int) {
	// If cursor is way past the end (more than 1 byte beyond len(line)), not a valid context
	if cursorByteCol > len(line)+1 {
		return sigNone, 0
	}

	// Clamp cursor position to valid range [0, len(line)]
	if cursorByteCol < 0 {
		cursorByteCol = 0
	}
	if cursorByteCol > len(line) {
		cursorByteCol = len(line)
	}

	// Extract the substring up to the cursor
	prefix := line[:cursorByteCol]

	// Tokenize the prefix
	tokens := tokenizeLine(prefix)
	if len(tokens) == 0 {
		return sigNone, 0
	}

	// Normalize the first token for keyword matching
	firstKeyword := strings.ToUpper(tokens[0])

	// Only CALLNAT and PERFORM are signature contexts
	var kind sigContextKind
	switch firstKeyword {
	case "CALLNAT":
		kind = sigCallnat
	case "PERFORM":
		kind = sigPerform
	default:
		return sigNone, 0
	}

	// If keyword-only (no space after keyword), it's not yet a signature context
	if len(tokens) == 1 && len(prefix) == len(tokens[0]) {
		return sigNone, 0
	}

	// Compute the argument index.
	// tokens[0] = keyword, tokens[1] = target, tokens[2+] = arguments.
	// argTokenCount = number of argument tokens (tokens after the target).
	argTokenCount := 0
	if len(tokens) > 2 {
		argTokenCount = len(tokens) - 2
	}

	// Check if the prefix ends with whitespace.
	endsWithWhitespace := len(prefix) > 0 && unicode.IsSpace(rune(prefix[len(prefix)-1]))

	// Compute argIndex based on whether there's trailing whitespace.
	var argIndex int
	if endsWithWhitespace {
		// Cursor has moved to the next argument slot.
		argIndex = argTokenCount
	} else {
		// Last argument token is under construction (or none if argTokenCount == 0).
		// argIndex = argTokenCount - 1, but never below 0.
		argIndex = argTokenCount - 1
		if argIndex < 0 {
			argIndex = 0
		}
	}

	return kind, argIndex
}
