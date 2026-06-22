// Package server implements the LSP lifecycle (initialize, shutdown) and
// request dispatch over stdio. It depends only on the analysis.Analyzer
// interface and the workspace index — never on a concrete extraction backend.
package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"natural-lsp/internal/analysis"
	"natural-lsp/internal/config"
)

// bgCtxHook is a test-only hook called after creating the background context.
// It allows tests to observe the background context and its cancellation.
// Set only in tests; nil in production.
var (
	bgCtxHook   func(context.Context)
	bgCtxHookMu sync.Mutex
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
	return nil
}

// Lifecycle states
const (
	statePreInit     = 0 // Before initialize
	stateInitialized = 1 // After initialize and initialized notification
	stateShutdown    = 2 // After shutdown request
)

// Server implements an LSP server over a JSON-RPC 2.0 connection (feature 03).
type Server struct {
	// TODO: fields to be added as features land
}

// handleInitialize processes an LSP "initialize" request, negotiates
// positionEncoding (UTF-8 preferred, UTF-16 default per ADR-008), and returns
// the marshalled InitializeResult bytes.
//
// Capabilities advertised here are intentionally minimal: only textDocumentSync
// and positionEncoding. This is a deliberate allow-list locked by TestInitialize —
// when features 09–13 add a provider (hover, definition, references, …) they MUST
// update that test to extend the allow-list, making the addition explicit.
func handleInitialize(params protocol.InitializeParams, version string) ([]byte, error) {
	// Negotiate position encoding: prefer UTF-8 if offered, else fall back to UTF-16.
	posEncoding := protocol.PositionEncodingKindUTF16
	if params.Capabilities.General != nil {
		for _, enc := range params.Capabilities.General.PositionEncodings {
			if enc == protocol.PositionEncodingKindUTF8 {
				posEncoding = protocol.PositionEncodingKindUTF8
				break
			}
		}
	}

	// Intentional minimal capability set — see comment above.
	initResult := protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: protocol.TextDocumentSyncKindFull,
			PositionEncoding: posEncoding,
		},
		ServerInfo: protocol.ServerInfo{
			Name:    "natural-lsp",
			Version: protocol.NewOptional(version),
		},
	}

	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := initResult.MarshalJSONTo(enc); err != nil {
		return nil, fmt.Errorf("marshal initialize result: %w", err)
	}
	return buf.Bytes(), nil
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

	_ = bgCtx // bgCtx will be passed to background goroutines in future features

	// Wrap the reader and writer into a ReadWriteCloser for jsonrpc2.NewHeaderStream.
	conn := &readWriteCloser{r: r, w: w}
	stream := jsonrpc2.NewHeaderStream(conn)
	defer stream.Close()

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
		// Read one JSON-RPC message from the framed stream.
		msg, _, err := stream.Read(ctx)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			logger.Error("malformed JSON-RPC message; skipping", "err", err)
			continue
		}

		// Route notifications (no id) before handling Calls (requests with id).
		if notif, ok := msg.(*jsonrpc2.Notification); ok {
			switch notif.Method() {
			case "initialized":
				// Transition to stateInitialized only from statePreInit.
				// Receiving "initialized" after shutdown is a client misbehaviour;
				// silently ignore it rather than crashing.
				if state == statePreInit {
					state = stateInitialized
				}
			case "exit":
				if state != stateShutdown {
					return fmt.Errorf("exit without shutdown")
				}
				return nil
			default:
				// Unknown notifications are silently ignored (LSP §3.4).
			}
			continue
		}

		// All other messages must be Calls (requests that require a response).
		call, ok := msg.(*jsonrpc2.Call)
		if !ok {
			// Neither a Notification nor a Call — malformed; skip.
			logger.Error("unexpected JSON-RPC message type; skipping", "type", fmt.Sprintf("%T", msg))
			continue
		}

		method := call.Method()

		// Gate: any request other than "initialize" before initialization is an error.
		if state == statePreInit && method != "initialize" {
			sendError(call.ID(), jsonrpc2.ServerNotInitialized, "server not initialized")
			continue
		}

		var result []byte

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
				result, err = handleInitialize(params, version)
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
				result = []byte(`null`)

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

			default:
				// Unknown method — send MethodNotFound per JSON-RPC 2.0 §5.1 and LSP §3.1.
				// MethodNotFound is the spec-correct response and prevents silently swallowing
				// methods that a client expects to be handled.
				sendError(call.ID(), jsonrpc2.MethodNotFound, fmt.Sprintf("method not found: %s", method))
			}
		}()

		// Build and send the success response (unless panic or error handler already sent a response).
		// If panic was recovered, the handler above already sent InternalError via sendError.
		if result != nil {
			response := jsonrpc2.NewResponse(call.ID(), jsonrpc2.RawMessage(result), nil)
			_, err := stream.Write(ctx, response)
			if err != nil {
				return fmt.Errorf("write response: %w", err)
			}
		}
	}
}
