// Package workspace holds the cross-file symbol table built from per-file
// FileAnalysis results, plus incremental re-analysis: when a file changes,
// only it and its dependents are re-indexed (PRD FR-35, FR-36).
package workspace

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"natural-lsp/internal/analysis"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// Index is an in-memory map of file paths to FileAnalysis results.
// It provides basic query methods for the workspace symbol table.
type Index struct {
	mu      sync.RWMutex
	entries map[string]model.FileAnalysis
}

// Add stores a FileAnalysis keyed by path.
func (idx *Index) Add(path string, analysis model.FileAnalysis) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.entries == nil {
		idx.entries = make(map[string]model.FileAnalysis)
	}
	idx.entries[path] = analysis
}

// Get retrieves a FileAnalysis for the given path.
// Returns ok=false if the path is not found.
func (idx *Index) Get(path string) (model.FileAnalysis, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	analysis, ok := idx.entries[path]
	return analysis, ok
}

// ForEach calls f for each entry in the index in arbitrary order.
func (idx *Index) ForEach(f func(path string, analysis model.FileAnalysis)) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for path, analysis := range idx.entries {
		f(path, analysis)
	}
}

// Keys returns all stored paths.
func (idx *Index) Keys() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	keys := make([]string, 0, len(idx.entries))
	for path := range idx.entries {
		keys = append(keys, path)
	}
	return keys
}

// Invalidate returns the set of files that depend on the given path.
// It traverses the INCLUDE edges to find all direct and transitive dependents.
// For example, if A includes B and B includes C, invalidating C returns both A and B.
func (idx *Index) Invalidate(path string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Build reverse dependency graph: for each file, find files that include it
	dependents := make(map[string]struct{})

	// First pass: find direct dependents (files with INCLUDE edge pointing to path)
	for depPath, fa := range idx.entries {
		for _, edge := range fa.Edges {
			if edge.Kind == model.EdgeIncludes && edge.TargetName == path {
				dependents[depPath] = struct{}{}
			}
		}
	}

	// Second pass: find transitive dependents via BFS
	// When A includes B and B includes C, invalidating C returns both A and B
	queue := make([]string, 0, len(dependents))
	visited := make(map[string]struct{})
	// Mark the original path as visited to avoid revisiting it
	visited[path] = struct{}{}
	for dep := range dependents {
		queue = append(queue, dep)
		visited[dep] = struct{}{}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Find all files that include 'current'
		for depPath, fa := range idx.entries {
			if _, alreadyVisited := visited[depPath]; alreadyVisited {
				continue
			}
			for _, edge := range fa.Edges {
				if edge.Kind == model.EdgeIncludes && edge.TargetName == current {
					dependents[depPath] = struct{}{}
					visited[depPath] = struct{}{}
					queue = append(queue, depPath)
					break
				}
			}
		}
	}

	// Convert to slice
	result := make([]string, 0, len(dependents))
	for dep := range dependents {
		result = append(result, dep)
	}
	sort.Strings(result)

	return result
}

// Build walks the workspace root directory, indexes all files in the indexed
// extension set, and returns an Index populated with FileAnalysis results.
//
// It handles the following:
// - Walks the workspace root using filepath.WalkDir
// - Filters files by the indexed extension set from config
// - Skips excluded directories using cfg.IsExcluded
// - Skips files exceeding cfg.Workspace.MaxFileSize
// - Analyzes each file using the provided analyzer
// - Populates the Index with analysis results
// - Invokes onProgress callback for each file with accurate counts
// - Handles analyzer panics gracefully (FR-43) by recovering and logging
//
// The returned Index is concurrency-safe. Errors are collected and returned
// at the end; individual file processing errors do not abort the build.
func Build(root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger, onProgress func(path string, current, total int)) (*Index, error) {
	idx, _, _, err := BuildWithCache(root, cfg, az, logger, "", nil, onProgress)
	return idx, err
}

// BuildWithCache is like Build but integrates with the on-disk cache.
// It accepts an optional cache path and a map of current file hashes.
//
// Behavior:
// - When cachePath is empty or cache doesn't exist: full index build from scratch
// - When cache exists and is fresh: load from cache, no re-analysis
// - When cache exists with stale files: load cache + re-analyze only stale files
//
// Returns:
// - *Index: the populated index
// - staleCount: number of files that were re-analyzed (stale files)
// - totalFiles: total number of files in the workspace
// - error: any error that occurred during the build
//
// The onProgress callback is invoked for each file with accurate counts.
func BuildWithCache(root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger, cachePath string, currentHashes map[string]string, onProgress func(path string, current, total int)) (*Index, int, int, error) {
	// Collect all files in the workspace root that match the indexed extensions.
	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip directory if it's excluded
			relPath, _ := filepath.Rel(root, path)
			if relPath == "" {
				relPath = path
			}
			if cfg.IsExcluded(relPath) {
				return filepath.SkipDir
			}
			return nil
		}

		// Find matching extension (case-insensitive)
		ext := filepath.Ext(path)
		matched := false
		for _, e := range cfg.Workspace.Extensions {
			if len(e) > 0 && e[0] == '.' {
				upperExt := strings.ToUpper(e)
				if upperExt == strings.ToUpper(ext) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return nil, 0, 0, err
	}

	// Sort files for deterministic ordering
	sort.Strings(files)
	totalFiles := len(files)

	// Try to load from cache first
	var idx *Index
	var staleFiles []string
	var err error

	if cachePath != "" {
		idx, staleFiles, err = Load(cachePath, currentHashes, logger)
		if err != nil {
			// Cache load failed, fall back to full build
			logger.Info("cache load failed, building from scratch", "path", cachePath, "error", err)
			idx = &Index{entries: make(map[string]model.FileAnalysis)}
			staleFiles = files
		} else if idx == nil {
			// Version mismatch - all files are stale
			idx = &Index{entries: make(map[string]model.FileAnalysis)}
			staleFiles = files
		}
	} else {
		// No cache path provided - full build from scratch
		idx = &Index{entries: make(map[string]model.FileAnalysis)}
	}

	// Create a map of stale files for quick lookup
	// staleFiles from Load() are already relative paths
	staleMap := make(map[string]bool)
	for _, f := range staleFiles {
		staleMap[f] = true
	}

	// Track how many files are actually re-analyzed (stale files from cache)
	staleCount := 0

	// Process all files - load from cache first, then re-analyze stale files
	for i, filePath := range files {
		relPath, _ := filepath.Rel(root, filePath)

		// Invoke progress callback
		if onProgress != nil {
			onProgress(relPath, i+1, totalFiles)
		}

		// Check if file is stale and needs re-analysis
		if staleMap[relPath] {
			// Read file
			content, err := os.ReadFile(filePath)
			if err != nil {
				logger.Warn("failed to read file", "path", filePath, "error", err)
				continue
			}

			// Check file size
			if int64(len(content)) > cfg.Workspace.MaxFileSize {
				logger.Info("skipping file due to size limit", "path", filePath, "size", len(content), "max", cfg.Workspace.MaxFileSize)
				continue
			}

			// Analyze file with panic recovery
			var fa model.FileAnalysis
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Warn("analyzer panic recovered", "path", filePath, "panic", r)
						fa = model.FileAnalysis{ObjectType: model.ObjectUnknown}
					}
				}()
				fa, err = az.Analyze(filePath, content)
				if err != nil {
					logger.Warn("analyzer returned error", "path", filePath, "error", err)
					fa = model.FileAnalysis{ObjectType: model.ObjectUnknown}
				}
			}()

			idx.Add(relPath, fa)
			staleCount++
		} else if cachePath == "" {
			// No cache exists - build file from scratch
			content, err := os.ReadFile(filePath)
			if err != nil {
				logger.Warn("failed to read file", "path", filePath, "error", err)
				continue
			}

			// Check file size
			if int64(len(content)) > cfg.Workspace.MaxFileSize {
				logger.Info("skipping file due to size limit", "path", filePath, "size", len(content), "max", cfg.Workspace.MaxFileSize)
				continue
			}

			// Analyze file with panic recovery
			var fa model.FileAnalysis
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Warn("analyzer panic recovered", "path", filePath, "panic", r)
						fa = model.FileAnalysis{ObjectType: model.ObjectUnknown}
					}
				}()
				fa, err = az.Analyze(filePath, content)
				if err != nil {
					logger.Warn("analyzer returned error", "path", filePath, "error", err)
					fa = model.FileAnalysis{ObjectType: model.ObjectUnknown}
				}
			}()

			idx.Add(relPath, fa)
		}
	}

	return idx, staleCount, totalFiles, nil
}
