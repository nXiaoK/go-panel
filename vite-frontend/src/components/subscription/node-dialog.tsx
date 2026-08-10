import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { updateProxyNode } from "@/lib/api";
import { unwrap } from "@/lib/api/query";
import type { ProxyNode } from "@/lib/types";
import { FormField as Field } from "@/components/page";

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
  onSaved: () => void;
}) {
  const [form, setForm] = useState<ProxyNode>(emptyNode);

  useEffect(() => {
    if (!open) return;
    setForm(editing ? { ...emptyNode, ...editing } : { ...emptyNode });
  }, [open, editing]);

  const saveMutation = useMutation({
    mutationFn: (payload: Record<string, unknown>) => updateProxyNode(payload).then(unwrap),
    onSuccess: () => {
      toast.success("节点已保存");
      onOpenChange(false);
      onSaved();
    },
    onError: (error: Error) => toast.error(error.message || "节点保存失败"),
  });

  const save = () => {
    if (!form.name.trim() || !form.server.trim()) return toast.error("节点名称和地址不能为空");
    saveMutation.mutate({
      ...form,
      port: Number(form.port),
      snellVersion: Number(form.snellVersion || 0),
      allowInsecure: form.allowInsecure === 1,
      udp: form.udp === 1,
      sort: Number(form.sort || 0),
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑节点</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="名称" htmlFor="subscription-node-name">
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
          <Field label="服务器" htmlFor="subscription-node-server">
            <Input
              id="subscription-node-server"
              value={form.server}
              onChange={(e) => setForm({ ...form, server: e.target.value })}
            />
          </Field>
          <Field label="端口" htmlFor="subscription-node-port">
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
            <Label htmlFor="subscription-node-insecure">允许不安全</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={save} disabled={saveMutation.isPending}>
            {saveMutation.isPending ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
