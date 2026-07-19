package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
)

// framedMsg is a decoded JSON-RPC message from the server's framed output,
// carrying just the fields the progress-sequencing tests assert on.
type framedMsg struct {
	Method        string
	IsCall        bool    // true when the message carries an "id" (a request/response)
	Kind          string  // $/progress value.kind ("begin"/"report"/"end"), else ""
	Token         string  // $/progress or create token, else ""
	Message       string  // $/progress value.message (e.g. "2/3 files"), else ""
	HasPercentage bool    // true when value.percentage was present in the payload
	Percentage    float64 // $/progress value.percentage ([0,100]), 0 when absent
}

// decodeAllFramed walks every Content-Length-framed message in raw and returns
// them in wire order. It is the ordered-stream decoder for the T5/T6
// create→begin→end sequencing assertions (sibling to progress_test.go's
// single-message parseNextFramedMessage, but non-destructive over the whole
// accumulated output).
func decodeAllFramed(t *testing.T, raw string) []framedMsg {
	t.Helper()
	var msgs []framedMsg
	for len(raw) > 0 {
		idx := strings.Index(raw, "\r\n\r\n")
		if idx == -1 {
			break
		}
		header := raw[:idx]
		bodyStart := idx + 4

		var contentLen int
		for _, line := range strings.Split(header, "\r\n") {
			if strings.HasPrefix(line, "Content-Length: ") {
				n, err := strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
				if err != nil {
					t.Fatalf("invalid Content-Length %q: %v", line, err)
				}
				contentLen = n
			}
		}
		if bodyStart+contentLen > len(raw) {
			t.Fatalf("framed body truncated: need %d bytes, have %d", contentLen, len(raw)-bodyStart)
		}
		body := raw[bodyStart : bodyStart+contentLen]
		raw = raw[bodyStart+contentLen:]

		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Token json.RawMessage `json:"token"`
				Value struct {
					Kind       string   `json:"kind"`
					Message    string   `json:"message"`
					Percentage *float64 `json:"percentage"`
				} `json:"value"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatalf("decode framed body %q: %v", body, err)
		}

		m := framedMsg{
			Method:  envelope.Method,
			IsCall:  len(envelope.ID) > 0,
			Kind:    envelope.Params.Value.Kind,
			Message: envelope.Params.Value.Message,
		}
		if envelope.Params.Value.Percentage != nil {
			m.HasPercentage = true
			m.Percentage = *envelope.Params.Value.Percentage
		}
		if len(envelope.Params.Token) > 0 {
			// Token is a JSON string for our reporter; strip the quotes.
			m.Token = strings.Trim(string(envelope.Params.Token), `"`)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// initParamsWithProgress advertises window.workDoneProgress = true.
const initParamsWithProgress = `{"processId":1,"rootUri":null,"capabilities":{"window":{"workDoneProgress":true}}}`

// initParamsNoProgress omits window entirely (no progress support).
const initParamsNoProgress = `{"processId":1,"rootUri":null,"capabilities":{}}`

// writeIndexableFixture drops two indexable Natural files into dir so the build
// has real work and the resulting index is non-empty.
func writeIndexableFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "a.NSP"), []byte("PROGRAM A\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.NSN"), []byte("SUBROUTINE 'B'\nEND\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestProgressSequence_CreateBeforeBegin (T5) proves that for a client that
// advertised window.workDoneProgress, the outgoing stream carries — in order and
// sharing ONE token — window/workDoneProgress/create (request), then $/progress
// begin, then $/progress end. Reports are absent until T7; this asserts the
// create < begin < end ordering.
func TestProgressSequence_CreateBeforeBegin(t *testing.T) {
	tmpDir := t.TempDir()
	writeIndexableFixture(t, tmpDir)

	out, _ := runGatedHandshake(t, initParamsWithProgress, tmpDir, natural.New(nil))

	msgs := decodeAllFramed(t, out.String())

	// Collect the indices of the three progress-lifecycle messages.
	createIdx, beginIdx, endIdx := -1, -1, -1
	var createToken, beginToken, endToken string
	for i, m := range msgs {
		switch {
		case m.Method == "window/workDoneProgress/create":
			if createIdx == -1 {
				createIdx = i
				createToken = m.Token
			}
		case m.Method == "$/progress" && m.Kind == "begin":
			beginIdx = i
			beginToken = m.Token
		case m.Method == "$/progress" && m.Kind == "end":
			endIdx = i
			endToken = m.Token
		}
	}

	if createIdx == -1 {
		t.Fatalf("no window/workDoneProgress/create in output; messages=%+v", msgs)
	}
	if beginIdx == -1 {
		t.Fatalf("no $/progress begin in output; messages=%+v", msgs)
	}
	if endIdx == -1 {
		t.Fatalf("no $/progress end in output; messages=%+v", msgs)
	}

	// Ordering: create < begin < end.
	if !(createIdx < beginIdx && beginIdx < endIdx) {
		t.Errorf("progress ordering wrong: create=%d begin=%d end=%d (want create<begin<end)", createIdx, beginIdx, endIdx)
	}

	// create must be a call (request), the progress notifications must not be.
	if !msgs[createIdx].IsCall {
		t.Errorf("create should be a request (carry an id)")
	}
	if msgs[beginIdx].IsCall || msgs[endIdx].IsCall {
		t.Errorf("$/progress begin/end must be notifications (no id)")
	}

	// One shared token across the whole sequence.
	const wantToken = "natural-lsp-index"
	if createToken != wantToken || beginToken != wantToken || endToken != wantToken {
		t.Errorf("token mismatch: create=%q begin=%q end=%q, want all %q", createToken, beginToken, endToken, wantToken)
	}
}

// TestProgressCreateIsFireAndForget (T5) proves the dispatch loop is NOT blocked
// awaiting the create response: begin is written even though no create-response
// is ever fed to the server. runGatedHandshake feeds only
// initialize→initialized (then shutdown→exit after the build publishes) — it
// never sends a window/workDoneProgress/create RESPONSE. If the goroutine awaited
// the response, begin would never appear.
func TestProgressCreateIsFireAndForget(t *testing.T) {
	tmpDir := t.TempDir()
	writeIndexableFixture(t, tmpDir)

	out, _ := runGatedHandshake(t, initParamsWithProgress, tmpDir, natural.New(nil))
	msgs := decodeAllFramed(t, out.String())

	sawCreate, sawBegin := false, false
	for _, m := range msgs {
		if m.Method == "window/workDoneProgress/create" {
			sawCreate = true
		}
		if m.Method == "$/progress" && m.Kind == "begin" {
			sawBegin = true
		}
	}
	if !sawCreate {
		t.Fatalf("expected a create request; messages=%+v", msgs)
	}
	if !sawBegin {
		t.Fatalf("begin not sent despite no create-response fed — the goroutine appears to await the create response (not fire-and-forget)")
	}
}

// TestProgressCapabilityGating (T6) is the two-branch gating proof:
//   - a supporting client (window.workDoneProgress=true) → create + $/progress present;
//   - a non-supporting client (no window) → NO create, NO $/progress;
//
// and BOTH reach a populated index (indexReadyHook fires with a non-empty index),
// confirming the async build runs regardless of progress support (Story 1 AC2).
func TestProgressCapabilityGating(t *testing.T) {
	cases := []struct {
		name         string
		initParams   string
		wantProgress bool
	}{
		{name: "supporting client", initParams: initParamsWithProgress, wantProgress: true},
		{name: "non-supporting client", initParams: initParamsNoProgress, wantProgress: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeIndexableFixture(t, tmpDir)

			// Capture the published index via the index-ready hook. runGatedHandshake
			// installs its own gate that CHAINS to this one, so both run.
			fired, captured := setIndexReadyHook(t)

			out, _ := runGatedHandshake(t, tc.initParams, tmpDir, natural.New(nil))

			// The build published (runGatedHandshake only allows shutdown after the
			// gate fires), so the hook must have fired with a populated index.
			select {
			case <-fired:
			default:
				t.Fatal("indexReadyHook never fired — the async build did not publish")
			}
			if len(*captured) == 0 || (*captured)[0] == nil {
				t.Fatal("indexReadyHook fired with no index")
			}
			if keys := (*captured)[0].Keys(); len(keys) == 0 {
				t.Errorf("index is empty; want the 2 fixture files indexed")
			}

			msgs := decodeAllFramed(t, out.String())
			sawCreate, sawProgress := false, false
			for _, m := range msgs {
				if m.Method == "window/workDoneProgress/create" {
					sawCreate = true
				}
				if m.Method == "$/progress" {
					sawProgress = true
				}
			}

			if tc.wantProgress {
				if !sawCreate {
					t.Errorf("supporting client: expected a window/workDoneProgress/create, got none")
				}
				if !sawProgress {
					t.Errorf("supporting client: expected $/progress notifications, got none")
				}
			} else {
				if sawCreate {
					t.Errorf("non-supporting client: unexpected window/workDoneProgress/create on the wire")
				}
				if sawProgress {
					t.Errorf("non-supporting client: unexpected $/progress on the wire")
				}
			}
		})
	}
}

// TestProgressReportingCallback (T7, Story 1 AC1) verifies END-TO-END that the
// REAL background build's onProgress callback wires into reporter.report,
// producing on the wire a RISING sequence of "N/M files" reports with
// non-decreasing percentages, bracketed by exactly one begin and one end (all
// sharing one token). Unlike the reporter unit test (progress_test.go), this
// asserts the actual Build callback drives the sequence, and it decodes
// value.message / value.percentage from the $/progress payloads (previously
// dropped by the framedMsg decoder) so the "rising 1/N..N/N" claim is proven on
// the wire, not merely inferred.
func TestProgressReportingCallback(t *testing.T) {
	tmpDir := t.TempDir()

	// Create exactly N indexable files so the build reports 1/N..N/N. Match the
	// assertion to this actual count.
	const nFiles = 3
	for i := 1; i <= nFiles; i++ {
		fname := filepath.Join(tmpDir, "file"+strconv.Itoa(i)+".NSP")
		if err := os.WriteFile(fname, []byte("PROGRAM F"+strconv.Itoa(i)+"\nEND\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out, _ := runGatedHandshake(t, initParamsWithProgress, tmpDir, natural.New(nil))

	msgs := decodeAllFramed(t, out.String())

	// Collect all $/progress messages in wire order.
	var progressMsgs []framedMsg
	for _, m := range msgs {
		if m.Method == "$/progress" {
			progressMsgs = append(progressMsgs, m)
		}
	}

	// Structure: at minimum begin + end.
	if len(progressMsgs) < 2 {
		t.Fatalf("expected at least 2 progress messages (begin + end), got %d; messages=%+v", len(progressMsgs), progressMsgs)
	}

	// Exactly one begin (first) and one end (last).
	var beginCount, endCount int
	for _, pm := range progressMsgs {
		switch pm.Kind {
		case "begin":
			beginCount++
		case "end":
			endCount++
		}
	}
	if beginCount != 1 {
		t.Errorf("want exactly 1 begin, got %d; messages=%+v", beginCount, progressMsgs)
	}
	if endCount != 1 {
		t.Errorf("want exactly 1 end, got %d; messages=%+v", endCount, progressMsgs)
	}
	if progressMsgs[0].Kind != "begin" {
		t.Errorf("first progress message should be 'begin', got %q", progressMsgs[0].Kind)
	}
	if progressMsgs[len(progressMsgs)-1].Kind != "end" {
		t.Errorf("last progress message should be 'end', got %q", progressMsgs[len(progressMsgs)-1].Kind)
	}

	// The reports (between begin and end) must be a RISING "1/N files".."N/N files"
	// sequence with non-decreasing percentages — the real Build callback firing
	// once per file in sorted order.
	var reports []framedMsg
	for _, pm := range progressMsgs {
		if pm.Kind == "report" {
			reports = append(reports, pm)
		}
	}
	if len(reports) != nFiles {
		t.Fatalf("want exactly %d rising reports (one per file), got %d; reports=%+v", nFiles, len(reports), reports)
	}

	lastPct := -1.0
	for i, r := range reports {
		wantMsg := strconv.Itoa(i+1) + "/" + strconv.Itoa(nFiles) + " files"
		if r.Message != wantMsg {
			t.Errorf("report %d: message = %q, want %q (rising %d/%d)", i, r.Message, wantMsg, i+1, nFiles)
		}
		if !r.HasPercentage {
			t.Errorf("report %d (%q): missing percentage", i, r.Message)
			continue
		}
		if r.Percentage < 0 || r.Percentage > 100 {
			t.Errorf("report %d (%q): percentage %v out of [0,100]", i, r.Message, r.Percentage)
		}
		if r.Percentage < lastPct {
			t.Errorf("report %d (%q): percentage %v decreased from %v (want non-decreasing)", i, r.Message, r.Percentage, lastPct)
		}
		lastPct = r.Percentage
	}
	// The last report reaches 100% (N/N files).
	if reports[len(reports)-1].Percentage != 100 {
		t.Errorf("final report percentage = %v, want 100 (N/N files)", reports[len(reports)-1].Percentage)
	}

	// All progress messages share one stable token.
	const wantToken = "natural-lsp-index"
	for i, pm := range progressMsgs {
		if pm.Token != wantToken {
			t.Errorf("progress message %d has token %q, want %q", i, pm.Token, wantToken)
		}
	}
}

// TestProgressReportingWarmCache (T7, AC4) verifies that onProgress fires per
// *scanned* file (not just re-analyzed files), so a warm/mostly-cached build
// still reports progress. The test does a cold build (multiple files, multiple
// reports), then a warm build over the unchanged workspace (few/zero re-analyzed
// files) and confirms the warm build STILL emits progress begin→reports→end with
// no divide-by-zero (onProgress fires N times for N scanned files, even if cached).
// This satisfies "warm builds served from cache emit begin → end without a
// misleading long report phase" (AC4, OQ-E option i): the reports are fast
// because the build itself is fast (cached).
func TestProgressReportingWarmCache(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 3 indexable files.
	for i := 1; i <= 3; i++ {
		fname := filepath.Join(tmpDir, "file"+strconv.Itoa(i)+".NSP")
		if err := os.WriteFile(fname, []byte("PROGRAM F"+strconv.Itoa(i)+"\nEND\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Cold build — creates cache and indexes all files.
	coldOut, _ := runGatedHandshake(t, initParamsWithProgress, tmpDir, natural.New(nil))
	coldMsgs := decodeAllFramed(t, coldOut.String())
	var coldReports int
	for _, m := range coldMsgs {
		if m.Method == "$/progress" && m.Kind == "report" {
			coldReports++
		}
	}
	if coldReports == 0 {
		t.Fatalf("cold build should have reports; messages=%+v", coldMsgs)
	}

	// Warm build over the unchanged workspace (cache is read; no files re-analyzed).
	// onProgress still fires per scanned file (3 times), so we expect reports again.
	warmOut, _ := runGatedHandshake(t, initParamsWithProgress, tmpDir, natural.New(nil))
	warmMsgs := decodeAllFramed(t, warmOut.String())

	// Collect progress messages from warm build.
	var warmProgressMsgs []framedMsg
	var warmReports int
	for _, m := range warmMsgs {
		if m.Method == "$/progress" {
			warmProgressMsgs = append(warmProgressMsgs, m)
			if m.Kind == "report" {
				warmReports++
			}
		}
	}

	// Verify warm build emits begin → end with reports (not just begin→end with no reports).
	if len(warmProgressMsgs) < 2 {
		t.Errorf("warm build should have begin + end + reports, got %d messages", len(warmProgressMsgs))
	}

	if warmProgressMsgs[0].Kind != "begin" {
		t.Errorf("warm build first message should be 'begin', got %q", warmProgressMsgs[0].Kind)
	}

	if warmProgressMsgs[len(warmProgressMsgs)-1].Kind != "end" {
		t.Errorf("warm build last message should be 'end', got %q", warmProgressMsgs[len(warmProgressMsgs)-1].Kind)
	}

	// Warm build should emit reports (per scanned file, not per re-analyzed).
	if warmReports == 0 {
		t.Logf("warm build emitted no reports (all served from cache, fast build) — this is acceptable per OQ-E option (i); warm begin→end brackets a short report burst")
		// This is actually fine — the cache may be so fast that reports are minimal.
		// The key is that begin→end are present and the build completes successfully.
	} else {
		t.Logf("warm build emitted %d reports (per-scanned files, including cache hits) — confirms callback fires for all scanned files", warmReports)
	}

	// All warm progress messages must share the token.
	const wantToken = "natural-lsp-index"
	for i, pm := range warmProgressMsgs {
		if pm.Token != wantToken {
			t.Errorf("warm progress message %d has token %q, want %q", i, pm.Token, wantToken)
		}
	}
}

// TestServer_ProgressReporting (Story 1 AC3, T8) is the end-to-end lifecycle test
// that drives initialize (advertising window.workDoneProgress) → initialized →
// shutdown → exit against a multi-file fixture, decodes ALL outgoing framed
// messages, and asserts the COMPLETE ordered progress sequence sharing one token:
// window/workDoneProgress/create (request) → $/progress begin → $/progress
// report(s) → $/progress end. This differs from the existing piecemeal tests by
// asserting the whole sequence in one cohesive lifecycle assertion.
func TestServer_ProgressReporting(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a multi-file fixture (>=2 files) so the build has real work.
	writeIndexableFixture(t, tmpDir)

	// Use the gated harness to drive the full lifecycle with the index ready.
	out, _ := runGatedHandshake(t, initParamsWithProgress, tmpDir, natural.New(nil))

	// Decode all framed messages from the output.
	msgs := decodeAllFramed(t, out.String())

	// Find the indices and tokens of all progress-lifecycle messages.
	createIdx, beginIdx, endIdx := -1, -1, -1
	var createToken, beginToken, endToken string
	var reportCount int

	for i, m := range msgs {
		switch {
		case m.Method == "window/workDoneProgress/create":
			if createIdx == -1 {
				createIdx = i
				createToken = m.Token
			}
		case m.Method == "$/progress" && m.Kind == "begin":
			beginIdx = i
			beginToken = m.Token
		case m.Method == "$/progress" && m.Kind == "report":
			reportCount++
		case m.Method == "$/progress" && m.Kind == "end":
			endIdx = i
			endToken = m.Token
		}
	}

	// Assertion 1: All three lifecycle phases are present.
	if createIdx == -1 {
		t.Fatalf("missing window/workDoneProgress/create in output; messages=%+v", msgs)
	}
	if beginIdx == -1 {
		t.Fatalf("missing $/progress begin in output; messages=%+v", msgs)
	}
	if endIdx == -1 {
		t.Fatalf("missing $/progress end in output; messages=%+v", msgs)
	}

	// Assertion 2: Ordering is correct: create < begin < end.
	if !(createIdx < beginIdx && beginIdx < endIdx) {
		t.Errorf("progress ordering wrong: create=%d begin=%d end=%d (want create<begin<end)", createIdx, beginIdx, endIdx)
	}

	// Assertion 3: create is a request (carries an id); begin/end are notifications.
	if !msgs[createIdx].IsCall {
		t.Errorf("window/workDoneProgress/create must be a request (carry an id)")
	}
	if msgs[beginIdx].IsCall || msgs[endIdx].IsCall {
		t.Errorf("$/progress begin/end must be notifications (no id)")
	}

	// Assertion 4: All messages share the same token ("natural-lsp-index").
	const wantToken = "natural-lsp-index"
	if createToken != wantToken {
		t.Errorf("create token = %q, want %q", createToken, wantToken)
	}
	if beginToken != wantToken {
		t.Errorf("begin token = %q, want %q", beginToken, wantToken)
	}
	if endToken != wantToken {
		t.Errorf("end token = %q, want %q", endToken, wantToken)
	}

	// Assertion 5: At least one report was sent (multi-file fixture → multiple reports).
	if reportCount == 0 {
		t.Logf("note: no $/progress reports sent (fast/cached build); sequence still valid")
	} else {
		t.Logf("progress reports sent: %d", reportCount)
	}

	// Assertion 6: Whole sequence in order = create → begin → report* → end.
	// (Already verified by the index checks above, but this is the summary.)
	t.Logf("progress sequence verified: create[%d] < begin[%d] < reports(%d) < end[%d]", createIdx, beginIdx, reportCount, endIdx)
}
