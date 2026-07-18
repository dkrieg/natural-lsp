// Package server implements the LSP lifecycle (initialize, shutdown) and
// request dispatch over stdio. It depends only on the analysis.Analyzer
// interface and the workspace index — never on a concrete extraction backend.
package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	gojson "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"natural-lsp/internal/analysis"
	"natural-lsp/internal/config"
	"natural-lsp/internal/document"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// bgCtxHook is a test-only hook called after creating the background context.
// It allows tests to observe the background context and its cancellation.
// Set only in tests; nil in production.
var (
	bgCtxHook   func(context.Context)
	bgCtxHookMu sync.Mutex
)

// indexReadyHook is a test-only hook called after the workspace index is built
// and the server reaches the initialized state. It allows tests to observe
// the built index and the negotiated position encoding.
// Set only in tests; nil in production.
var (
	indexReadyHook   func(*workspace.Index, protocol.PositionEncodingKind)
	indexReadyHookMu sync.Mutex
)

// initializeReadyHook is a test-only hook called in the "initialize" handler
// immediately after config.Bootstrap resolves the workspace root and config from
// the client's initialize params (feature 20, deferred bootstrap). It lets tests
// observe the NEGOTIATED root/cfg — the values that drive the store, watcher, and
// index build — without needing to reach the index build. As of feature 21 (T1),
// it also passes the clientSupportsWorkDoneProgress flag so tests can verify
// capability detection.
// Set only in tests; nil in production.
var (
	initializeReadyHook   func(root string, cfg config.Config, clientSupportsWorkDoneProgress bool)
	initializeReadyHookMu sync.Mutex
)

// readWriteCloser wraps separate Reader and Writer into an io.ReadWriteCloser
// for use with jsonrpc2.NewHeaderStream.
type readWriteCloser struct {
	r io.Reader
	w io.Writer
}

func (rwc *readWriteCloser) Read(p []byte) (int, error) {
	return rwc.r.Read(p)
}

func (rwc *readWriteCloser) Write(p []byte) (int, error) {
	return rwc.w.Write(p)
}

func (rwc *readWriteCloser) Close() error {
	// Close the underlying reader if it supports it (e.g. io.PipeReader, os.File).
	// This is essential for unblocking any goroutine blocked in a Read call on rwc.r
	// when the stream is closed due to context cancellation.
	if c, ok := rwc.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Lifecycle states
const (
	statePreInit     = 0 // Before initialize
	stateInitialized = 1 // After initialize and initialized notification
	stateShutdown    = 2 // After shutdown request
)

// buildWatchedFilesRegisterOptions serialises a DidChangeWatchedFilesRegistrationOptions
// value — containing one FileSystemWatcher per indexed extension — into a jsontext.Value
// suitable for Registration.RegisterOptions.
//
// The glob pattern "**/*<ext>" (e.g. "**/*.NSP") is a standard LSP glob that matches any
// file with that extension in the workspace tree, at any nesting depth.  WatchKind is
// omitted (zero) so the client defaults to create|change|delete (WatchKind 7 per spec).
func buildWatchedFilesRegisterOptions(extensions []string) (protocol.LSPAny, error) {
	watchers := make([]protocol.FileSystemWatcher, 0, len(extensions))
	for _, ext := range extensions {
		// ext already has a leading dot (e.g. ".NSP"); build "**/*.NSP".
		watchers = append(watchers, protocol.FileSystemWatcher{
			GlobPattern: protocol.Pattern("**/*" + ext),
		})
	}
	opts := protocol.DidChangeWatchedFilesRegistrationOptions{Watchers: watchers}
	var buf bytes.Buffer
	if err := opts.MarshalJSONTo(jsontext.NewEncoder(&buf)); err != nil {
		return nil, fmt.Errorf("marshal DidChangeWatchedFilesRegistrationOptions: %w", err)
	}
	return protocol.LSPAny(buf.Bytes()), nil
}

// marshalResult marshals v as a JSON LSP response result via the json/v2 path
// (gojson), so that protocol types carrying Optional/Nullable/union fields (e.g.
// protocol.CompletionItem.Detail, protocol.CallHierarchyItem.Data) are encoded
// correctly via their MarshalJSONTo implementations.
//
// Callers are responsible for the nil check and must call marshalResult only
// when v is provably non-nil. The per-method empty-result sentinel ("null" or
// "[]") is assigned by the caller before this is invoked — see the dispatch
// switch in Run for the pattern.
func marshalResult(v any) ([]byte, error) {
	return gojson.Marshal(v)
}

// initializeNegotiation holds the results of the initialize handshake,
// capturing the negotiated capabilities and encoding for later use.
type initializeNegotiation struct {
	result                         []byte
	posEncoding                    protocol.PositionEncodingKind
	clientSupportsWatchedFilesReg  bool
	clientSupportsWorkDoneProgress bool
}

// handleInitialize processes an LSP "initialize" request, negotiates
// positionEncoding (UTF-8 preferred, UTF-16 default per ADR-008), and returns
// an initializeNegotiation struct capturing the marshalled InitializeResult bytes,
// the negotiated PositionEncodingKind, a flag indicating whether the client
// supports dynamic registration for workspace/didChangeWatchedFiles, and a flag
// indicating whether the client supports server-initiated work-done progress.
//
// Note (feature 20): root/config discovery is NOT performed here — it happens in
// the "initialize" dispatch case (see Run), which computes the discovery start
// point via resolveRootStart(params, cwdFallback) and runs config.Bootstrap from
// that client-supplied path before storing root/cfg onto handlerContext. This
// keeps handleInitialize a pure capabilities/encoding negotiator with no I/O.
//
// Capabilities advertised here form a deliberately locked allow-list enforced
// by TestInitialize. Feature 10 (T3) adds the three navigation providers:
// definitionProvider, referencesProvider, workspaceSymbolProvider (each true).
// Feature 11 (T3) adds documentSymbolProvider (true).
// When features 12–13 add further providers (hover, completion, …),
// they MUST update TestInitialize to extend the allow-list, making additions explicit.
func handleInitialize(params protocol.InitializeParams, version string) (initializeNegotiation, error) {
	// Negotiate position encoding: prefer UTF-8 if offered, else fall back to UTF-16.
	// slices.Contains is O(n) over a typically-tiny list (1–3 entries).
	posEncoding := protocol.PositionEncodingKindUTF16
	if params.Capabilities.General != nil &&
		slices.Contains(params.Capabilities.General.PositionEncodings, protocol.PositionEncodingKindUTF8) {
		posEncoding = protocol.PositionEncodingKindUTF8
	}

	// Check whether the client supports dynamic registration for workspace/didChangeWatchedFiles (FR-34, A2).
	// This flag will be used in the initialized handler to send client/registerCapability if needed.
	clientSupportsWatchedFilesReg := false
	if params.Capabilities.Workspace != nil &&
		params.Capabilities.Workspace.DidChangeWatchedFiles != nil &&
		params.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration != nil &&
		*params.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration {
		clientSupportsWatchedFilesReg = true
	}

	// Check whether the client supports server-initiated work-done progress (FR-32).
	// Mirror the nil-chain deref pattern used for Workspace.DidChangeWatchedFiles.DynamicRegistration.
	// This flag will be used in the initialized handler to gate progress reporting (feature 21, T1).
	clientSupportsWorkDoneProgress := false
	if params.Capabilities.Window != nil &&
		params.Capabilities.Window.WorkDoneProgress != nil &&
		*params.Capabilities.Window.WorkDoneProgress {
		clientSupportsWorkDoneProgress = true
	}

	// Intentional minimal capability set — see comment above.
	// Feature 10, T3: advertise the three navigation providers (definition, references, workspace symbol).
	// Feature 11, T3: advertise documentSymbolProvider.
	// Feature 12, T6: advertise hoverProvider.
	// Feature 13, T6: advertise codeLensProvider.
	// Feature 16, T3: advertise completionProvider with space-trigger and no resolve handler.
	// Feature 17, T1: advertise signatureHelpProvider with space trigger and retrigger characters.
	// Feature 18, T1: advertise callHierarchyProvider.
	falseVal := false
	initResult := protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync:        protocol.TextDocumentSyncKindFull,
			PositionEncoding:        posEncoding,
			DefinitionProvider:      protocol.Boolean(true),
			ReferencesProvider:      protocol.Boolean(true),
			WorkspaceSymbolProvider: protocol.Boolean(true),
			DocumentSymbolProvider:  protocol.Boolean(true),
			HoverProvider:           protocol.Boolean(true),
			CodeLensProvider:        &protocol.CodeLensOptions{ResolveProvider: &falseVal},
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{" "},
				ResolveProvider:   &falseVal,
			},
			SignatureHelpProvider: &protocol.SignatureHelpOptions{
				TriggerCharacters:   []string{" "},
				RetriggerCharacters: []string{" "},
			},
			CallHierarchyProvider: protocol.Boolean(true),
		},
		ServerInfo: protocol.ServerInfo{
			Name:    "natural-lsp",
			Version: protocol.NewOptional(version),
		},
	}

	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := initResult.MarshalJSONTo(enc); err != nil {
		return initializeNegotiation{}, fmt.Errorf("marshal initialize result: %w", err)
	}
	return initializeNegotiation{
		result:                         buf.Bytes(),
		posEncoding:                    posEncoding,
		clientSupportsWatchedFilesReg:  clientSupportsWatchedFilesReg,
		clientSupportsWorkDoneProgress: clientSupportsWorkDoneProgress,
	}, nil
}

// handlerContext is the shared state available to every request handler in the
// dispatch switch. It is populated once — the index after "initialized", the
// encoding after "initialize" — and then mutated on didChange/watcher updates (T14).
//
// Concurrency note (F7): the dispatch loop is single-threaded; didChange and
// watcher callbacks fire synchronously in notification handlers (same goroutine).
// However, handlers read idx/res while applyDocumentChange mutates them, so we
// guard idx/res with sync.RWMutex: applyDocumentChange holds the write lock
// when updating idx/res; handlers hold the read lock when reading them.
// The discipline is: build a fresh index/resolution set, then atomically swap
// both pointers under the write lock (following the build-then-publish pattern
// in tasks.md F7 / F6a). Handlers copy the pointers under the read lock so they
// see a consistent (idx, res) pair for the duration of a single request.
type handlerContext struct {
	idxResMu sync.RWMutex // guards idx and res

	idx         *workspace.Index              // workspace index; nil until "initialized"
	res         *workspace.ResolutionSet      // resolution set (feature 10, T7); computed after index build in initialized handler
	posEncoding protocol.PositionEncodingKind // negotiated in "initialize"; used by all position converters (T1)
	store       *document.Store               // in-memory open-document view; nil until "initialize" negotiates the root (feature 20)
	root        string                        // negotiated absolute workspace root path; set in "initialize" (feature 20)
	cfg         config.Config                 // workspace configuration; loaded in "initialize" via config.Bootstrap (feature 20)
	az          analysis.Analyzer             // the analyzer (needed for applyDocumentChange re-analysis)
	logger      *slog.Logger                  // structured logger; MUST NOT write to the protocol stream
	watcher     *document.Watcher             // fsnotify watcher; started in "initialize" against the negotiated root (feature 20)

	// probe captures the root-discovery inputs (client paths, cwd fallback,
	// sentinel-found) recorded at "initialize" so the no-usable-root condition
	// can be evaluated ONCE at index-build time (feature 20, Story 3, T5/T6).
	probe rootProbe
}

// Run serves a JSON-RPC connection from an in-memory or stdio reader/writer.
// It reads requests, dispatches them, and writes responses back. The server
// completes the initialize/shutdown lifecycle per FR-41.
//
// Parameters:
//   - ctx: context for background work; cancelled on shutdown (ADR-012)
//   - r: input reader (stdin in production, bytes.Buffer in tests)
//   - w: output writer (stdout in production, bytes.Buffer in tests)
//   - version: the server version string (from main's build var, reported in serverInfo)
//   - cwdFallback: the process working directory (os.Getwd) used as the
//     lowest-precedence root-discovery start point when the client sends no
//     workspaceFolders/rootUri (feature 20, Variant A). Root and config are NOT
//     resolved here — they are resolved in the "initialize" handler from the
//     client-supplied path (see resolveRootStart + config.Bootstrap below).
//   - az: the analyzer backend (from analysis/natural or a stub in tests)
//   - logger: structured logger directed at stderr; MUST NOT write to w
//
// Run returns nil on a clean shutdown sequence or on a recoverable input error
// (malformed message). It returns a non-nil error only for unrecoverable failures
// such as being unable to write a response or context cancellation.
func Run(ctx context.Context, r io.Reader, w io.Writer, version, cwdFallback string, az analysis.Analyzer, logger *slog.Logger) error {
	// Lifecycle state machine
	state := statePreInit

	// clientSupportsWatchedFilesReg tracks whether the client supports dynamic registration
	// for workspace/didChangeWatchedFiles (parsed from initialize params, used in initialized handler).
	// Initially false; set to true by handleInitialize if the client advertises support.
	clientSupportsWatchedFilesReg := false

	// clientSupportsWorkDoneProgress tracks whether the client supports server-initiated
	// work-done progress (parsed from initialize params, used in initialized handler).
	// Initially false; set to true by handleInitialize if the client advertises support (feature 21, T1).
	clientSupportsWorkDoneProgress := false

	// bgCtx is the context for all background goroutines spawned by this server
	// instance (indexer, watcher, etc.). It is derived from the caller's ctx so
	// that external cancellation also propagates. bgCancel is called on shutdown
	// (before exit returns) so that background work stops promptly — ADR-012
	// shutdown hook.
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel() // ADR-012: cancel background work on any exit path

	// bgBuild tracks the background index-build goroutine spawned in the
	// "initialized" handler (feature 21, T4). Run must join it before returning
	// so (a) the goroutine never writes to the stream or touches hctx after Run
	// has torn them down (no use-after-return), and (b) no goroutine leaks. The
	// deferred wait is registered AFTER defer stream.Close() (below) so, by LIFO,
	// it runs FIRST — bgCancel aborts the in-flight build (T11), the goroutine
	// exits, and only then is the stream closed.
	var bgBuild sync.WaitGroup

	// Test hook: if set, called immediately after creating bgCtx to allow tests
	// to observe the background context (for ADR-012 verification).
	bgCtxHookMu.Lock()
	hook := bgCtxHook
	bgCtxHookMu.Unlock()
	if hook != nil {
		hook(bgCtx)
	}

	// hctx bundles all state that request handlers need (feature 10, T2).
	// Fields are filled incrementally: root/cfg/store/watcher are set when
	// "initialize" is processed (feature 20 — deferred bootstrap: the root is
	// negotiated from the client's workspaceFolders/rootUri, so it cannot be known
	// at Run start); posEncoding is also set at "initialize"; idx is set when
	// "initialized" is processed and the index build completes. Until "initialize"
	// runs, hctx.store is nil (no didOpen/didChange arrives before "initialized",
	// so nothing needs it), hctx.idx is nil, and hctx.posEncoding is the zero
	// string (which equals PositionEncodingKindUTF16 — safe default, ADR-008).
	hctx := &handlerContext{
		az:     az,
		logger: logger,
	}

	// The filesystem watcher is started in the "initialize" handler (against the
	// negotiated root), so it may be nil here and set later. Closing it on exit is
	// deferred via this closure, which reads the (possibly-updated) hctx.watcher.
	defer func() {
		if hctx.watcher != nil {
			hctx.watcher.Close()
		}
	}()

	// Wrap the reader and writer into a ReadWriteCloser for jsonrpc2.NewHeaderStream.
	conn := &readWriteCloser{r: r, w: w}
	stream := jsonrpc2.NewHeaderStream(conn)
	defer stream.Close()

	// Join the background build goroutine before the stream is closed (feature
	// 21, T4). Registered after defer stream.Close() so LIFO runs it FIRST:
	// bgCancel() aborts any in-flight workspace.Build mid-scan (T11), then Wait()
	// blocks until the goroutine has observed cancellation and returned. This
	// guarantees the goroutine never races the stream teardown or writes after
	// Run returns. bgCancel is idempotent (also deferred at the top of Run).
	defer func() {
		bgCancel()
		bgBuild.Wait()
	}()

	// done is closed when Run returns so the context-watcher goroutine below always
	// exits — whether Run returns normally (EOF/exit notification) or via a ctx
	// cancellation. Registered after defer stream.Close() so it fires first (LIFO),
	// letting the watcher exit before the stream is cleaned up.
	done := make(chan struct{})
	defer close(done)

	// Context-watcher: close the stream when ctx is cancelled so that any
	// blocking bufio.Reader.Read inside headerStream.ReadFrame is unblocked.
	// headerStream.ReadFrame only checks ctx.Err() before entering bufio.Reader —
	// it has no way to interrupt a blocking I/O mid-read. Closing the underlying
	// connection causes the blocked Read to return an error, which propagates up
	// through ReadFrame and back to the loop below as a non-nil error.
	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		case <-done:
			// Run returned normally; defer stream.Close() handles cleanup.
		}
	}()

	// sendError encodes and writes a JSON-RPC error response. Write failures are
	// logged rather than returned: the connection is likely broken, and the next
	// stream.Read will surface the same failure on the read path.
	sendError := func(id jsonrpc2.ID, code jsonrpc2.Code, msg string) {
		resp := jsonrpc2.NewResponse(id, nil, jsonrpc2.NewError(code, msg))
		_, writeErr := stream.Write(ctx, resp)
		if writeErr != nil {
			logger.Error("write error response", "err", writeErr)
		}
	}

	// publishDiag publishes diagnostics for a file URI (T7, Feature 14).
	// Errors are logged (FR-43) but don't crash the dispatch loop.
	publishDiag := func(uriStr string) {
		if err := hctx.publishFileDiagnostics(ctx, stream, uriStr); err != nil {
			logger.Warn("failed to publish diagnostics", "uri", uriStr, "err", err)
		}
	}

	for {
		// Read one JSON-RPC message from the framed stream. When the context is
		// cancelled, the context-watcher goroutine above closes the stream, which
		// unblocks the blocking bufio.Reader inside headerStream and causes Read
		// to return an error (io.EOF or io.ErrClosedPipe). The error cases below
		// then route context cancellation to a clean nil return.
		msg, _, err := stream.Read(ctx)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			// Context cancellation or deadline exceeded: clean exit (explicit signal).
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil // clean shutdown via context cancellation (e.g. SIGTERM)
			}
			// The context-watcher goroutine closes the stream when ctx is cancelled,
			// unblocking any pending bufio.Read. That close may race with the read and
			// return a connection-closed error (e.g. io.ErrClosedPipe) before the next
			// Read attempt has a chance to see ctx.Err(). Check the context here so we
			// don't log a spurious error or treat a clean shutdown as a protocol fault.
			if ctx.Err() != nil {
				return nil
			}
			// Stream position is unknown; unrecoverable
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("stream closed unexpectedly: %w", err)
			}
			logger.Error("malformed JSON-RPC message; skipping", "err", err)
			continue
		}

		// Route notifications (no id) before handling Calls (requests with id).
		if notif, ok := msg.(*jsonrpc2.Notification); ok {
			// Check for "exit" before the panic recovery wrapper, since exit needs to return from the outer loop.
			if notif.Method() == "exit" {
				if state != stateShutdown {
					return fmt.Errorf("exit without shutdown")
				}
				return nil
			}

			// Panic recovery wraps the notification dispatch switch.
			// Notifications have no id, so there is NO error response to send —
			// recovery is log-and-continue only (FR-43, Task 7).
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("panic in notification dispatch", "method", notif.Method(), "panic", r)
					}
				}()

				switch notif.Method() {
				case "initialized":
					// Transition to stateInitialized only from statePreInit.
					// Receiving "initialized" after shutdown is a client misbehaviour;
					// silently ignore it rather than crashing.
					if state == statePreInit {
						state = stateInitialized

						// Feature 21 T4 (NFR-5): run the initial index build on a
						// BACKGROUND goroutine so a cold build never stalls the
						// dispatch loop. The handler returns immediately after
						// spawning, so requests queued behind "initialized" are
						// serviced while the build is in flight (store-first
						// providers answer; index-backed providers degrade to
						// null/empty until the build publishes — FR-43).
						//
						// The goroutine is tied to bgCtx: on shutdown, bgCancel
						// aborts the in-flight workspace.Build mid-scan (T11) and
						// the guard below skips publish/hook/report (no
						// publish-after-shutdown, Story 2 AC5). bgBuild joins it in
						// Run's deferred cleanup so it never leaks and never writes
						// to the stream or hctx after Run returns.
						// Feature 21 T5/T6: construct the work-done progress
						// reporter for this build. It is ENABLED only when the
						// client advertised window.workDoneProgress (T1 flag,
						// negotiated at "initialize", which always precedes
						// "initialized"); otherwise it is a no-op object, so a
						// non-supporting client sees NO create/$/progress on the
						// wire while the async build still runs (Story 1 AC2).
						// The token is shared across create/begin/report/end
						// (OQ-C). No server capability is advertised for progress
						// — like publishDiagnostics, it needs none.
						reporter := newProgressReporter(stream, protocol.String("natural-lsp-index"), logger, clientSupportsWorkDoneProgress)

						bgBuild.Add(1)
						go func() {
							defer bgBuild.Done()

							// Fire-and-forget create, then begin (OQ-A option (i)):
							// send window/workDoneProgress/create WITHOUT awaiting
							// its response, then immediately send begin. The serial
							// dispatch loop cannot both block on a response and keep
							// reading, and a client that advertised
							// window.workDoneProgress is expected to accept
							// server-initiated progress right after create; the
							// create response stays logged-only in the Response
							// branch. Ordering on the wire is create → begin →
							// (report* in T7) → end, all sharing one token.
							_ = reporter.create(bgCtx)
							_ = reporter.begin(bgCtx, "Indexing Natural workspace")

							// buildIndex runs workspace.Build + workspace.Resolve
							// OFF the dispatch loop and returns the fresh (idx, res)
							// pair without touching hctx. The onProgress callback
							// (T7 / OQ-E) forwards each (path, current, total) from
							// the build into reporter.report on the background
							// goroutine, so the client sees a rising report sequence
							// "1/N", "2/N", ..., "N/N". A disabled reporter is a
							// no-op; this happens on the goroutine, not the dispatch
							// loop, so stream.Write is safe (jsonrpc2.Stream
							// serializes writes). bgCtx is passed so shutdown aborts
							// the build (T11 returns ctx.Err()).
							//
							// Errors are logged but don't abort the server (FR-43):
							// providers guard on idx==nil and return null/empty.
							idx, res, buildErr := hctx.buildIndex(bgCtx, func(path string, current, total int) {
								_ = reporter.report(bgCtx, current, total, path)
							})
							if buildErr != nil {
								hctx.logger.Error("failed to build workspace index", "err", buildErr)
								// Fall through with a nil idx so publishIndex leaves
								// handlers degrading gracefully — unless cancelled
								// (checked next), in which case we publish nothing.
							}

							// No-publish-after-shutdown guard (Story 2 AC5): if
							// shutdown raced the build and cancelled bgCtx, skip
							// publish, hook, report, AND the progress "end". We do
							// NOT emit progress for an aborted build — writing end to
							// a stream Run is tearing down is exactly what the guard
							// avoids (bgBuild.Wait joins us before stream.Close, but
							// skipping keeps the shutdown clean and emits no dangling
							// progress). create/begin may already be on the wire;
							// that is a harmless orphaned progress token the client
							// discards, non-fatal (FR-43).
							if bgCtx.Err() != nil {
								return
							}

							// F7 build-then-publish: commit (idx, res) atomically
							// under idxResMu. publishIndex takes the write lock;
							// providers snapshot the pair under the read lock, so
							// there is no torn (old idx, new res) observable.
							hctx.publishIndex(idx, res)

							// Feature 21 (T13 / OQ-B.1): replay any open-buffer edits
							// that arrived DURING the build into the freshly-published
							// index. A didChange racing the cold build lands only in
							// the store (applyDocumentChange saw idx==nil); this merge
							// makes index-backed providers reflect it too. It runs
							// under idxResMu (F7) and BEFORE reportNoUsableRoot/hook so
							// the no-usable-root count and any hook-gated test observe
							// the fully-realized index (an open buffer counts as usable
							// content).
							hctx.replayOpenBuffers()

							// Feature 21 T5 (OQ-D): close the progress UI with a
							// single "end" AFTER publish+replay but BEFORE the
							// no-usable-root window/showMessage, so the progress
							// notification retires before the actionable warning
							// surfaces. This is the last progress message and shares
							// the create/begin token. A disabled reporter writes
							// nothing (Story 1 AC2). Report* messages are wired in T7;
							// for now begin→end bracket the build with no reports.
							_ = reporter.end(bgCtx, "Indexing complete")

							// Feature 20 (Story 3, T5/T6): report the no-usable-root
							// condition ONCE, AFTER publish (OQ-D end-first ordering:
							// the no-usable-root signal reflects the built index's
							// file count and fires after the index is known). Emits a
							// Warn on stderr and a window/showMessage Warning to the
							// client when the root could not be established or the
							// index is empty; a healthy, populated root emits nothing.
							// stream.Write is safe from this goroutine — headerStream
							// serializes writes under its own writeMu, so this never
							// races the dispatch loop's response writes.
							hctx.reportNoUsableRoot(bgCtx, stream)

							// Test hook: fires as the goroutine's FINAL action, after
							// publish AND reportNoUsableRoot, so a test that waits on
							// it observes a fully-published index AND the completed
							// no-usable-root signal (feature 10 T2; feature 21 T4 uses
							// it as the async-build "everything done" sync point so
							// pre-fed lifecycle tests can withhold shutdown until the
							// build is fully finished). Reads hctx.idx/posEncoding
							// under the read lock — hctx.idx is only written here
							// (single background builder) via publishIndex.
							indexReadyHookMu.Lock()
							hook := indexReadyHook
							indexReadyHookMu.Unlock()
							if hook != nil {
								hctx.idxResMu.RLock()
								publishedIdx := hctx.idx
								enc := hctx.posEncoding
								hctx.idxResMu.RUnlock()
								hook(publishedIdx, enc)
							}
						}()

						// FR-34, A2: if the client supports dynamic registration for workspace/didChangeWatchedFiles,
						// send a client/registerCapability request to register our interest in file change events.
						// This is a call (not a notification) — the client's response will be handled below.
						if clientSupportsWatchedFilesReg {
							// Build registration options: one watcher per indexed extension so the
							// client notifies the server of create/change/delete events for those files.
							regOpts, optsErr := buildWatchedFilesRegisterOptions(hctx.cfg.Workspace.Extensions)
							if optsErr != nil {
								logger.Error("failed to build watchedFiles register options", "err", optsErr)
								break
							}
							regParams := protocol.RegistrationParams{
								Registrations: []protocol.Registration{
									{
										// Stable string ID — used if the server ever sends an
										// unregisterCapability to revoke this registration.
										ID:              "natural-lsp-watched-files",
										Method:          "workspace/didChangeWatchedFiles",
										RegisterOptions: regOpts,
									},
								},
							}

							// Serialize regParams to JSON.
							var paramsBuf bytes.Buffer
							paramsEnc := jsontext.NewEncoder(&paramsBuf)
							if err := regParams.MarshalJSONTo(paramsEnc); err != nil {
								logger.Error("failed to marshal registration params", "err", err)
							} else {
								// Use a stable string ID so the log message and any future
								// unregisterCapability call are readable without a magic number.
								regID := jsonrpc2.NewStringID("natural-lsp-watched-files-reg")
								call := jsonrpc2.NewCall(regID, "client/registerCapability", jsonrpc2.RawMessage(paramsBuf.Bytes()))
								if _, err := stream.Write(ctx, call); err != nil {
									logger.Error("failed to send client/registerCapability", "err", err)
									// non-fatal: FR-43
								}
							}
						}
					}
				case "test/panic-notification":
					// TEST-ONLY INFRASTRUCTURE: this case exists solely to let
					// TestNotificationPanicRecovery exercise the per-notification
					// panic-recovery path (FR-43). It is permanent: the test hook is
					// intentional dead code in production, and a build tag is not used
					// because the hook is trivial and segregating it would add build
					// complexity for no meaningful safety gain.
					panic("test panic for FR-43 notification recovery")
				case "textDocument/didOpen":
					// Only handle didOpen in the fully initialized state (FR-33, Task 5).
					// Notifications arriving before "initialized" or after "shutdown" are
					// silently ignored per LSP §3.4 — no response is sent for notifications.
					if state == stateInitialized {
						var params protocol.DidOpenTextDocumentParams
						dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
						if err := params.UnmarshalJSONFrom(dec); err != nil {
							logger.Error("invalid textDocument/didOpen params", "err", err)
						} else {
							u := params.TextDocument.URI
							hctx.store.Open(u, int(params.TextDocument.Version), []byte(params.TextDocument.Text))
							// T7: publish diagnostics after opening (S3-AC1)
							publishDiag(string(u))
						}
					}
				case "textDocument/didChange":
					// FR-33, Task 6: handle document content changes.
					// Only in stateInitialized; notifications get no response.
					if state == stateInitialized {
						var params protocol.DidChangeTextDocumentParams
						dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
						if err := params.UnmarshalJSONFrom(dec); err != nil {
							logger.Error("invalid textDocument/didChange params", "err", err)
						} else {
							u := params.TextDocument.URI
							// Handle each content change; full sync means we expect a single whole-document change
							for _, change := range params.ContentChanges {
								if whole, ok := change.(*protocol.TextDocumentContentChangeWholeDocument); ok {
									// Update the in-memory store
									hctx.store.Update(u, int(params.TextDocument.Version), []byte(whole.Text))

									// Feature 10, T14: update the workspace index and resolution set
									// Convert URI to relative path for index update
									absPath := u.FsPath()
									relPath, pathErr := filepath.Rel(hctx.root, absPath)
									if pathErr != nil {
										logger.Error("failed to compute relative path for didChange", "uri", u, "err", pathErr)
										continue
									}
									relPath = strings.ReplaceAll(relPath, "\\", "/")

									// Apply the change to the index and resolution
									hctx.applyDocumentChange(relPath, []byte(whole.Text))
									// T7: publish diagnostics after change (S3-AC1)
									publishDiag(string(u))
								} else if _, ok := change.(*protocol.TextDocumentContentChangePartial); ok {
									// Partial (range) edit under Full-sync policy: log and skip
									logger.Error("received partial change under full-sync policy; skipping", "uri", u)
								}
							}
						}
					}
				case "textDocument/didClose":
					// FR-33, Task 6: handle document close.
					// Only in stateInitialized; notifications get no response.
					if state == stateInitialized {
						var params protocol.DidCloseTextDocumentParams
						dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
						if err := params.UnmarshalJSONFrom(dec); err != nil {
							logger.Error("invalid textDocument/didClose params", "err", err)
						} else {
							u := params.TextDocument.URI
							hctx.store.Close(u)
							// T7: publish empty diagnostics to clear stale issues on close (OQ-3).
							// Publish directly with empty array (no version since file is no longer open).
							if err := publishDiagnostics(ctx, stream, string(u), nil, nil); err != nil {
								logger.Warn("failed to publish diagnostics on close", "uri", string(u), "err", err)
							}
						}
					}
				case "workspace/didChangeWatchedFiles":
					// FR-34, Task 9 (A2): handle externally-changed files (client-pushed).
					// Only in stateInitialized; notifications get no response.
					if state == stateInitialized {
						var params protocol.DidChangeWatchedFilesParams
						dec := jsontext.NewDecoder(bytes.NewReader(notif.Params()))
						if err := params.UnmarshalJSONFrom(dec); err != nil {
							logger.Error("invalid workspace/didChangeWatchedFiles params", "err", err)
						} else {
							// Dispatch re-analysis for each changed file.
							for _, event := range params.Changes {
								// Get the file path from the URI.
								absPath := event.URI.FsPath()
								// Derive the relative path.
								relPath, err := filepath.Rel(hctx.root, absPath)
								if err != nil {
									logger.Error("failed to compute relative path", "absPath", absPath, "root", hctx.root, "err", err)
									continue
								}
								relPath = strings.ReplaceAll(relPath, "\\", "/")

								// Handle file change type:
								// - FileChangeTypeDeleted (3): pass nil content to signal removal
								// - Others: read the file and analyze (if it exists and is not too large)
								if event.Type == protocol.FileChangeTypeDeleted {
									analyzeOne(hctx.cfg, hctx.az, relPath, nil, logger)
									// Feature 10, T14: update index/resolution for deletion
									hctx.applyDocumentChange(relPath, nil)
									// T7: publish empty diagnostics to clear on delete (S3-AC1).
									// Publish directly with empty array.
									if err := publishDiagnostics(ctx, stream, string(event.URI), nil, nil); err != nil {
										logger.Warn("failed to publish diagnostics on delete", "uri", string(event.URI), "err", err)
									}
									continue
								}
								// For create/change events: read and analyze the file
								content, err := os.ReadFile(absPath)
								if err != nil {
									logger.Error("failed to read file for re-analysis", "path", relPath, "err", err)
									continue
								}
								analyzeOne(hctx.cfg, hctx.az, relPath, content, logger)
								// Feature 10, T14: update index/resolution
								hctx.applyDocumentChange(relPath, content)
								// T7: publish diagnostics after change (S3-AC1)
								publishDiag(string(event.URI))
							}
						}
					}
				default:
					// Unknown notifications are silently ignored (LSP §3.4).
				}
			}()
			continue
		}

		// Handle Responses from the client (e.g. response to our client/registerCapability call).
		if resp, ok := msg.(*jsonrpc2.Response); ok {
			// A client error response to client/registerCapability means the registration
			// was rejected; the server can continue but file-change notifications won't arrive
			// for those paths, so log at Warn rather than silently absorbing it.
			if resp.Err() != nil {
				logger.Warn("client rejected server request", "id", resp.ID(), "err", resp.Err())
			} else {
				logger.Debug("client acknowledged server request", "id", resp.ID())
			}
			continue
		}

		// All other messages must be Calls (requests that require a response).
		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			// Neither a Notification nor a Call nor a Response — malformed; skip.
			logger.Error("unexpected JSON-RPC message type; skipping", "type", fmt.Sprintf("%T", msg))
			continue
		}

		method := call.Method()

		// Gate: any request other than "initialize" before initialization is an error.
		if state == statePreInit && method != "initialize" {
			sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
			continue
		}

		var respResult []byte

		// Panic recovery wraps only the dispatch switch — deliberately not the
		// response write below. Panics from stream.Write propagate to the caller
		// because they indicate an unrecoverable I/O failure, not a handler bug (FR-43).
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("panic in request dispatch", "panic", r)
					sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("panic: %v", r))
				}
			}()

			switch method {
			case "initialize":
				if state != statePreInit {
					// Duplicate initialize — reject.
					sendError(call.ID(), jsonrpc2.InvalidRequest, "already initialized")
					return
				}
				var params protocol.InitializeParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid initialize params: %v", err))
					return
				}
				negotiation, err := handleInitialize(params, version)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				respResult = negotiation.result
				hctx.posEncoding = negotiation.posEncoding
				clientSupportsWatchedFilesReg = negotiation.clientSupportsWatchedFilesReg
				clientSupportsWorkDoneProgress = negotiation.clientSupportsWorkDoneProgress

				// Feature 20 (Variant A): resolve the workspace root from the client's
				// initialize params (workspaceFolders → rootUri → cwdFallback) and load
				// config from that path via config.Bootstrap. The sentinel walk-up runs
				// FROM the client-supplied path (OQ-4), so a stray sentinel in the launch
				// cwd never influences discovery when the client sends a root.
				//
				// CR-6: Bootstrap never hard-fails — a missing sentinel or a bad config
				// degrades to usable defaults and logs Problems; the returned error is
				// always nil, so a config problem must NOT fail the initialize response.
				start := resolveRootStart(params, cwdFallback)
				root, cfg, _ := config.Bootstrap(start, "", logger)
				hctx.root = root
				hctx.cfg = cfg

				// Feature 20 (Story 3, T5/T6): record the root-discovery inputs so
				// the no-usable-root condition can be evaluated ONCE after the index
				// build (in "initialized"), naming every path tried. FindRoot is pure
				// and cheap; Bootstrap already ran it internally, so re-deriving the
				// sentinel-found signal here is a negligible second walk-up.
				_, sentinelFound := config.FindRoot(start)
				hctx.probe = rootProbe{
					clientPaths:   clientRootPaths(params),
					cwdFallback:   cwdFallback,
					sentinelFound: sentinelFound,
					resolvedRoot:  root,
				}

				// Test hook: observe the negotiated root/cfg (feature 20, T2).
				// As of feature 21 (T1), also pass clientSupportsWorkDoneProgress for capability detection testing.
				initializeReadyHookMu.Lock()
				initHook := initializeReadyHook
				initializeReadyHookMu.Unlock()
				if initHook != nil {
					initHook(root, cfg, clientSupportsWorkDoneProgress)
				}

				// Construct the document store now that the negotiated root is known
				// (the store keys URI→relPath on the root). No didOpen/didChange can
				// arrive before "initialized", so nothing needs the store earlier.
				hctx.store = document.New(root, func(relPath string, content []byte) model.FileAnalysis {
					result := analyzeOne(hctx.cfg, hctx.az, relPath, content, logger)
					return result.FileAnalysis
				}, logger)

				// Start the filesystem watcher (FR-34) against the negotiated root.
				// Watcher-start failure is non-fatal (FR-43): the server keeps running
				// without external-change detection. It is closed on exit via the
				// deferred closure at the top of Run (which reads hctx.watcher).
				watcher, watchErr := document.NewWatcher(bgCtx, root, &hctx.cfg, func(relPath string, content []byte) model.FileAnalysis {
					result := analyzeOne(hctx.cfg, hctx.az, relPath, content, logger)
					return result.FileAnalysis
				}, logger)
				if watchErr != nil {
					logger.Error("failed to start file watcher", "err", watchErr) // FR-43: non-fatal
				} else {
					hctx.watcher = watcher
				}

			case "shutdown":
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.InvalidRequest, "shutdown before initialization")
					return
				}
				state = stateShutdown
				bgCancel() // ADR-012: cancel background work at shutdown
				respResult = []byte(`null`)

			case "test/panic":
				// TEST-ONLY INFRASTRUCTURE: this case exists solely to let
				// TestRequestPanicRecovery exercise the per-request panic-recovery
				// path (FR-43). It is permanent: the test hook is intentional dead
				// code in production, and a build tag is not used because the hook
				// is trivial and segregating it would add build complexity for no
				// meaningful safety gain.
				panic("test panic for FR-43")

			case "textDocument/definition":
				// Feature 10, T4: go-to-definition handler skeleton.
				// Gate on stateInitialized; decode DefinitionParams; call provideDefinition.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.DefinitionParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid definition params: %v", err))
					return
				}
				// Call the provider function (T4: returns empty for now; T7: adds resolution logic).
				locations, err := provideDefinition(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: locations may be nil (empty) for a no-edge case.
				if locations == nil {
					respResult = []byte(`null`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(locations)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal locations: %v", marshalErr))
						return
					}
				}

			case "textDocument/references":
				// Feature 10, T10: find-all-references handler skeleton.
				// Gate on stateInitialized; decode ReferenceParams; call provideReferences.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.ReferenceParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid references params: %v", err))
					return
				}
				// Call the provider function (T10: returns empty for now; T11: adds sweep logic).
				locations, err := provideReferences(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: locations may be nil (empty) for a no-symbol case.
				if locations == nil {
					respResult = []byte(`null`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(locations)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal locations: %v", marshalErr))
						return
					}
				}

			case "workspace/symbol":
				// Feature 10, T13: workspace symbol search handler.
				// Gate on stateInitialized; decode WorkspaceSymbolParams; call provideWorkspaceSymbols.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.WorkspaceSymbolParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid workspace symbol params: %v", err))
					return
				}
				// Call the provider function.
				symbols := provideWorkspaceSymbols(hctx, params.Query)
				// Marshal the result: symbols may be nil (empty) for a no-match case.
				if symbols == nil {
					respResult = []byte(`[]`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(symbols)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal symbols: %v", marshalErr))
						return
					}
				}

			case "textDocument/documentSymbol":
				// Feature 11, T3: document outline handler.
				// Gate on stateInitialized; decode DocumentSymbolParams; call provideDocumentSymbols.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.DocumentSymbolParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid document symbol params: %v", err))
					return
				}
				// Call the provider function.
				docSymbols, err := provideDocumentSymbols(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: docSymbols may be nil (empty) for a not-found case.
				if docSymbols == nil {
					respResult = []byte(`null`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(docSymbols)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal document symbols: %v", marshalErr))
						return
					}
				}

			case "textDocument/hover":
				// Feature 12, T6: hover provider handler.
				// Gate on stateInitialized; decode HoverParams; call provideHover.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.HoverParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid hover params: %v", err))
					return
				}
				// Call the provider function.
				hover, err := provideHover(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: hover may be nil (no hover at that position).
				if hover == nil {
					respResult = []byte(`null`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(hover)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal hover: %v", marshalErr))
						return
					}
				}

			case "textDocument/codeLens":
				// Feature 13, T6: code lens provider handler.
				// Gate on stateInitialized; decode CodeLensParams; call provideCodeLens.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.CodeLensParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid code lens params: %v", err))
					return
				}
				// Call the provider function.
				lenses, err := provideCodeLens(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: lenses may be nil (empty) for a no-match case.
				if lenses == nil {
					respResult = []byte(`null`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(lenses)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal code lenses: %v", marshalErr))
						return
					}
				}

			case "textDocument/completion":
				// Feature 16, T3: completion provider handler.
				// Gate on stateInitialized; decode CompletionParams; call provideCompletion.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.CompletionParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid completion params: %v", err))
					return
				}
				// Call the provider function.
				items, err := provideCompletion(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: items may be nil (empty) for a no-match case.
				// Important: return [] for empty, never null (completion list is always an array).
				if items == nil {
					respResult = []byte(`[]`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(items)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal completion items: %v", marshalErr))
						return
					}
				}

			case "textDocument/signatureHelp":
				// Feature 17, T1: signature help provider handler.
				// Gate on stateInitialized; decode SignatureHelpParams; call provideSignatureHelp.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.SignatureHelpParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid signature help params: %v", err))
					return
				}
				// Call the provider function.
				sig, err := provideSignatureHelp(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: sig may be nil (no signature at that position).
				// CRITICAL: SignatureHelp contains union/Nullable fields (ActiveParameter, ParameterInformation.Label).
				// These MUST be marshaled via (*protocol.SignatureHelp).MarshalJSONTo(jsontext.NewEncoder(&buf)),
				// NOT json.Marshal, or the union/nullable fields will be wrong/empty.
				// See divergence note in tasks.md and the pattern in handleInitialize.
				if sig == nil {
					respResult = []byte(`null`)
				} else {
					var buf bytes.Buffer
					enc := jsontext.NewEncoder(&buf)
					if err := sig.MarshalJSONTo(enc); err != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal signature help: %v", err))
						return
					}
					respResult = buf.Bytes()
				}

			case "textDocument/prepareCallHierarchy":
				// Feature 18, T1: prepare call hierarchy handler.
				// Gate on stateInitialized; decode CallHierarchyPrepareParams; call providePrepareCallHierarchy.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.CallHierarchyPrepareParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid prepare call hierarchy params: %v", err))
					return
				}
				// Call the provider function.
				items, err := providePrepareCallHierarchy(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: items may be nil (empty) for a no-item case.
				// Important: return [] for empty, never null (arrays are always arrays).
				if items == nil {
					respResult = []byte(`[]`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(items)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal prepare items: %v", marshalErr))
						return
					}
				}

			case "callHierarchy/incomingCalls":
				// Feature 18, T1: incoming calls handler.
				// Gate on stateInitialized; decode CallHierarchyIncomingCallsParams; call provideIncomingCalls.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.CallHierarchyIncomingCallsParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid incoming calls params: %v", err))
					return
				}
				// Call the provider function.
				calls, err := provideIncomingCalls(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: calls may be nil (empty) for a no-callers case.
				// Important: return [] for empty, never null (arrays are always arrays).
				if calls == nil {
					respResult = []byte(`[]`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(calls)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal incoming calls: %v", marshalErr))
						return
					}
				}

			case "callHierarchy/outgoingCalls":
				// Feature 18, T1: outgoing calls handler.
				// Gate on stateInitialized; decode CallHierarchyOutgoingCallsParams; call provideOutgoingCalls.
				if state != stateInitialized {
					sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
					return
				}
				var params protocol.CallHierarchyOutgoingCallsParams
				dec := jsontext.NewDecoder(bytes.NewReader(call.Params()))
				if err := params.UnmarshalJSONFrom(dec); err != nil {
					sendError(call.ID(), jsonrpc2.InvalidParams, fmt.Sprintf("invalid outgoing calls params: %v", err))
					return
				}
				// Call the provider function.
				calls, err := provideOutgoingCalls(hctx, params)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
				}
				// Marshal the result: calls may be nil (empty) for a no-callees case.
				// Important: return [] for empty, never null (arrays are always arrays).
				if calls == nil {
					respResult = []byte(`[]`)
				} else {
					var marshalErr error
					respResult, marshalErr = marshalResult(calls)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal outgoing calls: %v", marshalErr))
						return
					}
				}

			default:
				// Unknown method — send MethodNotFound per JSON-RPC 2.0 §5.1 and LSP §3.1.
				// MethodNotFound is the spec-correct response and prevents silently swallowing
				// methods that a client expects to be handled.
				sendError(call.ID(), jsonrpc2.MethodNotFound, fmt.Sprintf("method not found: %s", method))
			}
		}()

		// Build and send the success response (unless panic or error handler already sent a response).
		// If panic was recovered, the handler above already sent InternalError via sendError.
		if respResult != nil {
			response := jsonrpc2.NewResponse(call.ID(), jsonrpc2.RawMessage(respResult), nil)
			_, err := stream.Write(ctx, response)
			if err != nil {
				return fmt.Errorf("write response: %w", err)
			}
		}
	}
}

// reportNoUsableRoot evaluates the no-usable-root condition (feature 20, Story 3,
// T5/T6) after the index build and, when it holds, emits an actionable signal
// ONCE: a Warn line on the server's stderr logger (naming every path tried) and
// a window/showMessage Warning notification to the client. A healthy, populated
// root emits nothing.
//
// This is deliberately called from the "initialized" handler (once), not from
// any per-request path, so there is no log spam. The index-file count is read
// under the idxResMu read lock (a nil index counts as 0 files → empty).
//
// FR-43: a window/showMessage write failure is logged, never fatal — the server
// keeps serving. Requests against files outside the root still degrade to
// null/empty via the providers' existing graceful-degradation paths (unchanged).
func (hctx *handlerContext) reportNoUsableRoot(ctx context.Context, stream jsonrpc2.Stream) {
	hctx.idxResMu.RLock()
	fileCount := 0
	if hctx.idx != nil {
		fileCount = len(hctx.idx.Keys())
	}
	hctx.idxResMu.RUnlock()

	msg, warn := noUsableRootMessage(hctx.probe, fileCount)
	if !warn {
		return
	}

	// (1) Mandatory actionable stderr signal (T5).
	hctx.logger.Warn(msg,
		"clientPaths", hctx.probe.clientPaths,
		"cwdFallback", hctx.probe.cwdFallback,
		"sentinelFound", hctx.probe.sentinelFound,
		"resolvedRoot", hctx.probe.resolvedRoot,
		"indexFileCount", fileCount,
	)

	// (2) window/showMessage Warning notification (T6, OQ-3). Unilateral
	// server→client notification — no capability required, always safe to send.
	hctx.sendShowMessage(ctx, stream, protocol.MessageTypeWarning, msg)
}

// sendShowMessage writes a window/showMessage notification to the client
// (feature 20, T6). It is the codebase's first window/showMessage sender.
// The params are marshaled via (ShowMessageParams).MarshalJSONTo through a
// jsontext.Encoder — the same json/v2 path used for client/registerCapability
// params — and written as a framed JSON-RPC notification. A write/marshal
// failure is logged (FR-43), never fatal.
func (hctx *handlerContext) sendShowMessage(ctx context.Context, stream jsonrpc2.Stream, typ protocol.MessageType, message string) {
	params := protocol.ShowMessageParams{Type: typ, Message: message}
	var buf bytes.Buffer
	if err := params.MarshalJSONTo(jsontext.NewEncoder(&buf)); err != nil {
		hctx.logger.Error("failed to marshal window/showMessage params", "err", err)
		return
	}
	notif := jsonrpc2.NewNotification("window/showMessage", jsonrpc2.RawMessage(buf.Bytes()))
	if _, err := stream.Write(ctx, notif); err != nil {
		hctx.logger.Error("failed to send window/showMessage", "err", err) // FR-43: non-fatal
	}
}

// buildIndex builds the workspace index and resolution set without publishing them
// onto hctx. It runs workspace.Build (passing hctx.root, hctx.cfg, hctx.az, and
// hctx.logger) followed by workspace.Resolve, and returns the fresh (idx, res) pair
// to the caller for inspection or subsequent publishIndex. Errors from Build are
// returned; a Build error yields a nil idx (and therefore a nil res).
//
// onProgress is forwarded verbatim to workspace.BuildWithCache — pass nil to
// suppress per-file progress callbacks (T7 supplies a real callback).
//
// ctx is threaded into the build so a cancelled build (e.g. server shutdown,
// feature 21 T4/OQ-F) aborts mid-scan and returns ctx.Err() rather than running
// to completion. The background build goroutine passes bgCtx so shutdown cancels
// it.
//
// Cache wiring (feature 21 T12 / OQ-E): the build goes through
// workspace.BuildWithCache with cachePath = root/cfg.Cache.Path (a
// workspace-relative location, default ".natural-lsp-cache"). A warm start loads
// the persisted index and re-analyzes only files whose content hash changed
// (FR-38/NFR-2); a cold first run writes the cache. currentHashes is passed nil
// so BuildWithCache computes content hashes from disk itself. A corrupt or
// unreadable cache falls back to a full rebuild without error (FR-43), and a
// cache-directory creation failure is logged, never fatal — the built index is
// still valid for the session. The cache format is unchanged ("0.6.0").
//
// This method is the pure-build half of the F7 build-then-publish pattern (T3).
// The caller must call publishIndex to atomically commit the pair to hctx.
func (hctx *handlerContext) buildIndex(ctx context.Context, onProgress func(path string, current, total int)) (*workspace.Index, *workspace.ResolutionSet, error) {
	cachePath := filepath.Join(hctx.root, hctx.cfg.Cache.Path)
	idx, changedCount, totalCount, err := workspace.BuildWithCache(ctx, hctx.root, hctx.cfg, hctx.az, hctx.logger, cachePath, nil, onProgress)
	if err != nil {
		return nil, nil, fmt.Errorf("build workspace index: %w", err)
	}
	hctx.logger.Info("workspace index built",
		"total", totalCount, "reanalyzed", changedCount, "cache", cachePath,
		"warm", changedCount < totalCount)
	res := workspace.Resolve(idx, &hctx.cfg)
	return idx, res, nil
}

// publishIndex atomically commits a freshly-built (idx, res) pair to hctx
// under the idxResMu write lock, mirroring the publish half of applyDocumentChange.
// After this call, all handlers that snapshot (idx, res) under the read lock will
// see the new pair consistently — never a torn (old idx, new res) or vice versa.
//
// A nil idx is accepted (e.g. when buildIndex returned an error); handlers already
// guard on idx == nil and degrade gracefully (FR-43).
func (hctx *handlerContext) publishIndex(idx *workspace.Index, res *workspace.ResolutionSet) {
	hctx.idxResMu.Lock()
	hctx.idx = idx
	hctx.res = res
	hctx.idxResMu.Unlock()
}

// replayOpenBuffers re-applies every open-document buffer's current analysis into
// the freshly-published index (feature 21, T13 / OQ-B.1). It must be called ONCE
// on the background build goroutine, immediately AFTER publishIndex and BEFORE the
// indexReadyHook fires.
//
// Why: a didOpen/didChange that arrives WHILE the cold background build is still
// running calls applyDocumentChange with hctx.idx == nil — the edit lands in the
// document store but not in the (not-yet-published) index. When the build then
// publishes fresh DISK content, index-backed providers (workspace/symbol,
// definition, references) would serve the stale disk analysis and miss the
// in-flight edit until the next change. Store-first providers (documentSymbol)
// are unaffected. Replay closes that window by merging the store's live buffers
// into the published index.
//
// It reuses the store's already-computed model.FileAnalysis (from Store.Open/Update)
// rather than re-analyzing, so no analyzer work happens under the lock.
//
// Concurrency (F7, mirrors applyDocumentChange): the per-document relPath derivation
// runs OFF the lock; a SINGLE idxResMu write-lock section then does idx.Add for each
// buffer followed by one ResolveInto over all replayed paths, so handlers reading
// under RLock never observe a torn (some-buffers-merged) state. A didChange arriving
// during replay is serialized by the dispatch loop and the mutex — it cannot
// interleave with this lock section.
//
// A nil index (build error) or an empty store makes this a no-op.
func (hctx *handlerContext) replayOpenBuffers() {
	if hctx.store == nil {
		return
	}
	docs := hctx.store.OpenDocuments()
	if len(docs) == 0 {
		return
	}

	// Derive (relPath, analysis) pairs off the lock. Buffers outside the root are
	// skipped (they cannot key the index); FR-43 keeps a single bad doc from
	// aborting the replay of the rest.
	type replayEntry struct {
		relPath  string
		analysis model.FileAnalysis
	}
	entries := make([]replayEntry, 0, len(docs))
	for i := range docs {
		doc := docs[i]
		_, relPath, err := uriToRelPath(hctx.root, doc.URI)
		if err != nil {
			hctx.logger.Warn("replay: skipping open buffer outside workspace root", "uri", doc.URI, "err", err)
			continue
		}
		entries = append(entries, replayEntry{relPath: relPath, analysis: doc.Analysis})
	}
	if len(entries) == 0 {
		return
	}

	// Single lock section: merge every buffer, then recompute resolution once over
	// all replayed paths, mirroring applyDocumentChange's publish half.
	hctx.idxResMu.Lock()
	defer hctx.idxResMu.Unlock()

	if hctx.idx == nil {
		// The build errored (nil idx published); nothing to merge into. The store
		// still serves live buffers via store-first providers (FR-43).
		return
	}

	changedPaths := make([]string, 0, len(entries))
	for _, e := range entries {
		hctx.idx.Add(e.relPath, e.analysis)
		changedPaths = append(changedPaths, e.relPath)
	}
	hctx.res = workspace.ResolveInto(hctx.res, hctx.idx, &hctx.cfg, changedPaths)
}

// applyDocumentChange (Feature 10, T14) handles incremental updates when a document changes.
// It re-analyzes the file content, updates the workspace index, and incrementally recomputes
// the resolution set, publishing the updated state so handlers see the new results.
//
// Note: This causes double analysis on didChange (once in store.Update, once here) in the
// current design. A future refactor could pass the pre-computed FileAnalysis from the store
// callback to avoid redundant analysis. For the green phase, correctness > efficiency.
//
// Parameters:
//   - relPath: workspace-relative path to the changed file (e.g., "SUBPROG.NSP")
//   - content: the new file content (nil for deletion)
//
// Concurrency (F7): holds the write lock on idxResMu for the entire update operation
// to ensure atomicity — handlers cannot see a torn (old idx, new res) or (new idx, old res)
// pair. Handlers hold the read lock when accessing idx/res so they see consistent state.
func (hctx *handlerContext) applyDocumentChange(relPath string, content []byte) {
	// Re-analyze the file content using analyzeOne (same path as store/watcher callbacks)
	result := analyzeOne(hctx.cfg, hctx.az, relPath, content, hctx.logger)

	// Acquire the write lock: no handlers can read idx/res while we update both
	hctx.idxResMu.Lock()
	defer hctx.idxResMu.Unlock()

	// Guard: index must be initialized before we can mutate it
	if hctx.idx == nil {
		hctx.logger.Error("applyDocumentChange called before index initialized", "path", relPath)
		return
	}

	// Step 1: Update the index with the new FileAnalysis.
	// idx.Add mutates the index in place; safe because we hold the write lock.
	hctx.idx.Add(relPath, result.FileAnalysis)

	// Step 2: Incrementally recompute the resolution set for affected files.
	// ResolveInto builds and returns a FRESH ResolutionSet, leaving the old one
	// untouched so any reader holding a snapshot sees a stable, immutable view.
	// We swap the pointer under the write lock so handlers see a consistent pair.
	hctx.res = workspace.ResolveInto(hctx.res, hctx.idx, &hctx.cfg, []string{relPath})

	// Both idx and res pointers are now updated atomically; handlers reading under
	// RLock will see a consistent (old idx, old res) or (new idx, new res) pair,
	// never a torn state. The old res remains immutable for any reader still holding it.
}
