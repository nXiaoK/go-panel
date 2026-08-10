import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
import { Textarea } from "@/components/ui/textarea";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import { createSubscriptionProfile, updateSubscriptionProfile } from "@/lib/api";
import { unwrap } from "@/lib/api/query";
import type { ProxyNode, SubscriptionFormat, SubscriptionProfile } from "@/lib/types";
import { subscriptionFormatLabels, subscriptionFormats } from "./formats";

type TemplateFormat = Exclude<SubscriptionFormat, "v2rayn">;

const emptyProfile: SubscriptionProfile = {
  id: 0,
  name: "",
  token: "",
  defaultFormat: "surge",
  description: "",
  surgeTemplate: "",
  clashTemplate: "",
  singboxTemplate: "",
  status: 1,
};

export function SubscriptionProfileDialog({
  open,
  onOpenChange,
  editing,
  nodes,
  initialNodeIds,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editing: SubscriptionProfile | null;
  nodes: ProxyNode[];
  /** Node ids to preselect when creating (all) or editing (bound). */
  initialNodeIds: number[];
  onSaved: () => void;
}) {
  const [form, setForm] = useState<SubscriptionProfile>(emptyProfile);
  const [nodeIds, setNodeIds] = useState<number[]>([]);
  const [templateTab, setTemplateTab] = useState<TemplateFormat>("surge");

  useEffect(() => {
    if (!open) return;
    if (!editing) {
      setForm({ ...emptyProfile });
      setNodeIds(initialNodeIds);
      setTemplateTab("surge");
      return;
    }
    setForm({ ...editing });
    setNodeIds(initialNodeIds);
    setTemplateTab(editing.defaultFormat === "v2rayn" ? "surge" : editing.defaultFormat);
  }, [open, editing, initialNodeIds]);

  const saveMutation = useMutation({
    mutationFn: (payload: SubscriptionProfile & { nodeIds: number[] }) =>
      (payload.id ? updateSubscriptionProfile(payload) : createSubscriptionProfile(payload)).then(
        unwrap,
      ),
    onSuccess: () => {
      toast.success("订阅配置已保存");
      onOpenChange(false);
      onSaved();
    },
    onError: (error: Error) => toast.error(error.message || "保存失败"),
  });

  const save = () => {
    if (!form.name.trim()) return toast.error("请输入订阅名称");
    saveMutation.mutate({ ...form, nodeIds });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{form.id ? "编辑订阅" : "新建订阅"}</DialogTitle>
          <DialogDescription>为不同人群定义独立 Token 和默认导出格式</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label htmlFor="subscription-profile-name">名称</Label>
            <Input
              id="subscription-profile-name"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="subscription-profile-format">默认格式</Label>
            <Select
              value={form.defaultFormat}
              onValueChange={(v) => {
                setForm({ ...form, defaultFormat: v as SubscriptionFormat });
                if (v !== "v2rayn") setTemplateTab(v as TemplateFormat);
              }}
            >
              <SelectTrigger id="subscription-profile-format">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {subscriptionFormats.map((f) => (
                  <SelectItem key={f} value={f}>
                    {subscriptionFormatLabels[f]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="sm:col-span-2 space-y-1">
            <Label htmlFor="subscription-profile-description">说明</Label>
            <Input
              id="subscription-profile-description"
              value={form.description || ""}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
            />
          </div>
          <div className="sm:col-span-2 flex items-center gap-2">
            <Switch
              id="subscription-profile-enabled"
              checked={form.status === 1}
              onCheckedChange={(v) => setForm({ ...form, status: v ? 1 : 0 })}
            />
            <Label htmlFor="subscription-profile-enabled">启用</Label>
          </div>
          <div className="sm:col-span-2 space-y-2">
            <Label id="subscription-profile-templates-label">模板配置</Label>
            <Tabs
              value={templateTab}
              onValueChange={(v) => setTemplateTab(v as TemplateFormat)}
              aria-labelledby="subscription-profile-templates-label"
            >
              <TabsList>
                <TabsTrigger value="surge">Surge</TabsTrigger>
                <TabsTrigger value="clash">Clash</TabsTrigger>
                <TabsTrigger value="singbox">Sing-box</TabsTrigger>
              </TabsList>
              <TabsContent value="surge">
                <Textarea
                  aria-label="Surge 模板配置"
                  value={form.surgeTemplate || ""}
                  onChange={(e) => setForm({ ...form, surgeTemplate: e.target.value })}
                  rows={12}
                  className="min-h-[260px] font-mono text-xs"
                />
              </TabsContent>
              <TabsContent value="clash">
                <Textarea
                  aria-label="Clash 模板配置"
                  value={form.clashTemplate || ""}
                  onChange={(e) => setForm({ ...form, clashTemplate: e.target.value })}
                  rows={12}
                  className="min-h-[260px] font-mono text-xs"
                />
              </TabsContent>
              <TabsContent value="singbox">
                <Textarea
                  aria-label="Sing-box 模板配置"
                  value={form.singboxTemplate || ""}
                  onChange={(e) => setForm({ ...form, singboxTemplate: e.target.value })}
                  rows={12}
                  className="min-h-[260px] font-mono text-xs"
                />
              </TabsContent>
            </Tabs>
          </div>
          <div className="sm:col-span-2 space-y-2">
            <Label>
              绑定节点 ({nodeIds.length}/{nodes.length})
            </Label>
            <ScrollArea className="h-56 rounded-md border p-2">
              <div className="space-y-1">
                {nodes.map((n) => {
                  const checked = nodeIds.includes(n.id);
                  return (
                    <label
                      key={n.id}
                      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 hover:bg-muted"
                    >
                      <Checkbox
                        checked={checked}
                        onCheckedChange={(c) => {
                          setNodeIds(c ? [...nodeIds, n.id] : nodeIds.filter((x) => x !== n.id));
                        }}
                      />
                      <span className="flex-1 truncate text-sm">{n.name}</span>
                      <span className="text-xs text-muted-foreground">
                        {(n.protocol || "").toUpperCase()}
                      </span>
                    </label>
                  );
                })}
              </div>
            </ScrollArea>
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
