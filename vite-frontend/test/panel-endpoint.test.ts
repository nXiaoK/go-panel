import { describe, expect, it } from "vitest";

import { resolvePanelEndpoint } from "../src/lib/panel-endpoint";

describe("resolvePanelEndpoint", () => {
  it("uses stored, env, then origin precedence", () => {
    expect(
      resolvePanelEndpoint({
        stored: "https://stored.example",
        env: "https://env.example",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).apiBase,
    ).toBe("https://stored.example/api/v1/");

    expect(
      resolvePanelEndpoint({
        stored: "",
        env: "https://env.example",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).apiBase,
    ).toBe("https://env.example/api/v1/");

    expect(
      resolvePanelEndpoint({
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).apiBase,
    ).toBe("https://ui.example/api/v1/");
  });

  it("normalizes a legacy api suffix and bare host", () => {
    const endpoint = resolvePanelEndpoint({
      stored: "panel.example:6365/api/v1",
      origin: "https://ui.example",
      pageProtocol: "https:",
    });
    expect(endpoint.root).toBe("https://panel.example:6365");
    expect(endpoint.apiBase).toBe("https://panel.example:6365/api/v1/");
  });

  it("prefixes a bare host with the page protocol", () => {
    expect(
      resolvePanelEndpoint({
        stored: "panel.example",
        origin: "http://ui.example",
        pageProtocol: "http:",
      }).root,
    ).toBe("http://panel.example");
  });

  it("removes trailing slashes and preserves a base path", () => {
    expect(
      resolvePanelEndpoint({
        stored: "https://host.example/base/",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).apiBase,
    ).toBe("https://host.example/base/api/v1/");
  });

  it("builds a ws url with type=0 and scheme conversion", () => {
    expect(
      resolvePanelEndpoint({
        stored: "https://panel.example",
        origin: "https://ui.example",
        pageProtocol: "https:",
      }).wsURL(),
    ).toBe("wss://panel.example/system-info?type=0");
    expect(
      resolvePanelEndpoint({
        stored: "http://panel.example:6365",
        origin: "http://ui.example",
        pageProtocol: "http:",
      }).wsURL(),
    ).toBe("ws://panel.example:6365/system-info?type=0");
  });

  it("builds subscription urls with encoded token and format", () => {
    const endpoint = resolvePanelEndpoint({
      stored: "https://panel.example",
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
});
