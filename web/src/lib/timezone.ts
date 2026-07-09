export const defaultTimeZone = "Asia/Tokyo";

const priorityTimeZones = [
  "Asia/Tokyo",
  "UTC",
  "America/Los_Angeles",
  "America/New_York",
  "Europe/London",
  "Europe/Paris",
  "Asia/Singapore",
  "Australia/Sydney",
] as const;

const fallbackTimeZones = [
  ...priorityTimeZones,
  "Africa/Cairo",
  "Africa/Casablanca",
  "Africa/Johannesburg",
  "Africa/Lagos",
  "Africa/Nairobi",
  "America/Anchorage",
  "America/Argentina/Buenos_Aires",
  "America/Bogota",
  "America/Caracas",
  "America/Detroit",
  "America/Halifax",
  "America/Lima",
  "America/Santiago",
  "America/St_Johns",
  "America/Winnipeg",
  "Asia/Seoul",
  "Asia/Shanghai",
  "Asia/Taipei",
  "Asia/Hong_Kong",
  "Asia/Bangkok",
  "Asia/Ho_Chi_Minh",
  "Asia/Jakarta",
  "Asia/Kuala_Lumpur",
  "Asia/Manila",
  "Asia/Kolkata",
  "Asia/Kathmandu",
  "Asia/Karachi",
  "Asia/Dubai",
  "Asia/Riyadh",
  "Asia/Jerusalem",
  "Asia/Tehran",
  "Europe/Athens",
  "Europe/Berlin",
  "Europe/Brussels",
  "Europe/Bucharest",
  "Europe/Dublin",
  "Europe/Helsinki",
  "Europe/Istanbul",
  "Europe/Lisbon",
  "Europe/Madrid",
  "Europe/Rome",
  "Europe/Amsterdam",
  "Europe/Oslo",
  "Europe/Prague",
  "Europe/Stockholm",
  "Europe/Warsaw",
  "Europe/Zurich",
  "Europe/Moscow",
  "America/Chicago",
  "America/Denver",
  "America/Phoenix",
  "America/Toronto",
  "America/Vancouver",
  "America/Sao_Paulo",
  "America/Mexico_City",
  "Australia/Adelaide",
  "Australia/Brisbane",
  "Australia/Darwin",
  "Australia/Melbourne",
  "Australia/Perth",
  "Pacific/Auckland",
  "Pacific/Fiji",
  "Pacific/Guam",
  "Pacific/Honolulu",
  "Pacific/Pago_Pago",
  "Pacific/Tahiti",
] as const;

export const timeZoneOptions = buildTimeZoneOptions();

function buildTimeZoneOptions() {
  const supported = supportedTimeZones();
  const values = uniqueStrings([...priorityTimeZones, ...supported]);
  return values.map((value) => ({ value, label: timeZoneLabel(value) }));
}

function supportedTimeZones() {
  const intlWithSupportedValues = Intl as typeof Intl & { supportedValuesOf?: (key: "timeZone") => string[] };
  try {
    const values = intlWithSupportedValues.supportedValuesOf?.("timeZone");
    if (values && values.length > 0) return values;
  } catch {
    // Older runtimes do not expose Intl.supportedValuesOf.
  }
  return [...fallbackTimeZones];
}

function uniqueStrings(values: readonly string[]) {
  const seen = new Set<string>();
  return values.filter((value) => {
    const normalized = value.trim();
    if (!normalized || seen.has(normalized)) return false;
    seen.add(normalized);
    return true;
  });
}

export function timeZoneLabel(value: string) {
  const city = value.split("/").pop()?.replace(/_/g, " ") || value;
  if (value === "UTC") return "UTC";
  return `${city} (${value})`;
}

export function isValidTimeZone(timeZone?: string) {
  const value = (timeZone || "").trim();
  if (!value) return false;
  try {
    new Intl.DateTimeFormat("ja-JP", { timeZone: value }).format(new Date(0));
    return true;
  } catch {
    return false;
  }
}

export function normalizeTimeZone(timeZone?: string) {
  const value = (timeZone || defaultTimeZone).trim() || defaultTimeZone;
  return isValidTimeZone(value) ? value : defaultTimeZone;
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
