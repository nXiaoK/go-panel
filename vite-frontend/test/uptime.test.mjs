import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { importTs } from "./import-ts.mjs";

const { formatUptimeSeconds } = await importTs(new URL("../src/lib/uptime.ts", import.meta.url));

describe("formatUptimeSeconds", () => {
  it("formats zero and short sessions", () => {
    assert.equal(formatUptimeSeconds(0), "0分钟");
    assert.equal(formatUptimeSeconds(152), "2分钟");
  });

  it("formats multi-day durations without hard-coded uptime", () => {
    const seconds = 42 * 24 * 60 * 60 + 7 * 60 * 60 + 12 * 60;
    assert.equal(formatUptimeSeconds(seconds), "42天7小时");
  });
});
