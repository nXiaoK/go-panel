import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  KeyRound,
  MoreHorizontal,
  Network as NetworkIcon,
  Plus,
  RefreshCcw,
  RotateCcw,
  Search,
  ShieldCheck,
  Trash2,
  UserCog,
  Users,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Progress } from "@/components/ui/progress";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  createUser,
  deleteUser,
  getAllUsers,
  getSpeedLimitList,
  getTunnelList,
  assignUserTunnel,
  getUserTunnelList,
  removeUserTunnel,
  updateUser,
  updateUserTunnel,
  resetUserFlow,
} from "@/lib/api";
import type {
  SpeedLimit,
  Tunnel,
  User as ApiUser,
  UserForm,
  UserTunnel,
  UserTunnelForm,
} from "@/lib/types";
import { useUserTunnels } from "@/hooks/use-user-tunnels";
import { filterUsers } from "@/lib/user-search";
import { invalidateGlobalSearch } from "@/lib/search-cache";
import { listData, queries, unwrap } from "@/lib/api/query";
import { KpiCard, PageHeader, QueryErrorNotice } from "@/components/page";

export const Route = createFileRoute("/_app/user")({
  head: () => ({ meta: [{ title: "用户管理 · Flux Panel" }] }),
  component: UserPage,
});

// ---------- helpers ----------
function formatBytes(v: number): string {
  if (!v || v <= 0) return "0 B";
  if (v < 1024) return `${v} B`;
  if (v < 1024 ** 2) return `${(v / 1024).toFixed(2)} KB`;
  if (v < 1024 ** 3) return `${(v / 1024 ** 2).toFixed(2)} MB`;
  return `${(v / 1024 ** 3).toFixed(2)} GB`;
}
function formatDate(ts?: number | null) {
  if (!ts) return "—";
  return new Date(ts).toLocaleString();
}
function initials(name: string) {
  return name.replace(/\s+/g, "").slice(0, 2).toUpperCase() || "U";
}
function toDateInput(ts?: number | null) {
  if (!ts) return "";
  const d = new Date(ts);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}
function fromDateInput(s: string): Date | null {
  if (!s) return null;
  const d = new Date(s + "T23:59:59");
  return isNaN(d.getTime()) ? null : d;
}
function getExpStatus(exp?: number) {
  if (!exp) return null;
  const now = Date.now();
  if (exp < now)
    return { text: "已过期", cls: "text-destructive bg-destructive/10 border-destructive/30" };
  const days = Math.ceil((exp - now) / 86400000);
  if (days <= 7)
    return { text: `${days}天后过期`, cls: "text-warning bg-warning/10 border-warning/30" };
  return { text: "有效", cls: "text-success bg-success/10 border-success/30" };
}

// ---------- component ----------
function UserPage() {
  const queryClient = useQueryClient();
  const [q, setQ] = useState("");

  // user form
  const [userDlgOpen, setUserDlgOpen] = useState(false);
  const [isEdit, setIsEdit] = useState(false);
  const [userForm, setUserForm] = useState<UserForm>({
    user: "",
    pwd: "",
    status: 1,
    flow: 100,
    num: 10,
    expTime: null,
    flowResetTime: 0,
  });

  // delete + reset dialogs
  const [toDelete, setToDelete] = useState<ApiUser | null>(null);
  const [toReset, setToReset] = useState<ApiUser | null>(null);

  // tunnel-permissions modal
  const [tunnelMgrOpen, setTunnelMgrOpen] = useState(false);
  const userTunnelManager = useUserTunnels<UserTunnel, ApiUser>(
    useCallback((userId: number) => getUserTunnelList({ userId }), []),
    useCallback((message: string) => toast.error(message), []),
  );
  const tunnelMgrUser = userTunnelManager.activeUser;
  const userTunnels = userTunnelManager.rows;
  const utLoading = userTunnelManager.loading;
  const [tunnelForm, setTunnelForm] = useState<UserTunnelForm>({
    tunnelId: null,
    flow: 100,
    num: 10,
    expTime: null,
    flowResetTime: 0,
    speedId: null,
  });
  const [assigning, setAssigning] = useState(false);
  const [utToDelete, setUtToDelete] = useState<UserTunnel | null>(null);
  const [utToReset, setUtToReset] = useState<UserTunnel | null>(null);

  // 一次性加载用户，本地过滤（见 visibleUsers），不再按关键字反复请求。
  const usersQuery = useQuery({
    queryKey: queries.user.list({ current: 1, size: 200 }),
    queryFn: () => getAllUsers({ current: 1, size: 200 }).then(listData<ApiUser>),
  });
  const users = useMemo(() => usersQuery.data ?? [], [usersQuery.data]);
  const loading = usersQuery.isPending;

  const tunnelsQuery = useQuery({
    queryKey: queries.tunnel.list(),
    queryFn: () => getTunnelList().then(listData<Tunnel>),
  });
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);

  const speedLimitsQuery = useQuery({
    queryKey: queries.limit.list(),
    queryFn: () => getSpeedLimitList().then(listData<SpeedLimit>),
  });
  const speedLimits = useMemo(() => speedLimitsQuery.data ?? [], [speedLimitsQuery.data]);

  const invalidateUsers = () => {
    void queryClient.invalidateQueries({ queryKey: ["user"] });
    void queryClient.invalidateQueries({ queryKey: queries.forward.all });
    void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
  };

  const refresh = () => {
    invalidateUsers();
    void queryClient.invalidateQueries({ queryKey: queries.tunnel.list() });
    void queryClient.invalidateQueries({ queryKey: queries.limit.list() });
  };

  const saveUserMutation = useMutation({
    mutationFn: ({ payload, isEdit }: { payload: Record<string, unknown>; isEdit: boolean }) =>
      (isEdit ? updateUser(payload) : createUser(payload)).then(unwrap),
    onSuccess: (_data, { isEdit }) => {
      invalidateGlobalSearch("user");
      toast.success(isEdit ? "更新成功" : "创建成功");
      setUserDlgOpen(false);
      invalidateUsers();
    },
    onError: (error: Error) => toast.error(error.message || "保存失败"),
  });

  const deleteUserMutation = useMutation({
    mutationFn: (id: number) => deleteUser(id).then(unwrap),
    onSuccess: () => {
      invalidateGlobalSearch("user");
      toast.success("删除成功");
      invalidateUsers();
    },
    onError: (error: Error) => toast.error(error.message || "删除失败"),
  });

  const toggleStatusMutation = useMutation({
    mutationFn: (u: ApiUser) =>
      updateUser({
        id: u.id,
        user: u.user,
        name: u.name,
        status: u.status === 1 ? 0 : 1,
        flow: u.flow,
        num: u.num,
        expTime: u.expTime,
        flowResetTime: u.flowResetTime ?? 0,
      }).then(unwrap),
    onSuccess: () => {
      toast.success("状态已更新");
      invalidateUsers();
    },
    onError: (error: Error) => toast.error(error.message || "更新失败"),
  });

  const resetFlowMutation = useMutation({
    mutationFn: (id: number) => resetUserFlow({ id, type: 1 }).then(unwrap),
    onSuccess: () => {
      toast.success("流量重置成功");
      invalidateUsers();
    },
    onError: (error: Error) => toast.error(error.message || "重置失败"),
  });

  const openCreate = () => {
    setIsEdit(false);
    setUserForm({
      user: "",
      pwd: "",
      status: 1,
      flow: 100,
      num: 10,
      expTime: null,
      flowResetTime: 0,
    });
    setUserDlgOpen(true);
  };
  const openEdit = (u: ApiUser) => {
    setIsEdit(true);
    setUserForm({
      id: u.id,
      name: u.name,
      user: u.user,
      pwd: "",
      status: u.status,
      flow: u.flow,
      num: u.num,
      expTime: u.expTime ? new Date(u.expTime) : null,
      flowResetTime: u.flowResetTime ?? 0,
    });
    setUserDlgOpen(true);
  };

  const submitUser = async () => {
    if (!userForm.user || (!isEdit && !userForm.pwd) || !userForm.expTime) {
      toast.error("请填写完整信息（用户名、密码、过期时间）");
      return;
    }
    const payload: any = { ...userForm, expTime: userForm.expTime.getTime() };
    if (isEdit && !payload.pwd) delete payload.pwd;
    saveUserMutation.mutate({ payload, isEdit });
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    deleteUserMutation.mutate(toDelete.id);
    setToDelete(null);
  };

  const toggleStatus = async (u: ApiUser) => {
    toggleStatusMutation.mutate(u);
  };

  const confirmResetFlow = async () => {
    if (!toReset) return;
    resetFlowMutation.mutate(toReset.id);
    setToReset(null);
  };

  // tunnel-permissions
  const loadUserTunnels = () => userTunnelManager.reload();
  const openTunnelMgr = (u: ApiUser) => {
    setTunnelForm({
      tunnelId: null,
      flow: 100,
      num: 10,
      expTime: null,
      flowResetTime: 0,
      speedId: null,
    });
    setTunnelMgrOpen(true);
    userTunnelManager.open(u);
  };
  const closeTunnelMgr = () => {
    setTunnelMgrOpen(false);
    userTunnelManager.close();
  };
  const assignTunnel = async () => {
    if (!tunnelMgrUser || !tunnelForm.tunnelId || !tunnelForm.expTime) {
      toast.error("请选择隧道并填写过期时间");
      return;
    }
    setAssigning(true);
    try {
      const res = await assignUserTunnel({
        userId: tunnelMgrUser.id,
        tunnelId: tunnelForm.tunnelId,
        flow: tunnelForm.flow,
        num: tunnelForm.num,
        expTime: tunnelForm.expTime.getTime(),
        flowResetTime: tunnelForm.flowResetTime,
        speedId: tunnelForm.speedId,
      });
      if (res.code === 0) {
        toast.success("分配成功");
        setTunnelForm({
          tunnelId: null,
          flow: 100,
          num: 10,
          expTime: null,
          flowResetTime: 0,
          speedId: null,
        });
        loadUserTunnels();
      } else toast.error(res.msg || "分配失败");
    } catch {
      toast.error("分配失败");
    } finally {
      setAssigning(false);
    }
  };
  const confirmRemoveUt = async () => {
    if (!utToDelete) return;
    try {
      const res = await removeUserTunnel({ id: utToDelete.id });
      if (res.code === 0) {
        toast.success("已移除");
        if (tunnelMgrUser) loadUserTunnels();
      } else toast.error(res.msg || "删除失败");
    } catch {
      toast.error("删除失败");
    } finally {
      setUtToDelete(null);
    }
  };
  const confirmResetUt = async () => {
    if (!utToReset) return;
    try {
      const res = await resetUserFlow({ id: utToReset.id, type: 2 });
      if (res.code === 0) {
        toast.success("隧道流量已重置");
        if (tunnelMgrUser) loadUserTunnels();
      } else toast.error(res.msg || "重置失败");
    } catch {
      toast.error("重置失败");
    } finally {
      setUtToReset(null);
    }
  };
  const toggleUtStatus = async (ut: UserTunnel) => {
    try {
      const res = await updateUserTunnel({
        id: ut.id,
        flow: ut.flow,
        num: ut.num,
        expTime: ut.expTime,
        flowResetTime: ut.flowResetTime,
        speedId: ut.speedId,
        status: ut.status === 1 ? 0 : 1,
      });
      if (res.code === 0) {
        toast.success("状态已更新");
        if (tunnelMgrUser) loadUserTunnels();
      } else toast.error(res.msg || "更新失败");
    } catch {
      toast.error("更新失败");
    }
  };

  const stats = useMemo(
    () => [
      { label: "用户总数", value: users.length, icon: Users },
      { label: "正常", value: users.filter((u) => u.status === 1).length, icon: ShieldCheck },
      { label: "禁用", value: users.filter((u) => u.status !== 1).length, icon: KeyRound },
      {
        label: "即将过期(7天)",
        value: users.filter((u) => {
          if (!u.expTime) return false;
          const d = u.expTime - Date.now();
          return d > 0 && d < 7 * 86400000;
        }).length,
        icon: RotateCcw,
      },
    ],
    [users],
  );

  const tunnelsArr = Array.isArray(tunnels) ? tunnels : [];
  const userTunnelsArr = Array.isArray(userTunnels) ? userTunnels : [];
  const speedLimitsArr = Array.isArray(speedLimits) ? speedLimits : [];
  const availableTunnels = tunnelsArr.filter(
    (t) => !userTunnelsArr.some((ut) => ut.tunnelId === t.id),
  );
  const availableSpeed = speedLimitsArr.filter((s) => s.tunnelId === tunnelForm.tunnelId);
  const visibleUsers = useMemo(() => filterUsers(users, q), [users, q]);

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow={
          <>
            <Users className="h-3.5 w-3.5" /> users
          </>
        }
        title="用户管理"
        description="管理账号、流量配额、转发数量、过期时间与隧道权限"
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              className="gap-2"
              onClick={refresh}
              disabled={
                usersQuery.isFetching || tunnelsQuery.isFetching || speedLimitsQuery.isFetching
              }
            >
              <RefreshCcw
                className={`h-4 w-4 ${usersQuery.isFetching || tunnelsQuery.isFetching || speedLimitsQuery.isFetching ? "animate-spin" : ""}`}
              />{" "}
              刷新
            </Button>
            <Button size="sm" className="gap-2 shadow-glow" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增用户
            </Button>
          </>
        }
      />

      <QueryErrorNotice
        error={usersQuery.error ?? tunnelsQuery.error ?? speedLimitsQuery.error}
        onRetry={() => {
          void usersQuery.refetch();
          void tunnelsQuery.refetch();
          void speedLimitsQuery.refetch();
        }}
      />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {stats.map((s) => (
          <KpiCard
            key={s.label}
            label={s.label}
            value={s.value}
            icon={<s.icon className="h-4 w-4" />}
            tone={s.label === "正常" ? "ok" : s.label === "禁用" ? "warn" : undefined}
          />
        ))}
      </div>

      <Card className="border-border/60 bg-card/40 shadow-card">
        <CardContent className="space-y-4 p-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="搜索用户名 / 昵称"
                className="h-9 w-64 pl-8"
              />
            </div>
            <div className="text-xs text-muted-foreground">
              {loading ? "加载中..." : `共 ${visibleUsers.length} 位用户`}
            </div>
          </div>

          <div className="overflow-x-auto rounded-lg border border-border/60">
            <Table className="min-w-[820px]">
              <TableHeader>
                <TableRow className="border-border/60 bg-muted/30 hover:bg-muted/30">
                  <TableHead>用户</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>流量 / 配额</TableHead>
                  <TableHead className="text-right">转发数</TableHead>
                  <TableHead>重置日</TableHead>
                  <TableHead>过期时间</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleUsers.map((u) => {
                  const used = (u.inFlow || 0) + (u.outFlow || 0);
                  const cap = u.flow * 1024 ** 3;
                  const pct = cap > 0 ? Math.min(100, (used / cap) * 100) : 0;
                  const exp = getExpStatus(u.expTime);
                  return (
                    <TableRow key={u.id} className="border-border/40 hover:bg-muted/20">
                      <TableCell>
                        <div className="flex items-center gap-3">
                          <Avatar className="h-9 w-9 border border-border/60">
                            <AvatarFallback className="bg-gradient-primary text-xs font-medium text-primary-foreground">
                              {initials(u.name || u.user)}
                            </AvatarFallback>
                          </Avatar>
                          <div className="min-w-0">
                            <div className="truncate text-sm font-medium">{u.name || u.user}</div>
                            <div className="truncate font-mono text-xs text-muted-foreground">
                              @{u.user}
                            </div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <span
                          className={`inline-flex rounded-full border px-2 py-0.5 text-xs ${
                            u.status === 1
                              ? "text-success bg-success/10 border-success/30"
                              : "text-destructive bg-destructive/10 border-destructive/30"
                          }`}
                        >
                          {u.status === 1 ? "正常" : "禁用"}
                        </span>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-3">
                          <Progress
                            value={pct}
                            className={`h-1.5 w-24 ${pct > 90 ? "[&>div]:bg-destructive" : ""}`}
                          />
                          <span className="font-mono text-xs text-muted-foreground">
                            {formatBytes(used)} / {u.flow} GB
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs">{u.num}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {u.flowResetTime ? `每月 ${u.flowResetTime} 号` : "不重置"}
                      </TableCell>
                      <TableCell>
                        {exp ? (
                          <span
                            className={`inline-flex rounded-full border px-2 py-0.5 text-xs ${exp.cls}`}
                          >
                            {exp.text}
                          </span>
                        ) : (
                          <span className="text-xs text-muted-foreground">—</span>
                        )}
                        <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                          {formatDate(u.expTime)}
                        </div>
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-8 w-8"
                              aria-label="用户操作"
                            >
                              <MoreHorizontal className="h-4 w-4" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => openEdit(u)}>
                              <UserCog className="mr-2 h-4 w-4" /> 编辑
                            </DropdownMenuItem>
                            <DropdownMenuItem onClick={() => openTunnelMgr(u)}>
                              <NetworkIcon className="mr-2 h-4 w-4" /> 隧道权限
                            </DropdownMenuItem>
                            <DropdownMenuItem onClick={() => setToReset(u)}>
                              <RotateCcw className="mr-2 h-4 w-4" /> 重置流量
                            </DropdownMenuItem>
                            <DropdownMenuItem onClick={() => toggleStatus(u)}>
                              <ShieldCheck className="mr-2 h-4 w-4" />
                              {u.status === 1 ? "禁用" : "启用"}
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              className="text-destructive focus:text-destructive"
                              onClick={() => setToDelete(u)}
                            >
                              <Trash2 className="mr-2 h-4 w-4" /> 删除
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  );
                })}
                {!loading && users.length === 0 && (
                  <TableRow>
                    <TableCell
                      colSpan={7}
                      className="py-10 text-left text-sm text-muted-foreground lg:text-center"
                    >
                      暂无用户
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      {/* User form dialog */}
      <Dialog open={userDlgOpen} onOpenChange={setUserDlgOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{isEdit ? "编辑用户" : "新增用户"}</DialogTitle>
            <DialogDescription>
              {isEdit ? "更新用户信息，留空密码则保持不变。" : "创建新账号并分配配额。"}
            </DialogDescription>
          </DialogHeader>
          <div className="grid grid-cols-2 gap-3">
            <div className="col-span-2 space-y-1.5">
              <Label htmlFor="user-form-username">用户名</Label>
              <Input
                id="user-form-username"
                value={userForm.user}
                disabled={isEdit}
                onChange={(e) => setUserForm({ ...userForm, user: e.target.value })}
              />
            </div>
            <div className="col-span-2 space-y-1.5">
              <Label htmlFor="user-form-name">昵称</Label>
              <Input
                id="user-form-name"
                value={userForm.name || ""}
                onChange={(e) => setUserForm({ ...userForm, name: e.target.value })}
              />
            </div>
            <div className="col-span-2 space-y-1.5">
              <Label htmlFor="user-form-password">
                密码{" "}
                {isEdit && <span className="text-xs text-muted-foreground">（留空不修改）</span>}
              </Label>
              <Input
                id="user-form-password"
                type="password"
                value={userForm.pwd || ""}
                onChange={(e) => setUserForm({ ...userForm, pwd: e.target.value })}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="user-form-flow">流量限制 (GB)</Label>
              <Input
                id="user-form-flow"
                type="number"
                min={0}
                value={userForm.flow}
                onChange={(e) => setUserForm({ ...userForm, flow: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="user-form-num">转发数量</Label>
              <Input
                id="user-form-num"
                type="number"
                min={0}
                value={userForm.num}
                onChange={(e) => setUserForm({ ...userForm, num: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="user-form-exp">过期时间</Label>
              <Input
                id="user-form-exp"
                type="date"
                value={toDateInput(userForm.expTime?.getTime())}
                onChange={(e) =>
                  setUserForm({ ...userForm, expTime: fromDateInput(e.target.value) })
                }
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="user-form-reset-day">重置日 (0 = 不重置)</Label>
              <Input
                id="user-form-reset-day"
                type="number"
                min={0}
                max={31}
                value={userForm.flowResetTime}
                onChange={(e) =>
                  setUserForm({ ...userForm, flowResetTime: Number(e.target.value) })
                }
              />
            </div>
            <div className="col-span-2 flex items-center justify-between rounded-md border border-border/60 p-3">
              <div>
                <div className="text-sm font-medium" id="user-form-status-label">
                  启用账号
                </div>
                <div className="text-xs text-muted-foreground">禁用后该用户无法登录与使用转发</div>
              </div>
              <Switch
                checked={userForm.status === 1}
                onCheckedChange={(v) => setUserForm({ ...userForm, status: v ? 1 : 0 })}
                aria-labelledby="user-form-status-label"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setUserDlgOpen(false)}>
              取消
            </Button>
            <Button onClick={submitUser} disabled={saveUserMutation.isPending}>
              {saveUserMutation.isPending ? "保存中..." : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete user */}
      <AlertDialog open={!!toDelete} onOpenChange={(o) => !o && setToDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除用户</AlertDialogTitle>
            <AlertDialogDescription>
              确定删除用户 <b>{toDelete?.user}</b>
              ？该账号下的所有隧道权限与转发规则将一并移除，且无法恢复。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmDelete}
              className="bg-destructive hover:bg-destructive/90"
            >
              确认删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Reset user flow */}
      <AlertDialog open={!!toReset} onOpenChange={(o) => !o && setToReset(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>重置流量</AlertDialogTitle>
            <AlertDialogDescription>
              将用户 <b>{toReset?.user}</b> 的入站/出站流量计数清零，操作不可撤销。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={confirmResetFlow}>确认重置</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Tunnel permissions dialog */}
      <Dialog open={tunnelMgrOpen} onOpenChange={(o) => !o && closeTunnelMgr()}>
        <DialogContent className="max-w-4xl">
          <DialogHeader>
            <DialogTitle>隧道权限 · {tunnelMgrUser?.user}</DialogTitle>
            <DialogDescription>
              为该用户分配可访问的隧道，并按隧道设置独立配额与限速。
            </DialogDescription>
          </DialogHeader>

          {/* assign form */}
          <div className="grid grid-cols-6 gap-3 rounded-md border border-border/60 p-3">
            <div className="col-span-2 space-y-1.5">
              <Label>隧道</Label>
              <Select
                value={tunnelForm.tunnelId ? String(tunnelForm.tunnelId) : ""}
                onValueChange={(v) =>
                  setTunnelForm({ ...tunnelForm, tunnelId: Number(v), speedId: null })
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder="选择隧道" />
                </SelectTrigger>
                <SelectContent>
                  {availableTunnels.map((t) => (
                    <SelectItem key={t.id} value={String(t.id)}>
                      {t.name}
                    </SelectItem>
                  ))}
                  {availableTunnels.length === 0 && (
                    <div className="px-2 py-1.5 text-xs text-muted-foreground">已全部分配</div>
                  )}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>流量 (GB)</Label>
              <Input
                type="number"
                min={0}
                value={tunnelForm.flow}
                onChange={(e) => setTunnelForm({ ...tunnelForm, flow: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-1.5">
              <Label>转发数</Label>
              <Input
                type="number"
                min={0}
                value={tunnelForm.num}
                onChange={(e) => setTunnelForm({ ...tunnelForm, num: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-1.5">
              <Label>过期</Label>
              <Input
                type="date"
                value={toDateInput(tunnelForm.expTime?.getTime())}
                onChange={(e) =>
                  setTunnelForm({ ...tunnelForm, expTime: fromDateInput(e.target.value) })
                }
              />
            </div>
            <div className="space-y-1.5">
              <Label>重置日</Label>
              <Input
                type="number"
                min={0}
                max={31}
                value={tunnelForm.flowResetTime}
                onChange={(e) =>
                  setTunnelForm({ ...tunnelForm, flowResetTime: Number(e.target.value) })
                }
              />
            </div>
            <div className="col-span-4 space-y-1.5">
              <Label>限速规则（可选）</Label>
              <Select
                value={tunnelForm.speedId ? String(tunnelForm.speedId) : "none"}
                onValueChange={(v) =>
                  setTunnelForm({ ...tunnelForm, speedId: v === "none" ? null : Number(v) })
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder="不限速" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">不限速</SelectItem>
                  {availableSpeed.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {s.name} · {s.speed} Mbps
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="col-span-2 flex items-end justify-end">
              <Button onClick={assignTunnel} disabled={assigning} className="w-full">
                <Plus className="mr-2 h-4 w-4" />
                {assigning ? "分配中..." : "分配"}
              </Button>
            </div>
          </div>

          {/* current permissions */}
          <div className="overflow-x-auto rounded-lg border border-border/60">
            <Table className="min-w-[760px]">
              <TableHeader>
                <TableRow className="bg-muted/30 hover:bg-muted/30">
                  <TableHead>隧道</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>流量</TableHead>
                  <TableHead className="text-right">转发数</TableHead>
                  <TableHead>过期</TableHead>
                  <TableHead>限速</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {utLoading ? (
                  <TableRow>
                    <TableCell
                      colSpan={7}
                      className="py-8 text-left text-sm text-muted-foreground lg:text-center"
                    >
                      加载中...
                    </TableCell>
                  </TableRow>
                ) : userTunnelsArr.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={7}
                      className="py-8 text-left text-sm text-muted-foreground lg:text-center"
                    >
                      尚未分配任何隧道
                    </TableCell>
                  </TableRow>
                ) : (
                  userTunnelsArr.map((ut) => {
                    const used = (ut.inFlow || 0) + (ut.outFlow || 0);
                    const cap = ut.flow * 1024 ** 3;
                    const pct = cap > 0 ? Math.min(100, (used / cap) * 100) : 0;
                    return (
                      <TableRow key={ut.id}>
                        <TableCell className="text-sm font-medium">{ut.tunnelName}</TableCell>
                        <TableCell>
                          <span
                            className={`inline-flex rounded-full border px-2 py-0.5 text-xs ${
                              ut.status === 1
                                ? "text-success bg-success/10 border-success/30"
                                : "text-destructive bg-destructive/10 border-destructive/30"
                            }`}
                          >
                            {ut.status === 1 ? "启用" : "禁用"}
                          </span>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center gap-2">
                            <Progress value={pct} className="h-1.5 w-20" />
                            <span className="font-mono text-[11px] text-muted-foreground">
                              {formatBytes(used)}/{ut.flow}GB
                            </span>
                          </div>
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs">{ut.num}</TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">
                          {formatDate(ut.expTime)}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">
                          {ut.speedLimitName || "不限速"}
                        </TableCell>
                        <TableCell>
                          <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8"
                                aria-label={`用户隧道操作：${ut.tunnelName}`}
                              >
                                <MoreHorizontal className="h-4 w-4" />
                              </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                              <DropdownMenuItem onClick={() => toggleUtStatus(ut)}>
                                <ShieldCheck className="mr-2 h-4 w-4" />
                                {ut.status === 1 ? "禁用" : "启用"}
                              </DropdownMenuItem>
                              <DropdownMenuItem onClick={() => setUtToReset(ut)}>
                                <RotateCcw className="mr-2 h-4 w-4" /> 重置流量
                              </DropdownMenuItem>
                              <DropdownMenuSeparator />
                              <DropdownMenuItem
                                className="text-destructive focus:text-destructive"
                                onClick={() => setUtToDelete(ut)}
                              >
                                <Trash2 className="mr-2 h-4 w-4" /> 移除
                              </DropdownMenuItem>
                            </DropdownMenuContent>
                          </DropdownMenu>
                        </TableCell>
                      </TableRow>
                    );
                  })
                )}
              </TableBody>
            </Table>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={closeTunnelMgr}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* remove ut */}
      <AlertDialog open={!!utToDelete} onOpenChange={(o) => !o && setUtToDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>移除隧道权限</AlertDialogTitle>
            <AlertDialogDescription>
              移除 <b>{utToDelete?.tunnelName}</b> 权限后，该用户下相关转发规则将同时失效。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmRemoveUt}
              className="bg-destructive hover:bg-destructive/90"
            >
              确认移除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* reset ut flow */}
      <AlertDialog open={!!utToReset} onOpenChange={(o) => !o && setUtToReset(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>重置隧道流量</AlertDialogTitle>
            <AlertDialogDescription>
              将该用户在 <b>{utToReset?.tunnelName}</b> 上的流量计数清零。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={confirmResetUt}>确认重置</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
