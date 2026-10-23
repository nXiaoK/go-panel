import type { Node, Tunnel, TunnelForm } from "@/lib/types";

export const emptyTunnelForm = (): TunnelForm => ({
  name: "",
  // 默认创建单节点端口转发；链路节点由用户明确选择，不修改已有隧道。
  type: 1,
  inNodeId: null,
  relayNodeId: null,
  outNodeId: null,
  // GOST 默认 TCP；全部 NFT 节点时提交强制 TCP+UDP，保持后端既有模式。
  protocol: "tcp",
  // 默认监听所有 IPv4 网卡；实际暴露范围还取决于节点防火墙和转发端口。
  tcpListenAddr: "0.0.0.0",
  udpListenAddr: "0.0.0.0",
  interfaceName: "",
  // 默认双向计费、1 倍流量；状态由服务端维护，表单不提供无效状态切换。
  flow: 2,
  trafficRatio: 1,
  status: 1,
});
export type TunnelErrors = Partial<Record<keyof TunnelForm, string>>;
export function nftModeFor(
  tunnel: Pick<TunnelForm, "type" | "inNodeId" | "relayNodeId" | "outNodeId">,
  nodes: ReadonlyMap<number, Node>,
) {
  return (
    nodes.get(tunnel.inNodeId ?? 0)?.forwardMode === "nftables" &&
    (tunnel.type === 1 ||
      (nodes.get(tunnel.outNodeId ?? 0)?.forwardMode === "nftables" &&
        (!tunnel.relayNodeId || nodes.get(tunnel.relayNodeId)?.forwardMode === "nftables")))
  );
}
export function validateTunnelForm(
  form: TunnelForm,
  nodes: ReadonlyMap<number, Node>,
): TunnelErrors {
  const errors: TunnelErrors = {};
  if (!form.name.trim()) errors.name = "请输入隧道名称";
  if (!form.inNodeId) errors.inNodeId = "请选择入口节点";
  if (form.type === 2 && !form.outNodeId) errors.outNodeId = "隧道转发需要选择出口节点";
  if (form.type === 2 && form.inNodeId && form.inNodeId === form.outNodeId)
    errors.outNodeId = "入口和出口节点不能重复";
  if (form.type === 2 && form.relayNodeId) {
    if (form.relayNodeId === form.inNodeId || form.relayNodeId === form.outNodeId)
      errors.relayNodeId = "入口、中继和出口节点不能重复";
    else if (!nftModeFor(form, nodes))
      errors.relayNodeId = "三节点串联要求入口、中继和出口均为 nftables 模式";
  }
  return errors;
}
export type NodeRole = "inNodeId" | "relayNodeId" | "outNodeId";
export function nodeDisabledReason(node: Node, role: NodeRole, form: TunnelForm) {
  if (
    ["inNodeId", "relayNodeId", "outNodeId"].some(
      (key) => key !== role && form[key as NodeRole] === node.id,
    )
  )
    return "与已选择的链路节点重复";
  if ((role === "relayNodeId" || form.relayNodeId) && node.forwardMode !== "nftables")
    return "三节点链路仅支持 nftables";
  return undefined;
}
export function canSpeedTest(tunnel?: Tunnel | null): tunnel is Tunnel {
  return Boolean(tunnel && tunnel.type === 2 && tunnel.inNodeId && tunnel.outNodeId);
}
// 方向值沿用后端路由映射：三节点只测相邻段，不引入跨中继直连。
export const speedDirections = (hasRelay: boolean) =>
  hasRelay
    ? [
        { value: "in-to-relay", label: "入口 → 中继" },
        { value: "relay-to-out", label: "中继 → 出口" },
        { value: "out-to-relay", label: "出口 → 中继" },
        { value: "relay-to-in", label: "中继 → 入口" },
      ]
    : [
        { value: "in-to-out", label: "入口 → 出口" },
        { value: "out-to-in", label: "出口 → 入口" },
      ];
export type SpeedInputs = { duration: string; parallel: string; port: string };
// 测速参数提交时严格校验：防止空值或异常并发；端口留空沿用服务端自动分配。
export function validateSpeedInputs(inputs: SpeedInputs) {
  const errors: Partial<Record<keyof SpeedInputs, string>> = {};
  const duration = Number(inputs.duration);
  const parallel = Number(inputs.parallel);
  const port = Number(inputs.port);
  if (!inputs.duration.trim() || !Number.isInteger(duration) || duration < 1 || duration > 120)
    errors.duration = "请输入 1–120 秒的整数时长";
  if (!inputs.parallel.trim() || !Number.isInteger(parallel) || parallel < 1 || parallel > 32)
    errors.parallel = "请输入 1–32 个并发连接";
  if (inputs.port.trim() && (!Number.isInteger(port) || port < 1 || port > 65535))
    errors.port = "请输入 1–65535 的端口，或留空自动分配";
  return errors;
}
