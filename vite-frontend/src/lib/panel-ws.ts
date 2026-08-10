import { currentPanelEndpoint, resolvePanelEndpoint } from "@/lib/panel-endpoint";

export function buildPanelWsUrlFromBase(base: string): string {
  return resolvePanelEndpoint({
    stored: base,
    origin: base,
    pageProtocol: base.startsWith("https") ? "https:" : "http:",
  }).wsURL();
}

export function buildPanelWsUrl(): string {
  return currentPanelEndpoint().wsURL();
}

export function createSpeedTestId(): string {
  const randomUUID = typeof crypto !== "undefined" ? crypto.randomUUID?.bind(crypto) : null;
  if (randomUUID) return `speed-${randomUUID()}`;
  return `speed-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
