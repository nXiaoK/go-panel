import { apiPost, type RequestOptions } from "@/lib/api/client";
import type { SpeedLimit, SpeedLimitWrite } from "@/lib/types";
export const createSpeedLimit = (
  data: SpeedLimitWrite,
  opts?: RequestOptions,
): Promise<string | null> => apiPost("/speed-limit/create", data, opts);
export const updateSpeedLimit = (data: SpeedLimitWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/speed-limit/update", data, opts);
export const getSpeedLimitList = async (opts?: RequestOptions): Promise<SpeedLimit[]> => {
  const data = await apiPost<SpeedLimit[] | { records: SpeedLimit[] }>(
    "/speed-limit/list",
    {},
    opts,
  );
  return Array.isArray(data) ? data : (data?.records ?? []);
};
export const deleteSpeedLimit = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/speed-limit/delete", { id }, opts);
