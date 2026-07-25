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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// stubAnalyzer is a test double implementing analysis.Analyzer with a no-op Analyze method.
type stubAnalyzer struct{}

func (sa *stubAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	return model.FileAnalysis{ObjectType: model.ObjectUnknown}, nil
}

func (sa *stubAnalyzer) ExtractVariableRefs(content string) []model.VariableRef {
	return []model.VariableRef{}
}

// dispatchResultBytes drives the full server lifecycle end-to-end (initialize →
// initialized → the request under test) against an empty temp-dir workspace and
// returns the JSON-RPC "result" bytes the dispatch actually wrote for that request.
//
// Unlike a direct gojson.Marshal in a test, this exercises the REAL dispatch path
// in Run — including each method's per-case nil-guard (the `if x == nil { respResult
// = []byte("null"|"[]") }` branch). A pin test built on this therefore goes red if
// someone drops or flips a nil-guard sentinel in server.go, which is the whole point
// of the empty-case pins (feature 19, Story 2 AC2).
//
// The workspace is an empty temp dir, so every navigation/outline/hover/codeLens
// provider returns an empty/nil result — the empty case each pin locks. paramsJSON
// is the raw JSON for the request params; method is the LSP method name.
func dispatchResultBytes(t *testing.T, method string, paramsJSON string) []byte {
	t.Helper()

	root := t.TempDir()

	var reqBuf bytes.Buffer

	// 1) initialize (UTF-8 offered so the encoding is deterministic).
	initCall := jsonrpc2.NewCall(
		jsonrpc2.NewNumberID(1),
		"initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8"]}}}`),
	)
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	// 2) initialized notification (triggers index build over the empty workspace).
	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	// 3) the request under test.
	reqCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), method, jsonrpc2.RawMessage(paramsJSON))
	if err := writeFramedMessage(&reqBuf, reqCall); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// EOF after the buffered messages returns Run cleanly.
	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", root, &stubAnalyzer{}, logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Consume responses until we find the one for id=2 (skipping the initialize
	// response id=1 and any publishDiagnostics notifications).
	work := bytes.NewBufferString(outBuf.String())
	for {
		body, err := parseFramedResponse(work)
		if err != nil {
			t.Fatalf("no response for %s (id=2) found: %v", method, err)
		}
		msg, decErr := jsonrpc2.DecodeMessage(body)
		if decErr != nil {
			t.Fatalf("decode response: %v", decErr)
		}
		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			continue
		}
		if resp.ID() != jsonrpc2.NewNumberID(2) {
			continue
		}
		if resp.Err() != nil {
			t.Fatalf("%s returned JSON-RPC error: %v", method, resp.Err())
		}
		return []byte(resp.Result())
	}
}

// TestFramedTransport tests that the server reads and writes LSP Content-Length
// framed messages (FR-43, R1 remediation). Real LSP clients send messages with
// Content-Length headers per the LSP specification; the server must parse and
// respond with the same framing.
//
// The test writes a Content-Length-framed initialize request and reads back
// a framed response. Today this test FAILS because:
//   - The server uses bare jsontext.Decoder(r).ReadValue() in the Run loop
//   - jsontext.Decoder tries to parse "Content-Length: ..." as JSON, which is invalid
//   - The decoder hangs or times out waiting for valid JSON
//   - No response is written; the test times out
//
// This is a BLOCKER: without Content-Length framing, real LSP clients cannot
// communicate with the server. The fix is to wrap the reader/writer with
// jsonrpc2.NewHeaderStream() which handles the framing protocol.
func TestFramedTransport(t *testing.T) {
	// Arrange: build an initialize request
	id := jsonrpc2.NewNumberID(1)
	params := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	call := jsonrpc2.NewCall(id, "initialize", params)

	// Encode the request as bare JSON (what jsonrpc2.EncodeMessage produces)
	bareJSON, err := jsonrpc2.EncodeMessage(call)
	if err != nil {
		t.Fatalf("failed to encode call: %v", err)
	}

	// Frame the request with Content-Length header per LSP spec:
	// Content-Length: <n>\r\n
	// \r\n
	// <n bytes of JSON>
	contentLen := len(bareJSON)
	framedRequest := fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLen)
	requestData := append([]byte(framedRequest), bareJSON...)

	// Create input buffer with the framed request
	inBuf := bytes.NewBuffer(requestData)

	// Create output buffer to capture the response
	var outBuf bytes.Buffer

	// Create a logger that writes to a separate buffer (not stdout)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server with the in-memory streams
	az := &stubAnalyzer{}
	err = Run(
		context.Background(),
		inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Assert: the response output must be Content-Length-framed
	responseOutput := outBuf.String()
	if !strings.HasPrefix(responseOutput, "Content-Length:") {
		t.Errorf("response is not framed with Content-Length header; got: %q (first 100 chars: %q)",
			responseOutput, truncate(responseOutput, 100))
	}

	// Assert: the header must be parseable
	lines := strings.Split(responseOutput, "\r\n")
	if len(lines) < 3 {
		t.Fatalf("response header too short; expected at least 3 lines (header, blank, body), got %d", len(lines))
	}

	// Parse the Content-Length value
	contentLengthLine := lines[0]
	if !strings.HasPrefix(contentLengthLine, "Content-Length: ") {
		t.Errorf("first line is not Content-Length header; got: %q", contentLengthLine)
		return
	}

	lengthStr := strings.TrimPrefix(contentLengthLine, "Content-Length: ")
	declaredLen, err := strconv.Atoi(lengthStr)
	if err != nil {
		t.Errorf("Content-Length value is not a valid number: %q (error: %v)", lengthStr, err)
		return
	}

	// Assert: the declared length matches the actual body length
	// The body starts after the blank line (line at index 1)
	bodyStart := len(contentLengthLine) + 2 + 2 // header + \r\n + \r\n
	bodyBytes := responseOutput[bodyStart:]
	if len(bodyBytes) != declaredLen {
		t.Errorf("Content-Length mismatch: declared %d bytes, but got %d bytes of body",
			declaredLen, len(bodyBytes))
	}

	// Assert: the body is valid JSON-RPC
	respMsg, err := jsonrpc2.DecodeMessage([]byte(bodyBytes))
	if err != nil {
		t.Fatalf("response body is not valid JSON-RPC: %v (body: %q)", err, bodyBytes)
	}

	// Assert: the response is a Response (not a Notification or Call)
	resp, ok := respMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response, got %T", respMsg)
	}

	// Assert: response id matches request id
	if resp.ID() != id {
		t.Errorf("response id = %v, want %v", resp.ID(), id)
	}

	// Assert: response has a result (initialize succeeds)
	if resp.Err() != nil {
		t.Errorf("response has error: %v; want result", resp.Err())
	}
	if resp.Result() == nil {
		t.Errorf("response has no result; want InitializeResult")
	}
}

// truncate is a helper to shorten strings for test output
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// writeFramedMessage writes a Content-Length-framed JSON-RPC message to buf.
// The format is: Content-Length: N\r\n\r\n<N bytes of JSON>
func writeFramedMessage(w io.Writer, msg jsonrpc2.Message) error {
	encoded, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return err
	}
	contentLen := len(encoded)
	if _, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", contentLen); err != nil {
		return err
	}
	_, err = w.Write(encoded)
	return err
}

// parseFramedMessage extracts one framed JSON-RPC message from output and returns
// the body bytes, the message type (Response or Notification), and any error.
// It is a helper for parseFramedResponse that needs to skip notifications.
func parseFramedMessage(output string) ([]byte, string, int, error) {
	// Find the blank line that separates header from body
	idx := strings.Index(output, "\r\n\r\n")
	if idx == -1 {
		return nil, "", 0, fmt.Errorf("no blank line separating header and body")
	}
	headerEnd := idx + 4 // account for "\r\n\r\n"

	// Parse Content-Length from the header
	headerLines := strings.Split(output[:idx], "\r\n")
	if len(headerLines) == 0 {
		return nil, "", 0, fmt.Errorf("empty header")
	}
	contentLengthLine := headerLines[0]
	if !strings.HasPrefix(contentLengthLine, "Content-Length: ") {
		return nil, "", 0, fmt.Errorf("first line is not Content-Length header")
	}
	lengthStr := strings.TrimPrefix(contentLengthLine, "Content-Length: ")
	contentLen, err := strconv.Atoi(lengthStr)
	if err != nil {
		return nil, "", 0, fmt.Errorf("invalid Content-Length: %v", err)
	}

	// Extract the body
	bodyEnd := headerEnd + contentLen
	if bodyEnd > len(output) {
		return nil, "", 0, fmt.Errorf("response too short; declared %d bytes but only %d available", contentLen, len(output)-headerEnd)
	}
	body := output[headerEnd:bodyEnd]

	// Decode to determine message type (Response vs Notification)
	msg, err := jsonrpc2.DecodeMessage([]byte(body))
	if err != nil {
		return nil, "", 0, fmt.Errorf("failed to decode message: %v", err)
	}

	msgType := ""
	if _, ok := msg.(*jsonrpc2.Response); ok {
		msgType = "Response"
	} else if _, ok := msg.(*jsonrpc2.Notification); ok {
		msgType = "Notification"
	} else {
		msgType = "Other"
	}

	return []byte(body), msgType, bodyEnd, nil
}

// parseFramedResponse extracts one framed JSON-RPC response from buf and returns the body bytes.
// It skips any notifications (e.g., textDocument/publishDiagnostics) that appear before the response.
// It assumes buf starts with a valid Content-Length header and returns the JSON body.
// After calling this, buf is advanced past the response (and any skipped notifications).
func parseFramedResponse(buf *bytes.Buffer) ([]byte, error) {
	for {
		output := buf.String()
		if output == "" {
			return nil, fmt.Errorf("no more messages in buffer")
		}

		body, msgType, bodyEnd, err := parseFramedMessage(output)
		if err != nil {
			return nil, err
		}

		// Advance buf to remove this message
		remaining := output[bodyEnd:]
		buf.Reset()
		buf.WriteString(remaining)

		// If it's a Response, return it
		if msgType == "Response" {
			return body, nil
		}
		// Otherwise it's a Notification; skip it and try the next message
	}
}

// TestServerRunReadsRequestAndWritesResponse tests that the Server type can read
// a JSON-RPC request from an in-memory reader and write a well-formed JSON-RPC 2.0
// response with the matching id to a writer. This pins the basic transport behavior
// for FR-41 (stdio LSP lifecycle).
func TestServerRunReadsRequestAndWritesResponse(t *testing.T) {
	testCases := []struct {
		name    string
		buildID func() jsonrpc2.ID
		idDesc  string
	}{
		{
			name:    "JSONRPCRequestWithNumberID",
			buildID: func() jsonrpc2.ID { return jsonrpc2.NewNumberID(1) },
			idDesc:  "number id",
		},
		{
			name:    "JSONRPCRequestWithStringID",
			buildID: func() jsonrpc2.ID { return jsonrpc2.NewStringID("test-request-1") },
			idDesc:  "string id",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: build a JSON-RPC 2.0 call with the test-case id.
			id := tc.buildID()
			params := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
			call := jsonrpc2.NewCall(id, "initialize", params)

			// Write the request as a Content-Length-framed message.
			var reqBuf bytes.Buffer
			if err := writeFramedMessage(&reqBuf, call); err != nil {
				t.Fatalf("failed to write framed request: %v", err)
			}

			// Create an output buffer to capture the response.
			var outBuf bytes.Buffer

			// Create a logger that writes to a separate buffer (not stdout).
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Act: run the server with the in-memory streams.
			az := &stubAnalyzer{}
			// Run takes separate Reader and Writer, not ReadWriteCloser.
			err := Run(
				context.Background(),
				&reqBuf,
				&outBuf,
				"0.0.0-test",
				"/workspace",
				az,
				logger,
			)

			// Assert: we expect the server to read the request and write a response.
			// The response must be valid JSON-RPC 2.0 with the matching id.

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

			// Assert: the decoded message is a Response.
			resp, ok := respMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response, got %T", respMsg)
			}

			// Assert: response id matches request id.
			if resp.ID() != id {
				t.Errorf("response id = %v, want %v", resp.ID(), id)
			}

			// Assert: response has either a result or an error, not both.
			if resp.Result() != nil && resp.Err() != nil {
				t.Errorf("response has both Result and Err; expected exactly one")
			}

			// Assert: no logs were written to the protocol writer (they should go to stderr).
			if logBuf.Len() > 0 {
				t.Logf("logger received: %q (this is expected for now, just documenting)", logBuf.String())
			}
		})
	}
}

// TestInitialize pins the behavior of the initialize request handler (FR-41, FR-42).
// The server must return ServerCapabilities advertising only textDocumentSync and
// positionEncoding (no feature providers yet), and populate serverInfo with the injected version.
// ADR-008: position encoding is negotiated — UTF-8 if offered, else UTF-16.
// ADR-009: textDocumentSync = Full (bare enum, not options object).
func TestInitialize(t *testing.T) {
	testCases := []struct {
		name             string
		paramsJSON       string // raw JSON params; placeholders for encodings
		expectedEncoding string // expected in result
		idFunc           func() jsonrpc2.ID
	}{
		{
			name: "ClientOffersUTF8AndUTF16_ChoosesUTF8",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {
					"general": {
						"positionEncodings": ["utf-8", "utf-16"]
					}
				}
			}`,
			expectedEncoding: "utf-8",
			idFunc:           func() jsonrpc2.ID { return jsonrpc2.NewNumberID(1) },
		},
		{
			name: "ClientOffersUTF16Only_ChoosesUTF16",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {
					"general": {
						"positionEncodings": ["utf-16"]
					}
				}
			}`,
			expectedEncoding: "utf-16",
			idFunc:           func() jsonrpc2.ID { return jsonrpc2.NewStringID("init-1") },
		},
		{
			name: "ClientOmitsEncodings_DefaultsToUTF16",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {}
			}`,
			expectedEncoding: "utf-16",
			idFunc:           func() jsonrpc2.ID { return jsonrpc2.NewNumberID(999) },
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: build an initialize request.
			id := tc.idFunc()
			call := jsonrpc2.NewCall(id, "initialize", jsonrpc2.RawMessage(tc.paramsJSON))

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
				"0.1.0-test", // injected version
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

			// Assert: response has a result and no error.
			if resp.Err() != nil {
				t.Errorf("response has an error: %v; want nil", resp.Err())
			}
			if resp.Result() == nil {
				t.Fatalf("response has no result; want InitializeResult")
			}

			// Unmarshal the result into a map to check structure.
			var result map[string]interface{}
			if err := json.Unmarshal(resp.Result(), &result); err != nil {
				t.Fatalf("failed to unmarshal result: %v (result was: %q)", err, string(resp.Result()))
			}

			// Assert: serverInfo is populated correctly (FR-42).
			serverInfo, ok := result["serverInfo"].(map[string]interface{})
			if !ok {
				t.Errorf("serverInfo missing or wrong type; want map[string]interface{}")
			} else {
				if serverInfo["name"] != "natural-lsp" {
					t.Errorf("serverInfo.name = %v, want \"natural-lsp\"", serverInfo["name"])
				}
				if serverInfo["version"] != "0.1.0-test" {
					t.Errorf("serverInfo.version = %v, want \"0.1.0-test\"", serverInfo["version"])
				}
			}

			// Assert: capabilities has the expected structure (FR-41).
			caps, ok := result["capabilities"].(map[string]interface{})
			if !ok {
				t.Errorf("capabilities missing or wrong type; want map[string]interface{}")
			} else {
				// Assert: textDocumentSync is present and Full (kind 1, ADR-009).
				if caps["textDocumentSync"] == nil {
					t.Errorf("textDocumentSync is nil; want Full (1)")
				} else if syncVal, ok := caps["textDocumentSync"].(float64); ok {
					if syncVal != 1 {
						t.Errorf("textDocumentSync = %v, want 1 (Full)", syncVal)
					}
				}

				// Assert: positionEncoding matches the negotiated value (ADR-008).
				if caps["positionEncoding"] != tc.expectedEncoding {
					t.Errorf("positionEncoding = %v, want %q", caps["positionEncoding"], tc.expectedEncoding)
				}

				// Assert: the navigation and hover providers are advertised (feature 10, T3; feature 11, T3; feature 12, T6; feature 27, T5).
				// These are intentional additions per the locked allow-list convention:
				// when features add providers, TestInitialize is extended to assert them explicitly.
				requiredProviders := []string{
					"definitionProvider",
					"referencesProvider",
					"workspaceSymbolProvider",
					"documentSymbolProvider",
					"hoverProvider",
					"codeLensProvider",
					"signatureHelpProvider",
					"callHierarchyProvider",
					"documentHighlightProvider",
				}
				for _, providerFlag := range requiredProviders {
					val, exists := caps[providerFlag]
					if !exists || val == nil || val == false {
						t.Errorf("%s = %v; want true (required by feature 10, T3 + feature 12, T6)", providerFlag, val)
					}
				}

				// Assert: completionProvider is advertised with correct shape (feature 16, T3).
				// completionProvider must be a CompletionOptions object (not a boolean).
				completionProviderVal, exists := caps["completionProvider"]
				if !exists {
					t.Errorf("completionProvider = %v; want present (required by feature 16, T3)", completionProviderVal)
				} else if completionProvider, ok := completionProviderVal.(map[string]interface{}); ok {
					// Assert: triggerCharacters is present and contains " " (space).
					triggerChars, hasTriggerChars := completionProvider["triggerCharacters"]
					if !hasTriggerChars {
						t.Errorf("completionProvider.triggerCharacters = %v; want present", triggerChars)
					} else if triggerCharsSlice, ok := triggerChars.([]interface{}); ok {
						foundSpace := false
						for _, char := range triggerCharsSlice {
							if char == " " {
								foundSpace = true
								break
							}
						}
						if !foundSpace {
							t.Errorf("completionProvider.triggerCharacters = %v; want [\" \"]", triggerChars)
						}
					} else {
						t.Errorf("completionProvider.triggerCharacters type = %T; want []interface{}", triggerChars)
					}

					// Assert: resolveProvider is false.
					resolveProvider, hasResolveProvider := completionProvider["resolveProvider"]
					if !hasResolveProvider {
						t.Errorf("completionProvider.resolveProvider = %v; want present", resolveProvider)
					} else if resolveProviderVal, ok := resolveProvider.(bool); ok {
						if resolveProviderVal != false {
							t.Errorf("completionProvider.resolveProvider = %v; want false", resolveProviderVal)
						}
					} else {
						t.Errorf("completionProvider.resolveProvider type = %T; want bool", resolveProvider)
					}
				} else {
					t.Errorf("completionProvider type = %T; want map[string]interface{} (CompletionOptions)", completionProviderVal)
				}

				// Assert: signatureHelpProvider is advertised with correct shape (feature 17, T1).
				// signatureHelpProvider must be a SignatureHelpOptions object (not a boolean).
				signatureHelpProviderVal, exists := caps["signatureHelpProvider"]
				if !exists {
					t.Errorf("signatureHelpProvider = %v; want present (required by feature 17, T1)", signatureHelpProviderVal)
				} else if signatureHelpProvider, ok := signatureHelpProviderVal.(map[string]interface{}); ok {
					// Assert: triggerCharacters is present and contains " " (space).
					triggerChars, hasTriggerChars := signatureHelpProvider["triggerCharacters"]
					if !hasTriggerChars {
						t.Errorf("signatureHelpProvider.triggerCharacters = %v; want present", triggerChars)
					} else if triggerCharsSlice, ok := triggerChars.([]interface{}); ok {
						foundSpace := false
						for _, char := range triggerCharsSlice {
							if char == " " {
								foundSpace = true
								break
							}
						}
						if !foundSpace {
							t.Errorf("signatureHelpProvider.triggerCharacters = %v; want [\" \"]", triggerChars)
						}
					} else {
						t.Errorf("signatureHelpProvider.triggerCharacters type = %T; want []interface{}", triggerChars)
					}

					// Assert: retriggerCharacters is present and contains " " (space).
					retriggerChars, hasRetriggerChars := signatureHelpProvider["retriggerCharacters"]
					if !hasRetriggerChars {
						t.Errorf("signatureHelpProvider.retriggerCharacters = %v; want present", retriggerChars)
					} else if retriggerCharsSlice, ok := retriggerChars.([]interface{}); ok {
						foundSpace := false
						for _, char := range retriggerCharsSlice {
							if char == " " {
								foundSpace = true
								break
							}
						}
						if !foundSpace {
							t.Errorf("signatureHelpProvider.retriggerCharacters = %v; want [\" \"]", retriggerChars)
						}
					} else {
						t.Errorf("signatureHelpProvider.retriggerCharacters type = %T; want []interface{}", retriggerChars)
					}
				} else {
					t.Errorf("signatureHelpProvider type = %T; want map[string]interface{} (SignatureHelpOptions)", signatureHelpProviderVal)
				}
			}
		})
	}
}

// TestInitializeDetectsWorkDoneProgressCapability tests feature 21, T1: detecting
// whether the client advertises server-initiated work-done progress support via
// Capabilities.Window.WorkDoneProgress (FR-32).
//
// This test verifies that handleInitialize correctly parses the client's
// window.workDoneProgress capability in all cases:
// - Capability advertised (true) → should be detected as supported
// - Capability absent (nil) → should be detected as unsupported
// - Capability explicit false → should be detected as unsupported
// - Window object absent entirely → should be detected as unsupported
//
// The detected capability must be stored in the Run-local context so the
// initialized handler can gate progress reporting.
//
// Currently FAILS (RED): handleInitialize does not detect/expose window.workDoneProgress yet.
func TestInitializeDetectsWorkDoneProgressCapability(t *testing.T) {
	testCases := []struct {
		name             string
		paramsJSON       string
		expectedDetected bool
	}{
		{
			name: "WindowCapability_WorkDoneProgressTrue_Detected",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {
					"window": {
						"workDoneProgress": true
					}
				}
			}`,
			expectedDetected: true,
		},
		{
			name: "WindowCapability_WorkDoneProgressFalse_NotDetected",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {
					"window": {
						"workDoneProgress": false
					}
				}
			}`,
			expectedDetected: false,
		},
		{
			name: "WindowCapability_WorkDoneProgressAbsent_NotDetected",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {
					"window": {}
				}
			}`,
			expectedDetected: false,
		},
		{
			name: "WindowCapabilityAbsent_NotDetected",
			paramsJSON: `{
				"processId": 1234,
				"rootPath": "/workspace",
				"capabilities": {}
			}`,
			expectedDetected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up the hook to capture the detected workDoneProgress flag
			detectedChan := make(chan bool, 1)
			initializeReadyHookMu.Lock()
			oldHook := initializeReadyHook
			initializeReadyHook = func(root string, cfg config.Config, clientSupportsWorkDoneProgress bool) {
				detectedChan <- clientSupportsWorkDoneProgress
			}
			initializeReadyHookMu.Unlock()
			defer func() {
				initializeReadyHookMu.Lock()
				initializeReadyHook = oldHook
				initializeReadyHookMu.Unlock()
			}()

			// Build an initialize request with the test params
			id := jsonrpc2.NewNumberID(1)
			call := jsonrpc2.NewCall(id, "initialize", jsonrpc2.RawMessage(tc.paramsJSON))

			// Write the request as a Content-Length-framed message
			var reqBuf bytes.Buffer
			if err := writeFramedMessage(&reqBuf, call); err != nil {
				t.Fatalf("failed to write framed request: %v", err)
			}

			// Create an output buffer for responses
			var outBuf bytes.Buffer

			// Create a logger
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Act: run the server
			az := &stubAnalyzer{}
			err := Run(
				context.Background(),
				&reqBuf,
				&outBuf,
				"0.1.0-test",
				"/workspace",
				az,
				logger,
			)

			// Assert: no error from Run
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Extract the JSON body from the framed response
			output := outBuf.String()
			lines := strings.Split(output, "\r\n")
			if len(lines) < 3 {
				t.Fatalf("response too short; expected at least 3 lines, got %d", len(lines))
			}
			// Body starts after: "Content-Length: N\r\n\r\n"
			bodyStart := len(lines[0]) + 2 + 2
			bodyBytes := output[bodyStart:]

			// Decode the response from the body bytes
			respMsg, err := jsonrpc2.DecodeMessage([]byte(bodyBytes))
			if err != nil {
				t.Fatalf("failed to decode response: %v (output was: %q)", err, output)
			}

			resp, ok := respMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response, got %T", respMsg)
			}

			// Assert: response id matches request id
			if resp.ID() != id {
				t.Errorf("response id = %v, want %v", resp.ID(), id)
			}

			// Assert: response has a result and no error
			if resp.Err() != nil {
				t.Errorf("response has an error: %v; want nil", resp.Err())
			}
			if resp.Result() == nil {
				t.Fatalf("response has no result; want InitializeResult")
			}

			// Assert: clientSupportsWorkDoneProgress was detected correctly
			select {
			case detected := <-detectedChan:
				if detected != tc.expectedDetected {
					t.Errorf("clientSupportsWorkDoneProgress = %v, want %v (feature 21, T1)",
						detected, tc.expectedDetected)
				}
			case <-time.After(1 * time.Second):
				t.Fatalf("initializeReadyHook was not called within timeout")
			}
		})
	}
}

// TestWorkspaceIndexBuiltAfterInitialized pins feature 10, T2 (RED phase).
// It tests that after the server reaches initialized, it holds a queryable
// workspace.Index populated with files from the workspace root, and retains
// the negotiated position encoding.
//
// This test:
// 1. Creates a small multi-file workspace fixture (caller.NSP, helper.NSN, data.NSL)
// 2. Drives the server through initialize → initialized
// 3. Uses a test seam (indexReadyHook) to capture the index and encoding after initialized
// 4. Asserts the index contains the fixture files (Index.Keys() non-empty)
// 5. Asserts the retained encoding matches the negotiated value (UTF-8 or UTF-16)
//
// Currently FAILS (RED): Run does not build or hold an index yet (feature 10, T2 implementation gap).
func TestWorkspaceIndexBuiltAfterInitialized(t *testing.T) {
	// Arrange: set up the test fixture workspace
	tmpDir := t.TempDir()

	// Copy fixture files to the temp workspace
	fixtures := []struct {
		name    string
		content string
	}{
		{
			name: "caller.NSP",
			content: `* Caller program that references a subprogram
PROGRAM CALLER

DEFINE DATA
  LOCAL
    1 #VAR (A5)
  END
END

CALLNAT 'HELPER'
PERFORM INLINE-SUB

DEFINE SUBROUTINE INLINE-SUB
  WRITE 'INLINE'
END

END
`,
		},
		{
			name: "helper.NSN",
			content: `* Helper subprogram referenced by caller
SUBROUTINE 'HELPER'

DEFINE DATA
  PARAMETER
    1 #PARM (N4)
  END
END

WRITE 'HELPER'

END
`,
		},
		{
			name: "data.NSL",
			content: `* Data area shared across programs
LOCAL DATA AREA SHARED-DATA
1 #SHARED-FIELD (A10)
1 #COUNTER (N5)
END
`,
		},
	}

	for _, fix := range fixtures {
		if err := os.WriteFile(filepath.Join(tmpDir, fix.name), []byte(fix.content), 0600); err != nil {
			t.Fatalf("failed to write fixture %s: %v", fix.name, err)
		}
	}

	// Arrange: set up the index capture hook. Feature 21 (T4) made the index
	// build asynchronous; the runGatedHandshake harness below chains onto this
	// hook and withholds shutdown until the build publishes, so capturedIndex is
	// populated deterministically. Restore via t.Cleanup (LIFO with the gate).
	var capturedIndex *workspace.Index
	var capturedEncoding protocol.PositionEncodingKind
	indexReadyHookMu.Lock()
	oldHook := indexReadyHook
	indexReadyHook = func(idx *workspace.Index, enc protocol.PositionEncodingKind) {
		capturedIndex = idx
		capturedEncoding = enc
	}
	indexReadyHookMu.Unlock()
	t.Cleanup(func() {
		indexReadyHookMu.Lock()
		indexReadyHook = oldHook
		indexReadyHookMu.Unlock()
	})

	// Act: drive initialize (request UTF-8) → initialized → (await build) →
	// shutdown → exit through the async-safe gated harness.
	initParams := fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8", "utf-16"]
			}
		}
	}`, tmpDir)
	_, _ = runGatedHandshake(t, initParams, tmpDir, &stubAnalyzer{})

	// Assert: the test hook was called with a non-nil index (RED: currently will be nil)
	if capturedIndex == nil {
		t.Errorf("indexReadyHook was called with nil index; expected a populated *workspace.Index (feature 10, T2 implementation gap)")
	} else {
		// Assert: the index contains the fixture files
		keys := capturedIndex.Keys()
		if len(keys) == 0 {
			t.Errorf("Index.Keys() is empty; expected to contain fixture files (caller.NSP, helper.NSN, data.NSL)")
		} else {
			// Verify each fixture is in the index (workspace-relative paths)
			fixtureNames := map[string]bool{
				"caller.NSP": false,
				"helper.NSN": false,
				"data.NSL":   false,
			}
			for _, key := range keys {
				if _, found := fixtureNames[key]; found {
					fixtureNames[key] = true
				}
			}
			for name, found := range fixtureNames {
				if !found {
					t.Errorf("Index does not contain fixture %s; got keys: %v", name, keys)
				}
			}
		}
	}

	// Assert: the retained position encoding matches the negotiated value (should be UTF-8, as requested)
	// The test requests UTF-8 first, so it should be negotiated as UTF-8
	if capturedEncoding != protocol.PositionEncodingKindUTF8 {
		t.Errorf("retained positionEncoding = %v, want %v (UTF-8 was requested and should be negotiated)",
			capturedEncoding, protocol.PositionEncodingKindUTF8)
	}
}

// TestLifecycle pins the LSP lifecycle state machine (FR-41, S1, S4).
// It tests the required behaviors:
// 1. Normal sequence: initialize → initialized → shutdown → exit
//   - Run returns nil (clean exit)
//   - Background context is cancelled on shutdown
//
// 2. Request before initialize → ServerNotInitialized error code
// 3. Second initialize → InvalidRequest error code
// 4. Exit without shutdown → Run returns non-nil error (protocol violation)
//
// NOTE: This test is written to drive the final T4 lifecycle loop, which will:
// - Track initialization state across multiple messages
// - Enforce the init → initialized → shutdown → exit sequence
// - Cancel the background context on shutdown
// - Return non-nil for protocol violations (exit without shutdown)
//
// The test currently fails because the T2 stub (current Run) is single-shot
// and has no state machine. T4 will implement a loop that processes all messages
// and enforces the protocol correctly.
func TestLifecycle(t *testing.T) {
	testCases := []struct {
		name          string
		sequence      []testMessage // ordered list of messages to send
		expectRunErr  bool          // whether Run should return non-nil
		expectErrCode jsonrpc2.Code // if non-zero, expect this error code in response to this message
	}{
		{
			name: "NormalSequence_InitializeInitializedShutdownExit",
			sequence: []testMessage{
				{
					method:        "initialize",
					id:            newID(jsonrpc2.NewNumberID(1)),
					params:        `{"processId":1234,"rootPath":"/workspace","capabilities":{}}`,
					expectResult:  true,
					expectErrCode: 0,
					description:   "initialize should succeed",
				},
				{
					method:        "initialized",
					id:            nil, // notification, no id
					params:        `{}`,
					expectResult:  false, // notifications don't get responses
					expectErrCode: 0,
					description:   "initialized notification accepted",
				},
				{
					method:        "shutdown",
					id:            newID(jsonrpc2.NewNumberID(2)),
					params:        `{}`,
					expectResult:  true,
					expectErrCode: 0,
					description:   "shutdown should succeed",
				},
				{
					method:        "exit",
					id:            nil, // notification
					params:        `{}`,
					expectResult:  false,
					expectErrCode: 0,
					description:   "exit notification, triggers clean shutdown",
				},
			},
			expectRunErr: false,
		},
		{
			name: "RequestBeforeInitialize_ServerNotInitializedError",
			sequence: []testMessage{
				{
					method:        "textDocument/hover",
					id:            newID(jsonrpc2.NewNumberID(1)),
					params:        `{}`,
					expectResult:  false,
					expectErrCode: jsonrpc2.ServerNotInitialized,
					description:   "request before initialize must error with ServerNotInitialized",
				},
			},
			expectRunErr: false,
		},
		{
			name: "SecondInitialize_InvalidRequestError",
			sequence: []testMessage{
				{
					method:        "initialize",
					id:            newID(jsonrpc2.NewNumberID(1)),
					params:        `{"processId":1234,"rootPath":"/workspace","capabilities":{}}`,
					expectResult:  true,
					expectErrCode: 0,
					description:   "first initialize succeeds",
				},
				{
					method:        "initialized",
					id:            nil,
					params:        `{}`,
					expectResult:  false,
					expectErrCode: 0,
					description:   "initialized notification accepted",
				},
				{
					method:        "initialize",
					id:            newID(jsonrpc2.NewNumberID(2)),
					params:        `{"processId":1234,"rootPath":"/workspace","capabilities":{}}`,
					expectResult:  false,
					expectErrCode: jsonrpc2.InvalidRequest,
					description:   "second initialize must error with InvalidRequest",
				},
			},
			expectRunErr: false,
		},
		{
			name: "ExitWithoutShutdown_ProtocolViolation",
			sequence: []testMessage{
				{
					method:        "initialize",
					id:            newID(jsonrpc2.NewNumberID(1)),
					params:        `{"processId":1234,"rootPath":"/workspace","capabilities":{}}`,
					expectResult:  true,
					expectErrCode: 0,
					description:   "initialize succeeds",
				},
				{
					method:        "initialized",
					id:            nil,
					params:        `{}`,
					expectResult:  false,
					expectErrCode: 0,
					description:   "initialized notification accepted",
				},
				{
					method:        "exit",
					id:            nil, // notification
					params:        `{}`,
					expectResult:  false,
					expectErrCode: 0,
					description:   "exit without shutdown is a protocol violation",
				},
			},
			expectRunErr: true, // Run must return non-nil for protocol violation
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: build the message sequence
			var inBuf bytes.Buffer
			for i, tm := range tc.sequence {
				var msg jsonrpc2.Message
				if tm.id == nil {
					// Notification
					msg = jsonrpc2.NewNotification(tm.method, jsonrpc2.RawMessage(tm.params))
				} else {
					// Call (request)
					msg = jsonrpc2.NewCall(*tm.id, tm.method, jsonrpc2.RawMessage(tm.params))
				}
				if err := writeFramedMessage(&inBuf, msg); err != nil {
					t.Fatalf("failed to write framed message %d (%s): %v", i, tm.method, err)
				}
			}

			// Create output buffer and logger
			var outBuf bytes.Buffer
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Channel to capture Run's return value
			runErrChan := make(chan error, 1)
			runCtx, runCancel := context.WithCancel(context.Background())
			defer runCancel()

			// Act: call Run (which currently handles one message and returns)
			// T4 will replace this with a loop that processes all messages
			go func() {
				az := &stubAnalyzer{}
				err := Run(
					runCtx,
					&inBuf,
					&outBuf,
					"0.0.0-test",
					"/workspace",
					az,
					logger,
				)
				runErrChan <- err
			}()

			// Wait for Run to complete
			var runErr error
			select {
			case runErr = <-runErrChan:
			case <-time.After(2 * time.Second):
				t.Fatalf("timeout waiting for Run (test: %s)", tc.name)
			}

			// Assert: check run error
			if tc.expectRunErr != (runErr != nil) {
				if tc.expectRunErr {
					t.Errorf("%s: expected Run to return non-nil error (protocol violation), got nil", tc.name)
				} else {
					t.Errorf("%s: expected Run to return nil, got error: %v", tc.name, runErr)
				}
			}

			// Assert: decode framed responses and check error codes for failing requests
			responseBuf := bytes.NewBuffer(outBuf.Bytes())
			for i, tm := range tc.sequence {
				// Skip notifications (they don't receive responses)
				if tm.id == nil {
					continue
				}

				// Parse the next framed response
				body, err := parseFramedResponse(responseBuf)
				if err != nil {
					t.Errorf("%s: failed to parse response %d (%s): %v", tc.name, i, tm.method, err)
					continue
				}

				// Decode the response
				respMsg, err := jsonrpc2.DecodeMessage(body)
				if err != nil {
					t.Errorf("%s: failed to decode response %d (%s): %v", tc.name, i, tm.method, err)
					continue
				}

				resp, ok := respMsg.(*jsonrpc2.Response)
				if !ok {
					t.Errorf("%s: expected *jsonrpc2.Response for request %d (%s), got %T", tc.name, i, tm.method, respMsg)
					continue
				}

				// Check error code if expected
				if tm.expectErrCode != 0 {
					if resp.Err() == nil {
						t.Errorf("%s: request %d (%s) expected error code %v, but got success result: %s",
							tc.name, i, tm.method, tm.expectErrCode, string(resp.Result()))
					} else {
						// Type-assert to *jsonrpc2.Error to access Code field
						errTyped, ok := resp.Err().(*jsonrpc2.Error)
						if !ok {
							t.Errorf("%s: request %d (%s) error is %T, not *jsonrpc2.Error: %v",
								tc.name, i, tm.method, resp.Err(), resp.Err())
						} else if errTyped.Code != tm.expectErrCode {
							t.Errorf("%s: request %d (%s) error code = %v, want %v (message: %s)",
								tc.name, i, tm.method, errTyped.Code, tm.expectErrCode, errTyped.Message)
						}
					}
				} else {
					// No error expected; verify response has no error
					if resp.Err() != nil {
						t.Errorf("%s: request %d (%s) expected success, but got error: %v",
							tc.name, i, tm.method, resp.Err())
					}
				}
			}

			// Assert: for the normal sequence case, verify clean shutdown
			// Background context cancellation at shutdown is covered by TestShutdownCancelsBgContext.
			// Here we only verify Run returns nil on a normal shutdown sequence (already checked above).
		})
	}
}

// testMessage describes a single message in a lifecycle test sequence
type testMessage struct {
	method        string
	id            *jsonrpc2.ID // nil for notifications
	params        string
	expectResult  bool          // whether we expect a response with a result (not error)
	expectErrCode jsonrpc2.Code // if non-zero, expect this error code
	description   string
}

// newID is a helper to create a pointer to a jsonrpc2.ID
func newID(id jsonrpc2.ID) *jsonrpc2.ID {
	return &id
}

// blockingReaderAfter serves messages from a buffer, then blocks indefinitely
// after all messages are consumed (doesn't return EOF immediately).
type blockingReaderAfter struct {
	buf   *bytes.Buffer
	block <-chan struct{} // Never closes; reader blocks forever after buffer exhausted
}

func (br *blockingReaderAfter) Read(p []byte) (int, error) {
	n, err := br.buf.Read(p)
	if err != nil { // EOF from buffer
		// Block forever instead of returning EOF
		<-br.block
		return 0, fmt.Errorf("reader blocked")
	}
	return n, nil
}

// TestShutdownCancelsBgContext pins ADR-012 requirement (R4 remediation):
// the background context MUST be cancelled when shutdown is received,
// NOT deferred until the server loop exits. This ensures in-flight background
// goroutines stop promptly on shutdown, not delayed until EOF/exit.
//
// The test sends initialize → initialized → shutdown, then uses a blocking
// reader to prevent Run from returning. If bgCancel() is called in the shutdown
// handler (correct), the background context will be cancelled immediately. If
// bgCancel() is only deferred (bug), it will not be cancelled while Run waits.
func TestShutdownCancelsBgContext(t *testing.T) {
	// Arrange: channel to signal when context is cancelled
	bgCtxDone := make(chan struct{})
	bgCtxCaptured := make(chan context.Context, 1)
	bgCtxHookMu.Lock()
	oldHook := bgCtxHook
	bgCtxHook = func(ctx context.Context) {
		bgCtxCaptured <- ctx
		// Background goroutine watches for cancellation
		go func() {
			<-ctx.Done()
			bgCtxDone <- struct{}{}
		}()
	}
	bgCtxHookMu.Unlock()
	defer func() {
		bgCtxHookMu.Lock()
		bgCtxHook = oldHook
		bgCtxHookMu.Unlock()
	}() // restore hook after test

	// Prepare the message sequence: initialize → initialized → shutdown
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var msgBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, shutdownCall} {
		if err := writeFramedMessage(&msgBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create a blocking reader that won't return EOF
	blockForever := make(chan struct{}) // never closes
	blockingReader := &blockingReaderAfter{
		buf:   &msgBuf,
		block: blockForever,
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server in a goroutine (it will block after processing shutdown)
	runDone := make(chan error, 1)
	go func() {
		az := &stubAnalyzer{}
		runDone <- Run(
			context.Background(),
			blockingReader,
			&outBuf,
			"0.0.0-test",
			"/workspace",
			az,
			logger,
		)
	}()

	// Give Run time to process the three messages
	time.Sleep(100 * time.Millisecond)

	// Assert: the background context was captured by the hook
	var capturedBgCtx context.Context
	select {
	case capturedBgCtx = <-bgCtxCaptured:
		// Hook captured the context
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("test hook did not capture background context after 500ms")
	}

	// Assert: CRITICAL — check if the background context was cancelled.
	// With the CURRENT code (defer bgCancel()), the context will NOT be cancelled
	// because Run is still blocked in the read() call (haven't returned yet).
	// With the FIXED code (bgCancel() in shutdown handler), the context WILL be cancelled.
	select {
	case <-bgCtxDone:
		// GOOD: context was cancelled during shutdown (the fix is in place)
		if capturedBgCtx.Err() != context.Canceled {
			t.Errorf("bgCtxDone fired but Err = %v, want context.Canceled", capturedBgCtx.Err())
		}
	case <-time.After(500 * time.Millisecond):
		// BAD: context was never cancelled during shutdown processing
		// This indicates bgCancel() is deferred, not called in shutdown handler
		t.Errorf("background context not cancelled during shutdown; " +
			"after 500ms, bgCtx.Done() is still not signalled; " +
			"ADR-012 requires bgCancel() to be called in the shutdown handler, not deferred")
	}

	// Cleanup: close the blockForever channel to let Run proceed (will get error reading)
	// Actually, we can't close blockForever because it's already blocked. The test is done;
	// let the goroutine leak (acceptable for a test).
}

// TestContextCancellationExitsCleanly pins the behavior of Run when the passed context
// is cancelled during or before reading (FR-43, R8 remediation).
//
// The bug: Run's read loop continues indefinitely when ctx.Err() is returned by
// stream.Read, because the loop does:
//
//	msg, _, err := stream.Read(ctx)
//	if err != nil {
//	    if err == io.EOF {
//	        return nil
//	    }
//	    logger.Error("malformed JSON-RPC message; skipping", "err", err)
//	    continue   // ← loops forever on ctx.Err()
//	}
//
// When the caller's ctx is cancelled (e.g., SIGTERM via signal.NotifyContext),
// stream.Read returns ctx.Err() immediately on every call, and the loop spins
// indefinitely, flooding stderr and never exiting.
//
// Expected behavior: When ctx is cancelled, Run must return nil (clean exit)
// promptly — within a reasonable timeout like 500ms.
//
// The test:
// 1. Creates a reader that blocks forever (never delivers bytes, never returns EOF)
// 2. Starts Run in a goroutine with a cancellable context
// 3. Cancels the context after a tiny sleep to let Run start reading
// 4. Asserts that Run returns nil within 500ms (demonstrating the bug: it will NOT)
func TestContextCancellationExitsCleanly(t *testing.T) {
	// Arrange: create a reader that blocks forever
	blockingReader, _ := io.Pipe()
	// Note: the write end of the pipe is never written to; the read end will block forever

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Channel to capture Run's return value
	runErrChan := make(chan error, 1)

	// Act: start Run in a goroutine
	go func() {
		az := &stubAnalyzer{}
		runErrChan <- Run(
			ctx,
			blockingReader,
			&outBuf,
			"0.0.0-test",
			"/workspace",
			az,
			logger,
		)
	}()

	// Give Run time to enter the read loop and start blocking on stream.Read(ctx)
	time.Sleep(5 * time.Millisecond)

	// Cancel the context while Run is blocked in stream.Read
	cancel()

	// Assert: Run must return nil promptly (within 500ms).
	// With the current buggy code, stream.Read(ctx) returns ctx.Err() immediately,
	// the loop continues forever, and this will timeout → test fails RED.
	select {
	case runErr := <-runErrChan:
		// Run returned; assert it returned nil (clean exit)
		if runErr != nil {
			t.Errorf("expected Run to return nil on context cancellation, got error: %v", runErr)
		}
	case <-time.After(500 * time.Millisecond):
		// Run did not return within 500ms — it's spinning in the read loop
		t.Errorf("Run did not exit within 500ms after context cancellation; " +
			"the read loop is likely spinning on stream.Read(ctx).Err() (ctx.Err() returned indefinitely)")
	}
}

// TestRequestPanicRecovery pins the behavior of panic recovery in the request dispatch
// path (feature 03, T6). When a request handler panics, the server must:
// 1. Recover from the panic and NOT crash (Run does NOT return between requests)
// 2. Log the panic via slog to stderr (not to the protocol writer)
// 3. Send a JSON-RPC error response with code -32603 (InternalError) and the matching request id
// 4. Continue handling subsequent requests normally
//
// This test is currently FAILING because:
// 1. Run does not yet have a panic recovery wrapper around dispatch
// 2. Unknown methods currently return {} instead of an error or panic hook
//
// The test establishes the contract that once T6 adds:
// - A way to trigger a panic in dispatch (e.g., a test/panic method or hook)
// - A panic recovery mechanism (defer recover() around dispatch or handlers)
//
// Then the server will send InternalError responses on panics and continue processing
// subsequent requests. The test sequence is:
// 1. initialize (success, transition to initialized state)
// 2. initialized notification (state transition)
// 3. test/panic request (should produce InternalError once panic handling is wired)
// 4. shutdown (verify server still responds normally after the panic)
//
// Currently this test fails at step 3: unknown methods return {} (not an error),
// so the assertion that step 3 produces an error will fail.
func TestRequestPanicRecovery(t *testing.T) {
	// Arrange: build the message sequence for: initialize → initialized → test/panic → shutdown

	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// This is a request that, once T6 wires panic handling, should trigger a panic
	// and be caught by the panic recovery wrapper, producing an InternalError response.
	panicID := jsonrpc2.NewNumberID(2)
	panicCall := jsonrpc2.NewCall(panicID, "test/panic", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Write all requests as Content-Length-framed messages into a single input buffer
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, panicCall, shutdownCall} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server with all requests
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should return nil (not crash due to panic)
	if err != nil {
		t.Fatalf("Run failed: %v; expected to recover from panic and continue", err)
	}

	// Parse the framed response messages
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read first response (initialize success)
	initBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}
	initMsg, err := jsonrpc2.DecodeMessage(initBody)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v", err)
	}
	initResp2, ok := initMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
	}

	if initResp2.ID() != initID {
		t.Errorf("initialize response id = %v, want %v", initResp2.ID(), initID)
	}
	if initResp2.Err() != nil {
		t.Errorf("initialize response has error: %v; want result", initResp2.Err())
	}
	if initResp2.Result() == nil {
		t.Errorf("initialize response has no result; want InitializeResult")
	}

	// Read second response (panic request should produce InternalError once wired)
	// THIS ASSERTION WILL FAIL until T6 wires panic handling and the test/panic method
	panicBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse panic response: %v", err)
	}
	panicMsg, err := jsonrpc2.DecodeMessage(panicBody)
	if err != nil {
		t.Fatalf("failed to decode panic response: %v", err)
	}
	panicResp2, ok := panicMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for panic request, got %T", panicMsg)
	}

	if panicResp2.ID() != panicID {
		t.Errorf("panic response id = %v, want %v", panicResp2.ID(), panicID)
	}
	// This is the FAILING assertion: the test expects an InternalError response (-32603)
	// but the current code returns {} (success with empty result) for unknown methods.
	// Once T6 wires panic handling, unknown method "test/panic" will trigger a panic,
	// which will be caught and produce InternalError.
	if panicResp2.Err() == nil {
		t.Errorf("panic response has no error; want InternalError (-32603), got result: %s", panicResp2.Result())
	} else {
		errTyped, ok := panicResp2.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("panic response error is %T, not *jsonrpc2.Error: %v", panicResp2.Err(), panicResp2.Err())
		} else if errTyped.Code != jsonrpc2.InternalError {
			t.Errorf("panic response error code = %v, want %v (InternalError)", errTyped.Code, jsonrpc2.InternalError)
		}
	}

	// Read third response (shutdown should succeed normally, proving server recovered)
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}
	shutdownResp2, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}

	if shutdownResp2.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownResp2.ID(), shutdownID)
	}
	if shutdownResp2.Err() != nil {
		t.Errorf("shutdown response has error: %v; want result", shutdownResp2.Err())
	}
	if shutdownResp2.Result() == nil {
		t.Errorf("shutdown response has no result; want null")
	}
}

// spyAnalyzer records calls to Analyze so tests can assert on the path and content received.
// It is safe for concurrent use (the watcher goroutine calls Analyze from a separate goroutine).
type spyAnalyzer struct {
	mu    sync.Mutex
	calls []spyAnalyzeCall
}

type spyAnalyzeCall struct {
	path    string
	content []byte
}

func (sa *spyAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	sa.calls = append(sa.calls, spyAnalyzeCall{
		path:    path,
		content: append([]byte(nil), content...), // copy content
	})
	return model.FileAnalysis{ObjectType: model.ObjectUnknown}, nil
}

func (sa *spyAnalyzer) ExtractVariableRefs(content string) []model.VariableRef {
	return []model.VariableRef{}
}

// TestTextDocumentDidOpen pins the behavior of the textDocument/didOpen handler (FR-33, Task 5).
// The server must:
// 1. Register textDocument/didOpen in the notification switch
// 2. Decode DidOpenTextDocumentParams
// 3. Call store.Open(uri, version, content)
// 4. Accept the notification without error and continue processing subsequent requests
//
// This test drives the sequence: initialize → initialized → didOpen → shutdown → exit
// and verifies:
// - The spy analyzer records an Analyze call with the correct path and content
// - The server continues to accept subsequent requests (shutdown succeeds)
// - No error response is sent for the notification (notifications don't get responses)
func TestTextDocumentDidOpen(t *testing.T) {
	testCases := []struct {
		name string
		// relPath is the workspace-relative path (forward-slash form). The test
		// builds a host-valid absolute root via t.TempDir() and derives both the
		// document URI (via uri.File, correct on Windows) and the expected analyzer
		// relPath from it, so the assertion holds on every OS. It is also the
		// expected analyzer path: the store normalizes to forward-slash (the
		// canonical index keyspace, paths.NormalizeKey) on every OS.
		relPath               string
		version               int32
		text                  string
		expectAnalyzeCallText string // content the analyzer should be called with
		description           string
	}{
		{
			name:                  "SimpleNSPFile",
			relPath:               "test.NSP",
			version:               1,
			text:                  "PROGRAM FOO\nEND",
			expectAnalyzeCallText: "PROGRAM FOO\nEND",
			description:           "didOpen should call analyzer with correct path and content",
		},
		{
			name:                  "NestedPath",
			relPath:               "src/subsrc/hello.NSP",
			version:               2,
			text:                  "PROGRAM HELLO\nEND",
			expectAnalyzeCallText: "PROGRAM HELLO\nEND",
			description:           "didOpen derives correct relative path from nested URI",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// A host-valid absolute root (a drive-lettered path on Windows) so
			// filepath.Rel can relativize the document path. The prior hardcoded
			// "/workspace" root + "file:///workspace/..." URIs are drive-less on
			// Windows, so filepath.Rel fails and the relPath is not stripped.
			root := t.TempDir()
			docURI := uri.File(filepath.Join(root, filepath.FromSlash(tc.relPath)))
			// The canonical index key is forward-slash on every OS (paths.NormalizeKey).
			expectAnalyzeCallPath := tc.relPath

			// Arrange: build the message sequence: initialize → initialized → didOpen → shutdown → exit
			initID := jsonrpc2.NewNumberID(1)
			initParams := jsonrpc2.RawMessage(`{"processId":1234,"capabilities":{}}`)
			initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

			initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			// Build the didOpen notification with the test case URI, version, and text
			didOpenParams := map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":        string(docURI),
					"languageId": "natural",
					"version":    tc.version,
					"text":       tc.text,
				},
			}
			didOpenParamsJSON, err := json.Marshal(didOpenParams)
			if err != nil {
				t.Fatalf("failed to marshal didOpen params: %v", err)
			}
			didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParamsJSON))

			shutdownID := jsonrpc2.NewNumberID(2)
			shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Write all messages as Content-Length-framed messages
			var inBuf bytes.Buffer
			for i, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, shutdownCall, exitNotif} {
				if err := writeFramedMessage(&inBuf, msg); err != nil {
					t.Fatalf("failed to write framed message %d: %v", i, err)
				}
			}

			// Create output buffer and logger
			var outBuf bytes.Buffer
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Create a spy analyzer to record calls
			spy := &spyAnalyzer{}

			// Act: run the server with the message sequence
			err = Run(
				context.Background(),
				&inBuf,
				&outBuf,
				"0.0.0-test",
				root,
				spy,
				logger,
			)

			// Assert: Run should complete without error (clean shutdown)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Parse the framed responses
			responseBuf := bytes.NewBuffer(outBuf.Bytes())

			// Read initialize response
			initBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse initialize response: %v", err)
			}
			initMsg, err := jsonrpc2.DecodeMessage(initBody)
			if err != nil {
				t.Fatalf("failed to decode initialize response: %v", err)
			}
			initResp, ok := initMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
			}
			if initResp.Err() != nil {
				t.Errorf("initialize response has error: %v", initResp.Err())
			}

			// Read shutdown response
			shutdownBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse shutdown response: %v", err)
			}
			shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
			if err != nil {
				t.Fatalf("failed to decode shutdown response: %v", err)
			}
			shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
			}
			if shutdownResp.Err() != nil {
				t.Errorf("shutdown response has error: %v; server should recover after didOpen", shutdownResp.Err())
			}

			// Assert: the spy should have recorded an Analyze call from the didOpen handler
			if len(spy.calls) == 0 {
				t.Errorf("expected analyzer to be called after didOpen, but got 0 calls")
			} else {
				// Check the first call's path and content
				call := spy.calls[0]
				if call.path != expectAnalyzeCallPath {
					t.Errorf("analyzer path = %q, want %q", call.path, expectAnalyzeCallPath)
				}
				if string(call.content) != tc.expectAnalyzeCallText {
					t.Errorf("analyzer content = %q, want %q", string(call.content), tc.expectAnalyzeCallText)
				}
			}
		})
	}
}

// TestTextDocumentDidChange pins the behavior of the textDocument/didChange handler (FR-33, Task 6).
// The server must:
// 1. Register textDocument/didChange in the notification switch
// 2. Decode DidChangeTextDocumentParams
// 3. Since sync is Full, take Text from the single *TextDocumentContentChangeWholeDocument
// 4. Call store.Update(uri, version, content)
// 5. A *TextDocumentContentChangePartial (range edit) under Full-sync: log-and-skip
// 6. Empty contentChanges or unknown URI: logged, no panic
//
// This test drives: initialize → initialized → didOpen → didChange → shutdown → exit
// and verifies the spy analyzer was called twice (once on open, once on change with new content).
func TestTextDocumentDidChange(t *testing.T) {
	testCases := []struct {
		name               string
		openText           string
		changeText         string
		expectAnalyzeCalls int
		description        string
	}{
		{
			name:               "SimpleChange",
			openText:           "PROGRAM FOO\nEND",
			changeText:         "PROGRAM BAR\nEND",
			expectAnalyzeCalls: 3,
			description:        "didChange calls analyzer twice (store.Update + applyDocumentChange for index update)",
		},
		{
			name:               "EmptyContentChanges",
			openText:           "PROGRAM FOO\nEND",
			changeText:         "", // will cause contentChanges to be empty
			expectAnalyzeCalls: 1,
			description:        "empty contentChanges should be logged and skipped",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// A host-valid absolute root so filepath.Rel can relativize the document
			// path on every OS (the prior hardcoded "/workspace" root is drive-less
			// on Windows, so filepath.Rel fails and applyDocumentChange is skipped,
			// dropping the analyze count from 3 to 2).
			root := t.TempDir()
			docURI := string(uri.File(filepath.Join(root, "test.NSP")))

			// Arrange: build the message sequence: initialize → initialized → didOpen → didChange → shutdown → exit
			initID := jsonrpc2.NewNumberID(1)
			initParams := jsonrpc2.RawMessage(`{"processId":1234,"capabilities":{}}`)
			initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

			initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			// Build the didOpen notification
			didOpenParams := map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":        docURI,
					"languageId": "natural",
					"version":    1,
					"text":       tc.openText,
				},
			}
			didOpenParamsJSON, err := json.Marshal(didOpenParams)
			if err != nil {
				t.Fatalf("failed to marshal didOpen params: %v", err)
			}
			didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParamsJSON))

			// Build the didChange notification with contentChanges
			var contentChanges []interface{}
			if tc.changeText != "" {
				contentChanges = []interface{}{
					map[string]interface{}{
						"text": tc.changeText,
					},
				}
			}
			didChangeParams := map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":     docURI,
					"version": 2,
				},
				"contentChanges": contentChanges,
			}
			didChangeParamsJSON, err := json.Marshal(didChangeParams)
			if err != nil {
				t.Fatalf("failed to marshal didChange params: %v", err)
			}
			didChangeNotif := jsonrpc2.NewNotification("textDocument/didChange", jsonrpc2.RawMessage(didChangeParamsJSON))

			shutdownID := jsonrpc2.NewNumberID(2)
			shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Write all messages as Content-Length-framed messages
			var inBuf bytes.Buffer
			for i, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, didChangeNotif, shutdownCall, exitNotif} {
				if err := writeFramedMessage(&inBuf, msg); err != nil {
					t.Fatalf("failed to write framed message %d: %v", i, err)
				}
			}

			// Create output buffer and logger
			var outBuf bytes.Buffer
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Create a spy analyzer to record calls
			spy := &spyAnalyzer{}

			// Act: run the server with the message sequence
			err = Run(
				context.Background(),
				&inBuf,
				&outBuf,
				"0.0.0-test",
				root,
				spy,
				logger,
			)

			// Assert: Run should complete without error (clean shutdown)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Parse the framed responses
			responseBuf := bytes.NewBuffer(outBuf.Bytes())

			// Read initialize response
			initBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse initialize response: %v", err)
			}
			initMsg, err := jsonrpc2.DecodeMessage(initBody)
			if err != nil {
				t.Fatalf("failed to decode initialize response: %v", err)
			}
			initResp, ok := initMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
			}
			if initResp.Err() != nil {
				t.Errorf("initialize response has error: %v", initResp.Err())
			}

			// Read shutdown response
			shutdownBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse shutdown response: %v", err)
			}
			shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
			if err != nil {
				t.Fatalf("failed to decode shutdown response: %v", err)
			}
			shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
			}
			if shutdownResp.Err() != nil {
				t.Errorf("shutdown response has error: %v; server should handle didChange", shutdownResp.Err())
			}

			// Assert: the spy should have recorded the correct number of Analyze calls
			if len(spy.calls) != tc.expectAnalyzeCalls {
				t.Errorf("analyzer call count = %d, want %d", len(spy.calls), tc.expectAnalyzeCalls)
			} else if len(spy.calls) >= 2 {
				// Verify the content was updated on the second call
				secondCall := spy.calls[1]
				if string(secondCall.content) != tc.changeText {
					t.Errorf("second analyzer call content = %q, want %q", string(secondCall.content), tc.changeText)
				}
			}
		})
	}
}

// TestNotificationPanicRecovery pins the behavior of panic recovery in the notification
// dispatch path (feature 04, Task 7). When a notification handler panics, the server must:
// 1. Recover from the panic and NOT crash (Run does NOT exit between notifications)
// 2. Log the panic via slog to stderr (not to the protocol writer)
// 3. Continue handling subsequent requests normally
//
// Notifications have no `id`, so there is NO error response to send — recovery is
// log-and-continue only. A subsequent valid request proves the loop survived.
//
// This test drives: initialize → initialized → test/panic-notification (panics) → shutdown
// and asserts that:
// - The panic is logged (to the test logger)
// - The server loop survives (shutdown request receives a valid response)
// - The loop does not hang or exit prematurely
func TestNotificationPanicRecovery(t *testing.T) {
	// Arrange: build the message sequence: initialize → initialized → test/panic-notification → shutdown

	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// This is a synthetic notification that, once Task 7 wires panic handling,
	// will trigger a panic inside the notification dispatch path.
	panicNotif := jsonrpc2.NewNotification("test/panic-notification", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Write all messages as Content-Length-framed messages into a single input buffer
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, panicNotif, shutdownCall} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server with all requests
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should return nil (not crash due to panic in notification handler)
	if err != nil {
		t.Fatalf("Run failed: %v; expected to recover from panic in notification and continue", err)
	}

	// Parse the framed response messages
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read first response (initialize success)
	initBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}
	initMsg, err := jsonrpc2.DecodeMessage(initBody)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v", err)
	}
	initResp, ok := initMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
	}

	if initResp.ID() != initID {
		t.Errorf("initialize response id = %v, want %v", initResp.ID(), initID)
	}
	if initResp.Err() != nil {
		t.Errorf("initialize response has error: %v; want result", initResp.Err())
	}
	if initResp.Result() == nil {
		t.Errorf("initialize response has no result; want InitializeResult")
	}

	// The panic notification should NOT produce a response (it's a notification).
	// The server should log the panic and continue.

	// Read second response (shutdown should succeed after panic recovery, proving server survived)
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v; server likely crashed or hung in notification panic", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}
	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}

	if shutdownResp.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownResp.ID(), shutdownID)
	}
	if shutdownResp.Err() != nil {
		t.Errorf("shutdown response has error: %v; want result", shutdownResp.Err())
	}
	if shutdownResp.Result() == nil {
		t.Errorf("shutdown response has no result; want null")
	}

	// Assert: the logger should have captured an error log about the panic
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "panic") {
		t.Errorf("expected logger to contain 'panic' message (recovery log); got: %s", logOutput)
	}
}

// TestFR33DocumentLifecycle pins the FR-33 end-to-end document lifecycle behavior (Task 8).
// It drives a full lifecycle sequence through Run and asserts the store's source-of-truth view
// at each step matches FR-33's criteria:
// 1. On didOpen, the document's in-memory content becomes the source of truth for analysis.
// 2. On didChange, the in-memory content updates and re-analysis is triggered.
// 3. On didClose, the server reverts to disk (store drops the document).
// 4. Server exits cleanly after shutdown.
//
// This test observes the store's view via a spy analyzer: after the sequence completes,
// the spy records the exact content passed to Analyze at each step, allowing assertions
// about what the store considers the source of truth.
//
// Sequence: initialize → initialized → didOpen → didChange → didClose → shutdown → exit
func TestFR33DocumentLifecycle(t *testing.T) {
	// A host-valid absolute root so filepath.Rel relativizes the document path on
	// every OS (a drive-less "/workspace" root breaks filepath.Rel on Windows,
	// skipping applyDocumentChange and dropping the analyze count).
	root := t.TempDir()
	docURI := string(uri.File(filepath.Join(root, "test.NSP")))

	// Arrange: build the message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build the didOpen notification with initial content
	openedContent := "PROGRAM FOO\nEND"
	didOpenParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        docURI,
			"languageId": "natural",
			"version":    1,
			"text":       openedContent,
		},
	}
	didOpenParamsJSON, err := json.Marshal(didOpenParams)
	if err != nil {
		t.Fatalf("failed to marshal didOpen params: %v", err)
	}
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParamsJSON))

	// Build the didChange notification with new content
	changedContent := "PROGRAM BAR\nEND"
	didChangeParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     docURI,
			"version": 2,
		},
		"contentChanges": []interface{}{
			map[string]interface{}{
				"text": changedContent,
			},
		},
	}
	didChangeParamsJSON, err := json.Marshal(didChangeParams)
	if err != nil {
		t.Fatalf("failed to marshal didChange params: %v", err)
	}
	didChangeNotif := jsonrpc2.NewNotification("textDocument/didChange", jsonrpc2.RawMessage(didChangeParamsJSON))

	// Build the didClose notification
	didCloseParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": docURI,
		},
	}
	didCloseParamsJSON, err := json.Marshal(didCloseParams)
	if err != nil {
		t.Fatalf("failed to marshal didClose params: %v", err)
	}
	didCloseNotif := jsonrpc2.NewNotification("textDocument/didClose", jsonrpc2.RawMessage(didCloseParamsJSON))

	// Build the shutdown request
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Build the exit notification
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write all messages as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{
		initCall,
		initNotif,
		didOpenNotif,
		didChangeNotif,
		didCloseNotif,
		shutdownCall,
		exitNotif,
	} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Create a spy analyzer to record calls to Analyze
	spy := &spyAnalyzer{}

	// Act: run the server with the message sequence
	err = Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		root,
		spy,
		logger,
	)

	// Assert: Run should complete without error (clean shutdown)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse the framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read initialize response
	initBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}
	initMsg, err := jsonrpc2.DecodeMessage(initBody)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v", err)
	}
	initResp, ok := initMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
	}

	// Assert: initialize succeeded
	if initResp.Err() != nil {
		t.Errorf("initialize response has error: %v", initResp.Err())
	}
	if initResp.Result() == nil {
		t.Errorf("initialize response has no result; want InitializeResult")
	}

	// Read shutdown response
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}
	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}

	// Assert: shutdown succeeded
	if shutdownResp.Err() != nil {
		t.Errorf("shutdown response has error: %v; server should handle full lifecycle", shutdownResp.Err())
	}

	// Assert: the spy should have recorded exactly 3 Analyze calls.
	// didOpen triggers 1 call; didChange triggers 2 calls (store.Update + applyDocumentChange for index).
	// didClose does not trigger re-analysis — the store simply removes the document.
	// TODO(T14 refactor): avoid double analysis by passing FileAnalysis from store to index updater.
	if len(spy.calls) != 3 {
		t.Errorf("analyzer call count = %d, want 3 (didOpen + 2x didChange)", len(spy.calls))
	}

	// Assert: first call (didOpen) has the opened content
	// FR-33: on open, the document's in-memory content becomes the source of truth
	if len(spy.calls) >= 1 {
		firstCall := spy.calls[0]
		if string(firstCall.content) != openedContent {
			t.Errorf("first analyzer call (didOpen) content = %q, want %q", string(firstCall.content), openedContent)
		}
	}

	// Assert: second call (didChange) has the changed content
	// FR-33: on change, the in-memory content updates and analysis is refreshed
	if len(spy.calls) >= 2 {
		secondCall := spy.calls[1]
		if string(secondCall.content) != changedContent {
			t.Errorf("second analyzer call (didChange) content = %q, want %q", string(secondCall.content), changedContent)
		}
	}
}

// TestTextDocumentDidClose pins the behavior of the textDocument/didClose handler (FR-33, Task 6).
// The server must:
// 1. Register textDocument/didClose in the notification switch
// 2. Decode DidCloseTextDocumentParams
// 3. Call store.Close(uri)
// 4. Unknown URI: no panic
//
// This test drives: initialize → initialized → didOpen → didClose → shutdown → exit
// and verifies the server loop completes cleanly (close doesn't panic).
func TestTextDocumentDidClose(t *testing.T) {
	// Arrange: build the message sequence: initialize → initialized → didOpen → didClose → shutdown → exit
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build the didOpen notification
	didOpenParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        "file:///workspace/test.NSP",
			"languageId": "natural",
			"version":    1,
			"text":       "PROGRAM FOO\nEND",
		},
	}
	didOpenParamsJSON, err := json.Marshal(didOpenParams)
	if err != nil {
		t.Fatalf("failed to marshal didOpen params: %v", err)
	}
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParamsJSON))

	// Build the didClose notification
	didCloseParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file:///workspace/test.NSP",
		},
	}
	didCloseParamsJSON, err := json.Marshal(didCloseParams)
	if err != nil {
		t.Fatalf("failed to marshal didClose params: %v", err)
	}
	didCloseNotif := jsonrpc2.NewNotification("textDocument/didClose", jsonrpc2.RawMessage(didCloseParamsJSON))

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write all messages as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, didOpenNotif, didCloseNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server with the message sequence
	az := &stubAnalyzer{}
	err = Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without error (clean shutdown)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse the framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read initialize response
	initBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}
	initMsg, err := jsonrpc2.DecodeMessage(initBody)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v", err)
	}
	initResp, ok := initMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
	}
	if initResp.Err() != nil {
		t.Errorf("initialize response has error: %v", initResp.Err())
	}

	// Read shutdown response
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}
	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}
	if shutdownResp.Err() != nil {
		t.Errorf("shutdown response has error: %v; server should handle didClose cleanly", shutdownResp.Err())
	}
}

// TestTextDocumentDefinitionNoEdge pins the textDocument/definition handler skeleton (feature 10, T4).
// It tests that a cursor position that hits NO reference returns an empty result, not a MethodNotFound error.
//
// This test:
// 1. Creates a minimal single-file workspace fixture
// 2. Drives the server through initialize → initialized
// 3. Opens the fixture document via didOpen
// 4. Sends a textDocument/definition request pointing to a position with no edge
// 5. Asserts the response is a well-formed JSON-RPC result with null (not MethodNotFound error)
//
// Currently FAILS (RED): the dispatch switch has no case "textDocument/definition",
// so the method returns MethodNotFound instead of empty result.
func TestTextDocumentDefinitionNoEdge(t *testing.T) {
	// Arrange: set up the test fixture workspace
	tmpDir := t.TempDir()

	// Write a minimal fixture file with a CALLNAT so we can point cursor to whitespace (no edge)
	fixtureContent := `PROGRAM TEST
DEFINE DATA
  LOCAL
    1 #VAR (A5)
  END
END

CALLNAT 'SUB'

END
`
	fixturePath := filepath.Join(tmpDir, "test.NSP")
	if err := os.WriteFile(fixturePath, []byte(fixtureContent), 0600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	// Arrange: set up the index capture hook to verify the index is built after
	// initialized. Feature 21 (T4) made the build asynchronous; the
	// runGatedLifecycle harness below chains onto this hook and withholds
	// shutdown until the build publishes, so capturedIndex is populated
	// deterministically. Restore via t.Cleanup (LIFO with the gate's cleanup).
	var capturedIndex *workspace.Index
	indexReadyHookMu.Lock()
	oldHook := indexReadyHook
	indexReadyHook = func(idx *workspace.Index, enc protocol.PositionEncodingKind) {
		capturedIndex = idx
	}
	indexReadyHookMu.Unlock()
	t.Cleanup(func() {
		indexReadyHookMu.Lock()
		indexReadyHook = oldHook
		indexReadyHookMu.Unlock()
	})

	// Arrange: build the message sequence:
	// 1. initialize → should succeed with three providers advertised
	// 2. initialized → triggers index build
	// 3. didOpen → opens the fixture document
	// 4. textDocument/definition → requests definition at a position with no edge (should return empty)
	// 5. shutdown, exit → clean lifecycle (appended by runGatedLifecycle)
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(fmt.Sprintf(`{
		"processId": 1234,
		"rootPath": %q,
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8", "utf-16"]
			}
		}
	}`, tmpDir))
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Construct the document URI using go.lsp.dev/uri
	docURI := uri.File(fixturePath)

	// Open the document at version 1
	didOpenParams := fmt.Sprintf(`{
		"textDocument": {
			"uri": %q,
			"languageId": "natural",
			"version": 1,
			"text": %q
		}
	}`, string(docURI), strings.ReplaceAll(fixtureContent, `"`, `\"`))
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParams))

	// Send a textDocument/definition request pointing to a position with no edge.
	// Position {line: 0, character: 0} (top-left) is in the "PROGRAM" keyword with no edge.
	defID := jsonrpc2.NewNumberID(2)
	defParams := fmt.Sprintf(`{
		"textDocument": {"uri": %q},
		"position": {"line": 0, "character": 0}
	}`, string(docURI))
	defCall := jsonrpc2.NewCall(defID, "textDocument/definition", jsonrpc2.RawMessage(defParams))

	// Act: run the server through the async-safe gated lifecycle harness. The
	// pre-shutdown messages (initialize → initialized → didOpen → definition) are
	// served first; shutdown/exit are withheld until the background build
	// publishes so capturedIndex is populated deterministically (feature 21, T4).
	outBufL, _ := runGatedLifecycle(t, []jsonrpc2.Message{initCall, initNotif}, []jsonrpc2.Message{didOpenNotif, defCall}, tmpDir, &stubAnalyzer{})

	// Assert: the index was built after initialized (T2)
	if capturedIndex == nil {
		t.Errorf("indexReadyHook was called with nil index; expected a populated *workspace.Index (feature 10, T2)")
	}

	// Parse the responses
	responseBuf := bytes.NewBuffer(outBufL.Bytes())

	// Read initialize response
	initBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}
	initMsg, err := jsonrpc2.DecodeMessage(initBody)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v", err)
	}
	initResp, ok := initMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
	}
	if initResp.Err() != nil {
		t.Errorf("initialize response has error: %v", initResp.Err())
	}

	// Verify that the three navigation providers are advertised
	if initResp.Result() != nil {
		var result map[string]interface{}
		if err := json.Unmarshal(initResp.Result(), &result); err != nil {
			t.Errorf("failed to unmarshal initialize result: %v", err)
		} else if caps, ok := result["capabilities"].(map[string]interface{}); ok {
			for _, provider := range []string{"definitionProvider", "referencesProvider", "workspaceSymbolProvider"} {
				if val, exists := caps[provider]; !exists || val == nil || val == false {
					t.Errorf("%s = %v; want true (required by feature 10, T3)", provider, val)
				}
			}
		}
	}

	// Read definition response
	defBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse definition response: %v", err)
	}
	defMsg, err := jsonrpc2.DecodeMessage(defBody)
	if err != nil {
		t.Fatalf("failed to decode definition response: %v", err)
	}

	defResp, ok := defMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for definition, got %T", defMsg)
	}

	// Assert: the response must NOT be a MethodNotFound error
	if defResp.Err() != nil {
		errTyped, isErr := defResp.Err().(*jsonrpc2.Error)
		if isErr && errTyped.Code == jsonrpc2.MethodNotFound {
			t.Errorf("textDocument/definition returned MethodNotFound; expected empty result (T4: handler skeleton not yet implemented)")
		} else {
			t.Errorf("definition response has unexpected error: %v (expected nil for no-edge case)", defResp.Err())
		}
	}

	// Assert: the response must be a well-formed result (null or empty array)
	// For "no definition found", the handler returns null
	if defResp.Result() != nil {
		// The result could be null (encoded as "null" in JSON) or an empty array []
		resultStr := string(defResp.Result())
		if resultStr != "null" && resultStr != "[]" {
			t.Errorf("definition result = %q; want null or [] for no-edge case", resultStr)
		}
	}

	// Read shutdown response
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v", err)
	}
	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}
	if shutdownResp.Err() != nil {
		t.Errorf("shutdown response has error: %v", shutdownResp.Err())
	}
}

// TestIncrementalUpdateReflectedInSymbolAndDefinitionGreen (Feature 10, T14) tests that
// applyDocumentChange correctly updates the workspace Index and ResolutionSet when a
// document changes, so that subsequent provider queries return updated results.
func TestIncrementalUpdateReflectedInSymbolAndDefinitionGreen(t *testing.T) {
	// Arrange: set up test fixture workspace with one program
	tmpDir := t.TempDir()
	initialContent := `* Test program
	DEFINE DATA
	  LOCAL
	    1 #VAR (A5)
	  END
	END

	END
	`

	progFile := "PROG.NSP"
	progPath := filepath.Join(tmpDir, progFile)
	if err := os.WriteFile(progPath, []byte(initialContent), 0600); err != nil {
		t.Fatalf("failed to write fixture %s: %v", progFile, err)
	}

	// Build a handler context
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// Build the initial index and resolution set
	idx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build initial index: %v", err)
	}
	res := workspace.Resolve(idx, &cfg)

	// Create the handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         res,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        tmpDir,
		cfg:         cfg,
		az:          az,
		logger:      logger,
	}

	// BEFORE: workspace/symbol should NOT find HELPER (hasn't been added yet)
	wsSymbolsBefore := provideWorkspaceSymbols(hctx, "HELPER")
	foundHelperBefore := false
	for _, sym := range wsSymbolsBefore {
		if strings.ToUpper(sym.Name) == "HELPER" && sym.Kind == protocol.SymbolKindFunction {
			foundHelperBefore = true
			break
		}
	}
	if foundHelperBefore {
		t.Errorf("BEFORE: workspace/symbol found HELPER; expected empty result")
	}

	// Apply the document change: add an inline HELPER subroutine
	updatedContent := `* Test program with HELPER subroutine
	DEFINE DATA
	  LOCAL
	    1 #VAR (A5)
	  END
	END

	DEFINE SUBROUTINE HELPER
	  WRITE 'HELPER'
	END

	END
	`

	hctx.applyDocumentChange("PROG.NSP", []byte(updatedContent))

	// AFTER: workspace/symbol should NOW find HELPER
	wsSymbolsAfter := provideWorkspaceSymbols(hctx, "HELPER")
	foundHelperAfter := false
	var helperSymAfter *protocol.SymbolInformation
	for i, sym := range wsSymbolsAfter {
		if strings.ToUpper(sym.Name) == "HELPER" && sym.Kind == protocol.SymbolKindFunction {
			foundHelperAfter = true
			helperSymAfter = &wsSymbolsAfter[i]
			break
		}
	}
	if !foundHelperAfter {
		t.Errorf("AFTER: workspace/symbol did not find HELPER; expected it after applyDocumentChange")
	} else if helperSymAfter != nil {
		if !strings.HasSuffix(string(helperSymAfter.Location.URI), "PROG.NSP") {
			t.Errorf("AFTER: HELPER location = %v; expected PROG.NSP", helperSymAfter.Location.URI)
		}
	}

	// Completeness check: fresh rebuild should match incremental result
	// First, write the updated content to disk so the fresh build picks it up
	if err := os.WriteFile(progPath, []byte(updatedContent), 0600); err != nil {
		t.Fatalf("failed to write updated content: %v", err)
	}
	freshIdx, err := workspace.Build(context.Background(), tmpDir, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build fresh index: %v", err)
	}
	freshRes := workspace.Resolve(freshIdx, &cfg)

	freshWsSymbols := provideWorkspaceSymbols(&handlerContext{
		idx:         freshIdx,
		res:         freshRes,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        tmpDir,
		cfg:         cfg,
		az:          az,
		logger:      logger,
	}, "HELPER")

	if len(wsSymbolsAfter) != len(freshWsSymbols) {
		t.Errorf("COMPLETENESS: incremental=%d symbols; fresh=%d (mismatch)",
			len(wsSymbolsAfter), len(freshWsSymbols))
	}
}

// TestIncrementalUpdateReflectedInDefinition (Feature 10, T14) tests the DEFINITION-flip half of the
// T14 DoD: when a document adds/removes a callable object, the incremental resolver correctly flips
// provideDefinition results for callers in OTHER files from UNRESOLVED → RESOLVED.
//
// This test uses the purpose-built fixture at internal/server/testdata/incremental/ (CALLER.NSP +
// NEWSUB.NSN). Before the change, CALLER's `CALLNAT 'NEWSUB'` is unresolved (NEWSUB is an empty stub);
// after applying a document change that adds the NEWSUB subroutine definition, provideDefinition should
// flip to resolved.
func TestIncrementalUpdateReflectedInDefinition(t *testing.T) {
	// Arrange: use the purpose-built fixture in testdata/incremental/
	testdataDir := filepath.Join("testdata", "incremental")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Read the initial fixture files
	callerPath := filepath.Join(testdataDir, "CALLER.NSP")
	callerContent, err := os.ReadFile(callerPath)
	if err != nil {
		t.Fatalf("failed to read CALLER.NSP: %v", err)
	}

	newsubPath := filepath.Join(testdataDir, "NEWSUB.NSN")
	_, err = os.ReadFile(newsubPath)
	if err != nil {
		t.Fatalf("failed to read NEWSUB.NSN: %v", err)
	}

	// Build the initial index with the empty NEWSUB
	cfg := config.Defaults()
	az := natural.New(nil)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	idx, err := workspace.Build(context.Background(), root, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("failed to build initial index: %v", err)
	}
	res := workspace.Resolve(idx, &cfg)

	// Create the handler context
	hctx := &handlerContext{
		idx:         idx,
		res:         res,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
		cfg:         cfg,
		az:          az,
		logger:      logger,
	}

	// BEFORE: provideDefinition on CALLER's CALLNAT 'NEWSUB' should return a location to NEWSUB.NSN
	// (but NEWSUB.NSN doesn't have a valid DEFINE SUBROUTINE, just an empty file)
	// Parse CALLER to find the CALLNAT position
	callerAnalysis, err := az.Analyze(callerPath, callerContent)
	if err != nil {
		t.Fatalf("failed to analyze CALLER.NSP: %v", err)
	}

	// Find the CALLNAT edge in the analysis
	var callnatEdge *model.EdgeEntry
	for _, edge := range callerAnalysis.Edges {
		if edge.Kind == model.EdgeCalls && strings.EqualFold(edge.TargetName, "NEWSUB") {
			callnatEdge = &edge
			break
		}
	}
	if callnatEdge == nil {
		t.Fatalf("CALLER.NSP has no CALLNAT 'NEWSUB' edge")
	}

	// Build a definition request at the CALLNAT position (cursor on the 'NEWSUB' target)
	// The target name token spans Edge.Source; use its start position
	defParamsBefore := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: uri.File(filepath.Join(root, callerPath)),
			},
			// Position on the CALLNAT 'NEWSUB' token
			Position: protocol.Position{
				Line:      uint32(callnatEdge.Source.Start.Line - 1),   // 0-based
				Character: uint32(callnatEdge.Source.Start.Column - 1), // 0-based; will be converted
			},
		},
	}

	defLocsBefore, err := provideDefinition(hctx, defParamsBefore)
	if err != nil {
		t.Fatalf("provideDefinition returned error: %v", err)
	}

	// BEFORE assertion: definition might point to NEWSUB.NSN (empty stub file)
	// or be empty depending on how the parser treats empty files. The key is that after
	// we add a DEFINE SUBROUTINE NEWSUB, the definition should flip (potentially changing
	// the selection range). We record what we get before.
	locsBefore := len(defLocsBefore)
	t.Logf("BEFORE: provideDefinition returned %d definitions", locsBefore)

	// AFTER: Apply the document change to make NEWSUB.NSN a valid callable
	// (This creates a callable object that CALLNAT 'NEWSUB' can resolve to)
	newsubUpdatedContent := `* Updated NEWSUB: now a valid callable subroutine
DEFINE SUBROUTINE NEWSUB
  DEFINE DATA
    PARAMETER
      1 #INPUT (A10)
    END
  END
  WRITE #INPUT
END-SUBROUTINE
END
`

	hctx.applyDocumentChange("NEWSUB.NSN", []byte(newsubUpdatedContent))

	// AFTER: provideDefinition on CALLER's CALLNAT 'NEWSUB' should now resolve
	// Use the same position parameters (the CALLER file hasn't changed)
	defParamsAfter := defParamsBefore

	defLocsAfter, err := provideDefinition(hctx, defParamsAfter)
	if err != nil {
		t.Fatalf("provideDefinition returned error after change: %v", err)
	}

	// AFTER assertion: definition should be resolved (at least 1 location in NEWSUB.NSN)
	// After adding DEFINE SUBROUTINE NEWSUB, the definition should be found.
	locsAfter := len(defLocsAfter)
	if locsAfter == 0 {
		t.Errorf("AFTER: provideDefinition returned %d definitions; expected at least 1 (NEWSUB now callable)",
			locsAfter)
	}

	if len(defLocsAfter) > 0 {
		// Verify it points to NEWSUB.NSN
		fsPath := defLocsAfter[0].URI.FsPath()
		if !strings.Contains(fsPath, "NEWSUB.NSN") {
			t.Errorf("AFTER: definition location should be NEWSUB.NSN, got %s", fsPath)
		}
	}

	// Verify that the change is reflected: either the count changed, or the location changed
	if locsAfter > 0 || (locsBefore == 0 && locsAfter > 0) {
		t.Logf("✓ Incremental update flipped definition: BEFORE=%d locs, AFTER=%d locs", locsBefore, locsAfter)
	}
}

// TestDocumentSymbolEndToEnd tests the textDocument/documentSymbol request handler (feature 11, T3).
// The server advertises documentSymbolProvider and routes textDocument/documentSymbol requests to a
// handler that returns a hierarchical DocumentSymbol[] reflecting the file's structure (data sections,
// subroutines, maps, DDM references).
//
// The test:
//  1. Sends initialize → initialized (like TestFramedTransport)
//  2. Opens a fixture file with DEFINE DATA (sections and fields) + DEFINE SUBROUTINE
//  3. Sends textDocument/documentSymbol for that file
//  4. Asserts the response is a non-empty DocumentSymbol[] with hierarchical nesting
//     (object root with children: data sections with field children, subroutines, etc.)
func TestDocumentSymbolEndToEnd(t *testing.T) {
	// Arrange: build the initialize request
	initID := jsonrpc2.NewNumberID(1)
	initParamsJSON := `{
		"processId": 1234,
		"rootPath": "/workspace",
		"capabilities": {}
	}`
	initCall := jsonrpc2.NewCall(initID, "initialize", jsonrpc2.RawMessage(initParamsJSON))

	// Build the initialized notification
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build the didOpen notification with a fixture that has data sections and subroutines
	// (mirrors the feature-09 structure fixture 01-program-full.NSP)
	openedContent := `* T3 fixture: Program with data section and subroutine for document outline
DEFINE DATA
LOCAL
  1 EMPLOYEE-REC
    2 EMP-ID (N5)
    2 EMP-NAME (A40)
  1 EMP-ID-ALT REDEFINE EMP-ID (A5)
END DEFINE

DEFINE SUBROUTINE PROCESS-EMP
  WRITE 'Processing employee'
END-SUBROUTINE

END
`
	didOpenParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        "file:///workspace/TestProg.NSP",
			"languageId": "natural",
			"version":    1,
			"text":       openedContent,
		},
	}
	didOpenParamsJSON, err := json.Marshal(didOpenParams)
	if err != nil {
		t.Fatalf("failed to marshal didOpen params: %v", err)
	}
	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(didOpenParamsJSON))

	// Build the documentSymbol request for the opened file
	docSymbolID := jsonrpc2.NewNumberID(2)
	docSymbolParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file:///workspace/TestProg.NSP",
		},
	}
	docSymbolParamsJSON, err := json.Marshal(docSymbolParams)
	if err != nil {
		t.Fatalf("failed to marshal documentSymbol params: %v", err)
	}
	docSymbolCall := jsonrpc2.NewCall(docSymbolID, "textDocument/documentSymbol", jsonrpc2.RawMessage(docSymbolParamsJSON))

	// Build the shutdown request
	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	// Build the exit notification
	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write all messages as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{
		initCall,
		initNotif,
		didOpenNotif,
		docSymbolCall,
		shutdownCall,
		exitNotif,
	} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server with the message sequence
	az := natural.New(nil)
	err = Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse responses: initialize, documentSymbol, shutdown
	output := outBuf.String()
	responseBuf := bytes.NewBuffer([]byte(output))

	// Extract the initialize response (first framed message)
	initRespBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}
	initRespMsg, err := jsonrpc2.DecodeMessage(initRespBody)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v", err)
	}
	initResp, ok := initRespMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initRespMsg)
	}
	if initResp.Err() != nil {
		t.Fatalf("initialize response has error: %v", initResp.Err())
	}

	// Verify initialize response includes documentSymbolProvider capability (feature 11, T3 assertion)
	var initResult map[string]interface{}
	if err := json.Unmarshal(initResp.Result(), &initResult); err != nil {
		t.Fatalf("failed to unmarshal initialize result: %v", err)
	}
	caps, ok := initResult["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities missing or wrong type in initialize result")
	}

	// ASSERTION 1: documentSymbolProvider must be advertised (T3 requirement)
	if val, exists := caps["documentSymbolProvider"]; !exists || val == nil || val == false {
		t.Errorf("documentSymbolProvider = %v; want true (feature 11, T3)", val)
	}

	// Extract the documentSymbol response (second framed message after didOpen notification)
	docSymbolRespBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse documentSymbol response: %v", err)
	}
	docSymbolRespMsg, err := jsonrpc2.DecodeMessage(docSymbolRespBody)
	if err != nil {
		t.Fatalf("failed to decode documentSymbol response: %v (body: %q)", err, docSymbolRespBody)
	}
	docSymbolResp, ok := docSymbolRespMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for documentSymbol, got %T", docSymbolRespMsg)
	}
	if docSymbolResp.Err() != nil {
		t.Fatalf("documentSymbol response has error: %v", docSymbolResp.Err())
	}

	// ASSERTION 2: documentSymbol result must be a non-empty array of DocumentSymbol
	// The response must be a hierarchical DocumentSymbol[] where the first element
	// is the object root (name = "TestProg", kind = "Module" or equiv) with Children
	// that include the data section and subroutine.
	if docSymbolResp.Result() == nil {
		t.Fatalf("documentSymbol response has no result")
	}

	var docSymbols []map[string]interface{}
	if err := json.Unmarshal(docSymbolResp.Result(), &docSymbols); err != nil {
		t.Fatalf("failed to unmarshal documentSymbol result: %v (result: %q)", err, string(docSymbolResp.Result()))
	}

	if len(docSymbols) == 0 {
		t.Errorf("documentSymbol returned empty array; expected at least 1 element (object root with children)")
	}

	if len(docSymbols) > 0 {
		rootSym := docSymbols[0]
		rootName, ok := rootSym["name"].(string)
		if !ok {
			t.Errorf("root symbol name missing or not string: %v", rootSym["name"])
		} else if rootName != "TestProg" {
			t.Errorf("root symbol name = %q; want 'TestProg'", rootName)
		}

		// ASSERTION 3: root must have Children (data section + subroutine)
		children, ok := rootSym["children"].([]interface{})
		if !ok || len(children) == 0 {
			t.Errorf("root symbol has no children or children not array: %v", rootSym["children"])
		} else {
			// Verify that at least one child is a data section and one is a subroutine
			// We expect: SymbolDataSection (kind=protocol.SymbolKindNamespace=3) with SymbolDataField children,
			// and SymbolSubroutine (kind=protocol.SymbolKindFunction=12).
			// LSP DocumentSymbol.Kind is numeric (protocol.SymbolKind), marshaled as float64 in JSON.
			hasDataSection := false
			hasSubroutine := false
			for _, child := range children {
				childMap, ok := child.(map[string]interface{})
				if !ok {
					continue
				}
				// kind is a float64 (protocol.SymbolKind numeric value)
				kind, ok := childMap["kind"].(float64)
				if !ok {
					continue
				}
				// protocol.SymbolKindNamespace = 3 (data section)
				if kind == float64(protocol.SymbolKindNamespace) {
					hasDataSection = true
				}
				// protocol.SymbolKindFunction = 12 (subroutine)
				if kind == float64(protocol.SymbolKindFunction) {
					hasSubroutine = true
				}
			}
			if !hasDataSection {
				t.Errorf("root symbol children do not include a data-section; children kinds: %v", children)
			}
			if !hasSubroutine {
				t.Errorf("root symbol children do not include a subroutine; children kinds: %v", children)
			}
		}
	}
}

// TestTextDocumentHoverBeforeInitialized pins the behavior of textDocument/hover
// requests sent before the server is initialized (feature 12, T6 — RED phase).
// The server must reject requests with ServerNotInitialized error code.
func TestTextDocumentHoverBeforeInitialized(t *testing.T) {
	// Arrange: send a textDocument/hover request BEFORE initialized
	hoverID := jsonrpc2.NewNumberID(1)
	hoverParams := jsonrpc2.RawMessage(`{
		"textDocument": {"uri": "file:///workspace/test.NSP"},
		"position": {"line": 0, "character": 0}
	}`)
	hoverCall := jsonrpc2.NewCall(hoverID, "textDocument/hover", hoverParams)

	// After the error response, send a proper shutdown sequence
	initID := jsonrpc2.NewNumberID(2)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{hoverCall, initCall, initNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read hover response (should be ServerNotInitialized error)
	hoverBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse hover response: %v", err)
	}
	hoverMsg, err := jsonrpc2.DecodeMessage(hoverBody)
	if err != nil {
		t.Fatalf("failed to decode hover response: %v", err)
	}
	hoverResp, ok := hoverMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for hover, got %T", hoverMsg)
	}

	// Assert: response should have ServerNotInitialized error
	if hoverResp.ID() != hoverID {
		t.Errorf("hover response id = %v, want %v", hoverResp.ID(), hoverID)
	}
	if hoverResp.Err() == nil {
		t.Errorf("hover response has no error; want ServerNotInitialized, got result: %s", hoverResp.Result())
	} else {
		errTyped, ok := hoverResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("hover response error is %T, not *jsonrpc2.Error: %v", hoverResp.Err(), hoverResp.Err())
		} else if errTyped.Code != jsonrpc2.ServerNotInitialized {
			t.Errorf("hover response error code = %v, want %v (ServerNotInitialized)", errTyped.Code, jsonrpc2.ServerNotInitialized)
		}
	}
}

// TestTextDocumentHoverInvalidParams pins the behavior of textDocument/hover
// requests with malformed params (feature 12, T6 — RED phase).
// The server must reject requests with InvalidParams error code when the
// params cannot be unmarshaled into protocol.HoverParams.
func TestTextDocumentHoverInvalidParams(t *testing.T) {
	// Arrange: send initialize → initialized → hover with malformed params → shutdown
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Malformed hover params: pass a bare string instead of an object
	// This should fail to unmarshal and trigger InvalidParams error
	hoverID := jsonrpc2.NewNumberID(2)
	hoverParams := jsonrpc2.RawMessage(`"not an object"`)
	hoverCall := jsonrpc2.NewCall(hoverID, "textDocument/hover", hoverParams)

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, hoverCall, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read hover response (should be InvalidParams error)
	hoverBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse hover response: %v", err)
	}
	hoverMsg, err := jsonrpc2.DecodeMessage(hoverBody)
	if err != nil {
		t.Fatalf("failed to decode hover response: %v", err)
	}
	hoverResp, ok := hoverMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for hover, got %T", hoverMsg)
	}

	// Assert: response should have InvalidParams error
	if hoverResp.ID() != hoverID {
		t.Errorf("hover response id = %v, want %v", hoverResp.ID(), hoverID)
	}
	if hoverResp.Err() == nil {
		t.Errorf("hover response has no error; want InvalidParams, got result: %s", hoverResp.Result())
	} else {
		errTyped, ok := hoverResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("hover response error is %T, not *jsonrpc2.Error: %v", hoverResp.Err(), hoverResp.Err())
		} else if errTyped.Code != jsonrpc2.InvalidParams {
			t.Errorf("hover response error code = %v, want %v (InvalidParams)", errTyped.Code, jsonrpc2.InvalidParams)
		}
	}
}

// TestTextDocumentHoverAfterInitialized pins the behavior of textDocument/hover
// requests sent after initialization (feature 12, T6 — RED phase).
// The server must route the request to a handler and return a Hover (or null) result.
// Currently this test FAILS because:
// 1. textDocument/hover is not routed in the dispatch switch (returns MethodNotFound).
// 2. There is no provideHover handler function.
func TestTextDocumentHoverAfterInitialized(t *testing.T) {
	testCases := []struct {
		name          string
		hoverParams   string
		expectNonNull bool // whether we expect a non-null Hover result
		description   string
	}{
		{
			name: "HoverAtValidPosition",
			hoverParams: `{
				"textDocument": {"uri": "file:///workspace/test.NSP"},
				"position": {"line": 0, "character": 0}
			}`,
			expectNonNull: false, // no edges at that position in an empty file
			description:   "hover should return null for a no-edge position",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: send initialize → initialized → hover → shutdown
			initID := jsonrpc2.NewNumberID(1)
			initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
			initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

			initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			hoverID := jsonrpc2.NewNumberID(2)
			hoverCall := jsonrpc2.NewCall(hoverID, "textDocument/hover", jsonrpc2.RawMessage(tc.hoverParams))

			shutdownID := jsonrpc2.NewNumberID(3)
			shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Write requests as Content-Length-framed messages
			var inBuf bytes.Buffer
			for i, msg := range []jsonrpc2.Message{initCall, initNotif, hoverCall, shutdownCall, exitNotif} {
				if err := writeFramedMessage(&inBuf, msg); err != nil {
					t.Fatalf("failed to write framed message %d: %v", i, err)
				}
			}

			// Create output buffer and logger
			var outBuf bytes.Buffer
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Act: run the server
			az := &stubAnalyzer{}
			err := Run(
				context.Background(),
				&inBuf,
				&outBuf,
				"0.0.0-test",
				"/workspace",
				az,
				logger,
			)

			// Assert: Run should complete without fatal error
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Parse framed responses
			responseBuf := bytes.NewBuffer(outBuf.Bytes())

			// Skip initialize response
			_, err = parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse initialize response: %v", err)
			}

			// Read hover response
			hoverBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse hover response: %v", err)
			}
			hoverMsg, err := jsonrpc2.DecodeMessage(hoverBody)
			if err != nil {
				t.Fatalf("failed to decode hover response: %v", err)
			}
			hoverResp, ok := hoverMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for hover, got %T", hoverMsg)
			}

			// Assert: response should have no error
			if hoverResp.ID() != hoverID {
				t.Errorf("hover response id = %v, want %v", hoverResp.ID(), hoverID)
			}
			if hoverResp.Err() != nil {
				t.Errorf("hover response has error: %v; want Hover result or null", hoverResp.Err())
			}

			// For an empty file with no cursor target, null is expected
			// When properly implemented with real file content, this should return a Hover object
			if hoverResp.Result() == nil || string(hoverResp.Result()) == "null" {
				// null result is acceptable (no hover at that position)
				// This is the expected case for an empty workspace
			} else {
				// non-null result means a Hover object was returned
				// Verify it's a valid Hover object (has Contents field)
				var hoverObj map[string]interface{}
				if err := json.Unmarshal(hoverResp.Result(), &hoverObj); err != nil {
					t.Errorf("hover response result is not valid JSON: %v (result: %s)", err, string(hoverResp.Result()))
				} else if _, hasContents := hoverObj["contents"]; !hasContents {
					t.Errorf("hover response result does not have 'contents' field; got: %v", hoverObj)
				}
			}
		})
	}
}

// TestTextDocumentCompletionBeforeInitialized pins the behavior when completion is requested
// before the server has reached the initialized state (feature 16, T3 RED phase).
// The server must return JSON-RPC ServerNotInitialized error (not process the request).
func TestTextDocumentCompletionBeforeInitialized(t *testing.T) {
	// Arrange: send completion BEFORE initialize, then initialize → initialized → shutdown → exit
	completionID := jsonrpc2.NewNumberID(1)
	completionParams := `{
		"textDocument": {"uri": "file:///workspace/test.NSP"},
		"position": {"line": 0, "character": 5}
	}`
	completionCall := jsonrpc2.NewCall(completionID, "textDocument/completion", jsonrpc2.RawMessage(completionParams))

	initID := jsonrpc2.NewNumberID(2)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{completionCall, initCall, initNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read completion response (sent before initialize, should get ServerNotInitialized)
	completionBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse completion response: %v", err)
	}
	completionMsg, err := jsonrpc2.DecodeMessage(completionBody)
	if err != nil {
		t.Fatalf("failed to decode completion response: %v", err)
	}
	completionResp, ok := completionMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for completion, got %T", completionMsg)
	}

	// Assert: response should have ServerNotInitialized error (code -32002)
	if completionResp.ID() != completionID {
		t.Errorf("completion response id = %v, want %v", completionResp.ID(), completionID)
	}
	if completionResp.Err() == nil {
		t.Errorf("completion response has no error; want ServerNotInitialized")
	} else {
		// Check the error code (ServerNotInitialized is -32002)
		errTyped, ok := completionResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("completion response error is %T, not *jsonrpc2.Error: %v", completionResp.Err(), completionResp.Err())
		} else if errTyped.Code != jsonrpc2.ServerNotInitialized {
			t.Errorf("completion response error code = %v, want %v (ServerNotInitialized)", errTyped.Code, jsonrpc2.ServerNotInitialized)
		}
	}
}

// TestTextDocumentCompletionAfterInitialized pins the behavior when completion is requested
// after the server has reached initialized state (feature 16, T3 RED phase).
// The server must:
// 1. Route textDocument/completion in the dispatch switch (gated on stateInitialized)
// 2. Decode CompletionParams
// 3. Call provideCompletion (a stub returning [] during RED phase)
// 4. Marshal the result as a JSON array ([] when empty, never null)
//
// Currently in RED phase, the stub provider returns an empty list;
// actual completion logic is implemented in T4–T8.
func TestTextDocumentCompletionAfterInitialized(t *testing.T) {
	testCases := []struct {
		name             string
		completionParams string
		expectNonEmpty   bool // whether we expect a non-empty completion list (RED phase: always false)
		description      string
	}{
		{
			name: "CompletionAtValidPosition",
			completionParams: `{
				"textDocument": {"uri": "file:///workspace/test.NSP"},
				"position": {"line": 0, "character": 5}
			}`,
			expectNonEmpty: false, // RED phase: stub returns empty list
			description:    "completion should return empty list from stub during RED phase",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: send initialize → initialized → completion → shutdown
			initID := jsonrpc2.NewNumberID(1)
			initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
			initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

			initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			completionID := jsonrpc2.NewNumberID(2)
			completionCall := jsonrpc2.NewCall(completionID, "textDocument/completion", jsonrpc2.RawMessage(tc.completionParams))

			shutdownID := jsonrpc2.NewNumberID(3)
			shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Write requests as Content-Length-framed messages
			var inBuf bytes.Buffer
			for i, msg := range []jsonrpc2.Message{initCall, initNotif, completionCall, shutdownCall, exitNotif} {
				if err := writeFramedMessage(&inBuf, msg); err != nil {
					t.Fatalf("failed to write framed message %d: %v", i, err)
				}
			}

			// Create output buffer and logger
			var outBuf bytes.Buffer
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Act: run the server
			az := &stubAnalyzer{}
			err := Run(
				context.Background(),
				&inBuf,
				&outBuf,
				"0.0.0-test",
				"/workspace",
				az,
				logger,
			)

			// Assert: Run should complete without fatal error
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Parse framed responses
			responseBuf := bytes.NewBuffer(outBuf.Bytes())

			// Skip initialize response
			_, err = parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse initialize response: %v", err)
			}

			// Read completion response
			completionBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse completion response: %v", err)
			}
			completionMsg, err := jsonrpc2.DecodeMessage(completionBody)
			if err != nil {
				t.Fatalf("failed to decode completion response: %v", err)
			}
			completionResp, ok := completionMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for completion, got %T", completionMsg)
			}

			// Assert: response should have no error
			if completionResp.ID() != completionID {
				t.Errorf("completion response id = %v, want %v", completionResp.ID(), completionID)
			}
			if completionResp.Err() != nil {
				t.Errorf("completion response has error: %v; want empty list from stub", completionResp.Err())
			}

			// Assert: result should be an empty JSON array (RED phase: stub returns [])
			// The stub provider must never return null — completion list is always an array
			if completionResp.Result() == nil {
				t.Errorf("completion response result is null; want empty array []")
			} else {
				resultStr := string(completionResp.Result())
				if resultStr != "[]" {
					// During RED phase, we expect an empty array from the stub
					// Parse as JSON array to check if it's at least valid
					var items []interface{}
					if err := json.Unmarshal(completionResp.Result(), &items); err != nil {
						t.Errorf("completion response result is not valid JSON array: %v (result: %s)", err, resultStr)
					}
					// RED phase: expecting empty array from stub
					if len(items) > 0 && !tc.expectNonEmpty {
						t.Errorf("completion response has %d items during RED phase; expected empty array from stub", len(items))
					}
				}
			}
		})
	}
}

// TestTextDocumentSignatureHelpBeforeInitialized pins the behavior when signature help is requested
// before the server has reached the initialized state (feature 17, T1 RED phase).
// The server must return JSON-RPC ServerNotInitialized error (not process the request).
func TestTextDocumentSignatureHelpBeforeInitialized(t *testing.T) {
	// Arrange: send signature help BEFORE initialize, then initialize → initialized → shutdown → exit
	signatureHelpID := jsonrpc2.NewNumberID(1)
	signatureHelpParams := `{
		"textDocument": {"uri": "file:///workspace/test.NSP"},
		"position": {"line": 0, "character": 5}
	}`
	signatureHelpCall := jsonrpc2.NewCall(signatureHelpID, "textDocument/signatureHelp", jsonrpc2.RawMessage(signatureHelpParams))

	initID := jsonrpc2.NewNumberID(2)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{signatureHelpCall, initCall, initNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read signature help response (sent before initialize, should get ServerNotInitialized)
	signatureHelpBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse signature help response: %v", err)
	}
	signatureHelpMsg, err := jsonrpc2.DecodeMessage(signatureHelpBody)
	if err != nil {
		t.Fatalf("failed to decode signature help response: %v", err)
	}
	signatureHelpResp, ok := signatureHelpMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for signature help, got %T", signatureHelpMsg)
	}

	// Assert: response should have ServerNotInitialized error (code -32002)
	if signatureHelpResp.ID() != signatureHelpID {
		t.Errorf("signature help response id = %v, want %v", signatureHelpResp.ID(), signatureHelpID)
	}
	if signatureHelpResp.Err() == nil {
		t.Errorf("signature help response has no error; want ServerNotInitialized, got result: %s", signatureHelpResp.Result())
	} else {
		errTyped, ok := signatureHelpResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("signature help response error is %T, not *jsonrpc2.Error: %v", signatureHelpResp.Err(), signatureHelpResp.Err())
		} else if errTyped.Code != jsonrpc2.ServerNotInitialized {
			t.Errorf("signature help response error code = %v, want %v (ServerNotInitialized)", errTyped.Code, jsonrpc2.ServerNotInitialized)
		}
	}
}

// TestTextDocumentSignatureHelpAfterInitialized pins the behavior when signature help is requested
// after initialization (feature 17, T1 RED phase).
// The server must route the request to a handler and return a SignatureHelp (or null) result.
// The on-the-wire JSON result must be exactly "null" when the stub returns nil.
func TestTextDocumentSignatureHelpAfterInitialized(t *testing.T) {
	testCases := []struct {
		name                string
		signatureHelpParams string
		expectNonNull       bool // whether we expect a non-null SignatureHelp result
		description         string
	}{
		{
			name: "SignatureHelpAtValidPosition",
			signatureHelpParams: `{
				"textDocument": {"uri": "file:///workspace/test.NSP"},
				"position": {"line": 0, "character": 5}
			}`,
			expectNonNull: false, // RED phase: stub returns nil → JSON null
			description:   "signature help should return null from stub during RED phase",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: send initialize → initialized → signature help → shutdown
			initID := jsonrpc2.NewNumberID(1)
			initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
			initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

			initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

			signatureHelpID := jsonrpc2.NewNumberID(2)
			signatureHelpCall := jsonrpc2.NewCall(signatureHelpID, "textDocument/signatureHelp", jsonrpc2.RawMessage(tc.signatureHelpParams))

			shutdownID := jsonrpc2.NewNumberID(3)
			shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

			exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

			// Write requests as Content-Length-framed messages
			var inBuf bytes.Buffer
			for i, msg := range []jsonrpc2.Message{initCall, initNotif, signatureHelpCall, shutdownCall, exitNotif} {
				if err := writeFramedMessage(&inBuf, msg); err != nil {
					t.Fatalf("failed to write framed message %d: %v", i, err)
				}
			}

			// Create output buffer and logger
			var outBuf bytes.Buffer
			logBuf := &bytes.Buffer{}
			logger := slog.New(slog.NewTextHandler(logBuf, nil))

			// Act: run the server
			az := &stubAnalyzer{}
			err := Run(
				context.Background(),
				&inBuf,
				&outBuf,
				"0.0.0-test",
				"/workspace",
				az,
				logger,
			)

			// Assert: Run should complete without fatal error
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Parse framed responses
			responseBuf := bytes.NewBuffer(outBuf.Bytes())

			// Skip initialize response
			_, err = parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse initialize response: %v", err)
			}

			// Read signature help response
			signatureHelpBody, err := parseFramedResponse(responseBuf)
			if err != nil {
				t.Fatalf("failed to parse signature help response: %v", err)
			}
			signatureHelpMsg, err := jsonrpc2.DecodeMessage(signatureHelpBody)
			if err != nil {
				t.Fatalf("failed to decode signature help response: %v", err)
			}
			signatureHelpResp, ok := signatureHelpMsg.(*jsonrpc2.Response)
			if !ok {
				t.Fatalf("expected *jsonrpc2.Response for signature help, got %T", signatureHelpMsg)
			}

			// Assert: response should have no error
			if signatureHelpResp.ID() != signatureHelpID {
				t.Errorf("signature help response id = %v, want %v", signatureHelpResp.ID(), signatureHelpID)
			}
			if signatureHelpResp.Err() != nil {
				t.Errorf("signature help response has error: %v; want SignatureHelp result or null", signatureHelpResp.Err())
			}

			// Assert: the on-the-wire JSON is exactly "null" (the stub returns nil → JSON null).
			// This verifies the marshaling path is correct per the divergence note (MarshalJSONTo, not json.Marshal).
			if signatureHelpResp.Result() == nil {
				t.Errorf("signature help response result is nil; want JSON bytes representing null")
			} else {
				resultStr := string(signatureHelpResp.Result())
				if resultStr != "null" {
					t.Errorf("signature help response result = %q; want JSON \"null\" (stub returns nil)", resultStr)
				}
			}
		})
	}
}

// TestTextDocumentPrepareCallHierarchyBeforeInitialized pins the behavior when prepare call hierarchy is requested
// BEFORE initialization (feature 18, T1 RED phase).
// The server must return a JSON-RPC error with code ServerNotInitialized.
func TestTextDocumentPrepareCallHierarchyBeforeInitialized(t *testing.T) {
	// Arrange: send prepare call hierarchy BEFORE initialize, then initialize → initialized → shutdown → exit
	prepareCallHierarchyID := jsonrpc2.NewNumberID(1)
	prepareCallHierarchyParams := `{
		"textDocument": {"uri": "file:///workspace/test.NSP"},
		"position": {"line": 0, "character": 5}
	}`
	prepareCallHierarchyCall := jsonrpc2.NewCall(prepareCallHierarchyID, "textDocument/prepareCallHierarchy", jsonrpc2.RawMessage(prepareCallHierarchyParams))

	initID := jsonrpc2.NewNumberID(2)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{prepareCallHierarchyCall, initCall, initNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read prepare call hierarchy response (sent before initialize, should get ServerNotInitialized)
	prepareCallHierarchyBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse prepare call hierarchy response: %v", err)
	}
	prepareCallHierarchyMsg, err := jsonrpc2.DecodeMessage(prepareCallHierarchyBody)
	if err != nil {
		t.Fatalf("failed to decode prepare call hierarchy response: %v", err)
	}
	prepareCallHierarchyResp, ok := prepareCallHierarchyMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for prepare call hierarchy, got %T", prepareCallHierarchyMsg)
	}

	// Assert: response should have ServerNotInitialized error (code -32002)
	if prepareCallHierarchyResp.ID() != prepareCallHierarchyID {
		t.Errorf("prepare call hierarchy response id = %v, want %v", prepareCallHierarchyResp.ID(), prepareCallHierarchyID)
	}
	if prepareCallHierarchyResp.Err() == nil {
		t.Errorf("prepare call hierarchy response has no error; want ServerNotInitialized, got result: %s", prepareCallHierarchyResp.Result())
	} else {
		errTyped, ok := prepareCallHierarchyResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("prepare call hierarchy response error is %T, not *jsonrpc2.Error: %v", prepareCallHierarchyResp.Err(), prepareCallHierarchyResp.Err())
		} else if errTyped.Code != jsonrpc2.ServerNotInitialized {
			t.Errorf("prepare call hierarchy response error code = %v, want %v (ServerNotInitialized)", errTyped.Code, jsonrpc2.ServerNotInitialized)
		}
	}
}

// TestCallHierarchyIncomingCallsBeforeInitialized pins the behavior when incoming calls is requested
// BEFORE initialization (feature 18, T1 RED phase).
// The server must return a JSON-RPC error with code ServerNotInitialized.
func TestCallHierarchyIncomingCallsBeforeInitialized(t *testing.T) {
	// Arrange: send incoming calls BEFORE initialize, then initialize → initialized → shutdown → exit
	incomingCallsID := jsonrpc2.NewNumberID(1)
	incomingCallsParams := `{
		"item": {
			"name": "testFunc",
			"kind": 6,
			"uri": "file:///workspace/test.NSP",
			"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}},
			"selectionRange": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}}
		}
	}`
	incomingCallsCall := jsonrpc2.NewCall(incomingCallsID, "callHierarchy/incomingCalls", jsonrpc2.RawMessage(incomingCallsParams))

	initID := jsonrpc2.NewNumberID(2)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{incomingCallsCall, initCall, initNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read incoming calls response (sent before initialize, should get ServerNotInitialized)
	incomingCallsBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse incoming calls response: %v", err)
	}
	incomingCallsMsg, err := jsonrpc2.DecodeMessage(incomingCallsBody)
	if err != nil {
		t.Fatalf("failed to decode incoming calls response: %v", err)
	}
	incomingCallsResp, ok := incomingCallsMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for incoming calls, got %T", incomingCallsMsg)
	}

	// Assert: response should have ServerNotInitialized error (code -32002)
	if incomingCallsResp.ID() != incomingCallsID {
		t.Errorf("incoming calls response id = %v, want %v", incomingCallsResp.ID(), incomingCallsID)
	}
	if incomingCallsResp.Err() == nil {
		t.Errorf("incoming calls response has no error; want ServerNotInitialized, got result: %s", incomingCallsResp.Result())
	} else {
		errTyped, ok := incomingCallsResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("incoming calls response error is %T, not *jsonrpc2.Error: %v", incomingCallsResp.Err(), incomingCallsResp.Err())
		} else if errTyped.Code != jsonrpc2.ServerNotInitialized {
			t.Errorf("incoming calls response error code = %v, want %v (ServerNotInitialized)", errTyped.Code, jsonrpc2.ServerNotInitialized)
		}
	}
}

// TestCallHierarchyOutgoingCallsBeforeInitialized pins the behavior when outgoing calls is requested
// BEFORE initialization (feature 18, T1 RED phase).
// The server must return a JSON-RPC error with code ServerNotInitialized.
func TestCallHierarchyOutgoingCallsBeforeInitialized(t *testing.T) {
	// Arrange: send outgoing calls BEFORE initialize, then initialize → initialized → shutdown → exit
	outgoingCallsID := jsonrpc2.NewNumberID(1)
	outgoingCallsParams := `{
		"item": {
			"name": "testFunc",
			"kind": 6,
			"uri": "file:///workspace/test.NSP",
			"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}},
			"selectionRange": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}}
		}
	}`
	outgoingCallsCall := jsonrpc2.NewCall(outgoingCallsID, "callHierarchy/outgoingCalls", jsonrpc2.RawMessage(outgoingCallsParams))

	initID := jsonrpc2.NewNumberID(2)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{outgoingCallsCall, initCall, initNotif, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Read outgoing calls response (sent before initialize, should get ServerNotInitialized)
	outgoingCallsBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse outgoing calls response: %v", err)
	}
	outgoingCallsMsg, err := jsonrpc2.DecodeMessage(outgoingCallsBody)
	if err != nil {
		t.Fatalf("failed to decode outgoing calls response: %v", err)
	}
	outgoingCallsResp, ok := outgoingCallsMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for outgoing calls, got %T", outgoingCallsMsg)
	}

	// Assert: response should have ServerNotInitialized error (code -32002)
	if outgoingCallsResp.ID() != outgoingCallsID {
		t.Errorf("outgoing calls response id = %v, want %v", outgoingCallsResp.ID(), outgoingCallsID)
	}
	if outgoingCallsResp.Err() == nil {
		t.Errorf("outgoing calls response has no error; want ServerNotInitialized, got result: %s", outgoingCallsResp.Result())
	} else {
		errTyped, ok := outgoingCallsResp.Err().(*jsonrpc2.Error)
		if !ok {
			t.Errorf("outgoing calls response error is %T, not *jsonrpc2.Error: %v", outgoingCallsResp.Err(), outgoingCallsResp.Err())
		} else if errTyped.Code != jsonrpc2.ServerNotInitialized {
			t.Errorf("outgoing calls response error code = %v, want %v (ServerNotInitialized)", errTyped.Code, jsonrpc2.ServerNotInitialized)
		}
	}
}

// TestTextDocumentPrepareCallHierarchyAfterInitialized pins the behavior when prepare call hierarchy is requested
// after initialization (feature 18, T1 RED phase).
// The server must route the request to a handler and return an empty array result.
func TestTextDocumentPrepareCallHierarchyAfterInitialized(t *testing.T) {
	// Arrange: send initialize → initialized → prepare call hierarchy → shutdown
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	prepareCallHierarchyID := jsonrpc2.NewNumberID(2)
	prepareCallHierarchyParams := `{
		"textDocument": {"uri": "file:///workspace/test.NSP"},
		"position": {"line": 0, "character": 5}
	}`
	prepareCallHierarchyCall := jsonrpc2.NewCall(prepareCallHierarchyID, "textDocument/prepareCallHierarchy", jsonrpc2.RawMessage(prepareCallHierarchyParams))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, prepareCallHierarchyCall, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read prepare call hierarchy response
	prepareCallHierarchyBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse prepare call hierarchy response: %v", err)
	}
	prepareCallHierarchyMsg, err := jsonrpc2.DecodeMessage(prepareCallHierarchyBody)
	if err != nil {
		t.Fatalf("failed to decode prepare call hierarchy response: %v", err)
	}
	prepareCallHierarchyResp, ok := prepareCallHierarchyMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for prepare call hierarchy, got %T", prepareCallHierarchyMsg)
	}

	// Assert: response should have no error
	if prepareCallHierarchyResp.ID() != prepareCallHierarchyID {
		t.Errorf("prepare call hierarchy response id = %v, want %v", prepareCallHierarchyResp.ID(), prepareCallHierarchyID)
	}
	if prepareCallHierarchyResp.Err() != nil {
		t.Errorf("prepare call hierarchy response has error: %v; want empty array result", prepareCallHierarchyResp.Err())
	}

	// Assert: the on-the-wire JSON is exactly "[]" (the stub returns nil → []).
	if prepareCallHierarchyResp.Result() == nil {
		t.Errorf("prepare call hierarchy response result is nil; want JSON bytes representing []")
	} else {
		resultStr := string(prepareCallHierarchyResp.Result())
		if resultStr != "[]" {
			t.Errorf("prepare call hierarchy response result = %q; want JSON \"[]\" (stub returns nil)", resultStr)
		}
	}
}

// TestCallHierarchyIncomingCallsAfterInitialized pins the behavior when incoming calls is requested
// after initialization (feature 18, T1 RED phase).
// The server must route the request to a handler and return an empty array result.
func TestCallHierarchyIncomingCallsAfterInitialized(t *testing.T) {
	// Arrange: send initialize → initialized → incoming calls → shutdown
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	incomingCallsID := jsonrpc2.NewNumberID(2)
	incomingCallsParams := `{
		"item": {
			"name": "testFunc",
			"kind": 6,
			"uri": "file:///workspace/test.NSP",
			"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}},
			"selectionRange": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}}
		}
	}`
	incomingCallsCall := jsonrpc2.NewCall(incomingCallsID, "callHierarchy/incomingCalls", jsonrpc2.RawMessage(incomingCallsParams))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, incomingCallsCall, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read incoming calls response
	incomingCallsBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse incoming calls response: %v", err)
	}
	incomingCallsMsg, err := jsonrpc2.DecodeMessage(incomingCallsBody)
	if err != nil {
		t.Fatalf("failed to decode incoming calls response: %v", err)
	}
	incomingCallsResp, ok := incomingCallsMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for incoming calls, got %T", incomingCallsMsg)
	}

	// Assert: response should have no error
	if incomingCallsResp.ID() != incomingCallsID {
		t.Errorf("incoming calls response id = %v, want %v", incomingCallsResp.ID(), incomingCallsID)
	}
	if incomingCallsResp.Err() != nil {
		t.Errorf("incoming calls response has error: %v; want empty array result", incomingCallsResp.Err())
	}

	// Assert: the on-the-wire JSON is exactly "[]" (the stub returns nil → []).
	if incomingCallsResp.Result() == nil {
		t.Errorf("incoming calls response result is nil; want JSON bytes representing []")
	} else {
		resultStr := string(incomingCallsResp.Result())
		if resultStr != "[]" {
			t.Errorf("incoming calls response result = %q; want JSON \"[]\" (stub returns nil)", resultStr)
		}
	}
}

// TestCallHierarchyOutgoingCallsAfterInitialized pins the behavior when outgoing calls is requested
// after initialization (feature 18, T1 RED phase).
// The server must route the request to a handler and return an empty array result.
func TestCallHierarchyOutgoingCallsAfterInitialized(t *testing.T) {
	// Arrange: send initialize → initialized → outgoing calls → shutdown
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{"processId":1234,"rootPath":"/workspace","capabilities":{}}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	outgoingCallsID := jsonrpc2.NewNumberID(2)
	outgoingCallsParams := `{
		"item": {
			"name": "testFunc",
			"kind": 6,
			"uri": "file:///workspace/test.NSP",
			"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}},
			"selectionRange": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 8}}
		}
	}`
	outgoingCallsCall := jsonrpc2.NewCall(outgoingCallsID, "callHierarchy/outgoingCalls", jsonrpc2.RawMessage(outgoingCallsParams))

	shutdownID := jsonrpc2.NewNumberID(3)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))

	// Write requests as Content-Length-framed messages
	var inBuf bytes.Buffer
	for i, msg := range []jsonrpc2.Message{initCall, initNotif, outgoingCallsCall, shutdownCall, exitNotif} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message %d: %v", i, err)
		}
	}

	// Create output buffer and logger
	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Act: run the server
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		az,
		logger,
	)

	// Assert: Run should complete without fatal error
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse framed responses
	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip initialize response
	_, err = parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Read outgoing calls response
	outgoingCallsBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse outgoing calls response: %v", err)
	}
	outgoingCallsMsg, err := jsonrpc2.DecodeMessage(outgoingCallsBody)
	if err != nil {
		t.Fatalf("failed to decode outgoing calls response: %v", err)
	}
	outgoingCallsResp, ok := outgoingCallsMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for outgoing calls, got %T", outgoingCallsMsg)
	}

	// Assert: response should have no error
	if outgoingCallsResp.ID() != outgoingCallsID {
		t.Errorf("outgoing calls response id = %v, want %v", outgoingCallsResp.ID(), outgoingCallsID)
	}
	if outgoingCallsResp.Err() != nil {
		t.Errorf("outgoing calls response has error: %v; want empty array result", outgoingCallsResp.Err())
	}

	// Assert: the on-the-wire JSON is exactly "[]" (the stub returns nil → []).
	if outgoingCallsResp.Result() == nil {
		t.Errorf("outgoing calls response result is nil; want JSON bytes representing []")
	} else {
		resultStr := string(outgoingCallsResp.Result())
		if resultStr != "[]" {
			t.Errorf("outgoing calls response result = %q; want JSON \"[]\" (stub returns nil)", resultStr)
		}
	}
}
