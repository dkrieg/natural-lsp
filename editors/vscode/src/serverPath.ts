// serverPath resolves the natural-lsp server binary location from configuration.
//
// This module is deliberately free of any `vscode` import so it can be unit-tested
// without the VS Code extension host (see src/test/unit/serverPath.test.ts).

/** The default server command, resolved via the OS PATH. */
export const DEFAULT_SERVER_PATH = "natural-lsp";

/**
 * A minimal view of the configuration this resolver needs. In the extension this
 * is satisfied by `vscode.workspace.getConfiguration('naturalLsp')`, but the
 * narrow interface keeps the resolver host-independent and mockable in tests.
 */
export interface ConfigGetter {
  get(section: string): unknown;
}

/**
 * resolveServerPath returns the configured server path when the
 * `naturalLsp.serverPath` setting holds a non-empty (trimmed) string, otherwise
 * it falls back to {@link DEFAULT_SERVER_PATH} (a bare `natural-lsp`, looked up
 * on PATH — the zero-config path). A whitespace-only value is treated as unset.
 */
export function resolveServerPath(config: ConfigGetter): string {
  const raw = config.get("serverPath");
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (trimmed.length > 0) {
      return trimmed;
    }
  }
  return DEFAULT_SERVER_PATH;
}
