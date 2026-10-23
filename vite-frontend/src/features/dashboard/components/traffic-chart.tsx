import { useEffect, useMemo, useRef, useState } from "react";
import { EmptyState } from "@/components/patterns/empty-state";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  getTrafficTrendState,
  pickTrafficHoverPoint,
  trafficChartGeometry,
  trafficTrendEmptyDescription,
  type TrafficPoint,
  type TrafficRange,
} from "../traffic";

const bytes = (gb: number) => formatBytes(gb * 1024 ** 3);
export function TrafficLegend() {
  return (
    <div className="flex flex-wrap gap-x-5 gap-y-2 text-caption text-body" aria-label="流量图例">
      <span className="inline-flex items-center gap-2">
        <span className="h-0.5 w-5 bg-chart-series-1" aria-hidden="true" />
        下行
      </span>
      <span className="inline-flex items-center gap-2">
        <span className="h-0.5 w-5 bg-chart-series-2" aria-hidden="true" />
        上行
      </span>
    </div>
  );
}
export function TrafficChart({
  data,
  range,
  view,
  loading = false,
  refreshing = false,
  tunnelSelected = false,
}: {
  data: TrafficPoint[];
  range: TrafficRange;
  view: "chart" | "table";
  loading?: boolean;
  refreshing?: boolean;
  tunnelSelected?: boolean;
}) {
  const container = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 280 });
  const [active, setActive] = useState<number | null>(null);
  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const update = (width: number, height: number) =>
      setSize((previous) =>
        width === previous.width && height === previous.height ? previous : { width, height },
      );
    const rect = element.getBoundingClientRect();
    if (rect.width) update(rect.width, rect.height || 280);
    const observer = new ResizeObserver(([entry]) => {
      if (entry?.contentRect.width)
        update(entry.contentRect.width, entry.contentRect.height || 280);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [view]);
  const geometry = useMemo(() => trafficChartGeometry(data, size.width, size.height), [data, size]);
  const point = active == null ? null : data[Math.min(active, data.length - 1)];
  const pointIndex = active == null ? 0 : Math.min(active, data.length - 1);
  const total = data.reduce((sum, row) => sum + row.down + row.up, 0);
  const rangeName = range === "24h" ? "过去 24 小时" : range === "7d" ? "过去 7 天" : "过去 30 天";
  const state = getTrafficTrendState(data, loading);
  return (
    <div
      className={cn("min-w-0", refreshing && "opacity-60")}
      aria-busy={refreshing || loading}
      data-testid="traffic-frame"
    >
      {state === "loading" ? (
        <Skeleton className="h-[280px] w-full" aria-label="流量趋势加载中" />
      ) : state === "empty" ? (
        <EmptyState
          title="暂无流量记录"
          description={trafficTrendEmptyDescription(false, tunnelSelected)}
        />
      ) : view === "table" ? (
        <Table aria-label="流量明细" containerClassName="max-h-[360px]">
          <TableHeader>
            <TableRow>
              <TableHead>时间</TableHead>
              {["下行", "上行", "合计"].map((label) => (
                <TableHead key={label} className="text-right">
                  {label}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.map((row, index) => (
              <TableRow key={index}>
                <TableCell className="whitespace-nowrap font-mono">{row.time}</TableCell>
                {[row.down, row.up, row.down + row.up].map((value, i) => (
                  <TableCell key={i} className="text-right font-mono tabular">
                    {bytes(value)}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
      <div
        ref={container}
        className={cn(
          "relative h-[280px] w-full min-w-0",
          (view !== "chart" || state !== "ready") && "hidden",
        )}
      >
        {size.width > 0 && state === "ready" && (
          <div
            role="img"
            tabIndex={0}
            aria-label={`${rangeName}流量趋势，合计 ${bytes(total)}，左右方向键选择时间点，Home 和 End 跳到首尾`}
            className="h-full w-full rounded-sm focus-visible:ring-2 focus-visible:ring-primary-soft"
            onPointerMove={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              const hover = pickTrafficHoverPoint(
                data,
                event.clientX - rect.left - geometry.left,
                geometry.plotWidth,
              );
              if (hover) setActive(hover.index);
            }}
            onPointerLeave={(event) => {
              if (document.activeElement !== event.currentTarget) setActive(null);
            }}
            onFocus={() => setActive((previous) => previous ?? 0)}
            onBlur={() => setActive(null)}
            onKeyDown={(event) => {
              if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
              event.preventDefault();
              setActive((previous) =>
                event.key === "Home"
                  ? 0
                  : event.key === "End"
                    ? data.length - 1
                    : Math.max(
                        0,
                        Math.min(
                          data.length - 1,
                          (previous ?? 0) + (event.key === "ArrowRight" ? 1 : -1),
                        ),
                      ),
              );
            }}
          >
            <svg width={size.width} height={size.height} aria-hidden="true" className="block">
              {geometry.ticks.map((tick) => (
                <g key={tick}>
                  <line
                    x1={geometry.left}
                    x2={geometry.left + geometry.plotWidth}
                    y1={geometry.y(tick)}
                    y2={geometry.y(tick)}
                    stroke={tick === 0 ? "var(--chart-axis)" : "var(--chart-grid)"}
                  />
                  <text
                    x={geometry.left - 10}
                    y={geometry.y(tick) + 4}
                    textAnchor="end"
                    fill="var(--mute)"
                    className="font-mono text-caption"
                  >
                    {tick === 0 ? "0" : bytes(tick)}
                  </text>
                </g>
              ))}
              {geometry.labelIndexes.map((index) => (
                <text
                  key={index}
                  x={geometry.x(index)}
                  y={size.height - 10}
                  textAnchor={index === 0 ? "start" : index === data.length - 1 ? "end" : "middle"}
                  fill="var(--mute)"
                  className="font-mono text-caption"
                >
                  {data[index].time}
                </text>
              ))}
              {(["down", "up"] as const).map((key, index) => (
                <g key={key}>
                  <path
                    d={geometry.area(key)}
                    fill={`var(--chart-series-${index + 1})`}
                    fillOpacity={0.1}
                  />
                  <path
                    data-series={key}
                    d={geometry.path(key)}
                    fill="none"
                    stroke={`var(--chart-series-${index + 1})`}
                    strokeWidth={2}
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </g>
              ))}
              {point && (
                <g>
                  <line
                    x1={geometry.x(pointIndex)}
                    x2={geometry.x(pointIndex)}
                    y1={geometry.top}
                    y2={geometry.top + geometry.plotHeight}
                    stroke="var(--border-control)"
                  />
                  {(["down", "up"] as const).map((key, index) => (
                    <circle
                      key={key}
                      cx={geometry.x(pointIndex)}
                      cy={geometry.y(point[key])}
                      r={4}
                      fill={`var(--chart-series-${index + 1})`}
                      stroke="var(--canvas)"
                      strokeWidth={2}
                    />
                  ))}
                </g>
              )}
            </svg>
            {point && (
              <div
                role="status"
                aria-live="polite"
                className="pointer-events-none absolute top-2 w-44 rounded-md border border-hairline bg-canvas-soft p-3 shadow-popover"
                style={{
                  left: Math.max(0, Math.min(size.width - 176, geometry.x(pointIndex) - 88)),
                }}
              >
                <p className="mb-2 font-mono text-caption text-mute">{point.time}</p>
                {(["down", "up"] as const).map((key, index) => (
                  <div
                    key={key}
                    className="flex items-center justify-between gap-3 text-caption text-body"
                  >
                    <span className="inline-flex items-center gap-1.5">
                      <span
                        aria-hidden="true"
                        className={cn(
                          "h-0.5 w-3",
                          index === 0 ? "bg-chart-series-1" : "bg-chart-series-2",
                        )}
                      />
                      {key === "down" ? "下行" : "上行"}
                    </span>
                    <strong className="font-mono text-body-strong tabular text-ink">
                      {bytes(point[key])}
                    </strong>
                  </div>
                ))}
                <div className="mt-2 flex justify-between border-t border-hairline pt-2 text-caption text-body">
                  <span>合计</span>
                  <strong className="font-mono text-body-strong tabular text-ink">
                    {bytes(point.down + point.up)}
                  </strong>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
