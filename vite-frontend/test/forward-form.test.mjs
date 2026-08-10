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

const {
  joinTargetAddresses,
  normalizeTargetAddressInput,
  splitTargetAddresses,
  validateForwardForm,
} = await importTs(new URL("../src/lib/forward-form.ts", import.meta.url));

describe("forward form helpers", () => {
  it("allows blank entry port when creating a forward", () => {
    const error = validateForwardForm({
      name: "web",
      tunnelId: 1,
      inPort: null,
      remoteAddr: "10.0.0.8:80",
      strategy: "fifo",
    });

    assert.equal(error, "");
  });

  it("rejects an explicit entry port outside the valid port range", () => {
    const error = validateForwardForm({
      name: "web",
      tunnelId: 1,
      inPort: 70000,
      remoteAddr: "10.0.0.8:80",
      strategy: "fifo",
    });

    assert.equal(error, "入口端口范围应为 1-65535");
  });

  it("extracts host and port from a pasted http url target", () => {
    assert.equal(normalizeTargetAddressInput("http://10.211.55.5:1002/login"), "10.211.55.5:1002");
  });

  it("keeps plain host and port targets unchanged", () => {
    assert.equal(normalizeTargetAddressInput(" 10.211.55.5:1002 "), "10.211.55.5:1002");
  });

  it("splits and rejoins multiple target address fields", () => {
    const targets = splitTargetAddresses("10.0.0.1:80, http://10.0.0.2:8080/login");

    assert.deepEqual(targets, ["10.0.0.1:80", "10.0.0.2:8080"]);
    assert.equal(joinTargetAddresses([...targets, ""]), "10.0.0.1:80,10.0.0.2:8080");
  });
});
