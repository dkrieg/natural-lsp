// Integration-test launcher for @vscode/test-electron.
//
// It downloads a pinned VS Code build, then runs the compiled Mocha suite
// (out/test/suite) inside that VS Code instance, opening the sample workspace
// so the launch test has a `.natural-lsp.toml` sentinel to anchor on.
//
// Server binary discovery for the launch test: the harness reads the
// NATURAL_LSP_SERVER_PATH env var (set by CI / the developer to the freshly
// built `natural-lsp` binary) and passes it through to the test process, which
// points the `naturalLsp.serverPath` setting at it. If unset, the test that
// needs a live server is skipped (see suite/launch.test.ts).

import * as path from "path";
import { runTests } from "@vscode/test-electron";

// Pin the VS Code version for reproducible runs.
const VSCODE_VERSION = "1.85.0";

async function main(): Promise<void> {
  try {
    const extensionDevelopmentPath = path.resolve(__dirname, "../../");
    const extensionTestsPath = path.resolve(__dirname, "./suite/index");
    const workspacePath = path.resolve(
      __dirname,
      "../../src/test/fixtures/workspace",
    );

    await runTests({
      version: VSCODE_VERSION,
      extensionDevelopmentPath,
      extensionTestsPath,
      launchArgs: [workspacePath, "--disable-extensions"],
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.error("Failed to run integration tests:", err);
    process.exit(1);
  }
}

void main();
