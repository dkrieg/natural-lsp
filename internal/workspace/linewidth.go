package workspace

import (
	"unicode/utf8"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// This file implements the in-memory, encoding-agnostic per-file line-width
// table (feature 22, T8 / OQ-B B-i). It exists solely to let the LSP providers
// convert the analyzer's byte-offset columns into negotiated position-encoding
// code units (UTF-8 or UTF-16) WITHOUT re-reading the source file on every
// query.
//
// Why it is needed: `workspace/symbol` and `references` returned protocol
// ranges by re-reading every indexed file's bytes on every query, purely to
// feed the byte-offset→code-unit conversion (UTF-16 needs the source line's
// bytes to count surrogate pairs; UTF-8 does not). At tens of thousands of
// objects that is tens of thousands of disk reads per keystroke-driven query.
// The symbol data is already in memory (FileAnalysis.Structure); only the
// per-line width data was missing.
//
// Placement & contract:
//   - Lives on Index as an in-memory-only field, keyed by workspace-relative
//     path, guarded by the same Index mutex as entries. It is NEVER persisted:
//     no cache-format bump (still 0.6.0), no model.FileAnalysis change, no
//     Analyzer-seam change. On a warm cache load it is recomputed once from
//     disk (an amortized one-time cost, not per-query) — see ensureLineWidths.
//   - Natural source is overwhelmingly ASCII (identifiers are A-Z/0-9/#/&/-/@/+),
//     so the representation optimizes the ASCII-common case to near-zero memory:
//     an ASCII line stores only its byte length; only a non-ASCII line retains
//     its raw bytes so UTF-16 surrogate-pair counting stays exact.

// lineWidthTable holds the per-line width data for one file, enough to map a
// (0-based line, byte offset within the line) to a code-unit count for both
// UTF-8 and UTF-16. It is immutable once built.
type lineWidthTable struct {
	lines []lineWidth
}

// lineWidth is the width data for a single source line (terminator excluded).
//
// For a pure-ASCII line (the overwhelming common case) byte offset == UTF-8
// unit == UTF-16 unit, so only byteLen is needed and bytes is nil — near-zero
// memory. A non-ASCII line retains its raw bytes so UTF-16 code-unit counting
// (surrogate pairs for runes > 0xFFFF) is exact for any byte offset.
type lineWidth struct {
	byteLen uint32 // number of bytes in the line (terminator excluded)
	bytes   []byte // nil when the line is pure ASCII; the line's raw bytes otherwise
}

// buildLineWidthTable splits content into lines (handling \n and \r\n, matching
// lineAt's terminator handling) and records each line's width data. It is O(n)
// over content and reuses subslices of content for non-ASCII lines (no copy),
// so it holds a reference to content's backing array only when a non-ASCII line
// exists — for a fully-ASCII file it retains no bytes at all.
func buildLineWidthTable(content string) *lineWidthTable {
	// Pre-count lines to size the slice once. A file with no trailing newline
	// still has a final line; an empty file has a single empty line, matching
	// lineAt(content, 0) == "".
	lines := make([]lineWidth, 0, 1)
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			end := i
			if end > start && content[end-1] == '\r' {
				end--
			}
			lines = append(lines, makeLineWidth(content[start:end]))
			start = i + 1
		}
	}
	// The trailing segment after the last '\n' (or the whole content if there is
	// no '\n') is the final line.
	lines = append(lines, makeLineWidth(content[start:]))
	return &lineWidthTable{lines: lines}
}

// makeLineWidth records one line's width data, retaining its raw bytes only when
// the line contains a non-ASCII byte.
func makeLineWidth(line string) lineWidth {
	ascii := true
	for i := 0; i < len(line); i++ {
		if line[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	lw := lineWidth{byteLen: uint32(len(line))}
	if !ascii {
		lw.bytes = []byte(line)
	}
	return lw
}

// codeUnit converts a byte offset within the given 0-based line to a code-unit
// count in the requested encoding, clamped to [0, byteLen]. utf16 selects UTF-16
// (surrogate-pair aware) code units; false selects UTF-8 (byte) units. An
// out-of-range line index yields 0.
//
// This mirrors byteOffsetToCharacter in the server package exactly, so a range
// converted through the table is byte-identical to one converted from the live
// file content — the correctness guarantee the T8 regression tests assert.
func (t *lineWidthTable) codeUnit(line, byteOffset int, utf16 bool) uint32 {
	if t == nil || line < 0 || line >= len(t.lines) {
		return 0
	}
	lw := t.lines[line]
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > int(lw.byteLen) {
		byteOffset = int(lw.byteLen)
	}
	if lw.bytes == nil {
		// Pure ASCII: byte offset == code unit in both encodings.
		return uint32(byteOffset)
	}
	if !utf16 {
		return uint32(byteOffset)
	}
	// UTF-16: count surrogate pairs across the prefix [:byteOffset].
	var units int
	for _, r := range string(lw.bytes[:byteOffset]) {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return uint32(units)
}

// ProtocolRange converts a model.Range for the file at path into a protocol-space
// range in the requested encoding, using the in-memory line-width table and
// NEVER reading the file. utf16 selects UTF-16 code units; false selects UTF-8.
//
// It reproduces the server's toProtocolRange semantics exactly (0-based,
// end-exclusive, zero-width caret preserved) so provider output is byte-identical
// to the previous disk-reading implementation. When the file has no line-width
// table (e.g. it was added without content), it falls back to treating columns as
// code units directly — correct for ASCII, the only case that can reach here
// without a table, and never a panic (FR-43).
//
// The returned values are the raw protocol coordinates (line/character pairs);
// the server package assembles them into a protocol.Range to avoid a dependency
// on go.lsp.dev/protocol here.
func (t *lineWidthTable) protocolRange(r model.Range, utf16 bool) (startLine, startChar, endLine, endChar uint32) {
	sLine := r.Start.Line - 1
	if sLine < 0 {
		sLine = 0
	}
	sByte := r.Start.Column - 1
	if sByte < 0 {
		sByte = 0
	}
	startLine = uint32(sLine)
	startChar = t.codeUnitOrFallback(sLine, sByte, utf16)

	zeroWidth := r.Start.Line == r.End.Line && r.Start.Column == r.End.Column
	if zeroWidth {
		return startLine, startChar, startLine, startChar
	}

	eLine := r.End.Line - 1
	if eLine < 0 {
		eLine = 0
	}
	eByte := r.End.Column // inclusive last byte + 1 = exclusive end
	if eByte < 0 {
		eByte = 0
	}
	endLine = uint32(eLine)
	endChar = t.codeUnitOrFallback(eLine, eByte, utf16)
	return startLine, startChar, endLine, endChar
}

// codeUnitOrFallback is codeUnit with the nil-table fallback: with no table the
// byte offset is treated as the code unit directly (exact for ASCII).
func (t *lineWidthTable) codeUnitOrFallback(line, byteOffset int, utf16 bool) uint32 {
	if t == nil {
		if byteOffset < 0 {
			return 0
		}
		return uint32(byteOffset)
	}
	return t.codeUnit(line, byteOffset, utf16)
}
