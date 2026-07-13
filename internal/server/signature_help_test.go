package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/workspace"
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
