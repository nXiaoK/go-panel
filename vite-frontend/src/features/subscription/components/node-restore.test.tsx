import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SubscriptionNodeBackup } from "@/lib/types";
import { SubscriptionNodeRestore } from "./node-restore";

const backups: SubscriptionNodeBackup[] = [
  {
    sourceHost: "node-original-tokyo",
    server: "tokyo.example",
    protocols: ["vless", "hy2"],
    updatedTime: 1793383200000,
    restoreCommand: "./vless-server.sh --flux-panel-restore panel key node-original-tokyo",
  },
  {
    sourceHost: "node-original-osaka",
    server: "osaka.example",
    protocols: ["snell"],
    updatedTime: 1793386800000,
    restoreCommand: "./vless-server.sh --flux-panel-restore panel key node-original-osaka",
  },
];

beforeEach(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn();
  HTMLElement.prototype.hasPointerCapture = () => false;
  HTMLElement.prototype.setPointerCapture = vi.fn();
  HTMLElement.prototype.releasePointerCapture = vi.fn();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function choose(host: string) {
  fireEvent.keyDown(screen.getByRole("combobox", { name: "选择原节点备份" }), {
    key: "ArrowDown",
  });
  fireEvent.click(await screen.findByRole("option", { name: new RegExp(host) }));
}

it("无备份说明须在重装前完成新版上报，不提供恢复命令", () => {
  render(<SubscriptionNodeRestore backups={[]} />);
  expect(screen.getByText("暂无可恢复的节点备份")).toBeInTheDocument();
  expect(screen.getByText(/请在重装前/)).toHaveTextContent("仅有订阅连接参数无法完整恢复");
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(screen.queryByRole("button", { name: "复制恢复命令" })).toBeNull();
});

it("明确选择主机后展示备份元数据，切换后复制对应恢复命令", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(<SubscriptionNodeRestore backups={backups} />);
  expect(screen.queryByRole("button", { name: "复制恢复命令" })).toBeNull();
  expect(screen.getByText(/备份包含私钥和证书/)).toBeInTheDocument();

  await choose("node-original-tokyo");
  expect(screen.getByText(backups[0].restoreCommand)).toBeInTheDocument();
  expect(screen.getByText("vless / hy2")).toBeInTheDocument();
  expect(screen.getByText("备份时间")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "复制恢复命令" }));
  await waitFor(() => expect(writeText).toHaveBeenLastCalledWith(backups[0].restoreCommand));

  await choose("node-original-osaka");
  expect(screen.queryByText(backups[0].restoreCommand)).toBeNull();
  expect(screen.getByText(backups[1].restoreCommand)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "复制恢复命令" }));
  await waitFor(() => expect(writeText).toHaveBeenLastCalledWith(backups[1].restoreCommand));
});

it("刷新后选中的备份被移除，不保留过期恢复命令", async () => {
  const { rerender } = render(<SubscriptionNodeRestore backups={backups} />);
  await choose("node-original-tokyo");
  expect(screen.getByText(backups[0].restoreCommand)).toBeInTheDocument();
  rerender(<SubscriptionNodeRestore backups={[backups[1]]} />);
  expect(screen.queryByText(backups[0].restoreCommand)).toBeNull();
  expect(screen.queryByRole("button", { name: "复制恢复命令" })).toBeNull();
});
