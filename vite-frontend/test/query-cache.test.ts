import { describe, expect, it } from "vitest";

import { nodeKeys } from "../src/features/node/queries";

describe("query cache keys", () => {
  it("shares REST node cache while runtime state lives outside the query cache", () => {
    expect(nodeKeys.rawList()).toEqual(nodeKeys.runtimeList());
    expect(nodeKeys.rawList().slice(0, 2)).toEqual(nodeKeys.list());
    expect(nodeKeys.runtimeList().slice(0, 2)).toEqual(nodeKeys.list());
  });
});
