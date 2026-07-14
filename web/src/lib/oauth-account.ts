type OAuthAccountView = Record<string, unknown>;

export type OAuthAccountPurpose = "drive" | "youtube";
export type OAuthAccountPurposeValue = OAuthAccountPurpose | "drive_youtube" | "unknown";

export function oauthAccountDisplayName(account: OAuthAccountView) {
  const email = stringField(account, "email").toLowerCase();
  const providerType = stringField(account, "provider_type");
  const providerName = stringField(account, "provider_name");

  for (const key of ["account_label", "display_name"] as const) {
    const value = stringField(account, key);
    if (usableOAuthAccountLabel(value, email, providerType, providerName)) return value;
  }
  const base = providerName && !genericOAuthProviderName(providerName, providerType)
    ? providerName
    : `${oauthProviderTypeLabel(providerType)}アカウント`;
  const reference = accountReference(account);
  return reference ? `${base} (${reference})` : `${base} (表示名未設定)`;
}

export function oauthAccountConfiguredName(account: OAuthAccountView) {
  const email = stringField(account, "email").toLowerCase();
  const providerType = stringField(account, "provider_type");
  const providerName = stringField(account, "provider_name");
  const accountLabel = stringField(account, "account_label");
  return usableOAuthAccountLabel(accountLabel, email, providerType, providerName) ? accountLabel : "";
}

export function oauthProviderTypeLabel(providerType: string) {
  switch (providerType.trim().toLowerCase()) {
    case "google":
      return "Google";
    case "github":
      return "GitHub";
    case "discord":
      return "Discord";
    default:
      return providerType.trim() || "OAuth";
  }
}

export function oauthAccountPurpose(account: OAuthAccountView): OAuthAccountPurposeValue {
  const scopes = stringListField(account, "scopes");
  if (scopes.length > 0) return purposeFromScopes(scopes);

  const explicit = stringField(account, "account_purpose").toLowerCase();
  if (explicit === "drive" || explicit === "youtube" || explicit === "drive_youtube" || explicit === "unknown") return explicit;
  return "unknown";
}

export function oauthAccountSupportsPurpose(account: OAuthAccountView, purpose: OAuthAccountPurpose) {
  const connectedPurpose = oauthAccountPurpose(account);
  return connectedPurpose === purpose || connectedPurpose === "drive_youtube";
}

export function oauthAccountPurposeLabel(account: OAuthAccountView) {
  switch (oauthAccountPurpose(account)) {
    case "drive":
      return "Drive保存";
    case "youtube":
      return "YouTube Live";
    case "drive_youtube":
      return "YouTube Live・Drive保存";
    default:
      return "用途未判定";
  }
}

function usableOAuthAccountLabel(value: string, email: string, providerType: string, providerName: string) {
  const label = value.trim();
  if (!label) return false;
  const normalized = label.toLowerCase();
  if (email && normalized === email) return false;
  return !generatedOAuthAccountLabel(normalized, providerType, providerName);
}

function generatedOAuthAccountLabel(normalizedLabel: string, providerType: string, providerName: string) {
  const compactLabel = compact(normalizedLabel);
  const bases = [oauthProviderTypeLabel(providerType), providerName].map((value) => value.trim()).filter(Boolean);
  return bases.some((base) => compactLabel === compact(base) || compactLabel === compact(`${base} 接続アカウント`) || compactLabel === compact(`${base} connected account`));
}

function genericOAuthProviderName(providerName: string, providerType: string) {
  const normalizedName = compact(providerName);
  const base = oauthProviderTypeLabel(providerType);
  return normalizedName === compact(base) || normalizedName === compact(`${base} 接続アカウント`) || normalizedName === compact(`${base} connected account`);
}

function compact(value: string) {
  return value.trim().toLowerCase().replace(/\s+/g, "");
}

function accountReference(account: OAuthAccountView) {
  return stringField(account, "id").replace(/-/g, "").slice(0, 8);
}

function purposeFromScopes(scopes: string[]): OAuthAccountPurposeValue {
  const normalized = new Set(scopes.map((scope) => scope.trim()));
  const drive = normalized.has("https://www.googleapis.com/auth/drive.file") || normalized.has("https://www.googleapis.com/auth/drive");
  const youtube = normalized.has("https://www.googleapis.com/auth/youtube") || normalized.has("https://www.googleapis.com/auth/youtube.force-ssl") || normalized.has("https://www.googleapis.com/auth/youtube.upload");
  if (drive && youtube) return "drive_youtube";
  if (drive) return "drive";
  if (youtube) return "youtube";
  return "unknown";
}

function stringListField(account: OAuthAccountView, key: string) {
  const value = account[key];
  if (Array.isArray(value)) return value.map((item) => String(item).trim()).filter(Boolean);
  if (typeof value !== "string" || value.trim() === "") return [];
  try {
    const parsed = JSON.parse(value);
    if (Array.isArray(parsed)) return parsed.map((item) => String(item).trim()).filter(Boolean);
  } catch {
    // Older API responses may expose a space- or comma-delimited scope string.
  }
  return value.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean);
}

function stringField(account: OAuthAccountView, key: string) {
  const value = account[key];
  if (typeof value === "string") return value.trim();
  if (typeof value === "number") return String(value);
  return "";
}
