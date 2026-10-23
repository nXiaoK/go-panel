import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import { FormField } from "@/components/patterns/form-field";
import type { Node } from "@/lib/types";
import { emptyTunnelForm } from "../model";
import { TunnelFormSheet, TunnelNodeSelect } from "./tunnel-form-sheet";
const nodes: Node[] = [
  { id: 1, name: "入口", status: 1, forwardMode: "nftables" },
  { id: 2, name: "中继离线", status: 0, forwardMode: "nftables" },
  { id: 3, name: "GOST", status: 1, forwardMode: "gost" },
];
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  HTMLElement.prototype.scrollIntoView = vi.fn();
  HTMLElement.prototype.hasPointerCapture = () => false;
  HTMLElement.prototype.setPointerCapture = () => {};
  HTMLElement.prototype.releasePointerCapture = () => {};
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("实际节点下拉禁用重复与不支持的模式并展示原因，离线仍可选", async () => {
  render(
    <FormField label="中继节点">
      <TunnelNodeSelect
        role="relayNodeId"
        form={{ ...emptyTunnelForm(), type: 2, inNodeId: 1 }}
        nodes={nodes}
        onChange={vi.fn()}
      />
    </FormField>,
  );
  fireEvent.keyDown(screen.getByRole("combobox", { name: "中继节点" }), { key: "Enter" });
  const duplicate = await screen.findByRole("option", { name: /入口.*重复/ });
  expect(duplicate.getAttribute("aria-disabled")).toBe("true");
  expect(
    screen.getByRole("option", { name: /GOST.*仅支持 nftables/ }).getAttribute("aria-disabled"),
  ).toBe("true");
  expect(
    screen.getByRole("option", { name: /中继离线.*离线/ }).getAttribute("aria-disabled"),
  ).not.toBe("true");
});
it("空表单提交显示关联字段错误，不使用toast校验", async () => {
  const save = vi.fn();
  render(
    <TooltipProvider>
      <TunnelFormSheet
        open
        onOpenChange={vi.fn()}
        initial={emptyTunnelForm()}
        nodes={nodes}
        onSave={save}
      />
    </TooltipProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "创建隧道" }));
  expect(await screen.findByText("请输入隧道名称")).toBeTruthy();
  expect(screen.getByLabelText(/隧道名称/).getAttribute("aria-invalid")).toBe("true");
  expect(screen.getByRole("combobox", { name: /入口节点/ }).getAttribute("aria-invalid")).toBe(
    "true",
  );
  expect(save).not.toHaveBeenCalled();
});
it("编辑禁用类型和链路，说明限制来源，状态不提供无效切换", () => {
  render(
    <TooltipProvider>
      <TunnelFormSheet
        open
        onOpenChange={vi.fn()}
        initial={{ ...emptyTunnelForm(), id: 9, name: "现有", inNodeId: 1 }}
        nodes={nodes}
        onSave={vi.fn()}
      />
    </TooltipProvider>,
  );
  expect(screen.getByRole("combobox", { name: /入口节点/ }).hasAttribute("disabled")).toBe(true);
  for (const radio of screen.getAllByRole("radio", { name: /端口转发|隧道转发/ }))
    expect(radio.hasAttribute("disabled")).toBe(true);
  expect(
    screen.getAllByText("现有链路不支持直接更换类型或节点，如需更换请新建隧道").length,
  ).toBeGreaterThan(0);
  expect(screen.queryByRole("combobox", { name: "状态" })).toBeNull();
});
