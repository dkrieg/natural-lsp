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
	"testing"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestProvideSemanticTokensFull_LexicalFixture tests the textDocument/semanticTokens/full
// handler end-to-end (feature 29, T6 — RED phase).
//
// Behavior: Opens the lexical.NSP fixture as a document in the store, issues a
// textDocument/semanticTokens/full request, and asserts the DESIRED behavior:
// - No error response (resp.Err() == nil)
// - SemanticTokens.Data is non-nil, non-empty, and len(Data) % 5 == 0
// - First token (at Data[3]) is correctly classified as keyword (type index 0)
//
// The test exercises:
// 1. Store-first pattern (open buffer takes precedence)
// 2. F7 snapshot pattern (idxResMu RLock/RUnlock before I/O)
// 3. Wire bytes marshaled via marshalResult (json/v2 gojson.Marshal)
//
// RED failure reason: Handler dispatch does not yet exist, so response is MethodNotFound error.
// GREEN will implement the handler to pass these assertions.
func TestProvideSemanticTokensFull_LexicalFixture(t *testing.T) {
	// Arrange: set up a temp workspace
	root := t.TempDir()

	// Write the lexical fixture to disk
	fixtureContent := `* Full-line comment
DEFINE DATA LOCAL
  1 #COUNT (N5)
END-DEFINE.

/* Rest-of-line comment
CALLNAT 'HELLO'
MOVE 42 TO #COUNT.
MOVE 'WORLD' TO #STR.
#X := #Y + 1.
`
	fixturePath := filepath.Join(root, "lexical.NSP")
	if err := os.WriteFile(fixturePath, []byte(fixtureContent), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Build the fixture URI using uri.File
	fixtureURI := uri.File(fixturePath)

	// Prepare the request: textDocument/semanticTokens/full
	paramsJSON := fmt.Sprintf(`{"textDocument":{"uri":"%s"}}`, fixtureURI)

	// Build the full lifecycle: initialize → initialized → didOpen → semanticTokens/full
	var reqBuf bytes.Buffer

	// 1) initialize (UTF-8 encoding for deterministic result)
	initCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(1),
		"initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
	)
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	// 2) initialized notification (triggers async index build)
	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// 3) textDocument/didOpen to populate the store with the open buffer
	didOpenParams := fmt.Sprintf(
		`{"textDocument":{"uri":"%s","languageId":"natural","version":1,"text":%s}}`,
		fixtureURI,
		quoteStringForJSON(fixtureContent),
	)
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParams))
	if err := writeFramedMessage(&reqBuf, didOpenNotif); err != nil {
		t.Fatalf("write didOpen: %v", err)
	}

	// 4) the semantic tokens request under test
	tokensCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/semanticTokens/full", jsonrpc2.RawMessage(paramsJSON))
	if err := writeFramedMessage(&reqBuf, tokensCall); err != nil {
		t.Fatalf("write semanticTokens/full: %v", err)
	}

	// Act: run the server
	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := newStubAnalyzer()

	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", root, az, logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Assert: extract and validate the response for id=2
	work := bytes.NewBufferString(outBuf.String())
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			t.Fatalf("parse response: %v", err)
		}

		msg, err := jsonrpc2.DecodeMessage(body)
		if err != nil {
			continue // Skip unparseable
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue // Skip non-responses (notifications)
		}

		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue // Skip other IDs
		}

		// Found the response for our request!
		// Assert: No error (handler must be implemented)
		if resp.Err() != nil {
			t.Fatalf("textDocument/semanticTokens/full returned error: %s (handler not implemented)", resp.Err().Error())
		}

		// Assert: Result is a valid SemanticTokens
		resultBytes := []byte(resp.Result())
		var result protocol.SemanticTokens
		if err := result.UnmarshalJSONFrom(jsontext.NewDecoder(bytes.NewReader(resultBytes))); err != nil {
			t.Fatalf("failed to unmarshal SemanticTokens response: %v (bytes: %s)", err, string(resultBytes))
		}

		// Assert: Data is non-nil and non-empty
		if result.Data == nil {
			t.Fatal("SemanticTokens.Data is nil; expected non-nil slice with lexical tokens")
		}
		if len(result.Data) == 0 {
			t.Fatal("SemanticTokens.Data is empty; expected lexical tokens for the fixture")
		}

		// Assert: Data length is a multiple of 5 (LSP semantic token stream invariant)
		if len(result.Data)%5 != 0 {
			t.Fatalf("SemanticTokens.Data length %d is not a multiple of 5", len(result.Data))
		}

		// Assert: the first emitted token (at Data[3]) is a comment (type index 1).
		// The fixture's first source line is "* Full-line comment", so the first
		// Phase-A token is a comment, not a keyword.
		firstTokenTypeIndex := result.Data[3] // 5-int stream: deltaLine, deltaStartChar, length, typeIndex, modifiers
		commentIndex := uint32(1)             // "comment" is index 1 in semanticTokenTypesLegend
		if firstTokenTypeIndex != commentIndex {
			t.Fatalf("first token type index %d; expected comment (index %d)", firstTokenTypeIndex, commentIndex)
		}

		t.Logf("PASS: SemanticTokens result: %d entries (%d uint32s)", len(result.Data)/5, len(result.Data))
		return
	}
}

// TestProvideSemanticTokensFull_OutOfRootReturnsEmpty tests that a request for an
// unopened or out-of-root URI returns an empty SemanticTokens result ({"data":[]})
// rather than an error (FR-43 graceful degradation).
//
// Behavior: Issues textDocument/semanticTokens/full for a URI outside the workspace root
// (unopened, unindexed), and asserts the DESIRED behavior:
// - No error response (resp.Err() == nil)
// - SemanticTokens.Data is non-nil but empty (len(Data) == 0)
//
// RED failure reason: Handler dispatch does not yet exist, so response is MethodNotFound error.
// GREEN will implement the handler to return empty Data for out-of-root URIs (FR-43).
func TestProvideSemanticTokensFull_OutOfRootReturnsEmpty(t *testing.T) {
	root := t.TempDir()

	// Build a URI that is outside the root (e.g., /nonexistent/file.NSP)
	outOfRootURI := "file:///nonexistent/file.NSP"

	paramsJSON := fmt.Sprintf(`{"textDocument":{"uri":"%s"}}`, outOfRootURI)

	var reqBuf bytes.Buffer
	initCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(1),
		"initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
	)
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// Issue the request WITHOUT opening the document in the store
	tokensCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/semanticTokens/full", jsonrpc2.RawMessage(paramsJSON))
	if err := writeFramedMessage(&reqBuf, tokensCall); err != nil {
		t.Fatalf("write semanticTokens/full: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := newStubAnalyzer()

	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", root, az, logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Extract and validate the response for id=2
	work := bytes.NewBufferString(outBuf.String())
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			t.Fatalf("parse response: %v", err)
		}

		msg, err := jsonrpc2.DecodeMessage(body)
		if err != nil {
			continue // Skip unparseable
		}

		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue // Skip non-responses
		}

		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue // Skip other IDs
		}

		// Found the response!
		// Assert: No error (handler must be implemented and handle out-of-root gracefully)
		if resp.Err() != nil {
			t.Fatalf("textDocument/semanticTokens/full returned error: %s (handler not implemented)", resp.Err().Error())
		}

		// Assert: Result is a valid SemanticTokens with empty data
		resultBytes := []byte(resp.Result())
		var result protocol.SemanticTokens
		if err := result.UnmarshalJSONFrom(jsontext.NewDecoder(bytes.NewReader(resultBytes))); err != nil {
			t.Fatalf("failed to unmarshal SemanticTokens response: %v (bytes: %s)", err, string(resultBytes))
		}

		// Assert: Data is non-nil but empty (graceful degradation for out-of-root, FR-43)
		if result.Data == nil {
			t.Fatal("SemanticTokens.Data is nil; expected non-nil empty slice for out-of-root")
		}
		if len(result.Data) != 0 {
			t.Fatalf("SemanticTokens.Data length %d; expected empty (0) for out-of-root", len(result.Data))
		}

		t.Logf("PASS: SemanticTokens empty result for out-of-root URI (FR-43)")
		return
	}
}

// Helper: quoteStringForJSON escapes a string for JSON embedding
func quoteStringForJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
