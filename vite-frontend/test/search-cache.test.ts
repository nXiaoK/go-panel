import { describe, expect, it, vi } from "vitest";

import { SearchCache } from "../src/lib/search-cache";

describe("SearchCache", () => {
  it("reloads after CRUD invalidation", async () => {
    const now = 1000;
    const cache = new SearchCache(60000, () => now);
    const loader = vi.fn().mockResolvedValue([{ id: "node-1" }]);
    await cache.load("admin", loader);
    cache.invalidate("node");
    await cache.load("admin", loader);
    expect(loader).toHaveBeenCalledTimes(2);
  });

  it("shares one in-flight promise per key", async () => {
    let resolve!: (value: string[]) => void;
    const loader = vi.fn(
      () =>
        new Promise<string[]>((r) => {
          resolve = r;
        }),
    );
    const cache = new SearchCache(60000, () => 0);
    const a = cache.load("admin", loader);
    const b = cache.load("admin", loader);
    expect(loader).toHaveBeenCalledTimes(1);
    resolve(["x"]);
    await expect(a).resolves.toEqual(["x"]);
    await expect(b).resolves.toEqual(["x"]);
  });

  it("expires after TTL and does not cache failures", async () => {
    let now = 0;
    const cache = new SearchCache(1000, () => now);
    const loader = vi.fn().mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(["ok"]);

    await expect(cache.load("user", loader)).rejects.toThrow("boom");
    expect(cache.get("user")).toBeUndefined();

    await expect(cache.load("user", loader)).resolves.toEqual(["ok"]);
    expect(loader).toHaveBeenCalledTimes(2);

    now = 1001;
    const loader2 = vi.fn().mockResolvedValue(["fresh"]);
    await expect(cache.load("user", loader2)).resolves.toEqual(["fresh"]);
    expect(loader2).toHaveBeenCalledTimes(1);
  });
});
