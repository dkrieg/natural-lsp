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
	"strings"
	"syscall"

	"github.com/dkrieg/natural-lsp/internal/analysis/natural"
	"github.com/dkrieg/natural-lsp/internal/config"
	"github.com/dkrieg/natural-lsp/internal/server"
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

// parseLogLevel parses a log level string (error, warn, info, debug — case-insensitive)
// and returns the corresponding slog.Level. Unknown values return slog.LevelInfo
// (the default level) and log a warning message to stderr.
func parseLogLevel(levelStr string, logger *slog.Logger) slog.Level {
	switch strings.ToLower(levelStr) {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	default:
		logger.Warn("invalid --log-level value; using default level", "value", levelStr)
		return slog.LevelInfo
	}
}

// runWithIO is the injectable entry point used in unit tests. r/w replace
// os.Stdin/os.Stdout so tests can drive the LSP message sequence directly.
func runWithIO(args []string, r io.Reader, w io.Writer, logger *slog.Logger) int {
	// First pass: extract --log-level if present. Both the "--log-level=info"
	// (equals) and "--log-level info" (space-separated, next-token) forms are
	// accepted, for parity with the "--stdio"-style arg loop. A bare trailing
	// "--log-level" with no following value is reported as an actionable stderr
	// message and otherwise ignored (CR-6: never crash).
	logLevelStr := ""
	var filteredArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--log-level="):
			// Equals form: --log-level=<level>.
			logLevelStr = strings.SplitN(arg, "=", 2)[1]
		case arg == "--log-level":
			// Space-separated form: consume the next token as the level.
			if i+1 < len(args) {
				logLevelStr = args[i+1]
				i++ // skip the consumed value
			} else {
				logger.Warn("--log-level requires a value (e.g. --log-level info); ignoring and using default level")
			}
		default:
			// Keep non-log-level args for further processing.
			filteredArgs = append(filteredArgs, arg)
		}
	}

	// If --log-level was specified, rebuild the logger with the parsed level.
	// Otherwise, use the provided logger as-is (for tests).
	if logLevelStr != "" {
		logLevel := parseLogLevel(logLevelStr, logger)
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	}

	// Second pass: process other arguments.
	for _, arg := range filteredArgs {
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
			// Feature 20 (Variant A): the workspace root and config are no longer
			// resolved at process startup. The server negotiates the root from the
			// LSP "initialize" params (workspaceFolders → rootUri → this cwd
			// fallback) and runs config.Bootstrap from that path inside the
			// initialize handler. Here we only compute the cwd fallback — the
			// lowest-precedence discovery start point, used when the client sends
			// no root.
			cwdFallback, err := os.Getwd()
			if err != nil {
				cwdFallback = "."
			}

			az := natural.New(nil)

			// Signal-aware shutdown context for production; tests use a plain
			// background context (they control the lifecycle via messages).
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// nil error = clean shutdown → 0; non-nil = protocol violation → 1.
			if err := server.Run(ctx, r, w, version, cwdFallback, az, logger); err != nil {
				return 1
			}
			return 0
		}
	}
	return 0
}
