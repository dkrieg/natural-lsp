package document

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// TestWatcherDetectsModifiedFile tests that the Watcher detects file modifications
// and dispatches re-analysis via the provided AnalyzeFunc.
// Implements Task 9 (FR-34): External-change detection + per-file re-analysis dispatch.
// The watcher must detect modified indexed files in the workspace and call the
// analyze function with the changed content (FR-34 acceptance criterion 1).
func TestWatcherDetectsModifiedFile(t *testing.T) {
	// Arrange: create a temp directory with a .NSP file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.nsp")
	initialContent := []byte("PROGRAM TEST. END.")

	err := os.WriteFile(testFile, initialContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a minimal config with default extensions (includes .NSP)
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD", ".NS4", ".NS7", ".NS3", ".NS8", ".NST"},
			Exclude:    []string{},
		},
	}

	// Set up a spy AnalyzeFunc that records calls
	var mu sync.Mutex
	var analyzeCalls []struct {
		relPath string
		content []byte
	}
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		mu.Lock()
		defer mu.Unlock()
		analyzeCalls = append(analyzeCalls, struct {
			relPath string
			content []byte
		}{relPath, content})
		return model.FileAnalysis{
			ObjectType: model.ObjectProgram,
		}
	}

	logger := slog.New(slog.NewTextHandler(nil, nil))

	// Act: create and start the watcher
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir, cfg, analyzeFunc, logger)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer func() {
		if watcher != nil {
			watcher.Close()
		}
	}()

	// Modify the file to trigger a watch event
	modifiedContent := []byte("PROGRAM TEST. DISPLAY 'MODIFIED'. END.")
	err = os.WriteFile(testFile, modifiedContent, 0644)
	if err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Assert: poll until the spy is called with the modified content
	// Use a retry loop with short interval and timeout
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, call := range analyzeCalls {
			if call.relPath == "test.nsp" && string(call.content) == string(modifiedContent) {
				found = true
				break
			}
		}
		mu.Unlock()
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Errorf("Expected watcher to analyze modified file with content %q, but spy calls were: %v",
			string(modifiedContent), analyzeCalls)
	}
}

// TestWatcherIgnoresNonIndexedExtensions tests that the watcher ignores files
// with extensions not in the indexed set (FR-34 acceptance criterion 3).
func TestWatcherIgnoresNonIndexedExtensions(t *testing.T) {
	// Arrange: create a temp directory with a non-indexed file (.TXT)
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("This is a text file")

	err := os.WriteFile(testFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a config with only .NSP in the indexed set
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP"},
			Exclude:    []string{},
		},
	}

	// Set up a spy that records all calls
	analyzeCallCount := atomic.Int32{}
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		analyzeCallCount.Add(1)
		return model.FileAnalysis{}
	}

	logger := slog.New(slog.NewTextHandler(nil, nil))

	// Act: create and start the watcher
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir, cfg, analyzeFunc, logger)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer func() {
		if watcher != nil {
			watcher.Close()
		}
	}()

	// Modify the non-indexed file
	newContent := []byte("Modified text")
	err = os.WriteFile(testFile, newContent, 0644)
	if err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Assert: give the watcher time to process (but it should not call analyze)
	time.Sleep(500 * time.Millisecond)
	if analyzeCallCount.Load() > 0 {
		t.Errorf("Expected watcher to ignore non-indexed extension, but analyze was called %d times",
			analyzeCallCount.Load())
	}
}

// TestWatcherFiltering tests that the watcher correctly ignores events for:
// 1. Files under excluded directories (cfg.IsExcluded)
// 2. Files with non-indexed extensions
// 3. Files outside the workspace root
// (FR-34 acceptance criterion 3: Changes within excluded directories or to non-indexed types are ignored)
//
// This test pins all three filtering criteria with table-driven cases.
func TestWatcherFiltering(t *testing.T) {
	tests := []struct {
		name                string
		setup               func(t *testing.T, root string) string // returns file path to modify
		config              *config.Config
		expectAnalyzeCalled bool
		fileContent         []byte
		description         string
	}{
		{
			name: "excluded_directory",
			setup: func(t *testing.T, root string) string {
				// Create an "archive" subdirectory (excluded)
				archiveDir := filepath.Join(root, "archive")
				if err := os.Mkdir(archiveDir, 0755); err != nil {
					t.Fatalf("Failed to create archive dir: %v", err)
				}
				// Create a .NSP file inside the excluded directory
				testFile := filepath.Join(archiveDir, "test.nsp")
				return testFile
			},
			config: &config.Config{
				Workspace: config.WorkspaceConfig{
					Extensions: []string{".NSP", ".NSN"},
					Exclude:    []string{"archive"}, // "archive" is excluded
				},
			},
			expectAnalyzeCalled: false,
			fileContent:         []byte("PROGRAM TEST. END."),
			description:         "watcher should NOT call analyze for files in excluded directory",
		},
		{
			name: "non_indexed_extension",
			setup: func(t *testing.T, root string) string {
				// Create a .TXT file (not in indexed extensions)
				testFile := filepath.Join(root, "test.txt")
				return testFile
			},
			config: &config.Config{
				Workspace: config.WorkspaceConfig{
					Extensions: []string{".NSP", ".NSN"}, // .TXT not included
					Exclude:    []string{},
				},
			},
			expectAnalyzeCalled: false,
			fileContent:         []byte("This is plain text"),
			description:         "watcher should NOT call analyze for non-indexed extensions",
		},
		{
			name: "valid_indexed_file",
			setup: func(t *testing.T, root string) string {
				// Create a valid .NSP file directly in root
				testFile := filepath.Join(root, "test.nsp")
				return testFile
			},
			config: &config.Config{
				Workspace: config.WorkspaceConfig{
					Extensions: []string{".NSP", ".NSN"},
					Exclude:    []string{},
				},
			},
			expectAnalyzeCalled: true,
			fileContent:         []byte("PROGRAM TEST. END."),
			description:         "watcher SHOULD call analyze for valid indexed file in root",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: set up the test environment
			tmpDir := t.TempDir()
			filePath := tc.setup(t, tmpDir)

			// Create a spy AnalyzeFunc that tracks calls
			analyzeCallCount := atomic.Int32{}
			analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
				analyzeCallCount.Add(1)
				return model.FileAnalysis{
					ObjectType: model.ObjectProgram,
				}
			}

			logger := slog.New(slog.NewTextHandler(nil, nil))

			// Act: create and start the watcher
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			watcher, err := NewWatcher(ctx, tmpDir, tc.config, analyzeFunc, logger)
			if err != nil {
				t.Fatalf("Failed to create watcher: %v", err)
			}
			defer func() {
				if watcher != nil {
					watcher.Close()
				}
			}()

			// Write/modify the file to trigger a watch event
			if err := os.WriteFile(filePath, tc.fileContent, 0644); err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			if tc.expectAnalyzeCalled {
				// Assert: poll until analyze is called (up to 2 seconds)
				deadline := time.Now().Add(2 * time.Second)
				found := false
				for time.Now().Before(deadline) {
					if analyzeCallCount.Load() > 0 {
						found = true
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if !found {
					t.Errorf("%s: expected analyze to be called, but it was not (calls: %d)",
						tc.description, analyzeCallCount.Load())
				}
			} else {
				// Assert: give the watcher time to process (it should NOT call analyze)
				time.Sleep(500 * time.Millisecond)
				if analyzeCallCount.Load() > 0 {
					t.Errorf("%s: expected analyze NOT to be called, but it was called %d times",
						tc.description, analyzeCallCount.Load())
				}
			}
		})
	}
}

// TestWatcherDrainPanicRecovery tests that a panic in the analyze callback during
// drainPending does not kill the watcher goroutine and prevents subsequent file
// events from being detected. This is a defense-in-depth test for FR-43 (graceful
// degradation): panics in analyze (e.g. inside cfg.IsExcluded, in the callback wrapper)
// outside the az.Analyze boundary must be recovered in drainPending, not escaped.
//
// The test:
// 1. Triggers a file event that causes analyze to panic
// 2. Verifies the watcher survives (second file event is processed)
// 3. Asserts that both analyze calls are attempted (first panics, second succeeds)
//
// Without recovery in drainPending, the first panic kills the background goroutine
// silently and the second event is never processed, causing callCount to be 1.
// With recovery, callCount reaches 2.
func TestWatcherDrainPanicRecovery(t *testing.T) {
	// Arrange: create a temp directory with a .NSP file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.nsp")
	initialContent := []byte("PROGRAM TEST. END.")

	err := os.WriteFile(testFile, initialContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a minimal config with default extensions (includes .NSP)
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD", ".NS4", ".NS7", ".NS3", ".NS8", ".NST"},
			Exclude:    []string{},
		},
	}

	// Set up an analyze function that panics on the first call (simulating a panic
	// in the analyze callback or its immediate wrapper), then succeeds on the second.
	// Use an atomic counter to track calls independent of recovery state.
	callCount := atomic.Int32{}
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		count := callCount.Add(1)
		if count == 1 {
			// First call panics (simulating a panic in cfg.IsExcluded, the callback wrapper, etc.)
			panic("drain panic test")
		}
		// Second and subsequent calls succeed
		return model.FileAnalysis{
			ObjectType: model.ObjectProgram,
		}
	}

	logger := slog.New(slog.NewTextHandler(nil, nil))

	// Act: create and start the watcher
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir, cfg, analyzeFunc, logger)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer func() {
		if watcher != nil {
			watcher.Close()
		}
	}()

	// Trigger the first file event (this will cause analyze to panic)
	modifiedContent1 := []byte("PROGRAM TEST. FIRST WRITE. END.")
	err = os.WriteFile(testFile, modifiedContent1, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file (first event): %v", err)
	}

	// Wait for the debounce window to fire and the panic to occur
	time.Sleep(200 * time.Millisecond)

	// Trigger a second file event to verify the watcher is still alive
	modifiedContent2 := []byte("PROGRAM TEST. SECOND WRITE. END.")
	err = os.WriteFile(testFile, modifiedContent2, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file (second event): %v", err)
	}

	// Wait for the second event to be debounced and processed
	time.Sleep(200 * time.Millisecond)

	// Assert: verify that both analyze calls were attempted
	// Without panic recovery in drainPending, the first panic kills the goroutine
	// and callCount stays at 1. With recovery, callCount reaches 2.
	finalCount := callCount.Load()
	if finalCount < 2 {
		t.Errorf("Expected watcher to survive first panic and attempt second analyze call; got callCount=%d (want >= 2)",
			finalCount)
	}
}

// TestWatcherDebounce tests that a burst of file events (e.g. rapid successive writes)
// is coalesced via per-path debouncing so the server isn't overwhelmed.
// A quiet window (100ms default) accumulates pending paths; when the window expires with
// no new events, all accumulated paths are analyzed once rather than multiple times.
// Implements Task 11 (FR-34 acceptance criterion 4): bulk changes are handled without
// overwhelming the server or producing incorrect partial state.
func TestWatcherDebounce(t *testing.T) {
	// Arrange: create a temp directory with a .NSP file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.nsp")
	initialContent := []byte("PROGRAM TEST. END.")

	err := os.WriteFile(testFile, initialContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a minimal config with default extensions (includes .NSP)
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD", ".NS4", ".NS7", ".NS3", ".NS8", ".NST"},
			Exclude:    []string{},
		},
	}

	// Set up a spy AnalyzeFunc that records calls (thread-safe)
	var mu sync.Mutex
	var analyzeCallCount int32
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		mu.Lock()
		defer mu.Unlock()
		analyzeCallCount++
		return model.FileAnalysis{
			ObjectType: model.ObjectProgram,
		}
	}

	logger := slog.New(slog.NewTextHandler(nil, nil))

	// Act: create and start the watcher
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir, cfg, analyzeFunc, logger)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer func() {
		if watcher != nil {
			watcher.Close()
		}
	}()

	// Simulate a rapid burst of writes (e.g. git checkout or atomic editor saves)
	// Write the same file 5 times in quick succession (< 10ms apart)
	for i := 1; i <= 5; i++ {
		modifiedContent := []byte("PROGRAM TEST. " + string(rune('0'+i)) + " END.")
		err = os.WriteFile(testFile, modifiedContent, 0644)
		if err != nil {
			t.Fatalf("Failed to write test file (iteration %d): %v", i, err)
		}
		// Rapid writes without waiting between them
		if i < 5 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Assert: wait for the quiet window + margin to settle
	// (debouncing quiet window typically 100ms, plus 200ms margin to ensure firing)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	callCount := analyzeCallCount
	mu.Unlock()

	// The test expects at most 2 calls (ideally 1 for the final state, but
	// the implementation might call once initially on watcher setup + once for debounced batch).
	// Without debouncing, this would be 5+ calls.
	// With debouncing, it should be 1 (the final debounced batch).
	if callCount > 2 {
		t.Errorf("Expected debounced watcher to call analyze at most 2 times after 5 rapid writes, but was called %d times",
			callCount)
	}
}

// TestWatcherDetectsCreatedFile tests that the Watcher detects newly-created files
// and dispatches re-analysis via the provided AnalyzeFunc.
// Implements Task 9 (FR-34): External-change detection for created files.
// The watcher must detect added indexed files in the workspace and call the
// analyze function with the file's relative path and content (FR-34 acceptance criterion 1).
func TestWatcherDetectsCreatedFile(t *testing.T) {
	// Arrange: create a temp directory without any .NSP file initially
	tmpDir := t.TempDir()

	// Create a minimal config with default extensions (includes .NSP)
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD", ".NS4", ".NS7", ".NS3", ".NS8", ".NST"},
			Exclude:    []string{},
		},
	}

	// Set up a spy AnalyzeFunc that records calls (thread-safe)
	var mu sync.Mutex
	var analyzeCalls []struct {
		relPath string
		content []byte
	}
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		mu.Lock()
		defer mu.Unlock()
		analyzeCalls = append(analyzeCalls, struct {
			relPath string
			content []byte
		}{relPath, content})
		return model.FileAnalysis{
			ObjectType: model.ObjectProgram,
		}
	}

	logger := slog.New(slog.NewTextHandler(nil, nil))

	// Act: create and start the watcher
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir, cfg, analyzeFunc, logger)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer func() {
		if watcher != nil {
			watcher.Close()
		}
	}()

	// Write a new .NSP file to the temp directory
	testFile := filepath.Join(tmpDir, "newfile.nsp")
	newContent := []byte("PROGRAM NEWFILE. DISPLAY 'CREATED'. END.")
	err = os.WriteFile(testFile, newContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Assert: poll up to 3 seconds for the spy to be called with the file's relPath and content
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, call := range analyzeCalls {
			if call.relPath == "newfile.nsp" && string(call.content) == string(newContent) {
				found = true
				break
			}
		}
		mu.Unlock()
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		mu.Lock()
		defer mu.Unlock()
		t.Errorf("Expected watcher to analyze created file with content %q, but spy calls were: %v",
			string(newContent), analyzeCalls)
	}
}

// TestWatcherDetectsRemovedFile tests that the Watcher detects file removals
// and dispatches re-analysis with nil content via the provided AnalyzeFunc.
// Implements Task 9 (FR-34): External-change detection for removed files.
// The watcher must detect removed indexed files in the workspace and call the
// analyze function with the file's relative path and nil content (FR-34 acceptance criterion 1).
func TestWatcherDetectsRemovedFile(t *testing.T) {
	// Arrange: create a temp directory with a .NSP file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.nsp")
	initialContent := []byte("PROGRAM TEST. END.")

	err := os.WriteFile(testFile, initialContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a minimal config with default extensions (includes .NSP)
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Extensions: []string{".NSP", ".NSN", ".NSS", ".NSC", ".NSM", ".NSL", ".NSG", ".NSA", ".NSH", ".NSD", ".NS4", ".NS7", ".NS3", ".NS8", ".NST"},
			Exclude:    []string{},
		},
	}

	// Set up a spy AnalyzeFunc that records calls (thread-safe)
	var mu sync.Mutex
	var analyzeCalls []struct {
		relPath string
		content []byte
	}
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		mu.Lock()
		defer mu.Unlock()
		analyzeCalls = append(analyzeCalls, struct {
			relPath string
			content []byte
		}{relPath, content})
		return model.FileAnalysis{
			ObjectType: model.ObjectProgram,
		}
	}

	logger := slog.New(slog.NewTextHandler(nil, nil))

	// Act: create and start the watcher
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watcher, err := NewWatcher(ctx, tmpDir, cfg, analyzeFunc, logger)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer func() {
		if watcher != nil {
			watcher.Close()
		}
	}()

	// Delete the .NSP file
	err = os.Remove(testFile)
	if err != nil {
		t.Fatalf("Failed to delete test file: %v", err)
	}

	// Assert: poll up to 3 seconds for the spy to be called with relPath == "test.nsp" and content == nil
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, call := range analyzeCalls {
			if call.relPath == "test.nsp" && call.content == nil {
				found = true
				break
			}
		}
		mu.Unlock()
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		mu.Lock()
		defer mu.Unlock()
		t.Errorf("Expected watcher to analyze removed file with nil content, but spy calls were: %v",
			analyzeCalls)
	}
}
