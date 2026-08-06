import * as assert from "assert";
import * as fs from "fs";
import * as path from "path";

const EXT_ROOT = path.resolve(__dirname, "../../..");
const VSCODEIGNORE_PATH = path.join(EXT_ROOT, ".vscodeignore");
const PACKAGE_JSON_PATH = path.join(EXT_ROOT, "package.json");

/**
 * Packaging invariants for the .vsix (regression guard for the v1.0.1 crash).
 *
 * The extension is NOT bundled — `main` is plain `tsc` output (`./out/extension.js`)
 * which `require()`s its runtime dependency `vscode-languageclient` from
 * `node_modules` at activation. Therefore the production dependency tree MUST be
 * packaged into the .vsix. A `.vscodeignore` that excludes `node_modules` strips
 * the dependency and the extension crashes on activation with
 * "Cannot find module 'vscode-languageclient'".
 *
 * These pure-fs tests (no vscode/electron API) run in the `vscode-extension` CI
 * job via the `out/test/unit/**` glob and fail fast if the misconfiguration
 * returns. If a bundler (esbuild/webpack) is ever adopted, update both these
 * assertions and `.vscodeignore` together.
 */
describe("vsix packaging invariants", () => {
  it(".vscodeignore does not exclude node_modules (extension is unbundled)", () => {
    const raw = fs.readFileSync(VSCODEIGNORE_PATH, "utf8");
    const offending = raw
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter((line) => line.length > 0 && !line.startsWith("#"))
      // Any glob whose first path segment is `node_modules` would drop the
      // runtime dependency tree from the .vsix.
      .filter((line) => /^!?node_modules(\/|$)/.test(line));

    assert.deepStrictEqual(
      offending,
      [],
      `.vscodeignore must not exclude node_modules while the extension is unbundled ` +
        `(would strip vscode-languageclient and crash activation). Offending line(s): ${JSON.stringify(offending)}`,
    );
  });

  it(".vscodeignore does not exclude bin/ (holds the bundled server binary)", () => {
    const raw = fs.readFileSync(VSCODEIGNORE_PATH, "utf8");
    const offending = raw
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter((line) => line.length > 0 && !line.startsWith("#"))
      // Any glob whose first path segment is `bin` would drop the bundled
      // natural-lsp server binary from the .vsix (feature 37), so the extension
      // would fall back to PATH and users lose the zero-deploy launch.
      .filter((line) => /^!?bin(\/|$)/.test(line));

    assert.deepStrictEqual(
      offending,
      [],
      `.vscodeignore must not exclude bin/ — it carries the bundled server binary (feature 37). ` +
        `Offending line(s): ${JSON.stringify(offending)}`,
    );
  });

  it("declares vscode-languageclient as a runtime dependency", () => {
    const pkg = JSON.parse(fs.readFileSync(PACKAGE_JSON_PATH, "utf8"));
    assert.ok(
      pkg.dependencies && typeof pkg.dependencies["vscode-languageclient"] === "string",
      "vscode-languageclient must be a runtime dependency in package.json",
    );
  });

  it("main points at unbundled tsc output (so node_modules must ship)", () => {
    const pkg = JSON.parse(fs.readFileSync(PACKAGE_JSON_PATH, "utf8"));
    assert.strictEqual(
      pkg.main,
      "./out/extension.js",
      "main is expected to be plain tsc output; if this changes to a bundle, revisit .vscodeignore and these guards",
    );
  });
});
