package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"natural-lsp/internal/analysis/natural"
)

// TestNoUsableRootMessage covers the pure decision helper for the no-usable-root
// condition (feature 20, Story 3 AC1). It verifies both sub-conditions, the
// distinct phrasing, that paths are always named, and that a populated healthy
// root emits nothing.
func TestNoUsableRootMessage(t *testing.T) {
	tests := []struct {
		name           string
		probe          rootProbe
		indexFileCount int
		wantWarn       bool
		wantContains   []string
	}{
		{
			name: "no root established (no client path, no sentinel)",
			probe: rootProbe{
				clientPaths:   nil,
				cwdFallback:   "/some/cwd",
				sentinelFound: false,
				resolvedRoot:  "/some/cwd",
			},
			indexFileCount: 0,
			wantWarn:       true,
			wantContains:   []string{"could not establish workspace root", `cwd="/some/cwd"`},
		},
		{
			name: "established root but empty index",
			probe: rootProbe{
				clientPaths:   []string{"/ws"},
				cwdFallback:   "/some/cwd",
				sentinelFound: true,
				resolvedRoot:  "/ws",
			},
			indexFileCount: 0,
			wantWarn:       true,
			wantContains:   []string{"no indexable Natural files", `client="/ws"`, `cwd="/some/cwd"`},
		},
		{
			name: "client path present but no sentinel, empty index -> empty-index message",
			probe: rootProbe{
				clientPaths:   []string{"/ws"},
				cwdFallback:   "/some/cwd",
				sentinelFound: false,
				resolvedRoot:  "/ws",
			},
			indexFileCount: 0,
			wantWarn:       true,
			// A client root WAS provided, so this is the empty-index case, not "could not establish".
			wantContains: []string{"no indexable Natural files"},
		},
		{
			name: "healthy populated root -> no message",
			probe: rootProbe{
				clientPaths:   []string{"/ws"},
				cwdFallback:   "/some/cwd",
				sentinelFound: true,
				resolvedRoot:  "/ws",
			},
			indexFileCount: 3,
			wantWarn:       false,
		},
		{
			name: "no client, sentinel found, populated -> no message",
			probe: rootProbe{
				clientPaths:   nil,
				cwdFallback:   "/some/cwd",
				sentinelFound: true,
				resolvedRoot:  "/some/cwd",
			},
			indexFileCount: 5,
			wantWarn:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, warn := noUsableRootMessage(tc.probe, tc.indexFileCount)
			if warn != tc.wantWarn {
				t.Fatalf("warn = %v, want %v (msg=%q)", warn, tc.wantWarn, msg)
			}
			if !tc.wantWarn {
				if msg != "" {
					t.Errorf("expected empty message when warn=false, got %q", msg)
				}
				return
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

// FuzzNoUsableRootMessage guards the decision helper against panics on degenerate
// probes (FR-43).
func FuzzNoUsableRootMessage(f *testing.F) {
	f.Add("/a", "/b", true, 0)
	f.Add("", "", false, 5)
	f.Fuzz(func(t *testing.T, client, cwd string, sentinel bool, count int) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		p := rootProbe{clientPaths: []string{client}, cwdFallback: cwd, sentinelFound: sentinel, resolvedRoot: cwd}
		_, _ = noUsableRootMessage(p, count)
	})
}

// runHandshakeAgainstRoot drives initialize(rootUri=rootDir) → initialized →
// shutdown → exit against a fresh server, with cwdFallback pointed at emptyCwd.
// It returns the server output buffer (framed notifications/responses) and the
// captured stderr log text. Encoding UTF-8 is offered.
func runHandshakeAgainstRoot(t *testing.T, rootDir, emptyCwd string) (outText string, logText string) {
	t.Helper()

	rootURI := uri.File(rootDir)
	initParams := fmt.Sprintf(`{
		"processId": 1234,
		"rootUri": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, string(rootURI))

	// Feature 21 (T4): the index build is asynchronous, so drive the handshake
	// through the ready-gated harness — shutdown is withheld until the background
	// build publishes (and fires reportNoUsableRoot), so the no-usable-root
	// stderr Warn / window/showMessage still appear deterministically.
	out, logText := runGatedHandshake(t, initParams, emptyCwd, natural.New(nil))
	return out.String(), logText
}

// TestNoUsableRoot_EmptyRoot_StderrWarn verifies T5 (Story 3 AC1): an empty
// negotiated root (no .NSx files, no sentinel) produces exactly ONE actionable
// Warn line on stderr that names the tried paths.
func TestNoUsableRoot_EmptyRoot_StderrWarn(t *testing.T) {
	emptyRoot := t.TempDir() // negotiated root: empty, no sentinel, no Natural files
	emptyCwd := t.TempDir()  // cwd fallback: also empty

	_, logText := runHandshakeAgainstRoot(t, emptyRoot, emptyCwd)

	// The Warn line must name the tried path (the negotiated root came in as rootUri,
	// so it is the resolved root; the message names it and the cwd).
	if !strings.Contains(logText, "no indexable Natural files") {
		t.Errorf("stderr log missing the empty-index phrase 'no indexable Natural files'.\nLog:\n%s", logText)
	}
	if !strings.Contains(logText, emptyRoot) {
		t.Errorf("stderr log does not name the negotiated root path %q.\nLog:\n%s", emptyRoot, logText)
	}
	if !strings.Contains(logText, emptyCwd) {
		t.Errorf("stderr log does not name the cwd fallback path %q.\nLog:\n%s", emptyCwd, logText)
	}

	// Emitted exactly once (no per-request spam).
	if got := strings.Count(logText, "no indexable Natural files"); got != 1 {
		t.Errorf("expected the no-usable-root Warn exactly once, got %d.\nLog:\n%s", got, logText)
	}

	t.Logf("STDERR WARN captured:\n%s", logText)
}

// TestNoUsableRoot_EmptyRoot_ShowMessage verifies T6 (Story 3 AC2, OQ-3): the
// empty-root scenario also sends exactly ONE window/showMessage Warning (type=2)
// notification on the wire.
func TestNoUsableRoot_EmptyRoot_ShowMessage(t *testing.T) {
	emptyRoot := t.TempDir()
	emptyCwd := t.TempDir()

	outText, _ := runHandshakeAgainstRoot(t, emptyRoot, emptyCwd)

	notifs := parseAllNotifications(t, outText)
	var showMsgs []*jsonrpc2.Notification
	for _, n := range notifs {
		if n.Method() == "window/showMessage" {
			showMsgs = append(showMsgs, n)
		}
	}

	if len(showMsgs) != 1 {
		t.Fatalf("expected exactly 1 window/showMessage notification, got %d", len(showMsgs))
	}

	var params protocol.ShowMessageParams
	dec := jsontext.NewDecoder(bytes.NewReader(showMsgs[0].Params()))
	if err := params.UnmarshalJSONFrom(dec); err != nil {
		t.Fatalf("failed to unmarshal ShowMessageParams: %v", err)
	}
	if params.Type != protocol.MessageTypeWarning {
		t.Errorf("window/showMessage type = %d, want %d (Warning)", params.Type, protocol.MessageTypeWarning)
	}
	if !strings.Contains(params.Message, "no indexable Natural files") {
		t.Errorf("window/showMessage text does not name the empty condition: %q", params.Message)
	}

	t.Logf("window/showMessage notification bytes:\n%s", string(showMsgs[0].Params()))
}

// TestNoUsableRoot_PopulatedRoot_NoSignal verifies that a healthy, populated
// workspace root emits NEITHER a Warn line NOR a window/showMessage notification.
func TestNoUsableRoot_PopulatedRoot_NoSignal(t *testing.T) {
	wsDir := t.TempDir()
	copyTestdataRootHandshake(t, wsDir)
	emptyCwd := t.TempDir()

	outText, logText := runHandshakeAgainstRoot(t, wsDir, emptyCwd)

	if strings.Contains(logText, "no indexable Natural files") ||
		strings.Contains(logText, "could not establish workspace root") {
		t.Errorf("populated root should emit NO no-usable-root Warn.\nLog:\n%s", logText)
	}

	notifs := parseAllNotifications(t, outText)
	for _, n := range notifs {
		if n.Method() == "window/showMessage" {
			t.Errorf("populated root should emit NO window/showMessage, got one: %s", string(n.Params()))
		}
	}
}

// TestNoUsableRoot_OutOfRootDefinition_NoError verifies Story 3 AC3 / FR-43:
// after startup against an empty root, a textDocument/definition request for a
// file OUTSIDE the root returns null (empty), NOT a JSON-RPC error, and the loop
// survives to serve a subsequent request.
func TestNoUsableRoot_OutOfRootDefinition_NoError(t *testing.T) {
	emptyRoot := t.TempDir()
	emptyCwd := t.TempDir()
	rootURI := uri.File(emptyRoot)

	// A definition request for a file that lives nowhere near the root.
	outsideURI := uri.File(filepath.Join(t.TempDir(), "OUTSIDE.NSP"))
	defParams := fmt.Sprintf(`{
		"textDocument": {"uri": %q},
		"position": {"line": 0, "character": 0}
	}`, string(outsideURI))

	initParams := fmt.Sprintf(`{
		"processId": 1234,
		"rootUri": %q,
		"capabilities": {}
	}`, string(rootURI))

	msgs := []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams)),
		jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`)),
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "textDocument/definition", jsonrpc2.RawMessage(defParams)),
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(3), "shutdown", jsonrpc2.RawMessage(`{}`)),
		jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`)),
	}
	var reqBuf bytes.Buffer
	for i, m := range msgs {
		if err := writeFramedMessage(&reqBuf, m); err != nil {
			t.Fatalf("write framed message %d: %v", i, err)
		}
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)
	if err := Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", emptyCwd, az, logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	responseBuf := bytes.NewBuffer(outBuf.Bytes())

	// Skip the initialize response.
	if _, err := parseFramedResponse(responseBuf); err != nil {
		t.Fatalf("parse initialize response: %v", err)
	}

	// The definition response must be a result (null), NOT an error.
	defBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("parse definition response: %v", err)
	}
	defMsg, err := jsonrpc2.DecodeMessage(defBody)
	if err != nil {
		t.Fatalf("decode definition response: %v", err)
	}
	defResp, ok := defMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response, got %T", defMsg)
	}
	if defResp.Err() != nil {
		t.Errorf("out-of-root definition returned a JSON-RPC error (FR-43 regression): %v", defResp.Err())
	}
	if !bytes.Equal(defResp.Result(), []byte(`null`)) {
		t.Errorf("out-of-root definition result = %s, want null", string(defResp.Result()))
	}

	// The loop survives: shutdown succeeds.
	shutdownBody, err := parseFramedResponse(responseBuf)
	if err != nil {
		t.Fatalf("parse shutdown response: %v", err)
	}
	shutdownMsg, err := jsonrpc2.DecodeMessage(shutdownBody)
	if err != nil {
		t.Fatalf("decode shutdown response: %v", err)
	}
	shutdownResp, ok := shutdownMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for shutdown, got %T", shutdownMsg)
	}
	if shutdownResp.Err() != nil {
		t.Errorf("shutdown after out-of-root definition should succeed; got error: %v", shutdownResp.Err())
	}
}

// copyTestdataRootHandshake copies the roothandshake testdata workspace (with
// sentinel + sample files) into dst.
func copyTestdataRootHandshake(t *testing.T, dst string) {
	t.Helper()
	srcDir := filepath.Join("testdata", "roothandshake")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("read testdata/roothandshake: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), content, 0o644); err != nil {
			t.Fatalf("write %s: %v", entry.Name(), err)
		}
	}
}
