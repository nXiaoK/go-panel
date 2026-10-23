import { apiPost, type RequestOptions } from "@/lib/api/client";
import type { PasswordUpdate } from "@/lib/types";
export interface LoginData {
  username: string;
  password: string;
}
export interface LoginResponse {
  token: string;
  role_id: number;
  name: string;
  requirePasswordChange?: boolean;
}
export const login = (data: LoginData, opts?: RequestOptions): Promise<LoginResponse> =>
  apiPost("/user/login", data, { ...opts, public: true });
export const updatePassword = (data: PasswordUpdate, opts?: RequestOptions): Promise<string> =>
  apiPost("/user/updatePassword", data, opts);
