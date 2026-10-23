import { toast } from "sonner";

import { readPreferences } from "@/lib/preferences";

/** 操作通知默认开启；pref_notify=0 仅抑制成功、信息和加载，错误/警告始终显示，避免隐藏安全与失败消息。 */
export const notify = {
  promise: notifyPromise,
  success(message: string, options?: Parameters<typeof toast.success>[1]) {
    if (!readPreferences().notify) return;
    if (options === undefined) toast.success(message);
    else toast.success(message, options);
  },
  info(message: string, options?: Parameters<typeof toast.info>[1]) {
    if (!readPreferences().notify) return;
    if (options === undefined) toast.info(message);
    else toast.info(message, options);
  },
  warning(message: string, options?: Parameters<typeof toast.warning>[1]) {
    if (options === undefined) toast.warning(message);
    else toast.warning(message, options);
  },
  error(message: string, options?: Parameters<typeof toast.error>[1]) {
    if (options === undefined) toast.error(message);
    else toast.error(message, options);
  },
};

export type PromiseNotification<T> = {
  loading: string;
  success: string | ((value: T) => string);
  error: string | ((error: unknown) => string);
};

/** 长操作的加载与成功提示遵循偏好；失败始终显示，并向调用方保留 rejection。 */
async function notifyPromise<T>(
  operation: Promise<T> | (() => Promise<T>),
  messages: PromiseNotification<T>,
): Promise<T> {
  const loadingId = readPreferences().notify ? toast.loading(messages.loading) : undefined;
  try {
    const value = await (typeof operation === "function" ? operation() : operation);
    notify.success(
      typeof messages.success === "function" ? messages.success(value) : messages.success,
    );
    return value;
  } catch (error) {
    notify.error(typeof messages.error === "function" ? messages.error(error) : messages.error);
    throw error;
  } finally {
    if (loadingId !== undefined) toast.dismiss(loadingId);
  }
}
