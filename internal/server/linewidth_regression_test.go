package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/model"
	"natural-lsp/internal/workspace"
)

// This file holds the feature-22 T8 regression tests proving the workspace/symbol
// and references providers now serve position ranges from the in-memory
// line-width table (OQ-B B-i) with BYTE-IDENTICAL results to the prior
// disk-reading implementation, and WITHOUT any per-query disk read.
//
// LOAD-BEARING REQUIREMENT (review finding, 2026-07-18): the fixture must put a
// NON-ASCII character BEFORE an *emitted* symbol's column ON THE SAME LINE, so
// the symbol's startChar/endChar genuinely DIFFER between UTF-8 and UTF-16. An
// earlier version of this fixture placed the non-ASCII content on a comment line
// with no emitted symbol, so the checked ranges were on pure-ASCII lines and the
// UTF-16 conversion was never exercised — breaking the surrogate/BMP counting in
// linewidth.go left these tests green. The construction below exploits Natural's
// support for MULTIPLE STATEMENTS ON ONE LINE: an assignment carrying a non-ASCII
// string literal precedes the DEFINE SUBROUTINE (workspace/symbol) and the CALLNAT
// (references) on the same physical line, so the emitted subroutine-name /
// CALLNAT-target range sits after a multibyte prefix. TestT8_* below asserts the
// emitted range's UTF-16 columns DIFFER from the UTF-8 columns by the expected
// code-unit delta AND equal the disk-reading oracle — so sabotaging the
// surrogate/`>0xFFFF` (or the é BMP) counting in linewidth.go now makes these
// tests FAIL.

// lwDiscardLogger drops analyzer log output.
func lwDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The shared non-ASCII prefix placed (as a string literal in a leading
// assignment) before the emitted token on its line. Its byte→UTF-16 code-unit
// delta must be non-zero for the fixture to exercise UTF-16 conversion.
//
//	"#X := 'café 😀' " byte layout before the following token:
//	  #(1) X(1) sp(1) :(1) =(1) sp(1) '(1) c(1) a(1) f(1)   -> 10 bytes
//	  é(2 bytes / 1 UTF-16 unit)                            -> +2 bytes
//	  sp(1) 😀(4 bytes / 2 UTF-16 units)                    -> +5 bytes
//	  '(1) sp(1)                                            -> +2 bytes
//	The é contributes 1 fewer UTF-16 unit than bytes (delta -1) and the
//	supplementary-plane 😀 contributes 2 fewer (4 bytes -> 2 units, delta -2), so
//	any column AT OR AFTER the prefix is UTF16 = UTF8 - 3.
const lwPrefixUTF16Delta = 3

// writeLineWidthCorpus writes a small multi-file workspace under root:
//   - MULTIB.NSN: a subprogram whose DEFINE SUBROUTINE is preceded ON THE SAME
//     LINE by an assignment carrying a non-ASCII string literal (café + 😀), so
//     the EMITTED subroutine-name SelectionRange lands after a multibyte prefix
//     and its UTF-16 columns differ from UTF-8.
//   - CALLER.NSP: a program whose CALLNAT 'MULTIB' is likewise preceded on the
//     same line by the non-ASCII assignment, so the EMITTED reference (CALLNAT
//     edge Source) also lands after a multibyte prefix — giving references a
//     cross-file sweep with a resolvable target on a multibyte line.
//   - A .natural-lsp.toml so config.Load finds the workspace root.
//
// Natural permits multiple statements on one physical line; the analyzer accepts
// both files with ZERO diagnostics (asserted indirectly by the byte-identical
// oracle comparison — a parse error would move the ranges).
func writeLineWidthCorpus(t *testing.T, root string) {
	t.Helper()

	// The assignment prefix carries é (BMP, 2 bytes / 1 UTF-16 unit) and 😀
	// (supplementary plane, 4 bytes / 2 UTF-16 units) BEFORE the DEFINE
	// SUBROUTINE on the same line, so MULTIB-SUB's emitted name range is on a
	// multibyte-prefixed line.
	multib := "" +
		"DEFINE DATA LOCAL\n" +
		"1 #X (A20)\n" +
		"END-DEFINE\n" +
		"#X := 'café 😀' DEFINE SUBROUTINE MULTIB-SUB\n" +
		"  IGNORE\n" +
		"END-SUBROUTINE\n" +
		"END\n"

	// Same multibyte prefix before CALLNAT, so the emitted CALLNAT edge Source
	// (the reference site swept by references) is on a multibyte-prefixed line.
	caller := "" +
		"DEFINE DATA LOCAL\n" +
		"1 #X (A20)\n" +
		"1 #P (A10)\n" +
		"END-DEFINE\n" +
		"#X := 'café 😀' CALLNAT 'MULTIB' #P\n" +
		"END\n"

	files := map[string]string{
		"MULTIB.NSN":        multib,
		"CALLER.NSP":        caller,
		".natural-lsp.toml": "[workspace]\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// buildLineWidthContext cold-builds the index over the corpus and returns a
// handlerContext wired as the server wires it, for the given encoding.
func buildLineWidthContext(t *testing.T, root string, enc protocol.PositionEncodingKind) *handlerContext {
	t.Helper()

	cfg, _, err := config.Load(filepath.Join(root, ".natural-lsp.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	az := natural.New(nil)
	idx, err := workspace.Build(context.Background(), root, cfg, az, lwDiscardLogger(), nil)
	if err != nil {
		t.Fatalf("workspace.Build: %v", err)
	}
	res := workspace.Resolve(idx, &cfg)
	return &handlerContext{
		idx:         idx,
		res:         res,
		posEncoding: enc,
		root:        root,
		cfg:         cfg,
		az:          az,
	}
}

// referenceConversion recomputes, by READING the file from disk, the protocol
// range for a model.Range via the original toProtocolRange path. This is the
// oracle the in-memory conversion must match byte-for-byte.
func referenceConversion(t *testing.T, absPath string, r model.Range, enc protocol.PositionEncodingKind) protocol.Range {
	t.Helper()
	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("oracle read %s: %v", absPath, err)
	}
	return toProtocolRange(r, string(content), enc)
}

// TestT8_WorkspaceSymbol_ByteIdenticalAndDiskFree proves workspace/symbol output
// is byte-identical to the disk-reading oracle (incl. the non-ASCII/UTF-16 case)
// AND survives deletion of the source files (disk-free proof) under both
// encodings.
//
// LOAD-BEARING: the subroutine symbol MULTIB-SUB is emitted with a name range on
// a line whose prefix carries é + 😀, so its UTF-16 startChar/endChar are strictly
// LESS than its UTF-8 columns (by lwPrefixUTF16Delta). The test asserts that
// difference explicitly, so a broken surrogate/BMP count in linewidth.go changes
// the emitted UTF-16 range and fails here.
func TestT8_WorkspaceSymbol_ByteIdenticalAndDiskFree(t *testing.T) {
	// Capture the UTF-8 range of the emitted subroutine symbol so the UTF-16 run
	// can assert it differs by exactly the multibyte-prefix delta.
	var subUTF8Range protocol.Range
	var subUTF8Found bool

	for _, enc := range []protocol.PositionEncodingKind{
		protocol.PositionEncodingKindUTF8,
		protocol.PositionEncodingKindUTF16,
	} {
		enc := enc
		t.Run(string(enc), func(t *testing.T) {
			root := t.TempDir()
			writeLineWidthCorpus(t, root)
			hctx := buildLineWidthContext(t, root, enc)

			// Query matching the subprogram object root AND its subroutine.
			// "MULTIB" matches the object name; the subroutine name contains it.
			got := provideWorkspaceSymbols(hctx, "MULTIB")
			if len(got) < 2 {
				t.Fatalf("expected object root + subroutine, got %d symbols: %+v", len(got), got)
			}

			// Build the oracle by reading files and converting each returned
			// symbol's range from disk content — must match byte-for-byte.
			var subRange protocol.Range
			var subSeen bool
			for _, sym := range got {
				absPath := sym.Location.URI.FsPath()
				// Recompute the range from the index's stored model range by
				// finding the matching symbol in the Structure tree.
				modelRange := findModelSelectionRange(t, hctx, sym)
				want := referenceConversion(t, absPath, modelRange, enc)
				if sym.Location.Range != want {
					t.Errorf("enc=%s symbol %q range = %+v, oracle (disk) = %+v",
						enc, sym.Name, sym.Location.Range, want)
				}
				if sym.Kind == protocol.SymbolKindFunction && sym.Name == "MULTIB-SUB" {
					subRange, subSeen = sym.Location.Range, true
				}
			}
			if !subSeen {
				t.Fatalf("emitted symbols did not include the MULTIB-SUB subroutine: %+v", got)
			}

			// LOAD-BEARING ASSERTION: the subroutine name sits after the é+😀
			// prefix, so its emitted columns MUST differ between encodings. The
			// subroutine name is on a single line (start.Line == end.Line).
			if subRange.Start.Line != subRange.End.Line {
				t.Fatalf("expected single-line subroutine range, got %+v", subRange)
			}
			switch enc {
			case protocol.PositionEncodingKindUTF8:
				subUTF8Range, subUTF8Found = subRange, true
			case protocol.PositionEncodingKindUTF16:
				if !subUTF8Found {
					t.Fatalf("UTF-8 subroutine range not captured before UTF-16 run")
				}
				// UTF-16 columns must be strictly less than UTF-8 by the exact
				// multibyte-prefix code-unit delta (é -1, 😀 -2 => -3). If the
				// surrogate/BMP counting in linewidth.go were wrong, this fails.
				if subRange.Start.Character != subUTF8Range.Start.Character-lwPrefixUTF16Delta {
					t.Errorf("subroutine UTF-16 startChar = %d, want UTF-8 %d minus delta %d = %d",
						subRange.Start.Character, subUTF8Range.Start.Character, lwPrefixUTF16Delta,
						subUTF8Range.Start.Character-lwPrefixUTF16Delta)
				}
				if subRange.End.Character != subUTF8Range.End.Character-lwPrefixUTF16Delta {
					t.Errorf("subroutine UTF-16 endChar = %d, want UTF-8 %d minus delta %d = %d",
						subRange.End.Character, subUTF8Range.End.Character, lwPrefixUTF16Delta,
						subUTF8Range.End.Character-lwPrefixUTF16Delta)
				}
			}

			// Now DELETE every source file: the in-memory table must serve the
			// identical result with zero disk reads possible.
			deleteSourceFiles(t, root)
			after := provideWorkspaceSymbols(hctx, "MULTIB")
			if !symbolsEqual(got, after) {
				t.Errorf("enc=%s results changed after deleting source files (disk-free proof failed):\nbefore=%+v\nafter =%+v",
					enc, got, after)
			}
		})
	}
}

// TestT8_References_ByteIdenticalAndDiskFree proves the references sweep output
// is byte-identical to the disk-reading oracle and survives file deletion, under
// both encodings, on a cross-file corpus (CALLER → MULTIB).
//
// LOAD-BEARING: the swept CALLNAT reference in CALLER.NSP is on a line whose
// prefix carries é + 😀, so its UTF-16 columns differ from UTF-8 by the multibyte
// delta — asserted explicitly below against the disk oracle and the UTF-8 run.
func TestT8_References_ByteIdenticalAndDiskFree(t *testing.T) {
	// Capture the UTF-8 range of the swept CALLNAT reference so the UTF-16 run can
	// assert the multibyte-prefix delta.
	var refUTF8Range protocol.Range
	var refUTF8Found bool

	for _, enc := range []protocol.PositionEncodingKind{
		protocol.PositionEncodingKindUTF8,
		protocol.PositionEncodingKindUTF16,
	} {
		enc := enc
		t.Run(string(enc), func(t *testing.T) {
			root := t.TempDir()
			writeLineWidthCorpus(t, root)
			hctx := buildLineWidthContext(t, root, enc)

			// Position the cursor on CALLER.NSP's CALLNAT 'MULTIB' target so the
			// sweep finds the reference to the MULTIB subprogram.
			callerRel := "CALLER.NSP"
			fa, ok := hctx.idx.Get(callerRel)
			if !ok {
				t.Fatalf("CALLER.NSP not in index")
			}
			var callEdge model.EdgeEntry
			foundEdge := false
			for _, e := range fa.Edges {
				if e.Kind == model.EdgeCalls {
					callEdge, foundEdge = e, true
					break
				}
			}
			if !foundEdge {
				t.Fatalf("no CALLNAT edge in CALLER.NSP")
			}

			// The cursor must land INSIDE the edge Source. Since the edge is now on
			// a multibyte-prefixed line, convert the model byte column through the
			// disk oracle to get the correct encoding-space cursor position.
			cursorProto := referenceConversion(t, filepath.Join(root, callerRel), callEdge.Source, enc)

			params := protocol.ReferenceParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(filepath.Join(root, callerRel)),
					},
					Position: cursorProto.Start,
				},
				Context: protocol.ReferenceContext{IncludeDeclaration: true},
			}

			got, err := provideReferences(hctx, params)
			if err != nil {
				t.Fatalf("provideReferences: %v", err)
			}
			if len(got) == 0 {
				t.Fatalf("expected at least the CALLNAT reference + declaration, got none")
			}

			// The swept reference site is the CALLNAT edge Source in CALLER.NSP.
			// Its emitted range must equal the disk oracle byte-for-byte.
			wantRef := referenceConversion(t, filepath.Join(root, callerRel), callEdge.Source, enc)
			callerURI := uri.File(filepath.Join(root, callerRel))
			var refRange protocol.Range
			var refSeen bool
			for _, loc := range got {
				if loc.URI == callerURI && loc.Range == wantRef {
					refRange, refSeen = loc.Range, true
					break
				}
			}
			if !refSeen {
				t.Fatalf("enc=%s swept CALLNAT reference range %+v (disk oracle) not found in results %+v",
					enc, wantRef, got)
			}

			// LOAD-BEARING ASSERTION: the CALLNAT site is after the é+😀 prefix, so
			// its emitted columns MUST differ between encodings by the exact delta.
			if refRange.Start.Line != refRange.End.Line {
				t.Fatalf("expected single-line CALLNAT reference range, got %+v", refRange)
			}
			switch enc {
			case protocol.PositionEncodingKindUTF8:
				refUTF8Range, refUTF8Found = refRange, true
			case protocol.PositionEncodingKindUTF16:
				if !refUTF8Found {
					t.Fatalf("UTF-8 reference range not captured before UTF-16 run")
				}
				if refRange.Start.Character != refUTF8Range.Start.Character-lwPrefixUTF16Delta {
					t.Errorf("CALLNAT reference UTF-16 startChar = %d, want UTF-8 %d minus delta %d = %d",
						refRange.Start.Character, refUTF8Range.Start.Character, lwPrefixUTF16Delta,
						refUTF8Range.Start.Character-lwPrefixUTF16Delta)
				}
				if refRange.End.Character != refUTF8Range.End.Character-lwPrefixUTF16Delta {
					t.Errorf("CALLNAT reference UTF-16 endChar = %d, want UTF-8 %d minus delta %d = %d",
						refRange.End.Character, refUTF8Range.End.Character, lwPrefixUTF16Delta,
						refUTF8Range.End.Character-lwPrefixUTF16Delta)
				}
			}

			// Delete source files and re-run: identical results (disk-free).
			// Note: provideReferences reads the cursor's source file once to
			// convert the cursor position, so keep CALLER.NSP for the second
			// run's cursor conversion — but the SWEEP over MULTIB must not need
			// disk. Delete only MULTIB.NSN (the swept target) to isolate the
			// sweep's disk-freedom.
			if err := os.Remove(filepath.Join(root, "MULTIB.NSN")); err != nil {
				t.Fatalf("remove MULTIB.NSN: %v", err)
			}
			after, err := provideReferences(hctx, params)
			if err != nil {
				t.Fatalf("provideReferences after delete: %v", err)
			}
			if !locationsEqual(got, after) {
				t.Errorf("enc=%s references changed after deleting swept target (disk-free proof failed):\nbefore=%+v\nafter =%+v",
					enc, got, after)
			}
		})
	}
}

// TestT8_ToProtocolRange_MatchesLineWidthTable pins the two range-conversion
// implementations together (protocol finding 3): the disk-reading path
// (position.go's toProtocolRange, fed the live file content) and the in-memory
// line-width-table path (workspace.Index.ProtocolRange / protocolRangeVia) must
// produce IDENTICAL protocol ranges for ranges whose lines carry multibyte
// characters, under both encodings. A future edit to one that diverges from the
// other is caught here.
func TestT8_ToProtocolRange_MatchesLineWidthTable(t *testing.T) {
	root := t.TempDir()
	writeLineWidthCorpus(t, root)

	// Cover both files and both the caret (object root) and multibyte-line ranges.
	fixtures := []struct {
		rel  string
		pick func(fa model.FileAnalysis) []model.Range
	}{
		{
			rel: "MULTIB.NSN",
			pick: func(fa model.FileAnalysis) []model.Range {
				var rs []model.Range
				if fa.Structure != nil {
					// Object-root caret (zero-width) + every subroutine name range
					// (on the multibyte-prefixed line).
					rs = append(rs, fa.Structure.SelectionRange)
					for _, c := range fa.Structure.Children {
						if c.Kind == model.SymbolSubroutine {
							rs = append(rs, c.SelectionRange)
						}
					}
				}
				return rs
			},
		},
		{
			rel: "CALLER.NSP",
			pick: func(fa model.FileAnalysis) []model.Range {
				var rs []model.Range
				// Every CALLNAT edge Source (on the multibyte-prefixed line).
				for _, e := range fa.Edges {
					rs = append(rs, e.Source)
				}
				return rs
			},
		},
	}

	for _, enc := range []protocol.PositionEncodingKind{
		protocol.PositionEncodingKindUTF8,
		protocol.PositionEncodingKindUTF16,
	} {
		enc := enc
		t.Run(string(enc), func(t *testing.T) {
			// Build once so the in-memory line-width table is populated.
			hctx := buildLineWidthContext(t, root, enc)

			for _, fx := range fixtures {
				fa, ok := hctx.idx.Get(fx.rel)
				if !ok {
					t.Fatalf("%s not in index", fx.rel)
				}
				ranges := fx.pick(fa)
				if len(ranges) == 0 {
					t.Fatalf("%s produced no ranges to compare", fx.rel)
				}
				content, err := os.ReadFile(filepath.Join(root, fx.rel))
				if err != nil {
					t.Fatalf("read %s: %v", fx.rel, err)
				}
				for _, r := range ranges {
					// Disk-reading path (position.go).
					fromDisk := toProtocolRange(r, string(content), enc)
					// In-memory line-width-table path (workspace.Index).
					fromTable := indexProtocolRange(hctx.idx, fx.rel, r, enc)
					if fromDisk != fromTable {
						t.Errorf("enc=%s %s range %+v: toProtocolRange(disk)=%+v != lineWidthTable=%+v",
							enc, fx.rel, r, fromDisk, fromTable)
					}
				}
			}
		})
	}
}

// findModelSelectionRange finds the model.Range (SelectionRange) that produced a
// returned SymbolInformation, by walking the index Structure for the matching
// path + name + kind. It is the input the oracle re-converts from disk.
func findModelSelectionRange(t *testing.T, hctx *handlerContext, sym protocol.SymbolInformation) model.Range {
	t.Helper()
	rel, err := filepath.Rel(hctx.root, sym.Location.URI.FsPath())
	if err != nil {
		t.Fatalf("rel path: %v", err)
	}
	fa, ok := hctx.idx.Get(rel)
	if !ok || fa.Structure == nil {
		t.Fatalf("no structure for %s", rel)
	}
	if fa.Structure.Name == sym.Name && sym.Kind == protocol.SymbolKindModule {
		return fa.Structure.SelectionRange
	}
	for _, c := range fa.Structure.Children {
		if c.Kind == model.SymbolSubroutine && c.Name == sym.Name {
			return c.SelectionRange
		}
	}
	t.Fatalf("could not find model range for symbol %q in %s", sym.Name, rel)
	return model.Range{}
}

func deleteSourceFiles(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"MULTIB.NSN", "CALLER.NSP"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatalf("remove %s: %v", name, err)
		}
	}
}

func symbolsEqual(a, b []protocol.SymbolInformation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Kind != b[i].Kind ||
			a[i].Location.URI != b[i].Location.URI || a[i].Location.Range != b[i].Location.Range {
			return false
		}
	}
	return true
}

func locationsEqual(a, b []protocol.Location) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].URI != b[i].URI || a[i].Range != b[i].Range {
			return false
		}
	}
	return true
}
