import type { ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
export function StatTile({
  label,
  value,
  unit,
  hint,
  href,
  loading,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  hint?: ReactNode;
  href?: string;
  loading?: boolean;
}) {
  const className = cn(
    "block min-w-0 rounded-md border border-hairline bg-canvas p-5",
    href && "transition-shadow hover:shadow-level-2",
  );
  const content = (
    <>
      <div className="text-caption text-mute">{label}</div>
      {loading ? (
        <Skeleton className="my-2 h-9 w-28" />
      ) : (
        <div className="mt-1 font-mono text-metric-lg tabular text-ink">
          {value}
          <span className="ml-1 text-caption text-mute">{unit}</span>
        </div>
      )}
      {hint && <div className="mt-1 text-caption text-mute">{hint}</div>}
    </>
  );
  return href ? (
    <a href={href} className={className}>
      {content}
    </a>
  ) : (
    <div className={className}>{content}</div>
  );
}
