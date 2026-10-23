import { useMemo, useState } from "react";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { FormField } from "@/components/patterns/form-field";
import { FormSection } from "@/components/patterns/form-section";
import { ErrorState } from "@/components/patterns/feedback";
import { StatusPill } from "@/components/patterns/status-pill";
import type { FieldControl } from "@/components/ui/field-control";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Node, TunnelForm, TunnelWrite } from "@/lib/types";
import {
  nftModeFor,
  nodeDisabledReason,
  validateTunnelForm,
  type NodeRole,
  type TunnelErrors,
} from "../model";
import { PathChips } from "./path-chips";

const immutableHint = "现有链路不支持直接更换类型或节点，如需更换请新建隧道";
export function TunnelNodeSelect({
  role,
  form,
  nodes,
  disabled,
  onChange,
  ...control
}: Partial<FieldControl> & {
  role: NodeRole;
  form: TunnelForm;
  nodes: Node[];
  disabled?: boolean;
  onChange: (id: number | null) => void;
}) {
  return (
    <Select
      value={String(form[role] || "none")}
      onValueChange={(value) => onChange(value === "none" ? null : Number(value))}
      disabled={disabled}
    >
      <SelectTrigger {...control}>
        <SelectValue placeholder="选择节点" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="none">{role === "relayNodeId" ? "不使用中继" : "选择节点"}</SelectItem>
        {nodes.map((node) => {
          const reason = nodeDisabledReason(node, role, form);
          return (
            <SelectItem
              key={node.id}
              value={String(node.id)}
              disabled={Boolean(reason)}
              textValue={`${node.name} · ${node.forwardMode === "nftables" ? "NFT" : "GOST"} · ${node.status === 1 ? "在线" : "离线"}`}
            >
              <span className="flex flex-col gap-0.5">
                <span>
                  {node.name} ·{" "}
                  <span className="font-mono">
                    {node.forwardMode === "nftables" ? "NFT" : "GOST"}
                  </span>{" "}
                  · {node.status === 1 ? "● 在线" : "○ 离线"}
                </span>
                {reason && <span className="text-caption text-mute">{reason}</span>}
              </span>
            </SelectItem>
          );
        })}
      </SelectContent>
    </Select>
  );
}
export function TunnelFormSheet({
  open,
  onOpenChange,
  initial,
  nodes,
  onSave,
  saving,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial: TunnelForm;
  nodes: Node[];
  onSave: (payload: TunnelWrite) => Promise<unknown>;
  saving?: boolean;
}) {
  const [form, setForm] = useState(initial);
  const [errors, setErrors] = useState<TunnelErrors>({});
  const [error, setError] = useState<unknown>();
  const nodeMap = useMemo(() => new Map(nodes.map((node) => [node.id, node])), [nodes]);
  const edit = Boolean(initial.id);
  const nft = nftModeFor(form, nodeMap);
  const offline = [form.inNodeId, form.relayNodeId, form.type === 2 ? form.outNodeId : null].some(
    (id) => id && nodeMap.get(id)?.status !== 1,
  );
  function patch(next: Partial<TunnelForm>) {
    setForm((previous) => ({ ...previous, ...next }));
    setErrors({});
    setError(undefined);
  }
  async function submit() {
    const validation = validateTunnelForm(form, nodeMap);
    setErrors(validation);
    if (Object.keys(validation).length > 0) return;
    try {
      await onSave({
        ...form,
        name: form.name.trim(),
        inNodeId: form.inNodeId!,
        protocol: nft ? "tcp+udp" : form.protocol,
      });
    } catch (failure) {
      setError(failure);
    }
  }
  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title={edit ? "编辑隧道" : "新建隧道"}
      description="配置转发链路、监听地址与流量计费"
      onSubmit={submit}
      submitText={edit ? "保存隧道" : "创建隧道"}
      submitting={saving}
      dirty={JSON.stringify(form) !== JSON.stringify(initial)}
    >
      {error ? <ErrorState error={error} /> : null}
      <FormSection title="基本信息">
        <FormField label="隧道名称" required error={errors.name}>
          <Input
            value={form.name}
            onChange={(event) => patch({ name: event.target.value })}
            autoComplete="off"
          />
        </FormField>
        <FormField
          label="类型"
          hint={edit ? immutableHint : "端口转发使用单节点；隧道转发连接入口与出口"}
        >
          <RadioGroup
            value={String(form.type)}
            onValueChange={(value) =>
              patch({ type: Number(value), relayNodeId: null, outNodeId: null })
            }
            disabled={edit}
            className="grid gap-2 sm:grid-cols-2"
          >
            <label className="flex min-h-16 items-center gap-2 rounded-md border border-border-control p-3">
              <RadioGroupItem value="1" />
              端口转发：单节点
            </label>
            <label className="flex min-h-16 items-center gap-2 rounded-md border border-border-control p-3">
              <RadioGroupItem value="2" />
              隧道转发：入口 → 出口
            </label>
          </RadioGroup>
        </FormField>
      </FormSection>
      <FormSection
        title="链路"
        description={edit ? immutableHint : "三节点链路要求入口、中继和出口均为 nftables 模式"}
      >
        <FormField label="入口节点" required error={errors.inNodeId}>
          <TunnelNodeSelect
            role="inNodeId"
            form={form}
            nodes={nodes}
            disabled={edit}
            onChange={(id) => patch({ inNodeId: id })}
          />
        </FormField>
        {form.type === 2 && (
          <>
            <FormField
              label="中继节点"
              error={errors.relayNodeId}
              hint="可选；三节点必须全部使用 nftables"
            >
              <TunnelNodeSelect
                role="relayNodeId"
                form={form}
                nodes={nodes}
                disabled={edit}
                onChange={(id) => patch({ relayNodeId: id })}
              />
            </FormField>
            <FormField label="出口节点" required error={errors.outNodeId}>
              <TunnelNodeSelect
                role="outNodeId"
                form={form}
                nodes={nodes}
                disabled={edit}
                onChange={(id) => patch({ outNodeId: id })}
              />
            </FormField>
          </>
        )}
        {offline && (
          <p
            role="status"
            className="rounded-md border border-status-warning bg-status-warning-bg p-3 text-caption text-status-warning"
          >
            {edit
              ? "节点当前离线，已保存链路的配置同步会在节点上线后重试；请在节点页核对同步状态"
              : "节点当前离线，新建隧道需等待所有链路节点上线"}
          </p>
        )}
        <div className="space-y-2">
          <p className="text-caption text-mute">链路预览</p>
          <PathChips tunnel={form} nodes={nodeMap} />
        </div>
      </FormSection>
      <FormSection
        title="协议与监听"
        description="监听地址决定节点接收流量的网卡；0.0.0.0 表示所有 IPv4 网卡，请确认防火墙允许范围"
      >
        <FormField
          label="协议"
          hint={nft ? "nftables 模式固定为 TCP+UDP" : "沿用当前隧道协议；更改后同步依赖的转发"}
        >
          <Select
            disabled={nft}
            value={nft ? "tcp+udp" : form.protocol}
            onValueChange={(value) => patch({ protocol: value })}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {[...new Set(["tcp", "udp", "tcp+udp", form.protocol])]
                .filter(Boolean)
                .map((protocol) => (
                  <SelectItem key={protocol} value={protocol}>
                    {protocol.toUpperCase()}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </FormField>
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField label="TCP 监听地址">
            <Input
              value={form.tcpListenAddr}
              onChange={(event) => patch({ tcpListenAddr: event.target.value })}
              className="font-mono"
            />
          </FormField>
          <FormField label="UDP 监听地址">
            <Input
              value={form.udpListenAddr}
              onChange={(event) => patch({ udpListenAddr: event.target.value })}
              className="font-mono"
            />
          </FormField>
        </div>
      </FormSection>
      <FormSection title="计费">
        <FormField label="计费方式">
          <RadioGroup
            value={String(form.flow)}
            onValueChange={(value) => patch({ flow: Number(value) })}
            className="flex flex-wrap gap-5"
          >
            <label className="flex min-h-8 items-center gap-2">
              <RadioGroupItem value="2" />
              双向
            </label>
            <label className="flex min-h-8 items-center gap-2">
              <RadioGroupItem value="1" />
              单向
            </label>
          </RadioGroup>
        </FormField>
        <FormField label="流量倍率" hint="按实际流量乘以倍率计费，默认 1.0">
          <Input
            type="number"
            min={0}
            step={0.1}
            value={form.trafficRatio}
            onChange={(event) => patch({ trafficRatio: Number(event.target.value) })}
            className="font-mono"
          />
        </FormField>
        <div className="space-y-1.5">
          <p className="text-caption text-body">状态</p>
          <StatusPill
            tone={form.status === 0 ? "neutral" : "good"}
            label={form.status === 0 ? "已禁用" : "启用"}
          />
          <p className="text-caption text-mute">状态由服务端维护，当前接口不支持切换</p>
        </div>
      </FormSection>
    </EntitySheet>
  );
}
