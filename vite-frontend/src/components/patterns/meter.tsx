import { cn } from "@/lib/utils";
export function Meter({
  value,
  max,
  label = "流量配额",
  formatValue = (n: number) => String(n),
}: {
  value: number | null | undefined;
  max: number | null | undefined;
  label?: string;
  formatValue?: (n: number) => string;
}) {
  const valid =
    value != null && max != null && Number.isFinite(value) && Number.isFinite(max) && max > 0;
  if (!valid)
    return (
      <div className="text-caption text-mute" aria-label={label}>
        {max != null && max <= 0 ? "不限流量" : "暂无数据"}
      </div>
    );
  const percent = Math.max(0, Math.min(100, (value / max) * 100));
  const tone =
    percent >= 100 ? "critical" : percent >= 95 ? "serious" : percent >= 80 ? "warning" : "good";
  return (
    <div className="space-y-2">
      <div
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={max}
        aria-valuenow={Math.min(max, Math.max(0, value))}
        aria-valuetext={`${formatValue(value)} / ${formatValue(max)}`}
        className="h-1.5 overflow-hidden rounded-pill bg-hairline-subtle"
      >
        <div
          data-tone={tone}
          className={cn(
            "h-full rounded-pill",
            {
              good: "bg-status-good",
              warning: "bg-status-warning",
              serious: "bg-status-serious",
              critical: "bg-status-critical",
            }[tone],
          )}
          style={{ width: `${percent}%` }}
        />
      </div>
      <div className="text-right font-mono text-caption tabular text-body">
        {formatValue(value)} / {formatValue(max)}
      </div>
    </div>
  );
}
