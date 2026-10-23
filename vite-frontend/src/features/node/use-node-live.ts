import { useSyncExternalStore } from "react";
import { nodeLiveStore } from "./live-store";
const emptySnapshot = new Map<never, never>();
export function useNodeLives() {
  return useSyncExternalStore(
    nodeLiveStore.subscribe,
    nodeLiveStore.getSnapshot,
    () => emptySnapshot,
  );
}
export function useNodeLive(id: number) {
  return useNodeLives().get(id);
}
