package server

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestDetectSignatureContext tests the pure function that classifies the signature-help
// context from a partial line and cursor position, extracting the argument index.
// (feature 17, T2, RED phase).
//
// The detector must:
// - Recognize CALLNAT and PERFORM as signature contexts, returning the context kind and arg index
// - Return sigNone for other verbs (FETCH, RUN, data-access, etc.)
// - Compute argIndex = 0-based count of space-separated argument tokens before the cursor
// - Handle trailing whitespace (cursor on next arg slot → argIndex++); mid-token → argIndex stays put
// - Clamp to valid range; never panic on empty line, keyword-only, or out-of-range cursor
func TestDetectSignatureContext(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		cursorByteCol  int
		expectKind     sigContextKind
		expectArgIndex int
		description    string
	}{
		// === CALLNAT context ===
		{
			name:           "CALLNAT keyword only (no space after)",
			line:           "CALLNAT",
			cursorByteCol:  7,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "keyword-only → no context yet",
		},
		{
			name:           "CALLNAT with target, cursor on target",
			line:           "CALLNAT 'SUBPRG'",
			cursorByteCol:  13, // Inside 'SUBPRG'
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "cursor on target → context recognized, argIndex 0",
		},
		{
			name:           "CALLNAT with target + space, cursor after space",
			line:           "CALLNAT 'SUBPRG' ",
			cursorByteCol:  17, // After trailing space
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "trailing space → moving to first arg slot",
		},
		{
			name:           "CALLNAT with target + first arg, cursor after arg",
			line:           "CALLNAT 'SUBPRG' #A ",
			cursorByteCol:  20, // After #A + space
			expectKind:     sigCallnat,
			expectArgIndex: 1,
			description:    "one complete arg + space → argIndex 1",
		},
		{
			name:           "CALLNAT with target + first arg, cursor mid-token",
			line:           "CALLNAT 'SUBPRG' #A",
			cursorByteCol:  19, // End of #A, no trailing space
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "arg token under construction (no trailing space) → argIndex 0",
		},
		{
			name:           "CALLNAT with target + two args, cursor after second arg",
			line:           "CALLNAT 'SUBPRG' #A #B ",
			cursorByteCol:  24, // After #B + space
			expectKind:     sigCallnat,
			expectArgIndex: 2,
			description:    "two complete args + space → argIndex 2",
		},
		{
			name:           "CALLNAT with target + three args, cursor mid-third arg",
			line:           "CALLNAT 'SUBPRG' #A #B #C",
			cursorByteCol:  26, // End of line, at #C
			expectKind:     sigCallnat,
			expectArgIndex: 2,
			description:    "third arg under construction → argIndex 2",
		},

		// === PERFORM context ===
		{
			name:           "PERFORM keyword only",
			line:           "PERFORM",
			cursorByteCol:  7,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "PERFORM keyword-only → no context yet",
		},
		{
			name:           "PERFORM with target, cursor on target",
			line:           "PERFORM INLINE-SUB",
			cursorByteCol:  12, // Inside INLINE-SUB
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "cursor on PERFORM target → context recognized",
		},
		{
			name:           "PERFORM with target + space",
			line:           "PERFORM INLINE-SUB ",
			cursorByteCol:  19, // After space
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "PERFORM target + space → context, argIndex 0",
		},
		{
			name:           "PERFORM with target + one arg",
			line:           "PERFORM EXT-SUB #X ",
			cursorByteCol:  19, // After #X + space
			expectKind:     sigPerform,
			expectArgIndex: 1,
			description:    "PERFORM with one arg → argIndex 1",
		},

		// === Non-call contexts ===
		{
			name:           "FETCH keyword (not a signature context)",
			line:           "FETCH 'PROG' #I",
			cursorByteCol:  11,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "FETCH is not a signature context (OQ-2)",
		},
		{
			name:           "RUN keyword (not a signature context)",
			line:           "RUN 'PROG'",
			cursorByteCol:  6,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "RUN is not a signature context (OQ-2)",
		},
		{
			name:           "READ keyword (data-access, not signature)",
			line:           "READ EMPLOYEE",
			cursorByteCol:  9,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "READ is not a signature context",
		},
		{
			name:           "Plain line, no keyword",
			line:           "WRITE 'Hello'",
			cursorByteCol:  6,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "non-call verb → no context",
		},

		// === Edge cases: degenerate input (never panic) ===
		{
			name:           "empty line",
			line:           "",
			cursorByteCol:  0,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "empty line → no context, no panic",
		},
		{
			name:           "negative cursor (clamped)",
			line:           "CALLNAT 'SUB'",
			cursorByteCol:  -5,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "negative cursor clamped to 0 → no context",
		},
		{
			name:           "cursor past end of line (clamped)",
			line:           "CALLNAT 'SUB'",
			cursorByteCol:  100,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "cursor way past end → clamped, no context",
		},
		{
			name:           "cursor at end of line",
			line:           "CALLNAT 'SUB' #A #B",
			cursorByteCol:  19, // At end
			expectKind:     sigCallnat,
			expectArgIndex: 1,
			description:    "cursor at end of line → valid context with last computed argIndex",
		},
		{
			name:           "whitespace-only line",
			line:           "    ",
			cursorByteCol:  2,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "whitespace-only line → no context",
		},
		{
			name:           "case insensitive: callnat lowercase",
			line:           "callnat 'SUB' #A ",
			cursorByteCol:  17,
			expectKind:     sigCallnat,
			expectArgIndex: 1,
			description:    "lowercase keyword → recognized",
		},
		{
			name:           "case insensitive: PERFORM mixed case",
			line:           "PerForm TARGET ",
			cursorByteCol:  15,
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "mixed-case PERFORM → recognized",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			kind, argIndex := detectSignatureContext(tc.line, tc.cursorByteCol)

			// Assert context kind
			if kind != tc.expectKind {
				t.Errorf("kind: got %v, want %v (context: %s)", kind, tc.expectKind, tc.description)
			}

			// Assert argument index
			if argIndex != tc.expectArgIndex {
				t.Errorf("argIndex: got %d, want %d (context: %s)", argIndex, tc.expectArgIndex, tc.description)
			}
		})
	}
}

// TestProvideSignatureHelp_CallnatBasic tests the signature help provider for CALLNAT
// context (feature 17, T4, RED phase).
//
// Exercises:
// - Cursor on the CALLNAT target returns signature for the resolved subprogram
// - Cursor in the argument region (after the target) returns the same signature
// - SignatureInformation includes Label and Parameters matching the subprogram's PARAMETER block
// - Parameters are rendered as "name type" (with array dims for array parameters)
// - ActiveSignature = 0 (only one signature per call)
//
// FR-48, Story 1, AC#1–AC#2.
func TestProvideSignatureHelp_CallnatBasic(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the signaturehelp fixture (CALLER.NSP + SUBPRG.NSN)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index from fixtures
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLER.NSP"},
		{"SUBPRG.NSN"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set (resolves CALLNAT 'SUBPRG' to SUBPRG.NSN)
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	tests := []struct {
		name           string
		cursorLine     int // 1-based
		cursorColumn   int // 1-based
		wantSignature  bool
		checkSignature func(*testing.T, *protocol.SignatureHelp)
		description    string
	}{
		{
			name:          "cursor on CALLNAT target 'SUBPRG'",
			cursorLine:    13, // Line with CALLNAT 'SUBPRG' #A #B
			cursorColumn:  13, // Inside 'SUBPRG' (1-based)
			wantSignature: true,
			checkSignature: func(t *testing.T, sh *protocol.SignatureHelp) {
				if sh == nil {
					t.Fatal("expected non-nil SignatureHelp")
				}

				// Assert exactly one signature
				if len(sh.Signatures) != 1 {
					t.Errorf("expected 1 signature, got %d", len(sh.Signatures))
					return
				}

				sig := sh.Signatures[0]

				// Assert the signature label contains the target name
				if sig.Label != "SUBPRG (P-NUM, P-NAME, P-ARR)" {
					t.Errorf("signature label: got %q, expected 'SUBPRG (P-NUM, P-NAME, P-ARR)'", sig.Label)
				}

				// Assert exactly 3 parameters
				if len(sig.Parameters) != 3 {
					t.Errorf("parameter count: got %d, expected 3", len(sig.Parameters))
					return
				}

				// Assert parameter labels are protocol.String and have correct format
				// First param: P-NUM (N8)
				pinfo0 := sig.Parameters[0]
				label0 := extractLabelString(t, pinfo0.Label)
				if label0 != "P-NUM N8" {
					t.Errorf("param[0] label: got %q, expected 'P-NUM N8'", label0)
				}

				// Second param: P-NAME (A50)
				pinfo1 := sig.Parameters[1]
				label1 := extractLabelString(t, pinfo1.Label)
				if label1 != "P-NAME A50" {
					t.Errorf("param[1] label: got %q, expected 'P-NAME A50'", label1)
				}

				// Third param: P-ARR (A10/1:5) — array with dimensions
				pinfo2 := sig.Parameters[2]
				label2 := extractLabelString(t, pinfo2.Label)
				if label2 != "P-ARR A10 (1:5)" {
					t.Errorf("param[2] label (array dims): got %q, expected 'P-ARR A10 (1:5)'", label2)
				}

				// Assert ActiveSignature = 0
				if sh.ActiveSignature == nil || *sh.ActiveSignature != 0 {
					t.Errorf("activeSignature: got %v, expected 0", sh.ActiveSignature)
				}
			},
			description: "cursor on target name",
		},
		{
			name:          "cursor in argument region (after target, on first arg)",
			cursorLine:    13,
			cursorColumn:  17, // On #A (first argument)
			wantSignature: true,
			checkSignature: func(t *testing.T, sh *protocol.SignatureHelp) {
				if sh == nil {
					t.Fatal("expected non-nil SignatureHelp")
				}

				// Assert exactly one signature
				if len(sh.Signatures) != 1 {
					t.Errorf("expected 1 signature, got %d", len(sh.Signatures))
					return
				}

				sig := sh.Signatures[0]

				// Assert 3 parameters
				if len(sig.Parameters) != 3 {
					t.Errorf("parameter count: got %d, expected 3", len(sig.Parameters))
				}
			},
			description: "cursor in argument list (after target)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Convert 1-based cursor to 0-based protocol position
			params := protocol.SignatureHelpParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(fixtureRoot, "CALLER.NSP")),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			// Act: call the provider
			result, err := provideSignatureHelp(hctx, params)

			// Assert no error
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Assert signature presence
			if tc.wantSignature {
				if result == nil {
					t.Fatal("expected non-nil SignatureHelp, got nil")
				}
				if tc.checkSignature != nil {
					tc.checkSignature(t, result)
				}
			} else {
				if result != nil {
					t.Errorf("expected nil SignatureHelp, got %+v", result)
				}
			}
		})
	}
}

// extractLabelString extracts the string value from a ParameterInformationLabel union.
// The label is constructed as protocol.String (a string), so we directly convert it.
func extractLabelString(t *testing.T, label protocol.ParameterInformationLabel) string {
	// ParameterInformationLabel is a union type. The string arm is directly a string value.
	// Cast to string directly since it was constructed as protocol.String.
	str := string(label.(protocol.String))
	return str
}

// TestProvideSignatureHelp_PerformInlineWins tests signature help for PERFORM with
// inline-before-external resolution (feature 17, T4a, RED phase).
//
// Exercises:
// - A caller with an inline DEFINE SUBROUTINE (no params) AND an external .NSS of the same name
// - PERFORM on the target resolves to the INLINE subroutine (FR-12: inline wins)
// - The signature reflects the inline subroutine's parameter interface (empty in this case)
// - The external subroutine's parameters are NOT shown (proving inline won)
//
// FR-48, Story 2, AC#1–AC#2; FR-12 inline-before-external.
func TestProvideSignatureHelp_PerformInlineWins(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the inline-wins fixture (PERFCALLER.NSP + MY-SUB.NSS)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index from fixtures
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"PERFCALLER.NSP"},
		{"MY-SUB.NSS"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	tests := []struct {
		name           string
		cursorLine     int // 1-based
		cursorColumn   int // 1-based
		wantSignature  bool
		checkSignature func(*testing.T, *protocol.SignatureHelp)
		description    string
	}{
		{
			name:          "PERFORM MY-SUB (inline wins, no params)",
			cursorLine:    16, // Line with PERFORM MY-SUB
			cursorColumn:  12, // On MY-SUB (1-based)
			wantSignature: true,
			checkSignature: func(t *testing.T, sh *protocol.SignatureHelp) {
				if sh == nil {
					t.Fatal("expected non-nil SignatureHelp")
				}

				// Assert exactly one signature
				if len(sh.Signatures) != 1 {
					t.Errorf("expected 1 signature, got %d", len(sh.Signatures))
					return
				}

				sig := sh.Signatures[0]

				// For inline subroutine (no params), the label should be just "MY-SUB"
				if sig.Label != "MY-SUB" {
					t.Errorf("signature label (inline, no params): got %q, expected 'MY-SUB'", sig.Label)
				}

				// Assert empty (but non-nil) Parameters slice (per Story 2 AC4)
				if sig.Parameters == nil {
					t.Errorf("Parameters should be non-nil (but empty) for a no-parameter subroutine, got nil")
				} else if len(sig.Parameters) != 0 {
					t.Errorf("parameter count (inline, no params): got %d, expected 0", len(sig.Parameters))
				}

				// Assert the external subroutine's params are NOT present
				// (i.e., we're showing the inline version, not the external one)
				// If any param started with "PARAM-" (the external's param names), the test fails
				for _, p := range sig.Parameters {
					label := extractLabelString(t, p.Label)
					if strings.HasPrefix(label, "PARAM-") {
						t.Errorf("external parameter found: %q (inline should have won over external)", label)
					}
				}

				// Assert ActiveSignature = 0
				if sh.ActiveSignature == nil || *sh.ActiveSignature != 0 {
					t.Errorf("activeSignature: got %v, expected 0", sh.ActiveSignature)
				}
			},
			description: "inline DEFINE SUBROUTINE wins over external MY-SUB.NSS (FR-12)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Convert 1-based cursor to 0-based protocol position
			params := protocol.SignatureHelpParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(fixtureRoot, "PERFCALLER.NSP")),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			// Act: call the provider
			result, err := provideSignatureHelp(hctx, params)

			// Assert no error
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Assert signature presence
			if tc.wantSignature {
				if result == nil {
					t.Fatal("expected non-nil SignatureHelp, got nil")
				}
				if tc.checkSignature != nil {
					tc.checkSignature(t, result)
				}
			} else {
				if result != nil {
					t.Errorf("expected nil SignatureHelp, got %+v", result)
				}
			}
		})
	}
}

// TestProvideSignatureHelp_PerformExternalBasic tests signature help for PERFORM
// resolving to an external subroutine (feature 17, T4a, RED phase).
//
// Exercises:
// - PERFORM EXTSUB with no inline definition → resolves to EXTSUB.NSS
// - SignatureInformation includes the external's PARAMETER block (≥2 params, including an array)
// - Parameter labels render as "name type (dims)" matching hover's style
//
// FR-48, Story 2, AC#1–AC#3.
func TestProvideSignatureHelp_PerformExternalBasic(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the external PERFORM fixture (PERFEXT.NSP + EXTSUB.NSS)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index from fixtures
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"PERFEXT.NSP"},
		{"EXTSUB.NSS"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	tests := []struct {
		name           string
		cursorLine     int // 1-based
		cursorColumn   int // 1-based
		wantSignature  bool
		checkSignature func(*testing.T, *protocol.SignatureHelp)
		description    string
	}{
		{
			name:          "PERFORM EXTSUB (external, with params)",
			cursorLine:    12, // Line with PERFORM EXTSUB
			cursorColumn:  11, // On EXTSUB (1-based)
			wantSignature: true,
			checkSignature: func(t *testing.T, sh *protocol.SignatureHelp) {
				if sh == nil {
					t.Fatal("expected non-nil SignatureHelp")
				}

				// Assert exactly one signature
				if len(sh.Signatures) != 1 {
					t.Errorf("expected 1 signature, got %d", len(sh.Signatures))
					return
				}

				sig := sh.Signatures[0]

				// Assert the signature label
				if sig.Label != "EXTSUB (EXT-NUM, EXT-NAME, EXT-VALS)" {
					t.Errorf("signature label: got %q, expected 'EXTSUB (EXT-NUM, EXT-NAME, EXT-VALS)'", sig.Label)
				}

				// Assert exactly 3 parameters
				if len(sig.Parameters) != 3 {
					t.Errorf("parameter count: got %d, expected 3", len(sig.Parameters))
					return
				}

				// First param: EXT-NUM (N8)
				pinfo0 := sig.Parameters[0]
				label0 := extractLabelString(t, pinfo0.Label)
				if label0 != "EXT-NUM N8" {
					t.Errorf("param[0] label: got %q, expected 'EXT-NUM N8'", label0)
				}

				// Second param: EXT-NAME (A30)
				pinfo1 := sig.Parameters[1]
				label1 := extractLabelString(t, pinfo1.Label)
				if label1 != "EXT-NAME A30" {
					t.Errorf("param[1] label: got %q, expected 'EXT-NAME A30'", label1)
				}

				// Third param: EXT-VALS (P7.2 / 1:10) — array with dimensions
				pinfo2 := sig.Parameters[2]
				label2 := extractLabelString(t, pinfo2.Label)
				if label2 != "EXT-VALS P7.2 (1:10)" {
					t.Errorf("param[2] label (array dims): got %q, expected 'EXT-VALS P7.2 (1:10)'", label2)
				}

				// Assert ActiveSignature = 0
				if sh.ActiveSignature == nil || *sh.ActiveSignature != 0 {
					t.Errorf("activeSignature: got %v, expected 0", sh.ActiveSignature)
				}
			},
			description: "external PERFORM with parameters and array dims",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Convert 1-based cursor to 0-based protocol position
			params := protocol.SignatureHelpParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(fixtureRoot, "PERFEXT.NSP")),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			// Act: call the provider
			result, err := provideSignatureHelp(hctx, params)

			// Assert no error
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Assert signature presence
			if tc.wantSignature {
				if result == nil {
					t.Fatal("expected non-nil SignatureHelp, got nil")
				}
				if tc.checkSignature != nil {
					tc.checkSignature(t, result)
				}
			} else {
				if result != nil {
					t.Errorf("expected nil SignatureHelp, got %+v", result)
				}
			}
		})
	}
}

// TestProvideSignatureHelp_PerformNoParams tests signature help for PERFORM
// to a subroutine with no parameters (feature 17, T4a, RED phase).
//
// Exercises:
// - PERFORM NOPARM where NOPARM.NSS has no DEFINE DATA PARAMETER block
// - Returns a non-nil SignatureInformation with empty (but non-nil) Parameters slice
// - Per Story 2 AC4: empty signature is valid, not an error
//
// FR-48, Story 2, AC#4.
func TestProvideSignatureHelp_PerformNoParams(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the no-params fixture (CALLNOPARM.NSP + NOPARM.NSS)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index from fixtures
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLNOPARM.NSP"},
		{"NOPARM.NSS"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	tests := []struct {
		name           string
		cursorLine     int // 1-based
		cursorColumn   int // 1-based
		wantSignature  bool
		checkSignature func(*testing.T, *protocol.SignatureHelp)
		description    string
	}{
		{
			name:          "PERFORM NOPARM (no parameters)",
			cursorLine:    11, // Line with PERFORM NOPARM
			cursorColumn:  11, // On NOPARM (1-based)
			wantSignature: true,
			checkSignature: func(t *testing.T, sh *protocol.SignatureHelp) {
				if sh == nil {
					t.Fatal("expected non-nil SignatureHelp")
				}

				// Assert exactly one signature
				if len(sh.Signatures) != 1 {
					t.Errorf("expected 1 signature, got %d", len(sh.Signatures))
					return
				}

				sig := sh.Signatures[0]

				// For a no-parameter subroutine, the label should be just "NOPARM"
				if sig.Label != "NOPARM" {
					t.Errorf("signature label (no params): got %q, expected 'NOPARM'", sig.Label)
				}

				// Assert empty (but non-nil) Parameters slice (per Story 2 AC4)
				if sig.Parameters == nil {
					t.Errorf("Parameters should be non-nil (but empty) for a no-parameter subroutine, got nil")
				} else if len(sig.Parameters) != 0 {
					t.Errorf("parameter count (no params): got %d, expected 0", len(sig.Parameters))
				}

				// Assert ActiveSignature = 0
				if sh.ActiveSignature == nil || *sh.ActiveSignature != 0 {
					t.Errorf("activeSignature: got %v, expected 0", sh.ActiveSignature)
				}
			},
			description: "PERFORM with no-parameter subroutine is valid, not an error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Convert 1-based cursor to 0-based protocol position
			params := protocol.SignatureHelpParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(fixtureRoot, "CALLNOPARM.NSP")),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			// Act: call the provider
			result, err := provideSignatureHelp(hctx, params)

			// Assert no error
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Assert signature presence
			if tc.wantSignature {
				if result == nil {
					t.Fatal("expected non-nil SignatureHelp, got nil")
				}
				if tc.checkSignature != nil {
					tc.checkSignature(t, result)
				}
			} else {
				if result != nil {
					t.Errorf("expected nil SignatureHelp, got %+v", result)
				}
			}
		})
	}
}

// TestProvideSignatureHelp_ActiveParameterTracking tests active-parameter tracking
// (feature 17, T5, RED phase).
//
// Exercises:
// - activeParameter index matches the cursor's argument position across the arg list
// - Moving past the last declared parameter clamps to the last index (Story 3 AC2)
// - Never panics on out-of-range indices
// - A param-less signature omits activeParameter (unset field)
//
// FR-48, Story 3, AC#1–AC#2.
func TestProvideSignatureHelp_ActiveParameterTracking(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the CALLNAT fixture (CALLER.NSP + SUBPRG.NSN)
	// SUBPRG.NSN has 3 params: P-NUM, P-NAME, P-ARR
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index from fixtures
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLER.NSP"},
		{"SUBPRG.NSN"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	tests := []struct {
		name                  string
		cursorLine            int // 1-based
		cursorColumn          int // 1-based
		expectActiveParameter int // Expected activeParameter value (or -1 if omitted)
		description           string
	}{
		{
			name:                  "cursor on CALLNAT target (arg index 0)",
			cursorLine:            13, // CALLNAT 'SUBPRG' #A #B
			cursorColumn:          13, // On 'SUBPRG'
			expectActiveParameter: 0,
			description:           "cursor on target → activeParameter 0",
		},
		{
			name:                  "cursor after target on first arg (#A)",
			cursorLine:            13,
			cursorColumn:          17, // On #A
			expectActiveParameter: 0,
			description:           "cursor on first arg → activeParameter 0",
		},
		{
			name:                  "cursor after first arg, before second",
			cursorLine:            13,
			cursorColumn:          19, // After #A, before space
			expectActiveParameter: 0,
			description:           "arg token under construction → activeParameter 0",
		},
		{
			name:                  "cursor on second arg (#B) with trailing space",
			cursorLine:            13,
			cursorColumn:          21, // After #B (within the token)
			expectActiveParameter: 1,
			description:           "cursor on second arg → activeParameter 1",
		},
		{
			name:                  "cursor on third arg (#C) with trailing space",
			cursorLine:            13,
			cursorColumn:          24, // After #B #C (with space after #B)
			expectActiveParameter: 2,
			description:           "cursor on third arg → activeParameter 2",
		},
		{
			name:                  "cursor past last parameter (would be arg 4, clamps to 2)",
			cursorLine:            13,
			cursorColumn:          26, // Past the 3 declared params (clamped)
			expectActiveParameter: 2,
			description:           "cursor past last param → activeParameter clamped to 2 (last index)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Convert 1-based cursor to 0-based protocol position
			params := protocol.SignatureHelpParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(fixtureRoot, "CALLER.NSP")),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1),
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			// Act: call the provider
			result, err := provideSignatureHelp(hctx, params)

			// Assert no error
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Assert signature is present
			if result == nil {
				t.Fatal("expected non-nil SignatureHelp")
			}

			if len(result.Signatures) != 1 {
				t.Errorf("expected 1 signature, got %d", len(result.Signatures))
				return
			}

			sig := result.Signatures[0]

			// Assert activeParameter matches expectation
			if tc.expectActiveParameter >= 0 {
				// activeParameter should be set to this value
				val, ok := sig.ActiveParameter.Get()
				if !ok {
					t.Errorf("expected ActiveParameter %d, got absent/unset", tc.expectActiveParameter)
				} else if val != uint32(tc.expectActiveParameter) {
					t.Errorf("expected ActiveParameter %d, got %d (%s)", tc.expectActiveParameter, val, tc.description)
				}
			} else {
				// activeParameter should be omitted (unset)
				_, ok := sig.ActiveParameter.Get()
				if ok {
					t.Errorf("expected ActiveParameter omitted, but it was set (%s)", tc.description)
				}
			}
		})
	}
}

// TestProvideSignatureHelp_ActiveParameterOmitted tests that activeParameter is omitted
// for param-less signatures (feature 17, T5, RED phase).
//
// Exercises:
// - A signature with zero parameters has no meaningful activeParameter
// - The field is omitted (nil) per Story 2 AC4
//
// FR-48, Story 2, AC#4; Story 3 (no meaningful index when no params).
func TestProvideSignatureHelp_ActiveParameterOmitted(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the no-params fixture (CALLNOPARM.NSP + NOPARM.NSS)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index from fixtures
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLNOPARM.NSP"},
		{"NOPARM.NSS"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the CALLNOPARM target
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "CALLNOPARM.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(10), // 0-based line 10 = 1-based line 11 (CALLNAT NOPARM)
				Character: uint32(10), // On NOPARM
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert signature is present
	if result == nil {
		t.Fatal("expected non-nil SignatureHelp")
	}

	if len(result.Signatures) != 1 {
		t.Errorf("expected 1 signature, got %d", len(result.Signatures))
		return
	}

	sig := result.Signatures[0]

	// Assert zero parameters
	if len(sig.Parameters) != 0 {
		t.Errorf("expected 0 parameters, got %d", len(sig.Parameters))
	}

	// Assert activeParameter is omitted (unset) for param-less signature
	_, ok := sig.ActiveParameter.Get()
	if ok {
		t.Errorf("expected ActiveParameter omitted (unset) for param-less signature, but it was set")
	}
}

// TestProvideSignatureHelp_MarshaledActiveParameter tests that the serialized JSON
// (via MarshalJSONTo) correctly represents activeParameter for a set case.
// (feature 17, T5, RED phase).
//
// This guards the Nullable[uint32] marshaling path — the protocol encoder must emit
// activeParameter in the JSON when it is set, and omit it when nil.
func TestProvideSignatureHelp_MarshaledActiveParameter(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the CALLNAT fixture
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLER.NSP"},
		{"SUBPRG.NSN"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the second argument (#B) — should yield activeParameter 1
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "CALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(12), // 0-based line 12 = 1-based line 13
				Character: uint32(20), // On #B (second arg)
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert signature is present
	if result == nil {
		t.Fatal("expected non-nil SignatureHelp")
	}

	// Marshal to JSON via the protocol encoder
	var buf bytes.Buffer
	encoder := jsontext.NewEncoder(&buf)
	if err := result.MarshalJSONTo(encoder); err != nil {
		t.Fatalf("failed to marshal SignatureHelp: %v", err)
	}

	jsonStr := buf.String()

	// Assert the JSON contains "activeParameter":1 (for the second arg)
	if !strings.Contains(jsonStr, `"activeParameter":1`) {
		t.Errorf("expected JSON to contain '\"activeParameter\":1', got: %s", jsonStr)
	}
}

// TestProvideSignatureHelp_DynamicTarget tests signature help for a dynamic CALLNAT
// (variable operand) (feature 17, T6, RED phase).
//
// Exercises:
// - CALLNAT #VAR (variable operand) → resolution IsDynamic() → returns nil (no signature)
// - No diagnostic is produced (FR-17: modeled gaps stay off diagnostic channel)
// - No error is returned
//
// FR-48, Story 1, AC#4; FR-17 modeled gaps.
func TestProvideSignatureHelp_DynamicTarget(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the dynamic-call fixture (CALLNAT_DYNAMIC.NSP)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLNAT_DYNAMIC.NSP"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the dynamic CALLNAT target (#SUB-NAME)
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "CALLNAT_DYNAMIC.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(9),  // 0-based line 9 = 1-based line 10
				Character: uint32(10), // On #SUB-NAME
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert result is nil (no signature for dynamic target)
	if result != nil {
		t.Errorf("expected nil SignatureHelp for dynamic target, got %+v", result)
	}
}

// TestProvideSignatureHelp_UnresolvedTarget tests signature help for an unresolved
// CALLNAT (literal target that does not exist in the workspace)
// (feature 17, T6, RED phase).
//
// Exercises:
// - CALLNAT 'NOPE' where NOPE is not in the workspace → resolution IsUnresolved() → returns nil
// - No diagnostic is produced (FR-17)
// - No error is returned
//
// FR-48, Story 1, AC#4; FR-17 modeled gaps.
func TestProvideSignatureHelp_UnresolvedTarget(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the unresolved-call fixture (CALLNAT_UNRESOLVED.NSP)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLNAT_UNRESOLVED.NSP"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the unresolved CALLNAT target ('NOPE')
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "CALLNAT_UNRESOLVED.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(8),  // 0-based line 8 = 1-based line 9
				Character: uint32(13), // On 'NOPE'
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert result is nil (no signature for unresolved target)
	if result != nil {
		t.Errorf("expected nil SignatureHelp for unresolved target, got %+v", result)
	}
}

// TestProvideSignatureHelp_AmbiguousTarget tests signature help for an ambiguous
// CALLNAT (literal target matching multiple objects in a flat namespace)
// (feature 17, T6, RED phase).
//
// Exercises:
// - CALLNAT 'AMBIG' where AMBIG is in both LIBA and LIBB → resolution IsAmbiguous() → returns nil
// - No diagnostic is produced by signature help (FR-17)
// - No error is returned
//
// FR-48, Story 1, AC#4; FR-17 modeled gaps.
func TestProvideSignatureHelp_AmbiguousTarget(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the ambiguous-call fixture (AMBIGUOUS_CALLER/MAIN.NSP with LIBA/AMBIG.NSN, LIBB/AMBIG.NSN)
	testdataDir := filepath.Join("testdata", "signaturehelp", "AMBIGUOUS_CALLER")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"MAIN.NSP"},
		{"LIBA/AMBIG.NSN"},
		{"LIBB/AMBIG.NSN"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the ambiguous CALLNAT target ('AMBIG')
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "MAIN.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(9),  // 0-based line 9 = 1-based line 10 (CALLNAT 'AMBIG')
				Character: uint32(13), // On 'AMBIG'
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert result is nil (no signature for ambiguous target)
	if result != nil {
		t.Errorf("expected nil SignatureHelp for ambiguous target, got %+v", result)
	}
}

// TestProvideSignatureHelp_FetchTarget tests that FETCH does not trigger signature help
// (feature 17, T6, RED phase, OQ-2).
//
// Exercises:
//   - FETCH 'PROG' #I → detectSignatureContext returns sigNone → provideSignatureHelp returns nil
//   - Per OQ-2 (natural-expert verified), programs invoked via FETCH receive data via the Natural
//     stack (INPUT), not declared parameters. So FETCH has no signature.
//   - This is an explicit negative test documenting the category-error decision (FR-17).
//   - No error is returned
//
// FR-48, OQ-2.
func TestProvideSignatureHelp_FetchTarget(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the FETCH fixture (FETCH_CALLER.NSP)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"FETCH_CALLER.NSP"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the FETCH target ('PROG')
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "FETCH_CALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(8),  // 0-based line 8 = 1-based line 9 (FETCH 'PROG')
				Character: uint32(10), // On 'PROG'
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert result is nil (FETCH has no signature context)
	if result != nil {
		t.Errorf("expected nil SignatureHelp for FETCH (no declared parameters), got %+v", result)
	}
}

// TestProvideSignatureHelp_RunTarget tests that RUN does not trigger signature help
// (feature 17, T6, RED phase, OQ-2).
//
// Exercises:
//   - RUN 'PROG' #I → detectSignatureContext returns sigNone → provideSignatureHelp returns nil
//   - Per OQ-2 (natural-expert verified), programs invoked via RUN receive data via the Natural
//     stack (INPUT), not declared parameters. So RUN has no signature.
//   - This is an explicit negative test documenting the category-error decision (FR-17).
//   - No error is returned
//
// FR-48, OQ-2.
func TestProvideSignatureHelp_RunTarget(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the RUN fixture (RUN_CALLER.NSP)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"RUN_CALLER.NSP"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the RUN target ('PROG')
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "RUN_CALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(8), // 0-based line 8 = 1-based line 9 (RUN 'PROG')
				Character: uint32(7), // On 'PROG'
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert result is nil (RUN has no signature context)
	if result != nil {
		t.Errorf("expected nil SignatureHelp for RUN (no declared parameters), got %+v", result)
	}
}

// TestProvideSignatureHelp_NotInCallContext tests that signature help returns nil
// when the cursor is not in a call context (feature 17, T6, RED phase).
//
// Exercises:
// - Cursor on a plain statement (MOVE, COMPUTE, etc.) → detectSignatureContext returns sigNone → returns nil
// - No error is returned
//
// FR-48, Story 4, AC#2.
func TestProvideSignatureHelp_NotInCallContext(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the not-in-context fixture (NOT_IN_CONTEXT.NSP)
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"NOT_IN_CONTEXT.NSP"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the MOVE statement (not a call context)
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "NOT_IN_CONTEXT.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(9), // 0-based line 9 = 1-based line 10 (MOVE 42 TO #X)
				Character: uint32(6), // On '42'
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Assert result is nil (not in a call context)
	if result != nil {
		t.Errorf("expected nil SignatureHelp when not in call context, got %+v", result)
	}
}

// TestProvideSignatureHelp_MarshaledParameterLabel tests that ParameterInformation.label
// serializes as a JSON string (union carrier), not as an empty object or null.
// (feature 19, T3: characterization/lock test for already-correct marshaling).
//
// Exercises:
// - The label field is a union type in protocol.ParameterInformation
// - It must serialize as a JSON string (e.g., "0:10" for a range), never as {} or null
// - This verifies the gojson marshaling path respects the union's MarshalJSONTo
//
// Story 3 AC1: wire-bytes assertion on the exact dispatch marshaling path.
func TestProvideSignatureHelp_MarshaledParameterLabel(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the CALLNAT fixture
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLER.NSP"},
		{"SUBPRG.NSN"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the second argument (#B) — should yield activeParameter 1
	// and the signature should have parameters with label fields
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "CALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(12), // 0-based line 12
				Character: uint32(20), // On #B (second arg)
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert signature is present
	if result == nil {
		t.Fatalf("expected non-nil SignatureHelp")
	}

	// Assert there is at least one signature with parameters
	if len(result.Signatures) == 0 {
		t.Fatalf("expected at least one signature")
	}

	sig := result.Signatures[0]
	if len(sig.Parameters) == 0 {
		t.Fatalf("expected signature to have parameters")
	}

	// Marshal to JSON via the protocol encoder (exact dispatch path)
	var buf bytes.Buffer
	encoder := jsontext.NewEncoder(&buf)
	if err := result.MarshalJSONTo(encoder); err != nil {
		t.Fatalf("failed to marshal SignatureHelp: %v", err)
	}

	jsonStr := buf.String()

	// Assert the JSON contains a label field as a JSON string, not as {}
	// The label should be present as a string value (e.g., "0:10" or similar range format)
	if !strings.Contains(jsonStr, `"label":"`) {
		t.Errorf("expected JSON to contain 'label' as a JSON string (e.g., '\"label\":\"...\"), got: %s", jsonStr)
	}

	// Assert the JSON does NOT contain "label":{} (would indicate union not marshaled)
	if strings.Contains(jsonStr, `"label":{}`) {
		t.Errorf("expected label to be a string, not an empty object; got: %s", jsonStr)
	}
}

// TestProvideSignatureHelp_MarshaledActiveParameterOmitted tests that activeParameter
// is OMITTED from the JSON when the signature has no parameters (Nullable omission).
// (feature 19, T3: characterization/lock test for already-correct marshaling.)
//
// Per the feature-17 provider, a PERFORM to an inline DEFINE SUBROUTINE has no
// PARAMETER block (shared scope), so buildSignatureInformation yields an empty
// Parameters slice and setActiveParameter leaves ActiveParameter unset (the zero
// Nullable[uint32]{}). This is a genuinely param-less, non-nil SignatureHelp — the
// real case that must omit activeParameter on the wire, not a faked one.
//
// Marshaling goes through the EXACT dispatch path: (*protocol.SignatureHelp).MarshalJSONTo
// via a jsontext encoder, as server.go's textDocument/signatureHelp case does. If the
// Nullable[uint32] omission rule regresses (e.g. it emits "activeParameter":0 or null for
// the zero value), this test goes red.
//
// Story 3 AC1: wire-bytes assertion proving Nullable omission for a param-less signature.
func TestProvideSignatureHelp_MarshaledActiveParameterOmitted(t *testing.T) {
	// Setup: position encoding, logger, and analyzer
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the inline-PERFORM fixture. PERFCALLER.NSP performs an inline
	// DEFINE SUBROUTINE MY-SUB with no params; the external MY-SUB.NSS (with params)
	// exists too but the inline definition wins (FR-12), yielding a param-less signature.
	testdataDir := filepath.Join("testdata", "signaturehelp")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	// Build the workspace index
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"PERFCALLER.NSP"},
		{"MY-SUB.NSS"},
	}

	for _, f := range files {
		filePath := filepath.Join(fixtureRoot, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", f.relPath, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.relPath, err)
		}

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on the PERFORM MY-SUB site (line 16, on the inline subroutine name).
	params := protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(fixtureRoot, "PERFCALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(15), // 0-based line 15 = 1-based line 16 (PERFORM MY-SUB)
				Character: uint32(11), // 0-based, on MY-SUB
			},
		},
	}

	// Act: call the provider
	result, err := provideSignatureHelp(hctx, params)

	// Assert no error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// This MUST be a real, non-nil, param-less signature (inline subroutine). We do
	// not fake it or skip: if it is nil the fixture/cursor is wrong and the test fails
	// loudly rather than silently passing.
	if result == nil {
		t.Fatal("expected non-nil param-less SignatureHelp for the inline PERFORM MY-SUB; got nil")
	}
	if len(result.Signatures) != 1 {
		t.Fatalf("expected exactly one signature, got %d", len(result.Signatures))
	}
	if len(result.Signatures[0].Parameters) != 0 {
		t.Fatalf("expected a param-less signature (inline subroutine has no PARAMETER block), got %d params",
			len(result.Signatures[0].Parameters))
	}

	// Marshal to JSON via the EXACT dispatch path: MarshalJSONTo through a jsontext encoder.
	var buf bytes.Buffer
	encoder := jsontext.NewEncoder(&buf)
	if err := result.MarshalJSONTo(encoder); err != nil {
		t.Fatalf("failed to marshal SignatureHelp: %v", err)
	}

	jsonStr := buf.String()

	// The core assertion: for a param-less signature, activeParameter must be ABSENT
	// entirely from the emitted JSON — not present as a number and not as null.
	if strings.Contains(jsonStr, `"activeParameter"`) {
		t.Errorf("activeParameter must be OMITTED for a param-less signature; got: %s", jsonStr)
	}
}
