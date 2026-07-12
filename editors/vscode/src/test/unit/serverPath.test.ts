import * as assert from "assert";
import {
  DEFAULT_SERVER_PATH,
  resolveServerPath,
  ConfigGetter,
} from "../../serverPath";

/** stubConfig returns a ConfigGetter whose `serverPath` yields the given value. */
function stubConfig(value: unknown): ConfigGetter {
  return {
    get(section: string): unknown {
      return section === "serverPath" ? value : undefined;
    },
  };
}

describe("resolveServerPath", () => {
  it("falls back to the default when the setting is unset (undefined)", () => {
    assert.strictEqual(resolveServerPath(stubConfig(undefined)), DEFAULT_SERVER_PATH);
    assert.strictEqual(DEFAULT_SERVER_PATH, "natural-lsp");
  });

  it("falls back to the default when the setting is an empty string", () => {
    assert.strictEqual(resolveServerPath(stubConfig("")), "natural-lsp");
  });

  it("falls back to the default when the setting is whitespace-only", () => {
    assert.strictEqual(resolveServerPath(stubConfig("   ")), "natural-lsp");
  });

  it("returns the configured path when set", () => {
    assert.strictEqual(
      resolveServerPath(stubConfig("/usr/local/bin/natural-lsp")),
      "/usr/local/bin/natural-lsp",
    );
  });

  it("trims surrounding whitespace from a configured path", () => {
    assert.strictEqual(
      resolveServerPath(stubConfig("  /opt/nlsp  ")),
      "/opt/nlsp",
    );
  });

  it("ignores non-string values and falls back to the default", () => {
    assert.strictEqual(resolveServerPath(stubConfig(42)), "natural-lsp");
    assert.strictEqual(resolveServerPath(stubConfig(null)), "natural-lsp");
  });
});
