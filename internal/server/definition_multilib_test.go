package server

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// multilibServerSetup builds the multi-library fixture into a handlerContext with
// the library map LOADED from the fixture's .natural-lsp.toml (via config.Bootstrap,
// NOT config.Defaults()), so the steplib chain is genuinely exercised.
//
// APP declares steplib COMMON; ALT is not in APP's chain. CUSTLDA.NSL and
// EMPLOYEE.NSD exist in BOTH COMMON (reachable) and ALT (unreachable). ALT's path
// sorts before COMMON, so a naive candidates[0] pick lands on the unreachable ALT
// copy — the chain resolution must exclude it.
func multilibServerSetup(t *testing.T) (*handlerContext, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)
	root, err := filepath.Abs(filepath.Join("testdata", "multilib"))
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	_, cfg, err := config.Bootstrap(root, "", logger)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(cfg.Resolution.Libraries) == 0 {
		t.Fatalf("expected library map from .natural-lsp.toml, got none — fixture toml not loaded")
	}
	idx, _, _, err := workspace.BuildWithCache(context.Background(), root, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	resSet := workspace.Resolve(idx, &cfg)
	hctx := &handlerContext{
		idx:         idx,
		res:         resSet,
		posEncoding: protocol.PositionEncodingKindUTF8,
		root:        root,
		cfg:         cfg,
		logger:      logger,
		az:          az,
	}
	return hctx, root
}

// TestProvideDefinition_MultiLib_USINGDataArea asserts that go-to-definition on a
// variable declared in an external USING data area resolves to the COMMON copy
// (chain winner) and NOT the unreachable ALT copy. Finding 2, T7 path.
func TestProvideDefinition_MultiLib_USINGDataArea(t *testing.T) {
	hctx, root := multilibServerSetup(t)
	callerFile := filepath.Join(root, "APP", "CALLER.NSP")

	// CALLER.NSP line 7: "MOVE 42 TO #CUST-ID" (shifted from line 5 due to added USING clauses)
	// Cursor on #CUST-ID (0-based line 6, char within the token starting at col 12 → 0-based 11).
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(callerFile)},
			Position:     protocol.Position{Line: 6, Character: 14},
		},
	}

	locations, err := provideDefinition(hctx, params)
	if err != nil {
		t.Fatalf("provideDefinition: %v", err)
	}
	if len(locations) == 0 {
		t.Fatalf("expected a resolved location for #CUST-ID via USING chain, got none")
	}
	got := locations[0].URI.FsPath()
	if !strings.HasSuffix(filepath.ToSlash(got), "COMMON/CUSTLDA.NSL") {
		t.Errorf("chain winner: got %q, want …/COMMON/CUSTLDA.NSL", got)
	}
	if strings.Contains(filepath.ToSlash(got), "/ALT/") {
		t.Errorf("unreachable-exclusion violated: resolved to ALT copy %q (outside APP's steplib chain)", got)
	}
}

// TestProvideDefinition_MultiLib_SQLDDM asserts that go-to-definition on a SQL FROM
// table operand resolves the DDM to the COMMON copy (chain winner) and NOT the
// unreachable ALT copy. Finding 2, T9 path.
func TestProvideDefinition_MultiLib_SQLDDM(t *testing.T) {
	hctx, root := multilibServerSetup(t)
	callerFile := filepath.Join(root, "APP", "CALLER.NSP")

	// CALLER.NSP line 10: "    FROM EMPLOYEE" — cursor on EMPLOYEE. (shifted from line 8 due to added USING clauses)
	// EMPLOYEE starts at 1-based col 10 (0-based char 9); aim mid-token.
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(callerFile)},
			Position:     protocol.Position{Line: 9, Character: 12},
		},
	}

	locations, err := provideDefinition(hctx, params)
	if err != nil {
		t.Fatalf("provideDefinition: %v", err)
	}
	if len(locations) == 0 {
		t.Fatalf("expected a resolved location for SQL FROM EMPLOYEE via chain, got none")
	}
	got := locations[0].URI.FsPath()
	if !strings.HasSuffix(filepath.ToSlash(got), "COMMON/EMPLOYEE.NSD") {
		t.Errorf("chain winner: got %q, want …/COMMON/EMPLOYEE.NSD", got)
	}
	if strings.Contains(filepath.ToSlash(got), "/ALT/") {
		t.Errorf("unreachable-exclusion violated: resolved to ALT DDM copy %q (outside APP's steplib chain)", got)
	}
}
