import { describe, expect, it } from "vitest";

import { dismissUpdate, displayVersion, shouldPromptForUpdate } from "../src/lib/update";

function memoryStorage() {
  const values = new Map<string, string>();
  return {
    getItem(key: string) {
      return values.get(key) ?? null;
    },
    setItem(key: string, value: string) {
      values.set(key, value);
    },
  };
}

describe("panel update helpers", () => {
  it("formats release and development versions", () => {
    expect(displayVersion("v1.2.3", "abcdef0123")).toBe("v1.2.3");
    expect(displayVersion("dev", "abcdef0123")).toBe("dev-abcdef0");
    expect(displayVersion("", "unknown")).toBe("dev");
  });

  it("only suppresses the exact dismissed release", () => {
    const storage = memoryStorage();
    expect(shouldPromptForUpdate("v1.1.0", storage)).toBe(true);
    dismissUpdate("v1.1.0", storage);
    expect(shouldPromptForUpdate("v1.1.0", storage)).toBe(false);
    expect(shouldPromptForUpdate("v1.2.0", storage)).toBe(true);
  });
});
