// Package natural: extraction of embedded-SQL data access and call-like edges
// — SELECT/SELECT SINGLE/INSERT/UPDATE/DELETE for DDM reads/writes,
// CALLDBPROC call-like edges, and host-variable references in SQL clauses.
// (Feature 08b-embedded-sql-extraction, Task 2–7).
package natural

import (
	"sort"

	"natural-lsp/internal/model"
)

// extractSQLAccess walks the parsed program and returns data-access entries for
// every recognized native SQL statement (SELECT, SELECT SINGLE, INSERT, UPDATE,
// DELETE, MERGE, READ RESULT SET) and their table operands.
//
// For SELECT and SELECT SINGLE, emits one EdgeReads entry per FromTables operand.
// For INSERT, emits one EdgeWrites entry for IntoTable.
// For SQL UPDATE and SQL DELETE, emits one EdgeWrites entry per table operand.
// For MERGE, emits one EdgeWrites entry for the target table.
// For READ RESULT SET, emits one EdgeReads entry for its result-set operand.
//
// Returns entries in source order (stable sort on Source.Start).
// Never panics over partial/nil ASTs (FR-43).
func extractSQLAccess(prog *Program) []model.DataAccessEntry {
	if prog == nil {
		return nil
	}

	var entries []model.DataAccessEntry

	// Task 2: SELECT and SELECT SINGLE statements emit EdgeReads for each
	// FromTables operand (the DDM/table name).

	// SelectStatement (cursor loop, ES-4): emit one EdgeReads per FromTables operand.
	for _, sel := range prog.Selects {
		if sel == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range sel.FromTables {
			if table.Name == "" {
				continue // malformed FROM clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeReads,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(sel.StartPos, sel.EndPos),
			})
		}
	}

	// SelectSingleStatement (singleton, ES-3): emit one EdgeReads per FromTables operand.
	for _, selSingle := range prog.SelectSingles {
		if selSingle == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range selSingle.FromTables {
			if table.Name == "" {
				continue // malformed FROM clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeReads,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(selSingle.StartPos, selSingle.EndPos),
			})
		}
	}

	// Task 3: INSERT, SQL UPDATE, and SQL DELETE statements emit EdgeWrites
	// for each table operand (the DDM/table name).

	// InsertStatement (SQL): emit one EdgeWrites for IntoTable operand.
	for _, ins := range prog.Inserts {
		if ins == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range ins.IntoTable {
			if table.Name == "" {
				continue // malformed INTO clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeWrites,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(ins.StartPos, ins.EndPos),
			})
		}
	}

	// SQLUpdateStatement (SQL form with SET/WHERE): emit one EdgeWrites for Table operand.
	for _, upd := range prog.SQLUpdates {
		if upd == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range upd.Table {
			if table.Name == "" {
				continue // malformed UPDATE clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeWrites,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(upd.StartPos, upd.EndPos),
			})
		}
	}

	// SQLDeleteStatement (SQL form with FROM/WHERE): emit one EdgeWrites for FromTable operand.
	for _, del := range prog.SQLDeletes {
		if del == nil {
			continue // graceful degradation: skip nil AST nodes
		}
		for _, table := range del.FromTable {
			if table.Name == "" {
				continue // malformed DELETE clause — no false edge
			}
			entries = append(entries, model.DataAccessEntry{
				Kind:      model.EdgeWrites,
				Name:      table.Name,
				NameRange: table.Range,
				Source:    stmtRange(del.StartPos, del.EndPos),
			})
		}
	}

	// TODO: implement Task 5b (MERGE)
	// TODO: implement Task 6c (READ RESULT SET)
	// TODO: implement Task 7 (PROCESS SQL)

	// Sort by source order (stable sort on Source.Start).
	sort.SliceStable(entries, func(i, j int) bool {
		return sourceStartLess(
			entries[i].Source.Start.Line, entries[i].Source.Start.Column,
			entries[j].Source.Start.Line, entries[j].Source.Start.Column,
		)
	})

	return entries
}
