import { useBusinessEnabled } from "@/lib/query-keys";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { useUserTunnelRows, userKeys } from "./queries";

export type UseUserTunnelsResult<Row, User extends { id: number }> = {
  /** Open the manager for one user; aborts and discards any previous load. */
  open: (user: User) => void;
  /** Close the manager; discards state and invalidates in-flight responses. */
  close: () => void;
  /** Reload the ACTIVE user's rows (after a mutation). */
  reload: () => void;
  rows: Row[];
  loading: boolean;
  /** True only when rows belong to the active user and nothing is in flight. */
  canMutate: boolean;
  activeUserID: number | null;
  activeUser: User | null;
};

/** 权限行归属由独立queryKey保证；切换/关闭显式取消旧请求，禁止迟到响应恢复旧用户行。 */
export function useUserTunnels<Row extends { userId?: number }, User extends { id: number }>(
  loader: (userID: number, signal?: AbortSignal) => Promise<Row[]>,
  onError?: (message: string) => void,
): UseUserTunnelsResult<Row, User> {
  const client = useQueryClient();
  const enabled = useBusinessEnabled(true);
  const [activeUser, setActiveUser] = useState<User | null>(null);
  const activeUserID = activeUser?.id ?? null;
  const query = useUserTunnelRows(activeUserID, loader);
  useEffect(() => {
    if (query.error) onError?.(query.error.message);
  }, [query.error, onError]);
  const cancelActive = useCallback(() => {
    if (activeUserID !== null)
      void client.cancelQueries({ queryKey: userKeys.tunnels(activeUserID), exact: true });
  }, [client, activeUserID]);
  const open = useCallback(
    (user: User) => {
      cancelActive();
      setActiveUser(user);
      void client.invalidateQueries({ queryKey: userKeys.tunnels(user.id), exact: true });
    },
    [client, cancelActive],
  );
  const close = useCallback(() => {
    cancelActive();
    setActiveUser(null);
  }, [cancelActive]);
  const reload = useCallback(() => {
    if (activeUserID !== null)
      void client.invalidateQueries({ queryKey: userKeys.tunnels(activeUserID), exact: true });
  }, [client, activeUserID]);
  const loading = activeUserID !== null && query.isFetching;
  // 重开或失效重取期间必须等待本次成功；失败时不能暴露旧配额，避免状态操作回写旧值。
  const canMutate =
    enabled &&
    activeUserID !== null &&
    query.isSuccess &&
    !loading &&
    (query.data ?? []).every((row) => row.userId === undefined || row.userId === activeUserID);
  const rows = canMutate ? (query.data ?? []) : [];
  return { open, close, reload, rows, loading, canMutate, activeUserID, activeUser };
}
