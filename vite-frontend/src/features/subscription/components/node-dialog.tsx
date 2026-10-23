import { FormField as Field } from "@/components/patterns/form-field";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useSubscriptionMutation } from "@/features/subscription/queries";
import { updateProxyNode } from "@/lib/api";
import { notify } from "@/lib/notify";
import type { ProxyNode, ProxyNodeWrite } from "@/lib/types";
import { useState } from "react";

// 编辑补全旧节点缺省值：默认 VLESS/443、UDP 开启、TLS 严格验证；不改动现有节点的值。
const emptyNode: ProxyNode = {
  id: 0,
  externalId: "",
  name: "",
  protocol: "vless",
  server: "",
  port: 443,
  allowInsecure: 0,
  udp: 1,
  sort: 0,
  status: 1,
  lastReportTime: 0,
};

export function SubscriptionNodeDialog({
  open,
  onOpenChange,
  editing,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editing: ProxyNode | null;
  onSaved?: () => void;
}) {
  const initial = editing ? { ...emptyNode, ...editing } : { ...emptyNode };
  const [form, setForm] = useState<ProxyNode>(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const dirty = JSON.stringify(form) !== JSON.stringify(initial);

  const saveMutation = useSubscriptionMutation("subscription.update", {
    mutationFn: (payload: ProxyNodeWrite) => updateProxyNode(payload),
    onSuccess: () => {
      notify.success("节点已保存");
      onOpenChange(false);
      onSaved?.();
    },
    onError: (error: Error) => notify.error(error.message || "节点保存失败"),
  });

  const save = () => {
    const next: Record<string, string> = {};
    if (!form.name.trim()) next.name = "请输入节点名称";
    if (!form.server.trim()) next.server = "请输入服务器地址";
    if (!Number.isInteger(form.port) || form.port < 1 || form.port > 65535)
      next.port = "端口必须是 1–65535 的整数";
    setErrors(next);
    const first = Object.keys(next)[0];
    if (first) {
      document.getElementById(`subscription-node-${first}`)?.focus();
      return;
    }
    return saveMutation
      .mutateAsync({
        ...form,
        port: Number(form.port),
        snellVersion: Number(form.snellVersion || 0),
        allowInsecure: form.allowInsecure === 1,
        udp: form.udp === 1,
        sort: Number(form.sort || 0),
      })
      .catch(() => undefined);
  };

  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title="编辑节点"
      description="修改协议连接参数与订阅展示信息"
      dirty={dirty}
      onSubmit={save}
      submitText="保存"
      submitting={saveMutation.isPending}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="名称" htmlFor="subscription-node-name" required error={errors.name}>
          <Input
            id="subscription-node-name"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field label="协议" htmlFor="subscription-node-protocol">
          <Select value={form.protocol} onValueChange={(v) => setForm({ ...form, protocol: v })}>
            <SelectTrigger id="subscription-node-protocol">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["vless", "vmess", "trojan", "snell", "ss", "socks5"].map((p) => (
                <SelectItem key={p} value={p}>
                  {p.toUpperCase()}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="服务器" htmlFor="subscription-node-server" required error={errors.server}>
          <Input
            id="subscription-node-server"
            value={form.server}
            onChange={(e) => setForm({ ...form, server: e.target.value })}
          />
        </Field>
        <Field label="端口" htmlFor="subscription-node-port" required error={errors.port}>
          <Input
            id="subscription-node-port"
            type="number"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: Number(e.target.value) })}
          />
        </Field>
        <Field label="UUID / 密码" htmlFor="subscription-node-credential">
          <Input
            id="subscription-node-credential"
            value={form.uuid || form.password || ""}
            onChange={(e) => setForm({ ...form, uuid: e.target.value, password: e.target.value })}
          />
        </Field>
        <Field label="SNI" htmlFor="subscription-node-sni">
          <Input
            id="subscription-node-sni"
            value={form.sni || ""}
            onChange={(e) => setForm({ ...form, sni: e.target.value })}
          />
        </Field>
        <Field label="传输" htmlFor="subscription-node-network">
          <Input
            id="subscription-node-network"
            value={form.network || ""}
            onChange={(e) => setForm({ ...form, network: e.target.value })}
          />
        </Field>
        <Field label="Path" htmlFor="subscription-node-path">
          <Input
            id="subscription-node-path"
            value={form.path || ""}
            onChange={(e) => setForm({ ...form, path: e.target.value })}
          />
        </Field>
        <Field label="地区" htmlFor="subscription-node-region">
          <Input
            id="subscription-node-region"
            value={form.region || ""}
            onChange={(e) => setForm({ ...form, region: e.target.value })}
          />
        </Field>
        <Field label="排序" htmlFor="subscription-node-sort">
          <Input
            id="subscription-node-sort"
            type="number"
            value={form.sort}
            onChange={(e) => setForm({ ...form, sort: Number(e.target.value) })}
          />
        </Field>
        <div className="flex items-center gap-2">
          <Switch
            id="subscription-node-enabled"
            checked={form.status === 1}
            onCheckedChange={(v) => setForm({ ...form, status: v ? 1 : 0 })}
          />
          <Label htmlFor="subscription-node-enabled">启用</Label>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="subscription-node-udp"
            checked={form.udp === 1}
            onCheckedChange={(v) => setForm({ ...form, udp: v ? 1 : 0 })}
          />
          <Label htmlFor="subscription-node-udp">UDP</Label>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="subscription-node-insecure"
            checked={form.allowInsecure === 1}
            onCheckedChange={(v) => setForm({ ...form, allowInsecure: v ? 1 : 0 })}
          />
          <Label htmlFor="subscription-node-insecure">允许不安全（跳过证书验证）</Label>
        </div>
      </div>
      <p className="text-caption text-mute">
        允许不安全会跳过服务器证书验证，可能遭受中间人攻击；默认关闭，仅在明确了解风险时启用。
      </p>
    </EntitySheet>
  );
}
