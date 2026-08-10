export type FormattedSpeedRate = {
  value: string;
  unit: "MB/s" | "GB/s";
};

export function formatSpeedRateFromMbps(mbps: unknown): FormattedSpeedRate {
  const value = Number(mbps);
  const mbPerSecond = Number.isFinite(value) && value > 0 ? value / 8 : 0;

  if (mbPerSecond >= 1024) {
    return {
      value: (mbPerSecond / 1024).toFixed(2),
      unit: "GB/s",
    };
  }

  return {
    value: mbPerSecond.toFixed(2),
    unit: "MB/s",
  };
}

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
