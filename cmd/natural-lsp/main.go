// Command natural-lsp is a stdio Language Server Protocol server for
// Software AG Natural. See README.md for the design spec and
// docs/plans/natural-lsp-prd.md for the requirements.
package main

import (
	"fmt"
	"os"
)

// version is the build version, overridden at release time via -ldflags.
var version = "0.0.0-dev"

func main() {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "-version":
			fmt.Printf("natural-lsp %s\n", version)
			return
		case "--stdio":
			// TODO: construct config, document store, workspace index, and
			// analyzer, then run the stdio LSP server (internal/server).
		}
	}
	// TODO: start the server; smoke test expects an initialize response shape.
}