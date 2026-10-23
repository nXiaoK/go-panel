import type { DiagnosisResult } from "./types";

type DiagnosisItem = DiagnosisResult["results"][number];

export const internetProbeNotice = "外网探测仅供参考，受节点所在网络环境影响";

export function diagnosisStatus(item: DiagnosisItem): "通过" | "警告" | "失败" {
  return item.success ? "通过" : item.advisory ? "警告" : "失败";
}

export function diagnosisSummary(results: DiagnosisItem[]): string {
  const checks = results.filter((item) => !item.advisory);
  if (checks.length === 0) return "无链路检查项，外网探测仅供参考";
  const failed = checks.filter((item) => !item.success).length;
  return failed ? `链路诊断失败：${failed} 项未通过` : "链路诊断通过";
}
