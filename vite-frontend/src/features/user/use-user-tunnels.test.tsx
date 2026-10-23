import { invalidateFor } from "@/lib/query-keys";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { userKeys } from "./queries";

import { useUserTunnels } from "@/features/user/use-user-tunnels";

let client: QueryClient;
function wrapper({ children }: PropsWithChildren) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
});
afterEach(() => {
  cleanup();
  client.clear();
  localStorage.clear();
});

type Row = { id: number; userId: number };

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

const ok = (data: Row[]) => data;

describe("useUserTunnels", () => {
  it("discards user A when user B becomes active before A resolves", async () => {
    const a = deferred<Row[]>();
    const b = deferred<Row[]>();
    const loader = vi.fn((userID: number) => (userID === 1 ? a.promise : b.promise));

    const { result } = renderHook(() => useUserTunnels<Row, { id: number; user: string }>(loader), {
      wrapper,
    });

    act(() => result.current.open({ id: 1, user: "a" }));
    act(() => result.current.open({ id: 2, user: "b" }));

    // A resolves late; its rows must be dropped because B is now active.
    await act(async () => {
      a.resolve(ok([{ id: 11, userId: 1 }]));
    });
    expect(result.current.rows).toEqual([]);
    expect(client.getQueryData(userKeys.tunnels(1))).toBeUndefined();

    await act(async () => {
      b.resolve(ok([{ id: 22, userId: 2 }]));
    });
    await waitFor(() => expect(result.current.rows[0]?.userId).toBe(2));
    expect(result.current.activeUserID).toBe(2);
  });

  it("allows mutations only when rows belong to the active user and idle", async () => {
    const loader = vi.fn(() => Promise.resolve(ok([{ id: 11, userId: 1 }])));
    const { result } = renderHook(() => useUserTunnels<Row, { id: number; user: string }>(loader), {
      wrapper,
    });

    expect(result.current.canMutate).toBe(false);
    await act(async () => {
      result.current.open({ id: 1, user: "a" });
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canMutate).toBe(true);

    act(() => result.current.close());
    expect(result.current.rows).toEqual([]);
    expect(result.current.canMutate).toBe(false);
    expect(result.current.activeUserID).toBeNull();
  });

  it("surfaces a failed load without applying rows", async () => {
    const loader = vi.fn(() => Promise.reject(new Error("denied")));
    const { result } = renderHook(() => useUserTunnels<Row, { id: number; user: string }>(loader), {
      wrapper,
    });
    await act(async () => {
      result.current.open({ id: 3, user: "c" });
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rows).toEqual([]);
    expect(result.current.canMutate).toBe(false);
  });
  it("有缓存后重开失败，不暴露旧配额或允许变更，成功重试才恢复", async () => {
    const onError = vi.fn();
    const loader = vi
      .fn()
      .mockResolvedValueOnce([{ id: 11, userId: 1 }])
      .mockRejectedValueOnce(new Error("重开失败"))
      .mockResolvedValueOnce([{ id: 12, userId: 1 }]);
    const { result } = renderHook(() => useUserTunnels<Row, { id: number }>(loader, onError), {
      wrapper,
    });
    act(() => result.current.open({ id: 1 }));
    await waitFor(() => expect(result.current.canMutate).toBe(true));
    act(() => result.current.close());
    expect(client.getQueryData(userKeys.tunnels(1))).toEqual([{ id: 11, userId: 1 }]);
    act(() => result.current.open({ id: 1 }));
    expect(result.current.rows).toEqual([]);
    expect(result.current.canMutate).toBe(false);
    await waitFor(() => expect(onError).toHaveBeenCalledWith("重开失败"));
    expect(result.current.loading).toBe(false);
    expect(result.current.rows).toEqual([]);
    expect(result.current.canMutate).toBe(false);
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.rows).toEqual([{ id: 12, userId: 1 }]));
    expect(result.current.canMutate).toBe(true);
  });

  it("限速变更失效后，打开中的真实用户授权查询重新获取并显示新行", async () => {
    const loader = vi
      .fn()
      .mockResolvedValueOnce([{ id: 11, userId: 1, speedName: "旧限速" }])
      .mockResolvedValueOnce([{ id: 11, userId: 1, speedName: "新限速" }]);
    const { result } = renderHook(
      () => useUserTunnels<Row & { speedName: string }, { id: number }>(loader),
      { wrapper },
    );
    act(() => result.current.open({ id: 1 }));
    await waitFor(() => expect(result.current.rows[0]?.speedName).toBe("旧限速"));
    await act(async () => {
      await invalidateFor(client, "limit.update");
    });
    await waitFor(() => expect(result.current.rows[0]?.speedName).toBe("新限速"));
    expect(loader).toHaveBeenCalledTimes(2);
  });
});
