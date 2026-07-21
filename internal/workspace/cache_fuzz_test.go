package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkrieg/natural-lsp/internal/model"
)

// FuzzLoadCache is the robustness fuzz target for Load and decodeCache (FR-43).
// It asserts that Load never panics on arbitrary cache bytes — the invariant is
// non-panic only; any return-value combination is acceptable (nil idx + error,
// nil idx + stale list, etc. all route to cold rebuild at index.go:654/659).
// The seed corpus covers: valid gzip-of-valid-JSON, gzip magic + truncated body,
// plaintext valid JSON, plaintext garbage, empty input, and bare gzip magic.
// Feature 24 / T7.
func FuzzLoadCache(f *testing.F) {
	// Helper to encode a simple CacheFile as gzip.
	encodeGzip := func() []byte {
		cache := CacheFile{
			Version: cacheFormatVersion,
			Entries: map[string]cacheEntry{
				"test.NSP": {
					ObjectType:  string(model.ObjectProgram),
					Symbols:     []model.SymbolEntry{},
					Edges:       []model.EdgeEntry{},
					DataAccess:  []model.DataAccessEntry{},
					Definitions: []model.DataDefinition{},
					WorkFiles:   []model.WorkFile{},
					HostVarRefs: []model.HostVarRef{},
					Structure:   nil,
					ContentHash: "abc123",
				},
			},
		}
		data, err := encodeCache(cache)
		if err != nil {
			f.Fatalf("encodeGzip helper failed: %v", err)
		}
		return data
	}

	// Seed 1: valid gzip-of-valid-JSON (current format cache)
	f.Add(encodeGzip())

	// Seed 2: gzip magic + truncated body (invalid gzip stream)
	f.Add([]byte{0x1f, 0x8b, 0x08, 0x00, 0x01, 0x02})

	// Seed 3: plaintext valid JSON (legacy pre-gzip cache)
	plainJSON := CacheFile{
		Version: cacheFormatVersion,
		Entries: map[string]cacheEntry{
			"legacy.NSP": {
				ObjectType:  string(model.ObjectProgram),
				ContentHash: "xyz789",
			},
		},
	}
	plainBytes, err := json.Marshal(plainJSON)
	if err != nil {
		f.Fatalf("plaintext JSON helper failed: %v", err)
	}
	f.Add(plainBytes)

	// Seed 4: plaintext garbage (no gzip magic, not JSON)
	f.Add([]byte("{ this is not json at all"))

	// Seed 5: empty input
	f.Add([]byte{})

	// Seed 6: bare gzip magic (incomplete header)
	f.Add([]byte{0x1f, 0x8b})

	// Fuzz body
	f.Fuzz(func(t *testing.T, data []byte) {
		// Arrange: write fuzz input to a temp cache file
		tmpDir := t.TempDir()
		cachePath := filepath.Join(tmpDir, "cache.bin")
		if err := os.WriteFile(cachePath, data, 0o600); err != nil {
			t.Fatalf("write fuzz input: %v", err)
		}

		// Act: call Load with empty hashes and nil logger
		// The only invariant is: no panic. Any return-value combination is acceptable.
		idx, stale, err := Load(cachePath, map[string]string{}, nil)

		// Assert: Load returned without panicking (the fuzzer catches panics).
		// We do not assert on the returned values — nil idx, non-empty stale,
		// and a non-nil err are all valid outcomes on corrupt input.
		// The contract is met as long as this line is reached:
		_ = idx
		_ = stale
		_ = err
	})
}
