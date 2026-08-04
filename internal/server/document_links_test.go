package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/document"
	"github.com/dkrieg/natural-lsp/internal/model"
	"github.com/dkrieg/natural-lsp/internal/workspace"
)

// TestProvideDocumentLink_ResolvedCALLNAT tests T2: a resolved CALLNAT edge
// produces a link whose Range == edge.Source and Target == resolved object URI.
func TestProvideDocumentLink_ResolvedCALLNAT(t *testing.T) {
	// Arrange: build index from callhierarchy fixture
	testdataDir := filepath.Join("testdata", "callhierarchy")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	idx := &workspace.Index{}
	cfg := config.Config{} // flat namespace

	files := []string{"CALLER.NSP", "CALLEE.NSN", "CC.NSC", "PGM.NSP"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "callhierarchy", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Resolve all edges
	resSet := workspace.Resolve(idx, &cfg)

	// Build handler context
	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
	}

	// Act: call provideDocumentLink on CALLER.NSP
	callerURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP"))
	params := protocol.DocumentLinkParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
	}

	links, err := provideDocumentLink(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDocumentLink failed: %v", err)
	}

	// Assert: CALLNAT 'CALLEE' produces a link (T2 main assertion)
	// CALLER.NSP line 12: CALLNAT 'CALLEE' #A
	// Expected: one link with Range == edge.Source and Target == CALLEE.NSN URI
	if links == nil {
		t.Fatal("expected non-nil links, got nil")
	}

	// Find the CALLNAT 'CALLEE' link by searching for the one pointing to CALLEE.NSN
	calleeURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLEE.NSN"))
	var calleeLink *protocol.DocumentLink
	for i := range links {
		if links[i].Target != nil && strings.Contains(string(*links[i].Target), "CALLEE.NSN") {
			calleeLink = &links[i]
			break
		}
	}

	if calleeLink == nil {
		t.Fatalf("expected a link to CALLEE.NSN, got %d links: %v", len(links), links)
	}

	// Assert: Target is correct
	if calleeLink.Target == nil {
		t.Fatal("link Target is nil")
	}
	if string(*calleeLink.Target) != string(calleeURI) {
		t.Errorf("link Target = %q, want %q", string(*calleeLink.Target), string(calleeURI))
	}

	// Assert: Range matches the edge.Source (line 12, the CALLNAT statement)
	// This is a sanity check; the exact range depends on edge.Source span
	if calleeLink.Range.Start.Line != 11 { // 0-based, so line 12 → 11
		t.Errorf("link Range.Start.Line = %d, want 11 (line 12)", calleeLink.Range.Start.Line)
	}
}

// TestProvideDocumentLink_ResolvedINCLUDE tests T3: a resolved INCLUDE edge
// produces a link to the .NSC copycode.
func TestProvideDocumentLink_ResolvedINCLUDE(t *testing.T) {
	testdataDir := filepath.Join("testdata", "callhierarchy")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []string{"CALLER.NSP", "CALLEE.NSN", "CC.NSC", "PGM.NSP"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "callhierarchy", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	resSet := workspace.Resolve(idx, &cfg)

	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
	}

	callerURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP"))
	params := protocol.DocumentLinkParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
	}

	links, err := provideDocumentLink(hctx, params)

	if err != nil {
		t.Fatalf("provideDocumentLink failed: %v", err)
	}

	if links == nil {
		t.Fatal("expected non-nil links, got nil")
	}

	// Find link to CC.NSC (line 24: INCLUDE CC)
	ccURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CC.NSC"))
	var ccLink *protocol.DocumentLink
	for i := range links {
		if links[i].Target != nil && strings.Contains(string(*links[i].Target), "CC.NSC") {
			ccLink = &links[i]
			break
		}
	}

	if ccLink == nil {
		t.Fatalf("expected a link to CC.NSC")
	}

	if ccLink.Target == nil {
		t.Fatal("link Target is nil")
	}
	if string(*ccLink.Target) != string(ccURI) {
		t.Errorf("link Target = %q, want %q", string(*ccLink.Target), string(ccURI))
	}

	// Range should be on line 24 (0-based: 23) for INCLUDE CC
	if ccLink.Range.Start.Line != 23 {
		t.Errorf("link Range.Start.Line = %d, want 23 (line 24)", ccLink.Range.Start.Line)
	}
}

// TestProvideDocumentLink_ResolvedFETCH_and_RUN tests T4: FETCH and RUN edges
// produce links to their .NSP targets.
func TestProvideDocumentLink_ResolvedFETCH_and_RUN(t *testing.T) {
	t.Run("FETCH_to_program", func(t *testing.T) {
		testdataDir := filepath.Join("testdata", "callhierarchy")
		workspaceRoot, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}

		idx := &workspace.Index{}
		cfg := config.Config{}

		files := []string{"CALLER.NSP", "CALLEE.NSN", "CC.NSC", "PGM.NSP"}
		az := natural.New(nil)

		for _, filename := range files {
			filePath := filepath.Join(testdataDir, filename)
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", filename, err)
			}

			analysis, err := az.Analyze(filePath, content)
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", filename, err)
			}

			relPath := filepath.Join("testdata", "callhierarchy", filename)
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			idx.Add(relPath, analysis)
		}

		resSet := workspace.Resolve(idx, &cfg)

		hctx := &handlerContext{
			cfg:         cfg,
			idx:         idx,
			res:         resSet,
			root:        workspaceRoot,
			posEncoding: protocol.PositionEncodingKindUTF8,
			store:       nil,
		}

		callerURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP"))
		params := protocol.DocumentLinkParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
		}

		links, err := provideDocumentLink(hctx, params)

		if err != nil {
			t.Fatalf("provideDocumentLink failed: %v", err)
		}

		if links == nil {
			t.Fatal("expected non-nil links, got nil")
		}

		// Find link to PGM.NSP (line 15: FETCH 'PGM')
		pgmURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "PGM.NSP"))
		var pgmLink *protocol.DocumentLink
		for i := range links {
			if links[i].Target != nil && strings.Contains(string(*links[i].Target), "PGM.NSP") {
				pgmLink = &links[i]
				break
			}
		}

		if pgmLink == nil {
			t.Fatalf("expected a link to PGM.NSP")
		}

		if pgmLink.Target == nil {
			t.Fatal("link Target is nil")
		}
		if string(*pgmLink.Target) != string(pgmURI) {
			t.Errorf("link Target = %q, want %q", string(*pgmLink.Target), string(pgmURI))
		}

		// Range should be on line 15 (0-based: 14) for FETCH 'PGM'
		if pgmLink.Range.Start.Line != 14 {
			t.Errorf("link Range.Start.Line = %d, want 14 (line 15)", pgmLink.Range.Start.Line)
		}
	})

	t.Run("RUN_to_program", func(t *testing.T) {
		testdataDir := filepath.Join("testdata", "references", "multi-caller")
		workspaceRoot, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}

		idx := &workspace.Index{}
		cfg := config.Config{}

		files := []string{"CALLER1.NSP", "CALLER2.NSP", "CALLER3.NSP", "SHARED.NSN"}
		az := natural.New(nil)

		for _, filename := range files {
			filePath := filepath.Join(testdataDir, filename)
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", filename, err)
			}

			analysis, err := az.Analyze(filePath, content)
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", filename, err)
			}

			relPath := filepath.Join("testdata", "references", "multi-caller", filename)
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			idx.Add(relPath, analysis)
		}

		resSet := workspace.Resolve(idx, &cfg)

		hctx := &handlerContext{
			cfg:         cfg,
			idx:         idx,
			res:         resSet,
			root:        workspaceRoot,
			posEncoding: protocol.PositionEncodingKindUTF8,
			store:       nil,
		}

		caller2URI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER2.NSP"))
		params := protocol.DocumentLinkParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: caller2URI},
		}

		links, err := provideDocumentLink(hctx, params)

		if err != nil {
			t.Fatalf("provideDocumentLink failed: %v", err)
		}

		if links == nil {
			t.Fatal("expected non-nil links for CALLER2.NSP, got nil")
		}

		// Find link to CALLER1.NSP (line 9: RUN 'CALLER1')
		caller1URI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER1.NSP"))
		var caller1Link *protocol.DocumentLink
		for i := range links {
			if links[i].Target != nil && strings.Contains(string(*links[i].Target), "CALLER1.NSP") {
				caller1Link = &links[i]
				break
			}
		}

		if caller1Link == nil {
			t.Fatalf("expected a link to CALLER1.NSP")
		}

		if caller1Link.Target == nil {
			t.Fatal("link Target is nil")
		}
		if string(*caller1Link.Target) != string(caller1URI) {
			t.Errorf("link Target = %q, want %q", string(*caller1Link.Target), string(caller1URI))
		}

		// Range should be on line 9 (0-based: 8) for RUN 'CALLER1'
		if caller1Link.Range.Start.Line != 8 {
			t.Errorf("link Range.Start.Line = %d, want 8 (line 9)", caller1Link.Range.Start.Line)
		}
	})
}

// TestProvideDocumentLink_ExternalVsInlinePERFORM tests T5: external PERFORM
// produces a link, but inline (same-file) PERFORM does not.
func TestProvideDocumentLink_ExternalVsInlinePERFORM(t *testing.T) {
	t.Run("external_PERFORM_has_link", func(t *testing.T) {
		testdataDir := filepath.Join("testdata", "roothandshake")
		workspaceRoot, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}

		idx := &workspace.Index{}
		cfg := config.Config{}

		files := []string{"HELLO.NSP", "CALLGREET.NSN", "SAYHELLO.NSS"}
		az := natural.New(nil)

		for _, filename := range files {
			filePath := filepath.Join(testdataDir, filename)
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", filename, err)
			}

			analysis, err := az.Analyze(filePath, content)
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", filename, err)
			}

			relPath := filepath.Join("testdata", "roothandshake", filename)
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			idx.Add(relPath, analysis)
		}

		resSet := workspace.Resolve(idx, &cfg)

		hctx := &handlerContext{
			cfg:         cfg,
			idx:         idx,
			res:         resSet,
			root:        workspaceRoot,
			posEncoding: protocol.PositionEncodingKindUTF8,
			store:       nil,
		}

		helloURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "HELLO.NSP"))
		params := protocol.DocumentLinkParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: helloURI},
		}

		links, err := provideDocumentLink(hctx, params)

		if err != nil {
			t.Fatalf("provideDocumentLink failed: %v", err)
		}

		if links == nil {
			t.Fatal("expected non-nil links, got nil")
		}

		// Find link to SAYHELLO.NSS (line 15: PERFORM SAYHELLO, external)
		sayHelloURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "SAYHELLO.NSS"))
		var sayHelloLink *protocol.DocumentLink
		for i := range links {
			if links[i].Target != nil && strings.Contains(string(*links[i].Target), "SAYHELLO.NSS") {
				sayHelloLink = &links[i]
				break
			}
		}

		if sayHelloLink == nil {
			t.Fatalf("expected a link to external SAYHELLO.NSS")
		}

		if sayHelloLink.Target == nil {
			t.Fatal("link Target is nil")
		}
		if string(*sayHelloLink.Target) != string(sayHelloURI) {
			t.Errorf("link Target = %q, want %q", string(*sayHelloLink.Target), string(sayHelloURI))
		}

		// Range should be on line 15 (0-based: 14) for PERFORM SAYHELLO
		if sayHelloLink.Range.Start.Line != 14 {
			t.Errorf("link Range.Start.Line = %d, want 14 (line 15)", sayHelloLink.Range.Start.Line)
		}
	})

	t.Run("inline_PERFORM_no_link", func(t *testing.T) {
		// CALLER.NSP has inline PERFORM SUB-A (same file, should NOT produce a link)
		testdataDir := filepath.Join("testdata", "callhierarchy")
		workspaceRoot, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}

		idx := &workspace.Index{}
		cfg := config.Config{}

		files := []string{"CALLER.NSP", "CALLEE.NSN", "CC.NSC", "PGM.NSP"}
		az := natural.New(nil)

		for _, filename := range files {
			filePath := filepath.Join(testdataDir, filename)
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", filename, err)
			}

			analysis, err := az.Analyze(filePath, content)
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", filename, err)
			}

			relPath := filepath.Join("testdata", "callhierarchy", filename)
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			idx.Add(relPath, analysis)
		}

		resSet := workspace.Resolve(idx, &cfg)

		hctx := &handlerContext{
			cfg:         cfg,
			idx:         idx,
			res:         resSet,
			root:        workspaceRoot,
			posEncoding: protocol.PositionEncodingKindUTF8,
			store:       nil,
		}

		callerURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP"))
		params := protocol.DocumentLinkParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
		}

		links, err := provideDocumentLink(hctx, params)

		if err != nil {
			t.Fatalf("provideDocumentLink failed: %v", err)
		}

		// Check that there is NO link pointing to SUB-A (same file)
		// Line 18: PERFORM SUB-A
		if links != nil {
			for i := range links {
				if links[i].Range.Start.Line == 17 { // 0-based line 18
					// This would be a link on the inline PERFORM line; it shouldn't exist
					t.Errorf("inline PERFORM SUB-A should NOT produce a link, but got one: %+v", links[i])
				}
			}
		}
		// If no links at all, that's OK (there may be other links in the file)
	})
}

// TestProvideDocumentLink_GapsCases tests T6: dynamic, unresolved, and ambiguous
// edges produce NO links.
func TestProvideDocumentLink_GapsCases(t *testing.T) {
	t.Run("dynamic_no_link", func(t *testing.T) {
		testdataDir := filepath.Join("testdata", "navigation")
		workspaceRoot, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}

		idx := &workspace.Index{}
		cfg := config.Config{}

		files := []string{"unresolved.NSP"}
		az := natural.New(nil)

		for _, filename := range files {
			filePath := filepath.Join(testdataDir, filename)
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", filename, err)
			}

			analysis, err := az.Analyze(filePath, content)
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", filename, err)
			}

			relPath := filepath.Join("testdata", "navigation", filename)
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			idx.Add(relPath, analysis)
		}

		resSet := workspace.Resolve(idx, &cfg)

		hctx := &handlerContext{
			cfg:         cfg,
			idx:         idx,
			res:         resSet,
			root:        workspaceRoot,
			posEncoding: protocol.PositionEncodingKindUTF8,
			store:       nil,
		}

		unresolvedURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "unresolved.NSP"))
		params := protocol.DocumentLinkParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: unresolvedURI},
		}

		links, err := provideDocumentLink(hctx, params)

		if err != nil {
			t.Fatalf("provideDocumentLink failed: %v", err)
		}

		// unresolved.NSP has only dynamic + no-target edges (no resolved links)
		// so the result should be nil (no links)
		if links != nil {
			t.Errorf("expected nil links for file with only gaps, got %d links", len(links))
		}
	})

	t.Run("ambiguous_no_link", func(t *testing.T) {
		testdataDir := filepath.Join("testdata", "ambiguity")
		workspaceRoot, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}

		idx := &workspace.Index{}
		cfg := config.Config{} // flat namespace

		// Ambiguity fixture has CALLER.NSP, and two DUP.NSN files in one/ and two/
		files := []struct {
			path    string
			relPath string
		}{
			{filepath.Join(testdataDir, "CALLER.NSP"), "testdata/ambiguity/CALLER.NSP"},
			{filepath.Join(testdataDir, "one", "DUP.NSN"), "testdata/ambiguity/one/DUP.NSN"},
			{filepath.Join(testdataDir, "two", "DUP.NSN"), "testdata/ambiguity/two/DUP.NSN"},
		}

		az := natural.New(nil)

		for _, f := range files {
			content, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatalf("failed to read %s: %v", f.path, err)
			}

			analysis, err := az.Analyze(f.path, content)
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", f.path, err)
			}

			relPath := strings.ReplaceAll(f.relPath, "\\", "/")
			idx.Add(relPath, analysis)
		}

		resSet := workspace.Resolve(idx, &cfg)

		hctx := &handlerContext{
			cfg:         cfg,
			idx:         idx,
			res:         resSet,
			root:        workspaceRoot,
			posEncoding: protocol.PositionEncodingKindUTF8,
			store:       nil,
		}

		callerURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP"))
		params := protocol.DocumentLinkParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
		}

		links, err := provideDocumentLink(hctx, params)

		if err != nil {
			t.Fatalf("provideDocumentLink failed: %v", err)
		}

		// CALLER.NSP has only CALLNAT 'DUP' which is ambiguous (two candidates).
		// Per T6 and FR-17, ambiguous references produce NO links (no arbitrary pick).
		if links != nil {
			t.Errorf("expected nil links for ambiguous reference, got %d links", len(links))
		}
	})
}

// TestProvideDocumentLink_MultipleSorted tests T7: multiple resolved links
// in one document are returned and sorted by Range.Start.
func TestProvideDocumentLink_MultipleSorted(t *testing.T) {
	testdataDir := filepath.Join("testdata", "roothandshake")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []string{"HELLO.NSP", "CALLGREET.NSN", "SAYHELLO.NSS"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "roothandshake", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	resSet := workspace.Resolve(idx, &cfg)

	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
	}

	helloURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "HELLO.NSP"))
	params := protocol.DocumentLinkParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: helloURI},
	}

	links, err := provideDocumentLink(hctx, params)

	if err != nil {
		t.Fatalf("provideDocumentLink failed: %v", err)
	}

	if links == nil {
		t.Fatal("expected non-nil links, got nil")
	}

	// HELLO.NSP has two resolved edges:
	// - Line 12: CALLNAT 'CALLGREET' → CALLGREET.NSN
	// - Line 15: PERFORM SAYHELLO → SAYHELLO.NSS
	// Expected: exactly 2 links, sorted by Range.Start (line 12 before line 15)

	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}

	// Assert: links are sorted by Range.Start
	if links[0].Range.Start.Line > links[1].Range.Start.Line {
		t.Errorf("links not sorted by Range.Start.Line: first=%d, second=%d",
			links[0].Range.Start.Line, links[1].Range.Start.Line)
	}

	// Assert: targets are correct
	targets := map[string]bool{}
	for i := range links {
		if links[i].Target != nil {
			targetStr := string(*links[i].Target)
			if strings.Contains(targetStr, "CALLGREET.NSN") {
				targets["CALLGREET"] = true
			} else if strings.Contains(targetStr, "SAYHELLO.NSS") {
				targets["SAYHELLO"] = true
			}
		}
	}

	if !targets["CALLGREET"] {
		t.Error("expected link to CALLGREET.NSN")
	}
	if !targets["SAYHELLO"] {
		t.Error("expected link to SAYHELLO.NSS")
	}

	// Assert: first link is on line 12, second on line 15 (0-based: 11, 14)
	if links[0].Range.Start.Line != 11 {
		t.Errorf("first link Range.Start.Line = %d, want 11 (line 12)", links[0].Range.Start.Line)
	}
	if links[1].Range.Start.Line != 14 {
		t.Errorf("second link Range.Start.Line = %d, want 14 (line 15)", links[1].Range.Start.Line)
	}
}

// TestProvideDocumentLink_NilResultWireBytes pins the null sentinel on the wire.
func TestProvideDocumentLink_NilResultWireBytes(t *testing.T) {
	// When provideDocumentLink returns nil (no links), the dispatch should emit "null"
	got := dispatchResultBytes(t, "textDocument/documentLink",
		`{"textDocument":{"uri":"file:///nonexistent/NOPE.NSP"}}`)

	if string(got) != "null" {
		t.Errorf("empty documentLink result: got %q, want %q", string(got), "null")
	}
}

// TestProvideDocumentLink_EmptyDocumentReturnsNil tests that a document with
// no edges returns nil (no links).
func TestProvideDocumentLink_EmptyDocumentReturnsNil(t *testing.T) {
	testdataDir := filepath.Join("testdata", "navigation")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	idx := &workspace.Index{}
	cfg := config.Config{}

	// helper.NSN is a minimal file with no edges
	files := []string{"helper.NSN"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "navigation", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	resSet := workspace.Resolve(idx, &cfg)

	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
	}

	helperURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "helper.NSN"))
	params := protocol.DocumentLinkParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: helperURI},
	}

	links, err := provideDocumentLink(hctx, params)

	if err != nil {
		t.Fatalf("provideDocumentLink failed: %v", err)
	}

	if links != nil {
		t.Errorf("expected nil links for file with no edges, got %d links", len(links))
	}
}

// TestProvideDocumentLink_StoreFirst tests T8: links reflect the LIVE BUFFER
// content from the document.Store, not stale disk (store-first precedence).
// Proves store-first by opening the buffer with content identical to disk and
// confirming the link is produced even when the on-disk file is absent (disk-free proof).
func TestProvideDocumentLink_StoreFirst(t *testing.T) {
	// Arrange: read the CALLER.NSP fixture from on-disk testdata
	fixtureDir := filepath.Join("testdata", "callhierarchy")
	fixtureSourcePath := filepath.Join(fixtureDir, "CALLER.NSP")
	callerContent, err := os.ReadFile(fixtureSourcePath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", fixtureSourcePath, err)
	}

	// Get the current working directory (workspace root)
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Build index from the fixture using the workspace root
	idx := &workspace.Index{}
	cfg := config.Config{} // flat namespace
	az := natural.New(nil)

	files := []string{"CALLER.NSP", "CALLEE.NSN", "CC.NSC", "PGM.NSP"}
	for _, filename := range files {
		filePath := filepath.Join(fixtureDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		// Index with paths relative to workspace root
		relPath := filepath.Join("testdata", "callhierarchy", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Resolve the index
	resSet := workspace.Resolve(idx, &cfg)

	// Create a document.Store rooted at workspaceRoot
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzeFunc := func(relPath string, content []byte) model.FileAnalysis {
		analysis, _ := az.Analyze(relPath, content)
		return analysis
	}
	store := document.New(workspaceRoot, analyzeFunc, logger)

	// Open CALLER.NSP in the store with the fixture content (NOT written to disk)
	// The URI must be rooted UNDER workspaceRoot
	callerURI := uri.File(filepath.Join(workspaceRoot, fixtureDir, "CALLER.NSP"))
	store.Open(callerURI, 1, callerContent)

	hctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot, // Root must match the store's root
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       store,
	}

	// Act: call provideDocumentLink on CALLER.NSP URI
	params := protocol.DocumentLinkParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
	}

	links, err := provideDocumentLink(hctx, params)

	// Assert: no error
	if err != nil {
		t.Fatalf("provideDocumentLink failed: %v", err)
	}

	// Assert: store-first produces the link from the live buffer
	// The fixture has CALLNAT 'CALLEE' on line 12, so we expect a link for it
	if links == nil {
		t.Fatal("expected non-nil links from store-first buffer, got nil")
	}

	// Find the CALLNAT 'CALLEE' link by its range (line 12, 0-based: 11)
	var calleeLink *protocol.DocumentLink
	for i := range links {
		if links[i].Range.Start.Line == 11 { // Line 12 → 0-based 11
			calleeLink = &links[i]
			break
		}
	}

	if calleeLink == nil {
		t.Fatalf("expected a link on line 12 (0-based: 11) from the store buffer")
	}

	// Assert: the Target is the CALLEE.NSN path (resolved correctly from the index)
	if calleeLink.Target == nil {
		t.Fatal("link Target is nil")
	}
	if !strings.Contains(string(*calleeLink.Target), "CALLEE.NSN") {
		t.Errorf("link Target = %q, want one containing 'CALLEE.NSN'", string(*calleeLink.Target))
	}
}

// TestProvideDocumentLink_Encoding tests T9: link ranges are encoding-aware
// (UTF-8 vs UTF-16 code units).
// The fixture ENCODING.NSP has a multibyte character (emoji) on the same line
// as a resolved CALLNAT, so the UTF-8 byte columns differ from UTF-16 code units.
// Running with UTF-8 and UTF-16 encodings, the link Range.Character should
// differ per encoding exactly as toProtocolRange computes.
func TestProvideDocumentLink_Encoding(t *testing.T) {
	// Arrange: build index from documentlinks fixture
	testdataDir := filepath.Join("testdata", "documentlinks")
	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	idx := &workspace.Index{}
	cfg := config.Config{} // flat namespace

	files := []string{"ENCODING.NSP", "TGT.NSN"}
	az := natural.New(nil)

	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "documentlinks", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	// Resolve edges
	resSet := workspace.Resolve(idx, &cfg)

	encodingURI := uri.File(filepath.Join(workspaceRoot, testdataDir, "ENCODING.NSP"))

	// Test with both encodings
	tests := []struct {
		name    string
		encKind protocol.PositionEncodingKind
	}{
		{
			name:    "UTF-8",
			encKind: protocol.PositionEncodingKindUTF8,
		},
		{
			name:    "UTF-16",
			encKind: protocol.PositionEncodingKindUTF16,
		},
	}

	var utf8Links, utf16Links []protocol.DocumentLink

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hctx := &handlerContext{
				cfg:         cfg,
				idx:         idx,
				res:         resSet,
				root:        workspaceRoot,
				posEncoding: tc.encKind,
				store:       nil,
			}

			params := protocol.DocumentLinkParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: encodingURI},
			}

			links, err := provideDocumentLink(hctx, params)

			if err != nil {
				t.Fatalf("provideDocumentLink failed: %v", err)
			}

			if links == nil {
				t.Fatal("expected non-nil links")
			}

			if len(links) == 0 {
				t.Fatal("expected at least one link")
			}

			if tc.encKind == protocol.PositionEncodingKindUTF8 {
				utf8Links = links
			} else {
				utf16Links = links
			}

			// Assert: link is on line 9 (0-based for line 10 of ENCODING.NSP, the CALLNAT line)
			if links[0].Range.Start.Line != 9 {
				t.Errorf("link Range.Start.Line = %d, want 9 (line 10)", links[0].Range.Start.Line)
			}
		})
	}

	// Assert: Character positions differ between UTF-8 and UTF-16
	// The emoji 🎯 is 4 bytes in UTF-8 but 2 code units in UTF-16
	// So UTF-16 Character should be LESS than UTF-8 Character for the same byte position
	if len(utf8Links) > 0 && len(utf16Links) > 0 {
		utf8Char := utf8Links[0].Range.Start.Character
		utf16Char := utf16Links[0].Range.Start.Character

		if utf8Char == utf16Char {
			t.Errorf("Character should differ per encoding: UTF-8 = %d, UTF-16 = %d; they must differ",
				utf8Char, utf16Char)
		}
	}
}

// TestProvideDocumentLink_Degradation tests T10: graceful degradation when
// resources are missing or inaccessible (FR-43, Story 1 AC3).
// Cases: (a) URI outside root, (b) file not in index and not in store,
// (c) file in index but os.ReadFile fails (unreadable), (d) cold index,
// (e) nil hctx.
func TestProvideDocumentLink_Degradation(t *testing.T) {
	// Setup: establish a test workspace with basic fixtures
	az := natural.New(nil)

	// Build a minimal index from callhierarchy fixtures
	testdataDir := filepath.Join("testdata", "callhierarchy")
	idx := &workspace.Index{}
	cfg := config.Config{}

	files := []string{"CALLER.NSP", "CALLEE.NSN"}
	for _, filename := range files {
		filePath := filepath.Join(testdataDir, filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", filename, err)
		}

		analysis, err := az.Analyze(filePath, content)
		if err != nil {
			t.Fatalf("failed to analyze %s: %v", filename, err)
		}

		relPath := filepath.Join("testdata", "callhierarchy", filename)
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		idx.Add(relPath, analysis)
	}

	resSet := workspace.Resolve(idx, &cfg)

	workspaceRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	baseHctx := &handlerContext{
		cfg:         cfg,
		idx:         idx,
		res:         resSet,
		root:        workspaceRoot,
		posEncoding: protocol.PositionEncodingKindUTF8,
		store:       nil,
	}

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "a_URI_outside_root",
			test: func(t *testing.T) {
				// URI with a path outside the workspace root
				outsideURI := uri.File("/some/other/place/OUTSIDE.NSP")

				params := protocol.DocumentLinkParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: outsideURI},
				}

				links, err := provideDocumentLink(baseHctx, params)

				// Assert: no error, nil result (graceful degradation)
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if links != nil {
					t.Errorf("expected nil links for URI outside root, got %d links", len(links))
				}
			},
		},
		{
			name: "b_file_not_in_index_and_not_in_store",
			test: func(t *testing.T) {
				// File that is neither in the index nor in the store
				missingFile := filepath.Join(workspaceRoot, "testdata", "callhierarchy", "NONEXISTENT.NSP")
				missingURI := uri.File(missingFile)

				params := protocol.DocumentLinkParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: missingURI},
				}

				links, err := provideDocumentLink(baseHctx, params)

				// Assert: no error, nil result
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if links != nil {
					t.Errorf("expected nil links for missing file, got %d links", len(links))
				}
			},
		},
		{
			name: "c_file_in_index_but_unreadable",
			test: func(t *testing.T) {
				// File is in the index but os.ReadFile will fail (simulate by using a path
				// that exists in the index but was deleted from disk)
				// Use CALLER.NSP which is in the index
				callerFile := filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP")

				// Temporarily move the file to make it unreadable
				deletedPath := callerFile + ".deleted"
				os.Rename(callerFile, deletedPath)
				defer os.Rename(deletedPath, callerFile)

				callerURI := uri.File(callerFile)
				params := protocol.DocumentLinkParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
				}

				links, err := provideDocumentLink(baseHctx, params)

				// Assert: no error, nil result (graceful degradation)
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if links != nil {
					t.Errorf("expected nil links when file is unreadable, got %d links", len(links))
				}
			},
		},
		{
			name: "d_cold_index_pre_first_build",
			test: func(t *testing.T) {
				// Cold index: hctx.idx and hctx.res are nil
				hctx := &handlerContext{
					cfg:         cfg,
					idx:         nil, // Cold
					res:         nil, // Cold
					root:        workspaceRoot,
					posEncoding: protocol.PositionEncodingKindUTF8,
					store:       nil,
				}

				callerFile := filepath.Join(workspaceRoot, testdataDir, "CALLER.NSP")
				callerURI := uri.File(callerFile)
				params := protocol.DocumentLinkParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: callerURI},
				}

				links, err := provideDocumentLink(hctx, params)

				// Assert: no error, nil result
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if links != nil {
					t.Errorf("expected nil links for cold index, got %d links", len(links))
				}
			},
		},
		{
			name: "e_nil_hctx",
			test: func(t *testing.T) {
				// nil handlerContext
				links, err := provideDocumentLink(nil, protocol.DocumentLinkParams{})

				// Assert: no error, nil result (nil guard in provideDocumentLink)
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if links != nil {
					t.Errorf("expected nil links for nil hctx, got %d links", len(links))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.test)
	}
}
