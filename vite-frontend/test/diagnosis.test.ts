import { describe, expect, it } from "vitest";
import { diagnosisStatus, diagnosisSummary, internetProbeNotice } from "../src/lib/diagnosis";
import type { DiagnosisResult } from "../src/lib/types";

const row = (success: boolean, advisory = false): DiagnosisResult["results"][number] => ({
  nodeId: 1,
  nodeName: "test-node",
  targetIp: "probe.example",
  description: "probe",
  success,
  advisory,
});

describe("diagnosis conclusions", () => {
  it("shows advisory failures as warnings and excludes them from the link conclusion", () => {
    expect(diagnosisStatus(row(false, true))).toBe("警告");
    expect(diagnosisStatus(row(false))).toBe("失败");
    expect(diagnosisStatus(row(true, true))).toBe("通过");
    expect(diagnosisSummary([row(true), row(false, true)])).toBe("链路诊断通过");
    expect(diagnosisSummary([row(false), row(true, true)])).toBe("链路诊断失败：1 项未通过");
    expect(internetProbeNotice).toBe("外网探测仅供参考，受节点所在网络环境影响");
  });
  it("does not call an advisory-only diagnosis a successful link check", () => {
    expect(diagnosisSummary([row(false, true)])).toBe("无链路检查项，外网探测仅供参考");
    expect(diagnosisSummary([])).toBe("无链路检查项，外网探测仅供参考");
  });
});
