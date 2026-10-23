export { formatMbps as formatSpeedRateFromMbps, type FormattedSpeedRate } from "./format";

function numericMetric(value: unknown): number | null {
  if (value === null || value === undefined || value === "") return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
}

export function formatLatencyMs(value: unknown): string {
  const parsed = numericMetric(value);
  return parsed === null ? "--" : `${parsed.toFixed(2)} ms`;
}

export function formatLossPercent(value: unknown): string {
  const parsed = numericMetric(value);
  return parsed === null ? "--" : `${parsed.toFixed(2)}%`;
}
