import {
  RouterProvider,
  createRootRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";
import { TooltipProvider } from "../src/components/ui/tooltip";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ComponentType } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiPost } from "../src/lib/api/client";
import { ConfirmProvider } from "../src/components/patterns/confirm-dialog";
import { ForwardPage } from "../src/features/forward/page";
import { Route as TunnelRoute } from "../src/routes/_app.tunnel";

vi.mock("../src/lib/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../src/lib/api/client")>()),
  apiPost: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useNavigate: () => vi.fn(),
}));
const post = vi.mocked(apiPost);
let client: QueryClient;
afterEach(() => {
  cleanup();
  client?.clear();
  vi.restoreAllMocks();
  localStorage.clear();
});

const results = [
  {
    nodeId: 1,
    nodeName: "entry",
    targetIp: "exit.example",
    description: "入口->出口",
    success: true,
  },
  {
    nodeId: 2,
    nodeName: "exit",
    targetIp: "probe.example",
    description: "出口->外网",
    success: false,
    advisory: true,
    message: "目标不可达",
  },
];

describe("diagnosis result displays", () => {
  it.each(["tunnel", "forward"])(
    "renders %s advisory failure as a warning without failing the link",
    async (kind) => {
      window.matchMedia = vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      });
      localStorage.setItem("role_id", "0");
      localStorage.setItem("token", "test-token");
      vi.spyOn(TunnelRoute, "useSearch").mockReturnValue({ action: undefined });
      post.mockImplementation(async (path) => {
        if (path.endsWith("/diagnose"))
          return { results, tunnelName: "test-tunnel", tunnelType: "隧道转发" };
        if (path === "/tunnel/list")
          return [{ id: 1, name: "test-tunnel", type: 2, inNodeId: 1, outNodeId: 2 }];
        if (path === "/forward/list")
          return [
            {
              id: 1,
              name: "test-forward",
              tunnelId: 1,
              inIp: "entry.example",
              inPort: 10001,
              remoteAddr: "target.example:443",
              status: 1,
            },
          ];
        return [];
      });
      client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const Page =
        kind === "forward" ? ForwardPage : (TunnelRoute.options.component as ComponentType);
      render(
        <QueryClientProvider client={client}>
          <ConfirmProvider>
            {kind === "tunnel" ? (
              <TooltipProvider>
                <RouterProvider
                  router={createRouter({
                    routeTree: createRootRoute({ component: Page }),
                    history: createMemoryHistory({ initialEntries: ["/"] }),
                  })}
                />
              </TooltipProvider>
            ) : (
              <Page />
            )}
          </ConfirmProvider>
        </QueryClientProvider>,
      );
      const trigger = await screen.findByRole("button", {
        name: kind === "forward" ? "转发操作：test-forward" : "隧道操作：test-tunnel",
      });
      fireEvent.keyDown(trigger, { key: "Enter" });
      fireEvent.click(await screen.findByRole("menuitem", { name: "诊断" }));
      const dialog = await screen.findByRole("dialog");
      expect(
        await within(dialog).findByText(kind === "tunnel" ? "警告（仅供参考）" : "警告"),
      ).toBeTruthy();
      expect(within(dialog).getByText("外网探测仅供参考，受节点所在网络环境影响")).toBeTruthy();
      expect(within(dialog).getByText("链路诊断通过")).toBeTruthy();
      expect(within(dialog).queryByText("失败")).toBeNull();
    },
  );
});
