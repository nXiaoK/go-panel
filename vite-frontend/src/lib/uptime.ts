function pad(value: number) {
  return String(value).padStart(2, "0");
}

export function formatUptimeSeconds(totalSeconds: number) {
  const safe = Math.max(0, Math.floor(Number.isFinite(totalSeconds) ? totalSeconds : 0));
  const days = Math.floor(safe / 86400);
  const hours = Math.floor((safe % 86400) / 3600);
  const minutes = Math.floor((safe % 3600) / 60);
  return `${pad(days)}d ${pad(hours)}h ${pad(minutes)}m`;
}
