package natural

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParser_SQL_SelectStatement_StructuredLoop tests ES-6: parsing SELECT statements
// with loop body and END-SELECT terminator (structured mode).
// Fixture: 10-sql-select-loop.nsp
func TestParser_SQL_SelectStatement_StructuredLoop(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "10-sql-select-loop.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-6 acceptance: exactly one SelectStatement (cursor loop form, not singleton)
	if len(prog.Selects) != 1 {
		t.Fatalf("len(prog.Selects) = %d, want 1; got: %v", len(prog.Selects), prog.Selects)
	}
	sel := prog.Selects[0]

	// No SelectSingleStatement should be present (that's the 11-sql-select-single.nsp case)
	if len(prog.SelectSingles) != 0 {
		t.Errorf("len(prog.SelectSingles) = %d, want 0; SelectStatement and SelectSingleStatement are distinct", len(prog.SelectSingles))
	}

	// SelectStatement must have a populated Body with at least one child statement
	if len(sel.Body) < 1 {
		t.Errorf("SelectStatement.Body length = %d, want >= 1", len(sel.Body))
	}

	// Verify operand lists are populated
	if len(sel.Columns) == 0 {
		t.Errorf("SelectStatement.Columns is empty, want populated (COL1, COL2)")
	}
	if len(sel.IntoTargets) == 0 {
		t.Errorf("SelectStatement.IntoTargets is empty, want populated (#A, #B)")
	}
	if len(sel.FromTables) == 0 {
		t.Errorf("SelectStatement.FromTables is empty, want populated (EMPLOYEES)")
	}
	if len(sel.WhereOperands) == 0 {
		t.Errorf("SelectStatement.WhereOperands is empty, want populated (host-var operand)")
	}

	// ES-6 acceptance: both colon-less and colon-prefixed host-vars are accepted.
	// The fixture has #K (colon-less in data section) and :#K (colon-prefixed in WHERE).
	// Both forms must appear and be recognized. Stored form: without leading colon.
	//
	// INTO targets: #A, #B (colon-less forms from DEFINE DATA)
	foundA := false
	foundB := false
	for _, op := range sel.IntoTargets {
		if op.Name == "#A" {
			foundA = true
		}
		if op.Name == "#B" {
			foundB = true
		}
	}
	if !foundA {
		t.Errorf("INTO target #A not found in IntoTargets: %v", sel.IntoTargets)
	}
	if !foundB {
		t.Errorf("INTO target #B not found in IntoTargets: %v", sel.IntoTargets)
	}

	// WHERE operands must include the host-var (stored without leading colon, even if
	// the source had :#K). The name should be captured without the leading colon.
	foundWhereOperand := false
	for _, op := range sel.WhereOperands {
		if op.Name == "#K" {
			foundWhereOperand = true
			break
		}
	}
	if !foundWhereOperand {
		t.Errorf("WHERE host-var #K not found in WhereOperands: %v", sel.WhereOperands)
	}

	// Verify whole-statement positions span SELECT...END-SELECT
	// StartPos must be at SELECT keyword
	// EndPos must be at END-SELECT token
	if sel.StartPos.Line == 0 || sel.EndPos.Line == 0 {
		t.Errorf("SelectStatement positions invalid: StartPos=%v, EndPos=%v", sel.StartPos, sel.EndPos)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 10-sql-select-loop.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_SelectSingleStatement tests ES-6: parsing SELECT SINGLE statements
// (non-loop singleton form, no terminator, no body).
// Fixture: 11-sql-select-single.nsp
func TestParser_SQL_SelectSingleStatement(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "11-sql-select-single.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-6 acceptance: exactly one SelectSingleStatement (singleton form, not loop)
	if len(prog.SelectSingles) != 1 {
		t.Fatalf("len(prog.SelectSingles) = %d, want 1", len(prog.SelectSingles))
	}
	sel := prog.SelectSingles[0]

	// No SelectStatement (loop form) should be present
	if len(prog.Selects) != 0 {
		t.Errorf("len(prog.Selects) = %d, want 0; SelectSingleStatement is distinct from SelectStatement", len(prog.Selects))
	}

	// SelectSingleStatement must NOT have a body (no loop body)
	// The AST structure does not define a Body field on SelectSingleStatement for ES-6.
	// If a Body field exists, it must be nil or empty.
	// Since SelectSingleStatement has no Body field, we just verify that the node exists
	// without a body attribute.

	// Verify operand lists are populated
	if len(sel.Columns) == 0 {
		t.Errorf("SelectSingleStatement.Columns is empty, want populated (COL1)")
	}
	if len(sel.IntoTargets) == 0 {
		t.Errorf("SelectSingleStatement.IntoTargets is empty, want populated (#A)")
	}
	if len(sel.FromTables) == 0 {
		t.Errorf("SelectSingleStatement.FromTables is empty, want populated (EMPLOYEES)")
	}
	if len(sel.WhereOperands) == 0 {
		t.Errorf("SelectSingleStatement.WhereOperands is empty, want populated (host-var operand)")
	}

	// Verify positions
	if sel.StartPos.Line == 0 || sel.EndPos.Line == 0 {
		t.Errorf("SelectSingleStatement positions invalid: StartPos=%v, EndPos=%v", sel.StartPos, sel.EndPos)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 11-sql-select-single.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_SelectStatement_ReportingLoop tests ES-6: parsing SELECT statements
// with loop body and LOOP terminator (reporting mode).
// Fixture: 12-sql-select-loop-reporting.nsp
func TestParser_SQL_SelectStatement_ReportingLoop(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "12-sql-select-loop-reporting.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-6 acceptance: exactly one SelectStatement (loop form closed by LOOP)
	if len(prog.Selects) != 1 {
		t.Fatalf("len(prog.Selects) = %d, want 1", len(prog.Selects))
	}
	sel := prog.Selects[0]

	// No SelectSingleStatement should be present
	if len(prog.SelectSingles) != 0 {
		t.Errorf("len(prog.SelectSingles) = %d, want 0", len(prog.SelectSingles))
	}

	// SelectStatement must have a populated Body with at least one child statement
	if len(sel.Body) < 1 {
		t.Errorf("SelectStatement.Body length = %d, want >= 1", len(sel.Body))
	}

	// Verify operand lists are populated
	if len(sel.Columns) == 0 {
		t.Errorf("SelectStatement.Columns is empty, want populated")
	}
	if len(sel.IntoTargets) == 0 {
		t.Errorf("SelectStatement.IntoTargets is empty, want populated")
	}
	if len(sel.FromTables) == 0 {
		t.Errorf("SelectStatement.FromTables is empty, want populated")
	}

	// Verify positions
	if sel.StartPos.Line == 0 || sel.EndPos.Line == 0 {
		t.Errorf("SelectStatement positions invalid: StartPos=%v, EndPos=%v", sel.StartPos, sel.EndPos)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 12-sql-select-loop-reporting.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_HostVarColonOptional tests ES-6 open-question resolution #1:
// Both colon-prefixed (:#VAR) and colon-less (#VAR) host-var operands are accepted.
// This test specifically asserts that fixture 10 demonstrates both forms in use.
func TestParser_SQL_HostVarColonOptional(t *testing.T) {
	// Fixture 10 (structured loop) has both forms:
	// - INTO #A, #B (colon-less from DEFINE DATA)
	// - WHERE DEPT = :#K (colon-prefixed in WHERE clause)
	// Both must parse and be captured without raising diagnostics or errors.

	content, err := os.ReadFile(filepath.Join("testdata", "parser", "10-sql-select-loop.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(prog.Selects) != 1 {
		t.Fatalf("Expected 1 SelectStatement, got %d", len(prog.Selects))
	}
	sel := prog.Selects[0]

	// INTO targets (colon-less forms from DEFINE DATA declaration):
	// Should appear as #A, #B (without colon, since they're from DEFINE DATA)
	foundIntoColon := 0
	for _, op := range sel.IntoTargets {
		if op.Name == "#A" || op.Name == "#B" {
			foundIntoColon++
		}
	}
	if foundIntoColon < 2 {
		t.Errorf("Expected at least 2 colon-less INTO targets (#A, #B), found %d in: %v",
			foundIntoColon, sel.IntoTargets)
	}

	// WHERE operands (colon-prefixed in source, but stored without colon per design):
	// The source has :#K; parser must consume the colon and store the operand as #K
	foundWhereColon := 0
	for _, op := range sel.WhereOperands {
		if op.Name == "#K" {
			foundWhereColon++
		}
	}
	if foundWhereColon < 1 {
		t.Errorf("Expected WHERE operand #K (colon-prefixed in source as :#K, stored without colon), found in: %v",
			sel.WhereOperands)
	}

	// No diagnostics should be raised for colon acceptance
	if len(prog.Diagnostics) != 0 {
		t.Errorf("Colon handling produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_ReadResultSet tests ES-7: parsing READ RESULT SET statements
// with loop body and END-RESULT terminator (structured mode).
// Fixture: 13-sql-read-result-set.nsp
func TestParser_SQL_ReadResultSet(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "13-sql-read-result-set.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-7 acceptance: exactly one ReadResultSetStatement
	if len(prog.ReadResultSets) != 1 {
		t.Fatalf("len(prog.ReadResultSets) = %d, want 1", len(prog.ReadResultSets))
	}
	rrs := prog.ReadResultSets[0]

	// ResultSetOperand must be non-empty and correctly captured
	if rrs.ResultSetOperand.Name == "" {
		t.Errorf("ReadResultSetStatement.ResultSetOperand.Name is empty, want populated (e.g., #RS)")
	}

	// Body must contain at least one statement
	if len(rrs.Body) < 1 {
		t.Errorf("ReadResultSetStatement.Body length = %d, want >= 1", len(rrs.Body))
	}

	// Verify body contains an IncludeStatement (non-CALLNAT/PERFORM statement)
	// This tests unified body dispatch: any Node type in the loop body is accepted.
	foundInclude := false
	for _, stmt := range rrs.Body {
		if _, ok := stmt.(*IncludeStatement); ok {
			foundInclude = true
			break
		}
	}
	if !foundInclude {
		t.Errorf("ReadResultSetStatement.Body does not contain *IncludeStatement; types: %T", rrs.Body)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 13-sql-read-result-set.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_ReadResultSetLoop tests ES-7: parsing READ RESULT SET statements
// with loop body and LOOP terminator (reporting mode).
// Fixture: 14-sql-read-result-set-loop.nsp
func TestParser_SQL_ReadResultSetLoop(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "14-sql-read-result-set-loop.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-7 acceptance: exactly one ReadResultSetStatement (closed by LOOP)
	if len(prog.ReadResultSets) != 1 {
		t.Fatalf("len(prog.ReadResultSets) = %d, want 1", len(prog.ReadResultSets))
	}
	rrs := prog.ReadResultSets[0]

	// ResultSetOperand must be non-empty
	if rrs.ResultSetOperand.Name == "" {
		t.Errorf("ReadResultSetStatement.ResultSetOperand.Name is empty, want populated (e.g., #RS)")
	}

	// Body must contain at least one statement
	if len(rrs.Body) < 1 {
		t.Errorf("ReadResultSetStatement.Body length = %d, want >= 1", len(rrs.Body))
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 14-sql-read-result-set-loop.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_ReadResultSetRegressionAgainstPlainRead tests that plain READ statements
// (fixture 06-read-store.nsp) are NOT parsed as ReadResultSetStatement.
// Regression: READ without RESULT SET must go to Program.Reads, not Program.ReadResultSets.
func TestParser_ReadResultSetRegressionAgainstPlainRead(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "06-read-store.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// Plain READ statements must NOT appear in ReadResultSets
	if len(prog.ReadResultSets) != 0 {
		t.Errorf("fixture 06-read-store.nsp produced %d ReadResultSets, want 0", len(prog.ReadResultSets))
	}

	// Plain READ statements MUST appear in Reads
	if len(prog.Reads) < 1 {
		t.Errorf("fixture 06-read-store.nsp produced %d Reads, want >= 2 (READ EMPLOYEES, READ (10) DEPARTMENTS)", len(prog.Reads))
	}
}

// TestParser_SQL_ProcessSQL tests ES-8: parsing PROCESS SQL statements
// with opaque multi-line body (<<...>>) containing unparsed SQL and host-vars.
// Fixture: 15-sql-process-sql.nsp
func TestParser_SQL_ProcessSQL(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "15-sql-process-sql.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-8 acceptance: exactly one ProcessSQLStatement
	if len(prog.ProcessSQLs) != 1 {
		t.Fatalf("len(prog.ProcessSQLs) = %d, want 1", len(prog.ProcessSQLs))
	}
	ps := prog.ProcessSQLs[0]

	// DDMName must be "DDMNAME"
	if ps.DDMName != "DDMNAME" {
		t.Errorf("ProcessSQLStatement.DDMName = %q, want %q", ps.DDMName, "DDMNAME")
	}

	// Body must contain the verbatim multi-line interior text INCLUDING the literal
	// :#PERS-ID substring (assert the substring is present) — i.e. it was captured,
	// not parsed into any structured field.
	if !strings.Contains(ps.Body, ":#PERS-ID") {
		t.Errorf("ProcessSQLStatement.Body does not contain literal ':#PERS-ID'; got: %q", ps.Body)
	}

	// BodyRange and statement positions must be set (non-zero) and span across
	// multiple lines
	if ps.BodyRange.Start.Line == 0 || ps.BodyRange.End.Line == 0 {
		t.Errorf("ProcessSQLStatement.BodyRange positions invalid: Start=%v, End=%v",
			ps.BodyRange.Start, ps.BodyRange.End)
	}
	if ps.BodyRange.Start.Line == ps.BodyRange.End.Line {
		t.Errorf("ProcessSQLStatement.BodyRange does not span multiple lines: Start line=%d, End line=%d",
			ps.BodyRange.Start.Line, ps.BodyRange.End.Line)
	}

	// StartPos and EndPos must be set
	if ps.StartPos.Line == 0 || ps.EndPos.Line == 0 {
		t.Errorf("ProcessSQLStatement positions invalid: StartPos=%v, EndPos=%v",
			ps.StartPos, ps.EndPos)
	}

	// Trailing CALLNAT 'NDBERR' must still parse to a *CallStatement (no gap after
	// the opaque block)
	if len(prog.Calls) < 1 {
		t.Errorf("prog.Calls length = %d, want >= 1 (trailing CALLNAT 'NDBERR' must parse)", len(prog.Calls))
	}
	foundNdberr := false
	for _, call := range prog.Calls {
		if call.Target == "NDBERR" {
			foundNdberr = true
			break
		}
	}
	if !foundNdberr {
		t.Errorf("trailing CALLNAT 'NDBERR' not found in prog.Calls; targets: %v",
			func() []string {
				var targets []string
				for _, c := range prog.Calls {
					targets = append(targets, c.Target)
				}
				return targets
			}())
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 15-sql-process-sql.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_Insert tests ES-9: parsing INSERT statements (SQL DML).
// Fixture: 16-sql-insert.nsp
func TestParser_SQL_Insert(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "16-sql-insert.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-9 acceptance: exactly one InsertStatement
	if len(prog.Inserts) != 1 {
		t.Fatalf("len(prog.Inserts) = %d, want 1", len(prog.Inserts))
	}
	ins := prog.Inserts[0]

	// IntoTable must be populated with table operand (DDMNAME)
	if len(ins.IntoTable) == 0 {
		t.Errorf("InsertStatement.IntoTable is empty, want populated (DDMNAME)")
	} else {
		foundTable := false
		for _, op := range ins.IntoTable {
			if op.Name == "DDMNAME" {
				foundTable = true
				break
			}
		}
		if !foundTable {
			t.Errorf("DDMNAME table operand not found in IntoTable: %v", ins.IntoTable)
		}
	}

	// Values must be populated with column and host-var operands
	if len(ins.Values) == 0 {
		t.Errorf("InsertStatement.Values is empty, want populated (COL1, COL2, #A, #B)")
	}

	// Verify positions are set
	if ins.StartPos.Line == 0 || ins.EndPos.Line == 0 {
		t.Errorf("InsertStatement positions invalid: StartPos=%v, EndPos=%v", ins.StartPos, ins.EndPos)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 16-sql-insert.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_UpdateDelete tests ES-9: parsing UPDATE and DELETE statements (SQL DML).
// Fixture: 17-sql-update-delete.nsp
func TestParser_SQL_UpdateDelete(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "17-sql-update-delete.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-9 acceptance: exactly one SQLUpdateStatement
	if len(prog.SQLUpdates) != 1 {
		t.Fatalf("len(prog.SQLUpdates) = %d, want 1", len(prog.SQLUpdates))
	}
	upd := prog.SQLUpdates[0]

	// Table must be populated (DDMNAME)
	if len(upd.Table) == 0 {
		t.Errorf("SQLUpdateStatement.Table is empty, want populated (DDMNAME)")
	} else {
		foundTable := false
		for _, op := range upd.Table {
			if op.Name == "DDMNAME" {
				foundTable = true
				break
			}
		}
		if !foundTable {
			t.Errorf("DDMNAME table operand not found in Table: %v", upd.Table)
		}
	}

	// SetTargets must be populated
	if len(upd.SetTargets) == 0 {
		t.Errorf("SQLUpdateStatement.SetTargets is empty, want populated (COL, #A)")
	}

	// WhereOperands must be populated
	if len(upd.WhereOperands) == 0 {
		t.Errorf("SQLUpdateStatement.WhereOperands is empty, want populated (KEY, #B)")
	}

	// Verify UPDATE positions are set
	if upd.StartPos.Line == 0 || upd.EndPos.Line == 0 {
		t.Errorf("SQLUpdateStatement positions invalid: StartPos=%v, EndPos=%v", upd.StartPos, upd.EndPos)
	}

	// ES-9 acceptance: exactly one SQLDeleteStatement
	if len(prog.SQLDeletes) != 1 {
		t.Fatalf("len(prog.SQLDeletes) = %d, want 1", len(prog.SQLDeletes))
	}
	del := prog.SQLDeletes[0]

	// FromTable must be populated (DDMNAME)
	if len(del.FromTable) == 0 {
		t.Errorf("SQLDeleteStatement.FromTable is empty, want populated (DDMNAME)")
	} else {
		foundTable := false
		for _, op := range del.FromTable {
			if op.Name == "DDMNAME" {
				foundTable = true
				break
			}
		}
		if !foundTable {
			t.Errorf("DDMNAME table operand not found in FromTable: %v", del.FromTable)
		}
	}

	// WhereOperands must be populated
	if len(del.WhereOperands) == 0 {
		t.Errorf("SQLDeleteStatement.WhereOperands is empty, want populated (KEY, #B)")
	}

	// Verify DELETE positions are set
	if del.StartPos.Line == 0 || del.EndPos.Line == 0 {
		t.Errorf("SQLDeleteStatement positions invalid: StartPos=%v, EndPos=%v", del.StartPos, del.EndPos)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 17-sql-update-delete.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_Merge tests ES-9: parsing MERGE statements (SQL DML).
// Fixture: 18-sql-merge.nsp
func TestParser_SQL_Merge(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "18-sql-merge.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// ES-9 acceptance: exactly one MergeStatement
	if len(prog.Merges) != 1 {
		t.Fatalf("len(prog.Merges) = %d, want 1", len(prog.Merges))
	}
	merge := prog.Merges[0]

	// Verify positions are set
	if merge.StartPos.Line == 0 || merge.EndPos.Line == 0 {
		t.Errorf("MergeStatement positions invalid: StartPos=%v, EndPos=%v", merge.StartPos, merge.EndPos)
	}

	// Task 5a: the MERGE INTO target table operand is captured.
	if len(merge.Table) != 1 {
		t.Fatalf("len(merge.Table) = %d, want 1 (MERGE INTO target)", len(merge.Table))
	}
	if merge.Table[0].Name != "EMPLOYEES" {
		t.Errorf("merge.Table[0].Name = %q, want %q", merge.Table[0].Name, "EMPLOYEES")
	}
	if merge.Table[0].Range.Start == merge.Table[0].Range.End {
		t.Error("merge.Table[0].Range is zero, want non-zero range on the table token")
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 18-sql-merge.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_Regression_ReadStore_Unchanged tests that fixture 06-read-store.nsp
// (Adabas READ/STORE) continues to parse correctly when SQL DML parsing is added.
// Regression: INSERT/UPDATE/DELETE/MERGE parsing must not affect READ/STORE.
func TestParser_SQL_Regression_ReadStore_Unchanged(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "06-read-store.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// No SQL DML nodes should be produced from this fixture
	if len(prog.Inserts) != 0 {
		t.Errorf("fixture 06-read-store.nsp produced %d Inserts, want 0", len(prog.Inserts))
	}
	if len(prog.SQLUpdates) != 0 {
		t.Errorf("fixture 06-read-store.nsp produced %d SQLUpdates, want 0", len(prog.SQLUpdates))
	}
	if len(prog.SQLDeletes) != 0 {
		t.Errorf("fixture 06-read-store.nsp produced %d SQLDeletes, want 0", len(prog.SQLDeletes))
	}
	if len(prog.Merges) != 0 {
		t.Errorf("fixture 06-read-store.nsp produced %d Merges, want 0", len(prog.Merges))
	}

	// READ and STORE statements must still be present and unchanged
	expectedReads := 2 // READ EMPLOYEES BY NAME and READ (10) DEPARTMENTS
	if len(prog.Reads) < expectedReads {
		t.Errorf("fixture 06-read-store.nsp produced %d Reads, want at least %d", len(prog.Reads), expectedReads)
	}

	expectedStores := 2 // STORE RECORD IN EMPLOYEES and STORE PERSONNEL
	if len(prog.Stores) < expectedStores {
		t.Errorf("fixture 06-read-store.nsp produced %d Stores, want at least %d", len(prog.Stores), expectedStores)
	}

	// Fixture must parse with zero diagnostics
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 06-read-store.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}

// TestParser_SQL_ParseErrors tests ES-10: malformed embedded-SQL statements
// emit diagnostics, and valid statements surrounding them are still retained
// (FR-30, FR-43, M-6).
// Fixture: 19-sql-parse-errors.nsp — contains an unterminated SELECT loop
// and an unterminated PROCESS SQL << >>, followed by a valid CALLNAT.
func TestParser_SQL_ParseErrors(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "19-sql-parse-errors.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}
	if err != nil {
		t.Errorf("Parse returned error %v; malformed input must surface as diagnostics, not errors", err)
	}

	// ES-10 acceptance: malformed SQL must emit at least one diagnostic (FR-30)
	if len(prog.Diagnostics) < 1 {
		t.Errorf("prog.Diagnostics count: got %d, want >= 1 for malformed SQL statements", len(prog.Diagnostics))
	} else {
		// Verify that at least one diagnostic has a sensible (non-zero) range
		foundValidRange := false
		for _, diag := range prog.Diagnostics {
			if diag.Range.Start.Line > 0 && diag.Range.End.Line > 0 {
				foundValidRange = true
				break
			}
		}
		if !foundValidRange {
			t.Errorf("No diagnostic has a valid range; diagnostics: %v", prog.Diagnostics)
		}
	}

	// ES-10 acceptance (M-6): trailing valid CALLNAT must be retained despite malformed SQL
	// before it. Search for a CALLNAT with target 'ERROR-HANDLER'.
	foundErrorHandler := false
	for _, call := range prog.Calls {
		if call.Target == "ERROR-HANDLER" {
			foundErrorHandler = true
			break
		}
	}
	if !foundErrorHandler {
		var targets []string
		for _, call := range prog.Calls {
			targets = append(targets, call.Target)
		}
		t.Errorf("trailing valid CALLNAT 'ERROR-HANDLER' not retained after malformed SQL; prog.Calls targets = %v", targets)
	}
}

// TestParser_SQL_UnterminatedConstructs covers the run-to-EOF malformed cases
// (ES-10, FR-30/FR-43): an unterminated SELECT loop and an unterminated PROCESS
// SQL << opaque span. Each must emit at least one diagnostic (not a silent gap)
// and must terminate — the parser must never hang or panic. Top-level retention
// is NOT asserted here: an unterminated construct legitimately consumes to EOF.
func TestParser_SQL_UnterminatedConstructs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "unterminated_select_loop",
			// SELECT with no END-SELECT/LOOP before EOF.
			src: "DEFINE DATA LOCAL END-DEFINE\nSELECT COL INTO #V FROM EMPLOYEES WHERE ID = #K\n  PERFORM 'DO-IT'\nEND\n",
		},
		{
			name: "unterminated_process_sql_span",
			// PROCESS SQL whose << opaque body is never closed by >>.
			src: "DEFINE DATA LOCAL END-DEFINE\nPROCESS SQL PAYROLL <<\n  UPDATE T SET C = :#A WHERE K = :#B\nEND\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := NewParser(NewLexer(tc.src)).Parse()
			if err != nil {
				t.Errorf("Parse returned error %v; malformed input must surface as diagnostics, not errors", err)
			}
			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}
			if len(prog.Diagnostics) < 1 {
				t.Errorf("got %d diagnostics, want >= 1 for %s (must not be a silent gap)", len(prog.Diagnostics), tc.name)
			}
			// Tighten assertion: at least one diagnostic must have a non-zero range
			foundValidRange := false
			for _, diag := range prog.Diagnostics {
				if diag.Range.Start.Line > 0 && diag.Range.End.Line > 0 {
					foundValidRange = true
					break
				}
			}
			if !foundValidRange {
				t.Errorf("No diagnostic has a valid range; diagnostics: %v", prog.Diagnostics)
			}
		})
	}
}

// TestParser_SQL_ClauseKeywords_NoHang tests that parseSelectWhere does NOT hang
// when parsing SELECT statements with WHERE clause followed by GROUP BY, ORDER BY,
// or HAVING keywords. This addresses a blocker-level hang in clause keyword handling.
// Test is guarded with a timeout per-case to prevent hanging the entire test run.
func TestParser_SQL_ClauseKeywords_NoHang(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "select_with_group_by",
			src: `DEFINE DATA LOCAL
  1 #V (A10)
  1 #X (N5)
END-DEFINE
SELECT COL INTO #V FROM T WHERE X = 1 GROUP BY COL
END-SELECT
END
`,
		},
		{
			name: "select_with_order_by",
			src: `DEFINE DATA LOCAL
  1 #V (A10)
END-DEFINE
SELECT COL INTO #V FROM T WHERE X = 1 ORDER BY COL
END-SELECT
END
`,
		},
		{
			name: "select_with_having",
			src: `DEFINE DATA LOCAL
  1 #V (A10)
END-DEFINE
SELECT COL INTO #V FROM T WHERE X = 1 GROUP BY COL HAVING COUNT(*) > 1
END-SELECT
END
`,
		},
		{
			name: "select_single_with_where_group",
			src: `DEFINE DATA LOCAL
  1 #V (A10)
END-DEFINE
SELECT SINGLE COL INTO #V FROM T WHERE X = 1 GROUP BY COL
END
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Guard with a timeout per-case to ensure no hang blocks the test
			done := make(chan struct{})
			var prog *Program
			var parseErr error

			go func() {
				prog, parseErr = NewParser(NewLexer(tc.src)).Parse()
				close(done)
			}()

			select {
			case <-done:
				// Parse completed in time
			case <-time.After(2 * time.Second):
				t.Fatalf("parser hung on case %s: parse did not complete within 2 seconds", tc.name)
			}

			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}
			if parseErr != nil {
				t.Logf("Parse returned error (ok for recovery): %v", parseErr)
			}

			// Assert exactly one Select or SelectSingle node (depending on case)
			totalSelects := len(prog.Selects) + len(prog.SelectSingles)
			if totalSelects != 1 {
				t.Fatalf("Expected 1 Select/SelectSingle, got %d selects + %d singles",
					len(prog.Selects), len(prog.SelectSingles))
			}

			// Verify WHERE operands do NOT include clause keywords
			if len(prog.Selects) > 0 {
				sel := prog.Selects[0]
				forbiddenNames := map[string]bool{"GROUP": true, "ORDER": true, "HAVING": true, "COL": true, "COUNT": true}
				for _, op := range sel.WhereOperands {
					if forbiddenNames[op.Name] {
						t.Errorf("WhereOperands includes forbidden keyword %q (should be filtered)", op.Name)
					}
				}
				// Verify actual WHERE operand (X) is present
				foundX := false
				for _, op := range sel.WhereOperands {
					if op.Name == "X" {
						foundX = true
						break
					}
				}
				if !foundX {
					t.Errorf("WHERE operand X not found in WhereOperands: %v", sel.WhereOperands)
				}
			} else {
				sel := prog.SelectSingles[0]
				forbiddenNames := map[string]bool{"GROUP": true, "ORDER": true, "HAVING": true, "COL": true, "COUNT": true}
				for _, op := range sel.WhereOperands {
					if forbiddenNames[op.Name] {
						t.Errorf("WhereOperands includes forbidden keyword %q (should be filtered)", op.Name)
					}
				}
				// Verify actual WHERE operand (X) is present
				foundX := false
				for _, op := range sel.WhereOperands {
					if op.Name == "X" {
						foundX = true
						break
					}
				}
				if !foundX {
					t.Errorf("WHERE operand X not found in WhereOperands: %v", sel.WhereOperands)
				}
			}
		})
	}
}

// TestParser_SQL_OperandsBoundedToStatement tests that operands are not "leaking" into
// statements from clause keywords (GROUP BY, ORDER BY, HAVING, END). This addresses spurious
// operand capture and out-of-bounds range issues.
// Fixtures: 11-sql-select-single.nsp and 17-sql-update-delete.nsp
func TestParser_SQL_OperandsBoundedToStatement(t *testing.T) {
	cases := []struct {
		name           string
		path           string
		assertSelector func(*Program) [][]OperandRef // returns all operand slices to check
	}{
		{
			name: "fixture_11_select_single",
			path: filepath.Join("testdata", "parser", "11-sql-select-single.nsp"),
			assertSelector: func(prog *Program) [][]OperandRef {
				if len(prog.SelectSingles) == 0 {
					return nil
				}
				sel := prog.SelectSingles[0]
				return [][]OperandRef{sel.Columns, sel.IntoTargets, sel.FromTables, sel.WhereOperands}
			},
		},
		{
			name: "fixture_17_update_delete",
			path: filepath.Join("testdata", "parser", "17-sql-update-delete.nsp"),
			assertSelector: func(prog *Program) [][]OperandRef {
				var allOps [][]OperandRef
				// Collect operands from UPDATE
				if len(prog.SQLUpdates) > 0 {
					upd := prog.SQLUpdates[0]
					allOps = append(allOps, upd.Table, upd.SetTargets, upd.WhereOperands)
				}
				// Collect operands from DELETE
				if len(prog.SQLDeletes) > 0 {
					del := prog.SQLDeletes[0]
					allOps = append(allOps, del.FromTable, del.WhereOperands)
				}
				return allOps
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("Failed to read fixture %s: %v", tc.path, err)
			}

			prog, parseErr := NewParser(NewLexer(string(content))).Parse()
			_ = parseErr
			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}

			operandSlices := tc.assertSelector(prog)
			if len(operandSlices) == 0 {
				t.Skip("No operand slices to check for this fixture")
			}

			// Assert no operand has Name == "END"
			for sliceIdx, slice := range operandSlices {
				for opIdx, op := range slice {
					if op.Name == "END" {
						t.Errorf("Operand slice[%d][%d] has spurious Name=END at line %d (should not leak from terminator)",
							sliceIdx, opIdx, op.Range.Start.Line)
					}
				}
			}

			// Assert every operand's range falls within the statement's line range
			// For fixture 11: SELECT SINGLE is on line 13, END is on line 15
			// For fixture 17: UPDATE is on line 13, DELETE is on line 15, final END is on line 17
			checkOperandRanges := func(stmt interface{}, stmtStartLine, stmtEndLine int, stmtName string) {
				var operandSlices [][]OperandRef
				switch s := stmt.(type) {
				case *SelectSingleStatement:
					operandSlices = [][]OperandRef{s.Columns, s.IntoTargets, s.FromTables, s.WhereOperands}
				case *SQLUpdateStatement:
					operandSlices = [][]OperandRef{s.Table, s.SetTargets, s.WhereOperands}
				case *SQLDeleteStatement:
					operandSlices = [][]OperandRef{s.FromTable, s.WhereOperands}
				}

				for sliceIdx, slice := range operandSlices {
					for opIdx, op := range slice {
						if op.Range.Start.Line < stmtStartLine || op.Range.Start.Line > stmtEndLine {
							t.Errorf("%s operand slice[%d][%d] has out-of-bounds line %d (statement spans lines %d–%d)",
								stmtName, sliceIdx, opIdx, op.Range.Start.Line, stmtStartLine, stmtEndLine)
						}
					}
				}
			}

			// Check ranges for fixture 11
			if len(prog.SelectSingles) > 0 {
				sel := prog.SelectSingles[0]
				checkOperandRanges(sel, 13, 13, "SelectSingle")
			}

			// Check ranges for fixture 17
			if len(prog.SQLUpdates) > 0 {
				upd := prog.SQLUpdates[0]
				checkOperandRanges(upd, 13, 13, "SQLUpdate")
			}
			if len(prog.SQLDeletes) > 0 {
				del := prog.SQLDeletes[0]
				checkOperandRanges(del, 15, 15, "SQLDelete")
			}
		})
	}
}

// TestParser_SQL_CallDBProc_EndPosBounded tests that CallDBProcStatement.EndPos
// does not extend beyond its own statement line (no leakage to next statement).
// Fixture: 09-sql-txn-calldbproc.nsp
func TestParser_SQL_CallDBProc_EndPosBounded(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "09-sql-txn-calldbproc.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	prog, err := NewParser(NewLexer(string(content))).Parse()
	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}

	if len(prog.CallDBProcs) == 0 {
		t.Fatalf("No CallDBProcStatement found in fixture")
	}

	calldbproc := prog.CallDBProcs[0]
	// Fixture has CALLDBPROC on line 13 (counting from 1)
	if calldbproc.StartPos.Line != calldbproc.EndPos.Line {
		t.Errorf("CallDBProcStatement.EndPos.Line = %d, want same as StartPos.Line = %d",
			calldbproc.EndPos.Line, calldbproc.StartPos.Line)
	}
}

// TestParser_SQL_TxnStatementRanges tests that CommitStatement and RollbackStatement
// EndPos.Column properly covers the keyword, not just collapsed to StartPos.Column.
// Fixture: 09-sql-txn-calldbproc.nsp
func TestParser_SQL_TxnStatementRanges(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "09-sql-txn-calldbproc.nsp"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	prog, err := NewParser(NewLexer(string(content))).Parse()
	if prog == nil {
		t.Fatal("Parser returned nil AST")
	}

	// Check COMMIT (line 9, keyword is 6 characters: COMMIT)
	if len(prog.Commits) == 0 {
		t.Fatalf("No CommitStatement found in fixture")
	}
	commit := prog.Commits[0]
	expectedEndCol := commit.StartPos.Column + len("COMMIT") - 1
	if commit.EndPos.Column < expectedEndCol {
		t.Errorf("CommitStatement.EndPos.Column = %d, want >= %d (covers keyword %q)",
			commit.EndPos.Column, expectedEndCol, "COMMIT")
	}

	// Check ROLLBACK (line 11, keyword is 8 characters: ROLLBACK)
	if len(prog.Rollbacks) == 0 {
		t.Fatalf("No RollbackStatement found in fixture")
	}
	rollback := prog.Rollbacks[0]
	expectedEndCol = rollback.StartPos.Column + len("ROLLBACK") - 1
	if rollback.EndPos.Column < expectedEndCol {
		t.Errorf("RollbackStatement.EndPos.Column = %d, want >= %d (covers keyword %q)",
			rollback.EndPos.Column, expectedEndCol, "ROLLBACK")
	}
}

// TestParser_ProcessSQL_MalformedEmitsDiagnostic asserts that each malformed
// PROCESS SQL shape emits at least one diagnostic (FR-30/M-6 — no silent drop).
// The three shapes covered are:
//   - PROCESS at EOF (no SQL keyword following)
//   - PROCESS SQL with DDM name missing (SQL keyword at end of line, nothing after)
//   - PROCESS SQL followed by a non-identifier (lone '<' before '<<')
func TestParser_ProcessSQL_MalformedEmitsDiagnostic(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "process_at_eof",
			// PROCESS with nothing following — EOF immediately after.
			src: "PROCESS",
		},
		{
			name: "process_sql_no_ddm",
			// PROCESS SQL with no DDM name on the same line (newline after SQL).
			src: "PROCESS SQL\nCALLNAT 'NEXT'\n",
		},
		{
			name: "process_sql_non_identifier",
			// PROCESS SQL followed by a lone '<' (not a valid DDM identifier).
			src: "PROCESS SQL <\nCALLNAT 'NEXT'\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := NewParser(NewLexer(tc.src)).Parse()
			if err != nil {
				t.Logf("Parse returned error (acceptable): %v", err)
			}
			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}
			// FR-30/M-6: malformed PROCESS SQL must emit at least one diagnostic, not silently drop.
			if len(prog.Diagnostics) < 1 {
				t.Errorf("got %d diagnostics, want >= 1 for malformed PROCESS SQL shape %q", len(prog.Diagnostics), tc.name)
			}
		})
	}
}

// TestParser_SQL_AdabasFormNotSQL tests that Adabas-form UPDATE (with no SET)
// and Adabas-form DELETE (with no FROM) are NOT parsed as SQL UPDATE/DELETE.
// These record forms should be rejected and result in zero SQL node counts.
func TestParser_SQL_AdabasFormNotSQL(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "adabas_update_no_set",
			src: `DEFINE DATA LOCAL
  1 #VAR (N5)
END-DEFINE
UPDATE (0010)
END
`,
		},
		{
			name: "adabas_delete_no_from",
			src: `DEFINE DATA LOCAL
  1 #VAR (N5)
END-DEFINE
DELETE
END
`,
		},
		{
			name: "bare_update",
			src: `DEFINE DATA LOCAL
  1 #VAR (N5)
END-DEFINE
UPDATE
END
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, _ := NewParser(NewLexer(tc.src)).Parse()
			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}

			// These Adabas forms should NOT be parsed as SQL UPDATE/DELETE
			if len(prog.SQLUpdates) != 0 {
				t.Errorf("Adabas-form UPDATE incorrectly parsed as SQLUpdate: got %d, want 0", len(prog.SQLUpdates))
			}
			if len(prog.SQLDeletes) != 0 {
				t.Errorf("Adabas-form DELETE incorrectly parsed as SQLDelete: got %d, want 0", len(prog.SQLDeletes))
			}
		})
	}
}
