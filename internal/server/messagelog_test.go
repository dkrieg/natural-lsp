package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	gojson "github.com/go-json-experiment/json"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// TestTraceValueToLevel_RoundTrip verifies that traceValueToLevel and
// levelToTraceValue are inverses for the three known constants.
func TestTraceValueToLevel_RoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		value protocol.TraceValue
		want  int32
	}{
		{name: "off", value: protocol.TraceValueOff, want: 0},
		{name: "messages", value: protocol.TraceValueMessages, want: 1},
		{name: "verbose", value: protocol.TraceValueVerbose, want: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Forward conversion
			level := traceValueToLevel(tc.value)
			if level != tc.want {
				t.Errorf("traceValueToLevel(%q) = %d, want %d", tc.value, level, tc.want)
			}

			// Reverse conversion
			back := levelToTraceValue(level)
			if back != tc.value {
				t.Errorf("levelToTraceValue(%d) = %q, want %q", level, back, tc.value)
			}
		})
	}
}

// TestTraceValueToLevel_UnknownMapsToOff verifies that unknown/garbage strings
// map to off (0).
func TestTraceValueToLevel_UnknownMapsToOff(t *testing.T) {
	cases := []struct {
		name  string
		value protocol.TraceValue
	}{
		{name: "empty string", value: ""},
		{name: "bogus value", value: "bogus"},
		{name: "random string", value: "xyz"},
		{name: "off with extra", value: "off extra"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			level := traceValueToLevel(tc.value)
			if level != 0 {
				t.Errorf("traceValueToLevel(%q) = %d, want 0 (off)", tc.value, level)
			}

			// Reverse should yield off
			back := levelToTraceValue(level)
			if back != protocol.TraceValueOff {
				t.Errorf("levelToTraceValue(0) = %q, want %q", back, protocol.TraceValueOff)
			}
		})
	}
}

// TestLogMessage_WritesOneFrame verifies that logMessage writes exactly one
// window/logMessage frame with the correct method, type, and message.
func TestLogMessage_WritesOneFrame(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	mlog := newMessageLogger(stream, logger, protocol.TraceValueOff)

	// Log an Info message
	testMsg := "This is a test message"
	mlog.logMessage(ctx, protocol.MessageTypeInfo, testMsg)

	// Decode the framed message
	body := parseNextFramedMessage(t, &buf)

	// Decode the notification
	var notif struct {
		Method string                    `json:"method"`
		Params protocol.LogMessageParams `json:"params"`
	}
	if err := gojson.Unmarshal(body, &notif); err != nil {
		t.Fatalf("failed to unmarshal notification: %v", err)
	}

	// Assert method is window/logMessage
	if notif.Method != "window/logMessage" {
		t.Errorf("method = %q, want %q", notif.Method, "window/logMessage")
	}

	// Assert type is Info (3)
	if notif.Params.Type != protocol.MessageTypeInfo {
		t.Errorf("type = %v, want %v", notif.Params.Type, protocol.MessageTypeInfo)
	}

	// Assert message is exact
	if notif.Params.Message != testMsg {
		t.Errorf("message = %q, want %q", notif.Params.Message, testMsg)
	}

	// Assert no more messages in buffer
	remaining := buf.String()
	if remaining != "" {
		t.Errorf("expected no more messages, but buffer has: %q", remaining)
	}
}

// TestLogTrace_VerboseNil verifies that logTrace with verbose==nil omits
// the verbose key from the JSON.
func TestLogTrace_VerboseNil(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	mlog := newMessageLogger(stream, logger, protocol.TraceValueMessages)

	testMsg := "Trace message without verbose"
	mlog.logTrace(ctx, testMsg, nil)

	// Decode the framed message
	body := parseNextFramedMessage(t, &buf)

	// Decode the notification
	var notif struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &notif); err != nil {
		t.Fatalf("failed to unmarshal notification: %v", err)
	}

	// Assert method is $/logTrace
	if notif.Method != "$/logTrace" {
		t.Errorf("method = %q, want %q", notif.Method, "$/logTrace")
	}

	// Decode the params to check for verbose key
	var params struct {
		Message string  `json:"message"`
		Verbose *string `json:"verbose"`
	}
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	// Assert message is present
	if params.Message != testMsg {
		t.Errorf("message = %q, want %q", params.Message, testMsg)
	}

	// Assert verbose is absent (nil)
	if params.Verbose != nil {
		t.Errorf("verbose key present with value %q, want it absent", *params.Verbose)
	}

	// Assert no "verbose" key in the raw JSON
	if strings.Contains(string(notif.Params), `"verbose"`) {
		t.Errorf("raw JSON should not contain \"verbose\" key: %s", notif.Params)
	}
}

// TestLogTrace_VerbosePresent verifies that logTrace with verbose!=nil includes
// the verbose key in the JSON.
func TestLogTrace_VerbosePresent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	mlog := newMessageLogger(stream, logger, protocol.TraceValueVerbose)

	testMsg := "Trace message with verbose"
	verboseText := "verbose details"
	mlog.logTrace(ctx, testMsg, &verboseText)

	// Decode the framed message
	body := parseNextFramedMessage(t, &buf)

	// Decode the notification
	var notif struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &notif); err != nil {
		t.Fatalf("failed to unmarshal notification: %v", err)
	}

	// Assert method is $/logTrace
	if notif.Method != "$/logTrace" {
		t.Errorf("method = %q, want %q", notif.Method, "$/logTrace")
	}

	// Decode the params to check for verbose key
	var params struct {
		Message string  `json:"message"`
		Verbose *string `json:"verbose"`
	}
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	// Assert message is present
	if params.Message != testMsg {
		t.Errorf("message = %q, want %q", params.Message, testMsg)
	}

	// Assert verbose is present and correct
	if params.Verbose == nil {
		t.Errorf("verbose key absent, want %q", verboseText)
	} else if *params.Verbose != verboseText {
		t.Errorf("verbose = %q, want %q", *params.Verbose, verboseText)
	}

	// Assert "verbose" key is present in the raw JSON
	if !strings.Contains(string(notif.Params), `"verbose"`) {
		t.Errorf("raw JSON should contain \"verbose\" key: %s", notif.Params)
	}
}

// TestLogMessage_StreamWriteError verifies that a write error does not panic
// and does not return an error; instead it logs a Warn to stderr.
func TestLogMessage_StreamWriteError(t *testing.T) {
	// Use a logger that writes to a buffer so we can assert the Warn was logged
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Use a failing stream
	failingStream := jsonrpc2.NewHeaderStream(&failingReadWriteCloser{})

	ctx := context.Background()
	mlog := newMessageLogger(failingStream, logger, protocol.TraceValueOff)

	// Call logMessage; it should NOT panic and should NOT return an error
	mlog.logMessage(ctx, protocol.MessageTypeInfo, "test message")

	// Assert that the logger recorded a Warn
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "failed to send window/logMessage") {
		t.Errorf("expected Warn log for write failure, got: %q", logOutput)
	}
}

// TestLogTrace_StreamWriteError verifies that a logTrace write error does not panic
// and does not return an error; instead it logs a Warn to stderr.
func TestLogTrace_StreamWriteError(t *testing.T) {
	// Use a logger that writes to a buffer so we can assert the Warn was logged
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Use a failing stream
	failingStream := jsonrpc2.NewHeaderStream(&failingReadWriteCloser{})

	ctx := context.Background()
	mlog := newMessageLogger(failingStream, logger, protocol.TraceValueVerbose)

	// Call logTrace; it should NOT panic and should NOT return an error
	mlog.logTrace(ctx, "test trace", nil)

	// Assert that the logger recorded a Warn
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "failed to send $/logTrace") {
		t.Errorf("expected Warn log for write failure, got: %q", logOutput)
	}
}

// TestMessageLogger_NilReceiver verifies that calling logMessage/logTrace on a
// nil *messageLogger is a no-op and does not panic.
func TestMessageLogger_NilReceiver(t *testing.T) {
	var mlog *messageLogger
	ctx := context.Background()

	// These should all be no-ops and not panic
	mlog.logMessage(ctx, protocol.MessageTypeInfo, "test")
	mlog.logTrace(ctx, "test", nil)

	// If we got here, no panic occurred
}

// TestMessageLogger_NilStream verifies that calling logMessage/logTrace on a
// messageLogger with a nil stream is a no-op and does not panic.
func TestMessageLogger_NilStream(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mlog := &messageLogger{
		stream: nil,
		logger: logger,
	}
	ctx := context.Background()

	// These should all be no-ops and not panic
	mlog.logMessage(ctx, protocol.MessageTypeInfo, "test")
	mlog.logTrace(ctx, "test", nil)

	// If we got here, no panic occurred
}

// TestSetTrace_SetAndRead verifies that setTrace and trace work correctly.
func TestSetTrace_SetAndRead(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)

	mlog := newMessageLogger(stream, logger, protocol.TraceValueOff)

	// Verify initial state is off
	if level := mlog.trace(); level != protocol.TraceValueOff {
		t.Errorf("initial trace() = %q, want %q", level, protocol.TraceValueOff)
	}

	// Set to messages
	mlog.setTrace(protocol.TraceValueMessages)
	if level := mlog.trace(); level != protocol.TraceValueMessages {
		t.Errorf("trace() after setTrace(messages) = %q, want %q", level, protocol.TraceValueMessages)
	}

	// Set to verbose
	mlog.setTrace(protocol.TraceValueVerbose)
	if level := mlog.trace(); level != protocol.TraceValueVerbose {
		t.Errorf("trace() after setTrace(verbose) = %q, want %q", level, protocol.TraceValueVerbose)
	}

	// Set to unknown (should become off)
	mlog.setTrace("bogus")
	if level := mlog.trace(); level != protocol.TraceValueOff {
		t.Errorf("trace() after setTrace(bogus) = %q, want %q (off)", level, protocol.TraceValueOff)
	}
}
