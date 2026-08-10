import { useEffect, useMemo, useState } from "react";
import { CloudUpload, KeyRound, Play, PlugZap, Save } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  getR2BackupSettings,
  runR2BackupNow,
  testR2BackupConnection,
  updateR2BackupSettings,
} from "@/lib/api";
import {
  buildR2BackupUpdate,
  defaultR2BackupForm,
  formatR2BackupBytes,
  formatR2BackupTimestamp,
  isR2BackupFormDirty,
  r2BackupSettingsToForm,
  toggleR2BackupSecretClear,
  type R2BackupForm,
} from "@/lib/r2-backup";
import type { R2BackupSettings } from "@/lib/types";

export function R2BackupCard() {
  const [settings, setSettings] = useState<R2BackupSettings | null>(null);
  const [form, setForm] = useState<R2BackupForm>(defaultR2BackupForm);
  const [secretAccessKey, setSecretAccessKey] = useState("");
  const [clearSecret, setClearSecret] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState<"save" | "test" | "run" | null>(null);

  const load = async () => {
    setLoading(true);
    setLoadError("");
    const response = await getR2BackupSettings();
    setLoading(false);
    if (response.code !== 0) {
      const message = response.msg || "加载 R2 备份设置失败";
      setLoadError(message);
      toast.error(message);
      return;
    }
    setSettings(response.data);
    setForm(r2BackupSettingsToForm(response.data));
    setSecretAccessKey("");
    setClearSecret(false);
  };

  useEffect(() => {
    void load();
  }, []);

  const dirty = useMemo(() => {
    return isR2BackupFormDirty(settings, form, secretAccessKey, clearSecret);
  }, [clearSecret, form, secretAccessKey, settings]);
  const formDisabled = loading || settings === null;

  const updateForm = <K extends keyof R2BackupForm>(name: K, value: R2BackupForm[K]) => {
    setForm((current) => ({ ...current, [name]: value }));
  };

  const toggleClearSecret = () => {
    const next = toggleR2BackupSecretClear(clearSecret, secretAccessKey);
    setClearSecret(next.clearSecret);
    setSecretAccessKey(next.secretAccessKey);
  };

  const save = async () => {
    const built = buildR2BackupUpdate(form, secretAccessKey, clearSecret);
    if (!built.value) {
      toast.error(built.error || "R2 备份设置格式无效");
      return;
    }
    setBusy("save");
    const response = await updateR2BackupSettings(built.value);
    setBusy(null);
    if (response.code !== 0) {
      toast.error(response.msg || "保存 R2 备份设置失败");
      return;
    }
    setSettings(response.data);
    setForm(r2BackupSettingsToForm(response.data));
    setSecretAccessKey("");
    setClearSecret(false);
    toast.success("R2 备份设置已保存");
  };

  const requireSavedSettings = () => {
    if (!dirty) return true;
    toast.info("请先保存 R2 设置，再执行连接测试或立即备份");
    return false;
  };

  const testConnection = async () => {
    if (!requireSavedSettings()) return;
    setBusy("test");
    const response = await testR2BackupConnection();
    setBusy(null);
    if (response.code === 0) toast.success(String(response.data || "R2 连接成功"));
    else toast.error(response.msg || "R2 连接测试失败");
  };

  const runNow = async () => {
    if (!requireSavedSettings()) return;
    setBusy("run");
    const response = await runR2BackupNow();
    setBusy(null);
    if (response.code !== 0) {
      toast.error(response.msg || "R2 备份失败");
      await load();
      return;
    }
    toast.success(`备份已上传：${response.data.objectKey}`);
    await load();
  };

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <CloudUpload className="h-4 w-4 text-primary" />
          <CardTitle className="text-base">Cloudflare R2 自动备份</CardTitle>
        </div>
        <CardDescription>
          每天按服务器本地时间上传经过完整性校验的 SQLite 快照，并自动删除超出保留数量的面板备份。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-xs leading-relaxed text-amber-700 dark:text-amber-300">
          备份包含用户、节点密钥和全部面板数据。请保持 R2
          存储桶私有，并使用仅限目标存储桶“对象读取和写入”的 API
          令牌；保留清理需要删除对象权限。Secret Access Key 会使用持久 JWT_SECRET 进行 AES-GCM
          加密且不会回传，变更 JWT_SECRET 后需要重新填写。
        </div>

        {loadError && (
          <div
            role="alert"
            className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"
          >
            <span>R2 备份设置加载失败：{loadError}</span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={loading}
              onClick={() => void load()}
            >
              重新加载
            </Button>
          </div>
        )}

        {!loading && settings && !settings.credentialEncryptionAvailable && (
          <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive">
            当前没有持久 JWT_SECRET，不能安全保存 R2 Secret Access Key。请在部署环境配置至少 32
            字节的 JWT_SECRET 并重启面板。
          </div>
        )}

        <div className="flex items-center justify-between rounded-md border p-3">
          <div>
            <Label htmlFor="r2-enabled">启用每日自动备份</Label>
            <p className="mt-1 text-xs text-muted-foreground">
              默认关闭；开启后到达计划时间会补跑，失败每 15 分钟重试一次。
            </p>
          </div>
          <Switch
            id="r2-enabled"
            checked={form.enabled}
            disabled={formDisabled}
            onCheckedChange={(checked) => updateForm("enabled", checked)}
          />
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <R2Field
            id="r2-account-id"
            label="Cloudflare Account ID"
            description="Cloudflare 控制台中的 32 位账户 ID，用于生成官方 R2 S3 端点。"
            value={form.accountId}
            placeholder="0123456789abcdef0123456789abcdef"
            disabled={formDisabled}
            onChange={(value) => updateForm("accountId", value)}
          />
          <R2Field
            id="r2-bucket"
            label="R2 存储桶"
            description="必须是私有存储桶；名称只允许小写字母、数字和连字符。"
            value={form.bucket}
            placeholder="flux-panel-backups"
            disabled={formDisabled}
            onChange={(value) => updateForm("bucket", value)}
          />
          <R2Field
            id="r2-access-key-id"
            label="Access Key ID"
            description="使用目标存储桶专用的 R2 API 令牌，不要使用账户级高权限令牌。"
            value={form.accessKeyId}
            placeholder="R2 Access Key ID"
            disabled={formDisabled}
            onChange={(value) => updateForm("accessKeyId", value)}
          />
          <div className="rounded-md border p-3">
            <Label htmlFor="r2-secret-access-key">Secret Access Key</Label>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
              {settings?.secretConfigured
                ? "已保存加密密钥；留空将保留，输入新值会安全轮换。"
                : "只在保存时提交，后端加密后不会再返回明文。"}
            </p>
            <div className="mt-2 flex gap-2">
              <Input
                id="r2-secret-access-key"
                type="password"
                autoComplete="new-password"
                value={secretAccessKey}
                placeholder={
                  settings?.secretConfigured ? "已配置，留空保留" : "R2 Secret Access Key"
                }
                disabled={formDisabled || clearSecret}
                onChange={(event) => setSecretAccessKey(event.target.value)}
              />
              {settings?.secretConfigured && (
                <Button
                  type="button"
                  variant={clearSecret ? "destructive" : "outline"}
                  onClick={toggleClearSecret}
                  disabled={formDisabled}
                >
                  <KeyRound className="h-4 w-4" /> {clearSecret ? "将清除" : "清除"}
                </Button>
              )}
            </div>
            {settings?.credentialMessage && !settings.secretUsable && (
              <p className="mt-2 text-xs text-destructive">{settings.credentialMessage}</p>
            )}
          </div>
          <R2Field
            id="r2-object-prefix"
            label="对象前缀"
            description="用于隔离不同面板；保留策略只处理此前缀下严格匹配 flux-panel-backup-YYYYMMDD-HHMMSS.db 命名规则的对象。多个面板必须使用不同前缀，避免相互清理备份。"
            value={form.objectPrefix}
            placeholder="flux-panel/backups"
            disabled={formDisabled}
            onChange={(value) => updateForm("objectPrefix", value)}
          />
          <div className="grid gap-4 sm:grid-cols-2">
            <R2Field
              id="r2-schedule-time"
              label="每日备份时间"
              description="使用面板服务器本地时区。"
              type="time"
              value={form.scheduleTime}
              disabled={formDisabled}
              onChange={(value) => updateForm("scheduleTime", value)}
            />
            <R2Field
              id="r2-retention-count"
              label="保留数量"
              description="范围 1-365，默认保留最近 30 份。"
              type="number"
              value={form.retentionCount}
              min="1"
              max="365"
              disabled={formDisabled}
              onChange={(value) => updateForm("retentionCount", value)}
            />
          </div>
        </div>

        <div className="flex flex-wrap gap-2">
          <Button type="button" onClick={save} disabled={loading || busy !== null || !dirty}>
            <Save className="h-4 w-4" /> {busy === "save" ? "保存中…" : "保存 R2 设置"}
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={testConnection}
            disabled={loading || busy !== null || !settings?.secretUsable}
          >
            <PlugZap className="h-4 w-4" /> {busy === "test" ? "测试中…" : "测试连接"}
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={runNow}
            disabled={loading || busy !== null || !settings?.secretUsable}
          >
            <Play className="h-4 w-4" /> {busy === "run" ? "上传中…" : "立即备份到 R2"}
          </Button>
        </div>

        {settings && (
          <div className="grid gap-3 rounded-md border p-3 text-xs sm:grid-cols-2">
            <div>
              <span className="text-muted-foreground">最近尝试：</span>
              {formatR2BackupTimestamp(settings.lastAttemptAt)}
            </div>
            <div>
              <span className="text-muted-foreground">最近成功：</span>
              {formatR2BackupTimestamp(settings.lastSuccessAt)}
            </div>
            <div>
              <span className="text-muted-foreground">对象大小：</span>
              {formatR2BackupBytes(settings.lastSize)}
            </div>
            <div className="min-w-0">
              <span className="text-muted-foreground">对象键：</span>
              <span className="break-all font-mono">{settings.lastObjectKey || "尚无记录"}</span>
            </div>
            {settings.lastSha256 && (
              <div className="min-w-0 sm:col-span-2">
                <span className="text-muted-foreground">SHA-256：</span>
                <span className="break-all font-mono">{settings.lastSha256}</span>
              </div>
            )}
            {settings.lastError && (
              <div className="rounded border border-destructive/30 bg-destructive/5 p-2 text-destructive sm:col-span-2">
                最近错误：{settings.lastError}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function R2Field({
  id,
  label,
  description,
  value,
  onChange,
  type = "text",
  placeholder,
  disabled,
  min,
  max,
}: {
  id: string;
  label: string;
  description: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  placeholder?: string;
  disabled?: boolean;
  min?: string;
  max?: string;
}) {
  return (
    <div className="rounded-md border p-3">
      <Label htmlFor={id}>{label}</Label>
      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{description}</p>
      <Input
        id={id}
        className="mt-2"
        type={type}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        min={min}
        max={max}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}
