export const googleAnalyticsMeasurementIDPattern = /^G-[A-Z0-9]{4,22}$/;

export function normalizeGoogleAnalyticsMeasurementID(value: string | undefined) {
  const normalized = (value || "").trim().toUpperCase();
  return googleAnalyticsMeasurementIDPattern.test(normalized) ? normalized : "";
}

export function googleAnalyticsPageLocation(origin: string, pathname: string) {
  const normalizedPath = pathname.startsWith("/") ? pathname : "/";
  return `${origin}${normalizedPath}`;
}
