import { act, cleanup, render } from "@testing-library/react";
import {
  focusManager,
  onlineManager,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import type { ComponentType } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Network from "@/lib/api/network";
import { setPreference } from "@/lib/preferences";
import { Route } from "./_app.node";

vi.mock("@/lib/api/network", () => ({ default: { post: vi.fn() } }));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useNavigate: () => vi.fn(),
}));

const post = vi.mocked(Network.post);
const NodePage = Route.options.component as ComponentType;
let client: QueryClient;

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

async function renderPage() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <NodePage />
    </QueryClientProvider>,
  );
  await advance(0);
}

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  post.mockResolvedValue({ code: 0, msg: "", data: [] });
  focusManager.setFocused(true);
  onlineManager.setOnline(true);
  // 节点页面同时建立状态流，本组只验证真实页面的 react-query 刷新生命周期。
  vi.stubGlobal(
    "WebSocket",
    class {
      static OPEN = 1;
      static CONNECTING = 0;
      readyState = 1;
      close() {}
    },
  );
});

afterEach(() => {
  cleanup();
  client?.clear();
  focusManager.setFocused(undefined);
  onlineManager.setOnline(true);
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("节点页面的自动刷新", () => {
  it("初次加载后每 30 秒刷新真实节点列表", async () => {
    await renderPage();
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenLastCalledWith("/node/list");
    await advance(30_000);
    expect(post).toHaveBeenCalledTimes(2);
    await advance(30_000);
    expect(post).toHaveBeenCalledTimes(3);
  });

  it("慢请求未结束时不会叠加轮询", async () => {
    await renderPage();
    let resolve!: (value: { code: number; msg: string; data: never[] }) => void;
    post.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    await advance(90_000);
    expect(post).toHaveBeenCalledTimes(2);
    await act(async () => resolve({ code: 0, msg: "", data: [] }));
    await advance(30_000);
    expect(post).toHaveBeenCalledTimes(3);
  });

  it("页面隐藏时暂停轮询，重新可见时立即补刷新", async () => {
    await renderPage();
    focusManager.setFocused(false);
    await advance(90_000);
    expect(post).toHaveBeenCalledTimes(1);
    await act(async () => focusManager.setFocused(true));
    await advance(0);
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("关闭偏好后停止轮询，重新启用后恢复，卸载后清除轮询", async () => {
    setPreference("autoRefresh", false);
    await renderPage();
    await advance(90_000);
    expect(post).toHaveBeenCalledTimes(1);
    act(() => setPreference("autoRefresh", true));
    await advance(30_000);
    expect(post).toHaveBeenCalledTimes(2);
    cleanup();
    await advance(90_000);
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("离线时不发送轮询请求，网络恢复后继续加载", async () => {
    await renderPage();
    onlineManager.setOnline(false);
    await advance(90_000);
    expect(post).toHaveBeenCalledTimes(1);
    await act(async () => onlineManager.setOnline(true));
    await advance(0);
    expect(post).toHaveBeenCalledTimes(2);
  });
});
