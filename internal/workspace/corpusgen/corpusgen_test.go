package corpusgen_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
	"github.com/dkrieg/natural-lsp/internal/workspace/corpusgen"
)

// discardLogger returns a slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// hashTree walks dir and returns a deterministic digest of the whole tree:
// the sorted list of workspace-relative paths each paired with the sha256 of
// its content. Two trees with identical file sets and identical bytes produce
// the same digest, so it is a byte-identical-corpus equality check.
func hashTree(t *testing.T, dir string) string {
	t.Helper()

	type entry struct {
		rel  string
		hash string
	}
	var entries []entry

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		entries = append(entries, entry{
			rel:  filepath.ToSlash(rel),
			hash: fmt.Sprintf("%x", sha256.Sum256(content)),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.rel)
		sb.WriteByte(0)
		sb.WriteString(e.hash)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// countObjects returns the number of generated Natural object files (excludes
// the .natural-lsp.toml config sentinel).
func countObjects(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".toml") {
			return nil
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

// TestGenerateDeterministic asserts that the same seed and object count produce
// a byte-identical corpus in two independent target directories (NFR-9).
func TestGenerateDeterministic(t *testing.T) {
	const (
		objects = 40
		seed    = int64(1234)
	)

	dirA := t.TempDir()
	dirB := t.TempDir()

	if err := corpusgen.Generate(dirA, objects, seed); err != nil {
		t.Fatalf("Generate(A): %v", err)
	}
	if err := corpusgen.Generate(dirB, objects, seed); err != nil {
		t.Fatalf("Generate(B): %v", err)
	}

	hashA := hashTree(t, dirA)
	hashB := hashTree(t, dirB)
	if hashA != hashB {
		t.Fatalf("same seed produced different corpora:\nA:\n%s\nB:\n%s", hashA, hashB)
	}
}

// TestGenerateSeedSensitivity confirms a different seed produces a different
// corpus (the seed actually drives generation — it is not a no-op).
func TestGenerateSeedSensitivity(t *testing.T) {
	const objects = 40

	dirA := t.TempDir()
	dirB := t.TempDir()

	if err := corpusgen.Generate(dirA, objects, 1); err != nil {
		t.Fatalf("Generate(seed 1): %v", err)
	}
	if err := corpusgen.Generate(dirB, objects, 2); err != nil {
		t.Fatalf("Generate(seed 2): %v", err)
	}

	if hashTree(t, dirA) == hashTree(t, dirB) {
		t.Fatalf("different seeds produced identical corpora — seed is not driving generation")
	}
}

// TestGenerateObjectCount asserts the generator emits the requested number of
// object files at two sizes (scales with the count parameter).
func TestGenerateObjectCount(t *testing.T) {
	for _, objects := range []int{20, 50} {
		dir := t.TempDir()
		if err := corpusgen.Generate(dir, objects, 99); err != nil {
			t.Fatalf("Generate(%d): %v", objects, err)
		}
		got := countObjects(t, dir)
		if got != objects {
			t.Fatalf("Generate(%d): emitted %d object files, want %d", objects, got, objects)
		}
	}
}

// TestGenerateEmitsConfig asserts a loadable .natural-lsp.toml with a library
// map is emitted so the steplib chain is exercised.
func TestGenerateEmitsConfig(t *testing.T) {
	dir := t.TempDir()
	if err := corpusgen.Generate(dir, 30, 7); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	cfgPath := filepath.Join(dir, ".natural-lsp.toml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("expected .natural-lsp.toml at %s: %v", cfgPath, err)
	}

	cfg, problems, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("config had validation problems: %v", problems)
	}
	if len(cfg.Resolution.Libraries) < 2 {
		t.Fatalf("expected a multi-library map (>=2), got %d", len(cfg.Resolution.Libraries))
	}
}

// TestGenerateAnalyzableAndResolvable is the correctness guard that makes the
// generator trustworthy for benchmarks: the corpus builds end-to-end, indexes
// the expected object count, produces no happy-path syntax diagnostics, and its
// generated cross-references actually resolve (proving the edges are real, not
// dangling).
func TestGenerateAnalyzableAndResolvable(t *testing.T) {
	const (
		objects = 50
		seed    = int64(2026)
	)

	dir := t.TempDir()
	if err := corpusgen.Generate(dir, objects, seed); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	cfg, problems, err := config.Load(filepath.Join(dir, ".natural-lsp.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("config problems: %v", problems)
	}

	az := natural.New(nil)
	idx, err := workspace.Build(context.Background(), dir, cfg, az, discardLogger(), nil)
	if err != nil {
		t.Fatalf("workspace.Build: %v", err)
	}

	// The index must contain exactly the emitted object files.
	keys := idx.Keys()
	if len(keys) != objects {
		t.Fatalf("indexed %d files, want %d", len(keys), objects)
	}

	// Happy-path: no syntax diagnostics anywhere in the generated corpus.
	idx.ForEach(func(path string, fa model.FileAnalysis) {
		if len(fa.Diagnostics) != 0 {
			t.Errorf("%s: unexpected diagnostics: %v", path, fa.Diagnostics)
		}
	})
	if t.Failed() {
		t.Fatalf("generated corpus produced syntax diagnostics on the happy path")
	}

	// Resolve cross-references and assert at least one CALLNAT, PERFORM, and
	// INCLUDE edge resolves — proving the generated cross-refs are real bindings
	// to generated objects, not dangling names.
	res := workspace.Resolve(idx, &cfg)

	var (
		callnatResolved int
		performResolved int
		includeResolved int
		anyEdges        int
		ddmReads        int
	)
	idx.ForEach(func(path string, fa model.FileAnalysis) {
		for _, edge := range fa.Edges {
			anyEdges++
			r, ok := res.Get(path, edge.Source)
			if !ok || !r.IsResolved() {
				continue
			}
			switch edge.Kind {
			case model.EdgeCalls:
				callnatResolved++
			case model.EdgePerforms:
				performResolved++
			case model.EdgeIncludes:
				includeResolved++
			}
		}
		// DDM reads/finds are extracted into the data-access channel, not the
		// call-graph resolution set; count named read sites against generated DDMs.
		for _, da := range fa.DataAccess {
			if da.Kind == model.EdgeReads && da.Name != "" {
				ddmReads++
			}
		}
	})

	if anyEdges == 0 {
		t.Fatalf("generated corpus has no edges at all — no cross-references generated")
	}
	if callnatResolved == 0 {
		t.Errorf("no CALLNAT edge resolved to a generated subprogram")
	}
	if performResolved == 0 {
		t.Errorf("no PERFORM edge resolved to a generated subroutine")
	}
	if includeResolved == 0 {
		t.Errorf("no INCLUDE edge resolved to a generated copycode")
	}
	if ddmReads == 0 {
		t.Errorf("no READ/FIND data-access site against a generated DDM")
	}

	// The generated DDM objects must themselves parse into field definitions
	// (the .NSD line-scanner), proving the DDM fixture format is faithful.
	ddmWithFields := 0
	idx.ForEach(func(path string, fa model.FileAnalysis) {
		if fa.ObjectType == model.ObjectDDM && len(fa.Definitions) > 0 {
			ddmWithFields++
		}
	})
	if ddmWithFields == 0 {
		t.Errorf("no generated DDM parsed into field definitions")
	}
}
