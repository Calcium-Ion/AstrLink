import * as React from "react";

import { Eye, EyeOff } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

function Input({ type, ...props }: React.ComponentProps<"input">) {
  return type === "password" ? (
    <PasswordInput {...props} />
  ) : (
    <InputControl type={type} {...props} />
  );
}

function PasswordInput({
  className,
  id,
  disabled,
  ...props
}: Omit<React.ComponentProps<"input">, "type">) {
  const t = useT();
  const [visible, setVisible] = React.useState(false);
  const generatedId = React.useId();
  const inputId = id ?? generatedId;
  const toggleLabel = t(visible ? "common.hideSecret" : "common.showSecret");

  return (
    <span data-slot="password-input" className="relative block w-full min-w-0">
      <InputControl
        {...props}
        id={inputId}
        disabled={disabled}
        type={visible ? "text" : "password"}
        // WebView2 has its own reveal button; keep one shared control on every OS.
        className={cn(
          className,
          "pr-9 [&::-ms-reveal]:hidden [&::-ms-clear]:hidden",
        )}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        className="absolute top-1/2 right-0.5 -translate-y-1/2 text-muted-foreground"
        aria-label={toggleLabel}
        aria-controls={inputId}
        title={toggleLabel}
        disabled={disabled}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setVisible((current) => !current)}
      >
        {visible ? <EyeOff /> : <Eye />}
      </Button>
    </span>
  );
}

function InputControl({
  className,
  type,
  ...props
}: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "h-8 w-full min-w-0 rounded-md border border-input bg-card px-2.5 py-1 text-sm transition-[color,box-shadow] outline-none selection:bg-primary selection:text-primary-foreground file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-xs file:font-medium file:text-foreground placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-45",
        "focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/25",
        "aria-invalid:border-destructive aria-invalid:ring-destructive/20",
        className,
      )}
      {...props}
    />
  );
}

function InputDatalist({
  id,
  options,
}: {
  id: string;
  options: readonly string[];
}) {
  return (
    <datalist id={id}>
      {options.map((value) => (
        <option key={value} value={value} />
      ))}
    </datalist>
  );
}

export { Input, InputDatalist };
