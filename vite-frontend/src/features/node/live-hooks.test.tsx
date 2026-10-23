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
  onclose = null;
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
});
