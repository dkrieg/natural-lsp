import * as assert from "assert";
import * as path from "path";
import {
  DEFAULT_SERVER_PATH,
  resolveServerPath,
  bundledServerPath,
  describeServerChoice,
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

describe("resolveServerPath (existing behavior)", () => {
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

describe("bundledServerPath", () => {
  it("returns bundled path ending in bin/natural-lsp for non-Windows platforms", () => {
    const bundled = bundledServerPath("/ext/path", "linux");
    assert.ok(bundled.endsWith(path.join("bin", "natural-lsp")), `Expected bundled path to end with bin/natural-lsp, got ${bundled}`);
  });

  it("returns bundled path ending in bin/natural-lsp for darwin (macOS)", () => {
    const bundled = bundledServerPath("/ext/path", "darwin");
    assert.ok(bundled.endsWith(path.join("bin", "natural-lsp")), `Expected bundled path to end with bin/natural-lsp, got ${bundled}`);
  });

  it("returns bundled path ending in bin/natural-lsp.exe for Windows (win32)", () => {
    const bundled = bundledServerPath("/ext/path", "win32");
    assert.ok(bundled.endsWith(path.join("bin", "natural-lsp.exe")), `Expected bundled path to end with bin/natural-lsp.exe, got ${bundled}`);
  });

  it("includes the extension path in the returned bundled path", () => {
    const bundled = bundledServerPath("/my/ext", "linux");
    assert.ok(
      bundled.includes("/my/ext") || bundled.includes("\\my\\ext"),
      `Expected bundled path to include extension path, got ${bundled}`,
    );
  });

  it("computes the correct bundled path for multiple extension paths", () => {
    const linuxPath = bundledServerPath("/opt/vscode/ext", "linux");
    assert.ok(linuxPath.includes("opt"), `Expected linux path to include 'opt', got ${linuxPath}`);

    const winPath = bundledServerPath("C:\\Users\\dev\\ext", "win32");
    assert.ok(winPath.includes("Users"), `Expected Windows path to include 'Users', got ${winPath}`);
  });
});

describe("resolveServerPath (bundled-binary tier)", () => {
  const bundledCandidate = {
    path: "/ext/bin/natural-lsp",
    exists: (p: string) => p === "/ext/bin/natural-lsp",
  };

  const bundledCandidateWin = {
    path: "C:\\ext\\bin\\natural-lsp.exe",
    exists: (p: string) => p === "C:\\ext\\bin\\natural-lsp.exe",
  };

  it("prefers serverPath setting even when bundled binary exists (override wins)", () => {
    const result = resolveServerPath(
      stubConfig("/usr/local/bin/natural-lsp"),
      bundledCandidate,
    );
    assert.strictEqual(result, "/usr/local/bin/natural-lsp");
  });

  it("returns bundled path when serverPath is unset and bundled exists", () => {
    const result = resolveServerPath(stubConfig(undefined), bundledCandidate);
    assert.strictEqual(result, "/ext/bin/natural-lsp");
  });

  it("returns bundled path when serverPath is empty string and bundled exists", () => {
    const result = resolveServerPath(stubConfig(""), bundledCandidate);
    assert.strictEqual(result, "/ext/bin/natural-lsp");
  });

  it("returns DEFAULT_SERVER_PATH when serverPath is unset and bundled does not exist", () => {
    const noBundled = {
      path: "/ext/bin/natural-lsp",
      exists: () => false, // Binary doesn't exist
    };
    const result = resolveServerPath(stubConfig(undefined), noBundled);
    assert.strictEqual(result, DEFAULT_SERVER_PATH);
  });

  it("returns DEFAULT_SERVER_PATH when neither serverPath nor bundled candidate is provided", () => {
    const result = resolveServerPath(stubConfig(undefined)); // No bundled arg
    assert.strictEqual(result, DEFAULT_SERVER_PATH);
  });

  it("respects bundled candidate on Windows with .exe suffix", () => {
    const result = resolveServerPath(stubConfig(undefined), bundledCandidateWin);
    assert.strictEqual(result, "C:\\ext\\bin\\natural-lsp.exe");
  });

  it("returns DEFAULT_SERVER_PATH when serverPath is unset and bundled candidate not provided", () => {
    // Backward compatibility: calling without bundled arg should work exactly as before
    assert.strictEqual(
      resolveServerPath(stubConfig(undefined)),
      DEFAULT_SERVER_PATH,
    );
    assert.strictEqual(
      resolveServerPath(stubConfig("")),
      DEFAULT_SERVER_PATH,
    );
    assert.strictEqual(
      resolveServerPath(stubConfig("   ")),
      DEFAULT_SERVER_PATH,
    );
  });

  it("applies serverPath trimming before checking precedence with bundled", () => {
    const result = resolveServerPath(
      stubConfig("  /opt/custom/nlsp  "),
      bundledCandidate,
    );
    assert.strictEqual(result, "/opt/custom/nlsp");
  });
});

describe("describeServerChoice", () => {
  it("returns 'serverPath setting' when serverPath setting is used", () => {
    const choice = describeServerChoice({
      config: stubConfig("/custom/path"),
      bundled: undefined,
      resolvedPath: "/custom/path",
    });
    assert.strictEqual(choice, "serverPath setting");
  });

  it("returns 'bundled' when bundled binary was selected", () => {
    const choice = describeServerChoice({
      config: stubConfig(undefined),
      bundled: { path: "/ext/bin/natural-lsp", exists: () => true },
      resolvedPath: "/ext/bin/natural-lsp",
    });
    assert.strictEqual(choice, "bundled");
  });

  it("returns 'PATH' when DEFAULT_SERVER_PATH was selected", () => {
    const choice = describeServerChoice({
      config: stubConfig(undefined),
      bundled: { path: "/ext/bin/natural-lsp", exists: () => false },
      resolvedPath: DEFAULT_SERVER_PATH,
    });
    assert.strictEqual(choice, "PATH");
  });

  it("returns 'PATH' when neither serverPath nor bundled was provided", () => {
    const choice = describeServerChoice({
      config: stubConfig(undefined),
      bundled: undefined,
      resolvedPath: DEFAULT_SERVER_PATH,
    });
    assert.strictEqual(choice, "PATH");
  });
});
