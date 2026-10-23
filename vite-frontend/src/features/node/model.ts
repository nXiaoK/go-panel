import { resourceSearch } from "@/features/search/route-search";
import { currentPanelEndpoint } from "@/lib/panel-endpoint";
export function isMissingPanelAddressError(message: string | undefined | null): boolean {
  const text = String(message ?? "").toLowerCase();
  return (
    text.includes("设置ip") ||
    text.includes("面板公网地址") ||
    text.includes("panel public address")
  );
}

export function buildSystemInfoURL(): string {
  return currentPanelEndpoint().wsURL();
}

export interface NodeForm {
  id: number | null;
  name: string;
  ipString: string;
  serverIp: string;
  portSta: number;
  portEnd: number;
  http: number;
  tls: number;
  socks: number;
  forwardMode: "gost" | "nftables";
}

// 新节点默认使用 GOST 和 1000–65535 端口范围；协议屏蔽默认关闭，仅在线编辑时可修改。
export const emptyNodeForm = (): NodeForm => ({
  id: null,
  name: "",
  ipString: "",
  serverIp: "",
  portSta: 1000,
  portEnd: 65535,
  http: 0,
  tls: 0,
  socks: 0,
  forwardMode: "gost",
});
const IPV4 =
  /^(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}$/;
const DOMAIN =
  /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$/;
export function validNodeAddress(address: string): boolean {
  const value = address.trim();
  if (value === "localhost") return true;
  if (value.includes(":")) {
    try {
      return new URL(`http://[${value}]`).hostname.startsWith("[");
    } catch {
      return false;
    }
  }
  if (/^[\d.]+$/.test(value)) return IPV4.test(value);
  return value.length <= 253 && DOMAIN.test(value);
}
export function validateNodeForm(form: NodeForm): Partial<Record<keyof NodeForm, string>> {
  const errors: Partial<Record<keyof NodeForm, string>> = {};
  if (!form.name.trim()) errors.name = "请输入节点名称";
  else if (form.name.trim().length < 2 || form.name.trim().length > 50)
    errors.name = "节点名称长度需在 2–50 之间";
  if (!form.serverIp.trim()) errors.serverIp = "请输入服务器 IP 或域名";
  else if (!validNodeAddress(form.serverIp)) errors.serverIp = "请输入合法的服务器 IP 或域名";
  const ips = form.ipString
    .split("\n")
    .map((ip) => ip.trim())
    .filter(Boolean);
  if (!ips.length) errors.ipString = "至少填写一个入口 IP";
  else {
    const invalid = ips.findIndex((ip) => !validNodeAddress(ip));
    if (invalid >= 0) errors.ipString = `第 ${invalid + 1} 行地址不合法`;
  }
  if (!Number.isInteger(form.portSta) || form.portSta < 1 || form.portSta > 65535)
    errors.portSta = "端口需为 1–65535 的整数";
  if (!Number.isInteger(form.portEnd) || form.portEnd < 1 || form.portEnd > 65535)
    errors.portEnd = "端口需为 1–65535 的整数";
  else if (form.portEnd < form.portSta) errors.portEnd = "结束端口不能小于起始端口";
  return errors;
}

// 视图偏好仅影响本浏览器展示；存储不可用时回退网格，不影响节点配置。
const nodeViewKey = "go-panel-node-view";
export function readNodeView(): "grid" | "list" {
  try {
    return localStorage.getItem(nodeViewKey) === "list" ? "list" : "grid";
  } catch {
    return "grid";
  }
}
export function saveNodeView(view: "grid" | "list") {
  try {
    localStorage.setItem(nodeViewKey, view);
  } catch {
    /* 无本地存储时仍可切换本次视图。 */
  }
}

export function nodeSearch(search: Record<string, unknown>) {
  return {
    ...resourceSearch(search),
    ...("mode" in search
      ? { mode: search.mode === "gost" || search.mode === "nftables" ? search.mode : undefined }
      : {}),
    ...("status" in search
      ? {
          status:
            typeof search.status === "string" &&
            ["online", "offline", "failed"].includes(search.status)
              ? search.status
              : undefined,
        }
      : {}),
  };
}
