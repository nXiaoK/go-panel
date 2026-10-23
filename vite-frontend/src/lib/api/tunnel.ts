import { apiPost, type RequestOptions } from "./client";
import type {
  Tunnel,
  TunnelWrite,
  TunnelListItem,
  UserTunnel,
  UserTunnelAssign,
  UserTunnelWrite,
  DiagnosisResult,
  SpeedTestRequest,
  SpeedTestResult,
} from "@/lib/types";
export const createTunnel = (data: TunnelWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/tunnel/create", data, opts);
export const updateTunnel = (data: TunnelWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/tunnel/update", data, opts);
export const getTunnelList = async (opts?: RequestOptions): Promise<Tunnel[]> => {
  const data = await apiPost<Tunnel[] | { records: Tunnel[] }>("/tunnel/list", {}, opts);
  return Array.isArray(data) ? data : (data?.records ?? []);
};
export const deleteTunnel = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/tunnel/delete", { id }, opts);
// 诊断后端写期限为 90 秒，额外预留 5 秒网络与解析时间。
export const diagnoseTunnel = (tunnelId: number, opts?: RequestOptions): Promise<DiagnosisResult> =>
  apiPost("/tunnel/diagnose", { tunnelId }, { ...opts, timeoutMs: 95_000 });
// 与后端归一化一致：默认 10 秒、上限 120 秒，额外预留 50 秒，晚于服务端 45 秒余量。
export const speedTestTunnel = (
  data: SpeedTestRequest,
  opts?: RequestOptions,
): Promise<SpeedTestResult> => {
  const seconds = data.durationSeconds;
  const duration =
    seconds === undefined || !Number.isFinite(seconds) || seconds <= 0
      ? 10
      : Math.min(seconds, 120);
  return apiPost("/tunnel/speed-test", data, { ...opts, timeoutMs: (duration + 50) * 1000 });
};
export const assignUserTunnel = (data: UserTunnelAssign, opts?: RequestOptions): Promise<string> =>
  apiPost("/tunnel/user/assign", data, opts);
export const getUserTunnelList = async (
  data: { userId: number },
  opts?: RequestOptions,
): Promise<UserTunnel[]> => {
  const rows = await apiPost<UserTunnel[] | { records: UserTunnel[] }>(
    "/tunnel/user/list",
    data,
    opts,
  );
  return Array.isArray(rows) ? rows : (rows?.records ?? []);
};
export const removeUserTunnel = (data: { id: number }, opts?: RequestOptions): Promise<string> =>
  apiPost("/tunnel/user/remove", data, opts);
export const updateUserTunnel = (data: UserTunnelWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/tunnel/user/update", data, opts);
export const userTunnel = (opts?: RequestOptions): Promise<TunnelListItem[]> =>
  apiPost("/tunnel/user/tunnel", {}, opts);
