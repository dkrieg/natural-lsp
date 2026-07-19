package workspace

import (
	"testing"
	"unicode/utf16"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// oracleCodeUnit is an independent reference implementation of byte-offset →
// code-unit conversion, computed directly from the line text. The line-width
// table's codeUnit must match it byte-for-byte for EVERY byte offset on EVERY
// line, including non-ASCII lines under UTF-16 (the case that would break if the
// per-line width data were dropped). utf16 selects UTF-16 code units.
func oracleCodeUnit(line string, byteOffset int, utf16Enc bool) uint32 {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(line) {
		byteOffset = len(line)
	}
	prefix := line[:byteOffset]
	if !utf16Enc {
		return uint32(len(prefix))
	}
	return uint32(len(utf16.Encode([]rune(prefix))))
}

// splitLines mirrors buildLineWidthTable's line splitting so the oracle sees the
// same per-line text.
func splitLines(content string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			end := i
			if end > start && content[end-1] == '\r' {
				end--
			}
			lines = append(lines, content[start:end])
			start = i + 1
		}
	}
	lines = append(lines, content[start:])
	return lines
}

// TestLineWidthTable_MatchesOracle asserts the table's codeUnit conversion equals
// the independent oracle for every byte offset on every line, across ASCII and
// non-ASCII (BMP + supplementary-plane) content, under both encodings. This is
// the correctness guard for OQ-B: a dropped/incorrect width would diverge here.
func TestLineWidthTable_MatchesOracle(t *testing.T) {
	contents := []string{
		// Pure ASCII.
		"DEFINE SUBROUTINE MYSUB\nEND-SUBROUTINE\n",
		// BMP non-ASCII (é = 2 UTF-8 bytes, 1 UTF-16 unit) before a token.
		"* café header\nREAD CUSTOMER\n",
		// Supplementary-plane (😀 = 4 UTF-8 bytes, 2 UTF-16 units) mid-line.
		"* smile 😀 tail\nFIND ORDERS\n",
		// Mixed multibyte, CRLF terminators, no trailing newline on last line.
		"* héllo 😀 wörld\r\nCALLNAT 'X'\r\nlast é line",
		// Empty file (single empty line).
		"",
	}

	for _, utf16Enc := range []bool{false, true} {
		for ci, content := range contents {
			table := buildLineWidthTable(content)
			lines := splitLines(content)
			if len(table.lines) != len(lines) {
				t.Fatalf("content[%d] utf16=%v: table has %d lines, oracle has %d",
					ci, utf16Enc, len(table.lines), len(lines))
			}
			for li, line := range lines {
				// Test every byte offset from 0 to len(line)+1 (over-range clamps).
				for bo := 0; bo <= len(line)+1; bo++ {
					got := table.codeUnit(li, bo, utf16Enc)
					want := oracleCodeUnit(line, bo, utf16Enc)
					if got != want {
						t.Errorf("content[%d] utf16=%v line=%d byteOffset=%d: codeUnit=%d, oracle=%d (line=%q)",
							ci, utf16Enc, li, bo, got, want, line)
					}
				}
			}
		}
	}
}

// TestLineWidthTable_ProtocolRange asserts protocolRange reproduces the
// server's toProtocolRange semantics: 0-based, end-exclusive, zero-width caret
// preserved, and correct code-unit columns on a non-ASCII line under UTF-16.
func TestLineWidthTable_ProtocolRange(t *testing.T) {
	// Line 0: "* café 😀"  (comment). Line 1: "READ CUSTOMER".
	// The word "CUSTOMER" starts at byte column 6 (1-based) on line 1 (ASCII).
	content := "* café 😀\nREAD CUSTOMER\n"
	table := buildLineWidthTable(content)

	// A range spanning "CUSTOMER" on line 1 (1-based model coords, inclusive end).
	// "READ " = 5 bytes, so CUSTOMER starts at column 6 and ends at column 13.
	r := model.Range{
		Start: model.Position{Line: 2, Column: 6},
		End:   model.Position{Line: 2, Column: 13},
	}
	// UTF-16 (line 1 is ASCII → byte==unit).
	sl, sc, el, ec := table.protocolRange(r, true)
	if sl != 1 || sc != 5 || el != 1 || ec != 13 {
		t.Errorf("protocolRange CUSTOMER = (%d,%d)-(%d,%d), want (1,5)-(1,13)", sl, sc, el, ec)
	}

	// A zero-width caret at line 0 col 1 stays zero-width.
	caret := model.Range{
		Start: model.Position{Line: 1, Column: 1},
		End:   model.Position{Line: 1, Column: 1},
	}
	sl, sc, el, ec = table.protocolRange(caret, true)
	if sl != 0 || sc != 0 || el != 0 || ec != 0 {
		t.Errorf("caret protocolRange = (%d,%d)-(%d,%d), want (0,0)-(0,0)", sl, sc, el, ec)
	}

	// A range on the non-ASCII line 0 must use UTF-16 code units. Byte layout of
	// "* café 😀" (12 bytes): '*'(1) ' '(1) 'c'(1) 'a'(1) 'f'(1) 'é'(2) ' '(1)
	// '😀'(4). Full line = 6 UTF-16 units for "* café" + 1 space + 2 for the
	// supplementary-plane 😀 = 9 UTF-16 units; = 12 UTF-8 units (bytes). A model
	// range Start col 1 .. End col 13 covers the whole line (End byte offset 13
	// clamps to the 12-byte line length).
	nonAscii := model.Range{
		Start: model.Position{Line: 1, Column: 1},
		End:   model.Position{Line: 1, Column: 13},
	}
	sl, sc, el, ec = table.protocolRange(nonAscii, true)
	if sc != 0 || ec != 9 {
		t.Errorf("non-ascii UTF-16 range end char = %d (start %d), want end 9 start 0", ec, sc)
	}
	// Same range in UTF-8: end byte offset clamps to the 12-byte line → 12 units.
	_, _, _, ec8 := table.protocolRange(nonAscii, false)
	if ec8 != 12 {
		t.Errorf("non-ascii UTF-8 range end char = %d, want 12", ec8)
	}
}
