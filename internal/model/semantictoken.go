package model

// SemanticToken is an on-demand, NON-persisted classification of one source token.
//
// It is NOT a FileAnalysis field and never enters the cache. Semantic tokens are
// computed on-demand from the open buffer (feature 29) for syntax/semantic highlighting
// provided by the LSP server to the editor. The Type and Modifiers describe the token's
// classification (keyword, variable, function, etc.) and metadata (declaration, readonly, etc.)
// for client-side rendering.
type SemanticToken struct {
	// Range is the source span of this token (1-based line/column with inclusive end,
	// using byte-offset columns). The server encodes this to LSP wire units
	// (code-unit offsets per the negotiated encoding).
	Range Range

	// Type is the stable, machine-readable classification of this token
	// (keyword, comment, variable, function, etc.), one of the SemanticTokenType constants.
	// Type is never empty; it is always one of the legend types.
	Type SemanticTokenType

	// Modifiers is a bitset of flags (declaration, definition, readonly, modification, defaultLibrary)
	// indicating metadata about this token. Zero modifiers is valid (no flags set).
	// Multiple modifiers can be combined with bitwise OR.
	Modifiers SemanticTokenModifier
}

// SemanticTokenType is the stable string classification of a token, matching the legend.
// String values are the wire contract and must be stable.
type SemanticTokenType string

const (
	// SemanticTokenTypeKeyword is the "keyword" token type.
	SemanticTokenTypeKeyword SemanticTokenType = "keyword"

	// SemanticTokenTypeComment is the "comment" token type.
	SemanticTokenTypeComment SemanticTokenType = "comment"

	// SemanticTokenTypeString is the "string" token type.
	SemanticTokenTypeString SemanticTokenType = "string"

	// SemanticTokenTypeNumber is the "number" token type.
	SemanticTokenTypeNumber SemanticTokenType = "number"

	// SemanticTokenTypeOperator is the "operator" token type.
	SemanticTokenTypeOperator SemanticTokenType = "operator"

	// SemanticTokenTypeVariable is the "variable" token type.
	SemanticTokenTypeVariable SemanticTokenType = "variable"

	// SemanticTokenTypeParameter is the "parameter" token type.
	SemanticTokenTypeParameter SemanticTokenType = "parameter"

	// SemanticTokenTypeFunction is the "function" token type.
	SemanticTokenTypeFunction SemanticTokenType = "function"

	// SemanticTokenTypeType is the "type" token type (for DDM/view names).
	SemanticTokenTypeType SemanticTokenType = "type"

	// SemanticTokenTypeProperty is the "property" token type (for DDM/view fields).
	SemanticTokenTypeProperty SemanticTokenType = "property"
)

// SemanticTokenModifier is a bitset of flags indicating metadata about a token.
// Each constant represents one bit position; multiple modifiers can be combined with bitwise OR.
// Bit positions match the legend (feature 29, OQ-1).
type SemanticTokenModifier uint32

const (
	// SemanticTokenModifierDeclaration indicates the token is a declaration (bit 0).
	SemanticTokenModifierDeclaration SemanticTokenModifier = 1 << iota // = 1

	// SemanticTokenModifierDefinition indicates the token is a definition (bit 1).
	SemanticTokenModifierDefinition // = 2

	// SemanticTokenModifierReadonly indicates the token is readonly (bit 2).
	SemanticTokenModifierReadonly // = 4

	// SemanticTokenModifierModification indicates the token is a write target / modification (bit 3).
	SemanticTokenModifierModification // = 8

	// SemanticTokenModifierDefaultLibrary indicates the token is a built-in / system variable (bit 4).
	SemanticTokenModifierDefaultLibrary // = 16
)
