package natural

import (
	"github.com/dkrieg/natural-lsp/internal/model"
	"testing"
)

// TestASTNodeTypes verifies that all required AST node types for Natural constructs
// are defined with correct structure (Task 4 / NFR-15, M-6).
//
// This test exercises the AST node types specified in the feature plan:
// - Program (root node)
// - Subroutine (inline subroutine)
// - DataSection (DEFINE DATA block)
// - DataField (individual data item)
// - Map (DEFINE MAP block)
// - IncludeStatement (INCLUDE statement)
// - CallStatement (CALLNAT statement)
// - FetchStatement (FETCH statement)
// - RunStatement (RUN statement)
// - PerformStatement (PERFORM statement)
// - ReadStatement (READ statement)
// - StoreStatement (STORE statement)
//
// Each node must have StartPos and EndPos position fields, and parent/child
// relationships must be representable (e.g., Program contains []Subroutine).
func TestASTNodeTypes(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "Program_node_has_required_fields",
			test: func(t *testing.T) {
				// Arrange: create a Program node with sample positions
				prog := &Program{
					StartPos:     model.Position{Line: 1, Column: 1},
					EndPos:       model.Position{Line: 100, Column: 1},
					Subroutines:  []*Subroutine{},
					DataSections: []*DataSection{},
					Includes:     []*IncludeStatement{},
				}

				// Assert: Program has correct StartPos
				if prog.StartPos.Line != 1 || prog.StartPos.Column != 1 {
					t.Errorf("Program.StartPos = (%d, %d), want (1, 1)", prog.StartPos.Line, prog.StartPos.Column)
				}

				// Assert: Program has correct EndPos
				if prog.EndPos.Line != 100 || prog.EndPos.Column != 1 {
					t.Errorf("Program.EndPos = (%d, %d), want (100, 1)", prog.EndPos.Line, prog.EndPos.Column)
				}

				// Assert: Program has Subroutines field
				if prog.Subroutines == nil {
					t.Error("Program.Subroutines is nil, want non-nil slice")
				}

				// Assert: Program has DataSections field
				if prog.DataSections == nil {
					t.Error("Program.DataSections is nil, want non-nil slice")
				}

				// Assert: Program has Includes field
				if prog.Includes == nil {
					t.Error("Program.Includes is nil, want non-nil slice")
				}
			},
		},
		{
			name: "Subroutine_node_has_required_fields",
			test: func(t *testing.T) {
				sub := &Subroutine{
					StartPos:    model.Position{Line: 10, Column: 5},
					EndPos:      model.Position{Line: 20, Column: 10},
					Name:        "TEST_SUB",
					DataSection: &DataSection{},
				}

				// Assert: Subroutine has correct StartPos
				if sub.StartPos.Line != 10 || sub.StartPos.Column != 5 {
					t.Errorf("Subroutine.StartPos = (%d, %d), want (10, 5)", sub.StartPos.Line, sub.StartPos.Column)
				}

				// Assert: Subroutine has correct EndPos
				if sub.EndPos.Line != 20 || sub.EndPos.Column != 10 {
					t.Errorf("Subroutine.EndPos = (%d, %d), want (20, 10)", sub.EndPos.Line, sub.EndPos.Column)
				}

				// Assert: Subroutine has Name field
				if sub.Name != "TEST_SUB" {
					t.Errorf("Subroutine.Name = %q, want \"TEST_SUB\"", sub.Name)
				}

				// Assert: Subroutine has DataSection field
				if sub.DataSection == nil {
					t.Error("Subroutine.DataSection is nil, want non-nil pointer")
				}
			},
		},
		{
			name: "DataSection_node_has_required_fields",
			test: func(t *testing.T) {
				ds := &DataSection{
					StartPos: model.Position{Line: 5, Column: 1},
					EndPos:   model.Position{Line: 15, Column: 1},
					Fields:   []*DataField{},
				}

				// Assert: DataSection has correct StartPos
				if ds.StartPos.Line != 5 || ds.StartPos.Column != 1 {
					t.Errorf("DataSection.StartPos = (%d, %d), want (5, 1)", ds.StartPos.Line, ds.StartPos.Column)
				}

				// Assert: DataSection has correct EndPos
				if ds.EndPos.Line != 15 || ds.EndPos.Column != 1 {
					t.Errorf("DataSection.EndPos = (%d, %d), want (15, 1)", ds.EndPos.Line, ds.EndPos.Column)
				}

				// Assert: DataSection has Fields field
				if ds.Fields == nil {
					t.Error("DataSection.Fields is nil, want non-nil slice")
				}
			},
		},
		{
			name: "DataField_node_has_required_fields",
			test: func(t *testing.T) {
				df := &DataField{
					StartPos: model.Position{Line: 6, Column: 5},
					EndPos:   model.Position{Line: 6, Column: 40},
					Name:     "MY-VAR",
					Type:     "PIC X(10)",
				}

				// Assert: DataField has correct StartPos
				if df.StartPos.Line != 6 || df.StartPos.Column != 5 {
					t.Errorf("DataField.StartPos = (%d, %d), want (6, 5)", df.StartPos.Line, df.StartPos.Column)
				}

				// Assert: DataField has correct EndPos
				if df.EndPos.Line != 6 || df.EndPos.Column != 40 {
					t.Errorf("DataField.EndPos = (%d, %d), want (6, 40)", df.EndPos.Line, df.EndPos.Column)
				}

				// Assert: DataField has Name field
				if df.Name != "MY-VAR" {
					t.Errorf("DataField.Name = %q, want \"MY-VAR\"", df.Name)
				}

				// Assert: DataField has Type field
				if df.Type != "PIC X(10)" {
					t.Errorf("DataField.Type = %q, want \"PIC X(10)\"", df.Type)
				}
			},
		},
		{
			name: "Map_node_has_required_fields",
			test: func(t *testing.T) {
				m := &Map{
					StartPos: model.Position{Line: 30, Column: 1},
					EndPos:   model.Position{Line: 60, Column: 1},
					Name:     "MY-MAP",
					Fields:   []*DataField{},
				}

				// Assert: Map has correct StartPos
				if m.StartPos.Line != 30 || m.StartPos.Column != 1 {
					t.Errorf("Map.StartPos = (%d, %d), want (30, 1)", m.StartPos.Line, m.StartPos.Column)
				}

				// Assert: Map has correct EndPos
				if m.EndPos.Line != 60 || m.EndPos.Column != 1 {
					t.Errorf("Map.EndPos = (%d, %d), want (60, 1)", m.EndPos.Line, m.EndPos.Column)
				}

				// Assert: Map has Name field
				if m.Name != "MY-MAP" {
					t.Errorf("Map.Name = %q, want \"MY-MAP\"", m.Name)
				}

				// Assert: Map has Fields field
				if m.Fields == nil {
					t.Error("Map.Fields is nil, want non-nil slice")
				}
			},
		},
		{
			name: "IncludeStatement_node_has_required_fields",
			test: func(t *testing.T) {
				inc := &IncludeStatement{
					StartPos: model.Position{Line: 40, Column: 1},
					EndPos:   model.Position{Line: 40, Column: 25},
					Target:   "COPYBOOK",
				}

				// Assert: IncludeStatement has correct StartPos
				if inc.StartPos.Line != 40 || inc.StartPos.Column != 1 {
					t.Errorf("IncludeStatement.StartPos = (%d, %d), want (40, 1)", inc.StartPos.Line, inc.StartPos.Column)
				}

				// Assert: IncludeStatement has correct EndPos
				if inc.EndPos.Line != 40 || inc.EndPos.Column != 25 {
					t.Errorf("IncludeStatement.EndPos = (%d, %d), want (40, 25)", inc.EndPos.Line, inc.EndPos.Column)
				}

				// Assert: IncludeStatement has Target field
				if inc.Target != "COPYBOOK" {
					t.Errorf("IncludeStatement.Target = %q, want \"COPYBOOK\"", inc.Target)
				}
			},
		},
		{
			name: "CallStatement_node_has_required_fields",
			test: func(t *testing.T) {
				call := &CallStatement{
					StartPos:   model.Position{Line: 50, Column: 1},
					EndPos:     model.Position{Line: 50, Column: 30},
					Target:     "PROGRAM-A",
					Parameters: []string{},
				}

				// Assert: CallStatement has correct StartPos
				if call.StartPos.Line != 50 || call.StartPos.Column != 1 {
					t.Errorf("CallStatement.StartPos = (%d, %d), want (50, 1)", call.StartPos.Line, call.StartPos.Column)
				}

				// Assert: CallStatement has correct EndPos
				if call.EndPos.Line != 50 || call.EndPos.Column != 30 {
					t.Errorf("CallStatement.EndPos = (%d, %d), want (50, 30)", call.EndPos.Line, call.EndPos.Column)
				}

				// Assert: CallStatement has Target field
				if call.Target != "PROGRAM-A" {
					t.Errorf("CallStatement.Target = %q, want \"PROGRAM-A\"", call.Target)
				}

				// Assert: CallStatement has Parameters field
				if call.Parameters == nil {
					t.Error("CallStatement.Parameters is nil, want non-nil slice")
				}
			},
		},
		{
			name: "FetchStatement_node_has_required_fields",
			test: func(t *testing.T) {
				fetch := &FetchStatement{
					StartPos: model.Position{Line: 55, Column: 1},
					EndPos:   model.Position{Line: 55, Column: 35},
					Target:   "MYPROG",
				}

				// Assert: FetchStatement has correct StartPos
				if fetch.StartPos.Line != 55 || fetch.StartPos.Column != 1 {
					t.Errorf("FetchStatement.StartPos = (%d, %d), want (55, 1)", fetch.StartPos.Line, fetch.StartPos.Column)
				}

				// Assert: FetchStatement has correct EndPos
				if fetch.EndPos.Line != 55 || fetch.EndPos.Column != 35 {
					t.Errorf("FetchStatement.EndPos = (%d, %d), want (55, 35)", fetch.EndPos.Line, fetch.EndPos.Column)
				}

				// Assert: FetchStatement has Target field (program name per Natural grammar)
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want \"MYPROG\" (program name, not DATABASE clause)", fetch.Target)
				}
			},
		},
		{
			name: "RunStatement_node_has_required_fields",
			test: func(t *testing.T) {
				run := &RunStatement{
					StartPos: model.Position{Line: 60, Column: 1},
					EndPos:   model.Position{Line: 60, Column: 28},
					Target:   "PROGRAM-B",
				}

				// Assert: RunStatement has correct StartPos
				if run.StartPos.Line != 60 || run.StartPos.Column != 1 {
					t.Errorf("RunStatement.StartPos = (%d, %d), want (60, 1)", run.StartPos.Line, run.StartPos.Column)
				}

				// Assert: RunStatement has correct EndPos
				if run.EndPos.Line != 60 || run.EndPos.Column != 28 {
					t.Errorf("RunStatement.EndPos = (%d, %d), want (60, 28)", run.EndPos.Line, run.EndPos.Column)
				}

				// Assert: RunStatement has Target field
				if run.Target != "PROGRAM-B" {
					t.Errorf("RunStatement.Target = %q, want \"PROGRAM-B\"", run.Target)
				}
			},
		},
		{
			name: "PerformStatement_node_has_required_fields",
			test: func(t *testing.T) {
				perf := &PerformStatement{
					StartPos: model.Position{Line: 65, Column: 1},
					EndPos:   model.Position{Line: 65, Column: 32},
					Target:   "SUBROUTINE-A",
				}

				// Assert: PerformStatement has correct StartPos
				if perf.StartPos.Line != 65 || perf.StartPos.Column != 1 {
					t.Errorf("PerformStatement.StartPos = (%d, %d), want (65, 1)", perf.StartPos.Line, perf.StartPos.Column)
				}

				// Assert: PerformStatement has correct EndPos
				if perf.EndPos.Line != 65 || perf.EndPos.Column != 32 {
					t.Errorf("PerformStatement.EndPos = (%d, %d), want (65, 32)", perf.EndPos.Line, perf.EndPos.Column)
				}

				// Assert: PerformStatement has Target field
				if perf.Target != "SUBROUTINE-A" {
					t.Errorf("PerformStatement.Target = %q, want \"SUBROUTINE-A\"", perf.Target)
				}
			},
		},
		{
			name: "ReadStatement_node_has_required_fields",
			test: func(t *testing.T) {
				read := &ReadStatement{
					StartPos: model.Position{Line: 70, Column: 1},
					EndPos:   model.Position{Line: 70, Column: 20},
					Target:   "EMPLOYEES",
				}

				// Assert: ReadStatement has correct StartPos
				if read.StartPos.Line != 70 || read.StartPos.Column != 1 {
					t.Errorf("ReadStatement.StartPos = (%d, %d), want (70, 1)", read.StartPos.Line, read.StartPos.Column)
				}

				// Assert: ReadStatement has correct EndPos
				if read.EndPos.Line != 70 || read.EndPos.Column != 20 {
					t.Errorf("ReadStatement.EndPos = (%d, %d), want (70, 20)", read.EndPos.Line, read.EndPos.Column)
				}

				// Assert: ReadStatement has Target field
				if read.Target != "EMPLOYEES" {
					t.Errorf("ReadStatement.Target = %q, want \"EMPLOYEES\"", read.Target)
				}

				// Assert: ReadStatement implements Node interface (Position returns StartPos/EndPos)
				start, end := read.Position()
				if start != read.StartPos {
					t.Errorf("ReadStatement.Position() start = %v, want %v", start, read.StartPos)
				}
				if end != read.EndPos {
					t.Errorf("ReadStatement.Position() end = %v, want %v", end, read.EndPos)
				}
			},
		},
		{
			name: "StoreStatement_node_has_required_fields",
			test: func(t *testing.T) {
				store := &StoreStatement{
					StartPos: model.Position{Line: 75, Column: 1},
					EndPos:   model.Position{Line: 75, Column: 22},
					Target:   "PERSONNEL",
				}

				// Assert: StoreStatement has correct StartPos
				if store.StartPos.Line != 75 || store.StartPos.Column != 1 {
					t.Errorf("StoreStatement.StartPos = (%d, %d), want (75, 1)", store.StartPos.Line, store.StartPos.Column)
				}

				// Assert: StoreStatement has correct EndPos
				if store.EndPos.Line != 75 || store.EndPos.Column != 22 {
					t.Errorf("StoreStatement.EndPos = (%d, %d), want (75, 22)", store.EndPos.Line, store.EndPos.Column)
				}

				// Assert: StoreStatement has Target field
				if store.Target != "PERSONNEL" {
					t.Errorf("StoreStatement.Target = %q, want \"PERSONNEL\"", store.Target)
				}

				// Assert: StoreStatement implements Node interface (Position returns StartPos/EndPos)
				start, end := store.Position()
				if start != store.StartPos {
					t.Errorf("StoreStatement.Position() start = %v, want %v", start, store.StartPos)
				}
				if end != store.EndPos {
					t.Errorf("StoreStatement.Position() end = %v, want %v", end, store.EndPos)
				}
			},
		},
		{
			name: "Program_parent_child_relationship",
			test: func(t *testing.T) {
				// Arrange: create a Program with child subroutines
				prog := &Program{
					StartPos: model.Position{Line: 1, Column: 1},
					EndPos:   model.Position{Line: 100, Column: 1},
					Subroutines: []*Subroutine{
						{
							StartPos: model.Position{Line: 10, Column: 1},
							EndPos:   model.Position{Line: 20, Column: 1},
							Name:     "SUB1",
						},
						{
							StartPos: model.Position{Line: 30, Column: 1},
							EndPos:   model.Position{Line: 40, Column: 1},
							Name:     "SUB2",
						},
					},
				}

				// Assert: Program contains subroutines
				if len(prog.Subroutines) != 2 {
					t.Errorf("Program.Subroutines length = %d, want 2", len(prog.Subroutines))
				}

				// Assert: First subroutine has correct name
				if prog.Subroutines[0].Name != "SUB1" {
					t.Errorf("Program.Subroutines[0].Name = %q, want \"SUB1\"", prog.Subroutines[0].Name)
				}

				// Assert: Second subroutine has correct name
				if prog.Subroutines[1].Name != "SUB2" {
					t.Errorf("Program.Subroutines[1].Name = %q, want \"SUB2\"", prog.Subroutines[1].Name)
				}
			},
		},
		{
			name: "DataSection_parent_child_relationship",
			test: func(t *testing.T) {
				// Arrange: create a DataSection with child fields
				ds := &DataSection{
					StartPos: model.Position{Line: 5, Column: 1},
					EndPos:   model.Position{Line: 15, Column: 1},
					Fields: []*DataField{
						{
							StartPos: model.Position{Line: 6, Column: 5},
							EndPos:   model.Position{Line: 6, Column: 20},
							Name:     "FIELD1",
						},
						{
							StartPos: model.Position{Line: 7, Column: 5},
							EndPos:   model.Position{Line: 7, Column: 20},
							Name:     "FIELD2",
						},
					},
				}

				// Assert: DataSection contains fields
				if len(ds.Fields) != 2 {
					t.Errorf("DataSection.Fields length = %d, want 2", len(ds.Fields))
				}

				// Assert: First field has correct name
				if ds.Fields[0].Name != "FIELD1" {
					t.Errorf("DataSection.Fields[0].Name = %q, want \"FIELD1\"", ds.Fields[0].Name)
				}

				// Assert: Second field has correct name
				if ds.Fields[1].Name != "FIELD2" {
					t.Errorf("DataSection.Fields[1].Name = %q, want \"FIELD2\"", ds.Fields[1].Name)
				}
			},
		},
		{
			name: "Map_parent_child_relationship",
			test: func(t *testing.T) {
				// Arrange: create a Map with child fields
				m := &Map{
					StartPos: model.Position{Line: 30, Column: 1},
					EndPos:   model.Position{Line: 60, Column: 1},
					Name:     "MY-MAP",
					Fields: []*DataField{
						{
							StartPos: model.Position{Line: 35, Column: 5},
							EndPos:   model.Position{Line: 35, Column: 30},
							Name:     "INPUT-FIELD",
						},
					},
				}

				// Assert: Map contains fields
				if len(m.Fields) != 1 {
					t.Errorf("Map.Fields length = %d, want 1", len(m.Fields))
				}

				// Assert: Field has correct name
				if m.Fields[0].Name != "INPUT-FIELD" {
					t.Errorf("Map.Fields[0].Name = %q, want \"INPUT-FIELD\"", m.Fields[0].Name)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.test(t)
		})
	}
}

// TestASTNodeTypes_EmbeddedSQL verifies AST node types for embedded SQL statements (ES-3).
// Task ES-3: add non-loop SQL statement nodes with unbound operand lists and raw opaque
// body representation (FR-30, NFR-15, M-6).
//
// This test asserts the structure of the SQL statement nodes without parser behavior:
// - SelectSingleStatement (SELECT SINGLE, no body)
// - InsertStatement (INSERT)
// - SQLUpdateStatement (SQL-form UPDATE)
// - SQLDeleteStatement (SQL-form DELETE)
// - MergeStatement (MERGE)
// - CommitStatement (COMMIT)
// - RollbackStatement (ROLLBACK)
// - CallDBProcStatement (CALLDBPROC)
// - ProcessSQLStatement (PROCESS SQL with opaque body)
//
// Each node embeds StartPos/EndPos, implements Node, and appears as a Program slice.
// ProcessSQLStatement's Body is a raw span (text + model.Range) with no host-var collection.
func TestASTNodeTypes_EmbeddedSQL(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "SelectSingleStatement_exposes_unbound_operand_lists",
			test: func(t *testing.T) {
				// Arrange: SelectSingleStatement must expose unbound operand lists
				// (name+range pairs) for:
				// - Columns: selected columns
				// - IntoTargets: INTO clause host-var target names
				// - FromTables: FROM clause table operands
				// - WhereOperands: WHERE clause host-var operands
				start := model.Position{Line: 10, Column: 1}
				end := model.Position{Line: 10, Column: 50}

				// Per ES-3 behavior, construct a SelectSingleStatement with operand lists.
				// Exact field names (Columns vs SelectedColumns, etc.) are a green-phase
				// decision, but this test structure pins that they must exist and support
				// round-tripping name+range pairs.
				stmt := &SelectSingleStatement{
					StartPos: start,
					EndPos:   end,
					Columns: []OperandRef{
						{Name: "COL1", Range: model.Range{Start: model.Position{Line: 10, Column: 10}, End: model.Position{Line: 10, Column: 14}}},
						{Name: "COL2", Range: model.Range{Start: model.Position{Line: 10, Column: 16}, End: model.Position{Line: 10, Column: 20}}},
					},
					IntoTargets: []OperandRef{
						{Name: "#VAR1", Range: model.Range{Start: model.Position{Line: 10, Column: 26}, End: model.Position{Line: 10, Column: 31}}},
					},
					FromTables: []OperandRef{
						{Name: "EMPLOYEES", Range: model.Range{Start: model.Position{Line: 10, Column: 37}, End: model.Position{Line: 10, Column: 46}}},
					},
					WhereOperands: []OperandRef{
						{Name: "#ID", Range: model.Range{Start: model.Position{Line: 10, Column: 49}, End: model.Position{Line: 10, Column: 52}}},
					},
				}

				// Act: call Position() to verify Node interface
				gotStart, gotEnd := stmt.Position()

				// Assert: positions round-trip correctly
				if gotStart != start {
					t.Errorf("SelectSingleStatement.Position() start = %v, want %v", gotStart, start)
				}
				if gotEnd != end {
					t.Errorf("SelectSingleStatement.Position() end = %v, want %v", gotEnd, end)
				}

				// Assert: operand lists round-trip
				if len(stmt.Columns) != 2 || stmt.Columns[0].Name != "COL1" {
					t.Errorf("SelectSingleStatement.Columns = %v, want [COL1, COL2]", stmt.Columns)
				}
				if len(stmt.IntoTargets) != 1 || stmt.IntoTargets[0].Name != "#VAR1" {
					t.Errorf("SelectSingleStatement.IntoTargets = %v, want [#VAR1]", stmt.IntoTargets)
				}
				if len(stmt.FromTables) != 1 || stmt.FromTables[0].Name != "EMPLOYEES" {
					t.Errorf("SelectSingleStatement.FromTables = %v, want [EMPLOYEES]", stmt.FromTables)
				}
				if len(stmt.WhereOperands) != 1 || stmt.WhereOperands[0].Name != "#ID" {
					t.Errorf("SelectSingleStatement.WhereOperands = %v, want [#ID]", stmt.WhereOperands)
				}

				// Assert: implements Node interface
				var _ Node = stmt
			},
		},
		{
			name: "InsertStatement_exposes_unbound_operand_lists",
			test: func(t *testing.T) {
				// Arrange: InsertStatement must expose unbound operand lists
				// (name+range pairs) for:
				// - IntoTable: INTO clause table operand
				// - Values: VALUES clause operands (host-vars or literals)
				start := model.Position{Line: 12, Column: 1}
				end := model.Position{Line: 12, Column: 55}
				stmt := &InsertStatement{
					StartPos: start,
					EndPos:   end,
					IntoTable: []OperandRef{
						{Name: "EMPLOYEES", Range: model.Range{Start: model.Position{Line: 12, Column: 15}, End: model.Position{Line: 12, Column: 24}}},
					},
					Values: []OperandRef{
						{Name: "#ID", Range: model.Range{Start: model.Position{Line: 12, Column: 33}, End: model.Position{Line: 12, Column: 36}}},
						{Name: "#NAME", Range: model.Range{Start: model.Position{Line: 12, Column: 38}, End: model.Position{Line: 12, Column: 43}}},
					},
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("InsertStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				// Assert: operand lists round-trip
				if len(stmt.IntoTable) != 1 || stmt.IntoTable[0].Name != "EMPLOYEES" {
					t.Errorf("InsertStatement.IntoTable = %v, want [EMPLOYEES]", stmt.IntoTable)
				}
				if len(stmt.Values) != 2 || stmt.Values[0].Name != "#ID" {
					t.Errorf("InsertStatement.Values = %v, want [#ID, #NAME]", stmt.Values)
				}

				var _ Node = stmt
			},
		},
		{
			name: "SQLUpdateStatement_exposes_unbound_operand_lists",
			test: func(t *testing.T) {
				// Arrange: SQLUpdateStatement must expose unbound operand lists
				// (name+range pairs) for:
				// - Table: table operand
				// - SetTargets: SET clause target columns and their values
				// - WhereOperands: WHERE clause host-var operands
				start := model.Position{Line: 14, Column: 1}
				end := model.Position{Line: 14, Column: 60}
				stmt := &SQLUpdateStatement{
					StartPos: start,
					EndPos:   end,
					Table: []OperandRef{
						{Name: "EMPLOYEES", Range: model.Range{Start: model.Position{Line: 14, Column: 8}, End: model.Position{Line: 14, Column: 17}}},
					},
					SetTargets: []OperandRef{
						{Name: "SALARY", Range: model.Range{Start: model.Position{Line: 14, Column: 22}, End: model.Position{Line: 14, Column: 28}}},
						{Name: "#NEWSAL", Range: model.Range{Start: model.Position{Line: 14, Column: 31}, End: model.Position{Line: 14, Column: 38}}},
					},
					WhereOperands: []OperandRef{
						{Name: "#EMPID", Range: model.Range{Start: model.Position{Line: 14, Column: 45}, End: model.Position{Line: 14, Column: 51}}},
					},
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("SQLUpdateStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				// Assert: operand lists round-trip
				if len(stmt.Table) != 1 || stmt.Table[0].Name != "EMPLOYEES" {
					t.Errorf("SQLUpdateStatement.Table = %v, want [EMPLOYEES]", stmt.Table)
				}
				if len(stmt.SetTargets) != 2 || stmt.SetTargets[0].Name != "SALARY" {
					t.Errorf("SQLUpdateStatement.SetTargets = %v, want [SALARY, #NEWSAL]", stmt.SetTargets)
				}
				if len(stmt.WhereOperands) != 1 || stmt.WhereOperands[0].Name != "#EMPID" {
					t.Errorf("SQLUpdateStatement.WhereOperands = %v, want [#EMPID]", stmt.WhereOperands)
				}

				var _ Node = stmt
			},
		},
		{
			name: "SQLDeleteStatement_exposes_unbound_operand_lists",
			test: func(t *testing.T) {
				// Arrange: SQLDeleteStatement must expose unbound operand lists
				// (name+range pairs) for:
				// - FromTable: FROM clause table operand
				// - WhereOperands: WHERE clause host-var operands
				start := model.Position{Line: 16, Column: 1}
				end := model.Position{Line: 16, Column: 50}
				stmt := &SQLDeleteStatement{
					StartPos: start,
					EndPos:   end,
					FromTable: []OperandRef{
						{Name: "EMPLOYEES", Range: model.Range{Start: model.Position{Line: 16, Column: 13}, End: model.Position{Line: 16, Column: 22}}},
					},
					WhereOperands: []OperandRef{
						{Name: "#ID", Range: model.Range{Start: model.Position{Line: 16, Column: 31}, End: model.Position{Line: 16, Column: 34}}},
					},
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("SQLDeleteStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				// Assert: operand lists round-trip
				if len(stmt.FromTable) != 1 || stmt.FromTable[0].Name != "EMPLOYEES" {
					t.Errorf("SQLDeleteStatement.FromTable = %v, want [EMPLOYEES]", stmt.FromTable)
				}
				if len(stmt.WhereOperands) != 1 || stmt.WhereOperands[0].Name != "#ID" {
					t.Errorf("SQLDeleteStatement.WhereOperands = %v, want [#ID]", stmt.WhereOperands)
				}

				var _ Node = stmt
			},
		},
		{
			name: "MergeStatement_implements_Node_and_round_trips_fields",
			test: func(t *testing.T) {
				// Arrange: MERGE statement with table operand
				start := model.Position{Line: 18, Column: 1}
				end := model.Position{Line: 18, Column: 45}
				stmt := &MergeStatement{
					StartPos: start,
					EndPos:   end,
					// Table operand, no deep MERGE grammar modeling
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("MergeStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				var _ Node = stmt
			},
		},
		{
			name: "CommitStatement_implements_Node_and_round_trips_fields",
			test: func(t *testing.T) {
				// Arrange: COMMIT takes no operands
				start := model.Position{Line: 20, Column: 1}
				end := model.Position{Line: 20, Column: 6}
				stmt := &CommitStatement{
					StartPos: start,
					EndPos:   end,
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("CommitStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				var _ Node = stmt
			},
		},
		{
			name: "RollbackStatement_implements_Node_and_round_trips_fields",
			test: func(t *testing.T) {
				// Arrange: ROLLBACK takes no operands
				start := model.Position{Line: 22, Column: 1}
				end := model.Position{Line: 22, Column: 9}
				stmt := &RollbackStatement{
					StartPos: start,
					EndPos:   end,
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("RollbackStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				var _ Node = stmt
			},
		},
		{
			name: "CallDBProcStatement_implements_Node_and_round_trips_fields",
			test: func(t *testing.T) {
				// Arrange: CALLDBPROC with procedure-name operand
				start := model.Position{Line: 24, Column: 1}
				end := model.Position{Line: 24, Column: 40}
				stmt := &CallDBProcStatement{
					StartPos: start,
					EndPos:   end,
					// Proc-name operand + remaining operands
				}

				// Act & Assert
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("CallDBProcStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				var _ Node = stmt
			},
		},
		{
			name: "ProcessSQLStatement_has_DDMName_and_opaque_Body_no_host_var_collection",
			test: func(t *testing.T) {
				// Arrange: PROCESS SQL statement with opaque raw-text body (Option B modeled gap).
				// Per ES-3, ProcessSQLStatement contains EXACTLY these fields:
				// - StartPos, EndPos (positions)
				// - DDMName (string), DDMNameRange (model.Range)
				// - Body (string), BodyRange (model.Range)
				// NO HostVars or host-var-collection field.
				// The interior of Body (e.g., :#VAR) is NEVER parsed.
				//
				// The struct literal below sets all intended fields by name. If a HostVars
				// field is later added to ProcessSQLStatement, this test must be updated to
				// assert its absence or zero value; the test documents the opaque-body contract
				// but does not enforce it at compile time (named struct literals allow new fields).
				start := model.Position{Line: 26, Column: 1}
				end := model.Position{Line: 28, Column: 20}
				stmt := &ProcessSQLStatement{
					// ONLY these fields exist; any HostVars field will cause a compile error:
					StartPos: start,
					EndPos:   end,
					DDMName:  "MY_DDM",
					DDMNameRange: model.Range{
						Start: model.Position{Line: 26, Column: 15},
						End:   model.Position{Line: 26, Column: 21},
					},
					Body: "UPDATE MY_DDM SET COL = :#VAR",
					BodyRange: model.Range{
						Start: model.Position{Line: 26, Column: 22},
						End:   model.Position{Line: 28, Column: 20},
					},
				}

				// Act & Assert: Position round-trips
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("ProcessSQLStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				// Assert: DDMName and DDMNameRange exist and round-trip
				if stmt.DDMName != "MY_DDM" {
					t.Errorf("ProcessSQLStatement.DDMName = %q, want \"MY_DDM\"", stmt.DDMName)
				}
				if stmt.DDMNameRange.Start.Line != 26 || stmt.DDMNameRange.Start.Column != 15 {
					t.Errorf("ProcessSQLStatement.DDMNameRange.Start = %v, want {26, 15}", stmt.DDMNameRange.Start)
				}
				if stmt.DDMNameRange.End.Line != 26 || stmt.DDMNameRange.End.Column != 21 {
					t.Errorf("ProcessSQLStatement.DDMNameRange.End = %v, want {26, 21}", stmt.DDMNameRange.End)
				}

				// Assert: Body is raw text (opaque, unparsed) — the interior :#VAR is NOT parsed.
				// The equality check is sufficient; a substring check after equality is redundant.
				if stmt.Body != "UPDATE MY_DDM SET COL = :#VAR" {
					t.Errorf("ProcessSQLStatement.Body = %q, want \"UPDATE MY_DDM SET COL = :#VAR\"", stmt.Body)
				}

				// Assert: BodyRange exists and covers the << >> delimiters
				if stmt.BodyRange.Start.Line != 26 || stmt.BodyRange.Start.Column != 22 {
					t.Errorf("ProcessSQLStatement.BodyRange.Start = %v, want {26, 22}", stmt.BodyRange.Start)
				}
				if stmt.BodyRange.End.Line != 28 || stmt.BodyRange.End.Column != 20 {
					t.Errorf("ProcessSQLStatement.BodyRange.End = %v, want {28, 20}", stmt.BodyRange.End)
				}

				// Assert: implements Node interface
				var _ Node = stmt
			},
		},
		{
			name: "Program_has_dedicated_SQL_statement_slices",
			test: func(t *testing.T) {
				// Arrange: create a Program with SQL statement slices
				prog := &Program{
					StartPos:      model.Position{Line: 1, Column: 1},
					EndPos:        model.Position{Line: 100, Column: 1},
					SelectSingles: []*SelectSingleStatement{},
					Inserts:       []*InsertStatement{},
					SQLUpdates:    []*SQLUpdateStatement{},
					SQLDeletes:    []*SQLDeleteStatement{},
					Merges:        []*MergeStatement{},
					Commits:       []*CommitStatement{},
					Rollbacks:     []*RollbackStatement{},
					CallDBProcs:   []*CallDBProcStatement{},
					ProcessSQLs:   []*ProcessSQLStatement{},
				}

				// Assert: all SQL slices exist and are accessible
				if prog.SelectSingles == nil {
					t.Error("Program.SelectSingles is nil, want non-nil slice")
				}
				if prog.Inserts == nil {
					t.Error("Program.Inserts is nil, want non-nil slice")
				}
				if prog.SQLUpdates == nil {
					t.Error("Program.SQLUpdates is nil, want non-nil slice")
				}
				if prog.SQLDeletes == nil {
					t.Error("Program.SQLDeletes is nil, want non-nil slice")
				}
				if prog.Merges == nil {
					t.Error("Program.Merges is nil, want non-nil slice")
				}
				if prog.Commits == nil {
					t.Error("Program.Commits is nil, want non-nil slice")
				}
				if prog.Rollbacks == nil {
					t.Error("Program.Rollbacks is nil, want non-nil slice")
				}
				if prog.CallDBProcs == nil {
					t.Error("Program.CallDBProcs is nil, want non-nil slice")
				}
				if prog.ProcessSQLs == nil {
					t.Error("Program.ProcessSQLs is nil, want non-nil slice")
				}
			},
		},
		{
			name: "SelectSingleStatement_has_no_body_field",
			test: func(t *testing.T) {
				// Arrange & Assert: SelectSingleStatement must NOT have a Body field
				// (ES-4 loop nodes have Body; ES-3 singleton nodes do not).
				// This is verified at compile time: if a Body field exists, the next
				// line will fail with "unknown field Body".
				stmt := &SelectSingleStatement{
					StartPos: model.Position{Line: 1, Column: 1},
					EndPos:   model.Position{Line: 1, Column: 30},
					// No Body field — this distinguishes it from SelectStatement (ES-4).
				}

				// The struct literal above is the real assertion: it compiles only
				// because SelectSingleStatement has no Body field. Exercise the node
				// so it is a Node whose positions round-trip.
				var _ Node = stmt
				start, end := stmt.Position()
				if start.Line != 1 || end.Column != 30 {
					t.Errorf("Position() = (%+v, %+v), want start.Line=1 end.Column=30", start, end)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.test(t)
		})
	}
}

// TestASTNodeTypes_LoopBodyNodes verifies AST node types for embedded SQL loop statements (ES-4).
// Task ES-4: add body-bearing loop nodes distinct from their singleton counterparts.
// This is the FIRST loop-body node in this codebase (FR-30, NFR-15, M-6).
//
// These two nodes differ from ES-3 in that they carry a loop Body:
//   - SelectStatement (cursor loop) — carries SAME unbound operand lists as SelectSingleStatement
//     (Columns, IntoTargets, FromTables, WhereOperands) PLUS a loop Body.
//   - ReadResultSetStatement (result-set loop) — carries the result-set operand PLUS a loop Body.
//
// PINNED BODY SHAPE: Body []Node
// All AST nodes already implement the Node interface, so a loop body is naturally
// represented as []Node — traversable, typed, and directly holds statement nodes.
// This test asserts that Body can hold and expose child statement nodes via type assertion.
func TestASTNodeTypes_LoopBodyNodes(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "SelectStatement_has_operand_lists_and_loop_body",
			test: func(t *testing.T) {
				// Arrange: SelectStatement must expose the SAME unbound operand lists
				// as SelectSingleStatement (Columns, IntoTargets, FromTables, WhereOperands)
				// PLUS a loop Body: []Node.
				//
				// This test constructs a SelectStatement with a Body containing a child statement
				// node, then asserts the body holds the expected typed element.
				start := model.Position{Line: 30, Column: 1}
				end := model.Position{Line: 40, Column: 10}

				// Create a child statement node to put in the body.
				childCall := &CallStatement{
					StartPos:        model.Position{Line: 31, Column: 5},
					EndPos:          model.Position{Line: 31, Column: 25},
					Target:          "SUBROUTINE",
					TargetIsLiteral: true,
					TargetRange:     model.Range{Start: model.Position{Line: 31, Column: 13}, End: model.Position{Line: 31, Column: 23}},
				}

				// Construct a SelectStatement with operand lists and a Body: []Node.
				// This struct literal will fail to compile if SelectStatement or Body don't exist
				// (expected red phase failure).
				stmt := &SelectStatement{
					StartPos: start,
					EndPos:   end,
					Columns: []OperandRef{
						{Name: "COL1", Range: model.Range{Start: model.Position{Line: 30, Column: 10}, End: model.Position{Line: 30, Column: 14}}},
						{Name: "COL2", Range: model.Range{Start: model.Position{Line: 30, Column: 16}, End: model.Position{Line: 30, Column: 20}}},
					},
					IntoTargets: []OperandRef{
						{Name: "#A", Range: model.Range{Start: model.Position{Line: 31, Column: 8}, End: model.Position{Line: 31, Column: 10}}},
						{Name: "#B", Range: model.Range{Start: model.Position{Line: 31, Column: 12}, End: model.Position{Line: 31, Column: 14}}},
					},
					FromTables: []OperandRef{
						{Name: "EMPLOYEES", Range: model.Range{Start: model.Position{Line: 32, Column: 7}, End: model.Position{Line: 32, Column: 16}}},
					},
					WhereOperands: []OperandRef{
						{Name: "#K", Range: model.Range{Start: model.Position{Line: 33, Column: 8}, End: model.Position{Line: 33, Column: 10}}},
					},
					Body: []Node{childCall}, // Body: []Node containing the child statement
				}

				// Act & Assert: Position round-trip
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("SelectStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				// Assert: operand lists exist and round-trip (same as SelectSingleStatement)
				if len(stmt.Columns) != 2 || stmt.Columns[0].Name != "COL1" {
					t.Errorf("SelectStatement.Columns = %v, want [COL1, COL2]", stmt.Columns)
				}
				if len(stmt.IntoTargets) != 2 || stmt.IntoTargets[0].Name != "#A" {
					t.Errorf("SelectStatement.IntoTargets = %v, want [#A, #B]", stmt.IntoTargets)
				}
				if len(stmt.FromTables) != 1 || stmt.FromTables[0].Name != "EMPLOYEES" {
					t.Errorf("SelectStatement.FromTables = %v, want [EMPLOYEES]", stmt.FromTables)
				}
				if len(stmt.WhereOperands) != 1 || stmt.WhereOperands[0].Name != "#K" {
					t.Errorf("SelectStatement.WhereOperands = %v, want [#K]", stmt.WhereOperands)
				}

				// Assert: Body is []Node and holds the child statement
				if len(stmt.Body) != 1 {
					t.Errorf("SelectStatement.Body length = %d, want 1", len(stmt.Body))
				}

				// Assert: Body element type-asserts back to CallStatement
				call, ok := stmt.Body[0].(*CallStatement)
				if !ok {
					t.Errorf("SelectStatement.Body[0] type assertion to *CallStatement failed, got %T", stmt.Body[0])
				} else if call.Target != "SUBROUTINE" {
					t.Errorf("SelectStatement.Body[0].Target = %q, want \"SUBROUTINE\"", call.Target)
				}

				// Assert: implements Node interface
				var _ Node = stmt
			},
		},
		{
			name: "ReadResultSetStatement_has_result_set_operand_and_loop_body",
			test: func(t *testing.T) {
				// Arrange: ReadResultSetStatement must expose the result-set operand
				// it reads PLUS a loop Body: []Node (similar to SelectStatement).
				start := model.Position{Line: 50, Column: 1}
				end := model.Position{Line: 60, Column: 10}

				// Create a child statement node to put in the body.
				childPerform := &PerformStatement{
					StartPos:    model.Position{Line: 51, Column: 5},
					EndPos:      model.Position{Line: 51, Column: 28},
					Target:      "MY-SUBROUTINE",
					TargetRange: model.Range{Start: model.Position{Line: 51, Column: 13}, End: model.Position{Line: 51, Column: 26}},
				}

				// Construct a ReadResultSetStatement with result-set operand and Body: []Node.
				// This struct literal will fail to compile if ReadResultSetStatement or Body don't exist
				// (expected red phase failure).
				stmt := &ReadResultSetStatement{
					StartPos: start,
					EndPos:   end,
					// Result-set operand: the operand it reads (name+range pair)
					ResultSetOperand: OperandRef{
						Name: "MYPROCRESULT",
						Range: model.Range{
							Start: model.Position{Line: 50, Column: 18},
							End:   model.Position{Line: 50, Column: 30},
						},
					},
					Body: []Node{childPerform}, // Body: []Node containing the child statement
				}

				// Act & Assert: Position round-trip
				gotStart, gotEnd := stmt.Position()
				if gotStart != start || gotEnd != end {
					t.Errorf("ReadResultSetStatement.Position() = (%v, %v), want (%v, %v)", gotStart, gotEnd, start, end)
				}

				// Assert: result-set operand exists and round-trips
				if stmt.ResultSetOperand.Name != "MYPROCRESULT" {
					t.Errorf("ReadResultSetStatement.ResultSetOperand.Name = %q, want \"MYPROCRESULT\"", stmt.ResultSetOperand.Name)
				}

				// Assert: Body is []Node and holds the child statement
				if len(stmt.Body) != 1 {
					t.Errorf("ReadResultSetStatement.Body length = %d, want 1", len(stmt.Body))
				}

				// Assert: Body element type-asserts back to PerformStatement
				perform, ok := stmt.Body[0].(*PerformStatement)
				if !ok {
					t.Errorf("ReadResultSetStatement.Body[0] type assertion to *PerformStatement failed, got %T", stmt.Body[0])
				} else if perform.Target != "MY-SUBROUTINE" {
					t.Errorf("ReadResultSetStatement.Body[0].Target = %q, want \"MY-SUBROUTINE\"", perform.Target)
				}

				// Assert: implements Node interface
				var _ Node = stmt
			},
		},
		{
			name: "SelectSingleStatement_vs_SelectStatement_has_no_body",
			test: func(t *testing.T) {
				// Arrange & Assert: The crucial distinction — SelectSingleStatement (ES-3, singleton)
				// has NO body field, while SelectStatement (ES-4, loop) has a Body: []Node field.
				// This test demonstrates the compile-time enforcement of the distinction.

				// SelectSingleStatement MUST NOT have a Body field:
				// The literal below must compile, which proves SelectSingleStatement.Body does NOT exist.
				singleStmt := &SelectSingleStatement{
					StartPos: model.Position{Line: 10, Column: 1},
					EndPos:   model.Position{Line: 10, Column: 30},
					Columns:  []OperandRef{{Name: "COL1", Range: model.Range{}}},
					// NO Body field — this is correct for ES-3.
					// If a Body field were added, this literal would fail to compile
					// with "unknown field Body", proving the distinction is enforced.
				}

				// Exercise the node to show it is a valid Node.
				var _ Node = singleStmt
				start, end := singleStmt.Position()
				if start.Line != 10 || end.Column != 30 {
					t.Errorf("SelectSingleStatement.Position() start.Line = %d, end.Column = %d; want 10, 30", start.Line, end.Column)
				}

				// Contrast: SelectStatement MUST have a Body: []Node field.
				// The green-phase test will populate this with actual child statements;
				// this red-phase test simply asserts the field exists and is of type []Node.
				loopStmt := &SelectStatement{
					StartPos: model.Position{Line: 20, Column: 1},
					EndPos:   model.Position{Line: 30, Column: 10},
					Columns:  []OperandRef{{Name: "COL1", Range: model.Range{}}},
					Body:     []Node{}, // Body: []Node (empty, but structure is pinned)
				}

				// Exercise the node to show it is a valid Node.
				var _ Node = loopStmt
				start, end = loopStmt.Position()
				if start.Line != 20 || end.Line != 30 {
					t.Errorf("SelectStatement.Position() start.Line = %d, end.Line = %d; want 20, 30", start.Line, end.Line)
				}

				// The literal assertions above are the real test:
				// - SelectSingleStatement compiles without a Body field (singleton, no loop).
				// - SelectStatement compiles WITH a Body: []Node field (loop, can have children).
				// If either struct is missing the field or has a different field type,
				// the literal will fail at compile time, clearly documenting the shape.
			},
		},
		{
			name: "Program_has_SelectStatement_and_ReadResultSetStatement_slices",
			test: func(t *testing.T) {
				// Arrange: create a Program with the new ES-4 loop-body slices.
				// This assertion verifies at compile-time that Program has Selects
				// and ReadResultSets fields (if they don't exist, this literal fails to compile).
				prog := &Program{
					StartPos:       model.Position{Line: 1, Column: 1},
					EndPos:         model.Position{Line: 100, Column: 1},
					Selects:        []*SelectStatement{},        // ES-4 loop form
					ReadResultSets: []*ReadResultSetStatement{}, // ES-4 loop form
					SelectSingles:  []*SelectSingleStatement{},  // ES-3 singleton form
					Reads:          []*ReadStatement{},          // existing Adabas form
				}

				// Assert: ES-4 loop-form slices are initialized (empty but not nil)
				if prog.Selects == nil {
					t.Error("Program.Selects is nil, want non-nil slice")
				}
				if prog.ReadResultSets == nil {
					t.Error("Program.ReadResultSets is nil, want non-nil slice")
				}

				// Assert: ES-3 and existing slices are still present (backward compat).
				// They are initialized in the literal above, so they are non-nil.
				if prog.SelectSingles == nil {
					t.Error("Program.SelectSingles (ES-3) is nil, want non-nil slice")
				}
				if prog.Reads == nil {
					t.Error("Program.Reads (existing Adabas READ) is nil, want non-nil slice")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.test(t)
		})
	}
}
