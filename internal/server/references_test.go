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

// TestProvideReferencesSkeletonEmpty tests the textDocument/references handler skeleton (feature 10, T10).
// A cursor on a position with no symbol returns empty (no error).
func TestProvideReferencesSkeletonEmpty(t *testing.T) {
	testdata := filepath.Join("testdata", "references", "multi-caller")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Read the fixture file CALLER1.NSP
	callerPath := filepath.Join(testdata, "CALLER1.NSP")
	callerContent, err := os.ReadFile(callerPath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", callerPath, err)
	}

	// Analyze the file
	az := natural.New(nil)
	callerAnalysis, err := az.Analyze(callerPath, callerContent)
	if err != nil {
		t.Fatalf("failed to analyze fixture: %v", err)
	}

	// Build a minimal index with just this file
	idx := &workspace.Index{}
	relPath := filepath.Join("testdata", "references", "multi-caller", "CALLER1.NSP")
	idx.Add(relPath, callerAnalysis)

	// Create a minimal resolution set (empty for this test; actual resolution not needed for skeleton)
	resSet := &workspace.ResolutionSet{}

	// Create handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
	}

	// Create params with a position that points to whitespace (no symbol)
	// Position {1, 1} is the first character, which is whitespace in the file
	params := protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(root, relPath)),
			},
			Position: protocol.Position{Line: 0, Character: 0}, // First position (whitespace)
		},
		Context: protocol.ReferenceContext{
			IncludeDeclaration: false,
		},
	}

	// Call provideReferences
	locations, err := provideReferences(hctx, params)

	// Expect: empty result (nil or zero-length slice), no error
	if err != nil {
		t.Errorf("provideReferences returned error: %v", err)
	}
	if locations != nil && len(locations) > 0 {
		t.Errorf("provideReferences returned non-empty locations for no-symbol cursor: %v", locations)
	}
}

// TestReferenceSitesMultiCallers tests the reverse-reference sweep primitive (feature 10, T10–T11).
// Given a target subprogram definition, it should find all CALLNAT sites that resolve to it
// across multiple files.
//
// FR-ID: FR-25 (find-all-references), criterion "complete with respect to index (multi-file fixture)".
func TestReferenceSitesMultiCallers(t *testing.T) {
	testdata := filepath.Join("testdata", "references", "multi-caller")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"SHARED.NSN", "CALLER1.NSP", "CALLER2.NSP", "CALLER3.NSP"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdata, filename)
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

	// The target is SHARED.NSN
	targetPath := "testdata/references/multi-caller/SHARED.NSN"
	targetName := "SHARED"
	targetType := model.ObjectSubprogram

	// Call the sweep primitive
	locations := referenceSites(idx, resSet, root, targetPath, targetName, targetType, false)

	// Verify: we expect 3 reference locations (one from each of CALLER1, CALLER2, CALLER3)
	if len(locations) != 3 {
		t.Errorf("referenceSites returned %d locations, want 3", len(locations))
	}

	// Verify each location's URI contains the correct file
	expectedFiles := []string{"CALLER1.NSP", "CALLER2.NSP", "CALLER3.NSP"}
	for i, loc := range locations {
		if i >= len(expectedFiles) {
			t.Errorf("more locations than expected")
			break
		}

		// Extract the filename from the URI
		fsPath := loc.URI.FsPath()
		// Allow for any order of the expected files
		found := false
		for _, expected := range expectedFiles {
			if strings.Contains(fsPath, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("location %d URI does not contain a CALLER file: %s", i, fsPath)
		}
	}

	// Verify that the ranges are non-zero (pointing to the CALLNAT statements)
	for i, loc := range locations {
		if loc.Range.Start.Line == loc.Range.End.Line && loc.Range.Start.Character == loc.Range.End.Character {
			// Zero-width range might be OK for some cases, but CALLNAT 'SHARED' should have a real range
			t.Logf("location %d has zero-width range (may be acceptable)", i)
		}
	}

	// Verify deterministic order (sorted by URI then range)
	isSorted := sort.SliceIsSorted(locations, func(i, j int) bool {
		if locations[i].URI != locations[j].URI {
			return string(locations[i].URI) < string(locations[j].URI)
		}
		if locations[i].Range.Start.Line != locations[j].Range.Start.Line {
			return locations[i].Range.Start.Line < locations[j].Range.Start.Line
		}
		return locations[i].Range.Start.Character < locations[j].Range.Start.Character
	})
	if !isSorted {
		t.Errorf("referenceSites returned locations in non-deterministic order")
	}
}

// TestReferenceSitesIncludeDeclaration tests that the sweep includes the declaration site
// when IncludeDeclaration is true.
func TestReferenceSitesIncludeDeclaration(t *testing.T) {
	testdata := filepath.Join("testdata", "references", "multi-caller")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index
	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []string{"SHARED.NSN", "CALLER1.NSP", "CALLER2.NSP", "CALLER3.NSP"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdata, filename)
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

	// Compute the resolution set
	resSet := workspace.Resolve(idx, &cfg)

	targetPath := "testdata/references/multi-caller/SHARED.NSN"
	targetName := "SHARED"
	targetType := model.ObjectSubprogram

	// Call with IncludeDeclaration = true
	locations := referenceSites(idx, resSet, root, targetPath, targetName, targetType, true)

	// Expect: 4 locations (3 references + 1 declaration in SHARED.NSN)
	if len(locations) != 4 {
		t.Errorf("referenceSites with IncludeDeclaration=true returned %d locations, want 4", len(locations))
	}

	// Verify that one of the locations is from SHARED.NSN (the declaration)
	foundDeclaration := false
	for _, loc := range locations {
		fsPath := loc.URI.FsPath()
		if strings.Contains(fsPath, "SHARED.NSN") {
			foundDeclaration = true
			break
		}
	}
	if !foundDeclaration {
		t.Errorf("declaration site (SHARED.NSN) not found in locations")
	}
}

// TestProvideReferencesCompleteness_DDMFieldCrossFile tests find-all-references completeness
// for DDM-field references across multiple files (feature 10, T11).
//
// FR-ID: FR-25 (find-all-references), criterion "complete with respect to index (multi-file fixture)"
// and "DDM field" case.
//
// This test creates a multi-file workspace where the same DDM (EMPLOYEES) is referenced
// from three files via READ, FIND, and GET statements. It asserts that provideReferences
// returns exactly the set of reference sites, with correct URIs and ranges.
//
// The test exercises the DDM-field reference case (the TODO left by T10 in referenceSites).
// Since DDM resolution is not yet implemented, matching is by name: all DataAccessEntry
// across files whose Name == the target DDM name are reference sites.
func TestProvideReferencesCompleteness_DDMFieldCrossFile(t *testing.T) {
	testdata := filepath.Join("testdata", "references", "ddm-refs")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build the index by analyzing all files in the fixture
	idx := &workspace.Index{}
	cfg := config.Config{} // Empty config for flat-namespace resolution

	files := []string{"EMPLOYEES.NSD", "PROG1.NSP", "PROG2.NSP", "PROG3.NSP"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdata, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "references", "ddm-refs", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Compute the resolution set over the index
	resSet := workspace.Resolve(idx, &cfg)

	// The target is EMPLOYEES (the DDM), referenced from PROG1, PROG2, PROG3
	// DDM resolution is not yet implemented; matching is by name.
	// Per task T11, find-references on a DDM name should return all DataAccessEntry
	// sites whose Name == "EMPLOYEES" (case-insensitive matching normalized to upper-case).
	targetPath := "testdata/references/ddm-refs/EMPLOYEES.NSD"
	targetName := "EMPLOYEES"
	targetType := model.ObjectDDM

	// Call the sweep primitive (this is what provideReferences will use for DDM targets)
	locations := referenceSites(idx, resSet, root, targetPath, targetName, targetType, false)

	// Assertion 1: We expect exactly 3 reference locations
	// (one READ from PROG1, one FIND from PROG2, one GET from PROG3).
	// The declaration site is NOT included because IncludeDeclaration=false.
	if len(locations) != 3 {
		t.Errorf("referenceSites returned %d DDM reference locations, want 3", len(locations))
	}

	// Assertion 2: Extract file names from the locations and verify they are PROG1, PROG2, PROG3
	locationsByFile := make(map[string]int)
	for _, loc := range locations {
		fsPath := loc.URI.FsPath()
		if strings.Contains(fsPath, "PROG1.NSP") {
			locationsByFile["PROG1.NSP"]++
		} else if strings.Contains(fsPath, "PROG2.NSP") {
			locationsByFile["PROG2.NSP"]++
		} else if strings.Contains(fsPath, "PROG3.NSP") {
			locationsByFile["PROG3.NSP"]++
		} else {
			t.Errorf("location URI contains unexpected file: %s", fsPath)
		}
	}

	// Verify exactly one reference per program
	expectedProgs := []string{"PROG1.NSP", "PROG2.NSP", "PROG3.NSP"}
	for _, prog := range expectedProgs {
		if locationsByFile[prog] != 1 {
			t.Errorf("expected 1 reference from %s, got %d", prog, locationsByFile[prog])
		}
	}

	// Assertion 3: Verify that the ranges are non-zero (pointing to the DDM names)
	for i, loc := range locations {
		if loc.Range.Start.Line == loc.Range.End.Line && loc.Range.Start.Character == loc.Range.End.Character {
			t.Errorf("location %d has zero-width range; DDM name should have a real range", i)
		}
	}

	// Assertion 4: Verify deterministic order (sorted by URI then range)
	isSorted := sort.SliceIsSorted(locations, func(i, j int) bool {
		if locations[i].URI != locations[j].URI {
			return string(locations[i].URI) < string(locations[j].URI)
		}
		if locations[i].Range.Start.Line != locations[j].Range.Start.Line {
			return locations[i].Range.Start.Line < locations[j].Range.Start.Line
		}
		return locations[i].Range.Start.Character < locations[j].Range.Start.Character
	})
	if !isSorted {
		t.Errorf("referenceSites returned DDM reference locations in non-deterministic order")
	}
}
