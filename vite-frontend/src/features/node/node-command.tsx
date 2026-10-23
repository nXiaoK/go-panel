import { useEffect, useState } from "react";
import { CodeBlock } from "@/components/patterns/code-block";
import { ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { Button } from "@/components/ui/button";
import { useNavigate } from "@tanstack/react-router";
import { getNodeInstallCommand, getNodeUninstallCommand } from "./api";
import { isMissingPanelAddressError } from "./model";
import { errorMessage } from "@/lib/api/client";
import type { Node } from "@/lib/types";
export type NodeCommandTarget = { node: Node; kind: "install" | "uninstall" };
export function NodeCommand({
  target,
  onClose,
}: {
  target: NodeCommandTarget | null;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const [result, setResult] = useState<{ code?: string; error?: string } | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    setResult(null);
    if (!target) return;
    const controller = new AbortController();
    const api = target.kind === "install" ? getNodeInstallCommand : getNodeUninstallCommand;
    void api(target.node.id, target.node.forwardMode || "gost", { signal: controller.signal }).then(
      (code) => {
        if (!controller.signal.aborted) setResult({ code });
      },
      (error: unknown) => {
        if (!controller.signal.aborted) setResult({ error: errorMessage(error) });
      },
    );
    return () => controller.abort();
  }, [target, attempt]);
  return (
    <EntitySheet
      open={Boolean(target)}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={`${target?.kind === "uninstall" ? "卸载命令" : "安装命令"} · ${target?.node.name ?? ""}`}
      description="适用于 Linux，需要 root 权限。请在对应节点服务器执行。"
      footer={({ requestClose }) => (
        <Button type="button" variant="outline" onClick={requestClose}>
          关闭命令
        </Button>
      )}
    >
      {!result ? (
        <LoadingRows />
      ) : result.error ? (
        <>
          <ErrorState
            error={
              isMissingPanelAddressError(result.error)
                ? "请先在系统配置中设置面板公网地址"
                : result.error
            }
            onRetry={() => setAttempt((value) => value + 1)}
          />
          {isMissingPanelAddressError(result.error) && (
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                onClose();
                void navigate({ to: "/config", search: { tab: "site" } as never });
              }}
            >
              去设置
            </Button>
          )}
        </>
      ) : (
        <CodeBlock code={result.code ?? ""} />
      )}
      <p className="text-caption text-status-warning">命令包含节点密钥，请勿公开分享</p>
    </EntitySheet>
  );
}
