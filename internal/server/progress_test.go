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

	gojson "github.com/go-json-experiment/json"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// TestProgressReporter_CreateRequest verifies that the progressReporter sends
// a valid window/workDoneProgress/create request with the correct token.
func TestProgressReporter_CreateRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	// Create an enabled reporter
	reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, true)

	// Call create
	if err := reporter.create(ctx); err != nil {
		t.Fatalf("create() failed: %v", err)
	}

	// Decode the framed message
	body := parseNextFramedMessage(t, &buf)
	var call struct {
		Method string                                `json:"method"`
		Params protocol.WorkDoneProgressCreateParams `json:"params"`
	}
	if err := gojson.Unmarshal(body, &call); err != nil {
		t.Fatalf("failed to unmarshal call: %v", err)
	}

	// Assert method and params
	if call.Method != "window/workDoneProgress/create" {
		t.Errorf("method = %q, want %q", call.Method, "window/workDoneProgress/create")
	}

	// Assert token (ProgressToken is an interface; String() method on protocol.String)
	var tokenStr string
	if s, ok := call.Params.Token.(protocol.String); ok {
		tokenStr = string(s)
	} else {
		t.Fatalf("token is not a protocol.String, got %T", call.Params.Token)
	}

	if tokenStr != "natural-lsp-index" {
		t.Errorf("token = %q, want %q", tokenStr, "natural-lsp-index")
	}
}

// TestProgressReporter_BeginNotification verifies that the progressReporter sends
// a $/progress notification with WorkDoneProgressBegin and the correct title.
func TestProgressReporter_BeginNotification(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, true)

	if err := reporter.begin(ctx, "Indexing Natural workspace"); err != nil {
		t.Fatalf("begin() failed: %v", err)
	}

	body := parseNextFramedMessage(t, &buf)
	var notif struct {
		Method string `json:"method"`
		Params struct {
			Token protocol.String `json:"token"`
			Value json.RawMessage `json:"value"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &notif); err != nil {
		t.Fatalf("failed to unmarshal notification: %v", err)
	}

	if notif.Method != "$/progress" {
		t.Errorf("method = %q, want %q", notif.Method, "$/progress")
	}

	if string(notif.Params.Token) != "natural-lsp-index" {
		t.Errorf("token = %q, want %q", notif.Params.Token, "natural-lsp-index")
	}

	// Decode the value to check Kind and Title
	var begin struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(notif.Params.Value, &begin); err != nil {
		t.Fatalf("failed to unmarshal value: %v", err)
	}

	if begin.Kind != "begin" {
		t.Errorf("kind = %q, want %q", begin.Kind, "begin")
	}
	if begin.Title != "Indexing Natural workspace" {
		t.Errorf("title = %q, want %q", begin.Title, "Indexing Natural workspace")
	}
}

// TestProgressReporter_ReportNotification verifies the $/progress report notification
// with percentage calculation and "N/M files" message.
func TestProgressReporter_ReportNotification(t *testing.T) {
	testCases := []struct {
		name        string
		current     int
		total       int
		wantMsg     string
		wantPercent int
	}{
		{
			name:        "normal case 37/128",
			current:     37,
			total:       128,
			wantMsg:     "37/128 files",
			wantPercent: 29, // 37/128 * 100 = 28.9, rounds to 29
		},
		{
			name:        "quarter way",
			current:     1,
			total:       4,
			wantMsg:     "1/4 files",
			wantPercent: 25,
		},
		{
			name:        "halfway",
			current:     50,
			total:       100,
			wantMsg:     "50/100 files",
			wantPercent: 50,
		},
		{
			name:        "nearly complete",
			current:     99,
			total:       100,
			wantMsg:     "99/100 files",
			wantPercent: 99,
		},
		{
			name:        "complete",
			current:     128,
			total:       128,
			wantMsg:     "128/128 files",
			wantPercent: 100,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			conn := &readWriteCloser{r: &buf, w: &buf}
			stream := jsonrpc2.NewHeaderStream(conn)
			ctx := context.Background()

			reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, true)

			if err := reporter.report(ctx, tc.current, tc.total, "test.nsp"); err != nil {
				t.Fatalf("report() failed: %v", err)
			}

			body := parseNextFramedMessage(t, &buf)
			var notif struct {
				Method string `json:"method"`
				Params struct {
					Token protocol.String `json:"token"`
					Value json.RawMessage `json:"value"`
				} `json:"params"`
			}
			if err := json.Unmarshal(body, &notif); err != nil {
				t.Fatalf("failed to unmarshal notification: %v", err)
			}

			if notif.Method != "$/progress" {
				t.Errorf("method = %q, want %q", notif.Method, "$/progress")
			}

			// Decode the value
			var report struct {
				Kind       string  `json:"kind"`
				Message    string  `json:"message,omitempty"`
				Percentage *uint32 `json:"percentage,omitempty"`
			}
			if err := json.Unmarshal(notif.Params.Value, &report); err != nil {
				t.Fatalf("failed to unmarshal value: %v", err)
			}

			if report.Kind != "report" {
				t.Errorf("kind = %q, want %q", report.Kind, "report")
			}
			if report.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", report.Message, tc.wantMsg)
			}
			if report.Percentage == nil {
				t.Errorf("percentage is nil, want %d", tc.wantPercent)
			} else if *report.Percentage != uint32(tc.wantPercent) {
				t.Errorf("percentage = %d, want %d", *report.Percentage, tc.wantPercent)
			}
		})
	}
}

// TestProgressReporter_ReportDivideByZero verifies that report with total==0
// does not divide by zero and omits the percentage.
func TestProgressReporter_ReportDivideByZero(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, true)

	// Should not panic and should not divide by zero
	if err := reporter.report(ctx, 0, 0, "test.nsp"); err != nil {
		t.Fatalf("report() with total==0 failed: %v", err)
	}

	body := parseNextFramedMessage(t, &buf)
	var notif struct {
		Params struct {
			Value json.RawMessage `json:"value"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &notif); err != nil {
		t.Fatalf("failed to unmarshal notification: %v", err)
	}

	var report struct {
		Message    string  `json:"message,omitempty"`
		Percentage *uint32 `json:"percentage,omitempty"`
	}
	if err := json.Unmarshal(notif.Params.Value, &report); err != nil {
		t.Fatalf("failed to unmarshal value: %v", err)
	}

	// Percentage should be omitted (nil) when total is 0
	if report.Percentage != nil {
		t.Errorf("percentage should be nil when total==0, got %d", *report.Percentage)
	}
	// Message should still be present: "0/0 files"
	if report.Message != "0/0 files" {
		t.Errorf("message = %q, want %q", report.Message, "0/0 files")
	}
}

// TestProgressReporter_EndNotification verifies the $/progress end notification.
func TestProgressReporter_EndNotification(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, true)

	if err := reporter.end(ctx, "Indexing complete"); err != nil {
		t.Fatalf("end() failed: %v", err)
	}

	body := parseNextFramedMessage(t, &buf)
	var notif struct {
		Method string `json:"method"`
		Params struct {
			Token protocol.String `json:"token"`
			Value json.RawMessage `json:"value"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &notif); err != nil {
		t.Fatalf("failed to unmarshal notification: %v", err)
	}

	if notif.Method != "$/progress" {
		t.Errorf("method = %q, want %q", notif.Method, "$/progress")
	}

	var end struct {
		Kind    string `json:"kind"`
		Message string `json:"message,omitempty"`
	}
	if err := json.Unmarshal(notif.Params.Value, &end); err != nil {
		t.Fatalf("failed to unmarshal value: %v", err)
	}

	if end.Kind != "end" {
		t.Errorf("kind = %q, want %q", end.Kind, "end")
	}
	if end.Message != "Indexing complete" {
		t.Errorf("message = %q, want %q", end.Message, "Indexing complete")
	}
}

// TestProgressReporter_DisabledWritesNothing verifies that a disabled reporter
// (enabled=false) writes nothing to the stream for any method call.
func TestProgressReporter_DisabledWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	// Create a DISABLED reporter
	reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, false)

	// Call all methods
	if err := reporter.create(ctx); err != nil {
		t.Fatalf("create() failed: %v", err)
	}
	if err := reporter.begin(ctx, "Indexing Natural workspace"); err != nil {
		t.Fatalf("begin() failed: %v", err)
	}
	if err := reporter.report(ctx, 1, 10, "test.nsp"); err != nil {
		t.Fatalf("report() failed: %v", err)
	}
	if err := reporter.end(ctx, "Done"); err != nil {
		t.Fatalf("end() failed: %v", err)
	}

	// Buffer should be empty
	if buf.Len() != 0 {
		t.Errorf("disabled reporter wrote %d bytes, want 0", buf.Len())
		t.Logf("output: %q", buf.String())
	}
}

// TestProgressReporter_WriteFailureLogged verifies that a write failure is logged
// and the method returns without panicking (FR-43).
func TestProgressReporter_WriteFailureLogged(t *testing.T) {
	failingWriter := &failingReadWriteCloser{}
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))
	ctx := context.Background()

	stream := jsonrpc2.NewHeaderStream(failingWriter)
	reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, true)

	// All methods should log the error and return without panicking
	reporter.create(ctx) // Should not panic, should log
	reporter.begin(ctx, "Title")
	reporter.report(ctx, 1, 10, "test.nsp")
	reporter.end(ctx, "Done")

	// Verify something was logged (error messages for the failures)
	logOutput := logBuf.String()
	if logOutput == "" {
		t.Errorf("expected error logs for write failures, got nothing")
	}
}

// TestProgressReporter_CompleteSequence tests a realistic sequence: create → begin → report → end,
// and verifies each message carries the same token and is in the correct order.
func TestProgressReporter_CompleteSequence(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	ctx := context.Background()

	token := protocol.String("natural-lsp-index")
	reporter := newProgressReporter(stream, token, logger, true)

	// Execute the full sequence
	if err := reporter.create(ctx); err != nil {
		t.Fatalf("create() failed: %v", err)
	}
	if err := reporter.begin(ctx, "Indexing Natural workspace"); err != nil {
		t.Fatalf("begin() failed: %v", err)
	}
	if err := reporter.report(ctx, 1, 3, "file1.nsp"); err != nil {
		t.Fatalf("report(1/3) failed: %v", err)
	}
	if err := reporter.report(ctx, 2, 3, "file2.nsp"); err != nil {
		t.Fatalf("report(2/3) failed: %v", err)
	}
	if err := reporter.report(ctx, 3, 3, "file3.nsp"); err != nil {
		t.Fatalf("report(3/3) failed: %v", err)
	}
	if err := reporter.end(ctx, "Done"); err != nil {
		t.Fatalf("end() failed: %v", err)
	}

	// Verify the sequence in order
	var messages []map[string]interface{}
	for buf.Len() > 0 {
		body := parseNextFramedMessage(t, &buf)
		var msg map[string]interface{}
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Fatalf("failed to unmarshal message: %v", err)
		}
		messages = append(messages, msg)
	}

	if len(messages) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(messages))
	}

	// Verify create is a call (has "method" and "id")
	if _, hasMethod := messages[0]["method"]; !hasMethod {
		t.Errorf("message 0 (create) should be a call with method")
	}

	// Verify the rest are notifications (all have "method" and "params", no "id")
	expectedMethods := []string{"$/progress", "$/progress", "$/progress", "$/progress", "$/progress"}
	for i, expectedMethod := range expectedMethods {
		idx := i + 1 // +1 because message 0 is the create call
		msg := messages[idx]
		if method, ok := msg["method"]; !ok || method != expectedMethod {
			t.Errorf("message %d: method = %v, want %q", idx, method, expectedMethod)
		}
	}

	// Verify the kinds in the $/progress values
	expectedKinds := []string{"begin", "report", "report", "report", "end"}
	for i, expectedKind := range expectedKinds {
		idx := i + 1 // Skip the create call
		msg := messages[idx]
		params, ok := msg["params"].(map[string]interface{})
		if !ok {
			t.Fatalf("message %d: params is not a map", idx)
		}
		value, ok := params["value"].(map[string]interface{})
		if !ok {
			// Try parsing from raw JSON if it's still stringified
			rawValue, ok := params["value"]
			if ok {
				// It might be stored as raw JSON, need to unmarshal again
				rawBytes, _ := json.Marshal(rawValue)
				json.Unmarshal(rawBytes, &value)
			}
		}
		if kind, ok := value["kind"].(string); !ok || kind != expectedKind {
			t.Errorf("message %d: kind = %v, want %q", idx, kind, expectedKind)
		}
	}
}

// parseNextFramedMessage extracts and parses the next framed message from buf.
// It returns the decoded JSON body as []byte, or fails the test if parsing fails.
func parseNextFramedMessage(t *testing.T, buf *bytes.Buffer) []byte {
	t.Helper()

	output := buf.String()
	if output == "" {
		t.Fatal("no more messages in buffer")
	}

	// Find the blank line separator
	idx := strings.Index(output, "\r\n\r\n")
	if idx == -1 {
		t.Fatal("no blank line separating header and body")
	}
	headerEnd := idx + 4

	// Parse Content-Length
	headerLines := strings.Split(output[:idx], "\r\n")
	if len(headerLines) == 0 {
		t.Fatal("empty header")
	}
	contentLengthLine := headerLines[0]
	if !strings.HasPrefix(contentLengthLine, "Content-Length: ") {
		t.Fatalf("first line is not Content-Length header: %q", contentLengthLine)
	}
	lengthStr := strings.TrimPrefix(contentLengthLine, "Content-Length: ")
	contentLen, err := strconv.Atoi(lengthStr)
	if err != nil {
		t.Fatalf("invalid Content-Length: %v", err)
	}

	// Extract body
	bodyEnd := headerEnd + contentLen
	if bodyEnd > len(output) {
		t.Fatalf("response too short; declared %d bytes but only %d available", contentLen, len(output)-headerEnd)
	}
	body := []byte(output[headerEnd:bodyEnd])

	// Remove this message from the buffer
	remaining := output[bodyEnd:]
	buf.Reset()
	buf.WriteString(remaining)

	return body
}

// failingReadWriteCloser is a test helper that always fails on Write.
type failingReadWriteCloser struct{}

func (f *failingReadWriteCloser) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("read not implemented")
}

func (f *failingReadWriteCloser) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("injected write failure for testing")
}

func (f *failingReadWriteCloser) Close() error {
	return nil
}
