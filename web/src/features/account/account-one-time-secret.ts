import type { OneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/contracts";

export type AccountOneTimeSecretValue = Readonly<{
  mfaSecret?: string;
  provisioningURI?: string;
  recoveryCodes?: readonly string[];
}>;

export type AccountOneTimePublicResult = Readonly<{
  method?: string;
  enrollmentPending: boolean;
  recoveryCodeCount: number;
}>;

export function adoptAccountOneTimeOutput(
  owner: OneTimeSecretLifecycleOwner<AccountOneTimeSecretValue>,
  response: unknown,
) {
  owner.dismiss();
  if (!plainRecord(response)) return emptyResult;
  const method = boundedString(safeOwn(response, "method"), 32);
  const secret = optionalSecret(safeOwn(response, "secret"));
  const provisioningURI = optionalSecret(safeOwn(response, "provisioning_uri"));
  const recoveryCodes = optionalRecoveryCodes(safeOwn(response, "recovery_codes"));
  if (secret.invalid || provisioningURI.invalid || recoveryCodes.invalid) return emptyResult;
  if (!secret.value && !provisioningURI.value && !recoveryCodes.value?.length) return emptyResult;
  const publicResult = Object.freeze({
    ...(method ? { method } : {}),
    enrollmentPending: Boolean(secret.value || provisioningURI.value),
    recoveryCodeCount: recoveryCodes.value?.length ?? 0,
  });
  const adopted = owner.replace({
    value: Object.freeze({
      ...(secret.value ? { mfaSecret: secret.value } : {}),
      ...(provisioningURI.value ? { provisioningURI: provisioningURI.value } : {}),
      ...(recoveryCodes.value ? { recoveryCodes: recoveryCodes.value } : {}),
    }),
    initialVisibility: "concealed",
  });
  return Object.freeze({ adopted, publicResult });
}

const emptyResult = Object.freeze({
  adopted: false,
  publicResult: Object.freeze({ enrollmentPending: false, recoveryCodeCount: 0 }),
});

function optionalSecret(value: unknown) {
  if (value === undefined || value === "") return Object.freeze({ invalid: false, value: undefined });
  return typeof value === "string" && value.length <= 16_384 && !/[\u0000\r\n]/u.test(value)
    ? Object.freeze({ invalid: false, value })
    : Object.freeze({ invalid: true, value: undefined });
}

function optionalRecoveryCodes(value: unknown) {
  if (value === undefined) return Object.freeze({ invalid: false, value: undefined });
  if (!Array.isArray(value) || value.length === 0 || value.length > 64) return Object.freeze({ invalid: true, value: undefined });
  const copy: string[] = [];
  for (const entry of value) {
    if (typeof entry !== "string" || entry.length === 0 || entry.length > 256 || /[\u0000\r\n]/u.test(entry) || copy.includes(entry)) {
      return Object.freeze({ invalid: true, value: undefined });
    }
    copy.push(entry);
  }
  return Object.freeze({ invalid: false, value: Object.freeze(copy) });
}

function plainRecord(value: unknown): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function safeOwn(value: Record<string, unknown>, key: string) {
  try {
    return Object.prototype.hasOwnProperty.call(value, key) ? Reflect.get(value, key) : undefined;
  } catch {
    return undefined;
  }
}

function boundedString(value: unknown, maximum: number) {
  return typeof value === "string" && value.length > 0 && value.length <= maximum ? value : undefined;
}
