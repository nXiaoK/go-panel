import { useCallback, useRef, useState } from "react";

type LoaderResponse<Row> = {
  code: number;
  msg?: string;
  data?: Row[] | { records?: Row[] } | null;
};

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

/**
 * useUserTunnels owns the per-user tunnel-permission rows behind the tunnel
 * manager dialog. Every load is tagged with a request generation and the
 * owning user ID; a response applies only when both still match, so a slow
 * response for user A can never appear under user B's dialog (and mutations
 * are blocked until ownership is proven).
 */
export function useUserTunnels<Row extends { userId?: number }, User extends { id: number }>(
  loader: (userID: number) => Promise<LoaderResponse<Row>>,
  onError?: (message: string) => void,
): UseUserTunnelsResult<Row, User> {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(false);
  const [activeUser, setActiveUser] = useState<User | null>(null);
  const [ownerUserID, setOwnerUserID] = useState<number | null>(null);
  const generation = useRef(0);

  const load = useCallback(
    (user: User) => {
      generation.current += 1;
      const requestGeneration = generation.current;
      setRows([]);
      setOwnerUserID(null);
      setLoading(true);
      loader(user.id)
        .then((res) => {
          if (generation.current !== requestGeneration) return; // superseded
          if (res.code === 0) {
            const d = res.data as Row[] | { records?: Row[] } | null | undefined;
            const next: Row[] = Array.isArray(d) ? d : (d?.records ?? []);
            setRows(next);
            setOwnerUserID(user.id);
          } else {
            onError?.(res.msg || "获取隧道权限失败");
          }
        })
        .catch(() => {
          if (generation.current !== requestGeneration) return;
          onError?.("获取隧道权限失败");
        })
        .finally(() => {
          if (generation.current !== requestGeneration) return;
          setLoading(false);
        });
    },
    [loader, onError],
  );

  const open = useCallback(
    (user: User) => {
      setActiveUser(user);
      load(user);
    },
    [load],
  );

  const reload = useCallback(() => {
    if (activeUser) load(activeUser);
  }, [activeUser, load]);

  const close = useCallback(() => {
    generation.current += 1; // invalidate any in-flight response
    setActiveUser(null);
    setOwnerUserID(null);
    setRows([]);
    setLoading(false);
  }, []);

  const activeUserID = activeUser?.id ?? null;
  const canMutate = !loading && activeUserID !== null && ownerUserID === activeUserID;

  return { open, close, reload, rows, loading, canMutate, activeUserID, activeUser };
}
