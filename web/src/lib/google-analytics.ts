export const googleAnalyticsMeasurementIDPattern = /^G-[A-Z0-9]{4,22}$/;

export type GoogleTagCommand = (...args: unknown[]) => void;

export function createGoogleTagCommandQueue(dataLayer: unknown[]): GoogleTagCommand {
  // gtag.js expects the Arguments object used by Google's official snippet.
  // A normal Array looks similar in DevTools but is not processed as a command.
  return function gtag() {
    // eslint-disable-next-line prefer-rest-params -- gtag.js requires Arguments, not a rest-parameter Array.
    dataLayer.push(arguments);
  };
}

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

export function shouldSendGoogleAnalyticsPageView(pageViewKey: string, lastPageViewKey: string, queuedPageViewKey: string | undefined) {
  return Boolean(pageViewKey) && lastPageViewKey !== pageViewKey && queuedPageViewKey !== pageViewKey;
}
