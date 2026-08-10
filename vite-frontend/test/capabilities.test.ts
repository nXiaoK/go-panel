import { describe, expect, it } from "vitest";

import { canAccessPath, ADMIN_ROLE_ID } from "../src/lib/capabilities";
import { navigationForRole } from "../src/lib/navigation";

describe("canAccessPath", () => {
  it("grants administrators every path", () => {
    for (const path of ["/", "/node", "/user", "/tunnel", "/config", "/forward"]) {
      expect(canAccessPath(ADMIN_ROLE_ID, path)).toBe(true);
    }
  });

  it("hides administrator routes from a normal user", () => {
    expect(canAccessPath(1, "/node")).toBe(false);
    expect(canAccessPath(1, "/user")).toBe(false);
    expect(canAccessPath(1, "/tunnel")).toBe(false);
    expect(canAccessPath(1, "/limit")).toBe(false);
    expect(canAccessPath(1, "/subscription")).toBe(false);
    expect(canAccessPath(1, "/config")).toBe(false);
  });

  it("allows normal users their own routes", () => {
    expect(canAccessPath(1, "/")).toBe(true);
    expect(canAccessPath(1, "/forward")).toBe(true);
    expect(canAccessPath(1, "/profile")).toBe(true);
    expect(canAccessPath(1, "/settings")).toBe(true);
  });

  it("matches nested paths against the prefix", () => {
    expect(canAccessPath(1, "/node/123")).toBe(false);
    expect(canAccessPath(1, "/forward/new")).toBe(true);
  });

  it("treats an unknown role as non-administrator", () => {
    expect(canAccessPath(null, "/node")).toBe(false);
    expect(canAccessPath(null, "/forward")).toBe(true);
  });
});

describe("navigationForRole", () => {
  it("excludes administrator items for a normal user", () => {
    const items = navigationForRole(1).flatMap((g) => g.items);
    expect(items.some((i) => i.url === "/node")).toBe(false);
    expect(items.some((i) => i.url === "/user")).toBe(false);
    expect(items.some((i) => i.url === "/forward")).toBe(true);
  });

  it("includes administrator items for role zero", () => {
    const items = navigationForRole(0).flatMap((g) => g.items);
    expect(items.some((i) => i.url === "/node")).toBe(true);
    expect(items.some((i) => i.url === "/user")).toBe(true);
  });

  it("drops groups that end up empty", () => {
    for (const group of navigationForRole(1)) {
      expect(group.items.length).toBeGreaterThan(0);
    }
  });
});
