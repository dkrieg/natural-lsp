import * as assert from "assert";
import * as fs from "fs";
import * as path from "path";

const TEMPLATE_PATH = path.resolve(
  __dirname,
  "../../../../jetbrains/lsp4ij-template/template.json",
);

/**
 * The 15 required Natural file-type extensions in the LSP4IJ template,
 * normalized to uppercase for coverage comparison.
 */
const REQUIRED_EXTENSIONS = new Set([
  "*.NSP", "*.NSN", "*.NSS", "*.NSC", "*.NSM",
  "*.NSL", "*.NSG", "*.NSA", "*.NSH", "*.NSD",
  "*.NS4", "*.NS7", "*.NS3", "*.NS8", "*.NST",
]);

/**
 * patternSet extracts the union of all fileType.patterns across the template's
 * fileTypeMappings and returns them as a normalized (uppercase) Set.
 */
function patternSet(template: unknown): Set<string> {
  const patterns = new Set<string>();
  if (
    template &&
    typeof template === "object" &&
    "fileTypeMappings" in template
  ) {
    const mappings = (template as Record<string, unknown>).fileTypeMappings;
    if (Array.isArray(mappings)) {
      for (const entry of mappings) {
        if (
          entry &&
          typeof entry === "object" &&
          "fileType" in entry
        ) {
          const fileType = (entry as Record<string, unknown>).fileType;
          if (
            fileType &&
            typeof fileType === "object" &&
            "patterns" in fileType
          ) {
            const patts = (fileType as Record<string, unknown>).patterns;
            if (Array.isArray(patts)) {
              for (const p of patts) {
                if (typeof p === "string") {
                  patterns.add(p.toUpperCase());
                }
              }
            }
          }
        }
      }
    }
  }
  return patterns;
}

describe("LSP4IJ template.json schema", () => {
  let template: unknown;

  before(() => {
    const content = fs.readFileSync(TEMPLATE_PATH, "utf8");
    template = JSON.parse(content);
  });

  it("is a valid JSON object", () => {
    assert.ok(template && typeof template === "object", "template should be an object");
  });

  it("has a non-empty id string", () => {
    assert.ok(
      typeof (template as Record<string, unknown>).id === "string" &&
      ((template as Record<string, unknown>).id as string).length > 0,
      "id should be a non-empty string",
    );
  });

  it("has a name string", () => {
    assert.ok(
      typeof (template as Record<string, unknown>).name === "string",
      "name should be a string",
    );
  });

  it("has programArgs object with non-empty default string", () => {
    const programArgs = (template as Record<string, unknown>).programArgs;
    assert.ok(
      programArgs && typeof programArgs === "object",
      "programArgs should be an object",
    );
    const defaultCmd = (programArgs as Record<string, unknown>).default;
    assert.ok(
      typeof defaultCmd === "string" && (defaultCmd as string).length > 0,
      "programArgs.default should be a non-empty string",
    );
  });

  it("includes --stdio in programArgs.default", () => {
    const programArgs = (template as Record<string, unknown>).programArgs;
    const defaultCmd = (programArgs as Record<string, unknown>).default as string;
    assert.ok(
      defaultCmd.includes("--stdio"),
      "programArgs.default should contain --stdio",
    );
  });

  it("has programArgs.windows as a non-empty string if present", () => {
    const programArgs = (template as Record<string, unknown>).programArgs;
    const windows = (programArgs as Record<string, unknown>).windows;
    if (windows !== undefined) {
      assert.ok(
        typeof windows === "string" && (windows as string).length > 0,
        "programArgs.windows, if present, should be a non-empty string",
      );
    }
  });

  it("has fileTypeMappings as a non-empty array", () => {
    const mappings = (template as Record<string, unknown>).fileTypeMappings;
    assert.ok(
      Array.isArray(mappings) && (mappings as unknown[]).length > 0,
      "fileTypeMappings should be a non-empty array",
    );
  });

  it("has valid fileType and patterns in each mapping", () => {
    const mappings = (template as Record<string, unknown>).fileTypeMappings as unknown[];
    for (const entry of mappings) {
      assert.ok(
        entry && typeof entry === "object",
        "each mapping entry should be an object",
      );
      const fileType = (entry as Record<string, unknown>).fileType;
      assert.ok(
        fileType && typeof fileType === "object",
        "fileType should be an object",
      );
      const patterns = (fileType as Record<string, unknown>).patterns;
      assert.ok(
        Array.isArray(patterns) && (patterns as unknown[]).length > 0,
        "fileType.patterns should be a non-empty array",
      );
      for (const p of patterns as unknown[]) {
        assert.ok(
          typeof p === "string",
          `each pattern should be a string, got ${typeof p}`,
        );
      }
    }
  });

  it("has languageId string in each mapping", () => {
    const mappings = (template as Record<string, unknown>).fileTypeMappings as unknown[];
    for (const entry of mappings) {
      const languageId = (entry as Record<string, unknown>).languageId;
      assert.ok(
        typeof languageId === "string",
        "languageId should be a string",
      );
    }
  });

  it("has a natural languageId in at least one mapping", () => {
    const mappings = (template as Record<string, unknown>).fileTypeMappings as unknown[];
    const hasNatural = mappings.some(
      (entry) => (entry as Record<string, unknown>).languageId === "natural",
    );
    assert.ok(hasNatural, "at least one mapping should have languageId='natural'");
  });

  it("does not have a top-level serverName key", () => {
    assert.ok(
      !("serverName" in (template as Record<string, unknown>)),
      "template should not have a serverName key",
    );
  });

  it("does not have a top-level command key", () => {
    assert.ok(
      !("command" in (template as Record<string, unknown>)),
      "template should not have a command key",
    );
  });

  it("does not have a top-level mappings key", () => {
    assert.ok(
      !("mappings" in (template as Record<string, unknown>)),
      "template should not have a mappings key",
    );
  });

  it("does not have a top-level initializationOptions key", () => {
    assert.ok(
      !("initializationOptions" in (template as Record<string, unknown>)),
      "template should not have an initializationOptions key",
    );
  });

  it("does not have a fileNamePatterns key in any mapping", () => {
    const mappings = (template as Record<string, unknown>).fileTypeMappings as unknown[];
    for (const entry of mappings) {
      assert.ok(
        !("fileNamePatterns" in (entry as Record<string, unknown>)),
        "no mapping entry should have a fileNamePatterns key",
      );
    }
  });

  it("includes all 15 required Natural extensions", () => {
    const patterns = patternSet(template);
    for (const ext of REQUIRED_EXTENSIONS) {
      assert.ok(
        patterns.has(ext),
        `expected pattern ${ext} to be in fileType.patterns (found: ${JSON.stringify([...patterns])}`,
      );
    }
  });

  it("fileType.patterns is a superset of the required 15 extensions", () => {
    const patterns = patternSet(template);
    const missing = [...REQUIRED_EXTENSIONS].filter((ext) => !patterns.has(ext));
    assert.strictEqual(
      missing.length,
      0,
      `all 15 required extensions should be present; missing: ${JSON.stringify(missing)}`,
    );
  });
});
