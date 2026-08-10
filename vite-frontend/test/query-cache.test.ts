import { describe, expect, it } from "vitest";

import { queries } from "../src/lib/api/query";

describe("query cache keys", () => {
  it("keeps raw and runtime node lists in separate cache entries", () => {
    expect(queries.node.rawList()).not.toEqual(queries.node.runtimeList());
    expect(queries.node.rawList().slice(0, 2)).toEqual(queries.node.list());
    expect(queries.node.runtimeList().slice(0, 2)).toEqual(queries.node.list());
  });
});
