const defaultPostLoginPath = "/admin/";
const validationOrigin = "https://autostream.invalid";
const forbiddenRedirectCharacters = /[\u0000-\u001f\u007f-\u009f\\]/;

type LocationParts = {
  pathname: string;
  search?: string;
  hash?: string;
};

export function safePostLoginPath(value: string | null | undefined) {
  const candidate = String(value || "").trim();
  if (!candidate.startsWith("/") || candidate.startsWith("//") || forbiddenRedirectCharacters.test(candidate)) {
    return defaultPostLoginPath;
  }

  try {
    if (forbiddenRedirectCharacters.test(decodeURIComponent(candidate))) return defaultPostLoginPath;
    const parsed = new URL(candidate, validationOrigin);
    if (parsed.origin !== validationOrigin || (parsed.pathname !== "/admin" && !parsed.pathname.startsWith("/admin/"))) {
      return defaultPostLoginPath;
    }
    return `${parsed.pathname}${parsed.search}${parsed.hash}`;
  } catch {
    return defaultPostLoginPath;
  }
}

export function loginPathForLocation(location: LocationParts, sessionExpired = false) {
  const redirectAfter = safePostLoginPath(`${location.pathname}${location.search || ""}${location.hash || ""}`);
  const params = new URLSearchParams({ redirect_after: redirectAfter });
  if (sessionExpired) params.set("reason", "session_expired");
  return `/login?${params.toString()}`;
}
