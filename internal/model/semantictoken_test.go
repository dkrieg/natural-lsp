package model

import (
	"testing"
)

// TestSemanticTokenType asserts that each SemanticTokenType constant
// has the correct string value matching the legend (OQ-1).
func TestSemanticTokenType(t *testing.T) {
	cases := []struct {
		name     string
		typ      SemanticTokenType
		expected string
	}{
		{"keyword", SemanticTokenTypeKeyword, "keyword"},
		{"comment", SemanticTokenTypeComment, "comment"},
		{"string", SemanticTokenTypeString, "string"},
		{"number", SemanticTokenTypeNumber, "number"},
		{"operator", SemanticTokenTypeOperator, "operator"},
		{"variable", SemanticTokenTypeVariable, "variable"},
		{"parameter", SemanticTokenTypeParameter, "parameter"},
		{"function", SemanticTokenTypeFunction, "function"},
		{"type", SemanticTokenTypeType, "type"},
		{"property", SemanticTokenTypeProperty, "property"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.typ) != tc.expected {
				t.Errorf("got %q, want %q", string(tc.typ), tc.expected)
			}
		})
	}
}

// TestSemanticTokenModifier asserts that each SemanticTokenModifier constant
// has the correct bit position matching the legend (OQ-1).
func TestSemanticTokenModifier(t *testing.T) {
	cases := []struct {
		name     string
		modifier SemanticTokenModifier
		expected uint32
	}{
		{"declaration", SemanticTokenModifierDeclaration, 1},        // bit 0
		{"definition", SemanticTokenModifierDefinition, 2},          // bit 1
		{"readonly", SemanticTokenModifierReadonly, 4},              // bit 2
		{"modification", SemanticTokenModifierModification, 8},      // bit 3
		{"defaultLibrary", SemanticTokenModifierDefaultLibrary, 16}, // bit 4
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if uint32(tc.modifier) != tc.expected {
				t.Errorf("got %d (bit position %d), want %d", uint32(tc.modifier), bitPosition(uint32(tc.modifier)), tc.expected)
			}
		})
	}
}

// TestSemanticTokenConstruction asserts that a SemanticToken struct
// can be constructed with Range, Type, and Modifiers fields.
func TestSemanticTokenConstruction(t *testing.T) {
	// Construct a token with literal values.
	token := SemanticToken{
		Range: Range{
			Start: Position{Line: 1, Column: 1},
			End:   Position{Line: 1, Column: 5},
		},
		Type:      SemanticTokenTypeKeyword,
		Modifiers: SemanticTokenModifierDeclaration,
	}

	// Assert the fields are present and set correctly.
	if token.Range.Start.Line != 1 {
		t.Errorf("Range.Start.Line: got %d, want 1", token.Range.Start.Line)
	}
	if token.Range.Start.Column != 1 {
		t.Errorf("Range.Start.Column: got %d, want 1", token.Range.Start.Column)
	}
	if token.Range.End.Line != 1 {
		t.Errorf("Range.End.Line: got %d, want 1", token.Range.End.Line)
	}
	if token.Range.End.Column != 5 {
		t.Errorf("Range.End.Column: got %d, want 5", token.Range.End.Column)
	}
	if token.Type != SemanticTokenTypeKeyword {
		t.Errorf("Type: got %q, want %q", token.Type, SemanticTokenTypeKeyword)
	}
	if token.Modifiers != SemanticTokenModifierDeclaration {
		t.Errorf("Modifiers: got %d, want %d", token.Modifiers, SemanticTokenModifierDeclaration)
	}
}

// TestSemanticTokenModifierBitset asserts that modifiers can be combined as bit flags.
func TestSemanticTokenModifierBitset(t *testing.T) {
	combined := SemanticTokenModifierDeclaration | SemanticTokenModifierReadonly
	expected := uint32(1 | 4) // declaration (bit 0) + readonly (bit 2)

	if uint32(combined) != expected {
		t.Errorf("combined modifiers: got %d, want %d", uint32(combined), expected)
	}
}

// bitPosition is a helper that returns the bit position (0-indexed)
// of the single set bit in a power-of-2 value.
func bitPosition(n uint32) int {
	for i := 0; i < 32; i++ {
		if n == uint32(1<<uint(i)) {
			return i
		}
	}
	return -1
}
