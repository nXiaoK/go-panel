import { Checkbox } from "@/components/ui/checkbox";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { FormField } from "@/components/patterns/form-field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { subscriptionFormatLabels, subscriptionFormats } from "@/features/subscription/model";
import { useSubscriptionMutation } from "@/features/subscription/queries";
import { createSubscriptionProfile, updateSubscriptionProfile } from "@/lib/api";
import { notify } from "@/lib/notify";
import type { ProxyNode, SubscriptionFormat, SubscriptionProfile } from "@/lib/types";
import { useState } from "react";

type TemplateFormat = Exclude<SubscriptionFormat, "v2rayn">;

// 新建订阅默认启用、使用 Surge；空模板沿用服务端默认，Token 由服务端生成。
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
  onSaved?: () => void;
}) {
  const initialForm = editing ? { ...editing } : { ...emptyProfile };
  const [form, setForm] = useState<SubscriptionProfile>(initialForm);
  const [nodeIds, setNodeIds] = useState<number[]>(initialNodeIds);
  const [nameError, setNameError] = useState("");
  const [templateTab, setTemplateTab] = useState<TemplateFormat>(
    editing?.defaultFormat && editing.defaultFormat !== "v2rayn" ? editing.defaultFormat : "surge",
  );
  const dirty =
    JSON.stringify(form) !== JSON.stringify(initialForm) ||
    JSON.stringify(nodeIds) !== JSON.stringify(initialNodeIds);

  const saveMutation = useSubscriptionMutation("subscription.update", {
    mutationFn: (payload: SubscriptionProfile & { nodeIds: number[] }) =>
      payload.id ? updateSubscriptionProfile(payload) : createSubscriptionProfile(payload),
    onSuccess: () => {
      notify.success("订阅配置已保存");
      onOpenChange(false);
      onSaved?.();
    },
    onError: (error: Error) => notify.error(error.message || "保存失败"),
  });

  const save = () => {
    if (!form.name.trim()) {
      setNameError("请输入订阅名称");
      document.getElementById("subscription-profile-name")?.focus();
      return;
    }
    setNameError("");
    return saveMutation.mutateAsync({ ...form, nodeIds }).catch(() => undefined);
  };

  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title={form.id ? "编辑订阅" : "新建订阅"}
      description="为不同人群定义独立 Token 和默认导出格式"
      dirty={dirty}
      onSubmit={save}
      submitText="保存"
      submitting={saveMutation.isPending}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <FormField label="名称" htmlFor="subscription-profile-name" required error={nameError}>
          <Input
            id="subscription-profile-name"
            value={form.name}
            onChange={(e) => {
              setForm({ ...form, name: e.target.value });
              setNameError("");
            }}
          />
        </FormField>
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
                className="min-h-[320px] font-mono text-xs"
              />
            </TabsContent>
            <TabsContent value="clash">
              <Textarea
                aria-label="Clash 模板配置"
                value={form.clashTemplate || ""}
                onChange={(e) => setForm({ ...form, clashTemplate: e.target.value })}
                rows={12}
                className="min-h-[320px] font-mono text-xs"
              />
            </TabsContent>
            <TabsContent value="singbox">
              <Textarea
                aria-label="Sing-box 模板配置"
                value={form.singboxTemplate || ""}
                onChange={(e) => setForm({ ...form, singboxTemplate: e.target.value })}
                rows={12}
                className="min-h-[320px] font-mono text-xs"
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
                    className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 hover:bg-surface-hover"
                  >
                    <Checkbox
                      checked={checked}
                      onCheckedChange={(c) => {
                        setNodeIds(c ? [...nodeIds, n.id] : nodeIds.filter((x) => x !== n.id));
                      }}
                    />
                    <span className="flex-1 truncate text-sm">{n.name}</span>
                    <span className="text-xs text-mute">{(n.protocol || "").toUpperCase()}</span>
                  </label>
                );
              })}
            </div>
          </ScrollArea>
        </div>
      </div>
    </EntitySheet>
  );
}
