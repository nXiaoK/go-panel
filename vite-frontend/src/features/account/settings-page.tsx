import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { usePreferences } from "@/hooks/use-preferences";
import { setPreference } from "@/lib/preferences";

export function SettingsPage() {
  const preferences = usePreferences();
  return (
    <Card>
      <CardHeader>
        <CardTitle>偏好</CardTitle>
        <CardDescription>仅保存在当前浏览器，修改后立即生效</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <label htmlFor="account-notify" className="text-body-strong text-ink">
              操作成功提示
            </label>
            <p id="account-notify-description" className="mt-1 text-caption text-mute">
              关闭后只保留警告和错误提示
            </p>
          </div>
          <Switch
            id="account-notify"
            checked={preferences.notify}
            onCheckedChange={(value) => setPreference("notify", value)}
            aria-describedby="account-notify-description"
          />
        </div>
        <div className="flex items-start justify-between gap-4 border-t border-hairline pt-5">
          <div>
            <label htmlFor="account-auto-refresh" className="text-body-strong text-ink">
              列表自动刷新
            </label>
            <p id="account-auto-refresh-description" className="mt-1 text-caption text-mute">
              节点、转发列表每 30 秒刷新一次
            </p>
          </div>
          <Switch
            id="account-auto-refresh"
            checked={preferences.autoRefresh}
            onCheckedChange={(value) => setPreference("autoRefresh", value)}
            aria-describedby="account-auto-refresh-description"
          />
        </div>
      </CardContent>
    </Card>
  );
}
