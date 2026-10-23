import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SpeedTestResult, Tunnel } from "@/lib/types";
import { SpeedTestSheet } from "./speed-test-sheet";
import { speedTestTunnel } from "../api";
vi.mock("../api", () => ({ speedTestTunnel: vi.fn() }));
vi.mock("@/lib/panel-ws", () => ({
  buildPanelWsUrl: () => "ws://127.0.0.1:6403/ws",
  createSpeedTestId: () => "speed-current",
}));
const tunnel: Tunnel = {
  id: 7,
  name: "三节点",
  type: 2,
  inNodeId: 1,
  relayNodeId: 2,
  outNodeId: 3,
};
class MockSocket {
  static instances: MockSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  close = vi.fn();
  constructor(
    public url: string,
    public protocols?: string[],
  ) {
    MockSocket.instances.push(this);
  }
}
beforeEach(() => {
  MockSocket.instances = [];
  vi.clearAllMocks();
  vi.stubGlobal("WebSocket", MockSocket);
  localStorage.setItem("token", "t");
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});
function result(): SpeedTestResult {
  return {
    tunnelId: 7,
    tunnelName: "三节点",
    testId: "speed-current",
    direction: "in-to-relay",
    sourceNodeId: 1,
    sourceNodeName: "入口",
    targetNodeId: 2,
    targetNodeName: "中继",
    targetHost: "127.0.0.1",
    port: 1234,
    durationSeconds: 10,
    parallel: 1,
    timestamp: 0,
    summary: {
      sentMbps: 12,
      receivedMbps: 11,
      sentBytes: 1,
      receivedBytes: 1,
      seconds: 10,
      retransmits: 2,
      latencyMs: 3,
      lossPercent: 1,
    },
  };
}
describe("绑定隧道的测速抽屉", () => {
  it("空隧道安全不渲染、不连接", () => {
    const view = render(<SpeedTestSheet onOpenChange={vi.fn()} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(MockSocket.instances).toHaveLength(0);
    view.rerender(<SpeedTestSheet tunnel={{ ...tunnel, type: 1 }} onOpenChange={vi.fn()} />);
    expect(MockSocket.instances).toHaveLength(0);
  });
  it("三节点四方向，时长清空提交显示字段错误且不发请求", async () => {
    render(<SpeedTestSheet tunnel={tunnel} onOpenChange={vi.fn()} />);
    const group = screen.getByRole("group", { name: "测试方向" });
    expect(group.querySelectorAll("button")).toHaveLength(4);
    fireEvent.change(screen.getByLabelText(/测试时长/), { target: { value: "" } });
    expect((screen.getByLabelText(/测试时长/) as HTMLInputElement).value).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "开始测速" }));
    expect(await screen.findByText("请输入 1–120 秒的整数时长")).toBeTruthy();
    expect(screen.getByLabelText(/测试时长/).getAttribute("aria-invalid")).toBe("true");
    expect(speedTestTunnel).not.toHaveBeenCalled();
  });
  it("方向传给API，隔离testId、仅保留最近六点，断线仍取得HTTP结果", async () => {
    let resolve!: (value: SpeedTestResult) => void;
    vi.mocked(speedTestTunnel).mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    render(<SpeedTestSheet tunnel={tunnel} onOpenChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "出口 → 中继" }));
    fireEvent.click(screen.getByRole("button", { name: "开始测速" }));
    expect(speedTestTunnel).toHaveBeenCalledWith(
      expect.objectContaining({
        tunnelId: 7,
        direction: "out-to-relay",
        durationSeconds: 10,
        parallel: 1,
        port: 0,
        testId: "speed-current",
      }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    const ws = MockSocket.instances[0];
    expect(ws.url).toBe("ws://127.0.0.1:6403/ws");
    expect(ws.protocols).toEqual(["jwt.t"]);
    act(() =>
      ws.onmessage?.({
        data: JSON.stringify({
          type: "speed-test-progress",
          data: { testId: "someone-else", mbps: 999 },
        }),
      }),
    );
    expect(screen.queryByRole("table", { name: "最近六个采样点" })).toBeNull();
    act(() => {
      for (let i = 1; i <= 8; i++)
        ws.onmessage?.({
          data: JSON.stringify({
            type: "speed-test-progress",
            data: { testId: "speed-current", mbps: i, startSeconds: i - 1, endSeconds: i },
          }),
        });
    });
    expect(
      screen.getByRole("table", { name: "最近六个采样点" }).querySelectorAll("tbody tr"),
    ).toHaveLength(6);
    expect(screen.queryByText("0–1")).toBeNull();
    act(() => ws.onclose?.());
    expect(screen.getByText("实时进度已断开，结果将在测试结束后显示")).toBeTruthy();
    await act(async () => resolve(result()));
    expect(await screen.findByText("测试完成")).toBeTruthy();
    expect(screen.getByLabelText("检测到丢包")).toBeTruthy();
    expect(screen.getByRole("button", { name: "再测一次" })).toBeTruthy();
  });
  it("关闭抽屉断开连接并取消本地结果订阅", async () => {
    vi.mocked(speedTestTunnel).mockImplementation(() => new Promise(() => {}));
    const close = vi.fn();
    const view = render(
      <SpeedTestSheet tunnel={{ ...tunnel, relayNodeId: undefined }} onOpenChange={close} />,
    );
    expect(screen.getByRole("group", { name: "测试方向" }).querySelectorAll("button")).toHaveLength(
      2,
    );
    fireEvent.click(screen.getByRole("button", { name: "开始测速" }));
    const signal = vi.mocked(speedTestTunnel).mock.calls[0][1]?.signal;
    const ws = MockSocket.instances[0];
    fireEvent.click(screen.getByRole("button", { name: "关闭测速" }));
    expect(close).toHaveBeenCalledWith(false);
    view.rerender(<SpeedTestSheet tunnel={null} onOpenChange={close} />);
    await waitFor(() => expect(ws.close).toHaveBeenCalledTimes(1));
    expect(ws.onmessage).toBeNull();
    expect(signal?.aborted).toBe(true);
  });
});
