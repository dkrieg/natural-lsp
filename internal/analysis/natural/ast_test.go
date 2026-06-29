package natural

import (
	"natural-lsp/internal/model"
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
