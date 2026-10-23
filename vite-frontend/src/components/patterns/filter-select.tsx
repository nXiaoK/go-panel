import {
  Select,
  SelectTrigger,
  SelectContent,
  SelectItem,
  SelectValue,
} from "@/components/ui/select";
export function FilterSelect({
  value,
  onChange,
  options,
  label,
  placeholder = "全部",
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  options: readonly { value: string; label: string }[];
  label: string;
  placeholder?: string;
  disabled?: boolean;
}) {
  const empty = "__filter_all__";
  return (
    <Select
      value={value || empty}
      onValueChange={(next) => onChange(next === empty ? "" : next)}
      disabled={disabled}
    >
      <SelectTrigger aria-label={label} className="w-auto min-w-28">
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value || empty}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
