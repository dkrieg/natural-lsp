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

// TestResolveDDMFieldLocation tests ResolveDDMFieldLocation directly, covering:
// - resolved DDM with a matching field (non-zero NameRange + DDMPath)
// - unreachable DDM (outside the steplib chain → zero Range + empty path)
// - TYPE: SQL DDM with no parsed Definitions (zero Range + empty path)
// - absent field in a resolvable DDM (zero Range + empty path)
func TestResolveDDMFieldLocation(t *testing.T) {
	tests := []struct {
		name            string
		fieldName       string
		ddmName         string
		indexSetup      func(*Index)
		referencingPath string
		cfg             *config.Config
		expectRange     bool
		expectPath      bool
		expectedPath    string
	}{
		{
			name:      "resolved DDM with matching field",
			fieldName: "EMP-ID",
			ddmName:   "TESTDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/TESTDDM.NSD", model.FileAnalysis{
					ObjectType: model.ObjectDDM,
					Definitions: []model.DataDefinition{
						{
							Name:  "EMP-ID",
							Level: 1,
							Type:  "N8",
							NameRange: model.Range{
								Start: model.Position{Line: 6, Column: 5},
								End:   model.Position{Line: 6, Column: 11},
							},
						},
						{
							Name:  "EMP-NAME",
							Level: 1,
							Type:  "A50",
							NameRange: model.Range{
								Start: model.Position{Line: 7, Column: 5},
								End:   model.Position{Line: 7, Column: 13},
							},
						},
					},
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectRange:  true,
			expectPath:   true,
			expectedPath: "lib/TESTDDM.NSD",
		},
		{
			name:      "unreachable DDM (outside steplib chain)",
			fieldName: "SOMEFIELD",
			ddmName:   "UNRESOLVED",
			indexSetup: func(idx *Index) {
				// No DDM added for UNRESOLVED — it will be unreachable
			},
			referencingPath: "lib1/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib1", Path: "lib1"},
						{Name: "lib2", Path: "lib2"},
					},
				},
			},
			expectRange:  false,
			expectPath:   false,
			expectedPath: "",
		},
		{
			name:      "TYPE: SQL DDM (no parsed Definitions)",
			fieldName: "FIELD1",
			ddmName:   "SQLDDM",
			indexSetup: func(idx *Index) {
				// TYPE: SQL DDM has an empty Definitions list
				idx.Add("lib/SQLDDM.NSD", model.FileAnalysis{
					ObjectType:  model.ObjectDDM,
					Definitions: []model.DataDefinition{}, // Empty — TYPE: SQL
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectRange:  false,
			expectPath:   false,
			expectedPath: "",
		},
		{
			name:      "absent field in resolvable DDM",
			fieldName: "NONEXISTENT",
			ddmName:   "TESTDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/TESTDDM.NSD", model.FileAnalysis{
					ObjectType: model.ObjectDDM,
					Definitions: []model.DataDefinition{
						{
							Name:  "EMP-ID",
							Level: 1,
							Type:  "N8",
							NameRange: model.Range{
								Start: model.Position{Line: 6, Column: 5},
								End:   model.Position{Line: 6, Column: 11},
							},
						},
					},
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectRange:  false,
			expectPath:   false,
			expectedPath: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idx := &Index{}
			tc.indexSetup(idx)

			rng, ddmPath := ResolveDDMFieldLocation(
				tc.fieldName,
				tc.ddmName,
				idx,
				tc.referencingPath,
				tc.cfg,
			)

			// Verify NameRange
			if tc.expectRange {
				if rng == (model.Range{}) {
					t.Errorf("expected non-zero NameRange for field %q in DDM %q, got empty Range", tc.fieldName, tc.ddmName)
				}
			} else {
				if rng != (model.Range{}) {
					t.Errorf("expected zero NameRange for field %q in DDM %q, got {%d:%d-%d:%d}",
						tc.fieldName, tc.ddmName,
						rng.Start.Line, rng.Start.Column, rng.End.Line, rng.End.Column)
				}
			}

			// Verify DDM path
			if tc.expectPath {
				if ddmPath != tc.expectedPath {
					t.Errorf("expected DDM path %q, got %q", tc.expectedPath, ddmPath)
				}
			} else {
				if ddmPath != "" {
					t.Errorf("expected empty DDM path, got %q", ddmPath)
				}
			}
		})
	}
}

// TestResolveDDMFieldType tests ResolveDDMFieldType directly, covering:
// - resolved DDM with matching field → returns field Type string
// - unreachable DDM → returns empty string
// - TYPE: SQL DDM (no Definitions) → returns empty string
// - absent field in resolvable DDM → returns empty string
func TestResolveDDMFieldType(t *testing.T) {
	tests := []struct {
		name            string
		fieldName       string
		ddmName         string
		indexSetup      func(*Index)
		referencingPath string
		cfg             *config.Config
		expectedType    string
	}{
		{
			name:      "resolved DDM, matching field → return Type string",
			fieldName: "EMP-ID",
			ddmName:   "TESTDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/TESTDDM.NSD", model.FileAnalysis{
					ObjectType: model.ObjectDDM,
					Definitions: []model.DataDefinition{
						{
							Name:  "EMP-ID",
							Level: 1,
							Type:  "N8",
							NameRange: model.Range{
								Start: model.Position{Line: 6, Column: 5},
								End:   model.Position{Line: 6, Column: 11},
							},
						},
						{
							Name:  "EMP-NAME",
							Level: 1,
							Type:  "A50",
							NameRange: model.Range{
								Start: model.Position{Line: 7, Column: 5},
								End:   model.Position{Line: 7, Column: 13},
							},
						},
					},
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectedType: "N8",
		},
		{
			name:      "resolved DDM, different field → return correct Type",
			fieldName: "EMP-NAME",
			ddmName:   "TESTDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/TESTDDM.NSD", model.FileAnalysis{
					ObjectType: model.ObjectDDM,
					Definitions: []model.DataDefinition{
						{
							Name:  "EMP-ID",
							Level: 1,
							Type:  "N8",
							NameRange: model.Range{
								Start: model.Position{Line: 6, Column: 5},
								End:   model.Position{Line: 6, Column: 11},
							},
						},
						{
							Name:  "EMP-NAME",
							Level: 1,
							Type:  "A50",
							NameRange: model.Range{
								Start: model.Position{Line: 7, Column: 5},
								End:   model.Position{Line: 7, Column: 13},
							},
						},
					},
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectedType: "A50",
		},
		{
			name:      "unreachable DDM → empty string",
			fieldName: "FIELD1",
			ddmName:   "UNRESOLVED",
			indexSetup: func(idx *Index) {
				// No DDM in index
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectedType: "",
		},
		{
			name:      "TYPE: SQL DDM (no Definitions) → empty string",
			fieldName: "SOMEFIELD",
			ddmName:   "SQLDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/SQLDDM.NSD", model.FileAnalysis{
					ObjectType:  model.ObjectDDM,
					Definitions: []model.DataDefinition{}, // Empty
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectedType: "",
		},
		{
			name:      "absent field in resolvable DDM → empty string",
			fieldName: "NONEXISTENT",
			ddmName:   "TESTDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/TESTDDM.NSD", model.FileAnalysis{
					ObjectType: model.ObjectDDM,
					Definitions: []model.DataDefinition{
						{
							Name:  "EMP-ID",
							Level: 1,
							Type:  "N8",
							NameRange: model.Range{
								Start: model.Position{Line: 6, Column: 5},
								End:   model.Position{Line: 6, Column: 11},
							},
						},
					},
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectedType: "",
		},
		{
			name:      "nested field in resolvable DDM → return correct Type",
			fieldName: "SUBFIELD",
			ddmName:   "TESTDDM",
			indexSetup: func(idx *Index) {
				idx.Add("lib/TESTDDM.NSD", model.FileAnalysis{
					ObjectType: model.ObjectDDM,
					Definitions: []model.DataDefinition{
						{
							Name:  "GROUP",
							Level: 1,
							Type:  "",
							Children: []model.DataDefinition{
								{
									Name:  "SUBFIELD",
									Level: 2,
									Type:  "A25",
									NameRange: model.Range{
										Start: model.Position{Line: 7, Column: 10},
										End:   model.Position{Line: 7, Column: 18},
									},
								},
							},
						},
					},
				})
			},
			referencingPath: "lib/CALLER.NSP",
			cfg: &config.Config{
				Resolution: config.ResolutionConfig{
					Libraries: []config.Library{
						{Name: "lib", Path: "lib"},
					},
				},
			},
			expectedType: "A25",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idx := &Index{}
			tc.indexSetup(idx)

			result := ResolveDDMFieldType(
				tc.fieldName,
				tc.ddmName,
				idx,
				tc.referencingPath,
				tc.cfg,
			)

			if result != tc.expectedType {
				t.Errorf("expected Type %q, got %q", tc.expectedType, result)
			}
		})
	}
}
