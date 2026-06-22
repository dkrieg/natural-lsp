package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// stubAnalyzer is a test double implementing analysis.Analyzer with a no-op Analyze method.
type stubAnalyzer struct{}

func (sa *stubAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	return model.FileAnalysis{ObjectType: model.ObjectUnknown}, nil
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
	cfg := config.Defaults()
	az := &stubAnalyzer{}
	err = Run(
		context.Background(),
		inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		cfg,
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
func writeFramedMessage(buf *bytes.Buffer, msg jsonrpc2.Message) error {
	encoded, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return err
	}
	contentLen := len(encoded)
	_, err = buf.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLen))
	if err != nil {
		return err
	}
	_, err = buf.Write(encoded)
	return err
}

// parseFramedResponse extracts one framed JSON-RPC response from buf and returns the body bytes.
// It assumes buf starts with a valid Content-Length header and returns the JSON body.
// After calling this, buf is advanced past the response (including header).
func parseFramedResponse(buf *bytes.Buffer) ([]byte, error) {
	output := buf.String()
	// Find the blank line that separates header from body
	idx := strings.Index(output, "\r\n\r\n")
	if idx == -1 {
		return nil, fmt.Errorf("no blank line separating header and body")
	}
	headerEnd := idx + 4 // account for "\r\n\r\n"

	// Parse Content-Length from the header
	headerLines := strings.Split(output[:idx], "\r\n")
	if len(headerLines) == 0 {
		return nil, fmt.Errorf("empty header")
	}
	contentLengthLine := headerLines[0]
	if !strings.HasPrefix(contentLengthLine, "Content-Length: ") {
		return nil, fmt.Errorf("first line is not Content-Length header")
	}
	lengthStr := strings.TrimPrefix(contentLengthLine, "Content-Length: ")
	contentLen, err := strconv.Atoi(lengthStr)
	if err != nil {
		return nil, fmt.Errorf("invalid Content-Length: %v", err)
	}

	// Extract the body
	bodyEnd := headerEnd + contentLen
	if bodyEnd > len(output) {
		return nil, fmt.Errorf("response too short; declared %d bytes but only %d available", contentLen, len(output)-headerEnd)
	}
	body := output[headerEnd:bodyEnd]

	// Advance buf to remove this response
	remaining := output[bodyEnd:]
	buf.Reset()
	buf.WriteString(remaining)

	return []byte(body), nil
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
			cfg := config.Defaults()
			az := &stubAnalyzer{}
			// Run takes separate Reader and Writer, not ReadWriteCloser.
			err := Run(
				context.Background(),
				&reqBuf,
				&outBuf,
				"0.0.0-test",
				"/workspace",
				cfg,
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
// ADR-009: textDocumentSync = Full with openClose: true.
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
			cfg := config.Defaults()
			az := &stubAnalyzer{}
			err := Run(
				context.Background(),
				&reqBuf,
				&outBuf,
				"0.1.0-test", // injected version
				"/workspace",
				cfg,
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

				// Assert: no feature provider flags are set (FR-41, NFR-11).
				// This is a regression guard — when features 09–13 add providers, this assertion will catch the change.
				providerFlags := []string{
					"definitionProvider",
					"referencesProvider",
					"hoverProvider",
					"documentSymbolProvider",
					"workspaceSymbolProvider",
					"codeLensProvider",
				}
				for _, flag := range providerFlags {
					if val, exists := caps[flag]; exists && val != nil && val != false {
						t.Errorf("%s is advertised (%v); want nil/false (not yet implemented)", flag, val)
					}
				}
			}
		})
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
				cfg := config.Defaults()
				az := &stubAnalyzer{}
				err := Run(
					runCtx,
					&inBuf,
					&outBuf,
					"0.0.0-test",
					"/workspace",
					cfg,
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
		cfg := config.Defaults()
		az := &stubAnalyzer{}
		runDone <- Run(
			context.Background(),
			blockingReader,
			&outBuf,
			"0.0.0-test",
			"/workspace",
			cfg,
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
		cfg := config.Defaults()
		az := &stubAnalyzer{}
		runErrChan <- Run(
			ctx,
			blockingReader,
			&outBuf,
			"0.0.0-test",
			"/workspace",
			cfg,
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
	cfg := config.Defaults()
	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		"/workspace",
		cfg,
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
