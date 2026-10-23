import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { FormField } from "@/components/patterns/form-field";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { UserMeter } from "./user-meter";
import { StatusPill } from "@/components/patterns/status-pill";
import { useConfirm } from "@/hooks/use-confirm";
import { useLimits } from "@/features/limit/queries";
import { useTunnels } from "@/features/tunnel/queries";
import {
  assignUserTunnel,
  getUserTunnelList,
  removeUserTunnel,
  resetUserFlow,
  updateUserTunnel,
} from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { formatDateTime } from "@/lib/format";
import { invalidateFor, resourceKeys } from "@/lib/query-keys";
import type { User, UserTunnel } from "@/lib/types";
import {
  dateTimestamp,
  expiryState,
  quotaValues,
  validateQuota,
  type FieldErrors,
  type QuotaValues,
} from "./model";
import { QuotaFields } from "./quota-fields";
import { focusFirstError } from "./model";
import { useUserTunnels } from "./use-user-tunnels";

export function UserPermissions({
  user,
  onDirtyChange,
  onBusyChange,
  onCountChange,
}: {
  user: User;
  onDirtyChange: (dirty: boolean) => void;
  onBusyChange: (busy: boolean) => void;
  onCountChange: (count: number | null) => void;
}) {
  const client = useQueryClient();
  const confirm = useConfirm();
  const manager = useUserTunnels<UserTunnel, User>(
    useCallback((userId, signal) => getUserTunnelList({ userId }, { signal }), []),
  );
  const managerRef = useRef(manager);
  useEffect(() => {
    managerRef.current = manager;
  }, [manager]);
  useEffect(() => {
    manager.open(user);
    return () => managerRef.current.close();
  }, [user.id]); // eslint-disable-line react-hooks/exhaustive-deps -- 只在用户身份变化时切换，表单保存不重开权限查询
  const tunnels = useTunnels();
  const limits = useLimits();
  const [values, setValues] = useState<QuotaValues>(quotaValues());
  const [tunnelId, setTunnelId] = useState("");
  const [editing, setEditing] = useState<number | null>(null);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  useEffect(() => {
    onBusyChange(busy);
  }, [busy, onBusyChange]);
  useEffect(() => {
    onCountChange(manager.canMutate ? manager.rows.length : null);
  }, [manager.canMutate, manager.rows.length, onCountChange]);
  useEffect(() => {
    onDirtyChange(
      editing !== null ||
        tunnelId !== "" ||
        JSON.stringify(values) !== JSON.stringify(quotaValues()),
    );
  }, [editing, tunnelId, values, onDirtyChange]);
  const selectedTunnel =
    editing !== null ? manager.rows.find((row) => row.id === editing)?.tunnelId : Number(tunnelId);
  const available = (tunnels.data ?? []).filter(
    (t) => !manager.rows.some((row) => row.tunnelId === t.id),
  );
  const availableLimits = (limits.data ?? []).filter((limit) => limit.tunnelId === selectedTunnel);
  const writable =
    manager.canMutate &&
    !busy &&
    !tunnels.isFetching &&
    !tunnels.isError &&
    !limits.isFetching &&
    !limits.isError;
  // 确认框可能跨越失效重取；提交时重新读取当前用户的成功缓存，禁止闭包旧行覆盖新配额。
  function currentRows() {
    const active = managerRef.current;
    const state = client.getQueryState<UserTunnel[]>(resourceKeys.user.tunnels(user.id));
    if (
      !active.canMutate ||
      active.activeUserID !== user.id ||
      state?.status !== "success" ||
      state.fetchStatus !== "idle"
    )
      return null;
    const rows = state.data ?? [];
    return rows.every((row) => row.userId === user.id) ? rows : null;
  }
  async function mutate(action: () => Promise<unknown>) {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      await action();
      await invalidateFor(client, "user.authorize");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }
  function resetForm() {
    setEditing(null);
    setTunnelId("");
    setValues(quotaValues());
    setErrors({});
  }
  async function rowAction(id: number, action: "toggle" | "reset" | "remove") {
    const initial = currentRows()?.find((row) => row.id === id);
    if (!initial || busyRef.current) return;
    if (
      action !== "toggle" &&
      !(await confirm({
        title: action === "reset" ? "重置隧道流量" : "移除隧道权限",
        description:
          action === "reset"
            ? `将 ${user.user} 在 ${initial.tunnelName ?? "该隧道"} 的流量计数清零，操作不可撤销`
            : `移除 ${initial.tunnelName ?? "该隧道"} 权限后，该用户相关转发将失效`,
        confirmText: action === "reset" ? "重置流量" : "移除权限",
        tone: "danger",
      }))
    )
      return;
    const row = currentRows()?.find((item) => item.id === id);
    if (!row) {
      setError("授权数据已变化，请等待刷新完成后重试");
      return;
    }
    await mutate(() =>
      action === "reset"
        ? resetUserFlow({ id: row.id, type: 2 })
        : action === "remove"
          ? removeUserTunnel({ id: row.id })
          : updateUserTunnel({
              id: row.id,
              flow: row.flow,
              num: row.num,
              expTime: row.expTime,
              flowResetTime: row.flowResetTime,
              speedId: row.speedId ?? null,
              status: row.status === 1 ? 0 : 1,
            }),
    );
  }
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!writable || !currentRows()) return;
    const next = validateQuota(values);
    if (editing === null && !available.some((t) => t.id === Number(tunnelId)))
      next.tunnelId = "请选择可授权的隧道";
    setErrors(next);
    if (Object.keys(next).length) {
      focusFirstError(event.currentTarget);
      return;
    }
    const current = editing === null ? null : currentRows()?.find((row) => row.id === editing);
    if (editing !== null && !current) {
      setError("该授权已变化，请重新选择");
      return;
    }
    const payload = {
      flow: Number(values.flow),
      num: Number(values.num),
      expTime: dateTimestamp(values.expiry)!,
      flowResetTime: Number(values.reset),
      speedId: values.speedId === "none" ? null : Number(values.speedId),
    };
    if (
      payload.speedId !== null &&
      !availableLimits.some((limit) => limit.id === payload.speedId)
    ) {
      setError("限速规则已变化，请重新选择");
      return;
    }
    await mutate(async () => {
      if (current) await updateUserTunnel({ ...payload, id: current.id, status: current.status });
      else await assignUserTunnel({ ...payload, userId: user.id, tunnelId: Number(tunnelId) });
      resetForm();
    });
  }
  const authorizationForm = (
    <section
      className="space-y-4 border-t border-hairline pt-5"
      aria-label={editing !== null ? "编辑授权配额" : "添加授权"}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-card-title text-ink">
          {editing !== null ? "编辑授权配额" : "添加授权"}
        </h3>
        {editing !== null && (
          <Button type="button" variant="ghost" size="sm" onClick={resetForm}>
            取消编辑
          </Button>
        )}
      </div>
      <form onSubmit={save} noValidate className="space-y-4">
        <FormField label="隧道" htmlFor="grant-tunnel" required error={errors.tunnelId}>
          <Select
            value={tunnelId}
            disabled={!writable || editing !== null}
            onValueChange={(id) => {
              setTunnelId(id);
              setValues({ ...values, speedId: "none" });
            }}
          >
            <SelectTrigger>
              <SelectValue placeholder={available.length ? "选择隧道" : "没有可添加的隧道"} />
            </SelectTrigger>
            <SelectContent>
              {editing !== null ? (
                <SelectItem value={tunnelId}>
                  {manager.rows.find((r) => r.id === editing)?.tunnelName || "当前隧道"}
                </SelectItem>
              ) : (
                available.map((tunnel) => (
                  <SelectItem key={tunnel.id} value={String(tunnel.id)}>
                    {tunnel.name}
                  </SelectItem>
                ))
              )}
            </SelectContent>
          </Select>
        </FormField>
        <QuotaFields
          prefix="grant"
          values={values}
          errors={errors}
          onChange={setValues}
          limits={availableLimits}
          disabled={!writable}
        />
        <Button type="submit" loading={busy} disabled={!writable}>
          {editing !== null ? "保存授权配额" : "添加授权"}
        </Button>
      </form>
    </section>
  );
  return (
    <div className="space-y-6">
      <section className="space-y-3" aria-label="已授权隧道">
        <h3 className="text-card-title text-ink">
          已授权隧道 {manager.canMutate ? manager.rows.length : ""}
        </h3>
        {manager.loading ? (
          <LoadingRows rows={3} />
        ) : !manager.canMutate ? (
          <ErrorState
            error="无法读取当前授权，刷新成功前已暂停所有授权操作"
            onRetry={manager.reload}
          />
        ) : manager.rows.length === 0 ? (
          <EmptyState title="尚未分配隧道" description="在下方添加第一个隧道权限" />
        ) : (
          manager.rows.map((row) => (
            <article key={row.id} className="space-y-3 rounded-md border border-hairline p-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h4 className="min-w-0 break-words text-body-strong text-ink">
                  {row.tunnelName || `隧道 #${row.tunnelId}`}
                </h4>
                <StatusPill
                  tone={row.status === 1 ? "good" : "neutral"}
                  hollow={row.status !== 1}
                  label={row.status === 1 ? "启用" : "已禁用"}
                />
              </div>
              <UserMeter row={row} label={`${row.tunnelName}流量配额`} />
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-caption text-body">
                <dt className="text-mute">转发上限</dt>
                <dd className="text-right font-mono tabular">{row.num}</dd>
                <dt className="text-mute">到期</dt>
                <dd className="flex flex-wrap justify-end gap-2">
                  <StatusPill {...expiryState(row.expTime)} />
                  <span className="font-mono">{formatDateTime(row.expTime)}</span>
                </dd>
                <dt className="text-mute">限速</dt>
                <dd className="break-words text-right">{row.speedLimitName || "不限速"}</dd>
              </dl>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!writable}
                  onClick={() => {
                    setEditing(row.id);
                    setTunnelId(String(row.tunnelId));
                    setValues(quotaValues(row));
                    setErrors({});
                  }}
                >
                  {editing === row.id ? "正在编辑" : "编辑配额"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!writable}
                  onClick={() => void rowAction(row.id, "toggle")}
                >
                  {row.status === 1 ? "禁用权限" : "启用权限"}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={!writable}
                  onClick={() => void rowAction(row.id, "reset")}
                >
                  重置流量
                </Button>
                <Button
                  type="button"
                  variant="danger"
                  size="sm"
                  disabled={!writable}
                  onClick={() => void rowAction(row.id, "remove")}
                >
                  移除
                </Button>
              </div>
              {editing === row.id && authorizationForm}
            </article>
          ))
        )}
      </section>
      {(tunnels.error || limits.error) && (
        <ErrorState
          error={tunnels.error || limits.error}
          onRetry={() => {
            void tunnels.refetch();
            void limits.refetch();
          }}
        />
      )}
      {error && <ErrorState error={error} />}
      {editing === null && authorizationForm}
    </div>
  );
}
