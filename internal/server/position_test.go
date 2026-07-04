package server

import (
	"testing"

	"go.lsp.dev/protocol"
	"natural-lsp/internal/model"
)

func TestToProtocolRange(t *testing.T) {
	tests := []struct {
		name     string
		modelRng model.Range
		content  string
		encoding protocol.PositionEncodingKind
		want     protocol.Range
	}{
		{
			name: "basic ASCII single-line range UTF-8",
			modelRng: model.Range{
				Start: model.Position{Line: 1, Column: 1},
				End:   model.Position{Line: 1, Column: 5},
			},
			content:  "HELLO world",
			encoding: protocol.PositionEncodingKindUTF8,
			want: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 5},
			},
		},
		{
			name: "basic ASCII single-line range UTF-16",
			modelRng: model.Range{
				Start: model.Position{Line: 1, Column: 1},
				End:   model.Position{Line: 1, Column: 5},
			},
			content:  "HELLO world",
			encoding: protocol.PositionEncodingKindUTF16,
			want: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 5},
			},
		},
		{
			// model.Column is a 1-based BYTE offset. In "hello🎉world" the emoji
			// occupies bytes 6-9 (1-based); a range spanning it is Start.Column 6
			// (byte offset 5) .. End.Column 9 (inclusive last byte). Model end is
			// inclusive, so the exclusive protocol end maps at byte offset 9.
			// UTF-8 code units == bytes: start "hello"=5, end "hello🎉"=9.
			name: "range spanning a multi-byte emoji, UTF-8",
			modelRng: model.Range{
				Start: model.Position{Line: 1, Column: 6},
				End:   model.Position{Line: 1, Column: 9},
			},
			content:  "hello🎉world",
			encoding: protocol.PositionEncodingKindUTF8,
			want: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 5},
				End:   protocol.Position{Line: 0, Character: 9},
			},
		},
		{
			// Same range, UTF-16: "hello" = 5 code units; "hello🎉" = 5 + 2
			// (surrogate pair) = 7. The divergence from UTF-8 (end 9 vs 7) is the
			// load-bearing ADR-008 case.
			name: "range spanning a multi-byte emoji, UTF-16",
			modelRng: model.Range{
				Start: model.Position{Line: 1, Column: 6},
				End:   model.Position{Line: 1, Column: 9},
			},
			content:  "hello🎉world",
			encoding: protocol.PositionEncodingKindUTF16,
			want: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 5},
				End:   protocol.Position{Line: 0, Character: 7},
			},
		},
		{
			name: "position at start of line",
			modelRng: model.Range{
				Start: model.Position{Line: 2, Column: 1},
				End:   model.Position{Line: 2, Column: 1},
			},
			content:  "line1\nline2\nline3",
			encoding: protocol.PositionEncodingKindUTF8,
			want: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 0},
				End:   protocol.Position{Line: 1, Character: 0},
			},
		},
		{
			name: "position at end of empty line",
			modelRng: model.Range{
				Start: model.Position{Line: 2, Column: 1},
				End:   model.Position{Line: 2, Column: 1},
			},
			content:  "line1\n\nline3",
			encoding: protocol.PositionEncodingKindUTF16,
			want: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 0},
				End:   protocol.Position{Line: 1, Character: 0},
			},
		},
		{
			name: "position at end of line",
			modelRng: model.Range{
				Start: model.Position{Line: 1, Column: 5},
				End:   model.Position{Line: 1, Column: 5},
			},
			content:  "hello",
			encoding: protocol.PositionEncodingKindUTF8,
			want: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 4},
				End:   protocol.Position{Line: 0, Character: 4},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toProtocolRange(tt.modelRng, tt.content, tt.encoding)
			if got.Start.Line != tt.want.Start.Line {
				t.Errorf("start line mismatch: got %d, want %d", got.Start.Line, tt.want.Start.Line)
			}
			if got.Start.Character != tt.want.Start.Character {
				t.Errorf("start character mismatch: got %d, want %d", got.Start.Character, tt.want.Start.Character)
			}
			if got.End.Line != tt.want.End.Line {
				t.Errorf("end line mismatch: got %d, want %d", got.End.Line, tt.want.End.Line)
			}
			if got.End.Character != tt.want.End.Character {
				t.Errorf("end character mismatch: got %d, want %d", got.End.Character, tt.want.End.Character)
			}
		})
	}
}

func TestFromProtocolPosition(t *testing.T) {
	tests := []struct {
		name     string
		protPos  protocol.Position
		content  string
		encoding protocol.PositionEncodingKind
		want     model.Position
	}{
		{
			name: "basic ASCII position start of line UTF-8",
			protPos: protocol.Position{
				Line:      0,
				Character: 0,
			},
			content:  "HELLO world",
			encoding: protocol.PositionEncodingKindUTF8,
			want: model.Position{
				Line:   1,
				Column: 1,
			},
		},
		{
			name: "basic ASCII position start of line UTF-16",
			protPos: protocol.Position{
				Line:      0,
				Character: 0,
			},
			content:  "HELLO world",
			encoding: protocol.PositionEncodingKindUTF16,
			want: model.Position{
				Line:   1,
				Column: 1,
			},
		},
		{
			name: "position after multi-byte character (emoji), UTF-8",
			protPos: protocol.Position{
				Line:      0,
				Character: 9,
			},
			content:  "hello🎉world",
			encoding: protocol.PositionEncodingKindUTF8,
			// UTF-8: "hello" = 5 bytes, "🎉" = 4 bytes, so byte offset 9 (Character 9)
			// is right after the emoji → 1-based byte column 10.
			want: model.Position{
				Line:   1,
				Column: 10,
			},
		},
		{
			name: "position after multi-byte character (emoji), UTF-16",
			protPos: protocol.Position{
				Line:      0,
				Character: 7,
			},
			content:  "hello🎉world",
			encoding: protocol.PositionEncodingKindUTF16,
			// In UTF-16: "hello" = 5 code units, "🎉" = 2 code units (surrogate pair)
			// Protocol Character 7 = 5 + 2 = after emoji, at byte position 10 (1-based) → model.Column 10
			want: model.Position{
				Line:   1,
				Column: 10,
			},
		},
		{
			name: "multi-line, position second line",
			protPos: protocol.Position{
				Line:      1,
				Character: 0,
			},
			content:  "line1\nline2\nline3",
			encoding: protocol.PositionEncodingKindUTF8,
			want: model.Position{
				Line:   2,
				Column: 1,
			},
		},
		{
			name: "empty line",
			protPos: protocol.Position{
				Line:      1,
				Character: 0,
			},
			content:  "line1\n\nline3",
			encoding: protocol.PositionEncodingKindUTF16,
			want: model.Position{
				Line:   2,
				Column: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromProtocolPosition(tt.protPos, tt.content, tt.encoding)
			if got.Line != tt.want.Line {
				t.Errorf("line mismatch: got %d, want %d", got.Line, tt.want.Line)
			}
			if got.Column != tt.want.Column {
				t.Errorf("column mismatch: got %d, want %d", got.Column, tt.want.Column)
			}
		})
	}
}
