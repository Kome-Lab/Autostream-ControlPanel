export const defaultTimeZone = "Asia/Tokyo";

export const timeZoneOptions = [
  { value: "Asia/Tokyo", label: "Japan (Asia/Tokyo)" },
  { value: "UTC", label: "UTC" },
  { value: "America/Los_Angeles", label: "Pacific Time (America/Los_Angeles)" },
  { value: "America/New_York", label: "Eastern Time (America/New_York)" },
  { value: "Europe/London", label: "London (Europe/London)" },
  { value: "Europe/Paris", label: "Paris (Europe/Paris)" },
  { value: "Asia/Singapore", label: "Singapore (Asia/Singapore)" },
  { value: "Australia/Sydney", label: "Sydney (Australia/Sydney)" },
] as const;

export function normalizeTimeZone(timeZone?: string) {
  const value = (timeZone || defaultTimeZone).trim() || defaultTimeZone;
  try {
    new Intl.DateTimeFormat("ja-JP", { timeZone: value }).format(new Date(0));
    return value;
  } catch {
    return defaultTimeZone;
  }
}

export function formatDateTimeInTimeZone(value?: string, timeZone?: string, options?: Intl.DateTimeFormatOptions) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("ja-JP", { ...options, timeZone: normalizeTimeZone(timeZone) }).format(date);
}

export function formatTimeInTimeZone(value?: string, timeZone?: string) {
  if (!value) return "--:--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--:--";
  return new Intl.DateTimeFormat("ja-JP", { hour: "2-digit", minute: "2-digit", timeZone: normalizeTimeZone(timeZone) }).format(date);
}
