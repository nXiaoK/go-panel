# Routes

TanStack Start uses **file-based routing**. Every `.tsx` file in this directory
defines a route. Do **not** create `src/pages/`, `src/routes/_app/index.tsx`, or
`app/layout.tsx` — those are Next.js / Remix conventions. The only root layout
is `src/routes/__root.tsx`.

## Conventions

| File                     | URL                                                     |
| ------------------------ | ------------------------------------------------------- |
| `index.tsx`              | `/`                                                     |
| `about.tsx`              | `/about`                                                |
| `users/index.tsx`        | `/users`                                                |
| `users/$id.tsx`          | `/users/:id` (dynamic — bare `$`, no curly braces)      |
| `posts/{-$category}.tsx` | `/posts/:category?` (optional segment)                  |
| `files/$.tsx`            | `/files/*` (splat — read via `_splat` param, never `*`) |
| `_layout.tsx`            | layout route (renders children via `<Outlet />`)        |
| `__root.tsx`             | app shell — wraps every page; preserve `<Outlet />`     |

`routeTree.gen.ts` is auto-generated. Don't edit it by hand.

## Flux Panel route notes

| File                                | Path              | Notes                                                             |
| ----------------------------------- | ----------------- | ----------------------------------------------------------------- |
| `__root.tsx`                        | shell             | Renders `<HeadContent />` then `<Outlet />`; owns theme + toaster |
| `login.tsx`                         | `/login`          | Public; may set `panel_address`                                   |
| `_app.tsx`                          | layout            | Session + capability gate for child routes                        |
| `_app.index.tsx`                    | `/`               | Dashboard health from `loadDashboardSources`                      |
| `_app.forward.tsx`                  | `/forward`        | Full-list reorder only when filters are clear                     |
| `_app.user.tsx`                     | `/user`           | Admin; entity-scoped tunnel dialog via `useUserTunnels`           |
| `_app.node.tsx` / `_app.tunnel.tsx` | `/node` `/tunnel` | Admin mutations call `invalidateGlobalSearch`                     |
| `_app.settings.tsx`                 | `/settings`       | Local preferences (`pref_*`)                                      |

Route `head:` meta titles are applied by TanStack Router `HeadContent`. Do not hard-code document titles in page components.

Capability summary: `role_id === 0` is administrator. Non-admin users are limited to paths allowed by `canAccessPath` (forwards, subscription, profile, etc.). Backend authorization remains authoritative.
