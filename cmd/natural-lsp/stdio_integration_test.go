//go:build integration

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
)

// serverBinaryName is the built server's file name for the current OS. Windows
// requires an executable extension: `go build -o <dir>/natural-lsp` and the
// subsequent exec must both use `natural-lsp.exe`, or the exec fails with
// "executable file not found" (the binary has no PATHEXT-recognized extension).
func serverBinaryName() string {
	if runtime.GOOS == "windows" {
		return "natural-lsp.exe"
	}
	return "natural-lsp"
}

// TestStdioHandshake is the first integration test (Feature 03, Task T9).
// It validates the end-to-end stdio LSP handshake:
// 1. Build the natural-lsp binary
// 2. Create a temp workspace with a .natural-lsp.toml sentinel
// 3. Launch the binary with --stdio
// 4. Drive initialize → initialized → shutdown → exit
// 5. Assert capabilities, serverInfo, and clean exit
//
// This pins the smoke criterion from FR-41 Story 1: "well-formed initialize response
// to stdio, stdout carries protocol bytes only, logs on stderr, process exits 0".
func TestStdioHandshake(t *testing.T) {
	// Step 1: Build the binary to a temp directory
	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, serverBinaryName())

	// Locate the module root by walking up from the test's working directory
	// (go test sets cwd to the package directory) until go.mod is found.
	// This works in both local dev and CI without hardcoded paths.
	moduleRoot, err := func() (string, error) {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return "", fmt.Errorf("go.mod not found")
			}
			dir = parent
		}
	}()
	if err != nil {
		t.Fatalf("could not locate module root: %v", err)
	}

	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/natural-lsp")
	buildCmd.Dir = moduleRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\noutput: %s", err, output)
	}

	// Step 2: Create a temp workspace with a .natural-lsp.toml sentinel
	workspaceDir := t.TempDir()
	sentinelPath := filepath.Join(workspaceDir, ".natural-lsp.toml")
	if err := os.WriteFile(sentinelPath, nil, 0o644); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}

	// Step 3: Launch the binary with --stdio
	cmd := exec.Command(binaryPath, "--stdio")
	cmd.Dir = workspaceDir
	cmd.Stderr = os.Stderr // log output to stderr (visible on test failure)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start binary: %v", err)
	}

	// Clean up process if test panics
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Step 4: Drive initialize → initialized → shutdown → exit sequence

	// Build and send initialize request (as Content-Length-framed JSON)
	initID := jsonrpc2.NewNumberID(1)
	initParamsJSON := jsonrpc2.RawMessage(`{
		"processId": 1234,
		"rootPath": "` + workspaceDir + `",
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8", "utf-16"]
			}
		}
	}`)

	initCall := jsonrpc2.NewCall(initID, "initialize", initParamsJSON)

	// Encode as bare JSON and frame it with Content-Length header
	initMsg, err := jsonrpc2.EncodeMessage(initCall)
	if err != nil {
		t.Fatalf("failed to encode initialize request: %v", err)
	}
	framedInitRequest := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(initMsg))
	if _, err := stdin.Write([]byte(framedInitRequest)); err != nil {
		t.Fatalf("failed to write initialize request header: %v", err)
	}
	if _, err := stdin.Write(initMsg); err != nil {
		t.Fatalf("failed to write initialize request body: %v", err)
	}

	// A single persistent framed reader over stdout — a fresh bufio.Reader per
	// call could over-read and drop a buffered frame once notifications are
	// interleaved with responses.
	fr := newFramedReader(stdout)

	// Read initialize response (Content-Length-framed, with timeout).
	// Skip any interleaved server→client notifications (window/showMessage,
	// publishDiagnostics) while awaiting the response to initID.
	initRespCall := fr.readResponseSkippingNotifications(t, &initID, 5*time.Second)

	// Assert: response id matches
	if initRespCall.ID() != initID {
		t.Errorf("initialize response id = %v, want %v", initRespCall.ID(), initID)
	}

	// Assert: response has no error
	if initRespCall.Err() != nil {
		t.Errorf("initialize response has error: %v", initRespCall.Err())
	}

	// Assert: response has result
	if initRespCall.Result() == nil {
		t.Fatalf("initialize response has no result")
	}

	// Assert: successful parsing of framed response proves stdout uses Content-Length framing
	// (readFramedMessage only succeeds if the header "Content-Length: N\r\n\r\n" is present)

	// Parse the InitializeResult as a generic map (avoids TextDocumentSync union type issues)
	var resultMap map[string]interface{}
	if err := json.Unmarshal(initRespCall.Result(), &resultMap); err != nil {
		t.Fatalf("failed to unmarshal InitializeResult: %v (result: %s)", err, string(initRespCall.Result()))
	}

	// Step 5: Assert capabilities and serverInfo
	{
		// Assert: serverInfo.name == "natural-lsp"
		serverInfo, ok := resultMap["serverInfo"].(map[string]interface{})
		if !ok {
			t.Fatalf("serverInfo missing or wrong type; want map[string]interface{}")
		}
		if serverInfo["name"] != "natural-lsp" {
			t.Errorf("serverInfo.name = %q, want %q", serverInfo["name"], "natural-lsp")
		}

		// Assert: serverInfo.version is present (should match --version output)
		version, ok := serverInfo["version"].(string)
		if !ok || version == "" {
			t.Errorf("serverInfo.version is not a string or is empty; got %v", serverInfo["version"])
		}

		// Assert: capabilities has the expected structure
		caps, ok := resultMap["capabilities"].(map[string]interface{})
		if !ok {
			t.Fatalf("capabilities missing or wrong type; want map[string]interface{}")
		}

		// Assert: capabilities.textDocumentSync is present and Full (kind 1, ADR-009)
		if caps["textDocumentSync"] == nil {
			t.Errorf("textDocumentSync is nil; want 1 (Full)")
		} else if syncVal, ok := caps["textDocumentSync"].(float64); ok {
			if syncVal != 1 {
				t.Errorf("textDocumentSync = %v, want 1 (Full)", syncVal)
			}
		} else {
			t.Errorf("textDocumentSync has unexpected type %T", caps["textDocumentSync"])
		}

		// Assert: capabilities.positionEncoding is present
		if caps["positionEncoding"] == nil {
			t.Errorf("positionEncoding is nil; want utf-8 or utf-16")
		} else if encoding, ok := caps["positionEncoding"].(string); ok {
			if encoding == "" {
				t.Errorf("positionEncoding is empty string; want utf-8 or utf-16")
			}
		} else {
			t.Errorf("positionEncoding has unexpected type %T", caps["positionEncoding"])
		}

		// Assert: navigation providers (feature 10, FR-24/25/26), the document
		// outline provider (feature 11, FR-27), and the hover provider (feature 12,
		// FR-28) are advertised as bare booleans — all should be true.
		navigationProviders := []string{
			"definitionProvider",
			"referencesProvider",
			"workspaceSymbolProvider",
			"documentSymbolProvider",
			"hoverProvider",
		}
		for _, flag := range navigationProviders {
			if val, exists := caps[flag]; !exists || val != true {
				t.Errorf("%s = %v, want true (feature 10/11/12)", flag, val)
			}
		}

		// Assert: the code lens provider (feature 13, FR-29) is advertised as an
		// OBJECT ({resolveProvider:false}), not a bare true — so assert it is present
		// and non-nil rather than == true.
		if val, exists := caps["codeLensProvider"]; !exists || val == nil || val == false {
			t.Errorf("codeLensProvider = %v, want an object (feature 13, FR-29)", val)
		}
	}

	// Send initialized notification (Content-Length-framed)
	initNotif := jsonrpc2.NewNotification("initialized", nil)
	initNotifMsg, err := jsonrpc2.EncodeMessage(initNotif)
	if err != nil {
		t.Fatalf("failed to encode initialized notification: %v", err)
	}
	framedInitNotif := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(initNotifMsg))
	if _, err := stdin.Write([]byte(framedInitNotif)); err != nil {
		t.Fatalf("failed to write initialized notification header: %v", err)
	}
	if _, err := stdin.Write(initNotifMsg); err != nil {
		t.Fatalf("failed to write initialized notification body: %v", err)
	}

	// Send shutdown request (Content-Length-framed)
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", nil)
	shutdownMsg, err := jsonrpc2.EncodeMessage(shutdownCall)
	if err != nil {
		t.Fatalf("failed to encode shutdown request: %v", err)
	}
	framedShutdownRequest := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(shutdownMsg))
	if _, err := stdin.Write([]byte(framedShutdownRequest)); err != nil {
		t.Fatalf("failed to write shutdown request header: %v", err)
	}
	if _, err := stdin.Write(shutdownMsg); err != nil {
		t.Fatalf("failed to write shutdown request body: %v", err)
	}

	// Read shutdown response (Content-Length-framed). The empty-root
	// window/showMessage Warning (Story 3 AC1(b)) is emitted during the
	// deferred bootstrap at `initialized`, so it may arrive in the stream
	// before the shutdown response — skip any interleaved notifications.
	shutdownRespCall := fr.readResponseSkippingNotifications(t, &shutdownID, 5*time.Second)

	// Assert: shutdown response id matches
	if shutdownRespCall.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownRespCall.ID(), shutdownID)
	}

	// Assert: shutdown response has no error
	if shutdownRespCall.Err() != nil {
		t.Errorf("shutdown response has error: %v", shutdownRespCall.Err())
	}

	// Send exit notification (Content-Length-framed)
	exitNotif := jsonrpc2.NewNotification("exit", nil)
	exitMsg, err := jsonrpc2.EncodeMessage(exitNotif)
	if err != nil {
		t.Fatalf("failed to encode exit notification: %v", err)
	}
	framedExitNotif := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(exitMsg))
	if _, err := stdin.Write([]byte(framedExitNotif)); err != nil {
		t.Fatalf("failed to write exit notification header: %v", err)
	}
	if _, err := stdin.Write(exitMsg); err != nil {
		t.Fatalf("failed to write exit notification body: %v", err)
	}

	// Close stdin to signal end of input
	stdin.Close()

	// Wait for process to exit with a timeout
	exitDone := make(chan error, 1)
	go func() {
		exitDone <- cmd.Wait()
	}()

	select {
	case err := <-exitDone:
		// Assert: process exits with code 0
		if err != nil {
			t.Errorf("process exit error: %v; want nil (exit 0)", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for process to exit")
	}
}

// framedReader reads Content-Length-framed JSON-RPC messages from an underlying
// stream. It wraps the stream in a SINGLE persistent *bufio.Reader so that any
// bytes buffered past one message boundary (a bufio.Reader can over-read on a
// single underlying Read) are retained for the next read rather than discarded.
// Constructing a fresh bufio.Reader per message (the earlier approach) could drop
// a frame that happened to be buffered alongside the one being returned — a
// hazard amplified once the server interleaves notifications with responses.
type framedReader struct {
	r *bufio.Reader
}

func newFramedReader(r io.Reader) *framedReader {
	return &framedReader{r: bufio.NewReader(r)}
}

// readResponseSkippingNotifications reads framed JSON-RPC messages until it
// observes a *jsonrpc2.Response, draining (skipping) any server→client
// notifications (e.g. window/showMessage, textDocument/publishDiagnostics) that
// arrive interleaved in the stream. This mirrors what a real LSP client does:
// notifications are unilateral and may arrive at any time while awaiting the
// response to a request id.
//
// If wantID is non-nil, only a Response whose id equals *wantID is accepted;
// other Responses are treated as unexpected and fail the test. If wantID is nil,
// the first Response of any id is returned.
func (fr *framedReader) readResponseSkippingNotifications(t *testing.T, wantID *jsonrpc2.ID, timeout time.Duration) *jsonrpc2.Response {
	t.Helper()
	for {
		body, err := fr.readFramedMessageWithTimeout(timeout)
		if err != nil {
			t.Fatalf("failed to read framed message: %v", err)
		}
		msg, err := jsonrpc2.DecodeMessage(body)
		if err != nil {
			t.Fatalf("failed to decode framed message: %v (message: %s)", err, string(body))
		}
		switch m := msg.(type) {
		case *jsonrpc2.Notification:
			// Drain server→client notifications and keep reading.
			continue
		case *jsonrpc2.Response:
			if wantID != nil && m.ID() != *wantID {
				t.Fatalf("expected *jsonrpc2.Response with id %v, got id %v", *wantID, m.ID())
			}
			return m
		default:
			t.Fatalf("expected *jsonrpc2.Response, got %T", msg)
		}
	}
}

// readFramedMessageWithTimeout reads one Content-Length-framed JSON-RPC message
// from the persistent reader with a timeout. It parses the "Content-Length:
// N\r\n\r\n" header, then reads exactly N bytes of the JSON body.
func (fr *framedReader) readFramedMessageWithTimeout(timeout time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}

	resultChan := make(chan result, 1)
	go func() {
		data, err := fr.readFramedMessage()
		resultChan <- result{data, err}
	}()

	select {
	case res := <-resultChan:
		return res.data, res.err
	case <-time.After(timeout):
		return nil, &timeoutError{"read message timeout"}
	}
}

// readFramedMessage reads one Content-Length-framed JSON-RPC message from the
// persistent reader. It returns just the JSON body (not the header).
func (fr *framedReader) readFramedMessage() ([]byte, error) {
	// Read the header line: "Content-Length: N\r\n"
	reader := fr.r
	headerLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read header line: %w", err)
	}

	// Remove trailing \r\n
	headerLine = strings.TrimSuffix(headerLine, "\r\n")
	headerLine = strings.TrimSuffix(headerLine, "\n")

	// Parse "Content-Length: N"
	if !strings.HasPrefix(headerLine, "Content-Length: ") {
		return nil, fmt.Errorf("expected 'Content-Length: ...' header, got: %q", headerLine)
	}
	lengthStr := strings.TrimPrefix(headerLine, "Content-Length: ")
	contentLen, err := strconv.Atoi(lengthStr)
	if err != nil {
		return nil, fmt.Errorf("invalid Content-Length value: %q (%v)", lengthStr, err)
	}

	// Read the blank line ("\r\n" or just "\n")
	blankLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read blank line: %w", err)
	}
	blankLine = strings.TrimSpace(blankLine)
	if blankLine != "" {
		return nil, fmt.Errorf("expected blank line after Content-Length header, got: %q", blankLine)
	}

	// Read exactly contentLen bytes of the JSON body
	body := make([]byte, contentLen)
	n, err := io.ReadFull(reader, body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w (read %d of %d bytes)", err, n, contentLen)
	}

	return body, nil
}

// TestCrossWorkdirRootUri is the integration regression test (Feature 20, Task T4).
// It reproduces the 2026-07-14 assessment defect #2: the server resolves workspace
// root from initialize's rootUri even when the process cwd is OUTSIDE the workspace.
//
// Scenario:
// 1. Build the natural-lsp binary
// 2. Create a WORKSPACE temp dir with a .natural-lsp.toml sentinel + HELLO.NSP caller
//   - CALLGREET.NSN callee
//     3. Create a SEPARATE CDIR temp dir (NO Natural files, NO sentinel) — the process cwd
//     4. Launch the binary with cmd.Dir = cdir (so os.Getwd() returns cdir, not workspace)
//     5. Send initialize with rootUri = workspace (the feature-20 fix: deferred bootstrap
//     must use rootUri, not cwd)
//     6. Drive initialized → didOpen → definition → shutdown → exit
//     7. Assert definition resolves to the callee file in the workspace
//
// On main before feature 20, the index is built from cwd at startup, misses the
// workspace, and definition returns null/empty. After feature 20, the index is
// built from the negotiated rootUri at initialize time, and definition resolves.
func TestCrossWorkdirRootUri(t *testing.T) {
	// Step 1: Build the binary to a temp directory
	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, serverBinaryName())

	moduleRoot, err := func() (string, error) {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return "", fmt.Errorf("go.mod not found")
			}
			dir = parent
		}
	}()
	if err != nil {
		t.Fatalf("could not locate module root: %v", err)
	}

	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/natural-lsp")
	buildCmd.Dir = moduleRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\noutput: %s", err, output)
	}

	// Step 2: Create workspace temp dir WITH a .natural-lsp.toml sentinel + sample files
	workspaceDir := t.TempDir()
	sentinelPath := filepath.Join(workspaceDir, ".natural-lsp.toml")
	if err := os.WriteFile(sentinelPath, nil, 0o644); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}

	// Copy sample workspace files (HELLO.NSP, CALLGREET.NSN, SAYHELLO.NSS)
	sampleDir := filepath.Join(moduleRoot, "docs/plans/features/15-editor-clients/sample-workspace")
	for _, filename := range []string{"HELLO.NSP", "CALLGREET.NSN", "SAYHELLO.NSS"} {
		srcPath := filepath.Join(sampleDir, filename)
		content, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("failed to read sample file %s: %v", filename, err)
		}
		dstPath := filepath.Join(workspaceDir, filename)
		if err := os.WriteFile(dstPath, content, 0o644); err != nil {
			t.Fatalf("failed to write %s to workspace: %v", filename, err)
		}
	}

	// Step 3: Create a separate cwd temp dir (NO Natural files, NO sentinel)
	cwdDir := t.TempDir()

	// Assert: cwd and workspace are distinct and neither is an ancestor of the other
	{
		cwdAbs, err := filepath.Abs(cwdDir)
		if err != nil {
			t.Fatalf("failed to abs cwd: %v", err)
		}
		wsAbs, err := filepath.Abs(workspaceDir)
		if err != nil {
			t.Fatalf("failed to abs workspace: %v", err)
		}
		if cwdAbs == wsAbs {
			t.Fatalf("cwd and workspace must be distinct; both are %s", cwdAbs)
		}
		// Check neither is an ancestor of the other
		if strings.HasPrefix(wsAbs, cwdAbs+string(filepath.Separator)) ||
			strings.HasPrefix(cwdAbs, wsAbs+string(filepath.Separator)) {
			t.Fatalf("cwd (%s) and workspace (%s) must not have ancestor relationship", cwdAbs, wsAbs)
		}
	}

	// Step 4: Launch binary with cmd.Dir = cwdDir (so os.Getwd() != workspace)
	cmd := exec.Command(binaryPath, "--stdio")
	cmd.Dir = cwdDir // THE CRITICAL DIFFERENCE: cwd is outside workspace
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start binary: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Step 5: Send initialize with rootUri = workspaceDir
	// (NOT rootPath; the rootUri is the deferred-bootstrap trigger)
	initID := jsonrpc2.NewNumberID(1)
	initParamsJSON := jsonrpc2.RawMessage(`{
		"processId": 1234,
		"rootUri": "file://` + workspaceDir + `",
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8", "utf-16"]
			}
		}
	}`)

	initCall := jsonrpc2.NewCall(initID, "initialize", initParamsJSON)
	initMsg, err := jsonrpc2.EncodeMessage(initCall)
	if err != nil {
		t.Fatalf("failed to encode initialize request: %v", err)
	}
	framedInitRequest := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(initMsg))
	if _, err := stdin.Write([]byte(framedInitRequest)); err != nil {
		t.Fatalf("failed to write initialize request header: %v", err)
	}
	if _, err := stdin.Write(initMsg); err != nil {
		t.Fatalf("failed to write initialize request body: %v", err)
	}

	// A single persistent framed reader over stdout (see framedReader).
	fr := newFramedReader(stdout)

	// Read initialize response, skipping any interleaved notifications.
	initRespCall := fr.readResponseSkippingNotifications(t, &initID, 5*time.Second)

	if initRespCall.Err() != nil {
		t.Fatalf("initialize response has error: %v", initRespCall.Err())
	}

	// Step 6: Send initialized notification
	initNotif := jsonrpc2.NewNotification("initialized", nil)
	initNotifMsg, err := jsonrpc2.EncodeMessage(initNotif)
	if err != nil {
		t.Fatalf("failed to encode initialized notification: %v", err)
	}
	framedInitNotif := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(initNotifMsg))
	if _, err := stdin.Write([]byte(framedInitNotif)); err != nil {
		t.Fatalf("failed to write initialized notification header: %v", err)
	}
	if _, err := stdin.Write(initNotifMsg); err != nil {
		t.Fatalf("failed to write initialized notification body: %v", err)
	}

	// Step 7: Send didOpen for HELLO.NSP
	helloURI := "file://" + filepath.Join(workspaceDir, "HELLO.NSP")
	helloContent, err := os.ReadFile(filepath.Join(workspaceDir, "HELLO.NSP"))
	if err != nil {
		t.Fatalf("failed to read HELLO.NSP: %v", err)
	}

	didOpenNotif := jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(`{
		"textDocument": {
			"uri": "`+helloURI+`",
			"languageId": "natural",
			"version": 1,
			"text": "`+escapeJSONString(string(helloContent))+`"
		}
	}`))

	didOpenMsg, err := jsonrpc2.EncodeMessage(didOpenNotif)
	if err != nil {
		t.Fatalf("failed to encode didOpen: %v", err)
	}
	framedDidOpen := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(didOpenMsg))
	if _, err := stdin.Write([]byte(framedDidOpen)); err != nil {
		t.Fatalf("failed to write didOpen header: %v", err)
	}
	if _, err := stdin.Write(didOpenMsg); err != nil {
		t.Fatalf("failed to write didOpen body: %v", err)
	}

	// Step 8: Send definition request at the CALLGREET call site
	// HELLO.NSP line 12 (0-indexed: line 11): CALLNAT 'CALLGREET' #NAME
	// The identifier CALLGREET is at character 10 (inside the quotes, after CALLNAT ')
	//
	// Feature 21 (T4): the workspace index is now built ASYNCHRONOUSLY on a
	// background goroutine, so a definition request sent immediately after
	// "initialized" may arrive before the index has published — in which case the
	// provider degrades to an empty Location[] (FR-43), NOT an error. A real
	// editor simply sees the definition become available once indexing finishes.
	// This test mirrors that by retrying the request (fresh id each attempt)
	// until it resolves or a bounded deadline elapses.
	var locations []interface{}
	deadline := time.Now().Add(10 * time.Second)
	nextID := int64(2)
	for attempt := 0; ; attempt++ {
		defID := jsonrpc2.NewNumberID(nextID)
		nextID++
		defCall := jsonrpc2.NewCall(defID, "textDocument/definition", jsonrpc2.RawMessage(`{
			"textDocument": {
				"uri": "`+helloURI+`"
			},
			"position": {
				"line": 11,
				"character": 10
			}
		}`))

		defMsg, err := jsonrpc2.EncodeMessage(defCall)
		if err != nil {
			t.Fatalf("failed to encode definition request: %v", err)
		}
		framedDefRequest := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(defMsg))
		if _, err := stdin.Write([]byte(framedDefRequest)); err != nil {
			t.Fatalf("failed to write definition request header: %v", err)
		}
		if _, err := stdin.Write(defMsg); err != nil {
			t.Fatalf("failed to write definition request body: %v", err)
		}

		// Read the correlated definition response, skipping any interleaved
		// notifications (e.g. publishDiagnostics, window/showMessage).
		defRespCall := fr.readResponseSkippingNotifications(t, &defID, 5*time.Second)
		if defRespCall.Err() != nil {
			t.Fatalf("definition response has error: %v", defRespCall.Err())
		}

		// A nil / null / [] result means the index has not published yet — retry.
		locations = nil
		if defRespCall.Result() != nil {
			if err := json.Unmarshal(defRespCall.Result(), &locations); err != nil {
				// Try parsing as a single Location object.
				var singleLoc interface{}
				if err := json.Unmarshal(defRespCall.Result(), &singleLoc); err != nil {
					t.Fatalf("failed to unmarshal definition result: %v (result: %s)", err, string(defRespCall.Result()))
				}
				if singleLoc != nil {
					locations = []interface{}{singleLoc}
				}
			}
		}
		if len(locations) > 0 {
			break // index published and definition resolved
		}
		if time.Now().After(deadline) {
			t.Fatalf("definition returned empty Location[] after %d attempts over 10s; the async index build never published a resolvable index from rootUri", attempt+1)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Step 9: Assert definition result points to CALLGREET.NSN in workspace.

	// Assert the first location's URI contains CALLGREET.NSN and is in the workspace
	firstLoc, ok := locations[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first location to be a map, got %T", locations[0])
	}

	uriVal, ok := firstLoc["uri"].(string)
	if !ok {
		t.Fatalf("expected location.uri to be a string, got %T", firstLoc["uri"])
	}

	if !strings.Contains(uriVal, "CALLGREET.NSN") {
		t.Errorf("definition URI = %q, want to contain CALLGREET.NSN", uriVal)
	}

	if !strings.HasPrefix(uriVal, "file://"+workspaceDir) {
		t.Errorf("definition URI = %q, want to be in workspace %s", uriVal, workspaceDir)
	}

	// Step 10: Send shutdown request. Use nextID (past any definition-retry ids)
	// so the id never collides with a retried definition request.
	shutdownID := jsonrpc2.NewNumberID(nextID)
	nextID++
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", nil)
	shutdownMsg, err := jsonrpc2.EncodeMessage(shutdownCall)
	if err != nil {
		t.Fatalf("failed to encode shutdown request: %v", err)
	}
	framedShutdownRequest := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(shutdownMsg))
	if _, err := stdin.Write([]byte(framedShutdownRequest)); err != nil {
		t.Fatalf("failed to write shutdown request header: %v", err)
	}
	if _, err := stdin.Write(shutdownMsg); err != nil {
		t.Fatalf("failed to write shutdown request body: %v", err)
	}

	// Read shutdown response, skipping any interleaved notifications.
	shutdownRespCall := fr.readResponseSkippingNotifications(t, &shutdownID, 5*time.Second)

	if shutdownRespCall.Err() != nil {
		t.Errorf("shutdown response has error: %v", shutdownRespCall.Err())
	}

	// Step 11: Send exit notification
	exitNotif := jsonrpc2.NewNotification("exit", nil)
	exitMsg, err := jsonrpc2.EncodeMessage(exitNotif)
	if err != nil {
		t.Fatalf("failed to encode exit notification: %v", err)
	}
	framedExitNotif := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(exitMsg))
	if _, err := stdin.Write([]byte(framedExitNotif)); err != nil {
		t.Fatalf("failed to write exit notification header: %v", err)
	}
	if _, err := stdin.Write(exitMsg); err != nil {
		t.Fatalf("failed to write exit notification body: %v", err)
	}

	// Close stdin and wait for exit
	stdin.Close()

	exitDone := make(chan error, 1)
	go func() {
		exitDone <- cmd.Wait()
	}()

	select {
	case err := <-exitDone:
		if err != nil {
			t.Errorf("process exit error: %v; want nil (exit 0)", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for process to exit")
	}

	t.Logf("SUCCESS: definition resolved to %s from workspace root negotiated via rootUri (not cwd)", uriVal)
}

// escapeJSONString escapes a string for embedding in JSON.
func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1]) // Strip the quotes added by json.Marshal
}

// ErrReadTimeout is the timeout error type for readFramedMessageWithTimeout.
type timeoutError struct {
	msg string
}

func (e *timeoutError) Error() string   { return e.msg }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return false }
