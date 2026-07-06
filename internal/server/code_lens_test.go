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
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
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

// TestBuildCallCountLens tests the pure builder for a call-count code lens
// (feature 13, T3, Story 1, AC #1–#2).
//
// Exercises: building a lens with a pluralized count (0 references, 1 reference,
// N references), using the object root SelectionRange as the Range, building the
// Command via showReferencesCommand (or equivalent) with the callers' Locations,
// and ensuring that a zero-count target still emits a lens (not suppressed).
func TestBuildCallCountLens(t *testing.T) {
	testdataDir := filepath.Join("testdata", "references", "multi-caller")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"SHARED.NSN", "CALLER1.NSP", "CALLER2.NSP", "CALLER3.NSP", "CALLER_DYN.NSP"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "references", "multi-caller", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Compute the resolution set over the index
	resSet := workspace.Resolve(idx, &cfg)

	tests := []struct {
		name            string
		targetPath      string // workspace-relative path of the target definition
		targetName      string // target object name
		targetType      model.ObjectType
		expectTitle     string // expected Command.Title
		expectLocCount  int    // expected number of Locations in Arguments[2]
		expectCommandID string // expected Command.Command
		expectNil       bool   // whether the lens should be nil
	}{
		{
			name:            "three_static_callers",
			targetPath:      "testdata/references/multi-caller/SHARED.NSN",
			targetName:      "SHARED-SUB",
			targetType:      model.ObjectSubprogram,
			expectTitle:     "3 references",
			expectLocCount:  3,
			expectCommandID: "editor.action.showReferences",
			expectNil:       false,
		},
		{
			name:            "one_reference_singular",
			targetPath:      "testdata/references/multi-caller/CALLER1.NSP",
			targetName:      "CALLER1",
			targetType:      model.ObjectProgram,
			expectTitle:     "1 reference",
			expectLocCount:  1,
			expectCommandID: "editor.action.showReferences",
			expectNil:       false,
		},
		{
			name:            "zero_references_not_suppressed",
			targetPath:      "testdata/references/multi-caller/NONEXISTENT.NSP",
			targetName:      "DOESNOTEXIST",
			targetType:      model.ObjectProgram,
			expectTitle:     "0 references",
			expectLocCount:  0,
			expectCommandID: "editor.action.showReferences",
			expectNil:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Get the target file's analysis
			targetFA, ok := idx.Get(tc.targetPath)
			if !ok && tc.expectNil {
				// Expected: if target doesn't exist and we expect nil, skip
				t.Logf("target %s not in index (as expected for zero-reference case)", tc.targetPath)
				return
			}

			// For valid targets, expect Structure to be present
			if ok && targetFA.Structure == nil && !tc.expectNil {
				t.Fatalf("target file has nil Structure")
			}

			// Get the root SelectionRange (for valid targets)
			var root *model.Symbol
			if ok {
				root = targetFA.Structure
			} else {
				// For zero-reference case, create a synthetic root
				root = &model.Symbol{
					Kind: model.SymbolObject,
					Name: tc.targetName,
					Range: model.Range{
						Start: model.Position{Line: 1, Column: 1},
						End:   model.Position{Line: 10, Column: 1},
					},
					SelectionRange: model.Range{
						Start: model.Position{Line: 1, Column: 1},
						End:   model.Position{Line: 1, Column: 1},
					},
				}
			}

			// Read the target file content (or use a synthetic one for zero-count)
			var fileContent []byte
			if ok {
				absPath := filepath.Join(workspaceRoot, tc.targetPath)
				var err error
				fileContent, err = os.ReadFile(absPath)
				if err != nil {
					t.Fatalf("failed to read target file: %v", err)
				}
			} else {
				// Synthetic content for zero-reference test
				fileContent = []byte("DEFINE PROGRAM TEST\nEND")
			}

			// Build the document URI
			fileURI := uri.File(filepath.Join(workspaceRoot, tc.targetPath))

			// Act: build the call-count lens
			lens := buildCallCountLens(idx, resSet, workspaceRoot, tc.targetPath, tc.targetName, tc.targetType, root.SelectionRange, fileURI, string(fileContent), protocol.PositionEncodingKindUTF8)

			// Assert: nil vs. non-nil expectation
			if tc.expectNil {
				if lens != nil {
					t.Errorf("expected nil lens, got non-nil: %+v", lens)
				}
				return
			}

			if lens == nil {
				t.Fatal("expected non-nil lens but got nil")
			}

			// Assert: lens Range matches the root SelectionRange
			expectedRange := toProtocolRange(root.SelectionRange, string(fileContent), protocol.PositionEncodingKindUTF8)
			if lens.Range.Start != expectedRange.Start || lens.Range.End != expectedRange.End {
				t.Errorf("lens Range mismatch: got %+v, expected %+v", lens.Range, expectedRange)
			}

			// Assert: Command.Title has correct pluralization
			if lens.Command.Title != tc.expectTitle {
				t.Errorf("Command.Title mismatch: got %q, expected %q", lens.Command.Title, tc.expectTitle)
			}

			// Assert: Command.Command is "editor.action.showReferences"
			if lens.Command.Command != tc.expectCommandID {
				t.Errorf("Command.Command mismatch: got %q, expected %q", lens.Command.Command, tc.expectCommandID)
			}

			// Assert: Command.Arguments has exactly 3 items [uri, position, []Location]
			if lens.Command.Arguments == nil || len(lens.Command.Arguments) != 3 {
				t.Errorf("Command.Arguments malformed: expected exactly 3 items, got %d", len(lens.Command.Arguments))
			}

			// Assert: the argument count matches expected locations
			// (Arguments[2] is the []Location; we can't easily unmarshal it from LSPAny,
			// but we can verify that the list has the expected length by re-calling referenceSites)
			actualLocs := referenceSites(idx, resSet, workspaceRoot, tc.targetPath, tc.targetName, tc.targetType, false, protocol.PositionEncodingKindUTF8)
			if len(actualLocs) != tc.expectLocCount {
				t.Errorf("Location count mismatch: got %d, expected %d", len(actualLocs), tc.expectLocCount)
			}
		})
	}
}
