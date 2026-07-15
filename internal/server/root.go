package server

import (
	"fmt"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// resolveRootStart returns the filesystem path to use as the starting point for
// workspace root discovery, applying LSP-standard precedence to initialize params.
//
// The precedence order (highest first) reflects LSP semantics and natural fallback chains:
//  1. WorkspaceFolders (authoritative, takes precedence over deprecated rootUri) —
//     returns the first entry's URI.FsPath() if non-empty and the URI is a file:// scheme.
//  2. RootURI (deprecated but still used) — if WorkspaceFolders is absent or empty,
//     uses RootURI.FsPath() if non-nil and non-empty and the URI is a file:// scheme.
//  3. cwdFallback (backward compatibility) — if both client fields are absent, empty,
//     or are non-file URIs, returns the cwdFallback verbatim (the process cwd from
//     os.Getwd() at startup).
//
// An empty or whitespace result, or a non-file URI scheme (e.g. untitled:, unsaved
// document schemes), at any tier causes fallthrough to the next. Never panics on
// degenerate/invalid URIs.
func resolveRootStart(params protocol.InitializeParams, cwdFallback string) string {
	// Tier 1: WorkspaceFolders (authoritative, LSP 3.17+)
	if folders, ok := params.WorkspaceFolders.Get(); ok && len(folders) > 0 {
		if isValidFileURI(folders[0].URI) {
			if path := strings.TrimSpace(folders[0].URI.FsPath()); path != "" {
				return path
			}
		}
	}

	// Tier 2: RootURI (deprecated but still in use)
	if params.RootURI != nil {
		if isValidFileURI(*params.RootURI) {
			if path := strings.TrimSpace(params.RootURI.FsPath()); path != "" {
				return path
			}
		}
	}

	// Tier 3: Fallback to the process cwd or other provided default
	return cwdFallback
}

// rootProbe captures everything needed to decide, once at index-build time,
// whether the server established a usable workspace root (feature 20, Story 3).
// It is populated in the "initialize" handler from the client params and the
// deferred config.Bootstrap outcome, then consumed after the "initialized"
// index build by noUsableRootMessage.
//
// Fields:
//   - clientPaths: the fs paths derived from the client's workspaceFolders /
//     rootUri (in precedence order). Empty when the client supplied no root.
//   - cwdFallback: the process cwd used as the lowest-precedence discovery start.
//   - sentinelFound: whether config.FindRoot located a .natural-lsp.toml sentinel
//     on the walk-up from the negotiated start path.
//   - resolvedRoot: the root config.Bootstrap actually resolved (for the message).
type rootProbe struct {
	clientPaths   []string
	cwdFallback   string
	sentinelFound bool
	resolvedRoot  string
}

// noUsableRootMessage decides whether the "no usable root" condition holds and,
// if so, returns an actionable, path-naming message plus true. It is pure (no
// I/O, no logging) so it can be unit-tested directly. The caller is responsible
// for emitting the message exactly once (Warn on stderr + window/showMessage).
//
// Two sub-conditions (Story 3 AC1):
//
//	(a) no workspace root could be established: the client sent no
//	    workspaceFolders/rootUri path AND the cwd walk-up found no sentinel.
//	    Phrase: "could not establish workspace root".
//	(b) the established root has no indexable files: the index built but is empty
//	    (indexFileCount == 0). Phrase: "workspace root has no indexable Natural
//	    files". This subsumes case (a) diagnostically but the message is distinct.
//
// The returned message always names the paths tried (the client
// workspaceFolders/rootUri fs paths and the cwd fallback) so an empty index is
// diagnosable. When neither sub-condition holds (a populated, healthy root),
// it returns ("", false) and the caller sends nothing.
func noUsableRootMessage(p rootProbe, indexFileCount int) (string, bool) {
	clientRootProvided := false
	for _, cp := range p.clientPaths {
		if strings.TrimSpace(cp) != "" {
			clientRootProvided = true
			break
		}
	}

	// Sub-condition (a): no client root and no sentinel on the cwd walk-up.
	noRootEstablished := !clientRootProvided && !p.sentinelFound

	// Sub-condition (b): the index built but is empty.
	emptyIndex := indexFileCount == 0

	if !noRootEstablished && !emptyIndex {
		return "", false
	}

	tried := triedPathsPhrase(p)
	if noRootEstablished {
		return fmt.Sprintf(
			"natural-lsp: could not establish workspace root — no workspaceFolders/rootUri was sent and no .natural-lsp.toml sentinel was found on the cwd walk-up. Paths tried: %s. Open a folder or add a .natural-lsp.toml to enable indexing.",
			tried,
		), true
	}
	// emptyIndex (a root was established, but it contains no indexable files)
	return fmt.Sprintf(
		"natural-lsp: workspace root %q has no indexable Natural files (index is empty). Paths tried: %s. Check the workspace root and the [workspace] extensions/exclude settings.",
		p.resolvedRoot, tried,
	), true
}

// triedPathsPhrase renders the discovery paths considered (client
// workspaceFolders/rootUri paths first, then the cwd fallback) into a single
// human-readable, comma-separated phrase for the actionable message.
func triedPathsPhrase(p rootProbe) string {
	parts := make([]string, 0, len(p.clientPaths)+1)
	for _, cp := range p.clientPaths {
		if s := strings.TrimSpace(cp); s != "" {
			parts = append(parts, fmt.Sprintf("client=%q", s))
		}
	}
	parts = append(parts, fmt.Sprintf("cwd=%q", p.cwdFallback))
	return strings.Join(parts, ", ")
}

// clientRootPaths returns the fs paths the client offered for the workspace root
// (workspaceFolders entries first, in order, then rootUri), skipping non-file or
// empty URIs. It mirrors resolveRootStart's precedence but returns ALL considered
// paths (for observability), not just the winning one. Never panics.
func clientRootPaths(params protocol.InitializeParams) []string {
	var paths []string
	if folders, ok := params.WorkspaceFolders.Get(); ok {
		for _, f := range folders {
			if isValidFileURI(f.URI) {
				if s := strings.TrimSpace(f.URI.FsPath()); s != "" {
					paths = append(paths, s)
				}
			}
		}
	}
	if params.RootURI != nil && isValidFileURI(*params.RootURI) {
		if s := strings.TrimSpace(params.RootURI.FsPath()); s != "" {
			paths = append(paths, s)
		}
	}
	return paths
}

// isValidFileURI checks if a URI has the file scheme, which is suitable for use
// as a workspace root. Non-file URIs (untitled:, vscode-notebook-cell:, etc.)
// are rejected.
//
// It delegates to the library's scheme detection (uri.URI.IsFile) rather than a
// hand-rolled prefix check, so spec-legal file URI forms are all accepted:
// the RFC 8089 single-slash form (file:/abs) and the file://host/abs authority
// form, in addition to the common file:///abs triple-slash form. The prior
// strings.HasPrefix(u, "file://") check rejected the single-slash form outright.
// Callers additionally guard against an empty FsPath() (the defensive
// TrimSpace(FsPath())=="" fallthrough), so a scheme-only URI still falls through.
func isValidFileURI(u uri.URI) bool {
	return u.IsFile()
}
