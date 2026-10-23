import { PageHeader } from "@/components/page";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { readPreferences, setPreference } from "@/lib/preferences";
import { Settings2 } from "lucide-react";
import { useEffect, useState } from "react";

export function SettingsPage() {
  const [notificationsEnabled, setNotify] = useState(true);
  const [autoRefresh, setAutoRefresh] = useState(true);

  useEffect(() => {
    const prefs = readPreferences();
    setNotify(prefs.notify);
    setAutoRefresh(prefs.autoRefresh);
  }, []);

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
        description="当前浏览器的通知与刷新偏好"
      />

      <Card>
        <CardHeader>
          <CardTitle className="text-base">本地偏好</CardTitle>
          <CardDescription>仅存储在浏览器 localStorage</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center justify-between rounded-md border border-border p-3">
            <div>
              <div className="text-sm font-medium">操作通知</div>
              <div className="mt-0.5 text-xs text-muted-foreground">操作完成后弹出 Toast 提示</div>
            </div>
            <Switch
              checked={notificationsEnabled}
              onCheckedChange={(v) => togglePref("notify", v)}
            />
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
