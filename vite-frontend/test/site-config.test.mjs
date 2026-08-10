import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import ts from "typescript";

async function importTs(path) {
  const source = await readFile(path, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2022,
      verbatimModuleSyntax: false,
    },
  }).outputText;
  return import(`data:text/javascript;charset=utf-8,${encodeURIComponent(compiled)}`);
}

const { normalizeAppName, appNameFromConfigs } = await importTs(
  new URL("../src/lib/site-config.ts", import.meta.url),
);

describe("site config helpers", () => {
  it("normalizes blank app names to the default brand", () => {
    assert.equal(normalizeAppName(""), "Flux Panel");
    assert.equal(normalizeAppName("   "), "Flux Panel");
  });

  it("extracts app_name from backend config data", () => {
    assert.equal(appNameFromConfigs({ app_name: "My Panel" }), "My Panel");
    assert.equal(appNameFromConfigs([{ name: "app_name", value: "Ops" }]), "Ops");
  });
});
