import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { importTs } from "./import-ts.mjs";

const { formatLatencyMs, formatLossPercent, formatSpeedRateFromMbps } = await importTs(
  new URL("../src/lib/speed-rate.ts", import.meta.url),
);

describe("speed rate formatting", () => {
  it("formats iperf Mbps as uppercase byte units", () => {
    assert.deepEqual(formatSpeedRateFromMbps(800), { value: "100.00", unit: "MB/s" });
    assert.deepEqual(formatSpeedRateFromMbps(8192), { value: "1.00", unit: "GB/s" });
  });

  it("formats latency and packet loss metrics", () => {
    assert.equal(formatLatencyMs(12.345), "12.35 ms");
    assert.equal(formatLossPercent(3.456), "3.46%");
    assert.equal(formatLatencyMs(undefined), "--");
    assert.equal(formatLossPercent(null), "--");
  });
});
