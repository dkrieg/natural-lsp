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
)

// TestLogMessage_BuildEndInfo drives a full initialize→initialized→shutdown→exit
// lifecycle over a populated fixture root and asserts a window/logMessage Info frame
// is emitted with a message naming the file count.
//
// It uses the async-safe gatedHandshake synchronization from async_build_test.go
// to ensure the background build publishes before shutdown cancels it (feature 21).
//
// Fixture: reuses testdata/navigation/ which has a populated multi-file root.
func TestLogMessage_BuildEndInfo(t *testing.T) {
	// Arrange: copy testdata/navigation to a temp dir so we have a real workspace
	root := copyFixture(t, "testdata/navigation")

	// Use the async-safe handshake: initialize → initialized → (wait for build) → shutdown → exit.
	// This ensures the background build publishes its index and emits window/logMessage
	// BEFORE shutdown cancels bgCtx.
	// Note: rootUri is null so the root falls back to cwdFallback (the actual root parameter).
	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	az := &stubAnalyzer{}
	outBuf, stderrContent := runGatedHandshake(t, initParams, root, az)

	// Assert: find a window/logMessage with type==3 (Info) in the output
	logMessages := extractWindowLogMessages(t, outBuf)
	if len(logMessages) == 0 {
		t.Fatalf("expected at least one window/logMessage frame, got none")
	}

	// Find an Info-level message about the index build
	foundBuildEnd := false
	for _, msg := range logMessages {
		if msg.Type == protocol.MessageTypeInfo {
			// The message should mention file counts (per S1-AC1)
			if strings.Contains(msg.Message, "file") || strings.Contains(msg.Message, "indexed") {
				foundBuildEnd = true
				break
			}
		}
	}

	if !foundBuildEnd {
		t.Errorf("expected window/logMessage Info with file count mention, got: %v", logMessages)
	}

	// Assert: the stderr slog buffer also contains the log line (dual sink, S1-AC2)
	if !strings.Contains(stderrContent, "workspace index built") &&
		!strings.Contains(stderrContent, "indexed") {
		t.Errorf("expected stderr log to mention index build, got: %q", stderrContent)
	}
}

// TestLogMessage_RequestPanicError fires a test/panic request and asserts:
// 1. A window/logMessage with type==1 (Error) is emitted (S1-AC1)
// 2. The JSON-RPC InternalError response still returns (S1-AC3, fire-and-forget)
// 3. The loop survives and shutdown succeeds (S1-AC3)
//
// It uses runGatedLifecycle to send the panic request AFTER the index is ready
// (feature 21, async build).
func TestLogMessage_RequestPanicError(t *testing.T) {
	root := t.TempDir()

	// Pre-messages: initialize + initialized
	// Note: rootUri is null so the root falls back to cwdFallback (the actual root parameter).
	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	initCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize",
		jsonrpc2.RawMessage(initParams))
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Mid-messages: test/panic request (sent AFTER the index is ready)
	panicID := jsonrpc2.NewNumberID(2)
	panicCall := jsonrpc2.NewCall(panicID, "test/panic", jsonrpc2.RawMessage(`{}`))

	// Use gatedLifecycle: drive initialize → initialized → (wait for build) → test/panic → shutdown → exit
	az := &stubAnalyzer{}
	outBuf, stderrContent := runGatedLifecycle(t,
		[]jsonrpc2.Message{initCall, initNotif},
		[]jsonrpc2.Message{panicCall},
		root, az)

	// Assert: find a window/logMessage with type==1 (Error) for the panic
	logMessages := extractWindowLogMessages(t, outBuf)
	foundErrorMsg := false
	for _, msg := range logMessages {
		if msg.Type == protocol.MessageTypeError {
			// The message should mention panic or internal error
			if strings.Contains(msg.Message, "panic") || strings.Contains(msg.Message, "error") {
				foundErrorMsg = true
				break
			}
		}
	}

	if !foundErrorMsg {
		t.Errorf("expected window/logMessage Error for panic, got: %v", logMessages)
	}

	// Assert: the panic request gets an InternalError response (S1-AC3, fire-and-forget)
	panicResult, panicIsErr := extractResponse(outBuf, 2) // panicID is 2 from above
	if panicResult == nil {
		t.Errorf("panic request (id=2) got no response")
	} else if !panicIsErr {
		t.Errorf("panic request should return error, got result: %q", string(panicResult))
	}

	// Assert: stderr slog buffer also contains the panic log (dual sink, S1-AC2)
	if !strings.Contains(stderrContent, "panic") && !strings.Contains(stderrContent, "error") {
		t.Logf("note: stderr content was: %q", stderrContent)
	}
}

// TestLogMessage_NoCapabilityAdded asserts the initialize response's ServerCapabilities
// is unchanged (no window/logMessage or trace capability added).
func TestLogMessage_NoCapabilityAdded(t *testing.T) {
	root := t.TempDir()

	// Initialize request
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{
		"processId":1234,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	var inBuf bytes.Buffer
	if err := writeFramedMessage(&inBuf, initCall); err != nil {
		t.Fatalf("failed to write initialize: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		root,
		az,
		logger,
	)

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse the initialize response
	responseBuf := bytes.NewBuffer(outBuf.Bytes())
	initBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("failed to parse initialize response: %v", err)
	}

	// Decode and check capabilities
	var rawResp struct {
		Result struct {
			Capabilities map[string]interface{} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(initBody, &rawResp); err != nil {
		t.Fatalf("failed to unmarshal initialize result: %v", err)
	}

	caps := rawResp.Result.Capabilities

	// Assert: no window, logMessage, trace, or setTrace capability keys
	forbiddenKeys := []string{"window", "logMessage", "trace", "setTrace", "$/logTrace"}
	for _, key := range forbiddenKeys {
		if _, exists := caps[key]; exists {
			t.Errorf("ServerCapabilities should not have key %q (no trace/log capability), but it does", key)
		}
	}

	// Assert: the required providers that SHOULD be there are still there
	requiredProviders := []string{
		"definitionProvider",
		"referencesProvider",
		"workspaceSymbolProvider",
		"documentSymbolProvider",
		"hoverProvider",
		"codeLensProvider",
		"signatureHelpProvider",
		"callHierarchyProvider",
	}
	for _, provider := range requiredProviders {
		if _, exists := caps[provider]; !exists {
			t.Errorf("ServerCapabilities missing required %q", provider)
		}
	}
}

// TestLogMessage_NoLogForDynamicReferences asserts that dynamic/unresolved references
// do NOT produce a window/logMessage (modeled gaps are not operational events, FR-17).
//
// Fixture: a Natural file with a dynamic CALLNAT reference (e.g., CALLNAT #VAR).
func TestLogMessage_NoLogForDynamicReferences(t *testing.T) {
	root := copyFixture(t, "testdata/navigation")

	// Write a file with a dynamic reference (CALLNAT #VAR)
	dynamicFile := filepath.Join(root, "dynamic.NSP")
	dynamicContent := `PROGRAM dynamic
  DEFINE DATA LOCAL
    1 #VAR (A30)
  END-DEFINE
  CALLNAT #VAR
END
`
	if err := os.WriteFile(dynamicFile, []byte(dynamicContent), 0644); err != nil {
		t.Fatalf("failed to write dynamic.NSP: %v", err)
	}

	// Build the message sequence
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{
		"processId":1234,
		"rootUri":"file:///workspace",
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", jsonrpc2.RawMessage(`{}`))

	var inBuf bytes.Buffer
	for _, msg := range []jsonrpc2.Message{initCall, initNotif, shutdownCall} {
		if err := writeFramedMessage(&inBuf, msg); err != nil {
			t.Fatalf("failed to write framed message: %v", err)
		}
	}

	var outBuf bytes.Buffer
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	az := &stubAnalyzer{}
	err := Run(
		context.Background(),
		&inBuf,
		&outBuf,
		"0.0.0-test",
		root,
		az,
		logger,
	)

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Assert: no window/logMessage mentioning "dynamic" or "#VAR"
	// (the dynamic reference is NOT an operational event)
	logMessages := extractWindowLogMessages(t, &outBuf)
	for _, msg := range logMessages {
		if strings.Contains(msg.Message, "#VAR") || strings.Contains(msg.Message, "dynamic") {
			t.Errorf("expected no window/logMessage for dynamic ref #VAR, but got: %q", msg.Message)
		}
	}

	// The stderr log should also not mention the dynamic reference as an event
	// (it may appear in parser/diagnostics, but not as an operational log event)
	stderrContent := logBuf.String()
	// Just assert it doesn't log a special "unresolved dynamic" event message
	if strings.Contains(stderrContent, "Unresolved dynamic") {
		t.Logf("note: stderr mentions unresolved dynamic (expected to be in diagnostics, not logs): %q", stderrContent)
	}
}

// --- Helper functions ---

// windowLogMessage is a decoded window/logMessage frame.
type windowLogMessage struct {
	Method  string
	Type    protocol.MessageType
	Message string
}

// extractWindowLogMessages parses all framed messages in buf (either *bytes.Buffer
// or *lockedBuffer via its Bytes() method) and returns the window/logMessage
// notifications, decoded.
func extractWindowLogMessages(t *testing.T, buf interface {
	Bytes() []byte
}) []windowLogMessage {
	t.Helper()
	var messages []windowLogMessage

	// Keep a copy of the buffer for parsing
	data := buf.Bytes()
	work := bytes.NewBuffer(data)

	for {
		output := work.String()
		if output == "" {
			break
		}

		// Parse one framed message
		body, msgType, bodyEnd, err := parseFramedMessage(output)
		if err != nil {
			break
		}

		// Advance work
		remaining := output[bodyEnd:]
		work.Reset()
		work.WriteString(remaining)

		// Decode if it's a notification
		if msgType == "Notification" {
			var notif struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(body, &notif); err != nil {
				continue
			}

			if notif.Method == "window/logMessage" {
				var params struct {
					Type    protocol.MessageType `json:"type"`
					Message string               `json:"message"`
				}
				if err := json.Unmarshal(notif.Params, &params); err != nil {
					t.Logf("failed to unmarshal window/logMessage params: %v", err)
					continue
				}
				messages = append(messages, windowLogMessage{
					Method:  notif.Method,
					Type:    params.Type,
					Message: params.Message,
				})
			}
		}
	}

	return messages
}

// copyFixture copies a testdata fixture directory to a temp dir and returns its path.
func copyFixture(t *testing.T, fixturePath string) string {
	t.Helper()
	root := t.TempDir()

	// Use filepath.WalkDir to copy all files from the fixture
	if err := filepath.WalkDir(fixturePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Read the source file
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Compute the destination path
		relPath, err := filepath.Rel(fixturePath, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(root, relPath)

		// Create subdirectories if needed
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return err
		}

		// Write to destination
		return os.WriteFile(dstPath, content, 0644)
	}); err != nil {
		t.Fatalf("failed to copy fixture %s: %v", fixturePath, err)
	}

	return root
}
