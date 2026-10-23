import { cleanup, render, screen } from "@testing-library/react";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { Node } from "@/lib/types";
import { afterEach, expect, it } from "vitest";
import { PathChips } from "./path-chips";
import { emptyTunnelForm } from "../model";
const nodes = new Map<number, Node>([
  [1, { id: 1, name: "入口", status: 1, forwardMode: "gost" }],
  [2, { id: 2, name: "中继", status: 0, forwardMode: "nftables" }],
  [3, { id: 3, name: "出口", status: 1, forwardMode: "gost" }],
]);
afterEach(cleanup);
it("按顺序展示二节点，并将模式作为文字", () => {
  render(
    <TooltipProvider>
      <PathChips
        tunnel={{ ...emptyTunnelForm(), type: 2, inNodeId: 1, outNodeId: 3 }}
        nodes={nodes}
      />
    </TooltipProvider>,
  );
  expect(screen.getByLabelText("隧道链路").textContent).toBe("入口GOST出口GOST");
  expect(screen.queryByLabelText("节点离线")).toBeNull();
});
it("三节点链路离线节点有 critical 圆点及可访问说明", () => {
  render(
    <TooltipProvider>
      <PathChips
        tunnel={{ ...emptyTunnelForm(), type: 2, inNodeId: 1, relayNodeId: 2, outNodeId: 3 }}
        nodes={nodes}
      />
    </TooltipProvider>,
  );
  expect(screen.getByLabelText("隧道链路").textContent).toBe("入口GOST中继NFT出口GOST");
  expect(screen.getByLabelText("节点离线").className).toContain("bg-status-critical");
});
