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
	"sync"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/uri"
	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/model"
)

// gatedDiskAnalyzer wraps the real Natural analyzer but blocks in Analyze while
// analyzing DISK content (content carrying diskMarker) until release is closed,
// making the background index build observably in-flight. Open-buffer content
// (which does NOT carry diskMarker) is analyzed immediately, so didOpen/didChange
// arriving during the blocked build are NOT gated — mirroring OQ-B.1's window
// where an edit lands in the store while the cold build is still scanning disk.
type gatedDiskAnalyzer struct {
	inner      *natural.Analyzer
	diskMarker string
	release    <-chan struct{}

	startedOnce sync.Once
	started     chan struct{} // closed when the first disk Analyze is entered
}

func newGatedDiskAnalyzer(diskMarker string, release <-chan struct{}) *gatedDiskAnalyzer {
	return &gatedDiskAnalyzer{
		inner:      natural.New(nil),
		diskMarker: diskMarker,
		release:    release,
		started:    make(chan struct{}),
	}
}

func (a *gatedDiskAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	if bytes.Contains(content, []byte(a.diskMarker)) {
		a.startedOnce.Do(func() { close(a.started) })
		<-a.release // block the disk build; buffer analysis is never gated
	}
	return a.inner.Analyze(path, content)
}

// TestReplayDirtyBufferAfterPublish (Feature 21, T13 / OQ-B.1) proves that an
// edit which arrives via didOpen/didChange WHILE the background index build is
// in flight (idx still nil) is reflected by an INDEX-BACKED provider after the
// build publishes — because the background goroutine replays open buffers into
// the freshly-published index.
//
// The disk file declares NO subroutine; the edited buffer adds one. An
// index-backed workspace/symbol query for that subroutine must find it after
// the build completes. Without the replay, the full-build publish would
// overwrite the index with disk content and the query would return empty
// (the OQ-B.1 window).
func TestReplayDirtyBufferAfterPublish(t *testing.T) {
	tmpDir := t.TempDir()

	// Disk content: a program with NO subroutine, carrying the disk marker so
	// the gated analyzer blocks the initial build (but not the buffer analysis).
	diskContent := "* DISK-ONLY-MARKER\nDEFINE DATA LOCAL\n1 #X (N4)\nEND-DEFINE\nEND\n"
	editPath := filepath.Join(tmpDir, "EDITME.NSP")
	if err := os.WriteFile(editPath, []byte(diskContent), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second disk file gives the build more than one file to scan.
	if err := os.WriteFile(filepath.Join(tmpDir, "OTHER.NSN"), []byte("* DISK-ONLY-MARKER\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Buffer content: adds a subroutine that exists ONLY in the edited buffer,
	// not on disk. It does NOT carry the disk marker, so its analysis is not gated.
	const replayedSub = "MY-REPLAYED-SUB"
	bufferContent := "DEFINE SUBROUTINE " + replayedSub + "\nIGNORE\nEND-SUBROUTINE\nEND\n"

	fired, _ := setIndexReadyHook(t)

	release := make(chan struct{})
	az := newGatedDiskAnalyzer("DISK-ONLY-MARKER", release)

	pr, pw := io.Pipe()
	var outBuf lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	runDone := make(chan error, 1)
	go func() { runDone <- Run(context.Background(), pr, &outBuf, "0.0.0-test", tmpDir, az, logger) }()

	// initialize + initialized: the background build starts and blocks on the
	// disk files (gated analyzer).
	initCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{}}`))
	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	// Wait until the disk build is genuinely blocked (mid-scan).
	select {
	case <-az.started:
	case <-time.After(2 * time.Second):
		t.Fatal("disk build never started; background goroutine not spawned")
	}

	editURI := string(uri.File(editPath))

	// didOpen EDITME.NSP (baseline, matches disk) then didChange to the edited
	// buffer content — WHILE the build is blocked and idx is still nil. Neither
	// buffer carries the disk marker, so the store's analysis is NOT gated. This
	// is the OQ-B.1 window: applyDocumentChange sees idx==nil, so the edit lands
	// in the store but NOT the index.
	baselineBuffer := "* opened from disk\nDEFINE DATA LOCAL\n1 #X (N4)\nEND-DEFINE\nEND\n"
	openParams := didOpenParams(editURI, 1, baselineBuffer)
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("textDocument/didOpen", jsonrpc2.RawMessage(openParams))); err != nil {
		t.Fatal(err)
	}
	changeParams := didChangeParams(editURI, 2, bufferContent)
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("textDocument/didChange", jsonrpc2.RawMessage(changeParams))); err != nil {
		t.Fatal(err)
	}

	// Barrier: send a request (id=99) and wait for its response. Because the
	// dispatch loop is strictly serial, receiving this response GUARANTEES the
	// preceding didOpen/didChange notifications were fully processed (the edit is
	// in the store) BEFORE we release the build — closing the OQ-B.1 race in the
	// test itself. The index is still nil here, so this query returns [].
	barrier := jsonrpc2.NewCall(jsonrpc2.NewNumberID(99), "workspace/symbol",
		jsonrpc2.RawMessage(`{"query":"`+replayedSub+`"}`))
	if err := writeFramedMessage(pw, barrier); err != nil {
		t.Fatal(err)
	}
	barrierDeadline := time.After(2 * time.Second)
	for extractResponseResult(outBuf.Bytes(), 99) == nil {
		select {
		case <-barrierDeadline:
			t.Fatal("barrier workspace/symbol (id=99) response never arrived — dispatch loop stalled")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Release the disk build; it publishes the (disk-content) index, then
	// replays the open buffer into it. Wait for the indexReadyHook (fired AFTER
	// the replay per the goroutine ordering).
	close(release)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("indexReadyHook never fired; build/replay did not complete")
	}

	// INDEX-BACKED assertion: workspace/symbol for the buffer-only subroutine.
	// The provider reads fa.Structure from the index (not the store), so a hit
	// proves the replay merged the buffer analysis into the published index.
	symCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "workspace/symbol",
		jsonrpc2.RawMessage(`{"query":"`+replayedSub+`"}`))
	if err := writeFramedMessage(pw, symCall); err != nil {
		t.Fatal(err)
	}

	// Poll for the id=2 response and assert it contains the subroutine name.
	deadline := time.After(2 * time.Second)
	var found bool
	for !found {
		if resp := extractResponseResult(outBuf.Bytes(), 2); resp != nil {
			if strings.Contains(string(resp), replayedSub) {
				found = true
				break
			}
			t.Fatalf("workspace/symbol result did not reflect the replayed buffer edit (index served stale disk content — OQ-B.1 window NOT closed): %s", resp)
		}
		select {
		case <-deadline:
			t.Fatal("workspace/symbol response for id=2 never arrived")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Clean shutdown.
	if err := writeFramedMessage(pw, jsonrpc2.NewCall(jsonrpc2.NewNumberID(3), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after exit")
	}
}

// didOpenParams builds a textDocument/didOpen params JSON body.
func didOpenParams(uriStr string, version int, text string) []byte {
	body := map[string]any{
		"textDocument": map[string]any{
			"uri":        uriStr,
			"languageId": "natural",
			"version":    version,
			"text":       text,
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// didChangeParams builds a textDocument/didChange params JSON body (full sync).
func didChangeParams(uriStr string, version int, text string) []byte {
	body := map[string]any{
		"textDocument": map[string]any{
			"uri":     uriStr,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": text},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// extractResponseResult scans framed JSON-RPC output for a response with the
// given numeric id and returns its raw "result" bytes, or nil if not present yet.
func extractResponseResult(framed []byte, id int) []byte {
	// Split on the Content-Length framing to isolate JSON bodies, then look for
	// the matching id and return the result field.
	parts := bytes.Split(framed, []byte("\r\n\r\n"))
	for _, part := range parts {
		// A body starts at the first '{'.
		brace := bytes.IndexByte(part, '{')
		if brace < 0 {
			continue
		}
		body := part[brace:]
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.ID != nil && *msg.ID == id && msg.Result != nil {
			return msg.Result
		}
	}
	return nil
}
