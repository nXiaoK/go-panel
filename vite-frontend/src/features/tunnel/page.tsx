import { PageHeader } from "@/components/patterns/page-header";
import { TableToolbar } from "@/components/patterns/table-toolbar";
import { FilterSelect } from "@/components/patterns/filter-select";
import { DiagnosisSheet } from "@/components/patterns/diagnosis-sheet";
import { Button } from "@/components/ui/button";
import { useNodes } from "@/features/node/queries";
import { useConfirm } from "@/hooks/use-confirm";
import { useUrlState } from "@/hooks/use-url-state";
import { createTunnel, deleteTunnel, diagnoseTunnel, updateTunnel } from "./api";
import { notify } from "@/lib/notify";
import { resourceKeys } from "@/lib/query-keys";
import type { DiagnosisResult, Tunnel, TunnelForm, TunnelWrite } from "@/lib/types";
import { useQueryClient } from "@tanstack/react-query";
import type { SortingState } from "@tanstack/react-table";
import { Plus, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTunnelMutation, useTunnels } from "./queries";
import { emptyTunnelForm, nftModeFor } from "./model";
import { TunnelList } from "./components/tunnel-list";
import { TunnelFormSheet } from "./components/tunnel-form-sheet";
import { SpeedTestSheet } from "./components/speed-test-sheet";

const defaults = {
  q: "",
  type: "",
  status: "",
  sort: "",
  focus: undefined as number | undefined,
  create: undefined as number | undefined,
  action: undefined as string | undefined,
};
export function TunnelPage() {
  const { state, setState } = useUrlState(defaults);
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [formOpen, setFormOpen] = useState(false);
  const [initialForm, setInitialForm] = useState<TunnelForm>(emptyTunnelForm);
  const [formKey, setFormKey] = useState(0);
  const [diagnosing, setDiagnosing] = useState<Tunnel | null>(null);
  const [diagLoading, setDiagLoading] = useState(false);
  const [diagResult, setDiagResult] = useState<DiagnosisResult | null>(null);
  const [diagError, setDiagError] = useState<unknown>();
  const diagSequence = useRef(0);
  const [speedTunnel, setSpeedTunnel] = useState<Tunnel | null>(null);
  const tunnelsQuery = useTunnels();
  const nodesQuery = useNodes();
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const nodeMap = useMemo(() => new Map(nodes.map((node) => [node.id, node])), [nodes]);
  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: resourceKeys.tunnel.list() });
    void queryClient.invalidateQueries({ queryKey: resourceKeys.node.list() });
  }, [queryClient]);
  const saveMutation = useTunnelMutation("tunnel.update", {
    mutationFn: (payload: TunnelWrite) =>
      payload.id ? updateTunnel(payload) : createTunnel(payload),
    onSuccess: (_, payload) => {
      notify.success(payload.id ? "隧道已更新" : "隧道已创建");
      setFormOpen(false);
    },
  });
  const deleteMutation = useTunnelMutation("tunnel.delete", {
    mutationFn: (id: number) => deleteTunnel(id),
    onSuccess: () => notify.success("隧道已删除"),
    onError: (error: Error) => notify.error(error.message),
  });
  const openCreate = useCallback(() => {
    setInitialForm(emptyTunnelForm());
    setFormKey((key) => key + 1);
    setFormOpen(true);
  }, []);
  useEffect(() => {
    if (state.create !== 1 && state.action !== "create") return;
    openCreate();
    void setState({ create: undefined, action: undefined });
  }, [state.create, state.action, setState, openCreate]);
  useEffect(() => {
    if (state.focus && (state.q || state.type || state.status))
      void setState({ q: "", type: "", status: "" });
  }, [state.focus, state.q, state.type, state.status, setState]);
  function openEdit(tunnel: Tunnel) {
    setInitialForm({
      ...emptyTunnelForm(),
      ...tunnel,
      protocol: tunnel.protocol || "tcp",
      relayNodeId: tunnel.relayNodeId ?? null,
      outNodeId: tunnel.outNodeId ?? null,
      interfaceName: tunnel.interfaceName || "",
      flow: tunnel.flow ?? 2,
      status: tunnel.status ?? 1,
      trafficRatio: tunnel.trafficRatio ?? 1,
    });
    setFormKey((key) => key + 1);
    setFormOpen(true);
  }
  const filtered = useMemo(
    () =>
      tunnels.filter((tunnel) => {
        const q = (state.q ?? "").trim().toLowerCase();
        return (
          (!(state.type ?? "") || String(tunnel.type) === state.type) &&
          (!(state.status ?? "") || String(tunnel.status ?? 1) === state.status) &&
          (!q ||
            `${tunnel.name} ${tunnel.id} ${nftModeFor(tunnel, nodeMap) ? "tcp+udp" : tunnel.protocol || "tcp"}`
              .toLowerCase()
              .includes(q))
        );
      }),
    [tunnels, state.q, state.type, state.status, nodeMap],
  );
  const sorting: SortingState =
    state.sort === "name:desc"
      ? [{ id: "name", desc: true }]
      : state.sort === "name:asc"
        ? [{ id: "name", desc: false }]
        : state.sort === "status:asc"
          ? [{ id: "status", desc: false }]
          : state.sort === "status:desc"
            ? [{ id: "status", desc: true }]
            : [];
  async function runDiagnose(tunnel: Tunnel) {
    const sequence = ++diagSequence.current;
    setDiagnosing(tunnel);
    setDiagLoading(true);
    setDiagResult(null);
    setDiagError(undefined);
    try {
      const result = await diagnoseTunnel(tunnel.id);
      if (sequence === diagSequence.current) setDiagResult(result);
    } catch (error) {
      if (sequence === diagSequence.current) setDiagError(error);
    } finally {
      if (sequence === diagSequence.current) setDiagLoading(false);
    }
  }
  async function remove(tunnel: Tunnel) {
    if (
      await confirm({
        title: `删除隧道「${tunnel.name}」`,
        description: "已被转发或用户权限占用的隧道无法删除，请先解除依赖。删除后无法恢复。",
        confirmText: "删除隧道",
        tone: "danger",
      })
    )
      deleteMutation.mutate(tunnel.id);
  }
  const hasFilter = Boolean(state.q || state.type || state.status);
  return (
    <div className="min-w-0 space-y-6">
      <PageHeader
        title="隧道"
        description="管理节点间的转发链路、协议与计费"
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              loading={tunnelsQuery.isFetching || nodesQuery.isFetching}
              onClick={refresh}
            >
              <RefreshCw />
              刷新
            </Button>
            <Button type="button" onClick={openCreate}>
              <Plus />
              新建隧道
            </Button>
          </>
        }
      />
      <TableToolbar
        searchValue={state.q ?? ""}
        onSearchChange={(q) => {
          void setState({ q, focus: undefined });
        }}
        searchPlaceholder="名称 / ID / 协议"
        count={`共 ${filtered.length} 条`}
        filters={
          <>
            <FilterSelect
              label="类型"
              value={state.type ?? ""}
              onChange={(type) => {
                void setState({ type, focus: undefined });
              }}
              options={[
                { value: "", label: "全部类型" },
                { value: "1", label: "端口转发" },
                { value: "2", label: "隧道转发" },
              ]}
            />
            <FilterSelect
              label="状态"
              value={state.status ?? ""}
              onChange={(status) => {
                void setState({ status, focus: undefined });
              }}
              options={[
                { value: "", label: "全部状态" },
                { value: "1", label: "启用" },
                { value: "0", label: "已禁用" },
              ]}
            />
          </>
        }
      />
      <TunnelList
        tunnels={filtered}
        nodes={nodeMap}
        loading={tunnelsQuery.isPending || nodesQuery.isPending}
        error={tunnelsQuery.error ?? nodesQuery.error}
        onRetry={refresh}
        filtered={hasFilter}
        onClearFilters={() => {
          void setState({ q: "", type: "", status: "" });
        }}
        onCreate={openCreate}
        onEdit={openEdit}
        onDiagnose={(tunnel) => {
          void runDiagnose(tunnel);
        }}
        onSpeed={setSpeedTunnel}
        onDelete={(tunnel) => {
          void remove(tunnel);
        }}
        focusRowId={String(state.focus ?? "")}
        sorting={sorting}
        onSortingChange={(updater) => {
          const next = typeof updater === "function" ? updater(sorting) : updater;
          void setState({ sort: next[0] ? `${next[0].id}:${next[0].desc ? "desc" : "asc"}` : "" });
        }}
      />
      {formOpen && (
        <TunnelFormSheet
          key={formKey}
          open={formOpen}
          onOpenChange={setFormOpen}
          initial={initialForm}
          nodes={nodes}
          onSave={(payload) => saveMutation.mutateAsync(payload)}
          saving={saveMutation.isPending}
        />
      )}
      <DiagnosisSheet
        open={Boolean(diagnosing)}
        onOpenChange={(open) => {
          if (!open) {
            diagSequence.current++;
            setDiagnosing(null);
          }
        }}
        title={`隧道诊断 · ${diagnosing?.name ?? ""}`}
        data={diagResult}
        loading={diagLoading}
        error={diagError}
        onRetry={() => {
          if (diagnosing) void runDiagnose(diagnosing);
        }}
      />
      <SpeedTestSheet
        tunnel={speedTunnel}
        onOpenChange={(open) => {
          if (!open) setSpeedTunnel(null);
        }}
      />
    </div>
  );
}
