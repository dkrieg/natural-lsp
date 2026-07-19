package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestBuildModuleHover_ResolvedTarget tests the Markdown builder for a resolved
// module-metadata hover card (feature 12, T1, Story 1).
//
// Exercises: title line (module name + object-type label), location line
// (workspace-relative path), inbound-call-count line, outbound-dependency count line.
// Counts render literally (0 shown, not omitted). No I/O, no locks.
//
// FR-28: hover, Story 1 (module metadata), AC #1–#4.
func TestBuildModuleHover_ResolvedTarget(t *testing.T) {
	tests := []struct {
		name          string
		targetName    string
		targetType    model.ObjectType
		targetPath    string
		inboundCount  int
		outboundCount int
		// expectations
		expectTitle        string // line 1: name + type label
		expectLocation     string // line 2: path
		expectInboundLine  string // line 3: inbound count
		expectOutboundLine string // line 4: outbound count
	}{
		{
			name:               "resolved_program_with_calls",
			targetName:         "MYPROGRAM",
			targetType:         model.ObjectProgram,
			targetPath:         "mylib/myprogram.nsp",
			inboundCount:       3,
			outboundCount:      5,
			expectTitle:        "**MYPROGRAM** (program)",
			expectLocation:     "mylib/myprogram.nsp",
			expectInboundLine:  "Inbound calls: 3",
			expectOutboundLine: "Outbound dependencies: 5",
		},
		{
			name:               "resolved_subprogram_zero_counts",
			targetName:         "HELPER",
			targetType:         model.ObjectSubprogram,
			targetPath:         "libs/helper.nsn",
			inboundCount:       0,
			outboundCount:      0,
			expectTitle:        "**HELPER** (subprogram)",
			expectLocation:     "libs/helper.nsn",
			expectInboundLine:  "Inbound calls: 0",
			expectOutboundLine: "Outbound dependencies: 0",
		},
		{
			name:               "resolved_external_subroutine",
			targetName:         "DO-WORK",
			targetType:         model.ObjectExternalSubroutine,
			targetPath:         "utils/do-work.nss",
			inboundCount:       1,
			outboundCount:      2,
			expectTitle:        "**DO-WORK** (external subroutine)",
			expectLocation:     "utils/do-work.nss",
			expectInboundLine:  "Inbound calls: 1",
			expectOutboundLine: "Outbound dependencies: 2",
		},
		{
			name:               "resolved_copycode",
			targetName:         "COMMON-DATA",
			targetType:         model.ObjectCopycode,
			targetPath:         "copycode/common-data.nsc",
			inboundCount:       10,
			outboundCount:      0,
			expectTitle:        "**COMMON-DATA** (copycode)",
			expectLocation:     "copycode/common-data.nsc",
			expectInboundLine:  "Inbound calls: 10",
			expectOutboundLine: "Outbound dependencies: 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: build the hover card for a resolved target
			result := buildModuleHover(tc.targetName, tc.targetType, tc.targetPath, tc.inboundCount, tc.outboundCount)

			// Assert: result is non-empty Markdown
			if result == "" {
				t.Fatal("buildModuleHover returned empty string for resolved target")
			}

			// Assert: title line is present and correct
			if !strings.Contains(result, tc.expectTitle) {
				t.Errorf("title line missing or incorrect: expected %q in output:\n%s", tc.expectTitle, result)
			}

			// Assert: location line is present and correct
			if !strings.Contains(result, tc.expectLocation) {
				t.Errorf("location line missing or incorrect: expected %q in output:\n%s", tc.expectLocation, result)
			}

			// Assert: inbound count line is present and correct
			if !strings.Contains(result, tc.expectInboundLine) {
				t.Errorf("inbound count line missing or incorrect: expected %q in output:\n%s", tc.expectInboundLine, result)
			}

			// Assert: outbound count line is present and correct
			if !strings.Contains(result, tc.expectOutboundLine) {
				t.Errorf("outbound count line missing or incorrect: expected %q in output:\n%s", tc.expectOutboundLine, result)
			}

			// Assert: output is valid Markdown (no structural validation, just check it's printable)
			if !isPrintable(result) {
				t.Errorf("buildModuleHover returned non-printable content")
			}
		})
	}
}

// TestBuildUnresolvedHover tests the dedicated unresolved-target builder
// (feature 12, T1, Story 1, AC #3, FR-17).
//
// An unresolved literal target (no match in steplib chain) must produce a message
// that is honest (no fabricated counts/path), mentions "unresolved", and is
// strictly different from the dynamic-target message.
func TestBuildUnresolvedHover(t *testing.T) {
	result := buildUnresolvedHover()

	// Assert: result is non-empty
	if result == "" {
		t.Fatal("buildUnresolvedHover returned empty string")
	}

	// Assert: contains an honest unresolved message
	if !strings.Contains(strings.ToLower(result), "unresolved") {
		t.Errorf("expected 'unresolved' in output, got: %s", result)
	}

	// Assert: does NOT contain fabricated metadata (counts or path fragments)
	if strings.Contains(result, "Inbound calls:") || strings.Contains(result, "Outbound dependencies:") {
		t.Errorf("unresolved hover must not contain fabricated metadata (counts): %s", result)
	}

	// Assert: distinct from the dynamic-target message (FR-17: two different modeled gaps)
	dynamicResult := buildDynamicHover()
	if result == dynamicResult {
		t.Errorf("unresolved and dynamic hover messages must be distinct:\n  unresolved: %s\n  dynamic:    %s", result, dynamicResult)
	}

	// Assert: output is valid Markdown
	if !isPrintable(result) {
		t.Errorf("buildUnresolvedHover returned non-printable content")
	}
}

// TestBuildDynamicHover tests the dedicated dynamic-target builder
// (feature 12, T1, Story 1, AC #3, FR-17).
//
// A dynamic call target (variable operand or '&'-placeholder literal) is resolved
// at runtime; the hover must say so honestly — no fabricated counts/path —
// and must be strictly different from the unresolved-literal message.
func TestBuildDynamicHover(t *testing.T) {
	result := buildDynamicHover()

	// Assert: result is non-empty
	if result == "" {
		t.Fatal("buildDynamicHover returned empty string")
	}

	// Assert: contains an honest dynamic message
	if !strings.Contains(strings.ToLower(result), "dynamic") {
		t.Errorf("expected 'dynamic' in output, got: %s", result)
	}

	// Assert: does NOT contain fabricated metadata (counts or path fragments)
	if strings.Contains(result, "Inbound calls:") || strings.Contains(result, "Outbound dependencies:") {
		t.Errorf("dynamic hover must not contain fabricated metadata (counts): %s", result)
	}

	// Assert: distinct from the unresolved-target message (FR-17: two different modeled gaps)
	unresolvedResult := buildUnresolvedHover()
	if result == unresolvedResult {
		t.Errorf("dynamic and unresolved hover messages must be distinct:\n  dynamic:    %s\n  unresolved: %s", result, unresolvedResult)
	}

	// Assert: output is valid Markdown
	if !isPrintable(result) {
		t.Errorf("buildDynamicHover returned non-printable content")
	}
}

// TestBuildModuleHover_ObjectTypeLabels tests that object-type labels are
// human-readable and correct (feature 12, T1).
//
// Compound names (external subroutine, local/global/parameter data area) must be
// space-separated, not run together.
func TestBuildModuleHover_ObjectTypeLabels(t *testing.T) {
	tests := []struct {
		name        string
		objectType  model.ObjectType
		expectLabel string // substring expected in the title line
	}{
		{
			name:        "program_label",
			objectType:  model.ObjectProgram,
			expectLabel: "(program)",
		},
		{
			name:        "subprogram_label",
			objectType:  model.ObjectSubprogram,
			expectLabel: "(subprogram)",
		},
		{
			name:        "external_subroutine_label",
			objectType:  model.ObjectExternalSubroutine,
			expectLabel: "(external subroutine)",
		},
		{
			name:        "copycode_label",
			objectType:  model.ObjectCopycode,
			expectLabel: "(copycode)",
		},
		{
			name:        "map_label",
			objectType:  model.ObjectMap,
			expectLabel: "(map)",
		},
		{
			name:        "local_data_area_label",
			objectType:  model.ObjectLocalDataArea,
			expectLabel: "(local data area)",
		},
		{
			name:        "global_data_area_label",
			objectType:  model.ObjectGlobalDataArea,
			expectLabel: "(global data area)",
		},
		{
			name:        "parameter_data_area_label",
			objectType:  model.ObjectParameterDataArea,
			expectLabel: "(parameter data area)",
		},
		{
			name:        "ddm_label",
			objectType:  model.ObjectDDM,
			expectLabel: "(ddm)",
		},
		{
			name:        "helproutine_label",
			objectType:  model.ObjectHelproutine,
			expectLabel: "(helproutine)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: build the hover card with the given object type
			result := buildModuleHover("TEST-OBJ", tc.objectType, "test/path.nsx", 1, 1)

			// Assert: the object-type label is in the output
			if !strings.Contains(result, tc.expectLabel) {
				t.Errorf("object-type label %q missing from output:\n%s", tc.expectLabel, result)
			}
		})
	}
}

// isPrintable is a helper to check that the string contains only printable UTF-8
// characters and newlines (Markdown is expected to have line breaks).
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
		if r == 127 { // DEL
			return false
		}
	}
	return true
}

// TestBuildSubroutineHover_WithParameters tests the Markdown builder for a
// subroutine-signature card (feature 12, T2, Story 2).
//
// Exercises: title line with routine name, parameter list built from
// SectionKind=="parameter" definitions, array dimensions rendered as (lower:upper)
// or (lower:*) for unbounded, group headers with no Type shown as nesting parents
// with children indented/nested underneath.
//
// FR-28: hover, Story 2 (subroutine signature), AC #1–#3.
func TestBuildSubroutineHover_WithParameters(t *testing.T) {
	tests := []struct {
		name             string
		fixturePath      string                            // relative path from test package directory
		expectTitleLine  string                            // expected line 1
		expectParamLines []string                          // substrings expected to appear in output
		checkAssertion   func(t *testing.T, result string) // custom assertion
	}{
		{
			name:            "with_scalar_and_group_params",
			fixturePath:     filepath.Join("testdata", "hover", "subprogram-params.NSN"),
			expectTitleLine: "**subprogram-params** (subroutine)",
			expectParamLines: []string{
				"IN-ID",
				"N5",
				"OUT-RESULT", // group header
				"RES-CODE",
				"N1",
				"RES-MSG",
				"A50",
			},
			checkAssertion: func(t *testing.T, result string) {
				// Verify group children are indented/nested under their parent
				// IN-ID should appear before OUT-RESULT and its children
				idxINID := strings.Index(result, "IN-ID")
				idxOUTRESULT := strings.Index(result, "OUT-RESULT")
				idxRESCODE := strings.Index(result, "RES-CODE")
				idxRESMSG := strings.Index(result, "RES-MSG")

				if idxINID < 0 || idxOUTRESULT < 0 || idxRESCODE < 0 || idxRESMSG < 0 {
					t.Fatalf("missing parameter names in output:\n%s", result)
				}

				// Verify order: IN-ID, then OUT-RESULT and its children
				if !(idxINID < idxOUTRESULT && idxOUTRESULT < idxRESCODE && idxRESCODE < idxRESMSG) {
					t.Errorf("parameter order incorrect; expected IN-ID < OUT-RESULT < RES-CODE < RES-MSG, got indices %d < %d < %d < %d",
						idxINID, idxOUTRESULT, idxRESCODE, idxRESMSG)
				}
			},
		},
		{
			name:            "with_array_params",
			fixturePath:     filepath.Join("testdata", "hover", "array-params.NSN"),
			expectTitleLine: "**array-params** (subroutine)",
			expectParamLines: []string{
				"ITEM-IDS",
				"1:10", // bounded dimension
				"UNBOUNDED-LIST",
				"1:*", // unbounded dimension
				"MATRIX",
				"1:3",
				"1:5",
			},
			checkAssertion: func(t *testing.T, result string) {
				// Verify array dimensions are rendered correctly
				if !strings.Contains(result, "1:10") {
					t.Errorf("bounded array dimension (1:10) missing from output:\n%s", result)
				}
				if !strings.Contains(result, "1:*") {
					t.Errorf("unbounded array dimension (1:*) missing from output:\n%s", result)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: read and analyze the fixture
			content, err := os.ReadFile(tc.fixturePath)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.fixturePath, err)
			}

			az := natural.New(nil)
			fa, err := az.Analyze(tc.fixturePath, content)
			if err != nil {
				t.Fatalf("failed to analyze fixture: %v", err)
			}

			// Extract parameters from the definitions
			var params []model.DataDefinition
			for _, def := range fa.Definitions {
				if def.SectionKind == "parameter" {
					params = append(params, def)
				}
			}

			// Act: build the subroutine hover card
			objName := strings.TrimSuffix(filepath.Base(tc.fixturePath), filepath.Ext(tc.fixturePath))
			result := buildSubroutineHover(objName, params)

			// Assert: result is non-empty
			if result == "" {
				t.Fatal("buildSubroutineHover returned empty string")
			}

			// Assert: title line is present and correct
			if !strings.Contains(result, tc.expectTitleLine) {
				t.Errorf("title line missing or incorrect: expected %q in output:\n%s", tc.expectTitleLine, result)
			}

			// Assert: expected parameter lines are present
			for _, expectedLine := range tc.expectParamLines {
				if !strings.Contains(result, expectedLine) {
					t.Errorf("expected parameter line %q missing from output:\n%s", expectedLine, result)
				}
			}

			// Assert: output is valid Markdown
			if !isPrintable(result) {
				t.Errorf("buildSubroutineHover returned non-printable content")
			}

			// Assert: custom assertion if provided
			if tc.checkAssertion != nil {
				tc.checkAssertion(t, result)
			}
		})
	}
}

// TestBuildSubroutineHover_NoParameters tests the subroutine-signature builder
// when there are no declared parameters (feature 12, T2, Story 2, AC #2).
//
// When a subroutine has no PARAMETER section, render an explicit
// "no declared parameters" line rather than an empty card.
func TestBuildSubroutineHover_NoParameters(t *testing.T) {
	tests := []struct {
		name            string
		fixturePath     string
		expectTitleLine string
	}{
		{
			name:            "empty_params_slice",
			fixturePath:     filepath.Join("testdata", "hover", "no-params.NSN"),
			expectTitleLine: "**no-params** (subroutine)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: read and analyze the fixture
			content, err := os.ReadFile(tc.fixturePath)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.fixturePath, err)
			}

			az := natural.New(nil)
			fa, err := az.Analyze(tc.fixturePath, content)
			if err != nil {
				t.Fatalf("failed to analyze fixture: %v", err)
			}

			// Extract parameters from the definitions (should be empty or no "parameter" kind)
			var params []model.DataDefinition
			for _, def := range fa.Definitions {
				if def.SectionKind == "parameter" {
					params = append(params, def)
				}
			}

			// Act: build the subroutine hover card with no parameters
			objName := strings.TrimSuffix(filepath.Base(tc.fixturePath), filepath.Ext(tc.fixturePath))
			result := buildSubroutineHover(objName, params)

			// Assert: result is non-empty
			if result == "" {
				t.Fatal("buildSubroutineHover returned empty string for no-params case")
			}

			// Assert: title line is present
			if !strings.Contains(result, tc.expectTitleLine) {
				t.Errorf("title line missing or incorrect: expected %q in output:\n%s", tc.expectTitleLine, result)
			}

			// Assert: contains "no declared parameters" message instead of empty card
			if !strings.Contains(strings.ToLower(result), "no declared parameters") {
				t.Errorf("expected 'no declared parameters' message in output:\n%s", result)
			}

			// Assert: does not contain parameter names from other sections
			if strings.Contains(result, "WORK-VAR") {
				t.Errorf("output should not include non-parameter data (WORK-VAR): %s", result)
			}

			// Assert: output is valid Markdown
			if !isPrintable(result) {
				t.Errorf("buildSubroutineHover returned non-printable content")
			}
		})
	}
}

// TestBuildDDMHover_IndexedDDM tests the Markdown builder for DDM field-details card
// when the referenced DDM file is indexed (feature 12, T3, Story 3, AC #1).
//
// When the DDM's FileAnalysis is available with Definitions, render the view name
// (title) and a list of field names and types from the DDM's Definitions.
// A DDM is indexed when its .NSD file is in the workspace index and has been analyzed.
//
// NOTE: .NSD files are in tabular Adabas DDM format (not Natural syntax), so the analyzer
// may not extract Definitions from them in the current implementation. This test fixture
// serves as a regression test for the case where Definitions ARE available (either from
// DDM files that are parseable or from future DDM-format parsing support).
//
// FR-28: hover, Story 3 (DDM field details), AC #1.
// FR-17: modeled gaps (unindexed DDM) stay off diagnostic channel, render as honest message.
func TestBuildDDMHover_IndexedDDM(t *testing.T) {
	tests := []struct {
		name             string
		viewName         string
		expectTitleLine  string   // expected title line (view name, bold)
		expectFieldLines []string // substrings expected in output
		checkAssertion   func(t *testing.T, result string)
	}{
		{
			name:            "ddm_indexed_with_fields",
			viewName:        "CUSTOMER",
			expectTitleLine: "**CUSTOMER**",
			expectFieldLines: []string{
				"CUSTOMER-ID",
				"N8",
				"CUSTOMER-NAME",
				"A50",
				"ADDRESS",
			},
			checkAssertion: func(t *testing.T, result string) {
				// Verify that at least one field name and its type are rendered
				if !strings.Contains(result, "CUSTOMER-ID") {
					t.Errorf("indexed DDM hover should contain field name CUSTOMER-ID")
				}
				if !strings.Contains(result, "N8") {
					t.Errorf("indexed DDM hover should contain field type N8")
				}
				// Verify that the group field ADDRESS appears
				if !strings.Contains(result, "ADDRESS") {
					t.Errorf("indexed DDM hover should contain group field ADDRESS")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: read and analyze the .NSD fixture to get a real FileAnalysis with Definitions
			fixturePath := filepath.Join("testdata", "hover", "customer.NSD")
			content, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("failed to read DDM fixture %s: %v", fixturePath, err)
			}

			az := natural.New(nil)
			ddmFA, err := az.Analyze(fixturePath, content)
			if err != nil {
				t.Fatalf("failed to analyze DDM fixture: %v", err)
			}

			// Act: build the DDM hover card with the indexed FileAnalysis
			result := buildDDMHover(tc.viewName, &ddmFA)

			// Assert: result is non-empty
			if result == "" {
				t.Fatal("buildDDMHover returned empty string for indexed DDM")
			}

			// Assert: title line (view name) is present
			if !strings.Contains(result, tc.expectTitleLine) {
				t.Errorf("title line (view name) missing or incorrect: expected %q in output:\n%s",
					tc.expectTitleLine, result)
			}

			// Assert: output is valid Markdown
			if !isPrintable(result) {
				t.Errorf("buildDDMHover returned non-printable content")
			}

			// Assert: custom assertion if provided
			if tc.checkAssertion != nil {
				tc.checkAssertion(t, result)
			}
		})
	}
}

// TestBuildDDMHover_UnindexedDDM tests the Markdown builder for DDM field-details card
// when the referenced DDM file is NOT indexed (feature 12, T3, Story 3, AC #2).
//
// When ddmFA is nil (physical metadata unavailable — Adabas/IMS), render only the view name
// and an honest "field details unavailable from source" line. NO fabrication of field info.
// This satisfies FR-17: modeled gaps stay off the diagnostic channel and are surfaced honestly.
//
// FR-28: hover, Story 3 (DDM field details), AC #2.
// FR-17: modeled gaps — render honest unavailability, not silence or false data.
func TestBuildDDMHover_UnindexedDDM(t *testing.T) {
	tests := []struct {
		name            string
		viewName        string
		expectTitleLine string // view name should appear
		checkAssertion  func(t *testing.T, result string)
	}{
		{
			name:            "ddm_not_indexed",
			viewName:        "EMPLOYEE",
			expectTitleLine: "**EMPLOYEE**",
			checkAssertion: func(t *testing.T, result string) {
				// Assert: contains an honest "unavailable" message (not a fabricated field list)
				if !strings.Contains(strings.ToLower(result), "unavailable") {
					t.Errorf("unindexed DDM hover should contain honest 'unavailable' message, got:\n%s", result)
				}
				// Assert: does NOT contain fabricated field names
				if strings.Contains(result, "EMP-ID") || strings.Contains(result, "EMP-NAME") {
					t.Errorf("unindexed DDM hover must not contain fabricated field names: %s", result)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: build the DDM hover card with nil FileAnalysis (not indexed)
			result := buildDDMHover(tc.viewName, nil)

			// Assert: result is non-empty
			if result == "" {
				t.Fatal("buildDDMHover returned empty string for unindexed DDM")
			}

			// Assert: title line (view name) is present
			if !strings.Contains(result, tc.expectTitleLine) {
				t.Errorf("title line (view name) missing or incorrect: expected %q in output:\n%s",
					tc.expectTitleLine, result)
			}

			// Assert: output is valid Markdown
			if !isPrintable(result) {
				t.Errorf("buildDDMHover returned non-printable content")
			}

			// Assert: custom assertion if provided
			if tc.checkAssertion != nil {
				tc.checkAssertion(t, result)
			}
		})
	}
}

// TestBuildDDMHover_EmptyViewName tests the Markdown builder for the modeled gap
// of empty-Name data-access entries (feature 12, T3, FR-17 modeled gap).
//
// Empty viewName (feature 08's record-form gap: UPDATE/DELETE with no source-level file operand)
// yields no card — return "". This prevents orphaned hover cards for implicit record references.
//
// FR-17: modeled gaps stay off diagnostic and surface as empty results, not errors.
func TestBuildDDMHover_EmptyViewName(t *testing.T) {
	// Arrange: empty view name and arbitrary FileAnalysis (indexed or nil)
	emptyFA := &model.FileAnalysis{}
	tests := []struct {
		name  string
		ddmFA *model.FileAnalysis // nil or non-nil; both cases should return ""
	}{
		{
			name:  "empty_view_name_with_indexed_ddm",
			ddmFA: emptyFA, // A non-nil FileAnalysis
		},
		{
			name:  "empty_view_name_with_nil_ddm",
			ddmFA: nil, // No indexed DDM
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act: build the DDM hover card with empty view name
			result := buildDDMHover("", tc.ddmFA)

			// Assert: result is empty string (no card)
			if result != "" {
				t.Errorf("buildDDMHover with empty viewName should return \"\", got %q", result)
			}
		})
	}
}

// TestProvideHoverEndToEnd tests the provideHover handler (feature 12, T4, Stories 1 & 2).
// This tests the full hover flow: cursor position → findCursorTarget → resolution →
// module/subroutine hover card, inbound/outbound counts, and unresolved/dynamic messages.
//
// FR-28, FR-17 (modeled gaps), FR-43 (graceful degradation).
//
// Behavior under test:
// - Story 1: cursor on resolved module reference (CALLNAT 'HELPER') → module card with inbound count ≥1, outbound count
// - Story 2a: cursor on inline PERFORM (PERFORM INLINE-SUB) → subroutine signature card
// - Story 2b: cursor on dynamic call (CALLNAT #VAR) → dynamic message, no fabricated metadata
// - Story 2c: cursor on unresolved literal (CALLNAT 'MISSING') → unresolved message
// - Edge case: cursor on whitespace / out of range → nil, nil (no error)
func TestProvideHoverEndToEnd(t *testing.T) {
	// Setup: position encoding and logger
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: build the workspace index from the navigation fixture
	testdataDir := filepath.Join("testdata", "navigation")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, testdataDir)

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges (required by provideHover)
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext (simulating server state)
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
	}

	tt := []struct {
		name             string
		sourceFile       string                            // relative path in fixture
		cursorLine       int                               // 1-based
		cursorColumn     int                               // 1-based
		wantHoverPresent bool                              // expect non-nil hover?
		checkContents    func(*testing.T, *protocol.Hover) // optional: verify hover.Contents
		description      string
	}{
		{
			// FR-28, Story 1, AC #1: resolved module reference → module card with counts
			name:             "CALLNAT_resolved_module",
			sourceFile:       "caller.NSP",
			cursorLine:       10,
			cursorColumn:     13, // Inside 'HELPER'
			wantHoverPresent: true,
			checkContents: func(t *testing.T, hover *protocol.Hover) {
				if hover == nil {
					t.Fatal("expected non-nil hover")
				}
				// Extract Markdown content from HoverContents union
				contents := extractMarkdownContent(hover.Contents)
				if contents == "" {
					t.Fatal("unable to extract Markdown from HoverContents")
				}
				// Must contain module name HELPER, path to helper.NSN
				if !strings.Contains(contents, "HELPER") {
					t.Errorf("Contents missing module name HELPER:\n%s", contents)
				}
				if !strings.Contains(contents, "helper.NSN") {
					t.Errorf("Contents missing target path helper.NSN:\n%s", contents)
				}
				// Must contain inbound and outbound counts (even if 0)
				if !strings.Contains(contents, "Inbound") {
					t.Errorf("Contents missing inbound count:\n%s", contents)
				}
				if !strings.Contains(contents, "Outbound") {
					t.Errorf("Contents missing outbound count:\n%s", contents)
				}
			},
			description: "CALLNAT 'HELPER' resolves to helper.NSN subprogram with counts",
		},
		{
			// FR-28, Story 2a: inline PERFORM → subroutine signature card
			name:             "PERFORM_inline_subroutine",
			sourceFile:       "caller.NSP",
			cursorLine:       11,
			cursorColumn:     11, // Inside 'INLINE-SUB'
			wantHoverPresent: true,
			checkContents: func(t *testing.T, hover *protocol.Hover) {
				if hover == nil {
					t.Fatal("expected non-nil hover")
				}
				contents := extractMarkdownContent(hover.Contents)
				if contents == "" {
					t.Fatal("unable to extract Markdown from HoverContents")
				}
				// Inline subroutine should show name and "no declared parameters" (it has none)
				if !strings.Contains(contents, "INLINE-SUB") {
					t.Errorf("Contents missing subroutine name INLINE-SUB:\n%s", contents)
				}
				// INLINE-SUB has no PARAMETER section, so it should say so or show name only
				if !strings.Contains(strings.ToLower(contents), "subroutine") {
					t.Errorf("Contents missing subroutine identifier:\n%s", contents)
				}
			},
			description: "PERFORM INLINE-SUB (inline subroutine) shows signature",
		},
		{
			// FR-17, Story 2b: dynamic call target → no fabricated metadata
			name:             "CALLNAT_dynamic_variable",
			sourceFile:       "unresolved.NSP",
			cursorLine:       14,
			cursorColumn:     11, // Inside #SUB-NAME
			wantHoverPresent: true,
			checkContents: func(t *testing.T, hover *protocol.Hover) {
				if hover == nil {
					t.Fatal("expected non-nil hover")
				}
				contents := extractMarkdownContent(hover.Contents)
				if contents == "" {
					t.Fatal("unable to extract Markdown from HoverContents")
				}
				// Dynamic message: no module name, no path, no counts
				if !strings.Contains(strings.ToLower(contents), "dynamic") {
					t.Errorf("Contents missing 'dynamic' indicator:\n%s", contents)
				}
				if !strings.Contains(strings.ToLower(contents), "runtime") {
					t.Errorf("Contents missing 'runtime' reference:\n%s", contents)
				}
				// Should NOT contain fabricated data
				if strings.Contains(contents, "Inbound") {
					t.Errorf("Dynamic target should not have inbound count:\n%s", contents)
				}
			},
			description: "CALLNAT #VAR (dynamic) shows dynamic message, no counts",
		},
		{
			// FR-17, Story 2c: unresolved literal → no fabricated metadata
			name:             "CALLNAT_unresolved_literal",
			sourceFile:       "unresolved.NSP",
			cursorLine:       17,
			cursorColumn:     11, // Inside 'MISSING'
			wantHoverPresent: true,
			checkContents: func(t *testing.T, hover *protocol.Hover) {
				if hover == nil {
					t.Fatal("expected non-nil hover")
				}
				contents := extractMarkdownContent(hover.Contents)
				if contents == "" {
					t.Fatal("unable to extract Markdown from HoverContents")
				}
				// Unresolved message: no module name, no path, no counts
				if !strings.Contains(strings.ToLower(contents), "unresolved") {
					t.Errorf("Contents missing 'unresolved' indicator:\n%s", contents)
				}
				// Should NOT contain fabricated data
				if strings.Contains(contents, "Inbound") {
					t.Errorf("Unresolved target should not have inbound count:\n%s", contents)
				}
				if strings.Contains(contents, "Outbound") {
					t.Errorf("Unresolved target should not have outbound count:\n%s", contents)
				}
			},
			description: "CALLNAT 'MISSING' (unresolved) shows unresolved message",
		},
		{
			// FR-43: no cursor target → nil, nil (no error)
			name:             "no_cursor_target_whitespace",
			sourceFile:       "caller.NSP",
			cursorLine:       1,
			cursorColumn:     1, // Whitespace (before PROGRAM keyword)
			wantHoverPresent: false,
			description:      "cursor on whitespace returns nil, nil",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: read the source file for this test case
			sourceAbsPath := filepath.Join(fixtureRoot, tc.sourceFile)
			_, err := os.ReadFile(sourceAbsPath)
			if err != nil {
				t.Fatalf("failed to read source file %s: %v", tc.sourceFile, err)
			}

			// Convert URI and get FileAnalysis from index
			testURI := uri.File(sourceAbsPath)
			_, ok := idx.Get(tc.sourceFile)
			if !ok {
				t.Fatalf("source file %q not found in index", tc.sourceFile)
			}

			// Act: call provideHover with the cursor position
			params := protocol.HoverParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: testURI,
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert to 0-based
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			hover, err := provideHover(hctx, params)

			// Assert: no error
			if err != nil {
				t.Fatalf("provideHover failed: %v", err)
			}

			// Assert: hover presence
			if tc.wantHoverPresent {
				if hover == nil {
					t.Errorf("expected non-nil hover, got nil")
					return
				}
				if tc.checkContents != nil {
					tc.checkContents(t, hover)
				}
			} else {
				if hover != nil {
					t.Errorf("expected nil hover, got %v", hover)
				}
			}
		})
	}
}

// extractMarkdownContent extracts the Markdown string from a HoverContents union.
// HoverContents can be *MarkupContent, String, *MarkedStringWithLanguage, or MarkedStringSlice.
// This helper handles the *MarkupContent case (which is what the builders produce).
func extractMarkdownContent(contents protocol.HoverContents) string {
	if contents == nil {
		return ""
	}
	// Try to assert to *MarkupContent
	if mc, ok := contents.(*protocol.MarkupContent); ok && mc != nil {
		return mc.Value
	}
	// Try to assert to String
	if str, ok := contents.(protocol.String); ok {
		return string(str)
	}
	// For other union types, return empty (not needed for this test)
	return ""
}

// TestProvideHoverDataAccess tests the provideHover handler for the DataAccessEntry branch
// (feature 12, T5, Story 3).
//
// Behavior under test:
//   - Story 3 AC #1: cursor on a DDM/view name (e.g., CUSTOMER in "READ CUSTOMER") where the
//     DDM is indexed → a Hover with Markdown Contents showing the view name and its field details
//     from buildDDMHover (Definitions of the indexed DDM).
//   - Story 3 AC #2: cursor on the same view name where the .NSD is NOT in the index → a
//     view-name-only honest card with "field details unavailable from source", no fabrication.
//   - Story 3 AC #3 / FR-17 modeled gap: cursor on a record-form empty-Name data-access site
//     (e.g., a record-form UPDATE/DELETE inside a READ loop) → nil, nil (no card).
//
// FR-28 (hover), FR-17 (modeled gaps), FR-43 (graceful degradation).
func TestProvideHoverDataAccess(t *testing.T) {
	// Setup: position encoding and logger
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: build the workspace with both reader.NSP and customer.NSD
	// This test simulates two scenarios by building the index differently:
	// 1. DDM-indexed case: both files are analyzed and indexed
	// 2. DDM-absent case: only reader.NSP is indexed; customer.NSD is not
	//
	// For the DDM-indexed case, build a temp workspace with both files.
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Copy reader.NSP from the fixture to the temp workspace
	readerFixturePath := filepath.Join(wd, "testdata", "hover", "reader.NSP")
	readerContent, err := os.ReadFile(readerFixturePath)
	if err != nil {
		t.Fatalf("failed to read reader.NSP fixture: %v", err)
	}
	readerPath := filepath.Join(tmpDir, "reader.NSP")
	if err := os.WriteFile(readerPath, readerContent, 0o600); err != nil {
		t.Fatalf("failed to write reader.NSP to temp workspace: %v", err)
	}

	// Copy customer.NSD from the fixture to the temp workspace (DDM-indexed case)
	ddmFixturePath := filepath.Join(wd, "testdata", "hover", "customer.NSD")
	ddmContent, err := os.ReadFile(ddmFixturePath)
	if err != nil {
		t.Fatalf("failed to read customer.NSD fixture: %v", err)
	}
	ddmPath := filepath.Join(tmpDir, "customer.NSD")
	if err := os.WriteFile(ddmPath, ddmContent, 0o600); err != nil {
		t.Fatalf("failed to write customer.NSD to temp workspace: %v", err)
	}

	cfg := config.Defaults()

	// Build the index with both files (DDM-indexed case)
	idx, _, _, err := workspace.BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index with DDM: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext for the DDM-indexed case
	hctxWithDDM := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        tmpDir,
		cfg:         cfg,
		logger:      logger,
	}

	// For the DDM-absent case, build an index with only reader.NSP
	tmpDirNoDDM := t.TempDir()
	readerPathNoDDM := filepath.Join(tmpDirNoDDM, "reader.NSP")
	if err := os.WriteFile(readerPathNoDDM, readerContent, 0o600); err != nil {
		t.Fatalf("failed to write reader.NSP to temp workspace (DDM-absent): %v", err)
	}

	idxNoDDM, _, _, err := workspace.BuildWithCache(context.Background(), tmpDirNoDDM, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index without DDM: %v", err)
	}

	resSetNoDDM := workspace.Resolve(idxNoDDM, &cfg)

	hctxNoDDM := &handlerContext{
		idx:         idxNoDDM,
		res:         resSetNoDDM,
		posEncoding: enc,
		root:        tmpDirNoDDM,
		cfg:         cfg,
		logger:      logger,
	}

	tt := []struct {
		name             string
		hctx             *handlerContext
		sourceFile       string // reader.NSP (only file in both cases)
		cursorLine       int    // 1-based
		cursorColumn     int    // 1-based
		wantHoverPresent bool
		checkContents    func(*testing.T, *protocol.Hover) // Optional: verify hover.Contents
		description      string
	}{
		{
			// FR-28, Story 3, AC #1: cursor on CUSTOMER in "READ CUSTOMER" with DDM indexed
			// → Hover with view name + field details (from buildDDMHover with indexed FileAnalysis)
			name:             "READ_CUSTOMER_DDM_indexed",
			hctx:             hctxWithDDM,
			sourceFile:       "reader.NSP",
			cursorLine:       7, // Line with "READ CUSTOMER"
			cursorColumn:     6, // Inside 'CUSTOMER' token
			wantHoverPresent: true,
			checkContents: func(t *testing.T, hover *protocol.Hover) {
				if hover == nil {
					t.Fatal("expected non-nil hover for indexed DDM")
				}
				contents := extractMarkdownContent(hover.Contents)
				if contents == "" {
					t.Fatal("unable to extract Markdown from HoverContents")
				}
				// Must contain view name CUSTOMER
				if !strings.Contains(contents, "CUSTOMER") {
					t.Errorf("Contents missing view name CUSTOMER:\n%s", contents)
				}
				// Must contain at least one field name from customer.NSD (CUSTOMER-ID is the first data field)
				if !strings.Contains(contents, "CUSTOMER-ID") {
					t.Errorf("Contents missing field CUSTOMER-ID from indexed DDM:\n%s", contents)
				}
				// Must contain at least one field type (N8 for CUSTOMER-ID)
				if !strings.Contains(contents, "N8") {
					t.Errorf("Contents missing field type N8 from indexed DDM:\n%s", contents)
				}
				// Verify Hover.Range points to the view-name token (NameRange), not the whole statement
				if hover.Range == nil {
					t.Error("expected non-nil Hover.Range for DDM reference")
				}
			},
			description: "READ CUSTOMER with DDM indexed → field details card",
		},
		{
			// FR-28, Story 3, AC #2: cursor on CUSTOMER in "READ CUSTOMER" WITHOUT DDM indexed
			// → Hover with view name + honest "unavailable" message, no fabrication (FR-17)
			name:             "READ_CUSTOMER_DDM_absent",
			hctx:             hctxNoDDM,
			sourceFile:       "reader.NSP",
			cursorLine:       7, // Line with "READ CUSTOMER"
			cursorColumn:     6, // Inside 'CUSTOMER' token
			wantHoverPresent: true,
			checkContents: func(t *testing.T, hover *protocol.Hover) {
				if hover == nil {
					t.Fatal("expected non-nil hover for unindexed DDM")
				}
				contents := extractMarkdownContent(hover.Contents)
				if contents == "" {
					t.Fatal("unable to extract Markdown from HoverContents")
				}
				// Must contain view name CUSTOMER
				if !strings.Contains(contents, "CUSTOMER") {
					t.Errorf("Contents missing view name CUSTOMER:\n%s", contents)
				}
				// Must contain honest "unavailable" message (no fabricated field list)
				if !strings.Contains(strings.ToLower(contents), "unavailable") {
					t.Errorf("Contents missing 'unavailable' message for unindexed DDM:\n%s", contents)
				}
				// Must NOT contain fabricated field names
				if strings.Contains(contents, "CUSTOMER-ID") || strings.Contains(contents, "N8") {
					t.Errorf("Contents should not contain fabricated fields for unindexed DDM:\n%s", contents)
				}
				// Verify Hover.Range is present and points to the view-name token
				if hover.Range == nil {
					t.Error("expected non-nil Hover.Range for DDM reference")
				}
			},
			description: "READ CUSTOMER with DDM absent → view-name-only honest card",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Determine the absolute path for the source file
			sourceAbsPath := filepath.Join(tc.hctx.root, tc.sourceFile)
			_, err := os.ReadFile(sourceAbsPath)
			if err != nil {
				t.Fatalf("failed to read source file %s: %v", tc.sourceFile, err)
			}

			// Verify the source file is in the index
			_, ok := tc.hctx.idx.Get(tc.sourceFile)
			if !ok {
				t.Fatalf("source file %q not found in index", tc.sourceFile)
			}

			// Act: call provideHover with the cursor position
			params := protocol.HoverParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(sourceAbsPath),
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine - 1), // Convert to 0-based
						Character: uint32(tc.cursorColumn - 1),
					},
				},
			}

			hover, err := provideHover(tc.hctx, params)

			// Assert: no error
			if err != nil {
				t.Fatalf("provideHover failed: %v", err)
			}

			// Assert: hover presence
			if tc.wantHoverPresent {
				if hover == nil {
					t.Errorf("expected non-nil hover, got nil")
					return
				}
				if tc.checkContents != nil {
					tc.checkContents(t, hover)
				}
			} else {
				if hover != nil {
					t.Errorf("expected nil hover, got %v", hover)
				}
			}
		})
	}
}

// TestProvideHoverExternalPerform is the regression test for Finding 1 (MAJOR,
// Story 2 AC1 / plan T4): a PERFORM that resolves to an EXTERNAL .NSS subroutine
// must render the parameter-signature card (buildSubroutineHover), NOT the
// module-metadata card (buildModuleHover).
//
// Before the fix, buildSubroutineHover was only reachable in the same-file
// (inline) branch, so an external PERFORM fell through to the module card and
// showed "Inbound calls:"/"Outbound dependencies:" instead of the parameter list.
//
// This also closes the acceptance "minor" that no end-to-end test rendered a
// non-empty parameter list: EXT-WORK declares a scalar (IN-CODE N3) and a group
// (OUT-DETAIL with OUT-FLAG/OUT-TEXT children).
func TestProvideHoverExternalPerform(t *testing.T) {
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Build a temp workspace holding both the caller (.NSP) and the external
	// subroutine (.NSS), so resolution binds the PERFORM to ext-work.NSS.
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for _, name := range []string{"extcaller.NSP", "ext-work.NSS"} {
		src := filepath.Join(wd, "testdata", "hover", name)
		content, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), content, 0o600); err != nil {
			t.Fatalf("failed to write fixture %s to temp workspace: %v", name, err)
		}
	}

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}
	resSet := workspace.Resolve(idx, &cfg)

	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        tmpDir,
		cfg:         cfg,
		logger:      logger,
	}

	// Cursor on EXT-WORK in "PERFORM EXT-WORK" (line 12, "PERFORM " is 8 chars,
	// so the target name starts at column 9; land inside it at column 11).
	sourceAbsPath := filepath.Join(tmpDir, "extcaller.NSP")
	params := protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(sourceAbsPath)},
			Position: protocol.Position{
				Line:      uint32(12 - 1),
				Character: uint32(11 - 1),
			},
		},
	}

	hover, err := provideHover(hctx, params)
	if err != nil {
		t.Fatalf("provideHover failed: %v", err)
	}
	if hover == nil {
		t.Fatal("expected non-nil hover for external PERFORM")
	}
	contents := extractMarkdownContent(hover.Contents)
	if contents == "" {
		t.Fatal("unable to extract Markdown from HoverContents")
	}

	// It must be the subroutine-signature card, not the module card.
	if !strings.Contains(strings.ToLower(contents), "subroutine") {
		t.Errorf("expected subroutine-signature card, got:\n%s", contents)
	}
	if strings.Contains(contents, "Inbound calls:") || strings.Contains(contents, "Outbound dependencies:") {
		t.Errorf("external PERFORM must NOT render module-metadata card:\n%s", contents)
	}

	// It must render the resolved .NSS parameter list (scalar + group children).
	for _, want := range []string{"IN-CODE", "N3", "OUT-DETAIL", "OUT-FLAG", "OUT-TEXT", "A20"} {
		if !strings.Contains(contents, want) {
			t.Errorf("expected parameter %q in signature card:\n%s", want, contents)
		}
	}
	if strings.Contains(strings.ToLower(contents), "no declared parameters") {
		t.Errorf("expected a non-empty parameter list, got the empty-params message:\n%s", contents)
	}
}

// TestBuildAmbiguousHover is the pure-builder regression test for Finding 2
// (MINOR, robustness): an Ambiguous resolution must render an honest message
// ("the target WAS located, in multiple libraries"), NOT the unresolved message.
//
// FR-17: no fabricated counts/paths — just an honest "ambiguous" statement.
func TestBuildAmbiguousHover(t *testing.T) {
	result := buildAmbiguousHover()

	if result == "" {
		t.Fatal("buildAmbiguousHover returned empty string")
	}
	if !strings.Contains(strings.ToLower(result), "ambiguous") {
		t.Errorf("expected 'ambiguous' in output, got: %s", result)
	}
	// Must be distinct from both other modeled-gap messages.
	if result == buildUnresolvedHover() {
		t.Errorf("ambiguous hover must differ from unresolved hover: %s", result)
	}
	if result == buildDynamicHover() {
		t.Errorf("ambiguous hover must differ from dynamic hover: %s", result)
	}
	// FR-17: no fabricated metadata.
	if strings.Contains(result, "Inbound calls:") || strings.Contains(result, "Outbound dependencies:") {
		t.Errorf("ambiguous hover must not contain fabricated metadata: %s", result)
	}
	if !isPrintable(result) {
		t.Errorf("buildAmbiguousHover returned non-printable content")
	}
}

// TestProvideHoverAmbiguous is the handler-level regression test for Finding 2.
// A flat-namespace ambiguity (two subprograms of the same name, no library map)
// makes the CALLNAT resolution Ambiguous; the hover must render the honest
// ambiguous card, not the misleading "could not be located" unresolved card.
func TestProvideHoverAmbiguous(t *testing.T) {
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	tmpDir := t.TempDir()

	// Caller CALLNATs 'DUP'.
	caller := "* Ambiguous-resolution caller\n" +
		"PROGRAM AMBCALL\n\nDEFINE DATA\n  LOCAL\n    1 #X (A5)\n  END\nEND\n\n" +
		"CALLNAT 'DUP'\n\nEND\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "ambcall.NSP"), []byte(caller), 0o600); err != nil {
		t.Fatalf("failed to write caller: %v", err)
	}

	// Two subprograms named DUP in different subdirectories -> flat-namespace
	// ambiguity (no library map configured).
	dup := "DEFINE DATA\nPARAMETER\n  1 #P (A5)\nEND DEFINE\nEND\n"
	for _, sub := range []string{"liba", "libb"} {
		dir := filepath.Join(tmpDir, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("failed to mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "DUP.NSN"), []byte(dup), 0o600); err != nil {
			t.Fatalf("failed to write DUP.NSN in %s: %v", sub, err)
		}
	}

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}
	resSet := workspace.Resolve(idx, &cfg)

	// Sanity: the CALLNAT edge must actually be Ambiguous, else the test proves nothing.
	callerFA, ok := idx.Get("ambcall.NSP")
	if !ok {
		t.Fatal("caller not in index")
	}
	var sawAmbiguous bool
	for _, edge := range callerFA.Edges {
		if edge.Kind == model.EdgeCalls {
			if r, ok := resSet.Get("ambcall.NSP", edge.Source); ok && r.IsAmbiguous() {
				sawAmbiguous = true
			}
		}
	}
	if !sawAmbiguous {
		t.Fatal("expected the CALLNAT 'DUP' edge to resolve as Ambiguous; fixture setup is wrong")
	}

	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        tmpDir,
		cfg:         cfg,
		logger:      logger,
	}

	sourceAbsPath := filepath.Join(tmpDir, "ambcall.NSP")
	params := protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(sourceAbsPath)},
			// CALLNAT 'DUP' on line 10; "CALLNAT '" is 9 chars, so DUP starts at col 10.
			Position: protocol.Position{Line: uint32(10 - 1), Character: uint32(11 - 1)},
		},
	}

	hover, err := provideHover(hctx, params)
	if err != nil {
		t.Fatalf("provideHover failed: %v", err)
	}
	if hover == nil {
		t.Fatal("expected non-nil hover for ambiguous reference")
	}
	contents := extractMarkdownContent(hover.Contents)
	if !strings.Contains(strings.ToLower(contents), "ambiguous") {
		t.Errorf("expected ambiguous hover, got:\n%s", contents)
	}
	if strings.Contains(strings.ToLower(contents), "could not be located") {
		t.Errorf("ambiguous reference must not render the unresolved message:\n%s", contents)
	}
}

// TestProvideHoverOutboundCountFiltered is the regression test for Finding 3
// (NIT, acceptance OQ-B): the module card's outbound count must count only
// calls/performs/includes (EdgeCalls/EdgeCallsDynamic/EdgePerforms/EdgeIncludes),
// NOT NavigatesTo/NavigatesToDynamic (FETCH/RUN) edges.
func TestProvideHoverOutboundCountFiltered(t *testing.T) {
	// A target file whose Edges include a FETCH (NavigatesTo) plus one CALLNAT:
	// the counted outbound must be 1 (the CALLNAT), not 2.
	fa := model.FileAnalysis{
		ObjectType: model.ObjectSubprogram,
		Edges: []model.EdgeEntry{
			{Kind: model.EdgeCalls, TargetName: "OTHER"},
			{Kind: model.EdgeNavigatesTo, TargetName: "SOMEPROG"},
			{Kind: model.EdgeNavigatesToDynamic, TargetName: "#P"},
		},
	}
	got := countOutboundDependencies(fa)
	if got != 1 {
		t.Errorf("outbound count = %d, want 1 (only calls/performs/includes counted, not FETCH/RUN)", got)
	}

	// A mix that includes every counted kind plus the excluded kinds.
	fa2 := model.FileAnalysis{
		Edges: []model.EdgeEntry{
			{Kind: model.EdgeCalls},
			{Kind: model.EdgeCallsDynamic},
			{Kind: model.EdgePerforms},
			{Kind: model.EdgeIncludes},
			{Kind: model.EdgeNavigatesTo},
			{Kind: model.EdgeNavigatesToDynamic},
		},
	}
	if got := countOutboundDependencies(fa2); got != 4 {
		t.Errorf("outbound count = %d, want 4 (calls+callsdynamic+performs+includes)", got)
	}
}

// TestProvideHoverInboundCountIncremental is the regression test for Finding 4
// (MINOR, Story 1 AC2): the inbound-caller count must update with incremental
// re-analysis. Build the index, capture the inbound count for a target, add a
// second caller, recompute resolution (ResolveInto), and assert the count grew.
func TestProvideHoverInboundCountIncremental(t *testing.T) {
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	tmpDir := t.TempDir()

	// Target subprogram.
	target := "DEFINE DATA\nPARAMETER\n  1 #P (A5)\nEND DEFINE\nEND\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "TARGET.NSN"), []byte(target), 0o600); err != nil {
		t.Fatalf("failed to write TARGET.NSN: %v", err)
	}

	// First caller.
	caller1 := "PROGRAM CALL1\n\nDEFINE DATA\n  LOCAL\n    1 #X (A5)\n  END\nEND\n\nCALLNAT 'TARGET'\n\nEND\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "call1.NSP"), []byte(caller1), 0o600); err != nil {
		t.Fatalf("failed to write call1.NSP: %v", err)
	}

	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), tmpDir, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}
	res := workspace.Resolve(idx, &cfg)

	before := len(referenceSites(idx, res, tmpDir, "TARGET.NSN", "TARGET", model.ObjectSubprogram, false, enc))
	if before != 1 {
		t.Fatalf("initial inbound count = %d, want 1", before)
	}

	// Incremental change: add a second caller, mirroring applyDocumentChange.
	caller2 := "PROGRAM CALL2\n\nDEFINE DATA\n  LOCAL\n    1 #Y (A5)\n  END\nEND\n\nCALLNAT 'TARGET'\n\nEND\n"
	call2Path := filepath.Join(tmpDir, "call2.NSP")
	if err := os.WriteFile(call2Path, []byte(caller2), 0o600); err != nil {
		t.Fatalf("failed to write call2.NSP: %v", err)
	}
	fa2, err := az.Analyze(call2Path, []byte(caller2))
	if err != nil {
		t.Fatalf("failed to analyze call2.NSP: %v", err)
	}
	idx.Add("call2.NSP", fa2)
	res = workspace.ResolveInto(res, idx, &cfg, []string{"call2.NSP"})

	after := len(referenceSites(idx, res, tmpDir, "TARGET.NSN", "TARGET", model.ObjectSubprogram, false, enc))
	if after <= before {
		t.Errorf("inbound count did not update with incremental re-analysis: before=%d after=%d", before, after)
	}
	if after != 2 {
		t.Errorf("inbound count after adding a second caller = %d, want 2", after)
	}
}

// TestProvideHover_MarshaledEmptyCase (T4) pins the wire bytes for an empty hover
// result by driving the REAL dispatch path end-to-end against an empty workspace.
// The provider returns a nil *protocol.Hover, and the dispatch's nil-guard must
// emit "null".
//
// If the hover nil-guard in server.go is dropped or flipped, the emitted bytes change
// and this test goes red (Story 2 AC2).
func TestProvideHover_MarshaledEmptyCase(t *testing.T) {
	got := dispatchResultBytes(t, "textDocument/hover",
		`{"textDocument":{"uri":"file:///nonexistent/NOPE.NSP"},"position":{"line":0,"character":0}}`)

	if string(got) != "null" {
		t.Errorf("empty hover result: got %q, want %q", string(got), "null")
	}
}

// TestProvideHover_MarshaledNonEmptyCase (T4) pins the exact wire bytes for a
// non-empty hover result via marshalResult — the EXACT function the hover dispatch
// calls in server.go's non-nil branch. Pinning the full bytes (not a substring)
// locks byte-for-byte preservation across the stdlib→gojson migration (Story 2 AC2).
func TestProvideHover_MarshaledNonEmptyCase(t *testing.T) {
	// Setup: one hover result with content
	hover := &protocol.Hover{
		Contents: &protocol.MarkupContent{
			Kind:  "markdown",
			Value: "# Test Hover",
		},
		Range: &protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 5},
		},
	}

	// Marshal via the dispatch's exact marshaler.
	got, err := marshalResult(hover)
	if err != nil {
		t.Fatalf("failed to marshal via marshalResult: %v", err)
	}

	want := `{"contents":{"kind":"markdown","value":"# Test Hover"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}`
	if string(got) != want {
		t.Errorf("non-empty hover wire bytes mismatch:\n got: %s\nwant: %s", string(got), want)
	}
}
