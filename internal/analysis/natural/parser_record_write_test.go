package natural

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParser_RecordUpdateDelete tests Task 7 (FR-20): parsing Adabas record-form UPDATE/DELETE
// statements and disambiguating them from SQL forms.
// Fixture: 22-record-update-delete.nsp
//
// Expected behavior:
// - Bare UPDATE → one RecordUpdateStatement with empty Label
// - UPDATE (0250) → one RecordUpdateStatement with Label "0250"
// - Bare DELETE → one RecordDeleteStatement with empty Label
// - DELETE (RD.) → one RecordDeleteStatement with Label "RD."
// - SQL UPDATE MYTABLE SET ... → one SQLUpdateStatement (no record node)
// - SQL DELETE FROM MYTABLE ... → one SQLDeleteStatement (no record node)
//
// This test ensures the SQL forms remain unaffected (regression guard) while the
// record forms are now captured in the AST.
func TestParser_RecordUpdateDelete(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "22-record-update-delete.nsp"))
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

	// Task 7 acceptance: exactly 2 RecordUpdateStatement nodes
	if len(prog.RecordUpdates) != 2 {
		t.Fatalf("len(prog.RecordUpdates) = %d, want 2; got: %v",
			len(prog.RecordUpdates), prog.RecordUpdates)
	}

	// Task 7 acceptance: exactly 2 RecordDeleteStatement nodes
	if len(prog.RecordDeletes) != 2 {
		t.Fatalf("len(prog.RecordDeletes) = %d, want 2; got: %v",
			len(prog.RecordDeletes), prog.RecordDeletes)
	}

	// Verify record UPDATE nodes
	upd1 := prog.RecordUpdates[0]
	if upd1.Label != "" {
		t.Errorf("First RecordUpdateStatement.Label = %q, want empty (bare UPDATE)", upd1.Label)
	}
	if upd1.StartPos.Line == 0 || upd1.EndPos.Line == 0 {
		t.Errorf("First RecordUpdateStatement positions invalid: StartPos=%v, EndPos=%v",
			upd1.StartPos, upd1.EndPos)
	}

	upd2 := prog.RecordUpdates[1]
	if upd2.Label != "0250" {
		t.Errorf("Second RecordUpdateStatement.Label = %q, want \"0250\"", upd2.Label)
	}
	if upd2.StartPos.Line == 0 || upd2.EndPos.Line == 0 {
		t.Errorf("Second RecordUpdateStatement positions invalid: StartPos=%v, EndPos=%v",
			upd2.StartPos, upd2.EndPos)
	}

	// Verify record DELETE nodes
	del1 := prog.RecordDeletes[0]
	if del1.Label != "" {
		t.Errorf("First RecordDeleteStatement.Label = %q, want empty (bare DELETE)", del1.Label)
	}
	if del1.StartPos.Line == 0 || del1.EndPos.Line == 0 {
		t.Errorf("First RecordDeleteStatement positions invalid: StartPos=%v, EndPos=%v",
			del1.StartPos, del1.EndPos)
	}

	del2 := prog.RecordDeletes[1]
	if del2.Label != "RD." {
		t.Errorf("Second RecordDeleteStatement.Label = %q, want \"RD.\"", del2.Label)
	}
	if del2.StartPos.Line == 0 || del2.EndPos.Line == 0 {
		t.Errorf("Second RecordDeleteStatement positions invalid: StartPos=%v, EndPos=%v",
			del2.StartPos, del2.EndPos)
	}

	// Regression guard: SQL UPDATE/DELETE must still parse as SQL, not record forms
	if len(prog.SQLUpdates) != 1 {
		t.Errorf("len(prog.SQLUpdates) = %d, want 1 (SQL UPDATE regression guard); got: %v",
			len(prog.SQLUpdates), prog.SQLUpdates)
	}

	if len(prog.SQLDeletes) != 1 {
		t.Errorf("len(prog.SQLDeletes) = %d, want 1 (SQL DELETE regression guard); got: %v",
			len(prog.SQLDeletes), prog.SQLDeletes)
	}

	// Verify the SQL UPDATE still has the table operand
	if len(prog.SQLUpdates) > 0 {
		sqlUpd := prog.SQLUpdates[0]
		if len(sqlUpd.Table) == 0 {
			t.Errorf("SQLUpdateStatement.Table is empty, want populated (MYTABLE)")
		}
	}

	// Verify the SQL DELETE still has the table operand
	if len(prog.SQLDeletes) > 0 {
		sqlDel := prog.SQLDeletes[0]
		if len(sqlDel.FromTable) == 0 {
			t.Errorf("SQLDeleteStatement.FromTable is empty, want populated (MYTABLE)")
		}
	}

	// Fixture must parse with zero diagnostics (all statements well-formed)
	if len(prog.Diagnostics) != 0 {
		t.Errorf("fixture 22-record-update-delete.nsp produced %d diagnostics, want 0: %v",
			len(prog.Diagnostics), prog.Diagnostics)
	}
}
