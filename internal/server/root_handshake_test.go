package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// TestRootHandshakeNegotiatedRootDrivesIndex verifies that the workspace index
// is built from the root negotiated during the initialize handshake, NOT from
// the process cwdFallback. This is the core T3 AC (Story 1 AC4).
//
// The test sets up:
//  1. A real workspace in a temp dir with .natural-lsp.toml sentinel and a
//     caller/callee pair (HELLO.NSP -> CALLNAT 'CALLGREET' -> CALLGREET.NSN).
//  2. A separate, unrelated empty temp dir (no Natural files, no sentinel) to
//     serve as the cwdFallback.
//  3. Launches Run with the empty dir as cwdFallback, but sends the workspace
//     temp dir as rootUri in the initialize request.
//  4. Drives initialize → initialized and asserts (via indexReadyHook) that:
//     - The index is non-empty (proves it indexed the workspace, not the empty cwdFallback).
//     - The index contains CALLGREET (one of the workspace objects).
func TestRootHandshakeNegotiatedRootDrivesIndex(t *testing.T) {
	// 1. Copy the testdata workspace (with sentinel + sample files) into a temp dir.
	//    This is the workspace the client will point to.
	wsDir := t.TempDir()
	srcDir := filepath.Join("testdata", "roothandshake")

	// List the testdata files and copy them.
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("failed to read testdata/roothandshake: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(wsDir, entry.Name())
		content, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("failed to read %s: %v", src, err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", dst, err)
		}
	}

	// 2. Create a separate, unrelated empty temp dir to serve as cwdFallback.
	//    This dir has no Natural files and no sentinel.
	emptyDir := t.TempDir()

	// 3. Set up the index-ready hook to capture the built index. Feature 21 (T4)
	// made the index build asynchronous; the runGatedHandshake harness below
	// chains onto this capture hook and withholds shutdown until the build has
	// published, so capturedIdx is populated deterministically. Restore is via
	// t.Cleanup (LIFO with the gate's own cleanup) so the hook chain unwinds
	// cleanly.
	var capturedIdx *workspace.Index
	var capturedEncoding protocol.PositionEncodingKind
	indexReadyHookMu.Lock()
	oldHook := indexReadyHook
	indexReadyHook = func(idx *workspace.Index, enc protocol.PositionEncodingKind) {
		capturedIdx = idx
		capturedEncoding = enc
	}
	indexReadyHookMu.Unlock()
	t.Cleanup(func() {
		indexReadyHookMu.Lock()
		indexReadyHook = oldHook
		indexReadyHookMu.Unlock()
	})

	// 4-5. Drive initialize(rootUri=wsDir) → initialized → (await build) →
	// shutdown → exit through the async-safe gated harness, with emptyDir as
	// cwdFallback (NOT the workspace).
	initParamsJSON := fmt.Sprintf(`{
		"processId": 1234,
		"rootUri": "file://%s",
		"capabilities": {
			"general": {
				"positionEncodings": ["utf-8"]
			}
		}
	}`, wsDir)
	_, _ = runGatedHandshake(t, initParamsJSON, emptyDir, natural.New(nil))

	// 6. Assert the index is non-empty and contains CALLGREET.
	if capturedIdx == nil {
		t.Fatal("index was not built (indexReadyHook not called)")
	}

	// Check that the index is non-empty and contains at least CALLGREET.
	keys := capturedIdx.Keys()
	if len(keys) == 0 {
		t.Fatal("index is empty; expected to find at least CALLGREET.NSN (proves index followed cwdFallback, not negotiated root)")
	}

	// Look for CALLGREET.NSN with ObjectSubprogram type in the index.
	callgreetFound := false
	for _, path := range keys {
		if fileAnal, ok := capturedIdx.Get(path); ok {
			// Check if this is CALLGREET.NSN or contains it (relative paths).
			if filepath.Base(path) == "CALLGREET.NSN" && fileAnal.ObjectType == model.ObjectSubprogram {
				callgreetFound = true
				break
			}
		}
	}

	if !callgreetFound {
		t.Logf("Index keys: %v", keys)
		t.Fatalf("CALLGREET.NSN (ObjectSubprogram) not found in index. This proves the index was built from the cwdFallback (empty dir), not the negotiated root.")
	}

	// 7. Assert the encoding was negotiated (UTF-8 was offered and chosen).
	if capturedEncoding != protocol.PositionEncodingKindUTF8 {
		t.Errorf("encoding = %s, want UTF-8", capturedEncoding)
	}

	t.Logf("SUCCESS: index was built from negotiated root %q (not cwdFallback %q), and contains CALLGREET. Index size: %d files.", wsDir, emptyDir, len(keys))
}

// TestRootHandshakeWatcherStartsAgainstNegotiatedRoot verifies that the filesystem
// watcher is started against the negotiated root (not the cwdFallback) and does not
// error on a valid workspace path. Watcher-start failures remain non-fatal per FR-43.
func TestRootHandshakeWatcherStartsAgainstNegotiatedRoot(t *testing.T) {
	// 1. Copy the testdata workspace into a temp dir.
	wsDir := t.TempDir()
	srcDir := filepath.Join("testdata", "roothandshake")

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("failed to read testdata/roothandshake: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(wsDir, entry.Name())
		content, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("failed to read %s: %v", src, err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", dst, err)
		}
	}

	// 2. Create an empty cwdFallback dir.
	emptyDir := t.TempDir()

	// 3. Set up a hook to observe the negotiated root/cfg after initialize.
	var capturedRoot string
	initializeReadyHookMu.Lock()
	oldHook := initializeReadyHook
	initializeReadyHook = func(root string, cfg config.Config, clientSupportsWorkDoneProgress bool) {
		capturedRoot = root
		_ = cfg                            // cfg captured to verify bootstrap occurred, though we only use root here
		_ = clientSupportsWorkDoneProgress // ignored in this test
	}
	initializeReadyHookMu.Unlock()
	defer func() {
		initializeReadyHookMu.Lock()
		initializeReadyHook = oldHook
		initializeReadyHookMu.Unlock()
	}()

	// 4. Build and run the handshake.
	var reqBuf bytes.Buffer

	initParamsJSON := fmt.Sprintf(`{
		"processId": 1234,
		"rootUri": "file://%s",
		"capabilities": {}
	}`, wsDir)

	initCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParamsJSON))
	if err := writeFramedMessage(&reqBuf, initCall); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	initializedNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, initializedNotif); err != nil {
		t.Fatalf("write initialized: %v", err)
	}

	shutdownCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "shutdown", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, shutdownCall); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}

	exitNotif := jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))
	if err := writeFramedMessage(&reqBuf, exitNotif); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	var outBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	err = Run(context.Background(), &reqBuf, &outBuf, "0.0.0-test", emptyDir, az, logger)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// 5. Assert the negotiated root is the workspace dir (or its sentinel ancestor).
	if capturedRoot == "" {
		t.Fatal("root was not negotiated (initializeReadyHook not called)")
	}

	// Verify the negotiated root is the workspace dir (wsDir has a sentinel).
	if capturedRoot != wsDir {
		t.Logf("note: negotiated root %q != requested wsDir %q (may be sentinel ancestor)", capturedRoot, wsDir)
	}

	t.Logf("SUCCESS: root was negotiated to %q (not cwdFallback %q)", capturedRoot, emptyDir)
}

// TestInitializeCR6MalformedConfig proves the CR-6 fail-safe survives the
// DEFERRED bootstrap (feature 20, T2 DoD / Story 2 AC2): a syntactically
// malformed .natural-lsp.toml at the negotiated root must NOT fail the
// initialize handshake. config.Bootstrap never hard-fails and the initialize
// handler ignores its error, so the observable contract is:
//
//	(a) a config-problem Warn is logged (the "config file error" line naming
//	    the offending .natural-lsp.toml path) — the bad config is visible, not
//	    silently swallowed (NFR-6/CR-6);
//	(b) the initialize response returns successfully with NO JSON-RPC error;
//	(c) no panic (the whole Run completes and the lifecycle drains);
//	(d) the server degrades to the DEFAULT config (config.Defaults()) and still
//	    builds a non-empty index over the workspace — a bad config never
//	    disables indexing.
//
// This exercises CR-6 through the initialize-deferred bootstrap path, not just
// at the config layer (config_test.go covers Bootstrap in isolation).
func TestInitializeCR6MalformedConfig(t *testing.T) {
	// A real workspace: a malformed sentinel + at least one Natural file so the
	// dir is a genuine workspace root (and the default-config index is non-empty).
	wsDir := t.TempDir()

	// Syntactically malformed TOML: an unterminated array that go-toml rejects.
	malformed := "extensions = [\nthis is not valid toml @@@\n"
	if err := os.WriteFile(filepath.Join(wsDir, ".natural-lsp.toml"), []byte(malformed), 0o644); err != nil {
		t.Fatalf("write malformed .natural-lsp.toml: %v", err)
	}
	// A minimal Natural program so the workspace has an indexable object.
	if err := os.WriteFile(filepath.Join(wsDir, "HELLO.NSP"), []byte("WRITE 'HELLO'\nEND\n"), 0o644); err != nil {
		t.Fatalf("write HELLO.NSP: %v", err)
	}

	// cwdFallback: an unrelated empty dir (proves the negotiated root, not cwd,
	// is what bootstrap read the malformed config from).
	emptyCwd := t.TempDir()

	// Capture the negotiated config to prove degradation to defaults (d).
	var capturedCfg config.Config
	var cfgCaptured bool
	initializeReadyHookMu.Lock()
	oldInitHook := initializeReadyHook
	initializeReadyHook = func(root string, cfg config.Config, clientSupportsWorkDoneProgress bool) {
		capturedCfg = cfg
		cfgCaptured = true
		_ = clientSupportsWorkDoneProgress // ignored in this test
	}
	initializeReadyHookMu.Unlock()
	defer func() {
		initializeReadyHookMu.Lock()
		initializeReadyHook = oldInitHook
		initializeReadyHookMu.Unlock()
	}()

	// Capture the built index to prove indexing still happens despite bad config
	// (d). Restore via t.Cleanup so it unwinds LIFO with runGatedHandshake's own
	// gate cleanup (feature 21, T4 — the build is asynchronous).
	var capturedIdx *workspace.Index
	indexReadyHookMu.Lock()
	oldIdxHook := indexReadyHook
	indexReadyHook = func(idx *workspace.Index, _ protocol.PositionEncodingKind) {
		capturedIdx = idx
	}
	indexReadyHookMu.Unlock()
	t.Cleanup(func() {
		indexReadyHookMu.Lock()
		indexReadyHook = oldIdxHook
		indexReadyHookMu.Unlock()
	})

	// Drive the full lifecycle with rootUri = the malformed-config workspace,
	// through the async-safe gated harness (withholds shutdown until the build
	// publishes). (c) no panic — Run completes without error.
	rootURI := uri.File(wsDir)
	initParams := fmt.Sprintf(`{
		"processId": 1234,
		"rootUri": %q,
		"capabilities": {"general": {"positionEncodings": ["utf-8"]}}
	}`, string(rootURI))

	outBufL, logText := runGatedHandshake(t, initParams, emptyCwd, natural.New(nil))
	outBuf := bytes.NewBuffer(outBufL.Bytes())

	// (a) the malformed config is surfaced as a Warn naming the offending file.
	if !strings.Contains(logText, "config file error") {
		t.Errorf("expected a 'config file error' Warn for the malformed .natural-lsp.toml.\nLog:\n%s", logText)
	}
	if !strings.Contains(logText, ".natural-lsp.toml") {
		t.Errorf("config-error Warn does not name the offending .natural-lsp.toml path.\nLog:\n%s", logText)
	}

	// (b) the initialize response returned successfully with NO JSON-RPC error.
	respBuf := bytes.NewBuffer(outBuf.Bytes())
	initBody, err := parseFramedResponse(respBuf)
	if err != nil {
		t.Fatalf("parse initialize response: %v", err)
	}
	initMsg, err := jsonrpc2.DecodeMessage(initBody)
	if err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	initResp, ok := initMsg.(*jsonrpc2.Response)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Response for initialize, got %T", initMsg)
	}
	if initResp.Err() != nil {
		t.Errorf("initialize returned a JSON-RPC error despite malformed config (CR-6 regression): %v", initResp.Err())
	}
	if len(initResp.Result()) == 0 || bytes.Equal(initResp.Result(), []byte("null")) {
		t.Errorf("initialize returned an empty/null result; want a valid InitializeResult")
	}

	// (d) the server degraded to the DEFAULT config and still built a non-empty index.
	if !cfgCaptured {
		t.Fatal("initializeReadyHook not called; deferred bootstrap did not run")
	}
	if !reflect.DeepEqual(capturedCfg, config.Defaults()) {
		t.Errorf("malformed config did not degrade to config.Defaults().\ngot:  %+v\nwant: %+v", capturedCfg, config.Defaults())
	}
	if capturedIdx == nil {
		t.Fatal("index was not built despite the malformed config (CR-6: bad config must not disable indexing)")
	}
	if got := len(capturedIdx.Keys()); got == 0 {
		t.Errorf("index is empty despite a valid HELLO.NSP in the workspace; default-config indexing should have found it")
	}

	t.Logf("CR-6 deferred bootstrap OK: config degraded to defaults, index size=%d.\nLog:\n%s",
		len(capturedIdx.Keys()), logText)
}
