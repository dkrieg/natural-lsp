package workspace

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
)

// multilibChainSetup builds the multi-library fixture index and a cfg whose
// library map declares APP (steplib COMMON) and ALT (not in APP's chain).
// CUSTLDA.NSL and EMPLOYEE.NSD exist in BOTH COMMON (reachable) and ALT
// (unreachable). ALT's path sorts before COMMON, so a naive candidates[0] pick
// resolves to the UNREACHABLE ALT copy.
func multilibChainSetup(t *testing.T) (*Index, config.Config) {
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
	idx, _, _, err := BuildWithCache(context.Background(), root, cfg, az, logger, "", nil, nil)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	return idx, cfg
}

// TestResolveDataAreaFieldLocation_ChainWinner asserts the USING data-area field
// resolution follows the steplib chain: the CALLER in APP resolves CUSTLDA to the
// COMMON copy (chain winner, non-transitive, first-match), NOT the same-named
// unreachable ALT copy. Finding 2 (T7 path).
func TestResolveDataAreaFieldLocation_ChainWinner(t *testing.T) {
	idx, cfg := multilibChainSetup(t)

	dataAreaRef := model.DataAreaRef{Name: "CUSTLDA", SectionKind: "local"}
	referencingPath := "APP/CALLER.NSP"

	rng, objPath := ResolveDataAreaFieldLocation("#CUST-ID", dataAreaRef, idx, referencingPath, &cfg)

	if objPath != "COMMON/CUSTLDA.NSL" {
		t.Errorf("chain winner: got object path %q, want %q (must not pick unreachable ALT copy)", objPath, "COMMON/CUSTLDA.NSL")
	}
	if objPath == "ALT/CUSTLDA.NSL" {
		t.Errorf("unreachable-exclusion violated: resolved to ALT copy (outside APP's steplib chain)")
	}
	if rng.Start.Line == 0 && rng.End.Line == 0 {
		t.Errorf("expected a non-zero field NameRange for #CUST-ID in the resolved data area")
	}
}

// TestResolveDDMPath_ChainWinner asserts DDM name → .NSD path resolution follows
// the steplib chain: EMPLOYEE resolves to the COMMON copy, not the unreachable ALT
// copy. Finding 2 (T9 path).
func TestResolveDDMPath_ChainWinner(t *testing.T) {
	idx, cfg := multilibChainSetup(t)

	referencingPath := "APP/CALLER.NSP"
	objPath := ResolveDDMPath("EMPLOYEE", idx, referencingPath, &cfg)

	if objPath != "COMMON/EMPLOYEE.NSD" {
		t.Errorf("chain winner: got DDM path %q, want %q (must not pick unreachable ALT copy)", objPath, "COMMON/EMPLOYEE.NSD")
	}
	if objPath == "ALT/EMPLOYEE.NSD" {
		t.Errorf("unreachable-exclusion violated: resolved to ALT DDM copy (outside APP's steplib chain)")
	}
}
