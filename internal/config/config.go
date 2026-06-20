// Package config loads and validates the workspace configuration
// (.natural-lsp.toml), supplies defaults, discovers the workspace root by
// walking up to the sentinel file, and exposes the library map and steplib
// search order consumed by workspace/resolution.go.
//
// See docs/plans/natural-lsp-prd.md (FR-1..6, CR-1..6).
package config

// TODO: Config struct (workspace extensions/excludes/max-file-size, cache
// path, analysis options, resolution library map), Load, Defaults, Validate,
// and FindRoot.
