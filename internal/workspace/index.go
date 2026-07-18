// Package workspace holds the cross-file symbol table built from per-file
// FileAnalysis results, plus incremental re-analysis: when a file changes,
// only it and its dependents are re-indexed (PRD FR-35, FR-36).
package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
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

// Candidate represents a definition candidate returned by Index.LookupByName.
// It includes the workspace-relative path and the derived owning library.
type Candidate struct {
	// Path is the workspace-relative file path.
	Path string

	// Name is the uppercased object name (filename stem, Natural identifiers are
	// case-insensitive). E.g. "APP/MYSUB.NSN" → "MYSUB". It is always non-empty
	// for a valid indexed file and matches the key used by objectIdentity.
	Name string

	// Library is the owning Natural library name (e.g., "APP", "COMMON"),
	// derived from the config Library mapping, or empty if not in a declared library.
	Library string

	// Type is the ObjectType of the candidate (e.g., ObjectSubprogram).
	Type model.ObjectType
}

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

// LookupByName looks up candidate definitions by object name (case-insensitive,
// derived from the filename stem via objectIdentity), optionally filtered by
// expected ObjectType. It returns the matching entries sorted by Path (deterministic)
// with workspace-relative paths and derived owning libraries (from config path
// mapping).
//
// Matching is case-insensitive: "mysub", "MYSUB", and "Mysub" are equivalent.
// The sort order is lexicographic on Path, which is byte-stable and independent
// of map-iteration order.
//
// A zero/empty typ (model.ObjectType("")) is the "any type" sentinel: all
// matching names are returned regardless of their ObjectType. Pass a non-zero
// typ to restrict results to a specific object kind (e.g. model.ObjectSubprogram).
//
// Unknown names return an empty (non-nil) slice, never an error.
//
// Performance: this method is O(n) over all indexed files on every call.
// When resolving many edges in bulk — for example during call-graph resolution —
// callers should build a full name index once using buildNameIndex and look up
// all edges against that map, rather than calling LookupByName once per edge
// (which would be O(edges * files)). See buildNameIndex for details.
//
// This method is thread-safe and race-free.
func (idx *Index) LookupByName(name string, typ model.ObjectType, cfg *config.Config) []Candidate {
	// Uppercase the input name for case-insensitive matching.
	inputName := strings.ToUpper(name)

	var candidates []Candidate

	// ForEach holds the read lock for the duration of the iteration.
	idx.ForEach(func(path string, fa model.FileAnalysis) {
		objName, objLibrary := objectIdentity(path, cfg)

		if objName != inputName {
			return
		}

		// A zero typ is the "any type" sentinel; non-zero restricts by ObjectType.
		if typ != "" && fa.ObjectType != typ {
			return
		}

		candidates = append(candidates, Candidate{
			Path:    path,
			Name:    objName,
			Library: objLibrary,
			Type:    fa.ObjectType,
		})
	})

	// Sort by path for deterministic, byte-stable output. The golden-file tests,
	// on-disk cache (SHA-256 keyed), and downstream lsp-graph consumer all require
	// this stability.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Path < candidates[j].Path
	})

	if len(candidates) == 0 {
		return []Candidate{}
	}

	return candidates
}

// NamesWithPrefix returns reachable candidates whose object name starts with the
// uppercased prefix AND whose Type == typ. The method reuses the steplib chain
// resolution logic from resolveByName: with a library map configured, only
// candidates reachable from the caller's current library (longest-prefix match of
// referencingPath) via the non-transitive steplib chain are returned. Unlike
// resolveByName (which collapses to a single binding), completion is a discovery
// surface: a name present in more than one reachable library yields one candidate
// per library (all reachable options are offered, not deduped to a single steplib
// winner). With no library map (or an undeclared-path caller), flat namespace: all
// prefix+type matches are returned.
//
// Empty prefix returns all reachable candidates of that type. Type filter of zero
// ObjectType ("") is not supported; pass a non-zero typ.
//
// Results are deterministic: sorted by path (stable across calls).
// Returns a non-nil (possibly empty) slice.
//
// This method is thread-safe and race-free (mirrors LookupByName discipline).
// It is used by the completion provider (feature 16, Task 2).
func (idx *Index) NamesWithPrefix(prefix string, typ model.ObjectType, referencingPath string, cfg *config.Config) []Candidate {
	// Build the name index once from the current snapshot.
	nameIndex := idx.buildNameIndex(cfg)

	// Normalize prefix to uppercase for case-insensitive matching.
	upperPrefix := strings.ToUpper(prefix)

	// Filter names by prefix match and collect candidates.
	var allCandidates []Candidate
	for name, candidates := range nameIndex {
		// Skip names that don't start with the prefix.
		if !strings.HasPrefix(name, upperPrefix) {
			continue
		}

		// Filter by ObjectType.
		for _, cand := range candidates {
			if cand.Type == typ {
				allCandidates = append(allCandidates, cand)
			}
		}
	}

	// Determine if we have a library map and the caller's current library.
	_, currentLibrary := objectIdentity(referencingPath, cfg)
	searchChain := buildSearchChain(currentLibrary, cfg)

	// Apply reachability filtering:
	// - If searchChain is non-empty: filter to chain-reachable candidates.
	// - If searchChain is empty (flat namespace): keep all candidates.
	if len(searchChain) > 0 {
		// Library map mode: keep only candidates whose library is in the search chain.
		var result []Candidate
		for _, cand := range allCandidates {
			inChain := false
			for _, lib := range searchChain {
				if cand.Library == lib {
					inChain = true
					break
				}
			}
			if inChain {
				result = append(result, cand)
			}
		}

		// Sort by path for determinism.
		sort.Slice(result, func(i, j int) bool {
			return result[i].Path < result[j].Path
		})
		return result
	}

	// Flat namespace mode: no library map (or undeclared-path caller).
	// Return all prefix+type matches (no deduping).
	sort.Slice(allCandidates, func(i, j int) bool {
		return allCandidates[i].Path < allCandidates[j].Path
	})
	if len(allCandidates) == 0 {
		return []Candidate{}
	}
	return allCandidates
}

// buildNameIndex snapshots the index and returns a map from uppercase object name
// to all Candidate definitions for that name (filtered and library-mapped per cfg).
//
// The map is built in a single O(n) pass over the index under the read lock,
// making it the right primitive for the call-graph resolver: call buildNameIndex
// once before the resolution loop, then look up each edge target in the returned
// map in O(1) — giving O(files + edges) overall rather than O(files * edges).
//
// Each Candidate slice in the map is sorted by Path (deterministic, byte-stable).
// Empty slices are not stored; absent map keys represent zero-candidate names.
//
// buildNameIndex takes a snapshot at call time. It does not cache state on Index
// and requires no invalidation — the caller owns the returned map.
func (idx *Index) buildNameIndex(cfg *config.Config) map[string][]Candidate {
	// Snapshot all entries under the read lock.
	idx.mu.RLock()
	snapshot := make(map[string]model.FileAnalysis, len(idx.entries))
	for path, fa := range idx.entries {
		snapshot[path] = fa
	}
	idx.mu.RUnlock()

	// Build the name → candidates map from the snapshot (no lock held).
	nameMap := make(map[string][]Candidate)
	for path, fa := range snapshot {
		objName, objLibrary := objectIdentity(path, cfg)
		nameMap[objName] = append(nameMap[objName], Candidate{
			Path:    path,
			Name:    objName,
			Library: objLibrary,
			Type:    fa.ObjectType,
		})
	}

	// Sort each candidate list by path for deterministic ordering.
	for name, list := range nameMap {
		sort.Slice(list, func(i, j int) bool {
			return list[i].Path < list[j].Path
		})
		nameMap[name] = list
	}

	return nameMap
}

// Invalidate returns the set of workspace-relative paths that directly or
// transitively depend on the file at path via INCLUDE edges. The returned
// slice is sorted for deterministic output; an empty slice (not nil) is
// returned when the file has no dependents.
//
// Dependency matching uses object NAME, not file path. An INCLUDE edge's
// TargetName (e.g. "SHARED") is compared against the uppercased filename stem
// of every indexed file (e.g. "SHARED.NSC" → "SHARED") using the shared
// objectIdentity helper. This corrects a prior name-vs-path bug where
// edge.TargetName was compared directly against the full workspace-relative
// path, which never matched (TargetName carries the bare copycode name;
// the path carries the full relative path including extension).
//
// Transitive closure is computed via BFS: if A includes B and B includes C,
// invalidating C returns {A, B}. Each newly discovered dependent is itself
// matched by object name, so the same name-based matching applies at every BFS
// level (no path comparison anywhere in the traversal).
//
// The entire operation is performed under a single read lock (RLock) held for
// its duration, making it race-safe for concurrent callers — consistent with
// the snapshot-on-read pattern used throughout Index (FR-35, FR-36).
func (idx *Index) Invalidate(path string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Compute the object name of the changed file (UPPERCASE filename stem).
	changedName, _ := objectIdentity(path, nil)

	// Build reverse dependency graph: for each file, find files that include it
	dependents := make(map[string]struct{})

	// First pass: find direct dependents (files with INCLUDE edge pointing to the changed object).
	// We match by UPPERCASE(edge.TargetName) against the changed object's name.
	for depPath, fa := range idx.entries {
		for _, edge := range fa.Edges {
			if edge.Kind == model.EdgeIncludes && strings.ToUpper(edge.TargetName) == changedName {
				dependents[depPath] = struct{}{}
			}
		}
	}

	// Second pass: find transitive dependents via BFS.
	// When A includes B and B includes C, invalidating C returns both A and B.
	// We now use the object names of files in the queue, not their paths.
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

		// Compute the object name of the current file.
		currentName, _ := objectIdentity(current, nil)

		// Find all files that include 'current' (by its object name).
		for depPath, fa := range idx.entries {
			if _, alreadyVisited := visited[depPath]; alreadyVisited {
				continue
			}
			for _, edge := range fa.Edges {
				if edge.Kind == model.EdgeIncludes && strings.ToUpper(edge.TargetName) == currentName {
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
// The build is cancellable via ctx: it is checked once per file at the top of
// the scan loop, so a cancelled ctx (e.g. on server shutdown, feature 21 T4)
// aborts the build early instead of running to completion. On cancellation the
// partially-populated index is discarded and (nil, ctx.Err()) is returned — see
// BuildWithCache for the rationale.
//
// The returned Index is concurrency-safe. Errors are collected and returned
// at the end; individual file processing errors do not abort the build.
func Build(ctx context.Context, root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger, onProgress func(path string, current, total int)) (*Index, error) {
	idx, _, _, err := BuildWithCache(ctx, root, cfg, az, logger, "", nil, onProgress)
	return idx, err
}

// BuildWithCache is like Build but integrates with the on-disk cache.
// It accepts an optional cache path and a map of current file hashes.
//
// Behavior:
//   - When cachePath is empty: full index build from scratch, no cache I/O.
//   - When cachePath is set and no/corrupt/version-mismatched cache exists: full
//     rebuild from scratch, then the fresh index is written to cachePath (FR-43
//     graceful degradation — a corrupt cache never crashes, it rebuilds).
//   - When a valid cache exists: load it, re-analyze only files whose content hash
//     changed (or that are newly present), retain the rest, then write the cache
//     back if anything was re-analyzed (warm start, FR-38/NFR-2).
//
// Content hashes: when currentHashes is nil and cachePath is set, they are
// computed from disk (sha256 of file content, keyed by workspace-relative path)
// so invalidation is content-based (FR-38), not mtime-based. Callers may supply
// an explicit map to override (used by the workspace tests).
//
// Returns:
// - *Index: the populated index
// - staleCount: number of files that were (re-)analyzed (stale or not-in-cache)
// - totalFiles: total number of files in the workspace
// - error: any error that occurred during the build
//
// The onProgress callback is invoked for each file with accurate counts.
//
// Cancellation contract (feature 21 T4/OQ-F): ctx is checked once per file at
// the top of the scan loop. When ctx is cancelled the build stops immediately
// and returns (nil, 0, totalFiles, ctx.Err()). A partial index is deliberately
// NOT returned: the only caller that cancels is the server's background build
// goroutine, which checks bgCtx before publishing and skips publish on cancel
// (so a partial index would never be published), and discarding it keeps the
// contract unambiguous — a non-nil error always means "no usable index". ctx is
// checked once per file, not more often, to avoid hurting build throughput.
func BuildWithCache(ctx context.Context, root string, cfg config.Config, az analysis.Analyzer, logger *slog.Logger, cachePath string, currentHashes map[string]string, onProgress func(path string, current, total int)) (*Index, int, int, error) {
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

	// When a cache is in play and the caller did not supply content hashes,
	// compute them from disk so Load can invalidate changed files by content
	// (FR-38 — not mtime). Keyed by workspace-relative path to match the cache
	// entry keys. A file that cannot be read is simply omitted (it will be
	// treated as not-in-cache and re-analyzed, or its read failure handled in
	// the scan loop). Callers that pass an explicit map (the workspace tests)
	// keep full control. Feature 21 T12.
	if cachePath != "" && currentHashes == nil {
		currentHashes = make(map[string]string, totalFiles)
		for _, filePath := range files {
			content, readErr := os.ReadFile(filePath)
			if readErr != nil {
				continue
			}
			relPath, _ := filepath.Rel(root, filePath)
			currentHashes[relPath] = fmt.Sprintf("%x", sha256.Sum256(content))
		}
	}

	// Try to load from cache first. A cache HIT populates idx with the persisted
	// per-file analysis; staleFiles are the entries whose content hash no longer
	// matches (relative paths, from Load). On any failure (missing/corrupt/
	// version-mismatch) fall back to a full rebuild: an empty idx with NO cached
	// entries, so every scanned file is treated as needing analysis below (the
	// notInCache branch). This is the FR-43 graceful-degradation path — a corrupt
	// cache never crashes; it just rebuilds. (Feature 21 T12.)
	var idx *Index
	var staleFiles []string
	var err error

	if cachePath != "" {
		idx, staleFiles, err = Load(cachePath, currentHashes, logger)
		if err != nil {
			// Cache missing/unreadable/corrupt: full rebuild from scratch.
			logger.Info("cache load failed, building from scratch", "path", cachePath, "error", err)
			idx = &Index{entries: make(map[string]model.FileAnalysis)}
			staleFiles = nil
		} else if idx == nil {
			// Version mismatch: discard the cache, full rebuild from scratch.
			idx = &Index{entries: make(map[string]model.FileAnalysis)}
			staleFiles = nil
		}
	} else {
		// No cache path provided - full build from scratch.
		idx = &Index{entries: make(map[string]model.FileAnalysis)}
	}

	// Create a map of stale files for quick lookup. staleFiles from Load() are
	// relative paths, matched against relPath below.
	staleMap := make(map[string]bool)
	for _, f := range staleFiles {
		staleMap[f] = true
	}

	// staleCount counts only files re-analyzed because their content hash no
	// longer matched a LOADED cache entry (a warm-start invalidation). A cold
	// build (no prior cache) analyzes every file but reports staleCount 0, so
	// the count means "files that changed since the last cached run" — the
	// signal the server uses to tell a warm start from a cold one.
	staleCount := 0
	// analyzedAny tracks whether ANY file was (re-)analyzed this build (stale OR
	// not-in-cache), which is what determines whether the on-disk cache needs
	// rewriting — distinct from staleCount, which excludes cold/new-file work.
	analyzedAny := false

	// Process all files. A file is (re-)analyzed when it is stale (content hash
	// changed) or absent from the loaded cache (cold start / newly-added file);
	// otherwise its cached analysis is retained. Fresh cache hits are neither
	// re-read nor re-analyzed (the warm-start fast path, FR-38/NFR-2).
	for i, filePath := range files {
		// Abort early if the build was cancelled (e.g. server shutdown raced the
		// build, feature 21 T4). Checked once per file — cheap and does not hurt
		// throughput. On cancel discard the partial index (see the doc comment).
		if err := ctx.Err(); err != nil {
			return nil, 0, totalFiles, err
		}

		relPath, _ := filepath.Rel(root, filePath)

		// Invoke progress callback
		if onProgress != nil {
			onProgress(relPath, i+1, totalFiles)
		}

		_, inCache := idx.Get(relPath)
		stale := staleMap[relPath]
		if stale || !inCache {
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
			analyzedAny = true
			if stale {
				staleCount++
			}
		}
	}

	// Persist the freshly-built index back to the cache so the next start is
	// warm (FR-37). Write failures are logged, never fatal (FR-43): the built
	// index is still valid for this session. Skipped when no cachePath is set
	// (the pure in-memory Build path). Only written when at least one file was
	// (re-)analyzed OR the cache did not previously exist, to avoid rewriting an
	// unchanged cache on a fully-warm start.
	if cachePath != "" && (analyzedAny || !cacheExists(cachePath)) {
		if err := saveIndex(idx, root, cachePath); err != nil {
			logger.Warn("failed to write cache", "path", cachePath, "error", err)
		}
	}

	return idx, staleCount, totalFiles, nil
}
