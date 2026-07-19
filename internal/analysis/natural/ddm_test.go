package natural

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestExtractDDMDefinitions_HeadersOnly verifies that a DDM file with only header
// rows and a terminator line (no data rows) returns an empty/nil slice without panic.
// Regression for the "headers-only" edge case.
func TestExtractDDMDefinitions_HeadersOnly(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "ddm", "headers-only.NSD"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}
	defs := extractDDMDefinitions(string(content))
	if len(defs) != 0 {
		t.Errorf("headers-only DDM: got %d definitions, want 0", len(defs))
	}
}

// TestExtractDDMDefinitions_SQLType verifies that a DDM file with "TYPE: SQL" header
// returns nil immediately (SQL DDMs are out of scope for field extraction).
func TestExtractDDMDefinitions_SQLType(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "ddm", "sql-type.NSD"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}
	defs := extractDDMDefinitions(string(content))
	if defs != nil {
		t.Errorf("SQL DDM: got %d definitions, want nil (out of scope)", len(defs))
	}
}

// TestExtractDDMDefinitions_ShortLines verifies that lines shorter than every column
// offset parse correctly:
//   - A group line "G 1 AC ADDRESS" (< 39 chars) parses to a group with empty Type
//   - A bare "TYPE:" line (5 chars, no trailing space) does NOT panic
//   - A line with a non-digit at the level offset is skipped, not mis-nested
func TestExtractDDMDefinitions_ShortLines(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "ddm", "short-lines.NSD"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}
	// Must not panic.
	defs := extractDDMDefinitions(string(content))

	// Find ADDRESS group — must parse despite line being shorter than 39 chars.
	var addr *model.DataDefinition
	for i := range defs {
		if defs[i].Name == "ADDRESS" {
			addr = &defs[i]
			break
		}
	}
	if addr == nil {
		t.Error("ADDRESS group not found in short-lines fixture")
	} else {
		if addr.Type != "" {
			t.Errorf("ADDRESS.Type = %q, want empty (short group line has no format column)", addr.Type)
		}
		if len(addr.Children) == 0 {
			t.Error("ADDRESS group has no children; expected at least STREET")
		}
	}

	// The "x" line (non-digit at level offset) must not produce a definition.
	for _, d := range defs {
		if d.Name == "X" {
			t.Error("non-digit level line produced a definition named X; want skipped")
		}
	}

	// BALANCE must still be parsed (level 1 elementary field after the group).
	var balance *model.DataDefinition
	for i := range defs {
		if defs[i].Name == "BALANCE" {
			balance = &defs[i]
			break
		}
	}
	if balance == nil {
		t.Error("BALANCE not found after short group line")
	}
}

// TestExtractDDMDefinitions_PeriodicGroup verifies that a periodic-group field
// (T=P at column offset 0) is extracted as a group (empty Type, children nested
// via Children) AND carries a single unbounded ArrayDimension — mirroring the MU
// (T=M) path, per the verified DDM spec (.claude/knowledge/natural/ddm-format.md):
// a PE field repeats but the report records no occurrence count, so it gets the
// same unbounded (*) dimension as MU. Regression for the finding that
// parseFormatAndDimensions returned early for group types (including 'P'),
// never reaching the MU-only dimension assignment.
func TestExtractDDMDefinitions_PeriodicGroup(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "ddm", "periodic.NSD"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}
	defs := extractDDMDefinitions(string(content))

	var orders *model.DataDefinition
	for i := range defs {
		if defs[i].Name == "ORDERS" {
			orders = &defs[i]
			break
		}
	}
	if orders == nil {
		t.Fatal("ORDERS periodic group not found")
	}
	if orders.Level != 1 {
		t.Errorf("ORDERS.Level = %d, want 1", orders.Level)
	}
	// A periodic group is a group: empty Type, children nested.
	if orders.Type != "" {
		t.Errorf("ORDERS.Type = %q, want empty (periodic group is a group header)", orders.Type)
	}
	if len(orders.Children) != 2 {
		t.Fatalf("ORDERS.Children len = %d, want 2", len(orders.Children))
	}
	childNames := []string{"ORDER-DATE", "ORDER-AMOUNT"}
	for i, expected := range childNames {
		if orders.Children[i].Name != expected {
			t.Errorf("ORDERS.Children[%d].Name = %q, want %q", i, orders.Children[i].Name, expected)
		}
		if orders.Children[i].Level != 2 {
			t.Errorf("ORDERS.Children[%d].Level = %d, want 2", i, orders.Children[i].Level)
		}
	}
	// A periodic group carries a single unbounded (*) dimension — same struct
	// shape the MU path uses.
	want := []model.ArrayDimension{{Lower: 1, Upper: 0, UpperUnbounded: true}}
	if len(orders.Dimensions) != 1 {
		t.Fatalf("ORDERS.Dimensions len = %d, want 1 (unbounded PE dimension)", len(orders.Dimensions))
	}
	if orders.Dimensions[0] != want[0] {
		t.Errorf("ORDERS.Dimensions[0] = %+v, want %+v", orders.Dimensions[0], want[0])
	}
}

// TestExtractDDMDefinitions_Customer verifies that analyzing a `.NSD` DDM file
// yields Definitions extracted from the fixed-column DDM report format.
// This is T2A of feature 12-hover: extract `.NSD` fields into FileAnalysis.Definitions.
// FR-28 story 3 (DDM field hover) depends on this.
func TestExtractDDMDefinitions_Customer(t *testing.T) {
	// Read the DDM fixture (byte-correct, exported DDM report format).
	content, err := os.ReadFile(filepath.Join("testdata", "ddm", "customer.NSD"))
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Analyze the DDM file via the natural analyzer.
	// The analyzer should classify it as ObjectDDM by extension.
	a := New(nil) // no custom extensions
	result, err := a.Analyze(filepath.Join("testdata", "ddm", "customer.NSD"), content)

	if err != nil {
		t.Errorf("Analyze error = %v, expected nil", err)
	}

	if result.ObjectType != model.ObjectDDM {
		t.Errorf("ObjectType = %q, want %q", result.ObjectType, model.ObjectDDM)
	}

	// Table-driven test cases for the definitions extracted from customer.NSD.
	// Acceptance criteria (from task T2A):
	//   - CUSTOMER-ID → Level 1, Type "N8"
	//   - CUSTOMER-NAME → Level 1, Type "A50"
	//   - ADDRESS → group (empty Type) with children STREET/CITY/ZIP-CODE (Level 2)
	//   - BALANCE → Type "P9,2"
	//   - PHONE → Type "A20" with unbounded * dimension (MU array)
	//   - NAME-CITY-SUPER → Type "A80"
	//   - All in source order; names upper-cased
	tests := []struct {
		name string
		run  func(t *testing.T, defs []model.DataDefinition)
	}{
		{
			name: "has_definitions",
			run: func(t *testing.T, defs []model.DataDefinition) {
				if len(defs) == 0 {
					t.Error("Definitions is empty, want non-empty")
				}
			},
		},
		{
			name: "CUSTOMER_ID_correct",
			run: func(t *testing.T, defs []model.DataDefinition) {
				var custID *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-ID" {
						custID = &defs[i]
						break
					}
				}
				if custID == nil {
					t.Error("CUSTOMER-ID not found")
					return
				}
				if custID.Level != 1 {
					t.Errorf("CUSTOMER-ID.Level = %d, want 1", custID.Level)
				}
				if custID.Type != "N8" {
					t.Errorf("CUSTOMER-ID.Type = %q, want %q", custID.Type, "N8")
				}
			},
		},
		{
			name: "CUSTOMER_NAME_correct",
			run: func(t *testing.T, defs []model.DataDefinition) {
				var custName *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "CUSTOMER-NAME" {
						custName = &defs[i]
						break
					}
				}
				if custName == nil {
					t.Error("CUSTOMER-NAME not found")
					return
				}
				if custName.Level != 1 {
					t.Errorf("CUSTOMER-NAME.Level = %d, want 1", custName.Level)
				}
				if custName.Type != "A50" {
					t.Errorf("CUSTOMER-NAME.Type = %q, want %q", custName.Type, "A50")
				}
			},
		},
		{
			name: "ADDRESS_is_group_with_children",
			run: func(t *testing.T, defs []model.DataDefinition) {
				var addr *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "ADDRESS" {
						addr = &defs[i]
						break
					}
				}
				if addr == nil {
					t.Error("ADDRESS not found")
					return
				}
				if addr.Level != 1 {
					t.Errorf("ADDRESS.Level = %d, want 1", addr.Level)
				}
				if addr.Type != "" {
					t.Errorf("ADDRESS.Type = %q, want empty (group header)", addr.Type)
				}
				if len(addr.Children) != 3 {
					t.Errorf("ADDRESS.Children len = %d, want 3", len(addr.Children))
					return
				}
				// Verify children: STREET, CITY, ZIP-CODE all Level 2
				childNames := []string{"STREET", "CITY", "ZIP-CODE"}
				childTypes := []string{"A40", "A30", "A10"}
				for i, expected := range childNames {
					if addr.Children[i].Name != expected {
						t.Errorf("ADDRESS.Children[%d].Name = %q, want %q", i, addr.Children[i].Name, expected)
					}
					if addr.Children[i].Level != 2 {
						t.Errorf("ADDRESS.Children[%d].Level = %d, want 2", i, addr.Children[i].Level)
					}
					if addr.Children[i].Type != childTypes[i] {
						t.Errorf("ADDRESS.Children[%d].Type = %q, want %q", i, addr.Children[i].Type, childTypes[i])
					}
				}
			},
		},
		{
			name: "BALANCE_packed_decimal",
			run: func(t *testing.T, defs []model.DataDefinition) {
				var balance *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "BALANCE" {
						balance = &defs[i]
						break
					}
				}
				if balance == nil {
					t.Error("BALANCE not found")
					return
				}
				if balance.Level != 1 {
					t.Errorf("BALANCE.Level = %d, want 1", balance.Level)
				}
				// Accept verbatim "P9,2" (comma as regional decimal separator)
				if balance.Type != "P9,2" {
					t.Errorf("BALANCE.Type = %q, want %q", balance.Type, "P9,2")
				}
			},
		},
		{
			name: "PHONE_multiple_value_array",
			run: func(t *testing.T, defs []model.DataDefinition) {
				var phone *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "PHONE" {
						phone = &defs[i]
						break
					}
				}
				if phone == nil {
					t.Error("PHONE not found")
					return
				}
				if phone.Level != 1 {
					t.Errorf("PHONE.Level = %d, want 1", phone.Level)
				}
				if phone.Type != "A20" {
					t.Errorf("PHONE.Type = %q, want %q", phone.Type, "A20")
				}
				// MU fields should have a single unbounded * Dimensions entry
				if len(phone.Dimensions) != 1 {
					t.Errorf("PHONE.Dimensions len = %d, want 1", len(phone.Dimensions))
					return
				}
				if !phone.Dimensions[0].UpperUnbounded {
					t.Errorf("PHONE.Dimensions[0].UpperUnbounded = false, want true")
				}
			},
		},
		{
			name: "NAME_CITY_SUPER_superdescriptor",
			run: func(t *testing.T, defs []model.DataDefinition) {
				var super *model.DataDefinition
				for i := range defs {
					if defs[i].Name == "NAME-CITY-SUPER" {
						super = &defs[i]
						break
					}
				}
				if super == nil {
					t.Error("NAME-CITY-SUPER not found")
					return
				}
				if super.Level != 1 {
					t.Errorf("NAME-CITY-SUPER.Level = %d, want 1", super.Level)
				}
				if super.Type != "A80" {
					t.Errorf("NAME-CITY-SUPER.Type = %q, want %q", super.Type, "A80")
				}
			},
		},
		{
			name: "definitions_in_source_order",
			run: func(t *testing.T, defs []model.DataDefinition) {
				// Top-level definitions should appear in the order:
				// CUSTOMER-ID, CUSTOMER-NAME, ADDRESS, BALANCE, PHONE, NAME-CITY-SUPER
				expectedTopLevel := []string{"CUSTOMER-ID", "CUSTOMER-NAME", "ADDRESS", "BALANCE", "PHONE", "NAME-CITY-SUPER"}
				// Count top-level entries (exclude children of ADDRESS)
				topLevel := []string{}
				for _, d := range defs {
					topLevel = append(topLevel, d.Name)
				}
				if len(topLevel) < len(expectedTopLevel) {
					t.Errorf("top-level definitions count = %d, want at least %d", len(topLevel), len(expectedTopLevel))
					return
				}
				for i, expected := range expectedTopLevel {
					if i >= len(topLevel) {
						t.Errorf("missing definition at position %d: %q", i, expected)
						continue
					}
					if topLevel[i] != expected {
						t.Errorf("definitions[%d].Name = %q, want %q", i, topLevel[i], expected)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, result.Definitions)
		})
	}
}
