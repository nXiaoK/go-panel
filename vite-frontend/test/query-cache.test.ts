import { describe, expect, it } from "vitest";

import { nodeKeys } from "../src/features/node/queries";

describe("query cache keys", () => {
  it("keeps raw and runtime node lists in separate cache entries", () => {
    expect(nodeKeys.rawList()).not.toEqual(nodeKeys.runtimeList());
    expect(nodeKeys.rawList().slice(0, 2)).toEqual(nodeKeys.list());
    expect(nodeKeys.runtimeList().slice(0, 2)).toEqual(nodeKeys.list());
  });
});
