import { apiPost, type RequestOptions } from "@/lib/api/client";
import type { UserPackage } from "@/lib/types";
export const getUserPackageInfo = (
  data: { range?: string; tunnelId?: number } = {},
  opts?: RequestOptions,
): Promise<UserPackage> => apiPost("/user/package", data, opts);
export { updatePassword } from "@/features/auth/api";
