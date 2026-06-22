// Package document watches workspace files for changes made outside the
// editor and keeps the index consistent with on-disk state (PRD FR-34).
package document

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"natural-lsp/internal/config"

	"github.com/fsnotify/fsnotify"
)

// logWatcherPanic logs a recovered panic via the structured logger.
// The call to logger.Error is itself guarded by a deferred recover so that a
// misconfigured logger (e.g. a nil writer in tests) cannot cause a secondary
// panic and kill the watcher goroutine — consistent with FR-43's requirement
// that every degradation path is survivable.
func logWatcherPanic(logger *slog.Logger, path string, r any) {
	defer func() { recover() }() //nolint:errcheck // secondary panic suppressed intentionally
	logger.Error("recovered from panic in watcher analyze callback",
		slog.String("path", path),
		slog.String("reason", fmt.Sprintf("%v", r)),
	)
}

// Watcher detects added/modified/removed indexed files in the workspace
// and dispatches re-analysis via the provided AnalyzeFunc (FR-34).
// The watcher runs in a background goroutine listening to filesystem events
// and processes them with FR-43 degradation guarantees.
type Watcher struct {
	fsw            *fsnotify.Watcher
	debounceWindow time.Duration
}

// defaultDebounceWindow is the default quiet-window duration for coalescing rapid events.
// Tests may override this package variable before creating a Watcher.
var defaultDebounceWindow = 100 * time.Millisecond

// NewWatcher creates and starts a filesystem watcher for the workspace root.
// It dispatches re-analysis of changed files via the provided analyze function.
// The watcher goroutine exits when ctx is cancelled.
// It returns an error if the watcher cannot be initialized.
func NewWatcher(ctx context.Context, root string, cfg *config.Config, analyze AnalyzeFunc, logger *slog.Logger) (*Watcher, error) {
	// Create the fsnotify watcher.
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fsw:            fsw,
		debounceWindow: defaultDebounceWindow,
	}

	// Walk the workspace root and add all non-excluded directories.
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		// Compute relative path for exclusion check.
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			// If we can't compute relative path, skip this directory.
			return nil
		}

		// Check if excluded; if so, skip this directory and its contents.
		if cfg.IsExcluded(relPath) {
			return fs.SkipDir
		}

		// Add this directory to the watcher.
		if err := fsw.Add(path); err != nil {
			// Non-fatal: log and continue.
			logger.Error("failed to watch directory", "path", path, "error", err)
		}

		return nil
	})

	if err != nil {
		fsw.Close()
		return nil, err
	}

	// Build extension set from config (normalized to upper-case with dot prefix).
	extSet := make(map[string]bool, len(cfg.Workspace.Extensions))
	for _, ext := range cfg.Workspace.Extensions {
		normalized := strings.ToUpper(ext)
		if !strings.HasPrefix(normalized, ".") {
			normalized = "." + normalized
		}
		extSet[normalized] = true
	}

	// Start the background event-processing goroutine.
	go func() {
		defer fsw.Close()

		// Debounce state: accumulate paths during quiet window.
		var pendingMu sync.Mutex
		pendingPaths := make(map[string]struct{})
		var debounceTimer *time.Timer

		// Helper to drain all pending paths and call analyze.
		drainPending := func() {
			pendingMu.Lock()
			defer pendingMu.Unlock()

			for relPath := range pendingPaths {
				// For removal events, we pass nil content.
				// For modification events, we need to read the file if it still exists.
				absolutePath := filepath.Join(root, relPath)

				// Check if this is a removal: try to stat the file.
				fi, err := os.Stat(absolutePath)
				if err != nil {
					// File doesn't exist; treat as removal (nil content).
					// Wrap analyze call in panic recovery (FR-43).
					func() {
						defer func() {
							if r := recover(); r != nil {
								logWatcherPanic(logger, relPath, r)
							}
						}()
						analyze(relPath, nil)
					}()
					continue
				}

				// Check file size.
				if cfg.Workspace.MaxFileSize > 0 && fi.Size() > cfg.Workspace.MaxFileSize {
					continue
				}

				// Read and analyze.
				content, err := os.ReadFile(absolutePath)
				if err != nil {
					logger.Error("failed to read file for re-analysis", "path", relPath, "error", err)
					continue
				}
				// Wrap analyze call in panic recovery (FR-43).
				func() {
					defer func() {
						if r := recover(); r != nil {
							logWatcherPanic(logger, relPath, r)
						}
					}()
					analyze(relPath, content)
				}()
			}
			pendingPaths = make(map[string]struct{})
		}

		for {
			select {
			case <-ctx.Done():
				// Stop the debounce timer first so no new timer goroutine can win the
				// mutex after we release it. Then drain any accumulated paths so no
				// events are lost on shutdown.
				pendingMu.Lock()
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				pendingMu.Unlock()

				drainPending()
				return

			case event, ok := <-fsw.Events:
				if !ok {
					return
				}

				// Ignore Chmod events (no content change).
				if event.Has(fsnotify.Chmod) {
					continue
				}

				// If it's a directory creation, add it to the watcher.
				if event.Has(fsnotify.Create) {
					if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
						relPath, err := filepath.Rel(root, event.Name)
						if err == nil && !cfg.IsExcluded(relPath) {
							if err := fsw.Add(event.Name); err != nil {
								logger.Error("failed to watch new directory", "path", event.Name, "error", err)
							}
						}
						continue
					}
				}

				// Apply extension filter first — only indexed extensions are relevant.
				// This also prevents spurious removal signals for non-indexed files
				// (e.g. editor temp files renamed during atomic saves).
				ext := strings.ToUpper(filepath.Ext(event.Name))
				if !extSet[ext] {
					continue
				}

				// Derive the relative path.
				relPath, err := filepath.Rel(root, event.Name)
				if err != nil {
					continue
				}

				// For Remove and Rename, signal removal with nil content. The file is
				// already gone so we cannot stat it; the extension check above is sufficient.
				// Add to pending and reset timer.
				if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					pendingMu.Lock()
					pendingPaths[relPath] = struct{}{}
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(w.debounceWindow, drainPending)
					pendingMu.Unlock()
					continue
				}

				// Check if excluded.
				if cfg.IsExcluded(relPath) {
					continue
				}

				// Add to pending paths and reset timer for debouncing.
				pendingMu.Lock()
				pendingPaths[relPath] = struct{}{}
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(w.debounceWindow, drainPending)
				pendingMu.Unlock()

			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				// Non-fatal: log and continue.
				logger.Error("watcher error", "error", err)
			}
		}
	}()

	return w, nil
}

// Close closes the watcher and stops processing events.
func (w *Watcher) Close() error {
	if w.fsw != nil {
		return w.fsw.Close()
	}
	return nil
}
