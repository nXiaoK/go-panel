import { PageHeader, QueryErrorNotice } from "@/components/page";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Separator } from "@/components/ui/separator";
import { useSession } from "@/hooks/use-session";
import { updatePassword } from "@/lib/api";
import { roleLabel } from "@/lib/capabilities";
import { formatBytes } from "@/lib/format";
import { notify } from "@/lib/notify";
import { clearSession } from "@/lib/session";
import type { PasswordUpdate, UserPackage } from "@/lib/types";
import { Route } from "@/routes/_app.profile";
import { useNavigate } from "@tanstack/react-router";
import { KeyRound, LogOut, ShieldCheck, UserCircle2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useAccountMutation, useAccountPackage } from "./queries";

type PackageSummary = { flow: number; num: number; used: number };

function normalizePackageInfo(data: UserPackage | null): PackageSummary | null {
  if (!data) return null;
  const { flow, num, inFlow = 0, outFlow = 0 } = data.userInfo;
  return { flow, num, used: (inFlow + outFlow) / 1024 ** 3 };
}

export function ProfilePage() {
  const navigate = useNavigate();
  const search = Route.useSearch();
  const { name: sessionName, roleId, isAdmin, mustChangePassword, token } = useSession();
  const name = sessionName || "admin";
  const forcePassword = mustChangePassword || search.forcePassword === 1;
  const [newUsername, setNewUsername] = useState(name);
  const [oldPwd, setOldPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");

  useEffect(() => {
    setNewUsername(name);
  }, [name]);

  const packageQuery = useAccountPackage({ enabled: !forcePassword });
  const pkg = forcePassword ? null : normalizePackageInfo(packageQuery.data ?? null);

  const passwordMutation = useAccountMutation("account.password", {
    mutationFn: (payload: PasswordUpdate) => updatePassword(payload),
    onSuccess: () => {
      notify.success("密码已更新，请重新登录");
      setOldPwd("");
      setNewPwd("");
      setConfirmPwd("");
      clearSession("password changed");
      setTimeout(() => navigate({ to: "/login" }), 800);
    },
    onError: (error: Error) => notify.error(error.message || "修改失败"),
  });

  const role = {
    label: roleLabel(roleId),
    tone: isAdmin ? "text-primary" : "text-muted-foreground",
  };
  const initials = name.slice(0, 2).toUpperCase();
  const usedGB = pkg ? pkg.used : 0;
  const totalGB = pkg ? pkg.flow : 0;
  const pct = totalGB > 0 ? Math.min(100, Math.round((usedGB / totalGB) * 100)) : 0;

  const submit = async () => {
    if (!newUsername.trim()) return notify.error("请填写用户名");
    if (!oldPwd || !newPwd) return notify.error("请填写原密码与新密码");
    if (newPwd.length < 6) return notify.error("新密码至少 6 位");
    if (newPwd !== confirmPwd) return notify.error("两次输入的新密码不一致");
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

      {forcePassword ? (
        <div
          role="alert"
          className="rounded-md border border-warning/50 bg-warning/10 p-4 text-sm text-foreground"
        >
          为了账号安全，请先修改初始密码。修改完成前无法使用其他功能。
        </div>
      ) : (
        <QueryErrorNotice error={packageQuery.error} onRetry={() => void packageQuery.refetch()} />
      )}

      <Card>
        <CardContent className="p-6">
          <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-4 sm:flex">
            <Avatar className="h-16 w-16 border border-border">
              <AvatarFallback className="bg-primary text-lg font-semibold text-primary-foreground">
                {initials}
              </AvatarFallback>
            </Avatar>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <div className="break-all text-lg font-semibold">{name}</div>
                <Badge variant="outline" className={`text-xs ${role.tone}`}>
                  <ShieldCheck className="mr-1 h-3 w-3" /> {role.label}
                </Badge>
              </div>
              <div className="mt-1 font-mono text-xs text-muted-foreground">
                role_id: {roleId ?? "-"}
              </div>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="col-span-2 shrink-0 justify-self-end"
              onClick={logout}
            >
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
                  <div className="mt-1 font-mono text-xl">{formatBytes(usedGB * 1024 ** 3)}</div>
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
            <Label htmlFor="profile-username">用户名</Label>
            <Input
              id="profile-username"
              value={newUsername}
              onChange={(e) => setNewUsername(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="profile-current-password">原密码</Label>
            <Input
              id="profile-current-password"
              type="password"
              value={oldPwd}
              onChange={(e) => setOldPwd(e.target.value)}
            />
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="profile-new-password">新密码</Label>
              <Input
                id="profile-new-password"
                type="password"
                value={newPwd}
                onChange={(e) => setNewPwd(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="profile-confirm-password">确认新密码</Label>
              <Input
                id="profile-confirm-password"
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
