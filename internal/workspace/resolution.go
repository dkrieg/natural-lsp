// Package workspace: cross-file resolution of references produced by
// extraction. Resolution follows Natural's steplib chain — current library →
// steplibs in order → SYSTEM — driven by the configured library map, NOT file
// paths. The same module name can exist in multiple libraries; search order
// disambiguates. With no library map, the workspace is a single flat namespace
// and ambiguous resolution is reported as a diagnostic.
//
// PERFORM resolves an inline subroutine before an external one of the same
// name. See docs/plans/natural-lsp-prd.md (FR-10..18, FR-31).
package workspace

// TODO: Resolver over the index + library map; steplib walk; inline-before-
// external subroutine scope; ambiguity diagnostics.
