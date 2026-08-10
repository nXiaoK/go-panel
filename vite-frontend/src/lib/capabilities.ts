/**
 * Route-level capability decisions. Administrator is role_id 0; the backend
 * remains authoritative for authorization — this only decides what the UI
 * offers and pre-empts obviously disallowed navigation.
 */

export const ADMIN_ROLE_ID = 0;

/** Path prefixes only administrators may open. */
export const ADMIN_PATH_PREFIXES = [
  "/tunnel",
  "/limit",
  "/node",
  "/user",
  "/subscription",
  "/config",
] as const;

function isAdminPath(path: string): boolean {
  return ADMIN_PATH_PREFIXES.some((prefix) => path === prefix || path.startsWith(prefix + "/"));
}

export function isAdministrator(roleID: number | null | undefined): boolean {
  return roleID === ADMIN_ROLE_ID;
}

export function canAccessPath(roleID: number | null | undefined, path: string): boolean {
  if (isAdministrator(roleID)) return true;
  return !isAdminPath(path);
}
