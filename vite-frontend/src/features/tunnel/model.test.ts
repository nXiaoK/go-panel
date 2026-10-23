import { describe, expect, it } from "vitest";
import type { Node } from "@/lib/types";
import {
  canSpeedTest,
  emptyTunnelForm,
  nftModeFor,
  nodeDisabledReason,
  speedDirections,
  validateSpeedInputs,
  validateTunnelForm,
} from "./model";
const nodes = new Map<number, Node>([
  [1, { id: 1, name: "入口", status: 1, forwardMode: "nftables" }],
  [2, { id: 2, name: "中继", status: 0, forwardMode: "nftables" }],
  [3, { id: 3, name: "出口", status: 1, forwardMode: "nftables" }],
  [4, { id: 4, name: "GOST", status: 1, forwardMode: "gost" }],
]);
describe("隧道字段校验", () => {
  it("空名称、入口和隧道出口分别关联字段错误", () => {
    expect(validateTunnelForm({ ...emptyTunnelForm(), type: 2 }, nodes)).toEqual({
      name: "请输入隧道名称",
      inNodeId: "请选择入口节点",
      outNodeId: "隧道转发需要选择出口节点",
    });
    expect(validateTunnelForm({ ...emptyTunnelForm(), name: "端口", inNodeId: 1 }, nodes)).toEqual(
      {},
    );
  });
  it("拒绝入口出口重复与中继重复", () => {
    const form = { ...emptyTunnelForm(), name: "链路", type: 2, inNodeId: 1, outNodeId: 1 };
    expect(validateTunnelForm(form, nodes).outNodeId).toContain("不能重复");
    expect(
      validateTunnelForm({ ...form, outNodeId: 3, relayNodeId: 1 }, nodes).relayNodeId,
    ).toContain("不能重复");
    expect(
      validateTunnelForm({ ...form, outNodeId: 3, relayNodeId: 3 }, nodes).relayNodeId,
    ).toContain("不能重复");
  });
  it("三节点所有位置都必须使用 NFT；离线状态由服务端创建校验", () => {
    const form = {
      ...emptyTunnelForm(),
      name: "链路",
      type: 2,
      inNodeId: 1,
      outNodeId: 3,
      relayNodeId: 2,
    };
    expect(validateTunnelForm(form, nodes)).toEqual({});
    for (const role of ["inNodeId", "outNodeId", "relayNodeId"] as const)
      expect(validateTunnelForm({ ...form, [role]: 4 }, nodes).relayNodeId).toContain(
        "均为 nftables",
      );
    expect(nftModeFor(form, nodes)).toBe(true);
    expect(nftModeFor({ ...form, outNodeId: 4 }, nodes)).toBe(false);
  });
  it("选择器禁用重复节点与非 NFT 中继，不禁止离线 NFT", () => {
    const form = { ...emptyTunnelForm(), type: 2, inNodeId: 1, outNodeId: 3 };
    expect(nodeDisabledReason(nodes.get(1)!, "outNodeId", form)).toContain("重复");
    expect(nodeDisabledReason(nodes.get(4)!, "relayNodeId", form)).toContain("仅支持 nftables");
    expect(nodeDisabledReason(nodes.get(2)!, "relayNodeId", form)).toBeUndefined();
  });
});
describe("测速参数与入口", () => {
  it("保留空输入但在提交时拒绝；范围必须为整数", () => {
    expect(validateSpeedInputs({ duration: "", parallel: "", port: "" })).toEqual({
      duration: "请输入 1–120 秒的整数时长",
      parallel: "请输入 1–32 个并发连接",
    });
    for (const duration of ["0", "121", "1.5", "NaN"])
      expect(validateSpeedInputs({ duration, parallel: "1", port: "" }).duration).toBeTruthy();
    for (const parallel of ["0", "33", "1.5"])
      expect(validateSpeedInputs({ duration: "10", parallel, port: "" }).parallel).toBeTruthy();
    expect(validateSpeedInputs({ duration: "120", parallel: "32", port: "65535" })).toEqual({});
    expect(validateSpeedInputs({ duration: "1", parallel: "1", port: "65536" }).port).toBeTruthy();
  });
  it("两节点两方向、三节点四方向，无隧道时安全不可测速", () => {
    expect(speedDirections(false).map((item) => item.value)).toEqual(["in-to-out", "out-to-in"]);
    expect(speedDirections(true).map((item) => item.value)).toEqual([
      "in-to-relay",
      "relay-to-out",
      "out-to-relay",
      "relay-to-in",
    ]);
    expect(canSpeedTest(undefined)).toBe(false);
    expect(canSpeedTest(null)).toBe(false);
  });
});
