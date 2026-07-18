package server

import (
	"go.lsp.dev/protocol"

	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// Position conversion between the analyzer's model coordinates and LSP protocol
// coordinates (ADR-008).
//
// model.Position is 1-based; model.Column is a 1-based BYTE offset within the
// line (the lexer advances the column counter per byte consumed). Model ranges
// are inclusive of the end position — tokenRange sets End.Column to the column
// of the token's last byte (`Column + len - 1`).
//
// protocol.Position is 0-based; Character counts code units in the negotiated
// PositionEncodingKind — bytes for UTF-8, UTF-16 code units for UTF-16 — and
// protocol ranges are end-exclusive. Converting a model range therefore maps the
// inclusive model End to the exclusive protocol End by advancing one byte past
// the last byte (endByteOffset = End.Column).
//
// Natural source is effectively ASCII (identifiers are A-Z/0-9/#/&/-/@/+), so
// byte offset, rune count, and both encodings coincide for every symbol name and
// reference target in practice; the encoding-aware path only matters for a range
// that spans a multi-byte character in a string literal or comment.

// lineAt returns the text of the given 0-based line index in content, without
// its terminator. An out-of-range index yields "".
func lineAt(content string, line int) string {
	if line < 0 {
		return ""
	}
	cur := 0
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			if cur == line {
				end := i
				if end > start && content[end-1] == '\r' {
					end--
				}
				return content[start:end]
			}
			cur++
			start = i + 1
		}
	}
	if cur == line {
		return content[start:]
	}
	return ""
}

// byteOffsetToCharacter counts code units in lineText[:byteOffset] in the given
// encoding. byteOffset is clamped to [0, len(lineText)].
func byteOffsetToCharacter(lineText string, byteOffset int, enc protocol.PositionEncodingKind) uint32 {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(lineText) {
		byteOffset = len(lineText)
	}
	prefix := lineText[:byteOffset]
	if enc == protocol.PositionEncodingKindUTF8 {
		// UTF-8 code units are bytes.
		return uint32(len(prefix))
	}
	// UTF-16 (the LSP default): a non-BMP rune is a surrogate pair (2 units).
	var units int
	for _, r := range prefix {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return uint32(units)
}

// characterToByteOffset is the inverse of byteOffsetToCharacter: it returns the
// byte offset within lineText at the given code-unit count in the encoding,
// clamped to [0, len(lineText)].
func characterToByteOffset(lineText string, character int, enc protocol.PositionEncodingKind) int {
	if character <= 0 {
		return 0
	}
	if enc == protocol.PositionEncodingKindUTF8 {
		if character > len(lineText) {
			return len(lineText)
		}
		return character
	}
	// UTF-16.
	units := 0
	for i, r := range lineText {
		if units >= character {
			return i
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len(lineText)
}

// modelPositionToProtocol converts a 1-based model.Position (byteOffset =
// Column-1) to a 0-based protocol.Position in the negotiated encoding.
func modelPositionToProtocol(pos model.Position, content string, enc protocol.PositionEncodingKind) protocol.Position {
	line := pos.Line - 1
	if line < 0 {
		line = 0
	}
	byteOffset := pos.Column - 1
	if byteOffset < 0 {
		byteOffset = 0
	}
	return protocol.Position{
		Line:      uint32(line),
		Character: byteOffsetToCharacter(lineAt(content, line), byteOffset, enc),
	}
}

// toProtocolRange converts a model.Range to an end-exclusive protocol.Range.
// The start maps at byteOffset = Start.Column-1. Model ranges are inclusive of
// the end byte, so a real span (End after Start) maps its end at byteOffset =
// End.Column (one byte past the inclusive last byte). A zero-width model range
// (Start == End, used as a caret — e.g. an object root's SelectionRange) maps to
// a zero-width protocol range at the same position, rather than being widened to
// a one-byte span.
func toProtocolRange(r model.Range, content string, enc protocol.PositionEncodingKind) protocol.Range {
	start := modelPositionToProtocol(r.Start, content, enc)

	zeroWidth := r.Start.Line == r.End.Line && r.Start.Column == r.End.Column
	if zeroWidth {
		return protocol.Range{Start: start, End: start}
	}

	endLine := r.End.Line - 1
	if endLine < 0 {
		endLine = 0
	}
	endByte := r.End.Column // inclusive last byte + 1 = exclusive end
	if endByte < 0 {
		endByte = 0
	}
	end := protocol.Position{
		Line:      uint32(endLine),
		Character: byteOffsetToCharacter(lineAt(content, endLine), endByte, enc),
	}
	return protocol.Range{Start: start, End: end}
}

// rangeConverter is the disk-free range converter handed to callbacks by
// Index.ForEachWithRange: it maps a model.Range to protocol-space coordinates in
// the given encoding using that file's in-memory line-width table (feature 22
// T8), never reading the file. It aliases workspace.RangeConverter so the
// callback literal's parameter type is identical to the method's signature.
type rangeConverter = workspace.RangeConverter

// protocolRangeVia builds an end-exclusive protocol.Range from a rangeConverter,
// reproducing toProtocolRange's semantics WITHOUT reading the file. Used by the
// full-workspace sweep providers (workspace/symbol, references), which would
// otherwise re-read every indexed file on every query purely for this conversion.
func protocolRangeVia(conv rangeConverter, r model.Range, enc protocol.PositionEncodingKind) protocol.Range {
	utf16 := enc != protocol.PositionEncodingKindUTF8
	sl, sc, el, ec := conv(r, utf16)
	return protocol.Range{
		Start: protocol.Position{Line: sl, Character: sc},
		End:   protocol.Position{Line: el, Character: ec},
	}
}

// indexProtocolRange converts a model.Range for the file at relPath into an
// end-exclusive protocol.Range using the index's in-memory line-width table
// (feature 22 T8), reproducing toProtocolRange's semantics WITHOUT reading the
// file. Used for bounded, single-file conversions (e.g. the references
// declaration site) outside a ForEachWithRange walk.
func indexProtocolRange(idx *workspace.Index, relPath string, r model.Range, enc protocol.PositionEncodingKind) protocol.Range {
	utf16 := enc != protocol.PositionEncodingKindUTF8
	sl, sc, el, ec := idx.ProtocolRange(relPath, r, utf16)
	return protocol.Range{
		Start: protocol.Position{Line: sl, Character: sc},
		End:   protocol.Position{Line: el, Character: ec},
	}
}

// fromProtocolPosition converts a 0-based protocol.Position (a cursor) to a
// 1-based model.Position, mapping the encoding-counted Character back to a
// 1-based byte column. Out-of-range input clamps to the end of the line; it
// never panics (FR-43).
func fromProtocolPosition(p protocol.Position, content string, enc protocol.PositionEncodingKind) model.Position {
	line := int(p.Line)
	byteOffset := characterToByteOffset(lineAt(content, line), int(p.Character), enc)
	return model.Position{
		Line:   line + 1,
		Column: byteOffset + 1,
	}
}
