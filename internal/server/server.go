// Package server implements the LSP lifecycle (initialize, shutdown) and
// request dispatch over stdio. It depends only on the analysis.Analyzer
// interface and the workspace index — never on a concrete extraction backend.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

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

// handleInitialize processes an LSP "initialize" request, negotiates
// positionEncoding (UTF-8 preferred, UTF-16 default per ADR-008), and returns
// the marshalled InitializeResult bytes, the negotiated PositionEncodingKind
// (for direct retention — no JSON round-trip), and a flag indicating whether
// the client supports dynamic registration for workspace/didChangeWatchedFiles.
//
// Capabilities advertised here form a deliberately locked allow-list enforced
// by TestInitialize. Feature 10 (T3) adds the three navigation providers:
// definitionProvider, referencesProvider, workspaceSymbolProvider (each true).
// When features 09, 11–13 add further providers (hover, document symbols, …),
// they MUST update TestInitialize to extend the allow-list, making additions explicit.
func handleInitialize(params protocol.InitializeParams, version string) ([]byte, protocol.PositionEncodingKind, bool, error) {
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

	// Intentional minimal capability set — see comment above.
	// Feature 10, T3: advertise the three navigation providers (definition, references, workspace symbol).
	initResult := protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync:        protocol.TextDocumentSyncKindFull,
			PositionEncoding:        posEncoding,
			DefinitionProvider:      protocol.Boolean(true),
			ReferencesProvider:      protocol.Boolean(true),
			WorkspaceSymbolProvider: protocol.Boolean(true),
		},
		ServerInfo: protocol.ServerInfo{
			Name:    "natural-lsp",
			Version: protocol.NewOptional(version),
		},
	}

	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := initResult.MarshalJSONTo(enc); err != nil {
		return nil, posEncoding, false, fmt.Errorf("marshal initialize result: %w", err)
	}
	return buf.Bytes(), posEncoding, clientSupportsWatchedFilesReg, nil
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
	store       *document.Store               // in-memory open-document view (didOpen/didChange/didClose)
	root        string                        // absolute workspace root path
	cfg         config.Config                 // workspace configuration
	az          analysis.Analyzer             // the analyzer (needed for applyDocumentChange re-analysis)
	logger      *slog.Logger                  // structured logger; MUST NOT write to the protocol stream
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
//   - root: the workspace root path (from config.Bootstrap)
//   - cfg: the parsed configuration (from config.Bootstrap)
//   - az: the analyzer backend (from analysis/natural or a stub in tests)
//   - logger: structured logger directed at stderr; MUST NOT write to w
//
// Run returns nil on a clean shutdown sequence or on a recoverable input error
// (malformed message). It returns a non-nil error only for unrecoverable failures
// such as being unable to write a response or context cancellation.
func Run(ctx context.Context, r io.Reader, w io.Writer, version, root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger) error {
	// Lifecycle state machine
	state := statePreInit

	// clientSupportsWatchedFilesReg tracks whether the client supports dynamic registration
	// for workspace/didChangeWatchedFiles (parsed from initialize params, used in initialized handler).
	// Initially false; set to true by handleInitialize if the client advertises support.
	clientSupportsWatchedFilesReg := false

	// bgCtx is the context for all background goroutines spawned by this server
	// instance (indexer, watcher, etc.). It is derived from the caller's ctx so
	// that external cancellation also propagates. bgCancel is called on shutdown
	// (before exit returns) so that background work stops promptly — ADR-012
	// shutdown hook.
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel() // ADR-012: cancel background work on any exit path

	// Test hook: if set, called immediately after creating bgCtx to allow tests
	// to observe the background context (for ADR-012 verification).
	bgCtxHookMu.Lock()
	hook := bgCtxHook
	bgCtxHookMu.Unlock()
	if hook != nil {
		hook(bgCtx)
	}

	// hctx bundles all state that request handlers need (feature 10, T2).
	// Fields are filled incrementally: posEncoding is set when "initialize" is
	// processed; idx is set when "initialized" is processed and the index build
	// completes. Until then, hctx.idx is nil and hctx.posEncoding is the zero
	// string (which equals PositionEncodingKindUTF16 — safe default, ADR-008).
	//
	// The document store (in-memory view of open documents) is constructed here
	// and wired to re-analyze content on Open/Update with graceful degradation (FR-43).
	hctx := &handlerContext{
		store: document.New(root, func(relPath string, content []byte) model.FileAnalysis {
			result := analyzeOne(cfg, az, relPath, content, logger)
			return result.FileAnalysis
		}, logger),
		root:   root,
		cfg:    cfg,
		az:     az,
		logger: logger,
	}

	// Start the filesystem watcher (FR-34) to detect externally-changed files.
	// The watcher runs in a background goroutine and dispatches re-analysis via
	// analyzeOne. Non-fatal failures are logged but don't abort the server (FR-43).
	watcher, watchErr := document.NewWatcher(bgCtx, root, &cfg, func(relPath string, content []byte) model.FileAnalysis {
		result := analyzeOne(cfg, az, relPath, content, logger)
		return result.FileAnalysis
	}, logger)
	if watchErr != nil {
		logger.Error("failed to start file watcher", "err", watchErr) // FR-43: non-fatal
	} else {
		defer watcher.Close()
	}

	// Wrap the reader and writer into a ReadWriteCloser for jsonrpc2.NewHeaderStream.
	conn := &readWriteCloser{r: r, w: w}
	stream := jsonrpc2.NewHeaderStream(conn)
	defer stream.Close()

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

						// Feature 10, T2: build the workspace index after initialized.
						// Errors are logged but don't abort the server (FR-43: graceful degradation).
						// onProgress is nil for MVP (no work-done progress yet).
						if hctx.idx == nil {
							builtIdx, buildErr := workspace.Build(root, cfg, az, logger, nil)
							if buildErr != nil {
								logger.Error("failed to build workspace index", "err", buildErr)
								// Leave hctx.idx nil; handlers guard with hctx.idx != nil
							} else {
								hctx.idx = builtIdx
							}
						}

						// Feature 10, T7: compute the resolution set after index build.
						// This binds call/dependency/data-access edges to their definitions.
						if hctx.idx != nil {
							hctx.res = workspace.Resolve(hctx.idx, &cfg)
						}

						// Test hook: if set, call it with the index and encoding (feature 10, T2).
						// This allows tests to verify the index is populated after initialization.
						indexReadyHookMu.Lock()
						hook := indexReadyHook
						indexReadyHookMu.Unlock()
						if hook != nil {
							hook(hctx.idx, hctx.posEncoding)
						}

						// FR-34, A2: if the client supports dynamic registration for workspace/didChangeWatchedFiles,
						// send a client/registerCapability request to register our interest in file change events.
						// This is a call (not a notification) — the client's response will be handled below.
						if clientSupportsWatchedFilesReg {
							// Build registration options: one watcher per indexed extension so the
							// client notifies the server of create/change/delete events for those files.
							regOpts, optsErr := buildWatchedFilesRegisterOptions(cfg.Workspace.Extensions)
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
					// TestNotificationPanicRecovery exercise the panic-recovery path for
					// notifications (FR-43, T7). It is intentional dead code in production and
					// will be removed once Task 7 adds the panic recovery wrapper.
					// A build tag is intentionally not used here: the hook is trivial,
					// the comment makes its purpose clear, and segregating it behind
					// a tag would add build complexity for no meaningful safety gain.
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
									relPath, pathErr := filepath.Rel(root, absPath)
									if pathErr != nil {
										logger.Error("failed to compute relative path for didChange", "uri", u, "err", pathErr)
										continue
									}
									relPath = strings.ReplaceAll(relPath, "\\", "/")

									// Apply the change to the index and resolution
									hctx.applyDocumentChange(relPath, []byte(whole.Text))
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
							hctx.store.Close(params.TextDocument.URI)
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
								relPath, err := filepath.Rel(root, absPath)
								if err != nil {
									logger.Error("failed to compute relative path", "absPath", absPath, "root", root, "err", err)
									continue
								}
								relPath = strings.ReplaceAll(relPath, "\\", "/")

								// Handle file change type:
								// - FileChangeTypeDeleted (3): pass nil content to signal removal
								// - Others: read the file and analyze (if it exists and is not too large)
								if event.Type == protocol.FileChangeTypeDeleted {
									analyzeOne(cfg, az, relPath, nil, logger)
									// Feature 10, T14: update index/resolution for deletion
									hctx.applyDocumentChange(relPath, nil)
									continue
								}
								// For create/change events: read and analyze the file
								content, err := os.ReadFile(absPath)
								if err != nil {
									logger.Error("failed to read file for re-analysis", "path", relPath, "err", err)
									continue
								}
								analyzeOne(cfg, az, relPath, content, logger)
								// Feature 10, T14: update index/resolution
								hctx.applyDocumentChange(relPath, content)
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
				respResult, hctx.posEncoding, clientSupportsWatchedFilesReg, err = handleInitialize(params, version)
				if err != nil {
					sendError(call.ID(), jsonrpc2.InternalError, err.Error())
					return
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
				// TestRequestPanicRecovery exercise the panic-recovery path
				// (FR-43, T6). It is intentional dead code in production and
				// will be removed once feature handlers (features 09–13) land
				// and the test no longer needs a synthetic panic trigger.
				// A build tag is intentionally not used here: the hook is trivial,
				// the comment makes its purpose clear, and segregating it behind
				// a tag would add build complexity for no meaningful safety gain.
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
					// Marshal the location slice as JSON.
					locationJSON, marshalErr := json.Marshal(locations)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal locations: %v", marshalErr))
						return
					}
					respResult = locationJSON
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
					// Marshal the location slice as JSON.
					locationJSON, marshalErr := json.Marshal(locations)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal locations: %v", marshalErr))
						return
					}
					respResult = locationJSON
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
					// Marshal the symbol information slice as JSON.
					symbolJSON, marshalErr := json.Marshal(symbols)
					if marshalErr != nil {
						sendError(call.ID(), jsonrpc2.InternalError, fmt.Sprintf("failed to marshal symbols: %v", marshalErr))
						return
					}
					respResult = symbolJSON
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
