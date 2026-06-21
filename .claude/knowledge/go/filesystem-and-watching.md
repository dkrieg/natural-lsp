# Filesystem traversal, watching & hashing

**Status:** verified (2026-06-20) — WalkDir/io/fs/fstest confirmed against pkg.go.dev; fsnotify
recursion behavior confirmed from its repo; **important hash correction** (maphash is NOT stable
across runs) recorded below.

## Facts (verified)

- **Traversal**: `filepath.WalkDir` (uses `fs.DirEntry`, avoids a `stat` per file — faster than the
  older `filepath.Walk`) for the initial workspace scan. Honor the configured exclude dirs by
  returning `fs.SkipDir`. Filter by the configured extension set.
- **`io/fs`**: abstracting over `fs.FS` makes indexing testable against an in-memory filesystem
  (`fstest.MapFS`) instead of real disk — useful for fixtures.
- **File reading**: `os.ReadFile` for whole-file reads (respect the configured max file size before
  reading large files).
- **Watching**: **`github.com/fsnotify/fsnotify`** is the de-facto cross-platform watcher (the
  README's feature set implies a watcher for external changes, FR-34). Latest **v1.10.1 (2026-05-04),
  actively maintained**; requires Go 1.23+. Backends: inotify (Linux), kqueue (macOS/BSD),
  ReadDirectoryChangesW (Windows), FEN (illumos).
  - **No recursive watching out of the box** — confirmed from the repo: "you must add watches for any
    directory you want to watch (a recursive watcher is on the roadmap: #18)." You must `Add` each
    directory; new subdirs created at runtime are not auto-watched. Manage recursion yourself (walk
    the tree, `Add` each indexed subdir, and `Add` newly-created dirs on `Create` events). Resolved
    open question below.
  - Debounce/coalesce rapid events (editors emit bursts; many tools also emit a rename as
    remove+create).
- **Content hashing for cache invalidation** (FR-38 — hash content, not mtime). The cache must be
  **stable across program runs and git checkouts**, which rules out one of the seeded candidates:
  - `crypto/sha256` — deterministic, collision-resistant, stable across runs; slower but fine for
    file-sized inputs. Safe default.
  - `hash/fnv` (e.g. FNV-1a 64) — fast, non-cryptographic, **deterministic/stable across runs**;
    adequate when collisions are only a cache-correctness concern and inputs aren't adversarial.
  - **`hash/maphash` is WRONG here.** Its seed is **random per process and cannot be serialized or
    recreated in another process** (per its godoc), so the same content hashes differently on every
    run — useless as a persisted cache key. It is only for in-memory hash tables within a single
    process. (This corrects the seed file.)
  - Decision lens: SHA-256 if you want zero collision worry and a stable, auditable key; FNV-1a if
    profiling shows hashing is hot. Either is stable; record the pick as an ADR.

## Resolved

- **Recursive-watch strategy:** fsnotify has no native recursive watch, so explicitly `Add` each
  indexed directory and `Add` newly-created dirs seen on `Create` events; a periodic full rescan is a
  reasonable belt-and-braces fallback for missed events. (Was an open question.)
- **Cache-key hash:** must be deterministic across runs → `crypto/sha256` (default) or `hash/fnv`;
  **not** `hash/maphash`. (Was an open question.)

## Sources

- https://pkg.go.dev/path/filepath (verified 2026-06-20: WalkDir / fs.SkipDir)
- https://pkg.go.dev/io/fs , https://pkg.go.dev/testing/fstest (verified 2026-06-20: fstest.MapFS)
- https://github.com/fsnotify/fsnotify (verified 2026-06-20: no recursive watch, backends,
  platforms); version via `https://proxy.golang.org/github.com/fsnotify/fsnotify/@latest` → v1.10.1
- https://pkg.go.dev/hash/maphash (verified 2026-06-20: per-process random seed, not serializable —
  unsuitable for persisted cache keys)