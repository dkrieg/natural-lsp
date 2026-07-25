package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// TestCursorLookup_edgeContainment verifies that a cursor position maps to the
// EdgeEntry whose Source range contains it (Feature 10, Task 5 — F5).
//
// Given a FileAnalysis with extracted edges, a cursor at a 1-based model.Position
// should return the EdgeEntry whose Source range contains the cursor, if any.
// For overlapping ranges, return the smallest containing range.
func TestCursorLookup_edgeContainment(t *testing.T) {
	// Arrange: read and analyze the fixture
	fixturePath := filepath.Join("testdata", "navigation", "cursor-lookup.NSP")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixturePath, err)
	}

	// Analyze the fixture using the parser backend
	az := natural.New(nil)
	fa, err := az.Analyze(fixturePath, content)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(fa.Edges) == 0 {
		t.Fatal("No edges extracted from fixture; expected at least one")
	}

	if len(fa.DataAccess) == 0 {
		t.Fatal("No data-access entries extracted from fixture; expected at least one")
	}

	// Helper to find the first edge and data-access entry for reference
	var callnatEdge *model.EdgeEntry
	var readEntry *model.DataAccessEntry
	for i := range fa.Edges {
		if fa.Edges[i].Kind == model.EdgeCalls && fa.Edges[i].TargetName == "SUBPROGRAM" {
			callnatEdge = &fa.Edges[i]
			break
		}
	}
	for i := range fa.DataAccess {
		if fa.DataAccess[i].Name == "TESTVIEW" && fa.DataAccess[i].Kind == model.EdgeReads {
			readEntry = &fa.DataAccess[i]
			break
		}
	}

	if callnatEdge == nil {
		t.Fatal("CALLNAT 'SUBPROGRAM' edge not found in fixture")
	}
	if readEntry == nil {
		t.Fatal("READ TESTVIEW entry not found in fixture")
	}

	// Act & Assert: table-driven test cases for cursor containment
	tests := []struct {
		name        string
		pos         model.Position
		wantEdge    *model.EdgeEntry
		wantAccess  *model.DataAccessEntry
		description string
	}{
		{
			name:        "cursor_inside_callnat_target",
			pos:         callnatEdge.Source.Start,
			wantEdge:    callnatEdge,
			wantAccess:  nil,
			description: "Cursor at the start of CALLNAT Source should find that edge",
		},
		{
			name:        "cursor_inside_read_view_name",
			pos:         readEntry.NameRange.Start,
			wantEdge:    nil,
			wantAccess:  readEntry,
			description: "Cursor at the start of READ view-name should find that data-access entry",
		},
		{
			name:        "cursor_on_whitespace",
			pos:         model.Position{Line: 20, Column: 1},
			wantEdge:    nil,
			wantAccess:  nil,
			description: "Cursor on empty line should find nothing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Call the lookup function (stub for RED phase)
			edge, access, _ := findCursorTarget(fa, tc.pos, "", nil)

			// Assert: edge match
			if tc.wantEdge == nil {
				if edge != nil {
					t.Errorf("expected no edge, got %+v", edge)
				}
			} else {
				if edge == nil {
					t.Errorf("expected edge, got nil")
				} else if edge.TargetName != tc.wantEdge.TargetName || edge.Kind != tc.wantEdge.Kind {
					t.Errorf("expected edge TargetName=%s Kind=%s, got TargetName=%s Kind=%s",
						tc.wantEdge.TargetName, tc.wantEdge.Kind, edge.TargetName, edge.Kind)
				}
			}

			// Assert: data-access match
			if tc.wantAccess == nil {
				if access != nil {
					t.Errorf("expected no data-access entry, got %+v", access)
				}
			} else {
				if access == nil {
					t.Errorf("expected data-access entry, got nil")
				} else if access.Name != tc.wantAccess.Name || access.Kind != tc.wantAccess.Kind {
					t.Errorf("expected access Name=%s Kind=%s, got Name=%s Kind=%s",
						tc.wantAccess.Name, tc.wantAccess.Kind, access.Name, access.Kind)
				}
			}
		})
	}
}
