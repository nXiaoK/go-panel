import { beforeEach, describe, expect, it, vi } from "vitest";

import { loadSortedForwards } from "../src/lib/forward-list";

describe("loadSortedForwards", () => {
  beforeEach(() => window.localStorage.clear());

  it("uses one API request and sorts by backend order", async () => {
    const fetchForwards = vi.fn(async () => ({
      code: 0,
      msg: "ok",
      data: [
        { id: 1, name: "later", inx: 2 },
        { id: 2, name: "first", inx: 1 },
      ],
    }));

    const result = await loadSortedForwards(fetchForwards);

    expect(fetchForwards).toHaveBeenCalledTimes(1);
    expect(result.map((forward) => forward.id)).toEqual([2, 1]);
  });

  it("falls back to the locally persisted order when backend order is absent", async () => {
    window.localStorage.setItem("forward-order", JSON.stringify([3, 1]));
    const fetchForwards = vi.fn(async () => ({
      code: 0,
      msg: "ok",
      data: [
        { id: 1, name: "one", inx: 0 },
        { id: 2, name: "two", inx: 0 },
        { id: 3, name: "three", inx: 0 },
      ],
    }));

    const result = await loadSortedForwards(fetchForwards);

    expect(fetchForwards).toHaveBeenCalledTimes(1);
    expect(result.map((forward) => forward.id)).toEqual([3, 1, 2]);
  });
});
