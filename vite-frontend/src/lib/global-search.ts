export type GlobalSearchKind = "tunnel" | "node" | "user" | "forward";

export interface GlobalSearchItem {
  id: string;
  kind: GlobalSearchKind;
  title: string;
  subtitle: string;
  href: "/tunnel" | "/node" | "/user" | "/forward";
  keywords: string[];
}

type SearchRecord = Record<string, unknown>;

export interface GlobalSearchLists {
  tunnels?: SearchRecord[];
  nodes?: SearchRecord[];
  users?: SearchRecord[];
  forwards?: SearchRecord[];
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

function statusLabel(value: unknown) {
  return Number(value ?? 1) === 1 ? "启用" : "停用";
}

export function buildGlobalSearchItems(lists: GlobalSearchLists): GlobalSearchItem[] {
  const tunnels = (lists.tunnels || []).map((t) => ({
    id: `tunnel-${t.id}`,
    kind: "tunnel" as const,
    title: String(t.name || `隧道 #${t.id}`),
    subtitle: compact([
      `#${t.id}`,
      t.type === 2 ? "隧道转发" : "端口转发",
      String(t.protocol || "tcp").toUpperCase(),
      statusLabel(t.status),
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
    kind: "node" as const,
    title: String(n.name || `节点 #${n.id}`),
    subtitle: compact([
      `#${n.id}`,
      n.forwardMode || "gost",
      n.serverIp || n.ip,
      Number(n.status) === 1 ? "在线" : "离线",
    ]).join(" · "),
    href: "/node" as const,
    keywords: compact([n.id, n.name, n.forwardMode, n.serverIp, n.serverIpv6, n.ip, n.version]),
  }));

  const users = (lists.users || []).map((u) => ({
    id: `user-${u.id}`,
    kind: "user" as const,
    title: String(u.user || u.name || `用户 #${u.id}`),
    subtitle: compact([
      u.name,
      `#${u.id}`,
      statusLabel(u.status),
      Number(u.roleId ?? u.role_id) === 0 ? "管理员" : "用户",
    ]).join(" · "),
    href: "/user" as const,
    keywords: compact([u.id, u.user, u.name, u.roleId]),
  }));

  const forwards = (lists.forwards || []).map((f) => ({
    id: `forward-${f.id}`,
    kind: "forward" as const,
    title: String(f.name || `转发 #${f.id}`),
    subtitle: compact([
      `#${f.id}`,
      f.tunnelName,
      f.inPort,
      f.remoteAddr,
      statusLabel(f.status),
    ]).join(" · "),
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
