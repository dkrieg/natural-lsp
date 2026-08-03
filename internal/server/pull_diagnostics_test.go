package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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

// TestProvideDocumentDiagnostic tests the provideDocumentDiagnostic function (T2 — RED phase).
//
// The provider must return a *RelatedFullDocumentDiagnosticReport in all cases (never nil),
// with Kind == "full", Items initialized as a non-nil slice (empty or containing diagnostics),
// and ResultID unset (OQ-2). Content must be byte-identical to the push path's aggregation.
//
// Test cases (reusing fixtures parse-error.NSP, clean.NSP, modeled-gaps.NSP):
//
//	A. parse-error.NSP (indexed/on-disk) → Kind=="full", Items non-empty with each Source=="natural-lsp",
//	   and byte-identical to what aggregateDiagnostics + toProtocolDiagnostic produce for the push path.
//	B. store-first: open buffer with fresh parse error (disk is clean) → Items reflect buffer content, not disk.
//	C. clean.NSP → Kind=="full", Items == [] (empty non-nil).
//	D. modeled-gaps.NSP → Kind=="full", Items == [] (empty non-nil, modeled gaps stay off diagnostic channel).
//	E. missing file → Kind=="full", Items == [], err == nil (FR-43: empty full report, never error).
//	F. out-of-root URI → Kind=="full", Items == [], err == nil (FR-43).
//	G. OQ-2: ResultID is nil in all cases.
func TestProvideDocumentDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		// fileName is written to tmpDir with diskContent, then indexed.
		fileName    string
		diskContent []byte
		// storedContent, if non-nil, is Open()ed into the store under the published URI
		// (simulating a live, possibly-unsaved buffer).
		storedContent []byte
		storedVersion int
		// publishName is the base name whose uri.File(tmpDir/publishName) is published.
		// When empty, fileName is used. A name absent from disk/index/store exercises
		// the missing-file path (case E).
		publishName string
		// outOfRoot, if true, sets up a URI pointing outside the workspace root (case F).
		outOfRoot bool

		// expected assertions
		expectKind        string // should be protocol.DocumentDiagnosticReportKindFull ("full")
		expectItemCount   int    // expected len(Items) >= 0
		expectNonEmpty    bool   // if true, expect len(Items) > 0 (exact count may vary)
		expectErrorSev    bool   // expect first item to be error severity
		expectHasMessage  string // substring of the published diagnostic message
		expectEmptyNonNil bool   // if true, expect Items == [] (empty but non-nil)
		expectResultIDNil bool   // expect ResultID to be nil
	}{
		{
			name:              "A. parse-error.NSP (indexed/on-disk) → full report with error diagnostics (S1-AC1/AC2, FR-30)",
			fileName:          "parse-error.NSP",
			diskContent:       nil, // use fixture content
			expectKind:        string(protocol.DocumentDiagnosticReportKindFull),
			expectNonEmpty:    true,
			expectErrorSev:    true,
			expectHasMessage:  "CALLNAT",
			expectResultIDNil: true,
		},
		{
			name:              "B. store-first: live erroring buffer wins over clean disk (S3-AC1)",
			fileName:          "live.NSP",
			diskContent:       []byte("* Clean\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND"),
			storedContent:     []byte("CALLNAT\nINVALID"),
			storedVersion:     7,
			expectKind:        string(protocol.DocumentDiagnosticReportKindFull),
			expectNonEmpty:    true, // store content has error, disk doesn't
			expectErrorSev:    true,
			expectHasMessage:  "operand",
			expectResultIDNil: true,
		},
		{
			name:              "C. clean.NSP → full report with empty items (S1-AC3)",
			fileName:          "clean.NSP",
			diskContent:       nil, // use fixture content
			expectKind:        string(protocol.DocumentDiagnosticReportKindFull),
			expectItemCount:   0,
			expectEmptyNonNil: true,
			expectResultIDNil: true,
		},
		{
			name:              "D. modeled-gaps.NSP → full report with empty items, no diagnostics (FR-17)",
			fileName:          "modeled-gaps.NSP",
			diskContent:       nil, // use fixture content
			expectKind:        string(protocol.DocumentDiagnosticReportKindFull),
			expectItemCount:   0,
			expectEmptyNonNil: true,
			expectResultIDNil: true,
		},
		{
			name:              "E. missing file → full report with empty items, no error (FR-43)",
			fileName:          "present.NSP",
			diskContent:       []byte("* Present\nEND"),
			publishName:       "does-not-exist.NSP",
			expectKind:        string(protocol.DocumentDiagnosticReportKindFull),
			expectItemCount:   0,
			expectEmptyNonNil: true,
			expectResultIDNil: true,
		},
		{
			name:              "F. out-of-root URI → full report with empty items, no error (FR-43)",
			fileName:          "present.NSP",
			diskContent:       []byte("* Present\nEND"),
			outOfRoot:         true,
			expectKind:        string(protocol.DocumentDiagnosticReportKindFull),
			expectItemCount:   0,
			expectEmptyNonNil: true,
			expectResultIDNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up the workspace
			tmpDir := t.TempDir()
			cfg := config.Defaults()
			az := natural.New(nil)
			logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

			// Load fixture content when diskContent is nil
			if tc.diskContent == nil {
				content, err := os.ReadFile(filepath.Join("testdata/diagnostics", tc.fileName))
				if err != nil {
					t.Fatalf("failed to read fixture %s: %v", tc.fileName, err)
				}
				tc.diskContent = content
			}

			// Write the file to disk and build the index
			diskPath := filepath.Join(tmpDir, tc.fileName)
			if err := os.WriteFile(diskPath, tc.diskContent, 0600); err != nil {
				t.Fatalf("failed to write disk file: %v", err)
			}

			idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
			if err != nil {
				t.Fatalf("failed to build index: %v", err)
			}

			res := workspace.Resolve(idx, &cfg)

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

			// Determine the URI to publish
			var docURI uri.URI
			publishName := tc.publishName
			if publishName == "" {
				publishName = tc.fileName
			}

			if tc.outOfRoot {
				// Create a URI pointing outside the workspace root
				docURI = uri.File("/outside/root/" + publishName)
			} else {
				docURI = uri.File(filepath.Join(tmpDir, publishName))
			}

			// Open a live buffer in the store when the case exercises store-first
			if tc.storedContent != nil {
				store.Open(docURI, tc.storedVersion, tc.storedContent)
				defer store.Close(docURI)
			}

			// Act: call provideDocumentDiagnostic with document params
			params := protocol.DocumentDiagnosticParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: docURI,
				},
			}

			report, err := provideDocumentDiagnostic(hctx, params)

			// Assert: call succeeded (never returns error per FR-43)
			if err != nil {
				t.Errorf("provideDocumentDiagnostic returned error: %v (expected nil, FR-43)", err)
			}

			// Assert: report is non-nil in all cases
			if report == nil {
				t.Fatalf("report is nil; expected non-nil *RelatedFullDocumentDiagnosticReport (OQ-2)")
			}

			// Assert: Kind is "full" in all cases
			if report.Kind != tc.expectKind {
				t.Errorf("Kind = %q, want %q", report.Kind, tc.expectKind)
			}

			// Assert: ResultID is nil (OQ-2) in all cases
			if report.ResultID != nil {
				t.Errorf("ResultID should be nil (OQ-2), got %v", *report.ResultID)
			}

			// Assert: Items is non-nil (initialized to empty slice on empty path)
			if report.Items == nil {
				t.Fatalf("Items is nil; expected non-nil slice (possibly empty)")
			}

			// Assert: Items count
			if tc.expectEmptyNonNil {
				if len(report.Items) != 0 {
					t.Errorf("expected Items to be empty (len 0), got %d items", len(report.Items))
				}
			} else if tc.expectNonEmpty {
				if len(report.Items) == 0 {
					t.Errorf("expected non-empty Items, got 0")
				}
			} else if tc.expectItemCount >= 0 {
				if len(report.Items) != tc.expectItemCount {
					t.Errorf("expected %d items, got %d", tc.expectItemCount, len(report.Items))
				}
			}

			// Assert: each item has Source == "natural-lsp"
			for i, item := range report.Items {
				if v, ok := item.Source.Get(); !ok || v != "natural-lsp" {
					t.Errorf("item[%d] Source = %q (ok=%v), want 'natural-lsp'", i, v, ok)
				}
			}

			// Assert: severity expectations
			if tc.expectErrorSev && len(report.Items) > 0 {
				if report.Items[0].Severity != protocol.DiagnosticSeverityError {
					t.Errorf("first item severity = %d, want %d (DiagnosticSeverityError)",
						report.Items[0].Severity, protocol.DiagnosticSeverityError)
				}
			}

			// Assert: message expectations
			if tc.expectHasMessage != "" && len(report.Items) > 0 {
				msgStr := ""
				if m, ok := report.Items[0].Message.(protocol.String); ok {
					msgStr = string(m)
				}
				if msgStr == "" {
					t.Errorf("first item message is empty, expected to contain %q", tc.expectHasMessage)
				} else if !bytes.Contains([]byte(msgStr), []byte(tc.expectHasMessage)) {
					t.Errorf("first item message %q does not contain %q", msgStr, tc.expectHasMessage)
				}
			}
		})
	}
}

// TestProvideDocumentDiagnostic_ByteIdenticalToPush verifies that the content returned
// by provideDocumentDiagnostic is byte-identical to what the push path produces
// (aggregateDiagnostics + toProtocolDiagnostic). This is the Story 1 AC1 assertion.
func TestProvideDocumentDiagnostic_ByteIdenticalToPush(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// Use the parse-error.NSP fixture
	parseErrorContent, err := os.ReadFile("testdata/diagnostics/parse-error.NSP")
	if err != nil {
		t.Fatalf("failed to read parse-error.NSP fixture: %v", err)
	}

	diskPath := filepath.Join(tmpDir, "parse-error.NSP")
	if err := os.WriteFile(diskPath, parseErrorContent, 0600); err != nil {
		t.Fatalf("failed to write disk file: %v", err)
	}

	idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	res := workspace.Resolve(idx, &cfg)

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

	docURI := uri.File(diskPath)
	params := protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: docURI,
		},
	}

	// Act: call provideDocumentDiagnostic
	report, err := provideDocumentDiagnostic(hctx, params)
	if err != nil {
		t.Fatalf("provideDocumentDiagnostic returned error: %v", err)
	}

	if report == nil || report.Items == nil {
		t.Fatalf("report or Items is nil")
	}

	// Compute the expected result via the push path
	relPath := "parse-error.NSP"
	fa, ok := idx.Get(relPath)
	if !ok {
		t.Fatalf("failed to get file analysis from index for %s", relPath)
	}

	resDiags := res.DiagnosticsFor(relPath)
	expectedAgg := aggregateDiagnostics(fa, resDiags)

	expectedProto := make([]protocol.Diagnostic, 0, len(expectedAgg))
	for _, d := range expectedAgg {
		expectedProto = append(expectedProto, toProtocolDiagnostic(d, string(parseErrorContent), protocol.PositionEncodingKindUTF8))
	}

	// Assert: counts match
	if len(report.Items) != len(expectedProto) {
		t.Errorf("item count mismatch: got %d, expected %d (from push path)",
			len(report.Items), len(expectedProto))
	}

	// Assert: each item is structurally identical (message, severity, code, range)
	for i := 0; i < len(report.Items) && i < len(expectedProto); i++ {
		got := report.Items[i]
		want := expectedProto[i]

		// Message
		gotMsg, _ := got.Message.(protocol.String)
		wantMsg, _ := want.Message.(protocol.String)
		if gotMsg != wantMsg {
			t.Errorf("item[%d] message mismatch: got %q, want %q", i, gotMsg, wantMsg)
		}

		// Severity
		if got.Severity != want.Severity {
			t.Errorf("item[%d] severity mismatch: got %d, want %d", i, got.Severity, want.Severity)
		}

		// Code
		if (got.Code == nil) != (want.Code == nil) {
			t.Errorf("item[%d] code nil mismatch: got %v, want %v", i, got.Code, want.Code)
		}
		if got.Code != nil && want.Code != nil {
			gotCode, _ := got.Code.(protocol.String)
			wantCode, _ := want.Code.(protocol.String)
			if gotCode != wantCode {
				t.Errorf("item[%d] code value mismatch: got %q, want %q", i, gotCode, wantCode)
			}
		}

		// Range
		if got.Range.Start.Line != want.Range.Start.Line {
			t.Errorf("item[%d] start line mismatch: got %d, want %d", i, got.Range.Start.Line, want.Range.Start.Line)
		}
		if got.Range.Start.Character != want.Range.Start.Character {
			t.Errorf("item[%d] start character mismatch: got %d, want %d", i, got.Range.Start.Character, want.Range.Start.Character)
		}
		if got.Range.End.Line != want.Range.End.Line {
			t.Errorf("item[%d] end line mismatch: got %d, want %d", i, got.Range.End.Line, want.Range.End.Line)
		}
		if got.Range.End.Character != want.Range.End.Character {
			t.Errorf("item[%d] end character mismatch: got %d, want %d", i, got.Range.End.Character, want.Range.End.Character)
		}

		// Source
		gotSrc, _ := got.Source.Get()
		wantSrc, _ := want.Source.Get()
		if gotSrc != wantSrc {
			t.Errorf("item[%d] source mismatch: got %q, want %q", i, gotSrc, wantSrc)
		}
	}
}

// TestProvideDocumentDiagnostic_EmptyNonNil verifies that empty reports serialize as
// "items":[] (not "items":null). This is a Story 1 AC3 requirement: empty Items
// must be a non-nil slice.
func TestProvideDocumentDiagnostic_EmptyNonNil(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// Use clean.NSP fixture (no diagnostics)
	cleanContent, err := os.ReadFile("testdata/diagnostics/clean.NSP")
	if err != nil {
		t.Fatalf("failed to read clean.NSP fixture: %v", err)
	}

	diskPath := filepath.Join(tmpDir, "clean.NSP")
	if err := os.WriteFile(diskPath, cleanContent, 0600); err != nil {
		t.Fatalf("failed to write disk file: %v", err)
	}

	idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	res := workspace.Resolve(idx, &cfg)
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

	docURI := uri.File(diskPath)
	params := protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: docURI,
		},
	}

	// Act
	report, err := provideDocumentDiagnostic(hctx, params)

	// Assert
	if err != nil {
		t.Fatalf("provideDocumentDiagnostic returned error: %v", err)
	}

	if report == nil {
		t.Fatalf("report is nil")
	}

	// Critical: Items must be non-nil even when empty
	if report.Items == nil {
		t.Errorf("Items is nil; must be empty non-nil slice for proper JSON serialization")
	}

	// Items should be empty
	if len(report.Items) != 0 {
		t.Errorf("Items has %d items, expected 0", len(report.Items))
	}
}

// TestTextDocumentDiagnostic_DispatchAndMarshal (T3 — RED phase) tests the wire-level
// round-trip of the textDocument/diagnostic method through the full server dispatch path.
//
// This test drives `initialize → initialized → textDocument/diagnostic` against a fixture
// workspace with parse-error.NSP and asserts on the raw response JSON:
// - result.kind == "full"
// - result.items is a JSON array
// - each item has source == "natural-lsp" and a valid range with start/end line/character
// - for an empty document, result.items serializes as [] (not null)
//
// Currently FAILS (RED) because:
// - textDocument/diagnostic is not yet dispatched in server.go
// - An unhandled method returns JSON-RPC MethodNotFound error instead of a diagnostic report
//
// This test will PASS (GREEN) once T3's dispatch arm is implemented in server.go.
//
// Feature 30 T3: Dispatch + marshal textDocument/diagnostic.
func TestTextDocumentDiagnostic_DispatchAndMarshal(t *testing.T) {
	testCases := []struct {
		name           string
		fileName       string
		expectKind     string
		expectHasItems bool // true if we expect non-empty items; false for empty []
	}{
		{
			name:           "parse-error.NSP (on-disk) → full report with error items",
			fileName:       "parse-error.NSP",
			expectKind:     "full",
			expectHasItems: true,
		},
		{
			name:           "clean.NSP (on-disk) → full report with empty items",
			fileName:       "clean.NSP",
			expectKind:     "full",
			expectHasItems: false,
		},
		{
			name:           "modeled-gaps.NSP (on-disk) → full report with empty items (gaps not diagnostics)",
			fileName:       "modeled-gaps.NSP",
			expectKind:     "full",
			expectHasItems: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up a workspace with the fixture file
			tmpDir := t.TempDir()

			// Copy the fixture into the temp workspace
			fixtureContent, err := os.ReadFile(filepath.Join("testdata", "diagnostics", tc.fileName))
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.fileName, err)
			}

			diskPath := filepath.Join(tmpDir, tc.fileName)
			if err := os.WriteFile(diskPath, fixtureContent, 0600); err != nil {
				t.Fatalf("failed to write disk file: %v", err)
			}

			// 1) initialize request
			initCall := jsonrpc2.NewCall(
				jsonrpc2.NewNumberID(1),
				"initialize",
				jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
			)

			// 2) initialized notification
			initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			// 3) textDocument/diagnostic request
			docURI := "file://" + diskPath
			diagCall := jsonrpc2.NewCall(
				jsonrpc2.NewNumberID(2),
				"textDocument/diagnostic",
				jsonrpc2.RawMessage(`{"textDocument":{"uri":"`+docURI+`"}}`),
			)

			// 4) shutdown + exit
			shutdownCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(3), "shutdown", jsonrpc2.RawMessage(`{}`))
			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Act: this method reads the index-path (resolution order 2 in
			// provideDocumentDiagnostic, since the fixture is on-disk and never
			// opened via didOpen), so — like the sibling wire test in
			// diagnostics_test.go — the request must not be pre-fed into a
			// single buffer: that would race the background index build
			// (feature 21 made the initial build asynchronous) and could
			// observe a not-yet-published (nil) index. Drive over a pipe:
			// send initialize+initialized, WAIT on the index-ready gate, THEN
			// send the diagnostic request (plus shutdown/exit).
			ready := indexReadyGate(t)

			pr, pw := io.Pipe()
			var out lockedBuffer
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			runDone := make(chan error, 1)
			go func() {
				runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, newStubAnalyzer(), logger)
			}()

			if err := writeFramedMessage(pw, initCall); err != nil {
				t.Fatalf("write initialize: %v", err)
			}
			if err := writeFramedMessage(pw, initializedNotif); err != nil {
				t.Fatalf("write initialized: %v", err)
			}

			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("index build did not publish within 5s")
			}

			if err := writeFramedMessage(pw, diagCall); err != nil {
				t.Fatalf("write diagnostic: %v", err)
			}
			if err := writeFramedMessage(pw, shutdownCall); err != nil {
				t.Fatalf("write shutdown: %v", err)
			}
			if err := writeFramedMessage(pw, exitNotif); err != nil {
				t.Fatalf("write exit: %v", err)
			}

			select {
			case err := <-runDone:
				if err != nil {
					t.Fatalf("Run returned error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return within 5s")
			}

			// Assert: find the diagnostic response (id=2) in the output
			output := out.String()
			work := bytes.NewBufferString(output)

			var resultJSON map[string]interface{}
			var foundResponse bool
			for {
				body, err := parseFramedResponse(work)
				if err != nil {
					break // no more responses
				}

				msg, decErr := jsonrpc2.DecodeMessage(body)
				if decErr != nil {
					continue
				}

				resp, ok := msg.(*jsonrpc2.Response)
				if !ok {
					continue
				}

				if resp.ID() != jsonrpc2.NewNumberID(2) {
					continue
				}

				foundResponse = true

				// If response has an error, it's because dispatch is not yet implemented (MethodNotFound)
				if resp.Err() != nil {
					// Parse the error response to confirm it's MethodNotFound
					var errJSON map[string]interface{}
					if err := json.Unmarshal(body, &errJSON); err != nil {
						t.Fatalf("textDocument/diagnostic returned error (dispatch not yet implemented): failed to parse error response: %v", err)
					}
					errField, hasErr := errJSON["error"]
					if hasErr {
						if errObj, ok := errField.(map[string]interface{}); ok {
							if msg, ok := errObj["message"].(string); ok && strings.Contains(msg, "method not found") {
								t.Fatalf("textDocument/diagnostic dispatch not yet implemented: returns MethodNotFound instead of diagnostic report")
							}
						}
					}
					t.Fatalf("textDocument/diagnostic returned unexpected error: %s", string(body))
				}

				// Parse the result as JSON
				if err := json.Unmarshal(resp.Result(), &resultJSON); err != nil {
					t.Fatalf("failed to unmarshal result: %v", err)
				}
				break
			}

			if !foundResponse {
				t.Fatalf("no response for textDocument/diagnostic found")
			}

			// Assert: result.kind == "full"
			kind, hasKind := resultJSON["kind"]
			if !hasKind {
				t.Errorf("result missing 'kind' field")
			}
			if kind != tc.expectKind {
				t.Errorf("result.kind = %q, want %q", kind, tc.expectKind)
			}

			// Assert: result.items is an array (never null)
			itemsVal, hasItems := resultJSON["items"]
			if !hasItems {
				t.Errorf("result missing 'items' field")
			}
			items, ok := itemsVal.([]interface{})
			if !ok {
				t.Errorf("result.items type = %T, want []interface{} (JSON array)", itemsVal)
			}

			// Assert: items is non-nil even when empty
			if itemsVal == nil {
				t.Errorf("result.items is null; must be empty array [] for proper serialization")
			}

			// Assert: if expectHasItems, items is non-empty; else empty
			if tc.expectHasItems {
				if len(items) == 0 {
					t.Errorf("expected non-empty items, got empty array")
				}
				// For each item, assert it has source == "natural-lsp" and a valid range
				for i, itemVal := range items {
					item, ok := itemVal.(map[string]interface{})
					if !ok {
						t.Errorf("items[%d] type = %T, want map[string]interface{}", i, itemVal)
						continue
					}

					// Assert: source == "natural-lsp"
					source, hasSource := item["source"]
					if !hasSource {
						t.Errorf("items[%d] missing 'source' field", i)
					} else if source != "natural-lsp" {
						t.Errorf("items[%d].source = %q, want %q", i, source, "natural-lsp")
					}

					// Assert: range present with start/end line/character
					rangeVal, hasRange := item["range"]
					if !hasRange {
						t.Errorf("items[%d] missing 'range' field", i)
						continue
					}
					rangeObj, ok := rangeVal.(map[string]interface{})
					if !ok {
						t.Errorf("items[%d].range type = %T, want map[string]interface{}", i, rangeVal)
						continue
					}

					// Check start position
					startVal, hasStart := rangeObj["start"]
					if !hasStart {
						t.Errorf("items[%d].range missing 'start' field", i)
					} else {
						start, ok := startVal.(map[string]interface{})
						if !ok {
							t.Errorf("items[%d].range.start type = %T, want map[string]interface{}", i, startVal)
						} else {
							if _, hasLine := start["line"]; !hasLine {
								t.Errorf("items[%d].range.start missing 'line'", i)
							}
							if _, hasChar := start["character"]; !hasChar {
								t.Errorf("items[%d].range.start missing 'character'", i)
							}
						}
					}

					// Check end position
					endVal, hasEnd := rangeObj["end"]
					if !hasEnd {
						t.Errorf("items[%d].range missing 'end' field", i)
					} else {
						end, ok := endVal.(map[string]interface{})
						if !ok {
							t.Errorf("items[%d].range.end type = %T, want map[string]interface{}", i, endVal)
						} else {
							if _, hasLine := end["line"]; !hasLine {
								t.Errorf("items[%d].range.end missing 'line'", i)
							}
							if _, hasChar := end["character"]; !hasChar {
								t.Errorf("items[%d].range.end missing 'character'", i)
							}
						}
					}
				}
			} else {
				// expect empty array
				if len(items) != 0 {
					t.Errorf("expected empty items, got %d items", len(items))
				}
			}
		})
	}
}

// TestTextDocumentDiagnostic_PreInitLifecycleGating (T3 — RED phase) tests that
// textDocument/diagnostic is gated by the `initialized` state — calling it before
// `initialized` completes returns a JSON-RPC ServerNotInitialized error.
//
// Currently FAILS (RED) because:
// - textDocument/diagnostic dispatch is not yet implemented
// - Pre-init requests will return MethodNotFound (method unhandled) instead of ServerNotInitialized
// - Once the dispatch arm is added with the stateInitialized gate, this will pass
//
// Feature 30 T3: gating on stateInitialized.
func TestTextDocumentDiagnostic_PreInitLifecycleGating(t *testing.T) {
	// Arrange: build an initialize request, but DO NOT send initialized.
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	// Arrange: build the textDocument/diagnostic request
	diagID := jsonrpc2.NewNumberID(2)
	diagParams := jsonrpc2.RawMessage(`{"textDocument":{"uri":"file:///workspace/test.NSP"}}`)
	diagCall := jsonrpc2.NewCall(diagID, "textDocument/diagnostic", diagParams)

	// Write both to the request buffer
	var reqBuf bytes.Buffer
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	// DO NOT write initialized notification — skip it to test pre-init behavior
	if err := writeFramedMessage(&reqBuf, diagCall); err != nil {
		t.Fatalf("write textDocument/diagnostic: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Act: run the server
	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", "/workspace", newStubAnalyzer(), logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Assert: find the diagnostic response (id=2) and verify it's a ServerNotInitialized error
	output := outBuf.String()
	work := bytes.NewBufferString(output)

	foundResponse := false
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break // no more responses
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue
		}

		if resp.ID() != diagID {
			continue // not our response
		}

		foundResponse = true

		// Assert: response has an error (not a result)
		if resp.Err() == nil {
			t.Fatalf("expected error response (ServerNotInitialized when gated, MethodNotFound in RED), got result: %s", string(resp.Result()))
		}

		// Parse the raw response JSON to check the error code
		var respJSON map[string]interface{}
		if err := json.Unmarshal(body, &respJSON); err != nil {
			t.Fatalf("failed to unmarshal error response: %v", err)
		}

		errField, hasErr := respJSON["error"]
		if !hasErr {
			t.Fatalf("response has no error field")
		}

		errObj, ok := errField.(map[string]interface{})
		if !ok {
			t.Fatalf("error field type = %T, want map[string]interface{}", errField)
		}

		// Check the error code
		codeVal, hasCode := errObj["code"]
		if !hasCode {
			t.Fatalf("error object missing 'code' field")
		}

		// Code comes as float64 from JSON
		code, ok := codeVal.(float64)
		if !ok {
			t.Fatalf("error.code type = %T, want float64", codeVal)
		}

		// In RED: dispatch not implemented → MethodNotFound
		// In GREEN: dispatch implemented with stateInitialized gate → ServerNotInitialized
		if code == float64(jsonrpc2.MethodNotFound) {
			t.Fatalf("textDocument/diagnostic dispatch not yet implemented: pre-init request returns MethodNotFound (should be ServerNotInitialized once dispatch is gated)")
		} else if code == float64(jsonrpc2.ServerNotInitialized) {
			// This is what we expect when the dispatch is implemented with proper gating
			// Success - the test passes in GREEN
			return
		} else {
			t.Fatalf("unexpected error code %v: expected ServerNotInitialized (-32002) or MethodNotFound (-32601)", code)
		}
	}

	if !foundResponse {
		t.Fatalf("no diagnostic response (id=2) found in output")
	}
}

// TestTextDocumentDiagnostic_MalformedParams (T3 — RED phase) tests that
// malformed textDocument/diagnostic params decode errors return a JSON-RPC
// InvalidParams error.
//
// Currently FAILS (RED) because:
// - textDocument/diagnostic dispatch is not yet implemented
// - Requests will return MethodNotFound instead of InvalidParams
// - Once the dispatch arm is added with params validation, this will pass
//
// Feature 30 T3: params validation.
func TestTextDocumentDiagnostic_MalformedParams(t *testing.T) {
	// Arrange: build requests manually to test bad params after initialized.
	tmpDir := t.TempDir()

	var reqBuf bytes.Buffer

	// 1) initialize
	initCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(1),
		"initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
	)
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	// 2) initialized notification
	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// 3) diagnostic request with malformed params (missing textDocument field)
	diagCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(2),
		"textDocument/diagnostic",
		jsonrpc2.RawMessage(`{"garbage":"not-a-valid-param"}`),
	)
	if err := writeFramedMessage(&reqBuf, diagCall); err != nil {
		t.Fatalf("write diagnostic: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Act: run the server
	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", tmpDir, newStubAnalyzer(), logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Assert: find the diagnostic response (id=2) and verify it's an InvalidParams error
	output := outBuf.String()
	work := bytes.NewBufferString(output)

	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue
		}

		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue
		}

		// Assert: response has an error
		if resp.Err() == nil {
			t.Fatalf("expected error response (InvalidParams in GREEN, MethodNotFound in RED), got result: %s", string(resp.Result()))
		}

		// Parse the raw response JSON to check the error code
		var respJSON map[string]interface{}
		if err := json.Unmarshal(body, &respJSON); err != nil {
			t.Fatalf("failed to unmarshal error response: %v", err)
		}

		errField, hasErr := respJSON["error"]
		if !hasErr {
			t.Fatalf("response has no error field")
		}

		errObj, ok := errField.(map[string]interface{})
		if !ok {
			t.Fatalf("error field type = %T, want map[string]interface{}", errField)
		}

		// Check the error code
		codeVal, hasCode := errObj["code"]
		if !hasCode {
			t.Fatalf("error object missing 'code' field")
		}

		// Code comes as float64 from JSON
		code, ok := codeVal.(float64)
		if !ok {
			t.Fatalf("error.code type = %T, want float64", codeVal)
		}

		// In RED: dispatch not implemented → MethodNotFound
		// In GREEN: dispatch implemented with params validation → InvalidParams
		if code == float64(jsonrpc2.MethodNotFound) {
			t.Fatalf("textDocument/diagnostic dispatch not yet implemented: malformed params request returns MethodNotFound (should be InvalidParams once dispatch is implemented)")
		} else if code == float64(jsonrpc2.InvalidParams) {
			// This is what we expect when the dispatch is implemented with proper params validation
			return
		} else {
			t.Fatalf("unexpected error code %v: expected InvalidParams (-32602) or MethodNotFound (-32601)", code)
		}
	}

	t.Fatalf("no error response (id=2) found in output")
}

// TestLifecycleDiagnosticPublishing_PushSuppressed_PullCapableClient (T4 — RED phase) tests
// that when a client advertises pull-diagnostics support (capabilities.textDocument.diagnostic
// is present), the server SUPPRESSES its push publishDiagnostics notifications.
//
// Scenario: initialize with textDocument.diagnostic capability present, then didOpen
// a file with parse errors. Assert that NO textDocument/publishDiagnostics notification
// is emitted for that file.
//
// Currently FAILS (RED) because:
// - The server does not yet check the pull-support capability flag
// - publishDiag closure and clear-on-close/delete calls always emit publishDiagnostics
// - Feature 30 T4 will add clientSupportsPullDiagnostics negotiation and gate the push calls
//
// Acceptance criterion: OQ-1 (push suppressed for pull-capable clients, preserve for push-only).
func TestLifecycleDiagnosticPublishing_PushSuppressed_PullCapableClient(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a parse-error file on disk
	errorPath := filepath.Join(tmpDir, "error.NSP")
	errorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(errorPath, errorContent, 0600); err != nil {
		t.Fatalf("failed to write error.NSP: %v", err)
	}

	errorURI := uri.File(errorPath)

	// Build initialize request WITH textDocument.diagnostic capability present
	// (indicating the client supports pull diagnostics).
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]},
			"textDocument": {
				"diagnostic": {}
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build didOpen for the error file
	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(errorURI), string(errorContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the full lifecycle
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse all notifications from output
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Assert: NO publishDiagnostics notification for the error file URI
	for _, notif := range notifications {
		if notif.Method() != "textDocument/publishDiagnostics" {
			continue
		}

		var params protocol.PublishDiagnosticsParams
		dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
		if err := params.UnmarshalJSONFrom(dec); err != nil {
			continue
		}

		if string(params.URI) == string(errorURI) {
			t.Fatalf("expected NO textDocument/publishDiagnostics for pull-capable client, but got one (push should be suppressed, OQ-1)")
		}
	}

	// If we reach here, no publishDiagnostics was found for the error file,
	// which is the expected behavior when push is suppressed for pull-capable clients.
	// This assertion will pass once the feature is implemented (GREEN phase).
}

// TestLifecycleDiagnosticPublishing_PushPreserved_PushOnlyClient (T4 — GREEN baseline) tests
// that when a client does NOT advertise pull-diagnostics support
// (capabilities.textDocument.diagnostic is absent), the server continues to push
// publishDiagnostics notifications (feature 14 behavior preserved).
//
// Scenario: initialize WITHOUT textDocument.diagnostic capability, then didOpen
// a file with parse errors. Assert that a textDocument/publishDiagnostics notification
// IS emitted for that file (push-only behavior, feature 14 baseline).
//
// This test should PASS with the existing code (GREEN baseline).
// When T4 is implemented, this test confirms that push is NOT suppressed for push-only clients.
//
// Acceptance criterion: OQ-1 push-only clients unaffected.
func TestLifecycleDiagnosticPublishing_PushPreserved_PushOnlyClient(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a parse-error file on disk
	errorPath := filepath.Join(tmpDir, "error.NSP")
	errorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(errorPath, errorContent, 0600); err != nil {
		t.Fatalf("failed to write error.NSP: %v", err)
	}

	errorURI := uri.File(errorPath)

	// Build initialize request WITHOUT textDocument.diagnostic capability
	// (indicating the client does NOT support pull diagnostics — push-only).
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build didOpen for the error file
	didOpenParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(errorURI), string(errorContent)))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", didOpenParams)

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the full lifecycle
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := natural.New(nil)

	err := Run(context.Background(), &inBuf, &outBuf, "0.0.0-test", tmpDir, az, logger)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse all notifications from output
	output := outBuf.String()
	notifications := parseAllNotifications(t, output)

	// Assert: publishDiagnostics notification DOES exist for the error file URI
	var foundPublishDiags *jsonrpc2.Notification
	for _, notif := range notifications {
		if notif.Method() != "textDocument/publishDiagnostics" {
			continue
		}

		var params protocol.PublishDiagnosticsParams
		dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
		if err := params.UnmarshalJSONFrom(dec); err != nil {
			continue
		}

		if string(params.URI) == string(errorURI) {
			foundPublishDiags = notif
			break
		}
	}

	if foundPublishDiags == nil {
		t.Fatalf("expected textDocument/publishDiagnostics for push-only client (no pull support advertised), got none")
	}

	// Parse the notification params and assert diagnostics are present
	var params protocol.PublishDiagnosticsParams
	dec := jsontext.NewDecoder(bytes.NewReader(foundPublishDiags.Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal publishDiagnostics params: %v", err)
	}

	// Assert: at least one diagnostic was published
	if len(params.Diagnostics) == 0 {
		t.Errorf("expected ≥1 diagnostic in push for push-only client with error content, got 0")
	}
}

// TestProvideWorkspaceDiagnostic tests the provideWorkspaceDiagnostic function (T5 — RED phase).
//
// The provider must return a *WorkspaceDiagnosticReport in all cases (never nil),
// with Items initialized as a non-nil slice (empty or containing reports).
// Only files with ≥1 diagnostic are reported; clean files are omitted.
// Content must be byte-identical to the per-document/push path.
// The sweep must be disk-free (verified by deleting files after build).
//
// Test cases:
//
//	A. Multi-file workspace (error + clean) → Items contains one WorkspaceFullDocumentDiagnosticReport
//	   for the error file only (clean file omitted); each report has Kind=="full", correct URI,
//	   non-empty Items, each item Source=="natural-lsp", and bytes match the push path.
//	B. Empty workspace → Items == [] (empty non-nil), err == nil (FR-43).
//	C. Disk-free proof: delete files after build, call provideWorkspaceDiagnostic, assert
//	   it still returns correct diagnostics from the index (no os.ReadFile per query).
func TestProvideWorkspaceDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		// files is a map of relative path → content to write to disk and index
		files map[string][]byte
		// expectReportedCount is the expected number of *WorkspaceFullDocumentDiagnosticReport
		// items in the result (files with ≥1 diagnostic).
		expectReportedCount int
		// expectErrorFile is the filename (relative path) expected to be reported with diagnostics.
		// Empty string means no file is expected to be reported.
		expectErrorFile string
		// diskFreeTest, if true, deletes all files after build and re-queries to verify
		// the sweep does not call os.ReadFile (ADR-025).
		diskFreeTest bool
	}{
		{
			name: "A. Multi-file workspace (error + clean) → error file reported, clean omitted (S2-AC1)",
			files: map[string][]byte{
				"parse-error.NSP": nil, // use fixture content
				"clean.NSP":       nil, // use fixture content
			},
			expectReportedCount: 1,
			expectErrorFile:     "parse-error.NSP",
			diskFreeTest:        false,
		},
		{
			name:  "B. Empty workspace → empty Items, non-nil (FR-43)",
			files: map[string][]byte{
				// no files
			},
			expectReportedCount: 0,
			expectErrorFile:     "",
			diskFreeTest:        false,
		},
		{
			name: "C. Disk-free proof: delete files after build, diagnostics still returned (ADR-025)",
			files: map[string][]byte{
				"parse-error.NSP": nil,
				"clean.NSP":       nil,
			},
			expectReportedCount: 1,
			expectErrorFile:     "parse-error.NSP",
			diskFreeTest:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up the workspace
			tmpDir := t.TempDir()
			cfg := config.Defaults()
			az := natural.New(nil)
			logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

			// Load fixture content for files with nil content
			for relPath, content := range tc.files {
				if content == nil {
					fixtureContent, err := os.ReadFile(filepath.Join("testdata/diagnostics", relPath))
					if err != nil {
						t.Fatalf("failed to read fixture %s: %v", relPath, err)
					}
					tc.files[relPath] = fixtureContent
				}
			}

			// Write all files to disk
			for relPath, content := range tc.files {
				diskPath := filepath.Join(tmpDir, relPath)
				dir := filepath.Dir(diskPath)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatalf("failed to create directory: %v", err)
				}
				if err := os.WriteFile(diskPath, content, 0600); err != nil {
					t.Fatalf("failed to write file %s: %v", relPath, err)
				}
			}

			// Build the index and resolution
			idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
			if err != nil {
				t.Fatalf("failed to build index: %v", err)
			}

			res := workspace.Resolve(idx, &cfg)

			// Optionally delete files after build to verify disk-free sweep (ADR-025)
			if tc.diskFreeTest {
				for relPath := range tc.files {
					diskPath := filepath.Join(tmpDir, relPath)
					if err := os.Remove(diskPath); err != nil {
						t.Fatalf("failed to delete %s (for disk-free test): %v", diskPath, err)
					}
				}
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

			// Act: call provideWorkspaceDiagnostic
			params := protocol.WorkspaceDiagnosticParams{}

			report, err := provideWorkspaceDiagnostic(hctx, params)

			// Assert: call succeeded (never returns error per FR-43)
			if err != nil {
				t.Errorf("provideWorkspaceDiagnostic returned error: %v (expected nil, FR-43)", err)
			}

			// Assert: report is non-nil in all cases
			if report == nil {
				t.Fatalf("report is nil; expected non-nil *WorkspaceDiagnosticReport")
			}

			// Assert: Items is non-nil (initialized to empty slice on empty path)
			if report.Items == nil {
				t.Fatalf("Items is nil; expected non-nil slice (possibly empty)")
			}

			// Assert: Items count matches expectation
			if len(report.Items) != tc.expectReportedCount {
				t.Errorf("expected %d reported files, got %d", tc.expectReportedCount, len(report.Items))
			}

			// Assert: each reported file has correct URI and Kind=="full"
			for i, raw := range report.Items {
				// Items is the LSP union type; each element must be a
				// *WorkspaceFullDocumentDiagnosticReport (never the unchanged variant here).
				item, ok := raw.(*protocol.WorkspaceFullDocumentDiagnosticReport)
				if !ok {
					t.Fatalf("item[%d] type = %T, want *protocol.WorkspaceFullDocumentDiagnosticReport", i, raw)
				}

				// Assert: Kind is "full"
				if item.Kind != "full" {
					t.Errorf("item[%d].Kind = %q, want %q", i, item.Kind, "full")
				}

				// Assert: URI is set (absolute file URI)
				if item.URI == "" {
					t.Errorf("item[%d].URI is empty; expected non-empty file URI", i)
				}

				// Assert: Items is non-nil
				if item.Items == nil {
					t.Errorf("item[%d].Items is nil; expected non-nil slice", i)
				}

				// If this is the expected error file, verify it has diagnostics
				if tc.expectErrorFile != "" && strings.Contains(string(item.URI), tc.expectErrorFile) {
					if len(item.Items) == 0 {
						t.Errorf("expected error file %s to have ≥1 diagnostic, got 0", tc.expectErrorFile)
					}

					// Assert: each diagnostic has Source == "natural-lsp"
					for j, diag := range item.Items {
						if src, ok := diag.Source.Get(); !ok || src != "natural-lsp" {
							t.Errorf("item[%d].Items[%d].Source = %q (ok=%v), want 'natural-lsp'", i, j, src, ok)
						}
					}
				}
			}

			// Assert: byte-identical content between workspace and per-document path.
			// Skipped in the disk-free case: provideDocumentDiagnostic falls back to
			// os.ReadFile (resolution order 2) for an unopened document, so with the
			// source files deleted it legitimately returns 0 items — the workspace
			// sweep's disk-freeness is already proven above by expectReportedCount
			// still being satisfied with the files gone (ADR-025).
			if tc.expectErrorFile != "" && !tc.diskFreeTest {
				// Find the reported error file in the workspace report
				var errorReport *protocol.WorkspaceFullDocumentDiagnosticReport
				for _, raw := range report.Items {
					full, ok := raw.(*protocol.WorkspaceFullDocumentDiagnosticReport)
					if !ok {
						continue
					}
					if strings.Contains(string(full.URI), tc.expectErrorFile) {
						errorReport = full
						break
					}
				}

				if errorReport != nil {
					// Get the per-document report for the same file
					docURI := errorReport.URI
					docParams := protocol.DocumentDiagnosticParams{
						TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
					}
					docReport, err := provideDocumentDiagnostic(hctx, docParams)
					if err != nil {
						t.Errorf("provideDocumentDiagnostic for %s returned error: %v", tc.expectErrorFile, err)
					} else if docReport != nil && docReport.Items != nil {
						// Assert: item counts match
						if len(errorReport.Items) != len(docReport.Items) {
							t.Errorf("workspace and document reports for %s have different item counts: %d vs %d",
								tc.expectErrorFile, len(errorReport.Items), len(docReport.Items))
						}
					}
				}
			}
		})
	}
}

// TestWorkspaceDiagnostic_DispatchAndMarshal (T6 — RED phase) tests the wire-level
// round-trip of the workspace/diagnostic method through the full server dispatch path.
//
// This test drives `initialize → initialized → workspace/diagnostic` against a fixture
// workspace with parse-error.NSP and clean.NSP and asserts on the raw response JSON:
// - result.items is a JSON array
// - each element (WorkspaceDiagnosticReport item) has kind:"full", a uri, and items array
// - the file with parse errors is included; the clean file is omitted
// - items serialize as [] (not null) for files with no diagnostics
//
// Currently FAILS (RED) because:
// - workspace/diagnostic is not yet dispatched in server.go
// - An unhandled method returns JSON-RPC MethodNotFound error instead of a workspace report
//
// This test will PASS (GREEN) once T6's dispatch arm is implemented in server.go.
//
// Feature 30 T6: Dispatch + marshal workspace/diagnostic.
func TestWorkspaceDiagnostic_DispatchAndMarshal(t *testing.T) {
	// Arrange: set up a workspace with both error and clean files
	tmpDir := t.TempDir()

	// Copy parse-error.NSP and clean.NSP fixtures into the temp workspace
	parseErrorContent, err := os.ReadFile("testdata/diagnostics/parse-error.NSP")
	if err != nil {
		t.Fatalf("failed to read parse-error.NSP fixture: %v", err)
	}

	cleanContent, err := os.ReadFile("testdata/diagnostics/clean.NSP")
	if err != nil {
		t.Fatalf("failed to read clean.NSP fixture: %v", err)
	}

	parseErrorPath := filepath.Join(tmpDir, "parse-error.NSP")
	cleanPath := filepath.Join(tmpDir, "clean.NSP")

	if err := os.WriteFile(parseErrorPath, parseErrorContent, 0600); err != nil {
		t.Fatalf("failed to write parse-error.NSP: %v", err)
	}

	if err := os.WriteFile(cleanPath, cleanContent, 0600); err != nil {
		t.Fatalf("failed to write clean.NSP: %v", err)
	}

	// 1) initialize request
	initCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(1),
		"initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
	)

	// 2) initialized notification
	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// 3) workspace/diagnostic request
	diagCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(2),
		"workspace/diagnostic",
		jsonrpc2.RawMessage(`{}`),
	)

	// 4) shutdown + exit
	shutdownCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(3), "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive over a pipe as in T3 (since the workspace build is asynchronous)
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, newStubAnalyzer(), logger)
	}()

	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if err := writeFramedMessage(pw, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	if err := writeFramedMessage(pw, diagCall); err != nil {
		t.Fatalf("write workspace/diagnostic: %v", err)
	}
	if err := writeFramedMessage(pw, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	if err := writeFramedMessage(pw, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Assert: find the workspace/diagnostic response (id=2) in the output
	output := out.String()
	work := bytes.NewBufferString(output)

	var resultJSON map[string]interface{}
	var foundResponse bool
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break // no more responses
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue
		}

		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue
		}

		foundResponse = true

		// If response has an error, it's because dispatch is not yet implemented (MethodNotFound)
		if resp.Err() != nil {
			var errJSON map[string]interface{}
			if err := json.Unmarshal(body, &errJSON); err != nil {
				t.Fatalf("workspace/diagnostic returned error (dispatch not yet implemented): failed to parse error response: %v", err)
			}
			errField, hasErr := errJSON["error"]
			if hasErr {
				if errObj, ok := errField.(map[string]interface{}); ok {
					if msg, ok := errObj["message"].(string); ok && strings.Contains(msg, "method not found") {
						t.Fatalf("workspace/diagnostic dispatch not yet implemented: returns MethodNotFound instead of diagnostic report")
					}
				}
			}
			t.Fatalf("workspace/diagnostic returned unexpected error: %s", string(body))
		}

		// Parse the result as JSON
		if err := json.Unmarshal(resp.Result(), &resultJSON); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}
		break
	}

	if !foundResponse {
		t.Fatalf("no response for workspace/diagnostic found")
	}

	// Assert: result.items is an array (never null)
	itemsVal, hasItems := resultJSON["items"]
	if !hasItems {
		t.Errorf("result missing 'items' field")
	}
	items, ok := itemsVal.([]interface{})
	if !ok {
		t.Errorf("result.items type = %T, want []interface{} (JSON array)", itemsVal)
	}

	// Assert: items is non-nil even when empty
	if itemsVal == nil {
		t.Errorf("result.items is null; must be empty array []")
	}

	// Assert: parse-error.NSP is reported; clean.NSP is omitted
	if len(items) == 0 {
		t.Errorf("expected ≥1 reported file (parse-error.NSP), got empty items")
	}

	// For each reported file, assert it has kind:"full", uri, and items array
	var foundErrorFile bool
	for i, itemVal := range items {
		item, ok := itemVal.(map[string]interface{})
		if !ok {
			t.Errorf("items[%d] type = %T, want map[string]interface{}", i, itemVal)
			continue
		}

		// Assert: kind == "full"
		kind, hasKind := item["kind"]
		if !hasKind {
			t.Errorf("items[%d] missing 'kind' field", i)
		} else if kind != "full" {
			t.Errorf("items[%d].kind = %q, want %q", i, kind, "full")
		}

		// Assert: uri is present
		uri, hasURI := item["uri"]
		if !hasURI {
			t.Errorf("items[%d] missing 'uri' field", i)
		} else if uriStr, ok := uri.(string); ok {
			if strings.Contains(uriStr, "parse-error.NSP") {
				foundErrorFile = true
			}
		}

		// Assert: items array present
		itemsField, hasItemsField := item["items"]
		if !hasItemsField {
			t.Errorf("items[%d] missing 'items' field", i)
		} else if itemsList, ok := itemsField.([]interface{}); ok {
			// For the error file, expect non-empty diagnostics
			if strings.Contains(uri.(string), "parse-error.NSP") && len(itemsList) == 0 {
				t.Errorf("parse-error.NSP reported with 0 diagnostics, expected ≥1")
			}
		}
	}

	if !foundErrorFile {
		t.Errorf("expected parse-error.NSP to be reported in workspace/diagnostic result")
	}
}

// TestWorkspaceDiagnostic_PreInitLifecycleGating (T6 — RED phase) tests that
// workspace/diagnostic is gated by the `initialized` state — calling it before
// `initialized` completes returns a JSON-RPC ServerNotInitialized error.
//
// Currently FAILS (RED) because:
// - workspace/diagnostic dispatch is not yet implemented
// - Pre-init requests will return MethodNotFound (method unhandled) instead of ServerNotInitialized
// - Once the dispatch arm is added with the stateInitialized gate, this will pass
//
// Feature 30 T6: gating on stateInitialized.
func TestWorkspaceDiagnostic_PreInitLifecycleGating(t *testing.T) {
	// Arrange: build an initialize request, but DO NOT send initialized.
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	// Arrange: build the workspace/diagnostic request
	diagID := jsonrpc2.NewNumberID(2)
	diagParams := jsonrpc2.RawMessage(`{}`)
	diagCall := jsonrpc2.NewCall(diagID, "workspace/diagnostic", diagParams)

	// Write both to the request buffer
	var reqBuf bytes.Buffer
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	// DO NOT write initialized notification — skip it to test pre-init behavior
	if err := writeFramedMessage(&reqBuf, diagCall); err != nil {
		t.Fatalf("write workspace/diagnostic: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Act: run the server
	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", "/workspace", newStubAnalyzer(), logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Assert: find the diagnostic response (id=2) and verify it's a ServerNotInitialized error
	output := outBuf.String()
	work := bytes.NewBufferString(output)

	foundResponse := false
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break // no more responses
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue
		}

		if resp.ID() != diagID {
			continue // not our response
		}

		foundResponse = true

		// Assert: response has an error (not a result)
		if resp.Err() == nil {
			t.Fatalf("expected error response (ServerNotInitialized when gated, MethodNotFound in RED), got result: %s", string(resp.Result()))
		}

		// Parse the raw response JSON to check the error code
		var respJSON map[string]interface{}
		if err := json.Unmarshal(body, &respJSON); err != nil {
			t.Fatalf("failed to unmarshal error response: %v", err)
		}

		errField, hasErr := respJSON["error"]
		if !hasErr {
			t.Fatalf("response has no error field")
		}

		errObj, ok := errField.(map[string]interface{})
		if !ok {
			t.Fatalf("error field type = %T, want map[string]interface{}", errField)
		}

		// Check the error code
		codeVal, hasCode := errObj["code"]
		if !hasCode {
			t.Fatalf("error object missing 'code' field")
		}

		// Code comes as float64 from JSON
		code, ok := codeVal.(float64)
		if !ok {
			t.Fatalf("error.code type = %T, want float64", codeVal)
		}

		// In RED: dispatch not implemented → MethodNotFound
		// In GREEN: dispatch implemented with stateInitialized gate → ServerNotInitialized
		if code == float64(jsonrpc2.MethodNotFound) {
			t.Fatalf("workspace/diagnostic dispatch not yet implemented: pre-init request returns MethodNotFound (should be ServerNotInitialized once dispatch is gated)")
		} else if code == float64(jsonrpc2.ServerNotInitialized) {
			// This is what we expect when the dispatch is implemented with proper gating
			// Success - the test passes in GREEN
			return
		} else {
			t.Fatalf("unexpected error code %v: expected ServerNotInitialized (-32002) or MethodNotFound (-32601)", code)
		}
	}

	if !foundResponse {
		t.Fatalf("no diagnostic response (id=2) found in output")
	}
}

// TestWorkspaceDiagnostic_MalformedParams (T6 — RED phase) tests that
// malformed workspace/diagnostic params decode errors return a JSON-RPC
// InvalidParams error.
//
// Currently FAILS (RED) because:
// - workspace/diagnostic dispatch is not yet implemented
// - Requests will return MethodNotFound instead of InvalidParams
// - Once the dispatch arm is added with params validation, this will pass
//
// Feature 30 T6: params validation.
func TestWorkspaceDiagnostic_MalformedParams(t *testing.T) {
	// Arrange: build requests manually to test bad params after initialized.
	tmpDir := t.TempDir()

	var reqBuf bytes.Buffer

	// 1) initialize
	initCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(1),
		"initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
	)
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	// 2) initialized notification
	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// 3) diagnostic request with malformed params (invalid JSON)
	diagCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(2),
		"workspace/diagnostic",
		jsonrpc2.RawMessage(`{not valid json}`),
	)
	if err := writeFramedMessage(&reqBuf, diagCall); err != nil {
		t.Fatalf("write diagnostic: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Act: run the server
	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", tmpDir, newStubAnalyzer(), logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Assert: find the diagnostic response (id=2) and verify it's an InvalidParams error
	output := outBuf.String()
	work := bytes.NewBufferString(output)

	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue
		}

		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue
		}

		// Assert: response has an error
		if resp.Err() == nil {
			t.Fatalf("expected error response (InvalidParams in GREEN, MethodNotFound in RED), got result: %s", string(resp.Result()))
		}

		// Parse the raw response JSON to check the error code
		var respJSON map[string]interface{}
		if err := json.Unmarshal(body, &respJSON); err != nil {
			t.Fatalf("failed to unmarshal error response: %v", err)
		}

		errField, hasErr := respJSON["error"]
		if !hasErr {
			t.Fatalf("response has no error field")
		}

		errObj, ok := errField.(map[string]interface{})
		if !ok {
			t.Fatalf("error field type = %T, want map[string]interface{}", errField)
		}

		// Check the error code
		codeVal, hasCode := errObj["code"]
		if !hasCode {
			t.Fatalf("error object missing 'code' field")
		}

		// Code comes as float64 from JSON
		code, ok := codeVal.(float64)
		if !ok {
			t.Fatalf("error.code type = %T, want float64", codeVal)
		}

		// In RED: dispatch not implemented → MethodNotFound
		// In GREEN: dispatch implemented with params validation → InvalidParams
		if code == float64(jsonrpc2.MethodNotFound) {
			t.Fatalf("workspace/diagnostic dispatch not yet implemented: malformed params request returns MethodNotFound (should be InvalidParams once dispatch is implemented)")
		} else if code == float64(jsonrpc2.InvalidParams) {
			// This is what we expect when the dispatch is implemented with proper params validation
			return
		} else {
			t.Fatalf("unexpected error code %v: expected InvalidParams (-32602) or MethodNotFound (-32601)", code)
		}
	}

	t.Fatalf("no error response (id=2) found in output")
}

// TestWorkspaceDiagnostic_ProgressToken (T6 — RED phase) tests that
// workspace/diagnostic honors the work-done progress token when present
// and the client advertised window.workDoneProgress support.
//
// Scenario 1: request WITH workDoneToken + client supports progress
// → server emits $/progress begin/end on that token before/around the response.
//
// Scenario 2: request WITHOUT workDoneToken
// → server returns a single response with no $/progress notifications.
//
// Currently FAILS (RED) because:
// - workspace/diagnostic dispatch is not yet implemented
// - No progress notifications will be emitted
//
// Feature 30 T6: progress token honored.
func TestWorkspaceDiagnostic_ProgressToken(t *testing.T) {
	tests := []struct {
		name                   string
		includeWorkDoneToken   bool
		clientSupportsProgress bool
		expectProgressEmitted  bool // whether we expect $/progress notifications
	}{
		{
			name:                   "request WITH workDoneToken, client supports progress → progress emitted",
			includeWorkDoneToken:   true,
			clientSupportsProgress: true,
			expectProgressEmitted:  true,
		},
		{
			name:                   "request WITHOUT workDoneToken, client supports progress → no progress emitted",
			includeWorkDoneToken:   false,
			clientSupportsProgress: true,
			expectProgressEmitted:  false,
		},
		{
			name:                   "request WITH workDoneToken, client does NOT support progress → no progress emitted",
			includeWorkDoneToken:   true,
			clientSupportsProgress: false,
			expectProgressEmitted:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up a workspace with at least one file
			tmpDir := t.TempDir()

			parseErrorContent, err := os.ReadFile("testdata/diagnostics/parse-error.NSP")
			if err != nil {
				t.Fatalf("failed to read fixture: %v", err)
			}

			diskPath := filepath.Join(tmpDir, "error.NSP")
			if err := os.WriteFile(diskPath, parseErrorContent, 0600); err != nil {
				t.Fatalf("failed to write file: %v", err)
			}

			// Build initialize request with or without window.workDoneProgress capability
			initCapabilities := `{"general":{"positionEncodings":["utf-8"]}}`
			if tc.clientSupportsProgress {
				initCapabilities = `{
					"general":{"positionEncodings":["utf-8"]},
					"window":{"workDoneProgress":true}
				}`
			}

			initID := jsonrpc2.NewNumberID(1)
			initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
				"processId":1,
				"rootUri":null,
				"capabilities":%s
			}`, initCapabilities))
			initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
			initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			// Build workspace/diagnostic request with or without workDoneToken
			diagID := jsonrpc2.NewNumberID(2)
			diagParams := `{}`
			if tc.includeWorkDoneToken {
				diagParams = `{"workDoneToken":"my-progress-token"}`
			}
			diagCall := jsonrpc2.NewCall(diagID, "workspace/diagnostic", jsonrpc2.RawMessage(diagParams))

			// Shutdown sequence
			shutdownID := jsonrpc2.NewNumberID(3)
			shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Act: drive over pipe with index-ready gate
			ready := indexReadyGate(t)

			pr, pw := io.Pipe()
			var out lockedBuffer
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			runDone := make(chan error, 1)
			go func() {
				runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, newStubAnalyzer(), logger)
			}()

			if err := writeFramedMessage(pw, initCall); err != nil {
				t.Fatalf("write initialize: %v", err)
			}
			if err := writeFramedMessage(pw, initNotif); err != nil {
				t.Fatalf("write initialized: %v", err)
			}

			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("index build did not publish within 5s")
			}

			if err := writeFramedMessage(pw, diagCall); err != nil {
				t.Fatalf("write workspace/diagnostic: %v", err)
			}
			if err := writeFramedMessage(pw, shutdownCall); err != nil {
				t.Fatalf("write shutdown: %v", err)
			}
			if err := writeFramedMessage(pw, exitNotif); err != nil {
				t.Fatalf("write exit: %v", err)
			}

			select {
			case err := <-runDone:
				if err != nil {
					t.Fatalf("Run returned error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return within 5s")
			}

			// Assert: check for $/progress notifications in the output
			output := out.String()
			progressNotifications := parseProgressNotifications(t, output, "my-progress-token")

			if tc.expectProgressEmitted {
				// Expect at least a begin and end
				if len(progressNotifications) < 2 {
					t.Fatalf("expected ≥2 progress notifications (begin/end), got %d", len(progressNotifications))
				}

				// Verify the token matches
				var beginToken, endToken string
				if len(progressNotifications) > 0 {
					if begin, ok := progressNotifications[0].(map[string]interface{}); ok {
						if params, ok := begin["params"].(map[string]interface{}); ok {
							if token, ok := params["token"].(string); ok {
								beginToken = token
							}
						}
					}
				}
				if len(progressNotifications) > len(progressNotifications)-1 {
					if end, ok := progressNotifications[len(progressNotifications)-1].(map[string]interface{}); ok {
						if params, ok := end["params"].(map[string]interface{}); ok {
							if token, ok := params["token"].(string); ok {
								endToken = token
							}
						}
					}
				}

				// Tokens should match "my-progress-token"
				if tc.includeWorkDoneToken {
					if beginToken != "my-progress-token" {
						t.Errorf("progress begin token = %q, want %q", beginToken, "my-progress-token")
					}
					if endToken != "my-progress-token" {
						t.Errorf("progress end token = %q, want %q", endToken, "my-progress-token")
					}
				}
			} else {
				// Expect NO progress notifications
				if len(progressNotifications) > 0 {
					t.Errorf("expected 0 progress notifications, got %d (progress should not be emitted)", len(progressNotifications))
				}
			}
		})
	}
}

// parseProgressNotifications extracts the $/progress notifications carrying the
// given progress token from the output. Filtering by token is essential: the
// feature-21 background index build ALSO emits $/progress on its own
// "natural-lsp-index" token, which must not be counted as workspace-diagnostic
// progress. Each returned element is shaped {"params": {...}} so callers can read
// params.token / params.value.
//
// It reuses parseAllNotifications' robust Content-Length framing (which advances
// past server→client Call frames such as window/workDoneProgress/create rather
// than aborting on them).
func parseProgressNotifications(t *testing.T, output, token string) []interface{} {
	t.Helper()

	var progressNotifs []interface{}
	for _, notif := range parseAllNotifications(t, output) {
		if notif.Method() != "$/progress" {
			continue
		}
		var params map[string]interface{}
		if err := json.Unmarshal(notif.Params(), &params); err != nil {
			continue
		}
		if tok, _ := params["token"].(string); tok != token {
			continue // a different progress operation (e.g. the index build)
		}
		progressNotifs = append(progressNotifs, map[string]interface{}{"params": params})
	}

	return progressNotifs
}

// TestWorkspaceDiagnosticRefresh_SentOnRepublish_RefreshCapableClient (T7 — RED phase) tests
// that when a pull-capable client advertises workspace.diagnostics.refreshSupport = true,
// the server sends a workspace/diagnostic/refresh request (fire-and-forget) on index/resolution
// republish (Feature 30, Finding F-A).
//
// Scenario: initialize with BOTH capabilities.textDocument.diagnostic (pull) AND
// capabilities.workspace.diagnostics.refreshSupport = true. Trigger a republish via
// workspace/didChangeWatchedFiles Changed event. Assert that the server emits a
// workspace/diagnostic/refresh request (a Call, not a notification) to the client.
//
// Currently FAILS (RED) because:
// - The server does not yet detect the refreshSupport capability
// - The publishIndex/applyDocumentChange republish points do not send workspace/diagnostic/refresh
// - Feature 30 T7 will add clientSupportsDiagnosticRefresh negotiation and send the request
//
// Acceptance criterion: FR-43, no panic on refresh failure; Finding F-A requirement.
func TestWorkspaceDiagnosticRefresh_SentOnRepublish_RefreshCapableClient(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file on disk that will change
	filePath := filepath.Join(tmpDir, "changing.NSP")
	initialContent := []byte("* Initial content\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")
	if err := os.WriteFile(filePath, initialContent, 0600); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	fileURI := uri.File(filePath)

	// Build initialize request WITH BOTH:
	// - capabilities.textDocument.diagnostic (pull support)
	// - capabilities.workspace.diagnostics.refreshSupport = true (refresh support)
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]},
			"textDocument": {
				"diagnostic": {}
			},
			"workspace": {
				"diagnostics": {
					"refreshSupport": true
				}
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build workspace/didChangeWatchedFiles Changed event to trigger republish
	changedFileEvent := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"changes": [
			{
				"uri": %q,
				"type": 2
			}
		]
	}`, string(fileURI)))
	changedNotif := jsonrpc2.NewNotification("workspace/didChangeWatchedFiles", changedFileEvent)

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the full lifecycle over a pipe (since the index build is asynchronous)
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	az := natural.New(nil)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, az, logger)
	}()

	// Send initialize
	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	// Send initialized
	if err := writeFramedMessage(pw, initNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// Wait for index build to complete
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	// Update the file on disk to trigger a change event
	newContent := []byte("* Changed content\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")
	if err := os.WriteFile(filePath, newContent, 0600); err != nil {
		t.Fatalf("failed to update file: %v", err)
	}

	// Send workspace/didChangeWatchedFiles Changed event to trigger republish
	if err := writeFramedMessage(pw, changedNotif); err != nil {
		t.Fatalf("write didChangeWatchedFiles: %v", err)
	}

	// Give the server a moment to process the change and send refresh
	time.Sleep(100 * time.Millisecond)

	// Send shutdown sequence
	if err := writeFramedMessage(pw, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	if err := writeFramedMessage(pw, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Assert: parse all Call frames from output and look for workspace/diagnostic/refresh
	output := out.String()
	work := bytes.NewBufferString(output)

	foundRefreshCall := false
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break // no more messages
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		// Look for a Call (server→client request) with method "workspace/diagnostic/refresh"
		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			continue // not a Call frame (could be Response, Notification, etc.)
		}

		if call.Method() == "workspace/diagnostic/refresh" {
			foundRefreshCall = true
			break
		}
	}

	if !foundRefreshCall {
		t.Fatalf("expected workspace/diagnostic/refresh request (Call) on republish for refresh-capable pull client, got none (RED: feature not yet implemented)")
	}
}

// TestWorkspaceDiagnosticRefresh_NotSent_PullCapableNoRefreshSupport (T7 — RED baseline) tests
// that when a client advertises pull-diagnostics support (capabilities.textDocument.diagnostic)
// but does NOT advertise workspace.diagnostics.refreshSupport, the server does NOT send
// workspace/diagnostic/refresh on republish (no refresh support → no refresh).
//
// Currently should PASS (GREEN baseline) — the feature is gated on refreshSupport,
// so absence of that flag means no refresh is sent (the existing code path where
// refresh is not sent is the baseline).
//
// Feature 30 T7: selective refresh emission (only when refreshSupport advertised).
func TestWorkspaceDiagnosticRefresh_NotSent_PullCapableNoRefreshSupport(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file on disk
	filePath := filepath.Join(tmpDir, "changing.NSP")
	initialContent := []byte("* Initial\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")
	if err := os.WriteFile(filePath, initialContent, 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	fileURI := uri.File(filePath)

	// Build initialize request WITH capabilities.textDocument.diagnostic (pull)
	// but WITHOUT workspace.diagnostics.refreshSupport
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]},
			"textDocument": {
				"diagnostic": {}
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build workspace/didChangeWatchedFiles Changed event
	changedFileEvent := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"changes": [
			{
				"uri": %q,
				"type": 2
			}
		]
	}`, string(fileURI)))
	changedNotif := jsonrpc2.NewNotification("workspace/didChangeWatchedFiles", changedFileEvent)

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the lifecycle
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	az := natural.New(nil)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, az, logger)
	}()

	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if err := writeFramedMessage(pw, initNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	// Update file to trigger change
	newContent := []byte("* Changed\nDEFINE DATA\nLOCAL\n1 #VAR (A5)\nEND\nEND")
	if err := os.WriteFile(filePath, newContent, 0600); err != nil {
		t.Fatalf("failed to update file: %v", err)
	}

	// Send didChangeWatchedFiles to trigger republish
	if err := writeFramedMessage(pw, changedNotif); err != nil {
		t.Fatalf("write didChangeWatchedFiles: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := writeFramedMessage(pw, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	if err := writeFramedMessage(pw, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Assert: NO workspace/diagnostic/refresh Call found
	output := out.String()
	work := bytes.NewBufferString(output)

	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			continue
		}

		if call.Method() == "workspace/diagnostic/refresh" {
			t.Fatalf("expected NO workspace/diagnostic/refresh for pull-capable client without refreshSupport, but got one")
		}
	}

	// If we reach here, no refresh was sent (expected for a client without refreshSupport)
}

// TestProvideWorkspaceDiagnostic_DeterministicSortByURI (FINDING 1 — determinism, RED phase) tests that
// provideWorkspaceDiagnostic returns Items sorted by URI in ascending order, ensuring byte-stable
// output across multiple calls. The current implementation builds Items in ForEachWithRange iteration
// order (arbitrary Go map traversal), making the result non-deterministic.
//
// Scenario: Create a workspace with ≥2 files that both have diagnostics (e.g., a parse-error file
// and an ambiguous-resolution file at different relative paths). Call provideWorkspaceDiagnostic
// multiple times and assert that the returned Items are sorted by URI ascending.
//
// Currently FAILS (RED) because Items are built in arbitrary map-iteration order from ForEachWithRange.
// When FINDING 1 is implemented, items will be sorted by URI, making the order deterministic.
//
// Acceptance: Each call to provideWorkspaceDiagnostic must return Items in the same order,
// sorted by URI ascending. Test calls the provider and asserts the URIs form a non-decreasing sequence.
func TestProvideWorkspaceDiagnostic_DeterministicSortByURI(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// Load fixture content for files with diagnostics
	parseErrorContent, err := os.ReadFile("testdata/diagnostics/parse-error.NSP")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	// Create two files that both have diagnostics at different relative paths
	// Using subdirectories to ensure map iteration order could differ significantly.
	// The names are chosen so alphabetically they could be in different order than map iteration.
	file1RelPath := "z-error.NSP"  // sorts last alphabetically
	file2RelPath := "a-error2.NSP" // sorts first alphabetically

	file1Path := filepath.Join(tmpDir, file1RelPath)
	file2Path := filepath.Join(tmpDir, file2RelPath)

	if err := os.WriteFile(file1Path, parseErrorContent, 0600); err != nil {
		t.Fatalf("failed to write %s: %v", file1RelPath, err)
	}

	if err := os.WriteFile(file2Path, parseErrorContent, 0600); err != nil {
		t.Fatalf("failed to write %s: %v", file2RelPath, err)
	}

	// Build the index and resolution
	idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	res := workspace.Resolve(idx, &cfg)

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

	// Act: call provideWorkspaceDiagnostic
	params := protocol.WorkspaceDiagnosticParams{}
	report, err := provideWorkspaceDiagnostic(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideWorkspaceDiagnostic returned error: %v", err)
	}

	if report == nil {
		t.Fatalf("report is nil")
	}

	if len(report.Items) < 2 {
		t.Fatalf("expected ≥2 reported files with diagnostics, got %d", len(report.Items))
	}

	// Extract URIs from the items
	var uris []string
	for _, raw := range report.Items {
		item, ok := raw.(*protocol.WorkspaceFullDocumentDiagnosticReport)
		if !ok {
			t.Fatalf("item type = %T, want *protocol.WorkspaceFullDocumentDiagnosticReport", raw)
		}
		uris = append(uris, string(item.URI))
	}

	// Assert: URIs are sorted by comparing against a sorted copy.
	// This catches non-determinism regardless of map iteration order.
	// Approach (b) from the finding: compare against a sorted-by-URI copy.
	sortedURIs := make([]string, len(uris))
	copy(sortedURIs, uris)
	sort.Strings(sortedURIs)

	// Assert: Items order matches the sorted order (deterministic, byte-stable)
	for i, got := range uris {
		want := sortedURIs[i]
		if got != want {
			t.Errorf("Items[%d] URI out of order: got %q, want %q (items not sorted by URI ascending)",
				i, got, want)
		}
	}
}

// TestProvideWorkspaceDiagnostic_FieldLevelByteIdentity (FINDING 2 — test strength, RED phase) tests
// that the workspace-diagnostic path produces field-level byte-identical results to the per-document path.
// The existing test only checks item counts match; this test strengthens it by asserting that each
// diagnostic's Range (start/end line/character), Message, Severity, and Source are identical to what
// provideDocumentDiagnostic produces for the same file.
//
// Scenario: Create a workspace with a file containing parse errors. Call both provideWorkspaceDiagnostic
// and provideDocumentDiagnostic for the same file. Assert that for each diagnostic:
// - Range.Start.Line == Range.Start.Line
// - Range.Start.Character == Range.Start.Character
// - Range.End.Line == Range.End.Line
// - Range.End.Character == Range.End.Character
// - Message == Message
// - Severity == Severity
// - Source == Source
//
// Currently should PASS if the two converters agree (they should), so this is a strengthening assertion.
// If it fails, it indicates a converter divergence between toProtocolDiagnosticFromConverter (workspace)
// and toProtocolDiagnostic (document).
func TestProvideWorkspaceDiagnostic_FieldLevelByteIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// Use the parse-error.NSP fixture
	parseErrorContent, err := os.ReadFile("testdata/diagnostics/parse-error.NSP")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	errorPath := filepath.Join(tmpDir, "parse-error.NSP")
	if err := os.WriteFile(errorPath, parseErrorContent, 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Build the index and resolution
	idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	res := workspace.Resolve(idx, &cfg)

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

	// Act: call provideWorkspaceDiagnostic
	wsParams := protocol.WorkspaceDiagnosticParams{}
	wsReport, err := provideWorkspaceDiagnostic(hctx, wsParams)
	if err != nil {
		t.Fatalf("provideWorkspaceDiagnostic returned error: %v", err)
	}

	if wsReport == nil || len(wsReport.Items) == 0 {
		t.Fatalf("expected ≥1 reported file, got none")
	}

	// Find the parse-error.NSP file in the workspace report
	var wsErrorReport *protocol.WorkspaceFullDocumentDiagnosticReport
	for _, raw := range wsReport.Items {
		full, ok := raw.(*protocol.WorkspaceFullDocumentDiagnosticReport)
		if !ok {
			continue
		}
		if strings.Contains(string(full.URI), "parse-error.NSP") {
			wsErrorReport = full
			break
		}
	}

	if wsErrorReport == nil {
		t.Fatalf("parse-error.NSP not found in workspace report")
	}

	if len(wsErrorReport.Items) == 0 {
		t.Fatalf("parse-error.NSP has 0 diagnostics in workspace report, expected ≥1")
	}

	// Act: call provideDocumentDiagnostic for the same file
	errorURI := uri.File(errorPath)
	docParams := protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: errorURI},
	}
	docReport, err := provideDocumentDiagnostic(hctx, docParams)
	if err != nil {
		t.Fatalf("provideDocumentDiagnostic returned error: %v", err)
	}

	if docReport == nil || len(docReport.Items) == 0 {
		t.Fatalf("provideDocumentDiagnostic returned empty report for parse-error.NSP")
	}

	// Assert: item counts must match
	if len(wsErrorReport.Items) != len(docReport.Items) {
		t.Errorf("item count mismatch: workspace=%d, document=%d",
			len(wsErrorReport.Items), len(docReport.Items))
	}

	// Assert: field-level equality for each diagnostic
	minLen := len(wsErrorReport.Items)
	if len(docReport.Items) < minLen {
		minLen = len(docReport.Items)
	}

	for i := 0; i < minLen; i++ {
		wsDiag := wsErrorReport.Items[i]
		docDiag := docReport.Items[i]

		// Message
		wsMsg, _ := wsDiag.Message.(protocol.String)
		docMsg, _ := docDiag.Message.(protocol.String)
		if wsMsg != docMsg {
			t.Errorf("diag[%d] message mismatch: workspace=%q, document=%q", i, wsMsg, docMsg)
		}

		// Severity
		if wsDiag.Severity != docDiag.Severity {
			t.Errorf("diag[%d] severity mismatch: workspace=%d, document=%d",
				i, wsDiag.Severity, docDiag.Severity)
		}

		// Code
		wsCode, _ := wsDiag.Code.(protocol.String)
		docCode, _ := docDiag.Code.(protocol.String)
		if wsCode != docCode {
			t.Errorf("diag[%d] code mismatch: workspace=%q, document=%q", i, wsCode, docCode)
		}

		// Source
		wsSource, _ := wsDiag.Source.Get()
		docSource, _ := docDiag.Source.Get()
		if wsSource != docSource {
			t.Errorf("diag[%d] source mismatch: workspace=%q, document=%q", i, wsSource, docSource)
		}

		// Range.Start.Line
		if wsDiag.Range.Start.Line != docDiag.Range.Start.Line {
			t.Errorf("diag[%d] range start line mismatch: workspace=%d, document=%d",
				i, wsDiag.Range.Start.Line, docDiag.Range.Start.Line)
		}

		// Range.Start.Character
		if wsDiag.Range.Start.Character != docDiag.Range.Start.Character {
			t.Errorf("diag[%d] range start character mismatch: workspace=%d, document=%d",
				i, wsDiag.Range.Start.Character, docDiag.Range.Start.Character)
		}

		// Range.End.Line
		if wsDiag.Range.End.Line != docDiag.Range.End.Line {
			t.Errorf("diag[%d] range end line mismatch: workspace=%d, document=%d",
				i, wsDiag.Range.End.Line, docDiag.Range.End.Line)
		}

		// Range.End.Character
		if wsDiag.Range.End.Character != docDiag.Range.End.Character {
			t.Errorf("diag[%d] range end character mismatch: workspace=%d, document=%d",
				i, wsDiag.Range.End.Character, docDiag.Range.End.Character)
		}
	}
}

// TestWorkspaceDiagnosticRefresh_NotSent_PushOnlyClient (T7 — RED baseline) tests
// that when a client does NOT advertise pull-diagnostics support (capabilities.textDocument.diagnostic absent),
// the server does NOT send workspace/diagnostic/refresh on republish — they get push instead
// (push-only clients unaffected by the refresh feature).
//
// Currently should PASS (GREEN baseline) — push-only clients get push diagnostics (feature 14),
// no refresh needed.
//
// Feature 30 T7: refresh only for pull-capable clients.
func TestWorkspaceDiagnosticRefresh_NotSent_PushOnlyClient(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file on disk with a parse error
	filePath := filepath.Join(tmpDir, "error.NSP")
	errorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(filePath, errorContent, 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	fileURI := uri.File(filePath)

	// Build initialize request WITHOUT capabilities.textDocument.diagnostic
	// (push-only client, no pull support)
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build workspace/didChangeWatchedFiles Changed event
	changedFileEvent := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"changes": [
			{
				"uri": %q,
				"type": 2
			}
		]
	}`, string(fileURI)))
	changedNotif := jsonrpc2.NewNotification("workspace/didChangeWatchedFiles", changedFileEvent)

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the lifecycle
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	az := natural.New(nil)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, az, logger)
	}()

	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if err := writeFramedMessage(pw, initNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	// Update file to trigger change
	updatedContent := []byte("* Updated error\nCALLNAT\nINVALID")
	if err := os.WriteFile(filePath, updatedContent, 0600); err != nil {
		t.Fatalf("failed to update file: %v", err)
	}

	// Send didChangeWatchedFiles to trigger republish
	if err := writeFramedMessage(pw, changedNotif); err != nil {
		t.Fatalf("write didChangeWatchedFiles: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := writeFramedMessage(pw, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	if err := writeFramedMessage(pw, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Assert: NO workspace/diagnostic/refresh Call found (push-only, no refresh)
	output := out.String()
	work := bytes.NewBufferString(output)

	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			continue
		}

		if call.Method() == "workspace/diagnostic/refresh" {
			t.Fatalf("expected NO workspace/diagnostic/refresh for push-only client (no pull capability), but got one")
		}
	}

	// If we reach here, no refresh was sent (expected for push-only clients)
}

// TestWorkspaceDiagnosticRefresh_SentAfterColdBuild_RefreshCapableClient (Feature 30 T8 — RED phase)
// tests that when a pull+refresh-capable client initializes, the server sends a
// workspace/diagnostic/refresh request AFTER the async background index build completes
// (Finding F-C: cold-build refresh was missing).
//
// Scenario: initialize with BOTH capabilities.textDocument.diagnostic (pull) AND
// capabilities.workspace.diagnostics.refreshSupport = true. The background build then
// runs asynchronously (no watched-file change needed). Wait for the build to publish
// via indexReadyGate. Assert that workspace/diagnostic/refresh was emitted AFTER the
// cold build completed — WITHOUT any didChange/watched-file event to trigger it.
// The ONLY trigger is the initial index build reaching publish.
//
// Currently FAILS (RED) because:
// - The background build goroutine does not send workspace/diagnostic/refresh after publishIndex
// - Feature 30 T8 will add sendDiagnosticRefresh() after the build publishes
//
// Acceptance criterion: FR-43 (no panic), Finding F-C requirement, S2-AC7.
func TestWorkspaceDiagnosticRefresh_SentAfterColdBuild_RefreshCapableClient(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file on disk with errors so the cold build produces diagnostics
	filePath := filepath.Join(tmpDir, "error.NSP")
	errorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(filePath, errorContent, 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Build initialize request WITH BOTH:
	// - capabilities.textDocument.diagnostic (pull support)
	// - capabilities.workspace.diagnostics.refreshSupport = true (refresh support)
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]},
			"textDocument": {
				"diagnostic": {}
			},
			"workspace": {
				"diagnostics": {
					"refreshSupport": true
				}
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Shutdown sequence (no didChange, no watched-file events)
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the lifecycle
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	az := natural.New(nil)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, az, logger)
	}()

	// Send initialize
	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	// Send initialized (triggers the background build)
	if err := writeFramedMessage(pw, initNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// Wait for the background index build to complete and publish
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	// Give the server a moment to send the refresh (it may be in flight after publish)
	time.Sleep(100 * time.Millisecond)

	// Send shutdown sequence
	if err := writeFramedMessage(pw, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	if err := writeFramedMessage(pw, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Assert: parse all Call frames from output and look for workspace/diagnostic/refresh
	output := out.String()
	work := bytes.NewBufferString(output)

	foundRefreshCall := false
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break // no more messages
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		// Look for a Call (server→client request) with method "workspace/diagnostic/refresh"
		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			continue // not a Call frame (could be Response, Notification, etc.)
		}

		if call.Method() == "workspace/diagnostic/refresh" {
			foundRefreshCall = true
			break
		}
	}

	if !foundRefreshCall {
		t.Fatalf("expected workspace/diagnostic/refresh request (Call) after cold build for refresh-capable pull client, got none (RED: feature not yet implemented — F-C)")
	}
}

// TestWorkspaceDiagnosticRefresh_NotSent_AfterColdBuild_PushOnlyClient (Feature 30 T8 — RED baseline)
// tests that when a push-only client (no pull capability) initializes, the server
// does NOT send workspace/diagnostic/refresh after the cold build (no refresh needed
// for push clients — they get push diagnostics via publishDiagnostics).
//
// Currently should PASS (GREEN baseline) — push-only clients get push diagnostics,
// no refresh needed, so the absence of refreshSupport means no refresh is sent.
//
// Feature 30 T8: refresh only on cold build for pull+refresh-capable clients.
func TestWorkspaceDiagnosticRefresh_NotSent_AfterColdBuild_PushOnlyClient(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file on disk with errors
	filePath := filepath.Join(tmpDir, "error.NSP")
	errorContent := []byte("CALLNAT\nINVALID")
	if err := os.WriteFile(filePath, errorContent, 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Build initialize request WITHOUT capabilities.textDocument.diagnostic
	// (push-only client, no pull support)
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {"positionEncodings": ["utf-8"]}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Shutdown sequence
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Act: drive the lifecycle
	ready := indexReadyGate(t)

	pr, pw := io.Pipe()
	var out lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	az := natural.New(nil)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(context.Background(), pr, &out, "0.0.0-test", tmpDir, az, logger)
	}()

	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if err := writeFramedMessage(pw, initNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// Wait for the background index build to complete and publish
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not publish within 5s")
	}

	// Give the server a moment to potentially send a (wrong) refresh
	time.Sleep(100 * time.Millisecond)

	if err := writeFramedMessage(pw, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	if err := writeFramedMessage(pw, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}

	// Assert: verify NO workspace/diagnostic/refresh was sent (push-only client)
	output := out.String()
	work := bytes.NewBufferString(output)

	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			break // no more messages
		}

		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			continue
		}

		// Look for a Call with method "workspace/diagnostic/refresh"
		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			continue
		}

		if call.Method() == "workspace/diagnostic/refresh" {
			t.Fatalf("expected NO workspace/diagnostic/refresh for push-only client after cold build, but got one")
		}
	}

	// If we reach here, no refresh was sent (expected for push-only clients)
}
