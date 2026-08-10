import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import {
  Copy,
  ExternalLink,
  Import,
  KeyRound,
  Link2,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  QrCode,
  RefreshCcw,
  Search,
  Shuffle,
  Trash2,
  Wifi,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

import {
  closeProxyNodeRelay,
  deleteProxyNode,
  deleteSubscriptionProfile,
  diagnoseForward,
  getSubscriptionSettings,
  regenerateSubscriptionToken,
  updateProxyNode,
  updateSubscriptionProfile,
} from "@/lib/api";
import { queries, unwrap } from "@/lib/api/query";
import { currentPanelEndpoint } from "@/lib/panel-endpoint";
import { SubscriptionProfileDialog } from "@/components/subscription/profile-dialog";
import { SubscriptionNodeDialog } from "@/components/subscription/node-dialog";
import { SubscriptionRelayDialog } from "@/components/subscription/relay-dialog";
import { SubscriptionAssignDialog } from "@/components/subscription/assign-dialog";
import { SubscriptionQrDialog } from "@/components/subscription/qr-dialog";
import { SubscriptionImportDialog } from "@/components/subscription/import-dialog";
import { SubscriptionApiKeyDialog } from "@/components/subscription/api-key-dialog";
import { subscriptionFormatLabels, subscriptionFormats } from "@/components/subscription/formats";
import { KpiCard, PageHeader, QueryErrorNotice } from "@/components/page";
import type {
  ProfileNode,
  ProxyNode,
  SubscriptionFormat,
  SubscriptionProfile,
  SubscriptionSettings,
  SubTunnelOption,
} from "@/lib/types";

export const Route = createFileRoute("/_app/subscription")({
  head: () => ({ meta: [{ title: "订阅管理 · Flux Panel" }] }),
  component: SubscriptionPage,
});

const buildSubUrl = (token: string, format?: string) => {
  if (typeof window === "undefined") return "";
  return currentPanelEndpoint().subscriptionURL(token, format);
};

const effectiveAddress = (node: ProxyNode) =>
  node.resolvedAddress || `${node.resolvedServer || node.server}:${node.resolvedPort || node.port}`;

const protocolClass = (p: string) => {
  switch ((p || "").toLowerCase()) {
    case "vless":
      return "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
    case "vmess":
      return "bg-indigo-100 text-indigo-700 dark:bg-indigo-950 dark:text-indigo-300";
    case "trojan":
      return "bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300";
    case "snell":
      return "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "ss":
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    case "socks5":
      return "bg-cyan-100 text-cyan-700 dark:bg-cyan-950 dark:text-cyan-300";
    default:
      return "bg-muted text-muted-foreground";
  }
};

const copy = async (text: string, msg = "已复制") => {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(msg);
  } catch {
    toast.error("复制失败");
  }
};

function SubscriptionPage() {
  const queryClient = useQueryClient();

  const settingsQuery = useQuery({
    queryKey: queries.subscription.settings(),
    queryFn: () => getSubscriptionSettings().then(unwrap) as Promise<SubscriptionSettings>,
  });
  const settings = settingsQuery.data;
  const loading = settingsQuery.isPending;
  const apiKey = settings?.apiKey ?? "";
  const profiles = useMemo(() => settings?.profiles ?? [], [settings]);
  const nodes = useMemo(() => settings?.nodes ?? [], [settings]);
  const profileNodes = useMemo(() => settings?.profileNodes ?? [], [settings]);
  const tunnels = useMemo(() => settings?.tunnels ?? [], [settings]);
  const bootstrap = settings?.vlessBootstrapScript ?? "";

  const loadSettings = () =>
    queryClient.invalidateQueries({ queryKey: queries.subscription.settings() });
  const loadRelayData = () =>
    Promise.all([
      loadSettings(),
      queryClient.invalidateQueries({ queryKey: queries.forward.all }),
      queryClient.invalidateQueries({ queryKey: queries.dashboard.all }),
    ]);

  const [nodeSearch, setNodeSearch] = useState("");
  const [protocolFilter, setProtocolFilter] = useState<string>("all");

  const [profileOpen, setProfileOpen] = useState(false);
  const [editingProfile, setEditingProfile] = useState<SubscriptionProfile | null>(null);

  const [nodeOpen, setNodeOpen] = useState(false);
  const [editingNode, setEditingNode] = useState<ProxyNode | null>(null);

  const [assignOpen, setAssignOpen] = useState(false);
  const [assignNode, setAssignNode] = useState<ProxyNode | null>(null);
  const [assignInitialIds, setAssignInitialIds] = useState<number[]>([]);

  const [relayOpen, setRelayOpen] = useState(false);
  const [relayNode, setRelayNode] = useState<ProxyNode | null>(null);

  const [qrOpen, setQrOpen] = useState(false);
  const [qrTitle, setQrTitle] = useState("");
  const [qrValue, setQrValue] = useState("");

  const [importOpen, setImportOpen] = useState(false);
  const [apiKeyOpen, setApiKeyOpen] = useState(false);

  const [selectedProfileIds, setSelectedProfileIds] = useState<number[]>([]);
  const [selectedNodeIds, setSelectedNodeIds] = useState<number[]>([]);
  const [batchAction, setBatchAction] = useState<null | {
    key:
      | "profile-enable"
      | "profile-disable"
      | "profile-delete"
      | "node-enable"
      | "node-disable"
      | "node-delete";
    done: number;
    total: number;
    label: string;
  }>(null);
  const batchRunning = batchAction !== null;
  const [batchResult, setBatchResult] = useState<null | {
    label: string;
    items: { name: string; success: boolean; reason?: string }[];
  }>(null);
  const [confirmState, setConfirmState] = useState<{
    open: boolean;
    title: string;
    description: string;
    confirmText?: string;
    destructive?: boolean;
    pending?: boolean;
    onConfirm?: () => void | Promise<void>;
  }>({ open: false, title: "", description: "" });
  const askConfirm = (opts: Omit<typeof confirmState, "open" | "pending">) =>
    setConfirmState({ open: true, pending: false, ...opts });

  const profilesById = useMemo(() => {
    const m = new Map<number, SubscriptionProfile>();
    profiles.forEach((p) => m.set(p.id, p));
    return m;
  }, [profiles]);

  const nodeProfileIds = (nid: number) =>
    profileNodes
      .filter((p) => p.proxyNodeId === nid)
      .sort((a, b) => a.sort - b.sort)
      .map((p) => p.subscriptionId);
  const profileNodeIdsOf = (pid: number) =>
    profileNodes
      .filter((p) => p.subscriptionId === pid)
      .sort((a, b) => a.sort - b.sort)
      .map((p) => p.proxyNodeId);

  const protocols = useMemo(() => {
    const s = new Set<string>();
    nodes.forEach((n) => n.protocol && s.add(n.protocol.toLowerCase()));
    return Array.from(s);
  }, [nodes]);

  const filteredNodes = useMemo(() => {
    const kw = nodeSearch.trim().toLowerCase();
    return nodes.filter((n) => {
      if (protocolFilter !== "all" && (n.protocol || "").toLowerCase() !== protocolFilter)
        return false;
      if (!kw) return true;
      return [n.name, n.protocol, n.server, `${n.server}:${n.port}`, effectiveAddress(n)]
        .join(" ")
        .toLowerCase()
        .includes(kw);
    });
  }, [nodes, nodeSearch, protocolFilter]);

  const activeProfileCount = useMemo(
    () => profiles.filter((p) => p.status === 1).length,
    [profiles],
  );
  const activeNodeCount = useMemo(() => nodes.filter((n) => n.status === 1).length, [nodes]);
  const relayNodeCount = useMemo(() => nodes.filter((n) => !!n.forwardId).length, [nodes]);

  // ---------- Profile actions ----------
  const openCreateProfile = () => {
    setEditingProfile(null);
    setProfileOpen(true);
  };
  const openEditProfile = (p: SubscriptionProfile) => {
    setEditingProfile(p);
    setProfileOpen(true);
  };
  const removeProfile = async (p: SubscriptionProfile) => {
    if (!window.confirm(`删除订阅「${p.name}」？`)) return;
    const res = await deleteSubscriptionProfile(p.id);
    if (res.code === 0) {
      toast.success("订阅已删除");
      await loadSettings();
    } else toast.error(res.msg || "删除失败");
  };
  const refreshProfileToken = async (p: SubscriptionProfile) => {
    const res = await regenerateSubscriptionToken(p.id);
    if (res.code === 0) {
      toast.success("Token 已重新生成");
      await loadSettings();
    } else toast.error(res.msg || "Token 更新失败");
  };
  const toggleProfileStatus = async (p: SubscriptionProfile) => {
    const res = await updateSubscriptionProfile({
      ...p,
      status: p.status === 1 ? 0 : 1,
      nodeIds: profileNodeIdsOf(p.id),
    });
    if (res.code === 0) {
      toast.success(p.status === 1 ? "已禁用" : "已启用");
      await loadSettings();
    } else toast.error(res.msg || "更新失败");
  };
  const showQr = (p: SubscriptionProfile, fmt: SubscriptionFormat) => {
    setQrTitle(`${p.name} · ${subscriptionFormatLabels[fmt]}`);
    setQrValue(buildSubUrl(p.token, fmt));
    setQrOpen(true);
  };
  const setQrDialogOpen = (open: boolean) => {
    setQrOpen(open);
    if (!open) {
      setQrTitle("");
      setQrValue("");
    }
  };
  // ---------- Node actions ----------
  const openEditNode = (n: ProxyNode) => {
    setEditingNode(n);
    setNodeOpen(true);
  };
  const removeNode = async (n: ProxyNode) => {
    if (!window.confirm(`删除节点「${n.name}」？`)) return;
    let del = false;
    if (n.forwardId || (n.relayChildCount || 0) > 0) {
      del = window.confirm(`节点「${n.name}」有关联中转规则/节点，是否一并删除？`);
    }
    const res = await deleteProxyNode(n.id, del);
    if (res.code === 0) {
      toast.success("节点已删除");
      await (n.forwardId || (n.relayChildCount || 0) > 0 ? loadRelayData() : loadSettings());
    } else toast.error(res.msg || "删除失败");
  };
  const toggleNodeStatus = async (n: ProxyNode) => {
    const res = await updateProxyNode({
      ...n,
      port: Number(n.port),
      allowInsecure: n.allowInsecure === 1,
      udp: n.udp === 1,
      status: n.status === 1 ? 0 : 1,
    });
    if (res.code === 0) {
      toast.success(n.status === 1 ? "已禁用" : "已启用");
      await loadSettings();
    } else toast.error(res.msg || "更新失败");
  };
  const openAssign = (n: ProxyNode) => {
    setAssignNode(n);
    setAssignInitialIds(
      n.profileIds && n.profileIds.length > 0 ? n.profileIds : nodeProfileIds(n.id),
    );
    setAssignOpen(true);
  };

  // ---------- Relay ----------
  const openRelay = (n: ProxyNode) => {
    setRelayNode(n);
    setRelayOpen(true);
  };
  const closeRelay = async (n: ProxyNode) => {
    if (!window.confirm(`关闭节点「${n.name}」的中转？`)) return;
    const res = await closeProxyNodeRelay(n.id);
    if (res.code === 0) {
      const d = (res.data || {}) as any;
      toast.success(
        `已关闭：删除 ${d.deletedForwards || 0} 中转规则、${d.deletedNodes || 0} 中转节点`,
      );
      await loadRelayData();
    } else toast.error(res.msg || "关闭失败");
  };
  const diagRelay = async (n: ProxyNode) => {
    if (!n.forwardId) return toast.error("该节点没有可测试的中转规则");
    const res = await diagnoseForward(n.forwardId);
    if (res.code === 0) {
      const r = (res.data as any)?.results?.[0];
      toast.success(
        r?.success ? `连通 · ${r.averageTime?.toFixed?.(0) || "?"}ms` : r?.message || "已完成",
      );
    } else toast.error(res.msg || "测试失败");
  };

  // ---------- API Key ----------
  const openApiKey = () => {
    setApiKeyOpen(true);
  };

  // ---------- Batch ops ----------
  const runBatch = async <T,>(
    key: NonNullable<typeof batchAction>["key"],
    items: T[],
    fn: (item: T) => Promise<any>,
    label: string,
    nameOf: (item: T) => string,
  ) => {
    setBatchAction({ key, done: 0, total: items.length, label });
    const results: { name: string; success: boolean; reason?: string }[] = [];
    try {
      for (let i = 0; i < items.length; i++) {
        const name = nameOf(items[i]) || `#${i + 1}`;
        try {
          const res = await fn(items[i]);
          if (res && res.code === 0) results.push({ name, success: true });
          else
            results.push({
              name,
              success: false,
              reason: res?.msg || `错误码 ${res?.code ?? "未知"}`,
            });
        } catch (e: any) {
          results.push({ name, success: false, reason: e?.message || "请求异常" });
        }
        setBatchAction((a) => (a ? { ...a, done: i + 1 } : a));
      }
      await loadSettings();
      setBatchResult({ label, items: results });
    } finally {
      setBatchAction(null);
    }
  };

  const batchProfileStatus = (targetStatus: 0 | 1) => {
    if (batchRunning) return;
    const items = profiles.filter(
      (p) => selectedProfileIds.includes(p.id) && p.status !== targetStatus,
    );
    if (items.length === 0) return toast.info("所选订阅已是目标状态");
    const label = targetStatus === 1 ? "启用" : "禁用";
    const key = targetStatus === 1 ? "profile-enable" : "profile-disable";
    askConfirm({
      title: `批量${label}订阅`,
      description: `即将${label} ${items.length} 个订阅配置，是否继续？`,
      confirmText: label,
      onConfirm: async () => {
        await runBatch(
          key,
          items,
          (p) =>
            updateSubscriptionProfile({
              ...p,
              status: targetStatus,
              nodeIds: profileNodeIdsOf(p.id),
            }),
          `批量${label}`,
          (p) => p.name,
        );
        setSelectedProfileIds([]);
      },
    });
  };
  const batchProfileDelete = () => {
    if (batchRunning) return;
    const items = profiles.filter((p) => selectedProfileIds.includes(p.id));
    if (items.length === 0) return;
    askConfirm({
      title: "批量删除订阅",
      description: `将删除 ${items.length} 个订阅配置，删除后无法恢复，其订阅链接将立即失效。是否继续？`,
      confirmText: "删除",
      destructive: true,
      onConfirm: async () => {
        await runBatch(
          "profile-delete",
          items,
          (p) => deleteSubscriptionProfile(p.id),
          "批量删除",
          (p) => p.name,
        );
        setSelectedProfileIds([]);
      },
    });
  };

  const batchNodeStatus = (targetStatus: 0 | 1) => {
    if (batchRunning) return;
    const items = nodes.filter((n) => selectedNodeIds.includes(n.id) && n.status !== targetStatus);
    if (items.length === 0) return toast.info("所选节点已是目标状态");
    const label = targetStatus === 1 ? "启用" : "禁用";
    const key = targetStatus === 1 ? "node-enable" : "node-disable";
    askConfirm({
      title: `批量${label}节点`,
      description: `即将${label} ${items.length} 个协议节点，是否继续？`,
      confirmText: label,
      onConfirm: async () => {
        await runBatch(
          key,
          items,
          (n) =>
            updateProxyNode({
              ...n,
              port: Number(n.port),
              allowInsecure: n.allowInsecure === 1,
              udp: n.udp === 1,
              status: targetStatus,
            }),
          `批量${label}`,
          (n) => n.name,
        );
        setSelectedNodeIds([]);
      },
    });
  };
  const batchNodeDelete = () => {
    if (batchRunning) return;
    const items = nodes.filter((n) => selectedNodeIds.includes(n.id));
    if (items.length === 0) return;
    const withRelay = items.filter((n) => !!n.forwardId || (n.relayChildCount || 0) > 0).length;
    askConfirm({
      title: "批量删除节点",
      description: `将删除 ${items.length} 个协议节点${withRelay > 0 ? `（其中 ${withRelay} 个存在关联中转，将一并删除）` : ""}，是否继续？`,
      confirmText: "删除",
      destructive: true,
      onConfirm: async () => {
        await runBatch(
          "node-delete",
          items,
          (n) => deleteProxyNode(n.id, !!n.forwardId || (n.relayChildCount || 0) > 0),
          "批量删除",
          (n) => n.name,
        );
        if (withRelay > 0) {
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: queries.forward.all }),
            queryClient.invalidateQueries({ queryKey: queries.dashboard.all }),
          ]);
        }
        setSelectedNodeIds([]);
      },
    });
  };

  if (loading) {
    return (
      <div className="flex min-h-[420px] items-center justify-center text-sm text-muted-foreground">
        加载订阅管理...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        icon={<Link2 className="h-5 w-5" />}
        title="订阅管理"
        description="管理协议节点、订阅配置、中转入口与上报密钥"
        actions={
          <>
            <Button onClick={openCreateProfile}>
              <Plus className="mr-1" />
              新建订阅
            </Button>
            <Button variant="secondary" onClick={() => setImportOpen(true)}>
              <Import className="mr-1" />
              导入链接
            </Button>
            <Button variant="secondary" onClick={openApiKey}>
              <KeyRound className="mr-1" />
              API Key
            </Button>
            <Button variant="outline" onClick={loadSettings} disabled={settingsQuery.isFetching}>
              <RefreshCcw className={`mr-1 ${settingsQuery.isFetching ? "animate-spin" : ""}`} />
              刷新
            </Button>
          </>
        }
      />

      <QueryErrorNotice error={settingsQuery.error} onRetry={() => void settingsQuery.refetch()} />

      {/* KPI */}
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard
          label="订阅配置"
          value={profiles.length}
          description={`${activeProfileCount} 个启用`}
          icon={<Link2 className="h-4 w-4" />}
        />
        <KpiCard
          label="协议节点"
          value={nodes.length}
          description={`${activeNodeCount} 个启用`}
          icon={<Wifi className="h-4 w-4" />}
        />
        <KpiCard
          label="已中转节点"
          value={relayNodeCount}
          description="含中转规则的节点数"
          icon={<Shuffle className="h-4 w-4" />}
        />
        <KpiCard
          label="可用隧道"
          value={tunnels.length}
          description="可用于中转"
          icon={<KeyRound className="h-4 w-4" />}
        />
      </div>

      {/* Profiles */}
      <Card>
        <CardHeader className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <CardTitle className="text-lg">订阅配置</CardTitle>
          {profiles.length > 0 && (
            <div className="flex flex-wrap items-center gap-2">
              <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
                <Checkbox
                  checked={
                    selectedProfileIds.length > 0 && selectedProfileIds.length === profiles.length
                  }
                  disabled={batchRunning}
                  onCheckedChange={(c) => setSelectedProfileIds(c ? profiles.map((p) => p.id) : [])}
                />
                全选
              </label>
              <span className="text-xs text-muted-foreground">
                已选 {selectedProfileIds.length}
              </span>
              <Button
                size="sm"
                variant="outline"
                disabled={selectedProfileIds.length === 0 || batchRunning}
                onClick={() => batchProfileStatus(1)}
              >
                {batchAction?.key === "profile-enable" ? (
                  <>
                    <Loader2 className="mr-1 animate-spin" />
                    启用中 {batchAction.done}/{batchAction.total}
                  </>
                ) : (
                  <>
                    <Wifi className="mr-1" />
                    批量启用
                  </>
                )}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={selectedProfileIds.length === 0 || batchRunning}
                onClick={() => batchProfileStatus(0)}
              >
                {batchAction?.key === "profile-disable" ? (
                  <>
                    <Loader2 className="mr-1 animate-spin" />
                    禁用中 {batchAction.done}/{batchAction.total}
                  </>
                ) : (
                  <>
                    <XCircle className="mr-1" />
                    批量禁用
                  </>
                )}
              </Button>
              <Button
                size="sm"
                variant="destructive"
                disabled={selectedProfileIds.length === 0 || batchRunning}
                onClick={batchProfileDelete}
              >
                {batchAction?.key === "profile-delete" ? (
                  <>
                    <Loader2 className="mr-1 animate-spin" />
                    删除中 {batchAction.done}/{batchAction.total}
                  </>
                ) : (
                  <>
                    <Trash2 className="mr-1" />
                    批量删除
                  </>
                )}
              </Button>
            </div>
          )}
        </CardHeader>
        <CardContent className="space-y-3">
          {profiles.length === 0 && (
            <div className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              暂无订阅配置
            </div>
          )}
          {profiles.map((p) => {
            const url = buildSubUrl(p.token, p.defaultFormat);
            const selected = selectedProfileIds.includes(p.id);
            return (
              <div
                key={p.id}
                className={`rounded-lg border p-4 ${p.status === 1 ? "" : "opacity-60"} ${selected ? "ring-1 ring-primary" : ""}`}
              >
                <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                  <div className="flex min-w-0 flex-1 gap-3">
                    <Checkbox
                      className="mt-1"
                      aria-label={`选择订阅 ${p.name}`}
                      checked={selected}
                      onCheckedChange={(c) =>
                        setSelectedProfileIds(
                          c
                            ? [...selectedProfileIds, p.id]
                            : selectedProfileIds.filter((x) => x !== p.id),
                        )
                      }
                    />
                    <div className="min-w-0 flex-1 space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <span
                          className={`inline-block h-2 w-2 rounded-full ${p.status === 1 ? "bg-emerald-500" : "bg-muted-foreground"}`}
                        />
                        <h3 className="text-base font-semibold">{p.name}</h3>
                        <Badge variant="secondary">
                          {subscriptionFormatLabels[p.defaultFormat]}
                        </Badge>
                        <Badge variant="outline">{profileNodeIdsOf(p.id).length} 节点</Badge>
                      </div>
                      <div className="flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-2">
                        <span
                          className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground"
                          title={url}
                        >
                          {url}
                        </span>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`复制 ${p.name} 订阅链接`}
                          onClick={() => copy(url, "链接已复制")}
                        >
                          <Copy />
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`打开 ${p.name} 订阅链接`}
                          onClick={() => window.open(url, "_blank", "noopener")}
                        >
                          <ExternalLink />
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`显示 ${p.name} 订阅二维码`}
                          onClick={() => showQr(p, p.defaultFormat)}
                        >
                          <QrCode />
                        </Button>
                      </div>
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button size="sm" variant="outline" onClick={() => openEditProfile(p)}>
                      <Pencil className="mr-1 h-3.5 w-3.5" />
                      编辑
                    </Button>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button size="sm" variant="outline" aria-label={`${p.name} 更多操作`}>
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => copy(p.token, "Token 已复制")}>
                          <Copy className="mr-2" />
                          复制 Token
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => refreshProfileToken(p)}>
                          <RefreshCcw className="mr-2" />换 Token
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => toggleProfileStatus(p)}>
                          {p.status === 1 ? (
                            <>
                              <XCircle className="mr-2" />
                              禁用
                            </>
                          ) : (
                            <>
                              <Wifi className="mr-2" />
                              启用
                            </>
                          )}
                        </DropdownMenuItem>
                        {subscriptionFormats.map((f) => (
                          <DropdownMenuItem key={f} onClick={() => showQr(p, f)}>
                            <QrCode className="mr-2" />
                            {subscriptionFormatLabels[f]} 二维码
                          </DropdownMenuItem>
                        ))}
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          className="text-destructive"
                          onClick={() => removeProfile(p)}
                        >
                          <Trash2 className="mr-2" />
                          删除
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </div>
              </div>
            );
          })}
        </CardContent>
      </Card>

      {/* Proxy Nodes */}
      <Card>
        <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <CardTitle className="text-lg">协议节点</CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <Select value={protocolFilter} onValueChange={setProtocolFilter}>
              <SelectTrigger className="w-32" aria-label="筛选节点协议">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部协议</SelectItem>
                {protocols.map((p) => (
                  <SelectItem key={p} value={p}>
                    {p.toUpperCase()}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <div className="relative w-64">
              <Search className="pointer-events-none absolute left-2 top-2.5 h-4 w-4 text-muted-foreground" />
              <Input
                className="pl-8"
                placeholder="搜索节点 / 地址…"
                value={nodeSearch}
                onChange={(e) => setNodeSearch(e.target.value)}
              />
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-2">
          {filteredNodes.length > 0 && (
            <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-2">
              <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
                <Checkbox
                  checked={
                    filteredNodes.length > 0 &&
                    filteredNodes.every((n) => selectedNodeIds.includes(n.id))
                  }
                  disabled={batchRunning}
                  onCheckedChange={(c) => {
                    const ids = filteredNodes.map((n) => n.id);
                    setSelectedNodeIds(
                      c
                        ? Array.from(new Set([...selectedNodeIds, ...ids]))
                        : selectedNodeIds.filter((id) => !ids.includes(id)),
                    );
                  }}
                />
                全选（当前筛选）
              </label>
              <span className="text-xs text-muted-foreground">已选 {selectedNodeIds.length}</span>
              <div className="ml-auto flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={selectedNodeIds.length === 0 || batchRunning}
                  onClick={() => batchNodeStatus(1)}
                >
                  {batchAction?.key === "node-enable" ? (
                    <>
                      <Loader2 className="mr-1 animate-spin" />
                      启用中 {batchAction.done}/{batchAction.total}
                    </>
                  ) : (
                    <>
                      <Wifi className="mr-1" />
                      批量启用
                    </>
                  )}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={selectedNodeIds.length === 0 || batchRunning}
                  onClick={() => batchNodeStatus(0)}
                >
                  {batchAction?.key === "node-disable" ? (
                    <>
                      <Loader2 className="mr-1 animate-spin" />
                      禁用中 {batchAction.done}/{batchAction.total}
                    </>
                  ) : (
                    <>
                      <XCircle className="mr-1" />
                      批量禁用
                    </>
                  )}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={selectedNodeIds.length === 0 || batchRunning}
                  onClick={batchNodeDelete}
                >
                  {batchAction?.key === "node-delete" ? (
                    <>
                      <Loader2 className="mr-1 animate-spin" />
                      删除中 {batchAction.done}/{batchAction.total}
                    </>
                  ) : (
                    <>
                      <Trash2 className="mr-1" />
                      批量删除
                    </>
                  )}
                </Button>
              </div>
            </div>
          )}
          {filteredNodes.length === 0 && (
            <div className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              暂无节点，先配置脚本上报或导入分享链接
            </div>
          )}
          {filteredNodes.map((n) => {
            const isDerived = n.relayMode === "append" || !!n.sourceProxyNodeId;
            const hasRelay = !!n.forwardId || (n.relayChildCount || 0) > 0;
            const bound = (
              n.profileIds && n.profileIds.length > 0 ? n.profileIds : nodeProfileIds(n.id)
            )
              .map((id) => profilesById.get(id)?.name)
              .filter(Boolean);
            const selected = selectedNodeIds.includes(n.id);
            return (
              <div
                key={n.id}
                className={`flex flex-col gap-3 rounded-lg border p-3 md:flex-row md:items-center md:justify-between ${n.status === 1 ? "" : "opacity-60"} ${selected ? "ring-1 ring-primary" : ""}`}
              >
                <div className="flex min-w-0 flex-1 items-start gap-3">
                  <Checkbox
                    className="mt-1"
                    aria-label={`选择节点 ${n.name}`}
                    checked={selected}
                    onCheckedChange={(c) =>
                      setSelectedNodeIds(
                        c ? [...selectedNodeIds, n.id] : selectedNodeIds.filter((x) => x !== n.id),
                      )
                    }
                  />
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span
                        className={`inline-block h-2 w-2 rounded-full ${n.status === 1 ? "bg-emerald-500" : "bg-muted-foreground"}`}
                      />
                      <span className="font-medium">{n.name}</span>
                      <Badge className={protocolClass(n.protocol)} variant="secondary">
                        {(n.protocol || "").toUpperCase()}
                      </Badge>
                      {n.region && <Badge variant="outline">{n.region.toUpperCase()}</Badge>}
                      {isDerived && <Badge variant="outline">中转节点</Badge>}
                      {hasRelay && <Badge variant="secondary">已中转</Badge>}
                      {bound.length > 0 && <Badge variant="outline">绑定 {bound.length}</Badge>}
                    </div>
                    <div className="font-mono text-xs text-muted-foreground truncate">
                      {effectiveAddress(n)}
                    </div>
                    {n.forwardName && (
                      <div className="text-xs text-muted-foreground">
                        中转：{n.forwardName}{" "}
                        {n.forwardInIp ? `· ${n.forwardInIp}:${n.forwardInPort}` : ""}
                      </div>
                    )}
                  </div>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" variant="outline" onClick={() => openEditNode(n)}>
                    <Pencil className="mr-1 h-3.5 w-3.5" />
                    编辑
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => openAssign(n)}>
                    <Link2 className="mr-1" />
                    绑定
                  </Button>
                  {!isDerived && (
                    <Button size="sm" variant="outline" onClick={() => openRelay(n)}>
                      <Shuffle className="mr-1" />
                      中转
                    </Button>
                  )}
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button size="sm" variant="outline" aria-label={`${n.name} 更多操作`}>
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem onClick={() => copy(effectiveAddress(n), "地址已复制")}>
                        <Copy className="mr-2" />
                        复制地址
                      </DropdownMenuItem>
                      {n.link && (
                        <DropdownMenuItem onClick={() => copy(n.link!, "链接已复制")}>
                          <Copy className="mr-2" />
                          复制分享链接
                        </DropdownMenuItem>
                      )}
                      <DropdownMenuItem onClick={() => toggleNodeStatus(n)}>
                        {n.status === 1 ? (
                          <>
                            <XCircle className="mr-2" />
                            禁用
                          </>
                        ) : (
                          <>
                            <Wifi className="mr-2" />
                            启用
                          </>
                        )}
                      </DropdownMenuItem>
                      {hasRelay && (
                        <>
                          <DropdownMenuItem onClick={() => diagRelay(n)}>
                            <Wifi className="mr-2" />
                            测试中转
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => closeRelay(n)}>
                            <XCircle className="mr-2" />
                            关闭中转
                          </DropdownMenuItem>
                        </>
                      )}
                      <DropdownMenuSeparator />
                      <DropdownMenuItem className="text-destructive" onClick={() => removeNode(n)}>
                        <Trash2 className="mr-2" />
                        删除
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </div>
            );
          })}
        </CardContent>
      </Card>

      {/* Bootstrap */}
      {bootstrap && (
        <Card>
          <CardHeader>
            <CardTitle className="text-lg">节点上报脚本</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            <Textarea value={bootstrap} readOnly rows={4} className="font-mono text-xs" />
            <Button size="sm" variant="outline" onClick={() => copy(bootstrap, "脚本已复制")}>
              <Copy className="mr-1" />
              复制脚本
            </Button>
          </CardContent>
        </Card>
      )}

      {/* ---------- Profile Dialog ---------- */}
      <SubscriptionProfileDialog
        open={profileOpen}
        onOpenChange={setProfileOpen}
        editing={editingProfile}
        nodes={nodes}
        initialNodeIds={
          editingProfile ? profileNodeIdsOf(editingProfile.id) : nodes.map((n) => n.id)
        }
        onSaved={() => void loadSettings()}
      />

      {/* ---------- Node Edit Dialog ---------- */}
      <SubscriptionNodeDialog
        open={nodeOpen}
        onOpenChange={setNodeOpen}
        editing={editingNode}
        onSaved={() => void loadSettings()}
      />

      {/* ---------- Assign Dialog ---------- */}
      <SubscriptionAssignDialog
        open={assignOpen}
        onOpenChange={setAssignOpen}
        node={assignNode}
        profiles={profiles}
        initialProfileIds={assignInitialIds}
        onSaved={() => void loadSettings()}
      />

      {/* ---------- Relay Dialog ---------- */}
      <SubscriptionRelayDialog
        open={relayOpen}
        onOpenChange={setRelayOpen}
        node={relayNode}
        tunnels={tunnels}
        onSaved={() => void loadRelayData()}
      />

      {/* ---------- QR ---------- */}
      <SubscriptionQrDialog
        open={qrOpen}
        title={qrTitle}
        value={qrValue}
        onOpenChange={setQrDialogOpen}
      />

      {/* ---------- Import ---------- */}
      <SubscriptionImportDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        onSaved={() => void loadSettings()}
      />

      {/* ---------- API Key ---------- */}
      <SubscriptionApiKeyDialog
        open={apiKeyOpen}
        onOpenChange={setApiKeyOpen}
        currentKey={apiKey}
        onSaved={() => void loadSettings()}
      />

      {/* ---------- Batch Confirm ---------- */}
      <AlertDialog
        open={confirmState.open}
        onOpenChange={(o) => {
          if (confirmState.pending) return; // prevent close while running
          setConfirmState((s) => ({ ...s, open: o }));
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmState.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmState.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={confirmState.pending}>取消</AlertDialogCancel>
            <AlertDialogAction
              disabled={confirmState.pending}
              className={
                confirmState.destructive
                  ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  : ""
              }
              onClick={async (e) => {
                e.preventDefault();
                if (confirmState.pending) return;
                const fn = confirmState.onConfirm;
                setConfirmState((s) => ({ ...s, pending: true }));
                try {
                  if (fn) await fn();
                } finally {
                  setConfirmState({ open: false, title: "", description: "" });
                }
              }}
            >
              {confirmState.pending ? (
                <>
                  <Loader2 className="mr-1 animate-spin" />
                  处理中…
                </>
              ) : (
                confirmState.confirmText || "确认"
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={!!batchResult}
        onOpenChange={(o) => {
          if (!o) setBatchResult(null);
        }}
      >
        <AlertDialogContent className="max-w-lg">
          <AlertDialogHeader>
            <AlertDialogTitle>{batchResult?.label}结果</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-3">
                {batchResult &&
                  (() => {
                    const ok = batchResult.items.filter((i) => i.success).length;
                    const fail = batchResult.items.length - ok;
                    return (
                      <div className="flex items-center gap-2 text-sm">
                        <span className="rounded bg-muted px-2 py-0.5">
                          总计 {batchResult.items.length}
                        </span>
                        <span className="rounded bg-emerald-500/10 px-2 py-0.5 text-emerald-600 dark:text-emerald-400">
                          成功 {ok}
                        </span>
                        <span
                          className={`rounded px-2 py-0.5 ${fail > 0 ? "bg-destructive/10 text-destructive" : "bg-muted text-muted-foreground"}`}
                        >
                          失败 {fail}
                        </span>
                      </div>
                    );
                  })()}
                <div className="max-h-72 overflow-y-auto rounded-md border">
                  <table className="w-full text-xs">
                    <thead className="bg-muted/50 text-muted-foreground">
                      <tr>
                        <th className="px-2 py-1.5 text-left font-medium">名称</th>
                        <th className="px-2 py-1.5 text-left font-medium w-16">状态</th>
                        <th className="px-2 py-1.5 text-left font-medium">原因</th>
                      </tr>
                    </thead>
                    <tbody>
                      {batchResult?.items.map((it, idx) => (
                        <tr key={idx} className="border-t">
                          <td className="px-2 py-1.5 font-mono">{it.name}</td>
                          <td className="px-2 py-1.5">
                            {it.success ? (
                              <span className="text-emerald-600 dark:text-emerald-400">成功</span>
                            ) : (
                              <span className="text-destructive">失败</span>
                            )}
                          </td>
                          <td className="px-2 py-1.5 text-muted-foreground">
                            {it.success ? "-" : it.reason || "未知错误"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogAction onClick={() => setBatchResult(null)}>关闭</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
