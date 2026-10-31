import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ReportedNodeRestore } from "@/lib/types";
import { ReportedNodeRestoreSection } from "./reported-node-restore";

const target: ReportedNodeRestore = {
  sourceHost: "node-original",
  server: "192.0.2.5",
  nodes: [
    {
      id: 1,
      name: "旧 SS",
      protocol: "ss",
      port: 8388,
      action: "preserve",
      reason: "沿用原密码和加密方式",
    },
    {
      id: 2,
      name: "旧 Reality",
      protocol: "vless",
      port: 443,
      action: "regenerate",
      reason: "沿用 UUID，缺少私钥，自动生成新密钥对",
    },
    {
      id: 3,
      name: "旧 Snell",
      protocol: "snell",
      port: 44000,
      action: "preserve",
      reason: "沿用原 PSK",
    },
    {
      id: 4,
      name: "旧 SOCKS",
      protocol: "socks5",
      port: 1080,
      action: "preserve",
      reason: "沿用原用户名和密码",
    },
    {
      id: 5,
      name: "旧未知协议",
      protocol: "unknown",
      port: 444,
      action: "unsupported",
      reason: "此协议暂不支持从旧记录重建",
    },
  ],
  restoreCommand: "./vless-server.sh --flux-panel-restore-reported panel key node-original",
};

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

async function choose(option: RegExp) {
  fireEvent.keyDown(screen.getByRole("combobox", { name: "选择旧上报节点" }), { key: "ArrowDown" });
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

it("明确选择旧主机后显示原端口、恢复方式及刷新订阅提示，复制后端命令", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(<ReportedNodeRestoreSection targets={[target]} />);
  expect(screen.queryByRole("button", { name: "复制旧记录恢复命令" })).toBeNull();
  await choose(/node-original/);
  expect(screen.getByText(/可恢复 4 \/ 5 个协议/)).toHaveTextContent("跳过无法恢复的协议");
  expect(screen.getAllByText("沿用原凭据")).toHaveLength(3);
  expect(screen.getByText("重新生成密钥 / 证书")).toBeInTheDocument();
  expect(screen.getByText("此协议暂不支持从旧记录重建")).toBeInTheDocument();
  for (const node of target.nodes) {
    expect(screen.getByText(`${node.protocol} · 原端口 ${node.port}`)).toBeInTheDocument();
  }
  expect(screen.getByText("恢复后必须刷新订阅")).toBeInTheDocument();
  expect(screen.getByText(/Reality 的公钥会改变/)).toHaveTextContent("订阅链接和绑定保持不变");
  fireEvent.click(screen.getByRole("button", { name: "复制旧记录恢复命令" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(target.restoreCommand));
});

it("仅不支持的旧记录显示原因，即使命令意外非空也不提供执行入口", async () => {
  render(<ReportedNodeRestoreSection targets={[{ ...target, nodes: [target.nodes[4]] }]} />);
  await choose(/node-original/);
  expect(screen.getByText("无法恢复")).toBeInTheDocument();
  expect(screen.getByText(/此节点无法从旧上报记录自动恢复/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "复制旧记录恢复命令" })).toBeNull();
  expect(screen.queryByText(target.restoreCommand)).toBeNull();
});

it("来源缺失的同地址记录按节点区分，不自行拼接恢复命令", async () => {
  const unknown = target.nodes.slice(0, 2).map((node) => ({
    ...target,
    sourceHost: "",
    nodes: [{ ...node, action: "unsupported" as const, reason: `无法确认原主机 ${node.name}` }],
    restoreCommand: "",
  }));
  render(<ReportedNodeRestoreSection targets={unknown} />);
  await choose(/来源标识缺失 #1/);
  expect(screen.getByText("无法确认原主机 旧 SS")).toBeInTheDocument();
  await choose(/来源标识缺失 #2/);
  expect(screen.getByText("无法确认原主机 旧 Reality")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "复制旧记录恢复命令" })).toBeNull();
});

it("列表为空或已选目标在刷新后消失时，不保留可复制命令", async () => {
  const { rerender } = render(<ReportedNodeRestoreSection targets={[target]} />);
  await choose(/node-original/);
  expect(screen.getByText(target.restoreCommand)).toBeInTheDocument();
  rerender(<ReportedNodeRestoreSection targets={[]} />);
  expect(screen.getByText("暂无可用于恢复的旧上报记录")).toBeInTheDocument();
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(screen.queryByRole("button", { name: "复制旧记录恢复命令" })).toBeNull();
});
