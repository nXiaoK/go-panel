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

const { formatUptimeSeconds } = await importTs(new URL("../src/lib/uptime.ts", import.meta.url));

describe("formatUptimeSeconds", () => {
  it("formats zero and short sessions", () => {
    assert.equal(formatUptimeSeconds(0), "00d 00h 00m");
    assert.equal(formatUptimeSeconds(152), "00d 00h 02m");
  });

  it("formats multi-day durations without hard-coded uptime", () => {
    const seconds = 42 * 24 * 60 * 60 + 7 * 60 * 60 + 12 * 60;
    assert.equal(formatUptimeSeconds(seconds), "42d 07h 12m");
  });
});
