// Package server implements the LSP lifecycle (initialize, shutdown) and
// request dispatch over stdio. It depends only on the analysis.Analyzer
// interface and the workspace index — never on a concrete extraction backend.
package server

// TODO: Server type, initialize handshake advertising supported capabilities,
// shutdown, and graceful degradation on malformed objects (PRD FR-41..43).
