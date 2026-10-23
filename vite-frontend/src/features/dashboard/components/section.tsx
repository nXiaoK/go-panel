import type { ReactNode } from "react";
import { ArrowRight, AlertCircle } from "lucide-react";
import { StatusPill } from "@/components/patterns/status-pill";
import { Meter } from "@/components/patterns/meter";
import { ErrorState } from "@/components/patterns/feedback";
import { formatBytes } from "@/lib/format";

export function Section({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="min-w-0 rounded-md border border-hairline bg-canvas p-5">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-card-title text-ink">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}
export function ViewLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      className="inline-flex min-h-8 items-center gap-1 text-caption text-body underline-offset-4 hover:underline max-md:min-h-10"
    >
      {children}
      <ArrowRight aria-hidden="true" className="size-3" />
    </a>
  );
}
export function QueryTile({
  query,
  label,
  children,
}: {
  query: { error: Error | null; refetch: () => unknown };
  label: string;
  children: ReactNode;
}) {
  return query.error ? (
    <div className="min-w-0 space-y-2">
      <p className="text-caption text-mute">{label}</p>
      <ErrorState
        error={query.error}
        onRetry={() => {
          void query.refetch();
        }}
      />
    </div>
  ) : (
    children
  );
}
/** 普通用户的 0 GB 配额表示已用尽，不能沿用共享 Meter 的“不限流量”回退。 */
export function FlowQuota({ used, quota, label }: { used: number; quota: number; label: string }) {
  return quota === 0 ? (
    <div aria-label={label} className="space-y-2">
      <StatusPill tone="critical" label="0 GB 已用尽" icon={AlertCircle} />
      <p className="font-mono text-caption tabular text-body">{formatBytes(used)} / 0 GB</p>
    </div>
  ) : (
    <Meter value={used} max={quota * 1024 ** 3} label={label} formatValue={formatBytes} />
  );
}
