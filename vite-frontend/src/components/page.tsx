import type { ReactNode } from "react";
import { PageHeader as Header, type PageHeaderProps } from "./patterns/page-header";
import { StatTile } from "./patterns/stat-tile";
import { ErrorState } from "./patterns/feedback";
export { FormField } from "./patterns/form-field";
/** 旧页面迁移期间保留参数兼容；英文眉标和装饰图标不再渲染。 */
export function PageHeader({
  eyebrow: _eyebrow,
  icon: _icon,
  ...props
}: PageHeaderProps & { eyebrow?: ReactNode; icon?: ReactNode }) {
  void _eyebrow;
  void _icon;
  return <Header {...props} />;
}
export function KpiCard({
  label,
  value,
  description,
}: {
  label: string;
  value: number | string;
  icon?: ReactNode;
  tone?: "ok" | "warn";
  description?: ReactNode;
}) {
  return <StatTile label={label} value={value} hint={description} />;
}
export function QueryErrorNotice({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return error ? <ErrorState error={error} onRetry={onRetry} /> : null;
}
