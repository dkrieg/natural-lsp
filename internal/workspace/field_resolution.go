// Package workspace: field-resolution helpers for cross-file field binding.
package workspace

import (
	"strings"

	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// ResolveDataAreaField resolves a variable name within a data-area USING reference.
// It takes a variable name and a data-area reference from a DEFINE DATA section,
// resolves the data-area object via the steplib chain, and returns the matching
// field's NameRange within that data area.
//
// If the data area cannot be resolved (outside the steplib chain) or the field
// is not found in the resolved data area, this returns a zero Range (FR-17).
//
// This function reuses the feature-07 steplib chain (non-transitive) to locate
// the data-area object.
func ResolveDataAreaField(
	variableName string,
	dataAreaRef model.DataAreaRef,
	idx *Index,
	referencingPath string,
	cfg *config.Config,
) model.Range {
	rng, _ := ResolveDataAreaFieldLocation(variableName, dataAreaRef, idx, referencingPath, cfg)
	return rng
}

// ResolveDataAreaFieldLocation resolves a variable within a USING data-area
// reference AND returns the workspace-relative path of the data-area object that
// the steplib chain selected.
//
// It is the location-aware sibling of ResolveDataAreaField: both share the same
// chain resolution (buildSearchChain + resolveViaChain, non-transitive), so the
// returned (fieldRange, objectPath) pair is always consistent — the field range
// is looked up in exactly the object whose path is returned. Callers that need the
// target file's URI (e.g. the LSP definition provider) MUST use objectPath rather
// than a separate, unfiltered idx.LookupByName candidates[0] pick, which bypasses
// the chain and can point at an unreachable same-named object in a non-chain
// library.
//
// If the data area is not reachable via the chain (or the field is not found), it
// returns a zero Range and an empty objectPath (FR-17).
func ResolveDataAreaFieldLocation(
	variableName string,
	dataAreaRef model.DataAreaRef,
	idx *Index,
	referencingPath string,
	cfg *config.Config,
) (fieldRange model.Range, objectPath string) {
	dataAreaCandidate := resolveDataAreaCandidate(dataAreaRef.Name, idx, referencingPath, cfg)
	if dataAreaCandidate == nil {
		return model.Range{}, ""
	}

	// Get the data-area file's FileAnalysis to access its Definitions
	dataAreaAnalysis, ok := idx.Get(dataAreaCandidate.Path)
	if !ok {
		return model.Range{}, ""
	}

	// Find the field within the data area's definitions
	return findFieldInDefinitions(variableName, dataAreaAnalysis.Definitions), dataAreaCandidate.Path
}

// resolveDataAreaCandidate locates the data-area object (.NSL/.NSA/.NSG) named
// dataAreaName reachable from referencingPath, applying Natural's steplib chain
// (non-transitive) when a library map is configured, or a flat-namespace
// first-candidate pick otherwise. Returns nil when no reachable candidate exists.
func resolveDataAreaCandidate(dataAreaName string, idx *Index, referencingPath string, cfg *config.Config) *Candidate {
	// Data areas can be .NSL (ObjectLocalDataArea), .NSA (ObjectParameterDataArea),
	// or .NSG (ObjectGlobalDataArea).
	candidates := idx.LookupByName(dataAreaName, model.ObjectLocalDataArea, cfg)
	if len(candidates) == 0 {
		candidates = idx.LookupByName(dataAreaName, model.ObjectParameterDataArea, cfg)
	}
	if len(candidates) == 0 {
		candidates = idx.LookupByName(dataAreaName, model.ObjectGlobalDataArea, cfg)
	}
	return resolveCandidateViaChain(candidates, referencingPath, cfg)
}

// ResolveDDMPath resolves a DDM name to the workspace-relative path of its .NSD
// object, applying Natural's steplib chain (non-transitive) from referencingPath
// when a library map is configured, or a flat-namespace first-candidate pick
// otherwise. DDMs share the object namespace with Adabas views/data areas, so the
// same chain rules apply.
//
// This is the chain-aware replacement for a raw idx.LookupByName(...).candidates[0]
// pick: it never returns a same-named DDM in a library outside the caller's chain
// (unreachable-exclusion). Returns "" when no reachable .NSD matches (FR-17).
func ResolveDDMPath(ddmName string, idx *Index, referencingPath string, cfg *config.Config) string {
	candidates := idx.LookupByName(ddmName, model.ObjectDDM, cfg)
	if cand := resolveCandidateViaChain(candidates, referencingPath, cfg); cand != nil {
		return cand.Path
	}
	return ""
}

// ResolveDDMFieldLocation resolves a field within a DDM and returns both the field's
// NameRange and the workspace-relative path of the .NSD object that the steplib chain
// selected. This is the DDM-field analog of ResolveDataAreaFieldLocation.
//
// It resolves the DDM name via the steplib chain, looks up the field within the resolved
// DDM's Definitions, and returns the field's NameRange and the DDM's path. Both values
// come from the same steplib-chain-selected DDM, ensuring consistency.
//
// If the DDM cannot be resolved (outside the steplib chain), the field is not found,
// or the DDM is a TYPE: SQL DDM (no parsed fields), it returns a zero Range and an
// empty DDMPath (FR-17, FR-43).
func ResolveDDMFieldLocation(
	fieldName string,
	ddmName string,
	idx *Index,
	referencingPath string,
	cfg *config.Config,
) (fieldRange model.Range, ddmPath string) {
	// Resolve the DDM name to its .NSD path via the steplib chain
	ddmPath = ResolveDDMPath(ddmName, idx, referencingPath, cfg)
	if ddmPath == "" {
		return model.Range{}, ""
	}

	// Fetch the DDM file's FileAnalysis
	ddmFA, ok := idx.Get(ddmPath)
	if !ok {
		return model.Range{}, ""
	}

	// Find the field within the DDM's definitions
	fieldRange = findFieldInDefinitions(fieldName, ddmFA.Definitions)
	if fieldRange == (model.Range{}) {
		// Field not found in the DDM (or TYPE: SQL DDM with no parsed Definitions)
		return model.Range{}, ""
	}

	return fieldRange, ddmPath
}

// ResolveDDMFieldType resolves a field's TYPE from a DDM. It locates the DDM via the
// steplib chain and returns the field's Type string if found, else an empty string.
// Returns "" when the DDM cannot be resolved, the field is absent, or the DDM is TYPE: SQL.
func ResolveDDMFieldType(
	fieldName string,
	ddmName string,
	idx *Index,
	referencingPath string,
	cfg *config.Config,
) string {
	// Resolve the DDM name to its .NSD path via the steplib chain
	ddmPath := ResolveDDMPath(ddmName, idx, referencingPath, cfg)
	if ddmPath == "" {
		return ""
	}

	// Fetch the DDM file's FileAnalysis
	ddmFA, ok := idx.Get(ddmPath)
	if !ok {
		return ""
	}

	// Find the field and extract its type
	def := findDefinitionByNameInDefs(fieldName, ddmFA.Definitions)
	if def == nil {
		return ""
	}

	return def.Type
}

// findDefinitionByNameInDefs recursively searches for a definition by name in a list
// of DataDefinitions, handling group-qualified names. Returns nil if not found.
// Helper for ResolveDDMFieldType.
func findDefinitionByNameInDefs(variableName string, defs []model.DataDefinition) *model.DataDefinition {
	upperName := strings.ToUpper(variableName)

	// Handle group-qualified names like #GROUP.FIELD
	if idx := strings.LastIndex(upperName, "."); idx != -1 {
		groupName := upperName[:idx]
		fieldName := upperName[idx+1:]

		// Find the group at level 1
		for i := range defs {
			if defs[i].Level == 1 && strings.ToUpper(defs[i].Name) == groupName && len(defs[i].Children) > 0 {
				// Search within the group's children
				return findDefinitionByNameInDefs(fieldName, defs[i].Children)
			}
		}
		// Group not found
		return nil
	}

	// Bare name lookup: search recursively for the first matching name
	for i := range defs {
		if strings.ToUpper(defs[i].Name) == upperName && defs[i].Name != "" {
			return &defs[i]
		}
	}

	// If not found at this level, recurse into children
	for i := range defs {
		if len(defs[i].Children) > 0 {
			if def := findDefinitionByNameInDefs(variableName, defs[i].Children); def != nil {
				return def
			}
		}
	}

	return nil
}

// resolveCandidateViaChain selects the reachable candidate from candidates using
// the steplib chain when a library map is configured, or the first candidate
// (flat namespace) otherwise. Returns nil when candidates is empty or nothing in
// the chain matches (unreachable). Shared by data-area and DDM resolution.
func resolveCandidateViaChain(candidates []Candidate, referencingPath string, cfg *config.Config) *Candidate {
	if len(candidates) == 0 {
		return nil
	}

	// Derive the caller's current library and build its (non-transitive) chain.
	_, currentLibrary := objectIdentity(referencingPath, cfg)
	searchChain := buildSearchChain(currentLibrary, cfg)

	if len(searchChain) > 0 {
		// Library map configured: resolve via the steplib chain. A candidate in a
		// library outside the chain is unreachable and is never returned.
		return resolveViaChain(candidates, searchChain)
	}

	// Flat namespace or caller not in a declared library: all objects have
	// Library="" and are equally reachable; the first candidate (Path-sorted by
	// LookupByName) is returned.
	return &candidates[0]
}

// findFieldInDefinitions recursively searches for a field by name within a list of
// DataDefinitions. It handles bare names, group-qualified names (#GROUP.FIELD),
// and REDEFINE subfields.
//
// The search is case-insensitive and returns the first matching field's NameRange.
// If no match is found, it returns a zero Range.
func findFieldInDefinitions(variableName string, defs []model.DataDefinition) model.Range {
	upperName := strings.ToUpper(variableName)

	// Handle group-qualified names like #GROUP.FIELD
	// A dot indicates a qualified lookup: look for GROUP at level 1, then FIELD within its children
	if idx := strings.LastIndex(upperName, "."); idx != -1 {
		groupName := upperName[:idx]
		fieldName := upperName[idx+1:]

		// Find the group at level 1
		for _, def := range defs {
			if def.Level == 1 && strings.ToUpper(def.Name) == groupName && len(def.Children) > 0 {
				// Search within the group's children
				return findFieldInDefinitions(fieldName, def.Children)
			}
		}
		// Group not found, return empty
		return model.Range{}
	}

	// Bare name lookup: search all definitions recursively for the first matching name
	for _, def := range defs {
		if strings.ToUpper(def.Name) == upperName && def.Name != "" {
			return def.NameRange
		}
	}

	// If not found at this level, recurse into children to catch nested/REDEFINE subfields
	for _, def := range defs {
		if len(def.Children) > 0 {
			if childRange := findFieldInDefinitions(variableName, def.Children); childRange != (model.Range{}) {
				return childRange
			}
		}
	}

	return model.Range{}
}
