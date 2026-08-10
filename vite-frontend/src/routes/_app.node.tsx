import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import {
  Activity,
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
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  checkNodeStatus,
  createNode,
  deleteNode,
  getNodeInstallCommand,
  getNodeList,
  getNodeUninstallCommand,
  upgradeNode,
  updateNode,
} from "@/lib/api";
import { isMissingPanelAddressError } from "@/lib/node-command";
import { reconnectDelay } from "@/lib/reconnect";
import { listData, queries, unwrap } from "@/lib/api/query";
import { usePreferences } from "@/hooks/use-preferences";
import { invalidateGlobalSearch } from "@/lib/search-cache";
import type { Node, NodeSystemInfo } from "@/lib/types";
import { FormField, KpiCard, PageHeader, QueryErrorNotice } from "@/components/page";

export const Route = createFileRoute("/_app/node")({
  head: () => ({ meta: [{ title: "节点管理 · Flux Panel" }] }),
  component: NodePage,
});

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

function formatBytes(bytes: number, perSec = false): string {
  if (!bytes || bytes <= 0) return perSec ? "0 B/s" : "0 B";
  const k = 1024;
  const units = perSec ? ["B/s", "KB/s", "MB/s", "GB/s", "TB/s"] : ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), units.length - 1);
  return `${(bytes / Math.pow(k, i)).toFixed(2)} ${units[i]}`;
}

function formatUptime(seconds: number): string {
  if (!seconds) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}天${h}小时`;
  if (h > 0) return `${h}小时${m}分`;
  return `${m}分钟`;
}

function numeric(obj: any, ...keys: string[]): number {
  for (const k of keys) {
    const v = obj?.[k];
    if (v === undefined || v === null || v === "") continue;
    const n = Number(v);
    if (Number.isFinite(n)) return n;
  }
  return 0;
}

function buildWsUrl(): string {
  const stored = window.localStorage.getItem("panel_address");
  const env = (import.meta as any).env?.VITE_API_BASE as string | undefined;
  const base = stored || env || window.location.origin;
  const url = new URL("/system-info", base);
  url.searchParams.set("type", "0");
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.toString();
}

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

function NodePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

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
  const [diagResult, setDiagResult] = useState<any>(null);
  const [diagNode, setDiagNode] = useState<Node | null>(null);

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttempts = useRef(0);

  const [isAdmin, setIsAdmin] = useState(false);
  useEffect(() => {
    const r = window.localStorage.getItem("role_id");
    setIsAdmin(r === "0");
  }, []);

  const { autoRefresh: autoRefreshEnabled } = usePreferences();

  /* ---- load ---- */
  const nodesQuery = useQuery({
    queryKey: queries.node.runtimeList(),
    queryFn: async () => {
      const list = await getNodeList().then(listData<Node>);
      return list.map((n) => ({
        ...n,
        connectionStatus: (n.status === 1 ? "online" : "offline") as "online" | "offline",
        systemInfo: null as NodeSystemInfo | null,
      }));
    },
    refetchInterval: autoRefreshEnabled ? 30_000 : false,
  });
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const loading = nodesQuery.isPending;

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: queries.node.list() });
  }, [queryClient]);

  const saveMutation = useMutation({
    mutationFn: ({ payload, isEdit }: { payload: Record<string, unknown>; isEdit: boolean }) =>
      (isEdit ? updateNode(payload) : createNode(payload)).then(unwrap),
    onSuccess: (_data, { isEdit }) => {
      invalidateGlobalSearch("node");
      toast.success(isEdit ? "已更新" : "已创建");
      setFormOpen(false);
      void queryClient.invalidateQueries({ queryKey: queries.node.list() });
      void queryClient.invalidateQueries({ queryKey: queries.tunnel.all });
      void queryClient.invalidateQueries({ queryKey: queries.forward.all });
      void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
    },
    onError: (error: Error) => toast.error(error.message || "保存失败"),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteNode(id).then(unwrap),
    onSuccess: () => {
      invalidateGlobalSearch("node");
      toast.success("已删除");
      void queryClient.invalidateQueries({ queryKey: queries.node.list() });
      void queryClient.invalidateQueries({ queryKey: queries.tunnel.all });
      void queryClient.invalidateQueries({ queryKey: queries.forward.all });
      void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
    },
    onError: (error: Error) => toast.error(error.message || "删除失败"),
  });

  /* ---- ws ---- */
  const closeWs = useCallback(() => {
    if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
    reconnectTimer.current = null;
    reconnectAttempts.current = 0;
    if (wsRef.current) {
      wsRef.current.onopen = null;
      wsRef.current.onmessage = null;
      wsRef.current.onerror = null;
      wsRef.current.onclose = null;
      try {
        wsRef.current.close();
      } catch {
        /* best-effort */
      }
      wsRef.current = null;
    }
  }, []);

  const handleMessage = useCallback(
    (data: any) => {
      const { id, type, data: payload } = data || {};
      if (!id) return;
      const patchNodes = (fn: (n: Node) => Node) =>
        queryClient.setQueryData<Node[]>(queries.node.runtimeList(), (prev) =>
          (prev ?? []).map(fn),
        );
      if (type === "status") {
        if (payload === 1) {
          window.setTimeout(refresh, 800);
        }
        patchNodes((n) =>
          n.id == id
            ? {
                ...n,
                connectionStatus: payload === 1 ? "online" : "offline",
                systemInfo: payload === 0 ? null : n.systemInfo,
              }
            : n,
        );
      } else if (type === "info") {
        patchNodes((n) => {
          if (n.id != id) return n;
          const info = typeof payload === "string" ? safeParse(payload) : payload;
          if (!info) return n;
          const upload = numeric(info, "bytes_transmitted", "bytesTransmitted");
          const download = numeric(info, "bytes_received", "bytesReceived");
          const uptime = numeric(info, "uptime");
          let uploadSpeed = 0;
          let downloadSpeed = 0;
          if (n.systemInfo?.uptime) {
            const td = uptime - n.systemInfo.uptime;
            if (td > 0 && td <= 10) {
              const uDiff = upload - n.systemInfo.uploadTraffic;
              const dDiff = download - n.systemInfo.downloadTraffic;
              if (uDiff >= 0 && upload >= n.systemInfo.uploadTraffic) uploadSpeed = uDiff / td;
              if (dDiff >= 0 && download >= n.systemInfo.downloadTraffic)
                downloadSpeed = dDiff / td;
            }
          }
          const next: NodeSystemInfo = {
            cpuUsage: numeric(info, "cpu_usage", "cpuUsage"),
            memoryUsage: numeric(info, "memory_usage", "memoryUsage"),
            uploadTraffic: upload,
            downloadTraffic: download,
            uploadSpeed,
            downloadSpeed,
            uptime,
          };
          return { ...n, connectionStatus: "online", systemInfo: next };
        });
      }
    },
    [queryClient, refresh],
  );

  const initWs = useCallback(() => {
    if (typeof window === "undefined") return;
    if (
      wsRef.current &&
      (wsRef.current.readyState === WebSocket.OPEN ||
        wsRef.current.readyState === WebSocket.CONNECTING)
    )
      return;
    let url: string;
    try {
      url = buildWsUrl();
    } catch {
      return;
    }
    const token = window.localStorage.getItem("token") || "";
    try {
      wsRef.current = new WebSocket(url, token ? [`jwt.${token}`] : undefined);
      wsRef.current.onopen = () => {
        reconnectAttempts.current = 0;
      };
      wsRef.current.onmessage = (ev) => {
        try {
          handleMessage(JSON.parse(ev.data));
        } catch {
          /* best-effort */
        }
      };
      wsRef.current.onerror = () => {};
      wsRef.current.onclose = () => {
        wsRef.current = null;
        // 无限重连：1s 起指数退避、±20% 抖动、30s 封顶，与节点 agent 策略一致。
        // 组件卸载或退出登录时由 closeWs 清理定时器终止循环。
        if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
        reconnectTimer.current = setTimeout(initWs, reconnectDelay(reconnectAttempts.current));
        reconnectAttempts.current++;
      };
    } catch {
      /* best-effort */
    }
  }, [handleMessage]);

  useEffect(() => {
    initWs();
    // 网络恢复或页面重新可见时立即重连，不等退避计时走完。
    const retryNow = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
      reconnectAttempts.current = 0;
      initWs();
    };
    window.addEventListener("online", retryNow);
    document.addEventListener("visibilitychange", retryNow);
    return () => {
      window.removeEventListener("online", retryNow);
      document.removeEventListener("visibilitychange", retryNow);
      closeWs();
    };
  }, [initWs, closeWs]);

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
    const payload: any = {
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
    if (!confirm(`确认删除节点「${n.name}」？该操作不可撤销。`)) return;
    deleteMutation.mutate(n.id);
  };

  /* ---- install / uninstall ---- */
  const fetchCommand = async (n: Node, kind: "install" | "uninstall") => {
    setCopyBusy(n.id);
    const api = kind === "install" ? getNodeInstallCommand : getNodeUninstallCommand;
    try {
      const res = await api(n.id, n.forwardMode || "gost");
      if (res.code !== 0 || !res.data) {
        const msg = res.msg || "获取命令失败";
        if (isMissingPanelAddressError(msg)) {
          toast.error("请先在系统配置中设置面板公网地址", {
            action: {
              label: "去设置",
              onClick: () => navigate({ to: "/config" }),
            },
          });
        } else {
          toast.error(msg);
        }
        return;
      }

      const label = kind === "install" ? "安装命令" : "卸载命令";
      const commandText = String(res.data ?? "");
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        try {
          await navigator.clipboard.writeText(commandText);
          toast.success(`${label}已复制到剪贴板`);
        } catch {
          toast.error("自动复制失败，请在弹窗中手动复制");
        }
        openCommandModal(label, commandText, n.name);
      } else {
        openCommandModal(label, commandText, n.name);
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "获取命令失败");
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
      toast.success(`${cmdTitle}已复制`);
    } catch {
      toast.error("复制失败，请手动选择文本");
    }
  };

  const runUpgrade = async (n: Node) => {
    if (!n.upgradeAvailable) return;
    const latest = n.latestVersion || "最新版本";
    if (!confirm(`确认升级节点「${n.name}」到 ${latest}？节点会自动重启并短暂离线。`)) return;
    setUpgradeBusy(n.id);
    try {
      const res = await upgradeNode(n.id);
      if (res.code !== 0) {
        toast.error(res.msg || "升级失败");
        return;
      }
      toast.success("升级已触发，节点稍后会自动重连");
      window.setTimeout(refresh, 3000);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "升级失败");
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
      const res = await checkNodeStatus(n.id);
      if (res.code !== 0) toast.error(res.msg || "检测失败");
      setDiagResult(res.data ?? res);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "检测失败");
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
              <Button size="sm" className="shadow-glow" onClick={openAdd}>
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
              <div className="grid grid-cols-2 gap-2">
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
            <Button onClick={submit} disabled={saveMutation.isPending} className="shadow-glow">
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
            <Button onClick={manualCopy} className="shadow-glow">
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
    <Card className="border-border/60 bg-card/60 transition-shadow hover:shadow-glow">
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
                  online
                    ? "bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.8)]"
                    : "bg-muted-foreground/60"
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
            <span className="font-mono">{online && info ? formatUptime(info.uptime) : "-"}</span>
          </Row>
        </div>

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
            value={online && info ? formatBytes(info.uploadSpeed, true) : "-"}
          />
          <MiniStat
            icon={<Download className="h-3 w-3" />}
            label="下载"
            value={online && info ? formatBytes(info.downloadSpeed, true) : "-"}
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
                    className="text-destructive focus:text-destructive"
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

function DiagResult({ data }: { data: any }) {
  if (!data) return <div className="text-sm text-muted-foreground">暂无数据</div>;
  // 后端返回结构可能是 { nodes: [{id, name, status, latency, message}], ...}
  const list: any[] = Array.isArray(data)
    ? data
    : Array.isArray(data.nodes)
      ? data.nodes
      : Array.isArray(data.results)
        ? data.results
        : [];
  if (list.length > 0) {
    return (
      <div className="space-y-2">
        {list.map((r, i) => {
          const ok = r.status === 1 || r.success === true || r.online === true;
          return (
            <div
              key={i}
              className={`rounded-md border p-3 text-sm ${
                ok
                  ? "border-emerald-500/30 bg-emerald-500/5"
                  : "border-destructive/40 bg-destructive/5"
              }`}
            >
              <div className="flex items-center justify-between">
                <div className="font-medium">{r.name || r.nodeName || `节点 #${r.id}`}</div>
                <Badge variant={ok ? "default" : "destructive"}>{ok ? "在线" : "离线"}</Badge>
              </div>
              <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                {r.serverIp || r.ip || ""}
                {typeof r.latency === "number" && ` · ${r.latency}ms`}
                {r.version && ` · v${r.version}`}
              </div>
              {r.message && (
                <div className="mt-1 text-[12px] text-muted-foreground">{r.message}</div>
              )}
            </div>
          );
        })}
      </div>
    );
  }
  // 单节点返回：{ status: 1|0, latency, ...}
  const single = data as any;
  const ok = single.status === 1 || single.online === true || single.success === true;
  return (
    <div
      className={`rounded-md border p-4 text-sm ${
        ok ? "border-emerald-500/30 bg-emerald-500/5" : "border-destructive/40 bg-destructive/5"
      }`}
    >
      <div className="flex items-center justify-between">
        <div className="font-medium">检测结果</div>
        <Badge variant={ok ? "default" : "destructive"}>{ok ? "在线" : "离线"}</Badge>
      </div>
      <pre className="mt-2 whitespace-pre-wrap break-all font-mono text-[11px] text-muted-foreground">
        {JSON.stringify(single, null, 2)}
      </pre>
    </div>
  );
}

function safeParse(s: string) {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}
