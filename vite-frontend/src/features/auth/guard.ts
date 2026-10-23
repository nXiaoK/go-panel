import { canAccessPath } from "@/lib/capabilities";
import { passwordChangeRedirect, readSession } from "@/lib/session";
import { redirect } from "@tanstack/react-router";
export const requireSession = ({ location }: { location: { pathname: string } }) => {
  if (typeof window === "undefined") return;
  const { token, roleId } = readSession();
  if (!token) {
    throw redirect({ to: "/login" });
  }
  // 持久改密要求优先于角色导航；资料页允许访问，刷新或手动输入其他路径也无法绕过。
  if (passwordChangeRedirect(location.pathname)) {
    throw redirect({ to: "/profile", search: { forcePassword: 1 } });
  }
  // The backend stays authoritative; this only stops a normal user from
  // opening an administrator route they can never use.
  if (!canAccessPath(roleId, location.pathname)) {
    throw redirect({ to: "/" });
  }
};
