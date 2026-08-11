export interface ConfigKV {
  name: string;
  value: string;
}

export interface KeyConfigOption {
  label: string;
  value: string;
  description?: string;
}

export interface KeyConfigMeta {
  name: string;
  label: string;
  placeholder?: string;
  description: string;
  type: "input" | "switch" | "select";
  defaultValue?: string;
  options?: KeyConfigOption[];
  dependsOn?: string;
  dependsValue?: string;
}

// 该键由后端根据部署环境只读返回，不会写入数据库。
// true 表示环境变量仍在强制放宽 HTTP 策略，页面开关无法单独将其关闭。
export const allowInsecureNodeDownloadsEnvOverrideName =
  "allow_insecure_node_downloads_env_override";

export const readOnlyConfigNames = [allowInsecureNodeDownloadsEnvOverrideName];

export type InsecureDownloadNoticeState =
  "environment_override" | "enabled" | "disable_pending" | "enable_pending" | null;

// 根据后端已保存值、当前草稿和部署环境覆盖计算真实告警状态。
// 环境变量覆盖优先级最高；未保存的开关不能被误报为已经生效。
export function resolveInsecureDownloadNoticeState(
  persistedValue: string,
  draftValue: string,
  envOverrideValue: string,
  dirty: boolean,
): InsecureDownloadNoticeState {
  if (envOverrideValue === "true") return "environment_override";
  if (persistedValue === "true") {
    return dirty && draftValue !== "true" ? "disable_pending" : "enabled";
  }
  return dirty && draftValue === "true" ? "enable_pending" : null;
}

export const keyConfigMeta: KeyConfigMeta[] = [
  {
    name: "ip",
    label: "面板公网地址",
    placeholder: "panel.example.com:6365 或 http://1.2.3.4:6365",
    description:
      "节点安装脚本会通过这个地址下载脚本并回连面板。请填写节点服务器可访问的面板公网地址，通常是域名/IP + 后端端口。",
    type: "input",
  },
  {
    name: "app_name",
    label: "应用名称",
    placeholder: "Flux Panel",
    description: "用于浏览器标题、导航展示和后续站点标识。",
    type: "input",
  },
  {
    // 默认留空并直连 GitHub；代理会接触可执行文件，必须使用自建或明确可信的 HTTPS 服务。
    name: "github_download_proxy",
    label: "GitHub 下载代理",
    placeholder: "https://github-proxy.example.com",
    description:
      "可选。用于订阅服务器脚本下载 GitHub API、Release 与 raw 文件；代理需支持“代理前缀 + 完整 GitHub URL”。留空时直连。代理可看到并篡改下载内容，请仅填写可信的 HTTPS 服务；节点 Agent 自身已从面板下载，不依赖此项。",
    type: "input",
  },
  {
    // 默认 false：只有管理员明确开启后，公网 HTTP 才能用于节点安装和升级。
    name: "allow_insecure_node_downloads",
    label: "允许 HTTP 节点安装/升级",
    description:
      "默认关闭并强制使用 HTTPS。仅在可信内网或临时迁移时开启；明文 HTTP 可能泄露节点密钥，并允许下载内容被监听或篡改。若部署环境变量 ALLOW_INSECURE_NODE_DOWNLOADS=true，页面关闭后也会继续放行并显示覆盖告警。",
    type: "switch",
    defaultValue: "false",
  },
];

export const keyConfigNames = keyConfigMeta.map((item) => item.name);

const hiddenConfigNames = new Set(["captcha_enabled", "captcha_type"]);

export function normalizeConfigItems(data: unknown): ConfigKV[] {
  const fromBackend: ConfigKV[] = Array.isArray(data)
    ? data.map((item: { name?: unknown; value?: unknown }) => ({
        name: String(item?.name ?? ""),
        value: String(item?.value ?? ""),
      }))
    : Object.entries((data ?? {}) as Record<string, unknown>).map(([name, value]) => ({
        name,
        value: String(value ?? ""),
      }));

  const values = new Map<string, string>();
  for (const item of fromBackend) {
    if (item.name) values.set(item.name, item.value);
  }

  return [
    ...keyConfigMeta.map((meta) => ({
      name: meta.name,
      value: values.get(meta.name) ?? meta.defaultValue ?? "",
    })),
    {
      // 后端旧版本未返回该只读状态时按 false 处理，避免把未知状态误报为已开启。
      name: allowInsecureNodeDownloadsEnvOverrideName,
      value: values.get(allowInsecureNodeDownloadsEnvOverrideName) ?? "false",
    },
    ...fromBackend.filter(
      (item) =>
        item.name &&
        !keyConfigNames.includes(item.name) &&
        !readOnlyConfigNames.includes(item.name) &&
        !hiddenConfigNames.has(item.name),
    ),
  ];
}

export function configItemsToMap(items: ConfigKV[]): Record<string, string> {
  return Object.fromEntries(
    items.filter((item) => item.name).map((item) => [item.name, item.value]),
  );
}

export function shouldShowKeyConfig(meta: KeyConfigMeta, values: Record<string, string>): boolean {
  if (!meta.dependsOn || meta.dependsValue === undefined) return true;
  return values[meta.dependsOn] === meta.dependsValue;
}
