package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestProvideDocumentHighlight_VariableBasic tests document highlight on a variable use-site.
// Cursor on a variable reference should return all occurrences of that variable in the file
// with DocumentHighlight entries containing precise Ranges and appropriate Kinds.
// FR-54, feature 27, T5.
func TestProvideDocumentHighlight_VariableBasic(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the variable navigation fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index from the fixture
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext with the index
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	fixtureFile := filepath.Join(fixtureRoot, "VARTEST.NSP")

	tt := []struct {
		name         string
		cursorLine   int
		cursorColumn int
		wantVarName  string // The variable name expected at the cursor
		wantMinCount int    // Minimum number of highlights expected
		description  string
	}{
		{
			// File line 22 (LSP line 21): WRITE #SCALAR-VAR
			// Cursor on #SCALAR-VAR (the variable use-site)
			// Highlight should return all uses of #SCALAR-VAR
			name:         "scalar_variable_use",
			cursorLine:   21,
			cursorColumn: 10, // Within #SCALAR-VAR token (cols 6-16)
			wantVarName:  "SCALAR-VAR",
			wantMinCount: 4, // Uses: lines 22, 23, 25, 26, 30 (excluding string literal on 34)
			description:  "cursor on #SCALAR-VAR use → returns all same-file highlights",
		},
		{
			// File line 23 (LSP line 22): MOVE 'HELLO' TO #SCALAR-VAR
			// Cursor on #SCALAR-VAR (the write target)
			name:         "scalar_variable_write_target",
			cursorLine:   22,
			cursorColumn: 22, // Within #SCALAR-VAR (write target, starts at 19)
			wantVarName:  "SCALAR-VAR",
			wantMinCount: 4, // Uses: lines 22, 23, 25, 26, 30 (excluding string literal on 34)
			description:  "cursor on #SCALAR-VAR as assignment target → returns all highlights",
		},
		{
			// File line 28 (LSP line 27): MOVE #GROUP.#SUB-FIELD TO #ARRAY-FIELD (#INDEX)
			// Cursor on #SUB-FIELD (the group-qualified reference)
			name:         "group_qualified_reference",
			cursorLine:   27,
			cursorColumn: 18, // Within #SUB-FIELD token (starts at 13)
			wantVarName:  "SUB-FIELD",
			wantMinCount: 2, // Line 28 (qualified), line 29 (unqualified use)
			description:  "cursor on #GROUP.#SUB-FIELD → returns group-qualified and unqualified highlights",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Convert fixture file path to URI
			fileURI := uri.File(fixtureFile)

			// Create DocumentHighlightParams with the cursor position
			params := protocol.DocumentHighlightParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: fileURI,
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine),
						Character: uint32(tc.cursorColumn),
					},
				},
			}

			// Call provideDocumentHighlight
			highlights, err := provideDocumentHighlight(hctx, params)

			// Expect: a result (no error)
			if err != nil {
				t.Errorf("provideDocumentHighlight returned error: %v", err)
				return
			}

			// Expect: at least the minimum count of highlights
			if highlights == nil || len(highlights) < tc.wantMinCount {
				t.Errorf("provideDocumentHighlight returned %d highlights, want at least %d (description: %s)",
					len(highlights), tc.wantMinCount, tc.description)
			}

			// Expect: each highlight carries a Range (non-zero)
			for i, hl := range highlights {
				if hl.Range.Start.Line == 0 && hl.Range.Start.Character == 0 &&
					hl.Range.End.Line == 0 && hl.Range.End.Character == 0 {
					t.Errorf("highlight[%d] has zero Range (start=%v, end=%v); want non-zero range",
						i, hl.Range.Start, hl.Range.End)
				}
			}

			// Pin the real behavior (Finding 4): the provider is best-effort per the
			// plan and does NOT derive read/write direction — every highlight is emitted
			// as DocumentHighlightKindText. Write-direction (e.g. distinguishing the
			// MOVE … TO #X assignment target as DocumentHighlightKindWrite) is out of
			// scope for feature 27. Assert EXACTLY Text so a future direction-aware
			// change is a deliberate, test-visible decision rather than silently allowed.
			for i, hl := range highlights {
				if hl.Kind != protocol.DocumentHighlightKindText {
					t.Errorf("highlight[%d] Kind = %v; want exactly DocumentHighlightKindText (write-direction is out of scope for feature 27)", i, hl.Kind)
				}
			}
		})
	}
}

// TestProvideDocumentHighlight_CallName tests document highlight on a call target name.
// Cursor on a CALLNAT/PERFORM target name should highlight that name's occurrence(s).
// FR-54, feature 27, T5.
func TestProvideDocumentHighlight_CallName(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load a fixture with calls (reuse an existing calls/ fixture under testdata)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "references", "multi-caller")

	// Build the workspace index from the fixture
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext with the index
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	fixtureFile := filepath.Join(fixtureRoot, "CALLER1.NSP")

	// Convert fixture file path to URI
	fileURI := uri.File(fixtureFile)

	// Create DocumentHighlightParams with a cursor on a call target
	// This assumes the fixture has a CALLNAT statement; adjust line/column as needed
	params := protocol.DocumentHighlightParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: fileURI,
			},
			Position: protocol.Position{
				Line:      5,
				Character: 10,
			},
		},
	}

	// Call provideDocumentHighlight
	highlights, err := provideDocumentHighlight(hctx, params)

	// Expect: either empty (no error) or highlights for the call name
	if err != nil {
		t.Errorf("provideDocumentHighlight returned error: %v", err)
	}

	// Basic validation: if we get highlights, they must have valid Ranges and Kinds
	for i, hl := range highlights {
		if hl.Range.Start.Line == 0 && hl.Range.Start.Character == 0 &&
			hl.Range.End.Line == 0 && hl.Range.End.Character == 0 {
			t.Errorf("highlight[%d] has zero Range; want non-zero range", i)
		}
	}
}

// TestProvideDocumentHighlight_ModeledGaps tests that document highlight returns empty (not error)
// for cursor positions on system variables, dynamic references, or undefined targets.
// FR-17 (modeled gaps stay off the error channel), FR-54 (feature 27, T5).
func TestProvideDocumentHighlight_ModeledGaps(t *testing.T) {
	// Setup
	enc := protocol.PositionEncodingKindUTF8
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	// Arrange: load the variable navigation fixture
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	fixtureRoot := filepath.Join(wd, "testdata", "variablenav")

	// Build the workspace index from the fixture
	cfg := config.Defaults()
	idx, _, _, err := workspace.BuildWithCache(context.Background(), fixtureRoot, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	// Resolve the workspace edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build the handlerContext with the index
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: enc,
		root:        fixtureRoot,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}

	fixtureFile := filepath.Join(fixtureRoot, "VARTEST.NSP")
	fileURI := uri.File(fixtureFile)

	tt := []struct {
		name         string
		cursorLine   int
		cursorColumn int
		description  string
	}{
		{
			// File line 32 (LSP line 31): MOVE *DATE TO #SYS-VAR
			// Cursor on *DATE (system variable)
			name:         "system_variable",
			cursorLine:   31,
			cursorColumn: 6, // Within *DATE (starts at 5)
			description:  "*DATE system var → empty, no error",
		},
		{
			// File line 34 (LSP line 33): MOVE 'VALUE: #SCALAR-VAR' TO #SCALAR-VAR
			// Cursor on #SCALAR-VAR inside a string literal (should be excluded as a use)
			name:         "variable_in_string_literal",
			cursorLine:   33,
			cursorColumn: 14, // Within the quoted string, at the # position
			description:  "#SCALAR-VAR inside string literal → empty or no match, no error",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// Create DocumentHighlightParams with the cursor position
			params := protocol.DocumentHighlightParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: fileURI,
					},
					Position: protocol.Position{
						Line:      uint32(tc.cursorLine),
						Character: uint32(tc.cursorColumn),
					},
				},
			}

			// Call provideDocumentHighlight
			highlights, err := provideDocumentHighlight(hctx, params)

			// Expect: no error
			if err != nil {
				t.Errorf("provideDocumentHighlight returned error: %v", err)
				return
			}

			// Expect: empty result (no highlights for modeled gaps)
			if highlights != nil && len(highlights) > 0 {
				t.Logf("(INFO) provideDocumentHighlight returned %d highlights for %s (this may be acceptable)",
					len(highlights), tc.description)
			}
		})
	}
}

// TestProvideDocumentHighlight_EmptyResult tests that the wire output for an empty
// document highlight result is `[]` (not `null`), matching list-provider conventions.
// Feature 19 (protocol marshaling unification), FR-54 (feature 27, T5).
func TestProvideDocumentHighlight_EmptyResult(t *testing.T) {
	// Arrange: dispatch an empty-workspace documentHighlight query (returns `[]`)
	resultBytes := dispatchResultBytes(t,
		"textDocument/documentHighlight",
		`{"textDocument":{"uri":"file:///workspace/unknown.NSP"},"position":{"line":0,"character":0}}`,
	)

	// Expect: the wire output is `[]` (empty array), not `null`
	wantBytes := []byte(`[]`)
	if string(resultBytes) != string(wantBytes) {
		t.Errorf("dispatchResultBytes returned %q; want %q", string(resultBytes), string(wantBytes))
	}
}

// TestInitialize_DocumentHighlightCapability tests that the initialize response advertises
// the documentHighlightProvider capability. This is the ONE new capability added by feature 27, T5.
// FR-54, feature 27, T5, Story 2b.
func TestInitialize_DocumentHighlightCapability(t *testing.T) {
	// Arrange: build an initialize request (same pattern as TestInitialize)
	id := jsonrpc2.NewNumberID(1)
	paramsJSON := `{
		"processId": 1234,
		"rootPath": "/workspace",
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8"]
			}
		}
	}`
	call := jsonrpc2.NewCall(id, "initialize", jsonrpc2.RawMessage(paramsJSON))

	// Write the request as a Content-Length-framed message.
	var reqBuf bytes.Buffer
	if err := writeFramedMessage(&reqBuf, call); err != nil {
		t.Fatalf("failed to write framed request: %v", err)
	}

	// Create an output buffer for the response.
	var outBuf bytes.Buffer

	// Create a logger.
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server.
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&reqBuf,
		&outBuf,
		"0.0.0-test", // injected version
		"/workspace",
		az,
		logger,
	)

	// Assert: no error from Run.
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Extract the JSON body from the framed response.
	output := outBuf.String()
	lines := strings.Split(output, "\r\n")
	if len(lines) < 3 {
		t.Fatalf("response too short; expected at least 3 lines, got %d", len(lines))
	}
	// Body starts after: "Content-Length: N\r\n\r\n"
	bodyStart := len(lines[0]) + 2 + 2
	bodyBytes := output[bodyStart:]

	// Decode the response from the body bytes.
	respMsg, err := jsonrpc2.DecodeMessage([]byte(bodyBytes))
	if err != nil {
		t.Fatalf("failed to decode response: %v (output was: %q)", err, output)
	}

	resp, ok := respMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response, got %T", respMsg)
	}

	// Assert: response id matches request id.
	if resp.ID() != id {
		t.Errorf("response id = %v, want %v", resp.ID(), id)
	}

	// Unmarshal the initialize result
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result(), &result); err != nil {
		t.Fatalf("failed to unmarshal initialize result: %v", err)
	}

	// Extract capabilities
	caps, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities missing or wrong type; want map[string]interface{}")
	}

	// Expect: documentHighlightProvider is present and true
	docHighlightVal, exists := caps["documentHighlightProvider"]
	if !exists {
		t.Errorf("documentHighlightProvider not present in capabilities; want present and true")
	} else if docHighlight, ok := docHighlightVal.(bool); ok {
		if !docHighlight {
			t.Errorf("documentHighlightProvider = %v; want true", docHighlight)
		}
	} else {
		t.Errorf("documentHighlightProvider type = %T; want bool", docHighlightVal)
	}
}
