# Safe Go concurrency (natural-lsp)

This server handles LSP requests over stdio **while** indexing runs in the background and a watcher
reacts to file changes — so shared state (the workspace index, the document store) is accessed
concurrently. Correctness here directly affects PRD NFR-3 (responsiveness) and NFR-8 (no
stale/torn results).

## Cancellation & lifetimes

- Thread `context.Context` through any operation that blocks, does I/O, or can be cancelled
  (indexing, per-file analysis, request handling). Honor `ctx.Done()`; return `ctx.Err()`.
- Tie background work (full index, watcher loop) to a context cancelled on server shutdown so nothing
  outlives the process cleanly (PRD FR-43 clean shutdown). Every goroutine must have a guaranteed
  exit path — no leaks.
- Don't store a `context.Context` in a struct field; pass it explicitly.

## Shared state

- Protect the index and document store with a clear strategy: a `sync.RWMutex` for read-heavy shared
  maps, or confine state to a single owner goroutine that others reach via channels. Pick one model
  per component and document it.
- Never expose internal maps/slices by reference if a caller could mutate them concurrently — return
  copies or immutable snapshots for query results.
- Use `sync.Once` for one-time init; `sync.WaitGroup` to await a fan-out of analysis goroutines.

## Patterns that fit this codebase

- **Bounded worker pool** for full-workspace indexing: fan out file analysis across a fixed number of
  workers (roughly CPU-bound), collect results, respect cancellation. Don't spawn one goroutine per
  file unbounded on a 30k-file repo (NFR-4 memory).
- **Debounce/coalesce** rapid file-change events from the watcher before triggering re-analysis
  (feature plan 04).
- Keep request handlers fast and non-blocking; offload heavy work and report progress via
  `window/workDoneProgress` rather than stalling the LSP loop.

## Hazards to flag in review

- Data races (run `go test -race`; it is the bar for any concurrent change).
- Goroutine leaks (started in a request/loop with no exit on cancel).
- Sending on or closing channels from the wrong side; closing a channel more than once.
- Deadlocks from lock ordering or holding a lock across a channel send/blocking call.
- Torn reads of the index during an incremental update (readers seeing half-updated state).