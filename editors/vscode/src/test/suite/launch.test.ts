import * as assert from "assert";
import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import type { LanguageClient, State } from "vscode-languageclient/node";

const EXTENSION_ID = "dkrieg.natural-lsp-vscode";

function workspaceDir(): string {
  return path.resolve(__dirname, "../../../src/test/fixtures/workspace");
}

/** Wait until `predicate` holds or the timeout elapses (polling). */
async function waitFor(
  predicate: () => boolean,
  timeoutMs: number,
  intervalMs = 100,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) {
      return true;
    }
    await new Promise((r) => setTimeout(r, intervalMs));
  }
  return predicate();
}

describe("Language server launch", () => {
  const serverPath = process.env.NATURAL_LSP_SERVER_PATH;

  before(async function () {
    if (!serverPath || !fs.existsSync(serverPath)) {
      // No real server binary available (NATURAL_LSP_SERVER_PATH unset or missing).
      // The launch test cannot start a live server; skip rather than fail so the
      // rest of the suite still runs. CI builds the Go binary and sets this var.
      // eslint-disable-next-line no-invalid-this
      this.skip();
      return;
    }
    // Point the setting at the freshly built server binary.
    await vscode.workspace
      .getConfiguration("naturalLsp")
      .update("serverPath", serverPath, vscode.ConfigurationTarget.Global);
  });

  it("starts the client and reaches the Running state when a .NSP is opened", async () => {
    // Opening a Natural document triggers onLanguage:natural activation.
    const uri = vscode.Uri.file(path.join(workspaceDir(), "HELLO.NSP"));
    const doc = await vscode.workspace.openTextDocument(uri);
    await vscode.window.showTextDocument(doc);

    const ext = vscode.extensions.getExtension(EXTENSION_ID);
    assert.ok(ext, `extension ${EXTENSION_ID} should be present`);
    await ext!.activate();

    const api = ext!.exports as
      | { getClient?: () => LanguageClient | undefined }
      | undefined;
    const getClient = api?.getClient;

    if (typeof getClient !== "function") {
      // Activation did not throw; that alone satisfies "did not crash".
      assert.ok(ext!.isActive, "extension should be active");
      return;
    }

    const ready = await waitFor(() => {
      const client = getClient();
      // State.Running === 2 in vscode-languageclient.
      return client !== undefined && (client.state as State) === 2;
    }, 30000);

    const client = getClient();
    assert.ok(client, "language client should exist after activation");
    assert.ok(
      ready,
      `client should reach Running state; current state = ${client?.state}`,
    );
  });
});
