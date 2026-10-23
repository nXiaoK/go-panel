import { apiPost, diagnosisRequestTimeoutMs, type RequestOptions } from "@/lib/api/client";
import type {
  CompleteForwardRule,
  DiagnosisResult,
  Forward,
  ForwardWrite,
  NftCompletion,
  NftDetection,
  TunnelDetection,
} from "@/lib/types";
export const createForward = (data: ForwardWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/forward/create", data, opts);
export const updateForward = (data: ForwardWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/forward/update", data, opts);
export const getForwardList = async (opts?: RequestOptions): Promise<Forward[]> => {
  const data = await apiPost<Forward[] | { records: Forward[] }>("/forward/list", {}, opts);
  return Array.isArray(data) ? data : (data?.records ?? []);
};
export const deleteForward = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/forward/delete", { id }, opts);
export const forceDeleteForward = (id: number, opts?: RequestOptions): Promise<string | null> =>
  apiPost("/forward/force-delete", { id }, opts);
export const pauseForwardService = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/forward/pause", { id }, opts);
export const resumeForwardService = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/forward/resume", { id }, opts);
// 诊断后端写期限为 90 秒，额外预留 5 秒网络与解析时间。
export const diagnoseForward = (
  forwardId: number,
  opts?: RequestOptions,
): Promise<DiagnosisResult> =>
  apiPost("/forward/diagnose", { forwardId }, { ...opts, timeoutMs: diagnosisRequestTimeoutMs });
export const updateForwardOrder = (
  data: { forwards: Array<{ id: number; inx: number }> },
  opts?: RequestOptions,
): Promise<string> => apiPost("/forward/update-order", data, opts);
export const detectNftRules = (nodeId: number, opts?: RequestOptions): Promise<NftDetection> =>
  apiPost("/forward/detect-nft-rules", { nodeId }, opts);
export const detectTunnelRules = (
  inNodeId: number,
  outNodeId: number,
  opts?: RequestOptions,
): Promise<TunnelDetection> =>
  apiPost("/forward/detect-tunnel-rules", { inNodeId, outNodeId }, opts);
export const completeFromNft = (
  nodeId: number,
  rules: CompleteForwardRule[],
  opts?: RequestOptions,
): Promise<NftCompletion> => apiPost("/forward/complete-from-nft", { nodeId, rules }, opts);
