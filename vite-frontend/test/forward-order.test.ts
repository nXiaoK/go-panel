import { describe, expect, it } from "vitest";

import {
  canReorderForwards,
  forwardOrderIDs,
  moveForwardByID,
} from "../src/features/forward/order";

describe("canReorderForwards", () => {
  it("disables sorting for any active filter", () => {
    expect(canReorderForwards({ query: "ssh", status: "all", tunnelID: null })).toBe(false);
    expect(canReorderForwards({ query: "", status: "1", tunnelID: null })).toBe(false);
    expect(canReorderForwards({ query: "", status: "all", tunnelID: 7 })).toBe(false);
    expect(canReorderForwards({ query: "  ", status: "all", tunnelID: "all" })).toBe(true);
    expect(canReorderForwards({ query: "", status: "all", tunnelID: null })).toBe(true);
  });
});

describe("moveForwardByID", () => {
  it("moves rows to the ends without changing the relative order of other rows", () => {
    const rows = [{ id: 1 }, { id: 2 }, { id: 3 }, { id: 4 }];
    expect(moveForwardByID(rows, 3, "top")).toEqual([
      { id: 3, inx: 1 },
      { id: 1, inx: 2 },
      { id: 2, inx: 3 },
      { id: 4, inx: 4 },
    ]);
    expect(moveForwardByID(rows, 2, "bottom")).toEqual([
      { id: 1, inx: 1 },
      { id: 3, inx: 2 },
      { id: 4, inx: 3 },
      { id: 2, inx: 4 },
    ]);
    expect(rows).toEqual([{ id: 1 }, { id: 2 }, { id: 3 }, { id: 4 }]);
  });

  it("moves the requested full-list neighbor", () => {
    expect(moveForwardByID([{ id: 1 }, { id: 2 }, { id: 3 }], 2, "down").map((x) => x.id)).toEqual([
      1, 3, 2,
    ]);
    expect(moveForwardByID([{ id: 1 }, { id: 2 }, { id: 3 }], 2, "up").map((x) => x.id)).toEqual([
      2, 1, 3,
    ]);
  });

  it("reindexes 1-based and is a no-op at edges", () => {
    const moved = moveForwardByID(
      [
        { id: 10, inx: 1 },
        { id: 20, inx: 2 },
      ],
      10,
      "down",
    );
    expect(moved.map((x) => ({ id: x.id, inx: x.inx }))).toEqual([
      { id: 20, inx: 1 },
      { id: 10, inx: 2 },
    ]);
    expect(moveForwardByID([{ id: 1 }, { id: 2 }], 1, "up")).toEqual([{ id: 1 }, { id: 2 }]);
    expect(moveForwardByID([{ id: 1 }, { id: 2 }], 2, "down")).toEqual([{ id: 1 }, { id: 2 }]);
  });

  it("exports stable id order helpers", () => {
    expect(forwardOrderIDs([{ id: 3 }, { id: 1 }])).toEqual([3, 1]);
  });
});
