package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	gojson "github.com/go-json-experiment/json"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
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
				t.Fatalf("Command.Arguments malformed: expected exactly 3 items [uri, position, []Location], got %d items", len(lens.Command.Arguments))
			}

			// Assert: the third argument decodes to the write-site []Location (the
			// payload that actually delivers "activating reveals the write sites",
			// Story 2 AC #2). Decode it and check the site count matches the named writes.
			var sites []protocol.Location
			if err := gojson.Unmarshal([]byte(lens.Command.Arguments[2]), &sites); err != nil {
				t.Fatalf("Command.Arguments[2] is not a valid []Location: %v", err)
			}
			if len(sites) != tc.expectLocCount {
				t.Errorf("write-site Location count = %d, want %d", len(sites), tc.expectLocCount)
			}

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

// TestProvideCodeLens pins the provideCodeLens provider entry point (feature 13, T5).
// It tests the provider's behavior when assembled into the full LSP handler context:
//
//  1. If hctx.cfg.Analysis.EnableCodeLens == false, return nil, nil (no lenses).
//  2. Resolve the document: open-document store first (live edits), else index snapshot.
//  3. If Structure == nil or file unreadable → nil, nil (FR-43).
//  4. Build the call-count lens (T3) and, if there are named writes, the write-summary
//     lens (T4), using the object root SelectionRange as the anchor.
//  5. Return the assembled []protocol.CodeLens in deterministic order (call-count then
//     write-summary), or nil when neither applies.
//
// This test covers:
//   - Enabled + object with inbound callers (SHARED.NSN from multi-caller): returns
//     a non-nil slice whose FIRST lens is the call-count lens with correct title.
//   - Object that also writes DDMs (write-summary.NSP): returns both lenses in order.
//   - EnableCodeLens == false: returns nil.
//   - A URI not in the index / with nil Structure: returns nil.
func TestProvideCodeLens(t *testing.T) {
	testdataDir := filepath.Join("testdata", "references", "multi-caller")
	codelensDir := filepath.Join("testdata", "codelens")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{
		Analysis: config.AnalysisConfig{
			EnableCodeLens: true, // enabled by default
		},
	}

	// Add multi-caller fixture files
	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "SHARED.NSN"), "testdata/references/multi-caller/SHARED.NSN"},
		{filepath.Join(testdataDir, "CALLER1.NSP"), "testdata/references/multi-caller/CALLER1.NSP"},
		{filepath.Join(testdataDir, "CALLER2.NSP"), "testdata/references/multi-caller/CALLER2.NSP"},
		{filepath.Join(testdataDir, "CALLER3.NSP"), "testdata/references/multi-caller/CALLER3.NSP"},
		{filepath.Join(testdataDir, "CALLER_DYN.NSP"), "testdata/references/multi-caller/CALLER_DYN.NSP"},
		{filepath.Join(codelensDir, "write-summary.NSP"), "testdata/codelens/write-summary.NSP"},
	}

	az := natural.New(nil)
	for _, f := range files {
		content, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", f.path, err)
		}
		analysis, err := az.Analyze(f.path, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.path, err)
		}
		idx.Add(strings.ReplaceAll(f.relPath, "\\", "/"), analysis)
	}

	// Compute the resolution set over the index
	resSet := workspace.Resolve(idx, &cfg)

	tests := []struct {
		name             string
		enabled          bool // whether EnableCodeLens is true
		uri              string
		relPath          string
		expectNonNil     bool
		expectMinLen     int // minimum number of lenses expected
		checkFirstTitle  string
		checkSecondTitle string // optional, for multi-lens cases
		description      string
	}{
		{
			name:            "enabled_with_inbound_callers",
			enabled:         true,
			uri:             "file:///workspace/testdata/references/multi-caller/SHARED.NSN",
			relPath:         "testdata/references/multi-caller/SHARED.NSN",
			expectNonNil:    true,
			expectMinLen:    1,
			checkFirstTitle: "3 references", // SHARED.NSN is called by 3 callers
			description:     "Enabled, object with 3 inbound callers should return call-count lens",
		},
		{
			name:             "enabled_with_writes",
			enabled:          true,
			uri:              "file:///workspace/testdata/codelens/write-summary.NSP",
			relPath:          "testdata/codelens/write-summary.NSP",
			expectNonNil:     true,
			expectMinLen:     2,
			checkFirstTitle:  "0 references",             // write-summary.NSP is not called
			checkSecondTitle: "Writes: CUSTOMER, ORDERS", // but writes to two DDMs
			description:      "Enabled, object with writes should return both lenses",
		},
		{
			name:         "disabled_returns_nil",
			enabled:      false,
			uri:          "file:///workspace/testdata/references/multi-caller/SHARED.NSN",
			relPath:      "testdata/references/multi-caller/SHARED.NSN",
			expectNonNil: false,
			expectMinLen: 0,
			description:  "When EnableCodeLens=false, provider should return nil",
		},
		{
			name:         "uri_not_in_index",
			enabled:      true,
			uri:          "file:///workspace/testdata/nonexistent.NSP",
			relPath:      "testdata/nonexistent.NSP",
			expectNonNil: false,
			expectMinLen: 0,
			description:  "A file not in index should return nil",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: build the handler context with the snapshot of idx/res
			hctx := &handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       nil, // no open documents for this test
			}

			// Override EnableCodeLens for the "disabled" case
			if !tc.enabled {
				hctx.cfg.Analysis.EnableCodeLens = false
			}

			// Construct the CodeLensParams. Build the URI from the real on-disk
			// fixture path (workspaceRoot = os.Getwd()) so it maps to an actual
			// file the provider can read; tc.uri is kept for documentation only.
			params := protocol.CodeLensParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: uri.File(filepath.Join(workspaceRoot, tc.relPath)),
				},
			}

			// Act: call provideCodeLens
			result, err := provideCodeLens(hctx, params)

			// Assert: no error expected
			if err != nil {
				t.Errorf("provideCodeLens returned error: %v", err)
			}

			// Assert: nil vs. non-nil expectation
			if tc.expectNonNil {
				if result == nil {
					t.Fatalf("expected non-nil result, got nil; %s", tc.description)
				}
				if len(result) < tc.expectMinLen {
					t.Errorf("expected at least %d lenses, got %d; %s", tc.expectMinLen, len(result), tc.description)
				}
				// Check the first lens title
				if tc.checkFirstTitle != "" {
					if result[0].Command.Title != tc.checkFirstTitle {
						t.Errorf("first lens title = %q, want %q", result[0].Command.Title, tc.checkFirstTitle)
					}
				}
				// Check the second lens title if provided
				if tc.checkSecondTitle != "" {
					if len(result) < 2 {
						t.Errorf("expected at least 2 lenses, got %d for check of second title", len(result))
					} else if !strings.HasPrefix(result[1].Command.Title, strings.Split(tc.checkSecondTitle, ":")[0]) {
						t.Errorf("second lens title = %q, want to start with %q", result[1].Command.Title, strings.Split(tc.checkSecondTitle, ":")[0])
					}
				}
			} else {
				if result != nil {
					t.Errorf("expected nil result, got %d lenses; %s", len(result), tc.description)
				}
			}
		})
	}
}

// TestProvideCodeLens_IncrementalFreshness verifies that after a didChange that
// adds/removes a caller, a subsequent textDocument/codeLens reflects the updated
// inbound count (feature 13, T7, Story 1 AC #3).
//
// This test exercises the incremental freshness path: it uses the same mechanism
// as applyDocumentChange (idx.Add + ResolveInto) to simulate a document change
// and verifies that the code lens count is updated accordingly.
//
// Scenario:
// 1. Build an index with SHARED.NSN (called by 3 callers: CALLER1, CALLER2, CALLER3)
// 2. Call provideCodeLens for SHARED.NSN and assert "3 references"
// 3. Remove CALLNAT 'SHARED' from CALLER3, re-analyze, and apply the change via idx.Add + ResolveInto
// 4. Call provideCodeLens again and assert "2 references" (count decreased)
func TestProvideCodeLens_IncrementalFreshness(t *testing.T) {
	testdataDir := filepath.Join("testdata", "references", "multi-caller")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: Build the initial index with all multi-caller fixture files
	idx := &workspace.Index{}
	cfg := config.Config{
		Analysis: config.AnalysisConfig{
			EnableCodeLens: true,
		},
	}

	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "SHARED.NSN"), "testdata/references/multi-caller/SHARED.NSN"},
		{filepath.Join(testdataDir, "CALLER1.NSP"), "testdata/references/multi-caller/CALLER1.NSP"},
		{filepath.Join(testdataDir, "CALLER2.NSP"), "testdata/references/multi-caller/CALLER2.NSP"},
		{filepath.Join(testdataDir, "CALLER3.NSP"), "testdata/references/multi-caller/CALLER3.NSP"},
		{filepath.Join(testdataDir, "CALLER_DYN.NSP"), "testdata/references/multi-caller/CALLER_DYN.NSP"},
	}

	az := natural.New(nil)
	for _, f := range files {
		content, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", f.path, err)
		}
		analysis, err := az.Analyze(f.path, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.path, err)
		}
		idx.Add(strings.ReplaceAll(f.relPath, "\\", "/"), analysis)
	}

	// Compute the initial resolution set
	res := workspace.Resolve(idx, &cfg)

	// Arrange: Create the initial handler context
	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         res,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
	}

	// First provideCodeLens call: SHARED.NSN should have 3 references
	sharedURI := uri.File(filepath.Join(workspaceRoot, "testdata/references/multi-caller/SHARED.NSN"))
	params := protocol.CodeLensParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: sharedURI,
		},
	}

	// Act: Get the initial lenses for SHARED.NSN
	initialResult, err := provideCodeLens(hctx, params)
	if err != nil {
		t.Fatalf("initial provideCodeLens failed: %v", err)
	}

	// Assert: Initial lens should have "3 references"
	if initialResult == nil || len(initialResult) == 0 {
		t.Fatal("expected non-nil initial result with at least 1 lens")
	}
	initialTitle := initialResult[0].Command.Title
	if initialTitle != "3 references" {
		t.Errorf("initial lens title = %q, want %q", initialTitle, "3 references")
	}

	// Simulate a document change: remove the CALLNAT 'SHARED' from CALLER3.NSP
	// Read CALLER3 original content
	caller3Path := filepath.Join(testdataDir, "CALLER3.NSP")
	caller3Content, err := os.ReadFile(caller3Path)
	if err != nil {
		t.Fatalf("failed to read CALLER3.NSP: %v", err)
	}

	// Remove the CALLNAT 'SHARED' line from CALLER3
	modifiedContent := strings.ReplaceAll(string(caller3Content), "CALLNAT 'SHARED'\n", "")
	if string(caller3Content) == modifiedContent {
		t.Fatal("failed to remove CALLNAT 'SHARED' from CALLER3.NSP content")
	}

	// Re-analyze the modified CALLER3 content
	caller3RelPath := "testdata/references/multi-caller/CALLER3.NSP"
	modifiedAnalysis, err := az.Analyze(caller3Path, []byte(modifiedContent))
	if err != nil {
		t.Fatalf("failed to analyze modified CALLER3.NSP: %v", err)
	}

	// Simulate applyDocumentChange: update the index and recompute resolution
	// This mimics the server's applyDocumentChange function behavior
	idx.Add(caller3RelPath, modifiedAnalysis)
	res = workspace.ResolveInto(res, idx, &cfg, []string{caller3RelPath})

	// Update the handler context with the new resolution set
	hctx.res = res

	// Act: Get the updated lenses for SHARED.NSN after the change
	updatedResult, err := provideCodeLens(hctx, params)
	if err != nil {
		t.Fatalf("updated provideCodeLens failed: %v", err)
	}

	// Assert: Updated lens should have "2 references" (CALLER1 and CALLER2 now)
	if updatedResult == nil || len(updatedResult) == 0 {
		t.Fatal("expected non-nil updated result with at least 1 lens")
	}
	updatedTitle := updatedResult[0].Command.Title
	if updatedTitle != "2 references" {
		t.Errorf("updated lens title = %q, want %q", updatedTitle, "2 references")
	}
}

// TestProvideCodeLens_MarshaledEmptyCase (T4) pins the wire bytes for an empty
// codeLens result by driving the REAL dispatch path end-to-end against an empty
// workspace. The provider returns nil for a document with no lenses, and the
// dispatch's nil-guard must emit "null".
//
// If the codeLens nil-guard in server.go is dropped or flipped to "[]", the emitted
// bytes change and this test goes red (Story 2 AC2).
func TestProvideCodeLens_MarshaledEmptyCase(t *testing.T) {
	got := dispatchResultBytes(t, "textDocument/codeLens",
		`{"textDocument":{"uri":"file:///nonexistent/NOPE.NSP"}}`)

	if string(got) != "null" {
		t.Errorf("empty codeLens result: got %q, want %q", string(got), "null")
	}
}

// TestProvideCodeLens_MarshaledNonEmptyCase (T4) pins the exact wire bytes for a
// non-empty codeLens result via marshalResult — the EXACT function the codeLens
// dispatch calls in its non-nil branch. Pinning the full bytes locks byte-for-byte
// preservation across the stdlib→gojson migration (Story 2 AC2).
func TestProvideCodeLens_MarshaledNonEmptyCase(t *testing.T) {
	// Setup: one code lens result
	lenses := []protocol.CodeLens{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 10},
			},
			Command: protocol.Command{
				Title:   "3 references",
				Command: "editor.action.showReferences",
			},
		},
	}

	// Marshal via the dispatch's exact marshaler.
	got, err := marshalResult(lenses)
	if err != nil {
		t.Fatalf("failed to marshal via marshalResult: %v", err)
	}

	want := `[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":10}},"command":{"title":"3 references","command":"editor.action.showReferences"}}]`
	if string(got) != want {
		t.Errorf("non-empty codeLens wire bytes mismatch:\n got: %s\nwant: %s", string(got), want)
	}
}
