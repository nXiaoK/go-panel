import { describe, expect, it } from "vitest";
import { forwardSearch } from "./search";
describe("forward search validation", () => {
  it("retains numeric tunnel and paused status from native URL parsing", () => {
    expect(forwardSearch({ tunnel: 3, status: 0, focus: "231", q: 20001 })).toMatchObject({
      tunnel: 3,
      status: "0",
      focus: 231,
      q: "20001",
    });
  });
  it("overwrites invalid provided keys while preserving unrelated resource parameters", () => {
    expect(
      forwardSearch({ tunnel: {}, status: "invalid", sort: "bad", focus: -1, action: "create" }),
    ).toMatchObject({ tunnel: "all", status: "all", sort: "", focus: undefined, action: "create" });
  });
});
