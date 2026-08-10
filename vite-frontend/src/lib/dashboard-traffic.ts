export type TrafficRange = "24h" | "7d" | "30d";
export type TrafficTrendState = "loading" | "empty" | "ready";

export interface TrafficFlowRow {
  time?: string;
  flow?: number;
  inFlow?: number;
  outFlow?: number;
  // Legacy cumulative metadata. Trend buckets deliberately ignore these fields.
  totalFlow?: number;
  totalInFlow?: number;
  totalOutFlow?: number;
}

export interface TrafficPoint {
  time: string;
  down: number;
  up: number;
}

export interface TrafficHoverPoint extends TrafficPoint {
  index: number;
  total: number;
  xPercent: number;
  tooltipLeftPercent: number;
}

export interface TrafficTotals {
  inFlow?: number | string | null;
  outFlow?: number | string | null;
  totalFlow?: number | string | null;
}

export interface TrafficTunnelOption {
  tunnelId: number;
  tunnelName?: string;
  type?: number;
  inNodeId?: number;
  inNodeName?: string;
  outNodeId?: number;
  outNodeName?: string;
}

export function trafficTunnelLabel(option: TrafficTunnelOption | null | undefined) {
  if (!option) return "全部隧道";
  const name = String(option.tunnelName || "").trim();
  const inNode = String(option.inNodeName || "").trim();
  const outNode = String(option.outNodeName || "").trim();
  const isDistinctRoute =
    Number(option.type) === 2 ||
    (inNode && outNode && Number(option.inNodeId || 0) !== Number(option.outNodeId || 0));
  const route = isDistinctRoute && inNode && outNode ? `${inNode} → ${outNode}` : inNode || outNode;
  if (name && route && name !== route) return `${name} · ${route}`;
  return name || route || `隧道 #${Number(option.tunnelId || 0)}`;
}

export function trafficTunnelRequestId(value: string) {
  if (value === "all") return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

export function trafficTrendEmptyDescription(isLoading: boolean, tunnelSelected: boolean) {
  if (isLoading) return "完成同步后会自动显示最新趋势。";
  if (tunnelSelected) {
    return "该隧道尚无升级后的流量记录；升级前数据没有隧道维度，无法可靠拆分。";
  }
  return "节点产生流量后，当前小时会立即出现在趋势图中。";
}

function nonNegativeNumber(value: unknown) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

export function resolveTrafficTotalBytes(totals: TrafficTotals | null | undefined, fallback = 0) {
  if (!totals) return nonNegativeNumber(fallback);

  if (totals.totalFlow !== undefined && totals.totalFlow !== null) {
    const total = Number(totals.totalFlow);
    if (Number.isFinite(total)) return nonNegativeNumber(total);
  }

  if (
    (totals.inFlow !== undefined && totals.inFlow !== null) ||
    (totals.outFlow !== undefined && totals.outFlow !== null)
  ) {
    return nonNegativeNumber(totals.inFlow) + nonNegativeNumber(totals.outFlow);
  }

  return nonNegativeNumber(fallback);
}

export const trafficRangeMeta: Record<
  TrafficRange,
  { label: string; points: number; description: string }
> = {
  "24h": { label: "24H", points: 24, description: "过去 24 小时流量增量（含当前小时，GB）" },
  "7d": { label: "7D", points: 7, description: "过去 7 天每日流量增量（含今天，GB）" },
  "30d": { label: "30D", points: 30, description: "过去 30 天每日流量增量（含今天，GB）" },
};

function bytesToGb(value: number) {
  if (!Number.isFinite(value) || value <= 0) return 0;
  return value / 1024 / 1024 / 1024;
}

function roundGb(value: number) {
  return Number(value.toFixed(3));
}

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}

function emptySeries(points: number, labelForIndex: (index: number) => string): TrafficPoint[] {
  return Array.from({ length: points }).map((_, index) => ({
    time: labelForIndex(index),
    down: 0,
    up: 0,
  }));
}

function hasDirectionalIncrement(row: TrafficFlowRow) {
  return Number(row?.inFlow || 0) > 0 || Number(row?.outFlow || 0) > 0;
}

function bucketDownBytes(row: TrafficFlowRow) {
  return Number(hasDirectionalIncrement(row) ? row?.inFlow || 0 : row?.flow || 0);
}

function bucketUpBytes(row: TrafficFlowRow) {
  return Number(hasDirectionalIncrement(row) ? row?.outFlow || 0 : 0);
}

function toPoint(row: TrafficFlowRow): TrafficPoint {
  return {
    time: String(row?.time || ""),
    down: roundGb(bytesToGb(bucketDownBytes(row))),
    up: roundGb(bytesToGb(bucketUpBytes(row))),
  };
}

function looksHourly(rows: TrafficFlowRow[]) {
  return rows.every((row) => /^\d{1,2}:\d{2}$/.test(String(row?.time || "")));
}

export function buildTrafficSeries(
  rows: TrafficFlowRow[] = [],
  range: TrafficRange = "24h",
): TrafficPoint[] {
  if (range === "24h") {
    const chronological = rows.slice().reverse().map(toPoint).slice(-24);
    const padding = emptySeries(
      24 - chronological.length,
      (index) => `${String(index).padStart(2, "0")}:00`,
    );
    return [...padding, ...chronological].slice(-24);
  }

  const meta = trafficRangeMeta[range];
  if (rows.length > 0 && !looksHourly(rows)) {
    const bucketed = rows.map(toPoint).slice(-meta.points);
    const padding = emptySeries(meta.points - bucketed.length, (index) => {
      const remaining = meta.points - bucketed.length - index;
      return `${remaining}D前`;
    });
    return [...padding, ...bucketed].slice(-meta.points);
  }

  const series = emptySeries(meta.points, (index) => {
    const remaining = meta.points - index - 1;
    if (remaining === 0) return "今天";
    return `${remaining}D前`;
  });

  const total = rows.reduce(
    (sum, row) => ({
      down: sum.down + bytesToGb(bucketDownBytes(row)),
      up: sum.up + bytesToGb(bucketUpBytes(row)),
    }),
    { down: 0, up: 0 },
  );

  series[series.length - 1] = {
    ...series[series.length - 1],
    down: roundGb(total.down),
    up: roundGb(total.up),
  };
  return series;
}

export function pickTrafficHoverPoint(
  data: TrafficPoint[] = [],
  pointerX: number,
  chartWidth: number,
): TrafficHoverPoint | null {
  if (!Array.isArray(data) || data.length === 0) return null;
  if (!Number.isFinite(pointerX) || !Number.isFinite(chartWidth) || chartWidth <= 0) return null;

  const lastIndex = data.length - 1;
  const clampedX = clamp(pointerX, 0, chartWidth);
  const index =
    lastIndex === 0 ? 0 : clamp(Math.round((clampedX / chartWidth) * lastIndex), 0, lastIndex);
  const point = data[index];
  const xPercent = lastIndex === 0 ? 0 : roundGb((index / lastIndex) * 100);
  const down = roundGb(Number(point?.down || 0));
  const up = roundGb(Number(point?.up || 0));

  return {
    index,
    time: String(point?.time || ""),
    down,
    up,
    total: roundGb(down + up),
    xPercent,
    tooltipLeftPercent: clamp(xPercent, 8, 92),
  };
}

export function getTrafficTrendState(
  data: TrafficPoint[] = [],
  isLoading = false,
): TrafficTrendState {
  const hasUsage = data.some((point) => Number(point?.down || 0) > 0 || Number(point?.up || 0) > 0);
  if (hasUsage) return "ready";
  return isLoading ? "loading" : "empty";
}
