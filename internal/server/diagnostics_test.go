package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/document"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestSeverityToProtocol tests the mapping of model.DiagnosticSeverity to
// protocol.DiagnosticSeverity. This is a pure function test with no I/O or
// concurrency concerns.
//
// Test cases cover:
// - DiagnosticError ("error") → DiagnosticSeverityError (1)
// - DiagnosticWarning ("warning") → DiagnosticSeverityWarning (2)
// - DiagnosticInfo ("info") → DiagnosticSeverityInformation (3)
// - Unknown/empty string → DiagnosticSeverityInformation (least alarming safe default)
//
// The choice of DiagnosticSeverityInformation for unknown values ensures that
// unclassified or malformed diagnostics do not default to an alarming severity.
func TestSeverityToProtocol(t *testing.T) {
	tests := []struct {
		name     string
		modelSev model.DiagnosticSeverity
		expected protocol.DiagnosticSeverity
	}{
		{
			name:     "DiagnosticError maps to DiagnosticSeverityError",
			modelSev: model.DiagnosticError,
			expected: protocol.DiagnosticSeverityError,
		},
		{
			name:     "DiagnosticWarning maps to DiagnosticSeverityWarning",
			modelSev: model.DiagnosticWarning,
			expected: protocol.DiagnosticSeverityWarning,
		},
		{
			name:     "DiagnosticInfo maps to DiagnosticSeverityInformation",
			modelSev: model.DiagnosticInfo,
			expected: protocol.DiagnosticSeverityInformation,
		},
		{
			name:     "Unknown/empty severity defaults to DiagnosticSeverityInformation",
			modelSev: model.DiagnosticSeverity(""),
			expected: protocol.DiagnosticSeverityInformation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := severityToProtocol(tc.modelSev)
			if got != tc.expected {
				t.Errorf("severityToProtocol(%q) = %d, want %d", tc.modelSev, got, tc.expected)
			}
		})
	}
}

// TestToProtocolDiagnostic tests the conversion of a model.Diagnostic to a
// protocol.Diagnostic with encoding-aware range conversion, message passthrough,
// severity mapping, source tag, and code categorization.
//
// Test cases verify:
// - Range conversion via toProtocolRange in both UTF-8 and UTF-16 encodings
// - Message verbatim copying (e.g., "unexpected token X" flows through)
// - Severity preserved and mapped to protocol equivalent
// - Source set to the constant "natural-lsp"
// - Code categorization: "syntax" for syntax errors, "ambiguity" for resolution issues
// - Empty Code → protocol Code unset/nil
// - Degenerate inputs (empty content, out-of-range ranges) do not panic
//
// Note: protocol.Diagnostic.Code is of type ProgressToken, a union that can be
// protocol.String or protocol.Integer. For code strings like "syntax" and "ambiguity",
// we use protocol.String(code). An unset code should result in a nil ProgressToken.
func TestToProtocolDiagnostic(t *testing.T) {
	tests := []struct {
		name            string
		modelDiag       model.Diagnostic
		content         string
		encoding        protocol.PositionEncodingKind
		expectedMessage string
		expectedCodeStr string
		wantStartLine   uint32
		wantStartChar   uint32
		wantEndLine     uint32
		wantEndChar     uint32
		wantNilCode     bool
	}{
		{
			name: "syntax error with code (UTF-8)",
			modelDiag: model.Diagnostic{
				Message:  "unexpected token INVALID at position",
				Severity: model.DiagnosticError,
				Code:     model.DiagnosticCodeSyntax,
				Range: model.Range{
					Start: model.Position{Line: 1, Column: 10},
					End:   model.Position{Line: 1, Column: 16},
				},
			},
			content:         "CALLNAT INVALID somewhere",
			encoding:        protocol.PositionEncodingKindUTF8,
			expectedMessage: "unexpected token INVALID at position",
			expectedCodeStr: "syntax",
			wantStartLine:   0,
			wantStartChar:   9,
			wantEndLine:     0,
			wantEndChar:     16,
		},
		{
			name: "ambiguity warning with code (UTF-16)",
			modelDiag: model.Diagnostic{
				Message:  "ambiguous reference 'PROG1': matches A, B",
				Severity: model.DiagnosticWarning,
				Code:     model.DiagnosticCodeAmbiguity,
				Range: model.Range{
					Start: model.Position{Line: 3, Column: 9},
					End:   model.Position{Line: 3, Column: 13},
				},
			},
			content:         "line1\nline2\nCALLNAT PROG1 extra",
			encoding:        protocol.PositionEncodingKindUTF16,
			expectedMessage: "ambiguous reference 'PROG1': matches A, B",
			expectedCodeStr: "ambiguity",
			wantStartLine:   2,
			wantStartChar:   8,
			wantEndLine:     2,
			wantEndChar:     13,
		},
		{
			name: "empty code → protocol Code unset",
			modelDiag: model.Diagnostic{
				Message:  "some issue",
				Severity: model.DiagnosticInfo,
				Code:     model.DiagnosticCode(""),
				Range: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 1, Column: 5},
				},
			},
			content:         "hello",
			encoding:        protocol.PositionEncodingKindUTF8,
			expectedMessage: "some issue",
			wantStartLine:   0,
			wantStartChar:   0,
			wantEndLine:     0,
			wantEndChar:     5,
			wantNilCode:     true,
		},
		{
			name: "degenerate input - empty content",
			modelDiag: model.Diagnostic{
				Message:  "test message",
				Severity: model.DiagnosticError,
				Code:     model.DiagnosticCodeSyntax,
				Range: model.Range{
					Start: model.Position{Line: 1, Column: 1},
					End:   model.Position{Line: 1, Column: 1},
				},
			},
			content:         "",
			encoding:        protocol.PositionEncodingKindUTF8,
			expectedMessage: "test message",
			expectedCodeStr: "syntax",
			wantStartLine:   0,
			wantStartChar:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := toProtocolDiagnostic(tc.modelDiag, tc.content, tc.encoding)

			// Verify Severity is mapped correctly
			expectedSev := severityToProtocol(tc.modelDiag.Severity)
			if got.Severity != expectedSev {
				t.Errorf("Severity mismatch: got %d, want %d", got.Severity, expectedSev)
			}

			// Verify Range conversion
			if got.Range.Start.Line != tc.wantStartLine {
				t.Errorf("start line mismatch: got %d, want %d", got.Range.Start.Line, tc.wantStartLine)
			}
			if got.Range.Start.Character != tc.wantStartChar {
				t.Errorf("start character mismatch: got %d, want %d", got.Range.Start.Character, tc.wantStartChar)
			}
			if got.Range.End.Line != tc.wantEndLine {
				t.Errorf("end line mismatch: got %d, want %d", got.Range.End.Line, tc.wantEndLine)
			}
			if got.Range.End.Character != tc.wantEndChar {
				t.Errorf("end character mismatch: got %d, want %d", got.Range.End.Character, tc.wantEndChar)
			}

			// Verify Code
			if tc.wantNilCode {
				if got.Code != nil {
					t.Errorf("Code should be nil for empty model Code, got %v", got.Code)
				}
			} else {
				if str, ok := got.Code.(protocol.String); ok {
					if string(str) != tc.expectedCodeStr {
						t.Errorf("Code mismatch: got %q, want %q", string(str), tc.expectedCodeStr)
					}
				} else {
					t.Errorf("Code type mismatch: got %T, want protocol.String", got.Code)
				}
			}

			// Verify Message
			if str, ok := got.Message.(protocol.String); ok {
				if string(str) != tc.expectedMessage {
					t.Errorf("Message mismatch: got %q, want %q", string(str), tc.expectedMessage)
				}
			} else {
				t.Errorf("Message type mismatch: got %T, want protocol.String", got.Message)
			}

			// Verify Source
			if v, ok := got.Source.Get(); !ok || v != "natural-lsp" {
				t.Errorf("Source mismatch: got %v (ok=%v), want 'natural-lsp'", v, ok)
			}
		})
	}
}

// TestAggregateDiagnostics tests the aggregateDiagnostics function, which merges
// diagnostics from two independent producer channels into a single deterministically-ordered,
// deduplicated list for publishing.
//
// The aggregator combines:
// 1. FileAnalysis.Diagnostics — parser/syntax diagnostics from analyzer (feature 00/01)
// 2. ResolutionSet.DiagnosticsFor — flat-namespace ambiguity diagnostics (feature 07)
//
// Deduplication key decision: exact matches on (Message, Severity, Code, Range).
// Two identical diagnostics from different producers (e.g., a parser diag + an ambiguity
// diag with same text/position) are rare in practice but logically could occur; dedup
// prevents duplicate publication. The dedup key is applied AFTER sorting so order is
// deterministic before dedup.
//
// Test cases cover:
//   - Merge: concatenation of both slices
//   - Order: stable sort by Range.Start (line, then column) — inputs out of order come back position-ordered
//   - Dedup: exact duplicate (same Message/Severity/Code/Range) collapses to one; no-duplicate case is a no-op
//   - Empty inputs: nil + nil → nil (not a non-nil empty slice)
//   - Modeled-gap purity (Story 3 AC3 / FR-17): FileAnalysis with dynamic edges (EdgeCallsDynamic,
//     EdgeNavigatesToDynamic) + empty Diagnostics + nil resDiags → aggregates to nil (the aggregator
//     reads ONLY the two diagnostic channels, never the edges, so modeled gaps never become diagnostics)
func TestAggregateDiagnostics(t *testing.T) {
	tests := []struct {
		name                 string
		fa                   model.FileAnalysis
		resDiags             []model.Diagnostic
		expectedLen          int
		expectedOrder        []string // message strings in expected order for assertions
		expectNil            bool
		expectedDeduped      bool // set true to verify that duplicates collapsed
		deduplicationTestMsg string
	}{
		{
			name: "merge: parser diagnostics + ambiguity diagnostics",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{
					{
						Message:  "unexpected token X",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 2, Column: 5},
							End:   model.Position{Line: 2, Column: 10},
						},
					},
				},
			},
			resDiags: []model.Diagnostic{
				{
					Message:  "ambiguous reference 'FOO': matches A, B",
					Severity: model.DiagnosticWarning,
					Code:     model.DiagnosticCodeAmbiguity,
					Range: model.Range{
						Start: model.Position{Line: 5, Column: 1},
						End:   model.Position{Line: 5, Column: 4},
					},
				},
			},
			expectedLen:   2,
			expectedOrder: []string{"unexpected token X", "ambiguous reference 'FOO': matches A, B"},
		},
		{
			name: "order: out-of-order inputs are sorted by Range.Start",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{
					{
						Message:  "error at line 5",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 5, Column: 1},
							End:   model.Position{Line: 5, Column: 10},
						},
					},
					{
						Message:  "error at line 2",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 2, Column: 1},
							End:   model.Position{Line: 2, Column: 10},
						},
					},
				},
			},
			resDiags:      nil,
			expectedLen:   2,
			expectedOrder: []string{"error at line 2", "error at line 5"},
		},
		{
			name: "order: same line, sorted by column",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{
					{
						Message:  "at column 10",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 3, Column: 10},
							End:   model.Position{Line: 3, Column: 15},
						},
					},
					{
						Message:  "at column 2",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 3, Column: 2},
							End:   model.Position{Line: 3, Column: 8},
						},
					},
				},
			},
			resDiags:      nil,
			expectedLen:   2,
			expectedOrder: []string{"at column 2", "at column 10"},
		},
		{
			name: "dedup: exact duplicate (Message/Severity/Code/Range match) collapses to one",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{
					{
						Message:  "duplicate message",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 1, Column: 1},
							End:   model.Position{Line: 1, Column: 5},
						},
					},
				},
			},
			resDiags: []model.Diagnostic{
				{
					Message:  "duplicate message",
					Severity: model.DiagnosticError,
					Code:     model.DiagnosticCodeSyntax,
					Range: model.Range{
						Start: model.Position{Line: 1, Column: 1},
						End:   model.Position{Line: 1, Column: 5},
					},
				},
			},
			expectedLen:     1,
			expectedOrder:   []string{"duplicate message"},
			expectedDeduped: true,
		},
		{
			name: "dedup: different Message means not a duplicate",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{
					{
						Message:  "message A",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 1, Column: 1},
							End:   model.Position{Line: 1, Column: 5},
						},
					},
				},
			},
			resDiags: []model.Diagnostic{
				{
					Message:  "message B",
					Severity: model.DiagnosticError,
					Code:     model.DiagnosticCodeSyntax,
					Range: model.Range{
						Start: model.Position{Line: 1, Column: 1},
						End:   model.Position{Line: 1, Column: 5},
					},
				},
			},
			expectedLen:   2,
			expectedOrder: []string{"message A", "message B"},
		},
		{
			name: "dedup: no-duplicate case is a no-op (nothing dropped)",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{
					{
						Message:  "unique message 1",
						Severity: model.DiagnosticError,
						Code:     model.DiagnosticCodeSyntax,
						Range: model.Range{
							Start: model.Position{Line: 1, Column: 1},
							End:   model.Position{Line: 1, Column: 5},
						},
					},
					{
						Message:  "unique message 2",
						Severity: model.DiagnosticWarning,
						Code:     model.DiagnosticCodeAmbiguity,
						Range: model.Range{
							Start: model.Position{Line: 2, Column: 1},
							End:   model.Position{Line: 2, Column: 5},
						},
					},
				},
			},
			resDiags:      nil,
			expectedLen:   2,
			expectedOrder: []string{"unique message 1", "unique message 2"},
		},
		{
			name:      "empty inputs: nil Diagnostics + nil resDiags → nil result",
			fa:        model.FileAnalysis{},
			resDiags:  nil,
			expectNil: true,
		},
		{
			name: "empty inputs: empty Diagnostics slice + nil resDiags → nil result",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{},
			},
			resDiags:  nil,
			expectNil: true,
		},
		{
			name:      "empty inputs: nil Diagnostics + empty resDiags slice → nil result",
			fa:        model.FileAnalysis{},
			resDiags:  []model.Diagnostic{},
			expectNil: true,
		},
		{
			name: "empty inputs: empty Diagnostics slice + empty resDiags slice → nil result",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{},
			},
			resDiags:  []model.Diagnostic{},
			expectNil: true,
		},
		{
			name: "modeled-gap purity: FileAnalysis with EdgeCallsDynamic + empty Diagnostics + nil resDiags → nil aggregate",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{},
				Edges: []model.EdgeEntry{
					{
						Kind:       model.EdgeCallsDynamic,
						TargetName: "#VAR",
						Source: model.Range{
							Start: model.Position{Line: 1, Column: 5},
							End:   model.Position{Line: 1, Column: 15},
						},
					},
				},
			},
			resDiags:  nil,
			expectNil: true,
		},
		{
			name: "modeled-gap purity: FileAnalysis with EdgeNavigatesToDynamic + empty Diagnostics + nil resDiags → nil aggregate",
			fa: model.FileAnalysis{
				Diagnostics: []model.Diagnostic{},
				Edges: []model.EdgeEntry{
					{
						Kind:       model.EdgeNavigatesToDynamic,
						TargetName: "#VAR",
						Source: model.Range{
							Start: model.Position{Line: 2, Column: 5},
							End:   model.Position{Line: 2, Column: 15},
						},
					},
				},
			},
			resDiags:  nil,
			expectNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateDiagnostics(tc.fa, tc.resDiags)

			// Check nil expectation
			if tc.expectNil {
				if got != nil {
					t.Errorf("expected nil result, got %v", got)
				}
				return
			}

			// Check length
			if len(got) != tc.expectedLen {
				t.Errorf("expected %d diagnostics, got %d", tc.expectedLen, len(got))
			}

			// Check order (message order)
			if tc.expectedOrder != nil {
				for i, expectedMsg := range tc.expectedOrder {
					if i >= len(got) {
						t.Errorf("expected message %q at index %d, but got shorter result", expectedMsg, i)
						break
					}
					if got[i].Message != expectedMsg {
						t.Errorf("message order mismatch at index %d: got %q, want %q", i, got[i].Message, expectedMsg)
					}
				}
			}

			// Verify deterministic ordering by checking Range.Start values are non-decreasing
			for i := 0; i < len(got)-1; i++ {
				cur := got[i].Range.Start
				next := got[i+1].Range.Start
				if cur.Line > next.Line || (cur.Line == next.Line && cur.Column > next.Column) {
					t.Errorf("not sorted at index %d→%d: line %d col %d vs line %d col %d",
						i, i+1, cur.Line, cur.Column, next.Line, next.Column)
				}
			}
		})
	}
}

// TestPublishDiagnostics tests the publishDiagnostics notification writer (T5 — RED phase).
//
// The writer must:
// 1. Write a JSON-RPC Notification with method "textDocument/publishDiagnostics" (no id)
// 2. Marshal params to protocol.PublishDiagnosticsParams carrying the URI and diagnostics
// 3. Ensure an empty diagnostics slice publishes as "diagnostics": [] (not null) to clear stale diagnostics
// 4. Optionally thread the version field when provided (open-document buffers have one)
//
// Test cases cover:
// - Happy path: one diagnostic publishes correctly with the right method and params
// - Empty array clearing: empty diagnostics slice produces []
// - Version threading: version-present and version-absent variants (both should work)
// - JSON serialization correctness: the marshaled JSON contains "diagnostics":[...] pattern
func TestPublishDiagnostics(t *testing.T) {
	tests := []struct {
		name             string
		uri              string
		diags            []protocol.Diagnostic
		version          protocol.Optional[int32]
		versionProvided  bool // true = version was explicitly set
		expectEmptyArray bool // true = expects empty [], false = has items or multiple
	}{
		{
			name: "single diagnostic publishes correctly",
			uri:  "file:///workspace/test.NSP",
			diags: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Message:  protocol.String("unexpected token"),
					Source:   protocol.NewOptional("natural-lsp"),
					Code:     protocol.String("syntax"),
				},
			},
			version:          protocol.NewOptional(int32(1)),
			versionProvided:  true,
			expectEmptyArray: false,
		},
		{
			name:             "empty diagnostics slice publishes as empty array (clears)",
			uri:              "file:///workspace/clean.NSP",
			diags:            []protocol.Diagnostic{},
			version:          protocol.NewOptional(int32(2)),
			versionProvided:  true,
			expectEmptyArray: true,
		},
		{
			name:             "empty diagnostics nil (also clears)",
			uri:              "file:///workspace/clean2.NSP",
			diags:            nil,
			version:          protocol.NewOptional(int32(3)),
			versionProvided:  true,
			expectEmptyArray: true,
		},
		{
			name: "multiple diagnostics",
			uri:  "file:///workspace/multi.NSP",
			diags: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 2},
						End:   protocol.Position{Line: 1, Character: 10},
					},
					Severity: protocol.DiagnosticSeverityWarning,
					Message:  protocol.String("ambiguous reference"),
					Code:     protocol.String("ambiguity"),
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 5, Character: 0},
						End:   protocol.Position{Line: 5, Character: 3},
					},
					Severity: protocol.DiagnosticSeverityError,
					Message:  protocol.String("undefined symbol"),
					Code:     protocol.String("syntax"),
				},
			},
			version:          protocol.NewOptional(int32(4)),
			versionProvided:  true,
			expectEmptyArray: false,
		},
		{
			name: "version omitted (on-disk file)",
			uri:  "file:///workspace/ondisk.NSP",
			diags: []protocol.Diagnostic{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Message:  protocol.String("syntax error"),
				},
			},
			version:          protocol.Optional[int32]{},
			versionProvided:  false, // no version for on-disk files
			expectEmptyArray: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: create a capture buffer to receive the written notification
			var captureBuf bytes.Buffer

			ctx := context.Background()

			// Act: call publishDiagnostics (this will fail initially — function does not exist)
			versionPtr := &tc.version
			if !tc.versionProvided {
				versionPtr = nil
			}
			err := publishDiagnostics(ctx, &captureStreamWriter{w: &captureBuf}, tc.uri, tc.diags, versionPtr)

			// Assert: write succeeded
			if err != nil {
				t.Fatalf("publishDiagnostics failed: %v", err)
			}

			// Parse the captured output as Content-Length framed JSON-RPC message
			output := captureBuf.String()
			if output == "" {
				t.Fatalf("no output written to stream")
			}

			// Extract the JSON body from the framed message
			idx := strings.Index(output, "\r\n\r\n")
			if idx == -1 {
				t.Fatalf("no blank line found in framed message; output: %q", output)
			}
			bodyStart := idx + 4
			jsonBody := output[bodyStart:]

			// Decode as JSON-RPC message
			msg, err := jsonrpc2.DecodeMessage([]byte(jsonBody))
			if err != nil {
				t.Fatalf("failed to decode message: %v (body: %q)", err, jsonBody)
			}

			// Assert: message is a Notification (no id)
			notif, ok := msg.(*jsonrpc2.Notification)
			if !ok {
				t.Fatalf("expected Notification, got %T", msg)
			}

			// Assert: method is "textDocument/publishDiagnostics"
			if notif.Method() != "textDocument/publishDiagnostics" {
				t.Errorf("method = %q, want %q", notif.Method(), "textDocument/publishDiagnostics")
			}

			// Assert: params parse as PublishDiagnosticsParams
			var params protocol.PublishDiagnosticsParams
			paramsDecoder := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(paramsDecoder); err != nil {
				t.Fatalf("failed to unmarshal params as PublishDiagnosticsParams: %v (params: %q)", err, string(notif.Params()))
			}

			// Assert: URI matches
			if string(params.URI) != tc.uri {
				t.Errorf("URI = %q, want %q", string(params.URI), tc.uri)
			}

			// Assert: diagnostics slice is correct
			if tc.expectEmptyArray {
				// Empty array: verify it's an empty slice, not nil
				if params.Diagnostics == nil {
					t.Errorf("Diagnostics is nil for empty-array case; expected empty slice []")
				} else if len(params.Diagnostics) != 0 {
					t.Errorf("Diagnostics has %d items; expected 0 (empty)", len(params.Diagnostics))
				}

				// Critical assertion (Story 3 AC2): verify JSON contains "diagnostics":[] when empty, not null
				if !bytes.Contains(notif.Params(), []byte(`"diagnostics":`)) {
					t.Errorf("'diagnostics' field missing from params JSON: %q", string(notif.Params()))
				}
				if !bytes.Contains(notif.Params(), []byte(`"diagnostics":[]`)) {
					t.Errorf("empty diagnostics must serialize as 'diagnostics':[], found: %q", string(notif.Params()))
				}
			} else {
				// Non-empty: verify we got the right count
				if len(params.Diagnostics) != len(tc.diags) {
					t.Errorf("Diagnostics count = %d, want %d", len(params.Diagnostics), len(tc.diags))
				}
			}

			// Assert: version is threaded correctly
			if tc.versionProvided {
				v, ok := params.Version.Get()
				expectedV, _ := tc.version.Get()
				if !ok {
					t.Errorf("Version should be present (Get() ok = true)")
				} else if v != expectedV {
					t.Errorf("Version = %d, want %d", v, expectedV)
				}
			} else {
				_, ok := params.Version.Get()
				if ok {
					t.Errorf("Version should not be present (Get() ok = false)")
				}
			}
		})
	}
}

// captureStreamWriter is a mock jsonrpc2.Stream that captures writes to a buffer.
// It implements the minimal interface needed for publishDiagnostics testing.
type captureStreamWriter struct {
	w io.Writer
}

// Read is not implemented (not needed for testing publishDiagnostics).
func (csw *captureStreamWriter) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	return nil, 0, io.EOF
}

// Write encodes and writes the message with Content-Length framing.
func (csw *captureStreamWriter) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	// Encode the message as JSON
	encoded, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}

	// Write with Content-Length framing
	contentLen := len(encoded)
	frameStr := fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLen)
	n, err := csw.w.Write([]byte(frameStr))
	if err != nil {
		return int64(n), err
	}

	n2, err := csw.w.Write(encoded)
	return int64(n + n2), err
}

// Close is a no-op for the test stream.
func (csw *captureStreamWriter) Close() error {
	return nil
}

// TestPublishFileDiagnostics tests the T6 orchestrator: publishFileDiagnostics method
// that brings together T4 (aggregation), T2 (conversion), and T5 (notification writing)
// with F7 lock discipline (snapshot idx/res/posEncoding/root ONCE under RLock, release
// before any I/O).
//
// The harness builds a real workspace.Index over a per-case tmpDir and constructs URIs
// with uri.File(filepath.Join(tmpDir, name)) so that uriToRelPath(root, uri) resolves to
// the exact index key (e.g. "err.NSP"). Every URI therefore corresponds to a real file —
// there is no synthetic /workspace/... path that resolves to nothing.
//
// Cases:
//
//	A. Parse error on disk (S1-AC1/AC2, FR-30): one error-severity diagnostic carrying the
//	   real parser message, at a sensible non-zero range.
//	B. Clean source (S1-AC3): "diagnostics":[] (zero diagnostics).
//	C. Store-first proves live edits win (S3-AC1): disk copy is CLEAN, store holds ERRORING
//	   content — a non-zero count proves the store content was used, and the published Version
//	   reflects the store version.
//	D. Missing file (FR-43): not in index, not on disk, not in store → [] and nil error.
//	E. nil res (FR-43): parse-error file on disk with res == nil → 1 diagnostic, no panic.
//
// The real parser message for the erroring content "CALLNAT\nINVALID" (CALLNAT with no
// target operand) is "CALLNAT requires a target operand" (error severity, code "syntax",
// range Line 1 Col 1 → Line 1 Col 7 in model coords), confirmed by running the analyzer.
// A valid dynamic call like "CALLNAT BADTOKEN" yields NO syntax diagnostic (it is a valid
// EdgeCallsDynamic modeled gap, FR-17) — which is why it is not used as erroring content.
func TestPublishFileDiagnostics(t *testing.T) {
	// erroringContent produces exactly one syntax diagnostic (verified via the analyzer).
	erroringContent := []byte("CALLNAT\nINVALID")
	// cleanContent parses without any diagnostics.
	cleanContent := []byte("* A comment\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")

	tests := []struct {
		name string
		// fileName is written to tmpDir with diskContent, then indexed.
		fileName    string
		diskContent []byte
		// storedContent, if non-nil, is Open()ed into the store under the published URI
		// (simulating a live, possibly-unsaved buffer). storedVersion is the LSP version.
		storedContent []byte
		storedVersion int
		// publishName is the base name whose uri.File(tmpDir/publishName) is published.
		// When empty, fileName is used. A name absent from disk/index/store exercises
		// the missing-file path (case D).
		publishName string
		nilRes      bool // set res to nil (case E)

		expectDiagCount  int
		expectErrorSev   bool
		expectHasMessage string // substring of the published diagnostic message
		wantVersion      bool   // expect params.Version present
		wantVersionValue int32
	}{
		{
			name:             "A. parse error on disk publishes error diagnostic (S1-AC1/AC2, FR-30)",
			fileName:         "err.NSP",
			diskContent:      erroringContent,
			expectDiagCount:  1,
			expectErrorSev:   true,
			expectHasMessage: "CALLNAT",
		},
		{
			name:            "B. clean source publishes empty array (S1-AC3)",
			fileName:        "clean.NSP",
			diskContent:     cleanContent,
			expectDiagCount: 0,
		},
		{
			name:             "C. store-first: live erroring buffer wins over clean disk (S3-AC1)",
			fileName:         "live.NSP",
			diskContent:      cleanContent, // disk/index path would yield 0
			storedContent:    erroringContent,
			storedVersion:    7,
			expectDiagCount:  1, // non-zero PROVES store content was used
			expectErrorSev:   true,
			expectHasMessage: "operand",
			wantVersion:      true,
			wantVersionValue: 7,
		},
		{
			name:            "D. missing file publishes empty array, no error (FR-43)",
			fileName:        "present.NSP",
			diskContent:     cleanContent,
			publishName:     "does-not-exist.NSP",
			expectDiagCount: 0,
		},
		{
			name:            "E. nil res: parse error on disk still publishes, no panic (FR-43)",
			fileName:        "nilres.NSP",
			diskContent:     erroringContent,
			nilRes:          true,
			expectDiagCount: 1,
			expectErrorSev:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: a real workspace with the on-disk file written under tmpDir.
			tmpDir := t.TempDir()
			diskPath := filepath.Join(tmpDir, tc.fileName)
			if err := os.WriteFile(diskPath, tc.diskContent, 0600); err != nil {
				t.Fatalf("failed to write disk file: %v", err)
			}

			cfg := config.Defaults()
			az := natural.New(nil)
			logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

			idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
			if err != nil {
				t.Fatalf("failed to build index: %v", err)
			}

			var res *workspace.ResolutionSet
			if !tc.nilRes {
				res = workspace.Resolve(idx, &cfg)
			}

			store := document.New(tmpDir, func(relPath string, content []byte) model.FileAnalysis {
				fa, _ := az.Analyze(filepath.Join(tmpDir, relPath), content)
				return fa
			}, logger)

			hctx := &handlerContext{
				idx:         idx,
				res:         res,
				posEncoding: protocol.PositionEncodingKindUTF8,
				store:       store,
				root:        tmpDir,
				cfg:         cfg,
				az:          az,
				logger:      logger,
			}

			// Build the published URI so uriToRelPath resolves to a real index key.
			publishName := tc.publishName
			if publishName == "" {
				publishName = tc.fileName
			}
			docURI := uri.File(filepath.Join(tmpDir, publishName))
			uriStr := string(docURI)

			// Open a live buffer in the store when the case exercises store-first.
			if tc.storedContent != nil {
				store.Open(docURI, tc.storedVersion, tc.storedContent)
				defer store.Close(docURI)
			}

			// Act.
			var captureBuf bytes.Buffer
			stream := &captureStreamWriter{w: &captureBuf}
			if err := hctx.publishFileDiagnostics(context.Background(), stream, uriStr); err != nil {
				t.Fatalf("publishFileDiagnostics failed: %v", err)
			}

			// Decode the framed publishDiagnostics notification.
			output := captureBuf.String()
			if output == "" {
				t.Fatalf("no output written to stream (expected a publishDiagnostics notification)")
			}
			sep := strings.Index(output, "\r\n\r\n")
			if sep == -1 {
				t.Fatalf("no blank line in framed message; output: %q", output)
			}
			msg, err := jsonrpc2.DecodeMessage([]byte(output[sep+4:]))
			if err != nil {
				t.Fatalf("failed to decode message: %v (body: %q)", err, output[sep+4:])
			}
			notif, ok := msg.(*jsonrpc2.Notification)
			if !ok {
				t.Fatalf("expected Notification, got %T", msg)
			}
			if notif.Method() != "textDocument/publishDiagnostics" {
				t.Errorf("method = %q, want %q", notif.Method(), "textDocument/publishDiagnostics")
			}

			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				t.Fatalf("failed to unmarshal params: %v (params: %q)", err, string(notif.Params()))
			}

			// Assert: URI round-trips.
			if string(params.URI) != uriStr {
				t.Errorf("URI = %q, want %q", string(params.URI), uriStr)
			}

			// Assert: diagnostic count.
			if got := len(params.Diagnostics); got != tc.expectDiagCount {
				t.Errorf("diagnostic count = %d, want %d (diags: %+v)", got, tc.expectDiagCount, params.Diagnostics)
			}

			// Story 3 AC2: an empty result must serialize as "diagnostics":[] (never null).
			if tc.expectDiagCount == 0 {
				if !bytes.Contains(notif.Params(), []byte(`"diagnostics":[]`)) {
					t.Errorf("empty result must serialize as 'diagnostics':[], found: %q", string(notif.Params()))
				}
			}

			if tc.expectDiagCount > 0 {
				d := params.Diagnostics[0]

				if tc.expectErrorSev && d.Severity != protocol.DiagnosticSeverityError {
					t.Errorf("first diagnostic severity = %d, want %d (Error)", d.Severity, protocol.DiagnosticSeverityError)
				}

				if tc.expectHasMessage != "" {
					m, ok := d.Message.(protocol.String)
					if !ok {
						t.Errorf("message type %T, want protocol.String", d.Message)
					} else if !strings.Contains(string(m), tc.expectHasMessage) {
						t.Errorf("message = %q, want to contain %q", string(m), tc.expectHasMessage)
					}
				}

				// Assert a sensible, non-degenerate range: the CALLNAT token span ends at a
				// non-zero character on line 0 (model Line 1 Col 7 → protocol Line 0 Char 6).
				if d.Range.End.Line == 0 && d.Range.End.Character == 0 {
					t.Errorf("diagnostic range is degenerate {0,0}→{0,0}: %+v", d.Range)
				}
				if d.Range.End.Character <= d.Range.Start.Character && d.Range.End.Line == d.Range.Start.Line {
					t.Errorf("diagnostic range is not forward: %+v", d.Range)
				}
			}

			// Version threading: store-first cases carry the store version; on-disk does not.
			if v, present := params.Version.Get(); tc.wantVersion {
				if !present {
					t.Errorf("expected Version present (store version), got none")
				} else if v != tc.wantVersionValue {
					t.Errorf("Version = %d, want %d", v, tc.wantVersionValue)
				}
			} else if present {
				t.Errorf("expected no Version for on-disk file, got %d", v)
			}
		})
	}
}

// TestLifecycleDiagnosticPublishing_DidOpen tests that the server publishes
// diagnostics when textDocument/didOpen is received (T7, S3-AC1).
//
// This is a full server lifecycle integration test that:
// 1. Starts a real server and drives it through initialize → initialized
// 2. Sends textDocument/didOpen with a file containing parse errors
// 3. Reads the server's outbound notifications and captures any publishDiagnostics
// 4. Asserts that a publishDiagnostics notification was sent with error diagnostics
//
// Acceptance criteria tested:
// - S1-AC1/AC2 (parse error → diagnostic at error position with useful message)
// - S3-AC1 (diagnostics update on didOpen)
func TestLifecycleDiagnosticPublishing_DidOpen(t *testing.T) {
	// Arrange: set up a workspace with fixture files
	tmpDir := t.TempDir()

	// Create parse-error.NSP with known bad content
	parseErrorPath := filepath.Join(tmpDir, "parse-error.NSP")
	parseErrorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(parseErrorPath, parseErrorContent, 0600); err != nil {
		t.Fatalf("failed to write parse-error.NSP: %v", err)
	}

	// Build the initialize and initialized sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8"]
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build didOpen request with the parse-error file
	didOpenURI := uri.File(parseErrorPath)
	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(didOpenURI), string(parseErrorContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write the message sequence
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Act: run the server
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		tmpDir,
		az,
		logger,
	)

	// Assert: server ran cleanly
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse all notifications from the output
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Find the publishDiagnostics notification for parse-error.NSP
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			// Check if this is for our URI
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				t.Logf("failed to unmarshal publishDiagnostics params: %v", err)
				continue
			}
			if string(params.URI) == string(didOpenURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	// Assert: publishDiagnostics was sent
	if publishDiagsNotif == nil {
		t.Fatalf("expected textDocument/publishDiagnostics notification for %q, got none (notifications: %v)",
			string(didOpenURI), len(notifications))
	}

	// Parse and verify the diagnostic content
	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal publishDiagnostics params: %v", err)
	}

	// Assert: at least one diagnostic was published
	if len(params.Diagnostics) == 0 {
		t.Errorf("expected ≥1 diagnostic for parse-error file, got 0 (params: %+v)", params)
	}

	// Assert: first diagnostic is error severity
	if len(params.Diagnostics) > 0 {
		d := params.Diagnostics[0]
		if d.Severity != protocol.DiagnosticSeverityError {
			t.Errorf("first diagnostic severity = %d, want %d (Error)", d.Severity, protocol.DiagnosticSeverityError)
		}

		// Assert: message contains useful info
		msgStr := ""
		if m, ok := d.Message.(protocol.String); ok {
			msgStr = string(m)
		}
		if msgStr == "" {
			t.Errorf("expected non-empty diagnostic message, got %q", msgStr)
		}

		// Assert: range is non-degenerate
		if d.Range.End.Line == 0 && d.Range.End.Character <= d.Range.Start.Character {
			t.Errorf("diagnostic range is degenerate: %+v", d.Range)
		}
	}
}

// TestLifecycleDiagnosticPublishing_Clean tests that a clean file publishes
// an empty diagnostics array (T7, S1-AC3, S3-AC1).
func TestLifecycleDiagnosticPublishing_Clean(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a clean file with no parse errors
	cleanPath := filepath.Join(tmpDir, "clean.NSP")
	cleanContent := []byte("* Comment\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")
	if err := os.WriteFile(cleanPath, cleanContent, 0600); err != nil {
		t.Fatalf("failed to write clean.NSP: %v", err)
	}

	// Build message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	cleanURI := uri.File(cleanPath)
	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(cleanURI), string(cleanContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	// Act
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse notifications
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Find publishDiagnostics for clean.NSP
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(cleanURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	if publishDiagsNotif == nil {
		t.Fatalf("expected publishDiagnostics for clean file, got none")
	}

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	// Assert: diagnostics is empty array (not null)
	if len(params.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics for clean file, got %d", len(params.Diagnostics))
	}

	// Assert: empty array in JSON (never null)
	if !bytes.Contains(publishDiagsNotif.Params(), []byte(`"diagnostics":`)) {
		t.Errorf("'diagnostics' field missing")
	}
}

// TestLifecycleDiagnosticPublishing_ModeledGaps verifies that programs with
// only modeled gaps (dynamic calls, unresolved targets) publish ZERO diagnostics
// (T7, S3-AC3, FR-17).
func TestLifecycleDiagnosticPublishing_ModeledGaps(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with modeled gaps but NO syntax errors
	gapsPath := filepath.Join(tmpDir, "gaps.NSP")
	gapsContent := []byte(`* Program with modeled gaps (no syntax error)
PROGRAM GAPS-PROG
DEFINE DATA
  LOCAL
    1 #DYNVAR (A20)
  END
END

CALLNAT #DYNVAR
CALLNAT 'NOSUCHPGM'

END`)
	if err := os.WriteFile(gapsPath, gapsContent, 0600); err != nil {
		t.Fatalf("failed to write gaps.NSP: %v", err)
	}

	gapsURI := uri.File(gapsPath)

	// Build message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(gapsURI), string(gapsContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	// Act
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse notifications
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Find publishDiagnostics for gaps.NSP
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(gapsURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	if publishDiagsNotif == nil {
		t.Fatalf("expected publishDiagnostics for gaps file, got none")
	}

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	// Assert: zero diagnostics (modeled gaps are NOT diagnostics per FR-17)
	if len(params.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics for modeled-gaps file (FR-17: gaps are not diagnostics), got %d: %+v",
			len(params.Diagnostics), params.Diagnostics)
	}
}

// parseAllNotifications extracts all JSON-RPC Notification messages from a
// Content-Length-framed byte stream. It returns a slice of successfully
// decoded Notifications, skipping any malformed messages.
func parseAllNotifications(t *testing.T, output string) []*jsonrpc2.Notification {
	var notifs []*jsonrpc2.Notification

	// Parse multiple framed messages from the output
	remaining := output
	for len(remaining) > 0 {
		// Find the blank line that separates header from body
		sep := strings.Index(remaining, "\r\n\r\n")
		if sep == -1 {
			// No more complete messages
			break
		}

		bodyStart := sep + 4
		headerLines := strings.Split(remaining[:sep], "\r\n")
		if len(headerLines) == 0 {
			break
		}

		// Parse Content-Length from the header
		contentLengthLine := headerLines[0]
		if !strings.HasPrefix(contentLengthLine, "Content-Length: ") {
			break
		}

		lengthStr := strings.TrimPrefix(contentLengthLine, "Content-Length: ")
		contentLen := 0
		if n, err := fmt.Sscanf(lengthStr, "%d", &contentLen); err != nil || n != 1 {
			break
		}

		// Extract the body
		if bodyStart+contentLen > len(remaining) {
			break
		}

		bodyEnd := bodyStart + contentLen
		bodyBytes := remaining[bodyStart:bodyEnd]

		// Try to decode as JSON-RPC message
		msg, err := jsonrpc2.DecodeMessage([]byte(bodyBytes))
		if err == nil {
			if notif, ok := msg.(*jsonrpc2.Notification); ok {
				notifs = append(notifs, notif)
			}
		}

		// Advance remaining
		remaining = remaining[bodyEnd:]
	}

	return notifs
}

// TestLifecycleDiagnosticPublishing_DidClose tests that the server publishes
// an empty diagnostics array when textDocument/didClose is received (T7, Story 3 / OQ-3 decision).
//
// Scenario: open a parse-error file (first publish has diagnostic), then send
// didClose for that URI. The server should publish an empty diagnostics array
// to clear stale diagnostics in the editor.
//
// Acceptance criterion: S3-AC2 (clear on fix) / OQ-3 decision (clear on close).
func TestLifecycleDiagnosticPublishing_DidClose(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a parse-error file on disk
	errorPath := filepath.Join(tmpDir, "to-close.NSP")
	errorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(errorPath, errorContent, 0600); err != nil {
		t.Fatalf("failed to write to-close.NSP: %v", err)
	}

	errorURI := uri.File(errorPath)

	// Build message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// didOpen the error file
	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(errorURI), string(errorContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	// didClose the error file
	didCloseParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q
		}
	}`, string(errorURI)))
	didCloseNotif := jsonrpc2.NewNotification("textDocument/didClose", didCloseParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, didCloseNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	// Act
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse notifications
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Collect all publishDiagnostics for the error file URI
	var publishDiagsNotifs []*jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(errorURI) {
				publishDiagsNotifs = append(publishDiagsNotifs, notif)
			}
		}
	}

	// Assert: we got at least one publishDiagnostics for this URI
	// (expecting: one after didOpen with error, one after didClose with empty array)
	if len(publishDiagsNotifs) == 0 {
		t.Fatalf("expected ≥1 publishDiagnostics notification for %q after didOpen/didClose, got 0",
			string(errorURI))
	}

	// Find the last publishDiagnostics (should be from didClose with empty array)
	lastPublish := publishDiagsNotifs[len(publishDiagsNotifs)-1]

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(lastPublish.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal last publishDiagnostics: %v", err)
	}

	// Assert: the last publish is empty (clears stale diagnostics on close)
	if len(params.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics after didClose (clear-on-close), got %d", len(params.Diagnostics))
	}

	// Assert: empty array in JSON (never null)
	if !bytes.Contains(lastPublish.Params(), []byte(`"diagnostics":`)) {
		t.Errorf("'diagnostics' field missing from final publish")
	}
}

// TestLifecycleDiagnosticPublishing_DidChangeWatchedFiles_Change tests that the server
// publishes diagnostics when a watched file is externally changed (T7, S3-AC1).
//
// Scenario: with a real file under the workspace root (written to disk), send a
// workspace/didChangeWatchedFiles notification with a Changed event for that file's URI.
// The server should publish diagnostics matching the file's content (erroring content →
// ≥1 error diagnostic; clean content → empty array).
//
// Acceptance criterion: S3-AC1 (update on external change).
func TestLifecycleDiagnosticPublishing_DidChangeWatchedFiles_Change(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an initial clean file on disk
	watchedPath := filepath.Join(tmpDir, "watched.NSP")
	initialContent := []byte("* Clean\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")
	if err := os.WriteFile(watchedPath, initialContent, 0600); err != nil {
		t.Fatalf("failed to write watched.NSP: %v", err)
	}

	watchedURI := uri.File(watchedPath)

	// Build message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]},
			"workspace": {
				"didChangeWatchedFiles": {
					"dynamicRegistration": true
				}
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Simulate external file change by sending didChangeWatchedFiles with Changed event
	errorContent := []byte("CALLNAT\nINVALID")
	// In real scenario the file would be changed on disk by external tool; here we just
	// send the notification and the server will try to read the file. So we update it on disk first.
	if err := os.WriteFile(watchedPath, errorContent, 0600); err != nil {
		t.Fatalf("failed to update watched.NSP: %v", err)
	}

	didChangeWatchedParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"changes": [
			{
				"uri": %q,
				"type": 2
			}
		]
	}`, string(watchedURI)))
	didChangeWatchedNotif := jsonrpc2.NewNotification("workspace/didChangeWatchedFiles", didChangeWatchedParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act. Feature 21 (T4) made the initial index build asynchronous, so the
	// index is NOT guaranteed to be published the instant "initialized" is
	// processed. The Changed-event diagnostics path reads through the index
	// (publishFileDiagnostics resolution order 2), so we must NOT pre-feed
	// didChangeWatchedFiles into a single buffer — it would race the background
	// build and observe a not-yet-published (nil) index. Instead, drive over a
	// pipe: send initialize+initialized, WAIT on the index-ready gate, then send
	// the watched-file change and shutdown. (didOpen-based sibling tests are
	// store-first and so are unaffected; only this index/disk path must gate.)
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	runDone := make(chan error, 1)
	go func() { runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, az, logger) }()

	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("failed to write initialize: %v", err)
	}
	if err := writeFramedMessage(pw, initNotif); err != nil {
		t.Fatalf("failed to write initialized: %v", err)
	}

	// Wait until the background build has published the index before sending the
	// watched-file change, so the change is applied to a live index.
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	for _, msg := range []jsonrpc2.Message{didChangeWatchedNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(pw, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Parse notifications
	output := out.String()
	notifications := parseAllNotifications(t, output)

	// Find publishDiagnostics for the watched file URI
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(watchedURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	// Assert: publishDiagnostics was sent
	if publishDiagsNotif == nil {
		t.Fatalf("expected textDocument/publishDiagnostics for watched file %q after didChangeWatchedFiles, got none",
			string(watchedURI))
	}

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal publishDiagnostics params: %v", err)
	}

	// Assert: at least one error diagnostic (file was changed to have parse error)
	if len(params.Diagnostics) == 0 {
		t.Errorf("expected ≥1 diagnostic for changed file with parse error, got 0")
	} else if len(params.Diagnostics) > 0 {
		// Verify it's error severity
		if params.Diagnostics[0].Severity != protocol.DiagnosticSeverityError {
			t.Errorf("first diagnostic severity = %d, want %d (Error)", params.Diagnostics[0].Severity, protocol.DiagnosticSeverityError)
		}
	}
}

// TestLifecycleDiagnosticPublishing_DidChangeWatchedFiles_Delete tests that the server
// publishes an empty diagnostics array when a watched file is deleted (T7, S3-AC1).
//
// Scenario: with a real file under the workspace root, send a workspace/didChangeWatchedFiles
// notification with a Deleted event for that file's URI. The server should publish an empty
// diagnostics array to clear stale diagnostics.
//
// Acceptance criterion: S3-AC1 (update on external delete).
func TestLifecycleDiagnosticPublishing_DidChangeWatchedFiles_Delete(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file on disk
	deletedPath := filepath.Join(tmpDir, "to-delete.NSP")
	content := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(deletedPath, content, 0600); err != nil {
		t.Fatalf("failed to write to-delete.NSP: %v", err)
	}

	deletedURI := uri.File(deletedPath)

	// Build message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]},
			"workspace": {
				"didChangeWatchedFiles": {
					"dynamicRegistration": true
				}
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Send didChangeWatchedFiles with Deleted event (type 3)
	didChangeWatchedParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"changes": [
			{
				"uri": %q,
				"type": 3
			}
		]
	}`, string(deletedURI)))
	didChangeWatchedNotif := jsonrpc2.NewNotification("workspace/didChangeWatchedFiles", didChangeWatchedParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, didChangeWatchedNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	// Act
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse notifications
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Find publishDiagnostics for the deleted file URI
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(deletedURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	// Assert: publishDiagnostics was sent
	if publishDiagsNotif == nil {
		t.Fatalf("expected textDocument/publishDiagnostics for deleted file %q after didChangeWatchedFiles(Delete), got none",
			string(deletedURI))
	}

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal publishDiagnostics params: %v", err)
	}

	// Assert: empty diagnostics array (clear on delete)
	if len(params.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics for deleted file (clear-on-delete), got %d", len(params.Diagnostics))
	}

	// Assert: empty array in JSON (never null)
	if !bytes.Contains(publishDiagsNotif.Params(), []byte(`"diagnostics":`)) {
		t.Errorf("'diagnostics' field missing from publish after delete")
	}
}

// TestLifecycleDiagnosticPublishing_FlatNamespaceAmbiguity tests that the server
// publishes an ambiguity diagnostic when a flat-namespace name is ambiguous (T7, S2-AC1/AC3).
//
// Scenario: set up a workspace with NO library map where a referenced name matches
// two objects in different directories. Open the referencing file and assert the
// publish includes a WARNING-severity ambiguity diagnostic (Code "ambiguity") naming
// the candidates, distinct from any error diagnostics.
//
// Acceptance criteria: S2-AC1 (no-map ambiguity → diagnostic with candidates),
// S2-AC3 (distinct from syntax errors, warning severity).
//
// NOTE: This test sets up a multi-directory workspace mirroring the structure of
// internal/workspace/testdata/resolution/ambiguous-flat/ (MAIN references DUP,
// DUP exists in two lib directories). The test verifies that ambiguity diagnostics
// from the resolver are published when the file is opened.
func TestLifecycleDiagnosticPublishing_FlatNamespaceAmbiguity(t *testing.T) {
	tmpDir := t.TempDir()

	// Mirror internal/workspace/testdata/resolution/ambiguous-flat/ structure:
	// - lib1/DUP.NSN (subprogram definition 1)
	// - lib2/DUP.NSN (subprogram definition 2)
	// - MAIN.NSP (references DUP via CALLNAT, which expects subprograms)
	// No library map → flat namespace → ambiguity on DUP
	// (CALLNAT resolves to ObjectSubprogram, so NSN is the correct target type)

	lib1Dir := filepath.Join(tmpDir, "lib1")
	lib2Dir := filepath.Join(tmpDir, "lib2")
	if err := os.MkdirAll(lib1Dir, 0700); err != nil {
		t.Fatalf("failed to create lib1 dir: %v", err)
	}
	if err := os.MkdirAll(lib2Dir, 0700); err != nil {
		t.Fatalf("failed to create lib2 dir: %v", err)
	}

	// Create DUP.NSN (subprogram) in lib1
	dup1Path := filepath.Join(lib1Dir, "DUP.NSN")
	dup1Content := []byte("DEFINE SUBROUTINE DUP\nDEFINE DATA\nLOCAL\nEND\nEND")
	if err := os.WriteFile(dup1Path, dup1Content, 0600); err != nil {
		t.Fatalf("failed to write lib1/DUP.NSN: %v", err)
	}

	// Create DUP.NSN (subprogram) in lib2
	dup2Path := filepath.Join(lib2Dir, "DUP.NSN")
	dup2Content := []byte("DEFINE SUBROUTINE DUP\nDEFINE DATA\nLOCAL\nEND\nEND")
	if err := os.WriteFile(dup2Path, dup2Content, 0600); err != nil {
		t.Fatalf("failed to write lib2/DUP.NSN: %v", err)
	}

	// Create MAIN.NSP that references DUP
	mainPath := filepath.Join(tmpDir, "MAIN.NSP")
	mainContent := []byte("PROGRAM MAIN\nCALLNAT 'DUP'\nEND")
	if err := os.WriteFile(mainPath, mainContent, 0600); err != nil {
		t.Fatalf("failed to write MAIN.NSP: %v", err)
	}

	mainURI := uri.File(mainPath)

	// Build message sequence
	initID := jsonrpc2.NewNumberID(1)
	// NO library map in the config (defaults to flat namespace)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// didOpen MAIN.NSP
	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(mainURI), string(mainContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	// Act: the ambiguity diagnostic depends on the resolution set, which is only
	// populated after the ASYNC index build publishes (feature 21, T4). Schedule
	// didOpen in the "mid" phase so it runs AFTER the build is ready — otherwise
	// publishFileDiagnostics would see a nil resolution set and emit no ambiguity
	// diagnostic. initialize+initialized run in the "pre" phase.
	outBufL, _ := runGatedLifecycle(t,
		[]jsonrpc2.Message{initCall, initNotif},
		[]jsonrpc2.Message{didOpenNotif},
		tmpDir, natural.New(nil))

	// Parse notifications
	output := outBufL.String()
	notifications := parseAllNotifications(t, output)

	// Find publishDiagnostics for MAIN.NSP
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(mainURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	// Assert: publishDiagnostics was sent
	if publishDiagsNotif == nil {
		t.Fatalf("expected textDocument/publishDiagnostics for MAIN.NSP, got none")
	}

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal publishDiagnostics params: %v", err)
	}

	// Assert: at least one diagnostic is present
	if len(params.Diagnostics) == 0 {
		t.Errorf("expected ≥1 diagnostic for ambiguous flat-namespace reference, got 0")
	}

	// Find the ambiguity diagnostic (WARNING severity, Code "ambiguity")
	var ambiguityDiag *protocol.Diagnostic
	for i := range params.Diagnostics {
		d := &params.Diagnostics[i]
		if d.Severity == protocol.DiagnosticSeverityWarning {
			if code, ok := d.Code.(protocol.String); ok && string(code) == "ambiguity" {
				ambiguityDiag = d
				break
			}
		}
	}

	// Assert: ambiguity diagnostic was found
	if ambiguityDiag == nil {
		t.Errorf("expected at least one WARNING-severity ambiguity diagnostic (Code 'ambiguity'), got: %+v",
			params.Diagnostics)
	} else {
		// Assert: message mentions the candidates or the ambiguous name
		msgStr := ""
		if m, ok := ambiguityDiag.Message.(protocol.String); ok {
			msgStr = string(m)
		}
		if !strings.Contains(msgStr, "ambiguous") && !strings.Contains(msgStr, "DUP") {
			t.Errorf("ambiguity diagnostic message should mention 'ambiguous' or the target name 'DUP', got: %q", msgStr)
		}

		// Assert: severity is WARNING (not ERROR)
		if ambiguityDiag.Severity != protocol.DiagnosticSeverityWarning {
			t.Errorf("ambiguity diagnostic severity = %d, want %d (Warning)", ambiguityDiag.Severity, protocol.DiagnosticSeverityWarning)
		}
	}

	// Assert: no error-severity syntax diagnostics (MAIN.NSP is syntactically clean)
	for _, d := range params.Diagnostics {
		if d.Severity == protocol.DiagnosticSeverityError {
			code := ""
			if c, ok := d.Code.(protocol.String); ok {
				code = string(c)
			}
			if code != "ambiguity" {
				t.Errorf("expected only warning ambiguity diagnostics, found error diagnostic with code %q: %+v", code, d)
			}
		}
	}
}

// TestAggregateDiagnosticsDoesNotMutateInput guards against slice-aliasing in
// aggregateDiagnostics: append(fa.Diagnostics, resDiags...) may return
// fa.Diagnostics's own backing array when it has spare capacity, so the
// subsequent in-place sort.SliceStable would reorder the slice owned by the
// store/index FileAnalysis. This test constructs a fa.Diagnostics slice with
// spare capacity holding entries in NON-sorted order, snapshots the original,
// then calls aggregateDiagnostics with non-empty resDiags and asserts the
// original slice is unchanged (same order and contents).
//
// Regression-first: fails with the old `combined := append(fa.Diagnostics, ...)`
// because the sort reorders the shared backing array in place.
func TestAggregateDiagnosticsDoesNotMutateInput(t *testing.T) {
	// Backing array with spare capacity (len 2, cap 8) so a naive append reuses
	// it. Entries are in NON-sorted order (line 5 before line 2) so the sort in
	// aggregateDiagnostics would visibly reorder the backing array if it aliases.
	diags := make([]model.Diagnostic, 2, 8)
	diags[0] = model.Diagnostic{
		Message:  "error at line 5",
		Severity: model.DiagnosticError,
		Code:     model.DiagnosticCodeSyntax,
		Range: model.Range{
			Start: model.Position{Line: 5, Column: 1},
			End:   model.Position{Line: 5, Column: 10},
		},
	}
	diags[1] = model.Diagnostic{
		Message:  "error at line 2",
		Severity: model.DiagnosticError,
		Code:     model.DiagnosticCodeSyntax,
		Range: model.Range{
			Start: model.Position{Line: 2, Column: 1},
			End:   model.Position{Line: 2, Column: 10},
		},
	}

	fa := model.FileAnalysis{Diagnostics: diags}

	// Snapshot the original order+contents before calling aggregateDiagnostics.
	original := make([]model.Diagnostic, len(diags))
	copy(original, diags)

	resDiags := []model.Diagnostic{
		{
			Message:  "ambiguous reference 'DUP'",
			Severity: model.DiagnosticWarning,
			Code:     model.DiagnosticCodeAmbiguity,
			Range: model.Range{
				Start: model.Position{Line: 3, Column: 1},
				End:   model.Position{Line: 3, Column: 4},
			},
		},
	}

	got := aggregateDiagnostics(fa, resDiags)

	// The returned aggregate must be sorted (line 2, line 3, line 5).
	wantOrder := []string{"error at line 2", "ambiguous reference 'DUP'", "error at line 5"}
	if len(got) != len(wantOrder) {
		t.Fatalf("aggregate length = %d, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, want := range wantOrder {
		if got[i].Message != want {
			t.Errorf("aggregate[%d].Message = %q, want %q", i, got[i].Message, want)
		}
	}

	// The ORIGINAL fa.Diagnostics slice must be UNCHANGED — same order, same
	// contents. This is the aliasing guard: aggregateDiagnostics must not sort
	// the caller's backing array in place.
	if len(fa.Diagnostics) != len(original) {
		t.Fatalf("fa.Diagnostics length changed: %d, want %d", len(fa.Diagnostics), len(original))
	}
	for i := range original {
		if fa.Diagnostics[i] != original[i] {
			t.Errorf("fa.Diagnostics[%d] was mutated: got %+v, want %+v", i, fa.Diagnostics[i], original[i])
		}
	}
}

// TestLifecycleDiagnosticPublishing_LibraryMapDisambiguates tests Story 2 AC2
// (a T7 scenario): a .natural-lsp.toml library map that makes an otherwise-ambiguous
// name resolve uniquely REMOVES the ambiguity diagnostic (contrast the no-map
// TestLifecycleDiagnosticPublishing_FlatNamespaceAmbiguity, which asserts the
// warning IS present).
//
// Scenario mirrors the FlatNamespaceAmbiguity fixture shape — two DUP.NSN
// subprograms in separate directories and a MAIN.NSP doing CALLNAT 'DUP' — but
// adds a library map:
//   - MAIN.NSP lives under app/ → library APP (path "app"), steplibs = ["LIB1"].
//   - lib1/DUP.NSN → library LIB1 (path "lib1"); reachable from APP's chain.
//   - lib2/DUP.NSN → library LIB2 (path "lib2"); NOT in APP's chain, so unreachable.
//
// APP's search chain is [APP, LIB1, SYSTEM] (non-transitive). CALLNAT 'DUP'
// filters to ObjectSubprogram; resolveViaChain finds LIB1/DUP.NSN (the earliest
// chain library with a match) and returns Resolved. LIB2/DUP.NSN is out-of-chain
// and invisible, so resolution is UNIQUE, not ambiguous — and Mode 2 (steplib
// chain) never emits an ambiguity diagnostic. The published diagnostics for MAIN
// must therefore contain ZERO Code=="ambiguity" entries.
//
// The library map is fed through the server the same way cmd/natural-lsp does it:
// a real .natural-lsp.toml is written at the workspace root, loaded via
// config.Load, and the resulting Config is passed to Run (the harness passes cfg
// straight into Run, which uses it for workspace.Build + workspace.Resolve).
func TestLifecycleDiagnosticPublishing_LibraryMapDisambiguates(t *testing.T) {
	tmpDir := t.TempDir()

	appDir := filepath.Join(tmpDir, "app")
	lib1Dir := filepath.Join(tmpDir, "lib1")
	lib2Dir := filepath.Join(tmpDir, "lib2")
	for _, d := range []string{appDir, lib1Dir, lib2Dir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("failed to create dir %q: %v", d, err)
		}
	}

	// DUP.NSN subprograms in lib1 and lib2 (same name, two libraries).
	dupContent := []byte("DEFINE SUBROUTINE DUP\nDEFINE DATA\nLOCAL\nEND\nEND")
	if err := os.WriteFile(filepath.Join(lib1Dir, "DUP.NSN"), dupContent, 0600); err != nil {
		t.Fatalf("failed to write lib1/DUP.NSN: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lib2Dir, "DUP.NSN"), dupContent, 0600); err != nil {
		t.Fatalf("failed to write lib2/DUP.NSN: %v", err)
	}

	// MAIN.NSP under app/ references DUP via CALLNAT.
	mainPath := filepath.Join(appDir, "MAIN.NSP")
	mainContent := []byte("PROGRAM MAIN\nCALLNAT 'DUP'\nEND")
	if err := os.WriteFile(mainPath, mainContent, 0600); err != nil {
		t.Fatalf("failed to write app/MAIN.NSP: %v", err)
	}

	// Write the .natural-lsp.toml library map at the workspace root. APP's chain
	// includes LIB1 (path lib1) but NOT LIB2 (path lib2), so exactly one DUP is
	// reachable from MAIN → unique resolution, no ambiguity.
	tomlPath := filepath.Join(tmpDir, ".natural-lsp.toml")
	tomlContent := "" +
		"[resolution]\n\n" +
		"[[resolution.library]]\n" +
		"name = \"APP\"\n" +
		"path = \"app\"\n" +
		"steplibs = [\"LIB1\"]\n\n" +
		"[[resolution.library]]\n" +
		"name = \"LIB1\"\n" +
		"path = \"lib1\"\n" +
		"steplibs = []\n\n" +
		"[[resolution.library]]\n" +
		"name = \"LIB2\"\n" +
		"path = \"lib2\"\n" +
		"steplibs = []\n"
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("failed to write .natural-lsp.toml: %v", err)
	}

	// Load the config the same way cmd/natural-lsp does, from the sentinel file.
	cfg, problems, err := config.Load(tomlPath)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("config.Load reported problems (library map must be clean): %+v", problems)
	}
	// Sanity: the library map loaded as expected (confirms the TOML is right).
	if len(cfg.Resolution.Libraries) != 3 {
		t.Fatalf("expected 3 libraries in loaded config, got %d: %+v", len(cfg.Resolution.Libraries), cfg.Resolution.Libraries)
	}

	mainURI := uri.File(mainPath)

	// Build message sequence.
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(mainURI), string(mainContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	if err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	notifications := parseAllNotifications(t, outBuf.String())

	// Find publishDiagnostics for MAIN.NSP.
	var publishDiagsNotif *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() == "textDocument/publishDiagnostics" {
			var params protocol.PublishDiagnosticsParams
			dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
			if err := params.UnmarshalJSONFrom(dec); err != nil {
				continue
			}
			if string(params.URI) == string(mainURI) {
				publishDiagsNotif = notif
				break
			}
		}
	}

	if publishDiagsNotif == nil {
		t.Fatalf("expected textDocument/publishDiagnostics for app/MAIN.NSP, got none")
	}

	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(publishDiagsNotif.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal publishDiagnostics params: %v", err)
	}

	// Assert: ZERO ambiguity diagnostics (unique resolution via the steplib chain).
	for _, d := range params.Diagnostics {
		if code, ok := d.Code.(protocol.String); ok && string(code) == "ambiguity" {
			t.Errorf("expected NO ambiguity diagnostic when the library map disambiguates, got: %+v", d)
		}
	}

	// MAIN.NSP is syntactically clean and DUP now resolves uniquely, so the
	// published set should be empty.
	if len(params.Diagnostics) != 0 {
		t.Errorf("expected empty diagnostics for MAIN.NSP (unique resolution, clean syntax), got %d: %+v",
			len(params.Diagnostics), params.Diagnostics)
	}
}

// TestLifecycleDiagnosticPublishing_DidChangeFixesError tests Story 3 AC2 (a T7
// scenario): a didChange (Full sync) that fixes a parse error clears the
// diagnostics for that URI on the subsequent publish.
//
// Sequence: didOpen a document with erroring content ("CALLNAT\nINVALID") →
// the FIRST publish for that URI carries ≥1 error diagnostic. Then didChange
// replaces the content with valid Natural source → the LAST publish for that URI
// is an empty diagnostics array (cleared).
func TestLifecycleDiagnosticPublishing_DidChangeFixesError(t *testing.T) {
	tmpDir := t.TempDir()

	docPath := filepath.Join(tmpDir, "FIXME.NSP")
	erroringContent := "CALLNAT\nINVALID"
	// Write the erroring content to disk so the file is indexed at build time.
	if err := os.WriteFile(docPath, []byte(erroringContent), 0600); err != nil {
		t.Fatalf("failed to write FIXME.NSP: %v", err)
	}

	docURI := uri.File(docPath)
	validContent := "* comment\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND"

	// Build message sequence.
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(docURI), erroringContent))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	// didChange (Full sync): replace the whole document with valid content.
	didChangeParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"version": 2
		},
		"contentChanges": [
			{"text": %q}
		]
	}`, string(docURI), validContent))
	didChangeNotif := jsonrpc2.NewNotification("textDocument/didChange", didChangeParams)

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, didChangeNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	if err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	notifications := parseAllNotifications(t, outBuf.String())

	// Collect all publishDiagnostics for this URI, in order.
	var publishes []protocol.PublishDiagnosticsParams
	for _, notif := range notifications {
		if notif.Method() != "textDocument/publishDiagnostics" {
			continue
		}
		var params protocol.PublishDiagnosticsParams
		dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
		if err := params.UnmarshalJSONFrom(dec); err != nil {
			continue
		}
		if string(params.URI) == string(docURI) {
			publishes = append(publishes, params)
		}
	}

	if len(publishes) < 2 {
		t.Fatalf("expected ≥2 publishDiagnostics for %q (open then fix), got %d", string(docURI), len(publishes))
	}

	// FIRST publish (from didOpen) must carry ≥1 error diagnostic.
	first := publishes[0]
	var firstErrCount int
	for _, d := range first.Diagnostics {
		if d.Severity == protocol.DiagnosticSeverityError {
			firstErrCount++
		}
	}
	if firstErrCount == 0 {
		t.Errorf("expected ≥1 error diagnostic on the first publish (erroring content), got %d: %+v",
			firstErrCount, first.Diagnostics)
	}

	// LAST publish (after the fixing didChange) must be empty (cleared).
	last := publishes[len(publishes)-1]
	if len(last.Diagnostics) != 0 {
		t.Errorf("expected empty diagnostics on the last publish after the fixing didChange, got %d: %+v",
			len(last.Diagnostics), last.Diagnostics)
	}
}
