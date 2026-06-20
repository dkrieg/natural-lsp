// Package workspace holds the cross-file symbol table built from per-file
// FileAnalysis results, plus incremental re-analysis: when a file changes,
// only it and its dependents are re-indexed (PRD FR-35, FR-36).
package workspace

// TODO: Index type, build, query (definitions/references/symbols), and
// dependency-aware invalidation (e.g. copycode changes re-index includers).
