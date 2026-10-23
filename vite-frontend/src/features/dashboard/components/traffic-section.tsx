import { useMemo, useState } from "react";
import { ErrorState } from "@/components/patterns/feedback";
import { SegmentedControl } from "@/components/patterns/segmented-control";
import { FilterSelect } from "@/components/patterns/filter-select";
import { formatBytes } from "@/lib/format";
import type { useDashboardSources } from "../queries";
import {
  buildTrafficSeries,
  resolveTrafficTotalBytes,
  trafficTunnelLabel,
  type TrafficRange,
} from "../traffic";
import { TrafficChart, TrafficLegend } from "./traffic-chart";
import { Section } from "./section";

export function TrafficSection({
  queries,
  range,
  setRange,
  tunnel,
  setTunnel,
}: {
  queries: ReturnType<typeof useDashboardSources>;
  range: TrafficRange;
  setRange: (range: TrafficRange) => void;
  tunnel: string;
  setTunnel: (id: string) => void;
}) {
  const [view, setView] = useState<"chart" | "table">("chart");
  const { traffic } = queries;
  const frameRange = ["24h", "7d", "30d"].includes(traffic.data?.trafficRange ?? "")
    ? (traffic.data!.trafficRange as TrafficRange)
    : range;
  const data = useMemo(
    () => buildTrafficSeries(traffic.data?.statisticsFlows ?? [], frameRange),
    [traffic.data?.statisticsFlows, frameRange],
  );
  const totals = traffic.data?.trafficTrendTotals;
  return (
    <Section
      title="流量趋势 · 范围内统计"
      action={
        <SegmentedControl
          label="流量展示方式"
          value={view}
          onChange={setView}
          options={[
            { value: "chart", label: "图表" },
            { value: "table", label: "表格" },
          ]}
        />
      }
    >
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <SegmentedControl
          label="流量时间范围"
          value={range}
          onChange={setRange}
          options={[
            { value: "24h", label: "24 小时" },
            { value: "7d", label: "7 天" },
            { value: "30d", label: "30 天" },
          ]}
        />
        <FilterSelect
          label="流量隧道"
          value={tunnel}
          onChange={setTunnel}
          options={[
            { value: "all", label: "全部隧道" },
            ...(traffic.data?.trafficTunnels ?? []).map((option) => ({
              value: String(option.tunnelId),
              label: trafficTunnelLabel(option),
            })),
          ]}
        />
      </div>
      {tunnel !== "all" && (
        <p className="mb-4 text-caption text-mute">
          仅含功能升级后记录；升级前数据没有隧道维度，无法可靠拆分
        </p>
      )}
      {traffic.error && (
        <div className="mb-4">
          <ErrorState
            error={traffic.error}
            onRetry={() => {
              void traffic.refetch();
            }}
          />
        </div>
      )}
      {(!traffic.error || traffic.data) && (
        <>
          <div className="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-3">
            {[
              { label: "区间合计", value: resolveTrafficTotalBytes(totals) },
              { label: "下行", value: totals?.inFlow ?? 0 },
              { label: "上行", value: totals?.outFlow ?? 0 },
            ].map((item) => (
              <div key={item.label}>
                <p className="text-caption text-mute">{item.label}</p>
                <p className="mt-1 font-mono text-metric-md tabular text-ink">
                  {traffic.isPending ? "—" : formatBytes(item.value)}
                </p>
              </div>
            ))}
          </div>
          <div className="mb-3">
            <TrafficLegend />
          </div>
          <TrafficChart
            data={data}
            range={frameRange}
            view={view}
            loading={traffic.isPending}
            refreshing={traffic.isFetching && Boolean(traffic.data)}
            tunnelSelected={tunnel !== "all"}
          />
        </>
      )}
    </Section>
  );
}
