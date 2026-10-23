import { PageHeader } from "@/components/patterns/page-header";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { FormField } from "@/components/patterns/form-field";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { useConfirm } from "@/hooks/use-confirm";
import { deleteConfig, downloadSiteBackup, restoreSiteBackup, updateConfigs } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { formatBytes, formatDateTime } from "@/lib/format";
import { notify } from "@/lib/notify";
import { clearSession } from "@/lib/session";
import { setCachedAppName } from "@/lib/site-config";
import { useQueryClient } from "@tanstack/react-query";
import { useBlocker, useNavigate, useSearch } from "@tanstack/react-router";
import { AlertTriangle, Download, Plus, RefreshCcw, Save, Trash2, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  allowInsecureNodeDownloadsEnvOverrideName,
  configItemsToMap,
  customConfigNameError,
  isProtectedConfigName,
  keyConfigMeta,
  keyConfigNames,
  normalizeConfigItems,
  readOnlyConfigNames,
  resolveInsecureDownloadNoticeState,
  restoreFileError,
  type ConfigTab,
  type KeyConfigMeta,
} from "./model";
import { configKeys, useConfigMutation, useConfigs } from "./queries";
import { R2BackupCard } from "./components/r2-backup-card";

type RestoreSummary = Awaited<ReturnType<typeof restoreSiteBackup>>;
const tabLabels: { value: ConfigTab; label: string }[] = [
  { value: "site", label: "站点" },
  { value: "security", label: "节点与安全" },
  { value: "backup", label: "备份与恢复" },
  { value: "r2", label: "云端备份 R2" },
  { value: "advanced", label: "高级" },
];

export function ConfigPage() {
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const tab = useSearch({ strict: false, select: (search) => search.tab }) as ConfigTab | undefined;
  const configsQuery = useConfigs();
  const [verticalTabs, setVerticalTabs] = useState(false);
  useEffect(() => {
    const media = window.matchMedia("(min-width: 1024px)");
    const updateOrientation = () => setVerticalTabs(media.matches);
    updateOrientation();
    media.addEventListener("change", updateOrientation);
    return () => media.removeEventListener("change", updateOrientation);
  }, []);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [addOpen, setAddOpen] = useState(false);
  const [busy, setBusy] = useState<"backup" | "restore" | "restored" | null>(null);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState<string>();
  const [restoreError, setRestoreError] = useState("");
  const [summary, setSummary] = useState<RestoreSummary | null>(null);
  const [addressError, setAddressError] = useState<string>();
  const fileRef = useRef<HTMLInputElement>(null);
  const restoredRef = useRef(false);
  const signOutTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (signOutTimer.current) clearTimeout(signOutTimer.current);
    },
    [],
  );

  // 草稿仅保存与当前服务端值不同的键；跨标签共用，不因标签切换或后台重取而丢失。
  const persistedValues = useMemo(
    () => configItemsToMap(configsQuery.data ?? []),
    [configsQuery.data],
  );
  const dirty = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(edits).filter(
          ([name, value]) => !(name in persistedValues) || value !== persistedValues[name],
        ),
      ),
    [edits, persistedValues],
  );
  const dirtyCount = Object.keys(dirty).length;
  const values = { ...persistedValues, ...dirty };
  const advancedItems = Object.entries(values).filter(
    ([name]) => !keyConfigNames.includes(name) && !readOnlyConfigNames.includes(name),
  );

  const saveMutation = useConfigMutation("config.update", {
    mutationFn: (payload: Record<string, string>) => updateConfigs(payload),
    onSuccess: (_data, payload) => {
      queryClient.setQueryData(
        configKeys.list(),
        normalizeConfigItems({ ...persistedValues, ...payload }),
      );
      if (payload.app_name !== undefined) setCachedAppName(payload.app_name);
      setEdits({});
      notify.success("系统配置已保存");
    },
    onError: (error) => notify.error(error.message || "保存失败"),
  });
  const deleteMutation = useConfigMutation("config.update", {
    mutationFn: (name: string) => deleteConfig(name),
    onSuccess: (_data, name) => {
      queryClient.setQueryData(configKeys.list(), (current: typeof configsQuery.data) =>
        current?.filter((item) => item.name !== name),
      );
      setEdits((current) =>
        Object.fromEntries(Object.entries(current).filter(([key]) => key !== name)),
      );
      notify.success("配置项已删除");
    },
    onError: (error) => notify.error(error.message || "删除失败"),
  });
  const restoring = busy === "restore" || busy === "restored";
  const controlsDisabled =
    configsQuery.isPending ||
    Boolean(configsQuery.error) ||
    saveMutation.isPending ||
    deleteMutation.isPending ||
    restoring;
  useBlocker({
    disabled: dirtyCount === 0 && !restoring,
    enableBeforeUnload: dirtyCount > 0 || restoring,
    shouldBlockFn: async ({ current, next }) => {
      if (restoredRef.current) return false;
      if (restoring) {
        notify.info("恢复过程中请保持页面打开，完成后会自动退出登录");
        return true;
      }
      if (current.pathname === next.pathname || dirtyCount === 0) return false;
      return !(await confirm({
        title: "放弃未保存的修改？",
        description: `还有 ${dirtyCount} 项系统配置尚未保存，离开将丢失这些修改`,
        confirmText: "放弃修改并离开",
        tone: "danger",
      }));
    },
  });

  const setValue = (name: string, value: string) => {
    setEdits((current) => ({ ...current, [name]: value }));
    if (name === "ip") setAddressError(undefined);
  };
  const suggestedPanelAddress =
    typeof window === "undefined"
      ? ""
      : `${window.location.hostname}:${window.location.port || (window.location.protocol === "https:" ? "443" : "80")}`;
  const save = () => {
    const payload = { ...dirty };
    // 地址与可信 HTTPS 下载代理去除首尾空白和末尾斜杠，最终安全校验仍由后端负责。
    if (payload.ip !== undefined) payload.ip = payload.ip.trim().replace(/\/+$/, "");
    if (payload.github_download_proxy !== undefined)
      payload.github_download_proxy = payload.github_download_proxy.trim().replace(/\/+$/, "");
    if (payload.ip !== undefined && !isPanelAddressLike(payload.ip)) {
      setAddressError("面板公网地址格式不正确，请填写 host:port 或 http(s)://host:port");
      void navigate({ to: "/config", search: { tab: "site" } });
      requestAnimationFrame(() => document.getElementById("site-config-ip")?.focus());
      return;
    }
    if (dirtyCount) saveMutation.mutate(payload);
  };
  const remove = async (name: string) => {
    if (isProtectedConfigName(name) || controlsDisabled) return;
    if (
      await confirm({
        title: "删除配置项",
        description: `删除「${name}」会立即从服务器移除此配置，使用它的功能将回到默认行为，请确认影响后继续`,
        confirmText: "删除配置项",
        tone: "danger",
        requireText: name,
      })
    )
      deleteMutation.mutate(name);
  };
  const backup = async () => {
    setBusy("backup");
    try {
      const { blob, filename } = await downloadSiteBackup();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = filename || "backup.db";
      anchor.click();
      URL.revokeObjectURL(url);
      notify.success("备份已下载");
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      setBusy(null);
    }
  };
  const restore = async () => {
    if (!selectedFile || busy !== null) return;
    const invalid = restoreFileError(selectedFile);
    if (invalid) {
      setFileError(invalid);
      return;
    }
    if (
      !(await confirm({
        title: "恢复站点备份",
        description: `使用「${selectedFile.name}」恢复将覆盖当前数据库和节点配置，无法撤销。建议先下载一份当前备份；确认后请保持页面打开，恢复过程不可取消`,
        confirmText: "恢复备份",
        tone: "danger",
        requireText: "恢复",
      }))
    )
      return;
    setBusy("restore");
    setRestoreError("");
    try {
      const result = await restoreSiteBackup(selectedFile);
      setSummary(result);
      setBusy("restored");
      setEdits({});
      // 恢复可能改变账户和令牌版本；展示真实摘要后等待 3 秒，清理旧身份并重新登录。
      signOutTimer.current = setTimeout(() => {
        restoredRef.current = true;
        clearSession("restored");
        void navigate({ to: "/login", search: { reason: "restored" }, ignoreBlocker: true });
      }, 3000);
    } catch (error) {
      setRestoreError(errorMessage(error));
      setBusy(null);
    }
  };
  const insecureNotice = insecureDownloadNotice(
    persistedValues,
    values,
    dirty.allow_insecure_node_downloads !== undefined,
  );

  return (
    <div className={dirtyCount ? "space-y-6 pb-24" : "space-y-6"}>
      <PageHeader
        title="系统配置"
        description="管理站点、节点安全策略和备份"
        actions={
          <Button
            variant="outline"
            onClick={() => void configsQuery.refetch()}
            disabled={controlsDisabled || dirtyCount > 0}
            loading={configsQuery.isFetching}
          >
            <RefreshCcw aria-hidden="true" />
            刷新配置
          </Button>
        }
      />
      {configsQuery.error && (
        <ErrorState error={configsQuery.error} onRetry={() => void configsQuery.refetch()} />
      )}
      <Tabs
        orientation={verticalTabs ? "vertical" : "horizontal"}
        value={tab ?? "site"}
        onValueChange={(value) =>
          void navigate({ to: "/config", search: { tab: value as ConfigTab } })
        }
        className="min-w-0 lg:grid lg:grid-cols-[160px_minmax(0,1fr)] lg:items-start lg:gap-6"
      >
        <TabsList
          aria-label="系统配置分区"
          className="flex w-full justify-start gap-4 overflow-x-auto lg:flex-col lg:items-stretch lg:gap-1 lg:border-b-0 lg:border-r lg:pr-4"
        >
          {tabLabels.map((item) => (
            <TabsTrigger
              key={item.value}
              value={item.value}
              disabled={restoring}
              className="shrink-0 lg:justify-start lg:border-b-0 lg:border-l-2 lg:px-3 lg:data-[state=active]:border-l-primary"
            >
              {item.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <div className="min-w-0 pt-4 lg:pt-0">
          <TabsContent value="site" className="mt-0 space-y-6">
            <ConfigSection
              title="站点"
              description="节点安装前请先设置节点服务器可以访问的面板公网地址"
            >
              {configsQuery.isPending ? (
                <LoadingRows rows={2} />
              ) : (
                <>
                  {!values.ip && (
                    <Notice tone="warning">
                      尚未设置面板公网地址，节点安装命令暂时无法生成
                      {suggestedPanelAddress && (
                        <Button
                          variant="outline"
                          size="sm"
                          className="mt-2 block"
                          disabled={controlsDisabled}
                          onClick={() => setValue("ip", suggestedPanelAddress)}
                        >
                          使用当前地址
                        </Button>
                      )}
                    </Notice>
                  )}
                  {keyConfigMeta
                    .filter((meta) => ["ip", "app_name"].includes(meta.name))
                    .map((meta) => (
                      <SiteConfigControl
                        key={meta.name}
                        meta={meta}
                        value={values[meta.name] ?? ""}
                        onChange={(value) => setValue(meta.name, value)}
                        disabled={controlsDisabled}
                        error={meta.name === "ip" ? addressError : undefined}
                      />
                    ))}
                </>
              )}
            </ConfigSection>
          </TabsContent>
          <TabsContent value="security" className="mt-0">
            <ConfigSection
              title="节点与安全"
              description="配置节点下载策略、可信代理与参考诊断目标"
            >
              {configsQuery.isPending ? (
                <LoadingRows rows={3} />
              ) : (
                <>
                  {insecureNotice && (
                    <Notice tone={insecureNotice.tone}>{insecureNotice.text}</Notice>
                  )}
                  {keyConfigMeta
                    .filter((meta) => !["ip", "app_name"].includes(meta.name))
                    .map((meta) => (
                      <SiteConfigControl
                        key={meta.name}
                        meta={meta}
                        value={values[meta.name] ?? ""}
                        onChange={(value) => setValue(meta.name, value)}
                        disabled={controlsDisabled}
                      />
                    ))}
                </>
              )}
            </ConfigSection>
          </TabsContent>
          <TabsContent value="backup" className="mt-0 space-y-6">
            <ConfigSection
              title="下载备份"
              description="导出包含用户、节点密钥和面板配置的一致性 SQLite 快照，请安全保管"
            >
              <Button
                variant="outline"
                loading={busy === "backup"}
                disabled={busy !== null}
                onClick={() => void backup()}
              >
                <Download aria-hidden="true" />
                下载备份
              </Button>
            </ConfigSection>
            <Card className="border-status-critical">
              <CardHeader>
                <CardTitle>从备份恢复</CardTitle>
                <CardDescription>
                  恢复将覆盖当前数据库与节点配置，建议先下载一份当前备份
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                {summary ? (
                  <div
                    role="status"
                    className="space-y-3 rounded-md border border-hairline bg-canvas-soft p-4 text-sm text-body"
                  >
                    <p className="text-body-strong text-ink">数据已恢复，3 秒后自动退出登录</p>
                    <dl className="space-y-2">
                      <div>
                        <dt className="text-mute">恢复时间</dt>
                        <dd className="font-mono">{formatDateTime(summary.restoredAt)}</dd>
                      </div>
                      {summary.preRestoreBackup && (
                        <div>
                          <dt className="text-mute">恢复前数据库备份</dt>
                          <dd className="break-all font-mono">{summary.preRestoreBackup}</dd>
                        </div>
                      )}
                    </dl>
                  </div>
                ) : (
                  <>
                    <FormField
                      label="SQLite 备份文件"
                      htmlFor="restore-file"
                      hint="只支持本面板导出的 SQLite 备份，最大 256 MB"
                      error={fileError}
                    >
                      <Input
                        id="restore-file"
                        ref={fileRef}
                        type="file"
                        accept=".db,.sqlite,.sqlite3"
                        className="h-auto min-h-9 py-2 file:mr-3 file:text-body"
                        disabled={busy !== null}
                        onChange={(event) => {
                          const file = event.target.files?.[0] ?? null;
                          const error = file ? restoreFileError(file) : undefined;
                          setFileError(error);
                          setSelectedFile(error ? null : file);
                          setRestoreError("");
                          if (error) event.target.value = "";
                        }}
                      />
                    </FormField>
                    {selectedFile && (
                      <p className="break-all text-caption text-body">
                        <span className="font-mono">{selectedFile.name}</span> ·{" "}
                        {formatBytes(selectedFile.size)}
                      </p>
                    )}
                    {restoreError && <ErrorState error={restoreError} />}
                    {busy === "restore" && (
                      <div role="status" className="text-sm text-body">
                        正在上传并恢复数据库，请保持页面打开，恢复过程不可取消
                      </div>
                    )}
                    <Button
                      variant="danger"
                      loading={busy === "restore"}
                      disabled={!selectedFile || busy !== null}
                      onClick={() => void restore()}
                    >
                      <Upload aria-hidden="true" />
                      恢复备份
                    </Button>
                  </>
                )}
              </CardContent>
            </Card>
          </TabsContent>
          <TabsContent
            value="r2"
            forceMount
            className="mt-0 space-y-4 data-[state=inactive]:hidden"
          >
            <p className="text-sm text-mute">
              R2 设置通过独立接口保存，使用下方“保存 R2 设置”，不计入页面底部的未保存修改数量
            </p>
            <R2BackupCard />
          </TabsContent>
          <TabsContent value="advanced" className="mt-0">
            <Card>
              <CardHeader className="flex-row flex-wrap items-center justify-between gap-3 space-y-0">
                <div className="space-y-1">
                  <CardTitle>高级配置项</CardTitle>
                  <CardDescription>仅展示后端返回的自定义配置，修改会在保存后生效</CardDescription>
                </div>
                <Button
                  variant="outline"
                  disabled={controlsDisabled}
                  onClick={() => setAddOpen(true)}
                >
                  <Plus aria-hidden="true" />
                  新增配置项
                </Button>
              </CardHeader>
              <CardContent className="space-y-4">
                {configsQuery.isPending ? (
                  <LoadingRows />
                ) : advancedItems.length === 0 ? (
                  <EmptyState
                    title="暂无自定义配置项"
                    description="仅在需要扩展功能时添加自定义键值"
                  />
                ) : (
                  advancedItems.map(([name, value]) => (
                    <div key={name} className="space-y-3 rounded-md border border-hairline p-4">
                      <div className="flex min-w-0 items-center justify-between gap-3">
                        <span className="min-w-0 break-all font-mono text-sm text-ink">{name}</span>
                        {!isProtectedConfigName(name) && (
                          <Button
                            size="icon"
                            variant="ghost"
                            aria-label={`删除配置项 ${name}`}
                            disabled={controlsDisabled}
                            onClick={() => void remove(name)}
                          >
                            <Trash2 aria-hidden="true" className="text-status-critical-text" />
                          </Button>
                        )}
                      </div>
                      <FormField label={`${name} 的值`} htmlFor={`config-item-${name}`}>
                        {value.length > 80 || value.includes("\n") ? (
                          <Textarea
                            id={`config-item-${name}`}
                            rows={6}
                            className="font-mono text-code"
                            value={value}
                            disabled={controlsDisabled}
                            onChange={(event) => setValue(name, event.target.value)}
                          />
                        ) : (
                          <Input
                            id={`config-item-${name}`}
                            className="font-mono text-code"
                            value={value}
                            disabled={controlsDisabled}
                            onChange={(event) => setValue(name, event.target.value)}
                          />
                        )}
                      </FormField>
                    </div>
                  ))
                )}
              </CardContent>
            </Card>
          </TabsContent>
        </div>
      </Tabs>
      {dirtyCount > 0 && (
        <div
          role="region"
          aria-label="未保存的系统配置"
          className="sticky bottom-0 z-20 flex flex-wrap items-center justify-between gap-3 rounded-md border border-hairline bg-canvas-soft p-4"
        >
          <p role="status" className="text-sm text-ink">
            {dirtyCount} 项未保存的修改
          </p>
          <div className="flex gap-2">
            <Button
              variant="outline"
              disabled={controlsDisabled}
              onClick={() => {
                setEdits({});
                setAddressError(undefined);
              }}
            >
              放弃修改
            </Button>
            <Button loading={saveMutation.isPending} disabled={controlsDisabled} onClick={save}>
              <Save aria-hidden="true" />
              保存配置
            </Button>
          </div>
        </div>
      )}
      <AddConfigDialog
        key={String(addOpen)}
        open={addOpen}
        onOpenChange={setAddOpen}
        existing={Object.keys(values)}
        onAdd={(name, value) => setValue(name, value)}
      />
    </div>
  );
}

function ConfigSection({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">{children}</CardContent>
    </Card>
  );
}
function Notice({ tone, children }: { tone: "danger" | "warning"; children: React.ReactNode }) {
  // 复用规范中已验证的语义文字/背景组合；危险边框与图标不承担正文信息。
  return (
    <div
      role="alert"
      className={`flex items-start gap-2 rounded-md border p-3 text-sm ${tone === "danger" ? "border-status-critical bg-status-critical-bg text-status-critical-text" : "border-status-warning bg-status-warning-bg text-status-warning"}`}
    >
      <AlertTriangle aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0">{children}</div>
    </div>
  );
}
function SiteConfigControl({
  meta,
  value,
  onChange,
  disabled,
  error,
}: {
  meta: KeyConfigMeta;
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
  error?: string;
}) {
  const id = `site-config-${meta.name}`;
  return (
    <FormField
      label={meta.label}
      htmlFor={id}
      hint={meta.description}
      error={error}
      required={meta.name === "ip"}
    >
      {meta.type === "switch" ? (
        <Switch
          id={id}
          checked={value === "true"}
          disabled={disabled}
          onCheckedChange={(checked) => onChange(String(checked))}
        />
      ) : meta.type === "select" ? (
        <Select
          value={value || meta.options?.[0]?.value || ""}
          onValueChange={onChange}
          disabled={disabled}
        >
          <SelectTrigger id={id}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {meta.options?.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={id}
          value={value}
          placeholder={meta.placeholder}
          disabled={disabled}
          className={meta.name === "app_name" ? "" : "font-mono"}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </FormField>
  );
}
function AddConfigDialog({
  open,
  onOpenChange,
  existing,
  onAdd,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing: string[];
  onAdd: (name: string, value: string) => void;
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [error, setError] = useState<string>();
  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    const invalid = customConfigNameError(name, existing);
    setError(invalid);
    if (invalid) {
      document.getElementById("new-config-name")?.focus();
      return;
    }
    onAdd(name, value);
    onOpenChange(false);
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setName("");
          setValue("");
          setError(undefined);
        }
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新增配置项</DialogTitle>
          <DialogDescription>填写自定义键值，添加后还需保存配置才会写入服务器</DialogDescription>
        </DialogHeader>
        <form id="new-config-form" className="space-y-4" onSubmit={submit}>
          <FormField
            label="键名"
            htmlFor="new-config-name"
            required
            error={error}
            hint="只允许小写字母、数字和下划线，不得使用系统内部键"
          >
            <Input
              id="new-config-name"
              autoFocus
              className="font-mono"
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                setError(undefined);
              }}
            />
          </FormField>
          <FormField
            label="值"
            htmlFor="new-config-value"
            hint="支持多行文本；配置用途由使用该键的功能决定"
          >
            <Textarea
              id="new-config-value"
              rows={6}
              className="font-mono text-code"
              value={value}
              onChange={(event) => setValue(event.target.value)}
            />
          </FormField>
        </form>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="new-config-form">
            添加配置项
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
function isPanelAddressLike(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed) return true;
  try {
    const url = new URL(/^https?:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`);
    return !!url.hostname && (!url.port || Number(url.port) <= 65535);
  } catch {
    return false;
  }
}
function insecureDownloadNotice(
  persisted: Record<string, string>,
  values: Record<string, string>,
  dirty: boolean,
): { tone: "danger" | "warning"; text: string } | null {
  // 必须使用服务端已保存值计算实际策略；环境变量覆盖最高，草稿开关不能提前声称生效。
  const state = resolveInsecureDownloadNoticeState(
    persisted.allow_insecure_node_downloads ?? "false",
    values.allow_insecure_node_downloads ?? "false",
    persisted[allowInsecureNodeDownloadsEnvOverrideName] ?? "false",
    dirty,
  );
  switch (state) {
    case "environment_override":
      return {
        tone: "danger",
        text: "部署环境变量 ALLOW_INSECURE_NODE_DOWNLOADS=true 仍在生效，公网 HTTP 安装/升级继续被允许，页面开关无法单独关闭。要恢复强制 HTTPS，请确保开关已关闭，将环境变量改为 false 后重启面板",
      };
    case "enabled":
      return {
        tone: "danger",
        text: "HTTP 节点安装/升级已开启。节点密钥和下载程序将通过明文链路传输，可能被监听或篡改；完成安装或迁移后请立即关闭",
      };
    case "disable_pending":
      return {
        tone: "danger",
        text: "HTTP 节点安装/升级当前仍已开启。你已选择关闭，但更改尚未保存；保存成功后才会恢复强制 HTTPS",
      };
    case "enable_pending":
      return {
        tone: "warning",
        text: "你已选择允许 HTTP 节点安装/升级，但更改尚未生效；保存后节点密钥和下载程序可能通过明文链路传输",
      };
    default:
      return null;
  }
}
