import * as assert from "assert";
import * as fs from "fs";
import * as path from "path";
import * as oniguruma from "vscode-oniguruma";
import * as vsctm from "vscode-textmate";

const GRAMMAR_PATH = path.resolve(
  __dirname,
  "../../../syntaxes/natural.tmLanguage.json",
);

/** loadRegistry wires vscode-textmate onto the vscode-oniguruma WASM engine. */
async function loadRegistry(): Promise<vsctm.Registry> {
  const wasmPath = require.resolve("vscode-oniguruma/release/onig.wasm");
  const wasmBin = fs.readFileSync(wasmPath).buffer;
  const vscodeOnigurumaLib: vsctm.IOnigLib = await oniguruma
    .loadWASM(wasmBin)
    .then(() => ({
      createOnigScanner: (patterns: string[]) =>
        new oniguruma.OnigScanner(patterns),
      createOnigString: (s: string) => new oniguruma.OnigString(s),
    }));

  return new vsctm.Registry({
    onigLib: Promise.resolve(vscodeOnigurumaLib),
    loadGrammar: async (scopeName: string) => {
      if (scopeName === "source.natural") {
        const content = fs.readFileSync(GRAMMAR_PATH, "utf8");
        return vsctm.parseRawGrammar(content, GRAMMAR_PATH);
      }
      return null;
    },
  });
}

/**
 * scopesFor tokenizes a single line and returns, for the token containing the
 * byte offset `at`, its full scope stack (outer→inner).
 */
function scopesForToken(
  tokens: vsctm.IToken[],
  at: number,
): string[] {
  for (const t of tokens) {
    if (at >= t.startIndex && at < t.endIndex) {
      return t.scopes;
    }
  }
  return [];
}

describe("natural.tmLanguage grammar scopes", () => {
  let grammar: vsctm.IGrammar | null;

  before(async () => {
    const registry = await loadRegistry();
    grammar = await registry.loadGrammar("source.natural");
    assert.ok(grammar, "grammar source.natural should load");
  });

  function tokenize(line: string): vsctm.IToken[] {
    assert.ok(grammar);
    return grammar!.tokenizeLine(line, vsctm.INITIAL).tokens;
  }

  it("scopes a full-line asterisk comment", () => {
    const line = "* this is a comment";
    const scopes = scopesForToken(tokenize(line), 2);
    assert.ok(
      scopes.some((s) => s.startsWith("comment.line")),
      `expected a comment scope, got ${JSON.stringify(scopes)}`,
    );
  });

  it("scopes a rest-of-line /* comment", () => {
    const line = "CALLNAT 'X' /* trailing note";
    const scopes = scopesForToken(tokenize(line), 14);
    assert.ok(
      scopes.some((s) => s.startsWith("comment.line")),
      `expected a comment scope, got ${JSON.stringify(scopes)}`,
    );
  });

  it("scopes a single-quoted string literal", () => {
    const line = "CALLNAT 'SUBPROG'";
    const scopes = scopesForToken(tokenize(line), 10);
    assert.ok(
      scopes.some((s) => s.startsWith("string.quoted.single")),
      `expected a string scope, got ${JSON.stringify(scopes)}`,
    );
  });

  it("scopes a numeric literal", () => {
    const line = "MOVE 1234 TO #N";
    const scopes = scopesForToken(tokenize(line), 6);
    assert.ok(
      scopes.some((s) => s.startsWith("constant.numeric")),
      `expected a numeric scope, got ${JSON.stringify(scopes)}`,
    );
  });

  it("scopes a keyword (case-insensitive)", () => {
    const upper = scopesForToken(tokenize("CALLNAT 'X'"), 2);
    assert.ok(
      upper.some((s) => s.startsWith("keyword")),
      `expected a keyword scope for CALLNAT, got ${JSON.stringify(upper)}`,
    );
    const lower = scopesForToken(tokenize("perform SUB"), 2);
    assert.ok(
      lower.some((s) => s.startsWith("keyword")),
      `expected a keyword scope for lowercase perform, got ${JSON.stringify(lower)}`,
    );
  });
});
