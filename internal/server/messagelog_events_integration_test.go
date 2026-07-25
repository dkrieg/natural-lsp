package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// realAZ returns the production Natural analyzer so build/resolution behavior
// (skips, ambiguity) is exercised for real rather than through the stub.
func realAZ() *natural.Analyzer { return natural.New(nil) }

// TestLogMessage_BuildBeginInfo asserts that a build-BEGIN Info window/logMessage
// is emitted before the build work (feature 26, Story 1, S1-AC1). The begin
// message names the workspace root.
func TestLogMessage_BuildBeginInfo(t *testing.T) {
	root := copyFixture(t, "testdata/navigation")

	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	outBuf, stderrContent := runGatedHandshake(t, initParams, root, realAZ())

	msgs := extractWindowLogMessages(t, outBuf)
	foundBegin := false
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeInfo &&
			strings.Contains(m.Message, "building workspace index") {
			foundBegin = true
			break
		}
	}
	if !foundBegin {
		t.Errorf("expected a build-BEGIN Info window/logMessage ('building workspace index'), got: %v", msgs)
	}

	// Dual sink (S1-AC2): the same event is on stderr.
	if !strings.Contains(stderrContent, "building workspace index") {
		t.Errorf("expected stderr to mention 'building workspace index', got: %q", stderrContent)
	}
}

// TestLogMessage_WarmVsRebuildSignal asserts that the build-end Info
// window/logMessage distinguishes a warm cache hit from a rebuild (feature 26,
// Story 1, S1-AC1). A cold first build reports "rebuild".
func TestLogMessage_WarmVsRebuildSignal(t *testing.T) {
	root := copyFixture(t, "testdata/navigation")

	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	outBuf, stderrContent := runGatedHandshake(t, initParams, root, realAZ())

	msgs := extractWindowLogMessages(t, outBuf)
	foundOutcome := false
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeInfo &&
			(strings.Contains(m.Message, "rebuild") || strings.Contains(m.Message, "warm cache hit")) {
			foundOutcome = true
			break
		}
	}
	if !foundOutcome {
		t.Errorf("expected a warm-vs-rebuild signal in a window/logMessage, got: %v", msgs)
	}

	// Dual sink: stderr carries the warm boolean.
	if !strings.Contains(stderrContent, "warm=") {
		t.Errorf("expected stderr build-end line to carry warm= field, got: %q", stderrContent)
	}
}

// TestLogMessage_SkipAggregateWarning asserts a SINGLE build-end Warning
// window/logMessage summarizing skipped files when the skip count > 0 (feature
// 26, Story 1, S1-AC1). The fixture's BIG.NSP exceeds the tiny max_file_size in
// .natural-lsp.toml and is skipped as too_large.
func TestLogMessage_SkipAggregateWarning(t *testing.T) {
	root := copyFixture(t, "testdata/skipfiles")

	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	outBuf, stderrContent := runGatedHandshake(t, initParams, root, realAZ())

	msgs := extractWindowLogMessages(t, outBuf)
	foundSkip := false
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeWarning &&
			strings.Contains(m.Message, "skipped") {
			foundSkip = true
			// Must be an aggregate naming the reason.
			if !strings.Contains(m.Message, "too_large") {
				t.Errorf("skip Warning should name the reason 'too_large', got: %q", m.Message)
			}
			break
		}
	}
	if !foundSkip {
		t.Errorf("expected a build-end Warning window/logMessage for skipped files, got: %v", msgs)
	}

	// Dual sink: stderr carries the aggregate.
	if !strings.Contains(stderrContent, "skipped files") {
		t.Errorf("expected stderr to mention skipped files, got: %q", stderrContent)
	}
}

// TestLogMessage_NoSkipWarningWhenClean asserts NO skip Warning is emitted when
// nothing was skipped (feature 26, Story 1 — "emit nothing when zero").
func TestLogMessage_NoSkipWarningWhenClean(t *testing.T) {
	root := copyFixture(t, "testdata/navigation")

	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	outBuf, _ := runGatedHandshake(t, initParams, root, realAZ())

	msgs := extractWindowLogMessages(t, outBuf)
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeWarning && strings.Contains(m.Message, "skipped") {
			t.Errorf("expected no skip Warning for a clean workspace, got: %q", m.Message)
		}
	}
}

// TestLogMessage_AmbiguityWarning asserts a build-end Warning window/logMessage
// summarizing flat-namespace resolution ambiguities (feature 26, Story 1,
// S1-AC1). The fixture calls CALLNAT 'DUP' with two DUP.NSN definitions.
func TestLogMessage_AmbiguityWarning(t *testing.T) {
	root := copyFixture(t, "testdata/ambiguity")

	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	outBuf, stderrContent := runGatedHandshake(t, initParams, root, realAZ())

	msgs := extractWindowLogMessages(t, outBuf)
	foundAmbig := false
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeWarning &&
			strings.Contains(m.Message, "ambiguous reference") {
			foundAmbig = true
			break
		}
	}
	if !foundAmbig {
		t.Errorf("expected a build-end Warning window/logMessage for ambiguity, got: %v", msgs)
	}

	// Dual sink: stderr carries the ambiguity count line.
	if !strings.Contains(stderrContent, "ambiguit") {
		t.Errorf("expected stderr to mention resolution ambiguities, got: %q", stderrContent)
	}
}

// buildIndexLogMessages runs hctx.buildIndex with an mlog wired to a capturing
// stream and returns the decoded window/logMessage frames it emitted.
func buildIndexLogMessages(t *testing.T, hctx *handlerContext) []windowLogMessage {
	t.Helper()
	var buf bytes.Buffer
	conn := &readWriteCloser{r: &buf, w: &buf}
	stream := jsonrpc2.NewHeaderStream(conn)
	hctx.mlog = newMessageLogger(stream, slog.New(slog.NewTextHandler(io.Discard, nil)), protocol.TraceValueOff)

	if _, _, err := hctx.buildIndex(context.Background(), nil); err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	return extractWindowLogMessages(t, &buf)
}

// TestLogMessage_WarmVsRebuild_ColdThenWarm proves the warm-vs-rebuild signal is
// CORRECT across a cold build (no cache → "rebuild") followed by a warm build
// over the unchanged workspace (cache present → "warm cache hit"). The counts
// alone cannot distinguish these; the signal keys on pre-build cache presence
// (feature 26, Story 1).
func TestLogMessage_WarmVsRebuild_ColdThenWarm(t *testing.T) {
	hctx, _, _ := newCacheWiringHCtx(t, map[string]string{
		"MAIN.NSP": "PROGRAM MAIN\nEND\n",
		"HELP.NSN": "DEFINE DATA PARAMETER\n1 #P (A5)\nEND-DEFINE\nEND\n",
	})

	// Cold build: no cache yet → "rebuild".
	cold := buildIndexLogMessages(t, hctx)
	if !hasInfoContaining(cold, "rebuild") {
		t.Errorf("cold build: expected a 'rebuild' outcome in window/logMessage, got: %v", cold)
	}
	if hasInfoContaining(cold, "warm cache hit") {
		t.Errorf("cold build must NOT report 'warm cache hit', got: %v", cold)
	}

	// Warm build: cache now present, workspace unchanged → "warm cache hit".
	warm := buildIndexLogMessages(t, hctx)
	if !hasInfoContaining(warm, "warm cache hit") {
		t.Errorf("warm build: expected 'warm cache hit' outcome, got: %v", warm)
	}
}

func hasInfoContaining(msgs []windowLogMessage, substr string) bool {
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeInfo && strings.Contains(m.Message, substr) {
			return true
		}
	}
	return false
}

// TestLogMessage_NoAmbiguityWarningWhenClean asserts NO ambiguity Warning when
// there are no ambiguous references (feature 26, Story 1 — "nothing when none").
func TestLogMessage_NoAmbiguityWarningWhenClean(t *testing.T) {
	root := copyFixture(t, "testdata/navigation")

	initParams := `{
		"processId":1234,
		"rootUri":null,
		"capabilities":{"general":{"positionEncodings":["utf-8"]}}
	}`
	outBuf, _ := runGatedHandshake(t, initParams, root, realAZ())

	msgs := extractWindowLogMessages(t, outBuf)
	for _, m := range msgs {
		if m.Type == protocol.MessageTypeWarning && strings.Contains(m.Message, "ambiguous reference") {
			t.Errorf("expected no ambiguity Warning for a clean workspace, got: %q", m.Message)
		}
	}
}
