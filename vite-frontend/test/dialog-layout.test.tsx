import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "../src/features/dashboard/page";
import { apiPost } from "../src/lib/api/client";
vi.mock("../src/lib/api/client", async (original) => ({
  ...(await original<typeof import("../src/lib/api/client")>()),
  apiPost: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useNavigate: () => vi.fn(),
}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});
describe("dialog layout", () => {
  it("keeps pressure test results scrollable without pushing footer actions out of view", async () => {
    localStorage.setItem("token", "test-token");
    localStorage.setItem("role_id", "0");
    vi.mocked(apiPost).mockImplementation(async (path) =>
      path === "/user/package"
        ? { userInfo: { flow: 100, num: 10 }, tunnelPermissions: [], forwards: [] }
        : path === "/tunnel/list"
          ? [{ id: 1, name: "测试双节点隧道", type: 2, inNodeId: 1, outNodeId: 2, status: 1 }]
          : [],
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <Dashboard />
      </QueryClientProvider>,
    );
    await screen.findByText("system nominal");
    fireEvent.click(await screen.findByRole("button", { name: "压力测试" }));
    const dialog = await screen.findByRole("dialog");
    const heading = screen.getByRole("heading", { name: "隧道压力测试" });
    const start = screen.getByRole("button", { name: /开始测试/ });
    const footer = start.parentElement!;
    const body = heading.parentElement!.nextElementSibling!;
    expect(dialog.classList.contains("overflow-hidden")).toBe(true);
    expect(dialog.classList.contains("max-h-[calc(100dvh-2rem)]")).toBe(true);
    expect(body.classList.contains("overflow-y-auto")).toBe(true);
    expect(body.classList.contains("min-h-0")).toBe(true);
    expect(body.classList.contains("flex-1")).toBe(true);
    expect(footer.parentElement).toBe(dialog);
    expect(body.nextElementSibling).toBe(footer);
    expect(body.contains(start)).toBe(false);
    expect(footer.classList.contains("shrink-0")).toBe(true);
    client.clear();
  });
});
