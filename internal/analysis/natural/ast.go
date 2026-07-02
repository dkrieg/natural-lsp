// Package natural implements a hand-written lexer and recursive-descent parser
// for Software AG Natural, following the reference implementation in natls.
//
// This file defines the AST node types for Natural constructs.
package natural

import "natural-lsp/internal/model"

// OperandRef represents a single operand reference (name + source range) within a statement.
// Used by SQL statement nodes to expose unbound operand lists (columns, table names,
// host-variable names, etc.). The reference is explicitly UNBOUND: no resolution or
// binding to a definition is performed at this layer. Binding is deferred to the
// extraction feature (feature 08b).
type OperandRef struct {
	Name  string      // the operand name (e.g., "COL1", "#VAR", "EMPLOYEES")
	Range model.Range // source range of the operand in the file
}

// Node is the base interface for all AST nodes.
type Node interface {
	Position() (model.Position, model.Position)
}

// Program is the root node of a Natural program AST.
type Program struct {
	StartPos     model.Position
	EndPos       model.Position
	Diagnostics  []model.Diagnostic
	Subroutines  []*Subroutine
	DataSections []*DataSection
	Includes     []*IncludeStatement
	Calls        []*CallStatement
	Fetches      []*FetchStatement
	Runs         []*RunStatement
	Performs     []*PerformStatement
	Maps         []*Map
	Reads        []*ReadStatement
	Stores       []*StoreStatement
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
}

func (p *Program) Position() (model.Position, model.Position) {
	return p.StartPos, p.EndPos
}

// Subroutine represents an inline subroutine definition.
type Subroutine struct {
	StartPos    model.Position
	EndPos      model.Position
	Name        string
	DataSection *DataSection
}

func (s *Subroutine) Position() (model.Position, model.Position) {
	return s.StartPos, s.EndPos
}

// DataSection represents a DEFINE DATA block.
type DataSection struct {
	StartPos model.Position
	EndPos   model.Position
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
	StartPos model.Position
	EndPos   model.Position
	Target   string
}

func (r *ReadStatement) Position() (model.Position, model.Position) {
	return r.StartPos, r.EndPos
}

// StoreStatement represents a STORE statement.
type StoreStatement struct {
	StartPos model.Position
	EndPos   model.Position
	Target   string
}

func (s *StoreStatement) Position() (model.Position, model.Position) {
	return s.StartPos, s.EndPos
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
// Table operand is unbound; MERGE grammar internals are not modeled.
// Table and operand fields are added by the parser task (ES-9).
type MergeStatement struct {
	StartPos model.Position
	EndPos   model.Position
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
