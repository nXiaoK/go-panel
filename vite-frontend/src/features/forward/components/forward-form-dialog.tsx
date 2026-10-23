import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
} from "@/features/forward/model";
import { createForward, updateForward } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { notify } from "@/lib/notify";
import type {
  Forward,
  ForwardExitMember,
  ForwardExitMode,
  ForwardTargetMode,
  ForwardTunnelOption,
  Node,
} from "@/lib/types";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

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

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
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
  const [saving, setSaving] = useState(false);

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
    if (!open) return;
    if (!editing) {
      setForm(emptyForm());
      return;
    }
    const remoteAddr = joinTargetAddresses(splitTargetAddresses(editing.remoteAddr));
    const targetAddrs = targetAddressFields(editing.remoteAddr);
    const tunnel = tunnelMap.get(editing.tunnelId);
    setForm({
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
    });
  }, [open, editing, tunnelMap]);

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

  const submit = async () => {
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
    if (validationError) return notify.error(validationError);
    if (
      hasRelay &&
      form.targetMode !== "manual" &&
      form.targetAddrs.filter((item) => item.trim()).length > 1
    ) {
      return notify.error("三节点 nftables 串联暂不支持多目标自动负载，请使用手动目标");
    }
    if (
      isAdmin &&
      isTunnelForward(tunnel) &&
      entryMode === "nftables" &&
      form.exitMode === "balance"
    ) {
      return notify.error("nftables 隧道暂不支持自动出口负载均衡，请使用手动负载");
    }
    setSaving(true);
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
      await (editing ? updateForward(payload) : createForward(payload));
      notify.success(editing ? "已更新" : "已创建");
      onOpenChange(false);
      onSaved();
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? "编辑转发" : "新建转发"}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <Field label="名称">
            <Input
              aria-label="名称"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="隧道">
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
                          <span className="text-xs text-muted-foreground">
                            {t.unavailableReason}
                          </span>
                        )}
                      </span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="入口端口（留空自动随机）">
              <Input
                type="number"
                min={1}
                max={65535}
                placeholder="自动分配未占用端口"
                value={form.inPort ?? ""}
                onChange={(e) =>
                  setForm({ ...form, inPort: e.target.value ? Number(e.target.value) : null })
                }
              />
              {!isAdmin &&
                selectedTunnel &&
                "inNodePortSta" in selectedTunnel &&
                selectedTunnel.inNodePortSta != null &&
                selectedTunnel.inNodePortEnd != null && (
                  <p className="text-xs text-muted-foreground">
                    可用端口 {selectedTunnel.inNodePortSta}–{selectedTunnel.inNodePortEnd}
                    ，留空自动分配
                  </p>
                )}
            </Field>
          </div>
          <Field label="目标地址">
            <div className="space-y-2">
              {form.targetAddrs.map((target, index) => (
                <div key={index} className="flex items-center gap-2">
                  <Input
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
          </Field>
          {hasRelay && (
            <p className="text-xs text-muted-foreground">
              三节点 nftables 串联暂不支持多目标自动负载，请使用手动目标
            </p>
          )}
          {entryMode === "nftables" && !hasRelay && (
            <p className="text-xs text-muted-foreground">
              nftables 入口的多目标转发由目标模式决定，手动目标仅使用当前选中的地址
            </p>
          )}
          <div className="rounded-md border border-border/60 bg-background/50 p-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="目标模式">
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
            </div>
            {form.targetMode === "manual" && (
              <div className="mt-3 space-y-3">
                <Field label="当前目标地址">
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
                  <div className="flex items-center justify-between gap-4 rounded-md border border-border/60 bg-muted/30 px-3 py-2">
                    <div className="min-w-0">
                      <Label htmlFor="force-switch-target" className="text-sm font-medium">
                        强制切换
                      </Label>
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        清理入口端口连接跟踪，旧访问会被断开并重新命中新目标。
                      </p>
                    </div>
                    <Switch
                      id="force-switch-target"
                      checked={form.forceSwitchTarget}
                      onCheckedChange={(checked) =>
                        setForm({ ...form, forceSwitchTarget: checked })
                      }
                    />
                  </div>
                )}
              </div>
            )}
          </div>
          {isAdmin && isTunnelForward(adminTunnel) && (
            <div className="rounded-md border border-border/60 bg-background/50 p-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="出口模式">
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
              <ExitMemberPicker
                className="mt-3"
                mode={form.exitMode}
                members={form.exitMembers}
                candidates={exitCandidates}
                nodeMap={nodeMap}
                tunnel={adminTunnel}
                onChange={(exitMembers) => setForm({ ...form, exitMembers })}
              />
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={submit} disabled={saving} className="shadow-glow">
            {saving && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />} 保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
