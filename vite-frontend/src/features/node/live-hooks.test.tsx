import { act, cleanup, render, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiPost } from "@/lib/api/client";
import { clearSession, setSession } from "@/lib/session";
import { nodeLiveStore } from "./live-store";
import { useNodeLive } from "./use-node-live";
import { useNodeSocket } from "./use-node-socket";
import { useNodes } from "./queries";
vi.mock("@/lib/api/client", () => ({ apiPost: vi.fn() }));
let client: QueryClient;
class Socket {
  static instances: Socket[] = [];
  readyState = 1;
  onopen: ((event: unknown) => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onerror = null;
  onclose: ((event: unknown) => void) | null = null;
  close = vi.fn();
  constructor(
    public url: string,
    public protocols: string[],
  ) {
    Socket.instances.push(this);
  }
  receive(message: unknown) {
    this.onmessage?.({ data: JSON.stringify(message) });
  }
}
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  localStorage.clear();
  Socket.instances = [];
  nodeLiveStore.clear();
  vi.stubGlobal("WebSocket", Socket);
  vi.mocked(apiPost).mockResolvedValue([{ id: 1, name: "节点", status: 1 }]);
  setSession({ token: "token", roleId: 0, name: "管理员", mustChangePassword: false });
});
afterEach(() => {
  cleanup();
  clearSession();
  client.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function Wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
describe("实时节点订阅贯通", () => {
  it("单节点指标不重渲染其他节点，连接状态快照只在状态变化时更新", () => {
    const renders = [0, 0];
    function Row({ id }: { id: number }) {
      const live = useNodeLive(id);
      renders[id - 1]++;
      return <output>{live?.info?.cpuUsage ?? "—"}</output>;
    }
    render(
      <>
        <Row id={1} />
        <Row id={2} />
      </>,
    );
    act(() =>
      nodeLiveStore.receive({
        id: "1",
        type: "info",
        data: JSON.stringify({ uptime: 1, cpu_usage: 30 }),
      }),
    );
    expect(renders).toEqual([2, 1]);
    const connections = nodeLiveStore.getConnectionsSnapshot();
    act(() =>
      nodeLiveStore.receive({
        id: "1",
        type: "info",
        data: JSON.stringify({ uptime: 2, cpu_usage: 31 }),
      }),
    );
    expect(renders).toEqual([3, 1]);
    expect(nodeLiveStore.getConnectionsSnapshot()).toBe(connections);
  });
  it("两页面共享单socket，真实字符串ID/JSON指标有效，单次上线仅发起一次REST刷新", async () => {
    const invalidation = vi.spyOn(client, "invalidateQueries");
    const hook = renderHook(
      () => {
        useNodeSocket();
        useNodeSocket();
        return useNodes();
      },
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(hook.result.current.data).toHaveLength(1));
    expect(Socket.instances).toHaveLength(1);
    expect(Socket.instances[0].url).toContain("/system-info?type=0");
    expect(Socket.instances[0].protocols).toEqual(["jwt.token"]);
    act(() =>
      Socket.instances[0].receive({
        id: "1",
        type: "info",
        data: JSON.stringify({
          uptime: 1,
          bytes_transmitted: 100,
          bytes_received: 200,
          cpu_usage: 30,
        }),
      }),
    );
    const before = nodeLiveStore.getSnapshot().get(1)?.info;
    act(() => Socket.instances[0].receive({ id: "1", type: "status", data: 1 }));
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2));
    expect(invalidation).toHaveBeenCalledTimes(1);
    expect(nodeLiveStore.getSnapshot().get(1)?.info).toBe(before);
    act(() =>
      Socket.instances[0].receive({
        id: "1",
        type: "info",
        data: JSON.stringify({ uptime: 3, bytes_transmitted: 300, bytes_received: 600 }),
      }),
    );
    expect(nodeLiveStore.getSnapshot().get(1)?.info).toMatchObject({
      uploadSpeed: 100,
      downloadSpeed: 200,
    });
    act(() => Socket.instances[0].receive({ id: "1", type: "status", data: 0 }));
    expect(nodeLiveStore.getSnapshot().get(1)).toMatchObject({ connection: "offline", info: null });
    act(() => clearSession());
    expect(Socket.instances[0].close).toHaveBeenCalledOnce();
    expect(nodeLiveStore.getSnapshot().size).toBe(0);
  });
  it("账户切换和强制改密清理旧实时信息且阻止普通用户连接", () => {
    renderHook(() => useNodeSocket(), { wrapper: Wrapper });
    act(() => Socket.instances[0].receive({ id: "1", type: "status", data: 1 }));
    act(() => setSession({ token: "viewer", roleId: 1, name: "用户", mustChangePassword: false }));
    expect(Socket.instances[0].close).toHaveBeenCalledOnce();
    expect(nodeLiveStore.getSnapshot().size).toBe(0);
    expect(Socket.instances).toHaveLength(1);
    act(() => setSession({ token: "forced", roleId: 0, name: "管理员", mustChangePassword: true }));
    expect(Socket.instances).toHaveLength(1);
  });
  it("离开节点页后丢失的离线广播不会让重新挂载使用旧在线状态或指标", async () => {
    const wrapper = { wrapper: Wrapper };
    const first = renderHook(() => {
      useNodeSocket();
      return { nodes: useNodes(), live: useNodeLive(1) };
    }, wrapper);
    await waitFor(() => expect(first.result.current.nodes.data).toHaveLength(1));
    act(() =>
      Socket.instances[0].receive({
        id: "1",
        type: "info",
        data: JSON.stringify({
          uptime: 10,
          cpu_usage: 38,
          memory_usage: 60,
          bytes_transmitted: 1000,
          bytes_received: 2000,
        }),
      }),
    );
    expect(first.result.current.live?.connection).toBe("online");
    first.unmount();
    expect(Socket.instances[0].close).toHaveBeenCalledOnce();
    expect(nodeLiveStore.getSnapshot().size).toBe(0);
    client.setQueryData(["node", "list", "raw"], [{ id: 1, name: "节点", status: 0 }]);
    const second = renderHook(() => {
      useNodeSocket();
      return { nodes: useNodes(), live: useNodeLive(1) };
    }, wrapper);
    expect(Socket.instances).toHaveLength(2);
    expect(second.result.current.live).toBeUndefined();
    expect(second.result.current.nodes.data?.[0].status).toBe(0);
    expect(second.result.current.live?.info).toBeUndefined();
  });
  it("当前socket意外断线撤销在线权威、保留退避，旧close回调不清新连接", async () => {
    const hook = renderHook(
      () => {
        useNodeSocket();
        return { nodes: useNodes(), live: useNodeLive(1) };
      },
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(hook.result.current.nodes.data).toHaveLength(1));
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    const oldClose = Socket.instances[0].onclose;
    act(() => Socket.instances[0].receive({ id: "1", type: "status", data: 1 }));
    await act(async () => {
      await Promise.resolve();
    });
    act(() => client.setQueryData(["node", "list", "raw"], [{ id: 1, name: "节点", status: 0 }]));
    act(() => oldClose?.({}));
    expect(hook.result.current.live).toBeUndefined();
    expect(hook.result.current.nodes.data?.[0].status).toBe(0);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(Socket.instances).toHaveLength(2);
    act(() =>
      Socket.instances[1].receive({
        id: "1",
        type: "info",
        data: JSON.stringify({
          uptime: 12,
          cpu_usage: 40,
          memory_usage: 60,
          bytes_transmitted: 1200,
          bytes_received: 2400,
        }),
      }),
    );
    expect(hook.result.current.live?.info?.cpuUsage).toBe(40);
    act(() => oldClose?.({}));
    expect(hook.result.current.live?.info?.cpuUsage).toBe(40);
    vi.useRealTimers();
  });
});
