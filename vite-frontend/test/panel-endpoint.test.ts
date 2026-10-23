import { afterEach, describe, expect, it, vi } from "vitest";

import { currentPanelEndpoint, resolvePanelEndpoint } from "../src/lib/panel-endpoint";

afterEach(() => {
  vi.unstubAllEnvs();
  localStorage.clear();
});

describe("resolvePanelEndpoint", () => {
  it("uses development env then same origin", () => {
    expect(
      resolvePanelEndpoint({
        env: "https://env.example",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).apiBase,
    ).toBe("https://env.example/api/v1/");
    expect(
      resolvePanelEndpoint({ env: "", origin: "https://ui.example", pageProtocol: "https:" })
        .apiBase,
    ).toBe("https://ui.example/api/v1/");
  });

  it("normalizes a development api suffix and bare host", () => {
    const endpoint = resolvePanelEndpoint({
      env: "panel.example:6365/api/v1",
      origin: "https://ui.example",
      pageProtocol: "https:",
    });
    expect(endpoint.root).toBe("https://panel.example:6365");
    expect(endpoint.apiBase).toBe("https://panel.example:6365/api/v1/");
  });

  it("prefixes a bare host with the page protocol", () => {
    expect(
      resolvePanelEndpoint({
        env: "panel.example",
        origin: "http://ui.example",
        pageProtocol: "http:",
      }).root,
    ).toBe("http://panel.example");
  });

  it("removes trailing slashes and preserves a base path", () => {
    expect(
      resolvePanelEndpoint({
        env: "https://host.example/base/",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).apiBase,
    ).toBe("https://host.example/base/api/v1/");
  });

  it("builds a ws url with type=0 and scheme conversion", () => {
    expect(
      resolvePanelEndpoint({
        env: "https://panel.example",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).wsURL(),
    ).toBe("wss://panel.example/system-info?type=0");
    expect(
      resolvePanelEndpoint({
        env: "http://panel.example:6365",
        origin: "http://ui.example",
        pageProtocol: "http:",
      }).wsURL(),
    ).toBe("ws://panel.example:6365/system-info?type=0");
  });

  it("builds subscription urls with encoded token and format", () => {
    const endpoint = resolvePanelEndpoint({
      env: "https://panel.example",
      origin: "https://ui.example",
      pageProtocol: "https:",
    });
    expect(endpoint.subscriptionURL("tok en")).toBe(
      "https://panel.example/api/v1/sub/render/tok%20en",
    );
    expect(endpoint.subscriptionURL("t", "clash")).toBe(
      "https://panel.example/api/v1/sub/render/t/clash",
    );
    expect(endpoint.subscriptionURL("t", "singbox")).toBe(
      "https://panel.example/api/v1/sub/render/t/singbox",
    );
  });
  it("生产环境忽略旧浏览器地址和开发环境覆盖，始终同源", () => {
    localStorage.setItem("panel_address", "https://old.example");
    vi.stubEnv("VITE_API_BASE", "https://dev.example/base");
    vi.stubEnv("DEV", false);
    expect(currentPanelEndpoint().apiBase).toBe(`${window.location.origin}/api/v1/`);
    expect(currentPanelEndpoint().wsURL()).toBe(`ws://${window.location.host}/system-info?type=0`);
    vi.stubEnv("DEV", true);
    expect(currentPanelEndpoint().apiBase).toBe("https://dev.example/base/api/v1/");
  });
});
