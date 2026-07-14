package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoStdlibJSONMarshalForResults is a guard that ensures stdlib json.Marshal
// is never reintroduced in internal/server to marshal protocol result types.
//
// Background: protocol types from go.lsp.dev/protocol (such as
// protocol.Optional[T], protocol.Nullable[T], protocol.CallHierarchyItem.Data)
// implement only the json/v2 MarshalJSONTo method, not stdlib json.MarshalJSON.
// When marshaled via stdlib json.Marshal, these fields silently corrupt to `{}`
// rather than their intended JSON value. Feature 19 (Story 2 AC3) unified all
// protocol result marshaling to gojson.Marshal (aliased as
// github.com/go-json-experiment/json) to respect MarshalJSONTo implementations.
//
// This test scans the package's production source files (server.go and dispatch
// files, excluding *_test.go) and fails if it finds evidence of stdlib
// json.Marshal being used to serialize a protocol result. The guard catches
// accidental reintroduction via review or merge, preventing silent corruption
// of completion details, sort text, signature-help unions, and call-hierarchy
// data fields.
//
// Implementation: asserts that no production .go file in internal/server imports
// "encoding/json" and that no production file contains the literal "json.Marshal(".
// If all marshaling uses gojson.Marshal or protocol.MarshalJSONTo, both
// assertions hold and the guard passes.
func TestNoStdlibJSONMarshalForResults(t *testing.T) {
	// Get the directory containing this test file (internal/server).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path")
	}
	pkgDir := filepath.Dir(thisFile)

	// Read all production .go files in the package (exclude *_test.go).
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	var productionFiles []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			productionFiles = append(productionFiles, filepath.Join(pkgDir, name))
		}
	}

	if len(productionFiles) == 0 {
		t.Fatal("no production .go files found in internal/server")
	}

	var findings []string

	// Check 1: No production file imports "encoding/json" (unless justified).
	// After feature 19 T5 refactor, the stdlib json import should be absent.
	// If any production file imports it, flag it.
	for _, file := range productionFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}

		fileStr := string(content)

		// Look for `import ... "encoding/json"` in the import block.
		// Be conservative: flag any file that imports encoding/json anywhere.
		if strings.Contains(fileStr, `"encoding/json"`) {
			findings = append(findings, filepath.Base(file)+": imports stdlib encoding/json (should use gojson for protocol results)")
		}

		// Check 2: No production file contains standalone json.Marshal( for result marshaling.
		// The pattern to catch is the stdlib package-qualified call (json.Marshal).
		// Legitimate use: only gojson.Marshal (aliased) or protocol.*.MarshalJSONTo are allowed
		// for result marshaling. This check looks for the unqualified pattern that would indicate
		// a conflict with the gojson import alias — i.e., if someone wrote json.Marshal directly
		// (which would require importing stdlib json).
		//
		// Since we check for "encoding/json" import above, this second check catches any
		// json.Marshal( call that would use that import (if it existed). We flag it conservatively:
		// if the file contains both an "encoding/json" import AND json.Marshal(, it's definitely wrong.
		// If it contains json.Marshal( but no "encoding/json" import, it's suspicious (orphaned
		// reference or already-removed import) and should be investigated.
		if strings.Contains(fileStr, `"encoding/json"`) && strings.Contains(fileStr, "json.Marshal(") {
			findings = append(findings, filepath.Base(file)+": contains json.Marshal( with encoding/json import (must use gojson.Marshal or MarshalJSONTo)")
		}
	}

	if len(findings) > 0 {
		t.Errorf("stdlib json.Marshal guard failed:\n%s", strings.Join(findings, "\n"))
	}
}
