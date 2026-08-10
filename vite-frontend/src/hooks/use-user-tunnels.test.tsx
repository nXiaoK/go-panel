import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useUserTunnels } from "./use-user-tunnels";

type Row = { id: number; userId: number };

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

const ok = (data: Row[]) => ({ code: 0, msg: "", data });

describe("useUserTunnels", () => {
  it("discards user A when user B becomes active before A resolves", async () => {
    const a = deferred<{ code: number; msg: string; data: Row[] }>();
    const b = deferred<{ code: number; msg: string; data: Row[] }>();
    const loader = vi.fn((userID: number) => (userID === 1 ? a.promise : b.promise));

    const { result } = renderHook(() => useUserTunnels<Row, { id: number; user: string }>(loader));

    act(() => result.current.open({ id: 1, user: "a" }));
    act(() => result.current.open({ id: 2, user: "b" }));

    // A resolves late; its rows must be dropped because B is now active.
    await act(async () => {
      a.resolve(ok([{ id: 11, userId: 1 }]));
    });
    expect(result.current.rows).toEqual([]);

    await act(async () => {
      b.resolve(ok([{ id: 22, userId: 2 }]));
    });
    await waitFor(() => expect(result.current.rows[0]?.userId).toBe(2));
    expect(result.current.activeUserID).toBe(2);
  });

  it("allows mutations only when rows belong to the active user and idle", async () => {
    const loader = vi.fn(() => Promise.resolve(ok([{ id: 11, userId: 1 }])));
    const { result } = renderHook(() => useUserTunnels<Row, { id: number; user: string }>(loader));

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
    const loader = vi.fn(() => Promise.resolve({ code: 1, msg: "denied", data: [] as Row[] }));
    const { result } = renderHook(() => useUserTunnels<Row, { id: number; user: string }>(loader));
    await act(async () => {
      result.current.open({ id: 3, user: "c" });
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rows).toEqual([]);
    expect(result.current.canMutate).toBe(false);
  });
});
