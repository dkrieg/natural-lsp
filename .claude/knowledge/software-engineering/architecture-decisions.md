# Architecture decisions (ADR log)

**Status:** verified (2026-06-20) against the repo's own README.md, docs/plans/natural-lsp-prd.md, and
CLAUDE.md — these are the authoritative source for project decisions. Append a new dated entry
whenever a significant decision is made; never silently reverse one — supersede it with a new entry.

## ADR-001 — Regex-based extraction, not a grammar
**Decision:** Extract Natural constructs with tuned regexes rather than a full parser/grammar.
**Rationale:** Usable coverage of production patterns quickly beats slow, complete coverage; no mature
Natural grammar exists. **Consequence:** two gap types handled separately — unresolvable references
are modeled outcomes (`CALLS_DYNAMIC`), unrecognized syntax becomes a diagnostic (must be flagged on
purpose; an unmatched regex is otherwise silent). See Go KB `regexp-and-extraction.md` for RE2 limits.

## ADR-002 — Analyzer interface as the replaceable-backend seam
**Decision:** LSP-facing code depends only on `internal/analysis.Analyzer` + `internal/model`, never
on the regex backend in `internal/analysis/natural`. **Rationale:** the backend can later become a
hand-written parser or tree-sitter grammar without touching the LSP layer.

## ADR-003 — Extraction and resolution are separate steps
**Decision:** Per-file extraction produces *unresolved* references with caller context; cross-file
**resolution** (`internal/workspace/resolution.go`) binds them via the library/steplib chain.
**Rationale:** keeps the highest-risk logic (resolution) out of the regex backend and behind the
index. (Added when the README architecture was aligned with the PRD.)

## ADR-004 — Module resolution follows the steplib chain, not file paths
**Decision:** Resolve CALLNAT/PERFORM/FETCH targets current-library → ordered steplibs → SYSTEM,
config-driven. With no library map, treat the workspace as one flat namespace and emit an ambiguity
diagnostic. **Rationale:** the same module name can exist in multiple libraries; search order
disambiguates. Names are not globally unique.

## ADR-005 — Cache invalidation by content hash, not mtime
**Decision:** Invalidate the on-disk index cache on file **content hash**; a cache-format version
forces a full rebuild. **Rationale:** mtime breaks across git checkouts. (Hash algorithm decided in
ADR-011: `crypto/sha256`.)

## ADR-006 — Filesystem-scoped to NaturalONE/SPoD `.NSx` files
**Decision:** Operate on exported object files, not mainframe-resident objects. The indexed extension
set maps to constructs and must stay in sync with the features that consume each type.

## ADR-007 — Batch export dropped from scope
**Decision:** No batch/bulk export feature. **Rationale:** the server is interactive/editor-driven;
clean extracted structure can still feed an external graph consumer.

## ADR-008 — Position encoding: negotiate UTF-8, default to UTF-16 (2026-06-20)
**Decision:** Advertise `general.positionEncodings`-aware behavior: pick **UTF-8** when the client
offers it (return `positionEncoding: "utf-8"` in `ServerCapabilities`), otherwise fall back to the
mandatory **UTF-16** baseline. Centralize the byte/rune↔LSP-column conversion in one place keyed off
the negotiated encoding. **Rationale:** Go source is held as UTF-8 bytes/runes; serving UTF-8 columns
when the client supports them avoids the UTF-16 surrogate conversion entirely for those clients, and
Natural source is overwhelmingly ASCII (UTF-8 and UTF-16 columns coincide except on non-ASCII lines),
so correctness risk is confined to multibyte literals/comments — handled by the one conversion point.
UTF-16 must remain implemented because it is the spec default and the only encoding a client lacking
`positionEncodings` accepts. **Alternatives considered:** (a) UTF-16 only — simplest to advertise but
forces surrogate math on every range even though most clients (incl. VS Code) now offer UTF-8;
(b) UTF-8 only — non-conformant, breaks clients that don't offer UTF-8. **Source:** LSP 3.17 spec,
see `lsp-protocol.md`.

## ADR-009 — Document sync kind: Full for the first release (2026-06-20)
**Decision:** Advertise `TextDocumentSyncKind.Full` (with `openClose: true`) initially; revisit
`Incremental` only if profiling shows full-text `didChange` payloads are a bottleneck.
**Rationale:** Natural objects are small single files; full-document sync is far simpler and removes a
whole class of range-application bugs (incremental requires correctly applying `TextDocumentContent-
ChangeEvent` ranges in order). The analyzer already re-extracts whole files, so incremental sync would
yield no analysis-side win. **Alternatives considered:** `Incremental` (2) — less data on the wire but
more complex and error-prone, unjustified for small files. **Source:** LSP 3.17 spec, `TextDocument-
SyncKind`; CLAUDE.md note that full is simpler.

## ADR-010 — LSP transport/types: depend on `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` (2026-06-20)
**Decision:** Use `go.lsp.dev/protocol` (LSP message types) + `go.lsp.dev/jsonrpc2` (JSON-RPC 2.0
transport), both **v1.0.0**, as the default rather than hand-rolling JSON-RPC framing and message
types. The dependency lives **behind the `internal/server` boundary** and must not leak into
`internal/analysis` or `internal/model` (preserves the Analyzer seam, ADR-002). **Rationale:** both
modules are at a freshly stabilized v1.0.0 and actively maintained (verified via the Go module proxy
in Go KB `lsp-go-ecosystem.md`); lowest implementation cost for the project's small method set, and
the 1.0 tag limits churn. **Alternatives considered:** (a) hand-roll minimal JSON-RPC + only the LSP
types used — maximum control, smallest dependency surface, but more code and ongoing spec-tracking;
(b) `tliron/glsp` — pre-1.0 and framework-heavy for our handful of methods; (c) `sourcegraph/go-lsp`
— **archived, rejected**. The hand-roll path (a) remains the fallback if the ~22 transitive deps of
`go.lsp.dev` become a concern. **Source:** Go KB `lsp-go-ecosystem.md` (verified 2026-06-20).

## ADR-011 — Cache-key content hash: `crypto/sha256` (2026-06-20)
**Decision:** Key the on-disk index cache (ADR-005) on **`crypto/sha256`** of file content.
**Rationale:** the cache key must be **deterministic and stable across program runs and git
checkouts** (FR-38). SHA-256 is deterministic, collision-resistant, gives a zero-collision-worry
auditable key, and is fast enough for file-sized inputs. Crucially, **`hash/maphash` is unsuitable**
— its seed is random per process and not serializable, so identical content hashes differently every
run (Go KB `filesystem-and-watching.md`). **Alternatives considered:** `hash/fnv` (FNV-1a 64) — also
deterministic/stable and faster, acceptable if profiling later shows hashing is hot, but trades
collision margin for speed with no present need; `hash/maphash` — rejected (non-serializable seed).
**Source:** Go KB `filesystem-and-watching.md`; `hash/maphash` godoc (verified 2026-06-20).

## ADR-012 — Index concurrency model: snapshot-on-read + bounded worker pool (2026-06-20)
**Decision:** The workspace index is read by LSP request handlers **concurrently** with background
(re)indexing and watcher-driven updates. Adopt two structural rules: **(1) queries read an immutable
snapshot** — a handler obtains a consistent view that a concurrent update cannot tear, by swapping a
new index value/pointer in atomically (or returning copies for query results) rather than mutating a
shared map under readers; **(2) full-workspace indexing fans out over a bounded worker pool** (≈ CPU
count, e.g. `errgroup` with `SetLimit`), never one unbounded goroutine per file, with every
background goroutine tied to a shutdown-cancelled context. **Rationale:** satisfies responsiveness
(NFR-3) and no-torn-results (NFR-8) without coarse locking that would stall the request loop; bounds
memory/goroutines on large repos (NFR-4); and gives a clean shutdown path (FR-43). The race detector
(`-race`) is the standing correctness bar for any change here. **Alternatives considered:** (a) one
big `RWMutex` around a mutating index — simple but readers block during rebuild and torn-read risk
returns the moment a read spans multiple map ops; (b) one goroutine per file — simplest fan-out but
unbounded memory/goroutines on a 30k-file repo; (c) single owner goroutine + channel queries (actor)
— viable and race-free, kept as a fallback if snapshot swapping proves awkward, but adds latency and
serializes reads. **Source:** PRD NFR-3/4/8, FR-43; Go KB `concurrency-primitives.md` (errgroup
`SetLimit`, snapshot/immutable guidance) and skill concurrency reference (mechanics).

## ADR-013 — Fuzz the extraction entry point as the FR-43 safety guard (2026-06-20)
**Decision:** Maintain a Go native fuzz target over the analyzer's extraction entry point asserting
**"never panics on any input"** (a safety/liveness property, not output correctness). Crashers found
by fuzzing are committed under `testdata/fuzz/...` and replay under plain `go test`, becoming
permanent regression fixtures by the same rule as hand-authored `.NSx` reproducers. **Rationale:**
the extractor consumes untrusted source files and FR-43 forbids any single file crashing the server;
fuzzing reaches pathological inputs no hand-written fixture would; the committed-corpus model
integrates with the existing `testdata/` regression convention at zero extra process cost.
**Alternatives considered:** hand-authored adversarial fixtures only — necessary but cannot match a
fuzzer's coverage of the malformed-input space; property-based libraries (e.g. gopter) — extra
dependency where stdlib fuzzing already fits the "no panic" property. **Source:**
https://go.dev/security/fuzz/ (native fuzzing since Go 1.18, corpus committed as regression seed;
verified 2026-06-20); `testing-strategy.md`, `engineering-principles.md` (secure-by-design).

## Pending decisions (record here when made)
- *(none open — ADR-008..011 resolved the position-encoding, sync-kind, transport, and content-hash
  decisions on 2026-06-20; ADR-012/013 added the index-concurrency and extractor-fuzzing decisions.)*

## Sources
- Internal (authoritative): `README.md`, `docs/plans/natural-lsp-prd.md`, `CLAUDE.md`,
  `docs/plans/features/`.
- Cross-referenced Go KB (verified 2026-06-20): `.claude/knowledge/go/lsp-go-ecosystem.md`,
  `.claude/knowledge/go/filesystem-and-watching.md`.
- LSP 3.17 spec for ADR-008/009 — see `lsp-protocol.md`.