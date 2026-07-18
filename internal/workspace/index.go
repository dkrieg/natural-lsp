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
//
// It also holds an in-memory-only, encoding-agnostic per-file line-width table
// (lineWidths, feature 22 T8 / OQ-B B-i) so the workspace/symbol and references
// providers can convert byte-offset columns to negotiated code units WITHOUT
// re-reading the source file on every query. The table is NOT persisted (no
// cache-format bump, no model change); it is recomputed once at cache load and
// populated inline wherever content is already in hand. See linewidth.go.
type Index struct {
	mu         sync.RWMutex
	entries    map[string]model.FileAnalysis
	lineWidths map[string]*lineWidthTable

	// nameIndex caches the name→[]Candidate map built by buildNameIndexLocked
	// (feature 22 T7). It is the O(files) work that NamesWithPrefix and
	// LookupByName previously repeated on every call (per completion keystroke /
	// per definition request). A non-nil value is a valid cache built for
	// nameIndexCfg; nil means "stale / not built" and forces a lazy rebuild on
	// the next lookup. It is invalidated (set nil) by every entries mutation —
	// which is exclusively Add — under the write lock (mu.Lock), so lookups only
	// ever READ it under the read lock (or build it under the write lock). See
	// cachedNameIndex for the double-checked build discipline (no RLock→Lock
	// upgrade, hence deadlock-free).
	nameIndex map[string][]Candidate

	// nameIndexCfg is the config pointer the cached nameIndex was built for. cfg
	// is fixed for the lifetime of a server session (loaded once at startup), so
	// in practice this never changes; comparing identity is a cheap defensive
	// guard that a caller passing a different cfg forces a rebuild rather than
	// serving results computed under the wrong library map.
	nameIndexCfg *config.Config
}

// Add stores a FileAnalysis keyed by path.
//
// Add does not populate the line-width table (it has no content); callers that
// hold the source content should call PutContent to enable disk-free range
// conversion for that file. A file added via Add alone falls back to treating
// byte columns as code units (exact for ASCII — see lineWidthTable.protocolRange).
func (idx *Index) Add(path string, analysis model.FileAnalysis) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.entries == nil {
		idx.entries = make(map[string]model.FileAnalysis)
	}
	idx.entries[path] = analysis
	// Invalidate the cached name index: the set of names/candidates may have
	// changed (new object, or a retype of an existing one). The next
	// NamesWithPrefix/LookupByName rebuilds it once. Done under the write lock we
	// already hold, so there is no lock upgrade and no race with a concurrent
	// lookup (feature 22 T7). Add is the ONLY writer of idx.entries (Build,
	// cache Load, and the server's applyDocumentChange all funnel through it),
	// so this is the sole invalidation point.
	idx.nameIndex = nil
	idx.nameIndexCfg = nil
}

// PutContent records the per-file line-width table for path from its raw source
// content, so range conversion in the workspace/symbol and references providers
// needs no disk read (feature 22 T8). It is safe to call alongside Add (same
// mutex) and idempotent: a later call replaces the prior table (e.g. on a
// document change). Content is not retained wholesale — only per-line width data,
// and for a fully-ASCII file no bytes are retained at all (see buildLineWidthTable).
func (idx *Index) PutContent(path string, content []byte) {
	table := buildLineWidthTable(string(content))
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.lineWidths == nil {
		idx.lineWidths = make(map[string]*lineWidthTable)
	}
	idx.lineWidths[path] = table
}

// RangeConverter maps a model.Range to protocol-space coordinates (0-based,
// end-exclusive) in the requested encoding WITHOUT reading the source file,
// using the file's in-memory line-width table (feature 22 T8). utf16 selects
// UTF-16 code units; false selects UTF-8 (byte) units. It reproduces the
// server's toProtocolRange semantics; the four returns are startLine, startChar,
// endLine, endChar (the package returns raw coordinates to avoid a dependency on
// go.lsp.dev/protocol).
type RangeConverter func(r model.Range, utf16 bool) (startLine, startChar, endLine, endChar uint32)

// ForEachWithRange calls f for each indexed entry, passing a disk-free
// RangeConverter bound to that file's line-width table (feature 22 T8). Both the
// entry walk and the converter run under a single RLock, so there is no
// nested-lock re-entrancy hazard (a plain ForEach + ProtocolRange would
// re-acquire the RLock inside the callback, which can deadlock if a writer is
// queued between the two acquisitions). Order is arbitrary.
func (idx *Index) ForEachWithRange(f func(path string, analysis model.FileAnalysis, toRange RangeConverter)) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for path, analysis := range idx.entries {
		table := idx.lineWidths[path]
		conv := func(r model.Range, utf16 bool) (uint32, uint32, uint32, uint32) {
			return table.protocolRange(r, utf16)
		}
		f(path, analysis, conv)
	}
}

// ProtocolRange converts a model.Range for the file at path into protocol-space
// coordinates (0-based, end-exclusive) in the requested encoding, using the
// in-memory line-width table and NEVER reading the file (feature 22 T8). utf16
// selects UTF-16 code units; false selects UTF-8 (byte) units. It reproduces the
// server's toProtocolRange semantics exactly, so results are byte-identical to
// the previous disk-reading implementation. Missing-table files fall back to
// byte==code-unit (exact for ASCII); never panics (FR-43).
//
// It returns the four raw coordinates (startLine, startChar, endLine, endChar)
// so this package need not depend on go.lsp.dev/protocol; the server assembles
// them into a protocol.Range.
func (idx *Index) ProtocolRange(path string, r model.Range, utf16 bool) (startLine, startChar, endLine, endChar uint32) {
	return idx.lineWidthsFor(path).protocolRange(r, utf16)
}

// lineWidthsFor returns the line-width table for path, or nil if none is
// recorded (in which case callers fall back to the ASCII-exact byte==code-unit
// path). Thread-safe.
func (idx *Index) lineWidthsFor(path string) *lineWidthTable {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.lineWidths[path]
}

// hasLineWidths reports whether path already has a line-width table (used by the
// one-time ensureLineWidths sweep to skip files whose table was populated inline
// during analysis). Thread-safe.
func (idx *Index) hasLineWidths(path string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.lineWidths[path]
	return ok
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
// Performance: this method consults the cached name index (feature 22 T7),
// which is built once and reused across calls until an Add invalidates it — so a
// lookup is O(candidates-for-name), not O(all files), on the warm path. The
// first call after a mutation pays the one-time O(files) rebuild. When resolving
// many edges in bulk — for example during call-graph resolution — callers may
// still build a full name index once with buildNameIndex and look up all edges
// against it; both share the same underlying map shape.
//
// This method is thread-safe and race-free.
func (idx *Index) LookupByName(name string, typ model.ObjectType, cfg *config.Config) []Candidate {
	// Uppercase the input name for case-insensitive matching.
	inputName := strings.ToUpper(name)

	// The cached bucket is already sorted by Path (buildNameMapFrom guarantees it),
	// so no re-sort is needed. Do NOT mutate or return the cache's slice: copy the
	// filtered matches into a fresh slice so a caller can never alias/mutate the
	// shared cache.
	bucket := idx.cachedNameIndex(cfg)[inputName]

	candidates := make([]Candidate, 0, len(bucket))
	for _, cand := range bucket {
		// A zero typ is the "any type" sentinel; non-zero restricts by ObjectType.
		if typ != "" && cand.Type != typ {
			continue
		}
		candidates = append(candidates, cand)
	}

	// Always a non-nil slice (make above), matching the prior contract.
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
	// Use the cached name index (built once, invalidated on Add) instead of
	// rebuilding the whole O(files) map on every keystroke (feature 22 T7).
	nameIndex := idx.cachedNameIndex(cfg)

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

	return buildNameMapFrom(snapshot, cfg)
}

// buildNameMapFrom builds the name → candidates map from a snapshot of entries.
// It is pure (no lock, no Index state), shared by buildNameIndex (which snapshots
// under RLock first) and buildNameIndexLocked (which passes idx.entries directly
// while holding the write lock).
func buildNameMapFrom(entries map[string]model.FileAnalysis, cfg *config.Config) map[string][]Candidate {
	nameMap := make(map[string][]Candidate)
	for path, fa := range entries {
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

// cachedNameIndex returns the cached name→[]Candidate map, building it on first
// use (or after an Add invalidated it) and reusing it across subsequent calls
// without an intervening mutation — turning the per-query O(files) buildNameIndex
// into an amortized O(1) map read (feature 22 T7).
//
// Lock discipline (deadlock-free, review-concurrency): the fast path takes the
// RLock and, if a cache built for this cfg is present, returns it. A lazy build
// cannot upgrade RLock→Lock (Go's RWMutex has no upgrade — that self-deadlocks),
// so the slow path RELEASES the RLock, takes the full Lock, and double-checks:
// another goroutine may have built the cache between the two acquisitions. Under
// the write lock it builds from idx.entries directly (no nested snapshot) and
// publishes both nameIndex and nameIndexCfg atomically. Add, the only writer,
// also holds the write lock when it nils the cache — so the cache is only ever
// read under RLock or written under Lock, with no torn reads.
//
// The returned map is treated as read-only by callers (they range over it and
// copy out Candidate values). It is never mutated after publication; a mutation
// (Add) replaces the whole map with nil, and the next call rebuilds a new one.
func (idx *Index) cachedNameIndex(cfg *config.Config) map[string][]Candidate {
	// Fast path: a valid cache built for this cfg.
	idx.mu.RLock()
	if idx.nameIndex != nil && idx.nameIndexCfg == cfg {
		nameIndex := idx.nameIndex
		idx.mu.RUnlock()
		return nameIndex
	}
	idx.mu.RUnlock()

	// Slow path: build (or rebuild for a new cfg) under the write lock.
	idx.mu.Lock()
	defer idx.mu.Unlock()
	// Double-check: another goroutine may have built it while we waited on Lock.
	if idx.nameIndex != nil && idx.nameIndexCfg == cfg {
		return idx.nameIndex
	}
	built := buildNameMapFrom(idx.entries, cfg)
	idx.nameIndex = built
	idx.nameIndexCfg = cfg
	return built
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
			// Populate the line-width table inline from the content we just read,
			// so no second disk read is needed for this file (feature 22 T8).
			idx.PutContent(relPath, content)
			analyzedAny = true
			if stale {
				staleCount++
			}
		}
	}

	// One-time line-width sweep for warm-cache hits: files served straight from
	// the cache were NOT re-read above, so they have no line-width table yet.
	// Read each such file exactly once here to build its table (feature 22 T8 /
	// OQ-B B-i — an amortized ONE-TIME cost at build/load, NOT a per-query read).
	// A file that cannot be read is left without a table; range conversion then
	// falls back to byte==code-unit (exact for ASCII, FR-43 graceful degradation).
	ensureLineWidths(idx, root, files)

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

// ensureLineWidths builds the line-width table for any indexed file that does
// not already have one (feature 22 T8). It is called once at the end of a build
// so warm-cache hits — whose content was never read into memory — gain a table
// from a single disk read each. This is an amortized ONE-TIME cost at build/load
// time, not a per-query read. Files not present in the index are skipped; a read
// failure leaves the file without a table (ASCII byte==code-unit fallback,
// FR-43). files are absolute paths (as walked); the table key is the
// workspace-relative path, matching the entries key.
func ensureLineWidths(idx *Index, root string, files []string) {
	for _, filePath := range files {
		relPath, _ := filepath.Rel(root, filePath)
		if _, inIndex := idx.Get(relPath); !inIndex {
			continue
		}
		if idx.hasLineWidths(relPath) {
			continue
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		idx.PutContent(relPath, content)
	}
}
