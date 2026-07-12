// The Natural LSP VS Code extension entry point.
//
// On activation (when a `natural` document is opened) it starts the existing
// `natural-lsp` language server over stdio — exactly as every other editor
// launches it (`natural-lsp --stdio`). There is no bespoke transport or protocol.

import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";
import { resolveServerPath } from "./serverPath";

let client: LanguageClient | undefined;

/** The document selector this client serves — the `natural` language, any scheme/path. */
const DOCUMENT_SELECTOR = [{ language: "natural" }];

/**
 * buildClient constructs (but does not start) a LanguageClient that launches the
 * server binary resolved from `naturalLsp.serverPath`.
 */
function buildClient(): LanguageClient {
  const serverPath = resolveServerPath(
    vscode.workspace.getConfiguration("naturalLsp"),
  );

  const serverOptions: ServerOptions = {
    command: serverPath,
    args: ["--stdio"],
    transport: TransportKind.stdio,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: DOCUMENT_SELECTOR,
    // Watch the workspace sentinel + Natural sources so the server can react to
    // out-of-editor changes; the server also runs its own fs watcher.
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher(
        "**/*.{nsp,nsn,nss,nsc,nsm,nsl,nsg,nsa,nsh,nsd,ns4,ns7,ns3,ns8,nst,NSP,NSN,NSS,NSC,NSM,NSL,NSG,NSA,NSH,NSD,NS4,NS7,NS3,NS8,NST}",
      ),
    },
  };

  return new LanguageClient(
    "naturalLsp",
    "Natural LSP",
    serverOptions,
    clientOptions,
  );
}

/**
 * startClient builds and starts the language client. On failure to start (most
 * commonly: the server binary is not on PATH and no `serverPath` is configured)
 * it surfaces an actionable notification and returns without throwing — the
 * client-side analogue of the server's graceful-degradation stance (FR-43).
 */
async function startClient(): Promise<void> {
  client = buildClient();
  try {
    await client.start();
  } catch (err) {
    const configured = resolveServerPath(
      vscode.workspace.getConfiguration("naturalLsp"),
    );
    const detail = err instanceof Error ? err.message : String(err);
    void vscode.window.showErrorMessage(
      `Natural LSP: failed to start the language server ("${configured}"). ` +
        `Ensure the natural-lsp binary is installed and on your PATH, or set ` +
        `the "naturalLsp.serverPath" setting to its full path. (${detail})`,
    );
    client = undefined;
  }
}

/** The API object returned from activate — used only by integration tests. */
export interface NaturalLspApi {
  getClient(): LanguageClient | undefined;
}

/** activate is called by VS Code when the first `natural` document is opened. */
export function activate(context: vscode.ExtensionContext): NaturalLspApi {
  context.subscriptions.push(
    vscode.commands.registerCommand("naturalLsp.restart", async () => {
      if (client) {
        await client.stop();
        client = undefined;
      }
      await startClient();
    }),
  );

  // Kick off the client start; activation itself never blocks on / throws from it.
  void startClient();

  return { getClient };
}

/**
 * getClient exposes the current LanguageClient for integration tests only. It is
 * not part of the extension's public contract; production code never calls it.
 */
export function getClient(): LanguageClient | undefined {
  return client;
}

/** deactivate is called by VS Code on shutdown; it stops the running client. */
export function deactivate(): Thenable<void> | undefined {
  if (!client) {
    return undefined;
  }
  const stopping = client.stop();
  client = undefined;
  return stopping;
}
