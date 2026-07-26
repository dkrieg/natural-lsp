// Package model defines the analyzer's output types — the contract shared
// between the extraction backend (internal/analysis), the workspace index
// (internal/workspace), and the LSP layer (internal/server).
//
// Keep these types free of backend internals (regex, parser state, etc.) so
// the extraction backend can be replaced and so the extracted structure stays
// clean enough for external consumers. See docs/plans/natural-lsp-prd.md
// (NFR-15, NFR-16).
package model

// EdgeKind classifies a relationship extracted from Natural source.
type EdgeKind string

const (
	EdgeCalls              EdgeKind = "CALLS"                // CALLNAT 'LITERAL' — static
	EdgeCallsDynamic       EdgeKind = "CALLS_DYNAMIC"        // CALLNAT #VARIABLE — unresolved
	EdgeNavigatesTo        EdgeKind = "NAVIGATES_TO"         // FETCH / RUN 'LITERAL'
	EdgeNavigatesToDynamic EdgeKind = "NAVIGATES_TO_DYNAMIC" // FETCH / RUN #VARIABLE — unresolved
	EdgePerforms           EdgeKind = "PERFORMS"             // PERFORM subroutine
	EdgeIncludes           EdgeKind = "INCLUDES"             // INCLUDE copycode
	EdgeReads              EdgeKind = "READS"                // READ / FIND / GET
	EdgeWrites             EdgeKind = "WRITES"               // STORE / UPDATE / DELETE
)

// ObjectType classifies a Natural source object by its file extension and
// content structure (programs, subprograms, copycodes, etc.).
//
// String values are stable and machine-readable: they are used as keys in the
// on-disk cache and may be consumed by external tools such as lsp-graph. Never
// change an existing value; add a new constant instead.
type ObjectType string

const (
	// ObjectProgram is a Natural program (.NSP). Programs are the primary
	// executable entry points in a Natural application.
	ObjectProgram ObjectType = "program"

	// ObjectSubprogram is a Natural subprogram (.NSN). Subprograms are called
	// via CALLNAT and communicate through a DEFINE DATA PARAMETER section.
	ObjectSubprogram ObjectType = "subprogram"

	// ObjectExternalSubroutine is a Natural external subroutine (.NSS). Called
	// via PERFORM … SUBROUTINE and shares data through the calling program's
	// data areas.
	ObjectExternalSubroutine ObjectType = "externalsubroutine"

	// ObjectCopycode is a Natural copycode (.NSC). Copycodes are textual
	// fragments INCLUDEd into other objects at compile time; they are not
	// independently executable.
	ObjectCopycode ObjectType = "copycode"

	// ObjectMap is a Natural map (.NSM) — a screen/layout definition used by
	// INPUT and WRITE MAP statements.
	ObjectMap ObjectType = "map"

	// ObjectLocalDataArea is a Local Data Area (.NSL). Defines variables
	// scoped to a single program or subprogram; it is a data-definition
	// fragment, not an executable object.
	ObjectLocalDataArea ObjectType = "localdataarea"

	// ObjectGlobalDataArea is a Global Data Area (.NSG). Defines variables
	// shared across all objects in the same Natural session; it is a
	// data-definition fragment, not an executable object.
	ObjectGlobalDataArea ObjectType = "globaldataarea"

	// ObjectParameterDataArea is a Parameter Data Area (.NSA). Defines the
	// parameter interface for subprograms and external subroutines; it is a
	// data-definition fragment, not an executable object.
	ObjectParameterDataArea ObjectType = "parameterdataarea"

	// ObjectHelproutine is a Natural helproutine (.NSH) — invoked implicitly
	// by the runtime to provide field-level help.
	ObjectHelproutine ObjectType = "helproutine"

	// ObjectDDM is a Data Definition Module (.NSD). Describes the layout of an
	// Adabas file or other external data source; it is a metadata fragment, not
	// an executable object.
	ObjectDDM ObjectType = "ddm"

	// ObjectClass is a NaturalX class (.NS4).
	ObjectClass ObjectType = "class"

	// ObjectFunction is a Natural user-defined function (.NS7) — a callable
	// unit that returns a value directly in an expression.
	ObjectFunction ObjectType = "function"

	// ObjectDialog is a Natural for Windows dialog (.NS3).
	ObjectDialog ObjectType = "dialog"

	// ObjectAdapter is a Natural Ajax adapter (.NS8).
	ObjectAdapter ObjectType = "adapter"

	// ObjectText is a plain-text member (.NST) stored alongside Natural source
	// objects. It contains no executable or structural content and is indexed
	// for completeness only.
	ObjectText ObjectType = "text"

	// ObjectUnknown is assigned when the file extension is not recognized or
	// the content cannot be classified. An unknown object is still indexed so
	// that references to it can be surfaced; consumers should not assume any
	// structural properties.
	ObjectUnknown ObjectType = "unknown"
)

// SymbolKind classifies the kind of symbol in the workspace index.
// String values are stable and machine-readable: they are used as keys in the
// on-disk cache and may be consumed by external tools. Never change an existing
// value; add a new constant instead.
type SymbolKind string

const (
	// SymbolProgram represents a program symbol.
	// This constant is part of the legacy flat SymbolEntry model; newer code
	// should use the recursive Symbol type and related constants.
	SymbolProgram SymbolKind = "program"

	// SymbolObject represents the root object symbol (program, subprogram, DDM, etc.)
	// derived from file name. Used by the hierarchical Symbol type (feature 09).
	SymbolObject SymbolKind = "object"

	// SymbolSubroutine represents a subroutine defined via DEFINE SUBROUTINE.
	SymbolSubroutine SymbolKind = "subroutine"

	// SymbolDataSection represents a DEFINE DATA section (LOCAL, PARAMETER, GLOBAL, LINKAGE, etc.).
	SymbolDataSection SymbolKind = "data-section"

	// SymbolDataField represents a data item declared within a DEFINE DATA section.
	SymbolDataField SymbolKind = "data-field"

	// SymbolMap represents a map defined via DEFINE MAP.
	SymbolMap SymbolKind = "map"

	// SymbolDDMReference represents a reference to a data definition module (DDM).
	SymbolDDMReference SymbolKind = "ddm-reference"
)

// Symbol represents a named construct extracted from a Natural source file,
// arranged as a hierarchical tree rooted at the object level. It is used by
// LSP providers (document outline, workspace symbol search, hover) to navigate
// and present the source structure to the user.
//
// The tree structure mirrors LSP DocumentSymbol: Range is the whole-construct
// span, SelectionRange is the name/identifier token span. Children are nested
// recursively and always kept in stable, deterministic source order
// (sorted by Range.Start).
//
// Kinds correspond to Natural constructs: SymbolObject (object root), SymbolSubroutine,
// SymbolDataSection, SymbolDataField, SymbolMap, SymbolDDMReference. The root object
// node's Children hold (in order): data sections (each with its fields), subroutines,
// maps (each with their fields), and DDM references.
type Symbol struct {
	// Kind identifies the type of symbol (object, subroutine, data-section, etc.).
	Kind SymbolKind

	// Name is the identifier of the symbol, captured as written in the source
	// (normalized by the parser/extractor but not case-adjusted for matching).
	Name string

	// Range is the whole-construct source span (e.g., from DEFINE SUBROUTINE
	// to END-SUBROUTINE, from DEFINE DATA to the end of the section).
	Range Range

	// SelectionRange is the name/identifier token span within Range, used by
	// editors to position the cursor for go-to-definition and symbol highlighting.
	// For constructs without a discrete name token (e.g., data sections identified
	// by keyword), SelectionRange is the keyword's span.
	SelectionRange Range

	// Children holds nested symbols in deterministic order (by Range.Start).
	// Nil/empty for leaf symbols (subroutines, maps, data fields with no children).
	// For the object root and data sections, children are nested recursively.
	Children []Symbol

	// Type is the verbatim data-type notation for a data field (e.g., "A26", "P9,2", "(A) DYNAMIC").
	// Empty for non-field nodes (subroutines, maps, data sections, object roots) and for
	// group headers. Added in feature 28 (phase A: typed outline).
	Type string

	// Level is the nesting level of a data field (1, 2, 3, ...).
	// Zero for non-field nodes. Added in feature 28 (phase A: typed outline).
	Level int

	// Dimensions holds array bounds for each dimension of a data field.
	// Nil/empty for scalar fields and non-field nodes. Added in feature 28 (phase A: typed outline).
	Dimensions []ArrayDimension

	// Redefines is the name of the data field being redefined by a REDEFINE block.
	// Empty for non-field nodes and for fields that do not REDEFINE another field.
	// Added in feature 28 (phase A: typed outline).
	Redefines string

	// ViewOfDDM is the DDM name for a VIEW OF data field (feature 28, phase B).
	// Empty for non-field nodes and for fields that do not declare a VIEW OF.
	// Added in feature 28 (phase B: VIEW OF binding).
	ViewOfDDM string
}

// Range represents a span in a file, identified by start and end positions.
type Range struct {
	Start Position
	End   Position
}

// Position represents a location in a file, using line/column (1-based).
type Position struct {
	Line   int
	Column int
}

// SymbolEntry represents a symbol in the workspace index.
type SymbolEntry struct {
	Name  string
	Kind  SymbolKind
	Range Range
}

// EdgeEntry represents a relationship between two symbols or locations.
//
// All fields are populated by extractEdges in internal/analysis/natural/calls.go.
// Library is additive and optional: it is only set when the edge carries an
// explicit library qualifier (currently only RUN with a library-id operand).
// All other edge kinds leave Library empty.
type EdgeEntry struct {
	Source     Range
	Target     Range
	Kind       EdgeKind
	TargetName string
	// Library is the library qualifier for the target — set from the
	// library-id operand of a RUN statement, empty for all other edge kinds.
	// FETCH's second positional operand is a parameter field, not a library,
	// so it never populates this field.
	Library string
}

// DataAccessEntry represents a data access relationship (read/write) to a file.
//
// Name is the normalized (upper-case) name of the accessed view/DDM/file.
// NameRange is the source span of just the view-name token (used by hover/references
// to point at the accessed name, not the whole statement). Source remains the
// whole-statement range.
type DataAccessEntry struct {
	Name      string
	Kind      EdgeKind
	Source    Range
	NameRange Range
}

// ArrayDimension represents a single dimension of an array field.
// For a 1-D array like (1:12), Lower=1, Upper=12, UpperUnbounded=false.
// For an unbounded dimension like (1:*), Lower=1, UpperUnbounded=true.
type ArrayDimension struct {
	Lower          int
	Upper          int
	UpperUnbounded bool
}

// DataDefinition represents a declared variable (data item) from a DEFINE DATA section.
// It captures the name, level, type, array dimensions, and the section kind (local/parameter/global).
// Redefine nesting is captured via Children: subfields of a REDEFINE block.
// For use in hover (to back signatures), the SectionKind field distinguishes PARAMETER
// from LOCAL/GLOBAL.
type DataDefinition struct {
	// Name is the normalized (upper-case) identifier of the data item.
	Name string

	// Level is the nesting level (1, 2, 3, ...) as declared in DEFINE DATA.
	Level int

	// Type is the verbatim type/format string (e.g., "A10", "N7.2", "P9.2").
	// Empty for group headers (which have no type).
	Type string

	// Dimensions holds array bounds for each dimension (empty for scalars).
	Dimensions []ArrayDimension

	// SectionKind is the section keyword (local/parameter/global/linkage).
	// The presence and value of this field let hover/signature helpers
	// distinguish parameter interfaces from local/global data.
	SectionKind string

	// Range is the source span of this data item (from first token to last).
	Range Range

	// NameRange is the source span of just the name token(s) for this data item.
	// For data fields with names, this is a subset of Range pointing at the identifier(s).
	// For REDEFINE block headers (Name=""), this is zero. Used by go-to-definition and outline
	// to highlight the precise name span. Additive (feature 27).
	NameRange Range

	// Children holds subfields if this is a group or a REDEFINE block.
	// Nesting order matches declaration order. Nil/empty for scalars and groups with no children.
	Children []DataDefinition

	// Redefines holds the target field name for a REDEFINE block's sub-fields
	// (e.g., "#CUSTOMER-ID" when a field is declared under a REDEFINE clause).
	// Empty string ("") for non-redefine fields (Feature 28, T3).
	Redefines string
}

// DiagnosticSeverity classifies the severity of an extraction diagnostic.
//
// String values are stable and machine-readable. Never change an existing
// value; add a new constant instead.
type DiagnosticSeverity string

const (
	DiagnosticInfo    DiagnosticSeverity = "info"
	DiagnosticWarning DiagnosticSeverity = "warning"
	DiagnosticError   DiagnosticSeverity = "error"
)

// DiagnosticCode categorizes the type of diagnostic, distinguishing syntax errors
// from reference-resolution issues (e.g., ambiguity in a flat namespace). A machine-readable
// category used for filtering and grouping diagnostics by kind.
//
// String values are stable and machine-readable. Never change an existing value;
// add a new constant instead. An empty string represents an uncategorized diagnostic.
type DiagnosticCode string

const (
	// DiagnosticCodeSyntax indicates a parse error or syntactic issue in the source.
	DiagnosticCodeSyntax DiagnosticCode = "syntax"

	// DiagnosticCodeAmbiguity indicates an unresolved reference due to multiple
	// candidates in a flat namespace (e.g., two modules with the same name, no
	// library chain configured).
	DiagnosticCodeAmbiguity DiagnosticCode = "ambiguity"
)

// Diagnostic is the analyzer-side signal for an extraction or analysis issue
// found in a Natural source file (e.g., unrecognized syntax, unresolvable
// reference). The feature-03 indexer reads Diagnostics from FileAnalysis to
// decide SkipReason and to emit structured log entries; the LSP layer may
// forward them to the editor as textDocument/publishDiagnostics notifications.
type Diagnostic struct {
	// Message is the human-readable description of the issue.
	Message string

	// Severity classifies how serious the issue is (info, warning, error).
	Severity DiagnosticSeverity

	// Code categorizes the type of diagnostic (syntax error, ambiguous reference, etc.).
	// An empty string represents an uncategorized diagnostic for back-compat.
	Code DiagnosticCode

	// Range is the source span where the issue was detected.
	// Task 7 of feature 00-parser-foundation wires real token positions here;
	// constructors that predate that work use a {1,1}→{1,1} placeholder.
	Range Range
}

// FileAnalysis is the structured result of analyzing a single Natural source
// file. Fields are intentionally left to be filled in as features land; see
// the functional requirements in docs/plans/natural-lsp-prd.md.
type FileAnalysis struct {
	// ObjectType is the classified kind of Natural object represented by the
	// analyzed file, derived from its file extension.
	ObjectType ObjectType

	// Diagnostics holds extraction and analysis issues found during analysis of
	// this file. A non-empty slice does not necessarily mean the file is
	// unindexable; callers should inspect each entry's Severity.
	Diagnostics []Diagnostic

	// Symbols holds the symbols defined in this file (e.g., programs,
	// subroutines, data items). Populated by the workspace indexer for
	// navigation, completion, and reference finding.
	Symbols []SymbolEntry

	// Edges holds the relationships extracted from this file (calls,
	// includes, data access). Populated by the workspace indexer for
	// call hierarchy, dependency resolution, and incremental invalidation.
	Edges []EdgeEntry

	// DataAccess holds data access relationships (READ/FIND/GET,
	// STORE/UPDATE/DELETE) and DEFINE DATA symbols. Populated by the
	// workspace indexer for data flow analysis and dependency tracking.
	DataAccess []DataAccessEntry

	// Definitions holds data item definitions extracted from DEFINE DATA sections
	// (LOCAL, PARAMETER, GLOBAL, LINKAGE). Used for hover, parameter interfaces,
	// and workspace symbol mapping. Declaration order is preserved.
	Definitions []DataDefinition

	// WorkFiles holds work-file definitions extracted from DEFINE WORK FILE statements
	// (file descriptor numbers and their associated file names or variables).
	// Populated by the workspace indexer for file-access analysis.
	WorkFiles []WorkFile

	// HostVarRefs holds host-variable references extracted from embedded-SQL statements
	// (INTO, WHERE, VALUES, SET clauses and flexible-SQL opaque bodies).
	// Each entry carries the normalized (colon-stripped) name and source range.
	// Used for binding host vars back to DEFINE DATA declarations (feature 08b).
	// Populated by the workspace indexer for embedded-SQL extraction.
	HostVarRefs []HostVarRef

	// Structure is the hierarchical symbol tree for this file, rooted at the object level.
	// It unifies subroutines, data sections, maps, and data-access references into a single,
	// kind-tagged, walkable tree (mirrors LSP DocumentSymbol). Nil only when the analyzer
	// produced no AST for the file; an object with an unrecognized extension is still parsed
	// and yields a (possibly sparse) root, so Structure is non-nil there.
	// Populated by the analyzer's structure-extraction backend (feature 09);
	// persisted in the workspace cache (0.6.0+).
	Structure *Symbol

	// DataAreaRefs holds references to external data areas from USING clauses in DEFINE DATA sections.
	// Each entry captures the data-area name, the section kind that carries the USING,
	// and the source range of the name token. Used for cross-file field resolution (feature 27, T7).
	// Populated by the analyzer's data-area reference extraction (feature 27, T7);
	// persisted in the cache (0.8.0).
	DataAreaRefs []DataAreaRef

	// AST holds the parsed AST for this file. Populated by the parser
	// backend when available; nil when the parser is not integrated.
	AST any
}

// WorkFile represents a work-file definition from a DEFINE WORK FILE statement.
type WorkFile struct {
	// Number is the work-file slot number from the source
	// (e.g., 1 in "DEFINE WORK FILE 1 'REPORT.TXT'").
	Number int

	// Name is the file name as it appears in source.
	// For a string literal, the surrounding quotes are stripped
	// (e.g., 'REPORT.TXT' → "REPORT.TXT").
	// For a variable, the value is kept verbatim including any leading sigil
	// (e.g., "#DYNNAME"). A leading '#' signals a dynamic/unresolvable
	// reference — a modeled gap, not a diagnostic.
	Name string

	// Range is the source span of the entire DEFINE WORK FILE statement.
	Range Range
}

// HostVarRef represents a host-variable *use* (reference) from an embedded-SQL statement.
// This is distinct from DataDefinition, which represents a *declaration* of a variable.
// HostVarRef is used for SQL clauses (INTO, WHERE, VALUES, SET) and flexible-SQL opaque
// bodies that reference field variables by name.
//
// Name is the normalized (upper-case) identifier of the host variable, with the SQL colon
// prefix stripped and the Natural sigil (#, &, @, +) preserved, matching feature-08's
// DataDefinition.Name convention so that resolution can bind refs back to declarations later.
// Range is the source span of the host-var operand token in the statement.
//
// This type is additive to feature 08's data model and requires a cache-format bump
// (0.4.0 → 0.5.0) to persist it.
type HostVarRef struct {
	Name  string
	Range Range
}

// VariableRef represents a variable *use* (reference) from statement bodies in Natural source.
// This is distinct from DataDefinition, which represents a *declaration* of a variable.
// VariableRef is used to track every occurrence of a variable identifier in executable statements,
// enabling go-to-definition, find-references, and document highlight operations.
//
// Name is the normalized (upper-case) identifier of the variable, with Natural sigils (#, &, @, +)
// preserved, matching DataDefinition.Name convention so that resolution can bind refs back to
// declarations. Array subscripts are stripped (e.g., #T(1:10) becomes #T as a separate ref, with
// the index variable captured as its own ref).
//
// Range is the source span of the variable occurrence in the statement. A group-qualified
// reference like #GROUP.FIELD is NOT captured as a single span: ExtractVariableRefs emits the
// group (#GROUP) and the field (FIELD) as separate VariableRef entries, each with its own Range
// pointing at just that identifier token, so a cursor on either component resolves independently.
//
// This type is additive and in-memory only (feature 27) — it is NOT persisted in the cache.
type VariableRef struct {
	Name  string
	Range Range
}

// DataAreaRef represents a USING data-area reference from a DEFINE DATA section.
// This captures the external data-area name from a LOCAL/PARAMETER/GLOBAL USING <name>
// clause, enabling cross-file field resolution (feature 27, phase B, T7).
//
// Name is the normalized (upper-case) identifier of the data area (e.g., CUSTLDA).
// SectionKind is the section keyword (local/parameter/global) that carries the USING reference.
// Range is the source span of the data-area name token in the USING clause.
//
// This type is additive and persisted in the cache (feature 27, T7 GREEN).
// However, the cache-format version is not bumped for this persistence (0.8.0 handles both
// DataDefinition.NameRange and DataAreaRefs in a single bump). DataAreaRefs are used for
// field-resolution binding but do not affect the stable cache key (unlike feature 07 where
// resolution is not persisted).
type DataAreaRef struct {
	Name        string
	SectionKind string
	Range       Range
}
