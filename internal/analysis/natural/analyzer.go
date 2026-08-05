// Package natural is the parser-based implementation of analysis.Analyzer.
// It extracts Natural constructs (calls, data access, structure) tuned to
// patterns found in production code. Per-file extraction produces *unresolved*
// references with caller context; cross-file binding happens in
// workspace/resolution.go, not here.
package natural

import (
	"fmt"
	"sort"

	"github.com/dkrieg/natural-lsp/internal/analysis"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// Analyzer is the parser-based extraction backend.
type Analyzer struct {
	// custom maps file extensions to model.ObjectType for user-defined overrides.
	// Keys must be normalized upper-case with leading dot (e.g., ".NAT").
	// Passed from config at construction time; consulted by classify().
	custom map[string]model.ObjectType
}

// New returns a parser-based Analyzer. custom maps normalized upper-case
// extensions (e.g. ".NAT") to ObjectType overrides sourced from config;
// pass nil for the default built-in table only.
func New(custom map[string]model.ObjectType) *Analyzer {
	return &Analyzer{custom: custom}
}

// compile-time assertion that Analyzer satisfies the analysis seam (NFR-15).
// This constraint must be preserved: LSP-facing code in internal/server,
// internal/workspace, and internal/document must consume FileAnalysis (including
// AST) only through the internal/model contract and the analysis.Analyzer
// interface. Type-asserting FileAnalysis.AST to concrete natural.* node types
// in LSP-facing code is forbidden and couples the LSP layer to the parser
// implementation, violating backend replaceability. See seam_test.go for
// architectural guard tests.
var _ analysis.Analyzer = (*Analyzer)(nil)

// Analyze runs the extraction pipeline over one file's contents.
func (a *Analyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	result := model.FileAnalysis{}
	result.ObjectType = classify(path, a.custom)

	// If the extension is unknown, append a diagnostic for observability (FR-9).
	// FileAnalysis.Diagnostics is the analyzer-side signal for unrecognized files;
	// the feature-03 indexer reads Diagnostics to emit SkipReason entries and
	// structured log output — that wiring is out of scope here.
	if result.ObjectType == model.ObjectUnknown {
		// Range is a placeholder; the extension diagnostic has no meaningful source span.
		result.Diagnostics = append(result.Diagnostics, model.Diagnostic{
			Code:     model.DiagnosticCodeSyntax,
			Severity: model.DiagnosticInfo,
			Message:  fmt.Sprintf("unrecognized extension %q", normalizeExt(path)),
			Range:    model.Range{Start: model.Position{Line: 1, Column: 1}, End: model.Position{Line: 1, Column: 1}},
		})
	}

	// Feature 12-hover, T2A: DDM field extraction. `.NSD` files are fixed-column
	// tabular reports, not Natural source — route through a dedicated DDM parser,
	// not the Natural lexer/recursive-descent parser.
	//
	// Structure is intentionally left nil here: extractStructure requires a parsed
	// *Program AST that DDM files do not produce, and building a DDM-specific
	// symbol tree (SymbolObject root + SymbolDataField children) is deferred as a
	// follow-up. The document-symbols handler (internal/server/document_symbols.go)
	// already handles nil Structure gracefully (returns an empty outline). Hover for
	// DDM fields reads Definitions, not Structure, so hover is unaffected.
	if result.ObjectType == model.ObjectDDM {
		result.Definitions = extractDDMDefinitions(string(content))
		return result, nil
	}

	// Parse the content into an AST. Parse always returns a non-nil AST and a nil
	// error; malformed input is surfaced through ast.Diagnostics with real token
	// positions rather than a returned error.
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	ast, _ := parser.Parse()
	result.AST = ast

	// Transfer parser diagnostics (syntax errors with real ranges) to the result,
	// stamping each with Code = DiagnosticCodeSyntax (Feature 14, T0).
	if ast != nil && len(ast.Diagnostics) > 0 {
		for _, diag := range ast.Diagnostics {
			diag.Code = model.DiagnosticCodeSyntax
			result.Diagnostics = append(result.Diagnostics, diag)
		}
	}

	// Extract edges from the parsed AST. The extractor runs over whatever the
	// parser produced (including partial ASTs from malformed code) without panicking
	// (FR-43), and returns all valid edges it can extract. No diagnostics are emitted
	// from extraction — syntax errors are already in result.Diagnostics.
	if ast != nil {
		result.Edges = extractEdges(ast)
		result.DataAccess = extractDataAccess(ast)
		result.Definitions = extractDefinitions(ast)
		result.WorkFiles = extractWorkFiles(ast)

		// Feature 08b: wire SQL extraction into the analysis pipeline.
		// Merge SQL call edges (CALLDBPROC) with feature-06 edges, maintaining global source order.
		sqlCalls := extractSQLCalls(ast)
		result.Edges = append(result.Edges, sqlCalls...)
		sort.SliceStable(result.Edges, func(i, j int) bool {
			return sourceStartLess(
				result.Edges[i].Source.Start.Line, result.Edges[i].Source.Start.Column,
				result.Edges[j].Source.Start.Line, result.Edges[j].Source.Start.Column,
			)
		})

		// Merge SQL data-access entries (SELECT FROM, PROCESS SQL DDM reads) with feature-08 entries,
		// maintaining global source order.
		sqlAccess := extractSQLAccess(ast)
		result.DataAccess = append(result.DataAccess, sqlAccess...)
		sort.SliceStable(result.DataAccess, func(i, j int) bool {
			return sourceStartLess(
				result.DataAccess[i].Source.Start.Line, result.DataAccess[i].Source.Start.Column,
				result.DataAccess[j].Source.Start.Line, result.DataAccess[j].Source.Start.Column,
			)
		})

		// Extract host-variable references from SQL statements. This is a new field
		// with no prior entries to merge; just assign directly.
		result.HostVarRefs = extractHostVarRefs(ast)

		// Extract data-area references from USING clauses in DEFINE DATA sections.
		// This is a new field with no prior entries to merge; just assign directly.
		// Used for feature 27, T7 (cross-file field resolution via external data areas).
		result.DataAreaRefs = extractDataAreaRefs(ast)

		// Derive USES_DATA_AREA edges from data-area references (Feature 36, T1).
		// One edge per USING clause, extracted from the already-computed DataAreaRefs
		// to guarantee Source matches the feature-27 field-resolution range (OQ-1).
		// Do not re-walk the AST (extractDataAreaRefs already omits malformed USING).
		dataAreaEdges := []model.EdgeEntry{}
		for _, ref := range result.DataAreaRefs {
			dataAreaEdges = append(dataAreaEdges, model.EdgeEntry{
				Source:     ref.Range,
				Target:     model.Range{},
				Kind:       model.EdgeUsesDataArea,
				TargetName: ref.Name,
			})
		}
		result.Edges = append(result.Edges, dataAreaEdges...)

		// Re-sort all edges (USES_DATA_AREA + existing CALLNAT/INCLUDE/etc.) by Source.Start.
		sort.SliceStable(result.Edges, func(i, j int) bool {
			return sourceStartLess(
				result.Edges[i].Source.Start.Line, result.Edges[i].Source.Start.Column,
				result.Edges[j].Source.Start.Line, result.Edges[j].Source.Start.Column,
			)
		})

		// Wire extractStructure into the analysis pipeline (Feature 09, Task 5).
		// Call after all extractors (Edges, DataAccess, Definitions, WorkFiles, HostVarRefs, DataAreaRefs)
		// and after all sorting is complete, so DataAccess slice is final.
		result.Structure = extractStructure(path, ast, result.Definitions, result.DataAccess)
	}

	return result, nil
}

// ExtractVariableRefs extracts variable use-site references from source content.
// This is a lightweight, on-demand scan that returns every variable identifier
// occurrence in statement bodies (not including DEFINE DATA declarations).
// The returned slice is always non-nil but may be empty (FR-43).
// This method is used by navigation providers to enable variable go-to-definition
// and find-references (feature 27, T2).
func (a *Analyzer) ExtractVariableRefs(content string) []model.VariableRef {
	return ExtractVariableRefs(content)
}

// SemanticTokens classifies tokens in source content for syntax/semantic highlighting.
// This is an on-demand, non-persisted operation that returns classified token spans
// for display in the editor. Path is used for object-type classification (by extension).
// The returned slice is always non-nil but may be empty (FR-43).
// This method is used by the LSP provider for semantic-tokens (feature 29, T2+).
// Currently implements Phase A (lexical) + Phase B (variable/parameter identifiers, T7).
func (a *Analyzer) SemanticTokens(path string, content []byte) []model.SemanticToken {
	return semanticTokensPhaseB(path, string(content))
}
