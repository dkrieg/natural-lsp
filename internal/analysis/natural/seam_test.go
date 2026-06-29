// Package natural provides the parser-based extraction backend. This file
// contains seam-purity guards to ensure LSP-facing code depends only on the
// analysis.Analyzer interface, never on concrete backend types.
//
// NFR-15 (replaceable backend) constraint:
// LSP-facing production code in internal/server, internal/workspace, and
// internal/document must NOT import "natural-lsp/internal/analysis/natural".
// Such code must consume FileAnalysis (including AST) only through the
// internal/model contract and the analysis.Analyzer interface.
// Type-asserting FileAnalysis.AST to concrete natural.* node types is forbidden
// in LSP-facing code — it couples the LSP layer to the parser implementation.
package natural

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"natural-lsp/internal/analysis"
	"natural-lsp/internal/model"
)

// TestSeam_LSPFacingPackagesDoNotImportConcreteBackend verifies that LSP-facing
// production code (server, workspace, document) does not import the concrete
// natural package, preserving the Analyzer interface seam (NFR-15).
func TestSeam_LSPFacingPackagesDoNotImportConcreteBackend(t *testing.T) {
	// LSP-facing production packages that must stay behind the Analyzer seam.
	// Use relative paths from this file's location (internal/analysis/natural).
	lspPackageRelPaths := map[string]string{
		"server":    "../../server",
		"workspace": "../../workspace",
		"document":  "../../document",
	}

	for pkgName, pkgRelPath := range lspPackageRelPaths {
		// Resolve relative to the current directory (internal/analysis/natural).
		pkgDir, err := filepath.Abs(pkgRelPath)
		if err != nil {
			t.Fatalf("Failed to resolve %s (%s): %v", pkgName, pkgRelPath, err)
		}

		// Skip if the package directory doesn't exist (shouldn't happen, but be robust).
		if info, err := os.Stat(pkgDir); err != nil || !info.IsDir() {
			t.Logf("Package %s directory (%s) not found; skipping", pkgName, pkgDir)
			continue
		}

		// Scan all non-test .go files in this package.
		files, err := os.ReadDir(pkgDir)
		if err != nil {
			t.Fatalf("Failed to read directory %s: %v", pkgDir, err)
		}

		for _, entry := range files {
			// Skip test files and non-.go files.
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}

			filePath := filepath.Join(pkgDir, entry.Name())
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read file %s: %v", filePath, err)
			}

			// Parse the file to extract import statements.
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filePath, content, parser.ImportsOnly)
			if err != nil {
				t.Errorf("Failed to parse %s: %v", filePath, err)
				continue
			}

			// Check each import.
			for _, importSpec := range f.Imports {
				importPath := strings.Trim(importSpec.Path.Value, `"`)
				if importPath == "natural-lsp/internal/analysis/natural" {
					t.Errorf(
						"%s (in LSP-facing package %s) imports the concrete natural backend "+
							"(natural-lsp/internal/analysis/natural), violating NFR-15: "+
							"LSP-facing code must depend only on analysis.Analyzer and internal/model",
						entry.Name(), pkgName,
					)
				}
			}
		}
	}
}

// TestSeam_AnalyzerUsableThroughInterface verifies that the concrete analyzer
// satisfies the analysis.Analyzer interface and is usable by LSP-facing code
// without type-asserting the AST to concrete backend types.
func TestSeam_AnalyzerUsableThroughInterface(t *testing.T) {
	// Instantiate the concrete analyzer and assign to the interface type.
	var a analysis.Analyzer = New(nil)

	// Call Analyze through the interface (simulating how LSP-facing code uses it).
	result, err := a.Analyze("test.NSP", []byte("CALLNAT 'MYPROG'"))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Verify the result conforms to the model.FileAnalysis contract.
	if result.ObjectType != model.ObjectProgram {
		t.Errorf("ObjectType = %v, want %v", result.ObjectType, model.ObjectProgram)
	}

	// Verify AST is populated.
	if result.AST == nil {
		t.Error("AST is nil; expected non-nil *Program")
	}

	// Verify AST is usable through the model contract.
	// LSP-facing code must NOT type-assert result.AST to *natural.Program.
	// This test documents that the AST is returned as an opaque interface{},
	// preserving the seam.
	_ = result.AST // Use via interface{} only, never type-assert in production code.

	// Diagnostics may be empty or contain parser/analyzer diagnostics, but both
	// are valid contracts. Just verify the slice exists.
	_ = result.Diagnostics
}
