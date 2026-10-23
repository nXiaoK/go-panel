import { Meter } from "@/components/patterns/meter";
import { formatBytes } from "@/lib/format";
/** 用户和授权的零配额由后端视为用尽，不能沿用通用 Meter 的不限流量文案。 */
export function UserMeter({
  row,
  label,
}: {
  row: { flow: number; inFlow?: number; outFlow?: number };
  label: string;
}) {
  const used = (row.inFlow ?? 0) + (row.outFlow ?? 0);
  return row.flow > 0 ? (
    <Meter value={used} max={row.flow * 1024 ** 3} formatValue={formatBytes} label={label} />
  ) : (
    <div aria-label={label} className="text-right font-mono text-caption tabular text-body">
      {formatBytes(used)} / 0 GB <span className="text-status-critical-text">已用尽</span>
    </div>
  );
}
