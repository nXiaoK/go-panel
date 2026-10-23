import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { apiPost } from "@/lib/api/client";
import { resourceKeys } from "@/lib/query-keys";
import { resourceSearch } from "@/features/search/route-search";
import { userSearch } from "./model";
import { UserPage } from "./page";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
let client: QueryClient;
const alice = {
  id: 1,
  user: "alice",
  name: "Alice",
  status: 1,
  flow: 100,
  num: 10,
  expTime: new Date("2030-12-31T13:24:56.789").getTime(),
  flowResetTime: 1,
};
const permission = {
  id: 3,
  userId: 1,
  tunnelId: 2,
  tunnelName: "旧配额隧道",
  flow: 10,
  num: 1,
  status: 1,
  expTime: alice.expTime,
  flowResetTime: 0,
};
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
  vi.stubGlobal(
    "matchMedia",
    vi.fn(
      () =>
        ({
          matches: false,
          addEventListener: vi.fn(),
          removeEventListener: vi.fn(),
        }) as unknown as MediaQueryList,
    ),
  );
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  Object.defineProperty(HTMLElement.prototype, "hasPointerCapture", {
    configurable: true,
    value: () => false,
  });
  Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", {
    configurable: true,
    value: () => {},
  });
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: () => {},
  });
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/user/list") return [alice];
    if (path === "/tunnel/list")
      return [
        { id: 2, name: "已有隧道", type: 1, status: 1 },
        { id: 4, name: "隧道 B", type: 1, status: 1 },
        { id: 5, name: "隧道 C", type: 1, status: 1 },
      ];
    if (path === "/speed-limit/list")
      return [
        { id: 10, tunnelId: 4, name: "B 限速", speed: 10 },
        { id: 11, tunnelId: 5, name: "C 限速", speed: 20 },
      ];
    if (path === "/tunnel/user/list") return [permission];
    return "成功";
  });
});
afterEach(() => {
  cleanup();
  client?.clear();
  localStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.resetAllMocks();
});
async function show(url = "/user") {
  const root = createRootRoute();
  const route = createRoute({
    getParentRoute: () => root,
    path: "/user",
    validateSearch: (search) => resourceSearch(userSearch(search)),
    component: UserPage,
  });
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        <RouterProvider router={router} />
      </ConfirmProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(router.state.status).toBe("idle"));
  return router;
}
async function openDetail() {
  fireEvent.click(await screen.findByText("@alice"));
  return screen.findByRole("dialog");
}
async function openPermissions(expectRows = true) {
  await openDetail();
  fireEvent.mouseDown(screen.getByRole("tab", { name: /^隧道权限(?: \d+)?$/ }), {
    button: 0,
    ctrlKey: false,
  });
  fireEvent.click(screen.getByRole("tab", { name: /^隧道权限(?: \d+)?$/ }));
  if (expectRows) return screen.findByRole("button", { name: "禁用权限" });
  return screen.findByRole("tabpanel", { name: /^隧道权限(?: \d+)?$/ });
}
async function openSelect(name: string) {
  fireEvent.keyDown(screen.getByRole("combobox", { name }), { key: "ArrowDown" });
  await screen.findByRole("listbox");
}

it("授权有缓存后重开失败，真实页面不允许使用旧行覆盖新配额或新增授权", async () => {
  let permissionsLoad = 0;
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/user/list") return [alice];
    if (path === "/tunnel/list") return [{ id: 2, name: "测试隧道", type: 1, status: 1 }];
    if (path === "/tunnel/user/list") {
      permissionsLoad += 1;
      if (permissionsLoad > 1) throw new Error("权限重取失败");
      return [permission];
    }
    return [];
  });
  await show();
  await openPermissions();
  expect(screen.getByRole("button", { name: "禁用权限" })).toBeEnabled();
  fireEvent.click(screen.getAllByRole("button", { name: /^关闭$/ })[0]);
  await openPermissions(false);
  await waitFor(() => expect(permissionsLoad).toBe(2));
  await waitFor(() => expect(screen.getByRole("button", { name: /^添加授权$/ })).toBeDisabled());
  expect(screen.queryByRole("button", { name: "禁用权限" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /^添加授权$/ }));
  expect(
    vi
      .mocked(apiPost)
      .mock.calls.some(([path]) => /tunnel\/user\/(update|assign|remove)$/.test(path)),
  ).toBe(false);
});
it("搜索空态提供清除筛选，URL状态清除后恢复列表且不重取每个搜索词", async () => {
  const router = await show("/user?q=missing&status=1&expiry=all");
  expect(await screen.findByText("没有匹配的用户")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
  expect(await screen.findByText("@alice")).toBeInTheDocument();
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ q: "", status: "all", expiry: "all" }),
  );
  expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/user/list")).toHaveLength(1);
  expect(vi.mocked(apiPost).mock.calls.some(([path]) => path === "/forward/list")).toBe(false);
});
it("新建缺少到期日期时定位字段错误，不发创建请求", async () => {
  await show("/user?create=1");
  const dialog = await screen.findByRole("dialog");
  fireEvent.change(within(dialog).getByLabelText(/用户名/), { target: { value: "new-user" } });
  fireEvent.change(within(dialog).getByLabelText(/初始密码/), { target: { value: "password" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "创建用户" }));
  expect(await screen.findByText("请选择到期日期")).toBeInTheDocument();
  expect(within(dialog).getByLabelText(/到期日期/, { selector: "input" })).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await waitFor(() =>
    expect(within(dialog).getByLabelText(/到期日期/, { selector: "input" })).toHaveFocus(),
  );
  expect(vi.mocked(apiPost).mock.calls.some(([path]) => path === "/user/create")).toBe(false);
});
it("创建成功使用重取列表的真实id进入权限，加载失败重试不重复创建", async () => {
  let created = false;
  let allowResolve = false;
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/user/create") {
      created = true;
      return "用户创建成功";
    }
    if (path === "/user/list") {
      if (created && !allowResolve) {
        throw new Error("列表暂时失败");
      }
      return created ? [alice, { ...alice, id: 734, user: "new-user", name: undefined }] : [alice];
    }
    if (path === "/tunnel/user/list") return [];
    return [];
  });
  await show();
  fireEvent.click(screen.getByRole("button", { name: "新增用户" }));
  fireEvent.change(screen.getByLabelText(/用户名/), { target: { value: "new-user" } });
  fireEvent.change(screen.getByLabelText(/初始密码/), { target: { value: "password" } });
  fireEvent.change(screen.getByLabelText(/到期日期/, { selector: "input" }), {
    target: { value: "2030-12-31" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建用户" }));
  const retry = await screen.findByRole("button", { name: "重试加载用户" });
  expect(screen.getByText(/已创建。加载成功后/)).toBeInTheDocument();
  allowResolve = true;
  fireEvent.click(retry);
  await waitFor(() =>
    expect(screen.getByRole("tab", { name: /^隧道权限(?: \d+)?$/ })).toHaveAttribute(
      "data-state",
      "active",
    ),
  );
  await waitFor(() =>
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.some(
          ([path, body]) =>
            path === "/tunnel/user/list" && (body as { userId: number }).userId === 734,
        ),
    ).toBe(true),
  );
  expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/user/create")).toHaveLength(1);
  expect(screen.queryByRole("tab", { name: "流量" })).toBeNull();
});
it("已有授权不在选项中，限速随隧道切换，抽屉没有嵌套form", async () => {
  await show();
  await openPermissions();
  await openSelect("隧道（必填）");
  expect(screen.queryByRole("option", { name: "已有隧道" })).toBeNull();
  fireEvent.click(screen.getByRole("option", { name: "隧道 B" }));
  await openSelect("限速规则");
  expect(screen.getByRole("option", { name: /B 限速/ })).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: /C 限速/ })).toBeNull();
  fireEvent.keyDown(screen.getByRole("listbox"), { key: "Escape" });
  expect(document.querySelector("form form")).toBeNull();
});
it("删除必须输入准确用户名，取消不写入", async () => {
  await show();
  await openDetail();
  fireEvent.click(screen.getByRole("button", { name: "删除用户" }));
  const requestConfirm = await screen.findByRole("alertdialog");
  const remove = within(requestConfirm).getByRole("button", { name: "删除用户" });
  expect(remove).toBeDisabled();
  fireEvent.change(within(requestConfirm).getByRole("textbox"), { target: { value: "Alice" } });
  expect(remove).toBeDisabled();
  fireEvent.change(within(requestConfirm).getByRole("textbox"), { target: { value: "alice" } });
  expect(remove).toBeEnabled();
  fireEvent.click(within(requestConfirm).getByRole("button", { name: "取消" }));
  expect(vi.mocked(apiPost).mock.calls.some(([path]) => path === "/user/delete")).toBe(false);
});
it("确认期间重取失败时拒绝旧授权写入", async () => {
  await show();
  await openPermissions();
  fireEvent.click(screen.getByRole("button", { name: /^重置流量$/ }));
  const requestConfirm = await screen.findByRole("alertdialog");
  await act(async () => {
    client.setQueryData(resourceKeys.user.tunnels(1), [permission]);
    await client.cancelQueries({ queryKey: resourceKeys.user.tunnels(1) });
    client
      .getQueryCache()
      .find({ queryKey: resourceKeys.user.tunnels(1) })
      ?.setState({ status: "error", error: new Error("重取失败") });
  });
  fireEvent.click(within(requestConfirm).getByRole("button", { name: "重置流量" }));
  await waitFor(() =>
    expect(screen.getByText("授权数据已变化，请等待刷新完成后重试")).toBeInTheDocument(),
  );
  expect(vi.mocked(apiPost).mock.calls.some(([path]) => path === "/user/reset")).toBe(false);
});
it("URL focus清筛选且定位数字id，非法筛选回退，普通用户不请求管理接口", async () => {
  const router = await show("/user?focus=1&q=missing&status=9&expiry=invalid");
  expect(await screen.findByText("@alice")).toBeInTheDocument();
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      q: "",
      status: "all",
      expiry: "all",
      focus: 1,
    }),
  );
  cleanup();
  client?.clear();
  vi.mocked(apiPost).mockClear();
  localStorage.setItem("role_id", "1");
  const root = createRootRoute();
  const route = createRoute({ getParentRoute: () => root, path: "/user", component: UserPage });
  const ordinary = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: ["/user"] }),
  });
  await ordinary.load();
  render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        <RouterProvider router={ordinary} />
      </ConfirmProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("需要管理员权限")).toBeInTheDocument();
  expect(vi.mocked(apiPost)).not.toHaveBeenCalled();
});

it("零流量用户和授权均显示用尽，不显示不限流量", async () => {
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/user/list") return [{ ...alice, flow: 0 }];
    if (path === "/tunnel/user/list") return [{ ...permission, flow: 0 }];
    return [];
  });
  await show();
  expect(await screen.findByLabelText("alice流量配额")).toHaveTextContent("0 B / 0 GB 已用尽");
  await openPermissions();
  expect(screen.getByLabelText("旧配额隧道流量配额")).toHaveTextContent("0 B / 0 GB 已用尽");
  expect(screen.queryByText("不限流量")).toBeNull();
});

it("status=0数字书签在重载后仍过滤禁用用户", async () => {
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/user/list" ? [alice, { ...alice, id: 8, user: "disabled", status: 0 }] : [],
  );
  const router = await show("/user?status=0");
  expect(await screen.findByText("@disabled")).toBeInTheDocument();
  expect(screen.queryByText("@alice")).toBeNull();
  await act(async () => {
    await router.load();
  });
  expect(router.state.location.search.status).toBe("0");
  expect(screen.getByRole("combobox", { name: "用户状态" })).toHaveTextContent("已禁用");
});

it("授权单列新增与行内配额保存发送当前用户和真实授权id", async () => {
  await show();
  await openPermissions();
  await openSelect("隧道（必填）");
  fireEvent.click(screen.getByRole("option", { name: "隧道 B" }));
  fireEvent.change(screen.getByLabelText(/到期日期/, { selector: "input" }), {
    target: { value: "2030-12-31" },
  });
  await openSelect("限速规则");
  fireEvent.click(screen.getByRole("option", { name: /B 限速/ }));
  fireEvent.click(screen.getByRole("button", { name: /^添加授权$/ }));
  await waitFor(() =>
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.some(
          ([path, body]) =>
            path === "/tunnel/user/assign" &&
            (body as { userId: number; tunnelId: number; speedId: number }).userId === 1 &&
            (body as { tunnelId: number }).tunnelId === 4 &&
            (body as { speedId: number }).speedId === 10,
        ),
    ).toBe(true),
  );
  await waitFor(() => expect(screen.getByRole("button", { name: "编辑配额" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "编辑配额" }));
  const edit = screen.getByRole("region", { name: "编辑授权配额" });
  expect(edit.closest("article")).toHaveTextContent("旧配额隧道");
  fireEvent.change(within(edit).getByLabelText(/流量配额/), { target: { value: "25" } });
  fireEvent.click(within(edit).getByRole("button", { name: "保存授权配额" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.some(
          ([path, body]) =>
            path === "/tunnel/user/update" &&
            (body as { id: number; flow: number; status: number }).id === 3 &&
            (body as { flow: number }).flow === 25 &&
            (body as { status: number }).status === 1,
        ),
    ).toBe(true),
  );
});

it("用户仅改配额保留原毫秒到期时刻，主动改日期才转换到日末", async () => {
  await show();
  const detail = await openDetail();
  const fields = within(screen.getByRole("tabpanel", { name: "基本信息" }));
  fireEvent.change(fields.getByLabelText(/流量配额/), { target: { value: "125" } });
  fireEvent.click(within(detail).getByRole("button", { name: "保存修改" }));
  await waitFor(() => {
    const writes = vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/user/update");
    expect(writes).toHaveLength(1);
    expect(writes[0][1]).toMatchObject({ id: alice.id, flow: 125, expTime: alice.expTime });
  });
  await waitFor(() =>
    expect(within(detail).getByRole("button", { name: "保存修改" })).toBeEnabled(),
  );
  fireEvent.change(fields.getByLabelText(/到期日期/, { selector: "input" }), {
    target: { value: "2031-01-02" },
  });
  fireEvent.click(within(detail).getByRole("button", { name: "保存修改" }));
  await waitFor(() => {
    const writes = vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/user/update");
    expect(writes).toHaveLength(2);
    expect(writes[1][1]).toMatchObject({ expTime: new Date("2031-01-02T23:59:59").getTime() });
  });
});

it("授权仅改配额保留重取后的精确到期时刻，主动改日期才转换到日末", async () => {
  await show();
  await openPermissions();
  fireEvent.click(screen.getByRole("button", { name: "编辑配额" }));
  const edit = screen.getByRole("region", { name: "编辑授权配额" });
  const refreshedExpiry = new Date("2031-01-01T09:12:34.567").getTime();
  await act(async () => {
    client.setQueryData(resourceKeys.user.tunnels(alice.id), [
      { ...permission, expTime: refreshedExpiry },
    ]);
  });
  fireEvent.change(within(edit).getByLabelText(/流量配额/), { target: { value: "35" } });
  fireEvent.click(within(edit).getByRole("button", { name: "保存授权配额" }));
  await waitFor(() => {
    const writes = vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/tunnel/user/update");
    expect(writes).toHaveLength(1);
    expect(writes[0][1]).toMatchObject({ id: permission.id, flow: 35, expTime: refreshedExpiry });
  });
  await waitFor(() => expect(screen.getByRole("button", { name: "编辑配额" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "编辑配额" }));
  const nextEdit = screen.getByRole("region", { name: "编辑授权配额" });
  fireEvent.change(within(nextEdit).getByLabelText(/到期日期/, { selector: "input" }), {
    target: { value: "2031-02-03" },
  });
  fireEvent.click(within(nextEdit).getByRole("button", { name: "保存授权配额" }));
  await waitFor(() => {
    const writes = vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/tunnel/user/update");
    expect(writes).toHaveLength(2);
    expect(writes[1][1]).toMatchObject({ expTime: new Date("2031-02-03T23:59:59").getTime() });
  });
});

it("到期筛选包含0与负值，不把null误报过期，清除后日期不冒称未设置", async () => {
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/user/list"
      ? [
          { ...alice, id: 1, user: "zero", expTime: 0 },
          { ...alice, id: 2, user: "negative", expTime: -1 },
          { ...alice, id: 3, user: "unlimited", expTime: null },
        ]
      : [],
  );
  await show("/user?expiry=expired");
  await screen.findByText("@zero");
  expect(screen.getByText("@negative")).toBeInTheDocument();
  expect(screen.queryByText("@unlimited")).toBeNull();
  for (const name of ["zero", "negative"]) {
    const row = screen.getByText(`@${name}`).closest("tr")!;
    expect(within(row).getByText("已到期")).toBeInTheDocument();
  }
});
