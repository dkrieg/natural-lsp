package server

import (
	"os"

	"github.com/dkrieg/natural-lsp/internal/model"
	"go.lsp.dev/protocol"
)

// semanticTokenTypesLegend is the fixed-order list of token types for the semantic tokens legend.
// The index of each type is its wire identifier; this ordering is a contract and must not change
// without a major version bump (clients refresh on legend change).
// Order matches model.SemanticTokenType* constants 1:1 (feature 29, OQ-1).
var semanticTokenTypesLegend = []string{
	"keyword",   // SemanticTokenTypeKeyword (index 0)
	"comment",   // SemanticTokenTypeComment (index 1)
	"string",    // SemanticTokenTypeString (index 2)
	"number",    // SemanticTokenTypeNumber (index 3)
	"operator",  // SemanticTokenTypeOperator (index 4)
	"variable",  // SemanticTokenTypeVariable (index 5)
	"parameter", // SemanticTokenTypeParameter (index 6)
	"function",  // SemanticTokenTypeFunction (index 7)
	"type",      // SemanticTokenTypeType (index 8)
	"property",  // SemanticTokenTypeProperty (index 9)
}

// semanticTokenModifiersLegend is the fixed-order list of token modifiers for the semantic tokens legend.
// Each modifier's position is its bit index; this ordering is a contract and must not change
// without a major version bump (clients refresh on legend change).
// Bit positions match model.SemanticTokenModifier* constants 1:1 (feature 29, OQ-1).
var semanticTokenModifiersLegend = []string{
	"declaration",    // SemanticTokenModifierDeclaration (bit 0)
	"definition",     // SemanticTokenModifierDefinition (bit 1)
	"readonly",       // SemanticTokenModifierReadonly (bit 2)
	"modification",   // SemanticTokenModifierModification (bit 3)
	"defaultLibrary", // SemanticTokenModifierDefaultLibrary (bit 4)
}

// encodeSemanticTokens converts []model.SemanticToken to the LSP relative 5-int stream format.
//
// Multi-line tokens are split into per-line entries before encoding. Each token in the output
// stream is represented by [deltaLine, deltaStartChar, length, tokenTypeIndex, tokenModifiersBitset].
// deltaLine is relative to the previous token's line (absolute for the first token).
// deltaStartChar is relative to the previous token's start char within the same line, absolute
// on a new line. length is measured in code units on the token's line. tokenTypeIndex is the
// index of Type in semanticTokenTypesLegend. tokenModifiersBitset is the Modifiers value.
//
// Empty/nil input returns a non-nil SemanticTokens with empty Data.
// Never panics (FR-43).
func encodeSemanticTokens(tokens []model.SemanticToken, content string, encoding protocol.PositionEncodingKind) *protocol.SemanticTokens {
	if len(tokens) == 0 {
		return &protocol.SemanticTokens{Data: []uint32{}}
	}

	// Build type index map for fast lookup.
	typeIndex := make(map[model.SemanticTokenType]uint32)
	for i, t := range semanticTokenTypesLegend {
		typeIndex[model.SemanticTokenType(t)] = uint32(i)
	}

	// Collect all per-line token entries (split multi-line tokens).
	type tokenEntry struct {
		line0     int    // 0-based line
		startChar uint32 // code units in the encoding
		length    uint32 // code units in the encoding
		typeIndex uint32
		modifiers uint32
	}
	var entries []tokenEntry

	for _, tok := range tokens {
		startLine := tok.Range.Start.Line - 1
		endLine := tok.Range.End.Line - 1

		if startLine == endLine {
			// Single-line token: convert directly.
			lineText := lineAt(content, startLine)
			startByteOffset := tok.Range.Start.Column - 1
			endByteOffset := tok.Range.End.Column // inclusive end → exclusive

			// Clamp startByteOffset to valid range
			if startByteOffset < 0 {
				startByteOffset = 0
			}
			if startByteOffset > len(lineText) {
				startByteOffset = len(lineText)
			}

			startChar := byteOffsetToCharacter(lineText, startByteOffset, encoding)

			// For length, measure the raw span from start to end (before clamping end)
			// This handles cases where the token claims to extend beyond the line.
			var length uint32
			if encoding == protocol.PositionEncodingKindUTF8 {
				// UTF-8: byte length = code unit length
				if endByteOffset < startByteOffset {
					length = 0
				} else {
					length = uint32(endByteOffset - startByteOffset)
				}
			} else {
				// UTF-16: need to count code units in the span
				spanEnd := endByteOffset
				if spanEnd > len(lineText) {
					spanEnd = len(lineText)
				}
				if spanEnd < startByteOffset {
					spanEnd = startByteOffset
				}
				span := lineText[startByteOffset:spanEnd]
				units := 0
				for _, r := range span {
					if r > 0xFFFF {
						units += 2
					} else {
						units++
					}
				}
				// Add units for the out-of-bounds part (measure byte distance, assume ASCII)
				outOfBounds := endByteOffset - spanEnd
				if outOfBounds > 0 {
					// Out-of-bounds bytes (assuming ASCII for simplicity)
					units += outOfBounds
				}
				length = uint32(units)
			}

			// Look up token type index.
			idx, ok := typeIndex[tok.Type]
			if !ok {
				// Unknown type: skip defensively.
				continue
			}

			entries = append(entries, tokenEntry{
				line0:     startLine,
				startChar: startChar,
				length:    length,
				typeIndex: idx,
				modifiers: uint32(tok.Modifiers),
			})
		} else {
			// Multi-line token: split into per-line entries.
			// First line: from Start.Column to end of line.
			firstLineText := lineAt(content, startLine)
			firstStartByteOffset := tok.Range.Start.Column - 1
			firstStartChar := byteOffsetToCharacter(firstLineText, firstStartByteOffset, encoding)
			firstEndChar := byteOffsetToCharacter(firstLineText, len(firstLineText), encoding)
			firstLength := firstEndChar - firstStartChar

			idx, ok := typeIndex[tok.Type]
			if !ok {
				continue // Skip unknown type.
			}

			entries = append(entries, tokenEntry{
				line0:     startLine,
				startChar: firstStartChar,
				length:    firstLength,
				typeIndex: idx,
				modifiers: uint32(tok.Modifiers),
			})

			// Middle lines: entire line (if any).
			for midLine := startLine + 1; midLine < endLine; midLine++ {
				midLineText := lineAt(content, midLine)
				midStartChar := uint32(0)
				midEndChar := byteOffsetToCharacter(midLineText, len(midLineText), encoding)
				midLength := midEndChar - midStartChar

				entries = append(entries, tokenEntry{
					line0:     midLine,
					startChar: midStartChar,
					length:    midLength,
					typeIndex: idx,
					modifiers: uint32(tok.Modifiers),
				})
			}

			// Last line: from column 1 to End.Column.
			lastLineText := lineAt(content, endLine)
			lastEndByteOffset := tok.Range.End.Column // inclusive end → exclusive
			lastStartByteOffset := 0                  // column 1 = byte offset 0
			lastStartChar := byteOffsetToCharacter(lastLineText, lastStartByteOffset, encoding)
			lastEndChar := byteOffsetToCharacter(lastLineText, lastEndByteOffset, encoding)
			lastLength := lastEndChar - lastStartChar

			entries = append(entries, tokenEntry{
				line0:     endLine,
				startChar: lastStartChar,
				length:    lastLength,
				typeIndex: idx,
				modifiers: uint32(tok.Modifiers),
			})
		}
	}

	// Sort entries by (line, startChar) defensively (input order not guaranteed).
	// Stable sort preserves order within the same (line, startChar).
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].line0 < entries[i].line0 ||
				(entries[j].line0 == entries[i].line0 && entries[j].startChar < entries[i].startChar) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Encode as relative 5-int stream.
	var data []uint32
	prevLine := 0
	prevStartChar := uint32(0)

	for _, e := range entries {
		deltaLine := uint32(e.line0 - prevLine)
		var deltaStartChar uint32
		if e.line0 == prevLine {
			deltaStartChar = e.startChar - prevStartChar
		} else {
			deltaStartChar = e.startChar
		}

		data = append(data, deltaLine, deltaStartChar, e.length, e.typeIndex, e.modifiers)

		prevLine = e.line0
		prevStartChar = e.startChar
	}

	return &protocol.SemanticTokens{Data: data}
}

// provideSemanticTokensFull handles the textDocument/semanticTokens/full request (feature 29, T6).
//
// Behavior: Snapshots posEncoding under the idxResMu RLock and releases before I/O (F7).
// Reads the document store-first (open buffer takes precedence), falling back to disk if not open.
// Out-of-root/missing/unreadable → return an empty tokens result (FR-43).
// Calls the analyzer seam SemanticTokens(absPath, content), encodes via encodeSemanticTokens,
// and marshals via marshalResult (json/v2 path).
func provideSemanticTokensFull(hctx *handlerContext, params protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	// F7: snapshot posEncoding under RLock and release before I/O.
	hctx.idxResMu.RLock()
	posEncoding := hctx.posEncoding
	hctx.idxResMu.RUnlock()

	// Store-first: try the open-document store first.
	var content []byte
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil {
			content = doc.Content
		}
	}

	// Resolve the URI to an absolute path.
	absPath, _, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// Out-of-root URI → return empty result (FR-43).
		return &protocol.SemanticTokens{Data: []uint32{}}, nil
	}

	// Fallback to disk if not in the store.
	if len(content) == 0 {
		b, err := os.ReadFile(absPath)
		if err != nil {
			// Missing/unreadable → return empty result (FR-43).
			return &protocol.SemanticTokens{Data: []uint32{}}, nil
		}
		content = b
	}

	// Call the analyzer seam to classify tokens.
	tokens := hctx.az.SemanticTokens(absPath, content)

	// Encode the tokens to the LSP 5-int stream format.
	result := encodeSemanticTokens(tokens, string(content), posEncoding)

	// Return the encoded result (never nil, but may have empty Data).
	return result, nil
}

// provideSemanticTokensRange handles the textDocument/semanticTokens/range request (feature 29, T11).
//
// Behavior: Snapshots posEncoding under the idxResMu RLock and releases before I/O (F7).
// Reads the document store-first (open buffer takes precedence), falling back to disk if not open.
// Out-of-root/missing/unreadable → return an empty tokens result (FR-43).
// Gets the full token list from the analyzer seam, filters to tokens intersecting the
// requested range (whole-token rule: include any token that partially overlaps),
// encodes the filtered list, and marshals via marshalResult (json/v2 path).
func provideSemanticTokensRange(hctx *handlerContext, params protocol.SemanticTokensRangeParams) (*protocol.SemanticTokens, error) {
	// F7: snapshot posEncoding under RLock and release before I/O.
	hctx.idxResMu.RLock()
	posEncoding := hctx.posEncoding
	hctx.idxResMu.RUnlock()

	// Store-first: try the open-document store first.
	var content []byte
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(params.TextDocument.URI); ok && doc != nil {
			content = doc.Content
		}
	}

	// Resolve the URI to an absolute path.
	absPath, _, err := uriToRelPath(hctx.root, params.TextDocument.URI)
	if err != nil {
		// Out-of-root URI → return empty result (FR-43).
		return &protocol.SemanticTokens{Data: []uint32{}}, nil
	}

	// Fallback to disk if not in the store.
	if len(content) == 0 {
		b, err := os.ReadFile(absPath)
		if err != nil {
			// Missing/unreadable → return empty result (FR-43).
			return &protocol.SemanticTokens{Data: []uint32{}}, nil
		}
		content = b
	}

	// Call the analyzer seam to classify tokens.
	allTokens := hctx.az.SemanticTokens(absPath, content)

	// Convert the requested protocol range to model coordinates.
	// Convert start and end positions separately.
	startPos := fromProtocolPosition(params.Range.Start, string(content), posEncoding)
	endPos := fromProtocolPosition(params.Range.End, string(content), posEncoding)
	requestedRange := model.Range{Start: startPos, End: endPos}

	// Filter tokens: include any token whose span intersects the requested range.
	// Whole-token rule: a token intersects if it's not entirely before the range start
	// and not entirely after the range end.
	var filtered []model.SemanticToken
	for _, tok := range allTokens {
		// Token is entirely before the range if its end is before the range start.
		if tok.Range.End.Line < requestedRange.Start.Line ||
			(tok.Range.End.Line == requestedRange.Start.Line &&
				tok.Range.End.Column < requestedRange.Start.Column) {
			continue
		}
		// Token is entirely after the range if its start is after the range end.
		if tok.Range.Start.Line > requestedRange.End.Line ||
			(tok.Range.Start.Line == requestedRange.End.Line &&
				tok.Range.Start.Column > requestedRange.End.Column) {
			continue
		}
		// Token intersects the range.
		filtered = append(filtered, tok)
	}

	// Encode the filtered tokens to the LSP 5-int stream format.
	result := encodeSemanticTokens(filtered, string(content), posEncoding)

	// Return the encoded result (never nil, but may have empty Data).
	return result, nil
}
