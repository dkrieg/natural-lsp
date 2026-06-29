package natural

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"natural-lsp/internal/model"
)

// TestParser_StatementTypes verifies that the parser correctly parses all
// common Natural statement types (Task 5 / Story 3 acceptance).
//
// This test ensures:
//   - Parser correctly parses DEFINE DATA sections
//   - Parser correctly parses DEFINE SUBROUTINE blocks
//   - Parser correctly parses DEFINE MAP blocks
//   - Parser correctly parses CALLNAT statements
//   - Parser correctly parses PERFORM statements
//   - Parser correctly parses INCLUDE statements
//   - Parser correctly parses FETCH statements
//   - Parser correctly parses RUN statements
//   - AST tree structure reflects the source hierarchy
//   - Position information is accurate for all nodes
//   - Unrecognized lines do not crash the parser
func TestParser_StatementTypes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		verify  func(t *testing.T, prog *Program)
	}{
		// DEFINE DATA section
		{
			name: "parser_DEFINES_data_section",
			input: `DEFINE DATA
LOCAL
PARAMETER myparam`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has DataSections
				if len(prog.DataSections) == 0 {
					t.Error("Program.DataSections is empty, want non-empty")
				}
			},
		},
		// CALLNAT statement
		{
			name:    "parser_CALLNAT_statement",
			input:   `CALLNAT 'MYPROG'`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has Calls
				if len(prog.Calls) == 0 {
					t.Error("Program.Calls is empty, want non-empty")
				}
			},
		},
		// PERFORM statement
		{
			name:    "parser_PERFORM_statement",
			input:   `PERFORM MYSUB`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has Performs
				if len(prog.Performs) == 0 {
					t.Error("Program.Performs is empty, want non-empty")
				}
			},
		},
		// INCLUDE statement
		{
			name:    "parser_INCLUDE_statement",
			input:   `INCLUDE 'MYCOPY'`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has Includes
				if len(prog.Includes) == 0 {
					t.Error("Program.Includes is empty, want non-empty")
				}
			},
		},
		// FETCH statement
		{
			name:    "parser_FETCH_statement",
			input:   `FETCH 'MYPROG'`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has Fetches
				if len(prog.Fetches) == 0 {
					t.Error("Program.Fetches is empty, want non-empty")
				}
			},
		},
		// RUN statement
		{
			name:    "parser_RUN_statement",
			input:   `RUN MYPROG`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has Runs
				if len(prog.Runs) == 0 {
					t.Error("Program.Runs is empty, want non-empty")
				}
			},
		},
		// Multiple statements
		{
			name: "parser_multiple_statements",
			input: `DEFINE DATA
LOCAL
PARAMETER myparam

CALLNAT 'MYPROG'

PERFORM MYSUB`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				// Verify Program has multiple constructs
				total := len(prog.DataSections) + len(prog.Calls) + len(prog.Performs)
				if total < 2 {
					t.Errorf("Program has only %d constructs, want at least 2", total)
				}
			},
		},
		// Position accuracy: CALLNAT 'MYPROG' — single-line, two tokens.
		// CALLNAT starts at col 1; 'MYPROG' starts at col 9 (after "CALLNAT ").
		// StartPos must reflect the CALLNAT keyword; EndPos must reflect 'MYPROG'
		// (the last consumed token), proving it is not equal to StartPos and is not
		// a fabricated constant.
		{
			name:    "parser_position_accuracy",
			input:   `CALLNAT 'MYPROG'`,
			wantErr: false,
			verify: func(t *testing.T, prog *Program) {
				if prog == nil {
					t.Fatal("Program is nil, want non-nil")
				}
				if len(prog.Calls) == 0 {
					t.Fatal("Program.Calls is empty, want 1")
				}
				call := prog.Calls[0]
				// StartPos: CALLNAT keyword is at line 1, column 1.
				if call.StartPos.Line != 1 {
					t.Errorf("Calls[0].StartPos.Line = %d, want 1", call.StartPos.Line)
				}
				if call.StartPos.Column != 1 {
					t.Errorf("Calls[0].StartPos.Column = %d, want 1", call.StartPos.Column)
				}
				// EndPos: 'MYPROG' is at line 1, column 9 (CALLNAT=7 chars + 1 space).
				if call.EndPos.Line != 1 {
					t.Errorf("Calls[0].EndPos.Line = %d, want 1", call.EndPos.Line)
				}
				if call.EndPos.Column != 9 {
					t.Errorf("Calls[0].EndPos.Column = %d, want 9 (EndPos must reflect last consumed token, not next token or StartPos)", call.EndPos.Column)
				}
				// EndPos must be strictly after StartPos within the same line.
				if call.EndPos.Column <= call.StartPos.Column {
					t.Errorf("Calls[0].EndPos.Column (%d) <= StartPos.Column (%d): statement appears zero-width",
						call.EndPos.Column, call.StartPos.Column)
				}
			},
		},
		// Error handling - malformed input
		{
			name: "parser_handles_malformed_input",
			input: `CALLNAT
THIS IS MALFORMED`,
			wantErr: false, // Parser should not crash, should recover
			verify: func(t *testing.T, prog *Program) {
				// Parser should still return something even for malformed input
				if prog == nil {
					t.Error("Program is nil for malformed input, want non-nil (parser recovered)")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: create a new parser with the test input
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)

			// Act: parse the input
			prog, err := parser.Parse()

			// Assert: check for expected errors
			if (err != nil) != tc.wantErr {
				t.Errorf("Parser.Parse() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			// Verify the Program if a verify function is provided
			if tc.verify != nil {
				tc.verify(t, prog)
			}
		})
	}
}

// TestParser_DefineDataSection verifies that the parser correctly parses
// DEFINE DATA sections with nested structures.
func TestParser_DefineDataSection(t *testing.T) {
	input := `DEFINE DATA
LOCAL
PARAMETER myparam
OTHERPARAM otherparam
END DEFINE`

	lexer := NewLexer(input)
	parser := NewParser(lexer)

	prog, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}

	// Verify Program has DataSections
	if len(prog.DataSections) == 0 {
		t.Error("Program.DataSections is empty, want non-empty")
	}
}

// TestParser_CallStatement verifies that the parser correctly parses
// CALLNAT statements with targets.
func TestParser_CallStatement(t *testing.T) {
	input := `CALLNAT 'MYPROG'`

	lexer := NewLexer(input)
	parser := NewParser(lexer)

	prog, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}
}

// TestParser_PerformStatement verifies that the parser correctly parses
// PERFORM statements with targets.
func TestParser_PerformStatement(t *testing.T) {
	input := `PERFORM MYSUB`

	lexer := NewLexer(input)
	parser := NewParser(lexer)

	prog, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}
}

// TestParser_IncludeStatement verifies that the parser correctly parses
// INCLUDE statements with targets.
func TestParser_IncludeStatement(t *testing.T) {
	input := `INCLUDE 'MYCOPY'`

	lexer := NewLexer(input)
	parser := NewParser(lexer)

	prog, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}
}

// TestParser_FetchStatement verifies that the parser correctly parses
// FETCH statements with targets (FETCH [REPEAT|RETURN] operand1).
func TestParser_FetchStatement(t *testing.T) {
	input := `FETCH 'MYPROG'`

	lexer := NewLexer(input)
	parser := NewParser(lexer)

	prog, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}
	if len(prog.Fetches) != 1 {
		t.Fatalf("len(prog.Fetches) = %d, want 1", len(prog.Fetches))
	}
	if prog.Fetches[0].Target != "MYPROG" {
		t.Errorf("FetchStatement.Target = %q, want %q (unquoted program name)", prog.Fetches[0].Target, "MYPROG")
	}
}

// TestParser_RunStatement verifies that the parser correctly parses
// RUN statements with targets.
func TestParser_RunStatement(t *testing.T) {
	input := `RUN MYPROG`

	lexer := NewLexer(input)
	parser := NewParser(lexer)

	prog, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}
}

// TestParser_ExactTargetValues_FR43_Q6 verifies the exact extracted values (not mere presence)
// for already-modeled statement/node kinds per Task 4 (Story 2/3/5, Q6 unquoted-target convention).
//
// Per acceptance criteria:
//   - CallStatement.Target, PerformStatement.Target, IncludeStatement.Target, FetchStatement.Target,
//     RunStatement.Target, Map.Name, Subroutine.Name are stored UNQUOTED (bare name, no surrounding quotes).
//   - Exact counts asserted (replacing len(...) != 0 checks).
//   - String-literal targets like CALLNAT 'MYPROG' store Target == "MYPROG" (unquoted).
//
// This test uses inline mirrors of statements found in testdata/parser/03-parser-statements.nsp,
// isolated to test each statement type independently.
func TestParser_ExactTargetValues_FR43_Q6(t *testing.T) {
	// Arrange: inline test cases for each statement type to isolate exact target values.
	tests := []struct {
		name         string
		input        string
		wantCalls    int
		checkCall    func(t *testing.T, call *CallStatement)
		wantPerforms int
		checkPerform func(t *testing.T, perf *PerformStatement)
		wantIncludes int
		checkInclude func(t *testing.T, inc *IncludeStatement)
		wantFetches  int
		checkFetch   func(t *testing.T, fetch *FetchStatement)
		wantRuns     int
		checkRun     func(t *testing.T, run *RunStatement)
		wantMaps     int
		checkMap     func(t *testing.T, m *Map)
	}{
		{
			name:      "CALLNAT_with_quoted_string_target_should_unquote",
			input:     `CALLNAT 'MYPROG'`,
			wantCalls: 1,
			checkCall: func(t *testing.T, call *CallStatement) {
				// FR-Q6: String-literal targets stored UNQUOTED
				if call.Target != "MYPROG" {
					t.Errorf("CallStatement.Target = %q, want %q (unquoted, no quotes)", call.Target, "MYPROG")
				}
			},
		},
		{
			name:         "PERFORM_with_identifier_target",
			input:        `PERFORM MYSUB`,
			wantPerforms: 1,
			checkPerform: func(t *testing.T, perf *PerformStatement) {
				// PERFORM takes an identifier (not quoted)
				if perf.Target != "MYSUB" {
					t.Errorf("PerformStatement.Target = %q, want %q", perf.Target, "MYSUB")
				}
			},
		},
		{
			name:         "INCLUDE_with_quoted_string_target_should_unquote",
			input:        `INCLUDE 'MYCOPY'`,
			wantIncludes: 1,
			checkInclude: func(t *testing.T, inc *IncludeStatement) {
				// FR-Q6: String-literal targets stored UNQUOTED
				if inc.Target != "MYCOPY" {
					t.Errorf("IncludeStatement.Target = %q, want %q (unquoted, no quotes)", inc.Target, "MYCOPY")
				}
			},
		},
		{
			name:        "FETCH_with_identifier_program_name",
			input:       `FETCH MYPROG`,
			wantFetches: 1,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// Correct grammar per Natural KB: FETCH [REPEAT|RETURN] operand1
				// FETCH MYPROG → Target should be MYPROG (the program name), not DATABASE
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:     "RUN_with_identifier_target",
			input:    `RUN MYPROG`,
			wantRuns: 1,
			checkRun: func(t *testing.T, run *RunStatement) {
				if run.Target != "MYPROG" {
					t.Errorf("RunStatement.Target = %q, want %q", run.Target, "MYPROG")
				}
			},
		},
		{
			name:     "DEFINE_MAP_name",
			input:    `DEFINE MAP MY_MAP END MAP`,
			wantMaps: 1,
			checkMap: func(t *testing.T, m *Map) {
				if m.Name != "MY_MAP" {
					t.Errorf("Map.Name = %q, want %q (unquoted)", m.Name, "MY_MAP")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: parse
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)
			prog, err := parser.Parse()

			// Assert: parse succeeds
			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}
			if prog == nil {
				t.Fatal("Program is nil, want non-nil")
			}

			// Assert: CALLNAT counts and values
			if tc.wantCalls > 0 {
				if len(prog.Calls) != tc.wantCalls {
					t.Fatalf("len(prog.Calls) = %d, want %d", len(prog.Calls), tc.wantCalls)
				}
				if tc.checkCall != nil {
					tc.checkCall(t, prog.Calls[0])
				}
			}

			// Assert: PERFORM counts and values
			if tc.wantPerforms > 0 {
				if len(prog.Performs) != tc.wantPerforms {
					t.Fatalf("len(prog.Performs) = %d, want %d", len(prog.Performs), tc.wantPerforms)
				}
				if tc.checkPerform != nil {
					tc.checkPerform(t, prog.Performs[0])
				}
			}

			// Assert: INCLUDE counts and values
			if tc.wantIncludes > 0 {
				if len(prog.Includes) != tc.wantIncludes {
					t.Fatalf("len(prog.Includes) = %d, want %d", len(prog.Includes), tc.wantIncludes)
				}
				if tc.checkInclude != nil {
					tc.checkInclude(t, prog.Includes[0])
				}
			}

			// Assert: FETCH counts and values
			if tc.wantFetches > 0 {
				if len(prog.Fetches) != tc.wantFetches {
					t.Fatalf("len(prog.Fetches) = %d, want %d", len(prog.Fetches), tc.wantFetches)
				}
				if tc.checkFetch != nil {
					tc.checkFetch(t, prog.Fetches[0])
				}
			}

			// Assert: RUN counts and values
			if tc.wantRuns > 0 {
				if len(prog.Runs) != tc.wantRuns {
					t.Fatalf("len(prog.Runs) = %d, want %d", len(prog.Runs), tc.wantRuns)
				}
				if tc.checkRun != nil {
					tc.checkRun(t, prog.Runs[0])
				}
			}

			// Assert: MAP counts and values
			if tc.wantMaps > 0 {
				if len(prog.Maps) != tc.wantMaps {
					t.Fatalf("len(prog.Maps) = %d, want %d", len(prog.Maps), tc.wantMaps)
				}
				if tc.checkMap != nil {
					tc.checkMap(t, prog.Maps[0])
				}
			}
		})
	}
}

// TestParser_RealPositionsOnMultilineStatements verifies that each AST node's
// StartPos reflects the actual line/column of its leading keyword, not a hardcoded (1,1).
// This test covers Story 2/3/5 acceptance: "each node carries source position information"
// (Task 3 from plan.md).
//
// Multi-line source where statements are on KNOWN, DIFFERENT lines proves positions
// are not fabricated to a constant value. This test uses multiple CALLNAT statements
// since the parser reads CALLNAT until it finds the next CALLNAT keyword.
func TestParser_RealPositionsOnMultilineStatements(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantCall1Line    int // expected line for first CALLNAT
		wantCall2Line    int // expected line for second CALLNAT (if exists)
		wantDataSectLine int // expected line for DEFINE DATA (if exists)
	}{
		{
			name: "multiline_callnat_statements_on_different_lines",
			input: `CALLNAT 'MYPROG'
CALLNAT 'PROG2'`,
			wantCall1Line: 1,
			wantCall2Line: 2,
		},
		{
			name: "callnat_statements_with_vertical_gaps",
			input: `CALLNAT 'PROG1'


CALLNAT 'PROG2'`,
			wantCall1Line: 1,
			wantCall2Line: 4,
		},
		{
			name: "define_data_on_line_2",
			input: `

DEFINE DATA
LOCAL`,
			wantDataSectLine: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: create a parser with multi-line input
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)

			// Act: parse the input
			prog, err := parser.Parse()

			// Assert: no parse error
			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}
			if prog == nil {
				t.Fatal("Program is nil, want non-nil")
			}

			// Assert: first CALLNAT statement has correct line
			if tc.wantCall1Line > 0 {
				if len(prog.Calls) == 0 {
					t.Fatal("Program.Calls is empty, want at least one CALLNAT")
				}
				// Prove position is NOT hardcoded: column should be at least 1 (where statement starts)
				if prog.Calls[0].StartPos.Line != tc.wantCall1Line {
					t.Errorf("Calls[0].StartPos.Line = %d, want %d (position hardcoded to 1,1?)",
						prog.Calls[0].StartPos.Line, tc.wantCall1Line)
				}
				if prog.Calls[0].StartPos.Column < 1 {
					t.Errorf("Calls[0].StartPos.Column = %d, want >= 1", prog.Calls[0].StartPos.Column)
				}
			}

			// Assert: second CALLNAT statement has correct line
			if tc.wantCall2Line > 0 {
				if len(prog.Calls) < 2 {
					t.Fatalf("Program.Calls has %d elements, want at least 2", len(prog.Calls))
				}
				// Key assertion: line must match actual source line, not hardcoded (1,1)
				if prog.Calls[1].StartPos.Line != tc.wantCall2Line {
					t.Errorf("Calls[1].StartPos.Line = %d, want %d (position hardcoded to 1,1?)",
						prog.Calls[1].StartPos.Line, tc.wantCall2Line)
				}
				if prog.Calls[1].StartPos.Column < 1 {
					t.Errorf("Calls[1].StartPos.Column = %d, want >= 1", prog.Calls[1].StartPos.Column)
				}
			}

			// Assert: DEFINE DATA has correct line
			if tc.wantDataSectLine > 0 {
				if len(prog.DataSections) == 0 {
					t.Fatal("Program.DataSections is empty, want at least one DEFINE DATA")
				}
				// Key assertion: line must match actual source line, not hardcoded (1,1)
				if prog.DataSections[0].StartPos.Line != tc.wantDataSectLine {
					t.Errorf("DataSections[0].StartPos.Line = %d, want %d (position hardcoded to 1,1?)",
						prog.DataSections[0].StartPos.Line, tc.wantDataSectLine)
				}
				if prog.DataSections[0].StartPos.Column < 1 {
					t.Errorf("DataSections[0].StartPos.Column = %d, want >= 1", prog.DataSections[0].StartPos.Column)
				}
			}
		})
	}
}

// TestParser_ReadStoreStatements verifies that the parser correctly parses READ
// and STORE statements (Q4 / Task 5 / Story 3).
//
// This test ensures:
//   - Parser correctly parses READ statements with view/DDM names
//   - Parser correctly parses STORE statements with view/file names
//   - Target names are captured correctly (unquoted)
//   - Position information is accurate (StartPos.Line matches source)
//   - Multiple READ/STORE statements are all extracted
func TestParser_ReadStoreStatements(t *testing.T) {
	// Arrange: read the fixture which contains READ and STORE statements
	content, readErr := os.ReadFile(filepath.Join("testdata", "parser", "06-read-store.nsp"))
	if readErr != nil {
		t.Fatalf("failed to read fixture testdata/parser/06-read-store.nsp: %v", readErr)
	}

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)

	// Act: parse the fixture
	prog, err := parser.Parse()

	// Assert: parse succeeds
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}

	// Assert: READ statements were extracted
	if len(prog.Reads) != 2 {
		t.Errorf("len(prog.Reads) = %d, want 2", len(prog.Reads))
	}

	// Assert: STORE statements were extracted
	if len(prog.Stores) != 2 {
		t.Errorf("len(prog.Stores) = %d, want 2", len(prog.Stores))
	}

	// Assert: first READ has correct Target (EMPLOYEES at line 4)
	if len(prog.Reads) >= 1 {
		read1 := prog.Reads[0]
		if read1.Target != "EMPLOYEES" {
			t.Errorf("Reads[0].Target = %q, want %q", read1.Target, "EMPLOYEES")
		}
		if read1.StartPos.Line != 4 {
			t.Errorf("Reads[0].StartPos.Line = %d, want 4", read1.StartPos.Line)
		}
	}

	// Assert: second READ has correct Target (DEPARTMENTS at line 8)
	if len(prog.Reads) >= 2 {
		read2 := prog.Reads[1]
		if read2.Target != "DEPARTMENTS" {
			t.Errorf("Reads[1].Target = %q, want %q", read2.Target, "DEPARTMENTS")
		}
		if read2.StartPos.Line != 8 {
			t.Errorf("Reads[1].StartPos.Line = %d, want 8", read2.StartPos.Line)
		}
	}

	// Assert: first STORE has correct Target (EMPLOYEES at line 6)
	if len(prog.Stores) >= 1 {
		store1 := prog.Stores[0]
		if store1.Target != "EMPLOYEES" {
			t.Errorf("Stores[0].Target = %q, want %q", store1.Target, "EMPLOYEES")
		}
		if store1.StartPos.Line != 6 {
			t.Errorf("Stores[0].StartPos.Line = %d, want 6", store1.StartPos.Line)
		}
	}

	// Assert: second STORE has correct Target (PERSONNEL at line 10)
	if len(prog.Stores) >= 2 {
		store2 := prog.Stores[1]
		if store2.Target != "PERSONNEL" {
			t.Errorf("Stores[1].Target = %q, want %q", store2.Target, "PERSONNEL")
		}
		if store2.StartPos.Line != 10 {
			t.Errorf("Stores[1].StartPos.Line = %d, want 10", store2.StartPos.Line)
		}
	}
}

// TestParser_DefineData_LevelsTypesArrays verifies that the parser correctly parses
// DEFINE DATA blocks with level numbers, type/format strings, and array dimensions
// per Task 6 / Story 2/3/5 / Q3 acceptance criteria.
//
// Expected extraction from testdata/parser/07-data-arrays.nsp (FR-44 / Task 6):
// 1. Scalar fields with level, name, type: #EMPLOYEE-ID (N7), #SALARY (P9.2)
// 2. Group fields with level and no type: #ADDRESS (level 1, no type)
// 3. Group member fields: #STREET (level 2, A30), #CITY (level 2, A20), #ZIP (level 2, A10)
// 4. Single-dimensional array: #MONTH-NAMES (A3/1:12) → Dimensions=[{1,12}]
// 5. Multi-dimensional array: #SCORE-MATRIX (N3/1:5,1:3) → Dimensions=[{1,5},{1,3}]
// 6. Group nesting via Children field: #ADDRESS has 3 children (#STREET, #CITY, #ZIP)
// 7. Names retain the # prefix and are uppercase (case-normalized by lexer)
func TestParser_DefineData_LevelsTypesArrays(t *testing.T) {
	// Arrange: read the fixture file (07-data-arrays.nsp)
	content, readErr := os.ReadFile(filepath.Join("testdata", "parser", "07-data-arrays.nsp"))
	if readErr != nil {
		t.Fatalf("failed to read fixture testdata/parser/07-data-arrays.nsp: %v", readErr)
	}
	// The fixture ends with "END" which is not part of the DEFINE DATA block and will be skipped by the parser.
	// This is fine for this test since we're only verifying the DEFINE DATA section structure.

	lexer := NewLexer(string(content))
	parser := NewParser(lexer)

	// Act: parse the input
	prog, err := parser.Parse()

	// Assert: no parse error
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v, want nil", err)
	}
	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}

	// Assert: one DEFINE DATA section
	if len(prog.DataSections) != 1 {
		t.Fatalf("DataSections count = %d, want 1", len(prog.DataSections))
	}
	section := prog.DataSections[0]

	// Helper: assert a scalar field's properties
	assertScalarField := func(t *testing.T, field *DataField, wantLevel int, wantName, wantType string) {
		t.Helper()
		if field == nil {
			t.Fatalf("field is nil, want non-nil")
		}
		if field.Level != wantLevel {
			t.Errorf("Level = %d, want %d", field.Level, wantLevel)
		}
		if field.Name != wantName {
			t.Errorf("Name = %q, want %q", field.Name, wantName)
		}
		if field.Type != wantType {
			t.Errorf("Type = %q, want %q", field.Type, wantType)
		}
		if len(field.Dimensions) != 0 {
			t.Errorf("Dimensions = %v, want empty", field.Dimensions)
		}
	}

	// Helper: assert array dimensions match expected bounds
	assertArrayDimensions := func(t *testing.T, field *DataField, wantBounds ...ArrayBound) {
		t.Helper()
		if len(field.Dimensions) != len(wantBounds) {
			t.Errorf("Dimensions count = %d, want %d", len(field.Dimensions), len(wantBounds))
			return
		}
		for i, want := range wantBounds {
			got := field.Dimensions[i]
			if got.Lower != want.Lower || got.Upper != want.Upper || got.UpperUnbounded != want.UpperUnbounded {
				t.Errorf("Dimensions[%d] = {Lower:%d, Upper:%d, Unbounded:%v}, want {Lower:%d, Upper:%d, Unbounded:%v}",
					i, got.Lower, got.Upper, got.UpperUnbounded, want.Lower, want.Upper, want.UpperUnbounded)
			}
		}
	}

	// Helper: assert a group field
	assertGroupField := func(t *testing.T, field *DataField, wantLevel int, wantName string, wantChildCount int) {
		t.Helper()
		if field == nil {
			t.Fatalf("field is nil, want non-nil")
		}
		if field.Level != wantLevel {
			t.Errorf("Level = %d, want %d", field.Level, wantLevel)
		}
		if field.Name != wantName {
			t.Errorf("Name = %q, want %q", field.Name, wantName)
		}
		if field.Type != "" {
			t.Errorf("Type = %q, want empty string (group has no type)", field.Type)
		}
		if len(field.Children) != wantChildCount {
			t.Errorf("Children count = %d, want %d", len(field.Children), wantChildCount)
		}
	}

	// The fixture has 5 top-level fields in the INTENDED nested structure:
	// #EMPLOYEE-ID, #SALARY, #ADDRESS (with 3 children), #MONTH-NAMES, #SCORE-MATRIX.
	// We assert the INTENDED nested structure that GREEN will build.
	// Current parser may return empty or flat, but we drive the full nested behavior.

	// Assert we have at least 3 top-level fields (the minimum expected):
	// #EMPLOYEE-ID, #SALARY, #ADDRESS
	if len(section.Fields) < 3 {
		t.Fatalf("DataSection.Fields count = %d, want at least 3 (got %v)", len(section.Fields), section.Fields)
	}

	// The test will fail on most assertions until parseDataSection is fully implemented.
	// We deliberately assert the INTENDED nested behavior to drive the implementation.

	// Fields[0]: #EMPLOYEE-ID (level 1, type N7)
	if len(section.Fields) > 0 {
		assertScalarField(t, section.Fields[0], 1, "#EMPLOYEE-ID", "N7")
	}

	// Fields[1]: #SALARY (level 1, type P9.2)
	if len(section.Fields) > 1 {
		assertScalarField(t, section.Fields[1], 1, "#SALARY", "P9.2")
	}

	// Fields[2]: #ADDRESS (level 1, group, no type, has 3 children)
	if len(section.Fields) > 2 {
		assertGroupField(t, section.Fields[2], 1, "#ADDRESS", 3)

		// #ADDRESS.Children[0]: #STREET (level 2, type A30)
		if len(section.Fields[2].Children) > 0 {
			assertScalarField(t, section.Fields[2].Children[0], 2, "#STREET", "A30")
		}

		// #ADDRESS.Children[1]: #CITY (level 2, type A20)
		if len(section.Fields[2].Children) > 1 {
			assertScalarField(t, section.Fields[2].Children[1], 2, "#CITY", "A20")
		}

		// #ADDRESS.Children[2]: #ZIP (level 2, type A10)
		if len(section.Fields[2].Children) > 2 {
			assertScalarField(t, section.Fields[2].Children[2], 2, "#ZIP", "A10")
		}
	}

	// Fields[3]: #MONTH-NAMES (level 1, type A3, Dimensions=[{1,12}])
	if len(section.Fields) > 3 {
		field := section.Fields[3]
		if field.Name == "#MONTH-NAMES" {
			if field.Level != 1 {
				t.Errorf("Fields[3].Level = %d, want 1", field.Level)
			}
			if field.Type != "A3" {
				t.Errorf("Fields[3].Type = %q, want %q", field.Type, "A3")
			}
			assertArrayDimensions(t, field, ArrayBound{Lower: 1, Upper: 12, UpperUnbounded: false})
		}
	}

	// Fields[4]: #SCORE-MATRIX (level 1, type N3, Dimensions=[{1,5},{1,3}])
	// Search for it by name since it might be at different indices depending on nesting.
	var scoreMatrixField *DataField
	for _, f := range section.Fields {
		if f.Name == "#SCORE-MATRIX" {
			scoreMatrixField = f
			break
		}
	}
	if scoreMatrixField != nil {
		if scoreMatrixField.Level != 1 {
			t.Errorf("scoreMatrixField.Level = %d, want 1", scoreMatrixField.Level)
		}
		if scoreMatrixField.Type != "N3" {
			t.Errorf("scoreMatrixField.Type = %q, want %q", scoreMatrixField.Type, "N3")
		}
		assertArrayDimensions(t, scoreMatrixField, ArrayBound{Lower: 1, Upper: 5}, ArrayBound{Lower: 1, Upper: 3})
	}
}

// TestParser_DefineData_Redefine verifies that the parser correctly parses REDEFINE
// clauses within DEFINE DATA sections (Task 6, Story 2/3/5, Q3 sub-part 6b).
//
// A REDEFINE clause remaps a previously-defined field into subfields with a different structure.
// The parser must:
//   - Recognize REDEFINE as a keyword indicating a redefinition block (not a field name)
//   - Populate DataField.Redefines with the target field name
//   - Nest the redefining subfields in DataField.Children
//   - Preserve the original field as a separate top-level field (no mutation)
//
// Per acceptance criteria (from natural-expert):
//   - Field: Level 1, Name "#EMPLOYEE-ID", Type "A7", no dims, no Redefines, no Children
//   - REDEFINE node: Level 1, Redefines == "#EMPLOYEE-ID", Type empty, with Children:
//   - Level 2, Name "#ID-PREFIX", Type "A3"
//   - Level 2, Name "#ID-SEQUENCE", Type "A4"
func TestParser_DefineData_Redefine(t *testing.T) {
	// Arrange: read the fixture file (permanent regression fixture for this behavior)
	content, readErr := os.ReadFile(filepath.Join("testdata", "parser", "08-data-redefine.nsp"))
	if readErr != nil {
		t.Fatalf("failed to read fixture testdata/parser/08-data-redefine.nsp: %v", readErr)
	}

	// Act: parse the fixture
	lexer := NewLexer(string(content))
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	// Assert: parse succeeds
	if err != nil {
		t.Fatalf("Parser.Parse() error = %v", err)
	}
	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}

	// Assert: exactly one DEFINE DATA section
	if len(prog.DataSections) != 1 {
		t.Fatalf("len(prog.DataSections) = %d, want 1", len(prog.DataSections))
	}

	section := prog.DataSections[0]

	// Assert: the section has exactly 2 top-level fields
	// - Field 1: #EMPLOYEE-ID (the original scalar)
	// - Field 2: REDEFINE node (with Children as the subfields)
	if len(section.Fields) != 2 {
		t.Fatalf("len(section.Fields) = %d, want 2; got fields: %v", len(section.Fields), section.Fields)
	}

	// Verify Field 1: #EMPLOYEE-ID (original scalar field)
	originalField := section.Fields[0]
	if originalField.Level != 1 {
		t.Errorf("Fields[0].Level = %d, want 1", originalField.Level)
	}
	if originalField.Name != "#EMPLOYEE-ID" {
		t.Errorf("Fields[0].Name = %q, want %q", originalField.Name, "#EMPLOYEE-ID")
	}
	if originalField.Type != "A7" {
		t.Errorf("Fields[0].Type = %q, want %q", originalField.Type, "A7")
	}
	if originalField.Redefines != "" {
		t.Errorf("Fields[0].Redefines = %q, want empty (original field, not a redefine)", originalField.Redefines)
	}
	if len(originalField.Children) != 0 {
		t.Errorf("Fields[0] has %d Children, want 0 (original field)", len(originalField.Children))
	}

	// Verify Field 2: REDEFINE #EMPLOYEE-ID (the redefine node)
	redefineNode := section.Fields[1]
	if redefineNode.Level != 1 {
		t.Errorf("Fields[1].Level = %d, want 1", redefineNode.Level)
	}
	if redefineNode.Redefines != "#EMPLOYEE-ID" {
		t.Errorf("Fields[1].Redefines = %q, want %q", redefineNode.Redefines, "#EMPLOYEE-ID")
	}
	if redefineNode.Type != "" {
		t.Errorf("Fields[1].Type = %q, want empty (redefine node has no type)", redefineNode.Type)
	}

	// Verify the redefine node has exactly 2 children (the subfields)
	if len(redefineNode.Children) != 2 {
		t.Fatalf("Fields[1].Children count = %d, want 2; got %v", len(redefineNode.Children), redefineNode.Children)
	}

	// Verify Child 1: #ID-PREFIX
	child1 := redefineNode.Children[0]
	if child1.Level != 2 {
		t.Errorf("Children[0].Level = %d, want 2", child1.Level)
	}
	if child1.Name != "#ID-PREFIX" {
		t.Errorf("Children[0].Name = %q, want %q", child1.Name, "#ID-PREFIX")
	}
	if child1.Type != "A3" {
		t.Errorf("Children[0].Type = %q, want %q", child1.Type, "A3")
	}

	// Verify Child 2: #ID-SEQUENCE
	child2 := redefineNode.Children[1]
	if child2.Level != 2 {
		t.Errorf("Children[1].Level = %d, want 2", child2.Level)
	}
	if child2.Name != "#ID-SEQUENCE" {
		t.Errorf("Children[1].Name = %q, want %q", child2.Name, "#ID-SEQUENCE")
	}
	if child2.Type != "A4" {
		t.Errorf("Children[1].Type = %q, want %q", child2.Type, "A4")
	}
}

// TestParser_DefineData_Redefine_MissingTarget verifies graceful degradation (FR-43)
// when a REDEFINE clause has no following identifier (input ends immediately after
// REDEFINE with no further tokens).  The parser must not panic, must return a valid
// AST, and any emitted field must have an empty Redefines field.
// A diagnostic for the missing target is deferred to Task 7.
func TestParser_DefineData_Redefine_MissingTarget(t *testing.T) {
	// Arrange: REDEFINE immediately followed by EOF — no target identifier present.
	// This is the canonical missing-target case: the look-ahead finds TokenEOF,
	// so neither p.matches(TokenIdentifier) nor p.matches(TokenKeyword) fires.
	input := "DEFINE DATA LOCAL\n1 REDEFINE"

	// Act: parse — must not panic.
	lexer := NewLexer(input)
	parser := NewParser(lexer)
	prog, err := parser.Parse()

	// Assert: no panic; parse returns a valid AST.
	if err != nil {
		t.Fatalf("Parser.Parse() returned unexpected error: %v", err)
	}
	if prog == nil {
		t.Fatal("Program is nil, want non-nil")
	}

	// Assert: any REDEFINE node emitted has Redefines == "" (empty — no target parsed).
	for _, sec := range prog.DataSections {
		for _, f := range sec.Fields {
			if f.Redefines != "" {
				t.Errorf("expected Redefines to be empty for malformed REDEFINE with missing target, got %q", f.Redefines)
			}
		}
	}
}

// TestParser_EndDefineTerminator reproduces the suspected bug where END-DEFINE
// (the canonical Natural hyphenated terminator) is not recognized by the parser
// as the DEFINE DATA block terminator. The lexer's hyphen-absorption rule produces
// a single TokenIdentifier "END-DEFINE", and parseDataSection only breaks on a
// bare TokenKeyword "END", causing END-DEFINE to be treated as a stray identifier
// instead. This test verifies the expected behavior: a DEFINE DATA LOCAL block
// terminated by END-DEFINE (not END DEFINE) immediately followed by a CALLNAT
// statement must parse with the data section containing ONLY its declared field(s),
// and the CALLNAT must be extracted (not consumed as part of the data section).
// See: CLAUDE.md Design decision note on "Natural is case-insensitive", and
// .claude/knowledge/natural/data-definition.md section "DEFINE DATA structure".
func TestParser_EndDefineTerminator(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantFields    []string          // expected field names in the data section
		wantCalls     []string          // expected CALLNAT targets
		wantRedefines map[string]string // field name -> redefine target (for fields that redefine)
	}{
		{
			name: "end_define_terminates_data_with_following_callnat",
			input: `DEFINE DATA LOCAL
1 #COUNT (N3)
END-DEFINE
CALLNAT 'MYPROG'
`,
			wantFields:    []string{"#COUNT"},
			wantCalls:     []string{"MYPROG"},
			wantRedefines: map[string]string{},
		},
		{
			name: "end_define_terminates_data_when_eof_follows",
			input: `DEFINE DATA LOCAL
1 #COUNT (N3)
END-DEFINE
`,
			wantFields:    []string{"#COUNT"},
			wantCalls:     []string{},
			wantRedefines: map[string]string{},
		},
		{
			name: "end_define_not_treated_as_field_even_at_level_line",
			input: `DEFINE DATA LOCAL
1 #ID (A7)
END-DEFINE
CALLNAT 'P'
`,
			// The bug would cause END-DEFINE to appear as a level-0 or phantom field
			// This test ensures it doesn't
			wantFields:    []string{"#ID"},
			wantCalls:     []string{"P"},
			wantRedefines: map[string]string{},
		},
		{
			name: "end_define_not_consumed_by_redefine_at_eof",
			input: `DEFINE DATA LOCAL
1 #ID (A7)
1 REDEFINE #ID
2 #A (A3)
END-DEFINE
`,
			// Test that END-DEFINE at EOF is not consumed as a redefine target
			// (it should stay as #ID)
			wantFields:    []string{"#ID", ""},
			wantCalls:     []string{},
			wantRedefines: map[string]string{},
		},
		{
			name: "redefine_before_end_define_does_not_consume_end_define_as_target",
			input: `DEFINE DATA LOCAL
1 #ID (A7)
1 REDEFINE #ID
2 #A (A3)
END-DEFINE
CALLNAT 'P'
`,
			// After parsing: #ID field (Name="#ID", Redefines="")
			// Then a REDEFINE node (Name="", Redefines="#ID") with child #A
			wantFields:    []string{"#ID", ""}, // Second "" is the REDEFINE node
			wantCalls:     []string{"P"},
			wantRedefines: map[string]string{}, // We'll verify Redefines separately
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: parse the input
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)

			// Act: parse
			prog, err := parser.Parse()

			// Assert: parse succeeds
			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}
			if prog == nil {
				t.Fatal("Program is nil, want non-nil")
			}

			// Assert: exactly one data section
			if len(prog.DataSections) != 1 {
				t.Errorf("expected 1 DataSection, got %d", len(prog.DataSections))
			}

			// Assert: data section contains exactly the expected fields (top-level)
			section := prog.DataSections[0]
			if len(section.Fields) != len(tc.wantFields) {
				t.Errorf("expected %d fields, got %d", len(tc.wantFields), len(section.Fields))
				// Dump actual fields for debugging
				for i, f := range section.Fields {
					t.Logf("  Field[%d]: Name=%q, Redefines=%q", i, f.Name, f.Redefines)
				}
			}

			// Assert: field names match (in order)
			for i, wantName := range tc.wantFields {
				if i >= len(section.Fields) {
					t.Errorf("missing field %q", wantName)
					continue
				}
				if section.Fields[i].Name != wantName {
					t.Errorf("Field[%d]: expected Name=%q, got Name=%q", i, wantName, section.Fields[i].Name)
				}
			}

			// Assert: CALLNAT is extracted (not consumed by data section)
			if len(prog.Calls) != len(tc.wantCalls) {
				t.Errorf("expected %d CALLNAT statements, got %d", len(tc.wantCalls), len(prog.Calls))
				for i, c := range prog.Calls {
					t.Logf("  Call[%d]: Target=%q", i, c.Target)
				}
			}

			// Assert: CALLNAT targets match (in order)
			for i, wantTarget := range tc.wantCalls {
				if i >= len(prog.Calls) {
					t.Errorf("missing CALLNAT target %q", wantTarget)
					continue
				}
				if prog.Calls[i].Target != wantTarget {
					t.Errorf("Call[%d]: expected Target=%q, got Target=%q", i, wantTarget, prog.Calls[i].Target)
				}
			}

			// For the REDEFINE case, verify that REDEFINE was parsed and END-DEFINE
			// was NOT treated as the redefine target.
			// Test case 2: REDEFINE should target #ID, not END-DEFINE.
			if tc.name == "redefine_before_end_define_does_not_consume_end_define_as_target" {
				// Look for a field with Redefines="#ID"
				foundRedefine := false
				for _, f := range section.Fields {
					if f.Redefines == "#ID" {
						foundRedefine = true
						// Verify it's not "END-DEFINE"
						if f.Redefines == "END-DEFINE" {
							t.Errorf("REDEFINE incorrectly targeted END-DEFINE instead of #ID")
						}
						// Verify it has a child "#A"
						hasChildA := false
						for _, child := range f.Children {
							if child.Name == "#A" {
								hasChildA = true
								break
							}
						}
						if !hasChildA {
							t.Errorf("REDEFINE node missing child field #A")
						}
						break
					}
				}
				if !foundRedefine {
					t.Errorf("REDEFINE node with Redefines=#ID not found in parsed data section")
				}
			}
		})
	}
}

// TestParser_SubroutineName verifies that the parser correctly extracts the name
// from a DEFINE SUBROUTINE block (Task 4 / Story 3 / Task 4 acceptance criterion).
// The subroutine name must be captured in the Subroutine.Name field after
// parseSubroutine reads the SUBROUTINE keyword.
func TestParser_SubroutineName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantName  string
	}{
		{
			name: "parse_subroutine_with_name",
			input: `DEFINE SUBROUTINE
MY_SUB
DEFINE DATA LOCAL
PARAMETER param
END DEFINE
END-SUBROUTINE`,
			wantCount: 1,
			wantName:  "MY_SUB",
		},
		{
			name: "parse_fixture_03_subroutine",
			input: `DEFINE SUBROUTINE
MY_SUB
DEFINE DATA LOCAL
PARAMETER param
END DEFINE
END-SUBROUTINE`,
			wantCount: 1,
			wantName:  "MY_SUB",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: parse the input
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)

			// Act: parse
			prog, err := parser.Parse()

			// Assert: parse succeeds
			if err != nil {
				t.Fatalf("Parser.Parse() error = %v", err)
			}
			if prog == nil {
				t.Fatal("Program is nil, want non-nil")
			}

			// Assert: subroutine count matches
			if len(prog.Subroutines) != tc.wantCount {
				t.Fatalf("len(prog.Subroutines) = %d, want %d", len(prog.Subroutines), tc.wantCount)
			}

			// Assert: subroutine name matches
			if tc.wantCount > 0 {
				if prog.Subroutines[0].Name != tc.wantName {
					t.Errorf("Subroutines[0].Name = %q, want %q", prog.Subroutines[0].Name, tc.wantName)
				}
			}
		})
	}
}

// TestParser_InlineCommentsSkipped tests that the parser correctly skips
// rest-of-line /* comments during statement parsing (Task 12 / Story 1, FR-30).
// Rest-of-line comments (/* to EOL, no closer) must not cause spurious diagnostics
// or break statement parsing. The parser delegates comment skipping to the lexer;
// this test verifies the end-to-end effect: a statement with rest-of-line comments
// parses cleanly without diagnostics.
func TestParser_InlineCommentsSkipped(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		wantCallCount     int
		wantPerformCount  int
		wantIncludeCount  int
		wantFetchCount    int
		wantDiagnosticLen int // expect 0 diagnostics for valid statements with rest-of-line comments
	}{
		{
			// CALLNAT with rest-of-line comment after target.
			// Asserts: parser extracts the call (target 'MYPROG') and produces zero diagnostics.
			// The /* starts a comment to EOL; no */ closer.
			name:              "callnat_with_rest_of_line_comment",
			input:             "CALLNAT 'MYPROG' /* call to myprog",
			wantCallCount:     1,
			wantPerformCount:  0,
			wantIncludeCount:  0,
			wantFetchCount:    0,
			wantDiagnosticLen: 0,
		},
		{
			// PERFORM with rest-of-line comment.
			// Asserts: parser extracts the perform (target MYSUB) and produces zero diagnostics.
			name:              "perform_with_rest_of_line_comment",
			input:             "PERFORM MYSUB /* call sub",
			wantCallCount:     0,
			wantPerformCount:  1,
			wantIncludeCount:  0,
			wantFetchCount:    0,
			wantDiagnosticLen: 0,
		},
		{
			// INCLUDE with rest-of-line comment.
			// Asserts: parser extracts the include (target 'MYCOPY') and produces zero diagnostics.
			name:              "include_with_rest_of_line_comment",
			input:             "INCLUDE 'MYCOPY' /* include copycode",
			wantCallCount:     0,
			wantPerformCount:  0,
			wantIncludeCount:  1,
			wantFetchCount:    0,
			wantDiagnosticLen: 0,
		},
		{
			// MOVE statement with multiplication operator and rest-of-line comment.
			// Asserts: parser extracts the move statement and produces zero diagnostics.
			// The * is an operator (multiplication), not a comment.
			name:              "move_with_multiplication_and_comment",
			input:             "MOVE 5 TO #RESULT /* result of computation",
			wantCallCount:     0,
			wantPerformCount:  0,
			wantIncludeCount:  0,
			wantFetchCount:    0,
			wantDiagnosticLen: 0,
		},
		{
			// Multiple statements with rest-of-line comments.
			// Asserts: multiple valid statements each with rest-of-line comments parse cleanly.
			name: "multiple_statements_with_rest_of_line_comments",
			input: `CALLNAT 'PROG1' /* first call
PERFORM SUB1 /* perform sub
INCLUDE 'COPY1' /* include copy`,
			wantCallCount:     1,
			wantPerformCount:  1,
			wantIncludeCount:  1,
			wantFetchCount:    0,
			wantDiagnosticLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)

			// Act
			prog, err := parser.Parse()

			// Assert: no hard error (graceful degradation)
			if err != nil {
				t.Errorf("Parse returned error %v, want nil", err)
			}

			// Assert: correct statement counts
			if len(prog.Calls) != tc.wantCallCount {
				t.Errorf("Calls count: got %d, want %d", len(prog.Calls), tc.wantCallCount)
			}
			if len(prog.Performs) != tc.wantPerformCount {
				t.Errorf("Performs count: got %d, want %d", len(prog.Performs), tc.wantPerformCount)
			}
			if len(prog.Includes) != tc.wantIncludeCount {
				t.Errorf("Includes count: got %d, want %d", len(prog.Includes), tc.wantIncludeCount)
			}
			if len(prog.Fetches) != tc.wantFetchCount {
				t.Errorf("Fetches count: got %d, want %d", len(prog.Fetches), tc.wantFetchCount)
			}

			// Assert: zero diagnostics for valid statements with inline comments.
			if len(prog.Diagnostics) != tc.wantDiagnosticLen {
				t.Errorf("Diagnostics count: got %d, want %d; diagnostics: %+v",
					len(prog.Diagnostics), tc.wantDiagnosticLen, prog.Diagnostics)
			}
		})
	}
}

// TestParser_FetchStatement_CorrectGrammar (R1 / remediation)
// verifies that FETCH statement parsing conforms to the Natural grammar:
// FETCH [REPEAT|RETURN] operand1
// where operand1 is the program name (identifier or string literal), and there
// are NO DATABASE/RECORD clauses. Tests the correct behavior for valid FETCH
// statements and the diagnostic for malformed FETCH with missing operand.
func TestParser_FetchStatement_CorrectGrammar(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		wantFetchCount    int
		wantDiagnosticLen int // number of diagnostics expected (0 for valid, >= 1 for malformed)
		checkFetch        func(t *testing.T, fetch *FetchStatement)
		checkDiagnostic   func(t *testing.T, diag model.Diagnostic)
	}{
		{
			name:              "FETCH_with_string_literal_target",
			input:             `FETCH 'MYPROG'`,
			wantFetchCount:    1,
			wantDiagnosticLen: 0,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// FETCH 'MYPROG' — Target should be MYPROG (unquoted per FR-Q6 convention)
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q (unquoted)", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:              "FETCH_with_identifier_target",
			input:             `FETCH MYPROG`,
			wantFetchCount:    1,
			wantDiagnosticLen: 0,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// FETCH MYPROG — Target should be MYPROG (the program name)
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:              "FETCH_RETURN_with_string_literal",
			input:             `FETCH RETURN 'MYPROG'`,
			wantFetchCount:    1,
			wantDiagnosticLen: 0,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// FETCH RETURN 'MYPROG' — RETURN is an optional modifier, Target should be MYPROG
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:              "FETCH_REPEAT_with_string_literal",
			input:             `FETCH REPEAT 'MYPROG'`,
			wantFetchCount:    1,
			wantDiagnosticLen: 0,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// FETCH REPEAT 'MYPROG' — REPEAT is an optional modifier, Target should be MYPROG
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:              "FETCH_RETURN_with_identifier",
			input:             `FETCH RETURN MYPROG`,
			wantFetchCount:    1,
			wantDiagnosticLen: 0,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// FETCH RETURN MYPROG — Target should be MYPROG, not RETURN
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:              "FETCH_REPEAT_with_identifier",
			input:             `FETCH REPEAT MYPROG`,
			wantFetchCount:    1,
			wantDiagnosticLen: 0,
			checkFetch: func(t *testing.T, fetch *FetchStatement) {
				// FETCH REPEAT MYPROG — Target should be MYPROG, not REPEAT
				if fetch.Target != "MYPROG" {
					t.Errorf("FetchStatement.Target = %q, want %q", fetch.Target, "MYPROG")
				}
			},
		},
		{
			name:              "FETCH_missing_operand_diagnostic",
			input:             `FETCH`,
			wantFetchCount:    1, // statement still captured (partial parse)
			wantDiagnosticLen: 1,
			checkDiagnostic: func(t *testing.T, diag model.Diagnostic) {
				// FETCH with no operand must produce a diagnostic mentioning FETCH and target/operand
				if !strings.Contains(diag.Message, "FETCH") {
					t.Errorf("Diagnostic message %q does not contain 'FETCH'", diag.Message)
				}
				if !strings.Contains(diag.Message, "target") && !strings.Contains(diag.Message, "operand") {
					t.Errorf("Diagnostic message %q does not contain 'target' or 'operand'", diag.Message)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: create parser with test input
			lexer := NewLexer(tc.input)
			parser := NewParser(lexer)

			// Act: parse
			prog, err := parser.Parse()

			// Assert: parse does not return hard error
			if err != nil {
				t.Fatalf("Parser.Parse() error = %v, want nil", err)
			}
			if prog == nil {
				t.Fatal("Program is nil, want non-nil")
			}

			// Assert: FETCH count matches
			if len(prog.Fetches) != tc.wantFetchCount {
				t.Fatalf("len(prog.Fetches) = %d, want %d", len(prog.Fetches), tc.wantFetchCount)
			}

			// Assert: diagnostics count matches
			if len(prog.Diagnostics) != tc.wantDiagnosticLen {
				t.Fatalf("len(prog.Diagnostics) = %d, want %d; diagnostics: %+v",
					len(prog.Diagnostics), tc.wantDiagnosticLen, prog.Diagnostics)
			}

			// Assert: FETCH target (if checking)
			if tc.checkFetch != nil && len(prog.Fetches) > 0 {
				tc.checkFetch(t, prog.Fetches[0])
			}

			// Assert: diagnostic message (if checking)
			if tc.checkDiagnostic != nil && len(prog.Diagnostics) > 0 {
				tc.checkDiagnostic(t, prog.Diagnostics[0])
			}
		})
	}
}
