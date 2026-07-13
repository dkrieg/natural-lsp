package server

import (
	"testing"
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
