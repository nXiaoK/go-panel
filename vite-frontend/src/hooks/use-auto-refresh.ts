import { useEffect, useRef } from "react";

/**
 * useAutoRefresh runs `callback` every `intervalMs` while the page is visible
 * and online. One timer, no overlap (a slow run skips ticks instead of
 * stacking), immediate catch-up when the tab becomes visible or the network
 * returns, and full cleanup on unmount.
 */
export function useAutoRefresh(
  callback: () => Promise<unknown> | unknown,
  intervalMs: number,
  enabled: boolean = true,
): void {
  const callbackRef = useRef(callback);
  callbackRef.current = callback;

  useEffect(() => {
    if (!enabled || intervalMs <= 0 || typeof window === "undefined") return;

    let running = false;
    const tick = async () => {
      if (running) return;
      if (document.visibilityState === "hidden") return;
      if (typeof navigator !== "undefined" && navigator.onLine === false) return;
      running = true;
      try {
        await callbackRef.current();
      } finally {
        running = false;
      }
    };

    const timer = window.setInterval(() => {
      void tick();
    }, intervalMs);
    const onWake = () => {
      if (document.visibilityState === "visible") void tick();
    };
    document.addEventListener("visibilitychange", onWake);
    window.addEventListener("online", onWake);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onWake);
      window.removeEventListener("online", onWake);
    };
  }, [enabled, intervalMs]);
}
