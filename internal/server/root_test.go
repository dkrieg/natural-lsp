package server

import (
	"path/filepath"
	"testing"

	"go.lsp.dev/protocol"
)

// TestResolveRootStart tests the pure resolver that converts initialize params
// to a root-discovery start path (FR-41, feature 20, task T1).
func TestResolveRootStart(t *testing.T) {
	testCases := []struct {
		name        string
		paramsJSON  string // Raw JSON to unmarshal into InitializeParams
		cwdFallback string
		expected    string
		// fromURI marks expectations derived from a file URI's FsPath(), which is
		// OS-native (backslash-separated on Windows). Such expectations are run
		// through filepath.FromSlash so the forward-slash literals below match on
		// every OS. Fallthrough cases return cwdFallback verbatim, so they leave
		// fromURI false and compare literally.
		fromURI bool
	}{
		{
			name: "WorkspaceFoldersWins_IgnoresRootUri",
			paramsJSON: `{
				"workspaceFolders": [{"uri": "file:///ws/a", "name": "WorkspaceA"}],
				"rootUri": "file:///ws/b"
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/ws/a",
			fromURI:     true,
		},
		{
			name: "RootUriUsed_WhenWorkspaceFoldersNull",
			paramsJSON: `{
				"workspaceFolders": null,
				"rootUri": "file:///ws/b"
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/ws/b",
			fromURI:     true,
		},
		{
			name: "CwdFallback_WhenBothAbsent",
			paramsJSON: `{
				"capabilities": {}
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/fallback/cwd",
		},
		{
			name: "CwdFallback_WhenWorkspaceFoldersEmpty",
			paramsJSON: `{
				"workspaceFolders": [],
				"rootUri": "file:///ws/b"
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/ws/b",
			fromURI:     true,
		},
		{
			name: "FallThrough_OnEmptyFsPath",
			paramsJSON: `{
				"workspaceFolders": [{"uri": "untitled:///document", "name": "UntitledDoc"}],
				"rootUri": "file:///ws/b"
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/ws/b",
			fromURI:     true,
		},
		{
			// RFC 8089 single-slash file URI form (file:/abs). The prior
			// strings.HasPrefix(u, "file://") check rejected this spec-legal
			// form, causing it to fall through to the cwd fallback; IsFile()
			// accepts it, so it now resolves to its FsPath.
			name: "SingleSlashFileURI_Resolves",
			paramsJSON: `{
				"workspaceFolders": [{"uri": "file:/ws/x", "name": "SingleSlash"}]
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/ws/x",
			fromURI:     true,
		},
		{
			// A non-file scheme (with no rootUri) must still fall through to
			// the cwd fallback — IsFile() rejects it.
			name: "NonFileScheme_FallsThroughToCwd",
			paramsJSON: `{
				"workspaceFolders": [{"uri": "untitled:///document", "name": "UntitledDoc"}]
			}`,
			cwdFallback: "/fallback/cwd",
			expected:    "/fallback/cwd",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Unmarshal the JSON into InitializeParams using protocol.Unmarshal
			var params protocol.InitializeParams
			if err := protocol.Unmarshal([]byte(tc.paramsJSON), &params); err != nil {
				t.Fatalf("failed to unmarshal test params: %v", err)
			}

			want := tc.expected
			if tc.fromURI {
				// resolveRootStart returns uri.FsPath() output, which is OS-native
				// (backslash-separated on Windows) — it is used as a filesystem
				// start path for config.Bootstrap/FindRoot. Convert the forward-slash
				// literal to the native separator so the assertion holds on Windows
				// without weakening it on Linux/macOS.
				want = filepath.FromSlash(want)
			}
			result := resolveRootStart(params, tc.cwdFallback)
			if result != want {
				t.Errorf("resolveRootStart() = %q, want %q", result, want)
			}
		})
	}
}

// FuzzResolveRootStart guards resolveRootStart against panics on degenerate inputs (FR-43, feature 20, task T1).
func FuzzResolveRootStart(f *testing.F) {
	f.Add(`{"workspaceFolders":[{"uri":"file:///workspace","name":"W"}]}`, "/fallback")
	f.Add(`{"workspaceFolders":[]}`, "/fallback")
	f.Add(`{"workspaceFolders":[{"uri":"untitled:///unsaved","name":"U"}]}`, "/fallback")
	f.Add(`{"workspaceFolders":null,"rootUri":"file:///root"}`, "/fallback")
	f.Add(`{"capabilities":{}}`, "")

	f.Fuzz(func(t *testing.T, paramsJSON string, fallback string) {
		// Unmarshal arbitrary JSON into InitializeParams.
		// The function must not panic, regardless of JSON validity or URI validity.
		var params protocol.InitializeParams
		if err := protocol.Unmarshal([]byte(paramsJSON), &params); err != nil {
			// If JSON is invalid, the test generator itself failed; skip.
			t.Skip()
		}

		// resolveRootStart must never panic, ever.
		_ = resolveRootStart(params, fallback)
	})
}
