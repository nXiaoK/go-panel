import { afterEach, describe, expect, it, vi } from "vitest";
import { createNodeSocketManager } from "./use-node-socket";
function setup() {
  const sockets: WebSocket[] = [];
  const connect = vi.fn(() => {
    const socket = {
      close: vi.fn(),
      readyState: 1,
      onopen: null,
      onmessage: null,
      onerror: null,
      onclose: null,
    } as unknown as WebSocket;
    sockets.push(socket);
    return socket;
  });
  const receive = vi.fn();
  const clear = vi.fn();
  const manager = createNodeSocketManager({ connect, receive, clear });
  return { manager, sockets, connect, receive, clear };
}
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});
describe("共享节点 WebSocket", () => {
  it("两消费者只连一次，释放最后一份引用才关闭", () => {
    const { manager, sockets, connect, receive } = setup();
    const online = vi.fn();
    const releaseA = manager.acquire("a", online);
    const releaseB = manager.acquire("a", online);
    expect(connect).toHaveBeenCalledTimes(1);
    sockets[0].onmessage?.({
      data: JSON.stringify({ id: 1, type: "status", data: 1 }),
    } as MessageEvent);
    expect(receive).toHaveBeenCalledWith({ id: 1, type: "status", data: 1 });
    expect(online).toHaveBeenCalledTimes(1);
    releaseA();
    expect(sockets[0].close).not.toHaveBeenCalled();
    releaseB();
    expect(sockets[0].close).toHaveBeenCalledOnce();
  });
  it("退避重连且最后释放后不重连，重新可见/联网会立即重连", async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    const { manager, sockets, connect } = setup();
    const release = manager.acquire("a");
    sockets[0].onclose?.({} as CloseEvent);
    await vi.advanceTimersByTimeAsync(999);
    expect(connect).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(connect).toHaveBeenCalledTimes(2);
    sockets[1].onclose?.({} as CloseEvent);
    window.dispatchEvent(new Event("online"));
    expect(connect).toHaveBeenCalledTimes(3);
    sockets[2].onclose?.({} as CloseEvent);
    release();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(connect).toHaveBeenCalledTimes(3);
  });
  it("切换会话关闭旧连接、清缓存，旧消费者释放不影响新连接", () => {
    const { manager, sockets, connect, clear } = setup();
    const releaseA = manager.acquire("a");
    const oldMessage = sockets[0].onmessage;
    const releaseB = manager.acquire("b");
    expect(sockets[0].close).toHaveBeenCalledOnce();
    expect(clear).toHaveBeenCalledOnce();
    expect(connect).toHaveBeenLastCalledWith("b");
    oldMessage?.call(sockets[0], { data: "{}" } as MessageEvent);
    releaseA();
    expect(sockets[1].close).not.toHaveBeenCalled();
    manager.reset();
    expect(sockets[1].close).toHaveBeenCalledOnce();
    expect(clear).toHaveBeenCalledTimes(2);
    releaseB();
  });
});
