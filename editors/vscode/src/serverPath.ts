// serverPath resolves the natural-lsp server binary location from configuration.
//
// This module is deliberately free of any `vscode` import so it can be unit-tested
// without the VS Code extension host (see src/test/unit/serverPath.test.ts).

import * as path from "path";

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
 * A bundled-binary candidate, injected into resolveServerPath for host-free testing.
 * The path is the proposed bundled binary location; exists is an fs-existence probe.
 */
export interface BundledCandidate {
  path: string;
  exists: (p: string) => boolean;
}

/**
 * bundledServerPath returns the expected path to a bundled server binary given the
 * extension path and target platform. It is platform-free and host-free.
 *
 * @param extensionPath the VS Code extension root path
 * @param platform the target platform (from process.platform or NodeJS.Platform)
 * @returns the expected bundled binary path
 */
export function bundledServerPath(extensionPath: string, platform: NodeJS.Platform): string {
  const binaryName = platform === "win32" ? "natural-lsp.exe" : "natural-lsp";
  return path.join(extensionPath, "bin", binaryName);
}

/**
 * resolveServerPath returns the server path according to the resolution tier precedence:
 *
 * 1. `naturalLsp.serverPath` setting (trimmed, non-empty) if set → returned (override wins)
 * 2. else if bundled candidate is provided and its binary exists → bundled.path returned
 * 3. else → {@link DEFAULT_SERVER_PATH} (bare `natural-lsp`, looked up on PATH)
 *
 * Backward compatibility: calling with no bundled arg behaves exactly as before.
 * A whitespace-only setting value is treated as unset.
 */
export function resolveServerPath(config: ConfigGetter, bundled?: BundledCandidate): string {
  // Tier 1: serverPath setting (trimmed, non-empty)
  const raw = config.get("serverPath");
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (trimmed.length > 0) {
      return trimmed;
    }
  }

  // Tier 2: bundled candidate (if provided and exists)
  if (bundled && bundled.exists(bundled.path)) {
    return bundled.path;
  }

  // Tier 3: default (via PATH)
  return DEFAULT_SERVER_PATH;
}

/**
 * ServerChoiceInfo describes which resolution tier was used to resolve the server path.
 */
export interface ServerChoiceInfo {
  config: ConfigGetter;
  bundled?: BundledCandidate;
  resolvedPath: string;
}

/**
 * describeServerChoice returns a human-readable label for the tier used to resolve the server.
 * Labels are: "serverPath setting" | "bundled" | "PATH"
 */
export function describeServerChoice(info: ServerChoiceInfo): string {
  // Tier 1: serverPath setting
  const raw = info.config.get("serverPath");
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (trimmed.length > 0 && trimmed === info.resolvedPath) {
      return "serverPath setting";
    }
  }

  // Tier 2: bundled
  if (
    info.bundled &&
    info.bundled.path === info.resolvedPath &&
    info.bundled.exists(info.bundled.path)
  ) {
    return "bundled";
  }

  // Tier 3: PATH
  return "PATH";
}
