// Command natural-lsp is a stdio Language Server Protocol server for
// Software AG Natural. See README.md for the design spec and
// docs/plans/natural-lsp-prd.md for the requirements.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"natural-lsp/internal/config"
)

// version is the build version, overridden at release time via -ldflags.
var version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], slog.Default()))
}

// run is the testable entry point: it dispatches the command-line args against
// logger and returns the process exit code. Splitting it out of main lets tests
// inject a logger and observe the --stdio path's Bootstrap wiring without
// touching os.Exit or the global slog default.
func run(args []string, logger *slog.Logger) int {
	for _, arg := range args {
		switch arg {
		case "--version", "-version":
			fmt.Printf("natural-lsp %s\n", version)
			return 0
		case "--init", "-init":
			// Emit a fully-commented sample .natural-lsp.toml to stdout so a
			// user can `natural-lsp --init > .natural-lsp.toml` (T8).
			fmt.Print(config.Sample())
			return 0
		case "--stdio":
			// Resolve the workspace root and load config from the sentinel.
			// Bootstrap never hard-fails (CR-6): a missing sentinel or bad
			// config degrades to usable defaults, and the result is logged.
			start, err := os.Getwd()
			if err != nil {
				start = "."
			}
			root, cfg, _ := config.Bootstrap(start, "", logger)
			_ = root
			_ = cfg
			// TODO: construct document store, workspace index, and analyzer
			// from cfg/root, then run the stdio LSP server (internal/server).
			fmt.Fprintln(os.Stderr, "natural-lsp: stdio LSP server not yet implemented")
			return 0
		}
	}
	// TODO: start the server; smoke test expects an initialize response shape.
	return 0
}
