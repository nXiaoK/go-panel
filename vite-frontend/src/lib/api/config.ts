import { apiPost, type RequestOptions } from "./client";
export const getConfigs = (opts?: RequestOptions): Promise<Record<string, string>> =>
  apiPost("/config/list", {}, opts);
export const getConfigByName = (
  name: string,
  opts?: RequestOptions,
): Promise<{ id: number; name: string; value: string; time: number }> =>
  apiPost("/config/get", { name }, { ...opts, public: true });
export const updateConfigs = (
  data: Record<string, string>,
  opts?: RequestOptions,
): Promise<string> => apiPost("/config/update", data, opts);

export const deleteConfig = (name: string, opts?: RequestOptions): Promise<string> =>
  apiPost("/config/delete", { name }, opts);
