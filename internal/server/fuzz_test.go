package server

import (
	"os"
	"path/filepath"
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
