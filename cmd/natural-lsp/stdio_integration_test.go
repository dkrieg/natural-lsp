//go:build integration

package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
)

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
	binaryPath := filepath.Join(tempDir, "natural-lsp")

	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/natural-lsp")
	buildCmd.Dir = "/Users/daniel/Projects/natural-lsp"
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

	// Build and send initialize request (as raw JSON)
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
	initMsg, err := jsonrpc2.EncodeMessage(initCall)
	if err != nil {
		t.Fatalf("failed to encode initialize request: %v", err)
	}

	// Send initialize
	if _, err := stdin.Write(initMsg); err != nil {
		t.Fatalf("failed to write initialize request: %v", err)
	}

	// Read initialize response (with timeout)
	initResp, err := readMessageWithTimeout(stdout, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to read initialize response: %v", err)
	}

	// Parse the response
	initRespMsg, err := jsonrpc2.DecodeMessage(initResp)
	if err != nil {
		t.Fatalf("failed to decode initialize response: %v (response: %s)", err, string(initResp))
	}

	initRespCall, ok := initRespMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initRespMsg)
	}

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

		// Assert: no feature providers are advertised (FR-41, NFR-11)
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
				t.Errorf("%s = %v, want nil/false (not yet implemented)", flag, val)
			}
		}
	}

	// Send initialized notification
	initNotif := jsonrpc2.NewNotification("initialized", nil)
	initNotifMsg, err := jsonrpc2.EncodeMessage(initNotif)
	if err != nil {
		t.Fatalf("failed to encode initialized notification: %v", err)
	}
	if _, err := stdin.Write(initNotifMsg); err != nil {
		t.Fatalf("failed to write initialized notification: %v", err)
	}

	// Send shutdown request
	shutdownID := jsonrpc2.NewNumberID(2)
	shutdownCall := jsonrpc2.NewCall(shutdownID, "shutdown", nil)
	shutdownMsg, err := jsonrpc2.EncodeMessage(shutdownCall)
	if err != nil {
		t.Fatalf("failed to encode shutdown request: %v", err)
	}
	if _, err := stdin.Write(shutdownMsg); err != nil {
		t.Fatalf("failed to write shutdown request: %v", err)
	}

	// Read shutdown response
	shutdownResp, err := readMessageWithTimeout(stdout, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to read shutdown response: %v", err)
	}

	shutdownRespMsg, err := jsonrpc2.DecodeMessage(shutdownResp)
	if err != nil {
		t.Fatalf("failed to decode shutdown response: %v (response: %s)", err, string(shutdownResp))
	}

	shutdownRespCall, ok := shutdownRespMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownRespMsg)
	}

	// Assert: shutdown response id matches
	if shutdownRespCall.ID() != shutdownID {
		t.Errorf("shutdown response id = %v, want %v", shutdownRespCall.ID(), shutdownID)
	}

	// Assert: shutdown response has no error
	if shutdownRespCall.Err() != nil {
		t.Errorf("shutdown response has error: %v", shutdownRespCall.Err())
	}

	// Send exit notification
	exitNotif := jsonrpc2.NewNotification("exit", nil)
	exitMsg, err := jsonrpc2.EncodeMessage(exitNotif)
	if err != nil {
		t.Fatalf("failed to encode exit notification: %v", err)
	}
	if _, err := stdin.Write(exitMsg); err != nil {
		t.Fatalf("failed to write exit notification: %v", err)
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

// readMessageWithTimeout reads one complete JSON-RPC message from r with a timeout.
// It uses a goroutine and time.After to prevent hanging.
func readMessageWithTimeout(r io.Reader, timeout time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}

	resultChan := make(chan result, 1)
	go func() {
		decoder := jsontext.NewDecoder(r)
		data, err := decoder.ReadValue()
		resultChan <- result{data, err}
	}()

	select {
	case res := <-resultChan:
		return res.data, res.err
	case <-time.After(timeout):
		return nil, ErrReadTimeout
	}
}

// ErrReadTimeout is returned when readMessageWithTimeout exceeds its deadline.
var ErrReadTimeout = &timeoutError{"read message timeout"}

type timeoutError struct {
	msg string
}

func (e *timeoutError) Error() string   { return e.msg }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return false }
