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
	EdgeCalls        EdgeKind = "CALLS"         // CALLNAT 'LITERAL' — static
	EdgeCallsDynamic EdgeKind = "CALLS_DYNAMIC" // CALLNAT #VARIABLE — unresolved
	EdgeNavigatesTo  EdgeKind = "NAVIGATES_TO"  // FETCH / RUN 'LITERAL'
	EdgePerforms     EdgeKind = "PERFORMS"      // PERFORM subroutine
	EdgeIncludes     EdgeKind = "INCLUDES"      // INCLUDE copycode
	EdgeReads        EdgeKind = "READS"         // READ / FIND / GET
	EdgeWrites       EdgeKind = "WRITES"        // STORE / UPDATE / DELETE
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

// Diagnostic is the analyzer-side signal for an extraction or analysis issue
// found in a Natural source file (e.g., unrecognized syntax, unresolvable
// reference). The feature-03 indexer reads Diagnostics from FileAnalysis to
// decide SkipReason and to emit structured log entries; the LSP layer may
// forward them to the editor as textDocument/publishDiagnostics notifications.
type Diagnostic struct {
	Message  string
	Severity DiagnosticSeverity
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

	// TODO: symbols, references/edges, data access, program structure.
}
