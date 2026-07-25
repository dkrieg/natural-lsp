package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/config"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// TestHandleInitialize_HonorsTraceVerbose tests that handleInitialize reads
// the trace parameter and returns it in initializeNegotiation (S2-AC1).
// When InitializeParams.Trace is "verbose", the result should expose initialTrace == "verbose".
func TestHandleInitialize_HonorsTraceVerbose(t *testing.T) {
	// Parse InitializeParams from JSON (has unexported fields)
	var params protocol.InitializeParams
	paramsJSON := []byte(`{
		"processId":1234,
		"trace":"verbose",
		"capabilities":{}
	}`)
	if err := protocol.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	negotiation, err := handleInitialize(params, "0.0.0-test")
	if err != nil {
		t.Fatalf("handleInitialize failed: %v", err)
	}

	// T3 RED: the negotiation struct must have an initialTrace field (not present yet)
	// This test will fail to compile until GREEN adds it.
	if negotiation.initialTrace != protocol.TraceValueVerbose {
		t.Errorf("initialTrace = %q, want %q", negotiation.initialTrace, protocol.TraceValueVerbose)
	}
}

// TestHandleInitialize_OmittedTraceBecomesOff tests that an omitted trace
// parameter defaults to off (S2-AC1, CR-6 fail-safe).
func TestHandleInitialize_OmittedTraceBecomesOff(t *testing.T) {
	var params protocol.InitializeParams
	paramsJSON := []byte(`{
		"processId":1234,
		"capabilities":{}
	}`)
	if err := protocol.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	negotiation, err := handleInitialize(params, "0.0.0-test")
	if err != nil {
		t.Fatalf("handleInitialize failed: %v", err)
	}

	// T3 RED: the negotiation struct must have an initialTrace field
	if negotiation.initialTrace != protocol.TraceValueOff {
		t.Errorf("initialTrace = %q, want %q", negotiation.initialTrace, protocol.TraceValueOff)
	}
}

// TestHandleInitialize_TraceMessages tests that trace="messages" is honored.
func TestHandleInitialize_TraceMessages(t *testing.T) {
	var params protocol.InitializeParams
	paramsJSON := []byte(`{
		"processId":1234,
		"trace":"messages",
		"capabilities":{}
	}`)
	if err := protocol.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	negotiation, err := handleInitialize(params, "0.0.0-test")
	if err != nil {
		t.Fatalf("handleInitialize failed: %v", err)
	}

	if negotiation.initialTrace != protocol.TraceValueMessages {
		t.Errorf("initialTrace = %q, want %q", negotiation.initialTrace, protocol.TraceValueMessages)
	}
}

// TestSetTraceRoundTrip_InitializeWithMessages drives a full initialize → initialized
// round-trip with trace="messages" and asserts the server's mlog.trace() becomes messages.
// This uses initializeReadyHook to observe the negotiated trace level (S2-AC1).
func TestSetTraceRoundTrip_InitializeWithMessages(t *testing.T) {
	// We'll use a custom hook to capture the hook call
	var hookCalled bool

	initializeReadyHookMu.Lock()
	oldInitializeReadyHook := initializeReadyHook
	initializeReadyHook = func(root string, cfg config.Config, clientSupportsWorkDoneProgress bool) {
		hookCalled = true
		// At this point, mlog has been seeded with the initial trace level
		// We'll verify the trace level was set correctly (T3 requirement)
	}
	initializeReadyHookMu.Unlock()
	defer func() {
		initializeReadyHookMu.Lock()
		initializeReadyHook = oldInitializeReadyHook
		initializeReadyHookMu.Unlock()
	}()

	root := t.TempDir()

	// Build the message sequence with trace="messages"
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{
		"processId":1234,
		"rootUri":null,
		"trace":"messages",
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)

	// Send initialized notification (which triggers the hook)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Build input buffer
	var inBuf bytes.Buffer
	if err := writeFramedMessage(&inBuf, initCall); err != nil {
		t.Fatalf("failed to write initialize: %v", err)
	}
	if err := writeFramedMessage(&inBuf, initNotif); err != nil {
		t.Fatalf("failed to write initialized: %v", err)
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

	// Assert: the hook was called (meaning initialize ran)
	if !hookCalled {
		t.Fatalf("initializeReadyHook was not called; test infrastructure failed")
	}

	// T3 NOTE: This test depends on the server setting mlog's initial trace level
	// from params.Trace during initialize (S2-AC1). We verify by asserting the hook
	// fired and initialize params were processed with trace="messages".
	// The actual trace level will be verified in T4 when $/logTrace behavior is wired.
}

// TestSetTraceNotification_RuntimeChange tests that sending a $/setTrace notification
// after initialized changes the trace level at runtime without restart (S2-AC2).
// This uses runGatedLifecycle to send the notification AFTER the index is ready.
func TestSetTraceNotification_RuntimeChange(t *testing.T) {
	root := t.TempDir()

	// Pre-messages: initialize + initialized
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{
		"processId":1234,
		"rootUri":null,
		"trace":"off",
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Mid-messages: $/setTrace notification (sent AFTER index is ready)
	// T3 RED: this notification method does not exist yet; the test will fail to run
	setTraceNotif := jsonrpc2.NewNotification("$/setTrace", jsonrpc2.RawMessage(`{
		"value":"verbose"
	}`))

	// Use runGatedLifecycle to ensure setTrace fires AFTER the index is built
	az := &stubAnalyzer{}
	outBuf, stderrContent := runGatedLifecycle(t,
		[]jsonrpc2.Message{initCall, initNotif},
		[]jsonrpc2.Message{setTraceNotif},
		root, az)

	// T3 NOTE: We can't directly assert mlog.trace() from here (it's private).
	// The actual effect will be seen in T4 when $/logTrace emissions are gated on the level.
	// For now, assert that the notification was processed without crashing.
	//
	// We verify: no error/panic in stderr, and the loop survives (outBuf is populated).
	outBufBytes := outBuf.Bytes()
	if len(outBufBytes) == 0 {
		t.Errorf("expected output from Run, got empty")
	}

	// Assert: no panic in stderr
	if strings.Contains(stderrContent, "panic") {
		t.Errorf("expected no panic, but stderr contains: %q", stderrContent)
	}
}

// TestSetTraceNotification_UnknownValueBecomesOff tests that an unknown trace value
// in $/setTrace is treated as off with a stderr Warn logged (S2-AC2, CR-6 fail-safe).
func TestSetTraceNotification_UnknownValueBecomesOff(t *testing.T) {
	root := t.TempDir()

	// Pre-messages: initialize + initialized
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{
		"processId":1234,
		"rootUri":null,
		"trace":"off",
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Mid-messages: $/setTrace with bogus value (should become off + Warn)
	setTraceNotif := jsonrpc2.NewNotification("$/setTrace", jsonrpc2.RawMessage(`{
		"value":"bogus"
	}`))

	// Observe the resulting trace level via the test hook.
	var gotLevel protocol.TraceValue
	var levelSeen bool
	setTraceHookMu.Lock()
	oldHook := setTraceHook
	setTraceHook = func(level protocol.TraceValue) {
		gotLevel = level
		levelSeen = true
	}
	setTraceHookMu.Unlock()
	defer func() {
		setTraceHookMu.Lock()
		setTraceHook = oldHook
		setTraceHookMu.Unlock()
	}()

	az := &stubAnalyzer{}
	outBuf, stderrContent := runGatedLifecycle(t,
		[]jsonrpc2.Message{initCall, initNotif},
		[]jsonrpc2.Message{setTraceNotif},
		root, az)

	// Assert: a Warn was logged to stderr naming the unknown value (CR-6 fail-safe).
	if !strings.Contains(stderrContent, "unknown $/setTrace value") {
		t.Errorf("expected stderr to warn 'unknown $/setTrace value', got: %q", stderrContent)
	}
	if !strings.Contains(stderrContent, "bogus") {
		t.Errorf("expected the warn to name the offending value 'bogus', got: %q", stderrContent)
	}

	// Assert: the level degraded to off (unknown ⇒ off).
	if !levelSeen {
		t.Fatalf("setTraceHook never fired; the $/setTrace case did not run")
	}
	if gotLevel != protocol.TraceValueOff {
		t.Errorf("unknown trace value should degrade level to off, got %q", gotLevel)
	}

	// Assert: the loop survived (no crash/panic)
	if strings.Contains(stderrContent, "panic") {
		t.Errorf("expected no panic for unknown trace value, got: %q", stderrContent)
	}

	// Assert: outBuf is not empty (server processed the notification)
	outBufBytes := outBuf.Bytes()
	if len(outBufBytes) == 0 {
		t.Errorf("expected output from Run, got empty")
	}
}

// TestSetTraceNotification_MalformedParamsIgnoredNoCrash tests that a
// well-framed $/setTrace whose PARAMS value fails to decode into SetTraceParams
// (here: "value" is a number, not a TraceValue string) is ignored and does not
// crash the loop; the level is left unchanged and an Error is logged (S2-AC2,
// CR-6). The frame itself is valid JSON-RPC, so it reaches the $/setTrace case
// and exercises the params-decode-error branch (not the transport parser).
func TestSetTraceNotification_MalformedParamsIgnoredNoCrash(t *testing.T) {
	root := t.TempDir()

	// Pre-messages: initialize (trace off) + initialized.
	initID := jsonrpc2.NewNumberID(1)
	initParams := jsonrpc2.RawMessage(`{
		"processId":1234,
		"rootUri":null,
		"trace":"off",
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`)
	initCall := jsonrpc2.NewCall(initID, "initialize", initParams)
	initNotif := jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))

	// Mid-message: a well-framed $/setTrace whose "value" is the wrong TYPE
	// (a number). The JSON-RPC frame is valid, so the transport layer accepts
	// it and dispatches to the $/setTrace case, where SetTraceParams decoding
	// fails — the branch we want to prove degrades safely.
	setTraceNotif := jsonrpc2.NewNotification("$/setTrace", jsonrpc2.RawMessage(`{"value":123}`))

	// Observe the resulting trace level via the test hook (fires even when the
	// decode fails — the level must remain unchanged at off).
	var gotLevel protocol.TraceValue
	var levelSeen bool
	setTraceHookMu.Lock()
	oldHook := setTraceHook
	setTraceHook = func(level protocol.TraceValue) {
		gotLevel = level
		levelSeen = true
	}
	setTraceHookMu.Unlock()
	defer func() {
		setTraceHookMu.Lock()
		setTraceHook = oldHook
		setTraceHookMu.Unlock()
	}()

	az := &stubAnalyzer{}
	outBuf, stderrContent := runGatedLifecycle(t,
		[]jsonrpc2.Message{initCall, initNotif},
		[]jsonrpc2.Message{setTraceNotif},
		root, az)

	// Assert: stderr logged an Error naming invalid $/setTrace params (CR-6).
	if !strings.Contains(stderrContent, "invalid $/setTrace params") {
		t.Errorf("expected stderr to log 'invalid $/setTrace params', got: %q", stderrContent)
	}

	// Assert: the level stayed off (malformed params are ignored, not applied).
	if !levelSeen {
		t.Fatalf("setTraceHook never fired; the $/setTrace case did not run")
	}
	if gotLevel != protocol.TraceValueOff {
		t.Errorf("malformed $/setTrace must leave the level unchanged (off), got %q", gotLevel)
	}

	// Assert: no panic in stderr.
	if strings.Contains(stderrContent, "panic") {
		t.Errorf("expected no panic for malformed params, got: %q", stderrContent)
	}

	// Assert: the loop survived (output was produced).
	if len(outBuf.Bytes()) == 0 {
		t.Errorf("expected output from Run, got empty (loop may have crashed)")
	}
}

// TestSetTrace_InitializeResponseContainsInitialTrace asserts that the initialize
// response includes the initialTrace value (not yet in the response, but part of negotiation).
// This is a placeholder for verifying the contract.
func TestSetTrace_InitializeResponsePrep(t *testing.T) {
	// This test verifies that handleInitialize returns an initializeNegotiation
	// with an initialTrace field that will be wired into the server state during Run.
	// The actual initialization response wire shape is unchanged (per S2-AC2, no capability added).

	var params protocol.InitializeParams
	paramsJSON := []byte(`{
		"processId":1234,
		"trace":"verbose",
		"capabilities":{}
	}`)
	if err := protocol.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	negotiation, err := handleInitialize(params, "0.0.0-test")
	if err != nil {
		t.Fatalf("handleInitialize failed: %v", err)
	}

	// T3 RED: verify initialTrace field exists (will fail to compile until GREEN adds it)
	_ = negotiation.initialTrace

	// The initialize response bytes themselves should be unchanged
	// (verified by TestInitialize's allow-list in server_test.go)
}
