import { cloneElement, isValidElement, useId, type ReactElement, type ReactNode } from "react";
import { Select } from "@/components/ui/select";
import { RadioGroup } from "@/components/ui/radio-group";
import { FieldControlContext, type FieldControl } from "@/components/ui/field-control";
export type FormFieldProps = {
  label: ReactNode;
  htmlFor?: string;
  required?: boolean;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactNode | ((control: FieldControl) => ReactNode);
};
export function FormField({ label, htmlFor, required, hint, error, children }: FormFieldProps) {
  const generated = useId();
  const element = isValidElement(children)
    ? (children as ReactElement<Record<string, unknown>>)
    : undefined;
  const existingId = typeof element?.props.id === "string" ? element.props.id : undefined;
  const id = htmlFor ?? existingId ?? `field-${generated}`;
  const isGroup = element?.type === RadioGroup;
  const LabelTag = isGroup ? "div" : "label";
  const isControl =
    element &&
    (typeof element.type === "function" ||
      typeof element.type === "object" ||
      (typeof element.type === "string" &&
        ["input", "select", "textarea", "button"].includes(element.type)));
  const description = error || hint;
  const control: FieldControl = {
    id,
    "aria-labelledby": `${id}-label`,
    "aria-describedby":
      [element?.props["aria-describedby"], description ? `${id}-description` : undefined]
        .filter(Boolean)
        .join(" ") || undefined,
    "aria-invalid": error ? true : undefined,
    "aria-required": required || undefined,
  };
  const child =
    typeof children === "function"
      ? children(control)
      : isControl && element.type !== Select
        ? cloneElement(element, control)
        : children;
  return (
    <FieldControlContext.Provider value={element?.type === Select ? control : undefined}>
      <div className="space-y-1.5">
        <LabelTag
          id={`${id}-label`}
          htmlFor={isGroup ? undefined : id}
          className="block text-caption-strong text-body"
        >
          {label}
          {required && (
            <>
              <span aria-hidden="true" className="ml-1 text-status-critical-text">
                *
              </span>
              <span className="sr-only">（必填）</span>
            </>
          )}
        </LabelTag>
        {child}
        {description && (
          <div
            id={`${id}-description`}
            className={error ? "text-caption text-status-critical-text" : "text-caption text-mute"}
          >
            {description}
          </div>
        )}
      </div>
    </FieldControlContext.Provider>
  );
}
