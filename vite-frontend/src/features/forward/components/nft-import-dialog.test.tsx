import { NftDetectDialog } from "./nft-import-dialog";
import { completeFromNft, detectNftRules, detectTunnelRules } from "@/lib/api";
import type { Node } from "@/lib/types";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  completeFromNft: vi.fn(),
  detectNftRules: vi.fn(),
  detectTunnelRules: vi.fn(),
}));
const nodes: Node[] = [
  { id: 1, name: "入口", forwardMode: "nftables", status: 1 },
  { id: 2, name: "出口", forwardMode: "nftables", status: 1 },
];
beforeEach(() => {
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
function show() {
  const onComplete = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <NftDetectDialog open onOpenChange={onOpenChange} onComplete={onComplete} nodes={nodes} />,
  );
  return { onComplete, onOpenChange };
}
describe("NFT rule selection", () => {
  it("preselects suggested port rules and imports the exact checked rows", async () => {
    const base = {
      tunnelId: 3,
      tunnelName: "test tunnel",
      targetHost: "target.example",
      protocol: "tcp",
      comment: "",
      outPort: 443,
    };
    vi.mocked(detectNftRules).mockResolvedValue({
      nodeId: 1,
      nodeName: "入口",
      total: 2,
      detected: [
        { ...base, inPort: 20001, rawRule: "first", suggested: true },
        { ...base, inPort: 20002, rawRule: "second", suggested: false },
      ],
    });
    vi.mocked(completeFromNft).mockResolvedValue({ created: 1, failed: 0, details: [] });
    const callbacks = show();
    fireEvent.click(screen.getByRole("button", { name: "下一步" }));
    fireEvent.click(screen.getByRole("button", { name: "开始识别" }));
    const selected = await screen.findByRole("checkbox", { name: "选择行 0" });
    expect(selected.getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("checkbox", { name: "选择行 1" }).getAttribute("aria-checked")).toBe(
      "false",
    );
    fireEvent.click(selected);
    fireEvent.click(screen.getByRole("checkbox", { name: "选择行 1" }));
    fireEvent.click(screen.getByRole("button", { name: "补全所选 (1)" }));
    await waitFor(() =>
      expect(completeFromNft).toHaveBeenCalledWith(1, [
        {
          tunnelId: 3,
          inPort: 20002,
          outPort: 443,
          targetHost: "target.example",
          protocol: "tcp",
          name: "NFT识别-端口20002",
          rawRule: "second",
        },
      ]),
    );
    expect(callbacks.onComplete).toHaveBeenCalledOnce();
    expect(callbacks.onOpenChange).toHaveBeenCalledWith(false);
  });
  it("keeps typed two-node tunnel import fields intact", async () => {
    vi.mocked(detectTunnelRules).mockResolvedValue({
      inNodeId: 1,
      outNodeId: 2,
      inNodeName: "入口",
      outNodeName: "出口",
      total: 1,
      detected: [
        {
          tunnelId: 3,
          tunnelName: "test tunnel",
          targetHost: "target.example",
          protocol: "tcp",
          comment: "",
          inPort: 20001,
          relayPort: 30001,
          targetPort: 443,
          inNodeName: "入口",
          outNodeName: "出口",
          inRawRule: "in-rule",
          outRawRule: "out-rule",
          suggested: true,
        },
      ],
    });
    vi.mocked(completeFromNft).mockResolvedValue({ created: 1, failed: 0, details: [] });
    show();
    fireEvent.click(screen.getByRole("radio", { name: /隧道转发识别/ }));
    fireEvent.click(screen.getByRole("button", { name: "下一步" }));
    fireEvent.click(screen.getByRole("button", { name: "开始识别" }));
    fireEvent.click(await screen.findByRole("button", { name: "补全所选 (1)" }));
    await waitFor(() =>
      expect(completeFromNft).toHaveBeenCalledWith(1, [
        {
          tunnelId: 3,
          inPort: 20001,
          outPort: 30001,
          targetPort: 443,
          targetHost: "target.example",
          protocol: "tcp",
          name: "NFT识别-隧道20001",
          rawRule: "in-rule",
          outRawRule: "out-rule",
        },
      ]),
    );
  });
});
