import { ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { FormField } from "@/components/patterns/form-field";
import { Meter } from "@/components/patterns/meter";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useSession } from "@/hooks/use-session";
import { updatePassword } from "@/lib/api";
import { roleLabel } from "@/lib/capabilities";
import { formatBytes, formatDate, formatDateTime } from "@/lib/format";
import { clearSession } from "@/lib/session";
import type { PasswordUpdate } from "@/lib/types";
import { Route } from "@/routes/_app.account";
import { useNavigate } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, Eye, EyeOff, LogOut } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useAccountPackage } from "./queries";

type Field = keyof PasswordUpdate;
type Errors = Partial<Record<Field, string>>;
const utf8Bytes = (value: string) => new TextEncoder().encode(value).length;
function strength(value: string) {
  if (!value) return "尚未输入";
  const groups = [/[a-z]/, /[A-Z]/, /\d/, /[^a-zA-Z\d]/].filter((pattern) =>
    pattern.test(value),
  ).length;
  return value.length >= 12 && groups >= 3 ? "强" : value.length >= 8 && groups >= 2 ? "中" : "弱";
}
function PasswordField({
  label,
  id,
  value,
  onChange,
  error,
  hint,
  autoComplete,
  disabled,
}: {
  label: string;
  id: string;
  value: string;
  onChange: (value: string) => void;
  error?: string;
  hint?: string;
  autoComplete: string;
  disabled: boolean;
}) {
  const [visible, setVisible] = useState(false);
  return (
    <FormField label={label} htmlFor={id} required error={error} hint={hint}>
      {(control) => (
        <div className="relative">
          <Input
            {...control}
            aria-label={label}
            required
            type={visible ? "text" : "password"}
            value={value}
            onChange={(event) => onChange(event.target.value)}
            autoComplete={autoComplete}
            disabled={disabled}
            className="pr-12"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="absolute right-0 top-0"
            aria-label={`${visible ? "隐藏" : "显示"}${label}`}
            aria-pressed={visible}
            onClick={() => setVisible(!visible)}
            disabled={disabled}
          >
            {visible ? <EyeOff /> : <Eye />}
          </Button>
        </div>
      )}
    </FormField>
  );
}

export function ProfilePage() {
  const navigate = useNavigate();
  const search = Route.useSearch();
  const { name: sessionName, roleId, isAdmin, mustChangePassword, token } = useSession();
  const name = sessionName || "用户";
  const forced = mustChangePassword || search.forcePassword === 1;
  const [form, setForm] = useState<PasswordUpdate>({
    newUsername: name,
    currentPassword: "",
    newPassword: "",
    confirmPassword: "",
  });
  const [errors, setErrors] = useState<Errors>({});
  const [submitError, setSubmitError] = useState<unknown>();
  const submitting = useRef(false);
  useEffect(() => {
    setForm((previous) => ({ ...previous, newUsername: name }));
  }, [name]);
  const packageQuery = useAccountPackage({ enabled: !forced && !isAdmin && Boolean(token) });
  const account = packageQuery.data?.userInfo;
  // flow 是后端 GB 配额；0 表示已用尽，不能套用 Meter 的“非正上限=不限”默认行为。
  const usedBytes = (account?.inFlow ?? 0) + (account?.outFlow ?? 0);
  // 成功后立即退出，不在旧令牌失效时先触发套餐失效重取。
  const passwordMutation = useMutation({
    mutationFn: (payload: PasswordUpdate) => updatePassword(payload),
    onSuccess: () => {
      setForm((previous) => ({
        ...previous,
        currentPassword: "",
        newPassword: "",
        confirmPassword: "",
      }));
      clearSession("password changed");
      void navigate({ to: "/login", search: { reason: "password-changed" }, replace: true });
    },
    onError: (error: Error) => {
      if (error.message.includes("当前密码错误")) setErrors({ currentPassword: error.message });
      else if (error.message.includes("用户名已被占用")) setErrors({ newUsername: error.message });
      else if (error.message.includes("两次输入的密码不一致"))
        setErrors({ confirmPassword: error.message });
      else setSubmitError(error);
    },
    onSettled: () => {
      submitting.current = false;
    },
  });
  function change(field: Field, value: string) {
    setForm((previous) => ({ ...previous, [field]: value }));
    setErrors((previous) => ({ ...previous, [field]: undefined }));
    setSubmitError(undefined);
  }
  function submit() {
    if (submitting.current) return;
    const next: Errors = {};
    if (!form.newUsername.trim()) next.newUsername = "请填写用户名";
    if (!form.currentPassword) next.currentPassword = "请填写当前密码";
    if (!form.newPassword) next.newPassword = "请填写新密码";
    // 后端不限制最小长度或复杂度；bcrypt 只接受最多 72 个 UTF-8 字节，强度提示不拦截提交。
    else if (utf8Bytes(form.newPassword) > 72) next.newPassword = "新密码不能超过 72 个 UTF-8 字节";
    if (!form.confirmPassword) next.confirmPassword = "请再次输入新密码";
    else if (form.newPassword !== form.confirmPassword)
      next.confirmPassword = "两次输入的新密码不一致";
    setErrors(next);
    setSubmitError(undefined);
    if (Object.keys(next).length) return;
    submitting.current = true;
    passwordMutation.mutate({ ...form, newUsername: form.newUsername.trim() });
  }
  function logout() {
    clearSession();
    void navigate({ to: "/login" });
  }
  return (
    <div className="min-w-0 space-y-6">
      {forced && (
        <div
          role="alert"
          className="flex items-start gap-3 rounded-md border border-status-warning/30 bg-status-warning-bg p-4 text-sm text-ink"
        >
          <AlertTriangle
            aria-hidden="true"
            className="mt-0.5 size-4 shrink-0 text-status-warning"
          />
          <span>为了账号安全，请先修改初始密码</span>
        </div>
      )}
      {!forced && (
        <Card>
          <CardHeader>
            <CardTitle>账号</CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            <div className="flex min-w-0 items-center gap-3">
              <Avatar className="size-12 shrink-0 border border-hairline">
                <AvatarFallback className="bg-canvas-soft text-body-strong text-ink">
                  {name.slice(0, 2).toUpperCase()}
                </AvatarFallback>
              </Avatar>
              <div className="min-w-0">
                <p className="break-all text-body-strong text-ink">{name}</p>
                <p className="text-caption text-mute">{roleLabel(roleId)}</p>
              </div>
            </div>
            {!isAdmin && (
              <section aria-label="账户配额" className="space-y-4 border-t border-hairline pt-5">
                <h3 className="text-caption-strong text-body">流量配额</h3>
                {packageQuery.isPending ? (
                  <LoadingRows rows={2} />
                ) : packageQuery.error ? (
                  <ErrorState
                    error={packageQuery.error}
                    onRetry={() => void packageQuery.refetch()}
                  />
                ) : account ? (
                  <>
                    {account.flow > 0 ? (
                      <Meter
                        value={usedBytes}
                        max={account.flow * 1024 ** 3}
                        label="账户流量配额"
                        formatValue={formatBytes}
                      />
                    ) : (
                      <p className="text-sm text-status-critical-text">
                        {formatBytes(usedBytes)} / 0 GB · 流量已用尽
                      </p>
                    )}
                    <dl className="grid gap-3 text-sm sm:grid-cols-2">
                      <div className="flex flex-wrap justify-between gap-2">
                        <dt className="text-mute">转发上限</dt>
                        <dd className="font-mono tabular text-ink">{account.num}</dd>
                      </div>
                      <div className="flex flex-wrap justify-between gap-2">
                        <dt className="text-mute">到期时间</dt>
                        <dd
                          className="font-mono tabular text-ink"
                          title={formatDateTime(account.expTime)}
                        >
                          {account.expTime != null ? formatDate(account.expTime) : "未设置"}
                        </dd>
                      </div>
                    </dl>
                  </>
                ) : (
                  <p className="text-sm text-mute">暂无配额数据</p>
                )}
              </section>
            )}
          </CardContent>
        </Card>
      )}
      <Card>
        <CardHeader>
          <CardTitle>修改登录信息</CardTitle>
          <CardDescription>修改后当前会话将失效，需要重新登录</CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="space-y-4"
            noValidate
            onSubmit={(event) => {
              event.preventDefault();
              submit();
            }}
          >
            {submitError ? <ErrorState error={submitError} /> : null}
            <FormField
              label="用户名"
              htmlFor="profile-username"
              required
              error={errors.newUsername}
            >
              <Input
                aria-label="用户名"
                required
                value={form.newUsername}
                autoComplete="username"
                disabled={passwordMutation.isPending}
                onChange={(event) => change("newUsername", event.target.value)}
              />
            </FormField>
            <PasswordField
              label="原密码"
              id="profile-current-password"
              value={form.currentPassword}
              onChange={(value) => change("currentPassword", value)}
              error={errors.currentPassword}
              autoComplete="current-password"
              disabled={passwordMutation.isPending}
            />
            <PasswordField
              label="新密码"
              id="profile-new-password"
              value={form.newPassword}
              onChange={(value) => change("newPassword", value)}
              error={errors.newPassword}
              hint={`强度：${strength(form.newPassword)}（仅供参考）；最多 72 个 UTF-8 字节`}
              autoComplete="new-password"
              disabled={passwordMutation.isPending}
            />
            <PasswordField
              label="确认新密码"
              id="profile-confirm-password"
              value={form.confirmPassword}
              onChange={(value) => change("confirmPassword", value)}
              error={errors.confirmPassword}
              autoComplete="new-password"
              disabled={passwordMutation.isPending}
            />
            <div className="flex justify-end pt-2">
              <Button type="submit" loading={passwordMutation.isPending}>
                更新登录信息
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
      {!forced && (
        <Button type="button" variant="outline" onClick={logout}>
          <LogOut />
          退出登录
        </Button>
      )}
    </div>
  );
}
