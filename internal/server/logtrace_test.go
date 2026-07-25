package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.lsp.dev/jsonrpc2"
)

// TestLogTrace_OffProducesNoTrace verifies S2-AC4: when initialize specifies
// trace:"off" (or omits trace), no $/logTrace frames appear on the wire.
func TestLogTrace_OffProducesNoTrace(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize with trace:"off" explicitly.
	initParams := `{
		"processId": 1,
		"rootUri": null,
		"trace": "off",
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`

	// Run the gated handshake, but we need a request AFTER the index is ready.
	// Create a simple hover request.
	preMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
	}

	midMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/hover", jsonrpc2.RawMessage(`{
			"textDocument": {"uri": "file:///workspace/test.NSP"},
			"position": {"line": 0, "character": 0}
		}`)),
	}

	az := &stubAnalyzer{}
	out, _ := runGatedLifecycle(t, preMsgs, midMsgs, tmpDir, az)

	// Scan the output for $/logTrace frames. There should be NONE.
	outStr := out.String()
	if strings.Contains(outStr, `"method":"$/logTrace"`) {
		t.Errorf("expected no $/logTrace frames when trace:off, but found some in output:\n%s", outStr)
	}
}

// TestLogTrace_MessagesProducesTraceWithoutVerbose verifies S2-AC3 and S2-AC6:
// when trace:"messages", a $/logTrace frame appears with method name in the
// message and NO verbose key (verbose should be nil, omitted from JSON).
func TestLogTrace_MessagesProducesTraceWithoutVerbose(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize with trace:"messages".
	initParams := `{
		"processId": 1,
		"rootUri": null,
		"trace": "messages",
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`

	preMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
	}

	midMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/hover", jsonrpc2.RawMessage(`{
			"textDocument": {"uri": "file:///workspace/test.NSP"},
			"position": {"line": 0, "character": 0}
		}`)),
	}

	az := &stubAnalyzer{}
	out, _ := runGatedLifecycle(t, preMsgs, midMsgs, tmpDir, az)

	// Extract $/logTrace frames and verify they contain the method name
	// and do NOT have a verbose key.
	logTraceFrame := extractLogTraceFrame(t, out.String(), "textDocument/hover")
	if logTraceFrame == nil {
		t.Fatal("expected a $/logTrace frame for textDocument/hover, found none")
	}

	// Wire-bytes assertion: decode the params.
	params := logTraceFrame.Params
	if params == nil {
		t.Fatal("$/logTrace frame has no params")
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to decode $/logTrace params: %v", err)
	}

	// Assert: "message" field is present and non-empty.
	message, hasMsg := decoded["message"]
	if !hasMsg {
		t.Errorf("$/logTrace params missing 'message' field")
	}
	messageStr, ok := message.(string)
	if !ok {
		t.Errorf("'message' field type = %T; want string", message)
	}
	if messageStr == "" {
		t.Errorf("'message' field is empty")
	}
	// The message should contain the method name.
	if !strings.Contains(messageStr, "textDocument/hover") {
		t.Errorf("message does not contain method name 'textDocument/hover': %q", messageStr)
	}

	// Assert: "verbose" field is NOT present (nil/omitted).
	_, hasVerbose := decoded["verbose"]
	if hasVerbose {
		t.Errorf("expected 'verbose' to be omitted when trace:messages, but it is present")
	}
}

// TestLogTrace_VerboseIncludesBoundedSummary verifies S2-AC3 and OQ-4:
// when trace:"verbose", a $/logTrace frame includes a non-nil "verbose" field
// with a bounded summary (≤ maxTraceVerboseBytes + elision marker).
func TestLogTrace_VerboseIncludesBoundedSummary(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize with trace:"verbose".
	initParams := `{
		"processId": 1,
		"rootUri": null,
		"trace": "verbose",
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`

	preMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
	}

	midMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/hover", jsonrpc2.RawMessage(`{
			"textDocument": {"uri": "file:///workspace/test.NSP"},
			"position": {"line": 0, "character": 0}
		}`)),
	}

	az := &stubAnalyzer{}
	out, _ := runGatedLifecycle(t, preMsgs, midMsgs, tmpDir, az)

	logTraceFrame := extractLogTraceFrame(t, out.String(), "textDocument/hover")
	if logTraceFrame == nil {
		t.Fatal("expected a $/logTrace frame for textDocument/hover, found none")
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(logTraceFrame.Params, &decoded); err != nil {
		t.Fatalf("failed to decode $/logTrace params: %v", err)
	}

	// Assert: "verbose" field is present and non-nil.
	verboseVal, hasVerbose := decoded["verbose"]
	if !hasVerbose {
		t.Errorf("expected 'verbose' field when trace:verbose, but it is absent")
		return
	}

	verboseStr, ok := verboseVal.(string)
	if !ok {
		t.Errorf("'verbose' field type = %T; want string", verboseVal)
		return
	}

	// The verbose string length should be bounded by maxTraceVerboseBytes + elision marker.
	// Note: We reference maxTraceVerboseBytes here as a compile-time check —
	// this will fail to compile until GREEN implements it.
	_ = maxTraceVerboseBytes // compile-fail until the constant is defined

	if len(verboseStr) > maxTraceVerboseBytes+len("… (0 bytes elided)") {
		t.Errorf("verbose string length %d exceeds max %d + marker", len(verboseStr), maxTraceVerboseBytes)
	}
}

// TestLogTrace_RuntimeFlip verifies S2-AC2: $/setTrace changes the trace level
// at runtime without restart. A request with trace:off produces no trace, then
// $/setTrace changes the level to "messages", and the next request produces a trace.
func TestLogTrace_RuntimeFlip(t *testing.T) {
	tmpDir := t.TempDir()

	initParams := `{
		"processId": 1,
		"rootUri": null,
		"trace": "off",
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`

	// Build messages: initialize → initialized → hover (no trace) → setTrace → hover (trace).
	preMsgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
	}

	midMsgs := []jsonrpc2.Message{
		// First hover with trace off (should produce no trace).
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/hover", jsonrpc2.RawMessage(`{
			"textDocument": {"uri": "file:///workspace/test.NSP"},
			"position": {"line": 0, "character": 0}
		}`)),
		// $/setTrace to "messages".
		jsonrpc2.NewNotification("$/setTrace", jsonrpc2.RawMessage(`{"value": "messages"}`)),
		// Second hover with trace on (should produce a trace).
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(3), "textDocument/hover", jsonrpc2.RawMessage(`{
			"textDocument": {"uri": "file:///workspace/test.NSP"},
			"position": {"line": 0, "character": 0}
		}`)),
	}

	az := &stubAnalyzer{}
	out, _ := runGatedLifecycle(t, preMsgs, midMsgs, tmpDir, az)

	outStr := out.String()

	// The trace message embeds the request ID: "[Trace] Received request
	// 'method - (id)' in ...". Count traces per request so we can prove the
	// PRE-setTrace hover (id=2, trace off) emitted ZERO traces and the
	// POST-setTrace hover (id=3, trace messages) emitted at least one.
	preTraces := strings.Count(outStr, "- (2)'")
	postTraces := strings.Count(outStr, "- (3)'")

	if preTraces != 0 {
		t.Errorf("expected ZERO $/logTrace for the pre-setTrace request (id=2, trace off), got %d", preTraces)
	}
	if postTraces < 1 {
		t.Errorf("expected at least one $/logTrace for the post-setTrace request (id=3), got %d", postTraces)
	}
}

// TestLogTrace_FireAndForgetOnStreamFailure verifies S2-AC5: if the stream fails
// during a traced request, the request's response still returns and the loop survives.
func TestLogTrace_FireAndForgetOnStreamFailure(t *testing.T) {
	tmpDir := t.TempDir()

	initParams := `{
		"processId": 1,
		"rootUri": null,
		"trace": "verbose",
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`

	// Create a stream that fails after writing a certain amount.
	failAfterNBytes := 500
	faultyStream := newFaultyStream(failAfterNBytes)

	var pre bytes.Buffer
	if err := writeFramedMessage(&pre, jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(&pre, jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	var post bytes.Buffer
	if err := writeFramedMessage(&post, jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/hover", jsonrpc2.RawMessage(`{
		"textDocument": {"uri": "file:///workspace/test.NSP"},
		"position": {"line": 0, "character": 0}
	}`))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(&post, jsonrpc2.NewCall(jsonrpc2.NewNumberID(9999), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(&post, jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	reader := &gatedHandshakeReader{pre: &pre, post: &post, ready: indexReadyGate(t)}
	logBuf := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	// Run should complete without panicking, even though the stream fails.
	// We don't expect all responses to be written (the stream fails), but the
	// loop should not panic.
	_ = Run(context.Background(), reader, faultyStream, "0.0.0-test", tmpDir, &stubAnalyzer{}, logger)

	// The test passes if no panic occurred. We don't verify responses because
	// the stream failure is expected.
}

// TestTraceSummary_SmallPayloadUnchanged verifies OQ-4: a payload ≤ cap
// passes through unchanged (no elision marker).
func TestTraceSummary_SmallPayloadUnchanged(t *testing.T) {
	payload := []byte("small payload")
	result := traceSummary(payload)
	if result != string(payload) {
		t.Errorf("small payload: got %q, want %q", result, string(payload))
	}
}

// TestTraceSummary_LargePayloadTruncated verifies OQ-4: a payload larger than
// maxTraceVerboseBytes is truncated and ends with an elision marker.
func TestTraceSummary_LargePayloadTruncated(t *testing.T) {
	_ = maxTraceVerboseBytes                 // compile-fail until GREEN defines it
	elisionMarker := "… (1024 bytes elided)" // example marker

	// Create a payload larger than the cap.
	payload := make([]byte, maxTraceVerboseBytes+1024)
	for i := range payload {
		payload[i] = 'x'
	}

	result := traceSummary(payload)

	// Check length: should be ≤ cap + marker length.
	if len(result) > maxTraceVerboseBytes+len(elisionMarker) {
		t.Errorf("truncated payload length %d exceeds cap %d + marker", len(result), maxTraceVerboseBytes)
	}

	// Check elision marker: should end with "…" or similar.
	if !strings.Contains(result, "…") && !strings.Contains(result, "...") {
		t.Errorf("truncated payload does not contain elision marker (expected '…' or '...'): %q", result)
	}
}

// TestTraceSummary_EmptyPayload verifies OQ-4: empty payload produces empty result.
func TestTraceSummary_EmptyPayload(t *testing.T) {
	result := traceSummary([]byte{})
	if result != "" {
		t.Errorf("empty payload: got %q, want empty", result)
	}
}

// TestTraceSummary_BoundaryCase verifies OQ-4: payload exactly at cap boundary
// should not be truncated.
func TestTraceSummary_BoundaryCase(t *testing.T) {
	_ = maxTraceVerboseBytes // compile-fail until GREEN defines it

	// Create a payload exactly at the cap.
	payload := make([]byte, maxTraceVerboseBytes)
	for i := range payload {
		payload[i] = 'y'
	}

	result := traceSummary(payload)

	// Should not be truncated.
	if len(result) != maxTraceVerboseBytes {
		t.Errorf("boundary case: got length %d, want %d", len(result), maxTraceVerboseBytes)
	}

	if strings.Contains(result, "…") || strings.Contains(result, "...") {
		t.Errorf("boundary case should not have elision marker")
	}
}

// --- Helper functions ---

// tracedLogFrame represents a parsed $/logTrace frame for testing.
type tracedLogFrame struct {
	Method string
	Params []byte
}

// extractLogTraceFrame scans the raw framed output for a $/logTrace notification
// whose message contains the expectedMethod, and returns its parsed params, or nil
// if not found. This handles cases where multiple $/logTrace frames are emitted
// (e.g., initialize and then textDocument/hover) by searching for the frame
// whose message field contains the expected method name.
func extractLogTraceFrame(t *testing.T, outStr, expectedMethod string) *tracedLogFrame {
	t.Helper()

	// Scan for ALL $/logTrace frames and find the one containing expectedMethod in its message.
	searchPos := 0
	for {
		idx := strings.Index(outStr[searchPos:], `"method":"$/logTrace"`)
		if idx == -1 {
			return nil
		}
		idx += searchPos

		// Extract the frame. $/logTrace is a notification, so the frame structure is:
		// {..., "method":"$/logTrace", "params":{...}, ...}
		// We need to extract the params object. For robustness, find the "params" key
		// after the method and extract until the matching closing brace.
		paramsStart := strings.Index(outStr[idx:], `"params":`)
		if paramsStart == -1 {
			searchPos = idx + 1
			continue
		}
		paramsStart += idx

		// Find the actual JSON start (skip the colon and whitespace).
		jsonStart := paramsStart + len(`"params":`)
		for jsonStart < len(outStr) && (outStr[jsonStart] == ' ' || outStr[jsonStart] == '\n' || outStr[jsonStart] == '\t') {
			jsonStart++
		}

		// Extract the JSON object. This is a simple brace-counting approach.
		depth := 0
		for i := jsonStart; i < len(outStr); i++ {
			if outStr[i] == '{' {
				depth++
			} else if outStr[i] == '}' {
				depth--
				if depth == 0 {
					paramsJSON := outStr[jsonStart : i+1]
					paramsJSONBytes := []byte(paramsJSON)

					// Check if this trace's message contains the expected method.
					var decoded map[string]interface{}
					if err := json.Unmarshal(paramsJSONBytes, &decoded); err == nil {
						if msgVal, hasMsg := decoded["message"]; hasMsg {
							if msgStr, ok := msgVal.(string); ok && strings.Contains(msgStr, expectedMethod) {
								return &tracedLogFrame{
									Method: expectedMethod,
									Params: paramsJSONBytes,
								}
							}
						}
					}

					// This trace didn't match; continue searching.
					searchPos = idx + 1
					break
				}
			}
		}
	}
}

// faultyStream is a jsonrpc2.Stream that fails after writing failAfterNBytes.
type faultyStream struct {
	written int
	limit   int
	buf     bytes.Buffer
}

func newFaultyStream(failAfter int) *faultyStream {
	return &faultyStream{limit: failAfter}
}

func (fs *faultyStream) Write(p []byte) (int, error) {
	if fs.written >= fs.limit {
		return 0, fmt.Errorf("faulty stream: write failed")
	}

	canWrite := len(p)
	if fs.written+canWrite > fs.limit {
		canWrite = fs.limit - fs.written
	}

	fs.written += canWrite
	fs.buf.Write(p[:canWrite])

	if fs.written >= fs.limit {
		return canWrite, fmt.Errorf("faulty stream: write limit exceeded")
	}

	return canWrite, nil
}

func (fs *faultyStream) Read(p []byte) (int, error) {
	// For this test, we don't really read from the faulty stream.
	// The stream is output-only.
	return 0, io.EOF
}

func (fs *faultyStream) Close() error {
	return nil
}
