// Package natural: extraction of call/dependency constructs — CALLNAT, FETCH,
// RUN, PERFORM, INCLUDE. Produces references with caller context and the
// appropriate EdgeKind; literal targets are emitted as static references and
// variable targets as dynamic (unresolved) ones. Binding to definitions is
// done later by workspace/resolution.go (PRD FR-10..18).
package natural

// TODO: per-construct extraction; flag literal names containing runtime
// substitution placeholders (e.g. '&') so they are not mis-resolved.
