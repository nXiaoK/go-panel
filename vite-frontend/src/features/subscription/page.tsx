import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnDef, RowSelectionState } from "@tanstack/react-table";
import {
  ExternalLink,
  Import,
  KeyRound,
  MoreHorizontal,
  Pencil,
  Plus,
  QrCode,
  RefreshCw,
} from "lucide-react";
import { PageHeader } from "@/components/patterns/page-header";
import { DataTable } from "@/components/patterns/data-table";
import { CodeBlock } from "@/components/patterns/code-block";
import { StatusPill } from "@/components/patterns/status-pill";
import { DiagnosisSheet } from "@/components/patterns/diagnosis-sheet";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { TableToolbar } from "@/components/patterns/table-toolbar";
import { FilterSelect } from "@/components/patterns/filter-select";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useConfirm } from "@/hooks/use-confirm";
import { useUrlState } from "@/hooks/use-url-state";
import { errorMessage } from "@/lib/api/client";
import { diagnoseForward } from "@/lib/api";
import { runBatch, type BatchResult } from "@/lib/batch";
import { formatDateTime, formatRelative } from "@/lib/format";
import { notify } from "@/lib/notify";
import { invalidateFor } from "@/lib/query-keys";
import { currentPanelEndpoint } from "@/lib/panel-endpoint";
import { tunnelStatus } from "@/lib/status";
import type {
  DiagnosisResult,
  ProxyNode,
  SubscriptionFormat,
  SubscriptionProfile,
} from "@/lib/types";
import {
  closeProxyNodeRelay,
  deleteProxyNode,
  deleteSubscriptionProfile,
  regenerateSubscriptionToken,
  updateProxyNode,
  updateSubscriptionProfile,
} from "./api";
import { useSubscriptionSettings } from "./queries";
import { subscriptionFormatLabels, subscriptionFormats } from "./model";
import { SubscriptionApiKeyDialog } from "./components/api-key-dialog";
import { SubscriptionAssignDialog } from "./components/assign-dialog";
import { SubscriptionImportDialog } from "./components/import-dialog";
import { SubscriptionNodeDialog } from "./components/node-dialog";
import { SubscriptionProfileDialog } from "./components/profile-dialog";
import { SubscriptionQrDialog } from "./components/qr-dialog";
import { SubscriptionRelayDialog } from "./components/relay-dialog";
import { SubscriptionDeleteNodesDialog } from "./components/delete-nodes-dialog";
import { SubscriptionNodeRestore } from "./components/node-restore";
import { ReportedNodeRestoreSection } from "./components/reported-node-restore";

// 默认打开订阅配置；节点筛选只影响协议节点，不清除其他标签页的书签参数。
const defaults = { tab: "profiles", q: "", protocol: "", status: "", relay: "" };
const addressOf = (node: ProxyNode) =>
  node.resolvedAddress || `${node.resolvedServer || node.server}:${node.resolvedPort || node.port}`;
const hasRelay = (node: ProxyNode) => Boolean(node.forwardId || node.relayChildCount);
const derived = (node: ProxyNode) => node.relayMode === "append" || Boolean(node.sourceProxyNodeId);
const subUrl = (token: string, format: SubscriptionFormat) =>
  currentPanelEndpoint().subscriptionURL(token, format);
async function copy(value: string, message = "已复制") {
  try {
    await navigator.clipboard.writeText(value);
    notify.success(message);
  } catch {
    notify.error("复制失败");
  }
}

export function SubscriptionPage() {
  const { state, setState } = useUrlState(defaults);
  const queryClient = useQueryClient();
  const requestConfirm = useConfirm();
  const query = useSubscriptionSettings();
  const profiles = useMemo(() => query.data?.profiles ?? [], [query.data]);
  const nodes = useMemo(() => query.data?.nodes ?? [], [query.data]);
  const bindings = query.data?.profileNodes ?? [];
  const [profileEditor, setProfileEditor] = useState<SubscriptionProfile | "new" | null>(null);
  const [nodeEditor, setNodeEditor] = useState<ProxyNode | null>(null);
  const [assignNode, setAssignNode] = useState<ProxyNode | null>(null);
  const [relayNode, setRelayNode] = useState<ProxyNode | null>(null);
  const [deletingNodes, setDeletingNodes] = useState<ProxyNode[] | null>(null);
  const [qr, setQr] = useState<{ title: string; value: string } | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [apiKeyOpen, setApiKeyOpen] = useState(false);
  const [profileSelection, setProfileSelection] = useState<number[]>([]);
  const [nodeSelection, setNodeSelection] = useState<RowSelectionState>({});
  const [busy, setBusy] = useState(false);
  const actionLock = useRef(false);
  const [progress, setProgress] = useState("");
  const [batchResults, setBatchResults] = useState<BatchResult<{ name: string }>[] | null>(null);
  const [diagnosis, setDiagnosis] = useState<{
    node: ProxyNode;
    data?: DiagnosisResult;
    error?: unknown;
    loading: boolean;
  } | null>(null);
  const diagnosisSequence = useRef(0);
  const refresh = (relay = false) =>
    invalidateFor(queryClient, relay ? "subscription.relay" : "subscription.update");
  const profileNodeIds = (id: number) =>
    bindings
      .filter((binding) => binding.subscriptionId === id)
      .sort((a, b) => a.sort - b.sort)
      .map((binding) => binding.proxyNodeId);
  const nodeProfileIds = (node: ProxyNode) =>
    node.profileIds?.length
      ? node.profileIds
      : bindings
          .filter((binding) => binding.proxyNodeId === node.id)
          .sort((a, b) => a.sort - b.sort)
          .map((binding) => binding.subscriptionId);
  const protocols = [...new Set(nodes.map((node) => node.protocol.toLowerCase()))].sort();
  const filteredNodes = nodes.filter((node) => {
    const q = state.q.trim().toLowerCase();
    return (
      (!state.protocol || node.protocol.toLowerCase() === state.protocol) &&
      (!state.status || String(node.status) === state.status) &&
      (!state.relay || hasRelay(node) === (state.relay === "yes")) &&
      (!q || [node.name, node.server, addressOf(node)].join(" ").toLowerCase().includes(q))
    );
  });
  const filtered = Boolean(state.q || state.protocol || state.status || state.relay);
  const selectedNodes = nodes.filter((node) => nodeSelection[String(node.id)]);
  const selectedProfiles = profiles.filter((profile) => profileSelection.includes(profile.id));
  async function act(action: () => Promise<unknown>, message: string, relay = false) {
    if (actionLock.current) return;
    actionLock.current = true;
    setBusy(true);
    try {
      await action();
      notify.success(message);
      await refresh(relay);
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      actionLock.current = false;
      setBusy(false);
    }
  }
  async function rotate(profile: SubscriptionProfile) {
    if (
      !(await requestConfirm({
        tone: "danger",
        title: "更换订阅 Token",
        description: "旧链接会立即失效，所有已导入该订阅的客户端都需要重新导入新链接。",
        confirmText: "更换 Token",
      }))
    )
      return;
    await act(() => regenerateSubscriptionToken(profile.id), "Token 已重新生成");
  }
  async function removeProfile(profile: SubscriptionProfile) {
    if (
      !(await requestConfirm({
        tone: "danger",
        title: `删除订阅「${profile.name}」`,
        description: "删除后该订阅链接将立即失效，且无法恢复。",
        confirmText: "删除订阅",
      }))
    )
      return;
    await act(() => deleteSubscriptionProfile(profile.id), "订阅已删除");
  }
  function updateProfile(profile: SubscriptionProfile, status: 0 | 1) {
    return updateSubscriptionProfile({ ...profile, status, nodeIds: profileNodeIds(profile.id) });
  }
  function updateNode(node: ProxyNode, status: 0 | 1) {
    return updateProxyNode({
      ...node,
      status,
      allowInsecure: node.allowInsecure === 1,
      udp: node.udp === 1,
    });
  }
  async function batch<T extends { name: string }>(
    items: T[],
    action: (item: T) => Promise<unknown>,
    label: string,
    relay = false,
  ) {
    if (actionLock.current) return;
    actionLock.current = true;
    setBusy(true);
    try {
      const results = await runBatch(items, action, {
        nameOf: (item) => item.name,
        onProgress: ({ done, total }) => setProgress(`${label} ${done}/${total}`),
      });
      setBatchResults(results);
      setProfileSelection([]);
      setNodeSelection({});
      await refresh(relay);
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      actionLock.current = false;
      setBusy(false);
      setProgress("");
    }
  }
  async function batchStatus(kind: "订阅" | "节点", status: 0 | 1) {
    const label = `批量${status ? "启用" : "禁用"}${kind}`;
    const items = (kind === "订阅" ? selectedProfiles : selectedNodes).filter(
      (item) => item.status !== status,
    );
    if (!items.length) return notify.info("所选项目已是目标状态");
    if (
      !(await requestConfirm({
        title: label,
        description: `即将${status ? "启用" : "禁用"} ${items.length} 个${kind}，是否继续？`,
        confirmText: label,
      }))
    )
      return;
    if (kind === "订阅")
      await batch(
        selectedProfiles.filter((item) => item.status !== status),
        (item) => updateProfile(item, status),
        label,
      );
    else
      await batch(
        selectedNodes.filter((item) => item.status !== status),
        (item) => updateNode(item, status),
        label,
      );
  }
  async function deleteProfiles() {
    if (
      !(await requestConfirm({
        title: "批量删除订阅",
        description: `将删除 ${selectedProfiles.length} 个订阅，旧链接将立即失效，且无法恢复。`,
        confirmText: "删除订阅",
        tone: "danger",
      }))
    )
      return;
    await batch(selectedProfiles, (item) => deleteSubscriptionProfile(item.id), "批量删除订阅");
  }
  async function deleteNodesBatch() {
    const items = selectedNodes;
    const withRelay = items.filter(hasRelay).length;
    if (
      !items.length ||
      !(await requestConfirm({
        title: "批量删除节点",
        description: `将删除 ${items.length} 个协议节点${withRelay ? `，其中 ${withRelay} 个存在关联中转，将一并删除中转规则和中转节点` : ""}，且无法恢复。`,
        confirmText: "删除节点",
        tone: "danger",
      }))
    )
      return;
    // 沿用既有批量语义：确认后，有中转的节点连同关联中转一起删除；单节点对话框可单独选择保留规则。
    await batch(
      items,
      (node) => deleteProxyNode(node.id, hasRelay(node)),
      "批量删除节点",
      withRelay > 0,
    );
  }
  async function closeRelay(node: ProxyNode) {
    const ruleCount = Number(Boolean(node.forwardId)) + (node.relayChildCount || 0);
    if (
      !(await requestConfirm({
        title: `关闭节点「${node.name}」的中转`,
        description: `将删除 ${ruleCount} 条关联中转规则${node.relayChildCount ? `和 ${node.relayChildCount} 个中转节点` : derived(node) ? "和当前中转节点" : ""}，通过中转地址连接的流量将中断。`,
        confirmText: "关闭中转",
        tone: "danger",
      }))
    )
      return;
    await act(() => closeProxyNodeRelay(node.id), "中转已关闭", true);
  }
  async function diagnose(node: ProxyNode) {
    if (!node.forwardId) return notify.error("该节点没有可测试的中转规则");
    const sequence = ++diagnosisSequence.current;
    setDiagnosis({ node, loading: true });
    try {
      const data = await diagnoseForward(node.forwardId);
      if (sequence === diagnosisSequence.current) setDiagnosis({ node, data, loading: false });
    } catch (error) {
      if (sequence === diagnosisSequence.current) setDiagnosis({ node, error, loading: false });
    }
  }
  function showQr(profile: SubscriptionProfile, format: SubscriptionFormat) {
    setQr({
      title: `${profile.name} · ${subscriptionFormatLabels[format]}`,
      value: subUrl(profile.token, format),
    });
  }
  function profileMenu(profile: SubscriptionProfile) {
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`${profile.name} 更多操作`}
            disabled={busy}
          >
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => copy(profile.token, "Token 已复制")}>
            复制 Token
          </DropdownMenuItem>
          {subscriptionFormats.map((format) => (
            <DropdownMenuItem key={format} onClick={() => showQr(profile, format)}>
              {subscriptionFormatLabels[format]} 二维码
            </DropdownMenuItem>
          ))}
          <DropdownMenuItem
            onClick={() =>
              act(
                () => updateProfile(profile, profile.status === 1 ? 0 : 1),
                profile.status === 1 ? "已禁用" : "已启用",
              )
            }
          >
            {profile.status === 1 ? "禁用订阅" : "启用订阅"}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => rotate(profile)}>更换 Token</DropdownMenuItem>
          <DropdownMenuItem
            className="text-status-critical-text"
            onClick={() => removeProfile(profile)}
          >
            删除订阅
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    );
  }
  function nodeActions(node: ProxyNode) {
    return (
      <div className="flex items-center justify-end gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`编辑节点 ${node.name}`}
          onClick={() => setNodeEditor(node)}
          disabled={busy}
        >
          <Pencil />
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`${node.name} 更多操作`}
              disabled={busy}
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => setAssignNode(node)}>绑定订阅</DropdownMenuItem>
            {!derived(node) && (
              <DropdownMenuItem onClick={() => setRelayNode(node)}>配置中转</DropdownMenuItem>
            )}
            <DropdownMenuItem onClick={() => copy(addressOf(node), "地址已复制")}>
              复制地址
            </DropdownMenuItem>
            {node.link && (
              <DropdownMenuItem onClick={() => copy(node.link!, "链接已复制")}>
                复制分享链接
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              onClick={() =>
                act(
                  () => updateNode(node, node.status === 1 ? 0 : 1),
                  node.status === 1 ? "已禁用" : "已启用",
                )
              }
            >
              {node.status === 1 ? "禁用节点" : "启用节点"}
            </DropdownMenuItem>
            {hasRelay(node) && (
              <>
                <DropdownMenuItem disabled={!node.forwardId} onClick={() => diagnose(node)}>
                  测试中转
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => closeRelay(node)}>关闭中转</DropdownMenuItem>
              </>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-status-critical-text"
              onClick={() => setDeletingNodes([node])}
            >
              删除节点
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    );
  }
  const columns: ColumnDef<ProxyNode>[] = [
    {
      accessorKey: "name",
      header: "名称",
      cell: ({ row }) => <span className="font-medium text-ink">{row.original.name}</span>,
    },
    {
      accessorKey: "protocol",
      header: "协议",
      cell: ({ row }) => <Badge variant="outline">{row.original.protocol.toUpperCase()}</Badge>,
    },
    {
      id: "address",
      accessorFn: addressOf,
      header: "地址",
      cell: ({ row }) => <span className="font-mono text-code">{addressOf(row.original)}</span>,
    },
    {
      accessorKey: "lastReportTime",
      header: "上报时间",
      cell: ({ row }) => (
        <span
          title={
            row.original.lastReportTime ? formatDateTime(row.original.lastReportTime) : undefined
          }
        >
          {row.original.lastReportTime ? formatRelative(row.original.lastReportTime) : "未上报"}
        </span>
      ),
    },
    {
      id: "relay",
      header: "中转",
      cell: ({ row }) =>
        row.original.forwardName ||
        (row.original.relayChildCount ? `${row.original.relayChildCount} 个中转节点` : "—"),
    },
    {
      id: "profiles",
      header: "所属订阅数",
      accessorFn: (node) => nodeProfileIds(node).length,
      meta: { numeric: true, align: "right" },
    },
    {
      accessorKey: "status",
      header: "状态",
      cell: ({ row }) => <StatusPill {...tunnelStatus(row.original)} />,
    },
    {
      id: "actions",
      header: "操作",
      enableSorting: false,
      meta: { actions: true },
      cell: ({ row }) => nodeActions(row.original),
    },
  ];
  const batchButtons = (kind: "订阅" | "节点") => (
    <>
      <Button size="sm" variant="outline" disabled={busy} onClick={() => batchStatus(kind, 1)}>
        批量启用
      </Button>
      <Button size="sm" variant="outline" disabled={busy} onClick={() => batchStatus(kind, 0)}>
        批量禁用
      </Button>
      <Button
        size="sm"
        variant="danger"
        disabled={busy}
        onClick={() => (kind === "订阅" ? deleteProfiles() : deleteNodesBatch())}
      >
        批量删除
      </Button>
      {progress && <span role="status">{progress}</span>}
    </>
  );
  return (
    <div className="min-w-0 space-y-6">
      <PageHeader
        title="订阅"
        description="管理订阅链接、协议节点和节点上报"
        actions={
          <>
            <Button variant="outline" onClick={() => setImportOpen(true)} disabled={busy}>
              <Import />
              导入节点
            </Button>
            <Button variant="outline" onClick={() => setApiKeyOpen(true)} disabled={busy}>
              <KeyRound />
              API Key
            </Button>
            <Button onClick={() => setProfileEditor("new")} disabled={busy}>
              <Plus />
              新建订阅
            </Button>
            <Button
              variant="ghost"
              size="icon"
              aria-label="刷新订阅"
              loading={query.isFetching}
              onClick={() => refresh()}
            >
              <RefreshCw />
            </Button>
          </>
        }
      />
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      {!(query.error && !query.data) && (
        <Tabs value={state.tab} onValueChange={(tab) => setState({ tab })}>
          <TabsList className="max-w-full">
            <TabsTrigger value="profiles">订阅配置 {profiles.length}</TabsTrigger>
            <TabsTrigger value="nodes">协议节点 {nodes.length}</TabsTrigger>
            <TabsTrigger value="report">节点上报</TabsTrigger>
          </TabsList>
          <TabsContent value="profiles" className="space-y-3">
            {query.isPending ? (
              <LoadingRows />
            ) : !profiles.length ? (
              <EmptyState title="暂无订阅配置" description="新建订阅后，将协议节点分享给客户端" />
            ) : (
              <>
                <label className="inline-flex min-h-10 items-center gap-2 text-caption text-body">
                  <Checkbox
                    aria-label="选择全部订阅"
                    checked={profiles.every((profile) => profileSelection.includes(profile.id))}
                    onCheckedChange={(checked) =>
                      setProfileSelection(checked ? profiles.map((profile) => profile.id) : [])
                    }
                    disabled={busy}
                  />
                  全选订阅
                </label>
                {profiles.map((profile) => (
                  <article
                    key={profile.id}
                    className={`min-w-0 space-y-3 rounded-md border border-hairline p-5 ${profileSelection.includes(profile.id) ? "bg-surface-selected" : "bg-canvas"}`}
                  >
                    <div className="flex flex-wrap items-center gap-3">
                      <Checkbox
                        aria-label={`选择订阅 ${profile.name}`}
                        checked={profileSelection.includes(profile.id)}
                        disabled={busy}
                        onCheckedChange={(checked) =>
                          setProfileSelection((ids) =>
                            checked ? [...ids, profile.id] : ids.filter((id) => id !== profile.id),
                          )
                        }
                      />
                      <h2 className="min-w-0 break-words text-card-title text-ink">
                        {profile.name}
                      </h2>
                      <StatusPill {...tunnelStatus(profile)} />
                      <Badge variant="outline">
                        {subscriptionFormatLabels[profile.defaultFormat]}
                      </Badge>
                      <span className="text-caption text-mute">
                        {profileNodeIds(profile.id).length} 个节点
                      </span>
                      <div className="ml-auto flex gap-1">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy}
                          onClick={() => setProfileEditor(profile)}
                        >
                          编辑
                        </Button>
                        {profileMenu(profile)}
                      </div>
                    </div>
                    {profile.description && (
                      <p className="break-words text-sm text-body">{profile.description}</p>
                    )}
                    <CodeBlock
                      code={subUrl(profile.token, profile.defaultFormat)}
                      label={`复制 ${profile.name} 订阅链接`}
                      singleLine
                      trailingActions={
                        <>
                          <Button variant="ghost" size="icon" asChild>
                            <a
                              href={subUrl(profile.token, profile.defaultFormat)}
                              target="_blank"
                              rel="noreferrer"
                              aria-label={`打开 ${profile.name} 订阅链接`}
                            >
                              <ExternalLink />
                            </a>
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={`显示 ${profile.name} 订阅二维码`}
                            onClick={() => showQr(profile, profile.defaultFormat)}
                          >
                            <QrCode />
                          </Button>
                        </>
                      }
                    />
                  </article>
                ))}
                {selectedProfiles.length > 0 && (
                  <div
                    role="region"
                    aria-label="订阅批量操作"
                    className="sticky bottom-0 z-20 flex flex-wrap items-center gap-2 rounded-md border border-hairline bg-canvas-soft p-3 shadow-level-3"
                  >
                    <span className="text-caption text-body">
                      已选 {selectedProfiles.length} 项
                    </span>
                    {batchButtons("订阅")}
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busy}
                      onClick={() => setProfileSelection([])}
                    >
                      取消选择
                    </Button>
                  </div>
                )}
              </>
            )}
          </TabsContent>
          <TabsContent value="nodes" className="space-y-4">
            <TableToolbar
              searchValue={state.q}
              onSearchChange={(q) => setState({ q })}
              searchPlaceholder="搜索名称和地址…"
              count={`共 ${filteredNodes.length} 个节点`}
              filters={
                <>
                  <FilterSelect
                    label="筛选节点协议"
                    value={state.protocol}
                    onChange={(protocol) => setState({ protocol })}
                    options={[
                      { value: "", label: "全部协议" },
                      ...protocols.map((value) => ({ value, label: value.toUpperCase() })),
                    ]}
                  />
                  <FilterSelect
                    label="筛选节点状态"
                    value={state.status}
                    onChange={(status) => setState({ status })}
                    options={[
                      { value: "", label: "全部状态" },
                      { value: "1", label: "启用" },
                      { value: "0", label: "已禁用" },
                    ]}
                  />
                  <FilterSelect
                    label="筛选节点中转"
                    value={state.relay}
                    onChange={(relay) => setState({ relay })}
                    options={[
                      { value: "", label: "全部中转" },
                      { value: "yes", label: "有中转" },
                      { value: "no", label: "无中转" },
                    ]}
                  />
                </>
              }
            />
            <DataTable
              ariaLabel="协议节点"
              columns={columns}
              data={filteredNodes}
              getRowId={(node) => String(node.id)}
              loading={query.isPending}
              filtered={filtered}
              onClearFilters={() => setState({ q: "", protocol: "", status: "", relay: "" })}
              emptyState={
                !filtered ? (
                  <EmptyState
                    title="暂无节点"
                    description="暂无节点，先配置脚本上报或导入分享链接"
                  />
                ) : undefined
              }
              enableSelection
              rowSelection={nodeSelection}
              onRowSelectionChange={setNodeSelection}
              bulkActions={() => batchButtons("节点")}
              renderMobileCard={(node) => (
                <div className="min-w-0 space-y-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="break-words text-body-strong text-ink">{node.name}</h2>
                    <Badge variant="outline">{node.protocol.toUpperCase()}</Badge>
                    <StatusPill {...tunnelStatus(node)} />
                  </div>
                  <p className="break-all font-mono text-code text-body">{addressOf(node)}</p>
                  <p
                    className="text-caption text-mute"
                    title={node.lastReportTime ? formatDateTime(node.lastReportTime) : undefined}
                  >
                    {node.lastReportTime ? formatRelative(node.lastReportTime) : "未上报"} ·{" "}
                    {nodeProfileIds(node).length} 个订阅
                  </p>
                  {hasRelay(node) && (
                    <p className="break-words text-caption text-body">
                      中转：{node.forwardName || `${node.relayChildCount} 个中转节点`}
                    </p>
                  )}
                  {nodeActions(node)}
                </div>
              )}
            />
          </TabsContent>
          <TabsContent value="report" className="space-y-4">
            <p className="text-sm text-body">
              上报脚本中包含 API Key，请勿公开或发送给他人。更换 API Key 后，已部署的脚本需要更新。
            </p>
            {query.isPending ? (
              <LoadingRows />
            ) : query.data?.vlessBootstrapScript ? (
              <CodeBlock code={query.data.vlessBootstrapScript} label="复制脚本" />
            ) : (
              <EmptyState title="暂无上报脚本" description="配置 API Key 后刷新查看节点上报脚本" />
            )}
            {!query.isPending && (
              <>
                <SubscriptionNodeRestore backups={query.data?.nodeBackups ?? []} />
                <ReportedNodeRestoreSection targets={query.data?.reportedNodeRestores ?? []} />
              </>
            )}
          </TabsContent>
        </Tabs>
      )}
      {profileEditor && (
        <SubscriptionProfileDialog
          open
          onOpenChange={(open) => {
            if (!open) setProfileEditor(null);
          }}
          editing={profileEditor === "new" ? null : profileEditor}
          nodes={nodes}
          initialNodeIds={
            profileEditor === "new"
              ? nodes.map((node) => node.id)
              : profileNodeIds(profileEditor.id)
          }
        />
      )}
      {nodeEditor && (
        <SubscriptionNodeDialog
          open
          onOpenChange={(open) => {
            if (!open) setNodeEditor(null);
          }}
          editing={nodeEditor}
        />
      )}
      {assignNode && (
        <SubscriptionAssignDialog
          open
          onOpenChange={(open) => {
            if (!open) setAssignNode(null);
          }}
          node={assignNode}
          profiles={profiles}
          initialProfileIds={nodeProfileIds(assignNode)}
        />
      )}
      {relayNode && (
        <SubscriptionRelayDialog
          open
          onOpenChange={(open) => {
            if (!open) setRelayNode(null);
          }}
          node={relayNode}
          tunnels={query.data?.tunnels ?? []}
        />
      )}
      {deletingNodes && (
        <SubscriptionDeleteNodesDialog
          nodes={deletingNodes}
          onClose={() => setDeletingNodes(null)}
          onDelete={async (deleteForward) => {
            await batch(
              deletingNodes,
              (node) => deleteProxyNode(node.id, deleteForward),
              "删除节点",
              deletingNodes.some(hasRelay),
            );
            setDeletingNodes(null);
          }}
        />
      )}
      <SubscriptionQrDialog
        open={Boolean(qr)}
        title={qr?.title ?? ""}
        value={qr?.value ?? ""}
        onOpenChange={(open) => {
          if (!open) setQr(null);
        }}
      />
      <SubscriptionImportDialog open={importOpen} onOpenChange={setImportOpen} />
      <SubscriptionApiKeyDialog
        open={apiKeyOpen}
        onOpenChange={setApiKeyOpen}
        currentKey={query.data?.apiKey ?? ""}
      />
      <DiagnosisSheet
        open={Boolean(diagnosis)}
        title={diagnosis ? `中转诊断 · ${diagnosis.node.name}` : "中转诊断"}
        onOpenChange={(open) => {
          if (!open) {
            diagnosisSequence.current++;
            setDiagnosis(null);
          }
        }}
        data={diagnosis?.data}
        loading={diagnosis?.loading}
        error={diagnosis?.error}
        onRetry={diagnosis ? () => void diagnose(diagnosis.node) : undefined}
      />
      <Dialog
        open={Boolean(batchResults)}
        onOpenChange={(open) => {
          if (!open) setBatchResults(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>批量操作结果</DialogTitle>
            <DialogDescription>
              成功 {batchResults?.filter((item) => item.success).length ?? 0} 项 · 失败{" "}
              {batchResults?.filter((item) => !item.success).length ?? 0} 项
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-72 space-y-2 overflow-y-auto">
            {batchResults?.map((item, index) => (
              <div key={index} className="space-y-1 rounded-md border border-hairline p-3">
                <div className="flex flex-wrap justify-between gap-2">
                  <span className="break-all text-body">{item.name}</span>
                  <StatusPill
                    tone={item.success ? "good" : "critical"}
                    label={item.success ? "成功" : "失败"}
                  />
                </div>
                {!item.success && (
                  <p className="break-words text-caption text-status-critical-text">
                    {item.reason || "未知错误"}
                  </p>
                )}
              </div>
            ))}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setBatchResults(null)}>
              关闭结果
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
