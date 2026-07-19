package server

import (
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/protocol"

	"github.com/dkrieg/natural-lsp/internal/model"
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

// provideSignatureHelp is the LSP signature help provider (feature 17, T4 GREEN phase).
// It is called when the client requests textDocument/signatureHelp.
//
// Implements signature help for CALLNAT and (future) PERFORM contexts.
// For CALLNAT: resolves the callee subprogram and returns a SignatureInformation
// with its PARAMETER section rendered as parameter labels.
// For other contexts: returns nil, nil (no signature).
//
// Concurrency (F7): snapshots idx/res/posEncoding/root/cfg under RLock, releases
// before any file I/O (index lookup, disk read).
//
// CRITICAL: When wiring the dispatch in server.go, the result MUST be marshaled via
// (*protocol.SignatureHelp).MarshalJSONTo(jsontext.NewEncoder(&buf)) — NOT json.Marshal —
// because SignatureHelp contains union/Nullable fields that require the protocol encoder.
// See the divergence note in tasks.md and server.go handleInitialize for the pattern.
func provideSignatureHelp(hctx *handlerContext, params protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Snapshot idx/res/posEncoding/root under the read lock; release before any file I/O (F7).
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
		// URI outside workspace root — no signature
		return nil, nil
	}

	// Get the source file's analysis from the index/store (store-first pattern)
	var sourceFA *model.FileAnalysis
	var sourceContent string

	// Try store first (for live edits while typing)
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil {
			sourceFA = &doc.Analysis
			sourceContent = string(doc.Content)
		}
	}

	// Fall back to index if not in store
	if sourceFA == nil {
		fa, ok := idx.Get(relPath)
		if !ok {
			// Source file not in index — no signature
			return nil, nil
		}
		sourceFA = &fa

		// Read content from disk for position conversion
		content, err := os.ReadFile(absPath)
		if err != nil {
			// Can't read source; no signature
			return nil, nil
		}
		sourceContent = string(content)
	}

	// Convert protocol position to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, sourceContent, posEncoding)

	// Derive the source line text for detectSignatureContext
	modelLine0Based := cursorPos.Line - 1 // model is 1-based, lineAt expects 0-based
	line := lineAt(sourceContent, modelLine0Based)
	cursorByteCol := cursorPos.Column - 1 // model is 1-based column, detectSignatureContext expects 0-based

	// Clamp cursor to line bounds
	if cursorByteCol < 0 {
		cursorByteCol = 0
	}
	if cursorByteCol > len(line) {
		cursorByteCol = len(line)
	}

	// Detect signature context (pure function, never panics)
	sigKind, argIndex := detectSignatureContext(line, cursorByteCol)

	// Handle CALLNAT context
	if sigKind == sigCallnat {
		// Find the enclosing CALLNAT or CALLNAT-dynamic edge on this line
		edge := enclosingCallEdge(sourceFA, modelLine0Based, model.EdgeCalls, model.EdgeCallsDynamic)
		if edge == nil {
			// No CALLNAT edge on this line — no signature
			return nil, nil
		}

		// Resolve the edge
		resolution, ok := res.Get(relPath, edge.Source)
		if !ok {
			// Edge not found in resolution set — no signature
			return nil, nil
		}

		// Handle resolved case
		if !resolution.IsResolved() {
			// Dynamic/unresolved/ambiguous — no signature (T6 handles these)
			return nil, nil
		}

		// Read the target file's analysis
		targetFA, ok := idx.Get(resolution.Path)
		if !ok {
			// Target file not in index (shouldn't happen after successful resolution)
			return nil, nil
		}

		// Build signature information from the target's PARAMETER definitions
		sigInfo := buildSignatureInformation(edge.TargetName, targetFA.Definitions)

		// Set the active parameter with clamping
		setActiveParameter(sigInfo, argIndex)

		// Wrap in protocol.SignatureHelp with ActiveSignature = 0
		return wrapSignatureHelp(sigInfo), nil
	}

	// Handle PERFORM context (FR-12: inline-before-external)
	if sigKind == sigPerform {
		// Find the enclosing PERFORM edge on this line
		edge := enclosingCallEdge(sourceFA, modelLine0Based, model.EdgePerforms)
		if edge == nil {
			// No PERFORM edge on this line — no signature
			return nil, nil
		}

		// Inline-before-external (FR-12, mirroring hover.go provideHover):
		// Check for an inline DEFINE SUBROUTINE in the caller's own Structure.Children
		targetName := strings.ToUpper(edge.TargetName)
		if sourceFA.Structure != nil && sourceFA.Structure.Children != nil {
			for _, child := range sourceFA.Structure.Children {
				if child.Kind == model.SymbolSubroutine && strings.EqualFold(child.Name, targetName) {
					// Found inline subroutine; build signature from caller's PARAMETER definitions
					sigInfo := buildSignatureInformation(edge.TargetName, sourceFA.Definitions)

					// Set the active parameter with clamping
					setActiveParameter(sigInfo, argIndex)

					// Wrap in protocol.SignatureHelp with ActiveSignature = 0
					return wrapSignatureHelp(sigInfo), nil
				}
			}
		}

		// No inline match; resolve via the resolution set
		resolution, ok := res.Get(relPath, edge.Source)
		if !ok {
			// Edge not found in resolution set — no signature
			return nil, nil
		}

		// Handle resolved case
		if !resolution.IsResolved() {
			// Dynamic/unresolved/ambiguous — no signature (T6 handles these)
			return nil, nil
		}

		// Read the target file's (external .NSS) analysis
		targetFA, ok := idx.Get(resolution.Path)
		if !ok {
			// Target file not in index (shouldn't happen after successful resolution)
			return nil, nil
		}

		// Build signature information from the target's PARAMETER definitions
		sigInfo := buildSignatureInformation(edge.TargetName, targetFA.Definitions)

		// Set the active parameter with clamping
		setActiveParameter(sigInfo, argIndex)

		// Wrap in protocol.SignatureHelp with ActiveSignature = 0
		return wrapSignatureHelp(sigInfo), nil
	}

	// For DDM and sigNone: return nil, nil (T6 handles these)
	return nil, nil
}

// enclosingCallEdge returns the edge on a given source line whose kind matches one of
// the specified kinds (for signature help: EdgeCalls or EdgeCallsDynamic for CALLNAT).
//
// This is a helper for finding the enclosing CALLNAT when the cursor may be positioned
// after the target (in the argument region), where findCursorTarget may miss because it
// matches EdgeEntry.Source containment on the whole statement, but we want to find the
// edge on the current line.
//
// Returns nil if no matching edge is found on that line.
func enclosingCallEdge(fa *model.FileAnalysis, line0Based int, kinds ...model.EdgeKind) *model.EdgeEntry {
	line1Based := line0Based + 1 // model uses 1-based line numbers

	// Check if any of the specified kinds match
	matchesKind := func(ek model.EdgeKind) bool {
		for _, k := range kinds {
			if ek == k {
				return true
			}
		}
		return false
	}

	// Scan edges to find one on this line with a matching kind
	for i := range fa.Edges {
		edge := &fa.Edges[i]
		if edge.Source.Start.Line == line1Based && matchesKind(edge.Kind) {
			return edge
		}
	}

	return nil
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

// buildSignatureInformation builds a protocol.SignatureInformation from a subroutine
// name and a list of data definitions (feature 17, T3 GREEN phase).
//
// The function filters defs to SectionKind=="parameter" and renders each parameter
// as a protocol.ParameterInformation with Label = protocol.String("<name> <type-with-dims>").
// Array dimensions are rendered as (lower:upper) or (lower:*) for unbounded dimensions,
// matching the rendering style used by hover (so both agree on the parameter interface).
// Group nesting is honored: a group header (Type=="") is rendered with no type,
// and its children are enumerated as separate ParameterInformation entries.
//
// SignatureInformation.Label is a readable header, e.g., "<name> (<p1>, <p2>, ...)"
// or just "<name>" if no parameters.
//
// When there are no parameters, the returned SignatureInformation has an empty
// (non-nil) Parameters slice per Story 2 AC4 — no parameters is a valid signature.
//
// No I/O, no locks — a pure function.
func buildSignatureInformation(name string, defs []model.DataDefinition) *protocol.SignatureInformation {
	// Use the shared parameter-interface helper (from hover.go)
	params := parameterInterface(defs)

	// Build the ParameterInformation slice (always non-nil, even if empty per AC4)
	paramInfos := make([]protocol.ParameterInformation, 0, len(params))
	for _, p := range params {
		// Render the label: name alone for group headers, name + type for others
		var label string
		if p.typeStr == "" {
			label = p.name
		} else {
			label = p.name + " " + p.typeStr
		}

		// Create a protocol.ParameterInformation with Label as protocol.String (union arm)
		paramInfos = append(paramInfos, protocol.ParameterInformation{
			Label: protocol.String(label),
		})
	}

	// Build the header label: "name (p1, p2, ...)" or just "name" if no params
	var headerLabel string
	if len(paramInfos) > 0 {
		// Collect parameter names (not types) for the header
		var paramNames []string
		for _, p := range params {
			paramNames = append(paramNames, p.name)
		}
		headerLabel = name + " (" + strings.Join(paramNames, ", ") + ")"
	} else {
		headerLabel = name
	}

	return &protocol.SignatureInformation{
		Label:      headerLabel,
		Parameters: paramInfos, // Always non-nil, even if empty
	}
}

// newNullableUint32 builds a protocol.Nullable[uint32] carrying v using only the
// type's public API (it has unexported fields and no exported constructor).
// Decoding a bare JSON number into the Nullable never fails; the error is ignored.
func newNullableUint32(v uint32) protocol.Nullable[uint32] {
	var n protocol.Nullable[uint32]
	_ = n.UnmarshalJSONFrom(jsontext.NewDecoder(strings.NewReader(strconv.FormatUint(uint64(v), 10))))
	return n
}

// setActiveParameter sets the ActiveParameter field on a SignatureInformation,
// clamping argIndex to the valid range [0, len(parameters)-1].
// If the signature has zero parameters, ActiveParameter is left unset (zero Nullable[uint32]{}).
// This is a helper for signature help providers to avoid duplication.
func setActiveParameter(sig *protocol.SignatureInformation, argIndex int) {
	if sig == nil || len(sig.Parameters) == 0 {
		// For param-less signatures, leave activeParameter unset (already zero in the struct)
		return
	}

	// Clamp argIndex to the valid range [0, len(sig.Parameters)-1]
	clamped := argIndex
	if clamped < 0 {
		clamped = 0
	}
	if clamped >= len(sig.Parameters) {
		clamped = len(sig.Parameters) - 1
	}

	sig.ActiveParameter = newNullableUint32(uint32(clamped))
}

// wrapSignatureHelp wraps a SignatureInformation in a protocol.SignatureHelp
// with ActiveSignature = 0 (only one signature per call context).
// This is a helper to eliminate duplication when returning signature help.
func wrapSignatureHelp(sig *protocol.SignatureInformation) *protocol.SignatureHelp {
	activeSignature := uint32(0)
	return &protocol.SignatureHelp{
		Signatures:      []protocol.SignatureInformation{*sig},
		ActiveSignature: &activeSignature,
	}
}
