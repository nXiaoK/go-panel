import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SpeedTestSheet } from "../src/features/tunnel/components/speed-test-sheet";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});
describe("dialog layout", () => {
  it("移到隧道页的测速结果仍可滚动，底部操作不进入滚动区域", async () => {
    vi.stubGlobal(
      "WebSocket",
      class {
        close() {}
      },
    );
    localStorage.setItem("token", "test-token");
    render(
      <SpeedTestSheet
        tunnel={{ id: 1, name: "双节点隧道", type: 2, inNodeId: 1, outNodeId: 2, status: 1 }}
        onOpenChange={vi.fn()}
      />,
    );
    const dialog = screen.getByRole("dialog");
    const start = screen.getByRole("button", { name: "开始测速" });
    const footer = start.parentElement!;
    const body = dialog.querySelector(".overflow-y-auto")!;
    expect(dialog.classList.contains("overflow-hidden")).toBe(true);
    expect(dialog.classList.contains("h-dvh")).toBe(true);
    expect(body.classList.contains("overflow-y-auto")).toBe(true);
    expect(body.classList.contains("min-h-0")).toBe(true);
    expect(body.classList.contains("flex-1")).toBe(true);
    expect(footer.parentElement).toBe(dialog);
    expect(body.nextElementSibling).toBe(footer);
    expect(body.contains(start)).toBe(false);
    expect(footer.classList.contains("shrink-0")).toBe(true);
  });
});
