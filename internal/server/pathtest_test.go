package server

import (
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/uri"
)

// testFileURI builds a canonical file URI from an OS path via uri.File, which
// formats Windows file URIs correctly (file:///C:/...) — unlike hand-built
// "file://"+path concatenation, which produces an unparseable URI on Windows
// (backslashes + a drive letter). Use this whenever a test needs a rootUri or a
// document URI from a real (temp) directory so the server's FsPath() round-trips
// on every OS.
func testFileURI(t *testing.T, path string) uri.URI {
	t.Helper()
	return uri.File(path)
}

// samePath reports whether two filesystem paths refer to the same location,
// tolerant of Windows path-form variance (drive-letter case, forward-vs-back
// slashes). It does NOT resolve 8.3 short names — for that, canonicalize with
// filepath.EvalSymlinks before comparing, or assert on filepath.Base.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.ToSlash(a), filepath.ToSlash(b))
}
