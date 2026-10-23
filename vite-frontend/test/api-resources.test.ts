import { beforeEach, describe, expect, it, vi } from "vitest";
import type { InternalAxiosRequestConfig } from "axios";
import {
  getAllUsers,
  getForwardList,
  getNodeList,
  getSpeedLimitList,
  getTunnelList,
  getUserTunnelList,
} from "../src/lib/api";
import type { RequestOptions } from "../src/lib/api/client";

const adapter = vi.hoisted(() => vi.fn());
vi.mock("axios", async (importOriginal) => {
  const actual = await importOriginal<typeof import("axios")>();
  const instance = actual.default.create({ adapter });
  return { ...actual, default: { ...actual.default, create: () => instance } };
});

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe("typed resource lists", () => {
  const resources = [
    ["/node/list", getNodeList, {}],
    ["/tunnel/list", getTunnelList, {}],
    ["/forward/list", getForwardList, {}],
    ["/user/list", getAllUsers, {}],
    ["/speed-limit/list", getSpeedLimitList, {}],
    [
      "/tunnel/user/list",
      (opts?: RequestOptions) => getUserTunnelList({ userId: 7 }, opts),
      { userId: 7 },
    ],
  ] as const;

  it.each(resources)("normalizes array and records responses at %s", async (path, read, body) => {
    const rows = [{ id: 1 }, { id: 2 }];
    const controller = new AbortController();
    for (const data of [rows, { records: rows }, [], { records: [] }]) {
      adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => ({
        data: { code: 0, data },
        headers: {},
        status: 200,
        statusText: "OK",
        config,
      }));
      await expect(read({ signal: controller.signal })).resolves.toEqual(
        Array.isArray(data) ? data : data.records,
      );
      expect(adapter).toHaveBeenLastCalledWith(
        expect.objectContaining({
          url: path,
          data: JSON.stringify(body),
          signal: controller.signal,
        }),
      );
    }
  });
});
