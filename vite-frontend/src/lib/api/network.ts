import axios, { type AxiosResponse } from "axios";
import type { ApiResponse } from "@/lib/types";
import { currentPanelEndpoint } from "@/lib/panel-endpoint";
import { invalidateSession } from "@/lib/session";

/**
 * Base URL resolution (per-request, not global mutation) is delegated to the
 * canonical panel endpoint resolver: stored panel_address > VITE_API_BASE >
 * browser origin, with legacy /api/v1 suffixes normalized.
 */
export function computeBaseURL(): string {
  return currentPanelEndpoint().apiBase;
}

const http = axios.create();

export const PANEL_ADDRESS_CHANGED_EVENT = "panel-address-changed";

// 面板地址为空时恢复同源 /api/v1 默认端点；非空值会成为后续所有 API 请求的目标。
// 地址切换可能把令牌发送到另一台服务器，因此仅保存用户明确填写的值，并通知数据层清空旧站点缓存。
export function setPanelAddress(address: string) {
  if (typeof window === "undefined") return;
  const next = address.trim();
  const previous = window.localStorage.getItem("panel_address") || "";
  if (next) window.localStorage.setItem("panel_address", next);
  else window.localStorage.removeItem("panel_address");
  if (next !== previous) window.dispatchEvent(new Event(PANEL_ADDRESS_CHANGED_EVENT));
}
export function getPanelAddress(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem("panel_address") || "";
}
function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem("token");
}

function handleTokenExpired() {
  // 集中会话失效：并发的多个受保护 401 只清理并跳转一次。
  invalidateSession("token expired");
}

function isTokenExpired(res: ApiResponse) {
  return (
    res &&
    res.code === 401 &&
    (res.msg === "未登录或token已过期" ||
      res.msg === "无效的token或token已过期" ||
      res.msg === "无法获取用户权限信息")
  );
}

function baseHeaders() {
  return {
    Authorization: getToken() || "",
  };
}

function axiosStatus(err: unknown): number | undefined {
  if (axios.isAxiosError(err)) return err.response?.status;
  return undefined;
}

function axiosMessage(err: unknown): string {
  if (axios.isAxiosError(err)) return err.message || "网络请求失败";
  if (err instanceof Error) return err.message;
  return "网络请求失败";
}

const Network = {
  get<T = unknown>(path = "", params: Record<string, unknown> = {}): Promise<ApiResponse<T>> {
    return new Promise((resolve, reject) => {
      http
        .get(path, {
          baseURL: computeBaseURL(),
          params,
          timeout: 30000,
          headers: baseHeaders(),
        })
        .then((r: AxiosResponse<ApiResponse<T>>) => {
          if (isTokenExpired(r.data)) {
            handleTokenExpired();
            return reject(new Error("token expired"));
          }
          resolve(r.data);
        })
        .catch((err: unknown) => {
          if (axiosStatus(err) === 401) {
            handleTokenExpired();
            return reject(new Error("unauthorized"));
          }
          resolve({ code: -1, msg: axiosMessage(err), data: null as T });
        });
    });
  },
  post<T = unknown>(path = "", data: unknown = {}): Promise<ApiResponse<T>> {
    return new Promise((resolve, reject) => {
      http
        .post(path, data, {
          baseURL: computeBaseURL(),
          timeout: 30000,
          headers: {
            ...baseHeaders(),
            "Content-Type": "application/json",
          },
        })
        .then((r: AxiosResponse<ApiResponse<T>>) => {
          if (isTokenExpired(r.data)) {
            handleTokenExpired();
            return reject(new Error("token expired"));
          }
          resolve(r.data);
        })
        .catch((err: unknown) => {
          if (axiosStatus(err) === 401) {
            handleTokenExpired();
            return reject(new Error("unauthorized"));
          }
          resolve({ code: -1, msg: axiosMessage(err), data: null as T });
        });
    });
  },
  postWithTimeout<T = unknown>(
    path = "",
    data: unknown = {},
    timeout = 30000,
  ): Promise<ApiResponse<T>> {
    return new Promise((resolve, reject) => {
      http
        .post(path, data, {
          baseURL: computeBaseURL(),
          timeout,
          headers: {
            ...baseHeaders(),
            "Content-Type": "application/json",
          },
        })
        .then((r: AxiosResponse<ApiResponse<T>>) => {
          if (isTokenExpired(r.data)) {
            handleTokenExpired();
            return reject(new Error("token expired"));
          }
          resolve(r.data);
        })
        .catch((err: unknown) => {
          if (axiosStatus(err) === 401) {
            handleTokenExpired();
            return reject(new Error("unauthorized"));
          }
          resolve({ code: -1, msg: axiosMessage(err), data: null as T });
        });
    });
  },
  download(path = ""): Promise<Blob> {
    return new Promise((resolve, reject) => {
      http
        .get(path, {
          baseURL: computeBaseURL(),
          timeout: 30000,
          responseType: "blob",
          headers: baseHeaders(),
        })
        .then(async (r: AxiosResponse<Blob>) => {
          const ct = String(r.headers["content-type"] || "");
          if (ct.includes("application/json")) {
            const txt = await r.data.text();
            try {
              const json = JSON.parse(txt) as ApiResponse;
              if (isTokenExpired(json)) {
                handleTokenExpired();
                return reject(new Error("token expired"));
              }
              reject(new Error(json.msg || "下载失败"));
            } catch {
              reject(new Error("下载失败"));
            }
            return;
          }
          resolve(r.data);
        })
        .catch((err: unknown) => {
          if (axiosStatus(err) === 401) {
            handleTokenExpired();
            return reject(new Error("unauthorized"));
          }
          reject(err instanceof Error ? err : new Error(axiosMessage(err)));
        });
    });
  },
  upload<T = unknown>(path = "", data: FormData): Promise<ApiResponse<T>> {
    return new Promise((resolve, reject) => {
      http
        .post(path, data, {
          baseURL: computeBaseURL(),
          timeout: 60000,
          headers: baseHeaders(),
        })
        .then((r: AxiosResponse<ApiResponse<T>>) => {
          if (isTokenExpired(r.data)) {
            handleTokenExpired();
            return reject(new Error("token expired"));
          }
          resolve(r.data);
        })
        .catch((err: unknown) => {
          if (axiosStatus(err) === 401) {
            handleTokenExpired();
            return reject(new Error("unauthorized"));
          }
          resolve({ code: -1, msg: axiosMessage(err), data: null as T });
        });
    });
  },
};

export default Network;
