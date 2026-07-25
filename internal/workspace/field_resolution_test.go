package workspace

import (
	"testing"

	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestFindFieldInDefinitions verifies the field lookup within a definitions list.
// It tests bare names, group-qualified names, and REDEFINE subfields.
func TestFindFieldInDefinitions(t *testing.T) {
	tests := []struct {
		name           string
		variableName   string
		definitions    []model.DataDefinition
		expectedFound  bool
		expectedLine   int
		expectedColumn int
	}{
		{
			name:         "bare name at level 1",
			variableName: "CUSTID",
			definitions: []model.DataDefinition{
				{
					Name: "CUSTID",
					NameRange: model.Range{
						Start: model.Position{Line: 5, Column: 10},
						End:   model.Position{Line: 5, Column: 15},
					},
				},
			},
			expectedFound:  true,
			expectedLine:   5,
			expectedColumn: 10,
		},
		{
			name:         "group-qualified field",
			variableName: "CUST.ID",
			definitions: []model.DataDefinition{
				{
					Name:  "CUST",
					Level: 1,
					Children: []model.DataDefinition{
						{
							Name: "ID",
							NameRange: model.Range{
								Start: model.Position{Line: 6, Column: 12},
								End:   model.Position{Line: 6, Column: 13},
							},
						},
					},
				},
			},
			expectedFound:  true,
			expectedLine:   6,
			expectedColumn: 12,
		},
		{
			name:         "field not found",
			variableName: "NOTHERE",
			definitions: []model.DataDefinition{
				{
					Name: "CUSTID",
					NameRange: model.Range{
						Start: model.Position{Line: 5, Column: 10},
						End:   model.Position{Line: 5, Column: 15},
					},
				},
			},
			expectedFound: false,
		},
		{
			name:         "case-insensitive match",
			variableName: "custid",
			definitions: []model.DataDefinition{
				{
					Name: "CUSTID",
					NameRange: model.Range{
						Start: model.Position{Line: 5, Column: 10},
						End:   model.Position{Line: 5, Column: 15},
					},
				},
			},
			expectedFound:  true,
			expectedLine:   5,
			expectedColumn: 10,
		},
		{
			name:         "REDEFINE nested field",
			variableName: "REDEFINED",
			definitions: []model.DataDefinition{
				{
					Name: "ORIGINAL",
					Children: []model.DataDefinition{
						{
							Name: "REDEFINED",
							NameRange: model.Range{
								Start: model.Position{Line: 8, Column: 14},
								End:   model.Position{Line: 8, Column: 22},
							},
						},
					},
				},
			},
			expectedFound:  true,
			expectedLine:   8,
			expectedColumn: 14,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := findFieldInDefinitions(tc.variableName, tc.definitions)

			if tc.expectedFound {
				if result == (model.Range{}) {
					t.Fatalf("expected to find field %q, but got empty Range", tc.variableName)
				}
				if result.Start.Line != tc.expectedLine || result.Start.Column != tc.expectedColumn {
					t.Errorf("field %q: got position {%d,%d}, want {%d,%d}",
						tc.variableName, result.Start.Line, result.Start.Column, tc.expectedLine, tc.expectedColumn)
				}
			} else {
				if result != (model.Range{}) {
					t.Errorf("expected not to find field %q, but got Range {%d,%d}", tc.variableName, result.Start.Line, result.Start.Column)
				}
			}
		})
	}
}

// TestResolveDataAreaField verifies the full cross-file field resolution.
// This is an integration test that requires a workspace index with a data area.
func TestResolveDataAreaField_SimpleCase(t *testing.T) {
	// Build an index with a data-area file containing a field definition
	idx := &Index{}
	idx.Add("lib1/CUSTLDA.NSL", model.FileAnalysis{
		ObjectType: model.ObjectLocalDataArea,
		Definitions: []model.DataDefinition{
			{
				Name:  "CUSTID",
				Level: 1,
				Type:  "N10",
				NameRange: model.Range{
					Start: model.Position{Line: 5, Column: 10},
					End:   model.Position{Line: 5, Column: 15},
				},
				Range: model.Range{
					Start: model.Position{Line: 5, Column: 1},
					End:   model.Position{Line: 5, Column: 20},
				},
			},
		},
	})

	// Test 1: Resolve a field in a USING data area
	result := ResolveDataAreaField(
		"CUSTID",
		model.DataAreaRef{
			Name:        "CUSTLDA",
			SectionKind: "local",
		},
		idx,
		"lib1/CALLER.NSP",
		&config.Config{
			Resolution: config.ResolutionConfig{
				Libraries: []config.Library{
					{
						Name: "lib1",
						Path: "lib1",
					},
				},
			},
		},
	)

	if result == (model.Range{}) {
		t.Fatal("expected to resolve CUSTID in CUSTLDA, but got empty Range")
	}
	if result.Start.Line != 5 || result.Start.Column != 10 {
		t.Errorf("resolved field position {%d,%d}, want {5,10}", result.Start.Line, result.Start.Column)
	}

	// Test 2: Unresolvable data area (out of chain)
	result = ResolveDataAreaField(
		"CUSTID",
		model.DataAreaRef{
			Name:        "UNRESOLVABLE",
			SectionKind: "local",
		},
		idx,
		"lib1/CALLER.NSP",
		&config.Config{
			Resolution: config.ResolutionConfig{
				Libraries: []config.Library{
					{
						Name: "lib1",
						Path: "lib1",
					},
				},
			},
		},
	)

	if result != (model.Range{}) {
		t.Errorf("expected empty result for unresolvable data area, but got {%d,%d}", result.Start.Line, result.Start.Column)
	}

	// Test 3: Field not found in data area
	result = ResolveDataAreaField(
		"NOTHERE",
		model.DataAreaRef{
			Name:        "CUSTLDA",
			SectionKind: "local",
		},
		idx,
		"lib1/CALLER.NSP",
		&config.Config{
			Resolution: config.ResolutionConfig{
				Libraries: []config.Library{
					{
						Name: "lib1",
						Path: "lib1",
					},
				},
			},
		},
	)

	if result != (model.Range{}) {
		t.Errorf("expected empty result for missing field, but got {%d,%d}", result.Start.Line, result.Start.Column)
	}
}
