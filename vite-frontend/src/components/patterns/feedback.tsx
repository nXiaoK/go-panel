import type { ReactNode } from "react";
import { AlertCircle, Inbox } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
export function EmptyState({
  title = "暂无数据",
  description = "添加第一条记录以开始使用",
  action,
  filtered = false,
  onClearFilters,
}: {
  title?: string;
  description?: ReactNode;
  action?: ReactNode;
  filtered?: boolean;
  onClearFilters?: () => void;
}) {
  return (
    <div className="flex min-h-48 flex-col items-center justify-center gap-3 px-5 py-8 text-center">
      <Inbox aria-hidden="true" className="size-5 text-mute" />
      <h3 className="text-body-strong text-ink">{filtered ? "没有匹配的结果" : title}</h3>
      <div className="text-sm leading-5 text-mute">
        {filtered ? "试试其他搜索词或筛选条件" : description}
      </div>
      {filtered && onClearFilters ? (
        <Button type="button" variant="outline" onClick={onClearFilters}>
          清除筛选
        </Button>
      ) : (
        action
      )}
    </div>
  );
}
export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-status-critical bg-status-critical-bg p-4"
    >
      <div className="flex min-w-0 items-start gap-2">
        <AlertCircle aria-hidden="true" className="size-4 shrink-0 text-status-critical" />
        <div>
          <p className="text-body-strong text-status-critical-text">数据加载失败</p>
          <p className="break-words text-caption text-status-critical-text">
            {error instanceof Error
              ? error.message
              : typeof error === "string"
                ? error
                : "请求失败，请稍后重试"}
          </p>
        </div>
      </div>
      {onRetry && (
        <Button type="button" variant="outline" onClick={onRetry}>
          重试
        </Button>
      )}
    </div>
  );
}
export function LoadingRows({ rows = 5 }: { rows?: number }) {
  return (
    <div role="status" aria-label="正在加载" className="space-y-3 p-4">
      <span className="sr-only">正在加载</span>
      {Array.from({ length: rows }, (_, index) => (
        <Skeleton key={index} className="h-8 w-full" />
      ))}
    </div>
  );
}
