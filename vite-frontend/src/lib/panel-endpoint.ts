/**
 * Canonical panel endpoint resolution.
 *
 * 配置优先级：开发环境 VITE_API_BASE > 当前浏览器同源地址。
 * 正式内嵌部署固定同源；浏览器旧版本保存的地址不再参与解析，避免令牌发送到其他站点。
 * The resolver is pure: adapters pass storage/env/origin in, so every consumer
 * (axios base, WebSocket URL, subscription links, config previews) derives
 * URLs from one implementation.
 */

export interface PanelEndpointInput {
  env?: string | null;
  origin: string;
  pageProtocol: "http:" | "https:";
}

export interface PanelEndpoint {
  /** Panel root without trailing slash, e.g. https://panel.example:6365/base */
  root: string;
  /** REST base with trailing slash, e.g. https://panel.example/api/v1/ */
  apiBase: string;
  wsURL(): string;
  subscriptionURL(token: string, format?: string): string;
}

function normalizeRoot(raw: string, pageProtocol: "http:" | "https:"): string {
  let value = raw.trim();
  if (!/^https?:\/\//i.test(value)) {
    value = `${pageProtocol}//${value}`;
  }
  const url = new URL(value);
  let path = url.pathname.replace(/\/+$/, "");
  // Legacy stored values may include the REST suffix; the root never does.
  if (path.toLowerCase().endsWith("/api/v1")) {
    path = path.slice(0, -"/api/v1".length);
  }
  path = path.replace(/\/+$/, "");
  return `${url.protocol}//${url.host}${path}`;
}

export function resolvePanelEndpoint(input: PanelEndpointInput): PanelEndpoint {
  const source = input.env?.trim() || input.origin;
  const root = normalizeRoot(source, input.pageProtocol);
  const apiBase = `${root}/api/v1/`;
  return {
    root,
    apiBase,
    wsURL() {
      const url = new URL(`${root}/system-info`);
      url.searchParams.set("type", "0");
      url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
      return url.toString();
    },
    subscriptionURL(token: string, format?: string) {
      const suffix = format ? `/${encodeURIComponent(format)}` : "";
      return `${apiBase}sub/render/${encodeURIComponent(token)}${suffix}`;
    },
  };
}

/** Browser adapter: reads env/origin and delegates to the resolver. */
export function currentPanelEndpoint(): PanelEndpoint {
  // 构建时读取开发专用地址；正式未配置时只使用当前页面同源，不读取浏览器历史地址。
  const env = import.meta.env.DEV ? import.meta.env.VITE_API_BASE : undefined;
  return resolvePanelEndpoint({
    env,
    origin: typeof window === "undefined" ? "http://127.0.0.1" : window.location.origin,
    pageProtocol:
      typeof window !== "undefined" && window.location.protocol === "https:" ? "https:" : "http:",
  });
}
