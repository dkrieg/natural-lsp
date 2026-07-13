package server

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"natural-lsp/internal/model"
)

// TestDetectSignatureContext tests the pure function that classifies whether a
// cursor position is in a CALLNAT or PERFORM call context and, if so, computes
// the 0-based argument index (count of whitespace-separated argument tokens before the cursor).
//
// This is the spine of signature help's active-parameter detection (feature 17, T2).
//
// The detector must:
// - Classify CALLNAT → sigCallnat, PERFORM → sigPerform, everything else → sigNone
// - Compute argIndex = count of whitespace-separated tokens between the target token and cursor
// - Fire on or after the target (cursor still in Source range)
// - Handle trailing whitespace: space means cursor has moved to next arg slot (index +1)
// - Handle mid-token typing: no trailing space means token is under construction (index unchanged)
// - Be case-insensitive for keywords
// - Never panic on degenerate input (empty/whitespace lines, cursor past end, negative cursor)
// - Never return negative argIndex
func TestDetectSignatureContext(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		cursorByteCol  int
		expectKind     sigContextKind
		expectArgIndex int
		description    string // for clarity on the test's intent
	}{
		// === CALLNAT context ===
		{
			name:           "CALLNAT only, no args",
			line:           "CALLNAT",
			cursorByteCol:  7,
			expectKind:     sigNone, // keyword-only, no space → sigNone
			expectArgIndex: 0,
			description:    "cursor at end of keyword-only line",
		},
		{
			name:           "CALLNAT with trailing space, cursor at space (first arg slot)",
			line:           "CALLNAT ",
			cursorByteCol:  8,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "cursor on trailing space after keyword",
		},
		{
			name:           "CALLNAT 'SUB' cursor on target name",
			line:           "CALLNAT 'SUB'",
			cursorByteCol:  11,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "cursor on target, no args yet",
		},
		{
			name:           "CALLNAT 'SUB' with space, cursor at first arg slot",
			line:           "CALLNAT 'SUB' ",
			cursorByteCol:  14,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "cursor after target + space, at first arg slot",
		},
		{
			name:           "CALLNAT 'SUB' A typing first arg, no trailing space",
			line:           "CALLNAT 'SUB' A",
			cursorByteCol:  15,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "cursor mid-typing first arg (no trailing space) → arg 0",
		},
		{
			name:           "CALLNAT 'SUB' A with trailing space, cursor at second arg slot",
			line:           "CALLNAT 'SUB' A ",
			cursorByteCol:  16,
			expectKind:     sigCallnat,
			expectArgIndex: 1,
			description:    "cursor after first arg + space → arg 1",
		},
		{
			name:           "CALLNAT 'SUB' A B progress to third arg slot",
			line:           "CALLNAT 'SUB' A B",
			cursorByteCol:  17,
			expectKind:     sigCallnat,
			expectArgIndex: 1,
			description:    "cursor mid-typing second arg (no trailing space) → arg 1",
		},
		{
			name:           "CALLNAT 'SUB' A B with trailing space, third arg slot",
			line:           "CALLNAT 'SUB' A B ",
			cursorByteCol:  18,
			expectKind:     sigCallnat,
			expectArgIndex: 2,
			description:    "cursor after second arg + space → arg 2",
		},
		{
			name:           "CALLNAT lowercase keyword",
			line:           "callnat 'SUB' ",
			cursorByteCol:  14,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "case-insensitive keyword match",
		},
		{
			name:           "CALLNAT mixed case keyword",
			line:           "CallNat 'SUB' ",
			cursorByteCol:  14,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "mixed-case keyword match",
		},
		{
			name:           "CALLNAT with quoted target, bare args",
			line:           "CALLNAT 'SUBPRG' VAR1 VAR2 ",
			cursorByteCol:  28,
			expectKind:     sigCallnat,
			expectArgIndex: 2,
			description:    "quoted target, multiple bare args, cursor after last + space",
		},
		{
			name:           "CALLNAT bare target (unquoted)",
			line:           "CALLNAT SUBPRG ",
			cursorByteCol:  15,
			expectKind:     sigCallnat,
			expectArgIndex: 0,
			description:    "bare unquoted target (less common but valid)",
		},

		// === PERFORM context ===
		{
			name:           "PERFORM only, no args",
			line:           "PERFORM",
			cursorByteCol:  7,
			expectKind:     sigNone, // keyword-only, no space → sigNone
			expectArgIndex: 0,
			description:    "cursor at end of keyword-only PERFORM",
		},
		{
			name:           "PERFORM with trailing space",
			line:           "PERFORM ",
			cursorByteCol:  8,
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "cursor on trailing space after PERFORM",
		},
		{
			name:           "PERFORM 'SUB' cursor on target name",
			line:           "PERFORM 'SUB'",
			cursorByteCol:  12,
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "cursor on PERFORM target, no args",
		},
		{
			name:           "PERFORM 'SUB' with space at first arg slot",
			line:           "PERFORM 'SUB' ",
			cursorByteCol:  14,
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "cursor after PERFORM target + space",
		},
		{
			name:           "PERFORM 'SUB' A B C with full args",
			line:           "PERFORM 'SUB' A B C ",
			cursorByteCol:  20,
			expectKind:     sigPerform,
			expectArgIndex: 3,
			description:    "cursor after three args + space",
		},
		{
			name:           "PERFORM lowercase keyword",
			line:           "perform 'SUB' ",
			cursorByteCol:  14,
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "case-insensitive PERFORM",
		},
		{
			name:           "PERFORM mixed case keyword",
			line:           "PerForm 'SUB' ",
			cursorByteCol:  14,
			expectKind:     sigPerform,
			expectArgIndex: 0,
			description:    "mixed-case PERFORM",
		},

		// === Non-call contexts (sigNone) ===
		{
			name:           "FETCH context → sigNone",
			line:           "FETCH 'PRG' ",
			cursorByteCol:  12,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "FETCH is NOT a signature context (OQ-2, deferred)",
		},
		{
			name:           "RUN context → sigNone",
			line:           "RUN 'PRG' ",
			cursorByteCol:  10,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "RUN is NOT a signature context (OQ-2, deferred)",
		},
		{
			name:           "READ data-access verb → sigNone",
			line:           "READ VIEW ",
			cursorByteCol:  10,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "data-access verbs are not call contexts",
		},
		{
			name:           "STORE data-access verb → sigNone",
			line:           "STORE VIEW ",
			cursorByteCol:  11,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "STORE is not a signature context",
		},
		{
			name:           "COMPUTE statement → sigNone",
			line:           "COMPUTE X = 1 ",
			cursorByteCol:  14,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "plain statement, not a call",
		},
		{
			name:           "MOVE statement → sigNone",
			line:           "MOVE X TO Y ",
			cursorByteCol:  12,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "MOVE is not a call context",
		},
		{
			name:           "plain line with no keyword → sigNone",
			line:           "Some plain text ",
			cursorByteCol:  16,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "no recognized keyword",
		},

		// === Degenerate input (never panics) ===
		{
			name:           "empty line",
			line:           "",
			cursorByteCol:  0,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "empty line",
		},
		{
			name:           "whitespace-only line",
			line:           "    ",
			cursorByteCol:  4,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "whitespace-only line",
		},
		{
			name:           "cursor negative",
			line:           "CALLNAT 'SUB' ",
			cursorByteCol:  -1,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "negative cursor column (clamped to 0)",
		},
		{
			name:           "cursor past end of line",
			line:           "CALLNAT 'SUB' A",
			cursorByteCol:  100,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "cursor past line end (clamped to line length)",
		},
		{
			name:           "cursor at position 0",
			line:           "CALLNAT 'SUB'",
			cursorByteCol:  0,
			expectKind:     sigNone,
			expectArgIndex: 0,
			description:    "cursor at line start",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			kind, argIndex := detectSignatureContext(tc.line, tc.cursorByteCol)

			// Assert kind
			if kind != tc.expectKind {
				t.Errorf("kind: got %s, want %s (case: %s)", kind, tc.expectKind, tc.description)
			}

			// Assert argIndex
			if argIndex != tc.expectArgIndex {
				t.Errorf("argIndex: got %d, want %d (case: %s)", argIndex, tc.expectArgIndex, tc.description)
			}

			// Assert argIndex is never negative
			if argIndex < 0 {
				t.Errorf("argIndex should never be negative, got %d (case: %s)", argIndex, tc.description)
			}
		})
	}
}

// TestBuildSignatureInformation tests the pure builder that renders a subroutine
// signature as a protocol.SignatureInformation with structured ParameterInformation
// entries (feature 17, T3 RED phase).
//
// Semantics (from tasks.md T3):
// - Filter defs to SectionKind=="parameter", in declaration order
// - Honor array dimensions (same rendering as hover: "1:10", "1:*" for unbounded)
// - Honor group nesting (same way hover does)
// - Each parameter → protocol.ParameterInformation{ Label: protocol.String("<name> <type-with-dims>") }
// - SignatureInformation.Label = a readable header (e.g., "<name> (<p1>, <p2>, ...)")
// - Empty parameter interface → SignatureInformation with empty (non-nil) Parameters slice
// - Pure function: no I/O, no locks
//
// IMPORTANT REFACTOR INVARIANT (T3 GREEN phase):
// The existing hover tests in hover_test.go (TestBuildSubroutineHover_WithParameters,
// TestBuildSubroutineHover_NoParameters) MUST remain byte-identical after T3's
// green extraction refactor. The refactor will extract a shared helper so both hover
// and signature help render the same "name + type" string, but hover's Markdown
// output and test assertions must NOT change. Do not modify hover_test.go during
// the refactor — only refactor hover.go and signature_help.go to call the shared helper.
func TestBuildSignatureInformation(t *testing.T) {
	tests := []struct {
		name                string
		inputName           string
		inputDefs           []model.DataDefinition
		expectLabelContains string   // substring that should appear in SignatureInformation.Label
		expectParamCount    int      // expected number of ParameterInformation entries
		expectParamLabels   []string // expected Label strings for each ParameterInformation
		description         string
	}{
		// === Case (a): Multiple parameters with mixed types and arrays ===
		{
			name:      "two_params_scalar_and_array",
			inputName: "MYROUTINE",
			inputDefs: []model.DataDefinition{
				// Non-parameter defs (should be filtered out)
				{
					Name:        "LOCAL-VAR",
					Type:        "N5",
					SectionKind: "local",
					Level:       1,
				},
				// Parameter defs (should be included)
				{
					Name:        "#PNUM",
					Type:        "N8",
					SectionKind: "parameter",
					Level:       1,
				},
				{
					Name:        "#ARR",
					Type:        "A10",
					SectionKind: "parameter",
					Level:       1,
					Dimensions: []model.ArrayDimension{
						{Lower: 1, Upper: 5, UpperUnbounded: false},
					},
				},
			},
			expectLabelContains: "MYROUTINE",
			expectParamCount:    2,
			expectParamLabels: []string{
				"#PNUM N8",
				"#ARR A10 (1:5)",
			},
			description: "filter to parameters, render type + dims",
		},

		// === Case (b): Array parameter with unbounded dimension ===
		{
			name:      "array_param_unbounded",
			inputName: "ARRGEN",
			inputDefs: []model.DataDefinition{
				{
					Name:        "#ITEMS",
					Type:        "N3",
					SectionKind: "parameter",
					Level:       1,
					Dimensions: []model.ArrayDimension{
						{Lower: 1, Upper: 0, UpperUnbounded: true},
					},
				},
			},
			expectLabelContains: "ARRGEN",
			expectParamCount:    1,
			expectParamLabels: []string{
				"#ITEMS N3 (1:*)",
			},
			description: "unbounded array dimension rendered as 1:*",
		},

		// === Case (c): Empty parameter interface ===
		{
			name:      "no_parameters",
			inputName: "NOPARAM",
			inputDefs: []model.DataDefinition{
				{
					Name:        "#LOCALVAR",
					Type:        "N5",
					SectionKind: "local",
					Level:       1,
				},
				{
					Name:        "#ANOTHERLOCAL",
					Type:        "A20",
					SectionKind: "local",
					Level:       1,
				},
			},
			expectLabelContains: "NOPARAM",
			expectParamCount:    0, // empty (non-nil) slice
			expectParamLabels:   []string{},
			description:         "no parameters → empty Parameters slice (non-nil)",
		},

		// === Case (d): Group nesting ===
		{
			name:      "group_with_children",
			inputName: "GROUPED",
			inputDefs: []model.DataDefinition{
				{
					Name:        "OUT-RESULT", // group header (no Type)
					Type:        "",
					SectionKind: "parameter",
					Level:       1,
					Children: []model.DataDefinition{
						{
							Name:        "RES-CODE",
							Type:        "N1",
							SectionKind: "parameter",
							Level:       2,
						},
						{
							Name:        "RES-MSG",
							Type:        "A50",
							SectionKind: "parameter",
							Level:       2,
						},
					},
				},
			},
			expectLabelContains: "GROUPED",
			expectParamCount:    3, // group header + 2 children (flat enumeration)
			expectParamLabels: []string{
				"OUT-RESULT",  // group header (no type)
				"RES-CODE N1", // child scalar
				"RES-MSG A50", // child scalar
			},
			description: "group nesting: header (no type) + children rendered as params",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: build the signature information
			result := buildSignatureInformation(tc.inputName, tc.inputDefs)

			// Assert: result is non-nil
			if result == nil {
				t.Fatal("buildSignatureInformation returned nil, expected non-nil *protocol.SignatureInformation")
			}

			// Assert: Label contains the routine name
			if !strings.Contains(result.Label, tc.expectLabelContains) {
				t.Errorf("SignatureInformation.Label missing expected substring %q; got: %q", tc.expectLabelContains, result.Label)
			}

			// Assert: Parameters count matches
			if len(result.Parameters) != tc.expectParamCount {
				t.Errorf("Parameters count: got %d, want %d (case: %s)", len(result.Parameters), tc.expectParamCount, tc.description)
			}

			// Assert: Parameters is always non-nil (even if empty) per Story 2 AC4
			if result.Parameters == nil {
				t.Errorf("Parameters slice must be non-nil (even if empty); got nil (case: %s)", tc.description)
			}

			// Assert: each ParameterInformation.Label matches expected string
			for i, param := range result.Parameters {
				if i >= len(tc.expectParamLabels) {
					t.Errorf("unexpected ParameterInformation at index %d (case: %s)", i, tc.description)
					break
				}

				// Extract the string value from the union Label
				label := param.Label
				if label == nil {
					t.Errorf("ParameterInformation[%d].Label is nil (case: %s)", i, tc.description)
					continue
				}

				// Type-assert to protocol.String to read the value
				strLabel, ok := label.(protocol.String)
				if !ok {
					t.Errorf("ParameterInformation[%d].Label is not protocol.String; got %T (case: %s)", i, label, tc.description)
					continue
				}

				if string(strLabel) != tc.expectParamLabels[i] {
					t.Errorf("ParameterInformation[%d].Label: got %q, want %q (case: %s)",
						i, string(strLabel), tc.expectParamLabels[i], tc.description)
				}
			}
		})
	}
}
