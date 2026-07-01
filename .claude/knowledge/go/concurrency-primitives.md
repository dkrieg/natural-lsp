# Concurrency primitives

**Status:** verified (2026-06-20) — APIs confirmed against pkg.go.dev (context, sync,
golang.org/x/sync/errgroup, os/signal).

Relevant because the server handles LSP requests while indexing runs in the background and a watcher
reacts to file changes. See also the `go-development` skill's concurrency reference for the
prescriptive patterns; this topic records the underlying facts.

## Facts (verified)

- **`context.Context`**: the cancellation/deadline carrier. Pass as the first arg to blocking/IO ops;
  never store in a struct. `ctx.Done()` channel + `ctx.Err()` for cancellation. `context.WithCancel`,
  `WithTimeout`, `WithValue`. `context.WithCancelCause(parent)` + `cancel(err)` lets a canceler attach
  a reason retrievable via `context.Cause(ctx)` (Go 1.20+); useful for distinguishing shutdown from
  an indexing error.
- **`sync`**: `Mutex`/`RWMutex` (RWMutex for read-heavy shared maps like the index), `Once`
  (one-time init), `WaitGroup` (await a fan-out). `sync.Map` only for specific append-mostly/disjoint
  workloads — a plain map + RWMutex is usually clearer.
- **`sync/atomic`**: lock-free counters/flags (e.g. an "indexing in progress" flag, progress counts).
- **Channels**: ownership matters — only the sender closes; never close twice; don't send on a closed
  channel. Use for worker-pool result collection and watcher event coalescing.
- **`golang.org/x/sync/errgroup`** (verified, maintained — latest **v0.21.0, 2026-06-04**): bounded
  fan-out with first-error propagation and context cancellation — a strong fit for the worker-pool
  indexer, and simpler than hand-rolling `WaitGroup` + an errors channel.
  - `g, ctx := errgroup.WithContext(parent)`: the derived `ctx` is canceled the **first time** a
    `Go`-launched func returns a non-nil error **or** the first time `Wait` returns (whichever first).
  - `g.SetLimit(n)`: caps active goroutines at `n` (negative = unlimited; 0 = block all). Subsequent
    `g.Go(...)` **blocks** until a slot frees — this is the bounded worker pool, no manual semaphore.
  - `g.TryGo(f) bool`: non-blocking variant; starts the goroutine only if under the limit, returns
    whether it started.
  - `g.Wait()` returns the **first** non-nil error. Note `errgroup` only propagates the first error;
    if every per-file failure must be observed (graceful-degradation/FR-43), collect failures into a
    mutex-guarded slice or a channel rather than relying on `Wait`'s single return.
- **`os/signal`**: prefer `signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)` →
  `(ctx, stop)`. `ctx` is canceled when a listed signal arrives, `stop()` is called, or `parent` is
  done. `context.Cause(ctx)` then describes the signal. `defer stop()` to release resources. Cleaner
  than `signal.Notify` + a manual goroutine for driving graceful shutdown.
- **Race detector** (`-race`) is the correctness bar; it instruments the binary and reports data
  races at runtime — run the concurrent test suite under it.

## Patterns this project needs

- Bounded worker pool for full-workspace indexing (≈ CPU count), not one goroutine per file.
- Snapshot/immutable query results so readers never see a half-updated index.
- All background goroutines tied to a shutdown-cancelled context (clean exit, no leaks).

## Sources

- https://pkg.go.dev/context (verified 2026-06-20)
- https://pkg.go.dev/sync (verified 2026-06-20)
- https://pkg.go.dev/golang.org/x/sync/errgroup (verified 2026-06-20: WithContext cancellation,
  SetLimit blocking semantics, TryGo)
- https://pkg.go.dev/os/signal (verified 2026-06-20: NotifyContext)
- errgroup maintenance: `https://proxy.golang.org/golang.org/x/sync/@latest` → v0.21.0 (2026-06-04),
  re-confirmed still latest 2026-06-30.