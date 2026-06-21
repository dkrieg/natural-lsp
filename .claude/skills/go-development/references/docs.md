# Godoc documentation conventions (natural-lsp)

Document Go code to godoc/pkg.go.dev standards.

## Package documentation

- Every package has a package comment. For multi-file packages, put it in one file (a `doc.go` is
  fine for large packages); for the small `internal/...` packages here, the existing top-of-file
  doc comment is the right home — keep it accurate as responsibilities evolve.
- The package comment starts with `Package name ...` and states the package's responsibility in one
  or two sentences, matching the role described in `README.md`'s Architecture section.

## Declaration documentation

- Every exported type, function, method, constant, and variable has a doc comment.
- The comment begins with the identifier's name (`// Analyze extracts ...`, `// FileAnalysis is ...`).
- Document behavior and contract, not the obvious: preconditions, what errors mean, ownership of
  returned values, concurrency-safety, and what a zero value means where relevant.
- For interfaces (especially `analysis.Analyzer`), document the contract implementations must honor.
- Note when a function takes/respects a `context.Context` for cancellation.

## Style

- Complete sentences, present tense. Keep comments close to the code they describe.
- Use `//` line comments; reserve `/* */` for the rare large block. Code examples in docs go in
  `Example...` test functions, not prose.
- Don't restate the type signature in words; add information the signature can't convey.
- Keep `TODO` comments actionable and, where useful, cross-referenced to a feature plan or PRD
  requirement (e.g. `// TODO: steplib resolution (FR-16)`), matching the existing stubs.

## Verify

- Run `go vet ./...` (it flags some doc issues) and confirm the code still builds.
- Optionally preview with `go doc ./internal/<pkg>` to read the rendered documentation.
- Do not invent behavior in docs that the code does not implement — for stubs, document the intended
  contract and mark it clearly as not-yet-implemented.