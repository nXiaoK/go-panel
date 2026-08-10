import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
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

const { buildGlobalSearchItems, filterGlobalSearchItems, normalizeApiList } = await importTs(
  new URL("../src/lib/global-search.ts", import.meta.url),
);

describe("global search helpers", () => {
  it("normalizes array and paged backend responses", () => {
    assert.deepEqual(normalizeApiList([{ id: 1 }]), [{ id: 1 }]);
    assert.deepEqual(normalizeApiList({ records: [{ id: 2 }] }), [{ id: 2 }]);
    assert.deepEqual(normalizeApiList(null), []);
  });

  it("builds navigable search items from backend lists", () => {
    const items = buildGlobalSearchItems({
      tunnels: [{ id: 7, name: "api-edge", protocol: "tcp", status: 1 }],
      nodes: [{ id: 2, name: "HK-01", serverIp: "192.0.2.10", status: 1 }],
      users: [{ id: 3, user: "alice", name: "Alice", status: 1 }],
      forwards: [{ id: 4, name: "ssh-forward", remoteAddr: "10.0.0.8", inPort: 22 }],
    });

    assert.deepEqual(
      items.map((item) => item.href),
      ["/tunnel", "/node", "/user", "/forward"],
    );
    assert.equal(items.find((item) => item.id === "tunnel-7")?.title, "api-edge");
    assert.match(items.find((item) => item.id === "forward-4")?.subtitle || "", /10\.0\.0\.8/);
  });

  it("filters by title, subtitle, and keywords", () => {
    const items = buildGlobalSearchItems({
      tunnels: [{ id: 7, name: "api-edge", protocol: "tcp", status: 1 }],
      nodes: [{ id: 2, name: "HK-01", serverIp: "192.0.2.10", forwardMode: "nftables" }],
      users: [{ id: 3, user: "alice", name: "Alice", status: 1 }],
      forwards: [{ id: 4, name: "ssh-forward", remoteAddr: "10.0.0.8", inPort: 22 }],
    });

    assert.deepEqual(
      filterGlobalSearchItems(items, "hk").map((item) => item.id),
      ["node-2"],
    );
    assert.deepEqual(
      filterGlobalSearchItems(items, "nft").map((item) => item.id),
      ["node-2"],
    );
    assert.deepEqual(
      filterGlobalSearchItems(items, "alice").map((item) => item.id),
      ["user-3"],
    );
  });
});
