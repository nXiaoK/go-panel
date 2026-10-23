import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiPost } from "@/lib/api/client";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { ForwardPage } from "./_app.forward";

vi.mock("@/lib/api/client", () => ({
  apiPost: vi.fn(),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : "请求失败"),
}));
const post = vi.mocked(apiPost);
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  localStorage.clear();
});

function renderPage(role: number) {
  localStorage.setItem("role_id", String(role));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        <ForwardPage />
      </ConfirmProvider>
    </QueryClientProvider>,
  );
  return client;
}

describe("forward page role requests", () => {
  it("loads only regular-user endpoints and displays the forward entry address", async () => {
    post.mockImplementation(async (path) =>
      path === "/forward/list"
        ? [
            {
              id: 1,
              name: "web",
              tunnelId: 3,
              inIp: "entry.example.com",
              inPort: 12345,
              remoteAddr: "example.com:443",
              status: 1,
            },
          ]
        : [],
    );
    const client = renderPage(1);
    await screen.findByText("entry.example.com:12345");
    expect(post.mock.calls.map(([path]) => path).sort()).toEqual([
      "/forward/list",
      "/tunnel/user/tunnel",
    ]);
    expect(screen.queryByRole("alert")).toBeNull();
    // The address shown in the row is authoritative even when no tunnel option remains.
    client.clear();
  });
  it("retains administrator endpoints from the first render", async () => {
    post.mockResolvedValue([]);
    const client = renderPage(0);
    await waitFor(() => expect(post).toHaveBeenCalledTimes(3));
    expect(post.mock.calls.map(([path]) => path).sort()).toEqual([
      "/forward/list",
      "/node/list",
      "/tunnel/list",
    ]);
    expect(screen.getByText("识别 NFT 规则")).toBeTruthy();
    client.clear();
  });
});
