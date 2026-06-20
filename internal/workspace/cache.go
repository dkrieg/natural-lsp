// Package workspace: serializes the index to disk and loads it on startup.
// Entries are invalidated on file *content* hash (not mtime, which breaks
// across git checkouts); a cache-format version forces a full rebuild on
// upgrade (PRD FR-37..40).
package workspace

// TODO: Save/Load, content-hash invalidation, format-version gating.
