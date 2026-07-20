package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/paths"
)

// writeFile is a small helper that creates parent dirs and writes a file.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestBuildIndexKeys_NoBackslash_SubfolderResolves is the workspace-level guard
// for the Windows path-separator bug: the index/resolution keyspace must use
// forward slashes on EVERY OS, and a call to a subprogram in the SAME subfolder
// must resolve to that subfolder's file key.
//
// On Linux/macOS this passes today (filepath.Rel already yields "/"), so it does
// not, by itself, catch a regression on those platforms — it documents and guards
// the invariant that every index key is forward-slash. The genuine cross-platform
// teeth are TestNormalizeKey and TestBuildIndexKeys_WindowsMismatchSimulation
// below (which exercise the literal-backslash path independent of the OS).
func TestBuildIndexKeys_NoBackslash_SubfolderResolves(t *testing.T) {
	root := t.TempDir()

	// A caller and callee both under a subfolder (code/LIB1/…). Flat namespace
	// (no library map), so a single matching MYSUB resolves.
	writeFile(t, filepath.Join(root, "code", "LIB1", "CALLER.NSP"),
		"DEFINE DATA LOCAL END-DEFINE\nCALLNAT 'MYSUB'\nEND\n")
	writeFile(t, filepath.Join(root, "code", "LIB1", "MYSUB.NSN"),
		"DEFINE DATA PARAMETER END-DEFINE\nEND\n")

	cfg := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	az := natural.New(nil)

	idx, err := Build(context.Background(), root, cfg, az, logger, nil)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// (a) Invariant: NO key in the index contains a backslash on any OS.
	for _, k := range idx.Keys() {
		if strings.Contains(k, "\\") {
			t.Errorf("index key %q contains a backslash; keys must be forward-slash canonical", k)
		}
	}

	// The subfolder keys must be present in forward-slash form.
	callerKey := "code/LIB1/CALLER.NSP"
	calleeKey := "code/LIB1/MYSUB.NSN"
	callerFA, ok := idx.Get(callerKey)
	if !ok {
		t.Fatalf("caller not found under forward-slash key %q; keys present: %v", callerKey, idx.Keys())
	}
	if _, ok := idx.Get(calleeKey); !ok {
		t.Fatalf("callee not found under forward-slash key %q; keys present: %v", calleeKey, idx.Keys())
	}

	// (b) Resolution: the CALLNAT 'MYSUB' from the subfolder resolves to the
	// subfolder's MYSUB.NSN key — same-folder subfolder resolution works.
	var callEdge model.EdgeEntry
	var found bool
	for _, e := range callerFA.Edges {
		if e.Kind == model.EdgeCalls && e.TargetName == "MYSUB" {
			callEdge = e
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("CALLNAT 'MYSUB' edge not found in %s; edges: %+v", callerKey, callerFA.Edges)
	}

	resSet := Resolve(idx, &cfg)
	res, exists := resSet.Get(callerKey, callEdge.Source)
	if !exists {
		t.Fatalf("no resolution recorded for CALLNAT 'MYSUB' at %s", callerKey)
	}
	if !res.IsResolved() {
		t.Fatalf("CALLNAT 'MYSUB' not resolved; outcome: %+v", res)
	}
	if res.Path != calleeKey {
		t.Errorf("resolved Path = %q, want %q (subfolder callee)", res.Path, calleeKey)
	}
}

// TestBuildIndexKeys_WindowsMismatchSimulation directly demonstrates that the
// producer (index) side and the consumer (server-lookup) side now agree, by
// simulating what Windows does: it adds an entry under a NormalizeKey-ed key and
// then looks it up via a raw backslash-form relative path that has itself been
// normalized. Both sides route through NormalizeKey, so they hit.
//
// This is PLATFORM-INDEPENDENT: it uses literal backslash bytes, not the OS
// separator, so it exercises the Windows code path on Linux/macOS CI. It is
// designed to FAIL if NormalizeKey is removed from EITHER side:
//   - remove it from the producer → the key is stored with backslashes → the
//     normalized lookup misses.
//   - remove it from the lookup → the raw backslash lookup misses the
//     forward-slash key.
func TestBuildIndexKeys_WindowsMismatchSimulation(t *testing.T) {
	idx := &Index{entries: make(map[string]model.FileAnalysis)}

	// PRODUCER side (as the build/watcher does on Windows): a filepath.Rel result
	// with backslashes, canonicalized before use as a key.
	winRel := "code\\LIB1\\MYSUB.NSN"
	producerKey := paths.NormalizeKey(winRel)
	idx.Add(producerKey, model.FileAnalysis{ObjectType: model.ObjectSubprogram})

	// The stored key must be forward-slash — the index never holds a backslash key.
	for _, k := range idx.Keys() {
		if strings.Contains(k, "\\") {
			t.Fatalf("index holds backslash key %q after Add; NormalizeKey missing on producer side", k)
		}
	}

	// CONSUMER side (as uriToRelPath does): a lookup path that arrives with
	// backslashes (Windows FsPath+Rel) is normalized before Get.
	lookupKey := paths.NormalizeKey(winRel)
	if _, ok := idx.Get(lookupKey); !ok {
		t.Fatalf("lookup via normalized key %q MISSED; producer and consumer keyspaces disagree", lookupKey)
	}

	// Sanity: a NON-normalized backslash lookup would miss the forward-slash key —
	// this is the exact Windows bug, proving normalization is load-bearing.
	if _, ok := idx.Get(winRel); ok {
		t.Fatalf("raw backslash key %q unexpectedly hit; the index key must be forward-slash only", winRel)
	}
}
