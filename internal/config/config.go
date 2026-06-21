// Package config loads and validates the workspace configuration
// (.natural-lsp.toml), supplies defaults, discovers the workspace root by
// walking up to the sentinel file, and exposes the library map and steplib
// search order consumed by workspace/resolution.go.
//
// See docs/plans/natural-lsp-prd.md (FR-1..6, CR-1..6).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config mirrors the .natural-lsp.toml schema (README "Workspace
// configuration"). Each field maps to a documented TOML table/key and has a
// default supplied by [Defaults]; see feature 01-workspace-and-configuration
// (FR-1, FR-2, FR-3, FR-4, FR-6).
type Config struct {
	// Workspace holds the indexing controls (extensions, exclude,
	// max_file_size). TOML table: [workspace]. README lines 231–252.
	Workspace WorkspaceConfig `toml:"workspace"`

	// Cache configures the on-disk workspace index cache. TOML table: [cache].
	Cache CacheConfig `toml:"cache"`

	// Analysis configures extraction behavior. TOML table: [analysis].
	Analysis AnalysisConfig `toml:"analysis"`

	// Resolution holds the library map and steplib search order. TOML table:
	// [resolution].
	Resolution ResolutionConfig `toml:"resolution"`
}

// WorkspaceConfig holds the indexing controls. TOML table: [workspace] (README
// lines 231–252).
type WorkspaceConfig struct {
	// Extensions is the ordered set of Natural object-file extensions to
	// index. TOML key: extensions. Default: the fifteen-element documented set
	// including core types (.NSP .NSN .NSS .NSC .NSM .NSL .NSG .NSA .NSH .NSD)
	// and extended types (.NS4 .NS7 .NS3 .NS8 .NST).
	Extensions []string `toml:"extensions"`

	// Exclude is the list of workspace-relative directory names skipped
	// during indexing. TOML key: exclude. Default: ["archive", "backup",
	// ".git"].
	Exclude []string `toml:"exclude"`

	// MaxFileSize is the upper bound, in bytes, on a file the indexer will
	// read; larger files are skipped (the limit is int64 so future values
	// beyond the int32 range remain representable). TOML key: max_file_size.
	// Default: 5_000_000.
	MaxFileSize int64 `toml:"max_file_size"`

	// ExtensionTypes maps additional file extensions to their object type
	// classifications. TOML table: [extension_types]. Entries are validated:
	// invalid values fall back to being dropped and reported as a Problem
	// (CR-6). Default: empty.
	ExtensionTypes map[string]string `toml:"extension_types"`
}

// CacheConfig configures the on-disk workspace index cache. TOML table:
// [cache].
type CacheConfig struct {
	// Path is the workspace-relative directory where the serialized index
	// cache is written. TOML key: path. Default: ".natural-lsp-cache".
	Path string `toml:"path"`
}

// AnalysisConfig configures extraction behavior. TOML table: [analysis]. It
// holds the two CR-4 controls over how dynamic CALLNAT/PERFORM/FETCH targets
// are modeled.
type AnalysisConfig struct {
	// FlagDynamicCalls controls whether a dynamic call (e.g. CALLNAT
	// #VARIABLE) is treated as a modeled dependency rather than discarded:
	// when true, an unresolvable target becomes a CALLS_DYNAMIC edge with
	// caller context preserved instead of being dropped or raised as an error
	// (ADR-001). TOML key: flag_dynamic_calls. Default: true.
	FlagDynamicCalls bool `toml:"flag_dynamic_calls"`

	// DynamicCallMinLength is the minimum identifier-length heuristic: a token
	// shorter than this is too short to be a credible module name and is not
	// treated as a dynamic-call candidate. It must be positive; a non-positive
	// value is rejected by [Validate] in favor of the default. TOML key:
	// dynamic_call_min_length. Default: 6.
	DynamicCallMinLength int `toml:"dynamic_call_min_length"`
}

// ResolutionConfig holds the library map and steplib search order. TOML table:
// [resolution]. An empty library list means the workspace is treated as a
// single flat namespace.
type ResolutionConfig struct {
	// Libraries is the declared library map, in declaration order. Each entry
	// is a [[resolution.library]] array-of-tables element. TOML key: library.
	// Default: empty (non-nil) slice.
	//
	// Exposed to the resolver in declared order; consumed by plan 06 (workspace
	// indexing).
	Libraries []Library `toml:"library"`
}

// Library maps a workspace directory to a named Natural library and declares
// its ordered steplib search chain. TOML array-of-tables:
// [[resolution.library]].
type Library struct {
	// Name is the Natural library name (e.g. "MYAPP"). TOML key: name. It is a
	// Natural identifier, so it matches case-insensitively while the original
	// spelling is preserved for display.
	Name string `toml:"name"`

	// Path is the workspace-relative filesystem path to the directory holding
	// the library's objects. TOML key: path.
	Path string `toml:"path"`

	// Steplibs is the ordered steplib search chain for this library; CALLNAT /
	// PERFORM / FETCH targets resolve current library first, then each steplib
	// in order, then SYSTEM. The order is the disambiguation rule (ADR-004).
	// TOML key: steplibs.
	Steplibs []string `toml:"steplibs"`
}

// sentinelName is the marker file whose presence identifies a workspace root.
const sentinelName = ".natural-lsp.toml"

// validExtensionTypeValues is the set of known model.ObjectType stable string
// values accepted in [workspace.extension_types]. Defined at package level to
// avoid rebuilding the map on every Validate call.
var validExtensionTypeValues = map[string]bool{
	"program":            true,
	"subprogram":         true,
	"externalsubroutine": true,
	"copycode":           true,
	"map":                true,
	"localdataarea":      true,
	"globaldataarea":     true,
	"parameterdataarea":  true,
	"helproutine":        true,
	"ddm":                true,
	"class":              true,
	"function":           true,
	"dialog":             true,
	"adapter":            true,
	"text":               true,
}

// FindRoot locates the workspace root by walking up parent directories from
// start, looking for the .natural-lsp.toml sentinel file (sentinelName).
//
// Nearest wins: when several sentinels lie on the path from start to the
// filesystem root, the deepest one (closest to start) is returned with
// found=true. When no sentinel is found anywhere up to the filesystem root,
// FindRoot returns (start, false) — falling back to the start directory is the
// documented contract, not an error.
//
// The returned root is always absolute. start is resolved with filepath.Abs
// before the walk, so a relative start still yields an absolute root (and an
// absolute fallback). If start cannot be made absolute (e.g. the current
// working directory is unavailable), the original start is used as-is.
//
// Output is deterministic: the same start always yields the same (root, found)
// for a given filesystem layout.
//
// Robustness: a sentinel is present only when os.Stat returns a nil error. Any
// stat error — including permission-denied on an unreadable intermediate
// directory — is treated as "no sentinel here", so the walk skips that level
// gracefully and continues toward the filesystem root rather than aborting.
//
// Feature 01-workspace-and-configuration, FR-1.
func FindRoot(start string) (string, bool) {
	// Normalize to an absolute path so the returned root is absolute
	// regardless of how start was supplied. On the rare error, fall back to
	// the caller's start rather than failing root discovery.
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}

	for dir := start; ; {
		// A nil error means the sentinel exists and is statable; any error
		// (not-exist, permission-denied, ...) means "not here" — skip and
		// keep walking up.
		if _, err := os.Stat(filepath.Join(dir, sentinelName)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without a sentinel.
			return start, false
		}
		dir = parent
	}
}

// Bootstrap wires config into process startup: it resolves the workspace root,
// loads the config at root/.natural-lsp.toml, and makes both the resolved root
// and any degradation observable on the injected logger.
//
// Root-resolution precedence (highest first):
//
//   - sentinel found: [FindRoot] located a .natural-lsp.toml on the path from
//     start to the filesystem root — that directory is the root.
//   - workspaceHint: no sentinel, but the caller supplied a non-empty hint (the
//     LSP initialize rootUri / workspaceFolders fallback) — use the hint.
//   - start: no sentinel and no hint — fall back to start (FindRoot's own
//     fallback contract).
//
// Logging contract: it logs the resolved root once at Info, including the exact
// phrase "sentinel found: true" or "sentinel found: false", and one Warn line
// per validation [Problem] reported by [Load] (each naming the offending key).
// A missing sentinel is not a Problem and is not warned; only a non
// file-not-found read/parse error is warned.
//
// CR-6 guarantee: Bootstrap never hard-fails. A missing sentinel or a bad
// config degrades to a usable Config (defaults, with bad values replaced), and
// the returned error is always nil.
//
// Feature 01-workspace-and-configuration, T9 (FR-1 Story 1 criterion 3, CR-6).
func Bootstrap(start, workspaceHint string, logger *slog.Logger) (string, Config, error) {
	root, found := FindRoot(start)
	if !found && workspaceHint != "" {
		root = workspaceHint
	}
	logger.Info(fmt.Sprintf("resolved workspace root: %s (sentinel found: %t)", root, found),
		"root", root, "sentinelFound", found)

	sentinelPath := filepath.Join(root, sentinelName)
	cfg, problems, err := Load(sentinelPath)
	if err != nil {
		// A missing sentinel is expected (no config) — Load already returns a
		// usable Defaults(); only warn on a genuine read/parse failure.
		if !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("config file error", "path", sentinelPath, "error", err)
		}
		cfg = Defaults()
	}
	for _, p := range problems {
		logger.Warn("config problem", "key", p.Key, "message", p.Message, "using", p.FallenBackTo)
	}
	return root, cfg, nil
}

// Defaults returns a Config populated with every documented default from the
// .natural-lsp.toml schema (README "Workspace configuration"), so the server
// runs with zero or minimal configuration (FR-6 / CR-2). The returned
// Resolution.Libraries is a non-nil empty slice.
func Defaults() Config {
	return Config{
		Workspace: WorkspaceConfig{
			Extensions: []string{
				".NSP", ".NSN", ".NSS", ".NSC", ".NSM",
				".NSL", ".NSG", ".NSA", ".NSH", ".NSD",
				".NS4", ".NS7", ".NS3", ".NS8", ".NST",
			},
			Exclude:        []string{"archive", "backup", ".git"},
			MaxFileSize:    5_000_000,
			ExtensionTypes: make(map[string]string),
		},
		Cache: CacheConfig{Path: ".natural-lsp-cache"},
		Analysis: AnalysisConfig{
			FlagDynamicCalls:     true,
			DynamicCallMinLength: 6,
		},
		Resolution: ResolutionConfig{
			Libraries: []Library{},
		},
	}
}

// IsExcluded reports whether the workspace-relative path relPath lies under a
// directory the user excluded from indexing (workspace.exclude). Matching is
// segment-anchored: relPath is split on both '/' and '\', and the path is
// excluded when any single segment equals an exclude entry, compared
// case-insensitively (Natural is case-insensitive). It is NOT a substring
// match — an exclude of "archive" does not match a segment "archived".
//
// This is the surface the future indexer calls to honor directory exclusions
// (Story 3, FR-2/FR-3). The exclusion is a *decision*, not a silent drop: the
// indexer that acts on a true result must report the skip via logs/diagnostics
// (with [SkipExcluded]) rather than discarding the file silently (NFR-6).
func (c *Config) IsExcluded(relPath string) bool {
	segments := strings.FieldsFunc(relPath, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, segment := range segments {
		upper := strings.ToUpper(segment)
		for _, entry := range c.Workspace.Exclude {
			if upper == strings.ToUpper(entry) {
				return true
			}
		}
	}
	return false
}

// SkipReason names why the indexer skipped a file. The indexer uses it to emit
// the right log/diagnostic message for each skip and must never silently
// discard a skipped file: every skip is observable via its SkipReason, never
// dropped silently (Story 3 / NFR-6). Its string value is the stable,
// machine-readable reason code.
type SkipReason string

const (
	// SkipExcluded means the file lay under an excluded directory
	// (workspace.exclude), per [Config.IsExcluded]. The indexer reports the
	// skip with this reason; it must not silently discard the file.
	SkipExcluded SkipReason = "excluded"

	// SkipTooLarge means the file exceeded workspace.max_file_size. The indexer
	// reports the skip with this reason; it must not silently discard the file.
	SkipTooLarge SkipReason = "too_large"
)

// Problem is a single actionable configuration issue surfaced by [Validate].
//
// It is the observable half of the CR-6 fail-safe contract: an invalid config
// *value* degrades to its default and is reported as a Problem, never crashing
// the server or aborting startup. The server keeps running on the defaulted
// value; the Problem is what makes that substitution visible (e.g. as a log
// line or diagnostic) instead of a silent override.
//
// Feature 01-workspace-and-configuration, CR-6.
type Problem struct {
	// Key is the dotted TOML key path of the offending setting (e.g.
	// "workspace.max_file_size"), identifying exactly which value was rejected.
	Key string

	// Message is a human-readable, actionable explanation of what was wrong
	// with the supplied value — enough for an operator to fix the TOML.
	Message string

	// FallenBackTo is the default value the server is actually using in place
	// of the rejected one, rendered as a string for display.
	FallenBackTo string
}

// Load reads the .natural-lsp.toml file at path and returns the effective
// [Config], any field-level [Problem]s, and an error. Its three behaviors
// implement the CR-6 fail-safe contract:
//
//   - It decodes the TOML onto a [Defaults] base, so any key absent from the
//     file keeps its documented default (go-toml/v2 overwrites only the fields
//     present in the file).
//   - It hard-errors *only* when the file cannot be read or is not syntactically
//     valid TOML — the sole non-nil-error paths.
//   - Even on that hard error it returns [Defaults] as the Config (never a zero
//     value), so the server can always start.
//
// Per-field validation never surfaces as the error return: an invalid value
// degrades to its default and is reported through the []Problem slice from
// [Validate], not as a failure.
//
// Feature 01-workspace-and-configuration, FR-6, CR-2, CR-6.
func Load(path string) (Config, []Problem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Defaults(), nil, fmt.Errorf("config file %s: %w", path, err)
	}

	// Decode onto a Defaults() base so keys absent from the file keep their
	// documented defaults: go-toml/v2 only overwrites fields present in the
	// TOML, leaving the rest at their initialized values.
	cfg := Defaults()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Defaults(), nil, fmt.Errorf("config file %s: %w", path, err)
	}

	cfg, problems := Validate(cfg)
	return cfg, problems, nil
}

// Sample returns a fully-commented sample .natural-lsp.toml generated from
// [Defaults]: every documented key is emitted with its default value and an
// explanatory comment, table by table, in a deterministic order. The TOML text
// is written by hand (not marshaled) because the per-key comments are required
// and a TOML marshaler cannot place arbitrary comments. Re-parsing the output
// with [Load] yields a Config equal to Defaults() with no problems (round-trip),
// so the emitted sample is itself a valid, default-equivalent config. It makes
// the documented defaults discoverable (T8, CR-2 / Story 2) and backs the
// natural-lsp --init CLI subcommand.
//
// The output is pinned by the committed golden file
// testdata/config/sample.golden.toml; regenerate it with
// `go test ./internal/config/ -update` when the schema or its defaults change.
//
// Feature 01-workspace-and-configuration, T8.
func Sample() string {
	d := Defaults()

	var b strings.Builder

	b.WriteString("# Sample .natural-lsp.toml — every key shown with its documented default.\n")
	b.WriteString("# This file is generated by `natural-lsp --init`. Re-parsing it yields the\n")
	b.WriteString("# built-in defaults, so you only need to keep the keys you actually change.\n")
	b.WriteString("\n")

	b.WriteString("[workspace]\n")
	b.WriteString("# Ordered set of Natural object-file extensions to index.\n")
	fmt.Fprintf(&b, "extensions = %s\n", tomlStringArray(d.Workspace.Extensions))
	b.WriteString("# Workspace-relative directory names skipped during indexing.\n")
	fmt.Fprintf(&b, "exclude = %s\n", tomlStringArray(d.Workspace.Exclude))
	b.WriteString("# Upper bound, in bytes, on a file the indexer will read; larger files are skipped.\n")
	fmt.Fprintf(&b, "max_file_size = %d\n", d.Workspace.MaxFileSize)
	b.WriteString("\n")
	b.WriteString("# Map additional file extensions to their object type classifications.\n")
	b.WriteString("# Valid types: program, subprogram, externalsubroutine, copycode, map,\n")
	b.WriteString("# localdataarea, globaldataarea, parameterdataarea, helproutine, ddm, class,\n")
	b.WriteString("# function, dialog, adapter, text.\n")
	b.WriteString("# [workspace.extension_types]\n")
	b.WriteString("# \".NAT\" = \"program\"\n")
	b.WriteString("\n")

	b.WriteString("[cache]\n")
	b.WriteString("# Workspace-relative directory where the serialized index cache is written.\n")
	fmt.Fprintf(&b, "path = %q\n", d.Cache.Path)
	b.WriteString("\n")

	b.WriteString("[analysis]\n")
	b.WriteString("# When true, an unresolvable dynamic call (e.g. CALLNAT #VAR) becomes a\n")
	b.WriteString("# CALLS_DYNAMIC dependency with caller context preserved instead of being dropped.\n")
	fmt.Fprintf(&b, "flag_dynamic_calls = %t\n", d.Analysis.FlagDynamicCalls)
	b.WriteString("# Minimum identifier length for a token to be a credible dynamic-call candidate.\n")
	fmt.Fprintf(&b, "dynamic_call_min_length = %d\n", d.Analysis.DynamicCallMinLength)
	b.WriteString("\n")

	// Resolution: the library map defaults to empty, so emit it as a fully
	// commented-out example rather than an active (and therefore parse-affecting)
	// table — keeping the round-trip equal to Defaults().
	b.WriteString("# Library map: map workspace directories to named Natural libraries and declare\n")
	b.WriteString("# each library's ordered steplib search chain. Empty by default — with no\n")
	b.WriteString("# libraries the workspace is treated as a single flat namespace.\n")
	b.WriteString("#\n")
	b.WriteString("# [[resolution.library]]\n")
	b.WriteString("# name = \"MYAPP\"\n")
	b.WriteString("# path = \"src/myapp\"\n")
	b.WriteString("# steplibs = [\"COMMON\", \"SYSTEM\"]\n")

	return b.String()
}

// tomlStringArray renders values as a TOML inline array of double-quoted
// strings: [] for empty, otherwise ["a", "b"] with ", " separators. Used by
// [Sample] to emit list-valued defaults deterministically.
func tomlStringArray(values []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", v)
	}
	b.WriteByte(']')
	return b.String()
}

// normalizeExtensions canonicalizes a list of object-file extensions for
// case-insensitive matching: each entry is trimmed of surrounding whitespace,
// skipped if empty, dot-prefixed if it lacks a leading dot, and upper-cased
// (Natural is case-insensitive). Entries are deduped on their normalized form
// in stable first-occurrence order, so ".nsp", "NSP" and " .NSP " collapse to a
// single ".NSP". It returns a non-nil (possibly empty) slice and never mutates
// its argument. Reused by sample-config generation (T8).
//
// It also drops degenerate entries that carry no extension body — a dot-only
// token (".", "...") or a whitespace-only/empty entry — since such a token can
// never match a real object file. The count of dropped degenerate entries is
// returned so callers can report the substitution (CR-6).
func normalizeExtensions(exts []string) (normalized []string, dropped int) {
	normalized = make([]string, 0, len(exts))
	// Keyed on the already-normalized (upper-cased, dot-prefixed) form so that
	// inputs differing only in case or dot-prefix dedupe to one entry.
	seen := make(map[string]bool, len(exts))
	for _, ext := range exts {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			dropped++
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		ext = strings.ToUpper(ext)
		// Degenerate: nothing but leading dot(s) — no extension body to match on.
		if strings.Trim(ext, ".") == "" {
			dropped++
			continue
		}
		if seen[ext] {
			continue
		}
		seen[ext] = true
		normalized = append(normalized, ext)
	}
	return normalized, dropped
}

// Validate is the per-field validation hook. It normalizes and bounds the core
// [workspace] and [cache] settings, returning the effective Config with any
// rejected value replaced by its default and a deterministically-ordered slice
// of [Problem]s describing each substitution. It never returns an error: a bad
// value is a reported Problem, not a failure — this is the CR-6 fail-safe
// contract, where every degradation is observable but startup always succeeds.
//
// It performs these checks today:
//
//   - workspace.extensions: each entry is trimmed, dot-prefixed and upper-cased
//     via [normalizeExtensions], then deduped in stable first-occurrence order.
//     An empty result falls back to the default object-type set.
//   - workspace.max_file_size: a non-positive value falls back to the default.
//   - cache.path: an empty value falls back to the default.
//   - resolution.library: a [[resolution.library]] entry with no name is
//     dropped (the resolver can never match a nameless library) and reported;
//     kept entries retain their declared order and original spelling.
//   - analysis.dynamic_call_min_length: a non-positive value falls back to the
//     default (the minimum-identifier-length heuristic must be positive).
//
// Feature 01-workspace-and-configuration, FR-2, FR-3, FR-4, CR-3, CR-4, CR-6.
func Validate(cfg Config) (Config, []Problem) {
	var problems []Problem

	defaults := Defaults()

	normalized, dropped := normalizeExtensions(cfg.Workspace.Extensions)
	if dropped > 0 {
		problems = append(problems, Problem{
			Key:          "workspace.extensions",
			Message:      fmt.Sprintf("%d degenerate extension(s) were dropped (dot-only or whitespace-only entries are not valid); using the normalized set", dropped),
			FallenBackTo: "normalized set without degenerate entries",
		})
	}
	if len(normalized) > 0 {
		cfg.Workspace.Extensions = normalized
	} else {
		cfg.Workspace.Extensions = defaults.Workspace.Extensions
		problems = append(problems, Problem{
			Key:          "workspace.extensions",
			Message:      "workspace.extensions contained no usable entries after normalization; using the default object-type set",
			FallenBackTo: strings.Join(defaults.Workspace.Extensions, " "),
		})
	}

	if cfg.Workspace.MaxFileSize <= 0 {
		fallback := defaults.Workspace.MaxFileSize
		problems = append(problems, Problem{
			Key:          "workspace.max_file_size",
			Message:      fmt.Sprintf("workspace.max_file_size must be a positive byte count, got %d; using default %d", cfg.Workspace.MaxFileSize, fallback),
			FallenBackTo: fmt.Sprintf("%d", fallback),
		})
		cfg.Workspace.MaxFileSize = fallback
	}

	if cfg.Cache.Path == "" {
		fallback := defaults.Cache.Path
		cfg.Cache.Path = fallback
		problems = append(problems, Problem{
			Key:          "cache.path",
			Message:      fmt.Sprintf("cache.path must not be empty; using default %q", fallback),
			FallenBackTo: fallback,
		})
	}

	// resolution.library: a [[resolution.library]] entry has no usable key
	// without a name (the name is what CALLNAT/PERFORM/FETCH resolution matches
	// on), so drop any nameless entry and report it rather than carrying a
	// library the resolver can never match. Declared order is preserved for the
	// kept entries; original spelling of Name/Steplibs is left untouched
	// (normalization is a display/matching concern handled at lookup time).
	if len(cfg.Resolution.Libraries) > 0 {
		kept := make([]Library, 0, len(cfg.Resolution.Libraries))
		for _, lib := range cfg.Resolution.Libraries {
			if strings.TrimSpace(lib.Name) == "" {
				problems = append(problems, Problem{
					Key:          "resolution.library.name",
					Message:      "a [[resolution.library]] entry has no name and was dropped",
					FallenBackTo: "entry omitted",
				})
				continue
			}
			kept = append(kept, lib)
		}
		cfg.Resolution.Libraries = kept
	}

	// analysis.dynamic_call_min_length: a non-positive minimum length is
	// meaningless as a heuristic threshold, so reject it, fall back to the
	// documented default, and report the substitution rather than silently
	// using an unusable value.
	if cfg.Analysis.DynamicCallMinLength <= 0 {
		fallback := defaults.Analysis.DynamicCallMinLength
		problems = append(problems, Problem{
			Key:          "analysis.dynamic_call_min_length",
			Message:      fmt.Sprintf("analysis.dynamic_call_min_length must be positive, got %d; using default %d", cfg.Analysis.DynamicCallMinLength, fallback),
			FallenBackTo: fmt.Sprintf("%d", fallback),
		})
		cfg.Analysis.DynamicCallMinLength = fallback
	}

	// workspace.extension_types: validate each entry's value against the known
	// ObjectType set. Invalid entries are dropped and reported; valid entries are
	// kept with normalized (upper-cased, dot-prefixed) keys. Collisions are detected
	// and reported: when two or more keys normalize to the same extension, the
	// first-seen (in sorted key order) is kept and later duplicates are dropped.
	if len(cfg.Workspace.ExtensionTypes) > 0 {
		// Collect keys and sort them for deterministic iteration (first-seen wins).
		keys := make([]string, 0, len(cfg.Workspace.ExtensionTypes))
		for key := range cfg.Workspace.ExtensionTypes {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		validated := make(map[string]string)
		for _, key := range keys {
			value := cfg.Workspace.ExtensionTypes[key]
			// Normalize the key: trim whitespace, upper-case and ensure leading dot
			trimmedKey := strings.TrimSpace(key)
			normalizedKey := strings.ToUpper(trimmedKey)
			if !strings.HasPrefix(normalizedKey, ".") {
				normalizedKey = "." + normalizedKey
			}
			// Drop degenerate keys (empty, whitespace-only, or bare dot) — they can
			// never match a real file extension. Consistent with normalizeExtensions.
			if normalizedKey == "." {
				problems = append(problems, Problem{
					Key:          "workspace.extension_types",
					Message:      fmt.Sprintf("degenerate extension key %q (normalized to %q); entry dropped", key, normalizedKey),
					FallenBackTo: "entry omitted",
				})
				continue
			}
			// Check for collision: if this normalized key was already seen, drop it and report.
			if _, exists := validated[normalizedKey]; exists {
				problems = append(problems, Problem{
					Key:          "workspace.extension_types",
					Message:      fmt.Sprintf("duplicate extension %q (normalized from %q) already mapped; entry dropped", normalizedKey, key),
					FallenBackTo: "entry omitted",
				})
				continue
			}
			// Validate the value
			if !validExtensionTypeValues[strings.ToLower(value)] {
				problems = append(problems, Problem{
					Key:          "workspace.extension_types",
					Message:      fmt.Sprintf("invalid object type %q for extension %q; entry dropped", value, key),
					FallenBackTo: "entry omitted",
				})
				continue
			}
			validated[normalizedKey] = strings.ToLower(value)
		}
		cfg.Workspace.ExtensionTypes = validated
	}

	return cfg, problems
}
