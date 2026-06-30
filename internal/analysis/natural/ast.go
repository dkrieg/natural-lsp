// Package natural implements a hand-written lexer and recursive-descent parser
// for Software AG Natural, following the reference implementation in natls.
//
// This file defines the AST node types for Natural constructs.
package natural

import "natural-lsp/internal/model"

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
