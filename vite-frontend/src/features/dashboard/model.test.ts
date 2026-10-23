import { describe, expect, it } from "vitest";
import type { ForwardTunnelOption, Node, PackagePermission, User } from "@/lib/types";
import { buildAttentionItems, mergeMyTunnels, sortDashboardNodes } from "./model";
const now = Date.UTC(2026, 9, 23);
const node = (id: number, extra: Partial<Node> = {}): Node => ({
  id,
  name: `node-${id}`,
  status: 1,
  ...extra,
});
const user = (id: number, extra: Partial<User> = {}): User => ({
  id,
  user: `user-${id}`,
  status: 1,
  flow: 100,
  num: 10,
  roleId: 1,
  ...extra,
});

describe("真实关注信号", () => {
  it("派生全部六类信号，优先离线/同步/转发/到期/配额/升级", () => {
    const items = buildAttentionItems({
      now,
      nodes: [
        node(2, { syncState: "failed" }),
        node(1, { status: 0, upgradeAvailable: true }),
        node(3, { upgradeAvailable: true }),
      ],
      forwards: [
        { id: 4, name: "game", status: -1 },
        { id: 5, name: "paused", status: 0 },
      ],
      users: [user(7, { expTime: now + 3 * 86400000 }), user(6, { inFlow: 92 * 1024 ** 3 })],
    });
    expect(items.map((item) => item.kind)).toEqual([
      "offline",
      "sync",
      "forward",
      "expiry",
      "quota",
      "upgrade",
    ]);
    expect(items.map((item) => item.label)).toEqual([
      "node-1 离线",
      "node-2 同步失败",
      "转发 game 异常",
      "用户 user-7 3 天后到期",
      "用户 user-6 流量已用 92%",
      "2 个节点可升级",
    ]);
    expect(items.map((item) => item.href)).toEqual([
      "/node?focus=1",
      "/node?focus=2",
      "/forward?focus=4",
      "/user?focus=7",
      "/user?focus=6",
      "/node?focus=1",
    ]);
  });
  it("边界包含恰好7天和90%以及已到期；8天/89.9%不加入", () => {
    const items = buildAttentionItems({
      now,
      users: [
        user(1, { expTime: now + 7 * 86400000 }),
        user(2, { expTime: now + 8 * 86400000 }),
        user(3, { expTime: now - 1 }),
        user(4, { inFlow: 90 * 1024 ** 3 }),
        user(5, { inFlow: 89.9 * 1024 ** 3 }),
      ],
    });
    expect(items.map((item) => item.id)).toEqual(["expiry-3", "expiry-1", "quota-4"]);
    expect(items[0].label).toContain("已到期");
  });
  it("0GB普通用户已用尽；管理员配额不参与；离线上线覆盖REST并排序稳定", () => {
    const connections = new Map([
      [1, "online"],
      [4, "offline"],
    ]);
    const nodes = [
      node(1, { status: 0 }),
      node(2, { upgradeAvailable: true }),
      node(3, { syncState: "failed" }),
      node(4),
    ];
    expect(sortDashboardNodes(nodes, connections).map((item) => item.id)).toEqual([4, 3, 2, 1]);
    const items = buildAttentionItems({
      nodes,
      connections,
      users: [user(1, { flow: 0 }), user(2, { flow: 0, roleId: 0 })],
    });
    expect(items.filter((item) => item.kind === "offline").map((item) => item.id)).toEqual([
      "offline-4",
    ]);
    expect(items.filter((item) => item.kind === "quota").map((item) => item.label)).toEqual([
      "用户 user-1 流量已用尽",
    ]);
    expect(nodes.map((item) => item.id)).toEqual([1, 2, 3, 4]);
  });
  it("全部正常或没有数据返回空数组", () => {
    expect(buildAttentionItems({})).toEqual([]);
    expect(
      buildAttentionItems({
        nodes: [node(1)],
        forwards: [{ id: 1, name: "ok", status: 1 }],
        users: [user(1)],
      }),
    ).toEqual([]);
  });
});
describe("我的隧道合并", () => {
  it("按tunnelId合并IP、端口与额度，不丢失无选项的失效授权", () => {
    const permissions = [
      { id: 1, tunnelId: 10, tunnelName: "旧名", flow: 0 },
      { id: 2, tunnelId: 20, tunnelName: "已过期", flow: 10 },
    ] as PackagePermission[];
    const options = [
      {
        id: 10,
        name: "新名",
        inIp: "1.2.3.4",
        type: 2,
        inNodePortSta: 1000,
        inNodePortEnd: 2000,
        available: false,
        unavailableReason: "流量已用尽",
      },
      {
        id: 30,
        name: "安全选项",
        inIp: "entry.example",
        type: 1,
        available: true,
        inNodePortSta: null,
        inNodePortEnd: null,
      },
    ] as ForwardTunnelOption[];
    const rows = mergeMyTunnels(permissions, options);
    expect(rows.map((row) => row.id)).toEqual([10, 20, 30]);
    expect(rows[0]).toMatchObject({
      name: "新名",
      inIp: "1.2.3.4",
      ports: "1000–2000",
      permission: permissions[0],
      unavailableReason: "流量已用尽",
    });
    expect(rows[1]).toMatchObject({
      name: "已过期",
      inIp: "—",
      ports: "—",
      unavailableReason: "授权不可用",
    });
    expect(rows[2].unavailableReason).toBeUndefined();
  });
});

it("到期信号沿用后端非null<=now语义，0和负值也过期而null无期限", () => {
  const items = buildAttentionItems({
    now,
    users: [user(1, { expTime: 0 }), user(2, { expTime: -1 }), user(3, { expTime: null }), user(4)],
  });
  expect(items.map((item) => item.id)).toEqual(["expiry-2", "expiry-1"]);
  expect(items.every((item) => item.label.endsWith("已到期"))).toBe(true);
});
