// Package server: window/logMessage and $/logTrace helpers for LSP-native
// observability (feature 26).
package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"unicode/utf8"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// maxTraceVerboseBytes is the byte cap for the verbose payload in $/logTrace
// notifications (feature 26, T4, OQ-4). Payloads larger than this are truncated
// with an elision marker to prevent flooding the editor's log with huge request/
// response bodies.
const maxTraceVerboseBytes = 2048

// messageLogger owns a jsonrpc2.Stream and an atomically-accessed trace level,
// and emits window/logMessage and $/logTrace notifications via fire-and-forget
// methods (never returning errors, never panicking — FR-43).
type messageLogger struct {
	stream     jsonrpc2.Stream
	logger     *slog.Logger
	traceLevel atomic.Int32
}

// traceValueToLevel maps a protocol.TraceValue string to an int32 level enum:
// off=0, messages=1, verbose=2. Unknown values map to off (0).
func traceValueToLevel(v protocol.TraceValue) int32 {
	switch v {
	case protocol.TraceValueOff, "":
		return 0
	case protocol.TraceValueMessages:
		return 1
	case protocol.TraceValueVerbose:
		return 2
	default:
		return 0 // unknown ⇒ off (CR-6 fail-safe)
	}
}

// levelToTraceValue maps an int32 level enum back to a protocol.TraceValue.
// Unknown levels default to off.
func levelToTraceValue(level int32) protocol.TraceValue {
	switch level {
	case 0:
		return protocol.TraceValueOff
	case 1:
		return protocol.TraceValueMessages
	case 2:
		return protocol.TraceValueVerbose
	default:
		return protocol.TraceValueOff
	}
}

// newMessageLogger creates a messageLogger with an initial trace level.
func newMessageLogger(stream jsonrpc2.Stream, logger *slog.Logger, initial protocol.TraceValue) *messageLogger {
	m := &messageLogger{
		stream: stream,
		logger: logger,
	}
	m.traceLevel.Store(traceValueToLevel(initial))
	return m
}

// setTrace sets the trace level from a protocol.TraceValue.
// Unknown values are mapped to off (0).
func (m *messageLogger) setTrace(v protocol.TraceValue) {
	m.traceLevel.Store(traceValueToLevel(v))
}

// trace returns the current trace level as a protocol.TraceValue.
func (m *messageLogger) trace() protocol.TraceValue {
	return levelToTraceValue(m.traceLevel.Load())
}

// logMessage sends a window/logMessage notification with the given type and message.
// Nil-safe: if m is nil or m.stream is nil, this is a no-op.
// Fire-and-forget: errors are logged to stderr and never returned or panic.
func (m *messageLogger) logMessage(ctx context.Context, typ protocol.MessageType, msg string) {
	if m == nil || m.stream == nil {
		return
	}

	// Build the params
	params := protocol.LogMessageParams{
		Type:    typ,
		Message: msg,
	}

	// Marshal via MarshalJSONTo (json/v2 path)
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := params.MarshalJSONTo(enc); err != nil {
		m.logger.Warn("failed to marshal window/logMessage params", "err", err)
		return
	}

	// Send the notification
	notif := jsonrpc2.NewNotification("window/logMessage", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := m.stream.Write(ctx, notif); err != nil {
		m.logger.Warn("failed to send window/logMessage", "err", err)
		return
	}
}

// logTrace sends a $/logTrace notification with the given message and optional verbose text.
// Nil-safe: if m is nil or m.stream is nil, this is a no-op.
// Fire-and-forget: errors are logged to stderr and never returned or panic.
func (m *messageLogger) logTrace(ctx context.Context, message string, verbose *string) {
	if m == nil || m.stream == nil {
		return
	}

	// Build the params
	params := protocol.LogTraceParams{
		Message: message,
		Verbose: verbose,
	}

	// Marshal via MarshalJSONTo (json/v2 path)
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := params.MarshalJSONTo(enc); err != nil {
		m.logger.Warn("failed to marshal $/logTrace params", "err", err)
		return
	}

	// Send the notification
	notif := jsonrpc2.NewNotification("$/logTrace", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := m.stream.Write(ctx, notif); err != nil {
		m.logger.Warn("failed to send $/logTrace", "err", err)
		return
	}
}

// traceSummary truncates a raw byte payload (e.g. request/response params) to
// maxTraceVerboseBytes and appends an elision marker if truncated. It never panics
// and handles invalid UTF-8 gracefully by truncating at byte boundaries.
//
// For payloads <= maxTraceVerboseBytes, the output is the entire payload as a string.
// For larger payloads, the output is the first maxTraceVerboseBytes bytes (carefully
// not split mid-rune) followed by "… (N bytes elided)" where N is the number of
// elided bytes.
//
// This is used to bound the verbose field in $/logTrace notifications (feature 26, T4, OQ-4).
func traceSummary(raw []byte) string {
	if len(raw) <= maxTraceVerboseBytes {
		return string(raw)
	}

	// Truncate at maxTraceVerboseBytes, but be careful not to split a multi-byte UTF-8 rune.
	// Walk backwards from maxTraceVerboseBytes to find a valid rune boundary.
	truncAt := maxTraceVerboseBytes
	for truncAt > 0 && !utf8.RuneStart(raw[truncAt]) {
		truncAt--
	}

	elided := len(raw) - truncAt
	elisionMarker := fmt.Sprintf("… (%d bytes elided)", elided)
	return string(raw[:truncAt]) + elisionMarker
}
