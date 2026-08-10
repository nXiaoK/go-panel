import { describe, expect, it } from "vitest";

import { buildPanelWsUrlFromBase, createSpeedTestId } from "../src/lib/panel-ws";

describe("panel websocket helpers", () => {
  it("builds an admin websocket URL from the panel base URL", () => {
    expect(buildPanelWsUrlFromBase("http://127.0.0.1:6365")).toBe(
      "ws://127.0.0.1:6365/system-info?type=0",
    );
    // A reverse-proxy base path is preserved so the upgrade request reaches
    // the proxied panel (previously the path was dropped).
    expect(buildPanelWsUrlFromBase("https://panel.example.com/app/")).toBe(
      "wss://panel.example.com/app/system-info?type=0",
    );
  });

  it("normalizes a legacy /api/v1 suffix", () => {
    expect(buildPanelWsUrlFromBase("https://panel.example.com/api/v1")).toBe(
      "wss://panel.example.com/system-info?type=0",
    );
  });

  it("creates a usable client-side speed test id", () => {
    expect(createSpeedTestId()).toMatch(/^speed-/);
  });
});
