import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { UserCircle2, KeyRound, LogOut, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { Separator } from "@/components/ui/separator";
import { updatePassword, getUserPackageInfo } from "@/lib/api";
import { queries, unwrap } from "@/lib/api/query";
import { clearSession } from "@/lib/session";
import { PageHeader, QueryErrorNotice } from "@/components/page";

export const Route = createFileRoute("/_app/profile")({
  head: () => ({ meta: [{ title: "个人资料 · Flux Panel" }] }),
  component: ProfilePage,
});

function roleLabel(id: string | null) {
  if (id === "0") return { label: "超级管理员", tone: "text-primary" };
  if (id === "1") return { label: "普通用户", tone: "text-muted-foreground" };
  if (id === "2") return { label: "运维", tone: "text-blue-400" };
  return { label: "普通用户", tone: "text-muted-foreground" };
}
function fmtBytes(n: number) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(2)} ${u[i]}`;
}

type PackageSummary = { flow: number; num: number; used: number };

function normalizePackageInfo(data: any): PackageSummary | null {
  if (!data) return null;
  const userInfo = data.userInfo || data;
  const flow = Number(userInfo.flow ?? data.flow ?? 0);
  const num = Number(userInfo.num ?? data.num ?? 0);
  const used =
    data.used !== undefined
      ? Number(data.used)
      : (Number(userInfo.inFlow ?? 0) + Number(userInfo.outFlow ?? 0)) / 1024 ** 3;
  return {
    flow: Number.isFinite(flow) ? flow : 0,
    num: Number.isFinite(num) ? num : 0,
    used: Number.isFinite(used) ? used : 0,
  };
}

function ProfilePage() {
  const navigate = useNavigate();
  const [name, setName] = useState("admin");
  const [newUsername, setNewUsername] = useState("admin");
  const [roleId, setRoleId] = useState<string | null>("1");
  const [oldPwd, setOldPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");

  useEffect(() => {
    if (typeof window === "undefined") return;
    const storedName = window.localStorage.getItem("name") || "admin";
    setName(storedName);
    setNewUsername(storedName);
    setRoleId(window.localStorage.getItem("role_id"));
  }, []);

  const packageQuery = useQuery({
    queryKey: queries.user.package(),
    queryFn: () => getUserPackageInfo().then(unwrap),
  });
  const pkg = normalizePackageInfo(packageQuery.data ?? null);

  const passwordMutation = useMutation({
    mutationFn: (payload: Record<string, unknown>) => updatePassword(payload).then(unwrap),
    onSuccess: () => {
      toast.success("密码已更新，请重新登录");
      setOldPwd("");
      setNewPwd("");
      setConfirmPwd("");
      setName(newUsername.trim());
      clearSession("password changed");
      setTimeout(() => navigate({ to: "/login" }), 800);
    },
    onError: (error: Error) => toast.error(error.message || "修改失败"),
  });

  const role = roleLabel(roleId);
  const initials = name.slice(0, 2).toUpperCase();
  const usedGB = pkg ? pkg.used : 0;
  const totalGB = pkg ? pkg.flow : 0;
  const pct = totalGB > 0 ? Math.min(100, Math.round((usedGB / totalGB) * 100)) : 0;

  const submit = async () => {
    if (!newUsername.trim()) return toast.error("请填写用户名");
    if (!oldPwd || !newPwd) return toast.error("请填写原密码与新密码");
    if (newPwd.length < 6) return toast.error("新密码至少 6 位");
    if (newPwd !== confirmPwd) return toast.error("两次输入的新密码不一致");
    passwordMutation.mutate({
      newUsername: newUsername.trim(),
      currentPassword: oldPwd,
      newPassword: newPwd,
      confirmPassword: confirmPwd,
    });
  };

  const logout = () => {
    clearSession();
    navigate({ to: "/login" });
  };

  return (
    <div className="space-y-6">
      <PageHeader
        icon={<UserCircle2 className="h-5 w-5" />}
        title="个人资料"
        description="账户信息、配额与密码管理"
      />

      <QueryErrorNotice error={packageQuery.error} onRetry={() => void packageQuery.refetch()} />

      <Card>
        <CardContent className="p-6">
          <div className="flex items-start gap-4">
            <Avatar className="h-16 w-16 border border-border">
              <AvatarFallback className="bg-gradient-primary text-lg font-semibold text-primary-foreground">
                {initials}
              </AvatarFallback>
            </Avatar>
            <div className="flex-1">
              <div className="flex items-center gap-2">
                <div className="text-lg font-semibold">{name}</div>
                <Badge variant="outline" className={`text-xs ${role.tone}`}>
                  <ShieldCheck className="mr-1 h-3 w-3" /> {role.label}
                </Badge>
              </div>
              <div className="mt-1 font-mono text-xs text-muted-foreground">
                role_id: {roleId ?? "-"}
              </div>
            </div>
            <Button variant="outline" size="sm" onClick={logout}>
              <LogOut className="h-4 w-4" /> 退出登录
            </Button>
          </div>

          {pkg && (
            <>
              <Separator className="my-5" />
              <div className="grid gap-4 md:grid-cols-3">
                <div>
                  <div className="text-xs text-muted-foreground">流量配额</div>
                  <div className="mt-1 font-mono text-xl">
                    {totalGB} <span className="text-sm text-muted-foreground">GB</span>
                  </div>
                </div>
                <div>
                  <div className="text-xs text-muted-foreground">已用流量</div>
                  <div className="mt-1 font-mono text-xl">{fmtBytes(usedGB * 1024 ** 3)}</div>
                  <Progress
                    value={pct}
                    className={`mt-2 h-1.5 ${pct >= 90 ? "[&>div]:bg-destructive" : ""}`}
                  />
                </div>
                <div>
                  <div className="text-xs text-muted-foreground">转发数量上限</div>
                  <div className="mt-1 font-mono text-xl">{pkg.num}</div>
                </div>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <KeyRound className="h-4 w-4 text-primary" /> 修改密码
          </CardTitle>
          <CardDescription>修改后当前会话将失效，需要重新登录</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1.5">
            <Label>用户名</Label>
            <Input value={newUsername} onChange={(e) => setNewUsername(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>原密码</Label>
            <Input type="password" value={oldPwd} onChange={(e) => setOldPwd(e.target.value)} />
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <div className="space-y-1.5">
              <Label>新密码</Label>
              <Input type="password" value={newPwd} onChange={(e) => setNewPwd(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>确认新密码</Label>
              <Input
                type="password"
                value={confirmPwd}
                onChange={(e) => setConfirmPwd(e.target.value)}
              />
            </div>
          </div>
          <div className="flex justify-end">
            <Button onClick={submit} disabled={passwordMutation.isPending}>
              {passwordMutation.isPending ? "提交中…" : "更新密码"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
