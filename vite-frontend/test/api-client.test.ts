import { renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InternalAxiosRequestConfig } from "axios";
import {
  ApiError,
  apiPost,
  apiGet,
  apiDownload,
  apiUpload,
  setPanelAddress,
} from "../src/lib/api/client";
const adapter = vi.hoisted(() => vi.fn());
vi.mock("axios", async (importOriginal) => {
  const actual = await importOriginal<typeof import("axios")>();
  const instance = actual.default.create({ adapter });
  return { ...actual, default: { ...actual.default, create: () => instance } };
});
function respond(data: unknown, headers: Record<string, string> = {}) {
  adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => ({
    data,
    headers,
    status: 200,
    statusText: "OK",
    config,
  }));
}
function fail(status?: number, data?: unknown, code?: string) {
  adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => {
    throw { isAxiosError: true, config, code, response: status ? { status, data } : undefined };
  });
}
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});
afterEach(() => {
  localStorage.clear();
});
describe("unified API client", () => {
  it("returns only success data and preserves business failure messages", async () => {
    respond({ code: 0, data: { id: 42 } });
    await expect(apiPost("/test")).resolves.toEqual({ id: 42 });
    respond({ code: -1, msg: "规则未同步", data: null });
    await expect(apiPost("/test")).rejects.toMatchObject({
      name: "ApiError",
      code: -1,
      message: "规则未同步",
      httpStatus: 200,
    });
  });
  it("keeps the backend Chinese message for HTTP 503", async () => {
    fail(503, { code: 503, msg: "系统维护中，请稍后重试" });
    await expect(apiGet("/test")).rejects.toMatchObject({
      code: 503,
      message: "系统维护中，请稍后重试",
      httpStatus: 503,
    });
  });
  it("uses Chinese fallback for a non-JSON HTTP 413", async () => {
    fail(413, "<html>too large</html>");
    await expect(apiUpload("/test", new FormData())).rejects.toMatchObject({
      message: "上传内容过大",
      httpStatus: 413,
    });
  });
  it("uses Chinese fallback for empty HTTP 503 and network failures", async () => {
    fail(503);
    await expect(apiGet("/test")).rejects.toThrow("系统维护中，请稍后重试");
    fail();
    await expect(apiGet("/test")).rejects.toThrow("无法连接面板，请检查网络");
  });
  it.each(["ECONNABORTED", "ETIMEDOUT"])("classifies timeout %s", async (code) => {
    fail(undefined, undefined, code);
    await expect(apiPost("/test")).rejects.toMatchObject({
      code: -2,
      message: "请求超时，请检查网络或稍后重试",
    });
  });
  it("parses JSON download errors on successful and non-2xx responses", async () => {
    const body = { text: async () => JSON.stringify({ code: -1, msg: "备份不可用" }) };
    respond(body, { "content-type": "application/json" });
    await expect(apiDownload("/backup")).rejects.toThrow("备份不可用");
    fail(503, body);
    await expect(apiDownload("/backup")).rejects.toMatchObject({
      message: "备份不可用",
      httpStatus: 503,
    });
  });
  it.each([
    ['attachment; filename="panel.db"', "panel.db"],
    ["attachment; filename=fallback.db; filename*=UTF-8''%E5%A4%87%E4%BB%BD.db", "备份.db"],
    ["attachment; filename=fallback.db; filename*=UTF-8''%ZZ", "fallback.db"],
  ])("uses the download filename in %s", async (disposition, filename) => {
    const blob = new Blob(["SQLite"]);
    respond(blob, { "content-disposition": disposition });
    await expect(apiDownload("/backup")).resolves.toEqual({ blob, filename });
  });
  it("reads endpoint and token for each request", async () => {
    respond({ code: 0, data: null });
    setPanelAddress("https://first.example");
    localStorage.setItem("token", "one");
    await apiPost("/test");
    setPanelAddress("https://second.example");
    localStorage.setItem("token", "two");
    await apiPost("/test");
    expect(adapter.mock.calls[0][0]).toMatchObject({ baseURL: "https://first.example/api/v1/" });
    expect(adapter.mock.calls[0][0].headers.Authorization).toBe("one");
    expect(adapter.mock.calls[1][0]).toMatchObject({ baseURL: "https://second.example/api/v1/" });
    expect(adapter.mock.calls[1][0].headers.Authorization).toBe("two");
  });
  it("forwards AbortSignal and cancels the pending transport", async () => {
    adapter.mockImplementation(
      (config: InternalAxiosRequestConfig) =>
        new Promise((_resolve, reject) =>
          config.signal?.addEventListener?.("abort", () =>
            reject({ isAxiosError: true, code: "ERR_CANCELED", __CANCEL__: true, config }),
          ),
        ),
    );
    const controller = new AbortController();
    const pending = apiGet("/test", {}, { signal: controller.signal });
    await vi.waitFor(() => expect(adapter).toHaveBeenCalled());
    expect(adapter.mock.calls[0][0].signal).toBe(controller.signal);
    controller.abort();
    await expect(pending).rejects.toBeInstanceOf(ApiError);
  });
});

it("aborts React Query transport on key changes and unmount", async () => {
  adapter.mockImplementation(
    (config: InternalAxiosRequestConfig) =>
      new Promise((_resolve, reject) =>
        config.signal?.addEventListener?.("abort", () =>
          reject({ isAxiosError: true, code: "ERR_CANCELED", __CANCEL__: true, config }),
        ),
      ),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  const { rerender, unmount } = renderHook(
    ({ id }) =>
      useQuery({
        queryKey: ["resource", id],
        queryFn: ({ signal }) => apiGet(`/resource/${id}`, {}, { signal }),
      }),
    { initialProps: { id: 1 }, wrapper },
  );
  await waitFor(() => expect(adapter).toHaveBeenCalledTimes(1));
  const firstSignal = adapter.mock.calls[0][0].signal;
  rerender({ id: 2 });
  await waitFor(() => expect(adapter).toHaveBeenCalledTimes(2));
  expect(firstSignal.aborted).toBe(true);
  const secondSignal = adapter.mock.calls[1][0].signal;
  unmount();
  expect(secondSignal.aborted).toBe(true);
  client.clear();
});
