// Package natural implements a hand-written lexer and recursive-descent parser
// for Software AG Natural, following the reference implementation in natls.
//
// This file defines the AST node types for Natural constructs.
package natural

import "github.com/dkrieg/natural-lsp/internal/model"

// OperandRef represents a single operand reference (name + source range) within a statement.
// Used by SQL statement nodes to expose unbound operand lists (columns, table names,
// host-variable names, etc.). The reference is explicitly UNBOUND: no resolution or
// binding to a definition is performed at this layer. Binding is deferred to the
// extraction feature (feature 08b).
type OperandRef struct {
	Name  string      // the operand name (e.g., "COL1", "#VAR", "EMPLOYEES")
	Range model.Range // source range of the operand in the file
	// HostVar reports whether this operand was written with a leading colon
	// (":VAR"), the mandatory marker for a native-SQL host variable whose name
	// carries no Natural sigil (e.g. the reserved-word case ":DATE"). The colon
	// itself is stripped from Name; sigil-prefixed host vars (#/&/+/@) are
	// recognized from Name directly and do not need this flag. Meaningless for
	// table/column operands, where it is always false.
	HostVar bool
}

// Node is the base interface for all AST nodes.
type Node interface {
	Position() (model.Position, model.Position)
}

// Program is the root node of a Natural program AST.
type Program struct {
	StartPos      model.Position
	EndPos        model.Position
	Diagnostics   []model.Diagnostic
	Subroutines   []*Subroutine
	DataSections  []*DataSection
	Includes      []*IncludeStatement
	Calls         []*CallStatement
	Fetches       []*FetchStatement
	Runs          []*RunStatement
	Performs      []*PerformStatement
	Maps          []*Map
	Reads         []*ReadStatement
	Stores        []*StoreStatement
	Finds         []*FindStatement
	Gets          []*GetStatement
	RecordUpdates []*RecordUpdateStatement
	RecordDeletes []*RecordDeleteStatement
	// Embedded SQL statement slices (ES-3, ES-4):
	SelectSingles  []*SelectSingleStatement
	Selects        []*SelectStatement
	Inserts        []*InsertStatement
	SQLUpdates     []*SQLUpdateStatement
	SQLDeletes     []*SQLDeleteStatement
	Merges         []*MergeStatement
	Commits        []*CommitStatement
	Rollbacks      []*RollbackStatement
	CallDBProcs    []*CallDBProcStatement
	ProcessSQLs    []*ProcessSQLStatement
	ReadResultSets []*ReadResultSetStatement
	WorkFiles      []*WorkFileDefinition
}

func (p *Program) Position() (model.Position, model.Position) {
	return p.StartPos, p.EndPos
}

// Subroutine represents an inline subroutine definition.
type Subroutine struct {
	StartPos    model.Position
	EndPos      model.Position
	Name        string
	NameRange   model.Range // The range of the subroutine name token (feature 18, T6a)
	DataSection *DataSection
}

func (s *Subroutine) Position() (model.Position, model.Position) {
	return s.StartPos, s.EndPos
}

// DataSection represents a single section within a DEFINE DATA block (LOCAL, PARAMETER, GLOBAL, or LINKAGE).
// When a DEFINE DATA contains multiple section keywords, one DataSection node is emitted per section.
type DataSection struct {
	StartPos model.Position
	EndPos   model.Position
	Kind     string // section keyword: "local", "parameter", "global", "linkage" (lowercase for case-insensitive comparison)
	Fields   []*DataField
}

func (ds *DataSection) Position() (model.Position, model.Position) {
	return ds.StartPos, ds.EndPos
}

// DataField represents a data item within a data section.
type DataField struct {
	StartPos   model.Position
	EndPos     model.Position
	Level      int
	Name       string
	Type       string       // verbatim format (e.g., "A10", "N7.2", "P9.2"); "" for a group
	Dimensions []ArrayBound // nil/empty for scalar
	Redefines  string       // target field name for REDEFINE; "" if not a redefine
	Children   []*DataField // group members and redefine subfields
	NameRange  model.Range  // source span of just the name token(s); zero for REDEFINE headers
}

func (df *DataField) Position() (model.Position, model.Position) {
	return df.StartPos, df.EndPos
}

// ArrayBound represents a single dimension of an array (e.g., 1:12 or 1:5).
type ArrayBound struct {
	Lower          int
	Upper          int
	UpperUnbounded bool // true for 1:* (unbounded upper limit)
}

// Map represents a DEFINE MAP block.
type Map struct {
	StartPos model.Position
	EndPos   model.Position
	Name     string
	Fields   []*DataField
}

func (m *Map) Position() (model.Position, model.Position) {
	return m.StartPos, m.EndPos
}

// IncludeStatement represents an INCLUDE statement.
type IncludeStatement struct {
	StartPos        model.Position
	EndPos          model.Position
	Target          string
	TargetIsLiteral bool
	TargetRange     model.Range
}

func (i *IncludeStatement) Position() (model.Position, model.Position) {
	return i.StartPos, i.EndPos
}

// CallStatement represents a CALLNAT statement.
type CallStatement struct {
	StartPos        model.Position
	EndPos          model.Position
	Target          string
	Parameters      []string
	TargetIsLiteral bool
	TargetRange     model.Range
}

func (c *CallStatement) Position() (model.Position, model.Position) {
	return c.StartPos, c.EndPos
}

// FetchStatement represents a FETCH statement.
type FetchStatement struct {
	StartPos        model.Position
	EndPos          model.Position
	Target          string
	TargetIsLiteral bool
	TargetRange     model.Range
}

func (f *FetchStatement) Position() (model.Position, model.Position) {
	return f.StartPos, f.EndPos
}

// RunStatement represents a RUN statement.
type RunStatement struct {
	StartPos        model.Position
	EndPos          model.Position
	Target          string
	TargetIsLiteral bool
	TargetRange     model.Range
	Library         string
}

func (r *RunStatement) Position() (model.Position, model.Position) {
	return r.StartPos, r.EndPos
}

// PerformStatement represents a PERFORM statement.
type PerformStatement struct {
	StartPos    model.Position
	EndPos      model.Position
	Target      string
	TargetRange model.Range
}

func (p *PerformStatement) Position() (model.Position, model.Position) {
	return p.StartPos, p.EndPos
}

// ReadStatement represents a READ statement.
type ReadStatement struct {
	StartPos    model.Position
	EndPos      model.Position
	Target      string
	TargetRange model.Range // source span of just the view-name token
}

func (r *ReadStatement) Position() (model.Position, model.Position) {
	return r.StartPos, r.EndPos
}

// StoreStatement represents a STORE statement.
type StoreStatement struct {
	StartPos    model.Position
	EndPos      model.Position
	Target      string
	TargetRange model.Range // source span of just the view-name token
}

func (s *StoreStatement) Position() (model.Position, model.Position) {
	return s.StartPos, s.EndPos
}

// FindStatement represents a FIND statement (Task 3 / FR-19).
// Captures the view/DDM name and statement position.
// A malformed FIND with no operand emits a diagnostic and has empty Target.
type FindStatement struct {
	StartPos    model.Position
	EndPos      model.Position
	Target      string
	TargetRange model.Range // source span of just the view-name token
}

func (f *FindStatement) Position() (model.Position, model.Position) {
	return f.StartPos, f.EndPos
}

// GetStatement represents a GET statement (Task 5 / FR-19).
// Captures the view/DDM name and statement position.
// GET SAME is a special case: it has no view operand (re-reads current record),
// so Target is empty and no diagnostic is emitted (it is valid Natural).
// A malformed GET with no operand (and not GET SAME) would be invalid, but
// GET SAME is explicitly valid and distinguished from malformed.
type GetStatement struct {
	StartPos    model.Position
	EndPos      model.Position
	Target      string
	TargetRange model.Range // source span of just the view-name token; empty for GET SAME
}

func (g *GetStatement) Position() (model.Position, model.Position) {
	return g.StartPos, g.EndPos
}

// RecordUpdateStatement represents an Adabas record UPDATE statement (Task 7 / FR-20).
// Adabas record UPDATE has no file operand: it updates the record from the preceding
// READ/FIND/GET loop. The optional Label (from UPDATE (label)) identifies which record.
// No Target field: the file/view comes from the enclosing loop, binding deferred to extraction.
type RecordUpdateStatement struct {
	StartPos model.Position
	EndPos   model.Position
	Label    string // optional label from UPDATE (label); empty for bare UPDATE
}

func (u *RecordUpdateStatement) Position() (model.Position, model.Position) {
	return u.StartPos, u.EndPos
}

// RecordDeleteStatement represents an Adabas record DELETE statement (Task 7 / FR-20).
// Adabas record DELETE has no file operand: it deletes the record from the preceding
// READ/FIND/GET loop. The optional Label (from DELETE (label)) identifies which record.
// No Target field: the file/view comes from the enclosing loop, binding deferred to extraction.
type RecordDeleteStatement struct {
	StartPos model.Position
	EndPos   model.Position
	Label    string // optional label from DELETE (label); empty for bare DELETE
}

func (d *RecordDeleteStatement) Position() (model.Position, model.Position) {
	return d.StartPos, d.EndPos
}

// SelectSingleStatement represents a SELECT SINGLE statement (SQL, non-loop form).
// Exposes unbound operand lists (name+range pairs); binding is deferred to feature 08b.
// Fields:
// - Columns: selected columns (name+range)
// - IntoTargets: INTO clause host-var target names (name+range)
// - FromTables: FROM clause table operands (name+range)
// - WhereOperands: WHERE clause host-var operands (name+range)
// This is a singleton (no loop body); contrast with SelectStatement (ES-4).
type SelectSingleStatement struct {
	StartPos      model.Position
	EndPos        model.Position
	Columns       []OperandRef
	IntoTargets   []OperandRef
	FromTables    []OperandRef
	WhereOperands []OperandRef
}

func (s *SelectSingleStatement) Position() (model.Position, model.Position) {
	return s.StartPos, s.EndPos
}

// SelectStatement represents a SELECT statement with loop body (cursor loop, ES-4).
// Exposes unbound operand lists (name+range pairs) identical to SelectSingleStatement:
// - Columns: selected columns (name+range)
// - IntoTargets: INTO clause host-var target names (name+range)
// - FromTables: FROM clause table operands (name+range)
// - WhereOperands: WHERE clause host-var operands (name+range)
// PLUS a loop Body: []Node containing the statements executed per fetch.
// Contrast with SelectSingleStatement (ES-3), which has no body.
type SelectStatement struct {
	StartPos      model.Position
	EndPos        model.Position
	Columns       []OperandRef
	IntoTargets   []OperandRef
	FromTables    []OperandRef
	WhereOperands []OperandRef
	// Body holds the statements executed on each fetch iteration.
	// All AST statement nodes implement Node, so the body is a heterogeneous,
	// traversable slice: any statement kind (CallStatement, PerformStatement, etc.)
	// may appear. This is the first loop-body representation in this codebase.
	Body []Node
}

func (s *SelectStatement) Position() (model.Position, model.Position) {
	return s.StartPos, s.EndPos
}

// InsertStatement represents an INSERT statement (SQL).
// Exposes unbound operand lists (name+range pairs); binding is deferred to feature 08b.
// Fields:
// - IntoTable: INTO clause table operand (name+range)
// - Values: VALUES clause operands (host-vars or literals, name+range)
type InsertStatement struct {
	StartPos  model.Position
	EndPos    model.Position
	IntoTable []OperandRef
	Values    []OperandRef
}

func (i *InsertStatement) Position() (model.Position, model.Position) {
	return i.StartPos, i.EndPos
}

// SQLUpdateStatement represents an UPDATE statement in SQL form (distinct from Adabas UpdateStatement).
// Exposes unbound operand lists (name+range pairs); binding is deferred to feature 08b.
// Fields:
// - Table: table operand (name+range)
// - SetTargets: SET clause target columns and their values (name+range)
// - WhereOperands: WHERE clause host-var operands (name+range)
type SQLUpdateStatement struct {
	StartPos      model.Position
	EndPos        model.Position
	Table         []OperandRef
	SetTargets    []OperandRef
	WhereOperands []OperandRef
}

func (u *SQLUpdateStatement) Position() (model.Position, model.Position) {
	return u.StartPos, u.EndPos
}

// SQLDeleteStatement represents a DELETE statement in SQL form (distinct from Adabas DeleteStatement).
// Exposes unbound operand lists (name+range pairs); binding is deferred to feature 08b.
// Fields:
// - FromTable: FROM clause table operand (name+range)
// - WhereOperands: WHERE clause host-var operands (name+range)
type SQLDeleteStatement struct {
	StartPos      model.Position
	EndPos        model.Position
	FromTable     []OperandRef
	WhereOperands []OperandRef
}

func (d *SQLDeleteStatement) Position() (model.Position, model.Position) {
	return d.StartPos, d.EndPos
}

// MergeStatement represents a MERGE statement (SQL).
// Table holds the MERGE INTO target DDM operand(s); the merge body (USING /
// WHEN MATCHED / WHEN NOT MATCHED) is not modeled. The operand is unbound —
// cross-library resolution is the resolver's job.
type MergeStatement struct {
	StartPos model.Position
	EndPos   model.Position
	Table    []OperandRef // MERGE INTO target table (a DDM name)
}

func (m *MergeStatement) Position() (model.Position, model.Position) {
	return m.StartPos, m.EndPos
}

// CommitStatement represents a COMMIT statement (SQL transaction).
// Takes no operands.
type CommitStatement struct {
	StartPos model.Position
	EndPos   model.Position
}

func (c *CommitStatement) Position() (model.Position, model.Position) {
	return c.StartPos, c.EndPos
}

// RollbackStatement represents a ROLLBACK statement (SQL transaction).
// Takes no operands.
type RollbackStatement struct {
	StartPos model.Position
	EndPos   model.Position
}

func (r *RollbackStatement) Position() (model.Position, model.Position) {
	return r.StartPos, r.EndPos
}

// CallDBProcStatement represents a CALLDBPROC statement.
// Invokes a database procedure with operands.
// Procedure-name and remaining operand fields are added by the parser task (ES-5).
type CallDBProcStatement struct {
	StartPos model.Position
	EndPos   model.Position
	// ProcName is the stored-procedure name (the first operand after CALLDBPROC),
	// with surrounding quotes stripped for a literal. ProcNameIsLiteral records
	// whether it was written as a quoted literal (vs. an identifier/variable),
	// mirroring CallStatement.TargetIsLiteral so a dynamic proc name downgrades to
	// EdgeCallsDynamic. The target is unbound — resolution is the resolver's job.
	ProcName          string
	ProcNameRange     model.Range
	ProcNameIsLiteral bool
}

func (c *CallDBProcStatement) Position() (model.Position, model.Position) {
	return c.StartPos, c.EndPos
}

// ProcessSQLStatement represents a PROCESS SQL statement with an opaque body.
// The body is captured as raw text spanning the << >> delimiters (Option B modeled gap):
// the interior is NEVER tokenized or parsed. No host-variable list is extracted from the
// body — that extraction is deferred to feature 08b. This struct intentionally has no
// HostVars field; any host-var references appear verbatim inside Body.
type ProcessSQLStatement struct {
	StartPos     model.Position
	EndPos       model.Position
	DDMName      string      // the DDM name operand (UNBOUND — binding deferred to 08b)
	DDMNameRange model.Range // source range of the DDM name
	Body         string      // raw interior text of << >> (opaque, unparsed)
	BodyRange    model.Range // source range covering the << >> span
}

func (p *ProcessSQLStatement) Position() (model.Position, model.Position) {
	return p.StartPos, p.EndPos
}

// ReadResultSetStatement represents a READ RESULT SET statement with loop body (ES-4).
// Fetches rows from a result set (obtained from CALLDBPROC or similar) and iterates
// through them. Exposes:
// - ResultSetOperand: the result-set operand it reads (name+range pair, UNBOUND)
// - Body: []Node containing the statements executed per fetch
type ReadResultSetStatement struct {
	StartPos         model.Position
	EndPos           model.Position
	ResultSetOperand OperandRef
	// Body holds the statements executed on each fetch iteration.
	// All AST statement nodes implement Node, so the body is a heterogeneous,
	// traversable slice: any statement kind (CallStatement, PerformStatement, etc.)
	// may appear. This is the first loop-body representation in this codebase.
	Body []Node
}

func (r *ReadResultSetStatement) Position() (model.Position, model.Position) {
	return r.StartPos, r.EndPos
}

// WorkFileDefinition represents a DEFINE WORK FILE statement (FR-22).
//
// Fields:
//   - Number is the work-file slot number from the source (e.g. 1 in
//     "DEFINE WORK FILE 1 'REPORT.TXT'").  It is always a plain integer;
//     the parser emits a diagnostic and discards the node for non-integer
//     numbers (e.g. 1.5).
//   - Name is the file name as it appears in source.  For a string literal
//     the surrounding quotes are stripped (e.g. 'REPORT.TXT' → "REPORT.TXT").
//     For a variable the value is kept verbatim including any leading sigil
//     (e.g. "#DYNNAME").  A leading '#' signals a dynamic/unresolvable
//     reference — a modeled gap, not a diagnostic; extraction (Task 15) will
//     flag it as dynamic rather than binding it statically.
//   - NameRange is the inclusive source span of just the name token, suitable
//     for hover and go-to-definition targeting.
type WorkFileDefinition struct {
	StartPos  model.Position
	EndPos    model.Position
	Number    int
	Name      string      // quoted literal (quotes stripped) or variable (verbatim with sigil)
	NameRange model.Range // source span of the name token only
}

func (w *WorkFileDefinition) Position() (model.Position, model.Position) {
	return w.StartPos, w.EndPos
}
