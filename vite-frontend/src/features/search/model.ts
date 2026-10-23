import { roleLabel } from "@/lib/capabilities";
import type { Forward, Node, Tunnel, User } from "@/lib/types";
export type GlobalSearchKind = "tunnel" | "node" | "user" | "forward";

export interface GlobalSearchItem {
  id: string;
  kind: GlobalSearchKind;
  entityId: string;
  statusValue: number;
  title: string;
  subtitle: string;
  href: "/tunnel" | "/node" | "/user" | "/forward";
  keywords: string[];
}

type SearchRecord = Record<string, unknown>;

export interface GlobalSearchLists {
  tunnels?: Partial<Tunnel>[];
  nodes?: Partial<Node>[];
  users?: (Partial<User> & { role_id?: number })[];
  forwards?: Partial<Forward>[];
}

export function normalizeApiList(data: unknown): SearchRecord[] {
  if (Array.isArray(data)) {
    return data.filter((item): item is SearchRecord => !!item && typeof item === "object");
  }
  if (data && typeof data === "object") {
    const records = (data as { records?: unknown }).records;
    if (Array.isArray(records)) {
      return records.filter((item): item is SearchRecord => !!item && typeof item === "object");
    }
  }
  return [];
}

function compact(values: Array<string | number | null | undefined | unknown>) {
  return values.map((value) => String(value ?? "").trim()).filter(Boolean);
}

export function buildGlobalSearchItems(lists: GlobalSearchLists): GlobalSearchItem[] {
  const tunnels = (lists.tunnels || []).map((t) => ({
    id: `tunnel-${t.id}`,
    entityId: String(t.id),
    statusValue: t.status ?? 1,
    kind: "tunnel" as const,
    title: String(t.name || `隧道 #${t.id}`),
    subtitle: compact([
      `#${t.id}`,
      t.type === 2 ? "隧道转发" : "端口转发",
      String(t.protocol || "tcp").toUpperCase(),
    ]).join(" · "),
    href: "/tunnel" as const,
    keywords: compact([
      t.id,
      t.name,
      t.protocol,
      t.inIp,
      t.outIp,
      t.tcpListenAddr,
      t.udpListenAddr,
    ]),
  }));

  const nodes = (lists.nodes || []).map((n) => ({
    id: `node-${n.id}`,
    entityId: String(n.id),
    statusValue: n.status ?? 0,
    kind: "node" as const,
    title: String(n.name || `节点 #${n.id}`),
    subtitle: compact([`#${n.id}`, n.forwardMode || "gost", n.serverIp || n.ip]).join(" · "),
    href: "/node" as const,
    keywords: compact([n.id, n.name, n.forwardMode, n.serverIp, n.serverIpv6, n.ip, n.version]),
  }));

  const users = (lists.users || []).map((u) => ({
    id: `user-${u.id}`,
    entityId: String(u.id),
    statusValue: u.status ?? 1,
    kind: "user" as const,
    title: String(u.user || u.name || `用户 #${u.id}`),
    subtitle: compact([u.name, `#${u.id}`, roleLabel(Number(u.roleId ?? u.role_id))]).join(" · "),
    href: "/user" as const,
    keywords: compact([u.id, u.user, u.name, u.roleId]),
  }));

  const forwards = (lists.forwards || []).map((f) => ({
    id: `forward-${f.id}`,
    entityId: String(f.id),
    statusValue: f.status ?? 1,
    kind: "forward" as const,
    title: String(f.name || `转发 #${f.id}`),
    subtitle: compact([`#${f.id}`, f.tunnelName, f.inPort, f.remoteAddr]).join(" · "),
    href: "/forward" as const,
    keywords: compact([
      f.id,
      f.name,
      f.tunnelName,
      f.userName,
      f.inPort,
      f.remoteAddr,
      f.activeRemoteAddr,
    ]),
  }));

  return [...tunnels, ...nodes, ...users, ...forwards];
}

export function filterGlobalSearchItems(
  items: GlobalSearchItem[],
  query: string,
  limit = 8,
): GlobalSearchItem[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return items.slice(0, limit);

  const tokens = normalized.split(/\s+/).filter(Boolean);
  return items
    .filter((item) => {
      const haystack = [item.title, item.subtitle, item.kind, ...item.keywords]
        .join(" ")
        .toLowerCase();
      return tokens.every((token) => haystack.includes(token));
    })
    .slice(0, limit);
}
