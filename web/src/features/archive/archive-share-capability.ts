import type { OneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/contracts";

export type ArchiveShareCapability = Readonly<{
  url: string;
  expiresAt: string;
  allowDownload: boolean;
}>;

export type ArchiveSharePublicResult = Readonly<{
  id?: string;
  expiresAt: string;
  allowDownload: boolean;
}>;

export function adoptArchiveShareCapability(
  owner: OneTimeSecretLifecycleOwner<ArchiveShareCapability>,
  response: unknown,
  expectedOrigin: string,
) {
  owner.dismiss();
  if (!plainRecord(response)) return emptyResult;
  const expiresAt = boundedString(safeOwn(response, "expires_at"), 128);
  const allowDownload = safeOwn(response, "allow_download");
  const id = boundedIdentifier(safeOwn(response, "id"));
  const origin = safeOrigin(expectedOrigin);
  if (!expiresAt || typeof allowDownload !== "boolean" || !origin) return emptyResult;
  const expiresAtEpochMs = Date.parse(expiresAt);
  if (!Number.isSafeInteger(expiresAtEpochMs) || expiresAtEpochMs < 0) return emptyResult;
  const url = capabilityURL(response, origin);
  if (!url) return emptyResult;
  const publicResult = Object.freeze({
    ...(id ? { id } : {}),
    expiresAt,
    allowDownload,
  });
  const adopted = owner.replace({
    value: Object.freeze({ url, expiresAt, allowDownload }),
    backendExpiresAtEpochMs: expiresAtEpochMs,
    initialVisibility: "concealed",
  });
  return Object.freeze({ adopted, publicResult });
}

const emptyResult = Object.freeze({
  adopted: false,
  publicResult: Object.freeze({ expiresAt: "", allowDownload: false }),
});

function capabilityURL(response: Record<string, unknown>, origin: string) {
  const direct = boundedString(safeOwn(response, "url"), 8_192);
  const token = boundedString(safeOwn(response, "token"), 4_096);
  let candidate: URL;
  try {
    candidate = direct
      ? new URL(direct)
      : token
        ? new URL(`/archive/share/?token=${encodeURIComponent(token)}`, origin)
        : new URL("about:blank");
  } catch {
    return undefined;
  }
  if (candidate.origin !== origin || candidate.pathname !== "/archive/share/" || !candidate.searchParams.has("token")) return undefined;
  return candidate.toString();
}

function safeOrigin(value: string) {
  try {
    const parsed = new URL(value);
    return (parsed.protocol === "https:" || parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1") && parsed.origin === value
      ? parsed.origin
      : undefined;
  } catch {
    return undefined;
  }
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
  return typeof value === "string" && value.length > 0 && value.length <= maximum && !/[\u0000-\u001f\u007f]/u.test(value) ? value : undefined;
}

function boundedIdentifier(value: unknown) {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value) ? value : undefined;
}
