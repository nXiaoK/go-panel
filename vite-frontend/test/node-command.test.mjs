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

const { isMissingPanelAddressError } = await importTs(
  new URL("../src/lib/node-command.ts", import.meta.url),
);

describe("isMissingPanelAddressError", () => {
  it("detects backend messages for missing panel public address", () => {
    assert.equal(isMissingPanelAddressError("请先前往网站配置中设置ip"), true);
    assert.equal(isMissingPanelAddressError("请先设置面板公网地址"), true);
  });

  it("does not match unrelated command errors", () => {
    assert.equal(isMissingPanelAddressError("节点不存在"), false);
    assert.equal(isMissingPanelAddressError(""), false);
  });
});

describe("node upgrade request timeout", () => {
  it("waits longer than the backend WebSocket upgrade command", async () => {
    const source = await readFile(new URL("../src/lib/api/index.ts", import.meta.url), "utf8");
    assert.match(source, /const nodeUpgradeRequestTimeoutMs = 100_000/);
    assert.match(
      source,
      /postWithTimeout\("\/node\/upgrade", \{ id \}, nodeUpgradeRequestTimeoutMs\)/,
    );
  });
});
