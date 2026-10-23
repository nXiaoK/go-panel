import { errorMessage } from "@/lib/api/client";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Loader2, LogIn, Settings2, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { login } from "@/lib/api";
import { getPanelAddress, setPanelAddress } from "@/lib/api/client";
import { markSessionActive, setPasswordChangeRequired } from "@/lib/session";

export const Route = createFileRoute("/login")({
  // 仅接受会话失效和账号禁用两个提示原因；未提供或未知值时沿用普通登录界面。
  validateSearch: (search: Record<string, unknown>): { reason?: "expired" | "disabled" } =>
    search.reason === "expired" || search.reason === "disabled" ? { reason: search.reason } : {},
  head: () => ({ meta: [{ title: "登录 · Flux Panel" }] }),
  component: LoginPage,
});

function LoginPage() {
  const navigate = useNavigate();
  const { reason } = Route.useSearch();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [panelOpen, setPanelOpen] = useState(false);
  const [panelInput, setPanelInput] = useState("");

  useEffect(() => {
    setPanelInput(getPanelAddress());
    if (typeof window !== "undefined" && window.localStorage.getItem("token")) {
      navigate({ to: "/" });
    }
  }, [navigate]);

  const savePanel = () => {
    const v = panelInput.trim();
    if (v && !/^https?:\/\//i.test(v)) {
      toast.error("请以 http:// 或 https:// 开头");
      return;
    }
    setPanelAddress(v);
    toast.success("面板地址已保存");
    setPanelOpen(false);
  };

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!username || !password) {
      toast.error("请输入用户名和密码");
      return;
    }
    setLoading(true);
    try {
      const res = await login({ username, password });
      if (res.token) {
        window.localStorage.setItem("token", res.token);
        window.localStorage.setItem("role_id", String(res.role_id));
        window.localStorage.setItem("name", res.name);
        setPasswordChangeRequired(Boolean(res.requirePasswordChange));
        markSessionActive();
        toast.success("登录成功");
        if (res.requirePasswordChange) {
          navigate({ to: "/profile", search: { forcePassword: 1 } });
        } else {
          navigate({ to: "/" });
        }
      } else {
        toast.error("登录响应缺少令牌");
      }
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="relative flex min-h-screen items-center justify-center bg-background bg-grid p-6">
      <div className="pointer-events-none absolute inset-0 bg-gradient-to-b from-primary/10 via-transparent to-transparent" />
      <Card className="relative w-full max-w-md border-border/60 bg-card/60 shadow-glow backdrop-blur">
        <CardHeader className="space-y-1">
          <div className="flex items-center gap-2 text-primary">
            <ShieldCheck className="h-5 w-5" />
            <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
              Flux Panel
            </span>
          </div>
          <CardTitle className="text-2xl">登录控制台</CardTitle>
        </CardHeader>
        <CardContent>
          {reason && (
            <p
              role="status"
              className="mb-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
            >
              {reason === "disabled" ? "账号已被禁用，请联系管理员" : "登录已失效，请重新登录"}
            </p>
          )}
          <form className="space-y-4" onSubmit={handleLogin}>
            <div className="space-y-2">
              <Label htmlFor="user">用户名</Label>
              <Input
                id="user"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="admin"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="pwd">密码</Label>
              <Input
                id="pwd"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
              />
            </div>
            <Button type="submit" className="w-full gap-2 shadow-glow" disabled={loading}>
              {loading ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <LogIn className="h-4 w-4" />
              )}
              登录
            </Button>
          </form>

          <div className="mt-6 border-t border-border/60 pt-4">
            {!panelOpen ? (
              <button
                type="button"
                onClick={() => setPanelOpen(true)}
                className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
              >
                <Settings2 className="h-3.5 w-3.5" />
                面板地址：{getPanelAddress() || "同源 /api/v1/"}
              </button>
            ) : (
              <div className="space-y-2">
                <Label htmlFor="panel" className="text-xs">
                  面板后端地址
                </Label>
                <div className="flex gap-2">
                  <Input
                    id="panel"
                    placeholder="https://panel.example.com"
                    value={panelInput}
                    onChange={(e) => setPanelInput(e.target.value)}
                  />
                  <Button size="sm" variant="outline" onClick={savePanel}>
                    保存
                  </Button>
                </div>
                <p className="text-[11px] text-muted-foreground">
                  留空则使用与前端同源的 <code className="font-mono">/api/v1/</code>
                </p>
              </div>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
