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
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"natural-lsp/internal/analysis"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// lockedBuffer is a concurrency-safe bytes.Buffer wrapper: the dispatch loop and
// the background build goroutine may both write framed output, and a test may
// read the accumulated bytes concurrently. All access is guarded.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Return a copy so callers never alias the buffer's backing array.
	return append([]byte(nil), b.buf.Bytes()...)
}

// channelGatedAnalyzer is a test analyzer whose Analyze blocks until a release
// channel is closed, making the background index build observably in-flight.
// started is closed the first time Analyze is entered so a test can synchronize
// on "the build has begun" without a sleep race.
type channelGatedAnalyzer struct {
	release     <-chan struct{} // Analyze blocks until this is closed
	startedOnce sync.Once
	started     chan struct{} // closed when Analyze is first entered
}

func newChannelGatedAnalyzer(release <-chan struct{}) *channelGatedAnalyzer {
	return &channelGatedAnalyzer{
		release: release,
		started: make(chan struct{}),
	}
}

func (a *channelGatedAnalyzer) Analyze(path string, content []byte) (model.FileAnalysis, error) {
	a.startedOnce.Do(func() { close(a.started) })
	<-a.release
	return model.FileAnalysis{ObjectType: model.ObjectUnknown}, nil
}

// setIndexReadyHook installs a test index-ready hook that closes fired and
// records the index; it restores the previous hook via t.Cleanup.
func setIndexReadyHook(t *testing.T) (fired chan struct{}, captured *[]*workspace.Index) {
	t.Helper()
	fired = make(chan struct{})
	var got []*workspace.Index
	var mu sync.Mutex
	indexReadyHookMu.Lock()
	old := indexReadyHook
	indexReadyHook = func(idx *workspace.Index, _ protocol.PositionEncodingKind) {
		mu.Lock()
		got = append(got, idx)
		mu.Unlock()
		close(fired)
	}
	indexReadyHookMu.Unlock()
	t.Cleanup(func() {
		indexReadyHookMu.Lock()
		indexReadyHook = old
		indexReadyHookMu.Unlock()
	})
	return fired, &got
}

// indexReadyGate installs an indexReadyHook that closes the returned channel
// when the background build publishes, CHAINING to any previously-installed
// hook so a caller's own capture hook still runs. It restores the prior hook via
// t.Cleanup. Feature 21 (T4) made the index build asynchronous, so pre-fed
// lifecycle tests can no longer assume the index is ready the instant
// "initialized" is processed — they must wait on this gate before allowing
// shutdown (which cancels bgCtx and would otherwise skip the publish).
func indexReadyGate(t *testing.T) <-chan struct{} {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	indexReadyHookMu.Lock()
	prev := indexReadyHook
	indexReadyHook = func(idx *workspace.Index, enc protocol.PositionEncodingKind) {
		if prev != nil {
			prev(idx, enc)
		}
		once.Do(func() { close(ready) })
	}
	indexReadyHookMu.Unlock()
	t.Cleanup(func() {
		indexReadyHookMu.Lock()
		indexReadyHook = prev
		indexReadyHookMu.Unlock()
	})
	return ready
}

// gatedHandshakeReader serves the pre-shutdown framed messages (initialize +
// initialized), then blocks until the index-ready gate fires, then serves the
// post-index messages (shutdown + exit) and finally EOF. This lets a pre-fed
// lifecycle test drive the full handshake deterministically against the async
// index build (feature 21, T4): the build always publishes before shutdown
// cancels bgCtx, so the index/no-usable-root signals fire exactly as they did
// under the old synchronous build.
type gatedHandshakeReader struct {
	pre   *bytes.Buffer
	post  *bytes.Buffer
	ready <-chan struct{}

	preDone bool
	waited  bool
}

func (r *gatedHandshakeReader) Read(p []byte) (int, error) {
	if !r.preDone {
		n, err := r.pre.Read(p)
		if err == nil {
			return n, nil
		}
		r.preDone = true // pre buffer exhausted; fall through to gate + post
	}
	if !r.waited {
		<-r.ready // block until the background build has published
		r.waited = true
	}
	return r.post.Read(p) // returns io.EOF once drained → Run exits cleanly
}

// runGatedHandshake drives initialize → initialized (pre), waits for the async
// index build to publish, then shutdown → exit (post), and returns the server's
// framed output and captured stderr log. It is the async-safe replacement for
// pre-fed "initialize→initialized→shutdown→exit" harnesses (feature 21, T4).
func runGatedHandshake(t *testing.T, initParams string, cwdFallback string, az analysis.Analyzer) (out *lockedBuffer, logText string) {
	t.Helper()

	ready := indexReadyGate(t)

	var pre bytes.Buffer
	if err := writeFramedMessage(&pre, jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize", jsonrpc2.RawMessage(initParams))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(&pre, jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	var post bytes.Buffer
	if err := writeFramedMessage(&post, jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(&post, jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	reader := &gatedHandshakeReader{pre: &pre, post: &post, ready: ready}
	out = &lockedBuffer{}
	logBuf := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	if err := Run(context.Background(), reader, out, "0.0.0-test", cwdFallback, az, logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return out, logBuf.String()
}

// runGatedLifecycle serves preMsgs (which must include initialize +
// initialized and may include interleaved requests that should run BEFORE the
// index publishes), then waits for the async index build to publish, then
// serves midMsgs (messages that must run AFTER the index is ready — e.g. a
// didOpen whose diagnostics depend on the resolution set) followed by shutdown
// + exit. It returns the server's framed output and captured stderr log.
// Feature 21 (T4): the index build is asynchronous, so tests that need
// index/resolution-dependent behavior must schedule those messages in midMsgs.
func runGatedLifecycle(t *testing.T, preMsgs, midMsgs []jsonrpc2.Message, cwdFallback string, az analysis.Analyzer) (out *lockedBuffer, logText string) {
	t.Helper()

	ready := indexReadyGate(t)

	var pre bytes.Buffer
	for i, m := range preMsgs {
		if err := writeFramedMessage(&pre, m); err != nil {
			t.Fatalf("write pre message %d: %v", i, err)
		}
	}

	var post bytes.Buffer
	for i, m := range midMsgs {
		if err := writeFramedMessage(&post, m); err != nil {
			t.Fatalf("write mid message %d: %v", i, err)
		}
	}
	if err := writeFramedMessage(&post, jsonrpc2.NewCall(jsonrpc2.NewNumberID(9999), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(&post, jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	reader := &gatedHandshakeReader{pre: &pre, post: &post, ready: ready}
	out = &lockedBuffer{}
	logBuf := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))
	if err := Run(context.Background(), reader, out, "0.0.0-test", cwdFallback, az, logger); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return out, logBuf.String()
}

// TestDispatchLoopResponsiveDuringBuild (Story 2 AC3, T4) proves the dispatch
// loop keeps servicing messages while the background index build is in flight.
// With a build that blocks in the analyzer, a shutdown request sent AFTER the
// build has started must still receive its response over the wire while the
// build is blocked — impossible if the build ran synchronously on the loop.
func TestDispatchLoopResponsiveDuringBuild(t *testing.T) {
	tmpDir := t.TempDir()
	// Two indexable files so the build has real work to do.
	if err := os.WriteFile(filepath.Join(tmpDir, "a.NSP"), []byte("PROGRAM A\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.NSN"), []byte("SUBROUTINE 'B'\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fired, _ := setIndexReadyHook(t)

	release := make(chan struct{})
	az := newChannelGatedAnalyzer(release)

	// Use a pipe so we can send the shutdown request AFTER observing the build
	// has started (mid-flight), rather than pre-feeding it.
	pr, pw := io.Pipe()
	var outBuf lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	runDone := make(chan error, 1)
	go func() { runDone <- Run(context.Background(), pr, &outBuf, "0.0.0-test", tmpDir, az, logger) }()

	// Feed initialize + initialized; the build goroutine then blocks in Analyze.
	initCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{}}`))
	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	// Wait for the build to have entered the analyzer (blocked).
	select {
	case <-az.started:
	case <-time.After(2 * time.Second):
		t.Fatal("analyzer never started; background build goroutine not spawned")
	}

	// The hook must NOT have fired: the build is blocked in the analyzer.
	select {
	case <-fired:
		t.Fatal("indexReadyHook fired while the build was still blocked — build ran synchronously, not on a goroutine")
	case <-time.After(100 * time.Millisecond):
	}

	// Send shutdown WHILE the build is blocked. If the loop is responsive
	// (async build), we get a shutdown response even though the build is gated.
	if err := writeFramedMessage(pw, jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	// Poll the output for the id=2 shutdown response while the build is still blocked.
	deadline := time.After(2 * time.Second)
	for {
		if bytes.Contains(outBuf.Bytes(), []byte(`"id":2`)) {
			break // responsive: shutdown answered during the blocked build
		}
		select {
		case <-deadline:
			t.Fatal("shutdown response not received while build blocked — dispatch loop was NOT responsive (build ran synchronously)")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Now release the build and send exit; Run must return cleanly.
	close(release)
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

// TestNoPublishAfterShutdown (Story 2 AC5, T4) proves that a shutdown racing an
// in-flight build cancels bgCtx and the build goroutine skips publish/hook: the
// indexReadyHook must NOT fire, hctx.idx stays nil, and Run returns cleanly.
func TestNoPublishAfterShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.NSP"), []byte("PROGRAM A\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fired, _ := setIndexReadyHook(t)

	release := make(chan struct{})
	az := newChannelGatedAnalyzer(release)

	// Use a pipe so the build is genuinely IN FLIGHT (blocked in the analyzer)
	// when shutdown arrives — pre-feeding shutdown would cancel bgCtx before the
	// build goroutine ever reached workspace.Build (T11 aborts before scanning).
	pr, pw := io.Pipe()
	var outBuf lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	runDone := make(chan error, 1)
	go func() { runDone <- Run(context.Background(), pr, &outBuf, "0.0.0-test", tmpDir, az, logger) }()

	// Feed initialize + initialized; the build goroutine then blocks in Analyze.
	initCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{}}`))
	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	// Wait until the build has started (blocked in the analyzer, mid-scan).
	select {
	case <-az.started:
	case <-time.After(2 * time.Second):
		t.Fatal("analyzer never started")
	}

	// Now send shutdown + exit WHILE the build is blocked. shutdown fires
	// bgCancel; exit returns from the loop. Run's deferred cleanup then joins the
	// still-blocked build goroutine (bgBuild.Wait).
	if err := writeFramedMessage(pw, jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("exit", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	// Give the loop time to process shutdown + exit (fire bgCancel, leave the
	// loop). Run cannot return yet — the deferred bgBuild.Wait joins the
	// still-blocked build goroutine.
	time.Sleep(100 * time.Millisecond)

	// Release the analyzer. buildIndex now returns (T11 abort with ctx.Err()),
	// but the goroutine must observe bgCtx.Err() and SKIP publish/hook.
	close(release)
	_ = pw.Close()

	// Run must return cleanly (the goroutine exits after the skip).
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after shutdown/exit + release (goroutine leaked?)")
	}

	// The hook must NOT have fired (no publish after shutdown).
	select {
	case <-fired:
		t.Fatal("indexReadyHook fired after shutdown — published after bgCtx cancellation (publish-after-shutdown)")
	default:
		// good
	}
}

// TestGracefulDegradationPrePublish (Story 2 AC3, T9) proves that index-backed
// providers degrade gracefully BEFORE the background build publishes (FR-43):
// while the build is deliberately blocked in the analyzer (so hctx.idx is still
// nil), index-backed requests sent over the wire return the empty/null result —
// NOT an error, NOT a hang. It then unblocks the build and confirms the SAME
// request returns real data, proving the earlier empty result was the transient
// pre-publish state, not a permanent failure.
//
// It uses the live-pipe + channel-gated slow-analyzer harness (same structure as
// TestReplayDirtyBufferAfterPublish and TestDispatchLoopResponsiveDuringBuild),
// synchronizing on az.started / the barrier response / the indexReadyHook — no
// sleeps in the assertion path.
//
// The two probed index-backed providers:
//   - workspace/symbol (query "MY-DEGRADE-SUB") → "[]" (empty array) pre-publish.
//   - textDocument/definition → "null" pre-publish.
//
// Both must be SUCCESSFUL JSON-RPC responses (no "error" field). documentSymbol is
// store-first (answers pre-publish from the buffer) so it is NOT the degradation
// probe here — these two read the index directly.
//
// AC5 (clean shutdown) is proven by TestNoPublishAfterShutdown.
func TestGracefulDegradationPrePublish(t *testing.T) {
	tmpDir := t.TempDir()

	// Disk file declares a real subroutine so that, AFTER the build publishes, an
	// index-backed workspace/symbol query returns a non-empty result (proving the
	// earlier "[]" was transient pre-publish state, not a permanent failure). The
	// disk marker gates the build inside the analyzer.
	const degradeSub = "MY-DEGRADE-SUB"
	diskContent := "* DISK-ONLY-MARKER\nDEFINE SUBROUTINE " + degradeSub + "\nIGNORE\nEND-SUBROUTINE\nEND\n"
	subPath := filepath.Join(tmpDir, "DEGRADE.NSS")
	if err := os.WriteFile(subPath, []byte(diskContent), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second disk file gives the build more than one file to scan.
	if err := os.WriteFile(filepath.Join(tmpDir, "OTHER.NSN"), []byte("* DISK-ONLY-MARKER\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fired, _ := setIndexReadyHook(t)

	release := make(chan struct{})
	// gatedDiskAnalyzer wraps the REAL analyzer and blocks on disk content, so
	// the build is genuinely in flight (idx nil) while producing a real index once
	// released. Buffer content (none here) would not be gated.
	az := newGatedDiskAnalyzer("DISK-ONLY-MARKER", release)

	pr, pw := io.Pipe()
	var outBuf lockedBuffer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	runDone := make(chan error, 1)
	go func() { runDone <- Run(context.Background(), pr, &outBuf, "0.0.0-test", tmpDir, az, logger) }()

	// initialize + initialized: the background build starts and blocks on the disk
	// files (gated analyzer). The client does NOT advertise progress, so no
	// $/progress traffic clutters the output.
	initCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "initialize",
		jsonrpc2.RawMessage(`{"processId":1,"rootUri":null,"capabilities":{}}`))
	if err := writeFramedMessage(pw, initCall); err != nil {
		t.Fatal(err)
	}
	if err := writeFramedMessage(pw, jsonrpc2.NewNotification("initialized", jsonrpc2.RawMessage(`{}`))); err != nil {
		t.Fatal(err)
	}

	// Wait until the build is genuinely blocked in the analyzer (mid-scan, idx nil).
	select {
	case <-az.started:
	case <-time.After(2 * time.Second):
		t.Fatal("disk build never started; background goroutine not spawned")
	}

	// The index must NOT be ready yet: the build is blocked.
	select {
	case <-fired:
		t.Fatal("indexReadyHook fired while the build was still blocked — build not on a goroutine")
	case <-time.After(50 * time.Millisecond):
	}

	subURI := string(uri.File(subPath))

	// Probe 1: workspace/symbol (id=10) for the on-disk subroutine name. Pre-publish
	// (idx nil) this must degrade to "[]", a successful response — not an error.
	symCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(10), "workspace/symbol",
		jsonrpc2.RawMessage(`{"query":"`+degradeSub+`"}`))
	if err := writeFramedMessage(pw, symCall); err != nil {
		t.Fatal(err)
	}

	// Probe 2: textDocument/definition (id=11) at a position in the disk file.
	// Pre-publish (idx/res nil) this must degrade to "null", a successful response.
	defParams := `{"textDocument":{"uri":"` + subURI + `"},"position":{"line":1,"character":20}}`
	defCall := jsonrpc2.NewCall(jsonrpc2.NewNumberID(11), "textDocument/definition",
		jsonrpc2.RawMessage(defParams))
	if err := writeFramedMessage(pw, defCall); err != nil {
		t.Fatal(err)
	}

	// The dispatch loop is strictly serial, so waiting for the id=11 response
	// guarantees id=10 was already answered too. Both are answered WHILE the build
	// is still blocked (idx nil), proving the loop is responsive and the providers
	// degrade rather than hang.
	waitForResponse(t, &outBuf, 11)

	// Probe 1 assertion: successful response, empty array (not null, not error).
	symResult, symIsErr := extractResponse(&outBuf, 10)
	if symResult == nil {
		t.Fatal("workspace/symbol (id=10) got no response while build blocked — hung or errored")
	}
	if symIsErr {
		t.Fatalf("workspace/symbol (id=10) returned a JSON-RPC error pre-publish; want a successful empty result. result=%q", symResult)
	}
	if string(symResult) != "[]" {
		t.Fatalf("workspace/symbol (id=10) pre-publish result = %q, want %q (empty array — graceful degradation)", symResult, "[]")
	}

	// Probe 2 assertion: successful response, null (not error).
	defResult, defIsErr := extractResponse(&outBuf, 11)
	if defResult == nil {
		t.Fatal("textDocument/definition (id=11) got no response while build blocked — hung or errored")
	}
	if defIsErr {
		t.Fatalf("textDocument/definition (id=11) returned a JSON-RPC error pre-publish; want a successful null result. result=%q", defResult)
	}
	if string(defResult) != "null" {
		t.Fatalf("textDocument/definition (id=11) pre-publish result = %q, want %q (null — graceful degradation)", defResult, "null")
	}

	// Unblock the build and wait for it to publish the index.
	close(release)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("indexReadyHook never fired after release; build did not complete")
	}

	// Confirmation: the SAME workspace/symbol query now returns real data (the
	// subroutine), proving the earlier "[]" was the transient pre-publish state,
	// not a permanent failure.
	symCall2 := jsonrpc2.NewCall(jsonrpc2.NewNumberID(12), "workspace/symbol",
		jsonrpc2.RawMessage(`{"query":"`+degradeSub+`"}`))
	if err := writeFramedMessage(pw, symCall2); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		if resp := extractResponseResult(outBuf.Bytes(), 12); resp != nil {
			if !strings.Contains(string(resp), degradeSub) {
				t.Fatalf("post-publish workspace/symbol (id=12) did not find %q; result=%q", degradeSub, resp)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("post-publish workspace/symbol (id=12) response never arrived")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Clean shutdown.
	if err := writeFramedMessage(pw, jsonrpc2.NewCall(jsonrpc2.NewNumberID(13), "shutdown", jsonrpc2.RawMessage(`{}`))); err != nil {
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

// waitForResponse polls the framed output until a JSON-RPC response with the given
// numeric id appears (result OR error field), failing the test on a 2s timeout.
// It exists because extractResponseResult only matches responses carrying a
// non-null "result" — a graceful-degradation "null" result IS a non-null
// json.RawMessage ("null"), so it is observable, but this helper also surfaces
// error responses so a degradation-that-errored is caught rather than timing out.
func waitForResponse(t *testing.T, buf *lockedBuffer, id int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if result, _ := extractResponse(buf, id); result != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("no JSON-RPC response for id=%d arrived within 2s (loop stalled or request hung)", id)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// extractResponse scans framed JSON-RPC output for a response with the given
// numeric id and returns (rawResult, isError). rawResult is the raw "result"
// bytes (e.g. []byte("null"), []byte("[]")) when a result field is present, or
// the raw "error" object bytes when the response carried an error instead;
// isError distinguishes the two. It returns (nil, false) when no matching
// response is present yet. Unlike extractResponseResult it treats a JSON `null`
// result as a real response (len("null")==4) and surfaces error responses, so
// callers can assert "successful response, no error field".
func extractResponse(buf *lockedBuffer, id int) (raw []byte, isError bool) {
	// The framed stream packs bodies as "<header>\r\n\r\n<body><header>\r\n\r\n<body>…",
	// so splitting on the header separator leaves each body followed by the NEXT
	// message's Content-Length header. A streaming json.Decoder reads exactly one
	// JSON value at the '{' and ignores the trailing header bytes, so every message
	// (not just the last) decodes cleanly.
	parts := bytes.Split(buf.Bytes(), []byte("\r\n\r\n"))
	for _, part := range parts {
		brace := bytes.IndexByte(part, '{')
		if brace < 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(part[brace:]))
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			continue
		}
		if msg.ID == nil || *msg.ID != id {
			continue
		}
		if len(msg.Error) > 0 {
			return msg.Error, true
		}
		if len(msg.Result) > 0 {
			return msg.Result, false
		}
	}
	return nil, false
}
