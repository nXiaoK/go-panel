import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent } from "react";
import { toast } from "sonner";
import {
  Activity,
  ArrowDownRight,
  ArrowUpRight,
  CalendarDays,
  Circle,
  Cpu,
  Gauge,
  Globe2,
  Info,
  Loader2,
  RefreshCw,
  ServerCog,
  Stethoscope,
  Users,
  Waypoints,
  Zap,
} from "lucide-react";

import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { speedTestTunnel } from "@/lib/api";
import { queries } from "@/lib/api/query";
import {
  buildTrafficSeries,
  getTrafficTrendState,
  pickTrafficHoverPoint,
  resolveTrafficTotalBytes,
  trafficRangeMeta,
  trafficTrendEmptyDescription,
  trafficTunnelLabel,
  trafficTunnelRequestId,
  type TrafficHoverPoint,
  type TrafficRange,
  type TrafficTunnelOption,
} from "@/lib/dashboard-traffic";
import {
  formatMissingSources,
  healthBadge,
  loadDashboardSources,
  type DashboardHealthStatus,
} from "@/lib/dashboard-load";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { buildPanelWsUrl, createSpeedTestId } from "@/lib/panel-ws";
import { formatLatencyMs, formatLossPercent, formatSpeedRateFromMbps } from "@/lib/speed-rate";

export const Route = createFileRoute("/_app/")({
  head: () => ({
    meta: [
      { title: "控制台 · Flux Panel" },
      { name: "description", content: "Flux Panel 控制台：实时流量、节点状态与最近活动。" },
    ],
  }),
  component: Dashboard,
});

const toneMap = {
  success: "text-success bg-success/10 border-success/30",
  warning: "text-warning bg-warning/10 border-warning/30",
  danger: "text-destructive bg-destructive/10 border-destructive/30",
  info: "text-primary bg-primary/10 border-primary/30",
};

function bytesToGb(value: number) {
  if (!value || value <= 0) return 0;
  return value / 1024 / 1024 / 1024;
}

function formatTraffic(bytes: number) {
  const gb = bytesToGb(bytes);
  if (gb >= 1024) return { value: (gb / 1024).toFixed(2), suffix: "TB" };
  return { value: gb.toFixed(gb >= 10 ? 1 : 2), suffix: "GB" };
}

function formatTrafficGb(value: number) {
  const gb = Number(value || 0);
  return `${gb.toFixed(gb >= 10 ? 2 : 3)} GB`;
}

function formatEventTime(value?: number | string) {
  if (!value) return "刚刚";
  const ts = typeof value === "number" ? value : Date.parse(value);
  if (!Number.isFinite(ts)) return "刚刚";
  return new Date(ts).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

function timeGreeting(date = new Date()) {
  const hour = date.getHours();
  if (hour >= 5 && hour < 12) return "早上好";
  if (hour >= 12 && hour < 18) return "下午好";
  return "晚上好";
}

function chartPointY(value: number, max: number) {
  const safeMax = Math.max(1, Number(max || 0));
  const y = 92 - (Number(value || 0) / safeMax) * 78;
  return Math.max(8, Math.min(92, y));
}

function makeChartPath(data: Array<{ down: number; up: number }>, key: "down" | "up", max: number) {
  if (data.length === 0) return "";
  return data
    .map((item, index) => {
      const x = data.length === 1 ? 0 : (index / (data.length - 1)) * 100;
      return `${index === 0 ? "M" : "L"} ${x.toFixed(2)} ${chartPointY(item[key], max).toFixed(2)}`;
    })
    .join(" ");
}

function makeAreaPath(linePath: string) {
  if (!linePath) return "";
  return `${linePath} L 100 96 L 0 96 Z`;
}

function stressTunnelIdOf(tunnel: any) {
  return Number(tunnel?.tunnelId ?? tunnel?.id ?? 0);
}

function Dashboard() {
  const navigate = useNavigate();
  const [trafficRange, setTrafficRange] = useState<TrafficRange>("24h");
  const [trafficTunnelId, setTrafficTunnelId] = useState("all");
  const [trafficTunnelOptions, setTrafficTunnelOptions] = useState<TrafficTunnelOption[]>([]);
  const [trafficHover, setTrafficHover] = useState<TrafficHoverPoint | null>(null);
  const [stressOpen, setStressOpen] = useState(false);
  const [stressTunnelId, setStressTunnelId] = useState("");
  const [stressDirection, setStressDirection] = useState("in-to-out");
  const [stressDuration, setStressDuration] = useState(10);
  const [stressParallel, setStressParallel] = useState(1);
  const [stressPort, setStressPort] = useState("");
  const [stressLoading, setStressLoading] = useState(false);
  const [stressResult, setStressResult] = useState<any>(null);
  const [stressTestId, setStressTestId] = useState("");
  const [stressProgress, setStressProgress] = useState<any[]>([]);
  const speedWsRef = useRef<WebSocket | null>(null);
  const activeStressTestIdRef = useRef("");

  const isAdmin = typeof window !== "undefined" && window.localStorage.getItem("role_id") === "0";

  const selectedTrafficTunnelId = trafficTunnelRequestId(trafficTunnelId);

  const dashboardQuery = useQuery({
    queryKey: queries.dashboard.sources(isAdmin, trafficRange, selectedTrafficTunnelId),
    queryFn: () => loadDashboardSources(isAdmin, trafficRange, selectedTrafficTunnelId),
  });
  const { refetch: refetchDashboard } = dashboardQuery;
  const dashboardResult = dashboardQuery.data ?? null;
  const dashboardLoading = dashboardQuery.isPending;
  const dashboardRefreshing = dashboardQuery.isFetching;
  const packageData = useMemo(
    () => (dashboardResult?.packageData ?? null) as any,
    [dashboardResult],
  );
  useEffect(() => {
    if (Array.isArray(packageData?.trafficTunnels)) {
      setTrafficTunnelOptions(packageData.trafficTunnels as TrafficTunnelOption[]);
    }
  }, [packageData?.trafficTunnels]);
  const adminNodes = useMemo(() => (dashboardResult?.nodes ?? []) as any[], [dashboardResult]);
  const adminTunnels = useMemo(() => (dashboardResult?.tunnels ?? []) as any[], [dashboardResult]);
  const adminForwards = useMemo(
    () => (dashboardResult?.forwards ?? []) as any[],
    [dashboardResult],
  );
  const adminUsers = useMemo(() => (dashboardResult?.users ?? []) as any[], [dashboardResult]);
  const lastSync = dashboardResult?.completedAt ?? null;
  const healthStatus: DashboardHealthStatus = dashboardResult?.status ?? "error";
  const dashboardError = useMemo(() => {
    if (dashboardQuery.error) {
      return dashboardQuery.error instanceof Error
        ? dashboardQuery.error.message
        : "控制台数据加载失败";
    }
    if (!dashboardResult) return "";
    if (dashboardResult.status === "success") return "";
    if (dashboardResult.missing.length > 0) {
      return `缺失：${formatMissingSources(dashboardResult.missing)}`;
    }
    return Object.values(dashboardResult.errors).filter(Boolean)[0] || "控制台数据加载失败";
  }, [dashboardQuery.error, dashboardResult]);

  const refreshDashboard = useCallback(async () => {
    const result = await refetchDashboard();
    const data = result.data;
    if (data?.status === "success") {
      toast.success("控制台数据已刷新");
    } else if (data?.status === "partial") {
      toast.warning(`部分数据未同步：${formatMissingSources(data.missing)}`);
    } else {
      toast.error(Object.values(data?.errors ?? {}).filter(Boolean)[0] || "控制台数据加载失败");
    }
  }, [refetchDashboard]);

  const closeSpeedWs = useCallback(() => {
    if (!speedWsRef.current) return;
    speedWsRef.current.onopen = null;
    speedWsRef.current.onmessage = null;
    speedWsRef.current.onerror = null;
    speedWsRef.current.onclose = null;
    try {
      speedWsRef.current.close();
    } catch {
      /* best-effort */
    }
    speedWsRef.current = null;
  }, []);

  const connectSpeedWs = useCallback(() => {
    if (typeof window === "undefined") return;
    if (
      speedWsRef.current &&
      (speedWsRef.current.readyState === WebSocket.OPEN ||
        speedWsRef.current.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }
    let url: string;
    try {
      url = buildPanelWsUrl();
    } catch {
      return;
    }
    const token = window.localStorage.getItem("token") || "";
    try {
      const ws = new WebSocket(url, token ? [`jwt.${token}`] : undefined);
      speedWsRef.current = ws;
      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          if (msg?.type !== "speed-test-progress") return;
          const progress = msg.data || {};
          if (!activeStressTestIdRef.current || progress.testId !== activeStressTestIdRef.current)
            return;
          setStressProgress((prev) => [...prev, progress].slice(-120));
        } catch {
          /* best-effort */
        }
      };
      ws.onclose = () => {
        if (speedWsRef.current === ws) speedWsRef.current = null;
      };
      ws.onerror = () => {};
    } catch {
      /* best-effort */
    }
  }, []);

  useEffect(() => {
    return () => closeSpeedWs();
  }, [closeSpeedWs]);

  useEffect(() => {
    if (stressOpen) connectSpeedWs();
  }, [connectSpeedWs, stressOpen]);

  const userInfo = packageData?.userInfo || {};
  const packageTunnels = useMemo(
    () => packageData?.tunnelPermissions || [],
    [packageData?.tunnelPermissions],
  );
  const packageForwards = useMemo(() => packageData?.forwards || [], [packageData?.forwards]);
  const statisticsFlows = useMemo(
    () => packageData?.statisticsFlows || [],
    [packageData?.statisticsFlows],
  );
  const stressTunnels = useMemo(
    () => (isAdmin ? adminTunnels.filter((t) => t.type === 2) : []),
    [adminTunnels, isAdmin],
  );
  const selectedStressTunnel = useMemo(
    () => stressTunnels.find((t: any) => stressTunnelIdOf(t) === Number(stressTunnelId)),
    [stressTunnelId, stressTunnels],
  );
  const stressHasRelay = Boolean(selectedStressTunnel?.relayNodeId);
  const visibleForwards = isAdmin && adminForwards.length > 0 ? adminForwards : packageForwards;
  const legacyForwardTrafficBytes = visibleForwards.reduce(
    (sum: number, f: any) => sum + Number(f.inFlow || 0) + Number(f.outFlow || 0),
    0,
  );
  const legacyUserTrafficBytes = Number(userInfo.inFlow || 0) + Number(userInfo.outFlow || 0);
  const legacyTrafficBytes =
    legacyForwardTrafficBytes > 0 ? legacyForwardTrafficBytes : legacyUserTrafficBytes;
  const trafficBytes = resolveTrafficTotalBytes(packageData?.trafficTotals, legacyTrafficBytes);
  const todayTrafficBytes = resolveTrafficTotalBytes(packageData?.todayTraffic);
  const traffic = formatTraffic(trafficBytes);
  const todayTraffic = formatTraffic(todayTrafficBytes);
  const trafficScope =
    packageData?.trafficScope === "system"
      ? "system"
      : packageData?.trafficScope === "user"
        ? "user"
        : isAdmin
          ? "system"
          : "user";
  const trafficScopeLabel = trafficScope === "system" ? "全系统" : "当前账号";
  const selectedTrafficTunnel = useMemo(
    () =>
      selectedTrafficTunnelId === undefined
        ? null
        : trafficTunnelOptions.find(
            (option) => Number(option.tunnelId) === selectedTrafficTunnelId,
          ) || null,
    [selectedTrafficTunnelId, trafficTunnelOptions],
  );
  const selectedTrafficTunnelLabel =
    selectedTrafficTunnelId === undefined
      ? trafficScopeLabel
      : selectedTrafficTunnel
        ? trafficTunnelLabel(selectedTrafficTunnel)
        : `隧道 #${selectedTrafficTunnelId}`;
  const trafficTrendTotal = formatTraffic(
    resolveTrafficTotalBytes(packageData?.trafficTrendTotals),
  );

  const kpis = useMemo(() => {
    const activeTunnels = isAdmin
      ? adminTunnels.filter((t) => (t.status ?? 1) === 1).length
      : packageTunnels.length;
    const onlineNodes = adminNodes.filter((n) => n.status === 1).length;
    const activeUsers = isAdmin
      ? adminUsers.filter((u) => (u.status ?? 1) === 1).length
      : packageForwards.filter((f: any) => (f.status ?? 1) === 1).length;

    return [
      {
        label: isAdmin ? "活跃隧道" : "可用隧道",
        value: String(activeTunnels),
        delta: isAdmin ? `${adminTunnels.length} total` : `${packageTunnels.length} total`,
        up: true,
        icon: Waypoints,
        hint: "实时",
        description: isAdmin
          ? "状态为启用的隧道数量，来自后端隧道列表。"
          : "当前账号拥有权限的隧道数量。",
      },
      {
        label: "在线节点",
        value: String(onlineNodes),
        suffix: adminNodes.length ? `/ ${adminNodes.length}` : "",
        delta: adminNodes.length ? `${Math.round((onlineNodes / adminNodes.length) * 100)}%` : "—",
        up: true,
        icon: ServerCog,
        hint: "可用率",
        description: "已连接并上报心跳的节点数量，以及全部节点中的在线占比。",
      },
      {
        label: "累计流量",
        value: traffic.value,
        suffix: traffic.suffix,
        delta: "实时",
        up: true,
        icon: Activity,
        hint: trafficScopeLabel,
        description:
          trafficScope === "system"
            ? "全系统范围内累计产生的上下行流量，由后端统一汇总。"
            : "当前账号累计产生的上下行流量，由后端统一汇总。",
      },
      {
        label: "今日流量",
        value: todayTraffic.value,
        suffix: todayTraffic.suffix,
        delta: "今日",
        up: true,
        icon: CalendarDays,
        hint: trafficScopeLabel,
        description:
          trafficScope === "system"
            ? "今天 00:00 至今全系统产生的上下行流量。"
            : "今天 00:00 至今当前账号产生的上下行流量。",
      },
      {
        label: isAdmin ? "活跃用户" : "运行转发",
        value: String(activeUsers),
        delta: isAdmin ? `${adminUsers.length} total` : `${packageForwards.length} total`,
        up: true,
        icon: Users,
        hint: isAdmin ? "账号状态" : "我的转发",
        description: isAdmin ? "状态正常的用户数量。" : "当前账号下状态为运行中的转发数量。",
      },
    ];
  }, [
    adminNodes,
    adminTunnels,
    adminUsers,
    isAdmin,
    packageForwards,
    packageTunnels,
    todayTraffic.suffix,
    todayTraffic.value,
    traffic.suffix,
    traffic.value,
    trafficScope,
    trafficScopeLabel,
  ]);

  const trafficData = useMemo(
    () => buildTrafficSeries(statisticsFlows, trafficRange),
    [statisticsFlows, trafficRange],
  );
  const trafficTrendState = useMemo(
    () => getTrafficTrendState(trafficData, dashboardLoading),
    [dashboardLoading, trafficData],
  );

  const trafficMax = useMemo(
    () => Math.max(1, ...trafficData.flatMap((item) => [item.down, item.up])),
    [trafficData],
  );
  const downPath = useMemo(
    () => makeChartPath(trafficData, "down", trafficMax),
    [trafficData, trafficMax],
  );
  const upPath = useMemo(
    () => makeChartPath(trafficData, "up", trafficMax),
    [trafficData, trafficMax],
  );
  const handleTrafficPointerMove = useCallback(
    (event: PointerEvent<HTMLDivElement>) => {
      const rect = event.currentTarget.getBoundingClientRect();
      setTrafficHover(pickTrafficHoverPoint(trafficData, event.clientX - rect.left, rect.width));
    },
    [trafficData],
  );
  const clearTrafficHover = useCallback(() => setTrafficHover(null), []);
  const latestStressProgress = stressProgress[stressProgress.length - 1] || null;
  const latestStressRate = formatSpeedRateFromMbps(latestStressProgress?.mbps || 0);
  const latestStressLatency = formatLatencyMs(latestStressProgress?.latencyMs);
  const latestStressLoss = formatLossPercent(latestStressProgress?.lossPercent);
  const sentStressRate = formatSpeedRateFromMbps(stressResult?.summary?.sentMbps || 0);
  const receivedStressRate = formatSpeedRateFromMbps(stressResult?.summary?.receivedMbps || 0);
  const finalStressLatency = formatLatencyMs(stressResult?.summary?.latencyMs);
  const finalStressLoss = formatLossPercent(stressResult?.summary?.lossPercent);
  const liveStressPercent = latestStressProgress
    ? Math.min(
        100,
        Math.round(
          (Number(latestStressProgress.endSeconds || 0) / Math.max(1, stressDuration)) * 100,
        ),
      )
    : 0;

  const nodeCards = useMemo(
    () =>
      adminNodes.slice(0, 6).map((n) => ({
        name: n.name,
        mode: n.forwardMode || "gost",
        version: n.version || "",
        upgradeAvailable: Boolean(n.upgradeAvailable),
        status: n.status === 1 ? "online" : "offline",
      })),
    [adminNodes],
  );

  const events = useMemo(() => {
    const rows: Array<{ time: string; type: string; text: string; tone: keyof typeof toneMap }> =
      [];
    for (const n of adminNodes.filter((node) => node.status !== 1).slice(0, 2)) {
      rows.push({ time: "实时", type: "node", text: `节点 ${n.name} 当前离线`, tone: "danger" });
    }
    for (const f of visibleForwards.slice(0, 4)) {
      rows.push({
        time: formatEventTime(f.createdTime),
        type: "forward",
        text: `转发 ${f.name} ${f.status === 1 ? "运行中" : "已暂停"}`,
        tone: f.status === 1 ? "success" : "warning",
      });
    }
    for (const t of adminTunnels.slice(0, 2)) {
      rows.push({
        time: formatEventTime(t.createdTime),
        type: "tunnel",
        text: `隧道 ${t.name} ${t.status === 0 ? "已禁用" : "可用"}`,
        tone: t.status === 0 ? "warning" : "info",
      });
    }
    if (rows.length === 0) {
      rows.push({ time: "实时", type: "system", text: "暂无最近活动", tone: "info" });
    }
    return rows.slice(0, 6);
  }, [adminNodes, adminTunnels, visibleForwards]);

  const displayName =
    typeof window !== "undefined"
      ? window.localStorage.getItem("name") || userInfo.user || "admin"
      : "admin";

  useEffect(() => {
    if (!stressOpen || stressTunnelId || stressTunnels.length === 0) return;
    setStressTunnelId(String(stressTunnelIdOf(stressTunnels[0])));
    setStressDirection(stressTunnels[0]?.relayNodeId ? "in-to-relay" : "in-to-out");
  }, [stressOpen, stressTunnelId, stressTunnels]);

  const openStressTest = () => {
    setStressResult(null);
    setStressProgress([]);
    setStressOpen(true);
    connectSpeedWs();
    if (!stressTunnelId && stressTunnelIdOf(stressTunnels[0])) {
      setStressTunnelId(String(stressTunnelIdOf(stressTunnels[0])));
      setStressDirection(stressTunnels[0]?.relayNodeId ? "in-to-relay" : "in-to-out");
    }
  };

  const runStressTest = async () => {
    const tunnelId = Number(stressTunnelId);
    if (!tunnelId) return toast.error("请选择要测试的隧道");
    const testId = createSpeedTestId();
    activeStressTestIdRef.current = testId;
    setStressTestId(testId);
    setStressProgress([]);
    connectSpeedWs();
    setStressLoading(true);
    setStressResult(null);
    try {
      const res = await speedTestTunnel({
        tunnelId,
        testId,
        direction: stressDirection,
        durationSeconds: stressDuration,
        parallel: stressParallel,
        port: Number(stressPort) || 0,
      });
      if (res.code === 0) {
        setStressResult(res.data);
        toast.success("压力测试完成");
      } else {
        toast.error(res.msg || "压力测试失败");
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "压力测试失败");
    } finally {
      setStressLoading(false);
    }
  };

  const badge = healthBadge(dashboardLoading ? "partial" : healthStatus);
  const badgeDotClass =
    badge.tone === "success"
      ? "fill-success text-success"
      : badge.tone === "warning"
        ? "fill-warning text-warning"
        : "fill-destructive text-destructive";

  return (
    <div className="space-y-6">
      {/* Hero */}
      <div className="relative overflow-hidden rounded-xl border border-border bg-card p-6 shadow-card">
        <div className="bg-grid pointer-events-none absolute inset-0 opacity-40" />
        <div className="relative flex flex-col justify-between gap-6 lg:flex-row lg:items-end">
          <div>
            <div className="flex items-center gap-2">
              <Badge
                variant="outline"
                className={`font-mono text-[10px] uppercase tracking-widest ${badge.className}`}
              >
                <Circle className={`mr-1 h-1.5 w-1.5 ${badgeDotClass}`} />
                {dashboardLoading ? "syncing" : badge.label}
              </Badge>
              <span className="font-mono text-[10px] text-muted-foreground">
                {dashboardLoading
                  ? "syncing · 数据同步中"
                  : healthStatus === "success" && lastSync
                    ? `last sync · ${lastSync.toLocaleTimeString("zh-CN")}`
                    : healthStatus === "partial"
                      ? "partial · 部分数据可用"
                      : "last sync · 等待同步"}
              </span>
              {dashboardError ? (
                <span
                  className="max-w-[220px] truncate font-mono text-[10px] text-destructive"
                  title={dashboardError}
                >
                  sync issue · {dashboardError}
                </span>
              ) : null}
            </div>
            <h1 className="mt-3 text-3xl font-semibold tracking-tight lg:text-4xl">
              {timeGreeting()}，<span className="text-gradient-primary">{displayName}</span>
            </h1>
            <p className="mt-2 max-w-xl text-sm text-muted-foreground">
              {isAdmin
                ? `${adminTunnels.filter((t) => (t.status ?? 1) === 1).length} 条隧道可用，${adminNodes.filter((n) => n.status === 1).length} 个节点在线。`
                : `${packageTunnels.length} 条隧道权限，${packageForwards.filter((f: any) => (f.status ?? 1) === 1).length} 条转发运行中。`}
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              className="border-border bg-card/60"
              onClick={() => void refreshDashboard()}
              disabled={dashboardRefreshing}
              title="刷新控制台数据"
            >
              <RefreshCw className={`mr-2 h-4 w-4 ${dashboardRefreshing ? "animate-spin" : ""}`} />
              刷新
            </Button>
            <Button variant="outline" className="border-border bg-card/60" onClick={openStressTest}>
              <Gauge className="mr-2 h-4 w-4" />
              压力测试
            </Button>
            <Button
              className="bg-gradient-primary text-primary-foreground shadow-glow hover:opacity-90"
              onClick={() => navigate({ to: "/tunnel", search: { action: "create" } })}
            >
              <Zap className="mr-2 h-4 w-4" />
              新建隧道
            </Button>
          </div>
        </div>
      </div>

      {/* KPI grid */}
      <TooltipProvider delayDuration={120}>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
          {kpis.map((k) => (
            <Card
              key={k.label}
              className="group relative overflow-hidden border-border bg-card p-5 shadow-card transition hover:border-primary/40 hover:shadow-glow"
            >
              <div className="pointer-events-none absolute -right-6 -top-6 h-24 w-24 rounded-full bg-primary/10 opacity-0 blur-2xl transition group-hover:opacity-100" />
              <div className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-1.5">
                    <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                      {k.label}
                    </p>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <button
                          type="button"
                          className="rounded-sm text-muted-foreground transition hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          aria-label={`${k.label}说明`}
                        >
                          <Info className="h-3.5 w-3.5" />
                        </button>
                      </TooltipTrigger>
                      <TooltipContent side="top" className="max-w-64 text-xs leading-relaxed">
                        {k.description}
                      </TooltipContent>
                    </Tooltip>
                  </div>
                  <div className="mt-3 flex items-baseline gap-1.5">
                    <span className="font-mono text-3xl font-semibold tracking-tight text-foreground">
                      {k.value}
                    </span>
                    {k.suffix && (
                      <span className="font-mono text-sm text-muted-foreground">{k.suffix}</span>
                    )}
                  </div>
                  <div className="mt-2 flex items-center gap-1.5">
                    <span
                      className={`inline-flex items-center gap-0.5 rounded-sm px-1 py-0.5 font-mono text-[10px] font-medium ${
                        k.up ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive"
                      }`}
                    >
                      {k.up ? (
                        <ArrowUpRight className="h-3 w-3" />
                      ) : (
                        <ArrowDownRight className="h-3 w-3" />
                      )}
                      {k.delta}
                    </span>
                    <span className="text-[11px] text-muted-foreground">{k.hint}</span>
                  </div>
                </div>
                <div className="flex h-9 w-9 items-center justify-center rounded-md border border-border bg-muted/40 text-primary">
                  <k.icon className="h-4 w-4" />
                </div>
              </div>
            </Card>
          ))}
        </div>
      </TooltipProvider>

      {/* Traffic chart */}
      <Card className="border-border bg-card p-6 shadow-card">
        <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-base font-semibold tracking-tight">流量趋势</h2>
              {dashboardLoading ? (
                <span className="inline-flex items-center gap-1 rounded-sm bg-primary/10 px-1.5 py-0.5 font-mono text-[10px] text-primary">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  syncing
                </span>
              ) : null}
            </div>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {trafficRangeMeta[trafficRange].description} · {selectedTrafficTunnelLabel}
              {selectedTrafficTunnelId !== undefined ? " · 仅含功能升级后记录" : ""}
            </p>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-3">
            <Select
              value={trafficTunnelId}
              onValueChange={(value) => {
                setTrafficHover(null);
                setTrafficTunnelId(value);
              }}
            >
              <SelectTrigger
                className="h-8 w-[min(18rem,70vw)] font-mono text-xs"
                aria-label="筛选流量趋势隧道"
              >
                <SelectValue placeholder="选择隧道" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部隧道</SelectItem>
                {trafficTunnelOptions.map((option) => (
                  <SelectItem key={option.tunnelId} value={String(option.tunnelId)}>
                    {trafficTunnelLabel(option)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {selectedTrafficTunnelId !== undefined && packageData && !dashboardLoading ? (
              <span className="rounded-md border border-border bg-muted/30 px-2.5 py-1 font-mono text-[11px] text-foreground">
                区间合计 {trafficTrendTotal.value} {trafficTrendTotal.suffix}
              </span>
            ) : null}
            <div className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full bg-primary shadow-glow" />
              <span className="font-mono text-xs text-muted-foreground">下行</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full bg-chart-2" />
              <span className="font-mono text-xs text-muted-foreground">上行</span>
            </div>
            <div className="flex overflow-hidden rounded-md border border-border">
              {(Object.keys(trafficRangeMeta) as TrafficRange[]).map((range) => (
                <button
                  key={range}
                  type="button"
                  aria-pressed={trafficRange === range}
                  onClick={() => {
                    setTrafficHover(null);
                    setTrafficRange(range);
                  }}
                  className={`px-3 py-1 font-mono text-[11px] uppercase transition ${
                    trafficRange === range
                      ? "bg-primary/15 text-primary"
                      : "text-muted-foreground hover:bg-muted/40"
                  }`}
                >
                  {trafficRangeMeta[range].label}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div
          className="relative flex h-72 w-full cursor-crosshair flex-col overflow-hidden rounded-md border border-border/60 bg-muted/10"
          onPointerMove={handleTrafficPointerMove}
          onPointerLeave={clearTrafficHover}
        >
          <svg
            className="min-h-0 flex-1"
            viewBox="0 0 100 100"
            preserveAspectRatio="none"
            role="img"
            aria-label={`${trafficRangeMeta[trafficRange].description} · ${selectedTrafficTunnelLabel}`}
          >
            <defs>
              <linearGradient id="downFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="oklch(0.72 0.19 240)" stopOpacity="0.45" />
                <stop offset="100%" stopColor="oklch(0.72 0.19 240)" stopOpacity="0" />
              </linearGradient>
              <linearGradient id="upFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="oklch(0.74 0.16 165)" stopOpacity="0.35" />
                <stop offset="100%" stopColor="oklch(0.74 0.16 165)" stopOpacity="0" />
              </linearGradient>
            </defs>
            {[20, 40, 60, 80].map((y) => (
              <line
                key={y}
                x1="0"
                x2="100"
                y1={y}
                y2={y}
                vectorEffect="non-scaling-stroke"
                stroke="oklch(1 0 0 / 7%)"
              />
            ))}
            <path d={makeAreaPath(downPath)} fill="url(#downFill)" />
            <path d={makeAreaPath(upPath)} fill="url(#upFill)" />
            <path
              d={downPath}
              fill="none"
              stroke="oklch(0.72 0.19 240)"
              strokeWidth="2"
              vectorEffect="non-scaling-stroke"
            />
            <path
              d={upPath}
              fill="none"
              stroke="oklch(0.74 0.16 165)"
              strokeWidth="2"
              vectorEffect="non-scaling-stroke"
            />
            {trafficHover ? (
              <g pointerEvents="none">
                <line
                  x1={trafficHover.xPercent}
                  x2={trafficHover.xPercent}
                  y1="8"
                  y2="92"
                  vectorEffect="non-scaling-stroke"
                  stroke="oklch(1 0 0 / 22%)"
                  strokeDasharray="4 4"
                />
                <circle
                  cx={trafficHover.xPercent}
                  cy={chartPointY(trafficHover.down, trafficMax)}
                  r="1.2"
                  fill="oklch(0.72 0.19 240)"
                  stroke="oklch(0.13 0.03 248)"
                  strokeWidth="0.6"
                  vectorEffect="non-scaling-stroke"
                />
                <circle
                  cx={trafficHover.xPercent}
                  cy={chartPointY(trafficHover.up, trafficMax)}
                  r="1.2"
                  fill="oklch(0.74 0.16 165)"
                  stroke="oklch(0.13 0.03 248)"
                  strokeWidth="0.6"
                  vectorEffect="non-scaling-stroke"
                />
              </g>
            ) : null}
          </svg>
          {trafficHover ? (
            <div
              className="pointer-events-none absolute top-3 z-10 min-w-44 rounded-md border border-border bg-popover/95 p-3 text-xs shadow-lg backdrop-blur"
              style={{ left: `${trafficHover.tooltipLeftPercent}%`, transform: "translateX(-50%)" }}
            >
              <div className="mb-2 flex items-center justify-between gap-4">
                <span className="font-mono text-[11px] text-muted-foreground">
                  {trafficHover.time}
                </span>
                <span className="font-mono text-[11px] font-semibold text-foreground">
                  合计 {formatTrafficGb(trafficHover.total)}
                </span>
              </div>
              <div className="space-y-1.5 font-mono">
                <div className="flex items-center justify-between gap-4">
                  <span className="flex items-center gap-1.5 text-muted-foreground">
                    <span className="h-1.5 w-1.5 rounded-full bg-primary" />
                    下行
                  </span>
                  <span className="text-primary">{formatTrafficGb(trafficHover.down)}</span>
                </div>
                <div className="flex items-center justify-between gap-4">
                  <span className="flex items-center gap-1.5 text-muted-foreground">
                    <span className="h-1.5 w-1.5 rounded-full bg-chart-2" />
                    上行
                  </span>
                  <span className="text-chart-2">{formatTrafficGb(trafficHover.up)}</span>
                </div>
              </div>
            </div>
          ) : null}
          {trafficTrendState !== "ready" ? (
            <div className="pointer-events-none absolute inset-x-0 bottom-7 top-0 flex items-center justify-center">
              <div className="mx-4 max-w-sm rounded-md border border-border bg-card/90 px-4 py-3 text-center shadow-lg backdrop-blur">
                <div className="mb-2 flex justify-center text-primary">
                  {trafficTrendState === "loading" ? (
                    <Loader2 className="h-5 w-5 animate-spin" />
                  ) : (
                    <Activity className="h-5 w-5" />
                  )}
                </div>
                <div className="text-sm font-medium">
                  {trafficTrendState === "loading" ? "正在同步流量数据" : "暂无可展示流量"}
                </div>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  {trafficTrendEmptyDescription(
                    trafficTrendState === "loading",
                    selectedTrafficTunnelId !== undefined,
                  )}
                </p>
              </div>
            </div>
          ) : null}
          <div className="flex shrink-0 justify-between px-3 pb-2 font-mono text-[10px] text-muted-foreground">
            <span>{trafficData[0]?.time || "00:00"}</span>
            <span>{trafficData[Math.floor(trafficData.length / 2)]?.time || "12:00"}</span>
            <span>{trafficData[trafficData.length - 1]?.time || "23:00"}</span>
          </div>
        </div>
      </Card>

      <Dialog
        open={stressOpen}
        onOpenChange={(open) => {
          setStressOpen(open);
          if (!open) closeSpeedWs();
        }}
      >
        <DialogContent className="flex max-h-[calc(100dvh-2rem)] max-w-2xl flex-col overflow-hidden">
          <DialogHeader className="shrink-0">
            <DialogTitle>隧道压力测试</DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto pr-1">
            <div className="grid gap-2">
              <div className="text-xs font-medium text-muted-foreground">选择隧道</div>
              <Select
                value={stressTunnelId}
                onValueChange={(value) => {
                  setStressTunnelId(value);
                  const selected = stressTunnels.find(
                    (t: any) => stressTunnelIdOf(t) === Number(value),
                  );
                  setStressDirection(selected?.relayNodeId ? "in-to-relay" : "in-to-out");
                }}
                disabled={stressTunnels.length === 0 || stressLoading}
              >
                <SelectTrigger>
                  <SelectValue
                    placeholder={stressTunnels.length === 0 ? "暂无可测试隧道" : "选择隧道"}
                  />
                </SelectTrigger>
                <SelectContent>
                  {stressTunnels.map((t: any) => (
                    <SelectItem
                      key={`${stressTunnelIdOf(t)}-${t.id ?? ""}`}
                      value={String(stressTunnelIdOf(t))}
                    >
                      {t.name || t.tunnelName || `#${t.id}`}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="grid gap-2">
                <div className="text-xs font-medium text-muted-foreground">测试方向</div>
                <Select
                  value={stressDirection}
                  onValueChange={setStressDirection}
                  disabled={stressLoading}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {stressHasRelay ? (
                      <>
                        <SelectItem value="in-to-relay">入口节点 → 中继节点</SelectItem>
                        <SelectItem value="relay-to-out">中继节点 → 出口节点</SelectItem>
                        <SelectItem value="out-to-relay">出口节点 → 中继节点</SelectItem>
                        <SelectItem value="relay-to-in">中继节点 → 入口节点</SelectItem>
                      </>
                    ) : (
                      <>
                        <SelectItem value="in-to-out">入口节点 → 出口节点</SelectItem>
                        <SelectItem value="out-to-in">出口节点 → 入口节点</SelectItem>
                      </>
                    )}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <div className="text-xs font-medium text-muted-foreground">测试时长</div>
                <Input
                  type="number"
                  min={1}
                  max={120}
                  value={stressDuration}
                  disabled={stressLoading}
                  onChange={(e) => setStressDuration(Number(e.target.value) || 10)}
                />
              </div>
              <div className="grid gap-2">
                <div className="text-xs font-medium text-muted-foreground">并发连接</div>
                <Input
                  type="number"
                  min={1}
                  max={32}
                  value={stressParallel}
                  disabled={stressLoading}
                  onChange={(e) => setStressParallel(Number(e.target.value) || 1)}
                />
              </div>
              <div className="grid gap-2">
                <div className="text-xs font-medium text-muted-foreground">iperf3 端口</div>
                <Input
                  inputMode="numeric"
                  placeholder="自动"
                  value={stressPort}
                  disabled={stressLoading}
                  onChange={(e) => setStressPort(e.target.value)}
                />
              </div>
            </div>
            {(stressLoading || stressProgress.length > 0) && (
              <div className="space-y-3 rounded-md border border-border/60 bg-muted/15 p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="grid flex-1 gap-3 sm:grid-cols-3">
                    <div>
                      <div className="text-xs text-muted-foreground">实时速率</div>
                      <div className="mt-1 font-mono text-2xl font-semibold">
                        {latestStressRate.value}
                        <span className="ml-1 text-xs text-muted-foreground">
                          {latestStressRate.unit}
                        </span>
                      </div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">实时延迟</div>
                      <div className="mt-1 font-mono text-xl font-semibold">
                        {latestStressLatency}
                      </div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">实时丢包</div>
                      <div className="mt-1 font-mono text-xl font-semibold">{latestStressLoss}</div>
                    </div>
                  </div>
                  <div className="text-right font-mono text-[11px] text-muted-foreground">
                    <div>{stressLoading ? "running" : "finished"}</div>
                    <div>{stressTestId ? stressTestId.slice(0, 18) : "speed-test"}</div>
                  </div>
                </div>
                <div className="space-y-1.5">
                  <Progress value={liveStressPercent} className="h-1.5 bg-background" />
                  <div className="flex justify-between font-mono text-[10px] text-muted-foreground">
                    <span>{Number(latestStressProgress?.endSeconds || 0).toFixed(0)}s</span>
                    <span>{stressDuration}s</span>
                  </div>
                </div>
                {stressProgress.length > 0 ? (
                  <div className="grid gap-1.5 sm:grid-cols-2">
                    {stressProgress.slice(-6).map((item, index) => {
                      const rate = formatSpeedRateFromMbps(item.mbps || 0);
                      const latency = formatLatencyMs(item.latencyMs);
                      const loss = formatLossPercent(item.lossPercent);
                      return (
                        <div
                          key={`${item.endSeconds}-${index}`}
                          className="rounded-sm border border-border/50 bg-background/50 px-2 py-1 font-mono text-[11px]"
                        >
                          <div className="flex items-center justify-between gap-2">
                            <span className="text-muted-foreground">
                              {Number(item.startSeconds || 0).toFixed(0)}-
                              {Number(item.endSeconds || 0).toFixed(0)}s
                            </span>
                            <span>
                              {rate.value} {rate.unit}
                            </span>
                          </div>
                          <div className="mt-0.5 flex justify-between gap-2 text-[10px] text-muted-foreground">
                            <span>延迟 {latency}</span>
                            <span>丢包 {loss}</span>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <div className="flex h-12 items-center justify-center text-sm text-muted-foreground">
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 等待节点上报测速数据
                  </div>
                )}
              </div>
            )}
            {!stressLoading && stressResult && (
              <div className="space-y-3">
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <Stethoscope className="h-4 w-4" />
                  <span className="font-mono">{stressResult.tunnelName}</span>
                  <span>
                    {stressResult.sourceNodeName} → {stressResult.targetNodeName}
                  </span>
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 p-3">
                    <div className="text-xs text-muted-foreground">发送速率</div>
                    <div className="mt-1 font-mono text-2xl font-semibold">
                      {sentStressRate.value}
                      <span className="ml-1 text-xs text-muted-foreground">
                        {sentStressRate.unit}
                      </span>
                    </div>
                  </div>
                  <div className="rounded-md border border-primary/30 bg-primary/5 p-3">
                    <div className="text-xs text-muted-foreground">接收速率</div>
                    <div className="mt-1 font-mono text-2xl font-semibold">
                      {receivedStressRate.value}
                      <span className="ml-1 text-xs text-muted-foreground">
                        {receivedStressRate.unit}
                      </span>
                    </div>
                  </div>
                  <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-3">
                    <div className="text-xs text-muted-foreground">最终延迟</div>
                    <div className="mt-1 font-mono text-2xl font-semibold">
                      {finalStressLatency}
                    </div>
                  </div>
                  <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3">
                    <div className="text-xs text-muted-foreground">最终丢包</div>
                    <div className="mt-1 font-mono text-2xl font-semibold">{finalStressLoss}</div>
                  </div>
                </div>
                <div className="rounded-md border border-border/60 bg-muted/20 p-3 font-mono text-[11px] text-muted-foreground">
                  <div>
                    target · {stressResult.targetHost}:{stressResult.port}
                  </div>
                  <div>
                    duration · {stressResult.durationSeconds}s · parallel · {stressResult.parallel}
                  </div>
                  <div>
                    latency · {finalStressLatency} · loss · {finalStressLoss}
                  </div>
                  <div>retransmits · {stressResult.summary?.retransmits ?? 0}</div>
                </div>
              </div>
            )}
            {!stressLoading && stressTunnels.length === 0 && (
              <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
                暂无可测速的双节点隧道，请先新建隧道转发。
              </div>
            )}
          </div>
          <DialogFooter className="shrink-0">
            <Button variant="outline" onClick={() => setStressOpen(false)}>
              关闭
            </Button>
            <Button
              disabled={stressLoading || stressTunnels.length === 0}
              onClick={runStressTest}
              className="shadow-glow"
            >
              {stressLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
              开始测试
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Nodes + Events */}
      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="border-border bg-card p-6 shadow-card lg:col-span-2">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-base font-semibold tracking-tight">节点状态</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">节点在线状态</p>
            </div>
            <Badge variant="outline" className="border-border font-mono text-[10px]">
              <Globe2 className="mr-1 h-3 w-3" />
              {adminNodes.length} nodes
            </Badge>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            {nodeCards.length === 0 && (
              <div className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground md:col-span-2">
                暂无节点数据
              </div>
            )}
            {nodeCards.map((n) => {
              const statusClass = n.status === "online" ? "bg-success" : "bg-destructive";
              return (
                <div
                  key={n.name}
                  className="group rounded-lg border border-border bg-muted/20 p-3 transition hover:border-primary/40 hover:bg-muted/40"
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className={`h-2 w-2 rounded-full ${statusClass} shadow-glow`} />
                      <span className="font-mono text-sm font-medium">{n.name}</span>
                    </div>
                    <span className="font-mono text-[11px] uppercase text-muted-foreground">
                      {n.mode}
                    </span>
                  </div>
                  <div className="mt-3 space-y-2">
                    <div className="flex items-center justify-between font-mono text-[11px]">
                      <span className="text-muted-foreground">在线状态</span>
                      <span className={n.status === "online" ? "text-success" : "text-destructive"}>
                        {n.status === "online" ? "在线" : "离线"}
                      </span>
                    </div>
                    <div className="flex items-center justify-between font-mono text-[11px]">
                      <span className="text-muted-foreground">版本</span>
                      <span className="text-foreground">{n.version || "未知"}</span>
                    </div>
                    {n.upgradeAvailable ? (
                      <div className="flex items-center justify-between font-mono text-[11px]">
                        <span className="text-muted-foreground">更新</span>
                        <span className="text-warning">可升级</span>
                      </div>
                    ) : null}
                  </div>
                </div>
              );
            })}
          </div>
        </Card>

        <Card className="border-border bg-card p-6 shadow-card">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-base font-semibold tracking-tight">最近活动</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">系统事件流</p>
            </div>
            <Cpu className="h-4 w-4 text-muted-foreground" />
          </div>
          <ul className="space-y-3">
            {events.map((e, i) => (
              <li key={i} className="flex items-start gap-3">
                <span
                  className={`mt-0.5 inline-flex h-5 items-center rounded border px-1.5 font-mono text-[9px] uppercase tracking-wider ${
                    toneMap[e.tone]
                  }`}
                >
                  {e.type}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="text-sm leading-snug text-foreground">{e.text}</p>
                  <p className="mt-0.5 font-mono text-[10px] text-muted-foreground">{e.time}</p>
                </div>
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </div>
  );
}
