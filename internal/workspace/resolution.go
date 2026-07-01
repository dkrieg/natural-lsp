// Package workspace implements cross-file resolution of call and dependency
// references produced by extraction (internal/analysis/natural). Resolution
// follows Natural's steplib chain — current library → steplibs in order →
// SYSTEM — driven by the configured library map, NOT file paths. The same
// module name can exist in multiple libraries; search order disambiguates.
// With no library map, the workspace falls back to a single flat namespace and
// ambiguous resolution is reported as a diagnostic.
//
// PERFORM resolves an inline subroutine before an external one of the same
// name. See docs/plans/natural-lsp-prd.md (FR-10..18, FR-31).
//
// OQ-1 architectural decision: the resolution index lives here in
// internal/workspace — not in internal/model and not in the on-disk cache.
// Resolution results are recomputed from cached model.EdgeEntry values on
// every load, so no cache-format version bump is required when resolution
// logic evolves. The types in this package are model-pure: they import
// internal/model for ObjectType only and carry no parser or LSP internals.
package workspace

import (
	"path"
	"sort"
	"strings"

	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
)

// UnresolvedReason classifies why an edge could not be resolved to a definition.
// The zero value is ReasonNoTarget (a literal name that matched nothing), which
// is the safest default for an explicitly constructed Unresolved result.
type UnresolvedReason int

const (
	// ReasonNoTarget indicates the literal target name matched no object in
	// the workspace under the applicable library/steplib chain.
	ReasonNoTarget UnresolvedReason = iota
	// ReasonDynamic indicates the target was a variable or runtime expression
	// and cannot be statically resolved. The call site is preserved so it
	// appears in find-references and outline rather than disappearing.
	ReasonDynamic
)

// Resolution represents the outcome of resolving a single call or dependency
// edge to its definition(s) in the workspace.
//
// The zero value is a valid, well-defined state: unresolved with reason
// ReasonNoTarget (i.e., a literal target name that matched nothing). Prefer
// the constructors Resolved, Unresolved, and Ambiguous for clarity.
//
// The discriminant field (outcome) is unexported; use the predicate methods
// IsResolved, IsUnresolved, IsAmbiguous, and IsDynamic as the public API.
// This keeps callsites readable and insulates them from the enum internals.
type Resolution struct {
	// outcome is the unexported discriminant; predicates are the public API.
	outcome ResolutionOutcome
	// Path is set for Resolved outcomes: workspace-relative path to the definition.
	Path string
	// Type is set for Resolved outcomes: ObjectType of the resolved target.
	Type model.ObjectType
	// Reason is set for Unresolved outcomes: why the binding failed.
	Reason UnresolvedReason
	// Candidates is set for Ambiguous outcomes: workspace-relative paths of
	// all definitions that matched (at least 2 for a genuine ambiguity).
	Candidates []string
}

// ResolutionOutcome is the discriminant for a Resolution value. The zero value
// is OutcomeUnresolved, so a zero Resolution correctly represents an
// unresolved reference — no special initialization required.
type ResolutionOutcome int

const (
	// OutcomeUnresolved: the reference could not be bound to a definition.
	// This is the zero value, matching the zero Resolution.
	OutcomeUnresolved ResolutionOutcome = iota
	// OutcomeResolved: the reference resolved to exactly one definition.
	OutcomeResolved
	// OutcomeAmbiguous: the reference matched multiple definitions in the
	// steplib chain, indicating a name collision across libraries.
	OutcomeAmbiguous
)

// Resolved constructs a Resolution for a successful single-definition binding.
// path must be workspace-relative; typ must be the object's classified ObjectType.
func Resolved(path string, typ model.ObjectType) Resolution {
	return Resolution{
		outcome: OutcomeResolved,
		Path:    path,
		Type:    typ,
	}
}

// Unresolved constructs a Resolution for an edge that could not be bound to a
// definition. reason distinguishes why: ReasonNoTarget (literal name, no match
// in the steplib chain) or ReasonDynamic (variable/expression target).
func Unresolved(reason UnresolvedReason) Resolution {
	return Resolution{
		outcome: OutcomeUnresolved,
		Reason:  reason,
	}
}

// Ambiguous constructs a Resolution for an edge whose literal target name
// matched more than one definition in the steplib chain. candidates holds the
// workspace-relative paths of all matches; the caller must supply at least 2.
func Ambiguous(candidates []string) Resolution {
	return Resolution{
		outcome:    OutcomeAmbiguous,
		Candidates: candidates,
	}
}

// IsResolved reports whether this resolution bound to exactly one definition.
func (r Resolution) IsResolved() bool {
	return r.outcome == OutcomeResolved
}

// IsUnresolved reports whether this resolution failed to bind to any definition.
// Both ReasonNoTarget and ReasonDynamic produce an unresolved outcome.
func (r Resolution) IsUnresolved() bool {
	return r.outcome == OutcomeUnresolved
}

// IsAmbiguous reports whether this resolution matched multiple definitions.
func (r Resolution) IsAmbiguous() bool {
	return r.outcome == OutcomeAmbiguous
}

// IsDynamic reports whether the edge target was a variable or runtime expression
// (ReasonDynamic) rather than a literal name. Only unresolved edges can be
// dynamic; resolved and ambiguous outcomes always return false. Use this to
// distinguish "could not find the module" from "target is not statically known".
func (r Resolution) IsDynamic() bool {
	return r.outcome == OutcomeUnresolved && r.Reason == ReasonDynamic
}

// isZeroRange reports whether r is the zero Range (all four fields zero).
// A zero Target on an EdgePerforms entry indicates the subroutine definition
// was not located in-file by the extractor; the edge is left for later tasks.
// Struct equality is used rather than four separate field comparisons because
// model.Range and model.Position contain only comparable int fields.
func isZeroRange(r model.Range) bool {
	return r == model.Range{}
}

// objectIdentity derives an object's name and owning library from its
// workspace-relative file path and the resolution library map.
//
// name is the filename stem, uppercased (Natural identifiers are case-insensitive).
// E.g. "APP/MYSUB.NSN" → "MYSUB"; "app/mysub.nsn" → "MYSUB".
//
// library is the Name of the config.Library whose Path is the longest
// slash-separated prefix of relPath (tie-break: declared order in
// cfg.Resolution.Libraries). Matching is segment-boundary-strict: "APP" matches
// "APP/X.NSN" but not "APPLE/X.NSN". When no library map is configured or no
// path matches, library is empty — the caller treats that as the flat namespace
// (OQ-3). relPath must use forward-slash separators (index keys are slash-relative;
// use path, not filepath, for all operations on these keys).
//
// ObjectType filtering (Task 3 / FR-10) is the caller's responsibility; this
// helper intentionally handles only name and library derivation.
//
// OQ-3 (FR-10, FR-16).
func objectIdentity(relPath string, cfg *config.Config) (name, library string) {
	// path.Base / path.Ext operate on slash-separated keys, not OS file paths.
	base := path.Base(relPath)
	ext := path.Ext(base)
	name = strings.ToUpper(strings.TrimSuffix(base, ext))

	if cfg == nil || len(cfg.Resolution.Libraries) == 0 {
		return name, ""
	}

	var longestMatch config.Library
	var longestLen int

	for _, lib := range cfg.Resolution.Libraries {
		// Segment-boundary rule: lib.Path must be followed by '/' or be the full
		// relPath. This prevents "APP" from matching "APPLE/X.NSN".
		if strings.HasPrefix(relPath, lib.Path) {
			if len(relPath) == len(lib.Path) || relPath[len(lib.Path)] == '/' {
				if len(lib.Path) > longestLen {
					longestMatch = lib
					longestLen = len(lib.Path)
				}
			}
		}
	}

	if longestLen > 0 {
		return name, longestMatch.Name
	}

	return name, ""
}

// ResolutionSet holds every resolved edge in the workspace, keyed by the call
// site: (referencing file path, edge Source range). Entries are populated by
// Resolve and queried via Get or iterated via All.
//
// Each entry preserves the caller's context through the Resolution fields
// (Path, Type, Reason, Candidates) so callers never need to re-look up the
// originating edge. Two lookup axes are supported:
//   - Get(filePath, source) — O(1) point lookup for a specific call site.
//   - All() — full scan, used for diagnostics and testing.
//
// The set also holds ambiguity diagnostics produced during resolution (OQ-2(a) /
// Task 7 / FR-5, FR-31): when a literal target name matches multiple definitions
// in a flat namespace (no library map), an ambiguity diagnostic is recorded and
// exposed via DiagnosticsFor(filePath). These are distinct from parser diagnostics
// stored in model.FileAnalysis.Diagnostics — the LSP server will merge them into
// the referencing file's publishDiagnostics. Resolve does NOT write into the index's
// FileAnalysis values, keeping it idempotent and safe to recompute from cached edges
// on every load without a cache-format version bump (OQ-1).
//
// The set is not safe for concurrent mutation; treat it as read-only after
// Resolve returns.
type ResolutionSet struct {
	// entries maps (filePath, sourceRange) to the resolution outcome.
	// The key is a composite of the referencing file's path and the edge's Source range.
	entries map[resolutionKey]Resolution

	// ambigDiagnostics maps referencing file path to the list of resolution-produced
	// ambiguity diagnostics for that file. Populated only in the flat-namespace path
	// (no library map): when a literal CALLNAT target matches more than one candidate,
	// one diagnostic per such call site is appended here (Task 7 / OQ-2(a)).
	// Never written by the library-map path — library-mapped multi-match is resolved
	// via the steplib chain rather than flagged as ambiguous.
	ambigDiagnostics map[string][]model.Diagnostic
}

// resolutionKey uniquely identifies an edge by its call site (file + source range).
// Two edges are considered the same if they occur at the same source location.
type resolutionKey struct {
	filePath string
	source   model.Range
}

// Get retrieves the Resolution for a given file and edge source range.
// Returns the Resolution and ok=true if found; ok=false if not found.
func (rs *ResolutionSet) Get(filePath string, source model.Range) (Resolution, bool) {
	if rs == nil || rs.entries == nil {
		return Resolution{}, false
	}
	res, ok := rs.entries[resolutionKey{filePath: filePath, source: source}]
	return res, ok
}

// All returns all resolutions in the set as a slice.
// Used for testing and diagnostics that need to iterate all outcomes.
func (rs *ResolutionSet) All() []Resolution {
	if rs == nil || rs.entries == nil {
		return nil
	}
	results := make([]Resolution, 0, len(rs.entries))
	for _, res := range rs.entries {
		results = append(results, res)
	}
	return results
}

// DiagnosticsFor returns the resolution-produced diagnostics for the given referencing
// file. These are warning-severity ambiguity diagnostics emitted by Resolve when a
// literal CALLNAT target matches more than one definition in a flat namespace (no library
// map configured). They are produced exclusively in the flat-namespace path: a library-map
// multi-match is disambiguated via the steplib chain and never produces a diagnostic here.
//
// These diagnostics are distinct from parser diagnostics in model.FileAnalysis.Diagnostics.
// The LSP server will merge them into the referencing file's publishDiagnostics (OQ-2(a)).
// Resolve does NOT mutate the index's FileAnalysis values — ResolutionSet is safe to
// recompute from cached edges on every load without a cache-format bump (OQ-1).
//
// Returns nil if no ambiguity diagnostics were produced for filePath.
func (rs *ResolutionSet) DiagnosticsFor(filePath string) []model.Diagnostic {
	if rs == nil || rs.ambigDiagnostics == nil {
		return nil
	}
	return rs.ambigDiagnostics[filePath]
}

// filterByType returns the subset of candidates whose ObjectType equals typ.
// It is used by the call-graph resolver to restrict a name-index lookup to the
// specific Natural object kind expected by each edge kind:
//   - EdgeCalls (CALLNAT)          → ObjectSubprogram   (Task 5)
//   - EdgeIncludes (INCLUDE)       → ObjectCopycode     (Task 6)
//   - EdgeNavigatesTo (FETCH/RUN)  → ObjectProgram      (Task 8)
//   - EdgePerforms (external)      → ObjectSubroutine   (Task 9)
//
// The returned slice is a fresh allocation; it never aliases candidates.
// An empty input produces nil (not an empty slice).
func filterByType(candidates []Candidate, typ model.ObjectType) []Candidate {
	var out []Candidate
	for _, c := range candidates {
		if c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

// buildSearchChain constructs the steplib search chain for a given current library.
//
// The chain is always: [currentLibrary, declaredSteplib1, ..., declaredSteplibN, SYSTEM].
// SYSTEM is appended implicitly as the terminal library at the end of every chain.
//
// Non-transitive (OQ-5): only the current library's own declared steplibs are added;
// a steplib's steplibs are never followed. This matches Natural's runtime behavior.
//
// If currentLibrary is empty (flat namespace — no library map configured or the
// referencing file's path matched no declared library), an empty slice is returned.
// Callers must treat an empty chain as the flat-namespace path (no chain filtering).
func buildSearchChain(currentLibrary string, cfg *config.Config) []string {
	if currentLibrary == "" {
		return []string{}
	}

	chain := []string{currentLibrary}

	// Find the current library's declared steplibs
	for _, lib := range cfg.Resolution.Libraries {
		if lib.Name == currentLibrary {
			// Add declared steplibs in order
			chain = append(chain, lib.Steplibs...)
			break
		}
	}

	// Append the implicit SYSTEM terminal
	chain = append(chain, "SYSTEM")

	return chain
}

// resolveViaChain finds the first candidate in Natural's steplib search order.
//
// It iterates searchChain in order (current library → declared steplibs → SYSTEM)
// and for each chain library returns the first candidate whose Library field matches.
// A candidate whose Library is not present in the chain is unreachable and is never
// returned, even if it is the only candidate with the matching name.
//
// The walk is non-transitive (OQ-5): searchChain is pre-built by buildSearchChain
// and already encodes exactly which libraries are reachable from the caller; this
// function applies it without re-expanding steplibs.
//
// Returns nil when no chain library has a matching candidate.
func resolveViaChain(candidates []Candidate, searchChain []string) *Candidate {
	// Build a set of library names in the chain for O(1) lookup
	chainSet := make(map[string]bool)
	for _, lib := range searchChain {
		chainSet[lib] = true
	}

	// Iterate through candidates in order; the name index already sorts them by Path
	// which is deterministic. For each library in the search chain, check if any
	// candidate belongs to that library.
	for _, chainLib := range searchChain {
		for _, cand := range candidates {
			if cand.Library == chainLib {
				return &cand
			}
		}
	}

	return nil
}

// resolveByName resolves a named call or dependency edge (CALLNAT, external
// PERFORM, FETCH/RUN navigation) against the workspace name index.
//
// Three modes are applied in strict priority order. The mode is determined by
// the edge's Library field and by whether a library map is configured:
//
// Mode 1 — Explicit-library bypass (edge.Library != ""):
//
//	Applies when the source text carries an explicit library identifier, e.g.
//	  RUN 'PROG' 'MYLIB'
//	Resolution is restricted to that library ONLY. If no candidate exists there,
//	return Unresolved(ReasonNoTarget) — never fall back to the steplib chain.
//	This matches Natural's runtime behavior for library-qualified navigation.
//
// Mode 2 — Steplib-chain resolution (library map present, edge.Library == ""):
//
//	A library map is configured ([resolution] libraries in .natural-lsp.toml).
//	The referencing file's current library is derived by longest-prefix match
//	of filePath against declared Library.Path values (objectIdentity).
//	buildSearchChain then yields: [currentLib, ...declaredSteplibs, SYSTEM].
//	resolveViaChain returns the first candidate whose Library appears in the
//	chain — implementing Natural's non-transitive, declared-order resolution
//	(OQ-5, FR-16).
//
//	Exception — OQ-3(a): if the referencing file's path matches NO declared
//	library (currentLibrary == ""), buildSearchChain returns an empty slice.
//	An empty chain means the caller is outside all declared library paths (an
//	undeclared path in a mapped workspace). Per OQ-3(a), such a caller falls
//	through to flat-namespace resolution rather than being left unresolved —
//	the contract of buildSearchChain makes this the only safe fallback.
//
// Mode 3 — Flat namespace (no library map, OR undeclared-path caller in Mode 2):
//
//	Both entry paths share a single code block (no duplication):
//	  - 1 candidate  → Resolved
//	  - >1 candidates → Ambiguous + one ambiguity diagnostic in ambigDiagnostics
//	    (OQ-2(a), FR-5, FR-31)
//	  - 0 candidates → Unresolved(ReasonNoTarget), no diagnostic (FR-17)
//
// expectedType restricts resolution to a specific ObjectType per edge kind:
//   - EdgeCalls (CALLNAT)         → ObjectSubprogram
//   - EdgeNavigatesTo (FETCH/RUN) → ObjectProgram
//   - EdgePerforms (external)     → ObjectExternalSubroutine
//   - EdgeIncludes (INCLUDE)      → ObjectCopycode
func resolveByName(
	targetName string,
	expectedType model.ObjectType,
	filePath string,
	edge model.EdgeEntry,
	nameIndex map[string][]Candidate,
	cfg *config.Config,
	ambigDiagnostics map[string][]model.Diagnostic,
) Resolution {
	// R2 guard (FR-43 defense-in-depth): reject empty target names before any
	// index lookup. An empty name is never a valid Natural identifier, and a
	// lookup of "" would match files whose stem is empty — a pathological case
	// that must not produce a false Resolved outcome. This is defense-in-depth:
	// the extractor should never produce an empty TargetName, but the resolver
	// must be safe regardless.
	upTargetName := strings.ToUpper(targetName)
	if upTargetName == "" {
		return Unresolved(ReasonNoTarget)
	}

	// Filter candidates to the expected ObjectType (e.g. ObjectSubprogram for
	// CALLNAT). Type-filtering happens once here; all three modes below operate
	// on the filtered slice.
	allCandidates := nameIndex[upTargetName]
	candidates := filterByType(allCandidates, expectedType)

	// --- Mode 1: Explicit-library bypass ---
	// The edge carries a library identifier from the source text (e.g. RUN
	// 'PROG' 'MYLIB'). Resolve against that library exclusively; do not fall
	// back to the steplib chain if the target is not found there.
	if edge.Library != "" {
		for _, cand := range candidates {
			if cand.Library == edge.Library {
				return Resolved(cand.Path, cand.Type)
			}
		}
		return Unresolved(ReasonNoTarget)
	}

	// --- Mode 2: Steplib-chain resolution (library map configured) ---
	// A library map transforms the workspace into a multi-library namespace.
	// Derive the referencing file's current library and build its search chain.
	if cfg != nil && len(cfg.Resolution.Libraries) > 0 {
		_, currentLibrary := objectIdentity(filePath, cfg)
		searchChain := buildSearchChain(currentLibrary, cfg)

		if len(searchChain) > 0 {
			// Caller is under a declared library: walk the chain and return the
			// first match. A non-nil result is always from the earliest chain
			// position that has a matching candidate (OQ-5, FR-16).
			if matched := resolveViaChain(candidates, searchChain); matched != nil {
				return Resolved(matched.Path, matched.Type)
			}
			// No candidate reachable via this caller's chain (OQ-5: out-of-chain
			// candidates are invisible even if they exist in the name index).
			return Unresolved(ReasonNoTarget)
		}
		// Empty chain: caller's path matched no declared library (OQ-3(a)).
		// Fall through to flat-namespace resolution below — the same path taken
		// when no library map is configured at all. This avoids leaving files in
		// unconfigured workspace subdirectories silently unresolved.
	}

	// --- Mode 3: Flat namespace ---
	// Reached when: (a) no library map is configured, OR (b) a library map is
	// configured but the referencing file's path matches no declared library
	// (OQ-3(a) fallback from Mode 2). Both entry paths share this single block;
	// there is no duplicate ambiguity logic.
	switch len(candidates) {
	case 1:
		return Resolved(candidates[0].Path, candidates[0].Type)
	case 0:
		// Zero matches: literal name that exists nowhere in the workspace.
		// FR-17: this is a modeled outcome, not a diagnostic — callers that
		// reference a missing module are preserved in find-references/outline.
		return Unresolved(ReasonNoTarget)
	default:
		// Multiple matches: ambiguous in a flat namespace. Record a warning
		// diagnostic at the call-site source range (OQ-2(a), FR-5, FR-31).
		// Candidate paths are sorted for deterministic output and stable
		// golden-file tests (SHA-256 cache contract, downstream lsp-graph).
		paths := make([]string, len(candidates))
		for i, cand := range candidates {
			paths[i] = cand.Path
		}
		sort.Strings(paths)

		ambigDiagnostics[filePath] = append(ambigDiagnostics[filePath], model.Diagnostic{
			Message:  formatAmbiguityMessage(targetName, paths),
			Severity: model.DiagnosticWarning,
			Range:    edge.Source,
		})
		return Ambiguous(paths)
	}
}

// Resolve walks every file's edges in the index and produces a ResolutionSet
// keyed by (referencing file path, edge Source range).
//
// Each Resolution preserves the caller's context so that unresolvable or
// dynamic references still appear in find-references and outline rather than
// disappearing (FR-30, M-6).
//
// Currently handled edge kinds (Task 4–9):
//   - EdgeCallsDynamic, EdgeNavigatesToDynamic — variable/expression targets
//     that cannot be statically resolved → Unresolved(ReasonDynamic).
//   - EdgePerforms with a non-zero Target range — the extractor located the
//     DEFINE SUBROUTINE block in-file → Resolved to the referencing file.
//   - EdgePerforms with a zero Target range — external subroutine fallback,
//     resolved via resolveByName with ObjectExternalSubroutine filter.
//   - EdgeCalls (CALLNAT) — static literal targets, resolved via resolveByName
//     with ObjectSubprogram filter.
//   - EdgeNavigatesTo (FETCH/RUN) — static literal program targets, resolved
//     via resolveByName with ObjectProgram filter. Supports explicit library
//     bypass via edge.Library field.
//   - EdgeIncludes (INCLUDE) — compile-time literal copycode targets (FR-13),
//     resolved via resolveByName with ObjectCopycode filter. INCLUDE targets
//     are always literals; EdgeIncludesDynamic does not exist. An unavailable
//     copycode → Unresolved(ReasonNoTarget); no diagnostic is emitted (FR-17).
//
// Ambiguity in flat namespace (no library map): when a literal target matches
// >1 definition, recorded as Ambiguous outcome with a diagnostic.
//
// OQ-5: steplib chain is non-transitive (do not follow a steplib's own steplibs).
func Resolve(idx *Index, cfg *config.Config) *ResolutionSet {
	rs := &ResolutionSet{
		entries:          make(map[resolutionKey]Resolution),
		ambigDiagnostics: make(map[string][]model.Diagnostic),
	}

	// Build the name index once before the edge loop for O(1) per-edge lookups.
	// Without this, we'd call LookupByName O(files * edges) times.
	nameIndex := idx.buildNameIndex(cfg)

	// Iterate over every file in the index and process its edges.
	idx.ForEach(func(filePath string, fa model.FileAnalysis) {
		// Process each edge in the file.
		for _, edge := range fa.Edges {
			var resolution Resolution

			// Handle each edge kind.
			switch edge.Kind {
			case model.EdgeCallsDynamic, model.EdgeNavigatesToDynamic:
				// Variable targets cannot be statically resolved.
				resolution = Unresolved(ReasonDynamic)

			case model.EdgePerforms:
				// PERFORM has two cases:
				// 1. Non-zero Target range: inline match found in-file.
				//    Resolve to the referencing file.
				// 2. Zero Target range: no inline match, fall back to external
				//    subroutine (.NSS) resolution via resolveByName.
				if !isZeroRange(edge.Target) {
					// Inline match: the DEFINE SUBROUTINE was found in this file.
					resolution = Resolved(filePath, fa.ObjectType)
				} else {
					// External fallback: resolve to ObjectExternalSubroutine.
					resolution = resolveByName(
						edge.TargetName,
						model.ObjectExternalSubroutine,
						filePath,
						edge,
						nameIndex,
						cfg,
						rs.ambigDiagnostics,
					)
				}

			case model.EdgeCalls:
				// Static CALLNAT with a literal target name.
				// Resolve to ObjectSubprogram via resolveByName.
				resolution = resolveByName(
					edge.TargetName,
					model.ObjectSubprogram,
					filePath,
					edge,
					nameIndex,
					cfg,
					rs.ambigDiagnostics,
				)

			case model.EdgeNavigatesTo:
				// Static FETCH/RUN with a literal program target.
				// Resolve to ObjectProgram via resolveByName.
				// edge.Library may be non-empty (explicit library bypass).
				resolution = resolveByName(
					edge.TargetName,
					model.ObjectProgram,
					filePath,
					edge,
					nameIndex,
					cfg,
					rs.ambigDiagnostics,
				)

			case model.EdgeIncludes:
				// INCLUDE binds to a copycode (.NSC) object at compile time.
				// Resolution uses resolveByName with ObjectCopycode as the expected type.
				//
				// INCLUDE targets are always literal names — the parser never produces an
				// EdgeIncludesDynamic kind — so ReasonDynamic is never applicable here.
				// edge.Library is always empty for INCLUDE (there is no library-id syntax).
				//
				// An unavailable copycode (TargetName matches nothing in the index) →
				// Unresolved(ReasonNoTarget). This is a modeled outcome (FR-13/FR-17):
				// no diagnostic is emitted to the referencing file.
				resolution = resolveByName(
					edge.TargetName,
					model.ObjectCopycode,
					filePath,
					edge,
					nameIndex,
					cfg,
					rs.ambigDiagnostics,
				)

			default:
				// Other edge kinds not handled in this slice.
				continue
			}

			// Store the resolution keyed by (filePath, edgeSourceRange).
			key := resolutionKey{
				filePath: filePath,
				source:   edge.Source,
			}
			rs.entries[key] = resolution
		}
	})

	return rs
}

// formatAmbiguityMessage constructs a diagnostic message for an ambiguous
// reference in a flat namespace. The message names the target and lists all
// candidate locations in deterministic (sorted) order.
//
// Sorting is performed inside this function so output is stable regardless of
// the order in which the name index returns candidates. Callers need not pre-sort.
//
// Example output: "ambiguous reference 'DUP': matches LIBA/DUP.NSN, LIBB/DUP.NSN"
func formatAmbiguityMessage(targetName string, candidates []string) string {
	sorted := make([]string, len(candidates))
	copy(sorted, candidates)
	sort.Strings(sorted)
	return "ambiguous reference '" + targetName + "': matches " + strings.Join(sorted, ", ")
}
