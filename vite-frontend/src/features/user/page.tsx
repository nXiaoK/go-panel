import { useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { Clock, MoreHorizontal, Plus, RefreshCw } from "lucide-react";
import { DataTable } from "@/components/patterns/data-table";
import { EmptyState, ErrorState } from "@/components/patterns/feedback";
import { UserMeter } from "./user-meter";
import { PageHeader } from "@/components/patterns/page-header";
import { StatusPill } from "@/components/patterns/status-pill";
import { TableToolbar } from "@/components/patterns/table-toolbar";
import { Button } from "@/components/ui/button";
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
import { useConfirm } from "@/hooks/use-confirm";
import { useSession } from "@/hooks/use-session";
import { useUrlState } from "@/hooks/use-url-state";
import { resetUserFlow, updateUser } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { formatDateTime } from "@/lib/format";
import { invalidateFor, resourceKeys } from "@/lib/query-keys";
import type { User } from "@/lib/types";
import { UserDetailSheet } from "./detail-sheet";
import { expiryState, filterUsers, userPayload, userSearchDefaults } from "./model";
import { useUsers } from "./queries";

function Identity({ user }: { user: User }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <span
        aria-hidden="true"
        className="flex size-9 shrink-0 items-center justify-center rounded-pill bg-canvas-soft text-caption-strong text-body"
      >
        {(user.name || user.user).slice(0, 2).toUpperCase()}
      </span>
      <div className="min-w-0">
        <div className="truncate text-body-strong text-ink">{user.name || user.user}</div>
        <div className="truncate text-caption text-mute">@{user.user}</div>
      </div>
    </div>
  );
}
function Expiry({ user }: { user: User }) {
  const state = expiryState(user.expTime);
  return (
    <div className="space-y-1">
      <StatusPill {...state} icon={state.tone === "warning" ? Clock : undefined} />
      <div
        className="font-mono text-caption tabular text-mute"
        title={formatDateTime(user.expTime)}
      >
        {user.expTime ? formatDateTime(user.expTime).split(" ")[0] : "未设置"}
      </div>
    </div>
  );
}
function Usage({ user }: { user: User }) {
  return (
    <div className="min-w-36">
      <UserMeter row={user} label={`${user.user}流量配额`} />
    </div>
  );
}
export function UserPage() {
  const { isAdmin } = useSession();
  const client = useQueryClient();
  const confirm = useConfirm();
  const query = useUsers();
  const users = useMemo(() => query.data ?? [], [query.data]);
  const { state, setState } = useUrlState(userSearchDefaults);
  const [sheet, setSheet] = useState<{ user: User | null } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [batchResult, setBatchResult] = useState("");
  const createHandled = useRef(false);
  const focusHandled = useRef<number | undefined>(undefined);
  const busyRef = useRef(false);
  useEffect(() => {
    if (state.create === 1 && !createHandled.current && isAdmin) {
      createHandled.current = true;
      setSheet({ user: null });
      void setState({ create: undefined });
    }
    if (state.create !== 1) createHandled.current = false;
  }, [state.create, isAdmin, setState]);
  useEffect(() => {
    if (state.focus && focusHandled.current !== state.focus) {
      focusHandled.current = state.focus;
      void setState({ q: "", status: "all", expiry: "all" });
    }
    if (!state.focus) focusHandled.current = undefined;
  }, [state.focus, setState]);
  const now = Date.now();
  const soon = users.filter(
    (u) => u.expTime && u.expTime > now && u.expTime <= now + 7 * 86400000,
  ).length;
  const visible = filterUsers(users, state.q).filter(
    (user) =>
      (state.status === "all" || String(user.status) === state.status) &&
      (state.expiry === "all" ||
        (state.expiry === "expired"
          ? Boolean(user.expTime && user.expTime <= now)
          : Boolean(user.expTime && user.expTime > now && user.expTime <= now + 7 * 86400000))),
  );
  const filtered = Boolean(state.q || state.status !== "all" || state.expiry !== "all");
  const writable = isAdmin && !query.isFetching && query.isSuccess && !busy;
  const clear = () => void setState({ q: "", status: "all", expiry: "all" });
  const sorting: SortingState = [
    { id: state.sort.replace(/-desc$/, ""), desc: state.sort.endsWith("-desc") },
  ];
  async function batch(rows: User[], action: "enable" | "disable" | "reset") {
    if (!writable || busyRef.current) return;
    if (
      action === "reset" &&
      !(await confirm({
        title: "重置用户流量",
        description: `将所选 ${rows.length} 位用户的入站与出站流量计数清零，操作不可撤销`,
        confirmText: "重置流量",
        tone: "danger",
      }))
    )
      return;
    const state = client.getQueryState<User[]>(resourceKeys.user.list());
    if (state?.status !== "success" || state.fetchStatus !== "idle") {
      setError("用户数据正在更新，请刷新后重试");
      return;
    }
    const current = rows
      .map((row) => state.data?.find((user) => user.id === row.id))
      .filter((row): row is User => Boolean(row));
    busyRef.current = true;
    setBusy(true);
    setError("");
    setBatchResult("");
    let successes = 0;
    const failures: string[] = [];
    for (const row of current) {
      try {
        if (action === "reset") await resetUserFlow({ id: row.id, type: 1 });
        else await updateUser(userPayload(row, action === "enable" ? 1 : 0));
        successes += 1;
      } catch (cause) {
        failures.push(`${row.user}：${errorMessage(cause)}`);
      }
    }
    await invalidateFor(client, action === "reset" ? "user.reset" : "user.update");
    setBatchResult(`成功 ${successes} 位，失败 ${failures.length} 位`);
    if (failures.length) setError(failures.join("；"));
    busyRef.current = false;
    setBusy(false);
  }
  const actions = (user: User) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={`用户操作：${user.user}`}>
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => setSheet({ user })}>查看详情</DropdownMenuItem>
        <DropdownMenuItem
          disabled={!writable}
          onClick={() => void batch([user], user.status === 1 ? "disable" : "enable")}
        >
          {user.status === 1 ? "禁用用户" : "启用用户"}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled={!writable} onClick={() => void batch([user], "reset")}>
          重置流量
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
  const columns: ColumnDef<User>[] = [
    {
      id: "user",
      accessorFn: (u) => u.name || u.user,
      header: "用户",
      cell: ({ row }) => <Identity user={row.original} />,
    },
    {
      id: "status",
      header: "状态",
      enableSorting: false,
      cell: ({ row }) => (
        <StatusPill
          tone={row.original.status === 1 ? "good" : "neutral"}
          hollow={row.original.status !== 1}
          label={row.original.status === 1 ? "正常" : "已禁用"}
        />
      ),
    },
    {
      id: "flow",
      accessorFn: (u) => (u.inFlow ?? 0) + (u.outFlow ?? 0),
      header: "流量",
      meta: { align: "right", numeric: true },
      cell: ({ row }) => <Usage user={row.original} />,
    },
    {
      id: "num",
      header: "转发上限",
      enableSorting: false,
      meta: { align: "right", numeric: true },
      cell: ({ row }) => row.original.num,
    },
    {
      id: "reset",
      header: "重置日",
      enableSorting: false,
      cell: ({ row }) => (
        <span className="font-mono text-caption text-body">
          {row.original.flowResetTime ? `每月 ${row.original.flowResetTime} 日` : "不重置"}
        </span>
      ),
    },
    {
      id: "expiry",
      accessorFn: (u) => u.expTime ?? 0,
      header: "到期",
      cell: ({ row }) => <Expiry user={row.original} />,
    },
    {
      id: "actions",
      header: "操作",
      enableSorting: false,
      meta: { actions: true },
      cell: ({ row }) => actions(row.original),
    },
  ];
  if (!isAdmin) return <EmptyState title="需要管理员权限" description="当前账号无法管理用户" />;
  return (
    <div className="min-w-0 space-y-6">
      <PageHeader
        title="用户"
        description="管理账号、配额和隧道权限"
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              <RefreshCw aria-hidden="true" />
              刷新
            </Button>
            <Button type="button" onClick={() => setSheet({ user: null })}>
              <Plus aria-hidden="true" />
              新增用户
            </Button>
          </>
        }
      />
      <TableToolbar
        searchValue={state.q}
        onSearchChange={(q) => void setState({ q, focus: undefined })}
        searchPlaceholder="用户名 / 昵称"
        filters={
          <>
            <Select
              value={state.status}
              onValueChange={(status) => void setState({ status, focus: undefined })}
            >
              <SelectTrigger aria-label="用户状态" className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">状态：全部</SelectItem>
                <SelectItem value="1">正常</SelectItem>
                <SelectItem value="0">已禁用</SelectItem>
              </SelectContent>
            </Select>
            <Select
              value={state.expiry}
              onValueChange={(expiry) => void setState({ expiry, focus: undefined })}
            >
              <SelectTrigger aria-label="到期筛选" className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">到期：全部</SelectItem>
                <SelectItem value="soon">7 天内</SelectItem>
                <SelectItem value="expired">已到期</SelectItem>
              </SelectContent>
            </Select>
          </>
        }
        count={
          <>
            <span>共 {visible.length} 位</span>
            <button
              type="button"
              className="min-h-10 text-body underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:ring-primary-soft"
              onClick={() => void setState({ expiry: "soon", focus: undefined })}
            >
              即将到期 {soon}
            </button>
          </>
        }
      />
      {error && <ErrorState error={error} />}
      {batchResult && (
        <p role="status" className="text-caption text-body">
          {batchResult}
        </p>
      )}
      <DataTable
        data={visible}
        columns={columns}
        getRowId={(u) => String(u.id)}
        ariaLabel="用户列表"
        loading={query.isPending}
        error={query.error}
        onRetry={() => void query.refetch()}
        enableSelection
        bulkActions={(rows) => (
          <>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!writable}
              onClick={() => void batch(rows, "enable")}
            >
              启用
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!writable}
              onClick={() => void batch(rows, "disable")}
            >
              禁用
            </Button>
            <Button
              type="button"
              variant="danger"
              size="sm"
              disabled={!writable}
              onClick={() => void batch(rows, "reset")}
            >
              重置流量
            </Button>
          </>
        )}
        sorting={sorting}
        onSortingChange={(change) => {
          const next = typeof change === "function" ? change(sorting) : change;
          void setState({
            sort: next.length ? `${next[0].id}${next[0].desc ? "-desc" : ""}` : "user",
          });
        }}
        focusRowId={state.focus ? String(state.focus) : undefined}
        onRowActivate={(user) => setSheet({ user })}
        filtered={filtered}
        onClearFilters={clear}
        emptyState={
          filtered ? (
            <EmptyState
              title="没有匹配的用户"
              description="试试其他搜索词或筛选条件"
              action={
                <Button type="button" variant="outline" onClick={clear}>
                  清除筛选
                </Button>
              }
            />
          ) : (
            <EmptyState
              title="还没有用户"
              description="新增账号并分配隧道后即可使用转发"
              action={
                <Button type="button" onClick={() => setSheet({ user: null })}>
                  新增用户
                </Button>
              }
            />
          )
        }
        renderMobileCard={(user) => (
          <div className="space-y-4">
            <div className="flex min-w-0 items-center justify-between gap-2">
              <Identity user={user} />
              {actions(user)}
            </div>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <StatusPill
                tone={user.status === 1 ? "good" : "neutral"}
                hollow={user.status !== 1}
                label={user.status === 1 ? "正常" : "已禁用"}
              />
              <Expiry user={user} />
            </div>
            <Usage user={user} />
            <div className="flex flex-wrap justify-between gap-2 text-caption text-body">
              <span>
                转发上限 <span className="font-mono">{user.num}</span>
              </span>
              <span>{user.flowResetTime ? `每月 ${user.flowResetTime} 日重置` : "不重置"}</span>
            </div>
          </div>
        )}
      />
      {sheet && (
        <UserDetailSheet
          key={sheet.user?.id ?? "create"}
          user={sheet.user}
          onClose={() => setSheet(null)}
        />
      )}
    </div>
  );
}
