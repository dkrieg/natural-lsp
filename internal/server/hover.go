package server

import (
	"fmt"
	"os"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
)

// buildModuleHover returns a Markdown hover card for a resolved module target.
// It takes the module's workspace-relative path, inbound-caller count, and
// outbound-dependency count directly from the index — callers must have a
// resolution outcome before calling this; use buildUnresolvedHover or
// buildDynamicHover for the two modeled-gap cases (FR-17).
//
// No I/O, no locks — a pure function.
func buildModuleHover(targetName string, targetType model.ObjectType, targetPath string, inboundCount, outboundCount int) string {
	label := objectTypeLabel(targetType)
	lines := []string{
		fmt.Sprintf("**%s** (%s)", targetName, label),
		targetPath,
		fmt.Sprintf("Inbound calls: %d", inboundCount),
		fmt.Sprintf("Outbound dependencies: %d", outboundCount),
	}
	return strings.Join(lines, "\n")
}

// buildUnresolvedHover returns a hover message for a literal call target that
// could not be located in the workspace (no match in the steplib chain).
// FR-17: this modeled gap is distinct from a dynamic target and is surfaced
// honestly — no fabricated metadata (no counts, no path).
// No I/O, no locks — a pure function.
func buildUnresolvedHover() string {
	return "Unresolved reference — target could not be located."
}

// buildAmbiguousHover returns a hover message for a call target whose literal
// name matched more than one object (a name collision across libraries in a
// flat namespace). Unlike buildUnresolvedHover, the target WAS located — just
// not uniquely — so an "unresolved" message would be misleading.
// FR-17: this modeled gap is surfaced honestly with no fabricated metadata
// (no counts, no path, and no candidate count/list here).
// No I/O, no locks — a pure function.
func buildAmbiguousHover() string {
	return "Ambiguous reference — matches multiple objects."
}

// buildDynamicHover returns a hover message for a dynamic call target (a
// variable operand such as CALLNAT #VAR, or a literal containing an '&'
// runtime-substitution placeholder). The target is determined at runtime and
// cannot be statically resolved.
// FR-17: this modeled gap is distinct from an unresolved literal and is
// surfaced honestly — no fabricated metadata (no counts, no path).
// No I/O, no locks — a pure function.
func buildDynamicHover() string {
	return "Dynamic call target — resolved at runtime."
}

// buildSubroutineHover returns a Markdown hover card for a subroutine/subprogram
// signature. It renders the routine name as a title line and a parameter list
// built from the provided DataDefinition slice, filtered to SectionKind=="parameter".
// Array dimensions are rendered as (lower:upper) or (lower:*) for unbounded.
// Group headers (DataDefinition with no Type but having Children) are shown as
// nesting parents with their children indented underneath, in declaration order.
// When there are no parameters, renders an explicit "no declared parameters" line
// rather than an empty card.
//
// No I/O, no locks — a pure function.
func buildSubroutineHover(name string, params []model.DataDefinition) string {
	lines := []string{
		fmt.Sprintf("**%s** (subroutine)", name),
	}

	// Filter to parameter-section definitions only
	var paramDefs []model.DataDefinition
	for _, def := range params {
		if def.SectionKind == "parameter" {
			paramDefs = append(paramDefs, def)
		}
	}

	// If no parameters, render the "no declared parameters" message
	if len(paramDefs) == 0 {
		lines = append(lines, "No declared parameters")
		return strings.Join(lines, "\n")
	}

	// Build parameter list with indentation for nesting
	lines = append(lines, buildParamLines(paramDefs, 0)...)
	return strings.Join(lines, "\n")
}

// paramItem represents a single parameter (including group headers) with rendered
// name and type strings for use by both hover and signature help.
type paramItem struct {
	name    string // parameter name (including sigils)
	typeStr string // type + dimensions, or empty for group headers
}

// parameterInterface extracts the parameter interface from a list of definitions.
// It filters to SectionKind=="parameter", flattens group nesting into a single list,
// and renders each parameter as name + type/dims (matching hover's style).
// Group headers are included with empty typeStr; their children are enumerated separately.
// The result is in declaration order.
//
// This helper is shared by both hover (buildSubroutineHover) and signature help
// (buildSignatureInformation) so they agree on the parameter interface.
func parameterInterface(defs []model.DataDefinition) []paramItem {
	var params []paramItem

	// Filter to parameter-section definitions
	var paramDefs []model.DataDefinition
	for _, def := range defs {
		if def.SectionKind == "parameter" {
			paramDefs = append(paramDefs, def)
		}
	}

	// Flatten the parameter tree, visiting group headers and their children in order
	var visit func([]model.DataDefinition)
	visit = func(defList []model.DataDefinition) {
		for _, def := range defList {
			if def.Type == "" {
				// Group header (no type): add with empty typeStr
				params = append(params, paramItem{name: def.Name, typeStr: ""})
				// Recursively add children
				if len(def.Children) > 0 {
					visit(def.Children)
				}
			} else {
				// Scalar or array: render name + type + dims
				params = append(params, paramItem{
					name:    def.Name,
					typeStr: renderParamType(def),
				})
			}
		}
	}

	visit(paramDefs)
	return params
}

// renderParamType returns the "TYPE (dims)" string for a data-field definition.
// For a leaf field with a type, it appends the dimension list in "(lower:upper,...)"
// notation (unbounded upper renders as "*"). For a group header (Type == ""), it
// returns "" — callers are responsible for the header-only rendering path.
// No I/O, no locks — a pure function.
func renderParamType(def model.DataDefinition) string {
	if def.Type == "" {
		return ""
	}
	if len(def.Dimensions) == 0 {
		return def.Type
	}
	dims := make([]string, 0, len(def.Dimensions))
	for _, d := range def.Dimensions {
		if d.UpperUnbounded {
			dims = append(dims, fmt.Sprintf("%d:*", d.Lower))
		} else {
			dims = append(dims, fmt.Sprintf("%d:%d", d.Lower, d.Upper))
		}
	}
	return def.Type + " (" + strings.Join(dims, ",") + ")"
}

// buildParamLines recursively renders parameter definitions with indentation.
// indent controls the nesting level (0 for top-level, 1+ for children).
func buildParamLines(defs []model.DataDefinition, indent int) []string {
	var lines []string
	prefix := strings.Repeat("  ", indent) // 2 spaces per indent level

	for _, def := range defs {
		if def.Type == "" {
			// Group header (no type): render name only
			lines = append(lines, fmt.Sprintf("%s%s", prefix, def.Name))
			// Recursively render children with increased indentation
			if len(def.Children) > 0 {
				lines = append(lines, buildParamLines(def.Children, indent+1)...)
			}
		} else {
			// Scalar or array field: render name, type, and dimensions
			lines = append(lines, fmt.Sprintf("%s%s", prefix, def.Name))
			lines = append(lines, fmt.Sprintf("%s%s", prefix, renderParamType(def)))
		}
	}

	return lines
}

// countOutboundDependencies counts a file's outbound module dependencies for the
// module-hover card's "Outbound dependencies" line. Per the approved acceptance
// scope (OQ-B), only call/perform/include edges count as dependencies:
// EdgeCalls/EdgeCallsDynamic (CALLNAT), EdgePerforms (PERFORM), and EdgeIncludes
// (INCLUDE). FETCH/RUN navigation edges (EdgeNavigatesTo/EdgeNavigatesToDynamic)
// are stack transfers, not module dependencies, and are excluded. Data-access
// edges (reads/writes) live in a separate slice and are never in fa.Edges' call set.
// No I/O, no locks — a pure function.
func countOutboundDependencies(fa model.FileAnalysis) int {
	count := 0
	for _, edge := range fa.Edges {
		switch edge.Kind {
		case model.EdgeCalls, model.EdgeCallsDynamic, model.EdgePerforms, model.EdgeIncludes:
			count++
		}
	}
	return count
}

// objectTypeLabel maps an ObjectType to a human-readable label for hover display.
func objectTypeLabel(ot model.ObjectType) string {
	switch ot {
	case model.ObjectProgram:
		return "program"
	case model.ObjectSubprogram:
		return "subprogram"
	case model.ObjectExternalSubroutine:
		return "external subroutine"
	case model.ObjectCopycode:
		return "copycode"
	case model.ObjectMap:
		return "map"
	case model.ObjectLocalDataArea:
		return "local data area"
	case model.ObjectGlobalDataArea:
		return "global data area"
	case model.ObjectParameterDataArea:
		return "parameter data area"
	case model.ObjectHelproutine:
		return "helproutine"
	case model.ObjectDDM:
		return "ddm"
	case model.ObjectClass:
		return "class"
	case model.ObjectFunction:
		return "function"
	case model.ObjectDialog:
		return "dialog"
	case model.ObjectAdapter:
		return "adapter"
	case model.ObjectText:
		return "text"
	default:
		return "unknown"
	}
}

// buildDDMHover returns a Markdown hover card for a data-access site (DDM/view reference).
// When the referenced DDM file is indexed and contains Definitions, it renders the view name
// (title) and a list of field names/types from the DDM's Definitions.
// When the DDM is NOT indexed (physical metadata unavailable — Adabas/IMS), it renders only
// the view name and an honest "field details unavailable from source" line — no fabrication
// (Story 3 AC #2, FR-17).
// Empty viewName returns "" (no card) — handles the feature-08 record-form empty-Name gap.
// No I/O, no locks — a pure function.
func buildDDMHover(viewName string, ddmFA *model.FileAnalysis) string {
	// Empty viewName (feature-08 record-form gap) → no card
	if viewName == "" {
		return ""
	}

	// Build title line (view name in bold)
	lines := []string{
		fmt.Sprintf("**%s**", viewName),
	}

	// If DDM is not indexed or has no Definitions, render honest unavailability message
	if ddmFA == nil || len(ddmFA.Definitions) == 0 {
		lines = append(lines, "Field details unavailable from source.")
		return strings.Join(lines, "\n")
	}

	// DDM is indexed with Definitions: render field list
	lines = append(lines, buildParamLines(ddmFA.Definitions, 0)...)
	return strings.Join(lines, "\n")
}

// provideHover handles the textDocument/hover request (feature 12, T4).
// It is the LSP provider entry point: it decodes the cursor position from the params,
// looks up any reference at that position, resolves it, and returns a Markdown hover card
// with module metadata (Story 1), subroutine signature (Story 2), or honest unresolved/dynamic
// messages (FR-17, modeled gaps).
//
// For a resolved module reference (CALLNAT/FETCH/RUN):
// - compute inbound count via referenceSites (number of callers)
// - compute outbound count from the target file's Edges
// - render buildModuleHover with these counts
// For a resolved PERFORM to a subroutine:
// - locate the target routine's PARAMETER Definitions (inline or external per resolution)
// - render buildSubroutineHover with the parameters
// For unresolved/dynamic targets:
// - render buildUnresolvedHover / buildDynamicHover (no fabricated metadata)
// For no cursor target or I/O errors:
// - return nil, nil (FR-43 graceful degradation)
//
// Concurrency (F7): snapshots the idx/res pointers under a brief RLock and releases it
// before any index lookup or file I/O. Safety after the snapshot rests on the Index's own
// internal mutex and on ResolutionSet immutability (applyDocumentChange builds a fresh set
// and swaps the pointer under the write lock — build-then-publish — so this snapshot never
// races an in-place mutation).
func provideHover(hctx *handlerContext, params protocol.HoverParams) (*protocol.Hover, error) {
	// Guard: hctx must be initialized
	if hctx == nil {
		return nil, nil
	}

	// Acquire read lock to read idx/res safely (applyDocumentChange holds write lock when updating)
	hctx.idxResMu.RLock()
	idx, res := hctx.idx, hctx.res
	posEncoding := hctx.posEncoding
	root := hctx.root
	hctx.idxResMu.RUnlock()

	if idx == nil || res == nil {
		return nil, nil
	}

	// Convert LSP URI to workspace-relative path (forward-slash index key convention)
	absPath, relPath, err := uriToRelPath(root, params.TextDocument.URI)
	if err != nil {
		// URI outside workspace root — no hover
		return nil, nil
	}

	// Get the source file's analysis from the index
	sourceFA, ok := idx.Get(relPath)
	if !ok {
		// Source file not in index — no hover
		return nil, nil
	}

	// Read the source file content for position conversion
	sourceContent, err := os.ReadFile(absPath)
	if err != nil {
		// Can't read source; no hover
		return nil, nil
	}

	// Convert protocol position (0-based) to model position (1-based)
	cursorPos := fromProtocolPosition(params.Position, string(sourceContent), posEncoding)

	// Find the edge (data-access, or variable ref) at the cursor position
	edge, dataAccess, _ := findCursorTarget(sourceFA, cursorPos, string(sourceContent), hctx.az)
	if edge == nil && dataAccess == nil {
		// No edge or data-access at cursor position — no hover
		return nil, nil
	}

	// Handle EdgeEntry branch
	if edge != nil {
		// Look up the resolution for this edge
		resolution, ok := res.Get(relPath, edge.Source)
		if !ok {
			// Edge not found in resolution set — no hover
			return nil, nil
		}

		// Handle resolved case: single definition
		if resolution.IsResolved() {
			// Read the target file's analysis
			targetFA, ok := idx.Get(resolution.Path)
			if !ok {
				// Target file not in index (shouldn't happen after successful resolution)
				return nil, nil
			}

			// Check if this is an inline PERFORM (same file)
			normalizedResPath := paths.NormalizeKey(resolution.Path)
			if strings.EqualFold(normalizedResPath, relPath) {
				// Inline PERFORM: look for the matching DEFINE SUBROUTINE in Structure.Children
				if sourceFA.Structure != nil && sourceFA.Structure.Children != nil {
					targetName := strings.ToUpper(edge.TargetName)
					for _, child := range sourceFA.Structure.Children {
						if child.Kind == model.SymbolSubroutine && strings.EqualFold(child.Name, targetName) {
							// Found the inline subroutine; build subroutine hover
							hoverStr := buildSubroutineHover(edge.TargetName, sourceFA.Definitions)
							rng := toProtocolRange(edge.Source, string(sourceContent), posEncoding)
							return &protocol.Hover{
								Contents: &protocol.MarkupContent{
									Kind:  protocol.MarkupKindMarkdown,
									Value: hoverStr,
								},
								Range: &rng,
							}, nil
						}
					}
				}
			}

			// External PERFORM (Story 2 AC1): a PERFORM resolved to a separate
			// .NSS external subroutine renders the parameter-signature card, not
			// the module-metadata card. The parameters come from the RESOLVED
			// file's PARAMETER-section Definitions.
			if edge.Kind == model.EdgePerforms {
				hoverStr := buildSubroutineHover(edge.TargetName, targetFA.Definitions)
				rng := toProtocolRange(edge.Source, string(sourceContent), posEncoding)
				return &protocol.Hover{
					Contents: &protocol.MarkupContent{
						Kind:  protocol.MarkupKindMarkdown,
						Value: hoverStr,
					},
					Range: &rng,
				}, nil
			}

			// External module target: build module hover with counts
			inboundCount := len(referenceSites(idx, res, root, resolution.Path, edge.TargetName, resolution.Type, false, posEncoding))
			outboundCount := countOutboundDependencies(targetFA)

			hoverStr := buildModuleHover(edge.TargetName, resolution.Type, resolution.Path, inboundCount, outboundCount)
			rng := toProtocolRange(edge.Source, string(sourceContent), posEncoding)
			return &protocol.Hover{
				Contents: &protocol.MarkupContent{
					Kind:  protocol.MarkupKindMarkdown,
					Value: hoverStr,
				},
				Range: &rng,
			}, nil
		}

		// Handle dynamic case
		if resolution.IsDynamic() {
			hoverStr := buildDynamicHover()
			rng := toProtocolRange(edge.Source, string(sourceContent), posEncoding)
			return &protocol.Hover{
				Contents: &protocol.MarkupContent{
					Kind:  protocol.MarkupKindMarkdown,
					Value: hoverStr,
				},
				Range: &rng,
			}, nil
		}

		// Handle ambiguous case: the target WAS located, but in multiple
		// libraries (a flat-namespace name collision). Render an honest
		// "ambiguous" message rather than the misleading unresolved one.
		if resolution.IsAmbiguous() {
			hoverStr := buildAmbiguousHover()
			rng := toProtocolRange(edge.Source, string(sourceContent), posEncoding)
			return &protocol.Hover{
				Contents: &protocol.MarkupContent{
					Kind:  protocol.MarkupKindMarkdown,
					Value: hoverStr,
				},
				Range: &rng,
			}, nil
		}

		// Handle unresolved case
		hoverStr := buildUnresolvedHover()
		rng := toProtocolRange(edge.Source, string(sourceContent), posEncoding)
		return &protocol.Hover{
			Contents: &protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: hoverStr,
			},
			Range: &rng,
		}, nil
	}

	// T5: DataAccessEntry branch
	if dataAccess != nil {
		// Feature-08 record-form empty-Name gap: no card
		if dataAccess.Name == "" {
			return nil, nil
		}

		// Look up the DDM in the index by name (case-insensitive, matching references.go)
		candidates := idx.LookupByName(dataAccess.Name, model.ObjectDDM, &hctx.cfg)
		var ddmFA *model.FileAnalysis

		// If a DDM with matching name is found, use the first one's FileAnalysis
		// (name-based matching; full resolution is future work)
		if len(candidates) > 0 {
			ddmPath := candidates[0].Path
			if fa, ok := idx.Get(ddmPath); ok {
				ddmFA = &fa
			}
		}

		// Build the DDM hover card (with or without indexed FileAnalysis)
		hoverStr := buildDDMHover(dataAccess.Name, ddmFA)
		if hoverStr == "" {
			// No card (shouldn't happen with non-empty name, but guard anyway)
			return nil, nil
		}

		// Wrap the hover card with the view-name's NameRange (not the whole statement)
		rng := toProtocolRange(dataAccess.NameRange, string(sourceContent), posEncoding)
		return &protocol.Hover{
			Contents: &protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: hoverStr,
			},
			Range: &rng,
		}, nil
	}

	return nil, nil
}
