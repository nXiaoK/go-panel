/**
 * reconnectDelay returns the wait before reconnect attempt `attempt`
 * (0-based): 1s base, doubling, ±20% jitter, capped at 30s. Mirrors the Go
 * agent Backoff policy so panel and agents share one reconnect behavior.
 */
export function reconnectDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(30000, 1000 * 2 ** Math.max(0, Math.min(attempt, 30)));
  return Math.round(base * (0.8 + random() * 0.4));
}
