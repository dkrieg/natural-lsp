// Package server: collects diagnostics from two producers — unrecognized
// syntax flagged during extraction (PRD FR-30) and ambiguous resolution from
// workspace/resolution.go (PRD FR-31) — and publishes them to the client.
package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-json-experiment/json/jsontext"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/model"
)

// severityToProtocol maps a model.DiagnosticSeverity to a protocol.DiagnosticSeverity.
// Unknown or unrecognized severities default to the least-alarming safe default
// (DiagnosticSeverityInformation) to avoid masking real issues.
func severityToProtocol(s model.DiagnosticSeverity) protocol.DiagnosticSeverity {
	switch s {
	case model.DiagnosticError:
		return protocol.DiagnosticSeverityError
	case model.DiagnosticWarning:
		return protocol.DiagnosticSeverityWarning
	case model.DiagnosticInfo:
		return protocol.DiagnosticSeverityInformation
	default:
		return protocol.DiagnosticSeverityInformation
	}
}

const diagnosticSource = "natural-lsp"

// toProtocolDiagnostic converts a model.Diagnostic to a protocol.Diagnostic with
// encoding-aware range conversion, message passthrough, severity mapping, and
// source tag. The Code field is converted from model.DiagnosticCode to protocol.ProgressToken.
//
// This is a pure function that never panics: out-of-range or degenerate ranges are
// clamped by toProtocolRange (FR-43).
func toProtocolDiagnostic(d model.Diagnostic, content string, enc protocol.PositionEncodingKind) protocol.Diagnostic {
	var code protocol.ProgressToken
	if d.Code != "" {
		code = protocol.String(string(d.Code))
	}

	return protocol.Diagnostic{
		Range:    toProtocolRange(d.Range, content, enc),
		Severity: severityToProtocol(d.Severity),
		Code:     code,
		Message:  protocol.String(d.Message),
		Source:   protocol.NewOptional(diagnosticSource),
	}
}

// aggregateDiagnostics merges diagnostics from two producer channels:
// - fa.Diagnostics: parser/syntax diagnostics from the analyzer (feature 00/01)
// - resDiags: flat-namespace ambiguity diagnostics from resolution (feature 07)
//
// It concatenates both slices, sorts by Range.Start (line then column),
// deduplicates exact matches on (Message, Severity, Code, Range), and returns
// nil for empty results (not an empty slice).
func aggregateDiagnostics(fa model.FileAnalysis, resDiags []model.Diagnostic) []model.Diagnostic {
	// Concatenate both slices into a fresh backing array. A plain
	// append(fa.Diagnostics, resDiags...) may return fa.Diagnostics's own
	// backing array (when it has spare capacity), so the sort.SliceStable below
	// would reorder the slice owned by the store/index FileAnalysis in place —
	// corrupting shared state. Allocate our own array so the sort touches only
	// this function's copy.
	combined := make([]model.Diagnostic, 0, len(fa.Diagnostics)+len(resDiags))
	combined = append(combined, fa.Diagnostics...)
	combined = append(combined, resDiags...)

	// Return nil for empty results
	if len(combined) == 0 {
		return nil
	}

	// Sort by Range.Start (line first, then column)
	sort.SliceStable(combined, func(i, j int) bool {
		if combined[i].Range.Start.Line != combined[j].Range.Start.Line {
			return combined[i].Range.Start.Line < combined[j].Range.Start.Line
		}
		return combined[i].Range.Start.Column < combined[j].Range.Start.Column
	})

	// Dedup exact matches on (Message, Severity, Code, Range)
	seen := make(map[diagnosticKey]struct{})
	result := make([]model.Diagnostic, 0, len(combined))
	for _, diag := range combined {
		key := diagnosticKey{
			Message:  diag.Message,
			Severity: diag.Severity,
			Code:     diag.Code,
			Range:    diag.Range,
		}
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, diag)
		}
	}

	return result
}

// diagnosticKey is the deduplication key for exact diagnostic matches.
type diagnosticKey struct {
	Message  string
	Severity model.DiagnosticSeverity
	Code     model.DiagnosticCode
	Range    model.Range
}

// publishDiagnostics writes a textDocument/publishDiagnostics notification to the jsonrpc2 stream.
//
// The notification carries a PublishDiagnosticsParams with:
//   - URI: the document URI
//   - Diagnostics: the protocol diagnostic array (will be empty [] to clear, never null)
//   - Version: optional; threaded when provided (for open-document buffers)
//
// The function marshals the params via MarshalJSONTo, wraps them in a Notification with
// method "textDocument/publishDiagnostics", and writes the notification via stream.Write
// with Content-Length framing (jsonrpc2 package handles framing).
//
// On write failure, the caller is responsible for logging (FR-43); this function only
// returns the error.
//
// T5 signature decision: Accept an optional version (*protocol.Optional[int32]). If nil,
// Version is omitted from PublishDiagnosticsParams; if present, it is threaded. This matches
// the LSP convention for open-document buffers (have version) vs on-disk/watched files (no version).
func publishDiagnostics(ctx context.Context, stream jsonrpc2.Stream, uriStr string, diags []protocol.Diagnostic, version *protocol.Optional[int32]) error {
	// Convert uriStr to uri.URI.
	docURI := uri.URI(uriStr)

	// Ensure Diagnostics is never nil when marshaled — empty slices must become []
	// in JSON, not null. If diags is nil, set it to an empty non-nil slice.
	if diags == nil {
		diags = []protocol.Diagnostic{}
	}

	// Build PublishDiagnosticsParams with the required fields.
	params := protocol.PublishDiagnosticsParams{
		URI:         docURI,
		Diagnostics: diags,
	}

	// Thread the version if provided.
	if version != nil {
		params.Version = *version
	}

	// Marshal params to JSON via the jsontext encoder.
	var paramsBuf bytes.Buffer
	paramsEnc := jsontext.NewEncoder(&paramsBuf)
	if err := params.MarshalJSONTo(paramsEnc); err != nil {
		return err
	}

	// Create and write the Notification.
	notif := jsonrpc2.NewNotification("textDocument/publishDiagnostics", jsonrpc2.RawMessage(paramsBuf.Bytes()))
	_, err := stream.Write(ctx, notif)
	return err
}

// publishFileDiagnostics is the T6 orchestrator: combines aggregation, conversion, and
// publishing for a single file URI with F7 lock discipline (snapshot under RLock, release before I/O).
//
// It follows the store-first pattern established in provideDocumentSymbols/provideHover:
//  1. Snapshot idx/res/posEncoding/root ONCE under RLock, release before any I/O (F7).
//  2. Try store.Get(uri) for an open document's live FileAnalysis + content + version.
//  3. Fall back to idx.Get(relPath) + os.ReadFile for on-disk indexed files (no version).
//  4. Aggregate parser diagnostics + resolution diagnostics, convert with encoding-aware
//     range conversion, and publish via publishDiagnostics.
//
// Missing/unreadable/out-of-root files publish an empty array (clear) and return nil (FR-43).
// Nil res is handled gracefully — DiagnosticsFor is nil-safe (resolution.go).
// A publish write error is logged (Warn) and returned, never fatal (FR-43).
func (hctx *handlerContext) publishFileDiagnostics(ctx context.Context, stream jsonrpc2.Stream, uriStr string) error {
	// Guard: hctx must be initialized.
	if hctx == nil {
		return nil
	}

	// F7 lock discipline: snapshot idx/res/posEncoding/root ONCE under the read lock,
	// then release before any I/O (store.Get, os.ReadFile, stream.Write). Everything
	// after this block is lock-free and reads only the local snapshots — never a race
	// with applyDocumentChange's build-then-publish pointer swap under the write lock.
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	docURI := uri.URI(uriStr)

	// Resolution order 1: open-document store (current, unsaved edits win over disk — Story 3).
	// The store's FileAnalysis reflects live buffer content, so a client seeing a fresh
	// parse error before saving is served from here. Guard a nil store (defensive,
	// consistent with the hctx == nil guard above): skip the store-first branch and
	// fall through to the index/disk path rather than dereferencing a nil store.
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(docURI); ok && doc != nil {
			version := protocol.NewOptional(int32(doc.Version))

			// Derive relPath from the snapshotted root for the resolution lookup.
			relPath, _ := filepath.Rel(root, docURI.FsPath())
			relPath = filepath.ToSlash(relPath)

			resDiags := res.DiagnosticsFor(relPath) // nil-safe (resolution.go)
			agg := aggregateDiagnostics(doc.Analysis, resDiags)

			protoDiags := make([]protocol.Diagnostic, 0, len(agg))
			for _, d := range agg {
				protoDiags = append(protoDiags, toProtocolDiagnostic(d, string(doc.Content), posEncoding))
			}
			return hctx.publish(ctx, stream, uriStr, protoDiags, &version)
		}
	}

	// Resolution order 2: index + on-disk content (document not open).
	absPath, relPath, err := uriToRelPath(root, docURI)
	if err != nil || filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "..") {
		// URI is outside the workspace root — clear any stale diagnostics (FR-43).
		return hctx.publish(ctx, stream, uriStr, nil, nil)
	}

	fa, ok := idx.Get(relPath)
	if !ok {
		// File not indexed (missing/unknown) — clear (FR-43).
		return hctx.publish(ctx, stream, uriStr, nil, nil)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		// Can't read the file — clear (FR-43).
		return hctx.publish(ctx, stream, uriStr, nil, nil)
	}

	resDiags := res.DiagnosticsFor(relPath) // nil-safe (resolution.go)
	agg := aggregateDiagnostics(fa, resDiags)

	protoDiags := make([]protocol.Diagnostic, 0, len(agg))
	for _, d := range agg {
		protoDiags = append(protoDiags, toProtocolDiagnostic(d, string(content), posEncoding))
	}
	// No version for on-disk files (LSP convention).
	return hctx.publish(ctx, stream, uriStr, protoDiags, nil)
}

// publish writes the notification and logs (FR-43) on a write error before returning it,
// so a transport failure never crashes the request loop.
func (hctx *handlerContext) publish(ctx context.Context, stream jsonrpc2.Stream, uriStr string, diags []protocol.Diagnostic, version *protocol.Optional[int32]) error {
	err := publishDiagnostics(ctx, stream, uriStr, diags, version)
	if err != nil && hctx.logger != nil {
		hctx.logger.Warn("failed to publish diagnostics", "uri", uriStr, "error", err)
	}
	return err
}
