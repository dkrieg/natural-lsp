package natural

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParser_TestDataFixtures verifies that the parser correctly handles
// all testdata fixtures (Task 8).
func TestParser_TestDataFixtures(t *testing.T) {
	testdataDir := filepath.Join("testdata", "parser")

	// Get all .nsp files in the testdata directory
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("Failed to read testdata directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".nsp" {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			// Read the fixture file
			content, err := os.ReadFile(filepath.Join(testdataDir, entry.Name()))
			if err != nil {
				t.Fatalf("Failed to read fixture file: %v", err)
			}

			// Parse the fixture
			lexer := NewLexer(string(content))
			parser := NewParser(lexer)
			prog, err := parser.Parse()

			// Parser must never crash and must always return an AST.
			if prog == nil {
				t.Fatal("Parser returned nil AST")
			}

			// Parse never returns a hard error; malformed input is surfaced as
			// diagnostics on the AST, not as a returned error (graceful degradation).
			if err != nil {
				t.Errorf("Parse returned error %v; malformed input must surface as diagnostics, not errors", err)
			}

			// Valid fixtures must produce zero diagnostics — the parser must not
			// flag good Natural. Only malformed fixtures are excluded:
			//   04-parser-parse-errors.nsp — intentionally malformed (diagnostics expected).
			switch entry.Name() {
			case "04-parser-parse-errors.nsp":
				// expectations handled elsewhere
			default:
				if len(prog.Diagnostics) != 0 {
					t.Errorf("valid fixture %s produced %d diagnostics, want 0: %+v",
						entry.Name(), len(prog.Diagnostics), prog.Diagnostics)
				}
			}
		})
	}
}

// TestParser_PartialParse_Fixture04 pins the partial-parse guarantee (Story 3 /
// M-6): a malformed statement does not prevent extraction of the valid
// statements around it, and the malformed region is flagged with a diagnostic.
// The malformed fixture ends with a valid `CALLNAT 'PROG'`; despite the earlier
// bare CALLNAT and FETCH-with-no-target, that trailing valid call must still be
// extracted (good line retained) while the bad lines are flagged (bad line
// flagged) — neither is dropped silently.
func TestParser_PartialParse_Fixture04(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "parser", "04-parser-parse-errors.nsp"))
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
		t.Errorf("Parse returned error %v; malformed input must surface as diagnostics", err)
	}

	// Bad lines flagged: malformed statements (bare CALLNAT, FETCH with no
	// target) must produce diagnostics rather than being dropped silently.
	if len(prog.Diagnostics) < 1 {
		t.Errorf("len(prog.Diagnostics) = %d, want >= 1 (malformed regions must be flagged)", len(prog.Diagnostics))
	}

	// Good line retained: the trailing valid `CALLNAT 'PROG'` must survive
	// recovery from the earlier errors.
	var foundProg bool
	for _, call := range prog.Calls {
		if call.Target == "PROG" {
			foundProg = true
			break
		}
	}
	if !foundProg {
		var targets []string
		for _, call := range prog.Calls {
			targets = append(targets, call.Target)
		}
		t.Errorf("trailing valid CALLNAT 'PROG' not retained after recovery; prog.Calls targets = %v", targets)
	}
}
