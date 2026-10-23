import { apiPost, type RequestOptions } from "@/lib/api/client";
import type { User, UserWrite } from "@/lib/types";
export const createUser = (data: UserWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/user/create", data, opts);
export const updateUser = (data: UserWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/user/update", data, opts);
export const getAllUsers = async (opts?: RequestOptions): Promise<User[]> => {
  const data = await apiPost<User[] | { records: User[] }>("/user/list", {}, opts);
  return Array.isArray(data) ? data : (data?.records ?? []);
};
export const deleteUser = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/user/delete", { id }, opts);
export const resetUserFlow = (
  data: { id: number; type: number },
  opts?: RequestOptions,
): Promise<null> => apiPost("/user/reset", data, opts);
