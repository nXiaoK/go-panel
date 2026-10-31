import { useState } from "react";
import { CodeBlock } from "@/components/patterns/code-block";
import { EmptyState } from "@/components/patterns/feedback";
import { FormField } from "@/components/patterns/form-field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatDateTime } from "@/lib/format";
import type { SubscriptionNodeBackup } from "@/lib/types";

export function SubscriptionNodeRestore({ backups }: { backups: SubscriptionNodeBackup[] }) {
  // 默认不选择主机；仅展示当前列表中明确选中的恢复命令，刷新移除备份后不保留旧命令。
  const [sourceHost, setSourceHost] = useState("");
  const selected = backups.find((backup) => backup.sourceHost === sourceHost);

  return (
    <section className="min-w-0 space-y-4 border-t border-hairline pt-6">
      <div className="space-y-2">
        <h2 className="text-section-title text-ink">恢复已有节点</h2>
        <p className="text-sm text-body">
          VPS 重装后，选择原节点的备份，在原 VPS
          上执行恢复命令，恢复服务端配置并保留原协议节点和订阅绑定。 恢复前请核对原地址和主机标识。
        </p>
        <p className="text-caption text-mute">
          每个节点保留最新一次成功上传的备份，只有使用新版脚本完成备份的节点才能恢复。备份包含私钥和证书，单独保存在面板中；请保护面板及站点备份。
        </p>
      </div>
      {backups.length === 0 ? (
        <EmptyState
          title="暂无可恢复的节点备份"
          description="请在重装前，在原 VPS 上执行上方新版上报脚本，完成配置备份后刷新本页。仅有订阅连接参数无法完整恢复服务端配置。"
        />
      ) : (
        <>
          <FormField label="选择原节点备份" hint="同一地址可能对应不同主机，请核对主机标识。">
            <Select value={selected?.sourceHost ?? ""} onValueChange={setSourceHost}>
              <SelectTrigger className="min-w-0">
                <SelectValue placeholder="选择需要恢复的节点" />
              </SelectTrigger>
              <SelectContent className="max-w-(--radix-select-trigger-width)">
                {backups.map((backup) => (
                  <SelectItem key={backup.sourceHost} value={backup.sourceHost}>
                    <span className="break-all">
                      {backup.server} · {backup.protocols.join(" / ")} · {backup.sourceHost}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FormField>
          {selected && (
            <div className="min-w-0 space-y-3">
              <dl className="grid gap-2 text-caption text-body sm:grid-cols-2">
                <div>
                  <dt className="text-mute">原节点地址</dt>
                  <dd className="break-all font-mono">{selected.server}</dd>
                </div>
                <div>
                  <dt className="text-mute">备份时间</dt>
                  <dd>{formatDateTime(selected.updatedTime)}</dd>
                </div>
                <div>
                  <dt className="text-mute">主机标识</dt>
                  <dd className="break-all font-mono">{selected.sourceHost}</dd>
                </div>
                <div>
                  <dt className="text-mute">恢复协议</dt>
                  <dd className="break-words">{selected.protocols.join(" / ")}</dd>
                </div>
              </dl>
              <CodeBlock
                key={selected.sourceHost}
                code={selected.restoreCommand}
                label="复制恢复命令"
              />
            </div>
          )}
        </>
      )}
    </section>
  );
}
