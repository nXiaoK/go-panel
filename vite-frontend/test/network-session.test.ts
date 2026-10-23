import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiGet, apiPost, apiUpload, apiDownload } from "../src/lib/api/client";
import { configureSessionForTest } from "../src/lib/session";
import type { InternalAxiosRequestConfig } from "axios";

const adapter = vi.hoisted(() => vi.fn());
vi.mock("axios", async (importOriginal) => {
  const actual = await importOriginal<typeof import("axios")>();
  const instance = actual.default.create({ adapter });
  return { ...actual, default: { ...actual.default, create: () => instance } };
});

let navigate = vi.fn();
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem("token", "secret");
  localStorage.setItem("role_id", "1");
  localStorage.setItem("name", "alice");
  navigate = vi.fn();
  configureSessionForTest({ storage: localStorage, navigate, pathname: "/forward" });
});

const requests = {
  get: (path: string) => apiGet(path),
  post: (path: string) => apiPost(path),
  postWithTimeout: (path: string) => apiPost(path, {}, { timeoutMs: 100 }),
  upload: (path: string) => apiUpload(path, new FormData()),
  download: (path: string) => apiDownload(path),
};
function respond(data: { code: number; msg: string }, download = false) {
  const response = download
    ? {
        data: { text: async () => JSON.stringify(data) },
        headers: { "content-type": "application/json" },
      }
    : { data };
  adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => ({
    ...response,
    headers: "headers" in response ? response.headers : {},
    status: 200,
    statusText: "OK",
    config,
  }));
}

describe.each(Object.entries(requests))("%s session failures", (method, request) => {
  it.each(["token已失效", "账号已过期", "用户不存在"])(
    "invalidates business 401: %s",
    async (msg) => {
      respond({ code: 401, msg }, method === "download");
      await expect(request("/secure")).rejects.toThrow();
      expect(localStorage.getItem("token")).toBeNull();
      expect(navigate).toHaveBeenCalledExactlyOnceWith("/login?reason=expired");
    },
  );
  it.each([4031, 403])("invalidates disabled accounts with code %s", async (code) => {
    respond({ code, msg: "账号已被禁用" }, method === "download");
    await expect(request("/secure")).rejects.toThrow();
    expect(localStorage.getItem("token")).toBeNull();
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/login?reason=disabled");
  });
  it.each([4032, 403])("requires password change with code %s", async (code) => {
    respond({ code, msg: "请先修改默认密码" }, method === "download");
    await expect(request("/secure")).rejects.toThrow();
    expect(localStorage.getItem("token")).toBe("secret");
    expect(localStorage.getItem("must_change_pwd")).toBe("1");
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/account?tab=security&forcePassword=1");
  });
  it("preserves session on an administrator permission error", async () => {
    respond({ code: 403, msg: "权限不足，仅管理员可操作" }, method === "download");
    await request("/secure").catch(() => undefined);
    expect(localStorage.getItem("token")).toBe("secret");
    expect(navigate).not.toHaveBeenCalled();
  });
  it("excludes public login business and HTTP 401 failures", async () => {
    respond({ code: 401, msg: "账号或密码错误" }, method === "download");
    await request("/user/login").catch(() => undefined);
    const error = { isAxiosError: true, response: { status: 401 }, message: "Unauthorized" };
    adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => {
      throw { ...error, config };
    });
    await request("/user/login").catch(() => undefined);
    expect(localStorage.getItem("token")).toBe("secret");
    expect(navigate).not.toHaveBeenCalled();
  });
  it.each([4031, 4032])("prioritizes HTTP 401 over a mismatched body code %s", async (code) => {
    const body = { code, msg: "代理错误体" };
    const data = method === "download" ? { text: async () => JSON.stringify(body) } : body;
    const error = { isAxiosError: true, response: { status: 401, data } };
    adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => {
      throw { ...error, config };
    });
    await expect(request("/secure")).rejects.toThrow();
    expect(localStorage.getItem("token")).toBeNull();
    expect(localStorage.getItem("must_change_pwd")).toBeNull();
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/login?reason=expired");
  });
  it("invalidates protected HTTP 401 failures", async () => {
    const error = { isAxiosError: true, response: { status: 401 }, message: "Unauthorized" };
    adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => {
      throw { ...error, config };
    });
    await expect(request("/secure")).rejects.toThrow();
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/login?reason=expired");
  });
});
it("redirects once for three simultaneous protected failures", async () => {
  respond({ code: 401, msg: "token已失效" });
  await Promise.allSettled([apiGet("/one"), apiPost("/two"), apiPost("/three")]);
  expect(navigate).toHaveBeenCalledTimes(1);
});
it("classifies non-2xx JSON download bodies", async () => {
  adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => {
    throw {
      config,
      isAxiosError: true,
      response: {
        status: 403,
        data: { text: async () => JSON.stringify({ code: 4032, msg: "改密" }) },
      },
    };
  });
  await expect(apiDownload("/secure")).rejects.toThrow();
  expect(navigate).toHaveBeenCalledExactlyOnceWith("/account?tab=security&forcePassword=1");
});
