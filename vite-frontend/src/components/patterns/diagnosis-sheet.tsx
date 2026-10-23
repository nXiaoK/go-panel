import type { DiagnosisResult, NodeStatusCheck } from "@/lib/types";
import { diagnosisSummary, internetProbeNotice } from "@/lib/diagnosis";
import { diagnosisItemStatus, nodeStatus, syncStatus, upgradeStatus } from "@/lib/status";
import { EntitySheet } from "./entity-sheet";
import { StatusPill } from "./status-pill";
import { EmptyState, ErrorState, LoadingRows } from "./feedback";
import { Button } from "@/components/ui/button";
export function DiagnosisSheet({
  open,
  onOpenChange,
  title = "诊断结果",
  data,
  loading,
  error,
  onRetry,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title?: string;
  data?: DiagnosisResult | NodeStatusCheck | null;
  loading?: boolean;
  error?: unknown;
  onRetry?: () => void;
}) {
  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description="检查结果反映本次检测时的状态"
      footer={({ requestClose }) => (
        <>
          {onRetry && (
            <Button type="button" loading={loading} onClick={onRetry}>
              重新诊断
            </Button>
          )}
          <Button type="button" variant="outline" onClick={requestClose}>
            关闭诊断
          </Button>
        </>
      )}
    >
      {loading ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : !data ? (
        <EmptyState title="暂无诊断结果" description="运行诊断后查看结果" />
      ) : "results" in data ? (
        <>
          <p className="text-body-strong">{diagnosisSummary(data.results)}</p>
          {data.results.some((item) => item.advisory) && (
            <p className="text-caption text-mute">{internetProbeNotice}</p>
          )}
          <ol className="space-y-3">
            {data.results.map((item, index) => (
              <li
                key={`${item.nodeId}-${index}`}
                className="space-y-2 rounded-md border border-hairline p-4"
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="text-body-strong">{item.description}</h3>
                  <StatusPill {...diagnosisItemStatus(item)} />
                </div>
                <p className="text-caption text-body">
                  {item.nodeName} ·{" "}
                  <span className="font-mono">
                    {item.targetIp}
                    {item.targetPort ? `:${item.targetPort}` : ""}
                  </span>
                </p>
                {item.message && (
                  <p className="break-words text-caption text-mute">{item.message}</p>
                )}
                {item.averageTime != null && (
                  <p className="font-mono text-caption text-body">
                    平均延迟{" "}
                    {Number.isFinite(item.averageTime) && item.averageTime >= 0
                      ? `${item.averageTime} ms`
                      : "—"}{" "}
                    · 丢包 {item.packetLoss ?? "—"}%
                  </p>
                )}
              </li>
            ))}
          </ol>
        </>
      ) : (
        <NodeDiagnosis data={data} />
      )}
    </EntitySheet>
  );
}
function NodeDiagnosis({ data }: { data: NodeStatusCheck }) {
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <StatusPill {...nodeStatus(data)} />
        <StatusPill {...syncStatus(data.syncState)} />
        {data.upgradeAvailable && <StatusPill {...upgradeStatus(true)} />}
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-3 text-body">
        <dt className="text-mute">节点</dt>
        <dd>
          {data.name} <span className="font-mono">#{data.nodeId}</span>
        </dd>
        <dt className="text-mute">连接</dt>
        <dd>{data.connected ? "已连接" : "未连接"}</dd>
        <dt className="text-mute">响应</dt>
        <dd>{data.responsive ? "响应正常" : "无响应"}</dd>
        <dt className="text-mute">往返延迟</dt>
        <dd className="font-mono">{data.responsive ? `${data.roundTripMs} ms` : "—"}</dd>
        <dt className="text-mute">转发模式</dt>
        <dd>{data.forwardMode}</dd>
        <dt className="text-mute">数据库状态</dt>
        <dd>{data.dbStatus === 1 ? "在线" : "离线"}</dd>
        <dt className="text-mute">当前版本</dt>
        <dd className="break-all font-mono">{data.version || "—"}</dd>
        <dt className="text-mute">最新版本</dt>
        <dd className="break-all font-mono">{data.latestVersion || "—"}</dd>
        <dt className="text-mute">上次同步</dt>
        <dd>
          {data.syncLastAttempt ? new Date(data.syncLastAttempt).toLocaleString("zh-CN") : "—"}
        </dd>
        <dt className="text-mute">检测时间</dt>
        <dd>{new Date(data.checkedAt).toLocaleString("zh-CN")}</dd>
      </dl>
      {data.syncLastError && (
        <p className="break-words text-caption text-status-critical-text">{data.syncLastError}</p>
      )}
      {data.message && <p className="break-words text-sm leading-5 text-body">{data.message}</p>}
    </div>
  );
}
