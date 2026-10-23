import type { ForwardExitMember, ForwardExitMode, ForwardTunnelOption, Tunnel } from "@/lib/types";

export function isTunnelForward(tunnel?: Pick<Tunnel, "type"> | null) {
  return tunnel?.type === 2;
}

export function defaultExitMembers(tunnel?: ForwardTunnelOption | null): ForwardExitMember[] {
  const outNodeId =
    tunnel && "inNodeId" in tunnel ? tunnel.outNodeId || tunnel.exitNodeId : undefined;
  return outNodeId ? [{ outNodeId, active: true, weight: 1 }] : [];
}

export function ensureOneActive(members: ForwardExitMember[]) {
  if (members.some((m) => m.active)) {
    let used = false;
    return members.map((m) => {
      if (!m.active) return m;
      if (used) return { ...m, active: false };
      used = true;
      return m;
    });
  }
  return members.map((m, index) => ({ ...m, active: index === 0 }));
}

export function normalizeExitMembersForMode(
  mode: ForwardExitMode,
  members: ForwardExitMember[],
  tunnel?: ForwardTunnelOption | null,
) {
  const selected = members.filter((m) => !!m.outNodeId);
  const base = selected.length ? selected : defaultExitMembers(tunnel);
  if (mode === "balance") {
    return base.map((m) => ({ ...m, active: true, weight: m.weight || 1 }));
  }
  if (mode === "manual") {
    return ensureOneActive(base).map((m) => ({ ...m, weight: m.weight || 1 }));
  }
  const active = base.find((m) => m.active) || base[0];
  return active ? [{ ...active, active: true, weight: active.weight || 1 }] : [];
}

export function exitModeLabel(mode?: string) {
  if (mode === "manual") return "手动";
  if (mode === "balance") return "自动";
  return "单出口";
}
