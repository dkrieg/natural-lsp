// Package corpusgen emits a deterministic, seeded synthetic Natural workspace
// for scale and performance benchmarking (feature 22, NFR-9).
//
// The generator produces a parameterized, multi-library corpus of sanitized
// .NSx objects with realistic cross-references — CALLNAT → subprogram (.NSN),
// PERFORM → external subroutine (.NSS), INCLUDE → copycode (.NSC), READ/FIND →
// DDM (.NSD) — plus a .natural-lsp.toml declaring the libraries and their
// steplib chains, so the corpus exercises the real extractor and the steplib
// resolution path (current library → steplibs → SYSTEM).
//
// Determinism is the core contract: the same (objectCount, seed) pair produces
// a byte-identical corpus in any target directory. Every random choice is drawn
// from a single seeded *math/rand.Rand — never the global unseeded source and
// never a wall-clock value — so benchmarks are reproducible and diffable.
//
// The corpus is generated, never committed: callers pass a target directory
// (a t.TempDir() in tests, a --out dir for manual large runs). All content is
// invented and non-proprietary (LIBnn, SUBPRGnnnn, and similar synthetic
// names).
//
// This package is normally built (no build tag) so both the small-corpus
// correctness test in `just test` and the //go:build bench benchmark package
// can import it.
package corpusgen

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// Params configures a corpus generation run. The zero value is not valid; use
// Generate or supply Objects, Libraries, and Seed explicitly. Defaults are
// filled in by normalize for the fields left at their zero value.
type Params struct {
	// Objects is the total number of Natural object files to emit (excludes the
	// .natural-lsp.toml sentinel). The count is honored exactly.
	Objects int

	// Libraries is the number of libraries the objects are spread across. Each
	// library is a top-level directory (LIB00, LIB01, …) with a steplib chain
	// declared in the emitted config. Defaults to a size-appropriate value when
	// zero.
	Libraries int

	// Seed seeds the generator's *rand.Rand. The same seed and Objects produce a
	// byte-identical corpus.
	Seed int64

	// CrossRefDensity is the probability (0..1) that an optional extra cross-ref
	// is emitted per program body slot. It only affects the number of edges, not
	// determinism (the same seed still yields the same corpus). Defaults to 0.5
	// when zero.
	CrossRefDensity float64
}

// object kinds, distributed across each library so every reference kind has a
// real target to resolve to.
type objectKind int

const (
	kindSubprogram objectKind = iota // .NSN — CALLNAT target
	kindSubroutine                   // .NSS — external PERFORM target
	kindCopycode                     // .NSC — INCLUDE target
	kindDDM                          // .NSD — READ/FIND target
	kindProgram                      // .NSP — the caller that references the above
)

// object is a single generated file's identity before its content is rendered.
type object struct {
	kind    objectKind
	name    string // uppercased Natural object name (filename stem)
	library int    // owning library index
	relPath string // workspace-relative path (slash-separated), incl. extension
}

// Generate writes a deterministic corpus of objectCount Natural objects into
// targetDir, seeded by seed. It is a convenience wrapper over GenerateParams
// with size-appropriate defaults for library count and cross-ref density.
//
// targetDir is created if it does not exist. Generate writes only under
// targetDir; it never touches files elsewhere.
func Generate(targetDir string, objectCount int, seed int64) error {
	return GenerateParams(targetDir, Params{Objects: objectCount, Seed: seed})
}

// GenerateParams writes a deterministic corpus described by p into targetDir.
// The same Params (and the same targetDir emptiness) produce a byte-identical
// tree. It returns an error only on I/O failure or invalid parameters.
func GenerateParams(targetDir string, p Params) error {
	p = normalize(p)
	if p.Objects <= 0 {
		return fmt.Errorf("corpusgen: Objects must be positive, got %d", p.Objects)
	}
	if targetDir == "" {
		return fmt.Errorf("corpusgen: targetDir must not be empty")
	}

	rng := rand.New(rand.NewSource(p.Seed))

	objects := planObjects(p)
	perLib := groupByLibrary(objects, p.Libraries)

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("corpusgen: create target dir: %w", err)
	}

	// Emit the config sentinel first (deterministic, no rng).
	if err := writeFile(targetDir, ".natural-lsp.toml", renderConfig(p.Libraries)); err != nil {
		return err
	}

	// Emit each object. Objects are visited in a fixed order (planObjects order),
	// so rng draws are consumed deterministically.
	for _, obj := range objects {
		var content string
		switch obj.kind {
		case kindProgram:
			content = renderProgram(obj, perLib, p, rng)
		case kindSubprogram:
			content = renderSubprogram(obj, perLib, p, rng)
		case kindSubroutine:
			content = renderSubroutine(obj)
		case kindCopycode:
			content = renderCopycode(obj)
		case kindDDM:
			content = renderDDM(obj)
		}
		if err := writeFile(targetDir, obj.relPath, content); err != nil {
			return err
		}
	}

	return nil
}

// normalize fills zero-valued Params fields with size-appropriate defaults.
func normalize(p Params) Params {
	if p.Libraries <= 0 {
		// Roughly one library per 12 objects, at least 2 (so the steplib chain
		// and cross-library resolution are exercised), capped so tiny corpora do
		// not end up with empty libraries.
		libs := p.Objects/12 + 2
		if libs < 2 {
			libs = 2
		}
		if libs > p.Objects && p.Objects > 0 {
			libs = p.Objects
		}
		p.Libraries = libs
	}
	if p.CrossRefDensity == 0 {
		p.CrossRefDensity = 0.5
	}
	return p
}

// planObjects deterministically assigns kinds, names, and libraries to
// objectCount objects. The distribution guarantees every library has at least
// one subprogram, subroutine, copycode, and DDM before any programs are added,
// so a program always has same-library targets to resolve against.
//
// The assignment is a fixed round-robin over kinds and libraries — it does not
// consume the rng — so the object set is a pure function of (Objects, Libraries)
// and the per-object content rng draws stay aligned regardless of density.
func planObjects(p Params) []object {
	objects := make([]object, 0, p.Objects)

	// Ensure each library gets a baseline of each non-program kind first, so
	// resolution targets always exist locally. The remaining budget is filled
	// with programs (the callers) round-robin across libraries.
	baselineKinds := []objectKind{kindSubprogram, kindSubroutine, kindCopycode, kindDDM}

	// Per-kind running counters for stable name numbering.
	counters := map[objectKind]int{}

	add := func(kind objectKind, lib int) {
		n := counters[kind]
		counters[kind]++
		name := objectName(kind, n)
		objects = append(objects, object{
			kind:    kind,
			name:    name,
			library: lib,
			relPath: relPathFor(kind, lib, name),
		})
	}

	// Phase 1: baseline non-program objects, one kind per slot, round-robin
	// across libraries, until we either exhaust the budget or every library has
	// a full baseline set.
	baselineTarget := p.Libraries * len(baselineKinds)
	if baselineTarget > p.Objects {
		baselineTarget = p.Objects
	}
	for i := 0; i < baselineTarget; i++ {
		kind := baselineKinds[i%len(baselineKinds)]
		lib := (i / len(baselineKinds)) % p.Libraries
		add(kind, lib)
	}

	// Phase 2: fill the rest with programs, round-robin across libraries.
	remaining := p.Objects - len(objects)
	for i := 0; i < remaining; i++ {
		add(kindProgram, i%p.Libraries)
	}

	return objects
}

// groupByLibrary buckets objects by (library, kind) for O(1) target lookup
// during content rendering. The inner slices are in planObjects order (stable).
func groupByLibrary(objects []object, libraries int) map[int]map[objectKind][]object {
	byLib := make(map[int]map[objectKind][]object, libraries)
	for lib := 0; lib < libraries; lib++ {
		byLib[lib] = make(map[objectKind][]object)
	}
	for _, obj := range objects {
		byLib[obj.library][obj.kind] = append(byLib[obj.library][obj.kind], obj)
	}
	return byLib
}

// reachableTargets returns the objects of the given kind reachable from a
// caller in library lib, following the steplib chain: current library first,
// then its declared steplibs (lib+1, wrapping) in order. The result is
// deterministic and never empty as long as some library holds that kind.
func reachableTargets(byLib map[int]map[objectKind][]object, lib, libraries int, kind objectKind) []object {
	var out []object
	// current library
	out = append(out, byLib[lib][kind]...)
	// steplib chain: see renderConfig for the declared order (lib+1 wrapping).
	for _, step := range steplibChain(lib, libraries) {
		out = append(out, byLib[step][kind]...)
	}
	return out
}

// steplibChain returns the ordered steplib library indices for library lib,
// matching what renderConfig declares. Each library steps to the next
// (wrapping), giving a single-hop, non-transitive chain that resolution honors.
func steplibChain(lib, libraries int) []int {
	if libraries <= 1 {
		return nil
	}
	return []int{(lib + 1) % libraries}
}

// objectName returns the deterministic uppercased name for the nth object of a
// kind. Names are collision-free within a kind and short enough for Natural's
// identifier rules (<= 32 chars).
func objectName(kind objectKind, n int) string {
	switch kind {
	case kindSubprogram:
		return fmt.Sprintf("SUBPRG%04d", n)
	case kindSubroutine:
		return fmt.Sprintf("SUBRTN%04d", n)
	case kindCopycode:
		return fmt.Sprintf("COPYC%04d", n)
	case kindDDM:
		return fmt.Sprintf("VIEW%04d", n)
	case kindProgram:
		return fmt.Sprintf("PROG%04d", n)
	default:
		return fmt.Sprintf("OBJ%04d", n)
	}
}

// extFor returns the .NSx extension for a kind.
func extFor(kind objectKind) string {
	switch kind {
	case kindSubprogram:
		return ".NSN"
	case kindSubroutine:
		return ".NSS"
	case kindCopycode:
		return ".NSC"
	case kindDDM:
		return ".NSD"
	case kindProgram:
		return ".NSP"
	default:
		return ".NSP"
	}
}

// libDir returns the workspace-relative directory name for a library index.
func libDir(lib int) string {
	return fmt.Sprintf("LIB%02d", lib)
}

// relPathFor returns the slash-separated workspace-relative path for an object.
func relPathFor(kind objectKind, lib int, name string) string {
	return libDir(lib) + "/" + name + extFor(kind)
}

// writeFile writes content to targetDir/relPath (relPath is slash-separated),
// creating parent directories. A trailing newline is guaranteed for POSIX-tidy
// files.
func writeFile(targetDir, relPath, content string) error {
	full := filepath.Join(targetDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("corpusgen: mkdir for %s: %w", relPath, err)
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Errorf("corpusgen: write %s: %w", relPath, err)
	}
	return nil
}

// renderConfig emits the .natural-lsp.toml declaring the libraries in index
// order, each stepping to the next (wrapping) — a single-hop steplib chain that
// makes both same-library and cross-library resolution exercisable. Output is a
// pure function of libraries (no rng), so it is deterministic.
func renderConfig(libraries int) string {
	var sb strings.Builder
	sb.WriteString("# Synthetic benchmark corpus — generated by internal/workspace/corpusgen.\n")
	sb.WriteString("# Deterministic (seeded); regenerate rather than edit. Do not commit.\n\n")
	sb.WriteString("[workspace]\n")
	sb.WriteString("max_file_size_kb = 512\n\n")
	sb.WriteString("[resolution]\n\n")
	for lib := 0; lib < libraries; lib++ {
		var steplibs []string
		for _, step := range steplibChain(lib, libraries) {
			steplibs = append(steplibs, libDir(step))
		}
		sb.WriteString("[[resolution.library]]\n")
		fmt.Fprintf(&sb, "name = %q\n", libDir(lib))
		fmt.Fprintf(&sb, "path = %q\n", libDir(lib))
		fmt.Fprintf(&sb, "steplibs = [%s]\n\n", quoteList(steplibs))
	}
	return sb.String()
}

// quoteList renders a []string as a TOML inline-array body (comma-separated
// quoted strings), deterministic and empty-safe.
func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(quoted, ", ")
}

// pick deterministically selects an element from candidates using rng. Callers
// must guard against empty slices.
func pick(candidates []object, rng *rand.Rand) object {
	return candidates[rng.Intn(len(candidates))]
}

// renderProgram emits a .NSP program body with a DEFINE DATA block, one inline
// DEFINE SUBROUTINE (a same-file PERFORM target), and a series of cross-refs:
// a guaranteed CALLNAT (subprogram), PERFORM (external subroutine), INCLUDE
// (copycode) and READ (DDM) against reachable targets, plus density-driven
// extras. All target names are drawn from reachableTargets so every static
// reference resolves.
func renderProgram(obj object, byLib map[int]map[objectKind][]object, p Params, rng *rand.Rand) string {
	subprograms := reachableTargets(byLib, obj.library, p.Libraries, kindSubprogram)
	subroutines := reachableTargets(byLib, obj.library, p.Libraries, kindSubroutine)
	copycodes := reachableTargets(byLib, obj.library, p.Libraries, kindCopycode)
	ddms := reachableTargets(byLib, obj.library, p.Libraries, kindDDM)

	var sb strings.Builder
	fmt.Fprintf(&sb, "* Synthetic program %s (library %s)\n", obj.name, libDir(obj.library))
	sb.WriteString("DEFINE DATA\n")
	sb.WriteString("LOCAL\n")
	sb.WriteString("1 #COUNTER (I4)\n")
	sb.WriteString("1 #NAME (A50)\n")
	sb.WriteString("END-DEFINE\n")
	sb.WriteString("*\n")

	// Guaranteed CALLNAT → subprogram.
	if len(subprograms) > 0 {
		fmt.Fprintf(&sb, "CALLNAT '%s'\n", pick(subprograms, rng).name)
	}
	// Guaranteed INCLUDE → copycode.
	if len(copycodes) > 0 {
		fmt.Fprintf(&sb, "INCLUDE %s\n", pick(copycodes, rng).name)
	}
	// Guaranteed external PERFORM → external subroutine (no inline subroutine of
	// this name exists in the file, so it falls through to the external .NSS).
	if len(subroutines) > 0 {
		fmt.Fprintf(&sb, "PERFORM %s\n", pick(subroutines, rng).name)
	}
	// Guaranteed READ → DDM.
	if len(ddms) > 0 {
		ddm := pick(ddms, rng)
		fmt.Fprintf(&sb, "READ %s BY CUSTOMER-ID\n", ddm.name)
		sb.WriteString("END-READ\n")
	}

	// Density-driven extra cross-refs (still deterministic for a given seed).
	extras := 2
	for i := 0; i < extras; i++ {
		if rng.Float64() < p.CrossRefDensity && len(subprograms) > 0 {
			fmt.Fprintf(&sb, "CALLNAT '%s'\n", pick(subprograms, rng).name)
		}
		if rng.Float64() < p.CrossRefDensity && len(ddms) > 0 {
			ddm := pick(ddms, rng)
			fmt.Fprintf(&sb, "FIND %s WITH CUSTOMER-ID = #COUNTER\n", ddm.name)
			sb.WriteString("END-FIND\n")
		}
	}

	// One inline subroutine (a same-file PERFORM target for outline/hierarchy).
	inlineName := "LOCAL-" + obj.name
	fmt.Fprintf(&sb, "PERFORM %s\n", inlineName)
	fmt.Fprintf(&sb, "DEFINE SUBROUTINE %s\n", inlineName)
	sb.WriteString("  ADD 1 TO #COUNTER\n")
	sb.WriteString("END-SUBROUTINE\n")

	sb.WriteString("END\n")
	return sb.String()
}

// renderSubprogram emits a .NSN subprogram with a PARAMETER DEFINE DATA block
// (so hover/signature help have a real interface) and an occasional onward
// CALLNAT to another reachable subprogram (fan-out for the call graph).
func renderSubprogram(obj object, byLib map[int]map[objectKind][]object, p Params, rng *rand.Rand) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "* Synthetic subprogram %s (library %s)\n", obj.name, libDir(obj.library))
	sb.WriteString("DEFINE DATA\n")
	sb.WriteString("PARAMETER\n")
	sb.WriteString("1 #IN (A20)\n")
	sb.WriteString("1 #OUT (N8)\n")
	sb.WriteString("END-DEFINE\n")
	sb.WriteString("*\n")

	// Occasional onward call to another subprogram (deterministic via rng).
	subprograms := reachableTargets(byLib, obj.library, p.Libraries, kindSubprogram)
	// Avoid a trivial self-call by filtering out this object's own name.
	filtered := filterOutName(subprograms, obj.name)
	if len(filtered) > 0 && rng.Float64() < p.CrossRefDensity {
		fmt.Fprintf(&sb, "CALLNAT '%s'\n", pick(filtered, rng).name)
	}

	sb.WriteString("END\n")
	return sb.String()
}

// filterOutName returns candidates excluding any object whose name equals name.
func filterOutName(candidates []object, name string) []object {
	out := candidates[:0:0]
	for _, c := range candidates {
		if c.name != name {
			out = append(out, c)
		}
	}
	return out
}

// renderSubroutine emits a .NSS external subroutine whose DEFINE SUBROUTINE
// name matches its object name (the external PERFORM binding target).
func renderSubroutine(obj object) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "* Synthetic external subroutine %s (library %s)\n", obj.name, libDir(obj.library))
	fmt.Fprintf(&sb, "DEFINE SUBROUTINE %s\n", obj.name)
	sb.WriteString("  IGNORE\n")
	sb.WriteString("END-SUBROUTINE\n")
	return sb.String()
}

// renderCopycode emits a .NSC copycode (an INCLUDE target). Copycode is
// textual, so a couple of statements suffice.
func renderCopycode(obj object) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "* Synthetic copycode %s (library %s)\n", obj.name, libDir(obj.library))
	sb.WriteString("RESET #COUNTER\n")
	return sb.String()
}

// renderDDM emits a .NSD DDM in the exported fixed-column report format the
// ddm.go line-scanner parses (T@0/L@2/DB@4/Name@7/F@41/Leng@43). Fields are
// invented and non-proprietary. The name column is left-aligned into a 32-wide
// field so the byte offsets line up with the parser.
func renderDDM(obj object) string {
	var sb strings.Builder
	// Header line: "DB: NNN FILE: NNN  - NAME ..." — the parser only reads the
	// field rows, but a realistic header keeps the fixture faithful.
	fmt.Fprintf(&sb, "DB: 000 FILE: 100  - %s\n", obj.name)
	sb.WriteString("TYPE: ADABAS\n")
	sb.WriteString("\n")
	sb.WriteString("T L DB Name                              F Leng  S D Remark\n")
	sb.WriteString("- - -- --------------------------------  - ----  - - ------------------------\n")
	sb.WriteString(ddmFieldRow("", "1", "AA", "CUSTOMER-ID", "N", "8"))
	sb.WriteString(ddmFieldRow("", "1", "AB", "CUSTOMER-NAME", "A", "50"))
	sb.WriteString(ddmFieldRow("", "1", "AC", "BALANCE", "P", "9,2"))
	sb.WriteString("******DDM OUTPUT TERMINATED******\n")
	return sb.String()
}

// ddmFieldRow renders one fixed-column DDM field row matching ddm.go's offsets:
// T@0, L@2, DB@4, Name@7 (32 wide), F@41, Leng@43.
func ddmFieldRow(typeFlag, level, db, name, format, length string) string {
	var b [53]byte
	for i := range b {
		b[i] = ' '
	}
	putAt(b[:], 0, typeFlag)
	putAt(b[:], 2, level)
	putAt(b[:], 4, db)
	putAt(b[:], 7, name)
	putAt(b[:], 41, format)
	putAt(b[:], 43, length)
	// Trim trailing spaces for tidy output; the parser tolerates short rows and
	// reads by fixed offset up to len(line), so trailing trim past the last
	// populated column is safe.
	line := strings.TrimRight(string(b[:]), " ")
	return line + "\n"
}

// putAt writes s into buf starting at off, clamped to buf's length.
func putAt(buf []byte, off int, s string) {
	for i := 0; i < len(s) && off+i < len(buf); i++ {
		buf[off+i] = s[i]
	}
}
