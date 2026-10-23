import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/components/patterns/form-field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { SpeedLimit } from "@/lib/types";
import { extendExpiry, type FieldErrors, type QuotaValues } from "./model";

export function QuotaFields({
  values,
  onChange,
  errors,
  prefix,
  limits,
  disabled = false,
}: {
  values: QuotaValues;
  onChange: (values: QuotaValues) => void;
  errors: FieldErrors;
  prefix: string;
  limits?: SpeedLimit[];
  disabled?: boolean;
}) {
  return (
    <>
      <FormField
        label="流量配额（GB）"
        htmlFor={`${prefix}-flow`}
        required
        error={errors.flow}
        hint="按 1024 进制计量；0 GB 将阻止新建或恢复转发"
      >
        <Input
          type="number"
          min={0}
          step={1}
          value={values.flow}
          disabled={disabled}
          onChange={(e) => onChange({ ...values, flow: e.target.value })}
        />
      </FormField>
      <FormField label="转发数量上限" htmlFor={`${prefix}-num`} required error={errors.num}>
        <Input
          type="number"
          min={0}
          step={1}
          value={values.num}
          disabled={disabled}
          onChange={(e) => onChange({ ...values, num: e.target.value })}
        />
      </FormField>
      <div className="space-y-2">
        <FormField
          label="到期日期"
          htmlFor={`${prefix}-expiry`}
          required
          error={errors.expiry}
          hint="选择新日期时于当天 23:59:59 失效；未改日期保留原到期时刻"
        >
          <Input
            type="date"
            value={values.expiry}
            disabled={disabled}
            onChange={(e) => onChange({ ...values, expiry: e.target.value })}
          />
        </FormField>
        <div className="flex flex-wrap gap-2" aria-label="延长到期日期">
          {[
            [1, "+1 月"],
            [3, "+3 月"],
            [12, "+1 年"],
          ].map(([months, label]) => (
            <Button
              key={months}
              type="button"
              size="sm"
              variant="outline"
              disabled={disabled}
              onClick={() =>
                onChange({ ...values, expiry: extendExpiry(values.expiry, Number(months)) })
              }
            >
              {label}
            </Button>
          ))}
        </div>
      </div>
      <FormField
        label="流量重置日"
        htmlFor={`${prefix}-reset`}
        required
        error={errors.reset}
        hint="每月 1–31 日重置流量，0 表示不重置"
      >
        <Input
          type="number"
          min={0}
          max={31}
          step={1}
          value={values.reset}
          disabled={disabled}
          onChange={(e) => onChange({ ...values, reset: e.target.value })}
        />
      </FormField>
      {limits && (
        <FormField
          label="限速规则"
          htmlFor={`${prefix}-speed`}
          hint="只列出当前隧道的规则；不选择时不限速"
        >
          <Select
            value={values.speedId}
            disabled={disabled}
            onValueChange={(speedId) => onChange({ ...values, speedId })}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">不限速</SelectItem>
              {limits.map((limit) => (
                <SelectItem key={limit.id} value={String(limit.id)}>
                  {limit.name} · {limit.speed} Mbps
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </FormField>
      )}
    </>
  );
}
