import { useState } from "react";
import { AlertTriangle, Check, CircleSlash } from "lucide-react";
import { CodeBlock } from "@/components/patterns/code-block";
import { EmptyState } from "@/components/patterns/feedback";
import { FormField } from "@/components/patterns/form-field";
import { StatusPill } from "@/components/patterns/status-pill";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ReportedNodeRestore } from "@/lib/types";

// 缺少可靠来源的记录仍展示原因，用节点 ID 区分，避免同 IP 的历史节点被前端错误合并。
const targetKey = (target: ReportedNodeRestore) =>
  target.sourceHost
    ? `host:${target.sourceHost}`
    : `nodes:${target.nodes.map((node) => node.id).join(",")}:${target.server}`;

export function ReportedNodeRestoreSection({ targets }: { targets: ReportedNodeRestore[] }) {
  // 必须明确选择旧主机；记录刷新后消失时隐藏原命令，避免复制过期的恢复目标。
  const [selection, setSelection] = useState("");
  const selected = targets.find((target) => targetKey(target) === selection);
  const restorable = selected?.nodes.filter((node) => node.action !== "unsupported") ?? [];
  const regeneratesKeys = selected?.nodes.some((node) => node.action === "regenerate");

  return (
    <section className="min-w-0 space-y-4 border-t border-hairline pt-6">
      <div className="space-y-2">
        <h2 className="text-section-title text-ink">从旧上报记录恢复</h2>
        <p className="text-sm text-body">
          适用于已重装且没有完整备份的
          VPS。使用面板保留的旧上报记录重建支持的协议，保留原协议节点、订阅链接和绑定。
        </p>
        <p className="text-caption text-mute">
          SS、Snell 和 SOCKS5 沿用原端口及凭据；VLESS Reality 沿用端口和
          UUID，生成新的密钥对。已有完整备份的节点请使用上方入口。
        </p>
      </div>
      {targets.length === 0 ? (
        <EmptyState
          title="暂无可用于恢复的旧上报记录"
          description="已有完整备份的节点显示在上方；没有旧上报记录的节点需要重新安装协议。"
        />
      ) : (
        <>
          <FormField
            label="选择旧上报节点"
            hint="核对原地址、主机标识和各协议端口，再在重装后的原 VPS 上执行恢复命令。"
          >
            <Select value={selected ? targetKey(selected) : ""} onValueChange={setSelection}>
              <SelectTrigger className="min-w-0">
                <SelectValue placeholder="选择需要恢复的旧节点" />
              </SelectTrigger>
              <SelectContent className="max-w-(--radix-select-trigger-width)">
                {targets.map((target) => (
                  <SelectItem key={targetKey(target)} value={targetKey(target)}>
                    <span className="break-all">
                      {target.server} ·{" "}
                      {target.sourceHost || `来源标识缺失 #${target.nodes[0]?.id ?? ""}`} ·{" "}
                      {target.nodes.length} 个协议
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
                  <dt className="text-mute">主机标识</dt>
                  <dd className="break-all font-mono">
                    {selected.sourceHost || "来源标识缺失，无法确认原节点"}
                  </dd>
                </div>
              </dl>
              <p className="text-caption text-mute">
                可恢复 {restorable.length} / {selected.nodes.length} 个协议
                {restorable.length < selected.nodes.length && "，恢复时将跳过无法恢复的协议"}
              </p>
              <ul
                aria-label="旧上报协议恢复明细"
                className="divide-y divide-hairline rounded-md border border-hairline"
              >
                {selected.nodes.map((node) => (
                  <li key={node.id} className="space-y-2 p-4">
                    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
                      <h3 className="min-w-0 break-all text-body-strong text-ink">{node.name}</h3>
                      {node.action === "preserve" ? (
                        <StatusPill tone="neutral" label="沿用原凭据" icon={Check} />
                      ) : node.action === "regenerate" ? (
                        <StatusPill
                          tone="warning"
                          label="重新生成密钥 / 证书"
                          icon={AlertTriangle}
                        />
                      ) : (
                        <StatusPill tone="neutral" label="无法恢复" icon={CircleSlash} />
                      )}
                    </div>
                    <p className="font-mono text-caption text-body">
                      {node.protocol} · 原端口 {node.port}
                    </p>
                    <p className="break-words text-caption text-mute">{node.reason}</p>
                  </li>
                ))}
              </ul>
              {regeneratesKeys && (
                <div className="flex items-start gap-2 rounded-md bg-status-warning-bg p-4 text-status-warning">
                  <AlertTriangle aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
                  <div className="min-w-0 space-y-1">
                    <p className="text-body-strong">恢复后必须刷新订阅</p>
                    <p className="text-sm">
                      重新生成的密钥或证书会更新连接参数。Reality
                      的公钥会改变；请让客户端刷新原订阅，订阅链接和绑定保持不变。
                    </p>
                  </div>
                </div>
              )}
              {selected.restoreCommand && restorable.length > 0 ? (
                <CodeBlock
                  key={targetKey(selected)}
                  code={selected.restoreCommand}
                  label="复制旧记录恢复命令"
                />
              ) : (
                <p className="text-sm text-body">
                  此节点无法从旧上报记录自动恢复，请查看各协议说明。
                </p>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}
