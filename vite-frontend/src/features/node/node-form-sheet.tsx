import { ErrorState } from "@/components/patterns/feedback";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { FormField } from "@/components/patterns/form-field";
import { FormSection } from "@/components/patterns/form-section";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { NodeForm } from "./model";

export function NodeFormSheet({
  open,
  onOpenChange,
  form,
  setForm,
  errors,
  online,
  saving,
  dirty,
  onSubmit,
  error,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  form: NodeForm;
  setForm: (form: NodeForm) => void;
  errors: Partial<Record<keyof NodeForm, string>>;
  online: boolean;
  saving: boolean;
  dirty: boolean;
  onSubmit: () => Promise<void>;
  error?: unknown;
}) {
  const editing = form.id !== null;
  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "编辑节点" : "添加节点"}
      description="配置节点的连接信息与可用端口"
      onSubmit={onSubmit}
      submitting={saving}
      dirty={dirty}
      submitText={editing ? "保存修改" : "添加节点"}
    >
      {Boolean(error) && <ErrorState error={error} />}
      <FormSection title="基本信息">
        <FormField label="节点名称" required error={errors.name}>
          <Input
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            placeholder="例如：香港-01"
          />
        </FormField>
        <FormField
          label="服务器 IP 或域名"
          required
          error={errors.serverIp}
          hint="被添加节点的真实地址，不是面板地址"
        >
          <Input
            value={form.serverIp}
            onChange={(event) => setForm({ ...form, serverIp: event.target.value })}
            placeholder="203.0.113.10 或 node.example.com"
          />
        </FormField>
      </FormSection>
      <FormSection
        title="入口 IP"
        description="展示给用户的访问地址；一行一个，支持 IPv4、IPv6 和域名。不确定时填写服务器 IP。"
      >
        <FormField label="入口地址" required error={errors.ipString}>
          <Textarea
            rows={3}
            value={form.ipString}
            onChange={(event) => setForm({ ...form, ipString: event.target.value })}
            placeholder={"203.0.113.10\n2001:db8::1"}
          />
        </FormField>
      </FormSection>
      <FormSection title="转发方式">
        <FormField label="节点运行模式">
          <RadioGroup
            className="grid gap-3 sm:grid-cols-2"
            value={form.forwardMode}
            onValueChange={(value) =>
              setForm({ ...form, forwardMode: value as NodeForm["forwardMode"] })
            }
          >
            {(
              [
                { value: "gost", title: "GOST", description: "用户态转发，支持多种协议" },
                {
                  value: "nftables",
                  title: "nftables",
                  description: "Linux 内核转发，使用 nftables 规则",
                },
              ] as const
            ).map((mode) => (
              <label
                key={mode.value}
                className="flex cursor-pointer items-start gap-3 rounded-md border border-border-control p-4 has-[[data-state=checked]]:bg-surface-selected"
              >
                <RadioGroupItem className="mt-0.5 shrink-0" value={mode.value} />
                <span>
                  <span className="block text-body-strong text-ink">{mode.title}</span>
                  <span className="mt-1 block text-caption text-mute">{mode.description}</span>
                </span>
              </label>
            ))}
          </RadioGroup>
        </FormField>
      </FormSection>
      <FormSection
        title="端口范围"
        description="仅允许在此范围内创建监听端口；调整前请确认现有转发仍在范围内。"
      >
        <div className="grid grid-cols-2 gap-3">
          <FormField label="起始端口" required error={errors.portSta}>
            <Input
              type="number"
              step="any"
              value={Number.isNaN(form.portSta) ? "" : form.portSta}
              onChange={(event) => setForm({ ...form, portSta: event.target.valueAsNumber })}
            />
          </FormField>
          <FormField label="结束端口" required error={errors.portEnd}>
            <Input
              type="number"
              step="any"
              value={Number.isNaN(form.portEnd) ? "" : form.portEnd}
              onChange={(event) => setForm({ ...form, portEnd: event.target.valueAsNumber })}
            />
          </FormField>
        </div>
      </FormSection>
      {editing && (
        <FormSection
          title="协议屏蔽"
          description={
            online
              ? "开启后屏蔽对应协议，仅入口节点需要；变更会影响已有转发。"
              : "节点在线后才能修改协议屏蔽"
          }
        >
          <div className="space-y-3">
            {(["http", "tls", "socks"] as const).map((protocol) => (
              <FormField key={protocol} label={`屏蔽 ${protocol.toUpperCase()}`}>
                <Switch
                  disabled={!online}
                  checked={form[protocol] === 1}
                  onCheckedChange={(value) => setForm({ ...form, [protocol]: value ? 1 : 0 })}
                />
              </FormField>
            ))}
          </div>
        </FormSection>
      )}
    </EntitySheet>
  );
}
