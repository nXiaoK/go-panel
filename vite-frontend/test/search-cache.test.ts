import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { forwardKeys } from "../src/features/forward/queries";
import { invalidateFor } from "../src/lib/query-keys";

afterEach(() => vi.restoreAllMocks());
describe("搜索共享实体缓存", () => {
  it("CRUD失效后重新加载搜索与页面共用的实体", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { staleTime: 60_000, retry: false } },
    });
    const loader = vi.fn().mockResolvedValue([{ id: 1 }]);
    const load = () => client.fetchQuery({ queryKey: forwardKeys.list(), queryFn: loader });
    await load();
    await invalidateFor(client, "forward.update");
    await load();
    expect(loader).toHaveBeenCalledTimes(2);
    client.clear();
  });
  it("同一实体键并发加载只调用一次并向两个消费者返回同一结果", async () => {
    const client = new QueryClient();
    let resolve!: (value: string[]) => void;
    const loader = vi.fn(
      () =>
        new Promise<string[]>((done) => {
          resolve = done;
        }),
    );
    const options = { queryKey: forwardKeys.list(), queryFn: loader };
    const first = client.fetchQuery(options);
    const second = client.fetchQuery(options);
    expect(loader).toHaveBeenCalledTimes(1);
    resolve(["共享行"]);
    await expect(first).resolves.toEqual(["共享行"]);
    await expect(second).resolves.toEqual(["共享行"]);
    client.clear();
  });
  it("同一实体键共用在途请求，清缓存后迟到响应不能恢复旧账号数据", async () => {
    const client = new QueryClient();
    let resolve!: (value: string[]) => void;
    const loader = vi.fn(
      () =>
        new Promise<string[]>((done) => {
          resolve = done;
        }),
    );
    const options = { queryKey: forwardKeys.list(), queryFn: loader };
    const first = client.fetchQuery(options);
    const second = client.fetchQuery(options);
    expect(loader).toHaveBeenCalledTimes(1);
    client.clear();
    resolve(["旧账号"]);
    await expect(first).rejects.toBeDefined();
    await expect(second).rejects.toBeDefined();
    expect(client.getQueryData(forwardKeys.list())).toBeUndefined();
  });
  it("缓存过期后重新加载，失败请求不会形成成功缓存", async () => {
    let now = 1000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    const client = new QueryClient({
      defaultOptions: { queries: { staleTime: 1000, retry: false } },
    });
    const loader = vi
      .fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(["ok"])
      .mockResolvedValueOnce(["fresh"]);
    const load = () => client.fetchQuery({ queryKey: forwardKeys.list(), queryFn: loader });
    await expect(load()).rejects.toThrow("boom");
    expect(client.getQueryData(forwardKeys.list())).toBeUndefined();
    await expect(load()).resolves.toEqual(["ok"]);
    await expect(load()).resolves.toEqual(["ok"]);
    expect(loader).toHaveBeenCalledTimes(2);
    now = 2001;
    await expect(load()).resolves.toEqual(["fresh"]);
    expect(loader).toHaveBeenCalledTimes(3);
    client.clear();
  });
});
