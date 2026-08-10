/**
 * Canonical panel endpoint resolution.
 *
 * Precedence: stored panel_address > VITE_API_BASE > browser origin.
 * The resolver is pure: adapters pass storage/env/origin in, so every consumer
 * (axios base, WebSocket URL, subscription links, config previews) derives
 * URLs from one implementation.
 */

export interface PanelEndpointInput {
  stored?: string | null;
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
  const source = input.stored?.trim() || input.env?.trim() || input.origin;
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

/** Browser adapter: reads storage/env/origin and delegates to the resolver. */
export function currentPanelEndpoint(): PanelEndpoint {
  if (typeof window === "undefined") {
    const env = (import.meta as ImportMeta & { env?: Record<string, string | undefined> }).env
      ?.VITE_API_BASE as string | undefined;
    return resolvePanelEndpoint({
      env,
      origin: "http://127.0.0.1",
      pageProtocol: "http:",
    });
  }
  return resolvePanelEndpoint({
    stored: window.localStorage.getItem("panel_address"),
    env: (import.meta as ImportMeta & { env?: Record<string, string | undefined> }).env
      ?.VITE_API_BASE as string | undefined,
    origin: window.location.origin,
    pageProtocol: window.location.protocol === "https:" ? "https:" : "http:",
  });
}
