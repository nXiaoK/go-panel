export interface ForwardFormInput {
  name?: string;
  tunnelId?: number | null;
  inPort?: number | null;
  remoteAddr?: string;
  strategy?: string;
  targetMode?: string;
  activeRemoteAddr?: string;
  exitMode?: string;
  exitMembers?: Array<{ outNodeId?: number | null; active?: boolean }>;
  tunnelType?: number;
}

export function normalizeTargetAddressInput(value: string) {
  const input = value.trim();
  if (!input) return "";
  if (!input.includes("://")) return input;

  try {
    const parsed = new URL(input);
    return parsed.host || input;
  } catch {
    return input;
  }
}

export function splitTargetAddresses(remoteAddr: string): string[] {
  return remoteAddr
    .split(",")
    .map((s) => normalizeTargetAddressInput(s))
    .filter(Boolean);
}

export function joinTargetAddresses(targets: string[]): string {
  return targets
    .map((s) => normalizeTargetAddressInput(s))
    .filter(Boolean)
    .join(",");
}

export function targetAddressFields(remoteAddr: string): string[] {
  const targets = splitTargetAddresses(remoteAddr);
  return targets.length ? targets : [""];
}

export function validateForwardForm(form: ForwardFormInput) {
  if (!form.name?.trim()) return "请输入转发名称";
  if (!form.tunnelId) return "请选择隧道";
  if (
    form.inPort !== null &&
    form.inPort !== undefined &&
    (!Number.isInteger(form.inPort) || form.inPort < 1 || form.inPort > 65535)
  ) {
    return "入口端口范围应为 1-65535";
  }
  const targets = splitTargetAddresses(form.remoteAddr || "");
  if (targets.length === 0) return "请输入目标地址";
  if (form.targetMode === "manual") {
    if (!form.activeRemoteAddr?.trim()) return "手动目标需要选择当前目标地址";
    if (!targets.includes(form.activeRemoteAddr.trim())) return "当前目标地址不在目标地址列表中";
  }
  if (form.tunnelType === 2) {
    const members = (form.exitMembers || []).filter((m) => !!m.outNodeId);
    if (form.exitMode === "manual") {
      if (members.length === 0) return "请至少选择一个出口节点";
      if (members.filter((m) => m.active).length !== 1) return "手动负载需要选择一个当前出口节点";
    }
    if (form.exitMode === "balance" && members.length < 2) {
      return "自动负载均衡至少需要两个出口节点";
    }
  }
  return "";
}
