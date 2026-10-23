import axios, { type AxiosRequestConfig, type AxiosResponse } from "axios";
import type { ApiResponse } from "@/lib/types";
import { currentPanelEndpoint } from "@/lib/panel-endpoint";
import { invalidateSession, requirePasswordChange } from "@/lib/session";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: number,
    readonly httpStatus?: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface RequestOptions {
  // 默认请求期限为 30 秒；仅诊断、测速、备份等已知长请求显式覆盖，单位为毫秒。
  timeoutMs?: number;
  // 公开接口失败时不撤销已有会话；登录接口默认公开，其余接口默认保护。
  public?: boolean;
  // 查询卸载或查询键改变时由 React Query 中止对应请求。
  signal?: AbortSignal;
}

declare module "axios" {
  interface AxiosRequestConfig {
    // 仅供会话分类，不发往服务端；公开请求失败不得影响已有登录状态。
    panelPublic?: boolean;
  }
}

export function computeBaseURL(): string {
  return currentPanelEndpoint().apiBase;
}

export const PANEL_ADDRESS_CHANGED_EVENT = "panel-address-changed";
// 地址切换会把令牌发送到指定服务器；仅保存用户明确填写的值，并通知缓存清空旧站点数据。
export function setPanelAddress(address: string) {
  if (typeof window === "undefined") return;
  const next = address.trim();
  const previous = window.localStorage.getItem("panel_address") || "";
  if (next) window.localStorage.setItem("panel_address", next);
  else window.localStorage.removeItem("panel_address");
  if (next !== previous) window.dispatchEvent(new Event(PANEL_ADDRESS_CHANGED_EVENT));
}
export function getPanelAddress(): string {
  return typeof window === "undefined" ? "" : window.localStorage.getItem("panel_address") || "";
}

function isPublic(config?: AxiosRequestConfig): boolean {
  return (
    config?.panelPublic ??
    config?.url?.split("?")[0].replace(/^\//, "").replace(/\/$/, "") === "user/login"
  );
}
function sessionFailure(
  body: Partial<ApiResponse> | undefined,
  config?: AxiosRequestConfig,
  status?: number,
) {
  if (isPublic(config)) return;
  // HTTP 401 优先于异常响应体中的 4031/4032；任何受保护 401 均撤销令牌。
  if (status === 401 || body?.code === 401) invalidateSession("expired");
  else if (body?.code === 4031 || (body?.code === 403 && body.msg === "账号已被禁用"))
    invalidateSession("disabled");
  else if (body?.code === 4032 || (body?.code === 403 && body.msg === "请先修改默认密码"))
    requirePasswordChange();
}
async function errorBody(data: unknown): Promise<Partial<ApiResponse> | undefined> {
  if (data && typeof data === "object" && "text" in data && typeof data.text === "function") {
    try {
      return await errorBody(JSON.parse(await data.text()));
    } catch {
      return undefined;
    }
  }
  if (data && typeof data === "object") return data as Partial<ApiResponse>;
  return undefined;
}
function statusMessage(status?: number) {
  if (status === 401) return "登录已失效，请重新登录";
  if (status === 403) return "无权执行此操作";
  if (status === 413) return "上传内容过大";
  if (status === 503) return "系统维护中，请稍后重试";
  return status ? `请求失败（HTTP ${status}）` : "无法连接面板，请检查网络";
}

// 全站只使用一个实例。端点与令牌逐次读取，避免地址或会话切换后发送旧鉴权信息。
const http = axios.create();
http.interceptors.request.use((config) => {
  config.baseURL = computeBaseURL();
  config.headers.set(
    "Authorization",
    typeof window === "undefined" ? "" : window.localStorage.getItem("token") || "",
  );
  return config;
});
http.interceptors.response.use(
  async (response) => {
    const download = response.config.responseType === "blob";
    const json =
      String(response.headers["content-type"] || "").includes("json") ||
      (response.data instanceof Blob && response.data.type.includes("json"));
    if (download && !json) return response;
    const body = await errorBody(response.data);
    sessionFailure(body, response.config, response.status);
    if (body?.code !== 0 || download)
      throw new ApiError(
        body?.msg || (download ? "下载失败" : "响应格式错误"),
        body?.code ?? -1,
        response.status,
      );
    return response;
  },
  async (error: unknown) => {
    if (error instanceof ApiError) throw error;
    if (!axios.isAxiosError(error))
      throw new ApiError(error instanceof Error ? error.message : "无法连接面板，请检查网络", -1);
    const status = error.response?.status;
    const body = await errorBody(error.response?.data);
    sessionFailure(body, error.config, status);
    if (error.code === "ECONNABORTED" || error.code === "ETIMEDOUT")
      throw new ApiError("请求超时，请检查网络或稍后重试", -2, status);
    if (axios.isCancel(error)) throw new ApiError("请求已取消", -1, status);
    throw new ApiError(
      body?.msg || statusMessage(status),
      status === 401 ? 401 : (body?.code ?? -1),
      status,
    );
  },
);

function config(opts: RequestOptions = {}): AxiosRequestConfig {
  return { timeout: opts.timeoutMs ?? 30_000, panelPublic: opts.public, signal: opts.signal };
}
export async function apiPost<T>(
  path: string,
  body: unknown = {},
  opts?: RequestOptions,
): Promise<T> {
  const response = await http.post<ApiResponse<T>>(path, body, config(opts));
  return response.data.data;
}
export async function apiGet<T>(
  path: string,
  params: Record<string, unknown> = {},
  opts?: RequestOptions,
): Promise<T> {
  const response = await http.get<ApiResponse<T>>(path, { ...config(opts), params });
  return response.data.data;
}
export async function apiUpload<T>(
  path: string,
  form: FormData,
  opts?: RequestOptions,
): Promise<T> {
  // 通用上传沿用 60 秒，备份显式覆盖为 10 分钟；浏览器自动补 multipart boundary。
  const response = await http.post<ApiResponse<T>>(
    path,
    form,
    config({ timeoutMs: 60_000, ...opts }),
  );
  return response.data.data;
}
function downloadFilename(disposition: string): string | undefined {
  const encoded = disposition.match(/filename\*\s*=\s*UTF-8''([^;]+)/i);
  const ordinary = disposition.match(/filename\s*=\s*(?:"([^"]*)"|([^;]*))/i);
  let name = ordinary?.[1] ?? ordinary?.[2]?.trim();
  if (encoded) {
    try {
      name = decodeURIComponent(encoded[1].trim());
    } catch {
      /* 非法编码回退到普通文件名。 */
    }
  }
  return (
    name
      ?.split(/[\\/]/)
      .pop()
      ?.split("")
      .filter((character) => character.charCodeAt(0) >= 32 && character.charCodeAt(0) !== 127)
      .join("") || undefined
  );
}
export async function apiDownload(
  path: string,
  opts?: RequestOptions,
): Promise<{ blob: Blob; filename?: string }> {
  const response: AxiosResponse<Blob> = await http.get(path, {
    ...config(opts),
    responseType: "blob",
  });
  return {
    blob: response.data,
    filename: downloadFilename(String(response.headers["content-disposition"] || "")),
  };
}
export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "请求失败，请稍后重试";
}
