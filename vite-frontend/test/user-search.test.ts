import { describe, expect, it } from "vitest";

import { filterUsers } from "../src/lib/user-search";

describe("filterUsers", () => {
  it("filters locally by username without requesting", () => {
    expect(filterUsers([{ user: "Alice" }, { user: "Bob" }], "ali")).toEqual([{ user: "Alice" }]);
  });

  it("is case-insensitive and trims the query", () => {
    expect(filterUsers([{ user: "Alice" }, { user: "bob" }], "  BOB ")).toEqual([{ user: "bob" }]);
  });

  it("returns every user for an empty query", () => {
    const users = [{ user: "a" }, { user: "b" }];
    expect(filterUsers(users, "")).toEqual(users);
    expect(filterUsers(users, "   ")).toEqual(users);
  });

  it("tolerates missing usernames", () => {
    expect(filterUsers([{ user: undefined }, { user: "match" }], "match")).toEqual([
      { user: "match" },
    ]);
  });
});
