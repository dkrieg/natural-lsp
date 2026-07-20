// Package paths holds path-key helpers shared across the natural-lsp packages.
// It is a LEAF package: it imports nothing internal (only stdlib strings), so
// every layer — workspace, document, server — may depend on it without risking
// an import cycle. It exists so the canonical index-key normalizer can be shared
// by both internal/workspace and internal/document (which must NOT import
// internal/workspace — see ADR-027 Finding 1).
package paths

import "strings"

// NormalizeKey canonicalizes a workspace-relative path to the index keyspace:
// forward-slash ("/") separators on every OS. Index/resolution keys, the
// content-hash map, the line-width table, and ALL lookups against them MUST
// route through this helper so that keys built on Windows (where filepath.Rel
// yields backslash separators, e.g. "code\LIB1\MYSUB.NSN") match the
// forward-slash lookups performed by the server layer (uriToRelPath).
//
// It uses strings.ReplaceAll on the literal backslash byte — NOT filepath.ToSlash
// — deliberately: filepath.ToSlash replaces only the current OS separator, so on
// Linux/macOS (separator "/") it is a no-op on backslashes and would neither fix
// Windows behavior nor be testable on non-Windows CI. ReplaceAll is unconditional
// and byte-literal, so this test is a real cross-platform guard, and it matches
// the server layer's existing normalization exactly (one canonical definition).
func NormalizeKey(rel string) string {
	return strings.ReplaceAll(rel, "\\", "/")
}
