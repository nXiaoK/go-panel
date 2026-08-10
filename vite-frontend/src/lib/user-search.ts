/**
 * Client-side user filtering. The user list is loaded once and filtered in the
 * browser, so typing in the search box never issues a network request (which
 * previously reloaded the whole list per keystroke).
 */
export function filterUsers<T extends { user?: string | null }>(users: T[], query: string): T[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return users;
  return users.filter((u) => (u.user ?? "").toLowerCase().includes(needle));
}
