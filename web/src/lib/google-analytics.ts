export const googleAnalyticsMeasurementIDPattern = /^G-[A-Z0-9]{4,22}$/;

export function normalizeGoogleAnalyticsMeasurementID(value: string | undefined) {
  const normalized = (value || "").trim().toUpperCase();
  return googleAnalyticsMeasurementIDPattern.test(normalized) ? normalized : "";
}

export function isGoogleAnalyticsPathAllowed(pathname: string | null | undefined) {
  const normalizedPath = (pathname || "/").replace(/\/+$/, "") || "/";
  return normalizedPath === "/login" || normalizedPath === "/admin" || normalizedPath.startsWith("/admin/");
}

export function googleAnalyticsPageLocation(origin: string, pathname: string) {
  const normalizedPath = pathname.startsWith("/") ? pathname : "/";
  return `${origin}${normalizedPath}`;
}
