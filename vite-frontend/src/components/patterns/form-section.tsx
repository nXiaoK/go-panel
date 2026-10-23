import type { ReactNode } from "react";
export function FormSection({
  title,
  description,
  children,
}: {
  title: string;
  description?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4">
      <div>
        <h2 className="text-section-title text-ink">{title}</h2>
        {description && <div className="mt-1 text-caption text-mute">{description}</div>}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  );
}
