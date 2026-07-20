package paths

import "testing"

// TestNormalizeKey is the core cross-platform guard for the index keyspace.
// It runs identically on Linux/macOS/Windows because strings.ReplaceAll operates
// on the literal backslash byte, independent of the OS path separator. This is
// exactly why the fix uses ReplaceAll and NOT filepath.ToSlash (which is a no-op
// on backslashes wherever the separator is "/", i.e. on the CI that runs this).
func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "windows backslash path is canonicalized to forward slashes",
			in:   "code\\LIB1\\MYSUB.NSN",
			want: "code/LIB1/MYSUB.NSN",
		},
		{
			name: "already forward-slash path is unchanged",
			in:   "already/slash/MYSUB.NSN",
			want: "already/slash/MYSUB.NSN",
		},
		{
			name: "single-segment path is unchanged",
			in:   "MYSUB.NSN",
			want: "MYSUB.NSN",
		},
		{
			name: "mixed separators are all forward-slashed",
			in:   "code\\LIB1/sub\\MYSUB.NSN",
			want: "code/LIB1/sub/MYSUB.NSN",
		},
		{
			name: "empty string stays empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeKey(tt.in)
			if got != tt.want {
				t.Errorf("NormalizeKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
