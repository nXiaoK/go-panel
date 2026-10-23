import axios, { type AxiosResponse } from "axios";
import type { ApiResponse } from "@/lib/types";
import { currentPanelEndpoint } from "@/lib/panel-endpoint";
import { invalidateSession, requirePasswordChange } from "@/lib/session";

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

// 登录是公开接口，其失败不能撤销当前会话；其余请求默认受保护。
function isPublicRequest(path: string): boolean {
  return path.split("?")[0].replace(/^\//, "").replace(/\/$/, "") === "user/login";
}

function sessionError(res: ApiResponse | undefined, path: string): Error | null {
  if (isPublicRequest(path)) return null;
  // 受保护接口的所有业务 401 均表示令牌不可再用，不能依赖可能变化的错误文案。
  if (res?.code === 401) {
    invalidateSession("expired");
    return new Error(res.msg || "登录已失效，请重新登录");
  }
  // 稳定业务码优先；兼容尚未升级后端的两条旧 403，权限不足等普通 403 保留会话。
  if (res?.code === 4031 || (res?.code === 403 && res.msg === "账号已被禁用")) {
    invalidateSession("disabled");
    return new Error(res.msg || "账号已被禁用，请联系管理员");
  }
  if (res?.code === 4032 || (res?.code === 403 && res.msg === "请先修改默认密码")) {
    requirePasswordChange();
    return new Error(res.msg || "请先修改默认密码");
  }
  return null;
}

async function axiosSessionError(err: unknown, path: string): Promise<Error | null> {
  if (isPublicRequest(path) || !axios.isAxiosError(err)) return null;
  // HTTP 401 本身已宣告令牌不可用，优先于代理或异常响应体里的业务码。
  if (err.response?.status === 401) {
    invalidateSession("expired");
    return new Error("登录已失效，请重新登录");
  }
  let data = err.response?.data;
  // 下载使用 blob 响应类型，非 2xx 的 JSON 错误体也需要先解析后分类。
  if (data && typeof data.text === "function") {
    try {
      data = JSON.parse(await data.text());
    } catch {
      data = undefined;
    }
  }
  return sessionError(data, path);
}

function baseHeaders() {
  return {
    Authorization: getToken() || "",
  };
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
          const failure = sessionError(r.data, path);
          if (failure) return reject(failure);
          resolve(r.data);
        })
        .catch(async (err: unknown) => {
          const failure = await axiosSessionError(err, path);
          if (failure) return reject(failure);
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
          const failure = sessionError(r.data, path);
          if (failure) return reject(failure);
          resolve(r.data);
        })
        .catch(async (err: unknown) => {
          const failure = await axiosSessionError(err, path);
          if (failure) return reject(failure);
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
          const failure = sessionError(r.data, path);
          if (failure) return reject(failure);
          resolve(r.data);
        })
        .catch(async (err: unknown) => {
          const failure = await axiosSessionError(err, path);
          if (failure) return reject(failure);
          resolve({ code: -1, msg: axiosMessage(err), data: null as T });
        });
    });
  },
  // 默认下载仍为 30 秒；仅已知大文件接口显式传入更长毫秒期限，避免全局慢请求占用。
  download(path = "", timeout = 30000): Promise<Blob> {
    return new Promise((resolve, reject) => {
      http
        .get(path, {
          baseURL: computeBaseURL(),
          timeout,
          responseType: "blob",
          headers: baseHeaders(),
        })
        .then(async (r: AxiosResponse<Blob>) => {
          const ct = String(r.headers["content-type"] || "");
          if (ct.includes("application/json")) {
            const txt = await r.data.text();
            try {
              const json = JSON.parse(txt) as ApiResponse;
              const failure = sessionError(json, path);
              if (failure) return reject(failure);
              reject(new Error(json.msg || "下载失败"));
            } catch {
              reject(new Error("下载失败"));
            }
            return;
          }
          resolve(r.data);
        })
        .catch(async (err: unknown) => {
          const failure = await axiosSessionError(err, path);
          if (failure) return reject(failure);
          reject(err instanceof Error ? err : new Error(axiosMessage(err)));
        });
    });
  },
  // 默认上传保持 60 秒；备份等限大小接口可覆盖期限，不能替代服务端上传大小校验。
  upload<T = unknown>(path = "", data: FormData, timeout = 60000): Promise<ApiResponse<T>> {
    return new Promise((resolve, reject) => {
      http
        .post(path, data, {
          baseURL: computeBaseURL(),
          timeout,
          headers: baseHeaders(),
        })
        .then((r: AxiosResponse<ApiResponse<T>>) => {
          const failure = sessionError(r.data, path);
          if (failure) return reject(failure);
          resolve(r.data);
        })
        .catch(async (err: unknown) => {
          const failure = await axiosSessionError(err, path);
          if (failure) return reject(failure);
          resolve({ code: -1, msg: axiosMessage(err), data: null as T });
        });
    });
  },
};

export default Network;
