import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { apiPost } from "@/lib/api/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SubscriptionPage } from "./page";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
afterEach(() => {
  cleanup();
  client.clear();
  localStorage.clear();
  vi.resetAllMocks();
});

it("真实订阅页保存后仅重取一次，关闭表单并显示新配置", async () => {
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
  let saved = false;
  const profile = {
    id: 1,
    name: "新订阅",
    token: "local-test-token",
    defaultFormat: "surge",
    status: 1,
  };
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/sub/settings")
      return { profiles: saved ? [profile] : [], nodes: [], profileNodes: [], tunnels: [] };
    if (path === "/sub/profile/create") {
      saved = true;
      return profile;
    }
    return [];
  });
  render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        <SubscriptionPage />
      </ConfirmProvider>
    </QueryClientProvider>,
  );
  await screen.findByText("暂无节点，先配置脚本上报或导入分享链接");
  expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/sub/settings")).toHaveLength(
    1,
  );
  fireEvent.click(screen.getByRole("button", { name: "新建订阅" }));
  fireEvent.change(screen.getByLabelText("名称"), { target: { value: "新订阅" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await screen.findByRole("heading", { name: "新订阅" });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(
    vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/sub/profile/create"),
  ).toHaveLength(1);
  expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/sub/settings")).toHaveLength(
    2,
  );
});
