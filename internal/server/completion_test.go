package server

import (
	"testing"

	"natural-lsp/internal/model"
)

// TestDetectCompletionContext tests the pure function that classifies the
// completion context from a partial, possibly-incomplete line and cursor position.
// This is the spine of the completion feature (feature 16, T1).
//
// The detector must:
// - Classify contexts: CALLNAT/FETCH/RUN/INCLUDE → module contexts (expecting specific object types)
// - PERFORM → subroutine context
// - data-access verbs (READ/FIND/GET/STORE/UPDATE/DELETE) with a named view → DDM-field context (carries DDM name)
// - anything else → ctxNone
// - Extract and return the partial prefix (leading quote stripped, uppercased)
// - Be case-insensitive for keywords
// - Never panic on degenerate input (empty/whitespace lines, cursor past end, negative cursor)
func TestDetectCompletionContext(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		cursorByteCol int
		expectKind    completionKind
		expectPrefix  string
		expectDDMName string           // only set when kind carries a DDM name
		expectObjType model.ObjectType // only set when kind specifies an object type
		// For detecting dynamic references (sigil-prefixed)
		expectIsDynamic bool
	}{
		// === CALLNAT context (module/subprogram) ===
		{
			name:            "CALLNAT with partial subprogram name",
			line:            "CALLNAT MYSU",
			cursorByteCol:   12,
			expectKind:      ctxSubroutine,
			expectPrefix:    "MYSU",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: false,
		},
		{
			name:            "CALLNAT with no prefix yet (bare keyword + space)",
			line:            "CALLNAT ",
			cursorByteCol:   8,
			expectKind:      ctxSubroutine,
			expectPrefix:    "",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: false,
		},
		{
			name:            "CALLNAT with quoted partial name",
			line:            "CALLNAT 'MYS",
			cursorByteCol:   13,
			expectKind:      ctxSubroutine,
			expectPrefix:    "MYS",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: false,
		},
		{
			name:            "CALLNAT lowercase keyword (case insensitive)",
			line:            "callnat PART",
			cursorByteCol:   12,
			expectKind:      ctxSubroutine,
			expectPrefix:    "PART",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: false,
		},
		{
			name:            "CALLNAT mixed case keyword",
			line:            "CallNat MYSU",
			cursorByteCol:   12,
			expectKind:      ctxSubroutine,
			expectPrefix:    "MYSU",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: false,
		},
		{
			name:            "CALLNAT with dynamic (sigil) target",
			line:            "CALLNAT #DYN",
			cursorByteCol:   12,
			expectKind:      ctxSubroutine,
			expectPrefix:    "#DYN",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: true,
		},
		{
			name:            "CALLNAT with ampersand sigil dynamic target",
			line:            "CALLNAT &VAR",
			cursorByteCol:   12,
			expectKind:      ctxSubroutine,
			expectPrefix:    "&VAR",
			expectObjType:   model.ObjectSubprogram,
			expectIsDynamic: true,
		},

		// === FETCH context (module/program) ===
		{
			name:            "FETCH with partial program name",
			line:            "FETCH MYPRO",
			cursorByteCol:   11,
			expectKind:      ctxProgram,
			expectPrefix:    "MYPRO",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},
		{
			name:            "FETCH with quoted partial name",
			line:            "FETCH 'MYP",
			cursorByteCol:   10,
			expectKind:      ctxProgram,
			expectPrefix:    "MYP",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},
		{
			name:            "FETCH REPEAT with partial program",
			line:            "FETCH REPEAT MYPRO",
			cursorByteCol:   18,
			expectKind:      ctxProgram,
			expectPrefix:    "MYPRO",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},
		{
			name:            "FETCH RETURN with partial program",
			line:            "FETCH RETURN MYPRO",
			cursorByteCol:   18,
			expectKind:      ctxProgram,
			expectPrefix:    "MYPRO",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},
		{
			name:            "fetch lowercase keyword",
			line:            "fetch PRO",
			cursorByteCol:   9,
			expectKind:      ctxProgram,
			expectPrefix:    "PRO",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},

		// === RUN context (module/program) ===
		{
			name:            "RUN with partial program name",
			line:            "RUN MYPRO",
			cursorByteCol:   9,
			expectKind:      ctxProgram,
			expectPrefix:    "MYPRO",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},
		{
			name:            "RUN with quoted partial name",
			line:            "RUN 'MYP",
			cursorByteCol:   8,
			expectKind:      ctxProgram,
			expectPrefix:    "MYP",
			expectObjType:   model.ObjectProgram,
			expectIsDynamic: false,
		},

		// === INCLUDE context (module/copycode) ===
		{
			name:            "INCLUDE with partial copycode name",
			line:            "INCLUDE SHAR",
			cursorByteCol:   12,
			expectKind:      ctxCopycode,
			expectPrefix:    "SHAR",
			expectObjType:   model.ObjectCopycode,
			expectIsDynamic: false,
		},
		{
			name:            "INCLUDE with quoted partial name",
			line:            "INCLUDE 'SHAR",
			cursorByteCol:   13,
			expectKind:      ctxCopycode,
			expectPrefix:    "SHAR",
			expectObjType:   model.ObjectCopycode,
			expectIsDynamic: false,
		},
		{
			name:            "INCLUDE with no prefix yet",
			line:            "INCLUDE ",
			cursorByteCol:   8,
			expectKind:      ctxCopycode,
			expectPrefix:    "",
			expectObjType:   model.ObjectCopycode,
			expectIsDynamic: false,
		},
		{
			name:            "include lowercase keyword",
			line:            "include SHAR",
			cursorByteCol:   12,
			expectKind:      ctxCopycode,
			expectPrefix:    "SHAR",
			expectObjType:   model.ObjectCopycode,
			expectIsDynamic: false,
		},

		// === PERFORM context (subroutine) ===
		{
			name:            "PERFORM with partial subroutine name",
			line:            "PERFORM MY",
			cursorByteCol:   10,
			expectKind:      newSubroutineContext(model.ObjectExternalSubroutine),
			expectPrefix:    "MY",
			expectObjType:   model.ObjectExternalSubroutine,
			expectIsDynamic: false,
		},
		{
			name:            "perform lowercase keyword",
			line:            "perform MY",
			cursorByteCol:   10,
			expectKind:      newSubroutineContext(model.ObjectExternalSubroutine),
			expectPrefix:    "MY",
			expectObjType:   model.ObjectExternalSubroutine,
			expectIsDynamic: false,
		},
		{
			name:            "PERFORM with no prefix yet",
			line:            "PERFORM ",
			cursorByteCol:   8,
			expectKind:      newSubroutineContext(model.ObjectExternalSubroutine),
			expectPrefix:    "",
			expectObjType:   model.ObjectExternalSubroutine,
			expectIsDynamic: false,
		},
		{
			name:            "PERFORM with dynamic sigil target",
			line:            "PERFORM #DYN",
			cursorByteCol:   12,
			expectKind:      newSubroutineContext(model.ObjectExternalSubroutine),
			expectPrefix:    "#DYN",
			expectObjType:   model.ObjectExternalSubroutine,
			expectIsDynamic: true,
		},

		// === Data-access contexts (DDM/field completion) ===
		// Note: For DDM contexts, we use a placeholder (ctxNone for now) in expectKind
		// but assert the DDM name separately. The real comparison is via the DDMName() method.
		{
			name:            "READ data-access with named view",
			line:            "READ CUSTOMER ",
			cursorByteCol:   14,
			expectKind:      ctxNone, // Will be ctxDDMField in real implementation
			expectPrefix:    "",
			expectDDMName:   "CUSTOMER",
			expectIsDynamic: false,
		},
		{
			name:            "READ data-access with view and partial field name",
			line:            "READ CUSTOMER CUST",
			cursorByteCol:   18,
			expectKind:      ctxNone, // Will be ctxDDMField in real implementation
			expectPrefix:    "CUST",
			expectDDMName:   "CUSTOMER",
			expectIsDynamic: false,
		},
		{
			name:            "FIND data-access with view name",
			line:            "FIND ORDERS ",
			cursorByteCol:   12,
			expectKind:      ctxNone, // Will be ctxDDMField in real implementation
			expectPrefix:    "",
			expectDDMName:   "ORDERS",
			expectIsDynamic: false,
		},
		{
			name:            "GET data-access with view name",
			line:            "GET PRODUCTS ",
			cursorByteCol:   13,
			expectKind:      ctxNone, // Will be ctxDDMField in real implementation
			expectPrefix:    "",
			expectDDMName:   "PRODUCTS",
			expectIsDynamic: false,
		},
		{
			name:            "STORE data-access with view name",
			line:            "STORE INVENTORY ",
			cursorByteCol:   16,
			expectKind:      ctxNone, // Will be ctxDDMField in real implementation
			expectPrefix:    "",
			expectDDMName:   "INVENTORY",
			expectIsDynamic: false,
		},

		// === ctxNone: unrecognized contexts ===
		{
			name:            "COMPUTE statement (no completion)",
			line:            "COMPUTE X = 1",
			cursorByteCol:   14,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
		{
			name:            "MOVE statement (no completion)",
			line:            "MOVE 'X' TO Y",
			cursorByteCol:   13,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
		{
			name:            "comment statement (no completion)",
			line:            "* This is a comment",
			cursorByteCol:   19,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},

		// === Degenerate input (never panics) ===
		{
			name:            "empty line",
			line:            "",
			cursorByteCol:   0,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
		{
			name:            "whitespace-only line",
			line:            "    ",
			cursorByteCol:   4,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
		{
			name:            "cursorByteCol negative",
			line:            "CALLNAT MYSU",
			cursorByteCol:   -1,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
		{
			name:            "cursorByteCol past end of line",
			line:            "CALLNAT",
			cursorByteCol:   100,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
		{
			name:            "cursorByteCol at position 0",
			line:            "CALLNAT MYSU",
			cursorByteCol:   0,
			expectKind:      ctxNone,
			expectPrefix:    "",
			expectIsDynamic: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			kind, prefix := detectCompletionContext(tc.line, tc.cursorByteCol)

			// Assert prefix matches expectation
			if prefix != tc.expectPrefix {
				t.Errorf("prefix: got %q, want %q", prefix, tc.expectPrefix)
			}

			// For DDM-field contexts, assert the DDM name is carried
			if tc.expectDDMName != "" {
				ddmName := kind.DDMName()
				if ddmName != tc.expectDDMName {
					t.Errorf("DDM name: got %q, want %q", ddmName, tc.expectDDMName)
				}
			} else {
				// For non-DDM contexts, assert kind and object type
				if kind != tc.expectKind {
					t.Errorf("kind: got %v, want %v", kind, tc.expectKind)
				}

				// For contexts with object type info, assert the detector provides
				// the right object type
				if tc.expectKind == ctxSubroutine || tc.expectKind == ctxProgram || tc.expectKind == ctxCopycode {
					if kind.ObjectType() != tc.expectObjType {
						t.Errorf("object type: got %v, want %v", kind.ObjectType(), tc.expectObjType)
					}
				}
			}

			// For dynamic detection, assert sigil presence
			if tc.expectIsDynamic {
				if !isPrefixDynamic(prefix) {
					t.Errorf("expected dynamic prefix (contains sigil), got %q", prefix)
				}
			} else {
				if isPrefixDynamic(prefix) && prefix != "" {
					t.Errorf("expected non-dynamic prefix, got %q", prefix)
				}
			}
		})
	}
}

// Helper to check if a prefix is dynamic (starts with a sigil).
// Used by the test to verify dynamic-detection capability.
func isPrefixDynamic(prefix string) bool {
	if len(prefix) == 0 {
		return false
	}
	first := prefix[0]
	return first == '#' || first == '&' || first == '+'
}
