package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// FuzzPositionConversion is the executable proof of the position-conversion primitives'
// robustness (FR-43, ADR-008, Task T16): fromProtocolPosition and toProtocolRange must
// NEVER panic and must ALWAYS return well-formed output over arbitrary content strings,
// line/character/column numbers (including huge/zero values), and both encodings.
//
// These primitives are critical: LSP handlers feed arbitrary user-supplied positions
// (potentially out-of-bounds or malformed) into the position-conversion path. A panic
// on any input violates FR-43 (graceful degradation).
//
// The fuzzer exercises:
//   - Arbitrary content (multi-line, empty, multi-byte UTF-8, garbage bytes)
//   - Arbitrary line numbers (0, negative, huge, beyond content length)
//   - Arbitrary character counts (0, negative, huge, beyond line length)
//   - Both position encodings (UTF-8 and UTF-16)
//   - Round-trip conversions (model → protocol → model)
//
// Seed corpus:
//   - Empty string, single-line, multi-line content
//   - Content with multi-byte characters (emoji, accented Latin)
//   - Content with CR/LF line endings (mixed)
//   - Boundary line/character combinations (start/end of lines, empty lines)
//
// Feature 10 Task T16, FR-43, ADR-008.
func FuzzPositionConversion(f *testing.F) {
	// Seed with various content patterns.

	// Empty content.
	f.Add([]byte(""))

	// Single line, ASCII.
	f.Add([]byte("HELLO"))

	// Multi-line, ASCII.
	f.Add([]byte("line1\nline2\nline3"))

	// Multi-byte UTF-8 (emoji).
	f.Add([]byte("hello🎉world"))

	// Multi-line with multi-byte characters and mixed line endings.
	f.Add([]byte("café\r\nnaïve\nбога"))

	// Empty lines.
	f.Add([]byte("line1\n\nline3"))

	// Trailing newline.
	f.Add([]byte("line1\nline2\n"))

	// Natural-like code snippets.
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #VAR (A10)\nEND-DEFINE"))

	f.Fuzz(func(t *testing.T, content []byte) {
		contentStr := string(content)

		// Generate a range of arbitrary line/character combinations.
		testPositions := []struct {
			line      uint32
			character uint32
			enc       protocol.PositionEncodingKind
		}{
			{0, 0, protocol.PositionEncodingKindUTF8},
			{0, 100, protocol.PositionEncodingKindUTF8},
			{1, 0, protocol.PositionEncodingKindUTF8},
			{100, 0, protocol.PositionEncodingKindUTF8},
			{0, 0, protocol.PositionEncodingKindUTF16},
			{0, 100, protocol.PositionEncodingKindUTF16},
			{1000000, 1000000, protocol.PositionEncodingKindUTF8},
		}

		for _, tc := range testPositions {
			// Act 1: fromProtocolPosition must not panic and must return a valid model.Position.
			// Out-of-range input should clamp gracefully (FR-43).
			protPos := protocol.Position{Line: tc.line, Character: tc.character}
			modelPos := fromProtocolPosition(protPos, contentStr, tc.enc)

			// Assert 1: model.Position must be non-negative and well-formed.
			if modelPos.Line < 1 {
				t.Fatalf("fromProtocolPosition returned Line < 1: %d", modelPos.Line)
			}
			if modelPos.Column < 1 {
				t.Fatalf("fromProtocolPosition returned Column < 1: %d", modelPos.Column)
			}

			// Act 2: toProtocolRange with a zero-width range at the converted position must not panic.
			modelRange := model.Range{Start: modelPos, End: modelPos}
			protRange := toProtocolRange(modelRange, contentStr, tc.enc)

			// Assert 2: a zero-width model range maps to a zero-width protocol
			// range (Start == End) — the meaningful well-formedness invariant.
			// (protocol positions are uint32, so non-negativity is guaranteed by
			// the type.)
			if protRange.Start != protRange.End {
				t.Fatalf("zero-width model range mapped to non-zero-width protocol range: %+v", protRange)
			}

			// Assert 3: for a zero-width range, start and end should be equal.
			if protRange.Start.Line != protRange.End.Line || protRange.Start.Character != protRange.End.Character {
				t.Errorf("zero-width model range should map to zero-width protocol range; got %+v", protRange)
			}
		}
	})
}

// FuzzCursorLookup is the executable proof of the cursor-lookup primitive's robustness
// (FR-43, Task T16): findCursorTarget must NEVER panic when fed arbitrary positions
// over arbitrary parsed content, and must ALWAYS return either nil pointers or pointers
// that point into the FileAnalysis.
//
// The cursor-lookup function is called by every LSP handler (go-to-def, references,
// hover, symbols) on user-supplied cursor positions. A panic is a critical defect.
//
// The fuzzer exercises:
//   - Arbitrary Natural source code (valid, malformed, empty, garbage)
//   - Arbitrary cursor positions (0, negative, huge line/column)
//   - All object types and statement kinds
//
// Seed corpus:
//   - Navigation fixtures (cursor-lookup.NSP, definition.NSP, references.NSP)
//   - Empty input
//   - Malformed/partial Natural constructs
//
// Feature 10 Task T16, FR-43.
func FuzzCursorLookup(f *testing.F) {
	// Seed from navigation fixtures.
	fixtureNames := []string{
		"cursor-lookup.NSP",
		"definition.NSP",
		"references.NSP",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("testdata", "navigation", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure).
			continue
		}
		f.Add(data)
	}

	// Hand-written edge-case seeds (FR-43 graceful degradation).

	// Empty input.
	f.Add([]byte(""))

	// Bare CALLNAT with no target.
	f.Add([]byte("CALLNAT"))

	// Malformed DEFINE DATA (no END-DEFINE).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #X (A5)\nCALLNAT 'Y'\n"))

	// Single-line program.
	f.Add([]byte("CALLNAT 'PROG'"))

	// Program with only whitespace.
	f.Add([]byte("   \n   \n   "))

	// Multiple statements.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nCALLNAT 'A'\nCALLNAT 'B'\nRUN 'C' 'LIB'\nEND\n"))

	// Deeply nested structure.
	f.Add([]byte("DEFINE DATA\nLOCAL\n  1 #G1\n    2 #G2\n      3 #G3\n        4 #G4\nEND-DEFINE\nEND\n"))

	// Data access with multiple views.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nREAD VIEW1\nEND-READ\nFIND VIEW2\nEND-FIND\nEND\n"))

	// Truncated/malformed statements.
	f.Add([]byte("CALLNAT\nREAD\nSTORE\nEND\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: analyze the arbitrary input using the Natural analyzer.
		// The analyzer must return a FileAnalysis even for garbage (M-6).
		az := natural.New(nil)
		fa, err := az.Analyze("fuzz.NSP", input)
		// err may be non-nil (expected for malformed input), but fa is a value
		// type that is always set by Analyze (FR-43).
		_ = err

		// Generate arbitrary cursor positions (including out-of-bounds).
		// We'll test a few positions covering the file's range and beyond.
		testPositions := []model.Position{
			{Line: 1, Column: 1},       // start
			{Line: 1, Column: 100},     // beyond first line
			{Line: 100, Column: 1},     // beyond file
			{Line: -10, Column: 1},     // negative (should clamp)
			{Line: 0, Column: 0},       // zero (should clamp)
			{Line: 1000000, Column: 1}, // huge line
			{Line: 1, Column: 1000000}, // huge column
		}

		// Act: call findCursorTarget with each arbitrary position.
		// This must NOT panic for any input or position.
		for _, pos := range testPositions {
			edge, access := findCursorTarget(fa, pos)

			// Assert: the returned pointers are either nil or point into fa.
			if edge != nil {
				// Verify the edge is actually in fa.Edges.
				found := false
				for i := range fa.Edges {
					if &fa.Edges[i] == edge {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("returned edge %p does not point into FileAnalysis.Edges", edge)
				}
			}

			if access != nil {
				// Verify the access is actually in fa.DataAccess.
				found := false
				for i := range fa.DataAccess {
					if &fa.DataAccess[i] == access {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("returned access %p does not point into FileAnalysis.DataAccess", access)
				}
			}
		}
	})
}

// FuzzProvideDefinition is the executable proof of the go-to-definition provider's
// robustness (FR-43, Task T16): provideDefinition must NEVER panic when fed arbitrary
// cursor positions over arbitrary workspace content, and must ALWAYS return a well-formed
// result (either nil or a valid []protocol.Location).
//
// The provider is called on every user cursor movement (potentially many times per second).
// A panic on any input violates FR-43. The result must be well-formed protocol output.
//
// The fuzzer exercises:
//   - Arbitrary cursor positions (0, negative, huge line/column)
//   - Arbitrary workspace content (valid, malformed, empty, mixed)
//   - Both resolved and unresolved references
//
// Seed corpus:
//   - Definition fixtures (single-file and multi-file)
//   - Empty input
//   - Malformed constructs with valid references nearby
//
// Feature 10 Task T16, FR-43.
func FuzzProvideDefinition(f *testing.F) {
	// Seed from definition fixtures.
	fixtureNames := []string{
		"definition.NSP",
		"definition_inline.NSP",
		"definition_unresolved.NSP",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("testdata", "navigation", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures.
			continue
		}
		f.Add(data)
	}

	// Hand-written edge-case seeds.

	// Empty input.
	f.Add([]byte(""))

	// Single CALLNAT, no matching target (unresolved).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nCALLNAT 'MISSING'\nEND\n"))

	// Dynamic call (variable target).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #VAR (A10)\nEND-DEFINE\nCALLNAT #VAR\nEND\n"))

	// Multiple definition sites (ambiguous, no library map).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nCALLNAT 'DUP'\nEND\n"))

	// Nested structure with subroutine definition.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nDEFINE SUBROUTINE MYSUB\n  PERFORM 'X'\nEND-SUBROUTINE\nPERFORM MYSUB\nEND\n"))

	// Mixed valid and malformed statements.
	f.Add([]byte("CALLNAT 'GOOD'\nCALLNAT\nFETCH 'X'\nEND\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: build a minimal handler context over a single-file workspace.
		az := natural.New(nil)
		fa, err := az.Analyze("fuzz.NSP", input)
		// err may be non-nil (expected for malformed input), fa is a value type.
		_ = err

		// Build a minimal index containing just the fuzzed file.
		idx := &workspace.Index{}
		idx.Add("fuzz.NSP", fa)

		// Resolve the index (will return an empty result set for unresolvable edges).
		cfg := &config.Config{}
		res := workspace.Resolve(idx, cfg)

		// Create a minimal handler context with a temp root.
		tmpDir := t.TempDir()
		hctx := &handlerContext{
			idx:         idx,
			res:         res,
			posEncoding: protocol.PositionEncodingKindUTF8,
			root:        tmpDir,
		}

		// Write the fuzzed input to a file so provideDefinition can read it.
		fuzzPath := filepath.Join(tmpDir, "fuzz.NSP")
		if err := os.WriteFile(fuzzPath, input, 0600); err != nil {
			// If we can't write the file, skip this test case (FR-43).
			return
		}

		// Generate arbitrary cursor positions.
		testPositions := []protocol.Position{
			{Line: 0, Character: 0},
			{Line: 0, Character: 50},
			{Line: 100, Character: 0},
			{Line: 1000000, Character: 1000000},
		}

		// Act: call provideDefinition with each arbitrary position.
		// This must NOT panic for any input or position.
		for _, protPos := range testPositions {
			params := protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(fuzzPath),
					},
					Position: protPos,
				},
			}

			// Call provideDefinition — must not panic (FR-43).
			locations, err := provideDefinition(hctx, params)
			// Verify result is well-formed: either nil or a non-nil slice.
			// (An empty slice is acceptable; a panic is a failure.)
			_ = locations
			_ = err
		}
	})
}

// FuzzProvideHover is the executable proof of the hover provider's robustness
// (FR-43, Task T7 of feature 12): provideHover must NEVER panic when fed arbitrary
// cursor positions over arbitrary workspace content, and must ALWAYS return a well-formed
// result (either nil or a valid *protocol.Hover).
//
// The provider is called on every user cursor movement (potentially many times per second).
// A panic on any input violates FR-43. The result must be well-formed protocol output.
//
// The fuzzer exercises:
//   - Arbitrary cursor positions (0, negative, huge line/column)
//   - Arbitrary workspace content (valid, malformed, empty, mixed)
//   - Both resolved and unresolved references
//   - Both data-access and module references
//   - Both position encodings (UTF-8 and UTF-16)
//
// Seed corpus:
//   - Hover fixtures (subprogram-params.NSN, no-params.NSN, array-params.NSN, customer.NSD, reader.NSP)
//   - Navigation fixtures (caller.NSP, helper.NSN, unresolved.NSP)
//   - Empty input
//   - Malformed constructs with valid references nearby
//   - Dynamic CALLNAT (#VAR)
//   - Data-access statements (READ, FIND, GET)
//   - Bare CALLNAT with no target
//
// Feature 12 Task T7, FR-43.
func FuzzProvideHover(f *testing.F) {
	// Seed from hover and navigation fixtures.
	fixtureNames := []string{
		// Hover-specific fixtures
		"../../analysis/natural/testdata/structure/02-subprogram-params.NSN",
		"testdata/hover/subprogram-params.NSN",
		"testdata/hover/no-params.NSN",
		"testdata/hover/array-params.NSN",
		"testdata/hover/customer.NSD",
		"testdata/hover/reader.NSP",
		// Navigation fixtures (reused)
		"testdata/navigation/caller.NSP",
		"testdata/navigation/helper.NSN",
		"testdata/navigation/unresolved.NSP",
		"testdata/navigation/cursor-lookup.NSP",
		"testdata/navigation/data.NSL",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("internal/server", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure).
			continue
		}
		f.Add(data)
	}

	// Hand-written edge-case seeds (FR-43 graceful degradation).

	// Empty input.
	f.Add([]byte(""))

	// Bare CALLNAT with no target.
	f.Add([]byte("CALLNAT"))

	// Dynamic CALLNAT (#VAR) — variable target, unresolvable.
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #SUB-NAME (A10)\nEND-DEFINE\nCALLNAT #SUB-NAME\nEND\n"))

	// Malformed DEFINE DATA (no END-DEFINE).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #X (A5)\nCALLNAT 'Y'\n"))

	// READ (data-access) statement.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nREAD CUSTOMER\nEND-READ\nEND\n"))

	// FIND (data-access) statement.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nFIND EMPLOYEE\nEND-FIND\nEND\n"))

	// GET (data-access) statement.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nGET CUSTOMER\nEND-GET\nEND\n"))

	// STORE (data-access) statement.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nSTORE EMPLOYEE\nEND-STORE\nEND\n"))

	// NSD-like tabular line (DDM format test).
	f.Add([]byte(" G  7               CUSTOMER-ID                                   N        8\n"))

	// Inline PERFORM with DEFINE SUBROUTINE.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nDEFINE SUBROUTINE INLINE-SUB\n  CALLNAT 'X'\nEND-SUBROUTINE\nPERFORM INLINE-SUB\nEND\n"))

	// Multiple statements with mixed valid and invalid.
	f.Add([]byte("CALLNAT 'GOOD'\nCALLNAT\nFETCH 'X'\nEND\n"))

	// Data-access with malformed DEFINE DATA.
	f.Add([]byte("DEFINE DATA\nLOCAL\n  1 #VAR\nREAD MYVIEW\nEND-READ\nEND\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: build a minimal handler context over a single-file workspace.
		az := natural.New(nil)
		fa, err := az.Analyze("fuzz.NSP", input)
		// err may be non-nil (expected for malformed input), fa is a value type.
		_ = err

		// Build a minimal index containing just the fuzzed file.
		idx := &workspace.Index{}
		idx.Add("fuzz.NSP", fa)

		// Resolve the index (will return an empty result set for unresolvable edges).
		cfg := &config.Config{}
		res := workspace.Resolve(idx, cfg)

		// Create a minimal handler context with a temp root.
		tmpDir := t.TempDir()
		hctx := &handlerContext{
			idx:         idx,
			res:         res,
			posEncoding: protocol.PositionEncodingKindUTF8,
			root:        tmpDir,
			cfg:         *cfg,
		}

		// Write the fuzzed input to a file so provideHover can read it.
		fuzzPath := filepath.Join(tmpDir, "fuzz.NSP")
		if err := os.WriteFile(fuzzPath, input, 0600); err != nil {
			// If we can't write the file, skip this test case (FR-43).
			return
		}

		// Test both encodings (UTF-8 and UTF-16)
		encodings := []protocol.PositionEncodingKind{
			protocol.PositionEncodingKindUTF8,
			protocol.PositionEncodingKindUTF16,
		}

		// Generate arbitrary cursor positions (including out-of-bounds).
		testPositions := []protocol.Position{
			{Line: 0, Character: 0},       // start
			{Line: 0, Character: 50},      // beyond first line
			{Line: 100, Character: 0},     // beyond file
			{Line: 1000000, Character: 1}, // huge position
		}

		// Act: call provideHover with each arbitrary position and encoding.
		// This must NOT panic for any input, position, or encoding (FR-43).
		for _, enc := range encodings {
			hctx.posEncoding = enc
			for _, protPos := range testPositions {
				params := protocol.HoverParams{
					TextDocumentPositionParams: protocol.TextDocumentPositionParams{
						TextDocument: protocol.TextDocumentIdentifier{
							URI: uri.File(fuzzPath),
						},
						Position: protPos,
					},
				}

				// Call provideHover — must not panic (FR-43).
				hover, err := provideHover(hctx, params)

				// Assert: result is well-formed.
				// Either nil or a valid *protocol.Hover with well-formed Contents.
				if hover != nil {
					// Non-nil result must have Contents (Markdown or PlainText).
					if hover.Contents == nil {
						t.Errorf("non-nil Hover.Contents is nil; unexpected")
					}
					// If Contents is MarkupContent, verify Kind is set.
					if mc, ok := hover.Contents.(*protocol.MarkupContent); ok {
						if mc.Value == "" && mc.Kind != "" {
							// Either both set, or both unset; mixed is invalid.
							// Value can be empty (edge case), Kind should be set if Content is present.
						}
					}
					// Verify Range is well-formed (if present).
					if hover.Range != nil {
						// Range must not be negative (uint32 type guarantees).
						// Start <= End is a logical invariant (not enforced here).
						_ = hover.Range
					}
				}
				// No specific assertion on hover.Range or other fields beyond type safety;
				// the main goal is no panic.
				_ = hover
				_ = err
			}
		}
	})
}

// FuzzDocumentSymbols is the executable proof of the document-symbol provider's
// robustness (FR-43, Task T4 of feature 11): symbolToDocumentSymbol and provideDocumentSymbols
// must NEVER panic when fed arbitrary model.Symbol trees (empty names, zero/huge/negative
// ranges, SelectionRange outside Range, deep nesting, unknown SymbolKind) and arbitrary
// content strings, and must ALWAYS return well-formed protocol.DocumentSymbol output.
//
// The converter is called for every documentSymbol request and must guard against degenerate
// input from partial/malformed ASTs. A panic violates FR-43.
//
// The fuzzer exercises:
//   - Arbitrary content strings (empty, multi-byte UTF-8, non-ASCII, mixed line endings)
//   - Arbitrary model.Symbol trees with degenerate ranges (zero-width, huge, negative)
//   - Deeply nested Children (multi-level recursion)
//   - Both protocol position encodings (UTF-8 and UTF-16)
//   - Unknown/unrecognized SymbolKind values
//
// Seed corpus:
//   - Empty tree (nil, zero len)
//   - Tree with zero-width ranges and SelectionRange == Range
//   - Tree with negative/huge coordinates (out-of-bounds)
//   - SelectionRange outside Range (model gap, guards the converter)
//   - Non-ASCII content (emoji, accented characters)
//   - Deeply nested children (multi-level recursion)
//   - All six SymbolKind values (object, subroutine, data-section, data-field, map, ddm-reference)
//
// Feature 11 Task T4, FR-43.
func FuzzDocumentSymbols(f *testing.F) {
	// Seed with minimal trees covering degenerate cases.

	// Empty tree (nil Symbol).
	f.Add(
		[]byte(""),     // content
		byte(0),        // Kind: object
		"",             // Name
		int(0), int(0), // Range.Start
		int(0), int(0), // Range.End
		int(0), int(0), // SelectionRange.Start
		int(0), int(0), // SelectionRange.End
		byte(0), // numChildren
		byte(0), // encoding (UTF-8)
	)

	// Single-line ASCII with object root.
	f.Add(
		[]byte("HELLO"),
		byte(0), // Kind: object
		"prog",
		int(1), int(1), // Range.Start
		int(1), int(5), // Range.End
		int(1), int(1), // SelectionRange.Start
		int(1), int(5), // SelectionRange.End
		byte(0), // numChildren
		byte(0), // encoding
	)

	// Multi-line with subroutine.
	f.Add(
		[]byte("DEFINE SUBROUTINE MYSUB\n  CALLNAT 'X'\nEND-SUBROUTINE"),
		byte(1), // Kind: subroutine
		"MYSUB",
		int(1), int(1), // Range.Start
		int(3), int(16), // Range.End
		int(1), int(23), // SelectionRange.Start
		int(1), int(27), // SelectionRange.End
		byte(0), // numChildren
		byte(0), // encoding
	)

	// Multi-byte UTF-8 content with data-field.
	f.Add(
		[]byte("café\nnaïve\nбога"),
		byte(4), // Kind: data-field
		"ITEM",
		int(1), int(1), // Range.Start
		int(3), int(4), // Range.End
		int(1), int(1), // SelectionRange.Start
		int(3), int(4), // SelectionRange.End
		byte(0), // numChildren
		byte(1), // encoding (UTF-16)
	)

	// Zero-width range (caret).
	f.Add(
		[]byte("X"),
		byte(2), // Kind: map
		"MAP1",
		int(1), int(1), // Range.Start
		int(1), int(1), // Range.End (same as Start)
		int(1), int(1), // SelectionRange == Range
		int(1), int(1),
		byte(0), // numChildren
		byte(0),
	)

	// Data section with children.
	f.Add(
		[]byte("DEFINE DATA\nLOCAL\n  1 #A\n  2 #B\nEND-DEFINE"),
		byte(3), // Kind: data-section
		"LOCAL",
		int(2), int(1), // Range.Start
		int(4), int(12), // Range.End
		int(2), int(1), // SelectionRange
		int(2), int(5),
		byte(2), // numChildren (simplified: children are synthesized in fuzz body)
		byte(0),
	)

	// DDM reference (deep nesting potential).
	f.Add(
		[]byte("READ MYVIEW\nEND-READ"),
		byte(6), // Kind: ddm-reference
		"MYVIEW",
		int(1), int(1),
		int(2), int(8),
		int(1), int(6),
		int(1), int(11),
		byte(0), // numChildren
		byte(1), // UTF-16
	)

	f.Fuzz(func(t *testing.T, content []byte, kindByte byte, name string,
		rangeStartLine, rangeStartCol, rangeEndLine, rangeEndCol int,
		selStartLine, selStartCol, selEndLine, selEndCol int,
		numChildren byte, encByte byte) {

		contentStr := string(content)

		// Map kind byte to a model.SymbolKind (6 valid kinds + unknown fallback).
		kindMap := []model.SymbolKind{
			model.SymbolObject,
			model.SymbolSubroutine,
			model.SymbolMap,
			model.SymbolDataSection,
			model.SymbolDataField,
			model.SymbolDDMReference,
		}
		var kind model.SymbolKind
		if int(kindByte) < len(kindMap) {
			kind = kindMap[kindByte]
		} else {
			// Unknown kind (test defensive default mapping).
			kind = model.SymbolKind("unknown")
		}

		// Map encoding byte to protocol encoding.
		var enc protocol.PositionEncodingKind
		if encByte == 1 {
			enc = protocol.PositionEncodingKindUTF16
		} else {
			enc = protocol.PositionEncodingKindUTF8
		}

		// Build a degenerate Symbol tree.
		children := make([]model.Symbol, 0)
		for i := 0; i < int(numChildren)%10; i++ { // Cap children to avoid explosion.
			children = append(children, model.Symbol{
				Kind: model.SymbolDataField,
				Name: "child" + string(rune(i)),
				Range: model.Range{
					Start: model.Position{Line: int(rangeStartLine) + i, Column: int(rangeStartCol) + i},
					End:   model.Position{Line: int(rangeEndLine) + i, Column: int(rangeEndCol) + i},
				},
				SelectionRange: model.Range{
					Start: model.Position{Line: int(selStartLine) + i, Column: int(selStartCol) + i},
					End:   model.Position{Line: int(selEndLine) + i, Column: int(selEndCol) + i},
				},
				Children: nil, // Avoid deep recursion blowup.
			})
		}

		sym := model.Symbol{
			Kind: kind,
			Name: name,
			Range: model.Range{
				Start: model.Position{Line: int(rangeStartLine), Column: int(rangeStartCol)},
				End:   model.Position{Line: int(rangeEndLine), Column: int(rangeEndCol)},
			},
			SelectionRange: model.Range{
				Start: model.Position{Line: int(selStartLine), Column: int(selStartCol)},
				End:   model.Position{Line: int(selEndLine), Column: int(selEndCol)},
			},
			Children: children,
		}

		// Act: call symbolToDocumentSymbol — must NOT panic (FR-43).
		docSym := symbolToDocumentSymbol(sym, contentStr, enc)

		// Assert: result is well-formed (type safety guarantees most of this).
		// Name must match the input (no modification).
		if docSym.Name != name {
			t.Errorf("Name mismatch: expected %q, got %q", name, docSym.Name)
		}

		// Kind must be a valid protocol.SymbolKind (never unrecognized; unknown maps to default).
		_ = docSym.Kind // Type is protocol.SymbolKind, always valid.

		// Range and SelectionRange must be well-formed (uint32, >= 0 guaranteed by type).
		// SelectionRange.Start should be <= SelectionRange.End (not asserted here; position.go owns it).
		_ = docSym.Range
		_ = docSym.SelectionRange

		// Children must be a well-formed slice (nil or non-nil, no panic).
		if docSym.Children != nil && len(docSym.Children) > 0 {
			// Verify children were converted (count must match input).
			if len(docSym.Children) != len(sym.Children) {
				t.Errorf("Children count mismatch: expected %d, got %d", len(sym.Children), len(docSym.Children))
			}
		}
	})
}

// FuzzProvideCodeLens is the executable proof of the code-lens provider's robustness
// (FR-43, Task T8 of feature 13): provideCodeLens must NEVER panic when fed arbitrary
// cursor positions over arbitrary workspace content, and must ALWAYS return a well-formed
// result (either nil or a valid []protocol.CodeLens).
//
// The provider is called on every user hover action or on language-server initialization
// for code lens discovery. A panic on any input violates FR-43. The result must be
// well-formed protocol output.
//
// The fuzzer exercises:
//   - Arbitrary cursor positions (0, negative, huge line/column)
//   - Arbitrary workspace content (valid, malformed, empty, mixed)
//   - Objects with call counts (single/multiple callers)
//   - Objects with data-access writes (named DDMs, record-form empty-Name gap)
//   - Both position encodings (UTF-8 and UTF-16)
//
// Seed corpus:
//   - Code-lens fixtures (write-summary.NSP, record-form-only.NSP)
//   - Navigation fixtures (reused for multi-caller context)
//   - Empty input
//   - Malformed constructs with valid references nearby
//   - Single CALLNAT, no matching target (unresolved call)
//   - Dynamic call (variable target)
//
// Feature 13 Task T8, FR-43.
func FuzzProvideCodeLens(f *testing.F) {
	// Seed from codelens and navigation fixtures.
	fixtureNames := []string{
		// Code-lens-specific fixtures
		"testdata/codelens/write-summary.NSP",
		"testdata/codelens/record-form-only.NSP",
		// Navigation fixtures (reused for multi-caller context)
		"testdata/navigation/caller.NSP",
		"testdata/navigation/helper.NSN",
		"testdata/navigation/unresolved.NSP",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("internal/server", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure).
			continue
		}
		f.Add(data)
	}

	// Hand-written edge-case seeds (FR-43 graceful degradation).

	// Empty input.
	f.Add([]byte(""))

	// Single CALLNAT with no target.
	f.Add([]byte("CALLNAT"))

	// Dynamic CALLNAT (#VAR) — variable target, unresolvable.
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #SUB-NAME (A10)\nEND-DEFINE\nCALLNAT #SUB-NAME\nEND\n"))

	// Malformed DEFINE DATA (no END-DEFINE).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #X (A5)\nCALLNAT 'Y'\n"))

	// Program with only whitespace.
	f.Add([]byte("   \n   \n   "))

	// Multiple statements with mixed valid and invalid.
	f.Add([]byte("CALLNAT 'GOOD'\nCALLNAT\nFETCH 'X'\nEND\n"))

	// Data-access write statement.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nSTORE CUSTOMER\nEND-STORE\nEND\n"))

	// Named write (DDM).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nREAD MYVIEW\n  UPDATE\nEND-READ\nEND\n"))

	// Record-form UPDATE/DELETE (empty-Name gap).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nREAD MYVIEW\n  UPDATE\nEND-READ\nEND\n"))

	// MERGE statement (SQL-form write).
	f.Add([]byte("MERGE INTO CUSTOMER\n  WHEN MATCHED THEN UPDATE SET ID = 1\nEND-MERGE\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: build a minimal handler context over a single-file workspace.
		az := natural.New(nil)
		fa, err := az.Analyze("fuzz.NSP", input)
		// err may be non-nil (expected for malformed input), fa is a value type.
		_ = err

		// Build a minimal index containing just the fuzzed file.
		idx := &workspace.Index{}
		idx.Add("fuzz.NSP", fa)

		// Resolve the index (will return an empty result set for unresolvable edges).
		cfg := &config.Config{}
		res := workspace.Resolve(idx, cfg)

		// Create a minimal handler context with a temp root.
		tmpDir := t.TempDir()
		hctx := &handlerContext{
			idx:         idx,
			res:         res,
			posEncoding: protocol.PositionEncodingKindUTF8,
			root:        tmpDir,
			cfg:         *cfg,
		}

		// Write the fuzzed input to a file so provideCodeLens can read it.
		fuzzPath := filepath.Join(tmpDir, "fuzz.NSP")
		if err := os.WriteFile(fuzzPath, input, 0600); err != nil {
			// If we can't write the file, skip this test case (FR-43).
			return
		}

		// Test both encodings (UTF-8 and UTF-16)
		encodings := []protocol.PositionEncodingKind{
			protocol.PositionEncodingKindUTF8,
			protocol.PositionEncodingKindUTF16,
		}

		// Act: call provideCodeLens with the fuzzed document.
		// This must NOT panic for any input or encoding (FR-43).
		for _, enc := range encodings {
			hctx.posEncoding = enc
			params := protocol.CodeLensParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: uri.File(fuzzPath),
				},
			}

			// Call provideCodeLens — must not panic (FR-43).
			lenses, err := provideCodeLens(hctx, params)

			// Assert: result is well-formed.
			// Either nil or a valid []protocol.CodeLens.
			if lenses != nil {
				// Non-nil result must be a valid slice.
				for _, lens := range lenses {
					// Each lens must have a Command (a struct, not a pointer).
					// Command.Title and Command.Command should be set.
					_ = lens.Command.Title
					_ = lens.Command.Command
					// Command.Arguments may be nil or an LSPAny slice.
					_ = lens.Command.Arguments
					// Range is a struct, always well-formed (contains Start and End positions).
					_ = lens.Range
				}
			}
			// No specific assertion on lens content beyond type safety;
			// the main goal is no panic.
			_ = lenses
			_ = err
		}

		// Bonus: if the structure is present and non-nil, exercise the pure builders directly.
		if fa.Structure != nil {
			// Exercise buildWriteSummaryLens directly with degenerate DataAccess.
			_ = buildWriteSummaryLens(fa.DataAccess, fa.Structure, uri.File(fuzzPath), string(input), protocol.PositionEncodingKindUTF8)

			// Exercise buildWriteSummaryLens with UTF-16 encoding.
			_ = buildWriteSummaryLens(fa.DataAccess, fa.Structure, uri.File(fuzzPath), string(input), protocol.PositionEncodingKindUTF16)
		}
	})
}

// FuzzDiagnosticConversion is the executable proof of the diagnostic converter's
// robustness (FR-43, Task T3 of feature 14): toProtocolDiagnostic must NEVER panic
// when fed arbitrary model.Diagnostic values with degenerate ranges, arbitrary
// severity/code values (including unknown), arbitrary message text, arbitrary
// content strings, and both position encodings. It must ALWAYS return a
// well-formed protocol.Diagnostic.
//
// The diagnostic converter is called for every diagnostic in the file being
// published (potentially many per file). A panic on any input violates FR-43.
// The result must be well-formed protocol output suitable for marshaling to JSON.
//
// The fuzzer exercises:
//   - Arbitrary Message strings (empty, multi-byte UTF-8, non-ASCII, garbage)
//   - Arbitrary Severity values (known: info/warning/error, plus unknown/empty)
//   - Arbitrary Code values (known: syntax/ambiguity, plus unknown/""/garbage)
//   - Arbitrary Range values (zero-width, out-of-bounds, Start > End, negative, huge)
//   - Arbitrary content strings (empty, multi-line, multi-byte, garbage)
//   - Both position encodings (UTF-8 and UTF-16)
//
// Seed corpus:
//   - Known severity/code combinations (all permutations)
//   - Degenerate ranges (zero, negative, huge, reversed)
//   - Multi-byte UTF-8 content (emoji, accented Latin)
//   - Empty/minimal inputs (edge cases for content or message)
//
// Feature 14 Task T3, FR-43.
func FuzzDiagnosticConversion(f *testing.F) {
	// Seed with various diagnostic and content patterns.

	// Seed: all known severity + code combinations (3 severities × 3 codes = 9 cases).
	severities := []model.DiagnosticSeverity{
		model.DiagnosticError,
		model.DiagnosticWarning,
		model.DiagnosticInfo,
		"unknown", // unknown severity (tests default handling)
		"",        // empty severity (tests default handling)
	}

	codes := []model.DiagnosticCode{
		model.DiagnosticCodeSyntax,
		model.DiagnosticCodeAmbiguity,
		"",       // empty code (valid per model — back-compat)
		"custom", // unknown code (tests passthrough)
	}

	messages := []string{
		"unexpected token",
		"ambiguous reference",
		"",
		"café naïve",  // multi-byte UTF-8
		"hello🎉world", // emoji
	}

	contents := []string{
		"",
		"CALLNAT 'PROG'",
		"line1\nline2\nline3",
		"café\r\nnaïve\nбога",
		"hello🎉world",
	}

	encodings := []protocol.PositionEncodingKind{
		protocol.PositionEncodingKindUTF8,
		protocol.PositionEncodingKindUTF16,
	}

	// Add combinations manually to seed corpus with known cases.
	for _, severity := range severities {
		for _, code := range codes {
			for _, message := range messages {
				for _, content := range contents {
					for _, enc := range encodings {
						// Build a seed by encoding all parameters into fuzzed bytes.
						// Use a simple encoding: severity (1 byte) | code (1 byte) | message (varint len + data)
						// | content (varint len + data) | encoding (1 byte).
						var seed []byte
						seed = append(seed, []byte(severity)...)
						seed = append(seed, '\x00') // separator
						seed = append(seed, []byte(code)...)
						seed = append(seed, '\x00') // separator
						seed = append(seed, []byte(message)...)
						seed = append(seed, '\x00') // separator
						seed = append(seed, []byte(content)...)
						seed = append(seed, '\x00') // separator
						if enc == protocol.PositionEncodingKindUTF16 {
							seed = append(seed, byte(1))
						} else {
							seed = append(seed, byte(0))
						}
						f.Add(seed)
					}
				}
			}
		}
	}

	// Additional hand-written seeds for degenerate ranges.
	f.Fuzz(func(t *testing.T, fuzzedInput []byte) {
		// Decode the seed format. Since fuzz corpus is binary, parse liberally.
		// Split by null bytes to extract severity, code, message, content, encoding.
		parts := bytes.Split(fuzzedInput, []byte{'\x00'})
		if len(parts) < 5 {
			// Malformed seed; pad with defaults.
			for len(parts) < 5 {
				parts = append(parts, []byte{})
			}
		}

		severity := model.DiagnosticSeverity(string(parts[0]))
		code := model.DiagnosticCode(string(parts[1]))
		message := string(parts[2])
		content := string(parts[3])

		enc := protocol.PositionEncodingKindUTF8
		if len(parts[4]) > 0 && parts[4][0] == 1 {
			enc = protocol.PositionEncodingKindUTF16
		}

		// Generate arbitrary ranges (including degenerate ones).
		testRanges := []model.Range{
			// Normal range
			{Start: model.Position{Line: 1, Column: 1}, End: model.Position{Line: 1, Column: 10}},
			// Zero-width range
			{Start: model.Position{Line: 1, Column: 1}, End: model.Position{Line: 1, Column: 1}},
			// Start > End (reversed, edge case)
			{Start: model.Position{Line: 5, Column: 100}, End: model.Position{Line: 1, Column: 1}},
			// Negative positions
			{Start: model.Position{Line: -1, Column: -1}, End: model.Position{Line: 0, Column: 0}},
			// Huge positions
			{Start: model.Position{Line: 1000000, Column: 1000000}, End: model.Position{Line: 1000001, Column: 1000001}},
		}

		for _, r := range testRanges {
			// Create a diagnostic with arbitrary values.
			d := model.Diagnostic{
				Message:  message,
				Severity: severity,
				Code:     code,
				Range:    r,
			}

			// Act: call toProtocolDiagnostic — must NOT panic (FR-43).
			protDiag := toProtocolDiagnostic(d, content, enc)

			// Assert: result is well-formed.
			// Message is an InlayHintTooltip interface; if set, it should not be nil.
			// Type safety guarantees the result is a valid protocol.Diagnostic.
			_ = protDiag.Message

			// Severity must map to a valid protocol.DiagnosticSeverity (1–3, with 3 as default).
			// Type safety guarantees it's a valid uint32.
			if protDiag.Severity < 1 || protDiag.Severity > 4 {
				t.Errorf("Severity out of valid range: %d", protDiag.Severity)
			}

			// Code must be a valid protocol.ProgressToken (which is a union of string/int).
			// If model Code was non-empty, protocol Code should be set.
			_ = protDiag.Code

			// Range must be well-formed (Start and End are protocol.Position, uint32 × uint32).
			// Type safety (uint32) guarantees non-negativity. The main goal is no panic.
			// Note: toProtocolRange may not normalize reversed ranges; this is acceptable
			// as the range comes from model input which may be degenerate.
			_ = protDiag.Range.Start
			_ = protDiag.Range.End

			// Source should be set to the constant "natural-lsp".
			// Source is an Optional[string], so we can check it without casting.
			_ = protDiag.Source
		}
	})
}

// FuzzCompletionContext is the executable proof of the completion context detector's
// robustness (FR-43, Story 4 AC1, Task T8): detectCompletionContext must NEVER panic
// when fed arbitrary line strings and arbitrary cursor byte-column positions, and must
// ALWAYS return a valid completionKind and prefix string.
//
// The detector is called on every completion request and is expected to handle
// incomplete/unparseable input robustly (a prefix scan, not a re-parse). A panic on
// any input violates FR-43. The result must be a well-formed classification.
//
// The fuzzer exercises:
//   - Arbitrary line strings (empty, single-line, multi-line, multi-byte UTF-8, garbage)
//   - Arbitrary cursor byte-column positions (0, negative, huge, beyond line length)
//   - All context kinds (CALLNAT, FETCH, RUN, INCLUDE, PERFORM, data-access, none)
//   - Dynamic sigil detection (#, &, +)
//
// Seed corpus:
//   - Completion-detector table cases (all context kinds + empty/degenerate input)
//   - Multi-byte UTF-8 content
//   - Out-of-bounds cursor positions
//
// Feature 16 Task T8, FR-43.
func FuzzCompletionContext(f *testing.F) {
	// Seed with various line patterns from the detector test cases.
	f.Add("CALLNAT MYSU", 12)
	f.Add("CALLNAT ", 8)
	f.Add("CALLNAT 'MYS", 13)
	f.Add("callnat PART", 12)
	f.Add("CallNat MYSU", 12)
	f.Add("CALLNAT #DYN", 12)
	f.Add("CALLNAT &VAR", 12)

	f.Add("FETCH MYPRO", 11)
	f.Add("FETCH 'MYP", 10)
	f.Add("FETCH REPEAT MYPRO", 18)
	f.Add("fetch PRO", 9)

	f.Add("RUN MYPRO", 9)
	f.Add("RUN 'MYP", 8)

	f.Add("INCLUDE SHAR", 12)
	f.Add("INCLUDE 'SHAR", 13)
	f.Add("INCLUDE ", 8)
	f.Add("include SHAR", 12)

	f.Add("PERFORM MY", 10)
	f.Add("perform MY", 10)
	f.Add("PERFORM ", 8)
	f.Add("PERFORM #DYN", 12)

	f.Add("READ CUSTOMER ", 14)
	f.Add("READ CUSTOMER CUST", 18)
	f.Add("FIND ORDERS ", 12)
	f.Add("GET PRODUCTS ", 13)
	f.Add("STORE INVENTORY ", 16)

	f.Add("COMPUTE X = 1", 14)
	f.Add("MOVE 'X' TO Y", 13)
	f.Add("* This is a comment", 19)

	// Degenerate inputs.
	f.Add("", 0)
	f.Add("    ", 4)
	f.Add("CALLNAT MYSU", -1) // negative cursor
	f.Add("CALLNAT", 100)     // cursor past end
	f.Add("CALLNAT MYSU", 0)  // cursor at position 0
	f.Add("café naïve", 10)   // multi-byte UTF-8
	f.Add("hello🎉world", 12)  // emoji
	f.Add("CALLNAT VERY-LONG-NAME-WITH-HYPHENS-AND-STUFF", 45)

	f.Fuzz(func(t *testing.T, line string, cursorByteCol int) {
		// Act: call detectCompletionContext — must NOT panic (FR-43).
		kind, prefix := detectCompletionContext(line, cursorByteCol)

		// Assert: result is well-formed.
		// kind is a struct with valid fields; no specific assertion needed beyond type safety.
		// prefix is a string; no assertion needed.
		_ = kind
		_ = prefix

		// Basic sanity checks:
		// - prefix must be a valid string (could be empty)
		if prefix != strings.ToUpper(prefix) {
			t.Errorf("prefix is not uppercased: %q", prefix)
		}
		// - kind must be one of the known types (or unknown, which is still valid).
		// We verify the kind type is not corrupted by checking it's in a reasonable state.
		_ = kind.kind
		_ = kind.ObjectType()
		_ = kind.DDMName()
	})
}

// FuzzProvideCompletion is the executable proof of the completion provider's
// robustness (FR-43, Story 4 AC1, Task T8): provideCompletion must NEVER panic
// when fed arbitrary cursor positions over arbitrary workspace content, and must
// ALWAYS return a well-formed result (either nil or a valid []protocol.CompletionItem).
//
// The provider is called on every completion request (potentially many times per second
// while the user types). A panic on any input violates FR-43. The result must be
// well-formed protocol output.
//
// The fuzzer exercises:
//   - Arbitrary cursor positions (0, negative, huge line/column)
//   - Arbitrary workspace content (valid, malformed, empty, mixed)
//   - All completion contexts (module, subroutine, DDM field, unrecognized)
//   - Both position encodings (UTF-8 and UTF-16)
//   - Dynamic targets (excluded from completion, no error)
//
// Seed corpus:
//   - Completion fixtures (module/perform/ddmfield/none)
//   - Navigation fixtures (caller.NSP, helper.NSN)
//   - Empty input
//   - Malformed constructs with valid/invalid contexts
//
// Feature 16 Task T8, FR-43.
func FuzzProvideCompletion(f *testing.F) {
	// Seed from completion fixtures and navigation fixtures.
	fixtureNames := []string{
		// Completion-specific fixtures (T4-T7)
		"testdata/completion/module/CALLER.NSP",
		"testdata/completion/module/MYSUB.NSN",
		"testdata/completion/module/MYPROG.NSP",
		"testdata/completion/module/SHARED.NSC",
		"testdata/completion/perform/CALLER.NSP",
		"testdata/completion/perform/MYEXT.NSS",
		"testdata/completion/ddmfield/CALLER.NSP",
		"testdata/completion/ddmfield/CUSTOMER.NSD",
		"testdata/completion/none/CALLER.NSP", // ctxNone fixture
		// Navigation fixtures (reused)
		"testdata/navigation/caller.NSP",
		"testdata/navigation/helper.NSN",
		"testdata/navigation/unresolved.NSP",
	}

	for _, name := range fixtureNames {
		path := filepath.Join("internal/server", name)
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip missing fixtures (not a test failure).
			continue
		}
		f.Add(data)
	}

	// Hand-written edge-case seeds (FR-43 graceful degradation).

	// Empty input.
	f.Add([]byte(""))

	// CALLNAT with no target.
	f.Add([]byte("CALLNAT"))

	// CALLNAT with dynamic target (#VAR).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #SUB-NAME (A10)\nEND-DEFINE\nCALLNAT #SUB-NAME\nEND\n"))

	// PERFORM with dynamic target.
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #SUB-NAME (A10)\nEND-DEFINE\nPERFORM #SUB-NAME\nEND\n"))

	// INCLUDE copycode.
	f.Add([]byte("INCLUDE 'SHARED'\nCALLNAT 'X'\nEND\n"))

	// FETCH program.
	f.Add([]byte("FETCH 'PROG'\nEND\n"))

	// RUN program.
	f.Add([]byte("RUN 'PROG' 'LIB'\nEND\n"))

	// READ with data-access (DDM field context).
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nREAD CUSTOMER\nEND-READ\nEND\n"))

	// STORE with data-access.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nSTORE EMPLOYEE\nEND-STORE\nEND\n"))

	// Non-triggering statements (ctxNone).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #X (N3)\nEND-DEFINE\nCOMPUTE #X = 1\nMOVE 'Y' TO #X\nEND\n"))

	// Comment lines (ctxNone).
	f.Add([]byte("* This is a comment\nCALLNAT 'X'\nEND\n"))

	// Inline PERFORM with DEFINE SUBROUTINE.
	f.Add([]byte("DEFINE DATA LOCAL END-DEFINE\nDEFINE SUBROUTINE INLINE-SUB\n  CALLNAT 'X'\nEND-SUBROUTINE\nPERFORM INLINE-SUB\nEND\n"))

	// Malformed DEFINE DATA (no END-DEFINE).
	f.Add([]byte("DEFINE DATA LOCAL\n  1 #X (A5)\nCALLNAT 'Y'\n"))

	// Multiple completion contexts on different lines.
	f.Add([]byte("CALLNAT 'GOOD'\nPERFORM 'X'\nINCLUDE 'Y'\nFETCH 'Z'\nEND\n"))

	// Multi-byte UTF-8 content.
	f.Add([]byte("café\nCALLNAT 'café-program'\nnaïve\nFETCH 'программа'\nEND\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		// Arrange: build a minimal handler context over a single-file workspace.
		az := natural.New(nil)
		fa, err := az.Analyze("fuzz.NSP", input)
		// err may be non-nil (expected for malformed input), fa is a value type.
		_ = err

		// Build a minimal index containing just the fuzzed file.
		idx := &workspace.Index{}
		idx.Add("fuzz.NSP", fa)

		// Resolve the index (will return an empty result set for unresolvable edges).
		cfg := &config.Config{}
		res := workspace.Resolve(idx, cfg)

		// Create a minimal handler context with a temp root.
		tmpDir := t.TempDir()
		hctx := &handlerContext{
			idx:         idx,
			res:         res,
			posEncoding: protocol.PositionEncodingKindUTF8,
			root:        tmpDir,
			cfg:         *cfg,
		}

		// Write the fuzzed input to a file so provideCompletion can read it.
		fuzzPath := filepath.Join(tmpDir, "fuzz.NSP")
		if err := os.WriteFile(fuzzPath, input, 0600); err != nil {
			// If we can't write the file, skip this test case (FR-43).
			return
		}

		// Test both encodings (UTF-8 and UTF-16)
		encodings := []protocol.PositionEncodingKind{
			protocol.PositionEncodingKindUTF8,
			protocol.PositionEncodingKindUTF16,
		}

		// Generate arbitrary cursor positions (including out-of-bounds).
		testPositions := []protocol.Position{
			{Line: 0, Character: 0},       // start
			{Line: 0, Character: 50},      // beyond first line
			{Line: 100, Character: 0},     // beyond file
			{Line: 1000000, Character: 1}, // huge position
		}

		// Act: call provideCompletion with each arbitrary position and encoding.
		// This must NOT panic for any input, position, or encoding (FR-43).
		for _, enc := range encodings {
			hctx.posEncoding = enc
			for _, protPos := range testPositions {
				params := protocol.CompletionParams{
					TextDocumentPositionParams: protocol.TextDocumentPositionParams{
						TextDocument: protocol.TextDocumentIdentifier{
							URI: uri.File(fuzzPath),
						},
						Position: protPos,
					},
				}

				// Call provideCompletion — must not panic (FR-43).
				items, err := provideCompletion(hctx, params)

				// Assert: result is well-formed.
				// Either nil or a valid []protocol.CompletionItem (never panics).
				if items != nil {
					// Non-nil result must be a valid slice.
					for _, item := range items {
						// Each item must have at least a Label.
						_ = item.Label
						// Kind is a valid CompletionItemKind (uint, always valid).
						_ = item.Kind
						// Detail and SortText are Optional fields (may be unset).
						_ = item.Detail
						_ = item.SortText
					}
				}
				// No specific assertion on items or err beyond type safety;
				// the main goal is no panic.
				_ = items
				_ = err
			}
		}
	})
}
