import { useCallback, useSyncExternalStore } from "react";
import { nodeLiveStore } from "./live-store";
const emptySnapshot = new Map<never, never>();
export function useNodeLives() {
  return useSyncExternalStore(
    nodeLiveStore.subscribe,
    nodeLiveStore.getSnapshot,
    () => emptySnapshot,
  );
}
export function useNodeConnections() {
  return useSyncExternalStore(
    nodeLiveStore.subscribe,
    nodeLiveStore.getConnectionsSnapshot,
    () => emptySnapshot,
  );
}
export function useNodeLive(id: number) {
  const getSnapshot = useCallback(() => nodeLiveStore.getSnapshot().get(id), [id]);
  return useSyncExternalStore(nodeLiveStore.subscribe, getSnapshot, () => undefined);
}
