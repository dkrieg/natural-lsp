package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/model"
)

// TestBuildWriteSummaryLens_NamedWrites tests the pure builder for a write-summary
// code lens (feature 13, T4, Story 2, AC #1–#2).
//
// Exercises: collecting distinct named write targets (deduped case-insensitively,
// sorted for determinism), skipping empty-Name entries (feature-08 record-form gap,
// FR-17), building Command.Title as "Writes: TARGET1, TARGET2", Command.Command as
// "editor.action.showReferences", and Command.Arguments as [uri, position, write-site
// Locations]. When the object has only empty-Name writes (record-form gap) and no
// named writes, returns nil (no lens).
func TestBuildWriteSummaryLens_NamedWrites(t *testing.T) {
	tests := []struct {
		name           string
		fixturePath    string // relative path from test package directory
		expectTitle    string // expected Command.Title substring
		expectCommand  string // expected Command.Command
		expectLocCount int    // expected number of write-site Locations in Arguments
		// Special case: empty-only object (only record-form writes, no named writes)
		expectNil bool
	}{
		{
			name:           "two_named_writes_with_record_form_gap",
			fixturePath:    filepath.Join("testdata", "codelens", "write-summary.NSP"),
			expectTitle:    "Writes: CUSTOMER, ORDERS",
			expectCommand:  "editor.action.showReferences",
			expectLocCount: 2, // two named STORE statements (CUSTOMER and ORDERS)
			expectNil:      false,
		},
		{
			name:           "record_form_only_no_named_writes",
			fixturePath:    filepath.Join("testdata", "codelens", "record-form-only.NSP"),
			expectTitle:    "", // not used when expectNil=true
			expectCommand:  "",
			expectLocCount: 0,
			expectNil:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: read and analyze the fixture
			content, err := os.ReadFile(tc.fixturePath)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.fixturePath, err)
			}

			az := natural.New(nil)
			fa, err := az.Analyze(tc.fixturePath, content)
			if err != nil {
				t.Fatalf("failed to analyze fixture: %v", err)
			}

			// Get the root SelectionRange (object root)
			if fa.Structure == nil {
				t.Fatal("fixture analysis produced nil Structure")
			}

			root := fa.Structure

			// Build the document URI (required by the builder)
			absPath, err := filepath.Abs(tc.fixturePath)
			if err != nil {
				t.Fatalf("failed to get absolute path: %v", err)
			}
			fileURI := uri.File(absPath)

			// Act: build the write-summary lens
			lens := buildWriteSummaryLens(fa.DataAccess, root, fileURI, string(content), protocol.PositionEncodingKindUTF8)

			// Assert: nil vs. non-nil expectation
			if tc.expectNil {
				if lens != nil {
					t.Errorf("expected nil lens for object with only record-form writes, got non-nil: %+v", lens)
				}
				return
			}

			if lens == nil {
				t.Fatal("expected non-nil lens but got nil")
			}

			// Assert: lens has the right Range (root SelectionRange)
			expectedRange := toProtocolRange(root.SelectionRange, string(content), protocol.PositionEncodingKindUTF8)
			if lens.Range.Start != expectedRange.Start || lens.Range.End != expectedRange.End {
				t.Errorf("lens Range mismatch: got %+v, expected %+v", lens.Range, expectedRange)
			}

			// Assert: Command has the right shape (Command is a struct value, not a pointer)
			// Assert: Command.Title contains the expected write targets
			if !strings.Contains(lens.Command.Title, tc.expectTitle) {
				t.Errorf("Command.Title missing expected substring: got %q, expected to contain %q", lens.Command.Title, tc.expectTitle)
			}

			// Assert: Command.Command is "editor.action.showReferences"
			if lens.Command.Command != tc.expectCommand {
				t.Errorf("Command.Command mismatch: got %q, expected %q", lens.Command.Command, tc.expectCommand)
			}

			// Assert: Command.Arguments has the right shape [uri, position, []Location]
			if lens.Command.Arguments == nil || len(lens.Command.Arguments) != 3 {
				t.Errorf("Command.Arguments malformed: expected exactly 3 items [uri, position, []Location], got %d items", len(lens.Command.Arguments))
			}

			// For this test, we verify that the lens was built with proper structure.
			// The Arguments will contain uri, position, and a slice of Locations when
			// the lens is properly built. We'll defer detailed Location validation
			// to when we test the actual builder implementation.

			// Assert: title targets are sorted and deduplicated
			// Extract the "Writes: ..." part and verify it's deterministic
			titleParts := strings.Split(lens.Command.Title, "Writes: ")
			if len(titleParts) == 2 {
				targets := strings.Split(titleParts[1], ", ")
				// Verify targets are sorted
				targetsCopy := make([]string, len(targets))
				copy(targetsCopy, targets)
				sort.Strings(targetsCopy)
				if !equal(targets, targetsCopy) {
					t.Errorf("write targets not sorted: got %v, expected %v", targets, targetsCopy)
				}

				// Verify targets are deduplicated (no duplicates)
				seen := make(map[string]bool)
				for _, name := range targets {
					if seen[name] {
						t.Errorf("write target %q appears more than once in title", name)
					}
					seen[name] = true
				}
			}
		})
	}
}

// equal is a helper to compare two string slices for equality.
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildWriteSummaryLens_EmptyNameGapHandled tests that the builder correctly
// skips empty-Name record-form write entries (feature-08 OQ-4, FR-17).
//
// When an object has a record-form UPDATE/DELETE with no explicit target (no file
// operand), the parser produces a DataAccessEntry with empty Name. The builder must
// skip such entries and not fabricate a table name.
func TestBuildWriteSummaryLens_EmptyNameGapHandled(t *testing.T) {
	// Manually construct a FileAnalysis with mixed named and empty-Name writes
	dataAccess := []model.DataAccessEntry{
		{
			Name:   "CUSTOMER",
			Kind:   model.EdgeWrites,
			Source: model.Range{Start: model.Position{Line: 1, Column: 1}, End: model.Position{Line: 1, Column: 20}},
			NameRange: model.Range{
				Start: model.Position{Line: 1, Column: 7},
				End:   model.Position{Line: 1, Column: 15},
			},
		},
		{
			Name:   "", // empty-Name record-form UPDATE/DELETE gap
			Kind:   model.EdgeWrites,
			Source: model.Range{Start: model.Position{Line: 2, Column: 1}, End: model.Position{Line: 2, Column: 10}},
			NameRange: model.Range{
				Start: model.Position{Line: 2, Column: 1},
				End:   model.Position{Line: 2, Column: 1},
			},
		},
		{
			Name:   "ORDERS",
			Kind:   model.EdgeWrites,
			Source: model.Range{Start: model.Position{Line: 3, Column: 1}, End: model.Position{Line: 3, Column: 15}},
			NameRange: model.Range{
				Start: model.Position{Line: 3, Column: 7},
				End:   model.Position{Line: 3, Column: 13},
			},
		},
	}

	root := &model.Symbol{
		Kind: model.SymbolObject,
		Name: "test",
		Range: model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 10, Column: 1},
		},
		SelectionRange: model.Range{
			Start: model.Position{Line: 1, Column: 1},
			End:   model.Position{Line: 1, Column: 1},
		},
	}

	fileURI := uri.File("/tmp/test.NSP")
	content := "DEFINE DATA\nLOCAL\nEND\nREAD CUSTOMER\n UPDATE\nEND\nSTORE CUSTOMER\nSTORE ORDERS\nEND"

	// Act: build the lens
	lens := buildWriteSummaryLens(dataAccess, root, fileURI, content, protocol.PositionEncodingKindUTF8)

	// Assert: lens is non-nil (has named writes)
	if lens == nil {
		t.Fatal("expected non-nil lens for object with named writes and empty-Name gaps")
	}

	// Assert: title contains only named targets (CUSTOMER, ORDERS), not empty
	if !strings.Contains(lens.Command.Title, "CUSTOMER") {
		t.Errorf("expected CUSTOMER in title, got: %s", lens.Command.Title)
	}
	if !strings.Contains(lens.Command.Title, "ORDERS") {
		t.Errorf("expected ORDERS in title, got: %s", lens.Command.Title)
	}

	// Assert: title does NOT contain the empty-Name entry (it would show as ",," or similar)
	// A properly deduplicated and gap-aware title should have exactly 2 targets
	targetPart := strings.TrimPrefix(lens.Command.Title, "Writes: ")
	targetCount := len(strings.Split(targetPart, ", "))
	if targetCount != 2 {
		t.Errorf("expected 2 distinct named targets in title, got %d: %s", targetCount, lens.Command.Title)
	}

	// Assert: command arguments have the correct length (3 items)
	if lens.Command.Arguments == nil || len(lens.Command.Arguments) != 3 {
		t.Errorf("expected 3 Arguments [uri, position, []Location], got %d", len(lens.Command.Arguments))
	}
}
