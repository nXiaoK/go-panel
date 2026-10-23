import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { importTs } from "./import-ts.mjs";

const { buildGlobalSearchItems, filterGlobalSearchItems, normalizeApiList } = await importTs(
  new URL("../src/features/search/model.ts", import.meta.url),
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
