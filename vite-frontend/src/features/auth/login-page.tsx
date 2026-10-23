import { Button } from "@/components/ui/button";
import { FormField } from "@/components/patterns/form-field";
import { Input } from "@/components/ui/input";
import { login, getConfigByName } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { setSession } from "@/lib/session";
import { getCachedAppName, setCachedAppName } from "@/lib/site-config";
import { Route } from "@/routes/login";
import { useNavigate } from "@tanstack/react-router";
import { AlertCircle, Eye, EyeOff, Zap } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { loginReasons } from "./login-search";

export function LoginPage() {
  const navigate = useNavigate();
  const reason = Route.useSearch().reason as keyof typeof loginReasons | undefined;
  const [appName, setAppName] = useState(getCachedAppName);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef(false);
  const passwordRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const controller = new AbortController();
    getConfigByName("app_name", { signal: controller.signal })
      .then((config) => {
        if (controller.signal.aborted) return;
        setCachedAppName(config.value);
        setAppName(getCachedAppName());
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, []);
  const handleLogin = async (event: React.FormEvent) => {
    event.preventDefault();
    if (pending.current) return;
    setError("");
    if (!username.trim() || !password) {
      setError("请输入用户名和密码");
      passwordRef.current?.focus();
      return;
    }
    pending.current = true;
    setLoading(true);
    try {
      const response = await login({ username: username.trim(), password });
      if (!response.token) throw new Error("登录响应缺少令牌");
      setSession({
        token: response.token,
        roleId: response.role_id,
        name: response.name,
        mustChangePassword: Boolean(response.requirePasswordChange),
      });
      if (response.requirePasswordChange)
        await navigate({ to: "/account", search: { tab: "security", forcePassword: 1 } });
      else await navigate({ to: "/" });
    } catch (failure) {
      setError(errorMessage(failure));
      passwordRef.current?.focus();
    } finally {
      pending.current = false;
      setLoading(false);
    }
  };
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-6 bg-canvas p-4 text-ink">
      <div className="flex max-w-full items-center gap-2 text-section-title">
        <Zap className="size-6 shrink-0 text-primary" aria-hidden="true" />
        <span className="truncate">{appName}</span>
      </div>
      <section
        className="w-full max-w-[400px] rounded-md border border-hairline bg-canvas p-8"
        aria-labelledby="login-title"
      >
        <h1 id="login-title" className="mb-6 text-page-title">
          登录控制台
        </h1>
        <form className="space-y-4" onSubmit={handleLogin}>
          <FormField label="用户名" htmlFor="login-username">
            <Input
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </FormField>
          <FormField label="密码" htmlFor="login-password">
            {({ id }) => (
              <div className="relative">
                <Input
                  ref={passwordRef}
                  id={id}
                  type={visible ? "text" : "password"}
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  className="pr-11"
                  aria-invalid={Boolean(error)}
                  aria-describedby={error ? "login-error" : undefined}
                />
                <button
                  type="button"
                  className="absolute right-0 top-0 flex h-9 w-10 items-center justify-center rounded-sm text-mute max-md:after:absolute max-md:after:-inset-y-1"
                  onClick={() => setVisible(!visible)}
                  aria-label={visible ? "隐藏密码" : "显示密码"}
                  aria-pressed={visible}
                >
                  {visible ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </div>
            )}
          </FormField>
          {error && (
            <p
              id="login-error"
              role="alert"
              className="flex items-start gap-2 text-sm leading-5 text-status-critical-text"
            >
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              {error}
            </p>
          )}
          <Button type="submit" size="lg" className="w-full" loading={loading}>
            登录
          </Button>
        </form>
      </section>
      {reason && (
        <p role="status" className="max-w-[400px] text-center text-sm leading-5 text-mute">
          {loginReasons[reason]}
        </p>
      )}
    </div>
  );
}
