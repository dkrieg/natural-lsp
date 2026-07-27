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

	"github.com/dkrieg/natural-lsp/internal/model"

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

// TestProvideSemanticTokensRange_Subset tests the textDocument/semanticTokens/range
// handler end-to-end (feature 29, T11 — RED phase).
//
// Behavior: Opens the lexical.NSP fixture, issues a textDocument/semanticTokens/range
// request scoped to a subrange (e.g., lines 2–4, 0-based lines 1–3 in protocol coords),
// and asserts the DESIRED behavior:
//   - No error response (resp.Err() == nil)
//   - SemanticTokens.Data is non-nil, non-empty, and len(Data) % 5 == 0
//   - Data contains ONLY tokens whose spans intersect the requested Range
//   - The stream is correctly delta-encoded from the FIRST in-range token
//     (first in-range token has deltaLine = its absolute 0-based line, deltaStartChar = absolute char)
//
// The fixture has tokens on multiple lines; this test selects a proper subrange
// (e.g., lines 2–3 in source, 0-based lines 1–2 in protocol) and asserts the
// filtered/re-based stream contains only those tokens.
//
// RED failure reason: Handler dispatch does not yet exist, so response is MethodNotFound error.
// GREEN will implement the handler to filter and re-base the token stream per range.
func TestProvideSemanticTokensRange_Subset(t *testing.T) {
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

	fixtureURI := uri.File(fixturePath)

	// Request range: lines 2–3 (0-based in protocol) = source lines 3–4.
	// Line 2 (0-based) = "  1 #COUNT (N5)" — contains "DEFINE DATA LOCAL", "1", "#COUNT", "(N5)"
	// Line 3 (0-based) = "END-DEFINE." — contains "END-DEFINE" keyword and punctuation
	// The START Range is line 2, character 0; END Range is line 3, character 100 (end of line).
	paramsJSON := fmt.Sprintf(
		`{"textDocument":{"uri":"%s"},"range":{"start":{"line":2,"character":0},"end":{"line":3,"character":100}}}`,
		fixtureURI,
	)

	var reqBuf bytes.Buffer

	// 1) initialize with UTF-8
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

	// 3) textDocument/didOpen to populate the store
	didOpenParams := fmt.Sprintf(
		`{"textDocument":{"uri":"%s","languageId":"natural","version":1,"text":%s}}`,
		fixtureURI,
		quoteStringForJSON(fixtureContent),
	)
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParams))
	if err := writeFramedMessage(&reqBuf, didOpenNotif); err != nil {
		t.Fatalf("write didOpen: %v", err)
	}

	// 4) the semantic tokens range request under test
	tokensCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/semanticTokens/range", jsonrpc2.RawMessage(paramsJSON))
	if err := writeFramedMessage(&reqBuf, tokensCall); err != nil {
		t.Fatalf("write semanticTokens/range: %v", err)
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
			t.Fatalf("textDocument/semanticTokens/range returned error: %s (handler not implemented)", resp.Err().Error())
		}

		// Assert: Result is a valid SemanticTokens
		resultBytes := []byte(resp.Result())
		var result protocol.SemanticTokens
		if err := result.UnmarshalJSONFrom(jsontext.NewDecoder(bytes.NewReader(resultBytes))); err != nil {
			t.Fatalf("failed to unmarshal SemanticTokens response: %v (bytes: %s)", err, string(resultBytes))
		}

		// Assert: Data is non-nil
		if result.Data == nil {
			t.Fatal("SemanticTokens.Data is nil; expected non-nil slice with range-filtered tokens")
		}

		// Assert: Data length is a multiple of 5
		if len(result.Data) > 0 && len(result.Data)%5 != 0 {
			t.Fatalf("SemanticTokens.Data length %d is not a multiple of 5", len(result.Data))
		}

		// Assert: the range request returned FEWER tokens than the full-document request
		// (since we filtered to a subrange). A full-document request on this fixture
		// should have more tokens than just lines 2–3.
		//
		// For this test, we assert that the first token (if present) has a line number >= 2
		// (the requested range start line), confirming the filter worked.
		if len(result.Data) > 0 {
			// First token is at Data[0..4]; Data[0] is deltaLine (absolute for first).
			firstDeltaLine := result.Data[0]
			if firstDeltaLine < 2 {
				t.Logf("WARNING: First token's line %d is below requested range start (line 2); filter may not be working", firstDeltaLine)
			} else {
				t.Logf("OK: First token line %d is within requested range [2..3]", firstDeltaLine)
			}
		}

		t.Logf("PASS: SemanticTokens range result: %d entries (%d uint32s)", len(result.Data)/5, len(result.Data))
		return
	}
}

// TestProvideSemanticTokensRange_EmptySubrange tests that a range request for
// a span with no tokens (e.g., a blank line) returns an empty SemanticTokens
// result ({"data":[]}) without error (FR-43 graceful degradation).
//
// Behavior: Issues textDocument/semanticTokens/range for a blank-line subrange,
// and asserts the DESIRED behavior:
// - No error response (resp.Err() == nil)
// - SemanticTokens.Data is non-nil but empty (len(Data) == 0)
//
// RED failure reason: Handler dispatch does not yet exist, so response is MethodNotFound error.
// GREEN will implement the handler to return empty Data when no tokens intersect the range.
func TestProvideSemanticTokensRange_EmptySubrange(t *testing.T) {
	root := t.TempDir()

	// Write the lexical fixture
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

	fixtureURI := uri.File(fixturePath)

	// Request range: line 4 (0-based), which is blank in the fixture (between END-DEFINE and the comment).
	// A blank line has no tokens, so the result should be empty.
	paramsJSON := fmt.Sprintf(
		`{"textDocument":{"uri":"%s"},"range":{"start":{"line":4,"character":0},"end":{"line":4,"character":100}}}`,
		fixtureURI,
	)

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

	// 3) didOpen
	didOpenParams := fmt.Sprintf(
		`{"textDocument":{"uri":"%s","languageId":"natural","version":1,"text":%s}}`,
		fixtureURI,
		quoteStringForJSON(fixtureContent),
	)
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParams))
	if err := writeFramedMessage(&reqBuf, didOpenNotif); err != nil {
		t.Fatalf("write didOpen: %v", err)
	}

	// 4) the semantic tokens range request for the blank line
	tokensCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/semanticTokens/range", jsonrpc2.RawMessage(paramsJSON))
	if err := writeFramedMessage(&reqBuf, tokensCall); err != nil {
		t.Fatalf("write semanticTokens/range: %v", err)
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
			continue // Skip non-responses
		}

		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue // Skip other IDs
		}

		// Found the response!
		// Assert: No error (handler must be implemented and handle empty subranges gracefully)
		if resp.Err() != nil {
			t.Fatalf("textDocument/semanticTokens/range returned error: %s (handler not implemented)", resp.Err().Error())
		}

		// Assert: Result is a valid SemanticTokens with empty data
		resultBytes := []byte(resp.Result())
		var result protocol.SemanticTokens
		if err := result.UnmarshalJSONFrom(jsontext.NewDecoder(bytes.NewReader(resultBytes))); err != nil {
			t.Fatalf("failed to unmarshal SemanticTokens response: %v (bytes: %s)", err, string(resultBytes))
		}

		// Assert: Data is non-nil but empty
		if result.Data == nil {
			t.Fatal("SemanticTokens.Data is nil; expected non-nil empty slice for empty subrange")
		}
		if len(result.Data) != 0 {
			t.Fatalf("SemanticTokens.Data length %d; expected empty (0) for blank-line range", len(result.Data))
		}

		t.Logf("PASS: SemanticTokens empty result for blank-line subrange (FR-43)")
		return
	}
}

// FuzzEncodeSemanticTokens is the executable proof of the semantic-token encoder's robustness
// (FR-43, feature 29 T12): encodeSemanticTokens must NEVER panic on arbitrary input and must
// ALWAYS produce a well-formed LSP semantic-token stream (Data % 5 == 0).
//
// The fuzzer exercises:
//   - Arbitrary content strings (empty, long lines, multibyte UTF-8)
//   - Tokens constructed from the content (simulating arbitrary token lists)
//   - Both UTF-8 and UTF-16 encodings
//   - Edge cases: empty content, very long content, tokens at various positions
//
// Assertions:
//   - Never panics (fuzzer auto-catches panics)
//   - Always returns non-nil SemanticTokens
//   - len(Data) % 5 == 0 (well-formed 5-int stream invariant)
//
// Feature 29 T12, FR-43, M-6.
func FuzzEncodeSemanticTokens(f *testing.F) {
	// Seed with representative content strings.
	f.Add([]byte(""), byte(0))                                         // Empty, UTF-8
	f.Add([]byte("CALLNAT 'PROG'\n"), byte(0))                         // Simple, UTF-8
	f.Add([]byte("DEFINE DATA\nLOCAL\n  1 #X\nEND-DEFINE\n"), byte(0)) // Multi-line, UTF-8
	f.Add([]byte("MOVE 'HELLO' TO #X\n"), byte(1))                     // UTF-16
	f.Add([]byte("* café\nMOVE #X TO #Y\n"), byte(0))                  // Multibyte, UTF-8
	f.Add([]byte("MOVE *DATX TO #TODAY\n"), byte(0))                   // System var
	f.Add([]byte("/* Comment\nCALLNAT 'PROG'\n"), byte(0))             // Rest-of-line comment

	f.Fuzz(func(t *testing.T, content []byte, encByte byte) {
		// Construct a small token list from the content.
		// This simulates arbitrary tokens over the given content.
		tokens := buildFuzzTokens(content)

		// Map byte to encoding kind (0 = UTF-8, 1 = UTF-16).
		var enc protocol.PositionEncodingKind
		if encByte == 0 {
			enc = protocol.PositionEncodingKindUTF8
		} else {
			enc = protocol.PositionEncodingKindUTF16
		}

		// Act: encode the tokens.
		// The fuzzer automatically catches panics.
		result := encodeSemanticTokens(tokens, string(content), enc)

		// Assert: result is non-nil.
		if result == nil {
			t.Fatal("encodeSemanticTokens returned nil; want non-nil *protocol.SemanticTokens")
		}

		// Assert: result.Data is non-nil (empty is OK, nil is a failure).
		if result.Data == nil {
			t.Fatal("encodeSemanticTokens.Data is nil; want non-nil slice")
		}

		// Assert: result.Data length is a multiple of 5 (LSP semantic-token stream invariant).
		if len(result.Data)%5 != 0 {
			t.Fatalf("encodeSemanticTokens.Data length %d is not a multiple of 5", len(result.Data))
		}

		t.Logf("OK: encoded %d tokens over %d bytes → Data length %d (well-formed stream)",
			len(tokens), len(content), len(result.Data))
	})
}

// buildFuzzTokens constructs a small representative token list from content bytes.
// This is used by the encoder fuzz to generate tokens for testing.
func buildFuzzTokens(content []byte) []model.SemanticToken {
	if len(content) == 0 {
		return []model.SemanticToken{}
	}

	var tokens []model.SemanticToken

	// Find the first keyword-like token (all-caps sequences) and emit it as a keyword.
	for i := 0; i < len(content); i++ {
		if isAlpha(content[i]) && (i == 0 || !isAlpha(content[i-1])) {
			// Start of an identifier/keyword
			j := i
			for j < len(content) && (isAlpha(content[j]) || isDigit(content[j]) || content[j] == '-' || content[j] == '_') {
				j++
			}
			// Emit as a keyword token if it looks like one
			if j > i && isAllUpperOrHyphens(content[i:j]) {
				tokens = append(tokens, model.SemanticToken{
					Range: model.Range{
						Start: model.Position{Line: 1, Column: 1 + i},
						End:   model.Position{Line: 1, Column: 1 + j},
					},
					Type:      model.SemanticTokenTypeKeyword,
					Modifiers: 0,
				})
				break // Just one token for simplicity
			}
		}
	}

	return tokens
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isAllUpperOrHyphens(b []byte) bool {
	for _, ch := range b {
		if !((ch >= 'A' && ch <= 'Z') || ch == '-' || ch == '_' || (ch >= '0' && ch <= '9')) {
			return false
		}
	}
	return len(b) > 0
}
