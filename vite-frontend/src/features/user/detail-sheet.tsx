import { useId, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { ErrorState } from "@/components/patterns/feedback";
import { FormField } from "@/components/patterns/form-field";
import { StatusPill } from "@/components/patterns/status-pill";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useConfirm } from "@/hooks/use-confirm";
import { createUser, deleteUser, getAllUsers, updateUser } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { invalidateFor, resourceKeys } from "@/lib/query-keys";
import { notify } from "@/lib/notify";
import { formatBytes } from "@/lib/format";
import type { User } from "@/lib/types";
import {
  dateTimestamp,
  expiryState,
  quotaValues,
  toDateInput,
  validateQuota,
  type FieldErrors,
  type UserValues,
} from "./model";
import { QuotaFields } from "./quota-fields";
import { focusFirstError } from "./model";
import { UserPermissions } from "./permissions";

export function UserDetailSheet({ user, onClose }: { user: User | null; onClose: () => void }) {
  const client = useQueryClient();
  const requestConfirm = useConfirm();
  const formId = useId();
  const [active, setActive] = useState(user);
  const initial: UserValues = {
    ...quotaValues(user ?? undefined),
    user: user?.user ?? "",
    pwd: "",
    status: user?.status !== 0,
  };
  const [values, setValues] = useState(initial);
  const [baseline, setBaseline] = useState(JSON.stringify(initial));
  const [tab, setTab] = useState("basic");
  const [permissionsDirty, setPermissionsDirty] = useState(false);
  const [permissionsBusy, setPermissionsBusy] = useState(false);
  const [permissionsCount, setPermissionsCount] = useState<number | null>(null);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [reveal, setReveal] = useState(false);
  const [createdUsername, setCreatedUsername] = useState<string | null>(null);
  const dirty = JSON.stringify(values) !== baseline || permissionsDirty;
  async function resolveCreated(username: string) {
    // 创建接口只返回消息；用户名有数据库唯一约束，以服务端列表中精确匹配的真实 ID 继续授权。
    const rows = await getAllUsers();
    client.setQueryData(resourceKeys.user.list(), rows);
    const created = rows.find(
      (row) => row.user === username && Number.isSafeInteger(row.id) && row.id > 0,
    );
    if (!created) throw new Error("用户已创建，但尚未读取到新账号，请重试加载用户");
    setActive(created);
    setCreatedUsername(null);
    setTab("permissions");
    const next = {
      ...quotaValues(created),
      user: created.user,
      pwd: "",
      status: created.status === 1,
    };
    setValues(next);
    setBaseline(JSON.stringify(next));
    notify.success("用户已创建，请继续分配隧道权限");
  }
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busyRef.current) return;
    const next = validateQuota(values);
    if (!values.user.trim()) next.user = "请输入用户名";
    if (!active && !createdUsername && !values.pwd) next.pwd = "请输入初始密码";
    setErrors(next);
    if (Object.keys(next).length && !createdUsername) {
      focusFirstError(event.currentTarget);
      return;
    }
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      if (createdUsername) {
        await resolveCreated(createdUsername);
        return;
      }
      const payload = {
        id: active?.id,
        user: active?.user ?? values.user.trim(),
        pwd: values.pwd || undefined,
        status: values.status ? 1 : 0,
        flow: Number(values.flow),
        num: Number(values.num),
        // 日期未变时保留原始毫秒时间；仅新建或主动改日期沿用日末失效策略。
        expTime:
          active?.expTime != null && values.expiry === toDateInput(active.expTime)
            ? active.expTime
            : dateTimestamp(values.expiry)!,
        flowResetTime: Number(values.reset),
      };
      if (active) {
        await updateUser(payload);
        setActive({ ...active, ...payload, id: active.id });
        const saved = { ...values, pwd: "" };
        setValues(saved);
        setBaseline(JSON.stringify(saved));
        await invalidateFor(client, "user.update");
        notify.success("用户信息已保存");
      } else {
        await createUser(payload);
        setCreatedUsername(payload.user);
        await invalidateFor(client, "user.create");
        await resolveCreated(payload.user);
      }
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }
  async function remove() {
    if (!active || busyRef.current) return;
    if (
      !(await requestConfirm({
        title: "删除用户",
        description: `删除 ${active.user} 后，该账号的全部隧道权限与转发规则将一并移除，且无法恢复`,
        requireText: active.user,
        confirmText: "删除用户",
        tone: "danger",
      }))
    )
      return;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      await deleteUser(active.id);
      await invalidateFor(client, "user.delete");
      notify.success("用户已删除");
      onClose();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }
  return (
    <EntitySheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={active ? `${active.name || active.user} (@${active.user})` : "新增用户"}
      description={
        active ? (
          <span className="flex flex-wrap items-center gap-2">
            <StatusPill
              tone={active.status === 1 ? "good" : "neutral"}
              hollow={active.status !== 1}
              label={active.status === 1 ? "正常" : "已禁用"}
            />
            <span className="font-mono text-caption text-body">
              {formatBytes((active.inFlow ?? 0) + (active.outFlow ?? 0))} /{" "}
              {formatBytes(active.flow * 1024 ** 3)}
            </span>
            <StatusPill {...expiryState(active.expTime)} />
          </span>
        ) : (
          "创建账号后继续分配隧道权限"
        )
      }
      dirty={dirty}
      submitting={busy || permissionsBusy}
      footer={({ requestClose, submitting }) => (
        <div className="flex w-full flex-wrap items-center gap-2">
          {active && tab === "basic" && (
            <Button
              type="button"
              variant="danger"
              disabled={submitting}
              onClick={() => void remove()}
            >
              删除用户
            </Button>
          )}
          <div className="ml-auto flex flex-wrap gap-2">
            <Button type="button" variant="outline" disabled={submitting} onClick={requestClose}>
              {tab === "permissions" ? "关闭" : "取消"}
            </Button>
            {tab === "basic" && (
              <Button type="submit" form={formId} loading={submitting}>
                {createdUsername ? "重试加载用户" : active ? "保存修改" : "创建用户"}
              </Button>
            )}
          </div>
        </div>
      )}
    >
      {error && <ErrorState error={error} />}
      {createdUsername && (
        <p role="status" className="rounded-md border border-hairline bg-canvas-soft p-3 text-body">
          账号 {createdUsername} 已创建。加载成功后可分配权限；重试不会重复创建
        </p>
      )}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList aria-label="用户详情">
          <TabsTrigger value="basic" disabled={busy || permissionsBusy}>
            基本信息
          </TabsTrigger>
          {active && (
            <TabsTrigger value="permissions" disabled={busy || permissionsBusy}>
              隧道权限{permissionsCount !== null ? ` ${permissionsCount}` : ""}
            </TabsTrigger>
          )}
        </TabsList>
        <TabsContent value="basic" className="pt-4">
          <form id={formId} onSubmit={save} noValidate className="space-y-4">
            <FormField
              label="用户名"
              htmlFor="user-name"
              required
              error={errors.user}
              hint={active ? "用户名不可修改" : "用于登录，新账号用户名必须唯一"}
            >
              <Input
                value={values.user}
                disabled={Boolean(active || createdUsername) || busy}
                autoComplete="off"
                onChange={(e) => setValues({ ...values, user: e.target.value })}
              />
            </FormField>
            {active?.name && (
              <p className="text-caption text-mute">昵称：{active.name}（当前后端不支持编辑）</p>
            )}
            <FormField
              label={active ? "新密码" : "初始密码"}
              htmlFor="user-password"
              required={!active}
              error={errors.pwd}
              hint={
                active ? "留空保留当前密码；修改后该用户已有登录会话失效" : "请为新账号设置初始密码"
              }
            >
              {(control) => (
                <div className="relative">
                  <Input
                    {...control}
                    type={reveal ? "text" : "password"}
                    value={values.pwd}
                    autoComplete="new-password"
                    disabled={Boolean(createdUsername) || busy}
                    className="pr-20"
                    onChange={(e) => setValues({ ...values, pwd: e.target.value })}
                  />
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="absolute right-0 top-0 h-9"
                    aria-label={reveal ? "隐藏密码" : "显示密码"}
                    aria-pressed={reveal}
                    onClick={() => setReveal(!reveal)}
                  >
                    {reveal ? "隐藏" : "显示"}
                  </Button>
                </div>
              )}
            </FormField>
            <QuotaFields
              prefix="user"
              values={values}
              errors={errors}
              onChange={(next) => setValues({ ...values, ...next })}
              disabled={Boolean(createdUsername) || busy}
            />
            <FormField label="账号状态" htmlFor="user-status" hint="禁用后无法登录与使用转发">
              <Switch
                checked={values.status}
                disabled={Boolean(createdUsername) || busy}
                onCheckedChange={(status) => setValues({ ...values, status })}
              />
            </FormField>
          </form>
        </TabsContent>
        {active && (
          <TabsContent value="permissions" forceMount className="pt-4 data-[state=inactive]:hidden">
            <UserPermissions
              user={active}
              onDirtyChange={setPermissionsDirty}
              onBusyChange={setPermissionsBusy}
              onCountChange={setPermissionsCount}
            />
          </TabsContent>
        )}
      </Tabs>
    </EntitySheet>
  );
}
