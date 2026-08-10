import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import {
  Globe2,
  SlidersHorizontal,
  Save,
  RefreshCcw,
  Download,
  Upload,
  Plus,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { R2BackupCard } from "@/components/r2-backup-card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { getConfigs, updateConfigs, downloadSiteBackup, restoreSiteBackup } from "@/lib/api";
import { queries, unwrap } from "@/lib/api/query";
import { PageHeader, QueryErrorNotice } from "@/components/page";
import {
  allowInsecureNodeDownloadsEnvOverrideName,
  configItemsToMap,
  keyConfigMeta,
  keyConfigNames,
  normalizeConfigItems,
  readOnlyConfigNames,
  resolveInsecureDownloadNoticeState,
  shouldShowKeyConfig,
  type ConfigKV,
  type KeyConfigMeta,
} from "@/lib/config-items";
import { setCachedAppName } from "@/lib/site-config";

export const Route = createFileRoute("/_app/config")({
  head: () => ({ meta: [{ title: "系统配置 · Flux Panel" }] }),
  component: ConfigPage,
});

function ConfigPage() {
  const queryClient = useQueryClient();
  const [draftItems, setDraftItems] = useState<ConfigKV[]>([]);
  const [dirty, setDirty] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<"backup" | "restore" | null>(null);
  const fileRef = useRef<HTMLInputElement | null>(null);

  const configsQuery = useQuery({
    queryKey: queries.config.list(),
    queryFn: () =>
      getConfigs().then((res) => {
        if (res.code !== 0) throw new Error(res.msg || "加载失败");
        return normalizeConfigItems(res.data);
      }),
  });

  // 草稿编辑区：每当服务端数据变化时同步一次，编辑不会回写到缓存。
  const [syncedFrom, setSyncedFrom] = useState<ConfigKV[] | null>(null);
  useEffect(() => {
    if (configsQuery.data && configsQuery.data !== syncedFrom) {
      setDraftItems(configsQuery.data);
      setSyncedFrom(configsQuery.data);
      setDirty({});
    }
  }, [configsQuery.data, syncedFrom]);

  const items = draftItems;
  const persistedValues = useMemo(
    () => configItemsToMap(configsQuery.data ?? []),
    [configsQuery.data],
  );
  const loading = configsQuery.isPending;
  const values = useMemo(() => configItemsToMap(items), [items]);
  const advancedItems = useMemo(
    () =>
      items.filter(
        (item) => !keyConfigNames.includes(item.name) && !readOnlyConfigNames.includes(item.name),
      ),
    [items],
  );
  const suggestedPanelAddress =
    typeof window === "undefined"
      ? ""
      : `${window.location.hostname}:${window.location.port || (window.location.protocol === "https:" ? "443" : "80")}`;

  const saveMutation = useMutation({
    mutationFn: (payload: Record<string, string>) => updateConfigs(payload).then(unwrap),
    onSuccess: (_data, payload) => {
      if (payload.app_name !== undefined) setCachedAppName(payload.app_name);
      toast.success("已保存");
      setDirty({});
      void queryClient.invalidateQueries({ queryKey: queries.config.list() });
    },
    onError: (error: Error) => toast.error(error.message || "保存失败"),
  });
  const saving = saveMutation.isPending;

  const reload = () => {
    void queryClient.invalidateQueries({ queryKey: queries.config.list() });
  };

  const applyValues = (updates: Record<string, string>) => {
    setDraftItems((current) => {
      const next = new Map(current.map((item) => [item.name, item.value]));
      Object.entries(updates).forEach(([name, value]) => next.set(name, value));
      return normalizeConfigItems(Object.fromEntries(next));
    });
    setDirty((d) => ({ ...d, ...updates }));
  };

  const setValue = (name: string, value: string) => applyValues({ [name]: value });

  const addKey = () => {
    const name = window.prompt("新配置项名称？");
    if (!name?.trim()) return;
    const cleanName = name.trim();
    if (items.some((it) => it.name === cleanName)) return toast.error("名称已存在");
    applyValues({ [cleanName]: "" });
  };
  const remove = (name: string) => {
    // 仅从本地列表移除，避免向后端提交空字符串误改配置。
    // 如需真正删除，请由后端提供 delete 接口后接入。
    setDraftItems((s) => s.filter((it) => it.name !== name));
    setDirty((d) => {
      const n = { ...d };
      delete n[name];
      return n;
    });
    toast.info("已从列表移除（后端未提供删除接口，保存不会同步）");
  };

  const normalizeForSave = (payload: Record<string, string>) => {
    const next = { ...payload };
    if (next.ip !== undefined) next.ip = next.ip.trim().replace(/\/+$/, "");
    return next;
  };

  const save = async () => {
    if (Object.keys(dirty).length === 0) return toast.info("没有需要保存的更改");
    const payload = normalizeForSave(dirty);
    if (payload.ip !== undefined && !isPanelAddressLike(payload.ip)) {
      toast.error("面板公网地址格式不正确，请填写 host:port 或 http(s)://host:port");
      return;
    }
    saveMutation.mutate(payload);
  };

  const backup = async () => {
    setBusy("backup");
    try {
      const blob = await downloadSiteBackup();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `flux-panel-backup-${new Date().toISOString().slice(0, 10)}.db`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("备份已下载");
    } catch (e: any) {
      toast.error(e?.message || "备份失败");
    } finally {
      setBusy(null);
    }
  };

  const restore = async (file: File) => {
    if (!confirm(`确认使用 "${file.name}" 恢复站点？将覆盖当前数据。`)) return;
    setBusy("restore");
    try {
      const res = await restoreSiteBackup(file);
      if (res.code === 0) toast.success("已恢复，稍后请重新登录");
      else toast.error(res.msg || "恢复失败");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "恢复失败");
    } finally {
      setBusy(null);
    }
  };

  const dirtyCount = Object.keys(dirty).length;
  const insecureDownloadNoticeState = resolveInsecureDownloadNoticeState(
    persistedValues.allow_insecure_node_downloads ?? "false",
    values.allow_insecure_node_downloads ?? "false",
    persistedValues[allowInsecureNodeDownloadsEnvOverrideName] ?? "false",
    dirty.allow_insecure_node_downloads !== undefined,
  );

  // 告警以已保存策略为准，并单独标出尚未保存的草稿，避免管理员误以为
  // 拨动开关后策略已经改变；部署级环境覆盖始终拥有最高提示优先级。
  let insecureDownloadNotice: { tone: "danger" | "warning"; text: string } | null = null;
  if (insecureDownloadNoticeState === "environment_override") {
    insecureDownloadNotice = {
      tone: "danger",
      text: "部署环境变量 ALLOW_INSECURE_NODE_DOWNLOADS=true 仍在生效，公网 HTTP 安装/升级继续被允许，页面开关无法单独关闭。要恢复强制 HTTPS，请确保下方开关已关闭，将环境变量改为 false 后重启面板。",
    };
  } else if (insecureDownloadNoticeState === "enabled") {
    insecureDownloadNotice = {
      tone: "danger",
      text: "HTTP 节点安装/升级已开启。节点密钥和下载程序将通过明文链路传输，可能被监听或篡改；完成安装或迁移后请立即关闭。",
    };
  } else if (insecureDownloadNoticeState === "disable_pending") {
    insecureDownloadNotice = {
      tone: "danger",
      text: "HTTP 节点安装/升级当前仍已开启。你已选择关闭，但更改尚未保存；保存成功后才会恢复强制 HTTPS。",
    };
  } else if (insecureDownloadNoticeState === "enable_pending") {
    insecureDownloadNotice = {
      tone: "warning",
      text: "你已选择允许 HTTP 节点安装/升级，但更改尚未生效；保存后节点密钥和下载程序可能通过明文链路传输。",
    };
  }

  return (
    <div className="space-y-6">
      <PageHeader
        icon={<SlidersHorizontal className="h-5 w-5" />}
        title="系统配置"
        description="全局参数、gost 模板、站点备份与恢复"
        actions={
          <>
            <Button variant="outline" size="sm" onClick={reload} disabled={configsQuery.isFetching}>
              <RefreshCcw className={`h-4 w-4 ${configsQuery.isFetching ? "animate-spin" : ""}`} />{" "}
              刷新
            </Button>
            <Button size="sm" onClick={save} disabled={saving || dirtyCount === 0}>
              <Save className="h-4 w-4" />{" "}
              {saving ? "保存中…" : `保存${dirtyCount ? ` (${dirtyCount})` : ""}`}
            </Button>
          </>
        }
      />

      <QueryErrorNotice error={configsQuery.error} onRetry={() => void configsQuery.refetch()} />

      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Globe2 className="h-4 w-4 text-primary" />
            <CardTitle className="text-base">站点配置</CardTitle>
          </div>
          <CardDescription>
            节点安装、登录验证码和站点展示依赖这些配置。节点安装前至少需要设置面板公网地址。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {!values.ip && (
            <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300">
              尚未设置面板公网地址，节点安装命令暂时无法生成。
              {suggestedPanelAddress && (
                <Button
                  type="button"
                  variant="link"
                  className="ml-1 h-auto p-0 text-amber-700 underline dark:text-amber-300"
                  onClick={() => setValue("ip", suggestedPanelAddress)}
                >
                  使用当前地址 {suggestedPanelAddress}
                </Button>
              )}
            </div>
          )}
          {insecureDownloadNotice && (
            <div
              role="alert"
              className={
                insecureDownloadNotice.tone === "danger"
                  ? "rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"
                  : "rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300"
              }
            >
              {insecureDownloadNotice.text}
            </div>
          )}
          <div className="grid gap-4 lg:grid-cols-2">
            {keyConfigMeta
              .filter((item) => shouldShowKeyConfig(item, values))
              .map((item) => (
                <SiteConfigControl
                  key={item.name}
                  meta={item}
                  value={values[item.name] ?? ""}
                  changed={dirty[item.name] !== undefined}
                  onChange={(value) => setValue(item.name, value)}
                />
              ))}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle className="text-base">高级配置项</CardTitle>
            <CardDescription>额外键 / 值配置，支持多行内容（如 gost 模板）</CardDescription>
          </div>
          <Button variant="outline" size="sm" onClick={addKey}>
            <Plus className="h-4 w-4" /> 新增
          </Button>
        </CardHeader>
        <CardContent className="space-y-4">
          {advancedItems.length === 0 && (
            <div className="rounded-md border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
              {loading ? "加载中…" : "暂无额外配置项"}
            </div>
          )}
          {advancedItems.map((it) => {
            const isLong = it.value.length > 80 || it.value.includes("\n");
            return (
              <div
                key={it.name}
                className={`rounded-md border p-3 ${dirty[it.name] !== undefined ? "border-primary/40 bg-primary/5" : "border-border"}`}
              >
                <div className="flex items-center justify-between gap-2">
                  <Label htmlFor={`config-item-${it.name}`} className="font-mono text-xs">
                    {it.name}
                  </Label>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => remove(it.name)}
                    className="h-7 w-7 text-muted-foreground hover:text-destructive"
                    aria-label={`移除配置项 ${it.name}`}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
                {isLong ? (
                  <Textarea
                    id={`config-item-${it.name}`}
                    rows={6}
                    className="mt-2 font-mono text-xs"
                    value={it.value}
                    onChange={(e) => setValue(it.name, e.target.value)}
                  />
                ) : (
                  <Input
                    id={`config-item-${it.name}`}
                    className="mt-2 font-mono text-xs"
                    value={it.value}
                    onChange={(e) => setValue(it.name, e.target.value)}
                  />
                )}
              </div>
            );
          })}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">站点备份</CardTitle>
          <CardDescription>下载全站数据快照，或使用备份文件恢复</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-3">
            <Button variant="outline" onClick={backup} disabled={busy === "backup"}>
              <Download className="h-4 w-4" /> {busy === "backup" ? "打包中…" : "下载备份"}
            </Button>
            <input
              ref={fileRef}
              type="file"
              accept=".db,.sqlite,.sqlite3,.tar.gz,.gz,.zip,.tar,application/vnd.sqlite3,application/x-sqlite3"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void restore(f);
                e.target.value = "";
              }}
            />
            <Button
              variant="outline"
              onClick={() => fileRef.current?.click()}
              disabled={busy === "restore"}
            >
              <Upload className="h-4 w-4" /> {busy === "restore" ? "恢复中…" : "从备份恢复"}
            </Button>
          </div>
          <Separator className="my-4" />
          <p className="text-xs text-muted-foreground">
            恢复操作会覆盖当前数据库与节点配置，建议先下载最新备份。
          </p>
        </CardContent>
      </Card>

      <R2BackupCard />
    </div>
  );
}

function SiteConfigControl({
  meta,
  value,
  changed,
  onChange,
}: {
  meta: KeyConfigMeta;
  value: string;
  changed: boolean;
  onChange: (value: string) => void;
}) {
  const controlID = `site-config-${meta.name}`;
  return (
    <div
      className={`rounded-md border p-3 ${
        changed ? "border-primary/40 bg-primary/5" : "border-border"
      }`}
    >
      <div className="mb-2 flex items-start justify-between gap-3">
        <div>
          <Label htmlFor={controlID} className="text-sm font-medium">
            {meta.label}
          </Label>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{meta.description}</p>
        </div>
        {changed && (
          <span className="rounded bg-primary/10 px-2 py-0.5 text-[11px] text-primary">已修改</span>
        )}
      </div>
      {meta.type === "switch" ? (
        <div className="flex h-9 items-center">
          <Switch
            id={controlID}
            checked={value === "true"}
            onCheckedChange={(checked) => onChange(String(checked))}
          />
        </div>
      ) : meta.type === "select" ? (
        <Select value={value || meta.options?.[0]?.value || ""} onValueChange={onChange}>
          <SelectTrigger id={controlID}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(meta.options ?? []).map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={controlID}
          className="font-mono text-xs"
          value={value}
          placeholder={meta.placeholder}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  );
}

function isPanelAddressLike(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed) return true;
  const candidate = /^https?:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
  try {
    const url = new URL(candidate);
    return !!url.hostname && (!url.port || Number(url.port) <= 65535);
  } catch {
    return false;
  }
}
