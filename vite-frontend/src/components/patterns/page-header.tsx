import type { ReactNode } from "react";
export type PageHeaderProps = {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumbs?: ReactNode;
};
export function PageHeader({ title, description, actions, breadcrumbs }: PageHeaderProps) {
  return (
    <header className="flex min-w-0 flex-wrap items-start justify-between gap-4">
      {" "}
      <div className="min-w-0">
        {breadcrumbs && (
          <nav aria-label="面包屑" className="mb-2 text-caption text-mute">
            {breadcrumbs}
          </nav>
        )}
        <h1 className="text-page-title text-ink">{title}</h1>
        {description && <div className="mt-1 text-sm leading-5 text-mute">{description}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
