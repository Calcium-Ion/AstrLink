import { useId, type Ref } from "react";

import { useT } from "@/i18n";

import { Field } from "@/components/Field";
import { Input } from "@/components/ui/input";

import {
  newPasswordIssue,
  passwordIsShort,
  type RawSealingStatus,
} from "@/raw-sealing-model";

/** Fixed ids, so password managers and labels find the new password. */
export const NEW_PASSWORD_ID = "raw-new-password";
export const CONFIRM_PASSWORD_ID = "raw-confirm-password";

type PasswordPolicy = Pick<
  RawSealingStatus,
  "password_min_length" | "password_max_length"
>;

/**
 * A new raw password and its confirmation (D16). The values live in the
 * caller's dialog state, which clears them once they are submitted.
 */
export function RawPasswordFields({
  confirmation,
  disabled = false,
  inputRef,
  labels,
  onConfirmationChange,
  onPasswordChange,
  password,
  policy,
}: {
  confirmation: string;
  disabled?: boolean;
  /** The new-password input, for a dialog that focuses it on open. */
  inputRef?: Ref<HTMLInputElement>;
  /** Replaces the new-password labels where no current password is asked. */
  labels?: { password: string; confirmation: string };
  onConfirmationChange: (value: string) => void;
  onPasswordChange: (value: string) => void;
  password: string;
  policy: PasswordPolicy;
}) {
  const t = useT();
  const hintId = useId();
  const issue = newPasswordIssue(password, confirmation, policy);
  const tooLong = issue === "too_long";
  // A mismatch only matters once the confirmation is as long as the password.
  const mismatch =
    issue === "mismatch" && confirmation.length >= password.length;
  const problem = tooLong
    ? t("rawSealing.tooLong", { max: policy.password_max_length })
    : mismatch
      ? t("rawSealing.mismatch")
      : null;
  const rule = t("rawSealing.lengthRule", {
    min: policy.password_min_length,
    max: policy.password_max_length,
  });
  return (
    <div className="grid gap-3" data-slot="raw-password-fields">
      <Field
        htmlFor={NEW_PASSWORD_ID}
        label={labels?.password ?? t("rawSealing.newPassword")}
      >
        <Input
          aria-describedby={hintId}
          aria-invalid={tooLong || undefined}
          autoComplete="new-password"
          disabled={disabled}
          id={NEW_PASSWORD_ID}
          name="new-password"
          onChange={(event) => onPasswordChange(event.target.value)}
          ref={inputRef}
          type="password"
          value={password}
        />
      </Field>
      <Field
        htmlFor={CONFIRM_PASSWORD_ID}
        label={labels?.confirmation ?? t("rawSealing.confirmPassword")}
      >
        <Input
          aria-describedby={hintId}
          aria-invalid={mismatch || undefined}
          autoComplete="new-password"
          disabled={disabled}
          id={CONFIRM_PASSWORD_ID}
          name="confirm-password"
          onChange={(event) => onConfirmationChange(event.target.value)}
          type="password"
          value={confirmation}
        />
      </Field>
      <p
        className={
          problem
            ? "text-xs text-danger-foreground"
            : "text-xs text-muted-foreground"
        }
        data-slot="raw-password-hint"
        id={hintId}
      >
        {problem ??
          (passwordIsShort(password)
            ? `${rule} ${t("rawSealing.suggestLonger")}`
            : rule)}
      </p>
    </div>
  );
}
