package document

import (
	"log/slog"
	"testing"

	"go.lsp.dev/uri"
	"natural-lsp/internal/model"
)

// TestStoreGet tests the basic Store construction and URI lookup behavior.
// Implements Task 1: A concurrency-safe in-memory store keyed by LSP document URI.
// FR-33 (track open documents).
func TestStoreGet(t *testing.T) {
	testURI := uri.File("/workspace/test.nsp")
	testContent := []byte("PROGRAM TEST. END.")
	testVersion := 1

	tests := []struct {
		name    string
		setup   func(*Store)
		uri     uri.URI
		wantOK  bool
		wantDoc *Document
	}{
		{
			name:    "missing_uri_returns_not_present",
			setup:   func(s *Store) {}, // empty store
			uri:     testURI,
			wantOK:  false,
			wantDoc: nil,
		},
		{
			name: "stored_document_found_by_uri",
			setup: func(s *Store) {
				// Store a document directly in the map (simulating what a future Store.Open will do)
				s.docs[testURI] = &Document{
					URI:     testURI,
					Version: testVersion,
					Content: testContent,
					// Analysis is zero-initialized
				}
			},
			uri:    testURI,
			wantOK: true,
			wantDoc: &Document{
				URI:     testURI,
				Version: testVersion,
				Content: testContent,
			},
		},
		{
			name: "different_uri_not_found",
			setup: func(s *Store) {
				// Store a document
				s.docs[testURI] = &Document{
					URI:     testURI,
					Version: testVersion,
					Content: testContent,
				}
			},
			uri:     uri.File("/workspace/other.nsp"),
			wantOK:  false,
			wantDoc: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := New("", nil, nil)
			tc.setup(store)

			doc, ok := store.Get(tc.uri)

			if ok != tc.wantOK {
				t.Errorf("Get(uri=%v) returned ok=%v, want %v", tc.uri, ok, tc.wantOK)
			}
			if ok {
				if doc == nil {
					t.Errorf("Get returned nil document with ok=true")
				}
				if doc.URI != tc.uri {
					t.Errorf("Get returned doc.URI=%v, want %v", doc.URI, tc.uri)
				}
				if string(doc.Content) != string(tc.wantDoc.Content) {
					t.Errorf("Get returned doc.Content=%q, want %q", string(doc.Content), string(tc.wantDoc.Content))
				}
				if doc.Version != tc.wantDoc.Version {
					t.Errorf("Get returned doc.Version=%d, want %d", doc.Version, tc.wantDoc.Version)
				}
			} else {
				if doc != nil {
					t.Errorf("Get returned non-nil document with ok=false")
				}
			}
		})
	}
}

// TestStoreDocumentFields tests that a Document carries the required fields.
// Implements Task 1: A stored document carries at least URI, version, content, and FileAnalysis.
func TestStoreDocumentFields(t *testing.T) {
	testURI := uri.File("/workspace/test.nsp")
	testContent := []byte("PROGRAM TEST. END.")
	testVersion := 3

	// Verify Document struct has the expected fields by constructing one directly.
	doc := &Document{
		URI:     testURI,
		Version: testVersion,
		Content: testContent,
		// Analysis field is zero-initialized
	}

	if doc.URI != testURI {
		t.Errorf("Document.URI=%v, want %v", doc.URI, testURI)
	}
	if doc.Version != testVersion {
		t.Errorf("Document.Version=%v, want %v", doc.Version, testVersion)
	}
	if string(doc.Content) != string(testContent) {
		t.Errorf("Document.Content=%q, want %q", string(doc.Content), string(testContent))
	}
	// Analysis should be zero-initialized (empty ObjectType, nil Diagnostics)
	if doc.Analysis.ObjectType != "" {
		t.Errorf("Document.Analysis.ObjectType=%v, want empty string", doc.Analysis.ObjectType)
	}
	if doc.Analysis.Diagnostics != nil {
		t.Errorf("Document.Analysis.Diagnostics=%v, want nil", doc.Analysis.Diagnostics)
	}
}

// TestStoreOpenUpdateClose tests the document lifecycle transitions:
// open → get, open → update → get, open → close → get, and idempotent close.
// Implements Task 2: Open stores content + version as source of truth; Update
// replaces content and bumps version; Close removes the override.
// FR-33: track open documents through their lifecycle transitions.
func TestStoreOpenUpdateClose(t *testing.T) {
	testURI := uri.File("/workspace/test.nsp")
	initialVersion := 1
	initialContent := []byte("PROGRAM TEST. END.")
	updatedVersion := 2
	updatedContent := []byte("PROGRAM TEST. DISPLAY 'UPDATED'. END.")

	tests := []struct {
		name string
		// setup performs Store operations before the assertion
		setup func(*Store)
		// assertion checks the expected state after setup
		assertion func(*testing.T, *Store)
	}{
		{
			name: "open_makes_content_available",
			setup: func(s *Store) {
				s.Open(testURI, initialVersion, initialContent)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if !ok {
					t.Errorf("Get after Open: ok=false, want true")
					return
				}
				if doc == nil {
					t.Errorf("Get after Open: returned nil, want *Document")
					return
				}
				if doc.Version != initialVersion {
					t.Errorf("Get after Open: Version=%d, want %d", doc.Version, initialVersion)
				}
				if string(doc.Content) != string(initialContent) {
					t.Errorf("Get after Open: Content=%q, want %q", string(doc.Content), string(initialContent))
				}
			},
		},
		{
			name: "update_replaces_content_and_version",
			setup: func(s *Store) {
				s.Open(testURI, initialVersion, initialContent)
				s.Update(testURI, updatedVersion, updatedContent)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if !ok {
					t.Errorf("Get after Update: ok=false, want true")
					return
				}
				if doc.Version != updatedVersion {
					t.Errorf("Get after Update: Version=%d, want %d", doc.Version, updatedVersion)
				}
				if string(doc.Content) != string(updatedContent) {
					t.Errorf("Get after Update: Content=%q, want %q", string(doc.Content), string(updatedContent))
				}
			},
		},
		{
			name: "close_removes_document_from_store",
			setup: func(s *Store) {
				s.Open(testURI, initialVersion, initialContent)
				s.Close(testURI)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if ok {
					t.Errorf("Get after Close: ok=true, want false")
				}
				if doc != nil {
					t.Errorf("Get after Close: returned non-nil %v, want nil", doc)
				}
			},
		},
		{
			name: "duplicate_close_is_idempotent",
			setup: func(s *Store) {
				s.Open(testURI, initialVersion, initialContent)
				s.Close(testURI)
				s.Close(testURI) // second close should not panic
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				// After idempotent close, document should still not be in store
				doc, ok := s.Get(testURI)
				if ok {
					t.Errorf("Get after duplicate Close: ok=true, want false")
				}
				if doc != nil {
					t.Errorf("Get after duplicate Close: returned non-nil %v, want nil", doc)
				}
			},
		},
		{
			name: "update_unopened_uri_is_noop",
			setup: func(s *Store) {
				// Update without Open is a no-op per LSP contract
				s.Update(testURI, initialVersion, initialContent)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if ok {
					t.Errorf("Get after Update on unopened URI: ok=true, want false (no-op)")
				}
				if doc != nil {
					t.Errorf("Get after Update on unopened URI: returned non-nil, want nil")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := New("", nil, nil)
			tc.setup(store)
			tc.assertion(t, store)
		})
	}
}

// TestStoreAnalysis tests that Open and Update trigger analysis and cache the result.
// Implements Task 4: Store re-analyzes on open/update and exposes latest FileAnalysis.
// FR-43: a panic in the analyze function must not propagate out of the store.
func TestStoreAnalysis(t *testing.T) {
	testURI := uri.File("/workspace/test.nsp")
	testContent := []byte("PROGRAM TEST. END.")

	tests := []struct {
		name string
		// setup configures the store and its dependencies; returns the final store
		setup func(*testing.T) *Store
		// action performs operations on the store
		action func(*testing.T, *Store)
		// assertion checks the expected state after action
		assertion func(*testing.T, *Store)
	}{
		{
			name: "open_with_recognized_extension_analyzes_and_caches",
			setup: func(t *testing.T) *Store {
				t.Helper()
				logger := slog.New(slog.NewTextHandler(nil, nil))
				// Test-double analyze func that returns ObjectProgram for .NSP files
				analyzeFn := func(relPath string, content []byte) model.FileAnalysis {
					return model.FileAnalysis{
						ObjectType: model.ObjectProgram,
					}
				}
				return New("/workspace", analyzeFn, logger)
			},
			action: func(t *testing.T, s *Store) {
				t.Helper()
				s.Open(testURI, 1, testContent)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if !ok {
					t.Errorf("Get after Open: ok=false, want true")
					return
				}
				if doc == nil {
					t.Errorf("Get after Open: returned nil, want *Document")
					return
				}
				if doc.Analysis.ObjectType != model.ObjectProgram {
					t.Errorf("Get after Open: Analysis.ObjectType=%v, want %v", doc.Analysis.ObjectType, model.ObjectProgram)
				}
			},
		},
		{
			name: "update_with_new_content_re_analyzes_and_updates_cache",
			setup: func(t *testing.T) *Store {
				t.Helper()
				logger := slog.New(slog.NewTextHandler(nil, nil))
				// Test-double analyze func that returns ObjectSubprogram for updated content
				callCount := 0
				analyzeFn := func(relPath string, content []byte) model.FileAnalysis {
					callCount++
					if callCount == 1 {
						// First call (during Open) returns ObjectProgram
						return model.FileAnalysis{
							ObjectType: model.ObjectProgram,
						}
					}
					// Second call (during Update) returns ObjectSubprogram
					return model.FileAnalysis{
						ObjectType: model.ObjectSubprogram,
					}
				}
				return New("/workspace", analyzeFn, logger)
			},
			action: func(t *testing.T, s *Store) {
				t.Helper()
				s.Open(testURI, 1, testContent)
				updatedContent := []byte("SUBROUTINE TEST. END SUBROUTINE.")
				s.Update(testURI, 2, updatedContent)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if !ok {
					t.Errorf("Get after Update: ok=false, want true")
					return
				}
				if doc == nil {
					t.Errorf("Get after Update: returned nil, want *Document")
					return
				}
				if doc.Analysis.ObjectType != model.ObjectSubprogram {
					t.Errorf("Get after Update: Analysis.ObjectType=%v, want %v", doc.Analysis.ObjectType, model.ObjectSubprogram)
				}
			},
		},
		{
			name: "analyze_panic_does_not_propagate_content_still_stored",
			setup: func(t *testing.T) *Store {
				t.Helper()
				logger := slog.New(slog.NewTextHandler(nil, nil))
				// Test-double analyze func that panics
				analyzeFn := func(relPath string, content []byte) model.FileAnalysis {
					panic("simulated analyze panic")
				}
				return New("/workspace", analyzeFn, logger)
			},
			action: func(t *testing.T, s *Store) {
				t.Helper()
				// This should not panic even though analyze panics
				s.Open(testURI, 1, testContent)
			},
			assertion: func(t *testing.T, s *Store) {
				t.Helper()
				doc, ok := s.Get(testURI)
				if !ok {
					t.Errorf("Get after panic in Open: ok=false, want true (content should be stored)")
					return
				}
				if doc == nil {
					t.Errorf("Get after panic in Open: returned nil, want *Document")
					return
				}
				// Verify content was stored
				if string(doc.Content) != string(testContent) {
					t.Errorf("Get after panic in Open: Content=%q, want %q", string(doc.Content), string(testContent))
				}
				// Verify Analysis is zero (recovery case)
				if doc.Analysis.ObjectType != "" {
					t.Errorf("Get after panic in Open: Analysis.ObjectType=%v, want empty string (zero value)", doc.Analysis.ObjectType)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.setup(t)
			tc.action(t, store)
			tc.assertion(t, store)
		})
	}
}
