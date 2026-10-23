import { describe, expect, it } from "vitest";
import { createNodeLiveStore } from "./live-store";
const metrics = (uptime: number, upload = 100, download = 200) => ({
  id: 1,
  type: "info",
  data: {
    uptime,
    bytes_transmitted: upload,
    bytes_received: download,
    cpu_usage: 38,
    memory_usage: 62,
  },
});
describe("独立节点实时指标", () => {
  it("状态推送保留指标，离线清除指标和速率基准", () => {
    const store = createNodeLiveStore();
    store.receive(metrics(1));
    store.receive(metrics(3, 300, 600));
    const info = store.getSnapshot().get(1)?.info;
    expect(info).toMatchObject({ uploadSpeed: 100, downloadSpeed: 200 });
    store.receive({ id: 1, type: "status", data: 1 });
    expect(store.getSnapshot().get(1)?.info).toBe(info);
    store.receive({ id: 1, type: "status", data: 0 });
    expect(store.getSnapshot().get(1)).toMatchObject({ connection: "offline", info: null });
    store.receive(metrics(4, 400, 800));
    expect(store.getSnapshot().get(1)?.info?.uploadSpeed).toBe(0);
  });
  it.each([0, 11, -1])("时间差%s不产生速率", (delta) => {
    const store = createNodeLiveStore();
    store.receive(metrics(10));
    store.receive(metrics(10 + delta, 300, 600));
    expect(store.getSnapshot().get(1)?.info).toMatchObject({ uploadSpeed: 0, downloadSpeed: 0 });
  });
  it("10秒仍可计算，计数回绕各自清零，下一帧使用新基准", () => {
    const store = createNodeLiveStore();
    store.receive(metrics(0, 1000, 2000));
    store.receive(metrics(10, 900, 3000));
    expect(store.getSnapshot().get(1)?.info).toMatchObject({ uploadSpeed: 0, downloadSpeed: 100 });
    store.receive(metrics(11, 1000, 10));
    expect(store.getSnapshot().get(1)?.info).toMatchObject({ uploadSpeed: 100, downloadSpeed: 0 });
    store.receive(metrics(12, 1100, 210));
    expect(store.getSnapshot().get(1)?.info).toMatchObject({
      uploadSpeed: 100,
      downloadSpeed: 200,
    });
  });
  it("无效帧不通知、不损坏快照；清理通知订阅者", () => {
    const store = createNodeLiveStore();
    let count = 0;
    const release = store.subscribe(() => count++);
    store.receive(metrics(1));
    const before = store.getSnapshot();
    for (const data of ["{", null, []]) store.receive({ id: 1, type: "info", data });
    store.receive({ id: -1, type: "status", data: 1 });
    expect(store.getSnapshot()).toBe(before);
    expect(count).toBe(1);
    store.clear();
    expect(store.getSnapshot().size).toBe(0);
    expect(count).toBe(2);
    release();
    store.receive(metrics(2));
    expect(count).toBe(2);
  });
});
