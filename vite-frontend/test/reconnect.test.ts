import { describe, expect, it } from "vitest";

import { reconnectDelay } from "../src/lib/reconnect";

describe("reconnectDelay", () => {
  it("keeps retrying after five failures and caps at 30 seconds", () => {
    expect(reconnectDelay(0, () => 0.5)).toBe(1000);
    expect(reconnectDelay(1, () => 0.5)).toBe(2000);
    expect(reconnectDelay(4, () => 0.5)).toBe(16000);
    expect(reconnectDelay(5, () => 0.5)).toBe(30000);
    expect(reconnectDelay(8, () => 0.5)).toBe(30000);
    expect(reconnectDelay(100, () => 0.5)).toBe(30000);
  });

  it("applies ±20% jitter", () => {
    expect(reconnectDelay(0, () => 0)).toBe(800);
    expect(reconnectDelay(0, () => 1)).toBe(1200);
  });

  it("treats negative attempts as the first attempt", () => {
    expect(reconnectDelay(-3, () => 0.5)).toBe(1000);
  });
});
