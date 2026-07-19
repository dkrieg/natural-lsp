package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
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

// TestProvideCompletion_ModuleContextCallnat tests module-name completion for CALLNAT
// (feature 16, T4, Story 1, AC #1 and AC #4).
//
// Exercises:
// - Completion for CALLNAT context expects ObjectSubprogram targets
// - Offers matching subprograms by name prefix
// - Completion items include Label, Kind (Module), and Detail (object-type label)
// - Type filtering: excludes programs and copycodes from CALLNAT context
//
// FR-24/FR-47: module-name completion from workspace index.
func TestProvideCompletion_ModuleContextCallnat(t *testing.T) {
	testdataDir := "testdata/completion/module"
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build the index by analyzing all files in the module fixture
	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "CALLER.NSP"), "testdata/completion/module/CALLER.NSP"},
		{filepath.Join(testdataDir, "MYSUB.NSN"), "testdata/completion/module/MYSUB.NSN"},
		{filepath.Join(testdataDir, "MYPROG.NSP"), "testdata/completion/module/MYPROG.NSP"},
		{filepath.Join(testdataDir, "SHARED.NSC"), "testdata/completion/module/SHARED.NSC"},
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

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set over the index
	resSet := workspace.Resolve(idx, &cfg)

	// Create additional fixture files for FETCH and INCLUDE test cases
	// (we'll reference them from CALLER but they need to exist in the index)

	tests := []struct {
		name              string
		callerPath        string
		callerRelPath     string
		cursorLine        int                         // 0-based
		cursorCol         int                         // byte column in the line
		expectLabel       string                      // expected CompletionItem.Label
		expectKind        protocol.CompletionItemKind // expected CompletionItemKind
		expectDetail      string                      // expected Detail substring
		expectFound       bool                        // whether the item should be present
		expectNotIncluded string                      // object name that should NOT be in the result (wrong type)
	}{
		{
			name:              "CALLNAT MYSU offers MYSUB (subprogram)",
			callerPath:        filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath:     "testdata/completion/module/CALLER.NSP",
			cursorLine:        4,  // Line with "  CALLNAT MYSU"
			cursorCol:         14, // at end of "MYSU"
			expectLabel:       "MYSUB",
			expectKind:        protocol.CompletionItemKindModule, // subprogram → Module
			expectDetail:      "subprogram",
			expectFound:       true,
			expectNotIncluded: "", // no exclusion expected in this case
		},
		{
			name:              "FETCH MYP offers MYPROG (program)",
			callerPath:        filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath:     "testdata/completion/module/CALLER.NSP",
			cursorLine:        5,  // Line with "  FETCH MYP"
			cursorCol:         11, // cursor at end of "MYP"
			expectLabel:       "MYPROG",
			expectKind:        protocol.CompletionItemKindFile, // program → File
			expectDetail:      "program",
			expectFound:       true,
			expectNotIncluded: "MYSUB", // subprogram should NOT appear in FETCH context
		},
		{
			name:              "INCLUDE SHAR offers SHARED (copycode)",
			callerPath:        filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath:     "testdata/completion/module/CALLER.NSP",
			cursorLine:        6,  // Line with "  INCLUDE SHAR"
			cursorCol:         14, // cursor at end of "SHAR"
			expectLabel:       "SHARED",
			expectKind:        protocol.CompletionItemKindReference, // copycode → Reference
			expectDetail:      "copycode",
			expectFound:       true,
			expectNotIncluded: "MYPROG", // program should NOT appear in INCLUDE context
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Read the caller file to construct the completion context
			content, err := os.ReadFile(tc.callerPath)
			if err != nil {
				t.Fatalf("failed to read caller file: %v", err)
			}

			// Derive the line text and cursor position
			line := lineAt(string(content), tc.cursorLine)
			_ = line // unused for now, just for completeness

			// Act: create handler context and call provideCompletion
			hctx := &handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       nil,
			}

			// Construct CompletionParams: cursor position with protocol encoding
			params := protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(workspaceRoot, tc.callerRelPath)),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine),
						Character: uint32(tc.cursorCol),
					},
				},
			}

			result, err := provideCompletion(hctx, params)

			// Assert: no error expected
			if err != nil {
				t.Errorf("provideCompletion returned error: %v", err)
			}

			// Assert: result should be a list (possibly empty during RED)
			if result == nil {
				result = []protocol.CompletionItem{}
			}

			// Assert: find the expected completion item
			var found *protocol.CompletionItem
			for i := range result {
				if result[i].Label == tc.expectLabel {
					found = &result[i]
					break
				}
			}

			if tc.expectFound {
				if found == nil {
					t.Errorf("expected completion item with label %q not found in result of %d items", tc.expectLabel, len(result))
					for i, item := range result {
						t.Logf("  [%d] %q (kind=%v)", i, item.Label, item.Kind)
					}
					return
				}

				// Assert: Kind matches expected
				if found.Kind != tc.expectKind {
					t.Errorf("Kind mismatch: got %v, want %v (Module=%v, File=%v, Reference=%v)", found.Kind, tc.expectKind, protocol.CompletionItemKindModule, protocol.CompletionItemKindFile, protocol.CompletionItemKindReference)
				}

				// Assert: Detail contains expected object-type label
				detail, ok := found.Detail.Get()
				if !ok {
					t.Errorf("Detail is not set on completion item")
				} else if !strings.Contains(detail, tc.expectDetail) {
					t.Errorf("Detail missing expected substring: got %q, expected to contain %q", detail, tc.expectDetail)
				}
			} else {
				if found != nil {
					t.Errorf("did not expect to find completion item with label %q", tc.expectLabel)
				}
			}

			// Assert: type filtering (if expectNotIncluded is set, that object should NOT be in the result)
			if tc.expectNotIncluded != "" {
				for _, item := range result {
					if item.Label == tc.expectNotIncluded {
						t.Errorf("type filtering failed: unexpected %q (wrong object type) in result", tc.expectNotIncluded)
					}
				}
			}
		})
	}
}

// TestProvideCompletion_SteplibFiltered tests steplib-reachable candidate filtering
// (feature 16, T5, Story 1, AC2). With a library map configured, only candidates
// reachable via the caller's steplib chain are offered. Unreachable libraries are excluded.
//
// Scenario:
// - Library map: APP (current) with steplib COMMON, plus unreachable OTHER
// - Caller in APP/APPCALLER.NSP, completing "CALLNAT APP|"
// - Expected: APPSUB (APP, reachable) + COMSUB (COMMON, steplib, reachable)
// - NOT offered: OTHSUB (OTHER, unreachable, not in APP's steplib chain)
func TestProvideCompletion_SteplibFiltered(t *testing.T) {
	testdataDir := "testdata/completion/steplib"
	_, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build the index with files from APP, COMMON, and OTHER
	idx := &workspace.Index{}

	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "APP", "APPCALLER.NSP"), "APP/APPCALLER.NSP"},
		{filepath.Join(testdataDir, "APP", "APPSUB.NSN"), "APP/APPSUB.NSN"},
		{filepath.Join(testdataDir, "COMMON", "COMSUB.NSN"), "COMMON/COMSUB.NSN"},
		{filepath.Join(testdataDir, "OTHER", "OTHSUB.NSN"), "OTHER/OTHSUB.NSN"},
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

		idx.Add(f.relPath, analysis)
	}

	// Configure a library map: APP (current) with COMMON as steplib
	cfg := config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD"},
		},
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{
				{
					Path:     "APP",
					Name:     "APP",
					Steplibs: []string{"COMMON"},
				},
			},
		},
	}

	tests := []struct {
		name              string
		cursorLine        int
		cursorCol         int
		prefix            string   // the partial prefix to search for
		libMapConfigured  bool     // when false, uses flat namespace (no library map)
		expectReachable   []string // subprogram names that should be offered
		expectUnreachable []string // subprogram names that should NOT be offered
		description       string
	}{
		{
			name:              "Steplib filtered: APP + COMMON reachable, OTHER excluded",
			cursorLine:        8,   // 0-based: line 9 with "  CALLNAT APPSUB" (but we'll type just "A")
			cursorCol:         13,  // at position for completing "A"
			prefix:            "A", // prefix that matches APPSUB but not COMSUB, OTHSUB
			libMapConfigured:  true,
			expectReachable:   []string{"APPSUB"},
			expectUnreachable: []string{"COMSUB", "OTHSUB"},
			description:       "AC2: with steplib chain, only reachable candidates offered",
		},
		{
			name:              "Flat namespace: all three offered (no library map)",
			cursorLine:        8,  // test with prefix matching all three
			cursorCol:         13, // placeholder
			prefix:            "", // empty prefix matches all
			libMapConfigured:  false,
			expectReachable:   []string{"APPSUB", "COMSUB", "OTHSUB"},
			expectUnreachable: []string{},
			description:       "AC3: with no library map, flat namespace offers all matches",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare config: use either the library map or empty for flat namespace
			testCfg := cfg
			if !tc.libMapConfigured {
				testCfg.Resolution.Libraries = nil
			}

			// Act: directly call provideModuleCompletion with the prefix
			// (bypassing the provider's line parsing since we control the prefix directly)
			result, err := provideModuleCompletion(idx, &testCfg, "APP/APPCALLER.NSP", tc.prefix, model.ObjectSubprogram)

			// Assert: no error
			if err != nil {
				t.Errorf("provideCompletion returned error: %v", err)
			}

			// Assert: result is non-nil (even if empty)
			if result == nil {
				result = []protocol.CompletionItem{}
			}

			// Collect offered names
			offeredNames := make(map[string]bool)
			for _, item := range result {
				offeredNames[item.Label] = true
			}

			// Assert: reachable candidates are present
			for _, expectedName := range tc.expectReachable {
				if !offeredNames[expectedName] {
					t.Errorf("expected reachable candidate %q not in result; got: %v", expectedName, offeredNames)
				}
			}

			// Assert: unreachable candidates are excluded
			for _, excludedName := range tc.expectUnreachable {
				if offeredNames[excludedName] {
					t.Errorf("unreachable candidate %q should NOT be in result (not in steplib chain)", excludedName)
				}
			}
		})
	}
}

// TestProvideCompletion_LiveFreshness tests live-document freshness (feature 16, T5, AC5).
// After a new module is added to the index via applyDocumentChange (simulating incremental
// re-analysis), a subsequent completion request offers the newly-added module without
// requiring a server restart.
//
// Scenario:
// - Start with APPSUB and COMSUB in the index
// - Call applyDocumentChange to analyze a new NEWSUB.NSN and add it to the index
// - Complete "CALLNAT NEW|"
// - Expected: NEWSUB is offered (demonstrating live freshness of the index)
func TestProvideCompletion_LiveFreshness(t *testing.T) {
	testdataDir := "testdata/completion/steplib"
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build initial index with APP/APPSUB and COMMON/COMSUB
	idx := &workspace.Index{}

	initialFiles := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "APP", "APPCALLER.NSP"), "APP/APPCALLER.NSP"},
		{filepath.Join(testdataDir, "APP", "APPSUB.NSN"), "APP/APPSUB.NSN"},
		{filepath.Join(testdataDir, "COMMON", "COMSUB.NSN"), "COMMON/COMSUB.NSN"},
	}

	az := natural.New(nil)
	for _, f := range initialFiles {
		content, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", f.path, err)
		}

		analysis, err := az.Analyze(f.path, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", f.path, err)
		}

		idx.Add(f.relPath, analysis)
	}

	cfg := config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD"},
		},
		Resolution: config.ResolutionConfig{
			Libraries: []config.Library{
				{
					Path:     "APP",
					Name:     "APP",
					Steplibs: []string{"COMMON"},
				},
			},
		},
	}

	// Initial resolution set
	resSet := workspace.Resolve(idx, &cfg)

	// Create handler context
	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
		idxResMu:    sync.RWMutex{},
		az:          az,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Verify initial state: NEWSUB is NOT yet available
	// Call provideModuleCompletion directly with empty prefix to see all subprograms
	result, err := provideModuleCompletion(idx, &cfg, "APP/APPCALLER.NSP", "", model.ObjectSubprogram)
	if err != nil {
		t.Fatalf("provideModuleCompletion returned error: %v", err)
	}
	if result == nil {
		result = []protocol.CompletionItem{}
	}

	// Verify NEWSUB is not in the initial results
	for _, item := range result {
		if item.Label == "NEWSUB" {
			t.Fatalf("NEWSUB should not be in initial results (before update)")
		}
	}

	// Act: add a new NEWSUB.NSN to APP via applyDocumentChange
	newSubContent := []byte(`*> New subprogram added dynamically
DEFINE DATA
END-DEFINE

PROCEDURE DIVISION.
END PROCEDURE.
`)
	hctx.applyDocumentChange("APP/NEWSUB.NSN", newSubContent)

	// Assert: subsequent completion finds NEWSUB
	// After applyDocumentChange, hctx.idx is updated with the new NEWSUB analysis
	// Snapshot the fresh idx under the read lock
	hctx.idxResMu.RLock()
	freshIdx := hctx.idx
	hctx.idxResMu.RUnlock()

	result, err = provideModuleCompletion(freshIdx, &hctx.cfg, "APP/APPCALLER.NSP", "", model.ObjectSubprogram)
	if err != nil {
		t.Fatalf("provideModuleCompletion returned error after update: %v", err)
	}
	if result == nil {
		result = []protocol.CompletionItem{}
	}

	// Find NEWSUB in the new result
	var foundNewsub *protocol.CompletionItem
	for i := range result {
		if result[i].Label == "NEWSUB" {
			foundNewsub = &result[i]
			break
		}
	}

	if foundNewsub == nil {
		t.Errorf("NEWSUB not found in completion after applyDocumentChange; got: %v", result)
		for i, item := range result {
			t.Logf("  [%d] %s", i, item.Label)
		}
		return
	}

	// Verify NEWSUB's properties
	if foundNewsub.Kind != protocol.CompletionItemKindModule {
		t.Errorf("NEWSUB Kind: got %v, want Module", foundNewsub.Kind)
	}
	if detail, ok := foundNewsub.Detail.Get(); !ok || detail != "subprogram" {
		t.Errorf("NEWSUB Detail: got %v, want 'subprogram'", detail)
	}
}

// TestProvideCompletion_PerformInlineBeforeExternal tests PERFORM subroutine completion
// with inline-first-then-external ordering (feature 16, T6, Story 2, AC1/AC2).
//
// Scenario:
// - A caller object with an inline DEFINE SUBROUTINE MY-INLINE and a partial "PERFORM MY"
// - An external subroutine MYEXT.NSS in the workspace
// - Expected: inline MY-INLINE comes first, followed by external MYEXT
// - Both should be offered as CompletionItemKindFunction (subroutine kind)
//
// Verifies that the provider lists inline candidates before external ones (mirrors
// FR-12 inline-before-external resolution order).
func TestProvideCompletion_PerformInlineBeforeExternal(t *testing.T) {
	testdataDir := "testdata/completion/perform"
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build the index with the caller (containing inline MY-INLINE) and
	// the external MYEXT subroutine
	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "CALLER.NSP"), "testdata/completion/perform/CALLER.NSP"},
		{filepath.Join(testdataDir, "MYEXT.NSS"), "testdata/completion/perform/MYEXT.NSS"},
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

		idx.Add(f.relPath, analysis)
	}

	// Compute the resolution set over the index
	resSet := workspace.Resolve(idx, &cfg)

	tests := []struct {
		name              string
		callerPath        string
		callerRelPath     string
		cursorLine        int // 0-based
		cursorCol         int // byte column in the line
		expectInlineFirst bool
		expectBoth        bool // whether both inline and external should be present
		description       string
	}{
		{
			name:              "PERFORM MY: inline first, then external (AC1/AC2)",
			callerPath:        filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath:     "testdata/completion/perform/CALLER.NSP",
			cursorLine:        7,  // Line with "PERFORM MY"
			cursorCol:         10, // at end of "MY"
			expectInlineFirst: true,
			expectBoth:        true,
			description:       "AC1/AC2: inline subroutine MY-INLINE listed before external MYEXT",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Read the caller file and get the structure to verify inline subroutine exists
			content, err := os.ReadFile(tc.callerPath)
			if err != nil {
				t.Fatalf("failed to read caller file: %v", err)
			}

			// Get the analysis for the caller to verify it has the inline subroutine
			callerAnalysis, err := az.Analyze(tc.callerPath, content)
			if err != nil {
				t.Fatalf("failed to analyze caller: %v", err)
			}

			// Verify the inline subroutine is in the structure
			if callerAnalysis.Structure == nil {
				t.Fatalf("caller structure is nil")
			}

			var foundInline bool
			for _, child := range callerAnalysis.Structure.Children {
				if child.Kind == model.SymbolSubroutine && strings.Contains(strings.ToUpper(child.Name), "INLINE") {
					foundInline = true
					break
				}
			}

			if !foundInline {
				t.Logf("warning: inline subroutine MY-INLINE not found in structure; children: %v", callerAnalysis.Structure.Children)
			}

			// Act: create handler context and call provideCompletion
			hctx := &handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       nil,
			}

			// Construct CompletionParams
			params := protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(workspaceRoot, tc.callerRelPath)),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine),
						Character: uint32(tc.cursorCol),
					},
				},
			}

			result, err := provideCompletion(hctx, params)

			// Assert: no error expected
			if err != nil {
				t.Errorf("provideCompletion returned error: %v", err)
			}

			// Assert: result should be a list (non-nil)
			if result == nil {
				result = []protocol.CompletionItem{}
			}

			// Assert: both MY-INLINE and MYEXT should be present
			if tc.expectBoth {
				var foundMyInline, foundMyext *protocol.CompletionItem
				for i := range result {
					if strings.Contains(strings.ToUpper(result[i].Label), "INLINE") {
						foundMyInline = &result[i]
					}
					if result[i].Label == "MYEXT" {
						foundMyext = &result[i]
					}
				}

				if foundMyInline == nil {
					t.Errorf("expected inline subroutine MY-INLINE not found in result (got %d items)", len(result))
					for i, item := range result {
						t.Logf("  [%d] %q (kind=%v)", i, item.Label, item.Kind)
					}
				}

				if foundMyext == nil {
					t.Errorf("expected external subroutine MYEXT not found in result (got %d items)", len(result))
					for i, item := range result {
						t.Logf("  [%d] %q (kind=%v)", i, item.Label, item.Kind)
					}
				}

				// Assert: both are CompletionItemKindFunction
				if foundMyInline != nil && foundMyInline.Kind != protocol.CompletionItemKindFunction {
					t.Errorf("MY-INLINE Kind: got %v, want Function", foundMyInline.Kind)
				}

				if foundMyext != nil && foundMyext.Kind != protocol.CompletionItemKindFunction {
					t.Errorf("MYEXT Kind: got %v, want Function", foundMyext.Kind)
				}

				// Assert: inline-before-external order
				if tc.expectInlineFirst && foundMyInline != nil && foundMyext != nil {
					inlineIdx := -1
					myextIdx := -1
					for i := range result {
						if strings.Contains(strings.ToUpper(result[i].Label), "INLINE") {
							inlineIdx = i
						}
						if result[i].Label == "MYEXT" {
							myextIdx = i
						}
					}

					if inlineIdx >= 0 && myextIdx >= 0 && inlineIdx > myextIdx {
						t.Errorf("inline-before-external order violated: MY-INLINE at index %d, MYEXT at index %d (expected INLINE < MYEXT)", inlineIdx, myextIdx)
					}
				}
			}
		})
	}
}

// TestProvideCompletion_PerformDynamicExcluded tests PERFORM dynamic-target exclusion
// (feature 16, T6, Story 2, AC3, FR-17/FR-18).
//
// Scenario:
// - A partial "PERFORM #DYN" (variable operand with # sigil)
// - Expected: no completions offered (empty list, no error, no diagnostic)
//
// Verifies that dynamic PERFORM targets (sigil-prefixed variables) are excluded from
// completion, as they are unresolvable modeled gaps (FR-17).
func TestProvideCompletion_PerformDynamicExcluded(t *testing.T) {
	testdataDir := "testdata/completion/perform"
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build index with the fixture
	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "CALLER.NSP"), "testdata/completion/perform/CALLER.NSP"},
		{filepath.Join(testdataDir, "MYEXT.NSS"), "testdata/completion/perform/MYEXT.NSS"},
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

		idx.Add(f.relPath, analysis)
	}

	resSet := workspace.Resolve(idx, &cfg)

	// A store lets each case's dynamic PERFORM line be the actual buffer the
	// provider reads (store-first) — the real completion path — instead of the
	// static on-disk fixture (which holds a non-dynamic "PERFORM MY").
	store := NewTestStore(t.TempDir(), az)

	tests := []struct {
		name          string
		line          string // the dynamic line placed in the open buffer
		cursorByteCol int
		description   string
		expectEmpty   bool
	}{
		{
			name:          "PERFORM #DYN: dynamic target excluded (AC3, FR-17)",
			line:          "PERFORM #DYN",
			cursorByteCol: 12,
			description:   "AC3: dynamic (sigil-prefixed) PERFORM target yields empty completion, no error",
			expectEmpty:   true,
		},
		{
			name:          "PERFORM &VAR: dynamic target excluded",
			line:          "PERFORM &VAR",
			cursorByteCol: 12,
			description:   "Dynamic ampersand-sigil target yields empty",
			expectEmpty:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: detect the context from the line
			kind, prefix := detectCompletionContext(tc.line, tc.cursorByteCol)

			// Verify the detector correctly identifies this as a PERFORM context
			if kind != newSubroutineContext(model.ObjectExternalSubroutine) {
				t.Fatalf("expected subroutine context, got %v", kind)
			}

			// Verify the prefix is dynamic (contains a sigil)
			if !isPrefixDynamic(prefix) {
				t.Fatalf("expected dynamic prefix (with sigil), got %q", prefix)
			}

			// Now test the provider: with the dynamic line as the open buffer, it
			// must detect the sigil prefix and return an empty list (no offer).
			docURI := newTestURI("dyn-perform.NSP")
			store.Open(docURI, 1, []byte(tc.line))

			result, err := provideCompletion(&handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       store,
			}, protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: docURI,
					},
					Position: protocol.Position{
						Line:      0,
						Character: uint32(tc.cursorByteCol),
					},
				},
			})

			// Assert: no error
			if err != nil {
				t.Errorf("provideCompletion returned error: %v", err)
			}

			// Assert: result is empty (no dynamic completions should be offered)
			if tc.expectEmpty {
				if result == nil {
					result = []protocol.CompletionItem{}
				}
				if len(result) > 0 {
					t.Errorf("expected empty result for dynamic PERFORM target, got %d items:", len(result))
					for i, item := range result {
						t.Logf("  [%d] %q", i, item.Label)
					}
				}
			}
		})
	}
}

// TestProvideCompletion_ContextNone tests the unrecognized-context path (Story 4, AC1).
// When the cursor is on a non-triggering statement (COMPUTE, MOVE, comment, blank line),
// provideCompletion returns an empty list (non-nil, no error).
//
// This verifies that ctxNone contexts gracefully return empty results without
// attempting to offer completions, and without raising an error or diagnostic.
func TestProvideCompletion_ContextNone(t *testing.T) {
	testdataDir := "testdata/completion/none"
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build the index by analyzing the fixture
	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []struct {
		path    string
		relPath string
	}{
		{filepath.Join(testdataDir, "CALLER.NSP"), "testdata/completion/none/CALLER.NSP"},
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

		idx.Add(f.relPath, analysis)
	}

	resSet := workspace.Resolve(idx, &cfg)

	tests := []struct {
		name          string
		callerPath    string
		callerRelPath string
		cursorLine    int // 0-based
		cursorCol     int // byte column
		description   string
	}{
		{
			name:          "COMPUTE statement: completion returns empty (AC1)",
			callerPath:    filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath: "testdata/completion/none/CALLER.NSP",
			cursorLine:    15, // line with "  COMPUTE #X = 1 + 2"
			cursorCol:     15,
			description:   "AC1: unrecognized context (COMPUTE) returns empty list, no error",
		},
		{
			name:          "MOVE statement: completion returns empty (AC1)",
			callerPath:    filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath: "testdata/completion/none/CALLER.NSP",
			cursorLine:    16, // line with "  MOVE 'HELLO' TO #Y"
			cursorCol:     15,
			description:   "AC1: unrecognized context (MOVE) returns empty list",
		},
		{
			name:          "Comment line: completion returns empty (AC1)",
			callerPath:    filepath.Join(testdataDir, "CALLER.NSP"),
			callerRelPath: "testdata/completion/none/CALLER.NSP",
			cursorLine:    17, // line with "*> This is a comment"
			cursorCol:     15,
			description:   "AC1: comment line returns empty list",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hctx := &handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       nil,
			}

			params := protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(workspaceRoot, tc.callerRelPath)),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine),
						Character: uint32(tc.cursorCol),
					},
				},
			}

			// Act
			result, err := provideCompletion(hctx, params)

			// Assert: no error expected
			if err != nil {
				t.Errorf("provideCompletion returned error: %v", err)
			}

			// Assert: result must be non-nil (even if empty) and empty
			if result == nil {
				t.Errorf("result is nil; expected non-nil empty list for ctxNone")
			} else if len(result) > 0 {
				t.Errorf("expected empty result for unrecognized context, got %d items:", len(result))
				for i, item := range result {
					t.Logf("  [%d] %q", i, item.Label)
				}
			}
		})
	}
}

// TestProvideCompletion_DDMField verifies DDM field-name completion at data-access
// statements (feature 16, T7, Story 3): field names + verbatim types are drawn from
// the resolved indexed .NSD (AC1/AC2), and a data-access statement naming an
// unindexed DDM yields an empty list with no error (AC3, FR-17).
//
// Fields come from the live buffer's data-access line, so each case is driven through
// the document Store (the real completion path). The workspace index holds CUSTOMER.NSD
// so LookupByName(CUSTOMER, ObjectDDM) resolves; ORDERS is intentionally absent.
func TestProvideCompletion_DDMField(t *testing.T) {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	idx := &workspace.Index{}
	cfg := config.Config{}
	az := natural.New(nil)

	ddmPath := filepath.Join("testdata/completion/ddmfield", "CUSTOMER.NSD")
	ddmContent, err := os.ReadFile(ddmPath)
	if err != nil {
		t.Fatalf("failed to read CUSTOMER.NSD: %v", err)
	}
	ddmAnalysis, err := az.Analyze(ddmPath, ddmContent)
	if err != nil {
		t.Fatalf("failed to analyze CUSTOMER.NSD: %v", err)
	}
	// Prerequisite: the DDM parser populated field definitions.
	if len(ddmAnalysis.Definitions) == 0 {
		t.Fatalf("CUSTOMER.NSD produced no Definitions; DDM-field completion prerequisite unmet")
	}
	idx.Add("testdata/completion/ddmfield/CUSTOMER.NSD", ddmAnalysis)
	resSet := workspace.Resolve(idx, &cfg)

	store := NewTestStore(t.TempDir(), az)

	tests := []struct {
		name         string
		bufferLine   string // dynamic data-access line placed in the open buffer
		cursorCol    int    // 0-based byte column (end of the partial field)
		expectFound  bool
		expectLabel  string
		expectDetail string // substring expected in Detail (the field type)
	}{
		{
			name:         "partial field NAM offers CUSTOMER-NAME with type A50 (AC1/AC2)",
			bufferLine:   "READ CUSTOMER NAM",
			cursorCol:    17,
			expectFound:  true,
			expectLabel:  "CUSTOMER-NAME",
			expectDetail: "A50",
		},
		{
			name:         "partial field EMA offers EMAIL with type A60 (AC1/AC2)",
			bufferLine:   "READ CUSTOMER EMA",
			cursorCol:    17,
			expectFound:  true,
			expectLabel:  "EMAIL",
			expectDetail: "A60",
		},
		{
			name:        "unresolved DDM (ORDERS not indexed) offers nothing (AC3, FR-17)",
			bufferLine:  "READ ORDERS FLD",
			cursorCol:   15,
			expectFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			docURI := newTestURI("ddm-buffer.NSP")
			store.Open(docURI, 1, []byte(tc.bufferLine))

			result, err := provideCompletion(&handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       store,
			}, protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
					Position: protocol.Position{
						Line:      0,
						Character: uint32(tc.cursorCol),
					},
				},
			})
			if err != nil {
				t.Fatalf("provideCompletion returned error: %v", err)
			}
			if result == nil {
				result = []protocol.CompletionItem{}
			}

			if !tc.expectFound {
				if len(result) != 0 {
					t.Errorf("expected empty result for unresolved DDM, got %d items", len(result))
					for i, item := range result {
						t.Logf("  [%d] %q", i, item.Label)
					}
				}
				return
			}

			var found *protocol.CompletionItem
			for i := range result {
				if result[i].Label == tc.expectLabel {
					found = &result[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("expected field %q not found in %d items", tc.expectLabel, len(result))
			}
			if found.Kind != protocol.CompletionItemKindField {
				t.Errorf("Kind: got %v, want Field (%v)", found.Kind, protocol.CompletionItemKindField)
			}
			detail, ok := found.Detail.Get()
			if !ok {
				t.Errorf("Detail not set on field completion %q", tc.expectLabel)
			} else if !strings.Contains(detail, tc.expectDetail) {
				t.Errorf("Detail: got %q, want to contain %q (field type)", detail, tc.expectDetail)
			}
		})
	}
}

// TestProvideCompletion_WireBytes_ModuleDetail tests that completion items for
// module contexts (CALLNAT, FETCH, etc.) emit detail as a JSON string on the wire,
// not as the corrupted {} value (feature 19, T1 RED).
//
// Story 1 AC1: The serialized JSON must contain "detail":"<string>" (e.g. "detail":"subprogram"),
// never "detail":{}.
//
// Scenario:
//   - CALLNAT prefix completing "MYSU" → matches MYSUB (subprogram) from the module fixture
//   - The completion provider builds the item with detail = "subprogram"
//   - When marshaled via stdlib json.Marshal (as the current dispatch does),
//     the protocol.Optional[string] should NOT serialize to {}
func TestProvideCompletion_WireBytes_ModuleDetail(t *testing.T) {
	testdataDir := "testdata/completion/module"
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build the index with the module fixture
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLER.NSP"},
		{"MYSUB.NSN"},
		{"MYPROG.NSP"},
		{"SHARED.NSC"},
	}

	az := natural.New(nil)
	for _, f := range files {
		filePath := filepath.Join(wd, testdataDir, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", f.relPath, err)
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
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        filepath.Join(wd, testdataDir),
		cfg:         cfg,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Act: call the completion provider with a CALLNAT prefix on line 5
	// (in the fixture CALLER.NSP, "CALLNAT MYSU" at line 5)
	params := protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(wd, testdataDir, "CALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(4),  // 0-based line 4 = 1-based line 5
				Character: uint32(12), // at end of "MYSU"
			},
		},
	}

	result, err := provideCompletion(hctx, params)
	if err != nil {
		t.Fatalf("provideCompletion returned error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil completion result")
	}

	// Find the MYSUB item in the result
	var found *protocol.CompletionItem
	for i := range result {
		if result[i].Label == "MYSUB" {
			found = &result[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected MYSUB completion item not found in %d items", len(result))
	}

	// Assert: Detail is set via Optional (in-memory check first)
	detail, ok := found.Detail.Get()
	if !ok || detail != "subprogram" {
		t.Fatalf("Detail not correctly set in memory: got %v, want 'subprogram'", detail)
	}

	// Marshal the result via marshalResult — the EXACT function the completion
	// dispatch calls in server.go's non-nil branch (feature 19). Calling the
	// dispatch marshaler (not a hard-coded gojson call) couples this test to the
	// production code path, so a regression that reverts the dispatch to stdlib
	// json.Marshal reproduces the {} corruption and turns this test red.
	marshalledJSON, err := marshalResult(result)
	if err != nil {
		t.Fatalf("failed to marshal completion result: %v", err)
	}

	jsonStr := string(marshalledJSON)

	// Assert: the emitted JSON contains "detail":"subprogram" as a string
	if !strings.Contains(jsonStr, `"detail":"subprogram"`) {
		t.Errorf("expected JSON to contain '\"detail\":\"subprogram\"', got: %s", jsonStr)
	}

	// Assert: the emitted JSON does NOT contain "detail":{} (the corruption)
	if strings.Contains(jsonStr, `"detail":{}`) {
		t.Errorf("detail field corrupted to empty object {} in JSON: %s", jsonStr)
	}
}

// TestProvideCompletion_WireBytes_PerformSortText tests that completion items for
// PERFORM contexts emit sortText as a JSON string on the wire with correct ordering
// (inline before external), not corrupted as {} (feature 19, T1 RED, Story 1 AC3).
//
// Story 1 AC3: Inline subroutines must sort before external ones via "sortText":"0..." vs "sortText":"1...",
// asserted at the wire level (marshaled JSON strings), not just on the Go struct.
//
// Scenario:
//   - PERFORM MY: completes with both inline MY-INLINE (should have sortText="0...") and
//     external MYEXT (should have sortText="1...")
//   - When marshaled via stdlib json.Marshal, both sortText values must be JSON strings,
//     never corrupted to {}
func TestProvideCompletion_WireBytes_PerformSortText(t *testing.T) {
	testdataDir := "testdata/completion/perform"
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Arrange: build the index with the perform fixture
	idx := &workspace.Index{}
	cfg := config.Defaults()

	files := []struct {
		relPath string
	}{
		{"CALLER.NSP"},
		{"MYEXT.NSS"},
	}

	az := natural.New(nil)
	for _, f := range files {
		filePath := filepath.Join(wd, testdataDir, f.relPath)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", f.relPath, err)
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
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        filepath.Join(wd, testdataDir),
		cfg:         cfg,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Act: call the completion provider with a PERFORM MY prefix on line 8
	// (in the fixture CALLER.NSP, "PERFORM MY" at line 8)
	params := protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(wd, testdataDir, "CALLER.NSP")),
			},
			Position: protocol.Position{
				Line:      uint32(7),  // 0-based line 7 = 1-based line 8
				Character: uint32(10), // at end of "MY"
			},
		},
	}

	result, err := provideCompletion(hctx, params)
	if err != nil {
		t.Fatalf("provideCompletion returned error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil completion result")
	}

	// Find the inline and external items
	var foundInline, foundExternal *protocol.CompletionItem
	for i := range result {
		if strings.Contains(result[i].Label, "MY-INLINE") {
			foundInline = &result[i]
		}
		if strings.Contains(result[i].Label, "MYEXT") {
			foundExternal = &result[i]
		}
	}

	if foundInline == nil {
		t.Fatalf("expected inline MY-INLINE not found in %d items", len(result))
	}
	if foundExternal == nil {
		t.Fatalf("expected external MYEXT not found in %d items", len(result))
	}

	// Verify in-memory: SortText is set via Optional
	inlineSortText, ok := foundInline.SortText.Get()
	if !ok || !strings.HasPrefix(inlineSortText, "0") {
		t.Fatalf("inline SortText not correctly set in memory: got %v, want prefix '0'", inlineSortText)
	}

	externalSortText, ok := foundExternal.SortText.Get()
	if !ok || !strings.HasPrefix(externalSortText, "1") {
		t.Fatalf("external SortText not correctly set in memory: got %v, want prefix '1'", externalSortText)
	}

	// Marshal the result via marshalResult — the EXACT function the completion
	// dispatch calls in server.go's non-nil branch (feature 19). Coupling to the
	// dispatch marshaler means a regression that reverts to stdlib json.Marshal
	// corrupts sortText to {} and turns this test red.
	marshalledJSON, err := marshalResult(result)
	if err != nil {
		t.Fatalf("failed to marshal completion result: %v", err)
	}

	jsonStr := string(marshalledJSON)

	// Assert: the emitted JSON contains "sortText":"0..." for inline (JSON string)
	if !strings.Contains(jsonStr, `"sortText":"0`) {
		t.Errorf("expected JSON to contain inline '\"sortText\":\"0...\"', got: %s", jsonStr)
	}

	// Assert: the emitted JSON contains "sortText":"1..." for external (JSON string)
	if !strings.Contains(jsonStr, `"sortText":"1`) {
		t.Errorf("expected JSON to contain external '\"sortText\":\"1...\"', got: %s", jsonStr)
	}

	// Assert: the emitted JSON does NOT contain "sortText":{} (the corruption)
	if strings.Contains(jsonStr, `"sortText":{}`) {
		t.Errorf("sortText field corrupted to empty object {} in JSON: %s", jsonStr)
	}
}
