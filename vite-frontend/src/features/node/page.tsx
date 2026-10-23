import { FormField, KpiCard, PageHeader, QueryErrorNotice } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { isMissingPanelAddressError } from "@/features/node/model";
import { useConfirm } from "@/hooks/use-confirm";
import { usePreferences } from "@/hooks/use-preferences";
import { useSession } from "@/hooks/use-session";
import {
  checkNodeStatus,
  createNode,
  deleteNode,
  getNodeInstallCommand,
  getNodeUninstallCommand,
  updateNode,
  upgradeNode,
} from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDateTime, formatDuration, formatRate } from "@/lib/format";
import { notify } from "@/lib/notify";
import { invalidateFor, resourceKeys as queries } from "@/lib/query-keys";
import type { Node, NodeStatusCheck, NodeWrite } from "@/lib/types";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  Activity,
  AlertCircle,
  Copy,
  Cpu,
  Download,
  HardDrive,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Server,
  Stethoscope,
  Trash2,
  Upload,
  Wifi,
  WifiOff,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useNodeLives } from "./use-node-live";
import { useNodeSocket } from "./use-node-socket";
import { useNodeMutation, useNodes } from "./queries";

interface NodeForm {
  id: number | null;
  name: string;
  ipString: string;
  serverIp: string;
  portSta: number;
  portEnd: number;
  http: number;
  tls: number;
  socks: number;
  forwardMode: "gost" | "nftables";
}

const emptyForm = (): NodeForm => ({
  id: null,
  name: "",
  ipString: "",
  serverIp: "",
  portSta: 1000,
  portEnd: 65535,
  http: 0,
  tls: 0,
  socks: 0,
  forwardMode: "gost",
});

/* ============ helpers ============ */

// 简单地址校验：IPv4 / IPv6 / 域名 / localhost
const IPV4 =
  /^(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}$/;
const IPV6 = /^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$/;
const DOMAIN =
  /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$/;

function validAddr(a: string): boolean {
  const s = a.trim();
  if (!s) return false;
  if (s === "localhost") return true;
  return IPV4.test(s) || IPV6.test(s) || DOMAIN.test(s);
}

/* ============ page ============ */

export function NodePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const confirm = useConfirm();

  const [formOpen, setFormOpen] = useState(false);
  const [isEdit, setIsEdit] = useState(false);
  const [form, setForm] = useState<NodeForm>(emptyForm());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [protocolDisabled, setProtocolDisabled] = useState(false);

  const [cmdOpen, setCmdOpen] = useState(false);
  const [cmdTitle, setCmdTitle] = useState("");
  const [cmdText, setCmdText] = useState("");
  const [cmdNodeName, setCmdNodeName] = useState("");
  const [copyBusy, setCopyBusy] = useState<number | null>(null);
  const [upgradeBusy, setUpgradeBusy] = useState<number | null>(null);

  const [diagOpen, setDiagOpen] = useState(false);
  const [diagLoading, setDiagLoading] = useState(false);
  const [diagResult, setDiagResult] = useState<NodeStatusCheck | null>(null);
  const [diagNode, setDiagNode] = useState<Node | null>(null);

  const { isAdmin } = useSession();

  const { autoRefresh: autoRefreshEnabled } = usePreferences();

  /* ---- load ---- */
  const nodesQuery = useNodes({
    runtime: true,
    refetchInterval: autoRefreshEnabled ? 30_000 : false,
  });
  const live = useNodeLives();
  useNodeSocket();
  const nodes = useMemo(
    () =>
      (nodesQuery.data ?? []).map((node) => ({
        ...node,
        connectionStatus:
          live.get(node.id)?.connection ?? (node.status === 1 ? "online" : "offline"),
        systemInfo: live.get(node.id)?.info ?? null,
      })),
    [nodesQuery.data, live],
  );
  const loading = nodesQuery.isPending;

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: queries.node.list() });
  }, [queryClient]);

  const saveMutation = useNodeMutation("node.update", {
    mutationFn: ({ payload, isEdit }: { payload: NodeWrite; isEdit: boolean }) =>
      isEdit ? updateNode(payload) : createNode(payload),
    onSuccess: (_data, { isEdit }) => {
      notify.success(isEdit ? "已更新" : "已创建");
      setFormOpen(false);
    },
    onError: (error: Error) => notify.error(error.message || "保存失败"),
  });

  const deleteMutation = useNodeMutation("node.delete", {
    mutationFn: (id: number) => deleteNode(id),
    onSuccess: () => {
      notify.success("已删除");
    },
    onError: (error: Error) => notify.error(error.message || "删除失败"),
  });

  /* ---- stats ---- */
  const stats = useMemo(() => {
    const total = nodes.length;
    const online = nodes.filter((n) => n.connectionStatus === "online").length;
    const offline = total - online;
    const gost = nodes.filter((n) => (n.forwardMode || "gost") === "gost").length;
    const nft = nodes.filter((n) => n.forwardMode === "nftables").length;
    return { total, online, offline, gost, nft };
  }, [nodes]);

  /* ---- form / crud ---- */
  const openAdd = () => {
    setForm(emptyForm());
    setErrors({});
    setIsEdit(false);
    setProtocolDisabled(true);
    setFormOpen(true);
  };

  const openEdit = (n: Node) => {
    setForm({
      id: n.id,
      name: n.name,
      ipString: (n.ip || "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean)
        .join("\n"),
      serverIp: n.serverIp || "",
      portSta: n.portSta ?? 1000,
      portEnd: n.portEnd ?? 65535,
      http: typeof n.http === "number" ? n.http : 0,
      tls: typeof n.tls === "number" ? n.tls : 0,
      socks: typeof n.socks === "number" ? n.socks : 0,
      forwardMode: n.forwardMode === "nftables" ? "nftables" : "gost",
    });
    setErrors({});
    setIsEdit(true);
    setProtocolDisabled(n.connectionStatus !== "online");
    setFormOpen(true);
  };

  const validate = (): boolean => {
    const e: Record<string, string> = {};
    const name = form.name.trim();
    if (!name) e.name = "请输入节点名称";
    else if (name.length < 2 || name.length > 50) e.name = "节点名称长度需在 2-50 之间";
    if (!form.serverIp.trim() || !validAddr(form.serverIp))
      e.serverIp = "请输入合法的服务器 IP / 域名";
    const ips = form.ipString
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    if (ips.length === 0) e.ipString = "至少填写一个入口 IP";
    else {
      for (let i = 0; i < ips.length; i++)
        if (!validAddr(ips[i])) {
          e.ipString = `第 ${i + 1} 行地址不合法：${ips[i]}`;
          break;
        }
    }
    if (!form.portSta || form.portSta < 1 || form.portSta > 65535) e.portSta = "端口需在 1-65535";
    if (!form.portEnd || form.portEnd < 1 || form.portEnd > 65535) e.portEnd = "端口需在 1-65535";
    else if (form.portEnd < form.portSta) e.portEnd = "结束端口需大于起始端口";
    setErrors(e);
    return Object.keys(e).length === 0;
  };

  const submit = async () => {
    if (!validate()) return;
    const ip = form.ipString
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean)
      .join(",");
    const payload: NodeWrite = {
      name: form.name.trim(),
      ip,
      serverIp: form.serverIp.trim(),
      portSta: form.portSta,
      portEnd: form.portEnd,
      http: form.http,
      tls: form.tls,
      socks: form.socks,
      forwardMode: form.forwardMode,
    };
    if (isEdit && form.id) payload.id = form.id;
    saveMutation.mutate({ payload, isEdit });
  };

  const remove = async (n: Node) => {
    if (
      !(await confirm({
        title: `删除节点「${n.name}」`,
        description:
          "删除后该节点的连接配置将被移除。请先处理依赖该节点的隧道和转发，此操作无法恢复。",
        confirmText: "删除节点",
        tone: "danger",
        requireText: n.name,
      }))
    )
      return;
    deleteMutation.mutate(n.id);
  };

  /* ---- install / uninstall ---- */
  const fetchCommand = async (n: Node, kind: "install" | "uninstall") => {
    setCopyBusy(n.id);
    const api = kind === "install" ? getNodeInstallCommand : getNodeUninstallCommand;
    try {
      const commandText = await api(n.id, n.forwardMode || "gost");
      const label = kind === "install" ? "安装命令" : "卸载命令";
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        try {
          await navigator.clipboard.writeText(commandText);
          notify.success(`${label}已复制到剪贴板`);
        } catch {
          notify.error("自动复制失败，请在弹窗中手动复制");
        }
        openCommandModal(label, commandText, n.name);
      } else {
        openCommandModal(label, commandText, n.name);
      }
    } catch (error) {
      const msg = errorMessage(error);
      if (isMissingPanelAddressError(msg)) {
        notify.error("请先在系统配置中设置面板公网地址", {
          action: { label: "去设置", onClick: () => navigate({ to: "/config" }) },
        });
      } else notify.error(msg);
    } finally {
      setCopyBusy(null);
    }
  };

  const openCommandModal = (title: string, text: string, nodeName: string) => {
    setCmdTitle(title);
    setCmdText(text);
    setCmdNodeName(nodeName);
    setCmdOpen(true);
  };

  const manualCopy = async () => {
    try {
      await navigator.clipboard.writeText(cmdText);
      notify.success(`${cmdTitle}已复制`);
    } catch {
      notify.error("复制失败，请手动选择文本");
    }
  };

  const runUpgrade = async (n: Node) => {
    if (!n.upgradeAvailable) return;
    const latest = n.latestVersion || "最新版本";
    if (
      !(await confirm({
        title: `升级节点「${n.name}」`,
        description: `升级到 ${latest} 后节点会自动重启并短暂离线，期间转发可能中断。`,
        confirmText: "升级节点",
      }))
    )
      return;
    setUpgradeBusy(n.id);
    try {
      await upgradeNode(n.id);
      void invalidateFor(queryClient, "node.upgrade");
      notify.success("升级已触发，节点稍后会自动重连");
      window.setTimeout(refresh, 3000);
    } catch (error) {
      notify.error(error instanceof Error ? error.message : "升级失败");
    } finally {
      setUpgradeBusy(null);
    }
  };

  /* ---- diagnose (check-status) ---- */
  const runDiagnose = async (n: Node) => {
    setDiagOpen(true);
    setDiagLoading(true);
    setDiagResult(null);
    setDiagNode(n);
    try {
      setDiagResult(await checkNodeStatus(n.id));
    } catch (error) {
      notify.error(error instanceof Error ? error.message : "检测失败");
    } finally {
      setDiagLoading(false);
    }
  };

  /* ---- render ---- */
  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow={
          <>
            <Server className="h-3.5 w-3.5" /> nodes
          </>
        }
        title="节点集群"
        description="实时监控 CPU/内存/流量，管理节点安装脚本与协议屏蔽"
        actions={
          <>
            <Button variant="outline" size="sm" onClick={refresh} disabled={nodesQuery.isFetching}>
              <RefreshCw
                className={`mr-1.5 h-3.5 w-3.5 ${nodesQuery.isFetching ? "animate-spin" : ""}`}
              />
              刷新
            </Button>
            {isAdmin && (
              <Button size="sm" onClick={openAdd}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> 新增节点
              </Button>
            )}
          </>
        }
      />

      <QueryErrorNotice error={nodesQuery.error} onRetry={() => void nodesQuery.refetch()} />

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard label="节点总数" value={stats.total} icon={<Server className="h-4 w-4" />} />
        <KpiCard label="在线" value={stats.online} tone="ok" icon={<Wifi className="h-4 w-4" />} />
        <KpiCard
          label="离线"
          value={stats.offline}
          tone={stats.offline > 0 ? "warn" : undefined}
          icon={<WifiOff className="h-4 w-4" />}
        />
        <KpiCard
          label="模式"
          value={`GOST ${stats.gost} · NFT ${stats.nft}`}
          icon={<Cpu className="h-4 w-4" />}
        />
      </div>

      {loading && nodes.length === 0 ? (
        <Card className="border-border/60 bg-card/60">
          <CardContent className="flex h-40 items-center justify-center text-muted-foreground">
            <Loader2 className="mr-2 h-5 w-5 animate-spin" /> 加载中…
          </CardContent>
        </Card>
      ) : nodes.length === 0 ? (
        <Card className="border-border/60 bg-card/60">
          <CardContent className="flex flex-col items-center gap-3 py-16 text-center">
            <Server className="h-10 w-10 text-muted-foreground/60" />
            <div>
              <div className="text-lg font-medium">暂无节点</div>
              <div className="mt-1 text-sm text-muted-foreground">点击右上角「新增节点」开始</div>
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {nodes.map((n) => (
            <NodeCard
              key={n.id}
              node={n}
              busy={copyBusy === n.id}
              isAdmin={isAdmin}
              onInstall={() => fetchCommand(n, "install")}
              onUninstall={() => fetchCommand(n, "uninstall")}
              onUpgrade={() => runUpgrade(n)}
              upgradeBusy={upgradeBusy === n.id}
              onEdit={() => openEdit(n)}
              onDelete={() => remove(n)}
              onDiagnose={() => runDiagnose(n)}
            />
          ))}
        </div>
      )}

      {/* 表单弹窗 */}
      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{isEdit ? "编辑节点" : "新增节点"}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <FormField label="节点名称" htmlFor="node-form-name" error={errors.name}>
              <Input
                id="node-form-name"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：香港-01"
              />
            </FormField>
            <FormField
              label="服务器 IP / 域名"
              htmlFor="node-form-server-ip"
              error={errors.serverIp}
            >
              <Input
                id="node-form-server-ip"
                value={form.serverIp}
                onChange={(e) => setForm({ ...form, serverIp: e.target.value })}
                placeholder="192.168.1.100 或 example.com"
              />
            </FormField>
            <FormField
              label="入口 IP（一行一个）"
              htmlFor="node-form-entry-ips"
              hint="用于展示给用户的访问地址，支持多行"
              error={errors.ipString}
            >
              <Textarea
                id="node-form-entry-ips"
                value={form.ipString}
                onChange={(e) => setForm({ ...form, ipString: e.target.value })}
                rows={3}
                placeholder={"1.1.1.1\nnode.example.com"}
              />
            </FormField>
            <FormField label="转发方式">
              {(control) => (
                <>
                  <div
                    role="group"
                    aria-labelledby={control["aria-labelledby"]}
                    className="grid grid-cols-2 gap-2"
                  >
                    {(["gost", "nftables"] as const).map((mode) => (
                      <Button
                        key={mode}
                        type="button"
                        variant={form.forwardMode === mode ? "default" : "outline"}
                        onClick={() => setForm({ ...form, forwardMode: mode })}
                      >
                        {mode.toUpperCase()}
                      </Button>
                    ))}
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    选择 nftables 后，安装脚本会切换为 nftables 节点脚本
                  </div>
                </>
              )}
            </FormField>
            <div className="grid grid-cols-2 gap-3">
              <FormField label="起始端口" htmlFor="node-form-port-start" error={errors.portSta}>
                <Input
                  id="node-form-port-start"
                  type="number"
                  min={1}
                  max={65535}
                  value={form.portSta}
                  onChange={(e) => setForm({ ...form, portSta: parseInt(e.target.value) || 1000 })}
                />
              </FormField>
              <FormField label="结束端口" htmlFor="node-form-port-end" error={errors.portEnd}>
                <Input
                  id="node-form-port-end"
                  type="number"
                  min={1}
                  max={65535}
                  value={form.portEnd}
                  onChange={(e) => setForm({ ...form, portEnd: parseInt(e.target.value) || 65535 })}
                />
              </FormField>
            </div>
            <div>
              <div className="mb-2 flex items-baseline justify-between">
                <div className="text-sm font-medium">屏蔽协议</div>
                <div className="text-[11px] text-muted-foreground">
                  开启表示屏蔽对应协议，仅入口节点需要
                </div>
              </div>
              {protocolDisabled && (
                <div className="mb-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-2 text-xs text-amber-500">
                  节点未在线，等待节点上线后再设置
                </div>
              )}
              <div className="grid grid-cols-3 gap-2">
                <ProtoTile
                  label="HTTP"
                  disabled={protocolDisabled}
                  value={form.http === 1}
                  onChange={(v) => setForm({ ...form, http: v ? 1 : 0 })}
                />
                <ProtoTile
                  label="TLS"
                  disabled={protocolDisabled}
                  value={form.tls === 1}
                  onChange={(v) => setForm({ ...form, tls: v ? 1 : 0 })}
                />
                <ProtoTile
                  label="SOCKS"
                  disabled={protocolDisabled}
                  value={form.socks === 1}
                  onChange={(v) => setForm({ ...form, socks: v ? 1 : 0 })}
                />
              </div>
            </div>
            <div className="rounded-md border border-primary/30 bg-primary/5 p-3 text-xs text-muted-foreground">
              服务器 IP 是被添加节点的真实 IP，不是面板本身。入口 IP
              用于展示给用户，不确定时统一填服务器 IP。
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setFormOpen(false)}>
              取消
            </Button>
            <Button onClick={submit} disabled={saveMutation.isPending}>
              {saveMutation.isPending && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}{" "}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 命令弹窗 */}
      <Dialog open={cmdOpen} onOpenChange={setCmdOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {cmdTitle} · <span className="text-muted-foreground">{cmdNodeName}</span>
            </DialogTitle>
          </DialogHeader>
          <ScrollArea className="max-h-[50vh]">
            <pre className="whitespace-pre-wrap break-all rounded-md border border-border/60 bg-background/60 p-3 font-mono text-[11px]">
              {cmdText}
            </pre>
          </ScrollArea>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setCmdOpen(false)}>
              关闭
            </Button>
            <Button onClick={manualCopy}>
              <Copy className="mr-1.5 h-3.5 w-3.5" /> 复制命令
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 诊断弹窗 */}
      <Dialog open={diagOpen} onOpenChange={setDiagOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>节点状态检测{diagNode ? ` · ${diagNode.name}` : ""}</DialogTitle>
          </DialogHeader>
          {diagLoading ? (
            <div className="flex h-40 items-center justify-center text-muted-foreground">
              <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 检测中…
            </div>
          ) : (
            <ScrollArea className="max-h-[55vh]">
              <DiagResult data={diagResult} />
            </ScrollArea>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

/* ============ subcomponents ============ */

function NodeCard({
  node,
  busy,
  isAdmin,
  onInstall,
  onUninstall,
  onUpgrade,
  upgradeBusy,
  onEdit,
  onDelete,
  onDiagnose,
}: {
  node: Node;
  busy: boolean;
  isAdmin: boolean;
  onInstall: () => void;
  onUninstall: () => void;
  onUpgrade: () => void;
  upgradeBusy: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onDiagnose: () => void;
}) {
  const online = node.connectionStatus === "online";
  const info = node.systemInfo;
  const cpu = online && info ? info.cpuUsage : 0;
  const mem = online && info ? info.memoryUsage : 0;
  const ipList = (node.ip || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  return (
    <Card className="border-border/60 bg-card/60 transition-shadow">
      <CardContent className="space-y-4 p-4">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="truncate font-semibold">{node.name}</div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">
              {node.serverIp || "-"}
            </div>
          </div>
          <div className="flex items-center gap-1.5">
            <Badge variant="outline" className="border-border/60 font-mono text-[10px] uppercase">
              {node.forwardMode || "gost"}
            </Badge>
            <span
              className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] ${
                online ? "bg-emerald-500/10 text-emerald-400" : "bg-muted text-muted-foreground"
              }`}
            >
              <span
                className={`h-1.5 w-1.5 rounded-full ${
                  online ? "bg-emerald-400 " : "bg-muted-foreground/60"
                }`}
              />
              {online ? "在线" : "离线"}
            </span>
          </div>
        </div>

        <div className="space-y-1 text-xs">
          <Row label="入口">
            {ipList.length === 0 ? (
              "-"
            ) : ipList.length === 1 ? (
              <span className="font-mono">{ipList[0]}</span>
            ) : (
              <span className="font-mono" title={ipList.join("\n")}>
                {ipList[0]} +{ipList.length - 1}
              </span>
            )}
          </Row>
          <Row label="端口">
            <span className="font-mono">
              {node.portSta ?? "-"}-{node.portEnd ?? "-"}
            </span>
          </Row>
          <Row label="版本">
            <span className="flex min-w-0 items-center justify-end gap-1">
              <span className="truncate font-mono">{node.version || "未知"}</span>
              {node.upgradeAvailable && node.latestVersion && (
                <Badge
                  variant="outline"
                  className="shrink-0 border-amber-500/40 text-[10px] text-amber-500"
                >
                  {node.latestVersion}
                </Badge>
              )}
            </span>
          </Row>
          <Row label="开机">
            <span className="font-mono">{online && info ? formatDuration(info.uptime) : "-"}</span>
          </Row>
        </div>

        {node.syncState === "failed" && (
          <div
            className="flex items-center gap-1 text-xs text-status-critical-text"
            title={node.syncLastError}
          >
            <AlertCircle className="h-3.5 w-3.5 shrink-0" /> 规则同步失败
          </div>
        )}

        <div className="grid grid-cols-2 gap-3">
          <MetricBar
            label="CPU"
            value={cpu}
            online={online}
            display={online && info ? `${cpu.toFixed(1)}%` : "-"}
          />
          <MetricBar
            label="内存"
            value={mem}
            online={online}
            display={online && info ? `${mem.toFixed(1)}%` : "-"}
          />
        </div>

        <div className="grid grid-cols-2 gap-2 text-[11px]">
          <MiniStat
            icon={<Upload className="h-3 w-3" />}
            label="上传"
            value={online && info ? formatRate(info.uploadSpeed) : "-"}
          />
          <MiniStat
            icon={<Download className="h-3 w-3" />}
            label="下载"
            value={online && info ? formatRate(info.downloadSpeed) : "-"}
          />
          <MiniStat
            icon={<Activity className="h-3 w-3" />}
            label="↑ 累计"
            value={online && info ? formatBytes(info.uploadTraffic) : "-"}
            tone="primary"
          />
          <MiniStat
            icon={<HardDrive className="h-3 w-3" />}
            label="↓ 累计"
            value={online && info ? formatBytes(info.downloadTraffic) : "-"}
            tone="ok"
          />
        </div>

        <div className="flex items-center gap-2 pt-1">
          {isAdmin && (
            <>
              <Button
                size="sm"
                variant="outline"
                className="flex-1"
                onClick={onInstall}
                disabled={busy}
              >
                {busy ? <Loader2 className="mr-1 h-3 w-3 animate-spin" /> : null}
                安装
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="flex-1"
                onClick={onUninstall}
                disabled={busy}
              >
                卸载
              </Button>
              {node.upgradeAvailable && (
                <Button
                  size="sm"
                  variant="outline"
                  className="flex-1"
                  onClick={onUpgrade}
                  disabled={upgradeBusy || busy || !online}
                >
                  {upgradeBusy ? (
                    <Loader2 className="mr-1 h-3 w-3 animate-spin" />
                  ) : (
                    <RefreshCw className="mr-1 h-3 w-3" />
                  )}
                  升级
                </Button>
              )}
            </>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="h-8 w-8"
                aria-label={`节点操作：${node.name}`}
              >
                <MoreHorizontal className="h-4 w-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-40">
              <DropdownMenuLabel>操作</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={onDiagnose}>
                <Stethoscope className="mr-2 h-3.5 w-3.5" /> 检测状态
              </DropdownMenuItem>
              {isAdmin && (
                <>
                  <DropdownMenuItem onClick={onEdit}>
                    <Pencil className="mr-2 h-3.5 w-3.5" /> 编辑
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    onClick={onDelete}
                    className="text-status-critical-text focus:text-status-critical-text"
                  >
                    <Trash2 className="mr-2 h-3.5 w-3.5" /> 删除
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </CardContent>
    </Card>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate text-right">{children}</span>
    </div>
  );
}

function MetricBar({
  label,
  value,
  online,
  display,
}: {
  label: string;
  value: number;
  online: boolean;
  display: string;
}) {
  const tone = !online
    ? "bg-muted"
    : value > 80
      ? "bg-destructive"
      : value > 50
        ? "bg-amber-400"
        : "bg-emerald-400";
  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-[11px]">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-mono">{display}</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className={`h-full transition-all ${tone}`}
          style={{ width: `${Math.min(100, Math.max(0, value))}%` }}
        />
      </div>
    </div>
  );
}

function MiniStat({
  icon,
  label,
  value,
  tone,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  tone?: "primary" | "ok";
}) {
  const cls =
    tone === "primary"
      ? "border-primary/30 bg-primary/5 text-primary"
      : tone === "ok"
        ? "border-emerald-500/30 bg-emerald-500/5 text-emerald-400"
        : "border-border/60 bg-background/60";
  return (
    <div className={`rounded-md border ${cls} p-2 text-center`}>
      <div className="flex items-center justify-center gap-1 text-[10px] opacity-80">
        {icon} {label}
      </div>
      <div className="mt-0.5 font-mono text-[11px]">{value}</div>
    </div>
  );
}

function ProtoTile({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div
      className={`rounded-md border border-border/60 bg-background/60 p-3 ${
        disabled ? "opacity-60" : ""
      }`}
    >
      <div className="mb-2 text-xs font-medium">{label}</div>
      <div className="flex items-center justify-between text-[11px] text-muted-foreground">
        <span>{value ? "已屏蔽" : "未屏蔽"}</span>
        <Switch checked={value} disabled={disabled} onCheckedChange={onChange} />
      </div>
    </div>
  );
}

const nodeSyncLabels = {
  synced: "已同步",
  pending: "待同步",
  failed: "失败",
  unknown: "未知",
};

function DiagResult({ data }: { data: NodeStatusCheck | null }) {
  if (!data) return <div className="text-sm text-muted-foreground">暂无检测结果</div>;
  return (
    <div className="space-y-3 rounded-md border border-border p-4 text-sm">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-3">
        <dt className="text-muted-foreground">连接</dt>
        <dd>{data.connected ? "已连接" : "未连接"}</dd>
        <dt className="text-muted-foreground">响应</dt>
        <dd className="font-mono">{data.responsive ? `${data.roundTripMs} ms` : "无响应"}</dd>
        <dt className="text-muted-foreground">版本</dt>
        <dd className="break-all font-mono">
          {data.version || "未知"}
          {data.upgradeAvailable && data.latestVersion && (
            <span className="ml-2 text-muted-foreground">可升级至 {data.latestVersion}</span>
          )}
        </dd>
        <dt className="text-muted-foreground">同步</dt>
        <dd className={data.syncState === "failed" ? "text-status-critical-text" : ""}>
          {data.syncState === "failed" && <AlertCircle className="mr-1 inline h-3.5 w-3.5" />}
          {nodeSyncLabels[data.syncState]}
        </dd>
        <dt className="text-muted-foreground">检测时间</dt>
        <dd className="break-words font-mono">{formatDateTime(data.checkedAt)}</dd>
      </dl>
      {data.syncState === "failed" && data.syncLastError && (
        <div className="break-words text-xs text-status-critical-text">{data.syncLastError}</div>
      )}
      {data.message && <p className="text-xs text-muted-foreground">{data.message}</p>}
    </div>
  );
}
