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
	// Get the current library from the referencing file's path
	_, currentLibrary := objectIdentity(referencingPath, cfg)

	// Build the steplib chain from the current library
	searchChain := buildSearchChain(currentLibrary, cfg)

	// Look up the data-area object by its name
	// Data areas can be .NSL (ObjectLocalDataArea), .NSA (ObjectParameterDataArea), or .NSG (ObjectGlobalDataArea)
	dataAreaCandidates := idx.LookupByName(dataAreaRef.Name, model.ObjectLocalDataArea, cfg)
	if len(dataAreaCandidates) == 0 {
		dataAreaCandidates = idx.LookupByName(dataAreaRef.Name, model.ObjectParameterDataArea, cfg)
	}
	if len(dataAreaCandidates) == 0 {
		dataAreaCandidates = idx.LookupByName(dataAreaRef.Name, model.ObjectGlobalDataArea, cfg)
	}

	if len(dataAreaCandidates) == 0 {
		// No data-area object found in any category
		return model.Range{}
	}

	// Resolve the data-area object: either via steplib chain (library map configured)
	// or directly in flat namespace (no library map, empty current library)
	var dataAreaCandidate *Candidate
	if len(searchChain) > 0 {
		// Library map configured: resolve via the steplib chain
		dataAreaCandidate = resolveViaChain(dataAreaCandidates, searchChain)
	} else {
		// Flat namespace or caller not in a declared library: use the first candidate.
		// In a flat namespace, all objects have Library="" so they're all equally reachable.
		// When there are multiple candidates with the same name in flat namespace, the
		// first one (by Path order from LookupByName sorting) is returned.
		dataAreaCandidate = &dataAreaCandidates[0]
	}

	if dataAreaCandidate == nil {
		// Data area not found or not reachable via the chain (should not happen after above checks)
		return model.Range{}
	}

	// Get the data-area file's FileAnalysis to access its Definitions
	dataAreaAnalysis, ok := idx.Get(dataAreaCandidate.Path)
	if !ok {
		return model.Range{}
	}

	// Find the field within the data area's definitions
	fieldRange := findFieldInDefinitions(variableName, dataAreaAnalysis.Definitions)
	return fieldRange
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
