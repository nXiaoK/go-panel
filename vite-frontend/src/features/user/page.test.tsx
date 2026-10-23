import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { apiPost } from "@/lib/api/client";
import { UserPage } from "./page";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
beforeEach(() => {
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
});
afterEach(() => {
  cleanup();
  client.clear();
  localStorage.clear();
  vi.resetAllMocks();
});

it("授权有缓存后重开失败，真实页面不允许使用旧行覆盖新配额或新增授权", async () => {
  let permissionsLoad = 0;
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/user/list") return [{ id: 1, user: "alice", status: 1, flow: 100, num: 10 }];
    if (path === "/tunnel/list") return [{ id: 2, name: "测试隧道", type: 1, status: 1 }];
    if (path === "/tunnel/user/list") {
      permissionsLoad += 1;
      if (permissionsLoad > 1) throw new Error("权限重取失败");
      return [
        { id: 3, userId: 1, tunnelId: 2, tunnelName: "旧配额隧道", flow: 10, num: 1, status: 1 },
      ];
    }
    return [];
  });
  render(
    <QueryClientProvider client={client}>
      <UserPage />
    </QueryClientProvider>,
  );
  const openManager = async () => {
    fireEvent.pointerDown(await screen.findByRole("button", { name: "用户操作" }), {
      button: 0,
      ctrlKey: false,
    });
    fireEvent.click(await screen.findByRole("menuitem", { name: "隧道权限" }));
  };
  await openManager();
  expect(await screen.findByRole("button", { name: "用户隧道操作：旧配额隧道" })).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "关闭" }));
  await openManager();
  await waitFor(() => expect(permissionsLoad).toBe(2));
  await waitFor(() => expect(screen.getByRole("button", { name: "分配" })).toBeDisabled());
  expect(screen.queryByRole("button", { name: "用户隧道操作：旧配额隧道" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "分配" }));
  expect(
    vi
      .mocked(apiPost)
      .mock.calls.some(([path]) => /tunnel\/user\/(update|assign|remove)$/.test(path)),
  ).toBe(false);
});
