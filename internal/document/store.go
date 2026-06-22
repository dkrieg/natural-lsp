// Package document holds the in-memory view of open documents
// (didOpen/didChange/didClose) and keeps analysis consistent as documents are
// edited (PRD FR-33).
package document

import (
	"log/slog"
	"path/filepath"
	"sync"

	"go.lsp.dev/uri"
	"natural-lsp/internal/model"
)

// AnalyzeFunc is the per-file analysis callback with FR-43 degradation guarantees.
// relPath is the workspace-relative path using OS-native path separators (as returned
// by filepath.Rel). content is the current in-memory document content.
// The Store wraps every call in panic recovery (FR-43); the function itself need not
// protect against panics.
type AnalyzeFunc func(relPath string, content []byte) model.FileAnalysis

// Document represents an open document with its current content and analysis.
// It carries the URI, version, unsaved content, and the last FileAnalysis result.
type Document struct {
	URI      uri.URI
	Version  int
	Content  []byte
	Analysis model.FileAnalysis
}

// Store is a concurrency-safe in-memory store of open documents, keyed by LSP URI.
// It is guarded by a sync.RWMutex for safe concurrent access from the request loop
// and future external watcher goroutines.
type Store struct {
	mu      sync.RWMutex
	docs    map[uri.URI]*Document
	root    string
	analyze AnalyzeFunc
	logger  *slog.Logger
}

// New creates and returns an empty Store with the given workspace root, analysis function, and logger.
// The root is used to derive workspace-relative paths from document URIs.
// The analyze function is called to analyze document content when documents are opened or updated.
// If analyze is nil, documents are stored but not analyzed.
func New(root string, analyze AnalyzeFunc, logger *slog.Logger) *Store {
	return &Store{
		docs:    make(map[uri.URI]*Document),
		root:    root,
		analyze: analyze,
		logger:  logger,
	}
}

// Get returns the Document for the given URI, or (nil, false) if the URI is not in the store.
// The returned pointer is valid for the lifetime of this Store entry; callers must not
// mutate the Content slice, as it may be shared with other readers until the next Update.
func (s *Store) Get(u uri.URI) (*Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	doc, ok := s.docs[u]
	return doc, ok
}

// Open stores a new document with the given URI, version, and content, making it
// the source of truth for that file. If the URI already exists, it is overwritten.
// FR-33: on open, the document's current content becomes the source of truth.
// Analysis runs outside the mutex so that a slow or blocking analyze call does not
// stall concurrent readers.
func (s *Store) Open(u uri.URI, version int, content []byte) {
	// Read stable fields without the lock; analyze is immutable after New.
	var analysis model.FileAnalysis
	if s.analyze != nil {
		relPath := deriveRelPath(u, s.root)
		analysis = callAnalyzeWithRecovery(relPath, content, s.analyze, s.logger)
	}

	doc := &Document{
		URI:      u,
		Version:  version,
		Content:  content,
		Analysis: analysis,
	}

	s.mu.Lock()
	s.docs[u] = doc
	s.mu.Unlock()
}

// Update replaces the content and version of an open document. If the URI is not
// open, the update is a no-op. This is safe because the LSP spec requires
// textDocument/didOpen to precede any textDocument/didChange for the same URI,
// so an unknown URI here indicates a client protocol violation that we tolerate
// silently rather than crash.
// FR-33: on change, the in-memory content becomes the source of truth.
// Analysis runs outside the mutex so that a slow or blocking analyze call does not
// stall concurrent readers.
func (s *Store) Update(u uri.URI, version int, content []byte) {
	// Check presence without holding the write lock during analysis.
	s.mu.RLock()
	_, ok := s.docs[u]
	s.mu.RUnlock()

	if !ok {
		return
	}

	var analysis model.FileAnalysis
	if s.analyze != nil {
		relPath := deriveRelPath(u, s.root)
		analysis = callAnalyzeWithRecovery(relPath, content, s.analyze, s.logger)
	}

	s.mu.Lock()
	if doc, ok := s.docs[u]; ok {
		doc.Version = version
		doc.Content = content
		doc.Analysis = analysis
	}
	s.mu.Unlock()
}

// Close removes the document from the store, reverting to on-disk content for
// subsequent operations. If the URI is not open, Close is a no-op.
// FR-33: on close, the server reverts to the on-disk content.
func (s *Store) Close(u uri.URI) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.docs, u)
}

// deriveRelPath derives the workspace-relative path from a URI and workspace root.
// uri.URI.FsPath() converts the file:// URI to an absolute OS path; filepath.Rel
// then computes the relative path using OS-native separators. If root is empty or
// filepath.Rel fails (e.g. different drive on Windows), the absolute path is returned.
func deriveRelPath(u uri.URI, root string) string {
	absPath := u.FsPath()
	if root == "" {
		return absPath
	}
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		return absPath
	}
	return relPath
}

// callAnalyzeWithRecovery calls the analyze function and recovers from any panic,
// returning a zero FileAnalysis on recovery (FR-43). A non-nil logger receives an
// Error entry with the panic value; if the logger itself panics (e.g. broken writer),
// that secondary panic is also suppressed so the store remains stable.
func callAnalyzeWithRecovery(relPath string, content []byte, analyze AnalyzeFunc, logger *slog.Logger) (analysis model.FileAnalysis) {
	defer func() {
		if r := recover(); r != nil {
			logPanicSafely(logger, relPath, r)
			// analysis remains zero-initialized
		}
	}()

	return analyze(relPath, content)
}

// logPanicSafely logs an analyzer panic recovery without letting a broken logger
// propagate a secondary panic out of the recovery path.
func logPanicSafely(logger *slog.Logger, relPath string, r any) {
	if logger == nil {
		return
	}
	defer func() { recover() }() //nolint:errcheck // secondary panic from broken logger suppressed
	logger.Error("analyzer panic recovered", "path", relPath, "panic", r)
}
