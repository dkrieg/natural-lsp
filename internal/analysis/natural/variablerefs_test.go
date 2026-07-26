package natural

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestExtractVariableRefs_BasicCapture verifies T2 feature:
// extractVariableRefs returns every variable use-site in the source with
// correct name (normalized upper-case) and precise source range.
//
// OQ-1, OQ-6: scan the lexer token stream (not raw text) so comments and
// string literals are excluded for free.
//
// Story 1/2 foundation (T2 net-new work).
func TestExtractVariableRefs_BasicCapture(t *testing.T) {
	// Read the fixture
	content, err := os.ReadFile(filepath.Join("testdata", "variablerefs", "01-basic-captures.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Extract variable refs (NOT-YET-IMPLEMENTED; compile failure is expected)
	refs := ExtractVariableRefs(string(content))

	tests := []struct {
		name   string
		verify func(t *testing.T, refs []model.VariableRef)
	}{
		{
			name: "BasicCapture_scalarVariable",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// #CUSTOMER-NAME is used in multiple places:
				// - MOVE #CUSTOMER-NAME TO #COUNTER (line ~23)
				// - MOVE #TABLE(#INDEX) TO #CUSTOMER-NAME (line ~33)
				// - MOVE '#CUSTOMER-NAME' TO #COUNTER (line ~38, INSIDE STRING — should NOT be captured)
				// We should capture exactly 2 occurrences (not the one in the string)

				// Count occurrences of #CUSTOMER-NAME
				customerNameRefs := 0
				for _, ref := range refs {
					if ref.Name == "#CUSTOMER-NAME" {
						customerNameRefs++
					}
				}

				if customerNameRefs != 2 {
					t.Errorf("Found %d #CUSTOMER-NAME refs, want 2 (excluding the one in the string literal)", customerNameRefs)
				}

				// Verify we actually have non-zero ranges for the captures
				hasValidRange := false
				for _, ref := range refs {
					if ref.Name == "#CUSTOMER-NAME" {
						if ref.Range.Start.Line > 0 && ref.Range.Start.Column > 0 {
							hasValidRange = true
							break
						}
					}
				}
				if !hasValidRange {
					t.Error("No #CUSTOMER-NAME refs with valid (non-zero) Range found")
				}
			},
		},
		{
			name: "BasicCapture_groupQualifiedField",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// #GROUP.#FIELD-1 is used in "MOVE 100 TO #GROUP.#FIELD-1" (line ~26)
				// The task plan says to capture group-qualified spans such that a cursor
				// on either #GROUP or #FIELD-1 resolves the same logical reference.
				// Design decision: capture the full qualified span #GROUP.#FIELD-1 as a single ref,
				// with the full range spanning from #GROUP through #FIELD-1.

				// For now, verify we capture BOTH #GROUP and #FIELD-1 at that location
				// (we'll refine the representation once we understand the exact design)
				groupRefs := 0
				field1Refs := 0
				for _, ref := range refs {
					if ref.Name == "#GROUP" {
						groupRefs++
					}
					if ref.Name == "#FIELD-1" {
						field1Refs++
					}
				}

				// Expect to capture the group and its field (at minimum)
				if groupRefs == 0 {
					t.Error("No #GROUP refs found; expected at least 1")
				}
				if field1Refs == 0 {
					t.Error("No #FIELD-1 refs found; expected at least 1")
				}
			},
		},
		{
			name: "BasicCapture_redefinedSubfield",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// #SUB-FIELD-1 is used in:
				// - WRITE #SUB-FIELD-1 #SUB-FIELD-2 (line ~36)
				// - CALLNAT 'SUB1' #REDEF-FIELD #COUNTER (line ~39, but this is #REDEF-FIELD, not #SUB-FIELD-1)
				// So we expect at least 1 ref to #SUB-FIELD-1

				subField1Refs := 0
				for _, ref := range refs {
					if ref.Name == "#SUB-FIELD-1" {
						subField1Refs++
					}
				}

				if subField1Refs < 1 {
					t.Errorf("Found %d #SUB-FIELD-1 refs, want >= 1", subField1Refs)
				}
			},
		},
		{
			name: "BasicCapture_arrayWithSubscript",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// #TABLE(#INDEX) on line ~33:
				// - The array reference #TABLE (subscript stripped)
				// - The index variable #INDEX (as its own separate ref)
				// Both should be captured.

				tableRefs := 0
				indexRefs := 0
				for _, ref := range refs {
					if ref.Name == "#TABLE" {
						tableRefs++
					}
					if ref.Name == "#INDEX" {
						indexRefs++
					}
				}

				if tableRefs < 1 {
					t.Errorf("Found %d #TABLE refs, want >= 1 (subscript stripped)", tableRefs)
				}
				if indexRefs < 1 {
					t.Errorf("Found %d #INDEX refs, want >= 1 (index var captured separately)", indexRefs)
				}
			},
		},
		{
			name: "CommentStringExclusion_stringAndCommentNotCaptured",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// The fixture uses #CUSTOMER-NAME in exactly two REAL statement
				// positions (MOVE #CUSTOMER-NAME TO #COUNTER, and
				// MOVE #TABLE(#INDEX) TO #CUSTOMER-NAME) plus once INSIDE a string
				// literal (MOVE '#CUSTOMER-NAME' TO #COUNTER). The string occurrence
				// must NOT be captured, so the count is exactly 2 — and the string
				// occurrence sits on the MOVE '...' line, which must be absent.
				var customerRefs []model.VariableRef
				for _, ref := range refs {
					if ref.Name == "#CUSTOMER-NAME" {
						customerRefs = append(customerRefs, ref)
					}
				}
				if len(customerRefs) != 2 {
					t.Fatalf("Found %d #CUSTOMER-NAME refs, want exactly 2 (the string-literal occurrence excluded)", len(customerRefs))
				}
				// Locate the MOVE '#CUSTOMER-NAME' line so we can prove no capture landed on it.
				stringLine := findLineContaining(t, "MOVE '#CUSTOMER-NAME'")
				for _, ref := range customerRefs {
					if ref.Range.Start.Line == stringLine {
						t.Errorf("Captured #CUSTOMER-NAME at line %d, which is inside a string literal", stringLine)
					}
				}

				// #FIELD appears ONLY inside the comment line
				// (* This is a comment with #FIELD inside it) and, as a distinct
				// name, nowhere else. It must yield ZERO refs. This simultaneously
				// proves comment exclusion AND exact-name matching: the real
				// #FIELD-EXT use-site must NOT be counted as a #FIELD match.
				for _, ref := range refs {
					if ref.Name == "#FIELD" {
						t.Errorf("Captured #FIELD at line %d; it appears only inside a comment (and #FIELD-EXT must not match as #FIELD)", ref.Range.Start.Line)
					}
				}
			},
		},
		{
			name: "SubstringDecoy_exactNameMatchingNotSubstring",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// #FIELD-EXT is a real, distinct use-site (WRITE #FIELD-EXT). It
				// must be captured exactly once, and must never be conflated with
				// the shorter, comment-only #FIELD name.
				fieldExtRefs := 0
				for _, ref := range refs {
					if ref.Name == "#FIELD-EXT" {
						fieldExtRefs++
					}
				}
				if fieldExtRefs != 1 {
					t.Errorf("Found %d #FIELD-EXT refs, want exactly 1 (the real WRITE use-site)", fieldExtRefs)
				}
				// Guard the decoy direction explicitly: no ref named #FIELD exists.
				for _, ref := range refs {
					if ref.Name == "#FIELD" {
						t.Error("A #FIELD ref exists; #FIELD-EXT must not be matched as the substring #FIELD")
					}
				}
			},
		},
		{
			name: "SystemVarExclusion_asteriskVarNotCaptured",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// Line ~41: MOVE *DATE TO #COUNTER
				// *DATE is a system variable and should NOT be captured.

				for _, ref := range refs {
					if ref.Name == "*DATE" {
						t.Error("Captured *DATE (system variable); should be excluded")
					}
				}

				// Verify #COUNTER on that line IS captured (it's not a system var)
				counterRefs := 0
				for _, ref := range refs {
					if ref.Name == "#COUNTER" {
						counterRefs++
					}
				}

				if counterRefs == 0 {
					t.Error("No #COUNTER refs found; expected at least 1 (from line ~41)")
				}
			},
		},
		{
			name: "DynamicVariableExclusion_ampersandVarNotCaptured",
			verify: func(t *testing.T, refs []model.VariableRef) {
				// The fixture has MOVE &DYNVAR TO #INDEX. &DYNVAR is a &-dynamic
				// (source-substitution) name and is a modeled gap per OQ-1 — it must
				// NOT be captured. The lexer preserves the leading & sigil, so the
				// excluded token is "&DYNVAR"; assert neither form leaks in.
				for _, ref := range refs {
					if ref.Name == "&DYNVAR" || ref.Name == "DYNVAR" {
						t.Errorf("Captured &-dynamic variable %q at line %d; it must be excluded (modeled gap)", ref.Name, ref.Range.Start.Line)
					}
				}
				// Any name beginning with the & sigil must be absent entirely.
				for _, ref := range refs {
					if len(ref.Name) > 0 && ref.Name[0] == '&' {
						t.Errorf("Captured &-prefixed ref %q; all &-dynamic names must be excluded", ref.Name)
					}
				}
				// Sanity: the real #INDEX on the same statement IS captured, proving
				// exclusion is scoped to the &-name, not the whole statement.
				indexRefs := 0
				for _, ref := range refs {
					if ref.Name == "#INDEX" {
						indexRefs++
					}
				}
				if indexRefs < 1 {
					t.Error("Expected #INDEX to be captured (the &-exclusion must not drop the surrounding statement)")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.verify(t, refs)
		})
	}
}

// TestExtractVariableRefs_FuzzNeverPanics verifies FR-43:
// ExtractVariableRefs never panics on arbitrary input and always returns a slice.
// (Placeholder; actual fuzz test in fuzz_test.go.)
func TestExtractVariableRefs_FuzzNeverPanics(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty_input",
			input: "",
		},
		{
			name:  "garbage_input",
			input: "!@#$%^&*()",
		},
		{
			name:  "massive_input",
			input: string(make([]byte, 1000000)), // 1MB of zeros
		},
		{
			name:  "utf8_multibyte",
			input: "MOVE #VAR€ TO #VAR₂\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ExtractVariableRefs panicked: %v", r)
				}
			}()

			refs := ExtractVariableRefs(tc.input)
			// Verify we always get a slice (never nil, may be empty)
			if refs == nil {
				t.Error("ExtractVariableRefs returned nil; want non-nil slice")
			}
		})
	}
}

// findLineContaining returns the 1-based line number of the first line in the
// 01-basic-captures fixture that contains substr. It is a test helper for
// asserting that no captured VariableRef lands on a specific source line
// (e.g., a string-literal line whose contents must be excluded).
func findLineContaining(t *testing.T, substr string) int {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "variablerefs", "01-basic-captures.NSP"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}
	for i, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, substr) {
			return i + 1
		}
	}
	t.Fatalf("fixture does not contain a line with %q", substr)
	return 0
}
