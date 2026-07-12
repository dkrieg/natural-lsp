import * as assert from "assert";
import * as path from "path";
import * as vscode from "vscode";

// The 15 Natural file types, mapped to the sample fixture file for each.
// Opening any of them must be recognized as the `natural` language.
const FIXTURES: Record<string, string> = {
  ".NSP": "HELLO.NSP",
  ".NSN": "GREET.NSN",
  ".NSS": "SUB.NSS",
  ".NSC": "COPY.NSC",
  ".NSM": "MENU.NSM",
  ".NSL": "GLOBALS.NSL",
  ".NSG": "SHARED.NSG",
  ".NSA": "PARMS.NSA",
  ".NSH": "HELPER.NSH",
  ".NSD": "CUSTOMER.NSD",
  ".NS4": "WIDGET.NS4",
  ".NS7": "CALC.NS7",
  ".NS3": "DLG.NS3",
  ".NS8": "ADAPT.NS8",
  ".NST": "NOTES.NST",
};

function workspaceDir(): string {
  return path.resolve(__dirname, "../../../src/test/fixtures/workspace");
}

describe("Natural file-type association", () => {
  for (const [ext, file] of Object.entries(FIXTURES)) {
    it(`recognizes ${ext} as language 'natural'`, async () => {
      const uri = vscode.Uri.file(path.join(workspaceDir(), file));
      const doc = await vscode.workspace.openTextDocument(uri);
      assert.strictEqual(
        doc.languageId,
        "natural",
        `${file} should be languageId 'natural' but was '${doc.languageId}'`,
      );
    });
  }
});
