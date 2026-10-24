import { useConfirm } from "@/hooks/use-confirm";
import type { PanelRollbackTarget } from "@/lib/types";
import { panelUpdateStore } from "./update-store";

// 回滚与恢复内置版本的影响说明：两者都会重启面板，但都不改动数据库内容。
export const ROLLBACK_IMPACT = "面板将重启约 1 分钟；节点转发不受影响；回滚后数据库保持当前内容";

/** 更新弹窗与“版本与更新”分区共用的操作入口；重试隔离版本和切换版本前必须二次确认。 */
export function useUpdateActions() {
  const confirm = useConfirm();
  return {
    start: () => panelUpdateStore.apply(false),
    async retryQuarantined() {
      const status = panelUpdateStore.getSnapshot().status;
      const version = status?.latestVersion;
      if (!version) return;
      const detail = status.softUpdate.detail?.trim();
      const accepted = await confirm({
        title: `仍要重试 ${version}？`,
        description: `${version} 曾试运行失败${detail ? `（${detail}）` : ""}。重试会再次切换到该版本，面板将重启约 1 分钟；再次失败时会自动回滚到当前版本`,
        confirmText: "仍要重试",
        tone: "danger",
      });
      if (accepted) await panelUpdateStore.apply(true);
    },
    async rollback(target: PanelRollbackTarget) {
      const runtime = panelUpdateStore.getSnapshot().status?.runtime;
      const version = target === "previous" ? runtime?.previousVersion : runtime?.builtinVersion;
      const accepted = await confirm({
        title:
          target === "previous"
            ? version
              ? `回滚到 ${version}？`
              : "回滚到上一版本？"
            : `恢复镜像内置版本${version ? ` ${version}` : ""}？`,
        description: ROLLBACK_IMPACT,
        confirmText: target === "previous" ? "确认回滚" : "恢复内置版本",
      });
      if (accepted) await panelUpdateStore.rollback(target);
    },
  };
}
