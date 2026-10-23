import { FormField, type FormFieldProps } from "@/components/patterns/form-field";
import { Button } from "@/components/ui/button";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { ErrorState } from "@/components/patterns/feedback";
import { useMutation } from "@tanstack/react-query";
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
import { ExitMemberPicker } from "@/features/forward/components/exit-member-picker";
import {
  defaultExitMembers,
  isTunnelForward,
  normalizeExitMembersForMode,
} from "@/features/forward/exit-members";
import {
  joinTargetAddresses,
  normalizeTargetAddressInput,
  splitTargetAddresses,
  targetAddressFields,
  validateForwardForm,
  type ForwardFormErrors,
} from "@/features/forward/model";
import { createForward, updateForward } from "@/lib/api";
import { notify } from "@/lib/notify";
import type {
  Forward,
  ForwardExitMember,
  ForwardExitMode,
  ForwardTargetMode,
  ForwardTunnelOption,
  Node,
} from "@/lib/types";
import { Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

export interface ForwardFormState {
  id?: number;
  name: string;
  tunnelId: number | null;
  inPort: number | null;
  remoteAddr: string;
  targetAddrs: string[];
  strategy: string;
  targetMode: ForwardTargetMode;
  activeRemoteAddr: string;
  forceSwitchTarget: boolean;
  exitMode: ForwardExitMode;
  exitStrategy: string;
  exitMembers: ForwardExitMember[];
}

// 新建默认使用隧道分配入口端口和单出口；普通用户编辑会省略出口键以保留管理员配置。
const emptyForm = (): ForwardFormState => ({
  name: "",
  tunnelId: null,
  inPort: null,
  remoteAddr: "",
  targetAddrs: [""],
  strategy: "fifo",
  targetMode: "balance",
  activeRemoteAddr: "",
  forceSwitchTarget: false,
  exitMode: "single",
  exitStrategy: "fifo",
  exitMembers: [],
});

// 沿用后端支持的策略：单目标默认 fifo，多目标自动负载默认 round，不新增调度行为。
const STRATEGIES: Array<{ value: string; label: string }> = [
  { value: "fifo", label: "顺序 (FIFO)" },
  { value: "round", label: "轮询 (Round)" },
  { value: "rand", label: "随机 (Random)" },
  { value: "hash", label: "哈希 (Hash)" },
];

function autoStrategy(remoteAddr: string, current: string): string {
  const n = splitTargetAddresses(remoteAddr).length;
  if (n <= 1) return "fifo";
  return current === "fifo" ? "round" : current;
}

function normalizeActiveRemoteAddr(mode: ForwardTargetMode, remoteAddr: string, current: string) {
  const targets = splitTargetAddresses(remoteAddr);
  if (mode !== "manual") return targets.includes(current.trim()) ? current.trim() : "";
  if (targets.includes(current.trim())) return current.trim();
  return targets.length === 1 ? targets[0] : "";
}

function Field(props: FormFieldProps) {
  return <FormField {...props} />;
}

export function ForwardFormDialog({
  mode,
  open,
  onOpenChange,
  editing,
  tunnels,
  nodes,
  onSaved,
}: {
  mode: "admin" | "user";
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editing: Forward | null;
  tunnels: ForwardTunnelOption[];
  nodes: Node[];
  onSaved: () => void;
}) {
  const isAdmin = mode === "admin";
  const [form, setForm] = useState<ForwardFormState>(emptyForm());
  const [errors, setErrors] = useState<ForwardFormErrors>({});
  const [initialForm, setInitialForm] = useState("");
  const initialized = useRef<string | null>(null);
  const mutation = useMutation({
    mutationFn: (payload: Parameters<typeof createForward>[0]) =>
      editing ? updateForward(payload) : createForward(payload),
  });
  const resetMutation = mutation.reset;

  const tunnelMap = useMemo(() => {
    const m = new Map<number, ForwardTunnelOption>();
    tunnels.forEach((t) => m.set(t.id, t));
    return m;
  }, [tunnels]);

  const nodeMap = useMemo(() => {
    const m = new Map<number, Node>();
    nodes.forEach((n) => m.set(n.id, n));
    return m;
  }, [nodes]);

  const selectedTunnel = form.tunnelId ? tunnelMap.get(form.tunnelId) : null;
  const adminTunnel = selectedTunnel && "inNodeId" in selectedTunnel ? selectedTunnel : null;
  const selectedEntryNode = adminTunnel ? nodeMap.get(adminTunnel.inNodeId) : null;
  const hasRelay =
    selectedTunnel && "hasRelay" in selectedTunnel
      ? selectedTunnel.hasRelay
      : !!adminTunnel?.relayNodeId;
  const entryMode =
    selectedTunnel && "inNodeForwardMode" in selectedTunnel
      ? selectedTunnel.inNodeForwardMode
      : selectedEntryNode?.forwardMode;
  const exitCandidates = useMemo(() => {
    if (!isAdmin || !adminTunnel || !isTunnelForward(adminTunnel)) return [];
    const entryMode = selectedEntryNode?.forwardMode;
    return nodes.filter((n) => {
      if (n.id === adminTunnel.inNodeId) return false;
      if (n.id === adminTunnel.relayNodeId) return false;
      if (entryMode && n.forwardMode && n.forwardMode !== entryMode) return false;
      return true;
    });
  }, [nodes, selectedEntryNode?.forwardMode, adminTunnel, isAdmin]);

  useEffect(() => {
    if (!open) {
      initialized.current = null;
      return;
    }
    const key = editing ? String(editing.id) : "create";
    if (initialized.current === key) return;
    initialized.current = key;
    setErrors({});
    resetMutation();
    if (!editing) {
      const next = emptyForm();
      setForm(next);
      setInitialForm(JSON.stringify(next));
      return;
    }
    const remoteAddr = joinTargetAddresses(splitTargetAddresses(editing.remoteAddr));
    const targetAddrs = targetAddressFields(editing.remoteAddr);
    const tunnel = tunnelMap.get(editing.tunnelId);
    const next: ForwardFormState = {
      id: editing.id,
      name: editing.name,
      tunnelId: editing.tunnelId,
      inPort: editing.inPort,
      remoteAddr,
      targetAddrs,
      strategy: editing.strategy || "fifo",
      targetMode: (editing.targetMode as ForwardTargetMode) || "balance",
      activeRemoteAddr: normalizeActiveRemoteAddr(
        ((editing.targetMode as ForwardTargetMode) || "balance") as ForwardTargetMode,
        remoteAddr,
        editing.activeRemoteAddr || "",
      ),
      forceSwitchTarget: false,
      exitMode: (editing.exitMode as ForwardExitMode) || "single",
      exitStrategy: editing.exitStrategy || "fifo",
      exitMembers: normalizeExitMembersForMode(
        ((editing.exitMode as ForwardExitMode) || "single") as ForwardExitMode,
        editing.exitMembers?.length ? editing.exitMembers : defaultExitMembers(tunnel),
        tunnel,
      ),
    };
    setForm(next);
    setInitialForm(JSON.stringify(next));
  }, [open, editing, tunnelMap, resetMutation]);

  const updateTargetAddrs = (targetAddrs: string[]) => {
    const fields = targetAddrs.length ? targetAddrs : [""];
    const remoteAddr = joinTargetAddresses(fields);
    setForm((prev) => ({
      ...prev,
      targetAddrs: fields,
      remoteAddr,
      activeRemoteAddr: normalizeActiveRemoteAddr(
        prev.targetMode,
        remoteAddr,
        prev.activeRemoteAddr,
      ),
    }));
  };

  const updateTargetAddr = (index: number, value: string) => {
    const normalized = value.includes("://") ? normalizeTargetAddressInput(value) : value;
    const next = [...form.targetAddrs];
    next[index] = normalized;
    updateTargetAddrs(next);
  };

  const normalizeTargetAddrAt = (index: number, value: string) => {
    const next = [...form.targetAddrs];
    next[index] = normalizeTargetAddressInput(value);
    updateTargetAddrs(next);
  };

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    const tunnel = form.tunnelId ? tunnelMap.get(form.tunnelId) : null;
    const remoteAddr = joinTargetAddresses(form.targetAddrs);
    const exitMembers =
      isAdmin && isTunnelForward(tunnel)
        ? normalizeExitMembersForMode(form.exitMode, form.exitMembers, tunnel)
        : [];
    const activeRemoteAddr = normalizeActiveRemoteAddr(
      form.targetMode,
      remoteAddr,
      form.activeRemoteAddr,
    );
    const validationError = validateForwardForm({
      ...form,
      remoteAddr,
      activeRemoteAddr,
      tunnelType: isAdmin ? tunnel?.type : undefined,
      exitMembers,
    });
    if (
      hasRelay &&
      form.targetMode !== "manual" &&
      form.targetAddrs.filter((item) => item.trim()).length > 1
    ) {
      validationError.targetMode = "三节点 nftables 串联暂不支持多目标自动负载，请使用手动目标";
    }
    if (
      isAdmin &&
      isTunnelForward(tunnel) &&
      entryMode === "nftables" &&
      form.exitMode === "balance"
    ) {
      validationError.exitMode = "nftables 隧道暂不支持自动出口负载均衡，请使用手动负载";
    }
    setErrors(validationError);
    if (Object.keys(validationError).length) {
      const formElement = event.currentTarget;
      requestAnimationFrame(() =>
        formElement.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(),
      );
      return;
    }
    // 普通用户只能使用隧道默认出口；编辑时必须省略全部出口键，保留管理员已有配置。
    const {
      exitMode: _exitMode,
      exitStrategy: _exitStrategy,
      exitMembers: _exitMembers,
      ...baseForm
    } = form;
    const exitConfig = isAdmin
      ? {
          exitMode: isTunnelForward(tunnel) ? form.exitMode : "single",
          exitStrategy: isTunnelForward(tunnel) ? form.exitStrategy : "fifo",
          exitMembers,
        }
      : editing
        ? {}
        : { exitMode: "single", exitMembers: [] };
    const payload = {
      ...baseForm,
      tunnelId: form.tunnelId!,
      remoteAddr,
      strategy: autoStrategy(remoteAddr, form.strategy),
      targetMode: form.targetMode,
      activeRemoteAddr,
      forceSwitchTarget: editing ? form.forceSwitchTarget : false,
      ...exitConfig,
    };
    try {
      await mutation.mutateAsync(payload);
      notify.success(editing ? "已更新" : "已创建");
      onOpenChange(false);
      onSaved();
    } catch {
      // 请求错误由抽屉内联展示；useMutation 自动恢复按钮状态，允许保留输入并重试。
    }
  };

  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "编辑转发" : "新建转发"}
      description="设置入口与目标地址，保存后应用到节点"
      onSubmit={submit}
      submitText="保存"
      submitting={mutation.isPending}
      dirty={JSON.stringify(form) !== initialForm}
    >
      <section className="space-y-4" aria-labelledby="forward-basic-heading">
        <h2 id="forward-basic-heading" className="text-section-title">
          基本信息
        </h2>
        <Field label="名称" required error={errors.name}>
          <Input
            aria-label="名称"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="隧道" required error={errors.tunnelId}>
            <Select
              value={form.tunnelId ? String(form.tunnelId) : ""}
              onValueChange={(v) => {
                const nextTunnel = tunnelMap.get(Number(v));
                setForm({
                  ...form,
                  tunnelId: Number(v),
                  exitMode: isTunnelForward(nextTunnel) ? form.exitMode : "single",
                  exitMembers: isTunnelForward(nextTunnel) ? defaultExitMembers(nextTunnel) : [],
                });
              }}
            >
              <SelectTrigger>
                <SelectValue placeholder="选择隧道" />
              </SelectTrigger>
              <SelectContent>
                {tunnels.map((t) => (
                  <SelectItem
                    key={t.id}
                    value={String(t.id)}
                    disabled={"available" in t && !t.available}
                  >
                    <span className="flex flex-col items-start">
                      <span>{t.name}</span>
                      {"unavailableReason" in t && t.unavailableReason && (
                        <span className="text-xs text-mute">{t.unavailableReason}</span>
                      )}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field
            label="入口端口（留空自动随机）"
            error={errors.inPort}
            hint={
              !isAdmin &&
              selectedTunnel &&
              "inNodePortSta" in selectedTunnel &&
              selectedTunnel.inNodePortSta != null &&
              selectedTunnel.inNodePortEnd != null
                ? `可用端口 ${selectedTunnel.inNodePortSta}–${selectedTunnel.inNodePortEnd}，留空自动分配`
                : undefined
            }
          >
            <Input
              type="number"
              step="any"
              placeholder="自动分配未占用端口"
              value={form.inPort ?? ""}
              onChange={(e) =>
                setForm({ ...form, inPort: e.target.value ? Number(e.target.value) : null })
              }
            />
          </Field>
        </div>
      </section>
      <section className="space-y-4" aria-labelledby="forward-target-heading">
        <h2 id="forward-target-heading" className="text-section-title">
          目标
        </h2>
        <Field label="目标地址" required error={errors.remoteAddr}>
          {(control) => (
            <div className="space-y-2">
              {form.targetAddrs.map((target, index) => (
                <div key={index} className="flex items-center gap-2">
                  <Input
                    {...control}
                    id={index === 0 ? control.id : `${control.id}-${index}`}
                    aria-labelledby={index === 0 ? control["aria-labelledby"] : undefined}
                    aria-label={index === 0 ? undefined : `目标地址 ${index + 1}`}
                    value={target}
                    onChange={(e) => updateTargetAddr(index, e.target.value)}
                    onBlur={(e) => normalizeTargetAddrAt(index, e.target.value)}
                    onPaste={(e) => {
                      const pasted = e.clipboardData.getData("text");
                      if (!pasted.includes("://")) return;
                      e.preventDefault();
                      normalizeTargetAddrAt(index, pasted);
                    }}
                    placeholder={
                      index === 0
                        ? "10.211.55.5:1002 或粘贴 http://10.211.55.5:1002/login"
                        : "添加目标地址"
                    }
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="h-9 w-9 shrink-0"
                    disabled={form.targetAddrs.length <= 1}
                    onClick={() =>
                      updateTargetAddrs(form.targetAddrs.filter((_, i) => i !== index))
                    }
                    aria-label="删除目标地址"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ))}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => updateTargetAddrs([...form.targetAddrs, ""])}
              >
                <Plus className="h-3.5 w-3.5" /> 添加目标地址
              </Button>
            </div>
          )}
        </Field>
        {hasRelay && (
          <p className="text-xs text-mute">
            三节点 nftables 串联暂不支持多目标自动负载，请使用手动目标
          </p>
        )}
        {entryMode === "nftables" && !hasRelay && (
          <p className="text-xs text-mute">
            nftables 入口的多目标转发由目标模式决定，手动目标仅使用当前选中的地址
          </p>
        )}
        <div className="rounded-md border border-hairline bg-canvas p-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="目标模式" error={errors.targetMode}>
              <Select
                value={form.targetMode}
                onValueChange={(v) => {
                  const mode = v as ForwardTargetMode;
                  setForm({
                    ...form,
                    targetMode: mode,
                    activeRemoteAddr: normalizeActiveRemoteAddr(
                      mode,
                      form.remoteAddr,
                      form.activeRemoteAddr,
                    ),
                    forceSwitchTarget: mode === "manual" ? form.forceSwitchTarget : false,
                  });
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="balance">自动负载</SelectItem>
                  <SelectItem value="manual">手动目标</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {splitTargetAddresses(form.remoteAddr).length > 1 && (
              <Field label="目标负载策略">
                <Select
                  value={form.strategy}
                  onValueChange={(v) => setForm({ ...form, strategy: v })}
                  disabled={form.targetMode !== "balance"}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {STRATEGIES.map((s) => (
                      <SelectItem key={s.value} value={s.value}>
                        {s.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </div>
          {form.targetMode === "manual" && (
            <div className="mt-3 space-y-3">
              <Field label="当前目标地址" error={errors.activeRemoteAddr}>
                <Select
                  value={form.activeRemoteAddr}
                  onValueChange={(v) => setForm({ ...form, activeRemoteAddr: v })}
                >
                  <SelectTrigger className="mt-1.5">
                    <SelectValue placeholder="选择当前目标" />
                  </SelectTrigger>
                  <SelectContent>
                    {splitTargetAddresses(form.remoteAddr).map((target) => (
                      <SelectItem key={target} value={target}>
                        {target}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {editing && (
                <div className="flex items-center justify-between gap-4 rounded-md border border-hairline bg-canvas-soft px-3 py-2">
                  <div className="min-w-0">
                    <Label htmlFor="force-switch-target" className="text-sm font-medium">
                      强制切换
                    </Label>
                    <p className="mt-0.5 text-xs text-mute">
                      清理入口端口连接跟踪，旧访问会被断开并重新命中新目标。
                    </p>
                  </div>
                  <Switch
                    id="force-switch-target"
                    checked={form.forceSwitchTarget}
                    onCheckedChange={(checked) => setForm({ ...form, forceSwitchTarget: checked })}
                  />
                </div>
              )}
            </div>
          )}
        </div>
      </section>
      {isAdmin && isTunnelForward(adminTunnel) && (
        <section className="space-y-4" aria-labelledby="forward-exit-heading">
          <h2 id="forward-exit-heading" className="text-section-title">
            出口
          </h2>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="出口模式" error={errors.exitMode}>
              <Select
                value={form.exitMode}
                onValueChange={(v) => {
                  const mode = v as ForwardExitMode;
                  setForm({
                    ...form,
                    exitMode: mode,
                    exitMembers: normalizeExitMembersForMode(
                      mode,
                      form.exitMembers,
                      selectedTunnel,
                    ),
                  });
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="single">单出口</SelectItem>
                  <SelectItem value="manual">手动负载</SelectItem>
                  <SelectItem value="balance">自动负载均衡</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="出口策略">
              <Select
                value={form.exitStrategy}
                onValueChange={(v) => setForm({ ...form, exitStrategy: v })}
                disabled={form.exitMode !== "balance"}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {STRATEGIES.map((s) => (
                    <SelectItem key={s.value} value={s.value}>
                      {s.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
          <div
            tabIndex={errors.exitMembers ? -1 : undefined}
            aria-invalid={errors.exitMembers ? true : undefined}
            aria-describedby={errors.exitMembers ? "forward-exit-error" : undefined}
          >
            <ExitMemberPicker
              className="mt-3"
              mode={form.exitMode}
              members={form.exitMembers}
              candidates={exitCandidates}
              nodeMap={nodeMap}
              tunnel={adminTunnel}
              onChange={(exitMembers) => setForm({ ...form, exitMembers })}
            />
            {errors.exitMembers && (
              <p id="forward-exit-error" className="mt-2 text-caption text-status-critical-text">
                {errors.exitMembers}
              </p>
            )}
          </div>
        </section>
      )}
      {mutation.error && <ErrorState error={mutation.error} />}
    </EntitySheet>
  );
}
