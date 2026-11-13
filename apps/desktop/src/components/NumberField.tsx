import { useId, useState, type ReactNode } from "react";

import { Field } from "@/components/Field";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * A numeric input with a persistent unit and native range validation, for
 * rows that label it themselves; NumberField adds the label above it.
 */
export function NumberInput({
  id,
  label,
  describedBy,
  unit,
  value,
  min,
  max,
  step = 1,
  disabled,
  invalid,
  className,
  onChange,
}: {
  id?: string;
  label: string;
  describedBy?: string;
  unit: string;
  value: number;
  min: number;
  max: number;
  step?: number | "any";
  disabled?: boolean;
  /** Marks the value invalid for a check the caller makes itself. */
  invalid?: boolean;
  className?: string;
  onChange: (value: number) => void;
}) {
  const unitId = `${useId()}-unit`;
  const [input, setInput] = useState<string | null>(null);

  return (
    <span className="relative block">
      <Input
        id={id}
        aria-label={label}
        aria-describedby={describedBy ? `${unitId} ${describedBy}` : unitId}
        aria-invalid={invalid || undefined}
        className={cn(
          "h-9 pr-12 tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none",
          className,
        )}
        type="number"
        inputMode={step === 1 ? "numeric" : "decimal"}
        required
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        value={input ?? value}
        onChange={(event) => {
          setInput(event.currentTarget.value);
          const next = event.currentTarget.valueAsNumber;
          if (Number.isFinite(next)) onChange(next);
        }}
        onBlur={(event) => {
          if (event.currentTarget.validity.valid) setInput(null);
        }}
      />
      <span
        id={unitId}
        className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs font-normal text-muted-foreground"
      >
        {unit}
      </span>
    </span>
  );
}

/** A numeric field with a persistent unit and native range validation. */
export function NumberField({
  label,
  hint,
  ...input
}: {
  label: string;
  hint?: ReactNode;
} & Omit<Parameters<typeof NumberInput>[0], "id" | "label" | "describedBy">) {
  const id = useId();

  return (
    <Field
      label={label}
      htmlFor={id}
      hint={hint ? <span id={`${id}-hint`}>{hint}</span> : undefined}
    >
      <NumberInput
        id={id}
        label={label}
        describedBy={hint ? `${id}-hint` : undefined}
        {...input}
      />
    </Field>
  );
}
