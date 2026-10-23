import { AlertCircle, AlertTriangle, ArrowUpCircle, Clock } from "lucide-react";
import type { StatusDescriptor } from "@/lib/status";
import type { Forward, ForwardTunnelOption, Node, PackagePermission, User } from "@/lib/types";
import { expiryStatus } from "@/lib/status";

export type AttentionItem = StatusDescriptor & {
  id: string;
  kind: "offline" | "sync" | "forward" | "expiry" | "quota" | "upgrade";
  href: string;
};
export function nodeOnline(node: Node, connections: ReadonlyMap<number, string>) {
  const connection = connections.get(node.id);
  return connection ? connection === "online" : node.status === 1;
}
export function sortDashboardNodes(nodes: Node[], connections: ReadonlyMap<number, string>) {
  const priority = (node: Node) =>
    !nodeOnline(node, connections)
      ? 0
      : node.syncState === "failed"
        ? 1
        : node.upgradeAvailable
          ? 2
          : 3;
  return [...nodes].sort((a, b) => priority(a) - priority(b) || a.id - b.id);
}
/** 只派生已加载资源的真实状态；不把创建时间伪装成操作事件。 */
export function buildAttentionItems({
  nodes = [],
  forwards = [],
  users = [],
  connections = new Map(),
  now = Date.now(),
}: {
  nodes?: Node[];
  forwards?: Pick<Forward, "id" | "name" | "status">[];
  users?: User[];
  connections?: ReadonlyMap<number, string>;
  now?: number;
}): AttentionItem[] {
  const orderedNodes = [...nodes].sort((a, b) => a.id - b.id);
  const offline: AttentionItem[] = orderedNodes
    .filter((node) => !nodeOnline(node, connections))
    .map((node) => ({
      id: `offline-${node.id}`,
      kind: "offline",
      tone: "critical",
      label: `${node.name} 离线`,
      href: `/node?focus=${node.id}`,
    }));
  const failed: AttentionItem[] = orderedNodes
    .filter((node) => node.syncState === "failed")
    .map((node) => ({
      id: `sync-${node.id}`,
      kind: "sync",
      tone: "critical",
      icon: AlertCircle,
      label: `${node.name} 同步失败`,
      href: `/node?focus=${node.id}`,
    }));
  const broken: AttentionItem[] = [...forwards]
    .filter((forward) => forward.status === -1)
    .sort((a, b) => a.id - b.id)
    .map((forward) => ({
      id: `forward-${forward.id}`,
      kind: "forward",
      tone: "critical",
      icon: AlertCircle,
      label: `转发 ${forward.name} 异常`,
      href: `/forward?focus=${forward.id}`,
    }));
  const expiring: AttentionItem[] = [...users]
    .filter((user) => user.expTime != null && user.expTime <= now + 7 * 86400000)
    .sort((a, b) => a.expTime! - b.expTime! || a.id - b.id)
    .map((user) => ({
      ...expiryStatus(user.expTime, now),
      id: `expiry-${user.id}`,
      kind: "expiry",
      icon: Clock,
      label: `用户 ${user.user} ${expiryStatus(user.expTime, now).label}`,
      href: `/user?focus=${user.id}`,
    }));
  const used = (user: User) =>
    Math.max(0, Number(user.inFlow || 0)) + Math.max(0, Number(user.outFlow || 0));
  // 管理员不受用户配额限制；普通用户的 0 GB 是已用尽，不能显示为不限。
  const ratio = (user: User) =>
    user.flow > 0 ? used(user) / (user.flow * 1024 ** 3) : user.flow === 0 ? 1 : 0;
  const quotas: AttentionItem[] = [...users]
    .filter((user) => user.roleId !== 0 && ratio(user) >= 0.9)
    .sort((a, b) => ratio(b) - ratio(a) || a.id - b.id)
    .map((user) => ({
      id: `quota-${user.id}`,
      kind: "quota",
      tone: ratio(user) >= 1 ? "critical" : ratio(user) >= 0.95 ? "serious" : "warning",
      icon: AlertTriangle,
      label: `用户 ${user.user} ${ratio(user) >= 1 ? "流量已用尽" : `流量已用 ${Math.round(ratio(user) * 100)}%`}`,
      href: `/user?focus=${user.id}`,
    }));
  const upgrades = orderedNodes.filter((node) => node.upgradeAvailable);
  const upgrade: AttentionItem[] = upgrades.length
    ? [
        {
          id: "upgrade",
          kind: "upgrade",
          tone: "warning",
          icon: ArrowUpCircle,
          label: `${upgrades.length} 个节点可升级`,
          href: `/node?focus=${upgrades[0].id}`,
        },
      ]
    : [];
  return [...offline, ...failed, ...broken, ...expiring, ...quotas, ...upgrade];
}
export type MyTunnel = {
  id: number;
  name: string;
  inIp: string;
  ports: string;
  permission?: PackagePermission;
  unavailableReason?: string;
};
/** 两个普通用户接口按真实 tunnelId 合并，保留失效授权并明确显示不可用原因。 */
export function mergeMyTunnels(
  permissions: PackagePermission[],
  options: ForwardTunnelOption[],
): MyTunnel[] {
  const byId = new Map(options.map((option) => [option.id, option]));
  const ids = new Set([
    ...permissions.map((permission) => permission.tunnelId),
    ...options.map((option) => option.id),
  ]);
  return [...ids].map((id) => {
    const option = byId.get(id);
    const permission = permissions.find((row) => row.tunnelId === id);
    const portSta = option && "inNodePortSta" in option ? option.inNodePortSta : null;
    const portEnd = option && "inNodePortEnd" in option ? option.inNodePortEnd : null;
    return {
      id,
      name: option?.name || permission?.tunnelName || `隧道 #${id}`,
      inIp: option?.inIp || "—",
      ports: portSta != null && portEnd != null ? `${portSta}–${portEnd}` : "—",
      permission,
      unavailableReason:
        option && "available" in option
          ? option.available
            ? undefined
            : option.unavailableReason || "授权不可用"
          : "授权不可用",
    };
  });
}
