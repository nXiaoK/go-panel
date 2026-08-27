import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import ts from "typescript";

async function importTs(path) {
  const source = await readFile(path, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2022,
      verbatimModuleSyntax: false,
    },
  }).outputText;
  return import(`data:text/javascript;charset=utf-8,${encodeURIComponent(compiled)}`);
}

const {
  buildTrafficSeries,
  getTrafficTrendState,
  pickTrafficHoverPoint,
  resolveTrafficTotalBytes,
  trafficRangeMeta,
  trafficTrendEmptyDescription,
  trafficTunnelLabel,
  trafficTunnelRequestId,
} = await importTs(new URL("../src/lib/dashboard-traffic.ts", import.meta.url));

const gb = 1024 ** 3;

describe("dashboard traffic helpers", () => {
  it("keeps a 24 point hourly series and places newest data at the end", () => {
    const series = buildTrafficSeries([{ time: "23:00", flow: gb, totalFlow: gb * 2 }], "24h");

    assert.equal(series.length, 24);
    assert.equal(series.at(-1)?.time, "23:00");
    assert.equal(series.at(-1)?.down, 1);
    assert.equal(series.at(-1)?.up, 0);
  });

  it("does not render cumulative totalFlow as upstream traffic", () => {
    const series = buildTrafficSeries([{ time: "03:00", flow: 0, totalFlow: gb * 7.484 }], "24h");

    assert.equal(series.at(-1)?.time, "03:00");
    assert.equal(series.at(-1)?.down, 0);
    assert.equal(series.at(-1)?.up, 0);
  });

  it("treats migrated zero directional columns as legacy total-only rows", () => {
    const series = buildTrafficSeries(
      [
        {
          time: "03:00",
          flow: gb,
          inFlow: 0,
          outFlow: 0,
          totalFlow: gb * 7.484,
          totalInFlow: 0,
          totalOutFlow: 0,
        },
      ],
      "24h",
    );

    assert.equal(series.at(-1)?.down, 1);
    assert.equal(series.at(-1)?.up, 0);
  });

  it("uses directional increments when the backend provides them", () => {
    const series = buildTrafficSeries(
      [{ time: "23:00", flow: gb * 3, inFlow: gb, outFlow: gb * 2, totalFlow: gb * 100 }],
      "24h",
    );

    assert.equal(series.at(-1)?.down, 1);
    assert.equal(series.at(-1)?.up, 2);
  });

  it("aggregates authoritative hourly increments into today for longer ranges", () => {
    const rows = [
      { time: "22:00", flow: gb, inFlow: gb * 0.25, outFlow: gb * 0.75 },
      { time: "23:00", flow: gb * 3, inFlow: gb, outFlow: gb * 2 },
    ];

    const sevenDays = buildTrafficSeries(rows, "7d");
    const thirtyDays = buildTrafficSeries(rows, "30d");

    assert.equal(sevenDays.length, 7);
    assert.equal(thirtyDays.length, 30);
    assert.equal(sevenDays.at(-1)?.time, "今天");
    assert.equal(sevenDays.at(-1)?.down, 1.25);
    assert.equal(sevenDays.at(-1)?.up, 2.75);
    assert.equal(thirtyDays.at(-1)?.down, 1.25);
  });

  it("preserves backend daily buckets for longer ranges", () => {
    const series = buildTrafficSeries(
      [
        { time: "06-30", flow: gb, totalFlow: gb * 30 },
        { time: "07-01", flow: gb * 2, totalFlow: gb * 32 },
      ],
      "7d",
    );

    assert.equal(series.length, 7);
    assert.equal(series.at(-2)?.time, "06-30");
    assert.equal(series.at(-2)?.down, 1);
    assert.equal(series.at(-1)?.time, "07-01");
    assert.equal(series.at(-1)?.down, 2);
    assert.equal(series.at(-1)?.up, 0);
  });

  it("keeps authoritative buckets stable when cumulative counters reset", () => {
    const series = buildTrafficSeries(
      [
        {
          time: "11:00",
          flow: gb * 0.75,
          inFlow: gb * 0.5,
          outFlow: gb * 0.25,
          totalFlow: gb * 0.75,
          totalInFlow: gb * 0.5,
          totalOutFlow: gb * 0.25,
        },
        {
          time: "10:00",
          flow: gb * 3,
          inFlow: gb,
          outFlow: gb * 2,
          totalFlow: gb * 120,
          totalInFlow: gb * 80,
          totalOutFlow: gb * 40,
        },
      ],
      "24h",
    );

    assert.deepEqual(series.slice(-2), [
      { time: "10:00", down: 1, up: 2 },
      { time: "11:00", down: 0.5, up: 0.25 },
    ]);
  });

  it("keeps admin system cumulative traffic out of the authoritative trend bucket", () => {
    const systemTotals = { inFlow: gb * 600, outFlow: gb * 300, totalFlow: gb * 900 };
    const series = buildTrafficSeries(
      [
        {
          time: "23:00",
          flow: gb * 0.3,
          inFlow: gb * 0.2,
          outFlow: gb * 0.1,
          totalFlow: systemTotals.totalFlow,
        },
      ],
      "24h",
    );

    assert.equal(resolveTrafficTotalBytes(systemTotals), gb * 900);
    assert.equal(series.at(-1)?.down, 0.2);
    assert.equal(series.at(-1)?.up, 0.1);
  });

  it("resolves new traffic totals before legacy fallback fields", () => {
    assert.equal(resolveTrafficTotalBytes({ totalFlow: gb * 3 }, gb * 99), gb * 3);
    assert.equal(resolveTrafficTotalBytes({ totalFlow: 0 }, gb * 99), 0);
    assert.equal(resolveTrafficTotalBytes({ inFlow: gb, outFlow: gb * 2 }), gb * 3);
    assert.equal(resolveTrafficTotalBytes(undefined, gb * 4), gb * 4);
  });

  it("selects the nearest chart point for a hover position", () => {
    const point = pickTrafficHoverPoint(
      [
        { time: "00:00", down: 1, up: 2 },
        { time: "12:00", down: 3, up: 4 },
        { time: "23:00", down: 5, up: 6 },
      ],
      260,
      400,
    );

    assert.deepEqual(point, {
      index: 1,
      time: "12:00",
      down: 3,
      up: 4,
      total: 7,
      xPercent: 50,
      tooltipLeftPercent: 50,
    });
  });

  it("keeps the hover tooltip inside the chart edges", () => {
    const series = [
      { time: "00:00", down: 1, up: 2 },
      { time: "23:00", down: 3, up: 4 },
    ];

    assert.equal(pickTrafficHoverPoint(series, -20, 400)?.tooltipLeftPercent, 8);
    assert.equal(pickTrafficHoverPoint(series, 440, 400)?.tooltipLeftPercent, 92);
    assert.equal(pickTrafficHoverPoint([], 10, 400), null);
  });

  it("reports loading and empty states for the traffic trend", () => {
    const zeroSeries = [
      { time: "00:00", down: 0, up: 0 },
      { time: "01:00", down: 0, up: 0 },
    ];
    const activeSeries = [
      { time: "00:00", down: 0, up: 0 },
      { time: "01:00", down: 0.25, up: 0 },
    ];

    assert.equal(getTrafficTrendState(zeroSeries, true), "loading");
    assert.equal(getTrafficTrendState(zeroSeries, false), "empty");
    assert.equal(getTrafficTrendState(activeSeries, true), "ready");
    assert.equal(getTrafficTrendState(activeSeries, false), "ready");
  });

  it("exposes accurate range metadata", () => {
    assert.equal(trafficRangeMeta["24h"].description, "过去 24 小时流量增量（含当前小时，GB）");
    assert.equal(trafficRangeMeta["7d"].description, "过去 7 天每日流量增量（含今天，GB）");
    assert.equal(trafficRangeMeta["30d"].points, 30);
  });

  it("labels tunnel traffic with its concrete entry and exit nodes", () => {
    assert.equal(
      trafficTunnelLabel({
        tunnelId: 7,
        tunnelName: "主线路",
        type: 2,
        inNodeId: 1,
        inNodeName: "A",
        outNodeId: 2,
        outNodeName: "B",
      }),
      "主线路 · A → B",
    );
    assert.equal(
      trafficTunnelLabel({
        tunnelId: 8,
        tunnelName: "三节点",
        type: 2,
        inNodeId: 1,
        inNodeName: "A",
        relayNodeId: 2,
        relayNodeName: "B",
        outNodeId: 3,
        outNodeName: "C",
      }),
      "三节点 · A → B → C",
    );
    assert.equal(trafficTunnelLabel(null), "全部隧道");
  });

  it("normalizes the tunnel selector and explains unavailable legacy history", () => {
    assert.equal(trafficTunnelRequestId("all"), undefined);
    assert.equal(trafficTunnelRequestId("7"), 7);
    assert.equal(trafficTunnelRequestId("0"), undefined);
    assert.equal(trafficTrendEmptyDescription(true, true), "完成同步后会自动显示最新趋势。");
    assert.match(trafficTrendEmptyDescription(false, true), /升级前数据没有隧道维度/);
  });
});
