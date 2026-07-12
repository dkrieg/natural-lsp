// Mocha entry point run *inside* the VS Code extension host by @vscode/test-electron.
// It discovers and runs the compiled *.test.js files in this directory.

import * as path from "path";
import Mocha from "mocha";
import { glob } from "glob";

export async function run(): Promise<void> {
  const mocha = new Mocha({
    ui: "bdd",
    color: true,
    timeout: 60000,
  });

  const testsRoot = __dirname;
  const files = await glob("**/*.test.js", { cwd: testsRoot });

  for (const f of files) {
    mocha.addFile(path.resolve(testsRoot, f));
  }

  await new Promise<void>((resolve, reject) => {
    try {
      mocha.run((failures) => {
        if (failures > 0) {
          reject(new Error(`${failures} test(s) failed.`));
        } else {
          resolve();
        }
      });
    } catch (err) {
      reject(err instanceof Error ? err : new Error(String(err)));
    }
  });
}
