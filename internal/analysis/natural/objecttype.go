package natural

import (
	"natural-lsp/internal/model"
	"path/filepath"
	"strings"
)

// builtInExtensions maps the canonical upper-case Natural file extensions to their
// model.ObjectType. All 15 NaturalONE / SPoD object types recognized by this server
// are listed here. The map is initialized once at package load and must never be
// mutated at runtime.
//
// Extension notes:
//   - filepath.Ext(".gitignore") returns ".gitignore" (the whole name), which
//     normalizes to ".GITIGNORE" and falls through to ObjectUnknown — correct.
//   - filepath.Ext("file.backup.NSP") returns ".NSP" (last segment only) — correct.
var builtInExtensions = map[string]model.ObjectType{
	".NSP": model.ObjectProgram,
	".NSN": model.ObjectSubprogram,
	".NSS": model.ObjectExternalSubroutine,
	".NSC": model.ObjectCopycode,
	".NSM": model.ObjectMap,
	".NSL": model.ObjectLocalDataArea,
	".NSG": model.ObjectGlobalDataArea,
	".NSA": model.ObjectParameterDataArea,
	".NSH": model.ObjectHelproutine,
	".NSD": model.ObjectDDM,
	".NS4": model.ObjectClass,
	".NS7": model.ObjectFunction,
	".NS3": model.ObjectDialog,
	".NS8": model.ObjectAdapter,
	".NST": model.ObjectText,
}

// normalizeExt returns the upper-case extension of path with its leading dot preserved
// (e.g. "customer.nsp" → ".NSP"). Returns "" if path has no extension.
// This is the canonical normalization step shared by classify and Analyze; it matches
// the form produced by config.normalizeExtensions so that classifier lookups are
// consistent with configured extension sets.
func normalizeExt(path string) string {
	return strings.ToUpper(filepath.Ext(path))
}

// classify maps a file path to its model.ObjectType using only the path's extension.
// It calls normalizeExt to normalize the extension to upper-case with a leading dot
// (e.g., "customer.nsp" → ".NSP"), then looks up the normalized extension in the custom
// map (if non-nil) first, then in builtInExtensions. Returns model.ObjectUnknown for no match.
//
// This function is content-independent: it never reads or depends on file content, only the
// path's extension. It is the core of FR-7 (object-type classification from extension).
func classify(path string, custom map[string]model.ObjectType) model.ObjectType {
	normalizedExt := normalizeExt(path)
	if normalizedExt == "" {
		return model.ObjectUnknown
	}

	// Check custom map first (if non-nil), allowing callers to override or extend
	// the built-in table without modifying it.
	if custom != nil {
		if objType, ok := custom[normalizedExt]; ok {
			return objType
		}
	}

	if objType, ok := builtInExtensions[normalizedExt]; ok {
		return objType
	}

	return model.ObjectUnknown
}
