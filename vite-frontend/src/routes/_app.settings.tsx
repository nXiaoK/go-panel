import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Settings2, Save, Globe, Moon, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { getPanelAddress, setPanelAddress } from "@/lib/api/network";
import { resolveTheme, setTheme, type Theme } from "@/lib/theme";
import { readPreferences, setPreference } from "@/lib/preferences";
import { PageHeader } from "@/components/page";

export const Route = createFileRoute("/_app/settings")({
  head: () => ({ meta: [{ title: "面板设置 · Flux Panel" }] }),
  component: SettingsPage,
});

function SettingsPage() {
  const [address, setAddress] = useState("");
  const [notify, setNotify] = useState(true);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [theme, setThemeState] = useState<Theme>("dark");

  useEffect(() => {
    setAddress(getPanelAddress());
    setThemeState(resolveTheme());
    const prefs = readPreferences();
    setNotify(prefs.notify);
    setAutoRefresh(prefs.autoRefresh);
  }, []);

  const toggleTheme = (v: boolean) => {
    const next: Theme = v ? "dark" : "light";
    setTheme(next);
    setThemeState(next);
    toast.success(v ? "已切换到深色模式" : "已切换到浅色模式");
  };

  const saveAddress = () => {
    const v = address.trim();
    // 留空会恢复同源 API 默认行为；自定义地址会改变 API、WebSocket 与订阅链接目标，
    // 仅应填写可信且由当前用户管理的面板地址，保存后网络层会清理旧站点缓存。
    if (!v) {
      setPanelAddress("");
      toast.success("已重置为同源 /api/v1/");
      return;
    }
    if (!/^https?:\/\//.test(v)) return toast.error("请以 http:// 或 https:// 开头");
    setPanelAddress(v);
    toast.success("面板地址已保存");
  };

  const togglePref = (k: "notify" | "auto_refresh", v: boolean) => {
    // 写入共享偏好存储并广播；订阅方（notify 包装器、自动刷新 hook）实时生效。
    setPreference(k === "notify" ? "notify" : "autoRefresh", v);
    if (k === "notify") setNotify(v);
    else setAutoRefresh(v);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        icon={<Settings2 className="h-5 w-5" />}
        title="面板设置"
        description="连接后端与本地偏好"
      />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Globe className="h-4 w-4 text-primary" /> 后端连接
          </CardTitle>
          <CardDescription>
            指向你的 Flux Panel Go 后端。留空则使用同源 <span className="font-mono">/api/v1/</span>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1.5">
            <Label>面板地址</Label>
            <div className="flex gap-2">
              <Input
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                placeholder="https://panel.example.com"
                className="font-mono"
              />
              <Button onClick={saveAddress}>
                <Save className="h-4 w-4" /> 保存
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              需允许当前域名跨域，https 页面不能调用 http 后端。
            </p>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">本地偏好</CardTitle>
          <CardDescription>仅存储在浏览器 localStorage</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center justify-between rounded-md border border-border p-3">
            <div className="flex items-center gap-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-md border border-border bg-muted/40 text-primary">
                {theme === "dark" ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}
              </div>
              <div>
                <div className="text-sm font-medium">深色模式</div>
                <div className="mt-0.5 text-xs text-muted-foreground">
                  切换深色 / 浅色主题；也可在顶栏一键切换
                </div>
              </div>
            </div>
            <Switch checked={theme === "dark"} onCheckedChange={toggleTheme} />
          </div>
          <div className="flex items-center justify-between rounded-md border border-border p-3">
            <div>
              <div className="text-sm font-medium">操作通知</div>
              <div className="mt-0.5 text-xs text-muted-foreground">操作完成后弹出 Toast 提示</div>
            </div>
            <Switch checked={notify} onCheckedChange={(v) => togglePref("notify", v)} />
          </div>
          <div className="flex items-center justify-between rounded-md border border-border p-3">
            <div>
              <div className="text-sm font-medium">列表自动刷新</div>
              <div className="mt-0.5 text-xs text-muted-foreground">节点/转发页每 30 秒轮询</div>
            </div>
            <Switch checked={autoRefresh} onCheckedChange={(v) => togglePref("auto_refresh", v)} />
          </div>
          <Separator />
          <p className="text-xs text-muted-foreground">所有偏好仅存储在当前浏览器。</p>
        </CardContent>
      </Card>
    </div>
  );
}
