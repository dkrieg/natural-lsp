// Command natural-lsp is a stdio Language Server Protocol server for
// Software AG Natural. See README.md for the design spec and
// docs/plans/natural-lsp-prd.md for the requirements.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"natural-lsp/internal/analysis/natural"
	"natural-lsp/internal/config"
	"natural-lsp/internal/server"
)

// version is the build version, overridden at release time via -ldflags.
var version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], slog.Default()))
}

// run is the testable entry point: it dispatches command-line args and returns
// the process exit code. It uses os.Stdin/os.Stdout for the --stdio path;
// use runWithIO for injectable stream testing.
func run(args []string, logger *slog.Logger) int {
	return runWithIO(args, os.Stdin, os.Stdout, logger)
}

// runWithIO is the injectable entry point used in unit tests. r/w replace
// os.Stdin/os.Stdout so tests can drive the LSP message sequence directly.
func runWithIO(args []string, r io.Reader, w io.Writer, logger *slog.Logger) int {
	for _, arg := range args {
		switch arg {
		case "--version", "-version":
			fmt.Printf("natural-lsp %s\n", version)
			return 0
		case "--init", "-init":
			// Emit a fully-commented sample .natural-lsp.toml to stdout so a
			// user can `natural-lsp --init > .natural-lsp.toml`.
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

			az := natural.New(nil)

			// Signal-aware shutdown context for production; tests use a plain
			// background context (they control the lifecycle via messages).
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// nil error = clean shutdown → 0; non-nil = protocol violation → 1.
			if err := server.Run(ctx, r, w, version, root, cfg, az, logger); err != nil {
				return 1
			}
			return 0
		}
	}
	return 0
}
