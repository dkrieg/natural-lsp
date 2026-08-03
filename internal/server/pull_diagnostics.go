package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// emptyFullReport builds the empty full diagnostic report used for every
// graceful-degradation exit (missing/unreadable/out-of-root/uninitialized —
// FR-43). Items is always a non-nil, empty slice, never null.
func emptyFullReport() *protocol.RelatedFullDocumentDiagnosticReport {
	return fullReport(nil)
}

// fullReport builds a full diagnostic report ("full", no ResultID, per OQ-2)
// wrapping the given items. Items is always normalized to a non-nil slice —
// nil input becomes an empty slice, so the wire shape is always "items":[]
// rather than "items":null.
func fullReport(items []protocol.Diagnostic) *protocol.RelatedFullDocumentDiagnosticReport {
	if items == nil {
		items = []protocol.Diagnostic{}
	}
	return &protocol.RelatedFullDocumentDiagnosticReport{
		FullDocumentDiagnosticReport: protocol.FullDocumentDiagnosticReport{
			Kind:  string(protocol.DocumentDiagnosticReportKindFull),
			Items: items,
		},
	}
}

// provideDocumentDiagnostic implements textDocument/diagnostic (pull model, LSP 3.17+).
// It returns diagnostics for a single document URI as a full diagnostic report
// (Kind="full", never delta), with no ResultID (OQ-2).
//
// Implementation mirrors publishFileDiagnostics for consistency:
//   - F7 lock discipline: snapshot idx/res/posEncoding/root under RLock, release before I/O.
//   - Store-first: open buffer's FileAnalysis (live edits) wins over disk.
//   - Graceful degradation: missing/unreadable/out-of-root → empty full report, never error (FR-43).
//   - Content is byte-identical to the push path (aggregateDiagnostics + toProtocolDiagnostic).
//
// Always returns a non-nil report with Items as a non-nil slice (empty if no diagnostics).
func provideDocumentDiagnostic(hctx *handlerContext, params protocol.DocumentDiagnosticParams) (*protocol.RelatedFullDocumentDiagnosticReport, error) {
	// Guard: hctx must be initialized.
	if hctx == nil {
		return emptyFullReport(), nil
	}

	// F7 lock discipline: snapshot idx/res/posEncoding/root ONCE under the read lock,
	// then release before any I/O. Everything after this block is lock-free.
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	docURI := params.TextDocument.URI

	// Resolution order 1: open-document store (current, unsaved edits win over disk).
	// Mirrors publishFileDiagnostics: the store is consulted unconditionally, before
	// the out-of-root check below, so an open buffer's diagnostics are never dropped
	// even if its URI would otherwise be classified out-of-root.
	if hctx.store != nil {
		if doc, ok := hctx.store.Get(docURI); ok && doc != nil {
			// Derive relPath from the snapshotted root for the resolution lookup.
			relPath, _ := filepath.Rel(root, docURI.FsPath())
			relPath = paths.NormalizeKey(relPath)

			resDiags := res.DiagnosticsFor(relPath) // nil-safe
			agg := aggregateDiagnostics(doc.Analysis, resDiags)

			return fullReport(convertDiagnostics(agg, string(doc.Content), posEncoding)), nil
		}
	}

	// Resolution order 2: index + on-disk content (document not open).
	absPath, relPath, err := uriToRelPath(root, docURI)
	if err != nil || filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "..") {
		// URI is outside the workspace root — return empty full report (FR-43).
		return emptyFullReport(), nil
	}

	// Guard: index must be initialized. If the index hasn't been built yet
	// (e.g., during server startup or before "initialized" completes),
	// return an empty full report (FR-43).
	if idx == nil {
		return emptyFullReport(), nil
	}

	fa, ok := idx.Get(relPath)
	if !ok {
		// File not indexed (missing/unknown) — return empty full report (FR-43).
		return emptyFullReport(), nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		// Can't read the file — return empty full report (FR-43).
		return emptyFullReport(), nil
	}

	resDiags := res.DiagnosticsFor(relPath) // nil-safe
	agg := aggregateDiagnostics(fa, resDiags)

	return fullReport(convertDiagnostics(agg, string(content), posEncoding)), nil
}

// provideWorkspaceDiagnostic implements workspace/diagnostic (pull model, LSP 3.17+).
// It returns diagnostics for all indexed files as a WorkspaceDiagnosticReport,
// with a list of WorkspaceFullDocumentDiagnosticReport items (one per file with ≥1 diagnostic).
//
// Implementation uses a disk-free sweep (ADR-025):
//   - F7 lock discipline: snapshot idx/res/posEncoding/root under RLock, release before iteration.
//   - Iterate idx.ForEachWithRange to walk all indexed files using in-memory line-width tables.
//   - Skip files with 0 diagnostics (clean files omitted from report).
//   - For each file with diagnostics, build a WorkspaceFullDocumentDiagnosticReport with
//     the file URI and full diagnostic list (kind="full", no ResultID per OQ-2).
//   - Content is byte-identical to the per-document path (aggregateDiagnostics +
//     toProtocolDiagnosticFromConverter via the RangeConverter).
//
// Always returns a non-nil report with Items as a non-nil slice (empty if no files have diagnostics).
// Never returns an error (FR-43).
func provideWorkspaceDiagnostic(hctx *handlerContext, params protocol.WorkspaceDiagnosticParams) (*protocol.WorkspaceDiagnosticReport, error) {
	// Guard: hctx must be initialized.
	if hctx == nil {
		return &protocol.WorkspaceDiagnosticReport{Items: []protocol.WorkspaceDocumentDiagnosticReport{}}, nil
	}

	// F7 lock discipline: snapshot idx/res/posEncoding/root ONCE under the read lock,
	// then release before any iteration/I/O. Everything after this block is lock-free.
	hctx.idxResMu.RLock()
	idx := hctx.idx
	res := hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	// Guard: index must be initialized. If the index hasn't been built yet,
	// return an empty report (FR-43).
	if idx == nil {
		return &protocol.WorkspaceDiagnosticReport{Items: []protocol.WorkspaceDocumentDiagnosticReport{}}, nil
	}

	// Disk-free sweep over all indexed files using ForEachWithRange (ADR-025).
	// Items is the LSP union type []WorkspaceDocumentDiagnosticReport; each element
	// is a *WorkspaceFullDocumentDiagnosticReport (which satisfies the interface).
	items := make([]protocol.WorkspaceDocumentDiagnosticReport, 0)

	idx.ForEachWithRange(func(relPath string, fa model.FileAnalysis, toRange workspace.RangeConverter) {
		// Aggregate diagnostics from both sources (parser + resolution)
		resDiags := res.DiagnosticsFor(relPath) // nil-safe
		agg := aggregateDiagnostics(fa, resDiags)

		// Skip clean files (no diagnostics)
		if len(agg) == 0 {
			return
		}

		// Convert model diagnostics to protocol diagnostics using the disk-free converter
		protoDiags := make([]protocol.Diagnostic, 0, len(agg))
		for _, d := range agg {
			protoDiags = append(protoDiags, toProtocolDiagnosticFromConverter(d, toRange, posEncoding))
		}

		// Build the file URI from root + relPath
		absPath := filepath.Join(root, relPath)
		fileURI := uri.File(absPath)

		// Create and append a WorkspaceFullDocumentDiagnosticReport for this file
		report := &protocol.WorkspaceFullDocumentDiagnosticReport{
			FullDocumentDiagnosticReport: protocol.FullDocumentDiagnosticReport{
				Kind:  string(protocol.DocumentDiagnosticReportKindFull),
				Items: protoDiags,
			},
			URI: fileURI,
			// Version is left nil for on-disk files (LSP convention, OQ-2)
		}
		items = append(items, report)
	})

	// Sort items by URI ascending for deterministic output. ForEachWithRange order is
	// arbitrary (Go map traversal), so we stabilize it here to match the posture of the
	// push path and aggregateDiagnostics' within-file sort.
	sort.SliceStable(items, func(i, j int) bool {
		var iURI, jURI string
		if item, ok := items[i].(*protocol.WorkspaceFullDocumentDiagnosticReport); ok {
			iURI = string(item.URI)
		}
		if item, ok := items[j].(*protocol.WorkspaceFullDocumentDiagnosticReport); ok {
			jURI = string(item.URI)
		}
		return iURI < jURI
	})

	// Return the report with Items as a non-nil slice (possibly empty)
	return &protocol.WorkspaceDiagnosticReport{Items: items}, nil
}
